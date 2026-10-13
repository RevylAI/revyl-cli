// Package main provides workflow settings commands for app, location, and run configuration.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/execution"
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
  set      - Set app overrides (per platform)
  clear    - Remove app overrides
  disable  - Turn app overrides off and keep them
  enable   - Turn saved app overrides back on
  show     - Show current app config

EXAMPLES:
  revyl workflow app set my-workflow --ios <app-id> --android <app-id>
  revyl workflow app show my-workflow
  revyl workflow app disable my-workflow
  revyl workflow app enable my-workflow
  revyl workflow app clear my-workflow`,
}

var workflowAppSetCmd = &cobra.Command{
	Use:   "set <name|id>",
	Short: "Set app overrides for a workflow",
	Long: `Set stored app overrides that apply to all tests in the workflow.

Specify an app or selector for at least one platform. Omit the app to edit
that platform's existing selection.

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

var workflowAppDisableCmd = &cobra.Command{
	Use:   "disable <name|id>",
	Short: "Turn a workflow's app overrides off and keep them",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowAppDisable,
}

var workflowAppEnableCmd = &cobra.Command{
	Use: "enable <name|id>", Short: "Turn a workflow's saved app overrides back on", Args: cobra.ExactArgs(1), RunE: runWorkflowAppEnable,
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
	workflowAppCmd.AddCommand(workflowAppDisableCmd)
	workflowAppCmd.AddCommand(workflowAppEnableCmd)

	workflowAppSetCmd.Flags().StringVar(&workflowAppIOS, "ios", "", "iOS app ID")
	workflowAppSetCmd.Flags().StringVar(&workflowAppAndroid, "android", "", "Android app ID")
	for _, platform := range []string{"ios", "android"} {
		workflowAppSetCmd.Flags().String(platform+"-branch", "", "Saved Revyl branch for this platform")
		workflowAppSetCmd.Flags().String(platform+"-build-selection", "", "Saved build choice: inherit (Automatic: the branch's default build), latest or pinned")
		workflowAppSetCmd.Flags().String(platform+"-build-id", "", "Exact build ID to save for this platform")
		workflowAppSetCmd.Flags().Bool(platform+"-automatic", false, "Use Default branch: the app's default branch and its default build")
		workflowAppSetCmd.Flags().Bool(platform+"-"+allBranchesFlagName, false, "Save All branches · Latest build for this platform: each run uses the newest build across all of the app's branches")
	}
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

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	workflowID, _, err := resolveWorkflowID(cmd.Context(), nameOrID, nil, client)
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

	if workflowAppIOS == "" && workflowAppAndroid == "" && !workflowPlatformSelectorChanged(cmd, "ios") && !workflowPlatformSelectorChanged(cmd, "android") {
		return fmt.Errorf("no app specified; provide an app or build selector for at least one platform")
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

	wf, err := client.GetWorkflow(cmd.Context(), workflowID)
	if err != nil {
		return fmt.Errorf("read saved workflow selectors: %w", err)
	}
	buildConfig := wf.BuildConfig
	if buildConfig == nil {
		buildConfig = map[string]interface{}{}
	}
	setSelections := map[string]map[string]interface{}{}
	for _, target := range []struct{ platform, appID string }{{"ios", workflowAppIOS}, {"android", workflowAppAndroid}} {
		if target.appID == "" && !workflowPlatformSelectorChanged(cmd, target.platform) {
			continue
		}
		slot := target.platform + "_build"
		value, _ := buildConfig[slot].(map[string]interface{})
		currentAppID, _ := value["app_id"].(string)
		if target.appID != "" && target.appID != currentAppID {
			value = map[string]interface{}{"app_id": target.appID}
		}
		if value == nil {
			return fmt.Errorf("set --%s <app-id> before choosing its branch", target.platform)
		}
		appID, _ := value["app_id"].(string)
		if appID == "" {
			return fmt.Errorf("workflow %s app ID is missing", target.platform)
		}
		if workflowPlatformSelectorChanged(cmd, target.platform) {
			branchName, _ := cmd.Flags().GetString(target.platform + "-branch")
			mode, _ := cmd.Flags().GetString(target.platform + "-build-selection")
			buildID, _ := cmd.Flags().GetString(target.platform + "-build-id")
			automatic, _ := cmd.Flags().GetBool(target.platform + "-automatic")
			allBranches, _ := cmd.Flags().GetBool(target.platform + "-" + allBranchesFlagName)
			branchID, _ := value["branch_id"].(string)
			mode, err := resolveAllBranchesSelection(target.platform+"-"+allBranchesFlagName, allBranches, target.platform+"-build-selection", mode,
				changedFlagConflicts(cmd, target.platform+"-branch", target.platform+"-build-id", target.platform+"-automatic")...)
			if err != nil {
				return err
			}
			switch {
			case automatic:
				if branchName != "" || mode != "" || buildID != "" {
					return fmt.Errorf("--%s-automatic cannot be combined with another selector", target.platform)
				}
				branchID, mode = "", "inherit"
			case api.BuildSelectionMode(mode) == api.BuildSelectionModeLatestAllBranches:
				branchID = ""
			case branchName != "" || buildID != "":
				branchID = ""
			}
			selection, err := execution.ResolveSavedBuildSelection(cmd.Context(), client, appID, branchName, branchID, mode, buildID)
			if err != nil {
				return fmt.Errorf("%s selector: %w", target.platform, err)
			}
			if selection == nil {
				return fmt.Errorf("%s selector is empty", target.platform)
			}
			value["branch_id"] = selection.BranchID
			value["build_selection"] = selection.BuildSelection
			value["pinned_build_id"] = selection.PinnedBuildID
			delete(value, "pinned_version")
		}
		buildConfig[slot] = value
		setSelections[target.platform] = value
	}

	ui.StartSpinner("Updating app config...")
	err = client.UpdateWorkflowBuildConfig(cmd.Context(), workflowID, buildConfig, true)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to update app config: %v", err)
		return err
	}
	if jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json"); jsonOutput {
		return writeWorkflowAppSelection(cmd, workflowID, true, buildConfig)
	}

	ui.PrintSuccess("App config set for workflow '%s'", nameOrID)
	for _, platform := range workflowAppPlatforms {
		if value, set := setSelections[platform.key]; set {
			printWorkflowPlatformSelection(cmd.Context(), client, platform.label, value)
		}
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
	if jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json"); jsonOutput {
		return writeWorkflowAppSelection(cmd, workflowID, false, nil)
	}

	ui.PrintSuccess("App override cleared for workflow '%s'", nameOrID)
	return nil
}

