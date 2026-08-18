package cursorpluginrelease

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	pluginOwnedPrefix    = "revyl-cli/cursor-plugin/"
	marketplaceOwnedPath = "revyl-cli/.cursor-plugin/marketplace.json"
	pluginJSONOwnedPath  = "revyl-cli/cursor-plugin/.cursor-plugin/plugin.json"
	runtimeManifestOwned = "revyl-cli/cursor-plugin/runtime-manifest.json"
	noPluginReleaseLabel = "no-plugin-release"
)

// IsPluginOwnedPath reports whether a repo-relative path requires a plugin pin.
//
// README, local installers, and tests under cursor-plugin/ do not require a bump.
//
// Parameters:
//   - path: A git path relative to the monorepo root.
//
// Returns:
//   - bool: True when a plugin version increase is required.
func IsPluginOwnedPath(path string) bool {
	normalized := filepath.ToSlash(strings.TrimSpace(path))
	switch normalized {
	case "revyl-cli/cursor-plugin/README.md",
		"revyl-cli/cursor-plugin/install-local.sh",
		"revyl-cli/cursor-plugin/install-local.ps1":
		return false
	case pluginJSONOwnedPath, runtimeManifestOwned, marketplaceOwnedPath:
		return true
	}
	if !strings.HasPrefix(normalized, pluginOwnedPrefix) {
		return false
	}
	if strings.HasSuffix(normalized, "_test.go") {
		return false
	}
	relative := strings.TrimPrefix(normalized, pluginOwnedPrefix)
	ownedRoots := []string{"hooks/", "skills/", "rules/", "assets/"}
	for _, root := range ownedRoots {
		if strings.HasPrefix(relative, root) {
			return true
		}
	}
	return false
}

// HasNoPluginReleaseLabel reports whether PR labels opt out of a plugin pin.
//
// Parameters:
//   - labelsJSON: A JSON array of label names. Empty is treated as [].
//
// Returns:
//   - bool: True when no-plugin-release is present.
//   - error: The labels value is not a JSON array of strings.
func HasNoPluginReleaseLabel(labelsJSON string) (bool, error) {
	raw := strings.TrimSpace(labelsJSON)
	if raw == "" {
		raw = "[]"
	}
	var labels []string
	if err := json.Unmarshal([]byte(raw), &labels); err != nil {
		return false, fmt.Errorf("PR_LABELS must be a JSON array: %w", err)
	}
	for _, label := range labels {
		if label == noPluginReleaseLabel {
			return true, nil
		}
	}
	return false, nil
}

// GuardInput is the plugin-version-guard decision input after git or test env.
type GuardInput struct {
	ChangedFiles string
	BaseVersion  string
	HeadVersion  string
	LabelsJSON   string
}

// GuardResult is the printable guard decision and process exit code.
type GuardResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// EvaluateGuard decides whether plugin-owned changes have a version increase.
//
// Parameters:
//   - input: Changed paths, versions, and optional PR labels.
//
// Returns:
//   - GuardResult: Stdout/stderr text and the process exit code.
func EvaluateGuard(input GuardInput) GuardResult {
	skip, err := HasNoPluginReleaseLabel(input.LabelsJSON)
	if err != nil {
		return GuardResult{
			ExitCode: 1,
			Stderr:   "::error::PR_LABELS must be a JSON array.\n",
		}
	}
	if skip {
		return GuardResult{
			ExitCode: 0,
			Stdout:   "PR has no-plugin-release label; skipping plugin version bump guard.\n",
		}
	}

	owned := pluginOwnedChanges(input.ChangedFiles)
	if len(owned) == 0 {
		return GuardResult{
			ExitCode: 0,
			Stdout:   "No plugin-owned files changed.\n",
		}
	}

	var stdout strings.Builder
	stdout.WriteString("Plugin-owned files changed:\n")
	for _, path := range owned {
		stdout.WriteString(path)
		stdout.WriteByte('\n')
	}

	compare, err := ComparePluginVersions(input.HeadVersion, input.BaseVersion)
	if err != nil {
		return GuardResult{
			ExitCode: 1,
			Stdout:   stdout.String(),
			Stderr:   fmt.Sprintf("::error::%v\n", err),
		}
	}
	if compare > 0 {
		stdout.WriteString(fmt.Sprintf(
			"Plugin version increased from %s to %s.\n",
			input.BaseVersion,
			input.HeadVersion,
		))
		return GuardResult{ExitCode: 0, Stdout: stdout.String()}
	}

	var stderr strings.Builder
	stderr.WriteString("::error::Plugin-owned files changed without a plugin version bump.\n")
	stderr.WriteString("::error::Run: make -C revyl-cli cursor-plugin-bump-patch\n")
	stderr.WriteString("::error::  or: cd revyl-cli && ./scripts/bump patch --plugin\n")
	stderr.WriteString("::error::Or add the no-plugin-release label if this PR must not cut a plugin pin.\n")
	stderr.WriteString(fmt.Sprintf(
		"Plugin version base=%s head=%s\n",
		input.BaseVersion,
		input.HeadVersion,
	))
	return GuardResult{
		ExitCode: 1,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}
}

// pluginOwnedChanges returns owned paths from a newline-separated file list.
func pluginOwnedChanges(changedFiles string) []string {
	owned := make([]string, 0)
	for _, path := range strings.Split(changedFiles, "\n") {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if IsPluginOwnedPath(path) {
			owned = append(owned, path)
		}
	}
	return owned
}
