// Package main provides the init command as a guided onboarding wizard.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
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
  4. Hot reload setup — configure Expo hot reload defaults
  5. First build — build and upload your artifact
  6. Create first test — create a test on the platform
  7. Create workflow — optionally group tests into a workflow

Use --non-interactive / -y to skip the wizard and just create config.

Examples:
  revyl init                    # Full guided wizard
  revyl init -y                 # Non-interactive: detect + create config only
  revyl init --hotreload        # Reconfigure hot reload for an existing project
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
	initHotReload      bool
)

func init() {
	initCmd.Flags().StringVar(&initProjectID, "project", "", "Link to existing Revyl project ID")
	initCmd.Flags().BoolVar(&initDetect, "detect", false, "Re-run build system detection")
	initCmd.Flags().BoolVar(&initForce, "force", false, "Overwrite existing configuration")
	initCmd.Flags().BoolVarP(&initNonInteractive, "non-interactive", "y", false, "Skip wizard prompts, just create config")
	initCmd.Flags().BoolVar(&initHotReload, "hotreload", false, "Configure hot reload and exit (for existing projects)")
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
	configExists := false
	if _, err := os.Stat(configPath); err == nil {
		configExists = true
	}

	// Check if already initialized
	if configExists && !initForce && !initDetect && !initHotReload {
		ui.PrintWarning("Project already initialized")
		ui.PrintInfo("Use --force to overwrite, --detect to re-run detection, or --hotreload to reconfigure hot reload")
		return nil
	}

	// Hot reload-only configuration mode for existing projects.
	if initHotReload && configExists {
		return runInitHotReloadOnly(cmd, cwd, configPath)
	}

	devMode, _ := cmd.Flags().GetBool("dev")

	// ── Step 1/7: Project Setup ──────────────────────────────────────────
	ui.PrintStepHeader(1, 7, "Project Setup")

	cfg, err := wizardProjectSetup(cwd, revylDir, configPath)
	if err != nil {
		return err
	}

	// In non-interactive mode we stop after creating the config.
	if initNonInteractive {
		wizardHotReloadSetup(context.Background(), nil, cfg, configPath, cwd, false)
		printCreatedFiles()
		printHotReloadInfo(cwd, cfg)
		ui.PrintInfo("Next steps:")
		ui.PrintInfo("  1. Authenticate:             revyl auth login")
		ui.PrintInfo("  2. Upload your first build:  revyl build upload --platform <ios|android>")
		ui.PrintInfo("  3. Create a test:            revyl test create <name> --platform <ios|android>")
		ui.PrintInfo("  4. Run it:                   revyl test run <name>")
		return nil
	}

	// ── Step 2/7: Authentication ─────────────────────────────────────────
	ui.PrintStepHeader(2, 7, "Authentication")

	ctx := context.Background()
	client, userInfo, authOK := wizardAuth(ctx, devMode)

	// If auth failed or was skipped, we cannot proceed to API-dependent steps.
	if !authOK {
		wizardHotReloadSetup(context.Background(), nil, cfg, configPath, cwd, false)
		ui.Println()
		ui.PrintWarning("Skipping steps 3-7 (require authentication)")
		ui.Println()
		printCreatedFiles()
		printHotReloadInfo(cwd, cfg)
		ui.PrintInfo("Next steps:")
		ui.PrintInfo("  1. Authenticate:             revyl auth login")
		ui.PrintInfo("  2. Upload your first build:  revyl build upload --platform <ios|android>")
		ui.PrintInfo("  3. Create a test:            revyl test create <name> --platform <ios|android>")
		ui.PrintInfo("  4. Run it:                   revyl test run <name>")
		return nil
	}

	// Bind the project to the authenticated organization when available.
	if userInfo != nil {
		orgID := strings.TrimSpace(userInfo.OrgID)
		if orgID != "" && cfg.Project.OrgID != orgID {
			cfg.Project.OrgID = orgID
			if err := config.WriteProjectConfig(configPath, cfg); err != nil {
				ui.PrintWarning("Could not persist project org binding: %v", err)
			}
		}
	}

	// ── Billing check (between auth and app creation) ──────────────────
	wizardBillingCheck(ctx, client, devMode)

	// ── Step 3/7: Create Apps ────────────────────────────────────────────
	ui.PrintStepHeader(3, 7, "Create Apps")

	wizardCreateApps(ctx, client, cfg, configPath)

	// ── Step 4/7: Hot Reload Setup ───────────────────────────────────────
	ui.PrintStepHeader(4, 7, "Hot Reload Setup")
	hotReloadReady := wizardHotReloadSetup(ctx, client, cfg, configPath, cwd, true)

	// Determine if any apps were linked.
	appsLinked := false
	for _, plat := range cfg.Build.Platforms {
		if plat.AppID != "" {
			appsLinked = true
			break
		}
	}

	// ── Step 5/7: First Build ────────────────────────────────────────────
	ui.PrintStepHeader(5, 7, "First Build")
	wizardFirstBuild(ctx, client, cfg, configPath)

	// ── Step 6/7: Create First Test ──────────────────────────────────────
	ui.PrintStepHeader(6, 7, "Create First Test")

	testID, testName := wizardCreateTest(ctx, client, cfg, configPath, devMode, userInfo)

	// ── Step 7/7: Create Workflow ────────────────────────────────────────
	ui.PrintStepHeader(7, 7, "Create Workflow")

	wizardCreateWorkflow(ctx, client, cfg, configPath, testID, testName, userInfo)

	// ── Summary ──────────────────────────────────────────────────────────
	// Mark config as synced now that all wizard steps have completed.
	cfg.MarkSynced()
	_ = config.WriteProjectConfig(configPath, cfg)

	ui.Println()

	// Build summary of what was accomplished.
	hotReloadDetail := cfg.HotReload.Default
	if hotReloadDetail == "" {
		hotReloadDetail = "not detected"
	}
	summaryItems := []ui.WizardSummaryItem{
		{Title: "Project Setup", OK: true, Detail: ".revyl/config.yaml"},
		{Title: "Authentication", OK: authOK},
		{Title: "Create Apps", OK: appsLinked},
		{Title: "Hot Reload", OK: hotReloadReady, Detail: hotReloadDetail},
		{Title: "Create Test", OK: testID != "", Detail: testName},
	}
	if userInfo != nil {
		summaryItems[1].Detail = userInfo.Email
	}
	ui.PrintWizardSummary(summaryItems)
	ui.Println()

	printHotReloadInfo(cwd, cfg)
	printDynamicNextSteps(cfg, authOK, testID)

	return nil
}

