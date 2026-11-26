package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/sessionprofile"
	"github.com/revyl/cli/internal/ui"
)

const computerStopReleaseWait = time.Minute

const computerSessionProfileFlagUsage = "Session profile from .revyl/config.yaml to run on the Mac before you use it (waits until it finishes); default: the checkout's session.default_profile; none for no profile"

// computerSessionProfileWait is how long create waits by default for a
// computer to start and its session profile to finish.
const computerSessionProfileWait = 10 * time.Minute

const computerSessionProfileUnsupportedHint = "This Revyl backend doesn't support session profiles on computers yet; start the computer without --session-profile."

type computerInvocation struct {
	ComputerSessionID  string `json:"computer_session_id,omitempty"`
	RequestID          string `json:"request_id,omitempty"`
	OperationID        string `json:"operation_id,omitempty"`
	Status             string `json:"status,omitempty"`
	Error              string `json:"error,omitempty"`
	BillingURL         string `json:"billing_url,omitempty"`
	BillingAction      string `json:"billing_action,omitempty"`
	NextStep           string `json:"next_step,omitempty"`
	ErrorCode          string `json:"error_code,omitempty"`
	waited             bool
	createdDisplay     *api.ComputerDisplaySize
	sessionProfile     string
	sessionProfilePick sessionprofile.PickSource
	sessionScriptSent  bool
	execOptions        bool
}

type computerPerformanceResult struct {
	Page      *api.ComputerPerformancePage
	Watch     bool
	EndReason string
}

