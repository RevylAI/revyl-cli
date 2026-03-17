//go:build e2e

package e2e

import (
	"testing"
)

// TestErrorPaths validates that the CLI handles error conditions correctly:
// 404 for nonexistent resources, 401 for bad auth, duplicate detection, etc.
func TestErrorPaths(t *testing.T) {
	step(t, "nonexistent_test_id_returns_error_or_empty", func(st *testing.T) {
		result := runCLI(t, "test", "remote", "00000000-0000-0000-0000-000000000000", "--json")
		// Backend may return 404 (exit non-zero) or an empty/null response (exit 0).
		// Either is acceptable -- the key thing is no panic or 500.
		if result.ExitCode != 0 {
			return // expected
		}
		if len(result.Stderr) > 5000 {
			st.Fatal("unexpectedly large stderr for nonexistent test (possible panic)")
		}
	})

	step(t, "nonexistent_workflow_status_returns_error", func(st *testing.T) {
		result := runCLI(t, "workflow", "status", "00000000-0000-0000-0000-000000000000", "--json")
		if result.ExitCode == 0 {
			st.Fatal("expected error for nonexistent workflow status but got exit 0")
		}
	})

	step(t, "delete_nonexistent_test_is_graceful", func(st *testing.T) {
		result := runCLI(t, "test", "delete", "00000000-0000-0000-0000-000000000000", "--force", "--json")
		// Should not panic or 500 -- may exit non-zero (404) but should be clean
		if result.ExitCode == 0 {
			return // idempotent delete is fine
		}
		if len(result.Stderr) > 5000 {
			st.Fatalf("delete of nonexistent test produced unexpectedly large stderr (possible panic)")
		}
	})

	step(t, "delete_nonexistent_workflow_is_graceful", func(st *testing.T) {
		result := runCLI(t, "workflow", "delete", "00000000-0000-0000-0000-000000000000", "--force", "--json")
		if result.ExitCode == 0 {
			return
		}
		if len(result.Stderr) > 5000 {
			st.Fatalf("delete of nonexistent workflow produced unexpectedly large stderr")
		}
	})

	step(t, "invalid_api_key_returns_auth_error", func(st *testing.T) {
		// Run with intentionally bad API key
		result := runCLIWithEnv(t, map[string]string{
			"REVYL_API_KEY":     "rev_invalid_key_for_e2e_test",
			"REVYL_BACKEND_URL": backendURL,
		}, "test", "list", "--json")
		if result.ExitCode == 0 {
			st.Fatal("expected auth error with invalid API key but got exit 0")
		}
	})

	step(t, "invalid_command_flag_returns_error", func(st *testing.T) {
		result := runCLI(t, "test", "list", "--nonexistent-flag-12345")
		if result.ExitCode == 0 {
			st.Fatal("expected error for invalid flag but got exit 0")
		}
	})
}

// runCLIWithEnv runs the CLI with custom environment overrides.
func runCLIWithEnv(t *testing.T, env map[string]string, args ...string) CLIResult {
	t.Helper()
	// Temporarily override the package-level vars
	origKey := apiKey
	origURL := backendURL
	defer func() {
		apiKey = origKey
		backendURL = origURL
	}()

	if v, ok := env["REVYL_API_KEY"]; ok {
		apiKey = v
	}
	if v, ok := env["REVYL_BACKEND_URL"]; ok {
		backendURL = v
	}

	return runCLI(t, args...)
}
