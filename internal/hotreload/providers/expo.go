package providers

import (
	"bufio"
	"context"
	"fmt"
	"io"
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
	hotreload.RegisterExpoDevServerFactory(func(workDir, appScheme string, port int, useExpPrefix bool) hotreload.DevServer {
		return NewExpoDevServer(workDir, appScheme, port, useExpPrefix)
	})
}

// ExpoDevServer manages an Expo development server via npx expo start --dev-client.
type ExpoDevServer struct {
	Port      int
	AppScheme string
	// UseExpPrefix prepends "exp+" to AppScheme in deep links (exp+{scheme}://...).
	UseExpPrefix   bool
	WorkDir        string
	proxyURL       string
	cmd            *exec.Cmd
	cancel         context.CancelFunc
	processDone    chan error
	stderr         *processStderr
	stopping       bool
	mu             sync.Mutex
	ready          bool
	outputCallback hotreload.DevServerOutputCallback
	debugMode      bool
	failureCh      chan hotreload.RuntimeFailure
}

func NewExpoDevServer(workDir, appScheme string, port int, useExpPrefix bool) *ExpoDevServer {
	if port == 0 {
		port = 8081
	}
	return &ExpoDevServer{
		Port:         port,
		AppScheme:    appScheme,
		UseExpPrefix: useExpPrefix,
		WorkDir:      workDir,
		failureCh:    make(chan hotreload.RuntimeFailure, 4),
	}
}

func (e *ExpoDevServer) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	outputCallback := e.outputCallback

	if _, err := exec.LookPath("npx"); err != nil {
		return fmt.Errorf("npx not found. Install Node.js: https://nodejs.org/")
	}

	if !e.isPortAvailable() {
		return fmt.Errorf("port %d is already in use. Stop the existing process or use --port to specify a different port\n\nTo kill the process using port %d, run:\n  lsof -ti :%d | xargs kill -9\n\nOr specify a different port:\n  revyl dev --port 8082", e.Port, e.Port, e.Port)
	}

	ctx, e.cancel = context.WithCancel(ctx)

	e.cmd = exec.CommandContext(ctx, "npx", e.expoStartArgs()...)
	e.cmd.Dir = e.WorkDir

	setSysProcAttr(e.cmd)

	e.cmd.Env = e.expoEnvironment()

	if e.proxyURL != "" {
		normalizedURL, hostname := normalizeProxyURL(e.proxyURL)
		e.cmd.Env = append(e.cmd.Env, fmt.Sprintf("EXPO_PACKAGER_PROXY_URL=%s", normalizedURL))
		if hostname != "" {
			e.cmd.Env = append(e.cmd.Env, fmt.Sprintf("REACT_NATIVE_PACKAGER_HOSTNAME=%s", hostname))
		}
	}

	stdout, err := e.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to capture stdout: %w", err)
	}

	errChan := make(chan error, 1)
	e.stderr = newProcessStderr(func(line string) {
		e.emitProcessOutput(outputCallback, hotreload.DevServerOutputStderr, line)
		lowerLine := strings.ToLower(line)
		if strings.Contains(lowerLine, "error") && strings.Contains(lowerLine, "fatal") {
			select {
			case errChan <- fmt.Errorf("Expo error: %s", line):
			default:
			}
		}
	})
	e.cmd.Stderr = e.stderr
	e.cmd.WaitDelay = processOutputWaitDelay

	if err := e.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start Expo dev server: %w", err)
	}
	e.stopping = false
	e.processDone = make(chan error, 1)
	go e.watchProcess(ctx, e.cmd, e.processDone)

	readyChan := make(chan bool, 1)
	signalReady := newReadyNotifier(readyChan)

	go func() {
		e.streamStdout(stdout, outputCallback, signalReady)
	}()

	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e.checkHealth() {
					signalReady()
					return
				}
			}
		}
	}()

	select {
	case <-readyChan:
		e.ready = true
		return nil
	case err := <-errChan:
		_ = e.stopLocked()
		return err
	case err := <-e.processDone:
		e.ready = false
		e.cmd = nil
		e.processDone = nil
		if e.cancel != nil {
			e.cancel()
			e.cancel = nil
		}
		return processExitError("Expo dev server exited before it became ready", err)
	case <-time.After(60 * time.Second):
		_ = e.stopLocked()
		return fmt.Errorf("timeout waiting for Expo dev server to start (60s)")
	case <-ctx.Done():
		_ = e.stopLocked()
		return ctx.Err()
	}
}

func (e *ExpoDevServer) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stopLocked()
}

// Callers must hold e.mu.
func (e *ExpoDevServer) stopLocked() error {

	e.ready = false
	e.stopping = true

	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}

	if e.cmd != nil && e.cmd.Process != nil {
		cmd := e.cmd
		pid := cmd.Process.Pid
		done := e.processDone

		killProcessGroup(pid)

		if done != nil {
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				forceKillProcessGroup(pid)
				<-time.After(500 * time.Millisecond)
			}
		}

		e.cmd = nil
		e.processDone = nil
	}

	return nil
}

