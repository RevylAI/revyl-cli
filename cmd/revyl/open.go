// Package main provides the open command for opening tests and workflows in browser.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/hotreload"
	_ "github.com/revyl/cli/internal/hotreload/providers" // Register providers
	"github.com/revyl/cli/internal/interactive"
	"github.com/revyl/cli/internal/ui"
)

var (
	// Hot reload flags for open test
	openTestHotReload         bool
	openTestHotReloadPort     int
	openTestHotReloadProvider string
	openTestHotReloadVariant  string

	// Interactive mode flag
	openTestInteractive bool

	// No-open flag (skip opening browser, just output URL)
	openTestNoOpen bool
)

// runOpenTest opens a test in the browser.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (test name or ID)
//
// Returns:
//   - error: Any error that occurred
func runOpenTest(cmd *cobra.Command, args []string) error {
	// If interactive mode is enabled, use the interactive flow
	if openTestInteractive {
		return runOpenTestInteractive(cmd, args)
	}

	// If hot reload is enabled, use the hot reload flow
	if openTestHotReload {
		return runOpenTestWithHotReload(cmd, args)
	}

	testNameOrID := args[0]

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds == nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")

	// Try to resolve name to ID from config
	var testID string
	cfg, err := config.LoadProjectConfig(configPath)
	if err == nil && cfg.Tests != nil {
		if id, ok := cfg.Tests[testNameOrID]; ok {
			testID = id
		}
	}

	// If not found in config, check if it looks like a UUID or search via API
	if testID == "" {
		// Check if it looks like a UUID (contains dashes and is ~36 chars)
		if looksLikeUUID(testNameOrID) {
			testID = testNameOrID
		} else {
			// Search via API
			devMode, _ := cmd.Flags().GetBool("dev")
			client := api.NewClientWithDevMode(creds.APIKey, devMode)

			ui.StartSpinner("Searching for test...")
			testsResp, err := client.ListOrgTests(cmd.Context(), 100, 0)
			ui.StopSpinner()

			if err != nil {
				ui.PrintError("Failed to search for test: %v", err)
				return err
			}

			for _, t := range testsResp.Tests {
				if t.Name == testNameOrID {
					testID = t.ID
					break
				}
			}

			if testID == "" {
				ui.PrintError("Test '%s' not found", testNameOrID)
				ui.PrintInfo("Use 'revyl test remote' to list available tests")
				return fmt.Errorf("test not found")
			}
		}
	}

	// Open browser (unless --no-open is set)
	devMode, _ := cmd.Flags().GetBool("dev")
	testURL := fmt.Sprintf("%s/tests/execute?testUid=%s", config.GetAppURL(devMode), testID)

	if openTestNoOpen {
		ui.PrintInfo("Test URL (browser not opened):")
		ui.PrintLink("Test", testURL)
		return nil
	}

	ui.PrintInfo("Opening test '%s'...", testNameOrID)
	ui.PrintLink("Test", testURL)

	if err := ui.OpenBrowser(testURL); err != nil {
		ui.PrintWarning("Could not open browser: %v", err)
		ui.PrintInfo("Open manually: %s", testURL)
	}

	return nil
}

