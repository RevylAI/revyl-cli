// Package main provides workflow status, history, report, and share commands.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

var (
	wfStatusOutputJSON  bool
	wfStatusOpen        bool
	wfHistoryOutputJSON bool
	wfHistoryLimit      int
	wfReportOpen        bool
	wfReportNoTests     bool
	wfShareOutputJSON   bool
	wfShareOpen         bool
)

func init() {
	workflowStatusCmd.Flags().BoolVar(&wfStatusOutputJSON, "json", false, "Output results as JSON")
	workflowStatusCmd.Flags().BoolVar(&wfStatusOpen, "open", false, "Open report in browser")

	workflowHistoryCmd.Flags().BoolVar(&wfHistoryOutputJSON, "json", false, "Output results as JSON")
	workflowHistoryCmd.Flags().IntVar(&wfHistoryLimit, "limit", 10, "Number of executions to show")

	workflowReportCmd.Flags().BoolVar(&wfReportOpen, "open", false, "Open report in browser")
	workflowReportCmd.Flags().BoolVar(&wfReportNoTests, "no-tests", false, "Hide individual test breakdown")

	workflowShareCmd.Flags().BoolVar(&wfShareOutputJSON, "json", false, "Output results as JSON")
	workflowShareCmd.Flags().BoolVar(&wfShareOpen, "open", false, "Open shareable link in browser")
}

// workflowStatusCmd shows the latest execution status for a workflow.
var workflowStatusCmd = &cobra.Command{
	Use:   "status <name|id>",
	Short: "Show latest workflow execution status",
	Long: `Show the status of the most recent execution for a workflow.

Examples:
  revyl workflow status smoke-tests
  revyl workflow status smoke-tests --json`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkflowStatus,
}

// workflowHistoryCmd shows execution history for a workflow.
var workflowHistoryCmd = &cobra.Command{
	Use:   "history <name|id>",
	Short: "Show workflow execution history",
	Long: `Show a table of past executions for a workflow.

Examples:
  revyl workflow history smoke-tests
  revyl workflow history smoke-tests --limit 20
  revyl workflow history smoke-tests --json`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkflowHistory,
}

// workflowReportCmd shows a detailed workflow report.
var workflowReportCmd = &cobra.Command{
	Use:   "report <name|id|taskId>",
	Short: "Show detailed workflow report",
	Long: `Show a detailed workflow report with individual test results.

Accepts workflow names (shows latest execution), workflow UUIDs, or task/execution IDs.

Examples:
  revyl workflow report smoke-tests
  revyl workflow report smoke-tests --json
  revyl workflow report smoke-tests --no-tests
  revyl workflow report <task-uuid>`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkflowReport,
}

// workflowShareCmd generates a shareable link for a workflow report.
var workflowShareCmd = &cobra.Command{
	Use:   "share <name|id|taskId>",
	Short: "Generate shareable workflow report link",
	Long: `Generate a shareable link for a workflow execution report.

Examples:
  revyl workflow share smoke-tests
  revyl workflow share <task-uuid> --json`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkflowShare,
}

// resolveToWorkflowTaskID resolves an argument to a workflow execution task ID.
// Tries: direct UUID as execution ID → workflow name/alias → workflow UUID → latest task.
func resolveToWorkflowTaskID(cmd *cobra.Command, nameOrID string, cfg *config.ProjectConfig, client *api.Client) (taskID string, workflowName string, err error) {
	// 1. If it looks like a UUID, try it directly as a workflow execution ID
	if looksLikeUUID(nameOrID) {
		_, statusErr := client.GetWorkflowStatus(cmd.Context(), nameOrID)
		if statusErr == nil {
			return nameOrID, "", nil
		}

		// Not a valid execution ID — try as a workflow definition ID
		var apiErr *api.APIError
		if errors.As(statusErr, &apiErr) && apiErr.StatusCode == 404 {
			latestTaskID, err := resolveLatestWorkflowTaskID(cmd.Context(), client, nameOrID)
			if err == nil {
				return latestTaskID, "", nil
			}
		}

		return "", "", fmt.Errorf("'%s' is not a valid workflow execution ID or workflow with executions", nameOrID)
	}

	// 2. Resolve as workflow name → get latest task ID
	workflowID, resolvedName, err := resolveWorkflowID(cmd.Context(), nameOrID, cfg, client)
	if err != nil {
		return "", "", err
	}

	displayName := resolvedName
	if displayName == "" {
		displayName = nameOrID
	}

	latestTaskID, err := resolveLatestWorkflowTaskID(cmd.Context(), client, workflowID)
	if err != nil {
		return "", displayName, fmt.Errorf("no executions found for '%s'. Run 'revyl workflow run %s' to execute it first", displayName, nameOrID)
	}

	return latestTaskID, displayName, nil
}

