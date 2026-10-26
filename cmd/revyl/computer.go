package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
)

type computerExitError struct {
	err  error
	code int
}

func (e *computerExitError) Error() string { return e.err.Error() }
func (e *computerExitError) Unwrap() error { return e.err }

type computerInvocation struct {
	ComputerSessionID string `json:"computer_session_id,omitempty"`
	RequestID         string `json:"request_id,omitempty"`
	OperationID       string `json:"operation_id,omitempty"`
	Status            string `json:"status,omitempty"`
	Error             string `json:"error,omitempty"`
}

type computerPerformanceResult struct {
	Page      *api.ComputerPerformancePage
	Watch     bool
	EndReason string
}

func newComputerCommand() *cobra.Command {
	command := &cobra.Command{
		Use: "computer", Short: "Start, use and stop cloud computers",
		Hidden: true,
		Long: `A computer is a cloud machine you start, run commands on, and stop.

  revyl computer create --os macos       start a computer
  revyl computer list                    show your organization's computers
  revyl computer exec ID -- uname -a     run a program on it
  revyl computer get ID                  show its state and steps
  revyl computer export ID               save a Harbor trial bundle
  revyl computer report ID --format performance
  revyl computer stop ID                 stop it

Add --json to any command for machine-readable output.`,
		RunE: computerHelp,
	}
	command.PersistentFlags().Bool("json", false, "Output JSON; binary fields use URL-safe base64")
	command.SetFlagErrorFunc(func(*cobra.Command, error) error { return errors.New("invalid computer flags; use --help") })
	create := &cobra.Command{Use: "create", Short: "Start a computer"}
	create.Flags().String("os", "", "Computer OS: macos or linux")
	create.Flags().String("id", "", "Proposed computer UUID (generated once when omitted)")
	create.RunE = func(cmd *cobra.Command, args []string) error {
		return runComputerCommand(cmd, func(invocation *computerInvocation) (any, error) {
			if len(args) != 0 {
				return nil, errors.New("computer create does not accept positional arguments")
			}
			osName, _ := cmd.Flags().GetString("os")
			if osName != "macos" && osName != "linux" {
				return nil, errors.New("--os must be macos or linux")
			}
			idText, _ := cmd.Flags().GetString("id")
			id, err := computerUUIDOrNew(idText)
			if err != nil {
				return nil, err
			}
			invocation.ComputerSessionID = id.String()
			fmt.Fprintf(cmd.ErrOrStderr(), "computer_session_id=%s\n", id)
			client, err := computerClient(cmd)
			if err != nil {
				return nil, err
			}
			report, err := client.CreateComputerRun(cmd.Context(), api.ComputerRunCreate{ComputerSessionId: id, Os: api.ComputerRunCreateOs(osName)})
			var refusal *api.APIError
			if jsonOutput, _ := cmd.Flags().GetBool("json"); !jsonOutput && errors.As(err, &refusal) {
				switch refusal.Code {
				case "computer_limit":
					refusal.Message = computerLimitMessage
				case "computer_not_enabled":
					refusal.Message = computerNotEnabledMessage
				}
			}
			return report, err
		})
	}
	command.AddCommand(create)
	listComputers := &cobra.Command{Use: "list", Short: "Show your organization's newest computers"}
	listComputers.Flags().Int("limit", 25, fmt.Sprintf("How many computers to show (1-%d)", api.ComputerListMaxLimit))
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
			return client.ListComputerRuns(cmd.Context(), limit)
		})
	}
	command.AddCommand(listComputers)
	leafShorts := map[string]string{"get": "Show a computer's state and steps", "stop": "Stop a computer", "report": "Print a computer report (JSON, ATIF or performance)"}
	for _, action := range []string{"get", "stop", "report"} {
		leaf := &cobra.Command{Use: action + " C1", Short: leafShorts[action]}
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
				case "stop":
					return client.StopComputerRun(cmd.Context(), id)
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
	return command
}

func computerHelp(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	return runComputerCommand(cmd, func(*computerInvocation) (any, error) { return nil, errors.New("unknown computer command; use --help") })
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
			if duration < time.Millisecond || duration > 5*time.Minute || duration%time.Millisecond != 0 {
				return nil, errors.New("timeout must be an exact number of milliseconds from 1ms to 5m")
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
	if isPerformance && performance.Page != nil {
		completion.DomainStatus = string(performance.Page.State)
	}
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if err != nil {
		invocation.Error = err.Error()
		if errors.Is(err, api.ErrComputerOutcomeUnconfirmed) {
			invocation.Status = "unconfirmed"
			completion.DomainStatus = "unconfirmed"
		}
		analytics.SetCommandCompletion(cmd.Context(), completion)
		if jsonOutput && !(isPerformance && performance.Watch) {
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
		} else if err := writeComputerDetail(cmd.OutOrStdout(), result, status); err != nil {
			return err
		}
	case *api.ComputerRunListResponse:
		completion.DomainStatus = "inspected"
		analytics.SetCommandCompletion(cmd.Context(), completion)
		if jsonOutput {
			if err := writeComputerJSON(cmd.OutOrStdout(), result, api.ComputerMaxResponseJSON); err != nil {
				return err
			}
		} else if err := writeComputerList(cmd.OutOrStdout(), result); err != nil {
			return err
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
		if cmd.Name() == "create" && (status.State == "failed" || status.State == "owner_lost") {
			exitCode = 125
		}
		if cmd.Name() == "create" && exitCode == 0 && status.Evidence != "recorded" {
			exitCode = 74
		}
		if jsonOutput || cmd.Name() == "report" {
			if err := writeComputerJSON(cmd.OutOrStdout(), result, api.ComputerMaxResponseJSON); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "computer_session_id=%s report_id=%s state=%s release=%s evidence=%s\n", result.ComputerSessionId, result.ReportId, status.State, status.Release, status.Evidence)
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
			return &computerExitError{code: 124, err: analytics.CompletedWithExitCode(errors.New("performance watch timed out"), completion)}
		}
		if result.EndReason == "cancelled" {
			completion.ExitCode = 130
			return &computerExitError{code: 130, err: analytics.CompletedWithExitCode(errors.New("performance watch canceled"), completion)}
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
		return &computerExitError{code: exitCode, err: analytics.CompletedWithExitCode(errors.New(exitMessage), completion)}
	}
	return nil
}
