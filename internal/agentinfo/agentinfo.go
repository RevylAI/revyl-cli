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
	"regexp"
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

// EnvironmentVariables lists every variable Detect reads, so callers that must
// run without agent attribution can clear them all.
var EnvironmentVariables = []string{
	"REVYL_AGENT",
	"CODEX_SHELL", "CODEX_CI", "CODEX_THREAD_ID", "CODEX_INTERNAL_ORIGINATOR_OVERRIDE",
	"CURSOR_AGENT", "CURSOR_EXTENSION_HOST_ROLE",
	"CLAUDE_CODE_REMOTE", "CLAUDECODE",
	"GEMINI_CLI",
	"OPENCODE", "OPENCODE_SESSION_ID",
	"COPILOT_AGENT",
	"CLINE_ACTIVE",
}

// declaredAgentNamePattern bounds a self-declared REVYL_AGENT name, which is
// sent as a header and an analytics property.
var declaredAgentNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,39}$`)

// Detect inspects the process environment and returns the detected agent.
//
// Returns an AgentInfo whose Name is empty when no known agent runtime is
// detected (e.g. a human running the CLI directly in a terminal). A runtime
// marker wins over a self-declared REVYL_AGENT name, except REVYL_AGENT=revyl,
// which Revyl's own agent sets to override the markers it inherits.
func Detect() AgentInfo {
	declared := strings.ToLower(strings.TrimSpace(os.Getenv("REVYL_AGENT")))
	switch {
	case declared == "revyl":
		return AgentInfo{Name: "revyl"}
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
	case os.Getenv("GEMINI_CLI") == "1":
		return AgentInfo{Name: "gemini_cli"}
	case os.Getenv("OPENCODE") == "1":
		return AgentInfo{Name: "opencode", SessionID: strings.TrimSpace(os.Getenv("OPENCODE_SESSION_ID"))}
	case os.Getenv("COPILOT_AGENT") == "1":
		return AgentInfo{Name: "copilot"}
	case os.Getenv("CLINE_ACTIVE") == "true":
		return AgentInfo{Name: "cline"}
	case declaredAgentNamePattern.MatchString(declared):
		return AgentInfo{Name: declared}
	default:
		return AgentInfo{}
	}
}
