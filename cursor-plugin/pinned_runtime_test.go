package cursorplugin_test

import (
	"encoding/json"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/pinnedskills"
)

// TestPluginTextUsesOnlyCommandsThePinnedRuntimeHas skips the proof Automation
// prompt because it installs the latest CLI on the CI runner it drives.
func TestPluginTextUsesOnlyCommandsThePinnedRuntimeHas(t *testing.T) {
	root := pluginRootPath(t)
	var manifest struct {
		ReleaseTag string `json:"release_tag"`
	}
	if err := json.Unmarshal([]byte(readText(t, filepath.Join(root, "runtime-manifest.json"))), &manifest); err != nil {
		t.Fatal(err)
	}
	tree, err := pinnedskills.LoadCommandTree(manifest.ReleaseTag)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"skills", "rules"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !(strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".mdc")) {
				return err
			}
			relative, err := filepath.Rel(root, path)
			if err != nil || filepath.ToSlash(relative) == "skills/revyl-proof-automation/PROMPT.md" {
				return err
			}
			for _, problem := range tree.Problems(readText(t, path)) {
				t.Errorf("%s %s", filepath.ToSlash(relative), problem)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
