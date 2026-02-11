// Package main provides build commands for the Revyl CLI.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// buildCmd is the parent command for build operations.
var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Manage app builds",
	Long: `Manage app builds for testing.

Commands:
  upload  - Build and upload the app
  list    - List uploaded build versions
  delete  - Delete an app or specific build version`,
}

// buildUploadCmd builds and uploads the app.
var buildUploadCmd = &cobra.Command{
	Use:   "upload",
	Short: "Build and upload the app",
	Long: `Build the app and upload it to Revyl.

By default, builds both iOS and Android concurrently if both platforms are configured.
Use --platform to build only one platform.

This command will:
  1. Run the build command(s) from .revyl/config.yaml
  2. Upload the resulting artifact(s) to Revyl
  3. Track metadata (git commit, branch, machine, etc.)

Examples:
  revyl build upload                    # Build both iOS and Android concurrently
  revyl build upload --platform ios     # Build iOS only
  revyl build upload --platform android # Build Android only
  revyl build upload --skip-build       # Upload existing artifacts
  revyl build upload --app <id>         # Upload to specific app
  revyl build upload --name "My App"    # Create app with specified name
  revyl build upload --name "My App" -y # Create and auto-save to config`,
	RunE: runBuildUpload,
}

// buildListCmd lists uploaded build versions.
var buildListCmd = &cobra.Command{
	Use:   "list",
	Short: "List uploaded build versions",
	Long: `List all uploaded build versions.

If an app is configured locally, lists builds for that app.
Otherwise, shows all apps in your organization.

Examples:
  revyl build list                           # List builds (or show org apps)
  revyl build list --app <id>               # List builds for specific app
  revyl build list --platform android        # Filter org apps by platform`,
	RunE: runBuildList,
}

// buildDeleteCmd deletes an app or build version.
var buildDeleteCmd = &cobra.Command{
	Use:   "delete <name|id>",
	Short: "Delete an app or build version",
	Long: `Delete an app (and all build versions) or a specific build version.

Use --version to delete only a specific build version.

Examples:
  revyl build delete "My App iOS"                 # Delete entire app
  revyl build delete "My App iOS" --version v1.2.3 # Delete specific build version only
  revyl build delete "My App iOS" --force          # Skip confirmation`,
	Args: cobra.ExactArgs(1),
	RunE: runDeleteBuild,
}

var (
	buildSkip          bool
	buildVersion       string
	buildSetCurr       bool
	appIDFlag          string
	buildPlatform      string
	uploadAppFlag      string
	uploadPlatformFlag string
	uploadNameFlag     string
	uploadYesFlag      bool
	buildListJSON      bool
	buildUploadJSON    bool
	buildDryRun        bool
)

func init() {
	buildCmd.AddCommand(buildUploadCmd)
	buildCmd.AddCommand(buildListCmd)
	buildCmd.AddCommand(buildDeleteCmd)

	buildDeleteCmd.Flags().BoolVarP(&deleteForce, "force", "f", false, "Skip confirmation prompt")
	buildDeleteCmd.Flags().StringVar(&deleteBuildVersion, "version", "", "Delete specific build version only")

	buildUploadCmd.Flags().BoolVar(&buildSkip, "skip-build", false, "Skip build step, upload existing artifact")
	buildUploadCmd.Flags().StringVar(&buildVersion, "version", "", "Version string for the upload (default: auto-generated)")
	buildUploadCmd.Flags().BoolVar(&buildSetCurr, "set-current", false, "Set this version as the current version")
	buildUploadCmd.Flags().StringVar(&uploadAppFlag, "app", "", "App ID to upload to")
	buildUploadCmd.Flags().StringVar(&uploadPlatformFlag, "platform", "", "Platform to build for (ios, android)")
	buildUploadCmd.Flags().StringVar(&uploadNameFlag, "name", "", "Name for new app (used when creating)")
	buildUploadCmd.Flags().BoolVarP(&uploadYesFlag, "yes", "y", false, "Automatically confirm prompts (e.g., save to config)")
	buildUploadCmd.Flags().BoolVar(&buildUploadJSON, "json", false, "Output results as JSON")
	buildUploadCmd.Flags().BoolVar(&buildDryRun, "dry-run", false, "Show what would be uploaded without uploading")

	buildListCmd.Flags().StringVar(&appIDFlag, "app", "", "App ID to list builds for")
	buildListCmd.Flags().StringVar(&buildPlatform, "platform", "", "Filter by platform (android, ios) when listing org apps")
	buildListCmd.Flags().BoolVar(&buildListJSON, "json", false, "Output results as JSON")
}

