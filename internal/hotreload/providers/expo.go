// Package providers contains implementations of the DevServer interface
// for different development frameworks and platforms.
package providers

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/revyl/cli/internal/hotreload"
)

func init() {
	// Register the Expo dev server factory with the hotreload package
	hotreload.RegisterExpoDevServerFactory(func(workDir, appScheme string, port int, useExpPrefix bool) hotreload.DevServer {
		return NewExpoDevServer(workDir, appScheme, port, useExpPrefix)
	})
}

// ExpoDevServer implements the DevServer interface for Expo/React Native.
//
// It manages the Expo development server lifecycle and provides deep link URLs
// for connecting development clients to the local Metro bundler.
type ExpoDevServer struct {
	// Port is the port for the Expo dev server (default: 8081).
	Port int

	// AppScheme is the app's URL scheme from app.json (e.g., "myapp").
	AppScheme string

	// UseExpPrefix controls whether to use "exp+" prefix in deep links.
	// When true: exp+{scheme}://expo-development-client/?url=...
	// When false: {scheme}://expo-development-client/?url=...
	UseExpPrefix bool

	// WorkDir is the working directory for the Expo project.
	WorkDir string

	// proxyURL is the tunnel URL for bundle URL rewriting (EXPO_PACKAGER_PROXY_URL).
	proxyURL string

	// cmd is the running Expo process.
	cmd *exec.Cmd

	// cancel is the context cancel function for stopping the server.
	cancel context.CancelFunc

	// mu protects concurrent access to the server state.
	mu sync.Mutex

	// ready indicates whether the server is ready to accept connections.
	ready bool
}

// NewExpoDevServer creates a new Expo development server instance.
//
// Parameters:
//   - workDir: The working directory containing the Expo project
//   - appScheme: The app's URL scheme from app.json
//   - port: The port for the dev server (0 for default 8081)
//   - useExpPrefix: Whether to use "exp+" prefix in deep links
//
// Returns:
//   - *ExpoDevServer: A new Expo dev server instance
func NewExpoDevServer(workDir, appScheme string, port int, useExpPrefix bool) *ExpoDevServer {
	if port == 0 {
		port = 8081
	}
	return &ExpoDevServer{
		Port:         port,
		AppScheme:    appScheme,
		UseExpPrefix: useExpPrefix,
		WorkDir:      workDir,
	}
}

// Start launches the Expo development server and waits until it's ready.
//
// The server is started with:
//   - --dev-client: Enables development client mode
//   - --port: Uses the configured port
//   - --non-interactive: Disables interactive prompts
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - error: nil if server started successfully, otherwise the error
func (e *ExpoDevServer) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Check if npx is available
	if _, err := exec.LookPath("npx"); err != nil {
		return fmt.Errorf("npx not found. Install Node.js: https://nodejs.org/")
	}

	// Check if port is available
	if !e.isPortAvailable() {
		return fmt.Errorf("port %d is already in use. Stop the existing process or use --port to specify a different port\n\nTo kill the process using port %d, run:\n  lsof -ti :%d | xargs kill -9\n\nOr specify a different port:\n  revyl test open <name> --hotreload --port 8082", e.Port, e.Port, e.Port)
	}

	// Create cancellable context
	ctx, e.cancel = context.WithCancel(ctx)

	// Build command
	e.cmd = exec.CommandContext(ctx, "npx", "expo", "start",
		"--dev-client",
		"--port", fmt.Sprintf("%d", e.Port),
		"--non-interactive",
	)
	e.cmd.Dir = e.WorkDir

	// Set process group so we can kill all child processes
	setSysProcAttr(e.cmd)

	// Set environment to avoid interactive prompts
	e.cmd.Env = append(os.Environ(),
		"CI=1",
		"EXPO_NO_TELEMETRY=1",
	)

	// Set proxy URL for bundle URL rewriting if configured
	// This makes Metro return bundle URLs using the tunnel URL instead of localhost
	if e.proxyURL != "" {
		e.cmd.Env = append(e.cmd.Env, fmt.Sprintf("EXPO_PACKAGER_PROXY_URL=%s", e.proxyURL))
	}

	// Capture stdout for ready detection
	stdout, err := e.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to capture stdout: %w", err)
	}

	// Also capture stderr
	stderr, err := e.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to capture stderr: %w", err)
	}

	// Start the process
	if err := e.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start Expo dev server: %w", err)
	}

	// Wait for server to be ready
	readyChan := make(chan bool, 1)
	errChan := make(chan error, 1)

	// Monitor stdout for ready indicators
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			// Look for ready indicators
			if e.isReadyIndicator(line) {
				readyChan <- true
				return
			}
		}
	}()

	// Monitor stderr for errors
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			// Check for fatal errors
			if strings.Contains(strings.ToLower(line), "error") &&
				strings.Contains(strings.ToLower(line), "fatal") {
				errChan <- fmt.Errorf("Expo error: %s", line)
				return
			}
		}
	}()

	// Also poll the health endpoint
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e.checkHealth() {
					readyChan <- true
					return
				}
			}
		}
	}()

	// Wait for ready signal with timeout
	select {
	case <-readyChan:
		e.ready = true
		return nil
	case err := <-errChan:
		e.Stop()
		return err
	case <-time.After(60 * time.Second):
		e.Stop()
		return fmt.Errorf("timeout waiting for Expo dev server to start (60s)")
	case <-ctx.Done():
		e.Stop()
		return ctx.Err()
	}
}

