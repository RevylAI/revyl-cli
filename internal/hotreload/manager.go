package hotreload

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

// StartResult contains the result of starting hot reload mode.
type StartResult struct {
	// TunnelURL is the public hot reload relay URL.
	TunnelURL string

	// DeepLinkURL is the deep link URL for launching the dev client.
	DeepLinkURL string

	// Transport is the public transport backing TunnelURL.
	Transport string

	// RelayID is populated when Transport=relay.
	RelayID string

	// DevServerPort is the port the dev server is running on.
	DevServerPort int
}

// DevServerFactory is a function that creates a DevServer.
// This is used to avoid import cycles between hotreload and providers packages.
type DevServerFactory func(workDir, appScheme string, port int, useExpPrefix bool) DevServer

// BareRNDevServerFactory creates a bare React Native DevServer.
// Simpler signature than DevServerFactory since bare RN has no app scheme or exp prefix.
type BareRNDevServerFactory func(workDir string, port int) DevServer

// expoDevServerFactory is set by the providers package during init.
var expoDevServerFactory DevServerFactory

// bareRNDevServerFactory is set by the providers package during init.
var bareRNDevServerFactory BareRNDevServerFactory

var postStartupDiagnostics = RunPostStartupDiagnosticsForPlatform
var waitForExpoMetroTransport = WaitForExpoMetroTransport
var waitForExpoManifestFetchResult = waitForExpoManifestFetch
var waitForExpoBundlePrewarmFromManifest = WaitForExpoBundlePrewarmFromManifest
var waitForExpoManifestHeadReady = WaitForExpoManifestHeadReady

var (
	runtimeFailureDedupWindow         = 30 * time.Second
	localDevServerHealthInterval      = 5 * time.Second
	localDevServerFailuresBeforeFatal = 3
)

// readyTimeoutEnvVar overrides the relay-readiness timeout without a flag,
// e.g. for automation paths (revyl run/open/create, MCP dev loop).
const readyTimeoutEnvVar = "REVYL_READY_TIMEOUT"

// bundlePrewarmTimeoutEnvVar overrides the cold Expo bundle prewarm timeout
// for automation paths that cannot pass the revyl dev flag.
const bundlePrewarmTimeoutEnvVar = "REVYL_PREWARM_TIMEOUT"

