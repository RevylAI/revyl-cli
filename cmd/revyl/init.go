// Package main provides the init command as a guided onboarding wizard.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/hotreload"
	_ "github.com/revyl/cli/internal/hotreload/providers" // Register providers
	syncpkg "github.com/revyl/cli/internal/sync"
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
		ui.PrintDim("Not authenticated; skipping app-link checks during hot reload setup.")
		ui.PrintDim("Run 'revyl auth login' and re-run 'revyl init --hotreload' to validate app links.")
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
		ui.PrintInfo("Next: revyl dev")
		ui.PrintInfo("Then: revyl dev test run %s", testAlias)
	} else {
		ui.PrintInfo("Next: revyl dev")
		ui.PrintInfo("Then: revyl dev test run <name>")
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
			OpenBrowser: func() *bool {
				v := true
				return &v
			}(),
			Timeout: 600,
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

	// For Expo, default to explicit dev/ci streams to avoid cross-contaminating
	// hot reload dev clients with CI/release uploads.
	configureExpoBuildStreams(cfg)

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

	if isExpoBuildSystem(cfg.Build.System) {
		wizardCreateExpoAppStreams(ctx, client, cfg, configPath)
		return
	}

	for _, platformKey := range platformKeys(cfg) {
		plat := cfg.Build.Platforms[platformKey]
		if plat.AppID != "" {
			ui.PrintDim("Platform %s already linked to app %s", platformKey, plat.AppID)
			continue
		}

		platform := mobilePlatformForBuildKey(platformKey)
		if platform == "" {
			ui.PrintWarning("Skipping %s: could not infer platform (ios/android)", platformKey)
			continue
		}

		fmt.Println(ui.TitleStyle.Render(fmt.Sprintf("Platform: %s", platformKey)))

		// Fetch existing apps for this platform.
		appsResp, err := client.ListApps(ctx, platform, 1, 10)
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
					nextResp, nextErr := client.ListApps(ctx, platform, page, 10)
					if nextErr != nil {
						ui.PrintWarning("Could not fetch more apps: %v", nextErr)
						continue
					}
					allApps = append(allApps, nextResp.Items...)
					hasMore = nextResp.HasNext
					continue
				}

				if idx == createNewIdx {
					appID = createAppInteractive(ctx, client, cfg.Project.Name, platform)
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
			appID = createAppInteractive(ctx, client, cfg.Project.Name, platform)
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
	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("%s-%s", defaultName, platform)
	}

	ui.StartSpinner("Creating app...")
	result, err := createOrLinkAppByName(ctx, client, name, platform)
	ui.StopSpinner()

	if err != nil {
		ui.PrintWarning("Failed to create app: %v", err)
		return ""
	}

	if result.LinkedExisting {
		ui.PrintSuccess("Linked to existing app %s (id: %s)", strings.TrimSpace(result.Name), result.ID)
		return result.ID
	}

	ui.PrintSuccess("Created app %s (id: %s)", result.Name, result.ID)
	return result.ID
}

func isExpoBuildSystem(system string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(system)), "expo")
}

func normalizeExpoBuildCommand(system, command string) (string, bool) {
	if !isExpoBuildSystem(system) {
		return command, false
	}
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return command, false
	}
	if strings.Contains(trimmed, "npx --yes eas-cli build") {
		return command, false
	}
	if strings.Contains(trimmed, "npx eas-cli build") {
		normalized := strings.ReplaceAll(trimmed, "npx eas-cli build", "npx --yes eas-cli build")
		return normalized, normalized != command
	}
	if strings.Contains(trimmed, "npx eas build") {
		normalized := strings.ReplaceAll(trimmed, "npx eas build", "npx --yes eas-cli build")
		return normalized, normalized != command
	}
	if !strings.Contains(trimmed, "eas build") {
		return command, false
	}
	normalized := strings.ReplaceAll(trimmed, "eas build", "npx --yes eas-cli build")
	return normalized, normalized != command
}

func defaultExpoBuildPlatforms() map[string]config.BuildPlatform {
	return map[string]config.BuildPlatform{
		"ios-dev": {
			Command: "npx --yes eas-cli build --platform ios --profile development --local --output build/dev-ios.tar.gz",
			Output:  "build/dev-ios.tar.gz",
		},
		"android-dev": {
			Command: "npx --yes eas-cli build --platform android --profile development --local --output build/dev-android.apk",
			Output:  "build/dev-android.apk",
		},
		"ios-ci": {
			Command: "npx --yes eas-cli build --platform ios --profile preview --local --output build/ci-ios.tar.gz",
			Output:  "build/ci-ios.tar.gz",
		},
		"android-ci": {
			Command: "npx --yes eas-cli build --platform android --profile preview --local --output build/ci-android.apk",
			Output:  "build/ci-android.apk",
		},
	}
}

