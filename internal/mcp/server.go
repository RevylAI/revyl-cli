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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	yamlPkg "gopkg.in/yaml.v3"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/execution"
	"github.com/revyl/cli/internal/hotreload"
	_ "github.com/revyl/cli/internal/hotreload/providers"
	"github.com/revyl/cli/internal/schema"
	"github.com/revyl/cli/internal/sse"
	"github.com/revyl/cli/internal/ui"
	"github.com/revyl/cli/internal/yaml"
)

// Server wraps the MCP server with Revyl-specific functionality.
type Server struct {
	mcpServer  *mcp.Server
	apiClient  *api.Client
	config     *config.ProjectConfig
	workDir    string
	version    string
	devMode    bool
	rootCmd    *cobra.Command
	sessionMgr *DeviceSessionManager

	// Hot reload session state (persists across tool calls)
	hotReloadManager *hotreload.Manager
	hotReloadMu      sync.Mutex
	hotReloadTestID  string                 // Test ID the session was started for
	hotReloadResult  *hotreload.StartResult // Cached URLs
}

// NewServer creates a new Revyl MCP server.
//
// Parameters:
//   - version: The CLI version string
//   - devMode: If true, use local development server URLs
//
// Returns:
//   - *Server: A new server instance
//   - error: Any error that occurred during initialization
func NewServer(version string, devMode bool) (*Server, error) {
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

	// Get working directory: prefer explicit env so Cursor (or any host) can set it
	// when the process is spawned with a different cwd (e.g. extension host cwd).
	workDir := os.Getenv("REVYL_PROJECT_DIR")
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			workDir = "."
		}
	} else {
		workDir = filepath.Clean(workDir)
	}
	if repoRoot, findErr := config.FindRepoRoot(workDir); findErr == nil {
		workDir = repoRoot
	}

	// Try to load project config
	var cfg *config.ProjectConfig
	configPath := filepath.Join(workDir, ".revyl", "config.yaml")
	cfg, _ = config.LoadProjectConfig(configPath)

	s := &Server{
		apiClient: api.NewClientWithDevMode(apiKey, devMode),
		config:    cfg,
		workDir:   workDir,
		version:   version,
		devMode:   devMode,
	}

	// Initialize device session manager
	s.sessionMgr = NewDeviceSessionManager(s.apiClient, workDir)

	// Set the version on the API client so the User-Agent header reflects
	// the real CLI build version (e.g. "revyl-cli/1.2.3") instead of "revyl-cli/dev".
	s.apiClient.SetVersion(version)

	// Create MCP server with instructions
	s.mcpServer = mcp.NewServer(
		&mcp.Implementation{
			Name:    "revyl",
			Version: version,
		},
		&mcp.ServerOptions{
			Instructions: `Revyl provides cloud-hosted Android and iOS device interaction for AI agents, plus test/workflow management, modules, scripts, and build management.

## Tool Categories

### Device Interaction
- **Device Session**: start_device_session, stop_device_session, get_session_info, list_device_sessions, switch_device_session
- **Device Actions** (grounded by default): device_tap, device_double_tap, device_long_press, device_type, device_swipe, device_drag
- **Vision**: screenshot, find_element
- **App Management**: install_app, launch_app
- **Diagnostics**: device_doctor

### Test Management
- **Run & Monitor**: run_test, get_test_status, cancel_test
- **CRUD**: create_test, update_test, delete_test, list_tests, list_remote_tests
- **Validation**: validate_yaml, get_schema (YAML format reference)
- **Editor**: open_test_editor (with optional hot reload), stop_hot_reload, hot_reload_status

### Workflow Management
- **Run & Monitor**: run_workflow, cancel_workflow
- **CRUD**: create_workflow, delete_workflow, list_workflows
- **Settings**: get_workflow_settings, set_workflow_location, clear_workflow_location, set_workflow_app, clear_workflow_app
- **Editor**: open_workflow_editor

### Build & App Management
- **Builds**: list_builds, upload_build
- **Apps**: create_app, delete_app

### Modules (Reusable Test Blocks)
- list_modules, get_module, create_module, delete_module, insert_module_block

### Scripts (Code Execution Blocks)
- list_scripts, get_script, create_script, update_script, delete_script, insert_script_block

### Tags & Organization
- list_tags, create_tag, delete_tag, get_test_tags, set_test_tags, add_remove_test_tags

### Environment Variables
- list_env_vars, set_env_var, delete_env_var, clear_env_vars

### System
- auth_status

## Getting Started (Device Interaction)

1. start_device_session(platform="android") -- provisions a cloud device (returns viewer_url and session_index)
2. screenshot() -- see the initial screen state
3. Use device_tap/device_type/device_swipe with target="..." to interact
4. screenshot() after every action to verify
5. stop_device_session() when done to release the device and stop billing

## Getting Started (Test Authoring)

1. get_schema() -- get the YAML format reference
2. create_test(name="...", yaml_content="...") -- create a test
3. validate_yaml(yaml_content="...") -- check syntax before running
4. run_test(test_name="...") -- execute and get results with viewer_url

## Multi-Session Support

You can run multiple devices simultaneously. Each session gets an auto-assigned index (0, 1, 2...).
- list_device_sessions() to see all active sessions
- switch_device_session(index=1) to change the default target
- Pass session_index to any action tool to target a specific session
- stop_device_session(all=true) to stop everything

## Device Tools: Grounded by Default

Device action tools accept EITHER:
  - target (DEFAULT): Describe the element in natural language. Coordinates are auto-resolved via AI vision grounding.
  - x, y: Direct pixel coordinates. For agents with vision or precise control.

Writing good grounding targets (priority order):
  1. Visible text/labels: "the 'Sign In' button", "input box with 'Email'"
  2. Visual characteristics: "blue rounded rectangle", "magnifying glass icon"
  3. Spatial anchors: "text area below the 'Subject:' line"

Avoid abstract UI jargon. Describe what is VISIBLE on screen.

## Swipe Direction Semantics

direction='up' moves the finger UP (scrolls content DOWN to reveal content below).
direction='down' moves the finger DOWN (scrolls content UP to reveal content above).

## Idle Timeout

Sessions auto-terminate after 5 minutes of inactivity. The timer resets on every tool call.
Use get_session_info() to check remaining time.

## Error Recovery

- "no active device session" → Call start_device_session(platform) first
- "could not locate <element>" → Call screenshot() to see the screen, then rephrase the target
- "worker returned 5xx" → Call device_doctor() to diagnose; may need to restart session
- "grounding request failed" → Check network; call device_doctor() for more info

When in doubt, call device_doctor() -- it checks auth, session, worker, grounding, and environment.`,
		},
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
	defer s.Shutdown()
	return s.mcpServer.Run(ctx, &mcp.StdioTransport{})
}

