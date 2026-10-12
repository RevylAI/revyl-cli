package buildselection

import (
	"context"
	"fmt"
	"strings"

	"github.com/revyl/cli/internal/api"
)

type ResolverClient interface {
	ResolveBuildSelection(ctx context.Context, appID string, selection api.BuildSelection) (*api.BuildVersionDetail, error)
}

type BranchBuildsClient interface {
	ResolverClient
	ListBuildVersionsWithOptions(ctx context.Context, appID string, opts api.BuildListOptions) (*api.BuildVersionsPage, error)
}

func SelectLatestBuildVersionPreferringGitBranch(ctx context.Context, client BranchBuildsClient, appID, branchID, gitBranch string) (*api.BuildVersion, string, []string, error) {
	latest := api.BuildSelection{BranchID: branchID, Mode: api.BuildSelectionModeLatest}
	if gitBranch == "" {
		return SelectBuildVersion(ctx, client, appID, latest)
	}
	matched, err := findGitBranchBuild(ctx, client, appID, branchID, gitBranch)
	if err != nil {
		return nil, "", nil, err
	}
	if matched != nil {
		return matched, "git_branch", nil, nil
	}
	selected, source, warnings, err := SelectBuildVersion(ctx, client, appID, latest)
	if err != nil {
		return nil, "", nil, err
	}
	warning := fmt.Sprintf("No builds found for current branch %q; using latest available build instead.", gitBranch)
	return selected, source, append(warnings, warning), nil
}

func findGitBranchBuild(ctx context.Context, client BranchBuildsClient, appID, branchID, gitBranch string) (*api.BuildVersion, error) {
	const pageSize = 100
	const maxPages = 500
	for page := 1; page <= maxPages; page++ {
		result, err := client.ListBuildVersionsWithOptions(ctx, appID, api.BuildListOptions{BranchID: branchID, Page: page, PageSize: pageSize})
		if err != nil {
			return nil, err
		}
		for i := range result.Items {
			if ExtractBranch(result.Items[i].Metadata) == gitBranch {
				return &result.Items[i], nil
			}
		}
		if !result.HasNext {
			break
		}
	}
	return nil, nil
}

func SelectPreferredBuildVersion(ctx context.Context, client ResolverClient, appID, workDir string) (*api.BuildVersion, string, []string, error) {
	return SelectBuildVersion(ctx, client, appID, api.BuildSelection{})
}

func SelectPreferredBuildVersionForBranch(ctx context.Context, client ResolverClient, appID, branchName string) (*api.BuildVersion, string, []string, error) {
	return SelectBuildVersion(ctx, client, appID, api.BuildSelection{BranchName: branchName})
}

func SelectBuildVersion(ctx context.Context, client ResolverClient, appID string, selection api.BuildSelection) (*api.BuildVersion, string, []string, error) {
	detail, err := client.ResolveBuildSelection(ctx, appID, selection)
	if err != nil {
		return nil, "", nil, err
	}
	source := "default_branch"
	if selection.BuildID != "" {
		source = "exact_build"
	} else if selection.BranchID != "" || selection.BranchName != "" {
		source = "explicit_branch"
	} else if selection.ContextBranchName != "" {
		source = "context_branch"
	}
	return detail.BuildVersion(), source, nil, nil
}

// CurrentBranch returns the current git branch for the work directory.
// Returns an empty string when branch detection fails or in detached HEAD mode.
func CurrentBranch(workDir string) string {
	if strings.TrimSpace(workDir) == "" {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), branchDetectionTimeout)
	defer cancel()
	branch, ok, _ := runContextGit(ctx, workDir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if !ok {
		return ""
	}
	return strings.TrimRight(branch, "\r\n")
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
