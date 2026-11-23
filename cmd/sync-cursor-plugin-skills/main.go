// Command sync-cursor-plugin-skills writes the shared CLI skills the Cursor
// plugin bundles from skills/ into cursor-plugin/skills/, rendered for the
// plugin's pinned CLI runtime, or with --check verifies the bundled copies.
//
// Usage: sync-cursor-plugin-skills [--check] [--root <revyl-cli dir>] <skill>...
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/revyl/cli/internal/pinnedskills"
)

var skillNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func main() {
	check := flag.Bool("check", false, "verify the bundled skills without writing")
	root := flag.String("root", ".", "directory holding skills/ and cursor-plugin/")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "sync Cursor plugin skills: name at least one skill")
		os.Exit(2)
	}
	if err := syncSkills(*root, flag.Args(), *check); err != nil {
		fmt.Fprintf(os.Stderr, "sync Cursor plugin skills: %v\n", err)
		os.Exit(1)
	}
}

func syncSkills(root string, skills []string, check bool) error {
	for _, skill := range skills {
		if !skillNamePattern.MatchString(skill) {
			return fmt.Errorf("%q is not a skill directory name", skill)
		}
	}
	sourceSkills, err := openDirectoryRoot(filepath.Join(root, "skills"))
	if err != nil {
		return err
	}
	defer sourceSkills.Close()
	pluginSkills, err := openDirectoryRoot(filepath.Join(root, "cursor-plugin", "skills"))
	if err != nil {
		return err
	}
	defer pluginSkills.Close()
	rendered := make([]map[string]pinnedskills.File, len(skills))
	for i, skill := range skills {
		if rendered[i], err = pinnedskills.RenderSkillFiles(sourceSkills.FS(), skill); err != nil {
			return err
		}
	}
	for i, skill := range skills {
		if check {
			if err := checkSkill(pluginSkills, skill, rendered[i]); err != nil {
				return fmt.Errorf("%w; run make sync-cursor-plugin-skills", err)
			}
			continue
		}
		if err := pluginSkills.RemoveAll(skill); err != nil {
			return err
		}
		for relative, file := range rendered[i] {
			path := filepath.Join(skill, filepath.FromSlash(relative))
			if err := pluginSkills.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				return err
			}
			if err := pluginSkills.WriteFile(path, file.Content, file.Mode); err != nil {
				return err
			}
			if err := pluginSkills.Chmod(path, file.Mode); err != nil {
				return err
			}
		}
	}
	if !check {
		fmt.Println("cursor-plugin skills synced")
	}
	return nil
}

// openDirectoryRoot opens path as an os.Root, refusing a symlink at path itself
// because os.OpenRoot would follow it out of the tree.
func openDirectoryRoot(path string) (*os.Root, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s must be a directory, not a symlink or file", filepath.ToSlash(path))
	}
	return os.OpenRoot(path)
}

func checkSkill(pluginSkills *os.Root, skill string, want map[string]pinnedskills.File) error {
	err := fs.WalkDir(pluginSkills.FS(), skill, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if _, ok := want[strings.TrimPrefix(path, skill+"/")]; !ok {
			return fmt.Errorf("cursor-plugin/skills/%s has no source in skills/", path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for relative, file := range want {
		path := filepath.Join(skill, filepath.FromSlash(relative))
		content, err := pluginSkills.ReadFile(path)
		if err != nil || !bytes.Equal(content, file.Content) {
			return fmt.Errorf("cursor-plugin/skills/%s is stale", filepath.ToSlash(path))
		}
		info, err := pluginSkills.Stat(path)
		if err != nil {
			return err
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != file.Mode {
			return fmt.Errorf("cursor-plugin/skills/%s mode %o differs from source %o", filepath.ToSlash(path), info.Mode().Perm(), file.Mode)
		}
	}
	return nil
}
