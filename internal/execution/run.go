// Package execution provides shared test and workflow creation and execution logic.
package execution

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/billingstatus"
	"github.com/revyl/cli/internal/config"
	startdevice "github.com/revyl/cli/internal/device"
	"github.com/revyl/cli/internal/launcharguments"
	"github.com/revyl/cli/internal/launchvars"
	"github.com/revyl/cli/internal/sse"
	"github.com/revyl/cli/internal/status"
)

const DefaultRunTimeoutSeconds = 60 * 60

const (
	MinRetries = 1
	MaxRetries = 1
)

const testRunBuildReadTimeout = 10 * time.Second

// RunTestParams configures RunTest; local alias resolution uses TestsDir rather than the ignored ProjectConfig argument.
type RunTestParams struct {
	TestNameOrID   string
	TestsDir       string
	Retries        int
	BuildVersionID string
	BranchID       string
	BranchName     string
	GitBranchName  string
	BuildSelection string
	Timeout        int
	DevMode        bool
	MonitoringMode sse.MonitoringMode
	OnProgress     func(status *sse.TestStatus)
	OnTaskStarted  func(taskID string)
	// OnQueued is called once the run is accepted when every device the
	// organization may run at once is busy, before the wait for one begins.
	OnQueued func(notice *billingstatus.QueueNotice)
	// OnDefaultBranchFallback is called with the app's default branch when it
	// replaced GitBranchName because that Git branch had no usable build.
	OnDefaultBranchFallback func(branchName string)
	OnTestDefinitionGitRead func(read api.TestDefinitionGitRead)
	OnBranchSelection       func(gitBranchName, branchName string, source api.BranchSource, enabled *bool)
	NoWait                  bool
	// LaunchURL is the deep link URL for hot reload mode.
	// When provided, the test will launch the app via this URL instead of the normal app launch.
	LaunchURL                  string
	LaunchVars                 []string
	LaunchArgSets              []string
	DisableInheritedLaunchVars bool
	Latitude                   float64
	Longitude                  float64
	HasLocation                bool
	DeviceModel                string
	OsVersion                  string
	Orientation                string
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
	// SessionProfile is the applied session profile a Web test's browser
	// starts with, such as a --cookies-file's storage state.
	SessionProfile *api.SessionProfileRequest
	// VariableOverrides are test variable values applied to this run. They are
	// referenced as {{name}} and override test/global variables for that run only.
	VariableOverrides map[string]string
	// TestDefinitionFiles are the checkout's files the run executes instead of
	// saved copies; nil runs what the server resolves on its own.
	TestDefinitionFiles *api.TestDefinitionFiles
}

// ErrServerCannotRunLocalDefinitions means the server started a run without
// reporting its test definition source, so it ignored the checkout's files.
var ErrServerCannotRunLocalDefinitions = errors.New("this Revyl server can't run local definitions yet")

const localDefinitionsRefusalCancelTimeout = 10 * time.Second

// cancelRunThatIgnoredLocalDefinitions cancels a run an older server started
// with its saved tests instead of the checkout's files, and returns the error
// the caller exits with.
func cancelRunThatIgnoredLocalDefinitions(ctx context.Context, taskID string, cancelRun func(context.Context) error) error {
	cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), localDefinitionsRefusalCancelTimeout)
	defer cancel()
	if err := cancelRun(cancelCtx); err != nil {
		return fmt.Errorf("%w: it started run %s with the saved tests and cancelling it failed (%v); rerun with --from-checkout=false to run the saved tests", ErrServerCannotRunLocalDefinitions, taskID, err)
	}
	return fmt.Errorf("%w: it started run %s with the saved tests, so the CLI cancelled it; rerun with --from-checkout=false to run the saved tests", ErrServerCannotRunLocalDefinitions, taskID)
}

