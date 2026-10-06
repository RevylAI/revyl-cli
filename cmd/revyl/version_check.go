// Package main provides a background version check that warns users
// when a newer CLI version is available.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

const (
	// versionCheckInterval is how often we check for updates (24 hours).
	// A failed check is cached for the same interval so an offline or
	// rate-limited machine does not retry on every command.
	versionCheckInterval = 24 * time.Hour

	// versionCheckTimeout is the max time for the background HTTP call.
	versionCheckTimeout = 5 * time.Second

	// versionCacheFile is the filename for the cached check result.
	versionCacheFile = "version-check.json"

	// updateNoticeFile records when the non-interactive update notice was last shown.
	updateNoticeFile = "update-notice.json"

	// updateNoticeInterval is the minimum spacing between non-interactive
	// update notices on one machine.
	updateNoticeInterval = 24 * time.Hour

	// updateNoticeLockStaleAfter bounds how long a lock left behind by a
	// crashed invocation can suppress notices.
	updateNoticeLockStaleAfter = time.Minute
)

// versionCheckCache stores the result of the last version check.
type versionCheckCache struct {
	LastChecked   time.Time `json:"last_checked"`
	LatestVersion string    `json:"latest_version"`
}

// updateNoticeState stores when each daily update notice was last shown.
type updateNoticeState struct {
	UpdateAvailableShownAt time.Time `json:"update_available_shown_at"`
	UpcomingMinimumShownAt time.Time `json:"upcoming_minimum_shown_at"`
}

func updateAvailableShownAt(state *updateNoticeState) *time.Time {
	return &state.UpdateAvailableShownAt
}

func upcomingMinimumShownAt(state *updateNoticeState) *time.Time {
	return &state.UpcomingMinimumShownAt
}

// versionCheckResult holds the outcome of a background check.
type versionCheckResult struct {
	UpdateAvailable bool
	LatestVersion   string
	InstallMethod   string
}

var (
	// versionCheckOnce ensures we only start one background check per invocation.
	versionCheckOnce    sync.Once
	versionCheckStarted bool

	// versionCheckDone is closed when the background check completes.
	versionCheckDone = make(chan struct{})

	// versionCheckOutput holds the result (if any) for printing after the command.
	versionCheckOutput *versionCheckResult

	// announcedMinimumCLIVersion returns the minimum version a backend
	// response announced for an upcoming compatibility cutoff. Overridden in tests.
	announcedMinimumCLIVersion = api.UpcomingMinimumVersion
)

// skipVersionCheckCommands lists commands that should not trigger a version check.
var skipVersionCheckCommands = map[string]bool{
	"upgrade":    true,
	"update":     true,
	"version":    true,
	"completion": true,
	"mcp":        true,
}

// shouldSkipVersionCheck reports whether the invoked command must never check
// for or mention CLI updates. Output modes such as --json and --quiet do not
// skip the check; they only change how the notice is printed.
func shouldSkipVersionCheck(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	for current := cmd; current != nil; current = current.Parent() {
		if skipVersionCheckCommands[current.Name()] {
			return true
		}
	}
	if versionFlag, err := cmd.Flags().GetBool("version"); err == nil && versionFlag {
		return true
	}
	if versionFlag, err := cmd.Root().Flags().GetBool("version"); err == nil && versionFlag {
		return true
	}
	return false
}

// startVersionCheck answers from a fresh cache immediately, so a cached
// result never depends on how long the command runs, and otherwise resolves
// the latest release in the background (non-blocking).
//
// Respects the REVYL_NO_UPDATE_NOTIFIER environment variable — if set to any
// non-empty value, the check is skipped entirely.
func startVersionCheck(currentVersion string) {
	if os.Getenv("REVYL_NO_UPDATE_NOTIFIER") != "" {
		return
	}

	versionCheckOnce.Do(func() {
		versionCheckStarted = true
		if versionCheckAnsweredFromCache(currentVersion) {
			close(versionCheckDone)
			return
		}
		go func() {
			defer close(versionCheckDone)
			refreshVersionCheck(currentVersion)
		}()
	})
}

// versionCheckAnsweredFromCache records a cached result and reports true when
// no release lookup is needed: a development build, or a cache younger than
// versionCheckInterval.
func versionCheckAnsweredFromCache(currentVersion string) bool {
	currentClean := strings.TrimPrefix(currentVersion, "v")
	if currentClean == "" || currentClean == "dev" {
		return true
	}
	cached, err := readVersionCache(revylStateFilePath(versionCacheFile))
	if err != nil || time.Since(cached.LastChecked) >= versionCheckInterval {
		return false
	}
	recordAvailableUpdate(currentClean, cached.LatestVersion)
	return true
}

