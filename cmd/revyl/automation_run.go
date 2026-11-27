package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/agentinfo"
	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
)

const (
	automationRunPollInterval        = 5 * time.Second
	automationRunPollFailureWindow   = 2 * time.Minute
	automationRunAgentDefaultTimeout = 110 * time.Minute
	automationIdempotencyKeyMaxChars = 512

	automationRunExitTimeout     = 124
	automationRunExitInterrupted = 130
)

const (
	automationRunStatusQueued    = string(api.AutomationLastRunResponseRunStatusQueued)
	automationRunStatusRunning   = string(api.AutomationLastRunResponseRunStatusRunning)
	automationRunStatusCompleted = string(api.AutomationLastRunResponseRunStatusCompleted)
	automationRunStatusFailed    = string(api.AutomationLastRunResponseRunStatusFailed)
	automationRunStatusCancelled = string(api.AutomationLastRunResponseRunStatusCancelled)
)

// automationRunWaitEnd is why a wait for runs stopped.
type automationRunWaitEnd string

const (
	automationRunWaitEnded       automationRunWaitEnd = "ended"
	automationRunWaitTimedOut    automationRunWaitEnd = "timed_out"
	automationRunWaitInterrupted automationRunWaitEnd = "interrupted"
)

func newAutomationRunCommand() *cobra.Command {
	run := &cobra.Command{
		Use:   "run",
		Short: "Start runs, follow them, and read, list and cancel them",
		Long: `Start runs, follow them, and read, list and cancel them.

A run is one time an automation took its action: a trigger fired, and the run
went from queued to running to completed, failed or cancelled. A run is
addressed by its ID.

Exit status reports the run, not the verdict of what it produced: completed
means the action ran to its end (for run_test, that the test ran, whether it
passed or failed), and the summary links to what it produced.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}

	start := &cobra.Command{
		Use:   "start <automation-name|id>",
		Short: "Start a run now and block until it ends",
		Long: `Start a run now, through the automation's manual trigger, and block until it
ends. It works while the automation is off.

--idempotency-key makes a retry safe: the same key returns the run it already
started instead of starting a second one. With --no-wait, the command prints the
new run's ID as soon as Revyl accepts it and returns.

Exit status:
  0    the run completed: its action ran to its end, whatever the verdict of
       what it produced
  1    the run failed or was cancelled, or the command itself failed
  124  --wait-timeout elapsed while the run keeps going; pick the wait back up
       with 'revyl automation run wait'
  130  interrupted while the run keeps going

Inside an agent sandbox, --wait-timeout defaults to 110m; elsewhere the
command waits as long as the run takes.`,
		Example: `  revyl automation run start nightly-smoke
  revyl automation run start nightly-smoke --idempotency-key "$CI_PIPELINE_ID" --no-wait
  revyl automation run start nightly-smoke --wait-timeout 30m --json`,
		Args: cobra.ExactArgs(1),
		RunE: runAutomationRunStart,
	}
	start.Flags().String("idempotency-key", "", "Makes a retry safe: the same key returns the run already started instead of a second one (1 to 512 characters)")
	start.Flags().Bool("no-wait", false, "Don't block: print the new run's ID as soon as Revyl accepts it, then return")
	start.Flags().Duration("wait-timeout", 0, "Block at most this long, then exit 124 while the run keeps going; defaults to 110m inside an agent sandbox")
	start.MarkFlagsMutuallyExclusive("no-wait", "wait-timeout")

	wait := &cobra.Command{
		Use:   "wait <automation-run-id>...",
		Short: "Block until runs end",
		Long: `Block until runs end, whichever trigger started them. Running it again picks the
wait back up.

Exit status:
  0    every run completed
  1    a run failed or was cancelled, or the command itself failed
  124  --wait-timeout elapsed with a run still going
  130  interrupted with a run still going

--wait-timeout 0s checks once and returns. Inside an agent sandbox,
--wait-timeout defaults to 110m; elsewhere the command waits as long as the runs
take. With --json, stdout is an array of the runs, in the order given.`,
		Example: `  revyl automation run wait <automation-run-id>
  revyl automation run wait <automation-run-id> <automation-run-id> --wait-timeout 0s --json`,
		Args: cobra.MinimumNArgs(1),
		RunE: runAutomationRunWait,
	}
	wait.Flags().Duration("wait-timeout", 0, "Block at most this long, then exit 124; 0s checks once and returns; defaults to 110m inside an agent sandbox")

	get := &cobra.Command{
		Use:   "get <automation-run-id>",
		Short: "Read one run",
		Long: `Read one run: its status, the trigger that started it and that trigger's event
(the schedule slot, the webhook request, the pull request, or who started it by
hand), the action it ran with, what it produced (for run_test, the test
execution and its report link), and why it failed if it did.`,
		Example: "  revyl automation run get <automation-run-id> --json",
		Args:    cobra.ExactArgs(1),
		RunE:    runAutomationRunGet,
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List runs, newest first, across every automation",
		Long: `List runs, newest first, across every automation in your organization.

Every filter applies before the page is cut. When there are more runs, the
command prints the cursor that reads the next page.`,
		Example: `  revyl automation run list --automation nightly-smoke
  revyl automation run list --run-status failed --run-status cancelled --trigger-type schedule
  revyl automation run list --produced-test-execution-id <test-execution-id> --json`,
		Args: cobra.NoArgs,
		RunE: runAutomationRunList,
	}
	list.Flags().String("automation", "", "Only this automation's runs: its name or ID")
	list.Flags().StringArray("run-status", nil, "Only runs in this status: queued, running, completed, failed or cancelled; repeat for any of several")
	list.Flags().StringArray("trigger-type", nil, "Only runs that a trigger of this type started, e.g. manual; repeat for any of several")
	list.Flags().String("automation-trigger-id", "", "Only runs this one trigger started")
	list.Flags().String("produced-test-execution-id", "", "The run that started this test execution (the task ID 'revyl test report' takes)")
	list.Flags().Int("limit", automationListDefaultLimit, "Page size, 1 to 100")
	list.Flags().String("cursor", "", "The next_cursor from the previous page")

	cancel := &cobra.Command{
		Use:   "cancel <automation-run-id>",
		Short: "Cancel a queued or running run",
		Long: `Cancel a run that's queued or running, along with what it started that's still
running (for run_test, its test execution).

While the run is starting its action, Revyl can't cancel it yet: the command
says so and exits 1, and running it again a moment later cancels the run.`,
		Example: "  revyl automation run cancel <automation-run-id>",
		Args:    cobra.ExactArgs(1),
		RunE:    runAutomationRunCancel,
	}

	run.AddCommand(start, wait, get, list, cancel)
	recordAnalyticsDomainBeforeEachRun(run, automationRunAnalyticsDomain)
	return run
}

// automationRunWaitLimit reads --wait-timeout. limited is false when the
// command may wait as long as the runs take.
func automationRunWaitLimit(cmd *cobra.Command) (timeout time.Duration, limited bool, err error) {
	if !cmd.Flags().Changed("wait-timeout") {
		if agentinfo.Detect().Name != "" {
			return automationRunAgentDefaultTimeout, true, nil
		}
		return 0, false, nil
	}
	timeout, _ = cmd.Flags().GetDuration("wait-timeout")
	if timeout < 0 {
		return 0, false, usageErrorf("--wait-timeout can't be negative: pass a duration such as 30m, or 0s to check once")
	}
	return timeout, true, nil
}

func automationRunNotFound(runID string) error {
	return usageErrorf("no run in your organization has ID %s; list them with 'revyl automation run list'", runID)
}

func runAutomationRunStart(cmd *cobra.Command, args []string) error {
	noWait, _ := cmd.Flags().GetBool("no-wait")
	idempotencyKey, _ := cmd.Flags().GetString("idempotency-key")
	if cmd.Flags().Changed("idempotency-key") {
		if length := len([]rune(idempotencyKey)); length < 1 || length > automationIdempotencyKeyMaxChars {
			return usageErrorf("--idempotency-key must be 1 to %d characters", automationIdempotencyKeyMaxChars)
		}
	}
	timeout, limited, err := automationRunWaitLimit(cmd)
	if err != nil {
		return err
	}

	client, err := automationClient(cmd)
	if err != nil {
		return err
	}
	automation, err := resolveAutomation(cmd, client, automationRunAnalyticsDomain, args[0])
	if err != nil {
		return err
	}
	signalCtx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	started, err := client.StartAutomationRun(signalCtx, automation.Id.String(), idempotencyKey)
	if err != nil {
		if !automationStartMayHaveRun(err) {
			return automationRequestError(cmd, automationRunAnalyticsDomain, "start run", err)
		}
		retry := "pass --idempotency-key to make a retry safe"
		if idempotencyKey != "" {
			retry = "retrying with the same --idempotency-key is safe"
		}
		err = fmt.Errorf("%w; the run may have started anyway: check with 'revyl automation run list --automation %s' (%s)", err, automation.Id, retry)
		if signalCtx.Err() != nil {
			completion := analytics.CommandCompletion{ExitCode: automationRunExitInterrupted, Domain: automationRunAnalyticsDomain, DomainStatus: string(automationRunWaitInterrupted)}
			return &commandExitError{code: automationRunExitInterrupted, err: analytics.CompletedWithExitCode(fmt.Errorf("start run: %w", err), completion)}
		}
		return automationRequestError(cmd, automationRunAnalyticsDomain, "start run", err)
	}

	runID := started.Id.String()
	recordAutomationOutcome(cmd, automationRunAnalyticsDomain, string(started.Outcome), runID)
	progress := automationProgress(cmd)
	if started.Outcome == api.AutomationManualRunResponseOutcomeDuplicate {
		fmt.Fprintf(progress, "This --idempotency-key already started run %s of %s, so no second run started.\n", runID, automation.Name)
	} else {
		fmt.Fprintf(progress, "Started run %s of %s.\n", runID, automation.Name)
	}
	if noWait {
		if automationJSONOutput(cmd) {
			return writeAutomationJSON(cmd, started)
		}
		fmt.Fprintln(cmd.OutOrStdout(), runID)
		fmt.Fprintf(progress, "Follow it with: revyl automation run wait %s\n", runID)
		return nil
	}

	runs, end, err := waitForAutomationRuns(signalCtx, cmd, client, []string{runID}, timeout, limited)
	if err != nil {
		return err
	}
	return finishAutomationRunWait(cmd, runs, end, runID)
}

// automationStartMayHaveRun reports a failure that leaves unknown whether the
// server started the run: no answer at all, an interrupt, or a server error.
func automationStartMayHaveRun(err error) bool {
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		return true
	}
	return apiErr.StatusCode >= http.StatusInternalServerError
}

func runAutomationRunWait(cmd *cobra.Command, args []string) error {
	var runIDs []string
	for _, arg := range args {
		runID, err := parseAutomationRecordID("run", arg)
		if err != nil {
			return err
		}
		if !slices.Contains(runIDs, runID) {
			runIDs = append(runIDs, runID)
		}
	}
	timeout, limited, err := automationRunWaitLimit(cmd)
	if err != nil {
		return err
	}
	client, err := automationClient(cmd)
	if err != nil {
		return err
	}
	runs, end, err := waitForAutomationRuns(cmd.Context(), cmd, client, runIDs, timeout, limited)
	if err != nil {
		return err
	}
	return finishAutomationRunWait(cmd, runs, end, "")
}

// waitForAutomationRuns reads each run, then polls the ones still going until
// they all end, the limit passes, or a signal arrives. Every read counts against
// the limit, except that --wait-timeout 0s reads each run once. A read that may
// recover (no answer, 5xx, 429) is retried for up to two minutes per run.
func waitForAutomationRuns(ctx context.Context, cmd *cobra.Command, client *api.Client, runIDs []string, timeout time.Duration, limited bool) ([]*api.AutomationRunDetailResponse, automationRunWaitEnd, error) {
	signalCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	waitCtx := signalCtx
	if limited {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(signalCtx, timeout)
		defer cancel()
	}
	checkOnce := limited && timeout == 0
	readCtx := waitCtx
	if checkOnce {
		readCtx = signalCtx
	}
	progress := automationProgress(cmd)

	runs := make([]*api.AutomationRunDetailResponse, len(runIDs))
	stopped := func() ([]*api.AutomationRunDetailResponse, automationRunWaitEnd, error) {
		end, code, reason := automationRunWaitTimedOut, automationRunExitTimeout, "--wait-timeout elapsed"
		if signalCtx.Err() != nil {
			end, code, reason = automationRunWaitInterrupted, automationRunExitInterrupted, "interrupted"
		}
		for index, run := range runs {
			if run == nil {
				completion := analytics.CommandCompletion{ExitCode: code, Domain: automationRunAnalyticsDomain, DomainStatus: string(end)}
				return nil, "", &commandExitError{code: code, err: analytics.CompletedWithExitCode(fmt.Errorf("%s before run %s could be read; it keeps going: pick the wait back up with 'revyl automation run wait %s'", reason, runIDs[index], strings.Join(runIDs, " ")), completion)}
			}
		}
		return runs, end, nil
	}

	readsFailingSince := map[string]time.Time{}
	ticker := time.NewTicker(automationRunPollInterval)
	defer ticker.Stop()
	for {
		for index, runID := range runIDs {
			previous := runs[index]
			if previous != nil {
				if status := string(previous.RunStatus); status != automationRunStatusQueued && status != automationRunStatusRunning {
					continue
				}
			}
			readStarted := time.Now()
			next, err := client.GetAutomationRunOnce(readCtx, runID)
			if err != nil && readCtx.Err() != nil {
				return stopped()
			}
			if previous == nil && isAutomationResourceNotFound(err) {
				return nil, "", automationRunNotFound(runID)
			}
			if err != nil {
				if checkOnce || !proofRunReadMayRecover(err) {
					return nil, "", automationRequestError(cmd, automationRunAnalyticsDomain, "read run "+runID, err)
				}
				failingSince, failing := readsFailingSince[runID]
				if !failing {
					failingSince = readStarted
					readsFailingSince[runID] = failingSince
					fmt.Fprintf(progress, "Couldn't read run %s (%v); retrying for up to %s.\n", runID, err, automationRunPollFailureWindow)
				}
				if time.Since(failingSince) >= automationRunPollFailureWindow {
					return nil, "", fmt.Errorf("read run %s: %w; it keeps going: pick the wait back up with 'revyl automation run wait %s'", runID, err, strings.Join(runIDs, " "))
				}
				continue
			}
			delete(readsFailingSince, runID)
			if previous == nil || next.RunStatus != previous.RunStatus {
				fmt.Fprintf(progress, "Run %s of %s is %s.\n", next.Id, next.AutomationName, next.RunStatus)
			}
			runs[index] = next
		}

		pending := false
		for _, run := range runs {
			if run == nil {
				pending = true
				continue
			}
			switch string(run.RunStatus) {
			case automationRunStatusCompleted, automationRunStatusFailed, automationRunStatusCancelled:
			case automationRunStatusQueued, automationRunStatusRunning:
				pending = true
			default:
				return nil, "", fmt.Errorf("run %s has status %q, which this CLI doesn't know: run 'revyl upgrade'", run.Id, run.RunStatus)
			}
		}
		if !pending {
			return runs, automationRunWaitEnded, nil
		}
		if checkOnce {
			return runs, automationRunWaitTimedOut, nil
		}

		select {
		case <-waitCtx.Done():
			return stopped()
		case <-ticker.C:
		}
	}
}

// finishAutomationRunWait prints the runs and turns how the wait ended into
// the command's exit status. entityID is the run this command started, if any.
func finishAutomationRunWait(cmd *cobra.Command, runs []*api.AutomationRunDetailResponse, end automationRunWaitEnd, entityID string) error {
	if automationJSONOutput(cmd) {
		var output any = runs
		if entityID != "" {
			output = runs[0]
		}
		if err := writeAutomationJSON(cmd, output); err != nil {
			return err
		}
	} else {
		devMode, _ := cmd.Flags().GetBool("dev")
		for index, run := range runs {
			if index > 0 {
				fmt.Fprintln(cmd.OutOrStdout())
			}
			printAutomationRunSummary(cmd.OutOrStdout(), run, devMode)
		}
	}

	var runIDs, pendingIDs, failedIDs, cancelledIDs []string
	for _, run := range runs {
		runIDs = append(runIDs, run.Id.String())
		switch string(run.RunStatus) {
		case automationRunStatusFailed:
			failedIDs = append(failedIDs, run.Id.String())
		case automationRunStatusCancelled:
			cancelledIDs = append(cancelledIDs, run.Id.String())
		case automationRunStatusQueued, automationRunStatusRunning:
			pendingIDs = append(pendingIDs, run.Id.String())
		}
	}
	completion := analytics.CommandCompletion{Domain: automationRunAnalyticsDomain}
	if entityID != "" {
		completion.Properties = map[string]interface{}{"entity_id": entityID}
	}
	waitState := automationRunCount(len(pendingIDs)) + " still going"
	if len(failedIDs) > 0 {
		waitState += fmt.Sprintf(", %d failed", len(failedIDs))
	}
	if len(cancelledIDs) > 0 {
		waitState += fmt.Sprintf(", %d cancelled", len(cancelledIDs))
	}
	rewait := "pick the wait back up with 'revyl automation run wait " + strings.Join(runIDs, " ") + "'"

	switch {
	case end == automationRunWaitInterrupted:
		completion.ExitCode, completion.DomainStatus = automationRunExitInterrupted, "interrupted"
		err := analytics.CompletedWithExitCode(fmt.Errorf("stopped waiting with %s; %s", waitState, rewait), completion)
		return &commandExitError{code: automationRunExitInterrupted, err: err}
	case end == automationRunWaitTimedOut:
		completion.ExitCode, completion.DomainStatus = automationRunExitTimeout, "timed_out"
		err := analytics.CompletedWithExitCode(fmt.Errorf("--wait-timeout elapsed with %s; %s", waitState, rewait), completion)
		return &commandExitError{code: automationRunExitTimeout, err: err}
	case len(failedIDs) > 0:
		completion.ExitCode, completion.DomainStatus = 1, automationRunStatusFailed
		return analytics.CompletedWithExitCode(fmt.Errorf("%s failed: %s", automationRunCount(len(failedIDs)), strings.Join(failedIDs, ", ")), completion)
	case len(cancelledIDs) > 0:
		completion.ExitCode, completion.DomainStatus = 1, automationRunStatusCancelled
		return analytics.CompletedWithExitCode(fmt.Errorf("%s cancelled: %s", automationRunCount(len(cancelledIDs)), strings.Join(cancelledIDs, ", ")), completion)
	default:
		completion.DomainStatus = automationRunStatusCompleted
		analytics.SetCommandCompletion(cmd.Context(), completion)
		return nil
	}
}

func automationRunCount(count int) string {
	if count == 1 {
		return "1 run"
	}
	return strconv.Itoa(count) + " runs"
}

func runAutomationRunGet(cmd *cobra.Command, args []string) error {
	runID, err := parseAutomationRecordID("run", args[0])
	if err != nil {
		return err
	}
	client, err := automationClient(cmd)
	if err != nil {
		return err
	}
	run, err := client.GetAutomationRun(cmd.Context(), runID)
	if isAutomationResourceNotFound(err) {
		return automationRunNotFound(runID)
	}
	if err != nil {
		return automationRequestError(cmd, automationRunAnalyticsDomain, "read run", err)
	}
	if automationJSONOutput(cmd) {
		return writeAutomationJSON(cmd, run)
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	printAutomationRunSummary(cmd.OutOrStdout(), run, devMode)
	return nil
}

func runAutomationRunList(cmd *cobra.Command, _ []string) error {
	flags := cmd.Flags()
	limit, _ := flags.GetInt("limit")
	if limit < 1 || limit > automationListMaxLimit {
		return usageErrorf("--limit must be from 1 to %d", automationListMaxLimit)
	}
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	runStatuses, _ := flags.GetStringArray("run-status")
	for _, runStatus := range runStatuses {
		query.Add("run_status", strings.TrimSpace(runStatus))
	}
	triggerTypes, _ := flags.GetStringArray("trigger-type")
	for _, triggerType := range triggerTypes {
		query.Add("trigger_type", strings.TrimSpace(triggerType))
	}
	if flags.Changed("automation-trigger-id") {
		triggerID, err := parseAutomationRecordID("trigger", automationStringFlag(cmd, "automation-trigger-id"))
		if err != nil {
			return err
		}
		query.Set("automation_trigger_id", triggerID)
	}
	if flags.Changed("produced-test-execution-id") {
		reference := strings.TrimSpace(automationStringFlag(cmd, "produced-test-execution-id"))
		executionID, err := uuid.Parse(reference)
		if err != nil {
			return usageErrorf("--produced-test-execution-id %q isn't a test execution ID", reference)
		}
		query.Set("produced_test_execution_id", executionID.String())
	}
	if cursor := strings.TrimSpace(automationStringFlag(cmd, "cursor")); cursor != "" {
		query.Set("cursor", cursor)
	}

	client, err := automationClient(cmd)
	if err != nil {
		return err
	}
	if flags.Changed("automation") {
		automation, err := resolveAutomation(cmd, client, automationRunAnalyticsDomain, automationStringFlag(cmd, "automation"))
		if err != nil {
			return err
		}
		query.Set("automation_id", automation.Id.String())
	}
	page, err := client.ListAutomationRuns(cmd.Context(), query)
	if err != nil {
		return automationRequestError(cmd, automationRunAnalyticsDomain, "list runs", err)
	}
	if automationJSONOutput(cmd) {
		return writeAutomationJSON(cmd, page)
	}

	var items []api.AutomationRunListItemResponse
	if page.Items != nil {
		items = *page.Items
	}
	if len(items) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No runs match.")
		return nil
	}
	rows := make([][]string, 0, len(items))
	for _, run := range items {
		produced := "-"
		if run.ProducedTestExecutionId != nil {
			produced = "test execution " + run.ProducedTestExecutionId.String()
		}
		rows = append(rows, []string{run.Id.String(), run.AutomationName, string(run.RunStatus), string(run.TriggerType), formatAutomationTime(run.CreatedAt), produced})
	}
	printAutomationTable(cmd.OutOrStdout(), []string{"RUN", "AUTOMATION", "STATUS", "TRIGGER", "QUEUED", "PRODUCED"}, rows)
	if nextCursor := stringValue(page.NextCursor); nextCursor != "" {
		fmt.Fprintf(automationProgress(cmd), "\nMore runs: add --cursor '%s'\n", nextCursor)
	}
	return nil
}

func runAutomationRunCancel(cmd *cobra.Command, args []string) error {
	runID, err := parseAutomationRecordID("run", args[0])
	if err != nil {
		return err
	}
	client, err := automationClient(cmd)
	if err != nil {
		return err
	}
	run, err := client.CancelAutomationRun(cmd.Context(), runID)
	if isAutomationResourceNotFound(err) {
		return automationRunNotFound(runID)
	}
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict && strings.Contains(apiErr.Detail, "starting") {
		recordAutomationOutcome(cmd, automationRunAnalyticsDomain, "starting", "")
		return analytics.WithSafeDiagnostic(usageErrorf("run %s is starting its action right now, so it can't be cancelled yet: run 'revyl automation run cancel %s' again in a moment", runID, runID), "run starting")
	}
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
		recordAutomationOutcome(cmd, automationRunAnalyticsDomain, "already_ended", "")
		return analytics.WithSafeDiagnostic(usageErrorf("run %s already ended, so there's nothing to cancel: %s", runID, apiErr.Detail), "run already ended")
	}
	if err != nil {
		return automationRequestError(cmd, automationRunAnalyticsDomain, "cancel run", err)
	}
	recordAutomationOutcome(cmd, automationRunAnalyticsDomain, automationRunStatusCancelled, "")
	if automationJSONOutput(cmd) {
		return writeAutomationJSON(cmd, run)
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	fmt.Fprintf(cmd.OutOrStdout(), "Cancelled run %s, along with anything it started that was still running.\n\n", run.Id)
	printAutomationRunSummary(cmd.OutOrStdout(), run, devMode)
	return nil
}
