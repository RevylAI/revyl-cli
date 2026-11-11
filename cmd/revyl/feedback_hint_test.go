package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/agentinfo"
	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/config"
)

func clearAgentEnvironment(t *testing.T) {
	t.Helper()
	original := commandBodyStarted
	commandBodyStarted = func() bool { return true }
	originalRecord := recordFeedbackHintShown
	recordFeedbackHintShown = func(*cobra.Command, string, string) {}
	t.Cleanup(func() {
		commandBodyStarted = original
		recordFeedbackHintShown = originalRecord
	})
	for _, name := range agentinfo.EnvironmentVariables {
		t.Setenv(name, "")
	}
}

type recordedHint struct {
	command   string
	commandID string
	kind      string
}

func captureRecordedHints(t *testing.T) *[]recordedHint {
	t.Helper()
	var recorded []recordedHint
	recordFeedbackHintShown = func(cmd *cobra.Command, commandID, kind string) {
		recorded = append(recorded, recordedHint{cmd.CommandPath(), commandID, kind})
	}
	return &recorded
}

func TestFrictionHintClaimsOncePerConversation(t *testing.T) {
	clearAgentEnvironment(t)
	t.Setenv("HOME", t.TempDir())
	start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

	steps := []struct {
		name      string
		at        time.Duration
		sessionID string
		want      bool
	}{
		{"first command on the machine", 0, "", true},
		{"same conversation keeps going", 5 * time.Minute, "", false},
		{"busy agent never goes idle", 64 * time.Minute, "", false},
		{"new conversation id", 65 * time.Minute, "thread-a", true},
		{"same conversation id", 66 * time.Minute, "thread-a", false},
		{"another conversation id", 67 * time.Minute, "thread-b", true},
		{"idle gap starts a new conversation", 67*time.Minute + agentConversationIdleGap, "thread-b", true},
		{"clock moved backwards", 30 * time.Minute, "thread-b", false},
	}
	for _, step := range steps {
		if got := claimFrictionHint(start.Add(step.at), step.sessionID); got != step.want {
			t.Fatalf("%s: claim = %v, want %v", step.name, got, step.want)
		}
	}
}

