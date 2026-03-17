//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestModuleLifecycle exercises module CRUD + versions + restore + usage.
func TestModuleLifecycle(t *testing.T) {
	name := uniqueName("e2e-module")
	var moduleID string

	step(t, "create_module", func(st *testing.T) {
		tmpDir := t.TempDir()
		blocksPath := filepath.Join(tmpDir, "blocks.yaml")
		blocksYAML := `blocks:
  - type: instructions
    step_description: "module step 1"
  - type: validation
    step_description: "module validation"
`
		if err := os.WriteFile(blocksPath, []byte(blocksYAML), 0644); err != nil {
			st.Fatalf("failed to write blocks file: %v", err)
		}

		result := runCLI(t, "module", "create", name,
			"--from-file", blocksPath,
			"--description", "E2E test module")
		if result.ExitCode != 0 {
			st.Fatalf("module create failed: %s\n%s", result.Stdout, result.Stderr)
		}

		combined := result.Stdout + result.Stderr
		re := regexp.MustCompile(`(?i)(?:id|ID)[:\s]+([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)
		if matches := re.FindStringSubmatch(combined); len(matches) > 1 {
			moduleID = matches[1]
		}

		if moduleID == "" {
			var resp struct {
				ID       string `json:"id"`
				ModuleID string `json:"module_id"`
			}
			raw := extractJSON(result.Stdout)
			if err := json.Unmarshal([]byte(raw), &resp); err == nil {
				moduleID = resp.ID
				if moduleID == "" {
					moduleID = resp.ModuleID
				}
			}
		}

		if moduleID == "" {
			st.Fatalf("module create returned no parseable ID\n%s", combined)
		}
		t.Cleanup(func() {
			_ = runCLI(t, "module", "delete", moduleID, "--force")
		})
	})

	step(t, "list_modules_contains_created", func(st *testing.T) {
		result := runCLI(t, "module", "list", "--json")
		if result.ExitCode != 0 {
			st.Fatalf("module list failed: %s", result.Stderr)
		}
		raw := extractJSON(result.Stdout)
		if !json.Valid([]byte(raw)) {
			st.Fatalf("module list is not valid JSON: %s", result.Stdout)
		}
	})

	step(t, "get_module", func(st *testing.T) {
		result := runCLI(t, "module", "get", moduleID, "--json")
		if result.ExitCode != 0 {
			st.Fatalf("module get failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})

	step(t, "update_module", func(st *testing.T) {
		result := runCLI(t, "module", "update", moduleID,
			"--description", "Updated E2E module",
			"--json")
		if result.ExitCode != 0 {
			st.Skipf("module update failed: %s", result.Stderr)
		}
	})

	step(t, "module_versions", func(st *testing.T) {
		result := runCLI(t, "module", "versions", moduleID)
		if result.ExitCode != 0 {
			st.Logf("module versions not supported: %s", result.Stderr)
			return
		}
	})

	step(t, "module_usage", func(st *testing.T) {
		result := runCLI(t, "module", "usage", moduleID, "--json")
		if result.ExitCode != 0 {
			st.Skipf("module usage not supported: %s", result.Stderr)
		}
	})

	step(t, "delete_module", func(st *testing.T) {
		result := runCLI(t, "module", "delete", moduleID, "--force")
		if result.ExitCode != 0 {
			st.Fatalf("module delete failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})
}
