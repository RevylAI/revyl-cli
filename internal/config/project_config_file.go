package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const gitLookupTimeout = 5 * time.Second

var configBackupNow = time.Now

// ProjectContext is the complete local context for one canonical configuration.
// Every path is resolved after applying -C, and config discovery is bounded by
// the active Git worktree.
type ProjectContext struct {
	EffectiveDirectory                   string
	WorktreeRoot                         string
	ProjectRoot                          string
	ConfigPath                           string
	TestsDir                             string
	RepositoryRelativeProjectRoot        string
	RepositoryRelativeExecutionDirectory string
	Authored                             *AuthoredConfig
	Aggregate                            *NormalizedProjectAggregate
	OriginalBytes                        []byte
}

// ConfigFileContext resolves a config file without assuming whether it is
// legacy or canonical. Migration uses this boundary before classifying the bytes.
type ConfigFileContext struct {
	EffectiveDirectory                   string
	WorktreeRoot                         string
	ProjectRoot                          string
	ConfigPath                           string
	TestsDir                             string
	RepositoryRelativeProjectRoot        string
	RepositoryRelativeExecutionDirectory string
	OriginalBytes                        []byte
}

// CompilationContext returns the pure repository-relative path context for
// strict normalization or explicit legacy translation.
func (c ConfigFileContext) CompilationContext() CompilationContext {
	return CompilationContext{
		RepositoryRelativeProjectRoot: c.RepositoryRelativeProjectRoot,
		ExecutionDirectory:            c.RepositoryRelativeExecutionDirectory,
	}
}

// ConfigSemanticComparison reports whether two canonical configs express the
// same project identity and normalized configuration meaning. The hashes are
// the server-compatible aggregate hashes and therefore intentionally exclude
// project identity on their own.
type ConfigSemanticComparison struct {
	Equal     bool
	LeftHash  string
	RightHash string
}

// ResolveProjectContext applies -C, finds the active Git worktree, selects
// the nearest hardened config within it, and strictly compiles that config.
func ResolveProjectContext(cwd, changeDirectory string) (*ProjectContext, error) {
	fileContext, err := ResolveConfigFileContext(cwd, changeDirectory)
	if err != nil {
		return nil, err
	}
	authored, err := ParseAuthoredConfig(fileContext.OriginalBytes)
	if err != nil {
		return nil, err
	}
	aggregate, err := NormalizeAuthoredConfig(*authored, fileContext.CompilationContext())
	if err != nil {
		return nil, err
	}
	return &ProjectContext{
		EffectiveDirectory:                   fileContext.EffectiveDirectory,
		WorktreeRoot:                         fileContext.WorktreeRoot,
		ProjectRoot:                          fileContext.ProjectRoot,
		ConfigPath:                           fileContext.ConfigPath,
		TestsDir:                             fileContext.TestsDir,
		RepositoryRelativeProjectRoot:        fileContext.RepositoryRelativeProjectRoot,
		RepositoryRelativeExecutionDirectory: fileContext.RepositoryRelativeExecutionDirectory,
		Authored:                             authored,
		Aggregate:                            aggregate,
		OriginalBytes:                        fileContext.OriginalBytes,
	}, nil
}

// ResolveGitWorktreeRoot applies -C semantics and requires the resulting
// directory to belong to an active non-bare Git worktree. It is the mutation
// boundary used by init before a .revyl directory or backup can be created.
func ResolveGitWorktreeRoot(cwd, changeDirectory string) (effectiveDirectory, worktreeRoot string, err error) {
	effectiveDirectory, err = ResolveEffectiveDirectory(cwd, changeDirectory)
	if err != nil {
		return "", "", err
	}
	worktreeRoot, err = resolveGitWorktreeRoot(effectiveDirectory)
	if err != nil {
		return "", "", err
	}
	return effectiveDirectory, worktreeRoot, nil
}

