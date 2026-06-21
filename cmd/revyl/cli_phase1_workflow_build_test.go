package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/execution"
)

func newBuildUploadTestCommand() *cobra.Command {
	cmd := newLeafCommand("upload", runBuildUpload)
	cmd.Flags().Bool("dev", false, "")
	return cmd
}

func newBuildTestCommand() *cobra.Command {
	cmd := newLeafCommand("build", runBuild)
	cmd.Flags().Bool("dev", false, "")
	return cmd
}

func newWorkflowRunTestCommand() *cobra.Command {
	cmd := newLeafCommand("run", runWorkflowExec)
	cmd.Flags().Bool("open", false, "")
	cmd.Flags().Int("timeout", execution.DefaultRunTimeoutSeconds, "")
	cmd.Flags().Bool("dev", false, "")
	return cmd
}

func TestRunBuildLocalBuildUnsupportedOnWindows(t *testing.T) {
	previousGOOS := buildHostGOOS
	previousPlatform := buildCommandPlatform
	previousRemote := buildCommandRemote
	previousDetach := buildDetachFlag
	previousNoCache := buildNoCacheFlag
	previousJSON := buildCommandJSON
	defer func() {
		buildHostGOOS = previousGOOS
		buildCommandPlatform = previousPlatform
		buildCommandRemote = previousRemote
		buildDetachFlag = previousDetach
		buildNoCacheFlag = previousNoCache
		buildCommandJSON = previousJSON
	}()

	buildHostGOOS = "windows"
	buildCommandPlatform = ""
	buildCommandRemote = false
	buildDetachFlag = false
	buildNoCacheFlag = false
	buildCommandJSON = false

	err := runBuild(newBuildTestCommand(), nil)
	if err == nil {
		t.Fatal("runBuild() error = nil, want unsupported Windows local build error")
	}
	if !strings.Contains(err.Error(), "local builds are not supported on Windows") {
		t.Fatalf("runBuild() error = %q, want unsupported Windows local build guidance", err.Error())
	}
}

func TestRunSinglePlatformBuildUnsupportedOnWindows(t *testing.T) {
	previousGOOS := buildHostGOOS
	defer func() {
		buildHostGOOS = previousGOOS
	}()

	buildHostGOOS = "windows"

	err := runSinglePlatformBuild(newBuildTestCommand(), &config.ProjectConfig{}, filepath.Join(t.TempDir(), "config.yaml"), "test-key", "ios")
	if err == nil {
		t.Fatal("runSinglePlatformBuild() error = nil, want unsupported Windows local build error")
	}
	if !strings.Contains(err.Error(), "local builds are not supported on Windows") {
		t.Fatalf("runSinglePlatformBuild() error = %q, want unsupported Windows local build guidance", err.Error())
	}
}

func TestRunConcurrentBuildsRequiresConfiguredApps(t *testing.T) {
	previousRequireApp := buildRequireConfiguredApp
	previousDryRun := buildDryRun
	previousSkip := buildSkip
	defer func() {
		buildRequireConfiguredApp = previousRequireApp
		buildDryRun = previousDryRun
		buildSkip = previousSkip
	}()

	buildRequireConfiguredApp = true
	buildDryRun = false
	buildSkip = false

	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"ios": {
					Command: "xcodebuild -scheme Example",
					Output:  "build/Example.app.zip",
				},
				"android": {
					Command: "./gradlew assembleDebug",
					Output:  "app/build/outputs/apk/debug/app-debug.apk",
				},
			},
		},
	}

	err := runConcurrentBuilds(newBuildTestCommand(), cfg, filepath.Join(t.TempDir(), "config.yaml"), "test-key")
	if err == nil {
		t.Fatal("runConcurrentBuilds() error = nil, want missing configured app error")
	}
	if !strings.Contains(err.Error(), `no app is configured for platform "ios"`) {
		t.Fatalf("runConcurrentBuilds() error = %q, want missing ios app guidance", err.Error())
	}
}