// readyTimeoutFromEnv resolves REVYL_READY_TIMEOUT as a positive integer
// number of seconds. Invalid values fall back to the default and return a
// warning for the caller to surface.
func readyTimeoutFromEnv() (time.Duration, string) {
	raw := strings.TrimSpace(os.Getenv(readyTimeoutEnvVar))
	if raw == "" {
		return DefaultMetroReadyTimeout, ""
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs <= 0 {
		return DefaultMetroReadyTimeout, fmt.Sprintf(
			"Ignoring %s=%q: expected a positive number of seconds; using default %s",
			readyTimeoutEnvVar, raw, DefaultMetroReadyTimeout)
	}
	return time.Duration(secs) * time.Second, ""
}

// bundlePrewarmTimeoutFromEnv resolves REVYL_PREWARM_TIMEOUT as a positive
// integer number of seconds within the relay's supported response-start budget.
func bundlePrewarmTimeoutFromEnv() (time.Duration, string) {
	raw := strings.TrimSpace(os.Getenv(bundlePrewarmTimeoutEnvVar))
	if raw == "" {
		return DefaultExpoBundlePrewarmTimeout, ""
	}
	secs, err := strconv.Atoi(raw)
	maxSecs := int(MaxExpoBundlePrewarmTimeout / time.Second)
	if err != nil || secs <= 0 || secs > maxSecs {
		return DefaultExpoBundlePrewarmTimeout, fmt.Sprintf(
			"Ignoring %s=%q: expected a positive number of seconds no greater than %d; using default %s",
			bundlePrewarmTimeoutEnvVar, raw, maxSecs, DefaultExpoBundlePrewarmTimeout)
	}
	return time.Duration(secs) * time.Second, ""
}

// RegisterExpoDevServerFactory registers the Expo dev server factory.
// Called by the providers package during init.
func RegisterExpoDevServerFactory(factory DevServerFactory) {
	expoDevServerFactory = factory
}

// RegisterBareRNDevServerFactory registers the bare React Native dev server factory.
// Called by the providers package during init.
func RegisterBareRNDevServerFactory(factory BareRNDevServerFactory) {
	bareRNDevServerFactory = factory
}

// TunnelBackendFactory creates a TunnelBackend for tests or custom wiring.
type TunnelBackendFactory func() TunnelBackend

// Manager orchestrates the hot reload flow including dev server and tunnel lifecycle.
type Manager struct {
	// providerName is the name of the provider (expo, swift, android).
	providerName string

	// providerConfig is the configuration for the selected provider.
	providerConfig *config.ProviderConfig

	// workDir is the working directory for the project.
	workDir string

	// devServer is the active development server.
	devServer DevServer

	// tunnel is the active tunnel backend.
	tunnel TunnelBackend

	// apiClient is used by the relay transport control plane.
	apiClient *api.Client

	// transportPreference selects the preferred public transport.
	transportPreference string

	// tunnelFactory overrides the default TunnelBackend construction.
	tunnelFactory TunnelBackendFactory

	// onLog is called with log messages for the UI.
	onLog func(message string)

	// onDevServerOutput is called for streamed dev server process output.
	onDevServerOutput DevServerOutputCallback

	// debugMode enables provider-specific debug startup behavior.
	debugMode bool

	// targetPlatform is the device platform used for Expo manifest requests.
	targetPlatform string

	// forceHotReload lets callers launch even when Expo relay readiness cannot
	// prove the manifest is ready yet. It does not bypass earlier startup errors.
	forceHotReload bool

	// readyTimeout bounds relay-readiness waits (transport probe at startup and
	// after relay recovery). Resolved from REVYL_READY_TIMEOUT at construction;
	// SetReadyTimeout overrides.
	readyTimeout time.Duration

	// readyTimeoutWarning holds a deferred warning about an invalid
	// REVYL_READY_TIMEOUT value, logged once at Start.
	readyTimeoutWarning string

	// bundlePrewarmTimeout bounds the first cold Expo bundle transform through
	// the public relay. Resolved from REVYL_PREWARM_TIMEOUT at construction;
	// SetBundlePrewarmTimeout overrides.
	bundlePrewarmTimeout time.Duration

	// bundlePrewarmTimeoutWarning holds a deferred warning about an invalid
	// REVYL_PREWARM_TIMEOUT value, logged once at Start.
	bundlePrewarmTimeoutWarning string

	// externalTunnelURL, when set, bypasses the relay tunnel and dev server entirely.
	// The manager returns this URL directly as the tunnel URL. If externalDeepLinkURL
	// is unset, the Expo deep link is constructed from provider config.
	externalTunnelURL string

	// externalDeepLinkURL, when set, is used directly for Expo dev-client launch.
	// This lets callers pass the full deep link Expo printed without requiring app_scheme.
	externalDeepLinkURL string

	// failures receives deduplicated runtime failures from providers and tunnels.
	failures chan RuntimeFailure

	// failureLast tracks the last emission time for a dedupe key.
	failureLast map[string]time.Time

	// recoveryMu serializes recovery paths that stop/start the local dev server.
	recoveryMu sync.Mutex

	// localRecoveryAttempted caps automatic local dev-server restart to once.
	localRecoveryAttempted bool

	// relayRecoveryMu serializes relay re-acquire attempts.
	relayRecoveryMu sync.Mutex

	// relayRecoveryAttempted caps automatic relay re-acquire to once.
	relayRecoveryAttempted bool

	// mu protects concurrent access.
	mu sync.Mutex

	// ctx is canceled when this manager stops, including advisory diagnostics.
	ctx context.Context

	// cancel stops manager-owned background work.
	cancel context.CancelFunc

	// running indicates whether hot reload is active.
	running bool
}

// NewManager creates a new hot reload manager.
//
// Parameters:
//   - providerName: The provider name (expo, swift, android)
//   - providerConfig: Configuration for the selected provider
//   - workDir: Working directory for the project
//
// Returns:
//   - *Manager: A new manager instance
func NewManager(providerName string, providerConfig *config.ProviderConfig, workDir string) *Manager {
	readyTimeout, readyTimeoutWarning := readyTimeoutFromEnv()
	bundlePrewarmTimeout, bundlePrewarmTimeoutWarning := bundlePrewarmTimeoutFromEnv()
	return &Manager{
		providerName:                providerName,
		providerConfig:              providerConfig,
		workDir:                     workDir,
		transportPreference:         "relay",
		targetPlatform:              "ios",
		failures:                    make(chan RuntimeFailure, 16),
		failureLast:                 make(map[string]time.Time),
		readyTimeout:                readyTimeout,
		readyTimeoutWarning:         readyTimeoutWarning,
		bundlePrewarmTimeout:        bundlePrewarmTimeout,
		bundlePrewarmTimeoutWarning: bundlePrewarmTimeoutWarning,
	}
}

// SetLogCallback sets the callback for log messages.
//
// Parameters:
//   - callback: Function to call with log messages
func (m *Manager) SetLogCallback(callback func(message string)) {
	m.onLog = callback
}

// SetDevServerOutputCallback sets a callback for dev-server process output lines.
//
// Parameters:
//   - callback: Function to call with stdout/stderr output lines
func (m *Manager) SetDevServerOutputCallback(callback DevServerOutputCallback) {
	m.onDevServerOutput = callback
}

// SetDebugMode configures provider-specific debug startup behavior.
func (m *Manager) SetDebugMode(enabled bool) {
	m.debugMode = enabled
}

// SetTargetPlatform configures the device platform used for Expo manifest
// readiness checks. Blank or unknown values default to iOS.
func (m *Manager) SetTargetPlatform(platform string) {
	m.targetPlatform = normalizeExpoPlatform(platform)
}

// SetForceHotReload allows startup to continue if the Expo relay readiness
// proof fails after the dev server and tunnel have started.
func (m *Manager) SetForceHotReload(enabled bool) {
	m.forceHotReload = enabled
}

// SetReadyTimeout overrides how long startup and relay recovery wait for the
// relay transport to become reachable. Non-positive values are ignored,
// keeping the env-resolved or default timeout.
func (m *Manager) SetReadyTimeout(timeout time.Duration) {
	if timeout > 0 {
		m.readyTimeout = timeout
		m.readyTimeoutWarning = ""
	}
}

// SetBundlePrewarmTimeout overrides how long startup waits for the first cold
// Expo bundle transform through the relay. Values outside the supported relay
// budget are ignored, keeping the env-resolved or default timeout.
func (m *Manager) SetBundlePrewarmTimeout(timeout time.Duration) {
	if timeout > 0 && timeout <= MaxExpoBundlePrewarmTimeout {
		m.bundlePrewarmTimeout = timeout
		m.bundlePrewarmTimeoutWarning = ""
	}
}

// SetAPIClient provides the authenticated backend client used by the relay transport.
func (m *Manager) SetAPIClient(client *api.Client) {
	m.apiClient = client
}

// SetTransportPreference sets the preferred public transport.
func (m *Manager) SetTransportPreference(transport string) {
	trimmed := strings.ToLower(strings.TrimSpace(transport))
	if trimmed == "" {
		trimmed = "relay"
	}
	m.transportPreference = trimmed
}

// ConfigureFromHotReloadConfig applies transport settings from project config.
func (m *Manager) ConfigureFromHotReloadConfig(hr *config.HotReloadConfig, client *api.Client) {
	m.apiClient = client
	if hr == nil {
		return
	}
	m.transportPreference = hr.GetTransport()
}

// SetTunnelBackendFactory overrides the default relay tunnel backend.
// Must be called before Start.
//
// Params:
//   - factory: function that creates a TunnelBackend
func (m *Manager) SetTunnelBackendFactory(factory TunnelBackendFactory) {
	m.tunnelFactory = factory
}

// SetExternalTunnelURL configures the manager to use a user-provided tunnel URL
// instead of provisioning a relay. When set, Start() skips the dev server and
// relay entirely, returning the external URL and a deep link built from provider config.
//
// Parameters:
//   - tunnelURL: The public tunnel URL (e.g. from npx expo start --tunnel)
func (m *Manager) SetExternalTunnelURL(tunnelURL string) {
	m.externalTunnelURL = strings.TrimSpace(tunnelURL)
}

// SetExternalDeepLinkURL configures the manager to use a user-provided Expo
// dev-client deep link instead of deriving one from provider config.
func (m *Manager) SetExternalDeepLinkURL(deepLinkURL string) {
	m.externalDeepLinkURL = strings.TrimSpace(deepLinkURL)
}

// log sends a message to the log callback if set.
func (m *Manager) log(format string, args ...interface{}) {
	if m.onLog != nil {
		m.onLog(fmt.Sprintf(format, args...))
	}
}

func (m *Manager) debugLog(format string, args ...interface{}) {
	if m.debugMode {
		m.log(format, args...)
	}
}

// Start initializes the dev server and tunnel for hot reload mode.
//
// This method:
//  1. Creates the dev server instance (but doesn't start it yet)
//  2. Starts the relay first to get the URL
//  3. Sets the proxy URL on the dev server for bundle URL rewriting
//  4. Starts the dev server with the proxy URL configured
//  5. Returns the URLs needed for test execution
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - *StartResult: URLs and information for test execution
//   - error: Any error that occurred
func (m *Manager) Start(ctx context.Context) (result *StartResult, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return nil, fmt.Errorf("hot reload is already running")
	}
	m.resetRuntimeFailuresLocked()
	m.localRecoveryAttempted = false
	m.relayRecoveryAttempted = false

	m.ctx, m.cancel = context.WithCancel(ctx)
	defer func() {
		if err != nil {
			m.cleanupLocked(false)
		}
	}()

	if m.readyTimeoutWarning != "" {
		m.log("%s", m.readyTimeoutWarning)
		m.readyTimeoutWarning = ""
	}
	if m.bundlePrewarmTimeoutWarning != "" {
		m.log("%s", m.bundlePrewarmTimeoutWarning)
		m.bundlePrewarmTimeoutWarning = ""
	}

	if m.externalTunnelURL != "" {
		return m.startExternal(m.ctx)
	}

	// 1. Create dev server instance (but don't start yet - we need tunnel URL first)
	m.log("Preparing %s dev server...", m.providerName)
	devServer, err := m.createDevServer()
	if err != nil {
		return nil, err
	}
	m.attachDevServerOutputCallback(devServer)
	m.attachDevServerDebugMode(devServer)

	// 2. Start tunnel FIRST to get the URL
	// This must happen before starting the dev server so we can set EXPO_PACKAGER_PROXY_URL
	backend, tunnelURL, err := m.createTunnelBackend(m.ctx, devServer)
	if err != nil {
		return nil, fmt.Errorf("failed to create tunnel: %w", err)
	}
	m.tunnel = backend
	m.debugLog("Tunnel ready: %s", tunnelURL)

	// 3. Set proxy URL on dev server for bundle URL rewriting
	// This makes Metro return bundle URLs using the tunnel URL instead of localhost
	devServer.SetProxyURL(tunnelURL)
	m.debugLog("Configured proxy URL for bundle rewriting")

	// 4. Now start the dev server with proxy URL configured
	m.log("Starting %s dev server...", m.providerName)
	if err := devServer.Start(m.ctx); err != nil {
		return nil, fmt.Errorf("failed to start dev server: %w", err)
	}
	m.devServer = devServer
	m.log("%s dev server ready", devServer.Name())
	m.debugLog("%s dev server port: %d", devServer.Name(), devServer.GetPort())

	// 5. Start health monitor for automatic tunnel reconnection
	backend.StartHealthMonitor(m.ctx)

	if m.providerName == "expo" {
		m.log("Verifying Expo relay readiness...")
		m.debugLog("Waiting for Expo relay transport...")
		if _, err := waitForExpoMetroTransport(
			m.ctx,
			devServer.GetPort(),
			tunnelURL,
			m.readyTimeout,
			metroTunnelReadyPollInterval,
		); err != nil {
			return nil, fmt.Errorf(
				"Expo relay transport is not ready yet; launching the dev client would likely show a project load error (increase --ready-timeout or set REVYL_READY_TIMEOUT if Metro needs longer to start): %w",
				err,
			)
		}
		m.debugLog("Expo relay transport is ready")
		m.log("Expo relay transport verified")

		if m.forceHotReload {
			m.log("Skipped manifest and bundle proof because --force-hot-reload is set.")
			m.debugLog("Launching anyway; the dev client may show a project load error if the first manifest or bundle is still generating.")
		} else {
			m.log("Warming Expo manifest through relay...")
			m.debugLog("Waiting for Expo manifest to be served through the relay...")
			manifest, _, err := waitForExpoManifestFetchResult(
				m.ctx,
				devServer.GetPort(),
				tunnelURL,
				expoManifestReadyTimeout,
				metroTunnelReadyPollInterval,
				m.targetPlatform,
			)
			if err != nil {
				return nil, expoManifestReadinessError(m.targetPlatform, err)
			}
			m.debugLog("Expo manifest is being served through the relay")

			m.log("Warming Expo bundle through relay...")
			m.debugLog("Prewarming Expo bundle through the relay...")
			prewarmResult, err := waitForExpoBundlePrewarmFromManifest(
				m.ctx,
				devServer.GetPort(),
				tunnelURL,
				m.bundlePrewarmTimeout,
				manifest,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"Expo bundle could not be prewarmed through the relay yet; launching the dev client would likely show a project load error (increase --prewarm-timeout or set REVYL_PREWARM_TIMEOUT for a large cold bundle): %w",
					err,
				)
			}
			if prewarmResult != nil && len(prewarmResult.Checks) > 0 {
				m.debugLog("Expo bundle prewarm complete: %s", prewarmResult.Checks[0].Detail)
			} else {
				m.debugLog("Expo bundle prewarm complete")
			}

			m.log("Checking device manifest path through relay...")
			m.debugLog("Checking device-safe Expo manifest path through the relay...")
			headResult, err := waitForExpoManifestHeadReady(
				m.ctx,
				tunnelURL,
				expoManifestReadyTimeout,
				metroTunnelReadyPollInterval,
				m.targetPlatform,
				func(check DiagnosticCheck) {
					m.log("Metro is still warming; waiting before launching device.")
					m.debugLog("Device-safe Expo manifest path is still slow: %s", check.Detail)
				},
			)
			if err != nil {
				return nil, expoManifestReadinessError(m.targetPlatform, err)
			}
			if headResult != nil && len(headResult.Checks) > 0 {
				m.debugLog("Device-safe Expo manifest path verified: %s", headResult.Checks[0].Detail)
			} else {
				m.debugLog("Device-safe Expo manifest path verified")
			}
			m.log("Expo relay readiness verified")
		}
	}

	if m.providerName == "react-native" {
		m.log("Waiting for Metro tunnel to become externally reachable...")
		if _, err := WaitForMetroTunnel(
			m.ctx,
			devServer.GetPort(),
			tunnelURL,
			m.readyTimeout,
			metroTunnelReadyPollInterval,
		); err != nil {
			return nil, fmt.Errorf(
				"Metro tunnel is not externally reachable yet; launching the bare React Native app would likely show a white screen (increase --ready-timeout or set REVYL_READY_TIMEOUT if Metro needs longer to start): %w",
				err,
			)
		}
		m.log("Metro tunnel is reachable")
	}

	// 6. Construct deep link URL
	deepLinkURL := devServer.GetDeepLinkURL(tunnelURL)

	m.running = true
	m.watchRuntimeFailures(m.ctx, devServer, backend)

	// 7. Run advisory HMR diagnostics only in debug mode. These checks are
	// intentionally non-blocking and can race Metro/Expo startup, so default
	// dev-loop UX should rely on the device session as source of truth.
	if m.debugMode {
		go m.runDiagnostics(devServer.GetPort(), tunnelURL)
	}

	transport := m.transportPreference
	relayID := ""
	if infoProvider, ok := backend.(TunnelBackendInfoProvider); ok {
		info := infoProvider.Metadata()
		if strings.TrimSpace(info.Transport) != "" {
			transport = info.Transport
		}
		relayID = info.RelayID
	}

	return &StartResult{
		TunnelURL:     tunnelURL,
		DeepLinkURL:   deepLinkURL,
		Transport:     transport,
		RelayID:       relayID,
		DevServerPort: devServer.GetPort(),
	}, nil
}

