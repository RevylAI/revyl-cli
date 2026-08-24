package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

var (
	legacyConfigAliasBackupWriter = writeConfigBackup
	legacyConfigAliasPublisher    = publishMigratedAliasFile
	legacyConfigAliasReplacer     = ReplaceConfigAtomically
)

// LegacyConfigTestAliasDisposition describes whether migration will create a
// local alias file or preserve an already-matching file byte-for-byte.
type LegacyConfigTestAliasDisposition string

const (
	LegacyConfigTestAliasCreate LegacyConfigTestAliasDisposition = "create"
	LegacyConfigTestAliasReuse  LegacyConfigTestAliasDisposition = "reuse"
)

// LegacyConfigTestAliasPlan is the read-only file plan shown by config migrate.
type LegacyConfigTestAliasPlan struct {
	Alias       string                           `json:"alias"`
	Path        string                           `json:"path"`
	Disposition LegacyConfigTestAliasDisposition `json:"disposition"`

	destinationPath string
	stubBytes       []byte
}

// PlanLegacyConfigTestAliases validates every legacy alias destination before
// config migration mutates the filesystem. Existing authored test files are
// reusable only when they already point at the exact same remote test.
func PlanLegacyConfigTestAliases(configPath string, aliases []LegacyTestAlias) ([]LegacyConfigTestAliasPlan, error) {
	if len(aliases) == 0 {
		return nil, nil
	}
	testsDir := filepath.Join(filepath.Dir(configPath), "tests")
	if metadata, err := os.Lstat(testsDir); err == nil {
		if metadata.Mode()&os.ModeSymlink != 0 || !metadata.IsDir() {
			return nil, newConfigError("write", "test_alias_directory_conflict", []string{"tests"}, "")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, newConfigError("write", "test_alias_directory_unreadable", []string{"tests"}, "")
	}

	plans := make([]LegacyConfigTestAliasPlan, 0, len(aliases))
	for _, alias := range aliases {
		destination := filepath.Join(testsDir, alias.Alias+".yaml")
		stubBytes, err := marshalMigratedTestAlias(alias)
		if err != nil {
			return nil, err
		}
		plan := LegacyConfigTestAliasPlan{
			Alias:           alias.Alias,
			Path:            filepath.ToSlash(filepath.Join(".revyl", "tests", alias.Alias+".yaml")),
			Disposition:     LegacyConfigTestAliasCreate,
			destinationPath: destination,
			stubBytes:       stubBytes,
		}
		metadata, statErr := os.Lstat(destination)
		switch {
		case errors.Is(statErr, os.ErrNotExist):
			// The exclusive writer rechecks absence after the backup is durable.
		case statErr != nil:
			return nil, newConfigError("write", "test_alias_destination_unreadable", []string{"tests", alias.Alias}, "")
		case metadata.Mode()&os.ModeSymlink != 0 || !metadata.Mode().IsRegular():
			return nil, newConfigError("write", "test_alias_destination_conflict", []string{"tests", alias.Alias}, "")
		default:
			content, readErr := os.ReadFile(destination)
			if readErr != nil {
				return nil, newConfigError("write", "test_alias_destination_unreadable", []string{"tests", alias.Alias}, "")
			}
			var existing LocalTest
			if unmarshalErr := yaml.Unmarshal(content, &existing); unmarshalErr != nil {
				return nil, newConfigError("write", "test_alias_destination_invalid", []string{"tests", alias.Alias}, "")
			}
			existingID, parseErr := uuid.Parse(existing.Meta.RemoteID)
			if parseErr != nil || existingID.String() != alias.RemoteID {
				return nil, newConfigError("write", "test_alias_destination_conflict", []string{"tests", alias.Alias}, "")
			}
			plan.Disposition = LegacyConfigTestAliasReuse
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func marshalMigratedTestAlias(alias LegacyTestAlias) ([]byte, error) {
	test := LocalTest{
		Meta: TestMeta{RemoteID: alias.RemoteID},
		Test: TestDefinition{Metadata: TestMetadata{Name: alias.Alias}},
	}
	test.Meta.Checksum = ComputeTestChecksum(&test.Test)
	data, err := yaml.Marshal(&test)
	if err != nil {
		return nil, newConfigError("write", "test_alias_encode_failed", []string{"tests", alias.Alias}, "")
	}
	return append([]byte("# Revyl Test Definition\n\n"), data...), nil
}

// BackupAndReplaceConfigWithLegacyTestAliases creates the exact-byte config
// backup, writes missing alias stubs exclusively, then CAS-replaces the config.
// Failures remove only unchanged files created by this invocation; a process
// crash remains safely rerunnable because matching stubs are reusable.
func BackupAndReplaceConfigWithLegacyTestAliases(
	configPath string,
	replacement []byte,
	expectedCurrent []byte,
	aliases []LegacyTestAlias,
) (string, error) {
	if _, err := ParseAuthoredConfig(replacement); err != nil {
		return "", err
	}
	currentBytes, metadata, err := readConfigFileAndMetadata(configPath)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(currentBytes, expectedCurrent) {
		return "", newConfigError("write", "config_changed_before_write", nil, "")
	}
	plans, err := PlanLegacyConfigTestAliases(configPath, aliases)
	if err != nil {
		return "", err
	}
	backupPath, err := legacyConfigAliasBackupWriter(configPath, expectedCurrent, metadata.Mode().Perm())
	if err != nil {
		return "", err
	}

	created := make([]LegacyConfigTestAliasPlan, 0, len(plans))
	rollback := func() error {
		var rollbackErr error
		for _, plan := range created {
			content, readErr := os.ReadFile(plan.destinationPath)
			metadata, statErr := os.Lstat(plan.destinationPath)
			if readErr != nil || statErr != nil || !metadata.Mode().IsRegular() || metadata.Mode()&os.ModeSymlink != 0 || !bytes.Equal(content, plan.stubBytes) {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("alias rollback state changed"))
				continue
			}
			if removeErr := os.Remove(plan.destinationPath); removeErr != nil {
				rollbackErr = errors.Join(rollbackErr, removeErr)
			}
		}
		return rollbackErr
	}

	if len(plans) > 0 {
		testsDir := filepath.Dir(plans[0].destinationPath)
		if err := os.MkdirAll(testsDir, 0o755); err != nil {
			return backupPath, newConfigError("write", "test_alias_directory_create_failed", []string{"tests"}, "")
		}
		directoryMetadata, statErr := os.Lstat(testsDir)
		if statErr != nil || !directoryMetadata.IsDir() || directoryMetadata.Mode()&os.ModeSymlink != 0 {
			return backupPath, newConfigError("write", "test_alias_directory_conflict", []string{"tests"}, "")
		}
	}
	for _, plan := range plans {
		if plan.Disposition == LegacyConfigTestAliasReuse {
			continue
		}
		if createErr := legacyConfigAliasPublisher(plan.destinationPath, plan.stubBytes); createErr != nil {
			if rollbackErr := rollback(); rollbackErr != nil {
				return backupPath, newConfigError("write", "test_alias_rollback_failed", []string{"tests"}, "")
			}
			code := "test_alias_publish_failed"
			if errors.Is(createErr, os.ErrExist) {
				code = "test_alias_destination_changed"
			}
			return backupPath, newConfigError("write", code, []string{"tests", plan.Alias}, "")
		}
		created = append(created, plan)
	}
	aliasPlans, err := PlanLegacyConfigTestAliases(configPath, aliases)
	if err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			return backupPath, newConfigError("write", "test_alias_rollback_failed", []string{"tests"}, "")
		}
		return backupPath, err
	}
	for _, plan := range aliasPlans {
		if plan.Disposition != LegacyConfigTestAliasReuse {
			if rollbackErr := rollback(); rollbackErr != nil {
				return backupPath, newConfigError("write", "test_alias_rollback_failed", []string{"tests"}, "")
			}
			return backupPath, newConfigError("write", "test_alias_destination_changed", []string{"tests", plan.Alias}, "")
		}
	}
	if err := legacyConfigAliasReplacer(configPath, replacement, expectedCurrent); err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			return backupPath, newConfigError("write", "test_alias_rollback_failed", []string{"tests"}, "")
		}
		return backupPath, err
	}
	return backupPath, nil
}

func publishMigratedAliasFile(destinationPath string, content []byte) error {
	directoryPath := filepath.Dir(destinationPath)
	temporary, err := os.CreateTemp(directoryPath, "."+filepath.Base(destinationPath)+".migrate-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := writeSyncedAliasFile(temporary, content); err != nil {
		return err
	}
	// A hard link publishes the already-complete sibling without overwriting a
	// destination that appeared after preflight. A crash can leave only the
	// private temp or the complete canonical file, both safely rerunnable.
	return os.Link(temporaryPath, destinationPath)
}

func writeSyncedAliasFile(file *os.File, content []byte) error {
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
