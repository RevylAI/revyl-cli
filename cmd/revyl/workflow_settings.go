// Package main provides workflow settings commands for app and location configuration.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// Workflow location flags
var (
	workflowLocationLat float64
	workflowLocationLng float64
)

// Workflow app flags
var (
	workflowAppIOS     string
	workflowAppAndroid string
)

// --- Location commands ---

var workflowLocationCmd = &cobra.Command{
	Use:   "location",
	Short: "Manage workflow GPS location override",
	Long: `Manage the stored GPS location override for a workflow.

When set, this location overrides individual test locations for all tests
in the workflow.

COMMANDS:
  set     - Set the GPS location override
  clear   - Remove the location override
  show    - Show current location config

EXAMPLES:
  revyl workflow location set my-workflow --lat 37.7749 --lng -122.4194
  revyl workflow location show my-workflow
  revyl workflow location clear my-workflow`,
}

var workflowLocationSetCmd = &cobra.Command{
	Use:   "set <name|id>",
	Short: "Set the GPS location override for a workflow",
	Long: `Set a stored GPS location that overrides individual test locations.

Both --lat and --lng are required.

EXAMPLES:
  revyl workflow location set my-workflow --lat 37.7749 --lng -122.4194`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkflowLocationSet,
}

var workflowLocationClearCmd = &cobra.Command{
	Use:   "clear <name|id>",
	Short: "Remove the location override from a workflow",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowLocationClear,
}

var workflowLocationShowCmd = &cobra.Command{
	Use:   "show <name|id>",
	Short: "Show current location config for a workflow",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowLocationShow,
}

// --- App commands ---

var workflowAppCmd = &cobra.Command{
	Use:   "app",
	Short: "Manage workflow app overrides",
	Long: `Manage the stored app overrides for a workflow.

When set, these apps override individual test app configurations for
matching platforms across all tests in the workflow.

COMMANDS:
  set     - Set app overrides (per platform)
  clear   - Remove app overrides
  show    - Show current app config

EXAMPLES:
  revyl workflow app set my-workflow --ios <app-id> --android <app-id>
  revyl workflow app show my-workflow
  revyl workflow app clear my-workflow`,
}

var workflowAppSetCmd = &cobra.Command{
	Use:   "set <name|id>",
	Short: "Set app overrides for a workflow",
	Long: `Set stored app overrides that apply to all tests in the workflow.

At least one of --ios or --android is required.

EXAMPLES:
  revyl workflow app set my-workflow --ios <app-id>
  revyl workflow app set my-workflow --android <app-id>
  revyl workflow app set my-workflow --ios <ios-id> --android <android-id>`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkflowAppSet,
}

var workflowAppClearCmd = &cobra.Command{
	Use:   "clear <name|id>",
	Short: "Remove app overrides from a workflow",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowAppClear,
}

var workflowAppShowCmd = &cobra.Command{
	Use:   "show <name|id>",
	Short: "Show current app config for a workflow",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowAppShow,
}

func init() {
	// Location subcommands
	workflowLocationCmd.AddCommand(workflowLocationSetCmd)
	workflowLocationCmd.AddCommand(workflowLocationClearCmd)
	workflowLocationCmd.AddCommand(workflowLocationShowCmd)

	workflowLocationSetCmd.Flags().Float64Var(&workflowLocationLat, "lat", 0, "Latitude (-90 to 90)")
	workflowLocationSetCmd.Flags().Float64Var(&workflowLocationLng, "lng", 0, "Longitude (-180 to 180)")
	_ = workflowLocationSetCmd.MarkFlagRequired("lat")
	_ = workflowLocationSetCmd.MarkFlagRequired("lng")

	// App subcommands
	workflowAppCmd.AddCommand(workflowAppSetCmd)
	workflowAppCmd.AddCommand(workflowAppClearCmd)
	workflowAppCmd.AddCommand(workflowAppShowCmd)

	workflowAppSetCmd.Flags().StringVar(&workflowAppIOS, "ios", "", "iOS app ID")
	workflowAppSetCmd.Flags().StringVar(&workflowAppAndroid, "android", "", "Android app ID")
}