// runOpenTestWithHotReload opens a test with hot reload enabled.
//
// This function:
//  1. Resolves the test ID
//  2. Starts the dev server and creates a Cloudflare tunnel
//  3. Opens the browser to the test editor
//  4. Prints the deep link URL for the user to add as a NAVIGATE step
//  5. Keeps the dev server running until Ctrl+C
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (test name or ID)
//
// Returns:
//   - error: Any error that occurred
func runOpenTestWithHotReload(cmd *cobra.Command, args []string) error {
	testNameOrID := args[0]

	ui.PrintBanner(version)

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds == nil || creds.APIKey == "" {
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

	// Resolve test ID
	var testID string
	if cfg.Tests != nil {
		if id, ok := cfg.Tests[testNameOrID]; ok {
			testID = id
		}
	}

	// If not found in config, check if it looks like a UUID or search via API
	if testID == "" {
		if looksLikeUUID(testNameOrID) {
			testID = testNameOrID
		} else {
			// Search via API
			client := api.NewClientWithDevMode(creds.APIKey, devMode)

			ui.StartSpinner("Searching for test...")
			testsResp, err := client.ListOrgTests(cmd.Context(), 100, 0)
			ui.StopSpinner()

			if err != nil {
				ui.PrintError("Failed to search for test: %v", err)
				return err
			}

			for _, t := range testsResp.Tests {
				if t.Name == testNameOrID {
					testID = t.ID
					break
				}
			}

			if testID == "" {
				ui.PrintError("Test '%s' not found", testNameOrID)
				ui.PrintInfo("Use 'revyl test remote' to list available tests")
				return fmt.Errorf("test not found")
			}
		}
	}

	// Select provider using registry
	registry := hotreload.DefaultRegistry()
	provider, providerCfg, err := registry.SelectProvider(&cfg.HotReload, openTestHotReloadProvider, cwd)
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
	if openTestHotReloadPort != 8081 {
		providerCfg.Port = openTestHotReloadPort
	}

	ui.Println()
	ui.PrintInfo("Starting hot reload...")
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
	ui.Println()
	ui.PrintInfo("Deep link URL (add as NAVIGATE step in your test):")
	ui.PrintDim("  %s", result.DeepLinkURL)
	ui.Println()

	// Open browser to test execute page
	testURL := fmt.Sprintf("%s/tests/execute?testUid=%s", config.GetAppURL(devMode), testID)

	ui.PrintInfo("Opening test '%s'...", testNameOrID)
	ui.PrintLink("Test", testURL)

	if err := ui.OpenBrowser(testURL); err != nil {
		ui.PrintWarning("Could not open browser: %v", err)
		ui.PrintInfo("Open manually: %s", testURL)
	}

	ui.Println()
	ui.PrintSuccess("Hot reload running. Press Ctrl+C to stop.")
	ui.Println()
	ui.PrintInfo("To use hot reload:")
	ui.PrintDim("  1. Add a NAVIGATE step with the deep link URL above")
	ui.PrintDim("  2. Run the test from the browser")
	ui.PrintDim("  3. The app will connect to your local dev server")
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

// runOpenWorkflow opens a workflow in the browser.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (workflow name or ID)
//
// Returns:
//   - error: Any error that occurred
func runOpenWorkflow(cmd *cobra.Command, args []string) error {
	workflowNameOrID := args[0]

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds == nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")

	// Try to resolve name to ID from config
	var workflowID string
	cfg, err := config.LoadProjectConfig(configPath)
	if err == nil && cfg.Workflows != nil {
		if id, ok := cfg.Workflows[workflowNameOrID]; ok {
			workflowID = id
		}
	}

	// If not found in config, assume it's an ID
	if workflowID == "" {
		workflowID = workflowNameOrID
	}

	// Open browser
	devMode, _ := cmd.Flags().GetBool("dev")
	workflowURL := fmt.Sprintf("%s/workflows/%s", config.GetAppURL(devMode), workflowID)

	ui.PrintInfo("Opening workflow '%s'...", workflowNameOrID)
	ui.PrintLink("Workflow", workflowURL)

	if err := ui.OpenBrowser(workflowURL); err != nil {
		ui.PrintWarning("Could not open browser: %v", err)
		ui.PrintInfo("Open manually: %s", workflowURL)
	}

	return nil
}

// runOpenTestInteractive opens a test in interactive mode for editing.
//
// This function:
//  1. Resolves the test ID
//  2. Fetches the existing test steps
//  3. Starts a device session
//  4. Connects to the worker WebSocket
//  5. Runs the interactive REPL with existing steps loaded
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (test name or ID)
//
// Returns:
//   - error: Any error that occurred
func runOpenTestInteractive(cmd *cobra.Command, args []string) error {
	testNameOrID := args[0]

	ui.PrintBanner(version)

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds == nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")

	// Load project config
	cfg, err := config.LoadProjectConfig(configPath)
	if err != nil {
		ui.PrintWarning("Project not initialized. Run 'revyl init' first for full functionality.")
		cfg = &config.ProjectConfig{
			Tests:     make(map[string]string),
			Workflows: make(map[string]string),
		}
	}

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")

	// Create API client
	client := api.NewClientWithDevMode(creds.APIKey, devMode)

	// Resolve test ID
	var testID string
	if cfg.Tests != nil {
		if id, ok := cfg.Tests[testNameOrID]; ok {
			testID = id
		}
	}

	// If not found in config, check if it looks like a UUID or search via API
	if testID == "" {
		if looksLikeUUID(testNameOrID) {
			testID = testNameOrID
		} else {
			// Search via API
			ui.StartSpinner("Searching for test...")
			testsResp, err := client.ListOrgTests(cmd.Context(), 100, 0)
			ui.StopSpinner()

			if err != nil {
				ui.PrintError("Failed to search for test: %v", err)
				return err
			}

			for _, t := range testsResp.Tests {
				if t.Name == testNameOrID {
					testID = t.ID
					break
				}
			}

			if testID == "" {
				ui.PrintError("Test '%s' not found", testNameOrID)
				ui.PrintInfo("Use 'revyl test remote' to list available tests")
				return fmt.Errorf("test not found")
			}
		}
	}

	// Fetch test details
	ui.StartSpinner("Loading test...")
	test, err := client.GetTest(cmd.Context(), testID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to load test: %v", err)
		return err
	}

	ui.PrintSuccess("Loaded test: %s (%s)", test.Name, test.Platform)
	ui.Println()

	// Create interactive session
	sessionConfig := interactive.SessionConfig{
		TestID:       testID,
		TestName:     test.Name,
		Platform:     test.Platform,
		APIKey:       creds.APIKey,
		DevMode:      devMode,
		IsSimulation: true,
	}

	// Track hot reload manager for cleanup
	var hotReloadManager *hotreload.Manager

	// If hot reload is also enabled, get the deep link URL
	if openTestHotReload && cfg.HotReload.IsConfigured() {
		registry := hotreload.DefaultRegistry()
		provider, providerCfg, err := registry.SelectProvider(&cfg.HotReload, openTestHotReloadProvider, cwd)
		if err == nil && providerCfg != nil && provider.IsSupported() {
			if openTestHotReloadPort != 8081 {
				providerCfg.Port = openTestHotReloadPort
			}

			hotReloadManager = hotreload.NewManager(provider.Name(), providerCfg, cwd)
			hotReloadManager.SetLogCallback(func(msg string) {
				ui.PrintDim("  %s", msg)
			})

			result, err := hotReloadManager.Start(cmd.Context())
			if err == nil {
				sessionConfig.HotReloadURL = result.DeepLinkURL
				ui.PrintInfo("Hot reload enabled: %s", result.DeepLinkURL)
				ui.Println()
			} else {
				ui.PrintWarning("Hot reload setup failed: %v", err)
				hotReloadManager = nil // Clear reference since start failed
			}
		}
	}

	// Ensure hot reload manager is cleaned up on exit
	if hotReloadManager != nil {
		defer func() {
			ui.PrintInfo("Stopping hot reload...")
			hotReloadManager.Stop()
		}()
	}

	session := interactive.NewSession(sessionConfig)

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	// If --no-open is set, run without REPL (just output URL and wait for Ctrl+C)
	if openTestNoOpen {
		return runOpenHeadlessSession(ctx, session)
	}

	// Create and run REPL with hot reload manager for coordinated cleanup
	repl := interactive.NewREPL(session)
	repl.SetHotReloadManager(hotReloadManager)

	return repl.Run(ctx)
}

// runOpenHeadlessSession starts a device session without the interactive REPL.
// It outputs the frontend URL and waits for Ctrl+C to stop.
//
// Parameters:
//   - ctx: Context for cancellation
//   - session: The interactive session to run
//
// Returns:
//   - error: Any error that occurred
func runOpenHeadlessSession(ctx context.Context, session *interactive.Session) error {
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