// registerTools registers all Revyl tools with the MCP server.
func (s *Server) registerTools() {
	// --- Device interaction tools ---
	s.registerDeviceTools()

	// run_test tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "run_test",
		Description: "Run a Revyl test by name or ID. Returns test results including pass/fail status and report URL.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Run Test",
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleRunTest)

	// run_workflow tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "run_workflow",
		Description: "Run a Revyl workflow (collection of tests) by name or ID. Returns workflow results including pass/fail counts.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Run Workflow",
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleRunWorkflow)

	// list_tests tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_tests",
		Description: "List available tests from the project's .revyl/config.yaml file.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Tests",
			ReadOnlyHint: true,
		},
	}, s.handleListTests)

	// get_test_status tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_test_status",
		Description: "Get the current status of a running or completed test execution.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Test Status",
			ReadOnlyHint: true,
		},
	}, s.handleGetTestStatus)

	// NEW: create_test tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "create_test",
		Description: `Create a new test from YAML content or just a name.

RECOMMENDED: Before creating a test, read the app's source code (screens, components, routes) to understand the real UI labels, navigation flow, and user-facing outcomes. Use get_schema for the YAML format reference.`,
		Annotations: &mcp.ToolAnnotations{
			Title: "Create Test",
		},
	}, s.handleCreateTest)

	// NEW: create_workflow tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "create_workflow",
		Description: "Create a new workflow (collection of tests).",
		Annotations: &mcp.ToolAnnotations{
			Title: "Create Workflow",
		},
	}, s.handleCreateWorkflow)

	// NEW: validate_yaml tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "validate_yaml",
		Description: "Validate YAML test syntax without creating or running. Returns validation errors/warnings.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Validate YAML",
			ReadOnlyHint: true,
		},
	}, s.handleValidateYAML)

	// NEW: get_schema tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_schema",
		Description: "Get the complete CLI command schema and YAML test schema for LLM reference.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Schema",
			ReadOnlyHint: true,
		},
	}, s.handleGetSchema)

	// NEW: list_builds tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_builds",
		Description: "List available build versions for the project.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Builds",
			ReadOnlyHint: true,
		},
	}, s.handleListBuilds)

	// NEW: open_workflow_editor tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "open_workflow_editor",
		Description: "Get the URL to open a workflow in the browser editor.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Open Workflow Editor",
			ReadOnlyHint: true,
		},
	}, s.handleOpenWorkflowEditor)

	// cancel_test tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "cancel_test",
		Description: "Cancel a running test execution by task ID.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Cancel Test",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleCancelTest)

	// cancel_workflow tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "cancel_workflow",
		Description: "Cancel a running workflow execution by task ID.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Cancel Workflow",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleCancelWorkflow)

	// delete_test tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_test",
		Description: "Delete a test by name (alias from config) or UUID.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete Test",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleDeleteTest)

	// delete_workflow tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_workflow",
		Description: "Delete a workflow by name (alias from config) or UUID.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete Workflow",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleDeleteWorkflow)

	// list_remote_tests tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_remote_tests",
		Description: "List all tests in the organization from the remote API (not just local config).",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Remote Tests",
			ReadOnlyHint: true,
		},
	}, s.handleListRemoteTests)

	// list_workflows tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_workflows",
		Description: "List all workflows in the organization from the remote API.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Workflows",
			ReadOnlyHint: true,
		},
	}, s.handleListWorkflows)

	// auth_status tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "auth_status",
		Description: "Check current authentication status and return user info.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Auth Status",
			ReadOnlyHint: true,
		},
	}, s.handleAuthStatus)

	// create_app tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "create_app",
		Description: "Create a new app for build uploads.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Create App",
		},
	}, s.handleCreateApp)

	// delete_app tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_app",
		Description: "Delete an app and all its build versions by app ID.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete App",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleDeleteApp)

	// --- Module tools ---

	// list_modules tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_modules",
		Description: "List all reusable test modules in the organization. Modules are groups of test blocks that can be imported into any test via module_import blocks.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Modules",
			ReadOnlyHint: true,
		},
	}, s.handleListModules)

	// get_module tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_module",
		Description: "Get details of a specific module by ID, including its blocks.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Module",
			ReadOnlyHint: true,
		},
	}, s.handleGetModule)

	// create_module tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "create_module",
		Description: "Create a new reusable test module from a list of blocks.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Create Module",
		},
	}, s.handleCreateModule)

	// delete_module tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_module",
		Description: "Delete a module by ID. Returns 409 if the module is in use by tests.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete Module",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleDeleteModule)

	// insert_module_block tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "insert_module_block",
		Description: "Given a module name or ID, returns a module_import block YAML snippet ready to insert into a test. Use this to compose tests with reusable modules.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Insert Module Block",
			ReadOnlyHint: true,
		},
	}, s.handleInsertModuleBlock)

	// --- Tag tools ---

	// list_tags tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_tags",
		Description: "List all tags in the organization with test counts. Tags are used to categorize and filter tests.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Tags",
			ReadOnlyHint: true,
		},
	}, s.handleListTags)

	// create_tag tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "create_tag",
		Description: "Create a new tag. If a tag with the same name already exists, the existing tag is returned (upsert behavior).",
		Annotations: &mcp.ToolAnnotations{
			Title: "Create Tag",
		},
	}, s.handleCreateTag)

	// delete_tag tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_tag",
		Description: "Delete a tag by name or ID. This removes it from all tests.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete Tag",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleDeleteTag)

	// get_test_tags tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_test_tags",
		Description: "Get all tags assigned to a specific test.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Test Tags",
			ReadOnlyHint: true,
		},
	}, s.handleGetTestTags)

	// set_test_tags tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "set_test_tags",
		Description: "Replace all tags on a test with the given tag names. Tags are auto-created if they don't exist.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Set Test Tags",
		},
	}, s.handleSetTestTags)

	// --- Env var tools ---

	// list_env_vars tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_env_vars",
		Description: "List all environment variables for a test. Env vars are encrypted at rest and injected at app launch.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Env Vars",
			ReadOnlyHint: true,
		},
	}, s.handleListEnvVars)

	// set_env_var tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "set_env_var",
		Description: "Add or update an environment variable for a test. If the key already exists, its value is updated.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Set Env Var",
		},
	}, s.handleSetEnvVar)

	// delete_env_var tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_env_var",
		Description: "Delete an environment variable from a test by key name.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Delete Env Var",
		},
	}, s.handleDeleteEnvVar)

	// clear_env_vars tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "clear_env_vars",
		Description: "Delete ALL environment variables for a test.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Clear Env Vars",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleClearEnvVars)

	// --- Workflow settings tools ---

	// get_workflow_settings tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_workflow_settings",
		Description: "Get workflow settings including location override and app override configuration.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Workflow Settings",
			ReadOnlyHint: true,
		},
	}, s.handleGetWorkflowSettings)

	// set_workflow_location tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "set_workflow_location",
		Description: "Set a stored GPS location override for all tests in a workflow.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Set Workflow Location",
		},
	}, s.handleSetWorkflowLocation)

	// clear_workflow_location tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "clear_workflow_location",
		Description: "Remove the stored GPS location override from a workflow.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Clear Workflow Location",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleClearWorkflowLocation)

	// set_workflow_app tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "set_workflow_app",
		Description: "Set stored app overrides (per platform) for all tests in a workflow. App IDs are validated.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Set Workflow App",
		},
	}, s.handleSetWorkflowApp)

	// clear_workflow_app tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "clear_workflow_app",
		Description: "Remove stored app overrides from a workflow.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Clear Workflow App",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleClearWorkflowApp)

	// add_remove_test_tags tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "add_remove_test_tags",
		Description: "Add and/or remove tags on a test without replacing all existing tags.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Add/Remove Test Tags",
		},
	}, s.handleAddRemoveTestTags)

	// --- Build tools ---

	// upload_build tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "upload_build",
		Description: "Upload a local build file (.apk, .ipa, or .zip) to an existing app. Returns the new version ID.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Upload Build",
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleUploadBuild)

	// --- Test update tools ---

	// update_test tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "update_test",
		Description: `Update an existing test's YAML content (blocks). Pushes new blocks to the remote test.

Use get_schema for the YAML format reference. The YAML must include the full test definition with metadata, build, and blocks sections.`,
		Annotations: &mcp.ToolAnnotations{
			Title: "Update Test",
		},
	}, s.handleUpdateTest)

	// --- Script tools ---

	// list_scripts tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_scripts",
		Description: "List all code execution scripts in the organization. Scripts contain reusable code that runs in sandboxed environments during test execution.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Scripts",
			ReadOnlyHint: true,
		},
	}, s.handleListScripts)

	// get_script tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_script",
		Description: "Get details of a specific script by ID, including its source code.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Script",
			ReadOnlyHint: true,
		},
	}, s.handleGetScript)

	// create_script tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "create_script",
		Description: "Create a new code execution script. Scripts can be referenced in tests via code_execution blocks.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Create Script",
		},
	}, s.handleCreateScript)

	// update_script tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "update_script",
		Description: "Update an existing script's name, code, runtime, or description.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Update Script",
		},
	}, s.handleUpdateScript)

	// delete_script tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_script",
		Description: "Delete a script by ID.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete Script",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleDeleteScript)

	// insert_script_block tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "insert_script_block",
		Description: "Given a script name or ID, returns a code_execution block YAML snippet ready to insert into a test.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Insert Script Block",
			ReadOnlyHint: true,
		},
	}, s.handleInsertScriptBlock)

	// --- Live editor tools ---

	// open_test_editor tool (with optional hot reload)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "open_test_editor",
		Description: "Open a test in the browser editor, optionally with hot reload. Starts dev server and tunnel if hot reload is configured. Opens the browser by default.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Open Test Editor",
			ReadOnlyHint: true,
		},
	}, s.handleOpenTestEditor)

	// stop_hot_reload tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "stop_hot_reload",
		Description: "Stop the hot reload session (dev server and tunnel). Call this when done with live editing.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Stop Hot Reload",
		},
	}, s.handleStopHotReload)

	// hot_reload_status tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "hot_reload_status",
		Description: "Check if a hot reload session is active and get current URLs.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Hot Reload Status",
			ReadOnlyHint: true,
		},
	}, s.handleHotReloadStatus)
}

// RunTestInput defines the input parameters for the run_test tool.
type RunTestInput struct {
	TestName       string `json:"test_name" jsonschema:"Test name (alias from .revyl/config.yaml) or UUID"`
	Retries        int    `json:"retries,omitempty" jsonschema:"Number of retry attempts (1-5)"`
	BuildVersionID string `json:"build_version_id,omitempty" jsonschema:"Specific build version ID to test against"`
	Location       string `json:"location,omitempty" jsonschema:"Override GPS location as lat,lng (e.g. 37.7749,-122.4194)"`
}

