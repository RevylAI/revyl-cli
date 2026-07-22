package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/skillcatalog"
	"github.com/revyl/cli/internal/testutil"
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

var authBypassLeafSkillNames = []string{
	"revyl-cli-auth-bypass-expo",
	"revyl-cli-auth-bypass-react-native",
	"revyl-cli-auth-bypass-ios",
	"revyl-cli-auth-bypass-android",
	"revyl-cli-auth-bypass-flutter",
}

func defaultInstalledSkillNamesForTest() []string {
	names := []string{"revyl-cli-dev-loop", "revyl-cli-create", "revyl-cli-auth-bypass"}
	return append(names, authBypassLeafSkillNames...)
}

func assertInstalledSkills(t *testing.T, baseDir string, names []string) {
	t.Helper()

	for _, name := range names {
		path := filepath.Join(baseDir, name, "SKILL.md")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}
}

func TestResolveInstallSkillsDefaultInstallsRecommendedBundle(t *testing.T) {
	withSkillFamilyFlags(false, false, func() {
		selected, err := resolveInstallSkills(nil)
		if err != nil {
			t.Fatalf("resolveInstallSkills(nil) error = %v", err)
		}
		got := make([]string, 0, len(selected))
		for _, sk := range selected {
			got = append(got, sk.Name)
		}
		want := defaultInstalledSkillNamesForTest()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("default skills = %v, want %v", got, want)
		}
	})
}

func TestPublicSkillListUsesAuthBypassParent(t *testing.T) {
	public := skillcatalog.Public()
	got := make([]string, 0, len(public))
	for _, sk := range public {
		got = append(got, sk.Name)
	}
	want := []string{"revyl-cli-dev-loop", "revyl-cli-create", "revyl-cli-auth-bypass"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("public skills = %v, want %v", got, want)
	}
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
		if len(selected) != 14 {
			t.Fatalf("expected 14 skills when both families selected, got %d", len(selected))
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

func TestResolveInstallSkillsByNameIncludesAuthBypassLeaves(t *testing.T) {
	withSkillFamilyFlags(false, false, func() {
		for _, name := range authBypassLeafSkillNames {
			selected, err := resolveInstallSkills([]string{name})
			if err != nil {
				t.Fatalf("resolveInstallSkills(%q) error = %v", name, err)
			}
			if len(selected) != 1 || selected[0].Name != name {
				t.Fatalf("selected = %#v, want only %s", selected, name)
			}
			if !strings.Contains(selected[0].Content, "REVYL_AUTH_BYPASS_TOKEN") {
				t.Fatalf("%s content did not mention REVYL_AUTH_BYPASS_TOKEN", name)
			}
		}
	})
}

func TestResolveInstallSkillsByNameIncludesAuthBypassParent(t *testing.T) {
	withSkillFamilyFlags(false, false, func() {
		selected, err := resolveInstallSkills([]string{"revyl-cli-auth-bypass"})
		if err != nil {
			t.Fatalf("resolveInstallSkills(name) error = %v", err)
		}
		if len(selected) != 1 || selected[0].Name != "revyl-cli-auth-bypass" {
			t.Fatalf("selected = %#v, want only revyl-cli-auth-bypass", selected)
		}
		for _, name := range authBypassLeafSkillNames {
			if !strings.Contains(selected[0].Content, name) {
				t.Fatalf("expected parent auth bypass skill content to mention %s", name)
			}
		}
	})
}

func TestInstallSelectedAuthBypassLeafSkills(t *testing.T) {
	workDir := t.TempDir()
	target := filepath.Join(workDir, ".codex", "skills")

	selected, err := resolveInstallSkills(authBypassLeafSkillNames)
	if err != nil {
		t.Fatalf("resolveInstallSkills(names) error = %v", err)
	}
	if err := installSkillsToTargets([]skillInstallTarget{{tool: "codex", path: target}}, selected, true); err != nil {
		t.Fatalf("installSkillsToTargets() error = %v", err)
	}

	for _, name := range authBypassLeafSkillNames {
		path := filepath.Join(target, name, "SKILL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("expected installed skill at %s: %v", path, err)
		}
		if !strings.Contains(string(data), "REVYL_AUTH_BYPASS_TOKEN") {
			t.Fatalf("installed skill content for %s did not mention REVYL_AUTH_BYPASS_TOKEN", name)
		}
	}
}

func TestInstallPublicSkillsForToolsWritesDefaultSkills(t *testing.T) {
	cases := []struct {
		tool      string
		skillsDir string
	}{
		{tool: "cursor", skillsDir: filepath.Join(".cursor", "skills")},
		{tool: "claude", skillsDir: filepath.Join(".claude", "skills")},
		{tool: "codex", skillsDir: filepath.Join(".codex", "skills")},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			workDir := t.TempDir()
			withWorkingDir(t, workDir)

			if err := installPublicSkillsForTools([]string{tc.tool}, false, true); err != nil {
				t.Fatalf("installPublicSkillsForTools() error = %v", err)
			}

			target := filepath.Join(workDir, tc.skillsDir)
			assertInstalledSkills(t, target, defaultInstalledSkillNamesForTest())

			compatPath := filepath.Join(target, "revyl-cli-analyze", "SKILL.md")
			if _, err := os.Stat(compatPath); !os.IsNotExist(err) {
				t.Fatalf("expected compatibility skill not to be installed by default, stat err = %v", err)
			}
		})
	}
}

