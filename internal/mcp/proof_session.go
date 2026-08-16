package mcp

import "strings"

// isProofOwnedSession reports whether a live session belongs to a proof run.
// SyncSessions must not auto-adopt these into a shared identity; otherwise
// `revyl device stop` can tear down a concurrent proof.
func isProofOwnedSession(metadata *map[string]interface{}) bool {
	return proofReviewRunID(metadata) != ""
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