// configureExpoBuildStreams ensures Expo projects have separate dev/ci build keys.
func configureExpoBuildStreams(cfg *config.ProjectConfig) {
	if cfg == nil || !isExpoBuildSystem(cfg.Build.System) {
		return
	}

	hasExplicitStreams := false
	hasLegacyPlatforms := false
	hasCustomPlatforms := false
	for key := range cfg.Build.Platforms {
		lower := strings.ToLower(strings.TrimSpace(key))
		if strings.Contains(lower, "dev") || strings.Contains(lower, "ci") {
			hasExplicitStreams = true
			break
		}
		if lower == "ios" || lower == "android" {
			hasLegacyPlatforms = true
			continue
		}
		hasCustomPlatforms = true
	}
	if hasExplicitStreams {
		return
	}
	if hasCustomPlatforms {
		return
	}
	if len(cfg.Build.Platforms) > 0 && !hasLegacyPlatforms {
		return
	}

	cfg.Build.Platforms = defaultExpoBuildPlatforms()
	if platformCfg, ok := cfg.Build.Platforms["ios-dev"]; ok {
		cfg.Build.Command = platformCfg.Command
		cfg.Build.Output = platformCfg.Output
	}
}

func mobilePlatformForBuildKey(platformKey string) string {
	key := strings.ToLower(strings.TrimSpace(platformKey))
	switch {
	case key == "ios", strings.Contains(key, "ios"):
		return "ios"
	case key == "android", strings.Contains(key, "android"):
		return "android"
	default:
		return ""
	}
}

func isDevBuildPlatformKey(platformKey string) bool {
	key := strings.ToLower(strings.TrimSpace(platformKey))
	return strings.Contains(key, "dev") || strings.Contains(key, "development")
}

func orderedExpoPlatformKeys(cfg *config.ProjectConfig) []string {
	keys := platformKeys(cfg)
	rank := func(key string) int {
		lower := strings.ToLower(strings.TrimSpace(key))
		switch {
		case lower == "ios-dev":
			return 0
		case lower == "android-dev":
			return 1
		case lower == "ios-ci":
			return 2
		case lower == "android-ci":
			return 3
		case strings.Contains(lower, "ios") && strings.Contains(lower, "dev"):
			return 4
		case strings.Contains(lower, "android") && strings.Contains(lower, "dev"):
			return 5
		case strings.Contains(lower, "ios"):
			return 6
		case strings.Contains(lower, "android"):
			return 7
		default:
			return 8
		}
	}

	sort.Slice(keys, func(i, j int) bool {
		if rank(keys[i]) != rank(keys[j]) {
			return rank(keys[i]) < rank(keys[j])
		}
		return keys[i] < keys[j]
	})
	return keys
}

func findAppIDByName(ctx context.Context, client *api.Client, platform, name string) (string, error) {
	target := canonicalAppName(name)
	if target == "" {
		return "", nil
	}

	page := 1
	for {
		appsResp, err := client.ListApps(ctx, platform, page, 100)
		if err != nil {
			return "", err
		}
		for _, app := range appsResp.Items {
			if canonicalAppName(app.Name) == target {
				return app.ID, nil
			}
		}
		if !appsResp.HasNext {
			break
		}
		page++
	}
	return "", nil
}

func canonicalAppName(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return ""
	}

	// Treat common separators as equivalent so conflict recovery can match
	// backend duplicate-name normalization.
	normalized := strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(lower)

	var b strings.Builder
	lastWasSpace := false
	for _, r := range normalized {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastWasSpace = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastWasSpace = false
		case r == ' ':
			if b.Len() > 0 && !lastWasSpace {
				b.WriteRune(' ')
				lastWasSpace = true
			}
		default:
			// Convert punctuation/noise into a single separator.
			if b.Len() > 0 && !lastWasSpace {
				b.WriteRune(' ')
				lastWasSpace = true
			}
		}
	}

	return strings.TrimSpace(b.String())
}

type createOrLinkAppResult struct {
	ID             string
	Name           string
	LinkedExisting bool
}

func isAppAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		errText := strings.ToLower(apiErr.Error())
		if apiErr.StatusCode == 409 {
			return true
		}
		if apiErr.StatusCode == 500 && strings.Contains(errText, "already exists") {
			return true
		}
	}

	return strings.Contains(strings.ToLower(err.Error()), "already exists")
}

