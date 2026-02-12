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

	// Set the version on the API client so the User-Agent header reflects
	// the real CLI build version (e.g. "revyl-cli/1.2.3") instead of "revyl-cli/dev".
	s.apiClient.SetVersion(version)

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
		Name: "create_test",
		Description: `Create a new test from YAML content or just a name.

RECOMMENDED: Before creating a test, read the app's source code (screens, components, routes) to understand the real UI labels, navigation flow, and user-facing outcomes. Use get_schema for the YAML format reference.`,
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

	// cancel_test tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "cancel_test",
		Description: "Cancel a running test execution by task ID.",
	}, s.handleCancelTest)

	// cancel_workflow tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "cancel_workflow",
		Description: "Cancel a running workflow execution by task ID.",
	}, s.handleCancelWorkflow)

	// delete_test tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_test",
		Description: "Delete a test by name (alias from config) or UUID.",
	}, s.handleDeleteTest)

	// delete_workflow tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_workflow",
		Description: "Delete a workflow by name (alias from config) or UUID.",
	}, s.handleDeleteWorkflow)

	// list_remote_tests tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_remote_tests",
		Description: "List all tests in the organization from the remote API (not just local config).",
	}, s.handleListRemoteTests)

	// list_workflows tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_workflows",
		Description: "List all workflows in the organization from the remote API.",
	}, s.handleListWorkflows)

	// auth_status tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "auth_status",
		Description: "Check current authentication status and return user info.",
	}, s.handleAuthStatus)

	// create_app tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "create_app",
		Description: "Create a new app for build uploads.",
	}, s.handleCreateApp)

	// delete_app tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_app",
		Description: "Delete an app and all its build versions by app ID.",
	}, s.handleDeleteApp)

	// --- Module tools ---

	// list_modules tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_modules",
		Description: "List all reusable test modules in the organization. Modules are groups of test blocks that can be imported into any test via module_import blocks.",
	}, s.handleListModules)

	// get_module tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_module",
		Description: "Get details of a specific module by ID, including its blocks.",
	}, s.handleGetModule)

	// create_module tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "create_module",
		Description: "Create a new reusable test module from a list of blocks.",
	}, s.handleCreateModule)

	// delete_module tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_module",
		Description: "Delete a module by ID. Returns 409 if the module is in use by tests.",
	}, s.handleDeleteModule)

	// insert_module_block tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "insert_module_block",
		Description: "Given a module name or ID, returns a module_import block YAML snippet ready to insert into a test. Use this to compose tests with reusable modules.",
	}, s.handleInsertModuleBlock)

	// --- Tag tools ---

	// list_tags tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_tags",
		Description: "List all tags in the organization with test counts. Tags are used to categorize and filter tests.",
	}, s.handleListTags)

	// create_tag tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "create_tag",
		Description: "Create a new tag. If a tag with the same name already exists, the existing tag is returned (upsert behavior).",
	}, s.handleCreateTag)

	// delete_tag tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_tag",
		Description: "Delete a tag by name or ID. This removes it from all tests.",
	}, s.handleDeleteTag)

	// get_test_tags tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_test_tags",
		Description: "Get all tags assigned to a specific test.",
	}, s.handleGetTestTags)

	// set_test_tags tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "set_test_tags",
		Description: "Replace all tags on a test with the given tag names. Tags are auto-created if they don't exist.",
	}, s.handleSetTestTags)

	// add_remove_test_tags tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "add_remove_test_tags",
		Description: "Add and/or remove tags on a test without replacing all existing tags.",
	}, s.handleAddRemoveTestTags)
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
	AppID       string `json:"app_id,omitempty" jsonschema:"description=App ID to associate with test"`
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
		AppID:       input.AppID,
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

// BuildInfo contains information about an app.
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

	result, err := s.apiClient.ListApps(ctx, input.Platform, 1, limit)
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

// --- Cancel tools ---

// CancelTestInput defines input for cancel_test tool.
type CancelTestInput struct {
	TaskID string `json:"task_id" jsonschema:"description=The task ID of the running test execution to cancel"`
}

// CancelTestOutput defines output for cancel_test tool.
type CancelTestOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// handleCancelTest handles the cancel_test tool call.
func (s *Server) handleCancelTest(ctx context.Context, req *mcp.CallToolRequest, input CancelTestInput) (*mcp.CallToolResult, CancelTestOutput, error) {
	if input.TaskID == "" {
		return nil, CancelTestOutput{Success: false, Error: "task_id is required"}, nil
	}

	resp, err := s.apiClient.CancelTest(ctx, input.TaskID)
	if err != nil {
		return nil, CancelTestOutput{Success: false, Error: fmt.Sprintf("failed to cancel test: %v", err)}, nil
	}

	return nil, CancelTestOutput{
		Success: resp.Success,
		Message: resp.Message,
	}, nil
}

