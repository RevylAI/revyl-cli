// Package build provides build execution and artifact management utilities.
package build

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// GenerateVersionString generates a version string based on the current timestamp.
//
// Returns:
//   - string: A version string in the format "YYYYMMDD-HHMMSS"
func GenerateVersionString() string {
	return time.Now().Format("20060102-150405")
}

// CollectMetadata gathers build metadata including git info, machine info, and build details.
//
// Parameters:
//   - workDir: The working directory (used for git commands)
//   - buildCommand: The build command that was executed
//   - variant: The build variant name (e.g., "ios", "android", "release")
//   - duration: How long the build took
//
// Returns:
//   - map[string]interface{}: A map containing build metadata
func CollectMetadata(workDir, buildCommand, variant string, duration time.Duration) map[string]interface{} {
	metadata := make(map[string]interface{})

	// Build info
	metadata["build_command"] = buildCommand
	if variant != "" {
		metadata["variant"] = variant
	}
	if duration > 0 {
		metadata["build_duration_seconds"] = int(duration.Seconds())
	}
	metadata["built_at"] = time.Now().UTC().Format(time.RFC3339)

	// Machine info
	metadata["machine"] = map[string]interface{}{
		"os":       runtime.GOOS,
		"arch":     runtime.GOARCH,
		"hostname": getHostname(),
		"user":     getUsername(),
	}

	// Git info
	gitInfo := collectGitInfo(workDir)
	if len(gitInfo) > 0 {
		metadata["git"] = gitInfo
	}

	return metadata
}

// collectGitInfo gathers git repository information.
//
// Parameters:
//   - workDir: The working directory to run git commands in
//
// Returns:
//   - map[string]interface{}: A map containing git metadata
func collectGitInfo(workDir string) map[string]interface{} {
	gitInfo := make(map[string]interface{})

	// Get current commit hash
	if commit := runGitCommand(workDir, "rev-parse", "HEAD"); commit != "" {
		gitInfo["commit"] = commit
	}

	// Get short commit hash
	if shortCommit := runGitCommand(workDir, "rev-parse", "--short", "HEAD"); shortCommit != "" {
		gitInfo["commit_short"] = shortCommit
	}

	// Get current branch
	if branch := runGitCommand(workDir, "rev-parse", "--abbrev-ref", "HEAD"); branch != "" {
		gitInfo["branch"] = branch
	}

	// Get commit message
	if message := runGitCommand(workDir, "log", "-1", "--format=%s"); message != "" {
		gitInfo["message"] = message
	}

	// Get commit author
	if author := runGitCommand(workDir, "log", "-1", "--format=%an"); author != "" {
		gitInfo["author"] = author
	}

	// Get commit timestamp
	if timestamp := runGitCommand(workDir, "log", "-1", "--format=%ci"); timestamp != "" {
		gitInfo["timestamp"] = timestamp
	}

	// Check if working directory is dirty
	if status := runGitCommand(workDir, "status", "--porcelain"); status != "" {
		gitInfo["dirty"] = true
	} else {
		gitInfo["dirty"] = false
	}

	// Get remote URL (sanitized)
	if remote := runGitCommand(workDir, "remote", "get-url", "origin"); remote != "" {
		gitInfo["remote"] = sanitizeRemoteURL(remote)
	}

	return gitInfo
}

// runGitCommand executes a git command and returns the trimmed output.
//
// Parameters:
//   - workDir: The working directory to run the command in
//   - args: The git command arguments
//
// Returns:
//   - string: The trimmed command output, or empty string on error
func runGitCommand(workDir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = workDir
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// sanitizeRemoteURL removes credentials from a git remote URL.
//
// Parameters:
//   - url: The remote URL to sanitize
//
// Returns:
//   - string: The sanitized URL
func sanitizeRemoteURL(url string) string {
	// Remove credentials from URLs like https://user:pass@github.com/...
	if strings.Contains(url, "@") && strings.HasPrefix(url, "https://") {
		parts := strings.SplitN(url, "@", 2)
		if len(parts) == 2 {
			return "https://" + parts[1]
		}
	}
	return url
}

// getHostname returns the machine hostname.
//
// Returns:
//   - string: The hostname, or "unknown" on error
func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}

// getUsername returns the current user's username.
//
// Returns:
//   - string: The username, or "unknown" on error
func getUsername() string {
	if user := os.Getenv("USER"); user != "" {
		return user
	}
	if user := os.Getenv("USERNAME"); user != "" {
		return user
	}
	return "unknown"
}

// FormatDuration formats a duration in a human-readable way.
//
// Parameters:
//   - d: The duration to format
//
// Returns:
//   - string: A human-readable duration string
func FormatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		secs := int(d.Seconds()) % 60
		if secs > 0 {
			return fmt.Sprintf("%dm %ds", mins, secs)
		}
		return fmt.Sprintf("%dm", mins)
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh %dm", hours, mins)
}