func createOrLinkAppByName(ctx context.Context, client *api.Client, name, platform string) (*createOrLinkAppResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("app name cannot be empty")
	}

	resp, err := client.CreateApp(ctx, &api.CreateAppRequest{
		Name:     name,
		Platform: platform,
	})
	if err == nil {
		return &createOrLinkAppResult{
			ID:             resp.ID,
			Name:           strings.TrimSpace(resp.Name),
			LinkedExisting: false,
		}, nil
	}

	if !isAppAlreadyExistsError(err) {
		return nil, err
	}

	existingID, findErr := findAppIDByName(ctx, client, platform, name)
	if findErr != nil {
		return nil, fmt.Errorf("app already exists but lookup failed: %w", findErr)
	}
	if existingID == "" {
		return nil, err
	}

	return &createOrLinkAppResult{
		ID:             existingID,
		Name:           name,
		LinkedExisting: true,
	}, nil
}

func ensureNamedApp(ctx context.Context, client *api.Client, name, platform string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("app name cannot be empty")
	}

	if existingID, err := findAppIDByName(ctx, client, platform, name); err == nil && existingID != "" {
		return existingID, nil
	}

	result, err := createOrLinkAppByName(ctx, client, name, platform)
	if err != nil {
		return "", err
	}
	return result.ID, nil
}

func wizardCreateExpoAppStreams(ctx context.Context, client *api.Client, cfg *config.ProjectConfig, configPath string) {
	keys := orderedExpoPlatformKeys(cfg)
	if len(keys) == 0 {
		ui.PrintDim("No Expo build platforms detected — skipping app creation")
		return
	}

	ui.PrintInfo("Auto-linking Expo app streams for dev/ci...")
	for _, platformKey := range keys {
		plat := cfg.Build.Platforms[platformKey]
		if strings.TrimSpace(plat.AppID) != "" {
			ui.PrintDim("Platform %s already linked to app %s", platformKey, plat.AppID)
			continue
		}

		platform := mobilePlatformForBuildKey(platformKey)
		if platform == "" {
			ui.PrintWarning("Skipping %s: could not infer platform (ios/android)", platformKey)
			continue
		}

		appName := fmt.Sprintf("%s-%s", cfg.Project.Name, platformKey)
		appID, err := ensureNamedApp(ctx, client, appName, platform)
		if err != nil {
			ui.PrintWarning("Failed to link/create app for %s: %v", platformKey, err)
			continue
		}

		plat.AppID = appID
		cfg.Build.Platforms[platformKey] = plat
		_ = config.WriteProjectConfig(configPath, cfg)
		ui.PrintSuccess("Linked %s -> %s (%s)", platformKey, appName, appID)
	}
}

// ---------------------------------------------------------------------------
// Step 5: First Build
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

	if isExpoBuildSystem(cfg.Build.System) {
		devPlatforms := make([]string, 0, len(platforms))
		for _, key := range platforms {
			if isDevBuildPlatformKey(key) {
				devPlatforms = append(devPlatforms, key)
			}
		}
		if len(devPlatforms) > 0 {
			ui.PrintDim("Expo detected: focusing first build on dev streams (%s)", strings.Join(devPlatforms, ", "))
			ui.PrintDim("CI streams can be uploaded later with: revyl build upload --platform <ios-ci|android-ci>")
			wizardFirstBuildExpo(ctx, client, cfg, configPath, cwd, devPlatforms)
			return
		}
	}
	wizardFirstBuildSequential(ctx, client, cfg, configPath, cwd, platforms)
}

type wizardBuildResult struct {
	Platform   string
	AppID      string
	Version    string
	VersionID  string
	Err        error
	RetryLater string
}

