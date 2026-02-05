// Package main provides cancel commands for stopping running tests and workflows.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/ui"
)

// cancelCmd is the parent command for cancellation operations.
var cancelCmd = &cobra.Command{
	Use:   "cancel",
	Short: "Cancel running tests or workflows",
	Long: `Cancel a running test or workflow execution.

Use the task ID shown when you started the test/workflow,
or from the report URL.

TASK ID SOURCES:
  - CLI output when starting: "Task ID: abc123-def456..."
  - Report URL: https://app.revyl.ai/tests/report?taskId=abc123...

COMMANDS:
  test      - Cancel a running test
  workflow  - Cancel a running workflow (and all its tests)

EXAMPLES:
  revyl cancel test abc123-def456-...
  revyl cancel workflow xyz789-...`,
}

// cancelTestCmd cancels a running test.
var cancelTestCmd = &cobra.Command{
	Use:   "test <task_id>",
	Short: "Cancel a running test",
	Long: `Cancel a running test execution by its task ID.

The task ID is shown when you start a test:
  $ revyl run test login-flow
  Task ID: abc123-def456-...

Or from the report URL:
  https://app.revyl.ai/tests/report?taskId=abc123-def456-...

NOTE: If the test has already completed, failed, or been cancelled,
this command will return an error indicating the current status.

EXAMPLES:
  revyl cancel test abc123-def456-789a-bcde-f01234567890
  revyl cancel test abc123-def456-789a-bcde-f01234567890 --json`,
	Args: cobra.ExactArgs(1),
	RunE: runCancelTest,
}

// cancelWorkflowCmd cancels a running workflow.
var cancelWorkflowCmd = &cobra.Command{
	Use:   "workflow <task_id>",
	Short: "Cancel a running workflow",
	Long: `Cancel a running workflow execution by its task ID.

This will cancel the workflow and all of its child test executions.

The task ID is shown when you start a workflow:
  $ revyl run workflow smoke-tests
  Task ID: xyz789-...

NOTE: If the workflow has already completed, failed, or been cancelled,
this command will return an error indicating the current status.

EXAMPLES:
  revyl cancel workflow xyz789-abc123-def456-...
  revyl cancel workflow xyz789-abc123-def456-... --json`,
	Args: cobra.ExactArgs(1),
	RunE: runCancelWorkflow,
}

func init() {
	cancelCmd.AddCommand(cancelTestCmd)
	cancelCmd.AddCommand(cancelWorkflowCmd)
}

// runCancelTest handles the cancel test command execution.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (task ID)
//
// Returns:
//   - error: Any error that occurred
func runCancelTest(cmd *cobra.Command, args []string) error {
	taskID := args[0]

	// Get API key
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")

	// Create API client
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Cancel the test
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ui.PrintInfo("Cancelling test %s...", taskID)

	resp, err := client.CancelTest(ctx, taskID)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			if apiErr.StatusCode == 404 {
				ui.PrintError("Test execution not found: %s", taskID)
				ui.PrintInfo("Make sure you're using the correct task ID")
				return fmt.Errorf("test execution not found")
			}
			if apiErr.StatusCode == 403 {
				ui.PrintError("Permission denied")
				ui.PrintInfo("You can only cancel tests in your organization")
				return fmt.Errorf("permission denied")
			}
		}
		ui.PrintError("Failed to cancel test: %v", err)
		return err
	}

	// Handle JSON output
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if jsonOutput {
		output, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			ui.PrintError("Failed to marshal JSON response: %v", err)
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
		fmt.Println(string(output))
		if !resp.Success {
			return fmt.Errorf("cancellation failed: %s", resp.Message)
		}
		return nil
	}

	// Display result
	if resp.Success {
		ui.PrintSuccess("Test cancelled successfully")
		if resp.Status != nil {
			ui.PrintInfo("Status: %s", *resp.Status)
		}
		return nil
	}

	// Cancellation failed - return error for proper exit code
	ui.PrintWarning("Could not cancel test: %s", resp.Message)
	if resp.Status != nil {
		ui.PrintInfo("Current status: %s", *resp.Status)
	}
	return fmt.Errorf("could not cancel test: %s", resp.Message)
}

// runCancelWorkflow handles the cancel workflow command execution.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (task ID)
//
// Returns:
//   - error: Any error that occurred
func runCancelWorkflow(cmd *cobra.Command, args []string) error {
	taskID := args[0]

	// Get API key
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")

	// Create API client
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Cancel the workflow
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ui.PrintInfo("Cancelling workflow %s...", taskID)

	resp, err := client.CancelWorkflow(ctx, taskID)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			if apiErr.StatusCode == 404 {
				ui.PrintError("Workflow execution not found: %s", taskID)
				ui.PrintInfo("Make sure you're using the correct task ID")
				return fmt.Errorf("workflow execution not found")
			}
			if apiErr.StatusCode == 403 {
				ui.PrintError("Permission denied")
				ui.PrintInfo("You can only cancel workflows in your organization")
				return fmt.Errorf("permission denied")
			}
		}
		ui.PrintError("Failed to cancel workflow: %v", err)
		return err
	}

	// Handle JSON output
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if jsonOutput {
		output, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			ui.PrintError("Failed to marshal JSON response: %v", err)
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
		fmt.Println(string(output))
		if !resp.Success {
			return fmt.Errorf("cancellation failed: %s", resp.Message)
		}
		return nil
	}

	// Display result
	if resp.Success {
		ui.PrintSuccess("Workflow cancelled successfully")
		ui.PrintInfo("All child test executions have been cancelled")
		return nil
	}

	// Cancellation failed - return error for proper exit code
	ui.PrintWarning("Could not cancel workflow: %s", resp.Message)
	return fmt.Errorf("could not cancel workflow: %s", resp.Message)
}

// getAPIKey retrieves the API key from environment or credentials file.
//
// Returns:
//   - string: The API key
//   - error: Any error that occurred
func getAPIKey() (string, error) {
	mgr := auth.NewManager()
	creds, err := mgr.GetCredentials()
	if err != nil || creds == nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated")
		ui.PrintInfo("Run 'revyl auth login' to authenticate")
		return "", fmt.Errorf("not authenticated")
	}
	return creds.APIKey, nil
}
