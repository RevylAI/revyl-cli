package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	ComputerMaxCommandBytes = 16_384
	ComputerMaxItems        = 64
	ComputerMaxTimeoutMs    = 300_000
	ComputerMaxResponseJSON = 2 * 1024 * 1024
)

var (
	ComputerMaxOutputBytes = computerSchemaByteCountMaximum(computerValidationSchema, "ComputerFileReadSummary")
	ComputerMaxWriteBytes  = computerSchemaByteCountMaximum(computerValidationSchema, "ComputerFileWriteSummary")
)

func computerSchemaByteCountMaximum(schema []byte, definition string) int {
	var parsed struct {
		Defs map[string]struct {
			Properties struct {
				ByteCount struct {
					Maximum *float64 `json:"maximum"`
				} `json:"byte_count"`
			} `json:"properties"`
		} `json:"$defs"`
	}
	err := json.Unmarshal(schema, &parsed)
	maximum := parsed.Defs[definition].Properties.ByteCount.Maximum
	if err != nil || maximum == nil || *maximum < 1 || *maximum > math.MaxInt32 || *maximum != math.Trunc(*maximum) {
		panic("computer schema " + definition + ".byte_count.maximum must be a positive integer")
	}
	return int(*maximum)
}

var errComputerResponse = errors.New("computer response is invalid; inspect the original run or operation before submitting again")

var errComputerReadFailed = errors.New("computer read failed; inspect the original run or operation before submitting again")

func EncodeComputerBytes(data []byte) string { return base64.URLEncoding.EncodeToString(data) }

func DecodeComputerBytes(encoded string, limit int) ([]byte, error) {
	if limit < 0 || len(encoded) > base64.URLEncoding.EncodedLen(limit) || strings.ContainsAny(encoded, "\r\n\t ") {
		return nil, errComputerResponse
	}
	data, err := base64.URLEncoding.Strict().DecodeString(strings.NewReplacer("+", "-", "/", "_").Replace(encoded))
	if err != nil || len(data) > limit {
		return nil, errComputerResponse
	}
	return data, nil
}

func ValidateComputerPath(path string) error {
	if !utf8.ValidString(path) || len(path) == 0 || len(path) > 1024 || strings.ContainsAny(path, "\\:") {
		return errors.New("computer path must be a bounded guest-relative path")
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return errors.New("computer path must be a bounded guest-relative path")
		}
	}
	for _, char := range path {
		if char < 32 || char == 127 {
			return errors.New("computer path must not contain control characters")
		}
	}
	return nil
}

func ValidateComputerCommand(request ComputerCommandRequest) error {
	if request.ComputerSessionId == uuid.Nil || request.RequestId == uuid.Nil {
		return errors.New("computer and request IDs must be nonzero UUIDs")
	}
	command, err := request.Command.ValueByDiscriminator()
	if err != nil {
		return errors.New("computer command kind is invalid")
	}
	var timeout *int
	switch command := command.(type) {
	case ComputerExec:
		timeout = command.TimeoutMs
		if err := ValidateComputerExec(command); err != nil {
			return err
		}
	case ComputerFileRead:
		timeout = command.TimeoutMs
		if err := ValidateComputerPath(command.Path); err != nil {
			return err
		}
		if command.MaxBytes != nil && (*command.MaxBytes < 1 || *command.MaxBytes > ComputerMaxOutputBytes) {
			return fmt.Errorf("computer read limit must be 1 to %d bytes", ComputerMaxOutputBytes)
		}
	case ComputerFileWrite:
		timeout = command.TimeoutMs
		if err := ValidateComputerPath(command.Path); err != nil {
			return err
		}
		if _, err := DecodeComputerBytes(command.Data, ComputerMaxWriteBytes); err != nil {
			return fmt.Errorf("computer file data is invalid or exceeds %d bytes", ComputerMaxWriteBytes)
		}
	case ComputerFileStat:
		timeout = command.TimeoutMs
		if err := ValidateComputerPath(command.Path); err != nil {
			return err
		}
	case ComputerProcessList:
		timeout = command.TimeoutMs
		if command.MaxItems != nil && (*command.MaxItems < 1 || *command.MaxItems > ComputerMaxItems) {
			return errors.New("computer process limit must be 1 to 64 items")
		}
	case ComputerProcessInspect:
		timeout = command.TimeoutMs
		if command.Handle.ComputerSessionId != request.ComputerSessionId || command.Handle.HandleId == uuid.Nil {
			return errors.New("computer process handle must belong to the selected run")
		}
	case ComputerProcessTerminate:
		timeout = command.TimeoutMs
		if command.Handle.ComputerSessionId != request.ComputerSessionId || command.Handle.HandleId == uuid.Nil {
			return errors.New("computer process handle must belong to the selected run")
		}
	default:
		return errors.New("computer command kind is invalid")
	}
	if timeout != nil && (*timeout < 1 || *timeout > ComputerMaxTimeoutMs) {
		return errors.New("computer timeout must be 1 to 300000 milliseconds")
	}
	return nil
}