func newComputerCommand() *cobra.Command {
	command := &cobra.Command{
		Use: "computer", Short: "Start, use and stop cloud computers",
		Long: `A computer is a cloud Mac you start, drive and stop. Run revyl computer
with no arguments to see your running computers.

  revyl computer create --wait              start a Mac and wait until it is active
  revyl computer list                       your organization's newest computers
  revyl computer get ID                     its state and latest steps
  revyl computer stop ID                    stop it

Drive it (x and y are screenshot pixels):
  revyl computer screenshot ID --out s.png
  revyl computer click ID --target "the Save button" --out s.png   act, then save the screen, in one call
  revyl computer type ID --text hello
  revyl computer key ID --key cmd+space
  revyl computer exec ID -- uname -a        run a program on it

Also: extend, hosting, sessions, report, export, files, process; each has --help.
--json prints machine-readable output; a failure prints error and, when Revyl named one, error_code.

macOS computers come with Pro: revyl billing add-card unlocks them, and free minutes still cover macOS.
Agents running on the Mac itself read /opt/revyl/computer-guide.md there.`,
		RunE: runComputerOverview,
	}
	command.PersistentFlags().Bool("json", false, "Output JSON; binary fields use URL-safe base64")
	command.SetFlagErrorFunc(computerFlagError)
	command.SetUsageTemplate(strings.Replace(command.UsageTemplate(), "Global Flags:\n{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}", "Global flags: --json (machine-readable output), --intent, -q/--quiet, --debug, -C/--chdir", 1))
	create := &cobra.Command{Use: "create", Short: "Start a computer"}
	create.Flags().String("os", "macos", "Computer OS")
	create.Flags().String("id", "", "Proposed computer UUID (generated once when omitted)")
	create.Flags().String("display", "", "Display size in pixels as WIDTHxHEIGHT, fixed at boot: even width 800-1920 and even height 600-1080 (default 1280x800)")
	create.Flags().Duration("timeout", 0, "Lifetime from creation (the API's timeout_seconds), whole seconds from 1m to 24h (default 2h)")
	create.Flags().Bool("wait", false, "Wait until the computer is active; exits non-zero if it ends or is still starting at --wait-timeout")
	create.Flags().Duration("wait-timeout", 2*time.Minute, "How long --wait waits, counted from the create request (1s-30m); 10m by default with --session-profile")
	create.Flags().String("session-profile", "", computerSessionProfileFlagUsage)
	create.Flags().Bool(fromCheckoutFlagName, true, "Send the session profile's definition and script from this checkout, uncommitted changes included; false lets Revyl read them, and the project's default profile, from Git")
	create.Flags().String("branch", "", "Git branch the session profile reads from Git (default: this checkout's Git branch, else the repository's default branch)")
	create.Flags().Bool("persistent", false, "Keep the whole disk when it stops or reaches its lifetime end, so it can start again under the same ID (macOS only)")
	_ = create.Flags().MarkHidden("persistent")
	create.Flags().String("from-snapshot", "", "Create a persistent macOS computer whose disk starts as this snapshot's (ID or name); it does not take the snapshot's computer's --inference, so pass --inference again for that")
	_ = create.Flags().MarkHidden("from-snapshot")
	create.Flags().Bool("inference", false, "Place the computer on a host with its own vision model loaded, so instruction, validation and extract run on it (macOS only)")
	create.RunE = func(cmd *cobra.Command, args []string) error {
		return runComputerCommand(cmd, func(invocation *computerInvocation) (any, error) {
			if len(args) != 0 {
				return nil, errors.New("computer create does not accept positional arguments")
			}
			osName, _ := cmd.Flags().GetString("os")
			fromSnapshot, _ := cmd.Flags().GetString("from-snapshot")
			if cmd.Flags().Changed("from-snapshot") {
				if !api.ValidComputerSnapshotReference(fromSnapshot) {
					return nil, errors.New("--from-snapshot requires a snapshot ID or name")
				}
				if osName != "macos" {
					return nil, errors.New("--from-snapshot requires --os macos")
				}
			}
			if osName != "macos" && osName != "linux" {
				return nil, errors.New("--os must be macos")
			}
			persistent, _ := cmd.Flags().GetBool("persistent")
			if persistent && osName != "macos" {
				return nil, errors.New("--persistent requires --os macos")
			}
			inference, _ := cmd.Flags().GetBool("inference")
			if inference && osName != "macos" {
				return nil, errors.New("--inference requires --os macos")
			}
			explicitWait, _ := cmd.Flags().GetBool("wait")
			waitTimeout, _ := cmd.Flags().GetDuration("wait-timeout")
			profile, err := computerSessionProfileRequest(cmd, osName)
			if err != nil {
				return nil, err
			}
			runsProfile := profile.Runs()
			if runsProfile && !cmd.Flags().Changed("wait-timeout") {
				waitTimeout = computerSessionProfileWait
			}
			wait := explicitWait || runsProfile
			if cmd.Flags().Changed("wait-timeout") && !wait {
				return nil, errors.New("--wait-timeout requires --wait")
			}
			if waitTimeout < time.Second || waitTimeout > 30*time.Minute {
				return nil, errors.New("--wait-timeout must be from 1s to 30m")
			}
			displayText, _ := cmd.Flags().GetString("display")
			display, err := computerDisplaySize(displayText)
			if err != nil {
				return nil, err
			}
			var timeoutSeconds *int
			if cmd.Flags().Changed("timeout") {
				if explicitWait && !cmd.Flags().Changed("wait-timeout") {
					return nil, errors.New("--timeout now sets the computer's lifetime, not how long --wait waits; pass --wait-timeout to bound --wait, alongside --timeout only if you meant a lifetime")
				}
				lifetime, _ := cmd.Flags().GetDuration("timeout")
				seconds, err := computerLifetimeSeconds(lifetime)
				if err != nil {
					return nil, err
				}
				timeoutSeconds = &seconds
			}
			idText, _ := cmd.Flags().GetString("id")
			id, err := computerUUIDOrNew(idText)
			if err != nil {
				return nil, err
			}
			request := api.ComputerRunCreate{ComputerSessionId: id, Os: api.ComputerRunCreateOs(osName), Display: display, TimeoutSeconds: timeoutSeconds}
			if profile.Instruction != nil {
				request.SessionProfile = &api.SessionProfileInstruction{Instruction: profile.Instruction.Instruction}
				if profile.Instruction.Name != "" {
					request.SessionProfile.Name = &profile.Instruction.Name
				}
			}
			if persistent {
				request.Persistent = &persistent
			}
			if fromSnapshot != "" {
				request.FromSnapshot = &fromSnapshot
			}
			if inference {
				request.Inference = &inference
			}
			request.SessionProfileSource, request.SessionProfileFiles = profile.Source, profile.Files
			if runsProfile {
				invocation.sessionProfilePick, invocation.sessionProfile, invocation.sessionScriptSent = profile.Pick, profile.Name(), profile.SendsScript()
			}
			invocation.ComputerSessionID = id.String()
			invocation.createdDisplay = display
			client, err := computerClient(cmd)
			if err != nil {
				return nil, err
			}
			if err := profile.RequireHostedRun(cmd.Context(), client); err != nil {
				return nil, err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "computer_session_id=%s\n", id)
			ctx := cmd.Context()
			if wait {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, waitTimeout)
				defer cancel()
			}
			report, err := client.CreateComputerRun(ctx, request)
			var refusal *api.APIError
			if request.SessionProfile != nil && errors.As(err, &refusal) && refusal.Code == "invalid_request" {
				refusal.Hint = computerSessionProfileUnsupportedHint
			}
			if jsonOutput, _ := cmd.Flags().GetBool("json"); !jsonOutput && errors.As(err, &refusal) {
				switch refusal.Code {
				case "computer_limit":
					refusal.Message = computerLimitMessage
				case "computer_long_limit":
					refusal.Message = computerLongLimitMessage
				case "computer_not_enabled":
					refusal.Message = computerNotEnabledMessage(osName, refusal)
				case "display_unsupported":
					refusal.Message = computerDisplayUnsupportedMessage
				}
			}
			if err == nil && report.SessionProfile != nil && report.SessionProfile.MissingGitBranchName != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Git has no branch %q, so session profile %s reads the repository's default branch instead\n", *report.SessionProfile.MissingGitBranchName, report.SessionProfile.Name)
			}
			if err != nil || !wait {
				return report, err
			}
			invocation.waited = true
			if api.InspectComputerReport(*report).State == "starting" {
				fmt.Fprintln(cmd.ErrOrStderr(), "waiting for the computer to become active")
			}
			if report.SessionProfile != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "waiting for session profile %s to finish on the computer\n", report.SessionProfile.Name)
			}
			report, err = client.AwaitComputerRunStarted(ctx, report)
			if err == nil && cmd.Context().Err() != nil {
				return nil, cmd.Context().Err()
			}
			return report, err
		})
	}
	command.AddCommand(create)
	listComputers := &cobra.Command{Use: "list", Short: "Show your organization's newest computers"}
	listComputers.Flags().Int("limit", 25, fmt.Sprintf("How many computers to show (1-%d)", api.ComputerListMaxLimit))
	listComputers.Flags().Bool("persistent", false, "Show persistent computers, stopped ones included")
	_ = listComputers.Flags().MarkHidden("persistent")
	listComputers.RunE = func(cmd *cobra.Command, args []string) error {
		return runComputerCommand(cmd, func(*computerInvocation) (any, error) {
			if len(args) != 0 {
				return nil, errors.New("computer list does not accept positional arguments")
			}
			limit, _ := cmd.Flags().GetInt("limit")
			client, err := computerClient(cmd)
			if err != nil {
				return nil, err
			}
			if persistent, _ := cmd.Flags().GetBool("persistent"); persistent {
				return client.ListPersistentComputers(cmd.Context(), limit)
			}
			return client.ListComputerRuns(cmd.Context(), limit)
		})
	}
	command.AddCommand(listComputers)
	start := &cobra.Command{Use: "start C1", Hidden: true, Short: "Boot a stopped persistent computer from its saved disk", Long: "Boot a stopped persistent computer from its saved disk as a new run.\n\n" + computerPersistentHelp}
	start.Flags().String("display", "", "Display size in pixels as WIDTHxHEIGHT, fixed at boot: even width 800-1920 and even height 600-1080 (default 1280x800)")
	start.Flags().Duration("timeout", 0, "Lifetime of the new run from the start (the API's timeout_seconds), whole seconds from 1m to 24h (default 2h)")
	start.Flags().Bool("no-inference", false, "Start this run without a host model even though the computer was created with --inference, when no host with one is up; later starts ask for it again")
	start.Flags().Bool("wait", false, "Wait until the computer is running; exits non-zero if it is not by --wait-timeout")
	start.Flags().Duration("wait-timeout", 10*time.Minute, "How long --wait waits (1s-30m)")
	start.RunE = func(cmd *cobra.Command, args []string) error {
		return runComputerCommand(cmd, func(invocation *computerInvocation) (any, error) {
			if len(args) != 1 {
				return nil, errors.New("computer start requires exactly one computer UUID")
			}
			id, err := parseComputerUUID(args[0])
			if err != nil {
				return nil, err
			}
			invocation.ComputerSessionID = id.String()
			wait, timeout, err := computerWaitFlags(cmd)
			if err != nil {
				return nil, err
			}
			displayText, _ := cmd.Flags().GetString("display")
			display, err := computerDisplaySize(displayText)
			if err != nil {
				return nil, err
			}
			request := api.PersistentComputerStartRequest{Display: display}
			request.WithoutInference, _ = cmd.Flags().GetBool("no-inference")
			if cmd.Flags().Changed("timeout") {
				lifetime, _ := cmd.Flags().GetDuration("timeout")
				seconds, err := computerLifetimeSeconds(lifetime)
				if err != nil {
					return nil, err
				}
				request.TimeoutSeconds = &seconds
			}
			client, err := computerClient(cmd)
			if err != nil {
				return nil, err
			}
			ctx := cmd.Context()
			if wait {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}
			status, err := client.StartComputer(ctx, id, request)
			if err != nil || !wait {
				return status, err
			}
			return awaitPersistentComputer(ctx, cmd, client, invocation, status, api.PersistentComputerStatusStateStarting, "waiting for the computer to become active")
		})
	}
	command.AddCommand(start)
	deleteComputer := &cobra.Command{Use: "delete C1", Hidden: true, Short: "Delete a persistent computer and its saved disk", Long: "Delete a persistent computer and its saved disk for good. A running computer\nstops first, without saving. Its snapshots stay: each keeps the save it names\nuntil you delete the snapshot.\n\n" + computerPersistentHelp}
	deleteComputer.Flags().Bool("yes", false, "Delete without asking; required when not run in a terminal")
	deleteComputer.RunE = func(cmd *cobra.Command, args []string) error {
		return runComputerCommand(cmd, func(invocation *computerInvocation) (any, error) {
			if len(args) != 1 {
				return nil, errors.New("computer delete requires exactly one computer UUID")
			}
			id, err := parseComputerUUID(args[0])
			if err != nil {
				return nil, err
			}
			invocation.ComputerSessionID = id.String()
			if yes, _ := cmd.Flags().GetBool("yes"); !yes {
				if !ui.IsInputTTY() {
					return nil, errors.New("computer delete removes the saved disk for good; pass --yes to confirm")
				}
				confirmed, err := ui.PromptConfirm(fmt.Sprintf("Delete computer %s and its saved disk? This can't be undone.", id), false)
				if err != nil || !confirmed {
					return nil, errors.New("computer delete canceled; nothing was deleted")
				}
			}
			client, err := computerClient(cmd)
			if err != nil {
				return nil, err
			}
			if err := client.DeleteComputer(cmd.Context(), id); err != nil {
				return nil, err
			}
			return &computerDeleteResult{ComputerID: id.String(), State: "deleted"}, nil
		})
	}
	command.AddCommand(deleteComputer)
	command.AddCommand(newComputerSnapshotCommand())
	extend := &cobra.Command{Use: "extend C1", Short: "Keep a running computer alive longer"}
	extend.Flags().Duration("timeout", 0, "Keep it running at least this long from now (the API's timeout_seconds), whole seconds from 1m to 24h; never past 24h from creation")
	extend.RunE = func(cmd *cobra.Command, args []string) error {
		return runComputerCommand(cmd, func(invocation *computerInvocation) (any, error) {
			if len(args) != 1 {
				return nil, errors.New("computer command requires exactly one computer UUID")
			}
			id, err := parseComputerUUID(args[0])
			if err != nil {
				return nil, err
			}
			invocation.ComputerSessionID = id.String()
			lifetime, _ := cmd.Flags().GetDuration("timeout")
			seconds, err := computerLifetimeSeconds(lifetime)
			if err != nil {
				return nil, err
			}
			client, err := computerClient(cmd)
			if err != nil {
				return nil, err
			}
			report, err := client.ExtendComputerRun(cmd.Context(), id, seconds)
			var refusal *api.APIError
			if jsonOutput, _ := cmd.Flags().GetBool("json"); !jsonOutput && errors.As(err, &refusal) {
				switch refusal.Code {
				case "computer_long_limit":
					refusal.Message = computerLongLimitMessage
				case "extend_unsupported":
					refusal.Message = computerExtendUnsupportedMessage
				}
			}
			return report, err
		})
	}
	command.AddCommand(extend)
	leafShorts := map[string]string{
		"get": "Show a computer's state and steps", "stop": "Stop a computer", "report": "Print a computer report (JSON, ATIF or performance)",
		"hosting": "Show which app-session platforms a running computer can host", "sessions": "List the app sessions hosted inside a computer",
	}
	for _, action := range []string{"get", "stop", "report", "hosting", "sessions"} {
		leaf := &cobra.Command{Use: action + " C1", Short: leafShorts[action]}
		if action == "get" {
			leaf.Flags().Bool("full", false, fmt.Sprintf("Show every step and whole commands, not the last %d steps with commands cut at %d characters", computerDetailStepLimit, computerDetailCommandRunes))
		}
		if action == "sessions" {
			leaf.Flags().Int("limit", 25, fmt.Sprintf("How many sessions to show (1-%d)", api.ComputerListMaxLimit))
		}
		if action == "stop" {
			leaf.Flags().Bool("wait", false, "On a persistent computer, wait until its disk is saved; exits non-zero if it is not stopped by --wait-timeout")
			leaf.Flags().Duration("wait-timeout", 10*time.Minute, "How long --wait waits for the disk to be saved (1s-30m)")
			_ = leaf.Flags().MarkHidden("wait")
			_ = leaf.Flags().MarkHidden("wait-timeout")
		}
		if action == "report" {
			leaf.Long = `Read a computer report as JSON, ATIF or performance statistics.

Performance prints whole-run latest/min/average/max statistics. Add --watch to
poll new samples; --json prints one API page per line while watching.
Live samples require host release r3.36 or later.

Watch exits 0 after reading the ended computer's saved report, or after a 10s
grace when no performance was saved. Timeout exits 124; Ctrl-C exits 130.
--watch, --interval and --timeout require --format performance; --interval and
--timeout also require --watch.`
			leaf.Flags().String("format", "json", "Report format: json, atif or performance")
			leaf.Flags().Bool("watch", false, "Poll performance samples until the computer ends")
			leaf.Flags().Duration("interval", 5*time.Second, "Performance poll interval (2s-60s)")
			leaf.Flags().Duration("timeout", 10*time.Minute, "Performance watch timeout (1s-2h)")
		}
		leaf.RunE = func(cmd *cobra.Command, args []string) error {
			return runComputerCommand(cmd, func(invocation *computerInvocation) (any, error) {
				if len(args) != 1 {
					return nil, errors.New("computer command requires exactly one computer UUID")
				}
				id, err := parseComputerUUID(args[0])
				if err != nil {
					return nil, err
				}
				invocation.ComputerSessionID = id.String()
				if action == "report" {
					if err := validateComputerReportFlags(cmd); err != nil {
						return nil, err
					}
				}
				client, err := computerClient(cmd)
				if err != nil {
					return nil, err
				}
				switch action {
				case "get":
					return client.GetComputerRun(cmd.Context(), id)
				case "hosting":
					return client.GetComputerHosting(cmd.Context(), id)
				case "sessions":
					limit, _ := cmd.Flags().GetInt("limit")
					return client.ListComputerSessions(cmd.Context(), id, limit)
				case "stop":
					wait, timeout, err := computerWaitFlags(cmd)
					if err != nil {
						return nil, err
					}
					report, persistent, err := client.StopComputerRun(cmd.Context(), id)
					if err == nil && persistent != nil {
						if !wait {
							return persistent, nil
						}
						ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
						defer cancel()
						return awaitPersistentComputer(ctx, cmd, client, invocation, persistent, api.PersistentComputerStatusStateStopping, "waiting for the computer's disk to be saved")
					}
					if err != nil || !api.ComputerRunUnsettled(api.InspectComputerReport(*report)) {
						return report, err
					}
					fmt.Fprintln(cmd.ErrOrStderr(), "waiting for the computer's release to be confirmed")
					ctx, cancel := context.WithTimeout(cmd.Context(), computerStopReleaseWait)
					defer cancel()
					report, err = client.AwaitComputerRunSettled(ctx, report)
					if err == nil && cmd.Context().Err() != nil {
						return nil, cmd.Context().Err()
					}
					return report, err
				default:
					format, _ := cmd.Flags().GetString("format")
					watch, _ := cmd.Flags().GetBool("watch")
					interval, _ := cmd.Flags().GetDuration("interval")
					timeout, _ := cmd.Flags().GetDuration("timeout")
					if format == "performance" {
						return readComputerPerformance(cmd, client, id, watch, interval, timeout)
					}
					if format == "atif" {
						return client.GetComputerATIF(cmd.Context(), id)
					}
					return client.GetComputerReport(cmd.Context(), id)
				}
			})
		}
		command.AddCommand(leaf)
	}
	export := &cobra.Command{Use: "export C1", Short: "Download a computer as a Harbor trial bundle"}
	export.Flags().String("format", "harbor", "Export format: harbor")
	export.Flags().String("out", "", "Destination file (default ./revyl-computer-<id>.zip)")
	export.RunE = func(cmd *cobra.Command, args []string) error {
		return runComputerCommand(cmd, func(invocation *computerInvocation) (any, error) {
			if len(args) != 1 {
				return nil, errors.New("computer export requires exactly one computer UUID")
			}
			id, err := parseComputerUUID(args[0])
			if err != nil {
				return nil, err
			}
			invocation.ComputerSessionID = id.String()
			format, _ := cmd.Flags().GetString("format")
			if format != "harbor" {
				return nil, errors.New("export format must be harbor")
			}
			path, _ := cmd.Flags().GetString("out")
			if path == "" {
				path = "./revyl-computer-" + id.String() + ".zip"
			}
			client, err := computerClient(cmd)
			if err != nil {
				return nil, err
			}
			return saveComputerHarbor(cmd.Context(), client, id, path)
		})
	}
	command.AddCommand(export)
	operation := &cobra.Command{Use: "operation", Short: "Inspect an existing operation without dispatching", RunE: computerHelp}
	getOperation := &cobra.Command{Use: "get C1 O1", Short: "Get the retained result or explicit pending state"}
	getOperation.RunE = func(cmd *cobra.Command, args []string) error {
		return runComputerCommand(cmd, func(invocation *computerInvocation) (any, error) {
			if len(args) != 2 {
				return nil, errors.New("operation get requires computer and operation UUIDs")
			}
			id, err := parseComputerUUID(args[0])
			if err != nil {
				return nil, err
			}
			operationID, err := parseComputerUUID(args[1])
			if err != nil {
				return nil, err
			}
			invocation.ComputerSessionID, invocation.OperationID = id.String(), operationID.String()
			client, err := computerClient(cmd)
			if err != nil {
				return nil, err
			}
			result, err := client.GetComputerOperation(cmd.Context(), id, operationID)
			if err != nil {
				return nil, err
			}
			if result == nil {
				invocation.Status = "pending"
				return invocation, nil
			}
			return result, nil
		})
	}
	operation.AddCommand(getOperation)
	command.AddCommand(operation)
	exec := newComputerOperationCommand("exec C1 -- PROGRAM [ARG...]", "Execute structured argv inside the selected computer", "exec")
	exec.Flags().String("stdin", "", "Read bounded stdin from a local file or -")
	exec.Flags().String("cwd", "", "Working directory: absolute, or relative to /Users/computer/workspace (default)")
	exec.Flags().StringArray("env", nil, "Set NAME=VALUE in the command's environment, or NAME to copy a local variable without printing it (repeatable)")
	exec.Flags().Lookup("timeout").Usage = "Command timeout (1ms-60m); timeout does not imply rollback"
	command.AddCommand(exec)
	files := &cobra.Command{Use: "files", Short: "Read, write or stat bounded guest-relative files", RunE: computerHelp}
	read := newComputerOperationCommand("read C1 PATH", "Read bounded file bytes", "file_read")
	read.Flags().Int("max-bytes", api.ComputerMaxOutputBytes, fmt.Sprintf("Maximum bytes returned (1-%d)", api.ComputerMaxOutputBytes))
	write := newComputerOperationCommand("write C1 PATH", "Write bounded file bytes", "file_write")
	write.Flags().String("from", "", "Read local input from a file or - (required)")
	files.AddCommand(read, write, newComputerOperationCommand("stat C1 PATH", "Read typed file metadata", "file_stat"))
	command.AddCommand(files)
	process := &cobra.Command{Use: "process", Short: "Inspect and terminate only run-owned process handles", RunE: computerHelp}
	list := newComputerOperationCommand("list C1", "List run-owned process handles", "process_list")
	list.Flags().Int("max-items", api.ComputerMaxItems, "Maximum process handles (1-64)")
	process.AddCommand(list, newComputerOperationCommand("inspect C1 HANDLE", "Inspect one run-owned process handle", "process_inspect"), newComputerOperationCommand("terminate C1 HANDLE", "Terminate one run-owned process handle", "process_terminate"))
	command.AddCommand(process)
	addComputerScreenCommands(command)
	addComputerStepCommands(command)
	return command
}