// CancelWorkflowInput defines input for cancel_workflow tool.
type CancelWorkflowInput struct {
	TaskID string `json:"task_id" jsonschema:"description=The task ID of the running workflow execution to cancel"`
}

// CancelWorkflowOutput defines output for cancel_workflow tool.
type CancelWorkflowOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// handleCancelWorkflow handles the cancel_workflow tool call.
func (s *Server) handleCancelWorkflow(ctx context.Context, req *mcp.CallToolRequest, input CancelWorkflowInput) (*mcp.CallToolResult, CancelWorkflowOutput, error) {
	if input.TaskID == "" {
		return nil, CancelWorkflowOutput{Success: false, Error: "task_id is required"}, nil
	}

	resp, err := s.apiClient.CancelWorkflow(ctx, input.TaskID)
	if err != nil {
		return nil, CancelWorkflowOutput{Success: false, Error: fmt.Sprintf("failed to cancel workflow: %v", err)}, nil
	}

	return nil, CancelWorkflowOutput{
		Success: resp.Success,
		Message: resp.Message,
	}, nil
}

// --- Delete tools ---

// DeleteTestInput defines input for delete_test tool.
type DeleteTestInput struct {
	TestNameOrID string `json:"test_name_or_id" jsonschema:"description=Test name (alias from config) or UUID"`
}

// DeleteTestOutput defines output for delete_test tool.
type DeleteTestOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// handleDeleteTest handles the delete_test tool call.
func (s *Server) handleDeleteTest(ctx context.Context, req *mcp.CallToolRequest, input DeleteTestInput) (*mcp.CallToolResult, DeleteTestOutput, error) {
	if input.TestNameOrID == "" {
		return nil, DeleteTestOutput{Success: false, Error: "test_name_or_id is required"}, nil
	}

	// Resolve name to ID from config
	testID := input.TestNameOrID
	if s.config != nil {
		if id, ok := s.config.Tests[input.TestNameOrID]; ok {
			testID = id
		}
	}

	resp, err := s.apiClient.DeleteTest(ctx, testID)
	if err != nil {
		return nil, DeleteTestOutput{Success: false, Error: fmt.Sprintf("failed to delete test: %v", err)}, nil
	}

	return nil, DeleteTestOutput{
		Success: true,
		Message: resp.Message,
	}, nil
}

// DeleteWorkflowInput defines input for delete_workflow tool.
type DeleteWorkflowInput struct {
	WorkflowNameOrID string `json:"workflow_name_or_id" jsonschema:"description=Workflow name (alias from config) or UUID"`
}

// DeleteWorkflowOutput defines output for delete_workflow tool.
type DeleteWorkflowOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// handleDeleteWorkflow handles the delete_workflow tool call.
func (s *Server) handleDeleteWorkflow(ctx context.Context, req *mcp.CallToolRequest, input DeleteWorkflowInput) (*mcp.CallToolResult, DeleteWorkflowOutput, error) {
	if input.WorkflowNameOrID == "" {
		return nil, DeleteWorkflowOutput{Success: false, Error: "workflow_name_or_id is required"}, nil
	}

	// Resolve name to ID from config
	workflowID := input.WorkflowNameOrID
	if s.config != nil {
		if id, ok := s.config.Workflows[input.WorkflowNameOrID]; ok {
			workflowID = id
		}
	}

	resp, err := s.apiClient.DeleteWorkflow(ctx, workflowID)
	if err != nil {
		return nil, DeleteWorkflowOutput{Success: false, Error: fmt.Sprintf("failed to delete workflow: %v", err)}, nil
	}

	return nil, DeleteWorkflowOutput{
		Success: true,
		Message: resp.Message,
	}, nil
}

// --- List tools ---

// ListRemoteTestsInput defines input for list_remote_tests tool.
type ListRemoteTestsInput struct {
	Limit  int `json:"limit,omitempty" jsonschema:"description=Maximum number of tests to return (default 50)"`
	Offset int `json:"offset,omitempty" jsonschema:"description=Offset for pagination (default 0)"`
}

// RemoteTestInfo contains information about a remote test.
type RemoteTestInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Status   string `json:"status,omitempty"`
}

