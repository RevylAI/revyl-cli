package hotreload

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

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

	// externalTunnelURL, when set, bypasses the relay tunnel and dev server entirely.
	// The manager returns this URL directly as the tunnel URL. If externalDeepLinkURL
	// is unset, the Expo deep link is constructed from provider config.
	externalTunnelURL string

	// externalDeepLinkURL, when set, is used directly for Expo dev-client launch.
	// This lets callers pass the full deep link Expo printed without requiring app_scheme.
	externalDeepLinkURL string

	// mu protects concurrent access.
	mu sync.Mutex

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
	return &Manager{
		providerName:        providerName,
		providerConfig:      providerConfig,
		workDir:             workDir,
		transportPreference: "relay",
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
func (m *Manager) Start(ctx context.Context) (*StartResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return nil, fmt.Errorf("hot reload is already running")
	}

	if m.externalTunnelURL != "" {
		return m.startExternal()
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
	backend, tunnelURL, err := m.createTunnelBackend(ctx, devServer)
	if err != nil {
		return nil, fmt.Errorf("failed to create tunnel: %w", err)
	}
	m.tunnel = backend
	m.log("Tunnel ready: %s", tunnelURL)

	// 3. Set proxy URL on dev server for bundle URL rewriting
	// This makes Metro return bundle URLs using the tunnel URL instead of localhost
	devServer.SetProxyURL(tunnelURL)
	m.log("Configured proxy URL for bundle rewriting")

	// 4. Now start the dev server with proxy URL configured
	m.log("Starting %s dev server...", m.providerName)
	if err := devServer.Start(ctx); err != nil {
		// Clean up tunnel on dev server failure
		backend.Stop()
		m.tunnel = nil
		return nil, fmt.Errorf("failed to start dev server: %w", err)
	}
	m.devServer = devServer
	m.log("%s dev server ready on port %d", devServer.Name(), devServer.GetPort())

	// 5. Start health monitor for automatic tunnel reconnection
	backend.StartHealthMonitor(ctx)

	if m.providerName == "react-native" {
		m.log("Waiting for Metro tunnel to become externally reachable...")
		if _, err := WaitForMetroTunnel(
			ctx,
			devServer.GetPort(),
			tunnelURL,
			metroTunnelReadyTimeout,
			metroTunnelReadyPollInterval,
		); err != nil {
			if m.tunnel != nil {
				_ = m.tunnel.Stop()
				m.tunnel = nil
			}
			if m.devServer != nil {
				_ = m.devServer.Stop()
				m.devServer = nil
			}
			return nil, fmt.Errorf(
				"Metro tunnel is not externally reachable yet; launching the bare React Native app would likely show a white screen: %w",
				err,
			)
		}
		m.log("Metro tunnel is reachable")
	}

	// 6. Construct deep link URL
	deepLinkURL := devServer.GetDeepLinkURL(tunnelURL)

	m.running = true

	// 7. Run HMR diagnostics in the background (non-blocking).
	// Results are logged as warnings; failures don't break the dev loop.
	go m.runDiagnostics(devServer.GetPort(), tunnelURL)

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
	m.log("Checking backend relay connectivity...")
	if err := CheckRelayConnectivity(ctx, m.apiClient); err != nil {
		return nil, "", err
	}
	m.log("Backend relay connectivity OK")

	m.log("Creating backend-owned relay...")
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
		logSetter.SetLogCallback(func(msg string) { m.log("%s", msg) })
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

	// Stop tunnel first
	if m.tunnel != nil {
		m.tunnel.Stop()
		m.tunnel = nil
		m.log("Tunnel stopped")
	}

	// Stop dev server
	if m.devServer != nil {
		m.devServer.Stop()
		m.devServer = nil
		m.log("Dev server stopped")
	}

	m.running = false
	m.log("Cleanup complete")
}

// startExternal handles the short-circuit path when an external tunnel URL is
// configured. No dev server or relay is started; the provided URL is used directly.
func (m *Manager) startExternal() (*StartResult, error) {
	tunnelURL := m.externalTunnelURL
	backend := NewExternalTunnelBackend(tunnelURL)
	m.tunnel = backend
	m.running = true
	m.log("Using external tunnel: %s", tunnelURL)

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

// runDiagnostics probes every layer of the HMR pipeline and logs per-check
// results. Intended to run in a goroutine immediately after Start() so results
// appear shortly after "Dev loop ready".
func (m *Manager) runDiagnostics(localPort int, tunnelURL string) {
	result := RunPostStartupDiagnostics(localPort, tunnelURL, m.providerName)
	for _, c := range result.Checks {
		if c.Passed {
			m.log("[hmr] %s: %s", c.Name, c.Detail)
		} else {
			m.log("[hmr] %s: FAILED (%s)", c.Name, c.Detail)
		}
	}
	if !result.AllPassed {
		m.log("[hmr] Hot reload may not work -- one or more diagnostic checks failed")
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
