// Package main provides the workflow command for workflow management.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/execution"
	"github.com/revyl/cli/internal/ui"
)

// workflowListJSON controls JSON output for workflow list.
var workflowListJSON bool

// workflowInfoJSON controls JSON output for workflow info.
var workflowInfoJSON bool

// workflowCmd is the parent command for workflow operations.
var workflowCmd = &cobra.Command{
	Use:               "workflow",
	Short:             "Manage workflows",
	PersistentPreRunE: enforceOrgBindingMatch,
	Long: `Manage workflows (collections of tests).

For build→run: use "revyl workflow run <name> --build" to build, upload, then run
all tests in the workflow.

COMMANDS:
  list         - List all workflows
  info         - Show workflow details (tests, overrides, config)
  run          - Run a workflow (add --build to build and upload first)
  cancel       - Cancel a running workflow
  create       - Create a new workflow
  add-tests    - Add tests to an existing workflow
  remove-tests - Remove tests from an existing workflow
  rename       - Rename a workflow while preserving history
  delete       - Delete a workflow
  open         - Open a workflow in the browser
  status       - Show latest execution status
  history      - Show execution history
  report       - Show detailed workflow report
  share        - Generate shareable report link
  location     - Manage stored GPS location override
  app          - Manage stored app overrides (per platform)
  config       - Manage run configuration (parallelism, retries)
  quarantine   - Manage test quarantine (ignore failures in CI)

EXAMPLES:
  revyl workflow list                        # List all workflows
  revyl workflow info smoke-tests            # View tests, overrides, config
  revyl workflow run smoke-tests --build     # Build first, then run workflow
  revyl workflow run smoke-tests             # Run only (no build)
  revyl workflow status smoke-tests          # Check latest execution status
  revyl workflow report smoke-tests          # View detailed report
  revyl workflow create regression --tests login,checkout
  revyl workflow add-tests smoke-tests login-flow
  revyl workflow remove-tests smoke-tests checkout
  revyl workflow delete smoke-tests`,
}

// workflowRunCmd runs a workflow.
var workflowRunCmd = &cobra.Command{
	Use:   "run <name|id>",
	Short: "Run a workflow by name or ID",
	Long: `Run a workflow by its alias name (from .revyl/config.yaml) or UUID.

Use --build to build and upload before running.
Use --ios-app / --android-app to override the app for all tests in the
workflow (useful for testing a specific app across platforms).

EXAMPLES:
  revyl workflow run smoke-tests
  revyl workflow run smoke-tests --build
  revyl workflow run smoke-tests --build --platform android
  revyl workflow run smoke-tests --android-app <app-uuid>
  revyl workflow run smoke-tests --ios-app <app-uuid> --android-app <app-uuid>`,
	Example: `  revyl workflow run smoke-tests
  revyl workflow run smoke-tests --build
  revyl workflow run smoke-tests --json --no-wait`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkflowExec,
}

// workflowListCmd lists all workflows from the API.
var workflowListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all workflows",
	Long: `List all workflows in your organization.

EXAMPLES:
  revyl workflow list
  revyl workflow list --json`,
	Example: `  revyl workflow list
  revyl workflow list --json`,
	RunE: runWorkflowList,
}

// workflowCancelCmd cancels a running workflow.
var workflowCancelCmd = &cobra.Command{
	Use:   "cancel <task_id>",
	Short: "Cancel a running workflow",
	Long: `Cancel a running workflow execution by its task ID.

This will cancel the workflow and all of its child test executions.`,
	Example: `  revyl workflow cancel <task-id>`,
	Args:    cobra.ExactArgs(1),
	RunE:    runCancelWorkflow,
}

// workflowCreateCmd creates a new workflow.
var workflowCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new workflow",
	Long: `Create a new workflow and open the editor.

EXAMPLES:
  revyl workflow create smoke-tests
  revyl workflow create regression --tests login-flow,checkout`,
	Example: `  revyl workflow create smoke-tests
  revyl workflow create regression --tests login-flow,checkout`,
	Args: cobra.ExactArgs(1),
	RunE: runCreateWorkflow,
}

// workflowRenameCmd renames a workflow while preserving history.
var workflowRenameCmd = &cobra.Command{
	Use:   "rename [old-name|id] [new-name]",
	Short: "Rename a workflow without recreating it",
	Long: `Rename a workflow while keeping the same workflow ID and execution history.

When called with no args in a TTY, this command prompts for workflow selection
and the new name.

EXAMPLES:
  revyl workflow rename smoke-tests regression-smoke
  revyl workflow rename`,
	Args: cobra.MaximumNArgs(2),
	RunE: runRenameWorkflow,
}

