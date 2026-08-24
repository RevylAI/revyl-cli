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

func TestRunBuildJSONOutputsStructuredResult(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local revyl build execution is unsupported on Windows")
	}

	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())

	const appID = "00000000-0000-4000-8000-000000000123"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/apps/" + appID + "/builds/upload-session":
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
		case "/api/v1/apps/" + appID + "/builds":
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
	gitInitBuildRepository(t, tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "build"), 0o755); err != nil {
		t.Fatalf("MkdirAll(build) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "build", "app.apk"), []byte("apk-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile(app.apk) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "app.json"), []byte(`{"expo":{"scheme":"example-dev"}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(app.json) error = %v", err)
	}

	writeProjectBuildConfig(t, tmp, `project:
  id: `+buildTestProjectID+`
build:
  framework: expo
  profiles:
    development:
      android:
        app_id: `+appID+`
        build_commands: ["true"]
        output_path: build/app.apk
`)

	originalBuildVersion := buildVersion
	originalBuildNoSetCurrent := buildNoSetCurrent
	originalBuildCommandJSON := buildCommandJSON
	originalBuildCommandProfile := buildCommandProfile
	originalBuildCommandPlatform := buildCommandPlatform
	originalBuildCommandRemote := buildCommandRemote
	originalBuildDetachFlag := buildDetachFlag
	originalBuildNoCacheFlag := buildNoCacheFlag
	originalBuildRequireConfiguredApp := buildRequireConfiguredApp
	t.Cleanup(func() {
		buildVersion = originalBuildVersion
		buildNoSetCurrent = originalBuildNoSetCurrent
		buildCommandJSON = originalBuildCommandJSON
		buildCommandProfile = originalBuildCommandProfile
		buildCommandPlatform = originalBuildCommandPlatform
		buildCommandRemote = originalBuildCommandRemote
		buildDetachFlag = originalBuildDetachFlag
		buildNoCacheFlag = originalBuildNoCacheFlag
		buildRequireConfiguredApp = originalBuildRequireConfiguredApp
	})

	buildVersion = "1.2.3"
	buildNoSetCurrent = false
	buildCommandJSON = true
	buildCommandProfile = "development"
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
	assertJSONString(t, buildObj, "profile", "development")
	assertJSONString(t, buildObj, "app_id", appID)
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

func TestRunTestExecBuildUsesUploadedBuildVersionID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local revyl build execution is unsupported on Windows")
	}

	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())

	const appID = "00000000-0000-4000-8000-000000000123"
	var createRequest map[string]interface{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/tests/get_simple_tests":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"tests":[{"id":"test-uuid-001","name":"Login Flow","platform":"android"}],"count":1}`))
		case "/api/v1/apps/" + appID + "/builds/upload-session":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"upload_id":"upload-123","upload_url":"` + server.URL + `/uploads/upload-123","content_type":"application/vnd.android.package-archive"}`))
		case "/uploads/upload-123":
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		case "/api/v1/apps/" + appID + "/builds":
			if err := json.NewDecoder(r.Body).Decode(&createRequest); err != nil {
				t.Fatalf("Decode create build request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"uploaded-build-id","version":"uploaded-version"}`))
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	projectRoot := t.TempDir()
	withWorkingDir(t, projectRoot)
	gitInitBuildRepository(t, projectRoot)
	writeExpoMetadataProjectFile(t, projectRoot, "app.json", `{"expo":{"scheme":"test-app"}}`)
	if err := os.MkdirAll(filepath.Join(projectRoot, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "build", "app.apk"), []byte("apk"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeProjectBuildConfig(t, projectRoot, `project:
  id: `+buildTestProjectID+`
build:
  framework: expo
  profiles:
    development:
      android:
        app_id: `+appID+`
        build_commands: ["true"]
        output_path: build/app.apk
`)

	originalRunTestExecution := runTestExecution
	originalRunTestBuild, originalRunTestProfile, originalRunTestPlatform := runTestBuild, runTestProfile, runTestPlatform
	originalRunBuildID, originalRunNoWait, originalRunOpen := runBuildID, runNoWait, runOpen
	originalRunRetries, originalRunOutputJSON, originalRunGitHubActions := runRetries, runOutputJSON, runGitHubActions
	originalBuildVersion := buildVersion
	t.Cleanup(func() {
		runTestExecution = originalRunTestExecution
		runTestBuild, runTestProfile, runTestPlatform = originalRunTestBuild, originalRunTestProfile, originalRunTestPlatform
		runBuildID, runNoWait, runOpen = originalRunBuildID, originalRunNoWait, originalRunOpen
		runRetries, runOutputJSON, runGitHubActions = originalRunRetries, originalRunOutputJSON, originalRunGitHubActions
		buildVersion = originalBuildVersion
	})

	var captured execution.RunTestParams
	runTestExecution = func(_ context.Context, _ string, _ *config.ProjectConfig, params execution.RunTestParams) (*execution.RunTestResult, error) {
		captured = params
		return &execution.RunTestResult{
			Success: true, TaskID: "test-task", TestID: params.TestNameOrID, TestName: "Login Flow",
			Status: "completed", ReportURL: "https://app.example/tests/report?taskId=test-task",
		}, nil
	}
	runTestBuild, runTestProfile, runTestPlatform = true, "development", "android"
	runBuildID, runNoWait, runOpen = "", true, false
	runRetries, runOutputJSON, runGitHubActions = 1, true, false
	buildVersion = "requested-version"

	output := captureStdout(t, func() {
		if err := runTestExec(newWorkflowRunTestCommand(), []string{"Login Flow"}); err != nil {
			t.Fatalf("runTestExec() error = %v", err)
		}
	})
	_ = parseJSON(t, output)
	if captured.BuildVersionID != "uploaded-build-id" {
		t.Fatalf("BuildVersionID = %q, want uploaded-build-id", captured.BuildVersionID)
	}
	if setAsCurrent, ok := createRequest["set_as_current"].(bool); !ok || setAsCurrent {
		t.Fatalf("set_as_current = %#v, want false", createRequest["set_as_current"])
	}
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

func TestRunWorkflowExecBuildUsesUploadedArtifactWithoutWait(t *testing.T) {
	runWorkflowBuildArtifactCase(t, true)
}

func TestRunWorkflowExecBuildUsesUploadedArtifactWhileWaiting(t *testing.T) {
	runWorkflowBuildArtifactCase(t, false)
}

func runWorkflowBuildArtifactCase(t *testing.T, noWait bool) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("local revyl build execution is unsupported on Windows")
	}

	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())
	const builtAppID = "00000000-0000-4000-8000-000000000123"
	const oppositeAppID = "00000000-0000-4000-8000-000000000456"
	const oppositeVersion = "existing-ios-version"

	var queuedRequest api.ExecuteWorkflowRequest
	var createRequest map[string]interface{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/workflows/get_with_last_status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"wf-uuid-001","name":"smoke-tests"}]}`))
		case "/api/v1/workflows/get_workflow_info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"wf-uuid-001","name":"smoke-tests"}`))
		case "/api/v1/apps/" + oppositeAppID:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"` + oppositeAppID + `","name":"iOS app","platform":"ios","latest_version":"` + oppositeVersion + `","versions_count":1}`))
		case "/api/v1/apps/" + oppositeAppID + "/builds":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[{"id":"ios-build-id","version":"` + oppositeVersion + `"}],"total":1,"page":1,"page_size":100,"total_pages":1}`))
		case "/api/v1/apps/" + builtAppID + "/builds/upload-session":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"upload_id":"upload-123","upload_url":"` + server.URL + `/uploads/upload-123","content_type":"application/vnd.android.package-archive"}`))
		case "/uploads/upload-123":
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		case "/api/v1/apps/" + builtAppID + "/builds":
			if err := json.NewDecoder(r.Body).Decode(&createRequest); err != nil {
				t.Fatalf("Decode create build request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"uploaded-build-id","version":"uploaded-android-version"}`))
		case "/api/v1/execution/api/execute_workflow_id_async":
			if err := json.NewDecoder(r.Body).Decode(&queuedRequest); err != nil {
				t.Fatalf("Decode workflow request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"task_id":"workflow-task"}`))
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	projectRoot := t.TempDir()
	withWorkingDir(t, projectRoot)
	gitInitBuildRepository(t, projectRoot)
	writeExpoMetadataProjectFile(t, projectRoot, "app.json", `{"expo":{"scheme":"test-app"}}`)
	if err := os.MkdirAll(filepath.Join(projectRoot, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "build", "app.apk"), []byte("apk"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeProjectBuildConfig(t, projectRoot, `project:
  id: `+buildTestProjectID+`
build:
  framework: expo
  profiles:
    development:
      android:
        app_id: `+builtAppID+`
        build_commands: ["true"]
        output_path: build/app.apk
`)

	originalRunWorkflowExecution := runWorkflowExecution
	originalRunNoWait, originalRunOpen, originalRunRetries := runNoWait, runOpen, runRetries
	originalRunOutputJSON, originalRunGitHubActions := runOutputJSON, runGitHubActions
	originalRunWorkflowBuild, originalRunWorkflowProfile, originalRunWorkflowPlatform := runWorkflowBuild, runWorkflowProfile, runWorkflowPlatform
	originalIOSAppID, originalAndroidAppID := runWorkflowIOSAppID, runWorkflowAndroidAppID
	originalIOSBuild, originalAndroidBuild := runWorkflowIOSBuild, runWorkflowAndroidBuild
	originalRunLocation, originalBuildVersion := runLocation, buildVersion
	t.Cleanup(func() {
		runWorkflowExecution = originalRunWorkflowExecution
		runNoWait, runOpen, runRetries = originalRunNoWait, originalRunOpen, originalRunRetries
		runOutputJSON, runGitHubActions = originalRunOutputJSON, originalRunGitHubActions
		runWorkflowBuild, runWorkflowProfile, runWorkflowPlatform = originalRunWorkflowBuild, originalRunWorkflowProfile, originalRunWorkflowPlatform
		runWorkflowIOSAppID, runWorkflowAndroidAppID = originalIOSAppID, originalAndroidAppID
		runWorkflowIOSBuild, runWorkflowAndroidBuild = originalIOSBuild, originalAndroidBuild
		runLocation, buildVersion = originalRunLocation, originalBuildVersion
	})

	var waitedParams execution.RunWorkflowParams
	runWorkflowExecution = func(_ context.Context, _ string, _ *config.ProjectConfig, params execution.RunWorkflowParams) (*execution.RunWorkflowResult, error) {
		waitedParams = params
		return &execution.RunWorkflowResult{
			Success: true, TaskID: "workflow-task", WorkflowID: params.WorkflowNameOrID, WorkflowName: "smoke-tests",
			Status: "completed", TotalTests: 1, PassedTests: 1, ReportURL: "https://app.example/workflows/report?taskId=workflow-task",
		}, nil
	}
	runNoWait, runOpen, runRetries = noWait, false, 1
	runOutputJSON, runGitHubActions = true, false
	runWorkflowBuild, runWorkflowProfile, runWorkflowPlatform = true, "development", "android"
	runWorkflowIOSAppID, runWorkflowAndroidAppID = oppositeAppID, ""
	runWorkflowIOSBuild, runWorkflowAndroidBuild = oppositeVersion, ""
	runLocation, buildVersion = "", "requested-version"

	output := captureStdout(t, func() {
		if err := runWorkflowExec(newWorkflowRunTestCommand(), []string{"smoke-tests"}); err != nil {
			t.Fatalf("runWorkflowExec() error = %v", err)
		}
	})
	_ = parseJSON(t, output)
	if setAsCurrent, ok := createRequest["set_as_current"].(bool); !ok || setAsCurrent {
		t.Fatalf("set_as_current = %#v, want false", createRequest["set_as_current"])
	}

	if noWait {
		if queuedRequest.BuildConfig == nil || queuedRequest.BuildConfig.AndroidBuild == nil || queuedRequest.BuildConfig.IosBuild == nil {
			t.Fatalf("queued build config = %+v", queuedRequest.BuildConfig)
		}
		if got := queuedRequest.BuildConfig.AndroidBuild.AppId.String(); got != builtAppID {
			t.Fatalf("queued Android app = %q, want %q", got, builtAppID)
		}
		if queuedRequest.BuildConfig.AndroidBuild.PinnedVersion == nil || *queuedRequest.BuildConfig.AndroidBuild.PinnedVersion != "uploaded-android-version" {
			t.Fatalf("queued Android version = %#v", queuedRequest.BuildConfig.AndroidBuild.PinnedVersion)
		}
		if got := queuedRequest.BuildConfig.IosBuild.AppId.String(); got != oppositeAppID {
			t.Fatalf("queued iOS app = %q, want %q", got, oppositeAppID)
		}
		if queuedRequest.BuildConfig.IosBuild.PinnedVersion == nil || *queuedRequest.BuildConfig.IosBuild.PinnedVersion != oppositeVersion {
			t.Fatalf("queued iOS version = %#v", queuedRequest.BuildConfig.IosBuild.PinnedVersion)
		}
		return
	}

	if waitedParams.AndroidAppID != builtAppID || waitedParams.AndroidBuild != "uploaded-android-version" {
		t.Fatalf("waited Android override = %q/%q", waitedParams.AndroidAppID, waitedParams.AndroidBuild)
	}
	if waitedParams.IOSAppID != oppositeAppID || waitedParams.IOSBuild != oppositeVersion {
		t.Fatalf("waited iOS override = %q/%q", waitedParams.IOSAppID, waitedParams.IOSBuild)
	}
}

func TestRunWorkflowExecRejectsSamePlatformSelectorsBeforeUpload(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())
	var uploadRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/workflows/get_with_last_status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"wf-uuid-001","name":"smoke-tests"}]}`))
		case "/api/v1/workflows/get_workflow_info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"wf-uuid-001","name":"smoke-tests"}`))
		default:
			uploadRequests++
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	projectRoot := t.TempDir()
	withWorkingDir(t, projectRoot)
	gitInitBuildRepository(t, projectRoot)
	writeProjectBuildConfig(t, projectRoot, projectBuildConfigYAML("development", "android", "build/app.apk", true))

	originalBuild, originalProfile, originalPlatform := runWorkflowBuild, runWorkflowProfile, runWorkflowPlatform
	originalAndroidAppID, originalAndroidBuild := runWorkflowAndroidAppID, runWorkflowAndroidBuild
	originalRetries := runRetries
	t.Cleanup(func() {
		runWorkflowBuild, runWorkflowProfile, runWorkflowPlatform = originalBuild, originalProfile, originalPlatform
		runWorkflowAndroidAppID, runWorkflowAndroidBuild = originalAndroidAppID, originalAndroidBuild
		runRetries = originalRetries
	})
	runWorkflowBuild, runWorkflowProfile, runWorkflowPlatform = true, "development", "android"
	runWorkflowAndroidAppID, runWorkflowAndroidBuild = "00000000-0000-4000-8000-000000000123", "existing-version"
	runRetries = 1

	err := runWorkflowExec(newWorkflowRunTestCommand(), []string{"smoke-tests"})
	if err == nil || !strings.Contains(err.Error(), "--android-app") || !strings.Contains(err.Error(), "--android-build") {
		t.Fatalf("runWorkflowExec() error = %v", err)
	}
	if uploadRequests != 0 {
		t.Fatalf("requests after preflight conflict = %d, want 0", uploadRequests)
	}
}
