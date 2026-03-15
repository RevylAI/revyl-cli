package providers

import "strings"

// Metro-shared utilities used by both ExpoDevServer and BareRNDevServer.
// Metro is the JavaScript bundler underlying all React Native variants.

var metroReadyIndicators = []string{
	"Metro waiting on",
	"Logs for your project",
	"Starting Metro",
	"Metro Bundler ready",
	"Development server running",
}

// isMetroReadyIndicator returns true if the log line signals that the Metro
// bundler is ready to accept connections. The same indicators apply whether
// Metro is launched via Expo CLI or React Native CLI.
func isMetroReadyIndicator(line string) bool {
	lower := strings.ToLower(line)
	for _, indicator := range metroReadyIndicators {
		if strings.Contains(lower, strings.ToLower(indicator)) {
			return true
		}
	}
	return false
}

// isMetroFatalError returns true if the log line indicates a fatal Metro error
// that should abort startup.
func isMetroFatalError(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "error") && strings.Contains(lower, "fatal")
}
