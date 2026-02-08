// Package revyl provides a public API for the Revyl CLI.
//
// This package exposes the core functionality of the CLI as a Go library,
// making it easy to integrate with other tools like MCP servers.
//
// Example usage:
//
//	client, err := revyl.NewClient(revyl.WithAPIKey("your-api-key"))
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	result, err := client.RunTest(ctx, "login-flow")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("Test %s: %s\n", result.TestID, result.Status)
package revyl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/sse"
	"github.com/revyl/cli/internal/status"
)

// Client is the main entry point for the Revyl public API.
type Client struct {
	apiClient *api.Client
	config    *config.ProjectConfig
	workDir   string
	baseURL   string // Custom base URL if set
	apiKey    string // Store API key for recreating client with custom URL
}

// Option configures a Client.
type Option func(*Client) error

// WithAPIKey sets the API key for authentication.
func WithAPIKey(apiKey string) Option {
	return func(c *Client) error {
		c.apiKey = apiKey
		// Create client with default URL initially; will be recreated if WithBaseURL is also used
		c.apiClient = api.NewClient(apiKey)
		return nil
	}
}

// WithBaseURL sets a custom base URL for the API.
// This option must be applied after WithAPIKey.
func WithBaseURL(baseURL string) Option {
	return func(c *Client) error {
		c.baseURL = baseURL
		// Recreate the API client with the custom base URL if we have an API key
		if c.apiKey != "" {
			c.apiClient = api.NewClientWithBaseURL(c.apiKey, baseURL)
		}
		return nil
	}
}

// WithWorkDir sets the working directory for project operations.
func WithWorkDir(dir string) Option {
	return func(c *Client) error {
		c.workDir = dir
		return nil
	}
}

// WithConfig sets the project configuration directly.
func WithConfig(cfg *config.ProjectConfig) Option {
	return func(c *Client) error {
		c.config = cfg
		return nil
	}
}

// NewClient creates a new Revyl client.
//
// Parameters:
//   - opts: Configuration options
//
// Returns:
//   - *Client: A new client instance
//   - error: Any error that occurred during initialization
func NewClient(opts ...Option) (*Client, error) {
	c := &Client{}

	// Apply options
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	// If no API key provided, try to load from credentials
	if c.apiClient == nil {
		mgr := auth.NewManager()
		creds, err := mgr.GetCredentials()
		if err != nil || creds == nil || creds.APIKey == "" {
			return nil, fmt.Errorf("no API key provided and not authenticated")
		}
		c.apiKey = creds.APIKey
		// Use custom base URL if set, otherwise use default
		if c.baseURL != "" {
			c.apiClient = api.NewClientWithBaseURL(creds.APIKey, c.baseURL)
		} else {
			c.apiClient = api.NewClient(creds.APIKey)
		}
	}

	// If no work dir, use current directory
	if c.workDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get current directory: %w", err)
		}
		c.workDir = cwd
	}

	// Try to load project config
	if c.config == nil {
		configPath := filepath.Join(c.workDir, ".revyl", "config.yaml")
		cfg, err := config.LoadProjectConfig(configPath)
		if err == nil {
			c.config = cfg
		}
	}

	return c, nil
}

// TestResult contains the result of a test execution.
type TestResult struct {
	// TaskID is the execution task ID.
	TaskID string
	// TestID is the test ID.
	TestID string
	// TestName is the test name.
	TestName string
	// Status is the final status (completed, failed, etc.).
	Status string
	// Success indicates if the test passed.
	Success bool
	// Duration is the execution duration.
	Duration string
	// ReportURL is the URL to the test report.
	ReportURL string
	// ErrorMessage is the error message if failed.
	ErrorMessage string
}

// RunTest runs a test by name or ID.
//
// Parameters:
//   - ctx: Context for cancellation
//   - nameOrID: Test name (alias) or UUID
//
// Returns:
//   - *TestResult: The test result
//   - error: Any error that occurred
func (c *Client) RunTest(ctx context.Context, nameOrID string) (*TestResult, error) {
	return c.RunTestWithOptions(ctx, nameOrID, nil)
}

