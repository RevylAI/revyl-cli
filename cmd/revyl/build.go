// Package main provides build commands for the Revyl CLI.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/buildselection"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// buildCmd is the parent command for build operations.
var buildCmd = &cobra.Command{
	Use:   "build [--platform ios|android|<config-key>] [--remote] [--env KEY=VALUE] [--secret NAME] [--timeout <seconds>] [--version <version>] [--detach] [--no-cache] [--no-set-current] [--json]",
	Short: "Build and manage app builds",
	Long: `Build the app from source and upload the generated artifact to Revyl.

By default, this runs the configured local build command from .revyl/config.yaml,
finds the generated artifact, and registers it as a Revyl build version.

Use --remote to run the build on Revyl cloud build runners.`,
	DisableFlagsInUseLine: true,
	Args:                  cobra.NoArgs,
	RunE:                  runBuild,
}

// buildUploadCmd uploads an existing app artifact.
var buildUploadCmd = &cobra.Command{
	Use:   "upload",
	Short: "Upload an existing build artifact",
	Long: `Upload an existing build artifact and register it in Revyl.

This command does not run a build. Use ` + "`revyl build`" + ` to build from source.`,
	Example: `  revyl build upload --file ./app.apk --app <id>
  revyl build upload --file ./build/App.app.zip --platform ios --yes
  revyl build upload --url https://artifacts.example.com/app.apk --app <id>
  revyl build upload --url https://artifacts.example.com/app.ipa --header "Authorization: Bearer <token>"
  revyl build upload --file ./app.apk --version 1.2.3 --no-set-current --json`,
	RunE: runBuildUpload,
}

// buildRemoteCmd is retained only to guide users to the new --remote flag.
var buildRemoteCmd = &cobra.Command{
	Use:    "remote",
	Short:  "Deprecated: use revyl build --remote",
	Long:   "`revyl build remote` has been replaced by `revyl build --remote`.",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return deprecatedBuildRemoteError()
	},
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
	Example: `  revyl build list
  revyl build list --app <id> --json
  revyl build list --platform android`,
	RunE: runBuildList,
}

// buildCancelCmd cancels a running remote build job.
var buildCancelCmd = &cobra.Command{
	Use:   "cancel <build-job-id>",
	Short: "Cancel a running remote build",
	Long: `Cancel a running remote build by its build job ID.

The job ID is returned by "revyl build --remote --detach" and shown in JSON
output from "revyl build --remote --json".`,
	Example: `  revyl build cancel <build-job-id>
  revyl build cancel <build-job-id> --json`,
	Args: cobra.ExactArgs(1),
	// API failures (already terminal, not found) are not usage errors.
	SilenceUsage: true,
	RunE:         runCancelBuild,
}

// buildStatusCmd shows or follows a remote build job.
var buildStatusCmd = &cobra.Command{
	Use:   "status <build-job-id>",
	Short: "Show remote build status",
	Long: `Show the status and recent logs for a remote build job.

Use --follow to stream status until the build reaches a terminal state.`,
	Example: `  revyl build status <build-job-id>
  revyl build status <build-job-id> --follow
  revyl build status <build-job-id> --follow --debug --json`,
	Args: cobra.ExactArgs(1),
	RunE: runBuildStatus,
}

var (
	buildSkip                 bool
	buildVersion              string
	buildSetCurr              bool
	buildNoSetCurrent         bool
	buildCommandJSON          bool
	buildCommandPlatform      string
	buildCommandImage         string
	buildCommandRemote        bool
	buildDetachFlag           bool
	buildNoCacheFlag          bool
	buildRequireConfiguredApp bool
	appIDFlag                 string
	buildPlatform             string
	uploadAppFlag             string
	uploadPlatformFlag        string
	uploadNameFlag            string
	uploadFileFlag            string
	uploadURLFlag             string
	uploadHeaderFlags         []string
	uploadYesFlag             bool
	uploadIncludeDirtyFlag    bool
	buildListJSON             bool
	buildListBranch           string
	buildUploadJSON           bool
	buildDryRun               bool
	uploadSchemeFlag          string
	uploadRemoteFlag          bool
	uploadCleanFlag           bool
	buildEnvFlags             []string
	buildSecretRefFlags       []string
	buildTimeoutSeconds       int
	remotePlatformFlag        string
	remoteAppFlag             string
	remoteVersionFlag         string
	remoteSetCurrFlag         bool
	remoteJSONFlag            bool
	remoteCleanFlag           bool
	remoteCommittedOnly       bool
	remoteDeprecatedEnvFlags  []string
	buildStatusJSON           bool
	buildStatusFollow         bool
)

var buildHostGOOS = runtime.GOOS

func init() {
	buildCmd.AddCommand(buildStatusCmd)
	buildCmd.AddCommand(buildListCmd)
	buildCmd.AddCommand(buildCancelCmd)
	buildCmd.AddCommand(buildUploadCmd)
	buildCmd.AddCommand(buildRemoteCmd)

	buildCmd.Flags().SortFlags = false
	buildUploadCmd.Flags().SortFlags = false
	buildStatusCmd.Flags().SortFlags = false

	buildCmd.Flags().StringVar(&buildCommandPlatform, "platform", "", "Build platform key from .revyl/config.yaml, e.g. ios, android, ios-dev")
	buildCmd.Flags().StringVar(&buildCommandImage, "image", "", "Remote build image key, e.g. ios-macos-26-xcode-26.2")
	buildCmd.Flags().BoolVar(&buildCommandRemote, "remote", false, "Run the build on Revyl cloud build runners")
	buildCmd.Flags().StringArrayVar(&buildEnvFlags, "env", nil, "Remote build environment override (repeatable: --env KEY=VALUE)")
	buildCmd.Flags().StringArrayVar(&buildSecretRefFlags, "secret", nil, "Build secret name (repeatable; local builds read the process environment)")
	buildCmd.Flags().IntVar(&buildTimeoutSeconds, "timeout", 0, "Remote build timeout in seconds (overrides build.platforms.<platform>.timeout from config when set)")
	buildCmd.Flags().StringVar(&buildVersion, "version", "", "Version string for the build (default: auto-generated)")
	buildCmd.Flags().BoolVar(&buildDetachFlag, "detach", false, "Queue remote build and return immediately")
	buildCmd.Flags().BoolVar(&buildNoCacheFlag, "no-cache", false, "Run remote build without restoring or saving configured caches")
	buildCmd.Flags().BoolVar(&buildNoSetCurrent, "no-set-current", false, "Do not set this build version as the app's current version")
	buildCmd.Flags().BoolVar(&buildCommandJSON, "json", false, "Output result as JSON")
	analytics.MarkFlagValue(buildCmd, "platform")
	analytics.MarkFlagValue(buildCmd, "image")
	analytics.MarkFlagValue(buildCmd, "version")
	analytics.MarkFlagValue(buildCmd, "remote")
	analytics.MarkFlagValue(buildCmd, "timeout")
	analytics.MarkFlagValue(buildCmd, "detach")
	analytics.MarkFlagValue(buildCmd, "no-cache")
	analytics.MarkFlagValue(buildCmd, "no-set-current")
	analytics.MarkFlagValue(buildCmd, "json")

	buildUploadCmd.Flags().StringVar(&buildVersion, "version", "", "Version string for the uploaded artifact (default: auto-generated)")
	buildUploadCmd.Flags().BoolVar(&buildNoSetCurrent, "no-set-current", false, "Do not set this build version as the app's current version")
	buildUploadCmd.Flags().StringVar(&uploadAppFlag, "app", "", "App name or ID to upload to")
	buildUploadCmd.Flags().StringVar(&uploadPlatformFlag, "platform", "", "Mobile platform or build key from .revyl/config.yaml, e.g. ios, android, ios-dev")
	buildUploadCmd.Flags().StringVar(&uploadNameFlag, "name", "", "Name for new app (used when creating)")
	buildUploadCmd.Flags().BoolVarP(&uploadYesFlag, "yes", "y", false, "Automatically confirm prompts (e.g., save to config)")
	buildUploadCmd.Flags().BoolVar(&buildUploadJSON, "json", false, "Output results as JSON")
	buildUploadCmd.Flags().StringVarP(&uploadFileFlag, "file", "f", "", "Path to a build artifact to upload directly (skips config-based build)")
	buildUploadCmd.Flags().StringVar(&uploadURLFlag, "url", "", "URL of a remote artifact to register (Artifactory, S3, GCS, GitHub Actions)")
	buildUploadCmd.Flags().StringArrayVar(&uploadHeaderFlags, "header", nil, `HTTP header for authenticated URL downloads (repeatable, format "Name: value")`)
	buildUploadCmd.Flags().BoolVar(&uploadRemoteFlag, "remote", false, "Build remotely on Revyl's cloud build runners")
	_ = buildUploadCmd.Flags().MarkHidden("remote")
	buildUploadCmd.Flags().BoolVar(&buildSkip, "skip-build", false, "Deprecated")
	_ = buildUploadCmd.Flags().MarkHidden("skip-build")
	buildUploadCmd.Flags().BoolVar(&buildDryRun, "dry-run", false, "Deprecated")
	_ = buildUploadCmd.Flags().MarkHidden("dry-run")
	buildUploadCmd.Flags().BoolVar(&uploadCleanFlag, "clean", false, "Deprecated")
	_ = buildUploadCmd.Flags().MarkHidden("clean")
	buildUploadCmd.Flags().BoolVar(&uploadIncludeDirtyFlag, "include-dirty", false, "Deprecated")
	_ = buildUploadCmd.Flags().MarkHidden("include-dirty")
	buildUploadCmd.Flags().BoolVar(&buildSetCurr, "set-current", true, "Deprecated")
	_ = buildUploadCmd.Flags().MarkHidden("set-current")
	analytics.MarkFlagValue(buildUploadCmd, "platform")
	analytics.MarkFlagValue(buildUploadCmd, "yes")
	analytics.MarkFlagValue(buildUploadCmd, "json")
	analytics.MarkFlagValue(buildUploadCmd, "version")
	analytics.MarkFlagValue(buildUploadCmd, "no-set-current")

	buildRemoteCmd.Flags().StringVar(&remotePlatformFlag, "platform", "", "Deprecated")
	buildRemoteCmd.Flags().StringVar(&remoteAppFlag, "app", "", "Deprecated")
	buildRemoteCmd.Flags().StringVar(&remoteVersionFlag, "version", "", "Deprecated")
	buildRemoteCmd.Flags().BoolVar(&remoteSetCurrFlag, "set-current", true, "Deprecated")
	buildRemoteCmd.Flags().BoolVar(&remoteJSONFlag, "json", false, "Deprecated")
	buildRemoteCmd.Flags().BoolVar(&remoteCleanFlag, "clean", false, "Deprecated")
	buildRemoteCmd.Flags().BoolVar(&remoteCommittedOnly, "committed-only", false, "Deprecated")
	buildRemoteCmd.Flags().StringArrayVar(&remoteDeprecatedEnvFlags, "env", nil, "Deprecated")
	_ = buildRemoteCmd.Flags().MarkHidden("platform")
	_ = buildRemoteCmd.Flags().MarkHidden("app")
	_ = buildRemoteCmd.Flags().MarkHidden("version")
	_ = buildRemoteCmd.Flags().MarkHidden("set-current")
	_ = buildRemoteCmd.Flags().MarkHidden("json")
	_ = buildRemoteCmd.Flags().MarkHidden("clean")
	_ = buildRemoteCmd.Flags().MarkHidden("committed-only")
	_ = buildRemoteCmd.Flags().MarkHidden("env")

	buildListCmd.Flags().StringVar(&appIDFlag, "app", "", "App name or ID to list builds for")
	buildListCmd.Flags().StringVar(&buildPlatform, "platform", "", "Filter by platform (android, ios) when listing org apps")
	buildListCmd.Flags().BoolVar(&buildListJSON, "json", false, "Output results as JSON")
	buildListCmd.Flags().StringVar(&buildListBranch, "branch", "", "Filter builds by git branch (use HEAD for current branch)")
	analytics.MarkFlagValue(buildListCmd, "platform")
	analytics.MarkFlagValue(buildListCmd, "json")

	buildStatusCmd.Flags().BoolVar(&buildStatusJSON, "json", false, "Output status as JSON")
	buildStatusCmd.Flags().BoolVar(&buildStatusFollow, "follow", false, "Follow until the remote build reaches a terminal state")
	analytics.MarkFlagValue(buildStatusCmd, "json")
	analytics.MarkFlagValue(buildStatusCmd, "follow")
}

