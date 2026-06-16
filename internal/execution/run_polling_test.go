package execution

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/revyl/cli/internal/sse"
)

func TestRunTest_PollingModeCompletesWithoutSSE(t *testing.T) {
	var sseRequested bool
	var statusCalls int
	var progressUpdates []sse.TestStatus

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/execution/api/execute_test_id_async":
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"task_id":"task-test-123"}`))
		case "/api/v1/tests/get_test_execution_task":
			statusCalls++
			if got := r.URL.Query().Get("task_id"); got != "task-test-123" {
				t.Fatalf("task_id query = %q, want task-test-123", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"status":"completed",
				"progress":100,
				"current_step":"Done",
				"steps_completed":3,
				"total_steps":3,
				"success":true,
				"execution_time_seconds":4.2
			}`))
		case "/api/v1/monitor/stream/unified":
			sseRequested = true
			http.Error(w, "unexpected SSE request", http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_APP_URL", "https://app.example")

	result, err := RunTest(context.Background(), "token", nil, RunTestParams{
		TestNameOrID:   "test-123",
		Timeout:        5,
		MonitoringMode: sse.MonitoringModePolling,
		OnProgress: func(status *sse.TestStatus) {
			progressUpdates = append(progressUpdates, *status)
		},
	})
	if err != nil {
		t.Fatalf("RunTest() error = %v", err)
	}
	if sseRequested {
		t.Fatal("expected polling mode to avoid the SSE endpoint")
	}
	if statusCalls == 0 {
		t.Fatal("expected at least one polling status call")
	}
	if !result.Success {
		t.Fatalf("Success = false, want true")
	}
	if result.TaskID != "task-test-123" {
		t.Fatalf("TaskID = %q, want task-test-123", result.TaskID)
	}
	if result.Status != "completed" {
		t.Fatalf("Status = %q, want completed", result.Status)
	}
	if result.ReportURL != "https://app.example/tests/report?taskId=task-test-123" {
		t.Fatalf("ReportURL = %q", result.ReportURL)
	}
	if len(progressUpdates) == 0 {
		t.Fatal("expected at least one progress update")
	}
	lastUpdate := progressUpdates[len(progressUpdates)-1]
	if lastUpdate.Status != "completed" {
		t.Fatalf("final progress status = %q, want completed", lastUpdate.Status)
	}
	if lastUpdate.TotalSteps != 3 || lastUpdate.CompletedSteps != 3 {
		t.Fatalf("progress steps = %d/%d, want 3/3", lastUpdate.CompletedSteps, lastUpdate.TotalSteps)
	}
	if lastUpdate.Progress != 100 {
		t.Fatalf("Progress = %d, want 100", lastUpdate.Progress)
	}
}

func TestRunWorkflow_PollingModeCompletesWithoutSSE(t *testing.T) {
	var sseRequested bool
	var statusCalls int
	var progressUpdates []sse.WorkflowStatus

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/execution/api/execute_workflow_id_async":
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"task_id":"task-workflow-456"}`))
		case "/api/v1/workflows/status/task-workflow-456":
			statusCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"status":"completed",
				"total_tests":1,
				"completed_tests":1,
				"passed_tests":1,
				"failed_tests":0,
				"duration":"1m 41s",
				"error_message":""
			}`))
		case "/api/v1/monitor/stream/unified":
			sseRequested = true
			http.Error(w, "unexpected SSE request", http.StatusInternalServerError)
		case "/api/v1/workflows/share/unified-report":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"workflow_task": {"id":"task-workflow-456","status":"completed"},
				"workflow_detail": {"id":"workflow-123","name":"Smoke Tests","tests":["t1"]},
				"test_info": [],
				"child_tasks": [{
					"task_id":"child-1",
					"test_name":"Login Flow",
					"platform":"android",
					"status":"completed",
					"success":true,
					"execution_time_seconds":42.5
				}]
			}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_APP_URL", "https://app.example")

	result, err := RunWorkflow(context.Background(), "token", nil, RunWorkflowParams{
		WorkflowNameOrID: "workflow-123",
		Timeout:          5,
		MonitoringMode:   sse.MonitoringModePolling,
		OnProgress: func(status *sse.WorkflowStatus) {
			progressUpdates = append(progressUpdates, *status)
		},
	})
	if err != nil {
		t.Fatalf("RunWorkflow() error = %v", err)
	}
	if sseRequested {
		t.Fatal("expected polling mode to avoid the SSE endpoint")
	}
	if statusCalls == 0 {
		t.Fatal("expected at least one workflow polling status call")
	}
	if !result.Success {
		t.Fatalf("Success = false, want true")
	}
	if result.TaskID != "task-workflow-456" {
		t.Fatalf("TaskID = %q, want task-workflow-456", result.TaskID)
	}
	if result.Status != "completed" {
		t.Fatalf("Status = %q, want completed", result.Status)
	}
	if result.TotalTests != 1 || result.PassedTests != 1 || result.FailedTests != 0 {
		t.Fatalf("workflow counts = total:%d passed:%d failed:%d", result.TotalTests, result.PassedTests, result.FailedTests)
	}
	if result.CompletedTests != 1 {
		t.Fatalf("CompletedTests = %d, want 1", result.CompletedTests)
	}
	if result.ReportURL != "https://app.example/workflows/report?taskId=task-workflow-456" {
		t.Fatalf("ReportURL = %q", result.ReportURL)
	}
	if len(progressUpdates) == 0 {
		t.Fatal("expected at least one workflow progress update")
	}
	lastUpdate := progressUpdates[len(progressUpdates)-1]
	if lastUpdate.Status != "completed" {
		t.Fatalf("final workflow progress status = %q, want completed", lastUpdate.Status)
	}
	if lastUpdate.TotalTests != 1 || lastUpdate.CompletedTests != 1 {
		t.Fatalf("workflow progress counts = %d/%d, want 1/1", lastUpdate.CompletedTests, lastUpdate.TotalTests)
	}
	if len(result.Tests) != 1 {
		t.Fatalf("Tests count = %d, want 1", len(result.Tests))
	}
	if result.Tests[0].TestName != "Login Flow" {
		t.Fatalf("Tests[0].TestName = %q, want Login Flow", result.Tests[0].TestName)
	}
	if !result.Tests[0].Success {
		t.Fatal("Tests[0].Success = false, want true")
	}
	if result.WorkflowName != "Smoke Tests" {
		t.Fatalf("WorkflowName = %q, want Smoke Tests", result.WorkflowName)
	}
}

