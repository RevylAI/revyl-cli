// Package main provides tests that verify JSON output structure for CLI commands.
//
// These tests execute command handlers with a mock HTTP server and verify
// that --json output produces valid, well-structured JSON.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

// --- Test helpers ---

// newMockAPIServer creates an httptest.Server that handles all API endpoints
// used by the status/history/report/share commands.
func newMockAPIServer(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	// GET /api/v1/tests/get_test_enhanced_history
	mux.HandleFunc("/api/v1/tests/get_test_enhanced_history", func(w http.ResponseWriter, r *http.Request) {
		testID := r.URL.Query().Get("test_id")
		w.Header().Set("Content-Type", "application/json")

		if testID == "test-no-executions" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"items":           []interface{}{},
				"total_count":     0,
				"requested_count": 1,
				"found_count":     0,
			})
			return
		}

		// Default: return history with 2 items
		successVal := true
		dur := 45.2
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items": []map[string]interface{}{
				{
					"id":             "hist-001",
					"test_uid":       testID,
					"execution_time": "2025-01-15T10:30:00Z",
					"status":         "completed",
					"duration":       dur,
					"has_report":     true,
					"enhanced_task": map[string]interface{}{
						"id":                     "task-001",
						"test_id":                testID,
						"success":                successVal,
						"progress":               100,
						"steps_completed":        5,
						"total_steps":            5,
						"status":                 "completed",
						"started_at":             "2025-01-15T10:30:00Z",
						"completed_at":           "2025-01-15T10:30:45Z",
						"execution_time_seconds": dur,
					},
				},
				{
					"id":             "hist-002",
					"test_uid":       testID,
					"execution_time": "2025-01-14T09:00:00Z",
					"status":         "failed",
					"duration":       30.0,
					"has_report":     true,
					"enhanced_task": map[string]interface{}{
						"id":                     "task-002",
						"test_id":                testID,
						"success":                false,
						"progress":               60,
						"steps_completed":        3,
						"total_steps":            5,
						"status":                 "failed",
						"started_at":             "2025-01-14T09:00:00Z",
						"completed_at":           "2025-01-14T09:00:30Z",
						"execution_time_seconds": 30.0,
					},
				},
			},
			"total_count":     2,
			"requested_count": 10,
			"found_count":     2,
		})
	})

	// GET /api/v1/reports-v3/reports/by-execution/{id}
	mux.HandleFunc("/api/v1/reports-v3/reports/by-execution/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		successVal := true
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":                 "report-001",
			"execution_id":       "task-001",
			"test_id":            "test-uuid-001",
			"test_name":          "Login Flow",
			"platform":           "android",
			"success":            successVal,
			"started_at":         "2025-01-15T10:30:00Z",
			"completed_at":       "2025-01-15T10:30:45Z",
			"total_steps":        3,
			"passed_steps":       3,
			"failed_steps":       0,
			"total_validations":  2,
			"validations_passed": 2,
			"app_name":           "TestApp",
			"build_version":      "1.0.0",
			"device_model":       "Pixel 7",
			"os_version":         "Android 14",
			"steps": []map[string]interface{}{
				{
					"id":               "step-1",
					"execution_order":  1,
					"step_type":        "instruction",
					"step_description": "Tap the login button",
					"status":           "passed",
				},
				{
					"id":               "step-2",
					"execution_order":  2,
					"step_type":        "instruction",
					"step_description": "Enter username",
					"status":           "passed",
				},
				{
					"id":               "step-3",
					"execution_order":  3,
					"step_type":        "validation",
					"step_description": "Verify dashboard is visible",
					"status":           "passed",
				},
			},
		})
	})

	// POST /api/v1/reports/generate_shareable_report_link_by_task
	mux.HandleFunc("/api/v1/reports/generate_shareable_report_link_by_task", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		taskID, _ := body["task_id"].(string)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"shareable_link": fmt.Sprintf("https://app.revyl.ai/shared/report/%s", taskID),
		})
	})

	// GET /api/v1/workflows/status/history/{id}
	mux.HandleFunc("/api/v1/workflows/status/history/", func(w http.ResponseWriter, r *http.Request) {
		// Extract workflow ID from path
		parts := strings.Split(r.URL.Path, "/")
		workflowID := parts[len(parts)-1]

		w.Header().Set("Content-Type", "application/json")

		if workflowID == "wf-no-executions" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"workflow_id":  workflowID,
				"executions":   []interface{}{},
				"total_count":  0,
				"success_rate": 0,
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"workflow_id": workflowID,
			"executions": []map[string]interface{}{
				{
					"execution_id":    "wf-exec-001",
					"workflow_id":     workflowID,
					"status":          "completed",
					"progress":        100,
					"completed_tests": 3,
					"total_tests":     3,
					"passed_tests":    3,
					"failed_tests":    0,
					"started_at":      "2025-01-15T10:00:00Z",
					"duration":        "2m 30s",
				},
				{
					"execution_id":    "wf-exec-002",
					"workflow_id":     workflowID,
					"status":          "failed",
					"progress":        100,
					"completed_tests": 3,
					"total_tests":     3,
					"passed_tests":    2,
					"failed_tests":    1,
					"started_at":      "2025-01-14T09:00:00Z",
					"duration":        "3m 10s",
				},
			},
			"total_count":      2,
			"success_rate":     50.0,
			"average_duration": 170.0,
		})
	})

	// GET /api/v1/workflows/status/status/{id}
	mux.HandleFunc("/api/v1/workflows/status/status/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"execution_id":    "wf-exec-001",
			"workflow_id":     "wf-uuid-001",
			"status":          "completed",
			"progress":        100,
			"completed_tests": 3,
			"total_tests":     3,
			"passed_tests":    3,
			"failed_tests":    0,
			"started_at":      "2025-01-15T10:00:00Z",
			"duration":        "2m 30s",
		})
	})

	// POST /api/v1/workflows/share/unified-report
	mux.HandleFunc("/api/v1/workflows/share/unified-report", func(w http.ResponseWriter, r *http.Request) {
		successVal := true
		totalTests := 2
		completedTests := 2
		dur := 150.0
		stepsA := 5
		totalA := 5
		stepsB := 3
		totalB := 3
		durA := 45.0
		durB := 30.0

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"workflow_task": map[string]interface{}{
				"task_id":         "wf-exec-001",
				"workflow_id":     "wf-uuid-001",
				"status":          "completed",
				"success":         successVal,
				"duration":        dur,
				"total_tests":     totalTests,
				"completed_tests": completedTests,
				"task_ids":        []string{"child-task-001", "child-task-002"},
				"started_at":      "2025-01-15T10:00:00Z",
			},
			"workflow_detail": map[string]interface{}{
				"id":    "wf-uuid-001",
				"name":  "Smoke Tests",
				"tests": []string{"test-uuid-001", "test-uuid-002"},
			},
			"test_info": []map[string]interface{}{
				{"id": "test-uuid-001", "name": "Login Flow", "platform": "android"},
				{"id": "test-uuid-002", "name": "Checkout", "platform": "ios"},
			},
			"child_tasks": []map[string]interface{}{
				{
					"task_id":                "child-task-001",
					"test_id":                "test-uuid-001",
					"test_name":              "Login Flow",
					"platform":               "android",
					"status":                 "completed",
					"success":                successVal,
					"started_at":             "2025-01-15T10:00:00Z",
					"completed_at":           "2025-01-15T10:00:45Z",
					"execution_time_seconds": durA,
					"steps_completed":        stepsA,
					"total_steps":            totalA,
				},
				{
					"task_id":                "child-task-002",
					"test_id":                "test-uuid-002",
					"test_name":              "Checkout",
					"platform":               "ios",
					"status":                 "completed",
					"success":                successVal,
					"started_at":             "2025-01-15T10:01:00Z",
					"completed_at":           "2025-01-15T10:01:30Z",
					"execution_time_seconds": durB,
					"steps_completed":        stepsB,
					"total_steps":            totalB,
				},
			},
		})
	})

	return httptest.NewServer(mux)
}

