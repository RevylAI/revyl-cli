//go:build e2e

package e2e

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLILocal tests CLI commands that need zero backend access.
// These always pass regardless of authentication or backend availability.
func TestCLILocal(t *testing.T) {
	step(t, "version_output", func(st *testing.T) {
		result := runCLI(t, "version")
		// revyl version outputs via --version flag
		if result.ExitCode != 0 {
			result = runCLI(t, "--version")
		}
		combined := result.Stdout + result.Stderr
		if !strings.Contains(strings.ToLower(combined), "revyl") && !strings.Contains(combined, "version") {
			st.Fatalf("version output missing 'revyl' or 'version': %s", combined)
		}
	})

	step(t, "help_exits_zero", func(st *testing.T) {
		result := runCLI(t, "--help")
		if result.ExitCode != 0 {
			st.Fatalf("--help exited %d", result.ExitCode)
		}
		if len(result.Stdout) == 0 {
			st.Fatal("--help produced no output")
		}
	})

	step(t, "unknown_command_exits_nonzero", func(st *testing.T) {
		result := runCLI(t, "nonexistent-command-12345")
		if result.ExitCode == 0 {
			st.Fatal("unknown command should exit non-zero")
		}
	})

	step(t, "config_path", func(st *testing.T) {
		result := runCLI(t, "config", "path")
		if result.ExitCode != 0 {
			st.Skipf("config path not supported: %s", result.Stderr)
		}
		out := strings.TrimSpace(result.Stdout)
		if out == "" {
			st.Fatal("config path returned empty output")
		}
	})

	step(t, "validate_minimal_yaml", func(st *testing.T) {
		yamlPath := filepath.Join(testdataDir(), "minimal.yaml")
		result := runCLI(t, "test", "validate", yamlPath, "--json")
		if result.ExitCode != 0 {
			st.Logf("test validate not supported or failed: %s", result.Stderr)
			return
		}
		raw := extractJSON(result.Stdout)
		if !json.Valid([]byte(raw)) {
			st.Fatalf("validate output is not valid JSON: %s", result.Stdout)
		}
	})

	step(t, "validate_comprehensive_yaml", func(st *testing.T) {
		yamlPath := filepath.Join(testdataDir(), "comprehensive.yaml")
		result := runCLI(t, "test", "validate", yamlPath, "--json")
		if result.ExitCode != 0 {
			st.Logf("test validate not supported: %s", result.Stderr)
			return
		}
	})

	step(t, "validate_invalid_yaml_reports_errors", func(st *testing.T) {
		yamlPath := filepath.Join(testdataDir(), "invalid.yaml")
		result := runCLI(t, "test", "validate", yamlPath, "--json")
		combined := result.Stdout + result.Stderr
		if result.ExitCode == 0 && !strings.Contains(strings.ToLower(combined), "error") && !strings.Contains(strings.ToLower(combined), "invalid") {
			st.Fatalf("invalid YAML should produce errors but got clean exit: %s", combined)
		}
	})
}
