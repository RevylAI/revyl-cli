// Package main provides test status and history commands.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/ui"
)

var (
	statusOutputJSON               bool
	statusOpen                     bool
	statusBranch                   string
	statusGitCommit                string
	statusSince                    string
	statusFailUnlessAllTestsPassed bool
	historyOutputJSON              bool
	historyLimit                   int
	historyOffset                  int
	historyBranch                  string
	historyGitCommit               string
)

func init() {
	testStatusCmd.Flags().BoolVar(&statusOutputJSON, "json", false, "Output results as JSON")
	testStatusCmd.Flags().BoolVar(&statusOpen, "open", false, "Open report in browser")
	testStatusCmd.Flags().StringVar(&statusBranch, "branch", "", "Show each test's newest run on this branch: the Git branch its tests came from, or its build's Revyl branch when it has no Git branch")
	testStatusCmd.Flags().StringVar(&statusGitCommit, "git-commit-sha", "", "Show each test's newest run of test definitions from this exact lowercase hexadecimal Git commit SHA")
	testStatusCmd.Flags().StringVar(&statusSince, "since", "", "Consider only runs from the last <n>h or <n>d, such as 24h or 7d, and count each test's finished runs and passes")
	testStatusCmd.Flags().BoolVar(&statusFailUnlessAllTestsPassed, "fail-unless-all-tests-passed", false, "Exit 2 when a newest run failed outside quarantine or has no known result, or no test matched, and 3 when one is still queued or running")

	testHistoryCmd.Flags().BoolVar(&historyOutputJSON, "json", false, "Output results as JSON")
	testHistoryCmd.Flags().IntVar(&historyLimit, "limit", 10, "Maximum number of executions to show")
	testHistoryCmd.Flags().IntVar(&historyOffset, "offset", 0, "Number of executions to skip (0-100000)")
	testHistoryCmd.Flags().StringVar(&historyBranch, "branch", "", "Filter executions to runs that used this branch for their tests from Git or for their Revyl build")
	testHistoryCmd.Flags().StringVar(&historyGitCommit, "git-commit-sha", "", "Filter executions to test definition snapshots from this exact lowercase hexadecimal Git commit SHA")
}

// testStatusCmd shows the latest execution status for a test, or each test's
// newest run on a branch or Git commit.
var testStatusCmd = &cobra.Command{
	Use:   "status [name|id]",
	Short: "Show latest execution status",
	Long: `Show the latest execution status for a test, or each test's newest run on a
branch or Git commit.

With only a test name, displays that test's latest execution: status,
duration, step progress, and report link. Accepts test names (config
aliases), display names, or UUIDs.

With --branch or --git-commit-sha and no test name, lists each test's newest
matching run, failed first, then running, unknown and passed. A run is on a
branch when its tests came from that Git branch, or, when it has no Git
branch, when its build came from that Revyl branch. Add a test name to show
only that test's newest matching run.

--since 7d or --since 24h considers only runs from that window, and counts
each test's finished runs and how many of them passed.

--fail-unless-all-tests-passed exits 2 when a newest run failed or has no
known result, or no test matched, and 3 when nothing failed but a run is still
queued or running. A failure quarantined in its workflow doesn't count.
Without it, the command exits 0 whenever it succeeds.

Examples:
  revyl test status login-flow
  revyl test status login-flow --json
  revyl test status login-flow --open
  revyl test status --branch feature/login
  revyl test status --branch feature/login --since 7d
  revyl test status --git-commit-sha <commit-sha> --fail-unless-all-tests-passed`,
	Example: `  revyl test status login-flow
  revyl test status login-flow --json
  revyl test status --branch feature/login
  revyl test status --branch feature/login --since 7d --json
  revyl test status --git-commit-sha <commit-sha> --fail-unless-all-tests-passed`,
	Args: cobra.MaximumNArgs(1),
	RunE: runTestStatus,
}

// testHistoryCmd shows execution history for a test.
var testHistoryCmd = &cobra.Command{
	Use:   "history [name|id]",
	Short: "Show execution history",
	Long: `Show execution history for a test in a table format.

Displays recent executions with status, duration, and step progress. Pass
--branch without a test name to list every execution that used that branch
for its tests from Git or for its Revyl build; with a test name, it narrows
that test's history. Use --git-commit-sha with a full 40- or 64-character
lowercase hexadecimal SHA to query an exact commit. To see each test's newest
run on a branch, use revyl test status --branch.
Accepts test names (config aliases), display names, or UUIDs.

Examples:
  revyl test history login-flow
  revyl test history login-flow --limit 20
  revyl test history login-flow --branch feature/login
  revyl test history --branch feature/login --json`,
	Example: `  revyl test history login-flow
  revyl test history login-flow --limit 20 --json
  revyl test history --branch feature/login --json
  revyl test history login-flow --git-commit-sha <commit-sha> --json`,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) > 1 {
			return cobra.MaximumNArgs(1)(cmd, args)
		}
		if len(args) == 0 && historyBranch == "" && historyGitCommit == "" {
			return fmt.Errorf("provide a test name or ID, or use --branch or --git-commit-sha")
		}
		return nil
	},
	RunE: runTestHistory,
}

