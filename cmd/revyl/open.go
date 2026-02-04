// Package main provides the open command for opening tests and workflows in browser.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// openCmd is the parent command for opening resources in browser.
var openCmd = &cobra.Command{
	Use:   "open",
	Short: "Open tests and workflows in browser",
	Long: `Open tests and workflows in your default browser.

COMMANDS:
  test      - Open a test in the browser editor
  workflow  - Open a workflow in the browser editor

RESOLUTION:
  Names are resolved from .revyl/config.yaml aliases first.
  If not found, searches the organization's tests/workflows.
  UUIDs can be used directly.

EXAMPLES:
  revyl open test login-flow
  revyl open workflow smoke-tests`,
}

// openTestCmd opens a test in the browser.
var openTestCmd = &cobra.Command{
	Use:   "test <name>",
	Short: "Open a test in the browser",
	Long: `Open a test in your default browser editor.

The test can be specified by:
  - Name (alias from .revyl/config.yaml)
  - UUID (direct ID)
  - Test name (searches organization)

WHAT IT DOES:
  1. Resolves the test name to an ID
  2. Opens the test editor in your default browser

EXAMPLES:
  revyl open test login-flow                    # By alias
  revyl open test 8ff0bd1b-d42d-4c7b-967c-...   # By UUID
  revyl open test "My Test Name"                # By name (searches org)`,
	Args: cobra.ExactArgs(1),
	RunE: runOpenTest,
}

// openWorkflowCmd opens a workflow in the browser.
var openWorkflowCmd = &cobra.Command{
	Use:   "workflow <name>",
	Short: "Open a workflow in the browser",
	Long: `Open a workflow in your default browser editor.

The workflow can be specified by:
  - Name (alias from .revyl/config.yaml)
  - UUID (direct ID)

WHAT IT DOES:
  1. Resolves the workflow name to an ID
  2. Opens the workflow editor in your default browser

EXAMPLES:
  revyl open workflow smoke-tests               # By alias
  revyl open workflow def456-abc123-...         # By UUID`,
	Args: cobra.ExactArgs(1),
	RunE: runOpenWorkflow,
}

func init() {
	openCmd.AddCommand(openTestCmd)
	openCmd.AddCommand(openWorkflowCmd)
}

// runOpenTest opens a test in the browser.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (test name or ID)
//
// Returns:
//   - error: Any error that occurred
func runOpenTest(cmd *cobra.Command, args []string) error {
	testNameOrID := args[0]

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")

	// Try to resolve name to ID from config
	var testID string
	cfg, err := config.LoadProjectConfig(configPath)
	if err == nil && cfg.Tests != nil {
		if id, ok := cfg.Tests[testNameOrID]; ok {
			testID = id
		}
	}

	// If not found in config, check if it looks like a UUID or search via API
	if testID == "" {
		// Check if it looks like a UUID (contains dashes and is ~36 chars)
		if len(testNameOrID) >= 32 {
			testID = testNameOrID
		} else {
			// Search via API
			devMode, _ := cmd.Flags().GetBool("dev")
			client := api.NewClientWithDevMode(creds.APIKey, devMode)

			ui.StartSpinner("Searching for test...")
			testsResp, err := client.ListOrgTests(cmd.Context(), 100, 0)
			ui.StopSpinner()

			if err != nil {
				ui.PrintError("Failed to search for test: %v", err)
				return err
			}

			for _, t := range testsResp.Tests {
				if t.Name == testNameOrID {
					testID = t.ID
					break
				}
			}

			if testID == "" {
				ui.PrintError("Test '%s' not found", testNameOrID)
				ui.PrintInfo("Use 'revyl tests remote' to list available tests")
				return fmt.Errorf("test not found")
			}
		}
	}

	// Open browser
	devMode, _ := cmd.Flags().GetBool("dev")
	testURL := fmt.Sprintf("%s/tests/execute?testUid=%s", config.GetAppURL(devMode), testID)

	ui.PrintInfo("Opening test '%s'...", testNameOrID)
	ui.PrintLink("Test", testURL)

	if err := ui.OpenBrowser(testURL); err != nil {
		ui.PrintWarning("Could not open browser: %v", err)
		ui.PrintInfo("Open manually: %s", testURL)
	}

	return nil
}

// runOpenWorkflow opens a workflow in the browser.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (workflow name or ID)
//
// Returns:
//   - error: Any error that occurred
func runOpenWorkflow(cmd *cobra.Command, args []string) error {
	workflowNameOrID := args[0]

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")

	// Try to resolve name to ID from config
	var workflowID string
	cfg, err := config.LoadProjectConfig(configPath)
	if err == nil && cfg.Workflows != nil {
		if id, ok := cfg.Workflows[workflowNameOrID]; ok {
			workflowID = id
		}
	}

	// If not found in config, assume it's an ID
	if workflowID == "" {
		workflowID = workflowNameOrID
	}

	// Open browser
	devMode, _ := cmd.Flags().GetBool("dev")
	workflowURL := fmt.Sprintf("%s/workflows/%s", config.GetAppURL(devMode), workflowID)

	ui.PrintInfo("Opening workflow '%s'...", workflowNameOrID)
	ui.PrintLink("Workflow", workflowURL)

	if err := ui.OpenBrowser(workflowURL); err != nil {
		ui.PrintWarning("Could not open browser: %v", err)
		ui.PrintInfo("Open manually: %s", workflowURL)
	}

	return nil
}
