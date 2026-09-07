package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/revyl/cli/internal/build"
)

type runtimeTreeEntry struct {
	Mode     fs.FileMode
	Contents string
}

func snapshotRuntimeTree(t *testing.T, root string) map[string]runtimeTreeEntry {
	t.Helper()
	entries := make(map[string]runtimeTreeEntry)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		contents := ""
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			contents = string(data)
		}
		entries[relativePath] = runtimeTreeEntry{info.Mode(), contents}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestManagedRuntimeRejectsDirectoryRedirects(t *testing.T) {
	for _, component := range []string{".revyl", ".revyl/dev-sessions", ".revyl/dev-sessions/default"} {
		for _, internal := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/internal=%v", component, internal), func(t *testing.T) {
				repoRoot := t.TempDir()
				if err := saveDevContext(repoRoot, &DevContext{Name: "default", PID: 12345}); err != nil {
					t.Fatal(err)
				}
				status := devCtxStatusPath(repoRoot, "default")
				pid := devCtxPIDPath(repoRoot, "default")
				manifest := devCtxManifestPath(repoRoot, "default")
				log := devDetachLogPath(repoRoot)
				if err := writeDevStatusFile(status, []byte(`{"state":"idle"}`)); err != nil {
					t.Fatal(err)
				}
				if err := writeDevCtxPIDFile(pid, 12345, 1); err != nil {
					t.Fatal(err)
				}
				if err := build.SaveManifest(&build.AppManifest{}, manifest.trustedRoot, manifest.relativePath); err != nil {
					t.Fatal(err)
				}
				if err := setCurrentDevContext(repoRoot, "default"); err != nil {
					t.Fatal(err)
				}
				if err := writeManagedRuntimeFile(log, []byte("existing log")); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(repoRoot, component)
				destination := filepath.Join(t.TempDir(), "redirected")
				if internal {
					destination = filepath.Join(repoRoot, "redirected")
				}
				if err := os.Rename(link, destination); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(destination, 0o755); err != nil {
					t.Fatal(err)
				}
				before := snapshotRuntimeTree(t, destination)
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
				writes := map[string]func() error{
					"context": func() error { return saveDevContext(repoRoot, &DevContext{Name: "default"}) },
					"status":  func() error { return writeDevStatusFile(status, []byte("replacement")) },
					"pid":     func() error { return writeDevCtxPIDFile(pid, 99999, 2) },
					"manifest": func() error {
						return build.SaveManifest(&build.AppManifest{}, manifest.trustedRoot, manifest.relativePath)
					},
				}
				if component != ".revyl/dev-sessions/default" {
					writes["marker"] = func() error { return setCurrentDevContext(repoRoot, "changed") }
					writes["log"] = func() error {
						file, err := openOrCreateManagedRuntimeFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND)
						if file != nil {
							_ = file.Close()
						}
						return err
					}
					if _, err := readCurrentDevContext(repoRoot); err == nil {
						t.Fatal("marker read followed directory redirect")
					}
					if _, err := readManagedRuntimeFile(log); err == nil {
						t.Fatal("log read followed directory redirect")
					}
					if _, err := listDevContexts(repoRoot); err == nil {
						t.Fatal("context listing followed directory redirect")
					}
				}
				for name, write := range writes {
					if err := write(); err == nil {
						t.Errorf("%s write followed directory redirect", name)
					}
				}
				if _, err := loadDevContext(repoRoot, "default"); err == nil {
					t.Error("context read followed directory redirect")
				}
				if _, err := readDevStatusFile(status); err == nil {
					t.Error("status read followed directory redirect")
				}
				if actualPID, _ := readDevCtxPIDFile(pid); actualPID != 0 {
					t.Error("PID read followed directory redirect")
				}
				if _, err := build.LoadManifest(manifest.trustedRoot, manifest.relativePath); err == nil {
					t.Error("manifest read followed directory redirect")
				}
				forceCleanupDevContext(repoRoot, "default")
				after := snapshotRuntimeTree(t, destination)
				if !reflect.DeepEqual(before, after) {
					t.Fatalf("redirected contents or permissions changed: before=%v after=%v", before, after)
				}
			})
		}
	}
}

func TestStatusReplacementRemainsAnchoredAfterDirectorySwap(t *testing.T) {
	repoRoot := t.TempDir()
	status := devCtxStatusPath(repoRoot, "default")
	if err := writeDevStatusFile(status, []byte("old")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "sentinel"), []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotRuntimeTree(t, outside)
	originalRename := renameDevStatusFile
	calls := 0
	directorySwapBlocked := false
	runtimeDirectory := filepath.Join(repoRoot, ".revyl")
	retainedDirectory := filepath.Join(repoRoot, "original")
	renameDevStatusFile = func(root *os.Root, oldName, newName string) error {
		calls++
		if calls == 1 {
			if err := os.Rename(runtimeDirectory, retainedDirectory); err != nil {
				if runtime.GOOS != "windows" || !errors.Is(err, os.ErrPermission) {
					t.Fatal(err)
				}
				directorySwapBlocked = true
			} else if err := os.Symlink(outside, runtimeDirectory); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			return errors.New("exercise replacement fallback")
		}
		return originalRename(root, oldName, newName)
	}
	t.Cleanup(func() { renameDevStatusFile = originalRename })
	if err := writeDevStatusFile(status, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("rename calls = %d, want initial replacement, backup, and retry", calls)
	}
	originalContext := filepath.Join(retainedDirectory, devContextsDir, "default")
	if directorySwapBlocked {
		originalContext = devCtxDir(repoRoot, "default")
	}
	data, err := os.ReadFile(filepath.Join(originalContext, devContextStatusFile))
	if err != nil || string(data) != "new" {
		t.Fatalf("write lost directory anchor: %q, %v", data, err)
	}
	entries, err := os.ReadDir(originalContext)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary or backup files remain: %v, %v", entries, err)
	}
	if after := snapshotRuntimeTree(t, outside); !reflect.DeepEqual(before, after) {
		t.Fatalf("redirected directory changed: %v", after)
	}
	if directorySwapBlocked {
		if err := os.Rename(runtimeDirectory, retainedDirectory); err != nil {
			t.Fatalf("runtime directory remains locked after the write: %v", err)
		}
		if err := os.Symlink(outside, runtimeDirectory); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	if _, err := readDevStatusFile(status); err == nil {
		t.Fatal("subsequent read followed replacement symlink")
	}
}

func TestRuntimeRepositoryAlias(t *testing.T) {
	repoRoot := t.TempDir()
	alias := filepath.Join(t.TempDir(), "repository")
	if err := os.Symlink(repoRoot, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := saveDevContext(alias, &DevContext{Name: "default"}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDevContext(alias, "default"); err != nil {
		t.Fatal(err)
	}
	status := devCtxStatusPath(alias, "default")
	if err := writeDevStatusFile(status, []byte("status")); err != nil {
		t.Fatal(err)
	}
	data, err := readDevStatusFile(status)
	if err != nil || string(data) != "status" {
		t.Fatalf("alias read failed: %q, %v", data, err)
	}
}
