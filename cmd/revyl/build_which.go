package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/buildselection"
	"github.com/revyl/cli/internal/ui"
	"github.com/spf13/cobra"
)

type buildWhichResult struct {
	AppID                string                 `json:"app_id"`
	AppName              string                 `json:"app_name"`
	BranchID             string                 `json:"branch_id"`
	BranchName           string                 `json:"branch_name"`
	BranchSource         api.BranchSource       `json:"branch_source"`
	GitBranchName        string                 `json:"git_branch_name,omitempty"`
	BuildID              string                 `json:"build_id"`
	BuildVersion         string                 `json:"build_version"`
	BranchBuildSelection api.BuildSelectionMode `json:"branch_build_selection"`
}

func init() {
	command := &cobra.Command{
		Use:   "which",
		Short: "Show the branch and build an app would use",
		Long: `Show the build a run on an app would use, without starting a run.

Uses --branch when supplied, otherwise the detected Git branch or the app's
default branch, then that branch's default build: Latest build or its pinned
build. Reports when the detected Git branch has no usable build and the app's
default branch is used.

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
	command.Flags().String("branch", "", "Revyl branch (default: your Git branch, else the app's default branch)")
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
		return fmt.Errorf("--branch requires a Revyl branch name; omit it to use your Git branch or the app's default branch")
	}
	selection := api.BuildSelection{BranchName: branchName}
	if err := selection.Validate(); err != nil {
		return err
	}
	gitBranchName, err := commandWorkingGitBranch(cmd)
	if err != nil {
		return err
	}
	selection.GitBranchName = gitBranchName
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
	fellBack := build.BranchSource.FellBackFromGitBranch(gitBranchName)
	source, reason := api.BranchSourceDefaultBranch, "app default branch"
	if branchName != "" {
		source, reason = api.BranchSourceExplicitBranch, "explicit --branch"
	} else if gitBranchName != "" && !fellBack {
		source, reason = api.BranchSourceGitBranch, "Git branch"
	} else if fellBack {
		reason = "app default branch; Git branch has no usable build"
	}
	result := buildWhichResult{
		AppID: appID, AppName: appName,
		BranchID: build.BranchID, BranchName: build.BranchName, BranchSource: source, GitBranchName: gitBranchName,
		BuildID: build.ID, BuildVersion: build.Version, BranchBuildSelection: mode,
	}
	if fellBack {
		fmt.Fprintln(cmd.ErrOrStderr(), ui.AppBranchFallbackNotice(appName, gitBranchName, build.BranchName))
	}
	jsonOutput, _ := cmd.Flags().GetBool("json")
	globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json")
	if jsonOutput || globalJSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}
	defaultBuild := "Latest build"
	if mode == api.BuildSelectionModePinned {
		defaultBuild = "Pinned build"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "App: %s (%s)\nBranch: %s (%s)\nReason: %s\nBuild: %s (%s)\nDefault build: %s\n",
		result.AppName, result.AppID, result.BranchName, result.BranchID, reason, result.BuildVersion, result.BuildID, defaultBuild)
	return nil
}

// resolveBranchRunBuild returns the build a run with no build choice uses for
// the selected branch, and whether that branch follows Latest or is pinned.
func resolveBranchRunBuild(ctx context.Context, client *api.Client, appID string, selection api.BuildSelection) (*api.BuildVersionDetail, api.BuildSelectionMode, error) {
	return buildselection.BranchRunBuild(ctx, client, appID, selection)
}

// uploadedBuild is a finished upload and the branch it landed on.
type uploadedBuild = buildselection.UploadedBuild

// printUploadedBuildBranch prints the branch an upload landed on and which build
// a plain run on that branch now uses. labelFormat is the caller's "Branch:"
// line format.
func printUploadedBuildBranch(ctx context.Context, client *api.Client, labelFormat string, upload uploadedBuild) {
	reportUploadedBuildBranch(ctx, client, upload, func(summary string) { ui.PrintInfo(labelFormat, summary) })
}

// reportUploadedBuildBranch hands printSummary the branch an upload landed on
// and which build a plain run on that branch now uses. A failed check only
// warns: the upload already succeeded.
func reportUploadedBuildBranch(ctx context.Context, client *api.Client, upload uploadedBuild, printSummary func(summary string)) {
	if upload.BranchName == "" || ui.IsQuietMode() {
		return
	}
	summary, err := uploadedBuildBranchSummary(ctx, client, upload)
	printSummary(summary)
	if err != nil {
		ui.PrintWarning("Couldn't check which build runs on %s: %v", upload.BranchName, err)
	}
}

func uploadedBuildBranchSummary(ctx context.Context, client *api.Client, upload uploadedBuild) (string, error) {
	return buildselection.UploadedBuildBranchSummary(ctx, client, upload)
}
