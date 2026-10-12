package main

import (
	"fmt"
	"strings"

	"github.com/revyl/cli/internal/toolcatalog"
)

type toolRecovery struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
}

type investigationError struct {
	Code           string         `json:"code"`
	Tool           string         `json:"tool,omitempty"`
	Message        string         `json:"message"`
	RetryUnchanged bool           `json:"retry_unchanged"`
	LimitBytes     int            `json:"limit_bytes,omitempty"`
	Recovery       []toolRecovery `json:"recovery"`
}

func (e *investigationError) Error() string { return e.Message }

func toolFailure(tool toolcatalog.Definition, input map[string]any, code, message string) *investigationError {
	failure := &investigationError{Code: code, Tool: tool.Name, Message: message, Recovery: []toolRecovery{}}
	if code != "result_too_large" {
		return failure
	}
	failure.LimitBytes = toolOutputLimitBytes
	reference, kind := "", "execution"
	switch tool.Name {
	case "sessions.report":
		reference, _ = input["session_id"].(string)
		kind = "session"
	case "reports.get":
		reference, _ = input["execution_id"].(string)
	case "reports.inspect":
		reference, _ = input["reference_id"].(string)
		if value, ok := input["id_kind"].(string); ok {
			kind = value
		}
	}
	if reference != "" && tool.Name != "reports.inspect" {
		failure.Recovery = append(failure.Recovery, toolRecovery{Tool: "reports.inspect", Arguments: map[string]any{"reference_id": reference, "id_kind": kind, "section": "overview"}})
		failure.Message = "Report output exceeded the budget. Read its overview, then request filtered steps/actions with reports.inspect. The overview is not the complete report."
	} else {
		failure.Message = "Tool output exceeded its budget. Narrow the query, reduce limit/tail, or read an omitted report field in chunks; no partial result was accepted."
	}
	return failure
}

func classifyInvestigationFailure(stderr string) (string, string) {
	hint := investigationFailureHint(stderr)
	switch {
	case strings.HasPrefix(hint, "query timed out"):
		return "timeout", hint
	case strings.HasPrefix(hint, "backend read failed"):
		return "backend_error", hint
	case strings.HasPrefix(hint, "backend unavailable"):
		return "backend_unavailable", hint
	case strings.HasPrefix(hint, "authentication unavailable"):
		return "authentication_required", hint
	case strings.HasPrefix(hint, "resource unavailable"):
		return "resource_unavailable", hint
	case strings.HasPrefix(hint, "this diagnostic"):
		return "evidence_unavailable", hint
	default:
		return "command_failed", fmt.Sprintf("%s; use tools describe to check accepted arguments", hint)
	}
}
