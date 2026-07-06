package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFilterDetachArgs(t *testing.T) {
	got := filterDetachArgs([]string{
		"dev", "--remote", "--detach", "--json", "--platform", "ios",
		"--open=true", "--detach=true", "--json=false", "--timeout", "600",
	})
	want := []string{"dev", "--remote", "--platform", "ios", "--timeout", "600"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterDetachArgs() = %v, want %v", got, want)
	}
}

func TestDevStatusOutputReady(t *testing.T) {
	if devStatusOutputReady(map[string]interface{}{"running": false}) {
		t.Fatal("not running should not be ready")
	}
	if devStatusOutputReady(map[string]interface{}{"running": true, "session_id": ""}) {
		t.Fatal("running without session should not be ready")
	}
	if !devStatusOutputReady(map[string]interface{}{"running": true, "session_id": "sess-1"}) {
		t.Fatal("running with session should be ready")
	}
}

func TestShouldAutoOpenViewer(t *testing.T) {
	// Ensure a clean flag/env baseline and restore after.
	prevNoOpen, prevOpen := devStartNoOpen, devStartOpen
	t.Cleanup(func() { devStartNoOpen, devStartOpen = prevNoOpen, prevOpen })
	devStartNoOpen, devStartOpen = false, true
	t.Setenv("CI", "")
	t.Setenv("SSH_CONNECTION", "")
	// CI runners are headless Linux; satisfy the no-display guard so the
	// default case behaves the same on every platform.
	t.Setenv("DISPLAY", ":0")

	dir := t.TempDir()
	if !shouldAutoOpenViewer(dir) {
		t.Fatal("default should auto-open")
	}

	devStartNoOpen = true
	if shouldAutoOpenViewer(dir) {
		t.Fatal("--no-open must disable auto-open")
	}
	devStartNoOpen = false

	t.Setenv("CI", "true")
	if shouldAutoOpenViewer(dir) {
		t.Fatal("CI must disable auto-open")
	}
	t.Setenv("CI", "")

	t.Setenv("SSH_CONNECTION", "10.0.0.1 22")
	if shouldAutoOpenViewer(dir) {
		t.Fatal("SSH session must disable auto-open")
	}
	t.Setenv("SSH_CONNECTION", "")

	// Explicit open_browser: false in the project config disables it.
	if err := os.MkdirAll(filepath.Join(dir, ".revyl"), 0755); err != nil {
		t.Fatal(err)
	}
	cfgYAML := "project:\n  name: x\ndefaults:\n  open_browser: false\n"
	if err := os.WriteFile(filepath.Join(dir, ".revyl", "config.yaml"), []byte(cfgYAML), 0644); err != nil {
		t.Fatal(err)
	}
	if shouldAutoOpenViewer(dir) {
		t.Fatal("explicit open_browser: false must disable auto-open")
	}
}