// Stop terminates the Expo development server and all its child processes.
//
// Returns:
//   - error: nil if server stopped successfully
func (e *ExpoDevServer) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.ready = false

	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}

	if e.cmd != nil && e.cmd.Process != nil {
		pid := e.cmd.Process.Pid

		// Kill the entire process group
		// This ensures Metro bundler and all child processes are killed
		killProcessGroup(pid)

		// Wait briefly for graceful shutdown
		done := make(chan error, 1)
		go func() {
			done <- e.cmd.Wait()
		}()

		select {
		case <-done:
			// Process exited gracefully
		case <-time.After(2 * time.Second):
			// Force kill the process group
			forceKillProcessGroup(pid)
			// Also try to kill any remaining processes on the port
			killProcessOnPort(e.Port)
			<-time.After(500 * time.Millisecond)
		}

		e.cmd = nil
	}

	// Final cleanup: ensure nothing is left on the port
	killProcessOnPort(e.Port)

	return nil
}

// GetPort returns the port the Expo dev server is listening on.
//
// Returns:
//   - int: The port number
func (e *ExpoDevServer) GetPort() int {
	return e.Port
}

// GetDeepLinkURL constructs the deep link URL for the Expo development client.
//
// The deep link format depends on UseExpPrefix:
//   - With prefix: exp+{scheme}://expo-development-client/?url={tunnelURL}
//   - Without prefix: {scheme}://expo-development-client/?url={tunnelURL}
//
// The "exp+" prefix is used by newer Expo dev client builds (SDK 45+) with
// addGeneratedScheme: true. Older builds or those with addGeneratedScheme: false
// only register the base scheme.
//
// Parameters:
//   - tunnelURL: The public Cloudflare tunnel URL
//
// Returns:
//   - string: The deep link URL for launching the dev client
func (e *ExpoDevServer) GetDeepLinkURL(tunnelURL string) string {
	// URL encode the tunnel URL
	encodedURL := url.QueryEscape(tunnelURL)

	// Construct deep link with or without exp+ prefix based on config
	if e.UseExpPrefix {
		return fmt.Sprintf("exp+%s://expo-development-client/?url=%s", e.AppScheme, encodedURL)
	}
	return fmt.Sprintf("%s://expo-development-client/?url=%s", e.AppScheme, encodedURL)
}

// Name returns the human-readable name of this dev server provider.
//
// Returns:
//   - string: "Expo"
func (e *ExpoDevServer) Name() string {
	return "Expo"
}

// SetProxyURL sets the tunnel URL for bundle URL rewriting.
//
// This sets the EXPO_PACKAGER_PROXY_URL environment variable which causes Metro
// to rewrite bundle URLs to use the tunnel URL instead of localhost.
// This is required for remote devices to fetch JavaScript bundles through the tunnel.
//
// Must be called before Start() for the setting to take effect.
//
// Parameters:
//   - tunnelURL: The public tunnel URL (e.g., "https://xxx.trycloudflare.com")
func (e *ExpoDevServer) SetProxyURL(tunnelURL string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.proxyURL = tunnelURL
}

// isPortAvailable checks if the configured port is available.
func (e *ExpoDevServer) isPortAvailable() bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", e.Port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// isReadyIndicator checks if a log line indicates the server is ready.
func (e *ExpoDevServer) isReadyIndicator(line string) bool {
	readyIndicators := []string{
		"Metro waiting on",
		"Logs for your project",
		"Starting Metro",
		"Metro Bundler ready",
		"Development server running",
	}

	lowerLine := strings.ToLower(line)
	for _, indicator := range readyIndicators {
		if strings.Contains(lowerLine, strings.ToLower(indicator)) {
			return true
		}
	}
	return false
}

// checkHealth checks if the Expo dev server is responding to health checks.
func (e *ExpoDevServer) checkHealth() bool {
	client := &http.Client{Timeout: 2 * time.Second}

	// Try the status endpoint
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/status", e.Port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// IsReady returns whether the server is ready to accept connections.
//
// Returns:
//   - bool: True if server is ready
func (e *ExpoDevServer) IsReady() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ready
}
