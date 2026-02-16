// Package main provides the create command for creating tests and workflows.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/hotreload"
	_ "github.com/revyl/cli/internal/hotreload/providers" // Register providers
	"github.com/revyl/cli/internal/interactive"
	"github.com/revyl/cli/internal/ui"
	"github.com/revyl/cli/internal/yaml"
)

var (
	// Test creation flags
	createTestPlatform string
	createTestAppID    string
	createTestNoOpen   bool
	createTestNoSync   bool
	createTestForce    bool
	createTestDryRun   bool
	createTestFromFile string
	createTestModules  []string
	createTestTags     []string

	// Hot reload flags for test creation
	createTestHotReload         bool
	createTestHotReloadPort     int
	createTestHotReloadProvider string
	createTestHotReloadPlatform string

	// Interactive mode flag
	createTestInteractive bool

	// Workflow creation flags
	createWorkflowTests  string
	createWorkflowNoOpen bool
	createWorkflowNoSync bool
	createWorkflowDryRun bool
)

// runCreateTest creates a new test on the server and adds it to the local config.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (test name)
//
// Returns:
//   - error: Any error that occurred during test creation
func runCreateTest(cmd *cobra.Command, args []string) error {
	// If --from-file is specified, copy to .revyl/tests/ and use push workflow
	if createTestFromFile != "" {
		return runCreateTestFromFile(cmd, args)
	}

	// If interactive mode is enabled, use the interactive flow
	if createTestInteractive {
		return runCreateTestInteractive(cmd, args)
	}

	// If hot reload is enabled, use the hot reload flow
	if createTestHotReload {
		return runCreateTestWithHotReload(cmd, args)
	}

	testName := args[0]

	// Validate test name
	if err := validateResourceName(testName, "test"); err != nil {
		ui.PrintError("%v", err)
		return err
	}

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds == nil || !creds.HasValidAuth() {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}
	apiKey, err := authMgr.GetActiveToken()
	if err != nil || apiKey == "" {
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

	// Auto-detect app_id from config if not provided via flag
	appID := createTestAppID
	if appID == "" && cfg.Build.Platforms != nil {
		if platformCfg, ok := cfg.Build.Platforms[platform]; ok && platformCfg.AppID != "" {
			appID = platformCfg.AppID
			if !createTestDryRun {
				ui.PrintInfo("Using app from config: %s", appID)
			}
		}
	}

	// Warn the user if no build is configured -- the test won't be runnable without one
	if appID == "" && !createTestDryRun {
		ui.Println()
		ui.PrintWarning("No app configured for platform '%s'.", platform)
		ui.PrintDim("This test won't be runnable until a build is uploaded and associated.")
		ui.Println()
		ui.PrintInfo("To upload a build, run:")
		ui.PrintDim("  revyl build upload --platform %s", platform)
		ui.Println()
	}

	// Handle dry-run mode
	if createTestDryRun {
		ui.Println()
		ui.PrintInfo("Dry-run mode - showing what would be created:")
		ui.Println()
		ui.PrintInfo("  Test Name:    %s", testName)
		ui.PrintInfo("  Platform:     %s", platform)
		if appID != "" {
			ui.PrintInfo("  App ID: %s", appID)
		} else {
			ui.PrintInfo("  App ID: (none)")
		}
		ui.PrintInfo("  Add to Config: %v", !createTestNoSync)
		ui.PrintInfo("  Open Browser:  %v", !createTestNoOpen)
		ui.Println()
		ui.PrintSuccess("Dry-run complete - no changes made")
		return nil
	}

	// Create API client with dev mode support
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

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

		// Update the test's app_id if we have one
		if appID != "" {
			ui.StartSpinner("Updating test app...")
			_, err := client.UpdateTest(cmd.Context(), &api.UpdateTestRequest{
				TestID: existingTestID,
				AppID:  appID,
				Force:  true,
			})
			ui.StopSpinner()

			if err != nil {
				ui.PrintWarning("Failed to update app: %v", err)
			} else {
				ui.PrintSuccess("Updated app")
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
			syncTestYAML(cmd.Context(), client, cfg, testName)
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

	// Resolve --module flags into module_import blocks
	var tasks []interface{}
	if len(createTestModules) > 0 {
		for _, moduleRef := range createTestModules {
			moduleID, moduleName, err := resolveModuleForCreate(cmd, client, moduleRef)
			if err != nil {
				ui.PrintError("Failed to resolve module '%s': %v", moduleRef, err)
				return err
			}
			tasks = append(tasks, map[string]interface{}{
				"type":             "module_import",
				"step_description": moduleName,
				"module_id":        moduleID,
			})
			ui.PrintInfo("  + module: %s (%s)", moduleName, moduleID)
		}
	}
	if tasks == nil {
		tasks = []interface{}{} // Empty tasks - user will define in browser
	}

	// Create test on server
	ui.StartSpinner("Creating test on server...")
	createResp, err := client.CreateTest(cmd.Context(), &api.CreateTestRequest{
		Name:     testName,
		Platform: platform,
		Tasks:    tasks,
		AppID:    appID,
		OrgID:    creds.OrgID,
	})
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to create test: %v", err)
		return err
	}

	ui.PrintSuccess("Created test: %s (id: %s)", testName, createResp.ID)

	// Assign tags if --tag flags were provided
	if len(createTestTags) > 0 {
		ui.StartSpinner("Assigning tags...")
		_, tagErr := client.SyncTestTags(cmd.Context(), createResp.ID, &api.CLISyncTagsRequest{
			TagNames: createTestTags,
		})
		ui.StopSpinner()

		if tagErr != nil {
			ui.PrintWarning("Failed to assign tags: %v", tagErr)
		} else {
			ui.PrintSuccess("Tagged: %s", strings.Join(createTestTags, ", "))
		}
	}

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
		syncTestYAML(cmd.Context(), client, cfg, testName)
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
	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Define steps in browser:", Command: fmt.Sprintf("revyl test open %s", testName)},
		{Label: "Run your test:", Command: fmt.Sprintf("revyl test run %s", testName)},
	})

	return nil
}