func runBuild(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	jsonOutput := buildCommandJSON
	if v, _ := cmd.Root().PersistentFlags().GetBool("json"); v {
		jsonOutput = true
	}
	if jsonOutput {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
	}

	if !buildCommandRemote {
		if len(buildEnvFlags) > 0 {
			return fmt.Errorf("--env is only supported with --remote")
		}
		if cmd.Flags().Changed("timeout") {
			return fmt.Errorf("--timeout is only supported with --remote")
		}
		if buildDetachFlag {
			return fmt.Errorf("--detach is only supported with --remote")
		}
		if buildNoCacheFlag {
			return fmt.Errorf("--no-cache is only supported with --remote")
		}
		if err := checkLocalBuildSupported(); err != nil {
			return err
		}
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	setCurrent := !buildNoSetCurrent
	if buildCommandRemote {
		envOverrides, err := parseRemoteBuildEnvOverrides(buildEnvFlags)
		if err != nil {
			return err
		}
		platform := strings.TrimSpace(buildCommandPlatform)
		if platform == "" {
			platform = "ios"
		}
		// Only the --timeout flag resolves here; the per-platform config timeout
		// is applied in runRemoteBuildWithOptions once the actual platform key
		// (possibly non-canonical, e.g. ios-release) is known.
		timeoutSeconds, err := remoteBuildTimeoutFlagSeconds(buildTimeoutSeconds, cmd.Flags().Changed("timeout"))
		if err != nil {
			return err
		}
		return runRemoteBuildWithOptions(cmd, apiKey, remoteBuildOptions{
			Platform:       platform,
			Version:        buildVersion,
			Image:          strings.TrimSpace(buildCommandImage),
			Env:            envOverrides,
			Secrets:        append([]string(nil), buildSecretRefFlags...),
			SetCurrent:     setCurrent,
			Clean:          buildNoCacheFlag,
			JSON:           jsonOutput,
			Wait:           !buildDetachFlag,
			IncludeDirty:   true,
			CommittedOnly:  false,
			TimeoutSeconds: timeoutSeconds,
		})
	}

	return runConfiguredBuild(cmd, apiKey, buildCommandPlatform, buildVersion, setCurrent, jsonOutput)
}

func runConfiguredBuild(cmd *cobra.Command, apiKey, platform, version string, setCurrent, jsonOutput bool) error {
	previousVersion := buildVersion
	previousSetCurrent := buildSetCurr
	previousJSON := buildUploadJSON
	previousSkip := buildSkip
	previousDryRun := buildDryRun
	previousApp := uploadAppFlag
	previousName := uploadNameFlag
	previousYes := uploadYesFlag
	previousScheme := uploadSchemeFlag
	previousRequireApp := buildRequireConfiguredApp
	defer func() {
		buildVersion = previousVersion
		buildSetCurr = previousSetCurrent
		buildUploadJSON = previousJSON
		buildSkip = previousSkip
		buildDryRun = previousDryRun
		uploadAppFlag = previousApp
		uploadNameFlag = previousName
		uploadYesFlag = previousYes
		uploadSchemeFlag = previousScheme
		buildRequireConfiguredApp = previousRequireApp
	}()

	buildVersion = version
	buildSetCurr = setCurrent
	buildUploadJSON = jsonOutput
	buildSkip = false
	buildDryRun = false
	uploadAppFlag = ""
	uploadNameFlag = ""
	uploadYesFlag = false
	uploadSchemeFlag = ""
	buildRequireConfiguredApp = true

	return runConfigDrivenBuild(cmd, apiKey, platform)
}

func runConfigDrivenBuild(cmd *cobra.Command, apiKey, platform string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	cfg, err := config.LoadProjectConfig(configPath)
	if err != nil {
		printProjectNotInitialized()
		return err
	}

	if platform != "" {
		return runSinglePlatformBuild(cmd, cfg, configPath, apiKey, platform)
	}

	buildablePlatforms := buildablePlatformKeys(cfg)

	hasIOS := false
	hasAndroid := false
	for _, platform := range buildablePlatforms {
		if platform == "ios" {
			hasIOS = true
		}
		if platform == "android" {
			hasAndroid = true
		}
	}

	if hasIOS && hasAndroid {
		return runConcurrentBuilds(cmd, cfg, configPath, apiKey)
	}

	platformCount := len(buildablePlatforms)
	if platformCount == 0 {
		placeholderKeys := placeholderBuildPlatformKeys(cfg)
		if len(placeholderKeys) > 0 {
			ui.PrintError("Detected build platforms are present but not configured yet")
			ui.PrintInfo("Placeholder platforms: %s", strings.Join(placeholderKeys, ", "))
			ui.PrintInfo("Finish native setup or add build.platforms.<key>.command and build.platforms.<key>.output in .revyl/config.yaml")
		} else {
			ui.PrintError("No runnable build platforms configured")
			ui.PrintInfo("Configure build.platforms.<key>.command and build.platforms.<key>.output in .revyl/config.yaml")
		}
		return fmt.Errorf("no buildable platforms configured")
	}

	if platformCount == 1 {
		return runSinglePlatformBuild(cmd, cfg, configPath, apiKey, buildablePlatforms[0])
	}

	platforms := buildablePlatforms
	if ui.IsInteractive() {
		options := make([]ui.SelectOption, len(platforms))
		for i, p := range platforms {
			options[i] = ui.SelectOption{
				Label:       p,
				Value:       p,
				Description: cfg.Build.Platforms[p].Command,
			}
		}

		_, selected, err := ui.Select("Select platform to build:", options, 0)
		if err != nil {
			return fmt.Errorf("platform selection: %w", err)
		}
		return runSinglePlatformBuild(cmd, cfg, configPath, apiKey, selected)
	}

	ui.PrintWarning("Multiple platforms configured without --platform flag, using '%s'", platforms[0])
	ui.PrintInfo("Use --platform to specify which platform to build")
	return runSinglePlatformBuild(cmd, cfg, configPath, apiKey, platforms[0])
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
	if v, _ := cmd.Flags().GetBool("json"); v {
		buildUploadJSON = true
	}
	if v, _ := cmd.Root().PersistentFlags().GetBool("json"); v {
		buildUploadJSON = true
	}
	buildSetCurr = !buildNoSetCurrent
	if buildUploadJSON {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
	}

	// Check authentication
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	if err := validateUploadSourceFlags(uploadFileFlag, uploadURLFlag, uploadHeaderFlags); err != nil {
		return err
	}

	if uploadRemoteFlag {
		return deprecatedBuildUploadRemoteError()
	}
	if buildSkip || buildDryRun || uploadCleanFlag || uploadIncludeDirtyFlag {
		return deprecatedBuildUploadLocalBuildError()
	}

	// Direct file upload bypasses config-based build entirely.
	if uploadFileFlag != "" {
		return runDirectFileUpload(cmd, apiKey)
	}

	// URL-based upload: the backend fetches the artifact server-side.
	if uploadURLFlag != "" {
		return runURLUpload(cmd, apiKey)
	}

	return deprecatedBuildUploadLocalBuildError()
}

func deprecatedBuildRemoteError() error {
	return fmt.Errorf("`revyl build remote` has been replaced.\n\nUse:\n  revyl build --remote --platform ios\n  revyl build --remote --platform android\n  revyl build --remote --detach")
}

func deprecatedBuildUploadRemoteError() error {
	return fmt.Errorf("`revyl build upload --remote` has been replaced.\n\nUse:\n  revyl build --remote")
}

func deprecatedBuildUploadLocalBuildError() error {
	return fmt.Errorf("`revyl build upload` requires --file or --url.\n\nUse:\n  revyl build                         Build from source and upload\n  revyl build upload --file ./app.apk Upload an existing artifact\n  revyl build upload --url <url>      Ingest an artifact from a URL")
}

func missingConfiguredBuildAppError(platform string) error {
	return fmt.Errorf("no app is configured for platform %q; run revyl init or add build.platforms.%s.app_id to .revyl/config.yaml", platform, platform)
}

func checkLocalBuildSupported() error {
	if buildHostGOOS == "windows" {
		return fmt.Errorf("local builds are not supported on Windows; use `revyl build --remote` or upload an existing artifact with `revyl build upload --file` or `revyl build upload --url`")
	}
	return nil
}

// validateLocalBuildSecrets verifies that local builds can inherit every
// configured secret from the current process environment.
func validateLocalBuildSecrets(platformKey string, platformCfg config.BuildPlatform) error {
	secretRefs, err := mergeBuildSecretRefs(platformCfg.Secrets, buildSecretRefFlags)
	if err != nil {
		return fmt.Errorf("build.platforms.%s.secrets: %w", platformKey, err)
	}
	if err := validateBuildEnvSecretCollisions(platformCfg.Env, secretRefs); err != nil {
		return fmt.Errorf("build.platforms.%s: %w", platformKey, err)
	}

	var missing []string
	for _, name := range secretRefs {
		if _, exists := os.LookupEnv(name); !exists {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf(
		"local build secrets are not set in the process environment: %s; export them or source a gitignored .env.local before running revyl build",
		strings.Join(missing, ", "),
	)
}

// runDirectFileUpload uploads a user-supplied artifact without requiring a
// .revyl/config.yaml build configuration. Platform is inferred from the file
// extension when --platform is not set. A project config is loaded
// opportunistically for app_id fallback but is not required.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - apiKey: Authentication token for API requests
//
// Returns:
//   - error: Any error that occurred during upload
func runDirectFileUpload(cmd *cobra.Command, apiKey string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Resolve and validate the artifact path.
	artifactPath, err := build.ResolveArtifactPath(cwd, uploadFileFlag)
	if err != nil {
		ui.PrintError("File not found: %s", uploadFileFlag)
		return fmt.Errorf("file not found: %w", err)
	}

	info, err := os.Stat(artifactPath)
	if err != nil {
		ui.PrintError("Cannot read file: %s", artifactPath)
		return fmt.Errorf("cannot read file: %w", err)
	}
	if info.IsDir() && !build.IsAppBundle(artifactPath) {
		ui.PrintError("Path is a directory, not a build artifact: %s", artifactPath)
		return fmt.Errorf("path is a directory, not a build artifact")
	}

	// Determine target platform from --platform flag or file extension.
	devicePlatform := uploadPlatformFlag
	if devicePlatform == "" {
		devicePlatform = build.PlatformFromFilePath(artifactPath)
	}
	if normalized, normalizeErr := normalizeMobilePlatform(devicePlatform, ""); normalizeErr == nil {
		devicePlatform = normalized
	} else if uploadPlatformFlag != "" {
		ui.PrintError("Invalid platform %q (must be ios or android)", uploadPlatformFlag)
		return fmt.Errorf("invalid platform: %s", uploadPlatformFlag)
	} else {
		ui.PrintError("Cannot determine platform from file extension '%s'", filepath.Ext(artifactPath))
		ui.PrintInfo("Use --platform to specify the target platform (ios or android)")
		return fmt.Errorf("unable to infer platform from file path: %s", artifactPath)
	}

	// Handle dry-run before doing any real work.
	if buildDryRun {
		ui.PrintBanner(version)
		ui.PrintInfo("Dry-run mode — showing what would be uploaded:")
		ui.Println()
		ui.PrintInfo("  File:           %s", artifactPath)
		ui.PrintInfo("  Platform:       %s", devicePlatform)
		if uploadAppFlag != "" {
			ui.PrintInfo("  App:            %s", uploadAppFlag)
		}
		if buildVersion != "" {
			ui.PrintInfo("  Build Version:  %s", buildVersion)
		}
		ui.Println()
		if !buildUploadJSON {
			ui.PrintSuccess("Dry-run complete — no changes made")
		}
		return nil
	}

	ui.PrintBanner(version)
	ui.PrintInfo("Direct Upload (%s)", devicePlatform)
	ui.Println()

	// Create API client.
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Resolve app ID: --app flag (name or UUID) → config fallback → interactive prompt.
	appID := uploadAppFlag
	if appID != "" && !looksLikeUUID(appID) {
		resolvedID, _, resolveErr := resolveAppNameOrID(cmd, client, appID)
		if resolveErr != nil {
			ui.PrintError("Could not resolve app %q: %v", appID, resolveErr)
			return resolveErr
		}
		appID = resolvedID
	}

	if appID == "" {
		configPath := filepath.Join(cwd, ".revyl", "config.yaml")
		if cfg, cfgErr := config.LoadProjectConfig(configPath); cfgErr == nil {
			if platformKey := pickBestBuildPlatformKey(cfg, devicePlatform); platformKey != "" {
				appID = cfg.Build.Platforms[platformKey].AppID
			}
		}
	}

	if appID == "" {
		configPath := filepath.Join(cwd, ".revyl", "config.yaml")
		cfg, cfgErr := config.LoadProjectConfig(configPath)
		if cfgErr != nil {
			cfg = &config.ProjectConfig{}
			cfg.Build.Platforms = make(map[string]config.BuildPlatform)
		}
		selectedID, promptErr := selectOrCreateAppForPlatform(cmd, client, cfg, configPath, devicePlatform, devicePlatform)
		if promptErr != nil {
			return promptErr
		}
		appID = selectedID
	}

	// Generate version string.
	versionStr := buildVersion
	if versionStr == "" {
		versionStr = build.GenerateVersionStringForWorkDir(cwd)
	}

	ui.PrintInfo("Uploading: %s", filepath.Base(artifactPath))
	ui.PrintInfo("Build Version: %s", versionStr)

	// Post-process iOS artifacts (tar.gz → zip, .app → zip).
	if build.IsTarGz(artifactPath) {
		ui.Println()
		ui.StartSpinner("Extracting .app from tar.gz...")
		zipPath, extractErr := build.ExtractAppFromTarGz(artifactPath)
		ui.StopSpinner()
		if extractErr != nil {
			ui.PrintError("Failed to extract .app from tar.gz: %v", extractErr)
			return extractErr
		}
		defer os.Remove(zipPath)
		artifactPath = zipPath
		if !buildUploadJSON {
			ui.PrintSuccess("Converted to: %s", filepath.Base(zipPath))
		}
	} else if build.IsAppBundle(artifactPath) {
		ui.Println()
		ui.StartSpinner("Zipping .app bundle...")
		zipPath, zipErr := build.ZipAppBundle(artifactPath)
		ui.StopSpinner()
		if zipErr != nil {
			ui.PrintError("Failed to zip .app bundle: %v", zipErr)
			return zipErr
		}
		defer os.Remove(zipPath)
		artifactPath = zipPath
		if !buildUploadJSON {
			ui.PrintSuccess("Created: %s", filepath.Base(zipPath))
		}
	}

	// Collect metadata (no build command or duration for direct uploads).
	metadata := build.CollectMetadata(cwd, "", devicePlatform, 0)

	ui.Println()
	ui.StartSpinner("Uploading artifact...")

	result, uploadErr := client.UploadBuild(cmd.Context(), &api.UploadBuildRequest{
		AppID:        appID,
		Version:      versionStr,
		FilePath:     artifactPath,
		Metadata:     metadata,
		SetAsCurrent: buildSetCurr,
	})

	ui.StopSpinner()

	if uploadErr != nil {
		ui.PrintError("Upload failed: %v", uploadErr)
		return uploadErr
	}

	ui.Println()
	if !buildUploadJSON {
		ui.PrintSuccess("Upload complete!")
	}
	ui.PrintInfo("App:             %s", appID)
	ui.PrintInfo("Build Version:   %s", result.Version)
	ui.PrintInfo("Build ID:        %s", result.VersionID)
	if result.PackageID != "" {
		ui.PrintInfo("Package ID:      %s", result.PackageID)
	}
	for _, warning := range result.Warnings {
		ui.PrintWarning("%s", warning)
	}
	ui.Println()
	ui.PrintDim("To list builds: revyl build list --app %s", appID)

	if buildUploadJSON {
		outputBuildUploadJSON([]BuildUploadJSONBuild{
			newBuildUploadJSONBuild(
				devicePlatform,
				devicePlatform,
				appID,
				artifactPath,
				0,
				result,
			),
		})
	}

	return nil
}

// validateUploadSourceFlags checks mutual exclusion between --file, --url, and --header.
//
// Parameters:
//   - file: Value of --file flag
//   - urlFlag: Value of --url flag
//   - headers: Values of --header flags
//
// Returns:
//   - error: If the flags are in an invalid combination
func validateUploadSourceFlags(file, urlFlag string, headers []string) error {
	if file != "" && urlFlag != "" {
		return fmt.Errorf("--file and --url are mutually exclusive")
	}
	if len(headers) > 0 && urlFlag == "" {
		return fmt.Errorf("--header requires --url")
	}
	return nil
}

// parseHeaderFlags parses repeatable --header "Name: value" flags into a map.
// Returns an error if any header is malformed (missing colon separator).
//
// Parameters:
//   - flags: Raw flag values from --header
//
// Returns:
//   - map[string]string: Parsed header name-value pairs
//   - error: If any flag value is not in "Name: value" format
func parseHeaderFlags(flags []string) (map[string]string, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	headers := make(map[string]string, len(flags))
	for _, h := range flags {
		idx := strings.Index(h, ":")
		if idx < 1 {
			return nil, fmt.Errorf("invalid --header format %q: expected \"Name: value\"", h)
		}
		name := strings.TrimSpace(h[:idx])
		if name == "" {
			return nil, fmt.Errorf("invalid --header format %q: header name is empty", h)
		}
		value := strings.TrimSpace(h[idx+1:])
		headers[name] = value
	}
	return headers, nil
}

// runURLUpload uploads a build by asking the backend to fetch the artifact from
// a remote URL. The developer does not need the binary locally. Platform is
// inferred from the URL filename when --platform is not set.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - apiKey: Authentication token for API requests
//
// Returns:
//   - error: Any error that occurred during the upload
func runURLUpload(cmd *cobra.Command, apiKey string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Parse --header flags.
	headers, err := parseHeaderFlags(uploadHeaderFlags)
	if err != nil {
		return err
	}

	// Infer platform from the URL filename extension.
	urlBase := filepath.Base(strings.SplitN(uploadURLFlag, "?", 2)[0])
	devicePlatform := uploadPlatformFlag
	if devicePlatform == "" {
		devicePlatform = build.PlatformFromFilePath(urlBase)
	}
	if normalized, normalizeErr := normalizeMobilePlatform(devicePlatform, ""); normalizeErr == nil {
		devicePlatform = normalized
	} else if uploadPlatformFlag != "" {
		ui.PrintError("Invalid platform %q (must be ios or android)", uploadPlatformFlag)
		return fmt.Errorf("invalid platform: %s", uploadPlatformFlag)
	} else {
		ui.PrintError("Cannot determine platform from URL filename '%s'", urlBase)
		ui.PrintInfo("Use --platform to specify the target platform (ios or android)")
		return fmt.Errorf("unable to infer platform from URL: %s", uploadURLFlag)
	}

	// Handle dry-run.
	if buildDryRun {
		ui.PrintBanner(version)
		ui.PrintInfo("Dry-run mode — showing what would be uploaded:")
		ui.Println()
		ui.PrintInfo("  Source URL:      %s", uploadURLFlag)
		ui.PrintInfo("  Platform:        %s", devicePlatform)
		if len(headers) > 0 {
			ui.PrintInfo("  Custom Headers:  %d", len(headers))
		}
		if uploadAppFlag != "" {
			ui.PrintInfo("  App:             %s", uploadAppFlag)
		}
		if buildVersion != "" {
			ui.PrintInfo("  Build Version:   %s", buildVersion)
		}
		ui.Println()
		if !buildUploadJSON {
			ui.PrintSuccess("Dry-run complete — no changes made")
		}
		return nil
	}

	ui.PrintBanner(version)
	ui.PrintInfo("URL Upload (%s)", devicePlatform)
	ui.Println()

	// Create API client.
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Resolve app ID: --app flag (name or UUID) -> config fallback -> interactive prompt.
	appID := uploadAppFlag
	if appID != "" && !looksLikeUUID(appID) {
		resolvedID, _, resolveErr := resolveAppNameOrID(cmd, client, appID)
		if resolveErr != nil {
			ui.PrintError("Could not resolve app %q: %v", appID, resolveErr)
			return resolveErr
		}
		appID = resolvedID
	}

	if appID == "" {
		configPath := filepath.Join(cwd, ".revyl", "config.yaml")
		if cfg, cfgErr := config.LoadProjectConfig(configPath); cfgErr == nil {
			if platformKey := pickBestBuildPlatformKey(cfg, devicePlatform); platformKey != "" {
				appID = cfg.Build.Platforms[platformKey].AppID
			}
		}
	}

	if appID == "" {
		configPath := filepath.Join(cwd, ".revyl", "config.yaml")
		cfg, cfgErr := config.LoadProjectConfig(configPath)
		if cfgErr != nil {
			cfg = &config.ProjectConfig{}
			cfg.Build.Platforms = make(map[string]config.BuildPlatform)
		}
		selectedID, promptErr := selectOrCreateAppForPlatform(cmd, client, cfg, configPath, devicePlatform, devicePlatform)
		if promptErr != nil {
			return promptErr
		}
		appID = selectedID
	}

	// Generate version string.
	versionStr := buildVersion
	if versionStr == "" {
		versionStr = build.GenerateVersionStringForWorkDir(cwd)
	}

	ui.PrintInfo("Uploading from: %s", uploadURLFlag)
	ui.PrintInfo("Build Version:  %s", versionStr)

	// Collect metadata (no build command or duration for URL uploads).
	metadata := build.CollectMetadata(cwd, "", devicePlatform, 0)
	metadata["source"] = "cli_url_upload"

	ui.Println()
	ui.StartSpinner("Ingesting artifact from URL...")

	result, uploadErr := client.CreateBuildFromURL(cmd.Context(), &api.CreateBuildFromURLRequest{
		AppID:        appID,
		FromURL:      uploadURLFlag,
		Headers:      headers,
		Version:      versionStr,
		Metadata:     metadata,
		SetAsCurrent: buildSetCurr,
	})

	ui.StopSpinner()

	if uploadErr != nil {
		ui.PrintError("Upload failed: %v", uploadErr)
		return uploadErr
	}

	ui.Println()
	if !buildUploadJSON {
		if result.WasReused {
			ui.PrintSuccess("Version already exists (idempotent)")
		} else {
			ui.PrintSuccess("Upload complete!")
		}
	}
	ui.PrintInfo("App:             %s", appID)
	ui.PrintInfo("Build Version:   %s", result.Version)
	ui.PrintInfo("Build ID:        %s", result.ID)
	if result.PackageName != "" {
		ui.PrintInfo("Package ID:      %s", result.PackageName)
	}
	ui.Println()
	ui.PrintDim("To list builds: revyl build list --app %s", appID)

	if buildUploadJSON {
		jsonOutput := map[string]interface{}{
			"platform":     devicePlatform,
			"app_id":       appID,
			"version":      result.Version,
			"version_id":   result.ID,
			"package_name": result.PackageName,
			"was_reused":   result.WasReused,
			"source_url":   uploadURLFlag,
		}
		data, _ := json.MarshalIndent(jsonOutput, "", "  ")
		fmt.Println(string(data))
	}

	return nil
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
	apps, err := client.ListAllApps(cmd.Context(), platform, 100)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to fetch apps: %v", err)
		return "", err
	}

	var appID string

	// If no existing apps, skip selection and create directly
	if len(apps) == 0 {
		ui.PrintInfo("No existing apps found. Let's create one.")
		ui.Println()
		appID, err = createNewApp(cmd, client, cfg, platform)
		if err != nil {
			return "", err
		}
	} else {
		options := []string{"Create new app"}
		for _, app := range apps {
			options = append(options, fmt.Sprintf("%s (%s)", app.Name, app.Platform))
		}

		// Show selection prompt
		ui.PrintInfo("Select an app to upload to:")
		selection, err := ui.PromptSelect("", options)
		if err != nil {
			return "", err
		}

		if selection == 0 {
			appID, err = createNewApp(cmd, client, cfg, platform)
			if err != nil {
				return "", err
			}
		} else {
			appID = apps[selection-1].ID
			ui.PrintSuccess("Selected: %s", apps[selection-1].Name)
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

	result, err := createOrLinkAppByName(cmd.Context(), client, name, platform)

	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to create app: %v", err)
		return "", err
	}

	if result.LinkedExisting {
		ui.PrintSuccess("Linked existing app: %s (%s)", result.Name, result.ID)
	} else {
		ui.PrintSuccess("Created: %s (%s)", result.Name, result.ID)
	}

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

// BuildUploadJSONBuild represents one uploaded build in machine-readable output.
type BuildUploadJSONBuild struct {
	PlatformKey          string   `json:"platform_key"`
	Platform             string   `json:"platform"`
	AppID                string   `json:"app_id"`
	BuildVersion         string   `json:"build_version"`
	BuildID              string   `json:"build_id"`
	ArtifactPath         string   `json:"artifact_path"`
	BuildDurationSeconds float64  `json:"build_duration_seconds,omitempty"`
	PackageID            string   `json:"package_id,omitempty"`
	Warnings             []string `json:"warnings,omitempty"`
}

// BuildUploadJSONOutput is the machine-readable payload for build uploads.
type BuildUploadJSONOutput struct {
	Success bool                   `json:"success"`
	Count   int                    `json:"count"`
	Build   *BuildUploadJSONBuild  `json:"build,omitempty"`
	Builds  []BuildUploadJSONBuild `json:"builds"`
}

func newBuildUploadJSONBuild(
	platformKey string,
	platform string,
	appID string,
	artifactPath string,
	buildDuration time.Duration,
	uploadResult *api.UploadBuildResponse,
) BuildUploadJSONBuild {
	build := BuildUploadJSONBuild{
		PlatformKey:  platformKey,
		Platform:     platform,
		AppID:        appID,
		ArtifactPath: artifactPath,
	}
	if uploadResult != nil {
		build.BuildVersion = uploadResult.Version
		build.BuildID = uploadResult.VersionID
		build.PackageID = uploadResult.PackageID
		build.Warnings = uploadResult.Warnings
	}
	if buildDuration > 0 {
		build.BuildDurationSeconds = buildDuration.Seconds()
	}
	return build
}

func outputBuildUploadJSON(builds []BuildUploadJSONBuild) {
	sort.Slice(builds, func(i, j int) bool {
		return builds[i].PlatformKey < builds[j].PlatformKey
	})

	output := BuildUploadJSONOutput{
		Success: true,
		Count:   len(builds),
		Builds:  builds,
	}
	if len(builds) == 1 {
		output.Build = &builds[0]
	}

	data, _ := json.MarshalIndent(output, "", "  ")
	fmt.Println(string(data))
}

func buildPlatformUsesCommandList(platformCfg config.BuildPlatform) bool {
	for _, command := range platformCfg.Commands {
		if strings.TrimSpace(command) != "" {
			return true
		}
	}
	return false
}

func setBuildPlatformCommands(platformCfg config.BuildPlatform, commands []string, useCommandList bool) config.BuildPlatform {
	trimmed := make([]string, 0, len(commands))
	for _, command := range commands {
		if command = strings.TrimSpace(command); command != "" {
			trimmed = append(trimmed, command)
		}
	}

	if useCommandList {
		platformCfg.Commands = trimmed
		if len(trimmed) == 1 {
			platformCfg.Command = trimmed[0]
		}
		return platformCfg
	}

	platformCfg.Commands = nil
	if len(trimmed) == 1 {
		platformCfg.Command = trimmed[0]
	} else {
		platformCfg.Command = strings.Join(trimmed, " && ")
	}
	return platformCfg
}

func normalizeExpoBuildCommands(system string, commands []string) ([]string, bool) {
	normalized := append([]string(nil), commands...)
	changed := false
	for i, command := range normalized {
		if next, didChange := normalizeExpoBuildCommand(system, command); didChange {
			normalized[i] = next
			changed = true
		}
	}
	return normalized, changed
}

func applyFixedBuildCommandToCommands(commands []string, originalJoined, fixedJoined string) []string {
	if originalProfile, usesListProfile := build.ExtractProfileFromCommand(originalJoined); usesListProfile {
		if fixedProfile, ok := build.ExtractProfileFromCommand(fixedJoined); ok {
			fixed := append([]string(nil), commands...)
			for i, command := range fixed {
				if commandProfile, ok := build.ExtractProfileFromCommand(command); ok && commandProfile == originalProfile {
					fixed[i] = build.ReplaceProfileInCommand(command, fixedProfile)
				}
			}
			return fixed
		}
	}
	return []string{fixedJoined}
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
				versionStr = build.GenerateVersionStringForWorkDir(cwd)
			}
			versionStr = fmt.Sprintf("%s-%s", versionStr, platform)

			ui.PrintInfo("[%s]", platform)
			ui.PrintInfo("  Configured Build Command:  %s", platformCfg.Command)
			ui.PrintInfo("  Configured Artifact Path:  %s", platformCfg.Output)
			ui.PrintInfo("  Build Version:  %s", versionStr)
			if platformCfg.AppID != "" {
				ui.PrintInfo("  App ID:         %s", platformCfg.AppID)
			} else {
				ui.PrintInfo("  App ID:         (not configured)")
			}
			ui.PrintInfo("  Set Current:    %v", buildSetCurr)
			ui.Println()
		}

		if !buildUploadJSON {
			ui.PrintSuccess("Dry-run complete - no changes made")
		}
		return nil
	}

	if !buildSkip && isExpoBuildSystem(cfg.Build.System) {
		changed, err := ensureExpoDevClientSchemeForBuild(cwd, cfg)
		if err != nil {
			printExpoSchemePreflightError(err)
			return err
		}
		if changed {
			if err := config.WriteProjectConfig(configPath, cfg); err != nil {
				ui.PrintWarning("Failed to save Expo scheme to config: %v", err)
			}
		}
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
			if buildRequireConfiguredApp {
				return missingConfiguredBuildAppError(platform)
			}
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
		if !buildUploadJSON {
			ui.PrintSuccess("Saved app IDs to .revyl/config.yaml")
		}
	}

	ui.PrintBanner(version)
	ui.PrintInfo("Building iOS and Android concurrently...")
	ui.Println()

	if !buildSkip && isExpoBuildSystem(cfg.Build.System) {
		if ready := ensureExpoEASAuth(cwd); !ready {
			return formatEASLoginRequiredError()
		}
	}

	// Channel to collect results
	results := make(chan BuildResult, len(platforms))
	var wg sync.WaitGroup

	// Mutex for synchronized output and shared config access in workers
	var outputMu sync.Mutex

	// Start concurrent builds
	for _, platform := range platforms {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			result := buildAndUploadPlatform(cmd, cfg, cwd, client, p, &outputMu, len(platforms) > 1)
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
	successfulBuilds := make([]BuildUploadJSONBuild, 0, len(platforms))
	for result := range results {
		if result.Error != nil {
			ui.PrintError("[%s] Failed: %v", result.Platform, result.Error)

			if toolErr, ok := result.Error.(*build.BuildToolError); ok {
				ui.Println()
				ui.PrintWarning("How to fix:")
				ui.Println()
				for _, line := range strings.Split(toolErr.Guidance, "\n") {
					ui.PrintDim("  %s", line)
				}
				ui.Println()
			}

			errors = append(errors, fmt.Errorf("%s: %w", result.Platform, result.Error))
		} else {
			successfulBuilds = append(successfulBuilds, newBuildUploadJSONBuild(
				result.Platform,
				result.Platform,
				result.AppID,
				result.ArtifactPath,
				result.Duration,
				result.UploadResult,
			))
			if !buildUploadJSON {
				ui.PrintSuccess("[%s] Upload complete!", result.Platform)
			}
			ui.PrintInfo("  App:             %s", result.AppID)
			ui.PrintInfo("  Build Version:   %s", result.UploadResult.Version)
			ui.PrintInfo("  Build ID:        %s", result.UploadResult.VersionID)
			if result.UploadResult.PackageID != "" {
				ui.PrintInfo("  Package ID:      %s", result.UploadResult.PackageID)
			}
			for _, warning := range result.UploadResult.Warnings {
				ui.PrintWarning("  %s", warning)
			}
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("%d platform(s) failed", len(errors))
	}

	if buildUploadJSON {
		outputBuildUploadJSON(successfulBuilds)
		return nil
	}

	// Suggest running a test after successful concurrent builds
	testsDir := filepath.Join(cwd, ".revyl", "tests")
	if aliases := config.ListLocalTestAliases(testsDir); len(aliases) > 0 {
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Run a test:", Command: fmt.Sprintf("revyl test run %s", aliases[0])},
		})
	} else {
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Create a test:", Command: "revyl test create <name>"},
		})
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
//   - outputMu: Mutex for synchronized output and shared config access
//
// Returns:
//   - BuildResult: The result of the build and upload
func buildAndUploadPlatform(cmd *cobra.Command, cfg *config.ProjectConfig, cwd string, client *api.Client, platform string, outputMu *sync.Mutex, concurrent bool) BuildResult {
	result := BuildResult{Platform: platform}

	outputMu.Lock()
	platformCfg := cfg.Build.Platforms[platform]
	outputMu.Unlock()
	if err := validateLocalBuildSecrets(platform, platformCfg); err != nil {
		result.Error = err
		return result
	}
	usesCommandList := buildPlatformUsesCommandList(platformCfg)
	buildCommands := platformCfg.BuildCommands()
	if normalized, changed := normalizeExpoBuildCommands(cfg.Build.System, buildCommands); changed {
		buildCommands = normalized
		platformCfg = setBuildPlatformCommands(platformCfg, buildCommands, usesCommandList)
		outputMu.Lock()
		cfg.Build.Platforms[platform] = platformCfg
		ui.PrintDim("[%s] Updated build command to use npx eas", platform)
		outputMu.Unlock()
	}
	buildCommand := strings.Join(buildCommands, " && ")

	// Apply Xcode scheme: --scheme flag > config scheme > leave as-is
	scheme := uploadSchemeFlag
	if scheme == "" {
		scheme = platformCfg.Scheme
	}
	if scheme != "" {
		for i, command := range buildCommands {
			buildCommands[i] = build.ApplySchemeToCommand(command, scheme)
		}
		buildCommand = strings.Join(buildCommands, " && ")
	}

	// Validate EAS simulator profile for iOS builds (non-interactive in concurrent mode)
	if !buildSkip && isExpoBuildSystem(cfg.Build.System) && build.IsIOSPlatformKey(platform) {
		easCfg, easErr := build.LoadEASConfig(cwd)
		if easErr == nil && easCfg != nil {
			vResult := build.ValidateEASSimulatorProfile(easCfg, buildCommand)
			if !vResult.Valid && !vResult.NoEASConfig && !vResult.ProfileNotFound {
				outputMu.Lock()
				ui.PrintWarning("[%s] EAS profile %q does not produce a simulator build.", platform, vResult.ProfileName)
				ui.PrintDim("  Revyl cloud devices are iOS simulators. The resulting artifact may not work.")
				if len(vResult.Alternatives) > 0 {
					ui.PrintInfo("  Compatible profiles: %s", strings.Join(vResult.Alternatives, ", "))
					ui.PrintInfo("  Update your config: revyl init --force")
				} else {
					ui.PrintDim("  %s", build.SimulatorFixSnippet(vResult.ProfileName))
				}
				outputMu.Unlock()
			}
		}
	}

	// Build
	if !buildSkip {
		outputMu.Lock()
		ui.PrintDim("[%s] Using configured build command from .revyl/config.yaml", platform)
		if len(buildCommands) == 1 {
			ui.PrintInfo("[%s] Building with configured command: %s", platform, buildCommands[0])
		} else {
			ui.PrintInfo("[%s] Building with %d configured commands", platform, len(buildCommands))
		}
		ui.PrintDim("[%s] Local build step: Revyl will upload after this command creates %s.", platform, platformCfg.Output)
		outputMu.Unlock()

		startTime := time.Now()
		runner := build.NewRunner(cwd)
		runner.Interactive = !concurrent

		var err error
		for i, command := range buildCommands {
			if len(buildCommands) > 1 {
				outputMu.Lock()
				ui.PrintDim("[%s] Build command %d/%d: %s", platform, i+1, len(buildCommands), command)
				outputMu.Unlock()
			}
			err = runner.Run(command, func(line string) {
				outputMu.Lock()
				ui.PrintDim("  [%s] %s", platform, line)
				outputMu.Unlock()
			})
			if err != nil {
				break
			}
		}

		result.Duration = time.Since(startTime)

		if err != nil {
			if _, ok := err.(*build.BuildToolError); ok {
				result.Error = err
			} else {
				result.Error = fmt.Errorf("build failed: %w", err)
			}
			return result
		}

		outputMu.Lock()
		if !buildUploadJSON {
			ui.PrintSuccess("[%s] Build completed in %s", platform, result.Duration.Round(time.Second))
		}
		outputMu.Unlock()
	} else {
		outputMu.Lock()
		ui.PrintInfo("[%s] Skipping build step", platform)
		outputMu.Unlock()
	}

	// Resolve artifact path
	outputMu.Lock()
	ui.PrintDim("[%s] Resolving configured artifact path from .revyl/config.yaml: %s", platform, platformCfg.Output)
	outputMu.Unlock()
	artifactPath, err := build.ResolveArtifactPath(cwd, platformCfg.Output)
	if err != nil {
		result.Error = fmt.Errorf("configured artifact path not found: %w", err)
		return result
	}
	result.ArtifactPath = artifactPath

	// Get app ID from platform config
	appID := platformCfg.AppID
	if buildRequireConfiguredApp && strings.TrimSpace(appID) == "" {
		result.Error = missingConfiguredBuildAppError(platform)
		return result
	}
	result.AppID = appID

	// Generate version string with platform suffix
	versionStr := buildVersion
	if versionStr == "" {
		versionStr = build.GenerateVersionStringForWorkDir(cwd)
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
		if !buildUploadJSON {
			ui.PrintSuccess("[%s] Converted to: %s", platform, filepath.Base(zipPath))
		}
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
		if !buildUploadJSON {
			ui.PrintSuccess("[%s] Created: %s", platform, filepath.Base(zipPath))
		}
		outputMu.Unlock()
	}

	// Collect metadata
	metadata := build.CollectMetadata(cwd, buildCommand, platform, result.Duration)

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
	apps, err := client.ListAllApps(cmd.Context(), platform, 100)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to fetch apps: %v", err)
		return "", err
	}

	var appID string

	// If no existing apps for this platform, create directly
	if len(apps) == 0 {
		ui.PrintInfo("No existing %s apps found. Creating one...", platform)
		appID, err = createNewAppForPlatform(cmd, client, cfg, platform)
		if err != nil {
			return "", err
		}
	} else {
		options := []string{fmt.Sprintf("Create new %s app", platform)}
		for _, app := range apps {
			options = append(options, fmt.Sprintf("%s (%s)", app.Name, app.Platform))
		}

		// Show selection prompt
		ui.PrintInfo("Select an app for %s:", platform)
		selection, err := ui.PromptSelect("", options)
		if err != nil {
			return "", err
		}

		if selection == 0 {
			appID, err = createNewAppForPlatform(cmd, client, cfg, platform)
			if err != nil {
				return "", err
			}
		} else {
			appID = apps[selection-1].ID
			ui.PrintSuccess("Selected: %s", apps[selection-1].Name)
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

	result, err := createOrLinkAppByName(cmd.Context(), client, name, platform)

	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to create app: %v", err)
		return "", err
	}

	if result.LinkedExisting {
		ui.PrintSuccess("Linked existing app: %s (%s)", result.Name, result.ID)
	} else {
		ui.PrintSuccess("Created: %s (%s)", result.Name, result.ID)
	}

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
	if err := checkLocalBuildSupported(); err != nil {
		return err
	}

	resolvedPlatform, err := resolveBuildUploadPlatform(cfg, platform)
	if err != nil {
		if setupErr, ok := asBuildPlatformNeedsSetupError(err); ok {
			ui.PrintError("Build platform %s is not ready yet", setupErr.PlatformKey)
			ui.PrintInfo(
				"Finish native setup or add build.platforms.%s.command and build.platforms.%s.output in .revyl/config.yaml",
				setupErr.PlatformKey,
				setupErr.PlatformKey,
			)
			return err
		}
		ui.PrintError("Unknown platform: %s", platform)
		ui.PrintInfo("Available platforms: %v", availableBuildPlatformKeys(cfg))
		return err
	}

	platformKey := resolvedPlatform.PlatformKey
	devicePlatform := resolvedPlatform.DevicePlatform
	platformCfg := resolvedPlatform.Config
	if err := validateLocalBuildSecrets(platformKey, platformCfg); err != nil {
		return err
	}

	// Validate platform configuration
	if platformCfg.Output == "" {
		ui.PrintError("Artifact path not configured for %s", platformKey)
		ui.PrintInfo("Please configure build.platforms.%s.output in .revyl/config.yaml (artifact path)", platformKey)
		return fmt.Errorf("incomplete build config: missing output for %s", platformKey)
	}
	buildCommands := platformCfg.BuildCommands()
	if len(buildCommands) == 0 && !buildSkip {
		ui.PrintError("Build command not configured for %s", platformKey)
		ui.PrintInfo("Please configure build.platforms.%s.command or build.platforms.%s.commands in .revyl/config.yaml, or use revyl build upload --file to upload an existing artifact", platformKey, platformKey)
		return fmt.Errorf("incomplete build config: missing command for %s", platformKey)
	}
	usesCommandList := buildPlatformUsesCommandList(platformCfg)
	if normalized, changed := normalizeExpoBuildCommands(cfg.Build.System, buildCommands); changed {
		buildCommands = normalized
		platformCfg = setBuildPlatformCommands(platformCfg, buildCommands, usesCommandList)
		if !resolvedPlatform.LegacyConfig {
			cfg.Build.Platforms[platformKey] = platformCfg
			_ = config.WriteProjectConfig(configPath, cfg)
		}
		ui.PrintDim("Updated build.platforms.%s.command to use npx eas", platformKey)
	}
	buildCommand := strings.Join(buildCommands, " && ")

	// Apply Xcode scheme: --scheme flag > config scheme > leave as-is
	scheme := uploadSchemeFlag
	if scheme == "" {
		scheme = platformCfg.Scheme
	}
	if scheme != "" {
		for i, command := range buildCommands {
			buildCommands[i] = build.ApplySchemeToCommand(command, scheme)
		}
		buildCommand = strings.Join(buildCommands, " && ")
	}

	// Validate EAS simulator profile for iOS builds (before dry-run/build)
	if isExpoBuildSystem(cfg.Build.System) && build.IsIOSPlatformKey(devicePlatform) {
		fixedCmd, err := validateEASSimulatorBuild(cwd, buildCommand)
		if err != nil {
			return err
		}
		if fixedCmd != buildCommand {
			buildCommands = applyFixedBuildCommandToCommands(buildCommands, buildCommand, fixedCmd)
			buildCommand = strings.Join(buildCommands, " && ")
			platformCfg = setBuildPlatformCommands(platformCfg, buildCommands, usesCommandList)
			if !resolvedPlatform.LegacyConfig {
				cfg.Build.Platforms[platformKey] = platformCfg
				_ = config.WriteProjectConfig(configPath, cfg)
			}
		}
	}

	ui.PrintBanner(version)
	ui.PrintInfo("Build and Upload (%s)", platformKey)
	if devicePlatform != "" && devicePlatform != platformKey {
		ui.PrintDim("Target device platform: %s", devicePlatform)
	}
	ui.Println()

	// Handle dry-run mode before starting the build
	if buildDryRun {
		ui.PrintInfo("Dry-run mode - showing what would be built and uploaded:")
		ui.Println()
		ui.PrintInfo("  Platform Key:   %s", platformKey)
		if devicePlatform != "" {
			ui.PrintInfo("  Platform:       %s", devicePlatform)
		}
		ui.PrintInfo("  Configured Build Command:  %s", buildCommand)
		if scheme != "" && strings.Contains(platformCfg.Command, "-scheme") {
			ui.PrintInfo("  Scheme:         %s", scheme)
		}
		ui.PrintInfo("  Configured Artifact Path:  %s", platformCfg.Output)
		if platformCfg.AppID != "" {
			ui.PrintInfo("  App ID:         %s", platformCfg.AppID)
		}
		if buildVersion != "" {
			ui.PrintInfo("  Build Version:  %s", buildVersion)
		}
		ui.Println()
		if !buildUploadJSON {
			ui.PrintSuccess("Dry-run complete - no changes made")
		}
		return nil
	}

	if !buildSkip && isExpoBuildSystem(cfg.Build.System) {
		changed, err := ensureExpoDevClientSchemeForBuild(cwd, cfg)
		if err != nil {
			printExpoSchemePreflightError(err)
			return err
		}
		if changed {
			if !resolvedPlatform.LegacyConfig {
				cfg.Build.Platforms[platformKey] = platformCfg
			}
			if err := config.WriteProjectConfig(configPath, cfg); err != nil {
				ui.PrintWarning("Failed to save Expo scheme to config: %v", err)
			}
		}
	}

	// Create API client early so we can validate org before building.
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Fail fast on org mismatch before starting a multi-minute build.
	if configOrgID := strings.TrimSpace(cfg.Project.OrgID); configOrgID != "" {
		if info, valErr := client.ValidateAPIKey(cmd.Context()); valErr == nil && info.OrgID != "" {
			if info.OrgID != configOrgID {
				ui.PrintError("Org mismatch: authenticated as %s (org %s) but config is bound to org %s",
					info.Email, info.OrgID, configOrgID)
				if os.Getenv("REVYL_API_KEY") != "" {
					ui.PrintInfo("Run 'revyl auth login' to switch accounts, or unset REVYL_API_KEY.")
				} else {
					ui.PrintInfo("Run 'revyl auth login' to switch orgs, or 'revyl init --force' to rebind.")
				}
				return fmt.Errorf("org mismatch: authenticated org %s != config org %s", info.OrgID, configOrgID)
			}
		}
	}

	var buildDuration time.Duration

	if !buildSkip && isExpoBuildSystem(cfg.Build.System) {
		if ready := ensureExpoEASAuth(cwd); !ready {
			return formatEASLoginRequiredError()
		}
	}

	// Run build if not skipped
	if !buildSkip {
		ui.PrintDim("Using configured build command from .revyl/config.yaml")
		if len(buildCommands) == 1 {
			ui.PrintInfo("Building with configured command: %s", buildCommands[0])
		} else {
			ui.PrintInfo("Building with %d configured commands", len(buildCommands))
		}
		ui.PrintDim("Local build step: Revyl will upload after this command creates %s.", platformCfg.Output)
		ui.PrintDim("If this sits quietly, rerun with --debug for raw EAS/Xcode output.")
		ui.PrintDim("Already have the artifact? Use revyl build upload --file to upload without rebuilding.")
		ui.Println()

		runner := build.NewRunner(cwd)
		runner.Interactive = true
		runner.FilterOutput = !ui.IsDebugMode()

		progress := RunBuildCommandsWithProgress(runner, buildCommands, platformKey, 10*time.Second)
		buildDuration = progress.Duration

		if progress.Err != nil {
			ui.Println()
			ui.PrintError("Build failed: %v", progress.Err)

			if toolErr, ok := progress.Err.(*build.BuildToolError); ok {
				ui.Println()
				ui.PrintWarning("How to fix:")
				ui.Println()
				for _, line := range strings.Split(toolErr.Guidance, "\n") {
					ui.PrintDim("  %s", line)
				}
			}

			return progress.Err
		}

		ui.Println()
		if !buildUploadJSON {
			ui.PrintSuccess("Build completed in %s", buildDuration.Round(time.Second))
		}
	} else {
		ui.PrintInfo("Skipping build step")
	}

	ui.PrintDim("Resolving configured artifact path from .revyl/config.yaml: %s", platformCfg.Output)

	// Check artifact exists
	artifactPath, err := build.ResolveArtifactPath(cwd, platformCfg.Output)
	if err != nil {
		ui.PrintError("Configured artifact path not found: %s", platformCfg.Output)
		return fmt.Errorf("artifact not found: %w", err)
	}

	// Determine app ID: --app flag (name or UUID) → platform config → interactive prompt.
	appID := uploadAppFlag
	if appID != "" && !looksLikeUUID(appID) {
		resolvedID, _, resolveErr := resolveAppNameOrID(cmd, client, appID)
		if resolveErr != nil {
			ui.PrintError("Could not resolve app %q: %v", appID, resolveErr)
			return resolveErr
		}
		appID = resolvedID
	}
	if appID == "" {
		appID = platformCfg.AppID
	}

	if buildRequireConfiguredApp && strings.TrimSpace(appID) == "" {
		err := missingConfiguredBuildAppError(platformKey)
		ui.PrintError("No app is configured for platform %q.", platformKey)
		ui.PrintInfo("Run: revyl init")
		ui.PrintInfo("Or add build.platforms.%s.app_id to .revyl/config.yaml.", platformKey)
		return err
	}

	if appID == "" {
		selectedID, err := selectOrCreateAppForPlatform(cmd, client, cfg, configPath, platformKey, devicePlatform)
		if err != nil {
			return err
		}
		appID = selectedID
	}

	// Generate version string if not provided
	versionStr := buildVersion
	if versionStr == "" {
		versionStr = build.GenerateVersionStringForWorkDir(cwd)
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
		if !buildUploadJSON {
			ui.PrintSuccess("Converted to: %s", filepath.Base(zipPath))
		}
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
		if !buildUploadJSON {
			ui.PrintSuccess("Created: %s", filepath.Base(zipPath))
		}
	}

	// Collect metadata
	metadata := build.CollectMetadata(cwd, buildCommand, devicePlatform, buildDuration)

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
		if os.Getenv("REVYL_API_KEY") != "" {
			ui.PrintDim("Note: REVYL_API_KEY env var is set and overrides stored credentials.")
		}
		return err
	}

	ui.Println()
	if !buildUploadJSON {
		ui.PrintSuccess("Upload complete!")
	}
	ui.PrintInfo("App:             %s", appID)
	ui.PrintInfo("Build Version:   %s", result.Version)
	ui.PrintInfo("Build ID:        %s", result.VersionID)
	if result.PackageID != "" {
		ui.PrintInfo("Package ID:      %s", result.PackageID)
	}
	for _, warning := range result.Warnings {
		ui.PrintWarning("%s", warning)
	}
	ui.Println()
	ui.PrintDim("To list builds: revyl build list --app %s", appID)

	if buildUploadJSON {
		outputBuildUploadJSON([]BuildUploadJSONBuild{
			newBuildUploadJSONBuild(
				platformKey,
				devicePlatform,
				appID,
				artifactPath,
				buildDuration,
				result,
			),
		})
		return nil
	}

	// Suggest running a test if local tests exist
	testsDir := filepath.Join(cwd, ".revyl", "tests")
	if aliases := config.ListLocalTestAliases(testsDir); len(aliases) > 0 {
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Run a test:", Command: fmt.Sprintf("revyl test run %s", aliases[0])},
		})
	} else {
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Create a test:", Command: "revyl test create <name>"},
		})
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

	// Determine app from flag (name or UUID) or show org apps.
	appID := appIDFlag
	if appID != "" {
		if !looksLikeUUID(appID) {
			resolvedID, _, resolveErr := resolveAppNameOrID(cmd, client, appID)
			if resolveErr != nil {
				ui.PrintError("Could not resolve app %q: %v", appID, resolveErr)
				return resolveErr
			}
			appID = resolvedID
		}
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

	// Resolve --branch flag (HEAD = current git branch)
	branchFilter := strings.TrimSpace(buildListBranch)
	if strings.EqualFold(branchFilter, "HEAD") {
		cwd, _ := os.Getwd()
		branchFilter = buildselection.CurrentBranch(cwd)
	}

	// Filter by branch when requested
	if branchFilter != "" {
		filtered := make([]api.BuildVersion, 0, len(versions))
		for _, v := range versions {
			if buildselection.ExtractBranch(v.Metadata) == branchFilter {
				filtered = append(filtered, v)
			}
		}
		versions = filtered
	}

	if jsonOutput {
		output := map[string]interface{}{
			"app_id":   appID,
			"versions": versions,
			"count":    len(versions),
		}
		if branchFilter != "" {
			output["branch_filter"] = branchFilter
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(versions) == 0 {
		if branchFilter != "" {
			ui.PrintInfo("No builds found for branch %q", branchFilter)
			ui.PrintDim("Build one with: revyl build --platform <key>")
		} else {
			ui.PrintInfo("No builds found")
		}
		return nil
	}

	ui.Println()
	if branchFilter != "" {
		ui.PrintInfo("Builds (branch: %s):", branchFilter)
	} else {
		ui.PrintInfo("Builds:")
	}
	ui.Println()

	// Create table with dynamic column widths
	table := ui.NewTable("VERSION", "BUILD ID", "UPLOADED", "BRANCH", "COMMIT", "PACKAGE ID", "CURRENT")
	table.SetMinWidth(0, 10) // VERSION
	table.SetMinWidth(1, 36) // BUILD ID - UUIDs are 36 chars
	table.SetMinWidth(2, 12) // UPLOADED

	for _, v := range versions {
		current := ""
		if v.IsCurrent {
			current = "✓"
		}
		branch := buildselection.ExtractBranch(v.Metadata)
		if branch == "" {
			branch = "-"
		}
		commit := extractBuildCommit(v.Metadata)
		if commit == "" {
			commit = "-"
		}
		table.AddRow(v.Version, v.ID, v.UploadedAt, branch, commit, v.PackageID, current)
	}

	table.Render()

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Build and upload:", Command: "revyl build"},
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
	apps, err := client.ListAllApps(cmd.Context(), buildPlatform, 100)
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
			"total": len(apps),
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(apps) == 0 {
		ui.PrintInfo("No apps found in your organization")
		ui.PrintInfo("Create apps at https://app.revyl.ai")
		return nil
	}

	ui.Println()
	ui.PrintInfo("Apps in your organization (%d total):", len(apps))
	ui.Println()

	// Create table with dynamic column widths
	table := ui.NewTable("NAME", "PLATFORM", "BUILDS", "LATEST", "APP ID")
	table.SetMinWidth(0, 20) // NAME - ensure readable width
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

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "List builds for an app:", Command: "revyl build list --app <id>"},
		{Label: "Build and upload:", Command: "revyl build"},
	})

	return nil
}

