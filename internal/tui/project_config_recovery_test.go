package tui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/projectpublication"
)

func writeLegacyProjectConfig(t *testing.T, projectRoot string) string {
	t.Helper()
	configDir := filepath.Join(projectRoot, ".revyl")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	legacy := []byte("project:\n  name: Example\nbuild:\n  system: expo\n")
	if err := os.WriteFile(configPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func legacyConfigError() error {
	return &config.ConfigError{Stage: "classification", Code: "legacy_config_requires_migration"}
}

func mixedConfigError() error {
	return &config.ConfigError{Stage: "classification", Code: "mixed_config_formats"}
}

func TestCurrentProjectConfigStatusDistinguishesLegacyFromMissing(t *testing.T) {
	projectRoot := initializeSettingsGitWorktree(t)
	configPath := writeLegacyProjectConfig(t, projectRoot)
	t.Chdir(projectRoot)

	status := currentProjectConfigStatus()
	if status.State != projectConfigStateLegacy {
		t.Fatalf("status.State = %q, want %q (err: %v)", status.State, projectConfigStateLegacy, status.Err)
	}
	gotInfo, gotErr := os.Stat(status.ConfigPath)
	wantInfo, wantErr := os.Stat(configPath)
	if gotErr != nil || wantErr != nil || !os.SameFile(gotInfo, wantInfo) {
		t.Fatalf("status.ConfigPath = %q, want same file as %q", status.ConfigPath, configPath)
	}
}

func TestCurrentProjectConfigStatusRequiresExplicitNestedProjectSelection(t *testing.T) {
	tests := []struct {
		name         string
		projectRoots []string
	}{
		{name: "one nested project", projectRoots: []string{"apps/mobile app"}},
		{name: "multiple nested projects", projectRoots: []string{"apps/android", "apps/ios"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			worktree := initializeSettingsGitWorktree(t)
			wantRoots := make([]string, 0, len(tc.projectRoots))
			for _, relativeRoot := range tc.projectRoots {
				projectRoot := filepath.Join(worktree, filepath.FromSlash(relativeRoot))
				writeSettingsConfig(t, projectRoot, canonicalSettingsConfig(300))
				wantRoots = append(wantRoots, projectRoot)
			}
			t.Chdir(worktree)

			status := currentProjectConfigStatus()
			if status.State != projectConfigStateSelection {
				t.Fatalf("status.State = %q, want %q (err: %v)", status.State, projectConfigStateSelection, status.Err)
			}
			if !slices.Equal(status.CandidateRoots, wantRoots) {
				t.Fatalf("candidate roots = %#v, want %#v", status.CandidateRoots, wantRoots)
			}

			m := newHubModel("dev", true)
			m.width = 120
			m.projectConfigStatus = status
			m.err = &config.ConfigError{Code: "config_not_found"}
			output := m.renderDashboard()
			for _, projectRoot := range wantRoots {
				command := projectConfigCommand(projectRoot)
				if !strings.Contains(output, command) {
					t.Fatalf("dashboard missing nested selection command %q:\n%s", command, output)
				}
			}
			for _, misleading := range []string{
				projectConfigCommand(worktree, "config", "pull"),
				projectConfigCommand(worktree, "init"),
			} {
				if strings.Contains(output, misleading) {
					t.Fatalf("dashboard exposed misleading root command %q:\n%s", misleading, output)
				}
			}
			if strings.Contains(output, "revyl-vilnius") || strings.Contains(output, "--dev") {
				t.Fatalf("dashboard exposed a workspace-only selection command:\n%s", output)
			}

			code, message := projectConfigHealthDiagnostic(status.Err, status.CommandRoot)
			steps := deriveSetupSteps([]HealthCheck{
				{Name: "Authentication", Status: "ok"},
				{Name: "API Connection", Status: "ok"},
				{Name: "Project Config", Status: "warning", Code: code, Message: message},
			})
			if steps[2].Label != "Select nested project" || steps[2].Status != "blocked" || steps[2].Action != "" {
				t.Fatalf("nested selection setup step = %+v", steps[2])
			}
			if !strings.Contains(steps[2].Message, projectConfigCommand(wantRoots[0])) {
				t.Fatalf("nested selection setup step missing exact command: %+v", steps[2])
			}
		})
	}
}

func TestActionableProjectConfigErrorPreservesTypedCauseAndCommand(t *testing.T) {
	err := actionableProjectConfigError(legacyConfigError())
	if !strings.Contains(err.Error(), "config migrate") {
		t.Fatalf("error = %v, want migrate command", err)
	}
	var configErr *config.ConfigError
	if !errors.As(err, &configErr) || configErr.Code != "legacy_config_requires_migration" {
		t.Fatalf("error lost typed config cause: %T %v", err, err)
	}
}

func TestActionableProjectConfigErrorExplainsRemovedProjectWithoutAnID(t *testing.T) {
	projectRoot := filepath.Join(t.TempDir(), "mobile app")
	recovery, ok := projectConfigRecoveryForError(
		&projectpublication.Error{Code: "project_removed"},
		projectRoot,
	)
	if !ok {
		t.Fatal("removed project did not produce recovery")
	}
	if !strings.Contains(recovery.Summary, "was deleted") ||
		!strings.Contains(recovery.Summary, "local-only commands") ||
		!strings.Contains(recovery.Summary, "GitHub settings") {
		t.Fatalf("recovery = %#v", recovery)
	}
	want := "revyl -C <replacement-root> config pull"
	if recovery.Command != want || strings.Contains(recovery.Command, "--project") {
		t.Fatalf("recovery command = %q, want %q", recovery.Command, want)
	}
}

func TestMixedProjectConfigUsesMigrationRecovery(t *testing.T) {
	recovery, ok := projectConfigRecoveryForError(mixedConfigError(), "")
	if !ok || recovery.Action != setupActionMigrateProject || recovery.Command != projectConfigMigrateCommand("") {
		t.Fatalf("mixed config recovery = %#v, %v", recovery, ok)
	}
	steps := deriveSetupSteps([]HealthCheck{
		{Name: "Authentication", Status: "ok"},
		{Name: "API Connection", Status: "ok"},
		{Name: "Project Config", Status: "warning", Code: "mixed_config_formats"},
	})
	if steps[2].Action != setupActionMigrateProject || steps[2].Status != "current" {
		t.Fatalf("mixed config setup step = %#v", steps[2])
	}
}

func TestProjectConfigRecoveryUsesPublicCommands(t *testing.T) {
	originalArgZero := os.Args[0]
	os.Args[0] = "/Users/example/.local/bin/revyl-vilnius"
	t.Cleanup(func() { os.Args[0] = originalArgZero })

	recovery, ok := projectConfigRecoveryForError(legacyConfigError(), "")
	if !ok {
		t.Fatal("legacy error did not produce recovery")
	}
	if recovery.Command != "revyl config migrate" {
		t.Fatalf("recovery command = %q", recovery.Command)
	}
	if recovery.PreviewCommand != "revyl config migrate --check" {
		t.Fatalf("preview command = %q", recovery.PreviewCommand)
	}
	missingRecovery, ok := projectConfigRecoveryForError(&config.ConfigError{Code: "config_not_found"}, "")
	if !ok || missingRecovery.Command != "revyl config pull" || missingRecovery.AlternativeCommand != "revyl init" {
		t.Fatalf("missing config recovery = %#v, %v", missingRecovery, ok)
	}
	invalidRecovery, ok := projectConfigRecoveryForError(&config.ConfigError{Code: "schema_validation_failed"}, "")
	if !ok || invalidRecovery.Command != "revyl config validate" {
		t.Fatalf("invalid config recovery = %#v, %v", invalidRecovery, ok)
	}
}

func TestProjectConfigRecoveryPreservesSelectedRootFromDifferentParentDirectory(t *testing.T) {
	parentDirectory := t.TempDir()
	selectedRoot := filepath.Join(parentDirectory, "selected project")
	if err := os.MkdirAll(selectedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(parentDirectory)

	recovery, ok := projectConfigRecoveryForError(legacyConfigError(), selectedRoot)
	if !ok {
		t.Fatal("legacy error did not produce recovery")
	}
	quotedRoot := quoteProjectConfigCommandArgument(selectedRoot)
	want := "revyl -C " + quotedRoot + " config migrate"
	if recovery.Command != want {
		t.Fatalf("selected-root recovery command = %q, want %q", recovery.Command, want)
	}
}

func TestRenderErrorDashboardPreservesExplicitSelectedRoot(t *testing.T) {
	parentDirectory := t.TempDir()
	selectedRoot := filepath.Join(parentDirectory, "selected project")
	if err := os.MkdirAll(selectedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	originalArgs := os.Args
	os.Args = []string{"revyl", "-C", selectedRoot}
	t.Cleanup(func() { os.Args = originalArgs })
	t.Chdir(selectedRoot)

	m := newHubModel("dev", true)
	m.width = 100
	m.projectConfigStatus.State = projectConfigStateLegacy
	m.err = legacyConfigError()
	if m.projectConfigStatus.CommandRoot != selectedRoot {
		t.Fatalf("cached command root = %q, want %q", m.projectConfigStatus.CommandRoot, selectedRoot)
	}
	t.Chdir(parentDirectory)

	out := m.renderDashboard()
	want := "revyl -C " + quoteProjectConfigCommandArgument(selectedRoot) + " config migrate"
	if !strings.Contains(out, want) {
		t.Fatalf("dashboard did not preserve selected root %q:\n%s", want, out)
	}
	if strings.Contains(out, "--dev") {
		t.Fatalf("dashboard exposed dev routing in customer recovery text:\n%s", out)
	}
}

func TestProjectConfigRecoveryExplainsHowToCreateGitWorktree(t *testing.T) {
	selectedRoot := filepath.Join(t.TempDir(), "new project")
	recovery, ok := projectConfigRecoveryForError(&config.ConfigError{Code: "git_worktree_unavailable"}, selectedRoot)
	if !ok {
		t.Fatal("outside-Git error did not produce recovery")
	}
	quotedRoot := quoteProjectConfigCommandArgument(selectedRoot)
	want := "git -C " + quotedRoot + " init && revyl -C " + quotedRoot + " init -y"
	if recovery.Command != want || !strings.Contains(recovery.Summary, "Git project root") {
		t.Fatalf("outside-Git recovery = %#v, want command %q", recovery, want)
	}
}

func TestRenderErrorDashboardOffersPullOrInitForMissingConfig(t *testing.T) {
	projectRoot := initializeSettingsGitWorktree(t)
	t.Chdir(projectRoot)

	m := newHubModel("dev", false)
	m.width = 100
	m.err = &config.ConfigError{Code: "config_not_found"}
	out := m.renderDashboard()
	for _, expected := range []string{
		projectConfigCommand("", "config", "pull"),
		projectConfigCommand("", "init"),
		"restore an existing project",
		"create a new one",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("dashboard missing %q:\n%s", expected, out)
		}
	}
}

func TestRenderErrorDashboardOffersLegacyMigrationWithoutClaimingMissing(t *testing.T) {
	projectRoot := initializeSettingsGitWorktree(t)
	writeLegacyProjectConfig(t, projectRoot)
	t.Chdir(projectRoot)

	m := newHubModel("dev", false)
	m.width = 100
	m.err = legacyConfigError()
	out := m.renderDashboard()
	for _, expected := range []string{
		".revyl/config.yaml found",
		"migration required",
		projectConfigPreviewCommand(""),
		projectConfigMigrateCommand(""),
		"dropped, defaulted, and ambiguous fields",
		"m migrate",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("dashboard missing %q:\n%s", expected, out)
		}
	}
	if strings.Contains(out, ".revyl/config.yaml not found") {
		t.Fatalf("legacy dashboard claimed the config was missing:\n%s", out)
	}
}

func TestRenderErrorDashboardUsesCachedProjectConfigStatus(t *testing.T) {
	projectRoot := initializeSettingsGitWorktree(t)
	configPath := writeLegacyProjectConfig(t, projectRoot)
	t.Chdir(projectRoot)

	m := newHubModel("dev", false)
	m.width = 100
	m.err = legacyConfigError()
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		out := m.renderDashboard()
		if !strings.Contains(out, ".revyl/config.yaml found") || strings.Contains(out, ".revyl/config.yaml not found") {
			t.Fatalf("render re-resolved project config instead of using cached status:\n%s", out)
		}
	}
}

func TestRenderErrorDashboardUsesPublicRecoveryCommandsInDevMode(t *testing.T) {
	originalArgZero := os.Args[0]
	os.Args[0] = "/Users/example/.local/bin/revyl-vilnius"
	t.Cleanup(func() { os.Args[0] = originalArgZero })

	cases := []struct {
		name     string
		state    projectConfigState
		err      error
		expected []string
	}{
		{
			name:  "legacy",
			state: projectConfigStateLegacy,
			err:   legacyConfigError(),
			expected: []string{
				"revyl config migrate --check",
				"revyl config migrate",
			},
		},
		{
			name:  "missing",
			state: projectConfigStateMissing,
			err:   &config.ConfigError{Code: "config_not_found"},
			expected: []string{
				"revyl config pull",
				"revyl init",
			},
		},
		{
			name:  "invalid",
			state: projectConfigStateInvalid,
			err:   &config.ConfigError{Code: "schema_validation_failed"},
			expected: []string{
				"revyl config validate",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newHubModel("dev", true)
			m.width = 100
			m.projectConfigStatus = projectConfigStatus{State: tc.state}
			m.err = tc.err
			out := m.renderDashboard()
			for _, expected := range tc.expected {
				if !strings.Contains(out, expected) {
					t.Fatalf("dashboard missing %q:\n%s", expected, out)
				}
			}
			if strings.Contains(out, "revyl-vilnius") || strings.Contains(out, "--dev") {
				t.Fatalf("dashboard exposed a workspace-only recovery command:\n%s", out)
			}
		})
	}
}

func TestErrorDashboardRefreshUpdatesCachedProjectConfigStatus(t *testing.T) {
	projectRoot := initializeSettingsGitWorktree(t)
	configPath := writeLegacyProjectConfig(t, projectRoot)
	t.Chdir(projectRoot)

	m := newHubModel("dev", false)
	m.err = legacyConfigError()
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}

	nextModel, _ := m.handleErrorDashboardKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	next := nextModel.(hubModel)
	if next.projectConfigStatus.State != projectConfigStateMissing {
		t.Fatalf("refreshed config state = %q, want %q", next.projectConfigStatus.State, projectConfigStateMissing)
	}
}

