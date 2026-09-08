// Package main provides the skill command for managing Revyl agent skills.
//
// Skills teach AI assistants (Cursor, Claude Code, Codex, VS Code) how to
// use Revyl effectively for screenshot-observe-action execution, dev-loop
// workflows, and turning exploratory sessions into reusable tests.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/skillcatalog"
	"github.com/revyl/cli/internal/ui"
)

// Supported skill directory locations for each tool, ordered by preference.
// Project-level directories are listed first, user-level (global) second.
var skillDirectories = map[string][]string{
	"cursor": {".cursor/skills", "~/.cursor/skills"},
	"claude": {".claude/skills", "~/.claude/skills"},
	"codex":  {".codex/skills", "~/.codex/skills"},
}

var supportedSkillTools = []string{"cursor", "claude", "codex"}

type skillInstallTarget struct {
	tool   string
	path   string
	global bool
}

var legacySkillNames = append([]string{
	"revyl-device",
	"revyl-dev-loop",
	"revyl-adhoc-to-test",
	"revyl-device-dev-loop",
	"revyl-create",
	"revyl-analyze",
}, skillcatalog.AuthBypassLeafAliases()...)

const (
	skillFamilyCLIPrefix = "revyl-cli"
	skillFamilyMCPPrefix = "revyl-mcp"
	cursorRuleFileName   = "revyl-skills.mdc"
)

// skillCmd is the parent command for agent skill management.
var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Manage Revyl agent skills",
	Long: `Manage Revyl agent skills for AI coding tools.

Revyl ships embedded skills:
- revyl-cli-dev-loop: agents run or attach to revyl dev, observe the app, and act through device commands
- revyl-cli-atlas: agents inspect Atlas screenshots, transition clips or frames, and originating reports
- revyl-cli-atlas-review: agents manage grounded Atlas feedback after an explicit user request
- revyl-cli-create: agents create or refine stable Revyl tests from YAML, source, or successful flows
- revyl-cli-auth-bypass: agents set up test-only auth bypass across mobile app stacks
- auth-bypass references: platform recipes loaded only after stack detection
- revyl-cli-optimize-tests for merging granular button-press steps in an existing test into intent-driven instructions when installed by name.

Additional optional and compatibility skills remain available by exact name.

EXAMPLES:
  revyl skill list
  revyl skill install
  revyl skill install --name revyl-cli-dev-loop --cursor
  revyl skill install --name revyl-cli-create --codex
  revyl skill install --name revyl-cli-auth-bypass --claude
  revyl skill show --name revyl-cli-dev-loop
  revyl skill show --name revyl-cli-atlas
  revyl skill install --name revyl-cli-atlas-review
  revyl skill update
  revyl skill export --name revyl-cli-create -o SKILL.md`,
}

var skillListCmd = &cobra.Command{
	Use:   "list",
	Short: "List first-class Revyl skills",
	Long: `List first-class Revyl skills that can be installed.

EXAMPLES:
  revyl skill list`,
	Args: cobra.NoArgs,
	RunE: runSkillList,
}

// skillShowCmd prints an embedded SKILL.md content to stdout.
var skillShowCmd = &cobra.Command{
	Use:   "show --name <skill-name>",
	Short: "Print a skill content to stdout",
	Long: `Print an embedded SKILL.md content to stdout.

EXAMPLES:
  revyl skill show --name revyl-cli-dev-loop
  revyl skill show --name revyl-cli-auth-bypass
  revyl skill show --name revyl-cli-create | pbcopy`,
	Args: cobra.NoArgs,
	RunE: runSkillShow,
}

// skillExportCmd writes an embedded SKILL.md to a file.
var skillExportCmd = &cobra.Command{
	Use:   "export --name <skill-name>",
	Short: "Export a skill to a file",
	Long: `Export an embedded SKILL.md to a file on disk.

EXAMPLES:
  revyl skill export --name revyl-cli-dev-loop -o /tmp/revyl-cli-dev-loop-SKILL.md
  revyl skill export --name revyl-cli-create -o SKILL.md`,
	Args: cobra.NoArgs,
	RunE: runSkillExport,
}

var (
	skillShowName      string
	skillExportName    string
	skillExportOutput  string
	skillInstallNames  []string
	skillInstallCLI    bool
	skillInstallMCP    bool
	skillInstallCursor bool
	skillInstallClaude bool
	skillInstallCodex  bool
	skillInstallGlobal bool
	skillInstallForce  bool
)

