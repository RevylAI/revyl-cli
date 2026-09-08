package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallPromptedUpgradeUsesHomebrewPrefix(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "homebrew prefix")
	for _, test := range []struct {
		name       string
		output     string
		prefixFail bool
		wantError  bool
	}{
		{name: "absolute prefix", output: prefix + "\n"},
		{name: "missing prefix", wantError: true},
		{name: "relative prefix", output: "relative/path\n", wantError: true},
		{name: "multiple prefixes", output: prefix + "\n" + prefix, wantError: true},
		{name: "failed lookup", prefixFail: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := brewCommandRunner
			t.Cleanup(func() { brewCommandRunner = previous })
			var calls []string
			brewCommandRunner = func(name string, args ...string) *exec.Cmd {
				call := strings.Join(append([]string{name}, args...), " ")
				calls = append(calls, call)
				cmd := exec.Command(os.Args[0], "-test.run=^TestHomebrewPrefixProcessHelper$")
				cmd.Env = append(os.Environ(), "REVYL_BREW_PREFIX_HELPER=1")
				if call == "brew --prefix revyl" {
					cmd.Env = append(cmd.Env, "REVYL_BREW_PREFIX_OUTPUT="+test.output)
					if test.prefixFail {
						cmd.Env = append(cmd.Env, "REVYL_BREW_PREFIX_FAIL=1")
					}
				}
				return cmd
			}
			binaryPath, err := installPromptedUpgrade(context.Background(), versionCheckResult{InstallMethod: "homebrew"})
			if (err != nil) != test.wantError {
				t.Fatalf("installPromptedUpgrade() = %q, %v", binaryPath, err)
			}
			if got := strings.Join(calls, "; "); got != "brew update; brew upgrade revyl; brew --prefix revyl" {
				t.Fatalf("unexpected Homebrew commands: %s", got)
			}
			if !test.wantError && binaryPath != filepath.Join(prefix, "bin", "revyl") {
				t.Fatalf("verification binary = %q, want the absolute Homebrew binary", binaryPath)
			}
		})
	}
}

func TestHomebrewPrefixProcessHelper(t *testing.T) {
	if os.Getenv("REVYL_BREW_PREFIX_HELPER") != "1" {
		return
	}
	if os.Getenv("REVYL_BREW_PREFIX_FAIL") == "1" {
		os.Exit(1)
	}
	fmt.Print(os.Getenv("REVYL_BREW_PREFIX_OUTPUT"))
	os.Exit(0)
}
