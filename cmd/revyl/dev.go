package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/buildselection"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/devpush"
	"github.com/revyl/cli/internal/execution"
	"github.com/revyl/cli/internal/hotreload"
	_ "github.com/revyl/cli/internal/hotreload/providers" // Register providers
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/sigutil"
	"github.com/revyl/cli/internal/ui"
)

var (
	devStartPlatform       string
	devStartPlatformKey    string
	devStartAppID          string
	devStartBuildVerID     string
	devStartBuild          bool
	devStartNoBuild        bool
	devStartRemote         bool
	devStartTunnelURL      string
	devStartLaunchVars     []string
	devStartPort           int
	devStartTimeout        int
	devStartOpen           bool
	devStartNoOpen         bool
	devStartForceHotReload bool

	devTestRunPlatform    string
	devTestRunPlatformKey string
)

func shouldAttemptHotReloadAutoSetup(cfg *config.ProjectConfig) bool {
	if cfg == nil || cfg.HotReload.IsConfigured() {
		return false
	}
	if len(cfg.Build.Platforms) == 0 {
		return true
	}
	return !isRebuildOnlyBuildSystem(cfg.Build.System)
}

var devCmd = &cobra.Command{
	Use:   "dev",
	Short: "Start or attach a dev loop on a cloud device session",
	Long: `Start and manage local development loops backed by cloud device sessions.

Auto-detects your project type (Expo, React Native, Swift, Android),
starts hot reload, provisions a cloud device, installs the latest
dev build, and opens a live viewer.

Each dev loop is a named "dev context" in the current worktree. Use
--context to manage multiple loops in the same repo, or let separate
worktrees use the default context automatically.

After starting, interact with the device using revyl device commands:
  revyl device screenshot
  revyl device tap --target "Login button"`,
	Example: `  revyl dev
  revyl dev --platform ios
  revyl dev --context ios-main --platform ios
  revyl dev --platform android --no-open
  revyl dev attach active
  revyl dev list`,
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

var (
	rebuildWait    bool
	rebuildTimeout int
	rebuildJSON    bool
)

var devRebuildCmd = &cobra.Command{
	Use:   "rebuild",
	Short: "Trigger a rebuild in a running dev session",
	Long: `Send a rebuild signal to a running revyl dev process.
For use by agents, automation, and MCP tools.

With --wait, blocks until the rebuild completes and reports the result.
With --json, outputs machine-readable status (implies --wait).`,
	Example: `  revyl dev rebuild
  revyl dev rebuild --wait
  revyl dev rebuild --wait --json
  revyl dev rebuild --wait --timeout 60`,
	RunE: runDevRebuild,
}

var devStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check dev session state",
	Long:  "Report whether a dev session is running and its current state.\nAlways outputs JSON.",
	Example: `  revyl dev status
  revyl dev status | jq .running
  revyl dev status | jq .last_rebuild_status`,
	RunE: runDevStatus,
}

func init() {
	devCmd.PersistentFlags().String("context", "", "Dev context name (defaults to 'default')")

	registerDevStartFlags(devCmd)
	registerDevStartFlags(devStartCmd)

	devCmd.AddCommand(devStartCmd)
	devCmd.AddCommand(devRebuildCmd)
	devCmd.AddCommand(devStatusCmd)
	devCmd.AddCommand(devTestCmd)
	devCmd.AddCommand(devAttachCmd)
	devCmd.AddCommand(devListCmd)
	devCmd.AddCommand(devContextUseCmd)
	devCmd.AddCommand(devStopCmd)

	devStopCmd.Flags().BoolVar(&devStopAll, "all", false, "Stop all dev contexts in the current worktree")

	devRebuildCmd.Flags().BoolVar(&rebuildWait, "wait", false, "Block until rebuild completes")
	devRebuildCmd.Flags().IntVar(&rebuildTimeout, "timeout", 120, "Timeout in seconds (with --wait)")
	devRebuildCmd.Flags().BoolVar(&rebuildJSON, "json", false, "Output result as JSON (implies --wait)")

	devTestCmd.AddCommand(devTestRunCmd)
	devTestCmd.AddCommand(devTestOpenCmd)
	devTestCmd.AddCommand(devTestCreateCmd)

	// dev test run flags (hotreload is always enabled in this namespace)
	devTestRunCmd.Flags().IntVarP(&runRetries, "retries", "r", 1, "Number of retry attempts (1-5)")
	devTestRunCmd.Flags().StringVarP(&runBuildID, "build-id", "b", "", "Specific build version ID")
	devTestRunCmd.Flags().BoolVar(&runNoWait, "no-wait", false, "Exit after test starts without waiting")
	devTestRunCmd.Flags().BoolVar(&runOpen, "open", false, "Open report in browser when complete")
	devTestRunCmd.Flags().IntVarP(&runTimeout, "timeout", "t", execution.DefaultRunTimeoutSeconds, "Timeout in seconds")
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

	// dev test create flags
	devTestCreateCmd.Flags().StringVar(&createTestPlatform, "platform", "ios", "Target platform (android, ios)")
	devTestCreateCmd.Flags().StringVar(&createTestAppID, "app", "", "App ID to associate with the test")
	devTestCreateCmd.Flags().BoolVar(&createTestNoOpen, "no-open", false, "Skip opening browser to test editor")
	devTestCreateCmd.Flags().BoolVar(&createTestForce, "force", false, "Update existing test if name already exists")
	devTestCreateCmd.Flags().BoolVar(&createTestDryRun, "dry-run", false, "Show what would be created without creating")
	devTestCreateCmd.Flags().StringVar(&createTestFromFile, "from-file", "", "Create test from YAML file (copies to .revyl/tests/ and pushes)")
	devTestCreateCmd.Flags().IntVar(&createTestHotReloadPort, "port", 8081, "Port for dev server")
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
	cmd.Flags().BoolVar(&devStartNoBuild, "no-build", false, "Never run build commands; require a pre-existing build")
	cmd.Flags().BoolVar(&devStartRemote, "remote", false, "Build native iOS changes on a remote Revyl build runner")
	cmd.Flags().StringVar(&devStartTunnelURL, "tunnel", "", "Use an external Expo tunnel URL or dev-client deep link instead of the Revyl relay")
	cmd.Flags().StringArrayVar(&devStartLaunchVars, "launch-var", nil, "Org launch variable key or ID to apply when starting the device session (repeatable)")
	cmd.Flags().IntVar(&devStartPort, "port", 8081, "Port for local dev server")
	cmd.Flags().IntVar(&devStartTimeout, "timeout", 300, "Device idle timeout in seconds")
	cmd.Flags().BoolVar(&devStartOpen, "open", true, "Open live device viewer in browser")
	cmd.Flags().BoolVar(&devStartNoOpen, "no-open", false, "Do not open the live device viewer in browser")
	cmd.Flags().BoolVar(&devStartForceHotReload, "force-hot-reload", false, "Launch even if Expo relay readiness cannot be proven")
}

func withDevStartLaunchVars(opts mcppkg.StartSessionOptions) mcppkg.StartSessionOptions {
	if len(devStartLaunchVars) == 0 {
		return opts
	}
	opts.LaunchVars = append([]string(nil), devStartLaunchVars...)
	return opts
}

func warnLaunchVarsIgnoredForReusedDevSession() {
	if len(devStartLaunchVars) == 0 {
		return
	}
	ui.PrintWarning("Ignoring --launch-var because revyl dev reused an existing device session; launch vars apply only when a session starts. Run `revyl dev stop` and rerun to apply them.")
}

func formatDevActionDebugPayload(body map[string]string) string {
	if len(body) == 0 {
		return ""
	}
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := body[key]
		if key == "app_url" {
			value = maskPresignedURL(value)
		}
		parts = append(parts, fmt.Sprintf("%s=%s", key, value))
	}
	return " " + strings.Join(parts, " ")
}

func debugDevActionRequest(action string, sessionIndex int, body map[string]string) time.Time {
	started := time.Now()
	ui.PrintDebug(
		"dev action %s request ts=%s session=%d%s",
		action,
		started.Format(time.RFC3339Nano),
		sessionIndex,
		formatDevActionDebugPayload(body),
	)
	return started
}

func debugDevActionResult(action string, sessionIndex int, started time.Time, respBody []byte, err error) {
	elapsedMs := time.Since(started).Milliseconds()
	now := time.Now().Format(time.RFC3339Nano)
	if err != nil {
		ui.PrintDebug(
			"dev action %s error ts=%s session=%d duration_ms=%d error=%v",
			action,
			now,
			sessionIndex,
			elapsedMs,
			err,
		)
		return
	}
	ui.PrintDebug(
		"dev action %s ack ts=%s session=%d duration_ms=%d response=%s",
		action,
		now,
		sessionIndex,
		elapsedMs,
		strings.TrimSpace(string(respBody)),
	)
}

type externalTunnelInput struct {
	tunnelURL    string
	deepLinkURL  string
	fromDeepLink bool
}

func parseExternalTunnelInput(raw string) (externalTunnelInput, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return externalTunnelInput{}, nil
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" {
		return externalTunnelInput{}, fmt.Errorf("--tunnel must be an https:// tunnel URL or an Expo dev-client deep link")
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "http" || scheme == "https" {
		if parsed.Host == "" {
			return externalTunnelInput{}, fmt.Errorf("--tunnel URL must include a host")
		}
		return externalTunnelInput{tunnelURL: value}, nil
	}

	nestedTunnelURL := strings.TrimSpace(parsed.Query().Get("url"))
	if nestedTunnelURL == "" {
		return externalTunnelInput{}, fmt.Errorf("--tunnel deep link must include a url=<https://...> parameter")
	}
	nested, err := url.Parse(nestedTunnelURL)
	if err != nil || nested.Host == "" || (nested.Scheme != "http" && nested.Scheme != "https") {
		return externalTunnelInput{}, fmt.Errorf("--tunnel deep link url parameter must be an http(s) tunnel URL")
	}

	return externalTunnelInput{
		tunnelURL:    nestedTunnelURL,
		deepLinkURL:  value,
		fromDeepLink: true,
	}, nil
}

func inferExpoProviderConfig(provider hotreload.Provider, cwd string) *config.ProviderConfig {
	info, err := provider.GetProjectInfo(cwd)
	if err != nil || info == nil {
		return nil
	}
	return provider.GetDefaultConfig(info)
}

