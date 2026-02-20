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

var legacySkillNames = []string{
	"revyl-device",
	"revyl-dev-loop",
	"revyl-adhoc-to-test",
	"revyl-device-dev-loop",
	"revyl-create",
	"revyl-analyze",
}

const (
	skillFamilyCLIPrefix = "revyl-cli"
	skillFamilyMCPPrefix = "revyl-mcp"
)

// skillCmd is the parent command for agent skill management.
var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Manage Revyl agent skills",
	Long: `Manage Revyl agent skills for AI coding tools.

Revyl ships embedded skills:
- revyl-cli, revyl-cli-create, revyl-cli-analyze, revyl-cli-dev-loop
- revyl-mcp, revyl-mcp-create, revyl-mcp-analyze, revyl-mcp-dev-loop

EXAMPLES:
  revyl skill list
  revyl skill install                         # Default: install CLI family
  revyl skill install --mcp                   # Install MCP family
  revyl skill install --cli --mcp             # Install both families
  revyl skill install --codex
  revyl skill show --name revyl-cli-dev-loop
  revyl skill show --name revyl-mcp-dev-loop
  revyl skill export --name revyl-mcp-create -o SKILL.md
  revyl skill export --name revyl-cli-analyze -o SKILL.md
  revyl skill revyl-cli-dev-loop install --codex`,
}

var skillListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available embedded skills",
	Long: `List all embedded Revyl skills that can be installed.

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
  revyl skill show --name revyl-cli
  revyl skill show --name revyl-mcp-dev-loop
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
  revyl skill export --name revyl-cli
  revyl skill export --name revyl-mcp-analyze
  revyl skill export --name revyl-mcp-dev-loop -o skills/SKILL.md
  revyl skill export --name revyl-cli-dev-loop -o /tmp/SKILL.md`,
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
	Long: `Install Revyl agent skills to the appropriate directories
for your AI coding tool.

Without flags, auto-detects which tools are present by checking
for their configuration directories. With a tool flag, installs
to that specific tool's skill directory.

By default installs to the project-level directory (e.g. .cursor/skills/).
Use --global to install to the user-level directory instead.

EXAMPLES:
  revyl skill install              # Auto-detect tool; install CLI skill family
  revyl skill install --cli        # Install CLI skill family
  revyl skill install --mcp        # Install MCP skill family
  revyl skill install --cli --mcp  # Install both families
  revyl skill install --name revyl-mcp-dev-loop
  revyl skill install --name revyl-cli-create --name revyl-cli-analyze
  revyl skill install --cursor     # Install for Cursor (project)
  revyl skill install --global     # Auto-detect, install globally
  revyl skill install --claude     # Install for Claude Code
  revyl skill install --codex      # Install for Codex
  revyl skill install --force      # Overwrite existing installations
  revyl skill revyl-mcp-dev-loop install --codex  # Install via shortcut`,
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
	fmt.Println("Available Revyl skills:")
	for _, s := range skillcatalog.All() {
		fmt.Printf("  %s - %s\n", s.Name, s.Description)
	}
	return nil
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

// runSkillInstall installs all embedded skills to each resolved target.
func runSkillInstall(cmd *cobra.Command, args []string) error {
	return runSkillInstallSelected(cmd, args, skillInstallNames)
}

func runSkillInstallSelected(cmd *cobra.Command, args []string, selectedNames []string) error {
	targets := resolveInstallTargets()
	if len(targets) == 0 {
		ui.PrintError("No supported AI tools detected.")
		ui.Println()
		ui.PrintInfo("Specify a tool explicitly:")
		ui.PrintDim("  revyl skill install --cursor")
		ui.PrintDim("  revyl skill install --claude")
		ui.PrintDim("  revyl skill install --codex")
		return fmt.Errorf("no install target found")
	}

	allSkills, err := resolveInstallSkills(selectedNames)
	if err != nil {
		return err
	}

	var installed []string
	var skipped []string
	var installErrors []string
	var pruned []string
	var pruneErrors []string

	for _, target := range targets {
		for _, sk := range allSkills {
			path, wrote, err := installSkillTo(target, sk)
			if err != nil {
				installErrors = append(installErrors, fmt.Sprintf("%s (%s): %v", target, sk.Name, err))
				continue
			}
			if wrote {
				installed = append(installed, path)
			} else {
				skipped = append(skipped, path)
			}
		}

		removed, errs := pruneLegacySkillDirs(target, allSkills)
		pruned = append(pruned, removed...)
		pruneErrors = append(pruneErrors, errs...)
	}

	if len(installed) > 0 {
		ui.Println()
		ui.PrintSuccess("Installed Revyl skills:")
		for _, path := range installed {
			ui.PrintDim("  %s", path)
		}
	}

	if len(skipped) > 0 {
		ui.Println()
		ui.PrintInfo("Already installed (use --force to overwrite):")
		for _, path := range skipped {
			ui.PrintDim("  %s", path)
		}
	}

	if len(pruned) > 0 {
		ui.Println()
		ui.PrintInfo("Removed legacy Revyl skill folders:")
		for _, path := range pruned {
			ui.PrintDim("  %s", path)
		}
	}

	if len(installErrors) > 0 {
		ui.Println()
		ui.PrintWarning("Some installations failed:")
		for _, e := range installErrors {
			ui.PrintDim("  %s", e)
		}
	}

	if len(pruneErrors) > 0 {
		ui.Println()
		ui.PrintWarning("Could not remove some legacy skill folders:")
		for _, e := range pruneErrors {
			ui.PrintDim("  %s", e)
		}
	}

	if len(installed) == 0 && len(skipped) == 0 {
		return fmt.Errorf("all installations failed")
	}

	ui.Println()
	ui.PrintInfo("Skills are auto-discovered by your AI agent on startup.")
	ui.PrintInfo("Restart your IDE if it was already running.")
	return nil
}

