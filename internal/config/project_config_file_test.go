package config

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const projectFileTestProjectID = "11111111-1111-4111-8111-111111111111"

func TestResolveProjectContextUsesChangeDirectoryAndNearestConfig(t *testing.T) {
	worktreeRoot := t.TempDir()
	gitInitConfigTestRepository(t, worktreeRoot)
	rootConfig := projectFileTestConfig(projectFileTestProjectID, "root")
	nestedConfig := projectFileTestConfig(projectFileTestProjectID, "nested")
	writeProjectFileTestConfig(t, worktreeRoot, rootConfig, 0o640)
	projectRoot := filepath.Join(worktreeRoot, "apps", "mobile")
	writeProjectFileTestConfig(t, projectRoot, nestedConfig, 0o600)
	executionDirectory := filepath.Join(projectRoot, "src", "screens")
	if err := os.MkdirAll(executionDirectory, 0o755); err != nil {
		t.Fatal(err)
	}

	context, err := ResolveProjectContext(worktreeRoot, filepath.Join("apps", "mobile", "src", "screens"))
	if err != nil {
		t.Fatal(err)
	}
	resolvedWorktreeRoot, err := filepath.EvalSymlinks(worktreeRoot)
	if err != nil {
		t.Fatal(err)
	}
	resolvedProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if context.WorktreeRoot != resolvedWorktreeRoot {
		t.Fatalf("worktree root = %q, want %q", context.WorktreeRoot, resolvedWorktreeRoot)
	}
	if context.ProjectRoot != resolvedProjectRoot {
		t.Fatalf("project root = %q, want %q", context.ProjectRoot, resolvedProjectRoot)
	}
	if context.ConfigPath != filepath.Join(resolvedProjectRoot, ".revyl", "config.yaml") {
		t.Fatalf("config path = %q", context.ConfigPath)
	}
	if context.RepositoryRelativeProjectRoot != "apps/mobile" {
		t.Fatalf("repository-relative project root = %q", context.RepositoryRelativeProjectRoot)
	}
	if context.RepositoryRelativeExecutionDirectory != "apps/mobile/src/screens" {
		t.Fatalf("repository-relative execution directory = %q", context.RepositoryRelativeExecutionDirectory)
	}
	if string(context.OriginalBytes) != nestedConfig {
		t.Fatalf("original bytes = %q, want exact nested config bytes", context.OriginalBytes)
	}
	if context.Authored.Project.ID != projectFileTestProjectID || context.Aggregate.ProjectID != projectFileTestProjectID {
		t.Fatalf("project IDs = %q, %q", context.Authored.Project.ID, context.Aggregate.ProjectID)
	}
}

func TestResolveProjectContextDoesNotCrossGitWorktree(t *testing.T) {
	parent := t.TempDir()
	writeProjectFileTestConfig(t, parent, projectFileTestConfig(projectFileTestProjectID, "parent"), 0o600)
	worktreeRoot := filepath.Join(parent, "repository")
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInitConfigTestRepository(t, worktreeRoot)

	_, err := ResolveProjectContext(worktreeRoot, "")
	assertConfigError(t, err, "read", "config_not_found")
}

func TestResolveProjectContextRequiresGitWorktree(t *testing.T) {
	root := t.TempDir()
	writeProjectFileTestConfig(t, root, projectFileTestConfig(projectFileTestProjectID, "local"), 0o600)

	_, err := ResolveProjectContext(root, "")
	assertConfigError(t, err, "read", "git_worktree_unavailable")
}

func TestResolveProjectContextAcceptsProviderNeutralRemote(t *testing.T) {
	root := t.TempDir()
	gitInitConfigTestRepository(t, root)
	if output, err := exec.Command("git", "-C", root, "remote", "add", "origin", "git@gitlab.example:team/mobile.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v: %s", err, output)
	}
	writeProjectFileTestConfig(t, root, projectFileTestConfig(projectFileTestProjectID, "build"), 0o600)
	if _, err := ResolveProjectContext(root, ""); err != nil {
		t.Fatalf("provider-neutral project resolution failed: %v", err)
	}
}

func TestResolveProjectContextAcceptsLinkedWorktree(t *testing.T) {
	repository := t.TempDir()
	gitInitConfigTestRepository(t, repository)
	for _, args := range [][]string{{"config", "user.name", "Revyl Test"}, {"config", "user.email", "test@revyl.invalid"}} {
		if output, err := exec.Command("git", append([]string{"-C", repository}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", repository, "add", "README.md").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, output)
	}
	if output, err := exec.Command("git", "-C", repository, "commit", "--quiet", "-m", "fixture").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, output)
	}
	linked := filepath.Join(t.TempDir(), "linked")
	if output, err := exec.Command("git", "-C", repository, "worktree", "add", "--quiet", "-b", "linked-test", linked).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, output)
	}
	writeProjectFileTestConfig(t, linked, projectFileTestConfig(projectFileTestProjectID, "build"), 0o600)
	project, err := ResolveProjectContext(linked, "")
	if err != nil {
		t.Fatalf("linked-worktree resolution failed: %v", err)
	}
	wantRoot, err := filepath.EvalSymlinks(linked)
	if err != nil {
		t.Fatal(err)
	}
	if project.WorktreeRoot != wantRoot {
		t.Fatalf("worktree root = %q, want linked root %q", project.WorktreeRoot, wantRoot)
	}
}

