// Package main provides the test command for test management.
package main

import (
	"github.com/spf13/cobra"
)

// testCmd is the parent command for test management operations.
var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Manage test definitions",
	Long: `Manage local and remote test definitions.

For the common build→test flow, use: revyl run <test-name> (builds then runs).
For run-only or advanced options (hot reload, specific build), use the run subcommand below.

COMMANDS:
  list      - List tests with sync status
  remote    - List all tests in your organization
  push      - Push local test changes to remote
  pull      - Pull remote test changes to local
  diff      - Show diff between local and remote
  validate  - Validate YAML test files
  run       - Run a test (optionally with --build)
  cancel    - Cancel a running test
  create    - Create a new test
  delete    - Delete a test
  open      - Open a test in the browser
  status    - Show latest execution status
  history   - Show execution history
  report    - Show detailed test report
  share     - Generate shareable report link
  env       - Manage app launch environment variables

EXAMPLES:
  revyl run login-flow               # Build and run (recommended)
  revyl test run login-flow          # Run only (no build)
  revyl test run login-flow --build  # Explicit build then run
  revyl test list                    # List tests with sync status
  revyl test status login-flow       # Check latest execution status
  revyl test report login-flow       # View detailed step report`,
}

// testRunCmd runs a single test (run-only by default; use --build to build first).
var testRunCmd = &cobra.Command{
	Use:   "run <name|id>",
	Short: "Run a test by name or ID",
	Long: `Run a test by its alias name (from .revyl/config.yaml) or UUID.

By default runs against the last uploaded build. Use --build to build and
upload first. For the common build→test flow, the shortcut "revyl run <name>"
builds then runs in one command.

Use the test NAME or UUID, not a file path.
  CORRECT: revyl test run login-flow
  WRONG:   revyl test run login-flow.yaml

EXAMPLES:
  revyl run login-flow               # Build then run (shortcut)
  revyl test run login-flow          # Run only (no build)
  revyl test run login-flow --build  # Build then run
  revyl test run login-flow --hotreload --platform ios-dev`,
	Args: cobra.ExactArgs(1),
	RunE: runTestExec,
}

// testCancelCmd cancels a running test.
var testCancelCmd = &cobra.Command{
	Use:   "cancel <task_id>",
	Short: "Cancel a running test",
	Long: `Cancel a running test execution by its task ID.

Task ID is shown when you start a test or in the report URL.`,
	Args: cobra.ExactArgs(1),
	RunE: runCancelTest,
}

// testCreateCmd creates a new test.
var testCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new test",
	Long: `Create a new test and open the editor.

EXAMPLES:
  revyl test create login-flow --platform android
  revyl test create checkout --platform ios`,
	Args: cobra.ExactArgs(1),
	RunE: runCreateTest,
}

// testDeleteCmd deletes a test.
var testDeleteCmd = &cobra.Command{
	Use:   "delete <name|id>",
	Short: "Delete a test",
	Long: `Delete a test from Revyl and remove local files.

By default removes from remote, local .revyl/tests/<name>.yaml, and config alias.
Use --remote-only or --local-only to limit scope.`,
	Args: cobra.ExactArgs(1),
	RunE: runDeleteTest,
}

// testOpenCmd opens a test in the browser.
var testOpenCmd = &cobra.Command{
	Use:   "open <name>",
	Short: "Open a test in the browser",
	Long: `Open a test in your default browser editor.

EXAMPLES:
  revyl test open login-flow
  revyl test open login-flow --hotreload`,
	Args: cobra.ExactArgs(1),
	RunE: runOpenTest,
}

