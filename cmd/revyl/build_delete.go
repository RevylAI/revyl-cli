package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

var buildDeleteCmd = &cobra.Command{
	Use:   "delete <build-version-id>",
	Short: "Delete an uploaded build version",
	Long: `Delete one uploaded build version by its ID. This cannot be undone.

Find build version IDs with "revyl build list --app <id>". Without --force,
the command asks for confirmation and fails when it cannot prompt.`,
	Example: `  revyl build delete <build-version-id>
  revyl build delete <build-version-id> --force
  revyl build delete <build-version-id> --force --json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE:         runBuildDelete,
}

type buildDeleteOutput struct {
	Success        bool   `json:"success"`
	BuildVersionID string `json:"build_version_id"`
	Message        string `json:"message,omitempty"`
}

func init() {
	buildCmd.AddCommand(buildDeleteCmd)
	buildDeleteCmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")
	buildDeleteCmd.Flags().Bool("json", false, "Output result as JSON")
}

// runBuildDelete deletes one build version after confirmation.
func runBuildDelete(cmd *cobra.Command, args []string) error {
	versionID := strings.TrimSpace(args[0])
	if versionID == "" {
		return fmt.Errorf("build version ID is required")
	}
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	if force, _ := cmd.Flags().GetBool("force"); !force {
		confirmed, promptErr := ui.PromptConfirm(fmt.Sprintf("Delete build version %s? This cannot be undone.", versionID), false)
		if promptErr != nil {
			return fmt.Errorf("could not confirm deleting build version %s: rerun with --force to skip the prompt", versionID)
		}
		if !confirmed {
			ui.PrintInfo("Cancelled")
			return nil
		}
	}

	resp, err := client.DeleteBuildVersion(cmd.Context(), versionID)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return fmt.Errorf("build version %s not found; run 'revyl build list --app <id>' to see build version IDs", versionID)
		}
		return fmt.Errorf("failed to delete build version %s: %w", versionID, err)
	}

	if jsonOutput {
		return encodeJSON(buildDeleteOutput{Success: true, BuildVersionID: versionID, Message: resp.Message})
	}
	ui.PrintSuccess("Deleted build version %s", versionID)
	return nil
}
