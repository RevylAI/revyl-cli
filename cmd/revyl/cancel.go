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
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")
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
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")
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

// getAPIKey retrieves the active authentication token from environment, browser auth, or credentials file.
// Supports both browser-based OAuth tokens (AccessToken) and API key authentication.
//
// Returns:
//   - string: The active authentication token
//   - error: An error if no valid credentials are found
func getAPIKey() (string, error) {
	mgr := auth.NewManager()
	token, err := mgr.GetActiveToken()
	if err != nil || token == "" {
		ui.PrintError("Not authenticated")
		ui.PrintInfo("Run 'revyl auth login' to authenticate")
		return "", fmt.Errorf("not authenticated")
	}
	return token, nil
}
