package tui

import (
	"strings"
	"testing"
)

func TestStartMonitoredExecutionRequiresCanonicalProject(t *testing.T) {
	t.Chdir(initializeSettingsGitWorktree(t))

	msgAny := startMonitoredExecutionCmd("test-1", "Checkout", "token", false)()
	msg, ok := msgAny.(ExecutionDoneMsg)
	if !ok {
		t.Fatalf("message type = %T, want ExecutionDoneMsg", msgAny)
	}
	if msg.Err == nil || !strings.Contains(msg.Err.Error(), "revyl init") {
		t.Fatalf("msg.Err = %v, want actionable project initialization error", msg.Err)
	}
}