// withMockClient overrides loadConfigAndClient so that command handlers use
// the mock HTTP server. Restores the original function via t.Cleanup.
func withMockClient(t *testing.T, server *httptest.Server) {
	t.Helper()
	original := loadConfigAndClient
	t.Cleanup(func() { loadConfigAndClient = original })

	loadConfigAndClient = func(devMode bool) (string, *config.ProjectConfig, *api.Client, error) {
		client := api.NewClientWithBaseURL("test-key", server.URL)
		cfg := &config.ProjectConfig{
			Tests: map[string]string{
				"login-flow": "test-uuid-001",
				"empty-test": "test-no-executions",
			},
			Workflows: map[string]string{
				"smoke-tests":    "wf-uuid-001",
				"empty-workflow": "wf-no-executions",
			},
		}
		return "test-key", cfg, client, nil
	}
}

// captureStdout redirects os.Stdout, runs fn, and returns whatever was printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading pipe: %v", err)
	}
	return string(out)
}

// newTestCommand creates a minimal cobra root command with the persistent flags
// that command handlers read via cmd.Root().PersistentFlags().
// The returned command (and any children) will have a background context set.
func newTestCommand() *cobra.Command {
	root := &cobra.Command{Use: "revyl"}
	root.PersistentFlags().Bool("json", false, "")
	root.PersistentFlags().Bool("dev", false, "")
	root.PersistentFlags().Bool("debug", false, "")
	root.PersistentFlags().Bool("quiet", false, "")
	root.SetContext(context.Background())
	return root
}

