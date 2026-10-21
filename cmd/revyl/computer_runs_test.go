package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/testutil"
)

func TestComputerCommandLifecycleFactsAndPrivacy(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	id, requestID := uuid.New(), uuid.New()
	const outputCanary = "private-output-canary"
	for _, outcome := range []string{"succeeded", "failed", "http_failure", "pending"} {
		t.Run(outcome, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if outcome == "pending" {
					w.WriteHeader(202)
					fmt.Fprint(w, "null")
					return
				}
				if outcome == "http_failure" {
					w.WriteHeader(503)
					fmt.Fprint(w, `{"detail":"private-error-canary"}`)
					return
				}
				execution := fmt.Sprintf(`{"outcome":%q,"result":{"kind":"exec","exit_code":0,"stdout":%q,"stderr":""}}`, outcome, api.EncodeComputerBytes([]byte(outputCanary)))
				if outcome == "failed" {
					execution = fmt.Sprintf(`{"outcome":"failed","error_code":"exec_nonzero","result":{"kind":"exec","exit_code":7,"stdout":%q,"stderr":""}}`, api.EncodeComputerBytes([]byte(outputCanary)))
				}
				fmt.Fprintf(w, `{"computer_session_id":%q,"operation_id":%q,"evidence":"recorded","execution":%s}`, id, api.ComputerOperationID(id, requestID), execution)
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			t.Setenv("REVYL_API_KEY", "fixture-key")
			root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
			root.AddCommand(newComputerCommand())
			args := []string{"computer", "exec", id.String(), "--request-id", requestID.String(), "--json", "--", "private-argv-canary"}
			if outcome == "pending" {
				args = []string{"computer", "operation", "get", id.String(), api.ComputerOperationID(id, requestID).String(), "--json"}
			}
			leaf, _, err := root.Find(args)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("REVYL_API_KEY", "")
			var payloads []analytics.TelemetryPayload
			recorder := analytics.NewWithFlusher(analytics.Config{}, func(payload analytics.TelemetryPayload) { payloads = append(payloads, payload) })
			run := recorder.StartCommand(leaf, []string{"private-argv-canary"})
			root.SetContext(analytics.ContextWithCommandRun(context.Background(), run))
			t.Setenv("REVYL_API_KEY", "fixture-key")
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs(args)
			err = root.Execute()
			run.Complete(err)
			run.Flush()
			if outcome == "succeeded" && err != nil {
				t.Fatal(err)
			}
			if outcome != "succeeded" && err == nil {
				t.Fatal("non-success command returned success")
			}
			encoded, _ := json.Marshal(payloads)
			for _, canary := range []string{outputCanary, "private-argv-canary", "private-error-canary", "fixture-key", api.EncodeComputerBytes([]byte(outputCanary))} {
				if bytes.Contains(encoded, []byte(canary)) {
					t.Fatal("command content leaked into analytics")
				}
			}
			if len(payloads) != 1 || len(payloads[0].Events) != 2 {
				t.Fatal("expected one start and one terminal event")
			}
			props := payloads[0].Events[1].Properties
			if props["domain"] != "computer" || props["entity_id"] != id.String() {
				t.Fatal("missing terminal correlation")
			}
			if outcome == "succeeded" || outcome == "failed" {
				if props["computer_outcome"] != outcome || props["computer_evidence_write_status"] != "recorded" {
					t.Fatal("missing observed outcome facts")
				}
			} else if _, exists := props["computer_outcome"]; exists {
				t.Fatal("invented outcome without response")
			}
			if !json.Valid(stdout.Bytes()) {
				t.Fatal("stdout is not pure JSON")
			}
			if strings.Contains(stderr.String(), outputCanary) {
				t.Fatal("JSON mode wrote response bytes to stderr")
			}
		})
	}
}

