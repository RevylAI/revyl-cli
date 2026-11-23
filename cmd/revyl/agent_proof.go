package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/execution"
	"github.com/revyl/cli/internal/ui"
)

const (
	// agentProofPromptMaxBytes mirrors the backend's bound on the prompt, which
	// counts bytes of UTF-8 rather than characters.
	agentProofPromptMaxBytes = 256 * 1024

	// A healthy run can spend 15 minutes queued and 60 minutes in its sandbox,
	// so the default wait covers both with margin.
	defaultAgentProofTimeout      = 90 * time.Minute
	defaultAgentProofPollInterval = 5 * time.Second
	// How long consecutive failed status reads are tolerated before the wait
	// gives up; a backend blip must not end a long wait like a failed proof.
	defaultAgentProofPollFailureWindow = 5 * time.Minute

	agentProofAnalyticsDomain = "proof_run"

	// Outcomes are what happened from the command's point of view. They pick
	// the stderr summary and the exit code and are recorded for analytics;
	// the --json object carries the run's own status instead.
	agentProofOutcomePassed       = "passed"
	agentProofOutcomeFailed       = "failed"
	agentProofOutcomeInconclusive = "inconclusive"
	agentProofOutcomeCancelled    = "cancelled"
	agentProofOutcomeUnknown      = "unknown"
	agentProofOutcomeQueued       = "queued"
	agentProofOutcomeRunning      = "running"
	agentProofOutcomeTimeout      = "timeout"
	agentProofOutcomeInterrupted  = "interrupted"
	agentProofOutcomePollFailed   = "poll_failed"

	agentProofStatusUnsupported = "unsupported"
	agentProofStatusStartFailed = "start_failed"

	agentProofExitInconclusive = 2
	agentProofExitTimeout      = 124
	agentProofExitInterrupted  = 130
)

type agentProofAPI interface {
	appLookupAPI
	GetBuildVersionDetail(context.Context, string, bool) (*api.BuildVersionDetail, error)
	ResolveBuildSelectionMetadata(context.Context, string, api.BuildSelection) (*api.BuildVersionDetail, error)
	CreateProofRun(context.Context, *api.ProofRunCreateRequest) (*api.ProofRun, error)
	GetProofRun(context.Context, string) (*api.ProofRun, error)
	CancelProofRun(context.Context, string) (*api.ProofRun, error)
}

var (
	agentProofNewClient = func(apiKey string, devMode bool) agentProofAPI {
		return api.NewClientWithDevMode(apiKey, devMode)
	}
	agentProofPollInterval      = defaultAgentProofPollInterval
	agentProofPollFailureWindow = defaultAgentProofPollFailureWindow
	agentProofSignals           = func() (<-chan os.Signal, func()) {
		ch := make(chan os.Signal, 2)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		return ch, func() { signal.Stop(ch) }
	}
)

// agentProofOutput is the stable --json contract of `revyl agent proof` and of
// `revyl agent status` for a proof run. Every key is always present.
type agentProofOutput struct {
	Kind              string  `json:"kind"`
	RunID             string  `json:"run_id"`
	Status            string  `json:"status"`
	Success           bool    `json:"success"`
	VerificationScore *int    `json:"verification_score"`
	MessageMarkdown   *string `json:"message_markdown"`
	FailureKind       *string `json:"failure_kind"`
	FailureReason     *string `json:"failure_reason"`
	URL               *string `json:"url"`
	AppID             string  `json:"app_id"`
	BuildID           string  `json:"build_id"`
	Platform          *string `json:"platform"`
	CreatedAt         *string `json:"created_at"`
	CompletedAt       *string `json:"completed_at"`
}

type agentProofOptions struct {
	platform       string
	buildID        string
	branch         string
	buildSelection string
	timeout        time.Duration
	noWait         bool
}

const agentProofFailureKindHelp = `failure_kind says why a proof run did not pass:
  change_broken         the agent found the change broken; fix the change
  agent_blocked         the agent could not reach the change, for example at
                        a sign-in wall; give it a way through in the prompt,
                        or accept that this change cannot be proven this way
  no_write_up, change_not_exercised, not_started, infrastructure_error
                        nothing was proven about the change; run it again
  did_not_finish        the proof ran out of time or stopped before it
                        finished; run it again, with a narrower prompt if
                        it repeats
  superseded            a newer commit or build replaced this pull request
                        proof before it finished (status cancelled); read
                        the newer run
  cancelled             the run was cancelled`

func newAgentProofCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "proof [app-name|app-id]",
		Short: "Prove a change works on a build",
		Long: fmt.Sprintf(`Start a proof-of-changes run from a prompt and wait for its result.

Use this when Revyl is not connected to your repository, for example from your
own CI. The prompt is free-form text, usually the pull or merge request
description followed by its diff. Revyl proves the change on a cloud device
against the selected build.

The run has no repository checkout: the agent sees only the prompt text, so
put everything it needs to know about the change in the prompt. Session
settings from .revyl/config.yaml, such as auth bypass and the before-script,
are not applied to these runs.

Prompt: pass exactly one of --prompt or --prompt-file. --prompt-file - reads
standard input. The prompt can be at most %d bytes of UTF-8. --prompt is also
limited by your operating system's command-line size, so pass a diff with
--prompt-file.

App and build: name the app by name or ID as the argument. Omit it to use the
app in .revyl/config.yaml; use --platform when the project maps more than one
app. --build-id selects an exact build; with --build-id and no app, the
build's app is used. Otherwise the app's automatic build selection applies.

%s

Access: the API key needs the Member role or higher. A Viewer key cannot
start or read proof runs.

Waiting: the command waits until the run finishes or --timeout elapses. The
default, %s, covers a run's own limits: up to 15 minutes queued and up to 60
minutes running. --timeout only stops waiting; it never cancels the run.
Ctrl-C and SIGTERM also stop the command without cancelling the run, and once
the run has been started its ID is still printed. A status read that fails
with a network error, a 5xx or a 429 is retried for up to %s before the
command gives up and exits 1. Use --no-wait to return as soon as the run is
queued, then read it with 'revyl agent status <run-id>'.

Output: the proof message (Markdown, the same message a pull request shows)
is written to stdout. Progress, the result, the run ID and the run URL go to
stderr. With --json, stdout is one JSON object with the keys kind ("proof"),
run_id, status, success, verification_score, message_markdown, failure_kind,
failure_reason, url, app_id, build_id, platform, created_at and completed_at.
status is the run's status in Revyl: queued, running, passed, failed,
inconclusive or cancelled. success is true only when status is passed.

Result: verification_score (1 to 5) is set only when status is passed, failed
or inconclusive. A passed proof can still have a low score: the message marks
a pass that scored 2 or lower as partial coverage, so check the score if your
CI gates on the result.

%s

Exit codes:
  0    the proof passed, or the run was queued with --no-wait
  1    the run ended without passing (failed, cancelled, or a status this
       CLI does not know), the command lost contact with the run, or the
       request failed
  2    the run was inconclusive: nothing was proven about the change
  124  --timeout elapsed while the run was still in progress
  130  interrupted while the run was still in progress

Telling the causes of exit code 1 apart with --json: when the run ended,
status is its final status and failure_kind says why it did not pass. When
status is still queued or running, the command lost contact with a run that
has not ended; read it again with 'revyl agent status <run-id>'. When stdout
has no run object, no run was reported. With exit codes 124 and 130 status is
also still queued or running.`,
			agentProofPromptMaxBytes, runtimeBuildSelectionHelp,
			formatAgentProofDuration(defaultAgentProofTimeout), formatAgentProofDuration(defaultAgentProofPollFailureWindow),
			agentProofFailureKindHelp),
		Example: `  revyl agent proof --prompt-file prompt.md
  git diff origin/main...HEAD | revyl agent proof --prompt-file - --platform ios
  revyl agent proof "My App" --prompt "Verify the new onboarding copy" --build-id <build-id> --json
  revyl agent proof --prompt-file prompt.md --no-wait --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: runAgentProof,
	}
	command.Flags().String("prompt", "", "Prompt text for the proof; for a diff use --prompt-file, because the operating system limits argument size")
	command.Flags().String("prompt-file", "", fmt.Sprintf("Read the prompt from a file, or - for stdin (max %d bytes of UTF-8)", agentProofPromptMaxBytes))
	command.Flags().String("platform", "", "Configured platform mapping: ios, android, or web")
	command.Flags().String("build-id", "", "Prove an exact build ID (otherwise follows the selected branch's default build)")
	command.Flags().String("branch", "", "Revyl branch to prove (default: Git branch when enabled in app settings, else app default)")
	command.Flags().String("build-selection", "", "inherit (Automatic) or latest (ignore the branch pin); exact build: --build-id")
	command.Flags().Bool(allBranchesFlagName, false, allBranchesFlagUsage)
	command.Flags().Duration("timeout", defaultAgentProofTimeout, "Maximum time to wait locally; does not cancel the run")
	command.Flags().Bool("no-wait", false, "Return after the proof run is queued")
	return command
}

// formatAgentProofDuration renders whole minutes as "90m" rather than Go's "1h30m0s".
func formatAgentProofDuration(value time.Duration) string {
	return fmt.Sprintf("%dm", int(value/time.Minute))
}

func runAgentProof(cmd *cobra.Command, args []string) error {
	options, err := readAgentProofOptions(cmd)
	if err != nil {
		return err
	}
	prompt, err := readAgentProofPrompt(cmd)
	if err != nil {
		return err
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	quiet, _ := cmd.Flags().GetBool("quiet")
	client := agentProofNewClient(apiKey, devMode)

	app, request, err := resolveAgentProofTarget(cmd, client, args, options, quiet)
	if err != nil {
		return err
	}
	request.Prompt = prompt

	if !jsonOutput && !quiet {
		fmt.Fprintf(cmd.ErrOrStderr(), "Starting proof run for %s (build %s)...\n", app.Name, request.BuildId)
	}
	signals, stopSignals := agentProofSignals()
	defer stopSignals()
	run, err := client.CreateProofRun(cmd.Context(), request)
	if err != nil {
		return agentProofCreateError(cmd, err)
	}
	if !quiet {
		if runURL := stringValue(run.Url); runURL != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "Proof run %s started: %s\n", run.Id, runURL)
		} else {
			fmt.Fprintf(cmd.ErrOrStderr(), "Proof run %s started.\n", run.Id)
		}
	}

	interruptedDuringCreate := false
	select {
	case <-signals:
		interruptedDuringCreate = !isProofRunTerminal(run)
	default:
	}
	if interruptedDuringCreate {
		return finishAgentProof(cmd, run, agentProofOutcomeInterrupted, nil, jsonOutput, true)
	}
	if options.noWait {
		return finishAgentProof(cmd, run, proofRunOutcome(run), nil, jsonOutput, true)
	}

	waitCtx, cancel := context.WithTimeout(cmd.Context(), options.timeout)
	defer cancel()
	last, outcome, waitErr := waitForProofRun(waitCtx, client, run, signals, cmd.ErrOrStderr(), !quiet)
	return finishAgentProof(cmd, last, outcome, waitErr, jsonOutput, true)
}

func readAgentProofOptions(cmd *cobra.Command) (agentProofOptions, error) {
	var options agentProofOptions
	options.buildID, _ = cmd.Flags().GetString("build-id")
	options.branch = commandBuildBranch(cmd)
	options.timeout, _ = cmd.Flags().GetDuration("timeout")
	options.noWait, _ = cmd.Flags().GetBool("no-wait")
	platform, _ := cmd.Flags().GetString("platform")
	buildSelection, _ := cmd.Flags().GetString("build-selection")
	allBranches, _ := cmd.Flags().GetBool(allBranchesFlagName)

	options.buildID = strings.TrimSpace(options.buildID)
	if cmd.Flags().Changed("build-id") {
		if _, err := uuid.Parse(options.buildID); err != nil {
			return options, usageErrorf("--build-id requires a build ID (a UUID); list an app's builds with 'revyl build list --app <app>'")
		}
	}
	var err error
	options.buildSelection, err = resolveAllBranchesSelection(allBranchesFlagName, allBranches, "build-selection", buildSelection,
		allBranchesConflict{flag: "--branch", set: options.branch != ""},
		allBranchesConflict{flag: "--build-id", set: options.buildID != ""},
	)
	if err != nil {
		return options, err
	}
	if err := execution.ValidateRuntimeBuildSelection("", options.branch, options.buildSelection, options.buildID); err != nil {
		return options, analytics.WithFailureClass(err, analytics.FailureClassUsage)
	}
	options.platform, err = normalizeExplorePlatform(platform)
	if err != nil {
		return options, analytics.WithFailureClass(err, analytics.FailureClassUsage)
	}
	if options.timeout <= 0 {
		return options, usageErrorf("--timeout must be greater than zero")
	}
	return options, nil
}

// readAgentProofPrompt returns the prompt untrimmed, because a diff is
// whitespace-sensitive. It never reads more than one byte past the limit, and
// its errors never carry the prompt, its length, or the file path.
func readAgentProofPrompt(cmd *cobra.Command) (string, error) {
	inline := cmd.Flags().Changed("prompt")
	fromFile := cmd.Flags().Changed("prompt-file")
	if inline == fromFile {
		return "", usageErrorf("exactly one of --prompt or --prompt-file is required")
	}

	var text string
	if inline {
		text, _ = cmd.Flags().GetString("prompt")
	} else {
		path, _ := cmd.Flags().GetString("prompt-file")
		if strings.TrimSpace(path) == "" {
			return "", usageErrorf("--prompt-file requires a path, or - for stdin")
		}
		reader := cmd.InOrStdin()
		if path != "-" {
			// #nosec G304 -- the caller explicitly selects this local file with --prompt-file.
			file, err := os.Open(path)
			if err != nil {
				return "", agentProofPromptReadError(err)
			}
			defer file.Close()
			reader = file
		}
		data, err := io.ReadAll(io.LimitReader(reader, int64(agentProofPromptMaxBytes)+1))
		if err != nil {
			return "", agentProofPromptReadError(err)
		}
		text = string(data)
	}

	if len(text) > agentProofPromptMaxBytes {
		return "", agentProofPromptTooLargeError()
	}
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "\uFFFD")
		fmt.Fprintln(cmd.ErrOrStderr(), "Warning: the prompt contained bytes that are not valid UTF-8; each invalid sequence was replaced with U+FFFD.")
		if len(text) > agentProofPromptMaxBytes {
			return "", agentProofPromptTooLargeError()
		}
	}
	if strings.TrimSpace(text) == "" {
		return "", usageErrorf("prompt cannot be empty; describe the change with --prompt or --prompt-file")
	}
	if strings.ContainsRune(text, 0) {
		return "", usageErrorf("prompt contains a NUL character, which Revyl does not accept; remove NUL bytes (for example with: tr -d '\\000') and retry")
	}
	return text, nil
}

func agentProofPromptTooLargeError() error {
	return usageErrorf("prompt exceeds %d bytes of UTF-8; shorten it (for example, send a smaller diff) and retry", agentProofPromptMaxBytes)
}

func agentProofPromptReadError(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		err = pathErr.Err
	}
	return usageErrorf("read --prompt-file: %v; check that the file exists and is readable", err)
}

// resolveAgentProofTarget returns the create request without its prompt. The
// app comes from the argument when one is given, as it does for `revyl agent
// explore`, then from --build-id, then from the project configuration.
func resolveAgentProofTarget(
	cmd *cobra.Command,
	client agentProofAPI,
	args []string,
	options agentProofOptions,
	quiet bool,
) (*api.App, *api.ProofRunCreateRequest, error) {
	ctx := cmd.Context()

	var build *api.BuildVersionDetail
	var err error
	if options.buildID != "" {
		build, err = client.GetBuildVersionDetail(ctx, options.buildID, false)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve --build-id: %w", err)
		}
	}

	var app *api.App
	switch {
	case len(args) == 1:
		app, err = resolveExploreApp(ctx, client, args, options.platform)
		if err != nil {
			return nil, nil, analytics.WithSafeDiagnostic(err, "proof run app could not be resolved")
		}
	case build != nil && build.AppID != "":
		app, err = client.GetApp(ctx, build.AppID)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve the app of --build-id: %w", err)
		}
	default:
		app, err = resolveExploreApp(ctx, client, nil, options.platform)
		if err != nil {
			return nil, nil, fmt.Errorf("%w (for this command, name the app as the argument or pass --build-id <id>)", err)
		}
	}

	appPlatform, err := normalizeExplorePlatform(app.Platform)
	if err != nil {
		return nil, nil, fmt.Errorf("app %s has unsupported platform %q", app.ID, app.Platform)
	}
	if options.platform != "" && options.platform != appPlatform {
		return nil, nil, usageErrorf("--platform does not match the app's platform, which is %s", appPlatform)
	}

	if build != nil {
		if build.AppID != "" && build.AppID != app.ID {
			return nil, nil, usageErrorf("--build-id belongs to a different app than the one named; name the build's app or leave the app out")
		}
	} else {
		gitBranch := ""
		if api.BuildSelectionMode(options.buildSelection) != api.BuildSelectionModeLatestAllBranches {
			gitBranch, err = commandWorkingGitBranch(cmd)
			if err != nil {
				return nil, nil, fmt.Errorf("detect Git branch: %w", err)
			}
		}
		build, err = client.ResolveBuildSelectionMetadata(ctx, app.ID, api.BuildSelection{
			GitBranchName: gitBranch,
			BranchName:    options.branch,
			Mode:          api.BuildSelectionMode(options.buildSelection),
		})
		if err != nil {
			return nil, nil, fmt.Errorf("select a build for app %s: %w", app.ID, err)
		}
		if notice := ui.AppBranchSelectionNotice(app.Name, build.GitBranchName, build.BranchName, string(build.BranchSource), build.AutoGitBranchEnabled); notice != "" && !quiet {
			fmt.Fprintln(cmd.ErrOrStderr(), notice)
		}
	}

	appID, err := uuid.Parse(app.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("Revyl returned an app ID that is not a UUID: %w", err)
	}
	buildID, err := uuid.Parse(build.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("Revyl returned a build ID that is not a UUID: %w", err)
	}
	return app, &api.ProofRunCreateRequest{AppId: appID, BuildId: buildID}, nil
}

