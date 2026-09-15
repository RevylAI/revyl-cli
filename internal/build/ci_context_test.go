package build

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectGitHubActionsPullRequest(t *testing.T) {
	dir := t.TempDir()
	eventPath := filepath.Join(dir, "event.json")
	payload := `{"pull_request":{"html_url":"https://github.com/acme/mobile/pull/42","number":42,"head":{"sha":"head-sha"},"base":{"sha":"base-sha"}}}`
	if err := os.WriteFile(eventPath, []byte(payload), 0o600); err != nil {
		t.Fatalf("write event: %v", err)
	}

	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("BUILDKITE", "")
	t.Setenv("GITHUB_EVENT_PATH", eventPath)
	t.Setenv("GITHUB_REPOSITORY", "acme/mobile")
	t.Setenv("GITHUB_SERVER_URL", "https://github.com")
	t.Setenv("GITHUB_RUN_ID", "12345")
	t.Setenv("GITHUB_REF_NAME", "feature/checkout")
	t.Setenv("GITHUB_ACTOR", "janedoe")

	context, ok := DetectCIContext()
	if !ok {
		t.Fatal("DetectCIContext() ok = false, want true")
	}
	want := CIContext{
		System:       "github-actions",
		CommitSHA:    "head-sha",
		Branch:       "feature/checkout",
		RunID:        "12345",
		RunURL:       "https://github.com/acme/mobile/actions/runs/12345",
		Repository:   "acme/mobile",
		Actor:        "janedoe",
		ActorURL:     "https://github.com/janedoe",
		SCMProvider:  "github",
		SCMNamespace: "acme",
		SCMProject:   "mobile",
		ReviewNumber: 42,
		ReviewURL:    "https://github.com/acme/mobile/pull/42",
		BaseSHA:      "base-sha",
	}
	if context != want {
		t.Fatalf("DetectCIContext() = %#v, want %#v", context, want)
	}
}

func TestDetectBuildkiteGitHubPullRequest(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("BUILDKITE", "true")
	t.Setenv("BUILDKITE_REPO", "git@github.com:acme/mobile.git")
	t.Setenv("BUILDKITE_PULL_REQUEST", "42")
	t.Setenv("BUILDKITE_PULL_REQUEST_HEAD_COMMIT", "head-sha")
	t.Setenv("BUILDKITE_COMMIT", "merge-sha")
	t.Setenv("BUILDKITE_BRANCH", "feature/checkout")
	t.Setenv("BUILDKITE_BUILD_ID", "build-id")
	t.Setenv("BUILDKITE_BUILD_URL", "https://buildkite.com/acme/mobile/builds/42")
	t.Setenv("BUILDKITE_BUILD_CREATOR", "Jane Doe")

	context, ok := DetectCIContext()
	if !ok {
		t.Fatal("DetectCIContext() ok = false, want true")
	}
	want := CIContext{
		System:       "buildkite",
		CommitSHA:    "head-sha",
		Branch:       "feature/checkout",
		RunID:        "build-id",
		RunURL:       "https://buildkite.com/acme/mobile/builds/42",
		Repository:   "acme/mobile",
		Actor:        "Jane Doe",
		SCMProvider:  "github",
		SCMNamespace: "acme",
		SCMProject:   "mobile",
		ReviewNumber: 42,
		ReviewURL:    "https://github.com/acme/mobile/pull/42",
	}
	if context != want {
		t.Fatalf("DetectCIContext() = %#v, want %#v", context, want)
	}
}

func TestDetectBuildkitePushUsesCommitAndOmitsReview(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("BUILDKITE", "true")
	t.Setenv("BUILDKITE_REPO", "https://github.com/acme/mobile.git")
	t.Setenv("BUILDKITE_PULL_REQUEST", "false")
	t.Setenv("BUILDKITE_PULL_REQUEST_HEAD_COMMIT", "")
	t.Setenv("BUILDKITE_COMMIT", "push-sha")

	context, ok := DetectCIContext()
	if !ok {
		t.Fatal("DetectCIContext() ok = false, want true")
	}
	if context.CommitSHA != "push-sha" {
		t.Fatalf("CommitSHA = %q, want push-sha", context.CommitSHA)
	}
	if context.ReviewNumber != 0 || context.ReviewURL != "" {
		t.Fatalf("review = (%d, %q), want no review", context.ReviewNumber, context.ReviewURL)
	}
}

func TestDetectOutsideSupportedCI(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("BUILDKITE", "")

	if context, ok := DetectCIContext(); ok || context != (CIContext{}) {
		t.Fatalf("DetectCIContext() = (%#v, %t), want zero context and false", context, ok)
	}
}

func TestParseGitHubRepository(t *testing.T) {
	for _, remote := range []string{
		"git@github.com:acme/mobile.git",
		"ssh://git@github.com/acme/mobile.git",
		"git://github.com/acme/mobile.git",
		"https://github.com/acme/mobile.git/",
		"https://user:secret@github.com/acme/mobile.git?token=secret#fragment",
	} {
		t.Run(remote, func(t *testing.T) {
			namespace, project, ok := parseGitHubRepository(remote)
			if !ok || namespace != "acme" || project != "mobile" {
				t.Fatalf("repository = (%q, %q, %t), want (acme, mobile, true)", namespace, project, ok)
			}
		})
	}
	for _, remote := range []string{
		"",
		"https://gitlab.com/acme/mobile.git",
		"https://notgithub.com/acme/mobile.git",
		"https://example.com/github.com/acme/mobile.git",
		"https://github.com/acme/mobile/extra",
		"https://github.com/acme/..",
		"https://github.com/acme/mobile%0Atoken",
	} {
		t.Run(remote, func(t *testing.T) {
			if namespace, project, ok := parseGitHubRepository(remote); ok || namespace != "" || project != "" {
				t.Fatalf("repository = (%q, %q, %t), want no GitHub identity", namespace, project, ok)
			}
		})
	}
}
