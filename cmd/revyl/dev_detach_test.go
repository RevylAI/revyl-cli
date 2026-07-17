package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spf13/cobra"
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
	cmd := &cobra.Command{Use: "dev"}
	cmd.Flags().BoolVar(&devStartOpen, "open", true, "")

	if !shouldAutoOpenViewer(cmd, dir) {
		t.Fatal("default should auto-open")
	}

	devStartNoOpen = true
	if shouldAutoOpenViewer(cmd, dir) {
		t.Fatal("--no-open must disable auto-open")
	}
	devStartNoOpen = false

	t.Setenv("CI", "true")
	if shouldAutoOpenViewer(cmd, dir) {
		t.Fatal("CI must disable auto-open")
	}
	t.Setenv("CI", "")

	t.Setenv("SSH_CONNECTION", "10.0.0.1 22")
	if shouldAutoOpenViewer(cmd, dir) {
		t.Fatal("SSH session must disable auto-open")
	}
	t.Setenv("SSH_CONNECTION", "")

	if err := os.MkdirAll(filepath.Join(dir, ".revyl"), 0755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, ".revyl", "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("project:\n  name: x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !shouldAutoOpenViewer(cmd, dir) {
		t.Fatal("omitted defaults.open_browser should auto-open for dev")
	}

	// Explicit open_browser: false in the project config disables it.
	cfgYAML := "project:\n  name: x\ndefaults:\n  open_browser: false\n"
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0644); err != nil {
		t.Fatal(err)
	}
	if shouldAutoOpenViewer(cmd, dir) {
		t.Fatal("explicit open_browser: false must disable auto-open")
	}

	if err := cmd.Flags().Set("open", "true"); err != nil {
		t.Fatal(err)
	}
	if !shouldAutoOpenViewer(cmd, dir) {
		t.Fatal("explicit --open must override project config")
	}
}

func TestPrintDetachHandshake_ReportsOpenedBrowser(t *testing.T) {
	prevJSON, prevNoOpen, prevOpen := devStartJSON, devStartNoOpen, devStartOpen
	prevOpenBrowser := openDetachedDevBrowser
	t.Cleanup(func() {
		devStartJSON, devStartNoOpen, devStartOpen = prevJSON, prevNoOpen, prevOpen
		openDetachedDevBrowser = prevOpenBrowser
	})
	devStartJSON, devStartNoOpen, devStartOpen = true, false, true
	t.Setenv("CI", "")
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("DISPLAY", ":0")

	var openedURL string
	openDetachedDevBrowser = func(rawURL string) error {
		openedURL = rawURL
		return nil
	}

	cmd := &cobra.Command{Use: "dev"}
	cmd.Flags().BoolVar(&devStartOpen, "open", true, "")
	devCtx := &DevContext{
		Name:         "default",
		PID:          42,
		SessionID:    "session-1",
		SessionIndex: 3,
		ViewerURL:    "https://viewer.example",
	}

	output := captureStdout(t, func() {
		printDetachHandshake(cmd, t.TempDir(), devCtx, "/tmp/detach.log")
	})

	var handshake devDetachHandshake
	if err := json.Unmarshal([]byte(output), &handshake); err != nil {
		t.Fatalf("parse handshake: %v\noutput: %s", err, output)
	}
	if !handshake.OpenedBrowser {
		t.Fatal("opened_browser = false, want true")
	}
	if openedURL != devCtx.ViewerURL {
		t.Fatalf("opened URL = %q, want %q", openedURL, devCtx.ViewerURL)
	}
}
