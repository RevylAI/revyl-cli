package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/skillcatalog"
	"github.com/revyl/cli/internal/ui"
)

var (
	skillInstallAll    bool
	skillInstallYes    bool
	skillInstallCopy   bool
	skillInstallJSON   bool
	skillInstallAgents []string
	skillListAll       bool
	skillListInstalled bool
	skillListJSON      bool
	skillUpdateGlobal  bool
	skillUpdateJSON    bool
	skillInputIsTTY    = ui.IsInteractive
	selectAgentSkills  = ui.MultiSelect
	confirmSkillPlan   = ui.PromptConfirm
)

type skillInstallEntry struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type skillInstallResult struct {
	Skills []skillInstallEntry `json:"skills"`
}

var skillUpdateCmd = &cobra.Command{
	Use:   "update [skill-name...]",
	Short: "Update installed Revyl-managed skills without overwriting local edits",
	Long:  "Update installed, unmodified Revyl skill packages from this CLI version. Unmanaged packages and local edits are preserved. No additional skills are installed.",
	RunE:  runSkillUpdate,
}

func init() {
	skillInstallCmd.Aliases = []string{"add"}
	skillInstallCmd.Flags().Var(skillInstallCmd.Flags().Lookup("name").Value, "skill", "Skill name(s) to install (alias of --name)")
	skillListCmd.Flags().BoolVar(&skillListAll, "all", false, "Include optional and compatibility skills")
	skillListCmd.Flags().BoolVar(&skillListInstalled, "installed", false, "List installed project and global skills")
	skillListCmd.Flags().BoolVar(&skillListJSON, "json", false, "Output structured JSON")
	skillUpdateCmd.Flags().BoolVar(&skillUpdateGlobal, "global", false, "Update global skills instead of project skills")
	skillUpdateCmd.Flags().BoolVar(&skillUpdateJSON, "json", false, "Output structured JSON")
	skillCmd.AddCommand(skillUpdateCmd)
}

func chooseInstallSkills(names []string) ([]skillcatalog.Skill, error) {
	if skillInstallAll {
		if len(names) > 0 || skillInstallCLI || skillInstallMCP {
			return nil, fmt.Errorf("--all cannot be combined with --name, --skill, --cli, or --mcp")
		}
		return skillcatalog.All(), nil
	}
	if len(names) > 0 || skillInstallCLI || skillInstallMCP {
		return resolveInstallSkills(names)
	}
	if skillInstallYes || skillInstallJSON || !skillInputIsTTY() {
		return nil, fmt.Errorf("choose skills explicitly with --name <skill> or --all; --yes does not select skills")
	}
	return promptSkillSelection()
}

func promptSkillSelection() ([]skillcatalog.Skill, error) {
	options := make([]ui.SelectOption, 0, len(skillcatalog.All()))
	public := make(map[string]bool)
	for _, skill := range skillcatalog.Public() {
		public[skill.Name] = true
		options = append(options, ui.SelectOption{Label: skill.Name, Value: skill.Name, Description: skill.Description})
	}
	for _, skill := range skillcatalog.All() {
		if !public[skill.Name] {
			options = append(options, ui.SelectOption{Label: skill.Name + " (optional)", Value: skill.Name, Description: skill.Description})
		}
	}
	selected, err := selectAgentSkills("Choose skills to install (none selected skips installation)", options, nil)
	if err != nil || len(selected) == 0 {
		return nil, err
	}
	result := make([]skillcatalog.Skill, 0, len(selected))
	for _, name := range selected {
		skill, ok := skillcatalog.Get(name)
		if !ok {
			return nil, fmt.Errorf("unknown selected skill %q", name)
		}
		result = append(result, skill)
	}
	return result, nil
}

