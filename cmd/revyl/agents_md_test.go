package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallAgentsMDBlockCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path, wrote, err := installAgentsMDBlock(dir, false)
	if err != nil {
		t.Fatalf("installAgentsMDBlock() error = %v", err)
	}
	if !wrote {
		t.Fatal("expected a new AGENTS.md to be written")
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, agentsMDStartMarker) || !strings.Contains(content, agentsMDEndMarker) {
		t.Fatal("AGENTS.md missing revyl markers")
	}
	if !strings.Contains(content, "revyl dev --remote --detach --json") {
		t.Fatal("AGENTS.md missing the agent dev-loop flow")
	}
}

func TestInstallAgentsMDBlockAppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	existing := "# My Project\n\nCustom instructions here.\n"
	if err := os.WriteFile(filepath.Join(dir, agentsMDFileName), []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}

	path, wrote, err := installAgentsMDBlock(dir, false)
	if err != nil || !wrote {
		t.Fatalf("installAgentsMDBlock() = %v, %v", wrote, err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "Custom instructions here.") {
		t.Fatal("existing content was lost")
	}
	if !strings.Contains(content, agentsMDStartMarker) {
		t.Fatal("revyl block not appended")
	}
}

func TestInstallAgentsMDBlockRespectsForce(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := installAgentsMDBlock(dir, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, agentsMDFileName)

	// Tamper inside the block, then re-install without force: untouched.
	data, _ := os.ReadFile(path)
	tampered := strings.Replace(string(data), "cloud device", "CLOUD DEVICE EDITED", 1)
	if err := os.WriteFile(path, []byte(tampered), 0644); err != nil {
		t.Fatal(err)
	}
	_, wrote, err := installAgentsMDBlock(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Fatal("without force, existing block should be left untouched")
	}

	// With force the block is refreshed but outer content survives.
	outer := "# Header outside\n\n" + tampered
	if err := os.WriteFile(path, []byte(outer), 0644); err != nil {
		t.Fatal(err)
	}
	_, wrote, err = installAgentsMDBlock(dir, true)
	if err != nil || !wrote {
		t.Fatalf("force install = %v, %v", wrote, err)
	}
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), "CLOUD DEVICE EDITED") {
		t.Fatal("force install did not refresh the block")
	}
	if !strings.Contains(string(data), "# Header outside") {
		t.Fatal("content outside the block was lost")
	}
}

func TestAgentsMDProjectLocationNote(t *testing.T) {
	// Install root IS the project: no note.
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".revyl"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".revyl", "config.yaml"), []byte("project:\n  name: x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if note := agentsMDProjectLocationNote(proj); note != "" {
		t.Fatalf("expected no note when root is the project, got %q", note)
	}

	// Monorepo root with the project in ios/: note names it.
	root := t.TempDir()
	for _, sub := range []string{"ios", "backend"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "ios", ".revyl"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ios", ".revyl", "config.yaml"), []byte("project:\n  name: app\n"), 0644); err != nil {
		t.Fatal(err)
	}
	note := agentsMDProjectLocationNote(root)
	if !strings.Contains(note, "`ios/`") || !strings.Contains(note, "-C ios") {
		t.Fatalf("expected note naming ios/, got %q", note)
	}

	// The rendered block carries the note through install.
	path, wrote, err := installAgentsMDBlock(root, false)
	if err != nil || !wrote {
		t.Fatalf("installAgentsMDBlock() = %v, %v", wrote, err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "The Revyl project lives in `ios/`") {
		t.Fatalf("AGENTS.md missing project location note:\n%s", string(data))
	}

	// No project anywhere: no note.
	if note := agentsMDProjectLocationNote(t.TempDir()); note != "" {
		t.Fatalf("expected no note for empty root, got %q", note)
	}
}
