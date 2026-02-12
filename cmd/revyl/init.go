// Package main provides the init command as a guided onboarding wizard.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/hotreload"
	_ "github.com/revyl/cli/internal/hotreload/providers" // Register providers
	"github.com/revyl/cli/internal/sync"
	"github.com/revyl/cli/internal/ui"
	"github.com/revyl/cli/internal/util"
)

// initCmd initializes a Revyl project in the current directory via a guided wizard.
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Revyl project configuration",
	Long: `Initialize a Revyl project in the current directory via a guided wizard.

The wizard walks you through:
  1. Project setup — detect build system, create config
  2. Authentication — check or prompt browser login
  3. Create apps — for each detected platform, create or select an app
  4. First build — build and upload your artifact
  5. Create first test — create a test on the platform
  6. Create workflow — optionally group tests into a workflow

Use --non-interactive / -y to skip the wizard and just create config.

Examples:
  revyl init                    # Full guided wizard
  revyl init -y                 # Non-interactive: detect + create config only
  revyl init --project ID       # Link to existing Revyl project
  revyl init --detect           # Re-run build system detection
  revyl init --force            # Overwrite existing configuration`,
	RunE: runInit,
}

var (
	initProjectID      string
	initDetect         bool
	initForce          bool
	initNonInteractive bool
)

func init() {
	initCmd.Flags().StringVar(&initProjectID, "project", "", "Link to existing Revyl project ID")
	initCmd.Flags().BoolVar(&initDetect, "detect", false, "Re-run build system detection")
	initCmd.Flags().BoolVar(&initForce, "force", false, "Overwrite existing configuration")
	initCmd.Flags().BoolVarP(&initNonInteractive, "non-interactive", "y", false, "Skip wizard prompts, just create config")
}

// ---------------------------------------------------------------------------
// Main entry point
// ---------------------------------------------------------------------------

// runInit executes the init wizard.
func runInit(cmd *cobra.Command, args []string) error {
	ui.PrintBanner(version)

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	revylDir := filepath.Join(cwd, ".revyl")
	configPath := filepath.Join(revylDir, "config.yaml")

	// Check if already initialized
	if _, err := os.Stat(configPath); err == nil && !initForce && !initDetect {
		ui.PrintWarning("Project already initialized")
		ui.PrintInfo("Use --force to overwrite or --detect to re-run detection")
		return nil
	}

	devMode, _ := cmd.Flags().GetBool("dev")

	// ── Step 1/6: Project Setup ──────────────────────────────────────────
	ui.PrintStepHeader(1, 6, "Project Setup")

	cfg, err := wizardProjectSetup(cwd, revylDir, configPath)
	if err != nil {
		return err
	}

	// In non-interactive mode we stop after creating the config.
	if initNonInteractive {
		printCreatedFiles()
		printHotReloadInfo(cwd)
		ui.PrintInfo("Next steps:")
		ui.PrintInfo("  1. Authenticate:             revyl auth login")
		ui.PrintInfo("  2. Upload your first build:  revyl build upload --platform <ios|android>")
		ui.PrintInfo("  3. Create a test:            revyl test create <name> --platform <ios|android>")
		ui.PrintInfo("  4. Run it:                   revyl run <name>")
		return nil
	}

	// ── Step 2/6: Authentication ─────────────────────────────────────────
	ui.PrintStepHeader(2, 6, "Authentication")

	ctx := context.Background()
	client, userInfo, authOK := wizardAuth(ctx, devMode)

	// If auth failed or was skipped, we cannot proceed to API-dependent steps.
	if !authOK {
		ui.Println()
		ui.PrintWarning("Skipping steps 3-6 (require authentication)")
		ui.Println()
		printCreatedFiles()
		printHotReloadInfo(cwd)
		ui.PrintInfo("Next steps:")
		ui.PrintInfo("  1. Authenticate:             revyl auth login")
		ui.PrintInfo("  2. Upload your first build:  revyl build upload --platform <ios|android>")
		ui.PrintInfo("  3. Create a test:            revyl test create <name> --platform <ios|android>")
		ui.PrintInfo("  4. Run it:                   revyl run <name>")
		return nil
	}

	// ── Step 3/6: Create Apps ────────────────────────────────────────────
	ui.PrintStepHeader(3, 6, "Create Apps")

	wizardCreateApps(ctx, client, cfg, configPath)

	// Determine if any apps were linked.
	appsLinked := false
	for _, plat := range cfg.Build.Platforms {
		if plat.AppID != "" {
			appsLinked = true
			break
		}
	}

	// ── Step 4/6: First Build ───────────────────────────────────────────
	ui.PrintStepHeader(4, 6, "First Build")
	wizardFirstBuild(ctx, client, cfg, configPath)

	// ── Step 5/6: Create First Test ──────────────────────────────────────
	ui.PrintStepHeader(5, 6, "Create First Test")

	testID, testName := wizardCreateTest(ctx, client, cfg, configPath, devMode, userInfo)

	// ── Step 6/6: Create Workflow ────────────────────────────────────────
	ui.PrintStepHeader(6, 6, "Create Workflow")

	wizardCreateWorkflow(ctx, client, cfg, configPath, testID, testName, userInfo)

	// ── Summary ──────────────────────────────────────────────────────────
	// Mark config as synced now that all wizard steps have completed.
	cfg.MarkSynced()
	_ = config.WriteProjectConfig(configPath, cfg)

	ui.Println()

	// Build summary of what was accomplished.
	summaryItems := []ui.WizardSummaryItem{
		{Title: "Project Setup", OK: true, Detail: ".revyl/config.yaml"},
		{Title: "Authentication", OK: authOK},
		{Title: "Create Apps", OK: appsLinked},
		{Title: "Create Test", OK: testID != "", Detail: testName},
	}
	if userInfo != nil {
		summaryItems[1].Detail = userInfo.Email
	}
	ui.PrintWizardSummary(summaryItems)
	ui.Println()

	printHotReloadInfo(cwd)
	printDynamicNextSteps(cfg, authOK, testID)

	return nil
}

