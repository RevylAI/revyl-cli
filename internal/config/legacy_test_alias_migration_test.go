package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupAndReplaceConfigWithLegacyTestAliasesCreatesRecoverableFiles(t *testing.T) {
	configPath, original, result := legacyAliasMigrationFixture(t)
	plans, err := PlanLegacyConfigTestAliases(configPath, result.TestAliases)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].Disposition != LegacyConfigTestAliasCreate {
		t.Fatalf("plans = %#v, want one create", plans)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(configPath), "tests")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only plan created tests directory: %v", err)
	}

	backupPath, err := BackupAndReplaceConfigWithLegacyTestAliases(configPath, result.CanonicalBytes, original, result.TestAliases)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, original) {
		t.Fatalf("backup changed original bytes\n got: %q\nwant: %q", backup, original)
	}
	aliasPath := filepath.Join(filepath.Dir(configPath), "tests", "login-flow.yaml")
	local, err := LoadLocalTest(aliasPath)
	if err != nil {
		t.Fatal(err)
	}
	if local.Meta.RemoteID != "44444444-4444-4444-8444-444444444444" || local.Test.Metadata.Name != "login-flow" {
		t.Fatalf("migrated alias = %#v", local)
	}
	if leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(aliasPath), ".login-flow.yaml.migrate-*")); err != nil || len(leftovers) != 0 {
		t.Fatalf("private migration temp files = %v, error = %v", leftovers, err)
	}
	if _, err := ParseAuthoredConfig(mustReadLegacyAliasFile(t, configPath)); err != nil {
		t.Fatalf("replacement is not canonical: %v", err)
	}
}

