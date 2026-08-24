// Package main provides app management commands for the Revyl CLI.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
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
	appListSearch   string
	appListJSON     bool

	// app delete flags
	appDeleteForce bool
)

func init() {
	appCmd.AddCommand(appCreateCmd)
	appCmd.AddCommand(appListCmd)
	appCmd.AddCommand(appDeleteCmd)
	appCmd.AddCommand(appStoreKitCmd)

	appCreateCmd.Flags().StringVar(&appCreateName, "name", "", "Name for the app (required)")
	appCreateCmd.Flags().StringVar(&appCreatePlatform, "platform", "", "Target platform: ios or android (required)")
	appCreateCmd.Flags().BoolVar(&appCreateJSON, "json", false, "Output result as JSON")
	_ = appCreateCmd.MarkFlagRequired("name")
	_ = appCreateCmd.MarkFlagRequired("platform")

	appListCmd.Flags().StringVar(&appListPlatform, "platform", "", "Filter by platform (android, ios)")
	appListCmd.Flags().StringVar(&appListSearch, "search", "", "Search by app name")
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

	// Human app creation preserves the established optional config-binding
	// prompt. Resolve and strictly validate that config before creating the
	// remote app so a legacy or malformed file cannot fail only after the
	// external side effect. JSON creation remains deliberately configless.
	var projectContext *config.ProjectContext
	if !jsonOutput {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return fmt.Errorf("failed to get current directory: %w", cwdErr)
		}
		projectContext, err = resolveOptionalAppProject(cwd)
		if err != nil {
			return fmt.Errorf("cannot inspect project config before creating app: %w", err)
		}
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

	if err := offerSaveAppBinding(projectContext, platform, result.ID); err != nil {
		ui.PrintWarning("Failed to save config: %v", err)
	}

	ui.Println()
	ui.PrintInfo("Next:")
	ui.PrintDim("  revyl build --platform %s        Build and upload to this app", platform)

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
	var apps []api.App
	var total int
	if strings.TrimSpace(appListSearch) != "" {
		result, listErr := client.SearchApps(cmd.Context(), appListSearch, appListPlatform, 100)
		if listErr == nil {
			apps = result.Items
			total = result.Total
		}
		err = listErr
	} else {
		apps, err = client.ListAllApps(cmd.Context(), appListPlatform, 100)
		total = len(apps)
	}
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to list apps: %v", err)
		return err
	}

	if jsonOutput {
		output := map[string]interface{}{
			"apps":  apps,
			"count": len(apps),
			"total": total,
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(apps) == 0 {
		ui.PrintInfo("No apps found in your organization")
		ui.Println()
		ui.PrintInfo("Create one with:")
		ui.PrintDim("  revyl app create --name \"My App\" --platform <ios|android>")
		return nil
	}

	ui.Println()
	ui.PrintInfo("Apps (%d shown, %d total):", len(apps), total)
	ui.Println()

	// Create table with dynamic column widths
	table := ui.NewTable("NAME", "PLATFORM", "BUILDS", "LATEST", "APP ID")
	table.SetMinWidth(0, 20) // NAME
	table.SetMinWidth(1, 8)  // PLATFORM
	table.SetMinWidth(4, 36) // APP ID - UUIDs are 36 chars

	for _, app := range apps {
		latestVer := "-"
		if app.LatestVersion != "" {
			latestVer = app.LatestVersion
		}
		table.AddRow(app.Name, app.Platform, fmt.Sprintf("%d", app.VersionsCount), latestVer, app.ID)
	}

	table.Render()

	ui.Println()
	ui.PrintDim("  revyl build list --app <APP ID>              List builds for an app")
	ui.PrintDim("  revyl build --platform <key>                 Build and upload a new build")

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

	// Resolve and strictly validate the nearest canonical config before deleting
	// the remote app. A project without config remains a supported standalone
	// workflow, but an invalid config must not be discovered only after deletion.
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}
	projectContext, err := resolveOptionalAppProject(cwd)
	if err != nil {
		return fmt.Errorf("cannot inspect project config before deleting app: %w", err)
	}
	configRefs, configReplacement, err := prepareAppBindingRemoval(projectContext, appID)
	if err != nil {
		return fmt.Errorf("cannot prepare project config update before deleting app: %w", err)
	}

	// Show what will be deleted
	if !appDeleteForce {
		ui.Println()
		ui.PrintInfo("Delete app \"%s\" (%s)?", appName, appID)
		ui.PrintDim("  - Remote: will delete app and ALL build versions")
		if len(configRefs) > 0 {
			ui.PrintDim("  - Config: will remove app_id from profile recipes: %v", configRefs)
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

	// Remove canonical profile bindings only after the remote deletion succeeds.
	if len(configRefs) > 0 {
		if err := config.ReplaceConfigAtomically(projectContext.ConfigPath, configReplacement, projectContext.OriginalBytes); err != nil {
			if !jsonOutput {
				ui.PrintWarning("Failed to update config: %v", err)
			}
		} else if !jsonOutput {
			ui.PrintSuccess("Removed app_id from config profiles")
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

type projectAppRecipeRef struct {
	Profile  string
	Platform string
}

func (r projectAppRecipeRef) String() string {
	return r.Profile + "/" + r.Platform
}

func resolveOptionalAppProject(cwd string) (*config.ProjectContext, error) {
	projectContext, err := config.ResolveProjectContext(cwd, "")
	if err == nil {
		return projectContext, nil
	}
	var configErr *config.ConfigError
	if errors.As(err, &configErr) && (configErr.Code == "git_worktree_unavailable" || configErr.Code == "config_not_found") {
		return nil, nil
	}
	return nil, actionableLocalConfigError(err)
}

func projectAppRecipeRefs(authored *config.AuthoredConfig, platform, appID string) []projectAppRecipeRef {
	if authored == nil || authored.Build == nil {
		return nil
	}
	refs := make([]projectAppRecipeRef, 0)
	for profileName, profile := range authored.Build.Profiles {
		var recipe *config.AuthoredBuildRecipe
		switch platform {
		case "ios":
			recipe = profile.IOS
		case "android":
			recipe = profile.Android
		}
		if recipe == nil {
			continue
		}
		if appID != "" && (recipe.AppID == nil || *recipe.AppID != appID) {
			continue
		}
		refs = append(refs, projectAppRecipeRef{Profile: profileName, Platform: platform})
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Profile == refs[j].Profile {
			return refs[i].Platform < refs[j].Platform
		}
		return refs[i].Profile < refs[j].Profile
	})
	return refs
}

func offerSaveAppBinding(projectContext *config.ProjectContext, platform, appID string) error {
	if projectContext == nil {
		return nil
	}
	refs := projectAppRecipeRefs(projectContext.Authored, platform, "")
	if len(refs) == 0 {
		return nil
	}
	selected := refs[0]
	if len(refs) > 1 {
		options := make([]string, len(refs))
		for i, ref := range refs {
			options[i] = ref.String()
		}
		selection, err := ui.PromptSelect("Save to which build profile?", options)
		if err != nil {
			return nil
		}
		if selection < 0 || selection >= len(refs) {
			return fmt.Errorf("invalid build profile selection")
		}
		selected = refs[selection]
	}
	save, err := ui.PromptConfirm(fmt.Sprintf("Save to .revyl/config.yaml for '%s'?", selected), true)
	if err != nil || !save {
		return nil
	}
	if err := replaceAppBinding(projectContext, selected, appID); err != nil {
		return err
	}
	ui.PrintSuccess("Saved app_id to build.profiles.%s.%s", selected.Profile, selected.Platform)
	return nil
}

func replaceAppBinding(projectContext *config.ProjectContext, ref projectAppRecipeRef, appID string) error {
	authored, err := config.ParseAuthoredConfig(projectContext.OriginalBytes)
	if err != nil {
		return err
	}
	if authored.Build == nil {
		return fmt.Errorf("selected build profile is unavailable")
	}
	profile, ok := authored.Build.Profiles[ref.Profile]
	if !ok {
		return fmt.Errorf("selected build profile is unavailable")
	}
	var recipe *config.AuthoredBuildRecipe
	switch ref.Platform {
	case "ios":
		recipe = profile.IOS
	case "android":
		recipe = profile.Android
	default:
		return fmt.Errorf("unsupported app platform %q", ref.Platform)
	}
	if recipe == nil {
		return fmt.Errorf("selected %s build recipe is unavailable", ref.Platform)
	}
	recipe.AppID = &appID
	authored.Build.Profiles[ref.Profile] = profile
	replacement, err := config.MarshalCanonicalConfig(*authored)
	if err != nil {
		return err
	}
	return config.ReplaceConfigAtomically(projectContext.ConfigPath, replacement, projectContext.OriginalBytes)
}

func prepareAppBindingRemoval(projectContext *config.ProjectContext, appID string) ([]projectAppRecipeRef, []byte, error) {
	if projectContext == nil || projectContext.Authored == nil || projectContext.Authored.Build == nil {
		return nil, nil, nil
	}
	authored, err := config.ParseAuthoredConfig(projectContext.OriginalBytes)
	if err != nil {
		return nil, nil, err
	}
	refs := append(projectAppRecipeRefs(authored, "ios", appID), projectAppRecipeRefs(authored, "android", appID)...)
	sort.Slice(refs, func(i, j int) bool { return refs[i].String() < refs[j].String() })
	if len(refs) == 0 {
		return nil, nil, nil
	}
	for _, ref := range refs {
		profile := authored.Build.Profiles[ref.Profile]
		if ref.Platform == "ios" {
			profile.IOS.AppID = nil
		} else {
			profile.Android.AppID = nil
		}
		authored.Build.Profiles[ref.Profile] = profile
	}
	replacement, err := config.MarshalCanonicalConfig(*authored)
	if err != nil {
		return nil, nil, err
	}
	return refs, replacement, nil
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

	// Search by exact name without scanning every app in the org.
	result, err := client.SearchApps(cmd.Context(), nameOrID, "", 20)
	if err != nil {
		return "", "", fmt.Errorf("failed to search apps: %w", err)
	}

	match, err := selectExactNameApp(result.Items, nameOrID)
	if err != nil {
		return "", "", err
	}
	return match.ID, match.Name, nil
}

// selectExactNameApp picks one app by exact name or returns a deterministic
// disambiguation error if multiple apps share the same name.
func selectExactNameApp(apps []api.App, name string) (api.App, error) {
	matches := make([]api.App, 0, 2)
	for _, app := range apps {
		if app.Name == name {
			matches = append(matches, app)
		}
	}
	if len(matches) == 0 {
		return api.App{}, fmt.Errorf("app %q not found", name)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Platform == matches[j].Platform {
			return matches[i].ID < matches[j].ID
		}
		return matches[i].Platform < matches[j].Platform
	})

	lines := make([]string, 0, len(matches))
	for _, app := range matches {
		lines = append(lines, fmt.Sprintf("  - %s (%s)", app.ID, app.Platform))
	}
	return api.App{}, fmt.Errorf("multiple apps named %q found. Use an app ID:\n%s", name, strings.Join(lines, "\n"))
}
