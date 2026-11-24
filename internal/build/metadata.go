package build

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// GenerateVersionString generates a timestamp version string formatted as "YYYYMMDD-HHMMSS".
func GenerateVersionString() string {
	return time.Now().Format("20060102-150405")
}

// GenerateVersionStringForWorkDir returns "<branch-slug>-YYYYMMDD-HHMMSS" when
// a git branch is available, or "YYYYMMDD-HHMMSS" otherwise (e.g., detached HEAD).
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

// CollectMetadata gathers build metadata (git info from workDir, machine info,
// buildCommand, platform, and duration).
func CollectMetadata(workDir, buildCommand, platform string, duration time.Duration) map[string]interface{} {
	return CollectMetadataWithContext(context.Background(), workDir, buildCommand, platform, duration)
}

// CollectMetadataWithContext includes explicit upload SCM identity when supplied.
func CollectMetadataWithContext(ctx context.Context, workDir, buildCommand, platform string, duration time.Duration) map[string]interface{} {
	metadata := make(map[string]interface{})

	metadata["build_command"] = buildCommand
	if platform != "" {
		metadata["platform"] = platform
	}
	if duration > 0 {
		metadata["build_duration_seconds"] = int(duration.Seconds())
	}
	metadata["built_at"] = time.Now().UTC().Format(time.RFC3339)

	metadata["machine"] = map[string]interface{}{
		"os":       runtime.GOOS,
		"arch":     runtime.GOARCH,
		"hostname": getHostname(),
		"user":     getUsername(),
	}

	gitInfo := collectGitInfo(workDir)
	if len(gitInfo) > 0 {
		metadata["git"] = gitInfo
	}

	attachCIMetadata(ctx, metadata, platform)

	return metadata
}

// CIMetadata returns the provider-neutral SCM keys that match uploaded builds
// with pull requests, or nil outside CI without explicit SCM flags.
func CIMetadata(ctx context.Context, platform string) map[string]interface{} {
	metadata := map[string]interface{}{}
	attachCIMetadata(ctx, metadata, platform)
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func attachCIMetadata(ctx context.Context, metadata map[string]interface{}, platform string) {
	context, ok := CIContextFromContext(ctx)
	if !ok {
		return
	}

	metadata["ci_system"] = context.System
	if context.RunID != "" {
		metadata["ci_run_id"] = context.RunID
	}
	if context.RunURL != "" {
		metadata["ci_run_url"] = context.RunURL
	}
	if context.Actor != "" {
		metadata["ci_actor"] = context.Actor
	}
	if context.Repository != "" {
		metadata["github_repository"] = context.Repository
		metadata["scm_repo"] = context.Repository
	}
	if context.SCMProvider != "" {
		metadata["scm_provider"] = context.SCMProvider
	}
	if context.SCMNamespace != "" {
		metadata["scm_namespace"] = context.SCMNamespace
	}
	if context.SCMProject != "" {
		metadata["scm_project"] = context.SCMProject
	}
	if context.Branch != "" {
		metadata["scm_branch"] = context.Branch
	}
	if context.ReviewNumber > 0 {
		metadata["scm_review_number"] = context.ReviewNumber
		metadata["pr_number"] = context.ReviewNumber
	}
	if context.CommitSHA != "" {
		metadata["scm_head_sha"] = context.CommitSHA
	}
	if context.BaseSHA != "" {
		metadata["scm_base_sha"] = context.BaseSHA
	}
	if platform != "" {
		metadata["scm_platform"] = strings.ToLower(strings.TrimSpace(platform))
	}
}

func collectGitInfo(workDir string) map[string]interface{} {
	gitInfo := make(map[string]interface{})

	if commit := runGitCommand(workDir, "rev-parse", "HEAD"); commit != "" {
		gitInfo["commit"] = commit
	}

	if shortCommit := runGitCommand(workDir, "rev-parse", "--short", "HEAD"); shortCommit != "" {
		gitInfo["commit_short"] = shortCommit
	}

	if branch := runGitCommand(workDir, "rev-parse", "--abbrev-ref", "HEAD"); branch != "" {
		gitInfo["branch"] = branch
	}

	if message := runGitCommand(workDir, "log", "-1", "--format=%s"); message != "" {
		gitInfo["message"] = message
	}

	if author := runGitCommand(workDir, "log", "-1", "--format=%an"); author != "" {
		gitInfo["author"] = author
	}

	if timestamp := runGitCommand(workDir, "log", "-1", "--format=%ci"); timestamp != "" {
		gitInfo["timestamp"] = timestamp
	}

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
func sanitizeRemoteURL(url string) string {
	if strings.Contains(url, "@") && strings.HasPrefix(url, "https://") {
		parts := strings.SplitN(url, "@", 2)
		if len(parts) == 2 {
			return "https://" + parts[1]
		}
	}
	return url
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}

func getUsername() string {
	if user := os.Getenv("USER"); user != "" {
		return user
	}
	if user := os.Getenv("USERNAME"); user != "" {
		return user
	}
	return "unknown"
}

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
