// Package main provides command suggestion functionality for the CLI.
//
// This file implements "did you mean" suggestions when users type commands
// in the wrong order (e.g., "revyl open test" instead of "revyl test open").
package main

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/ui"
)

// subcommandMap maps subcommand names to their parent commands.
// This is used to suggest the correct command when users type commands
// in the wrong order.
//
// Example: "open" -> ["test", "workflow"] means "open" is a subcommand
// of both "test" and "workflow".
var subcommandMap = map[string][]string{
	"open":     {"test", "workflow"},
	"run":      {"test", "workflow"},
	"create":   {"test", "workflow", "app"},
	"delete":   {"test", "workflow", "app"},
	"cancel":   {"test", "workflow"},
	"list":     {"test", "app"},
	"remote":   {"test"},
	"push":     {"test"},
	"pull":     {"test"},
	"diff":     {"test"},
	"validate": {"test"},
	"setup":    {"hotreload"},
}

// suggestCorrectCommand checks if the user typed a subcommand at the wrong level
// and returns a suggestion if found.
//
// This function analyzes the command line arguments to detect when a user has
// typed a subcommand before its parent command (e.g., "open test" instead of
// "test open") and constructs the correct command order.
//
// Parameters:
//   - unknownCmd: The command that was not recognized by Cobra
//   - allArgs: All command line arguments (excluding program name)
//   - rootCmd: The root command to search for valid parent commands
//
// Returns:
//   - string: A suggested command string with correct order, or empty if no suggestion found
//   - bool: True if a valid suggestion was found
//
// Example:
//
//	unknownCmd: "open"
//	allArgs: ["--dev", "open", "test", "peptide-view", "--interactive"]
//	Returns: "revyl --dev test open peptide-view --interactive", true
func suggestCorrectCommand(unknownCmd string, allArgs []string, rootCmd *cobra.Command) (string, bool) {
	// Check if the unknown command is a known subcommand
	parentCmds, isSubcommand := subcommandMap[unknownCmd]
	if !isSubcommand {
		return "", false
	}

	// Find the position of the unknown command in args
	unknownCmdIdx := -1
	for i, arg := range allArgs {
		if arg == unknownCmd {
			unknownCmdIdx = i
			break
		}
	}

	if unknownCmdIdx == -1 {
		return "", false
	}

	// Check if any of the args after the unknown command is a valid parent command
	for i := unknownCmdIdx + 1; i < len(allArgs); i++ {
		arg := allArgs[i]

		// Skip flags and their values
		if strings.HasPrefix(arg, "-") {
			continue
		}

		for _, parentCmd := range parentCmds {
			if arg == parentCmd {
				// Verify the parent command exists
				for _, cmd := range rootCmd.Commands() {
					if cmd.Name() == parentCmd {
						// Build the suggested command
						// We need to:
						// 1. Keep flags before the unknown command
						// 2. Insert parent command, then subcommand
						// 3. Add remaining args (excluding the parent command we found)

						var parts []string
						parts = append(parts, "revyl")

						// Add flags/args before the unknown command
						for j := 0; j < unknownCmdIdx; j++ {
							parts = append(parts, allArgs[j])
						}

						// Add parent command and subcommand
						parts = append(parts, parentCmd, unknownCmd)

						// Add args between unknown command and parent command (these are likely the target name)
						for j := unknownCmdIdx + 1; j < i; j++ {
							parts = append(parts, allArgs[j])
						}

						// Add args after the parent command
						for j := i + 1; j < len(allArgs); j++ {
							parts = append(parts, allArgs[j])
						}

						return strings.Join(parts, " "), true
					}
				}
			}
		}
	}

	return "", false
}

// printCommandSuggestion prints a "did you mean" suggestion to the user.
//
// This function formats and displays the suggested command in a user-friendly
// way using the UI package's styling.
//
// Parameters:
//   - suggestion: The suggested command string to display
func printCommandSuggestion(suggestion string) {
	ui.Println()
	ui.PrintInfo("Did you mean:")
	ui.PrintDim("  %s", suggestion)
	ui.Println()
}
