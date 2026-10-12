package analytics

import "context"

// SetBranchContextOrigin records hint availability, not the final saved-selector
// outcome, on the existing command terminal event.
func SetBranchContextOrigin(ctx context.Context, origin string) {
	switch origin {
	case "local_git", "github_actions", "buildkite", "submitted_git":
	default:
		origin = "none"
	}
	SetCommandCompletion(ctx, CommandCompletion{Properties: map[string]interface{}{
		"branch_context_origin": origin,
	}})
}