func TestPlanLegacyConfigTestAliasesPreservesMatchingAuthoredFile(t *testing.T) {
	configPath, original, result := legacyAliasMigrationFixture(t)
	testsDir := filepath.Join(filepath.Dir(configPath), "tests")
	if err := os.MkdirAll(testsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	authorBytes := []byte("# authored\n_meta:\n  remote_id: 44444444-4444-4444-8444-444444444444\ntest:\n  metadata:\n    name: authored-name\n    description: Keep this authored definition\n")
	aliasPath := filepath.Join(testsDir, "login-flow.yaml")
	if err := os.WriteFile(aliasPath, authorBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	plans, err := PlanLegacyConfigTestAliases(configPath, result.TestAliases)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].Disposition != LegacyConfigTestAliasReuse {
		t.Fatalf("plans = %#v, want one reuse", plans)
	}
	if _, err := BackupAndReplaceConfigWithLegacyTestAliases(configPath, result.CanonicalBytes, original, result.TestAliases); err != nil {
		t.Fatal(err)
	}
	if got := mustReadLegacyAliasFile(t, aliasPath); !bytes.Equal(got, authorBytes) {
		t.Fatalf("authored alias file changed\n got: %q\nwant: %q", got, authorBytes)
	}
}

func TestPlanLegacyConfigTestAliasesFailsBeforeBackupOnConflict(t *testing.T) {
	configPath, original, result := legacyAliasMigrationFixture(t)
	testsDir := filepath.Join(filepath.Dir(configPath), "tests")
	if err := os.MkdirAll(testsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasPath := filepath.Join(testsDir, "login-flow.yaml")
	if err := os.WriteFile(aliasPath, []byte("_meta:\n  remote_id: 55555555-5555-4555-8555-555555555555\ntest:\n  metadata:\n    name: other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	backupCalled := false
	originalBackupWriter := legacyConfigAliasBackupWriter
	legacyConfigAliasBackupWriter = func(string, []byte, os.FileMode) (string, error) {
		backupCalled = true
		return "", nil
	}
	t.Cleanup(func() { legacyConfigAliasBackupWriter = originalBackupWriter })
	_, err := BackupAndReplaceConfigWithLegacyTestAliases(configPath, result.CanonicalBytes, original, result.TestAliases)
	assertConfigError(t, err, "write", "test_alias_destination_conflict")
	if backupCalled {
		t.Fatal("conflicting destination created a backup")
	}
}

func TestBackupAndReplaceConfigWithLegacyTestAliasesCreatesNoStubWhenBackupFails(t *testing.T) {
	configPath, original, result := legacyAliasMigrationFixture(t)
	originalBackupWriter := legacyConfigAliasBackupWriter
	legacyConfigAliasBackupWriter = func(string, []byte, os.FileMode) (string, error) {
		return "", errors.New("backup unavailable")
	}
	t.Cleanup(func() { legacyConfigAliasBackupWriter = originalBackupWriter })
	_, err := BackupAndReplaceConfigWithLegacyTestAliases(configPath, result.CanonicalBytes, original, result.TestAliases)
	if err == nil {
		t.Fatal("expected backup failure")
	}
	aliasPath := filepath.Join(filepath.Dir(configPath), "tests", "login-flow.yaml")
	if _, statErr := os.Stat(aliasPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("backup failure left alias file: %v", statErr)
	}
}

func TestBackupAndReplaceConfigWithLegacyTestAliasesClassifiesPublicationFailure(t *testing.T) {
	configPath, original, result := legacyAliasMigrationFixture(t)
	originalPublisher := legacyConfigAliasPublisher
	legacyConfigAliasPublisher = func(string, []byte) error {
		return errors.New("storage unavailable")
	}
	t.Cleanup(func() { legacyConfigAliasPublisher = originalPublisher })
	_, err := BackupAndReplaceConfigWithLegacyTestAliases(configPath, result.CanonicalBytes, original, result.TestAliases)
	assertConfigError(t, err, "write", "test_alias_publish_failed")
	if got := mustReadLegacyAliasFile(t, configPath); !bytes.Equal(got, original) {
		t.Fatalf("publication failure changed config\n got: %q\nwant: %q", got, original)
	}
}

func TestBackupAndReplaceConfigWithLegacyTestAliasesRollsBackUnchangedStubOnCASFailure(t *testing.T) {
	configPath, original, result := legacyAliasMigrationFixture(t)
	originalReplacer := legacyConfigAliasReplacer
	legacyConfigAliasReplacer = func(string, []byte, []byte) error {
		return errors.New("CAS failed")
	}
	t.Cleanup(func() { legacyConfigAliasReplacer = originalReplacer })
	backupPath, err := BackupAndReplaceConfigWithLegacyTestAliases(configPath, result.CanonicalBytes, original, result.TestAliases)
	if err == nil || backupPath == "" {
		t.Fatalf("backupPath = %q, error = %v, want backup and CAS failure", backupPath, err)
	}
	aliasPath := filepath.Join(filepath.Dir(configPath), "tests", "login-flow.yaml")
	if _, statErr := os.Stat(aliasPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("CAS failure left unchanged created alias: %v", statErr)
	}
	if got := mustReadLegacyAliasFile(t, configPath); !bytes.Equal(got, original) {
		t.Fatalf("CAS failure changed config\n got: %q\nwant: %q", got, original)
	}
}

func TestBackupAndReplaceConfigWithLegacyTestAliasesRejectsDeletedReuseBeforeConfigReplacement(t *testing.T) {
	configPath, original, result := legacyAliasMigrationFixture(t)
	testsDir := filepath.Join(filepath.Dir(configPath), "tests")
	if err := os.MkdirAll(testsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasPath := filepath.Join(testsDir, "login-flow.yaml")
	if err := os.WriteFile(aliasPath, []byte("_meta:\n  remote_id: 44444444-4444-4444-8444-444444444444\ntest:\n  metadata:\n    name: login-flow\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	originalBackupWriter := legacyConfigAliasBackupWriter
	legacyConfigAliasBackupWriter = func(path string, content []byte, mode os.FileMode) (string, error) {
		backupPath, err := originalBackupWriter(path, content, mode)
		if err == nil {
			if removeErr := os.Remove(aliasPath); removeErr != nil {
				t.Fatal(removeErr)
			}
		}
		return backupPath, err
	}
	t.Cleanup(func() { legacyConfigAliasBackupWriter = originalBackupWriter })

	backupPath, err := BackupAndReplaceConfigWithLegacyTestAliases(configPath, result.CanonicalBytes, original, result.TestAliases)
	assertConfigError(t, err, "write", "test_alias_destination_changed")
	if backupPath == "" {
		t.Fatal("expected durable backup before destination changed")
	}
	if got := mustReadLegacyAliasFile(t, configPath); !bytes.Equal(got, original) {
		t.Fatalf("destination race changed config\n got: %q\nwant: %q", got, original)
	}
}

func legacyAliasMigrationFixture(t *testing.T) (string, []byte, *LegacyConfigMigrationResult) {
	t.Helper()
	projectRoot := t.TempDir()
	configDir := filepath.Join(projectRoot, ".revyl")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(migrationLegacyConfig("") + "tests:\n  login-flow: 44444444-4444-4444-8444-444444444444\n")
	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := MigrateLegacyConfigBytes(LegacyConfigMigrationInput{
		Data:               original,
		Context:            CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
		GeneratedProjectID: migrationGeneratedProjectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return configPath, original, result
}

func mustReadLegacyAliasFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
