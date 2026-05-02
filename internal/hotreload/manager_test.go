package hotreload

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/revyl/cli/internal/config"
)

type fakeDevServer struct{}

func (f *fakeDevServer) Start(ctx context.Context) error { return nil }
func (f *fakeDevServer) Stop() error                     { return nil }
func (f *fakeDevServer) GetPort() int                    { return 8081 }
func (f *fakeDevServer) GetDeepLinkURL(tunnelURL string) string {
	return tunnelURL
}
func (f *fakeDevServer) Name() string                 { return "fake" }
func (f *fakeDevServer) SetProxyURL(tunnelURL string) {}

type fakeOutputDevServer struct {
	fakeDevServer
	callback DevServerOutputCallback
}

func (f *fakeOutputDevServer) SetOutputCallback(callback DevServerOutputCallback) {
	f.callback = callback
}

type fakeDebugDevServer struct {
	fakeDevServer
	debugMode bool
}

func (f *fakeDebugDevServer) SetDebugMode(enabled bool) {
	f.debugMode = enabled
}

type fakeTunnelBackend struct {
	publicURL string
}

func (f *fakeTunnelBackend) Start(_ context.Context, _ int) (string, error) {
	return f.publicURL, nil
}

func (f *fakeTunnelBackend) StartHealthMonitor(_ context.Context) {}

func (f *fakeTunnelBackend) Stop() error { return nil }

func (f *fakeTunnelBackend) PublicURL() string { return f.publicURL }

type failingTunnelBackend struct{}

func (f *failingTunnelBackend) Start(_ context.Context, _ int) (string, error) {
	return "", errors.New("relay unavailable")
}

func (f *failingTunnelBackend) StartHealthMonitor(_ context.Context) {}

func (f *failingTunnelBackend) Stop() error { return nil }

func (f *failingTunnelBackend) PublicURL() string { return "" }

func withFakeExpoDevServerFactory(t *testing.T) {
	t.Helper()
	previous := expoDevServerFactory
	expoDevServerFactory = func(workDir, appScheme string, port int, useExpPrefix bool) DevServer {
		return &fakeDevServer{}
	}
	t.Cleanup(func() {
		expoDevServerFactory = previous
	})
}

func withFakePostStartupDiagnostics(t *testing.T, called chan<- struct{}) {
	t.Helper()
	previous := postStartupDiagnostics
	postStartupDiagnostics = func(localPort int, tunnelURL string, providerName string, targetPlatform string) *DiagnosticResult {
		select {
		case called <- struct{}{}:
		default:
		}
		return &DiagnosticResult{AllPassed: true}
	}
	t.Cleanup(func() {
		postStartupDiagnostics = previous
	})
}

func withFakeExpoMetroRelayReady(t *testing.T, called chan<- struct{}) {
	t.Helper()
	previous := waitForExpoMetroRelay
	waitForExpoMetroRelay = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
	) (*DiagnosticResult, error) {
		select {
		case called <- struct{}{}:
		default:
		}
		return &DiagnosticResult{AllPassed: true}, nil
	}
	t.Cleanup(func() {
		waitForExpoMetroRelay = previous
	})
}

func newTestManagerWithFakeTunnel() *Manager {
	m := NewManager("expo", &config.ProviderConfig{AppScheme: "myapp"}, ".")
	m.SetTunnelBackendFactory(func() TunnelBackend {
		return &fakeTunnelBackend{publicURL: "https://relay.example"}
	})
	return m
}

func TestAttachDevServerOutputCallback_AttachesWhenSupported(t *testing.T) {
	m := &Manager{}
	devServer := &fakeOutputDevServer{}

	var received DevServerOutput
	m.SetDevServerOutputCallback(func(output DevServerOutput) {
		received = output
	})

	m.attachDevServerOutputCallback(devServer)

	if devServer.callback == nil {
		t.Fatal("expected output callback to be attached")
	}

	devServer.callback(DevServerOutput{
		Stream: DevServerOutputStdout,
		Line:   "Metro ready",
	})

	if received.Stream != DevServerOutputStdout {
		t.Fatalf("received stream = %q, want %q", received.Stream, DevServerOutputStdout)
	}
	if received.Line != "Metro ready" {
		t.Fatalf("received line = %q, want %q", received.Line, "Metro ready")
	}
}

