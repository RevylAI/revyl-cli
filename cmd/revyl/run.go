// Package main provides run commands for executing tests and workflows.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/execution"
	"github.com/revyl/cli/internal/hotreload"
	_ "github.com/revyl/cli/internal/hotreload/providers" // Register providers
	"github.com/revyl/cli/internal/sse"
	"github.com/revyl/cli/internal/ui"
)

var (
	runRetries              int
	runBuildID              string
	runNoWait               bool
	runOpen                 bool
	runTimeout              int
	runOutputJSON           bool
	runGitHubActions        bool
	runVerbose              bool
	runTestBuild            bool
	runTestPlatform         string
	runWorkflowBuild        bool
	runWorkflowPlatform     string
	runWorkflowIOSAppID     string
	runWorkflowAndroidAppID string
	runHotReload            bool
	runHotReloadPort        int
	runHotReloadProvider    string
	runLocation             string
)

// minRetries is the minimum allowed retry count.
const minRetries = 1

// maxRetries is the maximum allowed retry count.
const maxRetries = 5

// resolveRunOpen determines whether reports should auto-open.
// Explicit --open takes precedence over config defaults.
func resolveRunOpen(cmd *cobra.Command, cfg *config.ProjectConfig, flagValue bool) bool {
	if cmd != nil && cmd.Flags().Changed("open") {
		return flagValue
	}
	return config.EffectiveOpenBrowser(cfg)
}