// RunTestResult holds the outcome of RunTest across dispatch refusal,
// no-wait queueing (Success=false, Status="queued"), and monitored runs.
type RunTestResult struct {
	AutoGitBranchEnabled *bool                `json:"auto_git_branch_enabled,omitempty"`
	BranchSource         api.BranchSource     `json:"branch_source,omitempty"`
	GitBranchName        string               `json:"git_branch_name,omitempty"`
	BranchName           string               `json:"branch_name,omitempty"`
	Success              bool                 `json:"success"`
	TaskID               string               `json:"task_id"`
	TestID               string               `json:"test_id"`
	TestName             string               `json:"test_name"`
	Status               string               `json:"status"`
	Duration             string               `json:"duration"`
	ReportURL            string               `json:"report_url"`
	ErrorMessage         string               `json:"error_message,omitempty"`
	BillingURL           string               `json:"billing_url,omitempty"`
	BillingAction        api.BillingAction    `json:"billing_action,omitempty"`
	UpgradeBenefits      *api.UpgradeBenefits `json:"upgrade_benefits,omitempty"`
	UsageNotice          *api.UsageNotice     `json:"billing_notice,omitempty"`
	// QueueNotice carries the server's queue notice from dispatch, when present.
	QueueNotice  *billingstatus.QueueNotice `json:"queue_notice,omitempty"`
	BuildID      string                     `json:"build_id,omitempty"`
	BuildVersion string                     `json:"build_version,omitempty"`

	TestDefinitionSource         api.TestDefinitionSource         `json:"test_definition_source,omitempty"`
	TestDefinitionFallbackReason api.TestDefinitionFallbackReason `json:"test_definition_fallback_reason,omitempty"`
	api.TestDefinitionGitRead
}