// runBuildUpload executes the build upload command.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments
//
// Returns:
//   - error: Any error that occurred during the build/upload process
func runBuildUpload(cmd *cobra.Command, args []string) error {
	// Check authentication
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Load project config
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	cfg, err := config.LoadProjectConfig(configPath)
	if err != nil {
		ui.PrintError("Project not initialized. Run 'revyl init' first.")
		return err
	}

	// If --platform is specified, run single platform build
	if uploadPlatformFlag != "" {
		return runSinglePlatformBuild(cmd, cfg, configPath, apiKey, uploadPlatformFlag)
	}

	// Check if both ios and android platforms exist for concurrent builds
	_, hasIOS := cfg.Build.Platforms["ios"]
	_, hasAndroid := cfg.Build.Platforms["android"]

	if hasIOS && hasAndroid {
		// Default: run concurrent builds for both platforms
		return runConcurrentBuilds(cmd, cfg, configPath, apiKey)
	}

	// Handle single platform case deterministically
	platformCount := len(cfg.Build.Platforms)
	if platformCount == 0 {
		ui.PrintError("No build platforms configured")
		ui.PrintInfo("Please configure build.platforms in .revyl/config.yaml")
		return fmt.Errorf("no build platforms configured")
	}

	if platformCount == 1 {
		// Single platform - use it directly
		for platform := range cfg.Build.Platforms {
			return runSinglePlatformBuild(cmd, cfg, configPath, apiKey, platform)
		}
	}

	// Multiple platforms but not ios+android - prefer ios, then android, then first alphabetically
	if hasIOS {
		return runSinglePlatformBuild(cmd, cfg, configPath, apiKey, "ios")
	}
	if hasAndroid {
		return runSinglePlatformBuild(cmd, cfg, configPath, apiKey, "android")
	}

	// Multiple custom platforms - pick first alphabetically for determinism
	platforms := make([]string, 0, platformCount)
	for platform := range cfg.Build.Platforms {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)

	ui.PrintWarning("Multiple platforms configured without --platform flag, using '%s'", platforms[0])
	ui.PrintInfo("Use --platform to specify which platform to build")
	return runSinglePlatformBuild(cmd, cfg, configPath, apiKey, platforms[0])
}

// selectOrCreateAppForPlatform prompts the user to select an existing app or create a new one,
// and saves it to the specified platform in the config.
//
// Parameters:
//   - cmd: The cobra command
//   - client: The API client
//   - cfg: The project config
//   - configPath: Path to the config file
//   - platformName: The platform name to save the app ID to (empty for no save)
//   - platform: The target platform
//
// Returns:
//   - string: The selected or created app ID
//   - error: Any error that occurred
func selectOrCreateAppForPlatform(cmd *cobra.Command, client *api.Client, cfg *config.ProjectConfig, configPath, platformName, platform string) (string, error) {
	ui.Println()
	ui.PrintWarning("No app configured for this project.")
	ui.Println()
	ui.PrintDim("An app stores your builds in Revyl so tests can run against them.")
	ui.Println()

	// Fetch existing apps
	ui.StartSpinner("Fetching apps...")
	result, err := client.ListApps(cmd.Context(), platform, 1, 50)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to fetch apps: %v", err)
		return "", err
	}

	var appID string

	// If no existing apps, skip selection and create directly
	if len(result.Items) == 0 {
		ui.PrintInfo("No existing apps found. Let's create one.")
		ui.Println()
		appID, err = createNewApp(cmd, client, cfg, platform)
		if err != nil {
			return "", err
		}
	} else {
		// Build options list
		var options []string
		for _, app := range result.Items {
			options = append(options, fmt.Sprintf("%s (%s)", app.Name, app.Platform))
		}
		options = append(options, "Create new app")

		// Show selection prompt
		ui.PrintInfo("Select an app to upload to:")
		selection, err := ui.PromptSelect("", options)
		if err != nil {
			return "", err
		}

		// If user selected "Create new"
		if selection == len(result.Items) {
			appID, err = createNewApp(cmd, client, cfg, platform)
			if err != nil {
				return "", err
			}
		} else {
			appID = result.Items[selection].ID
			ui.PrintSuccess("Selected: %s", result.Items[selection].Name)
		}
	}

	// Ask if user wants to save this to config (auto-confirm with --yes flag)
	save := uploadYesFlag
	if !save {
		var err error
		save, err = ui.PromptConfirm("Save this app to .revyl/config.yaml for future uploads?", true)
		if err != nil {
			return appID, nil // Continue even if prompt fails
		}
	}

	if save && platformName != "" {
		// Save to the platform
		platformCfg := cfg.Build.Platforms[platformName]
		platformCfg.AppID = appID
		cfg.Build.Platforms[platformName] = platformCfg
		if err := config.WriteProjectConfig(configPath, cfg); err != nil {
			ui.PrintWarning("Failed to save config: %v", err)
		} else {
			ui.PrintSuccess("Saved to .revyl/config.yaml")
		}
	}

	return appID, nil
}

