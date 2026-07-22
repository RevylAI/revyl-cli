package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const nestedProjectSearchDepth = 4

var skippedProjectDirectories = map[string]bool{
	".git":         true,
	".venv":        true,
	"DerivedData":  true,
	"Pods":         true,
	"build":        true,
	"dist":         true,
	"node_modules": true,
	"tmp":          true,
	"vendor":       true,
}

// MissingProjectRootError identifies the directory where no initialized Revyl project was found.
type MissingProjectRootError struct {
	WorkingDirectory string
}

// Error returns an actionable initialization message.
func (e *MissingProjectRootError) Error() string {
	return fmt.Sprintf(
		"no initialized Revyl project found from %q; run \"revyl init --non-interactive\" in that directory",
		e.WorkingDirectory,
	)
}

// AmbiguousProjectRootsError lists nested Revyl projects requiring caller selection.
type AmbiguousProjectRootsError struct {
	WorkingDirectory string
	Roots            []string
}

// Error returns a deterministic project-selection message.
func (e *AmbiguousProjectRootsError) Error() string {
	return fmt.Sprintf(
		"multiple initialized Revyl projects found under %q: %s; retry with project_dir set to one candidate root",
		e.WorkingDirectory,
		strings.Join(e.Roots, ", "),
	)
}

// FindRepoRoot walks up from the given directory looking for a .revyl/ directory.
func FindRepoRoot(dir string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	current := absDir
	for {
		revylDir := filepath.Join(current, ".revyl")
		if info, err := os.Stat(revylDir); err == nil && info.IsDir() {
			return current, nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", &MissingProjectRootError{WorkingDirectory: absDir}
		}
		current = parent
	}
}

// FindProjectRoot resolves the nearest project or one unambiguous nested project.
func FindProjectRoot(dir string) (string, error) {
	if root, err := FindRepoRoot(dir); err == nil {
		if info, configErr := os.Stat(filepath.Join(root, ".revyl", "config.yaml")); configErr == nil && !info.IsDir() {
			return root, nil
		}
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path: %w", err)
	}
	var roots []string
	walkErr := filepath.WalkDir(absDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return filepath.SkipDir
		}
		if !entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(absDir, path)
		if err != nil {
			return err
		}
		if entry.Name() == ".revyl" {
			if _, err := os.Stat(filepath.Join(path, "config.yaml")); err == nil {
				roots = append(roots, filepath.Dir(path))
			}
			return filepath.SkipDir
		}
		if relative != "." {
			depth := len(strings.Split(relative, string(filepath.Separator)))
			if skippedProjectDirectories[entry.Name()] || depth > nestedProjectSearchDepth {
				return filepath.SkipDir
			}
		}
		return nil
	})
	if walkErr != nil {
		return "", fmt.Errorf("search nested Revyl projects: %w", walkErr)
	}
	sort.Strings(roots)
	switch len(roots) {
	case 0:
		return "", &MissingProjectRootError{WorkingDirectory: absDir}
	case 1:
		return roots[0], nil
	default:
		return "", &AmbiguousProjectRootsError{WorkingDirectory: absDir, Roots: roots}
	}
}
