package buildselection

import (
	"context"
	"fmt"

	"github.com/revyl/cli/internal/api"
)

// BranchRunBuildClient reads the build a plain run on a branch uses.
type BranchRunBuildClient interface {
	ResolveBuildSelectionMetadata(ctx context.Context, appID string, selection api.BuildSelection) (*api.BuildVersionDetail, error)
	ResolveBranch(ctx context.Context, appID, branchID, branchName string) (*api.BuildBranch, error)
}

// UploadedBuild is a finished upload and the branch it landed on.
type UploadedBuild struct {
	AppID        string
	BranchID     string
	BranchName   string
	BuildID      string
	BuildVersion string
	// DefaultBuildChanged is true only when this upload pinned its branch to
	// itself (--pin). Other uploads never change a pin.
	DefaultBuildChanged bool
}

// BranchRunBuild returns the build a run with no build choice uses for the
// selected branch, and whether that branch follows Latest or is pinned.
func BranchRunBuild(ctx context.Context, client BranchRunBuildClient, appID string, selection api.BuildSelection) (*api.BuildVersionDetail, api.BuildSelectionMode, error) {
	build, err := client.ResolveBuildSelectionMetadata(ctx, appID, selection)
	if err != nil {
		return nil, "", fmt.Errorf("could not select the app's build: %w", err)
	}
	branch, err := client.ResolveBranch(ctx, appID, build.BranchID, "")
	if err != nil {
		return nil, "", fmt.Errorf("could not read the selected branch's build setting: %w", err)
	}
	if branch.PinnedBuildID != nil {
		return build, api.BuildSelectionModePinned, nil
	}
	return build, api.BuildSelectionModeLatest, nil
}

// UploadedBuildBranchSummary names the branch an upload landed on and the build
// a plain run on that branch now uses. A failed lookup returns the bare branch
// name with the error.
func UploadedBuildBranchSummary(ctx context.Context, client BranchRunBuildClient, upload UploadedBuild) (string, error) {
	if upload.DefaultBuildChanged {
		return branchPinnedToThisUpload(upload.BranchName), nil
	}
	selection := api.BuildSelection{BranchID: upload.BranchID}
	if upload.BranchID == "" {
		selection.BranchName = upload.BranchName
	}
	runBuild, mode, err := BranchRunBuild(ctx, client, upload.AppID, selection)
	if err != nil {
		return upload.BranchName, err
	}
	switch {
	case mode == api.BuildSelectionModePinned && runBuild.ID == upload.BuildID:
		return branchPinnedToThisUpload(upload.BranchName), nil
	case mode == api.BuildSelectionModePinned:
		return fmt.Sprintf("%s (pinned to %s; runs that don't choose a build keep using %s)", upload.BranchName, runBuild.Version, runBuild.Version), nil
	case runBuild.ID == upload.BuildID:
		return fmt.Sprintf("%s (Latest build: runs use this upload)", upload.BranchName), nil
	default:
		return fmt.Sprintf("%s (Latest build: runs use %s)", upload.BranchName, runBuild.Version), nil
	}
}

func branchPinnedToThisUpload(branchName string) string {
	return fmt.Sprintf("%s (pinned to this build)", branchName)
}
