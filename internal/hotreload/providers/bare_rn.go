// Package providers contains implementations of the DevServer interface
// for different development frameworks and platforms.
package providers

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/revyl/cli/internal/hotreload"
)

func init() {
	hotreload.RegisterBareRNDevServerFactory(func(workDir string, port int) hotreload.DevServer {
		return NewBareRNDevServer(workDir, port)
	})
}

// BareRNDevServer implements the DevServer interface for bare React Native
// projects (without Expo). It manages the Metro bundler lifecycle directly
// via `npx react-native start`.
//
// Unlike ExpoDevServer, this provider does not use EXPO_PACKAGER_PROXY_URL.
// It relies solely on REACT_NATIVE_PACKAGER_HOSTNAME to ensure Metro embeds
// the tunnel hostname in all generated URLs (bundle + HMR WebSocket).
type BareRNDevServer struct {
	// Port is the port for the Metro dev server (default: 8081).
	Port int

	// WorkDir is the working directory for the React Native project.
	WorkDir string

	// proxyURL is the tunnel URL; its hostname is extracted and set as
	// REACT_NATIVE_PACKAGER_HOSTNAME for Metro URL rewriting.
	proxyURL string

	cmd            *exec.Cmd
	cancel         context.CancelFunc
	mu             sync.Mutex
	ready          bool
	outputCallback hotreload.DevServerOutputCallback
	debugMode      bool
}

// NewBareRNDevServer creates a new bare React Native development server instance.
//
// Parameters:
//   - workDir: The working directory containing the React Native project
//   - port: The port for the dev server (0 for default 8081)
//
// Returns:
//   - *BareRNDevServer: A new bare RN dev server instance
func NewBareRNDevServer(workDir string, port int) *BareRNDevServer {
	if port == 0 {
		port = 8081
	}
	return &BareRNDevServer{
		Port:    port,
		WorkDir: workDir,
	}
}

// Start launches the Metro bundler and waits until it's ready.
//
// The server is started with:
//   - npx react-native start --port {port}
//   - REACT_NATIVE_PACKAGER_HOSTNAME set to the tunnel hostname (if proxy configured)
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - error: nil if server started successfully, otherwise the error
func (b *BareRNDevServer) Start(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	outputCallback := b.outputCallback

	if _, err := exec.LookPath("npx"); err != nil {
		return fmt.Errorf("npx not found. Install Node.js: https://nodejs.org/")
	}

	if !b.isPortAvailable() {
		return fmt.Errorf("port %d is already in use. Stop the existing process or use --port to specify a different port\n\nTo kill the process using port %d, run:\n  lsof -ti :%d | xargs kill -9\n\nOr specify a different port:\n  revyl test open <name> --hotreload --port 8082", b.Port, b.Port, b.Port)
	}

	ctx, b.cancel = context.WithCancel(ctx)

	b.cmd = exec.CommandContext(ctx, "npx", b.metroStartArgs()...)
	b.cmd.Dir = b.WorkDir

	setSysProcAttr(b.cmd)

	b.cmd.Env = b.metroEnvironment()

	// Set REACT_NATIVE_PACKAGER_HOSTNAME so Metro advertises the tunnel
	// hostname in bundle and HMR WebSocket URLs. Unlike Expo, bare RN does
	// not support EXPO_PACKAGER_PROXY_URL -- the hostname env var alone is
	// sufficient because Metro embeds it in all generated URLs.
	if b.proxyURL != "" {
		_, hostname := normalizeProxyURL(b.proxyURL)
		if hostname != "" {
			b.cmd.Env = append(b.cmd.Env, fmt.Sprintf("REACT_NATIVE_PACKAGER_HOSTNAME=%s", hostname))
		}
	}

	stdout, err := b.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to capture stdout: %w", err)
	}

	stderr, err := b.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to capture stderr: %w", err)
	}

	if err := b.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start Metro bundler: %w", err)
	}

	readyChan := make(chan bool, 1)
	errChan := make(chan error, 1)
	signalReady := newReadyNotifier(readyChan)

	go func() {
		b.streamStdout(stdout, outputCallback, signalReady)
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			b.emitProcessOutput(outputCallback, hotreload.DevServerOutputStderr, line)
			if isMetroFatalError(line) {
				errChan <- fmt.Errorf("Metro error: %s", line)
				return
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if b.checkHealth() {
					signalReady()
					return
				}
			}
		}
	}()

	select {
	case <-readyChan:
		b.ready = true
		return nil
	case err := <-errChan:
		_ = b.stopLocked()
		return err
	case <-time.After(90 * time.Second):
		_ = b.stopLocked()
		return fmt.Errorf("timeout waiting for Metro bundler to start (90s)")
	case <-ctx.Done():
		_ = b.stopLocked()
		return ctx.Err()
	}
}

