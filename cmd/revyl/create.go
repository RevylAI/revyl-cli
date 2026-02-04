// Package main provides the create command for creating tests and workflows.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// createCmd is the parent command for creating resources.
var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create tests and workflows",
	Long: `Create new tests and workflows.

Commands:
  test      - Create a new test and open the editor
  workflow  - Create a new workflow and open the editor

Examples:
  revyl create test login-flow --platform android
  revyl create workflow smoke-tests --tests login-flow,checkout`,
}

// createTestCmd creates a new test.
var createTestCmd = &cobra.Command{
	Use:   "test <name>",
	Short: "Create a new test",
	Long: `Create a new test and open the editor.

This command creates a test on the Revyl server and adds it to your
local .revyl/config.yaml. The browser opens to the test editor where
you can define the test steps.

Examples:
  revyl create test login-flow --platform android
  revyl create test checkout --platform ios --build-var <id>
  revyl create test onboarding --platform android --no-open`,
	Args: cobra.ExactArgs(1),
	RunE: runCreateTest,
}

// createWorkflowCmd creates a new workflow.
var createWorkflowCmd = &cobra.Command{
	Use:   "workflow <name>",
	Short: "Create a new workflow",
	Long: `Create a new workflow and open the editor.

This command creates a workflow on the Revyl server and adds it to your
local .revyl/config.yaml. The browser opens to the workflow editor where
you can configure the workflow.

Use --tests to pre-select tests to include in the workflow.

Examples:
  revyl create workflow smoke-tests
  revyl create workflow regression --tests login-flow,checkout,payment
  revyl create workflow nightly --no-open`,
	Args: cobra.ExactArgs(1),
	RunE: runCreateWorkflow,
}

var (
	// Test creation flags
	createTestPlatform string
	createTestBuildVar string
	createTestNoOpen   bool
	createTestNoSync   bool
	createTestForce    bool

	// Workflow creation flags
	createWorkflowTests  string
	createWorkflowNoOpen bool
	createWorkflowNoSync bool
)

func init() {
	createCmd.AddCommand(createTestCmd)
	createCmd.AddCommand(createWorkflowCmd)

	// Test creation flags
	createTestCmd.Flags().StringVar(&createTestPlatform, "platform", "", "Target platform (android, ios)")
	createTestCmd.Flags().StringVar(&createTestBuildVar, "build-var", "", "Build variable ID to associate with the test")
	createTestCmd.Flags().BoolVar(&createTestNoOpen, "no-open", false, "Skip opening browser to test editor")
	createTestCmd.Flags().BoolVar(&createTestNoSync, "no-sync", false, "Skip adding test to .revyl/config.yaml")
	createTestCmd.Flags().BoolVar(&createTestForce, "force", false, "Update existing test if name already exists")

	// Workflow creation flags
	createWorkflowCmd.Flags().StringVar(&createWorkflowTests, "tests", "", "Comma-separated test names or IDs to include")
	createWorkflowCmd.Flags().BoolVar(&createWorkflowNoOpen, "no-open", false, "Skip opening browser to workflow editor")
	createWorkflowCmd.Flags().BoolVar(&createWorkflowNoSync, "no-sync", false, "Skip adding workflow to .revyl/config.yaml")
}