func expoManifestReadinessError(platform string, err error) error {
	platform = normalizeExpoPlatform(platform)
	return fmt.Errorf(
		"Expo is running and the Revyl relay is reachable, but Revyl could not prove the first %s manifest through the relay.\n\n"+
			"Try diagnostic launch mode:\n"+
			"  revyl dev --platform %s --force-hot-reload\n\n"+
			"If the app loads, you can keep working. If the app shows a project load error, restart Expo/Metro or send:\n"+
			"  revyl device report --session-id <session-id> --json\n\n"+
			"Details: %w",
		platform,
		platform,
		err,
	)
}

func (m *Manager) createTunnelBackend(
	ctx context.Context,
	devServer DevServer,
) (TunnelBackend, string, error) {
	if m.tunnelFactory != nil {
		m.log("Creating tunnel...")
		backend := m.tunnelFactory()
		m.attachTunnelLogCallback(backend)
		url, err := backend.Start(ctx, devServer.GetPort())
		return backend, url, err
	}

	switch strings.ToLower(strings.TrimSpace(m.transportPreference)) {
	case "", "relay":
		return m.startRelayTunnel(ctx, devServer)
	default:
		return nil, "", fmt.Errorf("unsupported hotreload transport %q", m.transportPreference)
	}
}

func (m *Manager) startRelayTunnel(
	ctx context.Context,
	devServer DevServer,
) (TunnelBackend, string, error) {
	m.log("Connecting backend relay...")
	m.debugLog("Checking backend relay connectivity...")
	if err := CheckRelayConnectivity(ctx, m.apiClient); err != nil {
		return nil, "", err
	}
	m.debugLog("Backend relay connectivity OK")

	m.debugLog("Creating backend-owned relay...")
	backend := NewRelayTunnelBackend(m.apiClient, m.providerName)
	m.attachTunnelLogCallback(backend)
	tunnelURL, err := backend.Start(ctx, devServer.GetPort())
	if err != nil {
		return nil, "", err
	}
	return backend, tunnelURL, nil
}

