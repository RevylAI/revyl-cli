package tui

import (
	"context"
	"fmt"

	"github.com/revyl/cli/internal/api"
)

type testExecutionBuildSelection struct {
	Selection     api.BuildSelection
	OverrideAppID string
}

func prepareTestRunBuildSelection(ctx context.Context, client *api.Client, choice testExecutionBuildSelection) (api.BuildSelection, error) {
	selection := choice.Selection
	if choice.OverrideAppID != "" && selection.BranchID == "" && selection.BranchName == "" && selection.BuildID == "" && selection.Mode != api.BuildSelectionModeLatestAllBranches {
		branch, err := client.ResolveBranchWithContext(ctx, choice.OverrideAppID, "", "", selection.ContextBranchName)
		if err != nil {
			return selection, fmt.Errorf("resolve run branch override: %w", err)
		}
		selection.BranchID, selection.ContextBranchName = branch.ID, ""
	}
	return selection, nil
}