func TestMarshalCanonicalConfigIsStableAndStrict(t *testing.T) {
	const canonicalID = "11111111-1111-4111-8111-aaaaaaaaaaaa"
	raw := projectFileTestConfig("11111111-1111-4111-8111-AAAAAAAAAAAA", "canonical")
	authored, err := ParseAuthoredConfig([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	first, err := MarshalCanonicalConfig(*authored)
	if err != nil {
		t.Fatal(err)
	}
	secondAuthored, err := ParseAuthoredConfig(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MarshalCanonicalConfig(*secondAuthored)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("canonical marshal is unstable:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if !strings.Contains(string(first), "id: "+canonicalID+"\n") {
		t.Fatalf("canonical project UUID/order not materialized:\n%s", first)
	}
	if !strings.HasSuffix(string(first), "\n") {
		t.Fatalf("canonical config is not newline terminated: %q", first)
	}
}

func TestMarshalCanonicalConfigFallsBackToBoundedJSONCompatibleYAML(t *testing.T) {
	commands := []string{strings.Repeat("x\n", 280_000)}
	authored := AuthoredConfig{
		Project: AuthoredProject{ID: projectFileTestProjectID},
		Build: &AuthoredBuild{
			Framework: "expo",
			Profiles: map[string]AuthoredBuildProfile{
				"local": {
					Android: &AuthoredBuildRecipe{BuildCommands: &commands},
				},
			},
		},
	}

	canonical, err := MarshalCanonicalConfig(authored)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical) > MaxConfigBytes {
		t.Fatalf("canonical config exceeds file limit: %d", len(canonical))
	}
	if canonical[0] != '{' {
		t.Fatalf("expected JSON-compatible YAML fallback, got %q", canonical[:1])
	}
	if _, err := ParseAuthoredConfig(canonical); err != nil {
		t.Fatalf("fallback is not readable canonical config: %v", err)
	}
}

func TestMarshalCanonicalConfigCompactFallbackDoesNotEscapeHTML(t *testing.T) {
	commands := []string{strings.Repeat("x\n", 200_000)}
	authored := AuthoredConfig{
		Project: AuthoredProject{ID: projectFileTestProjectID},
		Build: &AuthoredBuild{
			Framework: "expo",
			Env:       map[string]string{"HTML": strings.Repeat("<", 80_000)},
			Profiles: map[string]AuthoredBuildProfile{
				"local": {
					Android: &AuthoredBuildRecipe{BuildCommands: &commands},
				},
			},
		},
	}

	canonical, err := MarshalCanonicalConfig(authored)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical) > MaxConfigBytes {
		t.Fatalf("canonical config exceeds file limit: %d", len(canonical))
	}
	if canonical[0] != '{' {
		t.Fatalf("expected compact fallback, got %q", canonical[:1])
	}
	if bytes.Contains(canonical, []byte(`\u003c`)) || !bytes.Contains(canonical, []byte("<")) {
		t.Fatal("compact fallback escaped HTML-sensitive content")
	}
}

func TestCompareConfigSemanticsIgnoresFormattingButIncludesProjectIdentity(t *testing.T) {
	left := []byte("# comment\nproject:\n  id: " + projectFileTestProjectID + "\nbuild:\n  framework: expo\n  profiles:\n    dev:\n      ios:\n        env:\n          B: two\n          A: one\n        build_commands: [build]\n")
	right := []byte("build:\n  profiles:\n    dev:\n      ios:\n        build_commands:\n          - build\n        env: {A: one, B: two}\n  framework: expo\nproject: {id: " + projectFileTestProjectID + "}\n")
	compilationContext := CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."}

	comparison, err := CompareConfigSemantics(left, right, compilationContext)
	if err != nil {
		t.Fatal(err)
	}
	if !comparison.Equal || comparison.LeftHash != comparison.RightHash {
		t.Fatalf("format-only comparison = %+v, want equal hashes and semantics", comparison)
	}

	differentProject := []byte(strings.ReplaceAll(string(right), projectFileTestProjectID, "22222222-2222-4222-8222-222222222222"))
	comparison, err = CompareConfigSemantics(left, differentProject, compilationContext)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Equal {
		t.Fatal("different project IDs compared equal")
	}
	if comparison.LeftHash != comparison.RightHash {
		t.Fatalf("scoped aggregate hashes unexpectedly include project identity: %+v", comparison)
	}

	changedRecipe := []byte(strings.Replace(string(right), "- build", "- build-changed", 1))
	comparison, err = CompareConfigSemantics(left, changedRecipe, compilationContext)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Equal || comparison.LeftHash == comparison.RightHash {
		t.Fatalf("changed recipe comparison = %+v, want divergent semantics", comparison)
	}
}

func TestCreateConfigBackupPreservesExactBytesModeAndExistingBackup(t *testing.T) {
	root := t.TempDir()
	configPath := writeProjectFileTestConfig(t, root, "# exact comment\n"+projectFileTestConfig(projectFileTestProjectID, "backup"), 0o640)
	originalNow := configBackupNow
	configBackupNow = func() time.Time {
		return time.Date(2026, time.August, 11, 23, 4, 5, 0, time.UTC)
	}
	t.Cleanup(func() { configBackupNow = originalNow })
	firstBackup := configPath + ".bak.20260811T230405Z"
	if err := os.WriteFile(firstBackup, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}

	backupPath, err := CreateConfigBackup(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if backupPath != configPath+".bak.20260811T230405Z.1" {
		t.Fatalf("backup path = %q, want collision-safe suffix", backupPath)
	}
	if existing, err := os.ReadFile(firstBackup); err != nil || string(existing) != "existing" {
		t.Fatalf("existing backup changed: %q, %v", existing, err)
	}
	want, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("backup bytes = %q, want exact %q", got, want)
	}
	if runtime.GOOS != "windows" {
		metadata, err := os.Stat(backupPath)
		if err != nil {
			t.Fatal(err)
		}
		if metadata.Mode().Perm() != 0o640 {
			t.Fatalf("backup mode = %o, want 640", metadata.Mode().Perm())
		}
	}
}

func TestReplaceConfigAtomicallyPreservesModeAndRejectsInvalidReplacement(t *testing.T) {
	root := t.TempDir()
	original := projectFileTestConfig(projectFileTestProjectID, "original")
	configPath := writeProjectFileTestConfig(t, root, original, 0o640)

	if err := ReplaceConfigAtomically(configPath, []byte("project: [invalid"), []byte(original)); err == nil {
		t.Fatal("invalid replacement succeeded")
	}
	unchanged, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != original {
		t.Fatalf("invalid replacement changed original to %q", unchanged)
	}

	replacement := projectFileTestConfig("22222222-2222-4222-8222-222222222222", "replacement")
	if err := ReplaceConfigAtomically(configPath, []byte(replacement), []byte("stale local bytes")); err == nil {
		t.Fatal("replacement with stale expected bytes succeeded")
	} else {
		assertConfigError(t, err, "write", "config_changed_before_write")
	}
	if err := ReplaceConfigAtomically(configPath, []byte(replacement), []byte(original)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != replacement {
		t.Fatalf("replacement bytes = %q, want %q", got, replacement)
	}
	if runtime.GOOS != "windows" {
		metadata, err := os.Stat(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if metadata.Mode().Perm() != 0o640 {
			t.Fatalf("replacement mode = %o, want 640", metadata.Mode().Perm())
		}
	}
	temporaryMatches, err := filepath.Glob(filepath.Join(filepath.Dir(configPath), ".config.yaml.replace-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporaryMatches) != 0 {
		t.Fatalf("temporary replacements leaked: %v", temporaryMatches)
	}
}

func TestConfigWritesRejectSymlinkedConfig(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, ".revyl")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realConfig := filepath.Join(root, "real-config.yaml")
	if err := os.WriteFile(realConfig, []byte(projectFileTestConfig(projectFileTestProjectID, "real")), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.Symlink(realConfig, configPath); err != nil {
		t.Fatal(err)
	}

	_, err := CreateConfigBackup(configPath)
	assertConfigError(t, err, "read", "config_not_regular_file")
	err = ReplaceConfigAtomically(
		configPath,
		[]byte(projectFileTestConfig(projectFileTestProjectID, "replacement")),
		[]byte(projectFileTestConfig(projectFileTestProjectID, "real")),
	)
	assertConfigError(t, err, "read", "config_not_regular_file")
}

func gitInitConfigTestRepository(t *testing.T, root string) {
	t.Helper()
	command := exec.Command("git", "-C", root, "init", "--quiet")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
}

func writeProjectFileTestConfig(t *testing.T, projectRoot, content string, mode os.FileMode) string {
	t.Helper()
	configPath := filepath.Join(projectRoot, ".revyl", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func projectFileTestConfig(projectID, command string) string {
	return "project:\n  id: " + projectID + "\nbuild:\n  framework: expo\n  profiles:\n    dev:\n      ios:\n        build_commands:\n          - " + command + "\n"
}
