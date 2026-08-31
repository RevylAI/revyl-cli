package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
)

// withFastGithubConnectPolling shortens the connect poll interval/timeout for
// the duration of a test and restores them afterwards.
func withFastGithubConnectPolling(t *testing.T, interval, timeout time.Duration) {
	t.Helper()
	prevInterval := githubConnectPollInterval
	prevTimeout := githubConnectPollTimeout
	githubConnectPollInterval = interval
	githubConnectPollTimeout = timeout
	t.Cleanup(func() {
		githubConnectPollInterval = prevInterval
		githubConnectPollTimeout = prevTimeout
	})
}

// githubReposServer returns an httptest server for the repositories endpoint
// whose responses are produced by next() on each request, simulating an
// installation that may become active partway through a poll loop.
func githubReposServer(t *testing.T, next func() api.GithubRepositoriesResponse) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/integrations/github/repositories" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(next()); err != nil {
			t.Fatalf("failed to encode response: %v", err)
		}
	}))
}

func githubReposErrorServer(t *testing.T, statusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/integrations/github/repositories" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(`{"message":"request rejected"}`))
	}))
}

func connectedRepos() api.GithubRepositoriesResponse {
	return api.GithubRepositoriesResponse{
		Repositories: []api.GithubOrgRepository{
			{Owner: "revyl", Repo: "app", InstallationID: 123},
		},
		Installation: &api.GithubOrgInstallation{InstallationID: 123, Status: "active"},
		HasAccess:    true,
	}
}

func notConnectedRepos() api.GithubRepositoriesResponse {
	return api.GithubRepositoriesResponse{Repositories: []api.GithubOrgRepository{}}
}

func TestEnsureGithubConnectedShortCircuitsWhenConnected(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	server := githubReposServer(t, func() api.GithubRepositoriesResponse {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return connectedRepos()
	})
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	repos, err := ensureGithubConnected(context.Background(), client)
	if err != nil {
		t.Fatalf("ensureGithubConnected() error = %v", err)
	}
	if !repos.IsConnected() {
		t.Fatalf("ensureGithubConnected() repos.IsConnected() = false, want true")
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("ensureGithubConnected() made %d status calls, want 1 (short-circuit)", calls)
	}
}

func TestEnsureGithubConnectedExplainsExpiredAuthentication(t *testing.T) {
	server := githubReposErrorServer(t, http.StatusUnauthorized)
	defer server.Close()

	client := api.NewClientWithBaseURL("expired-key", server.URL)
	_, err := ensureGithubConnected(context.Background(), client)
	if err == nil {
		t.Fatal("ensureGithubConnected() error = nil, want authentication recovery")
	}
	if got := err.Error(); got != "Revyl authentication is no longer valid; run 'revyl auth login', then retry 'revyl github connect'" {
		t.Fatalf("ensureGithubConnected() error = %q", got)
	}
}

func TestWaitForGithubInstallationBecomesActive(t *testing.T) {
	withFastGithubConnectPolling(t, time.Millisecond, 2*time.Second)

	var mu sync.Mutex
	calls := 0
	server := githubReposServer(t, func() api.GithubRepositoriesResponse {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls >= 3 {
			return connectedRepos()
		}
		return notConnectedRepos()
	})
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	repos, err := waitForGithubInstallation(context.Background(), client)
	if err != nil {
		t.Fatalf("waitForGithubInstallation() error = %v", err)
	}
	if !repos.IsConnected() {
		t.Fatalf("waitForGithubInstallation() repos.IsConnected() = false, want true")
	}
}

func TestWaitForGithubInstallationTimesOut(t *testing.T) {
	withFastGithubConnectPolling(t, time.Millisecond, 25*time.Millisecond)

	server := githubReposServer(t, notConnectedRepos)
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	_, err := waitForGithubInstallation(context.Background(), client)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("waitForGithubInstallation() error = %v, want timeout", err)
	}
}

func TestWaitForGithubInstallationStopsImmediatelyWhenAuthenticationExpires(t *testing.T) {
	withFastGithubConnectPolling(t, time.Millisecond, time.Second)

	server := githubReposErrorServer(t, http.StatusUnauthorized)
	defer server.Close()

	client := api.NewClientWithBaseURL("expired-key", server.URL)
	_, err := waitForGithubInstallation(context.Background(), client)
	if err == nil {
		t.Fatal("waitForGithubInstallation() error = nil, want authentication recovery")
	}
	if got := err.Error(); got != "Revyl authentication is no longer valid; run 'revyl auth login', then retry 'revyl github connect'" {
		t.Fatalf("waitForGithubInstallation() error = %q", got)
	}
}

func TestTerminalGithubInstallationPollingErrorKeepsTransientFailuresPolling(t *testing.T) {
	tests := []error{
		context.DeadlineExceeded,
		&api.APIError{StatusCode: http.StatusInternalServerError},
		&api.APIError{StatusCode: http.StatusTooManyRequests},
		&api.APIError{StatusCode: http.StatusRequestTimeout},
	}
	for _, err := range tests {
		if got := terminalGithubInstallationPollingError(err); got != nil {
			t.Errorf("terminalGithubInstallationPollingError(%v) = %v, want nil", err, got)
		}
	}
}

func TestTerminalGithubInstallationPollingErrorExplainsForbiddenRecovery(t *testing.T) {
	err := terminalGithubInstallationPollingError(&api.APIError{StatusCode: http.StatusForbidden})
	if err == nil {
		t.Fatal("terminalGithubInstallationPollingError() = nil, want recovery")
	}
	for _, want := range []string{"revyl auth status", "revyl github connect"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("terminalGithubInstallationPollingError() = %q, want %q", err, want)
		}
	}
}

func TestActionableGithubStatusErrorPreservesStatusRetry(t *testing.T) {
	err := actionableGithubStatusError(&api.APIError{StatusCode: http.StatusUnauthorized}, "revyl github status")
	if got := err.Error(); got != "Revyl authentication is no longer valid; run 'revyl auth login', then retry 'revyl github status'" {
		t.Fatalf("actionableGithubStatusError() = %q", got)
	}
}

func TestGithubRepositoryAvailableMatchesExactSlugCaseInsensitively(t *testing.T) {
	repos := connectedRepos()
	if !githubRepositoryAvailable(&repos, "REVYL", "APP") {
		t.Fatal("githubRepositoryAvailable() = false, want true")
	}
	if githubRepositoryAvailable(&repos, "revyl", "missing") {
		t.Fatal("githubRepositoryAvailable() = true for an ungranted repository")
	}
}

func TestGithubCommandSurfaceContainsOnlyConnectStatusAndSetup(t *testing.T) {
	names := make([]string, 0, len(githubCmd.Commands()))
	for _, command := range githubCmd.Commands() {
		names = append(names, command.Name())
	}
	want := []string{"connect", "status", "setup"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("github commands = %v, want %v", names, want)
	}
}

func TestGithubCommandRejectsRemovedSubcommands(t *testing.T) {
	for _, removed := range []string{"init", "push"} {
		t.Run(removed, func(t *testing.T) {
			err := githubCmd.Args(githubCmd, []string{removed})
			if err == nil || !strings.Contains(err.Error(), `unknown command "`+removed+`"`) {
				t.Fatalf("github %s error = %v, want unknown command", removed, err)
			}
		})
	}
}
