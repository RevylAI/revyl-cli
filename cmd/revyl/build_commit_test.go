package main

import "testing"

func TestExtractBuildCommit_PrefersGitCommitShort(t *testing.T) {
	metadata := map[string]interface{}{
		"git": map[string]interface{}{
			"commit_short": "abc1234",
			"commit":       "abc1234abcdef5678",
		},
		"source_metadata": map[string]interface{}{
			"commit_sha": "feedfacecafebeef",
		},
	}

	got := extractBuildCommit(metadata)
	if got != "abc1234" {
		t.Fatalf("extractBuildCommit() = %q, want %q", got, "abc1234")
	}
}

func TestExtractBuildCommit_UsesGitCommitWhenShortMissing(t *testing.T) {
	metadata := map[string]interface{}{
		"git": map[string]interface{}{
			"commit": "1234567890abcdef123456",
		},
	}

	got := extractBuildCommit(metadata)
	if got != "1234567890ab" {
		t.Fatalf("extractBuildCommit() = %q, want %q", got, "1234567890ab")
	}
}

func TestExtractBuildCommit_FallsBackToSourceMetadataCommitSHA(t *testing.T) {
	metadata := map[string]interface{}{
		"source_metadata": map[string]interface{}{
			"commit_sha": "feedfacecafebeef0000",
		},
	}

	got := extractBuildCommit(metadata)
	if got != "feedfacecafe" {
		t.Fatalf("extractBuildCommit() = %q, want %q", got, "feedfacecafe")
	}
}

func TestBuildListReadsGitHubAppBuildBranchAndCommit(t *testing.T) {
	metadata := map[string]interface{}{
		"scm_branch":   "feature/checkout",
		"scm_head_sha": "d717295d11a62f819751c14b4468e3e141825fcb",
	}

	if got := buildListGitBranch(metadata); got != "feature/checkout" {
		t.Fatalf("buildListGitBranch() = %q, want %q", got, "feature/checkout")
	}
	if got := extractBuildCommit(metadata); got != "d717295d11a6" {
		t.Fatalf("extractBuildCommit() = %q, want %q", got, "d717295d11a6")
	}
}

func TestBuildListPrefersRecordedGitMetadataOverGitHubAppFields(t *testing.T) {
	metadata := map[string]interface{}{
		"git":          map[string]interface{}{"branch": "main", "commit_short": "abc1234"},
		"scm_branch":   "feature/checkout",
		"scm_head_sha": "d717295d11a62f819751c14b4468e3e141825fcb",
	}

	if got := buildListGitBranch(metadata); got != "main" {
		t.Fatalf("buildListGitBranch() = %q, want %q", got, "main")
	}
	if got := extractBuildCommit(metadata); got != "abc1234" {
		t.Fatalf("extractBuildCommit() = %q, want %q", got, "abc1234")
	}
}