func wizardFirstBuildExpo(
	ctx context.Context,
	client *api.Client,
	cfg *config.ProjectConfig,
	configPath string,
	cwd string,
	platforms []string,
) {
	eligible := make([]string, 0, len(platforms))
	for _, platform := range platforms {
		platformCfg, ok := cfg.Build.Platforms[platform]
		if !ok {
			continue
		}
		if normalized, changed := normalizeExpoBuildCommand(cfg.Build.System, platformCfg.Command); changed {
			platformCfg.Command = normalized
			cfg.Build.Platforms[platform] = platformCfg
			_ = config.WriteProjectConfig(configPath, cfg)
			ui.PrintDim("Updated %s build command to use npx eas", platform)
		}
		if strings.TrimSpace(platformCfg.AppID) == "" {
			ui.PrintDim("Skipping %s — no app linked (run revyl build upload --platform %s later)", platform, platform)
			continue
		}
		if strings.TrimSpace(platformCfg.Output) == "" {
			ui.PrintDim("Skipping %s — no output path configured in .revyl/config.yaml", platform)
			continue
		}
		eligible = append(eligible, platform)
	}

	if len(eligible) == 0 {
		ui.PrintDim("No Expo dev streams are ready to build yet")
		return
	}

	defaultTargets := defaultExpoDevBuildTargets(eligible)
	defaultTarget := ""
	if len(defaultTargets) > 0 {
		defaultTarget = defaultTargets[0]
	}
	iosTarget := bestExpoDevPlatformForMobile(eligible, "ios")
	androidTarget := bestExpoDevPlatformForMobile(eligible, "android")

	options := []ui.SelectOption{
		{Label: fmt.Sprintf("Build and upload default dev stream (fastest: %s)", defaultTarget), Value: "default"},
	}
	if iosTarget != "" {
		options = append(options, ui.SelectOption{
			Label: fmt.Sprintf("Build and upload iOS dev stream only (%s)", iosTarget),
			Value: "ios",
		})
	}
	if androidTarget != "" {
		options = append(options, ui.SelectOption{
			Label: fmt.Sprintf("Build and upload Android dev stream only (%s)", androidTarget),
			Value: "android",
		})
	}
	if iosTarget != "" && androidTarget != "" && iosTarget != androidTarget {
		options = append(options, ui.SelectOption{
			Label: "Build and upload both dev streams (parallel)",
			Value: "both",
		})
	}
	options = append(options,
		ui.SelectOption{Label: "Upload existing artifact(s)", Value: "upload"},
		ui.SelectOption{Label: "Skip for now", Value: "skip"},
	)

	_, selection, err := ui.Select("How would you like to handle dev streams?", options, 0)
	if err != nil || selection == "skip" {
		for _, platform := range eligible {
			ui.PrintDim("  Run later: revyl build upload --platform %s", platform)
		}
		return
	}

	uploadOnly := selection == "upload"
	selectedTargets := make([]string, 0, len(eligible))
	switch selection {
	case "default":
		selectedTargets = append(selectedTargets, defaultTargets...)
	case "ios":
		if iosTarget != "" {
			selectedTargets = append(selectedTargets, iosTarget)
		}
	case "android":
		if androidTarget != "" {
			selectedTargets = append(selectedTargets, androidTarget)
		}
	case "both":
		if iosTarget != "" {
			selectedTargets = append(selectedTargets, iosTarget)
		}
		if androidTarget != "" && androidTarget != iosTarget {
			selectedTargets = append(selectedTargets, androidTarget)
		}
	case "upload":
		selectedTargets = append(selectedTargets, eligible...)
	}
	if len(selectedTargets) == 0 {
		ui.PrintDim("No Expo dev streams selected")
		return
	}

	pending := append([]string(nil), selectedTargets...)
	primaryTarget := selectedTargets[0]

	for {
		if !uploadOnly {
			if ready := ensureExpoEASAuth(cwd); !ready {
				recoveryOptions := []ui.SelectOption{
					{Label: "Retry EAS auth check now"},
					{Label: "Continue onboarding and fix later"},
				}
				choice, _, choiceErr := ui.Select("Could not verify EAS login. What next?", recoveryOptions, 0)
				if choiceErr != nil || choice == 1 {
					for _, platform := range pending {
						ui.PrintDim("  Retry later: revyl build upload --platform %s", platform)
					}
					return
				}
				continue
			}
		}

		var batchResults []wizardBuildResult
		if uploadOnly {
			artifactPaths, prepResults := collectWizardUploadArtifacts(cwd, cfg, pending)
			targets := make([]string, 0, len(artifactPaths))
			for _, platform := range pending {
				if _, ok := artifactPaths[platform]; ok {
					targets = append(targets, platform)
				}
			}
			uploadResults := runWizardBuildBatch(ctx, client, cfg, cwd, targets, true, artifactPaths)
			batchResults = orderWizardBuildResults(append(prepResults, uploadResults...), pending)
		} else {
			batchResults = runWizardBuildBatch(ctx, client, cfg, cwd, pending, false, nil)
		}

		failed := printWizardBuildResults(batchResults)
		if len(failed) == 0 {
			if !uploadOnly {
				printExpoDeferredDevBuildHint(eligible, primaryTarget)
			}
			return
		}

		recoveryOptions := []ui.SelectOption{
			{Label: "Retry failed dev streams now"},
			{Label: "Continue onboarding and fix later"},
		}
		choice, _, choiceErr := ui.Select("Some dev streams failed. What next?", recoveryOptions, 0)
		if choiceErr != nil || choice == 1 {
			for _, platform := range failed {
				ui.PrintDim("  Retry later: revyl build upload --platform %s", platform)
			}
			return
		}
		pending = failed
	}
}