// runTestStatus shows the latest execution status for a test, or each test's
// newest run on a branch or Git commit.
func runTestStatus(cmd *cobra.Command, args []string) error {
	// Honor global --json
	jsonOutput := statusOutputJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	devMode, _ := cmd.Flags().GetBool("dev")

	if len(args) == 0 && statusBranch == "" && statusGitCommit == "" {
		return usageErrorf("provide a test name or ID, or use --branch or --git-commit-sha")
	}
	if len(args) == 0 && statusOpen {
		return usageErrorf("--open needs a test name or ID")
	}
	filter := api.LatestRunResultsFilter{RunBranchName: statusBranch, GitCommitSHA: statusGitCommit}
	var sinceWindow string
	if cmd.Flags().Changed("since") {
		createdFrom, window, err := parseStatusSince(statusSince, time.Now())
		if err != nil {
			return err
		}
		filter.CreatedFrom, sinceWindow = &createdFrom, window
	}
	filtered := statusBranch != "" || statusGitCommit != "" || sinceWindow != "" || statusFailUnlessAllTestsPassed

	if len(args) == 0 {
		apiKey, err := getAPIKey()
		if err != nil {
			return err
		}
		if jsonOutput {
			ui.SetQuietMode(true)
			defer ui.SetQuietMode(false)
		}
		return showEveryTestStatus(cmd, api.NewClientWithDevMode(apiKey, devMode), devMode, jsonOutput, filter, sinceWindow)
	}

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
		return testLookupFailure(err)
	}

	// Use the input name for display if resolveTestID didn't return one
	displayName := testName
	if displayName == "" {
		displayName = testNameOrID
	}

	if !jsonOutput {
		ui.StartSpinner("Fetching latest execution...")
	}
	var (
		item          *api.CLIEnhancedHistoryItem
		queuedRun     *api.CLITestRunQueuedInWorkflow
		latestResults *api.CLITestLatestRunResultsResponse
	)
	if filtered {
		filter.TestID = testID
		latestResults, err = getEveryLatestRunResult(cmd.Context(), client, filter)
		if err == nil && len(latestResults.Items) > 0 {
			item, queuedRun = latestResults.Items[0].LatestRun, latestResults.Items[0].LatestRunQueuedInWorkflow
		}
	} else {
		var history *api.CLIEnhancedHistoryResponse
		history, err = client.GetTestEnhancedHistory(cmd.Context(), testID, 1, 0)
		if err == nil && len(history.Items) > 0 {
			item = &history.Items[0]
		}
	}
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to fetch execution status: %v", err)
		return err
	}

	if queuedRun != nil {
		return showQueuedTestStatus(cmd, testID, testNameOrID, displayName, latestResults, filter, sinceWindow, jsonOutput)
	}
	if item == nil {
		selection := testStatusSelection(sinceWindow)
		if jsonOutput {
			output := map[string]interface{}{
				"test_id": testID,
				"status":  "no_executions",
				"message": "No executions found for this test",
			}
			if filtered {
				output["message"] = "No executions found for this test " + selection
				addTestStatusSelection(output, filter)
			}
			data, _ := json.MarshalIndent(output, "", "  ")
			fmt.Println(string(data))
		} else {
			if filtered {
				ui.PrintInfo("No executions found for '%s' %s", displayName, selection)
			} else {
				ui.PrintInfo("No executions found for '%s'", displayName)
			}
			ui.Println()
			ui.PrintNextSteps([]ui.NextStep{
				{Label: "Run this test:", Command: fmt.Sprintf("revyl test run %s", testNameOrID)},
			})
		}
		if filtered {
			return finishTestStatus(cmd, latestResults, sinceWindow, jsonOutput)
		}
		return nil
	}

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
		if task != nil {
			addBuildIdentity(output, task.BuildID, task.BuildVersion, task.BranchID, task.BranchName)
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
		if filtered {
			latestResult := latestResults.Items[0]
			addTestStatusSelection(output, filter)
			addLatestRunFacts(output, latestResult)
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		if filtered {
			return finishTestStatus(cmd, latestResults, sinceWindow, jsonOutput)
		}
		return nil
	}

	ui.Println()

	// Display status summary
	ui.PrintKeyValue("Test:", displayName)
	if filtered {
		ui.PrintKeyValue("Result:", string(latestResults.Items[0].RunResult))
	}
	ui.PrintKeyValue("Status:", status)
	if task != nil {
		if task.BranchName != "" {
			ui.PrintKeyValue("Branch:", task.BranchName)
		}
		if filtered && task.GitBranchName != "" {
			ui.PrintKeyValue("Git branch:", task.GitBranchName)
		}
		if filtered && task.GitCommitSHA != "" {
			ui.PrintKeyValue("Git commit:", truncatePrefix(task.GitCommitSHA, 12))
		}
		if task.BuildID != "" {
			ui.PrintKeyValue("Build ID:", task.BuildID)
		}
	}
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
	if filtered {
		if counts := latestResults.Items[0].RunResultCountsSinceCreatedFrom; counts != nil {
			finishedRuns := finishedRunCount(*counts)
			ui.PrintKeyValue("Finished runs:", fmt.Sprintf("%d in the last %s, of which %s passed", finishedRuns, sinceWindow, passedWithShare(counts.PassedCount, finishedRuns)))
		}
		printRunNoteLines(latestRunNoteLines("This run", latestResults.Items[0]))
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

	if filtered {
		return finishTestStatus(cmd, latestResults, sinceWindow, jsonOutput)
	}
	return nil
}

// queuedRunStatus is a test status run's status while its workflow has queued
// it and its device session doesn't exist yet.
const queuedRunStatus = "queued"

// showQueuedTestStatus shows a test whose newest matching run its workflow has
// queued but not started.
func showQueuedTestStatus(cmd *cobra.Command, testID, testNameOrID, displayName string, latestResults *api.CLITestLatestRunResultsResponse, filter api.LatestRunResultsFilter, sinceWindow string, jsonOutput bool) error {
	latestResult := latestResults.Items[0]
	queuedRun := latestResult.LatestRunQueuedInWorkflow
	if jsonOutput {
		output := map[string]interface{}{"test_id": testID}
		addTestStatusSelection(output, filter)
		addLatestRunFacts(output, latestResult)
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return finishTestStatus(cmd, latestResults, sinceWindow, jsonOutput)
	}

	ui.Println()
	ui.PrintKeyValue("Test:", displayName)
	ui.PrintKeyValue("Result:", string(latestResult.RunResult))
	ui.PrintKeyValue("Status:", queuedRunStatus)
	if queuedRun.GitBranchName != "" {
		ui.PrintKeyValue("Git branch:", queuedRun.GitBranchName)
	}
	if queuedRun.GitCommitSHA != "" {
		ui.PrintKeyValue("Git commit:", truncatePrefix(queuedRun.GitCommitSHA, 12))
	}
	if queuedAt := formatAbsoluteTime(queuedRun.QueuedAt); queuedAt != "-" {
		ui.PrintKeyValue("Queued:", queuedAt)
	}
	if counts := latestResult.RunResultCountsSinceCreatedFrom; counts != nil {
		finishedRuns := finishedRunCount(*counts)
		ui.PrintKeyValue("Finished runs:", fmt.Sprintf("%d in the last %s, of which %s passed", finishedRuns, sinceWindow, passedWithShare(counts.PassedCount, finishedRuns)))
	}
	printRunNoteLines(latestRunNoteLines("This run", latestResult))
	if statusOpen {
		ui.Println()
		ui.PrintInfo("This run has no report to open until it starts.")
	}

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "View its workflow run:", Command: fmt.Sprintf("revyl workflow status %s", queuedRun.WorkflowExecutionID)},
		{Label: "View history:", Command: fmt.Sprintf("revyl test history %s", testNameOrID)},
	})
	return finishTestStatus(cmd, latestResults, sinceWindow, jsonOutput)
}

