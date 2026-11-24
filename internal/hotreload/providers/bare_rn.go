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

// BareRNDevServer manages a bare React Native Metro server via npx react-native start.
type BareRNDevServer struct {
	Port     int
	WorkDir  string
	proxyURL string

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

func NewBareRNDevServer(workDir string, port int) *BareRNDevServer {
	if port == 0 {
		port = 8081
	}
	return &BareRNDevServer{
		Port:      port,
		WorkDir:   workDir,
		failureCh: make(chan hotreload.RuntimeFailure, 4),
	}
}

func (b *BareRNDevServer) Start(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	outputCallback := b.outputCallback

	if _, err := exec.LookPath("npx"); err != nil {
		return fmt.Errorf("npx not found. Install Node.js: https://nodejs.org/")
	}

	if !b.isPortAvailable() {
		return fmt.Errorf("port %d is already in use. Stop the existing process or use --port to specify a different port\n\nTo kill the process using port %d, run:\n  lsof -ti :%d | xargs kill -9\n\nOr specify a different port:\n  revyl dev --port 8082", b.Port, b.Port, b.Port)
	}

	ctx, b.cancel = context.WithCancel(ctx)

	b.cmd = exec.CommandContext(ctx, "npx", b.metroStartArgs()...)
	b.cmd.Dir = b.WorkDir

	setSysProcAttr(b.cmd)

	b.cmd.Env = b.metroEnvironment()

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

	errChan := make(chan error, 1)
	b.stderr = newProcessStderr(func(line string) {
		b.emitProcessOutput(outputCallback, hotreload.DevServerOutputStderr, line)
		if isMetroFatalError(line) {
			select {
			case errChan <- fmt.Errorf("Metro error: %s", line):
			default:
			}
		}
	})
	b.cmd.Stderr = b.stderr
	b.cmd.WaitDelay = processOutputWaitDelay

	if err := b.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start Metro bundler: %w", err)
	}
	b.stopping = false
	b.processDone = make(chan error, 1)
	go b.watchProcess(ctx, b.cmd, b.processDone)

	readyChan := make(chan bool, 1)
	signalReady := newReadyNotifier(readyChan)

	go func() {
		b.streamStdout(stdout, outputCallback, signalReady)
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
	case err := <-b.processDone:
		b.ready = false
		b.cmd = nil
		b.processDone = nil
		if b.cancel != nil {
			b.cancel()
			b.cancel = nil
		}
		return processExitError("Metro bundler exited before it became ready", err)
	case <-time.After(90 * time.Second):
		_ = b.stopLocked()
		return fmt.Errorf("timeout waiting for Metro bundler to start (90s)")
	case <-ctx.Done():
		_ = b.stopLocked()
		return ctx.Err()
	}
}

func (b *BareRNDevServer) Stop() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stopLocked()
}

func (b *BareRNDevServer) stopLocked() error {
	b.ready = false
	b.stopping = true

	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}

	if b.cmd != nil && b.cmd.Process != nil {
		cmd := b.cmd
		pid := cmd.Process.Pid
		done := b.processDone

		killProcessGroup(pid)

		if done != nil {
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				forceKillProcessGroup(pid)
				<-time.After(500 * time.Millisecond)
			}
		}

		b.cmd = nil
		b.processDone = nil
	}

	return nil
}

func (b *BareRNDevServer) watchProcess(ctx context.Context, cmd *exec.Cmd, done chan<- error) {
	err := cmd.Wait()
	done <- err

	b.mu.Lock()
	shouldReport := b.ready && !b.stopping && b.cmd == cmd && ctx.Err() == nil
	port := b.Port
	stderr := b.stderr
	b.mu.Unlock()
	if !shouldReport {
		return
	}
	b.emitFailure(unexpectedProcessExitFailure(b.Name(), port, "Metro bundler exited unexpectedly", err, stderr.Tail()))
}

func (b *BareRNDevServer) Failures() <-chan hotreload.RuntimeFailure {
	return b.failureCh
}

func (b *BareRNDevServer) emitFailure(f hotreload.RuntimeFailure) {
	if b.failureCh == nil {
		return
	}
	select {
	case b.failureCh <- f:
	default:
	}
}

func (b *BareRNDevServer) GetPort() int {
	return b.Port
}

// GetDeepLinkURL returns tunnelURL directly because bare React Native has no dev-client deep-link scheme.
func (b *BareRNDevServer) GetDeepLinkURL(tunnelURL string) string {
	return tunnelURL
}

func (b *BareRNDevServer) Name() string {
	return "React Native"
}

func (b *BareRNDevServer) SetProxyURL(tunnelURL string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.proxyURL = tunnelURL
}

func (b *BareRNDevServer) SetOutputCallback(callback hotreload.DevServerOutputCallback) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.outputCallback = callback
}

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

func (b *BareRNDevServer) streamStdout(stdout io.Reader, outputCallback hotreload.DevServerOutputCallback, signalReady func()) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), hotreload.MaxDevServerOutputLineBytes)
	for scanner.Scan() {
		line := scanner.Text()
		b.emitProcessOutput(outputCallback, hotreload.DevServerOutputStdout, line)
		if b.isReadyIndicator(line) {
			signalReady()
		}
	}
}

func (b *BareRNDevServer) isPortAvailable() bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", b.Port))
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
	env := envWithoutKey(os.Environ(), "CI")
	return env
}

func (b *BareRNDevServer) isReadyIndicator(line string) bool {
	return isMetroReadyIndicator(line)
}

func (b *BareRNDevServer) checkHealth() bool {
	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/status", b.Port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

func (b *BareRNDevServer) IsReady() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ready
}