// ---------------------------------------------------------------------------
// Step 1: Project Setup
// ---------------------------------------------------------------------------

// wizardProjectSetup detects the build system, creates .revyl/ directory, and
// writes the initial config.yaml.
func wizardProjectSetup(cwd, revylDir, configPath string) (*config.ProjectConfig, error) {
	ui.PrintInfo("Initializing Revyl project in %s", cwd)
	ui.Println()

	// Detect build system
	ui.StartSpinner("Detecting build system...")
	detected, err := build.Detect(cwd)
	ui.StopSpinner()

	if err != nil {
		ui.PrintWarning("Could not auto-detect build system: %v", err)
		detected = &build.DetectedBuild{
			System: build.SystemUnknown,
		}
	}

	if detected.System != build.SystemUnknown {
		ui.PrintSuccess("Detected: %s", detected.System.String())
		if detected.Command != "" {
			ui.PrintInfo("Build command: %s", detected.Command)
		}
		if detected.Output != "" {
			ui.PrintInfo("Output: %s", detected.Output)
		}
	} else {
		ui.PrintWarning("Could not detect build system")
		ui.PrintInfo("You can configure this manually in .revyl/config.yaml")
	}

	ui.Println()

	projectName := util.SanitizeForFilename(filepath.Base(cwd))
	if projectName == "" {
		projectName = "my-project"
	}

	cfg := &config.ProjectConfig{
		Project: config.Project{
			ID:   initProjectID,
			Name: projectName,
		},
		Build: config.BuildConfig{
			System:  detected.System.String(),
			Command: detected.Command,
			Output:  detected.Output,
		},
		Tests:     make(map[string]string),
		Workflows: make(map[string]string),
		Defaults: config.Defaults{
			OpenBrowser: true,
			Timeout:     600,
		},
	}

	// Add platforms if detected
	if len(detected.Platforms) > 0 {
		cfg.Build.Platforms = make(map[string]config.BuildPlatform)
		for name, platform := range detected.Platforms {
			cfg.Build.Platforms[name] = config.BuildPlatform{
				Command: platform.Command,
				Output:  platform.Output,
			}
		}
	}

	// Create .revyl directory
	if err := os.MkdirAll(revylDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create .revyl directory: %w", err)
	}

	// Create tests directory
	testsDir := filepath.Join(revylDir, "tests")
	if err := os.MkdirAll(testsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create tests directory: %w", err)
	}

	// Write config file
	if err := config.WriteProjectConfig(configPath, cfg); err != nil {
		return nil, fmt.Errorf("failed to write config: %w", err)
	}

	// Create .gitignore for .revyl directory
	gitignorePath := filepath.Join(revylDir, ".gitignore")
	gitignoreContent := `# Revyl CLI local files
# Keep credentials out of version control
credentials.json

# Local overrides (optional)
config.local.yaml
`
	if err := os.WriteFile(gitignorePath, []byte(gitignoreContent), 0644); err != nil {
		ui.PrintWarning("Failed to create .gitignore: %v", err)
	}

	ui.PrintSuccess("Project config created")

	return cfg, nil
}

// ---------------------------------------------------------------------------
// Step 2: Authentication
// ---------------------------------------------------------------------------