// showEveryTestStatus lists each test's newest run that matches filter, with
// the newest runs' result counts and, with --since, each test's finished runs.
func showEveryTestStatus(cmd *cobra.Command, client *api.Client, devMode, jsonOutput bool, filter api.LatestRunResultsFilter, sinceWindow string) error {
	if !jsonOutput {
		ui.StartSpinner("Fetching each test's newest run...")
	}
	results, err := getEveryLatestRunResult(cmd.Context(), client, filter)
	if !jsonOutput {
		ui.StopSpinner()
	}
	if err != nil {
		ui.PrintError("Failed to fetch test status: %v", err)
		return err
	}

	if jsonOutput {
		tests := make([]map[string]interface{}, 0, len(results.Items))
		for _, result := range results.Items {
			entry := map[string]interface{}{
				"test_id":   result.TestID,
				"test_name": latestRunTestName(result),
			}
			if result.LatestRun != nil {
				entry["report_url"] = fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(devMode), result.LatestRun.EnhancedTask.ID)
			}
			addLatestRunFacts(entry, result)
			tests = append(tests, entry)
		}
		output := map[string]interface{}{
			"test_count":                          results.TotalCount,
			"latest_run_result_counts":            results.LatestRunResultCounts,
			"quarantined_failed_latest_run_count": results.QuarantinedFailedLatestRunCount,
			"tests":                               tests,
		}
		addTestStatusSelection(output, filter)
		if results.RunResultCountsSinceCreatedFrom != nil {
			output["run_result_counts_since_created_from"] = results.RunResultCountsSinceCreatedFrom
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return finishTestStatus(cmd, results, sinceWindow, jsonOutput)
	}

	counts := results.LatestRunResultCounts
	testNoun := "tests"
	if results.TotalCount == 1 {
		testNoun = "test"
	}
	ui.Println()
	ui.PrintInfo("Newest run of each test %s: %d %s, %d passed, %d failed, %d running, %d unknown",
		testStatusSelection(sinceWindow), results.TotalCount, testNoun, counts.PassedCount, counts.FailedCount, counts.RunningCount, counts.UnknownCount)
	if since := results.RunResultCountsSinceCreatedFrom; since != nil {
		finishedRuns := finishedRunCount(*since)
		ui.PrintInfo("Finished runs in the last %s: %d, of which %s passed", sinceWindow, finishedRuns, passedWithShare(since.PassedCount, finishedRuns))
	}
	if len(results.Items) == 0 {
		return finishTestStatus(cmd, results, sinceWindow, jsonOutput)
	}
	ui.Println()

	headers := []string{"#", "TEST", "RESULT", "LAST RUN", "DURATION", "GIT COMMIT", "SESSION"}
	if results.RunResultCountsSinceCreatedFrom != nil {
		headers = []string{"#", "TEST", "RESULT", "LAST RUN", "RUNS", "PASSED", "GIT COMMIT"}
	}
	table := ui.NewTable(headers...)
	for i, result := range results.Items {
		lastRun, duration, commitSHA, sessionID := "-", "-", "-", "-"
		runTime, gitCommitSHA := "", ""
		if queuedRun := result.LatestRunQueuedInWorkflow; queuedRun != nil {
			runTime, gitCommitSHA = queuedRun.QueuedAt, queuedRun.GitCommitSHA
		} else {
			task := result.LatestRun.EnhancedTask
			runTime, gitCommitSHA = result.LatestRun.ExecutionTime, task.GitCommitSHA
			if result.LatestRun.Duration != nil {
				duration = formatDurationSecs(*result.LatestRun.Duration)
			}
			if task.SessionID != "" {
				sessionID = truncatePrefix(task.SessionID, 8)
			}
		}
		if runAt, ok := parseRunTime(runTime); ok {
			lastRun = mcppkg.FormatAge(time.Since(runAt)) + " ago"
		}
		if gitCommitSHA != "" {
			commitSHA = truncatePrefix(gitCommitSHA, 12)
		}
		row := []string{fmt.Sprintf("%d", i+1), latestRunTestName(result), string(result.RunResult), lastRun}
		if counts := result.RunResultCountsSinceCreatedFrom; counts != nil {
			finishedRuns := finishedRunCount(*counts)
			row = append(row, fmt.Sprintf("%d", finishedRuns), passedWithShare(counts.PassedCount, finishedRuns), commitSHA)
		} else {
			row = append(row, duration, commitSHA, sessionID)
		}
		table.AddRow(row...)
	}
	table.Render()
	printRunNoteLines(runListNoteLines(len(results.Items), runNumberLabel, "Every run", func(index int, runLabel string) []string {
		return latestRunNoteLines(runLabel, results.Items[index])
	}))
	return finishTestStatus(cmd, results, sinceWindow, jsonOutput)
}

