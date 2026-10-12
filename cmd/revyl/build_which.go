package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
	"github.com/spf13/cobra"
)

type buildWhichResult struct {
	AppID                string                 `json:"app_id"`
	AppName              string                 `json:"app_name"`
	BranchID             string                 `json:"branch_id"`
	BranchName           string                 `json:"branch_name"`
	BranchSource         string                 `json:"branch_source"`
	BuildID              string                 `json:"build_id"`
	BuildVersion         string                 `json:"build_version"`
	BranchBuildSelection api.BuildSelectionMode `json:"branch_build_selection"`
}

func init() {
	command := &cobra.Command{
		Use:   "which",
		Short: "Show the branch and build an app would use",
		Long: `Show the runnable build selected for an app without starting a run.

Uses --branch when supplied, otherwise the app's default branch, then that
branch's default build: Latest build or an exact build.

This inspects app-level selection only. A test or workflow can have its own
saved branch or exact build, which this command does not inspect.

Examples:
  revyl build which --app "My App"
  revyl build which --app "My App" --branch staging
  revyl build which --app <app-id> --json`,
		Args: cobra.NoArgs,
		RunE: runBuildWhich,
	}
	command.Flags().String("app", "", "App name or ID (required)")
	command.Flags().String("branch", "", "Revyl branch (defaults to the app default branch)")
	command.Flags().Bool("json", false, "Output the selected branch and build as JSON")
	_ = command.MarkFlagRequired("app")
	buildCmd.AddCommand(command)
}

func runBuildWhich(cmd *cobra.Command, _ []string) error {
	app, _ := cmd.Flags().GetString("app")
	if strings.TrimSpace(app) == "" {
		return fmt.Errorf("--app requires an app name or ID")
	}
	branchName := commandBuildBranch(cmd)
	if cmd.Flags().Changed("branch") && strings.TrimSpace(branchName) == "" {
		return fmt.Errorf("--branch requires a Revyl branch name; omit it to use the app default branch")
	}
	selection := api.BuildSelection{BranchName: branchName}
	if err := selection.Validate(); err != nil {
		return err
	}
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	appID, appName, err := resolveAppNameOrID(cmd, client, app)
	if err != nil {
		return err
	}
	build, mode, err := resolveBranchRunBuild(cmd.Context(), client, appID, selection)
	if err != nil {
		return err
	}
	source, reason := "default_branch", "app default branch"
	if branchName != "" {
		source, reason = "explicit_branch", "explicit --branch"
	}
	result := buildWhichResult{
		AppID: appID, AppName: appName,
		BranchID: build.BranchID, BranchName: build.BranchName, BranchSource: source,
		BuildID: build.ID, BuildVersion: build.Version, BranchBuildSelection: mode,
	}
	jsonOutput, _ := cmd.Flags().GetBool("json")
	globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json")
	if jsonOutput || globalJSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}
	defaultBuild := "Latest build"
	if mode == api.BuildSelectionModePinned {
		defaultBuild = "Exact build"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "App: %s (%s)\nBranch: %s (%s)\nReason: %s\nBuild: %s (%s)\nDefault build: %s\n",
		result.AppName, result.AppID, result.BranchName, result.BranchID, reason, result.BuildVersion, result.BuildID, defaultBuild)
	return nil
}

// resolveBranchRunBuild returns the build a run with no build choice uses for
// the selected branch, and whether that branch follows Latest or is pinned.
func resolveBranchRunBuild(ctx context.Context, client *api.Client, appID string, selection api.BuildSelection) (*api.BuildVersionDetail, api.BuildSelectionMode, error) {
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

// uploadedBuild is a finished upload and the branch it landed on.
type uploadedBuild struct {
	AppID        string
	BranchID     string
	BranchName   string
	BuildID      string
	BuildVersion string
	// DefaultBuildChanged is true when the upload made itself the branch's
	// default build (a normal upload, --set-current).
	DefaultBuildChanged bool
}

// printUploadedBuildBranch prints the branch an upload landed on and which build
// a plain run on that branch now uses. labelFormat is the caller's "Branch:"
// line format. A failed check only warns: the upload already succeeded.
func printUploadedBuildBranch(ctx context.Context, client *api.Client, labelFormat string, upload uploadedBuild) {
	if upload.BranchName == "" || ui.IsQuietMode() {
		return
	}
	summary, err := uploadedBuildBranchSummary(ctx, client, upload)
	ui.PrintInfo(labelFormat, summary)
	if err != nil {
		ui.PrintWarning("Couldn't check which build runs on %s: %v", upload.BranchName, err)
	}
}

func uploadedBuildBranchSummary(ctx context.Context, client *api.Client, upload uploadedBuild) (string, error) {
	if upload.DefaultBuildChanged {
		return branchDefaultBuildIsNow(upload.BranchName, upload.BuildVersion), nil
	}
	selection := api.BuildSelection{BranchID: upload.BranchID}
	if upload.BranchID == "" {
		selection.BranchName = upload.BranchName
	}
	runBuild, mode, err := resolveBranchRunBuild(ctx, client, upload.AppID, selection)
	if err != nil {
		return upload.BranchName, err
	}
	switch {
	case mode == api.BuildSelectionModePinned && runBuild.ID == upload.BuildID:
		return branchDefaultBuildIsNow(upload.BranchName, upload.BuildVersion), nil
	case mode == api.BuildSelectionModePinned:
		return fmt.Sprintf("%s (default build stays %s; runs won't use this upload)", upload.BranchName, runBuild.Version), nil
	case runBuild.ID == upload.BuildID:
		return fmt.Sprintf("%s (Latest build: runs use this upload)", upload.BranchName), nil
	default:
		return fmt.Sprintf("%s (Latest build: runs use %s)", upload.BranchName, runBuild.Version), nil
	}
}

// branchDefaultBuildIsNow describes an upload that became its branch's default
// build.
func branchDefaultBuildIsNow(branchName, buildVersion string) string {
	return fmt.Sprintf("%s (default build is now %s)", branchName, buildVersion)
}
