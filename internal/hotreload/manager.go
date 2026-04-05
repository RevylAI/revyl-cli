package hotreload

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/revyl/cli/internal/config"
)

// StartResult contains the result of starting hot reload mode.
type StartResult struct {
	// TunnelURL is the public Cloudflare tunnel URL.
	TunnelURL string

	// DeepLinkURL is the deep link URL for launching the dev client.
	DeepLinkURL string

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

	// tunnel is the active Cloudflare tunnel.
	tunnel *TunnelManager

	// cloudflared manages the cloudflared binary.
	cloudflared *CloudflaredManager

	// onLog is called with log messages for the UI.
	onLog func(message string)

	// onDevServerOutput is called for streamed dev server process output.
	onDevServerOutput DevServerOutputCallback

	// debugMode enables provider-specific debug startup behavior.
	debugMode bool

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
		providerName:   providerName,
		providerConfig: providerConfig,
		workDir:        workDir,
		cloudflared:    NewCloudflaredManager(""),
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

// log sends a message to the log callback if set.
func (m *Manager) log(format string, args ...interface{}) {
	if m.onLog != nil {
		m.onLog(fmt.Sprintf(format, args...))
	}
}

// Start initializes the dev server and tunnel for hot reload mode.
//
// This method:
//  1. Checks network connectivity
//  2. Ensures cloudflared is available
//  3. Creates the dev server instance (but doesn't start it yet)
//  4. Starts the Cloudflare tunnel first to get the URL
//  5. Sets the proxy URL on the dev server for bundle URL rewriting
//  6. Starts the dev server with the proxy URL configured
//  7. Returns the URLs needed for test execution
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

	// 1. Check network connectivity
	m.log("Checking network connectivity...")
	connResult, err := CheckConnectivity(ctx)
	if err != nil {
		return nil, fmt.Errorf("connectivity check failed: %w", err)
	}

	if suggestion := DiagnoseAndSuggest(connResult); suggestion != "" {
		return nil, fmt.Errorf("network connectivity check failed:\n%s", suggestion)
	}
	m.log("Network connectivity OK")

	// 2. Ensure cloudflared is available
	m.log("Checking cloudflared...")
	cloudflaredPath, err := m.cloudflared.EnsureCloudflared()
	if err != nil {
		if isDownloadNeeded(err) {
			m.log("Downloading cloudflared (first time setup)...")
			cloudflaredPath, err = m.cloudflared.EnsureCloudflared()
			if err != nil {
				return nil, fmt.Errorf("failed to download cloudflared: %w", err)
			}
		} else {
			return nil, fmt.Errorf("cloudflared setup failed: %w", err)
		}
	}
	m.log("cloudflared ready: %s", cloudflaredPath)

	// 3. Create dev server instance (but don't start yet - we need tunnel URL first)
	m.log("Preparing %s dev server...", m.providerName)
	devServer, err := m.createDevServer()
	if err != nil {
		return nil, err
	}
	m.attachDevServerOutputCallback(devServer)
	m.attachDevServerDebugMode(devServer)

	// 4. Start Cloudflare tunnel FIRST to get the URL
	// This must happen before starting the dev server so we can set EXPO_PACKAGER_PROXY_URL
	m.log("Creating Cloudflare tunnel...")
	tunnel := NewTunnelManager(cloudflaredPath, nil)
	tunnel.SetLogCallback(func(msg string) { m.log("%s", msg) })
	tunnelInfo, err := tunnel.StartTunnelWithRetry(ctx, devServer.GetPort())
	if err != nil {
		return nil, fmt.Errorf("failed to create tunnel: %w", err)
	}
	m.tunnel = tunnel
	m.log("Tunnel ready: %s", tunnelInfo.PublicURL)

	// 5. Set proxy URL on dev server for bundle URL rewriting
	// This makes Metro return bundle URLs using the tunnel URL instead of localhost
	devServer.SetProxyURL(tunnelInfo.PublicURL)
	m.log("Configured proxy URL for bundle rewriting")

	// 6. Now start the dev server with proxy URL configured
	m.log("Starting %s dev server...", m.providerName)
	if err := devServer.Start(ctx); err != nil {
		// Clean up tunnel on dev server failure
		tunnel.Stop()
		m.tunnel = nil
		return nil, fmt.Errorf("failed to start dev server: %w", err)
	}
	m.devServer = devServer
	m.log("%s dev server ready on port %d", devServer.Name(), devServer.GetPort())

	// 7. Start health monitor for automatic tunnel reconnection
	tunnel.StartHealthMonitor(ctx)

	// 8. Construct deep link URL
	deepLinkURL := devServer.GetDeepLinkURL(tunnelInfo.PublicURL)

	m.running = true

	// 9. Run HMR diagnostics in the background (non-blocking).
	// Results are logged as warnings; failures don't break the dev loop.
	go m.runDiagnostics(devServer.GetPort(), tunnelInfo.PublicURL)

	return &StartResult{
		TunnelURL:     tunnelInfo.PublicURL,
		DeepLinkURL:   deepLinkURL,
		DevServerPort: devServer.GetPort(),
	}, nil
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
		return m.tunnel.GetPublicURL()
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
	result := RunPostStartupDiagnostics(localPort, tunnelURL)
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

// isDownloadNeeded checks if the error indicates cloudflared needs to be downloaded.
// Returns true only for file-not-found errors; other errors (e.g., permission denied,
// network errors) should not trigger a re-download.
func isDownloadNeeded(err error) bool {
	if err == nil {
		return false
	}
	return os.IsNotExist(err)
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
