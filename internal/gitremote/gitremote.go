// Package gitremote resolves provider and repository identity from Git origins.
package gitremote

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx,
		"git", "-C", root, "config", "--get", "remote.origin.url",
	).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

type RepositoryReference struct {
	Provider    string
	InstanceURL *string
	Namespace   string
	Name        string
}

func ResolveRepository(root string) (RepositoryReference, error) {
	remote, err := OriginURL(root)
	if err != nil {
		return RepositoryReference{}, fmt.Errorf("read Git origin: %w", err)
	}
	return ParseRepositoryRemote(remote)
}

func ParseRepositoryRemote(remote string) (RepositoryReference, error) {
	remote = strings.TrimSpace(remote)
	if strings.HasPrefix(remote, "git@") && !strings.Contains(remote, "://") {
		host, path, ok := strings.Cut(strings.TrimPrefix(remote, "git@"), ":")
		if !ok {
			return RepositoryReference{}, fmt.Errorf("invalid Git origin")
		}
		remote = "ssh://git@" + host + "/" + path
	}
	parsed, err := url.Parse(remote)
	if err != nil || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" {
		return RepositoryReference{}, fmt.Errorf("invalid Git origin")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "ssh" || parsed.Scheme == "https" && parsed.User != nil || parsed.Scheme == "ssh" && parsed.Port() != "" && parsed.Port() != "22" {
		return RepositoryReference{}, fmt.Errorf("use a credential-free HTTPS origin or standard Git SSH origin")
	}
	if parsed.Port() != "" {
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return RepositoryReference{}, fmt.Errorf("invalid Git origin port")
		}
	}
	if parsed.Scheme == "ssh" && (parsed.User == nil || parsed.User.String() != "git") {
		return RepositoryReference{}, fmt.Errorf("use a standard git SSH origin")
	}
	path := strings.TrimSuffix(strings.TrimSuffix(parsed.Path, "/"), ".git")
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 2 {
		return RepositoryReference{}, fmt.Errorf("Git origin must identify a namespace and repository")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || part == "-" {
			return RepositoryReference{}, fmt.Errorf("invalid Git repository path")
		}
		for _, character := range part {
			if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("-_.", character)) {
				return RepositoryReference{}, fmt.Errorf("invalid Git repository path")
			}
		}
	}
	host := strings.ToLower(parsed.Hostname())
	result := RepositoryReference{Provider: "gitlab", Namespace: strings.Join(parts[:len(parts)-1], "/"), Name: parts[len(parts)-1]}
	if host == "github.com" {
		if len(parts) != 2 || parsed.Scheme == "https" && parsed.Port() != "" && parsed.Port() != "443" {
			return RepositoryReference{}, fmt.Errorf("GitHub origin must identify owner/repository")
		}
		result.Provider = "github"
		return result, nil
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	origin := "https://" + host
	if parsed.Scheme == "https" && parsed.Port() != "" && parsed.Port() != "443" {
		origin += ":" + parsed.Port()
	}
	result.InstanceURL = &origin
	return result, nil
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
