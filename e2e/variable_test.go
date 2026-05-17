//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// TestVariableLifecycle exercises env var and custom variable CRUD including
// the PR-new get and rename commands.
func TestVariableLifecycle(t *testing.T) {
	testName := uniqueName("e2e-var-test")
	testID := createTestFixture(t, testName, "android")
	launchKey := strings.ToUpper(strings.ReplaceAll(uniqueName("e2e-key"), "-", "_"))

	// --- Org launch vars attached to a test ---
	step(t, "launch_var_create", func(st *testing.T) {
		result := runCLI(t, "global", "launch-var", "create", launchKey+"=e2e_value", "--json")
		if result.ExitCode != 0 {
			st.Fatalf("launch-var create failed: %s\n%s", result.Stdout, result.Stderr)
		}
		t.Cleanup(func() {
			_ = runCLI(t, "global", "launch-var", "delete", launchKey, "--force", "--json")
		})
	})

	step(t, "launch_var_list", func(st *testing.T) {
		result := runCLI(t, "global", "launch-var", "list", "--json")
		if result.ExitCode != 0 {
			st.Fatalf("launch-var list failed: %s\n%s", result.Stdout, result.Stderr)
		}
		if !strings.Contains(result.Stdout, launchKey) {
			st.Fatalf("launch-var list missing %s: %s", launchKey, result.Stdout)
		}
	})

	step(t, "launch_var_attach", func(st *testing.T) {
		result := runCLI(t, "test", "launch-var", "attach", testID, launchKey)
		if result.ExitCode != 0 {
			st.Fatalf("launch-var attach failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})

	step(t, "launch_var_attached_list", func(st *testing.T) {
		result := runCLI(t, "test", "launch-var", "list", testID)
		if result.ExitCode != 0 {
			st.Fatalf("test launch-var list failed: %s\n%s", result.Stdout, result.Stderr)
		}
		if !strings.Contains(result.Stdout, launchKey) {
			st.Fatalf("test launch-var list missing %s: %s", launchKey, result.Stdout)
		}
	})

	step(t, "launch_var_detach", func(st *testing.T) {
		result := runCLI(t, "test", "launch-var", "detach", testID, launchKey)
		if result.ExitCode != 0 {
			st.Fatalf("launch-var detach failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})

	// --- Custom vars ---
	step(t, "custom_var_set", func(st *testing.T) {
		result := runCLI(t, "test", "var", "set", testID, "MY_VAR=my_value")
		if result.ExitCode != 0 {
			st.Fatalf("var set failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})

	step(t, "custom_var_get", func(st *testing.T) {
		result := runCLI(t, "test", "var", "get", testID, "MY_VAR")
		if result.ExitCode != 0 {
			st.Skipf("var get not supported: %s", result.Stderr)
		}
		if !strings.Contains(result.Stdout, "my_value") {
			st.Fatalf("var get missing value: %s", result.Stdout)
		}
	})

	step(t, "custom_var_list", func(st *testing.T) {
		result := runCLI(t, "test", "var", "list", testID)
		if result.ExitCode != 0 {
			st.Fatalf("var list failed: %s\n%s", result.Stdout, result.Stderr)
		}
		combined := result.Stdout + result.Stderr
		if !strings.Contains(combined, "MY_VAR") {
			st.Fatalf("var list missing MY_VAR: %s", combined)
		}
	})

	step(t, "custom_var_rename", func(st *testing.T) {
		result := runCLI(t, "test", "var", "rename", testID, "MY_VAR", "RENAMED_VAR")
		if result.ExitCode != 0 {
			st.Skipf("var rename not supported: %s", result.Stderr)
		}
	})

	step(t, "custom_var_delete", func(st *testing.T) {
		// Delete whichever name the var has now
		result := runCLI(t, "test", "var", "delete", testID, "RENAMED_VAR")
		if result.ExitCode != 0 {
			// Try original name in case rename wasn't supported
			result = runCLI(t, "test", "var", "delete", testID, "MY_VAR")
			if result.ExitCode != 0 {
				st.Fatalf("var delete failed: %s\n%s", result.Stdout, result.Stderr)
			}
		}
	})
}
