package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

func newBuildUploadTestCommand() *cobra.Command {
	cmd := newLeafCommand("upload", runBuildUpload)
	cmd.Flags().Bool("dev", false, "")
	return cmd
}

func newWorkflowRunTestCommand() *cobra.Command {
	cmd := newLeafCommand("run", runWorkflowExec)
	cmd.Flags().Bool("open", false, "")
	cmd.Flags().Int("timeout", 3600, "")
	cmd.Flags().Bool("dev", false, "")
	return cmd
}

func TestRunBuildUploadJSONOutputsStructuredResult(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/builds/vars/app-android-123/versions/upload-url":
			if r.Method != http.MethodPost {
				t.Fatalf("upload-url method = %s, want POST", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"version_id":"build-ver-123",
				"version":"1.2.3",
				"upload_url":"` + server.URL + `/uploads/build-ver-123",
				"content_type":"application/vnd.android.package-archive"
			}`))
		case "/uploads/build-ver-123":
			if r.Method != http.MethodPut {
				t.Fatalf("upload target method = %s, want PUT", r.Method)
			}
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		case "/api/v1/builds/versions/build-ver-123/extract-package-id":
			if r.Method != http.MethodPost {
				t.Fatalf("extract-package-id method = %s, want POST", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"package_id":"com.example.android"}`))
		case "/api/v1/builds/versions/build-ver-123/complete-upload":
			if r.Method != http.MethodPost {
				t.Fatalf("complete-upload method = %s, want POST", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"version":"1.2.3",
				"package_id":"com.example.android"
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
					Command: "cd android && ./gradlew assembleDebug",
					Output:  "build/app.apk",
					AppID:   "app-android-123",
				},
			},
		},
	}
	if err := config.WriteProjectConfig(filepath.Join(tmp, ".revyl", "config.yaml"), cfg); err != nil {
		t.Fatalf("WriteProjectConfig() error = %v", err)
	}

	originalBuildSkip := buildSkip
	originalBuildVersion := buildVersion
	originalUploadPlatformFlag := uploadPlatformFlag
	originalBuildUploadJSON := buildUploadJSON
	originalBuildDryRun := buildDryRun
	originalBuildSetCurr := buildSetCurr
	originalUploadAppFlag := uploadAppFlag
	originalUploadNameFlag := uploadNameFlag
	originalUploadYesFlag := uploadYesFlag
	originalUploadSchemeFlag := uploadSchemeFlag
	t.Cleanup(func() {
		buildSkip = originalBuildSkip
		buildVersion = originalBuildVersion
		uploadPlatformFlag = originalUploadPlatformFlag
		buildUploadJSON = originalBuildUploadJSON
		buildDryRun = originalBuildDryRun
		buildSetCurr = originalBuildSetCurr
		uploadAppFlag = originalUploadAppFlag
		uploadNameFlag = originalUploadNameFlag
		uploadYesFlag = originalUploadYesFlag
		uploadSchemeFlag = originalUploadSchemeFlag
	})

	buildSkip = true
	buildVersion = "1.2.3"
	uploadPlatformFlag = "android"
	buildUploadJSON = true
	buildDryRun = false
	buildSetCurr = false
	uploadAppFlag = ""
	uploadNameFlag = ""
	uploadYesFlag = false
	uploadSchemeFlag = ""

	cmd := newBuildUploadTestCommand()
	output := captureStdout(t, func() {
		if err := runBuildUpload(cmd, nil); err != nil {
			t.Fatalf("runBuildUpload() error = %v", err)
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