func (m *Manager) attachTunnelLogCallback(backend TunnelBackend) {
	if logSetter, ok := backend.(interface{ SetLogCallback(func(string)) }); ok {
		logSetter.SetLogCallback(func(msg string) { m.debugLog("%s", msg) })
	}
}

// Stop cleans up all hot reload resources.
//
// This method stops the tunnel and dev server in order.
// It is safe to call multiple times.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.log("Cleaning up hot reload resources...")
	m.cleanupLocked(true)
	m.log("Cleanup complete")
}

func (m *Manager) cleanupLocked(logStopped bool) {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
		m.ctx = nil
	}

	// Stop tunnel first
	if m.tunnel != nil {
		m.tunnel.Stop()
		m.tunnel = nil
		if logStopped {
			m.log("Tunnel stopped")
		}
	}

	// Stop dev server
	if m.devServer != nil {
		m.devServer.Stop()
		m.devServer = nil
		if logStopped {
			m.log("Dev server stopped")
		}
	}

	m.running = false
	m.resetRuntimeFailuresLocked()
}

func (m *Manager) resetRuntimeFailuresLocked() {
	if m.failureLast == nil {
		m.failureLast = make(map[string]time.Time)
	} else {
		clear(m.failureLast)
	}
	if m.failures == nil {
		m.failures = make(chan RuntimeFailure, 16)
		return
	}
	for {
		select {
		case <-m.failures:
		default:
			return
		}
	}
}