// runCreateTest creates a new test on the server and adds it to the local config.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (test name)
//
// Returns:
//   - error: Any error that occurred during test creation
func runCreateTest(cmd *cobra.Command, args []string) error {
	testName := args[0]

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")

	// Load or create project config
	cfg, err := config.LoadProjectConfig(configPath)
	if err != nil {
		ui.PrintWarning("Project not initialized. Run 'revyl init' first for full functionality.")
		// Create minimal config for test creation
		cfg = &config.ProjectConfig{
			Tests:     make(map[string]string),
			Workflows: make(map[string]string),
		}
	}

	// Ensure maps are initialized (config file may exist but have nil maps)
	if cfg.Tests == nil {
		cfg.Tests = make(map[string]string)
	}
	if cfg.Workflows == nil {
		cfg.Workflows = make(map[string]string)
	}

	// Check if test name already exists in config
	if existingID, exists := cfg.Tests[testName]; exists {
		ui.PrintWarning("Test '%s' already exists in config (id: %s)", testName, existingID)
		overwrite, err := ui.PromptConfirm("Overwrite with new test?", false)
		if err != nil || !overwrite {
			ui.PrintInfo("Cancelled. Use a different name or remove the existing entry.")
			return nil
		}
	}

	// Determine platform
	platform := createTestPlatform
	if platform == "" {
		// Prompt user to select platform
		platformOptions := []string{"android", "ios"}
		idx, err := ui.PromptSelect("Select platform:", platformOptions)
		if err != nil {
			return fmt.Errorf("platform selection cancelled: %w", err)
		}
		platform = platformOptions[idx]
	}

	// Auto-detect build_var_id from config if not provided via flag
	buildVarID := createTestBuildVar
	if buildVarID == "" && cfg.Build.Variants != nil {
		if variant, ok := cfg.Build.Variants[platform]; ok && variant.BuildVarID != "" {
			buildVarID = variant.BuildVarID
			ui.PrintInfo("Using build variable from config: %s", buildVarID)
		}
	}

	// Create API client with dev mode support
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(creds.APIKey, devMode)

	// Check if test with same name already exists in the organization
	var existingTestID string
	testsResp, err := client.ListOrgTests(cmd.Context(), 100, 0)
	if err == nil {
		for _, t := range testsResp.Tests {
			if t.Name == testName {
				existingTestID = t.ID
				break
			}
		}
	}

	ui.Println()

	// Handle existing test
	if existingTestID != "" {
		if !createTestForce {
			ui.PrintError("A test named '%s' already exists (id: %s)", testName, existingTestID)
			ui.PrintInfo("Use --force to use the existing test, or choose a different name.")
			return fmt.Errorf("test already exists")
		}
		// Use existing test
		ui.PrintInfo("Using existing test '%s' (id: %s)", testName, existingTestID)

		// Update the test's build_var_id if we have one
		if buildVarID != "" {
			ui.StartSpinner("Updating test build variable...")
			_, err := client.UpdateTest(cmd.Context(), &api.UpdateTestRequest{
				TestID:     existingTestID,
				BuildVarID: buildVarID,
				Force:      true,
			})
			ui.StopSpinner()

			if err != nil {
				ui.PrintWarning("Failed to update build variable: %v", err)
			} else {
				ui.PrintSuccess("Updated build variable")
			}
		}

		// Add to config unless --no-sync is specified
		if !createTestNoSync {
			cfg.Tests[testName] = existingTestID

			// Ensure .revyl directory exists
			revylDir := filepath.Join(cwd, ".revyl")
			if err := os.MkdirAll(revylDir, 0755); err != nil {
				ui.PrintWarning("Failed to create .revyl directory: %v", err)
			} else {
				if err := config.WriteProjectConfig(configPath, cfg); err != nil {
					ui.PrintWarning("Failed to update config: %v", err)
				} else {
					ui.PrintSuccess("Added to .revyl/config.yaml")
				}
			}
		}

		// Open browser to test execute page unless --no-open is specified
		executeURL := fmt.Sprintf("%s/tests/execute?testUid=%s", config.GetAppURL(devMode), existingTestID)

		ui.Println()
		if !createTestNoOpen {
			ui.PrintInfo("Opening test...")
			ui.PrintLink("Test", executeURL)
			if err := ui.OpenBrowser(executeURL); err != nil {
				ui.PrintWarning("Could not open browser: %v", err)
				ui.PrintInfo("Open manually: %s", executeURL)
			}
		} else {
			ui.PrintInfo("Test URL: %s", executeURL)
		}

		return nil
	}

	ui.PrintInfo("Creating test '%s' (%s)...", testName, platform)

	// Create test on server with empty tasks
	ui.StartSpinner("Creating test on server...")
	createResp, err := client.CreateTest(cmd.Context(), &api.CreateTestRequest{
		Name:       testName,
		Platform:   platform,
		Tasks:      []interface{}{}, // Empty tasks - user will define in browser
		BuildVarID: buildVarID,
		OrgID:      creds.OrgID,
	})
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to create test: %v", err)
		return err
	}

	ui.PrintSuccess("Created test: %s (id: %s)", testName, createResp.ID)

	// Add to config unless --no-sync is specified
	if !createTestNoSync {
		cfg.Tests[testName] = createResp.ID

		// Ensure .revyl directory exists
		revylDir := filepath.Join(cwd, ".revyl")
		if err := os.MkdirAll(revylDir, 0755); err != nil {
			ui.PrintWarning("Failed to create .revyl directory: %v", err)
		} else {
			if err := config.WriteProjectConfig(configPath, cfg); err != nil {
				ui.PrintWarning("Failed to update config: %v", err)
			} else {
				ui.PrintSuccess("Added to .revyl/config.yaml")
			}
		}
	}

	// Open browser to test execute page unless --no-open is specified
	executeURL := fmt.Sprintf("%s/tests/execute?testUid=%s", config.GetAppURL(devMode), createResp.ID)

	ui.Println()
	if !createTestNoOpen {
		ui.PrintInfo("Opening test...")
		ui.PrintLink("Test", executeURL)
		if err := ui.OpenBrowser(executeURL); err != nil {
			ui.PrintWarning("Could not open browser: %v", err)
			ui.PrintInfo("Open manually: %s", executeURL)
		}
	} else {
		ui.PrintInfo("Test URL: %s", executeURL)
	}

	ui.Println()
	ui.PrintInfo("Next: Define your test steps in the browser, then run with:")
	ui.PrintDim("  revyl test %s", testName)

	return nil
}