type proofRunPoll struct {
	run *api.ProofRun
	err error
}

// waitForProofRun polls until the run is final, the context ends, or a signal
// arrives. It always returns the last run it read, the outcome, and the cause
// when the wait stopped early. It never cancels the run. Transient read
// failures are retried on later ticks until they have lasted
// agentProofPollFailureWindow.
func waitForProofRun(
	ctx context.Context,
	client agentProofAPI,
	run *api.ProofRun,
	signals <-chan os.Signal,
	progressWriter io.Writer,
	showProgress bool,
) (*api.ProofRun, string, error) {
	ticker := time.NewTicker(agentProofPollInterval)
	defer ticker.Stop()

	runID := run.Id.String()
	printedStatus := ""
	var readsFailingSince time.Time
	for {
		if status := proofRunStatus(run); showProgress && status != printedStatus {
			fmt.Fprintf(progressWriter, "Proof run status: %s\n", status)
			printedStatus = status
		}
		if isProofRunTerminal(run) {
			return run, proofRunOutcome(run), nil
		}

		select {
		case <-ctx.Done():
			return run, agentProofStoppedOutcome(ctx), ctx.Err()
		case <-signals:
			return run, agentProofOutcomeInterrupted, nil
		case <-ticker.C:
		}

		pollCtx, cancelPoll := context.WithCancel(ctx)
		polled := make(chan proofRunPoll, 1)
		go func() {
			next, err := client.GetProofRun(pollCtx, runID)
			polled <- proofRunPoll{run: next, err: err}
		}()
		var result proofRunPoll
		select {
		case <-signals:
			cancelPoll()
			return run, agentProofOutcomeInterrupted, nil
		case result = <-polled:
			cancelPoll()
		}
		if result.err == nil {
			run = result.run
			readsFailingSince = time.Time{}
			continue
		}
		if ctx.Err() != nil {
			return run, agentProofStoppedOutcome(ctx), ctx.Err()
		}
		if !proofRunReadMayRecover(result.err) {
			return run, agentProofOutcomePollFailed, result.err
		}
		firstFailure := readsFailingSince.IsZero()
		if firstFailure {
			readsFailingSince = time.Now()
		}
		if time.Since(readsFailingSince) >= agentProofPollFailureWindow {
			return run, agentProofOutcomePollFailed, result.err
		}
		if firstFailure && showProgress {
			fmt.Fprintf(progressWriter, "Could not read the proof run (%v); retrying for up to %s.\n", result.err, agentProofPollFailureWindow)
		}
	}
}

