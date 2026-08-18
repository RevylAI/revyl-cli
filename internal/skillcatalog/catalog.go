package skillcatalog

import (
	"strings"

	"github.com/revyl/cli/skills"
)

const SkillFileName = skills.SkillFileName

// Skill describes one installable agent skill.
type Skill struct {
	Name        string
	Description string
	Content     string
}

var catalog = []Skill{
	{
		Name:        skills.RevylCLIName,
		Description: "Base CLI skill for Revyl command workflows. Routes to CLI Atlas/create/analyze/dev-loop skills.",
		Content:     skills.RevylCLIContent,
	},
	{
		Name:        skills.RevylCLICreateName,
		Description: "Create stable Revyl E2E tests using CLI commands and convert exploratory sessions into regressions.",
		Content:     skills.RevylCLICreateContent,
	},
	{
		Name:        skills.RevylCLIAnalyzeName,
		Description: "Analyze failed Revyl test, workflow, and device-session reports, including auth/setup failures.",
		Content:     skills.RevylCLIAnalyzeContent,
	},
	{
		Name:        skills.RevylCLIOptimizeName,
		Description: "Optimize existing Revyl tests by merging granular button-press steps into intent-driven instructions.",
		Content:     skills.RevylCLIOptimizeContent,
	},
	{
		Name:        skills.RevylCLIDevLoopName,
		Description: "CLI-first dev loop for starting sessions, exploring flows, and converting successful paths into tests.",
		Content:     skills.RevylCLIDevLoopContent,
	},
	{
		Name:        skills.RevylCLIAtlasName,
		Description: "Explore an app bottom-up through observed screenshots, transition clips, and originating reports.",
		Content:     skills.RevylCLIAtlasContent,
	},
	{
		Name:        skills.RevylCLIAuthBypassName,
		Description: "Set up test-only auth bypass across mobile app stacks using Revyl launch variables and device verification.",
		Content:     skills.RevylCLIAuthBypassContent,
	},
	{
		Name:        skills.RevylMCPName,
		Description: "Base MCP skill for Revyl tool orchestration. Routes to MCP create/analyze/dev-loop skills.",
		Content:     skills.RevylMCPContent,
	},
	{
		Name:        skills.RevylMCPCreateName,
		Description: "Author tests via MCP tools: validate YAML, create/update tests, execute, and iterate.",
		Content:     skills.RevylMCPCreateContent,
	},
	{
		Name:        skills.RevylMCPAnalyzeName,
		Description: "Analyze failed MCP-driven test executions and produce concrete remediation actions.",
		Content:     skills.RevylMCPAnalyzeContent,
	},
	{
		Name:        skills.RevylMCPDevLoopName,
		Description: "MCP dev loop with screenshot-observe-action cycles, grounded interaction, and re-anchoring.",
		Content:     skills.RevylMCPDevLoopContent,
	},
}

var publicSkillNames = []string{
	skills.RevylCLIDevLoopName,
	skills.RevylCLIAtlasName,
	skills.RevylCLICreateName,
	skills.RevylCLIAuthBypassName,
}

var authBypassLeafAliases = []string{
	"revyl-cli-auth-bypass-expo",
	"revyl-cli-auth-bypass-react-native",
	"revyl-cli-auth-bypass-ios",
	"revyl-cli-auth-bypass-android",
	"revyl-cli-auth-bypass-flutter",
}

// All returns a copy of all embedded skills in deterministic install order.
func All() []Skill {
	out := make([]Skill, len(catalog))
	copy(out, catalog)
	return out
}

// Public returns the first-class customer-facing skills in display/install order.
func Public() []Skill {
	return skillsByName(publicSkillNames)
}

// DefaultInstall returns the skills installed by the no-name install path.
func DefaultInstall() []Skill {
	return Public()
}

// AuthBypassLeafAliases returns retired platform recipe names that resolve to
// revyl-cli-auth-bypass.
func AuthBypassLeafAliases() []string {
	out := make([]string, len(authBypassLeafAliases))
	copy(out, authBypassLeafAliases)
	return out
}

func skillsByName(names []string) []Skill {
	out := make([]Skill, 0, len(names))
	for _, name := range names {
		if sk, ok := Get(name); ok {
			out = append(out, sk)
		}
	}
	return out
}

// Names returns all valid skill names in deterministic order.
func Names() []string {
	names := make([]string, 0, len(catalog))
	for _, sk := range catalog {
		names = append(names, sk.Name)
	}
	return names
}

// canonicalSkillName maps a retired alias to the shipped skill name.
//
// Parameters:
//   - name: Requested skill name, which may be an alias.
//
// Returns:
//   - string: Catalog name to look up.
func canonicalSkillName(name string) string {
	for _, alias := range authBypassLeafAliases {
		if name == alias {
			return skills.RevylCLIAuthBypassName
		}
	}
	return name
}

// Get returns one skill by exact name or retired auth-bypass leaf alias.
//
// Parameters:
//   - name: Requested skill name.
//
// Returns:
//   - Skill: Matching catalog entry.
//   - bool: Whether the name or alias resolved.
func Get(name string) (Skill, bool) {
	name = canonicalSkillName(strings.TrimSpace(name))
	for _, sk := range catalog {
		if sk.Name == name {
			return sk, true
		}
	}
	return Skill{}, false
}
