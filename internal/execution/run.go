// Package execution provides shared execution logic for tests and workflows.
//
// This package contains the core execution functions used by both the CLI commands
// and the MCP server, ensuring consistent behavior and eliminating code duplication.
package execution

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	startdevice "github.com/revyl/cli/internal/device"
	"github.com/revyl/cli/internal/launcharguments"
	"github.com/revyl/cli/internal/launchvars"
	"github.com/revyl/cli/internal/sse"
	"github.com/revyl/cli/internal/status"
)

// DefaultRunTimeoutSeconds is the default execution timeout for test and workflow runs.
const DefaultRunTimeoutSeconds = 60 * 60

// testRunBuildReadTimeout bounds the read of the build a finished run used, so
// a slow report never holds the command's exit.
const testRunBuildReadTimeout = 10 * time.Second

// RunTestParams contains parameters for running a test.
//
// Fields:
//   - TestNameOrID: Test name (alias from config) or UUID
//   - Retries: Number of retry attempts (1-5)
//   - BuildVersionID: Optional specific build version ID
//   - Timeout: Timeout in seconds (default 3600)
//   - DevMode: If true, use local development servers
//   - MonitoringMode: Monitoring transport to use while waiting for completion
//   - OnProgress: Optional callback for progress updates
//   - OnTaskStarted: Optional callback called when task is created (provides task ID for cancellation)
//   - LaunchURL: Optional deep link URL for hot reload mode
type RunTestParams struct {
	TestNameOrID      string
	TestsDir          string
	Retries           int
	BuildVersionID    string
	BranchID          string
	BranchName        string
	ContextBranchName string
	BuildSelection    string
	Timeout           int
	DevMode           bool
	MonitoringMode    sse.MonitoringMode
	OnProgress        func(status *sse.TestStatus)
	// OnTaskStarted is called immediately after the test execution is started.
	// This provides the task ID early, enabling cancellation before monitoring completes.
	OnTaskStarted func(taskID string)
	// NoWait dispatches the run and returns as soon as the task is queued,
	// skipping the completion monitor. The result carries the TaskID +
	// ReportURL with Status "queued".
	NoWait bool
	// LaunchURL is the deep link URL for hot reload mode.
	// When provided, the test will launch the app via this URL instead of the normal app launch.
	LaunchURL string
	// LaunchVars are org launch variable keys or IDs to apply to this run.
	LaunchVars []string
	// LaunchArgSets are stored iOS argument-set names or IDs.
	LaunchArgSets []string
	// DisableInheritedLaunchVars omits launch variables inherited from the
	// current process environment.
	DisableInheritedLaunchVars bool
	// Location fields for initial GPS location at execution time.
	Latitude    float64
	Longitude   float64
	HasLocation bool
	// DeviceModel overrides the target device model (e.g. "iPhone 16").
	DeviceModel string
	// OsVersion overrides the target OS runtime (e.g. "iOS 18.5").
	OsVersion string
	// Orientation sets the initial device orientation ("portrait" or "landscape").
	Orientation string
	// FailFast halts the run on the first failed step or validation. When nil
	// the backend uses the test's stored run_config; when set it overrides
	// for this run only.
	FailFast *bool
	// LaunchEnvVars are inline launch environment variables (KEY=VALUE) applied
	// to the app's launch environment for this run. Merged over org launch
	// variables attached to the test; inline values take precedence.
	LaunchEnvVars map[string]string
	// LaunchArguments are non-secret inline iOS app arguments in exact order.
	LaunchArguments []string
	// VariableOverrides are test variable values applied to this run. They are
	// referenced as {{name}} and override test/global variables for that run only.
	VariableOverrides map[string]string
}

// RunTestResult contains the result of a test run.
//
// Fields:
//   - Success: Whether the test passed
//   - TaskID: The execution task ID
//   - TestID: The test UUID
//   - TestName: The test name
//   - Status: Final status string
//   - Duration: Execution duration
//   - ReportURL: URL to the test report
//   - ErrorMessage: Error message if failed
type RunTestResult struct {
	Success      bool   `json:"success"`
	TaskID       string `json:"task_id"`
	TestID       string `json:"test_id"`
	TestName     string `json:"test_name"`
	Status       string `json:"status"`
	Duration     string `json:"duration"`
	ReportURL    string `json:"report_url"`
	ErrorMessage string `json:"error_message,omitempty"`
	BuildID      string `json:"build_id,omitempty"`
	BuildVersion string `json:"build_version,omitempty"`
	BranchName   string `json:"branch_name,omitempty"`
}