// createNewApp prompts the user to create a new app.
//
// Parameters:
//   - cmd: The cobra command
//   - client: The API client
//   - cfg: The project config
//   - platform: The suggested platform
//
// Returns:
//   - string: The created app ID
//   - error: Any error that occurred
func createNewApp(cmd *cobra.Command, client *api.Client, cfg *config.ProjectConfig, platform string) (string, error) {
	ui.Println()
	ui.PrintInfo("Creating new app...")
	ui.Println()

	// Use --name flag if provided, otherwise prompt
	name := uploadNameFlag
	if name == "" {
		defaultName := fmt.Sprintf("%s %s", cfg.Project.Name, platform)
		var err error
		name, err = ui.Prompt(fmt.Sprintf("Name [%s]:", defaultName))
		if err != nil {
			return "", err
		}
		if name == "" {
			name = defaultName
		}
	} else {
		ui.PrintInfo("Name: %s", name)
	}

	// Prompt for platform if not determined
	if platform == "" {
		platformOptions := []string{"ios", "android"}
		idx, err := ui.PromptSelect("Platform:", platformOptions)
		if err != nil {
			return "", err
		}
		platform = platformOptions[idx]
	} else {
		ui.PrintInfo("Platform: %s", platform)
	}

	// Create the app
	ui.Println()
	ui.StartSpinner("Creating app...")

	result, err := client.CreateApp(cmd.Context(), &api.CreateAppRequest{
		Name:     name,
		Platform: platform,
	})

	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to create app: %v", err)
		return "", err
	}

	ui.PrintSuccess("Created: %s (%s)", result.Name, result.ID)

	return result.ID, nil
}

// BuildResult holds the result of a single platform build.
type BuildResult struct {
	// Platform is the platform that was built (ios or android).
	Platform string

	// ArtifactPath is the path to the built artifact.
	ArtifactPath string

	// Duration is how long the build took.
	Duration time.Duration

	// AppID is the app ID used for upload.
	AppID string

	// UploadResult contains the upload response.
	UploadResult *api.UploadBuildResponse

	// Error is any error that occurred during build or upload.
	Error error
}

