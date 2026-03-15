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

const (
	uuidLikeTestID         = "11111111-1111-1111-1111-111111111111"
	uuidLikeTestTaskID     = "task-from-test-uuid"
	routeMissingExecution  = "22222222-2222-2222-2222-222222222222"
	legacyMissingExecution = "33333333-3333-3333-3333-333333333333"
	serverErrorExecution   = "44444444-4444-4444-4444-444444444444"
	uuidLikeWorkflowTaskID = "55555555-5555-5555-5555-555555555555"
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
		if testID == routeMissingExecution || testID == legacyMissingExecution || testID == serverErrorExecution {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"items":           []interface{}{},
				"total_count":     0,
				"requested_count": 1,
				"found_count":     0,
			})
			return
		}
		if testID == uuidLikeTestID {
			successVal := true
			dur := 45.2
			json.NewEncoder(w).Encode(map[string]interface{}{
				"items": []map[string]interface{}{
					{
						"id":             "hist-uuid-test",
						"test_uid":       testID,
						"execution_time": "2025-01-15T10:30:00Z",
						"status":         "completed",
						"duration":       dur,
						"has_report":     true,
						"enhanced_task": map[string]interface{}{
							"id":                     uuidLikeTestTaskID,
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
				},
				"total_count":     1,
				"requested_count": 1,
				"found_count":     1,
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

	// GET /api/v1/reports-v3/reports/by-execution/{id}/context
	mux.HandleFunc("/api/v1/reports-v3/reports/by-execution/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		executionID := ""
		if len(parts) >= 7 {
			executionID = parts[5]
		}
		switch executionID {
		case uuidLikeTestID, routeMissingExecution:
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"detail": "Not Found",
			})
			return
		case legacyMissingExecution:
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"detail": map[string]interface{}{
					"message":      "Report not found in V3 database",
					"use_legacy":   true,
					"execution_id": executionID,
				},
			})
			return
		case serverErrorExecution:
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"detail": "Error fetching report: device metadata invalid",
			})
			return
		}
		successVal := true
		includeSteps := r.URL.Query().Get("include_steps") != "false"
		includeActions := r.URL.Query().Get("include_actions") != "false"

		steps := []map[string]interface{}{
			{
				"id":               "step-1",
				"execution_order":  1,
				"step_type":        "instruction",
				"step_description": "Tap the login button",
				"status":           "success",
				"effective_status": "passed",
			},
			{
				"id":               "step-2",
				"execution_order":  2,
				"step_type":        "instruction",
				"step_description": "Enter username",
				"status":           "success",
				"effective_status": "passed",
				"actions": []map[string]interface{}{
					{
						"id":                          "action-2-1",
						"action_index":                1,
						"action_type":                 "input",
						"agent_description":           "Type the username",
						"screenshot_before_url":       "https://cdn.example/task-001/before.png?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Signature=before-signature",
						"screenshot_before_clean_url": "https://cdn.example/task-001/before-clean.png?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Signature=before-clean-signature",
						"screenshot_after_url":        "https://cdn.example/task-001/after.png?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Signature=after-signature",
						"type_data": map[string]interface{}{
							"target": "username field",
						},
					},
				},
			},
			{
				"id":                      "step-3",
				"execution_order":         3,
				"step_type":               "validation",
				"step_description":        "Verify dashboard is visible",
				"status":                  "success",
				"effective_status":        "failed",
				"effective_status_reason": "Expected dashboard header was not visible.",
				"validation_result":       false,
				"validation_reasoning":    "Expected dashboard header was not visible.",
				"type_data": map[string]interface{}{
					"validation_result":    false,
					"validation_reasoning": "Expected dashboard header was not visible.",
				},
			},
		}
		if !includeActions {
			steps[1]["actions"] = []map[string]interface{}{}
		}

		response := map[string]interface{}{
			"id":                      "report-001",
			"report_url":              "https://app.revyl.ai/tests/report?taskId=task-001",
			"execution_id":            "task-001",
			"test_id":                 "test-uuid-001",
			"test_name":               "Login Flow",
			"platform":                "android",
			"success":                 successVal,
			"started_at":              "2025-01-15T10:30:00Z",
			"completed_at":            "2025-01-15T10:30:45Z",
			"total_steps":             3,
			"passed_steps":            3,
			"warning_steps":           0,
			"failed_steps":            0,
			"total_validations":       2,
			"validations_passed":      1,
			"effective_passed_steps":  2,
			"effective_warning_steps": 0,
			"effective_failed_steps":  1,
			"effective_running_steps": 0,
			"effective_pending_steps": 0,
			"app_name":                "TestApp",
			"build_version":           "1.0.0",
			"device_model":            "Pixel 7",
			"os_version":              "Android 14",
			"tldr": map[string]interface{}{
				"test_case": "Verify the dashboard loads after login.",
				"key_moments": []map[string]interface{}{
					{
						"step_reference": "[[step:3]]",
						"description":    "Dashboard validation failed after login.",
						"importance":     "high",
					},
				},
				"insights": []string{
					"Dashboard header was not visible after the login flow completed.",
				},
			},
			"device_metadata": map[string]interface{}{
				"width":       1080,
				"height":      1920,
				"platform":    "android",
				"runtime":     "Android 14",
				"device_type": "Pixel 7",
			},
			"video_url": "https://videos.example/task-001.mp4?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Signature=video-signature",
		}
		if executionID == uuidLikeTestTaskID {
			response["execution_id"] = uuidLikeTestTaskID
			response["report_url"] = "https://app.revyl.ai/tests/report?taskId=" + uuidLikeTestTaskID
			response["video_url"] = "https://videos.example/task-from-test-uuid.mp4?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Signature=uuid-video-signature"
		}
		if includeSteps {
			response["steps"] = steps
		}

		json.NewEncoder(w).Encode(response)
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
		executionID := strings.TrimPrefix(r.URL.Path, "/api/v1/workflows/status/status/")
		if executionID == "" {
			executionID = "wf-exec-001"
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"execution_id":    executionID,
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

func captureStdoutAndStderr(t *testing.T, fn func()) string {
	t.Helper()

	origOut, origErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe (stdout): %v", err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe (stderr): %v", err)
	}
	os.Stdout = wOut
	os.Stderr = wErr

	fn()

	wOut.Close()
	wErr.Close()
	os.Stdout = origOut
	os.Stderr = origErr

	outBytes, err := io.ReadAll(rOut)
	if err != nil {
		t.Fatalf("reading stdout pipe: %v", err)
	}
	errBytes, err := io.ReadAll(rErr)
	if err != nil {
		t.Fatalf("reading stderr pipe: %v", err)
	}
	return string(outBytes) + string(errBytes)
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

