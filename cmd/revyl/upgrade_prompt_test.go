package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/testutil"
	"github.com/revyl/cli/internal/ui"
)

func prepareUpgradePromptTest(t *testing.T) {
	t.Helper()
	testutil.SetHomeDir(t, t.TempDir())
	for _, name := range []string{
		"CI", "GITHUB_ACTIONS", "GITLAB_CI", "CIRCLECI", "BUILDKITE", "JENKINS_URL",
		"CODEX_SHELL", "CODEX_CI", "CODEX_THREAD_ID", "CURSOR_AGENT", "CLAUDECODE", "CLAUDE_CODE_REMOTE",
		"REVYL_NO_UPDATE_NOTIFIER",
	} {
		t.Setenv(name, "")
	}
	oldTerminal, oldConfirm := upgradeTerminalAvailable, confirmInlineUpgrade
	oldInstall, oldVerify := installInlineUpgrade, verifyInlineUpgrade
	oldStarted, oldDone, oldOutput := versionCheckStarted, versionCheckDone, versionCheckOutput
	t.Cleanup(func() {
		upgradeTerminalAvailable, confirmInlineUpgrade = oldTerminal, oldConfirm
		installInlineUpgrade, verifyInlineUpgrade = oldInstall, oldVerify
		versionCheckStarted, versionCheckDone, versionCheckOutput = oldStarted, oldDone, oldOutput
	})
	upgradeTerminalAvailable = func() bool { return true }
	confirmInlineUpgrade = func(string, bool) (bool, error) { return false, nil }
	installInlineUpgrade = func(context.Context, versionCheckResult) (string, error) {
		t.Fatal("unexpected installation")
		return "", nil
	}
	verifyInlineUpgrade = func(context.Context, string, string) (string, error) {
		t.Fatal("unexpected version verification")
		return "", nil
	}
	versionCheckStarted = true
	versionCheckDone = make(chan struct{})
	close(versionCheckDone)
	versionCheckOutput = &versionCheckResult{LatestVersion: "v999.0.0", UpdateAvailable: true, InstallMethod: "direct"}
	ui.SetQuietMode(false)
}

func TestCanPromptForUpgradeExcludesAutomation(t *testing.T) {
	prepareUpgradePromptTest(t)
	if !canPromptForUpgrade() {
		t.Fatal("interactive human should be eligible")
	}
	for _, name := range []string{
		"CI", "GITHUB_ACTIONS", "GITLAB_CI", "CIRCLECI", "BUILDKITE", "JENKINS_URL",
		"CODEX_SHELL", "CODEX_CI", "CODEX_THREAD_ID", "CURSOR_AGENT", "CLAUDECODE", "CLAUDE_CODE_REMOTE",
	} {
		t.Run(name, func(t *testing.T) {
			value := "1"
			if name == "CLAUDE_CODE_REMOTE" {
				value = "true"
			}
			t.Setenv(name, value)
			if canPromptForUpgrade() {
				t.Fatalf("prompt allowed with %s", name)
			}
		})
	}
	upgradeTerminalAvailable = func() bool { return false }
	if canPromptForUpgrade() {
		t.Fatal("nonterminal invocation should not prompt")
	}
}

func TestVersionNoticeRespectsOutputAndCommandBoundaries(t *testing.T) {
	for _, name := range []string{"json", "quiet", "upgrade", "update", "version", "completion", "mcp", "disabled", "not_started", "current", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			prepareUpgradePromptTest(t)
			cmd := &cobra.Command{Use: "example"}
			cmd.Flags().Bool("json", name == "json", "")
			cmd.Flags().Bool("quiet", name == "quiet", "")
			switch name {
			case "upgrade", "update", "version", "completion", "mcp":
				cmd.Use = name
			case "disabled":
				t.Setenv("REVYL_NO_UPDATE_NOTIFIER", "1")
			case "not_started":
				versionCheckStarted = false
			case "current":
				versionCheckOutput.UpdateAvailable = false
			case "cancelled":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				cmd.SetContext(ctx)
			}
			confirmInlineUpgrade = func(string, bool) (bool, error) {
				t.Fatal("unexpected prompt")
				return false, nil
			}
			if output := captureStdoutAndStderr(t, func() { printVersionWarning(cmd) }); output != "" {
				t.Fatalf("unexpected output: %s", output)
			}
		})
	}
}

