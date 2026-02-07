// Package mcp provides the MCP (Model Context Protocol) server implementation.
//
// This package implements an MCP server that exposes Revyl CLI functionality
// as tools that can be called by AI agents via the MCP protocol.
package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/execution"
	"github.com/revyl/cli/internal/schema"
	"github.com/revyl/cli/internal/yaml"
)

// Server wraps the MCP server with Revyl-specific functionality.
type Server struct {
	mcpServer *mcp.Server
	apiClient *api.Client
	config    *config.ProjectConfig
	workDir   string
	version   string
	rootCmd   *cobra.Command
}

// NewServer creates a new Revyl MCP server.
//
// Parameters:
//   - version: The CLI version string
//
// Returns:
//   - *Server: A new server instance
//   - error: Any error that occurred during initialization
func NewServer(version string) (*Server, error) {
	// Get API key from environment or credentials
	apiKey := os.Getenv("REVYL_API_KEY")
	if apiKey == "" {
		mgr := auth.NewManager()
		creds, err := mgr.GetCredentials()
		if err != nil || creds == nil || creds.APIKey == "" {
			return nil, fmt.Errorf("not authenticated: set REVYL_API_KEY or run 'revyl auth login'")
		}
		apiKey = creds.APIKey
	}

	// Get working directory
	workDir, err := os.Getwd()
	if err != nil {
		workDir = "."
	}

	// Try to load project config
	var cfg *config.ProjectConfig
	configPath := filepath.Join(workDir, ".revyl", "config.yaml")
	cfg, _ = config.LoadProjectConfig(configPath)

	s := &Server{
		apiClient: api.NewClient(apiKey),
		config:    cfg,
		workDir:   workDir,
		version:   version,
	}

	// Create MCP server
	s.mcpServer = mcp.NewServer(
		&mcp.Implementation{
			Name:    "revyl",
			Version: version,
		},
		nil,
	)

	// Register tools
	s.registerTools()

	return s, nil
}

// SetRootCmd sets the root Cobra command for schema generation.
//
// Parameters:
//   - cmd: The root Cobra command
func (s *Server) SetRootCmd(cmd *cobra.Command) {
	s.rootCmd = cmd
}

// Run starts the MCP server over stdio.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - error: Any error that occurred during execution
func (s *Server) Run(ctx context.Context) error {
	return s.mcpServer.Run(ctx, &mcp.StdioTransport{})
}

// registerTools registers all Revyl tools with the MCP server.
func (s *Server) registerTools() {
	// run_test tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "run_test",
		Description: "Run a Revyl test by name or ID. Returns test results including pass/fail status and report URL.",
	}, s.handleRunTest)

	// run_workflow tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "run_workflow",
		Description: "Run a Revyl workflow (collection of tests) by name or ID. Returns workflow results including pass/fail counts.",
	}, s.handleRunWorkflow)

	// list_tests tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_tests",
		Description: "List available tests from the project's .revyl/config.yaml file.",
	}, s.handleListTests)

	// get_test_status tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_test_status",
		Description: "Get the current status of a running or completed test execution.",
	}, s.handleGetTestStatus)

	// NEW: create_test tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "create_test",
		Description: "Create a new test. Can create from YAML content or just a name (opens browser editor).",
	}, s.handleCreateTest)

	// NEW: create_workflow tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "create_workflow",
		Description: "Create a new workflow (collection of tests).",
	}, s.handleCreateWorkflow)

	// NEW: validate_yaml tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "validate_yaml",
		Description: "Validate YAML test syntax without creating or running. Returns validation errors/warnings.",
	}, s.handleValidateYAML)

	// NEW: get_schema tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_schema",
		Description: "Get the complete CLI command schema and YAML test schema for LLM reference.",
	}, s.handleGetSchema)

	// NEW: list_builds tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_builds",
		Description: "List available build versions for the project.",
	}, s.handleListBuilds)

	// NEW: open_test_editor tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "open_test_editor",
		Description: "Get the URL to open a test in the browser editor.",
	}, s.handleOpenTestEditor)

	// NEW: open_workflow_editor tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "open_workflow_editor",
		Description: "Get the URL to open a workflow in the browser editor.",
	}, s.handleOpenWorkflowEditor)
}

