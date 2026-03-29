package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/buildselection"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/hotreload"
	_ "github.com/revyl/cli/internal/hotreload/providers" // Register providers
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/ui"
)

var (
	devStartPlatform    string
	devStartPlatformKey string
	devStartAppID       string
	devStartBuildVerID  string
	devStartBuild       bool
	devStartPort        int
	devStartTimeout     int
	devStartOpen        bool
	devStartNoOpen      bool

	devTestRunPlatform    string
	devTestRunPlatformKey string
)

var devCmd = &cobra.Command{
	Use:   "dev",
	Short: "Local development loop with live device",
	Long: `Start and manage the iterative local development loop.

Auto-detects your project type (Expo, React Native, Swift, Android),
starts hot reload, provisions a cloud device, installs the latest
dev build, and opens a live viewer.

On first run, auto-configures dev mode if not already set up.`,
	RunE: runDevStart,
}

var devStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a hot reload device loop",
	RunE:  runDevStart,
}

var devTestCmd = &cobra.Command{
	Use:               "test",
	Short:             "Run test commands with hot reload defaults",
	PersistentPreRunE: enforceOrgBindingMatch,
}

var devTestRunCmd = &cobra.Command{
	Use:   "run <name|id>",
	Short: "Run a test in dev mode (hot reload)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		runHotReload = true
		if strings.TrimSpace(devTestRunPlatformKey) != "" {
			runTestPlatform = strings.TrimSpace(devTestRunPlatformKey)
		} else {
			runTestPlatform = strings.TrimSpace(devTestRunPlatform)
		}
		return runTestExec(cmd, args)
	},
}

var devTestOpenCmd = &cobra.Command{
	Use:   "open <name>",
	Short: "Open a test with hot reload defaults",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		openTestHotReload = true
		return runOpenTest(cmd, args)
	},
}

var devTestCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a test with hot reload defaults",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		createTestHotReload = true
		return runCreateTest(cmd, args)
	},
}

func init() {
	registerDevStartFlags(devCmd)
	registerDevStartFlags(devStartCmd)

	devCmd.AddCommand(devStartCmd)
	devCmd.AddCommand(devTestCmd)

	devTestCmd.AddCommand(devTestRunCmd)
	devTestCmd.AddCommand(devTestOpenCmd)
	devTestCmd.AddCommand(devTestCreateCmd)

	// dev test run flags (hotreload is always enabled in this namespace)
	devTestRunCmd.Flags().IntVarP(&runRetries, "retries", "r", 1, "Number of retry attempts (1-5)")
	devTestRunCmd.Flags().StringVarP(&runBuildID, "build-id", "b", "", "Specific build version ID")
	devTestRunCmd.Flags().BoolVar(&runNoWait, "no-wait", false, "Exit after test starts without waiting")
	devTestRunCmd.Flags().BoolVar(&runOpen, "open", false, "Open report in browser when complete")
	devTestRunCmd.Flags().IntVarP(&runTimeout, "timeout", "t", 3600, "Timeout in seconds")
	devTestRunCmd.Flags().BoolVar(&runOutputJSON, "json", false, "Output results as JSON")
	devTestRunCmd.Flags().BoolVar(&runGitHubActions, "github-actions", false, "Format output for GitHub Actions")
	devTestRunCmd.Flags().BoolVarP(&runVerbose, "verbose", "v", false, "Show detailed monitoring output")
	devTestRunCmd.Flags().StringVar(&devTestRunPlatform, "platform", "ios", "Device platform to use (ios or android)")
	devTestRunCmd.Flags().StringVar(&devTestRunPlatformKey, "platform-key", "", "Explicit build.platforms key for dev client build")
	devTestRunCmd.Flags().StringVar(&runLocation, "location", "", "Initial GPS location as lat,lng (e.g. 37.7749,-122.4194)")
	devTestRunCmd.Flags().IntVar(&runHotReloadPort, "port", 8081, "Port for dev server")

	// dev test open flags
	devTestOpenCmd.Flags().IntVar(&openTestHotReloadPort, "port", 8081, "Port for dev server")
	devTestOpenCmd.Flags().BoolVar(&openTestInteractive, "interactive", false, "Edit test interactively with real-time device feedback")
	devTestOpenCmd.Flags().BoolVar(&openTestNoOpen, "no-open", false, "Skip opening browser (with --interactive: output URL and wait for Ctrl+C)")
	devTestOpenCmd.Flags().StringVar(&openTestHotReloadPlatform, "platform-key", "", "Build platform key for hot reload dev client")

	// dev test create flags
	devTestCreateCmd.Flags().StringVar(&createTestPlatform, "platform", "ios", "Target platform (android, ios)")
	devTestCreateCmd.Flags().StringVar(&createTestAppID, "app", "", "App ID to associate with the test")
	devTestCreateCmd.Flags().BoolVar(&createTestNoOpen, "no-open", false, "Skip opening browser to test editor")
	devTestCreateCmd.Flags().BoolVar(&createTestForce, "force", false, "Update existing test if name already exists")
	devTestCreateCmd.Flags().BoolVar(&createTestDryRun, "dry-run", false, "Show what would be created without creating")
	devTestCreateCmd.Flags().StringVar(&createTestFromFile, "from-file", "", "Create test from YAML file (copies to .revyl/tests/ and pushes)")
	devTestCreateCmd.Flags().IntVar(&createTestHotReloadPort, "port", 8081, "Port for dev server")
	devTestCreateCmd.Flags().StringVar(&createTestHotReloadPlatform, "platform-key", "", "Build platform key for hot reload dev client")
	devTestCreateCmd.Flags().BoolVar(&createTestInteractive, "interactive", false, "Create test interactively with real-time device feedback")
	devTestCreateCmd.Flags().StringSliceVar(&createTestModules, "module", nil, "Module name or ID to insert as module_import block (can be repeated)")
	devTestCreateCmd.Flags().StringSliceVar(&createTestTags, "tag", nil, "Tag to assign after creation (can be repeated)")
}

func registerDevStartFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&devStartPlatform, "platform", "ios", "Device platform to start (ios or android)")
	cmd.Flags().StringVar(&devStartPlatformKey, "platform-key", "", "Explicit build.platforms key for the dev client build")
	cmd.Flags().StringVar(&devStartAppID, "app-id", "", "App ID to resolve latest build from")
	cmd.Flags().StringVar(&devStartBuildVerID, "build-version-id", "", "Specific build version ID to use")
	cmd.Flags().BoolVar(&devStartBuild, "build", false, "Force build+upload before starting")
	cmd.Flags().IntVar(&devStartPort, "port", 8081, "Port for local dev server")
	cmd.Flags().IntVar(&devStartTimeout, "timeout", 300, "Device idle timeout in seconds")
	cmd.Flags().BoolVar(&devStartOpen, "open", true, "Open live device viewer in browser")
	cmd.Flags().BoolVar(&devStartNoOpen, "no-open", false, "Do not open the live device viewer in browser")
}

func runDevStart(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(args, " "))
	}
	if isCIEnvironment() {
		return fmt.Errorf("`revyl dev` is for local development loops. In CI, use `revyl test run` or `revyl device start`")
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}
	if repoRoot, rootErr := config.FindRepoRoot(cwd); rootErr == nil {
		cwd = repoRoot
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	cfg, err := config.LoadProjectConfig(configPath)
	if err != nil {
		ui.PrintError("Project not initialized. Run 'revyl init' first.")
		return fmt.Errorf("project not initialized")
	}

	if !cfg.HotReload.IsConfigured() {
		ui.PrintInfo("Dev mode not configured yet. Setting up...")
		ui.Println()

		devMode, _ := cmd.Flags().GetBool("dev")
		var setupClient *api.Client
		if apiKey != "" {
			setupClient = api.NewClientWithDevMode(apiKey, devMode)
		}

		ready := wizardHotReloadSetup(context.Background(), setupClient, cfg, configPath, cwd, false, nil, "")
		if !ready || !cfg.HotReload.IsConfigured() {
			ui.PrintError("Could not auto-configure dev mode.")
			ui.PrintInfo("Try: revyl init --provider expo")
			return fmt.Errorf("dev mode auto-setup failed")
		}
		ui.Println()
	}

	requestedPlatform, err := normalizeMobilePlatform(devStartPlatform, "ios")
	if err != nil {
		return err
	}
	appIDOverride := strings.TrimSpace(devStartAppID)
	buildVersionID := strings.TrimSpace(devStartBuildVerID)
	if buildVersionID != "" && devStartBuild {
		return fmt.Errorf("use either --build-version-id or --build, not both")
	}
	if appIDOverride != "" && devStartBuild {
		return fmt.Errorf("use either --app-id or --build, not both")
	}
	if appIDOverride != "" && buildVersionID != "" {
		return fmt.Errorf("use either --app-id or --build-version-id, not both")
	}

	registry := hotreload.DefaultRegistry()
	provider, providerCfg, err := registry.SelectProvider(&cfg.HotReload, "", cwd)
	if err != nil {
		return fmt.Errorf("dev mode is not configured: %w", err)
	}
	if !provider.IsSupported() {
		return fmt.Errorf("%s dev mode is not yet supported (coming soon)", provider.DisplayName())
	}
	if provider.Name() == "expo" && (providerCfg == nil || strings.TrimSpace(providerCfg.AppScheme) == "") {
		return fmt.Errorf("hotreload.providers.expo.app_scheme is required for Expo dev mode (run `revyl init --provider expo` or `revyl config set hotreload.app-scheme <scheme>`)")
	}

	devicePlatform := requestedPlatform
	platformKey := ""
	if strings.TrimSpace(devStartPlatformKey) != "" {
		platformKey, devicePlatform, err = resolveHotReloadBuildPlatform(cfg, providerCfg, strings.TrimSpace(devStartPlatformKey), requestedPlatform)
		if err != nil {
			return err
		}
	}

	timeout := devStartTimeout
	if !cmd.Flags().Changed("timeout") {
		timeout = config.EffectiveTimeoutSeconds(cfg, timeout)
	}
	if timeout <= 0 {
		timeout = 300
	}

	openBrowser := devStartOpen
	if !cmd.Flags().Changed("open") {
		openBrowser = config.EffectiveOpenBrowser(cfg)
	}
	if devStartNoOpen {
		openBrowser = false
	}

	if cmd.Flags().Changed("port") && devStartPort > 0 {
		providerCfg.Port = devStartPort
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	platformCfg := config.BuildPlatform{}
	if platformKey != "" {
		cfgForKey, ok := cfg.Build.Platforms[platformKey]
		if !ok {
			return fmt.Errorf("build.platforms.%s not found", platformKey)
		}
		platformCfg = cfgForKey
	}

	selectedAppID := appIDOverride
	buildSelectionSource := ""
	var selectedVersion *api.BuildVersion
	if buildVersionID == "" {
		if selectedAppID == "" {
			if platformKey == "" {
				platformKey, devicePlatform, err = resolveHotReloadBuildPlatform(cfg, providerCfg, requestedPlatform, requestedPlatform)
				if err != nil {
					return err
				}
			}
			cfgForKey, ok := cfg.Build.Platforms[platformKey]
			if !ok {
				return fmt.Errorf("build.platforms.%s not found", platformKey)
			}
			platformCfg = cfgForKey

			if strings.TrimSpace(platformCfg.AppID) == "" {
				ui.PrintWarning("No app_id configured for build.platforms.%s", platformKey)
				_, err := selectOrCreateAppForPlatform(cmd, client, cfg, configPath, platformKey, devicePlatform)
				if err != nil {
					return err
				}
				cfg, err = config.LoadProjectConfig(configPath)
				if err != nil {
					return fmt.Errorf("failed to reload config after app selection: %w", err)
				}
				platformCfg = cfg.Build.Platforms[platformKey]
				if strings.TrimSpace(platformCfg.AppID) == "" {
					return fmt.Errorf("build.platforms.%s.app_id is still empty after setup", platformKey)
				}
			}
			selectedAppID = strings.TrimSpace(platformCfg.AppID)
		}

		var source string
		var warnings []string
		var latestErr error
		selectedVersion, source, warnings, latestErr = buildselection.SelectPreferredBuildVersion(
			cmd.Context(),
			client,
			selectedAppID,
			cwd,
		)
		if latestErr != nil {
			return fmt.Errorf("failed to resolve latest build for app %s: %w", selectedAppID, latestErr)
		}
		for _, warning := range warnings {
			ui.PrintWarning("%s", warning)
		}
		if selectedVersion != nil {
			buildVersionID = selectedVersion.ID
			buildSelectionSource = source
		}
	}

	if devStartBuild || buildVersionID == "" {
		if appIDOverride != "" {
			return fmt.Errorf("no builds found for app %s; provide --build-version-id or use config-backed dev flow", appIDOverride)
		}
		if platformKey == "" {
			platformKey, devicePlatform, err = resolveHotReloadBuildPlatform(cfg, providerCfg, requestedPlatform, requestedPlatform)
			if err != nil {
				return err
			}
		}
		cfgForKey, ok := cfg.Build.Platforms[platformKey]
		if !ok {
			return fmt.Errorf("build.platforms.%s not found", platformKey)
		}
		platformCfg = cfgForKey

		ui.PrintInfo("Building and uploading dev client (%s)...", platformKey)
		if err := runSinglePlatformBuild(cmd, cfg, configPath, apiKey, platformKey); err != nil {
			return err
		}

		cfg, err = config.LoadProjectConfig(configPath)
		if err != nil {
			return fmt.Errorf("failed to reload config after build upload: %w", err)
		}
		platformCfg = cfg.Build.Platforms[platformKey]
		if strings.TrimSpace(platformCfg.AppID) == "" {
			return fmt.Errorf("build.platforms.%s.app_id is missing after build upload", platformKey)
		}
		selectedAppID = strings.TrimSpace(platformCfg.AppID)

		latestVersion, latestErr := client.GetLatestBuildVersion(cmd.Context(), selectedAppID)
		if latestErr != nil {
			return fmt.Errorf("failed to resolve uploaded build for %s: %w", platformKey, latestErr)
		}
		if latestVersion == nil {
			return fmt.Errorf("build upload finished but no build versions were found for %s", platformKey)
		}
		buildVersionID = latestVersion.ID
	}

	buildDetail, err := client.GetBuildVersionDownloadURL(cmd.Context(), buildVersionID)
	if err != nil {
		return fmt.Errorf("failed to resolve build download URL: %w", err)
	}
	ui.PrintDebug(
		"resolved dev build artifact: version=%s package=%s",
		buildVersionID,
		strings.TrimSpace(buildDetail.PackageName),
	)

	ui.PrintBanner(version)
	ui.Println()

	buildMeta := map[string]interface{}(nil)
	if selectedVersion != nil {
		buildMeta = selectedVersion.Metadata
	}
	if buildMeta == nil && buildDetail.Metadata != nil {
		buildMeta = buildDetail.Metadata
	}
	printDevPreflight(
		provider.DisplayName(),
		devicePlatform,
		platformKey,
		buildVersionID,
		buildSelectionSource,
		selectedVersion,
		buildMeta,
		provider.Name(),
		cwd,
	)
	ui.Println()

	manager := hotreload.NewManager(provider.Name(), providerCfg, cwd)
	manager.SetLogCallback(func(msg string) {
		ui.PrintDim("  %s", msg)
	})
	debugEnabled, _ := cmd.Flags().GetBool("debug")
	manager.SetDebugMode(debugEnabled)
	if debugEnabled {
		manager.SetDevServerOutputCallback(func(output hotreload.DevServerOutput) {
			line := strings.TrimSpace(output.Line)
			if line == "" {
				return
			}
			if output.Stream == hotreload.DevServerOutputStderr {
				ui.PrintWarning("[expo][stderr] %s", line)
				return
			}
			ui.PrintDim("  [expo][stdout] %s", line)
		})
	}

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	var interrupted int32

	sigChan := make(chan os.Signal, 2)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)
	stopSignalHandler := make(chan struct{})
	defer close(stopSignalHandler)

	go func() {
		interruptCount := 0
		for {
			select {
			case <-stopSignalHandler:
				return
			case <-sigChan:
				interruptCount++
				if interruptCount == 1 {
					atomic.StoreInt32(&interrupted, 1)
					ui.Println()
					ui.PrintInfo("Stopping dev session...")
					cancel()
					continue
				}
				ui.Println()
				ui.PrintWarning("Force exiting dev session...")
				os.Exit(130)
			}
		}
	}()

	isUserCanceled := func(err error) bool {
		return atomic.LoadInt32(&interrupted) == 1 && isContextCanceledError(err)
	}

	ui.PrintInfo("Starting hot reload...")
	startResult, err := manager.Start(ctx)
	if err != nil {
		if isUserCanceled(err) {
			return nil
		}
		return fmt.Errorf("failed to start hot reload: %w", err)
	}
	defer manager.Stop()

	ui.PrintSuccess("Hot reload ready: Expo server and tunnel are running")
	ui.PrintInfo("Starting cloud device session...")

	deviceMgr, err := getDeviceSessionMgr(cmd)
	if err != nil {
		return err
	}

	_, session, err := startDevSessionWithProgress(
		ctx,
		deviceMgr,
		mcppkg.StartSessionOptions{
			Platform:       devicePlatform,
			AppID:          selectedAppID,
			BuildVersionID: buildVersionID,
			AppURL:         strings.TrimSpace(buildDetail.DownloadURL),
			AppPackage:     strings.TrimSpace(buildDetail.PackageName),
			AppLink:        startResult.DeepLinkURL,
			IdleTimeout:    time.Duration(timeout) * time.Second,
		},
		30*time.Second,
		nil,
	)
	if err != nil {
		if isUserCanceled(err) {
			return nil
		}
		return err
	}
	ui.PrintSuccess("Device session ready")
	defer func() {
		if stopErr := deviceMgr.StopSession(context.Background(), session.Index); stopErr != nil {
			if isNoSessionAtIndexError(stopErr, session.Index) {
				return
			}
			ui.PrintWarning("Failed to stop device session %d: %v", session.Index, stopErr)
		}
	}()

	// Explicitly install the dev build via worker endpoint so dev loop behavior
	// is deterministic even if backend start-device flows skip installation.
	installBody := map[string]string{
		"app_url": strings.TrimSpace(buildDetail.DownloadURL),
	}
	if bundleID := strings.TrimSpace(buildDetail.PackageName); bundleID != "" {
		installBody["bundle_id"] = bundleID
	}
	ui.PrintInfo("Installing dev build on device...")
	ui.PrintDebug("install payload: app_url=%s bundle_id=%s", maskPresignedURL(installBody["app_url"]), installBody["bundle_id"])
	installRespBody, err := deviceMgr.WorkerRequestForSession(ctx, session.Index, "/install", installBody)
	if err != nil {
		if isUserCanceled(err) {
			return nil
		}
		ui.PrintDebug("install HTTP error: %v", err)
		return fmt.Errorf("device started but app install failed: %w", err)
	}
	ui.PrintDebug("install worker response: %s", string(installRespBody))
	if err := ensureWorkerActionSucceeded(installRespBody, "install"); err != nil {
		return fmt.Errorf("device started but app install failed: %w", err)
	}

	bundleID := strings.TrimSpace(buildDetail.PackageName)
	if bundleID == "" {
		bundleID = extractInstallBundleID(installRespBody)
	}
	if bundleID != "" {
		ui.PrintInfo("Launching dev client app...")
		launchRespBody, err := deviceMgr.WorkerRequestForSession(ctx, session.Index, "/launch", map[string]string{
			"bundle_id": bundleID,
		})
		if err != nil {
			if isUserCanceled(err) {
				return nil
			}
			return fmt.Errorf("app install succeeded but app launch failed: %w", err)
		}
		if err := ensureWorkerActionSucceeded(launchRespBody, "launch"); err != nil {
			return fmt.Errorf("app install succeeded but app launch failed: %w", err)
		}
	} else {
		ui.PrintWarning("Build metadata has no package_name and worker did not return bundle_id; skipping explicit launch and opening deep link directly")
	}

	deepLinkURL := strings.TrimSpace(startResult.DeepLinkURL)
	if deepLinkURL == "" {
		return fmt.Errorf("hot reload started but deep link URL is empty")
	}
	manualDeepLinkRequired := false
	ui.PrintInfo("Opening hot reload deep link...")
	openURLRespBody, err := deviceMgr.WorkerRequestForSession(ctx, session.Index, "/open_url", map[string]string{
		"url": deepLinkURL,
	})
	if err != nil {
		if isUserCanceled(err) {
			return nil
		}
		if isUnsupportedWorkerRoute(err, "/open_url") {
			manualDeepLinkRequired = true
			ui.PrintWarning("Device does not support /open_url; automatic deep-link navigation is unavailable for this session")
			ui.PrintInfo("Manual step: open this deep link on device: %s", deepLinkURL)
		} else {
			return fmt.Errorf(
				"deep-link navigation failed: %w\nhint: verify hotreload.providers.expo.app_scheme and try hotreload.providers.expo.use_exp_prefix: true",
				err,
			)
		}
	} else {
		if err := ensureWorkerActionSucceeded(openURLRespBody, "open_url"); err != nil {
			return fmt.Errorf(
				"deep-link navigation failed: %w\nhint: verify hotreload.providers.expo.app_scheme and try hotreload.providers.expo.use_exp_prefix: true",
				err,
			)
		}
	}

	viewerURL := session.ViewerURL
	if devMode {
		viewerURL = strings.Replace(viewerURL, "https://app.revyl.ai", "http://localhost:3000", 1)
	}
	reportURL := fmt.Sprintf("%s/tests/report?sessionId=%s", config.GetAppURL(devMode), session.SessionID)
	printDevReadyFooter(viewerURL, reportURL, startResult.DeepLinkURL, manualDeepLinkRequired)

	if openBrowser {
		_ = ui.OpenBrowser(reportURL)
	}

	// Disable the CLI-side idle timer: it cannot track activity from
	// other processes (browser viewer, `revyl device tap`, MCP tools).
	// The worker's idle timer is the authoritative source of truth.
	deviceMgr.StopIdleTimer(session.Index)

	waitForDevSessionStop(ctx, cancel, deviceMgr, session, time.Duration(timeout)*time.Second)
	return nil
}