func TestDeriveSetupStepsUsesMigrationActionForLegacyConfig(t *testing.T) {
	steps := deriveSetupSteps([]HealthCheck{
		{Name: "Authentication", Status: "ok"},
		{Name: "API Connection", Status: "ok"},
		{Name: "Project Config", Status: "warning", Code: "legacy_config_requires_migration"},
	})
	projectStep := steps[2]
	if projectStep.Label != "Migrate project config" || projectStep.Action != setupActionMigrateProject || projectStep.Status != "current" {
		t.Fatalf("project step = %#v", projectStep)
	}
	if steps[3].Status != "blocked" {
		t.Fatalf("app step = %#v, want blocked until migration", steps[3])
	}
}

func TestDeriveSetupStepsAllowsLocalMigrationWhenAPIIsUnavailable(t *testing.T) {
	steps := deriveSetupSteps([]HealthCheck{
		{Name: "Authentication", Status: "ok"},
		{Name: "API Connection", Status: "warning"},
		{Name: "Project Config", Status: "warning", Code: "legacy_config_requires_migration"},
	})
	projectStep := steps[2]
	if projectStep.Action != setupActionMigrateProject || projectStep.Status != "current" {
		t.Fatalf("project step = %#v, want actionable local migration", projectStep)
	}
}

