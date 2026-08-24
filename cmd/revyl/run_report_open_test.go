package main

import (
	"errors"
	"strconv"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/ui"
)

func TestResolveRunOpen(t *testing.T) {
	tests := []struct {
		name          string
		openChanged   bool
		open          bool
		noOpen        bool
		noWait        bool
		outputJSON    bool
		githubActions bool
		browserOK     bool
		want          bool
	}{
		{name: "human blocking run opens by default", browserOK: true, want: true},
		{name: "explicit open is supported", openChanged: true, open: true, browserOK: true, want: true},
		{name: "explicit open false suppresses", openChanged: true, browserOK: true},
		{name: "no-open suppresses default", noOpen: true, browserOK: true},
		{name: "no-open wins over open", openChanged: true, open: true, noOpen: true, browserOK: true},
		{name: "no-wait never opens", noWait: true, openChanged: true, open: true, browserOK: true},
		{name: "json never opens", outputJSON: true, openChanged: true, open: true, browserOK: true},
		{name: "github actions never opens", githubActions: true, openChanged: true, open: true, browserOK: true},
		{name: "headless never opens", openChanged: true, open: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			originalNoWait := runNoWait
			originalNoOpen := runNoOpen
			originalOutputJSON := runOutputJSON
			originalGitHubActions := runGitHubActions
			originalBrowserSupported := runReportBrowserSupportedFn
			t.Cleanup(func() {
				runNoWait = originalNoWait
				runNoOpen = originalNoOpen
				runOutputJSON = originalOutputJSON
				runGitHubActions = originalGitHubActions
				runReportBrowserSupportedFn = originalBrowserSupported
			})

			runNoWait = test.noWait
			runNoOpen = test.noOpen
			runOutputJSON = test.outputJSON
			runGitHubActions = test.githubActions
			runReportBrowserSupportedFn = func() bool { return test.browserOK }

			cmd := &cobra.Command{Use: "run"}
			cmd.Flags().Bool("open", false, "")
			if test.openChanged {
				if err := cmd.Flags().Set("open", strconv.FormatBool(test.open)); err != nil {
					t.Fatalf("set --open: %v", err)
				}
			}

			if got := resolveRunOpen(cmd, test.open); got != test.want {
				t.Fatalf("resolveRunOpen() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestOpenCompletedRunReport(t *testing.T) {
	originalOpenBrowser := runOpenBrowserFn
	t.Cleanup(func() {
		runOpenBrowserFn = originalOpenBrowser
		ui.SetOutputObserver(nil)
	})

	var openedURL string
	runOpenBrowserFn = func(rawURL string) error {
		openedURL = rawURL
		return errors.New("browser unavailable")
	}

	var warning string
	ui.SetOutputObserver(func(level, message string) {
		if level == "warning" {
			warning = message
		}
	})

	openCompletedRunReport(" https://app.example/report/task-1 ")
	if openedURL != "https://app.example/report/task-1" {
		t.Fatalf("opened URL = %q, want trimmed report URL", openedURL)
	}
	if warning != "Could not open browser: browser unavailable" {
		t.Fatalf("warning = %q, want nonfatal browser failure", warning)
	}

	openedURL = ""
	warning = ""
	openCompletedRunReport("  ")
	if openedURL != "" {
		t.Fatalf("empty report URL opened %q", openedURL)
	}
	if warning != "" {
		t.Fatalf("empty report URL warning = %q", warning)
	}
}
