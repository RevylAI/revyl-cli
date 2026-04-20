package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Profile controls which composite tools are registered.
type Profile string

const (
	ProfileCore Profile = "core"
	ProfileFull Profile = "full"
)

// CompositeInput is the generic input envelope for all composite tools.
// The Action field selects the operation; ActionParams carries the
// action-specific JSON payload that gets decoded by the dispatcher.
type CompositeInput struct {
	Action       string          `json:"action" jsonschema:"required,The operation to perform — see tool description for allowed values"`
	ActionParams json.RawMessage `json:"params,omitempty" jsonschema:"Action-specific parameters (varies by action)"`
}

// CompositeOutput is a thin wrapper returned by all composite dispatchers.
type CompositeOutput struct {
	Action string      `json:"action"`
	Result interface{} `json:"result"`
}

// registerCompositeTools registers composite (action-dispatched) tools
// based on the selected profile. Core profile registers ~10 tools
// sufficient for the dev-loop and test-creation journey. Full profile
// adds workflow, module, script, tag, file, and variable management.
func (s *Server) registerCompositeTools(profile Profile) {
	// --- Always registered (core + full) ---
	s.registerDeviceTools()
	s.registerDevLoopTools()

	s.registerManageTestsTool()
	s.registerManageBuildsTool()
	s.registerUtilityTools()

	// --- Full profile only ---
	if profile == ProfileFull {
		s.registerManageWorkflowsTool()
		s.registerManageModulesTool()
		s.registerManageScriptsTool()
		s.registerManageTagsTool()
		s.registerManageFilesTool()
		s.registerManageVariablesTool()
		s.registerLiveEditorTools()
	}
}

// dispatchComposite decodes ActionParams into the typed input and calls the
// underlying handler. It returns the result wrapped in CompositeOutput.
func dispatchComposite[I any, O any](
	ctx context.Context,
	req *mcp.CallToolRequest,
	input CompositeInput,
	handler func(context.Context, *mcp.CallToolRequest, I) (*mcp.CallToolResult, O, error),
) (*mcp.CallToolResult, CompositeOutput, error) {
	var typed I
	if len(input.ActionParams) > 0 {
		if err := json.Unmarshal(input.ActionParams, &typed); err != nil {
			return nil, CompositeOutput{Action: input.Action, Result: nil},
				fmt.Errorf("invalid params for action %q: %w", input.Action, err)
		}
	}
	toolResult, out, err := handler(ctx, req, typed)
	return toolResult, CompositeOutput{Action: input.Action, Result: out}, err
}

// ---------------------------------------------------------------------------
// manage_tests
// ---------------------------------------------------------------------------

func (s *Server) registerManageTestsTool() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "manage_tests",
		Description: `Manage Revyl tests. Use the "action" param to select an operation.

Actions:
  run          - Run a test by name or ID. Params: test_name (required), retries, build_version_id, location, device_model, os_version, orientation
  list         - List tests from .revyl/config.yaml. No params required.
  list_remote  - List all tests in the organization. No params required.
  create       - Create a test from YAML. Params: yaml_content (required), module_names_or_ids
  update       - Update a test's YAML. Params: test_name (required), yaml_content (required)
  delete       - Delete a test. Params: test_name (required)
  status       - Get execution status. Params: task_id (required)
  cancel       - Cancel a running test. Params: task_id (required)
  validate     - Validate YAML syntax. Params: yaml_content (required)`,
		Annotations: &mcp.ToolAnnotations{
			Title:         "Manage Tests",
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleManageTests)
}

func (s *Server) handleManageTests(ctx context.Context, req *mcp.CallToolRequest, input CompositeInput) (*mcp.CallToolResult, CompositeOutput, error) {
	switch input.Action {
	case "run":
		return dispatchComposite(ctx, req, input, s.handleRunTest)
	case "list":
		return dispatchComposite(ctx, req, input, s.handleListTests)
	case "list_remote":
		return dispatchComposite(ctx, req, input, s.handleListRemoteTests)
	case "create":
		return dispatchComposite(ctx, req, input, s.handleCreateTest)
	case "update":
		return dispatchComposite(ctx, req, input, s.handleUpdateTest)
	case "delete":
		return dispatchComposite(ctx, req, input, s.handleDeleteTest)
	case "status":
		return dispatchComposite(ctx, req, input, s.handleGetTestStatus)
	case "cancel":
		return dispatchComposite(ctx, req, input, s.handleCancelTest)
	case "validate":
		return dispatchComposite(ctx, req, input, s.handleValidateYAML)
	default:
		return nil, CompositeOutput{Action: input.Action},
			fmt.Errorf("unknown action %q for manage_tests; valid: run, list, list_remote, create, update, delete, status, cancel, validate", input.Action)
	}
}