func TestShouldSkipVersionCheckForOutputModes(t *testing.T) {
	for _, test := range []struct {
		name       string
		jsonOutput bool
		quiet      bool
		wantSkip   bool
	}{
		{name: "human output"},
		{name: "json", jsonOutput: true, wantSkip: true},
		{name: "quiet", quiet: true, wantSkip: true},
		{name: "quiet json", jsonOutput: true, quiet: true, wantSkip: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "example"}
			cmd.Flags().Bool("json", test.jsonOutput, "")
			cmd.Flags().Bool("quiet", test.quiet, "")
			if got := shouldSkipVersionCheck(cmd); got != test.wantSkip {
				t.Fatalf("shouldSkipVersionCheck() = %v, want %v", got, test.wantSkip)
			}
		})
	}
}

func TestExecuteWithVersionNoticePreservesJSONOutput(t *testing.T) {
	upgradeRequired := &api.APIError{
		StatusCode: http.StatusUpgradeRequired,
		Code:       "cli_upgrade_required",
		Message:    "This Revyl CLI version is no longer compatible. Run 'revyl upgrade' and retry.",
	}
	for _, localJSONFlag := range []bool{false, true} {
		for _, args := range [][]string{
			{"--json", "example", "run"},
			{"example", "run", "--json=true"},
		} {
			for _, outcome := range []struct {
				name   string
				output string
				err    error
			}{
				{name: "success", output: `{"status":"ok"}`},
				{name: "upgrade required", output: `{"code":"cli_upgrade_required"}`, err: upgradeRequired},
			} {
				t.Run(fmt.Sprintf("local=%v/args=%s/%s", localJSONFlag, strings.Join(args, " "), outcome.name), func(t *testing.T) {
					prepareUpgradePromptTest(t)
					confirmInlineUpgrade = func(string, bool) (bool, error) {
						t.Fatal("JSON command must not prompt for an upgrade")
						return false, nil
					}
					root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
					root.PersistentFlags().Bool("json", false, "")
					group := &cobra.Command{Use: "example"}
					command := &cobra.Command{Use: "run", RunE: func(cmd *cobra.Command, args []string) error {
						if !shouldSkipVersionCheck(cmd) {
							t.Fatal("JSON command must skip the background version check")
						}
						fmt.Fprintln(os.Stdout, outcome.output)
						return outcome.err
					}}
					if localJSONFlag {
						command.Flags().Bool("json", false, "")
					}
					group.AddCommand(command)
					root.AddCommand(group)
					root.SetArgs(args)
					output := captureStdoutAndStderr(t, func() {
						if err := executeWithVersionNotice(root); err != outcome.err {
							t.Fatalf("command result changed: got %v, want %v", err, outcome.err)
						}
					})
					if output != outcome.output+"\n" {
						t.Fatalf("JSON output was altered: %q", output)
					}
				})
			}
		}
	}
}

func TestVersionNoticeKeepsPackageManagerGuidance(t *testing.T) {
	for method, command := range map[string]string{
		"npm": "npm update -g @revyl/cli", "pip": "pip install --upgrade revyl", "pipx": "pipx upgrade revyl",
	} {
		t.Run(method, func(t *testing.T) {
			prepareUpgradePromptTest(t)
			versionCheckOutput.InstallMethod = method
			confirmInlineUpgrade = func(string, bool) (bool, error) {
				t.Fatal("package-manager install must not prompt")
				return false, nil
			}
			output := captureStdoutAndStderr(t, func() { printVersionWarning(&cobra.Command{Use: "example"}) })
			if !strings.Contains(output, command) {
				t.Fatalf("missing upgrade guidance: %s", output)
			}
		})
	}
}