// wfSettingsSetupClient creates an API client and resolves workflow ID from name/alias.
// Returns workflowID, client, and error.
// This is a package-level var so it can be overridden in tests.
var wfSettingsSetupClient = wfSettingsSetupClientDefault

func wfSettingsSetupClientDefault(cmd *cobra.Command, nameOrID string) (string, *api.Client, error) {
	apiKey, err := getAPIKey()
	if err != nil {
		return "", nil, err
	}

	cwd, _ := os.Getwd()
	cfg, _ := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	workflowID, _, err := resolveWorkflowID(cmd.Context(), nameOrID, cfg, client)
	if err != nil {
		ui.PrintError("%v", err)
		return "", nil, fmt.Errorf("workflow not found")
	}

	return workflowID, client, nil
}

// --- Location handlers ---

func runWorkflowLocationSet(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	// Validate ranges
	if workflowLocationLat < -90 || workflowLocationLat > 90 {
		return fmt.Errorf("latitude must be between -90 and 90 (got %s)", strconv.FormatFloat(workflowLocationLat, 'f', -1, 64))
	}
	if workflowLocationLng < -180 || workflowLocationLng > 180 {
		return fmt.Errorf("longitude must be between -180 and 180 (got %s)", strconv.FormatFloat(workflowLocationLng, 'f', -1, 64))
	}

	workflowID, client, err := wfSettingsSetupClient(cmd, nameOrID)
	if err != nil {
		return err
	}

	locationConfig := map[string]interface{}{
		"latitude":  workflowLocationLat,
		"longitude": workflowLocationLng,
	}

	ui.StartSpinner("Updating location config...")
	err = client.UpdateWorkflowLocationConfig(cmd.Context(), workflowID, locationConfig, true)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to update location config: %v", err)
		return err
	}

	ui.PrintSuccess("Location set for workflow '%s': %.6f, %.6f", nameOrID, workflowLocationLat, workflowLocationLng)
	ui.PrintInfo("Override enabled: all tests in this workflow will use this location")
	return nil
}

func runWorkflowLocationClear(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	workflowID, client, err := wfSettingsSetupClient(cmd, nameOrID)
	if err != nil {
		return err
	}

	ui.StartSpinner("Clearing location config...")
	err = client.UpdateWorkflowLocationConfig(cmd.Context(), workflowID, nil, false)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to clear location config: %v", err)
		return err
	}

	ui.PrintSuccess("Location override cleared for workflow '%s'", nameOrID)
	return nil
}

func runWorkflowLocationShow(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	workflowID, client, err := wfSettingsSetupClient(cmd, nameOrID)
	if err != nil {
		return err
	}

	ui.StartSpinner("Fetching workflow...")
	wf, err := client.GetWorkflow(cmd.Context(), workflowID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to get workflow: %v", err)
		return err
	}

	ui.Println()
	ui.PrintInfo("Location Config for '%s'", nameOrID)
	ui.Println()

	if !wf.OverrideLocation || wf.LocationConfig == nil {
		ui.PrintInfo("  Override: disabled")
		ui.PrintInfo("  Location: (not set)")
	} else {
		ui.PrintInfo("  Override: enabled")
		lat, _ := wf.LocationConfig["latitude"]
		lng, _ := wf.LocationConfig["longitude"]
		ui.PrintInfo("  Latitude:  %v", lat)
		ui.PrintInfo("  Longitude: %v", lng)
	}

	ui.Println()
	return nil
}

// --- App handlers ---

