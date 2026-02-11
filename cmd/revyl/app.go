// Package main provides app management commands for the Revyl CLI.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// appCmd is the parent command for app management.
var appCmd = &cobra.Command{
	Use:   "app",
	Short: "Manage apps",
	Long: `Manage apps for your organization.

An app is a named container that stores versions of your app binary.
Tests reference an app to know which binary to install on the device.

Commands:
  create - Create a new app
  list   - List all apps
  delete - Delete an app`,
}

// appCreateCmd creates a new app.
var appCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new app",
	Long: `Create a new app in your organization.

An app stores uploaded app builds so tests can reference them.

Examples:
  revyl app create --name "My App" --platform android
  revyl app create --name "iOS Dev Client" --platform ios
  revyl app create --name "My App" --platform android --json`,
	RunE: runAppCreate,
}

// appListCmd lists all apps.
var appListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all apps",
	Long: `List all apps in your organization.

Examples:
  revyl app list                    # List all apps
  revyl app list --platform android # Filter by platform
  revyl app list --json             # JSON output`,
	RunE: runAppList,
}

// appDeleteCmd deletes an app.
var appDeleteCmd = &cobra.Command{
	Use:   "delete <name|id>",
	Short: "Delete an app",
	Long: `Delete an app and all its build versions.

Examples:
  revyl app delete "My App iOS"         # Delete by name
  revyl app delete <uuid>               # Delete by ID
  revyl app delete "My App iOS" --force  # Skip confirmation`,
	Args: cobra.ExactArgs(1),
	RunE: runAppDelete,
}

var (
	// app create flags
	appCreateName     string
	appCreatePlatform string
	appCreateJSON     bool

	// app list flags
	appListPlatform string
	appListJSON     bool

	// app delete flags
	appDeleteForce bool
)

func init() {
	appCmd.AddCommand(appCreateCmd)
	appCmd.AddCommand(appListCmd)
	appCmd.AddCommand(appDeleteCmd)

	appCreateCmd.Flags().StringVar(&appCreateName, "name", "", "Name for the app (required)")
	appCreateCmd.Flags().StringVar(&appCreatePlatform, "platform", "", "Target platform: ios or android (required)")
	appCreateCmd.Flags().BoolVar(&appCreateJSON, "json", false, "Output result as JSON")
	_ = appCreateCmd.MarkFlagRequired("name")
	_ = appCreateCmd.MarkFlagRequired("platform")

	appListCmd.Flags().StringVar(&appListPlatform, "platform", "", "Filter by platform (android, ios)")
	appListCmd.Flags().BoolVar(&appListJSON, "json", false, "Output results as JSON")

	appDeleteCmd.Flags().BoolVarP(&appDeleteForce, "force", "f", false, "Skip confirmation prompt")
}

// runAppCreate creates a new app in the organization.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (unused)
//
// Returns:
//   - error: Any error that occurred during creation
func runAppCreate(cmd *cobra.Command, args []string) error {
	// Check authentication
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Validate platform
	platform := strings.ToLower(appCreatePlatform)
	if platform != "ios" && platform != "android" {
		ui.PrintError("Invalid platform '%s'. Must be 'ios' or 'android'.", appCreatePlatform)
		return fmt.Errorf("invalid platform: %s", appCreatePlatform)
	}

	// Check if --json flag is set (either local or global)
	jsonOutput := appCreateJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	// Create API client
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	if !jsonOutput {
		ui.StartSpinner("Creating app...")
	}

	result, err := client.CreateApp(cmd.Context(), &api.CreateAppRequest{
		Name:     appCreateName,
		Platform: platform,
	})

	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to create app: %v", err)
		return err
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.Println()
	ui.PrintSuccess("Created app: %s", result.Name)
	ui.PrintInfo("  App ID:    %s", result.ID)
	ui.PrintInfo("  Platform:  %s", platform)
	ui.Println()

	// Offer to save app_id to the matching config platform entry
	cwd, err := os.Getwd()
	if err == nil {
		configPath := filepath.Join(cwd, ".revyl", "config.yaml")
		cfg, cfgErr := config.LoadProjectConfig(configPath)

		if cfgErr == nil && cfg != nil {
			// Check if there's a matching platform entry
			if cfg.Build.Platforms != nil {
				if _, hasPlatform := cfg.Build.Platforms[platform]; hasPlatform {
					save, promptErr := ui.PromptConfirm(fmt.Sprintf("Save to .revyl/config.yaml for platform '%s'?", platform), true)
					if promptErr == nil && save {
						platformCfg := cfg.Build.Platforms[platform]
						platformCfg.AppID = result.ID
						cfg.Build.Platforms[platform] = platformCfg
						if writeErr := config.WriteProjectConfig(configPath, cfg); writeErr != nil {
							ui.PrintWarning("Failed to save config: %v", writeErr)
						} else {
							ui.PrintSuccess("Saved app_id to build.platforms.%s", platform)
						}
					}
				}
			}
		}
	}

	ui.Println()
	ui.PrintInfo("Next:")
	ui.PrintDim("  revyl build upload --platform %s        Upload a build to this app", platform)

	return nil
}