func assertOutputPreservesURLQuerySeparators(t *testing.T, output string) {
	t.Helper()
	if strings.Contains(output, "\\u0026") {
		t.Fatalf("expected JSON output to preserve literal '&' in URLs, got:\n%s", output)
	}
	if !strings.Contains(output, "X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Signature=") {
		t.Fatalf("expected presigned URLs with literal '&' separators, got:\n%s", output)
	}
}

// assertJSONKey asserts that the given key exists in the map.
func assertJSONKey(t *testing.T, m map[string]interface{}, key string) {
	t.Helper()
	if _, ok := m[key]; !ok {
		t.Errorf("expected key %q in JSON output, got keys: %v", key, mapKeys(m))
	}
}

// assertJSONNoKey asserts that the given key does not exist in the map.
func assertJSONNoKey(t *testing.T, m map[string]interface{}, key string) {
	t.Helper()
	if _, ok := m[key]; ok {
		t.Errorf("expected key %q to be absent in JSON output, got keys: %v", key, mapKeys(m))
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
	oldShare := reportShare
	reportOutputJSON = true
	reportNoSteps = false
	defer func() {
		reportOutputJSON = oldJSON
		reportNoSteps = oldNoSteps
		reportShare = oldShare
	}()

	leaf := newLeafCommand("report", runTestReport)

	output := captureStdout(t, func() {
		if err := leaf.RunE(leaf, []string{"login-flow"}); err != nil {
			t.Fatalf("runTestReport: %v", err)
		}
	})

	if !strings.Contains(output, "\n  \"test_name\"") {
		t.Fatalf("expected pretty-printed JSON output, got:\n%s", output)
	}
	assertOutputPreservesURLQuerySeparators(t, output)

	result := parseJSON(t, output)
	assertJSONKey(t, result, "test_name")
	assertJSONString(t, result, "platform", "android")
	assertJSONArrayLen(t, result, "steps", 3)
	assertJSONKey(t, result, "total_validations")
	assertJSONKey(t, result, "validations_passed")
	assertJSONKey(t, result, "report_url")
	assertJSONKey(t, result, "total_steps")
	assertJSONKey(t, result, "passed_steps")
	assertJSONKey(t, result, "effective_failed_steps")
	assertJSONKey(t, result, "video_url")
	assertJSONKey(t, result, "tldr")
	assertJSONKey(t, result, "device_metadata")
	assertJSONNoKey(t, result, "expected_states")
	assertJSONNoKey(t, result, "run_config")
	assertJSONNoKey(t, result, "s3_bucket")
	assertJSONNoKey(t, result, "video_s3_key")
	assertJSONNoKey(t, result, "device_logs_s3_key")

	// Verify step structure
	steps := result["steps"].([]interface{})
	firstStep := steps[0].(map[string]interface{})
	assertJSONKey(t, firstStep, "execution_order")
	assertJSONKey(t, firstStep, "step_type")
	assertJSONKey(t, firstStep, "step_description")
	assertJSONKey(t, firstStep, "status")

	secondStep := steps[1].(map[string]interface{})
	actions := secondStep["actions"].([]interface{})
	firstAction := actions[0].(map[string]interface{})
	assertJSONKey(t, firstAction, "screenshot_before_url")
	assertJSONKey(t, firstAction, "screenshot_before_clean_url")
	assertJSONKey(t, firstAction, "screenshot_after_url")
	assertJSONNoKey(t, firstAction, "screenshot_before_s3_key")
	assertJSONNoKey(t, firstAction, "screenshot_before_clean_s3_key")
	assertJSONNoKey(t, firstAction, "screenshot_after_s3_key")
	actionTypeData := firstAction["type_data"].(map[string]interface{})
	assertJSONNoKey(t, actionTypeData, "grounding_crop_s3_key")

	validationStep := steps[2].(map[string]interface{})
	assertJSONKey(t, validationStep, "effective_status")
	assertJSONKey(t, validationStep, "validation_result")
	assertJSONKey(t, validationStep, "validation_reasoning")
}

func TestTestReportJSONShareSanitizesInternalS3Keys(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withMockClient(t, server)

	oldJSON := reportOutputJSON
	oldNoSteps := reportNoSteps
	oldShare := reportShare
	reportOutputJSON = true
	reportNoSteps = false
	reportShare = true
	defer func() {
		reportOutputJSON = oldJSON
		reportNoSteps = oldNoSteps
		reportShare = oldShare
	}()

	leaf := newLeafCommand("report", runTestReport)

	output := captureStdout(t, func() {
		if err := leaf.RunE(leaf, []string{"login-flow"}); err != nil {
			t.Fatalf("runTestReport: %v", err)
		}
	})

	assertOutputPreservesURLQuerySeparators(t, output)

	result := parseJSON(t, output)
	assertJSONKey(t, result, "shareable_link")
	assertJSONKey(t, result, "video_url")
	assertJSONNoKey(t, result, "expected_states")
	assertJSONNoKey(t, result, "run_config")
	assertJSONNoKey(t, result, "s3_bucket")
	assertJSONNoKey(t, result, "video_s3_key")
	assertJSONNoKey(t, result, "device_logs_s3_key")

	steps := result["steps"].([]interface{})
	secondStep := steps[1].(map[string]interface{})
	actions := secondStep["actions"].([]interface{})
	firstAction := actions[0].(map[string]interface{})
	assertJSONKey(t, firstAction, "screenshot_before_url")
	assertJSONKey(t, firstAction, "screenshot_before_clean_url")
	assertJSONKey(t, firstAction, "screenshot_after_url")
	assertJSONNoKey(t, firstAction, "screenshot_before_s3_key")
	assertJSONNoKey(t, firstAction, "screenshot_before_clean_s3_key")
	assertJSONNoKey(t, firstAction, "screenshot_after_s3_key")
	actionTypeData := firstAction["type_data"].(map[string]interface{})
	assertJSONNoKey(t, actionTypeData, "grounding_crop_s3_key")
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
	assertJSONKey(t, result, "effective_failed_steps")
	assertJSONKey(t, result, "report_url")

	// Steps key should be absent when --no-steps is set
	if _, ok := result["steps"]; ok {
		t.Error("expected 'steps' key to be absent with --no-steps flag")
	}
}

func TestTestReportHumanReadableUsesEffectiveStatus(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withMockClient(t, server)

	oldJSON := reportOutputJSON
	oldNoSteps := reportNoSteps
	reportOutputJSON = false
	reportNoSteps = false
	defer func() {
		reportOutputJSON = oldJSON
		reportNoSteps = oldNoSteps
	}()

	leaf := newLeafCommand("report", runTestReport)

	output := captureStdoutAndStderr(t, func() {
		if err := leaf.RunE(leaf, []string{"login-flow"}); err != nil {
			t.Fatalf("runTestReport: %v", err)
		}
	})

	for _, expected := range []string{
		"Login Flow",
		"2/3 passed, 1 failed",
		"1/2 passed",
		"Verify dashboard is visible",
		"Expected dashboard header was not visible.",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output missing %q\noutput:\n%s", expected, output)
		}
	}
}