func TestFrictionHintShowsOncePerInterleavedConversation(t *testing.T) {
	clearAgentEnvironment(t)
	t.Setenv("HOME", t.TempDir())
	start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

	var hinted []string
	for minute, sessionID := range []string{"a", "b", "a", "b", "a", "b", "c", "a"} {
		if claimFrictionHint(start.Add(time.Duration(minute)*time.Minute), sessionID) {
			hinted = append(hinted, sessionID)
		}
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(hinted, want) {
		t.Fatalf("hinted conversations = %v, want %v", hinted, want)
	}

	if !claimFrictionHint(start.Add(8*time.Minute+agentConversationIdleGap), "b") {
		t.Fatal("a conversation idle past the gap must see the hint again")
	}
}

func TestFrictionHintSeparatesAgentsWithoutConversationIDs(t *testing.T) {
	clearAgentEnvironment(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDECODE", "1")
	command := &cobra.Command{Use: "run"}
	var claude bytes.Buffer
	printAgentFrictionHint(&claude, command)
	printAgentFrictionHint(&claude, command)

	t.Setenv("CLAUDECODE", "")
	t.Setenv("GEMINI_CLI", "1")
	var gemini bytes.Buffer
	printAgentFrictionHint(&gemini, command)

	if strings.Count(claude.String(), "\n") != 1 || strings.Count(gemini.String(), "\n") != 1 {
		t.Fatalf("claude hints %q, gemini hints %q; want one each", claude.String(), gemini.String())
	}
}

func TestFrictionHintRemembersABoundedNumberOfConversations(t *testing.T) {
	clearAgentEnvironment(t)
	t.Setenv("HOME", t.TempDir())
	start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

	for i := 0; i <= maxFrictionHintSessions; i++ {
		if !claimFrictionHint(start.Add(time.Duration(i)*time.Second), fmt.Sprintf("thread-%d", i)) {
			t.Fatalf("thread-%d: first command must see the hint", i)
		}
	}
	later := start.Add(time.Minute)
	if !claimFrictionHint(later, "thread-0") {
		t.Fatal("the oldest conversation must be evicted once the bound is reached")
	}
	if claimFrictionHint(later, fmt.Sprintf("thread-%d", maxFrictionHintSessions)) {
		t.Fatal("a recent conversation must stay remembered")
	}
}

func TestFrictionHintClaimStaysSilentWhileTheStateIsLocked(t *testing.T) {
	clearAgentEnvironment(t)
	t.Setenv("HOME", t.TempDir())
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	release, locked := lockUpdateNoticeState(revylStateFilePath(updateNoticeFile)+".lock", now)
	if !locked {
		t.Fatal("could not take the notice lock")
	}
	defer release()

	if claimFrictionHint(now, "thread-a") {
		t.Fatal("a contended state must not print")
	}
}

func TestFrictionHintShowsOnlyForAgentsOnCountedCommands(t *testing.T) {
	clearAgentEnvironment(t)
	recorded := captureRecordedHints(t)
	var claims []string
	original := claimFrictionHint
	claimFrictionHint = func(_ time.Time, sessionID string) bool {
		claims = append(claims, sessionID)
		return true
	}
	t.Cleanup(func() { claimFrictionHint = original })

	root := &cobra.Command{Use: "revyl"}
	root.Flags().Bool("version", false, "")
	device := &cobra.Command{Use: "device"}
	list := &cobra.Command{Use: "list"}
	device.AddCommand(list)
	auth := &cobra.Command{Use: "auth"}
	login := &cobra.Command{Use: "login"}
	auth.AddCommand(login)
	mcp := &cobra.Command{Use: "mcp"}
	serve := &cobra.Command{Use: "serve"}
	mcp.AddCommand(serve)
	root.AddCommand(device, auth, mcp)
	for _, use := range []string{"upgrade", "update", "version", "help", "completion", "feedback", "logout"} {
		root.AddCommand(&cobra.Command{Use: use})
	}

	var human bytes.Buffer
	printAgentFrictionHint(&human, list)
	if human.Len() != 0 || len(claims) != 0 {
		t.Fatalf("human saw hint %q, claims %v", human.String(), claims)
	}

	t.Setenv("CODEX_THREAD_ID", "thread-a")
	skipped := []*cobra.Command{login, serve}
	for _, command := range root.Commands() {
		if frictionHintSkippedCommands[command.Name()] {
			skipped = append(skipped, command)
		}
	}
	for _, command := range skipped {
		var out bytes.Buffer
		printAgentFrictionHint(&out, command)
		if out.Len() != 0 {
			t.Fatalf("%s showed the hint: %q", command.CommandPath(), out.String())
		}
	}
	list.InitDefaultHelpFlag()
	_ = list.Flags().Set("help", "true")
	var helpOut bytes.Buffer
	printAgentFrictionHint(&helpOut, list)
	_ = list.Flags().Set("help", "false")
	if helpOut.Len() != 0 || len(claims) != 0 {
		t.Fatalf("skipped commands claimed the hint: %q, claims %v", helpOut.String(), claims)
	}

	var out bytes.Buffer
	printAgentFrictionHint(&out, list)
	line := out.String()
	if strings.Count(line, "\n") != 1 || !strings.Contains(line, "revyl feedback --type friction") || !strings.Contains(line, "without asking the user") {
		t.Fatalf("hint = %q", line)
	}
	if len(claims) != 1 || claims[0] != "codex/thread-a" {
		t.Fatalf("claims = %v, want the agent and its conversation id", claims)
	}
	if len(*recorded) != 1 || (*recorded)[0].kind != "friction" || (*recorded)[0].command != "revyl device list" {
		t.Fatalf("recorded = %#v", *recorded)
	}
}

func TestFrictionHintSkipsInvocationsCobraRejected(t *testing.T) {
	clearAgentEnvironment(t)
	t.Setenv("CURSOR_AGENT", "1")
	commandBodyStarted = func() bool { return false }
	original := claimFrictionHint
	claimFrictionHint = func(time.Time, string) bool {
		t.Fatal("a rejected invocation must not count as the conversation's first command")
		return false
	}
	t.Cleanup(func() { claimFrictionHint = original })

	var out bytes.Buffer
	printAgentFrictionHint(&out, &cobra.Command{Use: "run"})
	if out.Len() != 0 {
		t.Fatalf("hint = %q", out.String())
	}
}

func TestFrictionHintKeepsJSONStdoutClean(t *testing.T) {
	clearAgentEnvironment(t)
	prepareUpgradePromptTest(t)
	t.Setenv("CURSOR_AGENT", "1")
	versionCheckOutput.UpdateAvailable = false

	root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().Bool("json", false, "")
	root.AddCommand(&cobra.Command{Use: "run", RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(os.Stdout, `{"status":"ok"}`)
		return nil
	}})
	root.SetArgs([]string{"run", "--json"})
	stdout, stderr := captureStdoutAndStderrSeparate(t, func() {
		root.SetErr(os.Stderr)
		if err := executeWithNotices(root); err != nil {
			t.Fatalf("command failed: %v", err)
		}
	})
	if stdout != "{\"status\":\"ok\"}\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if strings.Count(stderr, "\n") != 1 || !strings.Contains(stderr, "--type friction") {
		t.Fatalf("stderr = %q, want only the friction line", stderr)
	}
}

func TestAgentFeedbackHintRecordsTheFailureKind(t *testing.T) {
	clearAgentEnvironment(t)
	t.Setenv("CURSOR_AGENT", "1")
	recorded := captureRecordedHints(t)
	original := claimFeedbackHint
	claimFeedbackHint = func(time.Time) bool { return true }
	t.Cleanup(func() { claimFeedbackHint = original })

	var out bytes.Buffer
	printAgentFeedbackHint(&out, &cobra.Command{Use: "run"}, errors.New("server error"))
	if len(*recorded) != 1 || (*recorded)[0].kind != "failure" {
		t.Fatalf("recorded = %#v", *recorded)
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
