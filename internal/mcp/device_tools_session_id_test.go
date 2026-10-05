package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
)

const (
	activeSessionID     = "aaaaaaaa-1111-4111-8111-111111111111"
	activeWorkflowRunID = "wf-active"
	otherSessionID      = "bbbbbbbb-2222-4222-8222-222222222222"
	otherWorkflowRunID  = "wf-other"
	endedSessionID      = "cccccccc-3333-4333-8333-333333333333"
)

// sessionIDToolBackend records which workflow run each device action reached
// and which sessions were stopped, resolved by ID, or reported on.
type sessionIDToolBackend struct {
	mu            sync.Mutex
	proxied       []string
	cancelled     []string
	stoppedByID   []string
	resolvedByID  int
	reportedByIDs []string

	// onProxy runs before a device action is answered, so a test can change
	// the server's sessions mid-call.
	onProxy func(run, action string)
}

func (b *sessionIDToolBackend) record(list *[]string, value string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	*list = append(*list, value)
}

func (b *sessionIDToolBackend) snapshot() sessionIDToolBackend {
	b.mu.Lock()
	defer b.mu.Unlock()
	return sessionIDToolBackend{
		proxied:       append([]string(nil), b.proxied...),
		cancelled:     append([]string(nil), b.cancelled...),
		stoppedByID:   append([]string(nil), b.stoppedByID...),
		resolvedByID:  b.resolvedByID,
		reportedByIDs: append([]string(nil), b.reportedByIDs...),
	}
}

// newSessionIDToolServer builds a server tracking two live sessions, with the
// first one active, backed by a fake API that also knows targetSessionID, a
// running session this server does not track.
func newSessionIDToolServer(t *testing.T) (*Server, *sessionIDToolBackend) {
	t.Helper()
	backend := &sessionIDToolBackend{}
	stopResponse := `{"success":true,"request_accepted":true,"session_settled":true,"device_released":true,"message":"stopped"}`
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case strings.HasPrefix(path, "/api/v1/execution/device-proxy/"):
			run, action, _ := strings.Cut(strings.TrimPrefix(path, "/api/v1/execution/device-proxy/"), "/")
			backend.record(&backend.proxied, run+"/"+action)
			if backend.onProxy != nil {
				backend.onProxy(run, action)
			}
			switch action {
			case "health":
				_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
			case "screenshot":
				w.Header().Set("Content-Type", "image/png")
				_, _ = w.Write([]byte("png"))
			case "tap_target":
				_, _ = w.Write([]byte(`{"success":true,"found":true,"x":10,"y":20}`))
			default:
				_, _ = w.Write([]byte(`{"success":true}`))
			}
		case path == "/api/v1/execution/device-sessions/"+targetSessionID:
			backend.mu.Lock()
			backend.resolvedByID++
			backend.mu.Unlock()
			_, _ = w.Write([]byte(`{"id":"` + targetSessionID + `","org_id":"org-1","platform":"ios","status":"running","workflow_run_id":"` + targetWorkflowRunID + `","started_at":"2026-02-19T00:00:00Z"}`))
		case path == "/api/v1/execution/device-sessions/"+activeSessionID || path == "/api/v1/execution/device-sessions/"+endedSessionID:
			backend.mu.Lock()
			backend.resolvedByID++
			backend.mu.Unlock()
			id := strings.TrimPrefix(path, "/api/v1/execution/device-sessions/")
			_, _ = w.Write([]byte(`{"id":"` + id + `","org_id":"org-1","platform":"ios","status":"completed","workflow_run_id":"wf-ended","started_at":"2026-02-19T00:00:00Z","ended_at":"2026-02-19T00:05:00Z"}`))
		case path == "/api/v1/execution/device-sessions/active":
			_, _ = w.Write([]byte(`{"org_id":"org-1","sessions":[
				{"id":"` + activeSessionID + `","org_id":"org-1","platform":"ios","status":"running","workflow_run_id":"` + activeWorkflowRunID + `","user_email":"test@example.com","created_at":"2026-02-19T00:00:00Z","started_at":"2026-02-19T00:00:00Z"},
				{"id":"` + otherSessionID + `","org_id":"org-1","platform":"ios","status":"running","workflow_run_id":"` + otherWorkflowRunID + `","user_email":"test@example.com","created_at":"2026-02-19T00:01:00Z","started_at":"2026-02-19T00:01:00Z"}]}`))
		case path == "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-1","email":"test@example.com","concurrency_limit":1}`))
		case strings.HasPrefix(path, "/api/v1/execution/device/status/cancel/"):
			backend.record(&backend.cancelled, strings.TrimPrefix(path, "/api/v1/execution/device/status/cancel/"))
			_, _ = w.Write([]byte(stopResponse))
		case strings.HasPrefix(path, "/api/v1/execution/device/sessions/") && strings.HasSuffix(path, "/stop"):
			backend.record(&backend.stoppedByID, strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/execution/device/sessions/"), "/stop"))
			_, _ = w.Write([]byte(stopResponse))
		case strings.HasPrefix(path, "/api/v1/reports-v3/reports/by-session/"):
			backend.record(&backend.reportedByIDs, strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/reports-v3/reports/by-session/"), "/context"))
			_, _ = w.Write([]byte(`{"session_status":"completed","total_steps":2}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(apiServer.Close)

	now := time.Now()
	session := func(index int, sessionID, workflowRunID string) *DeviceSession {
		return &DeviceSession{
			Index: index, SessionID: sessionID, WorkflowRunID: workflowRunID, Platform: "ios",
			StartedAt: now, LastActivity: now, IdleTimeout: 5 * time.Minute,
		}
	}
	mgr := &DeviceSessionManager{
		apiClient: api.NewClientWithBaseURL("test-api-key", apiServer.URL),
		sessions: map[int]*DeviceSession{
			0: session(0, activeSessionID, activeWorkflowRunID),
			1: session(1, otherSessionID, otherWorkflowRunID),
		},
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: 0,
		nextIndex:   2,
	}
	t.Cleanup(func() {
		mgr.mu.Lock()
		defer mgr.mu.Unlock()
		for _, timer := range mgr.idleTimers {
			timer.Stop()
		}
	})
	return &Server{apiClient: mgr.apiClient, sessionMgr: mgr}, backend
}

