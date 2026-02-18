// Package main provides the skill command for managing the Revyl agent skill.
//
// The agent skill teaches AI assistants (Cursor, Claude Code, Codex, VS Code)
// optimal usage patterns for Revyl device tools. It is embedded in the binary
// at compile time and can be installed to any supported skill directory.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/skill"
	"github.com/revyl/cli/internal/ui"
)

// Supported skill directory locations for each tool, ordered by preference.
// Project-level directories are listed first, user-level (global) second.
var skillDirectories = map[string][]string{
	"cursor": {".cursor/skills", "~/.cursor/skills"},
	"claude": {".claude/skills", "~/.claude/skills"},
	"codex":  {".codex/skills", "~/.codex/skills"},
}

// skillCmd is the parent command for agent skill management.
var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Manage the Revyl agent skill",
	Long: `Manage the Revyl agent skill for AI coding tools.

The agent skill teaches AI assistants (Cursor, Claude Code, Codex)
optimal usage patterns for Revyl device tools including session
management, grounding, and troubleshooting workflows.

The skill is embedded in the CLI binary and can be installed to
any supported tool with a single command.

EXAMPLES:
  revyl skill install              # Auto-detect tool and install
  revyl skill install --cursor     # Install for Cursor
  revyl skill install --claude     # Install for Claude Code
  revyl skill install --codex      # Install for Codex
  revyl skill install --global     # Install to user-level directory
  revyl skill show                 # Print skill content to stdout
  revyl skill export -o SKILL.md   # Export to a file`,
}

// skillShowCmd prints the embedded SKILL.md content to stdout.
var skillShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the agent skill content to stdout",
	Long: `Print the embedded SKILL.md content to stdout.

Useful for piping into other tools or inspecting the skill content
without installing it.

EXAMPLES:
  revyl skill show                      # Print to terminal
  revyl skill show | pbcopy             # Copy to clipboard (macOS)
  revyl skill show > SKILL.md           # Redirect to file`,
	Args: cobra.NoArgs,
	RunE: runSkillShow,
}

// skillExportCmd writes the embedded SKILL.md to a file.
var skillExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export the agent skill to a file",
	Long: `Export the embedded SKILL.md to a file on disk.

If no output path is specified, writes to ./SKILL.md in the
current directory.

EXAMPLES:
  revyl skill export                          # Write to ./SKILL.md
  revyl skill export -o skills/SKILL.md       # Write to custom path
  revyl skill export -o /tmp/SKILL.md         # Write to absolute path`,
	Args: cobra.NoArgs,
	RunE: runSkillExport,
}

var (
	skillExportOutput  string
	skillInstallCursor bool
	skillInstallClaude bool
	skillInstallCodex  bool
	skillInstallGlobal bool
	skillInstallForce  bool
)

// skillInstallCmd installs the agent skill to the appropriate directory.
var skillInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the agent skill for your AI coding tool",
	Long: `Install the Revyl agent skill to the appropriate directory
for your AI coding tool.

Without flags, auto-detects which tools are present by checking
for their configuration directories. With a tool flag, installs
to that specific tool's skill directory.

By default installs to the project-level directory (e.g. .cursor/skills/).
Use --global to install to the user-level directory instead.

EXAMPLES:
  revyl skill install              # Auto-detect and install
  revyl skill install --cursor     # Install for Cursor (project)
  revyl skill install --global     # Auto-detect, install globally
  revyl skill install --claude     # Install for Claude Code
  revyl skill install --codex      # Install for Codex
  revyl skill install --force      # Overwrite existing installation`,
	Args: cobra.NoArgs,
	RunE: runSkillInstall,
}

