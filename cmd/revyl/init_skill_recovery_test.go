package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/ui"
)

func TestInitSkillRecoveryPreservesSelectedAgentAndCopyMode(t *testing.T) {
	for _, choice := range []struct {
		value string
		agent string
		retry string
	}{
		{value: "cursor", agent: "cursor", retry: "revyl skill install --cursor"},
		{value: "codex", agent: "codex", retry: "revyl skill install --codex"},
		{value: "claude", agent: "claude", retry: "revyl skill install --claude"},
		{value: "claude-copy", agent: "claude", retry: "revyl skill install --claude --copy"},
	} {
		t.Run(choice.value, func(t *testing.T) {
			resetSkillInstallFlags(t)
			withWorkingDir(t, t.TempDir())
			originalSelect, originalQuiet := selectInitAgentTool, ui.IsQuietMode()
			ui.SetQuietMode(false)
			t.Cleanup(func() {
				selectInitAgentTool = originalSelect
				ui.SetQuietMode(originalQuiet)
			})
			selectInitAgentTool = func(_ string, options []ui.SelectOption, _ int) (int, string, error) {
				for index, option := range options {
					if option.Value == choice.value {
						return index, option.Value, nil
					}
				}
				t.Fatalf("missing agent choice %s", choice.value)
				return 0, "", nil
			}
			selectAgentSkills = func(string, []ui.SelectOption, []string) ([]string, error) {
				return []string{"revyl-cli-create"}, nil
			}
			base := filepath.Join("."+choice.agent, "skills")
			if err := os.MkdirAll(base, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(base, "revyl-cli-create"), []byte("preserve this conflict"), 0o600); err != nil {
				t.Fatal(err)
			}
			output := captureStdoutAndStderr(t, func() {
				label, installed := wizardAgentSkillsSetup()
				if installed || label != agentSkillToolLabel(choice.agent) {
					t.Fatalf("label=%q installed=%t, want failed installation for %s", label, installed, choice.agent)
				}
			})
			if !strings.Contains(output, "Run manually: "+choice.retry+"\n") {
				t.Fatalf("output=%q, want recovery command %q", output, choice.retry)
			}
		})
	}
}