// refreshVersionCheck resolves the latest release and caches the outcome. A
// failed lookup is cached as well, keeping the last known release, so the
// next attempt waits a full versionCheckInterval; it never replaces a cache
// that a concurrent invocation refreshed while this lookup was failing.
func refreshVersionCheck(currentVersion string) {
	currentClean := strings.TrimPrefix(currentVersion, "v")
	cachePath := revylStateFilePath(versionCacheFile)
	latestVersion := ""
	if cached, err := readVersionCache(cachePath); err == nil {
		latestVersion = cached.LatestVersion
	}

	ctx, cancel := context.WithTimeout(context.Background(), versionCheckTimeout)
	defer cancel()
	if release, err := fetchLatestRelease(ctx, false); err != nil {
		log.Debug("Background version check failed; retrying after the check interval", "error", err)
		if cached, readErr := readVersionCache(cachePath); readErr == nil && time.Since(cached.LastChecked) < versionCheckInterval {
			recordAvailableUpdate(currentClean, cached.LatestVersion)
			return
		}
	} else {
		latestVersion = release.TagName
	}

	writeVersionCache(cachePath, versionCheckCache{
		LastChecked:   time.Now(),
		LatestVersion: latestVersion,
	})
	recordAvailableUpdate(currentClean, latestVersion)
}

func recordAvailableUpdate(currentClean, latestVersion string) {
	latestClean := strings.TrimPrefix(latestVersion, "v")
	if latestClean == "" || compareSemver(currentClean, latestClean) >= 0 {
		return
	}
	versionCheckOutput = &versionCheckResult{
		UpdateAvailable: true,
		LatestVersion:   latestVersion,
		InstallMethod:   detectInstallMethod(),
	}
}

func printVersionWarning(cmd *cobra.Command) {
	if cmd == nil || shouldSkipVersionCheck(cmd) || os.Getenv("REVYL_NO_UPDATE_NOTIFIER") != "" {
		return
	}
	if ctx := cmd.Context(); ctx != nil && ctx.Err() != nil {
		return
	}
	warnedAboutCutoff := printUpcomingMinimumWarning()
	if !versionCheckStarted {
		return
	}
	if !isInteractiveUpdateSession(cmd) {
		if !warnedAboutCutoff {
			printDailyUpdateNotice()
		}
		return
	}
	// Wait for the background check to finish (with a short timeout
	// so we never block the user for long).
	select {
	case <-versionCheckDone:
	case <-time.After(2 * time.Second):
		return // Don't block the user
	}

	if versionCheckOutput == nil || !versionCheckOutput.UpdateAvailable {
		return
	}

	ui.Println()
	ui.PrintWarning("A new version of Revyl CLI is available: %s (current: %s)", versionCheckOutput.LatestVersion, version)
	if canPromptForUpgrade() && (versionCheckOutput.InstallMethod == "direct" || versionCheckOutput.InstallMethod == "homebrew") {
		previousContext := upgradeCmd.Context()
		upgradeCmd.SetContext(cmd.Context())
		defer upgradeCmd.SetContext(previousContext)
		if err := runWithAnalytics(upgradeCmd, nil, func() error {
			return runPromptedUpgrade(upgradeCmd, *versionCheckOutput)
		}); err != nil {
			ui.PrintWarning("Update did not complete: %v", err)
		}
		return
	}

	ui.PrintDim("  Update with: %s", upgradeCommandForInstallMethod(versionCheckOutput.InstallMethod))
}

// isInteractiveUpdateSession reports whether a person at a terminal can take
// the inline upgrade prompt. Every other run (--json, --quiet, CI, coding
// agents, piped or redirected output) gets the daily one-line notice instead.
func isInteractiveUpdateSession(cmd *cobra.Command) bool {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	quiet, _ := cmd.Flags().GetBool("quiet")
	return !jsonOutput && !quiet && canPromptForUpgrade()
}

// printDailyUpdateNotice prints one stderr line naming the available release
// and the upgrade command for this installation, at most once per
// updateNoticeInterval per machine. It never waits for an in-flight release
// check, so it cannot delay the command; stdout is never written.
func printDailyUpdateNotice() {
	select {
	case <-versionCheckDone:
	default:
		return
	}
	update := versionCheckOutput
	if update == nil || !update.UpdateAvailable || !claimUpdateNotice(time.Now(), updateAvailableShownAt) {
		return
	}
	ui.PrintWarning(
		"Revyl CLI %s is available (current %s). Upgrade with: %s",
		strings.TrimPrefix(update.LatestVersion, "v"),
		strings.TrimPrefix(version, "v"),
		upgradeCommandForInstallMethod(update.InstallMethod),
	)
}

