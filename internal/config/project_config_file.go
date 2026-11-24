package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultGitLookupTimeout = 30 * time.Second
	maxGitLookupTimeout     = 10 * time.Minute
	gitLookupTimeoutEnvVar  = "REVYL_GIT_TIMEOUT"
)

// gitLookupTimeout reads positive seconds from REVYL_GIT_TIMEOUT up to
// maxGitLookupTimeout (using time.Duration conversion); fallback is defaultGitLookupTimeout.
func gitLookupTimeout() time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(os.Getenv(gitLookupTimeoutEnvVar)))
	if err != nil || seconds <= 0 || time.Duration(seconds)*time.Second > maxGitLookupTimeout {
		return defaultGitLookupTimeout
	}
	return time.Duration(seconds) * time.Second
}

var configBackupNow = time.Now

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

func (c ConfigFileContext) CompilationContext() CompilationContext {
	return CompilationContext{
		RepositoryRelativeProjectRoot: c.RepositoryRelativeProjectRoot,
		ExecutionDirectory:            c.RepositoryRelativeExecutionDirectory,
	}
}

type ConfigSemanticComparison struct {
	Equal     bool
	LeftHash  string
	RightHash string
}

func ResolveProjectContext(cwd, changeDirectory string) (*ProjectContext, error) {
	fileContext, err := ResolveConfigFileContext(cwd, changeDirectory)
	if err != nil {
		return nil, err
	}
	authored, err := ParseAuthoredConfig(fileContext.OriginalBytes)
	if err != nil {
		return nil, &os.PathError{Op: "validate", Path: fileContext.ConfigPath, Err: err}
	}
	aggregate, err := NormalizeAuthoredConfig(*authored, fileContext.CompilationContext())
	if err != nil {
		return nil, &os.PathError{Op: "validate", Path: fileContext.ConfigPath, Err: err}
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
		return nil, &os.PathError{Op: "read", Path: configPath, Err: err}
	}
	fileContext := &ConfigFileContext{
		EffectiveDirectory:                   effectiveDirectory,
		WorktreeRoot:                         worktreeRoot,
		ProjectRoot:                          projectRoot,
		ConfigPath:                           configPath,
		TestsDir:                             filepath.Join(projectRoot, ".revyl", "tests"),
		RepositoryRelativeProjectRoot:        repositoryRelativeProjectRoot,
		RepositoryRelativeExecutionDirectory: repositoryRelativeExecutionDirectory,
		OriginalBytes:                        originalBytes,
	}
	RecordConfigFileRead(worktreeRoot, configPath, originalBytes)
	return fileContext, nil
}

func resolveGitWorktreeRoot(effectiveDirectory string) (string, error) {
	timeout := gitLookupTimeout()
	lookupContext, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(lookupContext, "git", "-C", effectiveDirectory, "rev-parse", "--show-toplevel")
	command.WaitDelay = time.Second
	output, err := command.Output()
	if err != nil {
		if errors.Is(lookupContext.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf(
				"%w: git took longer than %s; rerun, or set %s to a longer number of seconds: %w",
				newConfigError("read", "git_worktree_lookup_timed_out", nil, ""), timeout, gitLookupTimeoutEnvVar, lookupContext.Err(),
			)
		}
		return "", fmt.Errorf("%w: %w", newConfigError("read", "git_worktree_unavailable", nil, ""), err)
	}
	worktreeRoot := strings.TrimRight(string(output), "\r\n")
	if worktreeRoot == "" {
		return "", newConfigError("read", "git_worktree_unavailable", nil, "")
	}
	resolved, err := filepath.EvalSymlinks(worktreeRoot)
	if err != nil {
		return "", fmt.Errorf("%w: %w", newConfigError("read", "git_worktree_unavailable", nil, ""), err)
	}
	metadata, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("%w: %w", newConfigError("read", "git_worktree_unavailable", nil, ""), err)
	}
	if !metadata.IsDir() {
		return "", newConfigError("read", "git_worktree_unavailable", nil, "")
	}
	if _, err := repositoryRelativePath(resolved, effectiveDirectory); err != nil {
		return "", err
	}
	return resolved, nil
}

