package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/config"
)

func TestInspectProjectConfigUsesNearestCanonicalProject(t *testing.T) {
	repository := t.TempDir()
	gitInitBuildRepository(t, repository)
	projectRoot := filepath.Join(repository, "mobile")
	nested := filepath.Join(projectRoot, "packages", "app")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	const projectID = "11111111-1111-4111-8111-111111111111"
	writeProjectBuildConfig(t, projectRoot, "project:\n  id: "+projectID+"\n")
	withWorkingDir(t, nested)

	check, project := inspectProjectConfig()
	if check.Status != "ok" {
		t.Fatalf("status = %q, details = %q", check.Status, check.Details)
	}
	resolvedRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if project == nil || project.ProjectRoot != resolvedRoot {
		t.Fatalf("project = %#v, want root %q", project, resolvedRoot)
	}
	if !strings.Contains(check.Details, projectID) {
		t.Fatalf("details = %q, want canonical project ID", check.Details)
	}
}

func TestInspectProjectConfigRejectsLegacyConfig(t *testing.T) {
	repository := t.TempDir()
	gitInitBuildRepository(t, repository)
	writeProjectBuildConfig(t, repository, "project:\n  name: Legacy\n  org_id: org-1\n")
	withWorkingDir(t, repository)

	check, project := inspectProjectConfig()
	if check.Status != "error" || project != nil {
		t.Fatalf("check = %#v, project = %#v", check, project)
	}
	if !strings.Contains(check.Details, "config migrate") {
		t.Fatalf("details = %q, want migration guidance", check.Details)
	}
	output := captureStdoutAndStderr(t, func() {
		printDoctorResults(DoctorResult{
			Checks:  []DoctorCheck{check},
			Issues:  1,
			Healthy: false,
		})
	})
	if !strings.Contains(output, "Preview migration:") || !strings.Contains(output, "revyl config migrate --check") {
		t.Fatalf("doctor output = %q, want top-level migration recovery", output)
	}
	if strings.Contains(output, "Initialize project:") || strings.Contains(output, "revyl init") {
		t.Fatalf("doctor output = %q, legacy config must never recommend init", output)
	}
}

func TestInspectProjectConfigOffersExactNestedProjectCommands(t *testing.T) {
	repository := t.TempDir()
	gitInitBuildRepository(t, repository)
	projectRoots := []string{
		filepath.Join(repository, "apps", "Android"),
		filepath.Join(repository, "apps", "My App"),
	}
	for index, projectRoot := range projectRoots {
		projectID := fmt.Sprintf("10000000-0000-4000-8000-%012d", index+1)
		writeProjectBuildConfig(t, projectRoot, "project:\n  id: "+projectID+"\n")
	}
	withWorkingDir(t, repository)

	check, project := inspectProjectConfig()
	if check.Status != "warning" || project != nil || check.Message != "Nested project selection required" {
		t.Fatalf("check = %#v, project = %#v", check, project)
	}
	if len(check.nextSteps) != len(projectRoots) {
		t.Fatalf("next steps = %#v, want one per nested project", check.nextSteps)
	}
	for index, projectRoot := range projectRoots {
		command := check.nextSteps[index].Command
		requireCLIRecoveryCommand(t, command, projectRoot, "doctor")
		if !strings.Contains(check.Details, command) {
			t.Fatalf("nested recovery details = %q, want %q", check.Details, command)
		}
	}

	output := captureStdoutAndStderr(t, func() {
		printDoctorResults(DoctorResult{Checks: []DoctorCheck{check}, Issues: 1, Healthy: false})
	})
	for _, step := range check.nextSteps {
		if !strings.Contains(output, step.Command) {
			t.Fatalf("doctor output = %q, want %q", output, step.Command)
		}
	}
	if strings.Contains(output, "Initialize project:") || strings.Contains(output, "revyl init") {
		t.Fatalf("doctor output = %q, nested projects must not recommend root initialization", output)
	}
}

func TestInspectProjectConfigMissingRecoveryPreservesSelectedRoot(t *testing.T) {
	repository := t.TempDir()
	gitInitBuildRepository(t, repository)
	selectedRoot := filepath.Join(repository, "apps", "My App")
	if err := os.MkdirAll(selectedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	withWorkingDir(t, selectedRoot)
	originalRoot, _ := rootCmd.PersistentFlags().GetString("chdir")
	t.Cleanup(func() { _ = rootCmd.PersistentFlags().Set("chdir", originalRoot) })
	if err := rootCmd.PersistentFlags().Set("chdir", selectedRoot); err != nil {
		t.Fatal(err)
	}

	check, project := inspectProjectConfig()
	if check.Status != "warning" || project != nil || check.Message != "No project configuration" {
		t.Fatalf("check = %#v, project = %#v", check, project)
	}
	wantPull := cliRecoveryCommand("config", "pull")
	wantInit := cliRecoveryCommand("init", "-y")
	if len(check.nextSteps) != 2 || check.nextSteps[0].Command != wantPull || check.nextSteps[1].Command != wantInit {
		t.Fatalf("next steps = %#v, want %q and %q", check.nextSteps, wantPull, wantInit)
	}
	quotedRoot := "'" + selectedRoot + "'"
	if runtime.GOOS == "windows" {
		quotedRoot = `"` + selectedRoot + `"`
	}
	if !strings.Contains(wantPull, "-C "+quotedRoot) || !strings.Contains(wantInit, "-C "+quotedRoot) {
		t.Fatalf("recovery commands did not preserve selected root: %q, %q", wantPull, wantInit)
	}
}

func TestInspectProjectConfigReportsNonGitBoundary(t *testing.T) {
	withWorkingDir(t, t.TempDir())

	check, project := inspectProjectConfig()
	if check.Status != "warning" || project != nil {
		t.Fatalf("check = %#v, project = %#v", check, project)
	}
	if check.Message != "Not in a Git worktree" {
		t.Fatalf("message = %q", check.Message)
	}
}

func TestCheckSyncStatusUsesCanonicalTestsDir(t *testing.T) {
	projectRoot := t.TempDir()
	testsDir := filepath.Join(projectRoot, ".revyl", "tests")
	if err := os.MkdirAll(testsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testsDir, "checkout.yaml"), []byte("_meta:\n  remote_id: 11111111-1111-4111-8111-111111111111\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	project := &config.ProjectContext{ProjectRoot: projectRoot, TestsDir: testsDir}

	check := checkSyncStatus(context.Background(), project, nil)
	if check.Status != "ok" || !strings.Contains(check.Message, "1 linked local test") {
		t.Fatalf("check = %#v", check)
	}
}
