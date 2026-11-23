package revylplugin_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/pinnedskills"
)

func TestPluginTextUsesOnlyCommandsThePinnedRuntimeHas(t *testing.T) {
	root := pluginRoot(t)
	var manifest struct {
		ReleaseTag string `json:"release_tag"`
	}
	content, err := os.ReadFile(filepath.Join(root, "runtime-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatal(err)
	}
	tree, err := pinnedskills.LoadCommandTree(manifest.ReleaseTag)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"skills", "references", "agents"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".md") {
				return err
			}
			text, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			relative, _ := filepath.Rel(root, path)
			for _, problem := range tree.Problems(string(text)) {
				t.Errorf("%s %s", filepath.ToSlash(relative), problem)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
