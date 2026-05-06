package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevLoopSkillContextGuidance(t *testing.T) {
	skillPath := filepath.Join("..", "..", "skills", "revyl-cli-dev-loop", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", skillPath, err)
	}
	text := string(data)

	for _, expected := range []string{
		"Use normal `revyl dev` for new work.",
		"Revyl auto-selects a safe branch/platform context name",
		"Pass `--context <name>` only when deliberately targeting a known",
		"revyl dev --no-build --app-id <app-id>",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("skill missing %q", expected)
		}
	}

	for _, unexpected := range []string{
		"REVYL_CONTEXT",
		"export REVYL_CONTEXT",
		"revyl dev --context \"$REVYL_CONTEXT\"",
	} {
		if strings.Contains(text, unexpected) {
			t.Fatalf("skill still contains legacy context guidance %q", unexpected)
		}
	}
}
