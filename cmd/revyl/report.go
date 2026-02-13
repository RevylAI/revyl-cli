// Package main provides test report and share commands.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

var (
	reportOutputJSON bool
	reportOpen       bool
	reportShare      bool
	reportNoSteps    bool
	shareOutputJSON  bool
	shareOpen        bool
)

func init() {
	testReportCmd.Flags().BoolVar(&reportOutputJSON, "json", false, "Output results as JSON")
	testReportCmd.Flags().BoolVar(&reportOpen, "open", false, "Open report in browser")
	testReportCmd.Flags().BoolVar(&reportShare, "share", false, "Generate and print a shareable link")
	testReportCmd.Flags().BoolVar(&reportNoSteps, "no-steps", false, "Hide step breakdown")

	testShareCmd.Flags().BoolVar(&shareOutputJSON, "json", false, "Output results as JSON")
	testShareCmd.Flags().BoolVar(&shareOpen, "open", false, "Open shareable link in browser")
}

// testReportCmd shows a detailed report for a test execution.
var testReportCmd = &cobra.Command{
	Use:   "report <name|id|taskId>",
	Short: "Show detailed test report",
	Long: `Show a detailed test report with step-by-step breakdown.

Accepts test names (shows latest execution), test UUIDs, or task/execution IDs.
When given a test name, shows the report for the most recent execution.

Examples:
  revyl test report login-flow           # Latest execution report
  revyl test report login-flow --json    # JSON output
  revyl test report login-flow --share   # Include shareable link
  revyl test report login-flow --no-steps # Summary only
  revyl test report <task-uuid>          # Report by task ID`,
	Args: cobra.ExactArgs(1),
	RunE: runTestReport,
}

// testShareCmd generates a shareable link for a test execution.
var testShareCmd = &cobra.Command{
	Use:   "share <name|id|taskId>",
	Short: "Generate shareable report link",
	Long: `Generate a shareable link for a test execution report.

Accepts test names (uses latest execution), test UUIDs, or task/execution IDs.

Examples:
  revyl test share login-flow
  revyl test share login-flow --json
  revyl test share <task-uuid> --open`,
	Args: cobra.ExactArgs(1),
	RunE: runTestShare,
}

// resolveToTaskID resolves an argument to a task/execution ID.
// Tries: direct UUID as execution ID → test name/alias → test UUID → latest task.
func resolveToTaskID(cmd *cobra.Command, nameOrID string, cfg *config.ProjectConfig, client *api.Client, devMode bool) (taskID string, testName string, err error) {
	// 1. If it looks like a UUID, try it directly as an execution/task ID
	if looksLikeUUID(nameOrID) {
		// Try to get report by this ID (it could be a task/execution ID)
		_, reportErr := client.GetReportByExecution(cmd.Context(), nameOrID, false)
		if reportErr == nil {
			return nameOrID, "", nil
		}

		// Not a valid execution ID - check if it's a test ID
		var apiErr *api.APIError
		if errors.As(reportErr, &apiErr) && apiErr.StatusCode == 404 {
			// Try as test ID → get latest task
			latestTaskID, err := resolveLatestTaskID(cmd.Context(), client, nameOrID)
			if err == nil {
				return latestTaskID, "", nil
			}
		}

		// Neither a valid execution ID nor a test ID with executions
		return "", "", fmt.Errorf("'%s' is not a valid execution ID or test with executions", nameOrID)
	}

	// 2. Resolve as test name → get latest task ID
	testID, resolvedName, err := resolveTestID(cmd.Context(), nameOrID, cfg, client)
	if err != nil {
		return "", "", err
	}

	displayName := resolvedName
	if displayName == "" {
		displayName = nameOrID
	}

	latestTaskID, err := resolveLatestTaskID(cmd.Context(), client, testID)
	if err != nil {
		return "", displayName, fmt.Errorf("no executions found for '%s'. Run 'revyl run %s' to execute it first", displayName, nameOrID)
	}

	return latestTaskID, displayName, nil
}