func init() {
	// Export flags
	skillExportCmd.Flags().StringVarP(&skillExportOutput, "output", "o", "SKILL.md", "Output file path")

	// Install flags
	skillInstallCmd.Flags().BoolVar(&skillInstallCursor, "cursor", false, "Install for Cursor")
	skillInstallCmd.Flags().BoolVar(&skillInstallClaude, "claude", false, "Install for Claude Code")
	skillInstallCmd.Flags().BoolVar(&skillInstallCodex, "codex", false, "Install for Codex")
	skillInstallCmd.Flags().BoolVar(&skillInstallGlobal, "global", false, "Install to user-level (global) directory instead of project-level")
	skillInstallCmd.Flags().BoolVar(&skillInstallForce, "force", false, "Overwrite existing skill installation")

	// Register subcommands
	skillCmd.AddCommand(skillShowCmd)
	skillCmd.AddCommand(skillExportCmd)
	skillCmd.AddCommand(skillInstallCmd)
}

// runSkillShow prints the embedded SKILL.md to stdout.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (unused, validated as empty by cobra)
//
// Returns:
//   - error: Any error that occurred during output
func runSkillShow(cmd *cobra.Command, args []string) error {
	fmt.Print(skill.SkillContent)
	return nil
}

// runSkillExport writes the embedded SKILL.md to a file on disk.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (unused, validated as empty by cobra)
//
// Returns:
//   - error: If the file cannot be created or written
func runSkillExport(cmd *cobra.Command, args []string) error {
	outputPath := skillExportOutput

	// Create parent directory if needed
	dir := filepath.Dir(outputPath)
	if dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	if err := os.WriteFile(outputPath, []byte(skill.SkillContent), 0644); err != nil {
		return fmt.Errorf("failed to write skill file: %w", err)
	}

	ui.PrintSuccess("Exported skill to %s", outputPath)
	return nil
}

// runSkillInstall installs the agent skill to the appropriate directory.
//
// When no tool flag is provided, auto-detects installed tools by checking
// for their configuration directories. When --global is set, installs to
// the user-level directory (~/.cursor/skills/) instead of project-level.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (unused, validated as empty by cobra)
//
// Returns:
//   - error: If installation fails
func runSkillInstall(cmd *cobra.Command, args []string) error {
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

	var installed []string
	var errors []string

	for _, target := range targets {
		if err := installSkillTo(target); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", target, err))
		} else {
			installed = append(installed, target)
		}
	}

	if len(installed) > 0 {
		ui.Println()
		ui.PrintSuccess("Installed Revyl agent skill to:")
		for _, path := range installed {
			ui.PrintDim("  %s", path)
		}
	}

	if len(errors) > 0 {
		ui.Println()
		ui.PrintWarning("Some installations failed:")
		for _, e := range errors {
			ui.PrintDim("  %s", e)
		}
	}

	if len(installed) > 0 {
		ui.Println()
		ui.PrintInfo("The skill will be automatically discovered by your AI agent.")
		ui.PrintInfo("Restart your IDE if it was already running.")
	}

	if len(installed) == 0 {
		return fmt.Errorf("all installations failed")
	}

	return nil
}

// resolveInstallTargets determines which directories to install the skill to
// based on the provided flags and auto-detection.
//
// Returns:
//   - []string: List of resolved directory paths to install to
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
//
// Parameters:
//   - tools: List of tool names (cursor, claude, codex)
//
// Returns:
//   - []string: Resolved directory paths
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

// installSkillTo writes the SKILL.md file to the given base skill directory.
// Creates the full path: <baseDir>/revyl-device/SKILL.md
//
// Parameters:
//   - baseDir: The skill directory root (e.g. .cursor/skills)
//
// Returns:
//   - error: If the directory cannot be created or file cannot be written
func installSkillTo(baseDir string) error {
	skillDir := filepath.Join(baseDir, skill.SkillName)
	skillPath := filepath.Join(skillDir, skill.SkillFileName)

	// Check if already installed
	if !skillInstallForce {
		if _, err := os.Stat(skillPath); err == nil {
			ui.PrintDim("  Already installed at %s (use --force to overwrite)", skillPath)
			return nil
		}
	}

	// Create directory structure
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", skillDir, err)
	}

	// Write the SKILL.md
	if err := os.WriteFile(skillPath, []byte(skill.SkillContent), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", skillPath, err)
	}

	return nil
}

// expandHome replaces a leading ~ with the user's home directory.
//
// Parameters:
//   - path: File path that may start with ~
//
// Returns:
//   - string: Path with ~ expanded to the actual home directory
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