func runWorkflowAppDisable(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	workflowID, client, err := wfSettingsSetupClient(cmd, nameOrID)
	if err != nil {
		return err
	}

	ui.StartSpinner("Disabling app override...")
	err = client.SetWorkflowBuildOverrideEnabled(cmd.Context(), workflowID, false)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to disable app override: %v", err)
		return err
	}
	if jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json"); jsonOutput {
		return writeWorkflowAppSelection(cmd, workflowID, false, nil)
	}

	ui.PrintSuccess("App override disabled for workflow '%s'; saved selectors retained", nameOrID)
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
	if jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json"); jsonOutput {
		return writeWorkflowAppSelection(cmd, workflowID, wf.OverrideBuildConfig, wf.BuildConfig)
	}

	ui.Println()
	ui.PrintInfo("App Config for '%s'", nameOrID)
	ui.Println()

	switch {
	case wf.OverrideBuildConfig:
		ui.PrintInfo("  Override: enabled")
	case workflowHasSavedApps(wf.BuildConfig):
		ui.PrintInfo("  Override: disabled (saved selectors retained)")
	default:
		ui.PrintInfo("  Override: disabled")
	}
	for _, platform := range workflowAppPlatforms {
		value, _ := wf.BuildConfig[platform.key+"_build"].(map[string]interface{})
		if value == nil {
			ui.PrintInfo("  %-12s (not set)", platform.label)
			continue
		}
		printWorkflowPlatformSelection(cmd.Context(), client, platform.label, value)
	}

	ui.Println()
	return nil
}

var workflowAppPlatforms = []struct{ key, label string }{{"ios", "iOS App:"}, {"android", "Android App:"}}