// RunTestInput defines the input parameters for the run_test tool.
type RunTestInput struct {
	TestName       string `json:"test_name" jsonschema:"description=Test name (alias from .revyl/config.yaml) or UUID"`
	Retries        int    `json:"retries,omitempty" jsonschema:"description=Number of retry attempts (1-5)"`
	BuildVersionID string `json:"build_version_id,omitempty" jsonschema:"description=Specific build version ID to test against"`
}

// RunTestOutput defines the output for the run_test tool.
type RunTestOutput struct {
	Success      bool   `json:"success"`
	TaskID       string `json:"task_id"`
	TestID       string `json:"test_id"`
	TestName     string `json:"test_name"`
	Status       string `json:"status"`
	Duration     string `json:"duration"`
	ReportURL    string `json:"report_url"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// handleRunTest handles the run_test tool call.
func (s *Server) handleRunTest(ctx context.Context, req *mcp.CallToolRequest, input RunTestInput) (*mcp.CallToolResult, RunTestOutput, error) {
	// Validate input
	if input.TestName == "" {
		return nil, RunTestOutput{
			Success:      false,
			ErrorMessage: "test_name is required",
		}, nil
	}

	// Validate retries bounds (1-5)
	retries := input.Retries
	if retries < 0 {
		retries = 1
	} else if retries > 5 {
		return nil, RunTestOutput{
			Success:      false,
			ErrorMessage: "retries must be between 1 and 5",
		}, nil
	} else if retries == 0 {
		retries = 1 // Default to 1 if not specified
	}

	// Use shared execution logic
	result, err := execution.RunTest(ctx, s.apiClient.GetAPIKey(), s.config, execution.RunTestParams{
		TestNameOrID:   input.TestName,
		Retries:        retries,
		BuildVersionID: input.BuildVersionID,
		Timeout:        3600,
		DevMode:        false,
		OnProgress:     nil, // MCP doesn't need progress callbacks
	})
	if err != nil {
		return nil, RunTestOutput{Success: false, ErrorMessage: err.Error()}, nil
	}

	return nil, RunTestOutput{
		Success:      result.Success,
		TaskID:       result.TaskID,
		TestID:       result.TestID,
		TestName:     result.TestName,
		Status:       result.Status,
		Duration:     result.Duration,
		ReportURL:    result.ReportURL,
		ErrorMessage: result.ErrorMessage,
	}, nil
}

// RunWorkflowInput defines the input parameters for the run_workflow tool.
type RunWorkflowInput struct {
	WorkflowName string `json:"workflow_name" jsonschema:"description=Workflow name (alias from .revyl/config.yaml) or UUID"`
	Retries      int    `json:"retries,omitempty" jsonschema:"description=Number of retry attempts (1-5)"`
}

// RunWorkflowOutput defines the output for the run_workflow tool.
type RunWorkflowOutput struct {
	Success      bool   `json:"success"`
	TaskID       string `json:"task_id"`
	WorkflowID   string `json:"workflow_id"`
	Status       string `json:"status"`
	TotalTests   int    `json:"total_tests"`
	PassedTests  int    `json:"passed_tests"`
	FailedTests  int    `json:"failed_tests"`
	Duration     string `json:"duration"`
	ReportURL    string `json:"report_url"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// handleRunWorkflow handles the run_workflow tool call.
func (s *Server) handleRunWorkflow(ctx context.Context, req *mcp.CallToolRequest, input RunWorkflowInput) (*mcp.CallToolResult, RunWorkflowOutput, error) {
	// Validate input
	if input.WorkflowName == "" {
		return nil, RunWorkflowOutput{
			Success:      false,
			ErrorMessage: "workflow_name is required",
		}, nil
	}

	// Validate retries bounds (1-5)
	retries := input.Retries
	if retries < 0 {
		retries = 1
	} else if retries > 5 {
		return nil, RunWorkflowOutput{
			Success:      false,
			ErrorMessage: "retries must be between 1 and 5",
		}, nil
	} else if retries == 0 {
		retries = 1 // Default to 1 if not specified
	}

	// Use shared execution logic
	result, err := execution.RunWorkflow(ctx, s.apiClient.GetAPIKey(), s.config, execution.RunWorkflowParams{
		WorkflowNameOrID: input.WorkflowName,
		Retries:          retries,
		Timeout:          3600,
		DevMode:          false,
		OnProgress:       nil,
	})
	if err != nil {
		return nil, RunWorkflowOutput{Success: false, ErrorMessage: err.Error()}, nil
	}

	return nil, RunWorkflowOutput{
		Success:      result.Success,
		TaskID:       result.TaskID,
		WorkflowID:   result.WorkflowID,
		Status:       result.Status,
		TotalTests:   result.TotalTests,
		PassedTests:  result.PassedTests,
		FailedTests:  result.FailedTests,
		Duration:     result.Duration,
		ReportURL:    result.ReportURL,
		ErrorMessage: result.ErrorMessage,
	}, nil
}

// ListTestsInput defines the input parameters for the list_tests tool.
type ListTestsInput struct {
	ProjectDir string `json:"project_dir,omitempty" jsonschema:"description=Path to project directory (defaults to current directory)"`
}

// TestInfo contains information about a test.
type TestInfo struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// WorkflowInfo contains information about a workflow.
type WorkflowInfo struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// ListTestsOutput defines the output for the list_tests tool.
type ListTestsOutput struct {
	Tests     []TestInfo     `json:"tests"`
	Workflows []WorkflowInfo `json:"workflows"`
	ConfigDir string         `json:"config_dir"`
}

// handleListTests handles the list_tests tool call.
func (s *Server) handleListTests(ctx context.Context, req *mcp.CallToolRequest, input ListTestsInput) (*mcp.CallToolResult, ListTestsOutput, error) {
	workDir := input.ProjectDir
	if workDir == "" {
		workDir = s.workDir
	}

	configPath := filepath.Join(workDir, ".revyl", "config.yaml")
	cfg, err := config.LoadProjectConfig(configPath)
	if err != nil {
		return nil, ListTestsOutput{
			Tests:     []TestInfo{},
			Workflows: []WorkflowInfo{},
			ConfigDir: configPath,
		}, nil
	}

	var tests []TestInfo
	for name, id := range cfg.Tests {
		tests = append(tests, TestInfo{Name: name, ID: id})
	}

	var workflows []WorkflowInfo
	for name, id := range cfg.Workflows {
		workflows = append(workflows, WorkflowInfo{Name: name, ID: id})
	}

	return nil, ListTestsOutput{
		Tests:     tests,
		Workflows: workflows,
		ConfigDir: configPath,
	}, nil
}

// GetTestStatusInput defines the input parameters for the get_test_status tool.
type GetTestStatusInput struct {
	TaskID string `json:"task_id" jsonschema:"description=The task ID of the test execution"`
}

// GetTestStatusOutput defines the output for the get_test_status tool.
type GetTestStatusOutput struct {
	Status         string `json:"status"`
	Progress       int    `json:"progress"`
	CurrentStep    string `json:"current_step,omitempty"`
	CompletedSteps int    `json:"completed_steps"`
	TotalSteps     int    `json:"total_steps"`
	Duration       string `json:"duration,omitempty"`
	ErrorMessage   string `json:"error_message,omitempty"`
}

// handleGetTestStatus handles the get_test_status tool call.
func (s *Server) handleGetTestStatus(ctx context.Context, req *mcp.CallToolRequest, input GetTestStatusInput) (*mcp.CallToolResult, GetTestStatusOutput, error) {
	// Validate input
	if input.TaskID == "" {
		return nil, GetTestStatusOutput{
			Status:       "error",
			ErrorMessage: "task_id is required",
		}, nil
	}

	// Call the API to get test status
	status, err := s.apiClient.GetTestStatus(ctx, input.TaskID)
	if err != nil {
		return nil, GetTestStatusOutput{
			Status:       "error",
			ErrorMessage: fmt.Sprintf("failed to get test status: %v", err),
		}, nil
	}

	// Calculate duration if we have timing info
	var duration string
	if status.ExecutionTimeSeconds > 0 {
		duration = fmt.Sprintf("%.1fs", status.ExecutionTimeSeconds)
	}

	return nil, GetTestStatusOutput{
		Status:         status.Status,
		Progress:       int(status.Progress),
		CurrentStep:    status.CurrentStep,
		CompletedSteps: status.StepsCompleted,
		TotalSteps:     status.TotalSteps,
		Duration:       duration,
		ErrorMessage:   status.ErrorMessage,
	}, nil
}

// CreateTestInput defines input for create_test tool.
type CreateTestInput struct {
	Name        string `json:"name" jsonschema:"description=Test name"`
	Platform    string `json:"platform" jsonschema:"description=Target platform (ios or android)"`
	YAMLContent string `json:"yaml_content,omitempty" jsonschema:"description=Optional YAML test definition. If provided, creates test with these blocks."`
	BuildVarID  string `json:"build_var_id,omitempty" jsonschema:"description=Build variable ID to associate with test"`
}

// CreateTestOutput defines output for create_test tool.
type CreateTestOutput struct {
	Success  bool   `json:"success"`
	TestID   string `json:"test_id,omitempty"`
	TestName string `json:"test_name,omitempty"`
	TestURL  string `json:"test_url,omitempty"`
	Error    string `json:"error,omitempty"`
}

// handleCreateTest handles the create_test tool call.
func (s *Server) handleCreateTest(ctx context.Context, req *mcp.CallToolRequest, input CreateTestInput) (*mcp.CallToolResult, CreateTestOutput, error) {
	// Validate required fields
	if input.Name == "" {
		return nil, CreateTestOutput{
			Success: false,
			Error:   "name is required",
		}, nil
	}

	if input.Platform == "" {
		return nil, CreateTestOutput{
			Success: false,
			Error:   "platform is required (ios or android)",
		}, nil
	}

	// Validate platform value
	platform := strings.ToLower(input.Platform)
	if platform != "ios" && platform != "android" {
		return nil, CreateTestOutput{
			Success: false,
			Error:   "platform must be 'ios' or 'android'",
		}, nil
	}

	// Validate YAML if provided
	if input.YAMLContent != "" {
		validationResult := yaml.ValidateYAML(input.YAMLContent)
		if !validationResult.Valid {
			return nil, CreateTestOutput{
				Success: false,
				Error:   fmt.Sprintf("YAML validation failed: %v", validationResult.Errors),
			}, nil
		}
	}

	result, err := execution.CreateTest(ctx, s.apiClient.GetAPIKey(), execution.CreateTestParams{
		Name:        input.Name,
		Platform:    platform,
		YAMLContent: input.YAMLContent,
		BuildVarID:  input.BuildVarID,
		DevMode:     false,
	})
	if err != nil {
		return nil, CreateTestOutput{Success: false, Error: err.Error()}, nil
	}

	return nil, CreateTestOutput{
		Success:  true,
		TestID:   result.TestID,
		TestName: result.TestName,
		TestURL:  result.TestURL,
	}, nil
}

// CreateWorkflowInput defines input for create_workflow tool.
type CreateWorkflowInput struct {
	Name    string   `json:"name" jsonschema:"description=Workflow name"`
	TestIDs []string `json:"test_ids,omitempty" jsonschema:"description=Optional test IDs to include in workflow"`
}

// CreateWorkflowOutput defines output for create_workflow tool.
type CreateWorkflowOutput struct {
	Success      bool   `json:"success"`
	WorkflowID   string `json:"workflow_id,omitempty"`
	WorkflowName string `json:"workflow_name,omitempty"`
	WorkflowURL  string `json:"workflow_url,omitempty"`
	Error        string `json:"error,omitempty"`
}

// handleCreateWorkflow handles the create_workflow tool call.
func (s *Server) handleCreateWorkflow(ctx context.Context, req *mcp.CallToolRequest, input CreateWorkflowInput) (*mcp.CallToolResult, CreateWorkflowOutput, error) {
	// Validate required fields
	if input.Name == "" {
		return nil, CreateWorkflowOutput{
			Success: false,
			Error:   "name is required",
		}, nil
	}

	// Get user ID from API key validation
	userInfo, err := s.apiClient.ValidateAPIKey(ctx)
	if err != nil {
		return nil, CreateWorkflowOutput{Success: false, Error: "Failed to validate API key: " + err.Error()}, nil
	}

	result, err := execution.CreateWorkflow(ctx, s.apiClient.GetAPIKey(), execution.CreateWorkflowParams{
		Name:    input.Name,
		TestIDs: input.TestIDs,
		Owner:   userInfo.UserID,
		DevMode: false,
	})
	if err != nil {
		return nil, CreateWorkflowOutput{Success: false, Error: err.Error()}, nil
	}

	return nil, CreateWorkflowOutput{
		Success:      true,
		WorkflowID:   result.WorkflowID,
		WorkflowName: result.WorkflowName,
		WorkflowURL:  result.WorkflowURL,
	}, nil
}

// ValidateYAMLInput defines input for validate_yaml tool.
type ValidateYAMLInput struct {
	Content string `json:"content" jsonschema:"description=YAML test content to validate"`
}

// ValidateYAMLOutput defines output for validate_yaml tool.
type ValidateYAMLOutput struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// handleValidateYAML handles the validate_yaml tool call.
func (s *Server) handleValidateYAML(ctx context.Context, req *mcp.CallToolRequest, input ValidateYAMLInput) (*mcp.CallToolResult, ValidateYAMLOutput, error) {
	result := yaml.ValidateYAML(input.Content)
	return nil, ValidateYAMLOutput{
		Valid:    result.Valid,
		Errors:   result.Errors,
		Warnings: result.Warnings,
	}, nil
}

// GetSchemaInput defines input for get_schema tool.
type GetSchemaInput struct {
	Format string `json:"format,omitempty" jsonschema:"description=Output format: json (default), markdown, or llm"`
}

// GetSchemaOutput defines output for get_schema tool.
type GetSchemaOutput struct {
	CLISchema      interface{} `json:"cli_schema,omitempty"`
	YAMLTestSchema interface{} `json:"yaml_test_schema,omitempty"`
	Markdown       string      `json:"markdown,omitempty"`
	LLMFormat      string      `json:"llm_format,omitempty"`
}

// handleGetSchema handles the get_schema tool call.
func (s *Server) handleGetSchema(ctx context.Context, req *mcp.CallToolRequest, input GetSchemaInput) (*mcp.CallToolResult, GetSchemaOutput, error) {
	format := input.Format
	if format == "" {
		format = "json"
	}

	// Generate CLI schema if we have the root command
	var cliSchema *schema.CLISchema
	if s.rootCmd != nil {
		cliSchema = schema.GetCLISchema(s.rootCmd, s.version)
	}

	switch format {
	case "json":
		return nil, GetSchemaOutput{
			CLISchema:      cliSchema,
			YAMLTestSchema: schema.YAMLTestSchemaJSON(),
		}, nil
	case "markdown":
		var md string
		if cliSchema != nil {
			md = schema.ToMarkdown(cliSchema)
		}
		md += "\n---\n\n" + schema.GetYAMLTestSchema()
		return nil, GetSchemaOutput{
			Markdown: md,
		}, nil
	case "llm":
		var llmOutput string
		if cliSchema != nil {
			llmOutput = schema.ToLLMFormat(cliSchema, schema.GetYAMLTestSchema())
		} else {
			llmOutput = schema.GetYAMLTestSchema()
		}
		return nil, GetSchemaOutput{
			LLMFormat: llmOutput,
		}, nil
	default:
		return nil, GetSchemaOutput{
			CLISchema:      cliSchema,
			YAMLTestSchema: schema.YAMLTestSchemaJSON(),
		}, nil
	}
}

// ListBuildsInput defines input for list_builds tool.
type ListBuildsInput struct {
	Platform string `json:"platform,omitempty" jsonschema:"description=Filter by platform (ios or android)"`
	Limit    int    `json:"limit,omitempty" jsonschema:"description=Maximum number of builds to return (default 20)"`
}

// BuildInfo contains information about a build variable.
type BuildInfo struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Platform       string `json:"platform"`
	CurrentVersion string `json:"current_version,omitempty"`
	VersionsCount  int    `json:"versions_count"`
}

