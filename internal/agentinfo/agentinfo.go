// Package agentinfo detects the AI coding agent (if any) that is driving the
// current CLI process from well-known environment variables.
//
// This is the single source of truth for agent attribution. It is intentionally
// dependency-free so it can be imported by both the analytics recorder (for
// telemetry base properties) and the API client (for device-action report
// attribution) without creating an import cycle.
package agentinfo

import (
	"os"
	"strings"
)

// AgentInfo describes the agent that invoked the CLI, derived from environment
// variables set by the host agent runtime.
type AgentInfo struct {
	// Name is the canonical agent identifier (e.g. "codex", "cursor",
	// "claude_code"). Empty when no agent is detected.
	Name string
	// SessionID is the agent's session/thread identifier when available.
	SessionID string
	// Originator is an optional origin hint exposed by the agent runtime.
	Originator string
	// Remote indicates the agent is running in a remote/hosted context.
	Remote bool
}

// Detect inspects the process environment and returns the detected agent.
//
// Returns an AgentInfo whose Name is empty when no known agent runtime is
// detected (e.g. a human running the CLI directly in a terminal).
func Detect() AgentInfo {
	switch {
	case os.Getenv("CODEX_SHELL") != "" || os.Getenv("CODEX_CI") != "" || strings.TrimSpace(os.Getenv("CODEX_THREAD_ID")) != "":
		return AgentInfo{
			Name:       "codex",
			SessionID:  strings.TrimSpace(os.Getenv("CODEX_THREAD_ID")),
			Originator: strings.TrimSpace(os.Getenv("CODEX_INTERNAL_ORIGINATOR_OVERRIDE")),
		}
	case os.Getenv("CURSOR_AGENT") == "1":
		return AgentInfo{
			Name:       "cursor",
			Originator: strings.TrimSpace(os.Getenv("CURSOR_EXTENSION_HOST_ROLE")),
		}
	case os.Getenv("CLAUDE_CODE_REMOTE") == "true":
		return AgentInfo{Name: "claude_code", Remote: true}
	case os.Getenv("CLAUDECODE") != "":
		return AgentInfo{Name: "claude_code"}
	default:
		return AgentInfo{}
	}
}