// ListRemoteTestsOutput defines output for list_remote_tests tool.
type ListRemoteTestsOutput struct {
	Tests []RemoteTestInfo `json:"tests"`
	Total int              `json:"total"`
	Error string           `json:"error,omitempty"`
}

// handleListRemoteTests handles the list_remote_tests tool call.
func (s *Server) handleListRemoteTests(ctx context.Context, req *mcp.CallToolRequest, input ListRemoteTestsInput) (*mcp.CallToolResult, ListRemoteTestsOutput, error) {
	limit := input.Limit
	if limit == 0 {
		limit = 50
	}

	resp, err := s.apiClient.ListOrgTests(ctx, limit, input.Offset)
	if err != nil {
		return nil, ListRemoteTestsOutput{
			Tests: []RemoteTestInfo{},
			Error: fmt.Sprintf("failed to list remote tests: %v", err),
		}, nil
	}

	var tests []RemoteTestInfo
	for _, t := range resp.Tests {
		tests = append(tests, RemoteTestInfo{
			ID:       t.ID,
			Name:     t.Name,
			Platform: t.Platform,
		})
	}

	return nil, ListRemoteTestsOutput{
		Tests: tests,
		Total: resp.Count,
	}, nil
}

// ListWorkflowsInput defines input for list_workflows tool.
type ListWorkflowsInput struct{}

// ListWorkflowsOutput defines output for list_workflows tool.
type ListWorkflowsOutput struct {
	Workflows []WorkflowInfo `json:"workflows"`
	Total     int            `json:"total"`
	Error     string         `json:"error,omitempty"`
}

// handleListWorkflows handles the list_workflows tool call.
func (s *Server) handleListWorkflows(ctx context.Context, req *mcp.CallToolRequest, input ListWorkflowsInput) (*mcp.CallToolResult, ListWorkflowsOutput, error) {
	resp, err := s.apiClient.ListWorkflows(ctx)
	if err != nil {
		return nil, ListWorkflowsOutput{
			Workflows: []WorkflowInfo{},
			Error:     fmt.Sprintf("failed to list workflows: %v", err),
		}, nil
	}

	var workflows []WorkflowInfo
	for _, w := range resp.Workflows {
		workflows = append(workflows, WorkflowInfo{
			Name: w.Name,
			ID:   w.ID,
		})
	}

	return nil, ListWorkflowsOutput{
		Workflows: workflows,
		Total:     resp.Count,
	}, nil
}

// --- Auth tool ---

// AuthStatusInput defines input for auth_status tool.
type AuthStatusInput struct{}

// AuthStatusOutput defines output for auth_status tool.
type AuthStatusOutput struct {
	Authenticated bool   `json:"authenticated"`
	Email         string `json:"email,omitempty"`
	UserID        string `json:"user_id,omitempty"`
	OrgID         string `json:"org_id,omitempty"`
	AuthMethod    string `json:"auth_method,omitempty"`
}

// handleAuthStatus handles the auth_status tool call.
func (s *Server) handleAuthStatus(ctx context.Context, req *mcp.CallToolRequest, input AuthStatusInput) (*mcp.CallToolResult, AuthStatusOutput, error) {
	mgr := auth.NewManager()
	creds, err := mgr.GetCredentials()
	if err != nil || creds == nil || !creds.HasValidAuth() {
		return nil, AuthStatusOutput{Authenticated: false}, nil
	}

	return nil, AuthStatusOutput{
		Authenticated: true,
		Email:         creds.Email,
		UserID:        creds.UserID,
		OrgID:         creds.OrgID,
		AuthMethod:    creds.AuthMethod,
	}, nil
}

// --- App tools ---

// CreateAppInput defines input for create_app tool.
type CreateAppInput struct {
	Name     string `json:"name" jsonschema:"description=App name"`
	Platform string `json:"platform" jsonschema:"description=Target platform (ios or android)"`
}

// CreateAppOutput defines output for create_app tool.
type CreateAppOutput struct {
	Success bool   `json:"success"`
	AppID   string `json:"app_id,omitempty"`
	AppName string `json:"app_name,omitempty"`
	Error   string `json:"error,omitempty"`
}

