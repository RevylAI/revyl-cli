package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFindRepoRootPrefersNearestRevylDir(t *testing.T) {
	root := copyConfigFixture(t, filepath.Join(repoRootForConfigFixtureTests(t), "internal-apps", "expo-monorepo-hoisted"))

	appDir := filepath.Join(root, "apps", "mobile")
	appRoot, err := FindRepoRoot(appDir)
	if err != nil {
		t.Fatalf("FindRepoRoot(appDir) error = %v", err)
	}
	if appRoot != appDir {
		t.Fatalf("FindRepoRoot(appDir) = %q, want app dir %q", appRoot, appDir)
	}

	sharedDir := filepath.Join(root, "packages", "shared")
	sharedRoot, err := FindRepoRoot(sharedDir)
	if err != nil {
		t.Fatalf("FindRepoRoot(sharedDir) error = %v", err)
	}
	if sharedRoot != root {
		t.Fatalf("FindRepoRoot(sharedDir) = %q, want workspace root %q", sharedRoot, root)
	}
}

func repoRootForConfigFixtureTests(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
}

func copyConfigFixture(t *testing.T, src string) string {
	t.Helper()

	if _, err := os.Stat(src); os.IsNotExist(err) {
		t.Skipf("fixture directory %s not found (running outside monorepo?)", src)
	}

	dst := t.TempDir()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		info, err := os.Stat(path)
		if err != nil {
			return err
		}

		return os.WriteFile(target, data, info.Mode())
	})
	if err != nil {
		t.Fatalf("copyConfigFixture(%s) error = %v", src, err)
	}

	return dst
}
