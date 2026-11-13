package config

import "strings"

// Stable codes for rejected deprecated-spelling combinations, shared with the
// Python compiler so the PR check can explain each one.
const (
	reviewTriggersCombinedWithTriggersCode = "review_triggers_combined_with_triggers"
	workflowIDsCombinedWithWorkflowsCode   = "workflow_ids_combined_with_workflows"
)

// CIUploadBlockTriggersMessage explains the one case where an absent root and
// triggers: {} differ. With Revyl builds both select every non-draft pull
// request under the project root.
const CIUploadBlockTriggersMessage = "pr_review.triggers is absent, so Revyl waits for CI uploads only on pull requests that an enabled proof or workflow block's triggers select; set pr_review.triggers, even to {}, to wait on every non-draft pull request under the project root"

func validateReviewTriggerBlocks(review *AuthoredPRReview) error {
	var proofTriggers, workflowTriggers *AuthoredReviewTriggers
	if review.ProofOfChanges != nil {
		proofTriggers = review.ProofOfChanges.Triggers
	}
	if review.Workflows != nil {
		workflowTriggers = review.Workflows.Triggers
	}
	for _, block := range []struct {
		triggers *AuthoredReviewTriggers
		path     []string
	}{
		{review.Triggers, []string{"pr_review", "triggers"}},
		{review.ReviewTriggers, []string{"pr_review", "review_triggers"}},
		{proofTriggers, []string{"pr_review", "proof_of_changes", "triggers"}},
		{workflowTriggers, []string{"pr_review", "workflows", "triggers"}},
	} {
		if err := validateAuthoredReviewTriggers(block.triggers, block.path); err != nil {
			return err
		}
	}
	return nil
}

func validateDeprecatedSpellings(review *AuthoredPRReview) error {
	if review.Triggers != nil && review.ReviewTriggers != nil {
		return newConfigError("contract", reviewTriggersCombinedWithTriggersCode, []string{"pr_review"}, "review_triggers cannot be combined with triggers; keep only pr_review.triggers")
	}
	if len(review.WorkflowIDs) > 0 && review.Workflows != nil {
		return newConfigError("contract", workflowIDsCombinedWithWorkflowsCode, []string{"pr_review"}, "workflow_ids cannot be combined with workflows; move the ids to pr_review.workflows.workflow_ids and set pr_review.workflows.enabled: true")
	}
	return nil
}

// RootTriggers returns the authored root triggers under either spelling.
func (review AuthoredPRReview) RootTriggers() *AuthoredReviewTriggers {
	if review.Triggers != nil {
		return review.Triggers
	}
	return review.ReviewTriggers
}

// TracksUnionOfBlocks reports whether an absent root with CI uploads selects
// only the pull requests that an enabled proof or workflow block's triggers
// select, instead of every non-draft pull request under the project root.
func (review AuthoredPRReview) TracksUnionOfBlocks() bool {
	if review.RootTriggers() != nil || review.Build.Kind != "ci_upload_to_revyl" {
		return false
	}
	if proof := review.ProofOfChanges; proof != nil && proof.Enabled != nil && *proof.Enabled && proof.Triggers != nil {
		return true
	}
	workflows := review.Workflows
	return workflows != nil && workflows.Enabled != nil && *workflows.Enabled && len(workflows.WorkflowIDs) > 0 && workflows.Triggers != nil
}

func normalizeReviewTriggers(triggers *AuthoredReviewTriggers) *NormalizedReviewTriggers {
	if triggers == nil {
		return nil
	}
	result := &NormalizedReviewTriggers{Paths: orderedUniqueStrings(triggers.Paths), Labels: orderedUniqueStrings(triggers.Labels)}
	if triggers.Drafts != nil {
		result.Drafts = *triggers.Drafts
	}
	return result
}

// normalizeReviewBlockTriggers materializes a block's omitted filters. An
// inherited block's omitted filters leave the root unnarrowed. Otherwise the
// block stands in for an absent root and takes the root defaults.
func normalizeReviewBlockTriggers(triggers *AuthoredReviewTriggers, inherited bool, projectRoot string) *NormalizedReviewTriggers {
	result := normalizeReviewTriggers(triggers)
	if result == nil {
		return nil
	}
	if !inherited {
		result.Paths = effectiveReviewPaths(result.Paths, projectRoot)
	} else if triggers.Drafts == nil {
		result.Drafts = true
	}
	return result
}

func effectiveReviewPaths(paths []string, projectRoot string) []string {
	for _, pattern := range paths {
		if !strings.HasPrefix(pattern, "!") {
			return paths
		}
	}
	if projectRoot == "." {
		return paths
	}
	return append([]string{projectRoot + "/**"}, paths...)
}

func authoredReviewTriggersFileProjection(triggers *AuthoredReviewTriggers) map[string]any {
	projection := authoredReviewBlockTriggersFileProjection(triggers)
	projection["drafts"] = triggers.Drafts != nil && *triggers.Drafts
	return projection
}

// authoredReviewBlockTriggersFileProjection keeps an omitted drafts omitted,
// because a block that omits it inherits the root's drafts setting.
func authoredReviewBlockTriggersFileProjection(triggers *AuthoredReviewTriggers) map[string]any {
	projection := map[string]any{"paths": nonNilSlice(triggers.Paths), "labels": nonNilSlice(triggers.Labels)}
	if triggers.Drafts != nil {
		projection["drafts"] = *triggers.Drafts
	}
	return projection
}