// runAppList lists all apps in the organization.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (unused)
//
// Returns:
//   - error: Any error that occurred while listing
func runAppList(cmd *cobra.Command, args []string) error {
	// Check authentication
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Check if --json flag is set (either local or global)
	jsonOutput := appListJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	// Create API client
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	if !jsonOutput {
		ui.StartSpinner("Fetching apps...")
	}
	result, err := client.ListApps(cmd.Context(), appListPlatform, 1, 50)
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to list apps: %v", err)
		return err
	}

	if jsonOutput {
		output := map[string]interface{}{
			"apps":  result.Items,
			"count": len(result.Items),
			"total": result.Total,
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(result.Items) == 0 {
		ui.PrintInfo("No apps found in your organization")
		ui.Println()
		ui.PrintInfo("Create one with:")
		ui.PrintDim("  revyl app create --name \"My App\" --platform <ios|android>")
		return nil
	}

	ui.Println()
	ui.PrintInfo("Apps (%d total):", result.Total)
	ui.Println()

	// Create table with dynamic column widths
	table := ui.NewTable("NAME", "PLATFORM", "BUILDS", "LATEST", "APP ID")
	table.SetMinWidth(0, 20) // NAME
	table.SetMinWidth(1, 8)  // PLATFORM
	table.SetMinWidth(4, 36) // APP ID - UUIDs are 36 chars

	for _, app := range result.Items {
		latestVer := "-"
		if app.LatestVersion != "" {
			latestVer = app.LatestVersion
		}
		table.AddRow(app.Name, app.Platform, fmt.Sprintf("%d", app.VersionsCount), latestVer, app.ID)
	}

	table.Render()

	ui.Println()
	ui.PrintDim("  revyl build list --app <APP ID>              List builds for an app")
	ui.PrintDim("  revyl build upload --platform <key>          Upload a new build")

	return nil
}

// runAppDelete deletes an app by name or ID.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (name or ID)
//
// Returns:
//   - error: Any error that occurred during deletion
func runAppDelete(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	// Determine JSON output mode early so human output can be suppressed
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	// Check authentication
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Create API client
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Resolve name or ID to both values
	appID, appName, err := resolveAppNameOrID(cmd, client, nameOrID)
	if err != nil {
		return err
	}

	// Load project config to check for references
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	cfg, cfgErr := config.LoadProjectConfig(configPath)

	// Check if app is referenced in config
	var configRefs []string
	if cfg != nil && cfg.Build.Platforms != nil {
		for platformName, platformCfg := range cfg.Build.Platforms {
			if platformCfg.AppID == appID {
				configRefs = append(configRefs, platformName)
			}
		}
	}

	// Show what will be deleted
	if !appDeleteForce {
		ui.Println()
		ui.PrintInfo("Delete app \"%s\"?", appName)
		ui.PrintDim("  - Remote: will delete app and ALL build versions")
		if len(configRefs) > 0 {
			ui.PrintDim("  - Config: will remove app_id from platforms: %v", configRefs)
		}

		ui.Println()
		confirmed, err := ui.PromptConfirm("Are you sure?", false)
		if err != nil || !confirmed {
			ui.PrintInfo("Cancelled")
			return nil
		}
	}

	// Delete from remote
	resp, err := client.DeleteApp(cmd.Context(), appID)
	if err != nil {
		ui.PrintError("Failed to delete app: %v", err)
		return err
	}

	if !jsonOutput {
		ui.PrintSuccess("Deleted from Revyl")
		if resp.DetachedTests > 0 {
			ui.PrintInfo("Detached %d test(s) from this app", resp.DetachedTests)
		}
	}

	// Remove from config
	if len(configRefs) > 0 && cfgErr == nil {
		for _, platformName := range configRefs {
			platformCfg := cfg.Build.Platforms[platformName]
			platformCfg.AppID = ""
			cfg.Build.Platforms[platformName] = platformCfg
		}
		if err := config.WriteProjectConfig(configPath, cfg); err != nil {
			if !jsonOutput {
				ui.PrintWarning("Failed to update config: %v", err)
			}
		} else if !jsonOutput {
			ui.PrintSuccess("Removed app_id from config platforms")
		}
	}

	// Handle JSON output
	if jsonOutput {
		output := map[string]interface{}{
			"success":  true,
			"app_name": appName,
			"app_id":   appID,
		}
		data, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON output: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	ui.Println()
	ui.PrintSuccess("App \"%s\" deleted successfully.", appName)
	return nil
}

// resolveAppNameOrID resolves an app name or ID to both values.
//
// Parameters:
//   - cmd: The cobra command
//   - client: The API client
//   - nameOrID: The name or UUID to resolve
//
// Returns:
//   - appID: The resolved app ID
//   - appName: The resolved app name
//   - error: Any error that occurred
func resolveAppNameOrID(cmd *cobra.Command, client *api.Client, nameOrID string) (appID, appName string, err error) {
	// Check if it looks like a UUID
	if looksLikeUUID(nameOrID) {
		app, err := client.GetApp(cmd.Context(), nameOrID)
		if err == nil {
			return nameOrID, app.Name, nil
		}
	}

	// Search by name
	result, err := client.ListApps(cmd.Context(), "", 1, 100)
	if err != nil {
		return "", "", fmt.Errorf("failed to list apps: %w", err)
	}

	for _, app := range result.Items {
		if app.Name == nameOrID {
			return app.ID, app.Name, nil
		}
	}

	return "", "", fmt.Errorf("app \"%s\" not found", nameOrID)
}