// resolveRunTimeout determines the effective timeout in seconds.
// Explicit --timeout takes precedence over config defaults.
func resolveRunTimeout(cmd *cobra.Command, cfg *config.ProjectConfig, flagValue int) int {
	if cmd != nil && cmd.Flags().Changed("timeout") {
		return flagValue
	}
	return config.EffectiveTimeoutSeconds(cfg, flagValue)
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
	// Validate retries range
	if runRetries < minRetries || runRetries > maxRetries {
		return fmt.Errorf("--retries must be between %d and %d (got %d)", minRetries, maxRetries, runRetries)
	}

	// Honor global --json (root persistent) and local --json
	if v, _ := cmd.Flags().GetBool("json"); v {
		runOutputJSON = true
	}
	if v, _ := cmd.Root().PersistentFlags().GetBool("json"); v {
		runOutputJSON = true
	}
	// Load project config for alias resolution
	cwd, _ := os.Getwd()
	cfg, _ := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))
	effectiveOpen := resolveRunOpen(cmd, cfg, runOpen)
	effectiveTimeout := resolveRunTimeout(cmd, cfg, runTimeout)

	// Check if hot reload mode is enabled
	if runHotReload {
		return runTestWithHotReload(cmd, args)
	}

	testNameOrID := args[0]

	// Check authentication
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Resolve test ID from alias for display
	testID := testNameOrID
	var isAlias bool
	if cfg != nil {
		if id, ok := cfg.Tests[testNameOrID]; ok {
			testID = id
			isAlias = true
			ui.PrintInfo("Resolved '%s' to test ID: %s", testNameOrID, testID)
		}
	}

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")

	// Validate test exists before building or executing (fail fast).
	// If the name was resolved from config, we trust the alias; otherwise
	// verify the identifier is a valid UUID and optionally probe the API.
	if !isAlias {
		if !looksLikeUUID(testID) {
			// Not an alias and not a UUID -- likely a typo or unregistered name
			var availableTests []string
			if cfg != nil && cfg.Tests != nil {
				for name := range cfg.Tests {
					availableTests = append(availableTests, name)
				}
			}
			errMsg := fmt.Sprintf("test '%s' not found in config", testNameOrID)
			if len(availableTests) > 0 {
				errMsg += fmt.Sprintf(". Available tests: %v", availableTests)
			}
			errMsg += "\n\nHint: Run 'revyl test remote' to see all available tests."
			ui.PrintError("%s", errMsg)
			return fmt.Errorf("test not found")
		}
		// It's a UUID format -- verify it exists via API before building
		validationClient := api.NewClientWithDevMode(apiKey, devMode)
		_, err := validationClient.GetTest(cmd.Context(), testID)
		if err != nil {
			ui.PrintError("test '%s' not found: %v", testNameOrID, err)
			return fmt.Errorf("test not found")
		}
	}

	ui.PrintBanner(version)
	ui.PrintInfo("Running Test")
	ui.Println()
	ui.PrintInfo("Test ID: %s", testID)
	if runRetries > 1 {
		ui.PrintInfo("Retries: %d", runRetries)
	}
	if runBuildID != "" {
		ui.PrintInfo("Build Version: %s", runBuildID)
	}

	// Parse --location flag
	var hasLocation bool
	var lat, lng float64
	if runLocation != "" {
		var parseErr error
		lat, lng, parseErr = parseLocation(runLocation)
		if parseErr != nil {
			return parseErr
		}
		hasLocation = true
		ui.PrintInfo("Location: %.6f, %.6f", lat, lng)
	}

	if devMode {
		ui.PrintInfo("Mode: Development (localhost)")
	}
	ui.Println()

	// Handle --build flag: build and upload before running test
	if runTestBuild {
		if cfg == nil {
			ui.PrintError("Project not initialized. Run 'revyl init' first.")
			return fmt.Errorf("project not initialized")
		}

		buildCfg := cfg.Build
		var platformCfg config.BuildPlatform

		if runTestPlatform != "" {
			var ok bool
			platformCfg, ok = cfg.Build.Platforms[runTestPlatform]
			if !ok {
				ui.PrintError("Unknown platform: %s", runTestPlatform)
				return fmt.Errorf("unknown platform: %s", runTestPlatform)
			}
			buildCfg.Command = platformCfg.Command
			buildCfg.Output = platformCfg.Output
		}

		if buildCfg.Command == "" {
			ui.PrintError("No build command configured. Check .revyl/config.yaml")
			return fmt.Errorf("no build command")
		}

		// Step 1: Build
		ui.PrintBox("Building", buildCfg.Command)

		startTime := time.Now()
		runner := build.NewRunner(cwd)

		err = runner.Run(buildCfg.Command, func(line string) {
			ui.PrintDim("  %s", line)
		})

		buildDuration := time.Since(startTime)

		if err != nil {
			ui.Println()
			ui.PrintError("Build failed: %v", err)
			return err
		}

		ui.PrintSuccess("Build completed in %s", buildDuration.Round(time.Second))
		ui.Println()

		// Step 2: Upload
		artifactPath := filepath.Join(cwd, buildCfg.Output)
		if _, err := os.Stat(artifactPath); os.IsNotExist(err) {
			ui.PrintError("Build artifact not found: %s", buildCfg.Output)
			return fmt.Errorf("artifact not found")
		}

		buildVersionStr := build.GenerateVersionString()
		metadata := build.CollectMetadata(cwd, buildCfg.Command, runTestPlatform, buildDuration)

		ui.PrintBox("Uploading", filepath.Base(buildCfg.Output))

		client := api.NewClientWithDevMode(apiKey, devMode)
		result, err := client.UploadBuild(cmd.Context(), &api.UploadBuildRequest{
			AppID:    platformCfg.AppID,
			Version:  buildVersionStr,
			FilePath: artifactPath,
			Metadata: metadata,
		})

		if err != nil {
			ui.PrintError("Upload failed: %v", err)
			return err
		}

		ui.PrintSuccess("Uploaded: %s", result.Version)
		ui.Println()
	}

	// Use shared execution logic with CLI-specific progress callback
	ui.StartSpinner("Starting test execution...")

	// Track if we've shown the report link yet
	reportLinkShown := false

	// Set up signal handling for graceful cancellation
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	// Track task ID for cancellation
	var taskID string
	var cancelled bool

	// Handle signals in background
	go func() {
		select {
		case <-sigChan:
			ui.StopSpinner()
			ui.Println()
			ui.PrintWarning("Cancelling test...")
			cancelled = true
			if taskID != "" {
				cancelClient := api.NewClientWithDevMode(apiKey, devMode)
				_, cancelErr := cancelClient.CancelTest(context.Background(), taskID)
				if cancelErr != nil {
					ui.PrintError("Failed to cancel test: %v", cancelErr)
				} else {
					ui.PrintInfo("Test cancellation requested")
				}
			}
			cancel() // Cancel the context to stop monitoring
		case <-ctx.Done():
			return
		}
	}()

	result, err := execution.RunTest(ctx, apiKey, cfg, execution.RunTestParams{
		TestNameOrID:   testNameOrID,
		Retries:        runRetries,
		BuildVersionID: runBuildID,
		Timeout:        effectiveTimeout,
		DevMode:        devMode,
		Latitude:       lat,
		Longitude:      lng,
		HasLocation:    hasLocation,
		OnTaskStarted: func(id string) {
			taskID = id
		},
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
				ui.PrintBasicStatus(status.Status, status.Progress, status.CurrentStep, status.CompletedSteps, status.TotalSteps)
			}
		},
	})
	ui.StopSpinner()

	// Handle cancellation
	if cancelled {
		ui.Println()
		ui.PrintWarning("Test cancelled by user")
		return fmt.Errorf("test cancelled")
	}

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
		if effectiveOpen {
			ui.OpenBrowser(result.ReportURL)
		}
		return nil
	}

	// Show final result
	switch {
	case result.Success:
		if runOutputJSON || runGitHubActions {
			outputTestResultJSON(result)
		} else {
			ui.PrintTestResult(result.TestName, "passed", result.ReportURL, "")
			ui.Println()
			ui.PrintSuccess("Test completed successfully!")
			ui.PrintNextSteps([]ui.NextStep{
				{Label: "View report:", Command: fmt.Sprintf("revyl test report %s", testNameOrID)},
				{Label: "View history:", Command: fmt.Sprintf("revyl test history %s", testNameOrID)},
			})
		}
	case result.Status == "cancelled":
		if runOutputJSON || runGitHubActions {
			outputTestResultJSON(result)
		} else {
			ui.PrintTestResult(result.TestName, "cancelled", result.ReportURL, "")
			ui.Println()
			ui.PrintWarning("Test was cancelled")
		}
	case result.Status == "timeout":
		if runOutputJSON || runGitHubActions {
			outputTestResultJSON(result)
		} else {
			ui.PrintTestResult(result.TestName, "timeout", result.ReportURL, result.ErrorMessage)
			ui.Println()
			ui.PrintWarning("Test timed out")
			ui.PrintNextSteps([]ui.NextStep{
				{Label: "Re-run with verbose:", Command: fmt.Sprintf("revyl test run %s -v", testNameOrID)},
			})
		}
	default:
		if runOutputJSON || runGitHubActions {
			outputTestResultJSON(result)
		} else {
			ui.PrintTestResult(result.TestName, "failed", result.ReportURL, result.ErrorMessage)
			ui.Println()
			ui.PrintError("Test failed")
			ui.PrintNextSteps([]ui.NextStep{
				{Label: "View report:", Command: fmt.Sprintf("revyl test report %s", testNameOrID)},
				{Label: "Re-run with verbose:", Command: fmt.Sprintf("revyl test run %s -v", testNameOrID)},
			})
		}
	}

	if effectiveOpen {
		ui.PrintInfo("Opening report in browser...")
		ui.OpenBrowser(result.ReportURL)
	}

	if !result.Success {
		switch result.Status {
		case "cancelled":
			return fmt.Errorf("test was cancelled")
		case "timeout":
			return fmt.Errorf("test timed out")
		default:
			return fmt.Errorf("test failed")
		}
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
	// Validate retries range
	if runRetries < minRetries || runRetries > maxRetries {
		return fmt.Errorf("--retries must be between %d and %d (got %d)", minRetries, maxRetries, runRetries)
	}

	// Honor global --json (root persistent) and local --json
	if v, _ := cmd.Flags().GetBool("json"); v {
		runOutputJSON = true
	}
	if v, _ := cmd.Root().PersistentFlags().GetBool("json"); v {
		runOutputJSON = true
	}
	workflowNameOrID := args[0]

	// Check authentication
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Load project config for alias resolution
	cwd, _ := os.Getwd()
	cfg, _ := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))
	effectiveOpen := resolveRunOpen(cmd, cfg, runOpen)
	effectiveTimeout := resolveRunTimeout(cmd, cfg, runTimeout)

	// Resolve workflow ID from alias for display
	workflowID := workflowNameOrID
	var isAlias bool
	if cfg != nil {
		if id, ok := cfg.Workflows[workflowNameOrID]; ok {
			workflowID = id
			isAlias = true
			ui.PrintInfo("Resolved '%s' to workflow ID: %s", workflowNameOrID, workflowID)
		}
	}

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")

	// Validate workflow exists before building (fail fast) - only if --build is set
	if runWorkflowBuild && !isAlias {
		if !looksLikeUUID(workflowNameOrID) {
			// Not an alias and not a UUID - likely a typo
			var availableWorkflows []string
			if cfg != nil && cfg.Workflows != nil {
				for name := range cfg.Workflows {
					availableWorkflows = append(availableWorkflows, name)
				}
			}
			errMsg := fmt.Sprintf("workflow '%s' not found in config", workflowNameOrID)
			if len(availableWorkflows) > 0 {
				errMsg += fmt.Sprintf(". Available workflows: %v", availableWorkflows)
			}
			errMsg += "\n\nHint: Run 'revyl test remote' to see all available tests/workflows."
			ui.PrintError("%s", errMsg)
			return fmt.Errorf("workflow not found")
		}
		// It's a UUID format - verify it exists via API before building
		validationClient := api.NewClientWithDevMode(apiKey, devMode)
		_, err := validationClient.GetWorkflow(cmd.Context(), workflowID)
		if err != nil {
			ui.PrintError("workflow '%s' not found: %v", workflowNameOrID, err)
			return fmt.Errorf("workflow not found")
		}
	}

	// Validate app IDs exist before running
	if runWorkflowIOSAppID != "" || runWorkflowAndroidAppID != "" {
		appClient := api.NewClientWithDevMode(apiKey, devMode)
		if runWorkflowIOSAppID != "" {
			ui.StartSpinner("Validating iOS app...")
			_, appErr := appClient.GetApp(cmd.Context(), runWorkflowIOSAppID)
			ui.StopSpinner()
			if appErr != nil {
				ui.PrintError("iOS app '%s' not found", runWorkflowIOSAppID)
				return fmt.Errorf("invalid --ios-app ID")
			}
		}
		if runWorkflowAndroidAppID != "" {
			ui.StartSpinner("Validating Android app...")
			_, appErr := appClient.GetApp(cmd.Context(), runWorkflowAndroidAppID)
			ui.StopSpinner()
			if appErr != nil {
				ui.PrintError("Android app '%s' not found", runWorkflowAndroidAppID)
				return fmt.Errorf("invalid --android-app ID")
			}
		}
	}

	ui.PrintBanner(version)
	ui.PrintInfo("Running Workflow")
	ui.Println()
	ui.PrintInfo("Workflow ID: %s", workflowID)
	if runRetries > 1 {
		ui.PrintInfo("Retries: %d", runRetries)
	}
	if runWorkflowIOSAppID != "" {
		ui.PrintInfo("iOS App Override: %s", runWorkflowIOSAppID)
	}
	if runWorkflowAndroidAppID != "" {
		ui.PrintInfo("Android App Override: %s", runWorkflowAndroidAppID)
	}

	// Parse --location flag for workflow
	var wfHasLocation bool
	var wfLat, wfLng float64
	if runLocation != "" {
		var parseErr error
		wfLat, wfLng, parseErr = parseLocation(runLocation)
		if parseErr != nil {
			return parseErr
		}
		wfHasLocation = true
		ui.PrintInfo("Location Override: %.6f, %.6f", wfLat, wfLng)
	}

	if devMode {
		ui.PrintInfo("Mode: Development (localhost)")
	}
	ui.Println()

	// Handle --build flag: build and upload before running workflow
	if runWorkflowBuild {
		if cfg == nil {
			ui.PrintError("Project not initialized. Run 'revyl init' first.")
			return fmt.Errorf("project not initialized")
		}

		buildCfg := cfg.Build
		var platformCfg config.BuildPlatform

		if runWorkflowPlatform != "" {
			var ok bool
			platformCfg, ok = cfg.Build.Platforms[runWorkflowPlatform]
			if !ok {
				ui.PrintError("Unknown platform: %s", runWorkflowPlatform)
				return fmt.Errorf("unknown platform: %s", runWorkflowPlatform)
			}
			buildCfg.Command = platformCfg.Command
			buildCfg.Output = platformCfg.Output
		}

		if buildCfg.Command == "" {
			ui.PrintError("No build command configured. Check .revyl/config.yaml")
			return fmt.Errorf("no build command")
		}

		// Step 1: Build
		ui.PrintBox("Building", buildCfg.Command)

		startTime := time.Now()
		runner := build.NewRunner(cwd)

		err = runner.Run(buildCfg.Command, func(line string) {
			ui.PrintDim("  %s", line)
		})

		buildDuration := time.Since(startTime)

		if err != nil {
			ui.Println()
			ui.PrintError("Build failed: %v", err)
			return err
		}

		ui.PrintSuccess("Build completed in %s", buildDuration.Round(time.Second))
		ui.Println()

		// Step 2: Upload
		artifactPath := filepath.Join(cwd, buildCfg.Output)
		if _, err := os.Stat(artifactPath); os.IsNotExist(err) {
			ui.PrintError("Build artifact not found: %s", buildCfg.Output)
			return fmt.Errorf("artifact not found")
		}

		buildVersionStr := build.GenerateVersionString()
		metadata := build.CollectMetadata(cwd, buildCfg.Command, runWorkflowPlatform, buildDuration)

		ui.PrintBox("Uploading", filepath.Base(buildCfg.Output))

		client := api.NewClientWithDevMode(apiKey, devMode)
		result, err := client.UploadBuild(cmd.Context(), &api.UploadBuildRequest{
			AppID:    platformCfg.AppID,
			Version:  buildVersionStr,
			FilePath: artifactPath,
			Metadata: metadata,
		})

		if err != nil {
			ui.PrintError("Upload failed: %v", err)
			return err
		}

		ui.PrintSuccess("Uploaded: %s", result.Version)
		ui.Println()
	}

	// Use shared execution logic
	ui.StartSpinner("Starting workflow execution...")

	// Track if we've shown the report link yet
	reportLinkShown := false

	// Set up signal handling for graceful cancellation
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	// Track task ID for cancellation
	var taskID string
	var cancelled bool

	// Handle signals in background
	go func() {
		select {
		case <-sigChan:
			ui.StopSpinner()
			ui.Println()
			ui.PrintWarning("Cancelling workflow...")
			cancelled = true
			if taskID != "" {
				cancelClient := api.NewClientWithDevMode(apiKey, devMode)
				_, cancelErr := cancelClient.CancelWorkflow(context.Background(), taskID)
				if cancelErr != nil {
					ui.PrintError("Failed to cancel workflow: %v", cancelErr)
				} else {
					ui.PrintInfo("Workflow cancellation requested")
				}
			}
			cancel() // Cancel the context to stop monitoring
		case <-ctx.Done():
			return
		}
	}()

	result, err := execution.RunWorkflow(ctx, apiKey, cfg, execution.RunWorkflowParams{
		WorkflowNameOrID: workflowNameOrID,
		Retries:          runRetries,
		Timeout:          effectiveTimeout,
		DevMode:          devMode,
		IOSAppID:         runWorkflowIOSAppID,
		AndroidAppID:     runWorkflowAndroidAppID,
		Latitude:         wfLat,
		Longitude:        wfLng,
		HasLocation:      wfHasLocation,
		OnTaskStarted: func(id string) {
			taskID = id
		},
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

	// Handle cancellation
	if cancelled {
		ui.Println()
		ui.PrintWarning("Workflow cancelled by user")
		return fmt.Errorf("workflow cancelled")
	}

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
		if effectiveOpen {
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

	if !(runOutputJSON || runGitHubActions) {
		if result.Success {
			ui.PrintNextSteps([]ui.NextStep{
				{Label: "View report:", Command: fmt.Sprintf("revyl workflow open %s", workflowNameOrID)},
			})
		} else {
			ui.PrintNextSteps([]ui.NextStep{
				{Label: "Re-run workflow:", Command: fmt.Sprintf("revyl workflow run %s", workflowNameOrID)},
				{Label: "Run verbose:", Command: fmt.Sprintf("revyl workflow run %s -v", workflowNameOrID)},
			})
		}
	}

	if effectiveOpen {
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

// runTestWithHotReload executes a test in hot reload mode.
//
// Hot reload mode:
//  1. Selects the appropriate provider (explicit, default, or auto-detected)
//  2. Starts a local dev server (Expo, Swift, or Android)
//  3. Creates a Cloudflare tunnel to expose it
//  4. Runs the test with a deep link URL to connect to the dev server
//  5. Keeps the dev server running for rapid iteration
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (test name or ID)
//
// Returns:
//   - error: Any error that occurred, or nil on success
func runTestWithHotReload(cmd *cobra.Command, args []string) error {
	// Honor global --json (root persistent) and local --json
	if v, _ := cmd.Flags().GetBool("json"); v {
		runOutputJSON = true
	}
	if v, _ := cmd.Root().PersistentFlags().GetBool("json"); v {
		runOutputJSON = true
	}
	testNameOrID := args[0]

	// Check authentication
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Load project config
	cwd, _ := os.Getwd()
	cfg, err := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))
	if err != nil {
		ui.PrintError("Failed to load project config: %v", err)
		ui.PrintInfo("Run 'revyl init' to initialize your project.")
		return fmt.Errorf("project not initialized")
	}
	effectiveOpen := resolveRunOpen(cmd, cfg, runOpen)
	effectiveTimeout := resolveRunTimeout(cmd, cfg, runTimeout)

	// Check hot reload configuration
	if !cfg.HotReload.IsConfigured() {
		ui.PrintError("Hot reload not configured.")
		ui.Println()
		ui.PrintInfo("Hot reload is configured during 'revyl init'.")
		ui.PrintInfo("Re-run init hot reload setup:")
		ui.PrintDim("  revyl init --hotreload")
		ui.Println()
		ui.PrintInfo("Or add to .revyl/config.yaml:")
		ui.Println()
		ui.PrintDim("  hotreload:")
		ui.PrintDim("    default: expo")
		ui.PrintDim("    providers:")
		ui.PrintDim("      expo:")
		ui.PrintDim("        app_scheme: \"your-app-scheme\"")
		ui.PrintDim("        platform_keys:")
		ui.PrintDim("          ios: \"ios-dev\"")
		ui.PrintDim("          android: \"android-dev\"")
		ui.PrintDim("        # use_exp_prefix: true  # Set to true if deep links fail with base scheme")
		ui.Println()
		return fmt.Errorf("hot reload not configured")
	}

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")

	// Select provider using registry
	registry := hotreload.DefaultRegistry()
	provider, providerCfg, err := registry.SelectProvider(&cfg.HotReload, runHotReloadProvider, cwd)
	if err != nil {
		ui.PrintError("Failed to select provider: %v", err)
		return err
	}

	// Defensive nil check for provider config
	if providerCfg == nil {
		ui.PrintError("Provider '%s' is not configured.", provider.Name())
		ui.Println()
		ui.PrintInfo("Re-run 'revyl init --hotreload' to configure hot reload defaults.")
		return fmt.Errorf("provider not configured")
	}

	// Check if provider is supported
	if !provider.IsSupported() {
		ui.PrintError("%s hot reload is not yet supported.", provider.DisplayName())
		return fmt.Errorf("%s not supported", provider.Name())
	}
	if provider.Name() != "expo" {
		ui.PrintError("Hot reload currently supports Expo projects only.")
		return fmt.Errorf("hot reload provider '%s' is not supported yet (expo only)", provider.Name())
	}

	// Override port if specified via flag
	if runHotReloadPort != 8081 {
		providerCfg.Port = runHotReloadPort
	}

	// Resolve build platform/device platform. Explicit --build-id can run without a platform mapping.
	platformKey := ""
	resolvedDevicePlatform := "ios"
	if runBuildID == "" || strings.TrimSpace(runTestPlatform) != "" {
		platformKey, resolvedDevicePlatform, err = resolveHotReloadBuildPlatform(cfg, providerCfg, runTestPlatform, "ios")
		if err != nil {
			ui.PrintError("Failed to resolve hot reload platform: %v", err)
			return err
		}
	}

	buildVersionID := ""
	buildSource := ""

	if runBuildID != "" {
		// 1. Explicit --build-id flag
		buildVersionID = runBuildID
		buildSource = "explicit"
	} else {
		if platformKey == "" {
			platformKey, resolvedDevicePlatform, err = resolveHotReloadBuildPlatform(cfg, providerCfg, runTestPlatform, "ios")
			if err != nil {
				ui.PrintError("Failed to resolve hot reload platform: %v", err)
				return err
			}
		}

		platformCfg, ok := cfg.Build.Platforms[platformKey]
		if !ok {
			return fmt.Errorf("platform key not found: %s", platformKey)
		}
		if platformCfg.AppID == "" {
			ui.PrintError("build.platforms.%s has no app_id configured.", platformKey)
			ui.Println()
			ui.PrintInfo("Run one of:")
			ui.PrintDim("  revyl init")
			ui.PrintDim("  revyl build upload --platform %s", platformKey)
			return fmt.Errorf("platform missing app_id: %s", platformKey)
		}

		client := api.NewClientWithDevMode(apiKey, devMode)
		latestVersion, latestErr := client.GetLatestBuildVersion(cmd.Context(), platformCfg.AppID)
		if latestErr != nil {
			ui.PrintError("Failed to get latest build version for platform '%s': %v", platformKey, latestErr)
			if diagnosis := diagnoseHotReloadNetworkError(latestErr); diagnosis != "" {
				ui.Println()
				ui.PrintDim("%s", diagnosis)
				ui.Println()
				ui.PrintInfo("Run 'revyl doctor' to verify API connectivity from this environment.")
			}
			return latestErr
		}
		if latestVersion != nil {
			buildVersionID = latestVersion.ID
			buildSource = fmt.Sprintf("platform:%s", platformKey)
		}
	}

	if buildVersionID == "" {
		ui.PrintError("No build versions found for platform '%s'.", platformKey)
		ui.Println()
		ui.PrintInfo("Upload a build first:")
		ui.PrintDim("  revyl build upload --platform %s", platformKey)
		return fmt.Errorf("no builds for platform: %s", platformKey)
	}

	// Validate provider config.
	if err := cfg.HotReload.ValidateProvider(provider.Name()); err != nil {
		ui.PrintError("Invalid hot reload configuration: %v", err)
		return err
	}

	// Resolve test ID from alias for display
	testID := testNameOrID
	if id, ok := cfg.Tests[testNameOrID]; ok {
		testID = id
		ui.PrintInfo("Resolved '%s' to test ID: %s", testNameOrID, testID)
	}

	ui.PrintBanner(version)
	ui.PrintInfo("Hot Reload Mode")
	ui.Println()

	// Show provider selection info
	if runHotReloadProvider != "" {
		ui.PrintInfo("Provider: %s (explicit)", provider.DisplayName())
	} else if cfg.HotReload.Default != "" {
		ui.PrintInfo("Provider: %s (default)", provider.DisplayName())
	} else {
		ui.PrintInfo("Provider: %s (auto-detected)", provider.DisplayName())
	}
	ui.PrintInfo("Device platform: %s", resolvedDevicePlatform)
	if platformKey != "" {
		ui.PrintInfo("Build platform key: %s", platformKey)
	}
	ui.PrintInfo("Dev client build: %s (%s)", buildVersionID, buildSource)
	ui.Println()

	// Create hot reload manager
	manager := hotreload.NewManager(provider.Name(), providerCfg, cwd)
	manager.SetLogCallback(func(msg string) {
		ui.PrintDim("  %s", msg)
	})

	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		ui.Println()
		ui.PrintInfo("Shutting down...")
		manager.Stop()
		cancel()
	}()

	// Start hot reload (dev server + tunnel)
	ui.PrintInfo("Starting hot reload...")
	ui.Println()

	result, err := manager.Start(ctx)
	if err != nil {
		ui.PrintError("Failed to start hot reload: %v", err)
		return err
	}

	// Ensure cleanup on exit
	defer manager.Stop()

	ui.Println()
	ui.PrintSuccess("Hot reload ready!")
	ui.Println()
	ui.PrintInfo("Tunnel URL: %s", result.TunnelURL)
	ui.PrintInfo("Deep Link: %s", result.DeepLinkURL)
	ui.Println()

	// Run the test with the deep link URL
	ui.PrintInfo("Running test: %s", testNameOrID)
	ui.Println()

	ui.StartSpinner("Starting test execution...")

	// Track if we've shown the report link yet
	reportLinkShown := false

	testResult, err := execution.RunTest(ctx, apiKey, cfg, execution.RunTestParams{
		TestNameOrID:   testNameOrID,
		Retries:        runRetries,
		BuildVersionID: buildVersionID,
		Timeout:        effectiveTimeout,
		DevMode:        devMode,
		LaunchURL:      result.DeepLinkURL,
		OnProgress: func(status *sse.TestStatus) {
			ui.StopSpinner()

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
				ui.PrintBasicStatus(status.Status, status.Progress, status.CurrentStep, status.CompletedSteps, status.TotalSteps)
			}
		},
	})
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Test execution failed: %v", err)
		return err
	}

	ui.Println()

	// Show final result
	switch {
	case testResult.Success:
		if runOutputJSON || runGitHubActions {
			outputTestResultJSON(testResult)
		} else {
			ui.PrintTestResult(testResult.TestName, "passed", testResult.ReportURL, "")
			ui.Println()
			ui.PrintSuccess("Test completed successfully!")
		}
	case testResult.Status == "cancelled":
		if runOutputJSON || runGitHubActions {
			outputTestResultJSON(testResult)
		} else {
			ui.PrintTestResult(testResult.TestName, "cancelled", testResult.ReportURL, "")
			ui.Println()
			ui.PrintWarning("Test was cancelled")
		}
	case testResult.Status == "timeout":
		if runOutputJSON || runGitHubActions {
			outputTestResultJSON(testResult)
		} else {
			ui.PrintTestResult(testResult.TestName, "timeout", testResult.ReportURL, testResult.ErrorMessage)
			ui.Println()
			ui.PrintWarning("Test timed out")
		}
	default:
		if runOutputJSON || runGitHubActions {
			outputTestResultJSON(testResult)
		} else {
			ui.PrintTestResult(testResult.TestName, "failed", testResult.ReportURL, testResult.ErrorMessage)
			ui.Println()
			ui.PrintError("Test failed")
		}
	}

	if effectiveOpen {
		ui.PrintInfo("Opening report in browser...")
		ui.OpenBrowser(testResult.ReportURL)
	}

	// Keep hot reload server running for rapid iteration
	ui.Println()
	ui.PrintInfo("────────────────────────────────────────────────────────────────")
	ui.PrintInfo("Hot reload server still running. Make code changes and run again.")
	ui.PrintDim("  Re-run:  revyl test run %s --hotreload", testNameOrID)
	ui.PrintInfo("Press Ctrl+C to stop.")
	ui.PrintInfo("────────────────────────────────────────────────────────────────")

	// Wait for interrupt signal
	<-sigChan

	if !testResult.Success {
		switch testResult.Status {
		case "cancelled":
			return fmt.Errorf("test was cancelled")
		case "timeout":
			return fmt.Errorf("test timed out")
		default:
			return fmt.Errorf("test failed")
		}
	}

	return nil
}

