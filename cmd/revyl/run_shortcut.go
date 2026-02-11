// Package main provides a root-level run command for build→test/workflow ergonomics.
//
// "revyl run <name>" builds and runs a test by default; "revyl run <name> -w" does
// the same for a workflow, so the common flow (build → upload → run) is a single command.
package main

import (
	"github.com/spf13/cobra"
)

// runShortcutNoBuild is set by the root-level run command to skip the build step.
var runShortcutNoBuild bool

// runShortcutWorkflow is set when --workflow/-w is used to run a workflow instead of a test.
var runShortcutWorkflow bool

// runCmd is the root-level shortcut: build and run a test or workflow in one command.
//
// Defaults to building first (--build implied). Use --no-build to run without rebuilding.
// Use --workflow / -w to run a workflow instead of a test.
var runCmd = &cobra.Command{
	Use:   "run <name>",
	Short: "Build and run a test or workflow",
	Long: `Build your app, upload it, and run the named test or workflow.

This is the recommended way to run after code changes: one command does
build → upload → run. Use --no-build to skip the build step and run against
the last uploaded build. Use -w/--workflow to run a workflow instead of a test.

For more options (hot reload, specific build version, etc.), use:
  revyl test run <name> [flags]
  revyl workflow run <name> [flags]

EXAMPLES:
  revyl run login-flow              # Build, upload, then run test (default)
  revyl run login-flow --no-build   # Run test without rebuilding
  revyl run smoke-tests -w          # Build, upload, then run workflow
  revyl run smoke-tests -w --no-build   # Run workflow without rebuilding
  revyl run login-flow --platform release
  revyl run login-flow --open       # Open report when done`,
	Args: cobra.ExactArgs(1),
	RunE: runShortcutExec,
}

func init() {
	runCmd.Flags().BoolVarP(&runShortcutWorkflow, "workflow", "w", false, "Run a workflow instead of a test")
	runCmd.Flags().BoolVar(&runShortcutNoBuild, "no-build", false, "Skip build step; run against last uploaded build")
	runCmd.Flags().StringVar(&runTestPlatform, "platform", "", "Platform to use (e.g. release, android)")
	runCmd.Flags().BoolVar(&runOpen, "open", false, "Open report in browser when complete")
	runCmd.Flags().IntVarP(&runTimeout, "timeout", "t", 3600, "Timeout in seconds")
	runCmd.Flags().IntVarP(&runRetries, "retries", "r", 1, "Number of retry attempts (1-5)")
	runCmd.Flags().BoolVar(&runOutputJSON, "json", false, "Output results as JSON")
	runCmd.Flags().BoolVarP(&runVerbose, "verbose", "v", false, "Show detailed output")
	runCmd.Flags().BoolVar(&runNoWait, "no-wait", false, "Queue and exit without waiting for result")
	runCmd.Flags().StringVarP(&runBuildID, "build-id", "b", "", "Specific build version ID (skips build step)")
	runCmd.Flags().BoolVar(&runHotReload, "hotreload", false, "Enable hot reload mode with local dev server")
	runCmd.Flags().IntVar(&runHotReloadPort, "port", 8081, "Port for dev server (used with --hotreload)")
	runCmd.Flags().StringVar(&runHotReloadProvider, "provider", "", "Hot reload provider (expo, swift, android)")
}

// runShortcutExec runs the root-level "revyl run <name>" command.
// If --workflow/-w is set, it sets runWorkflowBuild and delegates to runWorkflowExec;
// otherwise it sets runTestBuild and delegates to runTestExec.
func runShortcutExec(cmd *cobra.Command, args []string) error {
	if runShortcutWorkflow {
		runWorkflowBuild = !runShortcutNoBuild
		runWorkflowPlatform = runTestPlatform // --platform applies to workflow build too
		return runWorkflowExec(cmd, args)
	}
	runTestBuild = !runShortcutNoBuild
	return runTestExec(cmd, args)
}
