package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/revyl/cli/internal/testutil"

	"github.com/revyl/cli/internal/privatefs"
)

func TestEnsurePrivateRuntimeDirectoryTightensExistingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := privatefs.CreateDirectory(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertPOSIXPermissions(t, info, 0o700)
}

func TestSaveDevContextRejectsDirectorySymlinks(t *testing.T) {
	for _, target := range []string{"sessions", "context"} {
		t.Run(target, func(t *testing.T) {
			repoRoot := t.TempDir()
			outside := t.TempDir()
			if err := os.Chmod(outside, 0o755); err != nil {
				t.Fatal(err)
			}
			path := devCtxDir(repoRoot, "default")
			if target == "sessions" {
				path = filepath.Dir(path)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, path); err != nil {
				t.Skipf("symlinks are unavailable: %v", err)
			}
			if err := saveDevContext(repoRoot, &DevContext{Name: "default"}); err == nil {
				t.Fatal("expected symlinked runtime directory to fail")
			}
			info, err := os.Stat(outside)
			if err != nil {
				t.Fatal(err)
			}
			testutil.AssertPOSIXPermissions(t, info, 0o755)
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatalf("outside directory changed: entries=%v error=%v", entries, err)
			}
		})
	}
}

func TestWritePrivateRuntimeFileCreatesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "screenshot.png")
	if err := writePrivateRuntimeFile(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertPOSIXPermissions(t, info, 0o600)
}

func TestWritePrivateRuntimeFileTightensExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "screenshot.png")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateRuntimeFile(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertPOSIXPermissions(t, info, 0o600)
}

func TestReadPrivateRuntimeFilePreservesLegacyMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.json")
	if err := os.WriteFile(path, []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	contents, err := readPrivateRuntimeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "legacy" {
		t.Fatalf("legacy contents = %q", contents)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertPOSIXPermissions(t, info, 0o644)
}

func TestWritePrivateRuntimeFileRejectsSymlink(t *testing.T) {
	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "runtime.txt")
	if err := os.Symlink(outsidePath, path); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	if err := writePrivateRuntimeFile(path, []byte("replacement")); err == nil {
		t.Fatal("expected symlink write to fail")
	}
	contents, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "outside" {
		t.Fatalf("outside contents = %q", contents)
	}
	info, err := os.Stat(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertPOSIXPermissions(t, info, 0o644)
}

func TestWritePrivateRuntimeFileRejectsReadOnlyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce POSIX write permission bits")
	}
	path := filepath.Join(t.TempDir(), "read-only.txt")
	if err := os.WriteFile(path, []byte("old"), 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	if err := writePrivateRuntimeFile(path, []byte("new")); err == nil {
		t.Fatal("expected read-only write to fail")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "old" {
		t.Fatalf("read-only contents = %q", contents)
	}
}

func testRuntimePath(path string) privateRuntimePath {
	return privateRuntimePath{filepath.Dir(path), filepath.Base(path)}
}
