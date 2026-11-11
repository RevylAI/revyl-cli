package agentinfo

import (
	"strings"
	"testing"
)

func TestDetect(t *testing.T) {
	for _, key := range EnvironmentVariables {
		t.Setenv(key, "")
	}
	for _, test := range []struct {
		name string
		env  map[string]string
		want AgentInfo
	}{
		{name: "manual"},
		{name: "revyl", env: map[string]string{"REVYL_AGENT": "revyl"}, want: AgentInfo{Name: "revyl"}},
		{name: "normalized revyl", env: map[string]string{"REVYL_AGENT": " Revyl "}, want: AgentInfo{Name: "revyl"}},
		{name: "declared agent", env: map[string]string{"REVYL_AGENT": " Windsurf "}, want: AgentInfo{Name: "windsurf"}},
		{name: "declared agent with version", env: map[string]string{"REVYL_AGENT": "amp_1.2-beta"}, want: AgentInfo{Name: "amp_1.2-beta"}},
		{name: "unsafe declared name", env: map[string]string{"REVYL_AGENT": "my agent; rm"}},
		{name: "overlong declared name", env: map[string]string{"REVYL_AGENT": strings.Repeat("a", 41)}},
		{name: "blank override", env: map[string]string{"REVYL_AGENT": " "}},
		{name: "runtime marker wins over declared name", env: map[string]string{"REVYL_AGENT": "unknown", "CURSOR_AGENT": "1"}, want: AgentInfo{Name: "cursor"}},
		{name: "revyl overrides inherited agents without borrowing session", env: map[string]string{
			"REVYL_AGENT": "revyl", "CODEX_SHELL": "1", "CODEX_THREAD_ID": "outer-session",
			"CODEX_INTERNAL_ORIGINATOR_OVERRIDE": "outer-origin", "CURSOR_AGENT": "1", "CLAUDECODE": "1",
		}, want: AgentInfo{Name: "revyl"}},
		{name: "codex shell", env: map[string]string{"CODEX_SHELL": "1"}, want: AgentInfo{Name: "codex"}},
		{name: "codex ci", env: map[string]string{"CODEX_CI": "1"}, want: AgentInfo{Name: "codex"}},
		{name: "codex session", env: map[string]string{"CODEX_THREAD_ID": " session ", "CODEX_INTERNAL_ORIGINATOR_OVERRIDE": " origin "}, want: AgentInfo{Name: "codex", SessionID: "session", Originator: "origin"}},
		{name: "cursor", env: map[string]string{"CURSOR_AGENT": "1", "CURSOR_EXTENSION_HOST_ROLE": " role "}, want: AgentInfo{Name: "cursor", Originator: "role"}},
		{name: "cursor requires one", env: map[string]string{"CURSOR_AGENT": "true"}},
		{name: "claude local", env: map[string]string{"CLAUDECODE": "1"}, want: AgentInfo{Name: "claude_code"}},
		{name: "claude remote", env: map[string]string{"CLAUDE_CODE_REMOTE": "true"}, want: AgentInfo{Name: "claude_code", Remote: true}},
		{name: "gemini cli", env: map[string]string{"GEMINI_CLI": "1"}, want: AgentInfo{Name: "gemini_cli"}},
		{name: "opencode session", env: map[string]string{"OPENCODE": "1", "OPENCODE_SESSION_ID": " ses_1 "}, want: AgentInfo{Name: "opencode", SessionID: "ses_1"}},
		{name: "opencode session alone is not a marker", env: map[string]string{"OPENCODE_SESSION_ID": "ses_1"}},
		{name: "vs code copilot", env: map[string]string{"COPILOT_AGENT": "1"}, want: AgentInfo{Name: "copilot"}},
		{name: "cline", env: map[string]string{"CLINE_ACTIVE": "true"}, want: AgentInfo{Name: "cline"}},
		{name: "cline requires true", env: map[string]string{"CLINE_ACTIVE": "1"}},
		{name: "legacy precedence", env: map[string]string{"CODEX_CI": "1", "CURSOR_AGENT": "1", "CLAUDECODE": "1", "GEMINI_CLI": "1"}, want: AgentInfo{Name: "codex"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			if got := Detect(); got != test.want {
				t.Fatalf("Detect() = %#v, want %#v", got, test.want)
			}
		})
	}
}
