package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/revyl/cli/internal/api"
)

const (
	targetSessionID     = "9f3c1a2b-4d5e-4f60-8a71-b2c3d4e5f607"
	targetWorkflowRunID = "33333333-3333-3333-3333-333333333333"

	// Grounded taps scale against the session's screen size, so resolution has
	// to carry these through or coordinates silently go unscaled.
	targetScreenWidth  = 440
	targetScreenHeight = 956
)

// newSessionIDResolveServer serves the full durable-ID resolution path with a
// caller-chosen session status and workflow run.
func newSessionIDResolveServer(t *testing.T, status string, workflowRunID *string) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-1","email":"test@example.com","concurrency_limit":1}`))
		case r.URL.Path == "/api/v1/execution/device-sessions/"+targetSessionID:
			runField := "null"
			if workflowRunID != nil {
				runField = `"` + *workflowRunID + `"`
			}
			_, _ = w.Write([]byte(`{
				"id":"` + targetSessionID + `",
				"org_id":"org-1",
				"platform":"ios",
				"status":"` + status + `",
				"workflow_run_id":` + runField + `,
				"screen_width":` + strconv.Itoa(targetScreenWidth) + `,
				"screen_height":` + strconv.Itoa(targetScreenHeight) + `,
				"started_at":"2026-02-19T00:00:00Z"
			}`))
		case r.URL.Path == "/api/v1/execution/streaming/worker-connection/"+targetWorkflowRunID:
			_, _ = w.Write([]byte(`{"status":"ready","workflow_run_id":"` + targetWorkflowRunID +
				`","worker_ws_url":"ws://` + r.Host + `/ws/stream?token=test"}`))
		case r.URL.Path == "/api/v1/execution/device-proxy/"+targetWorkflowRunID+"/health":
			_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

// newIDTargetedManager builds a manager rooted at a temp workDir with
// persistence disabled, mirroring how the CLI constructs it for ID targeting.
func newIDTargetedManager(t *testing.T, serverURL string) (*DeviceSessionManager, string) {
	t.Helper()

	workDir := t.TempDir()
	mgr := NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", serverURL), workDir)
	mgr.DisablePersistence()
	return mgr, workDir
}