// getEveryLatestRunResult reads every page of each test's newest run that
// matches filter. A run that changes between two pages moves its test to
// another page, so a changed total or a repeated test reads every page again.
// When runs keep changing, the last read stands: each test once with its
// newest row, the newest page's totals, which decide the exit code, and a
// warning that the list may miss a test.
func getEveryLatestRunResult(ctx context.Context, client *api.Client, filter api.LatestRunResultsFilter) (*api.CLITestLatestRunResultsResponse, error) {
	// Every page recomputes each test's newest run, so pages are as large as the API allows.
	const pageSize, maxReads = 1000, 3
	for read := 1; ; read++ {
		var results *api.CLITestLatestRunResultsResponse
		positionByTestID := map[string]int{}
		changed := false
		for fetched := 0; ; {
			page, err := client.GetLatestRunResults(ctx, filter, pageSize, fetched)
			if err != nil {
				return nil, err
			}
			fetched += len(page.Items)
			if results == nil {
				results = &api.CLITestLatestRunResultsResponse{Items: make([]api.CLITestLatestRunResult, 0, len(page.Items))}
			} else if page.TotalCount != results.TotalCount ||
				page.LatestRunResultCounts != results.LatestRunResultCounts ||
				page.QuarantinedFailedLatestRunCount != results.QuarantinedFailedLatestRunCount ||
				!reflect.DeepEqual(page.RunResultCountsSinceCreatedFrom, results.RunResultCountsSinceCreatedFrom) {
				changed = true
			}
			results.TotalCount, results.LatestRunResultCounts = page.TotalCount, page.LatestRunResultCounts
			results.QuarantinedFailedLatestRunCount, results.RunResultCountsSinceCreatedFrom = page.QuarantinedFailedLatestRunCount, page.RunResultCountsSinceCreatedFrom
			for _, result := range page.Items {
				if result.LatestRunQueuedInWorkflow == nil && (result.LatestRun == nil || result.LatestRun.EnhancedTask == nil) {
					return nil, fmt.Errorf("the server sent test %s's newest run without its details", result.TestID)
				}
				if position, seen := positionByTestID[result.TestID]; seen {
					changed = true
					results.Items[position] = result
					continue
				}
				positionByTestID[result.TestID] = len(results.Items)
				results.Items = append(results.Items, result)
			}
			if len(page.Items) == 0 || fetched >= page.TotalCount {
				break
			}
		}
		if !changed {
			return results, nil
		}
		if read == maxReads {
			ui.PrintWarning("Tests' newest runs kept changing while their pages were read, so this list may miss a test or show it out of order. The counts and the exit code come from the last page read.")
			return results, nil
		}
	}
}