// runCreateTestFromFile creates a test from a YAML file.
//
// This function:
//  1. Validates the YAML file
//  2. Copies it to .revyl/tests/<name>.yaml
//  3. Uses the existing push workflow to sync to remote
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (test name)
//
// Returns:
//   - error: Any error that occurred during test creation
func runCreateTestFromFile(cmd *cobra.Command, args []string) error {
	testName := args[0]

	// Validate test name
	if err := validateResourceName(testName, "test"); err != nil {
		ui.PrintError("%v", err)
		return err
	}

	// Validate the YAML file first
	validationResult, err := yaml.ValidateYAMLFile(createTestFromFile)
	if err != nil {
		ui.PrintError("Failed to read YAML file: %v", err)
		return err
	}

	if !validationResult.Valid {
		ui.PrintError("YAML validation failed:")
		for _, e := range validationResult.Errors {
			ui.PrintError("  %s", e)
		}
		return fmt.Errorf("validation failed")
	}

	// Show warnings if any
	for _, w := range validationResult.Warnings {
		ui.PrintWarning("  %s", w)
	}

	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Handle dry-run mode
	if createTestDryRun {
		ui.Println()
		ui.PrintInfo("Dry-run mode - YAML validation passed:")
		ui.Println()
		ui.PrintInfo("  Source:      %s", createTestFromFile)
		ui.PrintInfo("  Destination: .revyl/tests/%s.yaml", testName)
		ui.PrintInfo("  Test Name:   %s", testName)
		ui.Println()
		ui.PrintSuccess("Dry-run complete - YAML is valid, no changes made")
		return nil
	}

	// Ensure .revyl/tests directory exists
	testsDir := filepath.Join(cwd, ".revyl", "tests")
	if err := os.MkdirAll(testsDir, 0755); err != nil {
		ui.PrintError("Failed to create tests directory: %v", err)
		return err
	}

	// Copy the file to .revyl/tests/<name>.yaml
	destPath := filepath.Join(testsDir, testName+".yaml")

	// Check if destination already exists
	if _, err := os.Stat(destPath); err == nil && !createTestForce {
		ui.PrintError("Test file already exists: %s", destPath)
		ui.PrintInfo("Use --force to overwrite.")
		return fmt.Errorf("file already exists")
	}

	// Read source file
	content, err := os.ReadFile(createTestFromFile)
	if err != nil {
		ui.PrintError("Failed to read source file: %v", err)
		return err
	}

	// Write to destination
	if err := os.WriteFile(destPath, content, 0644); err != nil {
		ui.PrintError("Failed to copy file: %v", err)
		return err
	}

	ui.PrintSuccess("Copied to %s", destPath)

	// Now delegate to the push command
	ui.Println()
	ui.PrintInfo("Pushing test to remote...")

	// Set up the push flags
	testsForce = createTestForce

	// Call the push function directly
	return runTestsPush(cmd, []string{testName})
}

