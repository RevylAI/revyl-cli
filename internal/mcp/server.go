// Package mcp provides the Revyl MCP server implementation.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	yamlPkg "gopkg.in/yaml.v3"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/billingstatus"
	"github.com/revyl/cli/internal/checkoutdefinitions"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/devloop"
	"github.com/revyl/cli/internal/execution"
	"github.com/revyl/cli/internal/hotreload"
	_ "github.com/revyl/cli/internal/hotreload/providers"
	"github.com/revyl/cli/internal/orgguard"
	"github.com/revyl/cli/internal/schema"
	"github.com/revyl/cli/internal/sse"
	"github.com/revyl/cli/internal/ui"
	"github.com/revyl/cli/internal/workflowref"
)

type Server struct {
	mcpServer          *mcp.Server
	apiClient          *api.Client
	authManager        *auth.Manager
	project            *config.ProjectContext
	projectErr         error
	workDir            string
	explicitProjectDir string
	version            string
	devMode            bool
	runInvestigation   investigationRunner
	rootCmd            *cobra.Command
	sessionMgr         *DeviceSessionManager

	hotReloadManager *hotreload.Manager
	hotReloadMu      sync.Mutex
	hotReloadTestID  string
	hotReloadResult  *hotreload.StartResult

	devLoopRunner       devloop.Runner
	delegatedDevWorkDir string
	delegatedDevContext string

	profile               Profile
	experimentalWorkspace bool
	experimentalComputer  bool
	workspaceOrigin       string
	chatGPTPluginID       string

	pendingAuth pendingAuthorization

	sessionTargetMu   sync.Mutex
	sessionTargetRank int
}

type ServerOption func(*Server)

// WithProfile selects the MCP tool profile (ProfileDev, ProfileCore, or ProfileFull).
func WithProfile(p Profile) ServerOption {
	return func(s *Server) {
		s.profile = p
	}
}

func WithDevLoopRunner(runner devloop.Runner) ServerOption {
	return func(s *Server) {
		s.devLoopRunner = runner
	}
}

func NewServer(version string, devMode bool, opts ...ServerOption) (*Server, error) {
	s := &Server{}
	for _, opt := range opts {
		opt(s)
	}

	credentialManager := auth.NewManager()
	authentication := resolveMCPAuthentication(credentialManager)
	if authentication.LoadError != nil && s.profile != ProfileDev {
		return nil, fmt.Errorf("could not load Revyl credentials: %w", authentication.LoadError)
	}
	if authentication.State != authenticationStateAuthenticated && s.profile != ProfileDev {
		return nil, fmt.Errorf("not authenticated: set REVYL_API_KEY or run 'revyl auth login'")
	}

	// Get working directory: prefer explicit env so Cursor (or any host) can set it
	// when the process is spawned with a different cwd (e.g. extension host cwd).
	workDir := os.Getenv("REVYL_PROJECT_DIR")
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve MCP working directory: %w", err)
		}
	} else {
		var err error
		workDir, err = filepath.Abs(workDir)
		if err != nil {
			return nil, fmt.Errorf("resolve MCP project directory: %w", err)
		}
		s.explicitProjectDir = workDir
	}

	project, projectErr := config.ResolveProjectContext(workDir, "")
	if projectErr == nil {
		workDir = project.ProjectRoot
	}

	binaryPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve Revyl executable for dev loop: %w", err)
	}
	s.apiClient = api.NewClientWithDevMode(authentication.Token, devMode)
	s.authManager = credentialManager
	s.project = project
	s.projectErr = projectErr
	s.workDir = workDir
	s.version = version
	s.devMode = devMode
	if s.experimentalWorkspace {
		s.chatGPTPluginID = os.Getenv("REVYL_CHATGPT_PLUGIN_ID")
		if s.chatGPTPluginID != "" && !chatGPTPluginIDPattern.MatchString(s.chatGPTPluginID) {
			return nil, fmt.Errorf("REVYL_CHATGPT_PLUGIN_ID must be a plugin ID of at most 128 letters, digits, underscores, or hyphens, starting with a letter or digit")
		}
		s.workspaceOrigin, err = validateWorkspaceOrigin(config.GetAppURL(devMode))
		if err != nil {
			return nil, err
		}
	}
	if s.devLoopRunner == nil {
		s.devLoopRunner = &devloop.CommandRunner{
			BinaryPath: binaryPath,
			DevMode:    devMode,
		}
	}

	s.sessionMgr = NewDeviceSessionManager(s.apiClient, workDir)
	s.sessionMgr.SetDevMode(devMode)

	// Set the version on the API client so the User-Agent header reflects
	// the real CLI build version (e.g. "revyl-cli/1.2.3") instead of "revyl-cli/dev".
	s.apiClient.SetVersion(version)

	s.mcpServer = mcp.NewServer(
		&mcp.Implementation{
			Name:    "revyl",
			Version: version,
		},
		&mcp.ServerOptions{
			Instructions: instructionsForProfile(s.profile),
		},
	)
	s.mcpServer.AddReceivingMiddleware(toolCallAnalyticsMiddleware)
	s.registerScreenshotAppResource()
	s.registerAuthAppResource()

	if s.profile != "" {
		s.registerCompositeTools(s.profile)
	} else {
		s.registerTools()
	}
	if s.experimentalWorkspace {
		s.registerWorkspaceApp()
	}

	return s, nil
}

func (s *Server) resolveOptionalCanonicalProject() (*config.ProjectContext, error) {
	return s.resolveOptionalCanonicalProjectAt("")
}

func (s *Server) resolveOptionalCanonicalProjectAt(projectDirectory string) (*config.ProjectContext, error) {
	workDir := s.toolProjectDirectory(projectDirectory)
	status := resolveSetupProjectStateForMode(workDir, s.devMode)
	if projectStateHasConfig(status.State) {
		return s.resolveCanonicalProject(workDir)
	}
	if status.State == projectStateNotInitialized ||
		(status.State == projectStateOutsideGit && localProjectConfigAbsent(workDir)) {
		return nil, nil
	}
	return nil, &projectSetupError{status: status}
}

func (s *Server) resolveCanonicalProject(projectDirectory string) (*config.ProjectContext, error) {
	if strings.TrimSpace(projectDirectory) == "" {
		projectDirectory = s.workDir
	}
	status := resolveSetupProjectStateForMode(projectDirectory, s.devMode)
	if !projectStateHasConfig(status.State) {
		return nil, &projectSetupError{status: status}
	}
	project, err := config.ResolveProjectContext(status.ProjectDirectory, "")
	if err != nil {
		return nil, &projectSetupError{status: inspectProjectConfigStateForMode(status.ProjectDirectory, s.devMode)}
	}
	return project, nil
}

func (s *Server) SetRootCmd(cmd *cobra.Command) {
	s.rootCmd = cmd
}

func (s *Server) Run(ctx context.Context) error {
	defer s.Shutdown()
	return s.mcpServer.Run(ctx, &mcp.StdioTransport{})
}

func (s *Server) registerTools() {
	s.registerDeviceTools()
	s.registerDevLoopTools()
	s.registerRunInspectTools()
	s.registerInvestigateTool()
	s.registerManagePersistentComputersTool()

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "run_test",
		Description: "Run a Revyl test by name or ID. Returns test results including pass/fail status and report URL. A run that waited for a free device also returns queue_notice.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Run Test",
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleRunTest)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "run_workflow",
		Description: "Run a Revyl workflow (collection of tests) by name or ID and wait for results. Returns pass/fail counts and per-child tests with resolved app and branch identity; branch_source says where each child's branch came from (git_branch, default_branch, or explicit_branch). auto_git_branch_enabled=false means automatic Git branch detection was disabled; default_branch with a git_branch_name means that Git branch had no usable build. Queued launches have no resolved branch yet.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Run Workflow",
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleRunWorkflow)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_tests",
		Description: "List the project's synced tests from .revyl/tests/ and the organization's workflows.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Tests",
			ReadOnlyHint: true,
		},
	}, s.handleListTests)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_test_status",
		Description: "Get the current status of a running or completed test execution.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Test Status",
			ReadOnlyHint: true,
		},
	}, s.handleGetTestStatus)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "create_test",
		Description: `Create a new runnable test from YAML content and optional module imports.

RECOMMENDED: Before creating a test, read the app's source code (screens, components, routes) to understand the real UI labels, navigation flow, and user-facing outcomes. Use get_schema for the YAML format reference, and use module_names_or_ids when reusable flows already exist.`,
		Annotations: &mcp.ToolAnnotations{
			Title: "Create Test",
		},
	}, s.handleCreateTest)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "create_workflow",
		Description: "Create a new workflow (collection of tests).",
		Annotations: &mcp.ToolAnnotations{
			Title: "Create Workflow",
		},
	}, s.handleCreateWorkflow)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_schema",
		Description: "Get the complete CLI command schema and YAML test schema for LLM reference.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Schema",
			ReadOnlyHint: true,
		},
	}, s.handleGetSchema)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_builds",
		Description: "List build versions across all branches of an app, or narrow by an explicit branch selector. Without app_id, list app summaries in the organization.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Builds",
			ReadOnlyHint: true,
		},
	}, s.handleListBuilds)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "open_workflow_editor",
		Description: "Get the URL to open a workflow in the browser editor.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Open Workflow Editor",
			ReadOnlyHint: true,
		},
	}, s.handleOpenWorkflowEditor)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "cancel_test",
		Description: "Cancel a running test execution by task ID.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Cancel Test",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleCancelTest)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "cancel_workflow",
		Description: "Cancel a running workflow execution by task ID.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Cancel Workflow",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleCancelWorkflow)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_test",
		Description: "Delete a test by name (a .revyl/tests/ file name or a Revyl test name) or UUID.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete Test",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleDeleteTest)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_workflow",
		Description: "Delete a workflow by name or UUID.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete Workflow",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleDeleteWorkflow)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_remote_tests",
		Description: "List all tests in the organization from the remote API (not just local config).",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Remote Tests",
			ReadOnlyHint: true,
		},
	}, s.handleListRemoteTests)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_workflows",
		Description: "List all workflows in the organization from the remote API.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Workflows",
			ReadOnlyHint: true,
		},
	}, s.handleListWorkflows)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "auth_status",
		Description: "Check current authentication status and return user info.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Auth Status",
			ReadOnlyHint: true,
		},
	}, s.handleAuthStatus)
	s.registerBillingStatusTool()

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "create_app",
		Description: "Create an iOS or Android app for uploads, or a Web app for URL builds.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Create App",
		},
	}, s.handleCreateApp)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_app",
		Description: "Delete an app and all its build versions by app ID.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete App",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleDeleteApp)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_modules",
		Description: "List all reusable test modules in the organization. Modules are groups of test blocks that can be imported into any test via module_import blocks.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Modules",
			ReadOnlyHint: true,
		},
	}, s.handleListModules)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_module",
		Description: "Get details of a specific module by ID, including its blocks.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Module",
			ReadOnlyHint: true,
		},
	}, s.handleGetModule)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "create_module",
		Description: "Create a new reusable test module from a list of blocks.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Create Module",
		},
	}, s.handleCreateModule)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_module",
		Description: "Delete a module by ID. Returns 409 if the module is in use by tests.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete Module",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleDeleteModule)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "insert_module_block",
		Description: "Given a module name or ID, returns a module_import block YAML snippet ready to insert into a test. Use this to compose tests with reusable modules.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Insert Module Block",
			ReadOnlyHint: true,
		},
	}, s.handleInsertModuleBlock)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_tags",
		Description: "List all tags in the organization with test counts. Tags are used to categorize and filter tests.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Tags",
			ReadOnlyHint: true,
		},
	}, s.handleListTags)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "create_tag",
		Description: "Create a new tag. If a tag with the same name already exists, the existing tag is returned (upsert behavior).",
		Annotations: &mcp.ToolAnnotations{
			Title: "Create Tag",
		},
	}, s.handleCreateTag)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_tag",
		Description: "Delete a tag by name or ID. This removes it from all tests.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete Tag",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleDeleteTag)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_test_tags",
		Description: "Get all tags assigned to a specific test.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Test Tags",
			ReadOnlyHint: true,
		},
	}, s.handleGetTestTags)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "set_test_tags",
		Description: "Replace all tags on a test with the given tag names. Tags are auto-created if they don't exist.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Set Test Tags",
		},
	}, s.handleSetTestTags)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_files",
		Description: "List all files uploaded to the organization. Files can be certificates, configs, images, or media used in tests via revyl-file:// references.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Files",
			ReadOnlyHint: true,
		},
	}, s.handleListFiles)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "upload_file",
		Description: "Upload a local file to the organization. Supports certs (.pem, .cer, .crt, .key, .p12, .pfx, .der), configs (.json, .xml, .yaml, .yml, .toml, .csv, .txt, .conf, .cfg, .ini, .properties), images (.png, .jpg, .jpeg, .gif, .pdf), and media (.mp4, .mp3).",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Upload File",
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleUploadFile)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "download_file",
		Description: "Download an organization file to a local path.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Download File",
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleDownloadFile)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_file_download_url",
		Description: "Get a presigned download URL for an organization file. Useful when the agent cannot write to disk directly.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get File Download URL",
			ReadOnlyHint: true,
		},
	}, s.handleGetFileDownloadURL)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "edit_file",
		Description: "Edit file metadata (filename, description) and/or replace file content. The file ID is preserved when replacing content, so revyl-file:// references remain valid.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Edit File",
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleEditFile)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_file",
		Description: "Delete an organization file. Warning: tests referencing this file via revyl-file:// will fail.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete File",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleDeleteFile)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_variables",
		Description: "List all test variables for a test. Test variables use {{name}} syntax in step descriptions and are substituted at runtime.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Variables",
			ReadOnlyHint: true,
		},
	}, s.handleListVariables)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "set_variable",
		Description: "Add or update a test variable. Test variables use {{name}} syntax in step descriptions. If the variable name already exists, its value is updated. Variable names must use letters, numbers, hyphens, or underscores (no spaces).",
		Annotations: &mcp.ToolAnnotations{
			Title: "Set Variable",
		},
	}, s.handleSetVariable)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_variable",
		Description: "Delete a test variable by name.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Delete Variable",
		},
	}, s.handleDeleteVariable)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_all_variables",
		Description: "Delete ALL test variables for a test.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete All Variables",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleDeleteAllVariables)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_workflow_settings",
		Description: "Get workflow settings including location override and app override configuration.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Workflow Settings",
			ReadOnlyHint: true,
		},
	}, s.handleGetWorkflowSettings)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "set_workflow_location",
		Description: "Set a stored GPS location override for all tests in a workflow.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Set Workflow Location",
		},
	}, s.handleSetWorkflowLocation)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "clear_workflow_location",
		Description: "Remove the stored GPS location override from a workflow.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Clear Workflow Location",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleClearWorkflowLocation)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "set_workflow_app",
		Description: "Set stored app overrides (per platform) for all tests in a workflow. App IDs are validated.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Set Workflow App",
		},
	}, s.handleSetWorkflowApp)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "clear_workflow_app",
		Description: "Remove stored app overrides from a workflow.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Clear Workflow App",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleClearWorkflowApp)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "add_tests_to_workflow",
		Description: "Add one or more tests to an existing workflow. Tests are appended (duplicates are ignored). Accepts test names (.revyl/tests/ file names or Revyl test names) or UUIDs.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Add Tests to Workflow",
		},
	}, s.handleAddTestsToWorkflow)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "remove_tests_from_workflow",
		Description: "Remove one or more tests from an existing workflow. Accepts test names (.revyl/tests/ file names or Revyl test names) or UUIDs.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Remove Tests from Workflow",
		},
	}, s.handleRemoveTestsFromWorkflow)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "add_remove_test_tags",
		Description: "Add and/or remove tags on a test without replacing all existing tags.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Add/Remove Test Tags",
		},
	}, s.handleAddRemoveTestTags)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "upload_build",
		Description: "Upload a local build file (.apk, .ipa, or .zip) to an existing app. Returns the new version ID.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Upload Build",
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleUploadBuild)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "update_test",
		Description: `Update an existing test's YAML content (blocks). Pushes new blocks to the remote test.

Use get_schema for the YAML format reference. The YAML must include the full test definition with metadata, build, and blocks sections.`,
		Annotations: &mcp.ToolAnnotations{
			Title: "Update Test",
		},
	}, s.handleUpdateTest)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_scripts",
		Description: "List all code execution scripts in the organization. Scripts contain reusable code that runs in sandboxed environments during test execution.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Scripts",
			ReadOnlyHint: true,
		},
	}, s.handleListScripts)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_script",
		Description: "Get details of a specific script by ID, including its source code.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Script",
			ReadOnlyHint: true,
		},
	}, s.handleGetScript)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "create_script",
		Description: "Create a new code execution script. Scripts can be referenced in tests via code_execution blocks.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Create Script",
		},
	}, s.handleCreateScript)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "update_script",
		Description: "Update an existing script's name, code, runtime, or description.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Update Script",
		},
	}, s.handleUpdateScript)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_script",
		Description: "Delete a script by ID.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete Script",
			DestructiveHint: boolPtr(true),
		},
	}, s.handleDeleteScript)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "insert_script_block",
		Description: "Given a script name or ID, returns a code_execution block YAML snippet ready to insert into a test.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Insert Script Block",
			ReadOnlyHint: true,
		},
	}, s.handleInsertScriptBlock)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "open_test_editor",
		Description: "Open a test in the browser editor, optionally with hot reload. Starts dev server and tunnel if hot reload is configured. Opens the browser by default.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Open Test Editor",
			ReadOnlyHint: true,
		},
	}, s.handleOpenTestEditor)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "stop_hot_reload",
		Description: "Stop the hot reload session (dev server and tunnel). Call this when done with live editing.",
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

