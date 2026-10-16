package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
)

func runtimeTokenManager(t *testing.T, handler http.HandlerFunc) (*DeviceSessionManager, *DeviceSession) {
	t.Helper()
	backend := httptest.NewServer(handler)
	t.Cleanup(backend.Close)
	manager := NewDeviceSessionManager(api.NewClientWithBaseURL("fixture-key", backend.URL), t.TempDir())
	session := &DeviceSession{Index: 0, SessionID: "fixture-session", WorkflowRunID: "fixture-workflow", Platform: "web", IdleTimeout: 10 * time.Minute}
	manager.sessions[0] = session
	manager.activeIndex = 0
	manager.StopIdleTimer(0)
	return manager, session
}

func TestRuntimeTargetResolutionFailsClosedWithoutBackendGrounding(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		status         int
	}{
		{"missing worker", `{}`, 404}, {"unavailable", `{}`, 503}, {"unsupported", `{}`, 501},
		{"bad JSON", `invalid`, 200}, {"missing coordinates", `{"found":true,"screen_token":"fixture-observation"}`, 200},
		{"missing token", `{"found":true,"x":1,"y":2}`, 200},
		{"negative coordinates", `{"found":true,"x":-1,"y":2,"screen_token":"fixture-observation"}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			manager, session := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if !strings.HasSuffix(r.URL.Path, "/resolve_target") {
					t.Errorf("unexpected fallback request %s", r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.response))
			})
			_, err := manager.ResolveTargetOnSession(context.Background(), session, "fixture target")
			if err == nil || calls != 1 {
				t.Fatalf("resolution must fail without retry or fallback: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestRuntimeResolvedTokenFlowsThroughMCPInputBodies(t *testing.T) {
	for _, action := range []string{"tap", "double_tap", "longpress", "input", "swipe", "clear_text"} {
		t.Run(action, func(t *testing.T) {
			inputs := 0
			manager, _ := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/resolve_target") {
					_, _ = w.Write([]byte(`{"found":true,"x":12,"y":34,"screen_token":"fixture-runtime-observation"}`))
					return
				}
				if !strings.HasSuffix(r.URL.Path, "/"+action) {
					t.Errorf("unexpected action path %s", r.URL.Path)
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil || body["screen_token"] != "fixture-runtime-observation" || body["x"] != float64(12) || body["y"] != float64(34) {
					t.Error("action lost its immutable resolved observation")
				}
				inputs++
				_, _ = w.Write([]byte(`{"success":true}`))
			})
			server := &Server{sessionMgr: manager}
			ctx := context.Background()
			switch action {
			case "tap":
				_, _, _ = server.handleDeviceTap(ctx, nil, DeviceTapInput{Target: "fixture target"})
			case "double_tap":
				_, _, _ = server.handleDeviceDoubleTap(ctx, nil, DeviceDoubleTapInput{Target: "fixture target"})
			case "longpress":
				_, _, _ = server.handleDeviceLongPress(ctx, nil, DeviceLongPressInput{Target: "fixture target"})
			case "input":
				_, _, _ = server.handleDeviceType(ctx, nil, DeviceTypeInput{Target: "fixture target", Text: "fixture input"})
			case "swipe":
				_, _, _ = server.handleDeviceSwipe(ctx, nil, DeviceSwipeInput{Target: "fixture target", Direction: "up"})
			case "clear_text":
				_, _, _ = server.handleDeviceClearText(ctx, nil, DeviceClearTextInput{Target: "fixture target"})
			}
			if inputs != 1 {
				t.Fatalf("physical action requests = %d, want one", inputs)
			}
		})
	}
}

func TestRuntimeScreenshotAnchorKeepsPrivateOpaqueTokenAcrossManagers(t *testing.T) {
	manager, session := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/screenshot") {
			w.Header().Set("X-Revyl-Screen-Token", "fixture-runtime-observation")
			_, _ = w.Write([]byte("fixture-image-bytes"))
			return
		}
		var body api.WorkerResolveTargetRequest
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.ScreenToken != "fixture-runtime-observation" || body.SessionID != sessionIDForRuntimeTest {
			t.Error("anchored resolution lost the persisted runtime token or child")
		}
		_, _ = w.Write([]byte(`{"found":true,"x":1,"y":2,"screen_token":"fixture-runtime-observation"}`))
	})
	image, err := manager.ScreenshotOnSession(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	localToken := manager.MarkScreenshotAnchorWithImage(0, image)
	if localToken == "fixture-runtime-observation" || !strings.HasPrefix(localToken, "screen-") {
		t.Fatal("local and runtime token namespaces were confused")
	}
	imagePath, err := manager.PersistAnchorImage(0, localToken, image)
	if err != nil {
		t.Fatal(err)
	}
	metadataPath := strings.TrimSuffix(imagePath, ".png") + ".json"
	metadata, err := os.ReadFile(metadataPath)
	if err != nil || !strings.Contains(string(metadata), "fixture-runtime-observation") {
		t.Fatal("opaque token was not retained in the private anchor cache")
	}
	for _, path := range []string{imagePath, metadataPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
			t.Fatal("screenshot anchor artifacts must be owner-only")
		}
	}
	reloaded := NewDeviceSessionManager(manager.apiClient, manager.workDir)
	reloaded.sessions[0] = session
	reloaded.StopIdleTimer(0)
	resolved, err := reloaded.ResolveTargetFromAnchor(context.Background(), 0, localToken, "fixture target")
	if err != nil || resolved.ScreenToken != "fixture-runtime-observation" {
		t.Fatalf("persisted anchor resolution failed: %v", err)
	}
	wrongSession := *session
	wrongSession.SessionID = "another-child"
	if _, err := reloaded.RuntimeScreenTokenForAnchor(&wrongSession, localToken); err == nil {
		t.Fatal("anchor was accepted for another child")
	}
	if _, err := reloaded.RuntimeScreenTokenForAnchor(session, "../runtime-token"); err == nil {
		t.Fatal("anchor path traversal was accepted")
	}
}

const sessionIDForRuntimeTest = "fixture-session"

func TestRuntimeInputAcknowledgementLossNeverRepeatsPhysicalRequest(t *testing.T) {
	for _, action := range []string{"tap", "double_tap", "longpress", "drag", "swipe", "input", "clear_text", "key", "navigate", "back", "hover", "scroll", "select", "reload", "forward", "hotkey", "tab_switch", "tab_close", "viewport", "clear_state", "tap_target"} {
		t.Run(action, func(t *testing.T) {
			var physicalCalls atomic.Int32
			manager, session := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/"+action) {
					http.NotFound(w, r)
					return
				}
				physicalCalls.Add(1)
				connection, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = connection.Close()
			})
			_, err := manager.WorkerRequestOnSession(context.Background(), session, "/"+action, map[string]any{"x": 1, "y": 2})
			var workerError *WorkerHTTPError
			if !errors.As(err, &workerError) || !workerError.ExecutionUncertain || !strings.Contains(err.Error(), "do not repeat") || physicalCalls.Load() != 1 {
				t.Fatalf("uncertain acknowledgement repeated input: calls=%d err=%v", physicalCalls.Load(), err)
			}
		})
	}
}

func TestRuntimeAppliedButUnrecordedIsNotReportedAsRetryable(t *testing.T) {
	manager, session := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte(`{"success":false,"error":"observer_failed","action_applied":true,"recorded":false}`))
	})
	_, err := manager.WorkerRequestOnSession(context.Background(), session, "/input", map[string]any{"x": 1, "y": 2, "text": "fixture text"})
	var workerError *WorkerHTTPError
	if !errors.As(err, &workerError) || workerError.ActionApplied == nil || !*workerError.ActionApplied || !strings.Contains(err.Error(), "do not repeat") {
		t.Fatalf("known physical outcome was lost: %v", err)
	}
}

func TestRuntimeStaleTargetIsTypedAndActionable(t *testing.T) {
	manager, session := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"error":"stale_target"}`))
	})
	_, err := manager.WorkerRequestOnSession(context.Background(), session, "/tap", map[string]any{"x": 1, "y": 2})
	var workerError *WorkerHTTPError
	if !errors.As(err, &workerError) || workerError.Code != "stale_target" || !strings.Contains(err.Error(), "fresh screenshot") {
		t.Fatalf("stale target was not actionable: %v", err)
	}
}

