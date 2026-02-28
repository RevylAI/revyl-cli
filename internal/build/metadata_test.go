package build

import (
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

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = filepath.Clean(dir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(output))
	}
}
