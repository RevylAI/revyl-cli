// Package main provides the test command for test management.
package main

import (
	"github.com/spf13/cobra"
)

// testCmd is the parent command for test management operations.
var testCmd = &cobra.Command{
	Use:               "test",
	Short:             "Manage test definitions",
	PersistentPreRunE: enforceOrgBindingMatch,
	Long: `Manage local and remote test definitions.

Use 'revyl test run <name>' to run a test, optionally with --build to build first.

COMMANDS:
  list      - List tests with sync status
  remote    - List all tests in your organization
  push      - Push local test changes to remote
  pull      - Pull remote test changes to local
  diff      - Show diff between local and remote
  rename    - Rename a test while preserving history
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
  duplicate - Duplicate an existing test
  versions  - List version history for a test
  restore   - Restore a test to a specific version

EXAMPLES:
  revyl test run login-flow          # Run a test
  revyl test run login-flow --build  # Build then run
  revyl test list                    # List tests with sync status
  revyl test status login-flow       # Check latest execution status
  revyl test report login-flow       # View detailed step report`,
	Example: `  revyl test run login-flow
  revyl test list
  revyl test status login-flow --json
  revyl test report login-flow --json`,
}

// testRunCmd runs a single test (run-only by default; use --build to build first).
var testRunCmd = &cobra.Command{
	Use:   "run <name|id>",
	Short: "Run a test by name or ID",
	Long: `Run a test by its alias name (from .revyl/config.yaml) or UUID.

By default runs against the last uploaded build. Use --build to build and
upload first.

Use the test NAME or UUID, not a file path.
  CORRECT: revyl test run login-flow
  WRONG:   revyl test run login-flow.yaml

EXAMPLES:
  revyl test run login-flow          # Run (no build)
  revyl test run login-flow --build  # Build then run`,
	Example: `  revyl test run login-flow
  revyl test run login-flow --build
  revyl test run login-flow --json --no-wait
  revyl test run login-flow --device-model "iPhone 16" --os-version "iOS 18.5"`,
	Args: cobra.ExactArgs(1),
	RunE: runTestExec,
}

// testCancelCmd cancels a running test.
var testCancelCmd = &cobra.Command{
	Use:   "cancel <task_id>",
	Short: "Cancel a running test",
	Long: `Cancel a running test execution by its task ID.

Task ID is shown when you start a test or in the report URL.`,
	Example: `  revyl test cancel <task-id>`,
	Args:    cobra.ExactArgs(1),
	RunE:    runCancelTest,
}

// testCreateCmd creates a new test.
var testCreateCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Create a new test",
	Long: `Create a new test and open the editor.

When --from-file is used, the name is optional and will be inferred from
the YAML file's test.metadata.name field.

EXAMPLES:
  revyl test create login-flow --platform android
  revyl test create checkout --platform ios
  revyl test create --from-file ./my-test.yaml`,
	Example: `  revyl test create login-flow --platform android
  revyl test create checkout --platform ios
  revyl test create --from-file ./my-test.yaml
  revyl test create login --platform ios --module login-module --tag smoke`,
	Args: func(cmd *cobra.Command, args []string) error {
		fromFile, _ := cmd.Flags().GetString("from-file")
		if fromFile != "" {
			return cobra.RangeArgs(0, 1)(cmd, args)
		}
		return cobra.ExactArgs(1)(cmd, args)
	},
	RunE: runCreateTest,
}

// testRenameCmd renames a test while preserving history.
var testRenameCmd = &cobra.Command{
	Use:   "rename [old-name|id] [new-name]",
	Short: "Rename a test without recreating it",
	Long: `Rename a test while keeping the same remote test ID and execution history.

When called with no args in a TTY, this command prompts for test selection
and the new name.

EXAMPLES:
  revyl test rename CLI-0-onboard-a cli-0-onboard-a
  revyl test rename login-flow smoke-login
  revyl test rename`,
	Example: `  revyl test rename old-name new-name
  revyl test rename old-name new-name --non-interactive`,
	Args: cobra.MaximumNArgs(2),
	RunE: runRenameTest,
}

// testDeleteCmd deletes a test.
var testDeleteCmd = &cobra.Command{
	Use:   "delete <name|id>",
	Short: "Delete a test",
	Long: `Delete a test from Revyl and remove local files.

By default removes from remote, local .revyl/tests/<name>.yaml, and config alias.
Use --remote-only or --local-only to limit scope.`,
	Example: `  revyl test delete login-flow
  revyl test delete login-flow --force
  revyl test delete login-flow --remote-only`,
	Args: cobra.ExactArgs(1),
	RunE: runDeleteTest,
}

