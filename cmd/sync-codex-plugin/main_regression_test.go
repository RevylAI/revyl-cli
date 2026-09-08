package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDistributionAttributesSurvivePackageExport(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	attributes, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}
	exportedRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(exportedRoot, ".gitattributes"), attributes, 0600); err != nil {
		t.Fatal(err)
	}
	runGit := func(t *testing.T, input string, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "git", args...)
		command.Dir = exportedRoot
		command.Stdin = strings.NewReader(input)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return output
	}
	runGit(t, "", "init", "--quiet")
	content := "first line\nsecond line\n"
	objectID := strings.TrimSpace(string(runGit(t, content, "hash-object", "-w", "--stdin", "--no-filters")))
	unprotected := runGit(t, "", "-c", "core.autocrlf=true", "cat-file", "--filters", "--path=unprotected.txt", objectID)
	if string(unprotected) != strings.ReplaceAll(content, "\n", "\r\n") {
		t.Fatalf("unprotected checkout = %q, want CRLF conversion", unprotected)
	}
	for _, path := range []string{
		"cursor-plugin/install-local.sh",
		"cursor-plugin/hooks/ensure-revyl",
		"cursor-plugin/hooks/launch-revyl",
		"cursor-plugin/hooks/launch-revyl.ps1",
		"cursor-plugin/hooks/launch-revyl.cmd",
		"cursor-plugin/assets/icon.svg",
		"cursor-plugin/skills/revyl-cli-dev-loop/SKILL.md",
		"skills/revyl-cli-dev-loop/SKILL.md",
		"plugins/revyl/.codex-plugin/plugin.json",
		"plugins/revyl/runtime-version",
		"plugins/revyl/runtime-manifest.json",
		"plugins/revyl/scripts/launch-revyl",
		"plugins/revyl/scripts/launch-revyl.cmd",
		"plugins/revyl/scripts/launch-revyl.ps1",
		"plugins/revyl/scripts/launch-runtime",
		"plugins/revyl/scripts/launch-runtime.ps1",
		"plugins/revyl/assets/icon.svg",
		"plugins/revyl/references/revyl-cli-dev-loop.md",
		"plugins/revyl/skills/revyl-codex-dev-loop/SKILL.md",
		"plugins/revyl/skills/revyl-codex-proof-ci/SKILL.md",
	} {
		t.Run(path, func(t *testing.T) {
			checkedOut := runGit(t, "", "-c", "core.autocrlf=true", "cat-file", "--filters", "--path="+path, objectID)
			if string(checkedOut) != content {
				t.Fatalf("exported autocrlf checkout = %q, want %q", checkedOut, content)
			}
		})
	}
}

func TestSyncPluginChecksAndRestoresExecutableBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX executable bits")
	}
	for _, test := range []struct {
		name      string
		path      string
		driftMode os.FileMode
	}{
		{"missing launcher executable bits", "scripts/launch-runtime", 0644},
		{"partial launcher executable bits", "scripts/launch-runtime", 0744},
		{"unexpected manifest executable bits", "runtime-manifest.json", 0755},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := syncFixture(t)
			client := releaseClient(t, "")
			if err := syncPlugin(root, false, client); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "plugins", "revyl", filepath.FromSlash(test.path))
			original, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, test.driftMode); err != nil {
				t.Fatal(err)
			}
			if err := syncPlugin(root, true, client); err == nil || !strings.Contains(err.Error(), "executable bits are stale") {
				t.Fatalf("check error = %v, want executable-bit drift", err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != test.driftMode {
				t.Fatalf("check changed mode to %04o, want %04o", info.Mode().Perm(), test.driftMode)
			}
			if err := syncPlugin(root, false, client); err != nil {
				t.Fatal(err)
			}
			info, err = os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != original.Mode().Perm() {
				t.Fatalf("sync mode = %04o, want %04o", info.Mode().Perm(), original.Mode().Perm())
			}
			if err := syncPlugin(root, true, client); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSyncPluginCreatesPrivateDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX directory permissions")
	}
	root := syncFixture(t)
	if err := syncPlugin(root, false, releaseClient(t, "")); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"scripts", "assets", "references"} {
		info, err := os.Stat(filepath.Join(root, "plugins", "revyl", directory))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0700 {
			t.Errorf("%s mode = %04o, want 0700", directory, info.Mode().Perm())
		}
	}
}

func TestSyncPluginRejectsSymlinkEscapes(t *testing.T) {
	for _, operation := range []struct {
		name  string
		check bool
	}{{"check", true}, {"sync", false}} {
		for _, path := range []string{
			"plugins/revyl/.codex-plugin/plugin.json",
			"plugins/revyl/runtime-version",
			"cursor-plugin/hooks/launch-revyl",
			"cursor-plugin/hooks/launch-revyl.ps1",
			"cursor-plugin/hooks/launch-revyl.cmd",
			"cursor-plugin/assets/icon.svg",
			"skills/revyl-cli-dev-loop/SKILL.md",
			"plugins/revyl/runtime-manifest.json",
			"plugins/revyl/scripts/launch-runtime",
			"plugins/revyl/scripts",
		} {
			t.Run(operation.name+"/"+path, func(t *testing.T) {
				root := syncFixture(t)
				client := releaseClient(t, "")
				if err := syncPlugin(root, false, client); err != nil {
					t.Fatal(err)
				}
				linkedPath := filepath.Join(root, filepath.FromSlash(path))
				outsidePath := filepath.Join(t.TempDir(), "asset")
				if err := os.Rename(linkedPath, outsidePath); err != nil {
					t.Fatal(err)
				}
				relativeTarget, err := filepath.Rel(filepath.Dir(linkedPath), outsidePath)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(relativeTarget, linkedPath); err != nil {
					if runtime.GOOS == "windows" {
						t.Skipf("Windows symlink creation unavailable: %v", err)
					}
					t.Fatal(err)
				}
				if path == "plugins/revyl/scripts" {
					outsidePath = filepath.Join(outsidePath, "launch-runtime")
				}
				before, err := os.ReadFile(outsidePath)
				if err != nil {
					t.Fatal(err)
				}
				if err := syncPlugin(root, operation.check, client); err == nil {
					t.Fatal("accepted a symlink outside the package root")
				}
				after, err := os.ReadFile(outsidePath)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatal("modified a file outside the package root")
				}
			})
		}
	}
}
