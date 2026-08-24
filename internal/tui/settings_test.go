package tui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/config"
)

const settingsProjectID = "77c1943b-9c5e-4b66-bf65-40f719da5f6e"

func TestSettingsLoadsNearestCanonicalTimeoutAndOmitsBrowserPreference(t *testing.T) {
	worktree := initializeSettingsGitWorktree(t)
	projectRoot := filepath.Join(worktree, "apps", "mobile")
	configPath := writeSettingsConfig(t, projectRoot, canonicalSettingsConfig(420))
	nested := filepath.Join(projectRoot, "src", "screens")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	chdirForTest(t, nested)

	m := newHubModel("dev", false)
	for i, action := range quickActions {
		if action.Key == "settings" {
			m.actionCursor = i
			break
		}
	}
	model, _ := m.executeQuickAction()
	m = asHub(t, model)
	if m.currentView != viewSettings {
		t.Fatalf("current view = %v, want settings", m.currentView)
	}
	actualInfo, actualErr := os.Stat(m.settingsConfigPath)
	wantInfo, wantErr := os.Stat(configPath)
	if actualErr != nil || wantErr != nil || !os.SameFile(actualInfo, wantInfo) || m.settingsTimeout != 420 {
		t.Fatalf("path = %q, timeout = %d", m.settingsConfigPath, m.settingsTimeout)
	}
	view := m.renderSettings()
	if strings.Contains(view, "open_browser") {
		t.Fatalf("settings still exposes retired open_browser: %q", view)
	}
	if !strings.Contains(view, "session.idle_timeout_seconds") {
		t.Fatalf("settings missing canonical timeout field: %q", view)
	}
}

func TestWriteCanonicalSettingsTimeoutPreservesOtherSessionFields(t *testing.T) {
	worktree := initializeSettingsGitWorktree(t)
	projectRoot := filepath.Join(worktree, "apps", "mobile")
	authored := canonicalSettingsConfig(300)
	scriptPath := "scripts/setup.sh"
	authored.Session.BeforeScript = &config.AuthoredBeforeScript{ScriptPath: &scriptPath}
	configPath := writeSettingsConfig(t, projectRoot, authored)
	local, err := config.ResolveProjectContext(projectRoot, "")
	if err != nil {
		t.Fatalf("ResolveProjectContext() error = %v", err)
	}

	if err := writeCanonicalSettingsTimeout(local, 900); err != nil {
		t.Fatalf("writeCanonicalSettingsTimeout() error = %v", err)
	}
	updatedBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := config.ParseAuthoredConfig(updatedBytes)
	if err != nil {
		t.Fatalf("ParseAuthoredConfig() error = %v", err)
	}
	if updated.Project.ID != settingsProjectID || updated.Session == nil || updated.Session.IdleTimeoutSeconds == nil || *updated.Session.IdleTimeoutSeconds != 900 {
		t.Fatalf("updated config = %#v", updated)
	}
	if updated.Session.BeforeScript == nil || updated.Session.BeforeScript.ScriptPath == nil || *updated.Session.BeforeScript.ScriptPath != scriptPath {
		t.Fatalf("before script was not preserved: %#v", updated.Session)
	}
}

func TestWriteCanonicalSettingsTimeoutRejectsStaleLocalBytes(t *testing.T) {
	worktree := initializeSettingsGitWorktree(t)
	projectRoot := filepath.Join(worktree, "apps", "mobile")
	configPath := writeSettingsConfig(t, projectRoot, canonicalSettingsConfig(300))
	local, err := config.ResolveProjectContext(projectRoot, "")
	if err != nil {
		t.Fatalf("ResolveProjectContext() error = %v", err)
	}
	external := canonicalSettingsConfig(600)
	externalBytes, err := config.MarshalCanonicalConfig(external)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, externalBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	err = writeCanonicalSettingsTimeout(local, 900)
	var configErr *config.ConfigError
	if !errors.As(err, &configErr) || configErr.Code != "config_changed_before_write" {
		t.Fatalf("writeCanonicalSettingsTimeout() error = %v", err)
	}
	current, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(externalBytes) {
		t.Fatal("stale settings write changed the externally updated config")
	}
}

func TestSettingsCursorIsBoundedToCanonicalTimeoutAndSave(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewSettings

	model, _ := m.handleSettingsKey(keyMsg("down"))
	m = asHub(t, model)
	model, _ = m.handleSettingsKey(keyMsg("down"))
	m = asHub(t, model)
	if m.settingsCursor != 1 {
		t.Fatalf("settings cursor = %d, want 1", m.settingsCursor)
	}
}

func canonicalSettingsConfig(timeout int) config.AuthoredConfig {
	return config.AuthoredConfig{
		Project: config.AuthoredProject{ID: settingsProjectID},
		Session: &config.AuthoredSession{IdleTimeoutSeconds: &timeout},
	}
}

func initializeSettingsGitWorktree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	command := exec.Command("git", "init", "-q", root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	return root
}

func writeSettingsConfig(t *testing.T, projectRoot string, authored config.AuthoredConfig) string {
	t.Helper()
	configDir := filepath.Join(projectRoot, ".revyl")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	contents, err := config.MarshalCanonicalConfig(authored)
	if err != nil {
		t.Fatalf("MarshalCanonicalConfig() error = %v", err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func chdirForTest(t *testing.T, directory string) {
	t.Helper()
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}
