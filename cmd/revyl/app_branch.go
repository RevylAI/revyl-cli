package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/buildselection"
	"github.com/revyl/cli/internal/ui"
	"github.com/spf13/cobra"
)

const appBranchPinLong = `Pin a branch to one build. Runs on the branch that don't choose a build use it
until you unpin the branch. Runs that choose Latest build use the branch's
newest build, even when the branch is pinned.

Uploads don't pin the branch unless you pass --pin to revyl build or revyl
build upload.

Unpinning, or deleting the pinned build, puts the branch back on Latest build.`

// branchCommandApp is the app a branch command acts on: its ID for requests,
// its name for messages, and the --app value to repeat in a suggested command.
type branchCommandApp struct {
	id, name, flag string
}

// resolveCommandBranch finds a branch of the app by name, naming the branch
// and app, and where to see the app's branches, when it doesn't exist.
func resolveCommandBranch(cmd *cobra.Command, c *api.Client, app branchCommandApp, name string) (*api.BuildBranch, error) {
	branch, err := c.ResolveBranch(cmd.Context(), app.id, "", name)
	if errors.Is(err, api.ErrBranchNotFound) {
		return nil, fmt.Errorf("%w; list its branches with %s",
			&api.BranchError{Err: api.ErrBranchNotFound, BranchName: name, AppName: app.name},
			cliRecoveryCommand("app", "branch", "list", "--app", quoteCLIRecoveryArgument(app.flag)))
	}
	return branch, err
}

func init() {
	branches := &cobra.Command{Use: "branch", Short: "Manage an app's build branches"}
	appCmd.AddCommand(branches)
	commands := []struct {
		use, short, long string
		args             int
		run              func(*cobra.Command, *api.Client, branchCommandApp, []string) (interface{}, error)
	}{
		{"list", "List build branches", "", 0, func(cmd *cobra.Command, c *api.Client, app branchCommandApp, args []string) (interface{}, error) {
			page, _ := cmd.Flags().GetInt("page")
			size, _ := cmd.Flags().GetInt("page-size")
			return c.ListBuildBranches(cmd.Context(), app.id, api.BranchListOptions{Page: page, PageSize: size})
		}},
		{"create <name>", "Create a build branch", "", 1, func(cmd *cobra.Command, c *api.Client, app branchCommandApp, args []string) (interface{}, error) {
			return c.CreateBuildBranch(cmd.Context(), app.id, args[0])
		}},
		{"rename <name> <new-name>", "Rename a branch without changing its identity", "", 2, func(cmd *cobra.Command, c *api.Client, app branchCommandApp, args []string) (interface{}, error) {
			if err := api.ValidateBranchName(args[1]); err != nil {
				return nil, analytics.WithFailureClass(err, analytics.FailureClassUsage)
			}
			branch, err := resolveCommandBranch(cmd, c, app, args[0])
			if err != nil {
				return nil, err
			}
			return c.UpdateBuildBranch(cmd.Context(), app.id, branch.ID, &api.UpdateBuildBranchRequest{Name: &args[1]})
		}},
		{"default <name>", "Set the app's default build branch", "", 1, func(cmd *cobra.Command, c *api.Client, app branchCommandApp, args []string) (interface{}, error) {
			branch, err := resolveCommandBranch(cmd, c, app, args[0])
			if err != nil {
				return nil, err
			}
			if err := c.SetDefaultBuildBranch(cmd.Context(), app.id, branch.ID); err != nil {
				return nil, err
			}
			isDefault := true
			branch.IsDefault = &isDefault
			return branch, nil
		}},
		{"pin <name> <build-id>", "Pin a branch to one build", appBranchPinLong, 2, func(cmd *cobra.Command, c *api.Client, app branchCommandApp, args []string) (interface{}, error) {
			if _, err := uuid.Parse(args[1]); err != nil {
				return nil, usageErrorf("%q is not a build ID; find the ID of the build to pin with %s", args[1],
					cliRecoveryCommand("build", "list", "--app", quoteCLIRecoveryArgument(app.flag), "--branch", quoteCLIRecoveryArgument(args[0])))
			}
			branch, err := resolveCommandBranch(cmd, c, app, args[0])
			if err != nil {
				return nil, err
			}
			pin := &args[1]
			return c.UpdateBuildBranch(cmd.Context(), app.id, branch.ID, &api.UpdateBuildBranchRequest{PinnedBuildID: &pin})
		}},
		{"unpin <name>", "Unpin a branch so it follows Latest build", "", 1, func(cmd *cobra.Command, c *api.Client, app branchCommandApp, args []string) (interface{}, error) {
			branch, err := resolveCommandBranch(cmd, c, app, args[0])
			if err != nil {
				return nil, err
			}
			var pin *string
			return c.UpdateBuildBranch(cmd.Context(), app.id, branch.ID, &api.UpdateBuildBranchRequest{PinnedBuildID: &pin})
		}},
	}
	for _, definition := range commands {
		cmd := &cobra.Command{Use: definition.use, Short: definition.short, Long: definition.long, Args: cobra.ExactArgs(definition.args)}
		cmd.Flags().String("app", "", "App name or ID (required)")
		cmd.Flags().Bool("json", false, "Output JSON")
		_ = cmd.MarkFlagRequired("app")
		if definition.args == 0 {
			cmd.Flags().Int("page", 1, "Page number")
			cmd.Flags().Int("page-size", 50, "Branches per page (maximum 100)")
		}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			key, err := getAPIKey()
			if err != nil {
				return err
			}
			dev, _ := cmd.Flags().GetBool("dev")
			client := api.NewClientWithDevMode(key, dev)
			appFlag, _ := cmd.Flags().GetString("app")
			appID, appName, err := resolveAppNameOrID(cmd, client, appFlag)
			if err != nil {
				return err
			}
			result, err := definition.run(cmd, client, branchCommandApp{id: appID, name: appName, flag: appFlag}, args)
			if err != nil {
				return err
			}
			output, _ := cmd.Flags().GetBool("json")
			global, _ := cmd.Root().PersistentFlags().GetBool("json")
			if output || global {
				data, err := json.MarshalIndent(result, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
				return nil
			}
			if page, ok := result.(*api.BuildBranchesPage); ok {
				table := ui.NewTable("BRANCH", "BRANCH ID", "DEFAULT BUILD", "BUILD ID")
				for _, branch := range page.Branches {
					table.AddRow(buildBranchListName(branch), branch.ID, buildselection.BranchDefaultBuildLabel(branch), buildBranchRunBuildID(branch))
				}
				table.Render()
				ui.PrintDim("%d total branches", page.Total)
			} else {
				branch := result.(*api.BuildBranch)
				ui.PrintSuccess("Branch: %s (%s)", branch.Name, branch.ID)
			}
			return nil
		}
		branches.AddCommand(cmd)
	}
}

func buildBranchListName(branch api.BuildBranch) string {
	if branch.IsDefault != nil && *branch.IsDefault {
		return branch.Name + " (default)"
	}
	return branch.Name
}

// buildBranchRunBuildID is the ID of the build a run on a listed branch uses
// now, or "-" when the branch follows Latest and has no builds.
func buildBranchRunBuildID(branch api.BuildBranch) string {
	switch {
	case branch.PinnedBuildID != nil:
		return *branch.PinnedBuildID
	case branch.LatestBuild != nil:
		return branch.LatestBuild.Id.String()
	default:
		return "-"
	}
}
