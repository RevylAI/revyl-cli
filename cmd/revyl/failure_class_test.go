package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/testutil"
)

func TestKnownExitPathsReportFailureClass(t *testing.T) {
	transportErr := &url.Error{Op: "Get", URL: "https://backend.example", Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}
	_, malformedHeaderErr := parseHeaderFlags([]string{"NoColonHere"})
	for _, tc := range []struct {
		name string
		err  error
		want analytics.FailureClass
	}{
		{name: "github status unauthorized", err: actionableGithubStatusError(&api.APIError{StatusCode: http.StatusUnauthorized}, "revyl github status"), want: analytics.FailureClassAuth},
		{name: "github status forbidden", err: actionableGithubStatusError(&api.APIError{StatusCode: http.StatusForbidden}, "revyl github status"), want: analytics.FailureClassAuth},
		{name: "github status rejected", err: actionableGithubStatusError(&api.APIError{StatusCode: http.StatusNotFound}, "revyl github connect"), want: analytics.FailureClassGitHub},
		{name: "github status server error", err: actionableGithubStatusError(&api.APIError{StatusCode: http.StatusBadGateway}, "revyl github connect"), want: analytics.FailureClassServer},
		{name: "github status unreachable", err: actionableGithubStatusError(transportErr, "revyl github connect"), want: analytics.FailureClassNetwork},
		{name: "upload source flags", err: validateUploadSourceFlags("app.apk", "https://artifacts.example/app.apk", nil), want: analytics.FailureClassUsage},
		{name: "upload header flag", err: malformedHeaderErr, want: analytics.FailureClassUsage},
		{name: "upload without artifact", err: deprecatedBuildUploadLocalBuildError(), want: analytics.FailureClassUsage},
		{name: "local config", err: actionableLocalConfigError(&config.ConfigError{Stage: "read", Code: "invalid_utf8"}), want: analytics.FailureClassConfig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reportedFailureClass(t, tc.err); got != string(tc.want) {
				t.Fatalf("failure_class = %v, want %q", got, tc.want)
			}
		})
	}
}

func TestGithubInstallationTimeoutReportsGithubFailure(t *testing.T) {
	withFastGithubConnectPolling(t, time.Millisecond, 25*time.Millisecond)
	server := githubReposServer(t, notConnectedRepos)
	defer server.Close()

	_, err := waitForGithubInstallation(context.Background(), api.NewClientWithBaseURL("test-key", server.URL))

	if got := reportedFailureClass(t, err); got != string(analytics.FailureClassGitHub) {
		t.Fatalf("failure_class = %v, want %q", got, analytics.FailureClassGitHub)
	}
}

func reportedFailureClass(t *testing.T, err error) interface{} {
	t.Helper()
	if err == nil {
		t.Fatal("expected a command error")
	}
	testutil.SetHomeDir(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "")
	var captured analytics.TelemetryPayload
	recorder := analytics.NewWithFlusher(analytics.Config{}, func(payload analytics.TelemetryPayload) {
		captured = payload
	})
	run := recorder.StartCommand(&cobra.Command{Use: "example"}, nil)
	run.Complete(err)
	run.Flush()
	terminal := captured.Events[len(captured.Events)-1]
	if terminal.Event != analytics.CliCommandFailedEvent {
		t.Fatalf("terminal event = %q, want %q", terminal.Event, analytics.CliCommandFailedEvent)
	}
	return terminal.Properties["failure_class"]
}