// wizardAuth checks for existing credentials and, if missing, walks the user
// through browser-based login. Returns an API client, validated user info,
// and a boolean indicating whether auth succeeded.
func wizardAuth(ctx context.Context, devMode bool) (*api.Client, *api.ValidateAPIKeyResponse, bool) {
	mgr := auth.NewManager()

	// Check existing credentials first.
	token, _ := mgr.GetActiveToken()
	if token != "" {
		ui.PrintDim("Found existing credentials, validating...")
		client := api.NewClientWithDevMode(token, devMode)
		userInfo, err := client.ValidateAPIKey(ctx)
		if err == nil {
			ui.PrintSuccess("Authenticated as %s", userInfo.Email)
			return client, userInfo, true
		}
		ui.PrintWarning("Existing credentials invalid, need to re-authenticate")
	}

	// Prompt user for browser login.
	proceed, err := ui.PromptConfirm("Log in via browser?", true)
	if err != nil || !proceed {
		ui.PrintWarning("Authentication skipped")
		return nil, nil, false
	}

	ui.StartSpinner("Waiting for browser authentication...")

	browserAuth := auth.NewBrowserAuth(auth.BrowserAuthConfig{
		AppURL:  config.GetAppURL(devMode),
		Timeout: 5 * time.Minute,
	})

	result, err := browserAuth.Authenticate(ctx)
	ui.StopSpinner()

	if err != nil {
		ui.PrintWarning("Authentication failed: %v", err)
		return nil, nil, false
	}

	if result.Error != "" {
		ui.PrintWarning("Authentication error: %s", result.Error)
		return nil, nil, false
	}

	// Save credentials.
	if err := mgr.SaveBrowserCredentials(result, defaultTokenExpiration); err != nil {
		ui.PrintWarning("Failed to save credentials: %v", err)
		// Continue anyway — the token is still usable this session.
	}

	// Validate and enrich credentials.
	client := api.NewClientWithDevMode(result.Token, devMode)
	userInfo, err := client.ValidateAPIKey(ctx)
	if err != nil {
		ui.PrintWarning("Could not validate token: %v", err)
		// Still partially usable.
		ui.PrintSuccess("Authenticated (could not fetch user details)")
		return client, nil, true
	}

	// Re-save with validated info.
	_ = mgr.SaveBrowserCredentials(&auth.BrowserAuthResult{
		Token:  result.Token,
		Email:  userInfo.Email,
		OrgID:  userInfo.OrgID,
		UserID: userInfo.UserID,
	}, defaultTokenExpiration)

	ui.PrintSuccess("Authenticated as %s", userInfo.Email)
	return client, userInfo, true
}

// ---------------------------------------------------------------------------
// Step 3: Create Apps
// ---------------------------------------------------------------------------

// wizardCreateApps iterates over detected platforms and lets the user create
// or select an app for each one, saving the AppID back into the config.
func wizardCreateApps(ctx context.Context, client *api.Client, cfg *config.ProjectConfig, configPath string) {
	if len(cfg.Build.Platforms) == 0 {
		ui.PrintDim("No platforms detected — skipping app creation")
		ui.PrintDim("You can add platforms manually in .revyl/config.yaml")
		return
	}

	for platformKey, plat := range cfg.Build.Platforms {
		if plat.AppID != "" {
			ui.PrintDim("Platform %s already linked to app %s", platformKey, plat.AppID)
			continue
		}

		fmt.Println(ui.TitleStyle.Render(fmt.Sprintf("Platform: %s", platformKey)))

		// Fetch existing apps for this platform.
		appsResp, err := client.ListApps(ctx, platformKey, 1, 10)
		if err != nil {
			ui.PrintWarning("Could not list apps for %s: %v", platformKey, err)
			continue
		}

		var appID string

		if len(appsResp.Items) > 0 {
			// Paginated selection loop: shows apps page-by-page with "Show more" option.
			allApps := make([]api.App, 0, len(appsResp.Items))
			allApps = append(allApps, appsResp.Items...)
			page := 1
			hasMore := appsResp.HasNext

			for {
				// Build selection options: accumulated apps + Show more (if available) + Create new + Skip.
				selectOptions := make([]ui.SelectOption, 0, len(allApps)+3)
				for _, app := range allApps {
					selectOptions = append(selectOptions, ui.SelectOption{
						Label: fmt.Sprintf("%s (id: %s)", app.Name, app.ID),
					})
				}

				showMoreIdx := -1
				if hasMore {
					showMoreIdx = len(selectOptions)
					selectOptions = append(selectOptions, ui.SelectOption{Label: "Show more..."})
				}

				createNewIdx := len(selectOptions)
				selectOptions = append(selectOptions, ui.SelectOption{Label: "Create new app"})
				selectOptions = append(selectOptions, ui.SelectOption{Label: "Skip"})

				skipIdx := len(selectOptions) - 1

				idx, _, selErr := ui.Select(fmt.Sprintf("Select app for %s:", platformKey), selectOptions, createNewIdx)
				if selErr != nil {
					ui.PrintWarning("Selection failed: %v", selErr)
					break
				}

				if hasMore && idx == showMoreIdx {
					// Fetch the next page and append results.
					page++
					nextResp, nextErr := client.ListApps(ctx, platformKey, page, 10)
					if nextErr != nil {
						ui.PrintWarning("Could not fetch more apps: %v", nextErr)
						continue
					}
					allApps = append(allApps, nextResp.Items...)
					hasMore = nextResp.HasNext
					continue
				}

				if idx == createNewIdx {
					appID = createAppInteractive(ctx, client, cfg.Project.Name, platformKey)
				} else if idx == skipIdx {
					// Skip — no action.
				} else if idx < len(allApps) {
					appID = allApps[idx].ID
					ui.PrintSuccess("Linked %s to app %s", platformKey, allApps[idx].Name)
				}
				break
			}
		} else {
			// No existing apps — offer to create one.
			proceed, err := ui.PromptConfirm(fmt.Sprintf("No apps found for %s. Create one?", platformKey), true)
			if err != nil || !proceed {
				continue
			}
			appID = createAppInteractive(ctx, client, cfg.Project.Name, platformKey)
		}

		if appID != "" {
			plat.AppID = appID
			cfg.Build.Platforms[platformKey] = plat
			_ = config.WriteProjectConfig(configPath, cfg)
		}
	}
}

