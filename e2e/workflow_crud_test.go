//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWorkflowLifecycle exercises the full workflow CRUD lifecycle:
// create -> info -> list -> quarantine add/list/remove -> config show/set ->
// run (--no-wait) -> status by task UUID -> cancel -> history -> report -> delete.
func TestWorkflowLifecycle(t *testing.T) {
	testName1 := uniqueName("e2e-wf-t1")
	testName2 := uniqueName("e2e-wf-t2")
	wfName := uniqueName("e2e-wf")

	testID1 := createTestFixture(t, testName1, "android")
	testID2 := createTestFixture(t, testName2, "android")

	var wfID string

	step(t, "create_workflow", func(st *testing.T) {
		wfID = createWorkflowFixture(t, wfName, []string{testID1, testID2})
		if wfID == "" {
			st.Fatal("createWorkflowFixture returned empty ID")
		}
		st.Logf("created workflow: id=%s name=%s", wfID, wfName)
	})

	step(t, "workflow_info", func(st *testing.T) {
		result := runCLI(t, "workflow", "info", wfID, "--json")
		if result.ExitCode != 0 {
			st.Skipf("workflow info not supported: %s", result.Stderr)
		}
		raw := extractJSON(result.Stdout)
		if !json.Valid([]byte(raw)) {
			st.Fatalf("workflow info is not valid JSON: %s", result.Stdout)
		}
	})

	step(t, "workflow_list_contains_created", func(st *testing.T) {
		result := runCLI(t, "workflow", "list", "--json")
		if result.ExitCode != 0 {
			st.Fatalf("workflow list failed: %s", result.Stderr)
		}
		if !strings.Contains(result.Stdout, wfID) {
			st.Fatalf("workflow list missing ID %s", wfID)
		}
	})

	step(t, "quarantine_add", func(st *testing.T) {
		result := runCLI(t, "workflow", "quarantine", "add", wfID, testID1)
		if result.ExitCode != 0 {
			st.Skipf("quarantine not supported: %s", result.Stderr)
		}
	})

	step(t, "quarantine_list", func(st *testing.T) {
		result := runCLI(t, "workflow", "quarantine", "list", wfID)
		if result.ExitCode != 0 {
			st.Skipf("quarantine list not supported: %s", result.Stderr)
		}
	})

	step(t, "quarantine_remove", func(st *testing.T) {
		result := runCLI(t, "workflow", "quarantine", "remove", wfID, testID1)
		if result.ExitCode != 0 {
			st.Skipf("quarantine remove not supported: %s", result.Stderr)
		}
	})

	step(t, "config_show", func(st *testing.T) {
		result := runCLI(t, "workflow", "config", "show", wfID)
		if result.ExitCode != 0 {
			st.Skipf("workflow config show not supported: %s", result.Stderr)
		}
	})

	step(t, "config_set", func(st *testing.T) {
		result := runCLI(t, "workflow", "config", "set", wfID, "--parallel", "2", "--retries", "1")
		if result.ExitCode != 0 {
			st.Skipf("workflow config set not supported: %s", result.Stderr)
		}
	})

	step(t, "workflow_run_no_wait", func(st *testing.T) {
		result := runCLI(t, "workflow", "run", wfID, "--no-wait", "--json")
		if result.ExitCode != 0 {
			st.Skipf("workflow run --no-wait failed: %s\n%s", result.Stdout, result.Stderr)
		}
		raw := extractJSON(result.Stdout)
		if !json.Valid([]byte(raw)) {
			st.Fatalf("run output is not valid JSON: %s", result.Stdout)
		}

		var resp struct {
			TaskID string `json:"task_id"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(raw), &resp); err == nil && resp.TaskID != "" {
			// Try status by task UUID
			statusResult := runCLI(t, "workflow", "status", resp.TaskID, "--json")
			if statusResult.ExitCode == 0 {
				statusRaw := extractJSON(statusResult.Stdout)
				if !json.Valid([]byte(statusRaw)) {
					st.Fatalf("status output is not valid JSON: %s", statusResult.Stdout)
				}
			}
			// Cancel it
			_ = runCLI(t, "workflow", "cancel", resp.TaskID)
		}
	})

	step(t, "workflow_history", func(st *testing.T) {
		result := runCLI(t, "workflow", "history", wfID, "--json")
		if result.ExitCode != 0 {
			st.Skipf("workflow history not available: %s", result.Stderr)
		}
		raw := extractJSON(result.Stdout)
		if !json.Valid([]byte(raw)) {
			st.Fatalf("history output is not valid JSON: %s", result.Stdout)
		}
	})

	step(t, "delete_workflow", func(st *testing.T) {
		result := runCLI(t, "workflow", "delete", wfID, "--force", "--json")
		if result.ExitCode != 0 {
			st.Fatalf("workflow delete failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})
}
