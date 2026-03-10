package execution

import (
	"context"
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
		case "/api/v1/workflows/status/status/task-workflow-456":
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
