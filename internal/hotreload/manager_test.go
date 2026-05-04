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

type fakeLoggingTunnelBackend struct {
	publicURL string
	onLog     func(string)
}

func (f *fakeLoggingTunnelBackend) SetLogCallback(callback func(string)) {
	f.onLog = callback
}

func (f *fakeLoggingTunnelBackend) Start(_ context.Context, _ int) (string, error) {
	if f.onLog != nil {
		f.onLog("[relay] reserved relay session id=a-test transport=relay")
		f.onLog("[relay] connection lost: relay websocket disconnected")
		f.onLog("[relay] reconnected to backend relay id=a-test transport=relay")
	}
	return f.publicURL, nil
}

func (f *fakeLoggingTunnelBackend) StartHealthMonitor(_ context.Context) {}

func (f *fakeLoggingTunnelBackend) Stop() error { return nil }

func (f *fakeLoggingTunnelBackend) PublicURL() string { return f.publicURL }

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
	previousTransport := waitForExpoMetroTransport
	previousManifest := waitForExpoManifestFetchResult
	previousPrewarm := waitForExpoBundlePrewarmFromManifest
	waitForExpoMetroTransport = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
	) (*DiagnosticResult, error) {
		select {
		case called <- struct{}{}:
		default:
		}
		return &DiagnosticResult{AllPassed: true}, nil
	}
	waitForExpoManifestFetchResult = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
	) (expoManifestFetchResult, *DiagnosticResult, error) {
		return expoManifestFetchResult{Manifest: map[string]any{}, Platform: targetPlatform}, &DiagnosticResult{AllPassed: true}, nil
	}
	waitForExpoBundlePrewarmFromManifest = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		fetched expoManifestFetchResult,
	) (*DiagnosticResult, error) {
		return &DiagnosticResult{AllPassed: true, Checks: []DiagnosticCheck{{Name: "Bundle prewarm", Passed: true, Detail: "OK"}}}, nil
	}
	t.Cleanup(func() {
		waitForExpoMetroTransport = previousTransport
		waitForExpoManifestFetchResult = previousManifest
		waitForExpoBundlePrewarmFromManifest = previousPrewarm
	})
}

func newTestManagerWithFakeTunnel() *Manager {
	m := NewManager("expo", &config.ProviderConfig{AppScheme: "myapp"}, ".")
	m.SetTunnelBackendFactory(func() TunnelBackend {
		return &fakeTunnelBackend{publicURL: "https://relay.example"}
	})
	return m
}

func newTestManagerWithLoggingTunnel() *Manager {
	m := NewManager("expo", &config.ProviderConfig{AppScheme: "myapp"}, ".")
	m.SetTunnelBackendFactory(func() TunnelBackend {
		return &fakeLoggingTunnelBackend{publicURL: "https://relay.example"}
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

func TestManagerStartWaitsForExpoMetroTransportManifestAndBundlePrewarm(t *testing.T) {
	withFakeExpoDevServerFactory(t)
	previousTransport := waitForExpoMetroTransport
	previousManifest := waitForExpoManifestFetchResult
	previousPrewarm := waitForExpoBundlePrewarmFromManifest
	transportCalled := make(chan struct{}, 1)
	manifestCalled := make(chan struct{}, 1)
	prewarmCalled := make(chan struct{}, 1)
	waitForExpoMetroTransport = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
	) (*DiagnosticResult, error) {
		transportCalled <- struct{}{}
		return &DiagnosticResult{AllPassed: true}, nil
	}
	waitForExpoManifestFetchResult = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
	) (expoManifestFetchResult, *DiagnosticResult, error) {
		manifestCalled <- struct{}{}
		return expoManifestFetchResult{Manifest: map[string]any{"source": "manifest-proof"}, Platform: targetPlatform}, &DiagnosticResult{AllPassed: true}, nil
	}
	waitForExpoBundlePrewarmFromManifest = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		fetched expoManifestFetchResult,
	) (*DiagnosticResult, error) {
		prewarmCalled <- struct{}{}
		if fetched.Manifest["source"] != "manifest-proof" {
			t.Fatalf("prewarm fetched manifest = %+v, want manifest proof result", fetched.Manifest)
		}
		return &DiagnosticResult{AllPassed: true, Checks: []DiagnosticCheck{{Name: "Bundle prewarm", Passed: true, Detail: "OK"}}}, nil
	}
	t.Cleanup(func() {
		waitForExpoMetroTransport = previousTransport
		waitForExpoManifestFetchResult = previousManifest
		waitForExpoBundlePrewarmFromManifest = previousPrewarm
	})

	m := newTestManagerWithFakeTunnel()
	if _, err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { m.Stop() })

	select {
	case <-transportCalled:
	case <-time.After(time.Second):
		t.Fatal("expected Start to wait for Expo transport readiness")
	}
	select {
	case <-manifestCalled:
	case <-time.After(time.Second):
		t.Fatal("expected Start to wait for Expo manifest readiness")
	}
	select {
	case <-prewarmCalled:
	case <-time.After(time.Second):
		t.Fatal("expected Start to prewarm Expo bundle")
	}
}