// newLeafCommand creates a child command attached to a root with context set.
func newLeafCommand(use string, runE func(cmd *cobra.Command, args []string) error) *cobra.Command {
	root := newTestCommand()
	leaf := &cobra.Command{Use: use, RunE: runE}
	root.AddCommand(leaf)
	leaf.SetContext(context.Background())
	return leaf
}

// parseJSON unmarshals output into a map and fails the test if invalid.
func parseJSON(t *testing.T, output string) map[string]interface{} {
	t.Helper()
	// Trim any non-JSON prefix (e.g. spinner artifacts)
	trimmed := strings.TrimSpace(output)
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		t.Fatalf("invalid JSON output:\n%s\nerror: %v", trimmed, err)
	}
	return m
}

// parseJSONArray unmarshals output into a slice and fails the test if invalid.
func parseJSONArray(t *testing.T, output string) []interface{} {
	t.Helper()
	trimmed := strings.TrimSpace(output)
	var arr []interface{}
	if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
		t.Fatalf("invalid JSON array output:\n%s\nerror: %v", trimmed, err)
	}
	return arr
}

// assertJSONKey asserts that the given key exists in the map.
func assertJSONKey(t *testing.T, m map[string]interface{}, key string) {
	t.Helper()
	if _, ok := m[key]; !ok {
		t.Errorf("expected key %q in JSON output, got keys: %v", key, mapKeys(m))
	}
}

// assertJSONString asserts that m[key] equals expected.
func assertJSONString(t *testing.T, m map[string]interface{}, key, expected string) {
	t.Helper()
	val, ok := m[key]
	if !ok {
		t.Errorf("expected key %q in JSON output", key)
		return
	}
	str, ok := val.(string)
	if !ok {
		t.Errorf("expected key %q to be a string, got %T", key, val)
		return
	}
	if str != expected {
		t.Errorf("expected %q=%q, got %q", key, expected, str)
	}
}

// assertJSONArrayLen asserts that m[key] is an array with at least minLen items.
func assertJSONArrayLen(t *testing.T, m map[string]interface{}, key string, minLen int) {
	t.Helper()
	val, ok := m[key]
	if !ok {
		t.Errorf("expected key %q in JSON output", key)
		return
	}
	arr, ok := val.([]interface{})
	if !ok {
		t.Errorf("expected key %q to be an array, got %T", key, val)
		return
	}
	if len(arr) < minLen {
		t.Errorf("expected %q to have at least %d items, got %d", key, minLen, len(arr))
	}
}

func mapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// --- Test Status Commands ---

func TestTestStatusJSON(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withMockClient(t, server)

	oldVal := statusOutputJSON
	statusOutputJSON = true
	defer func() { statusOutputJSON = oldVal }()

	leaf := newLeafCommand("status", runTestStatus)

	output := captureStdout(t, func() {
		if err := leaf.RunE(leaf, []string{"login-flow"}); err != nil {
			t.Fatalf("runTestStatus: %v", err)
		}
	})

	result := parseJSON(t, output)
	assertJSONString(t, result, "test_id", "test-uuid-001")
	assertJSONString(t, result, "task_id", "task-001")
	assertJSONString(t, result, "status", "completed")
	assertJSONKey(t, result, "duration")
	assertJSONKey(t, result, "report_url")
	assertJSONKey(t, result, "has_report")
	assertJSONKey(t, result, "success")
}

func TestTestStatusNoExecutions(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withMockClient(t, server)

	oldVal := statusOutputJSON
	statusOutputJSON = true
	defer func() { statusOutputJSON = oldVal }()

	leaf := newLeafCommand("status", runTestStatus)

	output := captureStdout(t, func() {
		if err := leaf.RunE(leaf, []string{"empty-test"}); err != nil {
			t.Fatalf("runTestStatus: %v", err)
		}
	})

	result := parseJSON(t, output)
	assertJSONString(t, result, "status", "no_executions")
	assertJSONKey(t, result, "message")
	assertJSONString(t, result, "test_id", "test-no-executions")
}

// --- Test History Commands ---