// validateEASSimulatorBuild checks that the EAS build command targets a simulator profile.
// Returns the (possibly updated) build command and any error.
// If a compatible profile exists, offers to switch. Otherwise, auto-creates a "revyl-build"
// profile in eas.json.
func validateEASSimulatorBuild(cwd, buildCommand string) (string, error) {
	easCfg, err := build.LoadEASConfig(cwd)
	if err != nil {
		ui.PrintWarning("Could not read eas.json: %v (skipping simulator check)", err)
		return buildCommand, nil
	}
	if easCfg == nil {
		return buildCommand, nil
	}

	result := build.ValidateEASSimulatorProfile(easCfg, buildCommand)
	if result.Valid || result.NoEASConfig || result.ProfileNotFound {
		return buildCommand, nil
	}

	// Profile doesn't produce a simulator build
	ui.Println()
	ui.PrintWarning("Profile %q is not a simulator build (Revyl requires simulator builds for iOS).", result.ProfileName)
	ui.Println()

	// Interactive mode: offer to fix
	if canPromptForEASLogin() {
		if len(result.Alternatives) > 0 {
			// Offer to switch to an existing simulator profile
			switchProfile, err := ui.PromptConfirm(fmt.Sprintf("Switch to %q?", result.Alternatives[0]), true)
			if err != nil {
				return buildCommand, nil
			}
			if switchProfile {
				newCmd := build.ReplaceProfileInCommand(buildCommand, result.Alternatives[0])
				ui.PrintSuccess("Switched to profile %q", result.Alternatives[0])
				return newCmd, nil
			}
			return buildCommand, fmt.Errorf("build cancelled: profile %q is not a simulator build", result.ProfileName)
		}

		// No existing alternatives — auto-create revyl-build profile
		ui.PrintInfo("Adding \"revyl-build\" simulator profile to eas.json...")
		if err := build.AddRevylBuildProfile(cwd, result.ProfileName); err != nil {
			ui.PrintError("Failed to update eas.json: %v", err)
			return buildCommand, fmt.Errorf("could not add revyl-build profile: %w", err)
		}
		newCmd := build.ReplaceProfileInCommand(buildCommand, "revyl-build")
		ui.PrintSuccess("Added \"revyl-build\" profile to eas.json (extends %q with simulator: true)", result.ProfileName)
		return newCmd, nil
	}

	// Non-interactive / CI: auto-create revyl-build profile
	if err := build.AddRevylBuildProfile(cwd, result.ProfileName); err != nil {
		return buildCommand, fmt.Errorf("profile %q is not a simulator build and failed to auto-fix eas.json: %w",
			result.ProfileName, err)
	}
	newCmd := build.ReplaceProfileInCommand(buildCommand, "revyl-build")
	ui.PrintInfo("Added \"revyl-build\" simulator profile to eas.json (extends %q)", result.ProfileName)
	return newCmd, nil
}