const computerAccountLaunchAgents = "/Users/computer/Library/LaunchAgents"

const computerPersistentHelp = `Persistent computers are a preview, open to organizations Revyl enabled;
others get persistent_computers_not_enabled. An organization keeps at most 10
persistent computers and 100 snapshots unless Revyl raised its limits.
A persistent computer (create --persistent, macOS only) keeps one ID for life.
Stop saves its whole disk on the Mac it runs on: files, brew installs and other
system changes; stop --wait waits until that save is confirmed. Memory and
running processes do not survive, so a start is a cold boot of a new run from
that disk, with the serial number and hardware UUID the computer had before.
A save is on that Mac first and is then copied off it right away at
a capped rate, usually within a few minutes: list and get show a stopped
computer as "saved on host" until the copy is done and "saved off host" after.
Until then, losing that Mac loses the save, and a start on that Mac stops the
copy and boots from its local disk. A start runs on the Mac holding the save
while it is online, waiting to start like a create when it is busy. When that
Mac is offline, a save copied off host starts on another Mac, and one still
only on its host fails with saved_disk_unavailable. If a start never becomes
active, the computer is stopped again with its disk unchanged. A saved disk is
kept until you delete the computer. Some saves still end: one made by an older
Mac runtime ends 30 days after it was made, and one whose stop could not ready
the disk ends with the sign-in window the computer was created with. Start
refuses such a save with saved_disk_expired in its last 24 hours, the longest a
started run can last. Reaching its lifetime end also stops a computer and keeps
its disk. Only delete removes the disk; deleting a computer leaves its
snapshots, and each keeps the save it names until you delete the snapshot.`

