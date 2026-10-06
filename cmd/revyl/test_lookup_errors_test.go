package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/execution"
	"github.com/revyl/cli/internal/testutil"
	"github.com/revyl/cli/internal/ui"
)

func TestRunTestExecReportsNotFoundOnlyForMissingTests(t *testing.T) {
	const testID = "11111111-2222-4333-8444-555555555555"
	for _, test := range []struct {
		name             string
		status           int
		body             string
		wantErr          string
		wantErrorMessage string
	}{
		{
			name:             "missing test",
			status:           http.StatusNotFound,
			body:             `{"detail":"Test not found"}`,
			wantErr:          "test not found",
			wantErrorMessage: "test not found",
		},
		{
			name:             "unsupported CLI version",
			status:           http.StatusUpgradeRequired,
			body:             `{"code":"cli_upgrade_required","message":"This Revyl CLI version is no longer compatible. Run 'revyl upgrade' and retry."}`,
			wantErr:          "look up test: This Revyl CLI version is no longer compatible. Run 'revyl upgrade' and retry.",
			wantErrorMessage: "test lookup failed (HTTP 426)",
		},
		{
			name:             "access denied",
			status:           http.StatusForbidden,
			body:             `{"detail":"Access denied for customer-private-name"}`,
			wantErr:          "look up test: Access denied for customer-private-name",
			wantErrorMessage: "test lookup failed (HTTP 403)",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("REVYL_API_KEY", "test-key")
			testutil.SetHomeDir(t, t.TempDir())
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/tests/get_test_by_id/"+testID {
					t.Errorf("unexpected request: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)

			originalRunTestExecution := runTestExecution
			originalRunRetries := runRetries
			t.Cleanup(func() {
				runTestExecution = originalRunTestExecution
				runRetries = originalRunRetries
			})
			runTestExecution = func(context.Context, string, *config.ProjectConfig, execution.RunTestParams) (*execution.RunTestResult, error) {
				t.Fatal("a failed test lookup must not start an execution")
				return nil, nil
			}
			runRetries = 1

			cmd := newLeafCommand("run", runTestExec)
			cmd.Flags().Bool("open", false, "")
			cmd.Flags().Int("timeout", execution.DefaultRunTimeoutSeconds, "")
			var captured analytics.TelemetryPayload
			recorder := analytics.NewWithFlusher(analytics.Config{}, func(payload analytics.TelemetryPayload) { captured.Events = append(captured.Events, payload.Events...) })
			run := recorder.StartCommand(cmd, []string{testID})

			runErr := runTestExec(cmd, []string{testID})
			run.Complete(runErr)
			recorder.Flush()

			if runErr == nil || runErr.Error() != test.wantErr {
				t.Fatalf("runTestExec() error = %v, want %q", runErr, test.wantErr)
			}
			if test.status != http.StatusNotFound {
				var apiErr *api.APIError
				if !errors.As(runErr, &apiErr) || apiErr.StatusCode != test.status {
					t.Fatalf("lookup failure lost its API error: %v", runErr)
				}
			}
			if len(captured.Events) != 2 {
				t.Fatalf("events = %+v, want start and failure", captured.Events)
			}
			if got := captured.Events[1].Properties["error_message"]; got != test.wantErrorMessage {
				t.Fatalf("error_message = %q, want %q", got, test.wantErrorMessage)
			}
		})
	}
}

func TestTestHistoryReportsLegacyProjectConfigurationInsteadOfNotFound(t *testing.T) {
	root := t.TempDir()
	gitInitBuildRepository(t, root)
	writeProjectBuildConfig(t, root, "build:\n  platforms:\n    ios:\n      command: make ios\n      output: build/App.app\n")
	withWorkingDir(t, root)

	original := loadConfigAndClient
	t.Cleanup(func() { loadConfigAndClient = original })
	loadConfigAndClient = func(bool) (string, *config.ProjectConfig, *api.Client, error) {
		return "test-key", nil, api.NewClientWithBaseURL("test-key", "http://127.0.0.1:0"), nil
	}

	cmd := newLeafCommand("history", runTestHistory)
	var captured analytics.TelemetryPayload
	recorder := analytics.NewWithFlusher(analytics.Config{}, func(payload analytics.TelemetryPayload) { captured.Events = append(captured.Events, payload.Events...) })
	run := recorder.StartCommand(cmd, []string{"login-flow"})

	historyErr := runTestHistory(cmd, []string{"login-flow"})
	run.Complete(historyErr)
	recorder.Flush()

	if historyErr == nil || !strings.Contains(historyErr.Error(), "legacy format") || !strings.Contains(historyErr.Error(), "config migrate") {
		t.Fatalf("runTestHistory() error = %v, want the legacy migration recovery", historyErr)
	}
	if len(captured.Events) != 2 {
		t.Fatalf("events = %+v, want start and failure", captured.Events)
	}
	want := "project configuration could not be used (legacy_config_requires_migration)"
	if got := captured.Events[1].Properties["error_message"]; got != want {
		t.Fatalf("error_message = %q, want %q", got, want)
	}
}

func TestTestLookupFailureKeepsSearchCauseOutOfAnalytics(t *testing.T) {
	searchErr := fmt.Errorf("failed to search for test: %w", &api.APIError{StatusCode: http.StatusBadGateway, Detail: "upstream customer-private-name"})
	var captured analytics.TelemetryPayload
	recorder := analytics.NewWithFlusher(analytics.Config{}, func(payload analytics.TelemetryPayload) { captured.Events = append(captured.Events, payload.Events...) })
	cmd := newLeafCommand("status", runTestStatus)
	run := recorder.StartCommand(cmd, []string{"login-flow"})
	ui.SetOutputObserver(run.ObserveOutput)
	t.Cleanup(func() { ui.SetOutputObserver(nil) })

	lookupErr := testLookupFailure(searchErr)
	run.Complete(lookupErr)
	recorder.Flush()

	if lookupErr.Error() != searchErr.Error() {
		t.Fatalf("testLookupFailure() = %q, want the search failure %q", lookupErr, searchErr)
	}
	if len(captured.Events) != 2 {
		t.Fatalf("events = %+v, want start and failure", captured.Events)
	}
	if got := captured.Events[1].Properties["error_message"]; got != "test lookup failed (HTTP 502)" {
		t.Fatalf("error_message = %q, want the bounded HTTP status", got)
	}
	if _, ok := captured.Events[1].Properties["output_tail"]; ok {
		t.Fatal("a bounded lookup diagnostic must not attach the raw output tail")
	}
}
