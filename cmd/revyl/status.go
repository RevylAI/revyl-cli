// Package main provides test status and history commands.
package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

var (
	statusOutputJSON  bool
	statusOpen        bool
	historyOutputJSON bool
	historyLimit      int
)

func init() {
	testStatusCmd.Flags().BoolVar(&statusOutputJSON, "json", false, "Output results as JSON")
	testStatusCmd.Flags().BoolVar(&statusOpen, "open", false, "Open report in browser")

	testHistoryCmd.Flags().BoolVar(&historyOutputJSON, "json", false, "Output results as JSON")
	testHistoryCmd.Flags().IntVar(&historyLimit, "limit", 10, "Maximum number of executions to show")
}

// testStatusCmd shows the latest execution status for a test.
var testStatusCmd = &cobra.Command{
	Use:   "status <name|id>",
	Short: "Show latest execution status",
	Long: `Show the latest execution status for a test.

Displays status, duration, step progress, and report link.
Accepts test names (config aliases), display names, or UUIDs.

Examples:
  revyl test status login-flow
  revyl test status login-flow --json
  revyl test status login-flow --open`,
	Args: cobra.ExactArgs(1),
	RunE: runTestStatus,
}

// testHistoryCmd shows execution history for a test.
var testHistoryCmd = &cobra.Command{
	Use:   "history <name|id>",
	Short: "Show execution history",
	Long: `Show execution history for a test in a table format.

Displays recent executions with status, duration, and step progress.
Accepts test names (config aliases), display names, or UUIDs.

Examples:
  revyl test history login-flow
  revyl test history login-flow --limit 20
  revyl test history login-flow --json`,
	Args: cobra.ExactArgs(1),
	RunE: runTestHistory,
}

// runTestStatus shows the latest execution status for a test.
func runTestStatus(cmd *cobra.Command, args []string) error {
	// Honor global --json
	jsonOutput := statusOutputJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	devMode, _ := cmd.Flags().GetBool("dev")

	_, cfg, client, err := loadConfigAndClient(devMode)
	if err != nil {
		return err
	}

	testNameOrID := args[0]

	if jsonOutput {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
	}

	testID, testName, err := resolveTestID(cmd.Context(), testNameOrID, cfg, client)
	if err != nil {
		ui.PrintError("%v", err)
		return fmt.Errorf("test not found")
	}

	// Use the input name for display if resolveTestID didn't return one
	displayName := testName
	if displayName == "" {
		displayName = testNameOrID
	}

	if !jsonOutput {
		ui.StartSpinner("Fetching latest execution...")
	}
	history, err := client.GetTestEnhancedHistory(cmd.Context(), testID, 1, 0)
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to fetch execution status: %v", err)
		return err
	}

	if len(history.Items) == 0 {
		if jsonOutput {
			output := map[string]interface{}{
				"test_id": testID,
				"status":  "no_executions",
				"message": "No executions found for this test",
			}
			data, _ := json.MarshalIndent(output, "", "  ")
			fmt.Println(string(data))
			return nil
		}
		ui.PrintInfo("No executions found for '%s'", displayName)
		ui.Println()
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Run this test:", Command: fmt.Sprintf("revyl test run %s", testNameOrID)},
		})
		return nil
	}

	item := history.Items[0]
	task := item.EnhancedTask

	// Determine status and step info
	status := item.Status
	var taskID string
	var stepsCompleted, totalSteps int
	var duration string
	var startedAt, completedAt string
	var success *bool
	var hasReport bool

	if task != nil {
		taskID = task.ID
		status = task.Status
		stepsCompleted = task.StepsCompleted
		totalSteps = task.TotalSteps
		success = task.Success
		startedAt = task.StartedAt
		completedAt = task.CompletedAt
		if task.ExecutionTimeSeconds > 0 {
			duration = formatDurationSecs(task.ExecutionTimeSeconds)
		}
	} else {
		taskID = item.ID
		if item.Duration != nil {
			duration = formatDurationSecs(*item.Duration)
		}
	}
	if duration == "" && item.Duration != nil {
		duration = formatDurationSecs(*item.Duration)
	}

	hasReport = item.HasReport

	// Build report URL
	reportURL := fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(devMode), taskID)

	if jsonOutput {
		output := map[string]interface{}{
			"test_id":    testID,
			"task_id":    taskID,
			"status":     status,
			"success":    success,
			"duration":   duration,
			"has_report": hasReport,
			"report_url": reportURL,
		}
		if stepsCompleted > 0 || totalSteps > 0 {
			output["steps_completed"] = stepsCompleted
			output["total_steps"] = totalSteps
		}
		if startedAt != "" {
			output["started_at"] = startedAt
		}
		if completedAt != "" {
			output["completed_at"] = completedAt
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.Println()

	// Display status summary
	ui.PrintKeyValue("Test:", displayName)
	ui.PrintKeyValue("Status:", status)
	if duration != "" {
		ui.PrintKeyValue("Duration:", duration)
	}
	if totalSteps > 0 {
		ui.PrintKeyValue("Steps:", fmt.Sprintf("%d/%d passed", stepsCompleted, totalSteps))
	}
	dateStr := formatAbsoluteTime(startedAt)
	if dateStr == "-" {
		dateStr = formatAbsoluteTime(item.ExecutionTime)
	}
	if dateStr != "-" {
		ui.PrintKeyValue("Date:", dateStr)
	}

	ui.Println()
	ui.PrintLink("Report", reportURL)

	if statusOpen {
		ui.OpenBrowser(reportURL)
	}

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "View full report:", Command: fmt.Sprintf("revyl test report %s", testNameOrID)},
		{Label: "View history:", Command: fmt.Sprintf("revyl test history %s", testNameOrID)},
	})

	return nil
}