const computerStartRunConflict = "This computer's last run hasn't finished stopping: Revyl may still be recording the disk its Mac saved. When that report is delayed this usually clears within 15 minutes, but a computer with no save off host to fall back on can wait until 24 hours 15 minutes after that run started. The disk is kept; try start again shortly, check it with revyl computer get, or remove it with revyl computer delete, which still works."

var computerPersistentRefusals = map[string]string{
	"persistent_computers_not_enabled": "Persistent computers are a preview and aren't enabled for this organization.",
	"computer_not_persistent":          "This computer isn't persistent. Only computers created with --persistent can start from a saved disk and be deleted.",
	"saved_disk_unavailable":           "The Mac holding this computer's saved disk is offline and the save isn't copied off it yet, or no Mac can start it right now. The disk stays saved; try start again later.",
	"saved_disk_expired":               "This computer's saved disk has reached its end: it was made by an older Mac runtime, which kept saves 30 days, or by a stop that could not ready the disk. Delete it, then create a new computer.",
	"computer_not_stopped":             "Only a stopped computer can be snapshotted. Stop it with revyl computer stop --wait, then save the snapshot.",
	"saved_disk_not_durable":           "This computer's save is still only on its Mac. Wait until revyl computer get shows it saved off host, then save the snapshot.",
	"snapshot_name_taken":              "Your organization already has a snapshot with this name. Pick another name, or delete the old snapshot first.",
	"snapshot_expired":                 "This snapshot's saved disk has reached its end, so it can't start a computer: it was made by an older Mac runtime, which kept saves 30 days, or by a stop that could not ready the disk. Save a new snapshot of a recently stopped computer.",
	"persistent_computer_limit":        "Your organization has as many persistent computers as it may keep. Delete one you no longer need, then try again.",
	"snapshot_limit":                   "Your organization has as many snapshots as it may keep. Delete one you no longer need, then save again.",
}

const computerSnapshotHelp = `Snapshots are part of the persistent computer preview, open to organizations
Revyl enabled; others get persistent_computers_not_enabled.
A snapshot names a stopped persistent computer's current saved disk once it is
saved off host, under a name unique in your organization. It copies nothing,
so it is ready at once. create --from-snapshot S1 creates a new persistent
macOS computer whose disk starts as the snapshot's. Files on that disk,
secrets stored in them included, and the login keychain carry over, so anyone
in your organization who can create computers can use what it holds. Items in
the data-protection keychain (Apple Account, iCloud, Internet Accounts and
Safari's saved passwords) are tied to the original computer's hardware
identity, so the new computer can't read them. Website sign-ins may also come
up signed out: many sites, Revyl's included, replace a browser's session token
each time it is used, so once the snapshotted computer, or another computer
made from the snapshot, has used that session, a new computer from the
snapshot opens the site signed out. Sign in again after creating it, or use a
session profile that signs in.
A snapshot lasts until you delete it or the save it names ends (expires_at;
see revyl computer start --help), even after its computer starts again or is
deleted. Deleting a snapshot never changes a computer already created from it.`

func newComputerSnapshotCommand() *cobra.Command {
	command := &cobra.Command{Use: "snapshot", Hidden: true, Short: "Save, list, show and delete snapshots of stopped persistent computers", Long: computerSnapshotHelp, RunE: computerHelp}
	save := &cobra.Command{Use: "save C1 --name NAME", Short: "Save a stopped persistent computer's saved disk as a named snapshot", Long: computerSnapshotHelp}
	save.Flags().String("name", "", "Snapshot name: 1-64 letters, digits, '.', '_' or '-', starting with a letter or digit")
	save.RunE = func(cmd *cobra.Command, args []string) error {
		return runComputerCommand(cmd, func(invocation *computerInvocation) (any, error) {
			if len(args) != 1 {
				return nil, errors.New("computer snapshot save requires exactly one computer UUID")
			}
			id, err := parseComputerUUID(args[0])
			if err != nil {
				return nil, err
			}
			invocation.ComputerSessionID = id.String()
			name, _ := cmd.Flags().GetString("name")
			client, err := computerClient(cmd)
			if err != nil {
				return nil, err
			}
			return client.SaveComputerSnapshot(cmd.Context(), id, name)
		})
	}
	list := &cobra.Command{Use: "list", Short: "Show your organization's newest snapshots"}
	list.Flags().Int("limit", 25, fmt.Sprintf("How many snapshots to show (1-%d)", api.ComputerListMaxLimit))
	list.RunE = func(cmd *cobra.Command, args []string) error {
		return runComputerCommand(cmd, func(*computerInvocation) (any, error) {
			if len(args) != 0 {
				return nil, errors.New("computer snapshot list does not accept positional arguments")
			}
			limit, _ := cmd.Flags().GetInt("limit")
			client, err := computerClient(cmd)
			if err != nil {
				return nil, err
			}
			return client.ListComputerSnapshots(cmd.Context(), limit)
		})
	}
	get := &cobra.Command{Use: "get S1", Short: "Show one snapshot by ID or name"}
	get.RunE = func(cmd *cobra.Command, args []string) error {
		return runComputerCommand(cmd, func(*computerInvocation) (any, error) {
			if len(args) != 1 {
				return nil, errors.New("computer snapshot get requires exactly one snapshot ID or name")
			}
			client, err := computerClient(cmd)
			if err != nil {
				return nil, err
			}
			return client.GetComputerSnapshot(cmd.Context(), args[0])
		})
	}
	deleteSnapshot := &cobra.Command{Use: "delete S1", Short: "Delete one snapshot by ID or name", Long: "Delete one snapshot by ID or name. Computers already created from it keep\ntheir own disks."}
	deleteSnapshot.Flags().Bool("yes", false, "Delete without asking; required when not run in a terminal")
	deleteSnapshot.RunE = func(cmd *cobra.Command, args []string) error {
		return runComputerCommand(cmd, func(*computerInvocation) (any, error) {
			if len(args) != 1 {
				return nil, errors.New("computer snapshot delete requires exactly one snapshot ID or name")
			}
			reference := args[0]
			if yes, _ := cmd.Flags().GetBool("yes"); !yes {
				if !ui.IsInputTTY() {
					return nil, errors.New("computer snapshot delete can't be undone; pass --yes to confirm")
				}
				confirmed, err := ui.PromptConfirm(fmt.Sprintf("Delete snapshot %s? This can't be undone.", reference), false)
				if err != nil || !confirmed {
					return nil, errors.New("computer snapshot delete canceled; nothing was deleted")
				}
			}
			client, err := computerClient(cmd)
			if err != nil {
				return nil, err
			}
			if err := client.DeleteComputerSnapshot(cmd.Context(), reference); err != nil {
				return nil, err
			}
			return &computerSnapshotDeleteResult{Snapshot: reference, State: "deleted"}, nil
		})
	}
	command.AddCommand(save, list, get, deleteSnapshot)
	return command
}

type computerSnapshotDeleteResult struct {
	Snapshot string `json:"snapshot"`
	State    string `json:"state"`
}

type computerDeleteResult struct {
	ComputerID string `json:"computer_id"`
	State      string `json:"state"`
}

// awaitPersistentComputer waits until ctx ends while a persistent computer is
// still in the waiting state, returning its latest status.
func awaitPersistentComputer(ctx context.Context, cmd *cobra.Command, client *api.Client, invocation *computerInvocation, status *api.PersistentComputerStatus, waiting api.PersistentComputerStatusState, waitText string) (*api.PersistentComputerStatus, error) {
	invocation.waited = true
	if status.State == waiting {
		fmt.Fprintln(cmd.ErrOrStderr(), waitText)
	}
	status, err := client.AwaitPersistentComputer(ctx, status, waiting)
	if err == nil && cmd.Context().Err() != nil {
		return nil, cmd.Context().Err()
	}
	return status, err
}