func runWorkflowStatus(cmd *cobra.Command, args []string) error {
	jsonOutput := wfStatusOutputJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	devMode, _ := cmd.Flags().GetBool("dev")

	_, cfg, client, err := loadConfigAndClient(devMode)
	if err != nil {
		return err
	}

	nameOrID := args[0]

	if jsonOutput {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
	} else {
		ui.StartSpinner("Loading workflow status...")
	}

	// Resolve workflow ID
	workflowID, workflowName, err := resolveWorkflowID(cmd.Context(), nameOrID, cfg, client)
	if err != nil {
		if !jsonOutput {
			ui.StopSpinner()
		}
		ui.PrintError("%v", err)
		return fmt.Errorf("workflow not found")
	}

	displayName := workflowName
	if displayName == "" {
		displayName = nameOrID
	}

	// Get latest execution
	history, err := client.GetWorkflowHistory(cmd.Context(), workflowID, 1, 0)
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to get workflow status: %v", err)
		return err
	}

	if len(history.Executions) == 0 {
		if jsonOutput {
			output := map[string]interface{}{
				"workflow_name": displayName,
				"workflow_id":   workflowID,
				"status":        "no_executions",
				"message":       "No executions found for this workflow",
			}
			data, _ := json.MarshalIndent(output, "", "  ")
			fmt.Println(string(data))
			return nil
		}
		ui.Println()
		ui.PrintInfo("No executions found for '%s'", displayName)
		ui.Println()
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Run workflow:", Command: fmt.Sprintf("revyl workflow run %s", nameOrID)},
		})
		return nil
	}

	latest := history.Executions[0]

	if jsonOutput {
		output := map[string]interface{}{
			"workflow_name":   displayName,
			"workflow_id":     workflowID,
			"status":          latest.Status,
			"progress":        latest.Progress,
			"completed_tests": latest.CompletedTests,
			"total_tests":     latest.TotalTests,
			"passed_tests":    latest.PassedTests,
			"failed_tests":    latest.FailedTests,
		}
		if latest.ExecutionID != "" {
			output["execution_id"] = latest.ExecutionID
		}
		if latest.StartedAt != "" {
			output["started_at"] = latest.StartedAt
		}
		if latest.Duration != "" {
			output["duration"] = latest.Duration
		}
		if latest.ErrorMessage != "" {
			output["error_message"] = latest.ErrorMessage
		}
		reportURL := fmt.Sprintf("%s/workflows/report?taskId=%s", config.GetAppURL(devMode), latest.ExecutionID)
		if latest.ExecutionID != "" {
			output["report_url"] = reportURL
		}

		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	// Display formatted status
	ui.Println()

	statusIcon := ui.SuccessStyle.Render("✓")
	resultText := capitalizeFirst(latest.Status)
	if latest.Status == "failed" || latest.Status == "timeout" {
		statusIcon = ui.ErrorStyle.Render("✗")
	} else if latest.Status == "running" || latest.Status == "queued" || latest.Status == "setup" {
		statusIcon = ui.AccentStyle.Render("●")
	} else if latest.Status == "cancelled" {
		statusIcon = ui.DimStyle.Render("○")
	}

	fmt.Printf("  %s %s  %s\n", statusIcon,
		ui.TitleStyle.Render(displayName),
		ui.DimStyle.Render(fmt.Sprintf("— %s", resultText)))
	ui.Println()

	// Test summary
	testsValue := fmt.Sprintf("%d/%d completed", latest.CompletedTests, latest.TotalTests)
	if latest.PassedTests > 0 || latest.FailedTests > 0 {
		testsValue = fmt.Sprintf("%s/%d passed",
			ui.AccentStyle.Render(fmt.Sprintf("%d", latest.PassedTests)),
			latest.TotalTests)
		if latest.FailedTests > 0 {
			testsValue += ui.ErrorStyle.Render(fmt.Sprintf(", %d failed", latest.FailedTests))
		}
	}
	ui.PrintKeyValue("Tests:", testsValue)

	if latest.Duration != "" {
		ui.PrintKeyValue("Duration:", latest.Duration)
	}
	if latest.StartedAt != "" {
		ui.PrintKeyValue("Date:", formatAbsoluteTime(latest.StartedAt))
	}
	if latest.ErrorMessage != "" {
		ui.PrintKeyValue("Error:", ui.ErrorStyle.Render(latest.ErrorMessage))
	}

	if latest.ExecutionID != "" {
		ui.Println()
		reportURL := fmt.Sprintf("%s/workflows/report?taskId=%s", config.GetAppURL(devMode), latest.ExecutionID)
		ui.PrintLink("Report", reportURL)
	}

	if wfStatusOpen && latest.ExecutionID != "" {
		reportURL := fmt.Sprintf("%s/workflows/report?taskId=%s", config.GetAppURL(devMode), latest.ExecutionID)
		ui.OpenBrowser(reportURL)
	}

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "View report:", Command: fmt.Sprintf("revyl workflow report %s", nameOrID)},
		{Label: "View history:", Command: fmt.Sprintf("revyl workflow history %s", nameOrID)},
	})

	return nil
}

