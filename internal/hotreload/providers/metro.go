package providers

import "strings"

var metroReadyIndicators = []string{
	"Metro waiting on",
	"Logs for your project",
	"Starting Metro",
	"Metro Bundler ready",
	"Development server running",
}

func isMetroReadyIndicator(line string) bool {
	lower := strings.ToLower(line)
	for _, indicator := range metroReadyIndicators {
		if strings.Contains(lower, strings.ToLower(indicator)) {
			return true
		}
	}
	return false
}

func isMetroFatalError(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "error") && strings.Contains(lower, "fatal")
}
