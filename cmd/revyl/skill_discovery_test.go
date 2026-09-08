package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/skillcatalog"
	"github.com/revyl/cli/internal/testutil"
	"github.com/revyl/cli/internal/ui"
)

func TestSkillDiscoveryListsAndPreservesRenamedManagedPackages(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, toolDir := range []string{".agents", ".cursor", ".claude", ".codex"} {
			t.Run(fmt.Sprintf("%s/global=%t", toolDir, global), func(t *testing.T) {
				resetSkillInstallFlags(t)
				workDir, homeDir := t.TempDir(), t.TempDir()
				withWorkingDir(t, workDir)
				testutil.SetHomeDir(t, homeDir)
				root := workDir
				if global {
					root = homeDir
				}
				skill, _ := skillcatalog.Get("revyl-cli-dev-loop")
				base := filepath.Join(root, toolDir, "skills")
				path, _, err := installSkillTo(base, skill, false)
				if err != nil {
					t.Fatal(err)
				}
				renamed := filepath.Join(base, "my-device-workflow")
				if err := os.Rename(filepath.Dir(path), renamed); err != nil {
					t.Fatal(err)
				}
				renamed, err = filepath.EvalSymlinks(renamed)
				if err != nil {
					t.Fatal(err)
				}
				before := snapshotSkillDiscoveryTree(t, root)
				originalInstalled, originalJSON := skillListInstalled, skillListJSON
				skillListInstalled, skillListJSON = true, true
				t.Cleanup(func() { skillListInstalled, skillListJSON = originalInstalled, originalJSON })
				var output bytes.Buffer
				cmd := &cobra.Command{}
				cmd.SetOut(&output)
				if err := printSkillCatalog(cmd); err != nil {
					t.Fatal(err)
				}
				var listed skillInstallResult
				if err := json.Unmarshal(output.Bytes(), &listed); err != nil {
					t.Fatal(err)
				}
				want := []skillInstallEntry{{Name: skill.Name, Path: renamed, Status: "installed"}}
				if !reflect.DeepEqual(listed.Skills, want) {
					t.Fatalf("listed skills = %#v, want %#v", listed.Skills, want)
				}
				skillUpdateGlobal, skillUpdateJSON = global, true
				for _, names := range [][]string{nil, {skill.Name}} {
					output.Reset()
					if err := runSkillUpdate(cmd, names); err == nil || !strings.Contains(err.Error(), "renamed") {
						t.Fatalf("update names=%v error = %v, want renamed package preservation", names, err)
					}
					var updated skillInstallResult
					if err := json.Unmarshal(output.Bytes(), &updated); err != nil {
						t.Fatal(err)
					}
					if len(updated.Skills) != 1 || updated.Skills[0].Name != skill.Name || updated.Skills[0].Path != renamed || updated.Skills[0].Status != "preserved" || !strings.Contains(updated.Skills[0].Reason, "renamed") {
						t.Fatalf("update result = %#v, want renamed package preserved at its actual path", updated)
					}
				}
				if !reflect.DeepEqual(snapshotSkillDiscoveryTree(t, root), before) {
					t.Fatal("list or update modified the renamed package or recreated its canonical directory")
				}
			})
		}
	}
}

func TestSkillDiscoveryIgnoresUnrelatedUnmanagedDirectories(t *testing.T) {
	resetSkillInstallFlags(t)
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	testutil.SetHomeDir(t, t.TempDir())
	base := filepath.Join(workDir, ".agents", "skills")
	for _, name := range []string{"unrelated", "revyl-custom-workflow"} {
		dir := filepath.Join(base, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("custom skill"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "notes.txt"), []byte("not a package"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotSkillDiscoveryTree(t, workDir)
	entries, err := installedSkillPaths(false)
	if err != nil || len(entries) != 0 {
		t.Fatalf("discovered unmanaged skills = %#v, error = %v", entries, err)
	}
	if err := runSkillUpdate(&cobra.Command{}, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshotSkillDiscoveryTree(t, workDir), before) {
		t.Fatal("update modified unrelated unmanaged content")
	}
}

func TestSkillDiscoveryRejectsInvalidRecordsInUnknownDirectories(t *testing.T) {
	for _, record := range []struct {
		name    string
		content string
	}{
		{name: "malformed", content: "not JSON"},
		{name: "unknown skill", content: `{"schema_version":1,"name":"unknown-skill","files":{"SKILL.md":"` + strings.Repeat("a", 64) + `"}}`},
		{name: "unsupported schema", content: `{"schema_version":999,"name":"revyl-cli-create","files":{"SKILL.md":"` + strings.Repeat("a", 64) + `"}}`},
		{name: "missing entrypoint", content: `{"schema_version":1,"name":"revyl-cli-create","files":{}}`},
	} {
		t.Run(record.name, func(t *testing.T) {
			resetSkillInstallFlags(t)
			workDir := t.TempDir()
			withWorkingDir(t, workDir)
			testutil.SetHomeDir(t, t.TempDir())
			dir := filepath.Join(workDir, ".agents", "skills", "my-workflow")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, skillInstallStateFile), []byte(record.content), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("keep this file"), 0o644); err != nil {
				t.Fatal(err)
			}
			before := snapshotSkillDiscoveryTree(t, workDir)
			entries, err := installedSkillPaths(false)
			if err == nil || len(entries) != 0 {
				t.Fatalf("discovery accepted invalid record: entries=%#v error=%v", entries, err)
			}
			originalInstalled, originalJSON := skillListInstalled, skillListJSON
			skillListInstalled, skillListJSON = true, true
			t.Cleanup(func() { skillListInstalled, skillListJSON = originalInstalled, originalJSON })
			var output bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetOut(&output)
			if err := printSkillCatalog(cmd); err == nil || output.Len() != 0 {
				t.Fatalf("list accepted an invalid record: output=%q error=%v", output.String(), err)
			}
			if err := runSkillUpdate(&cobra.Command{}, nil); err == nil {
				t.Fatal("update accepted an invalid installation record")
			}
			if !reflect.DeepEqual(snapshotSkillDiscoveryTree(t, workDir), before) {
				t.Fatal("invalid record discovery or update modified local content")
			}
		})
	}
}

func TestSkillDiscoveryDeduplicatesRenamedPackageLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires symlink support")
	}
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	skill, _ := skillcatalog.Get("revyl-cli-create")
	base := filepath.Join(workDir, ".agents", "skills")
	path, _, err := installSkillTo(base, skill, false)
	if err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(base, "my-workflow")
	if err := os.Rename(filepath.Dir(path), renamed); err != nil {
		t.Fatal(err)
	}
	linkBase := filepath.Join(workDir, ".claude", "skills")
	if err := os.MkdirAll(linkBase, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(renamed, filepath.Join(linkBase, "linked-workflow")); err != nil {
		t.Fatal(err)
	}
	entries, err := installedSkillPaths(false)
	if err != nil || len(entries) != 1 || entries[0].Name != skill.Name || entries[0].Path != renamed {
		t.Fatalf("renamed linked packages = %#v, error = %v", entries, err)
	}
}

