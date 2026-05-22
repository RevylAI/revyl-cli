// Package mcp: MCP tools for inspecting finished test runs.
//
// Mirrors the `revyl run {summary,identity,state}` CLI surface so
// agents can call the same orientation primitives without shelling
// out. Input/output structs are shared with the CLI via the
// runinspect package — adding a field threads through both surfaces.

package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/revyl/cli/internal/runinspect"
)

// registerRunInspectTools wires the run-inspection tools into the
// MCP server. Called from registerTools at startup.
func (s *Server) registerRunInspectTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_run_summary",
		Description: "Get a finished test run's outcome, step list, and identity highlights (who was logged in, what org). Pass the task_id (= execution_id) returned when the run started. Returns step indices (1-based), per-step status, available artifact pointers, and a small set of identity highlights — call get_run_identity for the full dump.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Get Run Summary",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleGetRunSummary)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_run_identity",
		Description: "Identify who was logged in during the run, what org/account they were in, which role flags they had, and what vendor analytics IDs (Statsig, Segment, Sentry, Datadog, Sardine, Intercom, etc.) were active. Use this to debug permission/role failures or pivot to vendor dashboards.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Get Run Identity",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleGetRunIdentity)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_run_state",
		Description: "Inspect the captured UserDefaults / SQLite for a finished run. Without 'path', lists every captured file (plist or sqlite). With 'path', returns the latest captured snapshot for that file — plist values dict, or sqlite per-table schema + row count. Escape hatch for the long tail when get_run_summary / get_run_identity don't have what you need.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Get Run State",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleGetRunState)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_run_traces",
		Description: "Surface backend-pivotable trace IDs that the app's observability SDK (currently Sentry) was about to ship at the time of the run. Each record carries an event_id / trace_id / replay_id pastable into Sentry's UI plus a step range and the on-disk envelope it came from. Output is deduplicated by (vendor, kind, value); the same trace seen across multiple envelopes appears once with first/last step seen.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Get Run Traces",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleGetRunTraces)
}

// --- get_run_summary ------------------------------------------------------

// GetRunSummaryInput is the input to get_run_summary.
type GetRunSummaryInput struct {
	TaskID string `json:"task_id" jsonschema:"Execution ID returned when the run started (REQUIRED)."`
}

// GetRunSummaryOutput wraps the shared runinspect.Summary with the
// MCP-call envelope (Success / Error). The summary fields are
// embedded so the wire format stays flat for the agent.
type GetRunSummaryOutput struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	*runinspect.Summary
}

func (s *Server) handleGetRunSummary(
	ctx context.Context, _ *mcp.CallToolRequest, input GetRunSummaryInput,
) (*mcp.CallToolResult, GetRunSummaryOutput, error) {
	if input.TaskID == "" {
		return nil, GetRunSummaryOutput{Error: "task_id is required"}, nil
	}
	report, lines, err := runinspect.LoadArtifacts(ctx, s.apiClient, input.TaskID)
	if err != nil {
		return nil, GetRunSummaryOutput{Error: err.Error()}, nil
	}
	identity := runinspect.DetectIdentityHighlights(lines, report)
	summary := runinspect.BuildSummary(report, identity)
	return nil, GetRunSummaryOutput{Success: true, Summary: &summary}, nil
}

// --- get_run_identity -----------------------------------------------------

// GetRunIdentityInput is the input to get_run_identity.
type GetRunIdentityInput struct {
	TaskID string `json:"task_id" jsonschema:"Execution ID returned when the run started (REQUIRED)."`
	AtStep int    `json:"at_step,omitempty" jsonschema:"Only show identity visible as of this 1-indexed step. Default 0 = all steps."`
}

// GetRunIdentityOutput is the get_run_identity result shape.
type GetRunIdentityOutput struct {
	Success  bool                       `json:"success"`
	Error    string                     `json:"error,omitempty"`
	TaskID   string                     `json:"task_id,omitempty"`
	Identity []runinspect.IdentityField `json:"identity"`
}

func (s *Server) handleGetRunIdentity(
	ctx context.Context, _ *mcp.CallToolRequest, input GetRunIdentityInput,
) (*mcp.CallToolResult, GetRunIdentityOutput, error) {
	if input.TaskID == "" {
		return nil, GetRunIdentityOutput{Error: "task_id is required"}, nil
	}
	report, lines, err := runinspect.LoadArtifacts(ctx, s.apiClient, input.TaskID)
	if err != nil {
		return nil, GetRunIdentityOutput{Error: err.Error()}, nil
	}
	identity := runinspect.DetectIdentity(
		lines,
		runinspect.IndexerFromReport(report),
		runinspect.IdentityOptions{AtStep: input.AtStep},
	)
	return nil, GetRunIdentityOutput{
		Success:  true,
		TaskID:   input.TaskID,
		Identity: identity,
	}, nil
}

