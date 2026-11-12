package cursorpluginrelease

import (
	"strings"
	"testing"
)

func TestCodexGuardRequiresAnIndependentVersionIncrease(t *testing.T) {
	for _, path := range []string{
		"revyl-cli/.agents/plugins/marketplace.json",
		"revyl-cli/.claude-plugin/marketplace.json",
		"revyl-cli/plugins/revyl/.codex-plugin/plugin.json",
		"revyl-cli/plugins/revyl/.claude-plugin/plugin.json",
		"revyl-cli/plugins/revyl/skills/revyl-dev-loop/SKILL.md",
		"revyl-cli/plugins/revyl/scripts/launch-runtime",
		"revyl-cli/plugins/revyl/runtime-version",
		"revyl-cli/cmd/sync-codex-plugin/main.go",
	} {
		t.Run(path, func(t *testing.T) {
			input := GuardInput{PluginHost: "codex", ChangedFiles: path, BaseVersion: "0.1.0", HeadVersion: "0.1.0"}
			result := EvaluateGuard(input)
			if result.ExitCode != 1 || !strings.Contains(result.Stderr, "sync-codex-plugin") {
				t.Fatalf("missing Codex version increase accepted: %+v", result)
			}
			input.HeadVersion = "0.1.1"
			if result := EvaluateGuard(input); result.ExitCode != 0 {
				t.Fatalf("Codex version increase rejected: %+v", result)
			}
		})
	}
}

func TestCodexGuardIgnoresOtherHostsAndTestOnlyChanges(t *testing.T) {
	for _, path := range []string{
		"revyl-cli/cursor-plugin/hooks/launch-revyl",
		"revyl-cli/.cursor-plugin/marketplace.json",
		"revyl-cli/cmd/revyl/main.go",
		"revyl-cli/plugins/revyl/README.md",
		"revyl-cli/plugins/revyl/plugin_test.go",
		"revyl-cli/cmd/sync-codex-plugin/main_test.go",
	} {
		result := EvaluateGuard(GuardInput{PluginHost: "codex", ChangedFiles: path, BaseVersion: "0.1.0", HeadVersion: "0.1.0"})
		if result.ExitCode != 0 || !strings.Contains(result.Stdout, "No plugin-owned files changed") {
			t.Fatalf("Codex guard rejected %s: %+v", path, result)
		}
	}
}

func TestStagingGuardLeavesUnchangedVersionsToReleaseWorkflow(t *testing.T) {
	for host, path := range map[string]string{
		"":       "revyl-cli/cursor-plugin/hooks/ensure-revyl",
		"cursor": "revyl-cli/cursor-plugin/skills/revyl-cli-dev-loop/SKILL.md",
		"codex":  "revyl-cli/plugins/revyl/skills/revyl-dev-loop/SKILL.md",
	} {
		input := GuardInput{PluginHost: host, BaseRef: "staging", ChangedFiles: path, BaseVersion: "0.1.3", HeadVersion: "0.1.3"}
		if result := EvaluateGuard(input); result.ExitCode != 0 || !strings.Contains(result.Stdout, "staging release workflow will patch-bump") {
			t.Fatalf("staging PR for host %q rejected: %+v", host, result)
		}
		input.HeadVersion = "0.1.2"
		if result := EvaluateGuard(input); result.ExitCode != 1 {
			t.Fatalf("staging version decrease for host %q accepted: %+v", host, result)
		}
		input.BaseRef, input.HeadVersion = "main", "0.1.3"
		if result := EvaluateGuard(input); result.ExitCode != 1 {
			t.Fatalf("unchanged version for host %q accepted outside staging: %+v", host, result)
		}
	}
}

func TestCodexGuardFirstReleaseAndExplicitOptOut(t *testing.T) {
	input := GuardInput{
		PluginHost: "codex", ChangedFiles: "revyl-cli/plugins/revyl/runtime-manifest.json",
		BaseVersion: "0.0.0", HeadVersion: "0.1.0",
	}
	if result := EvaluateGuard(input); result.ExitCode != 0 {
		t.Fatalf("first release rejected: %+v", result)
	}
	input.BaseVersion = input.HeadVersion
	input.LabelsJSON = `["no-plugin-release"]`
	if result := EvaluateGuard(input); result.ExitCode != 0 {
		t.Fatalf("explicit opt-out rejected: %+v", result)
	}
	input.PluginHost = "unknown"
	if result := EvaluateGuard(input); result.ExitCode != 1 || !strings.Contains(result.Stderr, "PLUGIN_HOST") {
		t.Fatalf("unknown host accepted: %+v", result)
	}
}
