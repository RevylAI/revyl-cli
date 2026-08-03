// Package main provides run commands for executing tests and workflows.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	startdevice "github.com/revyl/cli/internal/device"
	"github.com/revyl/cli/internal/devicetargets"
	"github.com/revyl/cli/internal/execution"
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
	runWorkflowIOSBuild     string
	runWorkflowAndroidBuild string
	runLocation             string
	runDeviceSelect         bool
	runDeviceModel          string
	runOsVersion            string
	runOrientation          string
	runFailFast             bool
	runLaunchEnv            []string
	runLaunchVars           []string
	runVars                 []string
)

// minRetries is the minimum allowed retry count.
const minRetries = 1

// maxRetries is the maximum allowed retry count.
const maxRetries = 5

const (
	runCancelRequestTimeout = 10 * time.Second
	runForceExitCode        = 130
)

const workflowBuildVersionValidationMaxPages = 500

var runInterruptExit = os.Exit
var runTestExecution = execution.RunTest
var runWorkflowExecution = execution.RunWorkflow
var runOpenBrowserFn = ui.OpenBrowser

// resolveRunOpen determines whether reports should auto-open.
// Explicit --open takes precedence over config defaults.
func resolveRunOpen(cmd *cobra.Command, cfg *config.ProjectConfig, flagValue bool) bool {
	if cmd != nil && cmd.Flags().Changed("open") {
		return flagValue
	}
	return config.EffectiveOpenBrowser(cfg)
}

// resolveRunTimeout determines the effective test/workflow execution timeout.
// Project defaults.timeout is reserved for CLI/device session timeouts.
func resolveRunTimeout(cmd *cobra.Command, cfg *config.ProjectConfig, flagValue int) int {
	return flagValue
}

type runInterruptState struct {
	taskIDMu  sync.RWMutex
	taskID    string
	cancelled atomic.Bool
}

func newRunInterruptState() *runInterruptState {
	return &runInterruptState{}
}

func (s *runInterruptState) SetTaskID(id string) {
	s.taskIDMu.Lock()
	s.taskID = strings.TrimSpace(id)
	s.taskIDMu.Unlock()
}

func (s *runInterruptState) TaskID() string {
	s.taskIDMu.RLock()
	defer s.taskIDMu.RUnlock()
	return s.taskID
}

func (s *runInterruptState) MarkCancelled() {
	s.cancelled.Store(true)
}

func (s *runInterruptState) Cancelled() bool {
	return s.cancelled.Load()
}

type runInterruptOptions struct {
	nounLower     string
	nounTitle     string
	requestCancel func(context.Context, string) error
	exitFunc      func(int)
}