func repositoryRelativePath(worktreeRoot, candidate string) (string, error) {
	relative, err := filepath.Rel(worktreeRoot, candidate)
	if err != nil {
		return "", fmt.Errorf("%w: %w", newConfigError("read", "path_outside_git_worktree", nil, ""), err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", newConfigError("read", "path_outside_git_worktree", nil, "")
	}
	if relative == "." {
		return ".", nil
	}
	return filepath.ToSlash(relative), nil
}

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
			return "", fmt.Errorf("%w: %w", newConfigError("write", "backup_create_failed", nil, ""), createErr)
		}
		complete := false
		defer func() {
			if !complete {
				_ = os.Remove(backupPath)
			}
		}()
		if err := backup.Chmod(mode.Perm()); err != nil {
			_ = backup.Close()
			return "", fmt.Errorf("%w: %w", newConfigError("write", "backup_write_failed", nil, ""), err)
		}
		if _, err := backup.Write(content); err != nil {
			_ = backup.Close()
			return "", fmt.Errorf("%w: %w", newConfigError("write", "backup_write_failed", nil, ""), err)
		}
		if err := backup.Sync(); err != nil {
			_ = backup.Close()
			return "", fmt.Errorf("%w: %w", newConfigError("write", "backup_write_failed", nil, ""), err)
		}
		if err := backup.Close(); err != nil {
			return "", fmt.Errorf("%w: %w", newConfigError("write", "backup_write_failed", nil, ""), err)
		}
		complete = true
		return backupPath, nil
	}
	return "", newConfigError("write", "backup_name_unavailable", nil, "")
}

// BackupAndReplaceConfig checks that the file still matches expectedCurrent,
// backs up those exact bytes, then stages and replaces the config file.
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

func CreateConfigIfAbsent(configPath string, content []byte, mode os.FileMode) error {
	if _, err := ParseAuthoredConfig(content); err != nil {
		return err
	}
	directoryPath := filepath.Dir(configPath)
	if err := os.MkdirAll(directoryPath, 0o755); err != nil {
		return fmt.Errorf("%w: %w", newConfigError("write", "config_directory_create_failed", nil, ""), err)
	}
	directoryMetadata, err := os.Lstat(directoryPath)
	if err != nil {
		return fmt.Errorf("%w: %w", newConfigError("write", "config_directory_invalid", nil, ""), err)
	}
	if !directoryMetadata.IsDir() || directoryMetadata.Mode()&os.ModeSymlink != 0 {
		return newConfigError("write", "config_directory_invalid", nil, "")
	}
	if mode.Perm() == 0 {
		mode = 0o644
	}
	temporary, err := os.CreateTemp(directoryPath, "."+filepath.Base(configPath)+".create-*")
	if err != nil {
		return fmt.Errorf("%w: %w", newConfigError("write", "temporary_file_create_failed", nil, ""), err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode.Perm()); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("%w: %w", newConfigError("write", "config_write_failed", nil, ""), err)
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("%w: %w", newConfigError("write", "config_write_failed", nil, ""), err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("%w: %w", newConfigError("write", "config_write_failed", nil, ""), err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("%w: %w", newConfigError("write", "config_write_failed", nil, ""), err)
	}
	if err := os.Link(temporaryPath, configPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: %w", newConfigError("write", "config_already_exists", nil, ""), err)
		}
		return fmt.Errorf("%w: %w", newConfigError("write", "config_create_failed", nil, ""), err)
	}
	return nil
}

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
		return fmt.Errorf("%w: %w", newConfigError("write", "temporary_file_create_failed", nil, ""), err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(metadata.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("%w: %w", newConfigError("write", "config_write_failed", nil, ""), err)
	}
	if _, err := temporary.Write(replacement); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("%w: %w", newConfigError("write", "config_write_failed", nil, ""), err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("%w: %w", newConfigError("write", "config_write_failed", nil, ""), err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("%w: %w", newConfigError("write", "config_write_failed", nil, ""), err)
	}
	currentBytes, currentMetadata, err := readConfigFileAndMetadata(configPath)
	if err != nil {
		return err
	}
	if !os.SameFile(metadata, currentMetadata) || !bytes.Equal(currentBytes, originalBytes) {
		return newConfigError("write", "config_changed_during_write", nil, "")
	}
	if err := replaceConfigFile(temporaryPath, configPath); err != nil {
		return fmt.Errorf("%w: %w", newConfigError("write", "config_replace_failed", nil, ""), err)
	}
	return nil
}

func readConfigFileAndMetadata(configPath string) ([]byte, os.FileInfo, error) {
	metadata, err := os.Lstat(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", newConfigError("read", "config_not_regular_file", nil, ""), err)
	}
	if !metadata.Mode().IsRegular() || metadata.Mode()&os.ModeSymlink != 0 {
		return nil, nil, newConfigError("read", "config_not_regular_file", nil, "")
	}
	data, err := ReadConfigFile(configPath)
	if err != nil {
		return nil, nil, err
	}
	currentMetadata, err := os.Lstat(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", newConfigError("read", "config_changed_during_read", nil, ""), err)
	}
	if !os.SameFile(metadata, currentMetadata) {
		return nil, nil, newConfigError("read", "config_changed_during_read", nil, "")
	}
	return data, currentMetadata, nil
}