func isCIEnvironment() bool {
	return strings.TrimSpace(os.Getenv("CI")) != "" ||
		strings.TrimSpace(os.Getenv("GITHUB_ACTIONS")) != ""
}

func printDevReadyFooter(viewerURL, reportURL, deepLinkURL string, manualDeepLinkRequired bool) {
	ui.Println()
	ui.PrintSuccess("Dev loop ready")
	ui.PrintLink("Viewer", viewerURL)
	ui.PrintLink("Report", reportURL)
	ui.PrintInfo("Deep Link: %s", deepLinkURL)
	if manualDeepLinkRequired {
		ui.PrintWarning("Deep link was not opened automatically on this worker. Use the Deep Link above in the device browser/dev client.")
	}
	ui.PrintDim("Press Ctrl+C to stop hot reload and release the device")
	ui.Println()
	ui.PrintInfo("In a new terminal, try:")
	ui.PrintDim("  revyl device tap --target \"Login button\"")
	ui.PrintDim("  revyl device screenshot")
}

// printDevPreflight renders the structured pre-flight checklist box showing
// build classification, compatibility verdict, and actionable fix commands.
//
// Parameters:
//   - providerDisplay: human-readable provider name (e.g. "Expo")
//   - devicePlatform: target platform (e.g. "ios", "android")
//   - platformKey: build platform key (e.g. "ios-dev")
//   - buildVersionID: the resolved build version ID
//   - source: how the build was selected ("branch:...", "latest", "explicit")
//   - ver: the full BuildVersion if available (nil for explicit --build-version-id)
//   - metadata: build metadata for classification
//   - providerName: provider identifier for heuristics (e.g. "expo")
//   - cwd: working directory for branch detection
func printDevPreflight(
	providerDisplay, devicePlatform, platformKey, buildVersionID, source string,
	ver *api.BuildVersion,
	metadata map[string]interface{},
	providerName, cwd string,
) {
	var rows []ui.PreflightRow

	platformDisplay := devicePlatform
	if platformKey != "" {
		platformDisplay = fmt.Sprintf("%s (%s)", devicePlatform, platformKey)
	}
	rows = append(rows,
		ui.PreflightRow{Key: "Provider:", Value: providerDisplay},
		ui.PreflightRow{Key: "Platform:", Value: platformDisplay},
	)

	buildLabel := buildVersionID
	if ver != nil && ver.Version != "" {
		buildLabel = ver.Version
	}
	rows = append(rows, ui.PreflightRow{Key: "Build:", Value: buildLabel})

	buildBranch := ""
	if ver != nil {
		buildBranch = buildselection.ExtractBranch(ver.Metadata)
	}
	currentBranch := buildselection.CurrentBranch(cwd)
	branchRow := buildBranchRow(buildBranch, currentBranch, source)
	if branchRow != nil {
		rows = append(rows, *branchRow)
	}

	preflight := buildselection.ClassifyBuild(metadata, providerName, platformKey)

	typeIcon := ""
	switch preflight.Compatible {
	case buildselection.CompatibleYes:
		typeIcon = "✓"
	case buildselection.CompatibleNo:
		typeIcon = "✗"
	case buildselection.CompatibleUnknown:
		typeIcon = "⚠"
	}
	rows = append(rows, ui.PreflightRow{Key: "Build type:", Value: string(preflight.Class), Icon: typeIcon})

	if ver != nil && ver.UploadedAt != "" {
		rows = append(rows, ui.PreflightRow{Key: "Uploaded:", Value: ver.UploadedAt})
	}

	var verdict ui.PreflightVerdict
	var verdictText string
	switch preflight.Compatible {
	case buildselection.CompatibleYes:
		verdict = ui.PreflightPass
		verdictText = "Build is compatible with hot reload"
	case buildselection.CompatibleNo:
		verdict = ui.PreflightFail
		verdictText = "This build will NOT work with hot reload"
	default:
		verdict = ui.PreflightWarn
		verdictText = "Could not verify hot-reload compatibility"
	}

	fixHeader := ""
	if preflight.Compatible == buildselection.CompatibleNo {
		fixHeader = "To fix, upload a dev client build:"
	}

	ui.PrintPreflightBox(ui.PreflightBoxInput{
		Rows:        rows,
		Verdict:     verdict,
		VerdictText: verdictText,
		Explanation: preflight.Reason,
		FixHeader:   fixHeader,
		FixCommands: preflight.FixCommands,
		Warnings:    preflight.Warnings,
	})
}