// startExternal handles the short-circuit path when an external tunnel URL is
// configured. No dev server or relay is started; the provided URL is used directly.
func (m *Manager) startExternal(ctx context.Context) (*StartResult, error) {
	tunnelURL := m.externalTunnelURL
	backend := NewExternalTunnelBackend(tunnelURL)
	m.tunnel = backend
	m.running = true
	m.log("Using external tunnel: %s", tunnelURL)
	if m.providerName == "expo" && m.debugMode {
		m.log("External tunnel readiness checks are advisory; Revyl does not own the external dev server lifecycle.")
		go m.runExternalExpoDiagnostics(ctx, tunnelURL)
	}

	deepLinkURL := strings.TrimSpace(m.externalDeepLinkURL)
	if deepLinkURL == "" {
		deepLinkURL = m.buildExpoDeepLink(tunnelURL)
	}

	return &StartResult{
		TunnelURL:   tunnelURL,
		DeepLinkURL: deepLinkURL,
		Transport:   "external",
	}, nil
}

// buildExpoDeepLink constructs an Expo dev client deep link from provider config
// and a tunnel URL. Kept in-package to avoid importing providers (cycle).
func (m *Manager) buildExpoDeepLink(tunnelURL string) string {
	if m.providerConfig == nil || m.providerConfig.AppScheme == "" {
		return ""
	}
	encodedURL := url.QueryEscape(tunnelURL)
	if m.providerConfig.UseExpPrefix {
		return fmt.Sprintf("exp+%s://expo-development-client/?url=%s", m.providerConfig.AppScheme, encodedURL)
	}
	return fmt.Sprintf("%s://expo-development-client/?url=%s", m.providerConfig.AppScheme, encodedURL)
}

// IsRunning returns whether hot reload is currently active.
//
// Returns:
//   - bool: True if hot reload is running
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// GetTunnelURL returns the current tunnel URL if running.
//
// Returns:
//   - string: The tunnel URL, or empty string if not running
func (m *Manager) GetTunnelURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tunnel != nil {
		return m.tunnel.PublicURL()
	}
	return ""
}

// GetDevServerPort returns the dev server port if running.
//
// Returns:
//   - int: The port number, or 0 if not running
func (m *Manager) GetDevServerPort() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.devServer != nil {
		return m.devServer.GetPort()
	}
	return 0
}

// Failures returns deduplicated runtime failures detected after startup.
func (m *Manager) Failures() <-chan RuntimeFailure {
	return m.failures
}

func (m *Manager) watchRuntimeFailures(ctx context.Context, devServer DevServer, tunnel TunnelBackend) {
	if reporter, ok := devServer.(DevServerFailureReporter); ok {
		go m.forwardRuntimeFailures(ctx, reporter.Failures())
	}
	if reporter, ok := tunnel.(TunnelFailureReporter); ok {
		go m.forwardRuntimeFailures(ctx, reporter.Failures())
	}
	go m.monitorLocalDevServerHealth(ctx, devServer)
}

