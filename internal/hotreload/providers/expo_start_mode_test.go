package providers

import (
	"strings"
	"testing"

	"github.com/revyl/cli/internal/hotreload"
)

func TestExpoStartArgs_NonDebugIncludesNonInteractive(t *testing.T) {
	server := NewExpoDevServer(".", "demo", 8081, false)

	args := server.expoStartArgs()
	if !containsString(args, "--non-interactive") {
		t.Fatalf("args = %v, expected --non-interactive in non-debug mode", args)
	}
}

func TestExpoStartArgs_DebugOmitsNonInteractive(t *testing.T) {
	server := NewExpoDevServer(".", "demo", 8081, false)
	server.SetDebugMode(true)

	args := server.expoStartArgs()
	if containsString(args, "--non-interactive") {
		t.Fatalf("args = %v, did not expect --non-interactive in debug mode", args)
	}
}

func TestExpoEnvironment_NonDebugClearsCI(t *testing.T) {
	server := NewExpoDevServer(".", "demo", 8081, false)

	env := server.expoEnvironment()
	if containsEnvKey(env, "CI") {
		t.Fatalf("env includes CI in non-debug mode; CI should never be set for hot reload")
	}
}

func TestExpoEnvironment_DebugClearsCI(t *testing.T) {
	server := NewExpoDevServer(".", "demo", 8081, false)
	server.SetDebugMode(true)

	env := server.expoEnvironment()
	if containsEnvKey(env, "CI") {
		t.Fatalf("env includes CI in debug mode")
	}
}

func TestNormalizeProxyURL_AddsHTTPSPort(t *testing.T) {
	normalized, hostname := normalizeProxyURL("https://hr-abc.revyl.ai")
	if hostname != "hr-abc.revyl.ai" {
		t.Fatalf("hostname = %q, want %q", hostname, "hr-abc.revyl.ai")
	}
	if !strings.Contains(normalized, ":443") {
		t.Fatalf("normalized = %q, want explicit :443", normalized)
	}
}

func TestNormalizeProxyURL_AddsHTTPPort(t *testing.T) {
	normalized, hostname := normalizeProxyURL("http://localhost")
	if hostname != "localhost" {
		t.Fatalf("hostname = %q, want %q", hostname, "localhost")
	}
	if !strings.Contains(normalized, ":80") {
		t.Fatalf("normalized = %q, want explicit :80", normalized)
	}
}

func TestNormalizeProxyURL_PreservesExplicitPort(t *testing.T) {
	normalized, hostname := normalizeProxyURL("https://example.com:8443")
	if hostname != "example.com" {
		t.Fatalf("hostname = %q, want %q", hostname, "example.com")
	}
	if !strings.Contains(normalized, ":8443") {
		t.Fatalf("normalized = %q, want :8443 preserved", normalized)
	}
	if strings.Contains(normalized, ":443") {
		t.Fatalf("normalized = %q, should not inject :443 when port is explicit", normalized)
	}
}

func TestNormalizeProxyURL_InvalidURL(t *testing.T) {
	normalized, hostname := normalizeProxyURL("://bad")
	if normalized != "://bad" {
		t.Fatalf("normalized = %q, want original returned for invalid URL", normalized)
	}
	if hostname != "" {
		t.Fatalf("hostname = %q, want empty for invalid URL", hostname)
	}
}

func TestStreamStdout_ContinuesAfterReady(t *testing.T) {
	server := NewExpoDevServer(".", "demo", 8081, false)

	readyChan := make(chan bool, 1)
	signalReady := newReadyNotifier(readyChan)

	var lines []string
	server.streamStdout(
		strings.NewReader("Starting Metro Bundler\nError: boom after ready\n"),
		func(output hotreload.DevServerOutput) {
			lines = append(lines, output.Line)
		},
		signalReady,
	)

	if len(lines) != 2 {
		t.Fatalf("lines = %v, want 2 lines", lines)
	}
	if lines[1] != "Error: boom after ready" {
		t.Fatalf("second line = %q, want %q", lines[1], "Error: boom after ready")
	}

	select {
	case <-readyChan:
	default:
		t.Fatal("expected readiness signal")
	}
}