func TestRunBuildJSONOutputsStructuredResult(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local revyl build execution is unsupported on Windows")
	}

	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/apps/app-android-123/builds/upload-session":
			if r.Method != http.MethodPost {
				t.Fatalf("upload-session method = %s, want POST", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"upload_id":"upload-123",
				"upload_url":"` + server.URL + `/uploads/upload-123",
				"upload_expires_at":123,
				"content_type":"application/vnd.android.package-archive"
			}`))
		case "/uploads/upload-123":
			if r.Method != http.MethodPut {
				t.Fatalf("upload target method = %s, want PUT", r.Method)
			}
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		case "/api/v1/apps/app-android-123/builds":
			if r.Method != http.MethodPost {
				t.Fatalf("create build method = %s, want POST", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id":"build-ver-123",
				"version":"1.2.3",
				"package_name":"com.example.android",
				"metadata":{
					"artifact_validation":{
						"warnings":["This Android APK does not appear to be debuggable."]
					}
				}
			}`))
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	tmp := t.TempDir()
	withWorkingDir(t, tmp)
	if err := os.MkdirAll(filepath.Join(tmp, ".revyl"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.revyl) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "build"), 0o755); err != nil {
		t.Fatalf("MkdirAll(build) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "build", "app.apk"), []byte("apk-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile(app.apk) error = %v", err)
	}

	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"android": {
					Command: "true",
					Output:  "build/app.apk",
					AppID:   "app-android-123",
				},
			},
		},
	}
	if err := config.WriteProjectConfig(filepath.Join(tmp, ".revyl", "config.yaml"), cfg); err != nil {
		t.Fatalf("WriteProjectConfig() error = %v", err)
	}

	originalBuildVersion := buildVersion
	originalBuildNoSetCurrent := buildNoSetCurrent
	originalBuildCommandJSON := buildCommandJSON
	originalBuildCommandPlatform := buildCommandPlatform
	originalBuildCommandRemote := buildCommandRemote
	originalBuildDetachFlag := buildDetachFlag
	originalBuildNoCacheFlag := buildNoCacheFlag
	originalBuildRequireConfiguredApp := buildRequireConfiguredApp
	t.Cleanup(func() {
		buildVersion = originalBuildVersion
		buildNoSetCurrent = originalBuildNoSetCurrent
		buildCommandJSON = originalBuildCommandJSON
		buildCommandPlatform = originalBuildCommandPlatform
		buildCommandRemote = originalBuildCommandRemote
		buildDetachFlag = originalBuildDetachFlag
		buildNoCacheFlag = originalBuildNoCacheFlag
		buildRequireConfiguredApp = originalBuildRequireConfiguredApp
	})

	buildVersion = "1.2.3"
	buildNoSetCurrent = false
	buildCommandJSON = true
	buildCommandPlatform = "android"
	buildCommandRemote = false
	buildDetachFlag = false
	buildNoCacheFlag = false
	buildRequireConfiguredApp = false

	cmd := newBuildTestCommand()
	output := captureStdout(t, func() {
		if err := runBuild(cmd, nil); err != nil {
			t.Fatalf("runBuild() error = %v", err)
		}
	})

	result := parseJSON(t, output)
	if got, ok := result["success"].(bool); !ok || !got {
		t.Fatalf("success = %v, want true", result["success"])
	}
	if got := int(result["count"].(float64)); got != 1 {
		t.Fatalf("count = %d, want 1", got)
	}
	buildObj, ok := result["build"].(map[string]interface{})
	if !ok {
		t.Fatalf("build missing or wrong type: %#v", result["build"])
	}
	assertJSONString(t, buildObj, "platform_key", "android")
	assertJSONString(t, buildObj, "platform", "android")
	assertJSONString(t, buildObj, "app_id", "app-android-123")
	assertJSONString(t, buildObj, "build_version", "1.2.3")
	assertJSONString(t, buildObj, "build_id", "build-ver-123")
	assertJSONString(t, buildObj, "package_id", "com.example.android")
	warnings, ok := buildObj["warnings"].([]interface{})
	if !ok || len(warnings) != 1 {
		t.Fatalf("warnings = %#v, want one warning", buildObj["warnings"])
	}
	if got, _ := warnings[0].(string); !strings.Contains(got, "debuggable") {
		t.Fatalf("warning = %q, want debuggable warning", got)
	}
	assertJSONKey(t, buildObj, "artifact_path")
}