// runTestHistory shows execution history for a test.
func runTestHistory(cmd *cobra.Command, args []string) error {
	// Honor global --json
	jsonOutput := historyOutputJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	devMode, _ := cmd.Flags().GetBool("dev")

	_, cfg, client, err := loadConfigAndClient(devMode)
	if err != nil {
		return err
	}

	testNameOrID := args[0]

	if jsonOutput {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
	}

	testID, testName, err := resolveTestID(cmd.Context(), testNameOrID, cfg, client)
	if err != nil {
		ui.PrintError("%v", err)
		return fmt.Errorf("test not found")
	}

	displayName := testName
	if displayName == "" {
		displayName = testNameOrID
	}

	if !jsonOutput {
		ui.StartSpinner("Fetching execution history...")
	}
	history, err := client.GetTestEnhancedHistory(cmd.Context(), testID, historyLimit, 0)
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to fetch history: %v", err)
		return err
	}

	if len(history.Items) == 0 {
		if jsonOutput {
			output := map[string]interface{}{
				"test_id":     testID,
				"items":       []interface{}{},
				"total_count": 0,
			}
			data, _ := json.MarshalIndent(output, "", "  ")
			fmt.Println(string(data))
			return nil
		}
		ui.PrintInfo("No executions found for '%s'", displayName)
		ui.Println()
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Run this test:", Command: fmt.Sprintf("revyl test run %s", testNameOrID)},
		})
		return nil
	}

	if jsonOutput {
		items := make([]map[string]interface{}, 0, len(history.Items))
		for i, item := range history.Items {
			entry := map[string]interface{}{
				"index":      i + 1,
				"id":         item.ID,
				"status":     item.Status,
				"has_report": item.HasReport,
			}
			if item.Duration != nil {
				entry["duration"] = *item.Duration
				entry["duration_formatted"] = formatDurationSecs(*item.Duration)
			}
			if item.EnhancedTask != nil {
				entry["task_id"] = item.EnhancedTask.ID
				entry["steps_completed"] = item.EnhancedTask.StepsCompleted
				entry["total_steps"] = item.EnhancedTask.TotalSteps
				entry["started_at"] = item.EnhancedTask.StartedAt
			}
			entry["execution_time"] = item.ExecutionTime
			items = append(items, entry)
		}
		output := map[string]interface{}{
			"test_id":     testID,
			"test_name":   displayName,
			"items":       items,
			"total_count": history.TotalCount,
			"shown_count": len(history.Items),
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.Println()
	ui.PrintInfo("Execution history for \"%s\" (%d total)", displayName, history.TotalCount)
	ui.Println()

	table := ui.NewTable("#", "STATUS", "DURATION", "STEPS", "DATE")
	table.SetMinWidth(0, 3)  // #
	table.SetMinWidth(1, 10) // STATUS
	table.SetMinWidth(2, 8)  // DURATION
	table.SetMinWidth(3, 7)  // STEPS
	table.SetMinWidth(4, 14) // DATE

	for i, item := range history.Items {
		status := item.Status

		duration := "-"
		if item.Duration != nil {
			duration = formatDurationSecs(*item.Duration)
		}

		steps := "-"
		if item.EnhancedTask != nil && item.EnhancedTask.TotalSteps > 0 {
			steps = fmt.Sprintf("%d/%d", item.EnhancedTask.StepsCompleted, item.EnhancedTask.TotalSteps)
		}

		dateStr := formatAbsoluteTime(item.ExecutionTime)
		if dateStr == "-" && item.EnhancedTask != nil {
			dateStr = formatAbsoluteTime(item.EnhancedTask.StartedAt)
		}

		table.AddRow(
			fmt.Sprintf("%d", i+1),
			strings.ToLower(status),
			duration,
			steps,
			dateStr,
		)
	}

	table.Render()

	if history.TotalCount > len(history.Items) {
		ui.Println()
		ui.PrintDim("Showing %d of %d. Use --limit %d to see more.",
			len(history.Items), history.TotalCount, min(history.TotalCount, 50))
	}

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "View latest report:", Command: fmt.Sprintf("revyl test report %s", testNameOrID)},
		{Label: "Run again:", Command: fmt.Sprintf("revyl test run %s", testNameOrID)},
	})

	return nil
}
