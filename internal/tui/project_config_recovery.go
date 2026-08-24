package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/projectpublication"
)

type projectConfigState string

const (
	projectConfigStateConfigured  projectConfigState = "configured"
	projectConfigStateMissing     projectConfigState = "missing"
	projectConfigStateLegacy      projectConfigState = "legacy"
	projectConfigStateInvalid     projectConfigState = "invalid"
	projectConfigStateSelection   projectConfigState = "selection_required"
	projectConfigStateUnavailable projectConfigState = "unavailable"
)

type projectConfigStatus struct {
	State            projectConfigState
	WorkingDirectory string
	CommandRoot      string
	ConfigPath       string
	Project          *config.ProjectContext
	CandidateRoots   []string
	Err              error
}

type projectConfigRecovery struct {
	Summary            string
	Command            string
	AlternativeCommand string
	PreviewCommand     string
	CandidateCommands  []string
	Action             setupAction
}

type nestedProjectSelectionError struct {
	workingDirectory string
	roots            []string
}

func (e *nestedProjectSelectionError) Error() string {
	if len(e.roots) == 1 {
		return fmt.Sprintf("a Revyl project exists below %q and must be selected explicitly", e.workingDirectory)
	}
	return fmt.Sprintf("multiple Revyl projects exist below %q and one must be selected explicitly", e.workingDirectory)
}

type projectConfigRecoveryError struct {
	cause    error
	recovery projectConfigRecovery
}

func (e *projectConfigRecoveryError) Error() string {
	if e == nil {
		return ""
	}
	if len(e.recovery.CandidateCommands) > 0 {
		return fmt.Sprintf(
			"%s; choose one project command: %s",
			e.recovery.Summary,
			strings.Join(e.recovery.CandidateCommands, ", "),
		)
	}
	if e.recovery.AlternativeCommand != "" {
		return fmt.Sprintf(
			"%s; run '%s' to restore an existing project, or '%s' to create a new one",
			e.recovery.Summary,
			e.recovery.Command,
			e.recovery.AlternativeCommand,
		)
	}
	return fmt.Sprintf("%s; run '%s'", e.recovery.Summary, e.recovery.Command)
}

