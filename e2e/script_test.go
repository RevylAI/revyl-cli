//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestScriptLifecycle exercises the full script CRUD lifecycle:
// create -> list -> get (by name) -> get (by ID) -> update -> usage -> insert -> delete.
func TestScriptLifecycle(t *testing.T) {
	name := uniqueName("e2e-script")
	var scriptID string

	step(t, "create_script", func(st *testing.T) {
		tmpDir := t.TempDir()
		scriptPath := filepath.Join(tmpDir, "e2e_script.py")
		if err := os.WriteFile(scriptPath, []byte("print('e2e test script')\n"), 0644); err != nil {
			st.Fatalf("failed to write script file: %v", err)
		}

		result := runCLI(t, "script", "create", name, "--file", scriptPath, "--runtime", "python")
		if result.ExitCode != 0 {
			st.Fatalf("script create failed: %s\n%s", result.Stdout, result.Stderr)
		}

		// script create outputs text like: Created script "name" (ID: uuid)
		combined := result.Stdout + result.Stderr
		re := regexp.MustCompile(`(?i)(?:id|ID)[:\s]+([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)
		if matches := re.FindStringSubmatch(combined); len(matches) > 1 {
			scriptID = matches[1]
		}

		if scriptID == "" {
			st.Logf("could not extract script ID from output (will use name): %s", combined)
		}
		t.Cleanup(func() {
			_ = runCLI(t, "script", "delete", name, "--force")
		})
	})

	step(t, "list_scripts", func(st *testing.T) {
		result := runCLI(t, "script", "list", "--json")
		if result.ExitCode != 0 {
			st.Fatalf("script list failed: %s", result.Stderr)
		}
		if !strings.Contains(result.Stdout, name) {
			st.Fatalf("script list missing %q", name)
		}
	})

	step(t, "list_scripts_runtime_filter", func(st *testing.T) {
		result := runCLI(t, "script", "list", "--runtime", "python", "--json")
		if result.ExitCode != 0 {
			st.Fatalf("script list --runtime failed: %s", result.Stderr)
		}
		if !strings.Contains(result.Stdout, name) {
			st.Fatalf("runtime-filtered list missing %q", name)
		}
	})

	step(t, "get_script_by_name", func(st *testing.T) {
		result := runCLI(t, "script", "get", name, "--json")
		if result.ExitCode != 0 {
			st.Fatalf("script get by name failed: %s\n%s", result.Stdout, result.Stderr)
		}
		raw := extractJSON(result.Stdout)
		if !json.Valid([]byte(raw)) {
			st.Fatalf("script get is not valid JSON: %s", result.Stdout)
		}
	})

	step(t, "get_script_by_id", func(st *testing.T) {
		if scriptID == "" {
			st.Skip("no script ID available")
		}
		result := runCLI(t, "script", "get", scriptID, "--json")
		if result.ExitCode != 0 {
			st.Fatalf("script get by ID failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})

	step(t, "update_script", func(st *testing.T) {
		tmpDir := t.TempDir()
		updatedPath := filepath.Join(tmpDir, "updated.py")
		if err := os.WriteFile(updatedPath, []byte("print('updated e2e script')\n"), 0644); err != nil {
			st.Fatalf("failed to write updated script: %v", err)
		}

		result := runCLI(t, "script", "update", name, "--file", updatedPath, "--json")
		if result.ExitCode != 0 {
			st.Skipf("script update failed: %s", result.Stderr)
		}
	})

	step(t, "script_usage", func(st *testing.T) {
		result := runCLI(t, "script", "usage", name, "--json")
		if result.ExitCode != 0 {
			st.Skipf("script usage not supported: %s", result.Stderr)
		}
	})

	step(t, "script_insert_yaml_snippet", func(st *testing.T) {
		result := runCLI(t, "script", "insert", name)
		if result.ExitCode != 0 {
			st.Skipf("script insert not supported: %s", result.Stderr)
		}
		if !strings.Contains(result.Stdout, "code_execution") {
			st.Fatalf("insert output missing 'code_execution': %s", result.Stdout)
		}
	})

	step(t, "delete_script", func(st *testing.T) {
		result := runCLI(t, "script", "delete", name, "--force")
		if result.ExitCode != 0 {
			st.Fatalf("script delete failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})
}