// handleCreateApp handles the create_app tool call.
func (s *Server) handleCreateApp(ctx context.Context, req *mcp.CallToolRequest, input CreateAppInput) (*mcp.CallToolResult, CreateAppOutput, error) {
	if input.Name == "" {
		return nil, CreateAppOutput{Success: false, Error: "name is required"}, nil
	}

	platform := strings.ToLower(input.Platform)
	if platform != "ios" && platform != "android" {
		return nil, CreateAppOutput{Success: false, Error: "platform must be 'ios' or 'android'"}, nil
	}

	resp, err := s.apiClient.CreateApp(ctx, &api.CreateAppRequest{
		Name:     input.Name,
		Platform: platform,
	})
	if err != nil {
		return nil, CreateAppOutput{Success: false, Error: fmt.Sprintf("failed to create app: %v", err)}, nil
	}

	return nil, CreateAppOutput{
		Success: true,
		AppID:   resp.ID,
		AppName: resp.Name,
	}, nil
}

// DeleteAppInput defines input for delete_app tool.
type DeleteAppInput struct {
	AppID string `json:"app_id" jsonschema:"description=The UUID of the app to delete"`
}

// DeleteAppOutput defines output for delete_app tool.
type DeleteAppOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// handleDeleteApp handles the delete_app tool call.
func (s *Server) handleDeleteApp(ctx context.Context, req *mcp.CallToolRequest, input DeleteAppInput) (*mcp.CallToolResult, DeleteAppOutput, error) {
	if input.AppID == "" {
		return nil, DeleteAppOutput{Success: false, Error: "app_id is required"}, nil
	}

	resp, err := s.apiClient.DeleteApp(ctx, input.AppID)
	if err != nil {
		return nil, DeleteAppOutput{Success: false, Error: fmt.Sprintf("failed to delete app: %v", err)}, nil
	}

	return nil, DeleteAppOutput{
		Success: true,
		Message: resp.Message,
	}, nil
}

// --- Module tools ---

// ListModulesInput defines input for list_modules tool.
type ListModulesInput struct {
	NameFilter string `json:"name_filter,omitempty" jsonschema:"description=Optional filter to search modules by name"`
}

// ModuleInfo contains information about a module.
type ModuleInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	BlockCount  int    `json:"block_count"`
}

// ListModulesOutput defines output for list_modules tool.
type ListModulesOutput struct {
	Modules []ModuleInfo `json:"modules"`
	Total   int          `json:"total"`
	Error   string       `json:"error,omitempty"`
}

// handleListModules handles the list_modules tool call.
func (s *Server) handleListModules(ctx context.Context, req *mcp.CallToolRequest, input ListModulesInput) (*mcp.CallToolResult, ListModulesOutput, error) {
	resp, err := s.apiClient.ListModules(ctx)
	if err != nil {
		return nil, ListModulesOutput{
			Modules: []ModuleInfo{},
			Error:   fmt.Sprintf("failed to list modules: %v", err),
		}, nil
	}

	var modules []ModuleInfo
	for _, m := range resp.Result {
		// Apply name filter if specified
		if input.NameFilter != "" {
			nameLower := strings.ToLower(m.Name)
			filterLower := strings.ToLower(input.NameFilter)
			if !strings.Contains(nameLower, filterLower) {
				continue
			}
		}
		modules = append(modules, ModuleInfo{
			ID:          m.ID,
			Name:        m.Name,
			Description: m.Description,
			BlockCount:  len(m.Blocks),
		})
	}

	if modules == nil {
		modules = []ModuleInfo{}
	}

	return nil, ListModulesOutput{
		Modules: modules,
		Total:   len(modules),
	}, nil
}

// GetModuleInput defines input for get_module tool.
type GetModuleInput struct {
	ModuleID string `json:"module_id" jsonschema:"description=The UUID of the module to retrieve"`
}

// GetModuleOutput defines output for get_module tool.
type GetModuleOutput struct {
	Success     bool          `json:"success"`
	ID          string        `json:"id,omitempty"`
	Name        string        `json:"name,omitempty"`
	Description string        `json:"description,omitempty"`
	Blocks      []interface{} `json:"blocks,omitempty"`
	Error       string        `json:"error,omitempty"`
}

// handleGetModule handles the get_module tool call.
func (s *Server) handleGetModule(ctx context.Context, req *mcp.CallToolRequest, input GetModuleInput) (*mcp.CallToolResult, GetModuleOutput, error) {
	if input.ModuleID == "" {
		return nil, GetModuleOutput{Success: false, Error: "module_id is required"}, nil
	}

	resp, err := s.apiClient.GetModule(ctx, input.ModuleID)
	if err != nil {
		return nil, GetModuleOutput{Success: false, Error: fmt.Sprintf("failed to get module: %v", err)}, nil
	}

	return nil, GetModuleOutput{
		Success:     true,
		ID:          resp.Result.ID,
		Name:        resp.Result.Name,
		Description: resp.Result.Description,
		Blocks:      resp.Result.Blocks,
	}, nil
}