// skillInstallCmd installs embedded skills to the appropriate directory.
var skillInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install Revyl agent skills for your AI coding tool",
	Long: `Choose and install Revyl agent skill packages.

Without skill selectors, opens an interactive picker with no skills selected.
In scripts, specify --name (or --skill), --cli, --mcp, or --all explicitly.
--yes skips confirmation, but never selects the entire catalog implicitly.

Packages are shared in .agents/skills, with per-skill Claude compatibility links.
Cursor and Codex discover the shared directory directly. --copy keeps independent
packages in each selected agent's directory instead. Use --global for user scope.
Existing legacy installations are preserved; move them aside before switching to
shared storage, or use --copy to keep their existing locations.
Cursor Marketplace plugin users do not need this command because the plugin
already bundles its CLI-first skills and routing rule.

EXAMPLES:
  revyl skill install
  revyl skill install --name revyl-cli-dev-loop --cursor
  revyl skill install --skill revyl-cli-create --agent codex --yes
  revyl skill install --name revyl-cli-auth-bypass --claude --global
  revyl skill install --all --cursor --yes`,
	Args: cobra.NoArgs,
	RunE: runSkillInstall,
}

func init() {
	// show flags
	skillShowCmd.Flags().StringVar(&skillShowName, "name", "", "Skill name to print (required)")

	// export flags
	skillExportCmd.Flags().StringVar(&skillExportName, "name", "", "Skill name to export (required)")
	skillExportCmd.Flags().StringVarP(&skillExportOutput, "output", "o", "SKILL.md", "Output file path")

	// install flags
	addInstallTargetFlags(skillInstallCmd)
	skillInstallCmd.Flags().StringSliceVar(&skillInstallNames, "name", nil, "Skill name(s) to install (repeatable)")

	// Register subcommands
	skillCmd.AddCommand(skillListCmd)
	skillCmd.AddCommand(skillShowCmd)
	skillCmd.AddCommand(skillExportCmd)
	skillCmd.AddCommand(skillInstallCmd)
	registerSkillShortcutCommands()
}

func runSkillList(cmd *cobra.Command, args []string) error {
	return printSkillCatalog(cmd)
}

// runSkillShow prints a selected embedded SKILL.md to stdout.
func runSkillShow(cmd *cobra.Command, args []string) error {
	selected, err := resolveNamedSkill(skillShowName)
	if err != nil {
		return err
	}
	fmt.Print(selected.Content)
	return nil
}

// runSkillExport writes a selected embedded SKILL.md to a file on disk.
func runSkillExport(cmd *cobra.Command, args []string) error {
	selected, err := resolveNamedSkill(skillExportName)
	if err != nil {
		return err
	}

	outputPath := skillExportOutput

	// Create parent directory if needed
	dir := filepath.Dir(outputPath)
	if dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	if err := os.WriteFile(outputPath, []byte(selected.Content), 0644); err != nil {
		return fmt.Errorf("failed to write skill file: %w", err)
	}

	ui.PrintSuccess("Exported %s to %s", selected.Name, outputPath)
	return nil
}

func runSkillInstall(cmd *cobra.Command, args []string) error {
	return runSkillInstallSelected(cmd, args, skillInstallNames)
}

func runSkillInstallSelected(cmd *cobra.Command, args []string, selectedNames []string) error {
	return installSelectedSkills(cmd, selectedNames)
}

func installSkillsToTargets(targets []skillInstallTarget, selected []skillcatalog.Skill, force bool) error {
	_, err := applySkillInstall(targets, selected, force, false)
	return err
}

func resolveInstallSkills(selectedNames []string) ([]skillcatalog.Skill, error) {
	if len(selectedNames) > 0 && (skillInstallCLI || skillInstallMCP) {
		return nil, fmt.Errorf("--name cannot be combined with --cli or --mcp")
	}

	if len(selectedNames) == 0 {
		return resolveInstallSkillsByFamily(skillInstallCLI, skillInstallMCP)
	}

	available := strings.Join(skillcatalog.Names(), ", ")
	resolved := make([]skillcatalog.Skill, 0, len(selectedNames))
	seen := make(map[string]struct{}, len(selectedNames))

	for _, raw := range selectedNames {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		sk, ok := skillcatalog.Get(name)
		if !ok {
			return nil, fmt.Errorf("unknown skill %q. Available skills: %s", name, available)
		}
		if _, ok := seen[sk.Name]; ok {
			continue
		}
		resolved = append(resolved, sk)
		seen[sk.Name] = struct{}{}
	}

	if len(resolved) == 0 {
		return nil, fmt.Errorf("no valid skill names provided. Available skills: %s", available)
	}
	return resolved, nil
}

func resolveInstallSkillsByFamily(includeCLI bool, includeMCP bool) ([]skillcatalog.Skill, error) {
	if !includeCLI && !includeMCP {
		return nil, fmt.Errorf("no skill families selected")
	}

	all := skillcatalog.All()
	filtered := make([]skillcatalog.Skill, 0, len(all))
	for _, sk := range all {
		if includeCLI && strings.HasPrefix(sk.Name, skillFamilyCLIPrefix) {
			filtered = append(filtered, sk)
			continue
		}
		if includeMCP && strings.HasPrefix(sk.Name, skillFamilyMCPPrefix) {
			filtered = append(filtered, sk)
		}
	}

	if len(filtered) == 0 {
		return nil, fmt.Errorf("no skills matched the selected family filters")
	}
	return filtered, nil
}