// RunTest validates options, resolves TestNameOrID via params.TestsDir when set,
// dispatches the test, and optionally monitors it to completion. The config argument is unused.
func RunTest(ctx context.Context, apiKey string, _ *config.ProjectConfig, params RunTestParams) (*RunTestResult, error) {
	if err := ValidateRuntimeBuildSelection(params.BranchID, params.BranchName, params.BuildSelection, params.BuildVersionID); err != nil {
		return nil, err
	}
	if err := launcharguments.Validate(params.LaunchArguments); err != nil {
		return nil, err
	}

	testID := params.TestNameOrID
	if strings.TrimSpace(params.TestsDir) != "" {
		if id, ltErr := config.GetLocalTestRemoteID(params.TestsDir, params.TestNameOrID); ltErr == nil && id != "" {
			testID = id
		}
	}

	retries := 1
	timeout := params.Timeout
	if timeout == 0 {
		timeout = DefaultRunTimeoutSeconds
	}

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
		TestID:              testID,
		Retries:             retries,
		BuildVersionID:      params.BuildVersionID,
		BranchID:            params.BranchID,
		BranchName:          params.BranchName,
		GitBranchName:       params.GitBranchName,
		BuildSelection:      api.BuildSelectionMode(params.BuildSelection),
		LaunchURL:           params.LaunchURL,
		LaunchEnvVarIds:     launchConfigurationIDs,
		DeviceModel:         params.DeviceModel,
		OsVersion:           params.OsVersion,
		LaunchEnvVars:       params.LaunchEnvVars,
		LaunchArguments:     params.LaunchArguments,
		SessionProfile:      params.SessionProfile,
		VariableOverrides:   params.VariableOverrides,
		TestDefinitionFiles: params.TestDefinitionFiles,
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
		refusal, _ := api.AsBillingRefusal(err)
		if refusal == nil {
			refusal = &api.BillingRefusal{}
		}
		return &RunTestResult{
			BillingURL: refusal.BillingURL, BillingAction: refusal.BillingAction, UpgradeBenefits: refusal.UpgradeBenefits,
			Success:      false,
			TestID:       testID,
			ErrorMessage: err.Error(),
		}, nil
	}

	if params.TestDefinitionFiles != nil && resp.TestDefinitionSource == "" {
		return nil, cancelRunThatIgnoredLocalDefinitions(ctx, resp.TaskID, func(cancelCtx context.Context) error {
			_, err := client.CancelTest(cancelCtx, resp.TaskID)
			return err
		})
	}

	if params.OnTestDefinitionGitRead != nil {
		params.OnTestDefinitionGitRead(resp.TestDefinitionGitRead)
	}
	if params.OnBranchSelection != nil {
		params.OnBranchSelection(resp.GitBranchName, resp.BranchName, resp.BranchSource, resp.AutoGitBranchEnabled)
	}
	if resp.BranchSource.FellBackFromGitBranch(resp.GitBranchName) && params.OnDefaultBranchFallback != nil {
		params.OnDefaultBranchFallback(resp.BranchName)
	}

	if params.OnTaskStarted != nil {
		params.OnTaskStarted(resp.TaskID)
	}
	queued := billingstatus.QueuedStart(resp.QueueNotice)
	if queued != nil && params.OnQueued != nil {
		params.OnQueued(queued)
	}

	if params.NoWait {
		reportURL := fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(params.DevMode), url.QueryEscape(resp.TaskID))
		return &RunTestResult{
			UsageNotice:          resp.UsageNotice,
			QueueNotice:          queued,
			TaskID:               resp.TaskID,
			BranchSource:         resp.BranchSource,
			AutoGitBranchEnabled: resp.AutoGitBranchEnabled,
			GitBranchName:        resp.GitBranchName,
			BranchName:           resp.BranchName,
			TestID:               testID,
			Status:               "queued",
			ReportURL:            reportURL,

			TestDefinitionSource:         resp.TestDefinitionSource,
			TestDefinitionFallbackReason: resp.TestDefinitionFallbackReason,
			TestDefinitionGitRead:        resp.TestDefinitionGitRead,
		}, nil
	}

	monitor := sse.NewMonitorWithDevMode(apiKey, timeout, params.DevMode)
	finalStatus, err := monitor.MonitorTestWithMode(ctx, resp.TaskID, testID, params.MonitoringMode, params.OnProgress)
	if err != nil {
		if finalStatus != nil && status.IsTerminal(finalStatus.Status) {
			reportURL := fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(params.DevMode), url.QueryEscape(resp.TaskID))
			result := &RunTestResult{
				UsageNotice:          resp.UsageNotice,
				QueueNotice:          queued,
				Success:              status.IsSuccess(finalStatus.Status, finalStatus.Success, finalStatus.ErrorMessage),
				TaskID:               resp.TaskID,
				BranchSource:         resp.BranchSource,
				AutoGitBranchEnabled: resp.AutoGitBranchEnabled,
				GitBranchName:        resp.GitBranchName,
				BranchName:           resp.BranchName,
				TestID:               testID,
				TestName:             finalStatus.TestName,
				Status:               finalStatus.Status,
				Duration:             finalStatus.Duration,
				ReportURL:            reportURL,
				ErrorMessage:         finalStatus.ErrorMessage,

				TestDefinitionSource:         resp.TestDefinitionSource,
				TestDefinitionFallbackReason: resp.TestDefinitionFallbackReason,
				TestDefinitionGitRead:        resp.TestDefinitionGitRead,
			}
			attachTestRunBuild(ctx, client, result)
			return result, nil
		}
		return &RunTestResult{
			UsageNotice:          resp.UsageNotice,
			QueueNotice:          queued,
			Success:              false,
			TaskID:               resp.TaskID,
			BranchSource:         resp.BranchSource,
			AutoGitBranchEnabled: resp.AutoGitBranchEnabled,
			GitBranchName:        resp.GitBranchName,
			BranchName:           resp.BranchName,
			Status:               "cancelled",
			ErrorMessage:         err.Error(),

			TestDefinitionSource:         resp.TestDefinitionSource,
			TestDefinitionFallbackReason: resp.TestDefinitionFallbackReason,
			TestDefinitionGitRead:        resp.TestDefinitionGitRead,
		}, nil
	}

	reportURL := fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(params.DevMode), url.QueryEscape(resp.TaskID))

	result := &RunTestResult{
		UsageNotice:          resp.UsageNotice,
		QueueNotice:          queued,
		Success:              status.IsSuccess(finalStatus.Status, finalStatus.Success, finalStatus.ErrorMessage),
		TaskID:               resp.TaskID,
		BranchSource:         resp.BranchSource,
		AutoGitBranchEnabled: resp.AutoGitBranchEnabled,
		GitBranchName:        resp.GitBranchName,
		BranchName:           resp.BranchName,
		TestID:               testID,
		TestName:             finalStatus.TestName,
		Status:               finalStatus.Status,
		Duration:             finalStatus.Duration,
		ReportURL:            reportURL,
		ErrorMessage:         finalStatus.ErrorMessage,

		TestDefinitionSource:         resp.TestDefinitionSource,
		TestDefinitionFallbackReason: resp.TestDefinitionFallbackReason,
		TestDefinitionGitRead:        resp.TestDefinitionGitRead,
	}
	attachTestRunBuild(ctx, client, result)
	return result, nil
}

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