// CreateModuleInput defines input for create_module tool.
type CreateModuleInput struct {
	Name        string        `json:"name" jsonschema:"description=Module name"`
	Description string        `json:"description,omitempty" jsonschema:"description=Optional module description"`
	Blocks      []interface{} `json:"blocks" jsonschema:"description=Array of test block objects"`
}

// CreateModuleOutput defines output for create_module tool.
type CreateModuleOutput struct {
	Success  bool   `json:"success"`
	ModuleID string `json:"module_id,omitempty"`
	Name     string `json:"name,omitempty"`
	Error    string `json:"error,omitempty"`
}

// handleCreateModule handles the create_module tool call.
func (s *Server) handleCreateModule(ctx context.Context, req *mcp.CallToolRequest, input CreateModuleInput) (*mcp.CallToolResult, CreateModuleOutput, error) {
	if input.Name == "" {
		return nil, CreateModuleOutput{Success: false, Error: "name is required"}, nil
	}

	if len(input.Blocks) == 0 {
		return nil, CreateModuleOutput{Success: false, Error: "blocks array is required and must not be empty"}, nil
	}

	resp, err := s.apiClient.CreateModule(ctx, &api.CLICreateModuleRequest{
		Name:        input.Name,
		Description: input.Description,
		Blocks:      input.Blocks,
	})
	if err != nil {
		return nil, CreateModuleOutput{Success: false, Error: fmt.Sprintf("failed to create module: %v", err)}, nil
	}

	return nil, CreateModuleOutput{
		Success:  true,
		ModuleID: resp.Result.ID,
		Name:     resp.Result.Name,
	}, nil
}

// DeleteModuleInput defines input for delete_module tool.
type DeleteModuleInput struct {
	ModuleID string `json:"module_id" jsonschema:"description=The UUID of the module to delete"`
}

// DeleteModuleOutput defines output for delete_module tool.
type DeleteModuleOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// handleDeleteModule handles the delete_module tool call.
func (s *Server) handleDeleteModule(ctx context.Context, req *mcp.CallToolRequest, input DeleteModuleInput) (*mcp.CallToolResult, DeleteModuleOutput, error) {
	if input.ModuleID == "" {
		return nil, DeleteModuleOutput{Success: false, Error: "module_id is required"}, nil
	}

	resp, err := s.apiClient.DeleteModule(ctx, input.ModuleID)
	if err != nil {
		return nil, DeleteModuleOutput{Success: false, Error: fmt.Sprintf("failed to delete module: %v", err)}, nil
	}

	return nil, DeleteModuleOutput{
		Success: true,
		Message: resp.Message,
	}, nil
}

// InsertModuleBlockInput defines input for insert_module_block tool.
type InsertModuleBlockInput struct {
	ModuleNameOrID string `json:"module_name_or_id" jsonschema:"description=Module name or UUID to generate the import block for"`
}

// InsertModuleBlockOutput defines output for insert_module_block tool.
type InsertModuleBlockOutput struct {
	Success         bool   `json:"success"`
	YAMLSnippet     string `json:"yaml_snippet,omitempty"`
	ModuleID        string `json:"module_id,omitempty"`
	ModuleName      string `json:"module_name,omitempty"`
	BlockType       string `json:"block_type,omitempty"`
	StepDescription string `json:"step_description,omitempty"`
	Error           string `json:"error,omitempty"`
}

// handleInsertModuleBlock handles the insert_module_block tool call.
func (s *Server) handleInsertModuleBlock(ctx context.Context, req *mcp.CallToolRequest, input InsertModuleBlockInput) (*mcp.CallToolResult, InsertModuleBlockOutput, error) {
	if input.ModuleNameOrID == "" {
		return nil, InsertModuleBlockOutput{Success: false, Error: "module_name_or_id is required"}, nil
	}

	// Resolve module name or ID
	var moduleID, moduleName string

	// Try as UUID first
	if len(input.ModuleNameOrID) == 36 {
		resp, err := s.apiClient.GetModule(ctx, input.ModuleNameOrID)
		if err == nil {
			moduleID = resp.Result.ID
			moduleName = resp.Result.Name
		}
	}

	// If not found by ID, search by name
	if moduleID == "" {
		listResp, err := s.apiClient.ListModules(ctx)
		if err != nil {
			return nil, InsertModuleBlockOutput{Success: false, Error: fmt.Sprintf("failed to list modules: %v", err)}, nil
		}

		for _, m := range listResp.Result {
			if strings.EqualFold(m.Name, input.ModuleNameOrID) {
				moduleID = m.ID
				moduleName = m.Name
				break
			}
		}
	}

	if moduleID == "" {
		return nil, InsertModuleBlockOutput{Success: false, Error: fmt.Sprintf("module '%s' not found", input.ModuleNameOrID)}, nil
	}

	yamlSnippet := fmt.Sprintf("- type: module_import\n  step_description: \"%s\"\n  module_id: \"%s\"", moduleName, moduleID)

	return nil, InsertModuleBlockOutput{
		Success:         true,
		YAMLSnippet:     yamlSnippet,
		ModuleID:        moduleID,
		ModuleName:      moduleName,
		BlockType:       "module_import",
		StepDescription: moduleName,
	}, nil
}

