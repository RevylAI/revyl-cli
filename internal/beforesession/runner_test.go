package beforesession

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/config"
)

// writeScript creates an executable script at repoRoot/relPath.
func writeScript(t *testing.T, repoRoot, relPath, body string) string {
	t.Helper()
	full := filepath.Join(repoRoot, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return full
}

// skipPOSIXShellFixture skips tests that exec a #!/bin/sh fixture on Windows,
// where bare shell scripts are not host-native executables.
func skipPOSIXShellFixture(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture is not executable on Windows")
	}
}

// newRepoRoot returns a symlink-resolved temp directory, matching what
// FindRepoRoot hands the runner on macOS where /var is a symlink.
func newRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	return root
}

func TestRun_NotConfiguredIsNoOp(t *testing.T) {
	result, err := Run(context.Background(), newRepoRoot(t), nil)
	if err != nil {
		t.Fatalf("Run() error = %v, want nil for absent config", err)
	}
	if len(result.Values) != 0 {
		t.Fatalf("Run() values = %v, want none", result.Values)
	}
}

func TestRun_ParsesKeyValueLinesAndIgnoresLogOutput(t *testing.T) {
	skipPOSIXShellFixture(t)
	root := newRepoRoot(t)
	writeScript(t, root, "scripts/mint.sh", `#!/bin/sh
echo "Minting a token for the test session..."
echo "E2E_AUTH_TOKEN=tok-123"
echo "not a pair"
echo "Setting FOO=bar in the app"
echo "E2E_USER_ID=user-9"
`)

	result, err := Run(context.Background(), root, &config.BeforeSessionConfig{
		Script: "./scripts/mint.sh",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := result.Values["E2E_AUTH_TOKEN"]; got != "tok-123" {
		t.Fatalf("E2E_AUTH_TOKEN = %q, want tok-123", got)
	}
	if got := result.Values["E2E_USER_ID"]; got != "user-9" {
		t.Fatalf("E2E_USER_ID = %q, want user-9", got)
	}
	// "Setting FOO=bar in the app" is prose, not a value line: the text left
	// of the first "=" is not a valid key.
	if len(result.Values) != 2 {
		t.Fatalf("Run() parsed %d values, want 2: %v", len(result.Values), result.Values)
	}
}

func TestRun_RunsFromRepoRootRegardlessOfWorkingDirectory(t *testing.T) {
	skipPOSIXShellFixture(t)
	root := newRepoRoot(t)
	writeScript(t, root, "scripts/pwd.sh", `#!/bin/sh
echo "CWD_MARKER=$(pwd)"
`)

	result, err := Run(context.Background(), root, &config.BeforeSessionConfig{
		Script: "scripts/pwd.sh",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := result.Values["CWD_MARKER"]; got != root {
		t.Fatalf("script cwd = %q, want repo root %q", got, root)
	}
}

func TestRun_NonZeroExitIsFatalAndRedacted(t *testing.T) {
	skipPOSIXShellFixture(t)
	root := newRepoRoot(t)
	writeScript(t, root, "scripts/fail.sh", `#!/bin/sh
echo "SECRET_TOKEN=super-secret"
echo "backend rejected credential super-secret" >&2
exit 3
`)

	_, err := Run(context.Background(), root, &config.BeforeSessionConfig{
		Script: "./scripts/fail.sh",
	})
	if err == nil {
		t.Fatal("Run() error = nil, want a fatal setup failure")
	}
	if !strings.Contains(err.Error(), "exit 3") {
		t.Fatalf("Run() error = %q, want the exit code", err)
	}
	if strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("Run() error leaked script output: %q", err)
	}
}

func TestRun_TimesOut(t *testing.T) {
	skipPOSIXShellFixture(t)
	root := newRepoRoot(t)
	writeScript(t, root, "scripts/hang.sh", "#!/bin/sh\nsleep 30\n")

	_, err := Run(context.Background(), root, &config.BeforeSessionConfig{
		Script:         "./scripts/hang.sh",
		TimeoutSeconds: 1,
	})
	if err == nil {
		t.Fatal("Run() error = nil, want a timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Run() error = %q, want a timeout message", err)
	}
}

func TestRun_RejectsScriptOutsideRepository(t *testing.T) {
	root := newRepoRoot(t)
	outside := newRepoRoot(t)
	writeScript(t, outside, "evil.sh", "#!/bin/sh\necho STOLEN=1\n")

	testCases := []struct {
		name   string
		script string
	}{
		{name: "relative traversal", script: "../" + filepath.Base(outside) + "/evil.sh"},
		{name: "absolute path", script: filepath.Join(outside, "evil.sh")},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := Run(context.Background(), root, &config.BeforeSessionConfig{
				Script: testCase.script,
			})
			if err == nil {
				t.Fatal("Run() error = nil, want rejection outside the repository")
			}
			if !strings.Contains(err.Error(), "outside the repository") &&
				!strings.Contains(err.Error(), "was not found") {
				t.Fatalf("Run() error = %q, want a containment failure", err)
			}
		})
	}
}

func TestRun_AllowsScriptElsewhereInGitWorktree(t *testing.T) {
	skipPOSIXShellFixture(t)
	gitRoot := newRepoRoot(t)
	if err := os.Mkdir(filepath.Join(gitRoot, ".git"), 0o755); err != nil {
		t.Fatalf("Mkdir(.git) error = %v", err)
	}
	projectRoot := filepath.Join(gitRoot, "ios")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(ios) error = %v", err)
	}
	writeScript(t, gitRoot, ".ai-skills/ensure.sh", `#!/bin/sh
echo "TOKEN=from-monorepo"
`)

	result, err := Run(context.Background(), projectRoot, &config.BeforeSessionConfig{
		Script: "../.ai-skills/ensure.sh",
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want success for in-worktree ../ script", err)
	}
	if got := result.Values["TOKEN"]; got != "from-monorepo" {
		t.Fatalf("TOKEN = %q, want from-monorepo", got)
	}
}

func TestRun_RejectsScriptOutsideGitWorktree(t *testing.T) {
	skipPOSIXShellFixture(t)
	gitRoot := newRepoRoot(t)
	if err := os.Mkdir(filepath.Join(gitRoot, ".git"), 0o755); err != nil {
		t.Fatalf("Mkdir(.git) error = %v", err)
	}
	projectRoot := filepath.Join(gitRoot, "ios")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(ios) error = %v", err)
	}
	outside := newRepoRoot(t)
	writeScript(t, outside, "evil.sh", "#!/bin/sh\necho STOLEN=1\n")

	_, err := Run(context.Background(), projectRoot, &config.BeforeSessionConfig{
		Script: filepath.Join(outside, "evil.sh"),
	})
	if err == nil {
		t.Fatal("Run() error = nil, want rejection outside the git worktree")
	}
	if !strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("Run() error = %q, want a containment failure", err)
	}
}

