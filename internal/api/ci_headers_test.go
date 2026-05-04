package api

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSetCIHeaders_NotInActions(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")

	req := httptest.NewRequest("POST", "http://example.test", nil)
	setCIHeaders(req)

	if got := req.Header.Get("X-CI-System"); got != "" {
		t.Fatalf("expected no X-CI-System header outside Actions, got %q", got)
	}
}

func TestSetCIHeaders_PullRequest(t *testing.T) {
	dir := t.TempDir()
	eventPath := filepath.Join(dir, "event.json")
	payload := `{"pull_request":{"html_url":"https://github.com/acme/web/pull/482","number":482}}`
	if err := os.WriteFile(eventPath, []byte(payload), 0o600); err != nil {
		t.Fatalf("write event: %v", err)
	}

	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_SERVER_URL", "https://github.com")
	t.Setenv("GITHUB_REPOSITORY", "acme/web")
	t.Setenv("GITHUB_ACTOR", "janedoe")
	t.Setenv("GITHUB_SHA", "9f2c1ab0")
	t.Setenv("GITHUB_REF_NAME", "feat/cool")
	t.Setenv("GITHUB_RUN_ID", "12345")
	t.Setenv("GITHUB_EVENT_PATH", eventPath)

	req := httptest.NewRequest("POST", "http://example.test", nil)
	setCIHeaders(req)

	checks := map[string]string{
		"X-CI-System":     "github-actions",
		"X-CI-Commit-SHA": "9f2c1ab0",
		"X-CI-Branch":     "feat/cool",
		"X-CI-Run-ID":     "12345",
		"X-CI-Repository": "acme/web",
		"X-CI-Actor":      "janedoe",
		"X-CI-Actor-URL":  "https://github.com/janedoe",
		"X-CI-Run-URL":    "https://github.com/acme/web/actions/runs/12345",
		"X-CI-PR-URL":     "https://github.com/acme/web/pull/482",
		"X-CI-PR-Number":  "482",
	}
	for key, want := range checks {
		if got := req.Header.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestSetCIHeaders_PushEvent_NoPR(t *testing.T) {
	dir := t.TempDir()
	eventPath := filepath.Join(dir, "event.json")
	if err := os.WriteFile(eventPath, []byte(`{"head_commit":{"id":"abc"}}`), 0o600); err != nil {
		t.Fatalf("write event: %v", err)
	}

	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_SERVER_URL", "https://github.com")
	t.Setenv("GITHUB_REPOSITORY", "acme/web")
	t.Setenv("GITHUB_ACTOR", "janedoe")
	t.Setenv("GITHUB_SHA", "9f2c1ab0")
	t.Setenv("GITHUB_REF_NAME", "main")
	t.Setenv("GITHUB_RUN_ID", "12345")
	t.Setenv("GITHUB_EVENT_PATH", eventPath)

	req := httptest.NewRequest("POST", "http://example.test", nil)
	setCIHeaders(req)

	if got := req.Header.Get("X-CI-PR-URL"); got != "" {
		t.Errorf("expected no X-CI-PR-URL on push, got %q", got)
	}
	if got := req.Header.Get("X-CI-PR-Number"); got != "" {
		t.Errorf("expected no X-CI-PR-Number on push, got %q", got)
	}
	if got := req.Header.Get("X-CI-Commit-SHA"); got != "9f2c1ab0" {
		t.Errorf("X-CI-Commit-SHA = %q, want 9f2c1ab0", got)
	}
}

func TestSetCIHeaders_MissingEventPath(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_SERVER_URL", "https://github.com")
	t.Setenv("GITHUB_REPOSITORY", "acme/web")
	t.Setenv("GITHUB_ACTOR", "")
	t.Setenv("GITHUB_SHA", "")
	t.Setenv("GITHUB_REF_NAME", "")
	t.Setenv("GITHUB_RUN_ID", "")
	t.Setenv("GITHUB_EVENT_PATH", "")

	req := httptest.NewRequest("POST", "http://example.test", nil)
	setCIHeaders(req)

	// Still sets the system marker even when most context is missing.
	if got := req.Header.Get("X-CI-System"); got != "github-actions" {
		t.Errorf("X-CI-System = %q, want github-actions", got)
	}
	// Empty values are not sent.
	if got := req.Header.Get("X-CI-Actor"); got != "" {
		t.Errorf("expected no X-CI-Actor when GITHUB_ACTOR empty, got %q", got)
	}
}
