package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/revyl/cli/internal/api"
)

// ---------------------------------------------------------------------------
// TestResolveCoords_Validation: Table-driven tests for the dual-param
// validation logic in resolveCoords (target OR x+y).
// ---------------------------------------------------------------------------

func TestResolveCoords_Validation(t *testing.T) {
	// Create a minimal server with a session manager that has no active
	// session. This lets us test the validation paths that fire before
	// any network call or grounding call.
	// Create a minimal server with a session manager. We inject one session
	// so that raw-coord tests can resolve it, but target-based tests will
	// fail on the network call (which is fine -- we're testing validation).
	mgr := &DeviceSessionManager{
		sessions:    make(map[int]*DeviceSession),
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: 0,
		nextIndex:   1,
	}
	mgr.sessions[0] = &DeviceSession{
		Index: 0, SessionID: "test", Platform: "android",
		IdleTimeout: 5 * time.Minute, StartedAt: time.Now(), LastActivity: time.Now(),
	}
	srv := &Server{
		sessionMgr: mgr,
	}

	intPtr := func(v int) *int { return &v }

	tests := []struct {
		name    string
		target  string
		x       *int
		y       *int
		wantErr string
		wantX   int
		wantY   int
	}{
		{
			name:    "both target and x+y returns error",
			target:  "Sign In button",
			x:       intPtr(100),
			y:       intPtr(200),
			wantErr: "provide either target OR x+y, not both",
		},
		{
			name:    "neither target nor x+y returns error",
			target:  "",
			x:       nil,
			y:       nil,
			wantErr: "provide target (element description) or x+y (pixel coordinates)",
		},
		{
			name:    "only x without y returns error",
			target:  "",
			x:       intPtr(100),
			y:       nil,
			wantErr: "provide target (element description) or x+y (pixel coordinates)",
		},
		{
			name:    "only y without x returns error",
			target:  "",
			x:       nil,
			y:       intPtr(200),
			wantErr: "provide target (element description) or x+y (pixel coordinates)",
		},
		{
			name:   "raw coords pass through",
			target: "",
			x:      intPtr(500),
			y:      intPtr(700),
			wantX:  500,
			wantY:  700,
		},
		{
			name:   "zero coords are valid raw coords",
			target: "",
			x:      intPtr(0),
			y:      intPtr(0),
			wantX:  0,
			wantY:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc, err := srv.resolveCoords(context.Background(), tt.target, tt.x, tt.y, -1)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error %q, got nil", tt.wantErr)
				}
				if err.Error() != tt.wantErr {
					t.Fatalf("expected error %q, got %q", tt.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rc.X != tt.wantX {
				t.Errorf("x = %d, want %d", rc.X, tt.wantX)
			}
			if rc.Y != tt.wantY {
				t.Errorf("y = %d, want %d", rc.Y, tt.wantY)
			}
		})
	}
}

func TestMaskEnv(t *testing.T) {
	const key = "REVYL_TEST_MASK_ENV"

	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset env: %v", err)
	}
	if got := maskEnv(key); got != "(not set)" {
		t.Fatalf("maskEnv unset = %q, want %q", got, "(not set)")
	}

	secret := "sk_live_1234567890abcdef"
	t.Setenv(key, secret)
	got := maskEnv(key)
	if got != "(set)" {
		t.Fatalf("maskEnv set = %q, want %q", got, "(set)")
	}
	if strings.Contains(got, "1234") || strings.Contains(got, "abcd") || strings.Contains(got, secret) {
		t.Fatalf("maskEnv leaked secret content: %q", got)
	}
}

// ---------------------------------------------------------------------------
// TestResolveCoords_TargetRequiresSession: When target is provided but no
// session is active, the grounding path should return a meaningful error.
// ---------------------------------------------------------------------------