func TestClassifyHMREvent_Bundling(t *testing.T) {
	got := classifyHMREvent("iOS Bundling node_modules/expo-router/entry.js")
	if got == "" {
		t.Fatal("expected non-empty HMR event for bundling line")
	}
	if !strings.Contains(got, "bundling") && !strings.Contains(got, "change") {
		t.Fatalf("event = %q, expected mention of bundling or change", got)
	}
}

func TestClassifyHMREvent_Bundled(t *testing.T) {
	got := classifyHMREvent("iOS Bundled 574ms node_modules/expo-router/entry.js (1224 modules)")
	if got == "" {
		t.Fatal("expected non-empty HMR event for bundled line")
	}
	if !strings.Contains(strings.ToLower(got), "bundle") {
		t.Fatalf("event = %q, expected mention of bundle", got)
	}
}

func TestClassifyHMREvent_Transforming(t *testing.T) {
	got := classifyHMREvent("Transforming node_modules/react-native/index.js")
	if got == "" {
		t.Fatal("expected non-empty HMR event for transforming line")
	}
}

func TestClassifyHMREvent_HMRUpdate(t *testing.T) {
	got := classifyHMREvent("HMR update sent to client")
	if got == "" {
		t.Fatal("expected non-empty HMR event for HMR update line")
	}
}

func TestClassifyHMREvent_IgnoresUnrelated(t *testing.T) {
	lines := []string{
		"Starting project at /tmp/test",
		"env: load .env",
		"Logs for your project will appear below.",
		"",
	}
	for _, line := range lines {
		if got := classifyHMREvent(line); got != "" {
			t.Fatalf("classifyHMREvent(%q) = %q, want empty", line, got)
		}
	}
}

func TestClassifyHMREvent_BundledWithError(t *testing.T) {
	got := classifyHMREvent("Bundled with error: SyntaxError")
	if got != "" {
		t.Fatalf("classifyHMREvent should return empty for bundled-with-error lines, got %q", got)
	}
}

func TestStreamStdout_EmitsHMREvents(t *testing.T) {
	server := NewExpoDevServer(".", "demo", 8081, false)

	readyChan := make(chan bool, 1)
	signalReady := newReadyNotifier(readyChan)

	var hmrLines []string
	server.streamStdout(
		strings.NewReader("Starting Metro Bundler\niOS Bundled 574ms entry.js (100 modules)\n"),
		func(output hotreload.DevServerOutput) {
			if output.Stream == hotreload.DevServerOutputHMR {
				hmrLines = append(hmrLines, output.Line)
			}
		},
		signalReady,
	)

	if len(hmrLines) != 1 {
		t.Fatalf("hmr events = %d, want 1", len(hmrLines))
	}
	if !strings.Contains(strings.ToLower(hmrLines[0]), "bundle") {
		t.Fatalf("hmr event = %q, expected mention of bundle", hmrLines[0])
	}
}

func TestNewReadyNotifier_OnlySignalsOnce(t *testing.T) {
	readyChan := make(chan bool, 1)
	signalReady := newReadyNotifier(readyChan)

	signalReady()
	signalReady()
	signalReady()

	received := 0
	for {
		select {
		case <-readyChan:
			received++
		default:
			if received != 1 {
				t.Fatalf("received %d readiness signals, want 1", received)
			}
			return
		}
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func containsEnvEntry(env []string, prefix string) bool {
	for _, kv := range env {
		if kv == prefix {
			return true
		}
		// Allow tests to tolerate process env duplicates where exact match is unavailable.
		if strings.HasSuffix(prefix, "=") && strings.HasPrefix(kv, prefix) {
			return true
		}
	}
	return false
}

func containsEnvKey(env []string, key string) bool {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return true
		}
	}
	return false
}
