package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	mcppkg "github.com/revyl/cli/internal/mcp"
)

const (
	flowSessionID     = "9f3c1a2b-4d5e-4f60-8a71-b2c3d4e5f607"
	flowWorkflowRunID = "44444444-4444-4444-4444-444444444444"
)

// newSessionTargetTestCommand builds a bare command carrying only the session
// targeting flags, for parsing and precedence assertions.
func newSessionTargetTestCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "screenshot", RunE: func(*cobra.Command, []string) error { return nil }}
	registerSessionTargetFlags(cmd)
	return cmd
}

func TestSessionTargetFromCommand_RejectsConflictingTargets(t *testing.T) {
	const otherSessionID = "11111111-2222-4333-8444-555555555555"

	testCases := []struct {
		name string
		args []string
	}{
		{name: "different session ids", args: []string{"--session-id", flowSessionID, "-s", otherSessionID}},
		{name: "session id and index", args: []string{"--session-id", flowSessionID, "-s", "1"}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(sessionIDEnvVar, "")

			cmd := newSessionTargetTestCommand()
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatalf("ParseFlags(%v) error = %v", tc.args, err)
			}

			if _, err := sessionTargetFromCommand(cmd); err == nil ||
				!strings.Contains(err.Error(), "conflicting session targets") {
				t.Fatalf("sessionTargetFromCommand() error = %v, want conflicting-target failure", err)
			}
		})
	}
}

// sessionlessDeviceCommands are the direct `device` subcommands that do not act
// on an existing session, so they neither need nor accept session targeting.
// Nested subcommands such as `device state list` are deliberately absent: they
// act on a session and must carry the flags.
var sessionlessDeviceCommands = map[string]bool{
	"start":   true, // creates the session
	"list":    true, // enumerates every session
	"use":     true, // takes a positional index
	"attach":  true, // takes a positional session ID
	"targets": true, // static device catalog
	"history": true, // account-wide history
}

// The dev commands select their session through their dev context, so an
// exported REVYL_SESSION_ID must not divert them onto the stateless path and
// leave their started or attached sessions unrecorded.
func TestDevCommands_IgnoreSessionEnvironmentForTargeting(t *testing.T) {
	t.Setenv(sessionIDEnvVar, "11111111-2222-4333-8444-555555555555")

	for _, cmd := range []*cobra.Command{devCmd, devStopCmd, devAttachCmd} {
		t.Run(cmd.CommandPath(), func(t *testing.T) {
			if commandSupportsSessionTargeting(cmd) {
				t.Skipf("%s registers session flags; targeting is intentional", cmd.CommandPath())
			}

			target, err := sessionTargetFromCommand(cmd)
			if err != nil {
				t.Fatalf("sessionTargetFromCommand() error = %v, want nil", err)
			}
			if target.ByDurableID() {
				t.Fatalf("%s resolved to session %s from the environment; it would skip local-session "+
					"hydration and disable persistence", cmd.CommandPath(), target.SessionID)
			}
		})
	}
}

// device start, list, use, and attach all call getDeviceSessionMgr. If an
// exported REVYL_SESSION_ID made those commands look ID-targeted, the manager
// would skip SyncSessions and disable persistence, so a newly started session
// would never land in device-sessions.json and list would return nothing.
func TestSessionlessDeviceCommands_IgnoreSessionEnvironmentForTargeting(t *testing.T) {
	t.Setenv(sessionIDEnvVar, "11111111-2222-4333-8444-555555555555")

	for _, cmd := range []*cobra.Command{deviceStartCmd, deviceListCmd, deviceUseCmd, deviceAttachCmd} {
		t.Run(cmd.CommandPath(), func(t *testing.T) {
			if commandSupportsSessionTargeting(cmd) {
				t.Fatalf("%s registered session flags; REVYL_SESSION_ID would skip persistence on this command",
					cmd.CommandPath())
			}

			target, err := sessionTargetFromCommand(cmd)
			if err != nil {
				t.Fatalf("sessionTargetFromCommand() error = %v, want nil", err)
			}
			if target.ByDurableID() {
				t.Fatalf("%s resolved to session %s from the environment; it would skip SyncSessions "+
					"and disable persistence", cmd.CommandPath(), target.SessionID)
			}
			if target.Index != -1 {
				t.Fatalf("Index = %d, want -1 (the active session)", target.Index)
			}
		})
	}
}

