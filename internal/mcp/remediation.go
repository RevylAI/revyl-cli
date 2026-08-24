package mcp

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/outcome"
)

// RemediationActionKind identifies one secret-free setup recovery plan.
type RemediationActionKind string

const (
	remediationExecutableEnvironment = "REVYL_MCP_EXECUTABLE"

	remediationActionCommand             RemediationActionKind = "command"
	remediationActionEnvironmentVariable RemediationActionKind = "environment_variable"
	remediationActionSelectProjectDir    RemediationActionKind = "select_project_dir"
	remediationActionRepairProjectConfig RemediationActionKind = "repair_project_config"
	remediationActionRestartSession      RemediationActionKind = "restart_session"
)

// Remediation describes at most one exact, secret-free setup recovery action.
type Remediation struct {
	ActionKind         RemediationActionKind `json:"action_kind"`
	Command            string                `json:"command,omitempty"`
	AlternativeCommand string                `json:"alternative_command,omitempty"`
	CheckCommand       string                `json:"check_command,omitempty"`
	ApplyCommand       string                `json:"apply_command,omitempty"`
	EnvName            string                `json:"env_name,omitempty"`
	WorkingDirectory   string                `json:"working_directory,omitempty"`
	CandidateRoots     []string              `json:"candidate_roots,omitempty"`
	ConfigPath         string                `json:"config_path,omitempty"`
	RestartRequired    bool                  `json:"restart_required"`
}

