package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/revyl/cli/internal/api"
)

const (
	startIDWorkflowRunID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb1"
	startIDSessionID     = "cccccccc-cccc-4ccc-8ccc-ccccccccccc1"
)

type startIDCallCounts struct {
	active atomic.Int32
	lookup atomic.Int32
}

func newStartSessionIDServer(t *testing.T, startBody string, counts *startIDCallCounts) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/execution/start_device":
			_, _ = w.Write([]byte(startBody))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/execution/streaming/worker-connection/"+startIDWorkflowRunID:
			_, _ = w.Write([]byte(`{"status":"ready","workflow_run_id":"` + startIDWorkflowRunID + `","worker_ws_url":"ws://` + r.Host + `/ws/stream?token=test"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/execution/device-proxy/"+startIDWorkflowRunID+"/health":
			_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/execution/device-sessions/active":
			counts.active.Add(1)
			_, _ = w.Write([]byte(`{"org_id":"org-1","sessions":[]}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/execution/device-sessions/by-workflow-run/"):
			counts.lookup.Add(1)
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
}

func startSessionForIDTest(t *testing.T, serverURL string) (*DeviceSessionManager, *DeviceSession) {
	t.Helper()

	workDir := t.TempDir()
	mgr := NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", serverURL), workDir)
	index, session, err := mgr.StartSession(context.Background(), StartSessionOptions{Platform: "ios"})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	if session == nil {
		t.Fatal("StartSession() session = nil")
	}
	t.Cleanup(func() { mgr.StopIdleTimer(index) })
	return mgr, session
}

func persistedStartSessionID(t *testing.T, workDir string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(workDir, ".revyl", "device-sessions.json"))
	if err != nil {
		t.Fatalf("ReadFile(device-sessions.json) error = %v", err)
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("Unmarshal(device-sessions.json) error = %v", err)
	}
	if len(state.Sessions) != 1 {
		t.Fatalf("persisted sessions = %d, want 1", len(state.Sessions))
	}
	return state.Sessions[0].SessionID
}

func TestStartSession_UsesSessionIDFromStartResponse(t *testing.T) {
	t.Parallel()

	counts := &startIDCallCounts{}
	server := newStartSessionIDServer(
		t,
		`{"workflow_run_id":"`+startIDWorkflowRunID+`","session_id":"`+startIDSessionID+`"}`,
		counts,
	)
	defer server.Close()

	mgr, session := startSessionForIDTest(t, server.URL)
	if session.SessionID != startIDSessionID {
		t.Fatalf("session ID = %q, want %q", session.SessionID, startIDSessionID)
	}
	if session.WorkflowRunID != startIDWorkflowRunID {
		t.Fatalf("workflow run ID = %q, want %q", session.WorkflowRunID, startIDWorkflowRunID)
	}
	if !strings.Contains(session.ViewerURL, startIDSessionID) {
		t.Fatalf("viewer URL = %q, want it to include the start-response session ID", session.ViewerURL)
	}
	if got := counts.active.Load(); got != 0 {
		t.Fatalf("active-session list calls = %d, want 0", got)
	}
	if got := counts.lookup.Load(); got != 0 {
		t.Fatalf("by-workflow-run lookups = %d, want 0", got)
	}
	if got := persistedStartSessionID(t, mgr.WorkDir()); got != startIDSessionID {
		t.Fatalf("persisted session ID = %q, want %q", got, startIDSessionID)
	}
}

func TestStartSession_MissingSessionIDLeavesSessionAddressableByWorkflowRun(t *testing.T) {
	counts := &startIDCallCounts{}
	server := newStartSessionIDServer(
		t,
		`{"workflow_run_id":"`+startIDWorkflowRunID+`"}`,
		counts,
	)
	defer server.Close()

	stderr := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	os.Stderr = writer
	_, session := startSessionForIDTest(t, server.URL)
	_ = writer.Close()
	os.Stderr = stderr
	var warning bytes.Buffer
	_, _ = warning.ReadFrom(reader)

	if session.SessionID != "" {
		t.Fatalf("session ID = %q, want empty when start_device omits session_id", session.SessionID)
	}
	if session.WorkflowRunID != startIDWorkflowRunID {
		t.Fatalf("workflow run ID = %q, want %q so the session stays usable by index", session.WorkflowRunID, startIDWorkflowRunID)
	}
	if got := counts.active.Load(); got != 0 {
		t.Fatalf("active-session list calls = %d, want 0", got)
	}
	if got := counts.lookup.Load(); got != 0 {
		t.Fatalf("by-workflow-run lookups = %d, want 0", got)
	}
	if !strings.Contains(warning.String(), "cannot be targeted by ID") {
		t.Fatalf("expected a missing-session-ID warning, got %q", warning.String())
	}
}