type buildUploadPlatformResolution struct {
	PlatformKey    string
	DevicePlatform string
	Config         config.BuildPlatform
	LegacyConfig   bool
}

// resolveBuildUploadPlatform resolves user input to a concrete build.platforms key
// and a canonical device platform (ios/android) used for API app lookups.
//
// Accepted input:
//   - build.platforms key (e.g. ios-dev)
//   - mobile platform alias (ios/android), mapped to best matching key
//   - legacy flat build config (no build.platforms) with ios/android
func resolveBuildUploadPlatform(
	cfg *config.ProjectConfig,
	platformOrKey string,
) (*buildUploadPlatformResolution, error) {
	if cfg == nil {
		return nil, fmt.Errorf("project config is required")
	}

	platformOrKey = strings.TrimSpace(platformOrKey)
	if platformOrKey == "" {
		return nil, fmt.Errorf("platform is required")
	}

	// Exact build.platforms key match always wins.
	if platformCfg, ok := cfg.Build.Platforms[platformOrKey]; ok {
		if !isRunnableBuildPlatform(platformCfg) {
			return nil, buildPlatformNeedsSetupError(platformOrKey)
		}
		devicePlatform := platformFromKey(platformOrKey)
		if normalized, err := normalizeMobilePlatform(platformOrKey, ""); err == nil {
			devicePlatform = normalized
		}
		return &buildUploadPlatformResolution{
			PlatformKey:    platformOrKey,
			DevicePlatform: devicePlatform,
			Config:         platformCfg,
		}, nil
	}

	normalizedPlatform, normalizeErr := normalizeMobilePlatform(platformOrKey, "")
	if normalizeErr == nil {
		// Resolve ios/android alias to configured key when possible.
		if platformKey := pickBestBuildPlatformKey(cfg, normalizedPlatform); platformKey != "" {
			return &buildUploadPlatformResolution{
				PlatformKey:    platformKey,
				DevicePlatform: normalizedPlatform,
				Config:         cfg.Build.Platforms[platformKey],
			}, nil
		}

		// Backward compatibility: support flat build.{command,output} configs.
		if len(cfg.Build.Platforms) == 0 && strings.TrimSpace(cfg.Build.Output) != "" {
			return &buildUploadPlatformResolution{
				PlatformKey:    normalizedPlatform,
				DevicePlatform: normalizedPlatform,
				Config: config.BuildPlatform{
					Command: cfg.Build.Command,
					Output:  cfg.Build.Output,
				},
				LegacyConfig: true,
			}, nil
		}
	}

	available := availableBuildPlatformKeys(cfg)
	return nil, fmt.Errorf(
		"unknown platform/platform-key '%s' (available: %v)",
		platformOrKey,
		available,
	)
}

