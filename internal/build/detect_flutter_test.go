package build

import (
	"strings"
	"testing"
)

// TestDetectFlutterProducesDebugBuilds guards that the default Flutter build
// commands produce debug artifacts. Debug builds enable the Dart VM Service
// (required for `flutter attach` hot reload on cloud devices) and keep the
// build debuggable for the State tab.
func TestDetectFlutterProducesDebugBuilds(t *testing.T) {
	t.Parallel()

	detected, err := detectFlutter(t.TempDir())
	if err != nil {
		t.Fatalf("detectFlutter error = %v", err)
	}

	ios := detected.Platforms["ios"].Command
	if !strings.Contains(ios, "--simulator") || !strings.Contains(ios, "--debug") {
		t.Fatalf("ios command = %q, want a --simulator --debug build (VM Service for hot reload)", ios)
	}

	android := detected.Platforms["android"].Command
	if !strings.Contains(android, "--debug") {
		t.Fatalf("android command = %q, want a --debug build", android)
	}
}
