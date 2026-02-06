// Package main provides run commands for executing tests and workflows.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/execution"
	"github.com/revyl/cli/internal/hotreload"
	_ "github.com/revyl/cli/internal/hotreload/providers" // Register providers
	"github.com/revyl/cli/internal/sse"
	"github.com/revyl/cli/internal/ui"
)

var (
	runRetries           int
	runBuildVersionID    string
	runNoWait            bool
	runOpen              bool
	runTimeout           int
	runOutputJSON        bool
	runGitHubActions     bool
	runVerbose           bool
	runTestBuild         bool
	runTestVariant       string
	runWorkflowBuild     bool
	runWorkflowVariant   string
	runHotReload         bool
	runHotReloadPort     int
	runHotReloadProvider string
)

// runTestExec executes a test using the shared execution package.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (test name or ID)
//
// Returns:
//   - error: Any error that occurred, or nil on success
func runTestExec(cmd *cobra.Command, args []string) error {
	// Honor global --json (root persistent) and local --json
	if v, _ := cmd.Flags().GetBool("json"); v {
		runOutputJSON = true
	}
	if v, _ := cmd.Root().PersistentFlags().GetBool("json"); v {
		runOutputJSON = true
	}
	// Check if hot reload mode is enabled
	if runHotReload {
		return runTestWithHotReload(cmd, args)
	}

	testNameOrID := args[0]

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds == nil || creds.APIKey == "" {
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

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")

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
		var variant config.BuildVariant

		if runTestVariant != "" {
			var ok bool
			variant, ok = cfg.Build.Variants[runTestVariant]
			if !ok {
				ui.PrintError("Unknown build variant: %s", runTestVariant)
				return fmt.Errorf("unknown variant: %s", runTestVariant)
			}
			buildCfg.Command = variant.Command
			buildCfg.Output = variant.Output
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
		metadata := build.CollectMetadata(cwd, buildCfg.Command, runTestVariant, buildDuration)

		ui.PrintBox("Uploading", filepath.Base(buildCfg.Output))

		client := api.NewClientWithDevMode(creds.APIKey, devMode)
		result, err := client.UploadBuild(cmd.Context(), &api.UploadBuildRequest{
			BuildVarID: variant.BuildVarID,
			Version:    buildVersionStr,
			FilePath:   artifactPath,
			Metadata:   metadata,
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
				cancelClient := api.NewClientWithDevMode(creds.APIKey, devMode)
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

	result, err := execution.RunTest(ctx, creds.APIKey, cfg, execution.RunTestParams{
		TestNameOrID:   testNameOrID,
		Retries:        runRetries,
		BuildVersionID: runBuildVersionID,
		Timeout:        runTimeout,
		DevMode:        devMode,
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
				ui.PrintBasicStatus(status.Status, status.Progress, status.CompletedSteps, status.TotalSteps)
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
	// Honor global --json (root persistent) and local --json
	if v, _ := cmd.Flags().GetBool("json"); v {
		runOutputJSON = true
	}
	if v, _ := cmd.Root().PersistentFlags().GetBool("json"); v {
		runOutputJSON = true
	}
	workflowNameOrID := args[0]

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds == nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Load project config for alias resolution
	cwd, _ := os.Getwd()
	cfg, _ := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))

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
		if !isValidUUID(workflowNameOrID) {
			// Not an alias and not a UUID - likely a typo
			availableWorkflows := getWorkflowNames(cfg.Workflows)
			errMsg := fmt.Sprintf("workflow '%s' not found in config", workflowNameOrID)
			if len(availableWorkflows) > 0 {
				errMsg += fmt.Sprintf(". Available workflows: %v", availableWorkflows)
			}
			errMsg += "\n\nHint: Run 'revyl test remote' to see all available tests/workflows."
			ui.PrintError(errMsg)
			return fmt.Errorf("workflow not found")
		}
		// It's a UUID format - verify it exists via API before building
		validationClient := api.NewClientWithDevMode(creds.APIKey, devMode)
		_, err := validationClient.GetWorkflow(cmd.Context(), workflowID)
		if err != nil {
			ui.PrintError("workflow '%s' not found: %v", workflowNameOrID, err)
			return fmt.Errorf("workflow not found")
		}
	}

	ui.PrintBanner(version)
	ui.PrintInfo("Running Workflow")
	ui.Println()
	ui.PrintInfo("Workflow ID: %s", workflowID)
	if runRetries > 1 {
		ui.PrintInfo("Retries: %d", runRetries)
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
		var variant config.BuildVariant

		if runWorkflowVariant != "" {
			var ok bool
			variant, ok = cfg.Build.Variants[runWorkflowVariant]
			if !ok {
				ui.PrintError("Unknown build variant: %s", runWorkflowVariant)
				return fmt.Errorf("unknown variant: %s", runWorkflowVariant)
			}
			buildCfg.Command = variant.Command
			buildCfg.Output = variant.Output
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
		metadata := build.CollectMetadata(cwd, buildCfg.Command, runWorkflowVariant, buildDuration)

		ui.PrintBox("Uploading", filepath.Base(buildCfg.Output))

		client := api.NewClientWithDevMode(creds.APIKey, devMode)
		result, err := client.UploadBuild(cmd.Context(), &api.UploadBuildRequest{
			BuildVarID: variant.BuildVarID,
			Version:    buildVersionStr,
			FilePath:   artifactPath,
			Metadata:   metadata,
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
				cancelClient := api.NewClientWithDevMode(creds.APIKey, devMode)
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

	result, err := execution.RunWorkflow(ctx, creds.APIKey, cfg, execution.RunWorkflowParams{
		WorkflowNameOrID: workflowNameOrID,
		Retries:          runRetries,
		Timeout:          runTimeout,
		DevMode:          devMode,
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
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds == nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Load project config
	cwd, _ := os.Getwd()
	cfg, err := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))
	if err != nil {
		ui.PrintError("Failed to load project config: %v", err)
		ui.PrintInfo("Run 'revyl init' to initialize your project.")
		return fmt.Errorf("project not initialized")
	}

	// Check hot reload configuration
	if !cfg.HotReload.IsConfigured() {
		ui.PrintError("Hot reload not configured.")
		ui.Println()
		ui.PrintInfo("To set up hot reload, run:")
		ui.PrintDim("  revyl hotreload setup")
		ui.Println()
		ui.PrintInfo("Or add to .revyl/config.yaml:")
		ui.Println()
		ui.PrintDim("  hotreload:")
		ui.PrintDim("    default: expo")
		ui.PrintDim("    providers:")
		ui.PrintDim("      expo:")
		ui.PrintDim("        dev_client_build_id: \"<your-dev-client-build-id>\"")
		ui.PrintDim("        app_scheme: \"your-app-scheme\"")
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
		ui.PrintInfo("Run 'revyl hotreload setup' to configure hot reload.")
		return fmt.Errorf("provider not configured")
	}

	// Check if provider is supported
	if !provider.IsSupported() {
		ui.PrintError("%s hot reload is not yet supported.", provider.DisplayName())
		return fmt.Errorf("%s not supported", provider.Name())
	}

	// Override port if specified via flag
	if runHotReloadPort != 8081 {
		providerCfg.Port = runHotReloadPort
	}

	// Resolve build version ID from flags or config
	// Priority: --build-version-id > --variant > providerCfg.DevClientBuildID
	buildVersionID := ""
	buildSource := ""

	if runBuildVersionID != "" {
		// 1. Explicit --build-version-id flag
		buildVersionID = runBuildVersionID
		buildSource = "explicit"
	} else if runTestVariant != "" {
		// 2. --variant flag: lookup from build.variants and get latest version
		variant, ok := cfg.Build.Variants[runTestVariant]
		if !ok {
			ui.PrintError("Build variant '%s' not found in config.", runTestVariant)
			ui.Println()
			ui.PrintInfo("Available variants:")
			for name := range cfg.Build.Variants {
				ui.PrintDim("  - %s", name)
			}
			return fmt.Errorf("variant not found: %s", runTestVariant)
		}
		if variant.BuildVarID == "" {
			ui.PrintError("Build variant '%s' has no build_var_id configured.", runTestVariant)
			ui.Println()
			ui.PrintInfo("Add build_var_id to your config:")
			ui.PrintDim("  build:")
			ui.PrintDim("    variants:")
			ui.PrintDim("      %s:", runTestVariant)
			ui.PrintDim("        build_var_id: \"<your-build-var-id>\"")
			return fmt.Errorf("variant missing build_var_id: %s", runTestVariant)
		}

		// Create API client to get latest version
		client := api.NewClientWithDevMode(creds.APIKey, devMode)
		latestVersion, err := client.GetLatestBuildVersion(cmd.Context(), variant.BuildVarID)
		if err != nil {
			ui.PrintError("Failed to get latest build version for variant '%s': %v", runTestVariant, err)
			return err
		}
		if latestVersion == nil {
			ui.PrintError("No build versions found for variant '%s'.", runTestVariant)
			ui.Println()
			ui.PrintInfo("Upload a build first:")
			ui.PrintDim("  revyl build upload <file> --name %s", runTestVariant)
			return fmt.Errorf("no builds for variant: %s", runTestVariant)
		}
		buildVersionID = latestVersion.ID
		buildSource = fmt.Sprintf("variant:%s", runTestVariant)
	} else if providerCfg.DevClientBuildID != "" {
		// 3. Fall back to config
		buildVersionID = providerCfg.DevClientBuildID
		buildSource = "config"
	} else {
		// 4. No build ID available
		ui.PrintError("No build specified for hot reload.")
		ui.Println()
		ui.PrintInfo("Specify a build using one of these options:")
		ui.PrintDim("  --variant <name>         Use latest from build.variants.<name>")
		ui.PrintDim("  --build-version-id <id>  Use explicit build version ID")
		ui.Println()
		ui.PrintInfo("Or configure dev_client_build_id in .revyl/config.yaml")
		return fmt.Errorf("no build specified")
	}

	// Update providerCfg with resolved build ID
	providerCfg.DevClientBuildID = buildVersionID

	// Validate provider config (now that we have build ID)
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
	ui.PrintInfo("Dev client: %s (%s)", providerCfg.DevClientBuildID, buildSource)
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

	testResult, err := execution.RunTest(ctx, creds.APIKey, cfg, execution.RunTestParams{
		TestNameOrID:   testNameOrID,
		Retries:        runRetries,
		BuildVersionID: providerCfg.DevClientBuildID,
		Timeout:        runTimeout,
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

	// Show final result
	if testResult.Success {
		if runOutputJSON || runGitHubActions {
			outputTestResultJSON(testResult)
		} else {
			ui.PrintTestResult(testResult.TestName, "passed", testResult.ReportURL, "")
			ui.Println()
			ui.PrintSuccess("Test completed successfully!")
		}
	} else {
		if runOutputJSON || runGitHubActions {
			outputTestResultJSON(testResult)
		} else {
			ui.PrintTestResult(testResult.TestName, "failed", testResult.ReportURL, testResult.ErrorMessage)
			ui.Println()
			ui.PrintError("Test failed")
		}
	}

	if runOpen {
		ui.PrintInfo("Opening report in browser...")
		ui.OpenBrowser(testResult.ReportURL)
	}

	// Keep hot reload server running for rapid iteration
	ui.Println()
	ui.PrintInfo("────────────────────────────────────────────────────────────────")
	ui.PrintInfo("Hot reload server still running. Make code changes and run again.")
	ui.PrintInfo("Press Ctrl+C to stop.")
	ui.PrintInfo("────────────────────────────────────────────────────────────────")

	// Wait for interrupt signal
	<-sigChan

	if !testResult.Success {
		return fmt.Errorf("test failed")
	}

	return nil
}