type RunTestInput struct {
	ProjectDir                 string   `json:"project_dir,omitempty" jsonschema:"Customer project directory supplying Git context when no branch is chosen; automatic Git branch detection in each app's Build branches settings decides whether to use it or the app default branch. Omission uses REVYL_PROJECT_DIR when bound, never the MCP server's own checkout"`
	TestName                   string   `json:"test_name" jsonschema:"Test name (a .revyl/tests/ file name or a Revyl test name) or UUID"`
	Retries                    int      `json:"retries,omitempty" jsonschema:"Deprecated and ignored; each test runs once"`
	BuildVersionID             string   `json:"build_version_id,omitempty" jsonschema:"Specific build version ID to test against"`
	BranchID                   string   `json:"branch_id,omitempty" jsonschema:"Explicit Revyl branch ID; omit to preserve the saved test selection"`
	BranchName                 string   `json:"branch_name,omitempty" jsonschema:"Exact Revyl branch name; overrides the project's Git branch and the app default branch"`
	BuildSelection             string   `json:"build_selection,omitempty" jsonschema:"Build choice: inherit (Automatic: the branch's default build), latest, latest_all_branches (the newest build across all of the app's branches; no branch; ignores the project's Git branch), or pinned (requires build_version_id)"`
	Location                   string   `json:"location,omitempty" jsonschema:"Override GPS location as lat,lng (e.g. 37.7749,-122.4194)"`
	DeviceModel                string   `json:"device_model,omitempty" jsonschema:"Override device model (e.g. iPhone 16, Pixel 7)"`
	OsVersion                  string   `json:"os_version,omitempty" jsonschema:"Override OS version (e.g. iOS 18.5, Android 14)"`
	Orientation                string   `json:"orientation,omitempty" jsonschema:"Override device orientation (portrait or landscape)"`
	LaunchVars                 []string `json:"launch_vars,omitempty" jsonschema:"Additional organization launch-variable keys or IDs."`
	LaunchArgSets              []string `json:"launch_arg_sets,omitempty" jsonschema:"Stored iOS argument-set names or IDs."`
	LaunchArguments            []string `json:"launch_arguments,omitempty" jsonschema:"Inline non-secret iOS app argument tokens in exact order."`
	DisableInheritedLaunchVars bool     `json:"disable_inherited_launch_vars,omitempty" jsonschema:"Ignore REVYL_INHERITED_LAUNCH_ENV_VAR_IDS entirely; explicit launch_vars still apply."`
	FromCheckout               *bool    `json:"from_checkout,omitempty" jsonschema:"Run the project checkout's test, module and script files, uncommitted changes included, instead of the files in Git or the saved copies. Omitted, it follows run_test_definitions_from_git in the project's .revyl/config.yaml, as revyl test run does; false runs without the checkout's files"`
}

type RunTestOutput struct {
	AutoGitBranchEnabled               *bool                            `json:"auto_git_branch_enabled,omitempty"`
	Success                            bool                             `json:"success"`
	TaskID                             string                           `json:"task_id"`
	TestID                             string                           `json:"test_id"`
	TestName                           string                           `json:"test_name"`
	Status                             string                           `json:"status"`
	Duration                           string                           `json:"duration"`
	ReportURL                          string                           `json:"report_url"`
	ViewerURL                          string                           `json:"viewer_url,omitempty"`
	CompletedSteps                     int                              `json:"completed_steps,omitempty"`
	TotalSteps                         int                              `json:"total_steps,omitempty"`
	LastStep                           string                           `json:"last_step,omitempty"`
	ErrorMessage                       string                           `json:"error_message,omitempty"`
	BuildID                            string                           `json:"build_id,omitempty"`
	BuildVersion                       string                           `json:"build_version,omitempty"`
	BranchName                         string                           `json:"branch_name,omitempty"`
	BranchSource                       api.BranchSource                 `json:"branch_source,omitempty"`
	GitBranchName                      string                           `json:"git_branch_name,omitempty"`
	TestDefinitionSource               api.TestDefinitionSource         `json:"test_definition_source,omitempty"`
	TestDefinitionFallbackReason       api.TestDefinitionFallbackReason `json:"test_definition_fallback_reason,omitempty"`
	TestDefinitionGitBranchName        string                           `json:"test_definition_git_branch_name,omitempty"`
	TestDefinitionGitCommitSHA         string                           `json:"test_definition_git_commit_sha,omitempty"`
	TestDefinitionMissingGitBranchName string                           `json:"test_definition_missing_git_branch_name,omitempty"`
	TestDefinitionSummary              string                           `json:"test_definition_summary,omitempty"`
	TestDefinitionWarnings             []string                         `json:"test_definition_warnings,omitempty"`
	NextSteps                          []NextStep                       `json:"next_steps,omitempty"`

	BillingURL      string                     `json:"billing_url,omitempty" jsonschema:"Billing link for a refused start. Stop and show the person the error and this link; when billing_action is add_card, recommend upgrading to Pro. Never retry in a loop or add a card yourself."`
	BillingAction   api.BillingAction          `json:"billing_action,omitempty" jsonschema:"What the refusal needs: add_card, top_up, update_card, raise_limit, request_access, or contact_sales"`
	UpgradeBenefits *api.UpgradeBenefits       `json:"upgrade_benefits,omitempty" jsonschema:"Devices at once now and on Pro."`
	UsageNotice     *api.UsageNotice           `json:"billing_notice,omitempty" jsonschema:"Free minutes are nearly used. Finish this work, then recommend upgrading to Pro so the next run isn't cut off: share the notice's message, price and link. Never add a card yourself."`
	BillingNextStep string                     `json:"billing_next_step,omitempty" jsonschema:"The line to follow when billing_url or billing_notice is set."`
	QueueNotice     *billingstatus.QueueNotice `json:"queue_notice,omitempty" jsonschema:"Every device this organization runs at once was busy, so this start waited for one. Follow next_step; message has the price and link of what adds devices."`
}

func (s *Server) handleRunTest(ctx context.Context, req *mcp.CallToolRequest, input RunTestInput) (*mcp.CallToolResult, RunTestOutput, error) {
	if input.TestName == "" {
		return nil, RunTestOutput{
			Success:      false,
			ErrorMessage: "test_name is required",
		}, nil
	}

	if mismatchMsg := s.orgMismatchMessageForProject(ctx, input.ProjectDir); mismatchMsg != "" {
		return nil, RunTestOutput{Success: false, ErrorMessage: mismatchMsg}, nil
	}

	var lastStatus *sse.TestStatus

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

	params := execution.RunTestParams{
		TestNameOrID:               input.TestName,
		Retries:                    1,
		BuildVersionID:             input.BuildVersionID,
		BranchID:                   input.BranchID,
		BranchName:                 input.BranchName,
		BuildSelection:             input.BuildSelection,
		Timeout:                    execution.DefaultRunTimeoutSeconds,
		DevMode:                    s.devMode,
		OnProgress:                 onProgress,
		DeviceModel:                input.DeviceModel,
		OsVersion:                  input.OsVersion,
		Orientation:                input.Orientation,
		LaunchVars:                 input.LaunchVars,
		LaunchArgSets:              input.LaunchArgSets,
		LaunchArguments:            input.LaunchArguments,
		DisableInheritedLaunchVars: input.DisableInheritedLaunchVars,
	}
	project, projectErr := s.resolveOptionalCanonicalProjectAt(input.ProjectDir)
	if projectErr != nil {
		if s.profile == ProfileCore || s.profile == ProfileFull {
			return nil, RunTestOutput{}, projectErr
		}
		return nil, RunTestOutput{Success: false, ErrorMessage: projectErr.Error()}, nil
	}
	testID := input.TestName
	if project != nil {
		params.TestsDir = project.TestsDir
		if remoteID, err := config.GetLocalTestRemoteID(project.TestsDir, input.TestName); err == nil && remoteID != "" {
			testID = remoteID
		}
	}
	runProject, err := checkoutdefinitions.RunProject(project, input.FromCheckout)
	if err != nil {
		return nil, RunTestOutput{Success: false, ErrorMessage: err.Error()}, nil
	}
	var checkoutUpload *checkoutdefinitions.Upload
	if runProject != nil {
		if checkoutUpload, err = collectCheckoutUpload(ctx, runProject, []string{testID}); err != nil {
			return nil, RunTestOutput{Success: false, ErrorMessage: err.Error()}, nil
		}
	}
	params.TestDefinitionFiles = checkoutUpload.RequestFiles()
	if input.Location != "" {
		lat, lng, locErr := parseLocationString(input.Location)
		if locErr != nil {
			return nil, RunTestOutput{Success: false, ErrorMessage: locErr.Error()}, nil
		}
		params.Latitude = lat
		params.Longitude = lng
		params.HasLocation = true
	}

	if input.BranchID == "" && input.BranchName == "" && input.BuildVersionID == "" &&
		api.BuildSelectionMode(input.BuildSelection) != api.BuildSelectionModeLatestAllBranches {
		gitBranch, err := s.detectGitBranch(ctx, input.ProjectDir)
		if err != nil {
			return nil, RunTestOutput{Success: false, ErrorMessage: err.Error()}, nil
		}
		params.GitBranchName = gitBranch
	} else if checkoutUpload != nil {
		params.GitBranchName = checkoutUpload.GitBranchName
	}

	result, err := execution.RunTest(ctx, s.apiClient.GetAPIKey(), nil, params)
	if err != nil {
		return nil, RunTestOutput{Success: false, ErrorMessage: err.Error()}, nil
	}

	out := RunTestOutput{
		Success:      result.Success,
		TaskID:       result.TaskID,
		TestID:       result.TestID,
		TestName:     result.TestName,
		Status:       result.Status,
		Duration:     result.Duration,
		ReportURL:    result.ReportURL,
		ViewerURL:    result.ReportURL,
		ErrorMessage: result.ErrorMessage,
		BuildID:      result.BuildID,
		BuildVersion: result.BuildVersion,
		BranchName:   result.BranchName,
		BranchSource: result.BranchSource, GitBranchName: result.GitBranchName,
		AutoGitBranchEnabled:               result.AutoGitBranchEnabled,
		TestDefinitionSource:               result.TestDefinitionSource,
		TestDefinitionFallbackReason:       result.TestDefinitionFallbackReason,
		TestDefinitionGitBranchName:        result.TestDefinitionGitBranchName,
		TestDefinitionGitCommitSHA:         result.TestDefinitionGitCommitSHA,
		TestDefinitionMissingGitBranchName: result.TestDefinitionMissingGitBranchName,
	}
	if result.TestDefinitionSource != "" {
		testCountByFallbackReason := map[api.TestDefinitionFallbackReason]int{}
		if result.TestDefinitionFallbackReason != "" {
			testCountByFallbackReason[result.TestDefinitionFallbackReason] = 1
		}
		out.TestDefinitionSummary = testDefinitionSummary(checkoutUpload, map[api.TestDefinitionSource]int{result.TestDefinitionSource: 1}, testCountByFallbackReason, result.TestDefinitionGitRead)
	}
	if checkoutUpload != nil {
		out.TestDefinitionWarnings = checkoutUpload.Warnings
	}
	out.BillingURL, out.BillingAction, out.UsageNotice = result.BillingURL, result.BillingAction, result.UsageNotice
	out.UpgradeBenefits, out.QueueNotice = result.UpgradeBenefits, result.QueueNotice
	out.BillingNextStep = billingNextStep(out.BillingURL, out.BillingAction, out.UpgradeBenefits, out.UsageNotice)
	if lastStatus != nil {
		out.CompletedSteps = lastStatus.CompletedSteps
		out.TotalSteps = lastStatus.TotalSteps
		out.LastStep = lastStatus.CurrentStep
	}

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
	default:
		out.NextSteps = []NextStep{
			{Tool: "get_test_status", Params: fmt.Sprintf("task_id=%s", result.TaskID), Reason: "Get step-by-step failure details"},
			{Tool: "run_test", Params: fmt.Sprintf("test_name=%s", input.TestName), Reason: "Retry the test after fixing the issue"},
		}
	}

	if out.BillingURL != "" {
		out.NextSteps = nil
	}

	return nil, out, nil
}

type RunWorkflowInput struct {
	ProjectDir                 string   `json:"project_dir,omitempty" jsonschema:"Customer project directory supplying Git context when no branch is chosen; automatic Git branch detection in each app's Build branches settings decides whether to use it or the app default branch. Omission uses REVYL_PROJECT_DIR when bound, never the MCP server's own checkout"`
	WorkflowName               string   `json:"workflow_name" jsonschema:"Workflow name or UUID"`
	Retries                    int      `json:"retries,omitempty" jsonschema:"Deprecated and ignored; each test runs once"`
	IOSAppID                   string   `json:"ios_app_id,omitempty" jsonschema:"Override iOS app ID for all tests in workflow"`
	AndroidAppID               string   `json:"android_app_id,omitempty" jsonschema:"Override Android app ID for all tests in workflow"`
	BranchName                 string   `json:"branch_name,omitempty" jsonschema:"Exact Revyl branch name resolved separately for each effective app; omitted preserves saved selections"`
	BuildSelection             string   `json:"build_selection,omitempty" jsonschema:"Build choice for every app: inherit (Automatic: the branch's default build), latest, or latest_all_branches (the newest build across all of each app's branches; no branch_name); omit when passing exact platform build IDs"`
	IOSBuildID                 string   `json:"ios_build_id,omitempty" jsonschema:"Exact iOS build ID for this run"`
	AndroidBuildID             string   `json:"android_build_id,omitempty" jsonschema:"Exact Android build ID for this run"`
	Location                   string   `json:"location,omitempty" jsonschema:"Override GPS location as lat,lng (e.g. 37.7749,-122.4194)"`
	LaunchVars                 []string `json:"launch_vars,omitempty" jsonschema:"Additional organization launch-variable keys or IDs for every child test."`
	LaunchArgSets              []string `json:"launch_arg_sets,omitempty" jsonschema:"Stored iOS argument-set names or IDs for iOS child tests."`
	LaunchArguments            []string `json:"launch_arguments,omitempty" jsonschema:"Inline non-secret iOS app argument tokens for iOS child tests."`
	DisableInheritedLaunchVars bool     `json:"disable_inherited_launch_vars,omitempty" jsonschema:"Ignore REVYL_INHERITED_LAUNCH_ENV_VAR_IDS entirely; explicit launch_vars still apply."`
	FromCheckout               *bool    `json:"from_checkout,omitempty" jsonschema:"Run the project checkout's test, module and script files, uncommitted changes included, instead of the files in Git or the saved copies. Omitted, it follows run_test_definitions_from_git in the project's .revyl/config.yaml, as revyl test run does; false runs without the checkout's files"`
}

type RunWorkflowOutput struct {
	Success                             bool                           `json:"success"`
	TaskID                              string                         `json:"task_id"`
	WorkflowID                          string                         `json:"workflow_id"`
	Status                              string                         `json:"status"`
	TotalTests                          int                            `json:"total_tests"`
	PassedTests                         int                            `json:"passed_tests"`
	FailedTests                         int                            `json:"failed_tests"`
	Duration                            string                         `json:"duration"`
	ReportURL                           string                         `json:"report_url"`
	ViewerURL                           string                         `json:"viewer_url,omitempty"`
	CompletedTests                      int                            `json:"completed_tests,omitempty"`
	CurrentTest                         string                         `json:"current_test,omitempty"`
	ErrorMessage                        string                         `json:"error_message,omitempty"`
	Tests                               []execution.WorkflowTestResult `json:"tests,omitempty"`
	TestCountByDefinitionSource         map[string]int                 `json:"test_count_by_definition_source,omitempty"`
	TestCountByDefinitionFallbackReason map[string]int                 `json:"test_count_by_definition_fallback_reason,omitempty"`
	TestDefinitionGitBranchName         string                         `json:"test_definition_git_branch_name,omitempty"`
	TestDefinitionGitCommitSHA          string                         `json:"test_definition_git_commit_sha,omitempty"`
	TestDefinitionMissingGitBranchName  string                         `json:"test_definition_missing_git_branch_name,omitempty"`
	TestDefinitionSummary               string                         `json:"test_definition_summary,omitempty"`
	TestDefinitionWarnings              []string                       `json:"test_definition_warnings,omitempty"`
	NextSteps                           []NextStep                     `json:"next_steps,omitempty"`

	BillingURL      string               `json:"billing_url,omitempty" jsonschema:"Billing link for a refused start. Stop and show the person the error and this link; when billing_action is add_card, recommend upgrading to Pro. Never retry in a loop or add a card yourself."`
	BillingAction   api.BillingAction    `json:"billing_action,omitempty" jsonschema:"What the refusal needs: add_card, top_up, update_card, raise_limit, request_access, or contact_sales"`
	UpgradeBenefits *api.UpgradeBenefits `json:"upgrade_benefits,omitempty" jsonschema:"Devices at once now and on Pro."`
	UsageNotice     *api.UsageNotice     `json:"billing_notice,omitempty" jsonschema:"Free minutes are nearly used. Finish this work, then recommend upgrading to Pro so the next run isn't cut off: share the notice's message, price and link. Never add a card yourself."`
	BillingNextStep string               `json:"billing_next_step,omitempty" jsonschema:"The line to follow when billing_url or billing_notice is set."`
}

