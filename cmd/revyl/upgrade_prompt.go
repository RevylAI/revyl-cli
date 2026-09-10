package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/revyl/cli/internal/agentinfo"
	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/ui"
)

type upgradePromptOutcome string

const (
	upgradePromptDeclined  upgradePromptOutcome = "declined"
	upgradePromptCancelled upgradePromptOutcome = "cancelled"
	upgradePromptFailed    upgradePromptOutcome = "failed"
	upgradePromptSucceeded upgradePromptOutcome = "succeeded"
)

var (
	upgradeTerminalAvailable = func() bool {
		return ui.IsInputTTY() && term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
	}
	confirmInlineUpgrade       = ui.PromptConfirm
	installInlineUpgrade       = installPromptedUpgrade
	verifyInlineUpgrade        = verifyInstalledUpgrade
	upgradeVerificationCommand = exec.CommandContext
)

func canPromptForUpgrade() bool {
	if !upgradeTerminalAvailable() || agentinfo.Detect().Name != "" {
		return false
	}
	for _, name := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "CIRCLECI", "BUILDKITE", "JENKINS_URL"} {
		if os.Getenv(name) != "" {
			return false
		}
	}
	return true
}

func runPromptedUpgrade(cmd *cobra.Command, update versionCheckResult) (err error) {
	outcome := upgradePromptCancelled
	defer func() {
		analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{
			Domain:       "cli_upgrade",
			DomainStatus: string(outcome),
			Properties:   map[string]interface{}{"selection_source": "prompted"},
		})
		err = analytics.WithSafeDiagnostic(err, "CLI upgrade failed")
	}()

	confirmed, err := confirmInlineUpgrade("Update now?", true)
	if err != nil {
		return fmt.Errorf("could not read update confirmation; run 'revyl upgrade' when ready: %w", err)
	}
	if !confirmed {
		outcome = upgradePromptDeclined
		ui.PrintDim("Update later with: revyl upgrade")
		return nil
	}

	outcome = upgradePromptFailed
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	binaryPath, err := installInlineUpgrade(ctx, update)
	if err != nil {
		return err
	}
	installedVersion, err := verifyInlineUpgrade(ctx, binaryPath, update.LatestVersion)
	if err != nil {
		return err
	}
	outcome = upgradePromptSucceeded
	ui.PrintSuccess("Verified Revyl CLI %s.", installedVersion)
	ui.PrintDim("The update did not change project configuration or rerun your previous command.")
	return nil
}

func installPromptedUpgrade(ctx context.Context, update versionCheckResult) (string, error) {
	switch update.InstallMethod {
	case "direct":
		return performSelfUpdateFn(ctx, update.LatestVersion)
	case "homebrew":
		if err := performBrewUpgradeWithContext(ctx); err != nil {
			return "", err
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var prefixOutput bytes.Buffer
		prefixCmd := brewCommandRunner("brew", "--prefix", "revyl")
		prefixCmd.Stdout = &prefixOutput
		prefixCmd.Stderr = os.Stderr
		if err := runUpgradeProcess(ctx, prefixCmd); err != nil {
			return "", fmt.Errorf("could not locate the upgraded Homebrew CLI; run 'brew --prefix revyl': %w", err)
		}
		prefix := strings.TrimSpace(prefixOutput.String())
		if !filepath.IsAbs(prefix) || strings.ContainsAny(prefix, "\r\n") {
			return "", fmt.Errorf("Homebrew returned an invalid installation path; run 'brew --prefix revyl'")
		}
		return filepath.Join(prefix, "bin", "revyl"), nil
	default:
		return "", fmt.Errorf("inline upgrade is not supported for %s installations", update.InstallMethod)
	}
}

func verifyInstalledUpgrade(ctx context.Context, binaryPath, expectedVersion string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := upgradeVerificationCommand(ctx, binaryPath, "version", "--json")
	cmd.Env = append(os.Environ(), "REVYL_TELEMETRY_DISABLED=1")
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("could not verify the installed CLI; run 'revyl version': %w", err)
	}
	var installed struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(output, &installed); err != nil || strings.TrimSpace(installed.Version) == "" {
		return "", fmt.Errorf("the installed CLI returned invalid version information; run 'revyl version'")
	}
	installedSemver, err := semver.StrictNewVersion(strings.TrimPrefix(installed.Version, "v"))
	if err != nil {
		return "", fmt.Errorf("the installed CLI returned an invalid version; run 'revyl version'")
	}
	expectedSemver, err := semver.StrictNewVersion(strings.TrimPrefix(expectedVersion, "v"))
	if err != nil {
		return "", fmt.Errorf("the requested release has an invalid version; run 'revyl upgrade'")
	}
	if installedSemver.LessThan(expectedSemver) {
		return "", fmt.Errorf("the installed CLI is still %s, expected %s; check your package manager and PATH, then run 'revyl upgrade'", installed.Version, expectedVersion)
	}
	return installed.Version, nil
}

// upgradeProcessCleanupWait bounds how long a cancelled updater may keep the
// caller waiting on output it never drains.
var upgradeProcessCleanupWait = time.Second

func runUpgradeProcess(ctx context.Context, cmd *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd.WaitDelay = time.Second
	configureCommandProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		terminateCommandProcessGroup(cmd)
		select {
		case <-done:
			return ctx.Err()
		case <-time.After(upgradeProcessCleanupWait):
			return fmt.Errorf("%w: timed out waiting for the updater to finish cleanup", ctx.Err())
		}
	}
}
