// Package main provides test report and share commands.
package main

import (
	"bytes"
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

var internalReportJSONKeys = map[string]struct{}{
	"expected_states":                {},
	"run_config":                     {},
	"s3_bucket":                      {},
	"video_s3_key":                   {},
	"device_logs_s3_key":             {},
	"screenshot_before_s3_key":       {},
	"screenshot_before_clean_s3_key": {},
	"screenshot_after_s3_key":        {},
	"grounding_crop_s3_key":          {},
}

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
	Example: `  revyl test report login-flow
  revyl test report login-flow --json
  revyl test report login-flow --share
  revyl test report login-flow --no-steps`,
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
	Example: `  revyl test share login-flow
  revyl test share login-flow --json`,
	Args: cobra.ExactArgs(1),
	RunE: runTestShare,
}

// resolveToTaskID resolves an argument to a task/execution ID.
// Tries: UUID-like test IDs via history → direct UUID execution/task IDs → test
// name/alias → latest task.
func resolveToTaskID(cmd *cobra.Command, nameOrID string, cfg *config.ProjectConfig, client *api.Client, devMode bool) (taskID string, testName string, err error) {
	// 1. If it looks like a UUID, first preserve test UUID support by resolving
	// the latest execution from history. Otherwise treat it as the execution/task
	// candidate directly and let the final reports-v3 fetch surface the real error.
	if looksLikeUUID(nameOrID) {
		if latestTaskID, latestErr := resolveLatestTaskID(cmd.Context(), client, nameOrID); latestErr == nil {
			return latestTaskID, "", nil
		}
		return nameOrID, "", nil
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
		return "", displayName, fmt.Errorf("no executions found for '%s'. Run 'revyl test run %s' to execute it first", displayName, nameOrID)
	}

	return latestTaskID, displayName, nil
}

func isReportContextRouteMissing(apiErr *api.APIError) bool {
	return apiErr != nil &&
		apiErr.StatusCode == 404 &&
		apiErr.DetailObject == nil &&
		strings.EqualFold(strings.TrimSpace(apiErr.Detail), "Not Found")
}

func isReportContextLegacyMissing(apiErr *api.APIError) bool {
	return apiErr != nil && apiErr.StatusCode == 404 && apiErr.DetailBool("use_legacy")
}

