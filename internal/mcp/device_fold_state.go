package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/revyl/cli/internal/api"
)

func (m *DeviceSessionManager) SetFoldStateOnSession(ctx context.Context, session *DeviceSession, state api.DeviceFoldState) error {
	if session == nil {
		return fmt.Errorf("no session resolved for set_fold_state")
	}
	if session.Platform != "ios" {
		return fmt.Errorf("set_fold_state is not supported on platform %s; select a multi-display iOS session (currently iPhone Duo)", session.Platform)
	}
	body, err := m.workerRequestForSession(ctx, session, "/set_fold_state", api.DeviceSetFoldStateRequest{FoldState: state})
	if err != nil {
		return fmt.Errorf("set_fold_state completion is unconfirmed: %w; inspect the same session before issuing another fold action", err)
	}
	if err := EnsureWorkerActionSucceeded(body, "set_fold_state"); err != nil {
		return fmt.Errorf("set_fold_state was not confirmed: %w; fold control requires a multi-display iOS device (currently iPhone Duo); inspect the same session before issuing another fold action", err)
	}
	return nil
}

type DeviceSetFoldStateInput struct {
	FoldState    string `json:"fold_state" jsonschema:"Fold state: folded or unfolded"`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID; never falls back to the active session. If session_index is also supplied, both must identify the same session."`
}

type DeviceSetFoldStateOutput struct {
	Success   bool                `json:"success"`
	FoldState api.DeviceFoldState `json:"fold_state,omitempty"`
	Error     string              `json:"error,omitempty"`
	NextSteps []NextStep          `json:"next_steps,omitempty"`
}

func (s *Server) registerDeviceSetFoldStateTool() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "device_set_fold_state",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"fold_state":    map[string]any{"type": "string", "enum": []string{"folded", "unfolded"}},
				"session_index": map[string]any{"type": "integer", "description": "Session index to target. Omit for active session."},
				"session_id":    map[string]any{"type": "string", "description": "Server-issued session ID; never falls back to the active session. If session_index is also supplied, both must identify the same session."},
			},
			"required":             []string{"fold_state"},
			"additionalProperties": false,
		},
		Description: "Fold or unfold a running multi-display iOS device (currently iPhone Duo), without creating a test. Waits for worker confirmation. If completion is unconfirmed, inspect the same session before another action.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Set Fold State",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceSetFoldState)
}

func (s *Server) handleDeviceSetFoldState(ctx context.Context, req *mcp.CallToolRequest, input DeviceSetFoldStateInput) (*mcp.CallToolResult, DeviceSetFoldStateOutput, error) {
	state, err := api.ParseDeviceFoldState(input.FoldState)
	if err != nil {
		return nil, DeviceSetFoldStateOutput{Error: err.Error()}, nil
	}
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err == nil {
		err = s.sessionMgr.SetFoldStateOnSession(ctx, session, state)
	}
	if err != nil {
		return nil, DeviceSetFoldStateOutput{Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	screenshotTarget := fmt.Sprintf("session_id=%q", session.SessionID)
	if session.SessionID == "" {
		screenshotTarget = fmt.Sprintf("session_index=%d", session.Index)
	}
	return nil, DeviceSetFoldStateOutput{
		Success: true, FoldState: state,
		NextSteps: []NextStep{{Tool: "screenshot", Params: screenshotTarget, Reason: "Inspect the same session after the fold state change"}},
	}, nil
}