// buildBranchRow creates the "Branch:" row for the pre-flight box.
// Returns nil if no meaningful branch info is available.
func buildBranchRow(buildBranch, currentBranch, source string) *ui.PreflightRow {
	if strings.HasPrefix(source, "branch:") {
		display := buildBranch
		if display == "" {
			display = strings.TrimPrefix(source, "branch:")
		}
		return &ui.PreflightRow{Key: "Branch:", Value: display + " (matches current)", Icon: "✓"}
	}

	if currentBranch != "" && buildBranch != "" {
		return &ui.PreflightRow{Key: "Branch:", Value: buildBranch + " (no match for " + currentBranch + ")", Icon: "⚠"}
	}

	if buildBranch != "" {
		return &ui.PreflightRow{Key: "Branch:", Value: buildBranch}
	}

	return nil
}

type devSessionStarter interface {
	StartSession(ctx context.Context, opts mcppkg.StartSessionOptions) (int, *mcppkg.DeviceSession, error)
}

type devSessionProgressHooks struct {
	startSpinner func(message string)
	stopSpinner  func()
	printInfo    func(format string, args ...interface{})
}

type devSessionStartResult struct {
	index   int
	session *mcppkg.DeviceSession
	err     error
}

func defaultDevSessionProgressHooks() devSessionProgressHooks {
	return devSessionProgressHooks{
		startSpinner: ui.StartSpinner,
		stopSpinner:  ui.StopSpinner,
		printInfo:    ui.PrintInfo,
	}
}