func (s *Server) handleRunWorkflow(ctx context.Context, req *mcp.CallToolRequest, input RunWorkflowInput) (*mcp.CallToolResult, RunWorkflowOutput, error) {
	if input.WorkflowName == "" {
		return nil, RunWorkflowOutput{
			Success:      false,
			ErrorMessage: "workflow_name is required",
		}, nil
	}

	if mismatchMsg := s.orgMismatchMessageForProject(ctx, input.ProjectDir); mismatchMsg != "" {
		return nil, RunWorkflowOutput{Success: false, ErrorMessage: mismatchMsg}, nil
	}

	var lastStatus *sse.WorkflowStatus

	var onProgress func(status *sse.WorkflowStatus)
	progressToken := req.Params.GetProgressToken()
	fallbackNotices := &ui.BranchFallbackNotices{}
	onProgress = func(status *sse.WorkflowStatus) {
		lastStatus = status
		if progressToken != nil {
			msg := fmt.Sprintf("[%s] %d/%d tests completed (%d passed, %d failed)",
				status.Status, status.CompletedTests, status.TotalTests, status.PassedTests, status.FailedTests)
			if status.Duration != "" {
				msg += fmt.Sprintf(" (%s)", status.Duration)
			}
			for _, child := range status.ChildTasks {
				fallbackNotices.TestDefinitionGitBranchName = child.TestDefinitionGitBranchName
				if notice := fallbackNotices.SelectionNotice(child.AppID, child.AppName, child.BranchID, child.BranchName, child.GitBranchName, string(child.BranchSource), child.AutoGitBranchEnabled); notice != "" {
					msg += "\n" + notice
				}
			}
			_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
				ProgressToken: progressToken,
				Message:       msg,
				Progress:      float64(status.CompletedTests),
				Total:         float64(status.TotalTests),
			})
		}
	}

	workflowID, resolveErr := s.resolveWorkflowID(ctx, input.WorkflowName)
	if resolveErr != nil {
		return nil, RunWorkflowOutput{
			Success:      false,
			ErrorMessage: resolveErr.Error(),
		}, nil
	}

	wfParams := execution.RunWorkflowParams{
		WorkflowNameOrID:           workflowID,
		Retries:                    1,
		Timeout:                    execution.DefaultRunTimeoutSeconds,
		DevMode:                    s.devMode,
		OnProgress:                 onProgress,
		IOSAppID:                   input.IOSAppID,
		AndroidAppID:               input.AndroidAppID,
		BranchName:                 input.BranchName,
		BuildSelection:             input.BuildSelection,
		IOSBuildID:                 input.IOSBuildID,
		AndroidBuildID:             input.AndroidBuildID,
		LaunchVars:                 input.LaunchVars,
		LaunchArgSets:              input.LaunchArgSets,
		LaunchArguments:            input.LaunchArguments,
		DisableInheritedLaunchVars: input.DisableInheritedLaunchVars,
	}
	project, projectErr := s.resolveOptionalCanonicalProjectAt(input.ProjectDir)
	if projectErr != nil && input.FromCheckout != nil && *input.FromCheckout {
		return nil, RunWorkflowOutput{Success: false, ErrorMessage: projectErr.Error()}, nil
	}
	var checkoutUpload *checkoutdefinitions.Upload
	if runProject, err := checkoutdefinitions.RunProject(project, input.FromCheckout); err != nil {
		return nil, RunWorkflowOutput{Success: false, ErrorMessage: err.Error()}, nil
	} else if runProject != nil {
		workflowInfo, err := s.apiClient.GetWorkflowInfo(ctx, workflowID)
		if err != nil {
			return nil, RunWorkflowOutput{Success: false, ErrorMessage: fmt.Sprintf("read the workflow's tests: %v", err)}, nil
		}
		testIDs := make([]string, 0, len(workflowInfo.TestInfo))
		for _, test := range workflowInfo.TestInfo {
			testIDs = append(testIDs, test.ID)
		}
		if checkoutUpload, err = collectCheckoutUpload(ctx, runProject, testIDs); err != nil {
			return nil, RunWorkflowOutput{Success: false, ErrorMessage: err.Error()}, nil
		}
	}
	wfParams.TestDefinitionFiles = checkoutUpload.RequestFiles()
	if input.Location != "" {
		lat, lng, locErr := parseLocationString(input.Location)
		if locErr != nil {
			return nil, RunWorkflowOutput{Success: false, ErrorMessage: locErr.Error()}, nil
		}
		wfParams.Latitude = lat
		wfParams.Longitude = lng
		wfParams.HasLocation = true
	}

	if input.BranchName == "" && (input.IOSBuildID == "" || input.AndroidBuildID == "") &&
		api.BuildSelectionMode(input.BuildSelection) != api.BuildSelectionModeLatestAllBranches {
		gitBranch, err := s.detectGitBranch(ctx, input.ProjectDir)
		if err != nil {
			return nil, RunWorkflowOutput{Success: false, ErrorMessage: err.Error()}, nil
		}
		wfParams.GitBranchName = gitBranch
	} else if checkoutUpload != nil {
		wfParams.GitBranchName = checkoutUpload.GitBranchName
	}

	result, err := execution.RunWorkflow(ctx, s.apiClient.GetAPIKey(), nil, wfParams)
	if err != nil {
		return nil, RunWorkflowOutput{Success: false, ErrorMessage: err.Error()}, nil
	}

	viewerURL := fmt.Sprintf(
		"%s/workflows/report?taskId=%s",
		config.GetAppURL(s.devMode),
		url.QueryEscape(result.TaskID),
	)

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
		Tests:        result.Tests,

		TestCountByDefinitionSource:         testCountsByName(result.TestCountByDefinitionSource),
		TestCountByDefinitionFallbackReason: testCountsByName(result.TestCountByDefinitionFallbackReason),
		TestDefinitionGitBranchName:         result.TestDefinitionGitBranchName,
		TestDefinitionGitCommitSHA:          result.TestDefinitionGitCommitSHA,
		TestDefinitionMissingGitBranchName:  result.TestDefinitionMissingGitBranchName,
		TestDefinitionSummary:               testDefinitionSummary(checkoutUpload, result.TestCountByDefinitionSource, result.TestCountByDefinitionFallbackReason, result.TestDefinitionGitRead),
	}
	if projectErr != nil {
		out.TestDefinitionWarnings = append(out.TestDefinitionWarnings, fmt.Sprintf("Ran without this checkout's test files, because its .revyl/config.yaml can't be read: %v", projectErr))
	}
	if checkoutUpload != nil {
		out.TestDefinitionWarnings = append(out.TestDefinitionWarnings, checkoutUpload.Warnings...)
	}
	out.BillingURL, out.BillingAction, out.UsageNotice = result.BillingURL, result.BillingAction, result.UsageNotice
	out.UpgradeBenefits = result.UpgradeBenefits
	out.BillingNextStep = billingNextStep(out.BillingURL, out.BillingAction, out.UpgradeBenefits, out.UsageNotice)
	if lastStatus != nil {
		out.CompletedTests = lastStatus.CompletedTests
		out.CurrentTest = lastStatus.WorkflowName
	}

	switch {
	case result.Success:
		out.NextSteps = []NextStep{
			{Tool: "open_workflow_editor", Reason: "View detailed workflow report in browser"},
		}
	case result.Status == "cancelled" || result.Status == "timeout":
		out.NextSteps = []NextStep{
			{Tool: "run_workflow", Params: fmt.Sprintf("workflow_name=%s", input.WorkflowName), Reason: "Retry the workflow"},
		}
	default:
		out.NextSteps = []NextStep{
			{Tool: "run_workflow", Params: fmt.Sprintf("workflow_name=%s", input.WorkflowName), Reason: "Retry the workflow after investigating failures"},
		}
		if result.FailedTests > 0 {
			out.NextSteps = append([]NextStep{
				{Tool: "open_workflow_editor", Reason: fmt.Sprintf("View details for %d failed test(s)", result.FailedTests)},
			}, out.NextSteps...)
		}
	}

	if out.BillingURL != "" {
		out.NextSteps = nil
	}

	return nil, out, nil
}

type ListTestsInput struct {
	ProjectDir string `json:"project_dir,omitempty" jsonschema:"Path to project directory (defaults to current directory)"`
}

type TestInfo struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type WorkflowInfo struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type ListTestsOutput struct {
	Tests     []TestInfo     `json:"tests"`
	Workflows []WorkflowInfo `json:"workflows"`
	ConfigDir string         `json:"config_dir"`
}

func (s *Server) handleListTests(ctx context.Context, req *mcp.CallToolRequest, input ListTestsInput) (*mcp.CallToolResult, ListTestsOutput, error) {
	project, err := s.resolveCanonicalProject(input.ProjectDir)
	if err != nil {
		return nil, ListTestsOutput{Tests: []TestInfo{}, Workflows: []WorkflowInfo{}}, err
	}
	configPath := project.ConfigPath
	testsDir := project.TestsDir

	localTests, ltErr := config.LoadLocalTests(testsDir)
	if ltErr != nil {
		return nil, ListTestsOutput{
			Tests:     []TestInfo{},
			Workflows: []WorkflowInfo{},
			ConfigDir: configPath,
		}, nil
	}

	var tests []TestInfo
	for name, lt := range localTests {
		if lt != nil && lt.Meta.RemoteID != "" {
			tests = append(tests, TestInfo{Name: name, ID: lt.Meta.RemoteID})
		}
	}

	var workflows []WorkflowInfo
	if resp, err := s.apiClient.ListWorkflows(ctx); err == nil {
		for _, w := range resp.Workflows {
			workflows = append(workflows, WorkflowInfo{Name: w.Name, ID: w.ID})
		}
	}

	return nil, ListTestsOutput{
		Tests:     tests,
		Workflows: workflows,
		ConfigDir: configPath,
	}, nil
}

type GetTestStatusInput struct {
	TaskID string `json:"task_id" jsonschema:"The task ID of the test execution"`
}

type GetTestStatusOutput struct {
	Status         string `json:"status"`
	Progress       int    `json:"progress"`
	CurrentStep    string `json:"current_step,omitempty"`
	CompletedSteps int    `json:"completed_steps"`
	TotalSteps     int    `json:"total_steps"`
	Duration       string `json:"duration,omitempty"`
	ErrorMessage   string `json:"error_message,omitempty"`
}