func TestTestHistoryJSON(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withMockClient(t, server)

	oldJSON := historyOutputJSON
	oldLimit := historyLimit
	historyOutputJSON = true
	historyLimit = 10
	defer func() {
		historyOutputJSON = oldJSON
		historyLimit = oldLimit
	}()

	leaf := newLeafCommand("history", runTestHistory)

	output := captureStdout(t, func() {
		if err := leaf.RunE(leaf, []string{"login-flow"}); err != nil {
			t.Fatalf("runTestHistory: %v", err)
		}
	})

	result := parseJSON(t, output)
	assertJSONString(t, result, "test_id", "test-uuid-001")
	assertJSONString(t, result, "test_name", "login-flow")
	assertJSONArrayLen(t, result, "items", 2)

	// Verify total_count and shown_count
	assertJSONKey(t, result, "total_count")
	assertJSONKey(t, result, "shown_count")

	// Verify first item has expected fields
	items := result["items"].([]interface{})
	first := items[0].(map[string]interface{})
	assertJSONKey(t, first, "id")
	assertJSONKey(t, first, "status")
	assertJSONKey(t, first, "task_id")
	assertJSONKey(t, first, "execution_time")
}

// --- Test Report Commands ---

func TestTestReportJSON(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withMockClient(t, server)

	oldJSON := reportOutputJSON
	oldNoSteps := reportNoSteps
	reportOutputJSON = true
	reportNoSteps = false
	defer func() {
		reportOutputJSON = oldJSON
		reportNoSteps = oldNoSteps
	}()

	leaf := newLeafCommand("report", runTestReport)

	output := captureStdout(t, func() {
		if err := leaf.RunE(leaf, []string{"login-flow"}); err != nil {
			t.Fatalf("runTestReport: %v", err)
		}
	})

	result := parseJSON(t, output)
	assertJSONKey(t, result, "test_name")
	assertJSONString(t, result, "platform", "Android")
	assertJSONArrayLen(t, result, "steps", 3)
	assertJSONKey(t, result, "total_validations")
	assertJSONKey(t, result, "validations_passed")
	assertJSONKey(t, result, "report_url")
	assertJSONKey(t, result, "total_steps")
	assertJSONKey(t, result, "passed_steps")

	// Verify step structure
	steps := result["steps"].([]interface{})
	firstStep := steps[0].(map[string]interface{})
	assertJSONKey(t, firstStep, "order")
	assertJSONKey(t, firstStep, "type")
	assertJSONKey(t, firstStep, "description")
	assertJSONKey(t, firstStep, "status")
}

func TestTestReportNoSteps(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withMockClient(t, server)

	oldJSON := reportOutputJSON
	oldNoSteps := reportNoSteps
	reportOutputJSON = true
	reportNoSteps = true
	defer func() {
		reportOutputJSON = oldJSON
		reportNoSteps = oldNoSteps
	}()

	leaf := newLeafCommand("report", runTestReport)

	output := captureStdout(t, func() {
		if err := leaf.RunE(leaf, []string{"login-flow"}); err != nil {
			t.Fatalf("runTestReport: %v", err)
		}
	})

	result := parseJSON(t, output)
	// Summary fields still present
	assertJSONKey(t, result, "test_name")
	assertJSONKey(t, result, "total_steps")
	assertJSONKey(t, result, "passed_steps")
	assertJSONKey(t, result, "report_url")

	// Steps key should be absent when --no-steps is set
	if _, ok := result["steps"]; ok {
		t.Error("expected 'steps' key to be absent with --no-steps flag")
	}
}

// --- Test Share Commands ---

func TestTestShareJSON(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withMockClient(t, server)

	oldVal := shareOutputJSON
	shareOutputJSON = true
	defer func() { shareOutputJSON = oldVal }()

	leaf := newLeafCommand("share", runTestShare)

	output := captureStdout(t, func() {
		if err := leaf.RunE(leaf, []string{"login-flow"}); err != nil {
			t.Fatalf("runTestShare: %v", err)
		}
	})

	result := parseJSON(t, output)
	assertJSONKey(t, result, "task_id")
	assertJSONKey(t, result, "shareable_link")

	link := result["shareable_link"].(string)
	if !strings.Contains(link, "task-001") {
		t.Errorf("expected shareable_link to contain task ID, got: %s", link)
	}
}

// --- Workflow Status Commands ---

