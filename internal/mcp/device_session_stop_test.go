package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
)

func TestStopAllSessionOutcomes(t *testing.T) {
	const pending = `{"success":true,"request_accepted":true,"session_settled":true,"device_released":false}`
	const released = `{"success":true,"request_accepted":true,"session_settled":true,"device_released":true}`
	const rejected = `{"success":false,"request_accepted":false,"message":"stop rejected"}`
	for _, tc := range []struct {
		name                        string
		responses                   []string
		accepted, settled, released bool
		retained                    int
	}{
		{"empty", nil, true, true, true, 0},
		{"pending", []string{pending, pending}, true, true, false, 2},
		{"released", []string{released, released}, true, true, true, 0},
		{"released_and_pending", []string{released, pending}, true, true, false, 1},
		{"pending_and_rejected", []string{pending, rejected}, false, false, false, 2},
		{"legacy", []string{`{"success":true,"db_updated":true}`}, true, false, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/api/v1/entity/users/get_user_uuid":
					fmt.Fprint(w, `{"user_id":"user-1","org_id":"org-1","email":"test@example.test"}`)
				case strings.HasSuffix(r.URL.Path, "/active"):
					fmt.Fprint(w, `{"org_id":"org-1","sessions":[]}`)
				case r.Method == http.MethodGet:
					fmt.Fprint(w, `{"status":"running"}`)
				default:
					for i, response := range tc.responses {
						if strings.HasSuffix(r.URL.Path, fmt.Sprintf("/run-%d", i)) {
							fmt.Fprint(w, response)
							return
						}
					}
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			manager := NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", server.URL), t.TempDir())
			for i := range tc.responses {
				index, err := manager.registerStartedSession(&DeviceSession{
					SessionID: fmt.Sprintf("session-%d", i), WorkflowRunID: fmt.Sprintf("run-%d", i),
					Platform: "ios", StartedAt: time.Now(), LastActivity: time.Now(), IdleTimeout: time.Hour,
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { manager.StopIdleTimer(index) })
			}
			srv := &Server{sessionMgr: manager}
			_, output, err := srv.handleStopDeviceSession(context.Background(), nil, StopDeviceSessionInput{All: true})
			if err != nil {
				t.Fatal(err)
			}
			if output.Success != tc.accepted || output.RequestAccepted != tc.accepted || output.SessionSettled != tc.settled || output.DeviceReleased != tc.released {
				t.Fatalf("unexpected batch outcome: %+v", output)
			}
			if (output.Error != "") == tc.accepted {
				t.Fatalf("unexpected batch error: %q", output.Error)
			}
			if len(output.Results) != len(tc.responses) {
				t.Fatalf("results = %d", len(output.Results))
			}
			for i, body := range tc.responses {
				var response api.CancelDeviceResponse
				if err := json.Unmarshal([]byte(body), &response); err != nil {
					t.Fatal(err)
				}
				outcome := output.Results[i]
				if outcome.SessionIndex != i || outcome.SessionID != fmt.Sprintf("session-%d", i) || outcome.WorkflowRunID != fmt.Sprintf("run-%d", i) || outcome.RequestAccepted != response.StopRequestAccepted() {
					t.Fatalf("unexpected member outcome: %+v", outcome)
				}
				if (outcome.Error != "") == response.StopRequestAccepted() {
					t.Fatalf("unexpected member error: %q", outcome.Error)
				}
			}
			if manager.SessionCount() != tc.retained {
				t.Fatalf("retained = %d, want %d", manager.SessionCount(), tc.retained)
			}
			reloaded := NewDeviceSessionManager(manager.apiClient, manager.workDir)
			reloaded.LoadPersistedSession()
			if reloaded.SessionCount() != tc.retained {
				t.Fatalf("persisted = %d, want %d", reloaded.SessionCount(), tc.retained)
			}
			for i := range tc.responses {
				t.Cleanup(func() { reloaded.StopIdleTimer(i) })
			}
		})
	}
}

