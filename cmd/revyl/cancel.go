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
		if resp.Status != nil && *resp.Status == "cancelled" {
			ui.PrintSuccess("Test cancelled successfully")
		} else {
			ui.PrintInfo("Test cancellation requested")
			ui.PrintInfo("Check the test status to confirm the execution has ended")
		}
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
		ui.PrintInfo("Workflow cancellation requested")
		ui.PrintInfo("Check child test statuses to confirm their executions have ended")
		return nil
	}

	// Cancellation failed - return error for proper exit code
	ui.PrintWarning("Could not cancel workflow: %s", resp.Message)
	return fmt.Errorf("could not cancel workflow: %s", resp.Message)
}

// runCancelBuild handles the build cancel command execution.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (remote build job ID)
//
// Returns:
//   - error: Any error that occurred
func runCancelBuild(cmd *cobra.Command, args []string) error {
	buildJobID := args[0]

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")
	if jsonOutput {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
	}

	client := api.NewClientWithDevMode(apiKey, devMode)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if !jsonOutput {
		ui.PrintInfo("Cancelling build %s...", buildJobID)
	}

	if err := client.CancelRemoteBuild(ctx, buildJobID); err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.StatusCode {
			case 404:
				ui.PrintError("Remote build not found: %s", buildJobID)
				return fmt.Errorf("remote build not found")
			case 403:
				ui.PrintError("Permission denied")
				ui.PrintInfo("You can only cancel builds in your organization")
				return fmt.Errorf("permission denied")
			case 409:
				ui.PrintWarning("Remote build is already terminal")
				return fmt.Errorf("remote build is already terminal")
			}
		}
		ui.PrintError("Failed to cancel build: %v", err)
		return err
	}

	if jsonOutput {
		output, err := json.MarshalIndent(map[string]string{
			"status":       "cancelled",
			"build_job_id": buildJobID,
		}, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
		fmt.Println(string(output))
		return nil
	}

	ui.PrintSuccess("Build cancellation requested")
	return nil
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