func TestRun_RejectsSymlinkEscapingRepository(t *testing.T) {
	root := newRepoRoot(t)
	outside := newRepoRoot(t)
	target := writeScript(t, outside, "evil.sh", "#!/bin/sh\necho STOLEN=1\n")

	link := filepath.Join(root, "setup.sh")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := Run(context.Background(), root, &config.BeforeSessionConfig{Script: "./setup.sh"})
	if err == nil {
		t.Fatal("Run() error = nil, want rejection of a symlink out of the repository")
	}
	if !strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("Run() error = %q, want a containment failure", err)
	}
}

func TestRun_RejectsMissingAndNonExecutableScripts(t *testing.T) {
	root := newRepoRoot(t)
	if err := os.WriteFile(filepath.Join(root, "plain.sh"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	testCases := []struct {
		name        string
		script      string
		wantMessage string
	}{
		{name: "missing", script: "./nope.sh", wantMessage: "was not found"},
		{name: "not executable", script: "./plain.sh", wantMessage: "not executable"},
		{name: "directory", script: ".", wantMessage: "not a regular file"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.name == "not executable" && runtime.GOOS == "windows" {
				t.Skip("Unix execute-bit check is not enforced on Windows")
			}
			_, err := Run(context.Background(), root, &config.BeforeSessionConfig{
				Script: testCase.script,
			})
			if err == nil || !strings.Contains(err.Error(), testCase.wantMessage) {
				t.Fatalf("Run() error = %v, want %q", err, testCase.wantMessage)
			}
		})
	}
}

func TestRun_RejectsAmbiguousValueOutput(t *testing.T) {
	skipPOSIXShellFixture(t)
	testCases := []struct {
		name        string
		body        string
		wantMessage string
	}{
		{
			name:        "empty value",
			body:        "#!/bin/sh\necho \"E2E_AUTH_TOKEN=\"\n",
			wantMessage: "empty value",
		},
		{
			name:        "duplicate key",
			body:        "#!/bin/sh\necho \"TOKEN=a\"\necho \"TOKEN=b\"\n",
			wantMessage: "more than once",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			root := newRepoRoot(t)
			writeScript(t, root, "setup.sh", testCase.body)

			_, err := Run(context.Background(), root, &config.BeforeSessionConfig{
				Script: "./setup.sh",
			})
			if err == nil || !strings.Contains(err.Error(), testCase.wantMessage) {
				t.Fatalf("Run() error = %v, want %q", err, testCase.wantMessage)
			}
		})
	}
}