// printUpcomingMinimumWarning warns, at most once per updateNoticeInterval per
// machine and in every output mode, when a backend response announced that an
// upcoming compatibility cutoff will reject this CLI version. A response
// without the announcement means there is nothing to say. It reports whether
// the warning was printed.
func printUpcomingMinimumWarning() bool {
	announced := announcedMinimumCLIVersion()
	current := strings.TrimPrefix(version, "v")
	if announced == "" || current == "" || current == "dev" || compareSemver(current, announced) >= 0 {
		return false
	}
	if !claimUpdateNotice(time.Now(), upcomingMinimumShownAt) {
		return false
	}
	ui.PrintWarning(
		"Revyl CLI %s will soon stop working: the Revyl API will require %s or later. Upgrade now with: %s",
		current,
		announced,
		upgradeCommandForInstallMethod(detectInstallMethod()),
	)
	return true
}

// claimUpdateNotice records now as the last time of the notice that shownAt
// selects and reports whether the caller may print, which is only when that
// notice was not shown within updateNoticeInterval and the new time was
// persisted. A lock file serializes concurrent invocations, so parallel agent
// commands print at most one notice; an invocation that cannot take the lock
// stays silent.
func claimUpdateNotice(now time.Time, shownAt func(*updateNoticeState) *time.Time) bool {
	statePath := revylStateFilePath(updateNoticeFile)
	if statePath == "" {
		return false
	}
	release, locked := lockUpdateNoticeState(statePath+".lock", now)
	if !locked {
		return false
	}
	defer release()

	var state updateNoticeState
	data, readErr := os.ReadFile(statePath) // #nosec G304 -- fixed file name inside the CLI state directory
	if readErr == nil {
		if err := json.Unmarshal(data, &state); err != nil {
			log.Debug("Ignoring unreadable update notice state", "error", err)
		}
	}
	lastShown := shownAt(&state)
	if elapsed := now.Sub(*lastShown); elapsed >= 0 && elapsed < updateNoticeInterval {
		return false
	}
	*lastShown = now
	if err := writeStateFileAtomically(statePath, state); err != nil {
		log.Debug("Failed to record update notice time", "error", err)
		return false
	}
	return true
}

// lockUpdateNoticeState takes an exclusive lock file without waiting. A lock
// older than updateNoticeLockStaleAfter is removed so a later invocation can
// take it.
func lockUpdateNoticeState(lockPath string, now time.Time) (func(), bool) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, false
	}
	file, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- fixed file name inside the CLI state directory
	if err != nil {
		if info, statErr := os.Stat(lockPath); statErr == nil && now.Sub(info.ModTime()) > updateNoticeLockStaleAfter {
			_ = os.Remove(lockPath)
		}
		return nil, false
	}
	_ = file.Close()
	return func() { _ = os.Remove(lockPath) }, true
}

// revylStateFilePath returns the path of name inside the CLI state directory (~/.revyl).
func revylStateFilePath(name string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".revyl", name)
}

// readVersionCache reads the cached version check result.
func readVersionCache(path string) (*versionCheckCache, error) {
	if path == "" {
		return nil, os.ErrNotExist
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cache versionCheckCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}

	return &cache, nil
}

// compareSemver compares two semver version strings (without "v" prefix).
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
// Pre-release suffixes (e.g., "-rc.1") are stripped for comparison.
func compareSemver(a, b string) int {
	// Strip pre-release suffix
	if idx := strings.Index(a, "-"); idx != -1 {
		a = a[:idx]
	}
	if idx := strings.Index(b, "-"); idx != -1 {
		b = b[:idx]
	}

	aParts := strings.SplitN(a, ".", 3)
	bParts := strings.SplitN(b, ".", 3)

	for i := 0; i < 3; i++ {
		var ai, bi int
		if i < len(aParts) {
			ai, _ = strconv.Atoi(aParts[i])
		}
		if i < len(bParts) {
			bi, _ = strconv.Atoi(bParts[i])
		}
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	return 0
}

// writeVersionCache writes the version check result to the cache file.
func writeVersionCache(path string, cache versionCheckCache) {
	if path == "" {
		return
	}
	if err := writeStateFileAtomically(path, cache); err != nil {
		log.Debug("Failed to write version cache", "error", err)
	}
}

// writeStateFileAtomically replaces path with value's JSON encoding, so a
// concurrent invocation never reads a partially written file.
func writeStateFileAtomically(path string, value any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}