func runWorkflowHistory(cmd *cobra.Command, args []string) error {
	jsonOutput := wfHistoryOutputJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	devMode, _ := cmd.Flags().GetBool("dev")

	_, cfg, client, err := loadConfigAndClient(devMode)
	if err != nil {
		return err
	}

	nameOrID := args[0]

	if jsonOutput {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
	} else {
		ui.StartSpinner("Loading workflow history...")
	}

	workflowID, workflowName, err := resolveWorkflowID(cmd.Context(), nameOrID, cfg, client)
	if err != nil {
		if !jsonOutput {
			ui.StopSpinner()
		}
		ui.PrintError("%v", err)
		return fmt.Errorf("workflow not found")
	}

	displayName := workflowName
	if displayName == "" {
		displayName = nameOrID
	}

	limit := wfHistoryLimit
	if limit <= 0 {
		limit = 10
	}

	history, err := client.GetWorkflowHistory(cmd.Context(), workflowID, limit, 0)
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to get workflow history: %v", err)
		return err
	}

	if jsonOutput {
		output := map[string]interface{}{
			"workflow_name": displayName,
			"workflow_id":   workflowID,
			"total_count":   history.TotalCount,
			"success_rate":  history.SuccessRate,
		}
		if history.AverageDuration != nil {
			output["average_duration"] = *history.AverageDuration
		}

		executions := make([]map[string]interface{}, 0, len(history.Executions))
		for i, e := range history.Executions {
			exec := map[string]interface{}{
				"index":           i + 1,
				"status":          e.Status,
				"completed_tests": e.CompletedTests,
				"total_tests":     e.TotalTests,
			}
			if e.ExecutionID != "" {
				exec["execution_id"] = e.ExecutionID
			}
			if e.StartedAt != "" {
				exec["started_at"] = e.StartedAt
			}
			if e.Duration != "" {
				exec["duration"] = e.Duration
			}
			executions = append(executions, exec)
		}
		output["executions"] = executions

		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(history.Executions) == 0 {
		ui.Println()
		ui.PrintInfo("No executions found for '%s'", displayName)
		ui.Println()
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Run workflow:", Command: fmt.Sprintf("revyl workflow run %s", nameOrID)},
		})
		return nil
	}

	ui.Println()
	ui.PrintInfo("Execution history for \"%s\" (%d total)", displayName, history.TotalCount)
	ui.Println()

	table := ui.NewTable("#", "STATUS", "TESTS", "DURATION", "DATE")
	table.SetMinWidth(0, 4)
	table.SetMinWidth(1, 12)
	table.SetMinWidth(2, 8)
	table.SetMinWidth(3, 10)
	table.SetMinWidth(4, 14)

	for i, e := range history.Executions {
		tests := fmt.Sprintf("%d/%d", e.CompletedTests, e.TotalTests)
		duration := e.Duration
		if duration == "" {
			duration = "-"
		}
		date := formatAbsoluteTime(e.StartedAt)
		table.AddRow(fmt.Sprintf("%d", i+1), e.Status, tests, duration, date)
	}

	table.Render()

	if history.TotalCount > len(history.Executions) {
		ui.PrintDim("  Showing %d of %d. Use --limit %d to see more.",
			len(history.Executions), history.TotalCount, min(history.TotalCount, 100))
	}

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "View latest report:", Command: fmt.Sprintf("revyl workflow report %s", nameOrID)},
		{Label: "Run again:", Command: fmt.Sprintf("revyl workflow run %s", nameOrID)},
	})

	return nil
}