func externalExpoProviderConfig(provider hotreload.Provider, cfg *config.ProjectConfig, cwd string, hasDeepLink bool) (*config.ProviderConfig, error) {
	var providerCfg *config.ProviderConfig
	if cfg != nil {
		providerCfg = cfg.HotReload.GetProviderConfig("expo")
	}
	if providerCfg == nil {
		providerCfg = &config.ProviderConfig{}
	}

	if strings.TrimSpace(providerCfg.AppScheme) == "" && !hasDeepLink {
		if inferred := inferExpoProviderConfig(provider, cwd); inferred != nil && strings.TrimSpace(inferred.AppScheme) != "" {
			copied := *providerCfg
			copied.AppScheme = strings.TrimSpace(inferred.AppScheme)
			if copied.Port == 0 {
				copied.Port = inferred.Port
			}
			if copied.PlatformKeys == nil {
				copied.PlatformKeys = inferred.PlatformKeys
			}
			providerCfg = &copied
		}
	}

	if strings.TrimSpace(providerCfg.AppScheme) == "" && !hasDeepLink {
		return nil, fmt.Errorf("--tunnel with a raw tunnel URL needs an Expo app scheme; pass the full Expo dev-client deep link instead, or run `revyl init --provider expo`")
	}

	return providerCfg, nil
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

	ctxName, err := resolveDevContextName(cwd, getDevContextFlag(cmd))
	if err != nil {
		return err
	}
	if existing, _ := loadDevContext(cwd, ctxName); existing != nil {
		pidPath := devCtxPIDPath(cwd, ctxName)
		if running, _ := isDevCtxProcessAlive(existing.PID, existing.StartedAtNano, pidPath); running {
			printDevContextAlreadyRunning(existing)
			return nil
		}
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	cfg, err := config.LoadProjectConfig(configPath)
	if err != nil {
		ui.PrintError("Project not initialized. Run 'revyl init' first.")
		return fmt.Errorf("project not initialized")
	}

	externalTunnel, err := parseExternalTunnelInput(devStartTunnelURL)
	if err != nil {
		return err
	}

	if devStartNoBuild && devStartBuild {
		return fmt.Errorf("use either --no-build or --build, not both")
	}
	if devStartRemote {
		return runDevRemoteRebuildOnly(cmd, cfg, configPath, cwd, apiKey, ctxName)
	}

	noBuild := devStartNoBuild || cfg.Build.NoBuild
	if noBuild && devStartBuild {
		noBuild = false
	}

	// When --tunnel is provided, the customer manages their own dev server and
	// tunnel. Skip hot reload auto-setup and the rebuild-only loop entirely.
	var provider hotreload.Provider
	var providerCfg *config.ProviderConfig
	useRebuildOnlyLoop := false

	if externalTunnel.tunnelURL != "" {
		registry := hotreload.DefaultRegistry()
		provider, err = registry.GetProvider("expo")
		if err != nil {
			return fmt.Errorf("--tunnel requires Expo support: %w", err)
		}
		providerCfg, err = externalExpoProviderConfig(provider, cfg, cwd, externalTunnel.fromDeepLink)
		if err != nil {
			return err
		}
	} else {
		// Determine whether we have a supported hot reload provider. If not,
		// native projects (Gradle/Xcode/Swift) use a rebuild-only dev loop.
		if !cfg.HotReload.IsConfigured() {
			if shouldAttemptHotReloadAutoSetup(cfg) {
				ui.PrintInfo("Dev mode not configured yet. Setting up...")
				ui.Println()

				devMode, _ := cmd.Flags().GetBool("dev")
				var setupClient *api.Client
				if apiKey != "" {
					setupClient = api.NewClientWithDevMode(apiKey, devMode)
				}

				ready := wizardHotReloadSetup(context.Background(), setupClient, cfg, configPath, cwd, false, nil, "")
				if !ready || !cfg.HotReload.IsConfigured() {
					if hasOnlyPlaceholderBuildPlatforms(cfg) {
						return fmt.Errorf("dev mode is not ready yet: configure at least one build.platforms.<key>.command and build.platforms.<key>.output")
					}
					ui.PrintError("Could not auto-configure dev mode.")
					ui.PrintInfo("Try: revyl init --provider expo")
					return fmt.Errorf("dev mode auto-setup failed")
				}
				ui.Println()
			} else if len(cfg.Build.Platforms) > 0 {
				useRebuildOnlyLoop = true
			}
		}

		if !useRebuildOnlyLoop {
			registry := hotreload.DefaultRegistry()
			var selectErr error
			provider, _, selectErr = registry.SelectProvider(&cfg.HotReload, "", cwd)
			if selectErr != nil || !provider.IsSupported() {
				useRebuildOnlyLoop = len(cfg.Build.Platforms) > 0
			}
		}
	}

	if useRebuildOnlyLoop {
		return runDevRebuildOnly(cmd, cfg, configPath, cwd, apiKey, ctxName)
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

	if externalTunnel.tunnelURL == "" {
		registry := hotreload.DefaultRegistry()
		provider, providerCfg, err = registry.SelectProvider(&cfg.HotReload, "", cwd)
		if err != nil {
			return fmt.Errorf("dev mode is not configured: %w", err)
		}
		if !provider.IsSupported() {
			return fmt.Errorf("%s dev mode is not yet supported (coming soon)", provider.DisplayName())
		}
		if provider.Name() == "expo" && (providerCfg == nil || strings.TrimSpace(providerCfg.AppScheme) == "") {
			return fmt.Errorf("hotreload.providers.expo.app_scheme is required for Expo dev mode (run `revyl init --provider expo` or `revyl config set hotreload.app-scheme <scheme>`)")
		}
	}

	devicePlatform := requestedPlatform
	platformKey := ""
	if strings.TrimSpace(devStartPlatformKey) != "" {
		platformKey, devicePlatform, err = resolveHotReloadBuildPlatform(cfg, providerCfg, strings.TrimSpace(devStartPlatformKey), requestedPlatform, noBuild)
		if err != nil {
			return err
		}
	}

	ctxName, err = resolveDevCtxPlatformConflict(cwd, ctxName, devicePlatform, cmd.Flags().Changed("context"))
	if err != nil {
		return err
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
				platformKey, devicePlatform, err = resolveHotReloadBuildPlatform(cfg, providerCfg, requestedPlatform, requestedPlatform, noBuild)
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

	needsDevBuild := devStartBuild || buildVersionID == ""

	if noBuild {
		needsDevBuild = false
		if buildVersionID == "" {
			return fmt.Errorf(
				"no existing build found and --no-build is active; " +
					"upload a build first (revyl build upload) or pass --build-version-id",
			)
		}
	}

	// When no uploaded build was found and --build was NOT explicitly requested,
	// try to find a local Xcode simulator .app from DerivedData. This avoids
	// running the full configured build when the developer already built in Xcode.
	var localSimBuildPath string
	if needsDevBuild && !devStartBuild && build.IsIOSPlatformKey(requestedPlatform) {
		simBuild := build.FindSimulatorBuild(cwd)
		if simBuild != nil {
			if platformKey == "" {
				platformKey, devicePlatform, err = resolveHotReloadBuildPlatform(cfg, providerCfg, requestedPlatform, requestedPlatform, noBuild)
				if err != nil {
					return err
				}
			}
			cfgForKey, ok := cfg.Build.Platforms[platformKey]
			if ok && strings.TrimSpace(cfgForKey.AppID) != "" {
				localAppID := strings.TrimSpace(cfgForKey.AppID)
				ui.PrintInfo("Found local simulator build: %s", filepath.Base(simBuild.Path))
				if simBuild.IsStale(4 * time.Hour) {
					ui.PrintWarning("Local simulator build is %s old (built %s)",
						formatBuildAge(simBuild.Age()), simBuild.ModTime.Format("Mon Jan 2 15:04"))
				}
				ui.StartSpinner("Uploading local simulator build...")
				_, _, uploadErr := uploadExistingArtifact(cmd.Context(), client, simBuild.Path, localAppID, cwd)
				ui.StopSpinner()
				if uploadErr == nil {
					needsDevBuild = false
					selectedAppID = localAppID
					localSimBuildPath = simBuild.Path
					ui.PrintSuccess("Uploaded local simulator build")
					latestVersion, latestErr := client.GetLatestBuildVersion(cmd.Context(), localAppID)
					if latestErr != nil {
						return fmt.Errorf("failed to resolve uploaded local build: %w", latestErr)
					}
					if latestVersion != nil {
						buildVersionID = latestVersion.ID
					}
				} else {
					ui.PrintDebug("local simulator upload failed, falling back to configured build: %v", uploadErr)
				}
			}
		}
	}

	if needsDevBuild {
		if appIDOverride != "" {
			return fmt.Errorf("no builds found for app %s; provide --build-version-id or use config-backed dev flow", appIDOverride)
		}
		if platformKey == "" {
			platformKey, devicePlatform, err = resolveHotReloadBuildPlatform(cfg, providerCfg, requestedPlatform, requestedPlatform, noBuild)
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

	var managerProviderName string
	if externalTunnel.tunnelURL != "" {
		managerProviderName = "expo"
	} else {
		managerProviderName = provider.Name()
	}
	manager := hotreload.NewManager(managerProviderName, providerCfg, cwd)
	manager.ConfigureFromHotReloadConfig(&cfg.HotReload, client)
	manager.SetTargetPlatform(devicePlatform)
	manager.SetForceHotReload(devStartForceHotReload)
	if externalTunnel.tunnelURL != "" {
		manager.SetExternalTunnelURL(externalTunnel.tunnelURL)
		if externalTunnel.deepLinkURL != "" {
			manager.SetExternalDeepLinkURL(externalTunnel.deepLinkURL)
		}
	}
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
				ui.PrintWarning("Force exiting — device session may not be released immediately")
				forceCleanupDevContext(cwd, ctxName)
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

	printHotReloadReady(provider.DisplayName(), startResult)
	deviceMgr, err := getDeviceSessionMgr(cmd)
	if err != nil {
		return err
	}

	var session *mcppkg.DeviceSession
	sessionOwned := true
	if savedCtx, _ := loadDevContext(cwd, ctxName); savedCtx != nil && savedCtx.SessionID != "" {
		reuse := tryReuseDevContextSession(ctx, deviceMgr, savedCtx, devicePlatform)
		if reuse != nil {
			session = reuse.Session
			sessionOwned = reuse.SessionOwned
			warnLaunchVarsIgnoredForReusedDevSession()
		}
	}

	if session == nil {
		ui.PrintInfo("Starting cloud device session...")
		_, session, err = startDevSessionWithProgress(
			ctx,
			deviceMgr,
			withDevStartLaunchVars(mcppkg.StartSessionOptions{
				Platform:       devicePlatform,
				AppID:          selectedAppID,
				BuildVersionID: buildVersionID,
				AppURL:         strings.TrimSpace(buildDetail.DownloadURL),
				AppPackage:     strings.TrimSpace(buildDetail.PackageName),
				AppLink:        startResult.DeepLinkURL,
				IdleTimeout:    time.Duration(timeout) * time.Second,
			}),
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
	}

	if sessionOwned {
		defer func() {
			if stopErr := deviceMgr.StopSession(context.Background(), session.Index); stopErr != nil {
				if isNoSessionAtIndexError(stopErr, session.Index) {
					return
				}
				ui.PrintWarning("Failed to stop device session %d: %v", session.Index, stopErr)
			}
		}()
	}

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
	installStarted := debugDevActionRequest("install", session.Index, installBody)
	installRespBody, err := deviceMgr.WorkerRequestForSession(ctx, session.Index, "/install", installBody)
	debugDevActionResult("install", session.Index, installStarted, installRespBody, err)
	if err != nil {
		if isUserCanceled(err) {
			return nil
		}
		return fmt.Errorf("device started but app install failed: %w\nhint: try re-running with --build to force a fresh build+upload", err)
	}
	ui.PrintDebug("install worker response: %s", string(installRespBody))
	if err := ensureWorkerActionSucceeded(installRespBody, "install"); err != nil {
		return fmt.Errorf("device started but app install failed: %w\nhint: try re-running with --build to force a fresh build+upload", err)
	}

	bundleID := strings.TrimSpace(buildDetail.PackageName)
	if bundleID == "" {
		bundleID = extractInstallBundleID(installRespBody)
	}
	if bundleID != "" {
		ui.PrintInfo("Launching dev client app...")
		launchPayload := map[string]string{
			"bundle_id": bundleID,
		}
		if provider.Name() == "react-native" && devicePlatform == "ios" {
			if parsed, err := url.Parse(startResult.TunnelURL); err == nil {
				host := parsed.Hostname()
				port := parsed.Port()
				if port == "" && parsed.Scheme == "https" {
					port = "443"
				} else if port == "" {
					port = "80"
				}
				launchPayload["packager_host"] = host + ":" + port
				launchPayload["packager_scheme"] = parsed.Scheme
			}
		}
		launchStarted := debugDevActionRequest("launch", session.Index, launchPayload)
		launchRespBody, err := deviceMgr.WorkerRequestForSession(ctx, session.Index, "/launch", launchPayload)
		debugDevActionResult("launch", session.Index, launchStarted, launchRespBody, err)
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
	manualDeepLinkRequired := false
	isBareRN := provider.Name() == "react-native"

	if isBareRN {
		// Bare React Native does not have a deep-link contract like Expo's
		// dev client. On iOS the packager host was injected into NSUserDefaults
		// via the /launch packager_host parameter. On Android the relay tunnel
		// handles port forwarding. Skip the open_url step.
		ui.PrintInfo("Metro tunnel: %s", startResult.TunnelURL)
		ui.PrintDim("  Bare React Native hot reload is active. The app loads its JS bundle")
		ui.PrintDim("  from the Metro server via the relay tunnel.")
	} else {
		deepLinkFailureHint := "hint: verify hotreload.providers.expo.app_scheme and try hotreload.providers.expo.use_exp_prefix: true"
		if externalTunnel.fromDeepLink {
			deepLinkFailureHint = "hint: verify the Expo dev-client link matches the installed build; if the scheme or native config changed, rebuild the dev client"
		}
		if deepLinkURL == "" {
			return fmt.Errorf("hot reload started but deep link URL is empty")
		}
		ui.PrintInfo("Opening hot reload deep link...")
		openURLPayload := map[string]string{
			"url": deepLinkURL,
		}
		openURLStarted := debugDevActionRequest("open_url", session.Index, openURLPayload)
		openURLRespBody, err := deviceMgr.WorkerRequestForSession(ctx, session.Index, "/open_url", openURLPayload)
		debugDevActionResult("open_url", session.Index, openURLStarted, openURLRespBody, err)
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
					"deep-link navigation failed: %w\n%s",
					err,
					deepLinkFailureHint,
				)
			}
		} else {
			if err := ensureWorkerActionSucceeded(openURLRespBody, "open_url"); err != nil {
				return fmt.Errorf(
					"deep-link navigation failed: %w\n%s",
					err,
					deepLinkFailureHint,
				)
			}
		}
	}

	viewerURL := devSessionViewerURL(session, devMode)
	printDevReadyFooter(viewerURL, startResult.DeepLinkURL, manualDeepLinkRequired, isBareRN, noBuild, ctxName, session.Index)

	// Compute packager host once for the rebuild loop. Only bare RN on iOS
	// needs this — the worker writes RCT_jsLocation to NSUserDefaults so the
	// app fetches its JS bundle from the relay tunnel instead of localhost.
	hrPackagerHost := ""
	hrPackagerScheme := ""
	if isBareRN && devicePlatform == "ios" {
		if parsed, pErr := url.Parse(startResult.TunnelURL); pErr == nil {
			host := parsed.Hostname()
			port := parsed.Port()
			if port == "" && parsed.Scheme == "https" {
				port = "443"
			} else if port == "" {
				port = "80"
			}
			hrPackagerHost = host + ":" + port
			hrPackagerScheme = parsed.Scheme
		}
	}

	if openBrowser {
		_ = ui.OpenBrowser(viewerURL)
	}

	pidPath := devCtxPIDPath(cwd, ctxName)
	if err := os.MkdirAll(filepath.Dir(pidPath), 0755); err != nil {
		return fmt.Errorf("failed to create dev context directory: %w", err)
	}
	startNonce := time.Now().UnixNano()
	_ = writeDevCtxPIDFile(pidPath, os.Getpid(), startNonce)
	defer os.Remove(pidPath)

	devCtx := &DevContext{
		Name:          ctxName,
		Platform:      devicePlatform,
		PlatformKey:   platformKey,
		Provider:      provider.Name(),
		SessionID:     session.SessionID,
		SessionIndex:  session.Index,
		SessionOwned:  sessionOwned,
		ViewerURL:     viewerURL,
		TunnelURL:     startResult.TunnelURL,
		DeepLinkURL:   startResult.DeepLinkURL,
		Transport:     startResult.Transport,
		RelayID:       startResult.RelayID,
		PID:           os.Getpid(),
		StartedAtNano: startNonce,
		State:         devContextStateRunning,
		Port:          devStartPort,
		CreatedAt:     time.Now(),
		LastActivity:  time.Now(),
	}
	_ = saveDevContext(cwd, devCtx)
	_ = setCurrentDevContext(cwd, ctxName)
	defer func() {
		devCtx.State = devContextStateStopped
		devCtx.PID = 0
		devCtx.TunnelURL = ""
		devCtx.DeepLinkURL = ""
		devCtx.Transport = ""
		devCtx.RelayID = ""
		if sessionOwned {
			devCtx.SessionID = ""
			devCtx.SessionIndex = 0
			devCtx.ViewerURL = ""
		}
		_ = saveDevContext(cwd, devCtx)
	}()

	sigusr1 := make(chan os.Signal, 1)
	if sigutil.RebuildSignal != nil {
		signal.Notify(sigusr1, sigutil.RebuildSignal)
	}
	defer signal.Stop(sigusr1)

	deviceMgr.StopIdleTimer(session.Index)

	hrTransport := devpush.NewTransport(client, deviceMgr)
	hrManifestPath := devCtxManifestPath(cwd, ctxName)
	hrStatusPath := devCtxStatusPath(cwd, ctxName)
	hrCachedManifest, manifestLoadErr := build.LoadManifest(hrManifestPath)
	if manifestLoadErr != nil {
		ui.PrintDim("  Could not load cached manifest: %v", manifestLoadErr)
	}
	// When the initial build came from DerivedData and no manifest exists on
	// disk, seed it from the DerivedData .app so the first [r] rebuild can
	// use delta pushes instead of a full upload.
	if hrCachedManifest == nil && localSimBuildPath != "" {
		if m, mErr := build.BuildManifest(localSimBuildPath); mErr == nil {
			hrCachedManifest = m
			_ = build.SaveManifest(m, hrManifestPath)
			ui.PrintDebug("seeded delta manifest from DerivedData build")
		}
	}
	var hrBgUploadCancel context.CancelFunc
	defer func() {
		if hrBgUploadCancel != nil {
			hrBgUploadCancel()
		}
	}()
	hrRebuildCount := 0

	// Event loop: session liveness + [r]/SIGUSR1 rebuild + [q] quit.
	stdinKeys, restoreTerminal, keybindsEnabled := readStdinKeys(ctx)
	defer restoreTerminal()
	ticker := time.NewTicker(defaultDevSessionPollInterval)
	defer ticker.Stop()

	var lastRebuildStart time.Time
	for {
		var doRebuild bool
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Second)
			alive, reason := deviceMgr.CheckSessionAlive(checkCtx, session)
			checkCancel()
			if !alive {
				ui.PrintWarning("Device session ended (%s). Stopping dev session...", reason)
				cancel()
				return nil
			}
		case <-sigusr1:
			if noBuild {
				ui.PrintWarning("Rebuild disabled (--no-build). Use your local dev server to reload.")
				continue
			}
			doRebuild = true
		case key := <-stdinKeys:
			switch key {
			case 'r':
				if noBuild {
					ui.PrintWarning("Rebuild disabled (--no-build). Use your local dev server to reload.")
					continue
				}
				doRebuild = true
			case 'q':
				ui.Println()
				ui.PrintInfo("Stopping dev session...")
				cancel()
				return nil
			}
		}

		if !doRebuild {
			continue
		}

		if !lastRebuildStart.IsZero() && time.Since(lastRebuildStart) < rebuildCooldown {
			if drainStdinKeys(stdinKeys) {
				ui.Println()
				ui.PrintInfo("Stopping dev session...")
				cancel()
				return nil
			}
			continue
		}
		lastRebuildStart = time.Now()

		hrRebuildCount++
		result := devBuildAndDeltaPush(ctx, cancel, cmd, cfg, configPath, apiKey, platformKey, devicePlatform,
			bundleID, session, deviceMgr, client, hrTransport, hrCachedManifest, hrManifestPath, cwd, hrPackagerHost, hrPackagerScheme)

		if drainStdinKeys(stdinKeys) {
			ui.Println()
			ui.PrintInfo("Stopping dev session...")
			cancel()
			return nil
		}

		writeDevStatus(
			hrStatusPath,
			session,
			viewerURL,
			startResult.TunnelURL,
			startResult.DeepLinkURL,
			startResult.Transport,
			devicePlatform,
			hrRebuildCount,
			hrCachedManifest != nil,
			result,
		)

		if result.buildErr != nil {
			ui.PrintWarning("Rebuild failed: %v", result.buildErr)
			if keybindsEnabled {
				ui.PrintDim("  [r] retry rebuild    [q] quit")
			}
			continue
		}
		if result.pushErr != nil {
			ui.PrintWarning("Push failed: %v", result.pushErr)
			if keybindsEnabled {
				ui.PrintDim("  [r] retry rebuild    [q] quit")
			}
			continue
		}

		if result.skipped {
			ui.PrintInfo("No changes detected — skipping push")
			if keybindsEnabled {
				ui.PrintDim("  [r] rebuild    [q] quit")
			}
			continue
		}

		hrCachedManifest = result.manifest
		if result.newBundleID != "" {
			bundleID = result.newBundleID
		}

		elapsed := formatProgressDuration(result.elapsed)
		timingSummary := formatRebuildTimingSummary(result)
		if result.dataPreserved {
			ui.PrintSuccess("Rebuilt (%s) - %s", elapsed, timingSummary)
			ui.PrintDim("  App data preserved")
		} else {
			ui.PrintSuccess("Rebuilt + reinstalled (%s) - %s", elapsed, timingSummary)
			if devicePlatform == "ios" {
				ui.PrintDim("  Note: iOS reinstalls clear app data")
			}
		}

		if result.usedDelta {
			ui.PrintDim("  ↻ Background: build uploaded to cloud")
			if hrBgUploadCancel != nil {
				hrBgUploadCancel()
			}
			bgCtx, bgCancel := context.WithCancel(context.Background())
			hrBgUploadCancel = bgCancel
			go backgroundUploadBuild(bgCtx, client, cfg, platformKey, cwd, hrStatusPath)
		}

		ui.Println()
		if keybindsEnabled {
			ui.PrintDim("  [r] rebuild    [q] quit")
		}
	}
}

func isCIEnvironment() bool {
	return strings.TrimSpace(os.Getenv("CI")) != "" ||
		strings.TrimSpace(os.Getenv("GITHUB_ACTIONS")) != ""
}

// devSessionViewerURL returns the canonical session viewer route for the active
// device session, preferring `/sessions/<session_id>` over legacy viewer paths.
func devSessionViewerURL(session *mcppkg.DeviceSession, devMode bool) string {
	if session == nil {
		return ""
	}

	appURL := strings.TrimRight(config.GetAppURL(devMode), "/")
	sessionID := strings.TrimSpace(session.SessionID)
	if sessionID != "" && appURL != "" {
		return fmt.Sprintf("%s/sessions/%s", appURL, url.PathEscape(sessionID))
	}

	viewerURL := strings.TrimSpace(session.ViewerURL)
	if devMode && appURL != "" {
		viewerURL = strings.Replace(viewerURL, "https://app.revyl.ai", appURL, 1)
	}
	return viewerURL
}

func printDevReadyFooter(viewerURL, deepLinkURL string, manualDeepLinkRequired, isBareRN, noBuild bool, ctxName string, sessionIndex int) {
	ui.Println()
	ui.PrintSuccess("Dev loop ready")
	ui.PrintLink("Viewer", viewerURL)
	if isBareRN {
		ui.PrintInfo("Tunnel URL: %s", deepLinkURL)
	} else {
		ui.PrintInfo("Deep Link: %s", deepLinkURL)
		if manualDeepLinkRequired {
			ui.PrintWarning("Deep link was not opened automatically on this worker. Use the Deep Link above in the device browser/dev client.")
		}
	}
	ui.Println()
	if noBuild {
		ui.PrintDim("  [q] quit")
	} else {
		ui.PrintDim("  [r] rebuild native + reinstall    [q] quit")
	}
	ui.Println()
	printNewTerminalHints(ctxName, sessionIndex)
}

func printHotReloadReady(providerDisplay string, startResult *hotreload.StartResult) {
	ui.PrintSuccess("Hot reload ready: %s server and tunnel are running", providerDisplay)
	if startResult == nil {
		return
	}
	if startResult.RelayID != "" {
		ui.PrintDebug("  relay: %s", startResult.RelayID)
	}
	if startResult.TunnelURL != "" {
		ui.PrintDebug("  tunnel: %s", startResult.TunnelURL)
	}
}

// printNewTerminalHints prints session management and device interaction commands
// the user can run from a separate terminal. Includes the context name for attach
// and the session index (-s flag) so commands work with multiple sessions.
//
// Parameters:
//   - ctxName: the dev context name (e.g. "default") for attach/status commands
//   - sessionIndex: the device session index for -s flags on device commands
func printNewTerminalHints(ctxName string, sessionIndex int) {
	ui.PrintInfo("In a new terminal (context: %s):", ctxName)
	ui.Println()
	ui.PrintDim("  Manage the session:")
	ui.PrintDim("    revyl dev status                # session state + rebuild history")
	ui.PrintDim("    revyl dev rebuild               # trigger native rebuild + reinstall")
	ui.PrintDim("    revyl dev test run <name>       # run a test against this session")
	ui.PrintDim("    revyl dev attach %s       # reuse this device in another context", ctxName)
	ui.Println()
	ui.PrintDim("  Interact with the device:")
	ui.PrintDim("    revyl device tap --target \"Login button\" -s %d    # AI-grounded tap", sessionIndex)
	ui.PrintDim("    revyl device instruction \"log in and verify\" -s %d  # multi-step AI instruction", sessionIndex)
	ui.PrintDim("    revyl device screenshot -s %d                       # save a screenshot locally", sessionIndex)
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

// defaultDevSessionPollInterval is the interval between backend status checks
// in the dev loop. Overridden in tests for fast execution.
var defaultDevSessionPollInterval = 10 * time.Second

func isNoSessionAtIndexError(err error, index int) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), fmt.Sprintf("no session at index %d", index))
}

