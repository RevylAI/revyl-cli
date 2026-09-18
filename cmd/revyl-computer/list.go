package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/ui"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Args:  cobra.NoArgs,
	Short: "List your organization's assigned computers",
	Long: `List the computers assigned to the organization in your Revyl credentials.

Shows each computer's instance ID and online or offline status without opening
a shell. Use REVYL_API_KEY or sign in with 'revyl auth login'.

EXAMPLES:
  revyl-computer list
  revyl-computer list --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		devMode, _ := cmd.Flags().GetBool("dev")
		jsonOutput, _ := cmd.Flags().GetBool("json")

		token, err := auth.NewManager().GetActiveToken()
		if err != nil || token == "" {
			ui.PrintWarning("Not authenticated")
			ui.PrintInfo("Set REVYL_API_KEY or run 'revyl auth login', then 'revyl-computer list'")
			return errors.New("not authenticated")
		}

		ctx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stopSignals()
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		computers, err := api.NewClientWithDevMode(token, devMode).ListComputers(ctx)
		if err != nil {
			return fmt.Errorf("failed to list computers: %w", err)
		}
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(computers)
		}
		if len(computers.Computers) == 0 {
			ui.PrintInfo("No computers are assigned to your organization.")
			return nil
		}

		table := ui.NewTable("INSTANCE ID", "STATUS")
		for _, computer := range computers.Computers {
			table.AddRow(computer.InstanceId, string(computer.Status))
		}
		table.Render()
		return nil
	},
}

func init() {
	listCmd.Flags().Bool("json", false, "Output the computer list as JSON")
	rootCmd.AddCommand(listCmd)
}