func init() {
	// Add management subcommands
	testCmd.AddCommand(testsListCmd)
	testCmd.AddCommand(testsRemoteCmd)
	testCmd.AddCommand(testsValidateCmd)
	testCmd.AddCommand(testsPushCmd)
	testCmd.AddCommand(testsPullCmd)
	testCmd.AddCommand(testsDiffCmd)
	// Add action subcommands (noun-first)
	testCmd.AddCommand(testRunCmd)
	testCmd.AddCommand(testCancelCmd)
	testCmd.AddCommand(testCreateCmd)
	testCmd.AddCommand(testDeleteCmd)
	testCmd.AddCommand(testOpenCmd)
	// Add status/history/report subcommands
	testCmd.AddCommand(testStatusCmd)
	testCmd.AddCommand(testHistoryCmd)
	testCmd.AddCommand(testReportCmd)
	testCmd.AddCommand(testShareCmd)
	// Add env var management
	testCmd.AddCommand(testEnvCmd)

	// test run flags
	testRunCmd.Flags().IntVarP(&runRetries, "retries", "r", 1, "Number of retry attempts (1-5)")
	testRunCmd.Flags().StringVarP(&runBuildID, "build-id", "b", "", "Specific build version ID")
	testRunCmd.Flags().BoolVar(&runNoWait, "no-wait", false, "Exit after test starts without waiting")
	testRunCmd.Flags().BoolVar(&runOpen, "open", false, "Open report in browser when complete")
	testRunCmd.Flags().IntVarP(&runTimeout, "timeout", "t", 3600, "Timeout in seconds")
	testRunCmd.Flags().BoolVar(&runOutputJSON, "json", false, "Output results as JSON")
	testRunCmd.Flags().BoolVar(&runGitHubActions, "github-actions", false, "Format output for GitHub Actions")
	testRunCmd.Flags().BoolVarP(&runVerbose, "verbose", "v", false, "Show detailed monitoring output")
	testRunCmd.Flags().BoolVar(&runTestBuild, "build", false, "Build and upload before running test")
	testRunCmd.Flags().StringVar(&runTestPlatform, "platform", "", "Platform to use (requires --build, or used with --hotreload)")
	testRunCmd.Flags().StringVar(&runLocation, "location", "", "Initial GPS location as lat,lng (e.g. 37.7749,-122.4194)")
	testRunCmd.Flags().BoolVar(&runHotReload, "hotreload", false, "Enable hot reload mode with local dev server")
	testRunCmd.Flags().IntVar(&runHotReloadPort, "port", 8081, "Port for dev server (used with --hotreload)")
	testRunCmd.Flags().StringVar(&runHotReloadProvider, "provider", "", "Hot reload provider (expo, swift, android)")

	// test cancel flags (inherits global --json)

	// test create flags
	testCreateCmd.Flags().StringVar(&createTestPlatform, "platform", "", "Target platform (android, ios)")
	testCreateCmd.Flags().StringVar(&createTestAppID, "app", "", "App ID to associate with the test")
	testCreateCmd.Flags().BoolVar(&createTestNoOpen, "no-open", false, "Skip opening browser to test editor")
	testCreateCmd.Flags().BoolVar(&createTestNoSync, "no-sync", false, "Skip adding test to .revyl/config.yaml")
	testCreateCmd.Flags().BoolVar(&createTestForce, "force", false, "Update existing test if name already exists")
	testCreateCmd.Flags().BoolVar(&createTestDryRun, "dry-run", false, "Show what would be created without creating")
	testCreateCmd.Flags().StringVar(&createTestFromFile, "from-file", "", "Create test from YAML file (copies to .revyl/tests/ and pushes)")
	testCreateCmd.Flags().BoolVar(&createTestHotReload, "hotreload", false, "Create test with hot reload (adds NAVIGATE step, starts dev server)")
	testCreateCmd.Flags().IntVar(&createTestHotReloadPort, "port", 8081, "Port for dev server (used with --hotreload)")
	testCreateCmd.Flags().StringVar(&createTestHotReloadProvider, "provider", "", "Hot reload provider (expo, swift, android)")
	testCreateCmd.Flags().StringVar(&createTestHotReloadPlatform, "platform-key", "", "Build platform key for hot reload dev client")
	testCreateCmd.Flags().BoolVar(&createTestInteractive, "interactive", false, "Create test interactively with real-time device feedback")
	testCreateCmd.Flags().StringSliceVar(&createTestModules, "module", nil, "Module name or ID to insert as module_import block (can be repeated)")
	testCreateCmd.Flags().StringSliceVar(&createTestTags, "tag", nil, "Tag to assign after creation (can be repeated)")

	// test delete flags
	testDeleteCmd.Flags().BoolVarP(&deleteForce, "force", "f", false, "Skip confirmation prompt")
	testDeleteCmd.Flags().BoolVar(&deleteRemoteOnly, "remote-only", false, "Only delete from remote, keep local files")
	testDeleteCmd.Flags().BoolVar(&deleteLocalOnly, "local-only", false, "Only delete local files, keep remote")

	// test open flags
	testOpenCmd.Flags().BoolVar(&openTestHotReload, "hotreload", false, "Start hot reload mode (dev server + tunnel)")
	testOpenCmd.Flags().IntVar(&openTestHotReloadPort, "port", 8081, "Port for dev server (used with --hotreload)")
	testOpenCmd.Flags().StringVar(&openTestHotReloadProvider, "provider", "", "Hot reload provider (expo, swift, android)")
	testOpenCmd.Flags().StringVar(&openTestHotReloadPlatform, "platform-key", "", "Build platform key for hot reload dev client")
	testOpenCmd.Flags().BoolVar(&openTestInteractive, "interactive", false, "Edit test interactively with real-time device feedback")
	testOpenCmd.Flags().BoolVar(&openTestNoOpen, "no-open", false, "Skip opening browser (with --interactive: output URL and wait for Ctrl+C)")
}