// RunTest executes a test and returns structured results.
//
// This is the shared implementation used by both CLI and MCP. It handles:
//   - Resolving test aliases to UUIDs
//   - Starting test execution via API
//   - Monitoring execution via SSE or polling
//   - Determining success/failure status
//
// Parameters:
//   - ctx: Context for cancellation
//   - apiKey: API key for authentication
//   - cfg: Project config for alias resolution (can be nil)
//   - params: Test execution parameters
//
// Returns:
//   - *RunTestResult: Execution result with status and report URL
//   - error: Any error that occurred (nil if result contains error info)
func RunTest(ctx context.Context, apiKey string, _ *config.ProjectConfig, params RunTestParams) (*RunTestResult, error) {
	if err := ValidateRuntimeBuildSelection(params.BranchID, params.BranchName, params.BuildSelection, params.BuildVersionID); err != nil {
		return nil, err
	}
	if err := launcharguments.Validate(params.LaunchArguments); err != nil {
		return nil, err
	}

	// Resolve test ID from local YAML
	testID := params.TestNameOrID
	if strings.TrimSpace(params.TestsDir) != "" {
		if id, ltErr := config.GetLocalTestRemoteID(params.TestsDir, params.TestNameOrID); ltErr == nil && id != "" {
			testID = id
		}
	}

	// Set defaults
	retries := params.Retries
	if retries == 0 {
		retries = 1
	}
	timeout := params.Timeout
	if timeout == 0 {
		timeout = DefaultRunTimeoutSeconds
	}

	// Create client and execute
	client := api.NewClientWithDevMode(apiKey, params.DevMode)
	inheritedLaunchConfigurationIDs, err := launchvars.LoadInheritedIDs(
		params.DisableInheritedLaunchVars,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load inherited launch variables: %w", err)
	}
	launchConfigurationIDs, err := startdevice.ResolveLaunchConfigurationSelection(
		ctx,
		client,
		inheritedLaunchConfigurationIDs,
		params.LaunchVars,
		params.LaunchArgSets,
	)
	if err != nil {
		return nil, err
	}
	req := &api.ExecuteTestRequest{
		TestID:            testID,
		Retries:           retries,
		BuildVersionID:    params.BuildVersionID,
		BranchID:          params.BranchID,
		BranchName:        params.BranchName,
		ContextBranchName: params.ContextBranchName,
		BuildSelection:    api.BuildSelectionMode(params.BuildSelection),
		LaunchURL:         params.LaunchURL,
		LaunchEnvVarIds:   launchConfigurationIDs,
		DeviceModel:       params.DeviceModel,
		OsVersion:         params.OsVersion,
		LaunchEnvVars:     params.LaunchEnvVars,
		LaunchArguments:   params.LaunchArguments,
		VariableOverrides: params.VariableOverrides,
	}
	if params.HasLocation || params.Orientation != "" || params.FailFast != nil {
		runCfg := &api.CLIRunConfig{}
		if params.HasLocation || params.Orientation != "" {
			execMode := &api.CLIExecutionMode{}
			if params.HasLocation {
				execMode.InitialLocation = &api.CLILocation{
					Latitude:  params.Latitude,
					Longitude: params.Longitude,
				}
			}
			if params.Orientation != "" {
				execMode.InitialOrientation = params.Orientation
			}
			runCfg.ExecutionMode = execMode
		}
		if params.FailFast != nil {
			runCfg.FailFast = params.FailFast
		}
		req.RunConfig = runCfg
	}
	resp, err := client.ExecuteTest(ctx, req)
	if err != nil {
		return &RunTestResult{
			Success:      false,
			TestID:       testID,
			ErrorMessage: err.Error(),
		}, nil
	}

	// Notify caller of task ID immediately for cancellation support
	if params.OnTaskStarted != nil {
		params.OnTaskStarted(resp.TaskID)
	}

	// No-wait: the run is dispatched; return without monitoring to completion.
	if params.NoWait {
		reportURL := fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(params.DevMode), url.QueryEscape(resp.TaskID))
		return &RunTestResult{
			TaskID:    resp.TaskID,
			TestID:    testID,
			Status:    "queued",
			ReportURL: reportURL,
		}, nil
	}

	// Monitor execution
	monitor := sse.NewMonitorWithDevMode(apiKey, timeout, params.DevMode)
	finalStatus, err := monitor.MonitorTestWithMode(ctx, resp.TaskID, testID, params.MonitoringMode, params.OnProgress)
	if err != nil {
		// If we have a valid final status (e.g., cancelled via frontend while context was cancelled),
		// prefer using it over reporting a generic error
		if finalStatus != nil && status.IsTerminal(finalStatus.Status) {
			reportURL := fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(params.DevMode), url.QueryEscape(resp.TaskID))
			result := &RunTestResult{
				Success:      status.IsSuccess(finalStatus.Status, finalStatus.Success, finalStatus.ErrorMessage),
				TaskID:       resp.TaskID,
				TestID:       testID,
				TestName:     finalStatus.TestName,
				Status:       finalStatus.Status,
				Duration:     finalStatus.Duration,
				ReportURL:    reportURL,
				ErrorMessage: finalStatus.ErrorMessage,
			}
			attachTestRunBuild(ctx, client, result)
			return result, nil
		}
		return &RunTestResult{
			Success:      false,
			TaskID:       resp.TaskID,
			Status:       "cancelled",
			ErrorMessage: err.Error(),
		}, nil
	}

	reportURL := fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(params.DevMode), url.QueryEscape(resp.TaskID))

	result := &RunTestResult{
		Success:      status.IsSuccess(finalStatus.Status, finalStatus.Success, finalStatus.ErrorMessage),
		TaskID:       resp.TaskID,
		TestID:       testID,
		TestName:     finalStatus.TestName,
		Status:       finalStatus.Status,
		Duration:     finalStatus.Duration,
		ReportURL:    reportURL,
		ErrorMessage: finalStatus.ErrorMessage,
	}
	attachTestRunBuild(ctx, client, result)
	return result, nil
}