// ListBuildsOutput defines output for list_builds tool.
type ListBuildsOutput struct {
	Builds       []BuildInfo `json:"builds"`
	Total        int         `json:"total"`
	ErrorMessage string      `json:"error_message,omitempty"`
}

// handleListBuilds handles the list_builds tool call.
func (s *Server) handleListBuilds(ctx context.Context, req *mcp.CallToolRequest, input ListBuildsInput) (*mcp.CallToolResult, ListBuildsOutput, error) {
	limit := input.Limit
	if limit == 0 {
		limit = 20
	}

	result, err := s.apiClient.ListOrgBuildVars(ctx, input.Platform, 1, limit)
	if err != nil {
		return nil, ListBuildsOutput{
			Builds:       []BuildInfo{},
			Total:        0,
			ErrorMessage: fmt.Sprintf("failed to list builds: %v", err),
		}, nil
	}

	var builds []BuildInfo
	for _, b := range result.Items {
		builds = append(builds, BuildInfo{
			ID:             b.ID,
			Name:           b.Name,
			Platform:       b.Platform,
			CurrentVersion: b.CurrentVersion,
			VersionsCount:  b.VersionsCount,
		})
	}

	return nil, ListBuildsOutput{
		Builds: builds,
		Total:  result.Total,
	}, nil
}

