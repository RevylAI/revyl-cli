package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
)

const (
	startWorkflowRunID = "dddddddd-dddd-4ddd-8ddd-ddddddddddd1"
	startSessionID     = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeee1"
	ownProofRunID      = "b3cd415d-0000-0000-0000-000000000001"
	siblingProofRunID  = "bc610fc1-0000-0000-0000-000000000001"
)

type startServer struct {
	*httptest.Server
	accepted      atomic.Bool
	workerLookups atomic.Int32
}

func newStartServer(t *testing.T, startBody string) *startServer {
	t.Helper()
	server := &startServer{}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/execution/start_device":
			_, _ = w.Write([]byte(startBody))
		case r.URL.Path == "/api/v1/execution/streaming/worker-connection/"+startWorkflowRunID:
			server.workerLookups.Add(1)
			if !server.accepted.Load() {
				t.Error("waited for the worker before reporting the accepted session")
			}
			_, _ = w.Write([]byte(`{"status":"ready","workflow_run_id":"` + startWorkflowRunID + `","worker_ws_url":"ws://` + r.Host + `/ws/stream?token=test"}`))
		case r.URL.Path == "/api/v1/execution/device-proxy/"+startWorkflowRunID+"/health":
			_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestStartSession_ReportsTheAcceptedSessionBeforeWaitingForTheDevice(t *testing.T) {
	server := newStartServer(t, `{"workflow_run_id":"`+startWorkflowRunID+`","session_id":"`+startSessionID+`","status":"initializing","platform":"ios"}`)
	mgr := NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", server.URL), t.TempDir())
	var gotSessionID string
	index, session, err := mgr.StartSession(context.Background(), StartSessionOptions{
		Platform: "ios",
		OnStartAccepted: func(sessionID string) {
			gotSessionID = sessionID
			server.accepted.Store(true)
		},
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	mgr.StopIdleTimer(index)
	if gotSessionID != startSessionID {
		t.Fatalf("OnStartAccepted(%q), want %q", gotSessionID, startSessionID)
	}
	if session.SessionID != startSessionID || server.workerLookups.Load() == 0 {
		t.Fatalf("session %q after %d worker lookups, want the accepted session once its worker is ready", session.SessionID, server.workerLookups.Load())
	}
}

func TestSyncSessions_ListsTheProofRunsOwnStartingSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/execution/device-sessions/active":
			_, _ = w.Write([]byte(`{
				"org_id":"org-1",
				"proof_run_id":"` + ownProofRunID + `",
				"sessions":[
					{"id":"own-starting","org_id":"org-1","platform":"ios","source":"cli","status":"queued",
					 "source_metadata":{"scm_review_run_id":"` + ownProofRunID + `"}},
					{"id":"sibling-proof","org_id":"org-1","platform":"android","source":"cli","status":"starting",
					 "source_metadata":{"scm_review_run_id":"` + siblingProofRunID + `"}}
				]
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	mgr := &DeviceSessionManager{
		apiClient:         api.NewClientWithBaseURL("test-api-key", server.URL),
		workDir:           t.TempDir(),
		sessions:          map[int]*DeviceSession{},
		ownedSessions:     map[int]bool{},
		idleTimerDisabled: make(map[int]bool),
		idleTimers:        make(map[int]*time.Timer),
		screenAnchors:     make(map[int]*screenAnchorState),
		activeIndex:       -1,
		orgID:             "org-1",
	}

	if err := mgr.SyncSessions(context.Background()); err != nil {
		t.Fatalf("SyncSessions() error = %v", err)
	}

	starting := mgr.UnreachableSessions()
	if len(starting) != 1 || starting[0].SessionID != "own-starting" || starting[0].BackendStatus != "queued" {
		t.Fatalf("unreachable sessions = %+v, want only the run's own queued session", starting)
	}
	if starting[0].Index != UnattachedSessionIndex {
		t.Fatalf("index = %d, want %d until its device is ready", starting[0].Index, UnattachedSessionIndex)
	}
}
