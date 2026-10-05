package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/execution"
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/testutil"
)

const defaultsTestCatalog = `{"platforms":{"ios":{"default_pair":{"model":"iPhone 16","runtime":"iOS 26.5"},"available_runtimes":["iOS 27.0","iOS 26.5","iOS 27-beta-4"],"available_models":["iPhone 16"],"compatible_runtimes":{"iPhone 16":["iOS 26.5","iOS 27-beta-4","iOS 27.0"]}},"android":{"default_pair":{"model":"Pixel 7","runtime":"Android 14"},"available_runtimes":["Android 14"],"available_models":["Pixel 7"],"compatible_runtimes":{"Pixel 7":["Android 14"]}}}}`

func TestDeviceStartCommand_DefaultsOSVersionForDeviceModel(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")

	const workflowRunID = "00000000-0000-0000-0000-000000000031"
	const sessionID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaa31"
	var startRequest struct {
		DeviceModel string `json:"device_model"`
		OsVersion   string `json:"os_version"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/execution/device-targets":
			_, _ = w.Write([]byte(defaultsTestCatalog))
		case r.URL.Path == "/api/v1/execution/start_device":
			if err := json.NewDecoder(r.Body).Decode(&startRequest); err != nil {
				t.Errorf("decode start_device request: %v", err)
			}
			_, _ = w.Write([]byte(`{"workflow_run_id":"` + workflowRunID + `","session_id":"` + sessionID + `"}`))
		case r.URL.Path == "/api/v1/execution/streaming/worker-connection/"+workflowRunID:
			_, _ = w.Write([]byte(`{"status":"ready","workflow_run_id":"` + workflowRunID + `","worker_ws_url":"ws://` + r.Host + `/ws/stream?token=test"}`))
		case r.URL.Path == "/api/v1/execution/device-proxy/"+workflowRunID+"/health":
			_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	cmd := newDeviceStartTestCommand(context.Background())
	for flag, value := range map[string]string{"platform": "ios", "device-model": "iPhone 16", "json": "true"} {
		if err := cmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	var runErr error
	stdout, stderr := captureStdoutAndStderrSeparate(t, func() { runErr = deviceStartCmd.RunE(cmd, nil) })
	if runErr != nil {
		t.Fatalf("device start error = %v, want the newest runtime to be chosen", runErr)
	}
	if startRequest.DeviceModel != "iPhone 16" || startRequest.OsVersion != "iOS 27.0" {
		t.Fatalf("start_device = %+v, want iPhone 16 on iOS 27.0", startRequest)
	}
	if !strings.Contains(stderr, `Using iOS 27.0 for --device-model "iPhone 16" (the newest runtime it supports). Pass --os-version to choose another.`) {
		t.Fatalf("stderr = %q, want the announced runtime", stderr)
	}
	var payload struct {
		SessionID       string           `json:"session_id"`
		DefaultsApplied []appliedDefault `json:"defaults_applied"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	want := appliedDefault{Flag: "os-version", Value: "iOS 27.0", Reason: "newest_runtime_for_model"}
	if payload.SessionID != sessionID || len(payload.DefaultsApplied) != 1 || payload.DefaultsApplied[0] != want {
		t.Fatalf("stdout = %s, want the session plus defaults_applied %+v", stdout, want)
	}
}

func TestResolveDeviceSelection_DefaultsOSVersionForTestRun(t *testing.T) {
	const testID = "11111111-2222-4333-8444-555555555555"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/execution/device-targets":
			_, _ = w.Write([]byte(defaultsTestCatalog))
		case strings.Contains(r.URL.Path, testID):
			_, _ = w.Write([]byte(`{"id":"` + testID + `","name":"checkout","platform":"ios"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	cmd := newStopTestCommand(t)
	var model, runtime string
	var err error
	_, stderr := captureStdoutAndStderrSeparate(t, func() {
		model, runtime, err = resolveDeviceSelection(cmd, testID, api.NewClientWithBaseURL("test-key", server.URL), false, "iPhone 16", "")
	})
	if err != nil || model != "iPhone 16" || runtime != "iOS 27.0" {
		t.Fatalf("resolveDeviceSelection() = %q, %q, %v; want iPhone 16 on iOS 27.0", model, runtime, err)
	}
	if !strings.Contains(stderr, "Pass --os-version to choose another.") {
		t.Fatalf("stderr = %q, want the announced runtime", stderr)
	}
	if _, _, err := resolveDeviceSelection(cmd, testID, api.NewClientWithBaseURL("test-key", server.URL), false, "", "iOS 27.0"); err == nil {
		t.Fatal("--os-version without --device-model was accepted")
	}
}

func TestRunTestExec_NoWaitJSONListsTheDefaultedRuntime(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	testutil.SetHomeDir(t, t.TempDir())
	withWorkingDir(t, t.TempDir())

	const testID = "11111111-2222-4333-8444-555555555555"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/execution/device-targets":
			_, _ = w.Write([]byte(defaultsTestCatalog))
		case r.URL.Path == "/api/v1/tests/get_test_by_id/"+testID:
			_, _ = w.Write([]byte(`{"id":"` + testID + `","name":"checkout","platform":"ios"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	originalRunTestExecution := runTestExecution
	originalRunNoWait, originalRunOpen, originalRunOutputJSON := runNoWait, runOpen, runOutputJSON
	originalRunRetries := runRetries
	originalRunDeviceModel, originalRunOsVersion, originalRunDeviceSelect := runDeviceModel, runOsVersion, runDeviceSelect
	t.Cleanup(func() {
		runTestExecution = originalRunTestExecution
		runNoWait, runOpen, runOutputJSON = originalRunNoWait, originalRunOpen, originalRunOutputJSON
		runRetries = originalRunRetries
		runDeviceModel, runOsVersion, runDeviceSelect = originalRunDeviceModel, originalRunOsVersion, originalRunDeviceSelect
	})

	var requested execution.RunTestParams
	runTestExecution = func(_ context.Context, _ string, _ *config.ProjectConfig, params execution.RunTestParams) (*execution.RunTestResult, error) {
		requested = params
		return &execution.RunTestResult{TaskID: "task-123", TestID: testID, Status: "queued"}, nil
	}
	runNoWait, runOpen, runOutputJSON = true, false, true
	runRetries = 1
	runDeviceModel, runOsVersion, runDeviceSelect = "iPhone 16", "", false

	cmd := newLeafCommand("run", runTestExec)
	cmd.Flags().Bool("open", false, "")
	cmd.Flags().Int("timeout", execution.DefaultRunTimeoutSeconds, "")

	var runErr error
	stdout, _ := captureStdoutAndStderrSeparate(t, func() { runErr = runTestExec(cmd, []string{testID}) })
	if runErr != nil {
		t.Fatalf("runTestExec() error = %v", runErr)
	}
	if requested.DeviceModel != "iPhone 16" || requested.OsVersion != "iOS 27.0" {
		t.Fatalf("run params = %q on %q, want iPhone 16 on iOS 27.0", requested.DeviceModel, requested.OsVersion)
	}
	var payload struct {
		TaskID          string           `json:"task_id"`
		DefaultsApplied []appliedDefault `json:"defaults_applied"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	want := appliedDefault{Flag: "os-version", Value: "iOS 27.0", Reason: "newest_runtime_for_model"}
	if payload.TaskID != "task-123" || len(payload.DefaultsApplied) != 1 || payload.DefaultsApplied[0] != want {
		t.Fatalf("stdout = %s, want the queued task plus defaults_applied %+v", stdout, want)
	}
}

func TestDeviceStartArtifactSelectors(t *testing.T) {
	const appID = "app-1"
	const buildVersionID = "build-1"
	testCases := []struct {
		name       string
		buildOwner string
		wantErr    string
	}{
		{name: "build version belongs to the app", buildOwner: appID},
		{name: "build version belongs to another app", buildOwner: "app-2", wantErr: "--build-version-id build-1 belongs to app app-2, not --app-id app-1. Pass only --build-version-id to start that build, or only --app-id to start the latest build of app-1"},
		{name: "owner unknown", wantErr: "Revyl did not report its app"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"` + buildVersionID + `","app_id":"` + tc.buildOwner + `","download_url":"https://artifact.example/app.ipa"}`))
			}))
			t.Cleanup(server.Close)

			err := confirmBuildVersionApp(context.Background(), api.NewClientWithBaseURL("test-key", server.URL), appID, buildVersionID)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("confirmBuildVersionApp() error = %v, want agreement", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("confirmBuildVersionApp() error = %v, want %q", err, tc.wantErr)
			}
		})
	}

	if err := checkDeviceStartArtifactSelectors(appID, buildVersionID, ""); err != nil {
		t.Fatalf("--app-id with --build-version-id rejected before the ownership check: %v", err)
	}
	if err := checkDeviceStartArtifactSelectors("", buildVersionID, "https://artifact.example/app.ipa"); err == nil ||
		!strings.Contains(err.Error(), "--app-url and --build-version-id conflict") {
		t.Fatalf("--app-url with --build-version-id error = %v, want the named conflict", err)
	}
}