func runWorkflowReport(cmd *cobra.Command, args []string) error {
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	devMode, _ := cmd.Flags().GetBool("dev")

	_, cfg, client, err := loadConfigAndClient(devMode)
	if err != nil {
		return err
	}

	nameOrID := args[0]

	if jsonOutput {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
	} else {
		ui.StartSpinner("Loading workflow report...")
	}

	taskID, workflowName, err := resolveToWorkflowTaskID(cmd, nameOrID, cfg, client)
	if err != nil {
		if !jsonOutput {
			ui.StopSpinner()
		}
		ui.PrintError("%v", err)
		return fmt.Errorf("report not found")
	}

	report, err := client.GetWorkflowUnifiedReport(cmd.Context(), taskID)
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		reportURL := fmt.Sprintf("%s/workflows/report?taskId=%s", config.GetAppURL(devMode), taskID)

		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			if apiErr.StatusCode == 404 {
				ui.PrintWarning("No report available for this workflow execution")
				ui.Println()
				ui.PrintLink("View in browser", reportURL)
				return nil
			}
			if apiErr.StatusCode >= 500 {
				ui.PrintError("Report API returned an error (HTTP %d)", apiErr.StatusCode)
				if apiErr.Detail != "" {
					ui.PrintDim("  %s", apiErr.Detail)
				}
				ui.Println()
				ui.PrintLink("View in browser", reportURL)
				return nil
			}
		}
		ui.PrintError("Failed to fetch workflow report: %v", err)
		return nil
	}

	// Use workflow detail name if available
	displayName := workflowName
	if displayName == "" && report.WorkflowDetail != nil && report.WorkflowDetail.Name != "" {
		displayName = report.WorkflowDetail.Name
	}
	if displayName == "" {
		displayName = nameOrID
	}

	reportURL := fmt.Sprintf("%s/workflows/report?taskId=%s", config.GetAppURL(devMode), taskID)

	if jsonOutput {
		output := map[string]interface{}{
			"workflow_name": displayName,
			"execution_id":  taskID,
			"status":        report.WorkflowTask.Status,
		}
		if report.WorkflowTask.WorkflowID != "" {
			output["workflow_id"] = report.WorkflowTask.WorkflowID
		}
		if report.WorkflowTask.Success != nil {
			output["success"] = *report.WorkflowTask.Success
		}
		if report.WorkflowTask.TotalTests != nil {
			output["total_tests"] = *report.WorkflowTask.TotalTests
		}
		if report.WorkflowTask.CompletedTests != nil {
			output["completed_tests"] = *report.WorkflowTask.CompletedTests
		}
		if report.WorkflowTask.Duration != nil {
			output["duration_seconds"] = *report.WorkflowTask.Duration
		}
		if report.WorkflowTask.StartedAt != "" {
			output["started_at"] = report.WorkflowTask.StartedAt
		}
		output["report_url"] = reportURL

		if len(report.ChildTasks) > 0 {
			tests := make([]map[string]interface{}, 0, len(report.ChildTasks))
			for _, ct := range report.ChildTasks {
				test := map[string]interface{}{
					"task_id": ct.TaskID,
					"status":  ct.Status,
				}
				if ct.TestName != "" {
					test["test_name"] = ct.TestName
				}
				if ct.Platform != "" {
					test["platform"] = normalizePlatform(ct.Platform)
				}
				if ct.Success != nil {
					test["success"] = *ct.Success
				}
				if ct.StartedAt != "" {
					test["started_at"] = ct.StartedAt
				}
				if ct.CompletedAt != "" {
					test["completed_at"] = ct.CompletedAt
				}
				if ct.Duration != nil {
					test["duration_seconds"] = *ct.Duration
				} else if ct.ExecutionTimeSeconds != nil {
					test["duration_seconds"] = *ct.ExecutionTimeSeconds
				}
				if ct.StepsCompleted != nil {
					test["steps_completed"] = *ct.StepsCompleted
				}
				if ct.TotalSteps != nil {
					test["total_steps"] = *ct.TotalSteps
				}
				if ct.ErrorMessage != "" {
					test["error_message"] = ct.ErrorMessage
				}
				tests = append(tests, test)
			}
			output["tests"] = tests
		}

		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	// Display formatted report
	ui.Println()

	// Count passed/failed
	passedTests := 0
	failedTests := 0
	for _, ct := range report.ChildTasks {
		if ct.Success != nil {
			if *ct.Success {
				passedTests++
			} else {
				failedTests++
			}
		}
	}

	// Result header
	statusIcon := ui.SuccessStyle.Render("✓")
	resultText := "Passed"
	if failedTests > 0 {
		statusIcon = ui.ErrorStyle.Render("✗")
		resultText = "Failed"
	} else if report.WorkflowTask.Status == "running" || report.WorkflowTask.Status == "queued" {
		statusIcon = ui.AccentStyle.Render("●")
		resultText = capitalizeFirst(report.WorkflowTask.Status)
	} else if report.WorkflowTask.Status == "cancelled" {
		statusIcon = ui.DimStyle.Render("○")
		resultText = "Cancelled"
	}

	fmt.Printf("  %s %s  %s\n", statusIcon,
		ui.TitleStyle.Render(displayName),
		ui.DimStyle.Render(fmt.Sprintf("— %s", resultText)))
	ui.Println()

	// Duration
	if report.WorkflowTask.Duration != nil {
		ui.PrintKeyValue("Duration:", formatDurationSecs(*report.WorkflowTask.Duration))
	}

	// Tests summary
	totalTests := len(report.ChildTasks)
	if report.WorkflowTask.TotalTests != nil {
		totalTests = *report.WorkflowTask.TotalTests
	}
	testsValue := fmt.Sprintf("%d total", totalTests)
	if passedTests > 0 {
		testsValue += fmt.Sprintf(", %s passed", ui.AccentStyle.Render(fmt.Sprintf("%d", passedTests)))
	}
	if failedTests > 0 {
		testsValue += fmt.Sprintf(", %s", ui.ErrorStyle.Render(fmt.Sprintf("%d failed", failedTests)))
	}
	ui.PrintKeyValue("Tests:", testsValue)

	if report.WorkflowTask.StartedAt != "" {
		ui.PrintKeyValue("Date:", formatAbsoluteTime(report.WorkflowTask.StartedAt))
	}

	// Child test breakdown
	if !wfReportNoTests && len(report.ChildTasks) > 0 {
		ui.Println()
		separator := ui.DimStyle.Render("  " + strings.Repeat("─", 64))
		fmt.Println(separator)
		ui.Println()

		// Compute column widths
		numWidth := 2
		nameWidth := 20
		platformWidth := 8

		for i, ct := range report.ChildTasks {
			w := len(fmt.Sprintf("%d", i+1))
			if w > numWidth {
				numWidth = w
			}
			if ct.TestName != "" && len(ct.TestName) > nameWidth {
				nameWidth = len(ct.TestName)
			}
		}
		if nameWidth > 30 {
			nameWidth = 30
		}

		for i, ct := range report.ChildTasks {
			testName := ct.TestName
			if testName == "" {
				testName = ct.TestID
			}

			platform := normalizePlatform(ct.Platform)
			status := strings.ToLower(ct.Status)

			// Status icon
			var icon string
			switch {
			case ct.Success != nil && *ct.Success:
				icon = ui.SuccessStyle.Render("✓")
			case ct.Success != nil && !*ct.Success:
				icon = ui.ErrorStyle.Render("✗")
			case status == "running":
				icon = ui.AccentStyle.Render("●")
			case status == "queued":
				icon = ui.DimStyle.Render("○")
			default:
				icon = ui.DimStyle.Render("·")
			}

			// Duration
			var durationStr string
			if ct.ExecutionTimeSeconds != nil && *ct.ExecutionTimeSeconds > 0 {
				durationStr = formatDurationSecs(*ct.ExecutionTimeSeconds)
			} else if ct.Duration != nil && *ct.Duration > 0 {
				durationStr = formatDurationSecs(*ct.Duration)
			} else if ct.StartedAt != "" && ct.CompletedAt != "" {
				durationStr = computeDuration(ct.StartedAt, ct.CompletedAt)
			}
			if durationStr == "" {
				durationStr = "-"
			}

			// Steps
			var stepsStr string
			if ct.StepsCompleted != nil && ct.TotalSteps != nil {
				stepsStr = fmt.Sprintf("%d/%d", *ct.StepsCompleted, *ct.TotalSteps)
			}

			numStr := fmt.Sprintf("%*d", numWidth, i+1)
			nameStr := fmt.Sprintf("%-*s", nameWidth, truncateStep(testName, nameWidth))
			platStr := fmt.Sprintf("%-*s", platformWidth, platform)

			line := fmt.Sprintf("  %s  %s  %s  %s",
				ui.DimStyle.Render(numStr),
				ui.InfoStyle.Render(nameStr),
				ui.DimStyle.Render(platStr),
				icon,
			)
			if stepsStr != "" {
				line += fmt.Sprintf("  %s", ui.DimStyle.Render(stepsStr))
			}
			line += fmt.Sprintf("  %s", ui.DimStyle.Render(durationStr))

			fmt.Println(line)

			// Error message for failed tests
			if ct.ErrorMessage != "" && ct.Success != nil && !*ct.Success {
				indent := numWidth + 2
				reason := truncateStep(ct.ErrorMessage, 60)
				fmt.Printf("  %s%s\n",
					strings.Repeat(" ", indent),
					ui.DimStyle.Render(reason))
			}
		}

		fmt.Println(separator)
	}

	ui.Println()
	ui.PrintLink("Report", reportURL)

	if wfReportOpen {
		ui.OpenBrowser(reportURL)
	}

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Run again:", Command: fmt.Sprintf("revyl workflow run %s", nameOrID)},
	})

	return nil
}