// runCreateTestWithHotReload creates a test with hot reload enabled.
//
// This function:
//  1. Starts the dev server and creates a Cloudflare tunnel
//  2. Builds a deep link URL for the dev client
//  3. Creates the test with a NAVIGATE step as the first task
//  4. Opens the browser to the test editor
//  5. Keeps the dev server running until Ctrl+C
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (test name)
//
// Returns:
//   - error: Any error that occurred
func runCreateTestWithHotReload(cmd *cobra.Command, args []string) error {
	testName := args[0]

	// Validate test name
	if err := validateResourceName(testName, "test"); err != nil {
		ui.PrintError("%v", err)
		return err
	}

	ui.PrintBanner(version)

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds == nil || !creds.HasValidAuth() {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}
	apiKey, err := authMgr.GetActiveToken()
	if err != nil || apiKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")

	// Load project config (required for hot reload)
	cfg, err := config.LoadProjectConfig(configPath)
	if err != nil {
		ui.PrintError("Project not initialized. Run 'revyl init' first.")
		return fmt.Errorf("project not initialized")
	}

	// Check hot reload configuration
	if !cfg.HotReload.IsConfigured() {
		ui.PrintError("Hot reload not configured.")
		ui.Println()
		ui.PrintInfo("To set up hot reload, run:")
		ui.PrintDim("  revyl hotreload setup")
		return fmt.Errorf("hot reload not configured")
	}

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")

	// Select provider using registry
	registry := hotreload.DefaultRegistry()
	provider, providerCfg, err := registry.SelectProvider(&cfg.HotReload, createTestHotReloadProvider, cwd)
	if err != nil {
		ui.PrintError("Failed to select provider: %v", err)
		return err
	}

	if providerCfg == nil {
		ui.PrintError("Provider '%s' is not configured.", provider.Name())
		ui.Println()
		ui.PrintInfo("Run 'revyl hotreload setup' to configure hot reload.")
		return fmt.Errorf("provider not configured")
	}

	if !provider.IsSupported() {
		ui.PrintError("%s hot reload is not yet supported.", provider.DisplayName())
		return fmt.Errorf("%s not supported", provider.Name())
	}

	// Override port if specified via flag
	if createTestHotReloadPort != 8081 {
		providerCfg.Port = createTestHotReloadPort
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

	// Auto-detect app_id from config if not provided via flag
	appID := createTestAppID
	if appID == "" && cfg.Build.Platforms != nil {
		if platformCfg, ok := cfg.Build.Platforms[platform]; ok && platformCfg.AppID != "" {
			appID = platformCfg.AppID
			ui.PrintInfo("Using app from config: %s", appID)
		}
	}

	ui.Println()
	ui.PrintInfo("Starting hot reload for test creation...")
	ui.Println()

	// Start hot reload manager
	manager := hotreload.NewManager(provider.Name(), providerCfg, cwd)
	manager.SetLogCallback(func(msg string) {
		ui.PrintDim("  %s", msg)
	})

	// Create a context that can be cancelled
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	// Start the dev server and tunnel
	result, err := manager.Start(ctx)
	if err != nil {
		ui.PrintError("Failed to start hot reload: %v", err)
		return err
	}
	defer manager.Stop()

	ui.Println()
	ui.PrintSuccess("Hot reload ready!")
	ui.Println()
	ui.PrintInfo("Tunnel URL: %s", result.TunnelURL)
	ui.PrintInfo("Deep link URL:")
	ui.PrintDim("  %s", result.DeepLinkURL)
	ui.Println()

	// Create API client
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Build tasks array with NAVIGATE step as first task
	tasks := []map[string]interface{}{
		{
			"instruction": fmt.Sprintf("Open deep link to connect to dev server: %s", result.DeepLinkURL),
		},
	}

	ui.PrintInfo("Creating test '%s' with NAVIGATE step...", testName)

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

	var testID string

	if existingTestID != "" {
		if !createTestForce {
			ui.PrintError("A test named '%s' already exists (id: %s)", testName, existingTestID)
			ui.Println()
			ui.PrintInfo("To open the existing test, run:")
			ui.PrintDim("  revyl test open %s", testName)
			ui.Println()
			ui.PrintInfo("Or use --force to update the existing test.")
			return fmt.Errorf("test already exists")
		}
		// Use existing test
		ui.PrintInfo("Using existing test '%s' (id: %s)", testName, existingTestID)
		testID = existingTestID

		// Update the test's tasks with the new NAVIGATE step
		ui.StartSpinner("Updating test with hot reload step...")
		_, err := client.UpdateTest(cmd.Context(), &api.UpdateTestRequest{
			TestID: existingTestID,
			AppID:  appID,
			Force:  true,
		})
		ui.StopSpinner()

		if err != nil {
			ui.PrintWarning("Failed to update test: %v", err)
		}
	} else {
		// Create test on server
		ui.StartSpinner("Creating test on server...")
		createResp, err := client.CreateTest(cmd.Context(), &api.CreateTestRequest{
			Name:     testName,
			Platform: platform,
			Tasks:    tasks,
			AppID:    appID,
			OrgID:    creds.OrgID,
		})
		ui.StopSpinner()

		if err != nil {
			ui.PrintError("Failed to create test: %v", err)
			return err
		}
		testID = createResp.ID
		ui.PrintSuccess("Created test: %s (id: %s)", testName, testID)
	}

	// Add to config
	if cfg.Tests == nil {
		cfg.Tests = make(map[string]string)
	}
	cfg.Tests[testName] = testID

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

	// Open browser to test execute page
	executeURL := fmt.Sprintf("%s/tests/execute?testUid=%s", config.GetAppURL(devMode), testID)

	ui.Println()
	if !createTestNoOpen {
		ui.PrintInfo("Opening test editor...")
		ui.PrintLink("Test", executeURL)
		if err := ui.OpenBrowser(executeURL); err != nil {
			ui.PrintWarning("Could not open browser: %v", err)
			ui.PrintInfo("Open manually: %s", executeURL)
		}
	} else {
		ui.PrintInfo("Test URL: %s", executeURL)
	}

	ui.Println()
	ui.PrintSuccess("Hot reload running. Press Ctrl+C to stop.")
	ui.Println()
	ui.PrintInfo("To test hot reload:")
	ui.PrintDim("  1. Run the test from the browser")
	ui.PrintDim("  2. The first step will open the deep link")
	ui.PrintDim("  3. Your app will connect to the local dev server")
	ui.PrintDim("  4. Make changes locally and see them reflected immediately")
	ui.Println()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	ui.Println()
	ui.PrintInfo("Shutting down hot reload...")

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

	// Validate workflow name
	if err := validateResourceName(workflowName, "workflow"); err != nil {
		ui.PrintError("%v", err)
		return err
	}

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds == nil || !creds.HasValidAuth() {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}
	apiKey, err := authMgr.GetActiveToken()
	if err != nil || apiKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Create API client with dev mode support
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

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

	// Handle dry-run mode
	if createWorkflowDryRun {
		ui.Println()
		ui.PrintInfo("Dry-run mode - showing what would be created:")
		ui.Println()
		ui.PrintInfo("  Workflow Name: %s", workflowName)
		if len(testIDs) > 0 {
			ui.PrintInfo("  Tests:         %d test(s)", len(testIDs))
			for _, id := range testIDs {
				ui.PrintDim("    - %s", id)
			}
		} else {
			ui.PrintInfo("  Tests:         (none - add in browser)")
		}
		ui.PrintInfo("  Add to Config: %v", !createWorkflowNoSync)
		ui.PrintInfo("  Open Browser:  %v", !createWorkflowNoOpen)
		ui.Println()
		ui.PrintSuccess("Dry-run complete - no changes made")
		return nil
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
	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Configure in browser:", Command: fmt.Sprintf("revyl workflow open %s", workflowName)},
		{Label: "Run workflow:", Command: fmt.Sprintf("revyl workflow run %s", workflowName)},
	})

	return nil
}