// TestRunWorkflow_PinsBuildVersion verifies that, for both platforms, an app
// override plus a build version override serialize into
// build_config.<platform>.pinned_version (and that an unpinned platform omits it).
func TestRunWorkflow_PinsBuildVersion(t *testing.T) {
	const iosAppID = "11111111-1111-1111-1111-111111111111"
	const androidAppID = "22222222-2222-2222-2222-222222222222"

	cases := []struct {
		name            string
		params          RunWorkflowParams
		wantIosApp      string
		wantIosVersion  interface{} // string, or nil when unset
		wantAndroidApp  string
		wantAndroidVers interface{}
	}{
		{
			name: "both platforms pinned",
			params: RunWorkflowParams{
				IOSAppID: iosAppID, IOSBuild: "1.4.2",
				AndroidAppID: androidAppID, AndroidBuild: "5.6.7",
			},
			wantIosApp: iosAppID, wantIosVersion: "1.4.2",
			wantAndroidApp: androidAppID, wantAndroidVers: "5.6.7",
		},
		{
			name: "android pinned, ios app-only (no pin)",
			params: RunWorkflowParams{
				IOSAppID:     iosAppID, // no IOSBuild
				AndroidAppID: androidAppID, AndroidBuild: "5.6.7",
			},
			wantIosApp: iosAppID, wantIosVersion: nil,
			wantAndroidApp: androidAppID, wantAndroidVers: "5.6.7",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/execution/api/execute_workflow_id_async":
					if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
						t.Fatalf("decode request body: %v", err)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"task_id":"task-build-789"}`))
				case "/api/v1/workflows/status/task-build-789":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"status":"completed","total_tests":1,"completed_tests":1,"passed_tests":1,"failed_tests":0}`))
				case "/api/v1/workflows/share/unified-report":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"workflow_task":{"id":"task-build-789","status":"completed"},"workflow_detail":{"id":"workflow-123","name":"Smoke Tests","tests":[]},"test_info":[],"child_tasks":[]}`))
				default:
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
			}))
			defer server.Close()

			t.Setenv("REVYL_BACKEND_URL", server.URL)
			t.Setenv("REVYL_APP_URL", "https://app.example")

			p := tc.params
			p.WorkflowNameOrID = "workflow-123"
			p.Timeout = 5
			p.MonitoringMode = sse.MonitoringModePolling
			if _, err := RunWorkflow(context.Background(), "token", nil, p); err != nil {
				t.Fatalf("RunWorkflow() error = %v", err)
			}

			if override, _ := captured["override_build_config"].(bool); !override {
				t.Fatalf("override_build_config = %v, want true", captured["override_build_config"])
			}
			buildConfig, ok := captured["build_config"].(map[string]interface{})
			if !ok {
				t.Fatalf("build_config missing or wrong type: %v", captured["build_config"])
			}

			iosBuild, ok := buildConfig["ios_build"].(map[string]interface{})
			if !ok {
				t.Fatalf("build_config.ios_build missing or wrong type: %v", buildConfig["ios_build"])
			}
			if got := iosBuild["app_id"]; got != tc.wantIosApp {
				t.Fatalf("ios_build.app_id = %v, want %s", got, tc.wantIosApp)
			}
			if got := iosBuild["pinned_version"]; got != tc.wantIosVersion {
				t.Fatalf("ios_build.pinned_version = %v, want %v", got, tc.wantIosVersion)
			}

			androidBuild, ok := buildConfig["android_build"].(map[string]interface{})
			if !ok {
				t.Fatalf("build_config.android_build missing or wrong type: %v", buildConfig["android_build"])
			}
			if got := androidBuild["app_id"]; got != tc.wantAndroidApp {
				t.Fatalf("android_build.app_id = %v, want %s", got, tc.wantAndroidApp)
			}
			if got := androidBuild["pinned_version"]; got != tc.wantAndroidVers {
				t.Fatalf("android_build.pinned_version = %v, want %v", got, tc.wantAndroidVers)
			}
		})
	}
}