func TestHealthCheckSelectsLegacyMigrationStep(t *testing.T) {
	m := newHubModel("dev", false)
	nextModel, cmd := m.Update(HealthCheckMsg{Checks: []HealthCheck{
		{Name: "Authentication", Status: "ok"},
		{Name: "API Connection", Status: "ok"},
		{Name: "Project Config", Status: "warning", Code: "legacy_config_requires_migration"},
	}})
	if cmd != nil {
		t.Fatalf("health update returned command: %v", cmd)
	}
	next := nextModel.(hubModel)
	if next.setupCursor != 2 || next.setupSteps[next.setupCursor].Action != setupActionMigrateProject {
		t.Fatalf("setup cursor = %d step = %#v", next.setupCursor, next.setupSteps[next.setupCursor])
	}
}

func TestErrorDashboardGatesHiddenActionsAndAllowsMigration(t *testing.T) {
	m := newHubModel("dev", true)
	m.err = legacyConfigError()

	nextModel, cmd := m.handleDashboardKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("hidden Enter action returned command: %v", cmd)
	}
	next := nextModel.(hubModel)
	if next.actionCursor != m.actionCursor || next.err == nil {
		t.Fatalf("hidden Enter action changed error dashboard state: %#v", next)
	}

	_, cmd = m.handleDashboardKey(keyRune('m'))
	if cmd == nil {
		t.Fatal("migrate key did not return a command")
	}
}

