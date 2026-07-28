package main

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/testutil"
)

func TestCompleteCommandAnalyticsMarksPanicAsFailure(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())

	var captured analytics.TelemetryPayload
	recorder := analytics.NewWithFlusher(analytics.Config{}, func(payload analytics.TelemetryPayload) {
		captured = payload
	})
	run := recorder.StartCommand(&cobra.Command{Use: "example"}, nil)

	completeCommandAnalytics(run, nil, true)

	if len(captured.Events) != 2 {
		t.Fatalf("event count = %d, want 2", len(captured.Events))
	}
	terminal := captured.Events[1]
	if terminal.Event != analytics.CliCommandFailedEvent {
		t.Fatalf("terminal event = %q, want %q", terminal.Event, analytics.CliCommandFailedEvent)
	}
	if exitCode, ok := terminal.Properties["exit_code"].(int); !ok || exitCode != 1 {
		t.Fatalf("exit_code = %#v, want 1", terminal.Properties["exit_code"])
	}
}
