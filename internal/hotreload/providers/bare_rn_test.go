package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/hotreload"
)

func TestBareRNDetect_ReactNativeWithoutExpo(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, `{
		"name": "my-rn-app",
		"dependencies": {
			"react": "18.2.0",
			"react-native": "0.73.0"
		}
	}`)

	provider := &BareRNProvider{}
	result, err := provider.Detect(dir)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if result == nil {
		t.Fatal("expected detection result for bare RN project, got nil")
	}
	if result.Provider != "react-native" {
		t.Fatalf("provider = %q, want %q", result.Provider, "react-native")
	}
	if result.Confidence != 0.8 {
		t.Fatalf("confidence = %v, want 0.8", result.Confidence)
	}
}

func TestBareRNDetect_SkipsExpoProjects(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, `{
		"name": "my-expo-app",
		"dependencies": {
			"react": "18.2.0",
			"react-native": "0.73.0",
			"expo": "~50.0.0"
		}
	}`)

	provider := &BareRNProvider{}
	result, err := provider.Detect(dir)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil detection for Expo project, got result")
	}
}

func TestBareRNDetect_NoReactNative(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, `{
		"name": "plain-node-app",
		"dependencies": {
			"express": "4.18.0"
		}
	}`)

	provider := &BareRNProvider{}
	result, err := provider.Detect(dir)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil detection for non-RN project, got result")
	}
}

func TestBareRNDetect_MetroConfigIndicator(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, `{
		"name": "my-rn-app",
		"dependencies": { "react-native": "0.73.0" }
	}`)
	os.WriteFile(filepath.Join(dir, "metro.config.js"), []byte("module.exports = {};"), 0644)

	provider := &BareRNProvider{}
	result, err := provider.Detect(dir)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if result == nil {
		t.Fatal("expected detection result, got nil")
	}

	hasMetroIndicator := false
	for _, ind := range result.Indicators {
		if ind == "metro.config.js" {
			hasMetroIndicator = true
		}
	}
	if !hasMetroIndicator {
		t.Fatalf("indicators = %v, expected metro.config.js", result.Indicators)
	}
}

func TestBareRNDevServer_MetroStartArgs(t *testing.T) {
	server := NewBareRNDevServer(".", 8081)

	args := server.metroStartArgs()

	if !containsString(args, "react-native") {
		t.Fatalf("args = %v, expected 'react-native'", args)
	}
	if !containsString(args, "start") {
		t.Fatalf("args = %v, expected 'start'", args)
	}
	if !containsString(args, "--port") {
		t.Fatalf("args = %v, expected '--port'", args)
	}
	if !containsString(args, "8081") {
		t.Fatalf("args = %v, expected '8081'", args)
	}
}

func TestBareRNDevServer_MetroEnvironment_StripsCI(t *testing.T) {
	server := NewBareRNDevServer(".", 8081)

	env := server.metroEnvironment()
	if containsEnvKey(env, "CI") {
		t.Fatal("env includes CI; CI should be stripped for hot reload")
	}
}

func TestBareRNDevServer_GetDeepLinkURL(t *testing.T) {
	server := NewBareRNDevServer(".", 8081)

	tunnelURL := "https://abc-def.trycloudflare.com"
	deepLink := server.GetDeepLinkURL(tunnelURL)

	if deepLink != tunnelURL {
		t.Fatalf("GetDeepLinkURL = %q, want tunnel URL %q", deepLink, tunnelURL)
	}
}

func TestBareRNDevServer_Name(t *testing.T) {
	server := NewBareRNDevServer(".", 8081)
	if server.Name() != "React Native" {
		t.Fatalf("Name = %q, want %q", server.Name(), "React Native")
	}
}

func TestBareRNDevServer_DefaultPort(t *testing.T) {
	server := NewBareRNDevServer(".", 0)
	if server.Port != 8081 {
		t.Fatalf("Port = %d, want 8081", server.Port)
	}
}

func TestBareRNDevServer_CustomPort(t *testing.T) {
	server := NewBareRNDevServer(".", 9090)
	if server.Port != 9090 {
		t.Fatalf("Port = %d, want 9090", server.Port)
	}
}

func TestBareRNDevServer_SetProxyURL(t *testing.T) {
	server := NewBareRNDevServer(".", 8081)
	server.SetProxyURL("https://abc.trycloudflare.com")

	if server.proxyURL != "https://abc.trycloudflare.com" {
		t.Fatalf("proxyURL = %q, want %q", server.proxyURL, "https://abc.trycloudflare.com")
	}
}

func TestBareRNDevServer_StreamStdout_ContinuesAfterReady(t *testing.T) {
	server := NewBareRNDevServer(".", 8081)

	readyChan := make(chan bool, 1)
	signalReady := newReadyNotifier(readyChan)

	var lines []string
	server.streamStdout(
		strings.NewReader("Starting Metro Bundler\nSome other log\n"),
		func(output hotreload.DevServerOutput) {
			lines = append(lines, output.Line)
		},
		signalReady,
	)

	if len(lines) != 2 {
		t.Fatalf("lines = %v, want 2 lines", lines)
	}

	select {
	case <-readyChan:
	default:
		t.Fatal("expected readiness signal from 'Starting Metro' indicator")
	}
}

func TestBareRNProvider_GetProjectInfo(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, `{
		"name": "test-rn-app",
		"dependencies": { "react-native": "0.73.0" }
	}`)

	provider := &BareRNProvider{}
	info, err := provider.GetProjectInfo(dir)
	if err != nil {
		t.Fatalf("GetProjectInfo error: %v", err)
	}
	if info.Name != "test-rn-app" {
		t.Fatalf("Name = %q, want %q", info.Name, "test-rn-app")
	}
	if info.ReactNative == nil {
		t.Fatal("ReactNative info is nil")
	}
	if info.Platform != "cross-platform" {
		t.Fatalf("Platform = %q, want %q", info.Platform, "cross-platform")
	}
}

func TestBareRNProvider_IsSupported(t *testing.T) {
	provider := &BareRNProvider{}
	if !provider.IsSupported() {
		t.Fatal("BareRNProvider.IsSupported() = false, want true")
	}
}

func TestBareRNDetect_DevDependencies(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, `{
		"name": "my-rn-app",
		"devDependencies": { "react-native": "0.73.0" }
	}`)

	provider := &BareRNProvider{}
	result, err := provider.Detect(dir)
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if result == nil {
		t.Fatal("expected detection when react-native is in devDependencies")
	}
}

// writePackageJSON is a test helper that writes a package.json file to a directory.
func writePackageJSON(t *testing.T, dir, content string) {
	t.Helper()
	err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(content), 0644)
	if err != nil {
		t.Fatalf("failed to write package.json: %v", err)
	}
}
