// Package main provides delete commands for removing tests, workflows, and builds.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
	"github.com/revyl/cli/internal/util"
)

// Delete command flags (used by test delete and workflow delete)
var (
	deleteForce      bool
	deleteRemoteOnly bool
	deleteLocalOnly  bool
)

// runDeleteTest handles the delete test command execution.
func runDeleteTest(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	// Determine JSON output mode early so human output can be suppressed
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	// Get API key
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Load project config
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	cfg, _ := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))

	// Resolve name to ID
	testID, testName, err := resolveTestNameOrID(cmd.Context(), client, cfg, nameOrID)
	if err != nil && !deleteLocalOnly {
		return err
	}

	// For local-only, we just need the name
	if deleteLocalOnly && testName == "" {
		testName = nameOrID
	}

	localFilePath, pathErr := util.SafeTestPath(filepath.Join(cwd, ".revyl", "tests"), testName)
	if pathErr != nil {
		return fmt.Errorf("invalid test name: %w", pathErr)
	}
	localFileExists := fileExists(localFilePath)

	// Show what will be deleted
	if !deleteForce {
		ui.Println()
		ui.PrintInfo("Delete test \"%s\"?", testName)

		if !deleteLocalOnly && testID != "" {
			ui.PrintDim("  - Remote: will be deleted from Revyl")
		}
		if !deleteRemoteOnly && localFileExists {
			ui.PrintDim("  - Local:  %s", localFilePath)
		}

		ui.Println()
		confirmed, err := ui.PromptConfirm(fmt.Sprintf("Delete test %q?", testName), false)
		if err != nil || !confirmed {
			ui.PrintInfo("Cancelled")
			return nil
		}
	}

	// Delete from remote
	if !deleteLocalOnly && testID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		_, err := client.DeleteTest(ctx, testID)
		if err != nil {
			var apiErr *api.APIError
			if errors.As(err, &apiErr) {
				if apiErr.StatusCode == 404 {
					if !jsonOutput {
						ui.PrintWarning("Test not found on remote (may already be deleted)")
					}
				} else if apiErr.StatusCode == 403 {
					ui.PrintError("Permission denied")
					return fmt.Errorf("not authorized to delete this test")
				} else {
					return fmt.Errorf("failed to delete test: %w", err)
				}
			} else {
				return fmt.Errorf("failed to delete test: %w", err)
			}
		} else if !jsonOutput {
			ui.PrintSuccess("Deleted from Revyl")
		}
	}

	// Delete local file
	if !deleteRemoteOnly && localFileExists {
		if err := os.Remove(localFilePath); err != nil {
			if !jsonOutput {
				ui.PrintWarning("Failed to remove local file: %v", err)
			}
		} else if !jsonOutput {
			ui.PrintSuccess("Removed %s", localFilePath)
		}
	}

	// Handle JSON output
	if jsonOutput {
		output := map[string]interface{}{
			"success":     true,
			"test_name":   testName,
			"test_id":     testID,
			"remote_only": deleteRemoteOnly,
			"local_only":  deleteLocalOnly,
		}
		data, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON output: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	ui.Println()
	ui.PrintSuccess("Test \"%s\" deleted successfully.", testName)
	ui.PrintNextSteps([]ui.NextStep{
		{Label: "List tests:", Command: "revyl test list"},
		{Label: "Create a test:", Command: "revyl test create <name>"},
	})
	return nil
}

// runDeleteWorkflow handles the delete workflow command execution.
func runDeleteWorkflow(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	// Determine JSON output mode early so human output can be suppressed
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	// Get API key
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Load project config
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	cfg, _ := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))

	// Resolve name to ID
	workflowID, workflowName, err := resolveWorkflowID(cmd.Context(), nameOrID, cfg, client)
	if err != nil {
		return err
	}

	// Show what will be deleted
	if !deleteForce {
		ui.Println()
		ui.PrintInfo("Delete workflow \"%s\"?", workflowName)
		ui.PrintDim("  - Remote: will be deleted from Revyl")

		ui.Println()
		confirmed, err := ui.PromptConfirm(fmt.Sprintf("Delete workflow %q?", workflowName), false)
		if err != nil || !confirmed {
			ui.PrintInfo("Cancelled")
			return nil
		}
	}

	// Delete from remote
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = client.DeleteWorkflow(ctx, workflowID)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			if apiErr.StatusCode == 404 {
				if !jsonOutput {
					ui.PrintWarning("Workflow not found on remote (may already be deleted)")
				}
			} else if apiErr.StatusCode == 403 {
				ui.PrintError("Permission denied")
				return fmt.Errorf("not authorized to delete this workflow")
			} else {
				return fmt.Errorf("failed to delete workflow: %w", err)
			}
		} else {
			return fmt.Errorf("failed to delete workflow: %w", err)
		}
	} else if !jsonOutput {
		ui.PrintSuccess("Deleted from Revyl")
	}

	// Handle JSON output
	if jsonOutput {
		output := map[string]interface{}{
			"success":       true,
			"workflow_name": workflowName,
			"workflow_id":   workflowID,
		}
		data, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON output: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	ui.Println()
	ui.PrintSuccess("Workflow \"%s\" deleted successfully.", workflowName)
	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Create a workflow:", Command: "revyl workflow create <name>"},
	})
	return nil
}

// resolveTestNameOrID resolves a test name or ID to both values.
func resolveTestNameOrID(ctx context.Context, client *api.Client, cfg *config.ProjectConfig, nameOrID string) (testID, testName string, err error) {
	// Check if it's a local test alias
	if cwd, cwdErr := os.Getwd(); cwdErr == nil {
		testsDir := filepath.Join(cwd, ".revyl", "tests")
		if id, _ := config.GetLocalTestRemoteID(testsDir, nameOrID); id != "" {
			return id, nameOrID, nil
		}
	}

	// Check if it looks like a UUID (try to use as ID directly)
	if looksLikeUUID(nameOrID) {
		// Verify it exists
		test, err := client.GetTest(ctx, nameOrID)
		if err == nil {
			return nameOrID, test.Name, nil
		}
	}

	// Try to find by name in org tests
	result, err := client.ListOrgTests(ctx, 100, 0)
	if err != nil {
		return "", "", fmt.Errorf("failed to list tests: %w", err)
	}

	for _, t := range result.Tests {
		if t.Name == nameOrID {
			return t.ID, t.Name, nil
		}
	}

	return "", "", fmt.Errorf("test \"%s\" not found. Run: revyl test list", nameOrID)
}

// fileExists checks if a file exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