// ---------------------------------------------------------------------------
// manage_builds
// ---------------------------------------------------------------------------

func (s *Server) registerManageBuildsTool() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "manage_builds",
		Description: `Manage app builds. Use the "action" param to select an operation.

Actions:
  list       - List available build versions. Params: app_id, platform
  upload     - Upload a build file. Params: file_path (required), app_id (required), version
  create_app - Create a new app. Params: name (required), platform (required)
  delete_app - Delete an app. Params: app_id (required)`,
		Annotations: &mcp.ToolAnnotations{
			Title:         "Manage Builds",
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleManageBuilds)
}

func (s *Server) handleManageBuilds(ctx context.Context, req *mcp.CallToolRequest, input CompositeInput) (*mcp.CallToolResult, CompositeOutput, error) {
	switch input.Action {
	case "list":
		return dispatchComposite(ctx, req, input, s.handleListBuilds)
	case "upload":
		return dispatchComposite(ctx, req, input, s.handleUploadBuild)
	case "create_app":
		return dispatchComposite(ctx, req, input, s.handleCreateApp)
	case "delete_app":
		return dispatchComposite(ctx, req, input, s.handleDeleteApp)
	default:
		return nil, CompositeOutput{Action: input.Action},
			fmt.Errorf("unknown action %q for manage_builds; valid: list, upload, create_app, delete_app", input.Action)
	}
}

// ---------------------------------------------------------------------------
// manage_workflows (full profile only)
// ---------------------------------------------------------------------------

func (s *Server) registerManageWorkflowsTool() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "manage_workflows",
		Description: `Manage Revyl workflows. Use the "action" param to select an operation.

Actions:
  list          - List all workflows. No params required.
  create        - Create a workflow. Params: name (required)
  delete        - Delete a workflow. Params: workflow_name (required)
  run           - Run a workflow. Params: workflow_name (required), retries, build_version_id
  cancel        - Cancel a running workflow. Params: task_id (required)
  add_tests     - Add tests to a workflow. Params: workflow_name (required), test_names (required)
  remove_tests  - Remove tests from a workflow. Params: workflow_name (required), test_names (required)
  get_settings  - Get workflow settings. Params: workflow_name (required)
  set_location  - Set GPS override. Params: workflow_name (required), location (required)
  set_app       - Set app overrides. Params: workflow_name (required), ios_app_id, android_app_id
  open_editor   - Get browser editor URL. Params: workflow_name (required)`,
		Annotations: &mcp.ToolAnnotations{
			Title:         "Manage Workflows",
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleManageWorkflows)
}

func (s *Server) handleManageWorkflows(ctx context.Context, req *mcp.CallToolRequest, input CompositeInput) (*mcp.CallToolResult, CompositeOutput, error) {
	switch input.Action {
	case "list":
		return dispatchComposite(ctx, req, input, s.handleListWorkflows)
	case "create":
		return dispatchComposite(ctx, req, input, s.handleCreateWorkflow)
	case "delete":
		return dispatchComposite(ctx, req, input, s.handleDeleteWorkflow)
	case "run":
		return dispatchComposite(ctx, req, input, s.handleRunWorkflow)
	case "cancel":
		return dispatchComposite(ctx, req, input, s.handleCancelWorkflow)
	case "add_tests":
		return dispatchComposite(ctx, req, input, s.handleAddTestsToWorkflow)
	case "remove_tests":
		return dispatchComposite(ctx, req, input, s.handleRemoveTestsFromWorkflow)
	case "get_settings":
		return dispatchComposite(ctx, req, input, s.handleGetWorkflowSettings)
	case "set_location":
		return dispatchComposite(ctx, req, input, s.handleSetWorkflowLocation)
	case "set_app":
		return dispatchComposite(ctx, req, input, s.handleSetWorkflowApp)
	case "open_editor":
		return dispatchComposite(ctx, req, input, s.handleOpenWorkflowEditor)
	default:
		return nil, CompositeOutput{Action: input.Action},
			fmt.Errorf("unknown action %q for manage_workflows; valid: list, create, delete, run, cancel, add_tests, remove_tests, get_settings, set_location, set_app, open_editor", input.Action)
	}
}

// ---------------------------------------------------------------------------
// manage_modules (full profile only)
// ---------------------------------------------------------------------------