func startDevSessionWithProgress(
	ctx context.Context,
	starter devSessionStarter,
	opts mcppkg.StartSessionOptions,
	hintEvery time.Duration,
	hooks *devSessionProgressHooks,
) (int, *mcppkg.DeviceSession, error) {
	if starter == nil {
		return -1, nil, fmt.Errorf("device session starter is required")
	}

	resolvedHooks := defaultDevSessionProgressHooks()
	if hooks != nil {
		if hooks.startSpinner != nil {
			resolvedHooks.startSpinner = hooks.startSpinner
		}
		if hooks.stopSpinner != nil {
			resolvedHooks.stopSpinner = hooks.stopSpinner
		}
		if hooks.printInfo != nil {
			resolvedHooks.printInfo = hooks.printInfo
		}
	}

	if hintEvery <= 0 {
		hintEvery = 30 * time.Second
	}

	platform := strings.TrimSpace(opts.Platform)
	if platform == "" {
		platform = "cloud"
	}
	spinnerMessage := fmt.Sprintf("Provisioning %s device...", platform)
	resolvedHooks.startSpinner(spinnerMessage)
	defer resolvedHooks.stopSpinner()

	resultCh := make(chan devSessionStartResult, 1)
	go func() {
		index, session, err := starter.StartSession(ctx, opts)
		resultCh <- devSessionStartResult{index: index, session: session, err: err}
	}()

	startedAt := time.Now()
	ticker := time.NewTicker(hintEvery)
	defer ticker.Stop()

	for {
		select {
		case result := <-resultCh:
			return result.index, result.session, result.err
		case <-ticker.C:
			elapsed := time.Since(startedAt).Round(time.Second)
			if elapsed < time.Second {
				elapsed = time.Second
			}
			resolvedHooks.stopSpinner()
			resolvedHooks.printInfo("Still provisioning device... (%s elapsed). Waiting for worker + device readiness.", elapsed)
			resolvedHooks.startSpinner(spinnerMessage)
		}
	}
}