func TestManagerStartExpoLogsAreCompactByDefault(t *testing.T) {
	withFakeExpoDevServerFactory(t)
	previousTransport := waitForExpoMetroTransport
	previousManifest := waitForExpoManifestFetchResult
	previousPrewarm := waitForExpoBundlePrewarmFromManifest
	waitForExpoMetroTransport = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
	) (*DiagnosticResult, error) {
		return &DiagnosticResult{AllPassed: true}, nil
	}
	waitForExpoManifestFetchResult = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
	) (expoManifestFetchResult, *DiagnosticResult, error) {
		return expoManifestFetchResult{Manifest: map[string]any{}, Platform: targetPlatform}, &DiagnosticResult{AllPassed: true}, nil
	}
	waitForExpoBundlePrewarmFromManifest = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		fetched expoManifestFetchResult,
	) (*DiagnosticResult, error) {
		return &DiagnosticResult{
			AllPassed: true,
			Checks: []DiagnosticCheck{{
				Name:   "Bundle prewarm",
				Passed: true,
				Detail: "OK platform=ios status=200 ttfb=582ms first_byte=598ms path=/node_modules/expo-router/entry.bundle drain=background",
			}},
		}, nil
	}
	t.Cleanup(func() {
		waitForExpoMetroTransport = previousTransport
		waitForExpoManifestFetchResult = previousManifest
		waitForExpoBundlePrewarmFromManifest = previousPrewarm
	})

	var logs []string
	m := newTestManagerWithLoggingTunnel()
	m.SetLogCallback(func(message string) {
		logs = append(logs, message)
	})
	if _, err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { m.Stop() })

	joined := strings.Join(logs, "\n")
	for _, expected := range []string{
		"Preparing expo dev server...",
		"Starting expo dev server...",
		"fake dev server ready",
		"Verifying Expo relay readiness...",
		"Expo relay readiness verified",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("logs missing %q\nlogs:\n%s", expected, joined)
		}
	}
	for _, unexpected := range []string{
		"[relay]",
		"Tunnel ready:",
		"Configured proxy URL",
		"dev server port:",
		"Waiting for Expo relay transport",
		"Expo relay transport is ready",
		"Waiting for Expo manifest",
		"Expo manifest is being served",
		"Prewarming Expo bundle",
		"Expo bundle prewarm complete",
		"ttfb=",
		"first_byte=",
		"path=/node_modules",
	} {
		if strings.Contains(joined, unexpected) {
			t.Fatalf("logs unexpectedly contain %q\nlogs:\n%s", unexpected, joined)
		}
	}
}