func chooseSkillTargets() ([]skillInstallTarget, error) {
	explicit := skillInstallCursor || skillInstallClaude || skillInstallCodex || len(skillInstallAgents) > 0
	tools := make([]string, 0, 3)
	for _, agent := range skillInstallAgents {
		switch agent {
		case "cursor", "codex":
			tools = append(tools, agent)
		case "claude", "claude-code":
			tools = append(tools, "claude")
		default:
			return nil, fmt.Errorf("unknown agent %q; choose cursor, codex, or claude-code", agent)
		}
	}
	targets := resolveInstallTargets()
	if explicit {
		if skillInstallCursor || skillInstallClaude || skillInstallCodex {
			for _, target := range targets {
				tools = append(tools, target.tool)
			}
		}
	} else {
		for _, target := range targets {
			tools = append(tools, target.tool)
		}
		if skillInputIsTTY() && !skillInstallYes && !skillInstallJSON {
			options := []ui.SelectOption{
				{Label: "Cursor", Value: "cursor", Description: "Discovers the shared .agents/skills directory"},
				{Label: "Claude Code", Value: "claude", Description: "Uses per-skill compatibility links in .claude/skills"},
				{Label: "Codex", Value: "codex", Description: "Discovers the shared .agents/skills directory"},
			}
			var err error
			tools, err = selectAgentSkills("Choose agent integrations", options, tools)
			if err != nil {
				return nil, err
			}
		}
	}
	if len(tools) == 0 {
		return nil, fmt.Errorf("no agent selected or detected; pass --agent cursor, --agent codex, or --agent claude-code")
	}
	sort.Strings(tools)
	unique := tools[:0]
	for _, tool := range tools {
		if len(unique) == 0 || unique[len(unique)-1] != tool {
			unique = append(unique, tool)
		}
	}
	return resolveDirectoriesForScope(unique, skillInstallGlobal), nil
}

func installSelectedSkills(cmd *cobra.Command, names []string) (returnErr error) {
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{Domain: "skill_install", DomainStatus: "failed"})
	defer func() {
		if errors.Is(returnErr, ui.ErrSelectionCancelled) {
			completion := analytics.CommandCompletion{ExitCode: 1, Domain: "skill_install", DomainStatus: "cancelled"}
			analytics.SetCommandCompletion(cmd.Context(), completion)
			returnErr = analytics.CompletedWithExitCode(returnErr, completion)
		}
	}()
	selected, err := chooseInstallSkills(names)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{Domain: "skill_install", DomainStatus: "skipped"})
		ui.PrintInfo("No skills selected; nothing changed.")
		return nil
	}
	targets, err := chooseSkillTargets()
	if err != nil {
		return err
	}
	if skillInputIsTTY() && !skillInstallYes && !skillInstallJSON {
		for _, selectedSkill := range selected {
			ui.PrintInfo("  %s", selectedSkill.Name)
		}
		if skillInstallCopy {
			ui.PrintInfo("Copy packages to the selected agent directories.")
		} else {
			ui.PrintInfo("Shared packages: %s", skillCanonicalBase(targets[0]))
			ui.PrintDim("Other agents that read .agents/skills can also discover these packages.")
		}
		for _, target := range targets {
			ui.PrintDim("  Agent: %s", target.tool)
		}
		if skillInstallForce {
			ui.PrintWarning("--force replaces the selected existing packages, including local edits.")
		}
		confirmed, confirmErr := confirmSkillPlan("Install the selected skills?", true)
		if confirmErr != nil {
			return confirmErr
		}
		if !confirmed {
			analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{Domain: "skill_install", DomainStatus: "skipped"})
			return nil
		}
	}
	result, installErr := applySkillInstall(targets, selected, skillInstallForce, skillInstallCopy)
	if skillInstallJSON {
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
			return err
		}
	}
	if installErr == nil {
		status := "unchanged"
		for _, entry := range result.Skills {
			if entry.Status == "installed" {
				status = "installed"
			}
		}
		analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{Domain: "skill_install", DomainStatus: status})
	}
	return installErr
}