// RunWorkflowParams configures RunWorkflow; callers resolve workflow aliases before dispatch.
type RunWorkflowParams struct {
	ConcurrencyLimitOverride *int
	WorkflowNameOrID         string
	Retries                  int
	Timeout                  int
	DevMode                  bool
	IOSAppID                 string
	AndroidAppID             string
	IOSBuild                 string
	AndroidBuild             string
	IOSBuildID               string
	AndroidBuildID           string
	IOSBranchID              string
	AndroidBranchID          string
	BranchName               string
	GitBranchName            string
	BuildSelection           string
	NoWait                   bool
	Latitude                 float64
	Longitude                float64
	HasLocation              bool
	// VariableOverrides are test variable values applied to every child test in
	// this workflow run. They are referenced as {{name}} and override test/global
	// variables for that run only.
	VariableOverrides          map[string]string
	LaunchVars                 []string
	LaunchArgSets              []string
	DisableInheritedLaunchVars bool
	// LaunchEnvVars are inline app launch values applied to every child test.
	// They override stored launch variables with the same key.
	LaunchEnvVars   map[string]string
	LaunchArguments []string
	// TestDefinitionFiles are the checkout's files the workflow's tests execute
	// instead of saved copies; nil runs what the server resolves on its own.
	TestDefinitionFiles *api.TestDefinitionFiles
	MonitoringMode      sse.MonitoringMode
	OnProgress          func(status *sse.WorkflowStatus)
	OnTaskStarted       func(taskID string)
	// OnTestDefinitionGitRead is called before monitoring with where the server
	// read the run's test definition files in Git, when every test read from
	// Git read the same branch.
	OnTestDefinitionGitRead func(read api.TestDefinitionGitRead)
}

// WorkflowTestResult holds per-child status from workflow monitoring or unified-report enrichment.
type WorkflowTestResult struct {
	AutoGitBranchEnabled *bool            `json:"auto_git_branch_enabled,omitempty"`
	TaskID               string           `json:"task_id,omitempty"`
	AppID                string           `json:"app_id,omitempty"`
	AppName              string           `json:"app_name,omitempty"`
	BranchSource         api.BranchSource `json:"branch_source,omitempty"`
	GitBranchName        string           `json:"git_branch_name,omitempty"`
	BuildID              string           `json:"build_id,omitempty"`
	BuildVersion         string           `json:"build_version,omitempty"`
	BranchID             string           `json:"branch_id,omitempty"`
	BranchName           string           `json:"branch_name,omitempty"`
	TestName             string           `json:"test_name"`
	Platform             string           `json:"platform"`
	Status               string           `json:"status"`
	Success              bool             `json:"success"`
	Duration             string           `json:"duration"`
	ErrorMessage         string           `json:"error_message,omitempty"`
}