func TestRunWorkflowExecNoWaitOutputsQueuedJSON(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())

	var executeReq api.ExecuteWorkflowRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/workflows/get_with_last_status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"wf-uuid-001","name":"smoke-tests"}]}`))
		case "/api/v1/execution/api/execute_workflow_id_async":
			if r.Method != http.MethodPost {
				t.Fatalf("execute workflow method = %s, want POST", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&executeReq); err != nil {
				t.Fatalf("Decode execute request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"task_id":"queued-workflow-task"}`))
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	tmp := t.TempDir()
	withWorkingDir(t, tmp)
	if err := os.MkdirAll(filepath.Join(tmp, ".revyl"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.revyl) error = %v", err)
	}
	cfg := &config.ProjectConfig{}
	if err := config.WriteProjectConfig(filepath.Join(tmp, ".revyl", "config.yaml"), cfg); err != nil {
		t.Fatalf("WriteProjectConfig() error = %v", err)
	}

	originalRunNoWait := runNoWait
	originalRunOpen := runOpen
	originalRunRetries := runRetries
	originalRunOutputJSON := runOutputJSON
	originalRunGitHubActions := runGitHubActions
	originalRunWorkflowBuild := runWorkflowBuild
	originalRunWorkflowPlatform := runWorkflowPlatform
	originalRunWorkflowIOSAppID := runWorkflowIOSAppID
	originalRunWorkflowAndroidAppID := runWorkflowAndroidAppID
	originalRunLocation := runLocation
	originalRunOpenBrowserFn := runOpenBrowserFn
	t.Cleanup(func() {
		runNoWait = originalRunNoWait
		runOpen = originalRunOpen
		runRetries = originalRunRetries
		runOutputJSON = originalRunOutputJSON
		runGitHubActions = originalRunGitHubActions
		runWorkflowBuild = originalRunWorkflowBuild
		runWorkflowPlatform = originalRunWorkflowPlatform
		runWorkflowIOSAppID = originalRunWorkflowIOSAppID
		runWorkflowAndroidAppID = originalRunWorkflowAndroidAppID
		runLocation = originalRunLocation
		runOpenBrowserFn = originalRunOpenBrowserFn
	})
	runOpenBrowserFn = func(_ string) error { return nil }

	runNoWait = true
	runOpen = false
	runRetries = 2
	runOutputJSON = true
	runGitHubActions = false
	runWorkflowBuild = false
	runWorkflowPlatform = ""
	runWorkflowIOSAppID = ""
	runWorkflowAndroidAppID = ""
	runLocation = ""

	cmd := newWorkflowRunTestCommand()
	output := captureStdout(t, func() {
		if err := runWorkflowExec(cmd, []string{"smoke-tests"}); err != nil {
			t.Fatalf("runWorkflowExec() error = %v", err)
		}
	})

	result := parseJSON(t, output)
	if got, ok := result["success"].(bool); !ok || !got {
		t.Fatalf("success = %v, want true", result["success"])
	}
	if got, ok := result["queued"].(bool); !ok || !got {
		t.Fatalf("queued = %v, want true", result["queued"])
	}
	assertJSONString(t, result, "task_id", "queued-workflow-task")
	assertJSONString(t, result, "workflow_id", "wf-uuid-001")
	assertJSONString(t, result, "workflow_name", "smoke-tests")
	assertJSONString(t, result, "status", "queued")
	assertJSONKey(t, result, "report_link")

	if executeReq.WorkflowID != "wf-uuid-001" {
		t.Fatalf("WorkflowID = %q, want wf-uuid-001", executeReq.WorkflowID)
	}
	if executeReq.Retries != 2 {
		t.Fatalf("Retries = %d, want 2", executeReq.Retries)
	}
}