func resolveNamedSkill(name string) (skillcatalog.Skill, error) {
	name = strings.TrimSpace(name)
	available := strings.Join(skillcatalog.Names(), ", ")
	if name == "" {
		return skillcatalog.Skill{}, fmt.Errorf("--name is required. Available skills: %s", available)
	}

	selected, ok := skillcatalog.Get(name)
	if !ok {
		return skillcatalog.Skill{}, fmt.Errorf("unknown skill %q. Available skills: %s", name, available)
	}
	return selected, nil
}

func addInstallTargetFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&skillInstallCLI, "cli", false, "Install CLI skill family")
	cmd.Flags().BoolVar(&skillInstallMCP, "mcp", false, "Install MCP skill family")
	cmd.Flags().BoolVar(&skillInstallCursor, "cursor", false, "Install for Cursor")
	cmd.Flags().BoolVar(&skillInstallClaude, "claude", false, "Install for Claude Code")
	cmd.Flags().BoolVar(&skillInstallCodex, "codex", false, "Install for Codex")
	cmd.Flags().BoolVar(&skillInstallGlobal, "global", false, "Install to user-level (global) directory instead of project-level")
	cmd.Flags().BoolVar(&skillInstallForce, "force", false, "Overwrite existing skill installations")
	cmd.Flags().BoolVar(&skillInstallAll, "all", false, "Explicitly select every CLI and MCP skill")
	cmd.Flags().BoolVarP(&skillInstallYes, "yes", "y", false, "Skip confirmation without selecting additional skills")
	cmd.Flags().BoolVar(&skillInstallCopy, "copy", false, "Copy to agent directories instead of using shared storage and links")
	cmd.Flags().BoolVar(&skillInstallJSON, "json", false, "Output structured JSON without interactive prompts")
	cmd.Flags().StringSliceVarP(&skillInstallAgents, "agent", "a", nil, "Agent integration(s): cursor, codex, claude-code")
}

func registerSkillShortcutCommands() {
	for _, sk := range skillcatalog.All() {
		selected := sk
		skillNameCmd := &cobra.Command{
			Use:   selected.Name,
			Short: fmt.Sprintf("Operations for %s", selected.Name),
		}

		installOneCmd := &cobra.Command{
			Use:   "install",
			Short: fmt.Sprintf("Install only %s", selected.Name),
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runSkillInstallSelected(cmd, args, []string{selected.Name})
			},
		}
		addInstallTargetFlags(installOneCmd)

		skillNameCmd.AddCommand(installOneCmd)
		skillCmd.AddCommand(skillNameCmd)
	}
}

// resolveInstallTargets determines which directories to install the skills to
// based on the provided flags and auto-detection.
func resolveInstallTargets() []skillInstallTarget {
	// If explicit tool flags are set, use those
	explicitTools := make([]string, 0)
	if skillInstallCursor {
		explicitTools = append(explicitTools, "cursor")
	}
	if skillInstallClaude {
		explicitTools = append(explicitTools, "claude")
	}
	if skillInstallCodex {
		explicitTools = append(explicitTools, "codex")
	}

	if len(explicitTools) > 0 {
		return resolveDirectories(explicitTools)
	}

	// Auto-detect: check which tool directories exist
	detected := make([]string, 0)
	for _, toolName := range supportedSkillTools {
		if skillToolPresent(skillDirectories[toolName]) {
			detected = append(detected, toolName)
		}
	}

	if len(detected) == 0 {
		return nil
	}

	return resolveDirectories(detected)
}

func skillToolPresent(dirs []string) bool {
	for _, dir := range dirs {
		expanded := expandHome(dir)
		for _, candidate := range []string{expanded, filepath.Dir(expanded)} {
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				return true
			}
		}
	}
	return false
}

// resolveDirectories maps tool names to their target install directories,
// respecting the --global flag.
func resolveDirectories(tools []string) []skillInstallTarget {
	return resolveDirectoriesForScope(tools, skillInstallGlobal)
}

func resolveDirectoriesForScope(tools []string, global bool) []skillInstallTarget {
	targets := make([]skillInstallTarget, 0, len(tools))

	for _, toolName := range tools {
		dirs, ok := skillDirectories[toolName]
		if !ok {
			continue
		}

		// dirs[0] = project-level, dirs[1] = user-level (global)
		idx := 0
		if global {
			idx = 1
		}

		if idx < len(dirs) {
			targets = append(targets, skillInstallTarget{
				tool:   toolName,
				path:   expandHome(dirs[idx]),
				global: global,
			})
		}
	}

	return targets
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}

	home, err := os.UserHomeDir()
	if err != nil {
		// Fallback for edge cases
		if runtime.GOOS == "windows" {
			home = os.Getenv("USERPROFILE")
		} else {
			home = os.Getenv("HOME")
		}
	}

	return filepath.Join(home, path[1:])
}
