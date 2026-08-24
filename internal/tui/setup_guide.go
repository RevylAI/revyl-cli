// Package tui provides the setup guide logic for the help screen.
//
// The setup guide derives an ordered checklist from health check results and
// makes each step actionable directly from the TUI. It only renders when
// one or more checks are incomplete.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type setupAction string

const (
	setupActionLogin             setupAction = "login"
	setupActionRetryAPI          setupAction = "retry_api"
	setupActionInitializeProject setupAction = "initialize_project"
	setupActionMigrateProject    setupAction = "migrate_project"
	setupActionLinkApp           setupAction = "link_app"
	setupActionBuild             setupAction = "build"
	setupActionCreateTest        setupAction = "create_test"
)

// deriveSetupSteps maps health check results and project config state to an
// ordered list of setup steps for the getting-started guide.
//
// Parameters:
//   - checks: the health check results from runHealthChecksCmd
//
// Returns:
//   - []SetupStep: ordered setup steps with status derived from checks
func deriveSetupSteps(checks []HealthCheck) []SetupStep {
	steps := make([]SetupStep, 6)

	// Helper to find a check by name.
	findCheck := func(name string) *HealthCheck {
		for i := range checks {
			if checks[i].Name == name {
				return &checks[i]
			}
		}
		return nil
	}

	// Step 1: Authentication
	authCheck := findCheck("Authentication")
	authDone := authCheck != nil && authCheck.Status == "ok"
	if authDone {
		steps[0] = SetupStep{Label: "Log in", Status: "done", Message: "authenticated", Action: setupActionLogin}
	} else {
		// Keep setup focused on authentication until login is complete.
		return []SetupStep{
			{Label: "Log in", Status: "current", Message: "enter: browser login  a: API key login", Action: setupActionLogin},
		}
	}

	// Step 2: API connectivity
	apiCheck := findCheck("API Connection")
	if apiCheck != nil && apiCheck.Status == "ok" {
		steps[1] = SetupStep{Label: "Connect to API", Status: "done", Message: "connected", Action: setupActionRetryAPI}
	} else if steps[0].Status != "done" {
		steps[1] = SetupStep{Label: "Connect to API", Status: "blocked", Message: "requires step 1", Action: setupActionRetryAPI}
	} else {
		steps[1] = SetupStep{Label: "Connect to API", Status: "current", Message: "press enter to retry", Action: setupActionRetryAPI}
	}

	// Step 3: Project config
	configCheck := findCheck("Project Config")
	if configCheck != nil && configCheck.Status == "ok" {
		steps[2] = SetupStep{Label: "Initialize project", Status: "done", Message: "configured", Action: setupActionInitializeProject}
	} else if configCheck != nil && (configCheck.Code == "legacy_config_requires_migration" || configCheck.Code == "mixed_config_formats") {
		steps[2] = SetupStep{
			Label:   "Migrate project config",
			Status:  "current",
			Message: "enter: review proposal and confirm",
			Action:  setupActionMigrateProject,
		}
	} else if configCheck != nil && configCheck.Code == "nested_project_selection_required" {
		steps[2] = SetupStep{
			Label:   "Select nested project",
			Status:  "blocked",
			Message: configCheck.Message,
		}
	} else if steps[1].Status != "done" {
		steps[2] = SetupStep{Label: "Initialize project", Status: "blocked", Message: "requires step 2", Action: setupActionInitializeProject}
	} else {
		steps[2] = SetupStep{Label: "Initialize project", Status: "current", Message: "press enter to set up", Action: setupActionInitializeProject}
	}

	// Step 4: App linked
	appCheck := findCheck("App Linked")
	if appCheck != nil && appCheck.Status == "ok" {
		steps[3] = SetupStep{Label: "Link or create an app", Status: "done", Message: "app linked", Action: setupActionLinkApp}
	} else if steps[2].Status != "done" {
		steps[3] = SetupStep{Label: "Link or create an app", Status: "blocked", Message: "requires step 3", Action: setupActionLinkApp}
	} else {
		steps[3] = SetupStep{Label: "Link or create an app", Status: "current", Message: "press enter to set up", Action: setupActionLinkApp}
	}

	// Step 5: Build uploaded
	buildCheck := findCheck("Build Uploaded")
	if buildCheck != nil && buildCheck.Status == "ok" {
		steps[4] = SetupStep{Label: "Build and upload", Status: "done", Message: "build available", Action: setupActionBuild}
	} else if steps[3].Status != "done" {
		steps[4] = SetupStep{Label: "Build and upload", Status: "blocked", Message: "requires step 4", Action: setupActionBuild}
	} else {
		steps[4] = SetupStep{Label: "Build and upload", Status: "hint", Message: "revyl build --platform <platform>", Action: setupActionBuild}
	}

	// Step 6: First test
	testCheck := findCheck("Tests Configured")
	if testCheck != nil && testCheck.Status == "ok" {
		steps[5] = SetupStep{Label: "Create your first test", Status: "done", Message: "tests configured", Action: setupActionCreateTest}
	} else if steps[3].Status != "done" {
		steps[5] = SetupStep{Label: "Create your first test", Status: "blocked", Message: "requires step 4", Action: setupActionCreateTest}
	} else {
		steps[5] = SetupStep{Label: "Create your first test", Status: "current", Message: "press enter to create", Action: setupActionCreateTest}
	}

	return steps
}