func intInput(value int) *int { return &value }

func TestDeviceToolSessionIDWinsOverTheActiveSession(t *testing.T) {
	srv, backend := newSessionIDToolServer(t)
	x, y := 5, 6

	_, output, err := srv.handleDeviceTap(context.Background(), nil, DeviceTapInput{X: &x, Y: &y, SessionID: otherSessionID})
	if err != nil || !output.Success {
		t.Fatalf("device_tap(session_id) = %+v, %v; want success", output, err)
	}
	if got := backend.snapshot().proxied; len(got) != 1 || got[0] != otherWorkflowRunID+"/tap" {
		t.Fatalf("worker requests = %v, want one tap on the session named by session_id, not the active one", got)
	}
	if srv.sessionMgr.ActiveIndex() != 0 {
		t.Fatalf("active index = %d, want session_id targeting to leave the default target alone", srv.sessionMgr.ActiveIndex())
	}
}

func TestDeviceToolAcceptsSessionIndexAndSessionIDForTheSameSession(t *testing.T) {
	srv, backend := newSessionIDToolServer(t)

	_, output, err := srv.handleDeviceBack(context.Background(), nil, DeviceBackInput{SessionIndex: intInput(1), SessionID: strings.ToUpper(otherSessionID)})
	if err != nil || !output.Success {
		t.Fatalf("device_back(session_index=1, session_id=same) = %+v, %v; want success", output, err)
	}
	if got := backend.snapshot().proxied; len(got) != 1 || got[0] != otherWorkflowRunID+"/back" {
		t.Fatalf("worker requests = %v, want one back on session 1", got)
	}
}

