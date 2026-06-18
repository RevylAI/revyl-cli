package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeBranchForVersion(t *testing.T) {
	tests := []struct {
		branch string
		want   string
	}{
		{branch: "feature/new-login", want: "feature-new-login"},
		{branch: "Feature/New_Login", want: "feature-new-login"},
		{branch: "bugfix!@#$%^&*()", want: "bugfix"},
		{branch: "HEAD", want: ""},
		{branch: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			got := sanitizeBranchForVersion(tt.branch)
			if got != tt.want {
				t.Fatalf("sanitizeBranchForVersion(%q) = %q, want %q", tt.branch, got, tt.want)
			}
		})
	}
}

func TestGenerateVersionStringForWorkDir_WithGitBranch(t *testing.T) {
	tmpDir := t.TempDir()
	runGit(t, tmpDir, "init")
	runGit(t, tmpDir, "-c", "user.email=test@example.com", "-c", "user.name=Test User", "commit", "--allow-empty", "-m", "init")
	runGit(t, tmpDir, "checkout", "-b", "feature/new-login")

	version := GenerateVersionStringForWorkDir(tmpDir)
	if !strings.HasPrefix(version, "feature-new-login-") {
		t.Fatalf("GenerateVersionStringForWorkDir() = %q, want prefix %q", version, "feature-new-login-")
	}
}

func TestGenerateVersionStringForWorkDir_NonGitFallback(t *testing.T) {
	version := GenerateVersionStringForWorkDir(t.TempDir())
	if strings.Contains(version, "/") || strings.Contains(version, "_") {
		t.Fatalf("GenerateVersionStringForWorkDir() = %q, want timestamp-like fallback", version)
	}
}

func TestCollectMetadataGitHubActionsPullRequest(t *testing.T) {
	dir := t.TempDir()
	eventPath := filepath.Join(dir, "event.json")
	payload := `{
		"pull_request": {
			"html_url": "https://github.com/acme/mobile/pull/42",
			"number": 42,
			"head": { "sha": "true-head-sha" },
			"base": { "sha": "base-sha" }
		}
	}`
	if err := os.WriteFile(eventPath, []byte(payload), 0o600); err != nil {
		t.Fatalf("write event: %v", err)
	}

	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_EVENT_PATH", eventPath)
	t.Setenv("GITHUB_REPOSITORY", "acme/mobile")
	t.Setenv("GITHUB_SERVER_URL", "https://github.com")
	t.Setenv("GITHUB_RUN_ID", "12345")
	t.Setenv("GITHUB_SHA", "merge-sha")
	t.Setenv("GITHUB_ACTOR", "janedoe")
	t.Setenv("REVYL_PR_HEAD_SHA", "env-head-sha")

	metadata := CollectMetadata(dir, "", "Android", 0)

	checks := map[string]interface{}{
		"ci_system":         "github-actions",
		"ci_run_id":         "12345",
		"ci_run_url":        "https://github.com/acme/mobile/actions/runs/12345",
		"ci_actor":          "janedoe",
		"github_repository": "acme/mobile",
		"scm_provider":      "github",
		"scm_repo":          "acme/mobile",
		"scm_namespace":     "acme",
		"scm_project":       "mobile",
		"scm_review_number": 42,
		"pr_number":         42,
		"scm_head_sha":      "true-head-sha",
		"scm_base_sha":      "base-sha",
		"scm_platform":      "android",
	}
	for key, want := range checks {
		if got := metadata[key]; got != want {
			t.Fatalf("%s = %#v, want %#v", key, got, want)
		}
	}
}

func TestCollectMetadataGitHubActionsFallbackSHA(t *testing.T) {
	dir := t.TempDir()

	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_EVENT_PATH", "")
	t.Setenv("GITHUB_REPOSITORY", "acme/mobile")
	t.Setenv("GITHUB_SHA", "push-sha")
	t.Setenv("REVYL_PR_HEAD_SHA", "")

	metadata := CollectMetadata(dir, "", "ios", 0)

	if got := metadata["scm_head_sha"]; got != "push-sha" {
		t.Fatalf("scm_head_sha = %#v, want push-sha", got)
	}
	if got := metadata["scm_platform"]; got != "ios" {
		t.Fatalf("scm_platform = %#v, want ios", got)
	}
	if _, ok := metadata["scm_review_number"]; ok {
		t.Fatalf("did not expect scm_review_number on non-PR metadata")
	}
}

func TestCollectMetadataNoGitHubActionsContext(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("GITHUB_REPOSITORY", "acme/mobile")

	metadata := CollectMetadata(t.TempDir(), "", "ios", 0)
	if _, ok := metadata["scm_provider"]; ok {
		t.Fatalf("did not expect scm_provider outside GitHub Actions")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = filepath.Clean(dir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(output))
	}
}
