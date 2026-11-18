package main

import (
	"fmt"
	"slices"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

// runBranches pairs the Git branch a run's tests came from with the Revyl
// branch of its build.
type runBranches struct {
	gitBranchName                      string
	gitBranchIsRepositoryDefaultBranch *bool
	buildBranchName                    string
	buildBranchIsInitialBranch         *bool
}

func enhancedTaskRunBranches(task *api.CLIEnhancedTask) runBranches {
	return runBranches{
		gitBranchName:                      task.GitBranchName,
		gitBranchIsRepositoryDefaultBranch: task.GitBranchIsRepositoryDefaultBranch,
		buildBranchName:                    task.BranchName,
		buildBranchIsInitialBranch:         task.BranchIsInitialBranch,
	}
}

func deviceSessionRunBranches(session api.CLIDeviceSessionHistoryItem) runBranches {
	return runBranches{
		gitBranchName:                      session.GitBranchName,
		gitBranchIsRepositoryDefaultBranch: session.GitBranchIsRepositoryDefaultBranch,
		buildBranchName:                    session.BranchName,
		buildBranchIsInitialBranch:         session.BranchIsInitialBranch,
	}
}

// testsAndBuildFromDifferentBranches reports whether the run's tests and build
// came from different branches, and whether both branches are known. The
// repository's default Git branch and the app's initial Revyl branch count as
// one branch only when the run recorded both facts.
func (branches runBranches) testsAndBuildFromDifferentBranches() (differ bool, known bool) {
	if branches.gitBranchName == "" || branches.buildBranchName == "" {
		return false, false
	}
	if branches.gitBranchName == branches.buildBranchName {
		return false, true
	}
	recordedTrue := func(fact *bool) bool { return fact != nil && *fact }
	return !(recordedTrue(branches.gitBranchIsRepositoryDefaultBranch) && recordedTrue(branches.buildBranchIsInitialBranch)), true
}

// differenceLine is the sentence that tells a reader runLabel's tests and build
// came from different branches, or "" when they didn't or a branch is unknown.
func (branches runBranches) differenceLine(runLabel string) string {
	if differ, _ := branches.testsAndBuildFromDifferentBranches(); !differ {
		return ""
	}
	return fmt.Sprintf("%s's tests came from Git branch \"%s\" and its build from Revyl branch \"%s\".", runLabel, branches.gitBranchName, branches.buildBranchName)
}

func addRunBranchFacts(output map[string]interface{}, branches runBranches) {
	if branches.gitBranchIsRepositoryDefaultBranch != nil {
		output["git_branch_is_repository_default_branch"] = *branches.gitBranchIsRepositoryDefaultBranch
	}
	if branches.buildBranchIsInitialBranch != nil {
		output["branch_is_initial_branch"] = *branches.buildBranchIsInitialBranch
	}
	if differ, known := branches.testsAndBuildFromDifferentBranches(); known {
		output["tests_and_build_from_different_branches"] = differ
	}
}

// runNoteLines are the sentences printed under a history table about one run:
// that its tests and build came from different branches, and that its
// checkout had test files with uncommitted changes. Each is left out when it
// isn't so or the run didn't record it.
func runNoteLines(runLabel string, branches runBranches, hasUncommittedDefinitionChanges *bool, gitCommitSHA string) []string {
	var lines []string
	if line := branches.differenceLine(runLabel); line != "" {
		lines = append(lines, line)
	}
	if hasUncommittedDefinitionChanges != nil && *hasUncommittedDefinitionChanges {
		uncommittedLine := runLabel + "'s checkout had test files with uncommitted changes"
		if gitCommitSHA != "" {
			uncommittedLine += fmt.Sprintf(", so they can differ from Git commit %s", truncatePrefix(gitCommitSHA, 12))
		}
		lines = append(lines, uncommittedLine+".")
	}
	return lines
}

// runListNoteLines are the sentences printed under a list of count runs: said
// once of everyRunLabel when every run has the same ones, else each run's own
// under runLabel(index).
func runListNoteLines(count int, runLabel func(index int) string, everyRunLabel string, noteLines func(index int, runLabel string) []string) []string {
	if count > 1 {
		shared := noteLines(0, everyRunLabel)
		every := len(shared) > 0
		for index := 1; every && index < count; index++ {
			every = slices.Equal(noteLines(index, everyRunLabel), shared)
		}
		if every {
			return shared
		}
	}
	var lines []string
	for index := 0; index < count; index++ {
		lines = append(lines, noteLines(index, runLabel(index))...)
	}
	return lines
}

func runNumberLabel(index int) string {
	return fmt.Sprintf("Run #%d", index+1)
}

func historyListNoteLines(items []api.CLIEnhancedHistoryItem) []string {
	return runListNoteLines(len(items), runNumberLabel, "Every run", func(index int, runLabel string) []string {
		task := items[index].EnhancedTask
		if task == nil {
			return nil
		}
		return runNoteLines(runLabel, enhancedTaskRunBranches(task), task.HasUncommittedDefinitionChanges, task.GitCommitSHA)
	})
}

func printRunNoteLines(lines []string) {
	if len(lines) == 0 {
		return
	}
	ui.Println()
	for _, line := range lines {
		ui.PrintDim("%s", line)
	}
}