func (s *Server) handleGetTestStatus(ctx context.Context, req *mcp.CallToolRequest, input GetTestStatusInput) (*mcp.CallToolResult, GetTestStatusOutput, error) {
	if input.TaskID == "" {
		return nil, GetTestStatusOutput{
			Status:       "error",
			ErrorMessage: "task_id is required",
		}, nil
	}

	status, err := s.apiClient.GetTestStatus(ctx, input.TaskID)
	if err != nil {
		return nil, GetTestStatusOutput{
			Status:       "error",
			ErrorMessage: fmt.Sprintf("failed to get test status: %v", err),
		}, nil
	}

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

type CreateTestInput struct {
	Name             string   `json:"name" jsonschema:"Test name"`
	Platform         string   `json:"platform" jsonschema:"Target platform (ios, android, or web)"`
	YAMLContent      string   `json:"yaml_content,omitempty" jsonschema:"Optional YAML test definition to create with real blocks"`
	AppID            string   `json:"app_id,omitempty" jsonschema:"Optional explicit app ID to associate with the test"`
	BranchID         string   `json:"branch_id,omitempty" jsonschema:"Saved Revyl branch ID; omit for Default branch (runs try the bound project's Git branch first only when automatic Git branch detection is enabled in the app's Build branches settings)"`
	BranchName       string   `json:"branch_name,omitempty" jsonschema:"Exact Revyl branch name to resolve and save as a stable ID"`
	BuildSelection   string   `json:"build_selection,omitempty" jsonschema:"Build choice: inherit (Automatic: the branch's default build), latest, latest_all_branches (the newest build across all of the app's branches; no branch), or pinned"`
	PinnedBuildID    string   `json:"pinned_build_id,omitempty" jsonschema:"Exact build ID to save; never falls back when unavailable"`
	ModuleNamesOrIDs []string `json:"module_names_or_ids,omitempty" jsonschema:"Optional ordered module names or IDs to prepend as module_import blocks"`
	Tags             []string `json:"tags,omitempty" jsonschema:"Optional tag names to assign to the test after creation"`
}

type CreateTestOutput struct {
	Success  bool   `json:"success"`
	TestID   string `json:"test_id,omitempty"`
	TestName string `json:"test_name,omitempty"`
	TestURL  string `json:"test_url,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (s *Server) handleCreateTest(ctx context.Context, req *mcp.CallToolRequest, input CreateTestInput) (*mcp.CallToolResult, CreateTestOutput, error) {
	if input.Name == "" {
		return nil, CreateTestOutput{
			Success: false,
			Error:   "name is required",
		}, nil
	}

	if input.Platform == "" {
		return nil, CreateTestOutput{
			Success: false,
			Error:   "platform is required (ios, android, or web)",
		}, nil
	}

	platform := strings.ToLower(input.Platform)
	if platform != "ios" && platform != "android" && platform != "web" {
		return nil, CreateTestOutput{
			Success: false,
			Error:   "platform must be 'ios', 'android', or 'web'",
		}, nil
	}
	if mismatchMsg := s.orgMismatchMessage(ctx); mismatchMsg != "" {
		return nil, CreateTestOutput{Success: false, Error: mismatchMsg}, nil
	}
	appID := strings.TrimSpace(input.AppID)
	projectContext := s.project
	if appID == "" {
		if projectContext == nil && !createYAMLHasBuildName(input.YAMLContent) {
			project, projectErr := s.resolveOptionalCanonicalProject()
			if projectErr != nil {
				if s.profile == ProfileCore || s.profile == ProfileFull {
					return nil, CreateTestOutput{}, projectErr
				}
				return nil, CreateTestOutput{Success: false, Error: projectErr.Error()}, nil
			}
			projectContext = project
		}
		var appErr error
		appID, appErr = canonicalAppIDForPlatform(projectContext, platform)
		if appErr != nil {
			return nil, CreateTestOutput{Success: false, Error: appErr.Error()}, nil
		}
	}

	result, err := execution.CreateTest(ctx, s.apiClient.GetAPIKey(), execution.CreateTestParams{
		Name:             strings.TrimSpace(input.Name),
		Platform:         platform,
		YAMLContent:      input.YAMLContent,
		AppID:            appID,
		BranchID:         input.BranchID,
		BranchName:       input.BranchName,
		BuildSelection:   input.BuildSelection,
		PinnedBuildID:    input.PinnedBuildID,
		ModuleNamesOrIDs: input.ModuleNamesOrIDs,
		Tags:             input.Tags,
		Config:           nil,
		DevMode:          false,
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

func createYAMLHasBuildName(content string) bool {
	var document struct {
		Test struct {
			Build struct {
				Name string `yaml:"name"`
			} `yaml:"build"`
		} `yaml:"test"`
	}
	if yamlPkg.Unmarshal([]byte(content), &document) != nil {
		return false
	}
	return strings.TrimSpace(document.Test.Build.Name) != ""
}

func canonicalAppIDForPlatform(project *config.ProjectContext, platform string) (string, error) {
	if project == nil || project.Authored == nil || project.Authored.Build == nil {
		return "", nil
	}
	appIDs := make(map[string]struct{})
	for _, profile := range project.Authored.Build.Profiles {
		var recipe *config.AuthoredBuildRecipe
		switch platform {
		case "ios":
			recipe = profile.IOS
		case "android":
			recipe = profile.Android
		case "web":
			recipe = (*config.AuthoredBuildRecipe)(profile.Web)
		}
		if recipe != nil && recipe.AppID != nil && strings.TrimSpace(*recipe.AppID) != "" {
			appIDs[strings.TrimSpace(*recipe.AppID)] = struct{}{}
		}
	}
	if len(appIDs) == 0 {
		return "", nil
	}
	if len(appIDs) > 1 {
		return "", fmt.Errorf("multiple %s apps are configured; provide app_id explicitly", platform)
	}
	for appID := range appIDs {
		return appID, nil
	}
	return "", nil
}

type CreateWorkflowInput struct {
	Name    string   `json:"name" jsonschema:"Workflow name"`
	TestIDs []string `json:"test_ids,omitempty" jsonschema:"Optional test IDs to include in workflow"`
}

type CreateWorkflowOutput struct {
	Success      bool   `json:"success"`
	WorkflowID   string `json:"workflow_id,omitempty"`
	WorkflowName string `json:"workflow_name,omitempty"`
	WorkflowURL  string `json:"workflow_url,omitempty"`
	Error        string `json:"error,omitempty"`
}

func (s *Server) handleCreateWorkflow(ctx context.Context, req *mcp.CallToolRequest, input CreateWorkflowInput) (*mcp.CallToolResult, CreateWorkflowOutput, error) {
	if input.Name == "" {
		return nil, CreateWorkflowOutput{
			Success: false,
			Error:   "name is required",
		}, nil
	}
	if mismatchMsg := s.orgMismatchMessage(ctx); mismatchMsg != "" {
		return nil, CreateWorkflowOutput{Success: false, Error: mismatchMsg}, nil
	}

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

type GetSchemaInput struct {
	Format string `json:"format,omitempty" jsonschema:"Output format: json (default), markdown, or llm"`
}

type GetSchemaOutput struct {
	CLISchema      map[string]any `json:"cli_schema,omitempty"`
	YAMLTestSchema map[string]any `json:"yaml_test_schema,omitempty"`
	Markdown       string         `json:"markdown,omitempty"`
	LLMFormat      string         `json:"llm_format,omitempty"`
}

func (s *Server) handleGetSchema(ctx context.Context, req *mcp.CallToolRequest, input GetSchemaInput) (*mcp.CallToolResult, GetSchemaOutput, error) {
	format := input.Format
	if format == "" {
		format = "json"
	}

	var cliSchema *schema.CLISchema
	if s.rootCmd != nil {
		cliSchema = schema.GetCLISchema(s.rootCmd, s.version)
	}
	cliSchemaOutput := make(map[string]any)
	if cliSchema != nil {
		encoded, err := json.Marshal(cliSchema)
		if err != nil {
			return nil, GetSchemaOutput{}, fmt.Errorf("encode CLI schema: %w", err)
		}
		if err := json.Unmarshal(encoded, &cliSchemaOutput); err != nil {
			return nil, GetSchemaOutput{}, fmt.Errorf("normalize CLI schema: %w", err)
		}
	}

	switch format {
	case "json":
		return nil, GetSchemaOutput{
			CLISchema:      cliSchemaOutput,
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
			CLISchema:      cliSchemaOutput,
			YAMLTestSchema: schema.YAMLTestSchemaJSON(),
		}, nil
	}
}

type ListBuildsInput struct {
	ProjectDir string `json:"project_dir,omitempty" jsonschema:"Customer project directory; retained for compatibility and does not narrow build browsing"`
	Platform   string `json:"platform,omitempty" jsonschema:"Filter app summaries by platform (ios, android, or web)"`
	Limit      int    `json:"limit,omitempty" jsonschema:"Maximum number of entries to return (default 20, maximum 100)"`
	AppID      string `json:"app_id,omitempty" jsonschema:"List actual build versions across all branches of this app instead of app summaries"`
	BranchID   string `json:"branch_id,omitempty" jsonschema:"Filter versions by this Revyl branch ID; requires app_id"`
	BranchName string `json:"branch_name,omitempty" jsonschema:"Filter versions by exact Revyl branch name; requires app_id; omitting both branch selectors includes all branches"`
	Page       int    `json:"page,omitempty" jsonschema:"Build version page (default 1)"`
}

type ListedBuildVersion struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	UploadedAt  string `json:"uploaded_at"`
	IsCurrent   bool   `json:"is_current"`
	BranchID    string `json:"branch_id,omitempty"`
	BranchName  string `json:"branch_name,omitempty"`
	PackageName string `json:"package_name,omitempty"`
	PackageID   string `json:"package_id,omitempty"`
}

type BuildInfo struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Platform       string `json:"platform"`
	CurrentVersion string `json:"current_version,omitempty"`
	VersionsCount  int    `json:"versions_count"`
}

type ListBuildsOutput struct {
	Builds       []BuildInfo          `json:"builds"`
	Total        int                  `json:"total"`
	ErrorMessage string               `json:"error_message,omitempty"`
	Versions     []ListedBuildVersion `json:"versions,omitempty"`
	AppID        string               `json:"app_id,omitempty"`
	BranchID     string               `json:"branch_id,omitempty"`
	BranchName   string               `json:"branch_name,omitempty"`
	Page         int                  `json:"page,omitempty"`
	HasNext      bool                 `json:"has_next,omitempty"`
	HasMore      bool                 `json:"has_more,omitempty"`
}

func (s *Server) handleListBuilds(ctx context.Context, req *mcp.CallToolRequest, input ListBuildsInput) (*mcp.CallToolResult, ListBuildsOutput, error) {
	limit := input.Limit
	if limit == 0 {
		limit = 20
	}
	failure := func(err error) (*mcp.CallToolResult, ListBuildsOutput, error) {
		return nil, ListBuildsOutput{Builds: []BuildInfo{}, ErrorMessage: err.Error()}, nil
	}
	if input.AppID == "" && (input.BranchID != "" || input.BranchName != "") {
		return failure(fmt.Errorf("app_id is required when selecting a branch"))
	}
	if input.AppID != "" {
		if len(input.AppID) != 36 || uuid.Validate(input.AppID) != nil {
			return failure(fmt.Errorf("app_id must be a hyphenated UUID"))
		}
		if limit < 1 || limit > 100 {
			return failure(fmt.Errorf("limit must be between 1 and 100"))
		}
		if input.Platform != "" {
			app, err := s.apiClient.GetApp(ctx, input.AppID)
			if err != nil {
				return failure(fmt.Errorf("failed to get app: %w", err))
			}
			if !strings.EqualFold(input.Platform, app.Platform) {
				return failure(fmt.Errorf("app platform does not match the requested platform"))
			}
		}
		branchID, branchName := "", ""
		if input.BranchID != "" || input.BranchName != "" {
			branch, err := s.apiClient.ResolveBranch(ctx, input.AppID, input.BranchID, input.BranchName)
			if err != nil {
				return failure(fmt.Errorf("failed to resolve branch: %w", err))
			}
			branchID, branchName = branch.ID, branch.Name
		}
		page, err := s.apiClient.ListBuildVersionsWithOptions(ctx, input.AppID, api.BuildListOptions{
			Page: input.Page, PageSize: limit, BranchID: branchID,
		})
		if err != nil {
			return failure(fmt.Errorf("failed to list build versions: %w", err))
		}
		versions := make([]ListedBuildVersion, 0, len(page.Items))
		for _, version := range page.Items {
			versions = append(versions, ListedBuildVersion{
				ID: version.ID, Version: version.Version, UploadedAt: version.UploadedAt, IsCurrent: version.IsCurrent,
				BranchID: version.BranchID, BranchName: version.BranchName,
				PackageName: version.PackageName, PackageID: version.PackageID,
			})
		}
		return nil, ListBuildsOutput{
			Builds: []BuildInfo{}, Versions: versions, Total: page.Total,
			AppID: input.AppID, BranchID: branchID, BranchName: branchName,
			Page: page.Page, HasNext: page.HasNext, HasMore: page.HasNext,
		}, nil
	}
	if limit > 100 {
		limit = 100
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

type OpenWorkflowEditorInput struct {
	WorkflowNameOrID string `json:"workflow_name_or_id" jsonschema:"Workflow name or UUID"`
}

type OpenWorkflowEditorOutput struct {
	Success     bool   `json:"success"`
	WorkflowID  string `json:"workflow_id"`
	WorkflowURL string `json:"workflow_url"`
	Error       string `json:"error,omitempty"`
}

func (s *Server) handleOpenWorkflowEditor(ctx context.Context, req *mcp.CallToolRequest, input OpenWorkflowEditorInput) (*mcp.CallToolResult, OpenWorkflowEditorOutput, error) {
	if input.WorkflowNameOrID == "" {
		return nil, OpenWorkflowEditorOutput{
			Success: false,
			Error:   "workflow_name_or_id is required",
		}, nil
	}

	result := execution.OpenWorkflowEditor(nil, execution.OpenWorkflowEditorParams{
		WorkflowNameOrID: input.WorkflowNameOrID,
		DevMode:          false,
	})

	return nil, OpenWorkflowEditorOutput{
		Success:     true,
		WorkflowID:  result.WorkflowID,
		WorkflowURL: result.WorkflowURL,
	}, nil
}

type CancelTestInput struct {
	TaskID string `json:"task_id" jsonschema:"The task ID of the running test execution to cancel"`
}

type CancelTestOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleCancelTest(ctx context.Context, req *mcp.CallToolRequest, input CancelTestInput) (*mcp.CallToolResult, CancelTestOutput, error) {
	if input.TaskID == "" {
		return nil, CancelTestOutput{Success: false, Error: "task_id is required"}, nil
	}
	if mismatchMsg := s.orgMismatchMessage(ctx); mismatchMsg != "" {
		return nil, CancelTestOutput{Success: false, Error: mismatchMsg}, nil
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

type CancelWorkflowInput struct {
	TaskID string `json:"task_id" jsonschema:"The task ID of the running workflow execution to cancel"`
}

type CancelWorkflowOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleCancelWorkflow(ctx context.Context, req *mcp.CallToolRequest, input CancelWorkflowInput) (*mcp.CallToolResult, CancelWorkflowOutput, error) {
	if input.TaskID == "" {
		return nil, CancelWorkflowOutput{Success: false, Error: "task_id is required"}, nil
	}
	if mismatchMsg := s.orgMismatchMessage(ctx); mismatchMsg != "" {
		return nil, CancelWorkflowOutput{Success: false, Error: mismatchMsg}, nil
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

type DeleteTestInput struct {
	TestNameOrID string `json:"test_name_or_id" jsonschema:"Test name (a .revyl/tests/ file name or a Revyl test name) or UUID"`
}

type DeleteTestOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleDeleteTest(ctx context.Context, req *mcp.CallToolRequest, input DeleteTestInput) (*mcp.CallToolResult, DeleteTestOutput, error) {
	if input.TestNameOrID == "" {
		return nil, DeleteTestOutput{Success: false, Error: "test_name_or_id is required"}, nil
	}
	if mismatchMsg := s.orgMismatchMessage(ctx); mismatchMsg != "" {
		return nil, DeleteTestOutput{Success: false, Error: mismatchMsg}, nil
	}

	testID := input.TestNameOrID
	id, resolutionErr := s.localTestRemoteID(input.TestNameOrID)
	if resolutionErr != nil {
		if setupErr := s.compositeProjectSetupError(resolutionErr); setupErr != nil {
			return nil, DeleteTestOutput{}, setupErr
		}
		return nil, DeleteTestOutput{Success: false, Error: resolutionErr.Error()}, nil
	}
	if id != "" {
		testID = id
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

type DeleteWorkflowInput struct {
	WorkflowNameOrID string `json:"workflow_name_or_id" jsonschema:"Workflow name or UUID"`
}

type DeleteWorkflowOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleDeleteWorkflow(ctx context.Context, req *mcp.CallToolRequest, input DeleteWorkflowInput) (*mcp.CallToolResult, DeleteWorkflowOutput, error) {
	if input.WorkflowNameOrID == "" {
		return nil, DeleteWorkflowOutput{Success: false, Error: "workflow_name_or_id is required"}, nil
	}
	if mismatchMsg := s.orgMismatchMessage(ctx); mismatchMsg != "" {
		return nil, DeleteWorkflowOutput{Success: false, Error: mismatchMsg}, nil
	}

	workflowID, err := s.resolveWorkflowID(ctx, input.WorkflowNameOrID)
	if err != nil {
		return nil, DeleteWorkflowOutput{Success: false, Error: err.Error()}, nil
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

type ListRemoteTestsInput struct {
	Limit  int `json:"limit,omitempty" jsonschema:"Maximum number of tests to return (default 50)"`
	Offset int `json:"offset,omitempty" jsonschema:"Offset for pagination (default 0)"`
}

type RemoteTestInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Status   string `json:"status,omitempty"`
}

type ListRemoteTestsOutput struct {
	Tests []RemoteTestInfo `json:"tests"`
	Total int              `json:"total"`
	Error string           `json:"error,omitempty"`
}

func (s *Server) handleListRemoteTests(ctx context.Context, req *mcp.CallToolRequest, input ListRemoteTestsInput) (*mcp.CallToolResult, ListRemoteTestsOutput, error) {
	limit := input.Limit
	if limit == 0 {
		limit = 50
	}

	resp, err := s.apiClient.ListOrgTests(ctx, limit, input.Offset)
	if err != nil {
		return nil, ListRemoteTestsOutput{}, fmt.Errorf("list remote tests: %w", err)
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

type ListWorkflowsInput struct{}

type ListWorkflowsOutput struct {
	Workflows []WorkflowInfo `json:"workflows"`
	Total     int            `json:"total"`
	Error     string         `json:"error,omitempty"`
}

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

type AuthStatusInput struct{}

type AuthStatusOutput struct {
	Authenticated bool   `json:"authenticated"`
	Email         string `json:"email,omitempty"`
	UserID        string `json:"user_id,omitempty"`
	OrgID         string `json:"org_id,omitempty"`
	OrgName       string `json:"org_name,omitempty"`
	AuthMethod    string `json:"auth_method,omitempty"`
}

func (s *Server) handleAuthStatus(ctx context.Context, req *mcp.CallToolRequest, input AuthStatusInput) (*mcp.CallToolResult, AuthStatusOutput, error) {
	mgr := auth.NewManager()
	creds, err := mgr.GetCredentials()
	if err != nil || creds == nil || !creds.HasValidAuth() {
		return nil, AuthStatusOutput{Authenticated: false}, nil
	}

	email := creds.Email
	userID := creds.UserID
	orgID := creds.OrgID
	orgName := ""

	if orgName == "" {
		token, _ := mgr.GetActiveToken()
		if token != "" {
			client := api.NewClientWithDevMode(token, s.devMode)
			enrichCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			if info, err := client.ValidateAPIKey(enrichCtx); err == nil {
				if info.OrgID != "" {
					orgID = info.OrgID
				}
				if info.OrgName != "" {
					orgName = info.OrgName
				}
				if email == "" {
					email = info.Email
				}
				if userID == "" {
					userID = info.UserID
				}
			}
		}
	}

	return nil, AuthStatusOutput{
		Authenticated: true,
		Email:         email,
		UserID:        userID,
		OrgID:         orgID,
		OrgName:       orgName,
		AuthMethod:    creds.AuthMethod,
	}, nil
}

type GetBillingStatusInput struct{}

func (s *Server) registerBillingStatusTool() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_billing_status",
		Description: "Read this organization's plan, card, free minutes, devices at once, usage, credit, auto-recharge, any monthly limit, rates, and billing link. Read-only.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Billing Status",
			ReadOnlyHint: true,
		},
	}, s.handleGetBillingStatus)
}

func billingNextStep(billingURL string, action api.BillingAction, benefits *api.UpgradeBenefits, notice *api.UsageNotice) string {
	switch {
	case billingURL != "":
		return billingstatus.RefusalAgentLine(action, benefits)
	case notice != nil:
		return billingstatus.UsageNoticeGuidance
	default:
		return ""
	}
}

func (s *Server) handleGetBillingStatus(ctx context.Context, req *mcp.CallToolRequest, input GetBillingStatusInput) (*mcp.CallToolResult, billingstatus.Status, error) {
	status, err := billingstatus.Compose(billingstatus.Read(ctx, s.apiClient), s.devMode)
	if err != nil {
		return nil, billingstatus.Status{}, err
	}
	return nil, status, nil
}

type CreateAppInput struct {
	Name     string `json:"name" jsonschema:"App name"`
	Platform string `json:"platform" jsonschema:"Target platform (ios, android, or web)"`
}

type CreateAppOutput struct {
	Success bool   `json:"success"`
	AppID   string `json:"app_id,omitempty"`
	AppName string `json:"app_name,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleCreateApp(ctx context.Context, req *mcp.CallToolRequest, input CreateAppInput) (*mcp.CallToolResult, CreateAppOutput, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, CreateAppOutput{Success: false, Error: "name is required"}, nil
	}

	platform := strings.ToLower(input.Platform)
	if platform != "ios" && platform != "android" && platform != "web" {
		return nil, CreateAppOutput{Success: false, Error: "platform must be 'ios', 'android', or 'web'"}, nil
	}

	resp, err := s.apiClient.CreateApp(ctx, &api.CreateAppRequest{
		Name:     name,
		Platform: platform,
	})
	if err != nil {
		if isAppAlreadyExistsError(err) {
			existing, findErr := findAppByName(ctx, s.apiClient, platform, name)
			if findErr != nil {
				return nil, CreateAppOutput{
					Success: false,
					Error:   fmt.Sprintf("app already exists but lookup failed: %v", findErr),
				}, nil
			}
			if existing != nil && existing.ID != "" {
				return nil, CreateAppOutput{
					Success: true,
					AppID:   existing.ID,
					AppName: existing.Name,
				}, nil
			}
		}
		return nil, CreateAppOutput{Success: false, Error: fmt.Sprintf("failed to create app: %v", err)}, nil
	}

	return nil, CreateAppOutput{
		Success: true,
		AppID:   resp.ID,
		AppName: resp.Name,
	}, nil
}

