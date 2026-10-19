package analytics

import "context"

// SetGitBranchOrigin records hint availability, not the final saved-selector
// outcome, on the existing command terminal event.
func SetGitBranchOrigin(ctx context.Context, origin string) {
	switch origin {
	case "local_git", "github_actions", "buildkite", "submitted_git":
	default:
		origin = "none"
	}
	SetCommandCompletion(ctx, CommandCompletion{Properties: map[string]interface{}{
		"git_branch_origin": origin,
	}})
}
