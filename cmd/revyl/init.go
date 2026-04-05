// Package main provides the init command as a guided onboarding wizard.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
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
	Long: `Initialize a Revyl project in the current directory.

Detects your build system, writes .revyl/config.yaml, then optionally
walks you through authentication, app creation, and first build.
You can exit at any point — the config is saved after each step.

Use -y to skip all prompts and just create the config file.

Examples:
  revyl init                    # Detect, create config, continue setup
  revyl init -y                 # Non-interactive: detect + create config only
  revyl init --provider expo    # Force Expo as hot reload provider
  revyl init --project ID       # Link to existing Revyl project
  revyl init --detect           # Re-run build system detection
  revyl init --force            # Overwrite existing configuration`,
	RunE: runInit,
}

var (
	initProjectID            string
	initDetect               bool
	initForce                bool
	initNonInteractive       bool
	initHotReloadAppScheme   string
	initHotReloadProvider    string
	initXcodeSchemeOverrides []string
)

func init() {
	initCmd.Flags().StringVar(&initProjectID, "project", "", "Link to existing Revyl project ID")
	initCmd.Flags().BoolVar(&initDetect, "detect", false, "Re-run build system detection")
	initCmd.Flags().BoolVar(&initForce, "force", false, "Overwrite existing configuration")
	initCmd.Flags().BoolVarP(&initNonInteractive, "non-interactive", "y", false, "Skip wizard prompts, just create config")
	initCmd.Flags().StringVar(&initHotReloadAppScheme, "hotreload-app-scheme", "", "Override Expo hotreload.providers.expo.app_scheme")
	initCmd.Flags().StringVar(&initHotReloadProvider, "provider", "", "Force dev mode provider (expo, react-native, swift, android)")
	initCmd.Flags().StringSliceVar(&initXcodeSchemeOverrides, "xcode-scheme", nil, "Override Xcode scheme by build platform key (format: key=Scheme, repeatable)")
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

	overrideOpts, err := newInitOverrideOptions(initXcodeSchemeOverrides, initHotReloadAppScheme, !initNonInteractive)
	if err != nil {
		return err
	}
	revylDir := filepath.Join(cwd, ".revyl")
	configPath := filepath.Join(revylDir, "config.yaml")
	configExists := false
	if _, err := os.Stat(configPath); err == nil {
		configExists = true
	}

	if configExists && !initForce && !initDetect {
		ui.PrintWarning("Project already initialized")
		ui.PrintInfo("Use --force to overwrite or --detect to re-run detection")
		return nil
	}

	devMode, _ := cmd.Flags().GetBool("dev")

	ui.PrintSectionHeader("Project Setup")

	cfg, err := wizardProjectSetup(cwd, revylDir, configPath, overrideOpts)
	if err != nil {
		return err
	}

	// In non-interactive mode we stop after creating the config.
	if initNonInteractive {
		ui.PrintDim("You can edit settings anytime in .revyl/config.yaml")
		wizardHotReloadSetup(context.Background(), nil, cfg, configPath, cwd, false, overrideOpts, initHotReloadProvider)
		printCreatedFiles()
		printHotReloadInfo(cwd, cfg)
		printInitNextSteps(cfg)
		return nil
	}

	// Progressive menu: continue, edit, or exit.
	continueToAuth := false
	for {
		options := []string{
			"Continue to authentication and setup",
			"Edit build settings",
			"Finish setup",
		}
		selection, selErr := ui.PromptSelect("What would you like to do?", options)
		if selErr != nil {
			break
		}

		switch selection {
		case 0:
			continueToAuth = true
		case 1:
			ui.Println()
			promptBuildSetupReview(cfg)
			promptForXcodeSchemeEdits(cfg)
			if writeErr := config.WriteProjectConfig(configPath, cfg); writeErr != nil {
				ui.PrintWarning("Could not save: %v", writeErr)
			} else {
				ui.PrintSuccess("Saved to .revyl/config.yaml")
			}
			ui.Println()
			continue
		}
		break
	}

	if !continueToAuth {
		wizardHotReloadSetup(context.Background(), nil, cfg, configPath, cwd, false, overrideOpts, initHotReloadProvider)
		printInitSummary(cfg)
		printCreatedFiles()
		printHotReloadInfo(cwd, cfg)
		printInitNextSteps(cfg)
		return nil
	}

	// ── Authentication ───────────────────────────────────────────────────
	ui.PrintSectionHeader("Authentication")

	ctx := context.Background()
	client, userInfo, authOK := wizardAuth(ctx, devMode, cfg.Project.OrgID)

	if !authOK {
		wizardHotReloadSetup(context.Background(), nil, cfg, configPath, cwd, false, overrideOpts, initHotReloadProvider)
		ui.Println()
		ui.PrintWarning("Skipping remaining steps (require authentication)")
		ui.Println()
		printCreatedFiles()
		printHotReloadInfo(cwd, cfg)
		printInitNextSteps(cfg)
		return nil
	}

	// Bind the project to the authenticated organization when available.
	// When the org changes, clear stale app_ids that belong to the previous
	// org so wizardCreateApps re-creates them under the correct org.
	if userInfo != nil {
		orgID := strings.TrimSpace(userInfo.OrgID)
		if orgID != "" && cfg.Project.OrgID != orgID {
			previousOrgID := cfg.Project.OrgID
			cfg.Project.OrgID = orgID
			if previousOrgID != "" {
				cleared := 0
				for key, plat := range cfg.Build.Platforms {
					if plat.AppID != "" {
						plat.AppID = ""
						cfg.Build.Platforms[key] = plat
						cleared++
					}
				}
				if cleared > 0 {
					ui.PrintInfo("Org changed (%s → %s) — cleared %d stale app link(s)", previousOrgID, orgID, cleared)
				}
			}
			if err := config.WriteProjectConfig(configPath, cfg); err != nil {
				ui.PrintWarning("Could not persist project org binding: %v", err)
			}
		}
	}

	// ── Billing check (between auth and app creation) ──────────────────
	wizardBillingCheck(ctx, client, devMode)

	// ── Create Apps ──────────────────────────────────────────────────────
	ui.PrintSectionHeader("Create Apps")

	wizardCreateApps(ctx, client, cfg, configPath)

	// ── Dev Loop ─────────────────────────────────────────────────────────
	ui.PrintSectionHeader("Dev Loop")
	hotReloadReady := wizardHotReloadSetup(ctx, client, cfg, configPath, cwd, true, overrideOpts, initHotReloadProvider)

	// Determine if any apps were linked.
	appsLinked := false
	for _, plat := range cfg.Build.Platforms {
		if plat.AppID != "" {
			appsLinked = true
			break
		}
	}

	// ── First Build ──────────────────────────────────────────────────────
	ui.PrintSectionHeader("First Build")
	wizardFirstBuild(ctx, client, cfg, configPath)

	// ── What's Next ──────────────────────────────────────────────────────
	ui.PrintSectionHeader("What's Next")

	var testID, testName string
	launchDevice := false

	if cfg.Project.Name != "" {
		ui.PrintDim("Project: %s", cfg.Project.Name)
	}

	whatsNextOptions := []ui.SelectOption{
		{
			Label:       "Start a live dev session",
			Value:       "dev",
			Description: "Opens a cloud device with your app installed and streams it to your browser",
		},
		{
			Label:       "Create a test",
			Value:       "test",
			Description: "Write a test in natural language that Revyl runs automatically",
		},
		{
			Label:       "Skip for now",
			Value:       "skip",
			Description: "Finish setup; run revyl dev or revyl test create later",
		},
	}

	_, selection, err := ui.Select("What would you like to do?", whatsNextOptions, 0)
	if err == nil {
		switch selection {
		case "dev":
			launchDevice = true
		case "test":
			testID, testName = wizardCreateTest(ctx, client, cfg, configPath, devMode, userInfo)
		default:
			ui.PrintDim("You can always run these later:")
			ui.PrintDim("  revyl dev              — start a live device")
			ui.PrintDim("  revyl test create      — create a test")
		}
	}

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

	// Launch live device session if selected (after summary so user sees it)
	if launchDevice {
		ui.Println()
		ui.PrintInfo("Starting live device session...")
		ui.Println()
		wizardLaunchDevice(ctx, cfg, devMode)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Step 1: Project Setup
// ---------------------------------------------------------------------------

// wizardProjectSetup detects the build system, creates .revyl/ directory, and
// writes the initial config.yaml.
func wizardProjectSetup(cwd, revylDir, configPath string, overrideOpts *initOverrideOptions) (*config.ProjectConfig, error) {
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
		if len(detected.Platforms) > 0 {
			var platNames []string
			for k := range detected.Platforms {
				platNames = append(platNames, k)
			}
			sort.Strings(platNames)
			ui.PrintInfo("  Platforms: %s", strings.Join(platNames, ", "))
			ui.Println()
			ui.PrintDim("  Build settings:")
			for _, name := range platNames {
				bp := detected.Platforms[name]
				ui.PrintDim("  %s: %s", name, bp.Command)
				if bp.Output != "" {
					ui.PrintDim("  %s output: %s", name, bp.Output)
				}
			}
		} else if detected.Command != "" {
			ui.PrintInfo("  Build: %s", detected.Command)
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

	// For Xcode/React Native iOS platforms with -scheme *, prompt user to pick a scheme
	for platformKey, platformCfg := range cfg.Build.Platforms {
		if strings.Contains(platformCfg.Command, "-scheme *") {
			allowPrompts := true
			if overrideOpts != nil {
				allowPrompts = overrideOpts.AllowInteractivePrompts
			}

			scheme := promptXcodeScheme(cwd, platformKey, allowPrompts)
			if scheme != "" {
				platformCfg = setBuildPlatformScheme(platformCfg, scheme)
				cfg.Build.Platforms[platformKey] = platformCfg
			}
		}
	}

	// For Expo, default to explicit dev/ci streams to avoid cross-contaminating
	// hot reload dev clients with CI/release uploads.
	configureExpoBuildStreams(cfg, cwd)

	// Validate EAS profiles for iOS simulator builds after configuring streams
	validateAndFixEASProfiles(cfg, cwd)

	if overrideOpts != nil {
		if err := applyXcodeSchemeOverrides(cfg, overrideOpts.XcodeSchemeOverrides); err != nil {
			return nil, err
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

# Device session cache (local machine state)
device-sessions.json
device-session.json
device.json

# Generated per-machine runtime files
remote.json
shell-init.sh
.services.pid
.dev.pid

# MCP session artifacts (screenshots, etc.)
mcp/
`
	if err := os.WriteFile(gitignorePath, []byte(gitignoreContent), 0644); err != nil {
		ui.PrintWarning("Failed to create .gitignore: %v", err)
	}

	ui.PrintSuccess("Project config created: .revyl/config.yaml")

	return cfg, nil
}

// ---------------------------------------------------------------------------
// Step 2: Authentication
// ---------------------------------------------------------------------------

// wizardAuth checks for existing credentials and, if missing, walks the user
// through browser-based login. Returns an API client, validated user info,
// and a boolean indicating whether auth succeeded.
//
// Parameters:
//   - ctx: Context for cancellation and API calls
//   - devMode: Whether to use local development servers
//   - configOrgID: The org_id from the existing project config (may be empty for new projects)
func wizardAuth(ctx context.Context, devMode bool, configOrgID string) (*api.Client, *api.ValidateAPIKeyResponse, bool) {
	mgr := auth.NewManager()

	// Track whether we have a validated fallback from existing credentials.
	var fallbackClient *api.Client
	var fallbackUserInfo *api.ValidateAPIKeyResponse
	wantsSwitch := false

	// Check existing credentials first.
	token, _ := mgr.GetActiveToken()
	if token != "" {
		ui.PrintDim("Found existing credentials, validating...")
		client := api.NewClientWithDevMode(token, devMode)
		userInfo, err := client.ValidateAPIKey(ctx)
		if err == nil {
			envOverride := os.Getenv("REVYL_API_KEY") != ""
			orgMismatch := configOrgID != "" && userInfo.OrgID != "" && configOrgID != userInfo.OrgID

			if orgMismatch {
				ui.Println()
				if envOverride {
					ui.PrintWarning("REVYL_API_KEY env var is set — it overrides stored credentials")
				} else {
					ui.PrintWarning("Org mismatch: credentials belong to a different org than this project")
				}
				ui.PrintDim("  Authenticated org:  %s (%s)", userInfo.OrgName, userInfo.OrgID)
				ui.PrintDim("  Config org:         %s", configOrgID)
				ui.PrintDim("  Continuing will rebind this project and clear existing app links.")
				ui.Println()
			}

			ui.PrintSuccess("Authenticated as %s", userInfo.Email)
			if envOverride {
				ui.PrintDim("  Auth source: REVYL_API_KEY environment variable")
			}
			options := []string{
				fmt.Sprintf("Continue as %s", userInfo.Email),
				"Switch to a different account",
			}
			selection, selErr := ui.PromptSelect("", options)
			if selErr != nil || selection == 0 {
				return client, userInfo, true
			}
			fallbackClient = client
			fallbackUserInfo = userInfo
			wantsSwitch = true
			ui.PrintDim("Opening browser to log in with a different account...")
		} else {
			ui.PrintWarning("Existing credentials invalid, need to re-authenticate")
		}
	}

	// Only prompt for confirmation when the user hasn't already expressed
	// intent to switch. "Switch to a different account" implies browser login.
	if !wantsSwitch {
		proceed, err := ui.PromptConfirm("Log in via browser?", true)
		if err != nil || !proceed {
			ui.PrintWarning("Authentication skipped")
			return nil, nil, false
		}
	}

	ui.StartSpinner("Waiting for browser authentication...")

	clientInstanceID, err := mgr.GetOrCreateClientInstanceID()
	if err != nil {
		ui.StopSpinner()
		ui.PrintWarning("Failed to prepare local CLI identity: %v", err)
		if fallbackClient != nil {
			return wizardAuthFallback(fallbackClient, fallbackUserInfo)
		}
		return nil, nil, false
	}

	browserAuth := auth.NewBrowserAuth(auth.BrowserAuthConfig{
		AppURL:           config.GetAppURL(devMode),
		Timeout:          5 * time.Minute,
		ClientInstanceID: clientInstanceID,
		DeviceLabel:      auth.CurrentDeviceLabel(),
	})

	result, err := browserAuth.Authenticate(ctx)
	ui.StopSpinner()

	if err != nil {
		ui.PrintWarning("Authentication failed: %v", err)
		if fallbackClient != nil {
			return wizardAuthFallback(fallbackClient, fallbackUserInfo)
		}
		return nil, nil, false
	}

	if result.Error != "" {
		ui.PrintWarning("Authentication error: %s", result.Error)
		if fallbackClient != nil {
			return wizardAuthFallback(fallbackClient, fallbackUserInfo)
		}
		return nil, nil, false
	}

	// Save credentials.
	isPersistentKey := result.AuthMethod == "api_key" && result.APIKeyID != ""
	if isPersistentKey {
		err = mgr.SaveBrowserAPIKeyCredentials(result, result.APIKeyID)
	} else {
		err = mgr.SaveBrowserCredentials(result, defaultTokenExpiration)
	}
	if err != nil {
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
	if isPersistentKey {
		_ = mgr.SaveBrowserAPIKeyCredentials(
			&auth.BrowserAuthResult{
				Token:      result.Token,
				Email:      userInfo.Email,
				OrgID:      userInfo.OrgID,
				UserID:     userInfo.UserID,
				APIKeyID:   result.APIKeyID,
				AuthMethod: result.AuthMethod,
			},
			result.APIKeyID,
		)
	} else {
		_ = mgr.SaveBrowserCredentials(&auth.BrowserAuthResult{
			Token:  result.Token,
			Email:  userInfo.Email,
			OrgID:  userInfo.OrgID,
			UserID: userInfo.UserID,
		}, defaultTokenExpiration)
	}

	ui.PrintSuccess("Authenticated as %s", userInfo.Email)
	return client, userInfo, true
}

// wizardAuthFallback prompts the user to fall back to previously validated
// credentials after a browser login attempt fails.
//
// Parameters:
//   - client: The already-validated API client from existing credentials
//   - userInfo: The validated user info from existing credentials
//
// Returns:
//   - The original client/userInfo/true if the user chooses to fall back,
//     or nil/nil/false if they choose to skip authentication
func wizardAuthFallback(client *api.Client, userInfo *api.ValidateAPIKeyResponse) (*api.Client, *api.ValidateAPIKeyResponse, bool) {
	options := []string{
		fmt.Sprintf("Continue as %s", userInfo.Email),
		"Skip authentication",
	}
	selection, selErr := ui.PromptSelect("Fall back to existing credentials?", options)
	if selErr != nil {
		ui.PrintDim("Falling back to %s (prompt interrupted)", userInfo.Email)
		return client, userInfo, true
	}
	if selection == 0 {
		ui.PrintSuccess("Continuing as %s", userInfo.Email)
		return client, userInfo, true
	}
	ui.PrintWarning("Authentication skipped")
	return nil, nil, false
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

// promptXcodeScheme attempts to discover Xcode schemes and lets the user pick one.
// Returns the selected scheme name, or empty string if none was selected.
func promptXcodeScheme(cwd, platformKey string, allowPrompt bool) string {
	// Try discovering schemes in cwd first, then ios/ subdirectory (React Native layout)
	var schemes []string
	var err error

	schemes, err = build.ListXcodeSchemes(cwd)
	if err != nil || len(schemes) == 0 {
		iosDir := filepath.Join(cwd, "ios")
		if build.DirExists(iosDir) {
			schemes, _ = build.ListXcodeSchemes(iosDir)
		}
	}

	if len(schemes) == 0 {
		if !allowPrompt {
			return ""
		}
		// Discovery failed — fall back to manual input
		ui.PrintWarning("Could not auto-detect Xcode schemes for %s", platformKey)
		name, err := ui.Prompt("Enter scheme name (or press Enter to skip):")
		if err != nil || strings.TrimSpace(name) == "" {
			return ""
		}
		return strings.TrimSpace(name)
	}

	if len(schemes) == 1 {
		ui.PrintSuccess("Auto-selected Xcode scheme: %s", schemes[0])
		return schemes[0]
	}

	if !allowPrompt {
		ui.PrintDim("Multiple Xcode schemes found for %s; defaulting to %s in non-interactive mode", platformKey, schemes[0])
		return schemes[0]
	}

	// Multiple schemes — let user pick
	ui.PrintInfo("Multiple Xcode schemes found for %s:", platformKey)
	idx, err := ui.PromptSelect("Select scheme:", schemes)
	if err != nil {
		return ""
	}
	return schemes[idx]
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

func defaultExpoBuildPlatforms(dir string) map[string]config.BuildPlatform {
	iosProfile := "development"

	easCfg, err := build.LoadEASConfig(dir)
	if err == nil && easCfg != nil {
		if p := easCfg.FindDevSimulatorProfile(); p != "" {
			iosProfile = p
		}
	}

	return map[string]config.BuildPlatform{
		"ios": {
			Command: fmt.Sprintf("npx --yes eas-cli build --platform ios --profile %s --local --output build/app.tar.gz", iosProfile),
			Output:  "build/app.tar.gz",
		},
		"android": {
			Command: "npx --yes eas-cli build --platform android --profile development --local --output build/app.apk",
			Output:  "build/app.apk",
		},
	}
}

// configureExpoBuildStreams sets up Expo build platforms using development profile.
//
// Uses 2 platform keys (ios, android) with the development EAS profile.
// A single development build supports both hot reload and regular testing.
// CI-optimized builds (preview profile) can be added later via
// `revyl config add-ci-profile`.
func configureExpoBuildStreams(cfg *config.ProjectConfig, cwd string) {
	if cfg == nil || !isExpoBuildSystem(cfg.Build.System) {
		return
	}

	hasCustomPlatforms := false
	for key := range cfg.Build.Platforms {
		lower := strings.ToLower(strings.TrimSpace(key))
		if lower != "ios" && lower != "android" {
			hasCustomPlatforms = true
			break
		}
	}
	if hasCustomPlatforms {
		return
	}

	cfg.Build.Platforms = defaultExpoBuildPlatforms(cwd)
	if platformCfg, ok := cfg.Build.Platforms["ios"]; ok {
		cfg.Build.Command = platformCfg.Command
		cfg.Build.Output = platformCfg.Output
	}
}

// validateAndFixEASProfiles checks each iOS platform command for simulator profile
// correctness and prompts the user to switch if a better profile is available.
func validateAndFixEASProfiles(cfg *config.ProjectConfig, cwd string) {
	if cfg == nil || !isExpoBuildSystem(cfg.Build.System) {
		return
	}

	easCfg, err := build.LoadEASConfig(cwd)
	if err != nil {
		ui.PrintWarning("Could not read eas.json: %v (skipping profile validation)", err)
		return
	}
	if easCfg == nil {
		return // No eas.json, skip silently
	}

	for key, platformCfg := range cfg.Build.Platforms {
		if !build.IsIOSPlatformKey(key) {
			continue
		}

		result := build.ValidateEASSimulatorProfile(easCfg, platformCfg.Command)
		if result.Valid || result.NoEASConfig || result.ProfileNotFound {
			continue
		}

		// Profile doesn't produce a simulator build
		ui.Println()
		ui.PrintWarning("Profile %q is not a simulator build for %s (Revyl requires simulator builds for iOS).", result.ProfileName, key)

		if len(result.Alternatives) > 0 {
			ui.Println()
			useAlt, err := ui.PromptConfirm(fmt.Sprintf("Switch to %q?", result.Alternatives[0]), true)
			if err == nil && useAlt {
				platformCfg.Command = build.ReplaceProfileInCommand(platformCfg.Command, result.Alternatives[0])
				cfg.Build.Platforms[key] = platformCfg
				ui.PrintSuccess("Updated %s to use profile %q", key, result.Alternatives[0])
			}
		} else {
			// No alternatives — auto-create revyl-build profile
			ui.PrintInfo("Adding \"revyl-build\" simulator profile to eas.json...")
			if err := build.AddRevylBuildProfile(cwd, result.ProfileName); err != nil {
				ui.PrintWarning("Could not update eas.json: %v", err)
			} else {
				platformCfg.Command = build.ReplaceProfileInCommand(platformCfg.Command, "revyl-build")
				cfg.Build.Platforms[key] = platformCfg
				ui.PrintSuccess("Added \"revyl-build\" to eas.json and updated %s", key)
			}
		}
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
		case lower == "ios":
			return 0
		case lower == "android":
			return 1
		case lower == "ios-dev":
			return 2
		case lower == "android-dev":
			return 3
		case lower == "ios-ci":
			return 4
		case lower == "android-ci":
			return 5
		case strings.Contains(lower, "ios") && strings.Contains(lower, "dev"):
			return 6
		case strings.Contains(lower, "android") && strings.Contains(lower, "dev"):
			return 7
		case strings.Contains(lower, "ios"):
			return 8
		case strings.Contains(lower, "android"):
			return 9
		default:
			return 10
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

	// Collect platforms that still need apps.
	type pendingApp struct {
		key      string
		platform string
		appName  string
	}
	var pending []pendingApp
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
		pending = append(pending, pendingApp{
			key:      platformKey,
			platform: platform,
			appName:  fmt.Sprintf("%s-%s", cfg.Project.Name, platformKey),
		})
	}
	if len(pending) == 0 {
		return
	}

	// Show what will be created and ask for confirmation.
	ui.Println()
	ui.PrintInfo("Creating apps to store your builds:")
	ui.Println()
	for i, p := range pending {
		ui.PrintInfo("  %d. %s  (%s)", i+1, p.appName, p.platform)
	}
	ui.Println()

	confirmed, err := ui.PromptConfirm("Create these apps?", true)
	if err != nil || !confirmed {
		ui.PrintDim("Skipped app creation. You can create apps later with: revyl build upload")
		return
	}

	for _, p := range pending {
		appID, err := ensureNamedApp(ctx, client, p.appName, p.platform)
		if err != nil {
			ui.PrintWarning("Failed to link/create app for %s: %v", p.key, err)
			continue
		}

		plat := cfg.Build.Platforms[p.key]
		plat.AppID = appID
		cfg.Build.Platforms[p.key] = plat
		_ = config.WriteProjectConfig(configPath, cfg)
		ui.PrintSuccess("Linked %s -> %s (%s)", p.key, p.appName, appID)
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
		// With the simplified 2-key config (ios/android), there are no
		// explicit dev stream suffixes. All platforms are dev-eligible
		// since they use the development EAS profile.
		if len(devPlatforms) == 0 {
			devPlatforms = platforms
		}
		ui.PrintDim("Expo detected: focusing first build on dev streams (%s)", strings.Join(devPlatforms, ", "))
		if len(platforms) > len(devPlatforms) {
			ui.PrintDim("Other platforms can be uploaded later with: revyl build upload --platform <platform>")
		}
		wizardFirstBuildExpo(ctx, client, cfg, configPath, cwd, devPlatforms)
		return
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
		{
			Label:       fmt.Sprintf("Build and upload default dev stream (fastest: %s)", defaultTarget),
			Value:       "default",
			Description: "Builds one platform so you can start testing quickly",
		},
	}
	if iosTarget != "" {
		options = append(options, ui.SelectOption{
			Label:       fmt.Sprintf("Build and upload iOS dev stream only (%s)", iosTarget),
			Value:       "ios",
			Description: "Builds an iOS simulator app using your development EAS profile",
		})
	}
	if androidTarget != "" {
		options = append(options, ui.SelectOption{
			Label:       fmt.Sprintf("Build and upload Android dev stream only (%s)", androidTarget),
			Value:       "android",
			Description: "Builds an Android APK using your development EAS profile",
		})
	}
	if iosTarget != "" && androidTarget != "" && iosTarget != androidTarget {
		options = append(options, ui.SelectOption{
			Label:       "Build and upload both dev streams (parallel)",
			Value:       "both",
			Description: "Builds iOS and Android concurrently — takes longer but covers both",
		})
	}
	options = append(options,
		ui.SelectOption{
			Label:       "Upload existing artifact(s)",
			Value:       "upload",
			Description: "Skip building — upload a .app/.apk you already have on disk",
		},
		ui.SelectOption{
			Label:       "Skip for now",
			Value:       "skip",
			Description: "Continue without a build; run revyl build upload later",
		},
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
			runner.Interactive = true

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
			var apiErr *api.APIError
			if errors.As(uploadErr, &apiErr) {
				if apiErr.StatusCode == 401 && os.Getenv("REVYL_API_KEY") != "" {
					ui.PrintDim("  REVYL_API_KEY env var is set — it may point to the wrong org or be revoked.")
					ui.PrintDim("  Try: unset REVYL_API_KEY && revyl auth login")
				} else if apiErr.StatusCode == 404 && strings.Contains(apiErr.Detail, "App not found") {
					ui.PrintDim("  The app_id may belong to a different org. Run 'revyl init --force' to rebind.")
				}
			}
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
		runner.Interactive = true
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

			var apiErr *api.APIError
			if errors.As(result.Err, &apiErr) {
				if apiErr.StatusCode == 401 {
					if os.Getenv("REVYL_API_KEY") != "" {
						ui.PrintDim("  REVYL_API_KEY env var is set — it may point to the wrong org or be revoked.")
						ui.PrintDim("  Try: unset REVYL_API_KEY && revyl auth login")
					}
				} else if apiErr.StatusCode == 404 && strings.Contains(apiErr.Detail, "App not found") {
					ui.PrintDim("  The app_id may belong to a different org than the one you're authenticated to.")
					ui.PrintDim("  Run 'revyl init --force' to rebind apps to the current org.")
				}
			}

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

// wizardLaunchDevice launches an interactive device session via `revyl dev`.
func wizardLaunchDevice(ctx context.Context, cfg *config.ProjectConfig, devMode bool) {
	// Determine platform — prefer ios if available
	platform := "ios"
	platforms := selectableRuntimePlatforms(cfg)
	if len(platforms) == 1 {
		platform = platforms[0]
	} else if len(platforms) > 1 {
		idx, err := ui.PromptSelect("Select platform:", platforms)
		if err != nil {
			ui.PrintWarning("Selection error: %v", err)
			return
		}
		platform = platforms[idx]
	}

	// Build the command args
	args := []string{"dev", "--platform", platform}
	if devMode {
		args = append(args, "--dev")
	}

	ui.PrintInfo("Running: revyl %s", strings.Join(args, " "))
	ui.Println()

	exe, err := os.Executable()
	if err != nil {
		ui.PrintWarning("Could not find revyl binary: %v", err)
		ui.PrintInfo("Run manually: revyl %s", strings.Join(args, " "))
		return
	}

	// Replace the current process with `revyl dev`
	env := os.Environ()
	if err := syscall.Exec(exe, append([]string{"revyl"}, args...), env); err != nil {
		ui.PrintWarning("Could not launch device session: %v", err)
		ui.PrintInfo("Run manually: revyl %s", strings.Join(args, " "))
	}
}

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
								testsDir := filepath.Join(filepath.Dir(configPath), "tests")
								if mkErr := os.MkdirAll(testsDir, 0755); mkErr == nil {
									localTest := &config.LocalTest{
										Meta: config.TestMeta{RemoteID: t.ID},
									}
									_ = config.SaveLocalTest(filepath.Join(testsDir, testName+".yaml"), localTest)
								}
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

		// Save local YAML file
		testsDir := filepath.Join(filepath.Dir(configPath), "tests")
		if mkErr := os.MkdirAll(testsDir, 0755); mkErr == nil {
			localTest := &config.LocalTest{
				Meta: config.TestMeta{RemoteID: resp.ID},
			}
			_ = config.SaveLocalTest(filepath.Join(testsDir, testName+".yaml"), localTest)
		}
		syncTestYAML(ctx, client, cfg, testName)

		// Open in browser.
		appURL := config.GetAppURL(devMode)
		testURL := fmt.Sprintf(
			"%s/tests/execute?testUid=%s",
			appURL,
			url.QueryEscape(resp.ID),
		)
		if openErr := ui.OpenBrowser(testURL); openErr == nil {
			ui.PrintDim("Opened in browser: %s", testURL)
		} else {
			ui.PrintDim("View your test: %s", testURL)
		}

		return resp.ID, testName
	}
}

// ---------------------------------------------------------------------------
// Step 4: Dev Loop
// ---------------------------------------------------------------------------

// wizardHotReloadSetup detects and configures hot reload in .revyl/config.yaml.
// It applies smart defaults from project detection, preserves existing explicit
// settings, and auto-maps ios/android to build.platforms keys when possible.
// When explicitProvider is non-empty, auto-detection is bypassed.
func wizardHotReloadSetup(
	ctx context.Context,
	client *api.Client,
	cfg *config.ProjectConfig,
	configPath, cwd string,
	checkConnectivity bool,
	overrideOpts *initOverrideOptions,
	explicitProvider string,
) bool {
	registry := hotreload.DefaultRegistry()

	var detection hotreload.ProviderDetection
	var ok bool

	if explicitProvider != "" {
		provider, provErr := registry.GetProvider(explicitProvider)
		if provErr != nil {
			ui.PrintError("Unknown provider %q. Available: expo, react-native, swift, android", explicitProvider)
			return false
		}
		det, _ := provider.Detect(cwd)
		if det == nil {
			det = &hotreload.DetectionResult{
				Provider:   explicitProvider,
				Confidence: 0.5,
				Platform:   "unknown",
				Indicators: []string{"explicitly requested via --provider"},
			}
		}
		detection = hotreload.ProviderDetection{Provider: provider, Detection: det}
		ok = true
	} else {
		detections := registry.DetectAllProviders(cwd)

		if len(detections) == 0 {
			fallback := tryFallbackProvider(cwd, registry)
			if fallback != nil {
				detections = []hotreload.ProviderDetection{*fallback}
			}
		}

		if len(detections) == 0 {
			ui.PrintDim("No hot reload providers detected in this project.")
			ui.PrintDim("If this is an Expo or React Native project, run:")
			ui.PrintDim("  revyl init --provider expo")
			ui.PrintDim("  revyl init --provider react-native")
			return false
		}

		detection, ok = selectHotReloadDetection(detections, cfg.HotReload.Default)
		if !ok {
			fallback := tryFallbackProvider(cwd, registry)
			if fallback != nil {
				detection = *fallback
				ok = true
			}
		}

		if !ok {
			if cfg != nil && len(cfg.Build.Platforms) > 0 {
				ui.PrintDim("No hot reload for this project type. Use the rebuild dev loop instead:")
				ui.PrintDim("  revyl dev    (press [r] to rebuild + reinstall)")
				return false
			}
			ui.PrintDim("Detected hot reload providers are not yet supported:")
			for _, d := range detections {
				ui.PrintDim("  • %s (coming soon)", d.Provider.DisplayName())
			}
			ui.Println()
			ui.PrintDim("If this is an Expo or React Native project, run:")
			ui.PrintDim("  revyl init --provider expo")
			return false
		}

		if !initNonInteractive {
			detection, ok = confirmHotReloadProvider(detection, detections, registry)
			if !ok {
				return false
			}
		}
	}

	platform := ""
	if detection.Detection != nil {
		platform = detection.Detection.Platform
	}
	setupResult, err := hotreload.AutoSetup(ctx, client, hotreload.SetupOptions{
		WorkDir:          cwd,
		ExplicitProvider: detection.Provider.Name(),
		Platform:         platform,
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
	if setupResult.ProviderName == "expo" {
		explicitAppScheme := ""
		if overrideOpts != nil {
			explicitAppScheme = overrideOpts.HotReloadAppScheme
		}
		applyExpoAppSchemeOverride(mergedCfg, explicitAppScheme, false)
	}
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
	ui.PrintSuccess("Saved hot reload settings to .revyl/config.yaml")
	ui.PrintDim("Edit manually anytime in .revyl/config.yaml")

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

// tryFallbackProvider checks for Expo or React Native indicators that auto-detection
// may have missed (common in monorepos where dependencies are hoisted). Returns a
// ProviderDetection if a supported fallback is found, nil otherwise.
func tryFallbackProvider(cwd string, registry *hotreload.Registry) *hotreload.ProviderDetection {
	hasExpoDep := false
	if data, err := os.ReadFile(filepath.Join(cwd, "package.json")); err == nil {
		hasExpoDep = strings.Contains(string(data), "\"expo\"")
	}

	if hasExpoDep {
		provider, err := registry.GetProvider("expo")
		if err != nil {
			return nil
		}
		var indicators []string
		indicators = append(indicators, "expo in package.json")
		for _, name := range []string{".expo", "eas.json", "app.config.js", "app.config.ts"} {
			if _, err := os.Stat(filepath.Join(cwd, name)); err == nil {
				indicators = append(indicators, name)
			}
		}
		ui.PrintDim("Auto-detected Expo indicators (%s)", strings.Join(indicators, ", "))
		return &hotreload.ProviderDetection{
			Provider: provider,
			Detection: &hotreload.DetectionResult{
				Provider:   "expo",
				Confidence: 0.8,
				Platform:   "cross-platform",
				Indicators: indicators,
			},
		}
	}

	hasRNDep := false
	if data, err := os.ReadFile(filepath.Join(cwd, "package.json")); err == nil {
		hasRNDep = strings.Contains(string(data), "\"react-native\"")
	}
	if hasRNDep {
		provider, err := registry.GetProvider("react-native")
		if err != nil {
			return nil
		}
		return &hotreload.ProviderDetection{
			Provider: provider,
			Detection: &hotreload.DetectionResult{
				Provider:   "react-native",
				Confidence: 0.7,
				Platform:   "cross-platform",
				Indicators: []string{"react-native in package.json"},
			},
		}
	}

	return nil
}

// confirmHotReloadProvider shows the detected provider and lets the user confirm
// or pick a different one. Skipped in non-interactive mode.
//
// Parameters:
//   - selected: the auto-selected provider detection
//   - all: all detected providers (for display context)
//   - registry: provider registry for alternative lookups
//
// Returns:
//   - ProviderDetection: the confirmed or changed selection
//   - bool: false if user cancelled
func confirmHotReloadProvider(selected hotreload.ProviderDetection, all []hotreload.ProviderDetection, registry *hotreload.Registry) (hotreload.ProviderDetection, bool) {
	ui.Println()
	ui.PrintDim("Detected dev loop provider: %s", selected.Provider.DisplayName())
	if selected.Detection != nil && len(selected.Detection.Indicators) > 0 {
		ui.PrintDim("  Indicators: %s", strings.Join(selected.Detection.Indicators, ", "))
	}
	ui.PrintDim("revyl dev uses this provider to push code changes to cloud devices in real time.")

	confirmed, err := ui.PromptConfirm(fmt.Sprintf("Use %s for the dev loop?", selected.Provider.DisplayName()), true)
	if err != nil {
		return selected, true
	}
	if confirmed {
		return selected, true
	}

	supported := registry.SupportedProviders()
	if len(supported) == 0 {
		ui.PrintDim("No supported providers available.")
		return hotreload.ProviderDetection{}, false
	}

	options := make([]ui.SelectOption, len(supported))
	for i, p := range supported {
		options[i] = ui.SelectOption{
			Label: p.DisplayName(),
			Value: p.Name(),
		}
	}

	idx, providerName, selectErr := ui.Select("Select hot reload provider:", options, 0)
	if selectErr != nil || idx < 0 {
		return hotreload.ProviderDetection{}, false
	}

	provider, getErr := registry.GetProvider(providerName)
	if getErr != nil {
		return hotreload.ProviderDetection{}, false
	}

	det, _ := provider.Detect("")
	if det == nil {
		det = &hotreload.DetectionResult{
			Provider:   providerName,
			Confidence: 0.5,
			Platform:   "unknown",
			Indicators: []string{"manually selected"},
		}
	}

	return hotreload.ProviderDetection{Provider: provider, Detection: det}, true
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
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
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
// printInitSummary prints a concise detection summary after lightweight init.
func printInitSummary(cfg *config.ProjectConfig) {
	ui.Println()
	system := cfg.Build.System
	if system == "" {
		system = "Unknown"
	}
	ui.PrintSuccess("Detected: %s", system)

	if len(cfg.Build.Platforms) > 0 {
		keys := platformKeys(cfg)
		ui.PrintInfo("  Platforms: %s", strings.Join(keys, ", "))
	}

	for _, key := range platformKeys(cfg) {
		plat := cfg.Build.Platforms[key]
		if plat.Command != "" {
			ui.PrintDim("  %s build: %s", key, plat.Command)
		}
	}
	ui.Println()

	printBuildSystemExplanation(cfg.Build.System)
}

// printBuildSystemExplanation prints a contextual explanation of how Revyl
// works for the detected build system, including dev loop instructions.
func printBuildSystemExplanation(system string) {
	switch strings.ToLower(system) {
	case "expo":
		ui.PrintDim("How it works:")
		ui.PrintDim("  Your config uses the \"development\" EAS profile. This creates a build")
		ui.PrintDim("  that includes the Expo dev client — enabling hot reload (revyl dev)")
		ui.PrintDim("  where JS/TS changes reflect instantly on a cloud device.")
		ui.PrintDim("")
		ui.PrintDim("  The same build also works for regular test runs (revyl test run).")
		ui.PrintDim("  No separate CI build is needed to get started.")
		ui.PrintDim("")
		ui.PrintDim("  Dev loop:  revyl dev             JS changes hot reload instantly")
		ui.PrintDim("             press [r]             rebuild native code + reinstall")

	case "react native":
		ui.PrintDim("How it works:")
		ui.PrintDim("  Debug builds connect to your local Metro bundler via hot reload.")
		ui.PrintDim("  JS/TS changes reflect instantly on the cloud device.")
		ui.PrintDim("")
		ui.PrintDim("  Dev loop:  revyl dev             JS changes hot reload via Metro")
		ui.PrintDim("             press [r]             rebuild native code + reinstall")

	case "gradle (android)", "gradle":
		ui.PrintDim("How it works:")
		ui.PrintDim("  Builds a debug APK using ./gradlew assembleDebug. The APK is uploaded")
		ui.PrintDim("  to a cloud device where tests run against it.")
		ui.PrintDim("")
		ui.PrintDim("  No hot reload for native Android — use the rebuild dev loop instead.")
		ui.PrintDim("")
		ui.PrintDim("  Dev loop:  revyl dev             build, upload, install on device")
		ui.PrintDim("             press [r]             rebuild + reinstall (~30-90s)")

	case "xcode", "swift package manager":
		ui.PrintDim("How it works:")
		ui.PrintDim("  Builds a simulator .app using xcodebuild with Debug configuration.")
		ui.PrintDim("  The .app is uploaded to a cloud simulator where tests run against it.")
		ui.PrintDim("")
		ui.PrintDim("  No hot reload for native iOS — use the rebuild dev loop instead.")
		ui.PrintDim("  Note: iOS reinstalls clear app data (login state, preferences).")
		ui.PrintDim("")
		ui.PrintDim("  Dev loop:  revyl dev             build, upload, install on device")
		ui.PrintDim("             press [r]             rebuild + reinstall (~20-60s)")

	default:
		ui.PrintDim("How Revyl works:")
		ui.PrintDim("  Revyl runs tests on cloud devices. Your app binary needs to be")
		ui.PrintDim("  uploaded so the device can install it.")
		ui.PrintDim("")
		ui.PrintDim("  \"App\"   = a named container that holds versions of your build")
		ui.PrintDim("  \"Build\" = an uploaded binary (.apk or .app) installed on devices")
	}
	ui.Println()
}

// printInitNextSteps prints actionable next steps after init completes.
func printInitNextSteps(cfg *config.ProjectConfig) {
	ui.PrintInfo("Next steps:")
	ui.PrintInfo("  1. revyl auth login              # Authenticate")

	platforms := platformKeys(cfg)
	if len(platforms) > 0 {
		ui.PrintInfo("  2. revyl build upload            # Build and upload")
		ui.PrintInfo("  3. revyl test create smoke-test  # Create your first test")
		ui.PrintInfo("  4. revyl test run smoke-test     # Run it")
	} else {
		ui.PrintInfo("  2. revyl build upload --platform <ios|android>")
		ui.PrintInfo("  3. revyl test create <name> --platform <ios|android>")
		ui.PrintInfo("  4. revyl test run <name>")
	}

	ui.Println()
	ui.PrintDim("Re-run to continue setup:")
	ui.PrintDim("  revyl init --force")
}

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
				ui.PrintDim("  • %s (rebuild dev loop via revyl dev)", d.Provider.DisplayName())
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
			ui.PrintDim("Hot reload can be configured by re-running: revyl init --detect")
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
		if cwd, cwdErr := os.Getwd(); cwdErr == nil {
			localAliases := config.ListLocalTestAliases(filepath.Join(cwd, ".revyl", "tests"))
			if len(localAliases) > 0 {
				testAlias = localAliases[0]
				steps = append(steps, ui.NextStep{Label: "Run your test:", Command: fmt.Sprintf("revyl test run %s", testAlias)})
			}
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
//   - cfg: Project configuration
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
