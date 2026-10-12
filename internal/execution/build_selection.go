package execution

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

func ValidateRuntimeBuildSelection(branchID, branchName, mode, buildID string) error {
	return (api.BuildSelection{BranchID: branchID, BranchName: branchName, Mode: api.BuildSelectionMode(mode), BuildID: buildID}).Validate()
}

func ResolveSavedBuildSelection(ctx context.Context, client *api.Client, appID, branchName, branchID, mode, buildID string) (*api.SavedBuildSelection, error) {
	if branchName == "" && branchID == "" && mode == "" && buildID == "" {
		return nil, nil
	}
	if err := ValidateRuntimeBuildSelection(branchID, branchName, mode, buildID); err != nil {
		return nil, err
	}
	if branchName != "" {
		branch, err := client.ResolveBuildBranch(ctx, appID, branchName)
		if err != nil {
			return nil, fmt.Errorf("resolve saved branch: %w", err)
		}
		branchID = branch.ID
	}
	if buildID != "" {
		build, err := client.GetBuildVersionDetail(ctx, buildID, false)
		if err != nil {
			return nil, fmt.Errorf("resolve saved build: %w", err)
		}
		if build.AppID != appID {
			return nil, fmt.Errorf("selected build does not belong to the test app")
		}
		if branchID != "" && branchID != build.BranchID {
			return nil, fmt.Errorf("selected build does not belong to the selected branch")
		}
		branchID = build.BranchID
		mode = "pinned"
	}
	return api.NewSavedBuildSelection(branchID, mode, buildID)
}

func SavedBuildSelectionFromConfig(build config.TestBuildConfig) (*api.SavedBuildSelection, error) {
	if build.PinnedVersion != "" {
		if build.BranchID == "" && build.BuildSelection == "" && build.PinnedBuildID == "" {
			return nil, nil
		}
		if build.BuildSelection != "pinned" || build.PinnedBuildID == "" {
			return nil, fmt.Errorf("pinned_version conflicts with the saved build selection")
		}
	}
	if build.BranchID == "" && build.BuildSelection == "" && build.PinnedBuildID == "" {
		return nil, nil
	}
	return api.NewSavedBuildSelection(build.BranchID, build.BuildSelection, build.PinnedBuildID)
}

func applyWorkflowExactBuild(ctx context.Context, client *api.Client, target *api.PlatformApp, buildID, legacyVersion, branchName, frozenBranchID string) error {
	if buildID == "" {
		return nil
	}
	if legacyVersion != "" {
		return fmt.Errorf("choose an exact build ID or a legacy build version, not both")
	}
	selection := api.BuildSelection{BranchName: branchName, BuildID: buildID, Mode: api.BuildSelectionModePinned}
	if frozenBranchID != "" {
		selection.BranchID, selection.BranchName = frozenBranchID, ""
	}
	build, err := client.ResolveBuildSelection(ctx, target.AppId.String(), selection)
	if err != nil {
		return fmt.Errorf("resolve workflow build: %w", err)
	}
	branchID, err := uuid.Parse(build.BranchID)
	if err != nil {
		return fmt.Errorf("resolved workflow build has no valid branch ID: %w", err)
	}
	id, err := uuid.Parse(buildID)
	if err != nil {
		return fmt.Errorf("invalid workflow build ID: %w", err)
	}
	mode := api.BuildSelectionModePinned
	target.BranchId = &branchID
	target.BuildSelection = &mode
	target.PinnedBuildId = &id
	return nil
}