func runWorkflowAppSet(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	if workflowAppIOS == "" && workflowAppAndroid == "" {
		ui.PrintError("At least one of --ios or --android is required")
		return fmt.Errorf("no app specified")
	}

	workflowID, client, err := wfSettingsSetupClient(cmd, nameOrID)
	if err != nil {
		return err
	}

	// Validate that app IDs exist before setting config
	if workflowAppIOS != "" {
		ui.StartSpinner("Validating iOS app...")
		app, appErr := client.GetApp(cmd.Context(), workflowAppIOS)
		ui.StopSpinner()
		if appErr != nil {
			ui.PrintError("iOS app '%s' not found", workflowAppIOS)
			return fmt.Errorf("invalid iOS app ID")
		}
		ui.PrintInfo("Found iOS app: %s (%s)", app.Name, app.Platform)
	}
	if workflowAppAndroid != "" {
		ui.StartSpinner("Validating Android app...")
		app, appErr := client.GetApp(cmd.Context(), workflowAppAndroid)
		ui.StopSpinner()
		if appErr != nil {
			ui.PrintError("Android app '%s' not found", workflowAppAndroid)
			return fmt.Errorf("invalid Android app ID")
		}
		ui.PrintInfo("Found Android app: %s (%s)", app.Name, app.Platform)
	}

	// Fetch existing config to merge (don't clobber the other platform)
	buildConfig := map[string]interface{}{}
	wf, wfErr := client.GetWorkflow(cmd.Context(), workflowID)
	if wfErr == nil && wf.BuildConfig != nil {
		buildConfig = wf.BuildConfig
	}
	if workflowAppIOS != "" {
		buildConfig["ios_build"] = map[string]interface{}{
			"app_id": workflowAppIOS,
		}
	}
	if workflowAppAndroid != "" {
		buildConfig["android_build"] = map[string]interface{}{
			"app_id": workflowAppAndroid,
		}
	}

	ui.StartSpinner("Updating app config...")
	err = client.UpdateWorkflowBuildConfig(cmd.Context(), workflowID, buildConfig, true)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to update app config: %v", err)
		return err
	}

	ui.PrintSuccess("App config set for workflow '%s'", nameOrID)
	if workflowAppIOS != "" {
		ui.PrintInfo("  iOS App: %s", workflowAppIOS)
	}
	if workflowAppAndroid != "" {
		ui.PrintInfo("  Android App: %s", workflowAppAndroid)
	}
	ui.PrintInfo("Override enabled: all tests in this workflow will use these apps")
	return nil
}

func runWorkflowAppClear(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	workflowID, client, err := wfSettingsSetupClient(cmd, nameOrID)
	if err != nil {
		return err
	}

	ui.StartSpinner("Clearing app config...")
	err = client.UpdateWorkflowBuildConfig(cmd.Context(), workflowID, nil, false)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to clear app config: %v", err)
		return err
	}

	ui.PrintSuccess("App override cleared for workflow '%s'", nameOrID)
	return nil
}

func runWorkflowAppShow(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	workflowID, client, err := wfSettingsSetupClient(cmd, nameOrID)
	if err != nil {
		return err
	}

	ui.StartSpinner("Fetching workflow...")
	wf, err := client.GetWorkflow(cmd.Context(), workflowID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to get workflow: %v", err)
		return err
	}

	ui.Println()
	ui.PrintInfo("App Config for '%s'", nameOrID)
	ui.Println()

	if !wf.OverrideBuildConfig || wf.BuildConfig == nil {
		ui.PrintInfo("  Override: disabled")
		ui.PrintInfo("  iOS App:     (not set)")
		ui.PrintInfo("  Android App: (not set)")
	} else {
		ui.PrintInfo("  Override: enabled")

		iosApp := "(not set)"
		if iosBuild, ok := wf.BuildConfig["ios_build"].(map[string]interface{}); ok {
			if appID, ok := iosBuild["app_id"].(string); ok && appID != "" {
				iosApp = appID
			}
		}

		androidApp := "(not set)"
		if androidBuild, ok := wf.BuildConfig["android_build"].(map[string]interface{}); ok {
			if appID, ok := androidBuild["app_id"].(string); ok && appID != "" {
				androidApp = appID
			}
		}

		ui.PrintInfo("  iOS App:     %s", iosApp)
		ui.PrintInfo("  Android App: %s", androidApp)
	}

	ui.Println()
	return nil
}