func latestRunTestName(result api.CLITestLatestRunResult) string {
	if result.TestName != "" {
		return result.TestName
	}
	return result.TestID
}

// addLatestRunFacts adds a test's newest matching run, its result and, with
// --since, its run counts to a test status JSON object.
func addLatestRunFacts(output map[string]interface{}, result api.CLITestLatestRunResult) {
	output["run_result"] = result.RunResult
	if result.RunResultCountsSinceCreatedFrom != nil {
		output["run_result_counts_since_created_from"] = result.RunResultCountsSinceCreatedFrom
	}
	if queuedRun := result.LatestRunQueuedInWorkflow; queuedRun != nil {
		output["status"] = queuedRunStatus
		output["workflow_task_id"] = queuedRun.WorkflowExecutionID
		output["queued_at"] = queuedRun.QueuedAt
		output["test_definition_source"] = queuedRun.TestDefinitionSource
		output["has_uncommitted_definition_changes"] = queuedRun.HasUncommittedDefinitionChanges
		if queuedRun.GitBranchName != "" {
			output["git_branch_name"] = queuedRun.GitBranchName
		}
		if queuedRun.GitCommitSHA != "" {
			output["git_commit_sha"] = queuedRun.GitCommitSHA
		}
		return
	}
	task := result.LatestRun.EnhancedTask
	output["was_quarantined"] = task.WasQuarantined
	output["task_id"] = task.ID
	output["status"] = task.Status
	for key, value := range map[string]string{
		"session_id":             task.SessionID,
		"started_at":             task.StartedAt,
		"branch_name":            task.BranchName,
		"build_version":          task.BuildVersion,
		"git_branch_name":        task.GitBranchName,
		"git_commit_sha":         task.GitCommitSHA,
		"test_definition_source": task.TestDefinitionSource,
	} {
		if value != "" {
			output[key] = value
		}
	}
	if result.LatestRun.Duration != nil {
		output["duration"] = *result.LatestRun.Duration
	}
	if task.HasUncommittedDefinitionChanges != nil {
		output["has_uncommitted_definition_changes"] = *task.HasUncommittedDefinitionChanges
	}
}