// runCreateWorkflow creates a new workflow on the server and adds it to the local config.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (workflow name)
//
// Returns:
//   - error: Any error that occurred during workflow creation
func runCreateWorkflow(cmd *cobra.Command, args []string) error {
	workflowName := args[0]

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Create API client with dev mode support
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(creds.APIKey, devMode)

	// Ensure UserID is available (may be missing if using REVYL_API_KEY env var)
	if creds.UserID == "" {
		userInfo, err := client.ValidateAPIKey(cmd.Context())
		if err != nil {
			ui.PrintError("Failed to validate API key: %v", err)
			return fmt.Errorf("failed to validate API key: %w", err)
		}
		creds.UserID = userInfo.UserID
	}

	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")

	// Load or create project config
	cfg, err := config.LoadProjectConfig(configPath)
	if err != nil {
		ui.PrintWarning("Project not initialized. Run 'revyl init' first for full functionality.")
		// Create minimal config for workflow creation
		cfg = &config.ProjectConfig{
			Tests:     make(map[string]string),
			Workflows: make(map[string]string),
		}
	}

	// Ensure maps are initialized (config file may exist but have nil maps)
	if cfg.Tests == nil {
		cfg.Tests = make(map[string]string)
	}
	if cfg.Workflows == nil {
		cfg.Workflows = make(map[string]string)
	}

	// Check if workflow name already exists in config
	if existingID, exists := cfg.Workflows[workflowName]; exists {
		ui.PrintWarning("Workflow '%s' already exists in config (id: %s)", workflowName, existingID)
		overwrite, err := ui.PromptConfirm("Overwrite with new workflow?", false)
		if err != nil || !overwrite {
			ui.PrintInfo("Cancelled. Use a different name or remove the existing entry.")
			return nil
		}
	}

	// Parse test IDs from --tests flag
	var testIDs []string
	if createWorkflowTests != "" {
		testNames := strings.Split(createWorkflowTests, ",")
		for _, name := range testNames {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			// Check if it's an alias in config, otherwise use as-is (assume it's an ID)
			if id, ok := cfg.Tests[name]; ok {
				testIDs = append(testIDs, id)
			} else {
				testIDs = append(testIDs, name)
			}
		}
	}

	ui.Println()
	if len(testIDs) > 0 {
		ui.PrintInfo("Creating workflow '%s' with %d test(s)...", workflowName, len(testIDs))
	} else {
		ui.PrintInfo("Creating workflow '%s'...", workflowName)
	}

	// Create workflow on server
	ui.StartSpinner("Creating workflow on server...")
	createResp, err := client.CreateWorkflow(cmd.Context(), &api.CLICreateWorkflowRequest{
		Name:     workflowName,
		Tests:    testIDs,
		Schedule: "No Schedule",
		Owner:    creds.UserID,
	})
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to create workflow: %v", err)
		return err
	}

	ui.PrintSuccess("Created workflow: %s (id: %s)", workflowName, createResp.Data.ID)

	// Add to config unless --no-sync is specified
	if !createWorkflowNoSync {
		cfg.Workflows[workflowName] = createResp.Data.ID

		// Ensure .revyl directory exists
		revylDir := filepath.Join(cwd, ".revyl")
		if err := os.MkdirAll(revylDir, 0755); err != nil {
			ui.PrintWarning("Failed to create .revyl directory: %v", err)
		} else {
			if err := config.WriteProjectConfig(configPath, cfg); err != nil {
				ui.PrintWarning("Failed to update config: %v", err)
			} else {
				ui.PrintSuccess("Added to .revyl/config.yaml")
			}
		}
	}

	// Open browser to workflow editor unless --no-open is specified
	editorURL := fmt.Sprintf("%s/workflows/%s", config.GetAppURL(devMode), createResp.Data.ID)

	ui.Println()
	if !createWorkflowNoOpen {
		ui.PrintInfo("Opening workflow editor...")
		ui.PrintLink("Editor", editorURL)
		if err := ui.OpenBrowser(editorURL); err != nil {
			ui.PrintWarning("Could not open browser: %v", err)
			ui.PrintInfo("Open manually: %s", editorURL)
		}
	} else {
		ui.PrintInfo("Workflow editor URL: %s", editorURL)
	}

	ui.Println()
	ui.PrintInfo("Next: Configure your workflow in the browser, then run with:")
	ui.PrintDim("  revyl run workflow %s", workflowName)

	return nil
}