func runWorkflowShare(cmd *cobra.Command, args []string) error {
	jsonOutput := wfShareOutputJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	devMode, _ := cmd.Flags().GetBool("dev")

	_, cfg, client, err := loadConfigAndClient(devMode)
	if err != nil {
		return err
	}

	nameOrID := args[0]

	if jsonOutput {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
	} else {
		ui.StartSpinner("Generating shareable link...")
	}

	taskID, _, err := resolveToWorkflowTaskID(cmd, nameOrID, cfg, client)
	if err != nil {
		if !jsonOutput {
			ui.StopSpinner()
		}
		ui.PrintError("%v", err)
		return fmt.Errorf("could not resolve workflow execution")
	}

	// Build shareable URL using the app URL since the backend
	// generate_shareable_link endpoint requires an Origin header
	// that the CLI doesn't send. We construct the link client-side.
	appURL := config.GetAppURL(devMode)
	shareURL := fmt.Sprintf("%s/workflows/report?taskId=%s", appURL, taskID)

	if !jsonOutput {
		ui.StopSpinner()
	}

	if jsonOutput {
		output := map[string]interface{}{
			"task_id":    taskID,
			"report_url": shareURL,
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.Println()
	ui.PrintSuccess("Report link generated")
	ui.Println()
	ui.PrintLink("Link", shareURL)

	if wfShareOpen {
		ui.OpenBrowser(shareURL)
	}

	return nil
}