func TestConfigMigrationExecCmdPreservesDevMode(t *testing.T) {
	cmd := configMigrationExecCmd(true)
	if !slices.Equal(cmd.Args[1:], []string{"--dev", "config", "migrate"}) {
		t.Fatalf("migration args = %#v", cmd.Args)
	}
	cmd = configMigrationExecCmd(false)
	if !slices.Equal(cmd.Args[1:], []string{"config", "migrate"}) {
		t.Fatalf("migration args = %#v", cmd.Args)
	}
}

func TestMigrationCompletionSurfacesDeclineAndKeepsRecoveryAction(t *testing.T) {
	projectRoot := initializeSettingsGitWorktree(t)
	writeLegacyProjectConfig(t, projectRoot)
	t.Chdir(projectRoot)

	m := newHubModel("dev", false)
	nextModel, cmd := m.Update(ProjectConfigMigrationDoneMsg{})
	if cmd != nil {
		t.Fatalf("declined migration returned command: %v", cmd)
	}
	next := nextModel.(hubModel)
	if next.err == nil || !strings.Contains(strings.ToLower(next.err.Error()), "migration was not applied") {
		t.Fatalf("declined migration error = %v", next.err)
	}
	if strings.Contains(next.err.Error(), "classification:") || strings.Contains(next.err.Error(), "legacy_config_requires_migration") {
		t.Fatalf("declined migration leaked internal classification: %v", next.err)
	}
	if recovery, ok := projectConfigRecoveryForError(next.err, projectRoot); !ok || recovery.Action != setupActionMigrateProject {
		t.Fatalf("declined migration lost recovery: %#v, %v", recovery, ok)
	}
}