// printWorkflowPlatformSelection prints a platform's saved app, branch and build
// by name and ID. The ID stands alone only when its name lookup fails.
func printWorkflowPlatformSelection(ctx context.Context, client *api.Client, label string, value map[string]interface{}) {
	appID := workflowSelectionText(value, "app_id")
	appName := ""
	if app, err := client.GetApp(ctx, appID); err == nil {
		appName = app.Name
	}
	ui.PrintInfo("  %-12s %s", label, nameWithID(appName, appID))
	ui.PrintInfo("    Branch: %s", workflowBranchChoice(ctx, client, appID, value))
	if version := workflowSelectionText(value, "pinned_version"); version != "" && workflowSelectionText(value, "pinned_build_id") == "" {
		ui.PrintInfo("    Saved version (before branches): %s", version)
	}
}

func workflowBranchChoice(ctx context.Context, client *api.Client, appID string, value map[string]interface{}) string {
	build := "Automatic"
	switch api.BuildSelectionMode(workflowSelectionText(value, "build_selection")) {
	case api.BuildSelectionModeLatestAllBranches:
		return "All branches · Latest build"
	case api.BuildSelectionModeLatest:
		build = "Latest build"
	case api.BuildSelectionModePinned:
		buildID := workflowSelectionText(value, "pinned_build_id")
		version := ""
		if detail, err := client.GetBuildVersionDetail(ctx, buildID, false); err == nil {
			version = detail.Version
		}
		build = "Exact build " + nameWithID(version, buildID)
	}
	branchID := workflowSelectionText(value, "branch_id")
	if branchID == "" {
		if build == "Automatic" {
			return "Default branch"
		}
		return "Default branch · " + build
	}
	branchName := ""
	if branch, err := client.ResolveBranch(ctx, appID, branchID, ""); err == nil {
		branchName = branch.Name
	}
	return nameWithID(branchName, branchID) + " · " + build
}

func nameWithID(name, id string) string {
	if name == "" {
		return id
	}
	return name + " (" + id + ")"
}

// workflowSelectionText reads a saved selector field. Selectors read back from
// the API hold strings; the ones runWorkflowAppSet just built hold *string and
// mode values.
func workflowSelectionText(value map[string]interface{}, key string) string {
	switch field := value[key].(type) {
	case string:
		return field
	case *string:
		if field != nil {
			return *field
		}
	case api.BuildSelectionMode:
		return string(field)
	}
	return ""
}

// --- Run config commands ---

// Workflow config flags
var (
	workflowConfigParallel int
	workflowConfigRetries  int
)

var workflowConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage workflow run configuration",
	Long: `Manage the run configuration for a workflow (parallelism, retries).

COMMANDS:
  show    - Show current run config
  set     - Set run config values

EXAMPLES:
  revyl workflow config show my-workflow
  revyl workflow config set my-workflow --parallel 3 --retries 2`,
}

var workflowConfigShowCmd = &cobra.Command{
	Use:   "show <name|id>",
	Short: "Show current run configuration for a workflow",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowConfigShow,
}

var workflowConfigSetCmd = &cobra.Command{
	Use:   "set <name|id>",
	Short: "Set run configuration for a workflow",
	Long: `Set the run configuration for a workflow.

At least one of --parallel or --retries must be provided.

EXAMPLES:
  revyl workflow config set my-workflow --parallel 3
  revyl workflow config set my-workflow --retries 2
  revyl workflow config set my-workflow --parallel 3 --retries 2`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkflowConfigSet,
}

func initWorkflowConfig() {
	workflowConfigCmd.AddCommand(workflowConfigShowCmd)
	workflowConfigCmd.AddCommand(workflowConfigSetCmd)

	workflowConfigSetCmd.Flags().IntVar(&workflowConfigParallel, "parallel", 0, "Number of tests to run in parallel")
	workflowConfigSetCmd.Flags().IntVar(&workflowConfigRetries, "retries", 0, "Max retries for failed tests")
}

