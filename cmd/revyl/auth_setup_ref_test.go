package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/testutil"
)

func TestAuthLoginRecordsOnlyAWellFormedSetupRef(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  interface{}
	}{
		{name: "well formed", value: "ffffffffffffffffffffffff", want: "ffffffffffffffffffffffff"},
		{name: "unset", value: "", want: nil},
		{name: "uppercase", value: "FFFFFFFFFFFFFFFFFFFFFFFF", want: nil},
		{name: "too short", value: "fffffffffffffffffffffff", want: nil},
		{name: "customer value", value: "https://example.com/private", want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.SetHomeDir(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "")
			t.Setenv("REVYL_SETUP_REF", tc.value)
			var captured analytics.TelemetryPayload
			recorder := analytics.NewWithFlusher(analytics.Config{}, func(payload analytics.TelemetryPayload) {
				captured.Events = append(captured.Events, payload.Events...)
			})
			cmd := &cobra.Command{Use: "login"}
			run := recorder.StartCommand(cmd, nil)
			cmd.SetContext(analytics.ContextWithCommandRun(context.Background(), run))

			recordSetupRef(cmd)
			run.Complete(nil)
			run.Flush()

			terminal := captured.Events[len(captured.Events)-1]
			if got := terminal.Properties["setup_ref"]; got != tc.want {
				t.Fatalf("setup_ref = %v, want %v", got, tc.want)
			}
			if _, onStart := captured.Events[0].Properties["setup_ref"]; onStart {
				t.Fatal("setup_ref leaked onto the started event")
			}
		})
	}
}

func TestAuthStatusCarriesTheSetupRefWithoutPersistingIt(t *testing.T) {
	const setupRef = "ffffffffffffffffffffffff"
	home := t.TempDir()
	testutil.SetHomeDir(t, home)
	t.Setenv("REVYL_API_KEY", "")
	t.Setenv("REVYL_SETUP_REF", setupRef)
	var captured analytics.TelemetryPayload
	recorder := analytics.NewWithFlusher(analytics.Config{}, func(payload analytics.TelemetryPayload) {
		captured.Events = append(captured.Events, payload.Events...)
	})
	originalContext := authStatusCmd.Context()
	t.Cleanup(func() { authStatusCmd.SetContext(originalContext) })
	run := recorder.StartCommand(authStatusCmd, nil)
	authStatusCmd.SetContext(analytics.ContextWithCommandRun(context.Background(), run))

	var runErr error
	captureStdoutAndStderrSeparate(t, func() { runErr = authStatusCmd.RunE(authStatusCmd, nil) })
	run.Complete(runErr)
	run.Flush()

	if got := captured.Events[len(captured.Events)-1].Properties["setup_ref"]; got != setupRef {
		t.Fatalf("setup_ref = %v, want %s", got, setupRef)
	}
	err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(content), setupRef) {
			t.Errorf("%s persisted the setup ref", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
