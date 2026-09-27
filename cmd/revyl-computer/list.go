package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
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

Shows each computer's name, online or offline status, last-seen time, and
instance ID without opening a shell. Filter by status or a name prefix. Use
REVYL_API_KEY or sign in with 'revyl auth login'.

EXAMPLES:
  revyl-computer list
  revyl-computer list --status online
  revyl-computer list --name Revyl-Mac-Studio
  revyl-computer list --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		devMode, _ := cmd.Flags().GetBool("dev")
		jsonOutput, _ := cmd.Flags().GetBool("json")
		status, _ := cmd.Flags().GetString("status")
		namePrefix, _ := cmd.Flags().GetString("name")
		if status != "" && status != "online" && status != "offline" {
			return errors.New("invalid --status; use online or offline")
		}
		if namePrefix != "" && !computerNamePattern.MatchString(namePrefix) {
			return errors.New("invalid --name; use a name prefix from 'revyl-computer list'")
		}

		token, err := auth.NewManager().GetActiveToken()
		if err != nil || token == "" {
			ui.PrintWarning("Not authenticated")
			ui.PrintInfo("Set REVYL_API_KEY or run 'revyl auth login', then 'revyl-computer list'")
			return errors.New("not authenticated")
		}

		ctx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stopSignals()
		ctx, cancel := context.WithTimeout(ctx, busyRetry.budget+api.DefaultTimeout)
		defer cancel()

		computers, err := retryWhileBusy(ctx, "listing computers", api.NewClientWithDevMode(token, devMode).ListComputers)
		if err != nil {
			return fmt.Errorf("failed to list computers: %w", err)
		}
		computers.Computers = filterComputers(
			computers.Computers, status, namePrefix,
		)
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(computers)
		}
		if len(computers.Computers) == 0 {
			if status != "" || namePrefix != "" {
				ui.PrintInfo("No computers match the selected filters.")
			} else {
				ui.PrintInfo("No computers are assigned to your organization.")
			}
			return nil
		}

		sort.SliceStable(computers.Computers, func(i, j int) bool {
			left := strings.ToLower(computers.Computers[i].Name)
			right := strings.ToLower(computers.Computers[j].Name)
			if left == right {
				return computers.Computers[i].InstanceId < computers.Computers[j].InstanceId
			}
			return left < right
		})

		online := 0
		table := ui.NewTable("NAME", "STATUS", "LAST SEEN", "INSTANCE ID")
		for _, computer := range computers.Computers {
			if computer.Status == api.CustomerComputerStatusOnline {
				online++
			}
			table.AddRow(
				computer.Name,
				string(computer.Status),
				formatComputerLastSeen(computer.LastSeenAt, time.Now()),
				computer.InstanceId,
			)
		}
		table.Render()
		ui.PrintInfo("%d computers: %d online, %d offline", len(computers.Computers), online, len(computers.Computers)-online)
		return nil
	},
}

func filterComputers(
	computers []api.CustomerComputer, status string, namePrefix string,
) []api.CustomerComputer {
	filtered := make([]api.CustomerComputer, 0, len(computers))
	prefix := strings.ToLower(namePrefix)
	for _, computer := range computers {
		if status != "" && string(computer.Status) != status {
			continue
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(computer.Name), prefix) {
			continue
		}
		filtered = append(filtered, computer)
	}
	return filtered
}

func formatComputerLastSeen(lastSeenAt *time.Time, now time.Time) string {
	if lastSeenAt == nil {
		return "unknown"
	}
	age := now.Sub(*lastSeenAt)
	if age < 0 {
		age = 0
	}
	if age < time.Minute {
		return "just now"
	}
	if age < time.Hour {
		return fmt.Sprintf("%dm ago", int(age/time.Minute))
	}
	if age < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(age/time.Hour))
	}
	return fmt.Sprintf("%dd ago", int(age/(24*time.Hour)))
}

func init() {
	listCmd.Flags().Bool("json", false, "Output the computer list as JSON")
	listCmd.Flags().String("status", "", "Filter by status: online or offline")
	listCmd.Flags().String("name", "", "Filter by a case-insensitive name prefix")
	rootCmd.AddCommand(listCmd)
}