// Stop terminates the Metro bundler and all its child processes.
//
// Returns:
//   - error: nil if server stopped successfully
func (b *BareRNDevServer) Stop() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stopLocked()
}

func (b *BareRNDevServer) stopLocked() error {
	b.ready = false

	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}

	if b.cmd != nil && b.cmd.Process != nil {
		cmd := b.cmd
		pid := cmd.Process.Pid

		killProcessGroup(pid)

		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			forceKillProcessGroup(pid)
			killProcessOnPort(b.Port)
			<-time.After(500 * time.Millisecond)
		}

		b.cmd = nil
	}

	killProcessOnPort(b.Port)

	return nil
}

// GetPort returns the port the Metro bundler is listening on.
//
// Returns:
//   - int: The port number
func (b *BareRNDevServer) GetPort() int {
	return b.Port
}

// GetDeepLinkURL returns the tunnel URL for connecting a development build.
//
// Bare React Native does not have a standardized deep link format like Expo's
// dev client. The returned URL is the Metro server's tunnel endpoint. The
// caller (worker / device session) is responsible for configuring the app to
// load its JS bundle from this URL.
//
// Parameters:
//   - tunnelURL: The public Cloudflare tunnel URL
//
// Returns:
//   - string: The tunnel URL for bundle loading
func (b *BareRNDevServer) GetDeepLinkURL(tunnelURL string) string {
	return tunnelURL
}

// Name returns the human-readable name of this dev server provider.
//
// Returns:
//   - string: "React Native"
func (b *BareRNDevServer) Name() string {
	return "React Native"
}

// SetProxyURL sets the tunnel URL used for REACT_NATIVE_PACKAGER_HOSTNAME.
//
// Must be called before Start() for the setting to take effect.
//
// Parameters:
//   - tunnelURL: The public tunnel URL (e.g., "https://xxx.trycloudflare.com")
func (b *BareRNDevServer) SetProxyURL(tunnelURL string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.proxyURL = tunnelURL
}

// SetOutputCallback registers a callback for Metro process output lines.
func (b *BareRNDevServer) SetOutputCallback(callback hotreload.DevServerOutputCallback) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.outputCallback = callback
}

// SetDebugMode toggles watch-friendly Metro startup behavior.
func (b *BareRNDevServer) SetDebugMode(enabled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.debugMode = enabled
}

func (b *BareRNDevServer) emitProcessOutput(callback hotreload.DevServerOutputCallback, stream hotreload.DevServerOutputStream, line string) {
	if callback == nil {
		return
	}
	callback(hotreload.DevServerOutput{
		Stream: stream,
		Line:   line,
	})
}

// streamStdout emits every stdout line and signals readiness when Metro indicators appear.
func (b *BareRNDevServer) streamStdout(stdout io.Reader, outputCallback hotreload.DevServerOutputCallback, signalReady func()) {
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		b.emitProcessOutput(outputCallback, hotreload.DevServerOutputStdout, line)
		if b.isReadyIndicator(line) {
			signalReady()
		}
	}
}

func (b *BareRNDevServer) isPortAvailable() bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", b.Port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func (b *BareRNDevServer) metroStartArgs() []string {
	return []string{
		"react-native",
		"start",
		"--port", fmt.Sprintf("%d", b.Port),
	}
}

func (b *BareRNDevServer) metroEnvironment() []string {
	// Strip CI so Metro runs in full dev mode with HMR/Fast Refresh enabled.
	env := envWithoutKey(os.Environ(), "CI")
	return env
}

// isReadyIndicator checks if a log line indicates Metro is ready.
// These are standard Metro bundler output lines, shared with Expo since both
// use Metro under the hood.
func (b *BareRNDevServer) isReadyIndicator(line string) bool {
	return isMetroReadyIndicator(line)
}

func (b *BareRNDevServer) checkHealth() bool {
	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/status", b.Port))
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
func (b *BareRNDevServer) IsReady() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ready
}
