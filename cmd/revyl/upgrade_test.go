package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/skillcatalog"
	"github.com/revyl/cli/internal/testutil"
	"github.com/revyl/cli/internal/ui"
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
			name:     "pipx venvs path",
			execPath: "/Users/alice/.local/pipx/venvs/revyl/bin/revyl",
			expected: "pipx",
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

func TestRunUpgradeDoesNotApplyFetchTimeoutToDownload(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("REVYL_NO_POST_UPGRADE_SKILL_INSTALL", "1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// A version far ahead of the build version so the self-update path runs.
		_, _ = w.Write([]byte(`{"tag_name":"v999.0.0"}`))
	}))
	defer server.Close()
	configureGitHubTestRequest(t, server.URL)

	originalDetect := detectInstallMethodFn
	detectInstallMethodFn = func() string { return "direct" }
	t.Cleanup(func() { detectInstallMethodFn = originalDetect })

	var captured context.Context
	originalSelfUpdate := performSelfUpdateFn
	performSelfUpdateFn = func(ctx context.Context, tagName string) (string, error) {
		captured = ctx
		return "/tmp/revyl", nil
	}
	t.Cleanup(func() { performSelfUpdateFn = originalSelfUpdate })

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	if err := runUpgrade(cmd, nil); err != nil {
		t.Fatalf("runUpgrade() error = %v, want nil", err)
	}

	if captured == nil {
		t.Fatal("performSelfUpdate was not invoked; self-update path not reached")
	}

	// Regression guard: the download phase must NOT inherit the 30s deadline
	// scoped to the GitHub release check. A short deadline here is the bug that
	// produced "context deadline exceeded" while downloading the binary.
	if deadline, ok := captured.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < 2*time.Minute {
			t.Fatalf("download context deadline = %v, want no short deadline (got <2m)", remaining)
		}
	}
}

func TestPerformBrewUpgrade_CallsCorrectCommands(t *testing.T) {
	var calls [][]string
	original := brewCommandRunner
	brewCommandRunner = func(name string, args ...string) *exec.Cmd {
		calls = append(calls, append([]string{name}, args...))
		return exec.Command("true")
	}
	t.Cleanup(func() { brewCommandRunner = original })

	if err := performBrewUpgrade(); err != nil {
		t.Fatalf("performBrewUpgrade() error = %v, want nil", err)
	}

	if len(calls) != 2 {
		t.Fatalf("expected 2 brew calls, got %d: %v", len(calls), calls)
	}

	if calls[0][0] != "brew" || calls[0][1] != "update" {
		t.Fatalf("first call = %v, want [brew update]", calls[0])
	}

	if calls[1][0] != "brew" || calls[1][1] != "upgrade" || calls[1][2] != "revyl" {
		t.Fatalf("second call = %v, want [brew upgrade revyl]", calls[1])
	}
}

func TestPerformBrewUpgrade_StopsOnUpdateFailure(t *testing.T) {
	var calls [][]string
	original := brewCommandRunner
	brewCommandRunner = func(name string, args ...string) *exec.Cmd {
		calls = append(calls, append([]string{name}, args...))
		return exec.Command("false")
	}
	t.Cleanup(func() { brewCommandRunner = original })

	err := performBrewUpgrade()
	if err == nil {
		t.Fatal("performBrewUpgrade() error = nil, want error on brew update failure")
	}

	if !strings.Contains(err.Error(), "brew update failed") {
		t.Fatalf("error = %q, want to contain %q", err.Error(), "brew update failed")
	}

	if len(calls) != 1 {
		t.Fatalf("expected 1 brew call (should stop after update failure), got %d: %v", len(calls), calls)
	}
}

