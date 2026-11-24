package sessionprofile

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/revyl/cli/internal/checkoutdefinitions"
)

func resolveRepositoryPath(projectRoot, ref string) (string, error) {
	realScript, err := realPathInRepository(projectRoot, filepath.Join(projectRoot, filepath.FromSlash(ref)), ref)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(realScript)
	if err != nil {
		return "", fmt.Errorf("script %s could not be read", ref)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("script %s is not a regular file", ref)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("script %s is not executable (chmod +x it)", ref)
	}
	return realScript, nil
}

// realPathInRepository resolves scriptPath's symlinks, refusing a script that
// resolves outside the repository holding projectRoot.
func realPathInRepository(projectRoot, scriptPath, ref string) (string, error) {
	realProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return "", fmt.Errorf("could not resolve the project root for script %s", ref)
	}
	repositoryRoot := repositoryRootOf(realProjectRoot)
	realScript, err := filepath.EvalSymlinks(scriptPath)
	if err != nil {
		return "", fmt.Errorf("script %s was not found in the repository", ref)
	}
	if realScript != repositoryRoot && !strings.HasPrefix(realScript, repositoryRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("script %s resolves outside the repository", ref)
	}
	return realScript, nil
}

func repositoryRootOf(projectRoot string) string {
	for current := projectRoot; ; {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return projectRoot
		}
		current = parent
	}
}

// Named scripts must be regular files in .revyl/scripts; unlike repository
// path scripts, the lookup does not accept a symlink as the file itself.
func findRepositoryScript(projectRoot, scriptsDir, name string) (string, bool, error) {
	var matches []string
	for _, extension := range slices.Sorted(maps.Keys(checkoutdefinitions.ScriptRuntimeByFileExtension)) {
		path := filepath.Join(scriptsDir, name+extension)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return "", false, fmt.Errorf(".revyl/scripts/%s%s is not a readable regular file", name, extension)
		}
		matches = append(matches, path)
	}
	if len(matches) > 1 {
		return "", false, fmt.Errorf(".revyl/scripts holds more than one file named %s; keep one", name)
	}
	if len(matches) == 0 {
		return "", false, nil
	}
	if _, err := realPathInRepository(projectRoot, matches[0], ".revyl/scripts/"+filepath.Base(matches[0])); err != nil {
		return "", false, err
	}
	return matches[0], true, nil
}
