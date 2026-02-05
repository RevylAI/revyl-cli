// Package main provides the hotreload command for configuring hot reload.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/hotreload"
	_ "github.com/revyl/cli/internal/hotreload/providers" // Register providers
	"github.com/revyl/cli/internal/ui"
)

// hotreloadCmd is the parent command for hot reload operations.
var hotreloadCmd = &cobra.Command{
	Use:   "hotreload",
	Short: "Hot reload commands for rapid development",
	Long: `Hot reload commands for rapid development iteration.

Hot reload enables near-instant testing by:
  - Starting a local dev server (Expo, Swift, or Android)
  - Creating a Cloudflare tunnel to expose it
  - Running tests against a pre-built development client

COMMANDS:
  setup     - Configure hot reload for this project

EXAMPLES:
  revyl hotreload setup              # Auto-detect and configure
  revyl hotreload setup --provider expo  # Configure specific provider`,
}

// hotreloadSetupCmd configures hot reload for a project.
var hotreloadSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Configure hot reload for this project",
	Long: `Configure hot reload for this project.

This command will:
  1. Detect your project type (Expo, Swift, Android)
  2. Extract project configuration (app scheme, etc.)
  3. Search for existing dev client builds
  4. Save configuration to .revyl/config.yaml

PREREQUISITES:
  - Authenticated: revyl auth login
  - Project initialized: revyl init

EXAMPLES:
  revyl hotreload setup              # Auto-detect and configure all providers
  revyl hotreload setup --provider expo  # Configure only Expo`,
	RunE: runHotreloadSetup,
}

var (
	hotreloadSetupProvider string
)

func init() {
	hotreloadCmd.AddCommand(hotreloadSetupCmd)

	hotreloadSetupCmd.Flags().StringVar(&hotreloadSetupProvider, "provider", "", "Specific provider to configure (expo, swift, android)")
}

// runHotreloadSetup executes the hot reload setup command.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments
//
// Returns:
//   - error: Any error that occurred during setup
func runHotreloadSetup(cmd *cobra.Command, args []string) error {
	ui.PrintBanner(version)

	// Check authentication
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Get working directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Load existing project config
	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	cfg, err := config.LoadProjectConfig(configPath)
	if err != nil {
		ui.PrintError("Project not initialized. Run 'revyl init' first.")
		return fmt.Errorf("project not initialized")
	}

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")

	// Create API client
	client := api.NewClientWithDevMode(creds.APIKey, devMode)

	ui.PrintInfo("Detecting project types...")
	ui.Println()

	// Detect providers
	registry := hotreload.DefaultRegistry()
	detections := registry.DetectAllProviders(cwd)

	if len(detections) == 0 {
		ui.PrintWarning("No compatible hot reload providers found.")
		ui.Println()
		ui.PrintInfo("Supported project types:")
		ui.PrintInfo("  - Expo (app.json + expo in package.json)")
		ui.PrintInfo("  - Swift/iOS (*.xcodeproj or *.xcworkspace) - coming soon")
		ui.PrintInfo("  - Android (build.gradle) - coming soon")
		return fmt.Errorf("no compatible providers found")
	}

	// Show detected providers
	ui.PrintInfo("Found %d compatible provider(s):", len(detections))
	for _, d := range detections {
		supportedStr := ""
		if !d.Provider.IsSupported() {
			supportedStr = " (coming soon)"
		}
		ui.PrintInfo("  ✓ %s (confidence: %.1f)%s", d.Provider.DisplayName(), d.Detection.Confidence, supportedStr)
		for _, indicator := range d.Detection.Indicators {
			ui.PrintDim("    - %s", indicator)
		}
	}
	ui.Println()

	// Filter to specific provider if requested
	if hotreloadSetupProvider != "" {
		found := false
		for _, d := range detections {
			if d.Provider.Name() == hotreloadSetupProvider {
				detections = []hotreload.ProviderDetection{d}
				found = true
				break
			}
		}
		if !found {
			ui.PrintError("Provider '%s' not detected in this project.", hotreloadSetupProvider)
			return fmt.Errorf("provider not found")
		}
	}

	// Setup each detected provider
	var configuredProviders []string
	for _, d := range detections {
		if !d.Provider.IsSupported() {
			ui.PrintWarning("%s hot reload is not yet supported. Skipping.", d.Provider.DisplayName())
			ui.Println()
			continue
		}

		ui.PrintInfo("Setting up %s...", d.Provider.DisplayName())

		result, err := hotreload.AutoSetup(cmd.Context(), client, hotreload.SetupOptions{
			WorkDir:          cwd,
			ExplicitProvider: d.Provider.Name(),
			Platform:         d.Detection.Platform,
		})
		if err != nil {
			ui.PrintWarning("Failed to setup %s: %v", d.Provider.DisplayName(), err)
			continue
		}

		// Show auto-detected info
		if result.ProjectInfo != nil {
			if result.ProjectInfo.Expo != nil && result.ProjectInfo.Expo.Scheme != "" {
				ui.PrintSuccess("Auto-detected app scheme: %s (from app.json)", result.ProjectInfo.Expo.Scheme)
			}
		}

		// Apply the setup result (without build ID - user specifies at runtime)
		hotreload.ApplySetupResult(cfg, result, len(configuredProviders) == 0)
		configuredProviders = append(configuredProviders, d.Provider.Name())

		ui.PrintSuccess("%s configured!", d.Provider.DisplayName())
		ui.Println()
	}

	if len(configuredProviders) == 0 {
		ui.PrintWarning("No providers were configured.")
		return fmt.Errorf("no providers configured")
	}

	// Ask about default provider if multiple configured
	if len(configuredProviders) > 1 {
		options := make([]ui.SelectOption, len(configuredProviders))
		for i, name := range configuredProviders {
			provider, _ := registry.GetProvider(name)
			options[i] = ui.SelectOption{
				Label: provider.DisplayName(),
				Value: name,
			}
		}

		idx, value, err := ui.Select("Set default provider:", options, 0)
		if err == nil && idx >= 0 {
			cfg.HotReload.Default = value
		}
	}

	// Save configuration
	if err := config.WriteProjectConfig(configPath, cfg); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	ui.PrintSuccess("Configuration saved!")
	ui.Println()
	ui.PrintInfo("To use hot reload, specify a build at runtime:")
	ui.Println()
	ui.PrintInfo("Run a test with hot reload:")
	ui.PrintDim("  revyl run test <test-name> --hotreload --variant <variant-name>")
	ui.Println()
	ui.PrintInfo("Create a new test with hot reload:")
	ui.PrintDim("  revyl create test <test-name> --hotreload --variant <variant-name>")
	ui.Println()
	ui.PrintInfo("Open an existing test in hot reload mode:")
	ui.PrintDim("  revyl open test <test-name> --hotreload --variant <variant-name>")
	ui.Println()
	ui.PrintInfo("Alternative: Use explicit build version ID")
	ui.PrintDim("  revyl run test <test-name> --hotreload --build-version-id <id>")
	ui.Println()
	if len(configuredProviders) > 1 {
		ui.PrintInfo("To specify a provider:")
		ui.PrintDim("  revyl run test <test-name> --hotreload --provider <provider> --variant <variant>")
		ui.Println()
	}
	ui.PrintInfo("To configure build variants, add to .revyl/config.yaml:")
	ui.PrintDim("  build:")
	ui.PrintDim("    variants:")
	ui.PrintDim("      ios-dev:")
	ui.PrintDim("        build_var_id: \"<your-build-var-id>\"")

	return nil
}
