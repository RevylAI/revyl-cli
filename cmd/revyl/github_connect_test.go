package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func connectedRepos() api.GithubRepositoriesResponse {
	return api.GithubRepositoriesResponse{
		Repositories: []api.GithubOrgRepository{
			{Owner: "revyl", Repo: "app", InstallationID: 123},
		},
		Installation:             &api.GithubOrgInstallation{InstallationID: 123, Status: "active"},
		HasAccess:                true,
		GithubIntegrationEnabled: true,
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

func intPtr(v int) *int { return &v }

func TestGithubScmConfigIsAutomationEnabled(t *testing.T) {
	tests := []struct {
		name string
		cfg  *api.GithubScmConfigResponse
		want bool
	}{
		{name: "nil", cfg: nil, want: false},
		{name: "enabled and installed", cfg: &api.GithubScmConfigResponse{Enabled: true, GithubInstallationId: intPtr(1)}, want: true},
		{name: "enabled no install", cfg: &api.GithubScmConfigResponse{Enabled: true}, want: false},
		{name: "installed not enabled", cfg: &api.GithubScmConfigResponse{GithubInstallationId: intPtr(1)}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.IsAutomationEnabled(); got != tc.want {
				t.Fatalf("IsAutomationEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFindRepoConfig(t *testing.T) {
	configs := []api.GithubScmConfigResponse{
		{RepoFullName: "revyl/app", Enabled: true, GithubInstallationId: intPtr(1)},
		{RepoFullName: "revyl/web"},
	}

	got := findRepoConfig(configs, "Revyl/App") // case-insensitive
	if got == nil || !got.IsAutomationEnabled() {
		t.Fatalf("findRepoConfig() = %+v, want enabled match", got)
	}
	if got := findRepoConfig(configs, "revyl/missing"); got != nil {
		t.Fatalf("findRepoConfig() = %+v, want nil for missing repo", got)
	}
}

func TestIsGithubNotConnectedErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "forbidden", err: &api.APIError{StatusCode: http.StatusForbidden}, want: true},
		{name: "not found", err: &api.APIError{StatusCode: http.StatusNotFound}, want: true},
		{name: "server error", err: &api.APIError{StatusCode: http.StatusInternalServerError}, want: false},
		{name: "non-api error", err: context.Canceled, want: false},
		{name: "nil", err: nil, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isGithubNotConnectedErr(tc.err); got != tc.want {
				t.Fatalf("isGithubNotConnectedErr(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