// attachTestRunBuild records the branch and build a finished test run used,
// read from its report. A failed read leaves them empty; the run result stands.
// The read outlives a cancelled run context, so a run stopped from the
// dashboard still reports its build, within testRunBuildReadTimeout.
func attachTestRunBuild(ctx context.Context, client *api.Client, result *RunTestResult) {
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), testRunBuildReadTimeout)
	defer cancel()
	envelope, err := client.GetReportContextByExecution(readCtx, result.TaskID, false, false, false)
	if err != nil {
		return
	}
	report := envelope.Report
	if report.ResolvedBuildId != nil {
		result.BuildID = *report.ResolvedBuildId
	}
	if report.BuildVersion != nil {
		result.BuildVersion = *report.BuildVersion
	}
	if report.BranchName != nil {
		result.BranchName = *report.BranchName
	}
}

// RunWorkflowParams contains parameters for running a workflow.
//
// Fields:
//   - WorkflowNameOrID: Workflow name (alias from config) or UUID
//   - Retries: Number of retry attempts (1-5)
//   - Timeout: Timeout in seconds (default 3600)
//   - DevMode: If true, use local development servers
//   - MonitoringMode: Monitoring transport to use while waiting for completion
//   - OnProgress: Optional callback for progress updates
//   - OnTaskStarted: Optional callback called when task is created (provides task ID for cancellation)
type RunWorkflowParams struct {
	WorkflowNameOrID  string
	Retries           int
	Timeout           int
	DevMode           bool
	IOSAppID          string // Optional iOS app ID override
	AndroidAppID      string // Optional Android app ID override
	IOSBuild          string // Optional iOS build version to pin (requires IOSAppID)
	AndroidBuild      string // Optional Android build version to pin (requires AndroidAppID)
	IOSBuildID        string
	AndroidBuildID    string
	IOSBranchID       string
	AndroidBranchID   string
	BranchName        string
	ContextBranchName string
	BuildSelection    string
	NoWait            bool
	// Location fields for initial GPS location override.
	Latitude    float64
	Longitude   float64
	HasLocation bool
	// VariableOverrides are test variable values applied to every child test in
	// this workflow run. They are referenced as {{name}} and override test/global
	// variables for that run only.
	VariableOverrides map[string]string
	// LaunchVars are org launch variable keys or IDs applied to every child test.
	LaunchVars []string
	// LaunchArgSets are stored iOS argument-set names or IDs.
	LaunchArgSets []string
	// DisableInheritedLaunchVars omits launch variables inherited from the
	// current process environment.
	DisableInheritedLaunchVars bool
	// LaunchEnvVars are inline app launch values applied to every child test.
	// They override stored launch variables with the same key.
	LaunchEnvVars   map[string]string
	LaunchArguments []string
	MonitoringMode  sse.MonitoringMode
	OnProgress      func(status *sse.WorkflowStatus)
	// OnTaskStarted is called immediately after the workflow execution is started.
	// This provides the task ID early, enabling cancellation before monitoring completes.
	OnTaskStarted func(taskID string)
}