// RunTestOptions contains options for running a test.
type RunTestOptions struct {
	// Retries is the number of retry attempts.
	Retries int
	// BuildVersionID overrides the build version.
	BuildVersionID string
	// Timeout is the execution timeout in seconds.
	Timeout int
	// OnProgress is called with progress updates.
	OnProgress func(progress int, step string)
}

// RunTestWithOptions runs a test with custom options.
//
// Parameters:
//   - ctx: Context for cancellation
//   - nameOrID: Test name (alias) or UUID
//   - opts: Execution options
//
// Returns:
//   - *TestResult: The test result
//   - error: Any error that occurred
func (c *Client) RunTestWithOptions(ctx context.Context, nameOrID string, opts *RunTestOptions) (*TestResult, error) {
	if opts == nil {
		opts = &RunTestOptions{
			Retries: 1,
			Timeout: 3600,
		}
	}

	// Resolve test ID from alias
	testID := nameOrID
	if c.config != nil {
		if id, ok := c.config.Tests[nameOrID]; ok {
			testID = id
		}
	}

	// Start execution
	resp, err := c.apiClient.ExecuteTest(ctx, &api.ExecuteTestRequest{
		TestID:         testID,
		Retries:        opts.Retries,
		BuildVersionID: opts.BuildVersionID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start test: %w", err)
	}

	// Monitor execution
	monitor := sse.NewMonitor("", opts.Timeout) // API key already in client
	finalStatus, err := monitor.MonitorTest(ctx, resp.TaskID, testID, func(status *sse.TestStatus) {
		if opts.OnProgress != nil {
			opts.OnProgress(status.Progress, status.CurrentStep)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("monitoring failed: %w", err)
	}

	return &TestResult{
		TaskID:       resp.TaskID,
		TestID:       testID,
		TestName:     finalStatus.TestName,
		Status:       finalStatus.Status,
		Success:      status.IsSuccess(finalStatus.Status, finalStatus.Success, finalStatus.ErrorMessage),
		Duration:     finalStatus.Duration,
		ReportURL:    fmt.Sprintf("%s/tests/report?taskId=%s", config.ProdAppURL, resp.TaskID),
		ErrorMessage: finalStatus.ErrorMessage,
	}, nil
}

// WorkflowResult contains the result of a workflow execution.
type WorkflowResult struct {
	// TaskID is the execution task ID.
	TaskID string
	// WorkflowID is the workflow ID.
	WorkflowID string
	// Status is the final status.
	Status string
	// Success indicates if the workflow passed (all tests passed).
	Success bool
	// TotalTests is the total number of tests.
	TotalTests int
	// PassedTests is the number of passed tests.
	PassedTests int
	// FailedTests is the number of failed tests.
	FailedTests int
	// Duration is the execution duration.
	Duration string
	// ReportURL is the URL to the workflow report.
	ReportURL string
}

// RunWorkflow runs a workflow by name or ID.
//
// Parameters:
//   - ctx: Context for cancellation
//   - nameOrID: Workflow name (alias) or UUID
//
// Returns:
//   - *WorkflowResult: The workflow result
//   - error: Any error that occurred
func (c *Client) RunWorkflow(ctx context.Context, nameOrID string) (*WorkflowResult, error) {
	// Resolve workflow ID from alias
	workflowID := nameOrID
	if c.config != nil {
		if id, ok := c.config.Workflows[nameOrID]; ok {
			workflowID = id
		}
	}

	// Start execution
	resp, err := c.apiClient.ExecuteWorkflow(ctx, &api.ExecuteWorkflowRequest{
		WorkflowID: workflowID,
		Retries:    1,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start workflow: %w", err)
	}

	// Monitor execution
	monitor := sse.NewMonitor("", 3600)
	finalStatus, err := monitor.MonitorWorkflow(ctx, resp.TaskID, workflowID, nil)
	if err != nil {
		return nil, fmt.Errorf("monitoring failed: %w", err)
	}

	return &WorkflowResult{
		TaskID:      resp.TaskID,
		WorkflowID:  workflowID,
		Status:      finalStatus.Status,
		Success:     status.IsWorkflowSuccess(finalStatus.Status, finalStatus.FailedTests),
		TotalTests:  finalStatus.TotalTests,
		PassedTests: finalStatus.PassedTests,
		FailedTests: finalStatus.FailedTests,
		Duration:    finalStatus.Duration,
		ReportURL:   fmt.Sprintf("%s/workflows/report?taskId=%s", config.ProdAppURL, resp.TaskID),
	}, nil
}

// BuildResult contains the result of a build upload.
type BuildResult struct {
	// VersionID is the uploaded version ID.
	VersionID string
	// Version is the version string.
	Version string
	// PackageID is the package identifier.
	PackageID string
}

// BuildAndUpload builds the app and uploads it.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - *BuildResult: The build result
//   - error: Any error that occurred
func (c *Client) BuildAndUpload(ctx context.Context) (*BuildResult, error) {
	return c.BuildAndUploadWithOptions(ctx, nil)
}

// BuildOptions contains options for building.
type BuildOptions struct {
	// Variant is the build variant to use.
	Variant string
	// SkipBuild skips the build step.
	SkipBuild bool
	// Version is a custom version string.
	Version string
	// OnOutput is called with build output lines.
	OnOutput func(line string)
}

// BuildAndUploadWithOptions builds and uploads with custom options.
//
// Parameters:
//   - ctx: Context for cancellation
//   - opts: Build options
//
// Returns:
//   - *BuildResult: The build result
//   - error: Any error that occurred
func (c *Client) BuildAndUploadWithOptions(ctx context.Context, opts *BuildOptions) (*BuildResult, error) {
	if c.config == nil {
		return nil, fmt.Errorf("no project configuration found")
	}

	if opts == nil {
		opts = &BuildOptions{}
	}

	buildCfg := c.config.Build
	var variant config.BuildVariant
	if opts.Variant != "" {
		var ok bool
		variant, ok = c.config.Build.Variants[opts.Variant]
		if !ok {
			return nil, fmt.Errorf("unknown variant: %s", opts.Variant)
		}
		buildCfg.Command = variant.Command
		buildCfg.Output = variant.Output
	}

	// Run build
	if !opts.SkipBuild && buildCfg.Command != "" {
		runner := build.NewRunner(c.workDir)
		if err := runner.Run(buildCfg.Command, opts.OnOutput); err != nil {
			return nil, fmt.Errorf("build failed: %w", err)
		}
	}

	// Generate version
	version := opts.Version
	if version == "" {
		version = build.GenerateVersionString()
	}

	// Upload
	artifactPath := filepath.Join(c.workDir, buildCfg.Output)
	metadata := build.CollectMetadata(c.workDir, buildCfg.Command, opts.Variant, 0)

	result, err := c.apiClient.UploadBuild(ctx, &api.UploadBuildRequest{
		BuildVarID: variant.BuildVarID,
		Version:    version,
		FilePath:   artifactPath,
		Metadata:   metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("upload failed: %w", err)
	}

	return &BuildResult{
		VersionID: result.VersionID,
		Version:   result.Version,
		PackageID: result.PackageID,
	}, nil
}

// IsAuthenticated checks if the client is authenticated.
//
// Returns:
//   - bool: True if authenticated
func IsAuthenticated() bool {
	mgr := auth.NewManager()
	return mgr.IsAuthenticated()
}

// GetProjectConfig returns the loaded project configuration.
//
// Returns:
//   - *config.ProjectConfig: The project config, or nil if not loaded
func (c *Client) GetProjectConfig() *config.ProjectConfig {
	return c.config
}