func TestDiscoverPostUpgradeSkillTargetsUsesExistingProjectAndGlobalDirs(t *testing.T) {
	workDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workDir)
	testutil.SetHomeDir(t, homeDir)

	var want []skillInstallTarget
	for _, global := range []bool{true, false} {
		for _, tool := range []string{"shared", "cursor", "claude", "codex"} {
			directory := "." + tool
			if tool == "shared" {
				directory = ".agents"
			}
			path := filepath.Join(directory, "skills")
			if global {
				path = filepath.Join(homeDir, path)
			}
			writeUpgradeSkillFixture(t, path, "revyl-cli-dev-loop")
			want = append(want, skillInstallTarget{tool: tool, path: path, global: global})
		}
	}

	if targets := discoverPostUpgradeSkillTargets(); !reflect.DeepEqual(targets, want) {
		t.Fatalf("targets = %#v, want %#v", targets, want)
	}
}

func TestDiscoverPostUpgradeSkillTargetsDedupesHomeAsGlobal(t *testing.T) {
	homeDir := t.TempDir()
	withWorkingDir(t, homeDir)
	testutil.SetHomeDir(t, homeDir)

	for _, directory := range []string{".agents", ".cursor", ".claude", ".codex"} {
		writeUpgradeSkillFixture(t, filepath.Join(homeDir, directory, "skills"), "revyl-cli-dev-loop")
	}

	targets := discoverPostUpgradeSkillTargets()
	if len(targets) != 4 {
		t.Fatalf("targets = %#v, want exactly four", targets)
	}
	for index, tool := range []string{"shared", "cursor", "claude", "codex"} {
		if targets[index].tool != tool || !targets[index].global {
			t.Fatalf("target = %#v, want global %s", targets[index], tool)
		}
	}
}

func TestDiscoverPostUpgradeSkillTargetsRecognizesCatalogAndRetiredNames(t *testing.T) {
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	testutil.SetHomeDir(t, t.TempDir())
	for _, name := range append(skillcatalog.Names(), legacySkillNames...) {
		t.Run(name, func(t *testing.T) {
			skillsDir := filepath.Join(workDir, ".cursor", "skills")
			writeUpgradeSkillFixture(t, skillsDir, name)
			t.Cleanup(func() { _ = os.RemoveAll(skillsDir) })

			targets := discoverPostUpgradeSkillTargets()
			if len(targets) != 1 || targets[0].tool != "cursor" || targets[0].global {
				t.Fatalf("targets = %#v, want project cursor for %s", targets, name)
			}
		})
	}
}