// RunTestOutput defines the output for the run_test tool.
type RunTestOutput struct {
	Success        bool       `json:"success"`
	TaskID         string     `json:"task_id"`
	TestID         string     `json:"test_id"`
	TestName       string     `json:"test_name"`
	Status         string     `json:"status"`
	Duration       string     `json:"duration"`
	ReportURL      string     `json:"report_url"`
	ViewerURL      string     `json:"viewer_url,omitempty"`
	CompletedSteps int        `json:"completed_steps,omitempty"`
	TotalSteps     int        `json:"total_steps,omitempty"`
	LastStep       string     `json:"last_step,omitempty"`
	ErrorMessage   string     `json:"error_message,omitempty"`
	NextSteps      []NextStep `json:"next_steps,omitempty"`
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

	// Track last progress for enriching final output
	var lastStatus *sse.TestStatus

	// Build progress callback: sends MCP progress notifications if the client
	// provided a progressToken, and always captures the latest status for the
	// enriched final output.
	var onProgress func(status *sse.TestStatus)
	progressToken := req.Params.GetProgressToken()
	onProgress = func(status *sse.TestStatus) {
		lastStatus = status
		if progressToken != nil {
			msg := fmt.Sprintf("[%s] %s", status.Status, status.CurrentStep)
			if status.TotalSteps > 0 {
				msg = fmt.Sprintf("[%s] Step %d/%d: %s",
					status.Status, status.CompletedSteps, status.TotalSteps, status.CurrentStep)
			}
			if status.Duration != "" {
				msg += fmt.Sprintf(" (%s)", status.Duration)
			}
			_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
				ProgressToken: progressToken,
				Message:       msg,
				Progress:      float64(status.CompletedSteps),
				Total:         float64(status.TotalSteps),
			})
		}
	}

	// Parse location if provided
	params := execution.RunTestParams{
		TestNameOrID:   input.TestName,
		Retries:        retries,
		BuildVersionID: input.BuildVersionID,
		Timeout:        3600,
		DevMode:        s.devMode,
		OnProgress:     onProgress,
	}
	if input.Location != "" {
		lat, lng, locErr := parseLocationString(input.Location)
		if locErr != nil {
			return nil, RunTestOutput{Success: false, ErrorMessage: locErr.Error()}, nil
		}
		params.Latitude = lat
		params.Longitude = lng
		params.HasLocation = true
	}

	// Use shared execution logic
	result, err := execution.RunTest(ctx, s.apiClient.GetAPIKey(), s.config, params)
	if err != nil {
		return nil, RunTestOutput{Success: false, ErrorMessage: err.Error()}, nil
	}

	// Build viewer URL for watching execution live in the browser
	viewerURL := fmt.Sprintf("%s/tests/execute?workflowRunId=%s", config.GetAppURL(s.devMode), result.TaskID)

	out := RunTestOutput{
		Success:      result.Success,
		TaskID:       result.TaskID,
		TestID:       result.TestID,
		TestName:     result.TestName,
		Status:       result.Status,
		Duration:     result.Duration,
		ReportURL:    result.ReportURL,
		ViewerURL:    viewerURL,
		ErrorMessage: result.ErrorMessage,
	}
	if lastStatus != nil {
		out.CompletedSteps = lastStatus.CompletedSteps
		out.TotalSteps = lastStatus.TotalSteps
		out.LastStep = lastStatus.CurrentStep
	}

	// Populate next steps based on outcome to guide the agent.
	switch {
	case result.Success:
		out.NextSteps = []NextStep{
			{Tool: "open_test_editor", Reason: "View detailed test report in browser"},
			{Tool: "get_test_status", Params: fmt.Sprintf("task_id=%s", result.TaskID), Reason: "Get step-by-step execution details"},
		}
	case result.Status == "cancelled" || result.Status == "timeout":
		out.NextSteps = []NextStep{
			{Tool: "run_test", Params: fmt.Sprintf("test_name=%s", input.TestName), Reason: "Retry the test"},
			{Tool: "get_test_status", Params: fmt.Sprintf("task_id=%s", result.TaskID), Reason: "Check what happened before cancellation"},
		}
	default: // failed
		out.NextSteps = []NextStep{
			{Tool: "get_test_status", Params: fmt.Sprintf("task_id=%s", result.TaskID), Reason: "Get step-by-step failure details"},
			{Tool: "run_test", Params: fmt.Sprintf("test_name=%s", input.TestName), Reason: "Retry the test after fixing the issue"},
		}
	}

	return nil, out, nil
}

// RunWorkflowInput defines the input parameters for the run_workflow tool.
type RunWorkflowInput struct {
	WorkflowName string `json:"workflow_name" jsonschema:"Workflow name (alias from .revyl/config.yaml) or UUID"`
	Retries      int    `json:"retries,omitempty" jsonschema:"Number of retry attempts (1-5)"`
	IOSAppID     string `json:"ios_app_id,omitempty" jsonschema:"Override iOS app ID for all tests in workflow"`
	AndroidAppID string `json:"android_app_id,omitempty" jsonschema:"Override Android app ID for all tests in workflow"`
	Location     string `json:"location,omitempty" jsonschema:"Override GPS location as lat,lng (e.g. 37.7749,-122.4194)"`
}

// RunWorkflowOutput defines the output for the run_workflow tool.
type RunWorkflowOutput struct {
	Success        bool       `json:"success"`
	TaskID         string     `json:"task_id"`
	WorkflowID     string     `json:"workflow_id"`
	Status         string     `json:"status"`
	TotalTests     int        `json:"total_tests"`
	PassedTests    int        `json:"passed_tests"`
	FailedTests    int        `json:"failed_tests"`
	Duration       string     `json:"duration"`
	ReportURL      string     `json:"report_url"`
	ViewerURL      string     `json:"viewer_url,omitempty"`
	CompletedTests int        `json:"completed_tests,omitempty"`
	CurrentTest    string     `json:"current_test,omitempty"`
	ErrorMessage   string     `json:"error_message,omitempty"`
	NextSteps      []NextStep `json:"next_steps,omitempty"`
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

	// Track last progress for enriching final output
	var lastStatus *sse.WorkflowStatus

	// Build progress callback: sends MCP progress notifications if the client
	// provided a progressToken, and always captures the latest status.
	var onProgress func(status *sse.WorkflowStatus)
	progressToken := req.Params.GetProgressToken()
	onProgress = func(status *sse.WorkflowStatus) {
		lastStatus = status
		if progressToken != nil {
			msg := fmt.Sprintf("[%s] %d/%d tests completed (%d passed, %d failed)",
				status.Status, status.CompletedTests, status.TotalTests, status.PassedTests, status.FailedTests)
			if status.Duration != "" {
				msg += fmt.Sprintf(" (%s)", status.Duration)
			}
			_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
				ProgressToken: progressToken,
				Message:       msg,
				Progress:      float64(status.CompletedTests),
				Total:         float64(status.TotalTests),
			})
		}
	}

	// Build params with optional overrides
	wfParams := execution.RunWorkflowParams{
		WorkflowNameOrID: input.WorkflowName,
		Retries:          retries,
		Timeout:          3600,
		DevMode:          s.devMode,
		OnProgress:       onProgress,
		IOSAppID:         input.IOSAppID,
		AndroidAppID:     input.AndroidAppID,
	}
	if input.Location != "" {
		lat, lng, locErr := parseLocationString(input.Location)
		if locErr != nil {
			return nil, RunWorkflowOutput{Success: false, ErrorMessage: locErr.Error()}, nil
		}
		wfParams.Latitude = lat
		wfParams.Longitude = lng
		wfParams.HasLocation = true
	}

	// Use shared execution logic
	result, err := execution.RunWorkflow(ctx, s.apiClient.GetAPIKey(), s.config, wfParams)
	if err != nil {
		return nil, RunWorkflowOutput{Success: false, ErrorMessage: err.Error()}, nil
	}

	// Build viewer URL for watching execution live in the browser
	viewerURL := fmt.Sprintf("%s/workflows/report?taskId=%s", config.GetAppURL(s.devMode), result.TaskID)

	out := RunWorkflowOutput{
		Success:      result.Success,
		TaskID:       result.TaskID,
		WorkflowID:   result.WorkflowID,
		Status:       result.Status,
		TotalTests:   result.TotalTests,
		PassedTests:  result.PassedTests,
		FailedTests:  result.FailedTests,
		Duration:     result.Duration,
		ReportURL:    result.ReportURL,
		ViewerURL:    viewerURL,
		ErrorMessage: result.ErrorMessage,
	}
	if lastStatus != nil {
		out.CompletedTests = lastStatus.CompletedTests
		out.CurrentTest = lastStatus.WorkflowName
	}

	// Populate next steps based on outcome to guide the agent.
	switch {
	case result.Success:
		out.NextSteps = []NextStep{
			{Tool: "open_workflow_editor", Reason: "View detailed workflow report in browser"},
		}
	case result.Status == "cancelled" || result.Status == "timeout":
		out.NextSteps = []NextStep{
			{Tool: "run_workflow", Params: fmt.Sprintf("workflow_name=%s", input.WorkflowName), Reason: "Retry the workflow"},
		}
	default: // failed or partial failures
		out.NextSteps = []NextStep{
			{Tool: "run_workflow", Params: fmt.Sprintf("workflow_name=%s", input.WorkflowName), Reason: "Retry the workflow after investigating failures"},
		}
		if result.FailedTests > 0 {
			out.NextSteps = append([]NextStep{
				{Tool: "open_workflow_editor", Reason: fmt.Sprintf("View details for %d failed test(s)", result.FailedTests)},
			}, out.NextSteps...)
		}
	}

	return nil, out, nil
}

