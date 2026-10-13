package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/execution"
	"github.com/revyl/cli/internal/ui"
	"github.com/spf13/cobra"
)

var testBuildCmd = &cobra.Command{Use: "build", Short: "Manage a test's saved branch and build choice"}
var testBuildShowCmd = &cobra.Command{Use: "show <test-name|id>", Args: cobra.ExactArgs(1), Short: "Show a test's saved branch and build", RunE: runTestBuildShow}
var testBuildSetCmd = &cobra.Command{Use: "set <test-name|id>", Args: cobra.ExactArgs(1), Short: "Save a branch and build choice: Automatic, Latest build, Exact build, or All branches · Latest build", RunE: runTestBuildSet}
var testBuildClearCmd = &cobra.Command{Use: "clear <test-name|id>", Args: cobra.ExactArgs(1), Short: "Restore Default branch: the app's default branch and its default build", RunE: runTestBuildClear}

func init() {
	testCmd.AddCommand(testBuildCmd)
	testBuildCmd.AddCommand(testBuildShowCmd, testBuildSetCmd, testBuildClearCmd)
	testBuildSetCmd.Flags().String("branch", "", "Revyl branch name to save")
	testBuildSetCmd.Flags().String("build-selection", "", "Build choice: inherit (Automatic: the branch's default build), latest or pinned")
	testBuildSetCmd.Flags().String("build-id", "", "Exact build ID to save")
	testBuildSetCmd.Flags().Bool(allBranchesFlagName, false, "Save All branches · Latest build: each run uses the newest build across all of the app's branches")
}

func runTestBuildShow(cmd *cobra.Command, args []string) error {
	testID, client, err := resolveTestClient(cmd, args[0])
	if err != nil {
		return err
	}
	test, err := client.GetTest(cmd.Context(), testID)
	if err != nil {
		return err
	}
	selection, err := api.NewSavedBuildSelection(test.BranchID, string(test.BuildSelection), test.PinnedBuildID)
	if err != nil {
		return fmt.Errorf("read saved build selector: %w", err)
	}
	legacyVersion := ""
	if selection.BranchID == nil && selection.PinnedBuildID == nil && selection.BuildSelection == api.BuildSelectionModeInherit {
		legacyVersion = test.PinnedVersion
	}
	if jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json"); jsonOutput {
		return writeSavedTestBuildSelection(cmd, testID, selection, legacyVersion)
	}
	if legacyVersion != "" {
		ui.PrintKeyValue("Branch:", "Not recorded (saved before branches)")
		ui.PrintKeyValue("Build:", fmt.Sprintf("Exact build %s", legacyVersion))
		return nil
	}
	branch := "Default branch"
	if selection.BuildSelection == api.BuildSelectionModeLatestAllBranches {
		branch = "All branches"
	}
	if selection.BranchID != nil {
		saved, err := client.ResolveBranch(cmd.Context(), test.AppID, *selection.BranchID, "")
		if err != nil {
			return fmt.Errorf("read saved branch: %w", err)
		}
		branch = fmt.Sprintf("%s (%s)", saved.Name, saved.ID)
	}
	build := "Automatic"
	switch selection.BuildSelection {
	case api.BuildSelectionModeLatest, api.BuildSelectionModeLatestAllBranches:
		build = "Latest build"
	case api.BuildSelectionModePinned:
		pinned, err := client.GetBuildVersionDetail(cmd.Context(), *selection.PinnedBuildID, false)
		var apiErr *api.APIError
		switch {
		case errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound:
			build = fmt.Sprintf("Exact build %s (unavailable)", *selection.PinnedBuildID)
		case err != nil:
			return fmt.Errorf("read saved exact build: %w", err)
		default:
			build = fmt.Sprintf("Exact build %s (%s)", pinned.Version, pinned.ID)
		}
	}
	ui.PrintKeyValue("Branch:", branch)
	ui.PrintKeyValue("Build:", build)
	return nil
}

func runTestBuildSet(cmd *cobra.Command, args []string) error {
	branchName, _ := cmd.Flags().GetString("branch")
	mode, _ := cmd.Flags().GetString("build-selection")
	buildID, _ := cmd.Flags().GetString("build-id")
	mode, err := resolveCommandAllBranchesSelection(cmd, mode, "branch", "build-id")
	if err != nil {
		return err
	}
	if branchName == "" && mode == "" && buildID == "" {
		return fmt.Errorf("provide --branch, --build-selection, --build-id or --all-branches; use clear to restore Default branch")
	}
	testID, client, err := resolveTestClient(cmd, args[0])
	if err != nil {
		return err
	}
	test, err := client.GetTest(cmd.Context(), testID)
	if err != nil {
		return err
	}
	branchID := ""
	if branchName == "" && buildID == "" && mode != string(api.BuildSelectionModeLatestAllBranches) {
		branchID = test.BranchID
	}
	selection, err := execution.ResolveSavedBuildSelection(cmd.Context(), client, test.AppID, branchName, branchID, mode, buildID)
	if err != nil {
		return err
	}
	if _, err := client.UpdateTest(cmd.Context(), &api.UpdateTestRequest{TestID: testID, SavedBuildSelection: selection}); err != nil {
		return err
	}
	if jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json"); jsonOutput {
		return writeSavedTestBuildSelection(cmd, testID, selection, "")
	}
	ui.PrintSuccess("Saved build selection for test %s", testID)
	return nil
}

func runTestBuildClear(cmd *cobra.Command, args []string) error {
	testID, client, err := resolveTestClient(cmd, args[0])
	if err != nil {
		return err
	}
	selection, err := api.NewSavedBuildSelection("", "inherit", "")
	if err != nil {
		return err
	}
	if _, err := client.UpdateTest(cmd.Context(), &api.UpdateTestRequest{TestID: testID, SavedBuildSelection: selection}); err != nil {
		return err
	}
	if jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json"); jsonOutput {
		return writeSavedTestBuildSelection(cmd, testID, selection, "")
	}
	ui.PrintSuccess("Test %s now uses Default branch", testID)
	return nil
}

func writeSavedTestBuildSelection(cmd *cobra.Command, testID string, selection *api.SavedBuildSelection, legacyVersion string) error {
	return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
		TestID string `json:"test_id"`
		*api.SavedBuildSelection
		PinnedVersion string `json:"pinned_version,omitempty"`
	}{TestID: testID, SavedBuildSelection: selection, PinnedVersion: legacyVersion})
}