// RunWorkflowResult holds the outcome of RunWorkflow across dispatch refusal,
// no-wait queueing (Success=true, Status="queued"), partial timeouts, and terminal runs.
type RunWorkflowResult struct {
	Success         bool                 `json:"success"`
	TaskID          string               `json:"task_id"`
	WorkflowID      string               `json:"workflow_id"`
	WorkflowName    string               `json:"workflow_name"`
	Status          string               `json:"status"`
	TotalTests      int                  `json:"total_tests"`
	CompletedTests  int                  `json:"completed_tests"`
	PassedTests     int                  `json:"passed_tests"`
	FailedTests     int                  `json:"failed_tests"`
	Duration        string               `json:"duration"`
	ReportURL       string               `json:"report_url"`
	ErrorMessage    string               `json:"error_message,omitempty"`
	BillingURL      string               `json:"billing_url,omitempty"`
	BillingAction   api.BillingAction    `json:"billing_action,omitempty"`
	UpgradeBenefits *api.UpgradeBenefits `json:"upgrade_benefits,omitempty"`
	UsageNotice     *api.UsageNotice     `json:"billing_notice,omitempty"`
	Tests           []WorkflowTestResult `json:"tests,omitempty"`

	TestCountByDefinitionSource         map[api.TestDefinitionSource]int         `json:"test_count_by_definition_source,omitempty"`
	TestCountByDefinitionFallbackReason map[api.TestDefinitionFallbackReason]int `json:"test_count_by_definition_fallback_reason,omitempty"`
	api.TestDefinitionGitRead
}