// computerWaitFlags reads --wait and its --wait-timeout, which needs --wait.
func computerWaitFlags(cmd *cobra.Command) (bool, time.Duration, error) {
	wait, _ := cmd.Flags().GetBool("wait")
	waitTimeout, _ := cmd.Flags().GetDuration("wait-timeout")
	if cmd.Flags().Changed("wait-timeout") && !wait {
		return false, 0, errors.New("--wait-timeout requires --wait")
	}
	if waitTimeout < time.Second || waitTimeout > 30*time.Minute {
		return false, 0, errors.New("--wait-timeout must be from 1s to 30m")
	}
	return wait, waitTimeout, nil
}

// runComputerOverview answers a bare revyl computer with the running
// computers; signed out, it shows help as before.
func runComputerOverview(cmd *cobra.Command, args []string) error {
	client, err := computerClient(cmd)
	if len(args) != 0 || err != nil {
		return computerHelp(cmd, args)
	}
	return runComputerCommand(cmd, func(*computerInvocation) (any, error) {
		return client.ListComputerRuns(cmd.Context(), 25)
	})
}

func computerHelp(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	return runComputerCommand(cmd, func(*computerInvocation) (any, error) { return nil, errors.New("unknown computer command; use --help") })
}

var computerFlagValueError = regexp.MustCompile(`for "(--[a-z0-9-]+)" flag`)

// computerFlagError names the flag without echoing its value, which may be
// text to type or an environment variable.
func computerFlagError(cmd *cobra.Command, err error) error {
	message := err.Error()
	for _, prefix := range []string{"unknown flag: ", "unknown shorthand flag: "} {
		if strings.HasPrefix(message, prefix) {
			return fmt.Errorf("unknown flag %s; use --help", strings.Fields(strings.TrimPrefix(message, prefix))[0])
		}
	}
	for _, match := range computerFlagValueError.FindAllStringSubmatch(message, -1) {
		if cmd.Flags().Lookup(strings.TrimLeft(match[1], "-")) != nil {
			return fmt.Errorf("invalid value for %s; use --help", match[1])
		}
	}
	if start := strings.Index(message, "flag needs an argument: "); start >= 0 {
		return fmt.Errorf("%s; use --help", message[start:])
	}
	return errors.New("invalid computer flags; use --help")
}

func printComputerNextSteps(cmd *cobra.Command, id string, running bool) {
	if jsonOutput, _ := cmd.Flags().GetBool("json"); jsonOutput {
		return
	}
	if !running {
		ui.PrintNextSteps([]ui.NextStep{{Label: "Start one:", Command: "revyl computer create --wait"}})
		return
	}
	ui.PrintNextSteps([]ui.NextStep{
		{Label: "See the screen:", Command: fmt.Sprintf("revyl computer screenshot %s --out screen.png", id)},
		{Label: "Act and see the result:", Command: fmt.Sprintf("revyl computer click %s --target \"the Save button\" --out screen.png", id)},
		{Label: "Stop it when done:", Command: fmt.Sprintf("revyl computer stop %s", id)},
	})
}

func printComputerNotReadyStep(cmd *cobra.Command, id string) {
	if jsonOutput, _ := cmd.Flags().GetBool("json"); jsonOutput {
		return
	}
	ui.PrintNextSteps([]ui.NextStep{{Label: "It is still starting; check until it is ready:", Command: "revyl computer get " + id}})
}

// printComputerListNextSteps points at a usable computer first, then one
// still starting, then one still holding a device slot, and offers create only
// when the page holds every computer and none of them is in use.
func printComputerListNextSteps(cmd *cobra.Command, list *api.ComputerRunListResponse) {
	if jsonOutput, _ := cmd.Flags().GetBool("json"); jsonOutput {
		return
	}
	for _, computer := range list.Computers {
		if computer.State == "active" {
			printComputerNextSteps(cmd, computer.ComputerSessionId.String(), true)
			return
		}
	}
	for _, computer := range list.Computers {
		if computer.State == "starting" {
			printComputerNotReadyStep(cmd, computer.ComputerSessionId.String())
			return
		}
	}
	for _, computer := range list.Computers {
		if computer.Release != api.ComputerRunSummaryReleaseConfirmed {
			ui.PrintNextSteps([]ui.NextStep{{Label: "It still holds a device slot; free it:", Command: "revyl computer stop " + computer.ComputerSessionId.String()}})
			return
		}
	}
	if list.Total > len(list.Computers) {
		ui.PrintNextSteps([]ui.NextStep{{Label: "Look further back:", Command: fmt.Sprintf("revyl computer list --limit %d", api.ComputerListMaxLimit)}})
		return
	}
	printComputerNextSteps(cmd, "", false)
}

func newComputerOperationCommand(use, short, kind string) *cobra.Command {
	command := &cobra.Command{Use: use, Short: short}
	command.Flags().Duration("timeout", 30*time.Second, "Operation timeout (1ms-5m); timeout does not imply rollback")
	command.Flags().String("request-id", "", "Stable request UUID (generated once when omitted; never retried automatically)")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		return runComputerCommand(cmd, func(invocation *computerInvocation) (any, error) {
			if len(args) == 0 {
				return nil, errors.New("an explicit computer UUID is required")
			}
			id, err := parseComputerUUID(args[0])
			if err != nil {
				return nil, err
			}
			invocation.ComputerSessionID = id.String()
			requestText, _ := cmd.Flags().GetString("request-id")
			requestID, err := computerUUIDOrNew(requestText)
			if err != nil {
				return nil, err
			}
			invocation.RequestID, invocation.OperationID = requestID.String(), api.ComputerOperationID(id, requestID).String()
			duration, _ := cmd.Flags().GetDuration("timeout")
			maxTimeout, maxText := 5*time.Minute, "5m"
			if kind == "exec" {
				maxTimeout, maxText = time.Hour, "60m"
			}
			if duration < time.Millisecond || duration > maxTimeout || duration%time.Millisecond != 0 {
				return nil, fmt.Errorf("timeout must be an exact number of milliseconds from 1ms to %s", maxText)
			}
			timeoutMs := int(duration / time.Millisecond)
			request := api.ComputerCommandRequest{ComputerSessionId: id, RequestId: requestID}
			switch kind {
			case "exec":
				if len(args) < 2 || cmd.ArgsLenAtDash() != 1 {
					return nil, errors.New("exec requires C1 followed by -- and structured argv")
				}
				stdinPath, _ := cmd.Flags().GetString("stdin")
				var stdin []byte
				if stdinPath != "" {
					stdin, err = readComputerLocalInput(cmd, stdinPath, api.ComputerMaxCommandBytes, duration)
					if err != nil {
						return nil, err
					}
				}
				encoded := api.EncodeComputerBytes(stdin)
				exec := api.ComputerExec{Argv: args[1:], Stdin: &encoded, TimeoutMs: &timeoutMs}
				if argv := strings.Join(exec.Argv, " "); strings.Contains(argv, "Library/LaunchAgents") && !strings.Contains(argv, computerAccountLaunchAgents) {
					fmt.Fprintf(cmd.ErrOrStderr(), "revyl: exec runs with HOME=/Users/computer/workspace, but macOS loads LaunchAgents at login only from %s\n", computerAccountLaunchAgents)
				}
				if cmd.Flags().Changed("cwd") {
					cwd, _ := cmd.Flags().GetString("cwd")
					exec.Cwd = &cwd
				}
				if cmd.Flags().Changed("env") {
					entries, _ := cmd.Flags().GetStringArray("env")
					env, envErr := computerExecEnv(entries)
					if envErr != nil {
						return nil, envErr
					}
					exec.Env = &env
				}
				invocation.execOptions = exec.Cwd != nil || exec.Env != nil || duration > 5*time.Minute
				if err := api.ValidateComputerExec(exec); err != nil {
					return nil, err
				}
				err = request.Command.FromComputerExec(exec)
			case "file_read", "file_write", "file_stat":
				if len(args) != 2 {
					return nil, errors.New("file operation requires computer UUID and guest-relative path")
				}
				if err := api.ValidateComputerPath(args[1]); err != nil {
					return nil, err
				}
				if kind == "file_read" {
					limit, _ := cmd.Flags().GetInt("max-bytes")
					err = request.Command.FromComputerFileRead(api.ComputerFileRead{Path: args[1], MaxBytes: &limit, TimeoutMs: &timeoutMs})
				}
				if kind == "file_stat" {
					err = request.Command.FromComputerFileStat(api.ComputerFileStat{Path: args[1], TimeoutMs: &timeoutMs})
				}
				if kind == "file_write" {
					path, _ := cmd.Flags().GetString("from")
					if path == "" {
						return nil, errors.New("file write requires --from FILE or --from -")
					}
					data, readErr := readComputerLocalInput(cmd, path, api.ComputerMaxWriteBytes, duration)
					if readErr != nil {
						return nil, readErr
					}
					err = request.Command.FromComputerFileWrite(api.ComputerFileWrite{Path: args[1], Data: api.EncodeComputerBytes(data), TimeoutMs: &timeoutMs})
				}
			case "process_list":
				if len(args) != 1 {
					return nil, errors.New("process list requires exactly one computer UUID")
				}
				limit, _ := cmd.Flags().GetInt("max-items")
				err = request.Command.FromComputerProcessList(api.ComputerProcessList{MaxItems: &limit, TimeoutMs: &timeoutMs})
			default:
				if len(args) != 2 {
					return nil, errors.New("process operation requires computer and handle UUIDs")
				}
				handleID, parseErr := parseComputerUUID(args[1])
				if parseErr != nil {
					return nil, parseErr
				}
				handle := api.ComputerProcessHandle{ComputerSessionId: id, HandleId: handleID}
				if kind == "process_inspect" {
					err = request.Command.FromComputerProcessInspect(api.ComputerProcessInspect{Handle: handle, TimeoutMs: &timeoutMs})
				} else {
					err = request.Command.FromComputerProcessTerminate(api.ComputerProcessTerminate{Handle: handle, TimeoutMs: &timeoutMs})
				}
			}
			if err != nil {
				return nil, errors.New("computer request could not be encoded")
			}
			if err := api.ValidateComputerCommand(request); err != nil {
				return nil, err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "computer_session_id=%s request_id=%s operation_id=%s\n", id, requestID, invocation.OperationID)
			client, err := computerClient(cmd)
			if err != nil {
				return nil, err
			}
			return client.SubmitComputerOperation(cmd.Context(), request)
		})
	}
	return command
}

