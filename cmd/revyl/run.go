// Package main provides run commands for executing tests and workflows.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/execution"
	"github.com/revyl/cli/internal/sse"
	"github.com/revyl/cli/internal/ui"
)

// runCmd is the parent command for running tests/workflows without building.
var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run tests or workflows (without building)",
	Long: `Run tests or workflows without building first.

Use this when you want to run against an existing build version.

PREREQUISITES:
  - Authenticated: revyl auth login
  - Project initialized: revyl init (optional, for aliases)

COMMANDS:
  test      - Run a single test
  workflow  - Run a workflow (multiple tests)

OUTPUT:
  - Human-readable progress by default
  - JSON with --output flag for programmatic use

EXIT CODES:
  0 - Test/workflow passed
  1 - Test/workflow failed or error`,
}

// runTestCmd runs a single test.
var runTestCmd = &cobra.Command{
	Use:   "test <name|id>",
	Short: "Run a test by name or ID",
	Long: `Run a test by its alias name (from .revyl/config.yaml) or UUID.

PREREQUISITES:
  - Authenticated: revyl auth login
  - Project initialized: revyl init (optional, for aliases)

OUTPUT:
  - Human-readable progress by default
  - JSON with --output flag for programmatic use

EXIT CODES:
  0 - Test passed
  1 - Test failed or error

EXAMPLES:
  revyl run test login-flow           # By alias from .revyl/config.yaml
  revyl run test abc123-def456...     # By UUID
  revyl run test login-flow --output  # JSON output for CI/CD
  revyl run test login-flow -r 3      # With 3 retries`,
	Args: cobra.ExactArgs(1),
	RunE: runTestExec,
}

// runWorkflowCmd runs a workflow.
var runWorkflowCmd = &cobra.Command{
	Use:   "workflow <name|id>",
	Short: "Run a workflow by name or ID",
	Long: `Run a workflow by its alias name (from .revyl/config.yaml) or UUID.

PREREQUISITES:
  - Authenticated: revyl auth login
  - Project initialized: revyl init (optional, for aliases)

OUTPUT:
  - Human-readable progress by default
  - JSON with --output flag for programmatic use

EXIT CODES:
  0 - All tests passed
  1 - One or more tests failed

EXAMPLES:
  revyl run workflow smoke-tests      # By alias from .revyl/config.yaml
  revyl run workflow abc123-def456... # By UUID
  revyl run workflow smoke-tests --output  # JSON output for CI/CD`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkflowExec,
}

var (
	runRetries        int
	runBuildVersionID string
	runNoWait         bool
	runOpen           bool
	runTimeout        int
	runOutputJSON     bool
	runGitHubActions  bool
	runVerbose        bool
)

func init() {
	runCmd.AddCommand(runTestCmd)
	runCmd.AddCommand(runWorkflowCmd)

	// Test flags
	runTestCmd.Flags().IntVarP(&runRetries, "retries", "r", 1, "Number of retry attempts (1-5)")
	runTestCmd.Flags().StringVarP(&runBuildVersionID, "build-version-id", "b", "", "Specific build version ID")
	runTestCmd.Flags().BoolVar(&runNoWait, "no-wait", false, "Exit after test starts without waiting")
	runTestCmd.Flags().BoolVar(&runOpen, "open", false, "Open report in browser when complete")
	runTestCmd.Flags().IntVarP(&runTimeout, "timeout", "t", 3600, "Timeout in seconds")
	runTestCmd.Flags().BoolVar(&runOutputJSON, "output", false, "Output results as JSON")
	runTestCmd.Flags().BoolVar(&runGitHubActions, "github-actions", false, "Format output for GitHub Actions")
	runTestCmd.Flags().BoolVarP(&runVerbose, "verbose", "v", false, "Show detailed monitoring output")

	// Workflow flags
	runWorkflowCmd.Flags().IntVarP(&runRetries, "retries", "r", 1, "Number of retry attempts (1-5)")
	runWorkflowCmd.Flags().BoolVar(&runNoWait, "no-wait", false, "Exit after workflow starts without waiting")
	runWorkflowCmd.Flags().BoolVar(&runOpen, "open", false, "Open report in browser when complete")
	runWorkflowCmd.Flags().IntVarP(&runTimeout, "timeout", "t", 3600, "Timeout in seconds")
	runWorkflowCmd.Flags().BoolVar(&runOutputJSON, "output", false, "Output results as JSON")
	runWorkflowCmd.Flags().BoolVar(&runGitHubActions, "github-actions", false, "Format output for GitHub Actions")
	runWorkflowCmd.Flags().BoolVarP(&runVerbose, "verbose", "v", false, "Show detailed monitoring output")
}

