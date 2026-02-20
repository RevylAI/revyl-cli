// Package main provides sanity tests for the Revyl CLI command initialization.
package main

import (
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
		"version", "auth", "init", "build", "test", "workflow", "sync",
		"docs", "mcp", "schema", "doctor", "ping", "upgrade",
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