// getPlatformNames returns a slice of platform names from the platforms map.
func getPlatformNames(platforms map[string]config.BuildPlatform) []string {
	names := make([]string, 0, len(platforms))
	for name := range platforms {
		names = append(names, name)
	}
	return names
}

func extractBuildCommit(metadata map[string]interface{}) string {
	if len(metadata) == 0 {
		return ""
	}

	if gitMeta := toMetadataMap(metadata["git"]); gitMeta != nil {
		if short := readMetadataString(gitMeta, "commit_short"); short != "" {
			return short
		}
		if commit := readMetadataString(gitMeta, "commit"); commit != "" {
			return abbreviateCommit(commit)
		}
	}

	if sourceMeta := toMetadataMap(metadata["source_metadata"]); sourceMeta != nil {
		if commit := readMetadataString(sourceMeta, "commit_sha"); commit != "" {
			return abbreviateCommit(commit)
		}
	}

	return ""
}

func toMetadataMap(raw interface{}) map[string]interface{} {
	switch typed := raw.(type) {
	case map[string]interface{}:
		return typed
	case map[string]string:
		converted := make(map[string]interface{}, len(typed))
		for key, value := range typed {
			converted[key] = value
		}
		return converted
	default:
		return nil
	}
}

func readMetadataString(metadata map[string]interface{}, key string) string {
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	if str, ok := value.(string); ok {
		return strings.TrimSpace(str)
	}
	return ""
}

func abbreviateCommit(commit string) string {
	commit = strings.TrimSpace(commit)
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