// runTestExec executes a test using the shared execution package.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (test name or ID)
//
// Returns:
//   - error: Any error that occurred, or nil on success
func runTestExec(cmd *cobra.Command, args []string) error {
	testNameOrID := args[0]

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Load project config for alias resolution
	cwd, _ := os.Getwd()
	cfg, _ := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))

	// Resolve test ID from alias for display
	testID := testNameOrID
	if cfg != nil {
		if id, ok := cfg.Tests[testNameOrID]; ok {
			testID = id
			ui.PrintInfo("Resolved '%s' to test ID: %s", testNameOrID, testID)
		}
	}

	ui.PrintBanner(version)
	ui.PrintInfo("Running Test")
	ui.Println()
	ui.PrintInfo("Test ID: %s", testID)
	if runRetries > 1 {
		ui.PrintInfo("Retries: %d", runRetries)
	}
	if runBuildVersionID != "" {
		ui.PrintInfo("Build Version: %s", runBuildVersionID)
	}

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")
	if devMode {
		ui.PrintInfo("Mode: Development (localhost)")
	}
	ui.Println()

	// Use shared execution logic with CLI-specific progress callback
	ui.StartSpinner("Starting test execution...")

	// Track if we've shown the report link yet
	reportLinkShown := false

	result, err := execution.RunTest(cmd.Context(), creds.APIKey, cfg, execution.RunTestParams{
		TestNameOrID:   testNameOrID,
		Retries:        runRetries,
		BuildVersionID: runBuildVersionID,
		Timeout:        runTimeout,
		DevMode:        devMode,
		OnProgress: func(status *sse.TestStatus) {
			ui.StopSpinner() // Stop spinner on first progress update

			// Show report link on first progress update (when we have the task ID)
			if !reportLinkShown && status.TaskID != "" {
				reportURL := fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(devMode), status.TaskID)
				ui.PrintLink("Report", reportURL)
				ui.Println()
				reportLinkShown = true
			}

			if runVerbose {
				ui.PrintVerboseStatus(status.Status, status.Progress, status.CurrentStep,
					status.CompletedSteps, status.TotalSteps, status.Duration)
			} else {
				ui.PrintBasicStatus(status.Status, status.Progress, status.CompletedSteps, status.TotalSteps)
			}
		},
	})
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Test execution failed: %v", err)
		return err
	}

	ui.Println()

	// Handle no-wait mode (result will have TaskID but may not be complete)
	if runNoWait && result.TaskID != "" {
		ui.PrintSuccess("Test queued successfully")
		ui.PrintInfo("Task ID: %s", result.TaskID)
		ui.PrintLink("Report", result.ReportURL)
		if runOpen {
			ui.OpenBrowser(result.ReportURL)
		}
		return nil
	}

	// Show final result
	if result.Success {
		if runOutputJSON || runGitHubActions {
			outputTestResultJSON(result)
		} else {
			ui.PrintTestResult(result.TestName, "passed", result.ReportURL, "")
			ui.Println()
			ui.PrintSuccess("Test completed successfully!")
		}
	} else {
		if runOutputJSON || runGitHubActions {
			outputTestResultJSON(result)
		} else {
			ui.PrintTestResult(result.TestName, "failed", result.ReportURL, result.ErrorMessage)
			ui.Println()
			ui.PrintError("Test failed")
		}
	}

	if runOpen {
		ui.PrintInfo("Opening report in browser...")
		ui.OpenBrowser(result.ReportURL)
	}

	if !result.Success {
		return fmt.Errorf("test failed")
	}

	return nil
}

// outputTestResultJSON outputs test results as JSON for CI/CD integration.
//
// Parameters:
//   - result: The test execution result
func outputTestResultJSON(result *execution.RunTestResult) {
	output := map[string]interface{}{
		"success":     result.Success,
		"task_id":     result.TaskID,
		"test_id":     result.TestID,
		"test_name":   result.TestName,
		"status":      result.Status,
		"report_link": result.ReportURL,
		"duration":    result.Duration,
	}
	if result.ErrorMessage != "" {
		output["error"] = result.ErrorMessage
	}

	data, _ := json.MarshalIndent(output, "", "  ")
	fmt.Println(string(data))
}