// --- Tag tools ---

// ListTagsInput defines input for list_tags tool.
type ListTagsInput struct {
	NameFilter string `json:"name_filter,omitempty" jsonschema:"description=Optional filter to search tags by name"`
}

// TagInfo contains information about a tag.
type TagInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color,omitempty"`
	Description string `json:"description,omitempty"`
	TestCount   int    `json:"test_count"`
}

// ListTagsOutput defines output for list_tags tool.
type ListTagsOutput struct {
	Tags  []TagInfo `json:"tags"`
	Total int       `json:"total"`
	Error string    `json:"error,omitempty"`
}

// handleListTags handles the list_tags tool call.
func (s *Server) handleListTags(ctx context.Context, req *mcp.CallToolRequest, input ListTagsInput) (*mcp.CallToolResult, ListTagsOutput, error) {
	resp, err := s.apiClient.ListTags(ctx)
	if err != nil {
		return nil, ListTagsOutput{
			Tags:  []TagInfo{},
			Error: fmt.Sprintf("failed to list tags: %v", err),
		}, nil
	}

	var tags []TagInfo
	for _, t := range resp.Tags {
		// Apply name filter if specified
		if input.NameFilter != "" {
			if !strings.Contains(strings.ToLower(t.Name), strings.ToLower(input.NameFilter)) {
				continue
			}
		}
		tags = append(tags, TagInfo{
			ID:          t.ID,
			Name:        t.Name,
			Color:       t.Color,
			Description: t.Description,
			TestCount:   t.TestCount,
		})
	}

	if tags == nil {
		tags = []TagInfo{}
	}

	return nil, ListTagsOutput{
		Tags:  tags,
		Total: len(tags),
	}, nil
}

// CreateTagInput defines input for create_tag tool.
type CreateTagInput struct {
	Name  string `json:"name" jsonschema:"description=Tag name"`
	Color string `json:"color,omitempty" jsonschema:"description=Tag color as hex string (e.g. #22C55E)"`
}

// CreateTagOutput defines output for create_tag tool.
type CreateTagOutput struct {
	Success bool   `json:"success"`
	TagID   string `json:"tag_id,omitempty"`
	Name    string `json:"name,omitempty"`
	Color   string `json:"color,omitempty"`
	Error   string `json:"error,omitempty"`
}

// handleCreateTag handles the create_tag tool call.
func (s *Server) handleCreateTag(ctx context.Context, req *mcp.CallToolRequest, input CreateTagInput) (*mcp.CallToolResult, CreateTagOutput, error) {
	if input.Name == "" {
		return nil, CreateTagOutput{Success: false, Error: "name is required"}, nil
	}

	resp, err := s.apiClient.CreateTag(ctx, &api.CLICreateTagRequest{
		Name:  input.Name,
		Color: input.Color,
	})
	if err != nil {
		return nil, CreateTagOutput{Success: false, Error: fmt.Sprintf("failed to create tag: %v", err)}, nil
	}

	return nil, CreateTagOutput{
		Success: true,
		TagID:   resp.ID,
		Name:    resp.Name,
		Color:   resp.Color,
	}, nil
}

// DeleteTagInput defines input for delete_tag tool.
type DeleteTagInput struct {
	TagNameOrID string `json:"tag_name_or_id" jsonschema:"description=Tag name or UUID to delete"`
}