func applySkillInstall(targets []skillInstallTarget, selected []skillcatalog.Skill, force, copyMode bool) (skillInstallResult, error) {
	result := skillInstallResult{Skills: []skillInstallEntry{}}
	if len(targets) == 0 || len(selected) == 0 {
		return result, fmt.Errorf("an installation requires at least one skill and one target")
	}
	for _, target := range targets {
		root := filepath.Dir(filepath.Dir(target.path))
		if err := validateSkillDestination(target.path, root); err != nil {
			return result, err
		}
		if !copyMode {
			if err := validateSkillDestination(skillCanonicalBase(target), root); err != nil {
				return result, err
			}
		}
	}
	if !copyMode {
		for _, target := range targets {
			for _, skill := range selected {
				legacyNames := []string{skill.Name}
				if skill.Name == "revyl-cli-auth-bypass" {
					legacyNames = append(legacyNames, skillcatalog.AuthBypassLeafAliases()...)
				}
				for _, agentDir := range []string{".cursor", ".claude", ".codex"} {
					for _, name := range legacyNames {
						legacy := filepath.Join(filepath.Dir(filepath.Dir(target.path)), agentDir, "skills", name)
						if _, err := os.Lstat(legacy); errors.Is(err, os.ErrNotExist) {
							continue
						} else if err != nil {
							return result, err
						}
						actual, actualErr := filepath.EvalSymlinks(legacy)
						canonical, canonicalErr := filepath.EvalSymlinks(filepath.Join(skillCanonicalBase(target), skill.Name))
						if actualErr != nil || canonicalErr != nil || actual != canonical {
							return result, fmt.Errorf("existing installation at %s is preserved; move it aside before shared installation, or use --copy to keep its current location", legacy)
						}
					}
				}
			}
		}
		for _, target := range targets {
			if err := validateSkillLinkSupport(target, selected); err != nil {
				return result, err
			}
		}
	}
	seen := make(map[string]bool)
	var failures []error
	for _, target := range targets {
		base := skillCanonicalBase(target)
		if copyMode {
			base = target.path
		}
		for _, skill := range selected {
			dir := filepath.Join(base, skill.Name)
			key, err := filepath.Abs(dir)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if !seen[key] {
				path, wrote, err := installSkillTo(base, skill, force)
				status := "unchanged"
				if wrote {
					status = "installed"
				}
				if err != nil {
					status = "failed"
					failures = append(failures, fmt.Errorf("%s: %w", skill.Name, err))
				}
				entry := skillInstallEntry{Name: skill.Name, Path: filepath.Dir(path), Status: status}
				if err != nil {
					entry.Reason = err.Error()
				}
				result.Skills = append(result.Skills, entry)
				if err != nil {
					continue
				}
				seen[key] = true
				ui.PrintInfo("%s: %s (%s)", skill.Name, status, path)
			}
			if !copyMode {
				if err := linkSkillForTarget(target, skill.Name, dir); err != nil {
					failures = append(failures, err)
					result.Skills = append(result.Skills, skillInstallEntry{Name: skill.Name, Path: filepath.Join(target.path, skill.Name), Status: "failed", Reason: err.Error()})
				}
			}
		}
	}
	if len(failures) == 0 {
		ui.PrintDim("Reload your agent's skills or restart it if the new skills do not appear.")
	}
	return result, errors.Join(failures...)
}

func installedSkillPaths(global bool) ([]skillInstallEntry, error) {
	root, err := os.Getwd()
	if global {
		root, err = os.UserHomeDir()
	}
	if err != nil {
		return nil, err
	}
	result := []skillInstallEntry{}
	seen := make(map[string]bool)
	for _, toolDir := range []string{".agents", ".cursor", ".claude", ".codex"} {
		base := filepath.Join(root, toolDir, "skills")
		entries, err := os.ReadDir(base)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
				continue
			}
			path := filepath.Join(base, entry.Name())
			if err := validateSkillDestination(path, root); err != nil {
				return nil, err
			}
			realPath, err := filepath.EvalSymlinks(path)
			if err != nil {
				return nil, fmt.Errorf("resolve installed skill %s: %w", path, err)
			}
			if seen[realPath] {
				continue
			}
			skill, known := skillcatalog.Get(entry.Name())
			skillName := skill.Name
			if !known {
				state, err := readInstalledSkillState(realPath)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					return nil, fmt.Errorf("read installed skill %s: %w", path, err)
				}
				skillName = state.Name
			}
			if _, err := os.Stat(filepath.Join(realPath, skillcatalog.SkillFileName)); err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					return nil, err
				}
				if _, stateErr := os.Lstat(filepath.Join(realPath, skillInstallStateFile)); errors.Is(stateErr, os.ErrNotExist) {
					continue
				} else if stateErr != nil {
					return nil, stateErr
				}
			}
			seen[realPath] = true
			result = append(result, skillInstallEntry{Name: skillName, Path: realPath, Status: "installed"})
		}
	}
	return result, nil
}

