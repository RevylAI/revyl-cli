// Package main provides tests management commands.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/sync"
	"github.com/revyl/cli/internal/ui"
	"github.com/revyl/cli/internal/yaml"
)

var (
	testsForce         bool
	testsLimit         int
	testsPlatform      string
	validateOutputJSON bool
	testsListJSON      bool
	testsRemoteJSON    bool
	testsPushDryRun    bool
	testsPullDryRun    bool
)

func init() {
	// Configure flags for subcommands
	testsPushCmd.Flags().BoolVar(&testsForce, "force", false, "Force overwrite remote")
	testsPushCmd.Flags().BoolVar(&testsPushDryRun, "dry-run", false, "Show what would be pushed without pushing")

	testsPullCmd.Flags().BoolVar(&testsForce, "force", false, "Force overwrite local")
	testsPullCmd.Flags().BoolVar(&testsPullDryRun, "dry-run", false, "Show what would be pulled without pulling")

	testsRemoteCmd.Flags().IntVar(&testsLimit, "limit", 50, "Maximum number of tests to return")
	testsRemoteCmd.Flags().StringVar(&testsPlatform, "platform", "", "Filter by platform (android, ios)")
	testsRemoteCmd.Flags().BoolVar(&testsRemoteJSON, "json", false, "Output results as JSON")

	testsListCmd.Flags().BoolVar(&testsListJSON, "json", false, "Output results as JSON")

	testsValidateCmd.Flags().BoolVar(&validateOutputJSON, "output", false, "Output results as JSON")
}

var testsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List tests with sync status",
	Long: `List all tests showing local and remote versions.

Shows sync status:
  synced      - Local and remote are in sync
  modified    - Local has changes not pushed
  outdated    - Remote has changes not pulled
  local-only  - Test exists only locally
  remote-only - Test exists only on remote`,
	RunE: runTestsList,
}

// testsPushCmd pushes local changes to remote.
var testsPushCmd = &cobra.Command{
	Use:   "push [name]",
	Short: "Push local changes to remote",
	Long: `Push local test changes to the Revyl server.

If a test name is provided, only that test is pushed.
Otherwise, all modified tests are pushed.

Examples:
  revyl test push              # Push all modified tests
  revyl test push login-flow   # Push specific test
  revyl test push --force      # Force overwrite remote`,
	RunE: runTestsPush,
}

// testsPullCmd pulls remote changes to local.
var testsPullCmd = &cobra.Command{
	Use:   "pull [name]",
	Short: "Pull remote changes to local",
	Long: `Pull test changes from the Revyl server.

If a test name is provided, only that test is pulled.
Otherwise, all outdated tests are pulled.

Examples:
  revyl test pull              # Pull all outdated tests
  revyl test pull login-flow   # Pull specific test
  revyl test pull --force      # Force overwrite local`,
	RunE: runTestsPull,
}

// testsDiffCmd shows diff between local and remote.
var testsDiffCmd = &cobra.Command{
	Use:   "diff <name>",
	Short: "Show diff between local and remote",
	Long:  `Show the differences between local and remote versions of a test.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runTestsDiff,
}

// testsRemoteCmd lists all tests in the organization.
var testsRemoteCmd = &cobra.Command{
	Use:   "remote",
	Short: "List all tests in your organization",
	Long: `List all tests available in your Revyl organization.

This shows all tests regardless of local project configuration.
Useful for discovering tests or working without a local .revyl/config.yaml.

Examples:
  revyl test remote                  # List all tests
  revyl test remote --limit 20       # Limit results
  revyl test remote --platform ios   # Filter by platform`,
	RunE: runTestsRemote,
}

// testsValidateCmd validates YAML test files.
var testsValidateCmd = &cobra.Command{
	Use:   "validate <file> [files...]",
	Short: "Validate YAML test files (dry-run)",
	Long: `Validate YAML test files without creating or running them.

This command checks the YAML syntax and schema compliance, reporting
any errors or warnings. Use this to verify test files before committing
or running them.

VALIDATES:
  - YAML syntax
  - Required fields (name, platform, build.name, blocks)
  - Block type validity (instructions, validation, extraction, manual, if, while, code_execution)
  - Manual step_type validity (wait, open_app, kill_app, go_home, navigate, set_location)
  - Variable definitions before use ({{variable-name}} syntax)
  - Platform values (ios/android only)

EXIT CODES:
  0 - All files valid
  1 - One or more files invalid

EXAMPLES:
  revyl test validate test.yaml           # Validate single file
  revyl test validate tests/*.yaml        # Validate multiple files
  revyl test validate --output test.yaml  # JSON output for CI/CD`,
	Args: cobra.MinimumNArgs(1),
	RunE: runTestsValidate,
}

