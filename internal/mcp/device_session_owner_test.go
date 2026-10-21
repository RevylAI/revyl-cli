package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
)

func ownerStringPtr(value string) *string { return &value }

func TestSyncSessionsFailsClosedOnSessionOwner(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		cliEmail             string
		sessionEmail         *string
		cached               bool
		started              bool
		reload               bool
		attached             bool
		selected             bool
		proofRun             bool
		inventoryUnavailable bool
		wantIncluded         bool
	}{
		{name: "unknown CLI email", sessionEmail: ownerStringPtr("colleague@example.com")},
		{name: "unknown session email", cliEmail: "me@example.com"},
		{name: "empty session email", cliEmail: "me@example.com", sessionEmail: ownerStringPtr("")},
		{name: "both emails unknown"},
		{name: "proof metadata is not ownership", proofRun: true},
		{name: "foreign email", cliEmail: "me@example.com", sessionEmail: ownerStringPtr("colleague@example.com")},
		{name: "matching emails", cliEmail: "me@example.com", sessionEmail: ownerStringPtr("me@example.com"), wantIncluded: true},
		{name: "started here", started: true, sessionEmail: ownerStringPtr("colleague@example.com"), wantIncluded: true},
		{name: "started here after reload", started: true, reload: true, sessionEmail: ownerStringPtr("colleague@example.com"), wantIncluded: true},
		{name: "explicitly attached", attached: true, sessionEmail: ownerStringPtr("colleague@example.com"), wantIncluded: true},
		{name: "explicitly attached after reload", attached: true, reload: true, sessionEmail: ownerStringPtr("colleague@example.com"), wantIncluded: true},
		{name: "explicitly selected", cached: true, selected: true, sessionEmail: ownerStringPtr("colleague@example.com"), wantIncluded: true},
		{name: "cached foreign email", cliEmail: "me@example.com", sessionEmail: ownerStringPtr("colleague@example.com"), cached: true, reload: true},
		{name: "cached unknown CLI email", sessionEmail: ownerStringPtr("colleague@example.com"), cached: true, reload: true},
		{name: "cached unknown session email", cliEmail: "me@example.com", cached: true, reload: true},
		{name: "cached foreign with unavailable inventory", cliEmail: "me@example.com", cached: true, reload: true, inventoryUnavailable: true},
		{name: "attached with unavailable inventory", attached: true, reload: true, inventoryUnavailable: true, wantIncluded: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stopCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/entity/users/get_user_uuid":
					_ = json.NewEncoder(w).Encode(map[string]any{"org_id": "org-1", "email": tc.cliEmail})
				case "/api/v1/execution/device-sessions/active":
					if tc.inventoryUnavailable {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					var proofRunID *string
					var metadata *map[string]interface{}
					if tc.proofRun {
						proofRunID = ownerStringPtr(ownProofRunID)
						metadata = &map[string]interface{}{"scm_review_run_id": ownProofRunID}
					}
					_ = json.NewEncoder(w).Encode(api.ActiveDeviceSessionsResponse{
						OrgId: "org-1", ProofRunId: proofRunID,
						Sessions: []api.ActiveDeviceSessionItem{{
							Id: startIDSessionID, OrgId: "org-1", Platform: "ios", Status: "running",
							WorkflowRunId: ownerStringPtr(startIDWorkflowRunID), UserEmail: tc.sessionEmail, SourceMetadata: metadata,
						}},
					})
				case "/api/v1/execution/streaming/worker-connection/" + startIDWorkflowRunID:
					_ = json.NewEncoder(w).Encode(map[string]any{"status": "ready", "worker_ws_url": "ws://" + r.Host + "/ws/stream"})
				case "/api/v1/execution/device-proxy/" + startIDWorkflowRunID + "/health":
					_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
				case "/api/v1/execution/device-sessions/" + startIDSessionID:
					_ = json.NewEncoder(w).Encode(api.DeviceSessionDetail{ID: startIDSessionID, Status: "running", Platform: ownerStringPtr("ios"), WorkflowRunID: ownerStringPtr(startIDWorkflowRunID)})
				case "/api/v1/execution/device/status/cancel/" + startIDWorkflowRunID:
					stopCalls.Add(1)
					_, _ = w.Write([]byte(`{"success":true,"session_settled":true,"device_released":true}`))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			dir := t.TempDir()
			client := api.NewClientWithBaseURL("fixture-key", server.URL)
			mgr := NewDeviceSessionManager(client, dir)
			session := &DeviceSession{SessionID: startIDSessionID, WorkflowRunID: startIDWorkflowRunID, Platform: "ios", StartedAt: time.Now(), IdleTimeout: time.Hour}
			if tc.cached {
				mgr.sessions[0] = session
				mgr.activeIndex, mgr.nextIndex = 0, 1
				mgr.persistAllSessionsForBootstrap()
			}
			if tc.started {
				index, err := mgr.registerStartedSession(session)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { mgr.StopIdleTimer(index) })
			}
			if tc.attached {
				if _, _, err := mgr.AttachBySessionID(context.Background(), startIDSessionID); err != nil {
					t.Fatal(err)
				}
			}
			if tc.selected {
				if err := mgr.SetActive(0); err != nil {
					t.Fatal(err)
				}
			}
			if tc.reload {
				mgr = NewDeviceSessionManager(client, dir)
			}
			err := mgr.SyncSessions(context.Background())
			if (err != nil) != tc.inventoryUnavailable {
				t.Fatalf("SyncSessions() error = %v, inventoryUnavailable=%t", err, tc.inventoryUnavailable)
			}
			resolved, resolveErr := mgr.ResolveSession(-1)
			choice, choiceErr := mgr.ResolveUntargetedSession()
			if tc.wantIncluded {
				if resolveErr != nil || resolved.SessionID != startIDSessionID || choiceErr != nil || choice.Session.SessionID != startIDSessionID {
					t.Fatalf("wanted included session, got default %v/%v and untargeted %v/%v", resolved, resolveErr, choice, choiceErr)
				}
			} else {
				if resolveErr == nil || choiceErr == nil || mgr.SessionCount() != 0 || len(mgr.UnreachableSessions()) != 0 {
					t.Fatalf("unowned session survived sync: default %v/%v, untargeted %v/%v, count %d", resolved, resolveErr, choice, choiceErr, mgr.SessionCount())
				}
				data, err := os.ReadFile(filepath.Join(dir, ".revyl", "device-sessions.json"))
				if err != nil {
					t.Fatal(err)
				}
				var state persistedState
				if err := json.Unmarshal(data, &state); err != nil || (!tc.inventoryUnavailable && len(state.Sessions) != 0) {
					t.Fatalf("unowned session persisted: %s, %v", data, err)
				}
				byID, err := mgr.ResolveSessionByID(context.Background(), startIDSessionID)
				if err != nil || byID.SessionID != startIDSessionID || mgr.SessionCount() != 0 {
					t.Fatalf("explicit ID must still resolve without joining defaults: %v, %v", byID, err)
				}
			}
			result, err := mgr.StopAllSessions(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			wantStops := 0
			if tc.wantIncluded {
				wantStops = 1
			}
			if len(result.Results) != wantStops || int(stopCalls.Load()) != wantStops {
				t.Fatalf("stop-all results=%d backend calls=%d, want %d", len(result.Results), stopCalls.Load(), wantStops)
			}
		})
	}
}
