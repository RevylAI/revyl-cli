package providers

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/revyl/cli/internal/hotreload"
)

// FlutterAttachDevServer drives `flutter attach` against a running debug build
// on a cloud device. The app exposes its Dart VM Service on the device; a
// ReverseTunnelBackend bridges that port to a local address (debugURL), and
// this dev server attaches to it and issues hot reload / hot restart in
// response to file changes.
//
// Unlike the Metro dev server, this one actively drives the running app rather
// than passively serving a bundle, so it implements hotreload.Reloadable.
type FlutterAttachDevServer struct {
	workDir  string
	debugURL string

	// commandFn is injectable for tests. Defaults to exec.CommandContext.
	commandFn func(ctx context.Context, name string, args ...string) *exec.Cmd

	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	onOutput hotreload.DevServerOutputCallback
}

// Compile-time assertions that the dev server satisfies the expected contracts.
var (
	_ hotreload.DevServer              = (*FlutterAttachDevServer)(nil)
	_ hotreload.Reloadable             = (*FlutterAttachDevServer)(nil)
	_ hotreload.DevServerOutputEmitter = (*FlutterAttachDevServer)(nil)
)

// NewFlutterAttachDevServer creates a Flutter attach dev server. debugURL is
// the local address produced by the reverse tunnel (e.g.
// "http://127.0.0.1:54321/"); it may be set later via SetDebugURL.
func NewFlutterAttachDevServer(workDir, debugURL string) *FlutterAttachDevServer {
	return &FlutterAttachDevServer{
		workDir:   workDir,
		debugURL:  strings.TrimSpace(debugURL),
		commandFn: exec.CommandContext,
	}
}

// SetDebugURL sets the VM Service URL to attach to. Must be called before Start.
func (f *FlutterAttachDevServer) SetDebugURL(debugURL string) {
	f.mu.Lock()
	f.debugURL = strings.TrimSpace(debugURL)
	f.mu.Unlock()
}

// SetOutputCallback registers a callback for process output lines.
func (f *FlutterAttachDevServer) SetOutputCallback(cb hotreload.DevServerOutputCallback) {
	f.mu.Lock()
	f.onOutput = cb
	f.mu.Unlock()
}

// attachArgs builds the `flutter attach` argument list. Exposed for testing.
func (f *FlutterAttachDevServer) attachArgs() []string {
	return []string{"attach", "--no-version-check", "--debug-url", f.debugURL}
}

// Start launches `flutter attach` and begins streaming its output.
func (f *FlutterAttachDevServer) Start(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.debugURL == "" {
		return fmt.Errorf("flutter attach requires a VM Service debug URL (reverse tunnel not established)")
	}
	if f.cmd != nil {
		return fmt.Errorf("flutter attach is already running")
	}

	cmd := f.commandFn(ctx, "flutter", f.attachArgs()...)
	cmd.Dir = f.workDir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to open flutter attach stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to open flutter attach stdout: %w", err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start flutter attach: %w", err)
	}

	f.cmd = cmd
	f.stdin = stdin
	go f.streamOutput(stdout)
	return nil
}

func (f *FlutterAttachDevServer) streamOutput(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		f.mu.Lock()
		cb := f.onOutput
		f.mu.Unlock()
		if cb != nil {
			cb(hotreload.DevServerOutput{Stream: hotreload.DevServerOutputStdout, Line: line})
		}
	}
}

// Reload issues a Flutter hot reload ("r").
func (f *FlutterAttachDevServer) Reload(ctx context.Context) error {
	return f.sendKey("r")
}

// HotRestart issues a Flutter hot restart ("R").
func (f *FlutterAttachDevServer) HotRestart(ctx context.Context) error {
	return f.sendKey("R")
}

func (f *FlutterAttachDevServer) sendKey(key string) error {
	f.mu.Lock()
	stdin := f.stdin
	f.mu.Unlock()
	if stdin == nil {
		return fmt.Errorf("flutter attach is not running")
	}
	if _, err := io.WriteString(stdin, key+"\n"); err != nil {
		return fmt.Errorf("failed to send %q to flutter attach: %w", key, err)
	}
	return nil
}

// Stop terminates the flutter attach process.
func (f *FlutterAttachDevServer) Stop() error {
	f.mu.Lock()
	cmd := f.cmd
	stdin := f.stdin
	f.cmd = nil
	f.stdin = nil
	f.mu.Unlock()

	if cmd == nil {
		return nil
	}
	if stdin != nil {
		_, _ = io.WriteString(stdin, "q\n")
		_ = stdin.Close()
	}
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
	return nil
}

// GetPort returns 0; Flutter attach is not an HTTP server the device pulls from.
func (f *FlutterAttachDevServer) GetPort() int { return 0 }

// GetDeepLinkURL is a no-op; attach uses a VM Service URL, not a deep link.
func (f *FlutterAttachDevServer) GetDeepLinkURL(tunnelURL string) string { return "" }

// SetProxyURL is a no-op; there is no Metro bundle URL to rewrite.
func (f *FlutterAttachDevServer) SetProxyURL(tunnelURL string) {}

// Name returns the human-readable provider name.
func (f *FlutterAttachDevServer) Name() string { return "Flutter" }
