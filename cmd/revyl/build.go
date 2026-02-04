// Package main provides build commands for the Revyl CLI.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
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
  list    - List uploaded build versions`,
}

// buildUploadCmd builds and uploads the app.
var buildUploadCmd = &cobra.Command{
	Use:   "upload",
	Short: "Build and upload the app",
	Long: `Build the app and upload it to Revyl.

By default, builds both iOS and Android concurrently if both variants are configured.
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
  revyl build upload --build-var <id>   # Upload to specific build variable
  revyl build upload --name "My App"    # Create build variable with specified name
  revyl build upload --name "My App" -y # Create and auto-save to config`,
	RunE: runBuildUpload,
}

// buildListCmd lists uploaded build versions.
var buildListCmd = &cobra.Command{
	Use:   "list",
	Short: "List uploaded build versions",
	Long: `List all uploaded build versions.

If a build variable is configured locally, lists versions for that build.
Otherwise, shows all build variables in your organization.

Examples:
  revyl build list                           # List versions (or show org builds)
  revyl build list --build-var <id>          # List versions for specific build var
  revyl build list --platform android        # Filter org builds by platform`,
	RunE: runBuildList,
}

var (
	buildVariant       string
	buildSkip          bool
	buildVersion       string
	buildSetCurr       bool
	buildVarIDFlag     string
	buildPlatform      string
	uploadBuildVarFlag string
	uploadPlatformFlag string
	uploadNameFlag     string
	uploadYesFlag      bool
)

func init() {
	buildCmd.AddCommand(buildUploadCmd)
	buildCmd.AddCommand(buildListCmd)

	buildUploadCmd.Flags().StringVar(&buildVariant, "variant", "", "Build variant to use (e.g., release, staging)")
	buildUploadCmd.Flags().BoolVar(&buildSkip, "skip-build", false, "Skip build step, upload existing artifact")
	buildUploadCmd.Flags().StringVar(&buildVersion, "version", "", "Version string for the upload (default: auto-generated)")
	buildUploadCmd.Flags().BoolVar(&buildSetCurr, "set-current", false, "Set this version as the current version")
	buildUploadCmd.Flags().StringVar(&uploadBuildVarFlag, "build-var", "", "Build variable ID to upload to")
	buildUploadCmd.Flags().StringVar(&uploadPlatformFlag, "platform", "", "Platform to build for (ios, android)")
	buildUploadCmd.Flags().StringVar(&uploadNameFlag, "name", "", "Name for new build variable (used when creating)")
	buildUploadCmd.Flags().BoolVarP(&uploadYesFlag, "yes", "y", false, "Automatically confirm prompts (e.g., save to config)")

	buildListCmd.Flags().StringVar(&buildVarIDFlag, "build-var", "", "Build variable ID to list versions for")
	buildListCmd.Flags().StringVar(&buildPlatform, "platform", "", "Filter by platform (android, ios) when listing org builds")
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
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
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
		return runSinglePlatformBuild(cmd, cfg, configPath, creds, uploadPlatformFlag)
	}

	// If --variant is specified, use legacy single-platform behavior
	if buildVariant != "" {
		return runLegacySingleBuild(cmd, cfg, configPath, creds)
	}

	// Check if both ios and android variants exist for concurrent builds
	_, hasIOS := cfg.Build.Variants["ios"]
	_, hasAndroid := cfg.Build.Variants["android"]

	if hasIOS && hasAndroid {
		// Default: run concurrent builds for both platforms
		return runConcurrentBuilds(cmd, cfg, configPath, creds)
	}

	// Fall back to legacy single build if only one platform is configured
	return runLegacySingleBuild(cmd, cfg, configPath, creds)
}