// proofRunReadMayRecover reports whether a failed status read is worth
// repeating: transport errors, 5xx, and 429. Any other answer, such as 401,
// 403, or 404, will not change by waiting.
func proofRunReadMayRecover(err error) bool {
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		return true
	}
	return apiErr.StatusCode >= http.StatusInternalServerError || apiErr.StatusCode == http.StatusTooManyRequests
}

func agentProofStoppedOutcome(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return agentProofOutcomeTimeout
	}
	return agentProofOutcomeInterrupted
}

func proofRunStatus(run *api.ProofRun) string {
	return strings.ToLower(strings.TrimSpace(string(run.Status)))
}

func isProofRunTerminal(run *api.ProofRun) bool {
	if run.CompletedAt != nil {
		return true
	}
	switch api.ProofRunStatus(proofRunStatus(run)) {
	case api.ProofRunStatusPassed, api.ProofRunStatusFailed, api.ProofRunStatusInconclusive, api.ProofRunStatusCancelled:
		return true
	default:
		return false
	}
}

func proofRunOutcome(run *api.ProofRun) string {
	status := api.ProofRunStatus(proofRunStatus(run))
	if !isProofRunTerminal(run) {
		if status == api.ProofRunStatusQueued {
			return agentProofOutcomeQueued
		}
		return agentProofOutcomeRunning
	}
	switch status {
	case api.ProofRunStatusPassed:
		return agentProofOutcomePassed
	case api.ProofRunStatusFailed:
		return agentProofOutcomeFailed
	case api.ProofRunStatusInconclusive:
		return agentProofOutcomeInconclusive
	case api.ProofRunStatusCancelled:
		return agentProofOutcomeCancelled
	default:
		return agentProofOutcomeUnknown
	}
}

