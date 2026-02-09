// Package main provides the entry point for the Revyl CLI.
//
// The Revyl CLI is an AI-powered mobile app testing tool that enables
// developers to run tests, manage builds, and create tests interactively.
package main

import (
	"os"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"

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
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		debug, _ := cmd.Flags().GetBool("debug")
		if debug {
			log.SetLevel(log.DebugLevel)
			log.Debug("Debug logging enabled")
		}

		// Set quiet mode from global flag
		quiet, _ := cmd.Flags().GetBool("quiet")
		ui.SetQuietMode(quiet)
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
	rootCmd.AddCommand(runCmd) // build→test shortcut: revyl run <name>
	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(testCmd)
	rootCmd.AddCommand(workflowCmd)
	rootCmd.AddCommand(docsCmd)
	rootCmd.AddCommand(mcpCmd)
	rootCmd.AddCommand(schemaCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(pingCmd)
	rootCmd.AddCommand(upgradeCmd)
	rootCmd.AddCommand(hotreloadCmd)
}

// versionCmd shows version information.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Run: func(cmd *cobra.Command, args []string) {
		ui.PrintBanner(version)
		ui.PrintInfo("Version: %s", version)
		ui.PrintInfo("Commit: %s", commit)
		ui.PrintInfo("Built: %s", date)
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