// runConcurrentBuilds builds and uploads both iOS and Android platforms concurrently.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - cfg: The project configuration
//   - configPath: Path to the config file
//   - apiKey: Authentication token for API requests
//
// Returns:
//   - error: Any error that occurred (aggregated from both platforms)
func runConcurrentBuilds(cmd *cobra.Command, cfg *config.ProjectConfig, configPath string, apiKey string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Validate both platforms exist in config
	platforms := []string{"ios", "android"}
	for _, platform := range platforms {
		if _, ok := cfg.Build.Platforms[platform]; !ok {
			ui.PrintError("Platform '%s' not found in config", platform)
			ui.PrintInfo("Available platforms: %v", getPlatformNames(cfg.Build.Platforms))
			return fmt.Errorf("missing platform: %s", platform)
		}
	}

	// Handle dry-run mode early
	if buildDryRun {
		ui.PrintBanner(version)
		ui.PrintInfo("Dry-run mode - showing what would be uploaded:")
		ui.Println()

		for _, platform := range platforms {
			platformCfg := cfg.Build.Platforms[platform]
			versionStr := buildVersion
			if versionStr == "" {
				versionStr = build.GenerateVersionString()
			}
			versionStr = fmt.Sprintf("%s-%s", versionStr, platform)

			ui.PrintInfo("[%s]", platform)
			ui.PrintInfo("  Command:        %s", platformCfg.Command)
			ui.PrintInfo("  Output:         %s", platformCfg.Output)
			ui.PrintInfo("  Build Version:  %s", versionStr)
			if platformCfg.AppID != "" {
				ui.PrintInfo("  App ID:         %s", platformCfg.AppID)
			} else {
				ui.PrintInfo("  App ID:         (not configured)")
			}
			ui.PrintInfo("  Set Current:    %v", buildSetCurr)
			ui.Println()
		}

		ui.PrintSuccess("Dry-run complete - no changes made")
		return nil
	}

	// Create API client
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Check and prompt for missing app IDs before starting builds
	for _, platform := range platforms {
		// Check platform-level app_id
		platformCfg := cfg.Build.Platforms[platform]
		appID := platformCfg.AppID

		if appID == "" {
			ui.Println()
			ui.PrintWarning("No app configured for %s", platform)
			selectedID, err := selectOrCreateAppInteractive(cmd, client, cfg, platform)
			if err != nil {
				return err
			}
			// Store in platform config
			platformCfg.AppID = selectedID
			cfg.Build.Platforms[platform] = platformCfg
		}
	}

	// Save updated config with app IDs
	if err := config.WriteProjectConfig(configPath, cfg); err != nil {
		ui.PrintWarning("Failed to save config: %v", err)
	} else {
		ui.PrintSuccess("Saved app IDs to .revyl/config.yaml")
	}

	ui.PrintBanner(version)
	ui.PrintInfo("Building iOS and Android concurrently...")
	ui.Println()

	// Channel to collect results
	results := make(chan BuildResult, len(platforms))
	var wg sync.WaitGroup

	// Mutex for synchronized output
	var outputMu sync.Mutex

	// Start concurrent builds
	for _, platform := range platforms {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			result := buildAndUploadPlatform(cmd, cfg, cwd, client, p, &outputMu)
			results <- result
		}(platform)
	}

	// Wait for all builds to complete
	wg.Wait()
	close(results)

	// Collect and report results
	ui.Println()
	ui.PrintInfo("Build Results:")
	ui.Println()

	var errors []error
	for result := range results {
		if result.Error != nil {
			ui.PrintError("[%s] Failed: %v", result.Platform, result.Error)

			// Check if this is an EAS error with guidance
			if easErr, ok := result.Error.(*build.EASBuildError); ok {
				ui.Println()
				ui.PrintWarning("How to fix:")
				ui.Println()
				// Print each line of guidance
				for _, line := range strings.Split(easErr.Guidance, "\n") {
					ui.PrintDim("  %s", line)
				}
				ui.Println()
			}

			errors = append(errors, fmt.Errorf("%s: %w", result.Platform, result.Error))
		} else {
			ui.PrintSuccess("[%s] Upload complete!", result.Platform)
			ui.PrintInfo("  App:             %s", result.AppID)
			ui.PrintInfo("  Build Version:   %s", result.UploadResult.Version)
			ui.PrintInfo("  Build ID:        %s", result.UploadResult.VersionID)
			if result.UploadResult.PackageID != "" {
				ui.PrintInfo("  Package ID:      %s", result.UploadResult.PackageID)
			}
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("%d platform(s) failed", len(errors))
	}

	// Suggest running a test after successful concurrent builds
	if !buildUploadJSON {
		if len(cfg.Tests) > 0 {
			for alias := range cfg.Tests {
				ui.PrintNextSteps([]ui.NextStep{
					{Label: "Run a test:", Command: fmt.Sprintf("revyl run %s", alias)},
				})
				break
			}
		} else {
			ui.PrintNextSteps([]ui.NextStep{
				{Label: "Create a test:", Command: "revyl test create <name>"},
			})
		}
	}

	return nil
}