func startRunInterruptHandler(
	ctx context.Context,
	cancel context.CancelFunc,
	sigChan <-chan os.Signal,
	state *runInterruptState,
	opts runInterruptOptions,
) func() {
	if state == nil {
		state = newRunInterruptState()
	}

	exitFunc := opts.exitFunc
	if exitFunc == nil {
		exitFunc = runInterruptExit
	}

	stopCh := make(chan struct{})
	var stopOnce sync.Once

	go func() {
		ctxDone := ctx.Done()
		interruptCount := 0
		for {
			select {
			case <-stopCh:
				return
			case <-ctxDone:
				// After first interrupt we intentionally keep listening for a second
				// interrupt to allow immediate force-exit while cancellation propagates.
				if state.Cancelled() {
					ctxDone = nil
					continue
				}
				return
			case _, ok := <-sigChan:
				if !ok {
					return
				}

				interruptCount++
				if interruptCount == 1 {
					ui.StopSpinner()
					ui.Println()
					ui.PrintWarning("Cancelling %s... (^C again to force-exit)", opts.nounLower)
					state.MarkCancelled()
					cancel()

					taskID := state.TaskID()
					if taskID != "" && opts.requestCancel != nil {
						go func(taskID string) {
							cancelCtx, cancelFn := context.WithTimeout(context.Background(), runCancelRequestTimeout)
							defer cancelFn()

							if err := opts.requestCancel(cancelCtx, taskID); err != nil {
								ui.PrintError("Failed to cancel %s: %v", opts.nounLower, err)
								return
							}
							ui.PrintInfo("%s cancellation requested", opts.nounTitle)
						}(taskID)
					}
					continue
				}

				ui.Println()
				ui.PrintWarning("Force exiting %s...", opts.nounLower)
				exitFunc(runForceExitCode)
				return
			}
		}
	}()

	return func() {
		stopOnce.Do(func() {
			close(stopCh)
		})
	}
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
	cfg, _, hasProjectConfig, err := loadProjectConfigOrEmpty(cwd)
	if err != nil {
		ui.PrintError("%v", err)
		return err
	}
	effectiveOpen := resolveRunOpen(cmd, cfg, runOpen)
	effectiveTimeout := resolveRunTimeout(cmd, cfg, runTimeout)

	testNameOrID := args[0]

	// Check authentication
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")

	validationClient := api.NewClientWithDevMode(apiKey, devMode)
	testID, _, err := resolveTestID(cmd.Context(), testNameOrID, cfg, validationClient)
	if err != nil {
		ui.PrintError("%v", err)
		fmt.Fprintln(os.Stderr, "  Run: revyl test list")
		return fmt.Errorf("test not found")
	}
	if looksLikeUUID(testNameOrID) {
		if _, err := validationClient.GetTest(cmd.Context(), testID); err != nil {
			ui.PrintError("test '%s' not found: %v", testNameOrID, err)
			fmt.Fprintln(os.Stderr, "  Run: revyl test list")
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
	if len(runLaunchVars) > 0 {
		ui.PrintInfo("Launch Vars: %s", strings.Join(runLaunchVars, ", "))
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

	// Resolve device selection (--device, --device-model, --os-version)
	var deviceModel, osVersion string
	if runDeviceModel != "" || runOsVersion != "" || runDeviceSelect {
		deviceModel, osVersion, err = resolveDeviceSelection(cmd, testID, validationClient, runDeviceSelect, runDeviceModel, runOsVersion)
		if err != nil {
			return err
		}
		if deviceModel != "" {
			ui.PrintInfo("Device: %s", devicetargets.FormatPairLabel(devicetargets.DevicePair{Model: deviceModel, Runtime: osVersion}))
		}
	}

	// Validate --orientation flag
	if runOrientation != "" && runOrientation != "portrait" && runOrientation != "landscape" {
		return fmt.Errorf("invalid --orientation value %q: must be 'portrait' or 'landscape'", runOrientation)
	}

	// Parse --launch-env KEY=VALUE flags
	launchEnvVars, err := parseLaunchEnvVars(runLaunchEnv)
	if err != nil {
		return err
	}
	if len(launchEnvVars) > 0 {
		ui.PrintInfo("Launch env vars: %d", len(launchEnvVars))
	}

	variableOverrides, err := parseRuntimeVars(runVars)
	if err != nil {
		return err
	}
	if len(variableOverrides) > 0 {
		ui.PrintInfo("Runtime Vars: %d", len(variableOverrides))
	}

	if devMode {
		ui.PrintInfo("Mode: Development (localhost)")
	}
	ui.Println()

	// Handle --build flag: build and upload before running test
	if runTestBuild {
		if !hasProjectConfig {
			printProjectNotInitialized()
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
			buildCfg.Command = platformCfg.JoinedBuildCommand()
			buildCfg.Output = platformCfg.Output
		}

		var buildCommands []string
		if trimmed := strings.TrimSpace(buildCfg.Command); trimmed != "" {
			buildCommands = []string{trimmed}
		}
		if runTestPlatform != "" {
			buildCommands = platformCfg.BuildCommands()
		}
		if len(buildCommands) == 0 {
			ui.PrintError("No build command configured for this platform.")
			fmt.Fprintln(os.Stderr, "  Run: revyl init --force")
			return fmt.Errorf("no build command")
		}

		// Step 1: Build
		ui.PrintBox("Building", buildCfg.Command)

		startTime := time.Now()
		runner := build.NewRunner(cwd)
		runner.Interactive = true

		for _, buildCommand := range buildCommands {
			err = runner.Run(buildCommand, func(line string) {
				ui.PrintDim("  %s", line)
			})
			if err != nil {
				break
			}
		}

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

	sigChan := make(chan os.Signal, 2)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	interruptState := newRunInterruptState()
	stopInterruptHandler := startRunInterruptHandler(ctx, cancel, sigChan, interruptState, runInterruptOptions{
		nounLower: "test",
		nounTitle: "Test",
		requestCancel: func(cancelCtx context.Context, taskID string) error {
			cancelClient := api.NewClientWithDevMode(apiKey, devMode)
			_, err := cancelClient.CancelTest(cancelCtx, taskID)
			return err
		},
	})
	defer stopInterruptHandler()

	var failFastPtr *bool
	if cmd.Flags().Changed("fail-fast") {
		v := runFailFast
		failFastPtr = &v
		ui.PrintInfo("Fail Fast: %v", v)
	}

	result, err := runTestExecution(ctx, apiKey, cfg, execution.RunTestParams{
		TestNameOrID:      testID,
		Retries:           runRetries,
		BuildVersionID:    runBuildID,
		Timeout:           effectiveTimeout,
		DevMode:           devMode,
		NoWait:            runNoWait,
		MonitoringMode:    sse.MonitoringModePolling,
		Latitude:          lat,
		Longitude:         lng,
		HasLocation:       hasLocation,
		DeviceModel:       deviceModel,
		OsVersion:         osVersion,
		Orientation:       runOrientation,
		FailFast:          failFastPtr,
		LaunchEnvVars:     launchEnvVars,
		VariableOverrides: variableOverrides,
		LaunchVars:        append([]string(nil), runLaunchVars...),
		OnTaskStarted: func(id string) {
			interruptState.SetTaskID(id)
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
	if interruptState.Cancelled() {
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
		if runOutputJSON || runGitHubActions {
			outputTestResultJSON(result)
			return nil
		}
		ui.PrintSuccess("Test queued successfully")
		ui.PrintInfo("Task ID: %s", result.TaskID)
		ui.PrintLink("Report", result.ReportURL)
		if effectiveOpen {
			runOpenBrowserFn(result.ReportURL)
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
		runOpenBrowserFn(result.ReportURL)
	}

	if !result.Success {
		switch result.Status {
		case "cancelled":
			return completedTestRunError(result, fmt.Errorf("test was cancelled"))
		case "timeout":
			return completedTestRunError(result, fmt.Errorf("test timed out"))
		default:
			return completedTestRunError(result, fmt.Errorf("test failed"))
		}
	}

	return completedTestRunError(result, nil)
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
		"error":       result.ErrorMessage,
	}

	data, _ := json.MarshalIndent(output, "", "  ")
	fmt.Println(string(data))
}

// validateWorkflowBuildVersion verifies that a build version exists for the given app,
// returning a helpful error listing recent versions if it does not.
func validateWorkflowBuildVersion(ctx context.Context, client *api.Client, appID, version, platformLabel string) error {
	const pageSize = 100

	ui.StartSpinner(fmt.Sprintf("Validating %s build version...", platformLabel))
	found := false
	var samples []string
	for page := 1; !found && page <= workflowBuildVersionValidationMaxPages; page++ {
		resp, err := client.ListBuildVersionsPage(ctx, appID, page, pageSize)
		if err != nil {
			ui.StopSpinner()
			return fmt.Errorf("failed to look up %s builds for app %s: %w", platformLabel, appID, err)
		}
		for _, v := range resp.Items {
			if v.Version == version {
				found = true
				break
			}
			if len(samples) < 5 {
				samples = append(samples, v.Version)
			}
		}
		if found || !hasNextWorkflowBuildVersionsPage(resp, page) {
			break
		}
	}
	ui.StopSpinner()

	if found {
		return nil
	}
	if len(samples) > 0 {
		ui.PrintError("%s build version %q not found for app %s (recent versions: %s)", platformLabel, version, appID, strings.Join(samples, ", "))
	} else {
		ui.PrintError("%s build version %q not found for app %s (app has no builds)", platformLabel, version, appID)
	}
	return fmt.Errorf("invalid --%s-build version", strings.ToLower(platformLabel))
}

func hasNextWorkflowBuildVersionsPage(resp *api.BuildVersionsPage, requestedPage int) bool {
	if resp == nil || len(resp.Items) == 0 {
		return false
	}
	if resp.HasNext {
		return true
	}
	if resp.TotalPages > 0 && requestedPage < resp.TotalPages {
		return true
	}
	return false
}

func queueWorkflowExecution(
	ctx context.Context,
	apiKey string,
	workflowID string,
	workflowName string,
	retries int,
	devMode bool,
	iosAppID string,
	androidAppID string,
	iosBuild string,
	androidBuild string,
	hasLocation bool,
	latitude float64,
	longitude float64,
	variableOverrides map[string]string,
	launchVars []string,
	launchEnvVars map[string]string,
) (*execution.RunWorkflowResult, error) {
	client := api.NewClientWithDevMode(apiKey, devMode)
	launchEnvVarIDs, err := startdevice.ResolveLaunchVarIDs(ctx, client, launchVars)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve launch variables: %w", err)
	}
	req := &api.ExecuteWorkflowRequest{
		WorkflowID:        workflowID,
		Retries:           retries,
		VariableOverrides: variableOverrides,
		LaunchEnvVarIds:   launchEnvVarIDs,
		LaunchEnvVars:     launchEnvVars,
	}
	if iosAppID != "" || androidAppID != "" {
		req.BuildConfig = &api.WorkflowAppConfig{}
		req.OverrideBuildConfig = true
		if iosAppID != "" {
			iosUUID, err := uuid.Parse(iosAppID)
			if err != nil {
				return nil, fmt.Errorf("invalid iOS app ID %q: %w", iosAppID, err)
			}
			iosApp := &api.PlatformApp{AppId: iosUUID}
			if iosBuild != "" {
				iosApp.PinnedVersion = &iosBuild
			}
			req.BuildConfig.IosBuild = iosApp
		}
		if androidAppID != "" {
			androidUUID, err := uuid.Parse(androidAppID)
			if err != nil {
				return nil, fmt.Errorf("invalid Android app ID %q: %w", androidAppID, err)
			}
			androidApp := &api.PlatformApp{AppId: androidUUID}
			if androidBuild != "" {
				androidApp.PinnedVersion = &androidBuild
			}
			req.BuildConfig.AndroidBuild = androidApp
		}
	}
	if hasLocation {
		req.LocationConfig = &api.CLILocation{
			Latitude:  latitude,
			Longitude: longitude,
		}
		req.OverrideLocation = true
	}

	resp, err := client.ExecuteWorkflow(ctx, req)
	if err != nil {
		return nil, err
	}

	reportURL := fmt.Sprintf("%s/workflows/report?taskId=%s", config.GetAppURL(devMode), resp.TaskID)
	return &execution.RunWorkflowResult{
		Success:      true,
		TaskID:       resp.TaskID,
		WorkflowID:   workflowID,
		WorkflowName: workflowName,
		Status:       "queued",
		ReportURL:    reportURL,
	}, nil
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
	if runOutputJSON || runGitHubActions {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
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

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Resolve workflow name or UUID via API
	workflowID, workflowName, err := resolveWorkflowID(cmd.Context(), workflowNameOrID, cfg, client)
	if err != nil {
		ui.PrintError("%v", err)
		return err
	}
	workflowDisplayName := workflowName
	if workflowDisplayName == "" {
		workflowDisplayName = workflowNameOrID
	}

	// Validate workflow exists before building (fail fast)
	if runWorkflowBuild {
		if _, err := client.GetWorkflow(cmd.Context(), workflowID); err != nil {
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

	// Validate build-version overrides: app-scoped, so they require the matching app.
	if runWorkflowIOSBuild != "" || runWorkflowAndroidBuild != "" {
		buildClient := api.NewClientWithDevMode(apiKey, devMode)
		if runWorkflowIOSBuild != "" {
			if runWorkflowIOSAppID == "" {
				ui.PrintError("--ios-build requires --ios-app")
				return fmt.Errorf("--ios-build requires --ios-app")
			}
			if err := validateWorkflowBuildVersion(cmd.Context(), buildClient, runWorkflowIOSAppID, runWorkflowIOSBuild, "iOS"); err != nil {
				return err
			}
		}
		if runWorkflowAndroidBuild != "" {
			if runWorkflowAndroidAppID == "" {
				ui.PrintError("--android-build requires --android-app")
				return fmt.Errorf("--android-build requires --android-app")
			}
			if err := validateWorkflowBuildVersion(cmd.Context(), buildClient, runWorkflowAndroidAppID, runWorkflowAndroidBuild, "Android"); err != nil {
				return err
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
	if runWorkflowIOSBuild != "" {
		ui.PrintInfo("iOS Build Override: %s", runWorkflowIOSBuild)
	}
	if runWorkflowAndroidBuild != "" {
		ui.PrintInfo("Android Build Override: %s", runWorkflowAndroidBuild)
	}

	variableOverrides, err := parseRuntimeVars(runVars)
	if err != nil {
		return err
	}
	if len(variableOverrides) > 0 {
		ui.PrintInfo("Runtime Vars: %d", len(variableOverrides))
	}
	launchEnvVars, err := parseLaunchEnvVars(runLaunchEnv)
	if err != nil {
		return err
	}
	if len(runLaunchVars) > 0 {
		ui.PrintInfo("Stored Launch Vars: %d", len(runLaunchVars))
	}
	if len(launchEnvVars) > 0 {
		ui.PrintInfo("Inline Launch Vars: %d", len(launchEnvVars))
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
			printProjectNotInitialized()
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
			buildCfg.Command = platformCfg.JoinedBuildCommand()
			buildCfg.Output = platformCfg.Output
		}

		var buildCommands []string
		if trimmed := strings.TrimSpace(buildCfg.Command); trimmed != "" {
			buildCommands = []string{trimmed}
		}
		if runWorkflowPlatform != "" {
			buildCommands = platformCfg.BuildCommands()
		}
		if len(buildCommands) == 0 {
			ui.PrintError("No build command configured for this platform.")
			fmt.Fprintln(os.Stderr, "  Run: revyl init --force")
			return fmt.Errorf("no build command")
		}

		// Step 1: Build
		ui.PrintBox("Building", buildCfg.Command)

		startTime := time.Now()
		runner := build.NewRunner(cwd)
		runner.Interactive = true

		for _, buildCommand := range buildCommands {
			err = runner.Run(buildCommand, func(line string) {
				ui.PrintDim("  %s", line)
			})
			if err != nil {
				break
			}
		}

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

	if runNoWait {
		queuedResult, err := queueWorkflowExecution(
			cmd.Context(),
			apiKey,
			workflowID,
			workflowDisplayName,
			runRetries,
			devMode,
			runWorkflowIOSAppID,
			runWorkflowAndroidAppID,
			runWorkflowIOSBuild,
			runWorkflowAndroidBuild,
			wfHasLocation,
			wfLat,
			wfLng,
			variableOverrides,
			runLaunchVars,
			launchEnvVars,
		)
		if err != nil {
			ui.PrintError("Failed to queue workflow: %v", err)
			return err
		}

		ui.Println()
		if runOutputJSON || runGitHubActions {
			outputWorkflowResultJSON(queuedResult)
		} else {
			ui.PrintSuccess("Workflow queued successfully")
			ui.PrintInfo("Task ID: %s", queuedResult.TaskID)
			ui.PrintLink("Report", queuedResult.ReportURL)
		}
		if effectiveOpen {
			runOpenBrowserFn(queuedResult.ReportURL)
		}
		return nil
	}

	// Use shared execution logic
	ui.StartSpinner("Starting workflow execution...")

	// Track if we've shown the report link yet
	reportLinkShown := false

	// Set up signal handling for graceful cancellation
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sigChan := make(chan os.Signal, 2)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	interruptState := newRunInterruptState()
	stopInterruptHandler := startRunInterruptHandler(ctx, cancel, sigChan, interruptState, runInterruptOptions{
		nounLower: "workflow",
		nounTitle: "Workflow",
		requestCancel: func(cancelCtx context.Context, taskID string) error {
			cancelClient := api.NewClientWithDevMode(apiKey, devMode)
			_, err := cancelClient.CancelWorkflow(cancelCtx, taskID)
			return err
		},
	})
	defer stopInterruptHandler()

	result, err := runWorkflowExecution(ctx, apiKey, cfg, execution.RunWorkflowParams{
		WorkflowNameOrID:  workflowID,
		Retries:           runRetries,
		Timeout:           effectiveTimeout,
		DevMode:           devMode,
		MonitoringMode:    sse.MonitoringModePolling,
		IOSAppID:          runWorkflowIOSAppID,
		AndroidAppID:      runWorkflowAndroidAppID,
		IOSBuild:          runWorkflowIOSBuild,
		AndroidBuild:      runWorkflowAndroidBuild,
		Latitude:          wfLat,
		Longitude:         wfLng,
		HasLocation:       wfHasLocation,
		VariableOverrides: variableOverrides,
		LaunchVars:        runLaunchVars,
		LaunchEnvVars:     launchEnvVars,
		OnTaskStarted: func(id string) {
			interruptState.SetTaskID(id)
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

			var childInfo []ui.ChildTestInfo
			for _, ct := range status.ChildTests {
				childInfo = append(childInfo, ui.ChildTestInfo{
					TestName: ct.TestName,
					Platform: ct.Platform,
					Status:   ct.Status,
					Success:  ct.Success,
					Duration: ct.Duration,
				})
			}

			if runVerbose {
				ui.PrintVerboseWorkflowStatus(status.Status, status.CompletedTests, status.TotalTests,
					status.PassedTests, status.FailedTests, status.Duration, childInfo)
			} else {
				ui.PrintBasicWorkflowStatus(status.Status, status.CompletedTests, status.TotalTests, childInfo)
			}
		},
	})
	ui.StopSpinner()

	// Handle cancellation
	if interruptState.Cancelled() {
		ui.Println()
		ui.PrintWarning("Workflow cancelled by user")
		return fmt.Errorf("workflow cancelled")
	}

	if err != nil {
		ui.PrintError("Workflow execution failed: %v", err)
		return err
	}
	if result != nil && result.WorkflowName == "" {
		result.WorkflowName = workflowDisplayName
	}

	ui.Println()

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
		runOpenBrowserFn(result.ReportURL)
	}

	if !result.Success {
		// Return appropriate error based on status
		switch result.Status {
		case "cancelled":
			return completedWorkflowRunError(result, fmt.Errorf("workflow was cancelled"))
		case "timeout":
			return completedWorkflowRunError(result, fmt.Errorf("workflow timed out"))
		default:
			if result.FailedTests > 0 {
				return completedWorkflowRunError(result, fmt.Errorf("workflow had %d failed tests", result.FailedTests))
			}
			return completedWorkflowRunError(result, fmt.Errorf("workflow failed with status: %s", result.Status))
		}
	}

	return completedWorkflowRunError(result, nil)
}

// outputWorkflowResultJSON outputs workflow results as JSON for CI/CD integration.
//
// Parameters:
//   - result: The workflow execution result
func outputWorkflowResultJSON(result *execution.RunWorkflowResult) {
	output := map[string]interface{}{
		"success":         result.Success,
		"task_id":         result.TaskID,
		"workflow_id":     result.WorkflowID,
		"workflow_name":   result.WorkflowName,
		"status":          result.Status,
		"report_link":     result.ReportURL,
		"total_tests":     result.TotalTests,
		"completed_tests": result.CompletedTests,
		"passed_tests":    result.PassedTests,
		"failed_tests":    result.FailedTests,
		"duration":        result.Duration,
		"error":           result.ErrorMessage,
	}
	if result.Status == "queued" {
		output["queued"] = true
	}

	tests := make([]map[string]interface{}, 0, len(result.Tests))
	for _, t := range result.Tests {
		entry := map[string]interface{}{
			"test_name":     t.TestName,
			"platform":      t.Platform,
			"status":        t.Status,
			"success":       t.Success,
			"duration":      t.Duration,
			"error_message": t.ErrorMessage,
		}
		tests = append(tests, entry)
	}
	output["tests"] = tests

	data, _ := json.MarshalIndent(output, "", "  ")
	fmt.Println(string(data))
}

func completedTestRunError(result *execution.RunTestResult, err error) error {
	if err == nil {
		return nil
	}
	if result == nil || strings.TrimSpace(result.TaskID) == "" {
		return err
	}

	return analytics.CompletedWithExitCode(err, analytics.CommandCompletion{
		ExitCode:     1,
		Domain:       "test_run",
		DomainStatus: failedDomainStatus(result.Status),
		Properties: map[string]interface{}{
			"test_task_id": result.TaskID,
			"test_id":      result.TestID,
			"test_status":  strings.TrimSpace(result.Status),
			"test_success": result.Success,
		},
	})
}

func completedWorkflowRunError(result *execution.RunWorkflowResult, err error) error {
	if err == nil {
		return nil
	}
	if result == nil || strings.TrimSpace(result.TaskID) == "" {
		return err
	}

	return analytics.CompletedWithExitCode(err, analytics.CommandCompletion{
		ExitCode:     1,
		Domain:       "workflow_run",
		DomainStatus: failedDomainStatus(result.Status),
		Properties: map[string]interface{}{
			"workflow_task_id":         result.TaskID,
			"workflow_id":              result.WorkflowID,
			"workflow_status":          strings.TrimSpace(result.Status),
			"workflow_success":         result.Success,
			"workflow_total_tests":     result.TotalTests,
			"workflow_completed_tests": result.CompletedTests,
			"workflow_passed_tests":    result.PassedTests,
			"workflow_failed_tests":    result.FailedTests,
		},
	})
}

func failedDomainStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" || status == "completed" {
		return "failed"
	}
	return status
}

// resolveDeviceSelection resolves the target device pair from flags or an
// interactive picker. When interactive is true it fetches the test's platform
// via the API and presents a bubbletea selection menu. When deviceModel and
// osVersion are provided directly they are validated against the target matrix.
//
// Parameters:
//   - cmd: cobra command (used for context)
//   - testID: resolved test UUID (needed to look up platform for interactive mode)
//   - client: API client for fetching test info
//   - interactive: whether to show the interactive device picker
//   - deviceModel: explicit device model flag value (may be empty)
//   - osVersion: explicit OS version flag value (may be empty)
//
// Returns:
//   - model: resolved device model (empty string means use default)
//   - runtime: resolved OS runtime
//   - error: validation or selection error
func resolveDeviceSelection(
	cmd *cobra.Command,
	testID string,
	client *api.Client,
	interactive bool,
	deviceModel string,
	osVersion string,
) (string, string, error) {
	// Non-interactive: validate the explicit pair
	if !interactive {
		if deviceModel == "" && osVersion == "" {
			return "", "", nil
		}
		if deviceModel == "" || osVersion == "" {
			return "", "", fmt.Errorf("--device-model and --os-version must both be provided")
		}
	}

	// Fetch the test once to determine the target platform.
	test, err := client.GetTest(cmd.Context(), testID)
	if err != nil {
		if interactive {
			return "", "", fmt.Errorf("failed to fetch test for device selection: %w", err)
		}
		return "", "", fmt.Errorf("failed to fetch test for device validation: %w", err)
	}

	targetCatalog := loadRuntimeDeviceTargetCatalog(cmd.Context(), client)
	if !interactive {
		if err := targetCatalog.ValidateDevicePair(test.Platform, deviceModel, osVersion); err != nil {
			return "", "", err
		}
		return deviceModel, osVersion, nil
	}

	pairs, err := targetCatalog.GetAvailableTargetPairs(test.Platform)
	if err != nil {
		return "", "", err
	}
	defaultPair, _ := targetCatalog.GetDefaultPair(test.Platform)

	options := make([]ui.SelectOption, 0, len(pairs)+1)
	options = append(options, ui.SelectOption{
		Label:       fmt.Sprintf("Auto (%s)", devicetargets.FormatPairLabel(defaultPair)),
		Value:       "auto",
		Description: "Use platform default",
	})
	for _, p := range pairs {
		options = append(options, ui.SelectOption{
			Label: devicetargets.FormatPairLabel(p),
			Value: fmt.Sprintf("%s|%s", p.Model, p.Runtime),
		})
	}

	_, selected, err := ui.Select("Select device:", options, 0)
	if err != nil {
		return "", "", fmt.Errorf("device selection failed: %w", err)
	}
	if selected == "auto" {
		return "", "", nil
	}

	parts := strings.SplitN(selected, "|", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected device selection value: %q", selected)
	}
	return parts[0], parts[1], nil
}

// parseRuntimeVars parses repeatable --var KEY=VALUE flags into a map.
// Values may contain '='; empty values are allowed.
func parseRuntimeVars(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, raw := range pairs {
		kv := strings.SplitN(raw, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid --var %q: expected KEY=VALUE", raw)
		}
		key := strings.TrimSpace(kv[0])
		if key == "" {
			return nil, fmt.Errorf("invalid --var %q: empty key", raw)
		}
		if !isValidVariableName(key) {
			return nil, fmt.Errorf(
				"invalid --var %q: key %q must use letters, numbers, hyphens, or underscores; "+
					"hyphens and underscores cannot be first, last, or adjacent",
				raw,
				key,
			)
		}
		out[key] = kv[1]
	}
	return out, nil
}

// parseLaunchEnvVars parses repeatable --launch-env KEY=VALUE flags into a map.
// Only the first '=' splits, so values may contain '='; empty input returns nil.
func parseLaunchEnvVars(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, raw := range pairs {
		kv := strings.SplitN(raw, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid --launch-env %q: expected KEY=VALUE", raw)
		}
		key := strings.TrimSpace(kv[0])
		if key == "" {
			return nil, fmt.Errorf("invalid --launch-env %q: empty key", raw)
		}
		if !isValidLaunchEnvKey(key) {
			return nil, fmt.Errorf(
				"invalid --launch-env %q: key %q must match [A-Za-z_][A-Za-z0-9_.-]*",
				raw,
				key,
			)
		}
		if isReservedLaunchEnvKey(key) {
			return nil, fmt.Errorf(
				"invalid --launch-env %q: key %q uses a reserved prefix "+
					"(DYLD_, SIMCTL_CHILD_, COGNISIM_) and is not allowed",
				raw,
				key,
			)
		}
		out[key] = kv[1]
	}
	return out, nil
}

func isValidLaunchEnvKey(key string) bool {
	if key == "" {
		return false
	}

	for i := 0; i < len(key); i++ {
		ch := key[i]
		isLetter := (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z')
		isDigit := ch >= '0' && ch <= '9'
		isUnderscore := ch == '_'

		if i == 0 {
			if !isLetter && !isUnderscore {
				return false
			}
			continue
		}

		isDotOrHyphen := ch == '.' || ch == '-'
		if !isLetter && !isDigit && !isUnderscore && !isDotOrHyphen {
			return false
		}
	}

	return true
}

// isReservedLaunchEnvKey blocks DYLD_/SIMCTL_CHILD_/COGNISIM_ keys, which reach
// dyld or our namespaces on the device launch path. Keep in sync with
// cognisim_schemas/utils/launch_env.py.
func isReservedLaunchEnvKey(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	return strings.HasPrefix(upper, "DYLD_") ||
		strings.HasPrefix(upper, "SIMCTL_CHILD_") ||
		strings.HasPrefix(upper, "COGNISIM_")
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
