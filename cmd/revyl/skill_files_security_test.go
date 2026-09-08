package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/skillcatalog"
)

func TestSkillInstallKeepsDirectoriesAndStatePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not available on Windows")
	}
	base := filepath.Join(t.TempDir(), ".agents", "skills")
	skill, _ := skillcatalog.Get("revyl-cli-auth-bypass")
	path, _, err := installSkillTo(base, skill, false)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	for _, path := range []string{base, dir, filepath.Join(dir, "agents"), filepath.Join(dir, "references")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("directory %s: info=%v error=%v, want mode 0700", path, info, err)
		}
	}
	info, err := os.Stat(filepath.Join(dir, skillInstallStateFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("installation record: info=%v error=%v, want mode 0600", info, err)
	}
	files, err := skill.Files()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(file.Path)))
		if err != nil || info.Mode().Perm() != file.Mode.Perm() {
			t.Fatalf("package file %s: info=%v error=%v, want mode %o", file.Path, info, err, file.Mode.Perm())
		}
	}
}

func TestSkillValidationRejectsSymlinkedStateAndPackageFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permission is not guaranteed on Windows")
	}
	for _, relative := range []string{skillInstallStateFile, "SKILL.md", "references"} {
		t.Run(relative, func(t *testing.T) {
			skill, _ := skillcatalog.Get("revyl-cli-auth-bypass")
			path, _, err := installSkillTo(t.TempDir(), skill, false)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Dir(path)
			target := filepath.Join(dir, relative)
			outside := filepath.Join(t.TempDir(), "outside")
			if err := os.Rename(target, outside); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, target); err != nil {
				t.Fatal(err)
			}
			if err := validateManagedSkill(dir, skill.Name); err == nil {
				t.Fatal("validation followed a symlink outside the package")
			}
		})
	}
}

func TestSkillStateRejectsInvalidFileEntries(t *testing.T) {
	for _, path := range []string{"../outside", "/absolute", "references\\outside", skillInstallStateFile} {
		t.Run(path, func(t *testing.T) {
			skill, _ := skillcatalog.Get("revyl-cli-create")
			skillPath, _, err := installSkillTo(t.TempDir(), skill, false)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Dir(skillPath)
			state, err := readInstalledSkillState(dir)
			if err != nil {
				t.Fatal(err)
			}
			state.Files[path] = strings.Repeat("a", 64)
			encoded, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, skillInstallStateFile), encoded, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readInstalledSkillState(dir); err == nil {
				t.Fatal("accepted an invalid installation record path")
			}
		})
	}
}

func TestSkillLinkPermissionFailurePrecedesPackageWritesAndAllowsCopy(t *testing.T) {
	resetSkillInstallFlags(t)
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	original := createSkillSymlink
	createSkillSymlink = func(string, string) error { return os.ErrPermission }
	t.Cleanup(func() { createSkillSymlink = original })
	skill, _ := skillcatalog.Get("revyl-cli-create")
	targets := resolveDirectoriesForScope([]string{"cursor", "claude"}, false)
	result, err := applySkillInstall(targets, []skillcatalog.Skill{skill}, false, false)
	if !errors.Is(err, os.ErrPermission) || !strings.Contains(err.Error(), "--copy") || len(result.Skills) != 0 {
		t.Fatalf("result=%v error=%v, want actionable preflight failure", result, err)
	}
	if _, err := os.Lstat(filepath.Join(workDir, ".agents")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlink permission failure still wrote shared packages: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(workDir, ".claude", "skills"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("link capability check left temporary files: entries=%v error=%v", entries, err)
	}
	result, err = applySkillInstall(targets, []skillcatalog.Skill{skill}, false, true)
	if err != nil || len(result.Skills) != 2 {
		t.Fatalf("copy fallback result=%v error=%v", result, err)
	}
}

func TestSkillBackupCleanupFailureRetainsSuccessfulInstallAndClaudeLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permission is not guaranteed on Windows")
	}
	resetSkillInstallFlags(t)
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	skill, _ := skillcatalog.Get("revyl-cli-create")
	if _, _, err := installSkillTo(filepath.Join(workDir, ".agents", "skills"), skill, false); err != nil {
		t.Fatal(err)
	}
	original := removeSkillBackup
	var backups []string
	removeSkillBackup = func(path string) error {
		backups = append(backups, path)
		return os.ErrPermission
	}
	t.Cleanup(func() { removeSkillBackup = original })
	var result skillInstallResult
	var installErr error
	output := captureStdoutAndStderr(t, func() {
		result, installErr = applySkillInstall(resolveDirectoriesForScope([]string{"cursor", "claude"}, false), []skillcatalog.Skill{skill}, true, false)
	})
	if installErr != nil || len(result.Skills) != 1 || result.Skills[0].Status != "installed" {
		t.Fatalf("completed install result=%v error=%v", result, installErr)
	}
	if len(backups) != 1 || !strings.Contains(output, backups[0]) || !strings.Contains(output, "remove it manually") {
		t.Fatalf("backup cleanup attempts=%v output=%q, want one actionable warning", backups, output)
	}
	if _, err := os.Stat(filepath.Join(backups[0], skill.Name, "SKILL.md")); err != nil {
		t.Fatalf("backup was not preserved: %v", err)
	}
	link := filepath.Join(workDir, ".claude", "skills", skill.Name)
	canonical, err := filepath.Abs(result.Skills[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if real, err := filepath.EvalSymlinks(link); err != nil || real != canonical {
		t.Fatalf("compatibility link=%q error=%v", real, err)
	}
}