// createAppInteractive prompts for a name and creates an app via the API.
// Returns the new app ID or empty string on failure.
func createAppInteractive(ctx context.Context, client *api.Client, defaultName, platform string) string {
	name, err := ui.Prompt(fmt.Sprintf("App name [%s-%s]:", defaultName, platform))
	if err != nil {
		ui.PrintWarning("Input error: %v", err)
		return ""
	}
	if name == "" {
		name = fmt.Sprintf("%s-%s", defaultName, platform)
	}

	ui.StartSpinner("Creating app...")
	resp, err := client.CreateApp(ctx, &api.CreateAppRequest{
		Name:     name,
		Platform: platform,
	})
	ui.StopSpinner()

	if err != nil {
		// Check if app already exists (409 Conflict) and link to it instead of failing.
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 409 {
			ui.PrintDim("App '%s' already exists, looking it up...", name)
			appsResp, listErr := client.ListApps(ctx, platform, 1, 100)
			if listErr == nil {
				for _, app := range appsResp.Items {
					if app.Name == name {
						ui.PrintSuccess("Linked to existing app %s (id: %s)", app.Name, app.ID)
						return app.ID
					}
				}
			}
		}
		ui.PrintWarning("Failed to create app: %v", err)
		return ""
	}

	ui.PrintSuccess("Created app %s (id: %s)", resp.Name, resp.ID)
	return resp.ID
}

// ---------------------------------------------------------------------------
// Step 4: First Build
// ---------------------------------------------------------------------------

