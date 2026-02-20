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

func TestExpoEnvironment_NonDebugSetsCI(t *testing.T) {
	server := NewExpoDevServer(".", "demo", 8081, false)

	env := server.expoEnvironment()
	if !containsEnvEntry(env, "CI=1") {
		t.Fatalf("env does not include CI=1 in non-debug mode")
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