func TestRuntimeReadPollsKeepSafeSummaryContractAndPhonePaths(t *testing.T) {
	for _, platform := range []string{"web", "ios"} {
		manager, session := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
			if platform == "web" {
				if !strings.HasSuffix(r.URL.Path, "/logs") && !strings.HasSuffix(r.URL.Path, "/requests") {
					t.Error("web poll used a phone path")
				}
				if r.URL.Query().Get("limit") != "100" || r.URL.Query().Get("max_bytes") != "" {
					t.Error("web poll leaked a phone-only query parameter")
				}
				_, _ = w.Write([]byte(`{"entries":[{"sequence":1,"kind":"request","outcome":"response","url_origin":"https://fixture.test","route_pattern":"/:segment","status_code":200}],"next_cursor":1,"dropped_before_cursor":false}`))
			} else {
				if !strings.HasSuffix(r.URL.Path, "/device_logs") && !strings.HasSuffix(r.URL.Path, "/network_requests") {
					t.Error("phone poll path changed")
				}
				_, _ = w.Write([]byte(`{"success":true,"items":[],"next_cursor":"1"}`))
			}
		})
		session.Platform = platform
		logs, err := manager.PollDeviceLogsOnSession(context.Background(), session, "0", 100)
		if err != nil || (platform == "web" && (logs.RuntimeSummary == nil || len(logs.Items) != 0)) {
			t.Fatal("logs summary parsing failed")
		}
		requests, err := manager.PollNetworkRequestsOnSession(context.Background(), session, "0", 100, 262144)
		if err != nil || (platform == "web" && (requests.RuntimeSummary == nil || len(requests.Items) != 0)) {
			t.Fatal("requests fabricated phone rows or lost summaries")
		}
	}
}