func TestWorkflowStatusJSON(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withMockClient(t, server)

	oldVal := wfStatusOutputJSON
	wfStatusOutputJSON = true
	defer func() { wfStatusOutputJSON = oldVal }()

	leaf := newLeafCommand("status", runWorkflowStatus)

	output := captureStdout(t, func() {
		if err := leaf.RunE(leaf, []string{"smoke-tests"}); err != nil {
			t.Fatalf("runWorkflowStatus: %v", err)
		}
	})

	result := parseJSON(t, output)
	assertJSONString(t, result, "workflow_name", "smoke-tests")
	assertJSONString(t, result, "workflow_id", "wf-uuid-001")
	assertJSONString(t, result, "status", "completed")
	assertJSONKey(t, result, "completed_tests")
	assertJSONKey(t, result, "total_tests")
	assertJSONKey(t, result, "passed_tests")
	assertJSONKey(t, result, "failed_tests")
	assertJSONKey(t, result, "report_url")
}

func TestWorkflowStatusNoExecutions(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withMockClient(t, server)

	oldVal := wfStatusOutputJSON
	wfStatusOutputJSON = true
	defer func() { wfStatusOutputJSON = oldVal }()

	leaf := newLeafCommand("status", runWorkflowStatus)

	output := captureStdout(t, func() {
		if err := leaf.RunE(leaf, []string{"empty-workflow"}); err != nil {
			t.Fatalf("runWorkflowStatus: %v", err)
		}
	})

	result := parseJSON(t, output)
	assertJSONString(t, result, "status", "no_executions")
	assertJSONKey(t, result, "message")
	assertJSONString(t, result, "workflow_id", "wf-no-executions")
}

// --- Workflow History Commands ---

func TestWorkflowHistoryJSON(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withMockClient(t, server)

	oldJSON := wfHistoryOutputJSON
	oldLimit := wfHistoryLimit
	wfHistoryOutputJSON = true
	wfHistoryLimit = 10
	defer func() {
		wfHistoryOutputJSON = oldJSON
		wfHistoryLimit = oldLimit
	}()

	leaf := newLeafCommand("history", runWorkflowHistory)

	output := captureStdout(t, func() {
		if err := leaf.RunE(leaf, []string{"smoke-tests"}); err != nil {
			t.Fatalf("runWorkflowHistory: %v", err)
		}
	})

	result := parseJSON(t, output)
	assertJSONString(t, result, "workflow_name", "smoke-tests")
	assertJSONString(t, result, "workflow_id", "wf-uuid-001")
	assertJSONKey(t, result, "total_count")
	assertJSONKey(t, result, "success_rate")
	assertJSONArrayLen(t, result, "executions", 2)

	// Verify first execution has expected fields
	execs := result["executions"].([]interface{})
	first := execs[0].(map[string]interface{})
	assertJSONKey(t, first, "status")
	assertJSONKey(t, first, "completed_tests")
	assertJSONKey(t, first, "total_tests")
}

// --- Workflow Report Commands ---

func TestWorkflowReportJSON(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withMockClient(t, server)

	oldVal := wfReportOutputJSON
	wfReportOutputJSON = true
	defer func() { wfReportOutputJSON = oldVal }()

	leaf := newLeafCommand("report", runWorkflowReport)

	output := captureStdout(t, func() {
		if err := leaf.RunE(leaf, []string{"smoke-tests"}); err != nil {
			t.Fatalf("runWorkflowReport: %v", err)
		}
	})

	result := parseJSON(t, output)
	assertJSONString(t, result, "status", "completed")
	assertJSONKey(t, result, "workflow_id")
	assertJSONKey(t, result, "report_url")
	assertJSONArrayLen(t, result, "tests", 2)

	// Verify child task structure
	tests := result["tests"].([]interface{})
	firstTest := tests[0].(map[string]interface{})
	assertJSONKey(t, firstTest, "task_id")
	assertJSONKey(t, firstTest, "test_name")
	assertJSONKey(t, firstTest, "platform")
	assertJSONKey(t, firstTest, "status")
}

// --- Workflow Share Commands ---

func TestWorkflowShareJSON(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withMockClient(t, server)

	oldVal := wfShareOutputJSON
	wfShareOutputJSON = true
	defer func() { wfShareOutputJSON = oldVal }()

	leaf := newLeafCommand("share", runWorkflowShare)

	output := captureStdout(t, func() {
		if err := leaf.RunE(leaf, []string{"smoke-tests"}); err != nil {
			t.Fatalf("runWorkflowShare: %v", err)
		}
	})

	result := parseJSON(t, output)
	assertJSONKey(t, result, "task_id")
	assertJSONKey(t, result, "report_url")

	reportURL := result["report_url"].(string)
	if !strings.Contains(reportURL, "workflows/report") {
		t.Errorf("expected report_url to contain 'workflows/report', got: %s", reportURL)
	}
}
