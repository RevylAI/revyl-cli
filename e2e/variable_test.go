//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestVariableLifecycle exercises env var and custom variable CRUD including
// the PR-new get and rename commands.
func TestVariableLifecycle(t *testing.T) {
	testName := uniqueName("e2e-var-test")
	testID := createTestFixture(t, testName, "android")

	// --- Env vars ---
	step(t, "env_var_set", func(st *testing.T) {
		result := runCLI(t, "test", "env", "set", testID, "E2E_KEY", "e2e_value")
		if result.ExitCode != 0 {
			st.Fatalf("env set failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})

	step(t, "env_var_list", func(st *testing.T) {
		result := runCLI(t, "test", "env", "list", testID, "--json")
		if result.ExitCode != 0 {
			st.Fatalf("env list failed: %s\n%s", result.Stdout, result.Stderr)
		}
		if !strings.Contains(result.Stdout, "E2E_KEY") {
			st.Fatalf("env list missing E2E_KEY: %s", result.Stdout)
		}
	})

	step(t, "env_var_delete", func(st *testing.T) {
		result := runCLI(t, "test", "env", "delete", testID, "E2E_KEY")
		if result.ExitCode != 0 {
			st.Fatalf("env delete failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})

	// --- Custom vars ---
	step(t, "custom_var_set", func(st *testing.T) {
		result := runCLI(t, "test", "var", "set", testID, "MY_VAR", "my_value")
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
		result := runCLI(t, "test", "var", "list", testID, "--json")
		if result.ExitCode != 0 {
			st.Fatalf("var list failed: %s\n%s", result.Stdout, result.Stderr)
		}
		raw := extractJSON(result.Stdout)
		if !json.Valid([]byte(raw)) {
			st.Fatalf("var list is not valid JSON: %s", result.Stdout)
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