// buildAndUploadPlatform builds and uploads a single platform.
//
// Parameters:
//   - cmd: The cobra command
//   - cfg: The project configuration
//   - cwd: Current working directory
//   - client: The API client
//   - platform: The platform to build (ios or android)
//   - outputMu: Mutex for synchronized output
//
// Returns:
//   - BuildResult: The result of the build and upload
func buildAndUploadPlatform(cmd *cobra.Command, cfg *config.ProjectConfig, cwd string, client *api.Client, platform string, outputMu *sync.Mutex) BuildResult {
	result := BuildResult{Platform: platform}

	platformCfg := cfg.Build.Platforms[platform]

	// Build
	if !buildSkip {
		outputMu.Lock()
		ui.PrintInfo("[%s] Building with: %s", platform, platformCfg.Command)
		outputMu.Unlock()

		startTime := time.Now()
		runner := build.NewRunner(cwd)

		err := runner.Run(platformCfg.Command, func(line string) {
			outputMu.Lock()
			ui.PrintDim("  [%s] %s", platform, line)
			outputMu.Unlock()
		})

		result.Duration = time.Since(startTime)

		if err != nil {
			// Preserve EAS errors for guidance display
			if _, ok := err.(*build.EASBuildError); ok {
				result.Error = err
			} else {
				result.Error = fmt.Errorf("build failed: %w", err)
			}
			return result
		}

		outputMu.Lock()
		ui.PrintSuccess("[%s] Build completed in %s", platform, result.Duration.Round(time.Second))
		outputMu.Unlock()
	}

	// Resolve artifact path
	artifactPath, err := build.ResolveArtifactPath(cwd, platformCfg.Output)
	if err != nil {
		result.Error = fmt.Errorf("artifact not found: %w", err)
		return result
	}
	result.ArtifactPath = artifactPath

	// Get app ID from platform config
	appID := platformCfg.AppID
	result.AppID = appID

	// Generate version string with platform suffix
	versionStr := buildVersion
	if versionStr == "" {
		versionStr = build.GenerateVersionString()
	}
	versionStr = fmt.Sprintf("%s-%s", versionStr, platform)

	outputMu.Lock()
	ui.PrintInfo("[%s] Uploading: %s", platform, filepath.Base(artifactPath))
	outputMu.Unlock()

	// Convert tar.gz to zip for iOS builds (EAS produces tar.gz)
	if build.IsTarGz(artifactPath) {
		outputMu.Lock()
		ui.PrintInfo("[%s] Extracting .app from tar.gz...", platform)
		outputMu.Unlock()
		zipPath, err := build.ExtractAppFromTarGz(artifactPath)
		if err != nil {
			result.Error = fmt.Errorf("failed to extract .app from tar.gz: %w", err)
			return result
		}
		defer os.Remove(zipPath) // Clean up temp zip after upload
		artifactPath = zipPath
		result.ArtifactPath = artifactPath
		outputMu.Lock()
		ui.PrintSuccess("[%s] Converted to: %s", platform, filepath.Base(zipPath))
		outputMu.Unlock()
	} else if build.IsAppBundle(artifactPath) {
		// Zip .app directory for iOS builds (Flutter, React Native, Xcode)
		outputMu.Lock()
		ui.PrintInfo("[%s] Zipping .app bundle...", platform)
		outputMu.Unlock()
		zipPath, err := build.ZipAppBundle(artifactPath)
		if err != nil {
			result.Error = fmt.Errorf("failed to zip .app bundle: %w", err)
			return result
		}
		defer os.Remove(zipPath) // Clean up temp zip after upload
		artifactPath = zipPath
		result.ArtifactPath = artifactPath
		outputMu.Lock()
		ui.PrintSuccess("[%s] Created: %s", platform, filepath.Base(zipPath))
		outputMu.Unlock()
	}

	// Collect metadata
	metadata := build.CollectMetadata(cwd, platformCfg.Command, platform, result.Duration)

	// Upload
	uploadResult, err := client.UploadBuild(cmd.Context(), &api.UploadBuildRequest{
		AppID:        appID,
		Version:      versionStr,
		FilePath:     artifactPath,
		Metadata:     metadata,
		SetAsCurrent: buildSetCurr,
	})

	if err != nil {
		result.Error = fmt.Errorf("upload failed: %w", err)
		return result
	}

	result.UploadResult = uploadResult
	return result
}

// selectOrCreateAppInteractive prompts the user to select or create an app for a specific platform.
//
// Parameters:
//   - cmd: The cobra command
//   - client: The API client
//   - cfg: The project config
//   - platform: The target platform
//
// Returns:
//   - string: The selected or created app ID
//   - error: Any error that occurred
func selectOrCreateAppInteractive(cmd *cobra.Command, client *api.Client, cfg *config.ProjectConfig, platform string) (string, error) {
	// Fetch existing apps for this platform
	ui.StartSpinner(fmt.Sprintf("Fetching %s apps...", platform))
	result, err := client.ListApps(cmd.Context(), platform, 1, 50)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to fetch apps: %v", err)
		return "", err
	}

	var appID string

	// If no existing apps for this platform, create directly
	if len(result.Items) == 0 {
		ui.PrintInfo("No existing %s apps found. Creating one...", platform)
		appID, err = createNewAppForPlatform(cmd, client, cfg, platform)
		if err != nil {
			return "", err
		}
	} else {
		// Build options list
		var options []string
		for _, app := range result.Items {
			options = append(options, fmt.Sprintf("%s (%s)", app.Name, app.Platform))
		}
		options = append(options, fmt.Sprintf("Create new %s app", platform))

		// Show selection prompt
		ui.PrintInfo("Select an app for %s:", platform)
		selection, err := ui.PromptSelect("", options)
		if err != nil {
			return "", err
		}

		// If user selected "Create new"
		if selection == len(result.Items) {
			appID, err = createNewAppForPlatform(cmd, client, cfg, platform)
			if err != nil {
				return "", err
			}
		} else {
			appID = result.Items[selection].ID
			ui.PrintSuccess("Selected: %s", result.Items[selection].Name)
		}
	}

	return appID, nil
}