// setupProjectStatus contains one typed project state and its bounded recovery action.
type setupProjectStatus struct {
	State            SetupProjectState
	ProjectDirectory string
	Remediation      *Remediation
	Failure          error
	Message          string
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
	if e.status.Message != "" {
		return e.status.Message
	}
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
// The action never depends on guessing whether the runtime has a browser.
// Login is offered everywhere, and the accompanying message names the
// unattended alternative, because misclassifying a runtime withdraws a recovery
// that would have worked.
//
// Parameters:
//   - state: Structured authentication state to remediate.
//
// Returns:
//   - *Remediation: Command or environment-variable action, or nil when authenticated.
func authenticationRemediation(state SetupAuthState) *Remediation {
	switch state {
	case authenticationStateRequired, authenticationStateExpired, authenticationStateInvalid:
		return &Remediation{
			ActionKind: remediationActionCommand,
			Command:    revylRemediationCommand("auth", "login"),
		}
	case authenticationStateCloudSecretRequired:
		return cloudSecretRemediation()
	case authenticationStateCloudContextInvalid:
		return &Remediation{
			ActionKind:      remediationActionRestartSession,
			RestartRequired: true,
		}
	case authenticationStateAuthenticated:
		return nil
	default:
		return nil
	}
}

// cloudSecretRemediation returns the runnable hosted-agent credential bridge.
//
// Credentials re-resolve on every tool call, so bridging the secret mid-session
// takes effect on the next call and needs no restart.
//
// Returns:
//   - *Remediation: Command action that bridges the injected Runtime Secret.
func cloudSecretRemediation() *Remediation {
	return &Remediation{
		ActionKind:      remediationActionCommand,
		Command:         revylRemediationCommand("auth", "persist-cloud-env"),
		EnvName:         "REVYL_API_KEY",
		RestartRequired: false,
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
	for _, argument := range arguments {
		commandParts = append(commandParts, quoteRemediationArgument(argument))
	}
	return strings.Join(commandParts, " ")
}

func projectRemediationCommand(_ bool, arguments ...string) string {
	return revylRemediationCommand(arguments...)
}

func quoteRemediationArgument(argument string) string {
	if argument != "" && strings.IndexFunc(argument, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') &&
			!(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') &&
			!strings.ContainsRune("_@%+=:,./-", r)
	}) == -1 {
		return argument
	}
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(argument, `"`, `""`) + `"`
	}
	return "'" + strings.ReplaceAll(argument, "'", `'"'"'`) + "'"
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
	return resolveSetupProjectStateForMode(workDir, false)
}

func resolveSetupProjectStateForMode(workDir string, devMode bool) setupProjectStatus {
	project := resolveProjectRootStateForMode(workDir, devMode)
	if !projectStateHasConfig(project.State) {
		return project
	}
	inspected := inspectProjectConfigStateForMode(project.ProjectDirectory, devMode)
	if inspected.State == projectStateInitialized && project.State == projectStateNested {
		inspected.State = projectStateNested
	}
	return inspected
}

// resolveProjectRootState finds an initialized project marker without parsing its config.
//
// Parameters:
//   - workDir: Base working directory to inspect.
//
// Returns:
//   - setupProjectStatus: Root-resolution state and remediation for missing or ambiguous projects.
func resolveProjectRootState(workDir string) setupProjectStatus {
	return resolveProjectRootStateForMode(workDir, false)
}

func resolveProjectRootStateForMode(workDir string, devMode bool) setupProjectStatus {
	effectiveDirectory, worktreeRoot, err := config.ResolveGitWorktreeRoot(workDir, "")
	if err != nil {
		return outsideGitProjectStatus(workDir, err)
	}

	configPath, err := config.DiscoverConfigPath(effectiveDirectory, worktreeRoot)
	if err == nil {
		return setupProjectStatus{
			State:            projectStateInitialized,
			ProjectDirectory: filepath.Dir(filepath.Dir(configPath)),
		}
	}

	var configErr *config.ConfigError
	if !errors.As(err, &configErr) || configErr.Code != "config_not_found" {
		projectDirectory, configPath := nearestProjectConfigLocation(effectiveDirectory, worktreeRoot)
		if projectDirectory == "" {
			projectDirectory = effectiveDirectory
			configPath = filepath.Join(projectDirectory, ".revyl", "config.yaml")
		}
		return invalidProjectStatus(projectDirectory, configPath, err, devMode)
	}

	nestedRoots, err := findNestedProjectRoots(effectiveDirectory, worktreeRoot)
	if err != nil {
		return invalidProjectStatus(
			effectiveDirectory,
			filepath.Join(effectiveDirectory, ".revyl", "config.yaml"),
			err,
			devMode,
		)
	}
	switch len(nestedRoots) {
	case 0:
		return missingProjectStatus(effectiveDirectory, devMode)
	case 1:
		return setupProjectStatus{
			State:            projectStateNested,
			ProjectDirectory: nestedRoots[0],
		}
	default:
		failure := &config.AmbiguousProjectRootsError{
			WorkingDirectory: effectiveDirectory,
			Roots:            nestedRoots,
		}
		return setupProjectStatus{
			State:   projectStateAmbiguous,
			Failure: failure,
			Message: failure.Error(),
			Remediation: &Remediation{
				ActionKind:       remediationActionSelectProjectDir,
				WorkingDirectory: effectiveDirectory,
				CandidateRoots:   nestedRoots,
			},
		}
	}
}

func projectStateHasConfig(state SetupProjectState) bool {
	return state == projectStateInitialized || state == projectStateNested
}

func missingProjectStatus(workDir string, devMode bool) setupProjectStatus {
	pullCommand := projectRemediationCommand(devMode, "-C", workDir, "config", "pull")
	initCommand := projectRemediationCommand(devMode, "-C", workDir, "init", "--non-interactive")
	failure := &config.MissingProjectRootError{WorkingDirectory: workDir}
	return setupProjectStatus{
		State:   projectStateNotInitialized,
		Failure: failure,
		Message: fmt.Sprintf("no Revyl project config was found; run %q to restore an existing project, or %q to create a new one", pullCommand, initCommand),
		Remediation: &Remediation{
			ActionKind:         remediationActionCommand,
			Command:            pullCommand,
			AlternativeCommand: initCommand,
			WorkingDirectory:   workDir,
		},
	}
}

func outsideGitProjectStatus(workDir string, failure error) setupProjectStatus {
	return setupProjectStatus{
		State:   projectStateOutsideGit,
		Failure: failure,
		Message: "Revyl project configuration requires an active Git worktree; select a project directory inside one and retry",
		Remediation: &Remediation{
			ActionKind:       remediationActionSelectProjectDir,
			EnvName:          "REVYL_PROJECT_DIR",
			WorkingDirectory: workDir,
		},
	}
}

func findNestedProjectRoots(workDir, worktreeRoot string) ([]string, error) {
	const maxDepth = 4
	skippedDirectories := map[string]bool{
		".git": true, ".venv": true, "DerivedData": true, "Pods": true,
		"build": true, "dist": true, "node_modules": true, "tmp": true, "vendor": true,
	}
	var roots []string
	err := filepath.WalkDir(workDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == workDir {
			return nil
		}
		relative, err := filepath.Rel(workDir, path)
		if err != nil {
			return err
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if filepath.Clean(path) != filepath.Clean(worktreeRoot) {
			if _, gitMarkerErr := os.Lstat(filepath.Join(path, ".git")); gitMarkerErr == nil {
				return filepath.SkipDir
			} else if !errors.Is(gitMarkerErr, fs.ErrNotExist) {
				return gitMarkerErr
			}
		}
		if entry.Name() == ".revyl" {
			metadata, metadataErr := os.Lstat(filepath.Join(path, "config.yaml"))
			if metadataErr != nil && !errors.Is(metadataErr, fs.ErrNotExist) {
				return metadataErr
			}
			if metadataErr == nil && !metadata.IsDir() {
				candidateRoot := filepath.Dir(path)
				_, candidateWorktreeRoot, rootErr := config.ResolveGitWorktreeRoot(candidateRoot, "")
				if rootErr != nil {
					return rootErr
				}
				if filepath.Clean(candidateWorktreeRoot) == filepath.Clean(worktreeRoot) {
					roots = append(roots, candidateRoot)
				}
			}
			return filepath.SkipDir
		}
		depth := len(strings.Split(relative, string(filepath.Separator)))
		if skippedDirectories[entry.Name()] || depth > maxDepth {
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(roots)
	return roots, err
}

func nearestProjectConfigLocation(workDir, worktreeRoot string) (string, string) {
	current := workDir
	for {
		revylPath := filepath.Join(current, ".revyl")
		if _, err := os.Lstat(revylPath); err == nil {
			return current, filepath.Join(revylPath, "config.yaml")
		}
		if current == worktreeRoot {
			return "", ""
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", ""
		}
		current = parent
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
	return inspectProjectConfigStateForMode(projectDirectory, false)
}

func inspectProjectConfigStateForMode(projectDirectory string, devMode bool) setupProjectStatus {
	configPath := filepath.Join(projectDirectory, ".revyl", "config.yaml")
	_, err := config.ResolveProjectContext(projectDirectory, "")
	if err != nil {
		var configErr *config.ConfigError
		if errors.As(err, &configErr) && (configErr.Code == "legacy_config_requires_migration" || configErr.Code == "mixed_config_formats") {
			return legacyProjectStatus(projectDirectory, configPath, err, devMode)
		}
		return invalidProjectStatus(projectDirectory, configPath, err, devMode)
	}
	return setupProjectStatus{
		State:            projectStateInitialized,
		ProjectDirectory: projectDirectory,
	}
}

func legacyProjectStatus(projectDirectory, configPath string, failure error, devMode bool) setupProjectStatus {
	checkCommand := projectRemediationCommand(devMode, "-C", projectDirectory, "config", "migrate", "--check", "--json")
	applyCommand := projectRemediationCommand(devMode, "-C", projectDirectory, "config", "migrate", "--write")
	return setupProjectStatus{
		State:            projectStateLegacy,
		ProjectDirectory: projectDirectory,
		Failure:          failure,
		Message: fmt.Sprintf(
			"legacy Revyl config requires migration; run `%s` to inspect the proposal, then `%s` to back up and apply it",
			checkCommand,
			applyCommand,
		),
		Remediation: &Remediation{
			ActionKind:       remediationActionRepairProjectConfig,
			Command:          checkCommand,
			CheckCommand:     checkCommand,
			ApplyCommand:     applyCommand,
			WorkingDirectory: projectDirectory,
			ConfigPath:       configPath,
		},
	}
}

func invalidProjectStatus(projectDirectory, configPath string, failure error, devMode bool) setupProjectStatus {
	checkCommand := projectRemediationCommand(devMode, "-C", projectDirectory, "config", "validate")
	return setupProjectStatus{
		State:            projectStateInvalid,
		ProjectDirectory: projectDirectory,
		Failure:          failure,
		Message:          fmt.Sprintf("Revyl config %q is invalid; run %q, repair the reported fields, and retry", configPath, checkCommand),
		Remediation: &Remediation{
			ActionKind:       remediationActionRepairProjectConfig,
			Command:          checkCommand,
			CheckCommand:     checkCommand,
			WorkingDirectory: projectDirectory,
			ConfigPath:       configPath,
		},
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
	case projectStateOutsideGit:
		outcomeCode = "project_outside_git"
	case projectStateAmbiguous:
		outcomeCode = "project_ambiguous"
	case projectStateLegacy:
		outcomeCode = "project_legacy_config"
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

	status := resolveProjectRootStateForMode(base, s.devMode)
	if !projectStateHasConfig(status.State) {
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
	if err := validateDevProjectConfigForMode(projectDirectory, s.devMode); err != nil {
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
	return validateDevProjectConfigForMode(projectDirectory, false)
}

func validateDevProjectConfigForMode(projectDirectory string, devMode bool) error {
	status := inspectProjectConfigStateForMode(projectDirectory, devMode)
	if status.State == projectStateInitialized {
		return nil
	}
	return &projectSetupError{status: status}
}