func isContextCanceledError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "context canceled")
}

type workerActionResponse struct {
	SuccessLower *bool  `json:"success"`
	SuccessUpper *bool  `json:"Success"`
	Action       string `json:"action"`
	Error        string `json:"error"`
}

func ensureWorkerActionSucceeded(respBody []byte, expectedAction string) error {
	var resp workerActionResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("failed to parse worker %s response: %w", expectedAction, err)
	}

	if resp.Action != "" && expectedAction != "" && resp.Action != expectedAction {
		return fmt.Errorf("worker returned action=%q, expected %q", resp.Action, expectedAction)
	}

	successKnown := false
	success := false
	if resp.SuccessLower != nil {
		successKnown = true
		success = *resp.SuccessLower
	}
	if resp.SuccessUpper != nil {
		successKnown = true
		success = *resp.SuccessUpper
	}
	if !successKnown {
		return fmt.Errorf("device action %s returned an unexpected response", expectedAction)
	}
	if !success {
		errMsg := strings.TrimSpace(resp.Error)
		if errMsg == "" {
			errMsg = fmt.Sprintf("device action %s failed", expectedAction)
		}
		return fmt.Errorf("%s", errMsg)
	}
	return nil
}

type workerInstallMetadata struct {
	BundleID    string `json:"bundle_id"`
	PackageName string `json:"package_name"`
	AppPackage  string `json:"app_package"`
}

