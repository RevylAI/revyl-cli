package hotreload

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
	previousHead := waitForExpoManifestHeadReady
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
	waitForExpoManifestHeadReady = func(
		ctx context.Context,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
		onSlowAttempt func(DiagnosticCheck),
	) (*DiagnosticResult, error) {
		return &DiagnosticResult{AllPassed: true, Checks: []DiagnosticCheck{{Name: "Manifest HEAD readiness", Passed: true, Detail: "OK"}}}, nil
	}
	t.Cleanup(func() {
		waitForExpoMetroTransport = previousTransport
		waitForExpoManifestFetchResult = previousManifest
		waitForExpoBundlePrewarmFromManifest = previousPrewarm
		waitForExpoManifestHeadReady = previousHead
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

func TestManagerStopCancelsExternalExpoDiagnostics(t *testing.T) {
	started := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
		select {
		case canceled <- struct{}{}:
		default:
		}
	}))
	defer server.Close()

	m := NewManager("expo", &config.ProviderConfig{AppScheme: "myapp"}, ".")
	m.SetExternalTunnelURL(server.URL)
	m.SetDebugMode(true)
	if _, err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("external diagnostics did not issue a manifest request")
	}

	m.Stop()

	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("Stop() did not cancel the external diagnostics request")
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

func TestManagerStartWaitsForExpoMetroTransportManifestBundlePrewarmAndDeviceHead(t *testing.T) {
	withFakeExpoDevServerFactory(t)
	previousTransport := waitForExpoMetroTransport
	previousManifest := waitForExpoManifestFetchResult
	previousPrewarm := waitForExpoBundlePrewarmFromManifest
	previousHead := waitForExpoManifestHeadReady
	transportCalled := make(chan struct{}, 1)
	manifestCalled := make(chan struct{}, 1)
	prewarmCalled := make(chan struct{}, 1)
	headCalled := make(chan struct{}, 1)
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
	waitForExpoManifestHeadReady = func(
		ctx context.Context,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
		onSlowAttempt func(DiagnosticCheck),
	) (*DiagnosticResult, error) {
		headCalled <- struct{}{}
		return &DiagnosticResult{AllPassed: true, Checks: []DiagnosticCheck{{Name: "Manifest HEAD readiness", Passed: true, Detail: "OK"}}}, nil
	}
	t.Cleanup(func() {
		waitForExpoMetroTransport = previousTransport
		waitForExpoManifestFetchResult = previousManifest
		waitForExpoBundlePrewarmFromManifest = previousPrewarm
		waitForExpoManifestHeadReady = previousHead
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
	select {
	case <-headCalled:
	case <-time.After(time.Second):
		t.Fatal("expected Start to prove device manifest HEAD readiness")
	}
}

func TestManagerStartExpoLogsAreCompactByDefault(t *testing.T) {
	withFakeExpoDevServerFactory(t)
	previousTransport := waitForExpoMetroTransport
	previousManifest := waitForExpoManifestFetchResult
	previousPrewarm := waitForExpoBundlePrewarmFromManifest
	previousHead := waitForExpoManifestHeadReady
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
	waitForExpoManifestHeadReady = func(
		ctx context.Context,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
		onSlowAttempt func(DiagnosticCheck),
	) (*DiagnosticResult, error) {
		return &DiagnosticResult{
			AllPassed: true,
			Checks: []DiagnosticCheck{{
				Name:   "Manifest HEAD readiness",
				Passed: true,
				Detail: "OK platform=ios status=200 duration=42ms",
			}},
		}, nil
	}
	t.Cleanup(func() {
		waitForExpoMetroTransport = previousTransport
		waitForExpoManifestFetchResult = previousManifest
		waitForExpoBundlePrewarmFromManifest = previousPrewarm
		waitForExpoManifestHeadReady = previousHead
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
		"Expo relay transport verified",
		"Warming Expo manifest through relay...",
		"Warming Expo bundle through relay...",
		"Checking device manifest path through relay...",
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
	previousHead := waitForExpoManifestHeadReady
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
	waitForExpoManifestHeadReady = func(
		ctx context.Context,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
		onSlowAttempt func(DiagnosticCheck),
	) (*DiagnosticResult, error) {
		return &DiagnosticResult{
			AllPassed: true,
			Checks: []DiagnosticCheck{{
				Name:   "Manifest HEAD readiness",
				Passed: true,
				Detail: "OK platform=ios status=200 duration=42ms",
			}},
		}, nil
	}
	t.Cleanup(func() {
		waitForExpoMetroTransport = previousTransport
		waitForExpoManifestFetchResult = previousManifest
		waitForExpoBundlePrewarmFromManifest = previousPrewarm
		waitForExpoManifestHeadReady = previousHead
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
		"Checking device-safe Expo manifest path through the relay...",
		"Device-safe Expo manifest path verified: OK platform=ios status=200 duration=42ms",
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
	previousHead := waitForExpoManifestHeadReady
	manifestPlatforms := make(chan string, 1)
	prewarmPlatforms := make(chan string, 1)
	headPlatforms := make(chan string, 1)
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
	waitForExpoManifestHeadReady = func(
		ctx context.Context,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
		onSlowAttempt func(DiagnosticCheck),
	) (*DiagnosticResult, error) {
		headPlatforms <- targetPlatform
		return &DiagnosticResult{AllPassed: true, Checks: []DiagnosticCheck{{Name: "Manifest HEAD readiness", Passed: true, Detail: "OK"}}}, nil
	}
	t.Cleanup(func() {
		waitForExpoMetroTransport = previousTransport
		waitForExpoManifestFetchResult = previousManifest
		waitForExpoBundlePrewarmFromManifest = previousPrewarm
		waitForExpoManifestHeadReady = previousHead
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
	select {
	case platform := <-headPlatforms:
		if platform != "android" {
			t.Fatalf("head target platform = %q, want android", platform)
		}
	case <-time.After(time.Second):
		t.Fatal("expected Start to call Expo manifest HEAD readiness")
	}
}

func TestManagerStartExpoManifestFailureSuggestsForceHotReloadDiagnostic(t *testing.T) {
	withFakeExpoDevServerFactory(t)
	previousTransport := waitForExpoMetroTransport
	previousManifest := waitForExpoManifestFetchResult
	previousPrewarm := waitForExpoBundlePrewarmFromManifest
	previousHead := waitForExpoManifestHeadReady
	prewarmCalled := make(chan struct{}, 1)
	headCalled := make(chan struct{}, 1)
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
		return expoManifestFetchResult{}, &DiagnosticResult{AllPassed: false}, errors.New("timed out after 1m30s waiting for Expo manifest readiness")
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
	waitForExpoManifestHeadReady = func(
		ctx context.Context,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
		onSlowAttempt func(DiagnosticCheck),
	) (*DiagnosticResult, error) {
		headCalled <- struct{}{}
		return &DiagnosticResult{AllPassed: true}, nil
	}
	t.Cleanup(func() {
		waitForExpoMetroTransport = previousTransport
		waitForExpoManifestFetchResult = previousManifest
		waitForExpoBundlePrewarmFromManifest = previousPrewarm
		waitForExpoManifestHeadReady = previousHead
	})

	m := newTestManagerWithFakeTunnel()
	m.SetTargetPlatform("ios")
	_, err := m.Start(context.Background())
	if err == nil {
		t.Fatal("expected manifest readiness failure")
	}
	errText := err.Error()
	for _, expected := range []string{
		"Expo is running and the Revyl relay is reachable",
		"could not prove the first ios manifest",
		"revyl dev --platform ios --force-hot-reload",
		"If the app loads, you can keep working",
		"restart Expo/Metro",
		"revyl device report --session-id <session-id> --json",
		"timed out after 1m30s waiting for Expo manifest readiness",
	} {
		if !strings.Contains(errText, expected) {
			t.Fatalf("error missing %q\nerror:\n%s", expected, errText)
		}
	}
	select {
	case <-prewarmCalled:
		t.Fatal("bundle prewarm should not run after manifest readiness failure")
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case <-headCalled:
		t.Fatal("manifest HEAD readiness should not run after manifest readiness failure")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestManagerStartLogsWhenDeviceManifestHeadIsSlow(t *testing.T) {
	withFakeExpoDevServerFactory(t)
	previousTransport := waitForExpoMetroTransport
	previousManifest := waitForExpoManifestFetchResult
	previousPrewarm := waitForExpoBundlePrewarmFromManifest
	previousHead := waitForExpoManifestHeadReady
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
		return &DiagnosticResult{AllPassed: true, Checks: []DiagnosticCheck{{Name: "Bundle prewarm", Passed: true, Detail: "OK"}}}, nil
	}
	waitForExpoManifestHeadReady = func(
		ctx context.Context,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
		onSlowAttempt func(DiagnosticCheck),
	) (*DiagnosticResult, error) {
		onSlowAttempt(DiagnosticCheck{
			Name:   "Manifest HEAD readiness",
			Passed: false,
			Detail: "expo_manifest_head_headers platform=ios timeout=8s duration=8s error=context deadline exceeded",
		})
		return &DiagnosticResult{AllPassed: true, Checks: []DiagnosticCheck{{Name: "Manifest HEAD readiness", Passed: true, Detail: "OK"}}}, nil
	}
	t.Cleanup(func() {
		waitForExpoMetroTransport = previousTransport
		waitForExpoManifestFetchResult = previousManifest
		waitForExpoBundlePrewarmFromManifest = previousPrewarm
		waitForExpoManifestHeadReady = previousHead
	})

	var logs []string
	m := newTestManagerWithFakeTunnel()
	m.SetLogCallback(func(message string) {
		logs = append(logs, message)
	})
	if _, err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { m.Stop() })

	joined := strings.Join(logs, "\n")
	if count := strings.Count(joined, "Metro is still warming; waiting before launching device."); count != 1 {
		t.Fatalf("warming status count = %d, want 1\nlogs:\n%s", count, joined)
	}
	if !strings.Contains(joined, "Expo relay readiness verified") {
		t.Fatalf("logs missing readiness success\nlogs:\n%s", joined)
	}
}

func TestManagerStartDeviceManifestHeadFailureSuggestsForceHotReloadDiagnostic(t *testing.T) {
	withFakeExpoDevServerFactory(t)
	previousTransport := waitForExpoMetroTransport
	previousManifest := waitForExpoManifestFetchResult
	previousPrewarm := waitForExpoBundlePrewarmFromManifest
	previousHead := waitForExpoManifestHeadReady
	prewarmCalled := make(chan struct{}, 1)
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
		prewarmCalled <- struct{}{}
		return &DiagnosticResult{AllPassed: true, Checks: []DiagnosticCheck{{Name: "Bundle prewarm", Passed: true, Detail: "OK"}}}, nil
	}
	waitForExpoManifestHeadReady = func(
		ctx context.Context,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
		onSlowAttempt func(DiagnosticCheck),
	) (*DiagnosticResult, error) {
		onSlowAttempt(DiagnosticCheck{
			Name:   "Manifest HEAD readiness",
			Passed: false,
			Detail: "expo_manifest_head_headers platform=ios timeout=8s duration=8s error=context deadline exceeded",
		})
		return &DiagnosticResult{AllPassed: false}, errors.New("timed out after 1m30s waiting for Expo device manifest readiness: Manifest HEAD readiness (expo_manifest_head_headers platform=ios timeout=8s duration=8s)")
	}
	t.Cleanup(func() {
		waitForExpoMetroTransport = previousTransport
		waitForExpoManifestFetchResult = previousManifest
		waitForExpoBundlePrewarmFromManifest = previousPrewarm
		waitForExpoManifestHeadReady = previousHead
	})

	var logs []string
	m := newTestManagerWithFakeTunnel()
	m.SetTargetPlatform("ios")
	m.SetLogCallback(func(message string) {
		logs = append(logs, message)
	})
	_, err := m.Start(context.Background())
	if err == nil {
		t.Fatal("expected device manifest HEAD readiness failure")
	}
	select {
	case <-prewarmCalled:
	case <-time.After(time.Second):
		t.Fatal("expected bundle prewarm before device manifest HEAD failure")
	}
	errText := err.Error()
	for _, expected := range []string{
		"could not prove the first ios manifest",
		"revyl dev --platform ios --force-hot-reload",
		"timed out after 1m30s waiting for Expo device manifest readiness",
		"expo_manifest_head_headers",
	} {
		if !strings.Contains(errText, expected) {
			t.Fatalf("error missing %q\nerror:\n%s", expected, errText)
		}
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "Metro is still warming; waiting before launching device.") {
		t.Fatalf("logs missing warming status\nlogs:\n%s", joined)
	}
	if strings.Contains(joined, "Expo relay readiness verified") {
		t.Fatalf("logs should not claim readiness after HEAD failure\nlogs:\n%s", joined)
	}
}

func TestManagerStartForceHotReloadSkipsManifestReadinessAndBundlePrewarm(t *testing.T) {
	withFakeExpoDevServerFactory(t)
	previousTransport := waitForExpoMetroTransport
	previousManifest := waitForExpoManifestFetchResult
	previousPrewarm := waitForExpoBundlePrewarmFromManifest
	previousHead := waitForExpoManifestHeadReady
	transportCalled := make(chan struct{}, 1)
	manifestCalled := make(chan struct{}, 1)
	prewarmCalled := make(chan struct{}, 1)
	headCalled := make(chan struct{}, 1)
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
	waitForExpoManifestHeadReady = func(
		ctx context.Context,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
		onSlowAttempt func(DiagnosticCheck),
	) (*DiagnosticResult, error) {
		headCalled <- struct{}{}
		return &DiagnosticResult{AllPassed: false}, errors.New("manifest HEAD should be skipped")
	}
	t.Cleanup(func() {
		waitForExpoMetroTransport = previousTransport
		waitForExpoManifestFetchResult = previousManifest
		waitForExpoBundlePrewarmFromManifest = previousPrewarm
		waitForExpoManifestHeadReady = previousHead
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
	select {
	case <-headCalled:
		t.Fatal("force mode should skip manifest HEAD readiness")
	case <-time.After(50 * time.Millisecond):
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "Expo relay transport verified") || !strings.Contains(joined, "Skipped manifest and bundle proof because --force-hot-reload is set.") {
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
	previousHead := waitForExpoManifestHeadReady
	manifestCalled := make(chan struct{}, 1)
	prewarmCalled := make(chan struct{}, 1)
	headCalled := make(chan struct{}, 1)
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
	waitForExpoManifestHeadReady = func(
		ctx context.Context,
		tunnelURL string,
		timeout time.Duration,
		interval time.Duration,
		targetPlatform string,
		onSlowAttempt func(DiagnosticCheck),
	) (*DiagnosticResult, error) {
		headCalled <- struct{}{}
		return &DiagnosticResult{AllPassed: true}, nil
	}
	t.Cleanup(func() {
		waitForExpoMetroTransport = previousTransport
		waitForExpoManifestFetchResult = previousManifest
		waitForExpoBundlePrewarmFromManifest = previousPrewarm
		waitForExpoManifestHeadReady = previousHead
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
	select {
	case <-headCalled:
		t.Fatal("manifest HEAD readiness should not run after transport failure")
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
				{Name: "Expo devtools plugin WebSocket", Passed: false, Detail: "unexpected response: HTTP/1.1 426 Upgrade Required"},
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
	if !strings.Contains(joined, "Expo devtools plugin WebSocket") {
		t.Fatalf("logs = %q, want Expo devtools plugin diagnostic name", joined)
	}
	if strings.Contains(joined, "FAILED") {
		t.Fatalf("logs = %q, should not use hard failure wording", joined)
	}
}