func TestPromptedUpgradeRecordsOutcomeWithoutSensitiveDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name       string
		confirm    bool
		promptErr  error
		installErr error
		verifyErr  error
		outcome    upgradePromptOutcome
	}{
		{name: "declined", outcome: upgradePromptDeclined},
		{name: "cancelled", promptErr: io.EOF, outcome: upgradePromptCancelled},
		{name: "install failed", confirm: true, installErr: errors.New("private installation details"), outcome: upgradePromptFailed},
		{name: "verification failed", confirm: true, verifyErr: errors.New("private verification details"), outcome: upgradePromptFailed},
		{name: "succeeded", confirm: true, outcome: upgradePromptSucceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			prepareUpgradePromptTest(t)
			confirmInlineUpgrade = func(message string, defaultYes bool) (bool, error) {
				if message != "Update now?" || !defaultYes {
					t.Fatalf("unexpected prompt: %q, %v", message, defaultYes)
				}
				return test.confirm, test.promptErr
			}
			installCalls, verifyCalls := 0, 0
			installInlineUpgrade = func(ctx context.Context, update versionCheckResult) (string, error) {
				installCalls++
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 10*time.Minute {
					t.Fatal("installation is not time bounded")
				}
				return "installed-revyl", test.installErr
			}
			verifyInlineUpgrade = func(ctx context.Context, path, expected string) (string, error) {
				verifyCalls++
				if path != "installed-revyl" || expected != "v999.0.0" {
					t.Fatalf("verification arguments = %q, %q", path, expected)
				}
				return "v999.0.0", test.verifyErr
			}
			var captured analytics.TelemetryPayload
			recorder := analytics.NewWithFlusher(analytics.Config{}, func(payload analytics.TelemetryPayload) { captured = payload })
			cmd := &cobra.Command{Use: "upgrade"}
			run := recorder.StartCommand(cmd, nil)
			cmd.SetContext(analytics.ContextWithCommandRun(context.Background(), run))
			err := runPromptedUpgrade(cmd, *versionCheckOutput)
			run.Complete(err)
			run.Flush()
			wantError := test.promptErr != nil || test.installErr != nil || test.verifyErr != nil
			if (err != nil) != wantError {
				t.Fatalf("error = %v, wantError = %v", err, wantError)
			}
			if !test.confirm && installCalls != 0 || test.installErr != nil && verifyCalls != 0 {
				t.Fatalf("unexpected calls: install=%d verify=%d", installCalls, verifyCalls)
			}
			if test.outcome == upgradePromptSucceeded && (installCalls != 1 || verifyCalls != 1) {
				t.Fatalf("expected one install and verification: %d, %d", installCalls, verifyCalls)
			}
			if len(captured.Events) != 2 {
				t.Fatalf("expected one start and terminal event: %+v", captured)
			}
			props := captured.Events[1].Properties
			if props["domain"] != "cli_upgrade" || props["domain_status"] != string(test.outcome) || props["selection_source"] != "prompted" {
				t.Fatalf("unexpected outcome metadata: %+v", props)
			}
			if wantError && props["error_message"] != "CLI upgrade failed" {
				t.Fatalf("unsafe diagnostics: %+v", props)
			}
		})
	}
}