func (s *Server) registerManageModulesTool() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "manage_modules",
		Description: `Manage reusable test modules. Use the "action" param to select an operation.

Actions:
  list   - List all modules. No params required.
  get    - Get module details. Params: module_id (required)
  create - Create a module. Params: name (required), blocks (required), description
  delete - Delete a module. Params: module_id (required)
  insert - Get a module_import YAML snippet. Params: module_name_or_id (required)`,
		Annotations: &mcp.ToolAnnotations{
			Title: "Manage Modules",
		},
	}, s.handleManageModules)
}

func (s *Server) handleManageModules(ctx context.Context, req *mcp.CallToolRequest, input CompositeInput) (*mcp.CallToolResult, CompositeOutput, error) {
	switch input.Action {
	case "list":
		return dispatchComposite(ctx, req, input, s.handleListModules)
	case "get":
		return dispatchComposite(ctx, req, input, s.handleGetModule)
	case "create":
		return dispatchComposite(ctx, req, input, s.handleCreateModule)
	case "delete":
		return dispatchComposite(ctx, req, input, s.handleDeleteModule)
	case "insert":
		return dispatchComposite(ctx, req, input, s.handleInsertModuleBlock)
	default:
		return nil, CompositeOutput{Action: input.Action},
			fmt.Errorf("unknown action %q for manage_modules; valid: list, get, create, delete, insert", input.Action)
	}
}

// ---------------------------------------------------------------------------
// manage_scripts (full profile only)
// ---------------------------------------------------------------------------

func (s *Server) registerManageScriptsTool() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "manage_scripts",
		Description: `Manage code execution scripts. Use the "action" param to select an operation.

Actions:
  list   - List all scripts. No params required.
  get    - Get script details and code. Params: script_id (required)
  create - Create a script. Params: name (required), code (required), runtime (required), description
  update - Update a script. Params: script_id (required), name, code, runtime, description
  delete - Delete a script. Params: script_id (required)
  insert - Get a code_execution YAML snippet. Params: script_name_or_id (required)`,
		Annotations: &mcp.ToolAnnotations{
			Title: "Manage Scripts",
		},
	}, s.handleManageScripts)
}

func (s *Server) handleManageScripts(ctx context.Context, req *mcp.CallToolRequest, input CompositeInput) (*mcp.CallToolResult, CompositeOutput, error) {
	switch input.Action {
	case "list":
		return dispatchComposite(ctx, req, input, s.handleListScripts)
	case "get":
		return dispatchComposite(ctx, req, input, s.handleGetScript)
	case "create":
		return dispatchComposite(ctx, req, input, s.handleCreateScript)
	case "update":
		return dispatchComposite(ctx, req, input, s.handleUpdateScript)
	case "delete":
		return dispatchComposite(ctx, req, input, s.handleDeleteScript)
	case "insert":
		return dispatchComposite(ctx, req, input, s.handleInsertScriptBlock)
	default:
		return nil, CompositeOutput{Action: input.Action},
			fmt.Errorf("unknown action %q for manage_scripts; valid: list, get, create, update, delete, insert", input.Action)
	}
}

// ---------------------------------------------------------------------------
// manage_tags (full profile only)
// ---------------------------------------------------------------------------

func (s *Server) registerManageTagsTool() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "manage_tags",
		Description: `Manage test tags. Use the "action" param to select an operation.

Actions:
  list          - List all tags with test counts. No params required.
  create        - Create a tag (upsert). Params: name (required), color, description
  delete        - Delete a tag. Params: tag_name_or_id (required)
  get_test_tags - Get tags for a test. Params: test_name (required)
  set_test_tags - Replace all tags on a test. Params: test_name (required), tag_names (required)
  add_remove    - Add/remove tags without replacing. Params: test_name (required), add, remove`,
		Annotations: &mcp.ToolAnnotations{
			Title: "Manage Tags",
		},
	}, s.handleManageTags)
}

func (s *Server) handleManageTags(ctx context.Context, req *mcp.CallToolRequest, input CompositeInput) (*mcp.CallToolResult, CompositeOutput, error) {
	switch input.Action {
	case "list":
		return dispatchComposite(ctx, req, input, s.handleListTags)
	case "create":
		return dispatchComposite(ctx, req, input, s.handleCreateTag)
	case "delete":
		return dispatchComposite(ctx, req, input, s.handleDeleteTag)
	case "get_test_tags":
		return dispatchComposite(ctx, req, input, s.handleGetTestTags)
	case "set_test_tags":
		return dispatchComposite(ctx, req, input, s.handleSetTestTags)
	case "add_remove":
		return dispatchComposite(ctx, req, input, s.handleAddRemoveTestTags)
	default:
		return nil, CompositeOutput{Action: input.Action},
			fmt.Errorf("unknown action %q for manage_tags; valid: list, create, delete, get_test_tags, set_test_tags, add_remove", input.Action)
	}
}

