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
	Short: "Expo-first local development loop",
	Long: `Start and manage the iterative local development loop.

By default this starts hot reload, provisions a device, installs the latest
dev build, and opens a live viewer.`,
	RunE: runDevStart,
}

var devStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a hot reload device loop (Expo)",
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
		runHotReloadProvider = "expo"
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
		openTestHotReloadProvider = "expo"
		return runOpenTest(cmd, args)
	},
}

var devTestCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a test with hot reload defaults",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		createTestHotReload = true
		createTestHotReloadProvider = "expo"
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
	devTestCreateCmd.Flags().BoolVar(&createTestNoSync, "no-sync", false, "Skip adding test to .revyl/config.yaml")
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
	if !strings.Contains(strings.ToLower(cfg.Build.System), "expo") {
		return fmt.Errorf("`revyl dev` currently supports Expo projects only (build.system=%q)", cfg.Build.System)
	}
	if !cfg.HotReload.IsConfigured() {
		ui.PrintError("Hot reload is not configured.")
		ui.PrintInfo("Run: revyl init --hotreload")
		return fmt.Errorf("hot reload not configured")
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
	provider, providerCfg, err := registry.SelectProvider(&cfg.HotReload, "expo", cwd)
	if err != nil {
		return fmt.Errorf("expo hot reload is not configured: %w", err)
	}
	if provider.Name() != "expo" {
		return fmt.Errorf("`revyl dev` currently supports Expo only")
	}
	if providerCfg == nil || strings.TrimSpace(providerCfg.AppScheme) == "" {
		return fmt.Errorf("hotreload.providers.expo.app_scheme is required (run `revyl init --hotreload`)")
	}
	if !provider.IsSupported() {
		return fmt.Errorf("%s hot reload is not yet supported", provider.DisplayName())
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

		latestVersion, latestErr := client.GetLatestBuildVersion(cmd.Context(), selectedAppID)
		if latestErr != nil {
			return fmt.Errorf("failed to resolve latest build for app %s: %w", selectedAppID, latestErr)
		}
		if latestVersion != nil {
			buildVersionID = latestVersion.ID
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
	ui.PrintInfo("Revyl Dev Loop")
	ui.Println()
	ui.PrintInfo("Provider: Expo")
	ui.PrintInfo("Device platform: %s", devicePlatform)
	if platformKey != "" {
		ui.PrintInfo("Build platform key: %s", platformKey)
	}
	if selectedAppID != "" {
		ui.PrintInfo("App ID: %s", selectedAppID)
	}
	ui.PrintInfo("Dev build: %s", buildVersionID)
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

	deviceMgr, err := getDeviceSessionMgr(cmd)
	if err != nil {
		return err
	}

	_, session, err := deviceMgr.StartSession(ctx, mcppkg.StartSessionOptions{
		Platform:       devicePlatform,
		AppID:          selectedAppID,
		BuildVersionID: buildVersionID,
		AppURL:         strings.TrimSpace(buildDetail.DownloadURL),
		AppPackage:     strings.TrimSpace(buildDetail.PackageName),
		AppLink:        startResult.DeepLinkURL,
		IdleTimeout:    time.Duration(timeout) * time.Second,
	})
	if err != nil {
		if isUserCanceled(err) {
			return nil
		}
		return err
	}
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
	installRespBody, err := deviceMgr.WorkerRequestForSession(ctx, session.Index, "POST", "/install", installBody)
	if err != nil {
		if isUserCanceled(err) {
			return nil
		}
		return fmt.Errorf("device started but app install failed: %w", err)
	}
	if err := ensureWorkerActionSucceeded(installRespBody, "install"); err != nil {
		return fmt.Errorf("device started but app install failed: %w", err)
	}

	bundleID := strings.TrimSpace(buildDetail.PackageName)
	if bundleID == "" {
		bundleID = extractInstallBundleID(installRespBody)
	}
	if bundleID != "" {
		ui.PrintInfo("Launching dev client app...")
		launchRespBody, err := deviceMgr.WorkerRequestForSession(ctx, session.Index, "POST", "/launch", map[string]string{
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
	openURLRespBody, err := deviceMgr.WorkerRequestForSession(ctx, session.Index, "POST", "/open_url", map[string]string{
		"url": deepLinkURL,
	})
	if err != nil {
		if isUserCanceled(err) {
			return nil
		}
		if isUnsupportedWorkerRoute(err, "/open_url") {
			manualDeepLinkRequired = true
			ui.PrintWarning("Worker does not support /open_url; automatic deep-link navigation is unavailable for this session")
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

	printDevReadyFooter(session.ViewerURL, startResult.DeepLinkURL, manualDeepLinkRequired)

	if openBrowser {
		_ = ui.OpenBrowser(session.ViewerURL)
	}

	waitForDevSessionStop(ctx, cancel, deviceMgr, session.Index, time.Second)
	return nil
}

func isCIEnvironment() bool {
	return strings.TrimSpace(os.Getenv("CI")) != "" ||
		strings.TrimSpace(os.Getenv("GITHUB_ACTIONS")) != ""
}

func printDevReadyFooter(viewerURL, deepLinkURL string, manualDeepLinkRequired bool) {
	ui.Println()
	ui.PrintSuccess("Dev loop ready")
	ui.PrintLink("Viewer", viewerURL)
	ui.PrintInfo("Deep Link: %s", deepLinkURL)
	if manualDeepLinkRequired {
		ui.PrintWarning("Deep link was not opened automatically on this worker. Use the Deep Link above in the device browser/dev client.")
	}
	ui.PrintDim("Press Ctrl+C to stop hot reload and release the device")
	ui.Println()
	ui.PrintInfo("Try device interactions:")
	ui.PrintDim("  revyl device tap --target \"Login button\"")
	ui.PrintDim("  revyl device screenshot")
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
		return fmt.Errorf("worker %s response missing success field", expectedAction)
	}
	if !success {
		errMsg := strings.TrimSpace(resp.Error)
		if errMsg == "" {
			errMsg = fmt.Sprintf("worker reported %s failure", expectedAction)
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

type devSessionLookup interface {
	GetSession(index int) *mcppkg.DeviceSession
}

func waitForDevSessionStop(
	ctx context.Context,
	cancel context.CancelFunc,
	sessions devSessionLookup,
	sessionIndex int,
	pollInterval time.Duration,
) {
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if sessions.GetSession(sessionIndex) != nil {
				continue
			}
			ui.PrintWarning("Device session ended (likely idle timeout). Stopping dev session...")
			cancel()
			return
		}
	}
}

func isNoSessionAtIndexError(err error, index int) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), fmt.Sprintf("no session at index %d", index))
}
