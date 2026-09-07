package privatefs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/testutil"
)

func TestCreateDirectory(t *testing.T) {
	for _, rooted := range []bool{false, true} {
		name := "path"
		if rooted {
			name = "rooted"
		}
		t.Run(name, func(t *testing.T) {
			for _, state := range []string{"new", "existing", "symlink", "file"} {
				t.Run(state, func(t *testing.T) {
					parentPath := t.TempDir()
					parent, err := os.OpenRoot(parentPath)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = parent.Close() })
					path := filepath.Join(parentPath, "private")
					outside := t.TempDir()
					if err := os.Chmod(outside, 0o755); err != nil {
						t.Fatal(err)
					}
					switch state {
					case "existing":
						if err := os.Mkdir(path, 0o755); err != nil {
							t.Fatal(err)
						}
					case "symlink":
						if err := os.Symlink(outside, path); err != nil {
							t.Skipf("symlinks are unavailable: %v", err)
						}
					case "file":
						if err := os.WriteFile(path, []byte("sentinel"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					var root *os.Root
					if rooted {
						root, err = CreateDirectoryInRoot(parent, "private"+string(filepath.Separator))
					} else {
						root, err = CreateDirectory(parentPath, "private"+string(filepath.Separator))
					}
					if root != nil {
						t.Cleanup(func() { _ = root.Close() })
					}
					if state == "file" || state == "symlink" {
						if err == nil || root != nil {
							t.Fatalf("unsafe directory accepted: root=%v error=%v", root, err)
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						info, err := os.Stat(path)
						if err != nil {
							t.Fatal(err)
						}
						testutil.AssertPOSIXPermissions(t, info, 0o700)
						if err := root.WriteFile("proof", []byte("confined"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					info, err := os.Stat(outside)
					if err != nil {
						t.Fatal(err)
					}
					testutil.AssertPOSIXPermissions(t, info, 0o755)
				})
			}
		})
	}
}

func TestTightenDirectoryRejectsReplacedDirectory(t *testing.T) {
	expectedPath := t.TempDir()
	expected, err := os.Lstat(expectedPath)
	if err != nil {
		t.Fatal(err)
	}
	replacementPath := t.TempDir()
	if err := os.Chmod(replacementPath, 0o755); err != nil {
		t.Fatal(err)
	}
	replacementRoot, err := os.OpenRoot(replacementPath)
	if err != nil {
		t.Fatal(err)
	}
	root, err := tightenDirectory(replacementRoot, expected)
	if root != nil {
		_ = root.Close()
	}
	if err == nil || root != nil {
		t.Fatalf("replacement accepted: root=%v error=%v", root, err)
	}
	info, err := os.Stat(replacementPath)
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertPOSIXPermissions(t, info, 0o755)
	if _, err := replacementRoot.Stat("."); err == nil {
		t.Fatal("rejected directory handle was not closed")
	}
}

func TestCreateDirectoryInRootRejectsEscapingParent(t *testing.T) {
	parentPath := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(parentPath, "redirect")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close() }()
	root, err := CreateDirectoryInRoot(parent, "redirect/private")
	if root != nil {
		_ = root.Close()
	}
	if err == nil {
		t.Fatal("expected escaping parent to fail")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("outside directory changed: entries=%v error=%v", entries, err)
	}
}

func TestManagedDirectoryRejectsEverySymlinkComponent(t *testing.T) {
	for _, relativePath := range []string{".revyl", ".revyl/dev-sessions", ".revyl/dev-sessions/default"} {
		for _, internal := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/internal=%v", relativePath, internal), func(t *testing.T) {
				trustedRoot := t.TempDir()
				destination := t.TempDir()
				if internal {
					destination = filepath.Join(trustedRoot, "redirect-target")
					if err := os.Mkdir(destination, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Chmod(destination, 0o755); err != nil {
					t.Fatal(err)
				}
				sentinel := filepath.Join(destination, "sentinel")
				if err := os.WriteFile(sentinel, []byte("unchanged"), 0o644); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(trustedRoot, relativePath)
				if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
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
				for _, open := range []func(string, string) (*os.Root, error){CreateDirectory, OpenDirectory} {
					root, err := open(trustedRoot, ".revyl/dev-sessions/default")
					if root != nil {
						_ = root.Close()
					}
					if err == nil || !strings.Contains(err.Error(), filepath.Base(relativePath)) {
						t.Fatalf("expected actionable component rejection, got %v", err)
					}
				}
				entries, err := os.ReadDir(destination)
				if err != nil || len(entries) != 1 {
					t.Fatalf("redirected directory changed: %v, %v", entries, err)
				}
				contents, err := os.ReadFile(sentinel)
				if err != nil || string(contents) != "unchanged" {
					t.Fatalf("redirected file changed: %q, %v", contents, err)
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

func TestOpenVerifiedDirectoryRejectsReplacementBeforeDescent(t *testing.T) {
	trustedRoot := t.TempDir()
	parent, err := os.OpenRoot(trustedRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close() }()
	if err := parent.Mkdir("runtime", 0o755); err != nil {
		t.Fatal(err)
	}
	expected, err := parent.Lstat("runtime")
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Rename("runtime", "original"); err != nil {
		t.Fatal(err)
	}
	if err := parent.Mkdir("runtime", 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := openVerifiedDirectory(parent, "runtime", expected)
	if root != nil {
		_ = root.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "changed while opening") {
		t.Fatalf("replacement accepted: %v", err)
	}
	info, err := parent.Stat("runtime")
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertPOSIXPermissions(t, info, 0o755)
	entries, err := os.ReadDir(filepath.Join(trustedRoot, "runtime"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("replacement modified: %v, %v", entries, err)
	}
}

func TestManagedDirectoryAllowsTrustedRootAliasAndPreservesParents(t *testing.T) {
	trustedRoot := t.TempDir()
	for _, dir := range []string{trustedRoot, filepath.Join(trustedRoot, ".revyl")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(t.TempDir(), "repository")
	if err := os.Symlink(trustedRoot, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	root, err := CreateDirectory(alias, ".revyl/dev-sessions/default")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := root.WriteFile("proof", []byte("confined"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{trustedRoot, filepath.Join(trustedRoot, ".revyl")} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		testutil.AssertPOSIXPermissions(t, info, 0o755)
	}
}
