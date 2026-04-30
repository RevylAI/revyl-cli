package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withSkillFamilyFlags(cli bool, mcp bool, fn func()) {
	prevCLI := skillInstallCLI
	prevMCP := skillInstallMCP
	skillInstallCLI = cli
	skillInstallMCP = mcp
	defer func() {
		skillInstallCLI = prevCLI
		skillInstallMCP = prevMCP
	}()
	fn()
}

func TestResolveInstallSkillsDefaultInstallsPublicSkills(t *testing.T) {
	withSkillFamilyFlags(false, false, func() {
		selected, err := resolveInstallSkills(nil)
		if err != nil {
			t.Fatalf("resolveInstallSkills(nil) error = %v", err)
		}
		got := make([]string, 0, len(selected))
		for _, sk := range selected {
			got = append(got, sk.Name)
		}
		want := []string{"revyl-cli-dev-loop", "revyl-cli-create"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("default skills = %v, want %v", got, want)
		}
	})
}

func TestResolveInstallSkillsMCPFamilyOnly(t *testing.T) {
	withSkillFamilyFlags(false, true, func() {
		selected, err := resolveInstallSkills(nil)
		if err != nil {
			t.Fatalf("resolveInstallSkills(nil) error = %v", err)
		}
		if len(selected) == 0 {
			t.Fatal("expected at least one MCP skill")
		}
		for _, sk := range selected {
			if !strings.HasPrefix(sk.Name, skillFamilyMCPPrefix) {
				t.Fatalf("expected only MCP family skills, got %q", sk.Name)
			}
		}
	})
}

func TestResolveInstallSkillsBothFamilies(t *testing.T) {
	withSkillFamilyFlags(true, true, func() {
		selected, err := resolveInstallSkills(nil)
		if err != nil {
			t.Fatalf("resolveInstallSkills(nil) error = %v", err)
		}
		if len(selected) != 9 {
			t.Fatalf("expected 9 skills when both families selected, got %d", len(selected))
		}
		var cliCount, mcpCount int
		for _, sk := range selected {
			if strings.HasPrefix(sk.Name, skillFamilyCLIPrefix) {
				cliCount++
			}
			if strings.HasPrefix(sk.Name, skillFamilyMCPPrefix) {
				mcpCount++
			}
		}
		if cliCount == 0 || mcpCount == 0 {
			t.Fatalf("expected both families in result, cli=%d mcp=%d", cliCount, mcpCount)
		}
	})
}

func TestResolveInstallSkillsRejectsMixedSelectors(t *testing.T) {
	withSkillFamilyFlags(false, true, func() {
		_, err := resolveInstallSkills([]string{"revyl-cli"})
		if err == nil {
			t.Fatal("expected error when --name is combined with --mcp/--cli selectors")
		}
	})
}

func TestResolveInstallSkillsByName(t *testing.T) {
	withSkillFamilyFlags(false, false, func() {
		selected, err := resolveInstallSkills([]string{"revyl-cli", "revyl-cli-dev-loop", "revyl-cli"})
		if err != nil {
			t.Fatalf("resolveInstallSkills(names) error = %v", err)
		}
		if len(selected) != 2 {
			t.Fatalf("expected duplicate names to be deduped to 2 skills, got %d", len(selected))
		}
	})
}

func TestResolveInstallSkillsByNameIncludesAuthBypassExpo(t *testing.T) {
	withSkillFamilyFlags(false, false, func() {
		selected, err := resolveInstallSkills([]string{"revyl-cli-auth-bypass-expo"})
		if err != nil {
			t.Fatalf("resolveInstallSkills(name) error = %v", err)
		}
		if len(selected) != 1 || selected[0].Name != "revyl-cli-auth-bypass-expo" {
			t.Fatalf("selected = %#v, want only revyl-cli-auth-bypass-expo", selected)
		}
		if !strings.Contains(selected[0].Content, "Expo Router") {
			t.Fatal("expected auth bypass skill content to mention Expo Router")
		}
	})
}

func TestInstallSelectedAuthBypassExpoSkill(t *testing.T) {
	workDir := t.TempDir()
	target := filepath.Join(workDir, ".codex", "skills")

	selected, err := resolveInstallSkills([]string{"revyl-cli-auth-bypass-expo"})
	if err != nil {
		t.Fatalf("resolveInstallSkills(name) error = %v", err)
	}
	if err := installSkillsToTargets([]string{target}, selected, true); err != nil {
		t.Fatalf("installSkillsToTargets() error = %v", err)
	}

	path := filepath.Join(target, "revyl-cli-auth-bypass-expo", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected installed skill at %s: %v", path, err)
	}
	if !strings.Contains(string(data), "Expo Router") {
		t.Fatalf("installed skill content did not mention Expo Router")
	}
}

func TestInstallPublicSkillsForToolsWritesOnlyFirstClassSkills(t *testing.T) {
	workDir := t.TempDir()
	withWorkingDir(t, workDir)

	if err := installPublicSkillsForTools([]string{"cursor"}, false, true); err != nil {
		t.Fatalf("installPublicSkillsForTools() error = %v", err)
	}

	for _, name := range []string{"revyl-cli-dev-loop", "revyl-cli-create"} {
		path := filepath.Join(workDir, ".cursor", "skills", name, "SKILL.md")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}

	compatPath := filepath.Join(workDir, ".cursor", "skills", "revyl-cli-analyze", "SKILL.md")
	if _, err := os.Stat(compatPath); !os.IsNotExist(err) {
		t.Fatalf("expected compatibility skill not to be installed by default, stat err = %v", err)
	}

	authBypassPath := filepath.Join(workDir, ".cursor", "skills", "revyl-cli-auth-bypass-expo", "SKILL.md")
	if _, err := os.Stat(authBypassPath); !os.IsNotExist(err) {
		t.Fatalf("expected auth bypass skill not to be installed by default, stat err = %v", err)
	}
}