// idTargetedFlowServer records what the ID-targeted path asked the backend for.
type idTargetedFlowServer struct {
	Server            *httptest.Server
	SessionLookups    *atomic.Int32
	ActiveSessionCall *atomic.Int32
	ProxiedPaths      chan string
}

// newIDTargetedFlowServer serves durable-ID resolution plus device-proxy
// actions for the resolved workflow run.
func newIDTargetedFlowServer(t *testing.T) *idTargetedFlowServer {
	t.Helper()

	flow := &idTargetedFlowServer{
		SessionLookups:    &atomic.Int32{},
		ActiveSessionCall: &atomic.Int32{},
		ProxiedPaths:      make(chan string, 16),
	}

	proxyPrefix := "/api/v1/execution/device-proxy/" + flowWorkflowRunID
	flow.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-1","email":"test@example.com","concurrency_limit":1}`))
		case r.URL.Path == "/api/v1/execution/device-sessions/active":
			flow.ActiveSessionCall.Add(1)
			_, _ = w.Write([]byte(`{"org_id":"org-1","sessions":[]}`))
		case r.URL.Path == "/api/v1/execution/device-sessions/"+flowSessionID:
			flow.SessionLookups.Add(1)
			_, _ = w.Write([]byte(`{
				"id":"` + flowSessionID + `",
				"org_id":"org-1",
				"platform":"ios",
				"status":"running",
				"workflow_run_id":"` + flowWorkflowRunID + `",
				"started_at":"2026-02-19T00:00:00Z"
			}`))
		case r.URL.Path == "/api/v1/execution/streaming/worker-connection/"+flowWorkflowRunID:
			_, _ = w.Write([]byte(`{"status":"ready","workflow_run_id":"` + flowWorkflowRunID +
				`","worker_ws_url":"ws://` + r.Host + `/ws/stream?token=test"}`))
		case strings.HasPrefix(r.URL.Path, proxyPrefix):
			action := strings.TrimPrefix(r.URL.Path, proxyPrefix)
			select {
			case flow.ProxiedPaths <- action:
			default:
			}
			switch action {
			case "/health":
				_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
			case "/execute_step":
				_, _ = w.Write([]byte(`{"success":true,"status":"completed","step_type":"instruction","reasoning":"done"}`))
			default:
				_, _ = w.Write([]byte(`{"success":true,"action":"` + strings.TrimPrefix(action, "/") + `"}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(flow.Server.Close)

	return flow
}

// proxiedActions drains the recorded device-proxy actions.
func (f *idTargetedFlowServer) proxiedActions() []string {
	var actions []string
	for {
		select {
		case action := <-f.ProxiedPaths:
			actions = append(actions, action)
		default:
			return actions
		}
	}
}

// seedForeignSessionCache writes a session cache owned by a different CLI
// process, so tests can prove ID targeting never touches it.
func seedForeignSessionCache(t *testing.T, dir string) []byte {
	t.Helper()

	revylDir := filepath.Join(dir, ".revyl")
	if err := os.MkdirAll(revylDir, 0o755); err != nil {
		t.Fatalf("mkdir .revyl: %v", err)
	}
	contents := []byte(`{"active":7,"next_index":8,"sessions":[]}`)
	if err := os.WriteFile(filepath.Join(revylDir, "device-sessions.json"), contents, 0o600); err != nil {
		t.Fatalf("write device-sessions.json: %v", err)
	}
	return contents
}

// assertSessionCacheUnchanged fails when an ID-targeted command mutated the
// shared session file that parallel batch workers depend on.
func assertSessionCacheUnchanged(t *testing.T, dir string, want []byte) {
	t.Helper()

	got, err := os.ReadFile(filepath.Join(dir, ".revyl", "device-sessions.json"))
	if err != nil {
		t.Fatalf("read device-sessions.json: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("device-sessions.json changed during ID-targeted command:\n got %s\nwant %s", got, want)
	}
}

func TestDeviceCommands_TargetSessionByDurableID(t *testing.T) {
	testCases := []struct {
		name        string
		selectorArg string
	}{
		{name: "session id via -s", selectorArg: "s"},
		{name: "session id via --session-id", selectorArg: "session-id"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			withWorkingDirectory(t, tmpDir)
			originalCache := seedForeignSessionCache(t, tmpDir)

			t.Setenv("REVYL_API_KEY", "test-api-key")
			t.Setenv(sessionIDEnvVar, "")
			flow := newIDTargetedFlowServer(t)
			t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)

			screenshotPath := filepath.Join(tmpDir, "screen.png")
			cmd := &cobra.Command{}
			cmd.SetContext(context.Background())
			cmd.Flags().String("out", "", "")
			cmd.Flags().Bool("json", false, "")
			cmd.Flags().Bool("dev", false, "")
			registerSessionTargetFlags(cmd)
			if err := cmd.Flags().Set(tc.selectorArg, flowSessionID); err != nil {
				t.Fatalf("set %s flag: %v", tc.selectorArg, err)
			}
			if err := cmd.Flags().Set("out", screenshotPath); err != nil {
				t.Fatalf("set out flag: %v", err)
			}

			captureStdout(t, func() {
				if err := deviceScreenshotCmd.RunE(cmd, nil); err != nil {
					t.Fatalf("device screenshot error = %v", err)
				}
			})

			if flow.SessionLookups.Load() != 1 {
				t.Fatalf("session lookups = %d, want 1 durable-ID resolution", flow.SessionLookups.Load())
			}
			if flow.ActiveSessionCall.Load() != 0 {
				t.Fatalf("active-session syncs = %d, want 0 for an ID-targeted command", flow.ActiveSessionCall.Load())
			}
			actions := flow.proxiedActions()
			if len(actions) == 0 || actions[len(actions)-1] != "/screenshot" {
				t.Fatalf("proxied actions = %v, want the resolved workflow run to receive /screenshot", actions)
			}
			assertSessionCacheUnchanged(t, tmpDir, originalCache)
		})
	}
}

// TestDeviceStateCommands_TargetSessionByDurableID proves every state
// inspector command relays through the already-resolved session instead of
// looking up session.Index, which is UnattachedSessionIndex on this path.
func TestDeviceStateCommands_TargetSessionByDurableID(t *testing.T) {
	testCases := []struct {
		name     string
		run      func(*cobra.Command, []string) error
		flags    map[string]string
		args     []string
		wantPath string
	}{
		{
			name:     "list",
			run:      deviceStateListCmd.RunE,
			wantPath: "/device_state/list",
		},
		{
			name:     "snapshot",
			run:      deviceStateSnapshotCmd.RunE,
			wantPath: "/device_state/snapshot",
		},
		{
			name:     "diff",
			run:      deviceStateDiffCmd.RunE,
			flags:    map[string]string{"since": "4823"},
			wantPath: "/device_state/diff",
		},
		{
			name:     "userdefaults",
			run:      deviceStateUserDefaultsCmd.RunE,
			args:     []string{"Library/Preferences/com.example.plist"},
			wantPath: "/device_state/userdefaults",
		},
		{
			name:     "sqlite",
			run:      deviceStateSqliteCmd.RunE,
			args:     []string{"Documents/app.db", "SELECT count(*) FROM users"},
			wantPath: "/device_state/sqlite/query",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			withWorkingDirectory(t, tmpDir)
			originalCache := seedForeignSessionCache(t, tmpDir)

			t.Setenv("REVYL_API_KEY", "test-api-key")
			t.Setenv(sessionIDEnvVar, "")
			flow := newIDTargetedFlowServer(t)
			t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)

			cmd := &cobra.Command{}
			cmd.SetContext(context.Background())
			cmd.Flags().Bool("json", false, "")
			cmd.Flags().Bool("dev", false, "")
			cmd.Flags().String("since", "", "")
			registerSessionTargetFlags(cmd)
			if err := cmd.Flags().Set("session-id", flowSessionID); err != nil {
				t.Fatalf("set session-id flag: %v", err)
			}
			if err := cmd.Flags().Set("json", "true"); err != nil {
				t.Fatalf("set json flag: %v", err)
			}
			for name, value := range tc.flags {
				if err := cmd.Flags().Set(name, value); err != nil {
					t.Fatalf("set %s flag: %v", name, err)
				}
			}

			captureStdout(t, func() {
				if err := tc.run(cmd, tc.args); err != nil {
					t.Fatalf("device state %s error = %v", tc.name, err)
				}
			})

			if flow.SessionLookups.Load() != 1 {
				t.Fatalf("session lookups = %d, want 1 durable-ID resolution", flow.SessionLookups.Load())
			}
			if flow.ActiveSessionCall.Load() != 0 {
				t.Fatalf("active-session syncs = %d, want 0 for an ID-targeted command", flow.ActiveSessionCall.Load())
			}
			actions := flow.proxiedActions()
			if len(actions) == 0 || actions[len(actions)-1] != tc.wantPath {
				t.Fatalf("proxied actions = %v, want %s on the resolved workflow run", actions, tc.wantPath)
			}
			assertSessionCacheUnchanged(t, tmpDir, originalCache)
		})
	}
}

func TestDeviceTapCommand_TargetsSessionByDurableID(t *testing.T) {
	tmpDir := t.TempDir()
	withWorkingDirectory(t, tmpDir)
	originalCache := seedForeignSessionCache(t, tmpDir)

	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	flow := newIDTargetedFlowServer(t)
	t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.Flags().String("target", "", "")
	cmd.Flags().Int("x", 0, "")
	cmd.Flags().Int("y", 0, "")
	cmd.Flags().Bool("json", false, "")
	cmd.Flags().Bool("dev", false, "")
	registerSessionTargetFlags(cmd)
	for name, value := range map[string]string{"s": flowSessionID, "x": "120", "y": "340", "json": "true"} {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatalf("set %s flag: %v", name, err)
		}
	}

	output := captureStdout(t, func() {
		if err := deviceTapCmd.RunE(cmd, nil); err != nil {
			t.Fatalf("device tap error = %v", err)
		}
	})

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &payload); err != nil {
		t.Fatalf("device tap stdout is not JSON: %v\n%s", err, output)
	}
	actions := flow.proxiedActions()
	if len(actions) == 0 || actions[len(actions)-1] != "/tap" {
		t.Fatalf("proxied actions = %v, want /tap on the resolved workflow run", actions)
	}
	assertSessionCacheUnchanged(t, tmpDir, originalCache)
}

func TestDeviceInstructionCommand_TargetsSessionFromEnvironment(t *testing.T) {
	tmpDir := t.TempDir()
	withWorkingDirectory(t, tmpDir)
	originalCache := seedForeignSessionCache(t, tmpDir)

	t.Setenv("REVYL_API_KEY", "test-api-key")
	// The environment variable is how a batch worker scopes a whole shell to
	// one session without repeating the flag.
	t.Setenv(sessionIDEnvVar, flowSessionID)
	flow := newIDTargetedFlowServer(t)
	t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("json", false, "")
	cmd.Flags().Bool("dev", false, "")
	registerSessionTargetFlags(cmd)
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatalf("set json flag: %v", err)
	}

	captureStdout(t, func() {
		if err := deviceInstructionCmd.RunE(cmd, []string{"tap", "the", "login", "button"}); err != nil {
			t.Fatalf("device instruction error = %v", err)
		}
	})

	if flow.ActiveSessionCall.Load() != 0 {
		t.Fatalf("active-session syncs = %d, want 0 when REVYL_SESSION_ID targets a session", flow.ActiveSessionCall.Load())
	}
	actions := flow.proxiedActions()
	if len(actions) == 0 || actions[len(actions)-1] != "/execute_step" {
		t.Fatalf("proxied actions = %v, want /execute_step on the resolved workflow run", actions)
	}
	assertSessionCacheUnchanged(t, tmpDir, originalCache)
}

const (
	secondFlowSessionID     = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	secondFlowWorkflowRunID = "66666666-6666-6666-6666-666666666666"
)

// inventoryStopAllServer hydrates two live sessions so --all can prove it
// cancels every backend session, not just the durable-ID target.
type inventoryStopAllServer struct {
	Server            *httptest.Server
	ActiveSessionCall *atomic.Int32
	CancelCalls       *atomic.Int32
	CancelledRuns     chan string
	CancelResponse    func(string) string
}

// newInventoryStopAllServer serves user lookup, a two-session active list,
// worker health, and cancel so SyncSessions can attach then StopAllSessions
// can tear both down.
func newInventoryStopAllServer(t *testing.T) *inventoryStopAllServer {
	t.Helper()

	flow := &inventoryStopAllServer{
		ActiveSessionCall: &atomic.Int32{},
		CancelCalls:       &atomic.Int32{},
		CancelledRuns:     make(chan string, 8),
	}

	sessions := []struct {
		sessionID     string
		workflowRunID string
	}{
		{sessionID: flowSessionID, workflowRunID: flowWorkflowRunID},
		{sessionID: secondFlowSessionID, workflowRunID: secondFlowWorkflowRunID},
	}

	flow.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-1","email":"test@example.com","concurrency_limit":1}`))
		case r.URL.Path == "/api/v1/execution/device-sessions/active":
			flow.ActiveSessionCall.Add(1)
			_, _ = w.Write([]byte(`{
				"org_id":"org-1",
				"sessions":[
					{
						"id":"` + flowSessionID + `",
						"org_id":"org-1",
						"platform":"ios",
						"source":"cli",
						"status":"running",
						"workflow_run_id":"` + flowWorkflowRunID + `",
						"user_email":"test@example.com",
						"created_at":"2026-02-19T00:00:00Z",
						"started_at":"2026-02-19T00:00:00Z"
					},
					{
						"id":"` + secondFlowSessionID + `",
						"org_id":"org-1",
						"platform":"android",
						"source":"cli",
						"status":"running",
						"workflow_run_id":"` + secondFlowWorkflowRunID + `",
						"user_email":"test@example.com",
						"created_at":"2026-02-19T00:01:00Z",
						"started_at":"2026-02-19T00:01:00Z"
					}
				]
			}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/execution/device/status/cancel/"):
			flow.CancelCalls.Add(1)
			runID := strings.TrimPrefix(r.URL.Path, "/api/v1/execution/device/status/cancel/")
			select {
			case flow.CancelledRuns <- runID:
			default:
			}
			if flow.CancelResponse != nil {
				_, _ = w.Write([]byte(flow.CancelResponse(runID)))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"request_accepted":true,"session_settled":true,"device_released":true,"message":"cancelled","workflow_run_id":"` + runID + `"}`))
		default:
			for _, session := range sessions {
				if r.URL.Path == "/api/v1/execution/streaming/worker-connection/"+session.workflowRunID {
					_, _ = w.Write([]byte(`{"status":"ready","workflow_run_id":"` + session.workflowRunID +
						`","worker_ws_url":"ws://` + r.Host + `/ws/stream?token=test"}`))
					return
				}
				if r.URL.Path == "/api/v1/execution/device-proxy/"+session.workflowRunID+"/health" {
					_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
					return
				}
			}
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(flow.Server.Close)

	return flow
}

func TestDeviceStopReportsUnsettledAndPartialOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name          string
		all           bool
		body          string
		wantError     bool
		wantRequested bool
		secondBody    string
		wantStopped   bool
	}{
		{"pending", false, `{"success":true,"request_accepted":true,"session_settled":false,"device_released":false}`, false, true, "", false},
		{"legacy", false, `{"success":true,"db_updated":true}`, false, true, "", false},
		{"rejected", false, `{"success":false,"request_accepted":false,"message":"stop rejected"}`, true, false, "", false},
		{"partial", true, `{"success":false,"request_accepted":false,"message":"stop rejected"}`, true, false, `{"success":true,"session_settled":true,"device_released":true}`, false},
		{"all_pending", true, `{"success":true,"request_accepted":true,"session_settled":false,"device_released":false}`, false, true, "", false},
		{"all_released", true, `{"success":true,"request_accepted":true,"session_settled":true,"device_released":true}`, false, true, "", true},
		{"all_legacy", true, `{"success":true,"db_updated":true}`, false, true, "", false},
		{"released_and_pending", true, `{"success":true,"session_settled":true,"device_released":true}`, false, true, `{"success":true,"session_settled":true,"device_released":false}`, false},
		{"pending_and_rejected", true, `{"success":true,"session_settled":true,"device_released":false}`, true, false, `{"success":false,"message":"stop rejected"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withWorkingDirectory(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "test-api-key")
			t.Setenv(sessionIDEnvVar, "")
			flow := newInventoryStopAllServer(t)
			flow.CancelResponse = func(runID string) string {
				if tc.secondBody != "" && runID == secondFlowWorkflowRunID {
					return tc.secondBody
				}
				return tc.body
			}
			t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)
			cmd := &cobra.Command{Use: "stop"}
			cmd.SetContext(context.Background())
			cmd.Flags().Bool("json", true, "")
			cmd.Flags().Bool("dev", false, "")
			cmd.Flags().Bool("all", tc.all, "")
			registerSessionTargetFlags(cmd)
			if !tc.all {
				if err := cmd.Flags().Set("s", "0"); err != nil {
					t.Fatal(err)
				}
			}
			var stopErr error
			output := captureStdout(t, func() { stopErr = deviceStopCmd.RunE(cmd, nil) })
			if (stopErr != nil) != tc.wantError {
				t.Fatalf("stop error = %v, want error %v", stopErr, tc.wantError)
			}
			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(output), &payload); err != nil {
				t.Fatalf("invalid JSON output %q: %v", output, err)
			}
			key := "stopped"
			if tc.all {
				key = "stopped_all"
			}
			if payload[key] != tc.wantStopped {
				t.Fatalf("%s = %v, want %v", key, payload[key], tc.wantStopped)
			}
			if tc.wantRequested && payload["stop_requested"] != true {
				t.Fatalf("missing stop acceptance: %s", output)
			}
			if tc.all {
				var batch struct {
					Results []mcppkg.DeviceSessionStopResult `json:"results"`
				}
				if err := json.Unmarshal([]byte(output), &batch); err != nil {
					t.Fatal(err)
				}
				if len(batch.Results) != 2 {
					t.Fatalf("missing batch results: %s", output)
				}
				for _, member := range batch.Results {
					body := tc.body
					if member.WorkflowRunID == secondFlowWorkflowRunID && tc.secondBody != "" {
						body = tc.secondBody
					}
					var acknowledgement api.CancelDeviceResponse
					if err := json.Unmarshal([]byte(body), &acknowledgement); err != nil {
						t.Fatal(err)
					}
					if member.SessionID == "" || member.RequestAccepted != acknowledgement.StopRequestAccepted() || member.SessionSettled != (acknowledgement.SessionSettled != nil && *acknowledgement.SessionSettled) || member.DeviceReleased != (acknowledgement.DeviceReleased != nil && *acknowledgement.DeviceReleased) {
						t.Fatalf("incorrect member result: %+v", member)
					}
				}
			}
		})
	}
}

func TestDeviceStopByIDDoesNotRequireWorkerOrActiveSession(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, flowSessionID)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/execution/device/sessions/"+flowSessionID+"/stop" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"request_accepted":true,"session_settled":true,"device_released":true}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	cmd := &cobra.Command{Use: "stop"}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("json", true, "")
	cmd.Flags().Bool("dev", false, "")
	cmd.Flags().Bool("all", false, "")
	registerSessionTargetFlags(cmd)
	output := captureStdout(t, func() {
		if err := deviceStopCmd.RunE(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["stopped"] != true || requests.Load() != 1 {
		t.Fatalf("output=%s requests=%d, want confirmed stop with one backend call", output, requests.Load())
	}
}

// cancelledWorkflowRuns drains the recorded cancel targets.
func (f *inventoryStopAllServer) cancelledWorkflowRuns() []string {
	var runs []string
	for {
		select {
		case runID := <-f.CancelledRuns:
			runs = append(runs, runID)
		default:
			return runs
		}
	}
}

func TestDeviceStopAll_HydratesWhenDurableIDPresent(t *testing.T) {
	testCases := []struct {
		name   string
		envID  string
		setID  bool
		flagID string
	}{
		{
			name:  "REVYL_SESSION_ID scopes the shell",
			envID: flowSessionID,
		},
		{
			name:   "explicit --session-id with --all",
			envID:  "",
			setID:  true,
			flagID: flowSessionID,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			withWorkingDirectory(t, tmpDir)
			_ = seedForeignSessionCache(t, tmpDir)

			t.Setenv("REVYL_API_KEY", "test-api-key")
			t.Setenv(sessionIDEnvVar, tc.envID)
			flow := newInventoryStopAllServer(t)
			t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)

			cmd := &cobra.Command{}
			cmd.SetContext(context.Background())
			cmd.Flags().Bool("json", false, "")
			cmd.Flags().Bool("dev", false, "")
			cmd.Flags().Bool("all", false, "")
			registerSessionTargetFlags(cmd)
			if err := cmd.Flags().Set("all", "true"); err != nil {
				t.Fatalf("set all flag: %v", err)
			}
			if err := cmd.Flags().Set("json", "true"); err != nil {
				t.Fatalf("set json flag: %v", err)
			}
			if tc.setID {
				if err := cmd.Flags().Set("session-id", tc.flagID); err != nil {
					t.Fatalf("set session-id flag: %v", err)
				}
			}

			output := captureStdout(t, func() {
				if err := deviceStopCmd.RunE(cmd, nil); err != nil {
					t.Fatalf("device stop --all error = %v", err)
				}
			})

			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &payload); err != nil {
				t.Fatalf("device stop --all stdout is not JSON: %v\n%s", err, output)
			}
			if payload["stopped_all"] != true {
				t.Fatalf("stopped_all = %v, want true", payload["stopped_all"])
			}
			if flow.ActiveSessionCall.Load() == 0 {
				t.Fatal("active-session syncs = 0, want hydration before StopAllSessions")
			}
			if flow.CancelCalls.Load() != 2 {
				t.Fatalf("cancel calls = %d, want 2 hydrated sessions", flow.CancelCalls.Load())
			}
			got := flow.cancelledWorkflowRuns()
			want := map[string]bool{flowWorkflowRunID: true, secondFlowWorkflowRunID: true}
			for _, runID := range got {
				if !want[runID] {
					t.Fatalf("unexpected cancel target %q, got %v", runID, got)
				}
				delete(want, runID)
			}
			if len(want) != 0 {
				t.Fatalf("missing cancel targets %v, got %v", want, got)
			}
		})
	}
}

// newIDTargetedResolveFailureServer serves durable-ID lookups that fail in a
// specific way so info and doctor can prove they surface the diagnostic.
func newIDTargetedResolveFailureServer(t *testing.T, mode string) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-1","email":"test@example.com","concurrency_limit":1}`))
		case r.URL.Path == "/api/v1/execution/device-sessions/active":
			_, _ = w.Write([]byte(`{"org_id":"org-1","sessions":[]}`))
		case r.URL.Path == "/api/v1/execution/device-sessions/"+flowSessionID:
			switch mode {
			case "not_found":
				http.NotFound(w, r)
			case "completed":
				_, _ = w.Write([]byte(`{
					"id":"` + flowSessionID + `",
					"org_id":"org-1",
					"platform":"ios",
					"status":"completed",
					"workflow_run_id":"` + flowWorkflowRunID + `",
					"started_at":"2026-02-19T00:00:00Z"
				}`))
			case "queued":
				_, _ = w.Write([]byte(`{
					"id":"` + flowSessionID + `",
					"org_id":"org-1",
					"platform":"ios",
					"status":"queued",
					"workflow_run_id":null,
					"started_at":"2026-02-19T00:00:00Z"
				}`))
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// newDeviceInspectionTestCommand builds a command carrying the flags info and
// doctor read from RunE, optionally targeting a durable session ID.
func newDeviceInspectionTestCommand(t *testing.T, sessionID string) *cobra.Command {
	t.Helper()

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("json", false, "")
	cmd.Flags().Bool("dev", false, "")
	registerSessionTargetFlags(cmd)
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatalf("set json flag: %v", err)
	}
	if sessionID != "" {
		if err := cmd.Flags().Set("session-id", sessionID); err != nil {
			t.Fatalf("set session-id flag: %v", err)
		}
	}
	return cmd
}