func bestExpoDevPlatformForMobile(platforms []string, mobile string) string {
	mobile = strings.ToLower(strings.TrimSpace(mobile))
	if mobile != "ios" && mobile != "android" {
		return ""
	}

	keys := append([]string(nil), platforms...)
	sort.Strings(keys)

	type candidate struct {
		key  string
		rank int
	}
	candidates := make([]candidate, 0, len(keys))
	for _, key := range keys {
		lower := strings.ToLower(strings.TrimSpace(key))
		if mobilePlatformForBuildKey(lower) != mobile {
			continue
		}

		rank := 50
		switch {
		case lower == mobile+"-dev":
			rank = 0
		case lower == mobile+"-development":
			rank = 1
		case strings.Contains(lower, mobile) && isDevBuildPlatformKey(lower):
			rank = 2
		case lower == mobile:
			rank = 3
		case strings.Contains(lower, mobile):
			rank = 4
		}
		candidates = append(candidates, candidate{key: key, rank: rank})
	}
	if len(candidates) == 0 {
		return ""
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].rank != candidates[j].rank {
			return candidates[i].rank < candidates[j].rank
		}
		return candidates[i].key < candidates[j].key
	})

	return candidates[0].key
}

func defaultExpoDevBuildTargetsForHost(platforms []string, hostOS string) []string {
	if len(platforms) == 0 {
		return nil
	}

	preferredMobile := "android"
	if strings.EqualFold(hostOS, "darwin") {
		preferredMobile = "ios"
	}

	if preferred := bestExpoDevPlatformForMobile(platforms, preferredMobile); preferred != "" {
		return []string{preferred}
	}

	secondaryMobile := "ios"
	if preferredMobile == "ios" {
		secondaryMobile = "android"
	}
	if fallback := bestExpoDevPlatformForMobile(platforms, secondaryMobile); fallback != "" {
		return []string{fallback}
	}

	ordered := append([]string(nil), platforms...)
	sort.Strings(ordered)
	return []string{ordered[0]}
}

func defaultExpoDevBuildTargets(platforms []string) []string {
	return defaultExpoDevBuildTargetsForHost(platforms, runtime.GOOS)
}

func printExpoDeferredDevBuildHint(eligible []string, builtPlatform string) {
	currentMobile := mobilePlatformForBuildKey(builtPlatform)
	if currentMobile == "" {
		return
	}

	nextMobile := "ios"
	if currentMobile == "ios" {
		nextMobile = "android"
	}
	nextPlatformKey := bestExpoDevPlatformForMobile(eligible, nextMobile)
	if nextPlatformKey == "" {
		return
	}

	ui.Println()
	ui.PrintDim("Optional next: revyl build upload --platform %s", nextPlatformKey)
}

