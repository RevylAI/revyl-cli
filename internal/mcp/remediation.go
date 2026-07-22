package mcp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/outcome"
)

// RemediationActionKind identifies one exact setup recovery action.
type RemediationActionKind string

const (
	remediationExecutableEnvironment = "REVYL_MCP_EXECUTABLE"

	remediationActionCommand             RemediationActionKind = "command"
	remediationActionEnvironmentVariable RemediationActionKind = "environment_variable"
	remediationActionSelectProjectDir    RemediationActionKind = "select_project_dir"
	remediationActionRepairProjectConfig RemediationActionKind = "repair_project_config"
)

// Remediation describes at most one exact, secret-free setup recovery action.
type Remediation struct {
	ActionKind       RemediationActionKind `json:"action_kind"`
	Command          string                `json:"command,omitempty"`
	EnvName          string                `json:"env_name,omitempty"`
	WorkingDirectory string                `json:"working_directory,omitempty"`
	CandidateRoots   []string              `json:"candidate_roots,omitempty"`
	ConfigPath       string                `json:"config_path,omitempty"`
	RestartRequired  bool                  `json:"restart_required"`
}

// setupProjectStatus contains one typed project state and its bounded recovery action.
type setupProjectStatus struct {
	State            SetupProjectState
	ProjectDirectory string
	Remediation      *Remediation
	Failure          error
}

// projectSetupError preserves a classified setup failure across adapter boundaries.
type projectSetupError struct {
	status setupProjectStatus
}

// Error returns the underlying project setup failure message.
//
// Returns:
//   - string: Actionable project setup failure message.
func (e *projectSetupError) Error() string {
	if e.status.Failure != nil {
		return e.status.Failure.Error()
	}
	return fmt.Sprintf("Revyl project setup is %s", e.status.State)
}

// Unwrap returns the lower-level project resolution or configuration failure.
//
// Returns:
//   - error: Underlying setup failure, when available.
func (e *projectSetupError) Unwrap() error {
	return e.status.Failure
}

// authenticationRemediation returns the exact supported action for one auth state.
//
// Parameters:
//   - state: Structured authentication state to remediate.
//
// Returns:
//   - *Remediation: Command or environment-variable action, or nil when authenticated.
func authenticationRemediation(state SetupAuthState) *Remediation {
	switch state {
	case authenticationStateRequired, authenticationStateExpired:
		return &Remediation{
			ActionKind: remediationActionCommand,
			Command:    revylRemediationCommand("auth", "login"),
		}
	case authenticationStateCloudSecretRequired:
		return &Remediation{
			ActionKind:      remediationActionEnvironmentVariable,
			EnvName:         "REVYL_API_KEY",
			RestartRequired: true,
		}
	case authenticationStateAuthenticated:
		return nil
	default:
		return nil
	}
}

// revylRemediationCommand builds one executable setup command for the active runtime.
//
// Parameters:
//   - arguments: Fixed Revyl arguments for the supported remediation.
//
// Returns:
//   - string: Shell-ready command using the plugin runtime when available.
func revylRemediationCommand(arguments ...string) string {
	executable := strings.TrimSpace(os.Getenv(remediationExecutableEnvironment))
	if executable == "" {
		executable = "revyl"
	}

	commandParts := make([]string, 0, len(arguments)+1)
	commandParts = append(commandParts, quoteRemediationExecutable(executable))
	commandParts = append(commandParts, arguments...)
	return strings.Join(commandParts, " ")
}

// quoteRemediationExecutable quotes an executable path for the current platform shell.
//
// Parameters:
//   - executable: Command name or absolute executable path.
//
// Returns:
//   - string: Unquoted simple command name or safely quoted path.
func quoteRemediationExecutable(executable string) string {
	if executable == "revyl" {
		return executable
	}
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(executable, `"`, `""`) + `"`
	}
	return "'" + strings.ReplaceAll(executable, "'", `'"'"'`) + "'"
}

// resolveSetupProjectState classifies the nearest or nested Revyl project without mutating it.
//
// Parameters:
//   - workDir: Base working directory to inspect.
//
// Returns:
//   - setupProjectStatus: Stable project state, failure, and one bounded remediation.
func resolveSetupProjectState(workDir string) setupProjectStatus {
	project := resolveProjectRootState(workDir)
	if project.State != projectStateInitialized {
		return project
	}
	return inspectProjectConfigState(project.ProjectDirectory)
}