// latestRunNoteLines are the sentences printed about a test's newest run: its
// provenance notes, that its workflow hasn't started it yet, and that its
// failure was quarantined in its workflow.
func latestRunNoteLines(runLabel string, result api.CLITestLatestRunResult) []string {
	if queuedRun := result.LatestRunQueuedInWorkflow; queuedRun != nil {
		lines := runNoteLines(runLabel, runBranches{gitBranchName: queuedRun.GitBranchName}, &queuedRun.HasUncommittedDefinitionChanges, queuedRun.GitCommitSHA)
		return append(lines, runLabel+" is queued in its workflow and has no device session yet.")
	}
	task := result.LatestRun.EnhancedTask
	lines := runNoteLines(runLabel, enhancedTaskRunBranches(task), task.HasUncommittedDefinitionChanges, task.GitCommitSHA)
	if result.RunResult == api.TestRunResultFailed && task.WasQuarantined {
		lines = append(lines, runLabel+" was quarantined in its workflow, so its failure doesn't stop --fail-unless-all-tests-passed.")
	}
	return lines
}

func addTestStatusSelection(output map[string]interface{}, filter api.LatestRunResultsFilter) {
	if filter.RunBranchName != "" {
		output["branch"] = filter.RunBranchName
	}
	if filter.GitCommitSHA != "" {
		output["git_commit_sha"] = filter.GitCommitSHA
	}
	if filter.CreatedFrom != nil {
		output["created_from"] = filter.CreatedFrom.UTC().Format(time.RFC3339)
	}
}

// testStatusSelection names the runs test status considered, such as
// "on branch \"main\" in the last 7 days".
func testStatusSelection(sinceWindow string) string {
	var parts []string
	if statusBranch != "" {
		parts = append(parts, fmt.Sprintf("on branch \"%s\"", statusBranch))
	}
	if statusGitCommit != "" {
		parts = append(parts, "at Git commit "+truncatePrefix(statusGitCommit, 12))
	}
	if sinceWindow != "" {
		parts = append(parts, "in the last "+sinceWindow)
	}
	return strings.Join(parts, " ")
}

var statusSincePattern = regexp.MustCompile(`^([1-9][0-9]*)([hd])$`)

// parseStatusSince turns --since's <n>h or <n>d into the earliest creation time
// a run may have, and the window in words, such as "7 days" or "hour".
func parseStatusSince(value string, now time.Time) (time.Time, string, error) {
	invalid := usageErrorf("--since must be a whole number of hours or days above zero, such as 24h or 7d, not %q", value)
	match := statusSincePattern.FindStringSubmatch(value)
	if match == nil {
		return time.Time{}, "", invalid
	}
	count, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return time.Time{}, "", invalid
	}
	unit := map[string]string{"h": "hour", "d": "day"}[match[2]]
	window := fmt.Sprintf("%d %ss", count, unit)
	if count == 1 {
		window = unit
	}
	// The API accepts no time before year 1, and no run is that old.
	earliest := time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC)
	days, hours := count, int64(0)
	if match[2] == "h" {
		days, hours = count/24, count%24
	}
	if days > (now.Unix()-earliest.Unix())/(24*60*60) {
		return earliest, window, nil
	}
	createdFrom := now.AddDate(0, 0, -int(days)).Add(-time.Duration(hours) * time.Hour)
	if createdFrom.Before(earliest) {
		return earliest, window, nil
	}
	return createdFrom, window, nil
}

func finishedRunCount(counts api.RunResultCounts) int {
	return counts.PassedCount + counts.FailedCount + counts.UnknownCount
}

// passedWithShare shows passed runs with their share of finished runs, rounded
// down so that only every run passing shows 100%.
func passedWithShare(passedRuns, finishedRuns int) string {
	if finishedRuns == 0 {
		return fmt.Sprintf("%d", passedRuns)
	}
	return fmt.Sprintf("%d (%d%%)", passedRuns, int(math.Floor(100*float64(passedRuns)/float64(finishedRuns))))
}