func (m *Manager) forwardRuntimeFailures(ctx context.Context, failures <-chan RuntimeFailure) {
	for {
		select {
		case <-ctx.Done():
			return
		case failure, ok := <-failures:
			if !ok {
				return
			}
			m.handleRuntimeFailure(ctx, failure)
		}
	}
}

func (m *Manager) monitorLocalDevServerHealth(ctx context.Context, devServer DevServer) {
	ticker := time.NewTicker(localDevServerHealthInterval)
	defer ticker.Stop()

	consecutiveConnectionRefused := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check := checkMetroHealth(devServer.GetPort(), "")
			if check.Passed {
				consecutiveConnectionRefused = 0
				continue
			}

			if localHealthCheckConnectionRefused(check) {
				consecutiveConnectionRefused++
				if consecutiveConnectionRefused < localDevServerFailuresBeforeFatal {
					continue
				}
				recovered := m.handleRuntimeFailure(ctx, RuntimeFailure{
					Kind:     RuntimeFailureLocalDevServerDown,
					Provider: devServer.Name(),
					Port:     devServer.GetPort(),
					Detail:   fmt.Sprintf("local dev server health check failed: %s", check.Detail),
					Fatal:    true,
				})
				if !recovered {
					return
				}
				consecutiveConnectionRefused = 0
				continue
			}

			consecutiveConnectionRefused = 0
			if failure, ok := runtimeFailureFromLocalHealthCheck(check, devServer.GetPort()); ok {
				failure.Provider = devServer.Name()
				m.emitRuntimeFailure(failure)
			}
		}
	}
}

func localHealthCheckConnectionRefused(check DiagnosticCheck) bool {
	return isConnectionRefusedMessage(check.Detail)
}

func runtimeFailureFromLocalHealthCheck(check DiagnosticCheck, localPort int) (RuntimeFailure, bool) {
	if check.Passed {
		return RuntimeFailure{}, false
	}
	lowerDetail := strings.ToLower(check.Detail)
	if strings.Contains(lowerDetail, "status 5") {
		return RuntimeFailure{
			Kind:   RuntimeFailureMetro500,
			Port:   localPort,
			Detail: fmt.Sprintf("%s: %s", check.Name, check.Detail),
			Fatal:  false,
		}, true
	}
	if localHealthCheckConnectionRefused(check) {
		return RuntimeFailure{}, false
	}
	return RuntimeFailure{
		Kind:   RuntimeFailureLocalDevServerDown,
		Port:   localPort,
		Detail: fmt.Sprintf("local dev server health check warning: %s", check.Detail),
		Fatal:  false,
	}, true
}

func (m *Manager) handleRuntimeFailure(ctx context.Context, failure RuntimeFailure) bool {
	if m.shouldRecoverLocalDevServer(failure) {
		if recovered, updated := m.tryRecoverLocalDevServer(ctx, failure); recovered {
			return true
		} else {
			failure = updated
		}
	}
	m.emitRuntimeFailure(failure)
	return false
}

func (m *Manager) shouldRecoverLocalDevServer(failure RuntimeFailure) bool {
	return failure.Fatal && failure.Kind == RuntimeFailureLocalDevServerDown
}

func (m *Manager) tryRecoverLocalDevServer(ctx context.Context, failure RuntimeFailure) (bool, RuntimeFailure) {
	m.recoveryMu.Lock()
	defer m.recoveryMu.Unlock()

	m.mu.Lock()
	devServer := m.devServer
	tunnel := m.tunnel
	running := m.running
	providerName := m.providerName
	alreadyAttempted := m.localRecoveryAttempted
	m.mu.Unlock()

	if !running || devServer == nil || tunnel == nil {
		return false, failureWithRestartError(failure, fmt.Errorf("hot reload manager is no longer running"))
	}

	if checkMetroHealth(devServer.GetPort(), "").Passed {
		m.log("Local %s dev server is responsive again; continuing.", devServer.Name())
		return true, failure
	}

	if alreadyAttempted {
		return false, failureWithRestartError(failure, fmt.Errorf("local dev server restart already attempted"))
	}

	tunnelURL := strings.TrimSpace(tunnel.PublicURL())
	if tunnelURL == "" {
		return false, failureWithRestartError(failure, fmt.Errorf("tunnel URL is unavailable"))
	}

	m.mu.Lock()
	m.localRecoveryAttempted = true
	m.mu.Unlock()

	m.log("Local %s dev server stopped; restarting once...", devServer.Name())
	if err := devServer.Stop(); err != nil {
		return false, failureWithRestartError(failure, fmt.Errorf("failed to stop stale local dev server: %w", err))
	}
	if err := devServer.Start(ctx); err != nil {
		return false, failureWithRestartError(failure, fmt.Errorf("failed to restart local dev server: %w", err))
	}
	if err := m.waitForRecoveredDevServer(ctx, providerName, devServer.GetPort(), tunnelURL); err != nil {
		return false, failureWithRestartError(failure, fmt.Errorf("local dev server restarted but relay path did not recover: %w", err))
	}
	m.log("Local %s dev server restarted", devServer.Name())
	return true, failure
}