// runCreateTestInteractive creates a test using interactive mode.
//
// This function:
//  1. Creates a test on the server
//  2. Starts a device session
//  3. Connects to the worker WebSocket
//  4. Runs the interactive REPL for step-by-step test creation
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (test name)
//
// Returns:
//   - error: Any error that occurred
func runCreateTestInteractive(cmd *cobra.Command, args []string) error {
	testName := args[0]

	// Validate test name
	if err := validateResourceName(testName, "test"); err != nil {
		ui.PrintError("%v", err)
		return err
	}

	ui.PrintBanner(version)

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds == nil || !creds.HasValidAuth() {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}
	apiKey, err := authMgr.GetActiveToken()
	if err != nil || apiKey == "" {
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
		cfg = &config.ProjectConfig{
			Tests:     make(map[string]string),
			Workflows: make(map[string]string),
		}
	}

	// Ensure maps are initialized
	if cfg.Tests == nil {
		cfg.Tests = make(map[string]string)
	}
	if cfg.Workflows == nil {
		cfg.Workflows = make(map[string]string)
	}

	// Determine platform
	platform := createTestPlatform
	if platform == "" {
		platformOptions := []string{"android", "ios"}
		idx, err := ui.PromptSelect("Select platform:", platformOptions)
		if err != nil {
			return fmt.Errorf("platform selection cancelled: %w", err)
		}
		platform = platformOptions[idx]
	}

	// Auto-detect app_id from config if not provided via flag
	appID := createTestAppID
	if appID == "" && cfg.Build.Platforms != nil {
		if platformCfg, ok := cfg.Build.Platforms[platform]; ok && platformCfg.AppID != "" {
			appID = platformCfg.AppID
			ui.PrintInfo("Using app from config: %s", appID)
		}
	}

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")

	// Create API client
	client := api.NewClientWithDevMode(apiKey, devMode)

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

	var testID string

	if existingTestID != "" {
		if !createTestForce {
			ui.PrintError("A test named '%s' already exists (id: %s)", testName, existingTestID)
			ui.Println()
			ui.PrintInfo("To open the existing test, run:")
			ui.PrintDim("  revyl test open %s", testName)
			ui.Println()
			ui.PrintInfo("Or use --force to update the existing test.")
			return fmt.Errorf("test already exists")
		}
		// Use existing test
		ui.PrintInfo("Using existing test '%s' (id: %s)", testName, existingTestID)
		testID = existingTestID

		// Update the test's app_id if we have one
		if appID != "" {
			ui.StartSpinner("Updating test app...")
			_, err := client.UpdateTest(cmd.Context(), &api.UpdateTestRequest{
				TestID: existingTestID,
				AppID:  appID,
				Force:  true,
			})
			ui.StopSpinner()

			if err != nil {
				ui.PrintWarning("Failed to update app: %v", err)
			}
		}
	} else {
		ui.PrintInfo("Creating test '%s' (%s)...", testName, platform)

		// Create test on server with empty tasks
		ui.StartSpinner("Creating test on server...")
		createResp, err := client.CreateTest(cmd.Context(), &api.CreateTestRequest{
			Name:     testName,
			Platform: platform,
			Tasks:    []interface{}{},
			AppID:    appID,
			OrgID:    creds.OrgID,
		})
		ui.StopSpinner()

		if err != nil {
			ui.PrintError("Failed to create test: %v", err)
			return err
		}

		testID = createResp.ID
		ui.PrintSuccess("Created test: %s (id: %s)", testName, testID)
	}

	// Add to config
	cfg.Tests[testName] = testID

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

	ui.Println()

	// Create interactive session
	sessionConfig := interactive.SessionConfig{
		TestID:       testID,
		TestName:     testName,
		Platform:     platform,
		APIKey:       apiKey,
		DevMode:      devMode,
		IsSimulation: true,
	}

	// If hot reload is also enabled, get the deep link URL
	if createTestHotReload {
		hotReloadURL, err := getHotReloadURL(cmd, cfg, cwd)
		if err != nil {
			ui.PrintWarning("Hot reload setup failed: %v", err)
			ui.PrintInfo("Continuing without hot reload...")
		} else {
			sessionConfig.HotReloadURL = hotReloadURL
			ui.PrintInfo("Hot reload enabled: %s", hotReloadURL)
		}
	}

	session := interactive.NewSession(sessionConfig)

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	// If --no-open is set, run without REPL (just output URL and wait for Ctrl+C)
	if createTestNoOpen {
		return runHeadlessSession(ctx, session)
	}

	// Create and run REPL
	repl := interactive.NewREPL(session)

	return repl.Run(ctx)
}

