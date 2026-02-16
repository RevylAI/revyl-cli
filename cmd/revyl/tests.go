// Package main provides tests management commands.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/sync"
	"github.com/revyl/cli/internal/ui"
	"github.com/revyl/cli/internal/util"
	"github.com/revyl/cli/internal/yaml"
)

var (
	testsForce         bool
	testsLimit         int
	testsPlatform      string
	testsTag           string
	validateOutputJSON bool
	testsListJSON      bool
	testsRemoteJSON    bool
	testsPushDryRun    bool
	testsPullDryRun    bool
	testsPullAll       bool
)

func init() {
	// Configure flags for subcommands
	testsPushCmd.Flags().BoolVar(&testsForce, "force", false, "Force overwrite remote")
	testsPushCmd.Flags().BoolVar(&testsPushDryRun, "dry-run", false, "Show what would be pushed without pushing")

	testsPullCmd.Flags().BoolVar(&testsForce, "force", false, "Force overwrite local")
	testsPullCmd.Flags().BoolVar(&testsPullDryRun, "dry-run", false, "Show what would be pulled without pulling")
	testsPullCmd.Flags().BoolVar(&testsPullAll, "all", false, "Pull all tests from the organization, including those not in local config")

	testsRemoteCmd.Flags().IntVar(&testsLimit, "limit", 50, "Maximum number of tests to return")
	testsRemoteCmd.Flags().StringVar(&testsPlatform, "platform", "", "Filter by platform (android, ios)")
	testsRemoteCmd.Flags().StringVar(&testsTag, "tag", "", "Filter by tag name")
	testsRemoteCmd.Flags().BoolVar(&testsRemoteJSON, "json", false, "Output results as JSON")

	testsListCmd.Flags().BoolVar(&testsListJSON, "json", false, "Output results as JSON")

	testsValidateCmd.Flags().BoolVar(&validateOutputJSON, "json", false, "Output results as JSON")
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
Otherwise, pulls all tests that are in your local config.

Use --all to discover and pull ALL tests from your organization,
including those created in the web UI that aren't in local config yet.

Examples:
  revyl test pull              # Pull all tests in local config
  revyl test pull login-flow   # Pull specific test
  revyl test pull --all        # Pull all org tests (including remote-only)
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

When filtering by tag, a TAGS column is included in the output.

Examples:
  revyl test remote                  # List all tests
  revyl test remote --limit 20       # Limit results
  revyl test remote --platform ios   # Filter by platform
  revyl test remote --tag regression # Filter by tag`,
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
  revyl test validate --json test.yaml  # JSON output for CI/CD`,
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
	apiKey, err := getAPIKey()
	if err != nil {
		return err
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
	client := api.NewClientWithDevMode(apiKey, devMode)
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

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Run a test:", Command: "revyl test run <name>"},
		{Label: "Create a test:", Command: "revyl test create <name>"},
	})

	return nil
}

// runTestsPush pushes local changes to remote.
func runTestsPush(cmd *cobra.Command, args []string) error {
	// Check authentication
	apiKey, err := getAPIKey()
	if err != nil {
		return err
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
	client := api.NewClientWithDevMode(apiKey, devMode)
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
	pushedCount := 0
	for _, r := range results {
		if r.Error != nil {
			ui.PrintError("%s: %v", r.Name, r.Error)
		} else if r.Conflict {
			ui.PrintWarning("%s: conflict detected (use --force to overwrite)", r.Name)
		} else {
			ui.PrintSuccess("%s: pushed to v%d", r.Name, r.NewVersion)
			pushedCount++
		}
	}

	// Update sync timestamp if any tests were pushed successfully.
	if pushedCount > 0 {
		cfg.MarkSynced()
		_ = config.WriteProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"), cfg)
	}

	return nil
}

// runTestsPull pulls remote changes to local.
func runTestsPull(cmd *cobra.Command, args []string) error {
	// Check authentication
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Load project config
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	cfg, err := config.LoadProjectConfig(configPath)
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
	client := api.NewClientWithDevMode(apiKey, devMode)

	// If --all flag is set, discover remote-only tests and add them to config before pulling
	if testsPullAll && len(args) == 0 {
		ui.StartSpinner("Discovering all organization tests...")
		remoteTests, err := client.ListOrgTests(cmd.Context(), 200, 0)
		ui.StopSpinner()

		if err != nil {
			ui.PrintWarning("Failed to fetch remote tests: %v", err)
		} else {
			// Build a reverse lookup of existing config test IDs
			existingIDs := make(map[string]bool)
			for _, id := range cfg.Tests {
				existingIDs[id] = true
			}

			// Also check local tests with remote IDs
			for _, local := range localTests {
				if local.Meta.RemoteID != "" {
					existingIDs[local.Meta.RemoteID] = true
				}
			}

			// Discover remote-only tests and add them to config
			newCount := 0
			if cfg.Tests == nil {
				cfg.Tests = make(map[string]string)
			}
			for _, t := range remoteTests.Tests {
				if !existingIDs[t.ID] {
					sanitizedName := util.SanitizeForFilename(t.Name)
					if sanitizedName == "" {
						sanitizedName = fmt.Sprintf("test-%s", truncatePrefix(t.ID, 8))
					}
					// Handle collisions (two tests that sanitize to the same name)
					finalName := sanitizedName
					for i := 2; cfg.Tests[finalName] != "" && cfg.Tests[finalName] != t.ID; i++ {
						finalName = fmt.Sprintf("%s-%d", sanitizedName, i)
					}
					cfg.Tests[finalName] = t.ID
					newCount++
				}
			}

			if newCount > 0 {
				ui.PrintInfo("Discovered %d new test(s) from organization", newCount)
				// Save updated config with new test mappings
				if err := config.WriteProjectConfig(configPath, cfg); err != nil {
					ui.PrintWarning("Failed to save config with new tests: %v", err)
				}
			}
		}
	}

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
	pulledCount := 0
	for _, r := range results {
		if r.Error != nil {
			ui.PrintError("%s: %v", r.Name, r.Error)
		} else if r.Conflict {
			ui.PrintWarning("%s: local changes would be overwritten (use --force)", r.Name)
		} else {
			ui.PrintSuccess("%s: pulled v%d", r.Name, r.NewVersion)
			pulledCount++
		}
	}

	// Update sync timestamp if any tests were pulled successfully.
	if pulledCount > 0 {
		cfg.MarkSynced()
		_ = config.WriteProjectConfig(configPath, cfg)
	}

	// If not using --all, hint about it when there might be more tests
	if !testsPullAll && testName == "" && pulledCount == 0 {
		ui.Println()
		ui.PrintDim("To pull all tests from your organization (including those not in local config):")
		ui.PrintDim("  revyl test pull --all")
	}

	return nil
}

// runTestsDiff shows diff between local and remote.
func runTestsDiff(cmd *cobra.Command, args []string) error {
	testName := args[0]

	// Check authentication
	apiKey, err := getAPIKey()
	if err != nil {
		return err
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
	client := api.NewClientWithDevMode(apiKey, devMode)
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
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Create API client with dev mode support
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	// When --tag is used, we need the full get_tests endpoint which includes tags
	if testsTag != "" {
		return runTestsRemoteWithTags(cmd, client, jsonOutput)
	}

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

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Run a test:", Command: "revyl test run <name>"},
		{Label: "Create a test:", Command: "revyl test create <name>"},
	})

	return nil
}

