package mcp

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// skillNonToolNames are the snake_case names the MCP dev-loop skill quotes that
// are tool fields, parameters, or outcomes rather than tools.
var skillNonToolNames = map[string]bool{
	"viewer_url": true, "session_id": true, "next_steps": true, "project_dir": true, "platform_key": true,
	"auth_required": true, "auth_expired": true, "auth_invalid": true, "cloud_secret_required": true,
	"project_not_initialized": true, "project_legacy_config": true, "project_outside_git": true,
	"project_ambiguous": true, "project_invalid": true, "dev_loop_platform_key_removed": true,
	"check_command": true, "apply_command": true,
}

var skillIdentifierSpan = regexp.MustCompile("`([a-z][a-z0-9]*(?:_[a-z0-9]+)+)`")

// registeredToolsByProfile returns, per published profile name, the tools an
// MCP client of that profile sees.
func registeredToolsByProfile(t *testing.T) map[string]map[string]bool {
	t.Helper()
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-environment-api-key")
	registered := map[string]map[string]bool{}
	for _, profile := range publishedProfiles {
		_, listed := connectToProfile(t, profile)
		registered[profile.name] = map[string]bool{}
		for _, tool := range listed {
			registered[profile.name][tool.Name] = true
		}
	}
	return registered
}

// TestMCPDevLoopSkillNamesOnlyToolsEachProfileRegisters keeps each profile's
// flow in the skill on that profile's tools; sections outside the profile
// sections must name only tools every listed profile registers.
func TestMCPDevLoopSkillNamesOnlyToolsEachProfileRegisters(t *testing.T) {
	registered := registeredToolsByProfile(t)
	content, err := os.ReadFile("../../skills/revyl-mcp-dev-loop/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range mcpSkillToolProblems(string(content), registered) {
		t.Error(problem)
	}
}

func TestMCPSkillToolProblemsReportsUnregisteredAndMisspelledTools(t *testing.T) {
	registered := registeredToolsByProfile(t)
	skill := "## Dev profile\n\n1. `intercat(task=\"Tap Sign In\")`\n2. Then `stop_dev_lop`.\n\n## Core and full profiles\n\nCall `interact(task=\"x\")`.\n"

	problems := strings.Join(mcpSkillToolProblems(skill, registered), "\n")

	for _, want := range []string{"intercat", "stop_dev_lop", "interact where the core profile applies"} {
		if !strings.Contains(problems, want) {
			t.Errorf("problems lack %q:\n%s", want, problems)
		}
	}
	if clean := mcpSkillToolProblems("## Dev profile\n\nCall `interact(task=\"x\")` and keep `session_id`.\n\n## Core and full profiles\n\nCall `screenshot()`.\n", registered); len(clean) > 0 {
		t.Errorf("a valid skill reported %v", clean)
	}
}

// mcpSkillToolProblems checks every call-shaped name and every quoted
// snake_case name in an MCP skill: each must be a tool registered by every
// profile whose section it appears in, or a listed non-tool name.
func mcpSkillToolProblems(content string, registered map[string]map[string]bool) []string {
	known := map[string]bool{}
	for _, tools := range registered {
		for name := range tools {
			known[name] = true
		}
	}
	sectionProfiles := map[string][]string{
		"## Dev profile":            {"dev"},
		"## Core and full profiles": {"core", "full"},
	}
	shared := []string{"dev", "core", "full"}
	profiles := shared
	var problems []string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "## ") {
			profiles = shared
			if listed, ok := sectionProfiles[line]; ok {
				profiles = listed
			}
		}
		var tools []string
		for _, match := range instructionToolCalled.FindAllStringSubmatch(line, -1) {
			tools = append(tools, match[1])
		}
		for _, match := range skillIdentifierSpan.FindAllStringSubmatch(line, -1) {
			switch {
			case known[match[1]]:
				tools = append(tools, match[1])
			case !skillNonToolNames[match[1]]:
				problems = append(problems, fmt.Sprintf("skill names %s, which no profile registers; if it is a field or outcome, list it in skillNonToolNames: %q", match[1], line))
			}
		}
		for _, name := range tools {
			for _, profile := range profiles {
				if !registered[profile][name] {
					problems = append(problems, fmt.Sprintf("skill calls %s where the %s profile applies, but that profile does not register it: %q", name, profile, line))
				}
			}
		}
	}
	for heading := range sectionProfiles {
		if !strings.Contains("\n"+content, "\n"+heading+"\n") {
			problems = append(problems, fmt.Sprintf("skill has no %q section", heading))
		}
	}
	return problems
}
