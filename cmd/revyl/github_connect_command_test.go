package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
)

const testGithubInstallURL = "https://github.com/apps/revyl/installations/new?state=test-state"

type githubConnectServer struct {
	mu            sync.Mutex
	repoCalls     int
	installCalls  int
	connectedFrom int
}

func (s *githubConnectServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/integrations/github/repositories":
			s.repoCalls++
			repos := notConnectedRepos()
			if s.connectedFrom > 0 && s.repoCalls >= s.connectedFrom {
				repos = connectedRepos()
			}
			_ = json.NewEncoder(w).Encode(repos)
		case "/api/v1/integrations/github/install-url":
			s.installCalls++
			_ = json.NewEncoder(w).Encode(api.GithubInstallURLResponse{
				StateToken: "test-state",
				InstallURL: testGithubInstallURL,
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newGithubConnectTestCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "connect"}
	cmd.SetContext(context.Background())
	cmd.PersistentFlags().Bool("json", false, "")
	cmd.Flags().Bool("dev", false, "")
	cmd.Flags().Bool("no-open", false, "")
	cmd.Flags().Bool("no-wait", false, "")
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func withGithubInstallPageOpener(t *testing.T, open func(string) error) *[]string {
	t.Helper()
	var opened []string
	previous := openGithubInstallPage
	openGithubInstallPage = func(url string) error {
		opened = append(opened, url)
		return open(url)
	}
	t.Cleanup(func() { openGithubInstallPage = previous })
	return &opened
}

func runGithubConnectForTest(t *testing.T, server *httptest.Server, args ...string) (stdout, stderr string, runErr error, terminal map[string]interface{}) {
	t.Helper()
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	cmd := newGithubConnectTestCommand(t, args...)
	var captured analytics.TelemetryPayload
	recorder := analytics.NewWithFlusher(analytics.Config{}, func(payload analytics.TelemetryPayload) {
		captured.Events = append(captured.Events, payload.Events...)
	})
	run := recorder.StartCommand(cmd, nil)
	cmd.SetContext(analytics.ContextWithCommandRun(context.Background(), run))
	stdout, stderr = captureStdoutAndStderrSeparate(t, func() { runErr = runGithubConnect(cmd, nil) })
	run.Complete(runErr)
	recorder.Flush()
	if len(captured.Events) != 2 {
		t.Fatalf("captured %d lifecycle events, want start and terminal", len(captured.Events))
	}
	return stdout, stderr, runErr, captured.Events[1].Properties
}

func decodeGithubConnectReport(t *testing.T, stdout string) githubConnectReport {
	t.Helper()
	var report githubConnectReport
	decoder := json.NewDecoder(strings.NewReader(stdout))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		t.Fatalf("stdout is not one connect report: %v\n%s", err, stdout)
	}
	if rest, _ := io.ReadAll(decoder.Buffered()); strings.TrimSpace(string(rest)) != "" || decoder.More() {
		t.Fatalf("stdout holds more than one JSON object: %q", stdout)
	}
	return report
}

func TestGithubConnectNoOpenNoWaitIssuesLinkWithoutBrowserOrPolling(t *testing.T) {
	opened := withGithubInstallPageOpener(t, func(string) error { return nil })
	backend := &githubConnectServer{}
	server := backend.start(t)

	stdout, stderr, err, terminal := runGithubConnectForTest(t, server, "--no-open", "--no-wait", "--json")
	if err != nil {
		t.Fatalf("connect error = %v", err)
	}
	report := decodeGithubConnectReport(t, stdout)
	if report != (githubConnectReport{Status: "link_issued", InstallURL: testGithubInstallURL}) {
		t.Fatalf("report = %+v, want link_issued with the install URL", report)
	}
	if len(*opened) != 0 {
		t.Fatalf("browser opened %v, want no browser with --no-open", *opened)
	}
	if backend.repoCalls != 1 || backend.installCalls != 1 {
		t.Fatalf("calls = %d status, %d install-url; want one of each and no polling", backend.repoCalls, backend.installCalls)
	}
	if !strings.Contains(stderr, testGithubInstallURL) {
		t.Fatalf("stderr = %q, want the install link for a human reader", stderr)
	}
	if terminal["domain"] != "github_connect" || terminal["domain_status"] != "link_issued" || terminal["exit_code"] != 0 {
		t.Fatalf("terminal analytics = %+v, want a link_issued completion", terminal)
	}
	if _, ok := terminal["install_url"]; ok || strings.Contains(jsonString(t, terminal), "github.com/apps") {
		t.Fatalf("terminal analytics carries the install URL: %+v", terminal)
	}
}

func TestGithubConnectReportsAlreadyConnectedWithoutIssuingLink(t *testing.T) {
	withGithubInstallPageOpener(t, func(string) error { return nil })
	backend := &githubConnectServer{connectedFrom: 1}
	server := backend.start(t)

	stdout, _, err, terminal := runGithubConnectForTest(t, server, "--no-open", "--no-wait", "--json")
	if err != nil {
		t.Fatalf("connect error = %v", err)
	}
	report := decodeGithubConnectReport(t, stdout)
	if report != (githubConnectReport{Status: "already_connected", Connected: true, RepositoryCount: 1}) {
		t.Fatalf("report = %+v, want already_connected", report)
	}
	if backend.installCalls != 0 {
		t.Fatalf("install-url calls = %d, want none when already connected", backend.installCalls)
	}
	if terminal["domain_status"] != "already_connected" {
		t.Fatalf("terminal analytics = %+v, want already_connected", terminal)
	}
}

func TestGithubConnectWaitsUntilInstalledAfterLaunchingBrowser(t *testing.T) {
	withFastGithubConnectPolling(t, time.Millisecond, 2*time.Second)
	opened := withGithubInstallPageOpener(t, func(string) error { return nil })
	backend := &githubConnectServer{connectedFrom: 3}
	server := backend.start(t)

	stdout, _, err, terminal := runGithubConnectForTest(t, server, "--json")
	if err != nil {
		t.Fatalf("connect error = %v", err)
	}
	report := decodeGithubConnectReport(t, stdout)
	if report.Status != "connected" || !report.Connected || report.InstallURL != testGithubInstallURL {
		t.Fatalf("report = %+v, want connected with the issued link", report)
	}
	if len(*opened) != 1 || (*opened)[0] != testGithubInstallURL {
		t.Fatalf("browser opened %v, want the install URL once", *opened)
	}
	if terminal["domain_status"] != "connected" {
		t.Fatalf("terminal analytics = %+v, want connected", terminal)
	}
}

func TestGithubConnectTimeoutIsReportedAsTimedOut(t *testing.T) {
	withFastGithubConnectPolling(t, time.Millisecond, 20*time.Millisecond)
	withGithubInstallPageOpener(t, func(string) error { return errors.New("no browser") })
	backend := &githubConnectServer{}
	server := backend.start(t)

	stdout, stderr, err, terminal := runGithubConnectForTest(t, server, "--json")
	if !errors.Is(err, errGithubInstallTimedOut) {
		t.Fatalf("connect error = %v, want the install timeout", err)
	}
	report := decodeGithubConnectReport(t, stdout)
	if report != (githubConnectReport{Status: "timed_out", InstallURL: testGithubInstallURL}) {
		t.Fatalf("report = %+v, want timed_out with the install URL", report)
	}
	if !strings.Contains(stderr, "Could not open a browser automatically.") {
		t.Fatalf("stderr = %q, want the browser fallback notice", stderr)
	}
	if terminal["domain"] != "github_connect" || terminal["domain_status"] != "timed_out" || terminal["exit_code"] != 1 {
		t.Fatalf("terminal analytics = %+v, want a timed_out failure", terminal)
	}
}

func TestGithubConnectHumanNoWaitPointsToStatus(t *testing.T) {
	withGithubInstallPageOpener(t, func(string) error { return nil })
	backend := &githubConnectServer{}
	server := backend.start(t)

	stdout, stderr, err, _ := runGithubConnectForTest(t, server, "--no-open", "--no-wait")
	if err != nil {
		t.Fatalf("connect error = %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want human output on stderr only", stdout)
	}
	if !strings.Contains(stderr, testGithubInstallURL) || !strings.Contains(stderr, "revyl github status") {
		t.Fatalf("stderr = %q, want the link and the status follow-up", stderr)
	}
}

func jsonString(t *testing.T, value interface{}) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestGithubConnectRecordsFailureBeforeALinkIsIssued(t *testing.T) {
	withGithubInstallPageOpener(t, func(string) error { return nil })
	server := githubReposErrorServer(t, http.StatusUnauthorized)

	stdout, _, err, terminal := runGithubConnectForTest(t, server, "--no-open", "--no-wait", "--json")
	if err == nil || !strings.Contains(err.Error(), "revyl auth login") {
		t.Fatalf("connect error = %v, want the authentication recovery", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want no connect report when no link was issued", stdout)
	}
	if terminal["domain"] != "github_connect" || terminal["domain_status"] != "failed" || terminal["exit_code"] != 1 {
		t.Fatalf("terminal analytics = %+v, want a failed github_connect outcome", terminal)
	}
}

func TestGithubConnectNoWaitOpensBrowserAndStillReportsInstallURL(t *testing.T) {
	opened := withGithubInstallPageOpener(t, func(string) error { return nil })
	backend := &githubConnectServer{}
	server := backend.start(t)

	stdout, stderr, err, terminal := runGithubConnectForTest(t, server, "--no-wait", "--json")
	if err != nil {
		t.Fatalf("connect error = %v", err)
	}
	report := decodeGithubConnectReport(t, stdout)
	if report != (githubConnectReport{Status: "link_issued", InstallURL: testGithubInstallURL}) {
		t.Fatalf("report = %+v, want link_issued with the install URL after opening the browser", report)
	}
	if len(*opened) != 1 || (*opened)[0] != testGithubInstallURL {
		t.Fatalf("browser opened %v, want the install URL once", *opened)
	}
	for _, line := range []string{"Opened the GitHub App install page in your browser.", "If the page didn't open, visit: " + testGithubInstallURL} {
		if !strings.Contains(stderr, line) {
			t.Fatalf("stderr = %q, want %q", stderr, line)
		}
	}
	if strings.Contains(stdout, "Opened") || strings.Contains(stdout, "If the page") {
		t.Fatalf("stdout carries human output: %q", stdout)
	}
	if terminal["domain_status"] != "link_issued" || terminal["exit_code"] != 0 {
		t.Fatalf("terminal analytics = %+v, want a link_issued completion", terminal)
	}
}

func TestGithubConnectFailedBrowserOpenKeepsLinkIssuedAndExitZero(t *testing.T) {
	opened := withGithubInstallPageOpener(t, func(string) error { return errors.New("no display") })
	backend := &githubConnectServer{}
	server := backend.start(t)

	stdout, stderr, err, terminal := runGithubConnectForTest(t, server, "--no-wait", "--json")
	if err != nil {
		t.Fatalf("connect error = %v, want a failed browser open to be non-fatal", err)
	}
	report := decodeGithubConnectReport(t, stdout)
	if report != (githubConnectReport{Status: "link_issued", InstallURL: testGithubInstallURL}) {
		t.Fatalf("report = %+v, want link_issued with the install URL", report)
	}
	if len(*opened) != 1 {
		t.Fatalf("browser open attempts = %d, want 1", len(*opened))
	}
	for _, line := range []string{"Could not open a browser automatically.", testGithubInstallURL} {
		if !strings.Contains(stderr, line) {
			t.Fatalf("stderr = %q, want %q", stderr, line)
		}
	}
	if strings.Contains(stdout, "Could not open") {
		t.Fatalf("stdout carries the browser warning: %q", stdout)
	}
	if terminal["domain_status"] != "link_issued" || terminal["exit_code"] != 0 {
		t.Fatalf("terminal analytics = %+v, want link_issued with exit 0", terminal)
	}
}