func extractInstallBundleID(respBody []byte) string {
	var meta workerInstallMetadata
	if err := json.Unmarshal(respBody, &meta); err != nil {
		return ""
	}
	for _, candidate := range []string{meta.BundleID, meta.PackageName, meta.AppPackage} {
		if value := strings.TrimSpace(candidate); value != "" {
			return value
		}
	}
	return ""
}

func isUnsupportedWorkerRoute(err error, path string) bool {
	var workerErr *mcppkg.WorkerHTTPError
	if !errors.As(err, &workerErr) {
		return false
	}
	return workerErr.StatusCode == 404 && strings.TrimSpace(workerErr.Path) == strings.TrimSpace(path)
}

type devSessionChecker interface {
	CheckSessionAlive(ctx context.Context, session *mcppkg.DeviceSession) (alive bool, reason string)
}

// defaultDevSessionPollInterval is the interval between backend status checks
// in the dev loop. Overridden in tests for fast execution.
var defaultDevSessionPollInterval = 10 * time.Second

func waitForDevSessionStop(
	ctx context.Context,
	cancel context.CancelFunc,
	checker devSessionChecker,
	session *mcppkg.DeviceSession,
	idleTimeout time.Duration,
) {
	ticker := time.NewTicker(defaultDevSessionPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Second)
			alive, reason := checker.CheckSessionAlive(checkCtx, session)
			checkCancel()
			if !alive {
				ui.PrintWarning("Device session ended (%s). Stopping dev session...", reason)
				ui.PrintDim("  Increase idle timeout: revyl dev --timeout <seconds>")
				ui.PrintDim("  Or set defaults.timeout in .revyl/config.yaml")
				cancel()
				return
			}
		}
	}
}

func isNoSessionAtIndexError(err error, index int) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), fmt.Sprintf("no session at index %d", index))
}

// maskPresignedURL redacts the query string from presigned S3/GCS URLs to
// prevent leaking time-limited auth tokens in logs or CI output.
func maskPresignedURL(rawURL string) string {
	if idx := strings.Index(rawURL, "?"); idx > 0 {
		return rawURL[:idx] + "?<redacted>"
	}
	return rawURL
}
