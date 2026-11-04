package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/config"
)

func clearAgentEnvironment(t *testing.T) {
	t.Helper()
	original := commandBodyStarted
	commandBodyStarted = func() bool { return true }
	t.Cleanup(func() { commandBodyStarted = original })
	for _, name := range []string{"REVYL_AGENT", "CODEX_SHELL", "CODEX_CI", "CODEX_THREAD_ID", "CURSOR_AGENT", "CLAUDE_CODE_REMOTE", "CLAUDECODE"} {
		t.Setenv(name, "")
	}
}

func TestAgentFeedbackHintShowsOnlyForAgentsOncePerInterval(t *testing.T) {
	clearAgentEnvironment(t)
	t.Setenv("HOME", t.TempDir())
	failed := &cobra.Command{Use: "rebuild"}
	commandErr := errors.New("rebuild failed")

	var human bytes.Buffer
	printAgentFeedbackHint(&human, failed, commandErr)
	if human.Len() != 0 {
		t.Fatalf("human saw hint: %q", human.String())
	}

	t.Setenv("CLAUDECODE", "1")
	var first, second bytes.Buffer
	printAgentFeedbackHint(&first, failed, commandErr)
	if !strings.Contains(first.String(), "revyl feedback --type bug") || !strings.Contains(first.String(), "without asking the user") {
		t.Fatalf("first hint = %q", first.String())
	}
	printAgentFeedbackHint(&second, failed, commandErr)
	if second.Len() != 0 {
		t.Fatalf("repeated hint inside interval: %q", second.String())
	}
	if !claimNotice(time.Now().Add(feedbackHintInterval+time.Minute), feedbackHintInterval, feedbackHintShownAt) {
		t.Fatal("hint should be claimable after the interval")
	}
}

func TestAgentFeedbackHintSkipsUserResolvedFailuresAndFeedbackItself(t *testing.T) {
	clearAgentEnvironment(t)
	t.Setenv("CURSOR_AGENT", "1")
	original := claimFeedbackHint
	claimFeedbackHint = func(time.Time) bool { return true }
	t.Cleanup(func() { claimFeedbackHint = original })

	cases := map[string]struct {
		cmd *cobra.Command
		err error
	}{
		"success":   {&cobra.Command{Use: "rebuild"}, nil},
		"feedback":  {&cobra.Command{Use: "feedback"}, errors.New("send failed")},
		"cancelled": {&cobra.Command{Use: "rebuild"}, analytics.WithFailureClass(errors.New("x"), analytics.FailureClassCancelled)},
		"auth":      {&cobra.Command{Use: "rebuild"}, analytics.WithFailureClass(errors.New("x"), analytics.FailureClassAuth)},
		"quota":     {&cobra.Command{Use: "rebuild"}, analytics.WithFailureClass(errors.New("x"), analytics.FailureClassQuota)},
		"computer":  {&cobra.Command{Use: "ssh"}, &computerExitError{err: errors.New("exit 2"), code: 2}},
		"no project": {&cobra.Command{Use: "list"}, &configDiagnosticError{
			cause:   &config.ConfigError{Stage: "read", Code: "config_not_found"},
			message: "no .revyl/config.yaml applies to the current directory",
		}},
		"missing root": {&cobra.Command{Use: "list"}, &config.MissingProjectRootError{WorkingDirectory: "/repo"}},
		"failed test verdict": {&cobra.Command{Use: "run"}, analytics.CompletedWithExitCode(errors.New("test failed"), analytics.CommandCompletion{
			ExitCode: 1, Domain: "test_run", DomainStatus: "failed",
		})},
		"failed workflow verdict": {&cobra.Command{Use: "run"}, analytics.CompletedWithExitCode(errors.New("workflow failed"), analytics.CommandCompletion{
			ExitCode: 1, Domain: "workflow_run", DomainStatus: "failed",
		})},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			printAgentFeedbackHint(&out, tc.cmd, tc.err)
			if out.Len() != 0 {
				t.Fatalf("hint = %q", out.String())
			}
		})
	}
}

func TestAgentFeedbackHintSkipsInvocationsCobraRejected(t *testing.T) {
	clearAgentEnvironment(t)
	t.Setenv("CURSOR_AGENT", "1")
	commandBodyStarted = func() bool { return false }
	original := claimFeedbackHint
	claimFeedbackHint = func(time.Time) bool { return true }
	t.Cleanup(func() { claimFeedbackHint = original })

	var out bytes.Buffer
	printAgentFeedbackHint(&out, &cobra.Command{Use: "run"}, errors.New("unknown flag: --latest-build"))
	if out.Len() != 0 {
		t.Fatalf("hint = %q", out.String())
	}
}

func TestAgentFeedbackHintShowsForFailuresRevylMayHaveCaused(t *testing.T) {
	clearAgentEnvironment(t)
	t.Setenv("CURSOR_AGENT", "1")
	original := claimFeedbackHint
	claimFeedbackHint = func(time.Time) bool { return true }
	t.Cleanup(func() { claimFeedbackHint = original })

	cases := map[string]error{
		"config validation": &configDiagnosticError{
			cause:   &config.ConfigError{Stage: "validation", Code: "unknown_field", Path: []string{"build", "profiles", "development", "iOS"}},
			message: "Invalid configuration",
		},
		"test timed out": analytics.CompletedWithExitCode(errors.New("test timed out"), analytics.CommandCompletion{
			ExitCode: 1, Domain: "test_run", DomainStatus: "timeout",
		}),
		"server error": errors.New("simulated server error"),
	}
	for name, err := range cases {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			printAgentFeedbackHint(&out, &cobra.Command{Use: "run"}, err)
			if !strings.Contains(out.String(), "revyl feedback --type bug") {
				t.Fatalf("hint = %q", out.String())
			}
		})
	}
}
