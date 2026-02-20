package main

import (
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

func TestResolveInstallSkillsDefaultInstallsCLIFamily(t *testing.T) {
	withSkillFamilyFlags(false, false, func() {
		selected, err := resolveInstallSkills(nil)
		if err != nil {
			t.Fatalf("resolveInstallSkills(nil) error = %v", err)
		}
		if len(selected) == 0 {
			t.Fatal("expected at least one CLI skill")
		}
		for _, sk := range selected {
			if !strings.HasPrefix(sk.Name, skillFamilyCLIPrefix) {
				t.Fatalf("expected only CLI family skills by default, got %q", sk.Name)
			}
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
		if len(selected) != 8 {
			t.Fatalf("expected 8 skills when both families selected, got %d", len(selected))
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