func runtimeReadyFixturePNG(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := png.Encode(&output, image.NewRGBA(image.Rect(0, 0, 1280, 800))); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

const webFixtureAppID = "11111111-1111-4111-8111-111111111111"
const webFixtureBuildID = "22222222-2222-4222-8222-222222222222"

func runtimeProjectedSessionDetail(sessionID, status string) map[string]any {
	detail := map[string]any{"id": sessionID, "org_id": "fixture-org", "platform": "web", "status": status, "build_id": webFixtureBuildID}
	if status == "running" {
		detail["viewer_kind"] = string(api.DeviceSessionDetailResponseViewerKindWhep)
		detail["workflow_run_id"] = startIDWorkflowRunID
	}
	return detail
}

func serveWebFixtureBuild(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/api/v1/apps/" + webFixtureAppID:
		_ = json.NewEncoder(w).Encode(map[string]any{"id": webFixtureAppID, "platform": "Web", "versions_count": 1, "latest_version": "1", "current_version": "1"})
	case "/api/v1/apps/builds/" + webFixtureBuildID:
		_ = json.NewEncoder(w).Encode(map[string]any{"id": webFixtureBuildID, "app_id": webFixtureAppID, "artifact_kind": "web_external"})
	default:
		return false
	}
	return true
}