// --- get_run_state --------------------------------------------------------

// GetRunStateInput is the input to get_run_state.
type GetRunStateInput struct {
	TaskID string `json:"task_id" jsonschema:"Execution ID returned when the run started (REQUIRED)."`
	Path   string `json:"path,omitempty" jsonschema:"Container-relative path of a captured plist or sqlite file. Omit to list all paths."`
	AtStep int    `json:"at_step,omitempty" jsonschema:"Show state as of this 1-indexed step. Default 0 = latest."`
}

// GetRunStateOutput is the get_run_state result shape.
type GetRunStateOutput struct {
	Success  bool                      `json:"success"`
	Error    string                    `json:"error,omitempty"`
	TaskID   string                    `json:"task_id,omitempty"`
	Path     string                    `json:"path,omitempty"`
	Paths    []runinspect.CapturedPath `json:"paths,omitempty"`
	Snapshot *runinspect.PathSnapshot  `json:"snapshot,omitempty"`
}

func (s *Server) handleGetRunState(
	ctx context.Context, _ *mcp.CallToolRequest, input GetRunStateInput,
) (*mcp.CallToolResult, GetRunStateOutput, error) {
	if input.TaskID == "" {
		return nil, GetRunStateOutput{Error: "task_id is required"}, nil
	}
	report, lines, err := runinspect.LoadArtifacts(ctx, s.apiClient, input.TaskID)
	if err != nil {
		return nil, GetRunStateOutput{Error: err.Error()}, nil
	}
	indexer := runinspect.IndexerFromReport(report)
	if input.Path == "" {
		paths := runinspect.ListCapturedPaths(lines, indexer, input.AtStep)
		return nil, GetRunStateOutput{
			Success: true,
			TaskID:  input.TaskID,
			Paths:   paths,
		}, nil
	}
	snap := runinspect.LatestStateForPath(lines, indexer, input.Path, input.AtStep)
	if snap == nil {
		return nil, GetRunStateOutput{
			Success: false,
			TaskID:  input.TaskID,
			Path:    input.Path,
			Error:   fmt.Sprintf("no captured state for path %q in this run", input.Path),
		}, nil
	}
	return nil, GetRunStateOutput{
		Success:  true,
		TaskID:   input.TaskID,
		Path:     input.Path,
		Snapshot: snap,
	}, nil
}

// --- get_run_traces ------------------------------------------------------

// GetRunTracesInput is the input to get_run_traces.
type GetRunTracesInput struct {
	TaskID  string   `json:"task_id" jsonschema:"Execution ID returned when the run started (REQUIRED)."`
	AtStep  int      `json:"at_step,omitempty" jsonschema:"Only show traces visible as of this 1-indexed step. Default 0 = all steps."`
	Vendors []string `json:"vendors,omitempty" jsonschema:"Restrict to vendors (currently only 'sentry' is implemented)."`
}

// GetRunTracesOutput is the get_run_traces result shape.
type GetRunTracesOutput struct {
	Success bool                     `json:"success"`
	Error   string                   `json:"error,omitempty"`
	TaskID  string                   `json:"task_id,omitempty"`
	Traces  []runinspect.TraceRecord `json:"traces"`
}

func (s *Server) handleGetRunTraces(
	ctx context.Context, _ *mcp.CallToolRequest, input GetRunTracesInput,
) (*mcp.CallToolResult, GetRunTracesOutput, error) {
	if input.TaskID == "" {
		return nil, GetRunTracesOutput{Error: "task_id is required"}, nil
	}
	report, lines, err := runinspect.LoadArtifacts(ctx, s.apiClient, input.TaskID)
	if err != nil {
		return nil, GetRunTracesOutput{Error: err.Error()}, nil
	}
	opts := runinspect.TracesOptions{AtStep: input.AtStep}
	for _, v := range input.Vendors {
		opts.Vendors = append(opts.Vendors, runinspect.TraceVendor(v))
	}
	traces := runinspect.DetectTraces(
		lines, runinspect.IndexerFromReport(report), opts,
	)
	return nil, GetRunTracesOutput{
		Success: true,
		TaskID:  input.TaskID,
		Traces:  traces,
	}, nil
}