// runTestReport shows a detailed report for a test execution.
func runTestReport(cmd *cobra.Command, args []string) error {
	// Honor global --json
	jsonOutput := reportOutputJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	devMode, _ := cmd.Flags().GetBool("dev")

	_, cfg, client, err := loadConfigAndClient(devMode)
	if err != nil {
		return err
	}

	nameOrID := args[0]

	// Suppress all spinners/UI noise when outputting JSON
	if jsonOutput {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
	} else {
		ui.StartSpinner("Loading report...")
	}

	taskID, testName, err := resolveToTaskID(cmd, nameOrID, cfg, client, devMode)
	if err != nil {
		if !jsonOutput {
			ui.StopSpinner()
		}
		ui.PrintError("%v", err)
		return fmt.Errorf("report not found")
	}

	// Fetch the report — include actions for JSON (agent consumption)
	includeSteps := !reportNoSteps
	includeActions := jsonOutput
	report, err := client.GetReportByExecution(cmd.Context(), taskID, includeSteps, includeActions)
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		// Build the web report URL as a fallback
		fallbackURL := fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(devMode), taskID)

		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			if apiErr.StatusCode == 404 {
				ui.PrintWarning("No detailed report available for this execution")
				ui.PrintInfo("The report may not have been generated yet, or this is a legacy execution.")
				ui.Println()
				ui.PrintLink("View in browser", fallbackURL)
				return nil
			}
			if apiErr.StatusCode >= 500 {
				ui.PrintError("Report API returned an error (HTTP %d)", apiErr.StatusCode)
				if apiErr.Detail != "" {
					ui.PrintDim("  %s", apiErr.Detail)
				}
				if devMode {
					ui.Println()
					ui.PrintInfo("This may indicate the local backend's DATABASE_URL is not configured.")
					ui.PrintInfo("The reports-v3 endpoint requires a direct database connection (SQLAlchemy).")
					ui.PrintInfo("Try without --dev to use the production API, or check backend logs.")
				}
				ui.Println()
				ui.PrintLink("View in browser", fallbackURL)
				return nil
			}
		}
		ui.PrintError("Failed to fetch report: %v", err)
		ui.Println()
		ui.PrintLink("View in browser", fallbackURL)
		return nil
	}

	// Use report test name if we don't have one from resolution
	displayName := testName
	if displayName == "" && report.TestName != "" {
		displayName = report.TestName
	}
	if displayName == "" {
		displayName = nameOrID
	}

	reportURL := fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(devMode), taskID)

	if jsonOutput {
		output := map[string]interface{}{
			"test_name":          displayName,
			"test_id":            report.TestID,
			"execution_id":       report.ExecutionID,
			"platform":           normalizePlatform(report.Platform),
			"success":            report.Success,
			"total_steps":        report.TotalSteps,
			"passed_steps":       report.PassedSteps,
			"failed_steps":       report.FailedSteps,
			"total_validations":  report.TotalValidations,
			"validations_passed": report.ValidsPassed,
			"report_url":         reportURL,
		}
		if report.StartedAt != "" {
			output["started_at"] = report.StartedAt
		}
		if report.CompletedAt != "" {
			output["completed_at"] = report.CompletedAt
		}
		if report.StartedAt != "" && report.CompletedAt != "" {
			if d := computeDuration(report.StartedAt, report.CompletedAt); d != "" {
				output["duration"] = d
			}
		}
		if report.AppName != "" {
			output["app_name"] = report.AppName
		}
		if report.BuildVersion != "" {
			output["build_version"] = report.BuildVersion
		}
		if report.DeviceModel != "" {
			output["device_model"] = report.DeviceModel
		}
		if report.OSVersion != "" {
			output["os_version"] = report.OSVersion
		}
		if includeSteps && len(report.Steps) > 0 {
			steps := make([]map[string]interface{}, 0, len(report.Steps))
			for _, s := range report.Steps {
				step := map[string]interface{}{
					"order":       s.ExecutionOrder,
					"type":        s.StepType,
					"description": s.StepDesc,
					"status":      s.Status,
				}
				if s.StatusReason != "" {
					step["status_reason"] = s.StatusReason
				}
				if len(s.TypeData) > 0 {
					step["type_data"] = s.TypeData
				}
				if len(s.Actions) > 0 {
					actions := make([]map[string]interface{}, 0, len(s.Actions))
					for _, a := range s.Actions {
						action := map[string]interface{}{
							"action_index": a.ActionIndex,
						}
						if a.ActionType != "" {
							action["action_type"] = a.ActionType
						}
						if a.AgentDescription != "" {
							action["agent_description"] = a.AgentDescription
						}
						if a.Reasoning != "" {
							action["reasoning"] = a.Reasoning
						}
						if a.ReflectionDecision != "" {
							action["reflection_decision"] = a.ReflectionDecision
						}
						if a.ReflectionReasoning != "" {
							action["reflection_reasoning"] = a.ReflectionReasoning
						}
						if a.ReflectionSuggestion != "" {
							action["reflection_suggestion"] = a.ReflectionSuggestion
						}
						if a.IsTerminal {
							action["is_terminal"] = true
						}
						if len(a.TypeData) > 0 {
							action["type_data"] = a.TypeData
						}
						actions = append(actions, action)
					}
					step["actions"] = actions
				}
				steps = append(steps, step)
			}
			output["steps"] = steps
		}

		// Include shareable link if --share flag
		if reportShare {
			shareResp, shareErr := client.GenerateShareableLink(cmd.Context(), taskID)
			if shareErr == nil {
				output["shareable_link"] = shareResp.ShareableLink
			}
		}

		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	// Display formatted report
	ui.Println()

	// Result header — big pass/fail indicator
	resultIcon := ui.SuccessStyle.Render("✓")
	resultText := "Passed"
	if (report.Success != nil && !*report.Success) || report.FailedSteps > 0 {
		resultIcon = ui.ErrorStyle.Render("✗")
		resultText = "Failed"
	}
	fmt.Printf("  %s %s  %s\n", resultIcon,
		ui.TitleStyle.Render(displayName),
		ui.DimStyle.Render(fmt.Sprintf("— %s", resultText)))
	ui.Println()

	// Metadata
	if report.Platform != "" {
		ui.PrintKeyValue("Platform:", normalizePlatform(report.Platform))
	}
	if report.AppName != "" {
		appInfo := report.AppName
		if report.BuildVersion != "" {
			appInfo += " v" + report.BuildVersion
		}
		ui.PrintKeyValue("App:", appInfo)
	}
	if report.DeviceModel != "" {
		deviceInfo := report.DeviceModel
		if report.OSVersion != "" {
			deviceInfo += fmt.Sprintf(" (%s)", report.OSVersion)
		}
		ui.PrintKeyValue("Device:", deviceInfo)
	}

	// Duration from started_at / completed_at
	if report.StartedAt != "" && report.CompletedAt != "" {
		duration := computeDuration(report.StartedAt, report.CompletedAt)
		if duration != "" {
			ui.PrintKeyValue("Duration:", duration)
		}
	}

	// Compact step/validation summary
	stepsValue := fmt.Sprintf("%s/%d passed",
		ui.AccentStyle.Render(fmt.Sprintf("%d", report.PassedSteps)),
		report.TotalSteps)
	if report.FailedSteps > 0 {
		stepsValue += ui.ErrorStyle.Render(fmt.Sprintf(", %d failed", report.FailedSteps))
	}
	ui.PrintKeyValue("Steps:", stepsValue)

	if report.TotalValidations > 0 {
		ui.PrintKeyValue("Validations:", fmt.Sprintf("%d/%d passed",
			report.ValidsPassed, report.TotalValidations))
	}

	// Display steps
	if includeSteps && len(report.Steps) > 0 {
		ui.Println()
		separator := ui.DimStyle.Render("  " + strings.Repeat("─", 64))
		fmt.Println(separator)
		ui.Println()

		// Compute column widths dynamically
		numWidth := 2
		for _, s := range report.Steps {
			w := len(fmt.Sprintf("%d", s.ExecutionOrder))
			if w > numWidth {
				numWidth = w
			}
		}

		typeWidth := 12
		for _, s := range report.Steps {
			w := len(strings.ToLower(s.StepType))
			if w > typeWidth {
				typeWidth = w
			}
		}

		// Description gets remaining space (target ~40 chars)
		descWidth := 40

		for _, step := range report.Steps {
			stepType := strings.ToLower(step.StepType)
			stepStatus := strings.ToLower(step.Status)
			desc := sanitizeDesc(step.StepDesc)

			// Status icon — only the icon is colored
			var statusIcon string
			switch stepStatus {
			case "passed", "success":
				statusIcon = ui.SuccessStyle.Render("✓")
			case "failed", "error":
				statusIcon = ui.ErrorStyle.Render("✗")
			case "warning":
				statusIcon = ui.WarningStyle.Render("⚠")
			default:
				statusIcon = ui.DimStyle.Render("·")
			}

			// Right-aligned number, dimmed type, normal description, status icon
			numStr := fmt.Sprintf("%*d", numWidth, step.ExecutionOrder)
			typeStr := fmt.Sprintf("%-*s", typeWidth, stepType)
			descStr := fmt.Sprintf("%-*s", descWidth, truncateStep(desc, descWidth))

			fmt.Printf("  %s  %s  %s  %s\n",
				ui.DimStyle.Render(numStr),
				ui.DimStyle.Render(typeStr),
				ui.InfoStyle.Render(descStr),
				statusIcon,
			)

			// Status reason on next line, indented to align with description
			if step.StatusReason != "" && (stepStatus == "failed" || stepStatus == "error" || stepStatus == "warning") {
				indent := numWidth + 2 + typeWidth + 2
				reason := truncateStep(step.StatusReason, 60)
				fmt.Printf("  %s%s\n",
					strings.Repeat(" ", indent),
					ui.DimStyle.Render(reason))
			}
		}

		fmt.Println(separator)
	}

	ui.Println()
	ui.PrintLink("Report", reportURL)

	if reportOpen {
		ui.OpenBrowser(reportURL)
	}

	// Handle --share flag
	if reportShare {
		ui.StartSpinner("Generating shareable link...")
		shareResp, shareErr := client.GenerateShareableLink(cmd.Context(), taskID)
		ui.StopSpinner()

		if shareErr != nil {
			ui.PrintWarning("Failed to generate shareable link: %v", shareErr)
		} else {
			ui.PrintLink("Shareable", shareResp.ShareableLink)
		}
	}

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Share report:", Command: fmt.Sprintf("revyl test share %s", nameOrID)},
		{Label: "Run again:", Command: fmt.Sprintf("revyl run %s", nameOrID)},
	})

	return nil
}