func TestComputerCommandsReportTheGuestOutcomeInPlainWords(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	id, requestID := uuid.New(), uuid.New()
	for _, test := range []struct {
		name, execution, evidence, message string
		code                               int
		args                               []string
	}{
		{"file_missing", `{"outcome":"failed","error_code":"not_found"}`, "recorded", "Command failed: not_found", 1, []string{"files", "stat", id.String(), "notes.txt", "--request-id", requestID.String()}},
		{"nonzero", `{"outcome":"failed","error_code":"exec_nonzero","result":{"kind":"exec","exit_code":7,"stdout":"","stderr":""}}`, "recorded", "Command exited with code 7", 7, nil},
		{"signalled", `{"outcome":"failed","error_code":"exec_nonzero","result":{"kind":"exec","exit_code":-9,"stdout":"","stderr":""}}`, "recorded", "Command exited with code -9", 1, nil},
		{"nonzero_unrecorded", `{"outcome":"failed","error_code":"exec_nonzero","result":{"kind":"exec","exit_code":3,"stdout":"","stderr":""}}`, "pending", "Command exited with code 3", 3, nil},
		{"timed_out", `{"outcome":"timed_out","termination":"confirmed"}`, "recorded", "Command timed out", 124, nil},
		{"unknown", `{"outcome":"unknown","error_code":"lost_ack"}`, "recorded", "computer command did not confirm a successful recorded outcome; inspect the original run or operation", 125, nil},
		{"unrecorded", `{"outcome":"succeeded","result":{"kind":"exec","exit_code":0,"stdout":"","stderr":""}}`, "pending", "computer command did not confirm a successful recorded outcome; inspect the original run or operation", 74, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"computer_session_id":%q,"operation_id":%q,"evidence":%q,"execution":%s}`, id, api.ComputerOperationID(id, requestID), test.evidence, test.execution)
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			t.Setenv("REVYL_API_KEY", "fixture-key")
			root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
			root.AddCommand(newComputerCommand())
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			args := test.args
			if args == nil {
				args = []string{"exec", id.String(), "--request-id", requestID.String(), "--", "false"}
			}
			root.SetArgs(append([]string{"computer"}, args...))

			var exitErr *computerExitError
			if err := root.Execute(); !errors.As(err, &exitErr) {
				t.Fatalf("expected a computer exit, got %v", err)
			}
			if exitErr.Error() != test.message || exitErr.code != test.code {
				t.Fatalf("got %q exit %d, want %q exit %d", exitErr.Error(), exitErr.code, test.message, test.code)
			}
		})
	}
}

func TestComputerExecHumanOutputNoticesExactLimit(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	id, requestID := uuid.New(), uuid.New()
	notice := "revyl: output reached the 1 MiB limit; the rest was dropped (pipe through head or redirect to a file)\n"
	for _, test := range []struct {
		name       string
		kind       string
		stdoutLen  int
		stderrLen  int
		exitCode   int
		json       bool
		wantNotice bool
	}{
		{"exact", "exec", api.ComputerMaxOutputBytes - 1, 1, 0, false, true},
		{"stdout_only", "exec", api.ComputerMaxOutputBytes, 0, 0, false, true},
		{"stderr_only", "exec", 0, api.ComputerMaxOutputBytes, 0, false, true},
		{"under", "exec", api.ComputerMaxOutputBytes - 1, 0, 0, false, false},
		{"nonzero", "exec", api.ComputerMaxOutputBytes - 1, 1, 7, false, true},
		{"json", "exec", api.ComputerMaxOutputBytes - 1, 1, 0, true, false},
		{"json_nonzero", "exec", api.ComputerMaxOutputBytes - 1, 1, 7, true, false},
		{"file_read", "file_read", api.ComputerMaxOutputBytes, 0, 0, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			stdoutData := bytes.Repeat([]byte{'o'}, test.stdoutLen)
			stderrData := bytes.Repeat([]byte{'e'}, test.stderrLen)
			outcome := "succeeded"
			result := fmt.Sprintf(`{"kind":"exec","exit_code":%d,"stdout":%q,"stderr":%q}`, test.exitCode, api.EncodeComputerBytes(stdoutData), api.EncodeComputerBytes(stderrData))
			if test.kind == "file_read" {
				result = fmt.Sprintf(`{"kind":"file_read","data":%q}`, api.EncodeComputerBytes(stdoutData))
			}
			execution := fmt.Sprintf(`{"outcome":"succeeded","result":%s}`, result)
			if test.exitCode != 0 {
				outcome = "failed"
				execution = fmt.Sprintf(`{"outcome":"failed","error_code":"exec_nonzero","result":%s}`, result)
			}
			body := fmt.Sprintf(`{"computer_session_id":%q,"evidence":"recorded","execution":%s,"operation_id":%q}`, id, execution, api.ComputerOperationID(id, requestID))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			t.Setenv("REVYL_API_KEY", "fixture-key")
			root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
			root.AddCommand(newComputerCommand())
			var stdout, stderr, combined bytes.Buffer
			root.SetOut(io.MultiWriter(&stdout, &combined))
			root.SetErr(io.MultiWriter(&stderr, &combined))
			args := []string{"computer", "exec", id.String(), "--request-id", requestID.String()}
			if test.kind == "file_read" {
				args = []string{"computer", "files", "read", id.String(), "file", "--request-id", requestID.String()}
			}
			if test.json {
				args = append(args, "--json")
			}
			if test.kind == "exec" {
				args = append(args, "--", "printf")
			}
			root.SetArgs(args)
			err := root.Execute()
			var exitErr *computerExitError
			if test.exitCode != 0 && (!errors.As(err, &exitErr) || exitErr.code != test.exitCode) {
				t.Fatalf("guest exit code changed: %v", err)
			} else if test.exitCode == 0 && err != nil {
				t.Fatal(err)
			}
			prefix := fmt.Sprintf("computer_session_id=%s request_id=%s operation_id=%s\n", id, requestID, api.ComputerOperationID(id, requestID))
			if test.json {
				var response api.ComputerCommandResponse
				if err := json.Unmarshal([]byte(body), &response); err != nil {
					t.Fatal(err)
				}
				wantJSON, err := json.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				if stdout.String() != string(wantJSON)+"\n" || stderr.String() != prefix {
					t.Fatal("JSON output or its stderr changed")
				}
				return
			}
			wantStderr := prefix + string(stderrData)
			if test.wantNotice {
				wantStderr += notice
			}
			wantStderr += fmt.Sprintf("\noperation_id=%s outcome=%s evidence=recorded\n", api.ComputerOperationID(id, requestID), outcome)
			if !bytes.Equal(stdout.Bytes(), stdoutData) || stderr.String() != wantStderr {
				t.Fatal("human output changed or notice was not exactly once before the operation summary")
			}
			if combined.String() != prefix+string(stdoutData)+strings.TrimPrefix(wantStderr, prefix) {
				t.Fatal("notice was not written after both output streams")
			}
		})
	}
}

func TestComputerReadHelpUsesSchemaOutputLimit(t *testing.T) {
	root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newComputerCommand())
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"computer", "files", "read", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Maximum bytes returned (1-1048576)") {
		t.Fatalf("read help omitted schema output limit: %s", output.String())
	}
}

func TestComputerFileWriteLocalInputUsesSchemaLimit(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	id, requestID := uuid.New(), uuid.New()
	input := t.TempDir() + "/input"
	if err := os.WriteFile(input, bytes.Repeat([]byte{'x'}, api.ComputerMaxWriteBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newComputerCommand())
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"computer", "files", "write", id.String(), "file", "--from", input, "--request-id", requestID.String()})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "computer input exceeds the byte limit") {
		t.Fatalf("oversize local write input returned %v", err)
	}
}

func TestComputerCommandSurfaceIsExplicit(t *testing.T) {
	for _, args := range [][]string{{"computer", "list", "11111111-1111-4111-8111-111111111111"}, {"computer", "list", "--state", "active"}, {"computer", "create", "--image", "anything"}, {"computer", "create", "--ttl", "1h"}, {"computer", "exec", "--cwd", "repo"}} {
		root := &cobra.Command{Use: "revyl", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newComputerCommand())
		root.SetArgs(args)
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		if err := root.Execute(); err == nil {
			t.Fatal("unsupported computer surface accepted")
		}
	}
	for _, value := range []string{"0", "C1", "", "11111111111141118111111111111111", "00000000-0000-0000-0000-000000000000"} {
		if _, err := parseComputerUUID(value); err == nil {
			t.Fatal("noncanonical computer target accepted")
		}
	}
}

func TestComputerGetPrintsActiveDetailWithFailedOperations(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	id := "11111111-1111-4111-8111-111111111111"
	body := `{
		"report_id":"22222222-2222-4222-8222-222222222222",
		"computer_session_id":"11111111-1111-4111-8111-111111111111",
		"org_id":"44444444-4444-4444-8444-444444444444","os":"macos",
		"created_at":"2026-10-10T00:00:00Z","expires_at":"2026-10-10T01:00:00Z",
		"as_of":"2026-10-10T00:00:04Z","execution_started_at":"2026-10-10T00:00:00Z",
		"state":"active","release":"pending","steps":[
			{"step_id":"33333333-3333-4333-8333-333333333333","execution_order":1,
			"kind":"exec","started_at":"2026-10-10T00:00:01Z","completed_at":"2026-10-10T00:00:02Z",
			"outcome":"failed","error_code":"exec_nonzero","command_display":"private-command-canary",
			"summary":{"kind":"exec","exit_code":1,"stdout_byte_count":0,"stderr_byte_count":12}},
			{"step_id":"55555555-5555-4555-8555-555555555555","execution_order":2,
			"kind":"exec","started_at":"2026-10-10T00:00:02Z","completed_at":"2026-10-10T00:00:03Z",
			"outcome":"failed","error_code":"not_found","summary":null,"command_display":null}
		]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/computer-runs/"+id {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, body)
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_API_KEY", "fixture-key")
	for _, jsonOutput := range []bool{false, true} {
		t.Run(fmt.Sprint(jsonOutput), func(t *testing.T) {
			root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
			root.AddCommand(newComputerCommand())
			args := []string{"computer", "get", id}
			if jsonOutput {
				args = append(args, "--json")
			}
			var output bytes.Buffer
			root.SetOut(&output)
			root.SetErr(&bytes.Buffer{})
			root.SetArgs(args)
			leaf, _, err := root.Find(args)
			if err != nil {
				t.Fatal(err)
			}
			var payloads []analytics.TelemetryPayload
			t.Setenv("REVYL_API_KEY", "")
			recorder := analytics.NewWithFlusher(analytics.Config{}, func(payload analytics.TelemetryPayload) { payloads = append(payloads, payload) })
			run := recorder.StartCommand(leaf, nil)
			root.SetContext(analytics.ContextWithCommandRun(context.Background(), run))
			t.Setenv("REVYL_API_KEY", "fixture-key")
			err = root.Execute()
			run.Complete(err)
			run.Flush()
			if err != nil {
				t.Fatalf("computer get rejected valid detail: %v", err)
			}
			if jsonOutput {
				var detail api.ComputerReportDetail
				if err := json.Unmarshal(output.Bytes(), &detail); err != nil {
					t.Fatal(err)
				}
				if detail.Steps == nil || len(*detail.Steps) != 2 || (*detail.Steps)[0].CommandDisplay == nil || *(*detail.Steps)[0].CommandDisplay != "private-command-canary" {
					t.Fatal("computer get lost operation details")
				}
			} else if want := `Computer  11111111-1111-4111-8111-111111111111
OS        macos
State     active
Created   2026-10-10 00:00:00 UTC
Started   2026-10-10 00:00:00 UTC
Expires   2026-10-10 01:00:00 UTC

Steps
#  KIND  RESULT             STARTED   TOOK  COMMAND
1  exec  exit 1             00:00:01  1s    private-command-canary
2  exec  failed: not_found  00:00:02  1s
`; output.String() != want {
				t.Fatalf("computer get printed\n%s\nwant\n%s", output.String(), want)
			}
			if len(payloads) != 1 || len(payloads[0].Events) != 2 {
				t.Fatal("expected one start and one terminal event")
			}
			props := payloads[0].Events[1].Properties
			if props["domain_status"] != "active" || props["computer_release"] != "pending" || props["computer_evidence_write_status"] != "recorded" {
				t.Fatal("missing computer detail lifecycle facts")
			}
			encoded, err := json.Marshal(payloads)
			if err != nil || bytes.Contains(encoded, []byte("private-command-canary")) {
				t.Fatal("command display entered analytics")
			}
		})
	}
}

func TestComputerListShowsTheNewestComputers(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	body := `{"computers":[
		{"computer_session_id":"11111111-1111-4111-8111-111111111111","os":"macos",
		"created_at":"2026-10-10T00:05:00Z","expires_at":"2026-10-10T01:05:00Z","state":"active",
		"release":"pending","released_at":null,"execution_started_at":"2026-10-10T00:05:30Z",
		"execution_ended_at":null,"evidence_write_status":"recorded","evidence_completeness":"complete"},
		{"computer_session_id":"22222222-2222-4222-8222-222222222222","os":"macos",
		"created_at":"2026-10-10T00:00:00Z","expires_at":"2026-10-10T01:00:00Z","state":"stopped",
		"release":"unknown","released_at":null,"execution_started_at":"2026-10-10T00:00:30Z",
		"execution_ended_at":"2026-10-10T00:04:00Z","evidence_write_status":"recorded","evidence_completeness":"complete"}
	],"total":3,"limit":2,"offset":0}`
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/computer-runs" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		queries = append(queries, r.URL.RawQuery)
		fmt.Fprint(w, body)
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_API_KEY", "fixture-key")
	run := func(args ...string) (string, error) {
		root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
		root.AddCommand(newComputerCommand())
		var output bytes.Buffer
		root.SetOut(&output)
		root.SetErr(&bytes.Buffer{})
		root.SetArgs(append([]string{"computer", "list"}, args...))
		err := root.Execute()
		return output.String(), err
	}

	printed, err := run("--limit", "2")
	if err != nil {
		t.Fatalf("computer list rejected a valid page: %v", err)
	}
	want := `COMPUTER                              OS     STATE                     CREATED
11111111-1111-4111-8111-111111111111  macos  active                    2026-10-10 00:05:00 UTC
22222222-2222-4222-8222-222222222222  macos  stopped, cleanup unknown  2026-10-10 00:00:00 UTC
Showing the newest 2 of 3 computers; use --limit (up to 100) for more.
`
	if printed != want {
		t.Fatalf("computer list printed\n%s\nwant\n%s", printed, want)
	}
	printed, err = run("--limit", "2", "--json")
	var list api.ComputerRunListResponse
	if err != nil || json.Unmarshal([]byte(printed), &list) != nil || len(list.Computers) != 2 || list.Total != 3 {
		t.Fatalf("computer list --json lost the page: %v", err)
	}
	if _, err := run(); err == nil {
		t.Fatal("computer list accepted a page larger than it asked for")
	}
	for _, limit := range []string{"0", "101"} {
		if _, err := run("--limit", limit); err == nil {
			t.Fatalf("computer list accepted --limit %s", limit)
		}
	}
	if strings.Join(queries, ",") != "limit=2,limit=2,limit=25" {
		t.Fatalf("computer list sent %v", queries)
	}
}

func TestComputerGetSaysWhyAStartFailed(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	id := "11111111-1111-4111-8111-111111111111"
	for _, tc := range []struct {
		reason     string
		jsonOutput bool
		said       string
	}{
		{"no_host", false, "No Mac was free to start this computer"},
		{"no_host", true, ""},
		{"device_limit", false, "All of your organization's device slots were in use, so this computer didn't start"},
		{"added_later", false, "added_later"},
	} {
		body := `{
		"report_id":"22222222-2222-4222-8222-222222222222",
		"computer_session_id":"11111111-1111-4111-8111-111111111111",
		"org_id":"44444444-4444-4444-8444-444444444444","os":"macos",
		"created_at":"2026-10-10T00:00:00Z","expires_at":"2026-10-10T01:00:00Z",
		"as_of":"2026-10-10T00:02:04Z","state":"failed","failure_reason":"` + tc.reason + `",
		"release":"confirmed","released_at":"2026-10-10T00:02:03Z",
		"evidence_completeness":"complete","steps":[],"added_later":{"kind":"new"}}`
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, body)
		}))
		defer server.Close()
		t.Setenv("REVYL_BACKEND_URL", server.URL)
		t.Setenv("REVYL_API_KEY", "fixture-key")
		jsonOutput := tc.jsonOutput
		t.Run(tc.reason+fmt.Sprint(jsonOutput), func(t *testing.T) {
			root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
			root.AddCommand(newComputerCommand())
			args := []string{"computer", "get", id}
			if jsonOutput {
				args = append(args, "--json")
			}
			var output bytes.Buffer
			root.SetOut(&output)
			root.SetErr(&bytes.Buffer{})
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("computer get rejected a failed start: %v", err)
			}
			if jsonOutput {
				if !strings.Contains(output.String(), `"failure_reason":"`+tc.reason+`"`) {
					t.Fatalf("computer get --json dropped the failure reason: %s", output.String())
				}
				return
			}
			if want := `Computer  11111111-1111-4111-8111-111111111111
OS        macos
State     failed
Reason    ` + tc.said + `
Created   2026-10-10 00:00:00 UTC
Expires   2026-10-10 01:00:00 UTC
Released  2026-10-10 00:02:03 UTC

No steps yet.
`; output.String() != want {
				t.Fatalf("computer get output = %q, want %q", output.String(), want)
			}
		})
	}
}

