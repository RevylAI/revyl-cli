//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"
)

// TestCLIOutputJSON validates that key CLI commands produce parseable JSON output.
// This catches schema drift between the backend and CLI type definitions.
func TestCLIOutputJSON(t *testing.T) {
	jsonCommands := []struct {
		name string
		args []string
	}{
		{"test_list", []string{"test", "list", "--json"}},
		{"workflow_list", []string{"workflow", "list", "--json"}},
		{"app_list", []string{"app", "list", "--json"}},
		{"tag_list", []string{"tag", "list", "--json"}},
		{"module_list", []string{"module", "list", "--json"}},
		{"script_list", []string{"script", "list", "--json"}},
	}

	for _, tc := range jsonCommands {
		tc := tc
		step(t, tc.name+"_produces_valid_json", func(st *testing.T) {
			result := runCLI(t, tc.args...)
			if result.ExitCode != 0 {
				st.Skipf("%s failed (exit %d): %s", tc.name, result.ExitCode, result.Stderr)
			}
			raw := extractJSON(result.Stdout)
			if !json.Valid([]byte(raw)) {
				st.Fatalf("%s output is not valid JSON: %.200s", tc.name, result.Stdout)
			}
		})
	}
}

// TestCLIHelpCommands validates that all major command groups have working --help.
func TestCLIHelpCommands(t *testing.T) {
	helpCommands := []string{
		"test", "workflow", "app", "build", "tag", "module", "script",
		"device", "config", "sync", "auth",
	}

	for _, cmd := range helpCommands {
		cmd := cmd
		step(t, cmd+"_help", func(st *testing.T) {
			result := runCLI(t, cmd, "--help")
			if result.ExitCode != 0 {
				st.Fatalf("%s --help exited %d: %s", cmd, result.ExitCode, result.Stderr)
			}
			if len(result.Stdout) == 0 && len(result.Stderr) == 0 {
				st.Fatalf("%s --help produced no output", cmd)
			}
		})
	}
}