// runTestShare generates a shareable link for a test execution.
func runTestShare(cmd *cobra.Command, args []string) error {
	// Honor global --json
	jsonOutput := shareOutputJSON
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

	taskID, _, err := resolveToTaskID(cmd, nameOrID, cfg, client, devMode)
	if err != nil {
		if !jsonOutput {
			ui.StopSpinner()
		}
		ui.PrintError("%v", err)
		return fmt.Errorf("could not resolve execution")
	}

	shareResp, err := client.GenerateShareableLink(cmd.Context(), taskID)
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to generate shareable link: %v", err)
		return err
	}

	if jsonOutput {
		output := map[string]interface{}{
			"task_id":        taskID,
			"shareable_link": shareResp.ShareableLink,
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.Println()
	ui.PrintSuccess("Shareable link generated")
	ui.Println()
	ui.PrintLink("Link", shareResp.ShareableLink)

	if shareOpen {
		ui.OpenBrowser(shareResp.ShareableLink)
	}

	return nil
}

// normalizePlatform fixes platform display casing (e.g. "Ios" → "iOS", "Android" stays).
func normalizePlatform(p string) string {
	switch strings.ToLower(p) {
	case "ios", "ios-dev":
		return "iOS"
	case "android", "android-dev":
		return "Android"
	default:
		return p
	}
}

// computeDuration calculates a human-readable duration from two ISO 8601 timestamps.
func computeDuration(startedAt, completedAt string) string {
	start, err := time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		start, err = time.Parse("2006-01-02T15:04:05", startedAt)
		if err != nil {
			return ""
		}
	}
	end, err := time.Parse(time.RFC3339Nano, completedAt)
	if err != nil {
		end, err = time.Parse("2006-01-02T15:04:05", completedAt)
		if err != nil {
			return ""
		}
	}
	secs := end.Sub(start).Seconds()
	if secs < 0 {
		return ""
	}
	return formatDurationSecs(secs)
}

// sanitizeDesc collapses whitespace and newlines in a step description to a single space.
func sanitizeDesc(s string) string {
	// Replace newlines/tabs with spaces, then collapse multiple spaces
	result := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, s)
	// Collapse multiple spaces into one
	for strings.Contains(result, "  ") {
		result = strings.ReplaceAll(result, "  ", " ")
	}
	return strings.TrimSpace(result)
}

// truncateStep truncates a step description to the given width.
func truncateStep(s string, width int) string {
	if len(s) <= width {
		return s
	}
	if width <= 3 {
		return s[:width]
	}
	return s[:width-3] + "..."
}