// runLegacySingleBuild runs the legacy single-platform build using default config.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - cfg: The project configuration
//   - configPath: Path to the config file
//   - creds: Authentication credentials
//
// Returns:
//   - error: Any error that occurred during the build/upload process
func runLegacySingleBuild(cmd *cobra.Command, cfg *config.ProjectConfig, configPath string, creds *auth.Credentials) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Determine build config
	buildCfg := cfg.Build
	var variant config.BuildVariant
	var variantName string
	if buildVariant != "" {
		var ok bool
		variant, ok = cfg.Build.Variants[buildVariant]
		if !ok {
			ui.PrintError("Unknown build variant: %s", buildVariant)
			ui.PrintInfo("Available variants: %v", getVariantNames(cfg.Build.Variants))
			return fmt.Errorf("unknown variant: %s", buildVariant)
		}
		variantName = buildVariant
		buildCfg.Command = variant.Command
		buildCfg.Output = variant.Output
	}

	if buildCfg.Command == "" || buildCfg.Output == "" {
		ui.PrintError("Build configuration incomplete")
		ui.PrintInfo("Please configure build.command and build.output in .revyl/config.yaml")
		return fmt.Errorf("incomplete build config")
	}

	// Determine platform from command
	platform := determinePlatform(buildCfg.Command, "")

	ui.PrintBanner(version)
	ui.PrintInfo("Build and Upload")
	ui.Println()

	var buildDuration time.Duration

	// Run build if not skipped
	if !buildSkip {
		ui.PrintInfo("Building with: %s", buildCfg.Command)
		ui.Println()

		startTime := time.Now()
		runner := build.NewRunner(cwd)

		err = runner.Run(buildCfg.Command, func(line string) {
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
	artifactPath, err := build.ResolveArtifactPath(cwd, buildCfg.Output)
	if err != nil {
		ui.PrintError("Build artifact not found: %s", buildCfg.Output)
		return fmt.Errorf("artifact not found: %w", err)
	}

	// Create API client
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(creds.APIKey, devMode)

	// Determine build variable ID from variant
	buildVarID := uploadBuildVarFlag
	if buildVarID == "" && variantName != "" {
		buildVarID = variant.BuildVarID
	}

	// If no build var ID, prompt user to select or create one
	if buildVarID == "" {
		selectedID, err := selectOrCreateBuildVarForVariant(cmd, client, cfg, configPath, variantName, platform)
		if err != nil {
			return err
		}
		buildVarID = selectedID
	}

	// Generate version string if not provided
	versionStr := buildVersion
	if versionStr == "" {
		versionStr = build.GenerateVersionString()
	}

	ui.Println()
	ui.PrintInfo("Uploading: %s", filepath.Base(artifactPath))
	ui.PrintInfo("Version: %s", versionStr)

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
	metadata := build.CollectMetadata(cwd, buildCfg.Command, buildVariant, buildDuration)

	ui.Println()
	ui.StartSpinner("Uploading artifact...")

	result, err := client.UploadBuild(cmd.Context(), &api.UploadBuildRequest{
		BuildVarID:   buildVarID,
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
	ui.PrintInfo("Version ID: %s", result.VersionID)
	ui.PrintInfo("Version: %s", result.Version)
	if result.PackageID != "" {
		ui.PrintInfo("Package ID: %s", result.PackageID)
	}

	return nil
}

// determinePlatform extracts the platform from the build command or flag.
//
// Parameters:
//   - command: The build command
//   - platformFlag: The platform flag value
//
// Returns:
//   - string: The platform (ios or android)
func determinePlatform(command, platformFlag string) string {
	if platformFlag != "" {
		return platformFlag
	}

	// Try to detect from command
	if strings.Contains(command, "ios") || strings.Contains(command, "iphonesimulator") {
		return "ios"
	}
	if strings.Contains(command, "android") || strings.Contains(command, "apk") || strings.Contains(command, "aab") {
		return "android"
	}

	return ""
}

// selectOrCreateBuildVar prompts the user to select an existing build variable or create a new one.
// This is a legacy function - prefer selectOrCreateBuildVarForVariant for variant-based configs.
//
// Parameters:
//   - cmd: The cobra command
//   - client: The API client
//   - cfg: The project config
//   - configPath: Path to the config file
//   - platform: The target platform
//
// Returns:
//   - string: The selected or created build variable ID
//   - error: Any error that occurred
func selectOrCreateBuildVar(cmd *cobra.Command, client *api.Client, cfg *config.ProjectConfig, configPath, platform string) (string, error) {
	return selectOrCreateBuildVarForVariant(cmd, client, cfg, configPath, "", platform)
}

// selectOrCreateBuildVarForVariant prompts the user to select an existing build variable or create a new one,
// and saves it to the specified variant in the config.
//
// Parameters:
//   - cmd: The cobra command
//   - client: The API client
//   - cfg: The project config
//   - configPath: Path to the config file
//   - variantName: The variant name to save the build var ID to (empty for no save)
//   - platform: The target platform
//
// Returns:
//   - string: The selected or created build variable ID
//   - error: Any error that occurred
func selectOrCreateBuildVarForVariant(cmd *cobra.Command, client *api.Client, cfg *config.ProjectConfig, configPath, variantName, platform string) (string, error) {
	ui.Println()
	ui.PrintWarning("No build variable configured for this project.")
	ui.Println()
	ui.PrintDim("A build variable stores your app builds in Revyl so tests can run against them.")
	ui.Println()

	// Fetch existing build variables
	ui.StartSpinner("Fetching build variables...")
	result, err := client.ListOrgBuildVars(cmd.Context(), platform, 1, 50)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to fetch build variables: %v", err)
		return "", err
	}

	var buildVarID string

	// If no existing build variables, skip selection and create directly
	if len(result.Items) == 0 {
		ui.PrintInfo("No existing build variables found. Let's create one.")
		ui.Println()
		buildVarID, err = createNewBuildVar(cmd, client, cfg, platform)
		if err != nil {
			return "", err
		}
	} else {
		// Build options list
		var options []string
		for _, bv := range result.Items {
			options = append(options, fmt.Sprintf("%s (%s)", bv.Name, bv.Platform))
		}
		options = append(options, "Create new build variable")

		// Show selection prompt
		ui.PrintInfo("Select a build variable to upload to:")
		selection, err := ui.PromptSelect("", options)
		if err != nil {
			return "", err
		}

		// If user selected "Create new"
		if selection == len(result.Items) {
			buildVarID, err = createNewBuildVar(cmd, client, cfg, platform)
			if err != nil {
				return "", err
			}
		} else {
			buildVarID = result.Items[selection].ID
			ui.PrintSuccess("Selected: %s", result.Items[selection].Name)
		}
	}

	// Ask if user wants to save this to config (auto-confirm with --yes flag)
	save := uploadYesFlag
	if !save {
		var err error
		save, err = ui.PromptConfirm("Save this build variable to .revyl/config.yaml for future uploads?", true)
		if err != nil {
			return buildVarID, nil // Continue even if prompt fails
		}
	}

	if save && variantName != "" {
		// Save to the variant
		variant := cfg.Build.Variants[variantName]
		variant.BuildVarID = buildVarID
		cfg.Build.Variants[variantName] = variant
		if err := config.WriteProjectConfig(configPath, cfg); err != nil {
			ui.PrintWarning("Failed to save config: %v", err)
		} else {
			ui.PrintSuccess("Saved to .revyl/config.yaml")
		}
	}

	return buildVarID, nil
}

// createNewBuildVar prompts the user to create a new build variable.
//
// Parameters:
//   - cmd: The cobra command
//   - client: The API client
//   - cfg: The project config
//   - platform: The suggested platform
//
// Returns:
//   - string: The created build variable ID
//   - error: Any error that occurred
func createNewBuildVar(cmd *cobra.Command, client *api.Client, cfg *config.ProjectConfig, platform string) (string, error) {
	ui.Println()
	ui.PrintInfo("Creating new build variable...")
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

	// Create the build variable
	ui.Println()
	ui.StartSpinner("Creating build variable...")

	result, err := client.CreateBuildVar(cmd.Context(), &api.CreateBuildVarRequest{
		Name:     name,
		Platform: platform,
	})

	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to create build variable: %v", err)
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

	// BuildVarID is the build variable ID used for upload.
	BuildVarID string

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
//   - creds: Authentication credentials
//
// Returns:
//   - error: Any error that occurred (aggregated from both platforms)
func runConcurrentBuilds(cmd *cobra.Command, cfg *config.ProjectConfig, configPath string, creds *auth.Credentials) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Validate both platforms exist in config
	platforms := []string{"ios", "android"}
	for _, platform := range platforms {
		if _, ok := cfg.Build.Variants[platform]; !ok {
			ui.PrintError("Platform variant '%s' not found in config", platform)
			ui.PrintInfo("Available variants: %v", getVariantNames(cfg.Build.Variants))
			return fmt.Errorf("missing platform variant: %s", platform)
		}
	}

	// Create API client
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(creds.APIKey, devMode)

	// Check and prompt for missing build variable IDs before starting builds
	for _, platform := range platforms {
		// Check variant-level build_var_id
		variant := cfg.Build.Variants[platform]
		buildVarID := variant.BuildVarID

		if buildVarID == "" {
			ui.Println()
			ui.PrintWarning("No build variable configured for %s", platform)
			selectedID, err := selectOrCreateBuildVarForPlatform(cmd, client, cfg, platform)
			if err != nil {
				return err
			}
			// Store in variant
			variant.BuildVarID = selectedID
			cfg.Build.Variants[platform] = variant
		}
	}

	// Save updated config with build var IDs
	if err := config.WriteProjectConfig(configPath, cfg); err != nil {
		ui.PrintWarning("Failed to save config: %v", err)
	} else {
		ui.PrintSuccess("Saved build variable IDs to .revyl/config.yaml")
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
			ui.PrintInfo("  Version: %s", result.UploadResult.Version)
			ui.PrintInfo("  Version ID: %s", result.UploadResult.VersionID)
			if result.UploadResult.PackageID != "" {
				ui.PrintInfo("  Package ID: %s", result.UploadResult.PackageID)
			}
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("%d platform(s) failed", len(errors))
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

	variant := cfg.Build.Variants[platform]

	// Build
	if !buildSkip {
		outputMu.Lock()
		ui.PrintInfo("[%s] Building with: %s", platform, variant.Command)
		outputMu.Unlock()

		startTime := time.Now()
		runner := build.NewRunner(cwd)

		err := runner.Run(variant.Command, func(line string) {
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
	artifactPath, err := build.ResolveArtifactPath(cwd, variant.Output)
	if err != nil {
		result.Error = fmt.Errorf("artifact not found: %w", err)
		return result
	}
	result.ArtifactPath = artifactPath

	// Get build variable ID from variant
	buildVarID := variant.BuildVarID
	result.BuildVarID = buildVarID

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
	metadata := build.CollectMetadata(cwd, variant.Command, platform, result.Duration)

	// Upload
	uploadResult, err := client.UploadBuild(cmd.Context(), &api.UploadBuildRequest{
		BuildVarID:   buildVarID,
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

// selectOrCreateBuildVarForPlatform prompts the user to select or create a build variable for a specific platform.
//
// Parameters:
//   - cmd: The cobra command
//   - client: The API client
//   - cfg: The project config
//   - platform: The target platform
//
// Returns:
//   - string: The selected or created build variable ID
//   - error: Any error that occurred
func selectOrCreateBuildVarForPlatform(cmd *cobra.Command, client *api.Client, cfg *config.ProjectConfig, platform string) (string, error) {
	// Fetch existing build variables for this platform
	ui.StartSpinner(fmt.Sprintf("Fetching %s build variables...", platform))
	result, err := client.ListOrgBuildVars(cmd.Context(), platform, 1, 50)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to fetch build variables: %v", err)
		return "", err
	}

	var buildVarID string

	// If no existing build variables for this platform, create directly
	if len(result.Items) == 0 {
		ui.PrintInfo("No existing %s build variables found. Creating one...", platform)
		buildVarID, err = createNewBuildVarForPlatform(cmd, client, cfg, platform)
		if err != nil {
			return "", err
		}
	} else {
		// Build options list
		var options []string
		for _, bv := range result.Items {
			options = append(options, fmt.Sprintf("%s (%s)", bv.Name, bv.Platform))
		}
		options = append(options, fmt.Sprintf("Create new %s build variable", platform))

		// Show selection prompt
		ui.PrintInfo("Select a build variable for %s:", platform)
		selection, err := ui.PromptSelect("", options)
		if err != nil {
			return "", err
		}

		// If user selected "Create new"
		if selection == len(result.Items) {
			buildVarID, err = createNewBuildVarForPlatform(cmd, client, cfg, platform)
			if err != nil {
				return "", err
			}
		} else {
			buildVarID = result.Items[selection].ID
			ui.PrintSuccess("Selected: %s", result.Items[selection].Name)
		}
	}

	return buildVarID, nil
}

// createNewBuildVarForPlatform creates a new build variable for a specific platform.
//
// Parameters:
//   - cmd: The cobra command
//   - client: The API client
//   - cfg: The project config
//   - platform: The target platform
//
// Returns:
//   - string: The created build variable ID
//   - error: Any error that occurred
func createNewBuildVarForPlatform(cmd *cobra.Command, client *api.Client, cfg *config.ProjectConfig, platform string) (string, error) {
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

	// Create the build variable
	ui.StartSpinner(fmt.Sprintf("Creating %s build variable...", platform))

	result, err := client.CreateBuildVar(cmd.Context(), &api.CreateBuildVarRequest{
		Name:     name,
		Platform: platform,
	})

	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to create build variable: %v", err)
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
//   - creds: Authentication credentials
//   - platform: The platform to build
//
// Returns:
//   - error: Any error that occurred during the build/upload process
func runSinglePlatformBuild(cmd *cobra.Command, cfg *config.ProjectConfig, configPath string, creds *auth.Credentials, platform string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Get variant for platform
	variant, ok := cfg.Build.Variants[platform]
	if !ok {
		ui.PrintError("Unknown platform: %s", platform)
		ui.PrintInfo("Available platforms: %v", getVariantNames(cfg.Build.Variants))
		return fmt.Errorf("unknown platform: %s", platform)
	}

	ui.PrintBanner(version)
	ui.PrintInfo("Build and Upload (%s)", platform)
	ui.Println()

	var buildDuration time.Duration

	// Run build if not skipped
	if !buildSkip {
		ui.PrintInfo("Building with: %s", variant.Command)
		ui.Println()

		startTime := time.Now()
		runner := build.NewRunner(cwd)

		err = runner.Run(variant.Command, func(line string) {
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
	artifactPath, err := build.ResolveArtifactPath(cwd, variant.Output)
	if err != nil {
		ui.PrintError("Build artifact not found: %s", variant.Output)
		return fmt.Errorf("artifact not found: %w", err)
	}

	// Create API client
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(creds.APIKey, devMode)

	// Determine build variable ID from variant
	buildVarID := uploadBuildVarFlag
	if buildVarID == "" {
		buildVarID = variant.BuildVarID
	}

	// If no build var ID, prompt user to select or create one
	if buildVarID == "" {
		selectedID, err := selectOrCreateBuildVarForVariant(cmd, client, cfg, configPath, platform, platform)
		if err != nil {
			return err
		}
		buildVarID = selectedID
	}

	// Generate version string if not provided
	versionStr := buildVersion
	if versionStr == "" {
		versionStr = build.GenerateVersionString()
	}

	ui.Println()
	ui.PrintInfo("Uploading: %s", filepath.Base(artifactPath))
	ui.PrintInfo("Version: %s", versionStr)

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
	metadata := build.CollectMetadata(cwd, variant.Command, platform, buildDuration)

	ui.Println()
	ui.StartSpinner("Uploading artifact...")

	result, err := client.UploadBuild(cmd.Context(), &api.UploadBuildRequest{
		BuildVarID:   buildVarID,
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
	ui.PrintInfo("Version ID: %s", result.VersionID)
	ui.PrintInfo("Version: %s", result.Version)
	if result.PackageID != "" {
		ui.PrintInfo("Package ID: %s", result.PackageID)
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
	authMgr := auth.NewManager()
	creds, err := authMgr.GetCredentials()
	if err != nil || creds.APIKey == "" {
		ui.PrintError("Not authenticated. Run 'revyl auth login' first.")
		return fmt.Errorf("not authenticated")
	}

	// Create API client with dev mode support
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(creds.APIKey, devMode)

	// Determine build var ID from flag or show org builds
	buildVarID := buildVarIDFlag

	// If we have a build var ID, list versions for it
	if buildVarID != "" {
		return listBuildVersions(cmd, client, buildVarID)
	}

	// Otherwise, show all build variables in the organization
	return listOrgBuildVars(cmd, client)
}

// listBuildVersions lists versions for a specific build variable.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - client: The API client
//   - buildVarID: The build variable ID to list versions for
//
// Returns:
//   - error: Any error that occurred while listing versions
func listBuildVersions(cmd *cobra.Command, client *api.Client, buildVarID string) error {
	ui.StartSpinner("Fetching build versions...")
	versions, err := client.ListBuildVersions(cmd.Context(), buildVarID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to list versions: %v", err)
		return err
	}

	if len(versions) == 0 {
		ui.PrintInfo("No build versions found")
		return nil
	}

	ui.Println()
	ui.PrintInfo("Build Versions:")
	ui.Println()

	// Create table with dynamic column widths
	table := ui.NewTable("VERSION", "UPLOADED", "PACKAGE ID", "CURRENT")
	table.SetMinWidth(0, 10) // VERSION
	table.SetMinWidth(1, 12) // UPLOADED

	for _, v := range versions {
		current := ""
		if v.IsCurrent {
			current = "✓"
		}
		table.AddRow(v.Version, v.UploadedAt, v.PackageID, current)
	}

	table.Render()
	return nil
}

// listOrgBuildVars lists all build variables in the organization.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - client: The API client
//
// Returns:
//   - error: Any error that occurred while listing build variables
func listOrgBuildVars(cmd *cobra.Command, client *api.Client) error {
	ui.StartSpinner("Fetching build variables from organization...")
	result, err := client.ListOrgBuildVars(cmd.Context(), buildPlatform, 1, 50)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to list build variables: %v", err)
		return err
	}

	if len(result.Items) == 0 {
		ui.PrintInfo("No build variables found in your organization")
		ui.PrintInfo("Create build variables at https://app.revyl.ai")
		return nil
	}

	ui.Println()
	ui.PrintInfo("Build Variables in your organization (%d total):", result.Total)
	ui.Println()

	// Create table with dynamic column widths
	table := ui.NewTable("NAME", "PLATFORM", "VERSIONS", "LATEST", "ID")
	table.SetMinWidth(0, 20) // NAME - ensure readable width
	table.SetMinWidth(1, 8)  // PLATFORM
	table.SetMinWidth(4, 36) // ID - UUIDs are 36 chars

	for _, bv := range result.Items {
		latestVer := "-"
		if bv.LatestVersion != "" {
			latestVer = bv.LatestVersion
		}
		table.AddRow(bv.Name, bv.Platform, fmt.Sprintf("%d", bv.VersionsCount), latestVer, bv.ID)
	}

	table.Render()

	ui.Println()
	ui.PrintDim("To list versions for a build variable:")
	ui.PrintDim("  revyl build list --build-var <id>")
	ui.Println()
	ui.PrintDim("Or configure build.build_var_id in .revyl/config.yaml")

	return nil
}

// getVariantNames returns a slice of variant names from the variants map.
func getVariantNames(variants map[string]config.BuildVariant) []string {
	names := make([]string, 0, len(variants))
	for name := range variants {
		names = append(names, name)
	}
	return names
}