// ListTestsInput defines the input parameters for the list_tests tool.
type ListTestsInput struct {
	ProjectDir string `json:"project_dir,omitempty" jsonschema:"Path to project directory (defaults to current directory)"`
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
	TaskID string `json:"task_id" jsonschema:"The task ID of the test execution"`
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
	Name        string `json:"name" jsonschema:"Test name"`
	Platform    string `json:"platform" jsonschema:"Target platform (ios or android)"`
	YAMLContent string `json:"yaml_content,omitempty" jsonschema:"Optional YAML test definition. If provided, creates test with these blocks."`
	AppID       string `json:"app_id,omitempty" jsonschema:"App ID to associate with test"`
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
	Name    string   `json:"name" jsonschema:"Workflow name"`
	TestIDs []string `json:"test_ids,omitempty" jsonschema:"Optional test IDs to include in workflow"`
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
	Content string `json:"content" jsonschema:"YAML test content to validate"`
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
	Format string `json:"format,omitempty" jsonschema:"Output format: json (default), markdown, or llm"`
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
	Platform string `json:"platform,omitempty" jsonschema:"Filter by platform (ios or android)"`
	Limit    int    `json:"limit,omitempty" jsonschema:"Maximum number of builds to return (default 20)"`
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

// OpenWorkflowEditorInput defines input for open_workflow_editor tool.
type OpenWorkflowEditorInput struct {
	WorkflowNameOrID string `json:"workflow_name_or_id" jsonschema:"Workflow name (from config) or UUID"`
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
	TaskID string `json:"task_id" jsonschema:"The task ID of the running test execution to cancel"`
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
	TaskID string `json:"task_id" jsonschema:"The task ID of the running workflow execution to cancel"`
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
	TestNameOrID string `json:"test_name_or_id" jsonschema:"Test name (alias from config) or UUID"`
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
	WorkflowNameOrID string `json:"workflow_name_or_id" jsonschema:"Workflow name (alias from config) or UUID"`
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
	Limit  int `json:"limit,omitempty" jsonschema:"Maximum number of tests to return (default 50)"`
	Offset int `json:"offset,omitempty" jsonschema:"Offset for pagination (default 0)"`
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
	Name     string `json:"name" jsonschema:"App name"`
	Platform string `json:"platform" jsonschema:"Target platform (ios or android)"`
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
	AppID string `json:"app_id" jsonschema:"The UUID of the app to delete"`
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
	NameFilter string `json:"name_filter,omitempty" jsonschema:"Optional filter to search modules by name"`
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
	ModuleID string `json:"module_id" jsonschema:"The UUID of the module to retrieve"`
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
	Name        string        `json:"name" jsonschema:"Module name"`
	Description string        `json:"description,omitempty" jsonschema:"Optional module description"`
	Blocks      []interface{} `json:"blocks" jsonschema:"Array of test block objects"`
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
	ModuleID string `json:"module_id" jsonschema:"The UUID of the module to delete"`
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
	ModuleNameOrID string `json:"module_name_or_id" jsonschema:"Module name or UUID to generate the import block for"`
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
	NameFilter string `json:"name_filter,omitempty" jsonschema:"Optional filter to search tags by name"`
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
	Name  string `json:"name" jsonschema:"Tag name"`
	Color string `json:"color,omitempty" jsonschema:"Tag color as hex string (e.g. #22C55E)"`
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
	TagNameOrID string `json:"tag_name_or_id" jsonschema:"Tag name or UUID to delete"`
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
	TestNameOrID string `json:"test_name_or_id" jsonschema:"Test name (from config) or UUID"`
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
	TestNameOrID string   `json:"test_name_or_id" jsonschema:"Test name (from config) or UUID"`
	TagNames     []string `json:"tag_names" jsonschema:"Tag names to set on the test (replaces all existing tags)"`
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
	TestNameOrID string   `json:"test_name_or_id" jsonschema:"Test name (from config) or UUID"`
	TagsToAdd    []string `json:"tags_to_add,omitempty" jsonschema:"Tag names to add to the test"`
	TagsToRemove []string `json:"tags_to_remove,omitempty" jsonschema:"Tag names to remove from the test"`
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

// --- Helper: parse location string ---

// parseLocationString parses a "lat,lng" string into coordinates.
func parseLocationString(s string) (float64, float64, error) {
	parts := strings.SplitN(s, ",", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid location format: expected lat,lng (e.g. 37.7749,-122.4194)")
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
		return 0, 0, fmt.Errorf("latitude must be between -90 and 90 (got %v)", lat)
	}
	if lng < -180 || lng > 180 {
		return 0, 0, fmt.Errorf("longitude must be between -180 and 180 (got %v)", lng)
	}
	return lat, lng, nil
}

// resolveTestID resolves a test name or ID to a UUID using config aliases and API search.
func (s *Server) resolveTestID(ctx context.Context, nameOrID string) (string, error) {
	testID := nameOrID
	if s.config != nil {
		if id, ok := s.config.Tests[nameOrID]; ok {
			testID = id
		}
	}
	if len(testID) != 36 {
		testsResp, err := s.apiClient.ListOrgTests(ctx, 100, 0)
		if err != nil {
			return "", fmt.Errorf("failed to search for test '%s': %w", nameOrID, err)
		}
		for _, t := range testsResp.Tests {
			if t.Name == nameOrID {
				return t.ID, nil
			}
		}
		return "", fmt.Errorf("test '%s' not found", nameOrID)
	}
	return testID, nil
}

// resolveWorkflowID resolves a workflow name or ID to a UUID using config aliases and API search.
func (s *Server) resolveWorkflowID(ctx context.Context, nameOrID string) (string, error) {
	wfID := nameOrID
	if s.config != nil {
		if id, ok := s.config.Workflows[nameOrID]; ok {
			wfID = id
		}
	}
	if len(wfID) != 36 {
		resp, err := s.apiClient.ListWorkflows(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to search for workflow '%s': %w", nameOrID, err)
		}
		for _, w := range resp.Workflows {
			if w.Name == nameOrID {
				return w.ID, nil
			}
		}
		return "", fmt.Errorf("workflow '%s' not found", nameOrID)
	}
	return wfID, nil
}

// --- Env var tool handlers ---

// ListEnvVarsInput defines input for list_env_vars tool.
type ListEnvVarsInput struct {
	TestNameOrID string `json:"test_name_or_id" jsonschema:"Test name (from config) or UUID"`
}

// EnvVarInfo contains information about an env var for MCP output.
type EnvVarInfo struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// ListEnvVarsOutput defines output for list_env_vars tool.
type ListEnvVarsOutput struct {
	Success bool         `json:"success"`
	TestID  string       `json:"test_id,omitempty"`
	EnvVars []EnvVarInfo `json:"env_vars,omitempty"`
	Error   string       `json:"error,omitempty"`
}

func (s *Server) handleListEnvVars(ctx context.Context, req *mcp.CallToolRequest, input ListEnvVarsInput) (*mcp.CallToolResult, ListEnvVarsOutput, error) {
	if input.TestNameOrID == "" {
		return nil, ListEnvVarsOutput{Success: false, Error: "test_name_or_id is required"}, nil
	}

	testID, err := s.resolveTestID(ctx, input.TestNameOrID)
	if err != nil {
		return nil, ListEnvVarsOutput{Success: false, Error: err.Error()}, nil
	}

	resp, err := s.apiClient.ListEnvVars(ctx, testID)
	if err != nil {
		return nil, ListEnvVarsOutput{Success: false, Error: fmt.Sprintf("failed to list env vars: %v", err)}, nil
	}

	var envVars []EnvVarInfo
	for _, ev := range resp.Result {
		updated := ev.UpdatedAt
		if updated == "" {
			updated = ev.CreatedAt
		}
		envVars = append(envVars, EnvVarInfo{Key: ev.Key, Value: ev.Value, UpdatedAt: updated})
	}
	if envVars == nil {
		envVars = []EnvVarInfo{}
	}

	return nil, ListEnvVarsOutput{Success: true, TestID: testID, EnvVars: envVars}, nil
}

// SetEnvVarInput defines input for set_env_var tool.
type SetEnvVarInput struct {
	TestNameOrID string `json:"test_name_or_id" jsonschema:"Test name (from config) or UUID"`
	Key          string `json:"key" jsonschema:"Environment variable key"`
	Value        string `json:"value" jsonschema:"Environment variable value"`
}

// SetEnvVarOutput defines output for set_env_var tool.
type SetEnvVarOutput struct {
	Success bool   `json:"success"`
	Action  string `json:"action,omitempty"` // "added" or "updated"
	Key     string `json:"key,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleSetEnvVar(ctx context.Context, req *mcp.CallToolRequest, input SetEnvVarInput) (*mcp.CallToolResult, SetEnvVarOutput, error) {
	if input.TestNameOrID == "" || input.Key == "" {
		return nil, SetEnvVarOutput{Success: false, Error: "test_name_or_id and key are required"}, nil
	}

	testID, err := s.resolveTestID(ctx, input.TestNameOrID)
	if err != nil {
		return nil, SetEnvVarOutput{Success: false, Error: err.Error()}, nil
	}

	// Check if key already exists (upsert)
	existing, err := s.apiClient.ListEnvVars(ctx, testID)
	if err != nil {
		return nil, SetEnvVarOutput{Success: false, Error: fmt.Sprintf("failed to check existing env vars: %v", err)}, nil
	}

	var existingVar *api.EnvVar
	for _, ev := range existing.Result {
		if ev.Key == input.Key {
			existingVar = &ev
			break
		}
	}

	if existingVar != nil {
		_, err = s.apiClient.UpdateEnvVar(ctx, existingVar.ID, input.Key, input.Value)
		if err != nil {
			return nil, SetEnvVarOutput{Success: false, Error: fmt.Sprintf("failed to update env var: %v", err)}, nil
		}
		return nil, SetEnvVarOutput{Success: true, Action: "updated", Key: input.Key}, nil
	}

	_, err = s.apiClient.AddEnvVar(ctx, testID, input.Key, input.Value)
	if err != nil {
		return nil, SetEnvVarOutput{Success: false, Error: fmt.Sprintf("failed to add env var: %v", err)}, nil
	}
	return nil, SetEnvVarOutput{Success: true, Action: "added", Key: input.Key}, nil
}

// DeleteEnvVarInput defines input for delete_env_var tool.
type DeleteEnvVarInput struct {
	TestNameOrID string `json:"test_name_or_id" jsonschema:"Test name (from config) or UUID"`
	Key          string `json:"key" jsonschema:"Environment variable key to delete"`
}

// DeleteEnvVarOutput defines output for delete_env_var tool.
type DeleteEnvVarOutput struct {
	Success bool   `json:"success"`
	Key     string `json:"key,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleDeleteEnvVar(ctx context.Context, req *mcp.CallToolRequest, input DeleteEnvVarInput) (*mcp.CallToolResult, DeleteEnvVarOutput, error) {
	if input.TestNameOrID == "" || input.Key == "" {
		return nil, DeleteEnvVarOutput{Success: false, Error: "test_name_or_id and key are required"}, nil
	}

	testID, err := s.resolveTestID(ctx, input.TestNameOrID)
	if err != nil {
		return nil, DeleteEnvVarOutput{Success: false, Error: err.Error()}, nil
	}

	existing, err := s.apiClient.ListEnvVars(ctx, testID)
	if err != nil {
		return nil, DeleteEnvVarOutput{Success: false, Error: fmt.Sprintf("failed to list env vars: %v", err)}, nil
	}

	var found *api.EnvVar
	for _, ev := range existing.Result {
		if ev.Key == input.Key {
			found = &ev
			break
		}
	}

	if found == nil {
		return nil, DeleteEnvVarOutput{Success: false, Error: fmt.Sprintf("env var '%s' not found", input.Key)}, nil
	}

	err = s.apiClient.DeleteEnvVar(ctx, found.ID)
	if err != nil {
		return nil, DeleteEnvVarOutput{Success: false, Error: fmt.Sprintf("failed to delete env var: %v", err)}, nil
	}

	return nil, DeleteEnvVarOutput{Success: true, Key: input.Key}, nil
}

// ClearEnvVarsInput defines input for clear_env_vars tool.
type ClearEnvVarsInput struct {
	TestNameOrID string `json:"test_name_or_id" jsonschema:"Test name (from config) or UUID"`
}

// ClearEnvVarsOutput defines output for clear_env_vars tool.
type ClearEnvVarsOutput struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleClearEnvVars(ctx context.Context, req *mcp.CallToolRequest, input ClearEnvVarsInput) (*mcp.CallToolResult, ClearEnvVarsOutput, error) {
	if input.TestNameOrID == "" {
		return nil, ClearEnvVarsOutput{Success: false, Error: "test_name_or_id is required"}, nil
	}

	testID, err := s.resolveTestID(ctx, input.TestNameOrID)
	if err != nil {
		return nil, ClearEnvVarsOutput{Success: false, Error: err.Error()}, nil
	}

	err = s.apiClient.DeleteAllEnvVars(ctx, testID)
	if err != nil {
		return nil, ClearEnvVarsOutput{Success: false, Error: fmt.Sprintf("failed to clear env vars: %v", err)}, nil
	}

	return nil, ClearEnvVarsOutput{Success: true}, nil
}

// --- Workflow settings tool handlers ---

// GetWorkflowSettingsInput defines input for get_workflow_settings tool.
type GetWorkflowSettingsInput struct {
	WorkflowNameOrID string `json:"workflow_name_or_id" jsonschema:"Workflow name (from config) or UUID"`
}

// WorkflowSettingsOutput defines output for get_workflow_settings tool.
type WorkflowSettingsOutput struct {
	Success             bool                   `json:"success"`
	WorkflowID          string                 `json:"workflow_id,omitempty"`
	OverrideLocation    bool                   `json:"override_location"`
	LocationConfig      map[string]interface{} `json:"location_config,omitempty"`
	OverrideBuildConfig bool                   `json:"override_build_config"`
	BuildConfig         map[string]interface{} `json:"build_config,omitempty"`
	Error               string                 `json:"error,omitempty"`
}

func (s *Server) handleGetWorkflowSettings(ctx context.Context, req *mcp.CallToolRequest, input GetWorkflowSettingsInput) (*mcp.CallToolResult, WorkflowSettingsOutput, error) {
	if input.WorkflowNameOrID == "" {
		return nil, WorkflowSettingsOutput{Success: false, Error: "workflow_name_or_id is required"}, nil
	}

	wfID, err := s.resolveWorkflowID(ctx, input.WorkflowNameOrID)
	if err != nil {
		return nil, WorkflowSettingsOutput{Success: false, Error: err.Error()}, nil
	}

	wf, err := s.apiClient.GetWorkflow(ctx, wfID)
	if err != nil {
		return nil, WorkflowSettingsOutput{Success: false, Error: fmt.Sprintf("failed to get workflow: %v", err)}, nil
	}

	return nil, WorkflowSettingsOutput{
		Success:             true,
		WorkflowID:          wfID,
		OverrideLocation:    wf.OverrideLocation,
		LocationConfig:      wf.LocationConfig,
		OverrideBuildConfig: wf.OverrideBuildConfig,
		BuildConfig:         wf.BuildConfig,
	}, nil
}

// SetWorkflowLocationInput defines input for set_workflow_location tool.
type SetWorkflowLocationInput struct {
	WorkflowNameOrID string  `json:"workflow_name_or_id" jsonschema:"Workflow name (from config) or UUID"`
	Latitude         float64 `json:"latitude" jsonschema:"Latitude (-90 to 90)"`
	Longitude        float64 `json:"longitude" jsonschema:"Longitude (-180 to 180)"`
}

// SimpleSuccessOutput is a generic success/error output.
type SimpleSuccessOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleSetWorkflowLocation(ctx context.Context, req *mcp.CallToolRequest, input SetWorkflowLocationInput) (*mcp.CallToolResult, SimpleSuccessOutput, error) {
	if input.WorkflowNameOrID == "" {
		return nil, SimpleSuccessOutput{Success: false, Error: "workflow_name_or_id is required"}, nil
	}
	if input.Latitude < -90 || input.Latitude > 90 {
		return nil, SimpleSuccessOutput{Success: false, Error: "latitude must be between -90 and 90"}, nil
	}
	if input.Longitude < -180 || input.Longitude > 180 {
		return nil, SimpleSuccessOutput{Success: false, Error: "longitude must be between -180 and 180"}, nil
	}

	wfID, err := s.resolveWorkflowID(ctx, input.WorkflowNameOrID)
	if err != nil {
		return nil, SimpleSuccessOutput{Success: false, Error: err.Error()}, nil
	}

	locationConfig := map[string]interface{}{
		"latitude":  input.Latitude,
		"longitude": input.Longitude,
	}
	err = s.apiClient.UpdateWorkflowLocationConfig(ctx, wfID, locationConfig, true)
	if err != nil {
		return nil, SimpleSuccessOutput{Success: false, Error: fmt.Sprintf("failed to set location: %v", err)}, nil
	}

	return nil, SimpleSuccessOutput{
		Success: true,
		Message: fmt.Sprintf("Location set to %.6f, %.6f with override enabled", input.Latitude, input.Longitude),
	}, nil
}

// ClearWorkflowLocationInput defines input for clear_workflow_location tool.
type ClearWorkflowLocationInput struct {
	WorkflowNameOrID string `json:"workflow_name_or_id" jsonschema:"Workflow name (from config) or UUID"`
}

func (s *Server) handleClearWorkflowLocation(ctx context.Context, req *mcp.CallToolRequest, input ClearWorkflowLocationInput) (*mcp.CallToolResult, SimpleSuccessOutput, error) {
	if input.WorkflowNameOrID == "" {
		return nil, SimpleSuccessOutput{Success: false, Error: "workflow_name_or_id is required"}, nil
	}

	wfID, err := s.resolveWorkflowID(ctx, input.WorkflowNameOrID)
	if err != nil {
		return nil, SimpleSuccessOutput{Success: false, Error: err.Error()}, nil
	}

	err = s.apiClient.UpdateWorkflowLocationConfig(ctx, wfID, nil, false)
	if err != nil {
		return nil, SimpleSuccessOutput{Success: false, Error: fmt.Sprintf("failed to clear location: %v", err)}, nil
	}

	return nil, SimpleSuccessOutput{Success: true, Message: "Location override cleared"}, nil
}

// SetWorkflowAppInput defines input for set_workflow_app tool.
type SetWorkflowAppInput struct {
	WorkflowNameOrID string `json:"workflow_name_or_id" jsonschema:"Workflow name (from config) or UUID"`
	IOSAppID         string `json:"ios_app_id,omitempty" jsonschema:"iOS app ID to override"`
	AndroidAppID     string `json:"android_app_id,omitempty" jsonschema:"Android app ID to override"`
}

func (s *Server) handleSetWorkflowApp(ctx context.Context, req *mcp.CallToolRequest, input SetWorkflowAppInput) (*mcp.CallToolResult, SimpleSuccessOutput, error) {
	if input.WorkflowNameOrID == "" {
		return nil, SimpleSuccessOutput{Success: false, Error: "workflow_name_or_id is required"}, nil
	}
	if input.IOSAppID == "" && input.AndroidAppID == "" {
		return nil, SimpleSuccessOutput{Success: false, Error: "at least one of ios_app_id or android_app_id is required"}, nil
	}

	wfID, err := s.resolveWorkflowID(ctx, input.WorkflowNameOrID)
	if err != nil {
		return nil, SimpleSuccessOutput{Success: false, Error: err.Error()}, nil
	}

	// Validate app IDs exist
	if input.IOSAppID != "" {
		_, err := s.apiClient.GetApp(ctx, input.IOSAppID)
		if err != nil {
			return nil, SimpleSuccessOutput{Success: false, Error: fmt.Sprintf("iOS app '%s' not found", input.IOSAppID)}, nil
		}
	}
	if input.AndroidAppID != "" {
		_, err := s.apiClient.GetApp(ctx, input.AndroidAppID)
		if err != nil {
			return nil, SimpleSuccessOutput{Success: false, Error: fmt.Sprintf("Android app '%s' not found", input.AndroidAppID)}, nil
		}
	}

	// Fetch existing config to merge (don't clobber the other platform)
	buildConfig := map[string]interface{}{}
	wf, wfErr := s.apiClient.GetWorkflow(ctx, wfID)
	if wfErr == nil && wf.BuildConfig != nil {
		buildConfig = wf.BuildConfig
	}
	if input.IOSAppID != "" {
		buildConfig["ios_build"] = map[string]interface{}{"app_id": input.IOSAppID}
	}
	if input.AndroidAppID != "" {
		buildConfig["android_build"] = map[string]interface{}{"app_id": input.AndroidAppID}
	}

	err = s.apiClient.UpdateWorkflowBuildConfig(ctx, wfID, buildConfig, true)
	if err != nil {
		return nil, SimpleSuccessOutput{Success: false, Error: fmt.Sprintf("failed to set app config: %v", err)}, nil
	}

	return nil, SimpleSuccessOutput{Success: true, Message: "App config set with override enabled"}, nil
}

// ClearWorkflowAppInput defines input for clear_workflow_app tool.
type ClearWorkflowAppInput struct {
	WorkflowNameOrID string `json:"workflow_name_or_id" jsonschema:"Workflow name (from config) or UUID"`
}

func (s *Server) handleClearWorkflowApp(ctx context.Context, req *mcp.CallToolRequest, input ClearWorkflowAppInput) (*mcp.CallToolResult, SimpleSuccessOutput, error) {
	if input.WorkflowNameOrID == "" {
		return nil, SimpleSuccessOutput{Success: false, Error: "workflow_name_or_id is required"}, nil
	}

	wfID, err := s.resolveWorkflowID(ctx, input.WorkflowNameOrID)
	if err != nil {
		return nil, SimpleSuccessOutput{Success: false, Error: err.Error()}, nil
	}

	err = s.apiClient.UpdateWorkflowBuildConfig(ctx, wfID, nil, false)
	if err != nil {
		return nil, SimpleSuccessOutput{Success: false, Error: fmt.Sprintf("failed to clear app config: %v", err)}, nil
	}

	return nil, SimpleSuccessOutput{Success: true, Message: "App override cleared"}, nil
}

// --- Build upload tool handler ---

// UploadBuildInput defines input for upload_build tool.
type UploadBuildInput struct {
	FilePath string `json:"file_path" jsonschema:"Absolute path to the build file (.apk, .ipa, or .zip)"`
	AppID    string `json:"app_id" jsonschema:"App ID to upload the build to"`
	Version  string `json:"version,omitempty" jsonschema:"Version string (auto-generated from timestamp if not provided)"`
}

// UploadBuildOutput defines output for upload_build tool.
type UploadBuildOutput struct {
	Success   bool   `json:"success"`
	VersionID string `json:"version_id,omitempty"`
	Version   string `json:"version,omitempty"`
	PackageID string `json:"package_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

// handleUploadBuild handles the upload_build tool call.
func (s *Server) handleUploadBuild(ctx context.Context, req *mcp.CallToolRequest, input UploadBuildInput) (*mcp.CallToolResult, UploadBuildOutput, error) {
	if input.FilePath == "" {
		return nil, UploadBuildOutput{Success: false, Error: "file_path is required"}, nil
	}
	if input.AppID == "" {
		return nil, UploadBuildOutput{Success: false, Error: "app_id is required"}, nil
	}

	// Validate file exists
	info, err := os.Stat(input.FilePath)
	if err != nil {
		return nil, UploadBuildOutput{Success: false, Error: fmt.Sprintf("file not found: %v", err)}, nil
	}
	if info.IsDir() {
		return nil, UploadBuildOutput{Success: false, Error: "file_path must be a file, not a directory"}, nil
	}

	// Validate file extension
	ext := strings.ToLower(filepath.Ext(input.FilePath))
	validExts := map[string]bool{".apk": true, ".ipa": true, ".zip": true, ".app": true}
	if !validExts[ext] {
		return nil, UploadBuildOutput{Success: false, Error: fmt.Sprintf("invalid file type '%s': must be .apk, .ipa, .zip, or .app", ext)}, nil
	}

	// Auto-generate version if not provided
	version := input.Version
	if version == "" {
		version = fmt.Sprintf("mcp-%d", time.Now().Unix())
	}

	resp, err := s.apiClient.UploadBuild(ctx, &api.UploadBuildRequest{
		AppID:    input.AppID,
		Version:  version,
		FilePath: input.FilePath,
	})
	if err != nil {
		return nil, UploadBuildOutput{Success: false, Error: fmt.Sprintf("upload failed: %v", err)}, nil
	}

	return nil, UploadBuildOutput{
		Success:   true,
		VersionID: resp.VersionID,
		Version:   resp.Version,
		PackageID: resp.PackageID,
	}, nil
}

// --- Test update tool handler ---

// UpdateTestInput defines input for update_test tool.
type UpdateTestInput struct {
	TestNameOrID string `json:"test_name_or_id" jsonschema:"Test name (from config) or UUID"`
	YAMLContent  string `json:"yaml_content" jsonschema:"Full YAML test definition with updated blocks"`
	AppID        string `json:"app_id,omitempty" jsonschema:"Optional app ID to associate with the test"`
	Force        bool   `json:"force,omitempty" jsonschema:"Force update even if remote has a newer version"`
}

// UpdateTestOutput defines output for update_test tool.
type UpdateTestOutput struct {
	Success    bool   `json:"success"`
	TestID     string `json:"test_id,omitempty"`
	NewVersion int    `json:"new_version,omitempty"`
	EditorURL  string `json:"editor_url,omitempty"`
	Error      string `json:"error,omitempty"`
}

// handleUpdateTest handles the update_test tool call.
func (s *Server) handleUpdateTest(ctx context.Context, req *mcp.CallToolRequest, input UpdateTestInput) (*mcp.CallToolResult, UpdateTestOutput, error) {
	if input.TestNameOrID == "" {
		return nil, UpdateTestOutput{Success: false, Error: "test_name_or_id is required"}, nil
	}
	if input.YAMLContent == "" {
		return nil, UpdateTestOutput{Success: false, Error: "yaml_content is required"}, nil
	}

	// Validate YAML
	validationResult := yaml.ValidateYAML(input.YAMLContent)
	if !validationResult.Valid {
		return nil, UpdateTestOutput{
			Success: false,
			Error:   fmt.Sprintf("YAML validation failed: %v", validationResult.Errors),
		}, nil
	}

	// Resolve test name to ID
	testID, err := s.resolveTestID(ctx, input.TestNameOrID)
	if err != nil {
		return nil, UpdateTestOutput{Success: false, Error: err.Error()}, nil
	}

	// Parse YAML to extract blocks
	var testDef yaml.TestDefinition
	if parseErr := yamlPkg.Unmarshal([]byte(input.YAMLContent), &testDef); parseErr != nil {
		return nil, UpdateTestOutput{
			Success: false,
			Error:   fmt.Sprintf("failed to parse YAML: %v", parseErr),
		}, nil
	}

	// Build update request
	updateReq := &api.UpdateTestRequest{
		TestID: testID,
		Tasks:  testDef.Test.Blocks,
		AppID:  input.AppID,
		Force:  input.Force,
	}

	resp, err := s.apiClient.UpdateTest(ctx, updateReq)
	if err != nil {
		// Check for version conflict
		if apiErr, ok := err.(*api.APIError); ok && apiErr.StatusCode == 409 {
			return nil, UpdateTestOutput{
				Success: false,
				Error:   "Version conflict: remote test has been modified. Use force=true to overwrite.",
			}, nil
		}
		return nil, UpdateTestOutput{Success: false, Error: fmt.Sprintf("update failed: %v", err)}, nil
	}

	editorURL := fmt.Sprintf("https://app.revyl.ai/tests/%s/edit", testID)

	return nil, UpdateTestOutput{
		Success:    true,
		TestID:     resp.ID,
		NewVersion: resp.Version,
		EditorURL:  editorURL,
	}, nil
}

// --- Script tool handlers ---

// ListScriptsInput defines input for list_scripts tool.
type ListScriptsInput struct {
	NameFilter    string `json:"name_filter,omitempty" jsonschema:"Optional filter to search scripts by name"`
	RuntimeFilter string `json:"runtime_filter,omitempty" jsonschema:"Optional filter by runtime (python, javascript, typescript, bash)"`
}

// ScriptInfo contains information about a script for MCP output.
type ScriptInfo struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Runtime     string  `json:"runtime"`
	Description *string `json:"description,omitempty"`
}

// ListScriptsOutput defines output for list_scripts tool.
type ListScriptsOutput struct {
	Scripts []ScriptInfo `json:"scripts"`
	Total   int          `json:"total"`
	Error   string       `json:"error,omitempty"`
}

// handleListScripts handles the list_scripts tool call.
func (s *Server) handleListScripts(ctx context.Context, req *mcp.CallToolRequest, input ListScriptsInput) (*mcp.CallToolResult, ListScriptsOutput, error) {
	resp, err := s.apiClient.ListScripts(ctx, input.RuntimeFilter, 100, 0)
	if err != nil {
		return nil, ListScriptsOutput{
			Scripts: []ScriptInfo{},
			Error:   fmt.Sprintf("failed to list scripts: %v", err),
		}, nil
	}

	var scripts []ScriptInfo
	for _, sc := range resp.Scripts {
		// Apply name filter if specified
		if input.NameFilter != "" {
			if !strings.Contains(strings.ToLower(sc.Name), strings.ToLower(input.NameFilter)) {
				continue
			}
		}
		scripts = append(scripts, ScriptInfo{
			ID:          sc.ID,
			Name:        sc.Name,
			Runtime:     sc.Runtime,
			Description: sc.Description,
		})
	}

	if scripts == nil {
		scripts = []ScriptInfo{}
	}

	return nil, ListScriptsOutput{
		Scripts: scripts,
		Total:   len(scripts),
	}, nil
}

// GetScriptInput defines input for get_script tool.
type GetScriptInput struct {
	ScriptID string `json:"script_id" jsonschema:"The UUID of the script to retrieve"`
}

// GetScriptOutput defines output for get_script tool.
type GetScriptOutput struct {
	Success     bool    `json:"success"`
	ID          string  `json:"id,omitempty"`
	Name        string  `json:"name,omitempty"`
	Code        string  `json:"code,omitempty"`
	Runtime     string  `json:"runtime,omitempty"`
	Description *string `json:"description,omitempty"`
	Error       string  `json:"error,omitempty"`
}

// handleGetScript handles the get_script tool call.
func (s *Server) handleGetScript(ctx context.Context, req *mcp.CallToolRequest, input GetScriptInput) (*mcp.CallToolResult, GetScriptOutput, error) {
	if input.ScriptID == "" {
		return nil, GetScriptOutput{Success: false, Error: "script_id is required"}, nil
	}

	resp, err := s.apiClient.GetScript(ctx, input.ScriptID)
	if err != nil {
		return nil, GetScriptOutput{Success: false, Error: fmt.Sprintf("failed to get script: %v", err)}, nil
	}

	return nil, GetScriptOutput{
		Success:     true,
		ID:          resp.ID,
		Name:        resp.Name,
		Code:        resp.Code,
		Runtime:     resp.Runtime,
		Description: resp.Description,
	}, nil
}

// CreateScriptInput defines input for create_script tool.
type CreateScriptInput struct {
	Name        string `json:"name" jsonschema:"Script name"`
	Code        string `json:"code" jsonschema:"Script source code"`
	Runtime     string `json:"runtime" jsonschema:"Runtime environment (python, javascript, typescript, or bash)"`
	Description string `json:"description,omitempty" jsonschema:"Optional script description"`
}

// CreateScriptOutput defines output for create_script tool.
type CreateScriptOutput struct {
	Success  bool   `json:"success"`
	ScriptID string `json:"script_id,omitempty"`
	Name     string `json:"name,omitempty"`
	Error    string `json:"error,omitempty"`
}

// handleCreateScript handles the create_script tool call.
func (s *Server) handleCreateScript(ctx context.Context, req *mcp.CallToolRequest, input CreateScriptInput) (*mcp.CallToolResult, CreateScriptOutput, error) {
	if input.Name == "" {
		return nil, CreateScriptOutput{Success: false, Error: "name is required"}, nil
	}
	if input.Code == "" {
		return nil, CreateScriptOutput{Success: false, Error: "code is required"}, nil
	}
	if input.Runtime == "" {
		return nil, CreateScriptOutput{Success: false, Error: "runtime is required"}, nil
	}

	// Validate runtime
	validRuntimes := map[string]bool{"python": true, "javascript": true, "typescript": true, "bash": true}
	if !validRuntimes[input.Runtime] {
		return nil, CreateScriptOutput{
			Success: false,
			Error:   "runtime must be one of: python, javascript, typescript, bash",
		}, nil
	}

	createReq := &api.CLICreateScriptRequest{
		Name:    input.Name,
		Code:    input.Code,
		Runtime: input.Runtime,
	}
	if input.Description != "" {
		createReq.Description = &input.Description
	}

	resp, err := s.apiClient.CreateScript(ctx, createReq)
	if err != nil {
		return nil, CreateScriptOutput{Success: false, Error: fmt.Sprintf("failed to create script: %v", err)}, nil
	}

	return nil, CreateScriptOutput{
		Success:  true,
		ScriptID: resp.ID,
		Name:     resp.Name,
	}, nil
}

// UpdateScriptInput defines input for update_script tool.
type UpdateScriptInput struct {
	ScriptID    string `json:"script_id" jsonschema:"The UUID of the script to update"`
	Name        string `json:"name,omitempty" jsonschema:"New script name"`
	Code        string `json:"code,omitempty" jsonschema:"New script source code"`
	Runtime     string `json:"runtime,omitempty" jsonschema:"New runtime (python, javascript, typescript, bash)"`
	Description string `json:"description,omitempty" jsonschema:"New script description"`
}

// UpdateScriptOutput defines output for update_script tool.
type UpdateScriptOutput struct {
	Success  bool   `json:"success"`
	ScriptID string `json:"script_id,omitempty"`
	Name     string `json:"name,omitempty"`
	Error    string `json:"error,omitempty"`
}

// handleUpdateScript handles the update_script tool call.
func (s *Server) handleUpdateScript(ctx context.Context, req *mcp.CallToolRequest, input UpdateScriptInput) (*mcp.CallToolResult, UpdateScriptOutput, error) {
	if input.ScriptID == "" {
		return nil, UpdateScriptOutput{Success: false, Error: "script_id is required"}, nil
	}

	if input.Name == "" && input.Code == "" && input.Runtime == "" && input.Description == "" {
		return nil, UpdateScriptOutput{Success: false, Error: "at least one field to update is required"}, nil
	}

	// Validate runtime if provided
	if input.Runtime != "" {
		validRuntimes := map[string]bool{"python": true, "javascript": true, "typescript": true, "bash": true}
		if !validRuntimes[input.Runtime] {
			return nil, UpdateScriptOutput{
				Success: false,
				Error:   "runtime must be one of: python, javascript, typescript, bash",
			}, nil
		}
	}

	updateReq := &api.CLIUpdateScriptRequest{}
	if input.Name != "" {
		updateReq.Name = &input.Name
	}
	if input.Code != "" {
		updateReq.Code = &input.Code
	}
	if input.Runtime != "" {
		updateReq.Runtime = &input.Runtime
	}
	if input.Description != "" {
		updateReq.Description = &input.Description
	}

	resp, err := s.apiClient.UpdateScript(ctx, input.ScriptID, updateReq)
	if err != nil {
		return nil, UpdateScriptOutput{Success: false, Error: fmt.Sprintf("failed to update script: %v", err)}, nil
	}

	return nil, UpdateScriptOutput{
		Success:  true,
		ScriptID: resp.ID,
		Name:     resp.Name,
	}, nil
}

// DeleteScriptInput defines input for delete_script tool.
type DeleteScriptInput struct {
	ScriptID string `json:"script_id" jsonschema:"The UUID of the script to delete"`
}

// DeleteScriptOutput defines output for delete_script tool.
type DeleteScriptOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// handleDeleteScript handles the delete_script tool call.
func (s *Server) handleDeleteScript(ctx context.Context, req *mcp.CallToolRequest, input DeleteScriptInput) (*mcp.CallToolResult, DeleteScriptOutput, error) {
	if input.ScriptID == "" {
		return nil, DeleteScriptOutput{Success: false, Error: "script_id is required"}, nil
	}

	err := s.apiClient.DeleteScript(ctx, input.ScriptID)
	if err != nil {
		return nil, DeleteScriptOutput{Success: false, Error: fmt.Sprintf("failed to delete script: %v", err)}, nil
	}

	return nil, DeleteScriptOutput{
		Success: true,
		Message: "Script deleted successfully",
	}, nil
}

// InsertScriptBlockInput defines input for insert_script_block tool.
type InsertScriptBlockInput struct {
	ScriptNameOrID string `json:"script_name_or_id" jsonschema:"Script name or UUID to generate the code_execution block for"`
	VariableName   string `json:"variable_name,omitempty" jsonschema:"Optional variable name to store the script output"`
}

// InsertScriptBlockOutput defines output for insert_script_block tool.
type InsertScriptBlockOutput struct {
	Success         bool   `json:"success"`
	YAMLSnippet     string `json:"yaml_snippet,omitempty"`
	ScriptID        string `json:"script_id,omitempty"`
	ScriptName      string `json:"script_name,omitempty"`
	BlockType       string `json:"block_type,omitempty"`
	StepDescription string `json:"step_description,omitempty"`
	Error           string `json:"error,omitempty"`
}

// handleInsertScriptBlock handles the insert_script_block tool call.
func (s *Server) handleInsertScriptBlock(ctx context.Context, req *mcp.CallToolRequest, input InsertScriptBlockInput) (*mcp.CallToolResult, InsertScriptBlockOutput, error) {
	if input.ScriptNameOrID == "" {
		return nil, InsertScriptBlockOutput{Success: false, Error: "script_name_or_id is required"}, nil
	}

	// Resolve script name or ID
	var scriptID, scriptName string

	// Try as UUID first
	if len(input.ScriptNameOrID) == 36 {
		resp, err := s.apiClient.GetScript(ctx, input.ScriptNameOrID)
		if err == nil {
			scriptID = resp.ID
			scriptName = resp.Name
		}
	}

	// If not found by ID, search by name
	if scriptID == "" {
		listResp, err := s.apiClient.ListScripts(ctx, "", 100, 0)
		if err != nil {
			return nil, InsertScriptBlockOutput{Success: false, Error: fmt.Sprintf("failed to list scripts: %v", err)}, nil
		}

		for _, sc := range listResp.Scripts {
			if strings.EqualFold(sc.Name, input.ScriptNameOrID) {
				scriptID = sc.ID
				scriptName = sc.Name
				break
			}
		}
	}

	if scriptID == "" {
		return nil, InsertScriptBlockOutput{Success: false, Error: fmt.Sprintf("script '%s' not found", input.ScriptNameOrID)}, nil
	}

	// Generate YAML snippet
	var yamlSnippet string
	if input.VariableName != "" {
		yamlSnippet = fmt.Sprintf("- type: code_execution\n  step_description: \"%s\"\n  variable_name: \"%s\"", scriptID, input.VariableName)
	} else {
		yamlSnippet = fmt.Sprintf("- type: code_execution\n  step_description: \"%s\"", scriptID)
	}

	return nil, InsertScriptBlockOutput{
		Success:         true,
		YAMLSnippet:     yamlSnippet,
		ScriptID:        scriptID,
		ScriptName:      scriptName,
		BlockType:       "code_execution",
		StepDescription: scriptID,
	}, nil
}

// --- Live editor tools ---

// OpenTestEditorInput defines input for the open_test_editor tool.
type OpenTestEditorInput struct {
	TestNameOrID string `json:"test_name_or_id" jsonschema:"Test name (from config) or UUID"`
	NoOpen       bool   `json:"no_open,omitempty" jsonschema:"Skip opening the browser (just return URLs)"`
	Provider     string `json:"provider,omitempty" jsonschema:"Hot reload provider (expo/swift/android). Auto-detected if not specified."`
	Port         int    `json:"port,omitempty" jsonschema:"Dev server port (default: from config or 8081)"`
}

// OpenTestEditorOutput defines output for the open_test_editor tool.
type OpenTestEditorOutput struct {
	Success       bool   `json:"success"`
	TestID        string `json:"test_id"`
	EditorURL     string `json:"editor_url"`
	HotReload     bool   `json:"hot_reload"`
	TunnelURL     string `json:"tunnel_url,omitempty"`
	DeepLinkURL   string `json:"deep_link_url,omitempty"`
	DevServerPort int    `json:"dev_server_port,omitempty"`
	Error         string `json:"error,omitempty"`
}

// handleOpenTestEditor handles the open_test_editor tool call.
func (s *Server) handleOpenTestEditor(ctx context.Context, req *mcp.CallToolRequest, input OpenTestEditorInput) (*mcp.CallToolResult, OpenTestEditorOutput, error) {
	if input.TestNameOrID == "" {
		return nil, OpenTestEditorOutput{
			Success: false,
			Error:   "test_name_or_id is required",
		}, nil
	}

	// Resolve test ID and editor URL
	editorResult := execution.OpenTestEditor(s.config, execution.OpenTestEditorParams{
		TestNameOrID: input.TestNameOrID,
		DevMode:      false,
	})

	editorURL := editorResult.TestURL
	testID := editorResult.TestID

	// Check if hot reload is configured
	hasHotReload := s.config != nil && s.config.HotReload.IsConfigured()

	if !hasHotReload {
		// No hot reload config — just open the editor
		if !input.NoOpen {
			_ = ui.OpenBrowser(editorURL)
		}
		return nil, OpenTestEditorOutput{
			Success:   true,
			TestID:    testID,
			EditorURL: editorURL,
			HotReload: false,
		}, nil
	}

	// Hot reload is configured — manage session state
	s.hotReloadMu.Lock()
	defer s.hotReloadMu.Unlock()

	// If already running for the same test, return cached URLs (idempotent)
	if s.hotReloadManager != nil && s.hotReloadManager.IsRunning() {
		if s.hotReloadTestID == testID {
			if !input.NoOpen {
				_ = ui.OpenBrowser(editorURL)
			}
			return nil, OpenTestEditorOutput{
				Success:       true,
				TestID:        testID,
				EditorURL:     editorURL,
				HotReload:     true,
				TunnelURL:     s.hotReloadResult.TunnelURL,
				DeepLinkURL:   s.hotReloadResult.DeepLinkURL,
				DevServerPort: s.hotReloadResult.DevServerPort,
			}, nil
		}
		// Running for a different test — stop it first
		s.hotReloadManager.Stop()
		s.hotReloadManager = nil
		s.hotReloadTestID = ""
		s.hotReloadResult = nil
	}

	// Select provider
	registry := hotreload.DefaultRegistry()
	_, providerCfg, err := registry.SelectProvider(&s.config.HotReload, input.Provider, s.workDir)
	if err != nil {
		// Provider selection failed — fall back to editor-only
		if !input.NoOpen {
			_ = ui.OpenBrowser(editorURL)
		}
		return nil, OpenTestEditorOutput{
			Success:   true,
			TestID:    testID,
			EditorURL: editorURL,
			HotReload: false,
			Error:     fmt.Sprintf("hot reload provider selection failed (opening editor only): %v", err),
		}, nil
	}

	// Override port if specified
	if input.Port > 0 {
		providerCfg.Port = input.Port
	}

	// Determine provider name for the manager
	providerName := input.Provider
	if providerName == "" {
		providerName = s.config.HotReload.Default
		if providerName == "" {
			// Use first configured provider
			for name := range s.config.HotReload.Providers {
				providerName = name
				break
			}
		}
	}

	// Create and start the manager with a background context (survives beyond tool call)
	manager := hotreload.NewManager(providerName, providerCfg, s.workDir)

	bgCtx := context.Background()
	result, err := manager.Start(bgCtx)
	if err != nil {
		// Hot reload failed to start — fall back to editor-only
		if !input.NoOpen {
			_ = ui.OpenBrowser(editorURL)
		}
		return nil, OpenTestEditorOutput{
			Success:   true,
			TestID:    testID,
			EditorURL: editorURL,
			HotReload: false,
			Error:     fmt.Sprintf("hot reload failed to start (opening editor only): %v", err),
		}, nil
	}

	// Store session state
	s.hotReloadManager = manager
	s.hotReloadTestID = testID
	s.hotReloadResult = result

	if !input.NoOpen {
		_ = ui.OpenBrowser(editorURL)
	}

	return nil, OpenTestEditorOutput{
		Success:       true,
		TestID:        testID,
		EditorURL:     editorURL,
		HotReload:     true,
		TunnelURL:     result.TunnelURL,
		DeepLinkURL:   result.DeepLinkURL,
		DevServerPort: result.DevServerPort,
	}, nil
}

// StopHotReloadInput defines input for the stop_hot_reload tool.
type StopHotReloadInput struct{}

// StopHotReloadOutput defines output for the stop_hot_reload tool.
type StopHotReloadOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// handleStopHotReload handles the stop_hot_reload tool call.
func (s *Server) handleStopHotReload(ctx context.Context, req *mcp.CallToolRequest, input StopHotReloadInput) (*mcp.CallToolResult, StopHotReloadOutput, error) {
	s.hotReloadMu.Lock()
	defer s.hotReloadMu.Unlock()

	if s.hotReloadManager == nil {
		return nil, StopHotReloadOutput{
			Success: true,
			Message: "No active hot reload session",
		}, nil
	}

	s.hotReloadManager.Stop()
	s.hotReloadManager = nil
	s.hotReloadTestID = ""
	s.hotReloadResult = nil

	return nil, StopHotReloadOutput{
		Success: true,
		Message: "Hot reload session stopped",
	}, nil
}

// HotReloadStatusInput defines input for the hot_reload_status tool.
type HotReloadStatusInput struct{}

// HotReloadStatusOutput defines output for the hot_reload_status tool.
type HotReloadStatusOutput struct {
	Active        bool   `json:"active"`
	TestID        string `json:"test_id,omitempty"`
	EditorURL     string `json:"editor_url,omitempty"`
	TunnelURL     string `json:"tunnel_url,omitempty"`
	DeepLinkURL   string `json:"deep_link_url,omitempty"`
	DevServerPort int    `json:"dev_server_port,omitempty"`
}

// handleHotReloadStatus handles the hot_reload_status tool call.
func (s *Server) handleHotReloadStatus(ctx context.Context, req *mcp.CallToolRequest, input HotReloadStatusInput) (*mcp.CallToolResult, HotReloadStatusOutput, error) {
	s.hotReloadMu.Lock()
	defer s.hotReloadMu.Unlock()

	if s.hotReloadManager == nil || !s.hotReloadManager.IsRunning() {
		return nil, HotReloadStatusOutput{Active: false}, nil
	}

	// Reconstruct editor URL from cached test ID
	editorURL := fmt.Sprintf("%s/tests/execute?testUid=%s", config.GetAppURL(false), s.hotReloadTestID)

	return nil, HotReloadStatusOutput{
		Active:        true,
		TestID:        s.hotReloadTestID,
		EditorURL:     editorURL,
		TunnelURL:     s.hotReloadResult.TunnelURL,
		DeepLinkURL:   s.hotReloadResult.DeepLinkURL,
		DevServerPort: s.hotReloadResult.DevServerPort,
	}, nil
}

// Shutdown cleans up server resources, including any active hot reload session.
func (s *Server) Shutdown() {
	s.hotReloadMu.Lock()
	defer s.hotReloadMu.Unlock()
	if s.hotReloadManager != nil {
		s.hotReloadManager.Stop()
		s.hotReloadManager = nil
	}
}
