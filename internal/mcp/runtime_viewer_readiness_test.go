package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestRuntimeViewerReadinessRequiresCurrentWHEPContract(t *testing.T) {
	for _, tc := range []struct {
		name, status, viewerKind string
		ready                    bool
	}{
		{name: "running WHEP", status: "running", viewerKind: "whep", ready: true},
		{name: "obsolete MJPEG", status: "running", viewerKind: "mjpeg"},
		{name: "no viewer", status: "running", viewerKind: "none"},
		{name: "missing viewer", status: "running"},
		{name: "starting WHEP", status: "starting", viewerKind: "whep"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, operation := range []string{"resolve", "observe", "attach"} {
				t.Run(operation, func(t *testing.T) {
					detailReads, screenshotCalls := 0, 0
					backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
						switch request.URL.Path {
						case "/api/v1/execution/device-sessions/" + startIDSessionID:
							detailReads++
							detail := runtimeProjectedSessionDetail(startIDSessionID, tc.status)
							detail["workflow_run_id"] = startIDWorkflowRunID
							if tc.viewerKind == "" {
								delete(detail, "viewer_kind")
							} else {
								detail["viewer_kind"] = tc.viewerKind
							}
							if err := json.NewEncoder(w).Encode(detail); err != nil {
								t.Error(err)
							}
						case "/api/v1/execution/device-proxy/" + startIDWorkflowRunID + "/screenshot":
							screenshotCalls++
							w.Header().Set("X-Revyl-Screen-Token", "fixture-observation")
							_, _ = w.Write(runtimeReadyFixturePNG(t))
						default:
							t.Errorf("unexpected readiness request: %s", request.URL.Path)
							http.NotFound(w, request)
						}
					}))
					t.Cleanup(backend.Close)
					manager := NewDeviceSessionManager(api.NewClientWithBaseURL("fixture-key", backend.URL), t.TempDir())
					session := &DeviceSession{Index: UnattachedSessionIndex, SessionID: startIDSessionID, WorkflowRunID: startIDWorkflowRunID, Platform: "web"}
					var err error
					switch operation {
					case "resolve":
						_, err = manager.ResolveSessionByID(context.Background(), startIDSessionID)
					case "observe":
						_, _, err = manager.readRuntimeReadyLocked(context.Background(), session)
					case "attach":
						index, _, attachErr := manager.AttachBySessionID(context.Background(), startIDSessionID)
						err = attachErr
						if attachErr == nil {
							manager.StopIdleTimer(index)
						}
					}
					if (err == nil) != tc.ready {
						t.Fatalf("readiness=%t, want %t: %v", err == nil, tc.ready, err)
					}
					wantScreenshots := 0
					if tc.ready && operation != "resolve" {
						wantScreenshots = 1
					}
					if screenshotCalls != wantScreenshots || detailReads == 0 {
						t.Fatalf("unexpected readiness observation: details=%d screenshots=%d, want screenshots=%d", detailReads, screenshotCalls, wantScreenshots)
					}
				})
			}
		})
	}
}

func TestRuntimeViewerWHEPDiscoveryPreservesObservedReadiness(t *testing.T) {
	for _, viewerKind := range []string{"whep", "mjpeg", "none"} {
		t.Run(viewerKind, func(t *testing.T) {
			screenshotCalls := 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/api/v1/entity/users/get_user_uuid":
					_, _ = w.Write([]byte(`{"org_id":"fixture-org","email":"fixture@example.test"}`))
				case "/api/v1/execution/device-sessions/active":
					_, _ = w.Write([]byte(`{"org_id":"fixture-org","sessions":[{"id":"` + startIDSessionID + `","org_id":"fixture-org","platform":"web","status":"running","source":"api"}]}`))
				case "/api/v1/execution/device-sessions/" + startIDSessionID:
					detail := runtimeProjectedSessionDetail(startIDSessionID, "running")
					detail["viewer_kind"] = viewerKind
					if err := json.NewEncoder(w).Encode(detail); err != nil {
						t.Error(err)
					}
				case "/api/v1/execution/device-proxy/" + startIDWorkflowRunID + "/screenshot":
					screenshotCalls++
					w.Header().Set("X-Revyl-Screen-Token", "fixture-observation")
					_, _ = w.Write(runtimeReadyFixturePNG(t))
				default:
					t.Errorf("unexpected discovery request: %s", request.URL.Path)
					http.NotFound(w, request)
				}
			}))
			t.Cleanup(backend.Close)
			manager := NewDeviceSessionManager(api.NewClientWithBaseURL("fixture-key", backend.URL), t.TempDir())
			manager.SetOrgInfo("fixture-org", "fixture@example.test")
			if err := manager.SyncSessions(context.Background()); err != nil {
				t.Fatal(err)
			}
			if viewerKind == "whep" {
				if manager.SessionCount() != 1 || screenshotCalls != 1 {
					t.Fatalf("ready WHEP session was hidden: sessions=%d screenshots=%d", manager.SessionCount(), screenshotCalls)
				}
				for _, session := range manager.ListSessions() {
					manager.StopIdleTimer(session.Index)
				}
			} else if manager.SessionCount() != 0 || screenshotCalls != 0 {
				t.Fatalf("unready viewer was admitted: sessions=%d screenshots=%d", manager.SessionCount(), screenshotCalls)
			}
		})
	}
}

func TestRuntimeViewerReadinessChangePreservesPhoneResolution(t *testing.T) {
	for _, platform := range []string{"ios", "android"} {
		t.Run(platform, func(t *testing.T) {
			calls := 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				calls++
				if !strings.HasSuffix(request.URL.Path, "/"+startIDSessionID) {
					t.Error("phone resolution probed a worker")
					http.Error(w, "unexpected phone readiness request", http.StatusBadRequest)
					return
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"id": startIDSessionID, "platform": platform, "status": "running", "workflow_run_id": startIDWorkflowRunID}); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(backend.Close)
			manager := NewDeviceSessionManager(api.NewClientWithBaseURL("fixture-key", backend.URL), "")
			session, err := manager.ResolveSessionByID(context.Background(), startIDSessionID)
			if err != nil || session.Platform != platform || session.WorkflowRunID != startIDWorkflowRunID || calls != 1 {
				t.Fatalf("phone readiness changed: calls=%d session=%+v err=%v", calls, session, err)
			}
		})
	}
}