func runWorkflowConfigShow(cmd *cobra.Command, args []string) error {
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

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")
	if jsonOutput {
		cfg := wf.RunConfig
		if cfg == nil {
			cfg = &api.WorkflowRunConfig{}
		}
		data, _ := json.MarshalIndent(cfg, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.Println()
	ui.PrintInfo("Run Config for '%s'", nameOrID)
	ui.Println()

	if wf.RunConfig == nil {
		ui.PrintInfo("  Parallelism: (default)")
		ui.PrintInfo("  Max Retries: (default)")
	} else {
		if wf.RunConfig.Parallelism > 0 {
			ui.PrintInfo("  Parallelism: %d", wf.RunConfig.Parallelism)
		} else {
			ui.PrintInfo("  Parallelism: (default)")
		}
		if wf.RunConfig.MaxRetries > 0 {
			ui.PrintInfo("  Max Retries: %d", wf.RunConfig.MaxRetries)
		} else {
			ui.PrintInfo("  Max Retries: (default)")
		}
	}

	ui.Println()
	return nil
}

func runWorkflowConfigSet(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	parallelChanged := cmd.Flags().Changed("parallel")
	retriesChanged := cmd.Flags().Changed("retries")
	if !parallelChanged && !retriesChanged {
		return fmt.Errorf("at least one of --parallel or --retries is required")
	}

	if parallelChanged && workflowConfigParallel < 1 {
		return fmt.Errorf("--parallel must be >= 1 (got %d)", workflowConfigParallel)
	}
	if retriesChanged && workflowConfigRetries < 0 {
		return fmt.Errorf("--retries must be >= 0 (got %d)", workflowConfigRetries)
	}

	workflowID, client, err := wfSettingsSetupClient(cmd, nameOrID)
	if err != nil {
		return err
	}

	// Merge with existing config to avoid clobbering unset fields
	existing, fetchErr := client.GetWorkflow(cmd.Context(), workflowID)
	cfg := &api.WorkflowRunConfig{}
	if fetchErr == nil && existing.RunConfig != nil {
		cfg = existing.RunConfig
	}

	if parallelChanged {
		cfg.Parallelism = workflowConfigParallel
	}
	if retriesChanged {
		cfg.MaxRetries = workflowConfigRetries
	}

	ui.StartSpinner("Updating run config...")
	err = client.UpdateWorkflowRunConfig(cmd.Context(), workflowID, cfg)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to update run config: %v", err)
		return err
	}

	ui.PrintSuccess("Run config updated for workflow '%s'", nameOrID)
	if parallelChanged {
		ui.PrintInfo("  Parallelism: %d", cfg.Parallelism)
	}
	if retriesChanged {
		ui.PrintInfo("  Max Retries: %d", cfg.MaxRetries)
	}
	return nil
}

func workflowPlatformSelectorChanged(cmd *cobra.Command, platform string) bool {
	for _, suffix := range []string{"branch", "build-selection", "build-id", "automatic"} {
		if cmd.Flags().Changed(strings.Join([]string{platform, suffix}, "-")) {
			return true
		}
	}
	// --<platform>-all-branches=false asks for nothing, so it keeps the saved choice.
	allBranches, _ := cmd.Flags().GetBool(platform + "-" + allBranchesFlagName)
	return allBranches
}

func runWorkflowAppEnable(cmd *cobra.Command, args []string) error {
	workflowID, client, err := wfSettingsSetupClient(cmd, args[0])
	if err != nil {
		return err
	}
	workflow, err := client.GetWorkflow(cmd.Context(), workflowID)
	if err != nil {
		return err
	}
	if !workflowHasSavedApps(workflow.BuildConfig) {
		return fmt.Errorf("no saved app selectors; configure them with workflow app set")
	}
	if err := client.SetWorkflowBuildOverrideEnabled(cmd.Context(), workflowID, true); err != nil {
		return err
	}
	if jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json"); jsonOutput {
		return writeWorkflowAppSelection(cmd, workflowID, true, workflow.BuildConfig)
	}
	ui.PrintSuccess("Enabled saved app selectors for workflow %s", args[0])
	return nil
}

func workflowHasSavedApps(buildConfig map[string]interface{}) bool {
	for _, slot := range []string{"ios_build", "android_build"} {
		if saved, _ := buildConfig[slot].(map[string]interface{}); saved != nil {
			return true
		}
	}
	return false
}

func writeWorkflowAppSelection(cmd *cobra.Command, workflowID string, enabled bool, buildConfig map[string]interface{}) error {
	return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
		WorkflowID          string                 `json:"workflow_id"`
		OverrideBuildConfig bool                   `json:"override_build_config"`
		BuildConfig         map[string]interface{} `json:"build_config,omitempty"`
	}{WorkflowID: workflowID, OverrideBuildConfig: enabled, BuildConfig: buildConfig})
}