// RunWorkflow validates overrides, dispatches WorkflowNameOrID, and optionally
// monitors it to completion. The config argument is unused; callers resolve aliases beforehand.
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
		return nil, fmt.Errorf("a build choice (Automatic, Latest build, or All branches · Latest build) can't be combined with an exact iOS or Android build")
	}
	if err := launcharguments.Validate(params.LaunchArguments); err != nil {
		return nil, err
	}

	workflowID := params.WorkflowNameOrID

	retries := 1
	timeout := params.Timeout
	if timeout == 0 {
		timeout = DefaultRunTimeoutSeconds
	}

	client := api.NewClientWithDevMode(apiKey, params.DevMode)

	var iosApp, androidApp *api.PlatformApp
	var iosAppErr, androidAppErr error
	if params.IOSAppID != "" {
		iosApp, iosAppErr = workflowPlatformApp("iOS", params.IOSAppID, params.IOSBuild)
	}
	if params.AndroidAppID != "" {
		androidApp, androidAppErr = workflowPlatformApp("Android", params.AndroidAppID, params.AndroidBuild)
	}
	var iosBuildErr, androidBuildErr error
	var exactBuilds sync.WaitGroup
	if iosApp != nil {
		exactBuilds.Add(1)
		go func() {
			defer exactBuilds.Done()
			iosBuildErr = applyWorkflowExactBuild(ctx, client, iosApp, params.IOSBuildID, params.IOSBuild, params.BranchName, params.IOSBranchID)
		}()
	}
	if androidApp != nil {
		exactBuilds.Add(1)
		go func() {
			defer exactBuilds.Done()
			androidBuildErr = applyWorkflowExactBuild(ctx, client, androidApp, params.AndroidBuildID, params.AndroidBuild, params.BranchName, params.AndroidBranchID)
		}()
	}

	var launchConfigurationIDs []string
	inheritedLaunchConfigurationIDs, inheritedErr := launchvars.LoadInheritedIDs(
		params.DisableInheritedLaunchVars,
	)
	var launchErr error
	if inheritedErr == nil {
		launchConfigurationIDs, launchErr = startdevice.ResolveLaunchConfigurationSelection(
			ctx,
			client,
			inheritedLaunchConfigurationIDs,
			params.LaunchVars,
			params.LaunchArgSets,
		)
	}
	exactBuilds.Wait()
	if inheritedErr != nil {
		return nil, fmt.Errorf("failed to load inherited launch variables: %w", inheritedErr)
	}
	if launchErr != nil {
		return nil, launchErr
	}
	for _, err := range []error{iosAppErr, iosBuildErr, androidAppErr, androidBuildErr} {
		if err != nil {
			return nil, err
		}
	}

	req := &api.ExecuteWorkflowRequest{
		ConcurrencyLimitOverride: params.ConcurrencyLimitOverride,
		WorkflowID:               workflowID,
		Retries:                  retries,
		VariableOverrides:        params.VariableOverrides,
		LaunchEnvVarIds:          launchConfigurationIDs,
		LaunchEnvVars:            params.LaunchEnvVars,
		LaunchArguments:          params.LaunchArguments,
		BranchName:               params.BranchName,
		GitBranchName:            params.GitBranchName,
		BuildSelection:           api.BuildSelectionMode(params.BuildSelection),
		TestDefinitionFiles:      params.TestDefinitionFiles,
	}
	if iosApp != nil || androidApp != nil {
		req.BuildConfig = &api.WorkflowAppConfig{IosBuild: iosApp, AndroidBuild: androidApp}
		req.OverrideBuildConfig = true
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
		refusal, _ := api.AsBillingRefusal(err)
		if refusal == nil {
			refusal = &api.BillingRefusal{}
		}
		return &RunWorkflowResult{
			BillingURL: refusal.BillingURL, BillingAction: refusal.BillingAction, UpgradeBenefits: refusal.UpgradeBenefits,
			Success:      false,
			WorkflowID:   workflowID,
			ErrorMessage: err.Error(),
		}, nil
	}
	if params.TestDefinitionFiles != nil && resp.TestCountByDefinitionSource == nil {
		return nil, cancelRunThatIgnoredLocalDefinitions(ctx, resp.TaskID, func(cancelCtx context.Context) error {
			_, err := client.CancelWorkflow(cancelCtx, resp.TaskID)
			return err
		})
	}

	if params.OnTaskStarted != nil {
		params.OnTaskStarted(resp.TaskID)
	}
	if params.OnTestDefinitionGitRead != nil {
		params.OnTestDefinitionGitRead(resp.TestDefinitionGitRead)
	}
	if params.NoWait {
		return &RunWorkflowResult{
			UsageNotice: resp.UsageNotice,
			Success:     true,
			TaskID:      resp.TaskID,
			WorkflowID:  workflowID,
			Status:      "queued",
			ReportURL:   fmt.Sprintf("%s/workflows/report?taskId=%s", config.GetAppURL(params.DevMode), url.QueryEscape(resp.TaskID)),

			TestCountByDefinitionSource:         resp.TestCountByDefinitionSource,
			TestCountByDefinitionFallbackReason: resp.TestCountByDefinitionFallbackReason,
			TestDefinitionGitRead:               resp.TestDefinitionGitRead,
		}, nil
	}

	monitor := sse.NewMonitorWithDevMode(apiKey, timeout, params.DevMode)
	finalStatus, err := monitor.MonitorWorkflowWithMode(ctx, resp.TaskID, workflowID, params.MonitoringMode, params.OnProgress)
	if err != nil {
		if finalStatus != nil && status.IsTerminal(finalStatus.Status) {
			reportURL := fmt.Sprintf("%s/workflows/report?taskId=%s", config.GetAppURL(params.DevMode), url.QueryEscape(resp.TaskID))
			result := &RunWorkflowResult{
				UsageNotice:    resp.UsageNotice,
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

				TestCountByDefinitionSource:         resp.TestCountByDefinitionSource,
				TestCountByDefinitionFallbackReason: resp.TestCountByDefinitionFallbackReason,
				TestDefinitionGitRead:               resp.TestDefinitionGitRead,
			}
			result.Tests = workflowStatusTests(finalStatus)
			enrichWithTestResults(context.Background(), client, result)
			return result, nil
		}
		if finalStatus != nil {
			reportURL := fmt.Sprintf("%s/workflows/report?taskId=%s", config.GetAppURL(params.DevMode), url.QueryEscape(resp.TaskID))
			result := &RunWorkflowResult{
				UsageNotice:    resp.UsageNotice,
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

				TestCountByDefinitionSource:         resp.TestCountByDefinitionSource,
				TestCountByDefinitionFallbackReason: resp.TestCountByDefinitionFallbackReason,
				TestDefinitionGitRead:               resp.TestDefinitionGitRead,
			}
			result.Tests = workflowStatusTests(finalStatus)
			enrichWithTestResults(context.Background(), client, result)
			return result, nil
		}
		return &RunWorkflowResult{
			UsageNotice:  resp.UsageNotice,
			Success:      false,
			TaskID:       resp.TaskID,
			Status:       "cancelled",
			ErrorMessage: err.Error(),

			TestCountByDefinitionSource:         resp.TestCountByDefinitionSource,
			TestCountByDefinitionFallbackReason: resp.TestCountByDefinitionFallbackReason,
			TestDefinitionGitRead:               resp.TestDefinitionGitRead,
		}, nil
	}

	reportURL := fmt.Sprintf("%s/workflows/report?taskId=%s", config.GetAppURL(params.DevMode), url.QueryEscape(resp.TaskID))

	result := &RunWorkflowResult{
		UsageNotice:    resp.UsageNotice,
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

		TestCountByDefinitionSource:         resp.TestCountByDefinitionSource,
		TestCountByDefinitionFallbackReason: resp.TestCountByDefinitionFallbackReason,
		TestDefinitionGitRead:               resp.TestDefinitionGitRead,
	}
	result.Tests = workflowStatusTests(finalStatus)
	enrichWithTestResults(ctx, client, result)
	return result, nil
}

