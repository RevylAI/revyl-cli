package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/revyl/cli/internal/config"
	"github.com/spf13/cobra"
)

func writeTestProjectConfig(t *testing.T, dir string) string {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(dir, ".revyl"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.revyl): %v", err)
	}

	open := true
	cfg := &config.ProjectConfig{
		Project: config.Project{Name: "test-project"},
		Build: config.BuildConfig{
			System:  "expo",
			Command: "echo build",
			Output:  "build.out",
		},
		Defaults: config.Defaults{
			OpenBrowser: &open,
			Timeout:     600,
		},
		Tests:     map[string]string{},
		Workflows: map[string]string{},
	}

	path := filepath.Join(dir, ".revyl", "config.yaml")
	if err := config.WriteProjectConfig(path, cfg); err != nil {
		t.Fatalf("WriteProjectConfig(): %v", err)
	}
	return path
}

func TestRunConfigSetOpenBrowser(t *testing.T) {
	tmp := t.TempDir()
	writeTestProjectConfig(t, tmp)

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	defer func() { _ = os.Chdir(origWD) }()

	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("Chdir(tmp): %v", err)
	}

	cmd := &cobra.Command{}
	if err := runConfigSet(cmd, []string{"open-browser", "false"}); err != nil {
		t.Fatalf("runConfigSet(open-browser): %v", err)
	}

	cfg, err := config.LoadProjectConfig(filepath.Join(tmp, ".revyl", "config.yaml"))
	if err != nil {
		t.Fatalf("LoadProjectConfig(): %v", err)
	}
	if got := config.EffectiveOpenBrowser(cfg); got {
		t.Errorf("EffectiveOpenBrowser() = %v, want false", got)
	}
}

func TestRunConfigSetTimeout(t *testing.T) {
	tmp := t.TempDir()
	writeTestProjectConfig(t, tmp)

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	defer func() { _ = os.Chdir(origWD) }()

	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("Chdir(tmp): %v", err)
	}

	cmd := &cobra.Command{}
	if err := runConfigSet(cmd, []string{"timeout", "900"}); err != nil {
		t.Fatalf("runConfigSet(timeout): %v", err)
	}

	cfg, err := config.LoadProjectConfig(filepath.Join(tmp, ".revyl", "config.yaml"))
	if err != nil {
		t.Fatalf("LoadProjectConfig(): %v", err)
	}
	if got := config.EffectiveTimeoutSeconds(cfg, 0); got != 900 {
		t.Errorf("EffectiveTimeoutSeconds() = %d, want 900", got)
	}
}

func TestRunConfigSetInvalidKey(t *testing.T) {
	tmp := t.TempDir()
	writeTestProjectConfig(t, tmp)

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	defer func() { _ = os.Chdir(origWD) }()

	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("Chdir(tmp): %v", err)
	}

	cmd := &cobra.Command{}
	if err := runConfigSet(cmd, []string{"unknown", "1"}); err == nil {
		t.Fatal("runConfigSet() expected error for unknown key")
	}
}

func TestProjectConfigPathUsesRepoRootFromNestedDir(t *testing.T) {
	tmp := t.TempDir()
	writeTestProjectConfig(t, tmp)

	nested := filepath.Join(tmp, "nested", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll(nested): %v", err)
	}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	defer func() { _ = os.Chdir(origWD) }()

	if err := os.Chdir(nested); err != nil {
		t.Fatalf("Chdir(nested): %v", err)
	}

	got, err := projectConfigPath()
	if err != nil {
		t.Fatalf("projectConfigPath(): %v", err)
	}

	want := filepath.Join(tmp, ".revyl", "config.yaml")
	gotCanonical := canonicalPath(got)
	wantCanonical := canonicalPath(want)
	if gotCanonical != wantCanonical {
		t.Fatalf("projectConfigPath() = %q, want %q", got, want)
	}
}

func TestRunConfigSetFromNestedDirUpdatesRepoRootConfig(t *testing.T) {
	tmp := t.TempDir()
	writeTestProjectConfig(t, tmp)

	nested := filepath.Join(tmp, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll(nested): %v", err)
	}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	defer func() { _ = os.Chdir(origWD) }()

	if err := os.Chdir(nested); err != nil {
		t.Fatalf("Chdir(nested): %v", err)
	}

	cmd := &cobra.Command{}
	if err := runConfigSet(cmd, []string{"timeout", "1200"}); err != nil {
		t.Fatalf("runConfigSet(timeout): %v", err)
	}

	cfg, err := config.LoadProjectConfig(filepath.Join(tmp, ".revyl", "config.yaml"))
	if err != nil {
		t.Fatalf("LoadProjectConfig(): %v", err)
	}
	if got := config.EffectiveTimeoutSeconds(cfg, 0); got != 1200 {
		t.Fatalf("EffectiveTimeoutSeconds() = %d, want 1200", got)
	}
}

func canonicalPath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	return path
}
