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
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/interactive"
	"github.com/revyl/cli/internal/ui"
)

var (
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

	testNameOrID := args[0]

	// Check authentication
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Try to resolve name to ID from local YAML
	testsDir := filepath.Join(cwd, ".revyl", "tests")
	var testID string
	if id, _ := config.GetLocalTestRemoteID(testsDir, testNameOrID); id != "" {
		testID = id
	}

	// If not found in config, check if it looks like a UUID or search via API
	if testID == "" {
		// Check if it looks like a UUID (contains dashes and is ~36 chars)
		if looksLikeUUID(testNameOrID) {
			testID = testNameOrID
		} else {
			// Search via API
			devMode, _ := cmd.Flags().GetBool("dev")
			client := api.NewClientWithDevMode(apiKey, devMode)

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
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)
	workflowID, _, err := resolveWorkflowID(cmd.Context(), workflowNameOrID, nil, client)
	if err != nil {
		ui.PrintError("%v", err)
		ui.PrintInfo("Use 'revyl workflow create <name>' to create a new workflow")
		return fmt.Errorf("workflow not found")
	}

	// Open browser
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
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	if _, err := config.LoadProjectConfig(configPath); err != nil {
		ui.PrintWarning("Project not initialized. Run 'revyl init' first for full functionality.")
	}

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")

	// Create API client
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Resolve test ID
	testsDir := filepath.Join(cwd, ".revyl", "tests")
	var testID string
	if id, _ := config.GetLocalTestRemoteID(testsDir, testNameOrID); id != "" {
		testID = id
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
		APIKey:       apiKey,
		DevMode:      devMode,
		IsSimulation: true,
	}

	session := interactive.NewSession(sessionConfig)

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	// If --no-open is set, run without REPL (just output URL and wait for Ctrl+C)
	if openTestNoOpen {
		return runOpenHeadlessSession(ctx, session)
	}

	// Create and run REPL
	repl := interactive.NewREPL(session)

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