func resolveRebuildLoopPlatform(
	cfg *config.ProjectConfig,
	requestedPlatform string,
	explicitPlatformKey string,
	platformFlagExplicit bool,
) (string, string, error) {
	if cfg == nil || len(cfg.Build.Platforms) == 0 {
		return "", "", fmt.Errorf("no build platforms configured in .revyl/config.yaml")
	}

	normalizedPlatform, err := normalizeMobilePlatform(requestedPlatform, "ios")
	if err != nil {
		return "", "", err
	}

	keys := make([]string, 0, len(cfg.Build.Platforms))
	for key := range cfg.Build.Platforms {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	buildableKeys := buildablePlatformKeys(cfg)
	if len(buildableKeys) == 0 {
		return "", "", fmt.Errorf("no buildable platforms configured in .revyl/config.yaml")
	}

	if explicitPlatformKey != "" {
		platformCfg, ok := cfg.Build.Platforms[explicitPlatformKey]
		if !ok {
			return "", "", fmt.Errorf("build.platforms.%s not found", explicitPlatformKey)
		}
		if !isRunnableBuildPlatform(platformCfg) {
			return "", "", buildPlatformNeedsSetupError(explicitPlatformKey)
		}

		devicePlatform := platformFromKey(explicitPlatformKey)
		if devicePlatform == "" {
			if !platformFlagExplicit {
				return "", "", fmt.Errorf(
					"build.platforms.%s does not indicate ios or android; pass --platform ios|android alongside --platform-key or rename the key",
					explicitPlatformKey,
				)
			}
			devicePlatform = normalizedPlatform
		}
		if platformFlagExplicit && devicePlatform != normalizedPlatform {
			return "", "", fmt.Errorf(
				"build.platforms.%s is an %s build, but --platform %s was requested",
				explicitPlatformKey,
				devicePlatform,
				normalizedPlatform,
			)
		}
		return explicitPlatformKey, devicePlatform, nil
	}

	if platformKey := pickBestBuildPlatformKey(cfg, normalizedPlatform); platformKey != "" {
		return platformKey, normalizedPlatform, nil
	}

	if len(buildableKeys) == 1 {
		devicePlatform := platformFromKey(buildableKeys[0])
		if devicePlatform == "" {
			if !platformFlagExplicit {
				return "", "", fmt.Errorf(
					"build.platforms.%s does not indicate ios or android; pass --platform ios|android or rename the key",
					buildableKeys[0],
				)
			}
			devicePlatform = normalizedPlatform
		}
		return buildableKeys[0], devicePlatform, nil
	}

	return "", "", fmt.Errorf(
		"could not infer a build.platforms key for %s. Available keys: %s. Use --platform-key to choose one",
		normalizedPlatform,
		strings.Join(buildableKeys, ", "),
	)
}

func printRebuildLoopControls(keybindsEnabled bool, retry bool) {
	if keybindsEnabled {
		if retry {
			ui.PrintDim("  [r] retry rebuild    [q] quit")
			return
		}
		ui.PrintDim("  [r] rebuild + reinstall    [q] quit")
		return
	}

	ui.PrintDim("  Trigger rebuild: revyl dev rebuild")
	ui.PrintDim("  Stop session:    Ctrl+C")
}

func formatBuildVersionLabel(version *api.BuildVersion) string {
	if version == nil {
		return "unknown"
	}

	buildVersion := strings.TrimSpace(version.Version)
	buildID := strings.TrimSpace(version.ID)
	switch {
	case buildVersion != "" && buildID != "" && buildVersion != buildID:
		return fmt.Sprintf("%s (%s)", buildVersion, buildID)
	case buildVersion != "":
		return buildVersion
	case buildID != "":
		return buildID
	default:
		return "unknown"
	}
}

func appIdentifierLabel(devicePlatform string) string {
	if strings.EqualFold(devicePlatform, "ios") {
		return "Bundle ID"
	}
	if strings.EqualFold(devicePlatform, "android") {
		return "Package name"
	}
	return "App identifier"
}

func formatInstalledAppIdentifier(devicePlatform, identifier string) string {
	value := strings.TrimSpace(identifier)
	if value == "" {
		return ""
	}
	return fmt.Sprintf("%s %s", appIdentifierLabel(devicePlatform), value)
}

type workerSessionRequester interface {
	WorkerRequestForSession(ctx context.Context, sessionIndex int, path string, body interface{}) ([]byte, error)
}

func tryLaunchInstalledApp(
	ctx context.Context,
	requester workerSessionRequester,
	sessionIndex int,
	devicePlatform string,
	appIdentifier string,
	packagerHost string,
	packagerScheme string,
) {
	identifier := strings.TrimSpace(appIdentifier)
	if identifier == "" {
		ui.PrintWarning(
			"App install succeeded, but no %s was returned. Skipping explicit launch.",
			strings.ToLower(appIdentifierLabel(devicePlatform)),
		)
		return
	}

	payload := map[string]string{"bundle_id": identifier}
	if packagerHost != "" {
		payload["packager_host"] = packagerHost
	}
	if packagerScheme != "" {
		payload["packager_scheme"] = packagerScheme
	}

	launchResp, err := requester.WorkerRequestForSession(
		ctx,
		sessionIndex,
		"/launch",
		payload,
	)
	if err != nil {
		ui.PrintWarning(
			"App install succeeded, but launch failed (%s): %v",
			formatInstalledAppIdentifier(devicePlatform, identifier),
			err,
		)
		return
	}
	if err := ensureWorkerActionSucceeded(launchResp, "launch"); err != nil {
		ui.PrintWarning(
			"App install succeeded, but launch failed (%s): %v",
			formatInstalledAppIdentifier(devicePlatform, identifier),
			err,
		)
	}
}

// maskPresignedURL redacts the query string from presigned S3/GCS URLs to
// prevent leaking time-limited auth tokens in logs or CI output.
func maskPresignedURL(rawURL string) string {
	if idx := strings.Index(rawURL, "?"); idx > 0 {
		return rawURL[:idx] + "?<redacted>"
	}
	return rawURL
}

// runDevRebuildOnly implements a rebuild-based dev loop for native projects
// (Gradle, Xcode, Swift) that lack hot reload support. The loop provisions a
// cloud device, builds and installs the app, then waits for the user to press
// [r] to rebuild+reinstall or [q] to quit.
func runDevRebuildOnly(cmd *cobra.Command, cfg *config.ProjectConfig, configPath, cwd, apiKey, ctxName string) error {
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	requestedPlatform, err := normalizeMobilePlatform(devStartPlatform, "ios")
	if err != nil {
		return err
	}
	platformKey, devicePlatform, err := resolveRebuildLoopPlatform(
		cfg,
		requestedPlatform,
		strings.TrimSpace(devStartPlatformKey),
		cmd.Flags().Changed("platform"),
	)
	if err != nil {
		return err
	}
	platCfg := cfg.Build.Platforms[platformKey]

	ctxName, err = resolveDevCtxPlatformConflict(cwd, ctxName, devicePlatform, cmd.Flags().Changed("context"))
	if err != nil {
		return err
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

	ui.PrintBanner(version)
	ui.Println()
	ui.PrintInfo("Dev loop (%s / %s)", cfg.Build.System, platformKey)
	ui.PrintDim("Press [r] to rebuild + reinstall once the device is ready")
	ui.Println()

	// Ensure the platform has an app linked.
	if strings.TrimSpace(platCfg.AppID) == "" {
		_, err := selectOrCreateAppForPlatform(cmd, client, cfg, configPath, platformKey, devicePlatform)
		if err != nil {
			return err
		}
		cfg, err = config.LoadProjectConfig(configPath)
		if err != nil {
			return fmt.Errorf("failed to reload config: %w", err)
		}
		platCfg = cfg.Build.Platforms[platformKey]
		if strings.TrimSpace(platCfg.AppID) == "" {
			return fmt.Errorf("build.platforms.%s.app_id is required", platformKey)
		}
	}

	appID := strings.TrimSpace(platCfg.AppID)

	// Only build if explicitly requested or no existing build is available.
	needsBuild := devStartBuild
	if !needsBuild {
		existing, existErr := client.GetLatestBuildVersion(cmd.Context(), appID)
		if existErr != nil || existing == nil {
			needsBuild = true
		}
	}

	// When a build is needed but --build was NOT explicitly requested, try to
	// find and upload a local Xcode simulator .app from DerivedData first.
	// This bootstraps the initial install so native iOS developers can build
	// in Xcode and run `revyl dev` without a separate build command.
	usedLocalSimBuild := false
	var localSimBuildPath string
	if needsBuild && !devStartBuild && build.IsIOSPlatformKey(devicePlatform) {
		simBuild := build.FindSimulatorBuild(cwd)
		if simBuild != nil {
			ui.PrintInfo("Found local simulator build: %s", filepath.Base(simBuild.Path))
			if simBuild.IsStale(4 * time.Hour) {
				ui.PrintWarning("Local simulator build is %s old (built %s)",
					formatBuildAge(simBuild.Age()), simBuild.ModTime.Format("Mon Jan 2 15:04"))
			}
			ui.StartSpinner("Uploading local simulator build...")
			_, _, uploadErr := uploadExistingArtifact(cmd.Context(), client, simBuild.Path, appID, cwd)
			ui.StopSpinner()
			if uploadErr == nil {
				needsBuild = false
				usedLocalSimBuild = true
				localSimBuildPath = simBuild.Path
				ui.PrintSuccess("Uploaded local simulator build from DerivedData")
			} else {
				ui.PrintDebug("local simulator upload failed, falling back to configured build: %v", uploadErr)
			}
		}
	}

	if needsBuild {
		ui.PrintInfo("Building %s...", platformKey)
		if err := runSinglePlatformBuild(cmd, cfg, configPath, apiKey, platformKey); err != nil {
			return fmt.Errorf("build failed: %w", err)
		}
		reloadedCfg, reloadErr := config.LoadProjectConfig(configPath)
		if reloadErr != nil {
			return fmt.Errorf("failed to reload config after build: %w", reloadErr)
		}
		cfg = reloadedCfg
		platCfg = cfg.Build.Platforms[platformKey]
		appID = strings.TrimSpace(platCfg.AppID)
	} else if usedLocalSimBuild {
		ui.PrintDim("Using local simulator build for initial install")
	} else if !devStartBuild {
		ui.PrintDim("Using latest uploaded build (pass --build to force rebuild)")
	}

	latestVersion, err := client.GetLatestBuildVersion(cmd.Context(), appID)
	if err != nil {
		return fmt.Errorf("could not resolve uploaded build: %w", err)
	}
	if latestVersion == nil {
		return fmt.Errorf("no builds found for app %s", appID)
	}
	buildDetail, err := client.GetBuildVersionDownloadURL(cmd.Context(), latestVersion.ID)
	if err != nil {
		return fmt.Errorf("could not resolve build download URL: %w", err)
	}

	// Provision device.
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	var interrupted int32

	sigChan := make(chan os.Signal, 2)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)
	stopSigHandler := make(chan struct{})
	defer close(stopSigHandler)
	go func() {
		count := 0
		for {
			select {
			case <-stopSigHandler:
				return
			case <-sigChan:
				count++
				if count == 1 {
					atomic.StoreInt32(&interrupted, 1)
					ui.Println()
					ui.PrintInfo("Stopping dev session...")
					cancel()
				} else {
					ui.Println()
					ui.PrintWarning("Force exiting — device session may not be released immediately")
					forceCleanupDevContext(cwd, ctxName)
					os.Exit(130)
				}
			}
		}
	}()
	_ = interrupted

	deviceMgr, err := getDeviceSessionMgr(cmd)
	if err != nil {
		return err
	}

	bundleID := strings.TrimSpace(buildDetail.PackageName)
	var session *mcppkg.DeviceSession
	sessionOwned := true
	if savedCtx, _ := loadDevContext(cwd, ctxName); savedCtx != nil && savedCtx.SessionID != "" {
		reuse := tryReuseDevContextSession(ctx, deviceMgr, savedCtx, devicePlatform)
		if reuse != nil {
			session = reuse.Session
			sessionOwned = reuse.SessionOwned
			warnLaunchVarsIgnoredForReusedDevSession()
		}
	}

	if session == nil {
		ui.PrintInfo("Starting cloud device session...")
		_, session, err = startDevSessionWithProgress(
			ctx,
			deviceMgr,
			withDevStartLaunchVars(mcppkg.StartSessionOptions{
				Platform:       devicePlatform,
				AppID:          appID,
				BuildVersionID: latestVersion.ID,
				AppURL:         strings.TrimSpace(buildDetail.DownloadURL),
				AppPackage:     bundleID,
				IdleTimeout:    time.Duration(timeout) * time.Second,
			}),
			30*time.Second,
			nil,
		)
		if err != nil {
			return err
		}
	}

	if sessionOwned {
		defer func() {
			if stopErr := deviceMgr.StopSession(context.Background(), session.Index); stopErr != nil {
				if !isNoSessionAtIndexError(stopErr, session.Index) {
					ui.PrintWarning("Failed to stop device session: %v", stopErr)
				}
			}
		}()
	}

	// Install + launch. Use install_mode=fast to seed the worker's dev cache
	// so that subsequent delta pushes have a cached .app to patch.
	// Retry with backoff because the worker may still be attaching the device
	// after the session reaches "running" status.
	ui.PrintInfo("Installing app on device...")
	installBody := map[string]string{
		"app_url":      strings.TrimSpace(buildDetail.DownloadURL),
		"install_mode": "fast",
	}
	if bundleID != "" {
		installBody["bundle_id"] = bundleID
	}
	var installResp []byte
	const maxInstallRetries = 3
	for attempt := 0; attempt <= maxInstallRetries; attempt++ {
		installResp, err = deviceMgr.WorkerRequestForSession(ctx, session.Index, "/install", installBody)
		if err == nil {
			break
		}
		var workerErr *mcppkg.WorkerHTTPError
		isDeviceNotReady := errors.As(err, &workerErr) && workerErr.StatusCode == 503
		if !isDeviceNotReady || attempt == maxInstallRetries {
			return fmt.Errorf("install failed: %w\nhint: try re-running with --build to force a fresh build+upload", err)
		}
		backoff := time.Duration(1<<uint(attempt)) * time.Second // 1s, 2s, 4s
		ui.PrintDebug("device not ready, retrying install in %s (attempt %d/%d)", backoff, attempt+1, maxInstallRetries)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := ensureWorkerActionSucceeded(installResp, "install"); err != nil {
		return fmt.Errorf("install failed: %w\nhint: try re-running with --build to force a fresh build+upload", err)
	}
	if bundleID == "" {
		bundleID = extractInstallBundleID(installResp)
	}
	tryLaunchInstalledApp(ctx, deviceMgr, session.Index, devicePlatform, bundleID, "", "")

	deviceMgr.StopIdleTimer(session.Index)

	viewerURL := devSessionViewerURL(session, devMode)

	ui.Println()
	ui.PrintSuccess("Dev loop ready")
	ui.PrintLink("Viewer", viewerURL)
	ui.PrintInfo("Installed build: %s", formatBuildVersionLabel(latestVersion))
	if identifier := formatInstalledAppIdentifier(devicePlatform, bundleID); identifier != "" {
		ui.PrintInfo("Installed app: %s", identifier)
	}
	ui.Println()
	printNewTerminalHints(ctxName, session.Index)
	ui.Println()

	pidPath := devCtxPIDPath(cwd, ctxName)
	if err := os.MkdirAll(filepath.Dir(pidPath), 0755); err != nil {
		return fmt.Errorf("failed to create dev context directory: %w", err)
	}
	startNonce := time.Now().UnixNano()
	_ = writeDevCtxPIDFile(pidPath, os.Getpid(), startNonce)
	defer os.Remove(pidPath)

	devCtx := &DevContext{
		Name:          ctxName,
		Platform:      devicePlatform,
		PlatformKey:   platformKey,
		Provider:      cfg.Build.System,
		SessionID:     session.SessionID,
		SessionIndex:  session.Index,
		SessionOwned:  sessionOwned,
		ViewerURL:     viewerURL,
		PID:           os.Getpid(),
		StartedAtNano: startNonce,
		State:         devContextStateRunning,
		CreatedAt:     time.Now(),
		LastActivity:  time.Now(),
	}
	_ = saveDevContext(cwd, devCtx)
	_ = setCurrentDevContext(cwd, ctxName)
	defer func() {
		devCtx.State = devContextStateStopped
		devCtx.PID = 0
		if sessionOwned {
			devCtx.SessionID = ""
			devCtx.SessionIndex = 0
			devCtx.ViewerURL = ""
		}
		_ = saveDevContext(cwd, devCtx)
	}()

	sigusr1 := make(chan os.Signal, 1)
	if sigutil.RebuildSignal != nil {
		signal.Notify(sigusr1, sigutil.RebuildSignal)
	}
	defer signal.Stop(sigusr1)

	if openBrowser {
		_ = ui.OpenBrowser(viewerURL)
	}

	transport := devpush.NewTransport(client, deviceMgr)
	manifestPath := devCtxManifestPath(cwd, ctxName)
	statusPath := devCtxStatusPath(cwd, ctxName)
	cachedManifest, cachedManifestErr := build.LoadManifest(manifestPath)
	if cachedManifestErr != nil {
		ui.PrintDim("  Could not load cached manifest: %v", cachedManifestErr)
	}
	if cachedManifest == nil {
		if artPath, artErr := build.ResolveArtifactPath(cwd, platCfg.Output); artErr == nil {
			if m, mErr := build.BuildManifest(artPath); mErr == nil {
				cachedManifest = m
				_ = build.SaveManifest(m, manifestPath)
			}
		}
	}
	// When the initial build came from DerivedData and the configured output
	// path didn't produce a manifest, seed it from the DerivedData .app so
	// the first [r] rebuild can use delta pushes.
	if cachedManifest == nil && localSimBuildPath != "" {
		if m, mErr := build.BuildManifest(localSimBuildPath); mErr == nil {
			cachedManifest = m
			_ = build.SaveManifest(m, manifestPath)
			ui.PrintDebug("seeded delta manifest from DerivedData build")
		}
	}

	var bgUploadCancel context.CancelFunc
	defer func() {
		if bgUploadCancel != nil {
			bgUploadCancel()
		}
	}()

	// Interactive event loop: poll session liveness + [r]/SIGUSR1 rebuild + [q] quit.
	stdinKeys, restoreTerminal, keybindsEnabled := readStdinKeys(ctx)
	defer restoreTerminal()
	ticker := time.NewTicker(defaultDevSessionPollInterval)
	defer ticker.Stop()

	// Start file watcher for Flutter projects so Dart file saves auto-trigger rebuilds.
	isFlutter := build.ParseBuildSystem(cfg.Build.System) == build.SystemFlutter
	var fileWatchCh <-chan hotreload.FileChangeEvent
	if isFlutter {
		fw, fwErr := hotreload.NewFileWatcher(cwd, hotreload.FlutterWatchConfig())
		if fwErr == nil {
			if startErr := fw.Start(ctx); startErr == nil {
				defer fw.Stop()
				fileWatchCh = fw.Changes()
				ui.PrintDim("  Watching .dart files — saves auto-trigger rebuild")
			} else {
				ui.PrintDebug("file watcher start failed: %v", startErr)
			}
		} else {
			ui.PrintDebug("file watcher init failed: %v", fwErr)
		}
	}

	printRebuildLoopControls(keybindsEnabled, false)
	ui.Println()

	rebuildCount := 0
	var lastRebuildStart time.Time
	for {
		var doRebuild bool
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Second)
			alive, reason := deviceMgr.CheckSessionAlive(checkCtx, session)
			checkCancel()
			if !alive {
				ui.PrintWarning("Device session ended (%s).", reason)
				cancel()
				return nil
			}
		case <-sigusr1:
			doRebuild = true
		case event := <-fileWatchCh:
			label := formatChangedFiles(event.Files)
			ui.PrintInfo("File changed: %s — rebuilding...", label)
			doRebuild = true
		case key := <-stdinKeys:
			switch key {
			case 'r':
				doRebuild = true
			case 'q':
				ui.Println()
				ui.PrintInfo("Stopping dev session...")
				cancel()
				return nil
			}
		}

		if !doRebuild {
			continue
		}

		if !lastRebuildStart.IsZero() && time.Since(lastRebuildStart) < rebuildCooldown {
			if drainStdinKeys(stdinKeys) {
				ui.Println()
				ui.PrintInfo("Stopping dev session...")
				cancel()
				return nil
			}
			continue
		}
		lastRebuildStart = time.Now()

		rebuildCount++
		result := devBuildAndDeltaPush(ctx, cancel, cmd, cfg, configPath, apiKey, platformKey, devicePlatform,
			bundleID, session, deviceMgr, client, transport, cachedManifest, manifestPath, cwd, "", "")

		if drainStdinKeys(stdinKeys) {
			ui.Println()
			ui.PrintInfo("Stopping dev session...")
			cancel()
			return nil
		}

		writeDevStatus(statusPath, session, viewerURL, "", "", "", devicePlatform, rebuildCount, cachedManifest != nil, result)

		if result.buildErr != nil {
			ui.PrintWarning("Rebuild failed: %v", result.buildErr)
			printRebuildLoopControls(keybindsEnabled, true)
			continue
		}
		if result.pushErr != nil {
			ui.PrintWarning("Push failed: %v", result.pushErr)
			printRebuildLoopControls(keybindsEnabled, true)
			continue
		}

		if result.skipped {
			ui.PrintInfo("No changes detected — skipping push")
			printRebuildLoopControls(keybindsEnabled, false)
			continue
		}

		cachedManifest = result.manifest
		if result.newBundleID != "" {
			bundleID = result.newBundleID
		}

		elapsed := formatProgressDuration(result.elapsed)
		timingSummary := formatRebuildTimingSummary(result)
		if result.dataPreserved {
			ui.PrintSuccess("Rebuilt (%s) - %s", elapsed, timingSummary)
			ui.PrintDim("  App data preserved")
		} else {
			ui.PrintSuccess("Rebuilt + reinstalled (%s) - %s", elapsed, timingSummary)
			if devicePlatform == "ios" {
				ui.PrintDim("  Note: iOS reinstalls clear app data")
			}
		}

		if result.usedDelta {
			ui.PrintDim("  ↻ Background: build uploaded to cloud")
			if bgUploadCancel != nil {
				bgUploadCancel()
			}
			bgCtx, bgCancel := context.WithCancel(context.Background())
			bgUploadCancel = bgCancel
			go backgroundUploadBuild(bgCtx, client, cfg, platformKey, cwd, statusPath)
		}

		ui.Println()
		printRebuildLoopControls(keybindsEnabled, false)
	}
}