func runSkillUpdate(cmd *cobra.Command, names []string) error {
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{Domain: "skill_update", DomainStatus: "failed"})
	requested := make(map[string]bool, len(names))
	for _, name := range names {
		skill, ok := skillcatalog.Get(name)
		if !ok {
			return fmt.Errorf("unknown skill %q", name)
		}
		requested[skill.Name] = true
	}
	installed, err := installedSkillPaths(skillUpdateGlobal)
	if err != nil {
		return err
	}
	for name := range requested {
		found := false
		for _, entry := range installed {
			found = found || entry.Name == name
		}
		if !found {
			return fmt.Errorf("%s is not installed in this scope", name)
		}
	}
	result := skillInstallResult{Skills: []skillInstallEntry{}}
	var failures []error
	for _, entry := range installed {
		if len(requested) > 0 && !requested[entry.Name] {
			continue
		}
		if err := validateManagedSkill(entry.Path, entry.Name); err != nil {
			entry.Status = "preserved"
			entry.Reason = err.Error()
			failures = append(failures, fmt.Errorf("%s: %w", entry.Name, err))
		} else {
			skill, _ := skillcatalog.Get(entry.Name)
			_, _, err := installSkillTo(filepath.Dir(entry.Path), skill, true)
			entry.Status = "updated"
			if err != nil {
				entry.Status = "failed"
				entry.Reason = err.Error()
				failures = append(failures, fmt.Errorf("%s: %w", entry.Name, err))
			}
		}
		result.Skills = append(result.Skills, entry)
		ui.PrintInfo("%s: %s", entry.Name, entry.Status)
	}
	if len(result.Skills) == 0 && len(failures) == 0 {
		ui.PrintInfo("No installed skills in this scope; nothing changed.")
	}
	if skillUpdateJSON {
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
			return err
		}
	}
	if len(failures) == 0 {
		analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{Domain: "skill_update", DomainStatus: "completed"})
	}
	return errors.Join(failures...)
}

func printSkillCatalog(cmd *cobra.Command) error {
	if skillListInstalled {
		entries := []skillInstallEntry{}
		seen := make(map[string]bool)
		for _, global := range []bool{false, true} {
			installed, err := installedSkillPaths(global)
			if err != nil {
				return err
			}
			for _, entry := range installed {
				if !seen[entry.Path] {
					entries = append(entries, entry)
					seen[entry.Path] = true
				}
			}
		}
		if skillListJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(skillInstallResult{Skills: entries})
		}
		for _, entry := range entries {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", entry.Name, entry.Path)
		}
		return nil
	}
	catalog := skillcatalog.Public()
	if skillListAll {
		catalog = skillcatalog.All()
	}
	type catalogEntry struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	entries := make([]catalogEntry, 0, len(catalog))
	for _, skill := range catalog {
		entries = append(entries, catalogEntry{Name: skill.Name, Description: skill.Description})
	}
	if skillListJSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			Skills []catalogEntry `json:"skills"`
		}{Skills: entries})
	}
	for _, entry := range entries {
		fmt.Fprintf(cmd.OutOrStdout(), "%s — %s\n", entry.Name, entry.Description)
	}
	ui.PrintDim("Choose skills with revyl skill install, or pass --name <skill>. Use list --all for optional skills.")
	return nil
}