// finishTestStatus records the newest runs' combined result and, with
// --fail-unless-all-tests-passed, ends the command with its exit code.
func finishTestStatus(cmd *cobra.Command, results *api.CLITestLatestRunResultsResponse, sinceWindow string, jsonOutput bool) error {
	counts := results.LatestRunResultCounts
	failedOutsideQuarantine := counts.FailedCount - results.QuarantinedFailedLatestRunCount
	completion := analytics.CommandCompletion{Domain: "test_status"}
	var message string
	switch {
	case results.TotalCount == 0:
		completion.DomainStatus, completion.ExitCode = "no_match", 2
		message = "No test has a run"
		if selection := testStatusSelection(sinceWindow); selection != "" {
			message += " " + selection
		}
	case failedOutsideQuarantine > 0 || counts.UnknownCount > 0:
		completion.DomainStatus, completion.ExitCode = "failed", 2
		var reasons []string
		if failedOutsideQuarantine > 0 {
			reasons = append(reasons, fmt.Sprintf("%d failed", failedOutsideQuarantine))
		}
		if counts.UnknownCount > 0 {
			reasons = append(reasons, fmt.Sprintf("%d with no known result", counts.UnknownCount))
		}
		message = "Not every test passed: " + strings.Join(reasons, ", ")
	case counts.RunningCount > 0:
		completion.DomainStatus, completion.ExitCode = "running", 3
		message = fmt.Sprintf("Not every test has finished: %d still queued or running", counts.RunningCount)
	default:
		completion.DomainStatus = "passed"
	}
	if !statusFailUnlessAllTestsPassed {
		completion.ExitCode = 0
	}
	analytics.SetCommandCompletion(cmd.Context(), completion)
	if completion.ExitCode == 0 {
		if statusFailUnlessAllTestsPassed && !jsonOutput {
			passedLine := "Every test passed"
			switch quarantined := results.QuarantinedFailedLatestRunCount; {
			case quarantined == 1:
				passedLine += ", apart from 1 whose failure was quarantined in its workflow"
			case quarantined > 1:
				passedLine += fmt.Sprintf(", apart from %d whose failures were quarantined in their workflows", quarantined)
			}
			ui.Println()
			ui.PrintSuccess("%s", passedLine)
		}
		return nil
	}
	return &commandExitError{code: completion.ExitCode, err: analytics.CompletedWithExitCode(errors.New(message), completion)}
}