func workflowStatusTests(ws *sse.WorkflowStatus) []WorkflowTestResult {
	tests := make([]WorkflowTestResult, 0, len(ws.ChildTasks))
	for _, child := range ws.ChildTasks {
		test := WorkflowTestResult{
			TaskID: child.ID, AppID: child.AppID, AppName: child.AppName,
			BranchSource: child.BranchSource, GitBranchName: child.GitBranchName,
			AutoGitBranchEnabled: child.AutoGitBranchEnabled,
			BuildID:              child.BuildID, BuildVersion: child.BuildVersion, BranchID: child.BranchID, BranchName: child.BranchName,
			Status: child.Status, ErrorMessage: child.ErrorMessage,
		}
		if child.Success != nil {
			test.Success = *child.Success
		}
		if child.ExecutionTimeSeconds != nil {
			test.Duration = fmt.Sprintf("%.1fs", *child.ExecutionTimeSeconds)
		}
		tests = append(tests, test)
	}
	return tests
}

func resolveCompletedTests(ws *sse.WorkflowStatus) int {
	if ws.CompletedTests > 0 {
		return ws.CompletedTests
	}
	return ws.PassedTests + ws.FailedTests
}

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
	if len(report.ChildTasks) == 0 {
		return
	}
	observed := make(map[string]WorkflowTestResult, len(result.Tests))
	for _, test := range result.Tests {
		observed[test.TaskID] = test
	}
	result.Tests = nil
	for _, child := range report.ChildTasks {
		tr := WorkflowTestResult{
			TaskID: child.TaskID, AppID: child.AppID, AppName: child.AppName,
			BranchSource: child.BranchSource, GitBranchName: child.GitBranchName,
			AutoGitBranchEnabled: child.AutoGitBranchEnabled,
			BuildID:              child.BuildID, BuildVersion: child.BuildVersion, BranchID: child.BranchID, BranchName: child.BranchName,
			TestName: child.TestName,
			Platform: child.Platform,
			Status:   child.Status,
		}
		if previous, ok := observed[child.TaskID]; ok && previous.BranchSource != "" {
			if tr.AutoGitBranchEnabled == nil {
				tr.AutoGitBranchEnabled = previous.AutoGitBranchEnabled
			}
			if tr.BranchSource == "" {
				tr.BranchSource, tr.GitBranchName = previous.BranchSource, previous.GitBranchName
			}
			if tr.AppID == "" {
				tr.AppID = previous.AppID
			}
			if tr.AppName == "" {
				tr.AppName = previous.AppName
			}
			if tr.BranchID == "" {
				tr.BranchID = previous.BranchID
			}
			if tr.BranchName == "" {
				tr.BranchName = previous.BranchName
			}
			if tr.BuildID == "" {
				tr.BuildID = previous.BuildID
			}
			if tr.BuildVersion == "" {
				tr.BuildVersion = previous.BuildVersion
			}
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