func TestTestReportUUIDLikeTestIDUsesHistoryWithoutContextPreflight(t *testing.T) {
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
		if err := leaf.RunE(leaf, []string{uuidLikeTestID}); err != nil {
			t.Fatalf("runTestReport: %v", err)
		}
	})

	result := parseJSON(t, output)
	assertJSONString(t, result, "execution_id", uuidLikeTestTaskID)
}

func TestTestReportContextRouteMissingShowsBackendMessage(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withMockClient(t, server)

	oldJSON := reportOutputJSON
	oldNoSteps := reportNoSteps
	reportOutputJSON = false
	reportNoSteps = false
	defer func() {
		reportOutputJSON = oldJSON
		reportNoSteps = oldNoSteps
	}()

	leaf := newLeafCommand("report", runTestReport)

	output := captureStdoutAndStderr(t, func() {
		if err := leaf.RunE(leaf, []string{routeMissingExecution}); err != nil {
			t.Fatalf("runTestReport: %v", err)
		}
	})

	if !strings.Contains(output, "does not expose the reports-v3 context endpoint yet") {
		t.Fatalf("expected backend context endpoint message, got:\n%s", output)
	}
	if strings.Contains(output, "not a valid execution ID") {
		t.Fatalf("expected route-missing error, not invalid-ID output:\n%s", output)
	}
}