// runTestsList lists tests with sync status.
func runTestsList(cmd *cobra.Command, args []string) error {
	// Check if --json flag is set (either local or global)
	jsonOutput := testsListJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds == nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Load project config
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	cfg, err := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))
	if err != nil {
		ui.PrintError("Project not initialized. Run 'revyl init' first.")
		return err
	}

	// Load local tests
	testsDir := filepath.Join(cwd, ".revyl", "tests")
	localTests, err := config.LoadLocalTests(testsDir)
	if err != nil {
		if !jsonOutput {
			ui.PrintWarning("Could not load local tests: %v", err)
		}
		localTests = make(map[string]*config.LocalTest)
	}

	// Fetch remote test info
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(creds.APIKey, devMode)
	resolver := sync.NewResolver(client, cfg, localTests)

	if !jsonOutput {
		ui.StartSpinner("Fetching test status...")
	}
	statuses, err := resolver.GetAllStatuses(cmd.Context())
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to fetch test status: %v", err)
		return err
	}

	if jsonOutput {
		// Output as JSON
		output := make([]map[string]interface{}, 0, len(statuses))
		for _, s := range statuses {
			item := map[string]interface{}{
				"name":           s.Name,
				"status":         s.Status.String(),
				"local_version":  s.LocalVersion,
				"remote_version": s.RemoteVersion,
				"last_sync":      s.LastSync,
			}
			output = append(output, item)
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(statuses) == 0 {
		ui.PrintInfo("No tests found")
		ui.PrintInfo("Add test aliases to .revyl/config.yaml or run 'revyl test create <name>' to create tests")
		return nil
	}

	ui.Println()

	// Create table with dynamic column widths
	table := ui.NewTable("NAME", "STATUS", "LOCAL", "REMOTE", "LAST SYNC")
	table.SetMinWidth(0, 15) // NAME
	table.SetMinWidth(1, 10) // STATUS

	for _, s := range statuses {
		localVer := "-"
		if s.LocalVersion > 0 {
			localVer = fmt.Sprintf("v%d", s.LocalVersion)
		}
		remoteVer := "-"
		if s.RemoteVersion > 0 {
			remoteVer = fmt.Sprintf("v%d", s.RemoteVersion)
		}
		table.AddRow(s.Name, s.Status.String(), localVer, remoteVer, s.LastSync)
	}

	table.Render()
	return nil
}

// runTestsPush pushes local changes to remote.
func runTestsPush(cmd *cobra.Command, args []string) error {
	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds == nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Load project config
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	cfg, err := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))
	if err != nil {
		ui.PrintError("Project not initialized. Run 'revyl init' first.")
		return err
	}

	// Load local tests
	testsDir := filepath.Join(cwd, ".revyl", "tests")
	localTests, err := config.LoadLocalTests(testsDir)
	if err != nil {
		ui.PrintWarning("Could not load local tests: %v", err)
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(creds.APIKey, devMode)
	resolver := sync.NewResolver(client, cfg, localTests)

	var testName string
	if len(args) > 0 {
		testName = args[0]
	}

	// Handle dry-run mode
	if testsPushDryRun {
		ui.StartSpinner("Checking what would be pushed...")
		statuses, err := resolver.GetAllStatuses(cmd.Context())
		ui.StopSpinner()

		if err != nil {
			ui.PrintError("Failed to check status: %v", err)
			return err
		}

		ui.Println()
		ui.PrintInfo("Dry-run mode - showing what would be pushed:")
		ui.Println()

		var toPush []sync.TestSyncStatus
		for _, s := range statuses {
			// Filter by name if specified
			if testName != "" && s.Name != testName {
				continue
			}
			// Only show tests that would be pushed (modified or local-only)
			if s.Status == sync.StatusModified || s.Status == sync.StatusLocalOnly {
				toPush = append(toPush, s)
			}
		}

		if len(toPush) == 0 {
			ui.PrintInfo("No tests to push")
		} else {
			for _, s := range toPush {
				ui.PrintInfo("  %s (%s)", s.Name, s.Status.String())
				if s.LocalVersion > 0 {
					ui.PrintDim("    Local version: v%d", s.LocalVersion)
				}
				if s.RemoteVersion > 0 {
					ui.PrintDim("    Remote version: v%d", s.RemoteVersion)
				}
			}
		}

		ui.Println()
		ui.PrintSuccess("Dry-run complete - no changes made")
		return nil
	}

	ui.StartSpinner("Pushing tests...")
	results, err := resolver.SyncToRemote(cmd.Context(), testName, testsDir, testsForce)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Push failed: %v", err)
		return err
	}

	ui.Println()
	for _, r := range results {
		if r.Error != nil {
			ui.PrintError("%s: %v", r.Name, r.Error)
		} else if r.Conflict {
			ui.PrintWarning("%s: conflict detected (use --force to overwrite)", r.Name)
		} else {
			ui.PrintSuccess("%s: pushed to v%d", r.Name, r.NewVersion)
		}
	}

	return nil
}