func TestDiscoverPostUpgradeSkillTargetsIgnoresUnrelatedAndIncompleteSkills(t *testing.T) {
	originalQuietMode := ui.IsQuietMode()
	ui.SetQuietMode(false)
	t.Cleanup(func() { ui.SetQuietMode(originalQuietMode) })

	workDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workDir)
	testutil.SetHomeDir(t, homeDir)
	t.Setenv("REVYL_NO_POST_UPGRADE_SKILL_INSTALL", "")

	for _, root := range []string{workDir, homeDir} {
		for _, directory := range []string{".agents", ".cursor", ".claude", ".codex"} {
			skillsDir := filepath.Join(root, directory, "skills")
			writeUpgradeSkillFixture(t, skillsDir, "unrelated-skill")
			writeUpgradeSkillFixture(t, skillsDir, "revyl-custom-skill")
			writeUpgradeSkillFixture(t, skillsDir, "revyl-cli-custom")
			writeUpgradeSkillFixture(t, skillsDir, "revyl-mcp-custom")
			if err := os.MkdirAll(filepath.Join(skillsDir, "revyl-cli-dev-loop", "SKILL.md"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(skillsDir, "revyl-device"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	if targets := discoverPostUpgradeSkillTargets(); len(targets) != 0 {
		t.Fatalf("targets = %#v, want no Revyl installations", targets)
	}
	if output := captureStdoutAndStderr(t, printPostUpgradeSkillGuidance); output != "" {
		t.Fatalf("guidance = %q, want no output without installed Revyl skills", output)
	}
}

func TestPrintPostUpgradeSkillGuidancePreservesSkillsWithoutRunningCommands(t *testing.T) {
	originalQuietMode := ui.IsQuietMode()
	ui.SetQuietMode(false)
	t.Cleanup(func() { ui.SetQuietMode(originalQuietMode) })

	for _, optOut := range []string{"", "1", "false"} {
		t.Run("opt-out="+optOut, func(t *testing.T) {
			workDir := t.TempDir()
			homeDir := t.TempDir()
			withWorkingDir(t, workDir)
			testutil.SetHomeDir(t, homeDir)
			t.Setenv("REVYL_NO_POST_UPGRADE_SKILL_INSTALL", optOut)
			for _, root := range []string{workDir, homeDir} {
				for _, directory := range []string{".agents", ".cursor", ".claude", ".codex"} {
					for _, name := range []string{"revyl-cli-dev-loop", "revyl-mcp-dev-loop", "revyl-device", "revyl-cli-auth-bypass-expo", "unrelated-skill"} {
						writeUpgradeSkillFixture(t, filepath.Join(root, directory, "skills"), name)
					}
				}
			}
			beforeProject := snapshotUpgradeSkillTree(t, workDir)
			beforeGlobal := snapshotUpgradeSkillTree(t, homeDir)
			originalRunner := brewCommandRunner
			brewCommandRunner = func(name string, args ...string) *exec.Cmd {
				t.Fatalf("unexpected command: %s %v", name, args)
				return nil
			}
			t.Cleanup(func() { brewCommandRunner = originalRunner })

			var stdout string
			stderr := captureStdoutAndStderr(t, func() {
				stdout = captureStdout(t, printPostUpgradeSkillGuidance)
			})
			if stdout != "" {
				t.Fatalf("stdout = %q, want guidance only on stderr", stdout)
			}
			if optOut == "" {
				if strings.Count(stderr, "revyl skill update") != 1 || !strings.Contains(stderr, "left unchanged") {
					t.Fatalf("guidance = %q, want one explicit update notice", stderr)
				}
				if lines := strings.Count(strings.TrimSpace(stderr), "\n") + 1; lines > 2 {
					t.Fatalf("guidance has %d lines, want at most two", lines)
				}
			} else if stderr != "" {
				t.Fatalf("guidance = %q, want no output when opted out", stderr)
			}
			if !reflect.DeepEqual(snapshotUpgradeSkillTree(t, workDir), beforeProject) {
				t.Fatal("post-upgrade guidance modified project skills")
			}
			if !reflect.DeepEqual(snapshotUpgradeSkillTree(t, homeDir), beforeGlobal) {
				t.Fatal("post-upgrade guidance modified global skills")
			}
		})
	}
}

func TestPrintPostUpgradeSkillGuidanceRespectsQuietMode(t *testing.T) {
	originalQuietMode := ui.IsQuietMode()
	ui.SetQuietMode(true)
	t.Cleanup(func() { ui.SetQuietMode(originalQuietMode) })

	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	testutil.SetHomeDir(t, t.TempDir())
	t.Setenv("REVYL_NO_POST_UPGRADE_SKILL_INSTALL", "")
	writeUpgradeSkillFixture(t, filepath.Join(workDir, ".agents", "skills"), "revyl-cli-dev-loop")

	if output := captureStdoutAndStderr(t, printPostUpgradeSkillGuidance); output != "" {
		t.Fatalf("guidance = %q, want no output in quiet mode", output)
	}
}

func TestRunUpgradeLeavesSkillsUnchanged(t *testing.T) {
	originalQuietMode := ui.IsQuietMode()
	ui.SetQuietMode(false)
	t.Cleanup(func() { ui.SetQuietMode(originalQuietMode) })

	for _, installMethod := range []string{"direct", "homebrew"} {
		t.Run(installMethod, func(t *testing.T) {
			workDir := t.TempDir()
			homeDir := t.TempDir()
			withWorkingDir(t, workDir)
			testutil.SetHomeDir(t, homeDir)
			t.Setenv("REVYL_NO_POST_UPGRADE_SKILL_INSTALL", "")
			t.Setenv("GITHUB_TOKEN", "")
			t.Setenv("GH_TOKEN", "")
			writeUpgradeSkillFixture(t, filepath.Join(workDir, ".agents", "skills"), "revyl-cli-dev-loop")
			writeUpgradeSkillFixture(t, filepath.Join(workDir, ".cursor", "skills"), "revyl-device")
			writeUpgradeSkillFixture(t, filepath.Join(homeDir, ".claude", "skills"), "revyl-mcp-dev-loop")
			beforeProject := snapshotUpgradeSkillTree(t, workDir)
			beforeGlobal := snapshotUpgradeSkillTree(t, homeDir)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"tag_name":"v999.0.0"}`))
			}))
			defer server.Close()
			configureGitHubTestRequest(t, server.URL)
			originalDetect := detectInstallMethodFn
			originalSelfUpdate := performSelfUpdateFn
			originalRunner := brewCommandRunner
			t.Cleanup(func() {
				detectInstallMethodFn = originalDetect
				performSelfUpdateFn = originalSelfUpdate
				brewCommandRunner = originalRunner
			})
			detectInstallMethodFn = func() string { return installMethod }
			selfUpdates := 0
			performSelfUpdateFn = func(ctx context.Context, tagName string) (string, error) {
				selfUpdates++
				return filepath.Join(workDir, "must-not-execute-new-revyl"), nil
			}
			var calls [][]string
			brewCommandRunner = func(name string, args ...string) *exec.Cmd {
				calls = append(calls, append([]string{name}, args...))
				return exec.Command(os.Args[0], "-test.run=^$")
			}

			var upgradeErr error
			output := captureStdoutAndStderr(t, func() {
				upgradeErr = runUpgrade(newTestCommand(), nil)
			})
			if upgradeErr != nil {
				t.Fatalf("runUpgrade() error = %v", upgradeErr)
			}
			if strings.Count(output, "revyl skill update") != 1 {
				t.Fatalf("upgrade output = %q, want explicit skill update guidance", output)
			}
			if strings.Contains(output, "refresh") || strings.Contains(output, "skill install") {
				t.Fatalf("upgrade output = %q, want no automatic skill installation attempt", output)
			}
			if installMethod == "direct" {
				if len(calls) != 0 || selfUpdates != 1 {
					t.Fatalf("runner calls = %v, self updates = %d; want no commands and one self update", calls, selfUpdates)
				}
			} else {
				want := [][]string{{"brew", "update"}, {"brew", "upgrade", "revyl"}}
				if !reflect.DeepEqual(calls, want) || selfUpdates != 0 {
					t.Fatalf("runner calls = %v, self updates = %d; want %v and no self update", calls, selfUpdates, want)
				}
			}
			if !reflect.DeepEqual(snapshotUpgradeSkillTree(t, workDir), beforeProject) {
				t.Fatal("CLI upgrade modified project skills")
			}
			if !reflect.DeepEqual(snapshotUpgradeSkillTree(t, homeDir), beforeGlobal) {
				t.Fatal("CLI upgrade modified global skills")
			}
		})
	}
}

func TestRunUpgradeCheckAndJSONDoNotUpdateSkills(t *testing.T) {
	originalQuietMode := ui.IsQuietMode()
	ui.SetQuietMode(false)
	t.Cleanup(func() { ui.SetQuietMode(originalQuietMode) })

	for _, jsonOutput := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%t", jsonOutput), func(t *testing.T) {
			workDir := t.TempDir()
			withWorkingDir(t, workDir)
			testutil.SetHomeDir(t, t.TempDir())
			t.Setenv("REVYL_NO_POST_UPGRADE_SKILL_INSTALL", "")
			t.Setenv("GITHUB_TOKEN", "")
			t.Setenv("GH_TOKEN", "")
			writeUpgradeSkillFixture(t, filepath.Join(workDir, ".agents", "skills"), "revyl-cli-dev-loop")
			before := snapshotUpgradeSkillTree(t, workDir)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"tag_name":"v999.0.0"}`))
			}))
			defer server.Close()
			configureGitHubTestRequest(t, server.URL)
			originalCheck := upgradeCheckOnly
			originalJSON := upgradeOutputJSON
			originalDetect := detectInstallMethodFn
			originalUpdate := performSelfUpdateFn
			originalRunner := brewCommandRunner
			t.Cleanup(func() {
				upgradeCheckOnly = originalCheck
				upgradeOutputJSON = originalJSON
				detectInstallMethodFn = originalDetect
				performSelfUpdateFn = originalUpdate
				brewCommandRunner = originalRunner
			})
			upgradeCheckOnly = !jsonOutput
			upgradeOutputJSON = jsonOutput
			detectInstallMethodFn = func() string { return "direct" }
			performSelfUpdateFn = func(context.Context, string) (string, error) {
				t.Fatal("check or JSON mode attempted a self update")
				return "", nil
			}
			brewCommandRunner = func(name string, args ...string) *exec.Cmd {
				t.Fatalf("unexpected command: %s %v", name, args)
				return nil
			}
			var stdout string
			var upgradeErr error
			stderr := captureStdoutAndStderr(t, func() {
				stdout = captureStdout(t, func() {
					upgradeErr = runUpgrade(newTestCommand(), nil)
				})
			})
			if upgradeErr != nil {
				t.Fatalf("runUpgrade() error = %v", upgradeErr)
			}
			if strings.Contains(stdout+stderr, "revyl skill update") {
				t.Fatalf("unexpected skill notice without an upgrade: %s%s", stdout, stderr)
			}
			if jsonOutput {
				var result UpgradeResult
				if err := json.Unmarshal([]byte(stdout), &result); err != nil {
					t.Fatalf("invalid JSON result %q: %v", stdout, err)
				}
				if !result.UpdateAvailable || stderr != "" {
					t.Fatalf("result = %#v, stderr = %q; want available update and no stderr", result, stderr)
				}
			}
			if !reflect.DeepEqual(snapshotUpgradeSkillTree(t, workDir), before) {
				t.Fatal("upgrade check modified skills")
			}
		})
	}
}