// DeleteTagOutput defines output for delete_tag tool.
type DeleteTagOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// handleDeleteTag handles the delete_tag tool call.
func (s *Server) handleDeleteTag(ctx context.Context, req *mcp.CallToolRequest, input DeleteTagInput) (*mcp.CallToolResult, DeleteTagOutput, error) {
	if input.TagNameOrID == "" {
		return nil, DeleteTagOutput{Success: false, Error: "tag_name_or_id is required"}, nil
	}

	// Resolve tag name to ID
	tagID := input.TagNameOrID
	listResp, err := s.apiClient.ListTags(ctx)
	if err != nil {
		return nil, DeleteTagOutput{Success: false, Error: fmt.Sprintf("failed to list tags: %v", err)}, nil
	}

	found := false
	for _, t := range listResp.Tags {
		if t.ID == input.TagNameOrID || strings.EqualFold(t.Name, input.TagNameOrID) {
			tagID = t.ID
			found = true
			break
		}
	}

	if !found {
		return nil, DeleteTagOutput{Success: false, Error: fmt.Sprintf("tag '%s' not found", input.TagNameOrID)}, nil
	}

	err = s.apiClient.DeleteTag(ctx, tagID)
	if err != nil {
		return nil, DeleteTagOutput{Success: false, Error: fmt.Sprintf("failed to delete tag: %v", err)}, nil
	}

	return nil, DeleteTagOutput{
		Success: true,
		Message: "Tag deleted successfully",
	}, nil
}

// GetTestTagsInput defines input for get_test_tags tool.
type GetTestTagsInput struct {
	TestNameOrID string `json:"test_name_or_id" jsonschema:"description=Test name (from config) or UUID"`
}

// GetTestTagsOutput defines output for get_test_tags tool.
type GetTestTagsOutput struct {
	Success  bool      `json:"success"`
	TestID   string    `json:"test_id,omitempty"`
	TestName string    `json:"test_name,omitempty"`
	Tags     []TagInfo `json:"tags,omitempty"`
	Error    string    `json:"error,omitempty"`
}

// handleGetTestTags handles the get_test_tags tool call.
func (s *Server) handleGetTestTags(ctx context.Context, req *mcp.CallToolRequest, input GetTestTagsInput) (*mcp.CallToolResult, GetTestTagsOutput, error) {
	if input.TestNameOrID == "" {
		return nil, GetTestTagsOutput{Success: false, Error: "test_name_or_id is required"}, nil
	}

	// Resolve test name to ID
	testID := input.TestNameOrID
	testName := input.TestNameOrID
	if s.config != nil {
		if id, ok := s.config.Tests[input.TestNameOrID]; ok {
			testID = id
		}
	}

	// If not a UUID, try to find by name in remote tests
	if len(testID) != 36 {
		testsResp, err := s.apiClient.ListOrgTests(ctx, 100, 0)
		if err == nil {
			for _, t := range testsResp.Tests {
				if t.Name == input.TestNameOrID {
					testID = t.ID
					testName = t.Name
					break
				}
			}
		}
	}

	tags, err := s.apiClient.GetTestTags(ctx, testID)
	if err != nil {
		return nil, GetTestTagsOutput{Success: false, Error: fmt.Sprintf("failed to get test tags: %v", err)}, nil
	}

	var tagInfos []TagInfo
	for _, t := range tags {
		tagInfos = append(tagInfos, TagInfo{
			ID:          t.ID,
			Name:        t.Name,
			Color:       t.Color,
			Description: t.Description,
		})
	}

	if tagInfos == nil {
		tagInfos = []TagInfo{}
	}

	return nil, GetTestTagsOutput{
		Success:  true,
		TestID:   testID,
		TestName: testName,
		Tags:     tagInfos,
	}, nil
}

// SetTestTagsInput defines input for set_test_tags tool.
type SetTestTagsInput struct {
	TestNameOrID string   `json:"test_name_or_id" jsonschema:"description=Test name (from config) or UUID"`
	TagNames     []string `json:"tag_names" jsonschema:"description=Tag names to set on the test (replaces all existing tags)"`
}

// SetTestTagsOutput defines output for set_test_tags tool.
type SetTestTagsOutput struct {
	Success bool      `json:"success"`
	TestID  string    `json:"test_id,omitempty"`
	Tags    []TagInfo `json:"tags,omitempty"`
	Error   string    `json:"error,omitempty"`
}

