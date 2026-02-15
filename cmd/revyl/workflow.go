// Package main provides the workflow command for workflow management.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// workflowListJSON controls JSON output for workflow list.
var workflowListJSON bool

// workflowCmd is the parent command for workflow operations.
var workflowCmd = &cobra.Command{
	Use:   "workflow",
	Short: "Manage workflows",
	Long: `Manage workflows (collections of tests).

For build→run: use "revyl workflow run <name> --build" to build, upload, then run
all tests in the workflow.

COMMANDS:
  list    - List all workflows
  run     - Run a workflow (add --build to build and upload first)
  cancel  - Cancel a running workflow
  create  - Create a new workflow
  delete  - Delete a workflow
  open    - Open a workflow in the browser
  status  - Show latest execution status
  history - Show execution history
  report  - Show detailed workflow report
  share    - Generate shareable report link
  location - Manage stored GPS location override
  app      - Manage stored app overrides (per platform)

EXAMPLES:
  revyl workflow list                        # List all workflows
  revyl workflow run smoke-tests --build     # Build first, then run workflow
  revyl workflow run smoke-tests             # Run only (no build)
  revyl workflow status smoke-tests          # Check latest execution status
  revyl workflow report smoke-tests          # View detailed report
  revyl workflow create regression --tests login,checkout
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
	Args: cobra.ExactArgs(1),
	RunE: runWorkflowExec,
}

// workflowListCmd lists all workflows from the API.
var workflowListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all workflows",
	Long: `List all workflows in your organization.

Workflows that are also registered in .revyl/config.yaml are marked with their
local alias name.

EXAMPLES:
  revyl workflow list
  revyl workflow list --json`,
	RunE: runWorkflowList,
}

// workflowCancelCmd cancels a running workflow.
var workflowCancelCmd = &cobra.Command{
	Use:   "cancel <task_id>",
	Short: "Cancel a running workflow",
	Long: `Cancel a running workflow execution by its task ID.

This will cancel the workflow and all of its child test executions.`,
	Args: cobra.ExactArgs(1),
	RunE: runCancelWorkflow,
}

// workflowCreateCmd creates a new workflow.
var workflowCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new workflow",
	Long: `Create a new workflow and open the editor.

EXAMPLES:
  revyl workflow create smoke-tests
  revyl workflow create regression --tests login-flow,checkout`,
	Args: cobra.ExactArgs(1),
	RunE: runCreateWorkflow,
}

// workflowDeleteCmd deletes a workflow.
var workflowDeleteCmd = &cobra.Command{
	Use:   "delete <name|id>",
	Short: "Delete a workflow",
	Long:  `Delete a workflow from Revyl and remove config alias.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runDeleteWorkflow,
}

// workflowOpenCmd opens a workflow in the browser.
var workflowOpenCmd = &cobra.Command{
	Use:   "open <name>",
	Short: "Open a workflow in the browser",
	Long:  `Open a workflow in your default browser editor.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runOpenWorkflow,
}

func init() {
	workflowCmd.AddCommand(workflowListCmd)
	workflowCmd.AddCommand(workflowRunCmd)
	workflowCmd.AddCommand(workflowCancelCmd)
	workflowCmd.AddCommand(workflowCreateCmd)
	workflowCmd.AddCommand(workflowDeleteCmd)
	workflowCmd.AddCommand(workflowOpenCmd)
	workflowCmd.AddCommand(workflowStatusCmd)
	workflowCmd.AddCommand(workflowHistoryCmd)
	workflowCmd.AddCommand(workflowReportCmd)
	workflowCmd.AddCommand(workflowShareCmd)
	workflowCmd.AddCommand(workflowLocationCmd)
	workflowCmd.AddCommand(workflowAppCmd)

	// workflow list flags
	workflowListCmd.Flags().BoolVar(&workflowListJSON, "json", false, "Output results as JSON")

	// workflow run flags (reuse run.go vars)
	workflowRunCmd.Flags().IntVarP(&runRetries, "retries", "r", 1, "Number of retry attempts (1-5)")
	workflowRunCmd.Flags().BoolVar(&runNoWait, "no-wait", false, "Exit after workflow starts without waiting")
	workflowRunCmd.Flags().BoolVar(&runOpen, "open", false, "Open report in browser when complete")
	workflowRunCmd.Flags().IntVarP(&runTimeout, "timeout", "t", 3600, "Timeout in seconds")
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
}

// runWorkflowList lists all workflows from the organization API.
//
// It fetches workflows from the server and cross-references them with
// local config aliases to show which workflows are tracked locally.
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

	// Load project config for local alias resolution
	cwd, _ := os.Getwd()
	var cfg *config.ProjectConfig
	if cwd != "" {
		cfg, _ = config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))
	}

	// Build a reverse map: workflow UUID -> local alias name
	aliasForID := make(map[string]string)
	if cfg != nil && cfg.Workflows != nil {
		for alias, id := range cfg.Workflows {
			aliasForID[id] = alias
		}
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	if !jsonOutput {
		ui.StartSpinner("Fetching workflows...")
	}

	resp, err := client.ListWorkflows(cmd.Context())
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to list workflows: %v", err)
		return err
	}

	if jsonOutput {
		output := make([]map[string]interface{}, 0, len(resp.Workflows))
		for _, w := range resp.Workflows {
			item := map[string]interface{}{
				"id":   w.ID,
				"name": w.Name,
			}
			if alias, ok := aliasForID[w.ID]; ok {
				item["local_alias"] = alias
			}
			output = append(output, item)
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(resp.Workflows) == 0 {
		ui.PrintInfo("No workflows found")
		ui.Println()
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Create a workflow:", Command: "revyl workflow create <name>"},
		})
		return nil
	}

	ui.Println()
	ui.PrintInfo("Workflows (%d)", len(resp.Workflows))
	ui.Println()

	table := ui.NewTable("NAME", "ID", "LOCAL ALIAS")
	table.SetMinWidth(0, 20) // NAME
	table.SetMinWidth(1, 36) // ID (UUID length)
	table.SetMinWidth(2, 12) // LOCAL ALIAS

	for _, w := range resp.Workflows {
		alias := "-"
		if a, ok := aliasForID[w.ID]; ok {
			alias = a
		}
		table.AddRow(w.Name, w.ID, alias)
	}

	table.Render()

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Run a workflow:", Command: "revyl workflow run <name>"},
		{Label: "Create a workflow:", Command: "revyl workflow create <name>"},
	})

	return nil
}