func TestRuntimeStartInitializingACKRequiresObservedScreenshotAndPreservesIdentityAfterRestartSync(t *testing.T) {
	var screenshots, stops atomic.Int32
	var detailReads atomic.Int32
	sessionID := ""
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveWebFixtureBuild(w, r) {
			return
		}
		switch r.URL.Path {
		case "/api/v1/telemetry/cli-traces":
			w.WriteHeader(http.StatusNoContent)
		case "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"org_id":"fixture-org","email":"fixture@example.test"}`))
		case "/api/v1/execution/start_device":
			var request api.StartDeviceRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			sessionID = request.SessionID
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "initializing", "session_id": sessionID, "workflow_run_id": startIDWorkflowRunID})
		case "/api/v1/execution/device-sessions/" + sessionID:
			if detailReads.Add(1) == 1 {
				_ = json.NewEncoder(w).Encode(runtimeProjectedSessionDetail(sessionID, "starting"))
			} else {
				_ = json.NewEncoder(w).Encode(runtimeProjectedSessionDetail(sessionID, "running"))
			}
		case "/api/v1/execution/device-sessions/active":
			_ = json.NewEncoder(w).Encode(map[string]any{"org_id": "fixture-org", "sessions": []map[string]any{
				{"id": sessionID, "org_id": "fixture-org", "platform": "web", "status": "running", "source": "api", "workflow_run_id": nil},
			}})
		case "/api/v1/execution/device-proxy/" + startIDWorkflowRunID + "/screenshot":
			screenshots.Add(1)
			w.Header().Set("X-Revyl-Screen-Token", "fixture-runtime-observation")
			_, _ = w.Write(runtimeReadyFixturePNG(t))
		case "/api/v1/execution/device/sessions/" + sessionID + "/stop":
			stops.Add(1)
			_, _ = w.Write([]byte(`{"success":true,"request_accepted":true,"session_settled":false,"device_released":false}`))
		default:
			t.Errorf("Web used a phone or unknown endpoint: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(backend.Close)
	root := t.TempDir()
	manager := NewDeviceSessionManager(api.NewClientWithBaseURL("fixture-key", backend.URL), root)
	manager.SetOrgInfo("fixture-org", "fixture@example.test")

	phases := []string{}
	index, session, err := manager.StartSession(context.Background(), StartSessionOptions{Platform: "web", AppID: webFixtureAppID, BuildVersionID: webFixtureBuildID, OnPhase: func(phase string) {
		if phase == "ready" && screenshots.Load() == 0 {
			t.Error("ACK or DTO was mistaken for observed readiness")
		}
		phases = append(phases, phase)
	}})
	if err != nil || session == nil || session.SessionID != sessionID || session.WorkflowRunID != startIDWorkflowRunID || session.WorkerBaseURL != "" || session.WhepURL != nil || len(phases) != 2 || phases[0] != "initializing" || phases[1] != "ready" {
		t.Fatalf("invalid Web start: %#v err=%v phases=%v", session, err, phases)
	}
	manager.StopIdleTimer(index)
	anchor := manager.MarkScreenshotAnchorWithImage(index, manager.screenAnchors[index].ImageBytes)
	if token, err := manager.RuntimeScreenTokenForAnchor(session, anchor); err != nil || token != "fixture-runtime-observation" {
		t.Fatal("startup observation token was lost")
	}
	restarted := NewDeviceSessionManager(manager.apiClient, root)
	if err := restarted.SyncSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if restarted.SessionCount() != 1 || restarted.GetSession(index).SessionID != sessionID || restarted.GetSession(index).WorkflowRunID != startIDWorkflowRunID {
		t.Fatal("sync changed the canonical session identity")
	}
	if _, err := restarted.ResolveSessionByID(context.Background(), sessionID); err != nil {
		t.Fatal("session workflow could not route the session")
	}
	_, attached, err := NewDeviceSessionManager(manager.apiClient, t.TempDir()).AttachBySessionID(context.Background(), sessionID)
	if err != nil || attached.WorkerBaseURL != "" || attached.WhepURL != nil {
		t.Fatalf("Web explicit attach failed: %v", err)
	}
	if err := manager.StopSession(context.Background(), index); err == nil {
		t.Fatal("pending cleanup was reported as settled")
	}
	if stops.Load() != 1 || manager.GetSession(index) == nil {
		t.Fatal("Web manual stop did not preserve pending session cleanup")
	}
	manager.StopIdleTimer(index)
}