// runInitHotReloadOnly reconfigures hot reload for an existing project and exits.
func runInitHotReloadOnly(cmd *cobra.Command, cwd, configPath string) error {
	cfg, err := config.LoadProjectConfig(configPath)
	if err != nil {
		ui.PrintError("Project not initialized. Run 'revyl init' first.")
		return fmt.Errorf("project not initialized")
	}

	ui.PrintStepHeader(1, 1, "Hot Reload Setup")

	var client *api.Client
	devMode, _ := cmd.Flags().GetBool("dev")

	authMgr := auth.NewManager()
	apiKey, tokenErr := authMgr.GetActiveToken()
	if tokenErr == nil && apiKey != "" {
		client = api.NewClientWithDevMode(apiKey, devMode)
	} else {
		ui.PrintDim("Not authenticated; skipping auto build selection during hot reload setup.")
		ui.PrintDim("Run 'revyl auth login' and re-run 'revyl init --hotreload' to auto-select a dev client build.")
	}

	ready := wizardHotReloadSetup(cmd.Context(), client, cfg, configPath, cwd, true)
	if !ready {
		return fmt.Errorf("hot reload setup failed")
	}

	ui.Println()
	ui.PrintSuccess("Hot reload setup complete.")

	testAlias := ""
	for alias := range cfg.Tests {
		testAlias = alias
		break
	}
	if testAlias != "" {
		ui.PrintInfo("Next: revyl test run %s --hotreload", testAlias)
	} else {
		ui.PrintInfo("Next: revyl test run <name> --hotreload")
	}

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
// Billing Check (post-auth)
// ---------------------------------------------------------------------------

// wizardBillingCheck checks whether the org has a billing plan attached. If
// not, it prompts the user to open the billing page in their browser to add a
// payment method. This is non-blocking — the user can skip and do it later.
func wizardBillingCheck(ctx context.Context, client *api.Client, devMode bool) {
	plan, err := client.GetBillingPlan(ctx)
	if err != nil {
		// Can't check — skip silently.
		return
	}

	// Enterprise/exempt orgs don't need self-serve billing.
	if plan.BillingExempt {
		return
	}

	// Already has a plan — no action needed.
	if plan.Plan != "none" && plan.Plan != "" {
		return
	}

	ui.Println()
	ui.PrintWarning("No payment method on file")
	ui.PrintInfo("Add a payment method to unlock 30 free simulator minutes per platform per month.")
	ui.PrintInfo("You won't be charged unless you exceed the free tier.")
	ui.Println()

	proceed, err := ui.PromptConfirm("Open billing page in browser?", true)
	if err != nil || !proceed {
		ui.PrintDim("You can add a payment method later: revyl auth billing")
		return
	}

	appURL := config.GetAppURL(devMode)
	billingURL := fmt.Sprintf("%s/settings?section=billing", appURL)

	if openErr := ui.OpenBrowser(billingURL); openErr != nil {
		ui.PrintInfo("Open this URL in your browser:")
		ui.PrintInfo("  %s", billingURL)
	} else {
		ui.PrintSuccess("Opened billing page in browser")
	}

	ui.PrintDim("Continue with the setup wizard while you add your payment method.")
	ui.Println()
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
// Step 4: Hot Reload Setup
// ---------------------------------------------------------------------------

// wizardHotReloadSetup detects and configures hot reload in .revyl/config.yaml.
// It applies smart defaults from project detection, preserves existing explicit
// settings, and optionally auto-selects a dev client build from configured
// platform app IDs.
func wizardHotReloadSetup(
	ctx context.Context,
	client *api.Client,
	cfg *config.ProjectConfig,
	configPath, cwd string,
	checkConnectivity bool,
) bool {
	registry := hotreload.DefaultRegistry()
	detections := registry.DetectAllProviders(cwd)

	if len(detections) == 0 {
		ui.PrintDim("No compatible hot reload providers detected in this project.")
		return true
	}

	detection, ok := selectHotReloadDetection(detections, cfg.HotReload.Default)
	if !ok {
		ui.PrintDim("Detected hot reload providers are not yet supported:")
		for _, d := range detections {
			ui.PrintDim("  • %s (coming soon)", d.Provider.DisplayName())
		}
		return true
	}

	setupResult, err := hotreload.AutoSetup(ctx, client, hotreload.SetupOptions{
		WorkDir:          cwd,
		ExplicitProvider: detection.Provider.Name(),
		Platform:         detection.Detection.Platform,
	})
	if err != nil {
		ui.PrintWarning("Could not configure hot reload: %v", err)
		return false
	}

	if cfg.HotReload.Providers == nil {
		cfg.HotReload.Providers = make(map[string]*config.ProviderConfig)
	}

	existingCfg := cfg.HotReload.GetProviderConfig(setupResult.ProviderName)
	mergedCfg := mergeHotReloadProviderConfig(existingCfg, setupResult.Config)
	cfg.HotReload.Providers[setupResult.ProviderName] = mergedCfg

	if cfg.HotReload.Default == "" {
		cfg.HotReload.Default = setupResult.ProviderName
	}

	if mergedCfg.AppScheme != "" {
		ui.PrintSuccess("Configured %s hot reload (scheme: %s)", detection.Provider.DisplayName(), mergedCfg.AppScheme)
	} else {
		ui.PrintSuccess("Configured %s hot reload", detection.Provider.DisplayName())
	}

	requestedPort := mergedCfg.GetPort(setupResult.ProviderName)
	activePort, portChanged := ensureAvailableHotReloadPort(mergedCfg, setupResult.ProviderName)
	if portChanged {
		ui.PrintWarning("Port %d is busy. Using port %d for hot reload.", requestedPort, activePort)
	}

	if client != nil {
		if mergedCfg.DevClientBuildID == "" {
			buildID, platformKey, resolveErr := resolveHotReloadBuildID(ctx, client, cfg)
			if resolveErr != nil {
				ui.PrintWarning("Could not auto-select a dev client build: %v", resolveErr)
			}
			if buildID != "" {
				mergedCfg.DevClientBuildID = buildID
				ui.PrintSuccess("Selected dev client build from platform %s: %s", platformKey, buildID)
			} else {
				ui.PrintDim("No build versions found yet. Upload a build to avoid passing --build-id.")
			}
		} else {
			ui.PrintDim("Using existing dev client build: %s", mergedCfg.DevClientBuildID)
		}
	}

	if checkConnectivity {
		connResult, connErr := hotreload.CheckConnectivity(ctx)
		if connErr != nil {
			ui.PrintWarning("Hot reload preflight skipped: %v", connErr)
		} else if suggestion := hotreload.DiagnoseAndSuggest(connResult); suggestion != "" {
			ui.PrintWarning("Hot reload network preflight found issues:")
			for _, line := range strings.Split(strings.TrimSpace(suggestion), "\n") {
				ui.PrintDim("  %s", line)
			}
		} else {
			ui.PrintSuccess("Hot reload network preflight passed")
		}
	}

	if err := config.WriteProjectConfig(configPath, cfg); err != nil {
		ui.PrintWarning("Failed to save hot reload configuration: %v", err)
		return false
	}

	return cfg.HotReload.IsConfigured()
}

// selectHotReloadDetection chooses which detected provider to configure.
func selectHotReloadDetection(detections []hotreload.ProviderDetection, defaultProvider string) (hotreload.ProviderDetection, bool) {
	if defaultProvider != "" {
		for _, d := range detections {
			if d.Provider.Name() == defaultProvider && d.Provider.IsSupported() {
				return d, true
			}
		}
	}

	for _, d := range detections {
		if d.Provider.IsSupported() {
			return d, true
		}
	}

	return hotreload.ProviderDetection{}, false
}

// mergeHotReloadProviderConfig merges auto-detected defaults with existing config.
// Existing explicit settings win.
func mergeHotReloadProviderConfig(existing, detected *config.ProviderConfig) *config.ProviderConfig {
	if detected == nil {
		detected = &config.ProviderConfig{}
	}
	if existing == nil {
		copyCfg := *detected
		return &copyCfg
	}

	merged := *existing
	if merged.DevClientBuildID == "" {
		merged.DevClientBuildID = detected.DevClientBuildID
	}
	if merged.Port == 0 {
		merged.Port = detected.Port
	}
	if merged.AppScheme == "" {
		merged.AppScheme = detected.AppScheme
	}
	if merged.BundleID == "" {
		merged.BundleID = detected.BundleID
	}
	if merged.InjectionPath == "" {
		merged.InjectionPath = detected.InjectionPath
	}
	if merged.ProjectPath == "" {
		merged.ProjectPath = detected.ProjectPath
	}
	if merged.PackageName == "" {
		merged.PackageName = detected.PackageName
	}

	return &merged
}

// ensureAvailableHotReloadPort keeps the configured/default port if available,
// otherwise selects the next free port in a small range.
func ensureAvailableHotReloadPort(providerCfg *config.ProviderConfig, providerName string) (int, bool) {
	port := providerCfg.GetPort(providerName)
	if isPortAvailable(port) {
		return port, false
	}

	nextPort := findAvailablePort(port+1, port+20)
	if nextPort == 0 {
		return port, false
	}

	providerCfg.Port = nextPort
	return nextPort, true
}

// resolveHotReloadBuildID finds a usable build from configured platform app IDs.
func resolveHotReloadBuildID(ctx context.Context, client *api.Client, cfg *config.ProjectConfig) (string, string, error) {
	var lookupErrors []string

	for _, platformKey := range orderedHotReloadPlatforms(cfg) {
		platformCfg, ok := cfg.Build.Platforms[platformKey]
		if !ok || platformCfg.AppID == "" {
			continue
		}

		latestVersion, err := client.GetLatestBuildVersion(ctx, platformCfg.AppID)
		if err != nil {
			lookupErrors = append(lookupErrors, fmt.Sprintf("%s: %v", platformKey, err))
			continue
		}
		if latestVersion != nil {
			return latestVersion.ID, platformKey, nil
		}
	}

	if len(lookupErrors) > 0 {
		return "", "", errors.New(strings.Join(lookupErrors, "; "))
	}

	return "", "", nil
}

// orderedHotReloadPlatforms returns platform keys sorted by dev-first priority.
func orderedHotReloadPlatforms(cfg *config.ProjectConfig) []string {
	keys := platformKeys(cfg)
	sort.Slice(keys, func(i, j int) bool {
		ri := hotReloadPlatformRank(keys[i])
		rj := hotReloadPlatformRank(keys[j])
		if ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	return keys
}

// hotReloadPlatformRank prioritizes platform keys likely to hold dev-client builds.
func hotReloadPlatformRank(name string) int {
	lower := strings.ToLower(name)

	switch {
	case strings.Contains(lower, "dev"):
		return 0
	case strings.Contains(lower, "preview"):
		return 1
	case strings.Contains(lower, "staging"):
		return 2
	case strings.Contains(lower, "qa"):
		return 3
	default:
		return 10
	}
}

// isPortAvailable checks if a TCP port can be bound on localhost.
func isPortAvailable(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// findAvailablePort returns the first available port in [start, end], or 0.
func findAvailablePort(start, end int) int {
	for p := start; p <= end; p++ {
		if isPortAvailable(p) {
			return p
		}
	}
	return 0
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
func printHotReloadInfo(cwd string, cfg *config.ProjectConfig) {
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
		if cfg != nil && cfg.HotReload.IsConfigured() {
			defaultProvider := cfg.HotReload.Default
			if defaultProvider == "" {
				defaultProvider = "auto"
			}
			ui.PrintSuccess("Hot reload configured during init (default: %s)", defaultProvider)
		} else {
			ui.PrintDim("Hot reload can be configured by re-running: revyl init")
			ui.PrintDim("Hot reload only mode: revyl init --hotreload")
		}
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
		testAlias := ""
		for alias := range cfg.Tests {
			testAlias = alias
			steps = append(steps, ui.NextStep{Label: "Run your test:", Command: fmt.Sprintf("revyl test run %s", alias)})
			break
		}
		if cfg.HotReload.IsConfigured() {
			if testAlias != "" {
				steps = append(steps, ui.NextStep{Label: "Run with hot reload:", Command: fmt.Sprintf("revyl test run %s --hotreload", testAlias)})
			} else {
				steps = append(steps, ui.NextStep{Label: "Run with hot reload:", Command: "revyl test run <name> --hotreload"})
			}
		}
	} else {
		steps = append(steps, ui.NextStep{Label: "Run a test:", Command: "revyl test run <name>"})
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