func (e *projectConfigRecoveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func currentProjectConfigStatus() projectConfigStatus {
	cwd, err := os.Getwd()
	if err != nil {
		return projectConfigStatus{State: projectConfigStateUnavailable, Err: err}
	}
	commandRoot := selectedProjectCommandRoot(cwd)
	fileContext, err := config.ResolveConfigFileContext(cwd, "")
	if err != nil {
		var configErr *config.ConfigError
		if errors.As(err, &configErr) && configErr.Code == "config_not_found" {
			candidateRoots, nestedErr := config.FindNestedProjectRoots(cwd)
			if nestedErr != nil {
				return projectConfigStatus{State: projectConfigStateUnavailable, WorkingDirectory: cwd, CommandRoot: commandRoot, Err: nestedErr}
			}
			if len(candidateRoots) > 0 {
				selectionErr := &nestedProjectSelectionError{workingDirectory: cwd, roots: candidateRoots}
				return projectConfigStatus{
					State:            projectConfigStateSelection,
					WorkingDirectory: cwd,
					CommandRoot:      commandRoot,
					CandidateRoots:   candidateRoots,
					Err:              selectionErr,
				}
			}
			return projectConfigStatus{State: projectConfigStateMissing, WorkingDirectory: cwd, CommandRoot: commandRoot, Err: err}
		}
		return projectConfigStatus{State: projectConfigStateUnavailable, WorkingDirectory: cwd, CommandRoot: commandRoot, Err: err}
	}
	project, err := config.ResolveProjectContext(cwd, "")
	if err != nil {
		var configErr *config.ConfigError
		if errors.As(err, &configErr) && (configErr.Code == "legacy_config_requires_migration" || configErr.Code == "mixed_config_formats") {
			return projectConfigStatus{State: projectConfigStateLegacy, WorkingDirectory: cwd, CommandRoot: commandRoot, ConfigPath: fileContext.ConfigPath, Err: err}
		}
		return projectConfigStatus{State: projectConfigStateInvalid, WorkingDirectory: cwd, CommandRoot: commandRoot, ConfigPath: fileContext.ConfigPath, Err: err}
	}
	return projectConfigStatus{State: projectConfigStateConfigured, WorkingDirectory: cwd, CommandRoot: commandRoot, ConfigPath: fileContext.ConfigPath, Project: project}
}

func selectedProjectCommandRoot(workingDirectory string) string {
	for index := 1; index < len(os.Args); index++ {
		argument := os.Args[index]
		switch {
		case argument == "-C" || argument == "--chdir":
			if index+1 < len(os.Args) && strings.TrimSpace(os.Args[index+1]) != "" {
				return workingDirectory
			}
		case strings.HasPrefix(argument, "--chdir=") && strings.TrimSpace(strings.TrimPrefix(argument, "--chdir=")) != "":
			return workingDirectory
		case strings.HasPrefix(argument, "-C") && len(argument) > len("-C"):
			return workingDirectory
		}
	}
	return ""
}

func projectConfigRecoveryForError(err error, workingDirectory string) (projectConfigRecovery, bool) {
	var actionable *projectConfigRecoveryError
	if errors.As(err, &actionable) {
		return actionable.recovery, true
	}
	var selectionErr *nestedProjectSelectionError
	if errors.As(err, &selectionErr) {
		return nestedProjectConfigRecovery(selectionErr.roots), true
	}
	var publicationErr *projectpublication.Error
	if errors.As(err, &publicationErr) && publicationErr.Code == "project_removed" {
		return projectConfigRecovery{
			Summary: "This Revyl project was deleted; the local configuration still works for local-only commands, but it cannot synchronize; create a replacement project at the intended root in GitHub settings",
			Command: "revyl -C <replacement-root> config pull",
		}, true
	}
	var configErr *config.ConfigError
	if !errors.As(err, &configErr) {
		return projectConfigRecovery{}, false
	}
	switch configErr.Code {
	case "legacy_config_requires_migration", "mixed_config_formats":
		return projectConfigRecovery{
			Summary:        "Local configuration uses the legacy format",
			Command:        projectConfigMigrateCommand(workingDirectory),
			PreviewCommand: projectConfigPreviewCommand(workingDirectory),
			Action:         setupActionMigrateProject,
		}, true
	case "config_not_found":
		return projectConfigRecovery{
			Summary:            "Project configuration file is missing",
			Command:            projectConfigCommand(workingDirectory, "config", "pull"),
			AlternativeCommand: projectConfigCommand(workingDirectory, "init"),
			Action:             setupActionInitializeProject,
		}, true
	case "git_worktree_unavailable", "git_worktree_lookup_timed_out", "path_outside_git_worktree":
		return projectConfigRecovery{
			Summary: "Run Revyl from a Git project root; to make the selected directory a new repository",
			Command: projectConfigGitInitCommand(workingDirectory),
		}, true
	default:
		return projectConfigRecovery{
			Summary: "Project configuration is invalid",
			Command: projectConfigCommand(workingDirectory, "config", "validate"),
		}, true
	}
}

func nestedProjectConfigRecovery(candidateRoots []string) projectConfigRecovery {
	commands := make([]string, 0, len(candidateRoots))
	for _, root := range candidateRoots {
		commands = append(commands, projectConfigCommand(root))
	}
	summary := "Select the nested Revyl project to open"
	if len(commands) == 1 {
		summary = "Open the nested Revyl project"
	}
	return projectConfigRecovery{Summary: summary, CandidateCommands: commands}
}

func projectConfigRecoveryForStatus(status projectConfigStatus, err error) (projectConfigRecovery, bool) {
	if len(status.CandidateRoots) > 0 {
		return nestedProjectConfigRecovery(status.CandidateRoots), true
	}
	return projectConfigRecoveryForError(err, status.CommandRoot)
}

func projectConfigCommand(workingDirectory string, args ...string) string {
	parts := []string{"revyl"}
	if strings.TrimSpace(workingDirectory) != "" {
		parts = append(parts, "-C", quoteProjectConfigCommandArgument(workingDirectory))
	}
	return strings.Join(append(parts, args...), " ")
}

func quoteProjectConfigCommandArgument(value string) string {
	if value != "" && strings.IndexFunc(value, func(character rune) bool {
		return !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("_@%+=:,./-", character))
	}) == -1 {
		return value
	}
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func projectConfigGitInitCommand(workingDirectory string) string {
	gitCommand := "git init"
	if strings.TrimSpace(workingDirectory) != "" {
		gitCommand = "git -C " + quoteProjectConfigCommandArgument(workingDirectory) + " init"
	}
	return gitCommand + " && " + projectConfigCommand(workingDirectory, "init", "-y")
}

func projectConfigMigrateCommand(workingDirectory string) string {
	return projectConfigCommand(workingDirectory, "config", "migrate")
}

func projectConfigPreviewCommand(workingDirectory string) string {
	return projectConfigMigrateCommand(workingDirectory) + " --check"
}

func actionableProjectConfigError(err error) error {
	if err == nil {
		return nil
	}
	var alreadyActionable *projectConfigRecoveryError
	if errors.As(err, &alreadyActionable) {
		return err
	}
	workingDirectory, _ := os.Getwd()
	recovery, ok := projectConfigRecoveryForError(err, selectedProjectCommandRoot(workingDirectory))
	if !ok {
		return err
	}
	return &projectConfigRecoveryError{cause: err, recovery: recovery}
}

func configMigrationExecCmd(devMode bool) *exec.Cmd {
	exe, err := os.Executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		exe = "revyl"
	}
	args := []string{"config", "migrate"}
	if devMode {
		args = append([]string{"--dev"}, args...)
	}
	return exec.Command(exe, args...)
}

func runProjectConfigMigrationCmd(devMode bool) tea.Cmd {
	return tea.ExecProcess(configMigrationExecCmd(devMode), func(err error) tea.Msg {
		return ProjectConfigMigrationDoneMsg{Err: err}
	})
}