func ValidateComputerExec(command ComputerExec) error {
	if len(command.Argv) < 1 || len(command.Argv) > ComputerMaxItems || command.Argv[0] == "" {
		return errors.New("computer exec requires 1 to 64 arguments and a nonempty executable")
	}
	var count int
	for _, arg := range command.Argv {
		if !utf8.ValidString(arg) || strings.ContainsRune(arg, 0) {
			return errors.New("computer exec arguments must be UTF-8 without NUL")
		}
		count += len(arg)
	}
	if command.Stdin != nil {
		data, err := DecodeComputerBytes(*command.Stdin, ComputerMaxCommandBytes)
		if err != nil {
			return errors.New("computer stdin is invalid or exceeds the byte limit")
		}
		count += len(data)
	}
	if count > ComputerMaxCommandBytes {
		return errors.New("computer argv and stdin exceed 16384 bytes")
	}
	return nil
}

func readComputerBody(response *http.Response, requestErr error) ([]byte, error) {
	if requestErr != nil {
		if errors.Is(requestErr, ErrComputerOutcomeUnconfirmed) {
			return nil, requestErr
		}
		return nil, errComputerReadFailed
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, ComputerMaxResponseJSON+1))
	if err != nil || len(body) > ComputerMaxResponseJSON {
		return nil, errComputerResponse
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if response.StatusCode == http.StatusForbidden && strings.EqualFold(contentType, "text/html") && strings.TrimSpace(response.Header.Get("cf-ray")) != "" && !json.Valid(body) {
			return nil, &APIError{
				StatusCode: response.StatusCode,
				Code:       "edge_blocked",
				Message:    "Cloudflare blocked the computer request before it reached the API",
				Hint:       "Contact Revyl support to review the edge security rule; signing in again will not resolve this block.",
			}
		}
		refusal := &APIError{StatusCode: response.StatusCode, Message: "computer request was refused"}
		var safe ComputerAPIError
		if decodeComputerJSON(body, &safe, "detail") == nil {
			code, _ := safe.Detail.AsComputerAPIErrorDetail0()
			switch string(code) {
			case "inaccessible", "invalid_request", "request_conflict", "run_conflict", "owner_unavailable", "run_closed", "operation_limit", "invalid_handle", "operation_not_found", "evidence_conflict", "evidence_unavailable", "start_failed", "runtime_unavailable", "computer_limit", "computer_not_enabled", "unauthenticated", "forbidden", "authentication_unavailable", "screen_control_held", "screen_unavailable", "screen_input_uncertain", "screen_text_unsupported", "screen_point_outside", "screen_key_unsupported":
				refusal.Code, refusal.Message = string(code), "computer request refused: "+string(code)
				if refusal.Code == "screen_input_uncertain" {
					refusal.Message += "; take a screenshot before acting again"
				}
			}
		}
		return nil, refusal
	}
	return body, nil
}

func decodeComputerJSON(body []byte, target any, required ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return errComputerResponse
	}
	for _, key := range required {
		if value, ok := fields[key]; !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errComputerResponse
		}
	}
	if err := json.Unmarshal(body, target); err != nil {
		return errComputerResponse
	}
	return nil
}

func readComputerReport(response *http.Response, requestErr error, id uuid.UUID) (*ComputerReport, error) {
	body, err := readComputerBody(response, requestErr)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, errComputerResponse
	}
	return decodeComputerReport(body, id)
}