// wizardFirstBuild iterates over configured platforms and offers to build and
// upload each one. Errors are non-fatal — a failed build/upload prints a
// warning and continues to the next platform (or next wizard step).
//
// Parameters:
//   - ctx: Context for cancellation and API calls
//   - client: Authenticated API client for uploading builds
//   - cfg: Current project configuration (platforms, app IDs)
//   - configPath: Path to .revyl/config.yaml for potential updates
func wizardFirstBuild(ctx context.Context, client *api.Client, cfg *config.ProjectConfig, configPath string) {
	platforms := platformKeys(cfg)
	if len(platforms) == 0 {
		ui.PrintDim("No platforms configured — skipping build step")
		return
	}

	cwd, err := os.Getwd()
	if err != nil {
		ui.PrintWarning("Could not determine working directory: %v", err)
		return
	}

	for _, platform := range platforms {
		platformCfg, ok := cfg.Build.Platforms[platform]
		if !ok {
			continue
		}

		// Check prerequisite: app ID must be set from Step 3.
		if platformCfg.AppID == "" {
			ui.PrintDim("Skipping %s — no app linked (run revyl build upload --platform %s later)", platform, platform)
			continue
		}

		// Check prerequisite: output path must be configured.
		if platformCfg.Output == "" {
			ui.PrintDim("Skipping %s — no output path configured in .revyl/config.yaml", platform)
			continue
		}

		// Ask user what to do for this platform.
		buildOptions := []ui.SelectOption{
			{Label: "Build and upload"},
			{Label: "Upload existing artifact"},
			{Label: "Skip"},
		}
		idx, _, promptErr := ui.Select(fmt.Sprintf("What would you like to do for %s?", platform), buildOptions, 0)
		if promptErr != nil || idx == 2 {
			ui.PrintDim("  Run later: revyl build upload --platform %s", platform)
			continue
		}
		skipBuild := idx == 1

		// Run the build (if not skipping).
		var buildDuration time.Duration
		if !skipBuild {
			ui.PrintInfo("Building with: %s", platformCfg.Command)
			ui.Println()

			startTime := time.Now()
			runner := build.NewRunner(cwd)

			buildErr := runner.Run(platformCfg.Command, func(line string) {
				ui.PrintDim("  %s", line)
			})

			buildDuration = time.Since(startTime)

			if buildErr != nil {
				ui.Println()
				ui.PrintWarning("Build failed for %s: %v", platform, buildErr)
				ui.PrintDim("  You can retry later: revyl build upload --platform %s", platform)
				continue
			}

			ui.Println()
			ui.PrintSuccess("Build completed in %s", buildDuration.Round(time.Second))
		} else {
			ui.PrintInfo("Skipping build step — uploading existing artifact")
		}

		// Resolve artifact path.
		artifactPath, resolveErr := build.ResolveArtifactPath(cwd, platformCfg.Output)
		if resolveErr != nil {
			ui.PrintWarning("Artifact not found at default location: %s", platformCfg.Output)
			customPath, customErr := ui.Prompt(fmt.Sprintf("Enter path to %s artifact (or press Enter to skip):", platform))
			if customErr != nil || customPath == "" {
				ui.PrintDim("  You can retry later: revyl build upload --platform %s", platform)
				continue
			}
			artifactPath, resolveErr = build.ResolveArtifactPath(cwd, customPath)
			if resolveErr != nil {
				ui.PrintWarning("Artifact not found: %s", customPath)
				ui.PrintDim("  You can retry later: revyl build upload --platform %s", platform)
				continue
			}
		}

		// Convert tar.gz to zip for iOS builds (EAS produces tar.gz).
		if build.IsTarGz(artifactPath) {
			ui.StartSpinner("Extracting .app from tar.gz...")
			zipPath, extractErr := build.ExtractAppFromTarGz(artifactPath)
			ui.StopSpinner()
			if extractErr != nil {
				ui.PrintWarning("Failed to extract .app from tar.gz: %v", extractErr)
				ui.PrintDim("  You can retry later: revyl build upload --platform %s", platform)
				continue
			}
			defer os.Remove(zipPath)
			artifactPath = zipPath
			ui.PrintSuccess("Converted to: %s", filepath.Base(zipPath))
		} else if build.IsAppBundle(artifactPath) {
			// Zip .app directory for iOS builds (Flutter, React Native, Xcode).
			ui.StartSpinner("Zipping .app bundle...")
			zipPath, zipErr := build.ZipAppBundle(artifactPath)
			ui.StopSpinner()
			if zipErr != nil {
				ui.PrintWarning("Failed to zip .app bundle: %v", zipErr)
				ui.PrintDim("  You can retry later: revyl build upload --platform %s", platform)
				continue
			}
			defer os.Remove(zipPath)
			artifactPath = zipPath
			ui.PrintSuccess("Created: %s", filepath.Base(zipPath))
		}

		// Collect build metadata and generate version.
		metadata := build.CollectMetadata(cwd, platformCfg.Command, platform, buildDuration)
		versionStr := build.GenerateVersionString()

		ui.Println()
		ui.PrintInfo("Uploading: %s", filepath.Base(artifactPath))
		ui.PrintInfo("Build Version: %s", versionStr)
		ui.Println()

		ui.StartSpinner("Uploading artifact...")
		result, uploadErr := client.UploadBuild(ctx, &api.UploadBuildRequest{
			AppID:        platformCfg.AppID,
			Version:      versionStr,
			FilePath:     artifactPath,
			Metadata:     metadata,
			SetAsCurrent: true,
		})
		ui.StopSpinner()

		if uploadErr != nil {
			ui.PrintWarning("Upload failed for %s: %v", platform, uploadErr)
			ui.PrintDim("  You can retry later: revyl build upload --platform %s", platform)
			continue
		}

		ui.Println()
		ui.PrintSuccess("Upload complete!")
		ui.PrintKeyValue("App:", platformCfg.AppID)
		ui.PrintKeyValue("Build Version:", result.Version)
		if result.VersionID != "" {
			ui.PrintKeyValue("Build ID:", result.VersionID)
		}
	}
}

// ---------------------------------------------------------------------------
// Step 5: Create First Test
// ---------------------------------------------------------------------------

