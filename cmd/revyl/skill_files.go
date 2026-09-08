package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/revyl/cli/internal/skillcatalog"
	"github.com/revyl/cli/internal/ui"
)

const skillInstallStateFile = ".revyl-install.json"

var (
	createSkillSymlink = os.Symlink
	removeSkillBackup  = os.RemoveAll
)

type installedSkillState struct {
	SchemaVersion int               `json:"schema_version"`
	Name          string            `json:"name"`
	CLIVersion    string            `json:"cli_version"`
	Files         map[string]string `json:"files"`
}

func skillCanonicalBase(target skillInstallTarget) string {
	return filepath.Join(filepath.Dir(filepath.Dir(target.path)), ".agents", "skills")
}

func validateSkillDestination(path, root string) error {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	ancestor := path
	for {
		_, err := os.Lstat(ancestor)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return fmt.Errorf("no existing ancestor for skill destination %s", path)
		}
		ancestor = parent
	}
	realPath, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return err
	}
	realPath, err = filepath.Abs(realPath)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, realPath)
	if err != nil {
		return err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("skill destination %s resolves outside the selected project or home directory", path)
	}
	return nil
}

func skillFileHash(content []byte, mode fs.FileMode) string {
	if runtime.GOOS == "windows" {
		mode &= 0o200
	}
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%o\x00", mode.Perm())
	_, _ = hash.Write(content)
	return hex.EncodeToString(hash.Sum(nil))
}

func installSkillTo(baseDir string, selected skillcatalog.Skill, force bool) (string, bool, error) {
	skillDir := filepath.Join(baseDir, selected.Name)
	skillPath := filepath.Join(skillDir, skillcatalog.SkillFileName)
	if !fs.ValidPath(selected.Name) || strings.ContainsAny(selected.Name, "/\\") || selected.Name == "." {
		return skillPath, false, fmt.Errorf("invalid skill directory name")
	}
	info, statErr := os.Lstat(skillDir)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return skillPath, false, statErr
	}
	if statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return skillPath, false, fmt.Errorf("refusing to replace a symlink or non-directory at %s", skillDir)
		}
		if !force {
			if _, err := os.Stat(skillPath); err != nil {
				return skillPath, false, fmt.Errorf("existing directory does not contain a readable skill: %w", err)
			}
			return skillPath, false, nil
		}
	}
	files, err := selected.Files()
	if err != nil {
		return skillPath, false, err
	}
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return skillPath, false, err
	}
	staged, err := os.MkdirTemp(baseDir, ".revyl-skill-stage-")
	if err != nil {
		return skillPath, false, err
	}
	defer os.RemoveAll(staged)
	state := installedSkillState{SchemaVersion: 1, Name: selected.Name, CLIVersion: version, Files: make(map[string]string, len(files))}
	for _, file := range files {
		if !fs.ValidPath(file.Path) || strings.Contains(file.Path, "\\") || file.Path == skillInstallStateFile {
			return skillPath, false, fmt.Errorf("invalid skill package path %q", file.Path)
		}
		path := filepath.Join(staged, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return skillPath, false, err
		}
		if err := os.WriteFile(path, file.Content, file.Mode.Perm()); err != nil {
			return skillPath, false, err
		}
		if err := os.Chmod(path, file.Mode.Perm()); err != nil {
			return skillPath, false, err
		}
		state.Files[file.Path] = skillFileHash(file.Content, file.Mode)
	}
	if _, ok := state.Files[skillcatalog.SkillFileName]; !ok {
		return skillPath, false, fmt.Errorf("skill package is missing SKILL.md")
	}
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return skillPath, false, err
	}
	if err := os.WriteFile(filepath.Join(staged, skillInstallStateFile), append(encoded, '\n'), 0o600); err != nil {
		return skillPath, false, err
	}
	if statErr == nil {
		backup, err := os.MkdirTemp(baseDir, ".revyl-skill-backup-")
		if err != nil {
			return skillPath, false, err
		}
		if err := os.Rename(skillDir, filepath.Join(backup, selected.Name)); err != nil {
			_ = os.Remove(backup)
			return skillPath, false, err
		}
		if err := os.Rename(staged, skillDir); err != nil {
			restoreErr := os.Rename(filepath.Join(backup, selected.Name), skillDir)
			if restoreErr != nil {
				return skillPath, false, fmt.Errorf("install failed: %v; restore failed: %v; previous files retained at %s", err, restoreErr, backup)
			}
			_ = os.Remove(backup)
			return skillPath, false, err
		}
		if err := removeSkillBackup(backup); err != nil {
			ui.PrintWarning("Installed %s, but could not remove backup %s: %v; remove it manually when safe", selected.Name, backup, err)
		}
	} else if err := os.Rename(staged, skillDir); err != nil {
		return skillPath, false, err
	}
	return skillPath, true, nil
}

