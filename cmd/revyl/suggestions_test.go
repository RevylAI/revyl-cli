// Package main provides tests for command suggestion functionality.
package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// createTestRootCmd creates a mock root command for testing.
func createTestRootCmd() *cobra.Command {
	root := &cobra.Command{Use: "revyl"}

	testCmd := &cobra.Command{Use: "test"}
	testCmd.AddCommand(&cobra.Command{Use: "open"})
	testCmd.AddCommand(&cobra.Command{Use: "run"})
	testCmd.AddCommand(&cobra.Command{Use: "create"})

	workflowCmd := &cobra.Command{Use: "workflow"}
	workflowCmd.AddCommand(&cobra.Command{Use: "open"})
	workflowCmd.AddCommand(&cobra.Command{Use: "run"})
	workflowCmd.AddCommand(&cobra.Command{Use: "create"})

	hotreloadCmd := &cobra.Command{Use: "hotreload"}
	hotreloadCmd.AddCommand(&cobra.Command{Use: "setup"})

	root.AddCommand(testCmd)
	root.AddCommand(workflowCmd)
	root.AddCommand(hotreloadCmd)

	return root
}

func TestSuggestCorrectCommand(t *testing.T) {
	rootCmd := createTestRootCmd()

	tests := []struct {
		name           string
		unknownCmd     string
		allArgs        []string
		wantSuggestion string
		wantFound      bool
	}{
		{
			name:           "open test with flags",
			unknownCmd:     "open",
			allArgs:        []string{"--dev", "open", "test", "peptide-view", "--interactive", "--hotreload", "--platform", "ios-dev"},
			wantSuggestion: "revyl --dev test open peptide-view --interactive --hotreload --platform ios-dev",
			wantFound:      true,
		},
		{
			name:           "open test simple",
			unknownCmd:     "open",
			allArgs:        []string{"open", "test", "my-test"},
			wantSuggestion: "revyl test open my-test",
			wantFound:      true,
		},
		{
			name:           "run workflow",
			unknownCmd:     "run",
			allArgs:        []string{"run", "workflow", "my-workflow"},
			wantSuggestion: "revyl workflow run my-workflow",
			wantFound:      true,
		},
		{
			name:           "create test with platform flag",
			unknownCmd:     "create",
			allArgs:        []string{"create", "test", "new-test", "--platform", "android"},
			wantSuggestion: "revyl test create new-test --platform android",
			wantFound:      true,
		},
		{
			name:           "setup hotreload",
			unknownCmd:     "setup",
			allArgs:        []string{"setup", "hotreload"},
			wantSuggestion: "revyl hotreload setup",
			wantFound:      true,
		},
		{
			name:           "unknown command - no suggestion",
			unknownCmd:     "foobar",
			allArgs:        []string{"foobar", "test"},
			wantSuggestion: "",
			wantFound:      false,
		},
		{
			name:           "subcommand without parent - no suggestion",
			unknownCmd:     "open",
			allArgs:        []string{"open", "my-test"},
			wantSuggestion: "",
			wantFound:      false,
		},
		{
			name:           "open workflow",
			unknownCmd:     "open",
			allArgs:        []string{"open", "workflow", "my-workflow"},
			wantSuggestion: "revyl workflow open my-workflow",
			wantFound:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSuggestion, gotFound := suggestCorrectCommand(tt.unknownCmd, tt.allArgs, rootCmd)

			if gotFound != tt.wantFound {
				t.Errorf("suggestCorrectCommand() found = %v, want %v", gotFound, tt.wantFound)
			}

			if gotSuggestion != tt.wantSuggestion {
				t.Errorf("suggestCorrectCommand() suggestion = %q, want %q", gotSuggestion, tt.wantSuggestion)
			}
		})
	}
}

func TestSuggestCorrectCommand_EdgeCases(t *testing.T) {
	rootCmd := createTestRootCmd()

	tests := []struct {
		name           string
		unknownCmd     string
		allArgs        []string
		wantSuggestion string
		wantFound      bool
	}{
		{
			name:           "empty args",
			unknownCmd:     "open",
			allArgs:        []string{},
			wantSuggestion: "",
			wantFound:      false,
		},
		{
			name:           "unknown cmd not in args",
			unknownCmd:     "open",
			allArgs:        []string{"test", "my-test"},
			wantSuggestion: "",
			wantFound:      false,
		},
		{
			name:           "parent command before subcommand (correct order)",
			unknownCmd:     "test",
			allArgs:        []string{"test", "open", "my-test"},
			wantSuggestion: "",
			wantFound:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSuggestion, gotFound := suggestCorrectCommand(tt.unknownCmd, tt.allArgs, rootCmd)

			if gotFound != tt.wantFound {
				t.Errorf("suggestCorrectCommand() found = %v, want %v", gotFound, tt.wantFound)
			}

			if gotSuggestion != tt.wantSuggestion {
				t.Errorf("suggestCorrectCommand() suggestion = %q, want %q", gotSuggestion, tt.wantSuggestion)
			}
		})
	}
}
