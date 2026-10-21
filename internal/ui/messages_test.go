package ui

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// captureStdoutAndStderr redirects os.Stdout and os.Stderr to pipes, runs fn,
// then returns whatever was written to each.
func captureStdoutAndStderr(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()

	origOut := os.Stdout
	origErr := os.Stderr
	defer func() {
		os.Stdout = origOut
		os.Stderr = origErr
	}()

	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating stdout pipe: %v", err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating stderr pipe: %v", err)
	}

	os.Stdout = wOut
	os.Stderr = wErr

	fn()

	wOut.Close()
	wErr.Close()

	var bufOut, bufErr bytes.Buffer
	bufOut.ReadFrom(rOut)
	bufErr.ReadFrom(rErr)

	return bufOut.String(), bufErr.String()
}

func TestPrintInfoWritesToStderr(t *testing.T) {
	SetQuietMode(false)
	stdout, stderr := captureStdoutAndStderr(t, func() {
		PrintInfo("hello %s", "world")
	})

	if stdout != "" {
		t.Errorf("PrintInfo wrote to stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "hello world") {
		t.Errorf("PrintInfo did not write to stderr; got %q", stderr)
	}
}

func TestPrintWarningWritesToStderr(t *testing.T) {
	stdout, stderr := captureStdoutAndStderr(t, func() {
		PrintWarning("danger %d", 42)
	})

	if stdout != "" {
		t.Errorf("PrintWarning wrote to stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "danger 42") {
		t.Errorf("PrintWarning did not write to stderr; got %q", stderr)
	}
}

func TestPrintSuccessWritesToStderr(t *testing.T) {
	stdout, stderr := captureStdoutAndStderr(t, func() {
		PrintSuccess("done")
	})

	if stdout != "" {
		t.Errorf("PrintSuccess wrote to stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "done") {
		t.Errorf("PrintSuccess did not write to stderr; got %q", stderr)
	}
}

func TestPrintErrorWritesToStderr(t *testing.T) {
	stdout, stderr := captureStdoutAndStderr(t, func() {
		PrintError("fail")
	})

	if stdout != "" {
		t.Errorf("PrintError wrote to stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "fail") {
		t.Errorf("PrintError did not write to stderr; got %q", stderr)
	}
}

func TestPrintInfoSuppressedInQuietMode(t *testing.T) {
	SetQuietMode(true)
	defer SetQuietMode(false)

	stdout, stderr := captureStdoutAndStderr(t, func() {
		PrintInfo("should not appear")
	})

	if stdout != "" {
		t.Errorf("PrintInfo wrote to stdout in quiet mode: %q", stdout)
	}
	if stderr != "" {
		t.Errorf("PrintInfo wrote to stderr in quiet mode: %q", stderr)
	}
}

func TestWorkflowProgressKeepsStdoutForJSON(t *testing.T) {
	passed := true
	children := []ChildTestInfo{{TestName: "Login", Platform: "ios", Status: "completed", Success: &passed}}
	for name, print := range map[string]func(){
		"basic":   func() { PrintBasicWorkflowStatus("running", 1, 2, children) },
		"verbose": func() { PrintVerboseWorkflowStatus("running", 1, 2, 1, 0, "4s", children) },
	} {
		t.Run(name, func(t *testing.T) {
			SetQuietMode(false)
			stdout, stderr := captureStdoutAndStderr(t, print)
			if stdout != "" {
				t.Errorf("workflow progress wrote to stdout: %q", stdout)
			}
			if !strings.Contains(stderr, "Login (ios)") {
				t.Errorf("workflow progress missing its test line on stderr; got %q", stderr)
			}
		})
	}
}

func TestBranchFallbackNoticeWithoutBranchIdentity(t *testing.T) {
	got := BranchFallbackNotice("", "")
	want := "Your Git branch has no usable build; using the app's default branch."
	if got != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}
}

func TestBranchFallbackNoticeNamesGitAndDefaultBranches(t *testing.T) {
	got := BranchFallbackNotice("feature-login", "main")
	want := `Git branch "feature-login" has no usable build; using the app's default branch "main".`
	if got != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}
}

func TestBranchFallbackNoticeNamesAppAndBranch(t *testing.T) {
	notices := &BranchFallbackNotices{}
	got := notices.Notice("app-id", "My App", "branch-id", "base", "feature-login", true)
	want := `Git branch "feature-login" has no usable build on "My App"; using the app's default branch "base".`
	if got != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}
	if again := notices.Notice("app-id", "My App", "branch-id", "base", "feature-login", true); again != "" {
		t.Fatalf("repeated notice = %q", again)
	}
	if got := notices.Notice("other-app", "Other App", "branch-id", "base", "feature-login", false); got != "" {
		t.Fatalf("notice without a fallback = %q", got)
	}
}

func TestBranchSelectionNoticeUsesConfirmedPolicy(t *testing.T) {
	enabled, disabled := true, false
	for _, test := range []struct {
		name, gitBranch, source, want string
		enabled                       *bool
	}{
		{name: "disabled", source: "default_branch", enabled: &disabled, want: `Using app default branch "base" because automatic Git branch detection is off.`},
		{name: "disabled ignores stale context", source: "default_branch", gitBranch: "feature", enabled: &disabled, want: `Using app default branch "base" because automatic Git branch detection is off.`},
		{name: "enabled fallback", source: "default_branch", gitBranch: "feature", enabled: &enabled, want: `Git branch "feature" has no usable build; using the app's default branch "base".`},
		{name: "old server fallback", source: "default_branch", gitBranch: "feature", want: `Git branch "feature" has no usable build; using the app's default branch "base".`},
		{name: "default without git", source: "default_branch", enabled: &enabled},
		{name: "explicit disabled", source: "explicit_branch", enabled: &disabled},
		{name: "exact disabled", enabled: &disabled},
		{name: "git matched", source: "git_branch", gitBranch: "feature", enabled: &enabled},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := BranchSelectionNotice(test.gitBranch, "base", test.source, test.enabled); got != test.want {
				t.Fatalf("notice = %q, want %q", got, test.want)
			}
		})
	}
}

func TestBranchSelectionNoticesKeepAppsAndReasonsDistinct(t *testing.T) {
	disabled := false
	notices := &BranchFallbackNotices{}
	want := `Using app default branch "base" on "Example" because automatic Git branch detection is off.`
	if got := notices.SelectionNotice("app", "Example", "branch", "base", "", "default_branch", &disabled); got != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}
	if got := notices.SelectionNotice("app", "Example", "branch", "base", "", "default_branch", &disabled); got != "" {
		t.Fatalf("repeated notice = %q", got)
	}
	if got := notices.SelectionNotice("other", "Other", "branch", "base", "", "default_branch", &disabled); !strings.Contains(got, `on "Other"`) {
		t.Fatalf("other app notice = %q", got)
	}
	if got := notices.SelectionNotice("app", "Example", "branch", "base", "feature", "default_branch", nil); !strings.Contains(got, "has no usable build") {
		t.Fatalf("changed reason notice = %q", got)
	}
}
