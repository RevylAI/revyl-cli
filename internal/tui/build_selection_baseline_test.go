package tui

import (
	"context"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestRuntimeSelectionPreservesExplicitAndFrozenChoices(t *testing.T) {
	selectBranchContextWorktree(t, "unrelated/checkout")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_HEAD_REF", "unrelated/ci")
	t.Setenv("REVYL_INTERNAL_BRANCH_CONTEXT", "unrelated/inherited")
	for _, selection := range []api.BuildSelection{
		{BranchID: pickerBranchID, Mode: api.BuildSelectionModeInherit},
		{BranchName: "release", Mode: api.BuildSelectionModeLatest},
		{BuildID: pickerBuildID, Mode: api.BuildSelectionModePinned},
	} {
		actual, err := prepareTestRunBuildSelection(context.Background(), nil, testExecutionBuildSelection{
			Selection: selection, OverrideAppID: pickerAppID,
		})
		if err != nil || actual != selection {
			t.Fatalf("explicit selection changed: %#v => %#v, %v", selection, actual, err)
		}
	}
}
