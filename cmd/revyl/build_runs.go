package main

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func newBuildRunsCommand() *cobra.Command {
	command := &cobra.Command{Use: "runs <build-id>", Short: "Page through exactly attributed executions of one build", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		id, err := uuid.Parse(args[0])
		if err != nil {
			return fmt.Errorf("build-id must be a UUID from build list")
		}
		page, _ := cmd.Flags().GetInt("page")
		limit, _ := cmd.Flags().GetInt("limit")
		if page < 1 || page > 10000 || limit < 1 || limit > 100 {
			return fmt.Errorf("page must be 1-10000 and limit 1-100")
		}
		client, err := atlasClient(cmd)
		if err != nil {
			return err
		}
		result, err := client.GetBuildRuns(cmd.Context(), id.String(), page, limit)
		if err != nil {
			return err
		}
		jsonOutput, _ := cmd.Flags().GetBool("json")
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%d runs on page %d (%d total)\n", len(result.Items), result.Page, result.Total)
		for _, run := range result.Items {
			status := "unknown"
			if run.Status != nil {
				status = *run.Status
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n", run.ExecutionID, status)
		}
		if result.HasNext {
			fmt.Fprintf(cmd.OutOrStdout(), "Next: revyl build runs %s --page %d --limit %d\n", id.String(), page+1, limit)
		}
		return nil
	}}
	command.Flags().Int("page", 1, "Page number (1-10000)")
	command.Flags().Int("limit", 20, "Executions per page (1-100)")
	command.Flags().Bool("json", false, "Output stable JSON with pagination")
	return command
}
