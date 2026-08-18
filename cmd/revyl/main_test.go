// Package main provides sanity tests for the Revyl CLI command initialization.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRootCommandInitialization verifies that the root command exists and has all expected subcommands.
//
// This test ensures that all CLI commands are properly registered during initialization,
// catching any issues with command registration early in the development cycle.
func TestRootCommandInitialization(t *testing.T) {
	// Verify root command exists
	if rootCmd == nil {
		t.Fatal("rootCmd is nil")
	}

	// List of all expected root subcommands (noun-first: test/workflow/build have run, cancel, create, delete, open as subcommands)
	expectedCommands := []string{
		"version", "auth", "init", "build", "test", "workflow", "config", "sync",
		"docs", "mcp", "schema", "doctor", "ping", "upgrade", "dev",
	}

	// Check each expected command is registered
	for _, name := range expectedCommands {
		found := false
		for _, cmd := range rootCmd.Commands() {
			if cmd.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected command %q not found", name)
		}
	}
}

// TestGlobalFlagsExist verifies that all expected global flags are registered on the root command.
//
// Global flags should be available to all subcommands and are critical for
// consistent CLI behavior (debug mode, JSON output, quiet mode, etc.).
func TestGlobalFlagsExist(t *testing.T) {
	// List of all expected global flags
	flags := []string{"debug", "dev", "json", "quiet"}

	// Check each expected flag is registered
	for _, name := range flags {
		if rootCmd.PersistentFlags().Lookup(name) == nil {
			t.Errorf("expected global flag %q not found", name)
		}
	}
}

// TestRootVersionFlagExists verifies the built-in --version flag is enabled.
func TestRootVersionFlagExists(t *testing.T) {
	if rootCmd.Version == "" {
		t.Fatal("expected root command version to be set")
	}

	rootCmd.InitDefaultVersionFlag()
	if rootCmd.Flags().Lookup("version") == nil {
		t.Fatal("expected --version flag to exist on root command")
	}
}

func TestVersionCommandPrintsStdoutTemplate(t *testing.T) {
	var stdout strings.Builder
	versionCmd.SetOut(&stdout)
	if err := rootCmd.PersistentFlags().Set("json", "false"); err != nil {
		t.Fatalf("clear json flag: %v", err)
	}
	if err := versionCmd.RunE(versionCmd, nil); err != nil {
		t.Fatalf("version command: %v", err)
	}
	got := stdout.String()
	want := "revyl version " + version + "\n"
	if got != want {
		t.Fatalf("version stdout = %q, want %q", got, want)
	}
}

func TestVersionJSONUnchanged(t *testing.T) {
	var stdout strings.Builder
	versionCmd.SetOut(&stdout)
	if err := rootCmd.PersistentFlags().Set("json", "true"); err != nil {
		t.Fatalf("set json flag: %v", err)
	}
	t.Cleanup(func() {
		_ = rootCmd.PersistentFlags().Set("json", "false")
	})
	if err := versionCmd.RunE(versionCmd, nil); err != nil {
		t.Fatalf("version --json: %v", err)
	}
	if !strings.Contains(stdout.String(), `"version"`) {
		t.Fatalf("version --json stdout = %q, want a version object", stdout.String())
	}
	if strings.Contains(stdout.String(), "revyl version ") {
		t.Fatal("version --json printed the human template")
	}
}

func TestShouldSkipVersionCheckForVersionFlagAndMCP(t *testing.T) {
	rootCmd.InitDefaultVersionFlag()
	if err := rootCmd.Flags().Set("version", "true"); err != nil {
		t.Fatalf("set version flag: %v", err)
	}
	t.Cleanup(func() {
		_ = rootCmd.Flags().Set("version", "false")
	})
	if !shouldSkipVersionCheck(rootCmd) {
		t.Fatal("root --version should skip the upgrade notice")
	}

	mcpServe, _, err := rootCmd.Find([]string{"mcp", "serve"})
	if err != nil {
		t.Fatalf("find mcp serve: %v", err)
	}
	if !shouldSkipVersionCheck(mcpServe) {
		t.Fatal("mcp serve should skip the upgrade notice")
	}
}

// TestRootCommandHasUse verifies the root command has the correct Use field.
func TestRootCommandHasUse(t *testing.T) {
	if rootCmd.Use != "revyl" {
		t.Errorf("expected root command Use to be 'revyl', got %q", rootCmd.Use)
	}
}

// TestSubcommandsHaveShortDescription verifies all subcommands have a Short description.
//
// Short descriptions are displayed in help output and are important for usability.
func TestSubcommandsHaveShortDescription(t *testing.T) {
	for _, cmd := range rootCmd.Commands() {
		if cmd.Short == "" {
			t.Errorf("command %q is missing Short description", cmd.Name())
		}
	}
}

func TestPublishCommandRemoved(t *testing.T) {
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "publish" {
			t.Fatal("expected publish command to be removed")
		}
	}
}

func TestResolveCLIVersionFromCandidates_UsesInjectedVersion(t *testing.T) {
	got := resolveCLIVersionFromCandidates("1.2.3", []string{"/tmp/does-not-exist"})
	if got != "1.2.3" {
		t.Fatalf("resolveCLIVersionFromCandidates() = %q, want %q", got, "1.2.3")
	}
}

func TestResolveCLIVersionFromCandidates_UsesVersionFileFallback(t *testing.T) {
	tmpDir := t.TempDir()
	versionPath := filepath.Join(tmpDir, "VERSION")
	if err := os.WriteFile(versionPath, []byte("0.1.5\n"), 0o644); err != nil {
		t.Fatalf("write VERSION file: %v", err)
	}

	got := resolveCLIVersionFromCandidates("dev", []string{versionPath})
	if got != "0.1.5" {
		t.Fatalf("resolveCLIVersionFromCandidates() = %q, want %q", got, "0.1.5")
	}
}

func TestResolveCLIVersionFromCandidates_FallsBackToDevWhenUnknown(t *testing.T) {
	got := resolveCLIVersionFromCandidates("", []string{"/tmp/does-not-exist"})
	if got != "dev" {
		t.Fatalf("resolveCLIVersionFromCandidates() = %q, want %q", got, "dev")
	}
}
