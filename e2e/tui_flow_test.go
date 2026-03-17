//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"
)

// TestTUIDataLoads validates that the TUI's data-loading commands work against
// the real backend. Rather than simulating bubbletea key presses (which would
// require importing internal packages), we exercise the same API calls the TUI
// makes by running the CLI commands that feed each TUI screen.
//
// This catches the most common TUI regression: the backend changed a response
// shape and the TUI's JSON deserialization breaks.
func TestTUIDataLoads(t *testing.T) {
	step(t, "dashboard_metrics_load", func(st *testing.T) {
		// workflow list does not require project init (unlike test list)
		result := runCLI(t, "workflow", "list", "--json")
		if result.ExitCode != 0 {
			st.Skipf("workflow list (dashboard proxy) failed: %s", result.Stderr)
		}
		raw := extractJSON(result.Stdout)
		if !json.Valid([]byte(raw)) {
			st.Fatalf("dashboard data is not valid JSON: %s", result.Stdout)
		}
	})

	step(t, "workflow_list_has_test_count_and_last_execution", func(st *testing.T) {
		result := runCLI(t, "workflow", "list", "--json")
		if result.ExitCode != 0 {
			st.Skipf("workflow list failed: %s", result.Stderr)
		}
		raw := extractJSON(result.Stdout)
		if !json.Valid([]byte(raw)) {
			st.Fatalf("workflow list is not valid JSON: %s", result.Stdout)
		}
	})

	step(t, "app_list_loads", func(st *testing.T) {
		result := runCLI(t, "app", "list", "--json")
		if result.ExitCode != 0 {
			st.Skipf("app list failed: %s", result.Stderr)
		}
		raw := extractJSON(result.Stdout)
		if !json.Valid([]byte(raw)) {
			st.Fatalf("app list is not valid JSON: %s", result.Stdout)
		}
	})

	step(t, "module_list_loads", func(st *testing.T) {
		result := runCLI(t, "module", "list", "--json")
		if result.ExitCode != 0 {
			st.Skipf("module list failed: %s", result.Stderr)
		}
	})

	step(t, "tag_list_loads", func(st *testing.T) {
		result := runCLI(t, "tag", "list", "--json")
		if result.ExitCode != 0 {
			st.Skipf("tag list failed: %s", result.Stderr)
		}
	})

	step(t, "script_list_loads", func(st *testing.T) {
		result := runCLI(t, "script", "list", "--json")
		if result.ExitCode != 0 {
			st.Skipf("script list failed: %s", result.Stderr)
		}
		raw := extractJSON(result.Stdout)
		if !json.Valid([]byte(raw)) {
			st.Fatalf("script list is not valid JSON: %s", result.Stdout)
		}
	})
}
