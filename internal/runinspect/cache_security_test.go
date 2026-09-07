package runinspect

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/revyl/cli/internal/testutil"
)

func TestRunCacheRejectsNonComponentTaskIDsAndFilenames(t *testing.T) {
	cacheDir := t.TempDir()
	invalidComponents := []string{"", ".", "..", "../escape", "nested/value", `nested\value`}
	for _, invalid := range invalidComponents {
		writeCachedBytes(cacheDir, invalid, "artifact.json", []byte("secret"))
		writeCachedBytes(cacheDir, "task-id", invalid, []byte("secret"))
		if _, ok := readCachedBytes(cacheDir, invalid, "artifact.json"); ok {
			t.Fatalf("readCachedBytes accepted task ID %q", invalid)
		}
		if _, ok := readCachedBytes(cacheDir, "task-id", invalid); ok {
			t.Fatalf("readCachedBytes accepted filename %q", invalid)
		}
		writeCachedJSONL(cacheDir, invalid, []byte("secret"))
		if _, ok := readCachedJSONL(cacheDir, invalid); ok {
			t.Fatalf("JSONL cache accepted task ID %q", invalid)
		}
	}

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("invalid cache keys wrote files: %v", entries)
	}
}

func TestRunCacheReadPreservesExistingPermissions(t *testing.T) {
	cacheDir := t.TempDir()
	path := filepath.Join(cacheDir, "task-id", "artifact.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("cached"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	contents, ok := readCachedBytes(cacheDir, "task-id", "artifact.json")
	if !ok || string(contents) != "cached" {
		t.Fatalf("expected cache hit, got contents=%q ok=%v", contents, ok)
	}
	directoryInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertPOSIXPermissions(t, directoryInfo, 0o755)
	testutil.AssertPOSIXPermissions(t, fileInfo, 0o644)
}

func TestRunCacheWriteTightensExistingPermissions(t *testing.T) {
	cacheDir := t.TempDir()
	path := filepath.Join(cacheDir, "task-id", "artifact.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("longer legacy contents"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	writeCachedBytes(cacheDir, "task-id", "artifact.json", []byte("private"))

	for _, directory := range []string{cacheDir, filepath.Dir(path)} {
		info, err := os.Stat(directory)
		if err != nil {
			t.Fatal(err)
		}
		testutil.AssertPOSIXPermissions(t, info, 0o700)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertPOSIXPermissions(t, info, 0o600)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "private" {
		t.Fatalf("cache contents = %q", contents)
	}
}

func TestRunCacheWriteRejectsDirectoryAndFileSymlinks(t *testing.T) {
	for _, target := range []string{"cache", "task", "file"} {
		t.Run(target, func(t *testing.T) {
			cacheDir := filepath.Join(t.TempDir(), "cache")
			outside := t.TempDir()
			if err := os.Chmod(outside, 0o755); err != nil {
				t.Fatal(err)
			}
			outsidePath := filepath.Join(outside, "artifact.json")
			if err := os.WriteFile(outsidePath, []byte("sentinel"), 0o644); err != nil {
				t.Fatal(err)
			}
			link, destination := cacheDir, outside
			if target == "task" {
				link = filepath.Join(cacheDir, "task-id")
			} else if target == "file" {
				link = filepath.Join(cacheDir, "task-id", "artifact.json")
				destination = outsidePath
			}
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(destination, link); err != nil {
				t.Skipf("symlinks are unavailable: %v", err)
			}
			writeCachedBytes(cacheDir, "task-id", "artifact.json", []byte("replacement"))
			contents, err := os.ReadFile(outsidePath)
			if err != nil || string(contents) != "sentinel" {
				t.Fatalf("outside contents changed: contents=%q error=%v", contents, err)
			}
			for path, mode := range map[string]os.FileMode{outside: 0o755, outsidePath: 0o644} {
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				testutil.AssertPOSIXPermissions(t, info, mode)
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 1 {
				t.Fatalf("outside directory changed: entries=%v error=%v", entries, err)
			}
		})
	}
}

func TestRunCacheDoesNotFollowTaskDirectorySymlinkOutsideRoot(t *testing.T) {
	cacheDir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "artifact.json")
	if err := os.WriteFile(outsidePath, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(cacheDir, "task-id")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	writeCachedBytes(cacheDir, "task-id", "artifact.json", []byte("replacement"))
	if _, ok := readCachedBytes(cacheDir, "task-id", "artifact.json"); ok {
		t.Fatal("cache read followed task directory symlink outside cache root")
	}
	contents, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "sentinel" {
		t.Fatalf("outside sentinel changed to %q", contents)
	}
}

func TestDefaultRunCacheRejectsManagedDirectoryRedirects(t *testing.T) {
	for _, component := range []string{".revyl", ".revyl/run-cache", ".revyl/run-cache/task-id"} {
		for _, internal := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/internal=%v", component, internal), func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("USERPROFILE", home)
				cacheDir := DefaultCacheDir()
				writeCachedBytes(cacheDir, "task-id", "artifact.json", []byte("sentinel"))
				link := filepath.Join(home, component)
				destination := filepath.Join(t.TempDir(), "redirected")
				if internal {
					destination = filepath.Join(home, "redirected")
				}
				if err := os.Rename(link, destination); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(destination, 0o755); err != nil {
					t.Fatal(err)
				}
				target := destination
				if internal {
					var err error
					target, err = filepath.Rel(filepath.Dir(link), destination)
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(target, link); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				writeCachedBytes(cacheDir, "task-id", "artifact.json", []byte("replacement"))
				if _, ok := readCachedBytes(cacheDir, "task-id", "artifact.json"); ok {
					t.Fatal("cache read followed directory redirect")
				}
				suffix, err := filepath.Rel(link, filepath.Join(cacheDir, "task-id", "artifact.json"))
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(filepath.Join(destination, suffix))
				if err != nil || string(data) != "sentinel" {
					t.Fatalf("redirected cache changed: %q, %v", data, err)
				}
				info, err := os.Stat(destination)
				if err != nil {
					t.Fatal(err)
				}
				testutil.AssertPOSIXPermissions(t, info, 0o755)
			})
		}
	}
}
