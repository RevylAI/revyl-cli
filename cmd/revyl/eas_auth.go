package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mattn/go-isatty"

	"github.com/revyl/cli/internal/ui"
)

type easAuthPreflightStatus string

const (
	easAuthPreflightAuthenticated easAuthPreflightStatus = "authenticated"
	easAuthPreflightNeedsLogin    easAuthPreflightStatus = "needs_login"
	easAuthPreflightTooling       easAuthPreflightStatus = "tooling"
	easAuthPreflightTransient     easAuthPreflightStatus = "transient"
)

func ensureExpoEASAuth(cwd string) bool {
	output, err := runEASWhoAmI(cwd)
	status := classifyEASAuthPreflight(output, err)
	switch status {
	case easAuthPreflightAuthenticated:
		return true
	case easAuthPreflightTransient:
		ui.PrintWarning("Could not verify EAS login before build: %s", summarizeEASOutput(output, err))
		ui.PrintDim("Continuing build anyway (preflight was inconclusive).")
		return true
	case easAuthPreflightTooling:
		ui.PrintWarning("EAS CLI check failed: %s", summarizeEASOutput(output, err))
		ui.PrintInfo("Fix your local EAS setup:")
		ui.PrintDim("  npx --yes eas-cli --version")
		ui.PrintDim("  npx --yes eas-cli login")
		return false
	case easAuthPreflightNeedsLogin:
		if canPromptForEASLogin() {
			proceed, promptErr := ui.PromptConfirm("EAS login required. Run `npx --yes eas-cli login` now?", true)
			if promptErr == nil && proceed {
				if loginErr := runEASLoginInteractive(cwd); loginErr != nil {
					ui.PrintWarning("EAS login failed: %v", loginErr)
				}
				recheckOutput, recheckErr := runEASWhoAmI(cwd)
				recheckStatus := classifyEASAuthPreflight(recheckOutput, recheckErr)
				if recheckStatus == easAuthPreflightAuthenticated {
					ui.PrintSuccess("EAS login confirmed.")
					return true
				}
				if recheckStatus == easAuthPreflightTransient {
					ui.PrintWarning("Could not verify EAS login after login attempt: %s", summarizeEASOutput(recheckOutput, recheckErr))
					ui.PrintDim("Continuing build anyway (preflight was inconclusive).")
					return true
				}
			}
		}

		ui.PrintWarning("EAS login is required before running Expo builds.")
		ui.PrintDim("  Run: npx --yes eas-cli login")
		return false
	default:
		ui.PrintWarning("Could not verify EAS login before build: %s", summarizeEASOutput(output, err))
		ui.PrintDim("Continuing build anyway (preflight was inconclusive).")
		return true
	}
}

func runEASWhoAmI(cwd string) (string, error) {
	cmd := exec.Command("npx", "--yes", "eas-cli", "whoami")
	cmd.Dir = cwd
	output, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

func runEASLoginInteractive(cwd string) error {
	cmd := exec.Command("npx", "--yes", "eas-cli", "login")
	cmd.Dir = cwd
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func canPromptForEASLogin() bool {
	if isCIEnvironment() {
		return false
	}
	return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
}

func classifyEASAuthPreflight(output string, err error) easAuthPreflightStatus {
	if err == nil {
		return easAuthPreflightAuthenticated
	}

	combined := strings.TrimSpace(output)
	if err != nil {
		if combined != "" {
			combined += "\n"
		}
		combined += err.Error()
	}
	lower := strings.ToLower(strings.TrimSpace(combined))
	if lower == "" {
		return easAuthPreflightTransient
	}

	if strings.Contains(lower, "not logged in") ||
		strings.Contains(lower, "log in to eas") ||
		strings.Contains(lower, "an expo user account is required") ||
		(strings.Contains(lower, "stdin is not readable") && strings.Contains(lower, "log in")) {
		return easAuthPreflightNeedsLogin
	}

	if strings.Contains(lower, "npx: command not found") ||
		strings.Contains(lower, "command not found: npx") ||
		strings.Contains(lower, "executable file not found") ||
		strings.Contains(lower, "no such file or directory") ||
		strings.Contains(lower, "could not determine executable to run") ||
		strings.Contains(lower, "eas-cli: command not found") ||
		strings.Contains(lower, "unable to locate package eas-cli") {
		return easAuthPreflightTooling
	}

	if strings.Contains(lower, "timed out") ||
		strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "econn") ||
		strings.Contains(lower, "enotfound") ||
		strings.Contains(lower, "temporary failure") ||
		strings.Contains(lower, "network") ||
		strings.Contains(lower, "service unavailable") ||
		strings.Contains(lower, "502") ||
		strings.Contains(lower, "503") ||
		strings.Contains(lower, "504") {
		return easAuthPreflightTransient
	}

	return easAuthPreflightTransient
}

func summarizeEASOutput(output string, err error) string {
	output = strings.TrimSpace(output)
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}
	if err != nil {
		return err.Error()
	}
	return "unknown error"
}

func formatEASLoginRequiredError() error {
	return fmt.Errorf("expo build requires EAS authentication; run npx --yes eas-cli login")
}