// createNewAppForPlatform creates a new app for a specific platform.
//
// Parameters:
//   - cmd: The cobra command
//   - client: The API client
//   - cfg: The project config
//   - platform: The target platform
//
// Returns:
//   - string: The created app ID
//   - error: Any error that occurred
func createNewAppForPlatform(cmd *cobra.Command, client *api.Client, cfg *config.ProjectConfig, platform string) (string, error) {
	// Use --name flag if provided, otherwise prompt
	name := uploadNameFlag
	if name == "" {
		defaultName := fmt.Sprintf("%s %s", cfg.Project.Name, platform)
		var err error
		name, err = ui.Prompt(fmt.Sprintf("Name [%s]:", defaultName))
		if err != nil {
			return "", err
		}
		if name == "" {
			name = defaultName
		}
	} else {
		ui.PrintInfo("Name: %s", name)
	}

	// Create the app
	ui.StartSpinner(fmt.Sprintf("Creating %s app...", platform))

	result, err := client.CreateApp(cmd.Context(), &api.CreateAppRequest{
		Name:     name,
		Platform: platform,
	})

	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to create app: %v", err)
		return "", err
	}

	ui.PrintSuccess("Created: %s (%s)", result.Name, result.ID)

	return result.ID, nil
}

// runSinglePlatformBuild builds and uploads a single platform.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - cfg: The project configuration
//   - configPath: Path to the config file
//   - apiKey: Authentication token for API requests
//   - platform: The platform to build
//
// Returns:
//   - error: Any error that occurred during the build/upload process
func runSinglePlatformBuild(cmd *cobra.Command, cfg *config.ProjectConfig, configPath string, apiKey string, platform string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Get platform config
	platformCfg, ok := cfg.Build.Platforms[platform]
	if !ok {
		ui.PrintError("Unknown platform: %s", platform)
		ui.PrintInfo("Available platforms: %v", getPlatformNames(cfg.Build.Platforms))
		return fmt.Errorf("unknown platform: %s", platform)
	}

	// Validate platform configuration
	if platformCfg.Output == "" {
		ui.PrintError("Build output path not configured for %s", platform)
		ui.PrintInfo("Please configure build.platforms.%s.output in .revyl/config.yaml", platform)
		return fmt.Errorf("incomplete build config: missing output for %s", platform)
	}
	if platformCfg.Command == "" && !buildSkip {
		ui.PrintError("Build command not configured for %s", platform)
		ui.PrintInfo("Please configure build.platforms.%s.command in .revyl/config.yaml, or use --skip-build to upload an existing artifact", platform)
		return fmt.Errorf("incomplete build config: missing command for %s", platform)
	}

	ui.PrintBanner(version)
	ui.PrintInfo("Build and Upload (%s)", platform)
	ui.Println()

	// Handle dry-run mode before starting the build
	if buildDryRun {
		ui.PrintInfo("Dry-run mode - showing what would be built and uploaded:")
		ui.Println()
		ui.PrintInfo("  Platform:       %s", platform)
		ui.PrintInfo("  Build Command:  %s", platformCfg.Command)
		ui.PrintInfo("  Output:         %s", platformCfg.Output)
		if platformCfg.AppID != "" {
			ui.PrintInfo("  App ID:         %s", platformCfg.AppID)
		}
		if buildVersion != "" {
			ui.PrintInfo("  Build Version:  %s", buildVersion)
		}
		ui.Println()
		ui.PrintSuccess("Dry-run complete - no changes made")
		return nil
	}

	var buildDuration time.Duration

	// Run build if not skipped
	if !buildSkip {
		ui.PrintInfo("Building with: %s", platformCfg.Command)
		ui.Println()

		startTime := time.Now()
		runner := build.NewRunner(cwd)

		err = runner.Run(platformCfg.Command, func(line string) {
			ui.PrintDim("  %s", line)
		})

		buildDuration = time.Since(startTime)

		if err != nil {
			ui.Println()
			ui.PrintError("Build failed: %v", err)

			// Check if this is an EAS error with guidance
			if easErr, ok := err.(*build.EASBuildError); ok {
				ui.Println()
				ui.PrintWarning("How to fix:")
				ui.Println()
				// Print each line of guidance
				for _, line := range strings.Split(easErr.Guidance, "\n") {
					ui.PrintDim("  %s", line)
				}
			}

			return err
		}

		ui.Println()
		ui.PrintSuccess("Build completed in %s", buildDuration.Round(time.Second))
	} else {
		ui.PrintInfo("Skipping build step")
	}

	// Check artifact exists
	artifactPath, err := build.ResolveArtifactPath(cwd, platformCfg.Output)
	if err != nil {
		ui.PrintError("Build artifact not found: %s", platformCfg.Output)
		return fmt.Errorf("artifact not found: %w", err)
	}

	// Create API client
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Determine app ID from platform config
	appID := uploadAppFlag
	if appID == "" {
		appID = platformCfg.AppID
	}

	// If no app ID, prompt user to select or create one
	if appID == "" {
		selectedID, err := selectOrCreateAppForPlatform(cmd, client, cfg, configPath, platform, platform)
		if err != nil {
			return err
		}
		appID = selectedID
	}

	// Generate version string if not provided
	versionStr := buildVersion
	if versionStr == "" {
		versionStr = build.GenerateVersionString()
	}

	ui.Println()
	ui.PrintInfo("Uploading: %s", filepath.Base(artifactPath))
	ui.PrintInfo("Build Version: %s", versionStr)

	// Convert tar.gz to zip for iOS builds (EAS produces tar.gz)
	if build.IsTarGz(artifactPath) {
		ui.Println()
		ui.StartSpinner("Extracting .app from tar.gz...")
		zipPath, err := build.ExtractAppFromTarGz(artifactPath)
		ui.StopSpinner()
		if err != nil {
			ui.PrintError("Failed to extract .app from tar.gz: %v", err)
			return err
		}
		defer os.Remove(zipPath) // Clean up temp zip after upload
		artifactPath = zipPath
		ui.PrintSuccess("Converted to: %s", filepath.Base(zipPath))
	} else if build.IsAppBundle(artifactPath) {
		// Zip .app directory for iOS builds (Flutter, React Native, Xcode)
		ui.Println()
		ui.StartSpinner("Zipping .app bundle...")
		zipPath, err := build.ZipAppBundle(artifactPath)
		ui.StopSpinner()
		if err != nil {
			ui.PrintError("Failed to zip .app bundle: %v", err)
			return err
		}
		defer os.Remove(zipPath) // Clean up temp zip after upload
		artifactPath = zipPath
		ui.PrintSuccess("Created: %s", filepath.Base(zipPath))
	}

	// Collect metadata
	metadata := build.CollectMetadata(cwd, platformCfg.Command, platform, buildDuration)

	// Handle dry-run mode
	if buildDryRun {
		ui.Println()
		ui.PrintInfo("Dry-run mode - showing what would be uploaded:")
		ui.Println()
		ui.PrintInfo("  Platform:       %s", platform)
		ui.PrintInfo("  Artifact:       %s", filepath.Base(artifactPath))
		ui.PrintInfo("  Build Version:  %s", versionStr)
		ui.PrintInfo("  App ID:         %s", appID)
		ui.PrintInfo("  Set Current:    %v", buildSetCurr)
		if metadata != nil {
			ui.PrintInfo("  Metadata:")
			if cmd, ok := metadata["build_command"].(string); ok {
				ui.PrintDim("    Build Command: %s", cmd)
			}
		}
		ui.Println()
		ui.PrintSuccess("Dry-run complete - no changes made")
		return nil
	}

	ui.Println()
	ui.StartSpinner("Uploading artifact...")

	result, err := client.UploadBuild(cmd.Context(), &api.UploadBuildRequest{
		AppID:        appID,
		Version:      versionStr,
		FilePath:     artifactPath,
		Metadata:     metadata,
		SetAsCurrent: buildSetCurr,
	})

	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Upload failed: %v", err)
		return err
	}

	ui.Println()
	ui.PrintSuccess("Upload complete!")
	ui.PrintInfo("App:             %s", appID)
	ui.PrintInfo("Build Version:   %s", result.Version)
	ui.PrintInfo("Build ID:        %s", result.VersionID)
	if result.PackageID != "" {
		ui.PrintInfo("Package ID:      %s", result.PackageID)
	}
	ui.Println()
	ui.PrintDim("To list builds: revyl build list --app %s", appID)

	// Suggest running a test if config has tests
	if !buildUploadJSON {
		cfg, cfgErr := config.LoadProjectConfig(configPath)
		if cfgErr == nil && cfg != nil && len(cfg.Tests) > 0 {
			for alias := range cfg.Tests {
				ui.PrintNextSteps([]ui.NextStep{
					{Label: "Run a test:", Command: fmt.Sprintf("revyl run %s", alias)},
				})
				break
			}
		} else {
			ui.PrintNextSteps([]ui.NextStep{
				{Label: "Create a test:", Command: "revyl test create <name>"},
			})
		}
	}

	return nil
}