// getHotReloadURL starts hot reload and returns the deep link URL.
func getHotReloadURL(cmd *cobra.Command, cfg *config.ProjectConfig, cwd string) (string, error) {
	if !cfg.HotReload.IsConfigured() {
		return "", fmt.Errorf("hot reload not configured")
	}

	registry := hotreload.DefaultRegistry()
	provider, providerCfg, err := registry.SelectProvider(&cfg.HotReload, createTestHotReloadProvider, cwd)
	if err != nil {
		return "", err
	}

	if providerCfg == nil {
		return "", fmt.Errorf("provider not configured")
	}

	if !provider.IsSupported() {
		return "", fmt.Errorf("%s not supported", provider.Name())
	}

	// Override port if specified
	if createTestHotReloadPort != 8081 {
		providerCfg.Port = createTestHotReloadPort
	}

	manager := hotreload.NewManager(provider.Name(), providerCfg, cwd)

	result, err := manager.Start(cmd.Context())
	if err != nil {
		return "", err
	}

	return result.DeepLinkURL, nil
}

// resolveModuleForCreate resolves a module name or UUID to an ID and name.
// Used by the --module flag on test create.
func resolveModuleForCreate(cmd *cobra.Command, client *api.Client, nameOrID string) (moduleID, moduleName string, err error) {
	// If it looks like a UUID, try direct lookup
	if looksLikeUUID(nameOrID) {
		resp, err := client.GetModule(cmd.Context(), nameOrID)
		if err == nil {
			return resp.Result.ID, resp.Result.Name, nil
		}
	}

	// Search by name in module list
	listResp, err := client.ListModules(cmd.Context())
	if err != nil {
		return "", "", fmt.Errorf("failed to list modules: %w", err)
	}

	for _, m := range listResp.Result {
		if strings.EqualFold(m.Name, nameOrID) {
			return m.ID, m.Name, nil
		}
	}

	return "", "", fmt.Errorf("module \"%s\" not found", nameOrID)
}