// handleSetTestTags handles the set_test_tags tool call.
func (s *Server) handleSetTestTags(ctx context.Context, req *mcp.CallToolRequest, input SetTestTagsInput) (*mcp.CallToolResult, SetTestTagsOutput, error) {
	if input.TestNameOrID == "" {
		return nil, SetTestTagsOutput{Success: false, Error: "test_name_or_id is required"}, nil
	}

	if len(input.TagNames) == 0 {
		return nil, SetTestTagsOutput{Success: false, Error: "tag_names is required and must not be empty"}, nil
	}

	// Resolve test name to ID
	testID := input.TestNameOrID
	if s.config != nil {
		if id, ok := s.config.Tests[input.TestNameOrID]; ok {
			testID = id
		}
	}

	// If not a UUID, try to find by name
	if len(testID) != 36 {
		testsResp, err := s.apiClient.ListOrgTests(ctx, 100, 0)
		if err == nil {
			for _, t := range testsResp.Tests {
				if t.Name == input.TestNameOrID {
					testID = t.ID
					break
				}
			}
		}
	}

	resp, err := s.apiClient.SyncTestTags(ctx, testID, &api.CLISyncTagsRequest{
		TagNames: input.TagNames,
	})
	if err != nil {
		return nil, SetTestTagsOutput{Success: false, Error: fmt.Sprintf("failed to set tags: %v", err)}, nil
	}

	var tags []TagInfo
	for _, t := range resp.Tags {
		tags = append(tags, TagInfo{
			ID:    t.ID,
			Name:  t.Name,
			Color: t.Color,
		})
	}

	if tags == nil {
		tags = []TagInfo{}
	}

	return nil, SetTestTagsOutput{
		Success: true,
		TestID:  testID,
		Tags:    tags,
	}, nil
}

// AddRemoveTestTagsInput defines input for add_remove_test_tags tool.
type AddRemoveTestTagsInput struct {
	TestNameOrID string   `json:"test_name_or_id" jsonschema:"description=Test name (from config) or UUID"`
	TagsToAdd    []string `json:"tags_to_add,omitempty" jsonschema:"description=Tag names to add to the test"`
	TagsToRemove []string `json:"tags_to_remove,omitempty" jsonschema:"description=Tag names to remove from the test"`
}

// AddRemoveTestTagsOutput defines output for add_remove_test_tags tool.
type AddRemoveTestTagsOutput struct {
	Success bool   `json:"success"`
	TestID  string `json:"test_id,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// handleAddRemoveTestTags handles the add_remove_test_tags tool call.
func (s *Server) handleAddRemoveTestTags(ctx context.Context, req *mcp.CallToolRequest, input AddRemoveTestTagsInput) (*mcp.CallToolResult, AddRemoveTestTagsOutput, error) {
	if input.TestNameOrID == "" {
		return nil, AddRemoveTestTagsOutput{Success: false, Error: "test_name_or_id is required"}, nil
	}

	if len(input.TagsToAdd) == 0 && len(input.TagsToRemove) == 0 {
		return nil, AddRemoveTestTagsOutput{Success: false, Error: "at least one of tags_to_add or tags_to_remove is required"}, nil
	}

	// Resolve test name to ID
	testID := input.TestNameOrID
	if s.config != nil {
		if id, ok := s.config.Tests[input.TestNameOrID]; ok {
			testID = id
		}
	}

	// If not a UUID, try to find by name
	if len(testID) != 36 {
		testsResp, err := s.apiClient.ListOrgTests(ctx, 100, 0)
		if err == nil {
			for _, t := range testsResp.Tests {
				if t.Name == input.TestNameOrID {
					testID = t.ID
					break
				}
			}
		}
	}

	resp, err := s.apiClient.BulkSyncTestTags(ctx, &api.CLIBulkSyncTagsRequest{
		TestIDs:      []string{testID},
		TagsToAdd:    input.TagsToAdd,
		TagsToRemove: input.TagsToRemove,
	})
	if err != nil {
		return nil, AddRemoveTestTagsOutput{Success: false, Error: fmt.Sprintf("failed to update tags: %v", err)}, nil
	}

	if resp.ErrorCount > 0 {
		for _, r := range resp.Results {
			if !r.Success && r.Error != nil {
				return nil, AddRemoveTestTagsOutput{
					Success: false,
					TestID:  testID,
					Error:   *r.Error,
				}, nil
			}
		}
	}

	var parts []string
	if len(input.TagsToAdd) > 0 {
		parts = append(parts, fmt.Sprintf("added: %s", strings.Join(input.TagsToAdd, ", ")))
	}
	if len(input.TagsToRemove) > 0 {
		parts = append(parts, fmt.Sprintf("removed: %s", strings.Join(input.TagsToRemove, ", ")))
	}

	return nil, AddRemoveTestTagsOutput{
		Success: true,
		TestID:  testID,
		Message: strings.Join(parts, "; "),
	}, nil
}