// wizardCreateTest offers to create a test, saves it in the config, and opens
// it in the browser. Returns the created test ID and name (both empty if
// skipped/failed).
func wizardCreateTest(
	ctx context.Context,
	client *api.Client,
	cfg *config.ProjectConfig,
	configPath string,
	devMode bool,
	userInfo *api.ValidateAPIKeyResponse,
) (string, string) {
	proceed, err := ui.PromptConfirm("Create your first test?", true)
	if err != nil || !proceed {
		ui.PrintDim("Skipped test creation")
		return "", ""
	}

	// Prompt for test name.
	testName, err := ui.Prompt("Test name [login]:")
	if err != nil {
		ui.PrintWarning("Input error: %v", err)
		return "", ""
	}
	if testName == "" {
		testName = "login"
	}

	// Select platform from configured ones.
	platforms := platformKeys(cfg)
	var platform string

	switch len(platforms) {
	case 0:
		// No platforms detected; ask the user directly.
		idx, err := ui.PromptSelect("Select platform:", []string{"ios", "android"})
		if err != nil {
			ui.PrintWarning("Selection error: %v", err)
			return "", ""
		}
		if idx == 0 {
			platform = "ios"
		} else {
			platform = "android"
		}
	case 1:
		platform = platforms[0]
		ui.PrintDim("Using platform: %s", platform)
	default:
		idx, err := ui.PromptSelect("Select platform:", platforms)
		if err != nil {
			ui.PrintWarning("Selection error: %v", err)
			return "", ""
		}
		platform = platforms[idx]
	}

	// Determine AppID and OrgID for the request.
	appID := ""
	if plat, ok := cfg.Build.Platforms[platform]; ok {
		appID = plat.AppID
	}

	orgID := ""
	if userInfo != nil {
		orgID = userInfo.OrgID
	}

	// Create the test (with retry loop for conflict resolution).
	for {
		ui.StartSpinner("Creating test...")
		resp, err := client.CreateTest(ctx, &api.CreateTestRequest{
			Name:     testName,
			Platform: platform,
			Tasks:    []interface{}{}, // empty tasks — user will add later
			AppID:    appID,
			OrgID:    orgID,
		})
		ui.StopSpinner()

		if err != nil {
			// Detect conflict: 409 from backend, 500 wrapping "already exists",
			// or raw error string containing "already exists" (fallback).
			isConflict := false
			var apiErr *api.APIError
			if errors.As(err, &apiErr) {
				isConflict = apiErr.StatusCode == 409 ||
					(apiErr.StatusCode == 500 && strings.Contains(apiErr.Error(), "already exists"))
			}
			if !isConflict {
				isConflict = strings.Contains(err.Error(), "already exists")
			}

			if isConflict {
				ui.PrintWarning("A test named \"%s\" already exists.", testName)
				conflictOptions := []ui.SelectOption{
					{Label: "Link to existing test"},
					{Label: "Rename and create new"},
					{Label: "Skip"},
				}
				idx, _, selErr := ui.Select("What would you like to do?", conflictOptions, 0)
				if selErr != nil || idx == 2 {
					ui.PrintDim("Skipped test creation")
					return "", ""
				}

				if idx == 0 {
					// Link to existing test by looking it up by name.
					listResp, listErr := client.ListOrgTests(ctx, 100, 0)
					if listErr == nil {
						for _, t := range listResp.Tests {
							if t.Name == testName {
								ui.PrintSuccess("Linked to existing test \"%s\" (id: %s)", t.Name, t.ID)
								if cfg.Tests == nil {
									cfg.Tests = make(map[string]string)
								}
								cfg.Tests[testName] = t.ID
								_ = config.WriteProjectConfig(configPath, cfg)
								syncTestYAML(ctx, client, cfg, testName)
								return t.ID, testName
							}
						}
					}
					ui.PrintWarning("Could not find existing test \"%s\"", testName)
					return "", ""
				}

				if idx == 1 {
					// Rename: prompt for a new name and retry.
					newName, promptErr := ui.Prompt(fmt.Sprintf("New test name [%s]:", testName))
					if promptErr != nil {
						ui.PrintWarning("Input error: %v", promptErr)
						return "", ""
					}
					if newName == "" {
						ui.PrintDim("No name entered, skipping test creation")
						return "", ""
					}
					testName = newName
					continue // retry with new name
				}
			}

			ui.PrintWarning("Failed to create test: %v", err)
			return "", ""
		}

		ui.PrintSuccess("Created test \"%s\" (id: %s)", testName, resp.ID)

		// Save to config.
		if cfg.Tests == nil {
			cfg.Tests = make(map[string]string)
		}
		cfg.Tests[testName] = resp.ID
		_ = config.WriteProjectConfig(configPath, cfg)
		syncTestYAML(ctx, client, cfg, testName)

		// Open in browser.
		appURL := config.GetAppURL(devMode)
		testURL := fmt.Sprintf("%s/tests/%s", appURL, resp.ID)
		if openErr := ui.OpenBrowser(testURL); openErr == nil {
			ui.PrintDim("Opened in browser: %s", testURL)
		} else {
			ui.PrintDim("View your test: %s", testURL)
		}

		return resp.ID, testName
	}
}

// ---------------------------------------------------------------------------
// Step 6: Create Workflow
// ---------------------------------------------------------------------------

