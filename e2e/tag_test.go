//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTagLifecycle exercises tag CRUD + assignment to a test.
func TestTagLifecycle(t *testing.T) {
	tagName := uniqueName("e2e-tag")
	testName := uniqueName("e2e-tag-test")
	var tagID string

	testID := createTestFixture(t, testName, "android")

	step(t, "create_tag", func(st *testing.T) {
		result := runCLI(t, "tag", "create", "--name", tagName, "--color", "#FF5733", "--json")
		if result.ExitCode != 0 {
			st.Fatalf("tag create failed: %s\n%s", result.Stdout, result.Stderr)
		}
		var resp struct {
			ID    string `json:"id"`
			TagID string `json:"tag_id"`
		}
		raw := extractJSON(result.Stdout)
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			st.Fatalf("failed to parse tag create response: %v\n%s", err, result.Stdout)
		}
		tagID = resp.ID
		if tagID == "" {
			tagID = resp.TagID
		}
		if tagID == "" {
			st.Fatalf("tag create returned empty ID\n%s", result.Stdout)
		}
		t.Cleanup(func() {
			_ = runCLI(t, "tag", "delete", tagID, "--force")
		})
	})

	step(t, "list_tags_contains_created", func(st *testing.T) {
		result := runCLI(t, "tag", "list", "--json")
		if result.ExitCode != 0 {
			st.Fatalf("tag list failed: %s", result.Stderr)
		}
		if !strings.Contains(result.Stdout, tagName) {
			st.Fatalf("tag list missing %q", tagName)
		}
	})

	step(t, "assign_tag_to_test", func(st *testing.T) {
		result := runCLI(t, "tag", "add", testID, tagID)
		if result.ExitCode != 0 {
			st.Fatalf("tag add failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})

	step(t, "verify_tag_on_test", func(st *testing.T) {
		result := runCLI(t, "tag", "get", testID, "--json")
		if result.ExitCode != 0 {
			st.Fatalf("tag get failed: %s\n%s", result.Stdout, result.Stderr)
		}
		if !strings.Contains(result.Stdout, tagName) {
			st.Fatalf("tag get output missing assigned tag %q: %s", tagName, result.Stdout)
		}
	})

	step(t, "remove_tag_from_test", func(st *testing.T) {
		result := runCLI(t, "tag", "remove", testID, tagID)
		if result.ExitCode != 0 {
			st.Fatalf("tag remove failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})

	step(t, "delete_tag", func(st *testing.T) {
		result := runCLI(t, "tag", "delete", tagID, "--force")
		if result.ExitCode != 0 {
			st.Fatalf("tag delete failed: %s\n%s", result.Stdout, result.Stderr)
		}
	})
}
