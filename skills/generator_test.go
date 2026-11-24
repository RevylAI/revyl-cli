package skills

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/revyl/cli/internal/pinnedskills"
)

func TestCursorPluginCopiesMatchRenderedPackages(t *testing.T) {
	for _, name := range []string{RevylCLIDevLoopName, RevylCLIAuthBypassName} {
		files, err := Files(name)
		if err != nil {
			t.Fatal(err)
		}
		pluginRoot := filepath.Join("..", "cursor-plugin", "skills", name)
		fileCount := 0
		err = filepath.WalkDir(pluginRoot, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				fileCount++
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if fileCount != len(files) {
			t.Errorf("%s: plugin has %d files, source has %d; run make sync-cursor-plugin-skills", name, fileCount, len(files))
		}
		for _, file := range files {
			pluginPath := filepath.Join(pluginRoot, filepath.FromSlash(file.Path))
			content, err := os.ReadFile(pluginPath)
			if err != nil {
				t.Fatal(err)
			}
			want := file.Content
			if strings.HasSuffix(file.Path, ".md") {
				if want, err = pinnedskills.Render(name+"/"+file.Path, file.Content); err != nil {
					t.Fatal(err)
				}
			}
			if !bytes.Equal(content, want) {
				t.Errorf("%s/%s: plugin content differs from the rendered source; run make sync-cursor-plugin-skills", name, file.Path)
			}
			info, err := os.Stat(pluginPath)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != file.Mode {
				t.Errorf("%s/%s: plugin mode %o differs from source %o", name, file.Path, info.Mode().Perm(), file.Mode)
			}
		}
	}
}

func TestSyncCursorPluginSkillsRendersMarkdownAndPreservesResourcesAndModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Makefile target uses POSIX paths")
	}
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is not installed")
	}
	makefile, err := filepath.Abs(filepath.Join("..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	fixtures := map[string]File{
		"skills/revyl-cli-auth-bypass/SKILL.md":                   {Content: []byte("# Skill\n\n## Revyl agent rules\n\n- Pass `--intent` on commands.\n- Report problems with Revyl yourself.\n- When a `revyl` command says a newer Revyl CLI is available, upgrade.\n\n## Shared Contract\n"), Mode: 0o644},
		"skills/revyl-cli-auth-bypass/agents/openai.yaml":         {Content: []byte("metadata: preserved\n"), Mode: 0o644},
		"skills/revyl-cli-auth-bypass/references/nested/guide.md": {Content: []byte("reference"), Mode: 0o644},
		"skills/revyl-cli-auth-bypass/scripts/check.sh":           {Content: []byte("#!/bin/sh\nexit 0\n"), Mode: 0o755},
		"skills/revyl-cli-auth-bypass/assets/icon.png":            {Content: []byte{0, 1, 2, 3}, Mode: 0o644},
		"cursor-plugin/skills/revyl-cli-auth-bypass/stale.md":     {Content: []byte("obsolete"), Mode: 0o644},
		"cursor-plugin/skills/revyl-cloud-agent/SKILL.md":         {Content: []byte("plugin-owned"), Mode: 0o644},
	}
	for name, fixture := range fixtures {
		destination := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, fixture.Content, fixture.Mode); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		cmd := exec.CommandContext(ctx, makePath, "-f", makefile, "sync-cursor-plugin-skills", "CURSOR_PLUGIN_COPY_SKILLS=revyl-cli-auth-bypass", "VERSION=test", "COMMIT=test", "DATE=test")
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("sync-cursor-plugin-skills: %v\n%s", err, output)
		}
		for _, name := range []string{"SKILL.md", "agents/openai.yaml", "references/nested/guide.md", "scripts/check.sh", "assets/icon.png"} {
			fixture := fixtures["skills/revyl-cli-auth-bypass/"+name]
			if name == "SKILL.md" {
				if fixture.Content, err = pinnedskills.Render("revyl-cli-auth-bypass/SKILL.md", fixture.Content); err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(fixture.Content, []byte("--intent")) {
					t.Fatal("pinned rendering kept the source agent rules")
				}
			}
			copiedPath := filepath.Join(root, "cursor-plugin", "skills", "revyl-cli-auth-bypass", filepath.FromSlash(name))
			content, err := os.ReadFile(copiedPath)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(copiedPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(content, fixture.Content) || info.Mode().Perm() != fixture.Mode {
				t.Errorf("%s: copy did not retain bytes and mode %o", name, fixture.Mode)
			}
		}
		if _, err := os.Stat(filepath.Join(root, "cursor-plugin", "skills", "revyl-cli-auth-bypass", "stale.md")); !os.IsNotExist(err) {
			t.Errorf("stale generated file must be removed: %v", err)
		}
		content, err := os.ReadFile(filepath.Join(root, "cursor-plugin", "skills", "revyl-cloud-agent", "SKILL.md"))
		if err != nil || string(content) != "plugin-owned" {
			t.Errorf("sync must preserve plugin-owned skills: %q, %v", content, err)
		}
	}
}