// allSetupStepsDone returns true if every step in the list has status "done".
//
// Parameters:
//   - steps: the setup steps to check
//
// Returns:
//   - bool: true if all steps are complete
func allSetupStepsDone(steps []SetupStep) bool {
	for _, s := range steps {
		if s.Status != "done" {
			return false
		}
	}
	return true
}

// firstActionableStep returns the index of the first step that is "current" or "hint".
// Returns -1 if no actionable step exists.
//
// Parameters:
//   - steps: the setup steps to search
//
// Returns:
//   - int: index of the first actionable step, or -1
func firstActionableStep(steps []SetupStep) int {
	for i, s := range steps {
		if s.Status == "current" || s.Status == "hint" {
			return i
		}
	}
	return -1
}

// nextActionableStep returns the next actionable step index after the given cursor.
// Wraps around. Returns the current index if no other actionable step exists.
//
// Parameters:
//   - steps: the setup steps
//   - current: the current cursor position
//
// Returns:
//   - int: the next actionable step index
func nextActionableStep(steps []SetupStep, current int) int {
	n := len(steps)
	for i := 1; i < n; i++ {
		idx := (current + i) % n
		if steps[idx].Status == "current" || steps[idx].Status == "hint" {
			return idx
		}
	}
	return current
}

// prevActionableStep returns the previous actionable step index before the given cursor.
// Wraps around. Returns the current index if no other actionable step exists.
//
// Parameters:
//   - steps: the setup steps
//   - current: the current cursor position
//
// Returns:
//   - int: the previous actionable step index
func prevActionableStep(steps []SetupStep, current int) int {
	n := len(steps)
	for i := 1; i < n; i++ {
		idx := (current - i + n) % n
		if steps[idx].Status == "current" || steps[idx].Status == "hint" {
			return idx
		}
	}
	return current
}

// executeSetupStep returns a tea.Cmd that performs the action for the given
// setup step index. Returns nil for steps that cannot be executed inline.
//
// Parameters:
//   - m: the hub model (for accessing client, devMode, etc.)
//   - steps: the current setup steps
//   - stepIndex: the index of the step to execute
//
// Returns:
//   - hubModel: the updated model
//   - tea.Cmd: the command to execute, or nil
func executeSetupStep(m hubModel, steps []SetupStep, stepIndex int) (hubModel, tea.Cmd) {
	if stepIndex < 0 || stepIndex >= len(steps) {
		return m, nil
	}

	step := steps[stepIndex]
	if step.Status != "current" && step.Status != "hint" {
		return m, nil
	}

	switch step.Action {
	case setupActionLogin:
		// Step 1: Auth -- shell out to revyl auth login
		m.returnToDashboardAfterAuth = true
		return m, tea.ExecProcess(authLoginCmd(false), func(err error) tea.Msg {
			return SetupActionMsg{Action: setupActionLogin, Err: err}
		})

	case setupActionRetryAPI:
		// Step 2: API -- just re-run health checks
		m.healthLoading = true
		m.healthChecks = nil
		return m, runHealthChecksCmd(m.devMode, m.client)

	case setupActionInitializeProject:
		// Step 3: Init project inline
		return m, initProjectCmd()

	case setupActionMigrateProject:
		return m, runProjectConfigMigrationCmd(m.devMode)

	case setupActionLinkApp:
		// Step 4: Link app -- transition to app list screen
		m.currentView = viewAppList
		m.appCursor = 0
		m.appsLoading = true
		if m.client != nil {
			return m, fetchAppsCmd(m.client)
		}
		return m, nil

	case setupActionBuild:
		// Step 5: Build upload -- hint-only, no inline action.
		// The hint message already shows the CLI command. This is a no-op
		// acknowledgement so the user sees the command they need to run.
		return m, nil

	case setupActionCreateTest:
		// Step 6: Create test -- transition to create test screen
		cm, err := newCurrentProjectCreateModel(m.apiKey, m.devMode, m.client, m.width, m.height)
		if err != nil {
			m.err = err
			return m, nil
		}
		m.createModel = &cm
		m.currentView = viewCreateTest
		return m, m.createModel.Init()
	}

	return m, nil
}

