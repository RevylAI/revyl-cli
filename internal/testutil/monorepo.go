package testutil

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// ReadMonorepoFile returns a file of the CogniSim monorepo that holds this
// module, named by its repository-root-relative path. The test is skipped only
// in the exported CLI repository; inside the monorepo a missing file fails the
// test, so a checkout that leaves the file out cannot pass.
func ReadMonorepoFile(t *testing.T, path string) string {
	t.Helper()
	moduleRoot, err := findModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	if isExportedCLI(moduleRoot) {
		t.Skipf("%s is outside the exported CLI repository", path)
	}
	content, err := os.ReadFile(filepath.Join(filepath.Dir(moduleRoot), filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read %s from the monorepo (a CI job that runs this test must check it out): %v", path, err)
	}
	return string(content)
}

// isExportedCLI reports whether the module is the published CLI repository
// rather than the monorepo's revyl-cli directory: the module is its own
// repository root, or neither a repository nor anything of the monorepo sits
// beside it.
func isExportedCLI(moduleRoot string) bool {
	if exists(filepath.Join(moduleRoot, ".git")) {
		return true
	}
	repoRoot := filepath.Dir(moduleRoot)
	for _, monorepoPath := range []string{".git", ".github/workflows/ci.yml", "frontend", "gtm"} {
		if exists(filepath.Join(repoRoot, filepath.FromSlash(monorepoPath))) {
			return false
		}
	}
	return true
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if exists(filepath.Join(dir, "go.mod")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod above the test's working directory")
		}
		dir = parent
	}
}
