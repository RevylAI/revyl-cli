package config

import (
	"fmt"
	"os"
	"path/filepath"
)

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
			return "", fmt.Errorf("no .revyl/ directory found (searched from %s to /)", absDir)
		}
		current = parent
	}
}
