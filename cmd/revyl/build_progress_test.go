package main

import (
	"strings"
	"testing"
	"time"

	"github.com/revyl/cli/internal/ui"
)

func TestPrintBuildRecapExplainsLocalBuildPhase(t *testing.T) {
	oldQuiet := ui.IsQuietMode()
	oldDebug := ui.IsDebugMode()
	ui.SetQuietMode(false)
	ui.SetDebugMode(false)
	t.Cleanup(func() {
		ui.SetQuietMode(oldQuiet)
		ui.SetDebugMode(oldDebug)
	})

	output := captureStdoutAndStderr(t, func() {
		printBuildRecap("native-ios-dev", []string{
			"[INSTALL_PODS] Pod installation complete!",
		}, 15*time.Minute)
	})

	for _, want := range []string{
		"Still running local build for native-ios-dev",
		"15m elapsed",
		"[INSTALL_PODS] Pod installation complete!",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}