func TestManagerStartExpoLogsDetailedInDebugMode(t *testing.T) {
	withFakeExpoDevServerFactory(t)
	diagnosticsCalled := make(chan struct{}, 1)
	withFakePostStartupDiagnostics(t, diagnosticsCalled)
	previousTransport := waitForExpoMetroTransport
	previousManifest := waitForExpoManifestFetchResult
	previousPrewarm := waitForExpoBundlePrewarmFromManifest
	waitForExpoMetroTransport = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
	) (*DiagnosticResult, error) {
		return &DiagnosticResult{AllPassed: true}, nil
	}
	waitForExpoManifestFetchResult = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
	) (expoManifestFetchResult, *DiagnosticResult, error) {
		return expoManifestFetchResult{Manifest: map[string]any{}, Platform: targetPlatform}, &DiagnosticResult{AllPassed: true}, nil
	}
	waitForExpoBundlePrewarmFromManifest = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		fetched expoManifestFetchResult,
	) (*DiagnosticResult, error) {
		return &DiagnosticResult{
			AllPassed: true,
			Checks: []DiagnosticCheck{{
				Name:   "Bundle prewarm",
				Passed: true,
				Detail: "OK platform=ios status=200 ttfb=582ms first_byte=598ms path=/node_modules/expo-router/entry.bundle drain=background",
			}},
		}, nil
	}
	t.Cleanup(func() {
		waitForExpoMetroTransport = previousTransport
		waitForExpoManifestFetchResult = previousManifest
		waitForExpoBundlePrewarmFromManifest = previousPrewarm
	})

	var logs []string
	m := newTestManagerWithLoggingTunnel()
	m.SetDebugMode(true)
	m.SetLogCallback(func(message string) {
		logs = append(logs, message)
	})
	if _, err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { m.Stop() })

	joined := strings.Join(logs, "\n")
	for _, expected := range []string{
		"[relay] reserved relay session id=a-test transport=relay",
		"[relay] connection lost: relay websocket disconnected",
		"[relay] reconnected to backend relay id=a-test transport=relay",
		"Tunnel ready: https://relay.example",
		"Configured proxy URL for bundle rewriting",
		"fake dev server port: 8081",
		"Waiting for Expo relay transport...",
		"Expo relay transport is ready",
		"Waiting for Expo manifest to be served through the relay...",
		"Expo manifest is being served through the relay",
		"Prewarming Expo bundle through the relay...",
		"Expo bundle prewarm complete: OK platform=ios status=200 ttfb=582ms first_byte=598ms path=/node_modules/expo-router/entry.bundle drain=background",
		"Expo relay readiness verified",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("logs missing %q\nlogs:\n%s", expected, joined)
		}
	}
}

func TestManagerStartPassesTargetPlatformToExpoManifestAndBundlePrewarm(t *testing.T) {
	withFakeExpoDevServerFactory(t)
	previousTransport := waitForExpoMetroTransport
	previousManifest := waitForExpoManifestFetchResult
	previousPrewarm := waitForExpoBundlePrewarmFromManifest
	manifestPlatforms := make(chan string, 1)
	prewarmPlatforms := make(chan string, 1)
	waitForExpoMetroTransport = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
	) (*DiagnosticResult, error) {
		return &DiagnosticResult{AllPassed: true}, nil
	}
	waitForExpoManifestFetchResult = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
	) (expoManifestFetchResult, *DiagnosticResult, error) {
		manifestPlatforms <- targetPlatform
		return expoManifestFetchResult{Manifest: map[string]any{}, Platform: targetPlatform}, &DiagnosticResult{AllPassed: true}, nil
	}
	waitForExpoBundlePrewarmFromManifest = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		fetched expoManifestFetchResult,
	) (*DiagnosticResult, error) {
		prewarmPlatforms <- fetched.Platform
		return &DiagnosticResult{AllPassed: true, Checks: []DiagnosticCheck{{Name: "Bundle prewarm", Passed: true, Detail: "OK"}}}, nil
	}
	t.Cleanup(func() {
		waitForExpoMetroTransport = previousTransport
		waitForExpoManifestFetchResult = previousManifest
		waitForExpoBundlePrewarmFromManifest = previousPrewarm
	})

	m := newTestManagerWithFakeTunnel()
	m.SetTargetPlatform("android")
	if _, err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { m.Stop() })

	select {
	case platform := <-manifestPlatforms:
		if platform != "android" {
			t.Fatalf("manifest target platform = %q, want android", platform)
		}
	case <-time.After(time.Second):
		t.Fatal("expected Start to call Expo manifest readiness")
	}
	select {
	case platform := <-prewarmPlatforms:
		if platform != "android" {
			t.Fatalf("prewarm target platform = %q, want android", platform)
		}
	case <-time.After(time.Second):
		t.Fatal("expected Start to call Expo bundle prewarm")
	}
}

