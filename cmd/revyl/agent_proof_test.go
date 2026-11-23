package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

const (
	agentProofTestAppID   = "11111111-1111-4111-8111-111111111111"
	agentProofTestBuildID = "22222222-2222-4222-8222-222222222222"
	agentProofTestRunID   = "0b9f2c1e-7a44-4a7e-9d53-1c2e8f6a5b10"
)

type fakeAgentProofAPI struct {
	created   *api.ProofRun
	polled    *api.ProofRun
	cancelled *api.ProofRun
	cancelErr error
}

func (f *fakeAgentProofAPI) GetApp(_ context.Context, id string) (*api.App, error) {
	return &api.App{ID: id, Name: "Shop", Platform: "android"}, nil
}

func (f *fakeAgentProofAPI) SearchApps(context.Context, string, string, int) (*api.CLIPaginatedAppsResponse, error) {
	return &api.CLIPaginatedAppsResponse{}, nil
}

func (f *fakeAgentProofAPI) GetBuildVersionDetail(context.Context, string, bool) (*api.BuildVersionDetail, error) {
	return &api.BuildVersionDetail{ID: agentProofTestBuildID, AppID: agentProofTestAppID}, nil
}

func (f *fakeAgentProofAPI) ResolveBuildSelectionMetadata(context.Context, string, api.BuildSelection) (*api.BuildVersionDetail, error) {
	return &api.BuildVersionDetail{ID: agentProofTestBuildID, AppID: agentProofTestAppID}, nil
}

func (f *fakeAgentProofAPI) CreateProofRun(context.Context, *api.ProofRunCreateRequest) (*api.ProofRun, error) {
	return f.created, nil
}

func (f *fakeAgentProofAPI) GetProofRun(context.Context, string) (*api.ProofRun, error) {
	return f.polled, nil
}

func (f *fakeAgentProofAPI) CancelProofRun(context.Context, string) (*api.ProofRun, error) {
	return f.cancelled, f.cancelErr
}

func proofRunForTest(status api.ProofRunStatus) *api.ProofRun {
	return &api.ProofRun{
		Id:        uuid.MustParse(agentProofTestRunID),
		Status:    status,
		AppId:     uuid.MustParse(agentProofTestAppID),
		BuildId:   uuid.MustParse(agentProofTestBuildID),
		CreatedAt: time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC),
	}
}

func runAgentCommandForTest(t *testing.T, client agentProofAPI, args ...string) (string, error) {
	t.Helper()
	originalClient, originalInterval, originalSignals := agentProofNewClient, agentProofPollInterval, agentProofSignals
	t.Cleanup(func() {
		agentProofNewClient, agentProofPollInterval, agentProofSignals = originalClient, originalInterval, originalSignals
	})
	agentProofNewClient = func(string, bool) agentProofAPI { return client }
	agentProofPollInterval = time.Nanosecond
	agentProofSignals = func() (<-chan os.Signal, func()) { return make(chan os.Signal), func() {} }
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())

	root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().Bool("json", false, "")
	root.PersistentFlags().BoolP("quiet", "q", false, "")
	root.PersistentFlags().Bool("dev", false, "")
	root.AddCommand(newAgentCommand())
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(args)
	err := root.Execute()
	return stdout.String(), err
}

func agentProofExitCode(err error) int {
	var exitErr *commandExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exitErr):
		return exitErr.code
	default:
		return 1
	}
}

func runProofArgs(extra ...string) []string {
	return append([]string{"agent", "proof", agentProofTestAppID, "--prompt", "verify the checkout change", "--build-id", agentProofTestBuildID}, extra...)
}

func TestAgentProofExitCodeFollowsTheOutcome(t *testing.T) {
	tests := []struct {
		status   api.ProofRunStatus
		wantExit int
	}{
		{status: api.ProofRunStatusPassed, wantExit: 0},
		{status: api.ProofRunStatusFailed, wantExit: 1},
		{status: api.ProofRunStatusInconclusive, wantExit: 2},
	}
	for _, test := range tests {
		t.Run(string(test.status), func(t *testing.T) {
			fake := &fakeAgentProofAPI{created: proofRunForTest(api.ProofRunStatusQueued), polled: proofRunForTest(test.status)}

			_, err := runAgentCommandForTest(t, fake, runProofArgs("--json")...)

			if got := agentProofExitCode(err); got != test.wantExit {
				t.Fatalf("exit code = %d, want %d (err %v)", got, test.wantExit, err)
			}
		})
	}
}

func TestAgentProofJSONEmitsTheDocumentedKeys(t *testing.T) {
	wantKeys := []string{
		"app_id", "build_id", "completed_at", "created_at", "failure_kind", "failure_reason", "kind", "message_markdown",
		"platform", "run_id", "status", "success", "url", "verification_score",
	}
	fake := &fakeAgentProofAPI{created: proofRunForTest(api.ProofRunStatusQueued)}

	stdout, err := runAgentCommandForTest(t, fake, runProofArgs("--no-wait", "--json")...)

	if err != nil {
		t.Fatalf("agent proof --no-wait --json: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, stdout)
	}
	keys := make([]string, 0, len(decoded))
	for key := range decoded {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("keys = %v, want exactly %v", keys, wantKeys)
	}
}

func TestReadAgentProofPromptRequiresExactlyOneNonEmptyPrompt(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "inline prompt", args: []string{"--prompt", "verify checkout"}},
		{name: "both flags", args: []string{"--prompt", "a", "--prompt-file", "-"}, wantErr: "exactly one of --prompt or --prompt-file is required"},
		{name: "neither flag", wantErr: "exactly one of --prompt or --prompt-file is required"},
		{name: "blank prompt", args: []string{"--prompt", " \n\t"}, wantErr: "prompt cannot be empty"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := newAgentProofCommand()
			cmd.SetIn(strings.NewReader(""))
			cmd.SetErr(&bytes.Buffer{})
			if err := cmd.ParseFlags(test.args); err != nil {
				t.Fatal(err)
			}

			_, err := readAgentProofPrompt(cmd)

			if test.wantErr == "" && err != nil {
				t.Fatalf("readAgentProofPrompt(): %v", err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("error = %v, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

func TestAgentCancelExitCode(t *testing.T) {
	tests := []struct {
		name     string
		fake     *fakeAgentProofAPI
		wantExit int
	}{
		{name: "run returned", fake: &fakeAgentProofAPI{cancelled: proofRunForTest(api.ProofRunStatusCancelled)}, wantExit: 0},
		{name: "pull request run", fake: &fakeAgentProofAPI{cancelErr: &api.APIError{StatusCode: http.StatusConflict, Detail: "A pull request's proof can't be cancelled"}}, wantExit: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := runAgentCommandForTest(t, test.fake, "agent", "cancel", agentProofTestRunID, "--json")

			if got := agentProofExitCode(err); got != test.wantExit {
				t.Fatalf("exit code = %d, want %d (err %v)", got, test.wantExit, err)
			}
		})
	}
}
