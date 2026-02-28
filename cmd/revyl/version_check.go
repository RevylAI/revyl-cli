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

	"github.com/revyl/cli/internal/ui"
)

const (
	// versionCheckInterval is how often we check for updates (24 hours).
	versionCheckInterval = 24 * time.Hour

	// versionCheckTimeout is the max time for the background HTTP call.
	versionCheckTimeout = 5 * time.Second

	// versionCacheFile is the filename for the cached check result.
	versionCacheFile = "version-check.json"
)

// versionCheckCache stores the result of the last version check.
type versionCheckCache struct {
	LastChecked   time.Time `json:"last_checked"`
	LatestVersion string    `json:"latest_version"`
}

// versionCheckResult holds the outcome of a background check.
type versionCheckResult struct {
	UpdateAvailable bool
	LatestVersion   string
	InstallMethod   string
}

var (
	// versionCheckOnce ensures we only start one background check per invocation.
	versionCheckOnce sync.Once

	// versionCheckDone is closed when the background check completes.
	versionCheckDone = make(chan struct{})

	// versionCheckOutput holds the result (if any) for printing after the command.
	versionCheckOutput *versionCheckResult
)

// skipVersionCheckCommands lists commands that should not trigger a version check.
var skipVersionCheckCommands = map[string]bool{
	"upgrade":    true,
	"update":     true,
	"version":    true,
	"completion": true,
}

// startVersionCheck kicks off a background version check (non-blocking).
// It reads the cache first — if a check was done within versionCheckInterval,
// it uses the cached result. Otherwise it fetches from GitHub.
//
// Respects the REVYL_NO_UPDATE_NOTIFIER environment variable — if set to any
// non-empty value, the check is skipped entirely.
func startVersionCheck(currentVersion string) {
	if os.Getenv("REVYL_NO_UPDATE_NOTIFIER") != "" {
		close(versionCheckDone) // unblock printVersionWarning
		return
	}

	versionCheckOnce.Do(func() {
		go func() {
			defer close(versionCheckDone)
			doVersionCheck(currentVersion)
		}()
	})
}

// doVersionCheck performs the actual check (cache read or HTTP fetch).
func doVersionCheck(currentVersion string) {
	currentClean := strings.TrimPrefix(currentVersion, "v")
	if currentClean == "" || currentClean == "dev" {
		return // Don't check for dev builds
	}

	cachePath := versionCheckCachePath()

	// Try reading cache first
	if cached, err := readVersionCache(cachePath); err == nil {
		if time.Since(cached.LastChecked) < versionCheckInterval {
			// Cache is fresh — use it
			latestClean := strings.TrimPrefix(cached.LatestVersion, "v")
			if latestClean != "" && compareSemver(currentClean, latestClean) < 0 {
				versionCheckOutput = &versionCheckResult{
					UpdateAvailable: true,
					LatestVersion:   cached.LatestVersion,
					InstallMethod:   detectInstallMethod(),
				}
			}
			return
		}
	}

	// Cache is stale or missing — fetch from GitHub
	ctx, cancel := context.WithTimeout(context.Background(), versionCheckTimeout)
	defer cancel()

	release, err := fetchLatestRelease(ctx, false)
	if err != nil {
		log.Debug("Background version check failed", "error", err)
		return
	}

	// Write cache regardless of result
	writeVersionCache(cachePath, versionCheckCache{
		LastChecked:   time.Now(),
		LatestVersion: release.TagName,
	})

	latestClean := strings.TrimPrefix(release.TagName, "v")
	if compareSemver(currentClean, latestClean) < 0 {
		versionCheckOutput = &versionCheckResult{
			UpdateAvailable: true,
			LatestVersion:   release.TagName,
			InstallMethod:   detectInstallMethod(),
		}
	}
}

// printVersionWarning prints an update notice to stderr if the background
// check found a newer version. This is called in PersistentPostRun so the
// warning appears after the command's normal output.
func printVersionWarning() {
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

	switch versionCheckOutput.InstallMethod {
	case "homebrew":
		ui.PrintDim("  Update with: brew upgrade revyl")
	case "npm":
		ui.PrintDim("  Update with: npm update -g @revyl/cli")
	case "pip":
		ui.PrintDim("  Update with: pip install --upgrade revyl")
	default:
		ui.PrintDim("  Update with: revyl upgrade")
	}
}

// versionCheckCachePath returns the path to the version check cache file.
func versionCheckCachePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".revyl", versionCacheFile)
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

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Debug("Failed to create cache directory", "error", err)
		return
	}

	data, err := json.Marshal(cache)
	if err != nil {
		log.Debug("Failed to marshal version cache", "error", err)
		return
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Debug("Failed to write version cache", "error", err)
	}
}
