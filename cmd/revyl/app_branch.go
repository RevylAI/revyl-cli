package main

import (
	"encoding/json"
	"fmt"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/buildselection"
	"github.com/revyl/cli/internal/ui"
	"github.com/spf13/cobra"
)

const appBranchPinLong = `Pin a branch to one build. Runs that use the branch without picking a build use it.

Uploads from revyl build, revyl build upload, or revyl init and Expo imports
pin the branch to the new build, even when it was following Latest. To keep
this build while uploads continue, upload with --no-set-current.

In a local revyl dev loop, a fresh initial build or an initial
Expo/React Native DerivedData upload pins the branch. Reused native artifacts
and later rebuild uploads never change the pin. revyl dev --remote pins its
branch, the app's default branch unless you pass --branch, to every remote
build it makes, including each rebuild. Deleting the pinned build puts the
branch back on Latest; revyl app branch unpin does the same.`

func init() {
	branches := &cobra.Command{Use: "branch", Short: "Manage an app's build branches"}
	appCmd.AddCommand(branches)
	commands := []struct {
		use, short, long string
		args             int
		run              func(*cobra.Command, *api.Client, string, []string) (interface{}, error)
	}{
		{"list", "List build branches", "", 0, func(cmd *cobra.Command, c *api.Client, appID string, args []string) (interface{}, error) {
			page, _ := cmd.Flags().GetInt("page")
			size, _ := cmd.Flags().GetInt("page-size")
			return c.ListBuildBranches(cmd.Context(), appID, api.BranchListOptions{Page: page, PageSize: size})
		}},
		{"create <name>", "Create a build branch", "", 1, func(cmd *cobra.Command, c *api.Client, appID string, args []string) (interface{}, error) {
			return c.CreateBuildBranch(cmd.Context(), appID, args[0])
		}},
		{"rename <name> <new-name>", "Rename a branch without changing its identity", "", 2, func(cmd *cobra.Command, c *api.Client, appID string, args []string) (interface{}, error) {
			branch, err := c.ResolveBranch(cmd.Context(), appID, "", args[0])
			if err != nil {
				return nil, err
			}
			return c.UpdateBuildBranch(cmd.Context(), appID, branch.ID, &api.UpdateBuildBranchRequest{Name: &args[1]})
		}},
		{"default <name>", "Set the app's default build branch", "", 1, func(cmd *cobra.Command, c *api.Client, appID string, args []string) (interface{}, error) {
			branch, err := c.ResolveBranch(cmd.Context(), appID, "", args[0])
			if err != nil {
				return nil, err
			}
			if err := c.SetDefaultBuildBranch(cmd.Context(), appID, branch.ID); err != nil {
				return nil, err
			}
			isDefault := true
			branch.IsDefault = &isDefault
			return branch, nil
		}},
		{"pin <name> <build-id>", "Pin the branch's default build to an exact build", appBranchPinLong, 2, func(cmd *cobra.Command, c *api.Client, appID string, args []string) (interface{}, error) {
			branch, err := c.ResolveBranch(cmd.Context(), appID, "", args[0])
			if err != nil {
				return nil, err
			}
			pin := &args[1]
			return c.UpdateBuildBranch(cmd.Context(), appID, branch.ID, &api.UpdateBuildBranchRequest{PinnedBuildID: &pin})
		}},
		{"unpin <name>", "Make the branch's default build follow its latest build", "", 1, func(cmd *cobra.Command, c *api.Client, appID string, args []string) (interface{}, error) {
			branch, err := c.ResolveBranch(cmd.Context(), appID, "", args[0])
			if err != nil {
				return nil, err
			}
			var pin *string
			return c.UpdateBuildBranch(cmd.Context(), appID, branch.ID, &api.UpdateBuildBranchRequest{PinnedBuildID: &pin})
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
			name, _ := cmd.Flags().GetString("app")
			appID, _, err := resolveAppNameOrID(cmd, client, name)
			if err != nil {
				return err
			}
			result, err := definition.run(cmd, client, appID, args)
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