// ResolveConfigFileContext applies -C, finds the active Git worktree, and
// reads the nearest hardened config without classifying its schema generation.
func ResolveConfigFileContext(cwd, changeDirectory string) (*ConfigFileContext, error) {
	effectiveDirectory, err := ResolveEffectiveDirectory(cwd, changeDirectory)
	if err != nil {
		return nil, err
	}
	worktreeRoot, err := resolveGitWorktreeRoot(effectiveDirectory)
	if err != nil {
		return nil, err
	}
	configPath, err := DiscoverConfigPath(effectiveDirectory, worktreeRoot)
	if err != nil {
		return nil, err
	}
	projectRoot := filepath.Dir(filepath.Dir(configPath))
	repositoryRelativeProjectRoot, err := repositoryRelativePath(worktreeRoot, projectRoot)
	if err != nil {
		return nil, err
	}
	repositoryRelativeExecutionDirectory, err := repositoryRelativePath(worktreeRoot, effectiveDirectory)
	if err != nil {
		return nil, err
	}
	originalBytes, err := ReadConfigFile(configPath)
	if err != nil {
		return nil, err
	}
	return &ConfigFileContext{
		EffectiveDirectory:                   effectiveDirectory,
		WorktreeRoot:                         worktreeRoot,
		ProjectRoot:                          projectRoot,
		ConfigPath:                           configPath,
		TestsDir:                             filepath.Join(projectRoot, ".revyl", "tests"),
		RepositoryRelativeProjectRoot:        repositoryRelativeProjectRoot,
		RepositoryRelativeExecutionDirectory: repositoryRelativeExecutionDirectory,
		OriginalBytes:                        originalBytes,
	}, nil
}

func resolveGitWorktreeRoot(effectiveDirectory string) (string, error) {
	lookupContext, cancel := context.WithTimeout(context.Background(), gitLookupTimeout)
	defer cancel()
	command := exec.CommandContext(lookupContext, "git", "-C", effectiveDirectory, "rev-parse", "--show-toplevel")
	output, err := command.Output()
	if err != nil {
		code := "git_worktree_unavailable"
		if errors.Is(lookupContext.Err(), context.DeadlineExceeded) {
			code = "git_worktree_lookup_timed_out"
		}
		return "", newConfigError("read", code, nil, "")
	}
	worktreeRoot := strings.TrimRight(string(output), "\r\n")
	if worktreeRoot == "" {
		return "", newConfigError("read", "git_worktree_unavailable", nil, "")
	}
	resolved, err := filepath.EvalSymlinks(worktreeRoot)
	if err != nil {
		return "", newConfigError("read", "git_worktree_unavailable", nil, "")
	}
	metadata, err := os.Stat(resolved)
	if err != nil || !metadata.IsDir() {
		return "", newConfigError("read", "git_worktree_unavailable", nil, "")
	}
	if _, err := repositoryRelativePath(resolved, effectiveDirectory); err != nil {
		return "", err
	}
	return resolved, nil
}

func repositoryRelativePath(worktreeRoot, candidate string) (string, error) {
	relative, err := filepath.Rel(worktreeRoot, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", newConfigError("read", "path_outside_git_worktree", nil, "")
	}
	if relative == "." {
		return ".", nil
	}
	return filepath.ToSlash(relative), nil
}

// MarshalCanonicalConfig renders a validated canonical authored config using
// stable struct field order, sorted mapping keys, and one trailing newline.
func MarshalCanonicalConfig(authored AuthoredConfig) ([]byte, error) {
	if err := authored.ValidateContract(); err != nil {
		return nil, err
	}
	initial, err := yaml.Marshal(authored)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical config: %w", err)
	}
	if len(initial) > MaxConfigBytes {
		return marshalCompactConfig(authored)
	}
	canonicalAuthored, err := ParseAuthoredConfig(initial)
	if err != nil {
		return nil, err
	}
	canonical, err := yaml.Marshal(canonicalAuthored)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical config: %w", err)
	}
	if len(canonical) > MaxConfigBytes {
		return marshalCompactConfig(authored)
	}
	return canonical, nil
}