// RecoverRelay replaces an expired relay session in place and restarts the
// owned local dev server so Metro advertises the replacement public host.
func (m *Manager) RecoverRelay(ctx context.Context) (*StartResult, error) {
	m.recoveryMu.Lock()
	defer m.recoveryMu.Unlock()

	m.relayRecoveryMu.Lock()
	defer m.relayRecoveryMu.Unlock()

	m.mu.Lock()
	if m.relayRecoveryAttempted {
		m.mu.Unlock()
		return nil, fmt.Errorf("relay recovery already attempted")
	}
	m.relayRecoveryAttempted = true
	devServer := m.devServer
	tunnel := m.tunnel
	running := m.running
	providerName := m.providerName
	managerCtx := m.ctx
	m.mu.Unlock()

	if !running || devServer == nil || tunnel == nil {
		return nil, fmt.Errorf("hot reload manager is no longer running")
	}
	reacquirer, ok := tunnel.(TunnelBackendReacquirer)
	if !ok {
		return nil, fmt.Errorf("active tunnel does not support relay recovery")
	}
	if managerCtx == nil {
		managerCtx = ctx
	}

	m.log("Relay lease expired; creating a new relay once...")
	recovered, err := reacquirer.Reacquire(ctx)
	if err != nil {
		return nil, err
	}
	tunnelURL := strings.TrimSpace(recovered.TunnelURL)
	if tunnelURL == "" {
		tunnelURL = strings.TrimSpace(tunnel.PublicURL())
	}
	if tunnelURL == "" {
		return nil, fmt.Errorf("replacement relay URL is unavailable")
	}

	devServer.SetProxyURL(tunnelURL)
	m.log("Restarting local %s dev server for the new relay...", devServer.Name())
	if err := devServer.Stop(); err != nil {
		return nil, fmt.Errorf("failed to stop local dev server during relay recovery: %w", err)
	}
	if err := devServer.Start(managerCtx); err != nil {
		return nil, fmt.Errorf("failed to restart local dev server during relay recovery: %w", err)
	}
	if err := m.waitForRecoveredDevServer(managerCtx, providerName, devServer.GetPort(), tunnelURL); err != nil {
		return nil, fmt.Errorf("replacement relay did not become reachable: %w", err)
	}

	transport := strings.TrimSpace(recovered.Transport)
	if transport == "" {
		transport = "relay"
	}
	relayID := strings.TrimSpace(recovered.RelayID)
	if infoProvider, ok := tunnel.(TunnelBackendInfoProvider); ok {
		info := infoProvider.Metadata()
		if strings.TrimSpace(info.Transport) != "" {
			transport = info.Transport
		}
		if strings.TrimSpace(info.RelayID) != "" {
			relayID = info.RelayID
		}
	}

	m.log("Relay recovered: %s", relayID)
	return &StartResult{
		TunnelURL:     tunnelURL,
		DeepLinkURL:   devServer.GetDeepLinkURL(tunnelURL),
		Transport:     transport,
		RelayID:       relayID,
		DevServerPort: devServer.GetPort(),
	}, nil
}

func (m *Manager) waitForRecoveredDevServer(ctx context.Context, providerName string, localPort int, tunnelURL string) error {
	switch providerName {
	case "expo":
		_, err := waitForExpoMetroTransport(
			ctx,
			localPort,
			tunnelURL,
			m.readyTimeout,
			metroTunnelReadyPollInterval,
		)
		return err
	case "react-native":
		_, err := WaitForMetroTunnel(
			ctx,
			localPort,
			tunnelURL,
			m.readyTimeout,
			metroTunnelReadyPollInterval,
		)
		return err
	default:
		check := checkMetroHealth(localPort, tunnelURL)
		if check.Passed {
			return nil
		}
		return fmt.Errorf("%s: %s", check.Name, check.Detail)
	}
}

func failureWithRestartError(failure RuntimeFailure, err error) RuntimeFailure {
	failure.Fatal = true
	failure.Err = err
	detail := strings.TrimSpace(failure.Detail)
	restartDetail := fmt.Sprintf("local dev server restart failed: %v", err)
	if detail == "" {
		failure.Detail = restartDetail
		return failure
	}
	if strings.Contains(detail, restartDetail) {
		return failure
	}
	failure.Detail = detail + "; " + restartDetail
	return failure
}

func (m *Manager) emitRuntimeFailure(failure RuntimeFailure) {
	if failure.Kind == "" {
		return
	}

	m.mu.Lock()
	if m.failureLast == nil {
		m.failureLast = make(map[string]time.Time)
	}
	if m.failures == nil {
		m.failures = make(chan RuntimeFailure, 16)
	}
	provider := m.providerName
	port := 0
	if m.devServer != nil {
		port = m.devServer.GetPort()
	}
	relayID := ""
	if infoProvider, ok := m.tunnel.(TunnelBackendInfoProvider); ok {
		relayID = infoProvider.Metadata().RelayID
	}
	failure = failure.WithDefaults(provider, port, relayID)
	now := time.Now()
	for existingKey, last := range m.failureLast {
		if now.Sub(last) >= runtimeFailureDedupWindow {
			delete(m.failureLast, existingKey)
		}
	}
	key := runtimeFailureDedupeKey(failure)
	if last, ok := m.failureLast[key]; ok && now.Sub(last) < runtimeFailureDedupWindow {
		m.mu.Unlock()
		return
	}
	m.failureLast[key] = now
	ch := m.failures
	m.mu.Unlock()

	select {
	case ch <- failure:
	default:
	}
}

func runtimeFailureDedupeKey(f RuntimeFailure) string {
	return fmt.Sprintf("%s|%t|%s|%d|%s", f.Kind, f.Fatal, f.Provider, f.Port, f.RelayID)
}

