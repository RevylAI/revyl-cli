// Package build provides build execution and artifact management utilities.
package build

import (
	"encoding/json"
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

// GenerateVersionStringForWorkDir generates a branch-aware version string.
//
// Format:
//   - "<branch-slug>-YYYYMMDD-HHMMSS" when a git branch is available
//   - "YYYYMMDD-HHMMSS" fallback otherwise (e.g., detached HEAD / non-git dir)
func GenerateVersionStringForWorkDir(workDir string) string {
	timestamp := GenerateVersionString()
	branch := runGitCommand(workDir, "rev-parse", "--abbrev-ref", "HEAD")
	branchSlug := sanitizeBranchForVersion(branch)
	if branchSlug == "" {
		return timestamp
	}
	return fmt.Sprintf("%s-%s", branchSlug, timestamp)
}

func sanitizeBranchForVersion(branch string) string {
	branch = strings.TrimSpace(strings.ToLower(branch))
	if branch == "" || branch == "head" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(branch))

	lastDash := false
	for _, r := range branch {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}

	slug := strings.Trim(b.String(), "-")
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-")
	}
	return slug
}

// CollectMetadata gathers build metadata including git info, machine info, and build details.
//
// Parameters:
//   - workDir: The working directory (used for git commands)
//   - buildCommand: The build command that was executed
//   - platform: The build platform name (e.g., "ios", "android")
//   - duration: How long the build took
//
// Returns:
//   - map[string]interface{}: A map containing build metadata
func CollectMetadata(workDir, buildCommand, platform string, duration time.Duration) map[string]interface{} {
	metadata := make(map[string]interface{})

	// Build info
	metadata["build_command"] = buildCommand
	if platform != "" {
		metadata["platform"] = platform
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

	attachGitHubActionsMetadata(metadata, platform)

	return metadata
}

// attachGitHubActionsMetadata normalizes GitHub Actions context into the
// provider-neutral SCM metadata keys expected by Revyl's GitHub automation.
func attachGitHubActionsMetadata(metadata map[string]interface{}, platform string) {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		return
	}

	event := readGitHubActionsEvent(os.Getenv("GITHUB_EVENT_PATH"))
	repo := strings.TrimSpace(os.Getenv("GITHUB_REPOSITORY"))
	namespace, project := splitGitHubRepository(repo)
	headSHA := firstNonEmpty(event.PullRequest.Head.SHA, os.Getenv("REVYL_PR_HEAD_SHA"), os.Getenv("GITHUB_SHA"))
	runURL := githubActionsRunURL(
		os.Getenv("GITHUB_SERVER_URL"),
		repo,
		os.Getenv("GITHUB_RUN_ID"),
	)

	metadata["ci_system"] = "github-actions"
	if runID := strings.TrimSpace(os.Getenv("GITHUB_RUN_ID")); runID != "" {
		metadata["ci_run_id"] = runID
	}
	if runURL != "" {
		metadata["ci_run_url"] = runURL
	}
	if actor := strings.TrimSpace(os.Getenv("GITHUB_ACTOR")); actor != "" {
		metadata["ci_actor"] = actor
	}
	if repo != "" {
		metadata["github_repository"] = repo
		metadata["scm_provider"] = "github"
		metadata["scm_repo"] = repo
	}
	if namespace != "" {
		metadata["scm_namespace"] = namespace
	}
	if project != "" {
		metadata["scm_project"] = project
	}
	if event.PullRequest.Number > 0 {
		metadata["scm_review_number"] = event.PullRequest.Number
		metadata["pr_number"] = event.PullRequest.Number
	}
	if headSHA != "" {
		metadata["scm_head_sha"] = headSHA
	}
	if baseSHA := strings.TrimSpace(event.PullRequest.Base.SHA); baseSHA != "" {
		metadata["scm_base_sha"] = baseSHA
	}
	if platform != "" {
		metadata["scm_platform"] = strings.ToLower(strings.TrimSpace(platform))
	}
}

type githubActionsEvent struct {
	PullRequest struct {
		HTMLURL string `json:"html_url"`
		Number  int    `json:"number"`
		Head    struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			SHA string `json:"sha"`
		} `json:"base"`
	} `json:"pull_request"`
}

func readGitHubActionsEvent(eventPath string) githubActionsEvent {
	var event githubActionsEvent
	if strings.TrimSpace(eventPath) == "" {
		return event
	}
	data, err := os.ReadFile(eventPath)
	if err != nil {
		return event
	}
	_ = json.Unmarshal(data, &event)
	return event
}

func splitGitHubRepository(repo string) (string, string) {
	repo = strings.TrimSpace(repo)
	if repo == "" || !strings.Contains(repo, "/") {
		return "", ""
	}
	parts := strings.SplitN(repo, "/", 2)
	namespace := strings.TrimSpace(parts[0])
	project := strings.TrimSpace(parts[1])
	if namespace == "" || project == "" {
		return "", ""
	}
	return namespace, project
}

func githubActionsRunURL(server, repo, runID string) string {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	repo = strings.TrimSpace(repo)
	runID = strings.TrimSpace(runID)
	if server == "" || repo == "" || runID == "" {
		return ""
	}
	return server + "/" + repo + "/actions/runs/" + runID
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
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
