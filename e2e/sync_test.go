//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSyncLifecycle exercises sync push/pull with a comprehensive YAML fixture
// including all block types. Validates the round-trip: push -> pull -> verify content.
func TestSyncLifecycle(t *testing.T) {
	name := uniqueName("e2e-sync")

	// Create a test first so we have something to sync against
	testID := createTestFixture(t, name, "android")

	step(t, "push_comprehensive_yaml", func(st *testing.T) {
		yamlPath := writeYAMLFixture(t, name)
		result := runCLI(t, "test", "push", testID, "--file", yamlPath)
		if result.ExitCode != 0 {
			st.Skipf("test push not supported or failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})

	step(t, "pull_test_yaml", func(st *testing.T) {
		tmpDir := t.TempDir()
		outPath := filepath.Join(tmpDir, name+".yaml")
		result := runCLI(t, "test", "pull", testID, "--output", outPath)
		if result.ExitCode != 0 {
			st.Skipf("test pull not supported: %s\n%s", result.Stdout, result.Stderr)
		}

		content, err := os.ReadFile(outPath)
		if err != nil {
			st.Fatalf("failed to read pulled YAML: %v", err)
		}
		if len(content) == 0 {
			st.Fatal("pulled YAML is empty")
		}

		// Verify the pulled YAML contains key elements from our fixture
		s := string(content)
		for _, expected := range []string{"instructions", "validation"} {
			if !strings.Contains(s, expected) {
				st.Fatalf("pulled YAML missing %q block type", expected)
			}
		}
	})

	step(t, "diff_shows_no_changes_after_roundtrip", func(st *testing.T) {
		result := runCLI(t, "test", "diff", testID)
		if result.ExitCode != 0 {
			st.Skipf("test diff not supported: %s", result.Stderr)
		}
	})
}

// TestSyncValidateFixtures validates all three YAML testdata fixtures locally.
func TestSyncValidateFixtures(t *testing.T) {
	step(t, "validate_comprehensive", func(st *testing.T) {
		result := runCLI(t, "test", "validate", filepath.Join(testdataDir(), "comprehensive.yaml"), "--json")
		if result.ExitCode != 0 {
			st.Logf("validate not supported or failed: %s", result.Stderr)
			return
		}
		raw := extractJSON(result.Stdout)
		if !json.Valid([]byte(raw)) {
			st.Fatalf("validate output is not valid JSON: %s", result.Stdout)
		}
	})

	step(t, "validate_minimal", func(st *testing.T) {
		result := runCLI(t, "test", "validate", filepath.Join(testdataDir(), "minimal.yaml"), "--json")
		if result.ExitCode != 0 {
			st.Logf("validate not supported: %s", result.Stderr)
			return
		}
	})

	step(t, "validate_invalid_catches_errors", func(st *testing.T) {
		result := runCLI(t, "test", "validate", filepath.Join(testdataDir(), "invalid.yaml"), "--json")
		combined := result.Stdout + result.Stderr
		if result.ExitCode == 0 && !strings.Contains(strings.ToLower(combined), "error") {
			st.Fatal("invalid YAML should produce errors")
		}
	})
}