// OpenTestEditorInput defines input for open_test_editor tool.
type OpenTestEditorInput struct {
	TestNameOrID string `json:"test_name_or_id" jsonschema:"description=Test name (from config) or UUID"`
}

// OpenTestEditorOutput defines output for open_test_editor tool.
type OpenTestEditorOutput struct {
	Success bool   `json:"success"`
	TestID  string `json:"test_id"`
	TestURL string `json:"test_url"`
	Error   string `json:"error,omitempty"`
}

// handleOpenTestEditor handles the open_test_editor tool call.
func (s *Server) handleOpenTestEditor(ctx context.Context, req *mcp.CallToolRequest, input OpenTestEditorInput) (*mcp.CallToolResult, OpenTestEditorOutput, error) {
	// Validate input
	if input.TestNameOrID == "" {
		return nil, OpenTestEditorOutput{
			Success: false,
			Error:   "test_name_or_id is required",
		}, nil
	}

	result := execution.OpenTestEditor(s.config, execution.OpenTestEditorParams{
		TestNameOrID: input.TestNameOrID,
		DevMode:      false,
	})

	return nil, OpenTestEditorOutput{
		Success: true,
		TestID:  result.TestID,
		TestURL: result.TestURL,
	}, nil
}

// OpenWorkflowEditorInput defines input for open_workflow_editor tool.
type OpenWorkflowEditorInput struct {
	WorkflowNameOrID string `json:"workflow_name_or_id" jsonschema:"description=Workflow name (from config) or UUID"`
}

// OpenWorkflowEditorOutput defines output for open_workflow_editor tool.
type OpenWorkflowEditorOutput struct {
	Success     bool   `json:"success"`
	WorkflowID  string `json:"workflow_id"`
	WorkflowURL string `json:"workflow_url"`
	Error       string `json:"error,omitempty"`
}

// handleOpenWorkflowEditor handles the open_workflow_editor tool call.
func (s *Server) handleOpenWorkflowEditor(ctx context.Context, req *mcp.CallToolRequest, input OpenWorkflowEditorInput) (*mcp.CallToolResult, OpenWorkflowEditorOutput, error) {
	// Validate input
	if input.WorkflowNameOrID == "" {
		return nil, OpenWorkflowEditorOutput{
			Success: false,
			Error:   "workflow_name_or_id is required",
		}, nil
	}

	result := execution.OpenWorkflowEditor(s.config, execution.OpenWorkflowEditorParams{
		WorkflowNameOrID: input.WorkflowNameOrID,
		DevMode:          false,
	})

	return nil, OpenWorkflowEditorOutput{
		Success:     true,
		WorkflowID:  result.WorkflowID,
		WorkflowURL: result.WorkflowURL,
	}, nil
}
