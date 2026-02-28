package buildselection

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/revyl/cli/internal/api"
)

// ResolverClient describes the API surface required for build selection.
type ResolverClient interface {
	ListBuildVersionsPage(ctx context.Context, appID string, page int, pageSize int) (*api.BuildVersionsPage, error)
}

// SelectPreferredBuildVersion selects a build for an app using branch-aware precedence.
//
// Precedence:
//  1. Latest build matching the current git branch in build metadata
//  2. Latest build overall
func SelectPreferredBuildVersion(
	ctx context.Context,
	client ResolverClient,
	appID string,
	workDir string,
) (*api.BuildVersion, string, []string, error) {
	return SelectPreferredBuildVersionForBranch(ctx, client, appID, CurrentBranch(workDir))
}

// SelectPreferredBuildVersionForBranch selects a build using an explicit branch value.
// This is primarily useful for tests and call-sites that already know the branch.
func SelectPreferredBuildVersionForBranch(
	ctx context.Context,
	client ResolverClient,
	appID string,
	currentBranch string,
) (*api.BuildVersion, string, []string, error) {
	const pageSize = 100
	const maxPages = 500
	trimmedAppID := strings.TrimSpace(appID)
	branch := strings.TrimSpace(currentBranch)

	var latestOverall *api.BuildVersion
	for page := 1; page <= maxPages; page++ {
		result, err := client.ListBuildVersionsPage(ctx, trimmedAppID, page, pageSize)
		if err != nil {
			return nil, "", nil, err
		}
		if result == nil || len(result.Items) == 0 {
			if page == 1 {
				return nil, "", nil, nil
			}
			break
		}

		if latestOverall == nil {
			selected := result.Items[0]
			latestOverall = &selected
			if branch == "" {
				return latestOverall, "latest", nil, nil
			}
		}

		for i := range result.Items {
			buildBranch := strings.TrimSpace(ExtractBranch(result.Items[i].Metadata))
			if buildBranch == "" || buildBranch != branch {
				continue
			}
			selected := result.Items[i]
			return &selected, fmt.Sprintf("branch:%s", branch), nil, nil
		}

		if !hasNextBuildVersionsPage(result, page) {
			break
		}
	}

	if latestOverall == nil {
		return nil, "", nil, nil
	}

	warnings := []string(nil)
	if branch != "" {
		warnings = []string{
			fmt.Sprintf(
				"No builds found for current branch %q; using latest available build instead.",
				branch,
			),
		}
	}
	return latestOverall, "latest", warnings, nil
}

func hasNextBuildVersionsPage(result *api.BuildVersionsPage, requestedPage int) bool {
	if result == nil {
		return false
	}
	if result.HasNext {
		return true
	}
	if result.TotalPages > 0 && requestedPage < result.TotalPages {
		return true
	}
	if result.Page > 0 && result.TotalPages > result.Page {
		return true
	}
	return false
}

// CurrentBranch returns the current git branch for the work directory.
// Returns an empty string when branch detection fails or in detached HEAD mode.
func CurrentBranch(workDir string) string {
	if strings.TrimSpace(workDir) == "" {
		return ""
	}

	cmd := exec.Command("git", "-C", workDir, "rev-parse", "--abbrev-ref", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	branch := strings.TrimSpace(string(output))
	if branch == "" || strings.EqualFold(branch, "HEAD") {
		return ""
	}
	return branch
}

// ExtractBranch reads a branch value from build metadata.
//
// Metadata key precedence:
//  1. metadata.git.branch
//  2. metadata.source_metadata.branch
//  3. metadata.branch
func ExtractBranch(metadata map[string]interface{}) string {
	if len(metadata) == 0 {
		return ""
	}

	if gitMeta := toStringMap(metadata["git"]); gitMeta != nil {
		if branch := readString(gitMeta, "branch"); branch != "" {
			return branch
		}
	}

	if sourceMeta := toStringMap(metadata["source_metadata"]); sourceMeta != nil {
		if branch := readString(sourceMeta, "branch"); branch != "" {
			return branch
		}
	}

	return readString(metadata, "branch")
}

func readString(m map[string]interface{}, key string) string {
	value, ok := m[key]
	if !ok || value == nil {
		return ""
	}

	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case []byte:
		return strings.TrimSpace(string(v))
	default:
		return ""
	}
}

func toStringMap(value interface{}) map[string]interface{} {
	switch m := value.(type) {
	case map[string]interface{}:
		return m
	case map[string]string:
		out := make(map[string]interface{}, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(m))
		for k, v := range m {
			key, ok := k.(string)
			if !ok {
				continue
			}
			out[key] = v
		}
		return out
	default:
		return nil
	}
}
