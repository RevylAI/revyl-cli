package buildselection

import "github.com/revyl/cli/internal/api"

// BranchDefaultBuildLabel names a listed branch's default build and the build
// a run on it uses now: "Latest build (v4)", "Latest build (no builds)", or
// "Pinned build (v1.2)" with the pinned build's version (its ID when the
// version is unknown). Only branch lists carry latest_build, so for a branch
// read by ID a missing one means unknown, not empty.
func BranchDefaultBuildLabel(branch api.BuildBranch) string {
	if branch.PinnedBuildID == nil {
		if branch.LatestBuild == nil {
			return "Latest build (no builds)"
		}
		return "Latest build (" + branch.LatestBuild.Version + ")"
	}
	label := *branch.PinnedBuildID
	if branch.PinnedBuildVersion != nil && *branch.PinnedBuildVersion != "" {
		label = *branch.PinnedBuildVersion
	}
	if branch.PinnedBuildAvailable != nil && !*branch.PinnedBuildAvailable {
		label += ", unavailable"
	}
	return "Pinned build (" + label + ")"
}