func TestRuntimeReadyFailureAndCancellationRequestSessionStopWithoutWorkflowCancel(t *testing.T) {
	for _, missingToken := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing-token=%t", missingToken), func(t *testing.T) {
			var stops atomic.Int32
			sessionID := ""
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if serveWebFixtureBuild(w, r) {
					return
				}
				switch r.URL.Path {
				case "/api/v1/telemetry/cli-traces":
					w.WriteHeader(http.StatusNoContent)
				case "/api/v1/execution/start_device":
					var request api.StartDeviceRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Fatal(err)
					}
					sessionID = request.SessionID
					_, _ = w.Write([]byte(`{"status":"initializing","session_id":"` + sessionID + `","workflow_run_id":"` + startIDWorkflowRunID + `"}`))
				case "/api/v1/execution/device-sessions/" + sessionID:
					if missingToken {
						_ = json.NewEncoder(w).Encode(runtimeProjectedSessionDetail(sessionID, "running"))
					} else {
						_ = json.NewEncoder(w).Encode(runtimeProjectedSessionDetail(sessionID, "starting"))
					}
				case "/api/v1/execution/device-proxy/" + startIDWorkflowRunID + "/screenshot":
					_, _ = w.Write(runtimeReadyFixturePNG(t))
				case "/api/v1/execution/device/sessions/" + sessionID + "/stop":
					stops.Add(1)
					_, _ = w.Write([]byte(`{"success":true,"request_accepted":true,"session_settled":false,"device_released":false}`))
				default:
					t.Errorf("unexpected workflow/phone cleanup endpoint: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(backend.Close)
			manager := NewDeviceSessionManager(api.NewClientWithBaseURL("fixture-key", backend.URL), t.TempDir())

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			index, session, err := manager.StartSession(ctx, StartSessionOptions{Platform: "web", AppID: webFixtureAppID, BuildVersionID: webFixtureBuildID})
			if err == nil || index != -1 || session != nil || manager.SessionCount() != 0 || stops.Load() != 1 {
				t.Fatalf("unobserved Web start became ready or skipped cleanup: stops=%d err=%v", stops.Load(), err)
			}
		})
	}
}

func TestRuntimeIdleStopTargetsSessionAndRetainsPendingCleanup(t *testing.T) {
	var stops atomic.Int32
	stopped := make(chan struct{}, 1)
	manager, session := runtimeTokenManager(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/execution/device/sessions/fixture-session/stop" {
			t.Errorf("idle cleanup used workflow cancel: %s", r.URL.Path)
		}
		stops.Add(1)
		_, _ = w.Write([]byte(`{"success":true,"request_accepted":true,"session_settled":false,"device_released":false}`))
		select {
		case stopped <- struct{}{}:
		default:
		}
	})
	session.IdleTimeout = 20 * time.Millisecond
	manager.mu.Lock()
	manager.ownedSessions[0] = true
	delete(manager.idleTimerDisabled, 0)
	manager.resetIdleTimerForSessionLocked(0, context.Background())
	manager.mu.Unlock()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("idle stop was not requested")
	}
	if stops.Load() == 0 || manager.GetSession(0) == nil {
		t.Fatal("idle pending cleanup lost the session or never requested stop")
	}
	manager.StopIdleTimer(0)
}

func TestRuntimeDetailIdentityAndReadinessAreRequiredBeforeProxyControl(t *testing.T) {
	for _, change := range []map[string]any{{"id": "another-session"}, {"workflow_run_id": nil}, {"status": "starting"}, {"viewer_kind": nil}} {
		calls := 0
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/execution/device-sessions/"+startIDSessionID {
				detail := runtimeProjectedSessionDetail(startIDSessionID, "running")
				for key, value := range change {
					detail[key] = value
				}
				_ = json.NewEncoder(w).Encode(detail)
				return
			}
			calls++
			http.NotFound(w, r)
		}))
		manager := NewDeviceSessionManager(api.NewClientWithBaseURL("fixture-key", backend.URL), t.TempDir())
		if _, err := manager.ResolveSessionByID(context.Background(), startIDSessionID); err == nil {
			t.Fatalf("invalid Web identity/readiness was accepted: %v", change)
		}
		if _, _, err := manager.AttachBySessionID(context.Background(), startIDSessionID); err == nil {
			t.Fatalf("invalid Web identity/readiness was attached: %v", change)
		}
		if calls != 0 {
			t.Fatal("invalid session detail reached proxy control")
		}
		backend.Close()
	}
}