// runTestsPull pulls remote changes to local.
func runTestsPull(cmd *cobra.Command, args []string) error {
	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds == nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Load project config
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	cfg, err := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))
	if err != nil {
		ui.PrintError("Project not initialized. Run 'revyl init' first.")
		return err
	}

	testsDir := filepath.Join(cwd, ".revyl", "tests")
	localTests, err := config.LoadLocalTests(testsDir)
	if err != nil {
		localTests = make(map[string]*config.LocalTest)
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(creds.APIKey, devMode)
	resolver := sync.NewResolver(client, cfg, localTests)

	var testName string
	if len(args) > 0 {
		testName = args[0]
	}

	// Handle dry-run mode
	if testsPullDryRun {
		ui.StartSpinner("Checking what would be pulled...")
		statuses, err := resolver.GetAllStatuses(cmd.Context())
		ui.StopSpinner()

		if err != nil {
			ui.PrintError("Failed to check status: %v", err)
			return err
		}

		ui.Println()
		ui.PrintInfo("Dry-run mode - showing what would be pulled:")
		ui.Println()

		var toPull []sync.TestSyncStatus
		for _, s := range statuses {
			// Filter by name if specified
			if testName != "" && s.Name != testName {
				continue
			}
			// Only show tests that would be pulled (outdated or remote-only)
			if s.Status == sync.StatusOutdated || s.Status == sync.StatusRemoteOnly {
				toPull = append(toPull, s)
			}
		}

		if len(toPull) == 0 {
			ui.PrintInfo("No tests to pull")
		} else {
			for _, s := range toPull {
				ui.PrintInfo("  %s (%s)", s.Name, s.Status.String())
				if s.LocalVersion > 0 {
					ui.PrintDim("    Local version: v%d", s.LocalVersion)
				}
				if s.RemoteVersion > 0 {
					ui.PrintDim("    Remote version: v%d", s.RemoteVersion)
				}
			}
		}

		ui.Println()
		ui.PrintSuccess("Dry-run complete - no changes made")
		return nil
	}

	ui.StartSpinner("Pulling tests...")
	results, err := resolver.PullFromRemote(cmd.Context(), testName, testsDir, testsForce)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Pull failed: %v", err)
		return err
	}

	ui.Println()
	for _, r := range results {
		if r.Error != nil {
			ui.PrintError("%s: %v", r.Name, r.Error)
		} else if r.Conflict {
			ui.PrintWarning("%s: local changes would be overwritten (use --force)", r.Name)
		} else {
			ui.PrintSuccess("%s: pulled v%d", r.Name, r.NewVersion)
		}
	}

	return nil
}