func resolveInstallSkills(selectedNames []string) ([]skillcatalog.Skill, error) {
	if len(selectedNames) > 0 && (skillInstallCLI || skillInstallMCP) {
		return nil, fmt.Errorf("--name cannot be combined with --cli or --mcp")
	}

	if len(selectedNames) == 0 {
		installCLI := skillInstallCLI
		installMCP := skillInstallMCP

		// Default behavior: install CLI family when no selector is provided.
		if !installCLI && !installMCP {
			installCLI = true
		}
		return resolveInstallSkillsByFamily(installCLI, installMCP)
	}

	available := strings.Join(skillcatalog.Names(), ", ")
	resolved := make([]skillcatalog.Skill, 0, len(selectedNames))
	seen := make(map[string]struct{}, len(selectedNames))

	for _, raw := range selectedNames {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		sk, ok := skillcatalog.Get(name)
		if !ok {
			return nil, fmt.Errorf("unknown skill %q. Available skills: %s", name, available)
		}
		resolved = append(resolved, sk)
		seen[name] = struct{}{}
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

func pruneLegacySkillDirs(baseDir string, selected []skillcatalog.Skill) ([]string, []string) {
	selectedNames := make(map[string]struct{}, len(selected))
	for _, sk := range selected {
		selectedNames[sk.Name] = struct{}{}
	}

	var removed []string
	var errs []string

	for _, legacyName := range legacySkillNames {
		if _, keep := selectedNames[legacyName]; keep {
			continue
		}

		legacyDir := filepath.Join(baseDir, legacyName)
		legacySkillPath := filepath.Join(legacyDir, skillcatalog.SkillFileName)
		if _, err := os.Stat(legacySkillPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			errs = append(errs, fmt.Sprintf("%s: %v", legacySkillPath, err))
			continue
		}

		if err := os.RemoveAll(legacyDir); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", legacyDir, err))
			continue
		}
		removed = append(removed, legacyDir)
	}

	return removed, errs
}

// resolveInstallTargets determines which directories to install the skills to
// based on the provided flags and auto-detection.
func resolveInstallTargets() []string {
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
	for toolName, dirs := range skillDirectories {
		for _, dir := range dirs {
			expanded := expandHome(dir)
			if _, err := os.Stat(expanded); err == nil {
				detected = append(detected, toolName)
				break
			}
		}
	}

	if len(detected) == 0 {
		return nil
	}

	return resolveDirectories(detected)
}

// resolveDirectories maps tool names to their target install directories,
// respecting the --global flag.
func resolveDirectories(tools []string) []string {
	paths := make([]string, 0, len(tools))

	for _, toolName := range tools {
		dirs, ok := skillDirectories[toolName]
		if !ok {
			continue
		}

		// dirs[0] = project-level, dirs[1] = user-level (global)
		idx := 0
		if skillInstallGlobal {
			idx = 1
		}

		if idx < len(dirs) {
			paths = append(paths, expandHome(dirs[idx]))
		}
	}

	return paths
}

// installSkillTo writes the selected SKILL.md file to the given base skill directory.
// Creates: <baseDir>/<skill-name>/SKILL.md
func installSkillTo(baseDir string, selected skillcatalog.Skill) (string, bool, error) {
	skillDir := filepath.Join(baseDir, selected.Name)
	skillPath := filepath.Join(skillDir, skillcatalog.SkillFileName)

	if !skillInstallForce {
		if _, err := os.Stat(skillPath); err == nil {
			return skillPath, false, nil
		} else if !os.IsNotExist(err) {
			return skillPath, false, fmt.Errorf("failed to check existing skill file: %w", err)
		}
	}

	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return skillPath, false, fmt.Errorf("failed to create directory %s: %w", skillDir, err)
	}

	if err := os.WriteFile(skillPath, []byte(selected.Content), 0644); err != nil {
		return skillPath, false, fmt.Errorf("failed to write %s: %w", skillPath, err)
	}

	return skillPath, true, nil
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
