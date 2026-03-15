package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDetectInstallMethodFromPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		execPath string
		expected string
	}{
		{
			name:     "homebrew cellar path",
			execPath: "/opt/homebrew/Cellar/revyl/0.1.0/bin/revyl",
			expected: "homebrew",
		},
		{
			name:     "npm global path",
			execPath: "/usr/local/lib/node_modules/@revyl/cli/bin/revyl",
			expected: "npm",
		},
		{
			name:     "pip site-packages path",
			execPath: "/opt/venv/lib/python3.12/site-packages/revyl/bin/revyl",
			expected: "pip",
		},
		{
			name:     "pip dist-packages path",
			execPath: "/usr/lib/python3/dist-packages/revyl/bin/revyl",
			expected: "pip",
		},
		{
			name:     "downloaded binary in revyl home",
			execPath: "/Users/alice/.revyl/bin/revyl-darwin-arm64",
			expected: "direct",
		},
		{
			name:     "default direct path",
			execPath: "/usr/local/bin/revyl",
			expected: "direct",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			actual := detectInstallMethodFromPath(tc.execPath)
			if actual != tc.expected {
				t.Fatalf("detectInstallMethodFromPath(%q) = %q, want %q", tc.execPath, actual, tc.expected)
			}
		})
	}
}

func TestFetchLatestReleaseAddsAuthorizationHeader(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "github-token")
	t.Setenv("GH_TOKEN", "gh-token")

	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
	}))
	defer server.Close()

	configureGitHubTestRequest(t, server.URL)

	release, err := fetchLatestRelease(context.Background(), false)
	if err != nil {
		t.Fatalf("fetchLatestRelease returned error: %v", err)
	}

	if release.TagName != "v1.2.3" {
		t.Fatalf("fetchLatestRelease tag = %q, want %q", release.TagName, "v1.2.3")
	}

	if authorization != "Bearer github-token" {
		t.Fatalf("Authorization header = %q, want %q", authorization, "Bearer github-token")
	}
}

func TestFetchLatestReleaseOmitsAuthorizationHeaderWithoutToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
	}))
	defer server.Close()

	configureGitHubTestRequest(t, server.URL)

	if _, err := fetchLatestRelease(context.Background(), false); err != nil {
		t.Fatalf("fetchLatestRelease returned error: %v", err)
	}

	if authorization != "" {
		t.Fatalf("Authorization header = %q, want empty", authorization)
	}
}

func TestFetchLatestReleaseRetriesTransientFailures(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	tests := []struct {
		name       string
		statusCode int
	}{
		{
			name:       "rate limited",
			statusCode: http.StatusTooManyRequests,
		},
		{
			name:       "server error",
			statusCode: http.StatusBadGateway,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				w.Header().Set("Content-Type", "application/json")
				if attempts == 1 {
					w.WriteHeader(tc.statusCode)
					_, _ = w.Write([]byte(`{"message":"temporary failure"}`))
					return
				}
				_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
			}))
			defer server.Close()

			configureGitHubTestRequest(t, server.URL)

			release, err := fetchLatestRelease(context.Background(), false)
			if err != nil {
				t.Fatalf("fetchLatestRelease returned error: %v", err)
			}

			if release.TagName != "v1.2.3" {
				t.Fatalf("fetchLatestRelease tag = %q, want %q", release.TagName, "v1.2.3")
			}

			if attempts != 2 {
				t.Fatalf("attempt count = %d, want %d", attempts, 2)
			}
		})
	}
}

func TestFetchLatestReleaseFormatsRateLimitErrors(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	resetUnix := int64(1773082136)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Reset", "1773082136")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded for 198.27.222.106."}`))
	}))
	defer server.Close()

	configureGitHubTestRequest(t, server.URL)

	_, err := fetchLatestRelease(context.Background(), false)
	if err == nil {
		t.Fatal("fetchLatestRelease error = nil, want rate-limit error")
	}

	expectedReset := time.Unix(resetUnix, 0).UTC().Format(time.RFC3339)
	if !strings.Contains(err.Error(), expectedReset) {
		t.Fatalf("error %q does not contain reset timestamp %q", err.Error(), expectedReset)
	}

	if !strings.Contains(err.Error(), "GITHUB_TOKEN or GH_TOKEN") {
		t.Fatalf("error %q does not mention GitHub token guidance", err.Error())
	}

	if !strings.Contains(err.Error(), "API rate limit exceeded") {
		t.Fatalf("error %q does not include rate-limit message", err.Error())
	}
}

func configureGitHubTestRequest(t *testing.T, baseURL string) {
	t.Helper()

	originalBaseURL := gitHubAPIBaseURL
	originalMaxRetries := gitHubMaxRetries
	originalBaseDelay := gitHubRetryBaseDelay
	originalMaxDelay := gitHubRetryMaxDelay

	gitHubAPIBaseURL = baseURL
	gitHubMaxRetries = 2
	gitHubRetryBaseDelay = time.Millisecond
	gitHubRetryMaxDelay = 2 * time.Millisecond

	t.Cleanup(func() {
		gitHubAPIBaseURL = originalBaseURL
		gitHubMaxRetries = originalMaxRetries
		gitHubRetryBaseDelay = originalBaseDelay
		gitHubRetryMaxDelay = originalMaxDelay
	})
}
