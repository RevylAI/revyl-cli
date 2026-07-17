package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

func TestReadBuildSecretFromReaderPreservesValueAndRemovesLineEnding(t *testing.T) {
	value, err := readBuildSecretFromReader(strings.NewReader("  secret value  \r\n"))
	if err != nil {
		t.Fatalf("readBuildSecretFromReader() error = %v", err)
	}
	if value != "  secret value  " {
		t.Fatalf("readBuildSecretFromReader() = %q, want spaces preserved", value)
	}
}

func TestRunBuildSecretListNeverPrintsValues(t *testing.T) {
	const secretValue = "must-not-appear-in-output"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/variables/org_launch_env" {
			t.Fatalf("path = %q, want launch variable endpoint", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.OrgLaunchVariablesResponse{
			Result: []api.OrgLaunchVariable{{
				ID:          "secret-id",
				Key:         "EXPO_TOKEN",
				Value:       secretValue,
				Description: "Expo build credential",
				UpdatedAt:   "2026-07-09T12:00:00Z",
			}},
		})
	}))
	t.Cleanup(server.Close)

	previousSetup := buildSecretSetupClient
	buildSecretSetupClient = func(cmd *cobra.Command) (*api.Client, error) {
		return api.NewClientWithBaseURL("test-key", server.URL), nil
	}
	t.Cleanup(func() {
		buildSecretSetupClient = previousSetup
	})

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	output := captureStdoutAndStderr(t, func() {
		if err := runBuildSecretList(cmd, nil); err != nil {
			t.Fatalf("runBuildSecretList() error = %v", err)
		}
	})
	if !strings.Contains(output, "EXPO_TOKEN") {
		t.Fatalf("output = %q, want secret name", output)
	}
	if strings.Contains(output, secretValue) {
		t.Fatalf("output leaked secret value: %q", output)
	}
}

func TestRunBuildSecretSetReadsValueFromStdinWithoutPrintingIt(t *testing.T) {
	const secretValue = "stdin-only-secret"
	type createRequest struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	var request createRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.OrgLaunchVariablesResponse{})
		case r.Method == http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(api.OrgLaunchVariableResponse{
				Result: api.OrgLaunchVariable{
					ID:    "secret-id",
					Key:   request.Key,
					Value: request.Value,
				},
			})
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	t.Cleanup(server.Close)

	previousSetup := buildSecretSetupClient
	previousStdin := buildSecretSetFromStdin
	buildSecretSetupClient = func(cmd *cobra.Command) (*api.Client, error) {
		return api.NewClientWithBaseURL("test-key", server.URL), nil
	}
	buildSecretSetFromStdin = true
	t.Cleanup(func() {
		buildSecretSetupClient = previousSetup
		buildSecretSetFromStdin = previousStdin
	})

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var output string
	withStdin(t, secretValue+"\n", func() {
		output = captureStdoutAndStderr(t, func() {
			if err := runBuildSecretSet(cmd, []string{"EXPO_TOKEN"}); err != nil {
				t.Fatalf("runBuildSecretSet() error = %v", err)
			}
		})
	})

	if request.Key != "EXPO_TOKEN" || request.Value != secretValue {
		t.Fatalf("create request = %#v, want EXPO_TOKEN and stdin value", request)
	}
	if strings.Contains(output, secretValue) {
		t.Fatalf("output leaked secret value: %q", output)
	}
}

func TestValidateLocalBuildSecretsUsesProcessEnvironment(t *testing.T) {
	previousRefs := buildSecretRefFlags
	buildSecretRefFlags = []string{"CLI_TOKEN"}
	t.Cleanup(func() {
		buildSecretRefFlags = previousRefs
	})
	t.Setenv("CONFIG_TOKEN", "config-value")
	t.Setenv("CLI_TOKEN", "cli-value")

	err := validateLocalBuildSecrets("ios", config.BuildPlatform{
		Secrets: []string{"CONFIG_TOKEN"},
	})
	if err != nil {
		t.Fatalf("validateLocalBuildSecrets() error = %v", err)
	}
}

func TestValidateLocalBuildSecretsRejectsMissingAndPlaintextCollisions(t *testing.T) {
	previousRefs := buildSecretRefFlags
	buildSecretRefFlags = nil
	t.Cleanup(func() {
		buildSecretRefFlags = previousRefs
	})

	err := validateLocalBuildSecrets("ios", config.BuildPlatform{
		Secrets: []string{"REVYL_TEST_MISSING_BUILD_SECRET"},
	})
	if err == nil || !strings.Contains(err.Error(), "REVYL_TEST_MISSING_BUILD_SECRET") {
		t.Fatalf("missing secret error = %v", err)
	}

	err = validateLocalBuildSecrets("ios", config.BuildPlatform{
		Env:     map[string]string{"EXPO_TOKEN": "plaintext"},
		Secrets: []string{"EXPO_TOKEN"},
	})
	if err == nil || !strings.Contains(err.Error(), "both plaintext env and encrypted secrets") {
		t.Fatalf("collision error = %v", err)
	}
}
