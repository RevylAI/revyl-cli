package tui

import (
	"context"
	"fmt"
	"os"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/buildselection"
	"github.com/revyl/cli/internal/ui"
)

type testExecutionBuildSelection struct {
	Selection     api.BuildSelection
	OverrideAppID string
}

func withRuntimeGitBranch(ctx context.Context, selection api.BuildSelection) (api.BuildSelection, error) {
	if selection.BranchID != "" || selection.BranchName != "" || selection.BuildID != "" || selection.GitBranchName != "" ||
		selection.Mode == api.BuildSelectionModeLatestAllBranches {
		return selection, nil
	}
	name, err := detectRuntimeGitBranch(ctx)
	if err != nil {
		return selection, err
	}
	selection.GitBranchName = name
	return selection, nil
}

// detectRuntimeGitBranch returns the Git branch of the directory the terminal
// UI runs in, or "" when there is none to read.
func detectRuntimeGitBranch(ctx context.Context) (string, error) {
	workDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve selected project directory: %w", err)
	}
	gitBranch, err := buildselection.DetectGitBranch(ctx, buildselection.GitBranchOptions{WorkDir: workDir})
	if err != nil {
		return "", fmt.Errorf("detect project branch: %w", err)
	}
	return gitBranch.Name, nil
}

func prepareTestRunBuildSelection(ctx context.Context, client *api.Client, choice testExecutionBuildSelection) (api.BuildSelection, string, error) {
	selection, err := withRuntimeGitBranch(ctx, choice.Selection)
	if err != nil {
		return selection, "", err
	}
	if choice.OverrideAppID != "" && selection.BranchID == "" && selection.BranchName == "" && selection.BuildID == "" && selection.Mode != api.BuildSelectionModeLatestAllBranches {
		branch, err := client.ResolveBranchWithGitBranch(ctx, choice.OverrideAppID, "", "", selection.GitBranchName, selection.Mode)
		if err != nil {
			return selection, "", fmt.Errorf("resolve run branch override: %w", err)
		}
		selection.BranchID, selection.GitBranchName = branch.ID, ""
		return selection, ui.BranchSelectionNotice(branch.GitBranchName, branch.Name, string(branch.BranchSource), branch.AutoGitBranchEnabled), nil
	}
	return selection, "", nil
}