func TestDeviceInfo_SurfacesDurableIDResolveFailures(t *testing.T) {
	testCases := []struct {
		name     string
		mode     string
		wantDiag string
	}{
		{name: "terminal", mode: "completed", wantDiag: "terminal state"},
		{name: "inaccessible", mode: "not_found", wantDiag: "not found or not accessible"},
		{name: "queued", mode: "queued", wantDiag: "no workflow run ID"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			withWorkingDirectory(t, tmpDir)
			originalCache := seedForeignSessionCache(t, tmpDir)

			t.Setenv("REVYL_API_KEY", "test-api-key")
			t.Setenv(sessionIDEnvVar, "")
			server := newIDTargetedResolveFailureServer(t, tc.mode)
			t.Setenv("REVYL_BACKEND_URL", server.URL)

			cmd := newDeviceInspectionTestCommand(t, flowSessionID)
			var runErr error
			output := captureStdout(t, func() {
				runErr = deviceInfoCmd.RunE(cmd, nil)
			})

			if runErr == nil {
				t.Fatal("device info error = nil, want durable-ID resolve failure")
			}
			if !strings.Contains(runErr.Error(), tc.wantDiag) {
				t.Fatalf("device info error = %q, want it to contain %q", runErr, tc.wantDiag)
			}
			if strings.Contains(output, "No active device session") {
				t.Fatalf("device info printed the empty-session contract:\n%s", output)
			}
			assertSessionCacheUnchanged(t, tmpDir, originalCache)
		})
	}
}