// runWorkflowExec executes a workflow using the shared execution package.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (workflow name or ID)
//
// Returns:
//   - error: Any error that occurred, or nil on success
func runWorkflowExec(cmd *cobra.Command, args []string) error {
	workflowNameOrID := args[0]

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Load project config for alias resolution
	cwd, _ := os.Getwd()
	cfg, _ := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))

	// Resolve workflow ID from alias for display
	workflowID := workflowNameOrID
	if cfg != nil {
		if id, ok := cfg.Workflows[workflowNameOrID]; ok {
			workflowID = id
			ui.PrintInfo("Resolved '%s' to workflow ID: %s", workflowNameOrID, workflowID)
		}
	}

	ui.PrintBanner(version)
	ui.PrintInfo("Running Workflow")
	ui.Println()
	ui.PrintInfo("Workflow ID: %s", workflowID)
	if runRetries > 1 {
		ui.PrintInfo("Retries: %d", runRetries)
	}

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")
	if devMode {
		ui.PrintInfo("Mode: Development (localhost)")
	}
	ui.Println()

	// Use shared execution logic
	ui.StartSpinner("Starting workflow execution...")

	// Track if we've shown the report link yet
	reportLinkShown := false

	result, err := execution.RunWorkflow(cmd.Context(), creds.APIKey, cfg, execution.RunWorkflowParams{
		WorkflowNameOrID: workflowNameOrID,
		Retries:          runRetries,
		Timeout:          runTimeout,
		DevMode:          devMode,
		OnProgress: func(status *sse.WorkflowStatus) {
			ui.StopSpinner() // Stop spinner on first progress update

			// Show report link on first progress update (when we have the task ID)
			if !reportLinkShown && status.TaskID != "" {
				reportURL := fmt.Sprintf("%s/workflows/report?taskId=%s", config.GetAppURL(devMode), status.TaskID)
				ui.PrintLink("Report", reportURL)
				ui.Println()
				reportLinkShown = true
			}

			if runVerbose {
				ui.PrintVerboseWorkflowStatus(status.Status, status.CompletedTests, status.TotalTests,
					status.PassedTests, status.FailedTests, status.Duration)
			} else {
				ui.PrintBasicWorkflowStatus(status.Status, status.CompletedTests, status.TotalTests)
			}
		},
	})
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Workflow execution failed: %v", err)
		return err
	}

	ui.Println()

	// Handle no-wait mode
	if runNoWait && result.TaskID != "" {
		ui.PrintSuccess("Workflow queued successfully")
		ui.PrintInfo("Task ID: %s", result.TaskID)
		ui.PrintLink("Report", result.ReportURL)
		if runOpen {
			ui.OpenBrowser(result.ReportURL)
		}
		return nil
	}

	// Show final result
	if runOutputJSON || runGitHubActions {
		outputWorkflowResultJSON(result)
	} else if result.Success {
		ui.PrintSuccess("Workflow completed: %d/%d tests passed", result.PassedTests, result.TotalTests)
	} else {
		// Show appropriate message based on status
		switch result.Status {
		case "cancelled":
			ui.PrintWarning("Workflow cancelled: %d passed, %d failed", result.PassedTests, result.FailedTests)
		case "timeout":
			ui.PrintWarning("Workflow timed out: %d passed, %d failed", result.PassedTests, result.FailedTests)
		default:
			ui.PrintError("Workflow finished: %d passed, %d failed", result.PassedTests, result.FailedTests)
		}
	}

	ui.PrintLink("Report", result.ReportURL)

	if runOpen {
		ui.PrintInfo("Opening report in browser...")
		ui.OpenBrowser(result.ReportURL)
	}

	if !result.Success {
		// Return appropriate error based on status
		switch result.Status {
		case "cancelled":
			return fmt.Errorf("workflow was cancelled")
		case "timeout":
			return fmt.Errorf("workflow timed out")
		default:
			if result.FailedTests > 0 {
				return fmt.Errorf("workflow had %d failed tests", result.FailedTests)
			}
			return fmt.Errorf("workflow failed with status: %s", result.Status)
		}
	}

	return nil
}

// outputWorkflowResultJSON outputs workflow results as JSON for CI/CD integration.
//
// Parameters:
//   - result: The workflow execution result
func outputWorkflowResultJSON(result *execution.RunWorkflowResult) {
	output := map[string]interface{}{
		"success":      result.Success,
		"task_id":      result.TaskID,
		"workflow_id":  result.WorkflowID,
		"status":       result.Status,
		"report_link":  result.ReportURL,
		"total_tests":  result.TotalTests,
		"passed_tests": result.PassedTests,
		"failed_tests": result.FailedTests,
		"duration":     result.Duration,
	}
	if result.ErrorMessage != "" {
		output["error"] = result.ErrorMessage
	}

	data, _ := json.MarshalIndent(output, "", "  ")
	fmt.Println(string(data))
}