func TestRunWorkflowExecBlockingUsesResolvedWorkflowUUID(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/workflows/get_with_last_status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"wf-uuid-001","name":"smoke-tests"}]}`))
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	tmp := t.TempDir()
	withWorkingDir(t, tmp)
	if err := os.MkdirAll(filepath.Join(tmp, ".revyl"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.revyl) error = %v", err)
	}
	if err := config.WriteProjectConfig(filepath.Join(tmp, ".revyl", "config.yaml"), &config.ProjectConfig{}); err != nil {
		t.Fatalf("WriteProjectConfig() error = %v", err)
	}

	originalRunNoWait := runNoWait
	originalRunOpen := runOpen
	originalRunRetries := runRetries
	originalRunOutputJSON := runOutputJSON
	originalRunGitHubActions := runGitHubActions
	originalRunWorkflowBuild := runWorkflowBuild
	originalRunWorkflowPlatform := runWorkflowPlatform
	originalRunWorkflowIOSAppID := runWorkflowIOSAppID
	originalRunWorkflowAndroidAppID := runWorkflowAndroidAppID
	originalRunLocation := runLocation
	originalRunOpenBrowserFn := runOpenBrowserFn
	originalRunWorkflowExecution := runWorkflowExecution
	t.Cleanup(func() {
		runNoWait = originalRunNoWait
		runOpen = originalRunOpen
		runRetries = originalRunRetries
		runOutputJSON = originalRunOutputJSON
		runGitHubActions = originalRunGitHubActions
		runWorkflowBuild = originalRunWorkflowBuild
		runWorkflowPlatform = originalRunWorkflowPlatform
		runWorkflowIOSAppID = originalRunWorkflowIOSAppID
		runWorkflowAndroidAppID = originalRunWorkflowAndroidAppID
		runLocation = originalRunLocation
		runOpenBrowserFn = originalRunOpenBrowserFn
		runWorkflowExecution = originalRunWorkflowExecution
	})
	runOpenBrowserFn = func(_ string) error { return nil }

	var captured execution.RunWorkflowParams
	runWorkflowExecution = func(_ context.Context, _ string, _ *config.ProjectConfig, params execution.RunWorkflowParams) (*execution.RunWorkflowResult, error) {
		captured = params
		return &execution.RunWorkflowResult{
			Success:      true,
			TaskID:       "workflow-task",
			WorkflowID:   params.WorkflowNameOrID,
			WorkflowName: "smoke-tests",
			Status:       "completed",
			TotalTests:   1,
			PassedTests:  1,
			ReportURL:    "https://app.example/workflows/report?taskId=workflow-task",
		}, nil
	}

	runNoWait = false
	runOpen = false
	runRetries = 1
	runOutputJSON = true
	runGitHubActions = false
	runWorkflowBuild = false
	runWorkflowPlatform = ""
	runWorkflowIOSAppID = ""
	runWorkflowAndroidAppID = ""
	runLocation = ""

	cmd := newWorkflowRunTestCommand()
	output := captureStdout(t, func() {
		if err := runWorkflowExec(cmd, []string{"smoke-tests"}); err != nil {
			t.Fatalf("runWorkflowExec() error = %v", err)
		}
	})

	result := parseJSON(t, output)
	assertJSONString(t, result, "workflow_id", "wf-uuid-001")
	if captured.WorkflowNameOrID != "wf-uuid-001" {
		t.Fatalf("WorkflowNameOrID = %q, want wf-uuid-001", captured.WorkflowNameOrID)
	}
}