func wizardFirstBuildSequential(
	ctx context.Context,
	client *api.Client,
	cfg *config.ProjectConfig,
	configPath string,
	cwd string,
	platforms []string,
) {
	for _, platform := range platforms {
		platformCfg, ok := cfg.Build.Platforms[platform]
		if !ok {
			continue
		}
		buildCommand := platformCfg.Command
		if normalized, changed := normalizeExpoBuildCommand(cfg.Build.System, platformCfg.Command); changed {
			buildCommand = normalized
			platformCfg.Command = normalized
			cfg.Build.Platforms[platform] = platformCfg
			_ = config.WriteProjectConfig(configPath, cfg)
			ui.PrintDim("Updated %s build command to use npx eas", platform)
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
			ui.PrintInfo("Building with: %s", buildCommand)
			ui.Println()

			startTime := time.Now()
			runner := build.NewRunner(cwd)

			buildErr := runner.Run(buildCommand, func(line string) {
				ui.PrintDim("  %s", line)
			})

			buildDuration = time.Since(startTime)

			if buildErr != nil {
				ui.Println()
				ui.PrintWarning("Build failed for %s: %v", platform, buildErr)
				var easErr *build.EASBuildError
				if errors.As(buildErr, &easErr) {
					ui.Println()
					ui.PrintInfo("How to fix:")
					ui.Println()
					for _, line := range strings.Split(strings.TrimSpace(easErr.Guidance), "\n") {
						ui.PrintDim("  %s", line)
					}
				}

				if strings.Contains(platformCfg.Command, "eas build") && !strings.Contains(platformCfg.Command, "npx eas") {
					ui.Println()
					ui.PrintInfo("Tip: use npx to avoid requiring global EAS CLI:")
					ui.PrintDim("  npx --yes eas-cli build ...")
					ui.PrintDim("  revyl build upload --platform %s", platform)
				}
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
		metadata := build.CollectMetadata(cwd, buildCommand, platform, buildDuration)
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

func collectWizardUploadArtifacts(cwd string, cfg *config.ProjectConfig, platforms []string) (map[string]string, []wizardBuildResult) {
	artifactPaths := make(map[string]string, len(platforms))
	prepResults := make([]wizardBuildResult, 0, len(platforms))

	for _, platform := range platforms {
		platformCfg, ok := cfg.Build.Platforms[platform]
		if !ok {
			prepResults = append(prepResults, wizardBuildResult{
				Platform:   platform,
				Err:        fmt.Errorf("platform %s is not configured", platform),
				RetryLater: fmt.Sprintf("revyl build upload --platform %s", platform),
			})
			continue
		}

		artifactPath, err := build.ResolveArtifactPath(cwd, platformCfg.Output)
		if err != nil {
			ui.PrintWarning("Artifact not found for %s at %s", platform, platformCfg.Output)
			customPath, promptErr := ui.Prompt(fmt.Sprintf("Enter path to %s artifact (or press Enter to skip):", platform))
			if promptErr != nil || strings.TrimSpace(customPath) == "" {
				prepResults = append(prepResults, wizardBuildResult{
					Platform:   platform,
					Err:        fmt.Errorf("artifact path not provided"),
					RetryLater: fmt.Sprintf("revyl build upload --platform %s", platform),
				})
				continue
			}

			artifactPath, err = build.ResolveArtifactPath(cwd, customPath)
			if err != nil {
				ui.PrintWarning("Artifact not found: %s", customPath)
				prepResults = append(prepResults, wizardBuildResult{
					Platform:   platform,
					Err:        fmt.Errorf("artifact not found: %s", customPath),
					RetryLater: fmt.Sprintf("revyl build upload --platform %s", platform),
				})
				continue
			}
		}

		artifactPaths[platform] = artifactPath
	}

	return artifactPaths, prepResults
}

func runWizardBuildBatch(
	ctx context.Context,
	client *api.Client,
	cfg *config.ProjectConfig,
	cwd string,
	platforms []string,
	skipBuild bool,
	artifactPaths map[string]string,
) []wizardBuildResult {
	if len(platforms) == 0 {
		return nil
	}

	type indexedResult struct {
		index  int
		result wizardBuildResult
	}

	resultsCh := make(chan indexedResult, len(platforms))
	var wg sync.WaitGroup
	var outputMu sync.Mutex

	for i, platform := range platforms {
		platformCfg, ok := cfg.Build.Platforms[platform]
		if !ok {
			resultsCh <- indexedResult{
				index: i,
				result: wizardBuildResult{
					Platform:   platform,
					Err:        fmt.Errorf("platform %s is not configured", platform),
					RetryLater: fmt.Sprintf("revyl build upload --platform %s", platform),
				},
			}
			continue
		}

		providedArtifactPath := ""
		if artifactPaths != nil {
			providedArtifactPath = strings.TrimSpace(artifactPaths[platform])
		}

		wg.Add(1)
		go func(resultIndex int, platform string, platformCfg config.BuildPlatform, artifactPath string) {
			defer wg.Done()
			resultsCh <- indexedResult{
				index:  resultIndex,
				result: runWizardBuildForPlatform(ctx, client, cwd, platform, platformCfg, skipBuild, artifactPath, &outputMu),
			}
		}(i, platform, platformCfg, providedArtifactPath)
	}

	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	ordered := make([]wizardBuildResult, len(platforms))
	for item := range resultsCh {
		ordered[item.index] = item.result
	}

	return ordered
}

func runWizardBuildForPlatform(
	ctx context.Context,
	client *api.Client,
	cwd string,
	platform string,
	platformCfg config.BuildPlatform,
	skipBuild bool,
	preResolvedArtifactPath string,
	outputMu *sync.Mutex,
) wizardBuildResult {
	result := wizardBuildResult{
		Platform:   platform,
		AppID:      strings.TrimSpace(platformCfg.AppID),
		RetryLater: fmt.Sprintf("revyl build upload --platform %s", platform),
	}

	if result.AppID == "" {
		result.Err = fmt.Errorf("no app linked for %s", platform)
		return result
	}

	buildCommand := strings.TrimSpace(platformCfg.Command)
	var buildDuration time.Duration
	if !skipBuild {
		if buildCommand == "" {
			result.Err = fmt.Errorf("no build command configured for %s", platform)
			return result
		}

		outputMu.Lock()
		ui.PrintInfo("[%s] Building with: %s", platform, buildCommand)
		outputMu.Unlock()

		startTime := time.Now()
		runner := build.NewRunner(cwd)
		err := runner.Run(buildCommand, func(line string) {
			outputMu.Lock()
			ui.PrintDim("  [%s] %s", platform, line)
			outputMu.Unlock()
		})
		buildDuration = time.Since(startTime)

		if err != nil {
			result.Err = err
			return result
		}

		outputMu.Lock()
		ui.PrintSuccess("[%s] Build completed in %s", platform, buildDuration.Round(time.Second))
		outputMu.Unlock()
	}

	artifactPath := strings.TrimSpace(preResolvedArtifactPath)
	if artifactPath == "" {
		resolved, err := build.ResolveArtifactPath(cwd, platformCfg.Output)
		if err != nil {
			result.Err = fmt.Errorf("artifact not found for %s: %w", platform, err)
			return result
		}
		artifactPath = resolved
	}

	if build.IsTarGz(artifactPath) {
		outputMu.Lock()
		ui.PrintInfo("[%s] Extracting .app from tar.gz...", platform)
		outputMu.Unlock()
		zipPath, err := build.ExtractAppFromTarGz(artifactPath)
		if err != nil {
			result.Err = fmt.Errorf("failed to extract .app from tar.gz: %w", err)
			return result
		}
		defer os.Remove(zipPath)
		artifactPath = zipPath
		outputMu.Lock()
		ui.PrintSuccess("[%s] Converted to: %s", platform, filepath.Base(zipPath))
		outputMu.Unlock()
	} else if build.IsAppBundle(artifactPath) {
		outputMu.Lock()
		ui.PrintInfo("[%s] Zipping .app bundle...", platform)
		outputMu.Unlock()
		zipPath, err := build.ZipAppBundle(artifactPath)
		if err != nil {
			result.Err = fmt.Errorf("failed to zip .app bundle: %w", err)
			return result
		}
		defer os.Remove(zipPath)
		artifactPath = zipPath
		outputMu.Lock()
		ui.PrintSuccess("[%s] Created: %s", platform, filepath.Base(zipPath))
		outputMu.Unlock()
	}

	versionStr := build.GenerateVersionString()
	metadata := build.CollectMetadata(cwd, buildCommand, platform, buildDuration)

	outputMu.Lock()
	ui.PrintInfo("[%s] Uploading: %s", platform, filepath.Base(artifactPath))
	ui.PrintInfo("[%s] Build Version: %s", platform, versionStr)
	outputMu.Unlock()

	uploadResult, err := client.UploadBuild(ctx, &api.UploadBuildRequest{
		AppID:        result.AppID,
		Version:      versionStr,
		FilePath:     artifactPath,
		Metadata:     metadata,
		SetAsCurrent: true,
	})
	if err != nil {
		result.Err = fmt.Errorf("upload failed: %w", err)
		return result
	}

	result.Version = uploadResult.Version
	result.VersionID = uploadResult.VersionID
	return result
}

func orderWizardBuildResults(results []wizardBuildResult, order []string) []wizardBuildResult {
	if len(results) == 0 || len(order) == 0 {
		return results
	}

	byPlatform := make(map[string]wizardBuildResult, len(results))
	for _, result := range results {
		byPlatform[result.Platform] = result
	}

	ordered := make([]wizardBuildResult, 0, len(results))
	for _, platform := range order {
		if result, ok := byPlatform[platform]; ok {
			ordered = append(ordered, result)
			delete(byPlatform, platform)
		}
	}
	if len(byPlatform) > 0 {
		remaining := make([]string, 0, len(byPlatform))
		for platform := range byPlatform {
			remaining = append(remaining, platform)
		}
		sort.Strings(remaining)
		for _, platform := range remaining {
			ordered = append(ordered, byPlatform[platform])
		}
	}

	return ordered
}

func printWizardBuildResults(results []wizardBuildResult) []string {
	ui.Println()
	ui.PrintInfo("Dev stream build results:")
	ui.Println()

	failed := make([]string, 0)
	for _, result := range results {
		if result.Err != nil {
			ui.PrintWarning("[%s] Failed: %v", result.Platform, result.Err)
			var easErr *build.EASBuildError
			if errors.As(result.Err, &easErr) {
				ui.PrintInfo("  Fix suggestion:")
				for _, line := range strings.Split(strings.TrimSpace(easErr.Guidance), "\n") {
					ui.PrintDim("    %s", line)
				}
			}
			if result.RetryLater != "" {
				ui.PrintDim("  %s", result.RetryLater)
			}
			failed = append(failed, result.Platform)
			continue
		}

		ui.PrintSuccess("[%s] Upload complete", result.Platform)
		if result.AppID != "" {
			ui.PrintInfo("  App: %s", result.AppID)
		}
		if result.Version != "" {
			ui.PrintInfo("  Build Version: %s", result.Version)
		}
		if result.VersionID != "" {
			ui.PrintInfo("  Build ID: %s", result.VersionID)
		}
	}

	return failed
}

// ---------------------------------------------------------------------------
// Step 6: Create First Test
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

	// Select runtime platform (ios/android) from configured build keys.
	platforms := selectableRuntimePlatforms(cfg)
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
	appID := resolveAppIDForRuntimePlatform(cfg, platform)

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
// settings, and auto-maps ios/android to build.platforms keys when possible.
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
	mergedCfg.PlatformKeys = mergePlatformKeys(mergedCfg.PlatformKeys, inferHotReloadPlatformKeys(cfg))
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

	for _, platform := range []string{"ios", "android"} {
		if platformKey := strings.TrimSpace(mergedCfg.PlatformKeys[platform]); platformKey != "" {
			if _, ok := cfg.Build.Platforms[platformKey]; ok {
				ui.PrintDim("Mapped %s hot reload to build.platforms.%s", platform, platformKey)
			}
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
		if len(detected.PlatformKeys) > 0 {
			copyCfg.PlatformKeys = make(map[string]string, len(detected.PlatformKeys))
			for k, v := range detected.PlatformKeys {
				copyCfg.PlatformKeys[k] = v
			}
		}
		return &copyCfg
	}

	merged := *existing
	merged.PlatformKeys = mergePlatformKeys(existing.PlatformKeys, detected.PlatformKeys)
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
	sort.Strings(keys)
	return keys
}

func selectableRuntimePlatforms(cfg *config.ProjectConfig) []string {
	if cfg == nil {
		return nil
	}
	set := make(map[string]struct{})
	for key := range cfg.Build.Platforms {
		if platform := mobilePlatformForBuildKey(key); platform != "" {
			set[platform] = struct{}{}
		}
	}
	if len(set) == 0 {
		return nil
	}
	platforms := make([]string, 0, len(set))
	for _, preferred := range []string{"ios", "android"} {
		if _, ok := set[preferred]; ok {
			platforms = append(platforms, preferred)
			delete(set, preferred)
		}
	}
	if len(set) > 0 {
		rest := make([]string, 0, len(set))
		for platform := range set {
			rest = append(rest, platform)
		}
		sort.Strings(rest)
		platforms = append(platforms, rest...)
	}
	return platforms
}

func resolveAppIDForRuntimePlatform(cfg *config.ProjectConfig, runtimePlatform string) string {
	if cfg == nil {
		return ""
	}
	runtimePlatform = strings.ToLower(strings.TrimSpace(runtimePlatform))
	if runtimePlatform == "" {
		return ""
	}

	// Prefer explicit hotreload platform mapping when present.
	if expoCfg := cfg.HotReload.GetProviderConfig("expo"); expoCfg != nil {
		if mappedKey := strings.TrimSpace(expoCfg.PlatformKeys[runtimePlatform]); mappedKey != "" {
			if mapped, ok := cfg.Build.Platforms[mappedKey]; ok && strings.TrimSpace(mapped.AppID) != "" {
				return strings.TrimSpace(mapped.AppID)
			}
		}
	}

	// Fallback to best matching build key (prefers *-dev).
	if bestKey := pickBestBuildPlatformKey(cfg, runtimePlatform); bestKey != "" {
		if platformCfg, ok := cfg.Build.Platforms[bestKey]; ok && strings.TrimSpace(platformCfg.AppID) != "" {
			return strings.TrimSpace(platformCfg.AppID)
		}
	}

	// Final fallback to direct key match.
	if platformCfg, ok := cfg.Build.Platforms[runtimePlatform]; ok {
		return strings.TrimSpace(platformCfg.AppID)
	}

	return ""
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
			steps = append(steps, ui.NextStep{Label: "Start dev loop:", Command: "revyl dev"})
			if testAlias != "" {
				steps = append(steps, ui.NextStep{Label: "Run test in dev loop:", Command: fmt.Sprintf("revyl dev test run %s", testAlias)})
			} else {
				steps = append(steps, ui.NextStep{Label: "Run test in dev loop:", Command: "revyl dev test run <name>"})
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
	resolver := syncpkg.NewResolver(client, cfg, localTests)
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