// runDiagnostics probes every layer of the HMR pipeline and logs per-check
// results. Intended to run in a goroutine immediately after Start() so results
// appear shortly after "Dev loop ready".
func (m *Manager) runDiagnostics(localPort int, tunnelURL string) {
	result := postStartupDiagnostics(localPort, tunnelURL, m.providerName, m.targetPlatform)
	for _, c := range result.Checks {
		if c.Passed {
			m.log("[hmr diagnostic] %s: %s", c.Name, c.Detail)
		} else {
			m.log("[hmr diagnostic] %s: advisory warning (%s)", c.Name, c.Detail)
			if failure, ok := runtimeFailureFromDiagnostic(c, localPort); ok {
				m.emitRuntimeFailure(failure)
			}
		}
	}
	if !result.AllPassed {
		m.log("[hmr diagnostic] Advisory checks did not all pass; verify the device session before treating this as a hard failure")
	}
}

func (m *Manager) runExternalExpoDiagnostics(ctx context.Context, tunnelURL string) {
	fetched, manifestCheck := checkExpoManifestContractForPlatformWithTimeout(
		ctx,
		0,
		tunnelURL,
		m.targetPlatform,
		expoManifestHTTPTimeout,
	)
	if ctx.Err() != nil {
		return
	}
	if manifestCheck.Passed {
		m.log("[hmr diagnostic] External Expo manifest: %s", manifestCheck.Detail)
	} else {
		m.log("[hmr diagnostic] External Expo manifest: advisory warning (%s)", manifestCheck.Detail)
		return
	}

	bundleCheck := checkExpoBundlePrewarmFromManifestWithTimeout(
		ctx,
		0,
		tunnelURL,
		fetched,
		m.bundlePrewarmTimeout,
	)
	if ctx.Err() != nil {
		return
	}
	if bundleCheck.Passed {
		m.log("[hmr diagnostic] External Expo bundle prewarm: %s", bundleCheck.Detail)
		return
	}
	m.log("[hmr diagnostic] External Expo bundle prewarm: advisory warning (%s)", bundleCheck.Detail)
}

func runtimeFailureFromDiagnostic(check DiagnosticCheck, localPort int) (RuntimeFailure, bool) {
	name := strings.ToLower(check.Name)
	detail := strings.ToLower(check.Detail)
	switch {
	case strings.Contains(name, "manifest"):
		return RuntimeFailure{
			Kind:   RuntimeFailureManifestUnhealthy,
			Port:   localPort,
			Detail: fmt.Sprintf("%s: %s", check.Name, check.Detail),
			Fatal:  false,
		}, true
	case strings.Contains(name, "bundle"):
		return RuntimeFailure{
			Kind:   RuntimeFailureBundleSlow,
			Port:   localPort,
			Detail: fmt.Sprintf("%s: %s", check.Name, check.Detail),
			Fatal:  false,
		}, true
	case strings.Contains(name, "metro") && strings.Contains(detail, "status 5"):
		return RuntimeFailure{
			Kind:   RuntimeFailureMetro500,
			Port:   localPort,
			Detail: fmt.Sprintf("%s: %s", check.Name, check.Detail),
			Fatal:  false,
		}, true
	default:
		return RuntimeFailure{}, false
	}
}

// attachDevServerOutputCallback wires process output callbacks when supported.
func (m *Manager) attachDevServerOutputCallback(devServer DevServer) {
	if m.onDevServerOutput == nil {
		return
	}
	emitter, ok := devServer.(DevServerOutputEmitter)
	if !ok {
		return
	}
	emitter.SetOutputCallback(m.onDevServerOutput)
}

// attachDevServerDebugMode wires debug-mode startup behavior when supported.
func (m *Manager) attachDevServerDebugMode(devServer DevServer) {
	debugConfigurable, ok := devServer.(DevServerDebugConfigurable)
	if !ok {
		return
	}
	debugConfigurable.SetDebugMode(m.debugMode)
}

// createDevServer creates the appropriate dev server based on the provider.
func (m *Manager) createDevServer() (DevServer, error) {
	switch m.providerName {
	case "expo":
		if m.providerConfig.AppScheme == "" {
			return nil, fmt.Errorf("app_scheme is required for Expo")
		}
		if expoDevServerFactory == nil {
			return nil, fmt.Errorf("expo dev server factory not registered - import github.com/revyl/cli/internal/hotreload/providers")
		}
		return expoDevServerFactory(
			m.workDir,
			m.providerConfig.AppScheme,
			m.providerConfig.GetPort("expo"),
			m.providerConfig.UseExpPrefix,
		), nil

	case "react-native":
		if bareRNDevServerFactory == nil {
			return nil, fmt.Errorf("bare RN dev server factory not registered - import github.com/revyl/cli/internal/hotreload/providers")
		}
		return bareRNDevServerFactory(
			m.workDir,
			m.providerConfig.GetPort("react-native"),
		), nil

	case "swift":
		return nil, fmt.Errorf("swift hot reload is not available — use [r] in revyl dev to rebuild + reinstall")

	case "android":
		return nil, fmt.Errorf("android hot reload is not available — use [r] in revyl dev to rebuild + reinstall")

	default:
		return nil, fmt.Errorf("unknown provider: %s", m.providerName)
	}
}

// GetProviderName returns the provider name.
//
// Returns:
//   - string: The provider name
func (m *Manager) GetProviderName() string {
	return m.providerName
}

// GetProviderConfig returns the provider configuration.
//
// Returns:
//   - *config.ProviderConfig: The provider configuration
func (m *Manager) GetProviderConfig() *config.ProviderConfig {
	return m.providerConfig
}
