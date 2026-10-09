package mcp

import "strings"

// isOtherProofRunSession reports whether a live session belongs to a proof run
// other than the one the listing is confined to. SyncSessions must not
// auto-adopt these into a shared identity; otherwise `revyl device stop` can
// tear down a concurrent proof. A proof agent's own listing names its run, so
// its own sessions are still adopted.
func isOtherProofRunSession(metadata *map[string]interface{}, listingProofRunID *string) bool {
	runID := proofReviewRunID(metadata)
	if runID == "" {
		return false
	}
	return listingProofRunID == nil || !strings.EqualFold(runID, strings.TrimSpace(*listingProofRunID))
}

func proofReviewRunID(metadata *map[string]interface{}) string {
	if metadata == nil {
		return ""
	}
	raw, ok := (*metadata)["scm_review_run_id"]
	if !ok || raw == nil {
		return ""
	}
	value, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}