func marshalCompactConfig(authored AuthoredConfig) ([]byte, error) {
	encoded, err := encodeCanonicalAuthoredConfig(authored)
	if err != nil {
		return nil, fmt.Errorf("marshal compact canonical config: %w", err)
	}
	if len(encoded) > MaxConfigBytes {
		return nil, newConfigError("read", "config_too_large", nil, "")
	}
	if _, err := ParseAuthoredConfig(encoded); err != nil {
		return nil, err
	}
	return encoded, nil
}

// CompareConfigSemantics compares normalized meaning instead of raw YAML
// formatting. A different project ID is always divergent even when both scoped
// aggregate hashes happen to match.
func CompareConfigSemantics(left, right []byte, compilationContext CompilationContext) (ConfigSemanticComparison, error) {
	leftAggregate, err := CompileConfigBytes(left, compilationContext)
	if err != nil {
		return ConfigSemanticComparison{}, err
	}
	rightAggregate, err := CompileConfigBytes(right, compilationContext)
	if err != nil {
		return ConfigSemanticComparison{}, err
	}
	return ConfigSemanticComparison{
		Equal:     leftAggregate.ProjectID == rightAggregate.ProjectID && leftAggregate.ProjectConfigurationHash == rightAggregate.ProjectConfigurationHash,
		LeftHash:  leftAggregate.ProjectConfigurationHash,
		RightHash: rightAggregate.ProjectConfigurationHash,
	}, nil
}

// CreateConfigBackup writes the current config's exact bytes beside it.
// Existing backups are never overwritten, and the source mode is preserved.
func CreateConfigBackup(configPath string) (string, error) {
	currentBytes, metadata, err := readConfigFileAndMetadata(configPath)
	if err != nil {
		return "", err
	}
	return writeConfigBackup(configPath, currentBytes, metadata.Mode().Perm())
}

func writeConfigBackup(configPath string, content []byte, mode os.FileMode) (string, error) {
	timestamp := configBackupNow().UTC().Format("20060102T150405Z")
	for suffix := 0; suffix < 10_000; suffix++ {
		backupPath := configPath + ".bak." + timestamp
		if suffix > 0 {
			backupPath = fmt.Sprintf("%s.bak.%s.%d", configPath, timestamp, suffix)
		}
		backup, createErr := os.OpenFile(backupPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
		if errors.Is(createErr, os.ErrExist) {
			continue
		}
		if createErr != nil {
			return "", newConfigError("write", "backup_create_failed", nil, "")
		}
		complete := false
		defer func() {
			if !complete {
				_ = os.Remove(backupPath)
			}
		}()
		if err := backup.Chmod(mode.Perm()); err != nil {
			_ = backup.Close()
			return "", newConfigError("write", "backup_write_failed", nil, "")
		}
		if _, err := backup.Write(content); err != nil {
			_ = backup.Close()
			return "", newConfigError("write", "backup_write_failed", nil, "")
		}
		if err := backup.Sync(); err != nil {
			_ = backup.Close()
			return "", newConfigError("write", "backup_write_failed", nil, "")
		}
		if err := backup.Close(); err != nil {
			return "", newConfigError("write", "backup_write_failed", nil, "")
		}
		complete = true
		return backupPath, nil
	}
	return "", newConfigError("write", "backup_name_unavailable", nil, "")
}

// BackupAndReplaceConfig performs one compare-and-swap local migration:
// the exact bytes the caller inspected are backed up, then replacement refuses
// to overwrite any intervening local edit.
func BackupAndReplaceConfig(configPath string, replacement, expectedCurrent []byte) (string, error) {
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
	backupPath, err := writeConfigBackup(configPath, expectedCurrent, metadata.Mode().Perm())
	if err != nil {
		return "", err
	}
	if err := ReplaceConfigAtomically(configPath, replacement, expectedCurrent); err != nil {
		return backupPath, err
	}
	return backupPath, nil
}

// CreateConfigIfAbsent publishes a complete validated config in one
// filesystem step and never overwrites an existing path.
func CreateConfigIfAbsent(configPath string, content []byte, mode os.FileMode) error {
	if _, err := ParseAuthoredConfig(content); err != nil {
		return err
	}
	directoryPath := filepath.Dir(configPath)
	if err := os.MkdirAll(directoryPath, 0o755); err != nil {
		return newConfigError("write", "config_directory_create_failed", nil, "")
	}
	directoryMetadata, err := os.Lstat(directoryPath)
	if err != nil || !directoryMetadata.IsDir() || directoryMetadata.Mode()&os.ModeSymlink != 0 {
		return newConfigError("write", "config_directory_invalid", nil, "")
	}
	if mode.Perm() == 0 {
		mode = 0o644
	}
	temporary, err := os.CreateTemp(directoryPath, "."+filepath.Base(configPath)+".create-*")
	if err != nil {
		return newConfigError("write", "temporary_file_create_failed", nil, "")
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode.Perm()); err != nil {
		_ = temporary.Close()
		return newConfigError("write", "config_write_failed", nil, "")
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return newConfigError("write", "config_write_failed", nil, "")
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return newConfigError("write", "config_write_failed", nil, "")
	}
	if err := temporary.Close(); err != nil {
		return newConfigError("write", "config_write_failed", nil, "")
	}
	if err := os.Link(temporaryPath, configPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return newConfigError("write", "config_already_exists", nil, "")
		}
		return newConfigError("write", "config_create_failed", nil, "")
	}
	return nil
}

