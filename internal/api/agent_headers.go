package api

import (
	"net/http"

	"github.com/revyl/cli/internal/agentinfo"
)

// Agent attribution header names. The backend device-proxy forwards these to
// the worker so device actions can be attributed to the invoking agent in
// reports (rendered as "Agent Step: <name>"). Other backend endpoints ignore
// them, mirroring how X-CI-* headers are attached globally but only consumed
// where relevant.
const (
	headerAgentName      = "X-Revyl-Agent"
	headerAgentSessionID = "X-Revyl-Agent-Session-Id"
)

// setAgentHeaders attaches X-Revyl-Agent[-Session-Id] headers when the CLI is
// invoked by a known AI coding agent (codex, cursor, claude_code, ...).
//
// Detection is env-based via agentinfo.Detect(); when no agent is detected this
// is a no-op so direct/manual CLI usage carries no attribution and continues to
// render as "Manual".
func setAgentHeaders(req *http.Request) {
	agent := agentinfo.Detect()
	if agent.Name == "" {
		return
	}
	setIf(req, headerAgentName, agent.Name)
	setIf(req, headerAgentSessionID, agent.SessionID)
}