// computerExecEnv reads --env entries: NAME=VALUE, or NAME to copy that local
// variable, so a secret need not appear on the local command line.
func computerExecEnv(entries []string) (map[string]string, error) {
	env := make(map[string]string, len(entries))
	for _, entry := range entries {
		name, value, hasValue := strings.Cut(entry, "=")
		if !hasValue {
			var ok bool
			if value, ok = os.LookupEnv(name); !ok {
				return nil, fmt.Errorf("--env %s: no local environment variable %s; pass NAME=VALUE or export it first", name, name)
			}
		}
		env[name] = value
	}
	return env, nil
}

func parseComputerUUID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil || value != id.String() {
		return uuid.Nil, errors.New("computer, request and handle IDs must be canonical nonzero UUIDs")
	}
	return id, nil
}

func computerUUIDOrNew(value string) (uuid.UUID, error) {
	if value == "" {
		return uuid.New(), nil
	}
	return parseComputerUUID(value)
}

func computerLifetimeSeconds(lifetime time.Duration) (int, error) {
	if lifetime < time.Minute || lifetime > 24*time.Hour || lifetime%time.Second != 0 {
		return 0, errors.New("--timeout must be whole seconds from 1m to 24h")
	}
	return int(lifetime / time.Second), nil
}

func computerDisplaySize(value string) (*api.ComputerDisplaySize, error) {
	if value == "" {
		return nil, nil
	}
	var width, height int
	if n, err := fmt.Sscanf(value, "%dx%d", &width, &height); err != nil || n != 2 || fmt.Sprintf("%dx%d", width, height) != value ||
		width < 800 || width > 1920 || height < 600 || height > 1080 || width%2 != 0 || height%2 != 0 {
		return nil, errors.New("--display must be WIDTHxHEIGHT with an even width from 800 to 1920 and an even height from 600 to 1080, for example 1440x900")
	}
	return &api.ComputerDisplaySize{Width: width, Height: height}, nil
}

func computerDisplayAnalytics(display *api.ComputerDisplaySize) (preset string, sizeBucket string) {
	if display == nil {
		return "default", "1280x800"
	}
	switch size := fmt.Sprintf("%dx%d", display.Width, display.Height); size {
	case "1280x800", "1280x1024", "1920x1080":
		return "custom", size
	}
	return "custom", "other"
}

// computerSessionProfileRequest is what a create sends for a session profile,
// resolved the way device starts resolve theirs: from the checkout, the
// default, --session-profile, else the checkout's session.default_profile, sent
// by name with its definition and script as this checkout has them.
// --from-checkout=false sends --session-profile, else the project's default,
// for Revyl to read from Git. Both send the project in .revyl/config.yaml and
// the Git branch: --branch, else the checkout's.
func computerSessionProfileRequest(cmd *cobra.Command, osName string) (sessionprofile.Start, error) {
	pick, _ := cmd.Flags().GetString("session-profile")
	pick = strings.TrimSpace(pick)
	fromCheckout, _ := cmd.Flags().GetBool(fromCheckoutFlagName)
	explicit := pick != "" && pick != config.NoSessionProfile
	asked := explicit || cmd.Flags().Changed(fromCheckoutFlagName) || cmd.Flags().Changed("branch")
	if explicit && !config.ValidSessionProfileName(pick) {
		return sessionprofile.Start{}, fmt.Errorf("--session-profile %q isn't a session profile name: use 1-64 lowercase letters, digits or hyphens, or none", pick)
	}
	if pick == config.NoSessionProfile {
		if cmd.Flags().Changed(fromCheckoutFlagName) || cmd.Flags().Changed("branch") {
			return sessionprofile.Start{}, errors.New("--from-checkout and --branch need a session profile, not none")
		}
		return sessionprofile.Start{}, nil
	}
	if osName != "macos" {
		if asked {
			return sessionprofile.Start{}, errors.New("--session-profile, --from-checkout and --branch run only on --os macos")
		}
		return sessionprofile.Start{}, nil
	}
	project, err := resolveLocalProjectConfiguration()
	if err != nil {
		if asked {
			return sessionprofile.Start{}, actionableLocalConfigError(err)
		}
		return sessionprofile.Start{}, nil
	}
	var session config.AuthoredSession
	if project.Authored.Session != nil {
		session = *project.Authored.Session
	}
	branch, _ := cmd.Flags().GetString("branch")
	profile, err := sessionprofile.ResolveStart(cmd.Context(), sessionprofile.StartOptions{
		Session:       session,
		ProjectRoot:   project.ProjectRoot,
		ConfigPath:    project.ConfigPath,
		Pick:          pick,
		FromCheckout:  fromCheckout,
		GitBranchName: branch,
		Asked:         asked,
	})
	if err != nil {
		if fromCheckout && !errors.Is(err, sessionprofile.ErrNoProjectID) {
			return sessionprofile.Start{}, fmt.Errorf("%w; rerun with --%s=false to use the profile Revyl has from Git", err, fromCheckoutFlagName)
		}
		return sessionprofile.Start{}, err
	}
	if profile.Instruction == nil && cmd.Flags().Changed("branch") {
		return sessionprofile.Start{}, errors.New("--branch needs a session profile: pass --session-profile, set session.default_profile, or pass --from-checkout=false")
	}
	return profile, nil
}

func computerClient(cmd *cobra.Command) (*api.Client, error) {
	key, err := getAPIKey()
	if err != nil {
		return nil, errors.New("not authenticated; run revyl auth login")
	}
	return api.NewClientWithDevMode(key, commandDevMode(cmd)), nil
}

