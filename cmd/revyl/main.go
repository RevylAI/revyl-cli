// Package main provides the entry point for the Revyl CLI.
//
// The Revyl CLI is an AI-powered mobile app testing tool that enables
// developers to run tests, manage builds, and create tests interactively.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/tui"
	"github.com/revyl/cli/internal/ui"
)

// Version information set at build time via ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:   "revyl",
	Short: "Proactive reliability for mobile apps",
	Long:  ui.GetHelpText(),
	// Run handles the no-args case. When running in an interactive TTY
	// (no --json, no --quiet, stdout is a terminal), launches the Bubble Tea TUI hub.
	// Otherwise falls back to the condensed cheat-sheet for agents, CI, and pipes.
	Run: func(cmd *cobra.Command, args []string) {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		quiet, _ := cmd.Flags().GetBool("quiet")
		devMode, _ := cmd.Flags().GetBool("dev")

		if tui.ShouldRunTUI(jsonOutput, quiet) {
			if err := tui.RunHub(version, devMode); err != nil {
				fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
				os.Exit(1)
			}
			return
		}

		fmt.Print(ui.GetCondensedHelp())
	},
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		debug, _ := cmd.Flags().GetBool("debug")
		if debug {
			log.SetLevel(log.DebugLevel)
			log.Debug("Debug logging enabled")
		}

		// Set debug mode for UI package
		ui.SetDebugMode(debug)

		// Set quiet mode from global flag
		quiet, _ := cmd.Flags().GetBool("quiet")
		ui.SetQuietMode(quiet)

		// Propagate CLI version to the API package so every client
		// automatically sends the correct User-Agent header.
		api.SetDefaultVersion(version)
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
//
// This function also handles "did you mean" suggestions when users type
// commands in the wrong order (e.g., "revyl open test" instead of "revyl test open").
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		// Check if this is an unknown command error and provide suggestions
		errStr := err.Error()
		if strings.Contains(errStr, "unknown command") {
			// Extract the unknown command from the error message
			// Error format: unknown command "open" for "revyl"
			if start := strings.Index(errStr, `unknown command "`); start != -1 {
				start += len(`unknown command "`)
				if end := strings.Index(errStr[start:], `"`); end != -1 {
					unknownCmd := errStr[start : start+end]

					// Get the original args (skip program name)
					args := os.Args[1:]

					// Try to suggest a correct command
					if suggestion, found := suggestCorrectCommand(unknownCmd, args, rootCmd); found {
						printCommandSuggestion(suggestion)
					}
				}
			}
		}
		os.Exit(1)
	}
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug logging")
	rootCmd.PersistentFlags().Bool("dev", false, "Use local development servers (reads PORT from .env files)")
	rootCmd.PersistentFlags().Bool("json", false, "Output results as JSON (where supported)")
	rootCmd.PersistentFlags().BoolP("quiet", "q", false, "Suppress non-essential output")

	// Add subcommands
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(authCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(appCmd)
	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(testCmd)
	rootCmd.AddCommand(workflowCmd)
	rootCmd.AddCommand(moduleCmd)
	rootCmd.AddCommand(tagCmd)
	rootCmd.AddCommand(docsCmd)
	rootCmd.AddCommand(mcpCmd)
	rootCmd.AddCommand(schemaCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(pingCmd)
	rootCmd.AddCommand(upgradeCmd)
	rootCmd.AddCommand(hotreloadCmd)

	// App store publishing
	rootCmd.AddCommand(publishCmd)

	// Service session management
	rootCmd.AddCommand(servicesCmd)

	// Shell completion (built-in Cobra support for bash, zsh, fish, powershell)
	rootCmd.AddCommand(completionCmd)

	// Internal/dev commands (gated behind --dev flag)
	rootCmd.AddCommand(sandboxCmd)

	// Device interaction commands
	rootCmd.AddCommand(deviceCmd)
}

// completionCmd generates shell completion scripts.
var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate shell completion scripts",
	Long: `Generate shell completion scripts for bash, zsh, fish, or powershell.

EXAMPLES:
  # Bash (add to ~/.bashrc):
  source <(revyl completion bash)

  # Zsh (add to ~/.zshrc):
  source <(revyl completion zsh)

  # Fish:
  revyl completion fish | source

  # PowerShell:
  revyl completion powershell | Out-String | Invoke-Expression`,
	ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
	Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return rootCmd.GenBashCompletion(os.Stdout)
		case "zsh":
			return rootCmd.GenZshCompletion(os.Stdout)
		case "fish":
			return rootCmd.GenFishCompletion(os.Stdout, true)
		case "powershell":
			return rootCmd.GenPowerShellCompletionWithDesc(os.Stdout)
		default:
			return nil
		}
	},
}

// versionCmd shows version information.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")
		if jsonOutput {
			data, err := json.MarshalIndent(map[string]string{
				"version": version,
				"commit":  commit,
				"date":    date,
			}, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal version info: %w", err)
			}
			fmt.Println(string(data))
			return nil
		}

		ui.PrintBanner(version)
		ui.PrintInfo("Version: %s", version)
		ui.PrintInfo("Commit: %s", commit)
		ui.PrintInfo("Built: %s", date)
		return nil
	},
}

// docsCmd opens the documentation in the browser.
var docsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Open Revyl documentation in browser",
	Run: func(cmd *cobra.Command, args []string) {
		docsURL := "https://docs.revyl.com"
		ui.PrintInfo("Opening documentation: %s", docsURL)
		if err := ui.OpenBrowser(docsURL); err != nil {
			ui.PrintError("Failed to open browser: %v", err)
		}
	},
}

func main() {
	Execute()
}
