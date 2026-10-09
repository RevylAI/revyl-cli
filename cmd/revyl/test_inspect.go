package main

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func newTestInspectCommand() *cobra.Command {
	command := &cobra.Command{Use: "inspect <test-id>", Short: "Read a current test definition, device targets and saved configuration", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		id, err := uuid.Parse(args[0])
		if err != nil {
			return fmt.Errorf("test-id must be a UUID from test query or execution history")
		}
		client, err := atlasClient(cmd)
		if err != nil {
			return err
		}
		definition, err := client.GetTest(cmd.Context(), id.String())
		if err != nil {
			return err
		}
		jsonOutput, _ := cmd.Flags().GetBool("json")
		if !jsonOutput {
			fmt.Fprintln(cmd.ErrOrStderr(), "Current saved definition; past executions may have used a different version or overrides.")
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(definition)
	}}
	command.Flags().Bool("json", false, "Output the current definition as JSON")
	return command
}
