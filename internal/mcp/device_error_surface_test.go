package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
)

// mcpToolCallPattern matches MCP tool-call syntax such as screenshot(). A
// shell user can't run a tool call, so CLI-surface errors must never contain it.
var mcpToolCallPattern = regexp.MustCompile(`\w+\(\)`)

func TestDeviceNextStepTextCoversEverySurface(t *testing.T) {
	steps := []deviceNextStep{
		nextStepListSessions,
		nextStepSelectSession,
		nextStepStartSession,
		nextStepStartNewSession,
		nextStepStartPlatformSession,
		nextStepScreenshot,
		nextStepDiagnose,
		nextStepDeviceConnecting,
		nextStepRetryStart,
		nextStepRetryWhenIdle,
	}
	for _, surface := range []ErrorSurface{ErrorSurfaceMCP, ErrorSurfaceCLI} {
		if len(deviceNextStepText[surface]) != len(steps) {
			t.Fatalf("surface %d has %d steps, want %d", surface, len(deviceNextStepText[surface]), len(steps))
		}
		for _, step := range steps {
			text := deviceNextStepText[surface][step]
			if text == "" {
				t.Fatalf("surface %d has no text for step %d", surface, step)
			}
			if surface == ErrorSurfaceCLI && (mcpToolCallPattern.MatchString(text) || !strings.Contains(text, "revyl ")) {
				t.Fatalf("CLI step %d = %q, want a revyl command and no MCP tool call", step, text)
			}
		}
	}
}

const (
	surfaceTestWorkflowRunID = "12121212-1212-4212-8212-121212121212"
	surfaceTestSessionID     = "34343434-3434-4434-8434-343434343434"
)

// surfaceTestBackend fakes the backend endpoints device-session errors pass
// through: device start, worker connection status, and the worker proxy.
type surfaceTestBackend struct {
	server            *httptest.Server
	connectionStatus  string
	connectionMessage string
	proxyStatus       map[string]int
	executeStepCalls  atomic.Int32
}

func newSurfaceTestBackend(t *testing.T, connectionStatus, connectionMessage string, proxyStatus map[string]int) *surfaceTestBackend {
	t.Helper()
	backend := &surfaceTestBackend{
		connectionStatus:  connectionStatus,
		connectionMessage: connectionMessage,
		proxyStatus:       proxyStatus,
	}
	proxyPrefix := "/api/v1/execution/device-proxy/" + surfaceTestWorkflowRunID + "/"
	backend.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/execution/start_device":
			fmt.Fprintf(w, `{"workflow_run_id":%q,"session_id":%q}`, surfaceTestWorkflowRunID, surfaceTestSessionID)
		case r.URL.Path == "/api/v1/execution/streaming/worker-connection/"+surfaceTestWorkflowRunID:
			fmt.Fprintf(w, `{"status":%q,"workflow_run_id":%q,"message":%q}`, backend.connectionStatus, surfaceTestWorkflowRunID, backend.connectionMessage)
		case strings.HasPrefix(r.URL.Path, "/api/v1/execution/device/status/cancel/"):
			_, _ = w.Write([]byte(`{"success":true,"request_accepted":true,"session_settled":true,"device_released":true}`))
		case strings.HasPrefix(r.URL.Path, proxyPrefix):
			action := strings.TrimPrefix(r.URL.Path, proxyPrefix)
			if action == "execute_step" {
				backend.executeStepCalls.Add(1)
			}
			if status, ok := backend.proxyStatus[action]; ok {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"detail":"worker detail"}`))
				return
			}
			if action == "resolve_target" {
				_, _ = w.Write([]byte(`{"found":false,"error":"could not locate 'Sign In' in the screenshot"}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(backend.server.Close)
	return backend
}

func (b *surfaceTestBackend) manager(t *testing.T, surface ErrorSurface, sessions ...*DeviceSession) *DeviceSessionManager {
	t.Helper()
	mgr := NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", b.server.URL), "")
	mgr.SetErrorSurface(surface)
	for _, session := range sessions {
		mgr.sessions[session.Index] = session
	}
	return mgr
}

func surfaceTestSession(index int) *DeviceSession {
	return &DeviceSession{
		Index:         index,
		SessionID:     surfaceTestSessionID,
		WorkflowRunID: surfaceTestWorkflowRunID,
		Platform:      "ios",
		StartedAt:     time.Now(),
		LastActivity:  time.Now(),
	}
}