// wizardCreateWorkflow offers to group tests into a workflow. Default is No.
//
// Parameters:
//   - ctx: Context for cancellation and API calls
//   - client: Authenticated API client
//   - cfg: Current project configuration
//   - configPath: Path to .revyl/config.yaml
//   - justCreatedTestID: ID of the test created in Step 5 (empty if skipped)
//   - justCreatedTestName: Name of the test created in Step 5 (empty if skipped)
//   - userInfo: Validated user info for ownership
func wizardCreateWorkflow(
	ctx context.Context,
	client *api.Client,
	cfg *config.ProjectConfig,
	configPath string,
	justCreatedTestID string,
	justCreatedTestName string,
	userInfo *api.ValidateAPIKeyResponse,
) {
	proceed, err := ui.PromptConfirm("Create a workflow to group tests?", false)
	if err != nil || !proceed {
		ui.PrintDim("Skipped workflow creation")
		return
	}

	workflowName, err := ui.Prompt("Workflow name [smoke-tests]:")
	if err != nil {
		ui.PrintWarning("Input error: %v", err)
		return
	}
	if workflowName == "" {
		workflowName = "smoke-tests"
	}

	// Gather test IDs for the workflow.
	testIDs := gatherTestIDsForWorkflow(ctx, client, justCreatedTestID, justCreatedTestName)

	ownerID := ""
	orgID := ""
	if userInfo != nil {
		ownerID = userInfo.UserID
		orgID = userInfo.OrgID
	}

	ui.StartSpinner("Creating workflow...")
	resp, err := client.CreateWorkflow(ctx, &api.CLICreateWorkflowRequest{
		Name:     workflowName,
		Tests:    testIDs,
		Schedule: "No Schedule",
		Owner:    ownerID,
		OrgID:    orgID,
	})
	ui.StopSpinner()

	if err != nil {
		ui.PrintWarning("Failed to create workflow: %v", err)
		return
	}

	ui.PrintSuccess("Created workflow \"%s\" (id: %s)", resp.Data.Name, resp.GetID())

	// Save to config.
	if cfg.Workflows == nil {
		cfg.Workflows = make(map[string]string)
	}
	cfg.Workflows[workflowName] = resp.GetID()
	_ = config.WriteProjectConfig(configPath, cfg)
}