func TestDeviceToolRefusesSessionIndexThatNamesADifferentSession(t *testing.T) {
	tests := []struct {
		name      string
		index     int
		wantIndex string
	}{
		{name: "index of another live session", index: 0, wantIndex: `session_index 0 is session "` + activeSessionID + `"`},
		{name: "index with no session", index: 7, wantIndex: "session_index 7 is no live session"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, backend := newSessionIDToolServer(t)
			srv.sessionMgr.apiClient = nil

			_, output, err := srv.handleScreenshot(context.Background(), nil, ScreenshotInput{SessionIndex: intInput(tc.index), SessionID: otherSessionID})
			if err != nil {
				t.Fatalf("screenshot returned Go error %v", err)
			}
			if output.Success {
				t.Fatal("screenshot succeeded, want a conflict error instead of picking either session")
			}
			for _, want := range []string{
				"conflicting session targets: session_id " + otherSessionID,
				tc.wantIndex,
				"Pass session_id alone",
			} {
				if !strings.Contains(output.Error, want) {
					t.Fatalf("error = %q, want it to contain %q", output.Error, want)
				}
			}
			if got := backend.snapshot().proxied; len(got) != 0 {
				t.Fatalf("worker requests = %v, want none on a conflict", got)
			}
		})
	}
}

func TestDeviceToolResolvesAnUntrackedSessionIDThroughTheBackend(t *testing.T) {
	srv, backend := newSessionIDToolServer(t)

	result, output, err := srv.handleScreenshot(context.Background(), nil, ScreenshotInput{SessionID: targetSessionID})
	if err != nil || !output.Success {
		t.Fatalf("screenshot(untracked session_id) = %+v, %v; want success", output, err)
	}
	if result == nil || len(result.Content) != 1 {
		t.Fatalf("screenshot result = %+v, want the native image", result)
	}
	if output.ScreenToken != "" || output.ImagePath != "" {
		t.Fatalf("screenshot output = %+v, want no anchor for a session this server does not track", output)
	}
	calls := backend.snapshot()
	if calls.resolvedByID != 1 {
		t.Fatalf("by-ID lookups = %d, want 1", calls.resolvedByID)
	}
	if len(calls.proxied) != 1 || calls.proxied[0] != targetWorkflowRunID+"/screenshot" {
		t.Fatalf("worker requests = %v, want one screenshot on the untracked session", calls.proxied)
	}
	if srv.sessionMgr.SessionCount() != 2 || srv.sessionMgr.ActiveIndex() != 0 {
		t.Fatalf("local sessions = %d, active = %d; want resolution by ID to leave local state untouched",
			srv.sessionMgr.SessionCount(), srv.sessionMgr.ActiveIndex())
	}
}

func TestInteractKeepsEveryStepOnTheUntrackedSessionID(t *testing.T) {
	srv, backend := newSessionIDToolServer(t)

	_, output, err := srv.handleInteract(context.Background(), nil, InteractInput{
		Task: "tap Continue", InteractionType: "tap", Target: "Continue button", SessionID: targetSessionID,
	})
	if err != nil || output.Result["success"] != true {
		t.Fatalf("interact(untracked session_id) = %+v, %v; want success", output, err)
	}
	for _, request := range backend.snapshot().proxied {
		if !strings.HasPrefix(request, targetWorkflowRunID+"/") {
			t.Fatalf("worker requests = %v, want every step on the session named by session_id", backend.snapshot().proxied)
		}
	}
	if got := len(backend.snapshot().proxied); got != 3 {
		t.Fatalf("worker requests = %v, want pre-screenshot, tap, and post-screenshot", backend.snapshot().proxied)
	}
}

func TestStopDeviceSessionBySessionIDStopsOnlyThatSession(t *testing.T) {
	t.Run("tracked", func(t *testing.T) {
		srv, backend := newSessionIDToolServer(t)

		_, output, err := srv.handleStopDeviceSession(context.Background(), nil, StopDeviceSessionInput{SessionID: otherSessionID})
		if err != nil || !output.Success {
			t.Fatalf("stop(session_id) = %+v, %v; want success", output, err)
		}
		if got := backend.snapshot().cancelled; len(got) != 1 || got[0] != otherWorkflowRunID {
			t.Fatalf("cancelled runs = %v, want only the named session", got)
		}
		if srv.sessionMgr.SessionCount() != 1 || srv.sessionMgr.GetSession(0) == nil {
			t.Fatalf("remaining sessions = %d, want the active session kept", srv.sessionMgr.SessionCount())
		}
	})
	t.Run("untracked", func(t *testing.T) {
		srv, backend := newSessionIDToolServer(t)

		_, output, err := srv.handleStopDeviceSession(context.Background(), nil, StopDeviceSessionInput{SessionID: targetSessionID})
		if err != nil || !output.Success {
			t.Fatalf("stop(untracked session_id) = %+v, %v; want success", output, err)
		}
		calls := backend.snapshot()
		if len(calls.stoppedByID) != 1 || calls.stoppedByID[0] != targetSessionID || len(calls.cancelled) != 0 {
			t.Fatalf("stopped by ID = %v, cancelled = %v; want one stop by the named ID", calls.stoppedByID, calls.cancelled)
		}
		if srv.sessionMgr.SessionCount() != 2 {
			t.Fatalf("local sessions = %d, want both tracked sessions kept", srv.sessionMgr.SessionCount())
		}
	})
}