func TestDeviceSessionErrorsRenderPerSurface(t *testing.T) {
	testCases := []struct {
		name              string
		connectionStatus  string
		connectionMessage string
		proxyStatus       map[string]int
		invoke            func(t *testing.T, backend *surfaceTestBackend, surface ErrorSurface) error
		wantCLI           string
		wantMCP           string
	}{
		{
			name: "no active sessions",
			invoke: func(t *testing.T, backend *surfaceTestBackend, surface ErrorSurface) error {
				_, err := backend.manager(t, surface).ResolveSession(-1)
				return err
			},
			wantCLI: "no active device sessions. Start one with 'revyl device start'",
			wantMCP: "no active device sessions. Start one with start_device_session(platform='ios')",
		},
		{
			name: "stale index",
			invoke: func(t *testing.T, backend *surfaceTestBackend, surface ErrorSurface) error {
				_, err := backend.manager(t, surface, surfaceTestSession(0)).ResolveSession(4)
				return err
			},
			wantCLI: "no session at index 4. Run 'revyl device list' to see active sessions",
			wantMCP: "no session at index 4. Call list_device_sessions() to see active sessions",
		},
		{
			name: "multiple sessions",
			invoke: func(t *testing.T, backend *surfaceTestBackend, surface ErrorSurface) error {
				_, err := backend.manager(t, surface, surfaceTestSession(0), surfaceTestSession(1)).ResolveSession(-1)
				return err
			},
			wantCLI: "multiple sessions active. Specify -s <index or session ID>",
			wantMCP: "multiple sessions active. Pass session_id (or session_index), or call list_device_sessions()",
		},
		{
			name:             "worker 503 after the idempotent retry",
			connectionStatus: "ready",
			proxyStatus:      map[string]int{"screenshot": http.StatusServiceUnavailable},
			invoke: func(t *testing.T, backend *surfaceTestBackend, surface ErrorSurface) error {
				mgr := backend.manager(t, surface, surfaceTestSession(0))
				_, err := mgr.ScreenshotOnSession(context.Background(), mgr.GetSession(0))
				return err
			},
			wantCLI: "worker returned 503 on /screenshot. The device is still connecting",
			wantMCP: "worker returned 503 on /screenshot. The device may not be fully connected yet -- wait a few seconds and retry, or call device_doctor() to diagnose",
		},
		{
			name:             "worker 500",
			connectionStatus: "ready",
			proxyStatus:      map[string]int{"hierarchy": http.StatusInternalServerError},
			invoke: func(t *testing.T, backend *surfaceTestBackend, surface ErrorSurface) error {
				mgr := backend.manager(t, surface, surfaceTestSession(0))
				_, err := mgr.WorkerRequestOnSession(context.Background(), mgr.GetSession(0), "/hierarchy", nil)
				return err
			},
			wantCLI: "worker returned 500 on /hierarchy. Run 'revyl device doctor' to check worker health",
			wantMCP: "worker returned 500 on /hierarchy. Call device_doctor() to check worker health",
		},
		{
			name:              "worker 404 after the session ended",
			connectionStatus:  "stopped",
			connectionMessage: "Idle timeout reached",
			proxyStatus:       map[string]int{"screenshot": http.StatusNotFound},
			invoke: func(t *testing.T, backend *surfaceTestBackend, surface ErrorSurface) error {
				mgr := backend.manager(t, surface, surfaceTestSession(0))
				_, err := mgr.ScreenshotOnSession(context.Background(), mgr.GetSession(0))
				return err
			},
			wantCLI: "worker returned 404 on /screenshot. session idle timeout. Start a new session with 'revyl device start'",
			wantMCP: "worker returned 404 on /screenshot. session idle timeout. Start a new session with start_device_session()",
		},
		{
			name:             "worker 404 while the session still runs",
			connectionStatus: "ready",
			proxyStatus:      map[string]int{"hierarchy": http.StatusNotFound},
			invoke: func(t *testing.T, backend *surfaceTestBackend, surface ErrorSurface) error {
				mgr := backend.manager(t, surface, surfaceTestSession(0))
				_, err := mgr.WorkerRequestForSession(context.Background(), 0, "/hierarchy", nil)
				return err
			},
			wantCLI: "worker returned 404 on /hierarchy. The device session did not accept this action and may have ended. Run 'revyl device list'",
			wantMCP: "worker returned 404 on /hierarchy. The device session did not accept this action and may have ended. Call list_device_sessions()",
		},
		{
			name:        "worker 409 on a live step",
			proxyStatus: map[string]int{"execute_step": http.StatusConflict},
			invoke: func(t *testing.T, backend *surfaceTestBackend, surface ErrorSurface) error {
				mgr := backend.manager(t, surface, surfaceTestSession(0))
				_, err := mgr.ExecuteLiveStepOnSession(context.Background(), mgr.GetSession(0), LiveStepRequest{StepType: "instruction"})
				return err
			},
			wantCLI: "worker returned 409 on /execute_step. The device session is busy with another step or app install. Wait for it to finish, then retry; run 'revyl device screenshot",
			wantMCP: "worker returned 409 on /execute_step. The device session is busy with another step or app install. Wait for it to finish, then retry; call screenshot()",
		},
		{
			name:             "worker 503 on a live step is not retried",
			connectionStatus: "ready",
			proxyStatus:      map[string]int{"execute_step": http.StatusServiceUnavailable},
			invoke: func(t *testing.T, backend *surfaceTestBackend, surface ErrorSurface) error {
				mgr := backend.manager(t, surface, surfaceTestSession(0))
				_, err := mgr.ExecuteLiveStepOnSession(context.Background(), mgr.GetSession(0), LiveStepRequest{StepType: "instruction"})
				if calls := backend.executeStepCalls.Load(); calls != 1 {
					t.Fatalf("execute_step calls = %d, want 1: non-idempotent actions are never retried", calls)
				}
				return err
			},
			wantCLI: "worker returned 503 on /execute_step. The device is still connecting",
			wantMCP: "worker returned 503 on /execute_step. The device may not be fully connected yet",
		},
		{
			name: "grounding miss",
			invoke: func(t *testing.T, backend *surfaceTestBackend, surface ErrorSurface) error {
				mgr := backend.manager(t, surface, surfaceTestSession(0))
				_, err := mgr.ResolveTargetOnSession(context.Background(), mgr.GetSession(0), "Sign In")
				return err
			},
			wantCLI: "could not locate 'Sign In' in the screenshot. Run 'revyl device screenshot --out screen.png' to see the current screen, then adjust --target",
			wantMCP: "could not locate 'Sign In' in the screenshot. Try screenshot() to see the current screen and adjust the target description",
		},
		{
			name:              "device start worker never ready",
			connectionStatus:  "stopped",
			connectionMessage: "Session completed",
			invoke: func(t *testing.T, backend *surfaceTestBackend, surface ErrorSurface) error {
				_, _, err := backend.manager(t, surface).StartSession(context.Background(), StartSessionOptions{Platform: "ios"})
				return err
			},
			wantCLI: "Retry 'revyl device start', or run 'revyl device doctor' to diagnose",
			wantMCP: "Try again or call device_doctor() to diagnose",
		},
	}

	for _, tc := range testCases {
		for _, surface := range []ErrorSurface{ErrorSurfaceCLI, ErrorSurfaceMCP} {
			name := tc.name + "/cli"
			want := tc.wantCLI
			if surface == ErrorSurfaceMCP {
				name = tc.name + "/mcp"
				want = tc.wantMCP
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				backend := newSurfaceTestBackend(t, tc.connectionStatus, tc.connectionMessage, tc.proxyStatus)
				err := tc.invoke(t, backend, surface)
				if err == nil {
					t.Fatal("error = nil, want a device-session failure")
				}
				got := err.Error()
				if !strings.Contains(got, want) {
					t.Fatalf("error = %q, want it to contain %q", got, want)
				}
				if surface == ErrorSurfaceCLI && mcpToolCallPattern.MatchString(got) {
					t.Fatalf("CLI error = %q names an MCP tool call", got)
				}
				var workerErr *WorkerHTTPError
				if code, ok := tc.proxyStatus[strings.TrimPrefix(workerPathInError(got), "/")]; ok && (!errors.As(err, &workerErr) || workerErr.StatusCode != code) {
					t.Fatalf("error %q lost its typed worker status %d", got, code)
				}
			})
		}
	}
}

var workerPathPattern = regexp.MustCompile(`worker returned \d{3} on (/[a-z_]+)`)

func workerPathInError(message string) string {
	match := workerPathPattern.FindStringSubmatch(message)
	if match == nil {
		return ""
	}
	return match[1]
}