func findAppByName(ctx context.Context, client *api.Client, platform, name string) (*api.App, error) {
	target := canonicalizeAppName(name)
	if target == "" {
		return nil, nil
	}

	page := 1
	for {
		appsResp, err := client.ListApps(ctx, platform, page, 100)
		if err != nil {
			return nil, err
		}

		for _, app := range appsResp.Items {
			if canonicalizeAppName(app.Name) == target {
				matched := app
				return &matched, nil
			}
		}

		if !appsResp.HasNext {
			break
		}
		page++
	}
	return nil, nil
}

func canonicalizeAppName(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return ""
	}

	normalized := strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(lower)
	var b strings.Builder
	lastWasSpace := false
	for _, r := range normalized {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastWasSpace = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastWasSpace = false
		case r == ' ':
			if b.Len() > 0 && !lastWasSpace {
				b.WriteRune(' ')
				lastWasSpace = true
			}
		default:
			if b.Len() > 0 && !lastWasSpace {
				b.WriteRune(' ')
				lastWasSpace = true
			}
		}
	}

	return strings.TrimSpace(b.String())
}

func isAppAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		errText := strings.ToLower(apiErr.Error())
		if apiErr.StatusCode == 409 {
			return true
		}
		if apiErr.StatusCode == 500 && strings.Contains(errText, "already exists") {
			return true
		}
	}

	return strings.Contains(strings.ToLower(err.Error()), "already exists")
}

type DeleteAppInput struct {
	AppID string `json:"app_id" jsonschema:"The UUID of the app to delete"`
}

type DeleteAppOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

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

type ListModulesInput struct {
	NameFilter string `json:"name_filter,omitempty" jsonschema:"Optional filter to search modules by name"`
}

type ModuleInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	BlockCount  int    `json:"block_count"`
}

type ListModulesOutput struct {
	Modules []ModuleInfo `json:"modules"`
	Total   int          `json:"total"`
	Error   string       `json:"error,omitempty"`
}

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

type GetModuleInput struct {
	ModuleID string `json:"module_id" jsonschema:"The UUID of the module to retrieve"`
}

type GetModuleOutput struct {
	Success     bool          `json:"success"`
	ID          string        `json:"id,omitempty"`
	Name        string        `json:"name,omitempty"`
	Description string        `json:"description,omitempty"`
	Blocks      []interface{} `json:"blocks,omitempty"`
	Error       string        `json:"error,omitempty"`
}

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

type CreateModuleInput struct {
	Name        string        `json:"name" jsonschema:"Module name"`
	Description string        `json:"description,omitempty" jsonschema:"Optional module description"`
	Blocks      []interface{} `json:"blocks" jsonschema:"Array of test block objects"`
}

type CreateModuleOutput struct {
	Success  bool   `json:"success"`
	ModuleID string `json:"module_id,omitempty"`
	Name     string `json:"name,omitempty"`
	Error    string `json:"error,omitempty"`
}

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

type DeleteModuleInput struct {
	ModuleID string `json:"module_id" jsonschema:"The UUID of the module to delete"`
}

type DeleteModuleOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

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

type InsertModuleBlockInput struct {
	ModuleNameOrID string `json:"module_name_or_id" jsonschema:"Module name or UUID to generate the import block for"`
}

type InsertModuleBlockOutput struct {
	Success         bool   `json:"success"`
	YAMLSnippet     string `json:"yaml_snippet,omitempty"`
	ModuleID        string `json:"module_id,omitempty"`
	ModuleName      string `json:"module_name,omitempty"`
	BlockType       string `json:"block_type,omitempty"`
	StepDescription string `json:"step_description,omitempty"`
	Error           string `json:"error,omitempty"`
}

func (s *Server) handleInsertModuleBlock(ctx context.Context, req *mcp.CallToolRequest, input InsertModuleBlockInput) (*mcp.CallToolResult, InsertModuleBlockOutput, error) {
	if input.ModuleNameOrID == "" {
		return nil, InsertModuleBlockOutput{Success: false, Error: "module_name_or_id is required"}, nil
	}

	var moduleID, moduleName string

	if len(input.ModuleNameOrID) == 36 {
		resp, err := s.apiClient.GetModule(ctx, input.ModuleNameOrID)
		if err == nil {
			moduleID = resp.Result.ID
			moduleName = resp.Result.Name
		}
	}

	if moduleID == "" {
		listResp, err := s.apiClient.ListModules(ctx)
		if err != nil {
			return nil, InsertModuleBlockOutput{Success: false, Error: fmt.Sprintf("failed to list modules: %v", err)}, nil
		}

		needle := strings.TrimSpace(input.ModuleNameOrID)
		for _, m := range listResp.Result {
			if strings.TrimSpace(m.Name) == needle {
				moduleID = m.ID
				moduleName = m.Name
				break
			}
		}
	}

	if moduleID == "" {
		return nil, InsertModuleBlockOutput{Success: false, Error: fmt.Sprintf("module %q not found; use an exact module name or UUID", input.ModuleNameOrID)}, nil
	}

	yamlSnippet := fmt.Sprintf("- type: module_import\n  module: \"%s\"", moduleName)

	return nil, InsertModuleBlockOutput{
		Success:     true,
		YAMLSnippet: yamlSnippet,
		ModuleID:    moduleID,
		ModuleName:  moduleName,
		BlockType:   "module_import",
	}, nil
}

type ListTagsInput struct {
	NameFilter string `json:"name_filter,omitempty" jsonschema:"Optional filter to search tags by name"`
}

type TagInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color,omitempty"`
	Description string `json:"description,omitempty"`
	TestCount   int    `json:"test_count"`
}

type ListTagsOutput struct {
	Tags  []TagInfo `json:"tags"`
	Total int       `json:"total"`
	Error string    `json:"error,omitempty"`
}

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

type CreateTagInput struct {
	Name  string `json:"name" jsonschema:"Tag name"`
	Color string `json:"color,omitempty" jsonschema:"Tag color as hex string (e.g. #22C55E)"`
}

type CreateTagOutput struct {
	Success bool   `json:"success"`
	TagID   string `json:"tag_id,omitempty"`
	Name    string `json:"name,omitempty"`
	Color   string `json:"color,omitempty"`
	Error   string `json:"error,omitempty"`
}

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

type DeleteTagInput struct {
	TagNameOrID string `json:"tag_name_or_id" jsonschema:"Tag name or UUID to delete"`
}

type DeleteTagOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleDeleteTag(ctx context.Context, req *mcp.CallToolRequest, input DeleteTagInput) (*mcp.CallToolResult, DeleteTagOutput, error) {
	if input.TagNameOrID == "" {
		return nil, DeleteTagOutput{Success: false, Error: "tag_name_or_id is required"}, nil
	}

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

type GetTestTagsInput struct {
	TestNameOrID string `json:"test_name_or_id" jsonschema:"Test name (a .revyl/tests/ file name or a Revyl test name) or UUID"`
}

type GetTestTagsOutput struct {
	Success  bool      `json:"success"`
	TestID   string    `json:"test_id,omitempty"`
	TestName string    `json:"test_name,omitempty"`
	Tags     []TagInfo `json:"tags,omitempty"`
	Error    string    `json:"error,omitempty"`
}

func (s *Server) handleGetTestTags(ctx context.Context, req *mcp.CallToolRequest, input GetTestTagsInput) (*mcp.CallToolResult, GetTestTagsOutput, error) {
	if input.TestNameOrID == "" {
		return nil, GetTestTagsOutput{Success: false, Error: "test_name_or_id is required"}, nil
	}

	testID := input.TestNameOrID
	testName := input.TestNameOrID
	id, resolutionErr := s.localTestRemoteID(input.TestNameOrID)
	if resolutionErr != nil {
		if setupErr := s.compositeProjectSetupError(resolutionErr); setupErr != nil {
			return nil, GetTestTagsOutput{}, setupErr
		}
		return nil, GetTestTagsOutput{Success: false, Error: resolutionErr.Error()}, nil
	}
	if id != "" {
		testID = id
	}

	if len(testID) != 36 {
		testsResp, err := s.apiClient.ListOrgTests(ctx, 100, 0)
		if err != nil {
			return nil, GetTestTagsOutput{}, fmt.Errorf(
				"look up test %q: %w", input.TestNameOrID, err,
			)
		}
		for _, t := range testsResp.Tests {
			if t.Name == input.TestNameOrID {
				testID = t.ID
				testName = t.Name
				break
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

type SetTestTagsInput struct {
	TestNameOrID string   `json:"test_name_or_id" jsonschema:"Test name (a .revyl/tests/ file name or a Revyl test name) or UUID"`
	TagNames     []string `json:"tag_names" jsonschema:"Tag names to set on the test (replaces all existing tags)"`
}

type SetTestTagsOutput struct {
	Success bool      `json:"success"`
	TestID  string    `json:"test_id,omitempty"`
	Tags    []TagInfo `json:"tags,omitempty"`
	Error   string    `json:"error,omitempty"`
}

func (s *Server) handleSetTestTags(ctx context.Context, req *mcp.CallToolRequest, input SetTestTagsInput) (*mcp.CallToolResult, SetTestTagsOutput, error) {
	if input.TestNameOrID == "" {
		return nil, SetTestTagsOutput{Success: false, Error: "test_name_or_id is required"}, nil
	}

	if len(input.TagNames) == 0 {
		return nil, SetTestTagsOutput{Success: false, Error: "tag_names is required and must not be empty"}, nil
	}
	if mismatchMsg := s.orgMismatchMessage(ctx); mismatchMsg != "" {
		return nil, SetTestTagsOutput{Success: false, Error: mismatchMsg}, nil
	}

	testID := input.TestNameOrID
	id, resolutionErr := s.localTestRemoteID(input.TestNameOrID)
	if resolutionErr != nil {
		if setupErr := s.compositeProjectSetupError(resolutionErr); setupErr != nil {
			return nil, SetTestTagsOutput{}, setupErr
		}
		return nil, SetTestTagsOutput{Success: false, Error: resolutionErr.Error()}, nil
	}
	if id != "" {
		testID = id
	}

	if len(testID) != 36 {
		testsResp, err := s.apiClient.ListOrgTests(ctx, 100, 0)
		if err != nil {
			return nil, SetTestTagsOutput{}, fmt.Errorf(
				"look up test %q: %w", input.TestNameOrID, err,
			)
		}
		for _, t := range testsResp.Tests {
			if t.Name == input.TestNameOrID {
				testID = t.ID
				break
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

type AddRemoveTestTagsInput struct {
	TestNameOrID string   `json:"test_name_or_id" jsonschema:"Test name (a .revyl/tests/ file name or a Revyl test name) or UUID"`
	TagsToAdd    []string `json:"tags_to_add,omitempty" jsonschema:"Tag names to add to the test"`
	TagsToRemove []string `json:"tags_to_remove,omitempty" jsonschema:"Tag names to remove from the test"`
}

type AddRemoveTestTagsOutput struct {
	Success bool   `json:"success"`
	TestID  string `json:"test_id,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleAddRemoveTestTags(ctx context.Context, req *mcp.CallToolRequest, input AddRemoveTestTagsInput) (*mcp.CallToolResult, AddRemoveTestTagsOutput, error) {
	if input.TestNameOrID == "" {
		return nil, AddRemoveTestTagsOutput{Success: false, Error: "test_name_or_id is required"}, nil
	}

	if len(input.TagsToAdd) == 0 && len(input.TagsToRemove) == 0 {
		return nil, AddRemoveTestTagsOutput{Success: false, Error: "at least one of tags_to_add or tags_to_remove is required"}, nil
	}
	if mismatchMsg := s.orgMismatchMessage(ctx); mismatchMsg != "" {
		return nil, AddRemoveTestTagsOutput{Success: false, Error: mismatchMsg}, nil
	}

	testID := input.TestNameOrID
	id, resolutionErr := s.localTestRemoteID(input.TestNameOrID)
	if resolutionErr != nil {
		if setupErr := s.compositeProjectSetupError(resolutionErr); setupErr != nil {
			return nil, AddRemoveTestTagsOutput{}, setupErr
		}
		return nil, AddRemoveTestTagsOutput{Success: false, Error: resolutionErr.Error()}, nil
	}
	if id != "" {
		testID = id
	}

	if len(testID) != 36 {
		testsResp, err := s.apiClient.ListOrgTests(ctx, 100, 0)
		if err != nil {
			return nil, AddRemoveTestTagsOutput{}, fmt.Errorf(
				"look up test %q: %w", input.TestNameOrID, err,
			)
		}
		for _, t := range testsResp.Tests {
			if t.Name == input.TestNameOrID {
				testID = t.ID
				break
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

type ListFilesInput struct {
	Limit  int `json:"limit,omitempty" jsonschema:"Max results (1-1000, default 100)"`
	Offset int `json:"offset,omitempty" jsonschema:"Pagination offset (default 0)"`
}

type MCPFileInfo struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	FileSize    int64  `json:"file_size"`
	ContentType string `json:"content_type,omitempty"`
	Description string `json:"description,omitempty"`
	CreatedAt   string `json:"created_at"`
}

type ListFilesOutput struct {
	Files []MCPFileInfo `json:"files"`
	Count int           `json:"count"`
	Error string        `json:"error,omitempty"`
}

func (s *Server) handleListFiles(ctx context.Context, req *mcp.CallToolRequest, input ListFilesInput) (*mcp.CallToolResult, ListFilesOutput, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = 100
	}

	resp, err := s.apiClient.ListOrgFiles(ctx, limit, input.Offset)
	if err != nil {
		return nil, ListFilesOutput{Files: []MCPFileInfo{}, Error: fmt.Sprintf("failed to list files: %v", err)}, nil
	}

	files := make([]MCPFileInfo, 0, len(resp.Files))
	for _, f := range resp.Files {
		files = append(files, MCPFileInfo{
			ID:          f.ID,
			Filename:    f.Filename,
			FileSize:    f.FileSize,
			ContentType: f.ContentType,
			Description: f.Description,
			CreatedAt:   f.CreatedAt,
		})
	}

	return nil, ListFilesOutput{Files: files, Count: resp.Count}, nil
}

type UploadFileInput struct {
	FilePath    string `json:"file_path" jsonschema:"Absolute path to the local file to upload"`
	DisplayName string `json:"display_name,omitempty" jsonschema:"Display name (defaults to filename)"`
	Description string `json:"description,omitempty" jsonschema:"Optional file description"`
}

type UploadFileOutput struct {
	Success  bool   `json:"success"`
	FileID   string `json:"file_id,omitempty"`
	Filename string `json:"filename,omitempty"`
	FileSize int64  `json:"file_size,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (s *Server) handleUploadFile(ctx context.Context, req *mcp.CallToolRequest, input UploadFileInput) (*mcp.CallToolResult, UploadFileOutput, error) {
	if input.FilePath == "" {
		return nil, UploadFileOutput{Success: false, Error: "file_path is required"}, nil
	}

	info, err := os.Stat(input.FilePath)
	if err != nil {
		return nil, UploadFileOutput{Success: false, Error: fmt.Sprintf("file not found: %v", err)}, nil
	}
	if info.IsDir() {
		return nil, UploadFileOutput{Success: false, Error: "file_path must be a file, not a directory"}, nil
	}

	resp, err := s.apiClient.UploadOrgFile(ctx, input.FilePath, input.DisplayName, input.Description)
	if err != nil {
		return nil, UploadFileOutput{Success: false, Error: fmt.Sprintf("upload failed: %v", err)}, nil
	}

	return nil, UploadFileOutput{
		Success:  true,
		FileID:   resp.ID,
		Filename: resp.Filename,
		FileSize: resp.FileSize,
	}, nil
}

type DownloadFileInput struct {
	FileID   string `json:"file_id" jsonschema:"File ID to download"`
	DestPath string `json:"dest_path,omitempty" jsonschema:"Local path to save the file (defaults to current directory + original filename)"`
}

type DownloadFileOutput struct {
	Success  bool   `json:"success"`
	FilePath string `json:"file_path,omitempty"`
	Filename string `json:"filename,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (s *Server) handleDownloadFile(ctx context.Context, req *mcp.CallToolRequest, input DownloadFileInput) (*mcp.CallToolResult, DownloadFileOutput, error) {
	if input.FileID == "" {
		return nil, DownloadFileOutput{Success: false, Error: "file_id is required"}, nil
	}

	dlResp, err := s.apiClient.GetOrgFileDownloadURL(ctx, input.FileID)
	if err != nil {
		return nil, DownloadFileOutput{Success: false, Error: fmt.Sprintf("failed to get download URL: %v", err)}, nil
	}

	destPath := input.DestPath
	if destPath == "" {
		destPath = filepath.Join(s.workDir, dlResp.Filename)
	} else {
		info, statErr := os.Stat(destPath)
		if statErr == nil && info.IsDir() {
			destPath = filepath.Join(destPath, dlResp.Filename)
		}
	}

	if err := s.apiClient.DownloadFileFromURL(ctx, dlResp.URL, destPath); err != nil {
		return nil, DownloadFileOutput{Success: false, Error: fmt.Sprintf("download failed: %v", err)}, nil
	}

	return nil, DownloadFileOutput{
		Success:  true,
		FilePath: destPath,
		Filename: dlResp.Filename,
	}, nil
}

type GetFileDownloadURLInput struct {
	FileID string `json:"file_id" jsonschema:"File ID"`
}

type GetFileDownloadURLOutput struct {
	Success   bool   `json:"success"`
	URL       string `json:"url,omitempty"`
	Filename  string `json:"filename,omitempty"`
	ExpiresIn int    `json:"expires_in,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (s *Server) handleGetFileDownloadURL(ctx context.Context, req *mcp.CallToolRequest, input GetFileDownloadURLInput) (*mcp.CallToolResult, GetFileDownloadURLOutput, error) {
	if input.FileID == "" {
		return nil, GetFileDownloadURLOutput{Success: false, Error: "file_id is required"}, nil
	}

	resp, err := s.apiClient.GetOrgFileDownloadURL(ctx, input.FileID)
	if err != nil {
		return nil, GetFileDownloadURLOutput{Success: false, Error: fmt.Sprintf("failed to get download URL: %v", err)}, nil
	}

	return nil, GetFileDownloadURLOutput{
		Success:   true,
		URL:       resp.URL,
		Filename:  resp.Filename,
		ExpiresIn: resp.ExpiresIn,
	}, nil
}

type EditFileInput struct {
	FileID      string  `json:"file_id" jsonschema:"File ID to edit"`
	Filename    string  `json:"filename,omitempty" jsonschema:"New filename"`
	Description *string `json:"description,omitempty" jsonschema:"New description (empty string clears it)"`
	FilePath    string  `json:"file_path,omitempty" jsonschema:"Path to replacement file (replaces content, preserves ID)"`
}

type EditFileOutput struct {
	Success  bool   `json:"success"`
	FileID   string `json:"file_id,omitempty"`
	Filename string `json:"filename,omitempty"`
	FileSize int64  `json:"file_size,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (s *Server) handleEditFile(ctx context.Context, req *mcp.CallToolRequest, input EditFileInput) (*mcp.CallToolResult, EditFileOutput, error) {
	if input.FileID == "" {
		return nil, EditFileOutput{Success: false, Error: "file_id is required"}, nil
	}

	hasMetadata := input.Filename != "" || input.Description != nil
	hasFile := input.FilePath != ""

	if !hasMetadata && !hasFile {
		return nil, EditFileOutput{Success: false, Error: "specify at least filename, description, or file_path"}, nil
	}

	if hasFile {
		info, err := os.Stat(input.FilePath)
		if err != nil {
			return nil, EditFileOutput{Success: false, Error: fmt.Sprintf("file not found: %v", err)}, nil
		}
		if info.IsDir() {
			return nil, EditFileOutput{Success: false, Error: "file_path must be a file, not a directory"}, nil
		}

		desc := ""
		if input.Description != nil {
			desc = *input.Description
		}
		resp, err := s.apiClient.ReplaceOrgFileContent(ctx, input.FileID, input.FilePath, input.Filename, desc)
		if err != nil {
			return nil, EditFileOutput{Success: false, Error: fmt.Sprintf("replace failed: %v", err)}, nil
		}

		return nil, EditFileOutput{
			Success:  true,
			FileID:   resp.ID,
			Filename: resp.Filename,
			FileSize: resp.FileSize,
		}, nil
	}

	updateReq := &api.CLIOrgFileUpdateRequest{}
	if input.Filename != "" {
		updateReq.Filename = &input.Filename
	}
	if input.Description != nil {
		updateReq.Description = input.Description
	}

	resp, err := s.apiClient.UpdateOrgFile(ctx, input.FileID, updateReq)
	if err != nil {
		return nil, EditFileOutput{Success: false, Error: fmt.Sprintf("update failed: %v", err)}, nil
	}

	return nil, EditFileOutput{
		Success:  true,
		FileID:   resp.ID,
		Filename: resp.Filename,
		FileSize: resp.FileSize,
	}, nil
}

type DeleteFileInput struct {
	FileID string `json:"file_id" jsonschema:"File ID to delete"`
}

type DeleteFileOutput struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleDeleteFile(ctx context.Context, req *mcp.CallToolRequest, input DeleteFileInput) (*mcp.CallToolResult, DeleteFileOutput, error) {
	if input.FileID == "" {
		return nil, DeleteFileOutput{Success: false, Error: "file_id is required"}, nil
	}

	if err := s.apiClient.DeleteOrgFile(ctx, input.FileID); err != nil {
		return nil, DeleteFileOutput{Success: false, Error: fmt.Sprintf("failed to delete file: %v", err)}, nil
	}

	return nil, DeleteFileOutput{Success: true}, nil
}

// orgMismatchMessage returns a standardized mismatch message when the project
// org binding differs from the authenticated org. Returns empty string when the
// mismatch guard is not active.
func (s *Server) orgMismatchMessage(ctx context.Context) string {
	return s.orgMismatchMessageForProject(ctx, "")
}

func (s *Server) orgMismatchMessageForProject(ctx context.Context, projectDirectory string) string {
	result := orgguard.Check(ctx, s.toolProjectDirectory(projectDirectory), s.devMode)
	if result == nil || result.Mismatch == nil {
		return ""
	}
	return result.Mismatch.UserMessage()
}

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

func (s *Server) resolveTestID(ctx context.Context, nameOrID string) (string, error) {
	testID := nameOrID
	id, resolutionErr := s.localTestRemoteID(nameOrID)
	if resolutionErr != nil {
		return "", resolutionErr
	}
	if id != "" {
		testID = id
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

func (s *Server) localTestRemoteID(name string) (string, error) {
	if strings.TrimSpace(name) == "" || looksLikeMCPUUID(name) {
		return "", nil
	}
	project := s.project
	if project == nil {
		resolved, err := s.resolveOptionalCanonicalProject()
		if err != nil {
			return "", err
		}
		if resolved == nil {
			return "", nil
		}
		project = resolved
	}
	id, err := config.GetLocalTestRemoteID(project.TestsDir, name)
	if err != nil {
		return "", nil
	}
	return id, nil
}

func looksLikeMCPUUID(value string) bool {
	return len(strings.TrimSpace(value)) == 36
}

func optionalMCPProjectError(err error, workDir string) bool {
	var configErr *config.ConfigError
	if !errors.As(err, &configErr) {
		return false
	}
	if configErr.Code == "config_not_found" {
		return true
	}
	if configErr.Code != "git_worktree_unavailable" {
		return false
	}
	return localProjectConfigAbsent(workDir)
}

func localProjectConfigAbsent(workDir string) bool {
	_, statErr := os.Lstat(filepath.Join(workDir, ".revyl", "config.yaml"))
	return errors.Is(statErr, os.ErrNotExist)
}

func (s *Server) compositeProjectSetupError(err error) error {
	if s.profile != ProfileCore && s.profile != ProfileFull {
		return nil
	}
	var setupErr *projectSetupError
	if errors.As(err, &setupErr) {
		return err
	}
	return nil
}

func (s *Server) resolveWorkflowID(ctx context.Context, nameOrID string) (string, error) {
	resolved, err := workflowref.Resolve(ctx, s.apiClient, nameOrID)
	if err != nil {
		return "", err
	}
	return resolved.ID, nil
}

type ListVariablesInput struct {
	TestNameOrID string `json:"test_name_or_id" jsonschema:"Test name (a .revyl/tests/ file name or a Revyl test name) or UUID"`
}

type VariableInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

type ListVariablesOutput struct {
	Success   bool           `json:"success"`
	TestID    string         `json:"test_id,omitempty"`
	Variables []VariableInfo `json:"variables,omitempty"`
	Error     string         `json:"error,omitempty"`
}

func (s *Server) handleListVariables(ctx context.Context, req *mcp.CallToolRequest, input ListVariablesInput) (*mcp.CallToolResult, ListVariablesOutput, error) {
	if input.TestNameOrID == "" {
		return nil, ListVariablesOutput{Success: false, Error: "test_name_or_id is required"}, nil
	}

	testID, err := s.resolveTestID(ctx, input.TestNameOrID)
	if err != nil {
		if setupErr := s.compositeProjectSetupError(err); setupErr != nil {
			return nil, ListVariablesOutput{}, setupErr
		}
		return nil, ListVariablesOutput{Success: false, Error: err.Error()}, nil
	}

	resp, err := s.apiClient.ListCustomVariables(ctx, testID)
	if err != nil {
		return nil, ListVariablesOutput{Success: false, Error: fmt.Sprintf("failed to list variables: %v", err)}, nil
	}

	var variables []VariableInfo
	for _, v := range resp.Result {
		variables = append(variables, VariableInfo{
			ID:    v.ID,
			Name:  v.VariableName,
			Value: v.VariableValue,
		})
	}
	if variables == nil {
		variables = []VariableInfo{}
	}

	return nil, ListVariablesOutput{Success: true, TestID: testID, Variables: variables}, nil
}

type SetVariableInput struct {
	TestNameOrID string `json:"test_name_or_id" jsonschema:"Test name (a .revyl/tests/ file name or a Revyl test name) or UUID"`
	Name         string `json:"name" jsonschema:"Variable name (letters, numbers, hyphens, or underscores; no spaces)"`
	Value        string `json:"value,omitempty" jsonschema:"Variable value (optional, defaults to empty string)"`
}

type SetVariableOutput struct {
	Success bool   `json:"success"`
	Action  string `json:"action,omitempty"`
	Name    string `json:"name,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleSetVariable(ctx context.Context, req *mcp.CallToolRequest, input SetVariableInput) (*mcp.CallToolResult, SetVariableOutput, error) {
	if input.TestNameOrID == "" || input.Name == "" {
		return nil, SetVariableOutput{Success: false, Error: "test_name_or_id and name are required"}, nil
	}
	if mismatchMsg := s.orgMismatchMessage(ctx); mismatchMsg != "" {
		return nil, SetVariableOutput{Success: false, Error: mismatchMsg}, nil
	}

	if !isValidVariableName(input.Name) {
		return nil, SetVariableOutput{
			Success: false,
			Error:   fmt.Sprintf("invalid variable name '%s': must use letters, numbers, hyphens, or underscores (no leading/trailing/consecutive separators or spaces)", input.Name),
		}, nil
	}

	testID, err := s.resolveTestID(ctx, input.TestNameOrID)
	if err != nil {
		if setupErr := s.compositeProjectSetupError(err); setupErr != nil {
			return nil, SetVariableOutput{}, setupErr
		}
		return nil, SetVariableOutput{Success: false, Error: err.Error()}, nil
	}

	existing, err := s.apiClient.ListCustomVariables(ctx, testID)
	if err != nil {
		return nil, SetVariableOutput{Success: false, Error: fmt.Sprintf("failed to check existing variables: %v", err)}, nil
	}

	for _, v := range existing.Result {
		if v.VariableName == input.Name {
			err = s.apiClient.UpdateCustomVariableValue(ctx, testID, v.ID, input.Value)
			if err != nil {
				return nil, SetVariableOutput{Success: false, Error: fmt.Sprintf("failed to update variable: %v", err)}, nil
			}
			return nil, SetVariableOutput{Success: true, Action: "updated", Name: input.Name}, nil
		}
	}

	_, err = s.apiClient.AddCustomVariable(ctx, testID, input.Name, input.Value)
	if err != nil {
		return nil, SetVariableOutput{Success: false, Error: fmt.Sprintf("failed to add variable: %v", err)}, nil
	}
	return nil, SetVariableOutput{Success: true, Action: "added", Name: input.Name}, nil
}

type DeleteVariableInput struct {
	TestNameOrID string `json:"test_name_or_id" jsonschema:"Test name (a .revyl/tests/ file name or a Revyl test name) or UUID"`
	Name         string `json:"name" jsonschema:"Variable name to delete"`
}

type DeleteVariableOutput struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleDeleteVariable(ctx context.Context, req *mcp.CallToolRequest, input DeleteVariableInput) (*mcp.CallToolResult, DeleteVariableOutput, error) {
	if input.TestNameOrID == "" || input.Name == "" {
		return nil, DeleteVariableOutput{Success: false, Error: "test_name_or_id and name are required"}, nil
	}
	if mismatchMsg := s.orgMismatchMessage(ctx); mismatchMsg != "" {
		return nil, DeleteVariableOutput{Success: false, Error: mismatchMsg}, nil
	}

	testID, err := s.resolveTestID(ctx, input.TestNameOrID)
	if err != nil {
		if setupErr := s.compositeProjectSetupError(err); setupErr != nil {
			return nil, DeleteVariableOutput{}, setupErr
		}
		return nil, DeleteVariableOutput{Success: false, Error: err.Error()}, nil
	}

	err = s.apiClient.DeleteCustomVariable(ctx, testID, input.Name)
	if err != nil {
		return nil, DeleteVariableOutput{Success: false, Error: fmt.Sprintf("failed to delete variable: %v", err)}, nil
	}

	return nil, DeleteVariableOutput{Success: true}, nil
}

type DeleteAllVariablesInput struct {
	TestNameOrID string `json:"test_name_or_id" jsonschema:"Test name (a .revyl/tests/ file name or a Revyl test name) or UUID"`
}

type DeleteAllVariablesOutput struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleDeleteAllVariables(ctx context.Context, req *mcp.CallToolRequest, input DeleteAllVariablesInput) (*mcp.CallToolResult, DeleteAllVariablesOutput, error) {
	if input.TestNameOrID == "" {
		return nil, DeleteAllVariablesOutput{Success: false, Error: "test_name_or_id is required"}, nil
	}
	if mismatchMsg := s.orgMismatchMessage(ctx); mismatchMsg != "" {
		return nil, DeleteAllVariablesOutput{Success: false, Error: mismatchMsg}, nil
	}

	testID, err := s.resolveTestID(ctx, input.TestNameOrID)
	if err != nil {
		if setupErr := s.compositeProjectSetupError(err); setupErr != nil {
			return nil, DeleteAllVariablesOutput{}, setupErr
		}
		return nil, DeleteAllVariablesOutput{Success: false, Error: err.Error()}, nil
	}

	err = s.apiClient.DeleteAllCustomVariables(ctx, testID)
	if err != nil {
		return nil, DeleteAllVariablesOutput{Success: false, Error: fmt.Sprintf("failed to delete all variables: %v", err)}, nil
	}

	return nil, DeleteAllVariablesOutput{Success: true}, nil
}

func isValidVariableName(name string) bool {
	if name == "" {
		return false
	}
	if name[0] == '-' || name[0] == '_' || name[len(name)-1] == '-' || name[len(name)-1] == '_' {
		return false
	}
	for i, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
		if (c == '-' || c == '_') && i > 0 && (name[i-1] == '-' || name[i-1] == '_') {
			return false
		}
	}
	return true
}

type GetWorkflowSettingsInput struct {
	WorkflowNameOrID string `json:"workflow_name_or_id" jsonschema:"Workflow name or UUID"`
}

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

type SetWorkflowLocationInput struct {
	WorkflowNameOrID string  `json:"workflow_name_or_id" jsonschema:"Workflow name or UUID"`
	Latitude         float64 `json:"latitude" jsonschema:"Latitude (-90 to 90)"`
	Longitude        float64 `json:"longitude" jsonschema:"Longitude (-180 to 180)"`
}

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
	if mismatchMsg := s.orgMismatchMessage(ctx); mismatchMsg != "" {
		return nil, SimpleSuccessOutput{Success: false, Error: mismatchMsg}, nil
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

type ClearWorkflowLocationInput struct {
	WorkflowNameOrID string `json:"workflow_name_or_id" jsonschema:"Workflow name or UUID"`
}

func (s *Server) handleClearWorkflowLocation(ctx context.Context, req *mcp.CallToolRequest, input ClearWorkflowLocationInput) (*mcp.CallToolResult, SimpleSuccessOutput, error) {
	if input.WorkflowNameOrID == "" {
		return nil, SimpleSuccessOutput{Success: false, Error: "workflow_name_or_id is required"}, nil
	}
	if mismatchMsg := s.orgMismatchMessage(ctx); mismatchMsg != "" {
		return nil, SimpleSuccessOutput{Success: false, Error: mismatchMsg}, nil
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

type SetWorkflowAppInput struct {
	WorkflowNameOrID      string `json:"workflow_name_or_id" jsonschema:"Workflow name or UUID"`
	IOSAppID              string `json:"ios_app_id,omitempty" jsonschema:"iOS app ID to override"`
	AndroidAppID          string `json:"android_app_id,omitempty" jsonschema:"Android app ID to override"`
	IOSBranchID           string `json:"ios_branch_id,omitempty" jsonschema:"Saved iOS Revyl branch ID"`
	IOSBranchName         string `json:"ios_branch_name,omitempty" jsonschema:"Exact iOS Revyl branch name to resolve and save"`
	IOSBuildSelection     string `json:"ios_build_selection,omitempty" jsonschema:"iOS build choice: inherit (Automatic: the branch's default build), latest, latest_all_branches (the newest build across all of the app's branches; no branch), or pinned; inherit alone resets to Default branch"`
	IOSPinnedBuildID      string `json:"ios_pinned_build_id,omitempty" jsonschema:"Exact iOS build ID to save"`
	AndroidBranchID       string `json:"android_branch_id,omitempty" jsonschema:"Saved Android Revyl branch ID"`
	AndroidBranchName     string `json:"android_branch_name,omitempty" jsonschema:"Exact Android Revyl branch name to resolve and save"`
	AndroidBuildSelection string `json:"android_build_selection,omitempty" jsonschema:"Android build choice: inherit (Automatic: the branch's default build), latest, latest_all_branches (the newest build across all of the app's branches; no branch), or pinned; inherit alone resets to Default branch"`
	AndroidPinnedBuildID  string `json:"android_pinned_build_id,omitempty" jsonschema:"Exact Android build ID to save"`
}

func (s *Server) handleSetWorkflowApp(ctx context.Context, req *mcp.CallToolRequest, input SetWorkflowAppInput) (*mcp.CallToolResult, SimpleSuccessOutput, error) {
	if input.WorkflowNameOrID == "" {
		return nil, SimpleSuccessOutput{Success: false, Error: "workflow_name_or_id is required"}, nil
	}
	if input.IOSAppID == "" && input.AndroidAppID == "" {
		return nil, SimpleSuccessOutput{Success: false, Error: "at least one of ios_app_id or android_app_id is required"}, nil
	}
	if mismatchMsg := s.orgMismatchMessage(ctx); mismatchMsg != "" {
		return nil, SimpleSuccessOutput{Success: false, Error: mismatchMsg}, nil
	}

	wfID, err := s.resolveWorkflowID(ctx, input.WorkflowNameOrID)
	if err != nil {
		return nil, SimpleSuccessOutput{Success: false, Error: err.Error()}, nil
	}

	if input.IOSAppID == "" && (input.IOSBranchID != "" || input.IOSBranchName != "" || input.IOSBuildSelection != "" || input.IOSPinnedBuildID != "") {
		return nil, SimpleSuccessOutput{Error: "ios_app_id is required for an iOS build selection"}, nil
	}
	if input.AndroidAppID == "" && (input.AndroidBranchID != "" || input.AndroidBranchName != "" || input.AndroidBuildSelection != "" || input.AndroidPinnedBuildID != "") {
		return nil, SimpleSuccessOutput{Error: "android_app_id is required for an Android build selection"}, nil
	}
	wf, err := s.apiClient.GetWorkflow(ctx, wfID)
	if err != nil {
		return nil, SimpleSuccessOutput{Error: fmt.Sprintf("failed to read existing app config: %v", err)}, nil
	}
	buildConfig := wf.BuildConfig
	if buildConfig == nil {
		buildConfig = map[string]interface{}{}
	}
	for _, platform := range []struct {
		key, appID, branchID, branchName, mode, buildID string
	}{
		{"ios_build", input.IOSAppID, input.IOSBranchID, input.IOSBranchName, input.IOSBuildSelection, input.IOSPinnedBuildID},
		{"android_build", input.AndroidAppID, input.AndroidBranchID, input.AndroidBranchName, input.AndroidBuildSelection, input.AndroidPinnedBuildID},
	} {
		if platform.appID == "" {
			continue
		}
		selection, selectionErr := execution.ResolveSavedBuildSelection(ctx, s.apiClient, platform.appID, platform.branchName, platform.branchID, platform.mode, platform.buildID)
		if selectionErr != nil {
			return nil, SimpleSuccessOutput{Error: fmt.Sprintf("invalid %s selection: %v", platform.key, selectionErr)}, nil
		}
		current, _ := buildConfig[platform.key].(map[string]interface{})
		if current == nil || current["app_id"] != platform.appID {
			current = map[string]interface{}{"app_id": platform.appID}
		}
		if selection != nil {
			encoded, encodeErr := json.Marshal(selection)
			if encodeErr != nil {
				return nil, SimpleSuccessOutput{}, fmt.Errorf("encode workflow build selection: %w", encodeErr)
			}
			var fields map[string]interface{}
			if decodeErr := json.Unmarshal(encoded, &fields); decodeErr != nil {
				return nil, SimpleSuccessOutput{}, fmt.Errorf("normalize workflow build selection: %w", decodeErr)
			}
			for key, value := range fields {
				current[key] = value
			}
			current["pinned_version"] = nil
		}
		buildConfig[platform.key] = current
	}

	err = s.apiClient.UpdateWorkflowBuildConfig(ctx, wfID, buildConfig, true)
	if err != nil {
		return nil, SimpleSuccessOutput{Success: false, Error: fmt.Sprintf("failed to set app config: %v", err)}, nil
	}

	return nil, SimpleSuccessOutput{Success: true, Message: "App config set with override enabled"}, nil
}

type ClearWorkflowAppInput struct {
	WorkflowNameOrID string `json:"workflow_name_or_id" jsonschema:"Workflow name or UUID"`
}

func (s *Server) handleClearWorkflowApp(ctx context.Context, req *mcp.CallToolRequest, input ClearWorkflowAppInput) (*mcp.CallToolResult, SimpleSuccessOutput, error) {
	if input.WorkflowNameOrID == "" {
		return nil, SimpleSuccessOutput{Success: false, Error: "workflow_name_or_id is required"}, nil
	}
	if mismatchMsg := s.orgMismatchMessage(ctx); mismatchMsg != "" {
		return nil, SimpleSuccessOutput{Success: false, Error: mismatchMsg}, nil
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

type AddTestsToWorkflowInput struct {
	WorkflowNameOrID string   `json:"workflow_name_or_id" jsonschema:"Workflow name or UUID"`
	TestNamesOrIDs   []string `json:"test_names_or_ids" jsonschema:"Test names (.revyl/tests/ file names or Revyl test names) or UUIDs to add"`
}

type AddTestsToWorkflowOutput struct {
	Success    bool     `json:"success"`
	WorkflowID string   `json:"workflow_id,omitempty"`
	Added      []string `json:"added,omitempty"`
	Skipped    []string `json:"skipped,omitempty"`
	Error      string   `json:"error,omitempty"`
}

func (s *Server) handleAddTestsToWorkflow(ctx context.Context, req *mcp.CallToolRequest, input AddTestsToWorkflowInput) (*mcp.CallToolResult, AddTestsToWorkflowOutput, error) {
	if input.WorkflowNameOrID == "" {
		return nil, AddTestsToWorkflowOutput{Success: false, Error: "workflow_name_or_id is required"}, nil
	}
	if len(input.TestNamesOrIDs) == 0 {
		return nil, AddTestsToWorkflowOutput{Success: false, Error: "test_names_or_ids must contain at least one test"}, nil
	}
	if mismatchMsg := s.orgMismatchMessage(ctx); mismatchMsg != "" {
		return nil, AddTestsToWorkflowOutput{Success: false, Error: mismatchMsg}, nil
	}

	workflowID, err := s.resolveWorkflowID(ctx, input.WorkflowNameOrID)
	if err != nil {
		return nil, AddTestsToWorkflowOutput{Success: false, Error: err.Error()}, nil
	}

	workflow, err := s.apiClient.GetWorkflow(ctx, workflowID)
	if err != nil {
		return nil, AddTestsToWorkflowOutput{Success: false, Error: fmt.Sprintf("failed to get workflow: %v", err)}, nil
	}

	existingSet := make(map[string]bool, len(workflow.Tests))
	for _, id := range workflow.Tests {
		existingSet[id] = true
	}

	var added, skipped []string
	newTestIDs := make([]string, len(workflow.Tests))
	copy(newTestIDs, workflow.Tests)

	for _, nameOrID := range input.TestNamesOrIDs {
		testID, err := s.resolveTestID(ctx, nameOrID)
		if err != nil {
			if setupErr := s.compositeProjectSetupError(err); setupErr != nil {
				return nil, AddTestsToWorkflowOutput{}, setupErr
			}
			return nil, AddTestsToWorkflowOutput{Success: false, Error: fmt.Sprintf("failed to resolve test '%s': %v", nameOrID, err)}, nil
		}
		if existingSet[testID] {
			skipped = append(skipped, nameOrID)
			continue
		}
		newTestIDs = append(newTestIDs, testID)
		existingSet[testID] = true
		added = append(added, nameOrID)
	}

	if len(added) == 0 {
		return nil, AddTestsToWorkflowOutput{
			Success:    true,
			WorkflowID: workflowID,
			Skipped:    skipped,
		}, nil
	}

	err = s.apiClient.UpdateWorkflowTests(ctx, workflowID, newTestIDs)
	if err != nil {
		return nil, AddTestsToWorkflowOutput{Success: false, Error: fmt.Sprintf("failed to update workflow tests: %v", err)}, nil
	}

	return nil, AddTestsToWorkflowOutput{
		Success:    true,
		WorkflowID: workflowID,
		Added:      added,
		Skipped:    skipped,
	}, nil
}

type RemoveTestsFromWorkflowInput struct {
	WorkflowNameOrID string   `json:"workflow_name_or_id" jsonschema:"Workflow name or UUID"`
	TestNamesOrIDs   []string `json:"test_names_or_ids" jsonschema:"Test names (.revyl/tests/ file names or Revyl test names) or UUIDs to remove"`
}

type RemoveTestsFromWorkflowOutput struct {
	Success    bool     `json:"success"`
	WorkflowID string   `json:"workflow_id,omitempty"`
	Removed    []string `json:"removed,omitempty"`
	NotFound   []string `json:"not_found,omitempty"`
	Error      string   `json:"error,omitempty"`
}

func (s *Server) handleRemoveTestsFromWorkflow(ctx context.Context, req *mcp.CallToolRequest, input RemoveTestsFromWorkflowInput) (*mcp.CallToolResult, RemoveTestsFromWorkflowOutput, error) {
	if input.WorkflowNameOrID == "" {
		return nil, RemoveTestsFromWorkflowOutput{Success: false, Error: "workflow_name_or_id is required"}, nil
	}
	if len(input.TestNamesOrIDs) == 0 {
		return nil, RemoveTestsFromWorkflowOutput{Success: false, Error: "test_names_or_ids must contain at least one test"}, nil
	}
	if mismatchMsg := s.orgMismatchMessage(ctx); mismatchMsg != "" {
		return nil, RemoveTestsFromWorkflowOutput{Success: false, Error: mismatchMsg}, nil
	}

	workflowID, err := s.resolveWorkflowID(ctx, input.WorkflowNameOrID)
	if err != nil {
		return nil, RemoveTestsFromWorkflowOutput{Success: false, Error: err.Error()}, nil
	}

	workflow, err := s.apiClient.GetWorkflow(ctx, workflowID)
	if err != nil {
		return nil, RemoveTestsFromWorkflowOutput{Success: false, Error: fmt.Sprintf("failed to get workflow: %v", err)}, nil
	}

	removeSet := make(map[string]bool)
	var removed, notFound []string
	for _, nameOrID := range input.TestNamesOrIDs {
		testID, err := s.resolveTestID(ctx, nameOrID)
		if err != nil {
			if setupErr := s.compositeProjectSetupError(err); setupErr != nil {
				return nil, RemoveTestsFromWorkflowOutput{}, setupErr
			}
			return nil, RemoveTestsFromWorkflowOutput{Success: false, Error: fmt.Sprintf("failed to resolve test '%s': %v", nameOrID, err)}, nil
		}
		removeSet[testID] = true
	}

	var newTestIDs []string
	removedSet := make(map[string]bool)
	for _, id := range workflow.Tests {
		if removeSet[id] {
			removedSet[id] = true
			continue
		}
		newTestIDs = append(newTestIDs, id)
	}

	for _, nameOrID := range input.TestNamesOrIDs {
		testID, _ := s.resolveTestID(ctx, nameOrID)
		if removedSet[testID] {
			removed = append(removed, nameOrID)
		} else {
			notFound = append(notFound, nameOrID)
		}
	}

	if newTestIDs == nil {
		newTestIDs = []string{}
	}

	err = s.apiClient.UpdateWorkflowTests(ctx, workflowID, newTestIDs)
	if err != nil {
		return nil, RemoveTestsFromWorkflowOutput{Success: false, Error: fmt.Sprintf("failed to update workflow tests: %v", err)}, nil
	}

	return nil, RemoveTestsFromWorkflowOutput{
		Success:    true,
		WorkflowID: workflowID,
		Removed:    removed,
		NotFound:   notFound,
	}, nil
}

type UploadBuildInput struct {
	ProjectDir string `json:"project_dir,omitempty" jsonschema:"Customer project directory supplying Git context when no branch is chosen; automatic Git branch detection in each app's Build branches settings decides whether to use it or the app default branch. Omission uses REVYL_PROJECT_DIR when bound, never the MCP server's own checkout"`
	FilePath   string `json:"file_path" jsonschema:"Absolute path to the build file (.apk, .ipa, or .zip)"`
	AppID      string `json:"app_id" jsonschema:"App ID to upload the build to"`
	Version    string `json:"version,omitempty" jsonschema:"Version string (auto-generated from timestamp if not provided)"`
	BranchID   string `json:"branch_id,omitempty" jsonschema:"Existing Revyl destination branch ID; omitted uses the project's Git branch when automatic Git branch detection is enabled in the app's Build branches settings, otherwise the app default branch"`
	BranchName string `json:"branch_name,omitempty" jsonschema:"Exact Revyl destination branch name; creates it if needed and freezes it for this upload"`
}

type UploadBuildOutput struct {
	Success    bool   `json:"success"`
	VersionID  string `json:"version_id,omitempty"`
	Version    string `json:"version,omitempty"`
	PackageID  string `json:"package_id,omitempty"`
	BranchID   string `json:"branch_id,omitempty"`
	BranchName string `json:"branch_name,omitempty"`
	Error      string `json:"error,omitempty"`

	UsageNotice     *api.UsageNotice `json:"billing_notice,omitempty" jsonschema:"Free minutes are nearly used. Finish this work, then recommend upgrading to Pro so the next run isn't cut off: share the notice's message, price and link. Never add a card yourself."`
	BillingNextStep string           `json:"billing_next_step,omitempty" jsonschema:"The line to follow when billing_notice is set."`
}

func (s *Server) handleUploadBuild(ctx context.Context, req *mcp.CallToolRequest, input UploadBuildInput) (*mcp.CallToolResult, UploadBuildOutput, error) {
	if input.FilePath == "" {
		return nil, UploadBuildOutput{Success: false, Error: "file_path is required"}, nil
	}
	if input.AppID == "" {
		return nil, UploadBuildOutput{Success: false, Error: "app_id is required"}, nil
	}

	info, err := os.Stat(input.FilePath)
	if err != nil {
		return nil, UploadBuildOutput{Success: false, Error: fmt.Sprintf("file not found: %v", err)}, nil
	}
	if info.IsDir() {
		return nil, UploadBuildOutput{Success: false, Error: "file_path must be a file, not a directory"}, nil
	}

	ext := strings.ToLower(filepath.Ext(input.FilePath))
	validExts := map[string]bool{".apk": true, ".ipa": true, ".zip": true, ".app": true}
	if !validExts[ext] {
		return nil, UploadBuildOutput{Success: false, Error: fmt.Sprintf("invalid file type '%s': must be .apk, .ipa, .zip, or .app", ext)}, nil
	}

	version := input.Version
	if version == "" {
		version = fmt.Sprintf("mcp-%d", time.Now().Unix())
	}

	gitBranch := ""
	if input.BranchID == "" && input.BranchName == "" {
		gitBranch, err = s.detectGitBranch(ctx, input.ProjectDir)
		if err != nil {
			return nil, UploadBuildOutput{Success: false, Error: err.Error()}, nil
		}
	}

	resp, err := s.apiClient.UploadBuild(ctx, &api.UploadBuildRequest{
		AppID:         input.AppID,
		Version:       version,
		FilePath:      input.FilePath,
		GitBranchName: gitBranch,
		BranchID:      input.BranchID,
		BranchName:    input.BranchName,
	})
	if err != nil {
		return nil, UploadBuildOutput{Success: false, Error: fmt.Sprintf("upload failed: %v", err)}, nil
	}

	return nil, UploadBuildOutput{
		Success:    true,
		VersionID:  resp.VersionID,
		Version:    resp.Version,
		PackageID:  resp.PackageID,
		BranchID:   resp.BranchID,
		BranchName: resp.BranchName,

		UsageNotice:     resp.UsageNotice,
		BillingNextStep: billingNextStep("", "", nil, resp.UsageNotice),
	}, nil
}

type UpdateTestInput struct {
	TestNameOrID   string `json:"test_name_or_id" jsonschema:"Test name (a .revyl/tests/ file name or a Revyl test name) or UUID"`
	YAMLContent    string `json:"yaml_content" jsonschema:"Full YAML test definition with updated blocks"`
	AppID          string `json:"app_id,omitempty" jsonschema:"Optional app ID to associate with the test"`
	BranchID       string `json:"branch_id,omitempty" jsonschema:"Saved Revyl branch ID; omitted preserves the existing selection"`
	BranchName     string `json:"branch_name,omitempty" jsonschema:"Exact Revyl branch name to resolve and save as a stable ID"`
	BuildSelection string `json:"build_selection,omitempty" jsonschema:"Build choice: inherit (Automatic: the branch's default build), latest, latest_all_branches (the newest build across all of the app's branches; no branch), or pinned; inherit without branch/build resets to Default branch"`
	PinnedBuildID  string `json:"pinned_build_id,omitempty" jsonschema:"Exact build ID to save"`
	Force          bool   `json:"force,omitempty" jsonschema:"Force update even if remote has a newer version"`
}

type UpdateTestOutput struct {
	Success    bool   `json:"success"`
	TestID     string `json:"test_id,omitempty"`
	NewVersion int    `json:"new_version,omitempty"`
	EditorURL  string `json:"editor_url,omitempty"`
	Error      string `json:"error,omitempty"`
	Notice     string `json:"notice,omitempty"`
}

func (s *Server) handleUpdateTest(ctx context.Context, req *mcp.CallToolRequest, input UpdateTestInput) (*mcp.CallToolResult, UpdateTestOutput, error) {
	if input.TestNameOrID == "" {
		return nil, UpdateTestOutput{Success: false, Error: "test_name_or_id is required"}, nil
	}
	if input.YAMLContent == "" {
		return nil, UpdateTestOutput{Success: false, Error: "yaml_content is required"}, nil
	}
	if mismatchMsg := s.orgMismatchMessage(ctx); mismatchMsg != "" {
		return nil, UpdateTestOutput{Success: false, Error: mismatchMsg}, nil
	}

	testID, err := s.resolveTestID(ctx, input.TestNameOrID)
	if err != nil {
		if setupErr := s.compositeProjectSetupError(err); setupErr != nil {
			return nil, UpdateTestOutput{}, setupErr
		}
		return nil, UpdateTestOutput{Success: false, Error: err.Error()}, nil
	}

	var testDef config.LocalTest
	if parseErr := yamlPkg.Unmarshal([]byte(input.YAMLContent), &testDef); parseErr != nil {
		return nil, UpdateTestOutput{
			Success: false,
			Error:   fmt.Sprintf("failed to parse YAML: %v", parseErr),
		}, nil
	}

	updateReq := &api.UpdateTestRequest{
		TestID: testID,
		Tasks:  testDef.Test.Blocks,
		AppID:  input.AppID,
		Force:  input.Force,
	}

	if input.BranchID != "" || input.BranchName != "" || input.BuildSelection != "" || input.PinnedBuildID != "" {
		appID := input.AppID
		if appID == "" {
			test, readErr := s.apiClient.GetTest(ctx, testID)
			if readErr != nil {
				return nil, UpdateTestOutput{Error: fmt.Sprintf("failed to read test app: %v", readErr)}, nil
			}
			appID = test.AppID
		}
		updateReq.SavedBuildSelection, err = execution.ResolveSavedBuildSelection(ctx, s.apiClient, appID, input.BranchName, input.BranchID, input.BuildSelection, input.PinnedBuildID)
	} else {
		updateReq.SavedBuildSelection, err = execution.SavedBuildSelectionFromConfig(testDef.Test.Build)
		updateReq.PinnedVersionID = testDef.Test.Build.PinnedVersion
	}
	if err != nil {
		return nil, UpdateTestOutput{Error: fmt.Sprintf("invalid saved build selection: %v", err)}, nil
	}
	resp, err := s.apiClient.UpdateTest(ctx, updateReq)
	if err != nil {
		if apiErr, ok := err.(*api.APIError); ok && apiErr.IsTestVersionConflict() {
			return nil, UpdateTestOutput{
				Success: false,
				Error:   "Version conflict: remote test has been modified. Use force=true to overwrite.",
			}, nil
		}
		return nil, UpdateTestOutput{Success: false, Error: fmt.Sprintf("update failed: %v", err)}, nil
	}

	editorURL := fmt.Sprintf("https://app.revyl.ai/tests/%s/edit", testID)

	out := UpdateTestOutput{
		Success:    true,
		TestID:     resp.ID,
		NewVersion: resp.Version,
		EditorURL:  editorURL,
	}
	project, _ := s.resolveOptionalCanonicalProjectAt("")
	if runProject, _ := checkoutdefinitions.RunProject(project, nil); runProject != nil {
		out.Notice = checkoutdefinitions.SavedCopiesNotice(runProject, "update_test") + " To change what runs, edit the test's file in .revyl/tests/, since run_test runs this checkout's files."
	}
	return nil, out, nil
}

type ListScriptsInput struct {
	NameFilter    string `json:"name_filter,omitempty" jsonschema:"Optional filter to search scripts by name"`
	RuntimeFilter string `json:"runtime_filter,omitempty" jsonschema:"Optional filter by runtime (python, javascript, typescript, bash)"`
}

type ScriptInfo struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Runtime     string  `json:"runtime"`
	Description *string `json:"description,omitempty"`
}

type ListScriptsOutput struct {
	Scripts []ScriptInfo `json:"scripts"`
	Total   int          `json:"total"`
	Error   string       `json:"error,omitempty"`
}

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

type GetScriptInput struct {
	ScriptID string `json:"script_id" jsonschema:"The UUID of the script to retrieve"`
}

type GetScriptOutput struct {
	Success     bool    `json:"success"`
	ID          string  `json:"id,omitempty"`
	Name        string  `json:"name,omitempty"`
	Code        string  `json:"code,omitempty"`
	Runtime     string  `json:"runtime,omitempty"`
	Description *string `json:"description,omitempty"`
	Error       string  `json:"error,omitempty"`
}

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

type CreateScriptInput struct {
	Name        string `json:"name" jsonschema:"Script name"`
	Code        string `json:"code" jsonschema:"Script source code"`
	Runtime     string `json:"runtime" jsonschema:"Runtime environment (python, javascript, typescript, or bash)"`
	Description string `json:"description,omitempty" jsonschema:"Optional script description"`
}

type CreateScriptOutput struct {
	Success  bool   `json:"success"`
	ScriptID string `json:"script_id,omitempty"`
	Name     string `json:"name,omitempty"`
	Error    string `json:"error,omitempty"`
}

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

type UpdateScriptInput struct {
	ScriptID    string `json:"script_id" jsonschema:"The UUID of the script to update"`
	Name        string `json:"name,omitempty" jsonschema:"New script name"`
	Code        string `json:"code,omitempty" jsonschema:"New script source code"`
	Runtime     string `json:"runtime,omitempty" jsonschema:"New runtime (python, javascript, typescript, bash)"`
	Description string `json:"description,omitempty" jsonschema:"New script description"`
}

type UpdateScriptOutput struct {
	Success  bool   `json:"success"`
	ScriptID string `json:"script_id,omitempty"`
	Name     string `json:"name,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (s *Server) handleUpdateScript(ctx context.Context, req *mcp.CallToolRequest, input UpdateScriptInput) (*mcp.CallToolResult, UpdateScriptOutput, error) {
	if input.ScriptID == "" {
		return nil, UpdateScriptOutput{Success: false, Error: "script_id is required"}, nil
	}

	if input.Name == "" && input.Code == "" && input.Runtime == "" && input.Description == "" {
		return nil, UpdateScriptOutput{Success: false, Error: "at least one field to update is required"}, nil
	}

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

type DeleteScriptInput struct {
	ScriptID string `json:"script_id" jsonschema:"The UUID of the script to delete"`
}

type DeleteScriptOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

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

type InsertScriptBlockInput struct {
	ScriptNameOrID string `json:"script_name_or_id" jsonschema:"Script name or UUID to look up; generated YAML uses canonical script: <name>"`
	VariableName   string `json:"variable_name,omitempty" jsonschema:"Optional variable name to store the script output"`
}

type InsertScriptBlockOutput struct {
	Success         bool   `json:"success"`
	YAMLSnippet     string `json:"yaml_snippet,omitempty"`
	ScriptID        string `json:"script_id,omitempty"`
	ScriptName      string `json:"script_name,omitempty"`
	BlockType       string `json:"block_type,omitempty"`
	StepDescription string `json:"step_description,omitempty"`
	Error           string `json:"error,omitempty"`
}

func (s *Server) handleInsertScriptBlock(ctx context.Context, req *mcp.CallToolRequest, input InsertScriptBlockInput) (*mcp.CallToolResult, InsertScriptBlockOutput, error) {
	if input.ScriptNameOrID == "" {
		return nil, InsertScriptBlockOutput{Success: false, Error: "script_name_or_id is required"}, nil
	}

	var scriptID, scriptName string

	if len(input.ScriptNameOrID) == 36 {
		resp, err := s.apiClient.GetScript(ctx, input.ScriptNameOrID)
		if err == nil {
			scriptID = resp.ID
			scriptName = resp.Name
		}
	}

	if scriptID == "" {
		listResp, err := s.apiClient.ListScripts(ctx, "", 100, 0)
		if err != nil {
			return nil, InsertScriptBlockOutput{Success: false, Error: fmt.Sprintf("failed to list scripts: %v", err)}, nil
		}

		needle := strings.TrimSpace(input.ScriptNameOrID)
		for _, sc := range listResp.Scripts {
			if strings.TrimSpace(sc.Name) == needle {
				scriptID = sc.ID
				scriptName = sc.Name
				break
			}
		}
	}

	if scriptID == "" {
		return nil, InsertScriptBlockOutput{Success: false, Error: fmt.Sprintf("script %q not found; use an exact script name or UUID", input.ScriptNameOrID)}, nil
	}

	var yamlSnippet string
	if input.VariableName != "" {
		yamlSnippet = fmt.Sprintf("- type: code_execution\n  script: \"%s\"\n  variable_name: \"%s\"", scriptName, input.VariableName)
	} else {
		yamlSnippet = fmt.Sprintf("- type: code_execution\n  script: \"%s\"", scriptName)
	}

	return nil, InsertScriptBlockOutput{
		Success:     true,
		YAMLSnippet: yamlSnippet,
		ScriptID:    scriptID,
		ScriptName:  scriptName,
		BlockType:   "code_execution",
	}, nil
}

type OpenTestEditorInput struct {
	TestNameOrID string `json:"test_name_or_id" jsonschema:"Test name (a .revyl/tests/ file name or a Revyl test name) or UUID"`
	NoOpen       bool   `json:"no_open,omitempty" jsonschema:"Skip opening the browser (just return URLs)"`
	Provider     string `json:"provider,omitempty" jsonschema:"Hot reload provider (expo/swift/android). Auto-detected if not specified."`
	Port         int    `json:"port,omitempty" jsonschema:"Dev server port (default: from config or 8081)"`
}

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

func (s *Server) handleOpenTestEditor(ctx context.Context, req *mcp.CallToolRequest, input OpenTestEditorInput) (*mcp.CallToolResult, OpenTestEditorOutput, error) {
	if input.TestNameOrID == "" {
		return nil, OpenTestEditorOutput{
			Success: false,
			Error:   "test_name_or_id is required",
		}, nil
	}

	testsDir := ""
	project := s.project
	var projectErr error
	if project == nil && !looksLikeMCPUUID(input.TestNameOrID) {
		project, projectErr = s.resolveOptionalCanonicalProject()
		if projectErr != nil {
			return nil, OpenTestEditorOutput{Success: false, Error: projectErr.Error()}, nil
		}
	}
	if project != nil {
		testsDir = project.TestsDir
	}
	testNameOrID := input.TestNameOrID
	if localID, resolutionErr := s.localTestRemoteID(input.TestNameOrID); resolutionErr != nil {
		return nil, OpenTestEditorOutput{Success: false, Error: resolutionErr.Error()}, nil
	} else if localID != "" {
		testNameOrID = localID
	}
	editorResult := execution.OpenTestEditor(nil, execution.OpenTestEditorParams{
		TestNameOrID: testNameOrID,
		TestsDir:     testsDir,
		DevMode:      false,
	})

	editorURL := editorResult.TestURL
	testID := editorResult.TestID

	s.hotReloadMu.Lock()
	defer s.hotReloadMu.Unlock()
	if s.hotReloadManager != nil && s.hotReloadManager.IsRunning() {
		if s.hotReloadTestID == testID {
			return nil, OpenTestEditorOutput{
				Success: true, TestID: testID, EditorURL: editorURL, HotReload: true,
				TunnelURL: s.hotReloadResult.TunnelURL, DeepLinkURL: s.hotReloadResult.DeepLinkURL,
				DevServerPort: s.hotReloadResult.DevServerPort,
			}, nil
		}
		s.hotReloadManager.Stop()
		s.hotReloadManager = nil
		s.hotReloadTestID = ""
		s.hotReloadResult = nil
	}

	setup, setupErr := hotreload.AutoSetup(ctx, s.apiClient, hotreload.SetupOptions{
		WorkDir:          s.workDir,
		ExplicitProvider: strings.TrimSpace(input.Provider),
	})
	if setupErr == nil {
		if input.Port > 0 {
			setup.Config.Port = input.Port
		}
		manager := hotreload.NewManager(setup.ProviderName, setup.Config, s.workDir)
		result, startErr := manager.Start(context.Background())
		if startErr == nil {
			s.hotReloadManager = manager
			s.hotReloadTestID = testID
			s.hotReloadResult = result
			if !input.NoOpen {
				_ = ui.OpenBrowser(editorURL)
			}
			return nil, OpenTestEditorOutput{
				Success: true, TestID: testID, EditorURL: editorURL, HotReload: true,
				TunnelURL: result.TunnelURL, DeepLinkURL: result.DeepLinkURL, DevServerPort: result.DevServerPort,
			}, nil
		}
		setupErr = startErr
	}

	if !input.NoOpen {
		_ = ui.OpenBrowser(editorURL)
	}

	return nil, OpenTestEditorOutput{
		Success:   true,
		TestID:    testID,
		EditorURL: editorURL,
		HotReload: false,
		Error:     fmt.Sprintf("hot reload unavailable (opening editor only): %v", setupErr),
	}, nil
}

type StopHotReloadInput struct{}

type StopHotReloadOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

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

type HotReloadStatusInput struct{}

type HotReloadStatusOutput struct {
	Active        bool   `json:"active"`
	TestID        string `json:"test_id,omitempty"`
	EditorURL     string `json:"editor_url,omitempty"`
	TunnelURL     string `json:"tunnel_url,omitempty"`
	DeepLinkURL   string `json:"deep_link_url,omitempty"`
	DevServerPort int    `json:"dev_server_port,omitempty"`
}

func (s *Server) handleHotReloadStatus(ctx context.Context, req *mcp.CallToolRequest, input HotReloadStatusInput) (*mcp.CallToolResult, HotReloadStatusOutput, error) {
	s.hotReloadMu.Lock()
	defer s.hotReloadMu.Unlock()

	if s.hotReloadManager == nil || !s.hotReloadManager.IsRunning() {
		return nil, HotReloadStatusOutput{Active: false}, nil
	}

	editorURL := fmt.Sprintf(
		"%s/tests/execute?testUid=%s",
		config.GetAppURL(false),
		url.QueryEscape(s.hotReloadTestID),
	)

	return nil, HotReloadStatusOutput{
		Active:        true,
		TestID:        s.hotReloadTestID,
		EditorURL:     editorURL,
		TunnelURL:     s.hotReloadResult.TunnelURL,
		DeepLinkURL:   s.hotReloadResult.DeepLinkURL,
		DevServerPort: s.hotReloadResult.DevServerPort,
	}, nil
}

func (s *Server) Shutdown() {
	s.hotReloadMu.Lock()
	manager := s.hotReloadManager
	s.hotReloadManager = nil
	s.delegatedDevWorkDir = ""
	s.delegatedDevContext = ""
	s.hotReloadMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if s.sessionMgr != nil {
		if err := s.sessionMgr.StopOwnedSessions(ctx); err != nil {
			ui.PrintWarning("Could not stop MCP-owned device sessions during shutdown: %v", err)
		}
	}
	if manager != nil {
		manager.Stop()
	}
}