func TestComputerCreateExplainsTheComputerLimitLikeTheUI(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	for _, tc := range []struct {
		name       string
		jsonOutput bool
		code       string
		status     int
		message    string
	}{
		{"text", false, "computer_limit", http.StatusTooManyRequests, "Every device slot a computer can use is busy. Stop a session, then try again."},
		{"json", true, "computer_limit", http.StatusTooManyRequests, ""},
		{"not_enabled_text", false, "computer_not_enabled", http.StatusForbidden, "Computers aren't enabled for this organization. Contact Revyl to turn them on."},
		{"not_enabled_json", true, "computer_not_enabled", http.StatusForbidden, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/api/v1/computer-runs" {
					w.WriteHeader(tc.status)
					fmt.Fprintf(w, `{"detail":%q}`, tc.code)
					return
				}
				reads++
				w.WriteHeader(http.StatusForbidden)
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			t.Setenv("REVYL_API_KEY", "fixture-key")
			root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
			root.AddCommand(newComputerCommand())
			args := []string{"computer", "create", "--os", "macos"}
			if tc.jsonOutput {
				args = append(args, "--json")
			}
			var output bytes.Buffer
			root.SetOut(&output)
			root.SetErr(&bytes.Buffer{})
			root.SetArgs(args)
			err := root.Execute()
			if tc.jsonOutput {
				if err == nil || reads != 0 || !strings.Contains(output.String(), `"error":"computer request refused: `+tc.code+`"`) {
					t.Fatalf("computer create --json = %q, %v after %d reads; want the refusal code and no extra read", output.String(), err, reads)
				}
				return
			}
			if err == nil || err.Error() != tc.message || reads != 0 {
				t.Fatalf("computer create said %v after %d reads, want %q and no extra read", err, reads, tc.message)
			}
		})
	}
}