// runBuildList lists uploaded build versions.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments
//
// Returns:
//   - error: Any error that occurred while listing builds
func runBuildList(cmd *cobra.Command, args []string) error {
	// Check authentication
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Create API client with dev mode support
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Determine app ID from flag or show org apps
	appID := appIDFlag

	// If we have an app ID, list builds for it
	if appID != "" {
		return listBuildVersions(cmd, client, appID)
	}

	// Otherwise, show all apps in the organization
	return listOrgApps(cmd, client)
}

// listBuildVersions lists versions for a specific app.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - client: The API client
//   - appID: The app ID to list builds for
//
// Returns:
//   - error: Any error that occurred while listing builds
func listBuildVersions(cmd *cobra.Command, client *api.Client, appID string) error {
	// Check if --json flag is set (either local or global)
	jsonOutput := buildListJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	if !jsonOutput {
		ui.StartSpinner("Fetching builds...")
	}
	versions, err := client.ListBuildVersions(cmd.Context(), appID)
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to list builds: %v", err)
		return err
	}

	if jsonOutput {
		output := map[string]interface{}{
			"app_id":   appID,
			"versions": versions,
			"count":    len(versions),
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(versions) == 0 {
		ui.PrintInfo("No builds found")
		return nil
	}

	ui.Println()
	ui.PrintInfo("Builds:")
	ui.Println()

	// Create table with dynamic column widths
	table := ui.NewTable("VERSION", "BUILD ID", "UPLOADED", "PACKAGE ID", "CURRENT")
	table.SetMinWidth(0, 10) // VERSION
	table.SetMinWidth(1, 36) // BUILD ID - UUIDs are 36 chars
	table.SetMinWidth(2, 12) // UPLOADED

	for _, v := range versions {
		current := ""
		if v.IsCurrent {
			current = "✓"
		}
		table.AddRow(v.Version, v.ID, v.UploadedAt, v.PackageID, current)
	}

	table.Render()

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Upload a new build:", Command: "revyl build upload"},
	})

	return nil
}