// parseRunTime parses a run's ISO 8601 start or creation time.
func parseRunTime(isoTimestamp string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999"} {
		if parsed, err := time.Parse(layout, isoTimestamp); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// runTestHistory shows execution history for a test.
func runTestHistory(cmd *cobra.Command, args []string) error {
	// Honor global --json
	jsonOutput := historyOutputJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	devMode, _ := cmd.Flags().GetBool("dev")

	var (
		cfg          *config.ProjectConfig
		client       *api.Client
		testNameOrID string
		err          error
	)
	if len(args) == 0 {
		apiKey, keyErr := getAPIKey()
		if keyErr != nil {
			return keyErr
		}
		client = api.NewClientWithDevMode(apiKey, devMode)
	} else {
		_, cfg, client, err = loadConfigAndClient(devMode)
		if err != nil {
			return err
		}
		testNameOrID = args[0]
	}

	if jsonOutput {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
	}

	var (
		testID      string
		testName    string
		displayName string
	)
	if testNameOrID != "" {
		var resolveErr error
		testID, testName, resolveErr = resolveTestID(cmd.Context(), testNameOrID, cfg, client)
		if resolveErr != nil {
			return testLookupFailure(resolveErr)
		}
		displayName = testName
		if displayName == "" {
			displayName = testNameOrID
		}
	}

	if !jsonOutput {
		ui.StartSpinner("Fetching execution history...")
	}
	if historyOffset < 0 || historyOffset > 100000 {
		return fmt.Errorf("--offset must be 0-100000")
	}
	history, err := client.GetTestEnhancedHistoryWithOptions(cmd.Context(), testID, historyBranch, historyGitCommit, historyLimit, historyOffset)
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
				"total_count": history.TotalCount,
				"offset":      historyOffset,
				"has_more":    false,
			}
			addHistorySelection(output)
			data, _ := json.MarshalIndent(output, "", "  ")
			fmt.Println(string(data))
			return nil
		}
		var selection string
		switch {
		case historyGitCommit != "":
			selection = fmt.Sprintf(" at Git commit \"%s\"", historyGitCommit)
		case historyBranch != "":
			selection = fmt.Sprintf(" on branch \"%s\"", historyBranch)
		}
		if testNameOrID == "" {
			ui.PrintInfo("No executions found%s", selection)
			return nil
		}
		ui.PrintInfo("No executions found for '%s'%s", displayName, selection)
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
				"success":    nil,
			}
			if item.Duration != nil {
				entry["duration"] = *item.Duration
				entry["duration_formatted"] = formatDurationSecs(*item.Duration)
			}
			if item.EnhancedTask != nil {
				addBuildIdentity(entry, item.EnhancedTask.BuildID, item.EnhancedTask.BuildVersion, item.EnhancedTask.BranchID, item.EnhancedTask.BranchName)
				addRunBranchFacts(entry, enhancedTaskRunBranches(item.EnhancedTask))
				entry["task_id"] = item.EnhancedTask.ID
				entry["steps_completed"] = item.EnhancedTask.StepsCompleted
				entry["total_steps"] = item.EnhancedTask.TotalSteps
				entry["started_at"] = item.EnhancedTask.StartedAt
				entry["success"] = item.EnhancedTask.Success
				if item.EnhancedTask.SessionID != "" {
					entry["session_id"] = item.EnhancedTask.SessionID
				}
				if item.EnhancedTask.TestName != "" {
					entry["test_name"] = item.EnhancedTask.TestName
				}
				if item.EnhancedTask.GitBranchName != "" {
					entry["git_branch_name"] = item.EnhancedTask.GitBranchName
				}
				if item.EnhancedTask.GitCommitSHA != "" {
					entry["git_commit_sha"] = item.EnhancedTask.GitCommitSHA
				}
				if item.EnhancedTask.HasUncommittedDefinitionChanges != nil {
					entry["has_uncommitted_definition_changes"] = *item.EnhancedTask.HasUncommittedDefinitionChanges
				}
				if item.EnhancedTask.TestDefinitionSource != "" {
					entry["test_definition_source"] = item.EnhancedTask.TestDefinitionSource
				}
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
			"offset":      historyOffset,
			"has_more":    historyOffset+len(history.Items) < history.TotalCount,
		}
		addHistorySelection(output)
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.Println()
	if testNameOrID == "" {
		if historyGitCommit != "" {
			ui.PrintInfo("Execution history for Git commit \"%s\" (%d total)", historyGitCommit, history.TotalCount)
		} else {
			ui.PrintInfo("Execution history for branch \"%s\" (%d total)", historyBranch, history.TotalCount)
		}
	} else {
		ui.PrintInfo("Execution history for \"%s\" (%d total)", displayName, history.TotalCount)
	}
	ui.Println()

	if testNameOrID == "" {
		table := ui.NewTable("#", "TEST", "STATUS", "DURATION", "SESSION", "DATE", "REVYL BRANCH", "GIT BRANCH", "GIT COMMIT")
		for i, item := range history.Items {
			name := item.TestUID
			duration := "-"
			if item.Duration != nil {
				duration = formatDurationSecs(*item.Duration)
			}
			sessionID := "-"
			commitSHA := "-"
			revylBranch := "-"
			gitBranch := "-"
			if item.EnhancedTask != nil {
				if item.EnhancedTask.TestName != "" {
					name = item.EnhancedTask.TestName
				}
				if item.EnhancedTask.SessionID != "" {
					sessionID = truncatePrefix(item.EnhancedTask.SessionID, 8)
				}
				if item.EnhancedTask.GitCommitSHA != "" {
					commitSHA = truncatePrefix(item.EnhancedTask.GitCommitSHA, 12)
				}
				if item.EnhancedTask.BranchName != "" {
					revylBranch = item.EnhancedTask.BranchName
				}
				if item.EnhancedTask.GitBranchName != "" {
					gitBranch = item.EnhancedTask.GitBranchName
				}
			}
			table.AddRow(fmt.Sprintf("%d", i+1), name, strings.ToLower(item.Status), duration, sessionID, formatAbsoluteTime(item.ExecutionTime), revylBranch, gitBranch, commitSHA)
		}
		table.Render()
		printRunNoteLines(historyListNoteLines(history.Items))
		return nil
	}

	table := ui.NewTable("#", "STATUS", "DURATION", "STEPS", "DATE", "REVYL BRANCH", "BUILD", "GIT BRANCH")
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

		branch, build, gitBranch := "-", "-", "-"
		if item.EnhancedTask != nil {
			if item.EnhancedTask.BranchName != "" {
				branch = item.EnhancedTask.BranchName
			}
			if item.EnhancedTask.BuildVersion != "" {
				build = item.EnhancedTask.BuildVersion
			} else if item.EnhancedTask.BuildID != "" {
				build = item.EnhancedTask.BuildID
			}
			if item.EnhancedTask.GitBranchName != "" {
				gitBranch = item.EnhancedTask.GitBranchName
			}
		}
		table.AddRow(
			fmt.Sprintf("%d", i+1),
			strings.ToLower(status),
			duration,
			steps,
			dateStr,
			branch,
			build,
			gitBranch,
		)
	}

	table.Render()
	printRunNoteLines(historyListNoteLines(history.Items))

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

func addHistorySelection(output map[string]interface{}) {
	if historyBranch != "" {
		output["branch"] = historyBranch
	}
	if historyGitCommit != "" {
		output["git_commit_sha"] = historyGitCommit
	}
}

func addBuildIdentity(output map[string]interface{}, buildID, buildVersion, branchID, branchName string) {
	for key, value := range map[string]string{"build_id": buildID, "build_version": buildVersion, "branch_id": branchID, "branch_name": branchName} {
		if value != "" {
			output[key] = value
		}
	}
}