// WorkflowTestResult contains the result of an individual test within a workflow run.
//
// Fields:
//   - TestName: The name of the test
//   - Platform: Execution platform (ios, android)
//   - Status: Final status string (completed, failed, cancelled, timeout)
//   - Success: Whether the test passed
//   - Duration: Execution duration as a human-readable string
//   - ErrorMessage: Error message if the test failed
type WorkflowTestResult struct {
	BuildID      string `json:"build_id,omitempty"`
	BuildVersion string `json:"build_version,omitempty"`
	BranchID     string `json:"branch_id,omitempty"`
	BranchName   string `json:"branch_name,omitempty"`
	TestName     string `json:"test_name"`
	Platform     string `json:"platform"`
	Status       string `json:"status"`
	Success      bool   `json:"success"`
	Duration     string `json:"duration"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// RunWorkflowResult contains the result of a workflow run.
//
// Fields:
//   - Success: Whether all tests passed
//   - TaskID: The execution task ID
//   - WorkflowID: The workflow UUID
//   - WorkflowName: The workflow name
//   - Status: Final status string
//   - TotalTests: Total number of tests
//   - CompletedTests: Number of tests that finished (passed + failed)
//   - PassedTests: Number of passed tests
//   - FailedTests: Number of failed tests
//   - Duration: Execution duration
//   - ReportURL: URL to the workflow report
//   - ErrorMessage: Error message if failed
//   - Tests: Per-test results when available (populated from unified report)
type RunWorkflowResult struct {
	Success        bool                 `json:"success"`
	TaskID         string               `json:"task_id"`
	WorkflowID     string               `json:"workflow_id"`
	WorkflowName   string               `json:"workflow_name"`
	Status         string               `json:"status"`
	TotalTests     int                  `json:"total_tests"`
	CompletedTests int                  `json:"completed_tests"`
	PassedTests    int                  `json:"passed_tests"`
	FailedTests    int                  `json:"failed_tests"`
	Duration       string               `json:"duration"`
	ReportURL      string               `json:"report_url"`
	ErrorMessage   string               `json:"error_message,omitempty"`
	Tests          []WorkflowTestResult `json:"tests,omitempty"`
}

// RunWorkflow executes a workflow and returns structured results.
//
// This is the shared implementation used by both CLI and MCP. It handles:
//   - Resolving workflow aliases to UUIDs
//   - Starting workflow execution via API
//   - Monitoring execution via SSE or polling
//   - Determining success/failure status
//
// Parameters:
//   - ctx: Context for cancellation
//   - apiKey: API key for authentication
//   - cfg: Project config for alias resolution (can be nil)
//   - params: Workflow execution parameters
//
// Returns:
//   - *RunWorkflowResult: Execution result with status and report URL
//   - error: Any error that occurred (nil if result contains error info)
func RunWorkflow(ctx context.Context, apiKey string, _ *config.ProjectConfig, params RunWorkflowParams) (*RunWorkflowResult, error) {
	if err := ValidateRuntimeBuildSelection("", params.BranchName, params.BuildSelection, ""); err != nil {
		return nil, err
	}
	if params.IOSBranchID != "" && params.IOSBuildID == "" || params.AndroidBranchID != "" && params.AndroidBuildID == "" {
		return nil, fmt.Errorf("a frozen workflow branch requires its exact build ID")
	}
	if params.IOSBuildID != "" && params.IOSBuild != "" || params.AndroidBuildID != "" && params.AndroidBuild != "" {
		return nil, fmt.Errorf("choose an exact build ID or legacy version for each platform, not both")
	}
	if (params.IOSBuildID != "" || params.IOSBuild != "") && params.IOSAppID == "" {
		return nil, fmt.Errorf("an iOS build override requires an iOS app")
	}
	if (params.AndroidBuildID != "" || params.AndroidBuild != "") && params.AndroidAppID == "" {
		return nil, fmt.Errorf("an Android build override requires an Android app")
	}
	if params.BuildSelection != "" && (params.IOSBuildID != "" || params.AndroidBuildID != "" || params.IOSBuild != "" || params.AndroidBuild != "") {
		return nil, fmt.Errorf("a workflow build mode cannot be combined with exact build or version overrides")
	}
	if err := launcharguments.Validate(params.LaunchArguments); err != nil {
		return nil, err
	}

	workflowID := params.WorkflowNameOrID

	// Set defaults
	retries := params.Retries
	if retries == 0 {
		retries = 1
	}
	timeout := params.Timeout
	if timeout == 0 {
		timeout = DefaultRunTimeoutSeconds
	}

	// Create client and execute
	client := api.NewClientWithDevMode(apiKey, params.DevMode)
	inheritedLaunchConfigurationIDs, err := launchvars.LoadInheritedIDs(
		params.DisableInheritedLaunchVars,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load inherited launch variables: %w", err)
	}
	launchConfigurationIDs, err := startdevice.ResolveLaunchConfigurationSelection(
		ctx,
		client,
		inheritedLaunchConfigurationIDs,
		params.LaunchVars,
		params.LaunchArgSets,
	)
	if err != nil {
		return nil, err
	}
	req := &api.ExecuteWorkflowRequest{
		WorkflowID:        workflowID,
		Retries:           retries,
		VariableOverrides: params.VariableOverrides,
		LaunchEnvVarIds:   launchConfigurationIDs,
		LaunchEnvVars:     params.LaunchEnvVars,
		LaunchArguments:   params.LaunchArguments,
		BranchName:        params.BranchName,
		ContextBranchName: params.ContextBranchName,
		BuildSelection:    api.BuildSelectionMode(params.BuildSelection),
	}
	if params.IOSAppID != "" || params.AndroidAppID != "" {
		req.BuildConfig = &api.WorkflowAppConfig{}
		req.OverrideBuildConfig = true
		if params.IOSAppID != "" {
			iosUUID, err := uuid.Parse(params.IOSAppID)
			if err != nil {
				return nil, fmt.Errorf("invalid iOS app ID %q: %w", params.IOSAppID, err)
			}
			iosApp := &api.PlatformApp{AppId: iosUUID}
			if params.IOSBuild != "" {
				iosApp.PinnedVersion = &params.IOSBuild
			}
			if err := applyWorkflowExactBuild(ctx, client, iosApp, params.IOSBuildID, params.IOSBuild, params.BranchName, params.IOSBranchID); err != nil {
				return nil, err
			}
			req.BuildConfig.IosBuild = iosApp
		}
		if params.AndroidAppID != "" {
			androidUUID, err := uuid.Parse(params.AndroidAppID)
			if err != nil {
				return nil, fmt.Errorf("invalid Android app ID %q: %w", params.AndroidAppID, err)
			}
			androidApp := &api.PlatformApp{AppId: androidUUID}
			if params.AndroidBuild != "" {
				androidApp.PinnedVersion = &params.AndroidBuild
			}
			if err := applyWorkflowExactBuild(ctx, client, androidApp, params.AndroidBuildID, params.AndroidBuild, params.BranchName, params.AndroidBranchID); err != nil {
				return nil, err
			}
			req.BuildConfig.AndroidBuild = androidApp
		}
	}
	if params.HasLocation {
		req.LocationConfig = &api.CLILocation{
			Latitude:  params.Latitude,
			Longitude: params.Longitude,
		}
		req.OverrideLocation = true
	}
	resp, err := client.ExecuteWorkflow(ctx, req)
	if err != nil {
		return &RunWorkflowResult{
			Success:      false,
			WorkflowID:   workflowID,
			ErrorMessage: err.Error(),
		}, nil
	}

	// Notify caller of task ID immediately for cancellation support
	if params.OnTaskStarted != nil {
		params.OnTaskStarted(resp.TaskID)
	}
	if params.NoWait {
		return &RunWorkflowResult{
			Success:    true,
			TaskID:     resp.TaskID,
			WorkflowID: workflowID,
			Status:     "queued",
			ReportURL:  fmt.Sprintf("%s/workflows/report?taskId=%s", config.GetAppURL(params.DevMode), url.QueryEscape(resp.TaskID)),
		}, nil
	}

	// Monitor execution
	monitor := sse.NewMonitorWithDevMode(apiKey, timeout, params.DevMode)
	finalStatus, err := monitor.MonitorWorkflowWithMode(ctx, resp.TaskID, workflowID, params.MonitoringMode, params.OnProgress)
	if err != nil {
		// If we have a valid final status (e.g., cancelled via frontend while context was cancelled),
		// prefer using it over reporting a generic error
		if finalStatus != nil && status.IsTerminal(finalStatus.Status) {
			reportURL := fmt.Sprintf("%s/workflows/report?taskId=%s", config.GetAppURL(params.DevMode), url.QueryEscape(resp.TaskID))
			result := &RunWorkflowResult{
				Success:        status.IsWorkflowSuccess(finalStatus.Status, finalStatus.FailedTests),
				TaskID:         resp.TaskID,
				WorkflowID:     workflowID,
				WorkflowName:   finalStatus.WorkflowName,
				Status:         finalStatus.Status,
				TotalTests:     finalStatus.TotalTests,
				CompletedTests: resolveCompletedTests(finalStatus),
				PassedTests:    finalStatus.PassedTests,
				FailedTests:    finalStatus.FailedTests,
				Duration:       finalStatus.Duration,
				ReportURL:      reportURL,
				ErrorMessage:   finalStatus.ErrorMessage,
			}
			enrichWithTestResults(context.Background(), client, result)
			return result, nil
		}
		if finalStatus != nil {
			reportURL := fmt.Sprintf("%s/workflows/report?taskId=%s", config.GetAppURL(params.DevMode), url.QueryEscape(resp.TaskID))
			result := &RunWorkflowResult{
				Success:        false,
				TaskID:         resp.TaskID,
				WorkflowID:     workflowID,
				WorkflowName:   finalStatus.WorkflowName,
				Status:         "timeout",
				TotalTests:     finalStatus.TotalTests,
				CompletedTests: resolveCompletedTests(finalStatus),
				PassedTests:    finalStatus.PassedTests,
				FailedTests:    finalStatus.FailedTests,
				Duration:       finalStatus.Duration,
				ReportURL:      reportURL,
				ErrorMessage:   err.Error(),
			}
			enrichWithTestResults(context.Background(), client, result)
			return result, nil
		}
		return &RunWorkflowResult{
			Success:      false,
			TaskID:       resp.TaskID,
			Status:       "cancelled",
			ErrorMessage: err.Error(),
		}, nil
	}

	reportURL := fmt.Sprintf("%s/workflows/report?taskId=%s", config.GetAppURL(params.DevMode), url.QueryEscape(resp.TaskID))

	result := &RunWorkflowResult{
		Success:        status.IsWorkflowSuccess(finalStatus.Status, finalStatus.FailedTests),
		TaskID:         resp.TaskID,
		WorkflowID:     workflowID,
		WorkflowName:   finalStatus.WorkflowName,
		Status:         finalStatus.Status,
		TotalTests:     finalStatus.TotalTests,
		CompletedTests: resolveCompletedTests(finalStatus),
		PassedTests:    finalStatus.PassedTests,
		FailedTests:    finalStatus.FailedTests,
		Duration:       finalStatus.Duration,
		ReportURL:      reportURL,
		ErrorMessage:   finalStatus.ErrorMessage,
	}
	enrichWithTestResults(ctx, client, result)
	return result, nil
}

// resolveCompletedTests returns the completed test count from the workflow status,
// falling back to passed + failed when the field is zero (e.g. older backends).
func resolveCompletedTests(ws *sse.WorkflowStatus) int {
	if ws.CompletedTests > 0 {
		return ws.CompletedTests
	}
	return ws.PassedTests + ws.FailedTests
}

// enrichWithTestResults fetches the unified workflow report and populates
// per-test results on the RunWorkflowResult. Failures are silently ignored
// so the caller always gets at least the aggregate metrics.
func enrichWithTestResults(ctx context.Context, client *api.Client, result *RunWorkflowResult) {
	if result.TaskID == "" {
		return
	}
	report, err := client.GetWorkflowUnifiedReport(ctx, result.TaskID)
	if err != nil {
		return
	}
	if result.WorkflowName == "" && report.WorkflowDetail != nil {
		result.WorkflowName = report.WorkflowDetail.Name
	}
	for _, child := range report.ChildTasks {
		tr := WorkflowTestResult{
			BuildID: child.BuildID, BuildVersion: child.BuildVersion, BranchID: child.BranchID, BranchName: child.BranchName,
			TestName: child.TestName,
			Platform: child.Platform,
			Status:   child.Status,
		}
		if child.Success != nil {
			tr.Success = *child.Success
		}
		if child.ExecutionTimeSeconds != nil && *child.ExecutionTimeSeconds > 0 {
			tr.Duration = fmt.Sprintf("%.1fs", *child.ExecutionTimeSeconds)
		}
		tr.ErrorMessage = child.ErrorMessage
		result.Tests = append(result.Tests, tr)
	}
}