func TestMigrationCompletionSurfacesFailureAndKeepsRecoveryAction(t *testing.T) {
	projectRoot := initializeSettingsGitWorktree(t)
	writeLegacyProjectConfig(t, projectRoot)
	t.Chdir(projectRoot)

	m := newHubModel("dev", false)
	nextModel, cmd := m.Update(ProjectConfigMigrationDoneMsg{Err: errors.New("exit status 1")})
	if cmd != nil {
		t.Fatalf("failed migration returned command: %v", cmd)
	}
	next := nextModel.(hubModel)
	if next.err == nil || !strings.Contains(next.err.Error(), "migration failed") {
		t.Fatalf("failed migration error = %v", next.err)
	}
	if recovery, ok := projectConfigRecoveryForError(next.err, projectRoot); !ok || recovery.Action != setupActionMigrateProject {
		t.Fatalf("failed migration lost recovery: %#v, %v", recovery, ok)
	}
}

func TestMigrationCompletionRefreshesAfterCanonicalConfig(t *testing.T) {
	projectRoot := initializeSettingsGitWorktree(t)
	writeSettingsConfig(t, projectRoot, canonicalSettingsConfig(300))
	t.Chdir(projectRoot)

	m := newHubModel("dev", false)
	nextModel, cmd := m.Update(ProjectConfigMigrationDoneMsg{})
	if cmd == nil {
		t.Fatal("successful migration did not schedule refresh/authentication")
	}
	next := nextModel.(hubModel)
	if next.err != nil || !next.loading || next.currentView != viewDashboard {
		t.Fatalf("successful migration state = err %v loading %v view %v", next.err, next.loading, next.currentView)
	}
}

func TestSetupActionFailureIsSurfaced(t *testing.T) {
	m := newHubModel("dev", false)
	nextModel, cmd := m.Update(SetupActionMsg{Action: setupActionInitializeProject, Err: errors.New("init failed")})
	if cmd != nil {
		t.Fatalf("failed setup action returned command: %v", cmd)
	}
	next := nextModel.(hubModel)
	if next.currentView != viewDashboard || next.err == nil || !strings.Contains(next.err.Error(), "init failed") {
		t.Fatalf("failed setup action state = view %v err %v", next.currentView, next.err)
	}
}

func TestProjectConfigHealthDiagnosticCarriesLegacyCode(t *testing.T) {
	code, message := projectConfigHealthDiagnostic(legacyConfigError(), "")
	if code != "legacy_config_requires_migration" || !strings.Contains(message, projectConfigMigrateCommand("")) {
		t.Fatalf("health diagnostic = code %q message %q", code, message)
	}
}