// workflowDeleteCmd deletes a workflow.
var workflowDeleteCmd = &cobra.Command{
	Use:   "delete <name|id>",
	Short: "Delete a workflow",
	Long:  `Delete a workflow from Revyl and remove config alias.`,
	Example: `  revyl workflow delete smoke-tests
  revyl workflow delete smoke-tests --force`,
	Args: cobra.ExactArgs(1),
	RunE: runDeleteWorkflow,
}

// workflowOpenCmd opens a workflow in the browser.
var workflowOpenCmd = &cobra.Command{
	Use:   "open <name>",
	Short: "Open a workflow in the browser",
	Long:  `Open a workflow in your default browser editor.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runOpenWorkflow,
}

// workflowInfoCmd shows detailed information about a workflow.
var workflowInfoCmd = &cobra.Command{
	Use:   "info <name|id>",
	Short: "Show workflow details (tests, overrides, config)",
	Long: `Show detailed information about a workflow including its tests,
app/location overrides, run configuration, and last execution.

EXAMPLES:
  revyl workflow info smoke-tests
  revyl workflow info smoke-tests --json
  revyl workflow info <workflow-uuid>`,
	Example: `  revyl workflow info smoke-tests
  revyl workflow info smoke-tests --json`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkflowInfo,
}

func init() {
	workflowCmd.AddCommand(workflowInfoCmd)
	workflowCmd.AddCommand(workflowListCmd)
	workflowCmd.AddCommand(workflowRunCmd)
	workflowCmd.AddCommand(workflowCancelCmd)
	workflowCmd.AddCommand(workflowCreateCmd)
	workflowCmd.AddCommand(workflowRenameCmd)
	workflowCmd.AddCommand(workflowDeleteCmd)
	workflowCmd.AddCommand(workflowOpenCmd)
	workflowCmd.AddCommand(workflowStatusCmd)
	workflowCmd.AddCommand(workflowHistoryCmd)
	workflowCmd.AddCommand(workflowReportCmd)
	workflowCmd.AddCommand(workflowShareCmd)
	workflowCmd.AddCommand(workflowLocationCmd)
	workflowCmd.AddCommand(workflowAppCmd)
	workflowCmd.AddCommand(workflowQuarantineCmd)
	workflowCmd.AddCommand(workflowConfigCmd)
	initWorkflowConfig()

	// workflow info flags
	workflowInfoCmd.Flags().BoolVar(&workflowInfoJSON, "json", false, "Output results as JSON")

	// workflow list flags
	workflowListCmd.Flags().BoolVar(&workflowListJSON, "json", false, "Output results as JSON")

	// workflow run flags (reuse run.go vars)
	workflowRunCmd.Flags().IntVarP(&runRetries, "retries", "r", 1, "Number of retry attempts (1-5)")
	workflowRunCmd.Flags().BoolVar(&runNoWait, "no-wait", false, "Exit after workflow starts without waiting")
	workflowRunCmd.Flags().BoolVar(&runOpen, "open", false, "Open report in browser when complete")
	workflowRunCmd.Flags().IntVarP(&runTimeout, "timeout", "t", execution.DefaultRunTimeoutSeconds, "Timeout in seconds")
	workflowRunCmd.Flags().BoolVar(&runOutputJSON, "json", false, "Output results as JSON")
	workflowRunCmd.Flags().BoolVar(&runGitHubActions, "github-actions", false, "Format output for GitHub Actions")
	workflowRunCmd.Flags().BoolVarP(&runVerbose, "verbose", "v", false, "Show detailed monitoring output")
	workflowRunCmd.Flags().BoolVar(&runWorkflowBuild, "build", false, "Build and upload before running workflow")
	workflowRunCmd.Flags().StringVar(&runWorkflowPlatform, "platform", "", "Platform to use (requires --build)")
	workflowRunCmd.Flags().StringVar(&runWorkflowIOSAppID, "ios-app", "", "Override iOS app ID for all tests in workflow")
	workflowRunCmd.Flags().StringVar(&runWorkflowAndroidAppID, "android-app", "", "Override Android app ID for all tests in workflow")
	workflowRunCmd.Flags().StringVar(&runLocation, "location", "", "Override GPS location for all tests as lat,lng (e.g. 37.7749,-122.4194)")

	// workflow delete flags
	workflowDeleteCmd.Flags().BoolVarP(&deleteForce, "force", "f", false, "Skip confirmation prompt")

	// workflow create flags (reuse create.go vars)
	workflowCreateCmd.Flags().StringVar(&createWorkflowTests, "tests", "", "Comma-separated test names or IDs to include")
	workflowCreateCmd.Flags().BoolVar(&createWorkflowNoOpen, "no-open", false, "Skip opening browser to workflow editor")
	workflowCreateCmd.Flags().BoolVar(&createWorkflowNoSync, "no-sync", false, "Skip adding workflow to .revyl/config.yaml")
	workflowCreateCmd.Flags().BoolVar(&createWorkflowDryRun, "dry-run", false, "Show what would be created without creating")

	workflowRenameCmd.Flags().BoolVar(&workflowRenameNonInteractive, "non-interactive", false, "Disable prompts; requires both positional args")
	workflowRenameCmd.Flags().BoolVarP(&workflowRenameYes, "yes", "y", false, "Auto-accept default rename prompts")
}

// runWorkflowList lists all workflows from the organization API.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (none expected)
//
// Returns:
//   - error: Any error that occurred, or nil on success
func runWorkflowList(cmd *cobra.Command, args []string) error {
	// Check if --json flag is set (either local or global)
	jsonOutput := workflowListJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	// Check authentication
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	if !jsonOutput {
		ui.StartSpinner("Fetching workflows...")
	}

	workflows, err := client.ListAllWorkflows(cmd.Context(), 200)
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to list workflows: %v", err)
		return err
	}

	if jsonOutput {
		output := make([]map[string]interface{}, 0, len(workflows))
		for _, w := range workflows {
			item := map[string]interface{}{
				"id":   w.ID,
				"name": w.Name,
			}
			output = append(output, item)
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(workflows) == 0 {
		ui.PrintInfo("No workflows found")
		ui.Println()
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Create a workflow:", Command: "revyl workflow create <name>"},
		})
		return nil
	}

	ui.Println()
	ui.PrintInfo("Workflows (%d)", len(workflows))
	ui.Println()

	table := ui.NewTable("NAME", "ID")
	table.SetMinWidth(0, 20) // NAME
	table.SetMinWidth(1, 36) // ID (UUID length)

	for _, w := range workflows {
		table.AddRow(w.Name, w.ID)
	}

	table.Render()

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Run a workflow:", Command: "revyl workflow run <name>"},
		{Label: "Create a workflow:", Command: "revyl workflow create <name>"},
	})

	return nil
}

// runWorkflowInfo displays detailed information about a single workflow
// including tests (with per-test last status), overrides, run config, and
// the most recent execution.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (workflow name or ID)
//
// Returns:
//   - error: Any error that occurred, or nil on success
func runWorkflowInfo(cmd *cobra.Command, args []string) error {
	jsonOutput := workflowInfoJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	cwd, _ := os.Getwd()
	cfg, _ := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	if jsonOutput {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
	} else {
		ui.StartSpinner("Loading workflow info...")
	}

	workflowID, workflowName, err := resolveWorkflowID(cmd.Context(), args[0], cfg, client)
	if err != nil {
		if !jsonOutput {
			ui.StopSpinner()
		}
		ui.PrintError("%v", err)
		return fmt.Errorf("workflow not found")
	}

	displayName := workflowName
	if displayName == "" {
		displayName = args[0]
	}

	var (
		wfInfo    *api.WorkflowInfoResponse
		wfFull    *api.Workflow
		wfHistory *api.CLIWorkflowHistoryResponse
		infoErr   error
	)

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		wfInfo, infoErr = client.GetWorkflowInfo(cmd.Context(), workflowID)
	}()
	go func() {
		defer wg.Done()
		wfFull, _ = client.GetWorkflow(cmd.Context(), workflowID)
	}()
	go func() {
		defer wg.Done()
		wfHistory, _ = client.GetWorkflowHistory(cmd.Context(), workflowID, 1, 0)
	}()

	wg.Wait()

	if !jsonOutput {
		ui.StopSpinner()
	}

	if infoErr != nil {
		ui.PrintError("Failed to fetch workflow info: %v", infoErr)
		return infoErr
	}

	if jsonOutput {
		return outputWorkflowInfoJSON(wfInfo, wfFull, wfHistory)
	}

	renderWorkflowInfoCLI(displayName, workflowID, wfInfo, wfFull, wfHistory)
	return nil
}

// outputWorkflowInfoJSON emits the combined workflow info as a single JSON object.
func outputWorkflowInfoJSON(
	info *api.WorkflowInfoResponse,
	full *api.Workflow,
	history *api.CLIWorkflowHistoryResponse,
) error {
	output := map[string]interface{}{
		"id":   info.ID,
		"name": info.Name,
	}

	tests := make([]map[string]interface{}, 0, len(info.TestInfo))
	for _, t := range info.TestInfo {
		item := map[string]interface{}{
			"id":       t.ID,
			"name":     t.Name,
			"platform": t.Platform,
		}
		if t.LastStatus != nil {
			item["last_status"] = *t.LastStatus
		}
		if t.LastDuration != nil {
			item["last_duration"] = *t.LastDuration
		}
		tests = append(tests, item)
	}
	output["tests"] = tests

	if full != nil {
		overrides := map[string]interface{}{}
		if full.OverrideBuildConfig && full.BuildConfig != nil {
			overrides["app"] = full.BuildConfig
		}
		if full.OverrideLocation && full.LocationConfig != nil {
			overrides["location"] = full.LocationConfig
		}
		if len(overrides) > 0 {
			output["overrides"] = overrides
		}
		if full.RunConfig != nil {
			output["run_config"] = full.RunConfig
		}
	}

	if history != nil && len(history.Executions) > 0 {
		latest := history.Executions[0]
		output["last_run"] = map[string]interface{}{
			"status":     latest.Status,
			"started_at": latest.StartedAt,
			"duration":   latest.Duration,
		}
	}

	data, _ := json.MarshalIndent(output, "", "  ")
	fmt.Println(string(data))
	return nil
}

// renderWorkflowInfoCLI renders the human-readable workflow info output.
func renderWorkflowInfoCLI(
	name, id string,
	info *api.WorkflowInfoResponse,
	full *api.Workflow,
	history *api.CLIWorkflowHistoryResponse,
) {
	ui.Println()
	ui.PrintKeyValue("Workflow:", name)
	ui.PrintKeyValue("ID:", id)

	// Tests
	ui.Println()
	if len(info.TestInfo) == 0 {
		ui.PrintInfo("Tests (0)")
		ui.PrintDim("  No tests in this workflow")
	} else {
		ui.PrintInfo("Tests (%d)", len(info.TestInfo))
		ui.Println()

		table := ui.NewTable("NAME", "PLATFORM", "LAST STATUS", "DURATION")
		table.SetMinWidth(0, 20)
		table.SetMinWidth(1, 10)
		table.SetMinWidth(2, 12)

		for _, t := range info.TestInfo {
			status := "–"
			if t.LastStatus != nil && *t.LastStatus != "" {
				status = *t.LastStatus
			}
			dur := "–"
			if t.LastDuration != nil {
				secs := int(*t.LastDuration)
				if secs >= 60 {
					dur = fmt.Sprintf("%dm %ds", secs/60, secs%60)
				} else {
					dur = fmt.Sprintf("%ds", secs)
				}
			}
			table.AddRow(t.Name, t.Platform, status, dur)
		}
		table.Render()
	}

	// Overrides (only shown when at least one is configured)
	if full != nil {
		hasOverrides := false

		iosApp := ""
		androidApp := ""
		if full.OverrideBuildConfig && full.BuildConfig != nil {
			if iosBuild, ok := full.BuildConfig["ios_build"].(map[string]interface{}); ok {
				if appID, ok := iosBuild["app_id"].(string); ok && appID != "" {
					iosApp = appID
					hasOverrides = true
				}
			}
			if androidBuild, ok := full.BuildConfig["android_build"].(map[string]interface{}); ok {
				if appID, ok := androidBuild["app_id"].(string); ok && appID != "" {
					androidApp = appID
					hasOverrides = true
				}
			}
		}

		locStr := ""
		if full.OverrideLocation && full.LocationConfig != nil {
			lat, _ := full.LocationConfig["latitude"]
			lng, _ := full.LocationConfig["longitude"]
			if lat != nil && lng != nil {
				locStr = fmt.Sprintf("%v, %v", lat, lng)
				hasOverrides = true
			}
		}

		if hasOverrides {
			ui.Println()
			ui.PrintInfo("Overrides")
			if iosApp != "" {
				ui.PrintKeyValue("App (iOS):", iosApp)
			}
			if androidApp != "" {
				ui.PrintKeyValue("App (Android):", androidApp)
			}
			if locStr != "" {
				ui.PrintKeyValue("Location:", locStr)
			}
		}

		// Run Config (only shown when non-default)
		if full.RunConfig != nil && (full.RunConfig.Parallelism > 0 || full.RunConfig.MaxRetries > 0) {
			ui.Println()
			ui.PrintInfo("Run Config")
			if full.RunConfig.Parallelism > 0 {
				ui.PrintKeyValue("Parallelism:", fmt.Sprintf("%d", full.RunConfig.Parallelism))
			}
			if full.RunConfig.MaxRetries > 0 {
				ui.PrintKeyValue("Max Retries:", fmt.Sprintf("%d", full.RunConfig.MaxRetries))
			}
		}
	}

	// Last Run
	if history != nil && len(history.Executions) > 0 {
		latest := history.Executions[0]
		ui.Println()
		ui.PrintInfo("Last Run")
		ui.PrintKeyValue("Status:", latest.Status)
		if latest.Duration != "" {
			ui.PrintKeyValue("Duration:", latest.Duration)
		}
	}

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Run this workflow:", Command: fmt.Sprintf("revyl workflow run %s", name)},
		{Label: "Execution history:", Command: fmt.Sprintf("revyl workflow history %s", name)},
	})
}