func (e *ExpoDevServer) watchProcess(ctx context.Context, cmd *exec.Cmd, done chan<- error) {
	err := cmd.Wait()
	done <- err

	e.mu.Lock()
	shouldReport := e.ready && !e.stopping && e.cmd == cmd && ctx.Err() == nil
	port := e.Port
	stderr := e.stderr
	e.mu.Unlock()
	if !shouldReport {
		return
	}
	e.emitFailure(unexpectedProcessExitFailure(e.Name(), port, "Expo dev server exited unexpectedly", err, stderr.Tail()))
}

func (e *ExpoDevServer) Failures() <-chan hotreload.RuntimeFailure {
	return e.failureCh
}

func (e *ExpoDevServer) emitFailure(f hotreload.RuntimeFailure) {
	if e.failureCh == nil {
		return
	}
	select {
	case e.failureCh <- f:
	default:
	}
}

func (e *ExpoDevServer) GetPort() int {
	return e.Port
}

// BuildExpoDeepLinkURL builds an Expo dev-client deep link for tunnelURL.
func BuildExpoDeepLinkURL(appScheme string, useExpPrefix bool, tunnelURL string) string {
	encodedURL := url.QueryEscape(tunnelURL)
	if useExpPrefix {
		return fmt.Sprintf("exp+%s://expo-development-client/?url=%s", appScheme, encodedURL)
	}
	return fmt.Sprintf("%s://expo-development-client/?url=%s", appScheme, encodedURL)
}

// GetDeepLinkURL builds the Expo dev-client deep link, or returns tunnelURL when AppScheme is empty.
func (e *ExpoDevServer) GetDeepLinkURL(tunnelURL string) string {
	if e.AppScheme == "" {
		return tunnelURL
	}
	encodedURL := url.QueryEscape(tunnelURL)

	if e.UseExpPrefix {
		return fmt.Sprintf("exp+%s://expo-development-client/?url=%s", e.AppScheme, encodedURL)
	}
	return fmt.Sprintf("%s://expo-development-client/?url=%s", e.AppScheme, encodedURL)
}

func (e *ExpoDevServer) Name() string {
	return "Expo"
}

func (e *ExpoDevServer) SetProxyURL(tunnelURL string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.proxyURL = tunnelURL
}

func (e *ExpoDevServer) SetOutputCallback(callback hotreload.DevServerOutputCallback) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.outputCallback = callback
}

func (e *ExpoDevServer) SetDebugMode(enabled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.debugMode = enabled
}

func (e *ExpoDevServer) emitProcessOutput(callback hotreload.DevServerOutputCallback, stream hotreload.DevServerOutputStream, line string) {
	if callback == nil {
		return
	}
	callback(hotreload.DevServerOutput{
		Stream: stream,
		Line:   line,
	})
}

func (e *ExpoDevServer) streamStdout(stdout io.Reader, outputCallback hotreload.DevServerOutputCallback, signalReady func()) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), hotreload.MaxDevServerOutputLineBytes)
	for scanner.Scan() {
		line := scanner.Text()
		e.emitProcessOutput(outputCallback, hotreload.DevServerOutputStdout, line)
		if e.isReadyIndicator(line) {
			signalReady()
		}
		if hmrEvent := classifyHMREvent(line); hmrEvent != "" {
			e.emitProcessOutput(outputCallback, hotreload.DevServerOutputHMR, hmrEvent)
		}
	}
}

func classifyHMREvent(line string) string {
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, "bundling"):
		return "File change detected, bundling..."
	case strings.Contains(lower, "bundled") && !strings.Contains(lower, "error"):
		return "Bundle complete"
	case strings.Contains(lower, "hmr update"):
		return "HMR update sent"
	case strings.Contains(lower, "transforming"):
		return "File change detected, transforming..."
	default:
		return ""
	}
}

func (e *ExpoDevServer) isPortAvailable() bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", e.Port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func (e *ExpoDevServer) expoStartArgs() []string {
	args := []string{
		"expo",
		"start",
		"--dev-client",
		"--port", fmt.Sprintf("%d", e.Port),
	}
	if !e.debugMode {
		args = append(args, "--non-interactive")
	}
	return args
}

func (e *ExpoDevServer) expoEnvironment() []string {
	env := envWithoutKey(os.Environ(), "EXPO_NO_TELEMETRY")
	env = append(env, "EXPO_NO_TELEMETRY=1")
	env = envWithoutKey(env, "CI")
	return env
}

// normalizeProxyURL adds an explicit default port (:443/:80) because React Native's
// WebSocket URL parser fails to infer default HTTPS ports (Expo #42316).
func normalizeProxyURL(rawURL string) (normalized string, hostname string) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL, ""
	}
	hostname = parsed.Hostname()
	if parsed.Port() == "" {
		switch parsed.Scheme {
		case "https":
			parsed.Host = hostname + ":443"
		case "http":
			parsed.Host = hostname + ":80"
		}
	}
	return parsed.String(), hostname
}

func envWithoutKey(env []string, key string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			continue
		}
		filtered = append(filtered, kv)
	}
	return filtered
}

func newReadyNotifier(readyChan chan<- bool) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			select {
			case readyChan <- true:
			default:
			}
		})
	}
}

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

func (e *ExpoDevServer) checkHealth() bool {
	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/status", e.Port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

func (e *ExpoDevServer) IsReady() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ready
}
