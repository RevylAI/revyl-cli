package providers

import "testing"

func TestIsMetroReadyIndicator(t *testing.T) {
	positives := []string{
		"Metro waiting on exp://192.168.1.1:8081",
		"Logs for your project will appear below.",
		"Starting Metro Bundler",
		"Metro Bundler ready.",
		"Development server running on port 8081",
		"metro waiting on http://localhost:8081",
	}
	for _, line := range positives {
		if !isMetroReadyIndicator(line) {
			t.Errorf("expected ready indicator for %q", line)
		}
	}

	negatives := []string{
		"Loading dependency graph...",
		"Compiling module /src/App.tsx",
		"warn: some warning message",
		"",
	}
	for _, line := range negatives {
		if isMetroReadyIndicator(line) {
			t.Errorf("unexpected ready indicator for %q", line)
		}
	}
}

func TestIsMetroFatalError(t *testing.T) {
	if !isMetroFatalError("Error: Fatal error occurred") {
		t.Error("expected fatal error for 'Error: Fatal error occurred'")
	}
	if !isMetroFatalError("FATAL ERROR: something broke") {
		t.Error("expected fatal error for 'FATAL ERROR: something broke'")
	}
	if isMetroFatalError("Error: module not found") {
		t.Error("non-fatal error incorrectly classified as fatal")
	}
	if isMetroFatalError("warn: something happened") {
		t.Error("warning incorrectly classified as fatal error")
	}
}