// ---------------------------------------------------------------------------
// manage_files (full profile only)
// ---------------------------------------------------------------------------

func (s *Server) registerManageFilesTool() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "manage_files",
		Description: `Manage organization files. Use the "action" param to select an operation.

Actions:
  list     - List all files. No params required.
  upload   - Upload a file. Params: file_path (required), description
  download - Download a file. Params: file_id (required), output_path (required)
  edit     - Edit file metadata or replace content. Params: file_id (required), filename, description, file_path
  delete   - Delete a file. Params: file_id (required)
  get_url  - Get presigned download URL. Params: file_id (required)`,
		Annotations: &mcp.ToolAnnotations{
			Title:         "Manage Files",
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleManageFiles)
}

func (s *Server) handleManageFiles(ctx context.Context, req *mcp.CallToolRequest, input CompositeInput) (*mcp.CallToolResult, CompositeOutput, error) {
	switch input.Action {
	case "list":
		return dispatchComposite(ctx, req, input, s.handleListFiles)
	case "upload":
		return dispatchComposite(ctx, req, input, s.handleUploadFile)
	case "download":
		return dispatchComposite(ctx, req, input, s.handleDownloadFile)
	case "edit":
		return dispatchComposite(ctx, req, input, s.handleEditFile)
	case "delete":
		return dispatchComposite(ctx, req, input, s.handleDeleteFile)
	case "get_url":
		return dispatchComposite(ctx, req, input, s.handleGetFileDownloadURL)
	default:
		return nil, CompositeOutput{Action: input.Action},
			fmt.Errorf("unknown action %q for manage_files; valid: list, upload, download, edit, delete, get_url", input.Action)
	}
}

// ---------------------------------------------------------------------------
// manage_variables (full profile only)
// ---------------------------------------------------------------------------

func (s *Server) registerManageVariablesTool() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "manage_variables",
		Description: `Manage test variables and environment variables. Use the "action" param to select an operation.

Test variables use {{name}} syntax in step descriptions. Env vars are encrypted at rest and injected at app launch.

Actions:
  list_vars       - List test variables. Params: test_name (required)
  set_var         - Set a test variable. Params: test_name (required), name (required), value (required)
  delete_var      - Delete a test variable. Params: test_name (required), name (required)
  delete_all_vars - Delete all test variables. Params: test_name (required)`,
		Annotations: &mcp.ToolAnnotations{
			Title: "Manage Variables",
		},
	}, s.handleManageVariables)
}

func (s *Server) handleManageVariables(ctx context.Context, req *mcp.CallToolRequest, input CompositeInput) (*mcp.CallToolResult, CompositeOutput, error) {
	switch input.Action {
	case "list_vars":
		return dispatchComposite(ctx, req, input, s.handleListVariables)
	case "set_var":
		return dispatchComposite(ctx, req, input, s.handleSetVariable)
	case "delete_var":
		return dispatchComposite(ctx, req, input, s.handleDeleteVariable)
	case "delete_all_vars":
		return dispatchComposite(ctx, req, input, s.handleDeleteAllVariables)
	default:
		return nil, CompositeOutput{Action: input.Action},
			fmt.Errorf("unknown action %q for manage_variables; valid: list_vars, set_var, delete_var, delete_all_vars", input.Action)
	}
}

// ---------------------------------------------------------------------------
// Utility tools (auth_status, get_schema) — registered as standalone
// ---------------------------------------------------------------------------

func (s *Server) registerUtilityTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "auth_status",
		Description: "Check current authentication status and return user info.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Auth Status",
			ReadOnlyHint: true,
		},
	}, s.handleAuthStatus)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_schema",
		Description: "Get the complete CLI command schema and YAML test schema for LLM reference.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Schema",
			ReadOnlyHint: true,
		},
	}, s.handleGetSchema)
}

// ---------------------------------------------------------------------------
// Live editor tools (full profile only)
// ---------------------------------------------------------------------------

func (s *Server) registerLiveEditorTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "open_test_editor",
		Description: "Open a test in the browser editor, optionally with hot reload.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Open Test Editor",
			ReadOnlyHint: true,
		},
	}, s.handleOpenTestEditor)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "stop_hot_reload",
		Description: "Stop the hot reload session (dev server and tunnel).",
		Annotations: &mcp.ToolAnnotations{
			Title: "Stop Hot Reload",
		},
	}, s.handleStopHotReload)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "hot_reload_status",
		Description: "Check if a hot reload session is active and get current URLs.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Hot Reload Status",
			ReadOnlyHint: true,
		},
	}, s.handleHotReloadStatus)
}
