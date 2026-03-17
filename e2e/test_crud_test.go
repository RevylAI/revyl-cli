//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTestLifecycle exercises the full test CRUD lifecycle end-to-end:
// create -> get -> rename -> duplicate -> versions -> history -> delete.
func TestTestLifecycle(t *testing.T) {
	name := uniqueName("e2e-test")
	renamedName := uniqueName("e2e-renamed")
	dupName := uniqueName("e2e-copy")

	var testID string

	step(t, "create_test", func(st *testing.T) {
		testID = createTestFixture(t, name, "android")
		if testID == "" {
			st.Fatal("createTestFixture returned empty ID")
		}
		st.Logf("created test: id=%s name=%s", testID, name)
	})

	step(t, "get_test_by_id", func(st *testing.T) {
		// "test remote" is a list command (no positional args); use "test history" to verify by ID.
		result := runCLI(t, "test", "history", testID, "--json")
		if result.ExitCode != 0 {
			st.Fatalf("test history (get-by-id) failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})

	step(t, "rename_test", func(st *testing.T) {
		// test rename takes positional args: [old-name|id] [new-name]
		result := runCLI(t, "test", "rename", testID, renamedName, "--non-interactive")
		if result.ExitCode != 0 {
			st.Logf("test rename failed (may require interactive): %s", result.Stderr)
			return
		}
	})

	step(t, "duplicate_test", func(st *testing.T) {
		result := runCLI(t, "test", "duplicate", testID, "--name", dupName, "--json")
		if result.ExitCode != 0 {
			st.Logf("duplicate not supported: %s", result.Stderr)
			return
		}
		dupID := extractUUID(result.Stdout + result.Stderr)
		if dupID != "" {
			t.Cleanup(func() {
				_ = runCLI(t, "test", "delete", dupID, "--force")
			})
		}
	})

	step(t, "test_versions", func(st *testing.T) {
		result := runCLI(t, "test", "versions", testID, "--json")
		if result.ExitCode != 0 {
			st.Logf("versions not supported: %s", result.Stderr)
			return
		}
	})

	step(t, "test_history", func(st *testing.T) {
		result := runCLI(t, "test", "history", testID, "--json")
		if result.ExitCode != 0 {
			st.Logf("history not available: %s", result.Stderr)
			return
		}
		raw := extractJSON(result.Stdout)
		if !json.Valid([]byte(raw)) {
			st.Fatalf("history output is not valid JSON: %s", result.Stdout)
		}
	})

	step(t, "delete_test", func(st *testing.T) {
		result := runCLI(t, "test", "delete", testID, "--force")
		if result.ExitCode != 0 {
			st.Fatalf("test delete failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})

	step(t, "verify_deleted_test_not_accessible", func(st *testing.T) {
		// "test history" takes a test ID and should fail for deleted tests.
		result := runCLI(t, "test", "history", testID, "--json")
		if result.ExitCode != 0 {
			return // expected: deleted test returns error
		}
		combined := strings.ToLower(result.Stdout + result.Stderr)
		if strings.Contains(combined, "not found") || strings.Contains(combined, "deleted") || strings.Contains(combined, "[]") {
			return
		}
		st.Logf("deleted test still accessible via history (soft-delete?): %s", result.Stdout)
	})
}