// resolveProjectRootState finds an initialized project marker without parsing its config.
//
// Parameters:
//   - workDir: Base working directory to inspect.
//
// Returns:
//   - setupProjectStatus: Root-resolution state and remediation for missing or ambiguous projects.
func resolveProjectRootState(workDir string) setupProjectStatus {
	projectDirectory, err := config.FindProjectRoot(workDir)
	if err != nil {
		var ambiguous *config.AmbiguousProjectRootsError
		if errors.As(err, &ambiguous) {
			candidateRoots := append([]string(nil), ambiguous.Roots...)
			sort.Strings(candidateRoots)
			return setupProjectStatus{
				State:   projectStateAmbiguous,
				Failure: err,
				Remediation: &Remediation{
					ActionKind:       remediationActionSelectProjectDir,
					WorkingDirectory: ambiguous.WorkingDirectory,
					CandidateRoots:   candidateRoots,
				},
			}
		}

		workingDirectory := workDir
		var missing *config.MissingProjectRootError
		if errors.As(err, &missing) {
			workingDirectory = missing.WorkingDirectory
		}
		return setupProjectStatus{
			State:   projectStateNotInitialized,
			Failure: err,
			Remediation: &Remediation{
				ActionKind:       remediationActionCommand,
				Command:          revylRemediationCommand("init", "--non-interactive"),
				WorkingDirectory: workingDirectory,
			},
		}
	}

	return setupProjectStatus{
		State:            projectStateInitialized,
		ProjectDirectory: projectDirectory,
	}
}

// inspectProjectConfigState validates the config for an already-resolved project root.
//
// Parameters:
//   - projectDirectory: Project root containing the existing .revyl/config.yaml path.
//
// Returns:
//   - setupProjectStatus: Initialized or invalid state with exact repair metadata.
func inspectProjectConfigState(projectDirectory string) setupProjectStatus {
	configPath := filepath.Join(projectDirectory, ".revyl", "config.yaml")
	if _, err := config.LoadProjectConfig(configPath); err != nil {
		return setupProjectStatus{
			State:            projectStateInvalid,
			ProjectDirectory: projectDirectory,
			Failure:          err,
			Remediation: &Remediation{
				ActionKind:       remediationActionRepairProjectConfig,
				WorkingDirectory: projectDirectory,
				ConfigPath:       configPath,
			},
		}
	}
	return setupProjectStatus{
		State:            projectStateInitialized,
		ProjectDirectory: projectDirectory,
	}
}

// projectResolutionFailure maps a project setup error to its stable outcome and remediation.
//
// Parameters:
//   - resolutionErr: Error returned while resolving an initialized Revyl project.
//
// Returns:
//   - outcome.Envelope: Stable semantic failure classification.
//   - *Remediation: Actionable recovery data, or nil for an unrelated error.
func projectResolutionFailure(resolutionErr error) (outcome.Envelope, *Remediation) {
	var setupErr *projectSetupError
	if !errors.As(resolutionErr, &setupErr) {
		return outcome.Failed("project_not_found", resolutionErr.Error(), false), nil
	}

	var outcomeCode string
	switch setupErr.status.State {
	case projectStateNotInitialized:
		outcomeCode = "project_not_initialized"
	case projectStateAmbiguous:
		outcomeCode = "project_ambiguous"
	case projectStateInvalid:
		outcomeCode = "project_invalid"
	default:
		outcomeCode = "project_not_found"
	}
	return outcome.Failed(outcomeCode, resolutionErr.Error(), false), setupErr.status.Remediation
}

// resolveDevProjectDir resolves an explicit path or one unambiguous nested project without parsing config.
//
// Parameters:
//   - requested: Optional absolute or server-relative project directory.
//
// Returns:
//   - string: Resolved project directory with an existing config path.
//   - error: Classified setup failure when the project is missing or ambiguous.
func (s *Server) resolveDevProjectDir(requested string) (string, error) {
	base := s.workDir
	if strings.TrimSpace(requested) != "" {
		base = strings.TrimSpace(requested)
		if !filepath.IsAbs(base) {
			base = filepath.Join(s.workDir, base)
		}
	}

	status := resolveProjectRootState(base)
	if status.State != projectStateInitialized {
		return "", &projectSetupError{status: status}
	}
	return status.ProjectDirectory, nil
}

// resolveValidatedDevProjectDir resolves a project root and validates its config for setup-sensitive work.
//
// Parameters:
//   - requested: Optional absolute or server-relative project directory.
//
// Returns:
//   - string: Resolved project directory with a parse-valid config.
//   - error: Classified setup failure when the project is unavailable or invalid.
func (s *Server) resolveValidatedDevProjectDir(requested string) (string, error) {
	projectDirectory, err := s.resolveDevProjectDir(requested)
	if err != nil {
		return "", err
	}
	if err := validateDevProjectConfig(projectDirectory); err != nil {
		return "", err
	}
	return projectDirectory, nil
}

// validateDevProjectConfig validates config for an already-resolved project root.
//
// Parameters:
//   - projectDirectory: Project root containing .revyl/config.yaml.
//
// Returns:
//   - error: Classified project_invalid failure, or nil when the config parses.
func validateDevProjectConfig(projectDirectory string) error {
	status := inspectProjectConfigState(projectDirectory)
	if status.State == projectStateInitialized {
		return nil
	}
	return &projectSetupError{status: status}
}
