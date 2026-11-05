package config

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
)

type configFileRead struct {
	worktreeRoot string
	configPath   string
	configBytes  []byte
}

var lastReadConfigFile struct {
	sync.Mutex
	read *configFileRead
}

// RecordConfigFileRead notes the project config this process read most
// recently, so FindConfigPathWithUncommittedMigration can check it before exit.
func RecordConfigFileRead(worktreeRoot, configPath string, configBytes []byte) {
	lastReadConfigFile.Lock()
	defer lastReadConfigFile.Unlock()
	lastReadConfigFile.read = &configFileRead{worktreeRoot: worktreeRoot, configPath: configPath, configBytes: configBytes}
}

// FindConfigPathWithUncommittedMigration reports the repository-relative path
// of the project config this process read most recently when that file is
// canonical in the working tree but its copy at git HEAD would still fail with
// the legacy-format error. It reads one blob from the local repository and
// reports nothing when the file is absent at HEAD.
func FindConfigPathWithUncommittedMigration() (string, bool) {
	lastReadConfigFile.Lock()
	read := lastReadConfigFile.read
	lastReadConfigFile.Unlock()
	if read == nil {
		return "", false
	}
	if _, err := ParseAuthoredConfig(read.configBytes); err != nil {
		return "", false
	}
	repositoryRelativeConfigPath, err := repositoryRelativePath(read.worktreeRoot, read.configPath)
	if err != nil {
		return "", false
	}
	lookupContext, cancel := context.WithTimeout(context.Background(), gitLookupTimeout)
	defer cancel()
	// #nosec G204 -- Git receives a fixed command and a repository-relative path as argv, without a shell.
	committedBytes, err := exec.CommandContext(lookupContext, "git", "-C", read.worktreeRoot, "cat-file", "blob", "HEAD:"+repositoryRelativeConfigPath).Output()
	if err != nil || len(committedBytes) > MaxConfigBytes {
		return "", false
	}
	_, err = ParseAuthoredConfig(committedBytes)
	var configError *ConfigError
	if !errors.As(err, &configError) || (configError.Code != "legacy_config_requires_migration" && configError.Code != "mixed_config_formats") {
		return "", false
	}
	return repositoryRelativeConfigPath, true
}

// FindPathsChangedSinceHEAD reports which repository-relative paths are
// untracked, staged, or modified relative to git HEAD. When git cannot answer,
// it reports every path, so a commit list built from it never omits one.
func FindPathsChangedSinceHEAD(worktreeRoot string, repositoryRelativePaths []string) []string {
	if len(repositoryRelativePaths) == 0 {
		return nil
	}
	lookupContext, cancel := context.WithTimeout(context.Background(), gitLookupTimeout)
	defer cancel()
	arguments := append([]string{"--literal-pathspecs", "-C", worktreeRoot, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--"}, repositoryRelativePaths...)
	// #nosec G204 -- Git receives a fixed flag set and literal repository-relative paths after --, without a shell.
	output, err := exec.CommandContext(lookupContext, "git", arguments...).Output()
	if err != nil {
		return repositoryRelativePaths
	}
	changed := map[string]bool{}
	entries := strings.Split(string(output), "\x00")
	for index := 0; index < len(entries); index++ {
		entry := entries[index]
		if len(entry) < 4 {
			continue
		}
		changed[entry[3:]] = true
		if entry[0] == 'R' || entry[0] == 'C' {
			index++
		}
	}
	changedPaths := make([]string, 0, len(repositoryRelativePaths))
	for _, repositoryRelativePath := range repositoryRelativePaths {
		if changed[repositoryRelativePath] {
			changedPaths = append(changedPaths, repositoryRelativePath)
		}
	}
	return changedPaths
}