// listOrgApps lists all apps in the organization.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - client: The API client
//
// Returns:
//   - error: Any error that occurred while listing apps
func listOrgApps(cmd *cobra.Command, client *api.Client) error {
	// Check if --json flag is set (either local or global)
	jsonOutput := buildListJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	if !jsonOutput {
		ui.StartSpinner("Fetching apps from organization...")
	}
	result, err := client.ListApps(cmd.Context(), buildPlatform, 1, 50)
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
		ui.PrintInfo("Create apps at https://app.revyl.ai")
		return nil
	}

	ui.Println()
	ui.PrintInfo("Apps in your organization (%d total):", result.Total)
	ui.Println()

	// Create table with dynamic column widths
	table := ui.NewTable("NAME", "PLATFORM", "BUILDS", "LATEST", "APP ID")
	table.SetMinWidth(0, 20) // NAME - ensure readable width
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

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "List builds for an app:", Command: "revyl build list --app <id>"},
		{Label: "Upload a new build:", Command: "revyl build upload"},
	})

	return nil
}

// getPlatformNames returns a slice of platform names from the platforms map.
func getPlatformNames(platforms map[string]config.BuildPlatform) []string {
	names := make([]string, 0, len(platforms))
	for name := range platforms {
		names = append(names, name)
	}
	return names
}