func TestManagerStartForceHotReloadSkipsManifestReadinessAndBundlePrewarm(t *testing.T) {
	withFakeExpoDevServerFactory(t)
	previousTransport := waitForExpoMetroTransport
	previousManifest := waitForExpoManifestFetchResult
	previousPrewarm := waitForExpoBundlePrewarmFromManifest
	transportCalled := make(chan struct{}, 1)
	manifestCalled := make(chan struct{}, 1)
	prewarmCalled := make(chan struct{}, 1)
	waitForExpoMetroTransport = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
	) (*DiagnosticResult, error) {
		transportCalled <- struct{}{}
		return &DiagnosticResult{AllPassed: true}, nil
	}
	waitForExpoManifestFetchResult = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
	) (expoManifestFetchResult, *DiagnosticResult, error) {
		manifestCalled <- struct{}{}
		return expoManifestFetchResult{}, &DiagnosticResult{AllPassed: false}, errors.New("manifest should be skipped")
	}
	waitForExpoBundlePrewarmFromManifest = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		fetched expoManifestFetchResult,
	) (*DiagnosticResult, error) {
		prewarmCalled <- struct{}{}
		return &DiagnosticResult{AllPassed: false}, errors.New("bundle prewarm should be skipped")
	}
	t.Cleanup(func() {
		waitForExpoMetroTransport = previousTransport
		waitForExpoManifestFetchResult = previousManifest
		waitForExpoBundlePrewarmFromManifest = previousPrewarm
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
	select {
	case <-transportCalled:
	case <-time.After(time.Second):
		t.Fatal("expected force mode to wait for transport readiness")
	}
	select {
	case <-manifestCalled:
		t.Fatal("force mode should skip manifest readiness")
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case <-prewarmCalled:
		t.Fatal("force mode should skip bundle prewarm")
	case <-time.After(50 * time.Millisecond):
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "Expo relay transport verified; skipped manifest and bundle proof because --force-hot-reload is set.") {
		t.Fatalf("logs = %q, want force warning", joined)
	}
	if strings.Contains(joined, "Launching anyway") {
		t.Fatalf("logs = %q, should not include long force detail in normal mode", joined)
	}
	if strings.Contains(joined, "Manifest URLs") {
		t.Fatalf("logs = %q, should not include manifest failure detail in force mode", joined)
	}
}

func TestManagerStartForceHotReloadDoesNotBypassTransportFailure(t *testing.T) {
	withFakeExpoDevServerFactory(t)
	previousTransport := waitForExpoMetroTransport
	previousManifest := waitForExpoManifestFetchResult
	previousPrewarm := waitForExpoBundlePrewarmFromManifest
	manifestCalled := make(chan struct{}, 1)
	prewarmCalled := make(chan struct{}, 1)
	waitForExpoMetroTransport = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
	) (*DiagnosticResult, error) {
		return &DiagnosticResult{
			AllPassed: false,
			Checks: []DiagnosticCheck{
				{Name: "Tunnel HTTP", Passed: false, Detail: "timeout"},
			},
		}, errors.New("Tunnel HTTP (timeout)")
	}
	waitForExpoManifestFetchResult = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
	) (expoManifestFetchResult, *DiagnosticResult, error) {
		manifestCalled <- struct{}{}
		return expoManifestFetchResult{Manifest: map[string]any{}, Platform: targetPlatform}, &DiagnosticResult{AllPassed: true}, nil
	}
	waitForExpoBundlePrewarmFromManifest = func(
		ctx context.Context,
		localPort int,
		tunnelURL string,
		timeout time.Duration,
		fetched expoManifestFetchResult,
	) (*DiagnosticResult, error) {
		prewarmCalled <- struct{}{}
		return &DiagnosticResult{AllPassed: true}, nil
	}
	t.Cleanup(func() {
		waitForExpoMetroTransport = previousTransport
		waitForExpoManifestFetchResult = previousManifest
		waitForExpoBundlePrewarmFromManifest = previousPrewarm
	})

	m := newTestManagerWithFakeTunnel()
	m.SetForceHotReload(true)

	if _, err := m.Start(context.Background()); err == nil {
		t.Fatal("expected transport readiness failure")
	}
	select {
	case <-manifestCalled:
		t.Fatal("manifest readiness should not run after transport failure")
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case <-prewarmCalled:
		t.Fatal("bundle prewarm should not run after transport failure")
	case <-time.After(50 * time.Millisecond):
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