// gatherTestIDsForWorkflow builds the list of test IDs for a new workflow.
// If we just created a test, it is automatically included. Additionally, the
// user may pick from existing org tests (capped at 10).
//
// Parameters:
//   - ctx: Context for cancellation and API calls
//   - client: Authenticated API client
//   - justCreatedTestID: ID of the just-created test (empty if none)
//   - justCreatedTestName: Display name of the just-created test (empty if none)
//
// Returns:
//   - []string: Collected test IDs for the workflow
func gatherTestIDsForWorkflow(ctx context.Context, client *api.Client, justCreatedTestID, justCreatedTestName string) []string {
	var testIDs []string

	if justCreatedTestID != "" {
		testIDs = append(testIDs, justCreatedTestID)
		if justCreatedTestName != "" {
			ui.PrintSuccess("Included: %s (just created)", justCreatedTestName)
		} else {
			ui.PrintSuccess("Included the test you just created")
		}
	}

	// Offer to add more tests from the org.
	addMore, err := ui.PromptConfirm("Add existing tests from your organization?", false)
	if err != nil || !addMore {
		return testIDs
	}

	listResp, err := client.ListOrgTests(ctx, 10, 0)
	if err != nil {
		ui.PrintWarning("Could not list org tests: %v", err)
		return testIDs
	}

	if len(listResp.Tests) == 0 {
		ui.PrintDim("No existing tests found in your organization")
		return testIDs
	}

	// Build options list, excluding the test we already added.
	options := make([]string, 0, len(listResp.Tests)+1)
	indexMap := make([]int, 0, len(listResp.Tests))

	for i, t := range listResp.Tests {
		if t.ID == justCreatedTestID {
			continue
		}
		options = append(options, fmt.Sprintf("%s (%s)", t.Name, t.Platform))
		indexMap = append(indexMap, i)
	}

	if len(options) == 0 {
		ui.PrintDim("No additional tests available")
		return testIDs
	}

	// Hint when more tests exist beyond what we fetched.
	if listResp.Count > 10 {
		ui.PrintDim("Showing first 10 of %d tests. Use 'revyl workflow edit' to add more.", listResp.Count)
	}

	options = append(options, "Done — no more tests")

	// Let user pick tests one at a time.
	for {
		idx, err := ui.PromptSelect("Select a test to add (or Done):", options)
		if err != nil || idx >= len(indexMap) {
			break
		}

		selectedTest := listResp.Tests[indexMap[idx]]
		testIDs = append(testIDs, selectedTest.ID)
		ui.PrintSuccess("Added: %s", selectedTest.Name)

		// Remove the selected option so it can't be added twice.
		options = append(options[:idx], options[idx+1:]...)
		indexMap = append(indexMap[:idx], indexMap[idx+1:]...)

		if len(indexMap) == 0 {
			break
		}
	}

	return testIDs
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// platformKeys returns the platform keys from the config (e.g. ["ios", "android"]).
func platformKeys(cfg *config.ProjectConfig) []string {
	if len(cfg.Build.Platforms) == 0 {
		return nil
	}
	keys := make([]string, 0, len(cfg.Build.Platforms))
	for k := range cfg.Build.Platforms {
		keys = append(keys, k)
	}
	return keys
}

// printCreatedFiles prints the list of files created during init.
func printCreatedFiles() {
	ui.PrintSuccess("Project initialized!")
	ui.Println()
	ui.PrintInfo("Created:")
	ui.PrintInfo("  .revyl/config.yaml    - Project configuration")
	ui.PrintInfo("  .revyl/tests/         - Local test definitions")
	ui.PrintInfo("  .revyl/.gitignore     - Git ignore rules")
	ui.Println()
}

// printHotReloadInfo checks for hot-reload-compatible providers and prints info.
func printHotReloadInfo(cwd string) {
	registry := hotreload.DefaultRegistry()
	detections := registry.DetectAllProviders(cwd)

	if len(detections) == 0 {
		return
	}

	var supportedDetections []hotreload.ProviderDetection
	for _, d := range detections {
		if d.Provider.IsSupported() {
			supportedDetections = append(supportedDetections, d)
		}
	}

	if len(supportedDetections) > 0 {
		ui.PrintInfo("Found compatible hot reload provider(s):")
		for _, d := range supportedDetections {
			ui.PrintInfo("  • %s (fully supported)", d.Provider.DisplayName())
		}

		for _, d := range detections {
			if !d.Provider.IsSupported() {
				ui.PrintDim("  • %s (coming soon)", d.Provider.DisplayName())
			}
		}
		ui.Println()
		ui.PrintDim("To set up hot reload later, run: revyl hotreload setup")
		ui.Println()
	}
}

// printDynamicNextSteps prints next-step suggestions based on what was completed.
func printDynamicNextSteps(cfg *config.ProjectConfig, authOK bool, testID string) {
	var steps []ui.NextStep

	if !authOK {
		steps = append(steps, ui.NextStep{Label: "Authenticate:", Command: "revyl auth login"})
	}

	// Check if any platform still needs an app.
	hasApps := false
	for _, plat := range cfg.Build.Platforms {
		if plat.AppID != "" {
			hasApps = true
			break
		}
	}
	if !hasApps && len(cfg.Build.Platforms) > 0 {
		steps = append(steps, ui.NextStep{Label: "Create an app:", Command: "revyl init (re-run wizard)"})
	}

	// Build upload is always a useful next step.
	platforms := platformKeys(cfg)
	if len(platforms) > 0 {
		steps = append(steps, ui.NextStep{Label: "Upload a build:", Command: fmt.Sprintf("revyl build upload --platform %s", platforms[0])})
	} else {
		steps = append(steps, ui.NextStep{Label: "Upload a build:", Command: "revyl build upload --platform <ios|android>"})
	}

	if testID == "" {
		steps = append(steps, ui.NextStep{Label: "Create a test:", Command: "revyl test create <name> --platform <ios|android>"})
	}

	if testID != "" {
		// Test exists, suggest running it.
		for alias := range cfg.Tests {
			steps = append(steps, ui.NextStep{Label: "Run your test:", Command: fmt.Sprintf("revyl run %s", alias)})
			break
		}
	} else {
		steps = append(steps, ui.NextStep{Label: "Run a test:", Command: "revyl run <name>"})
	}

	ui.PrintNextSteps(steps)
}

// syncTestYAML pulls a test definition from the server and saves it to .revyl/tests/<name>.yaml.
// Logs a dim message on success or a fallback hint on failure. Non-fatal.
//
// Parameters:
//   - ctx: Context for cancellation
//   - client: Authenticated API client
//   - cfg: Project configuration (must have the test ID already saved in cfg.Tests)
//   - testName: Name of the test to sync
func syncTestYAML(ctx context.Context, client *api.Client, cfg *config.ProjectConfig, testName string) {
	cwd, err := os.Getwd()
	if err != nil {
		ui.PrintDim("  Run 'revyl test pull %s' to sync test definition", testName)
		return
	}
	testsDir := filepath.Join(cwd, ".revyl", "tests")
	localTests, _ := config.LoadLocalTests(testsDir)
	if localTests == nil {
		localTests = make(map[string]*config.LocalTest)
	}
	resolver := sync.NewResolver(client, cfg, localTests)
	results, pullErr := resolver.PullFromRemote(ctx, testName, testsDir, true)
	if pullErr == nil && len(results) > 0 && results[0].Error == nil {
		cfg.MarkSynced()
		// Persist the updated LastSyncedAt timestamp to disk.
		cwd2, _ := os.Getwd()
		if cwd2 != "" {
			configPath := filepath.Join(cwd2, ".revyl", "config.yaml")
			_ = config.WriteProjectConfig(configPath, cfg)
		}
		ui.PrintDim("  Synced to .revyl/tests/%s.yaml", testName)
	} else {
		ui.PrintDim("  Run 'revyl test pull %s' to sync test definition", testName)
	}
}