func TestGetSessionReportBySessionIDSkipsSessionResolution(t *testing.T) {
	srv, backend := newSessionIDToolServer(t)

	_, output, err := srv.handleGetSessionReport(context.Background(), nil, GetSessionReportInput{SessionID: targetSessionID})
	if err != nil || !output.Success || output.SessionID != targetSessionID {
		t.Fatalf("get_session_report(session_id) = %+v, %v; want the named session's report", output, err)
	}
	calls := backend.snapshot()
	if calls.resolvedByID != 0 || len(calls.reportedByIDs) != 1 || calls.reportedByIDs[0] != targetSessionID {
		t.Fatalf("by-ID lookups = %d, reports = %v; want one report read and no session lookup, so ended sessions work",
			calls.resolvedByID, calls.reportedByIDs)
	}
}

func TestSessionIDReachesAgentsFromStartAndList(t *testing.T) {
	apiServer := newSessionSyncTestServer(t)
	defer apiServer.Close()

	srv := &Server{sessionMgr: NewDeviceSessionManager(api.NewClientWithBaseURL("test-api-key", apiServer.URL), t.TempDir())}
	_, listed, err := srv.handleListDeviceSessions(context.Background(), nil, ListDeviceSessionsInput{})
	if err != nil {
		t.Fatalf("list_device_sessions error = %v", err)
	}
	encoded, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"session_id":"sess-1"`) {
		t.Fatalf("list_device_sessions = %s, want each session's session_id", encoded)
	}

	if got := sessionIDParam(targetSessionID); got != `session_id="`+targetSessionID+`"` {
		t.Fatalf("start next-step params = %q, want the session_id to pass back", got)
	}
}

func TestEveryToolWithSessionIndexAlsoAcceptsSessionID(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-environment-api-key")

	for _, profile := range []Profile{"", ProfileCore, ProfileFull, ProfileDev} {
		t.Run(string(profile), func(t *testing.T) {
			var options []ServerOption
			if profile != "" {
				options = append(options, WithProfile(profile))
			}
			server, err := NewServer("test", false, options...)
			if err != nil {
				t.Fatalf("NewServer(): %v", err)
			}
			for _, tool := range listServerTools(t, server) {
				encoded, err := json.Marshal(tool.InputSchema)
				if err != nil {
					t.Fatal(err)
				}
				var schema struct {
					Properties map[string]json.RawMessage `json:"properties"`
					Required   []string                   `json:"required"`
				}
				if err := json.Unmarshal(encoded, &schema); err != nil {
					t.Fatal(err)
				}
				if _, hasIndex := schema.Properties["session_index"]; !hasIndex {
					continue
				}
				if _, hasID := schema.Properties["session_id"]; !hasID {
					t.Errorf("%s accepts session_index but not session_id", tool.Name)
				}
				for _, required := range schema.Required {
					if required == "session_id" {
						t.Errorf("%s requires session_id; it must stay optional", tool.Name)
					}
				}
			}
		})
	}
}

func TestInteractFailsInsteadOfSwitchingDevicesWhenItsIndexIsReused(t *testing.T) {
	srv, backend := newSessionIDToolServer(t)
	mgr := srv.sessionMgr
	var swapped sync.Once
	backend.onProxy = func(run, action string) {
		if run != activeWorkflowRunID || action != "screenshot" {
			return
		}
		swapped.Do(func() {
			mgr.mu.Lock()
			defer mgr.mu.Unlock()
			now := time.Now()
			mgr.sessions[0] = &DeviceSession{
				Index: 0, SessionID: "dddddddd-4444-4444-8444-444444444444", WorkflowRunID: "wf-new", Platform: "ios",
				StartedAt: now, LastActivity: now, IdleTimeout: 5 * time.Minute,
			}
		})
	}

	_, output, err := srv.handleInteract(context.Background(), nil, InteractInput{
		Task: "tap Continue", InteractionType: "tap", Target: "Continue button", SessionID: activeSessionID,
	})
	if err != nil {
		t.Fatalf("interact returned Go error %v", err)
	}
	if output.Result["success"] == true {
		t.Fatalf("interact = %+v, want failure once its session ended and index 0 became another device", output)
	}
	if reason, _ := output.Result["error"].(string); !strings.Contains(reason, "is in terminal state") {
		t.Fatalf("interact error = %q, want the ended-session reason", reason)
	}
	for _, request := range backend.snapshot().proxied {
		if strings.HasPrefix(request, "wf-new/") {
			t.Fatalf("worker requests = %v, want nothing sent to the device that reused index 0", backend.snapshot().proxied)
		}
	}
}

func TestDeviceSessionDoctorHonorsBothSelectors(t *testing.T) {
	t.Run("index only diagnoses that index", func(t *testing.T) {
		srv, backend := newSessionIDToolServer(t)

		_, output, err := srv.handleDeviceSession(context.Background(), nil, DeviceSessionInput{Action: "doctor", SessionIndex: intInput(1)})
		if err != nil || output.Outcome.OperationStatus == "failed" {
			t.Fatalf("device_session(doctor, session_index=1) = %+v, %v; want a completed diagnosis", output, err)
		}
		if got := backend.snapshot().proxied; len(got) != 1 || got[0] != otherWorkflowRunID+"/health" {
			t.Fatalf("worker requests = %v, want the health check on session 1, not the active session", got)
		}
	})
	t.Run("conflict is an error", func(t *testing.T) {
		srv, backend := newSessionIDToolServer(t)

		toolResult, output, err := srv.handleDeviceSession(context.Background(), nil, DeviceSessionInput{
			Action: "doctor", SessionIndex: intInput(0), SessionID: otherSessionID,
		})
		if err != nil {
			t.Fatalf("device_session(doctor) returned Go error %v", err)
		}
		reason, _ := output.Result["error"].(string)
		if toolResult == nil || !toolResult.IsError || output.Outcome.OperationStatus != "failed" || !strings.Contains(reason, "conflicting session targets") {
			t.Fatalf("device_session(doctor, conflict) = %+v, error %q; want a failed outcome naming the conflict", output, reason)
		}
		if got := backend.snapshot().proxied; len(got) != 0 {
			t.Fatalf("worker requests = %v, want no health check on either session", got)
		}
	})
}

func TestGetSessionInfoReportsTheTargetingOutcome(t *testing.T) {
	t.Run("untracked session omits the index", func(t *testing.T) {
		srv, _ := newSessionIDToolServer(t)

		_, output, err := srv.handleGetSessionInfo(context.Background(), nil, GetSessionInfoInput{SessionID: targetSessionID})
		if err != nil || !output.Active || output.SessionID != targetSessionID {
			t.Fatalf("get_session_info(untracked) = %+v, %v; want the session", output, err)
		}
		encoded, err := json.Marshal(output)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "session_index") {
			t.Fatalf("get_session_info = %s, want no session_index for a session this server does not track", encoded)
		}
	})
	t.Run("tracked session keeps its index", func(t *testing.T) {
		srv, _ := newSessionIDToolServer(t)

		_, output, err := srv.handleGetSessionInfo(context.Background(), nil, GetSessionInfoInput{SessionID: otherSessionID})
		if err != nil || output.SessionIndex == nil || *output.SessionIndex != 1 {
			t.Fatalf("get_session_info(tracked) = %+v, %v; want session_index 1", output, err)
		}
	})
	tests := []struct {
		name      string
		input     GetSessionInfoInput
		wantError string
		wantNext  string
	}{
		{name: "ended session", input: GetSessionInfoInput{SessionID: endedSessionID}, wantError: "is in terminal state", wantNext: "get_session_report"},
		{name: "conflict", input: GetSessionInfoInput{SessionIndex: intInput(0), SessionID: otherSessionID}, wantError: "conflicting session targets", wantNext: "list_device_sessions"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newSessionIDToolServer(t)

			_, output, err := srv.handleGetSessionInfo(context.Background(), nil, tc.input)
			if err != nil || output.Active {
				t.Fatalf("get_session_info = %+v, %v; want an inactive result", output, err)
			}
			if !strings.Contains(output.Error, tc.wantError) {
				t.Fatalf("error = %q, want it to contain %q", output.Error, tc.wantError)
			}
			if len(output.NextSteps) == 0 || output.NextSteps[0].Tool != tc.wantNext {
				t.Fatalf("next steps = %+v, want %s first", output.NextSteps, tc.wantNext)
			}
		})
	}
}

func TestServeAnalyticsRecordTheMostExplicitSessionTargetMode(t *testing.T) {
	run := func(t *testing.T, calls func(ctx context.Context, srv *Server)) map[string]interface{} {
		t.Helper()
		srv, _ := newSessionIDToolServer(t)
		var captured analytics.TelemetryPayload
		recorder := analytics.NewWithFlusher(analytics.Config{}, func(payload analytics.TelemetryPayload) { captured = payload })
		commandRun := recorder.StartCommand(&cobra.Command{Use: "serve"}, nil)
		calls(analytics.ContextWithCommandRun(context.Background(), commandRun), srv)
		commandRun.Complete(nil)
		recorder.Flush()
		if len(captured.Events) != 2 {
			t.Fatalf("captured %d lifecycle events, want start and terminal", len(captured.Events))
		}
		return captured.Events[1].Properties
	}

	terminal := run(t, func(ctx context.Context, srv *Server) {
		_, _, _ = srv.handleDeviceBack(ctx, nil, DeviceBackInput{})
		_, _, _ = srv.handleDeviceBack(ctx, nil, DeviceBackInput{SessionID: otherSessionID})
		_, _, _ = srv.handleDeviceBack(ctx, nil, DeviceBackInput{SessionIndex: intInput(0)})
		_, _, _ = srv.handleDeviceBack(ctx, nil, DeviceBackInput{})
	})
	if terminal["domain"] != "mcp_session_target" || terminal["domain_status"] != "session_id" {
		t.Fatalf("terminal properties = %+v, want mcp_session_target = session_id after any session_id call", terminal)
	}
	for _, value := range terminal {
		if text, ok := value.(string); ok && strings.Contains(text, otherSessionID) {
			t.Fatalf("terminal properties = %+v, want no session identifiers", terminal)
		}
	}

	terminal = run(t, func(ctx context.Context, srv *Server) {
		_, _, _ = srv.handleInteract(ctx, nil, InteractInput{Task: "tap Continue", InteractionType: "tap", Target: "Continue button"})
	})
	if terminal["domain_status"] != "active" {
		t.Fatalf("terminal properties = %+v, want active: interact pins its own steps by ID, which is not the caller's choice", terminal)
	}
}

// The serve command's analytics run rides on the context passed to Run, so a
// tool call through the SDK must still see it.
func TestSessionTargetModeReachesTheServeEventThroughTheSDK(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-environment-api-key")
	server, err := NewServer("test", false)
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	fake, _ := newSessionIDToolServer(t)
	server.sessionMgr = fake.sessionMgr

	var captured analytics.TelemetryPayload
	recorder := analytics.NewWithFlusher(analytics.Config{}, func(payload analytics.TelemetryPayload) { captured = payload })
	commandRun := recorder.StartCommand(&cobra.Command{Use: "serve"}, nil)
	serveCtx := analytics.ContextWithCommandRun(context.Background(), commandRun)

	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.mcpServer.Connect(serveCtx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server Connect(): %v", err)
	}
	defer serverSession.Close()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "session-target-test", Version: "test"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect(): %v", err)
	}
	defer clientSession.Close()

	if _, err := clientSession.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "device_back",
		Arguments: map[string]any{"session_id": otherSessionID},
	}); err != nil {
		t.Fatalf("CallTool(device_back): %v", err)
	}
	commandRun.Complete(nil)
	recorder.Flush()
	if len(captured.Events) != 2 || captured.Events[1].Properties["domain_status"] != "session_id" {
		t.Fatalf("captured events = %+v, want the serve terminal event to record session_id targeting", captured.Events)
	}
}