// parseLocation parses a "lat,lng" string into float64 values.
// Validates that latitude is in [-90, 90] and longitude is in [-180, 180].
func parseLocation(s string) (float64, float64, error) {
	parts := strings.SplitN(s, ",", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid --location format: expected lat,lng (e.g. 37.7749,-122.4194)")
	}

	lat, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid latitude: %v", err)
	}

	lng, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid longitude: %v", err)
	}

	if lat < -90 || lat > 90 {
		return 0, 0, fmt.Errorf("latitude must be between -90 and 90 (got %.6f)", lat)
	}
	if lng < -180 || lng > 180 {
		return 0, 0, fmt.Errorf("longitude must be between -180 and 180 (got %.6f)", lng)
	}

	return lat, lng, nil
}

// diagnoseHotReloadNetworkError maps common network failures to a user-friendly diagnosis.
func diagnoseHotReloadNetworkError(err error) string {
	if err == nil {
		return ""
	}

	errText := strings.ToLower(err.Error())

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) || strings.Contains(errText, "no such host") {
		return hotreload.DiagnoseAndSuggest(&hotreload.ConnectivityCheckResult{BlockedBy: "dns"})
	}

	var netErr net.Error
	if (errors.As(err, &netErr) && netErr.Timeout()) ||
		strings.Contains(errText, "i/o timeout") ||
		strings.Contains(errText, "connection timed out") {
		return hotreload.DiagnoseAndSuggest(&hotreload.ConnectivityCheckResult{BlockedBy: "firewall"})
	}

	if strings.Contains(errText, "connection refused") ||
		strings.Contains(errText, "tls handshake timeout") ||
		strings.Contains(errText, "proxyconnect") ||
		strings.Contains(errText, "x509") {
		return hotreload.DiagnoseAndSuggest(&hotreload.ConnectivityCheckResult{BlockedBy: "firewall"})
	}

	return ""
}
