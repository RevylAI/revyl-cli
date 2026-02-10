// Package main provides the init command as a guided onboarding wizard.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/hotreload"
	_ "github.com/revyl/cli/internal/hotreload/providers" // Register providers
	"github.com/revyl/cli/internal/ui"
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
  4. First build — (coming soon) build and upload your artifact
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
	ui.PrintInfo("Step 1/6: Project Setup")
	ui.Println()

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
	ui.Println()
	ui.PrintInfo("Step 2/6: Authentication")
	ui.Println()

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
	ui.Println()
	ui.PrintInfo("Step 3/6: Create Apps")
	ui.Println()

	wizardCreateApps(ctx, client, cfg, configPath)

	// ── Step 4/6: First Build (coming soon) ──────────────────────────────
	ui.Println()
	ui.PrintInfo("Step 4/6: First Build")
	ui.Println()
	wizardFirstBuild(cfg)

	// ── Step 5/6: Create First Test ──────────────────────────────────────
	ui.Println()
	ui.PrintInfo("Step 5/6: Create First Test")
	ui.Println()

	testID := wizardCreateTest(ctx, client, cfg, configPath, devMode, userInfo)

	// ── Step 6/6: Create Workflow ────────────────────────────────────────
	ui.Println()
	ui.PrintInfo("Step 6/6: Create Workflow")
	ui.Println()

	wizardCreateWorkflow(ctx, client, cfg, configPath, testID, userInfo)

	// ── Summary ──────────────────────────────────────────────────────────
	ui.Println()
	printCreatedFiles()
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

	projectName := filepath.Base(cwd)

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
	if err := mgr.SaveBrowserCredentials(result, 8*time.Hour); err != nil {
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
	}, 8*time.Hour)

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

		ui.PrintInfo("Platform: %s", platformKey)

		// Fetch existing apps for this platform.
		appsResp, err := client.ListApps(ctx, platformKey, 1, 10)
		if err != nil {
			ui.PrintWarning("Could not list apps for %s: %v", platformKey, err)
			continue
		}

		var appID string

		if len(appsResp.Items) > 0 {
			// Build selection options: existing apps + Create new + Skip.
			options := make([]string, 0, len(appsResp.Items)+2)
			for _, app := range appsResp.Items {
				label := fmt.Sprintf("%s (id: %s)", app.Name, app.ID)
				options = append(options, label)
			}
			options = append(options, "Create new app")
			options = append(options, "Skip")

			idx, err := ui.PromptSelect(fmt.Sprintf("Select app for %s:", platformKey), options)
			if err != nil {
				ui.PrintWarning("Selection failed: %v", err)
				continue
			}

			if idx < len(appsResp.Items) {
				// User picked an existing app.
				appID = appsResp.Items[idx].ID
				ui.PrintSuccess("Linked %s to app %s", platformKey, appsResp.Items[idx].Name)
			} else if idx == len(appsResp.Items) {
				// Create new.
				appID = createAppInteractive(ctx, client, cfg.Project.Name, platformKey)
			}
			// else: Skip
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
		ui.PrintWarning("Failed to create app: %v", err)
		return ""
	}

	ui.PrintSuccess("Created app %s (id: %s)", resp.Name, resp.ID)
	return resp.ID
}

// ---------------------------------------------------------------------------
// Step 4: First Build (stub)
// ---------------------------------------------------------------------------

// wizardFirstBuild is a placeholder for the build+upload step.
// The build logic is complex and lives in build.go; we intentionally avoid
// duplicating it here and instead point the user to the dedicated command.
func wizardFirstBuild(cfg *config.ProjectConfig) {
	platforms := platformKeys(cfg)
	if len(platforms) == 0 {
		ui.PrintDim("No platforms configured — skipping build step")
		return
	}

	ui.PrintDim("Build upload coming soon in wizard.")
	for _, p := range platforms {
		ui.PrintDim("  Run: revyl build upload --platform %s", p)
	}
}

// ---------------------------------------------------------------------------
// Step 5: Create First Test
// ---------------------------------------------------------------------------

// wizardCreateTest offers to create a test, saves it in the config, and opens
// it in the browser. Returns the created test ID (empty if skipped/failed).
func wizardCreateTest(
	ctx context.Context,
	client *api.Client,
	cfg *config.ProjectConfig,
	configPath string,
	devMode bool,
	userInfo *api.ValidateAPIKeyResponse,
) string {
	proceed, err := ui.PromptConfirm("Create your first test?", true)
	if err != nil || !proceed {
		ui.PrintDim("Skipped test creation")
		return ""
	}

	// Prompt for test name.
	testName, err := ui.Prompt("Test name [login]:")
	if err != nil {
		ui.PrintWarning("Input error: %v", err)
		return ""
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
			return ""
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
			return ""
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

	// Create the test.
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
		ui.PrintWarning("Failed to create test: %v", err)
		return ""
	}

	ui.PrintSuccess("Created test \"%s\" (id: %s)", testName, resp.ID)

	// Save to config.
	if cfg.Tests == nil {
		cfg.Tests = make(map[string]string)
	}
	cfg.Tests[testName] = resp.ID
	_ = config.WriteProjectConfig(configPath, cfg)

	// Open in browser.
	appURL := config.GetAppURL(devMode)
	testURL := fmt.Sprintf("%s/tests/%s", appURL, resp.ID)
	if openErr := ui.OpenBrowser(testURL); openErr == nil {
		ui.PrintDim("Opened in browser: %s", testURL)
	} else {
		ui.PrintDim("View your test: %s", testURL)
	}

	return resp.ID
}

// ---------------------------------------------------------------------------
// Step 6: Create Workflow
// ---------------------------------------------------------------------------

// wizardCreateWorkflow offers to group tests into a workflow. Default is No.
func wizardCreateWorkflow(
	ctx context.Context,
	client *api.Client,
	cfg *config.ProjectConfig,
	configPath string,
	justCreatedTestID string,
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
	testIDs := gatherTestIDsForWorkflow(ctx, client, justCreatedTestID)

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
// user may pick from existing org tests.
func gatherTestIDsForWorkflow(ctx context.Context, client *api.Client, justCreatedTestID string) []string {
	var testIDs []string

	if justCreatedTestID != "" {
		testIDs = append(testIDs, justCreatedTestID)
		ui.PrintDim("Including the test you just created")
	}

	// Offer to add more tests from the org.
	addMore, err := ui.PromptConfirm("Add existing tests from your organization?", false)
	if err != nil || !addMore {
		return testIDs
	}

	listResp, err := client.ListOrgTests(ctx, 20, 0)
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