func writeUpgradeSkillFixture(t *testing.T, skillsDir, name string) {
	t.Helper()
	skillDir := filepath.Join(skillsDir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, filename := range []string{"SKILL.md", "custom-recipe.md"} {
		if err := os.WriteFile(filepath.Join(skillDir, filename), []byte("Customized "+name+" "+filename+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func snapshotUpgradeSkillTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		var content []byte
		if info.IsDir() || info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			info, err = file.Stat()
			if err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				content, err = io.ReadAll(file)
				if err != nil {
					return err
				}
			}
		}
		snapshot[path] = fmt.Sprintf("%s\x00%s\x00%s", info.Mode(), info.ModTime().UTC().Format(time.RFC3339Nano), content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
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

func TestDownloadBinaryRespectsContextDeadlineNotReleaseCheckTimeout(t *testing.T) {
	// Trickle the body so the transfer outlasts a short context deadline but
	// finishes well within downloadBinary's own 5-minute client timeout.
	payload := bytes.Repeat([]byte("x"), 8*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		flusher, _ := w.(http.Flusher)
		const chunk = 1024
		for i := 0; i < len(payload); i += chunk {
			end := i + chunk
			if end > len(payload) {
				end = len(payload)
			}
			_, _ = w.Write(payload[i:end])
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(5 * time.Millisecond)
		}
	}))
	defer server.Close()

	// The bug: the download shared the 30s release-check context. Any short
	// deadline aborts the slow transfer mid-stream.
	shortCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if path, err := downloadBinary(shortCtx, server.URL); err == nil {
		os.Remove(path)
		t.Fatal("expected download to fail under a short context deadline")
	}

	// The fix: with the root context the client's own timeout governs, so the
	// slow transfer completes.
	path, err := downloadBinary(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("download under root context failed: %v", err)
	}
	defer os.Remove(path)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temp file: %v", err)
	}
	if len(got) != len(payload) {
		t.Fatalf("downloaded %d bytes, want %d", len(got), len(payload))
	}
}