// authLoginCmd returns an exec.Cmd for running `revyl auth login` as a subprocess.
//
// Returns:
//   - *exec.Cmd: the command to execute
func authLoginCmd(useAPIKey bool) *exec.Cmd {
	exe, err := os.Executable()
	if err != nil {
		exe = "revyl"
	}
	if useAPIKey {
		return exec.Command(exe, "auth", "login", "--api-key")
	}
	return exec.Command(exe, "auth", "login")
}

// initProjectCmd delegates to the canonical non-interactive init command. That
// command owns Git-worktree validation, detection, and config persistence.
//
// Returns:
//   - tea.Cmd: command that produces a SetupActionMsg
func initProjectCmd() tea.Cmd {
	return func() tea.Msg {
		exe, err := os.Executable()
		if err != nil {
			exe = "revyl"
		}
		output, err := exec.Command(exe, "init", "--non-interactive").CombinedOutput()
		if err != nil {
			detail := strings.TrimSpace(string(output))
			if detail == "" {
				detail = err.Error()
			}
			return SetupActionMsg{Action: setupActionInitializeProject, Err: fmt.Errorf("project initialization failed: %s", detail)}
		}

		return SetupActionMsg{Action: setupActionInitializeProject, Err: nil}
	}
}

// renderSetupGuide renders the GETTING STARTED section for the help screen.
// Only renders when at least one step is incomplete.
//
// Parameters:
//   - steps: the derived setup steps
//   - cursor: the current setup cursor position
//   - innerW: the inner width for separators
//
// Returns:
//   - string: the rendered setup guide, or empty string if all done
func renderSetupGuide(steps []SetupStep, cursor int, innerW int) string {
	if len(steps) == 0 || allSetupStepsDone(steps) {
		return ""
	}

	var b strings.Builder

	// Count done steps for progress indicator
	doneCount := 0
	currentStep := 0
	for i, s := range steps {
		if s.Status == "done" {
			doneCount++
		}
		if s.Status == "current" || s.Status == "hint" {
			if currentStep == 0 {
				currentStep = i + 1
			}
		}
	}

	header := fmt.Sprintf("  GETTING STARTED%s",
		dimStyle.Render(fmt.Sprintf("%*s", innerW-17, fmt.Sprintf("step %d of %d", currentStep, len(steps)))))
	b.WriteString(sectionStyle.Render(header) + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	for i, step := range steps {
		var icon string
		var labelStyle = dimStyle
		var msgStyle = dimStyle

		switch step.Status {
		case "done":
			icon = successStyle.Render("✓")
			labelStyle = dimStyle
		case "current":
			if i == cursor {
				icon = selectedStyle.Render("▸")
				labelStyle = selectedStyle
				msgStyle = normalStyle
			} else {
				icon = normalStyle.Render("○")
				labelStyle = normalStyle
			}
		case "hint":
			if i == cursor {
				icon = selectedStyle.Render("▸")
				labelStyle = selectedStyle
				msgStyle = actionDescStyle
			} else {
				icon = dimStyle.Render("○")
				msgStyle = actionDescStyle
			}
		case "blocked":
			icon = dimStyle.Render("·")
		}

		num := dimStyle.Render(fmt.Sprintf("%d. ", i+1))
		label := labelStyle.Render(fmt.Sprintf("%-26s", step.Label))
		msg := msgStyle.Render(step.Message)
		b.WriteString(fmt.Sprintf("  %s %s%s %s\n", icon, num, label, msg))
	}

	return b.String()
}