func runComputerCommand(cmd *cobra.Command, run func(*computerInvocation) (any, error)) error {
	invocation := &computerInvocation{}
	value, err := run(invocation)
	completion := analytics.CommandCompletion{Domain: "computer", Properties: map[string]any{}}
	performance, isPerformance := value.(*computerPerformanceResult)
	if invocation.ComputerSessionID != "" {
		completion.Properties["entity_id"] = invocation.ComputerSessionID
	}
	if cmd.Name() == "create" && invocation.ComputerSessionID != "" {
		completion.Properties["computer_display_preset"], completion.Properties["computer_display_size"] = computerDisplayAnalytics(invocation.createdDisplay)
		if invocation.sessionProfilePick != "" {
			completion.Properties["session_profile_pick"] = string(invocation.sessionProfilePick)
			completion.Properties["session_profile_script_sent"] = invocation.sessionScriptSent
		}
	}
	if isPerformance && performance.Page != nil {
		completion.DomainStatus = string(performance.Page.State)
	}
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if err != nil {
		var refusal *api.APIError
		if invocation.execOptions && errors.As(err, &refusal) && refusal.Code == "invalid_request" {
			refusal.Hint = "This server may not accept --cwd, --env or an exec --timeout over 5m yet; retry without them, for example -- /bin/zsh -lc 'cd DIR && COMMAND'."
		}
		if !jsonOutput && errors.As(err, &refusal) {
			if message, ok := computerPersistentRefusals[refusal.Code]; ok {
				refusal.Message = message
			}
			if cmd.Name() == "start" && refusal.Code == "run_conflict" {
				refusal.Message = computerStartRunConflict
			}
		}
		if cmd.Name() == "start" && errors.As(err, &refusal) && refusal.Code == "inference_unavailable" {
			refusal.Message = "no computer host with its own vision model is up right now, so this computer created with --inference did not start"
			refusal.Hint = "Retry later, or start this run without its model with revyl computer start " + invocation.ComputerSessionID + " --no-inference."
		}
		invocation.Error = err.Error()
		if errors.As(err, &refusal) {
			invocation.ErrorCode = refusal.Code
		}
		if errors.Is(err, api.ErrComputerOutcomeUnconfirmed) {
			invocation.Status = "unconfirmed"
			completion.DomainStatus = "unconfirmed"
		}
		analytics.SetCommandCompletion(cmd.Context(), completion)
		if jsonOutput && !(isPerformance && performance.Watch) {
			if errors.As(err, &refusal) && refusal.BillingURL != "" {
				invocation.BillingURL, invocation.BillingAction = refusal.BillingURL, string(refusal.BillingAction)
				invocation.NextStep = billingRefusalGuidance(cmd.Context(), refusal.BillingAction, refusal.UpgradeBenefits)
				refusal.BillingReported = true
			}
			if outputErr := writeComputerJSON(cmd.OutOrStdout(), invocation, api.ComputerMaxResponseJSON); outputErr != nil {
				return outputErr
			}
		}
		return analytics.WithSafeDiagnostic(err, "computer request failed")
	}
	exitCode := 0
	exitMessage := "computer command did not confirm a successful recorded outcome; inspect the original run or operation"
	switch result := value.(type) {
	case *api.ComputerScreenshot, *api.ComputerScreenActionResult:
		completion.DomainStatus = "succeeded"
		if err := writeComputerScreenOutput(cmd, result, jsonOutput); err != nil {
			return analytics.WithSafeDiagnostic(err, "computer screen output failed")
		}
	case *api.ComputerStepResult:
		completion.DomainStatus = result.Status
		if result.Status != "passed" {
			exitCode, exitMessage = 1, fmt.Sprintf("computer %s %s", result.StepType, result.Status)
		}
		if err := writeComputerStep(cmd, result, jsonOutput); err != nil {
			return analytics.WithSafeDiagnostic(err, "computer step output failed")
		}
	case *api.ComputerCommandResponse:
		info, inspectErr := api.InspectComputerExecution(*result)
		if inspectErr != nil {
			return inspectErr
		}
		completion.DomainStatus = info.Outcome
		completion.Properties["computer_outcome"] = info.Outcome
		completion.Properties["computer_evidence_write_status"] = string(result.Evidence)
		analytics.SetCommandCompletion(cmd.Context(), completion)
		switch info.Outcome {
		case "failed":
			exitCode = 1
			exitMessage = "Command failed: " + info.ErrorCode
			if info.ExitCode != nil {
				exitMessage = fmt.Sprintf("Command exited with code %d", *info.ExitCode)
				if *info.ExitCode > 0 && *info.ExitCode <= 255 {
					exitCode = *info.ExitCode
				}
			}
		case "timed_out":
			exitCode = 124
			exitMessage = "Command timed out"
		case "unknown":
			exitCode = 125
		}
		if exitCode == 0 && result.Evidence != "recorded" {
			exitCode = 74
		}
		if !jsonOutput && (info.Kind == "exec" || info.Kind == "file_read") {
			stdout := info.Stdout
			if info.Kind == "file_read" {
				stdout = info.Data
			}
			if err := writeComputerOutput(cmd.OutOrStdout(), stdout, api.ComputerMaxOutputBytes); err != nil {
				return err
			}
			if err := writeComputerOutput(cmd.ErrOrStderr(), info.Stderr, api.ComputerMaxOutputBytes); err != nil {
				return err
			}
			if info.Kind == "exec" && len(info.Stdout)+len(info.Stderr) == api.ComputerMaxOutputBytes {
				fmt.Fprintf(cmd.ErrOrStderr(), "revyl: output reached the %g MiB limit; the rest was dropped (pipe through head or redirect to a file)\n", float64(api.ComputerMaxOutputBytes)/(1<<20))
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "\noperation_id=%s outcome=%s evidence=%s\n", result.OperationId, info.Outcome, result.Evidence)
		} else if err := writeComputerJSON(cmd.OutOrStdout(), result, api.ComputerMaxResponseJSON); err != nil {
			return err
		}
	case *api.ComputerReportDetail:
		status := api.InspectComputerReport(api.ComputerReport{
			State:               (*api.ComputerReportState)(result.State),
			Release:             (*api.ComputerReportRelease)(result.Release),
			EvidenceWriteStatus: (*api.ComputerReportEvidenceWriteStatus)(result.EvidenceWriteStatus),
		})
		completion.DomainStatus = status.State
		completion.Properties["computer_evidence_write_status"] = status.Evidence
		completion.Properties["computer_release"] = status.Release
		analytics.SetCommandCompletion(cmd.Context(), completion)
		if jsonOutput {
			if err := writeComputerJSON(cmd.OutOrStdout(), result, api.ComputerMaxResponseJSON); err != nil {
				return err
			}
		} else {
			full, _ := cmd.Flags().GetBool("full")
			if err := writeComputerDetail(cmd.OutOrStdout(), result, status, full); err != nil {
				return err
			}
			profileReady := result.SessionProfile == nil || result.SessionProfile.Status == api.ComputerSessionProfileStatusSucceeded
			if status.State == "active" && profileReady {
				printComputerNextSteps(cmd, result.ComputerSessionId.String(), true)
			} else if status.State == "starting" || status.State == "active" {
				printComputerNotReadyStep(cmd, result.ComputerSessionId.String())
			}
		}
	case *api.ComputerRunListResponse:
		completion.DomainStatus = "inspected"
		analytics.SetCommandCompletion(cmd.Context(), completion)
		if jsonOutput {
			if err := writeComputerJSON(cmd.OutOrStdout(), result, api.ComputerMaxResponseJSON); err != nil {
				return err
			}
		} else {
			write := writeComputerList
			if cmd.Name() == "computer" {
				write = writeComputerOverview
			}
			if err := write(cmd.OutOrStdout(), result); err != nil {
				return err
			}
			printComputerListNextSteps(cmd, result)
		}
	case *api.ComputerReport:
		status := api.InspectComputerReport(*result)
		completion.DomainStatus = status.State
		completion.Properties["computer_evidence_write_status"] = status.Evidence
		completion.Properties["computer_release"] = status.Release
		analytics.SetCommandCompletion(cmd.Context(), completion)
		if cmd.Name() == "stop" {
			if status.Release == "pending" {
				exitCode = 75
			}
			if status.Release == "unknown" || status.State == "owner_lost" {
				exitCode = 125
			}
			if exitCode == 0 && status.Evidence != "recorded" {
				exitCode = 74
			}
		}
		if cmd.Name() == "create" {
			profile := result.SessionProfile
			if profile != nil {
				completion.Properties["computer_session_profile_status"] = string(profile.Status)
			}
			switch {
			case invocation.sessionProfile != "" && profile == nil:
				exitCode, exitMessage = 1, fmt.Sprintf("Revyl didn't report session profile %s on this computer, so it may not have run; stop it with revyl computer stop %s", invocation.sessionProfile, result.ComputerSessionId)
			case profile != nil && profile.Status == api.ComputerSessionProfileStatusFailed:
				exitCode, exitMessage = 1, fmt.Sprintf("session profile %s failed, so Revyl stopped the computer", profile.Name)
				if profile.FailureReason != nil {
					exitMessage = fmt.Sprintf("session profile %s failed: %s; Revyl stopped the computer", profile.Name, *profile.FailureReason)
				}
			case status.State == "failed" || status.State == "owner_lost":
				exitCode, exitMessage = 125, fmt.Sprintf("computer is %s (release %s), not active", status.State, status.Release)
			case invocation.waited && status.State == "starting":
				exitCode, exitMessage = 124, "computer is still starting at --wait-timeout; check it with revyl computer get, or stop it"
			case invocation.waited && status.State != "active":
				exitCode, exitMessage = 1, fmt.Sprintf("computer is %s (release %s), not active", status.State, status.Release)
			case invocation.waited && profile != nil && profile.Status != api.ComputerSessionProfileStatusSucceeded:
				exitCode = 124
				var stopped *api.ComputerReport
				client, stopErr := computerClient(cmd)
				if stopErr == nil {
					stopped, _, stopErr = client.StopComputerRun(cmd.Context(), result.ComputerSessionId)
				}
				switch {
				case stopErr != nil:
					exitMessage = fmt.Sprintf("session profile %s is still %s at --wait-timeout, and stopping the computer failed (%v), so it keeps running; stop it with revyl computer stop %s", profile.Name, profile.Status, stopErr, result.ComputerSessionId)
				case api.InspectComputerReport(*stopped).Release == "confirmed":
					exitMessage = fmt.Sprintf("session profile %s is still %s at --wait-timeout, so the computer was stopped", profile.Name, profile.Status)
				default:
					exitMessage = fmt.Sprintf("session profile %s is still %s at --wait-timeout, so Revyl was asked to stop computer %s, and its release is %s; confirm it with revyl computer stop %s", profile.Name, profile.Status, result.ComputerSessionId, api.InspectComputerReport(*stopped).Release, result.ComputerSessionId)
				}
			case status.Evidence != "recorded":
				exitCode = 74
			}
		}
		if jsonOutput || cmd.Name() == "report" {
			if err := writeComputerJSON(cmd.OutOrStdout(), result, api.ComputerMaxResponseJSON); err != nil {
				return err
			}
		} else {
			line := fmt.Sprintf("computer_session_id=%s report_id=%s state=%s release=%s evidence=%s", result.ComputerSessionId, result.ReportId, status.State, status.Release, status.Evidence)
			if result.SessionProfile != nil {
				line += " session_profile=" + string(result.SessionProfile.Status)
			}
			fmt.Fprintln(cmd.OutOrStdout(), line)
			if cmd.Name() == "create" && exitCode == 0 && status.State == "starting" {
				printComputerNotReadyStep(cmd, result.ComputerSessionId.String())
			}
			if cmd.Name() == "create" && exitCode == 0 && status.State == "active" {
				printComputerNextSteps(cmd, result.ComputerSessionId.String(), true)
			}
		}
	case *api.PersistentComputerStatus:
		completion.DomainStatus = string(result.State)
		if invocation.waited {
			switch {
			case cmd.Name() == "stop" && result.State == api.PersistentComputerStatusStateStopping:
				exitCode, exitMessage = 124, "computer is still stopping at --wait-timeout: its save isn't confirmed, and its disk stays on its Mac for recovery. Check it with revyl computer get; contact support@revyl.ai if it stays stopping"
			case cmd.Name() == "start" && result.State == api.PersistentComputerStatusStateStarting:
				exitCode, exitMessage = 124, "computer is still starting at --wait-timeout; check it with revyl computer get, or stop it"
			case cmd.Name() == "start" && result.State == api.PersistentComputerStatusStateStopped:
				exitCode, exitMessage = 1, "computer ended before it became active, so it is stopped again with its saved disk unchanged; start it again later"
			case cmd.Name() == "stop" && result.State != api.PersistentComputerStatusStateStopped, cmd.Name() == "start" && result.State != api.PersistentComputerStatusStateRunning:
				exitCode, exitMessage = 1, fmt.Sprintf("computer is %s", result.State)
			}
		}
		if jsonOutput {
			if err := writeComputerJSON(cmd.OutOrStdout(), result, api.ComputerMaxResponseJSON); err != nil {
				return err
			}
		} else if err := writePersistentComputerStatus(cmd.OutOrStdout(), result); err != nil {
			return err
		}
	case *api.PersistentComputerListResponse:
		completion.DomainStatus = "inspected"
		analytics.SetCommandCompletion(cmd.Context(), completion)
		if jsonOutput {
			if err := writeComputerJSON(cmd.OutOrStdout(), result, api.ComputerMaxResponseJSON); err != nil {
				return err
			}
		} else if err := writePersistentComputerList(cmd.OutOrStdout(), result); err != nil {
			return err
		}
	case *computerDeleteResult:
		completion.DomainStatus = result.State
		if jsonOutput {
			if err := writeComputerJSON(cmd.OutOrStdout(), result, api.ComputerMaxResponseJSON); err != nil {
				return err
			}
		} else if _, err := fmt.Fprintf(cmd.OutOrStdout(), "computer_id=%s state=%s\n", result.ComputerID, result.State); err != nil {
			return err
		}
	case *api.ComputerSnapshot:
		completion.DomainStatus = "inspected"
		if cmd.Name() == "save" {
			completion.DomainStatus = "saved"
		}
		if jsonOutput {
			if err := writeComputerJSON(cmd.OutOrStdout(), result, api.ComputerMaxResponseJSON); err != nil {
				return err
			}
		} else if err := writeComputerSnapshot(cmd.OutOrStdout(), result); err != nil {
			return err
		}
	case *api.ComputerSnapshotListResponse:
		completion.DomainStatus = "inspected"
		if jsonOutput {
			if err := writeComputerJSON(cmd.OutOrStdout(), result, api.ComputerMaxResponseJSON); err != nil {
				return err
			}
		} else if err := writeComputerSnapshotList(cmd.OutOrStdout(), result); err != nil {
			return err
		}
	case *computerSnapshotDeleteResult:
		completion.DomainStatus = result.State
		if jsonOutput {
			if err := writeComputerJSON(cmd.OutOrStdout(), result, api.ComputerMaxResponseJSON); err != nil {
				return err
			}
		} else if _, err := fmt.Fprintf(cmd.OutOrStdout(), "snapshot=%s state=%s\n", result.Snapshot, result.State); err != nil {
			return err
		}
	case *api.ComputerHostingCapability:
		completion.DomainStatus = "inspected"
		analytics.SetCommandCompletion(cmd.Context(), completion)
		if jsonOutput {
			if err := writeComputerJSON(cmd.OutOrStdout(), result, api.ComputerMaxResponseJSON); err != nil {
				return err
			}
		} else if err := writeComputerHosting(cmd.OutOrStdout(), result); err != nil {
			return err
		}
	case *api.ComputerRelatedSessionListResponse:
		completion.DomainStatus = "inspected"
		analytics.SetCommandCompletion(cmd.Context(), completion)
		if jsonOutput {
			if err := writeComputerJSON(cmd.OutOrStdout(), result, api.ComputerMaxResponseJSON); err != nil {
				return err
			}
		} else if err := writeComputerSessions(cmd.OutOrStdout(), result); err != nil {
			return err
		}
	case *api.ComputerATIFTrajectory:
		completion.DomainStatus = "inspected"
		completion.Properties["computer_evidence_write_status"] = string(result.Extra.Revyl.EvidenceWriteStatus)
		completion.Properties["computer_release"] = string(result.Extra.Revyl.Release)
		analytics.SetCommandCompletion(cmd.Context(), completion)
		if err := writeComputerJSON(cmd.OutOrStdout(), result, api.ComputerMaxResponseJSON); err != nil {
			return err
		}
	case *computerPerformanceResult:
		analytics.SetCommandCompletion(cmd.Context(), completion)
		if !result.Watch {
			if jsonOutput {
				return writeComputerJSON(cmd.OutOrStdout(), result.Page, api.ComputerMaxResponseJSON)
			}
			return writeComputerPerformance(cmd.OutOrStdout(), result.Page)
		}
		if result.EndReason == "timeout" {
			completion.ExitCode = 124
			return &commandExitError{code: 124, err: analytics.CompletedWithExitCode(errors.New("performance watch timed out"), completion)}
		}
		if result.EndReason == "cancelled" {
			completion.ExitCode = 130
			return &commandExitError{code: 130, err: analytics.CompletedWithExitCode(errors.New("performance watch canceled"), completion)}
		}
	case *computerExportResult:
		completion.DomainStatus = "exported"
		analytics.SetCommandCompletion(cmd.Context(), completion)
		if jsonOutput {
			if err := writeComputerJSON(cmd.OutOrStdout(), result, api.ComputerMaxResponseJSON); err != nil {
				return err
			}
		} else if err := writeComputerExport(cmd.ErrOrStderr(), result); err != nil {
			return err
		}
	case *computerInvocation:
		completion.DomainStatus = "pending"
		exitCode = 75
		if err := writeComputerJSON(cmd.OutOrStdout(), result, api.ComputerMaxResponseJSON); err != nil {
			return err
		}
	default:
		return errors.New("computer command returned an invalid result")
	}
	completion.ExitCode = exitCode
	analytics.SetCommandCompletion(cmd.Context(), completion)
	if exitCode != 0 {
		exitErr := errors.New(exitMessage)
		if invocation.sessionProfilePick != "" {
			exitErr = analytics.WithSafeDiagnostic(exitErr, "computer with a session profile did not become ready")
		}
		return &commandExitError{code: exitCode, err: analytics.CompletedWithExitCode(exitErr, completion)}
	}
	return nil
}
