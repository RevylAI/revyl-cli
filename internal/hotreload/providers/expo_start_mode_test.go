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
	normalized, hostname := normalizeProxyURL("https://abc-def.trycloudflare.com")
	if hostname != "abc-def.trycloudflare.com" {
		t.Fatalf("hostname = %q, want %q", hostname, "abc-def.trycloudflare.com")
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