func decodeComputerReport(body []byte, id uuid.UUID) (*ComputerReport, error) {
	var report ComputerReport
	var raw struct {
		Steps []map[string]json.RawMessage `json:"steps"`
	}
	if validateComputerResponseSchema(body, "ComputerReport") != nil || decodeComputerJSON(body, &report) != nil || json.Unmarshal(body, &raw) != nil || report.ComputerSessionId != id || report.ReportId == uuid.Nil || report.OrgId == uuid.Nil {
		return nil, errComputerResponse
	}
	for _, step := range raw.Steps {
		if _, ok := step["command_display"]; ok {
			return nil, errComputerResponse
		}
	}
	if !validComputerReportStates(report) {
		return nil, errComputerResponse
	}
	return &report, nil
}

func readComputerReportDetail(response *http.Response, requestErr error, id uuid.UUID) (*ComputerReportDetail, error) {
	body, err := readComputerBody(response, requestErr)
	if err != nil {
		return nil, err
	}
	var detail ComputerReportDetail
	if response.StatusCode != http.StatusOK || validateComputerResponseSchema(body, "ComputerReportDetail") != nil || decodeComputerJSON(body, &detail) != nil {
		return nil, errComputerResponse
	}
	var fields map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if decoder.Decode(&fields) != nil {
		return nil, errComputerResponse
	}
	if steps, ok := fields["steps"].([]any); ok {
		for _, step := range steps {
			if stepFields, ok := step.(map[string]any); ok {
				delete(stepFields, "command_display")
			}
		}
	}
	reportBody, err := json.Marshal(fields)
	if err != nil {
		return nil, errComputerResponse
	}
	if _, err := decodeComputerReport(reportBody, id); err != nil {
		return nil, err
	}
	return &detail, nil
}

func readComputerATIF(response *http.Response, requestErr error, id uuid.UUID) (*ComputerATIFTrajectory, error) {
	body, err := readComputerBody(response, requestErr)
	if err != nil {
		return nil, err
	}
	var result ComputerATIFTrajectory
	if response.StatusCode != http.StatusOK || validateComputerResponseSchema(body, "ComputerATIFTrajectory") != nil || decodeComputerJSON(body, &result) != nil || result.SessionId != id || result.TrajectoryId == uuid.Nil {
		return nil, errComputerResponse
	}
	if !validComputerATIF(result) {
		return nil, errComputerResponse
	}
	return &result, nil
}

func readComputerOperation(response *http.Response, requestErr error, id, operationID uuid.UUID, allowPending bool) (*ComputerCommandResponse, error) {
	body, err := readComputerBody(response, requestErr)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusAccepted {
		if allowPending && json.Valid(body) && bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
			return nil, nil
		}
		return nil, errComputerResponse
	}
	var result ComputerCommandResponse
	if validateComputerResponseSchema(body, "ComputerCommandResponse") != nil || decodeComputerJSON(body, &result, "computer_session_id", "operation_id", "execution", "evidence") != nil || result.ComputerSessionId != id || result.OperationId != operationID || result.Evidence != "recorded" && result.Evidence != "pending" && result.Evidence != "failed" {
		return nil, errComputerResponse
	}
	if _, err := InspectComputerExecution(result); err != nil {
		return nil, err
	}
	return &result, nil
}

type ComputerExecution struct {
	Outcome   string
	Kind      string
	ErrorCode string
	ExitCode  *int
	Stdout    []byte
	Stderr    []byte
	Data      []byte
}

func InspectComputerExecution(response ComputerCommandResponse) (ComputerExecution, error) {
	var info ComputerExecution
	outcome, err := response.Execution.Discriminator()
	if err != nil {
		return info, errComputerResponse
	}
	info.Outcome = outcome
	switch outcome {
	case "succeeded":
		var result ComputerSucceeded
		if decodeComputerJSON(response.Execution.union, &result, "outcome", "result") != nil {
			return info, errComputerResponse
		}
		info, err = inspectComputerResult(result.Result.union, response.ComputerSessionId)
		info.Outcome = outcome
		if err != nil || info.ExitCode != nil && *info.ExitCode != 0 {
			return info, errComputerResponse
		}
	case "failed":
		var result ComputerFailed
		if decodeComputerJSON(response.Execution.union, &result, "outcome", "error_code") != nil {
			return info, errComputerResponse
		}
		info.ErrorCode = string(result.ErrorCode)
		if result.ErrorCode == "exec_nonzero" && result.Result != nil {
			data, _ := json.Marshal(result.Result)
			info, err = inspectComputerResult(data, response.ComputerSessionId, "exec")
			info.Outcome, info.ErrorCode = outcome, string(result.ErrorCode)
			if err != nil || info.ExitCode == nil || *info.ExitCode == 0 {
				return info, errComputerResponse
			}
		} else if result.Result != nil || result.ErrorCode != "not_found" && result.ErrorCode != "access_denied" {
			return info, errComputerResponse
		}
	case "timed_out":
		var result ComputerTimedOut
		if decodeComputerJSON(response.Execution.union, &result, "outcome", "termination") != nil || computerString(result.ErrorCode, "timed_out") != "timed_out" || result.Termination != "confirmed" && result.Termination != "unknown" {
			return info, errComputerResponse
		}
	case "unknown":
		var result ComputerUnknown
		if decodeComputerJSON(response.Execution.union, &result, "outcome", "error_code") != nil || computerString(result.Termination, "unknown") != "confirmed" && computerString(result.Termination, "unknown") != "unknown" {
			return info, errComputerResponse
		}
		switch result.ErrorCode {
		case "lost_ack", "executor_error", "invalid_executor_result", "owner_lost", "stopped", "start_evidence_failed":
		default:
			return info, errComputerResponse
		}
	default:
		return info, errComputerResponse
	}
	return info, nil
}