// runTestsRemoteWithTags lists tests filtered by tag.
// Uses the full get_tests endpoint which returns tag data per test.
func runTestsRemoteWithTags(cmd *cobra.Command, client *api.Client, jsonOutput bool) error {
	if !jsonOutput {
		ui.StartSpinner("Fetching tests with tags...")
	}
	// Fetch all tests since we filter client-side by tag.
	// Backend max limit is 200, so paginate if needed.
	const pageSize = 200
	var allTests []api.TestWithTags
	offset := 0
	for {
		page, fetchErr := client.ListOrgTestsWithTags(cmd.Context(), pageSize, offset)
		if fetchErr != nil {
			if !jsonOutput {
				ui.StopSpinner()
			}
			ui.PrintError("Failed to fetch tests: %v", fetchErr)
			return fetchErr
		}
		allTests = append(allTests, page.Tests...)
		if len(page.Tests) < pageSize {
			break
		}
		offset += pageSize
	}
	result := &api.CLITestListWithTagsResponse{Tests: allTests, Count: len(allTests)}
	if !jsonOutput {
		ui.StopSpinner()
	}

	// Filter by tag (case-insensitive)
	tagFilter := strings.ToLower(testsTag)
	var filtered []api.TestWithTags
	for _, t := range result.Tests {
		for _, tag := range t.Tags {
			if strings.EqualFold(tag.Name, tagFilter) {
				// Also apply platform filter if set
				if testsPlatform == "" || strings.EqualFold(t.Platform, testsPlatform) {
					filtered = append(filtered, t)
				}
				break
			}
		}
	}

	if jsonOutput {
		type jsonTest struct {
			ID       string   `json:"id"`
			Name     string   `json:"name"`
			Platform string   `json:"platform"`
			Tags     []string `json:"tags"`
		}
		tests := make([]jsonTest, 0, len(filtered))
		for _, t := range filtered {
			var tagNames []string
			for _, tag := range t.Tags {
				tagNames = append(tagNames, tag.Name)
			}
			tests = append(tests, jsonTest{
				ID:       t.ID,
				Name:     t.Name,
				Platform: t.Platform,
				Tags:     tagNames,
			})
		}
		output := map[string]interface{}{
			"tests":      tests,
			"count":      len(tests),
			"tag_filter": testsTag,
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(filtered) == 0 {
		ui.PrintInfo("No tests found with tag \"%s\"", testsTag)
		ui.Println()
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "List all tags:", Command: "revyl tag list"},
			{Label: "Tag a test:", Command: "revyl tag set <test> " + testsTag},
		})
		return nil
	}

	ui.Println()
	ui.PrintInfo("Tests with tag \"%s\" (%d):", testsTag, len(filtered))
	ui.Println()

	table := ui.NewTable("NAME", "PLATFORM", "TAGS", "ID")
	table.SetMinWidth(0, 25) // NAME
	table.SetMinWidth(1, 8)  // PLATFORM
	table.SetMinWidth(2, 15) // TAGS
	table.SetMinWidth(3, 36) // ID

	for _, t := range filtered {
		var tagNames []string
		for _, tag := range t.Tags {
			tagNames = append(tagNames, tag.Name)
		}
		table.AddRow(t.Name, t.Platform, strings.Join(tagNames, ", "), t.ID)
	}

	table.Render()

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Run a test:", Command: "revyl test run <name>"},
		{Label: "List all tags:", Command: "revyl tag list"},
	})

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
	// Check if --json flag is set (either local or global)
	jsonOutput := validateOutputJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	allValid := true
	var results []map[string]interface{}

	for _, file := range args {
		result, err := yaml.ValidateYAMLFile(file)
		if err != nil {
			if jsonOutput {
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

		if jsonOutput {
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

	if jsonOutput {
		data, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(data))
	}

	if !allValid {
		return fmt.Errorf("validation failed")
	}
	return nil
}
