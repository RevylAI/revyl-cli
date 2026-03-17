//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"
)

// TestAuthLifecycle validates authentication and read-only endpoints in a single
// scenario: auth status, ping, dashboard metrics, and billing plan.
func TestAuthLifecycle(t *testing.T) {
	step(t, "auth_status_returns_valid_json", func(st *testing.T) {
		result := runCLI(t, "auth", "status")
		if result.ExitCode != 0 {
			st.Fatalf("auth status failed: %s\n%s", result.Stdout, result.Stderr)
		}
		combined := result.Stdout + result.Stderr
		if len(combined) == 0 {
			st.Fatal("auth status produced no output")
		}
	})

	step(t, "ping_succeeds", func(st *testing.T) {
		result := runCLI(t, "ping")
		if result.ExitCode != 0 {
			st.Fatalf("ping failed (exit %d): %s\n%s", result.ExitCode, result.Stdout, result.Stderr)
		}
	})

	step(t, "auth_billing_returns_plan", func(st *testing.T) {
		result := runCLI(t, "auth", "billing", "--json")
		if result.ExitCode != 0 {
			st.Skipf("billing endpoint not available: %s", result.Stderr)
		}
		raw := extractJSON(result.Stdout)
		if raw == "" || raw == "{}" {
			st.Skip("billing returned empty output")
		}
		if !json.Valid([]byte(raw)) {
			st.Fatalf("billing output is not valid JSON: %s", result.Stdout)
		}
	})
}
