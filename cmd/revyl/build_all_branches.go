package main

import (
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

const (
	allBranchesFlagName       = "all-branches"
	allBranchesFlagUsage      = "Use the newest build across all of the app's branches"
	runtimeBuildSelectionHelp = `Build selection:
  Automatic (inherit): the selected branch's Latest build or pin.
  --build-selection latest: newest on that branch; ignore its pin.
  --all-branches: newest across the app; ignore Git and pins.

Branch: explicit/saved choice first. Otherwise, automatic Git branch detection
in the app's Build branches settings controls selection: enabled uses Git, then
the app default; disabled uses the app default directly. Invalid, missing, or
empty Git branches fall back with a notice. Unavailable explicit/saved choices
or pins fail. GitHub Actions and Buildkite detect the pull request source branch
(owner:branch for forks).`
)

// allBranchesConflict is a flag that can't be combined with --all-branches
// and whether the user set it.
type allBranchesConflict struct {
	flag string
	set  bool
}

// resolveAllBranchesSelection returns the build selection mode for a run or
// saved choice: latest_all_branches when allBranches is set, otherwise mode
// unchanged. All branches always means Latest across every branch, so a set
// conflict, or any mode other than latest, is refused with the flag named.
func resolveAllBranchesSelection(allBranchesFlag string, allBranches bool, selectionFlag, mode string, conflicts ...allBranchesConflict) (string, error) {
	if !allBranches {
		return mode, nil
	}
	for _, conflict := range conflicts {
		if conflict.set {
			return "", usageErrorf("--%s cannot be combined with %s; --%s uses the newest build across all of the app's branches, so drop one of them", allBranchesFlag, conflict.flag, allBranchesFlag)
		}
	}
	switch api.BuildSelectionMode(mode) {
	case "", api.BuildSelectionModeLatest, api.BuildSelectionModeLatestAllBranches:
		return string(api.BuildSelectionModeLatestAllBranches), nil
	default:
		return "", usageErrorf("--%s cannot be combined with --%s %s; --%s always uses Latest build", allBranchesFlag, selectionFlag, mode, allBranchesFlag)
	}
}

// changedFlagConflicts reports which of the named flags the user set to a
// non-default value.
func changedFlagConflicts(cmd *cobra.Command, names ...string) []allBranchesConflict {
	conflicts := make([]allBranchesConflict, 0, len(names))
	for _, name := range names {
		flag := cmd.Flags().Lookup(name)
		conflicts = append(conflicts, allBranchesConflict{
			flag: "--" + name,
			set:  flag != nil && flag.Changed && flag.Value.String() != flag.DefValue,
		})
	}
	return conflicts
}

// resolveCommandAllBranchesSelection applies the command's --all-branches flag
// to mode; conflictingFlags name the flags that choose a branch or exact build.
func resolveCommandAllBranchesSelection(cmd *cobra.Command, mode string, conflictingFlags ...string) (string, error) {
	allBranches, _ := cmd.Flags().GetBool(allBranchesFlagName)
	return resolveAllBranchesSelection(allBranchesFlagName, allBranches, "build-selection", mode, changedFlagConflicts(cmd, conflictingFlags...)...)
}

// rejectAllBranchesSelection refuses latest_all_branches where one branch must
// be chosen, such as a dev loop whose rebuilds upload to that branch.
func rejectAllBranchesSelection(mode, reason string) error {
	if api.BuildSelectionMode(mode) == api.BuildSelectionModeLatestAllBranches {
		return usageErrorf("latest_all_branches isn't available here: %s", reason)
	}
	return nil
}
