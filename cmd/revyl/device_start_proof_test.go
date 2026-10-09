package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

const (
	proofStartWorkflowRunID = "00000000-0000-0000-0000-000000000061"
	proofStartSessionID     = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaa61"
	proofStartRunID         = "b3cd415d-0000-0000-0000-000000000061"
)

func serveProofDeviceStart(t *testing.T, startBody string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/execution/start_device":
			_, _ = w.Write([]byte(startBody))
		case "/api/v1/execution/streaming/worker-connection/" + proofStartWorkflowRunID:
			_, _ = w.Write([]byte(`{"status":"ready","workflow_run_id":"` + proofStartWorkflowRunID + `","worker_ws_url":"ws://` + r.Host + `/ws/stream?token=test"}`))
		case "/api/v1/execution/device-proxy/" + proofStartWorkflowRunID + "/health":
			_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)
}

func runDeviceStartJSON(t *testing.T) (stdout, stderr string) {
	t.Helper()
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	cmd := newDeviceStartTestCommand(context.Background())
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var runErr error
	stdout, stderr = captureStdoutAndStderrSeparate(t, func() { runErr = deviceStartCmd.RunE(cmd, nil) })
	if runErr != nil {
		t.Fatalf("device start error = %v\nstderr: %s", runErr, stderr)
	}
	return stdout, stderr
}

type deviceStartJSON struct {
	SessionID       string           `json:"session_id"`
	DefaultsApplied []appliedDefault `json:"defaults_applied"`
}

func TestDeviceStartNamesTheNewSessionOnStderr(t *testing.T) {
	serveProofDeviceStart(t, `{"workflow_run_id":"`+proofStartWorkflowRunID+`","session_id":"`+proofStartSessionID+`","status":"initializing","platform":"ios"}`)

	stdout, stderr := runDeviceStartJSON(t)

	want := "Started session " + proofStartSessionID + "; waiting for its device. Run 'revyl device list' to check on it."
	if !strings.Contains(stderr, want) {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
	var payload deviceStartJSON
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if payload.SessionID != proofStartSessionID || payload.DefaultsApplied != nil {
		t.Fatalf("stdout = %s, want the new session and no defaults_applied", stdout)
	}
}

func newDeviceListTestCommand(t *testing.T, jsonOutput bool) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("json", jsonOutput, "")
	cmd.Flags().Bool("dev", false, "")
	return cmd
}

func serveProofRunListing(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"proof","org_id":"org-1","email":""}`))
		case "/api/v1/execution/device-sessions/active":
			_, _ = w.Write([]byte(`{"org_id":"org-1","proof_run_id":"` + proofStartRunID + `","sessions":[
				{"id":"` + proofStartSessionID + `","org_id":"org-1","platform":"ios","source":"cli","status":"queued",
				 "created_at":"` + time.Now().Add(-90*time.Second).UTC().Format(time.RFC3339) + `",
				 "source_metadata":{"scm_review_run_id":"` + proofStartRunID + `"}},
				{"id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbb61","org_id":"org-1","platform":"android","source":"cli","status":"starting",
				 "source_metadata":{"scm_review_run_id":"bc610fc1-0000-0000-0000-000000000061"}}
			]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
}

func TestDeviceListShowsTheProofRunsStartingSessionWithItsStatus(t *testing.T) {
	serveProofRunListing(t)

	var runErr error
	stdout, _ := captureStdoutAndStderrSeparate(t, func() { runErr = deviceListCmd.RunE(newDeviceListTestCommand(t, true), nil) })
	if runErr != nil {
		t.Fatalf("device list --json error = %v", runErr)
	}

	var entries []map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("stdout is not a JSON array: %v\n%s", err, stdout)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %s, want only this run's session", stdout)
	}
	entry := entries[0]
	if entry["session_id"] != proofStartSessionID || entry["status"] != "queued" || entry["index"] != float64(-1) || entry["platform"] != "ios" {
		t.Fatalf("entry = %v, want the queued session with no local index", entry)
	}
	for _, key := range []string{"workflow_run_id", "worker_base_url", "viewer_url", "started_at"} {
		if _, ok := entry[key]; !ok {
			t.Fatalf("entry = %v, want existing key %q kept", entry, key)
		}
	}
}

func TestDeviceListTableMarksASessionWithoutADevice(t *testing.T) {
	serveProofRunListing(t)

	var runErr error
	stdout, stderr := captureStdoutAndStderrSeparate(t, func() { runErr = deviceListCmd.RunE(newDeviceListTestCommand(t, false), nil) })
	if runErr != nil {
		t.Fatalf("device list error = %v", runErr)
	}

	if !strings.Contains(stdout, "  -   ios        queued     aaaaaaaa     1m3") {
		t.Fatalf("stdout = %q, want a queued row with no index", stdout)
	}
	if !strings.Contains(stderr, "target one by ID, e.g. -s "+proofStartSessionID) {
		t.Fatalf("stderr = %q, want how to target the session by ID", stderr)
	}
}