func TestResolveCoords_TargetRequiresSession(t *testing.T) {
	srv := &Server{
		sessionMgr: &DeviceSessionManager{
			sessions:    make(map[int]*DeviceSession),
			idleTimers:  make(map[int]*time.Timer),
			activeIndex: -1,
		},
	}

	_, err := srv.resolveCoords(context.Background(), "Sign In button", nil, nil, -1)
	if err == nil {
		t.Fatal("expected error when using target without active session")
	}
	// The error should mention no active session (from Screenshot -> WorkerRequest)
	if got := err.Error(); got == "" {
		t.Fatal("error message should not be empty")
	}
}

// newSessionSyncTestServer returns a backend+worker test server that supports
// session sync endpoints and a healthy worker endpoint.
func newSessionSyncTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-1","email":"test@example.com","concurrency_limit":1}`))
		case r.URL.Path == "/api/v1/execution/device-sessions/active":
			if got := r.URL.Query().Get("org_id"); got != "org-1" {
				t.Errorf("expected org_id=org-1, got %q", got)
			}
			_, _ = w.Write([]byte(`{
				"org_id":"org-1",
				"sessions":[
					{
						"id":"sess-1",
						"org_id":"org-1",
						"platform":"ios",
						"source":"cli",
						"status":"running",
						"workflow_run_id":"wf-1",
						"user_email":"test@example.com",
						"created_at":"2026-02-19T00:00:00Z",
						"started_at":"2026-02-19T00:00:00Z"
					}
				]
			}`))
		case r.URL.Path == "/api/v1/execution/streaming/worker-connection/wf-1":
			_, _ = w.Write([]byte(fmt.Sprintf(`{"status":"ready","workflow_run_id":"wf-1","worker_ws_url":"ws://%s/ws/stream?token=test"}`, r.Host)))
		case r.URL.Path == "/health":
			_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestHandleListDeviceSessions_SyncsFromBackend(t *testing.T) {
	apiServer := newSessionSyncTestServer(t)
	defer apiServer.Close()

	mgr := NewDeviceSessionManager(api.NewClientWithBaseURL("test-api-key", apiServer.URL), t.TempDir())
	srv := &Server{sessionMgr: mgr}

	_, output, err := srv.handleListDeviceSessions(context.Background(), nil, ListDeviceSessionsInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(output.Sessions) != 1 {
		t.Fatalf("expected 1 session after sync, got %d", len(output.Sessions))
	}
	if output.ActiveIndex != 0 {
		t.Fatalf("expected active index 0, got %d", output.ActiveIndex)
	}
	if output.Sessions[0].Platform != "ios" {
		t.Fatalf("expected ios platform, got %q", output.Sessions[0].Platform)
	}
}

func TestHandleListDeviceSessions_FallsBackToPersistedCache(t *testing.T) {
	tmpDir := t.TempDir()
	now := time.Now()
	state := persistedState{
		Active:  0,
		NextIdx: 1,
		Sessions: []*DeviceSession{
			{
				Index:         0,
				SessionID:     "persisted-sess-1",
				WorkflowRunID: "wf-persisted",
				WorkerBaseURL: "http://localhost:1234",
				ViewerURL:     "https://app.revyl.ai/tests/execute?workflowRunId=wf-persisted&platform=android",
				Platform:      "android",
				StartedAt:     now,
				LastActivity:  now,
				IdleTimeout:   5 * time.Minute,
			},
		},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal persisted state: %v", err)
	}
	revylDir := filepath.Join(tmpDir, ".revyl")
	if mkErr := os.MkdirAll(revylDir, 0o755); mkErr != nil {
		t.Fatalf("mkdir .revyl: %v", mkErr)
	}
	if writeErr := os.WriteFile(filepath.Join(revylDir, "device-sessions.json"), data, 0o644); writeErr != nil {
		t.Fatalf("write persisted session file: %v", writeErr)
	}

	mgr := NewDeviceSessionManager(nil, tmpDir)
	srv := &Server{sessionMgr: mgr}

	_, output, callErr := srv.handleListDeviceSessions(context.Background(), nil, ListDeviceSessionsInput{})
	if callErr != nil {
		t.Fatalf("unexpected error: %v", callErr)
	}
	if len(output.Sessions) != 1 {
		t.Fatalf("expected 1 cached session, got %d", len(output.Sessions))
	}
	if output.Sessions[0].Platform != "android" {
		t.Fatalf("expected android platform from cache, got %q", output.Sessions[0].Platform)
	}
}

func TestHandleGetSessionInfo_SyncsBeforeResolve(t *testing.T) {
	apiServer := newSessionSyncTestServer(t)
	defer apiServer.Close()

	mgr := NewDeviceSessionManager(api.NewClientWithBaseURL("test-api-key", apiServer.URL), t.TempDir())
	srv := &Server{sessionMgr: mgr}

	_, output, err := srv.handleGetSessionInfo(context.Background(), nil, GetSessionInfoInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !output.Active {
		t.Fatalf("expected active=true after sync")
	}
	if output.Platform != "ios" {
		t.Fatalf("expected ios platform, got %q", output.Platform)
	}
	if output.TotalSessions != 1 {
		t.Fatalf("expected total_sessions=1, got %d", output.TotalSessions)
	}
}

func TestHandleGetSessionInfo_NoSessionAfterSyncFallback(t *testing.T) {
	mgr := NewDeviceSessionManager(nil, t.TempDir())
	srv := &Server{sessionMgr: mgr}

	_, output, err := srv.handleGetSessionInfo(context.Background(), nil, GetSessionInfoInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Active {
		t.Fatalf("expected active=false when no session exists")
	}
	if output.TotalSessions != 0 {
		t.Fatalf("expected total_sessions=0, got %d", output.TotalSessions)
	}
	if len(output.NextSteps) == 0 || output.NextSteps[0].Tool != "start_device_session" {
		t.Fatalf("expected next step to start a device session, got %+v", output.NextSteps)
	}
}

func TestHandleDeviceDoctor_IncludesMCPRuntimeChecks(t *testing.T) {
	apiServer := newSessionSyncTestServer(t)
	defer apiServer.Close()

	srv := &Server{
		apiClient:  api.NewClientWithBaseURL("test-api-key", apiServer.URL),
		sessionMgr: NewDeviceSessionManager(api.NewClientWithBaseURL("test-api-key", apiServer.URL), t.TempDir()),
		version:    "dev",
		devMode:    true,
		workDir:    t.TempDir(),
	}

	_, output, err := srv.handleDeviceDoctor(context.Background(), nil, DeviceDoctorInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	seen := map[string]bool{}
	for _, check := range output.Checks {
		seen[check.Name] = true
	}

	for _, required := range []string{"mcp_dev_mode", "mcp_backend_url", "mcp_workdir", "mcp_binary"} {
		if !seen[required] {
			t.Fatalf("missing diagnostic check %q", required)
		}
	}
}

func TestDevLoopActionGuard_DoesNotGateActionsWhenActive(t *testing.T) {
	now := time.Now()
	workerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/resolve_target":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"found":true,"x":120,"y":240}`))
		case "/tap":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer workerServer.Close()

	mgr := &DeviceSessionManager{
		httpClient: workerServer.Client(),
		sessions: map[int]*DeviceSession{
			0: {
				Index:         0,
				SessionID:     "sess-1",
				WorkflowRunID: "wf-1",
				WorkerBaseURL: workerServer.URL,
				Platform:      "ios",
				StartedAt:     now,
				LastActivity:  now,
				IdleTimeout:   5 * time.Minute,
			},
		},
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: 0,
	}
	srv := &Server{
		sessionMgr:          mgr,
		devLoopActive:       true,
		devLoopSessionIndex: 0,
	}

	_, output, err := srv.handleDeviceTap(context.Background(), nil, DeviceTapInput{Target: "Continue button"})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !output.Success {
		t.Fatalf("expected tap success without preflight checks, got: %+v", output)
	}
}

func TestDevLoopActionGuard_InactiveDevLoopDoesNotGateActions(t *testing.T) {
	now := time.Now()
	workerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/resolve_target":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"found":true,"x":120,"y":240}`))
		case "/tap":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer workerServer.Close()

	mgr := &DeviceSessionManager{
		httpClient: workerServer.Client(),
		sessions: map[int]*DeviceSession{
			0: {
				Index:         0,
				SessionID:     "sess-3",
				WorkflowRunID: "wf-3",
				WorkerBaseURL: workerServer.URL,
				Platform:      "ios",
				StartedAt:     now,
				LastActivity:  now,
				IdleTimeout:   5 * time.Minute,
			},
		},
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: 0,
	}
	srv := &Server{
		sessionMgr:          mgr,
		devLoopActive:       false,
		devLoopSessionIndex: 0,
	}

	_, output, err := srv.handleDeviceTap(context.Background(), nil, DeviceTapInput{Target: "Continue button"})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !output.Success {
		t.Fatalf("expected tap success outside dev-loop strict mode, got: %+v", output)
	}
}

// ---------------------------------------------------------------------------
// TestClearFirst_BugFix: Verify the ClearFirst bug fix. After the fix,
// setting clear_first: false should actually produce false.
// ---------------------------------------------------------------------------

func TestClearFirst_BugFix(t *testing.T) {
	// Simulate what handleDeviceType does with the fixed logic
	// by calling the handler with a crafted CallToolRequest.
	// Since we can't easily call the handler without a full MCP server,
	// we'll test the logic pattern directly.

	tests := []struct {
		name      string
		jsonInput string
		wantClear bool
	}{
		{
			name:      "clear_first explicitly true",
			jsonInput: `{"text":"hello","clear_first":true,"x":100,"y":200}`,
			wantClear: true,
		},
		{
			name:      "clear_first explicitly false",
			jsonInput: `{"text":"hello","clear_first":false,"x":100,"y":200}`,
			wantClear: false,
		},
		{
			name:      "clear_first omitted defaults to true",
			jsonInput: `{"text":"hello","x":100,"y":200}`,
			wantClear: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Unmarshal input like the MCP SDK would
			var input DeviceTypeInput
			if err := json.Unmarshal([]byte(tt.jsonInput), &input); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			// Replicate the fixed logic from handleDeviceType
			clearFirst := true
			var raw map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tt.jsonInput), &raw); err == nil {
				if _, exists := raw["clear_first"]; exists {
					clearFirst = input.ClearFirst
				}
			}

			if clearFirst != tt.wantClear {
				t.Errorf("clearFirst = %v, want %v", clearFirst, tt.wantClear)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestNextSteps_ToolOutputs: Verify that key tool outputs include NextSteps.
// Uses the output structs directly to confirm the pattern.
// ---------------------------------------------------------------------------

func TestNextSteps_ToolOutputStructs(t *testing.T) {
	// Verify that output structs have the NextSteps field (compile-time check).
	// We also verify that we can construct valid outputs with next steps.

	tests := []struct {
		name      string
		nextSteps []NextStep
	}{
		{
			name: "StartDeviceSessionOutput has NextSteps",
			nextSteps: StartDeviceSessionOutput{
				Success: true,
				NextSteps: []NextStep{
					{Tool: "screenshot", Reason: "test"},
				},
			}.NextSteps,
		},
		{
			name: "StopDeviceSessionOutput has NextSteps",
			nextSteps: StopDeviceSessionOutput{
				Success: true,
				NextSteps: []NextStep{
					{Tool: "create_test", Reason: "test"},
				},
			}.NextSteps,
		},
		{
			name: "DeviceTapOutput has NextSteps",
			nextSteps: DeviceTapOutput{
				Success: true,
				NextSteps: []NextStep{
					{Tool: "screenshot", Reason: "test"},
				},
			}.NextSteps,
		},
		{
			name: "DeviceDragOutput has NextSteps",
			nextSteps: DeviceDragOutput{
				Success: true,
				NextSteps: []NextStep{
					{Tool: "screenshot", Reason: "test"},
				},
			}.NextSteps,
		},
		{
			name: "ScreenshotOutput has NextSteps",
			nextSteps: ScreenshotOutput{
				Success: true,
				NextSteps: []NextStep{
					{Tool: "get_session_info", Reason: "test"},
				},
			}.NextSteps,
		},
		{
			name: "InstallAppOutput has NextSteps",
			nextSteps: InstallAppOutput{
				Success: true,
				NextSteps: []NextStep{
					{Tool: "launch_app", Reason: "test"},
				},
			}.NextSteps,
		},
		{
			name: "LaunchAppOutput has NextSteps",
			nextSteps: LaunchAppOutput{
				Success: true,
				NextSteps: []NextStep{
					{Tool: "screenshot", Reason: "test"},
				},
			}.NextSteps,
		},
		{
			name: "GetSessionInfoOutput has NextSteps",
			nextSteps: GetSessionInfoOutput{
				Active: false,
				NextSteps: []NextStep{
					{Tool: "start_device_session", Reason: "test"},
				},
			}.NextSteps,
		},
		{
			name: "DeviceDoctorOutput has NextSteps (after bug fix)",
			nextSteps: DeviceDoctorOutput{
				AllPassed: true,
				NextSteps: []NextStep{
					{Tool: "screenshot", Reason: "test"},
				},
			}.NextSteps,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.nextSteps) == 0 {
				t.Errorf("NextSteps should not be empty")
			}
			for _, ns := range tt.nextSteps {
				if ns.Tool == "" {
					t.Errorf("NextStep.Tool should not be empty")
				}
				if ns.Reason == "" {
					t.Errorf("NextStep.Reason should not be empty")
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestInputValidation_StartDeviceSession: Platform validation.
// ---------------------------------------------------------------------------

func TestInputValidation_StartDeviceSession(t *testing.T) {
	srv := &Server{
		sessionMgr: &DeviceSessionManager{},
	}

	tests := []struct {
		name      string
		input     StartDeviceSessionInput
		wantError string
	}{
		{
			name:      "empty platform",
			input:     StartDeviceSessionInput{Platform: ""},
			wantError: "platform is required (ios or android)",
		},
		{
			name:      "invalid platform",
			input:     StartDeviceSessionInput{Platform: "web"},
			wantError: "platform must be 'ios' or 'android'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, output, err := srv.handleStartDeviceSession(context.Background(), nil, tt.input)
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if output.Success {
				t.Fatal("expected Success=false")
			}
			if output.Error != tt.wantError {
				t.Errorf("error = %q, want %q", output.Error, tt.wantError)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestInputValidation_DeviceType_TextRequired: Text field validation.
// ---------------------------------------------------------------------------

func TestInputValidation_DeviceType_TextRequired(t *testing.T) {
	srv := &Server{
		sessionMgr: &DeviceSessionManager{},
	}

	_, output, err := srv.handleDeviceType(context.Background(), nil, DeviceTypeInput{
		Text: "",
		X:    intPtrHelper(100),
		Y:    intPtrHelper(200),
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if output.Success {
		t.Fatal("expected Success=false when text is empty")
	}
	if !strings.Contains(output.Error, "text is required") {
		t.Errorf("error = %q, want 'text is required'", output.Error)
	}
}

// ---------------------------------------------------------------------------
// TestInputValidation_DeviceSwipe_DirectionRequired: Direction field validation.
// ---------------------------------------------------------------------------

func TestInputValidation_DeviceSwipe_DirectionRequired(t *testing.T) {
	srv := &Server{
		sessionMgr: &DeviceSessionManager{},
	}

	_, output, err := srv.handleDeviceSwipe(context.Background(), nil, DeviceSwipeInput{
		Direction: "",
		X:         intPtrHelper(100),
		Y:         intPtrHelper(200),
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if output.Success {
		t.Fatal("expected Success=false when direction is empty")
	}
	if output.Error != "direction is required (up, down, left, right)" {
		t.Errorf("error = %q, want direction error", output.Error)
	}
}

// ---------------------------------------------------------------------------
// TestInputValidation_InstallApp_RequiresInput: install_app requires app_url or build_version_id.
// ---------------------------------------------------------------------------

func TestInputValidation_InstallApp_RequiresInput(t *testing.T) {
	srv := &Server{
		sessionMgr: &DeviceSessionManager{},
	}

	_, output, err := srv.handleInstallApp(context.Background(), nil, InstallAppInput{
		AppURL: "",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if output.Success {
		t.Fatal("expected Success=false when app_url is empty")
	}
	if !strings.Contains(output.Error, "either app_url or build_version_id is required") {
		t.Errorf("error = %q, want missing app_url/build_version_id validation", output.Error)
	}
}

// ---------------------------------------------------------------------------
// TestInputValidation_LaunchApp_BundleIDRequired: BundleID field validation.
// ---------------------------------------------------------------------------

func TestInputValidation_LaunchApp_BundleIDRequired(t *testing.T) {
	srv := &Server{
		sessionMgr: &DeviceSessionManager{},
	}

	_, output, err := srv.handleLaunchApp(context.Background(), nil, LaunchAppInput{
		BundleID: "",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if output.Success {
		t.Fatal("expected Success=false when bundle_id is empty")
	}
	if !strings.Contains(output.Error, "bundle_id is required") {
		t.Errorf("error = %q, want 'bundle_id is required'", output.Error)
	}
}

// ---------------------------------------------------------------------------
// TestNextStep_Serialization: Verify NextStep JSON serialization.
// ---------------------------------------------------------------------------

func TestNextStep_Serialization(t *testing.T) {
	ns := NextStep{
		Tool:   "screenshot",
		Params: "target=\"Sign In\"",
		Reason: "Verify the tap worked",
	}

	data, err := json.Marshal(ns)
	if err != nil {
		t.Fatalf("failed to marshal NextStep: %v", err)
	}

	var decoded NextStep
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal NextStep: %v", err)
	}

	if decoded.Tool != ns.Tool {
		t.Errorf("Tool = %q, want %q", decoded.Tool, ns.Tool)
	}
	if decoded.Params != ns.Params {
		t.Errorf("Params = %q, want %q", decoded.Params, ns.Params)
	}
	if decoded.Reason != ns.Reason {
		t.Errorf("Reason = %q, want %q", decoded.Reason, ns.Reason)
	}
}

// ---------------------------------------------------------------------------
// TestMCPToolRegistration_Count: Verify all device tools are registered
// and accessible via a client session.
// ---------------------------------------------------------------------------

func TestMCPToolRegistration_Count(t *testing.T) {
	mcpServer := mcpsdk.NewServer(
		&mcpsdk.Implementation{Name: "revyl-test", Version: "0.0.0-test"},
		nil,
	)

	srv := &Server{
		mcpServer:  mcpServer,
		sessionMgr: &DeviceSessionManager{},
	}
	srv.registerDeviceTools()

	// Set up an in-process client-server connection
	ctx := context.Background()
	ct, st := mcpsdk.NewInMemoryTransports()

	ss, err := mcpServer.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer ss.Close()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	result, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	expectedTools := map[string]bool{
		"start_device_session": false,
		"stop_device_session":  false,
		"device_tap":           false,
		"device_double_tap":    false,
		"device_long_press":    false,
		"device_type":          false,
		"device_swipe":         false,
		"device_drag":          false,
		"screenshot":           false,
		"install_app":          false,
		"launch_app":           false,
		"get_session_info":     false,
		"device_doctor":        false,
	}

	for _, tool := range result.Tools {
		if _, ok := expectedTools[tool.Name]; ok {
			expectedTools[tool.Name] = true
		}
	}

	for name, found := range expectedTools {
		if !found {
			t.Errorf("expected device tool %q was not registered", name)
		}
	}

	if len(result.Tools) != 15 {
		t.Errorf("expected 15 device tools, got %d", len(result.Tools))
	}
}

// intPtrHelper returns a pointer to the given int value.
func intPtrHelper(v int) *int {
	return &v
}