func TestDeviceSessionManager_StopAcknowledgementPreservesUnsettledIdentity(t *testing.T) {
	for _, tc := range []struct {
		name         string
		body         string
		wantPending  bool
		wantRetained bool
	}{
		{"rejected", `{"success":false,"message":"stop rejected"}`, false, true},
		{"accepted", `{"success":true,"request_accepted":true,"session_settled":false,"device_released":false}`, true, true},
		{"legacy", `{"success":true,"db_updated":true}`, true, true},
		{"released", `{"success":true,"request_accepted":true,"session_settled":true,"device_released":true}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)
			manager := NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", server.URL), t.TempDir())
			_, err := manager.registerStartedSession(&DeviceSession{
				SessionID: "session-1", WorkflowRunID: "workflow-1", Platform: "ios",
				StartedAt: time.Now(), LastActivity: time.Now(), IdleTimeout: time.Hour,
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { manager.StopIdleTimer(0) })
			err = manager.StopSession(context.Background(), 0)
			var pending *api.DeviceSessionStopPendingError
			if errors.As(err, &pending) != tc.wantPending {
				t.Fatalf("stop error = %v, want pending %v", err, tc.wantPending)
			}
			if (err != nil) != tc.wantRetained {
				t.Fatalf("stop error = %v, want error %v", err, tc.wantRetained)
			}
			if (manager.GetSession(0) != nil) != tc.wantRetained {
				t.Fatalf("session retained = %v, want %v", manager.GetSession(0) != nil, tc.wantRetained)
			}
			reloaded := NewDeviceSessionManager(manager.apiClient, manager.workDir)
			reloaded.LoadPersistedSession()
			t.Cleanup(func() { reloaded.StopIdleTimer(0) })
			if (reloaded.GetSession(0) != nil) != tc.wantRetained {
				t.Fatalf("persisted session retained = %v, want %v", reloaded.GetSession(0) != nil, tc.wantRetained)
			}
		})
	}
}

func TestSyncSessionsRequiresSettlementAndReleaseBeforePruning(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		body         string
		wantRetained bool
		wantError    bool
	}{
		{"stopping", 200, `{"status":"stopping"}`, true, false},
		{"terminal_without_release", 200, `{"status":"completed"}`, true, false},
		{"malformed_release", 200, `{"status":"completed","source_metadata":{"device_released_at":"unknown"}}`, true, false},
		{"released", 200, `{"status":"completed","source_metadata":{"device_released_at":"2026-09-10T12:00:00Z"}}`, false, false},
		{"unavailable", 503, `{"detail":"unavailable"}`, true, true},
		{"inaccessible", 403, `{"detail":"inaccessible"}`, true, true},
		{"missing", 404, `{"detail":"missing"}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/entity/users/get_user_uuid":
					_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-1","email":"test@example.test"}`))
				case "/api/v1/execution/device-sessions/active":
					_, _ = w.Write([]byte(`{"org_id":"org-1","sessions":[]}`))
				case "/api/v1/execution/device-sessions/session-1":
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			manager := NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", server.URL), t.TempDir())
			manager.orgID = "org-1"
			manager.userEmail = "test@example.test"
			_, err := manager.registerStartedSession(&DeviceSession{SessionID: "session-1", WorkflowRunID: "workflow-1", Platform: "ios", StartedAt: time.Now(), LastActivity: time.Now(), IdleTimeout: time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { manager.StopIdleTimer(0) })
			if err := manager.SyncSessions(context.Background()); (err != nil) != tc.wantError {
				t.Fatalf("sync error = %v, want error %v", err, tc.wantError)
			}
			if (manager.GetSession(0) != nil) != tc.wantRetained {
				t.Fatalf("retained = %v, want %v", manager.GetSession(0) != nil, tc.wantRetained)
			}
		})
	}
}
