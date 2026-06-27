// Package gitremote resolves a GitHub repository's owner/name ("slug") from a
// local git checkout. It is shared by the `revyl github` commands and the TUI
// integrations screen so both derive the same namespace/project for a repo.
package gitremote

import (
	"fmt"
	"os/exec"
	"strings"
)

// OriginURL returns the configured origin remote URL for a repository root.
//
// Parameters:
//   - root: The repository root directory to query.
//
// Returns:
//   - string: The trimmed origin remote URL.
//   - error: A non-nil error when git is unavailable or no origin is set.
func OriginURL(root string) (string, error) {
	out, err := exec.Command(
		"git", "-C", root, "config", "--get", "remote.origin.url",
	).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ParseGithubRemote extracts owner/repo from a GitHub remote URL, supporting
// scp-like (git@github.com:owner/repo.git) and URL (https/ssh) forms.
//
// Parameters:
//   - remote: The raw remote URL.
//
// Returns:
//   - string: The repository owner/namespace.
//   - string: The repository name.
//   - bool: true when the remote parsed into a GitHub owner/repo pair.
func ParseGithubRemote(remote string) (string, string, bool) {
	remote = strings.TrimSuffix(strings.TrimSpace(remote), ".git")
	if idx := strings.Index(remote, "github.com:"); idx != -1 {
		return splitOwnerRepo(remote[idx+len("github.com:"):])
	}
	if idx := strings.Index(remote, "github.com/"); idx != -1 {
		return splitOwnerRepo(remote[idx+len("github.com/"):])
	}
	return "", "", false
}

// ResolveSlug resolves the GitHub owner/repo for a repository.
//
// Parameters:
//   - root: The repository root used to query the git origin remote.
//   - override: An optional "owner/repo" override (takes priority when set).
//
// Returns:
//   - string: The repository owner/namespace.
//   - string: The repository name.
//   - error: A non-nil error when neither the override nor the git remote
//     yields a parseable GitHub owner/repo.
func ResolveSlug(root, override string) (string, string, error) {
	if strings.TrimSpace(override) != "" {
		return splitRepoSlug(override)
	}
	remote, err := OriginURL(root)
	if err != nil || strings.TrimSpace(remote) == "" {
		return "", "", fmt.Errorf(
			"could not determine the GitHub repo; pass --repo owner/name",
		)
	}
	namespace, project, ok := ParseGithubRemote(remote)
	if !ok {
		return "", "", fmt.Errorf(
			"could not parse a GitHub repo from remote %q; pass --repo owner/name",
			remote,
		)
	}
	return namespace, project, nil
}

// splitOwnerRepo splits a trimmed "owner/repo[/...]" path into owner and repo.
func splitOwnerRepo(path string) (string, string, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// splitRepoSlug parses an explicit "owner/name" override.
func splitRepoSlug(slug string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(slug), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid --repo %q (expected owner/name)", slug)
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), nil
}