// runTestsDiff shows diff between local and remote.
func runTestsDiff(cmd *cobra.Command, args []string) error {
	testName := args[0]

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds == nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Load project config
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	cfg, err := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))
	if err != nil {
		ui.PrintError("Project not initialized. Run 'revyl init' first.")
		return err
	}

	testsDir := filepath.Join(cwd, ".revyl", "tests")
	localTests, err := config.LoadLocalTests(testsDir)
	if err != nil {
		localTests = make(map[string]*config.LocalTest)
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(creds.APIKey, devMode)
	resolver := sync.NewResolver(client, cfg, localTests)

	ui.StartSpinner("Fetching diff...")
	diff, err := resolver.GetDiff(cmd.Context(), testName)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to get diff: %v", err)
		return err
	}

	if diff == "" {
		ui.PrintInfo("No differences found")
		return nil
	}

	ui.Println()
	ui.PrintDiff(diff)

	return nil
}

// runTestsRemote lists all tests in the organization.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (unused)
//
// Returns:
//   - error: Any error that occurred while listing tests
func runTestsRemote(cmd *cobra.Command, args []string) error {
	// Check if --json flag is set (either local or global)
	jsonOutput := testsRemoteJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds == nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Create API client with dev mode support
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(creds.APIKey, devMode)

	if !jsonOutput {
		ui.StartSpinner("Fetching tests from organization...")
	}
	result, err := client.ListOrgTests(cmd.Context(), testsLimit, 0)
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to fetch tests: %v", err)
		return err
	}

	// Filter by platform if specified
	tests := result.Tests
	if testsPlatform != "" {
		filtered := make([]api.SimpleTest, 0)
		for _, t := range tests {
			if t.Platform == testsPlatform {
				filtered = append(filtered, t)
			}
		}
		tests = filtered
	}

	if jsonOutput {
		// Output as JSON
		output := map[string]interface{}{
			"tests": tests,
			"count": len(tests),
			"total": result.Count,
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(result.Tests) == 0 {
		ui.PrintInfo("No tests found in your organization")
		ui.PrintInfo("Create tests at https://app.revyl.ai")
		return nil
	}

	if len(tests) == 0 {
		ui.PrintInfo("No tests found for platform: %s", testsPlatform)
		return nil
	}

	ui.Println()
	ui.PrintInfo("Tests in your organization (%d total):", result.Count)
	ui.Println()

	// Create table with dynamic column widths
	table := ui.NewTable("NAME", "PLATFORM", "ID")
	table.SetMinWidth(0, 25) // NAME - ensure readable width
	table.SetMinWidth(1, 8)  // PLATFORM
	table.SetMinWidth(2, 36) // ID - UUIDs are 36 chars

	for _, t := range tests {
		table.AddRow(t.Name, t.Platform, t.ID)
	}

	table.Render()

	if result.Count > len(tests) {
		ui.Println()
		ui.PrintDim("Showing %d of %d tests. Use --limit to see more.", len(tests), result.Count)
	}

	return nil
}

// runTestsValidate validates YAML test files.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: File paths to validate
//
// Returns:
//   - error: Returns error if any file is invalid
func runTestsValidate(cmd *cobra.Command, args []string) error {
	allValid := true
	var results []map[string]interface{}

	for _, file := range args {
		result, err := yaml.ValidateYAMLFile(file)
		if err != nil {
			if validateOutputJSON {
				results = append(results, map[string]interface{}{
					"file":  file,
					"valid": false,
					"error": err.Error(),
				})
			} else {
				ui.PrintError("%s: %v", file, err)
			}
			allValid = false
			continue
		}

		if validateOutputJSON {
			resultMap := map[string]interface{}{
				"file":  file,
				"valid": result.Valid,
			}
			if len(result.Errors) > 0 {
				resultMap["errors"] = result.Errors
			}
			if len(result.Warnings) > 0 {
				resultMap["warnings"] = result.Warnings
			}
			results = append(results, resultMap)
		} else {
			if result.Valid {
				ui.PrintSuccess("%s: Valid", file)
				for _, w := range result.Warnings {
					ui.PrintWarning("  Warning: %s", w)
				}
			} else {
				ui.PrintError("%s: Invalid", file)
				for _, e := range result.Errors {
					ui.PrintError("  %s", e)
				}
				for _, w := range result.Warnings {
					ui.PrintWarning("  Warning: %s", w)
				}
			}
		}

		if !result.Valid {
			allValid = false
		}
	}

	if validateOutputJSON {
		data, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(data))
	}

	if !allValid {
		return fmt.Errorf("validation failed")
	}
	return nil
}