// ReplaceConfigAtomically validates replacement bytes, verifies the local
// file still contains the bytes the caller inspected, writes to a same-directory
// temporary file, preserves the current mode, then atomically replaces the
// destination. A failed write leaves the original untouched.
func ReplaceConfigAtomically(configPath string, replacement, expectedCurrent []byte) error {
	if _, err := ParseAuthoredConfig(replacement); err != nil {
		return err
	}
	originalBytes, metadata, err := readConfigFileAndMetadata(configPath)
	if err != nil {
		return err
	}
	if !bytes.Equal(originalBytes, expectedCurrent) {
		return newConfigError("write", "config_changed_before_write", nil, "")
	}
	directoryPath := filepath.Dir(configPath)
	temporary, err := os.CreateTemp(directoryPath, "."+filepath.Base(configPath)+".replace-*")
	if err != nil {
		return newConfigError("write", "temporary_file_create_failed", nil, "")
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(metadata.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return newConfigError("write", "config_write_failed", nil, "")
	}
	if _, err := temporary.Write(replacement); err != nil {
		_ = temporary.Close()
		return newConfigError("write", "config_write_failed", nil, "")
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return newConfigError("write", "config_write_failed", nil, "")
	}
	if err := temporary.Close(); err != nil {
		return newConfigError("write", "config_write_failed", nil, "")
	}
	currentBytes, currentMetadata, err := readConfigFileAndMetadata(configPath)
	if err != nil {
		return err
	}
	if !os.SameFile(metadata, currentMetadata) || !bytes.Equal(currentBytes, originalBytes) {
		return newConfigError("write", "config_changed_during_write", nil, "")
	}
	if err := replaceConfigFile(temporaryPath, configPath); err != nil {
		return newConfigError("write", "config_replace_failed", nil, "")
	}
	return nil
}

func readConfigFileAndMetadata(configPath string) ([]byte, os.FileInfo, error) {
	metadata, err := os.Lstat(configPath)
	if err != nil || !metadata.Mode().IsRegular() || metadata.Mode()&os.ModeSymlink != 0 {
		return nil, nil, newConfigError("read", "config_not_regular_file", nil, "")
	}
	data, err := ReadConfigFile(configPath)
	if err != nil {
		return nil, nil, err
	}
	currentMetadata, err := os.Lstat(configPath)
	if err != nil || !os.SameFile(metadata, currentMetadata) {
		return nil, nil, newConfigError("read", "config_changed_during_read", nil, "")
	}
	return data, currentMetadata, nil
}