// runHeadlessSession starts a device session without the interactive REPL.
// It outputs the frontend URL and waits for Ctrl+C to stop.
//
// Parameters:
//   - ctx: Context for cancellation
//   - session: The interactive session to run
//
// Returns:
//   - error: Any error that occurred
func runHeadlessSession(ctx context.Context, session *interactive.Session) error {
	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	// Start session
	ui.PrintInfo("Starting device...")
	if err := session.Start(ctx); err != nil {
		return fmt.Errorf("failed to start session: %w", err)
	}

	ui.PrintSuccess("Device ready!")
	ui.Println()

	// Display frontend URL
	frontendURL := session.GetFrontendURL()
	ui.PrintInfo("Live preview: %s", frontendURL)
	ui.Println()
	ui.PrintInfo("Press Ctrl+C to stop the session...")

	// Wait for signal
	select {
	case <-ctx.Done():
		ui.Println()
		ui.PrintInfo("Context cancelled, stopping session...")
	case sig := <-sigChan:
		ui.Println()
		ui.PrintInfo("Received %v, stopping session...", sig)
	}

	// Stop session
	if err := session.Stop(); err != nil {
		ui.PrintWarning("Error stopping session: %v", err)
	}

	ui.PrintSuccess("Session stopped.")
	return nil
}