func reportContextDetailMessage(apiErr *api.APIError) string {
	if apiErr == nil {
		return ""
	}
	if message := apiErr.DetailString("message"); message != "" {
		return message
	}
	return apiErr.Detail
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

	// Fetch the canonical context report from the backend.
	includeSteps := !reportNoSteps
	includeActions := includeSteps && jsonOutput
	includeLLMCalls := includeSteps && jsonOutput
	reportEnvelope, err := client.GetReportContextByExecution(
		cmd.Context(),
		taskID,
		includeSteps,
		includeActions,
		includeLLMCalls,
	)
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		// Build the web report URL as a fallback
		fallbackURL := fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(devMode), taskID)

		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			if apiErr.StatusCode == 404 {
				switch {
				case isReportContextLegacyMissing(apiErr):
					ui.PrintWarning("This execution does not have a reports-v3 context report")
					if detail := reportContextDetailMessage(apiErr); detail != "" {
						ui.PrintDim("  %s", detail)
					}
					ui.PrintInfo("The execution exists, but it does not have a reports-v3 /context payload.")
				case isReportContextRouteMissing(apiErr):
					ui.PrintWarning("This backend does not expose the reports-v3 context endpoint yet")
					ui.PrintInfo("Deploy the backend version that serves the reports-v3 /context route for executions.")
				default:
					ui.PrintWarning("No reports-v3 context report available for this execution")
					if detail := reportContextDetailMessage(apiErr); detail != "" {
						ui.PrintDim("  %s", detail)
					}
				}
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

	report := reportEnvelope.Report

	// Prefer the backend's canonical test name when available.
	displayName := stringValue(report.TestName)
	if displayName == "" {
		displayName = testName
	}
	if displayName == "" {
		displayName = nameOrID
	}

	reportURL := stringValue(report.ReportURL)
	if reportURL == "" {
		reportURL = fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(devMode), taskID)
	}

	if jsonOutput {
		if !reportShare {
			data, err := buildUserFacingReportJSON(reportEnvelope.Raw)
			if err != nil {
				return err
			}
			fmt.Println(string(data))
			return nil
		}

		var output map[string]interface{}
		if err := json.Unmarshal(reportEnvelope.Raw, &output); err != nil {
			return fmt.Errorf("failed to parse report JSON: %w", err)
		}

		shareResp, shareErr := client.GenerateShareableLink(cmd.Context(), taskID)
		if shareErr == nil {
			output["shareable_link"] = shareResp.ShareableLink
		}

		data, err := marshalUserFacingReportJSON(output)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	// Display formatted report
	ui.Println()

	// Result header — big pass/fail indicator
	resultIcon := ui.SuccessStyle.Render("✓")
	resultText := "Passed"
	if isFalse(report.Success) || intValue(report.EffectiveFailedSteps) > 0 {
		resultIcon = ui.ErrorStyle.Render("✗")
		resultText = "Failed"
	}
	fmt.Printf("  %s %s  %s\n", resultIcon,
		ui.TitleStyle.Render(displayName),
		ui.DimStyle.Render(fmt.Sprintf("— %s", resultText)))
	ui.Println()

	// Metadata
	if stringValue(report.Platform) != "" {
		ui.PrintKeyValue("Platform:", normalizePlatform(stringValue(report.Platform)))
	}
	if stringValue(report.AppName) != "" {
		appInfo := stringValue(report.AppName)
		if stringValue(report.BuildVersion) != "" {
			appInfo += " v" + stringValue(report.BuildVersion)
		}
		ui.PrintKeyValue("App:", appInfo)
	}
	if stringValue(report.DeviceModel) != "" {
		deviceInfo := stringValue(report.DeviceModel)
		if stringValue(report.OsVersion) != "" {
			deviceInfo += fmt.Sprintf(" (%s)", stringValue(report.OsVersion))
		}
		ui.PrintKeyValue("Device:", deviceInfo)
	}

	// Duration from started_at / completed_at
	if stringValue(report.StartedAt) != "" && stringValue(report.CompletedAt) != "" {
		duration := computeDuration(stringValue(report.StartedAt), stringValue(report.CompletedAt))
		if duration != "" {
			ui.PrintKeyValue("Duration:", duration)
		}
	}

	// Compact step/validation summary
	stepsValue := fmt.Sprintf("%s/%d passed",
		ui.AccentStyle.Render(fmt.Sprintf("%d", intValue(report.EffectivePassedSteps))),
		intValue(report.TotalSteps))
	if intValue(report.EffectiveFailedSteps) > 0 {
		stepsValue += ui.ErrorStyle.Render(fmt.Sprintf(", %d failed", intValue(report.EffectiveFailedSteps)))
	}
	ui.PrintKeyValue("Steps:", stepsValue)

	if intValue(report.TotalValidations) > 0 {
		ui.PrintKeyValue("Validations:", fmt.Sprintf("%d/%d passed",
			intValue(report.ValidationsPassed), intValue(report.TotalValidations)))
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
			stepStatus := strings.ToLower(stringValue(step.EffectiveStatus))
			desc := sanitizeDesc(stringValue(step.StepDescription))

			// Status icon — only the icon is colored
			var statusIcon string
			switch stepStatus {
			case "passed":
				statusIcon = ui.SuccessStyle.Render("✓")
			case "failed":
				statusIcon = ui.ErrorStyle.Render("✗")
			case "warning":
				statusIcon = ui.WarningStyle.Render("⚠")
			case "running":
				statusIcon = ui.AccentStyle.Render("●")
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
			reason := stringValue(step.EffectiveStatusReason)
			if reason != "" && (stepStatus == "failed" || stepStatus == "warning") {
				indent := numWidth + 2 + typeWidth + 2
				fmt.Printf("  %s%s\n",
					strings.Repeat(" ", indent),
					ui.DimStyle.Render(truncateStep(reason, 60)))
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
		{Label: "Run again:", Command: fmt.Sprintf("revyl test run %s", nameOrID)},
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
		data, _ := marshalPrettyJSON(output)
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

func buildUserFacingReportJSON(raw []byte) ([]byte, error) {
	var output interface{}
	if err := json.Unmarshal(raw, &output); err != nil {
		return nil, fmt.Errorf("failed to parse report JSON: %w", err)
	}
	return marshalUserFacingReportJSON(output)
}

func marshalPrettyJSON(value interface{}) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("failed to format report JSON: %w", err)
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

func marshalUserFacingReportJSON(value interface{}) ([]byte, error) {
	sanitizeUserFacingReportJSON(value)

	data, err := marshalPrettyJSON(value)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func sanitizeUserFacingReportJSON(value interface{}) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			if _, internal := internalReportJSONKeys[key]; internal {
				delete(typed, key)
				continue
			}
			sanitizeUserFacingReportJSON(child)
		}
	case []interface{}:
		for _, item := range typed {
			sanitizeUserFacingReportJSON(item)
		}
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func isFalse(value *bool) bool {
	return value != nil && !*value
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