func TestTestReportLegacyContextMissingShowsV3Message(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withMockClient(t, server)

	oldJSON := reportOutputJSON
	oldNoSteps := reportNoSteps
	reportOutputJSON = false
	reportNoSteps = false
	defer func() {
		reportOutputJSON = oldJSON
		reportNoSteps = oldNoSteps
	}()

	leaf := newLeafCommand("report", runTestReport)

	output := captureStdoutAndStderr(t, func() {
		if err := leaf.RunE(leaf, []string{legacyMissingExecution}); err != nil {
			t.Fatalf("runTestReport: %v", err)
		}
	})

	if !strings.Contains(output, "does not have a reports-v3 context report") {
		t.Fatalf("expected reports-v3-missing message, got:\n%s", output)
	}
	if !strings.Contains(output, "Report not found in V3 database") {
		t.Fatalf("expected structured backend detail, got:\n%s", output)
	}
	if strings.Contains(output, "not a valid execution ID") {
		t.Fatalf("expected V3-specific error, not invalid-ID output:\n%s", output)
	}
}

func TestTestReportContextServerErrorShowsAPIError(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withMockClient(t, server)

	oldJSON := reportOutputJSON
	oldNoSteps := reportNoSteps
	reportOutputJSON = false
	reportNoSteps = false
	defer func() {
		reportOutputJSON = oldJSON
		reportNoSteps = oldNoSteps
	}()

	leaf := newLeafCommand("report", runTestReport)

	output := captureStdoutAndStderr(t, func() {
		if err := leaf.RunE(leaf, []string{serverErrorExecution}); err != nil {
			t.Fatalf("runTestReport: %v", err)
		}
	})

	if !strings.Contains(output, "Report API returned an error (HTTP 500)") {
		t.Fatalf("expected server error message, got:\n%s", output)
	}
	if !strings.Contains(output, "device metadata invalid") {
		t.Fatalf("expected server error detail, got:\n%s", output)
	}
	if strings.Contains(output, "not a valid execution ID") {
		t.Fatalf("expected context server error, not invalid-ID output:\n%s", output)
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

func TestWorkflowStatusJSONByExecutionID(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withMockClient(t, server)

	oldVal := wfStatusOutputJSON
	wfStatusOutputJSON = true
	defer func() { wfStatusOutputJSON = oldVal }()

	leaf := newLeafCommand("status", runWorkflowStatus)

	output := captureStdout(t, func() {
		if err := leaf.RunE(leaf, []string{uuidLikeWorkflowTaskID}); err != nil {
			t.Fatalf("runWorkflowStatus: %v", err)
		}
	})

	result := parseJSON(t, output)
	assertJSONString(t, result, "execution_id", uuidLikeWorkflowTaskID)
	assertJSONString(t, result, "workflow_id", "wf-uuid-001")
	assertJSONString(t, result, "status", "completed")
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