// readStdinKeys reads single keypresses from stdin in a goroutine and sends
// them to the returned channel. When stdin is a TTY, raw mode is enabled so
// keypresses are received immediately without waiting for Enter. The caller
// must defer the returned restore function to reset the terminal on exit.
//
// When stdin is not a TTY (piped, /dev/null, CI), the goroutine is never
// started and keybinds are silently disabled.
//
// Stops when ctx is cancelled or stdin reaches EOF.
func readStdinKeys(ctx context.Context) (keys <-chan byte, restore func(), enabled bool) {
	ch := make(chan byte, 1)
	noop := func() {}

	if !ui.IsInputTTY() {
		return ch, noop, false
	}

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return ch, noop, false
	}
	ui.SetRawMode(true)
	restoreFn := func() {
		ui.SetRawMode(false)
		_ = term.Restore(int(os.Stdin.Fd()), oldState)
	}

	go func() {
		buf := make([]byte, 1)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			n, readErr := os.Stdin.Read(buf)
			if readErr != nil || n == 0 {
				return
			}
			select {
			case ch <- buf[0]:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, restoreFn, true
}

// rebuildCooldown is the minimum interval between consecutive rebuild triggers
// to prevent accidental key-repeat from cascading into back-to-back rebuilds.
const rebuildCooldown = 1 * time.Second

// drainStdinKeys discards buffered keypresses from the channel so that
// keys accumulated during a long-running rebuild don't trigger follow-up
// rebuilds immediately. Returns true if a 'q' (quit) keypress was found
// among the drained keys, so the caller can honour it.
func drainStdinKeys(ch <-chan byte) bool {
	sawQuit := false
	for {
		select {
		case key := <-ch:
			if key == 'q' {
				sawQuit = true
			}
		default:
			return sawQuit
		}
	}
}

// monitorSessionDuringRebuild polls session liveness in the background while
// a blocking rebuild is in progress. If the session dies (idle timeout,
// cancellation, or worker failure), it cancels the parent context so the
// rebuild handler can detect the dead session promptly instead of waiting
// for the build to complete.
//
// The caller must cancel rebuildCtx when the rebuild finishes to stop polling.
func monitorSessionDuringRebuild(
	rebuildCtx context.Context,
	deviceMgr *mcppkg.DeviceSessionManager,
	session *mcppkg.DeviceSession,
	cancelParent context.CancelFunc,
) {
	t := time.NewTicker(defaultDevSessionPollInterval)
	defer t.Stop()
	for {
		select {
		case <-rebuildCtx.Done():
			return
		case <-t.C:
			checkCtx, checkCancel := context.WithTimeout(rebuildCtx, 5*time.Second)
			alive, reason := deviceMgr.CheckSessionAlive(checkCtx, session)
			checkCancel()
			if !alive {
				ui.Println()
				ui.PrintWarning("Device session ended during rebuild (%s)", reason)
				cancelParent()
				return
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Delta push rebuild + status infrastructure
// ---------------------------------------------------------------------------

const maxDeltaSizeBytes = 20 * 1024 * 1024 // 20 MB

// devRebuildResult collects the outcome of a single rebuild iteration.
type devRebuildResult struct {
	buildErr        error
	pushErr         error
	buildMode       string
	buildOutput     string
	buildErrors     []build.BuildError
	elapsed         time.Duration
	buildDuration   time.Duration
	pushDuration    time.Duration
	manifest        *build.AppManifest
	newBundleID     string
	usedDelta       bool
	dataPreserved   bool
	skipped         bool
	filesChanged    int
	remoteJobID     string
	remoteVersionID string
	remoteVersion   string
}

// formatProgressDuration formats a duration for user-facing rebuild status messages.
// Sub-second durations are rounded to 100ms; longer durations are rounded to 1s.
//
// Parameters:
//   - d: The duration to format.
//
// Returns:
//   - string: Human-readable duration string (e.g. "8s", "200ms").
func formatProgressDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Second {
		return d.Round(100 * time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}

// formatBuildAge returns a human-readable age string like "2h30m" or "3d".
//
// Parameters:
//   - d: Duration since the build was last modified
//
// formatChangedFiles returns a short human-readable summary of changed files
// for display in the rebuild trigger message.
//
// Parameters:
//   - files: List of relative file paths that changed
//
// Returns:
//   - string: Summary like "lib/main.dart" or "3 files"
func formatChangedFiles(files []string) string {
	if len(files) == 0 {
		return "unknown"
	}
	if len(files) == 1 {
		return files[0]
	}
	if len(files) <= 3 {
		return strings.Join(files, ", ")
	}
	return fmt.Sprintf("%s + %d more", files[0], len(files)-1)
}

// formatBuildAge formats a time.Duration into a human-readable age string.
//
// Returns:
//   - string: Human-readable age
func formatBuildAge(d time.Duration) string {
	if d < time.Hour {
		return d.Round(time.Minute).String()
	}
	hours := int(d.Hours())
	if hours < 24 {
		minutes := int(d.Minutes()) % 60
		if minutes > 0 {
			return fmt.Sprintf("%dh%dm", hours, minutes)
		}
		return fmt.Sprintf("%dh", hours)
	}
	days := hours / 24
	remainingHours := hours % 24
	if remainingHours > 0 {
		return fmt.Sprintf("%dd%dh", days, remainingHours)
	}
	return fmt.Sprintf("%dd", days)
}

// formatRebuildTimingSummary returns a concise phase breakdown for a completed rebuild
// (e.g. "build: 8s, device update: 4s").
//
// Parameters:
//   - result: The rebuild result containing phase durations.
//
// Returns:
//   - string: Comma-separated phase timing summary.
func formatRebuildTimingSummary(result devRebuildResult) string {
	parts := []string{fmt.Sprintf("build: %s", formatProgressDuration(result.buildDuration))}
	if result.pushDuration > 0 {
		parts = append(parts, fmt.Sprintf("device update: %s", formatProgressDuration(result.pushDuration)))
	}
	return strings.Join(parts, ", ")
}

// devBuildAndDeltaPush runs the build, diffs the manifest, and either pushes
// a delta or falls back to the full S3 upload path. Returns a structured result
// so the caller can update UI and status files.
func devBuildAndDeltaPush(
	ctx context.Context,
	cancelParent context.CancelFunc,
	cmd *cobra.Command,
	cfg *config.ProjectConfig,
	configPath, apiKey, platformKey, devicePlatform, bundleID string,
	session *mcppkg.DeviceSession,
	deviceMgr *mcppkg.DeviceSessionManager,
	client *api.Client,
	transport devpush.WorkerTransport,
	cachedManifest *build.AppManifest,
	manifestPath, cwd string,
	packagerHost string,
	packagerScheme string,
) devRebuildResult {
	result := devRebuildResult{}
	rebuildStart := time.Now()

	ui.Println()
	ui.PrintInfo("Rebuilding %s...", platformKey)

	rebuildCtx, rebuildCancel := context.WithCancel(ctx)
	go monitorSessionDuringRebuild(rebuildCtx, deviceMgr, session, cancelParent)

	platCfg, ok := cfg.Build.Platforms[platformKey]
	if !ok {
		rebuildCancel()
		result.buildErr = fmt.Errorf("platform %q not found in config", platformKey)
		result.elapsed = time.Since(rebuildStart)
		return result
	}

	buildCommand := platCfg.Command
	if platCfg.Scheme != "" {
		buildCommand = build.ApplySchemeToCommand(buildCommand, platCfg.Scheme)
	}

	runner := build.NewRunner(cwd)
	runner.FilterOutput = !ui.IsDebugMode()

	progress := RunBuildWithProgress(runner, buildCommand, platformKey, 10*time.Second)

	result.buildDuration = progress.Duration
	result.buildOutput = progress.Output
	buildErr := progress.Err
	buildLineCount := progress.LineCount

	if buildErr != nil {
		ui.PrintDim("  Build failed after %s", formatProgressDuration(result.buildDuration))
	} else if buildLineCount > 0 {
		ui.PrintDim("  Build completed in %s (%d updates)", formatProgressDuration(result.buildDuration), buildLineCount)
	} else {
		ui.PrintDim("  Build completed in %s", formatProgressDuration(result.buildDuration))
	}

	rebuildCancel()

	if buildErr != nil {
		result.buildErr = buildErr
		result.buildErrors = build.ParseXcodeBuildErrors(result.buildOutput)
		if len(result.buildErrors) == 0 {
			result.buildErrors = build.ParseGradleBuildErrors(result.buildOutput)
		}
		result.elapsed = time.Since(rebuildStart)
		return result
	}

	if ctx.Err() != nil {
		result.buildErr = ctx.Err()
		result.elapsed = time.Since(rebuildStart)
		return result
	}

	artifactPath, artErr := build.ResolveArtifactPath(cwd, platCfg.Output)
	if artErr != nil {
		result.buildErr = fmt.Errorf("artifact not found after build: %w", artErr)
		result.elapsed = time.Since(rebuildStart)
		return result
	}
	ui.PrintDebug("artifact: %s", artifactPath)

	newManifest, manErr := build.BuildManifest(artifactPath)
	if manErr != nil {
		result.buildErr = fmt.Errorf("failed to build manifest: %w", manErr)
		result.elapsed = time.Since(rebuildStart)
		return result
	}
	ui.PrintDebug("manifest: %d files, hash=%s", len(newManifest.Files), newManifest.Hash[:12])
	if cachedManifest != nil {
		ui.PrintDebug("cached:   %d files, hash=%s", len(cachedManifest.Files), cachedManifest.Hash[:12])
	} else {
		ui.PrintDebug("cached:   nil (first build)")
	}

	diff := build.DiffManifest(cachedManifest, newManifest)
	ui.PrintDebug("diff: %d changed, %d deleted", len(diff.Changed), len(diff.Deleted))
	for _, f := range diff.Changed {
		ui.PrintDebug("  changed: %s", f)
	}

	if len(diff.Changed) == 0 && len(diff.Deleted) == 0 && cachedManifest != nil {
		result.skipped = true
		result.manifest = newManifest
		result.elapsed = time.Since(rebuildStart)
		_ = build.SaveManifest(newManifest, manifestPath)
		return result
	}

	deltaSize := build.DeltaSize(artifactPath, diff.Changed)

	// Single-file artifacts (e.g. Android APKs) can't benefit from delta
	// pushes -- the "delta" would be the entire file. Skip to full upload.
	isSingleFileArtifact := len(newManifest.Files) <= 1

	if cachedManifest != nil && deltaSize < maxDeltaSizeBytes && !isSingleFileArtifact {
		changedCount := len(diff.Changed) + len(diff.Deleted)
		ui.PrintInfo("Pushing changes to device...")
		ui.PrintDim("  %d files changed (%.1f MB)", changedCount, float64(deltaSize)/(1024*1024))

		deltaZip, zipErr := build.CreateDeltaZip(artifactPath, diff.Changed)
		if zipErr != nil {
			result.pushErr = fmt.Errorf("failed to create delta zip: %w", zipErr)
			result.elapsed = time.Since(rebuildStart)
			return result
		}

		pushStart := time.Now()
		ui.StartSpinner("Uploading delta to device...")
		ref, pushErr := transport.PushArtifact(ctx, session, deltaZip)
		ui.StopSpinner()
		if pushErr != nil {
			result.pushErr = fmt.Errorf("failed to push delta: %w", pushErr)
			result.elapsed = time.Since(rebuildStart)
			return result
		}

		ui.StartSpinner("Installing delta on device...")
		installResult, installErr := transport.SendInstall(ctx, session, ref, devpush.InstallOpts{
			Mode:         "delta",
			BundleID:     bundleID,
			Platform:     devicePlatform,
			DeletedFiles: diff.Deleted,
		})
		ui.StopSpinner()
		result.pushDuration = time.Since(pushStart)

		if installErr != nil {
			// Delta failed (e.g. worker cache empty) — fall through to full
			// upload path instead of surfacing the error to the user.
			ui.PrintDim("  Delta push failed (%v), falling back to full install...", installErr)
		} else {
			result.usedDelta = true
			result.dataPreserved = installResult.DataPreserved
			result.newBundleID = installResult.BundleID
			result.filesChanged = len(diff.Changed) + len(diff.Deleted)
			result.manifest = newManifest
			result.elapsed = time.Since(rebuildStart)
			_ = build.SaveManifest(newManifest, manifestPath)
			return result
		}
	}

	// Fall back to full S3 upload path — upload the already-built artifact
	// instead of rebuilding from scratch.
	appID := strings.TrimSpace(platCfg.AppID)
	ui.PrintInfo("Uploading full artifact to cloud...")
	pushStart := time.Now()
	ui.StartSpinner("Uploading full artifact to cloud...")
	downloadURL, detectedBID, uploadErr := uploadExistingArtifact(ctx, client, artifactPath, appID, cwd)
	ui.StopSpinner()
	if uploadErr != nil {
		result.pushErr = fmt.Errorf("full artifact upload failed: %w", uploadErr)
		result.elapsed = time.Since(rebuildStart)
		return result
	}
	if detectedBID != "" && bundleID == "" {
		bundleID = detectedBID
	}

	reinstallBody := map[string]string{
		"app_url":      downloadURL,
		"install_mode": "fast",
	}
	if bundleID != "" {
		reinstallBody["bundle_id"] = bundleID
	}
	ui.StartSpinner("Installing full build on device...")
	resp, installErr := deviceMgr.WorkerRequestForSession(ctx, session.Index, "/install", reinstallBody)
	if installErr != nil {
		ui.StopSpinner()
		result.pushErr = fmt.Errorf("reinstall failed: %w", installErr)
		result.elapsed = time.Since(rebuildStart)
		return result
	}
	if err := ensureWorkerActionSucceeded(resp, "install"); err != nil {
		ui.StopSpinner()
		result.pushErr = fmt.Errorf("reinstall failed: %w", err)
		result.elapsed = time.Since(rebuildStart)
		return result
	}
	ui.StopSpinner()
	result.pushDuration = time.Since(pushStart)
	if newBID := extractInstallBundleID(resp); newBID != "" {
		result.newBundleID = newBID
	}
	launchID := bundleID
	if result.newBundleID != "" {
		launchID = result.newBundleID
	}
	ui.StartSpinner("Launching app...")
	tryLaunchInstalledApp(ctx, deviceMgr, session.Index, devicePlatform, launchID, packagerHost, packagerScheme)
	ui.StopSpinner()

	result.manifest = newManifest
	result.filesChanged = len(diff.Changed) + len(diff.Deleted)
	result.elapsed = time.Since(rebuildStart)
	_ = build.SaveManifest(newManifest, manifestPath)
	return result
}

// backgroundUploadBuild uploads the already-built artifact to S3 in a
// background goroutine so the build version is tracked without blocking the
// dev loop. It does NOT rebuild — only zips and uploads the existing artifact.
// Updates the dev status file on completion or failure.
//
// Parameters:
//   - ctx: cancellation context (cancelled when the next rebuild starts)
//   - client: API client for UploadBuild
//   - cfg: project config for platform output path and app ID
//   - platformKey: build platform key (e.g. "ios")
//   - cwd: working directory for artifact resolution
//   - statusPath: path to .dev-status.json for updating background_upload_status
func backgroundUploadBuild(ctx context.Context, client *api.Client, cfg *config.ProjectConfig, platformKey, cwd, statusPath string) {
	platCfg, ok := cfg.Build.Platforms[platformKey]
	if !ok {
		ui.PrintDim("  ✗ Background upload skipped: platform %s not found", platformKey)
		return
	}

	artifactPath, err := build.ResolveArtifactPath(cwd, platCfg.Output)
	if err != nil {
		ui.PrintDim("  ✗ Background upload skipped: artifact not found")
		return
	}

	if ctx.Err() != nil {
		return
	}

	uploadPath := artifactPath
	var tmpZip string
	if build.IsAppBundle(artifactPath) {
		zipped, zipErr := build.ZipAppBundle(artifactPath)
		if zipErr != nil {
			ui.PrintDim("  ✗ Background upload failed: %v", zipErr)
			return
		}
		tmpZip = zipped
		uploadPath = zipped
	}
	if tmpZip != "" {
		defer os.Remove(tmpZip)
	}

	if ctx.Err() != nil {
		return
	}

	appID := strings.TrimSpace(platCfg.AppID)
	if appID == "" {
		ui.PrintDim("  ✗ Background upload skipped: no app_id configured")
		return
	}

	versionStr := build.GenerateVersionStringForWorkDir(cwd)
	_, uploadErr := client.UploadBuild(ctx, &api.UploadBuildRequest{
		AppID:    appID,
		Version:  versionStr,
		FilePath: uploadPath,
	})
	if uploadErr != nil {
		if ctx.Err() != nil {
			return
		}
		ui.PrintDim("  ✗ Background upload failed: %v", uploadErr)
		updateBgUploadStatus(statusPath, "failed")
		return
	}
	ui.PrintDim("  ✓ Background: build uploaded to cloud")
	updateBgUploadStatus(statusPath, "completed")
}

// updateBgUploadStatus patches the background_upload_status field in the
// dev status file without rewriting other fields.
func updateBgUploadStatus(statusPath, status string) {
	data, err := os.ReadFile(statusPath)
	if err != nil {
		return
	}
	var ds devStatus
	if err := json.Unmarshal(data, &ds); err != nil || ds.LastRebuild == nil {
		return
	}
	ds.LastRebuild.BackgroundUpload = status
	out, err := json.MarshalIndent(ds, "", "  ")
	if err != nil {
		return
	}
	tmp := statusPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return
	}
	_ = os.Rename(tmp, statusPath)
}

// uploadExistingArtifact uploads an already-built artifact to S3 without
// rebuilding. Returns the download URL for the uploaded build, or an error.
//
// Parameters:
//   - ctx: cancellation context
//   - client: API client for UploadBuild
//   - artifactPath: resolved path to the built artifact (.app, .apk, or .tar.gz)
//   - appID: application ID for the upload
//   - cwd: working directory for version string generation
//
// Returns:
//   - downloadURL: presigned URL the worker can fetch from
//   - bundleID: package name from the uploaded build (may be empty)
//   - error: upload or resolution failure
func uploadExistingArtifact(ctx context.Context, client *api.Client, artifactPath, appID, cwd string) (downloadURL, bundleID string, err error) {
	uploadPath := artifactPath
	var tmpZip string
	if build.IsTarGz(artifactPath) {
		zipped, extractErr := build.ExtractAppFromTarGz(artifactPath)
		if extractErr != nil {
			return "", "", fmt.Errorf("failed to extract app from archive: %w", extractErr)
		}
		tmpZip = zipped
		uploadPath = zipped
	} else if build.IsAppBundle(artifactPath) {
		zipped, zipErr := build.ZipAppBundle(artifactPath)
		if zipErr != nil {
			return "", "", fmt.Errorf("failed to zip artifact: %w", zipErr)
		}
		tmpZip = zipped
		uploadPath = zipped
	}
	if tmpZip != "" {
		defer os.Remove(tmpZip)
	}

	versionStr := build.GenerateVersionStringForWorkDir(cwd)
	uploadResp, uploadErr := client.UploadBuild(ctx, &api.UploadBuildRequest{
		AppID:    appID,
		Version:  versionStr,
		FilePath: uploadPath,
	})
	if uploadErr != nil {
		return "", "", fmt.Errorf("upload failed: %w", uploadErr)
	}

	versionID := strings.TrimSpace(uploadResp.VersionID)
	if versionID == "" {
		return "", "", fmt.Errorf("upload succeeded but no build version ID was returned for app %s", appID)
	}
	bd, bdErr := client.GetBuildVersionDownloadURL(ctx, versionID)
	if bdErr != nil {
		return "", "", fmt.Errorf("could not get download URL: %w", bdErr)
	}
	return strings.TrimSpace(bd.DownloadURL), strings.TrimSpace(bd.PackageName), nil
}

// ---------------------------------------------------------------------------
// Status file
// ---------------------------------------------------------------------------

type devStatus struct {
	State          string          `json:"state"`
	PID            int             `json:"pid"`
	Platform       string          `json:"platform"`
	BuildMode      string          `json:"build_mode,omitempty"`
	SessionID      string          `json:"session_id,omitempty"`
	ViewerURL      string          `json:"viewer_url,omitempty"`
	TunnelURL      string          `json:"tunnel_url,omitempty"`
	DeepLinkURL    string          `json:"deep_link_url,omitempty"`
	Transport      string          `json:"transport,omitempty"`
	DeltaCacheWarm bool            `json:"delta_cache_warm"`
	RebuildCount   int             `json:"rebuild_count"`
	LastRebuild    *devRebuildInfo `json:"last_rebuild,omitempty"`
}

type devRebuildInfo struct {
	CompletedAt      string             `json:"completed_at"`
	Seq              int                `json:"seq"`
	Status           string             `json:"status"`
	DurationMs       int64              `json:"duration_ms"`
	BuildDurationMs  int64              `json:"build_duration_ms"`
	PushMode         string             `json:"push_mode"`
	PushDurationMs   int64              `json:"push_duration_ms"`
	FilesChanged     int                `json:"files_changed"`
	DataPreserved    bool               `json:"data_preserved"`
	BackgroundUpload string             `json:"background_upload_status,omitempty"`
	RemoteJobID      string             `json:"remote_job_id,omitempty"`
	RemoteVersionID  string             `json:"remote_build_version_id,omitempty"`
	RemoteVersion    string             `json:"remote_build_version,omitempty"`
	Error            string             `json:"error,omitempty"`
	BuildErrors      []build.BuildError `json:"build_errors"`
}

func writeDevStatus(
	statusPath string,
	session *mcppkg.DeviceSession,
	viewerURL string,
	tunnelURL string,
	deepLinkURL string,
	transport string,
	platform string,
	rebuildCount int,
	cacheWarm bool,
	result devRebuildResult,
) {
	status := "success"
	if result.buildErr != nil {
		status = "build_failed"
	} else if result.pushErr != nil {
		status = "push_failed"
	} else if result.skipped {
		status = "skipped"
	}

	pushMode := "full"
	if result.usedDelta {
		pushMode = "delta"
	}
	if result.skipped {
		pushMode = "none"
	}

	bgUpload := ""
	if result.usedDelta {
		bgUpload = "in_progress"
	}

	errs := result.buildErrors
	if errs == nil {
		errs = []build.BuildError{}
	}
	lastErr := ""
	if result.buildErr != nil {
		lastErr = result.buildErr.Error()
	} else if result.pushErr != nil {
		lastErr = result.pushErr.Error()
	}

	ds := devStatus{
		State:          "idle",
		PID:            os.Getpid(),
		Platform:       platform,
		BuildMode:      strings.TrimSpace(result.buildMode),
		TunnelURL:      strings.TrimSpace(tunnelURL),
		DeepLinkURL:    strings.TrimSpace(deepLinkURL),
		Transport:      strings.TrimSpace(transport),
		DeltaCacheWarm: cacheWarm || result.manifest != nil,
		RebuildCount:   rebuildCount,
		LastRebuild: &devRebuildInfo{
			CompletedAt:      time.Now().UTC().Format(time.RFC3339Nano),
			Seq:              rebuildCount,
			Status:           status,
			DurationMs:       result.elapsed.Milliseconds(),
			BuildDurationMs:  result.buildDuration.Milliseconds(),
			PushMode:         pushMode,
			PushDurationMs:   result.pushDuration.Milliseconds(),
			FilesChanged:     result.filesChanged,
			DataPreserved:    result.dataPreserved,
			BackgroundUpload: bgUpload,
			RemoteJobID:      strings.TrimSpace(result.remoteJobID),
			RemoteVersionID:  strings.TrimSpace(result.remoteVersionID),
			RemoteVersion:    strings.TrimSpace(result.remoteVersion),
			Error:            lastErr,
			BuildErrors:      errs,
		},
	}

	if session != nil {
		ds.SessionID = session.SessionID
	}
	ds.ViewerURL = strings.TrimSpace(viewerURL)

	data, err := json.MarshalIndent(ds, "", "  ")
	if err != nil {
		ui.PrintDim("  Failed to marshal dev status: %v", err)
		return
	}
	tmp := statusPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		ui.PrintDim("  Failed to write dev status: %v", err)
		return
	}
	_ = os.Rename(tmp, statusPath)
}

// ---------------------------------------------------------------------------
// revyl dev rebuild (with --wait / --json)
// ---------------------------------------------------------------------------

// runDevRebuild sends SIGUSR1 to a running `revyl dev` process to trigger a
// rebuild. With --wait it polls .dev-status.json until the rebuild completes.
// With --json it outputs the structured rebuild result.
//
// Returns:
//   - error: if no dev session is running or the signal cannot be delivered
func runDevRebuild(cmd *cobra.Command, args []string) error {
	cwd, err := resolveDevCwd()
	if err != nil {
		return err
	}

	ctxName, err := resolveDevContextName(cwd, getDevContextFlag(cmd))
	if err != nil {
		return err
	}

	pidPath := devCtxPIDPath(cwd, ctxName)
	pid, _ := readDevCtxPIDFile(pidPath)
	if pid == 0 {
		if rebuildJSON {
			fmt.Println(`{"status":"no_session","error":"no dev session running"}`)
			return nil
		}
		return fmt.Errorf("no dev session running (missing %s)", pidPath)
	}

	ctxMeta, _ := loadDevContext(cwd, ctxName)
	expectedNonce := int64(0)
	if ctxMeta != nil {
		expectedNonce = ctxMeta.StartedAtNano
	}
	alive, _ := isDevCtxProcessAlive(pid, expectedNonce, pidPath)
	if !alive {
		if rebuildJSON {
			fmt.Printf(`{"status":"no_session","error":"dev session (PID %d) is not running"}`, pid)
			fmt.Println()
			return nil
		}
		return fmt.Errorf("dev session (PID %d) is not running", pid)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		if rebuildJSON {
			fmt.Printf(`{"status":"no_session","error":"process %d not found"}`, pid)
			fmt.Println()
			return nil
		}
		return fmt.Errorf("process %d not found", pid)
	}
	if sigutil.RebuildSignal == nil {
		return fmt.Errorf("signal-based rebuild is not supported on this platform")
	}
	if err := proc.Signal(sigutil.RebuildSignal); err != nil {
		if rebuildJSON {
			fmt.Printf(`{"status":"no_session","error":"dev session (PID %d) is not running"}`, pid)
			fmt.Println()
			return nil
		}
		return fmt.Errorf("dev session (PID %d) is not running: %w", pid, err)
	}

	if !rebuildWait && !rebuildJSON {
		ui.PrintSuccess("Rebuild triggered (PID %d)", pid)
		return nil
	}

	statusPath := devCtxStatusPath(cwd, ctxName)
	priorSeq := readLastRebuildSeq(statusPath)

	timeout := time.Duration(rebuildTimeout) * time.Second
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		currentSeq := readLastRebuildSeq(statusPath)
		if currentSeq > priorSeq {
			statusData, readErr := os.ReadFile(statusPath)
			if readErr != nil {
				if rebuildJSON {
					fmt.Printf(`{"status":"error","error":"rebuild completed but failed to read status: %s"}`, readErr)
					fmt.Println()
					return nil
				}
				return fmt.Errorf("rebuild completed but failed to read status: %w", readErr)
			}

			if rebuildJSON {
				var ds devStatus
				if jsonErr := json.Unmarshal(statusData, &ds); jsonErr == nil && ds.LastRebuild != nil {
					out, _ := json.MarshalIndent(ds.LastRebuild, "", "  ")
					fmt.Println(string(out))
				} else {
					fmt.Println(string(statusData))
				}
				return nil
			}

			var ds devStatus
			if jsonErr := json.Unmarshal(statusData, &ds); jsonErr == nil && ds.LastRebuild != nil {
				rb := ds.LastRebuild
				if rb.Status == "success" || rb.Status == "skipped" {
					ui.PrintSuccess("Rebuild %s (%dms)", rb.Status, rb.DurationMs)
					ui.PrintDim("  push_mode=%s files_changed=%d data_preserved=%v", rb.PushMode, rb.FilesChanged, rb.DataPreserved)
				} else {
					ui.PrintError("Rebuild %s (%dms)", rb.Status, rb.DurationMs)
					for _, be := range rb.BuildErrors {
						ui.PrintDim("  %s:%d:%d: %s: %s", be.File, be.Line, be.Column, be.Severity, be.Message)
					}
				}
			}
			return nil
		}
	}

	if rebuildJSON {
		fmt.Printf(`{"status":"timeout","error":"rebuild did not complete within %ds"}`, rebuildTimeout)
		fmt.Println()
	} else {
		ui.PrintWarning("Rebuild did not complete within %ds", rebuildTimeout)
	}
	return fmt.Errorf("rebuild did not complete within %ds", rebuildTimeout)
}

func readLastRebuildSeq(statusPath string) int {
	data, err := os.ReadFile(statusPath)
	if err != nil {
		return 0
	}
	var ds devStatus
	if err := json.Unmarshal(data, &ds); err != nil || ds.LastRebuild == nil {
		return 0
	}
	return ds.LastRebuild.Seq
}

// ---------------------------------------------------------------------------
// revyl dev status
// ---------------------------------------------------------------------------

func runDevStatus(cmd *cobra.Command, args []string) error {
	cwd, err := resolveDevCwd()
	if err != nil {
		return err
	}

	ctxName, err := resolveDevContextName(cwd, getDevContextFlag(cmd))
	if err != nil {
		return err
	}

	pidPath := devCtxPIDPath(cwd, ctxName)
	statusPath := devCtxStatusPath(cwd, ctxName)

	pid, nonce := readDevCtxPIDFile(pidPath)
	if pid == 0 {
		out, _ := json.Marshal(map[string]interface{}{"running": false})
		fmt.Println(string(out))
		return nil
	}

	ctxMeta, _ := loadDevContext(cwd, ctxName)
	expectedNonce := nonce
	if ctxMeta != nil {
		expectedNonce = ctxMeta.StartedAtNano
	}
	running, _ := isDevCtxProcessAlive(pid, expectedNonce, pidPath)

	if !running {
		out, _ := json.Marshal(map[string]interface{}{"running": false})
		fmt.Println(string(out))
		return nil
	}

	statusData, statusErr := os.ReadFile(statusPath)
	if statusErr != nil {
		out, _ := json.MarshalIndent(buildDevStatusOutput(ctxName, pid, ctxMeta, nil), "", "  ")
		fmt.Println(string(out))
		return nil
	}

	var ds devStatus
	if err := json.Unmarshal(statusData, &ds); err != nil {
		out, _ := json.MarshalIndent(buildDevStatusOutput(ctxName, pid, ctxMeta, nil), "", "  ")
		fmt.Println(string(out))
		return nil
	}

	out, _ := json.MarshalIndent(buildDevStatusOutput(ctxName, pid, ctxMeta, &ds), "", "  ")
	fmt.Println(string(out))
	return nil
}

func buildDevStatusOutput(ctxName string, pid int, ctxMeta *DevContext, ds *devStatus) map[string]interface{} {
	sessionOwned := true
	if ctxMeta != nil {
		sessionOwned = ctxMeta.SessionOwned
	}

	platform := ""
	sessionID := ""
	viewerURL := ""
	tunnelURL := ""
	deepLinkURL := ""
	transport := ""
	buildMode := ""
	state := devContextStateRunning
	deltaCacheWarm := false
	rebuildCount := 0
	remoteJobID := ""
	remoteVersionID := ""
	lastRebuildError := ""
	var lastRebuild *devRebuildInfo

	if ctxMeta != nil {
		platform = strings.TrimSpace(ctxMeta.Platform)
		sessionID = strings.TrimSpace(ctxMeta.SessionID)
		viewerURL = strings.TrimSpace(ctxMeta.ViewerURL)
		tunnelURL = strings.TrimSpace(ctxMeta.TunnelURL)
		deepLinkURL = strings.TrimSpace(ctxMeta.DeepLinkURL)
		transport = strings.TrimSpace(ctxMeta.Transport)
		if strings.TrimSpace(ctxMeta.State) != "" {
			state = ctxMeta.State
		}
	}

	if ds != nil {
		if strings.TrimSpace(ds.Platform) != "" {
			platform = strings.TrimSpace(ds.Platform)
		}
		if strings.TrimSpace(ds.SessionID) != "" {
			sessionID = strings.TrimSpace(ds.SessionID)
		}
		if strings.TrimSpace(ds.ViewerURL) != "" {
			viewerURL = strings.TrimSpace(ds.ViewerURL)
		}
		if strings.TrimSpace(ds.TunnelURL) != "" {
			tunnelURL = strings.TrimSpace(ds.TunnelURL)
		}
		if strings.TrimSpace(ds.DeepLinkURL) != "" {
			deepLinkURL = strings.TrimSpace(ds.DeepLinkURL)
		}
		if strings.TrimSpace(ds.Transport) != "" {
			transport = strings.TrimSpace(ds.Transport)
		}
		if strings.TrimSpace(ds.BuildMode) != "" {
			buildMode = strings.TrimSpace(ds.BuildMode)
		}
		if strings.TrimSpace(ds.State) != "" {
			state = ds.State
		}
		deltaCacheWarm = ds.DeltaCacheWarm
		rebuildCount = ds.RebuildCount
		lastRebuild = ds.LastRebuild
		if lastRebuild != nil {
			remoteJobID = strings.TrimSpace(lastRebuild.RemoteJobID)
			remoteVersionID = strings.TrimSpace(lastRebuild.RemoteVersionID)
			lastRebuildError = strings.TrimSpace(lastRebuild.Error)
		}
	}

	return map[string]interface{}{
		"running":                  true,
		"pid":                      pid,
		"context":                  ctxName,
		"platform":                 platform,
		"session_id":               sessionID,
		"session_owned":            sessionOwned,
		"viewer_url":               viewerURL,
		"tunnel_url":               tunnelURL,
		"deep_link_url":            deepLinkURL,
		"transport":                transport,
		"build_mode":               buildMode,
		"state":                    state,
		"delta_cache_warm":         deltaCacheWarm,
		"rebuild_count":            rebuildCount,
		"remote_job_id":            remoteJobID,
		"remote_build_version_id":  remoteVersionID,
		"last_rebuild_error":       lastRebuildError,
		"last_rebuild_status":      safeLastRebuildField(lastRebuild, "status"),
		"last_rebuild_duration_ms": safeLastRebuildDuration(lastRebuild),
	}
}

func safeLastRebuildField(rb *devRebuildInfo, field string) string {
	if rb == nil {
		return ""
	}
	switch field {
	case "status":
		return rb.Status
	default:
		return ""
	}
}

func safeLastRebuildDuration(rb *devRebuildInfo) int64 {
	if rb == nil {
		return 0
	}
	return rb.DurationMs
}
