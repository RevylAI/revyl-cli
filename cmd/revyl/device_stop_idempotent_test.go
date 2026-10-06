package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
)

// newEmptyInventoryServer serves a backend where the caller has no live device
// sessions, or fails the active-session read when inventoryStatus is not 200.
func newEmptyInventoryServer(t *testing.T, inventoryStatus int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-1","email":"test@example.com","concurrency_limit":1}`))
		case "/api/v1/execution/device-sessions/active":
			w.WriteHeader(inventoryStatus)
			_, _ = w.Write([]byte(`{"org_id":"org-1","sessions":[]}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newStopTestCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "stop"}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("json", false, "")
	cmd.Flags().Bool("dev", false, "")
	cmd.Flags().Bool("all", false, "")
	registerSessionTargetFlags(cmd)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return cmd
}

// runStopWithAnalytics runs a command RunE with a command run in its context and
// returns the terminal telemetry event properties.
func runStopWithAnalytics(t *testing.T, cmd *cobra.Command, runE func(*cobra.Command, []string) error, args []string) (stdout, stderr string, runErr error, terminal map[string]interface{}) {
	t.Helper()
	var captured analytics.TelemetryPayload
	recorder := analytics.NewWithFlusher(analytics.Config{}, func(payload analytics.TelemetryPayload) { captured.Events = append(captured.Events, payload.Events...) })
	run := recorder.StartCommand(cmd, args)
	cmd.SetContext(analytics.ContextWithCommandRun(context.Background(), run))
	stdout, stderr = captureStdoutAndStderrSeparate(t, func() { runErr = runE(cmd, args) })
	run.Complete(runErr)
	recorder.Flush()
	if len(captured.Events) != 2 {
		t.Fatalf("captured %d lifecycle events, want start and terminal", len(captured.Events))
	}
	return stdout, stderr, runErr, captured.Events[1].Properties
}

func TestDeviceStop_NothingToStopExitsZero(t *testing.T) {
	testCases := []struct {
		name       string
		args       []string
		wantNotice string
	}{
		{name: "no active session", args: []string{"--json"}, wantNotice: "No active device session to stop; nothing to do."},
		{name: "stale index", args: []string{"--json", "-s", "3"}, wantNotice: "No active device session at index 3 to stop; nothing to do."},
		{name: "all", args: []string{"--json", "--all"}, wantNotice: "No active device sessions to stop; nothing to do."},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			withWorkingDirectory(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "test-api-key")
			t.Setenv(sessionIDEnvVar, "")
			t.Setenv("REVYL_BACKEND_URL", newEmptyInventoryServer(t, http.StatusOK).URL)

			cmd := newStopTestCommand(t, tc.args...)
			stdout, stderr, err, terminal := runStopWithAnalytics(t, cmd, deviceStopCmd.RunE, nil)
			if err != nil {
				t.Fatalf("device stop error = %v, want nothing-to-stop success", err)
			}
			var payload map[string]interface{}
			if jsonErr := json.Unmarshal([]byte(stdout), &payload); jsonErr != nil {
				t.Fatalf("stdout is not one JSON document: %v\n%s", jsonErr, stdout)
			}
			if payload["already_stopped"] != true {
				t.Fatalf("already_stopped = %v, want true: %s", payload["already_stopped"], stdout)
			}
			if _, all := payload["stopped_all"]; !all && payload["stopped"] != false {
				t.Fatalf("stopped = %v, want false for a no-op stop", payload["stopped"])
			}
			if !strings.Contains(stderr, tc.wantNotice) || strings.Count(strings.TrimSpace(stderr), "\n") != 0 {
				t.Fatalf("stderr = %q, want the single notice %q", stderr, tc.wantNotice)
			}
			if terminal["domain"] != "device_session_stop" || terminal["domain_status"] != "already_stopped" || terminal["exit_code"] != 0 {
				t.Fatalf("terminal analytics = %+v, want an already_stopped completion", terminal)
			}
		})
	}
}

func TestDeviceStop_UnconfirmedInventoryStillFails(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	t.Setenv("REVYL_BACKEND_URL", newEmptyInventoryServer(t, http.StatusForbidden).URL)

	cmd := newStopTestCommand(t, "--json")
	var stopErr error
	stdout := captureStdout(t, func() { stopErr = deviceStopCmd.RunE(cmd, nil) })
	if stopErr == nil || !strings.Contains(stopErr.Error(), "could not read your device sessions from Revyl, so nothing was stopped") {
		t.Fatalf("device stop error = %v, want the unconfirmed-inventory failure", stopErr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want no success document when the inventory could not be read", stdout)
	}
}

func TestDevStop_MissingContextExitsZero(t *testing.T) {
	testCases := []struct {
		name       string
		args       []string
		all        bool
		wantNotice string
		wantKey    string
	}{
		{name: "named context", args: []string{"ios-main"}, wantNotice: "No dev context 'ios-main' in this worktree; nothing to stop.", wantKey: "context"},
		{name: "all contexts", all: true, wantNotice: "No dev contexts in this worktree; nothing to stop.", wantKey: "contexts"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			withWorkingDirectory(t, t.TempDir())
			previousAll := devStopAll
			devStopAll = tc.all
			t.Cleanup(func() { devStopAll = previousAll })

			cmd := &cobra.Command{Use: "stop"}
			cmd.Flags().Bool("json", true, "")
			cmd.Flags().String("context", "", "")
			stdout, stderr, err, terminal := runStopWithAnalytics(t, cmd, runDevStop, tc.args)
			if err != nil {
				t.Fatalf("dev stop error = %v, want nothing-to-stop success", err)
			}
			var payload map[string]interface{}
			if jsonErr := json.Unmarshal([]byte(stdout), &payload); jsonErr != nil {
				t.Fatalf("stdout is not JSON: %v\n%s", jsonErr, stdout)
			}
			if payload["already_stopped"] != true || payload["stopped"] != false {
				t.Fatalf("payload = %s, want stopped=false and already_stopped=true", stdout)
			}
			if _, ok := payload[tc.wantKey]; !ok {
				t.Fatalf("payload = %s, want key %q", stdout, tc.wantKey)
			}
			if !strings.Contains(stderr, tc.wantNotice) {
				t.Fatalf("stderr = %q, want %q", stderr, tc.wantNotice)
			}
			if terminal["domain"] != "dev_stop" || terminal["domain_status"] != "already_stopped" {
				t.Fatalf("terminal analytics = %+v, want an already_stopped dev_stop completion", terminal)
			}
		})
	}
}

func TestDeviceStop_UnreachableLiveSessionIsNotNothingToStop(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-1","email":"test@example.com"}`))
		case "/api/v1/execution/device-sessions/active":
			_, _ = w.Write([]byte(`{"org_id":"org-1","sessions":[{"id":"` + flowSessionID + `","org_id":"org-1","platform":"ios","status":"starting","workflow_run_id":"` + flowWorkflowRunID + `","user_email":"test@example.com"}]}`))
		case "/api/v1/execution/streaming/worker-connection/" + flowWorkflowRunID:
			_, _ = w.Write([]byte(`{"status":"not_ready","workflow_run_id":"` + flowWorkflowRunID + `"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	for _, args := range [][]string{{"--json"}, {"--json", "-s", "2"}} {
		cmd := newStopTestCommand(t, args...)
		var stopErr error
		stdout := captureStdout(t, func() { stopErr = deviceStopCmd.RunE(cmd, nil) })
		if stopErr == nil || !strings.Contains(stopErr.Error(), "revyl device stop -s "+flowSessionID) {
			t.Fatalf("device stop %v error = %v, want the starting session named with a stop-by-ID command", args, stopErr)
		}
		if strings.Contains(stdout, "already_stopped") {
			t.Fatalf("device stop %v reported a no-op while a session is still starting: %s", args, stdout)
		}
	}

	cmd := newStopTestCommand(t, "--json", "--all")
	var stopErr error
	stdout, stderr := captureStdoutAndStderrSeparate(t, func() { stopErr = deviceStopCmd.RunE(cmd, nil) })
	if stopErr != nil {
		t.Fatalf("device stop --all error = %v", stopErr)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	unreachable, _ := payload["unreachable_session_ids"].([]interface{})
	if payload["stopped_all"] != false || payload["already_stopped"] != nil || len(unreachable) != 1 || unreachable[0] != flowSessionID {
		t.Fatalf("device stop --all = %s, want stopped_all=false naming the starting session", stdout)
	}
	if !strings.Contains(stderr, "revyl device stop -s "+flowSessionID) {
		t.Fatalf("stderr = %q, want the stop-by-ID command", stderr)
	}
}

func TestDevStop_ContextWithoutMetadataStillFails(t *testing.T) {
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, ".revyl", devContextsDir, "ios-main"), 0o700); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{Use: "stop"}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("json", true, "")
	cmd.Flags().String("context", "", "")
	var stopErr error
	stdout := captureStdout(t, func() { stopErr = runDevStop(cmd, []string{"ios-main"}) })
	if stopErr == nil || !strings.Contains(stopErr.Error(), "could not read dev context 'ios-main'") {
		t.Fatalf("dev stop error = %v, want a failure while the context directory exists", stopErr)
	}
	if strings.Contains(stdout, "already_stopped") {
		t.Fatalf("dev stop reported a no-op for an existing context: %s", stdout)
	}
}

func TestDevStop_UnreadableContextStillFails(t *testing.T) {
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	contextDir := filepath.Join(dir, ".revyl", devContextsDir, "ios-main")
	if err := os.MkdirAll(contextDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contextDir, devContextMetaFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{Use: "stop"}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("json", true, "")
	cmd.Flags().String("context", "", "")
	var stopErr error
	captureStdout(t, func() { stopErr = runDevStop(cmd, []string{"ios-main"}) })
	if stopErr == nil || !strings.Contains(stopErr.Error(), "could not read dev context 'ios-main'") {
		t.Fatalf("dev stop error = %v, want the unreadable-context failure", stopErr)
	}
}

func TestDeviceStop_InaccessibleSessionIDStillFails(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/execution/device/sessions/"+flowSessionID+"/stop" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Session not found: ` + flowSessionID + `"}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	cmd := newStopTestCommand(t, "--json", "-s", flowSessionID)
	var stopErr error
	stdout := captureStdout(t, func() { stopErr = deviceStopCmd.RunE(cmd, nil) })
	if stopErr == nil {
		t.Fatal("device stop error = nil, want a failure for a session ID this organization cannot see")
	}
	if strings.Contains(stdout, "already_stopped") {
		t.Fatalf("stdout = %s, an inaccessible session ID must not report a no-op stop", stdout)
	}
}

// TestDeviceCommandErrorsNameCLICommands drives device commands through the
// real CLI manager and asserts their session errors name revyl commands, never
// MCP tool calls such as screenshot().
func TestDeviceCommandErrorsNameCLICommands(t *testing.T) {
	testCases := []struct {
		name    string
		runE    func(*cobra.Command, []string) error
		args    []string
		ended   bool
		wantErr string
	}{
		{name: "screenshot without a session", runE: deviceScreenshotCmd.RunE, wantErr: "Start one with 'revyl device start'"},
		{name: "hierarchy with a stale index", runE: deviceHierarchyCmd.RunE, args: []string{"-s", "3"}, wantErr: "Run 'revyl device list'"},
		{name: "screenshot after the session ended", runE: deviceScreenshotCmd.RunE, args: []string{"-s", flowSessionID}, ended: true, wantErr: "Start a new session with 'revyl device start'"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			withWorkingDirectory(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "test-api-key")
			t.Setenv(sessionIDEnvVar, "")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/entity/users/get_user_uuid":
					_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-1","email":"test@example.com"}`))
				case "/api/v1/execution/device-sessions/active":
					_, _ = w.Write([]byte(`{"org_id":"org-1","sessions":[]}`))
				case "/api/v1/execution/device-sessions/" + flowSessionID:
					_, _ = w.Write([]byte(`{"id":"` + flowSessionID + `","org_id":"org-1","platform":"ios","status":"running","workflow_run_id":"` + flowWorkflowRunID + `"}`))
				case "/api/v1/execution/streaming/worker-connection/" + flowWorkflowRunID:
					_, _ = w.Write([]byte(`{"status":"stopped","workflow_run_id":"` + flowWorkflowRunID + `","message":"Idle timeout reached"}`))
				case "/api/v1/execution/device-proxy/" + flowWorkflowRunID + "/screenshot":
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"detail":"Worker URL not found"}`))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			t.Setenv("REVYL_BACKEND_URL", server.URL)

			cmd := newStopTestCommand(t, tc.args...)
			cmd.Flags().String("out", "", "")
			var runErr error
			captureStdout(t, func() { runErr = tc.runE(cmd, nil) })
			if runErr == nil || !strings.Contains(runErr.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", runErr, tc.wantErr)
			}
			if mcpToolCall := regexp.MustCompile(`\w+\(\)`); mcpToolCall.MatchString(runErr.Error()) {
				t.Fatalf("CLI error %q names an MCP tool call", runErr)
			}
		})
	}
}
