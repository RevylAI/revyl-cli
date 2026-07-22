package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// BuildPreflightInfo preserves the legacy MCP build compatibility schema.
type BuildPreflightInfo struct {
	BuildClass  string   `json:"build_class"`
	Compatible  string   `json:"compatible"`
	Reason      string   `json:"reason,omitempty"`
	FixCommands []string `json:"fix_commands,omitempty"`
}

// StartDevLoopOutput preserves the flat core/full MCP startup contract.
type StartDevLoopOutput struct {
	Success            bool                `json:"success"`
	SessionIndex       int                 `json:"session_index"`
	ManualStepRequired bool                `json:"manual_step_required,omitempty"`
	DeepLinkURL        string              `json:"deep_link_url,omitempty"`
	ViewerURL          string              `json:"viewer_url,omitempty"`
	BuildSelection     string              `json:"build_selection,omitempty"`
	Preflight          *BuildPreflightInfo `json:"preflight,omitempty"`
	Warnings           []string            `json:"warnings,omitempty"`
	Error              string              `json:"error,omitempty"`
}

// StopDevLoopInput preserves the empty legacy stop input schema.
type StopDevLoopInput struct{}

// StopDevLoopOutput preserves the flat core/full MCP stop contract.
type StopDevLoopOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// handleStartDevLoopCompat adapts canonical startup to the legacy flat output.
func (s *Server) handleStartDevLoopCompat(
	ctx context.Context,
	req *mcp.CallToolRequest,
	input StartDevLoopInput,
) (*mcp.CallToolResult, StartDevLoopOutput, error) {
	toolResult, canonical, err := s.handleStartDevLoopCommand(ctx, req, input)
	output := StartDevLoopOutput{
		Success:      canonical.Success,
		SessionIndex: canonical.Result.SessionIndex,
		ViewerURL:    canonical.Result.ViewerURL,
		Error:        canonical.Error,
	}
	if canonical.Outcome.SessionIndex != nil {
		output.SessionIndex = *canonical.Outcome.SessionIndex
	}
	if output.ViewerURL == "" {
		output.ViewerURL = canonical.Outcome.ViewerURL
	}
	return toolResult, output, err
}

// handleStopDevLoopCompat adapts canonical cleanup to the legacy flat output.
func (s *Server) handleStopDevLoopCompat(
	ctx context.Context,
	req *mcp.CallToolRequest,
	input StopDevLoopInput,
) (*mcp.CallToolResult, StopDevLoopOutput, error) {
	toolResult, canonical, err := s.handleStopDevLoopCommand(ctx, req, CanonicalStopDevLoopInput{})
	output := StopDevLoopOutput{
		Success: canonical.Success,
		Error:   canonical.Error,
	}
	if canonical.Success {
		output.Message = "Dev loop stopped"
	}
	return toolResult, output, err
}