func readInstalledSkillState(skillDir string) (installedSkillState, error) {
	var state installedSkillState
	root, err := os.OpenRoot(skillDir)
	if err != nil {
		return state, err
	}
	defer root.Close()
	info, err := root.Lstat(skillInstallStateFile)
	if err != nil {
		return state, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return state, fmt.Errorf("invalid installation record")
	}
	content, err := root.ReadFile(skillInstallStateFile)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(content, &state); err != nil {
		return state, fmt.Errorf("invalid installation record: %w", err)
	}
	skill, known := skillcatalog.Get(state.Name)
	if state.SchemaVersion != 1 || !known || state.Name != skill.Name || len(state.Files) == 0 || state.Files[skillcatalog.SkillFileName] == "" {
		return state, fmt.Errorf("unsupported or invalid installation record")
	}
	for path, digest := range state.Files {
		if !fs.ValidPath(path) || strings.Contains(path, "\\") || path == skillInstallStateFile || len(digest) != sha256.Size*2 {
			return state, fmt.Errorf("invalid installation record file entry")
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return state, fmt.Errorf("invalid installation record file hash")
		}
	}
	return state, nil
}

func validateManagedSkill(skillDir string, name string) error {
	if filepath.Base(skillDir) != name {
		return fmt.Errorf("skill directory was renamed; leaving it unchanged")
	}
	state, err := readInstalledSkillState(skillDir)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("not managed by Revyl; reinstall explicitly with --name %s --force to adopt it", name)
	}
	if err != nil {
		return err
	}
	if state.Name != name {
		return fmt.Errorf("unsupported or invalid installation record")
	}
	root, err := os.OpenRoot(skillDir)
	if err != nil {
		return err
	}
	defer root.Close()
	seen := make(map[string]bool, len(state.Files))
	err = fs.WalkDir(root.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if relative == "." {
			return nil
		}
		if entry.IsDir() {
			for installedPath := range state.Files {
				if strings.HasPrefix(installedPath, relative+"/") {
					return nil
				}
			}
			return fmt.Errorf("local directory at %s; leaving skill unchanged", relative)
		}
		if relative == skillInstallStateFile {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("local symlink at %s; leaving skill unchanged", relative)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular file at %s; leaving skill unchanged", relative)
		}
		if _, known := state.Files[relative]; !known {
			return fmt.Errorf("local file at %s; leaving skill unchanged", relative)
		}
		data, err := root.ReadFile(filepath.FromSlash(relative))
		if err != nil {
			return err
		}
		if state.Files[relative] != skillFileHash(data, info.Mode()) {
			return fmt.Errorf("local changes at %s; leaving skill unchanged", relative)
		}
		seen[relative] = true
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(state.Files) {
		return fmt.Errorf("installed files were removed; leaving skill unchanged")
	}
	return nil
}

func linkSkillForTarget(target skillInstallTarget, name string, canonicalDir string) error {
	linkPath := filepath.Join(target.path, name)
	if target.tool != "claude" {
		if _, err := os.Lstat(linkPath); errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	if info, err := os.Lstat(linkPath); err == nil {
		resolved, resolveErr := filepath.EvalSymlinks(linkPath)
		canonical, canonicalErr := filepath.EvalSymlinks(canonicalDir)
		if resolveErr == nil && canonicalErr == nil && resolved == canonical {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("existing link %s points elsewhere; leaving it unchanged", linkPath)
		}
		return fmt.Errorf("legacy installation at %s is preserved; move it aside before retrying to avoid duplicate discovery", linkPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(target.path, 0o700); err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(target.path)
	if err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(canonicalDir)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(parent, canonical)
	if err != nil {
		return err
	}
	if err := createSkillSymlink(relative, linkPath); err != nil {
		return fmt.Errorf("create compatibility link: %w; use --copy if this filesystem does not support symlinks", err)
	}
	return nil
}

func validateSkillLinkSupport(target skillInstallTarget, selected []skillcatalog.Skill) error {
	if target.tool != "claude" {
		return nil
	}
	for _, skill := range selected {
		if _, err := os.Lstat(filepath.Join(target.path, skill.Name)); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.MkdirAll(target.path, 0o700); err != nil {
			return err
		}
		probe, err := os.MkdirTemp(target.path, ".revyl-link-check-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(probe)
		if err := createSkillSymlink(".", filepath.Join(probe, "link")); err != nil {
			return fmt.Errorf("Claude compatibility links are unavailable: %w; use --copy or choose Claude Code (copy mode) during init", err)
		}
		return nil
	}
	return nil
}