// swipeDefaultsFlow serves a durable-ID session whose screen size is known
// only to the worker, and records the swipe body the worker received.
type swipeDefaultsFlow struct {
	Server    *httptest.Server
	mu        sync.Mutex
	swipeBody map[string]interface{}
	inputs    int
}

func newSwipeDefaultsFlow(t *testing.T) *swipeDefaultsFlow {
	t.Helper()
	flow := &swipeDefaultsFlow{}
	proxyPrefix := "/api/v1/execution/device-proxy/" + flowWorkflowRunID
	flow.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/execution/device-sessions/" + flowSessionID:
			_, _ = w.Write([]byte(`{"id":"` + flowSessionID + `","org_id":"org-1","platform":"ios","status":"running","workflow_run_id":"` + flowWorkflowRunID + `"}`))
		case proxyPrefix + "/health":
			_, _ = w.Write([]byte(`{"status":"ok","device_connected":true,"screen_width":1080,"screen_height":2400}`))
		case proxyPrefix + "/swipe":
			flow.mu.Lock()
			_ = json.NewDecoder(r.Body).Decode(&flow.swipeBody)
			flow.mu.Unlock()
			_, _ = w.Write([]byte(`{"success":true,"action":"swipe"}`))
		case proxyPrefix + "/input":
			flow.mu.Lock()
			flow.inputs++
			flow.mu.Unlock()
			_, _ = w.Write([]byte(`{"success":true,"action":"input"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(flow.Server.Close)
	return flow
}

func TestDeviceSwipeDefaultsToScreenCenter(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	flow := newSwipeDefaultsFlow(t)
	t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)

	cmd := newStopTestCommand(t, "--json", "-s", flowSessionID)
	cmd.Flags().String("target", "", "")
	cmd.Flags().Int("x", 0, "")
	cmd.Flags().Int("y", 0, "")
	cmd.Flags().String("direction", "", "")
	cmd.Flags().Int("duration", 500, "")
	var runErr error
	stdout, stderr := captureStdoutAndStderrSeparate(t, func() { runErr = deviceSwipeCmd.RunE(cmd, []string{"up"}) })
	if runErr != nil {
		t.Fatalf("device swipe error = %v, want a swipe from the screen center", runErr)
	}
	if flow.swipeBody["x"] != float64(540) || flow.swipeBody["y"] != float64(1200) || flow.swipeBody["direction"] != "up" {
		t.Fatalf("swipe body = %v, want direction up from (540, 1200)", flow.swipeBody)
	}
	if !strings.Contains(stderr, "Using the screen center (540, 1200) as the swipe start (no --target or --x/--y given). Pass --target or --x/--y to choose another.") {
		t.Fatalf("stderr = %q, want the announced start point", stderr)
	}
	var payload struct {
		X, Y            int
		DefaultsApplied []appliedDefault `json:"defaults_applied"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	wantDefaults := []appliedDefault{{Flag: "x", Value: "540", Reason: "screen_center"}, {Flag: "y", Value: "1200", Reason: "screen_center"}}
	if payload.X != 540 || payload.Y != 1200 || len(payload.DefaultsApplied) != 2 || payload.DefaultsApplied[0] != wantDefaults[0] || payload.DefaultsApplied[1] != wantDefaults[1] {
		t.Fatalf("stdout = %s, want the swipe result plus defaults_applied %+v", stdout, wantDefaults)
	}
}

func TestDeviceScreenCenterPrefersTheWorkersCurrentSize(t *testing.T) {
	for _, tc := range []struct {
		name         string
		healthBody   string
		wantX, wantY int
	}{
		{name: "worker reports the rotated size", healthBody: `{"status":"ok","device_connected":true,"screen_width":2532,"screen_height":1170}`, wantX: 1266, wantY: 585},
		{name: "worker omits the size", healthBody: `{"status":"ok","device_connected":true}`, wantX: 585, wantY: 1266},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.healthBody))
			}))
			t.Cleanup(server.Close)
			mgr := mcppkg.NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", server.URL), "")
			session := &mcppkg.DeviceSession{Index: mcppkg.UnattachedSessionIndex, WorkflowRunID: flowWorkflowRunID, ScreenWidth: 1170, ScreenHeight: 2532}
			x, y, err := deviceScreenCenter(context.Background(), mgr, session)
			if err != nil || x != tc.wantX || y != tc.wantY {
				t.Fatalf("deviceScreenCenter() = %d, %d, %v; want (%d, %d)", x, y, err, tc.wantX, tc.wantY)
			}
		})
	}
}

func TestDeviceTypeWithoutTargetNamesTheNextCommand(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	flow := newSwipeDefaultsFlow(t)
	t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)

	cmd := newStopTestCommand(t, "-s", flowSessionID)
	cmd.Flags().String("target", "", "")
	cmd.Flags().Int("x", 0, "")
	cmd.Flags().Int("y", 0, "")
	cmd.Flags().String("text", "hello", "")
	cmd.Flags().Bool("clear-first", true, "")
	var runErr error
	captureStdout(t, func() { runErr = deviceTypeCmd.RunE(cmd, nil) })
	if runErr == nil || !strings.Contains(runErr.Error(), `'revyl device type --target "email field" --text "<text>"'`) {
		t.Fatalf("device type error = %v, want the next command", runErr)
	}
	if flow.inputs != 0 {
		t.Fatal("device type sent input without a field to type into")
	}
}
