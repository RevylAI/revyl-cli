package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/revyl/cli/internal/toolcatalog"
)

// toolInputRefusal matches the refusals the CLI and the Atlas API give for a
// caller's own arguments. Each names a position or a limit, never a value, so
// the matched text alone is safe to return to the caller.
var toolInputRefusal = regexp.MustCompile(
	`provide a test name or ID, or use --branch or --git-commit-sha|` +
		`--since must be a whole number of hours or days above zero, such as 24h or 7d|` +
		`at most \d+ launch filters are allowed|a scope may combine at most \d+ launch filters|` +
		`launch filter \d+ (?:must be KEY=VALUE or KEY with a key of at most 256 characters and no colon|has an invalid value)|` +
		`atlas edge --runs does not apply launch-variable filters; drop --launch to read it unfiltered, or use a graph read such as atlas screen or atlas graph|` +
		`Launch filter term \d+ names a secret variable; secret launch variables can be filtered only by whether they were set, so send the key alone`,
)

// atlasArgumentConflict matches the CLI's refusals of two scope arguments
// that exclude each other. They name flags only, never values.
var atlasArgumentConflict = regexp.MustCompile(
	`--([a-z-]+) cannot be combined with --([a-z-]+)`,
)

func toolArgumentName(flag string) string {
	return strings.ReplaceAll(flag, "-", "_")
}

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
	if tool.Name == "atlas.graph" {
		failure.Message = "The graph exceeded the output budget; limit caps screens but not their relationships. Start with atlas.brief, read one product area with atlas.area, or scope atlas.graph to a build, report, test or time range."
	} else if reference != "" && tool.Name != "reports.inspect" {
		failure.Recovery = append(failure.Recovery, toolRecovery{Tool: "reports.inspect", Arguments: map[string]any{"reference_id": reference, "id_kind": kind, "section": "overview"}})
		failure.Message = "Report output exceeded the budget. Read its overview, then request filtered steps/actions with reports.inspect. The overview is not the complete report."
	} else {
		failure.Message = "Tool output exceeded its budget. Narrow the query, reduce limit/tail, or read an omitted report field in chunks; no partial result was accepted."
	}
	return failure
}

func classifyInvestigationFailure(stderr string) (string, string) {
	if refusal := toolInputRefusal.FindString(stderr); refusal != "" {
		return "invalid_arguments", refusal
	}
	if conflict := atlasArgumentConflict.FindStringSubmatch(stderr); conflict != nil {
		return "invalid_arguments", fmt.Sprintf("%s cannot be combined with %s; pass only one of them", toolArgumentName(conflict[1]), toolArgumentName(conflict[2]))
	}
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