func TestSkillDiscoveryRejectsOutOfScopeUnknownDirectoryLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires symlink support")
	}
	resetSkillInstallFlags(t)
	workDir, outside := t.TempDir(), t.TempDir()
	withWorkingDir(t, workDir)
	skill, _ := skillcatalog.Get("revyl-cli-create")
	path, _, err := installSkillTo(outside, skill, false)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(workDir, ".agents", "skills")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(path), filepath.Join(base, "my-workflow")); err != nil {
		t.Fatal(err)
	}
	before := snapshotSkillDiscoveryTree(t, outside)
	entries, err := installedSkillPaths(false)
	if err == nil || len(entries) != 0 || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("discovery followed out-of-scope link: entries=%#v error=%v", entries, err)
	}
	if err := runSkillUpdate(&cobra.Command{}, nil); err == nil {
		t.Fatal("update accepted an out-of-scope package link")
	}
	if !reflect.DeepEqual(snapshotSkillDiscoveryTree(t, outside), before) {
		t.Fatal("discovery or update modified out-of-scope content")
	}
}

func TestSkillSelectionCancellationHasTypedCompletion(t *testing.T) {
	for _, stage := range []string{"skill", "agent", "confirmation"} {
		for _, selectionErr := range []error{ui.ErrSelectionCancelled, fmt.Errorf("wrapped: %w", ui.ErrSelectionCancelled), errors.New("selection cancelled"), errors.New("terminal failed")} {
			t.Run(stage+"/"+selectionErr.Error(), func(t *testing.T) {
				resetSkillInstallFlags(t)
				workDir := t.TempDir()
				withWorkingDir(t, workDir)
				testutil.SetHomeDir(t, t.TempDir())
				skillInputIsTTY = func() bool { return true }
				skillInstallAgents = []string{"cursor"}
				names := []string{"revyl-cli-create"}
				if stage == "skill" {
					names = nil
				}
				if stage == "agent" {
					skillInstallAgents = nil
				}
				selectAgentSkills = func(string, []ui.SelectOption, []string) ([]string, error) {
					return nil, selectionErr
				}
				confirmSkillPlan = func(string, bool) (bool, error) { return false, selectionErr }
				err := installSelectedSkills(newTestCommand(), names)
				if !errors.Is(err, selectionErr) {
					t.Fatalf("installation error = %v, want original selection error %v", err, selectionErr)
				}
				var completed *analytics.CompletedError
				if errors.Is(selectionErr, ui.ErrSelectionCancelled) {
					if !errors.As(err, &completed) {
						t.Fatalf("cancellation has no typed completion: %v", err)
					}
					completion := completed.Completion()
					if completion.ExitCode != 1 || completion.Domain != "skill_install" || completion.DomainStatus != "cancelled" {
						t.Fatalf("cancellation completion = %#v", completion)
					}
				} else if errors.As(err, &completed) {
					t.Fatalf("arbitrary failure was classified as cancellation: %#v", completed.Completion())
				}
				entries, err := os.ReadDir(workDir)
				if err != nil || len(entries) != 0 {
					t.Fatalf("selection failure wrote files: entries=%v error=%v", entries, err)
				}
			})
		}
	}
}

func TestSkillDiscoveryRejectsSymlinkedUnknownInstallationRecord(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires symlink support")
	}
	workDir, outside := t.TempDir(), t.TempDir()
	withWorkingDir(t, workDir)
	skill, _ := skillcatalog.Get("revyl-cli-create")
	path, _, err := installSkillTo(outside, skill, false)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(workDir, ".agents", "skills", "my-workflow")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(filepath.Dir(path), skillInstallStateFile), filepath.Join(dir, skillInstallStateFile)); err != nil {
		t.Fatal(err)
	}
	entries, err := installedSkillPaths(false)
	if err == nil || len(entries) != 0 || !strings.Contains(err.Error(), "invalid installation record") {
		t.Fatalf("discovery followed a manifest symlink: entries=%#v error=%v", entries, err)
	}
}

func snapshotSkillDiscoveryTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		var content []byte
		if info.IsDir() || info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			info, err = file.Stat()
			if err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				content, err = io.ReadAll(file)
				if err != nil {
					return err
				}
			}
		}
		snapshot[path] = fmt.Sprintf("%s\x00%s\x00%s", info.Mode(), info.ModTime().UTC().Format(time.RFC3339Nano), content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