// newCountingSessionIDServer serves the resolution path while recording how
// many times each backend route was hit.
func newCountingSessionIDServer(t *testing.T, counts map[string]int, mu *sync.Mutex) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		counts[r.URL.Path]++
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-1","email":"test@example.com","concurrency_limit":1}`))
		case "/api/v1/execution/device-sessions/" + targetSessionID:
			_, _ = w.Write([]byte(`{
				"id":"` + targetSessionID + `",
				"org_id":"org-1",
				"platform":"ios",
				"status":"running",
				"workflow_run_id":"` + targetWorkflowRunID + `",
				"started_at":"2026-02-19T00:00:00Z"
			}`))
		case "/api/v1/execution/streaming/worker-connection/" + targetWorkflowRunID:
			_, _ = w.Write([]byte(`{"status":"ready","workflow_run_id":"` + targetWorkflowRunID +
				`","worker_ws_url":"ws://` + r.Host + `/ws/stream?token=test"}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

// Parallel batches pay resolution once per command, so anything the happy path
// does not strictly need is multiplied across every step of every worker.
func TestResolveSessionByID_IssuesSingleBackendCall(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	counts := make(map[string]int)
	server := newCountingSessionIDServer(t, counts, &mu)
	defer server.Close()

	mgr, _ := newIDTargetedManager(t, server.URL)

	if _, err := mgr.ResolveSessionByID(context.Background(), targetSessionID); err != nil {
		t.Fatalf("ResolveSessionByID() error = %v, want nil", err)
	}

	mu.Lock()
	defer mu.Unlock()

	total := 0
	for _, count := range counts {
		total += count
	}
	if total != 1 {
		t.Fatalf("backend calls during resolution = %d (%v), want exactly 1", total, counts)
	}
	if got := counts["/api/v1/execution/device-sessions/"+targetSessionID]; got != 1 {
		t.Fatalf("session lookups = %d, want 1", got)
	}
	if got := counts["/api/v1/entity/users/get_user_uuid"]; got != 0 {
		t.Fatalf("API key validations = %d, want 0: org info is never read on this path", got)
	}
	if got := counts["/api/v1/execution/streaming/worker-connection/"+targetWorkflowRunID]; got != 0 {
		t.Fatalf("worker URL lookups = %d, want 0: worker actions are relayed by workflow run ID", got)
	}
}

// Dropping the eager health probe must not cost diagnosis: a failing action
// should still explain that the device is merely still starting.
func TestWorkerRequestOnSession_ExplainsNotReadyDeviceOnFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/execution/device-sessions/" + targetSessionID:
			_, _ = w.Write([]byte(`{
				"id":"` + targetSessionID + `",
				"org_id":"org-1",
				"platform":"ios",
				"status":"running",
				"workflow_run_id":"` + targetWorkflowRunID + `",
				"started_at":"2026-02-19T00:00:00Z"
			}`))
		case "/api/v1/execution/streaming/worker-connection/" + targetWorkflowRunID:
			_, _ = w.Write([]byte(`{"status":"not_ready","workflow_run_id":"` + targetWorkflowRunID + `"}`))
		default:
			http.Error(w, `{"detail":"worker not reachable"}`, http.StatusBadGateway)
		}
	}))
	defer server.Close()

	mgr, _ := newIDTargetedManager(t, server.URL)

	session, err := mgr.ResolveSessionByID(context.Background(), targetSessionID)
	if err != nil {
		t.Fatalf("ResolveSessionByID() error = %v, want nil: resolution must not probe the worker", err)
	}

	_, err = mgr.WorkerRequestOnSession(context.Background(), session, "/tap", map[string]int{"x": 1, "y": 2})
	if err == nil {
		t.Fatal("WorkerRequestOnSession() error = nil, want failure against an unreachable worker")
	}
	if !strings.Contains(err.Error(), "still starting") {
		t.Fatalf("WorkerRequestOnSession() error = %q, want still-starting guidance", err)
	}
}

func TestResolveSessionByID_ReturnsUnattachedSession(t *testing.T) {
	t.Parallel()

	runID := targetWorkflowRunID
	server := newSessionIDResolveServer(t, "running", &runID)
	defer server.Close()

	mgr, _ := newIDTargetedManager(t, server.URL)

	session, err := mgr.ResolveSessionByID(context.Background(), targetSessionID)
	if err != nil {
		t.Fatalf("ResolveSessionByID() error = %v, want nil", err)
	}
	if session.SessionID != targetSessionID {
		t.Fatalf("SessionID = %q, want %q", session.SessionID, targetSessionID)
	}
	if session.WorkflowRunID != targetWorkflowRunID {
		t.Fatalf("WorkflowRunID = %q, want %q", session.WorkflowRunID, targetWorkflowRunID)
	}
	if session.Index != UnattachedSessionIndex {
		t.Fatalf("Index = %d, want UnattachedSessionIndex (%d)", session.Index, UnattachedSessionIndex)
	}
	if session.Platform != "ios" {
		t.Fatalf("Platform = %q, want ios", session.Platform)
	}
}

// An ID-targeted session that reports no screen size makes grounded taps fall
// back to unscaled coordinates, so they land on the wrong pixels.
func TestResolveSessionByID_CarriesScreenDimensions(t *testing.T) {
	t.Parallel()

	runID := targetWorkflowRunID
	server := newSessionIDResolveServer(t, "running", &runID)
	defer server.Close()

	mgr, _ := newIDTargetedManager(t, server.URL)

	session, err := mgr.ResolveSessionByID(context.Background(), targetSessionID)
	if err != nil {
		t.Fatalf("ResolveSessionByID() error = %v, want nil", err)
	}
	if session.ScreenWidth != targetScreenWidth {
		t.Fatalf("ScreenWidth = %d, want %d", session.ScreenWidth, targetScreenWidth)
	}
	if session.ScreenHeight != targetScreenHeight {
		t.Fatalf("ScreenHeight = %d, want %d", session.ScreenHeight, targetScreenHeight)
	}
}

func TestResolveSessionByID_RejectsTerminalSession(t *testing.T) {
	t.Parallel()

	runID := targetWorkflowRunID
	server := newSessionIDResolveServer(t, "completed", &runID)
	defer server.Close()

	mgr, _ := newIDTargetedManager(t, server.URL)

	_, err := mgr.ResolveSessionByID(context.Background(), targetSessionID)
	if err == nil {
		t.Fatal("ResolveSessionByID() error = nil, want terminal-state rejection")
	}
	if !strings.Contains(err.Error(), "terminal state") {
		t.Fatalf("ResolveSessionByID() error = %q, want terminal-state message", err)
	}
}

func TestResolveSessionByID_RejectsQueuedSessionWithoutWorkflowRun(t *testing.T) {
	t.Parallel()

	server := newSessionIDResolveServer(t, "queued", nil)
	defer server.Close()

	mgr, _ := newIDTargetedManager(t, server.URL)

	_, err := mgr.ResolveSessionByID(context.Background(), targetSessionID)
	if err == nil {
		t.Fatal("ResolveSessionByID() error = nil, want queued-session rejection")
	}
	if !strings.Contains(err.Error(), "no workflow run ID") {
		t.Fatalf("ResolveSessionByID() error = %q, want missing-workflow-run message", err)
	}
}

func TestResolveSessionByID_LeavesLocalSessionStateUntouched(t *testing.T) {
	t.Parallel()

	runID := targetWorkflowRunID
	server := newSessionIDResolveServer(t, "running", &runID)
	defer server.Close()

	mgr, workDir := newIDTargetedManager(t, server.URL)

	// Seed a session cache that a parallel CLI process would own.
	cachePath := filepath.Join(workDir, ".revyl", "device-sessions.json")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatalf("mkdir .revyl: %v", err)
	}
	original := []byte(`{"active":4,"next_index":5,"sessions":[]}`)
	if err := os.WriteFile(cachePath, original, 0o600); err != nil {
		t.Fatalf("write session cache: %v", err)
	}

	session, err := mgr.ResolveSessionByID(context.Background(), targetSessionID)
	if err != nil {
		t.Fatalf("ResolveSessionByID() error = %v, want nil", err)
	}
	if _, err := mgr.WorkerRequestOnSession(context.Background(), session, "/health", nil); err != nil {
		t.Fatalf("WorkerRequestOnSession() error = %v, want nil", err)
	}

	after, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read session cache: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("device-sessions.json changed during ID-targeted command:\n got %s\nwant %s", after, original)
	}
	if _, err := mgr.ResolveSession(0); err == nil {
		t.Fatal("ResolveSession(0) error = nil, want no local session registered by ID targeting")
	}
}

func TestDisablePersistence_SuppressesCacheWrites(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	mgr := NewDeviceSessionManager(nil, workDir)
	mgr.DisablePersistence()

	mgr.mu.Lock()
	mgr.persistSessions()
	mgr.mu.Unlock()

	if _, err := os.Stat(filepath.Join(workDir, ".revyl", "device-sessions.json")); !os.IsNotExist(err) {
		t.Fatalf("device-sessions.json stat error = %v, want not-exist with persistence disabled", err)
	}
}
