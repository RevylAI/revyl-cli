package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func failOnUpgradePrompt(t *testing.T) {
	t.Helper()
	confirmInlineUpgrade = func(string, bool) (bool, error) {
		t.Fatal("non-interactive run must not prompt for an upgrade")
		return false, nil
	}
}

func TestDailyUpdateNoticeNamesReleaseCurrentVersionAndInstallCommand(t *testing.T) {
	for method, command := range map[string]string{
		"homebrew": "brew upgrade revyl",
		"pip":      "pip install --upgrade revyl",
		"pipx":     "pipx upgrade revyl",
		"uv":       "uv tool upgrade revyl",
		"direct":   "revyl upgrade",
	} {
		t.Run(method, func(t *testing.T) {
			prepareUpgradePromptTest(t)
			failOnUpgradePrompt(t)
			upgradeTerminalAvailable = func() bool { return false }
			versionCheckOutput.InstallMethod = method

			stdout, stderr := captureStdoutAndStderrSeparate(t, func() { printVersionWarning(&cobra.Command{Use: "example"}) })

			want := fmt.Sprintf("Revyl CLI 999.0.0 is available (current %s). Upgrade with: %s", strings.TrimPrefix(version, "v"), command)
			if stdout != "" || strings.Count(stderr, "\n") != 1 || !strings.Contains(stderr, want) {
				t.Fatalf("stdout = %q, stderr = %q; want only the stderr line %q", stdout, stderr, want)
			}
		})
	}
}

func TestNonInteractiveRunsGetDailyNoticeInsteadOfPrompt(t *testing.T) {
	for _, name := range []string{"ci", "agent", "not_a_terminal", "json", "quiet"} {
		t.Run(name, func(t *testing.T) {
			prepareUpgradePromptTest(t)
			failOnUpgradePrompt(t)
			cmd := &cobra.Command{Use: "example"}
			cmd.Flags().Bool("json", name == "json", "")
			cmd.Flags().Bool("quiet", name == "quiet", "")
			switch name {
			case "ci":
				t.Setenv("CI", "true")
			case "agent":
				t.Setenv("CLAUDECODE", "1")
			case "not_a_terminal":
				upgradeTerminalAvailable = func() bool { return false }
			}

			first := captureStdoutAndStderr(t, func() { printVersionWarning(cmd) })
			second := captureStdoutAndStderr(t, func() { printVersionWarning(cmd) })

			if strings.Count(first, "\n") != 1 || !strings.Contains(first, "Upgrade with: revyl upgrade") {
				t.Fatalf("first run output = %q, want one notice line", first)
			}
			if second != "" {
				t.Fatalf("second run output = %q, want no repeat within the interval", second)
			}
		})
	}
}

func TestDailyUpdateNoticeNeverWaitsForAnInFlightCheck(t *testing.T) {
	prepareUpgradePromptTest(t)
	failOnUpgradePrompt(t)
	upgradeTerminalAvailable = func() bool { return false }
	versionCheckDone = make(chan struct{})

	started := time.Now()
	output := captureStdoutAndStderr(t, func() { printVersionWarning(&cobra.Command{Use: "example"}) })
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("notice waited %v for the release check", elapsed)
	}
	if output != "" {
		t.Fatalf("output = %q, want nothing before the check completes", output)
	}

	close(versionCheckDone)
	if output := captureStdoutAndStderr(t, func() { printVersionWarning(&cobra.Command{Use: "example"}) }); !strings.Contains(output, "Revyl CLI 999.0.0 is available") {
		t.Fatalf("skipped notice was consumed; next run output = %q", output)
	}
}