func TestCursorProjectInstallWritesCompanionRule(t *testing.T) {
	workDir := t.TempDir()
	withWorkingDir(t, workDir)

	if err := installPublicSkillsForTools([]string{"cursor"}, false, true); err != nil {
		t.Fatalf("installPublicSkillsForTools() error = %v", err)
	}

	rulePath := filepath.Join(workDir, ".cursor", "rules", cursorRuleFileName)
	data, err := os.ReadFile(rulePath)
	if err != nil {
		t.Fatalf("expected Cursor companion rule at %s: %v", rulePath, err)
	}

	rule := string(data)
	for _, want := range []string{
		"alwaysApply: false",
		"explicit CLI-only fallback",
		"revyl-mcp-dev-loop",
		"revyl-cli-dev-loop",
		"revyl-cli-create",
		"revyl-cli-auth-bypass",
		"revyl device screenshot",
	} {
		if !strings.Contains(rule, want) {
			t.Fatalf("Cursor rule did not contain %q", want)
		}
	}
}

func TestCursorMCPInstallRemovesCLIOnlyCompanionRule(t *testing.T) {
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	target := skillInstallTarget{
		tool: "cursor",
		path: filepath.Join(workDir, ".cursor", "skills"),
	}

	if err := installPublicSkillsForTools([]string{"cursor"}, false, true); err != nil {
		t.Fatalf("installPublicSkillsForTools() error = %v", err)
	}
	rulePath := filepath.Join(workDir, ".cursor", "rules", cursorRuleFileName)
	if _, err := os.Stat(rulePath); err != nil {
		t.Fatalf("expected CLI-only rule before MCP install: %v", err)
	}

	mcpSkills, err := resolveInstallSkillsByFamily(false, true)
	if err != nil {
		t.Fatalf("resolve MCP skills: %v", err)
	}
	if err := installSkillsToTargets([]skillInstallTarget{target}, mcpSkills, true); err != nil {
		t.Fatalf("install MCP skills: %v", err)
	}
	if _, err := os.Stat(rulePath); !os.IsNotExist(err) {
		t.Fatalf("CLI-only rule remains after MCP install: %v", err)
	}
}

func TestCursorGlobalInstallDoesNotWriteCompanionRule(t *testing.T) {
	workDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workDir)
	testutil.SetHomeDir(t, homeDir)

	if err := installPublicSkillsForTools([]string{"cursor"}, true, true); err != nil {
		t.Fatalf("installPublicSkillsForTools() error = %v", err)
	}

	assertInstalledSkills(t, filepath.Join(homeDir, ".cursor", "skills"), defaultInstalledSkillNamesForTest())

	for _, rulePath := range []string{
		filepath.Join(workDir, ".cursor", "rules", cursorRuleFileName),
		filepath.Join(homeDir, ".cursor", "rules", cursorRuleFileName),
	} {
		if _, err := os.Stat(rulePath); !os.IsNotExist(err) {
			t.Fatalf("expected no global Cursor companion rule at %s, stat err = %v", rulePath, err)
		}
	}
}

func TestDefaultInstalledSkillContentIncludesNativeAgentBehavior(t *testing.T) {
	withSkillFamilyFlags(false, false, func() {
		selected, err := resolveInstallSkills(nil)
		if err != nil {
			t.Fatalf("resolveInstallSkills(nil) error = %v", err)
		}

		for _, sk := range selected {
			for _, want := range []string{"Native Agent Behavior", "open it in the native browser", "Codex Browser", "Claude Code `.claude/skills`", "Cursor `.cursor/skills`", "revyl device screenshot"} {
				if !strings.Contains(sk.Content, want) {
					t.Fatalf("%s content did not contain %q", sk.Name, want)
				}
			}
		}
	})
}

func withoutExplicitSkillTools(t *testing.T) {
	t.Helper()

	prevCursor, prevClaude, prevCodex, prevGlobal := skillInstallCursor, skillInstallClaude, skillInstallCodex, skillInstallGlobal
	skillInstallCursor, skillInstallClaude, skillInstallCodex, skillInstallGlobal = false, false, false, false
	t.Cleanup(func() {
		skillInstallCursor, skillInstallClaude, skillInstallCodex, skillInstallGlobal = prevCursor, prevClaude, prevCodex, prevGlobal
	})
}

func detectedSkillTools(targets []skillInstallTarget) map[string]bool {
	tools := make(map[string]bool, len(targets))
	for _, target := range targets {
		tools[target.tool] = true
	}
	return tools
}

func TestResolveInstallTargetsDetectsClaudeFromParentDir(t *testing.T) {
	workDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workDir)
	testutil.SetHomeDir(t, homeDir)
	withoutExplicitSkillTools(t)

	if err := os.MkdirAll(filepath.Join(workDir, ".claude"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.claude) error = %v", err)
	}

	tools := detectedSkillTools(resolveInstallTargets())
	if !tools["claude"] {
		t.Fatalf("expected claude to be auto-detected from .claude/ parent dir, got %v", tools)
	}
	if tools["cursor"] || tools["codex"] {
		t.Fatalf("expected only claude to be detected, got %v", tools)
	}
}

func TestResolveInstallTargetsDetectsToolFromSkillsDir(t *testing.T) {
	workDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workDir)
	testutil.SetHomeDir(t, homeDir)
	withoutExplicitSkillTools(t)

	if err := os.MkdirAll(filepath.Join(workDir, ".cursor", "skills"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.cursor/skills) error = %v", err)
	}

	tools := detectedSkillTools(resolveInstallTargets())
	if !tools["cursor"] {
		t.Fatalf("expected cursor to be auto-detected from .cursor/skills, got %v", tools)
	}
}

func TestResolveInstallTargetsNoToolsReturnsNil(t *testing.T) {
	workDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workDir)
	testutil.SetHomeDir(t, homeDir)
	withoutExplicitSkillTools(t)

	if targets := resolveInstallTargets(); targets != nil {
		t.Fatalf("expected no install targets when no tool dirs exist, got %v", targets)
	}
}