func agentProofOutputFromRun(run *api.ProofRun) agentProofOutput {
	output := agentProofOutput{
		Kind:              agentRunKindProof,
		RunID:             run.Id.String(),
		Status:            proofRunStatus(run),
		Success:           api.ProofRunStatus(proofRunStatus(run)) == api.ProofRunStatusPassed,
		VerificationScore: run.VerificationScore,
		MessageMarkdown:   run.MessageMarkdown,
		FailureReason:     run.FailureReason,
		URL:               run.Url,
		AppID:             run.AppId.String(),
		BuildID:           run.BuildId.String(),
		CreatedAt:         proofRunTimestamp(&run.CreatedAt),
		CompletedAt:       proofRunTimestamp(run.CompletedAt),
	}
	output.FailureKind = proofRunEnumText(run.FailureKind)
	output.Platform = proofRunEnumText(run.Platform)
	return output
}

func proofRunEnumText[Text ~string](value *Text) *string {
	if value == nil {
		return nil
	}
	text := string(*value)
	return &text
}

func proofRunTimestamp(value *time.Time) *string {
	if value == nil || value.IsZero() {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}

func finishAgentProof(
	cmd *cobra.Command,
	run *api.ProofRun,
	outcome string,
	cause error,
	jsonOutput bool,
	serverIssuedRunID bool,
) error {
	output := agentProofOutputFromRun(run)
	writeErr := writeAgentProofOutput(cmd, output, outcome, jsonOutput)
	if err := agentProofCompletionError(cmd, output, outcome, cause, serverIssuedRunID); err != nil {
		return err
	}
	return writeErr
}

// writeAgentProofOutput keeps stdout to the JSON object or the proof message.
// It writes with fmt rather than the ui package, whose output feeds failure
// analytics, because the message and failure reason are customer content.
func writeAgentProofOutput(cmd *cobra.Command, output agentProofOutput, outcome string, jsonOutput bool) error {
	var writeErr error
	if jsonOutput {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetEscapeHTML(false)
		writeErr = encoder.Encode(output)
	} else if message := stringValue(output.MessageMarkdown); message != "" {
		if !strings.HasSuffix(message, "\n") {
			message += "\n"
		}
		_, writeErr = fmt.Fprint(cmd.OutOrStdout(), message)
	}

	stderr := cmd.ErrOrStderr()
	inProgress := false
	switch outcome {
	case agentProofOutcomePassed:
		fmt.Fprintln(stderr, "Proof passed.")
	case agentProofOutcomeFailed:
		fmt.Fprintln(stderr, "Proof failed.")
	case agentProofOutcomeInconclusive:
		fmt.Fprintln(stderr, "Proof inconclusive.")
	case agentProofOutcomeCancelled:
		fmt.Fprintln(stderr, "Proof run cancelled.")
	case agentProofOutcomeQueued, agentProofOutcomeRunning:
		inProgress = true
		fmt.Fprintf(stderr, "Proof run %s.\n", output.Status)
	case agentProofOutcomeTimeout:
		fmt.Fprintf(stderr, "Stopped waiting: proof run is still %s.\n", output.Status)
	case agentProofOutcomeInterrupted:
		fmt.Fprintf(stderr, "Interrupted: proof run is still %s.\n", output.Status)
	case agentProofOutcomePollFailed:
		fmt.Fprintf(stderr, "Lost contact with the proof run before it ended; last known status %s.\n", output.Status)
	default:
		fmt.Fprintf(stderr, "Proof run ended with status %s.\n", output.Status)
	}
	if reason := stringValue(output.FailureReason); reason != "" {
		fmt.Fprintf(stderr, "Reason: %s\n", reason)
	}
	if advice := agentProofNextStepAdvice(output); advice != "" {
		fmt.Fprintln(stderr, advice)
	}
	fmt.Fprintf(stderr, "Run: %s\n", output.RunID)
	if runURL := stringValue(output.URL); runURL != "" {
		fmt.Fprintf(stderr, "URL: %s\n", runURL)
	}
	if inProgress {
		fmt.Fprintf(stderr, "Check it with: revyl agent status %s\n", output.RunID)
	}
	return writeErr
}

// agentProofCompletionError records the outcome for analytics and returns the
// error that sets the exit status. Only a server-issued run ID is recorded;
// one the caller typed is an argument value.
func agentProofCompletionError(cmd *cobra.Command, output agentProofOutput, outcome string, cause error, serverIssuedRunID bool) error {
	completion := analytics.CommandCompletion{Domain: agentProofAnalyticsDomain, DomainStatus: outcome}
	if serverIssuedRunID {
		completion.Properties = map[string]interface{}{"entity_id": output.RunID}
	}

	runURL := stringValue(output.URL)
	seeURL := ""
	if runURL != "" {
		seeURL = "; see " + runURL
	}
	checkLater := "the run was not cancelled. Check it with: revyl agent status " + output.RunID
	exitCode := 1
	var err error
	switch outcome {
	case agentProofOutcomePassed, agentProofOutcomeQueued, agentProofOutcomeRunning:
		analytics.SetCommandCompletion(cmd.Context(), completion)
		return nil
	case agentProofOutcomeFailed:
		err = fmt.Errorf("proof run %s failed%s", output.RunID, seeURL)
	case agentProofOutcomeInconclusive:
		exitCode = agentProofExitInconclusive
		err = fmt.Errorf("proof run %s was inconclusive%s", output.RunID, seeURL)
	case agentProofOutcomeCancelled:
		err = fmt.Errorf("proof run %s was cancelled", output.RunID)
	case agentProofOutcomeTimeout:
		exitCode = agentProofExitTimeout
		err = fmt.Errorf("--timeout elapsed while waiting for proof run %s; %s", output.RunID, checkLater)
	case agentProofOutcomeInterrupted:
		exitCode = agentProofExitInterrupted
		err = fmt.Errorf("interrupted with proof run %s still in progress; %s", output.RunID, checkLater)
	case agentProofOutcomePollFailed:
		err = fmt.Errorf("could not read proof run %s: %v; %s", output.RunID, cause, checkLater)
	default:
		seeOrUpgrade := "run 'revyl upgrade'"
		if runURL != "" {
			seeOrUpgrade += " or see " + runURL
		}
		err = fmt.Errorf("proof run %s ended with status %q, which this CLI version does not recognize; %s", output.RunID, output.Status, seeOrUpgrade)
	}

	completion.ExitCode = exitCode
	completed := analytics.CompletedWithExitCode(&agentProofOutcomeError{err: err, notARevylBug: agentProofOutcomeIsNotARevylBug(output, outcome)}, completion)
	if exitCode != 1 {
		return &commandExitError{code: exitCode, err: completed}
	}
	return completed
}

// agentProofOutcomeError marks a run that ended without passing, so the agent
// feedback hint can tell an outcome that is no Revyl bug from one Revyl may
// have caused.
type agentProofOutcomeError struct {
	err          error
	notARevylBug bool
}

func (e *agentProofOutcomeError) Error() string { return e.err.Error() }
func (e *agentProofOutcomeError) Unwrap() error { return e.err }

// agentProofOutcomeIsNotARevylBug reports whether the run ended for a reason
// that gives no cause to report a Revyl bug: a broken change, a blocked agent,
// a cancellation, a run a newer commit or build replaced, or a run that did
// not finish. did_not_finish covers both an agent that ran out of time on a
// broad prompt and a run Revyl stopped, and the response does not say which. A backend that predates failure_kind sends
// none, and its failed and cancelled statuses carry the same meaning.
func agentProofOutcomeIsNotARevylBug(output agentProofOutput, outcome string) bool {
	switch api.ProofRunFailureKind(stringValue(output.FailureKind)) {
	case api.ProofRunFailureKindChangeBroken, api.ProofRunFailureKindAgentBlocked, api.ProofRunFailureKindCancelled,
		api.ProofRunFailureKindSuperseded, api.ProofRunFailureKindDidNotFinish:
		return true
	case "":
		switch outcome {
		case agentProofOutcomeFailed, agentProofOutcomeCancelled, agentProofOutcomeTimeout, agentProofOutcomeInterrupted:
			return true
		}
		return false
	default:
		return false
	}
}

// agentProofNextStepAdvice names what to do after a run that proved nothing.
// It never says how to start the proof again, because a run read by ID may
// have been started by a pull request rather than by this CLI.
func agentProofNextStepAdvice(output agentProofOutput) string {
	switch api.ProofRunFailureKind(stringValue(output.FailureKind)) {
	case api.ProofRunFailureKindInfrastructureError, api.ProofRunFailureKindNotStarted:
		return "Revyl did not reach a verdict on your change because of a problem on its side, not because of the change. Start the proof again to retry."
	case api.ProofRunFailureKindDidNotFinish:
		return "The proof ran out of time or stopped before it finished, so nothing was proven about your change. Start the proof again; if it happens again, narrow the prompt."
	case api.ProofRunFailureKindSuperseded:
		return "This run will not finish. Read the proof run of the newer commit or build instead."
	default:
		return ""
	}
}

// agentProofCreateError explains why the command has no run to report. The
// backend commits a run before it renders the response, so a lost or failed
// response does not prove that nothing started.
func agentProofCreateError(cmd *cobra.Command, err error) error {
	const maybeStarted = "Revyl may have started the run anyway: the request was sent once and not retried, because a retry could start a second run. Before running this command again, look for a new manual proof run under Cloud Agents > Proof of Changes in Revyl"
	status := agentProofStatusStartFailed
	var apiErr *api.APIError
	switch {
	case !errors.As(err, &apiErr) || apiErr.StatusCode >= http.StatusInternalServerError:
		err = fmt.Errorf("start proof run: %w. %s", err, maybeStarted)
	case proofRunCreateRouteMissing(apiErr):
		status = agentProofStatusUnsupported
		err = usageErrorf("start proof run: %s (POST /api/v1/scm/proof-runs returned %d)", agentProofUnsupportedServerMessage, apiErr.StatusCode)
	case apiErr.StatusCode != http.StatusNotFound:
		err = fmt.Errorf("start proof run: %w", err)
	default:
		err = fmt.Errorf("start proof run: %w (HTTP 404). Check that the app argument and --build-id, or the app in .revyl/config.yaml, name an app and a build in the organization your API key belongs to; list them with 'revyl app list' and 'revyl build list --app <app>'", err)
	}
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{Domain: agentProofAnalyticsDomain, DomainStatus: status})
	return analytics.WithSafeDiagnostic(err, "proof run start failed")
}

const agentProofUnsupportedServerMessage = "this Revyl server does not support proof runs"

// proofRunCreateRouteMissing reports whether the refusal came from a server
// that predates the route that starts a proof run. Such a server already lists
// proof runs at the same path, so it answers the POST with 405; one with no
// route there answers 404 with the framework's generic body, or no body. Every
// other 404 names a missing resource in its detail.
func proofRunCreateRouteMissing(apiErr *api.APIError) bool {
	switch apiErr.StatusCode {
	case http.StatusMethodNotAllowed:
		return true
	case http.StatusNotFound:
		detail := strings.TrimSpace(apiErr.Detail)
		return detail == "" || strings.EqualFold(detail, "Not Found")
	default:
		return false
	}
}