func inspectComputerResult(body []byte, id uuid.UUID, defaultKind ...string) (ComputerExecution, error) {
	var info ComputerExecution
	var discriminator struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(body, &discriminator) != nil {
		return info, errComputerResponse
	}
	info.Kind = discriminator.Kind
	if info.Kind == "" && len(defaultKind) == 1 {
		info.Kind = defaultKind[0]
	}
	switch info.Kind {
	case "exec":
		var result ComputerExecResult
		if decodeComputerJSON(body, &result, "exit_code") != nil || result.ExitCode < -255 || result.ExitCode > 255 {
			return info, errComputerResponse
		}
		stdout, err := DecodeComputerBytes(computerString(result.Stdout, ""), ComputerMaxOutputBytes)
		if err != nil {
			return info, err
		}
		stderr, err := DecodeComputerBytes(computerString(result.Stderr, ""), ComputerMaxOutputBytes-len(stdout))
		if err != nil {
			return info, err
		}
		info.Stdout, info.Stderr, info.ExitCode = stdout, stderr, &result.ExitCode
	case "file_read":
		var result ComputerFileReadResult
		if decodeComputerJSON(body, &result, "kind", "data") != nil {
			return info, errComputerResponse
		}
		data, err := DecodeComputerBytes(result.Data, ComputerMaxOutputBytes)
		if err != nil {
			return info, err
		}
		info.Data = data
	case "file_write":
		var result ComputerFileWriteResult
		if decodeComputerJSON(body, &result, "kind", "byte_count") != nil || result.ByteCount < 0 || result.ByteCount > ComputerMaxWriteBytes {
			return info, errComputerResponse
		}
	case "file_stat":
		var result ComputerFileStatResult
		if decodeComputerJSON(body, &result, "kind", "file_kind", "size_bytes") != nil || result.SizeBytes < 0 || result.FileKind != "file" && result.FileKind != "directory" {
			return info, errComputerResponse
		}
	case "process_list":
		var result ComputerProcessListResult
		if decodeComputerJSON(body, &result, "kind", "processes") != nil || len(result.Processes) > ComputerMaxItems {
			return info, errComputerResponse
		}
		seen := make(map[uuid.UUID]bool)
		for _, process := range result.Processes {
			if !validComputerProcess(process, id) || seen[process.Handle.HandleId] {
				return info, errComputerResponse
			}
			seen[process.Handle.HandleId] = true
		}
	case "process_inspect":
		var result ComputerProcessInspectResult
		if decodeComputerJSON(body, &result, "kind", "process") != nil || !validComputerProcess(result.Process, id) {
			return info, errComputerResponse
		}
	case "process_terminate":
		var result ComputerProcessTerminateResult
		if decodeComputerJSON(body, &result, "kind") != nil || computerString(result.Termination, "confirmed") != "confirmed" {
			return info, errComputerResponse
		}
	default:
		return info, errComputerResponse
	}
	return info, nil
}

func validComputerProcess(process ComputerProcessInfo, id uuid.UUID) bool {
	return process.Handle.ComputerSessionId == id && process.Handle.HandleId != uuid.Nil &&
		(process.State == "running" && process.ExitCode == nil || process.State == "exited") &&
		(process.ExitCode == nil || *process.ExitCode >= -255 && *process.ExitCode <= 255)
}