func TestRun_PropagatesCallerCancellation(t *testing.T) {
	skipPOSIXShellFixture(t)
	root := newRepoRoot(t)
	writeScript(t, root, "hang.sh", "#!/bin/sh\nsleep 30\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Run(ctx, root, &config.BeforeSessionConfig{Script: "./hang.sh"})
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("Run() error = %v, want a cancellation message", err)
	}
}

func TestLimitedWriter_MarksTruncationWhenBudgetExceeded(t *testing.T) {
	var buf bytes.Buffer
	w := &limitedWriter{buf: &buf, remaining: 4}

	n, err := w.Write([]byte("ab"))
	if err != nil || n != 2 {
		t.Fatalf("Write(ab) = (%d, %v), want (2, nil)", n, err)
	}
	if w.truncated {
		t.Fatal("truncated = true after a write within budget")
	}

	n, err = w.Write([]byte("cdef"))
	if err != nil || n != 4 {
		t.Fatalf("Write(cdef) = (%d, %v), want (4, nil)", n, err)
	}
	if !w.truncated {
		t.Fatal("truncated = false, want true after a write past the budget")
	}
	if got := buf.String(); got != "abcd" {
		t.Fatalf("buf = %q, want abcd", got)
	}

	n, err = w.Write([]byte("more"))
	if err != nil || n != 4 {
		t.Fatalf("Write(more) = (%d, %v), want (4, nil)", n, err)
	}
	if got := buf.String(); got != "abcd" {
		t.Fatalf("buf after discard = %q, want abcd", got)
	}
}

func TestLimitedWriter_ExactBudgetIsNotTruncation(t *testing.T) {
	var buf bytes.Buffer
	w := &limitedWriter{buf: &buf, remaining: 4}

	n, err := w.Write([]byte("abcd"))
	if err != nil || n != 4 {
		t.Fatalf("Write(abcd) = (%d, %v), want (4, nil)", n, err)
	}
	if w.truncated {
		t.Fatal("truncated = true after a write that fills the budget exactly")
	}
	if got := buf.String(); got != "abcd" {
		t.Fatalf("buf = %q, want abcd", got)
	}
}

func TestRun_RejectsTruncatedStdout(t *testing.T) {
	skipPOSIXShellFixture(t)
	previousLimit := maxCapturedOutputBytes
	maxCapturedOutputBytes = 64
	t.Cleanup(func() { maxCapturedOutputBytes = previousLimit })

	root := newRepoRoot(t)
	secret := "super-secret-token-value-that-must-not-leak"
	// Pad past the tiny capture budget mid KEY=VALUE so a naive parse would
	// accept a corrupted partial value.
	writeScript(t, root, "setup.sh", "#!/bin/sh\necho \"E2E_AUTH_TOKEN="+secret+strings.Repeat("x", 80)+"\"\n")

	result, err := Run(context.Background(), root, &config.BeforeSessionConfig{
		Script: "./setup.sh",
	})
	if err == nil {
		t.Fatal("Run() error = nil, want a truncation failure")
	}
	if !strings.Contains(err.Error(), "produced more than") ||
		!strings.Contains(err.Error(), "stdout") {
		t.Fatalf("Run() error = %q, want a stdout truncation message", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Run() error leaked script output: %q", err)
	}
	if len(result.Values) != 0 {
		t.Fatalf("Run() values = %v, want none on truncation", result.Values)
	}
}
