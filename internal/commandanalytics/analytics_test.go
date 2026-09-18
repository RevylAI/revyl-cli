package commandanalytics

import (
	"errors"
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

func TestComputerCommandLifecycle(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	for _, failed := range []bool{false, true} {
		var captured analytics.TelemetryPayload
		recorder := analytics.NewWithFlusher(analytics.Config{Version: "test-version"}, func(payload analytics.TelemetryPayload) {
			captured = payload
		})
		root := &cobra.Command{Use: "revyl-computer"}
		ssh := &cobra.Command{Use: "ssh"}
		root.AddCommand(ssh)
		run := recorder.StartCommand(ssh, nil)
		var err error
		terminalEvent := analytics.CliCommandCompletedEvent
		if failed {
			err = errors.New("no machine available")
			terminalEvent = analytics.CliCommandFailedEvent
		}
		completeCommandAnalytics(run, err, false)
		if len(captured.Events) != 2 {
			t.Fatalf("event count = %d, want 2", len(captured.Events))
		}
		if captured.Events[0].Event != analytics.CliCommandStartedEvent || captured.Events[1].Event != terminalEvent {
			t.Fatalf("unexpected lifecycle events: %v", captured.Events)
		}
		for _, event := range captured.Events {
			if event.Properties["command"] != "revyl-computer ssh" || event.Properties["cli_version"] != "test-version" {
				t.Fatalf("unexpected command identity: %v", event.Properties)
			}
		}
	}
}

func TestInstallPreservesCommandResult(t *testing.T) {
	t.Setenv("REVYL_TELEMETRY_DISABLED", "true")
	root := &cobra.Command{Use: "revyl-computer", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().Bool("dev", false, "")
	expected := errors.New("connection failed")
	runs := 0
	ssh := &cobra.Command{
		Use: "ssh",
		RunE: func(cmd *cobra.Command, args []string) error {
			runs++
			return expected
		},
	}
	root.AddCommand(ssh)
	Install(root, analytics.Config{})
	root.SetArgs([]string{"ssh"})
	if err := root.Execute(); !errors.Is(err, expected) || runs != 1 {
		t.Fatalf("command result = %v, runs = %d", err, runs)
	}
}