func TestClaimUpdateNoticeAllowsOneNoticePerInterval(t *testing.T) {
	prepareUpgradePromptTest(t)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	for _, step := range []struct {
		at   time.Time
		want bool
	}{
		{at: now, want: true},
		{at: now.Add(time.Hour), want: false},
		{at: now.Add(updateNoticeInterval - time.Second), want: false},
		{at: now.Add(updateNoticeInterval), want: true},
		{at: now, want: true},
	} {
		if got := claimUpdateNotice(step.at, updateAvailableShownAt); got != step.want {
			t.Fatalf("claimUpdateNotice(%s) = %v, want %v", step.at, got, step.want)
		}
	}

	data, err := os.ReadFile(revylStateFilePath(updateNoticeFile))
	if err != nil {
		t.Fatalf("read notice state: %v", err)
	}
	var state updateNoticeState
	if err := json.Unmarshal(data, &state); err != nil || !state.UpdateAvailableShownAt.Equal(now) {
		t.Fatalf("notice state = %s (%v), want last shown at %s", data, err, now)
	}
}

func TestClaimUpdateNoticeAllowsOneOfManyConcurrentInvocations(t *testing.T) {
	prepareUpgradePromptTest(t)
	now := time.Now()

	var claimed atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if claimUpdateNotice(now, updateAvailableShownAt) {
				claimed.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := claimed.Load(); got != 1 {
		t.Fatalf("%d concurrent invocations claimed the notice, want exactly 1", got)
	}
}

func TestClaimUpdateNoticeYieldsToHeldLockAndClearsStaleLock(t *testing.T) {
	prepareUpgradePromptTest(t)
	lockPath := revylStateFilePath(updateNoticeFile) + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	if claimUpdateNotice(now, updateAvailableShownAt) {
		t.Fatal("claimed the notice while another invocation holds the lock")
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("held lock was removed: %v", err)
	}

	stale := now.Add(-2 * updateNoticeLockStaleAfter)
	if err := os.Chtimes(lockPath, stale, stale); err != nil {
		t.Fatal(err)
	}
	if claimUpdateNotice(now, updateAvailableShownAt) {
		t.Fatal("claimed the notice in the same invocation that cleared a stale lock")
	}
	if !claimUpdateNotice(now, updateAvailableShownAt) {
		t.Fatal("notice stayed blocked after the stale lock was cleared")
	}
}

// startVersionCheckForTest runs one invocation's version check to completion.
func startVersionCheckForTest(t *testing.T, currentVersion string) {
	t.Helper()
	t.Cleanup(func() { versionCheckOnce = sync.Once{} })
	versionCheckOnce = sync.Once{}
	versionCheckDone = make(chan struct{})
	startVersionCheck(currentVersion)
	select {
	case <-versionCheckDone:
	case <-time.After(10 * time.Second):
		t.Fatal("version check did not finish")
	}
}

func TestStartVersionCheckAnswersFromFreshCacheWithoutWaiting(t *testing.T) {
	prepareUpgradePromptTest(t)
	versionCheckOutput = nil
	writeVersionCache(revylStateFilePath(versionCacheFile), versionCheckCache{LastChecked: time.Now(), LatestVersion: "v0.2.0"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("fresh cache triggered a release lookup: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	configureGitHubTestRequest(t, server.URL)
	t.Cleanup(func() { versionCheckOnce = sync.Once{} })
	versionCheckOnce = sync.Once{}
	versionCheckDone = make(chan struct{})

	startVersionCheck("0.1.0")

	select {
	case <-versionCheckDone:
	default:
		t.Fatal("a fresh cache must answer before startVersionCheck returns")
	}
	if versionCheckOutput == nil || versionCheckOutput.LatestVersion != "v0.2.0" {
		t.Fatalf("update result = %+v, want cached v0.2.0", versionCheckOutput)
	}
}

func TestRefreshVersionCheckCachesFailedLookupAndKeepsLastKnownRelease(t *testing.T) {
	prepareUpgradePromptTest(t)
	versionCheckOutput = nil
	cachePath := revylStateFilePath(versionCacheFile)
	writeVersionCache(cachePath, versionCheckCache{LastChecked: time.Now().Add(-48 * time.Hour), LatestVersion: "v0.2.0"})

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	configureGitHubTestRequest(t, server.URL)

	startVersionCheckForTest(t, "0.1.0")
	firstRequests := requests.Load()
	startVersionCheckForTest(t, "0.1.0")

	if firstRequests == 0 || requests.Load() != firstRequests {
		t.Fatalf("requests = %d then %d, want a failed lookup to back off until the next interval", firstRequests, requests.Load())
	}
	cached, err := readVersionCache(cachePath)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if time.Since(cached.LastChecked) > time.Minute || cached.LatestVersion != "v0.2.0" {
		t.Fatalf("cache = %+v, want a fresh check time and the last known release", cached)
	}
	if versionCheckOutput == nil || versionCheckOutput.LatestVersion != "v0.2.0" {
		t.Fatalf("update result = %+v, want the last known release", versionCheckOutput)
	}
}

func TestRefreshVersionCheckFailureKeepsConcurrentlyRefreshedCache(t *testing.T) {
	prepareUpgradePromptTest(t)
	versionCheckOutput = nil
	cachePath := revylStateFilePath(versionCacheFile)
	writeVersionCache(cachePath, versionCheckCache{LastChecked: time.Now().Add(-48 * time.Hour), LatestVersion: "v0.2.0"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeVersionCache(cachePath, versionCheckCache{LastChecked: time.Now(), LatestVersion: "v0.3.0"})
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	configureGitHubTestRequest(t, server.URL)

	refreshVersionCheck("0.1.0")

	cached, err := readVersionCache(cachePath)
	if err != nil || cached.LatestVersion != "v0.3.0" {
		t.Fatalf("cache = %+v (%v), want the concurrently refreshed v0.3.0", cached, err)
	}
	if versionCheckOutput == nil || versionCheckOutput.LatestVersion != "v0.3.0" {
		t.Fatalf("update result = %+v, want v0.3.0", versionCheckOutput)
	}
}

func TestRefreshVersionCheckResolvesReleaseWithoutGitHubAPI(t *testing.T) {
	prepareUpgradePromptTest(t)
	versionCheckOutput = nil
	serveLatestReleaseRedirect(t, "v0.3.0")

	refreshVersionCheck("0.1.0")

	cached, err := readVersionCache(revylStateFilePath(versionCacheFile))
	if err != nil || cached.LatestVersion != "v0.3.0" {
		t.Fatalf("cache = %+v (%v), want v0.3.0", cached, err)
	}
	if versionCheckOutput == nil || !versionCheckOutput.UpdateAvailable || versionCheckOutput.LatestVersion != "v0.3.0" {
		t.Fatalf("update result = %+v, want v0.3.0 available", versionCheckOutput)
	}
}

func announceUpcomingMinimum(t *testing.T, current, announced string) {
	t.Helper()
	oldVersion := version
	t.Cleanup(func() { version = oldVersion })
	version = current
	announcedMinimumCLIVersion = func() string { return announced }
}

func TestUpcomingMinimumWarningNamesCutoffAndUpgradeCommandOncePerDay(t *testing.T) {
	prepareUpgradePromptTest(t)
	failOnUpgradePrompt(t)
	upgradeTerminalAvailable = func() bool { return false }
	announceUpcomingMinimum(t, "v0.1.100", "0.1.120")
	want := "Revyl CLI 0.1.100 will soon stop working: the Revyl API will require 0.1.120 or later. Upgrade now with: " +
		upgradeCommandForInstallMethod(detectInstallMethod())

	first := captureStdoutAndStderr(t, func() { printVersionWarning(&cobra.Command{Use: "example"}) })
	second := captureStdoutAndStderr(t, func() { printVersionWarning(&cobra.Command{Use: "example"}) })
	third := captureStdoutAndStderr(t, func() { printVersionWarning(&cobra.Command{Use: "example"}) })

	if strings.Count(first, "\n") != 1 || !strings.Contains(first, want) {
		t.Fatalf("first run output = %q, want only the cutoff warning %q", first, want)
	}
	if strings.Contains(second, "will soon stop working") || !strings.Contains(second, "Revyl CLI 999.0.0 is available") {
		t.Fatalf("second run output = %q, want the regular notice and no repeated cutoff warning", second)
	}
	if third != "" {
		t.Fatalf("third run output = %q, want nothing more within the interval", third)
	}
}

func TestUpcomingMinimumWarningHasNothingToSayWithoutACurrentAnnouncement(t *testing.T) {
	for _, test := range []struct {
		name      string
		current   string
		announced string
	}{
		{name: "no header", current: "0.1.100", announced: ""},
		{name: "already at minimum", current: "0.1.120", announced: "0.1.120"},
		{name: "newer than minimum", current: "v0.2.0", announced: "0.1.120"},
		{name: "development build", current: "dev", announced: "0.1.120"},
	} {
		t.Run(test.name, func(t *testing.T) {
			prepareUpgradePromptTest(t)
			announceUpcomingMinimum(t, test.current, test.announced)
			if printUpcomingMinimumWarning() {
				t.Fatal("warned without an announced minimum above the running version")
			}
		})
	}
}

func TestUpcomingMinimumWarningRespectsOptOutAndSkippedCommands(t *testing.T) {
	for _, name := range []string{"disabled", "mcp", "version"} {
		t.Run(name, func(t *testing.T) {
			prepareUpgradePromptTest(t)
			failOnUpgradePrompt(t)
			announceUpcomingMinimum(t, "0.1.100", "0.1.120")
			cmd := &cobra.Command{Use: "example"}
			if name == "disabled" {
				t.Setenv("REVYL_NO_UPDATE_NOTIFIER", "1")
			} else {
				cmd.Use = name
			}
			if output := captureStdoutAndStderr(t, func() { printVersionWarning(cmd) }); output != "" {
				t.Fatalf("unexpected output: %q", output)
			}
		})
	}
}

func TestUpcomingMinimumWarningPrecedesInteractivePrompt(t *testing.T) {
	prepareUpgradePromptTest(t)
	announceUpcomingMinimum(t, "0.1.100", "0.1.120")
	prompts := 0
	confirmInlineUpgrade = func(string, bool) (bool, error) {
		prompts++
		return false, nil
	}

	output := captureStdoutAndStderr(t, func() { printVersionWarning(&cobra.Command{Use: "example"}) })

	warning := strings.Index(output, "will soon stop working")
	notice := strings.Index(output, "A new version of Revyl CLI is available")
	if warning < 0 || notice < warning || prompts != 1 {
		t.Fatalf("output = %q, prompts = %d; want the cutoff warning, then the usual notice and prompt", output, prompts)
	}
}

func TestJSONStdoutIsIdenticalWithAndWithoutUpcomingMinimumWarning(t *testing.T) {
	prepareUpgradePromptTest(t)
	failOnUpgradePrompt(t)
	versionCheckOutput.UpdateAvailable = false
	run := func(announced string) (string, string) {
		announceUpcomingMinimum(t, "0.1.100", announced)
		root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
		root.PersistentFlags().Bool("json", false, "")
		root.AddCommand(&cobra.Command{Use: "run", RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(os.Stdout, `{"status":"ok"}`)
			return nil
		}})
		root.SetArgs([]string{"run", "--json"})
		return captureStdoutAndStderrSeparate(t, func() {
			if err := executeWithVersionNotice(root); err != nil {
				t.Fatalf("command failed: %v", err)
			}
		})
	}

	withoutStdout, withoutStderr := run("")
	withStdout, withStderr := run("0.1.120")

	if withoutStdout != "{\"status\":\"ok\"}\n" || withStdout != withoutStdout {
		t.Fatalf("JSON stdout changed: %q vs %q", withoutStdout, withStdout)
	}
	if withoutStderr != "" || strings.Count(withStderr, "\n") != 1 || !strings.Contains(withStderr, "will soon stop working") {
		t.Fatalf("stderr = %q / %q, want only one cutoff warning line", withoutStderr, withStderr)
	}
}