func TestExecuteWithVersionNoticePreservesCommandResult(t *testing.T) {
	for _, failed := range []bool{false, true} {
		for _, accept := range []bool{false, true} {
			t.Run(fmt.Sprintf("failed=%v/accept=%v", failed, accept), func(t *testing.T) {
				prepareUpgradePromptTest(t)
				commandErr := errors.New("original command failed")
				runCalls, prompts := 0, 0
				root := &cobra.Command{Use: "example", SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
					runCalls++
					if failed {
						return commandErr
					}
					return nil
				}}
				root.SetArgs(nil)
				root.SetErr(io.Discard)
				confirmInlineUpgrade = func(string, bool) (bool, error) {
					prompts++
					if runCalls != 1 {
						t.Fatal("prompt ran before command completion")
					}
					return accept, nil
				}
				installInlineUpgrade = func(context.Context, versionCheckResult) (string, error) {
					return "", errors.New("upgrade failed independently")
				}
				err := executeWithVersionNotice(root)
				if failed && err != commandErr || !failed && err != nil {
					t.Fatalf("original result changed: %v", err)
				}
				if runCalls != 1 || prompts != 1 {
					t.Fatalf("runCalls=%d prompts=%d", runCalls, prompts)
				}
			})
		}
	}
}

func TestUpgradeProcessCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	cmd := exec.Command(os.Args[0], "-test.run=^TestUpgradeProcessHelper$")
	cmd.Env = append(os.Environ(), "REVYL_UPGRADE_PROCESS_HELPER=1")
	if err := runUpgradeProcess(ctx, cmd); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if cmd.ProcessState == nil {
		t.Fatal("cancelled child was not reaped")
	}
}

func TestPromptedUpgradeInheritsDevelopmentRouting(t *testing.T) {
	for _, devMode := range []bool{false, true} {
		t.Run(fmt.Sprintf("dev=%v", devMode), func(t *testing.T) {
			root := &cobra.Command{Use: "revyl"}
			root.PersistentFlags().Bool("dev", devMode, "")
			upgrade := &cobra.Command{Use: "upgrade"}
			root.AddCommand(upgrade)
			if commandDevMode(upgrade) != devMode {
				t.Fatal("prompted upgrade did not inherit development routing")
			}
		})
	}
}

func TestUpgradeProcessHelper(t *testing.T) {
	if os.Getenv("REVYL_UPGRADE_PROCESS_HELPER") != "1" {
		return
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestVerifyInstalledUpgrade(t *testing.T) {
	for _, test := range []struct {
		name      string
		output    string
		exitCode  string
		wantError bool
	}{
		{name: "installed", output: `{"version":"v999.0.0"}`},
		{name: "newer", output: `{"version":"999.1.0"}`},
		{name: "unchanged", output: `{"version":"0.1.96"}`, wantError: true},
		{name: "invalid", output: `not json`, wantError: true},
		{name: "missing", output: `{}`, wantError: true},
		{name: "failed", exitCode: "1", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("REVYL_UPGRADE_VERSION_HELPER", "1")
			t.Setenv("REVYL_UPGRADE_VERSION_OUTPUT", test.output)
			t.Setenv("REVYL_UPGRADE_VERSION_EXIT", test.exitCode)
			previous := upgradeVerificationCommand
			t.Cleanup(func() { upgradeVerificationCommand = previous })
			upgradeVerificationCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
				if name != "installed-revyl" || strings.Join(args, " ") != "version --json" {
					t.Fatalf("unexpected version command: %s %v", name, args)
				}
				return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUpgradeVersionHelper$")
			}
			actual, err := verifyInstalledUpgrade(context.Background(), "installed-revyl", "v999.0.0")
			if (err != nil) != test.wantError {
				t.Fatalf("verification = %q, %v", actual, err)
			}
		})
	}
}

func TestUpgradeVersionHelper(t *testing.T) {
	if os.Getenv("REVYL_UPGRADE_VERSION_HELPER") != "1" {
		return
	}
	if os.Getenv("REVYL_TELEMETRY_DISABLED") != "1" {
		os.Exit(2)
	}
	if os.Getenv("REVYL_UPGRADE_VERSION_EXIT") == "1" {
		os.Exit(1)
	}
	fmt.Print(os.Getenv("REVYL_UPGRADE_VERSION_OUTPUT"))
	os.Exit(0)
}
