package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSyncSkillsRefusesASourceSymlinkThatLeavesTheSkillsTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs extra privileges on Windows")
	}
	root := t.TempDir()
	outside := filepath.Join(root, "outside.md")
	if err := os.WriteFile(outside, []byte("not a skill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"skills/demo", "cursor-plugin/skills"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "skills", "demo", "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	err := syncSkills(root, []string{"demo"}, false)

	if err == nil {
		t.Fatal("syncSkills() error = nil, want the escaping symlink refused")
	}
	if _, statErr := os.Stat(filepath.Join(root, "cursor-plugin", "skills", "demo", "SKILL.md")); !os.IsNotExist(statErr) {
		t.Errorf("synced content from outside the skills tree: %v", statErr)
	}
}

func TestSyncSkillsRefusesASymlinkedSkillsDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs extra privileges on Windows")
	}
	root := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Join(elsewhere, "demo"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "demo", "SKILL.md"), []byte("# Demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cursor-plugin", "skills"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(root, "skills")); err != nil {
		t.Fatal(err)
	}

	err := syncSkills(root, []string{"demo"}, false)

	if err == nil {
		t.Fatal("syncSkills() error = nil, want the symlinked skills directory refused")
	}
	if _, statErr := os.Stat(filepath.Join(root, "cursor-plugin", "skills", "demo")); !os.IsNotExist(statErr) {
		t.Errorf("synced a skill through the symlinked skills directory: %v", statErr)
	}
}

func TestSyncSkillsWritesNothingWhenALaterSkillFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs extra privileges on Windows")
	}
	root := t.TempDir()
	for _, dir := range []string{"skills/good", "skills/bad", "cursor-plugin/skills/good"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "good", "SKILL.md"), []byte("# Good, updated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.md")
	if err := os.WriteFile(outside, []byte("not a skill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "skills", "bad", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	bundled := filepath.Join(root, "cursor-plugin", "skills", "good", "SKILL.md")
	previous := []byte("# Good, as bundled\n")
	if err := os.WriteFile(bundled, previous, 0o600); err != nil {
		t.Fatal(err)
	}

	err := syncSkills(root, []string{"good", "bad"}, false)

	if err == nil {
		t.Fatal("syncSkills() error = nil, want the escaping symlink in the second skill refused")
	}
	content, readErr := os.ReadFile(bundled)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(content, previous) {
		t.Errorf("first skill's bundled copy = %q, want it untouched as %q", content, previous)
	}
}
