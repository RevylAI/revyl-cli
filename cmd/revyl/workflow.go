// Package main provides the workflow command for workflow management.
package main

import (
	"github.com/spf13/cobra"
)

// workflowCmd is the parent command for workflow operations.
var workflowCmd = &cobra.Command{
	Use:   "workflow",
	Short: "Manage workflows",
	Long: `Manage workflows (collections of tests).

For build→run: use "revyl workflow run <name> --build" to build, upload, then run
all tests in the workflow.

COMMANDS:
  run     - Run a workflow (add --build to build and upload first)
  cancel  - Cancel a running workflow
  create  - Create a new workflow
  delete  - Delete a workflow
  open    - Open a workflow in the browser

EXAMPLES:
  revyl workflow run smoke-tests --build   # Build first, then run workflow
  revyl workflow run smoke-tests           # Run only (no build)
  revyl workflow create regression --tests login,checkout
  revyl workflow delete smoke-tests`,
}

// workflowRunCmd runs a workflow.
var workflowRunCmd = &cobra.Command{
	Use:   "run <name|id>",
	Short: "Run a workflow by name or ID",
	Long: `Run a workflow by its alias name (from .revyl/config.yaml) or UUID.

Use --build to build and upload before running.

EXAMPLES:
  revyl workflow run smoke-tests
  revyl workflow run smoke-tests --build
  revyl workflow run smoke-tests --build --variant android`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkflowExec,
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
	workflowCmd.AddCommand(workflowRunCmd)
	workflowCmd.AddCommand(workflowCancelCmd)
	workflowCmd.AddCommand(workflowCreateCmd)
	workflowCmd.AddCommand(workflowDeleteCmd)
	workflowCmd.AddCommand(workflowOpenCmd)

	// workflow run flags (reuse run.go vars)
	workflowRunCmd.Flags().IntVarP(&runRetries, "retries", "r", 1, "Number of retry attempts (1-5)")
	workflowRunCmd.Flags().BoolVar(&runNoWait, "no-wait", false, "Exit after workflow starts without waiting")
	workflowRunCmd.Flags().BoolVar(&runOpen, "open", false, "Open report in browser when complete")
	workflowRunCmd.Flags().IntVarP(&runTimeout, "timeout", "t", 3600, "Timeout in seconds")
	workflowRunCmd.Flags().BoolVar(&runOutputJSON, "json", false, "Output results as JSON")
	workflowRunCmd.Flags().BoolVar(&runGitHubActions, "github-actions", false, "Format output for GitHub Actions")
	workflowRunCmd.Flags().BoolVarP(&runVerbose, "verbose", "v", false, "Show detailed monitoring output")
	workflowRunCmd.Flags().BoolVar(&runWorkflowBuild, "build", false, "Build and upload before running workflow")
	workflowRunCmd.Flags().StringVar(&runWorkflowVariant, "variant", "", "Build variant to use (requires --build)")

	// workflow delete flags
	workflowDeleteCmd.Flags().BoolVarP(&deleteForce, "force", "f", false, "Skip confirmation prompt")

	// workflow create flags (reuse create.go vars)
	workflowCreateCmd.Flags().StringVar(&createWorkflowTests, "tests", "", "Comma-separated test names or IDs to include")
	workflowCreateCmd.Flags().BoolVar(&createWorkflowNoOpen, "no-open", false, "Skip opening browser to workflow editor")
	workflowCreateCmd.Flags().BoolVar(&createWorkflowNoSync, "no-sync", false, "Skip adding workflow to .revyl/config.yaml")
	workflowCreateCmd.Flags().BoolVar(&createWorkflowDryRun, "dry-run", false, "Show what would be created without creating")
}