func TestAttachDevServerOutputCallback_NoConfiguredCallback(t *testing.T) {
	m := &Manager{}
	devServer := &fakeOutputDevServer{}

	m.attachDevServerOutputCallback(devServer)

	if devServer.callback != nil {
		t.Fatal("expected callback to remain nil when manager callback is unset")
	}
}

func TestAttachDevServerOutputCallback_IgnoresUnsupportedServer(t *testing.T) {
	m := &Manager{}
	m.SetDevServerOutputCallback(func(output DevServerOutput) {})

	unsupported := &fakeDevServer{}
	m.attachDevServerOutputCallback(unsupported)
}

func TestAttachDevServerDebugMode_AttachesWhenSupported(t *testing.T) {
	m := &Manager{}
	m.SetDebugMode(true)

	devServer := &fakeDebugDevServer{}
	m.attachDevServerDebugMode(devServer)

	if !devServer.debugMode {
		t.Fatal("expected debug mode to be enabled on dev server")
	}
}

func TestAttachDevServerDebugMode_IgnoresUnsupportedServer(t *testing.T) {
	m := &Manager{}
	m.SetDebugMode(true)

	unsupported := &fakeDevServer{}
	m.attachDevServerDebugMode(unsupported)
}

func TestManagerStartExternalUsesProvidedDeepLinkWithoutProviderConfig(t *testing.T) {
	m := NewManager("expo", nil, ".")
	m.SetExternalTunnelURL("https://example.ngrok.app")
	m.SetExternalDeepLinkURL("myapp://expo-development-client/?url=https%3A%2F%2Fexample.ngrok.app")

	result, err := m.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if result.TunnelURL != "https://example.ngrok.app" {
		t.Fatalf("TunnelURL = %q, want external tunnel", result.TunnelURL)
	}
	if result.DeepLinkURL != "myapp://expo-development-client/?url=https%3A%2F%2Fexample.ngrok.app" {
		t.Fatalf("DeepLinkURL = %q, want provided deep link", result.DeepLinkURL)
	}
	if result.Transport != "external" {
		t.Fatalf("Transport = %q, want external", result.Transport)
	}
}

func TestManagerStartExternalBuildsDeepLinkWhenOnlyTunnelProvided(t *testing.T) {
	m := NewManager("expo", &config.ProviderConfig{AppScheme: "myapp"}, ".")
	m.SetExternalTunnelURL("https://example.ngrok.app")

	result, err := m.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if result.DeepLinkURL != "myapp://expo-development-client/?url=https%3A%2F%2Fexample.ngrok.app" {
		t.Fatalf("DeepLinkURL = %q, want derived Expo deep link", result.DeepLinkURL)
	}
}

func TestManagerStartSkipsPostStartupDiagnosticsByDefault(t *testing.T) {
	withFakeExpoDevServerFactory(t)
	withFakeExpoMetroRelayReady(t, nil)
	called := make(chan struct{}, 1)
	withFakePostStartupDiagnostics(t, called)

	m := newTestManagerWithFakeTunnel()
	result, err := m.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { m.Stop() })
	if result.TunnelURL == "" {
		t.Fatal("expected Start to return a tunnel URL")
	}

	select {
	case <-called:
		t.Fatal("post-startup diagnostics ran without debug mode")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestManagerStartRunsPostStartupDiagnosticsInDebugMode(t *testing.T) {
	withFakeExpoDevServerFactory(t)
	withFakeExpoMetroRelayReady(t, nil)
	called := make(chan struct{}, 1)
	withFakePostStartupDiagnostics(t, called)

	m := newTestManagerWithFakeTunnel()
	m.SetDebugMode(true)
	if _, err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { m.Stop() })

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("post-startup diagnostics did not run in debug mode")
	}
}

