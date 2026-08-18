// Command plugin-version-guard fails when plugin-owned files change without a pin.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/revyl/cli/internal/cursorpluginrelease"
)

const defaultPluginJSONPath = "revyl-cli/cursor-plugin/.cursor-plugin/plugin.json"

// main evaluates the plugin pin guard and exits with its status.
func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help") {
		fmt.Print(usage())
		return
	}

	input, err := guardInputFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	result := cursorpluginrelease.EvaluateGuard(input)
	if result.Stdout != "" {
		fmt.Fprint(os.Stdout, result.Stdout)
	}
	if result.Stderr != "" {
		fmt.Fprint(os.Stderr, result.Stderr)
	}
	os.Exit(result.ExitCode)
}

// usage prints how CI and tests invoke this guard.
func usage() string {
	return `Usage: plugin-version-guard

Git mode (CI): BASE_SHA, HEAD_SHA, optional PR_LABELS JSON array.
Test mode: CHANGED_FILES, BASE_PLUGIN_VERSION, HEAD_PLUGIN_VERSION.
`
}

// guardInputFromEnv reads CI or test-mode environment into a guard decision.
//
// Returns:
//   - cursorpluginrelease.GuardInput: Changed files, versions, and labels.
//   - error: Missing required env, git failure, or unreadable plugin.json.
func guardInputFromEnv() (cursorpluginrelease.GuardInput, error) {
	input := cursorpluginrelease.GuardInput{
		LabelsJSON: os.Getenv("PR_LABELS"),
	}
	if _, testMode := os.LookupEnv("CHANGED_FILES"); testMode {
		input.ChangedFiles = os.Getenv("CHANGED_FILES")
		input.BaseVersion = os.Getenv("BASE_PLUGIN_VERSION")
		input.HeadVersion = os.Getenv("HEAD_PLUGIN_VERSION")
		if input.BaseVersion == "" || input.HeadVersion == "" {
			return cursorpluginrelease.GuardInput{}, fmt.Errorf(
				"test mode requires BASE_PLUGIN_VERSION and HEAD_PLUGIN_VERSION",
			)
		}
		return input, nil
	}

	baseSHA := os.Getenv("BASE_SHA")
	headSHA := os.Getenv("HEAD_SHA")
	if baseSHA == "" || headSHA == "" {
		return cursorpluginrelease.GuardInput{}, fmt.Errorf("BASE_SHA and HEAD_SHA are required")
	}

	repoRoot, err := gitOutput("rev-parse", "--show-toplevel")
	if err != nil {
		return cursorpluginrelease.GuardInput{}, err
	}
	mergeBase, err := gitOutput("merge-base", baseSHA, headSHA)
	if err != nil {
		return cursorpluginrelease.GuardInput{}, err
	}
	fmt.Printf("Comparing plugin changes from merge base %s to %s\n", mergeBase, headSHA)

	changed, err := gitOutput("diff", "--name-only", mergeBase, headSHA)
	if err != nil {
		return cursorpluginrelease.GuardInput{}, err
	}
	input.ChangedFiles = changed

	pluginRelPath := defaultPluginJSONPath
	if override := strings.TrimSpace(os.Getenv("PLUGIN_JSON_PATH")); override != "" {
		pluginRelPath = override
	}
	input.BaseVersion, err = pluginVersionAtRevision(mergeBase, pluginRelPath)
	if err != nil {
		return cursorpluginrelease.GuardInput{}, err
	}
	input.HeadVersion, err = cursorpluginrelease.ReadPluginVersion(
		filepath.Join(repoRoot, pluginRelPath),
	)
	if err != nil {
		return cursorpluginrelease.GuardInput{}, err
	}
	return input, nil
}

// pluginVersionAtRevision reads plugin.json version at a git revision, or 0.0.0.
//
// Parameters:
//   - revision: Git revision that should contain plugin.json.
//   - relPath: Repo-relative plugin.json path.
//
// Returns:
//   - string: The version, or 0.0.0 when the file is absent.
//   - error: git show or JSON decode failure for a present file.
func pluginVersionAtRevision(revision string, relPath string) (string, error) {
	command := exec.Command("git", "cat-file", "-e", revision+":"+relPath)
	if err := command.Run(); err != nil {
		return "0.0.0", nil
	}
	content, err := gitOutput("show", revision+":"+relPath)
	if err != nil {
		return "", err
	}
	var document struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(content), &document); err != nil {
		return "", fmt.Errorf("decode plugin.json at %s: %w", revision, err)
	}
	if strings.TrimSpace(document.Version) == "" {
		return "", fmt.Errorf("plugin.json at %s missing version", revision)
	}
	return document.Version, nil
}

// gitOutput runs git and returns trimmed stdout.
//
// Parameters:
//   - args: git argv after the command name.
//
// Returns:
//   - string: Trimmed stdout.
//   - error: Non-zero git exit, with stderr included.
func gitOutput(args ...string) (string, error) {
	command := exec.Command("git", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}