func TestDeviceDoctor_SurfacesDurableIDResolveFailures(t *testing.T) {
	testCases := []struct {
		name     string
		mode     string
		wantDiag string
	}{
		{name: "terminal", mode: "completed", wantDiag: "terminal state"},
		{name: "inaccessible", mode: "not_found", wantDiag: "not found or not accessible"},
		{name: "queued", mode: "queued", wantDiag: "no workflow run ID"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			withWorkingDirectory(t, tmpDir)
			originalCache := seedForeignSessionCache(t, tmpDir)

			t.Setenv("REVYL_API_KEY", "test-api-key")
			t.Setenv(sessionIDEnvVar, "")
			server := newIDTargetedResolveFailureServer(t, tc.mode)
			t.Setenv("REVYL_BACKEND_URL", server.URL)

			cmd := newDeviceInspectionTestCommand(t, flowSessionID)
			var runErr error
			output := captureStdout(t, func() {
				runErr = deviceDoctorCmd.RunE(cmd, nil)
			})
			if runErr != nil {
				t.Fatalf("device doctor error = %v, want nil", runErr)
			}

			var payload struct {
				Checks []struct {
					Name   string `json:"name"`
					Status string `json:"status"`
					Detail string `json:"detail"`
				} `json:"checks"`
			}
			if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &payload); err != nil {
				t.Fatalf("device doctor stdout is not JSON: %v\n%s", err, output)
			}

			var sessionCheck *struct {
				Name   string `json:"name"`
				Status string `json:"status"`
				Detail string `json:"detail"`
			}
			for i := range payload.Checks {
				if payload.Checks[i].Name == "session" {
					sessionCheck = &payload.Checks[i]
					break
				}
			}
			if sessionCheck == nil {
				t.Fatalf("device doctor omitted the session check:\n%s", output)
			}
			if sessionCheck.Status != "fail" {
				t.Fatalf("session status = %q, want fail", sessionCheck.Status)
			}
			if !strings.Contains(sessionCheck.Detail, tc.wantDiag) {
				t.Fatalf("session detail = %q, want it to contain %q", sessionCheck.Detail, tc.wantDiag)
			}
			if sessionCheck.Detail == "No active session" || strings.Contains(sessionCheck.Detail, "No active session") {
				t.Fatalf("session detail hid the diagnostic behind the empty-session message: %q", sessionCheck.Detail)
			}
			assertSessionCacheUnchanged(t, tmpDir, originalCache)
		})
	}
}

func TestDeviceInfo_EmptyDefaultSessionStillSucceeds(t *testing.T) {
	tmpDir := t.TempDir()
	withWorkingDirectory(t, tmpDir)

	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	flow := newIDTargetedFlowServer(t)
	t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)

	cmd := newDeviceInspectionTestCommand(t, "")
	var runErr error
	output := captureStdout(t, func() {
		runErr = deviceInfoCmd.RunE(cmd, nil)
	})
	if runErr != nil {
		t.Fatalf("device info error = %v, want nil for the empty default session", runErr)
	}

	var payload struct {
		Active        bool `json:"active"`
		TotalSessions int  `json:"total_sessions"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &payload); err != nil {
		t.Fatalf("device info stdout is not JSON: %v\n%s", err, output)
	}
	if payload.Active {
		t.Fatalf("active = true, want false")
	}
	if payload.TotalSessions != 0 {
		t.Fatalf("total_sessions = %d, want 0", payload.TotalSessions)
	}
	if flow.SessionLookups.Load() != 0 {
		t.Fatalf("session lookups = %d, want 0 for an untargeted empty probe", flow.SessionLookups.Load())
	}
}