func TestManagerStartWaitsForExpoMetroRelay(t *testing.T) {
	withFakeExpoDevServerFactory(t)
	relayCalled := make(chan struct{}, 1)
	withFakeExpoMetroRelayReady(t, relayCalled)

	m := newTestManagerWithFakeTunnel()
	if _, err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { m.Stop() })

	select {
	case <-relayCalled:
	case <-time.After(time.Second):
		t.Fatal("expected Start to wait for Expo relay readiness")
	}
}

func TestManagerStartPassesTargetPlatformToExpoMetroRelay(t *testing.T) {
	withFakeExpoDevServerFactory(t)
	previous := waitForExpoMetroRelay
	platforms := make(chan string, 1)
	waitForExpoMetroRelay = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
	) (*DiagnosticResult, error) {
		platforms <- targetPlatform
		return &DiagnosticResult{AllPassed: true}, nil
	}
	t.Cleanup(func() {
		waitForExpoMetroRelay = previous
	})

	m := newTestManagerWithFakeTunnel()
	m.SetTargetPlatform("android")
	if _, err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { m.Stop() })

	select {
	case platform := <-platforms:
		if platform != "android" {
			t.Fatalf("target platform = %q, want android", platform)
		}
	case <-time.After(time.Second):
		t.Fatal("expected Start to call Expo relay readiness")
	}
}

func TestManagerStartForceHotReloadContinuesAfterExpoRelayReadinessFailure(t *testing.T) {
	withFakeExpoDevServerFactory(t)
	previous := waitForExpoMetroRelay
	waitForExpoMetroRelay = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
	) (*DiagnosticResult, error) {
		return &DiagnosticResult{
			AllPassed: false,
			Checks: []DiagnosticCheck{
				{Name: "Manifest URLs", Passed: false, Detail: "parse failed: invalid character '<'"},
			},
		}, errors.New("Manifest URLs (parse failed: invalid character '<')")
	}
	t.Cleanup(func() {
		waitForExpoMetroRelay = previous
	})

	var logs []string
	m := newTestManagerWithFakeTunnel()
	m.SetForceHotReload(true)
	m.SetLogCallback(func(message string) {
		logs = append(logs, message)
	})

	result, err := m.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { m.Stop() })
	if result == nil || result.TunnelURL == "" {
		t.Fatalf("expected Start result with tunnel URL, got %+v", result)
	}
	if !m.IsRunning() {
		t.Fatal("expected manager to keep running in force mode")
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "--force-hot-reload is set") {
		t.Fatalf("logs = %q, want force warning", joined)
	}
	if !strings.Contains(joined, "Manifest URLs") {
		t.Fatalf("logs = %q, want failed readiness detail", joined)
	}
}

func TestManagerStartForceHotReloadDoesNotBypassTunnelStartFailure(t *testing.T) {
	withFakeExpoDevServerFactory(t)

	m := NewManager("expo", &config.ProviderConfig{AppScheme: "myapp"}, ".")
	m.SetForceHotReload(true)
	m.SetTunnelBackendFactory(func() TunnelBackend {
		return &failingTunnelBackend{}
	})

	if _, err := m.Start(context.Background()); err == nil {
		t.Fatal("expected tunnel start failure")
	}
}

func TestRunDiagnosticsUsesAdvisoryFailureLanguage(t *testing.T) {
	previous := postStartupDiagnostics
	postStartupDiagnostics = func(localPort int, tunnelURL string, providerName string, targetPlatform string) *DiagnosticResult {
		return &DiagnosticResult{
			AllPassed: false,
			Checks: []DiagnosticCheck{
				{Name: "Tunnel HTTP", Passed: false, Detail: "status 502"},
			},
		}
	}
	t.Cleanup(func() {
		postStartupDiagnostics = previous
	})

	var logs []string
	m := &Manager{}
	m.SetLogCallback(func(message string) {
		logs = append(logs, message)
	})

	m.runDiagnostics(8081, "https://relay.example")

	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "advisory warning") {
		t.Fatalf("logs = %q, want advisory warning language", joined)
	}
	if strings.Contains(joined, "FAILED") {
		t.Fatalf("logs = %q, should not use hard failure wording", joined)
	}
}