// testOpenCmd opens a test in the browser.
var testOpenCmd = &cobra.Command{
	Use:   "open <name>",
	Short: "Open a test in the browser",
	Long: `Open a test in your default browser editor.

EXAMPLES:
  revyl test open login-flow`,
	Example: `  revyl test open login-flow`,
	Args:    cobra.ExactArgs(1),
	RunE:    runOpenTest,
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
	testCmd.AddCommand(testRenameCmd)
	testCmd.AddCommand(testDeleteCmd)
	testCmd.AddCommand(testOpenCmd)
	// Add status/history/report subcommands
	testCmd.AddCommand(testStatusCmd)
	testCmd.AddCommand(testHistoryCmd)
	testCmd.AddCommand(testReportCmd)
	testCmd.AddCommand(testShareCmd)
	// Add env var management
	testCmd.AddCommand(testEnvCmd)
	// Add test variable management
	testCmd.AddCommand(testVarCmd)
	// Add duplication and versioning
	testCmd.AddCommand(testDuplicateCmd)
	testCmd.AddCommand(testVersionsCmd)
	testCmd.AddCommand(testRestoreCmd)

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
	testRunCmd.Flags().StringVar(&runTestPlatform, "platform", "", "Build platform key or ios/android")
	testRunCmd.Flags().StringVar(&runLocation, "location", "", "Initial GPS location as lat,lng (e.g. 37.7749,-122.4194)")
	testRunCmd.Flags().IntVar(&runHotReloadPort, "port", 8081, "Port for local dev server")
	testRunCmd.Flags().StringVar(&runHotReloadProvider, "provider", "", "Hot reload provider (expo, react-native)")
	testRunCmd.Flags().BoolVar(&runDeviceSelect, "device", false, "Interactively select device model and OS version")
	testRunCmd.Flags().StringVar(&runDeviceModel, "device-model", "", "Target device model (e.g. \"iPhone 16\")")
	testRunCmd.Flags().StringVar(&runOsVersion, "os-version", "", "Target OS version (e.g. \"iOS 18.5\")")
	testRunCmd.Flags().StringVar(&runOrientation, "orientation", "", "Initial device orientation (portrait or landscape)")

	// test cancel flags (inherits global --json)

	// test create flags
	testCreateCmd.Flags().StringVar(&createTestPlatform, "platform", "", "Target platform (android, ios)")
	testCreateCmd.Flags().StringVar(&createTestAppID, "app", "", "App ID to associate with the test")
	testCreateCmd.Flags().BoolVar(&createTestNoOpen, "no-open", false, "Skip opening browser to test editor")
	testCreateCmd.Flags().BoolVar(&createTestForce, "force", false, "Update existing test if name already exists")
	testCreateCmd.Flags().BoolVar(&createTestDryRun, "dry-run", false, "Show what would be created without creating")
	testCreateCmd.Flags().StringVar(&createTestFromFile, "from-file", "", "Create test from YAML file (copies to .revyl/tests/ and pushes)")
	testCreateCmd.Flags().IntVar(&createTestHotReloadPort, "port", 8081, "Port for local dev server")
	testCreateCmd.Flags().StringVar(&createTestHotReloadProvider, "provider", "", "Hot reload provider (expo, react-native)")
	testCreateCmd.Flags().BoolVar(&createTestInteractive, "interactive", false, "Create test interactively with real-time device feedback")
	testCreateCmd.Flags().StringSliceVar(&createTestModules, "module", nil, "Module name or ID to insert as module_import block (can be repeated)")
	testCreateCmd.Flags().StringSliceVar(&createTestTags, "tag", nil, "Tag to assign after creation (can be repeated)")

	// test rename flags
	testRenameCmd.Flags().BoolVar(&renameNonInteractive, "non-interactive", false, "Disable prompts; requires both positional args")
	testRenameCmd.Flags().BoolVarP(&renameYes, "yes", "y", false, "Auto-accept default rename prompts")

	// test delete flags
	testDeleteCmd.Flags().BoolVarP(&deleteForce, "force", "f", false, "Skip confirmation prompt")
	testDeleteCmd.Flags().BoolVar(&deleteRemoteOnly, "remote-only", false, "Only delete from remote, keep local files")
	testDeleteCmd.Flags().BoolVar(&deleteLocalOnly, "local-only", false, "Only delete local files, keep remote")

	// test open flags
	testOpenCmd.Flags().IntVar(&openTestHotReloadPort, "port", 8081, "Port for local dev server")
	testOpenCmd.Flags().StringVar(&openTestHotReloadProvider, "provider", "", "Hot reload provider (expo, react-native)")
	testOpenCmd.Flags().BoolVar(&openTestInteractive, "interactive", false, "Edit test interactively with real-time device feedback")
	testOpenCmd.Flags().BoolVar(&openTestNoOpen, "no-open", false, "Skip opening browser (with --interactive: output URL and wait for Ctrl+C)")
}
