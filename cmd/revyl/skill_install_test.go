package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/skillcatalog"
	"github.com/revyl/cli/internal/testutil"
	"github.com/revyl/cli/internal/ui"
)

func resetSkillInstallFlags(t *testing.T) {
	t.Helper()
	oldAll, oldYes, oldCopy, oldJSON := skillInstallAll, skillInstallYes, skillInstallCopy, skillInstallJSON
	oldAgents, oldTTY, oldSelect, oldConfirm := skillInstallAgents, skillInputIsTTY, selectAgentSkills, confirmSkillPlan
	oldCLI, oldMCP, oldGlobal := skillInstallCLI, skillInstallMCP, skillInstallGlobal
	oldUpdateGlobal, oldUpdateJSON := skillUpdateGlobal, skillUpdateJSON
	skillInstallAll, skillInstallYes, skillInstallCopy, skillInstallJSON = false, false, false, false
	skillInstallCLI, skillInstallMCP, skillInstallGlobal = false, false, false
	skillUpdateGlobal, skillUpdateJSON = false, false
	skillInstallAgents = nil
	skillInputIsTTY = func() bool { return false }
	withoutExplicitSkillTools(t)
	t.Cleanup(func() {
		skillInstallAll, skillInstallYes, skillInstallCopy, skillInstallJSON = oldAll, oldYes, oldCopy, oldJSON
		skillInstallAgents, skillInputIsTTY, selectAgentSkills, confirmSkillPlan = oldAgents, oldTTY, oldSelect, oldConfirm
		skillInstallCLI, skillInstallMCP, skillInstallGlobal = oldCLI, oldMCP, oldGlobal
		skillUpdateGlobal, skillUpdateJSON = oldUpdateGlobal, oldUpdateJSON
	})
}

func skillNames(skills []skillcatalog.Skill) []string {
	names := make([]string, 0, len(skills))
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	return names
}

func captureSkillUIMessages(t *testing.T) *[]string {
	t.Helper()
	var messages []string
	ui.SetOutputObserver(func(_ string, message string) { messages = append(messages, message) })
	t.Cleanup(func() { ui.SetOutputObserver(nil) })
	return &messages
}

func TestChooseInstallSkillsHonorsExplicitSelectors(t *testing.T) {
	resetSkillInstallFlags(t)
	selectAgentSkills = func(string, []ui.SelectOption, []string) ([]string, error) {
		t.Fatal("explicit selectors opened the picker")
		return nil, nil
	}
	selected, usedDefault, err := chooseInstallSkills([]string{"revyl-cli-analyze"})
	if err != nil || usedDefault || strings.Join(skillNames(selected), ",") != "revyl-cli-analyze" {
		t.Fatalf("--name = %v, default=%v, err=%v", skillNames(selected), usedDefault, err)
	}
	skillInstallAll = true
	selected, usedDefault, err = chooseInstallSkills(nil)
	if err != nil || usedDefault || len(selected) != len(skillcatalog.All()) {
		t.Fatalf("--all = %d skills, default=%v, err=%v", len(selected), usedDefault, err)
	}
	if _, _, err := chooseInstallSkills([]string{"revyl-cli-dev-loop"}); err == nil {
		t.Fatal("mixed selectors were accepted")
	}
}

func TestChooseInstallSkillsUsesDefaultSetWhenNoninteractive(t *testing.T) {
	want := strings.Join(skillNames(skillcatalog.DefaultInstall()), ",")
	cases := []struct {
		name        string
		tty         bool
		yes, asJSON bool
	}{
		{name: "no terminal", tty: false},
		{name: "--yes", tty: true, yes: true},
		{name: "--json", tty: true, asJSON: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetSkillInstallFlags(t)
			skillInputIsTTY = func() bool { return tc.tty }
			skillInstallYes, skillInstallJSON = tc.yes, tc.asJSON
			selectAgentSkills = func(string, []ui.SelectOption, []string) ([]string, error) {
				t.Fatal("non-interactive run opened the picker")
				return nil, nil
			}
			selected, usedDefault, err := chooseInstallSkills(nil)
			if err != nil || !usedDefault || strings.Join(skillNames(selected), ",") != want {
				t.Fatalf("selected=%v default=%v err=%v, want %s", skillNames(selected), usedDefault, err, want)
			}
		})
	}
}

func TestInstallPickerPreselectsDefaultSet(t *testing.T) {
	resetSkillInstallFlags(t)
	skillInputIsTTY = func() bool { return true }
	want := skillNames(skillcatalog.DefaultInstall())
	selectAgentSkills = func(_ string, options []ui.SelectOption, initial []string) ([]string, error) {
		if strings.Join(initial, ",") != strings.Join(want, ",") || len(options) != len(skillcatalog.All()) {
			t.Fatalf("picker defaults=%v options=%d, want defaults %v", initial, len(options), want)
		}
		return initial, nil
	}
	selected, usedDefault, err := chooseInstallSkills(nil)
	if err != nil || usedDefault || strings.Join(skillNames(selected), ",") != strings.Join(want, ",") {
		t.Fatalf("accepting the preselection = %v, default=%v, err=%v", skillNames(selected), usedDefault, err)
	}
	selectAgentSkills = func(string, []ui.SelectOption, []string) ([]string, error) {
		return []string{"revyl-cli-analyze"}, nil
	}
	selected, _, err = chooseInstallSkills(nil)
	if err != nil || strings.Join(skillNames(selected), ",") != "revyl-cli-analyze" {
		t.Fatalf("changed selection = %v, err=%v", skillNames(selected), err)
	}
	selectAgentSkills = func(string, []ui.SelectOption, []string) ([]string, error) { return nil, nil }
	selected, _, err = chooseInstallSkills(nil)
	if err != nil || len(selected) != 0 {
		t.Fatalf("empty selection became a bundle: %v, %v", skillNames(selected), err)
	}
}

func TestNoninteractiveInstallAnnouncesDefaultSetOnStderr(t *testing.T) {
	resetSkillInstallFlags(t)
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	testutil.SetHomeDir(t, t.TempDir())
	skillInstallAgents = []string{"cursor"}
	messages := captureSkillUIMessages(t)
	var stdout bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&stdout)
	if err := installSelectedSkills(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("human mode wrote to stdout: %q", stdout.String())
	}
	var announcement []string
	for _, message := range *messages {
		if strings.Contains(message, "recommended set") {
			announcement = append(announcement, message)
		}
	}
	if len(announcement) != 1 || !strings.Contains(announcement[0], "--name <skill>") || !strings.Contains(announcement[0], "--all") {
		t.Fatalf("announcement=%q, want one line naming --name and --all", announcement)
	}
	for _, skill := range skillcatalog.DefaultInstall() {
		if !strings.Contains(announcement[0], skill.Name) {
			t.Fatalf("announcement %q does not name %s", announcement[0], skill.Name)
		}
		if _, err := os.Stat(filepath.Join(workDir, ".agents", "skills", skill.Name, skillcatalog.SkillFileName)); err != nil {
			t.Fatalf("default skill %s not installed: %v", skill.Name, err)
		}
	}
}

func TestSharedInstallCreatesOneCompletePackageAndClaudeLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permission is not guaranteed on Windows; copy mode is tested separately")
	}
	resetSkillInstallFlags(t)
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	skill, _ := skillcatalog.Get("revyl-cli-auth-bypass")
	targets := resolveDirectoriesForScope([]string{"cursor", "claude", "codex"}, false)
	result, err := applySkillInstall(targets, []skillcatalog.Skill{skill}, false, false)
	if err != nil || len(result.Skills) != 1 {
		t.Fatalf("result=%v err=%v", result, err)
	}
	canonical, err := filepath.EvalSymlinks(filepath.Join(workDir, ".agents", "skills", skill.Name))
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"SKILL.md", "agents/openai.yaml", "references/expo.md", skillInstallStateFile} {
		if _, err := os.Stat(filepath.Join(canonical, relative)); err != nil {
			t.Fatalf("missing package file %s: %v", relative, err)
		}
	}
	link := filepath.Join(workDir, ".claude", "skills", skill.Name)
	linkTarget, err := os.Readlink(link)
	if err != nil || filepath.IsAbs(linkTarget) {
		t.Fatalf("link target=%q err=%v", linkTarget, err)
	}
	real, err := filepath.EvalSymlinks(link)
	if err != nil || real != canonical {
		t.Fatalf("link resolved=%q err=%v", real, err)
	}
	for _, unexpected := range []string{".cursor/skills", ".codex/skills", "AGENTS.md", ".cursor/rules/revyl-skills.mdc"} {
		if _, err := os.Lstat(filepath.Join(workDir, unexpected)); !os.IsNotExist(err) {
			t.Fatalf("unexpected eager guidance or duplicate directory %s: %v", unexpected, err)
		}
	}
	entries, err := installedSkillPaths(false)
	if err != nil || len(entries) != 1 {
		t.Fatalf("duplicate discovery: entries=%v err=%v", entries, err)
	}
}

func TestSharedInstallRefusesLegacyConflictBeforeWriting(t *testing.T) {
	resetSkillInstallFlags(t)
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	skill, _ := skillcatalog.Get("revyl-cli-dev-loop")
	legacy := filepath.Join(workDir, ".cursor", "skills", skill.Name)
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := []byte("custom workflow")
	if err := os.WriteFile(filepath.Join(legacy, "SKILL.md"), custom, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := applySkillInstall(resolveDirectoriesForScope([]string{"cursor", "claude"}, false), []skillcatalog.Skill{skill}, true, false)
	if err == nil || !strings.Contains(err.Error(), "preserved") {
		t.Fatalf("legacy conflict error=%v", err)
	}
	actual, _ := os.ReadFile(filepath.Join(legacy, "SKILL.md"))
	if !bytes.Equal(actual, custom) {
		t.Fatal("legacy customization was overwritten")
	}
	if _, err := os.Stat(filepath.Join(workDir, ".agents")); !os.IsNotExist(err) {
		t.Fatal("preflight conflict still wrote canonical files")
	}
}

func TestCopyInstallDoesNotCreateSharedDirectory(t *testing.T) {
	resetSkillInstallFlags(t)
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	skill, _ := skillcatalog.Get("revyl-cli-atlas-review")
	result, err := applySkillInstall(resolveDirectoriesForScope([]string{"claude", "cursor"}, false), []skillcatalog.Skill{skill}, false, true)
	if err != nil || len(result.Skills) != 2 {
		t.Fatalf("result=%v err=%v", result, err)
	}
	for _, tool := range []string{".claude", ".cursor"} {
		path := filepath.Join(workDir, tool, "skills", skill.Name)
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("copy=%s info=%v err=%v", path, info, err)
		}
		if err := validateManagedSkill(path, skill.Name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(workDir, ".agents")); !os.IsNotExist(err) {
		t.Fatal("copy mode created a shared directory")
	}
}

func TestSkillUpdatePreservesSelectionAndModifiedFiles(t *testing.T) {
	resetSkillInstallFlags(t)
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	skill, _ := skillcatalog.Get("revyl-cli-dev-loop")
	base := filepath.Join(workDir, ".agents", "skills")
	path, _, err := installSkillTo(base, skill, false)
	if err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{}
	if err := runSkillUpdate(cmd, nil); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(base)
	if len(entries) != 1 || entries[0].Name() != skill.Name {
		t.Fatalf("update expanded selection: %v", entries)
	}
	custom := []byte("custom instructions")
	if err := os.WriteFile(path, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runSkillUpdate(cmd, nil); err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Fatalf("modified update error=%v", err)
	}
	actual, _ := os.ReadFile(path)
	if !bytes.Equal(actual, custom) {
		t.Fatal("update overwrote local changes")
	}
}

func TestSkillUpdatePreservesUnmanagedAndAdditionalFiles(t *testing.T) {
	resetSkillInstallFlags(t)
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	skill, _ := skillcatalog.Get("revyl-cli-create")
	base := filepath.Join(workDir, ".agents", "skills")
	if _, _, err := installSkillTo(base, skill, false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, skill.Name)
	if err := os.WriteFile(filepath.Join(dir, "custom.md"), []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateManagedSkill(dir, skill.Name); err == nil {
		t.Fatal("additional custom file was ignored")
	}
	if err := os.Remove(filepath.Join(dir, skillInstallStateFile)); err != nil {
		t.Fatal(err)
	}
	if err := runSkillUpdate(&cobra.Command{}, nil); err == nil || !strings.Contains(err.Error(), "not managed") {
		t.Fatalf("unmanaged update error=%v", err)
	}
}

func TestInstallJSONIsParseableAndOnlyReportsSelectedPackage(t *testing.T) {
	resetSkillInstallFlags(t)
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	testutil.SetHomeDir(t, t.TempDir())
	skillInstallJSON = true
	skillInstallAgents = []string{"cursor"}
	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&output)
	if err := installSelectedSkills(cmd, []string{"revyl-cli-analyze"}); err != nil {
		t.Fatal(err)
	}
	var result skillInstallResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON %q: %v", output.String(), err)
	}
	if len(result.Skills) != 1 || result.Skills[0].Name != "revyl-cli-analyze" || result.Skills[0].Status != "installed" {
		t.Fatalf("result=%v", result)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(output.Bytes(), &keys); err != nil || len(keys) != 1 || keys["skills"] == nil {
		t.Fatalf("explicit selection JSON keys = %v, want only skills: %v", keys, err)
	}
}

func TestInstallJSONContractForDefaultSelection(t *testing.T) {
	resetSkillInstallFlags(t)
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	testutil.SetHomeDir(t, t.TempDir())
	skillInputIsTTY = func() bool { return true }
	skillInstallJSON = true
	skillInstallAgents = []string{"cursor"}
	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&output)
	if err := installSelectedSkills(cmd, nil); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Selection string `json:"selection"`
		Skills    []struct {
			Name   string `json:"name"`
			Path   string `json:"path"`
			Status string `json:"status"`
		} `json:"skills"`
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		t.Fatalf("stdout is not the install JSON contract %q: %v", output.String(), err)
	}
	if decoder.More() {
		t.Fatalf("stdout has trailing output after JSON: %q", output.String())
	}
	if payload.Selection != "default" {
		t.Fatalf("selection=%q, want default", payload.Selection)
	}
	want := skillNames(skillcatalog.DefaultInstall())
	if len(payload.Skills) != len(want) {
		t.Fatalf("skills=%+v, want %v", payload.Skills, want)
	}
	for i, entry := range payload.Skills {
		if entry.Name != want[i] || entry.Status != "installed" || filepath.Base(entry.Path) != entry.Name {
			t.Fatalf("entry %d = %+v, want installed %s", i, entry, want[i])
		}
	}
}

func TestInstallFallsBackToCopyWhenLegacyPackagesBlockSharedStorage(t *testing.T) {
	resetSkillInstallFlags(t)
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	testutil.SetHomeDir(t, t.TempDir())
	legacy := filepath.Join(workDir, ".cursor", "skills", "revyl-cli-dev-loop")
	writeLegacySkillPackage(t, legacy)
	skillInstallAgents = []string{"cursor"}
	messages := captureSkillUIMessages(t)
	if err := installSelectedSkills(&cobra.Command{}, []string{"revyl-cli-dev-loop", "revyl-cli-create"}); err != nil {
		t.Fatalf("legacy packages failed the install: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(legacy, skillcatalog.SkillFileName)); err != nil || string(content) != "customized" {
		t.Fatalf("legacy package changed without --force: %q, %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(workDir, ".cursor", "skills", "revyl-cli-create", skillcatalog.SkillFileName)); err != nil {
		t.Fatalf("remaining skill was not copied beside the legacy package: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(workDir, ".agents", "skills")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fallback created shared storage: %v", err)
	}
	if !strings.Contains(strings.Join(*messages, "\n"), "continuing with --copy") {
		t.Fatalf("fallback was not announced: %q", *messages)
	}
}

func TestNoninteractiveInstallWithoutDetectedAgentUsesSharedStorage(t *testing.T) {
	resetSkillInstallFlags(t)
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	testutil.SetHomeDir(t, t.TempDir())
	skillInstallYes = true
	if err := installSelectedSkills(&cobra.Command{}, []string{"revyl-cli-dev-loop"}); err != nil {
		t.Fatalf("install without a detected agent failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workDir, ".agents", "skills", "revyl-cli-dev-loop", skillcatalog.SkillFileName)); err != nil {
		t.Fatalf("skill not installed to shared storage: %v", err)
	}
}

func TestInstallRefusesCanonicalSymlinkEvenWithForce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires symlink support")
	}
	skill, _ := skillcatalog.Get("revyl-cli-dev-loop")
	base := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "SKILL.md"), []byte("private"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, skill.Name)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := installSkillTo(base, skill, true); err == nil {
		t.Fatal("force followed an arbitrary skill symlink")
	}
	data, _ := os.ReadFile(filepath.Join(outside, "SKILL.md"))
	if string(data) != "private" {
		t.Fatal("outside file was changed")
	}
}

func TestSharedInstallDetectsConflictsInUnselectedAgents(t *testing.T) {
	resetSkillInstallFlags(t)
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	skill, _ := skillcatalog.Get("revyl-cli-dev-loop")
	legacy := filepath.Join(workDir, ".codex", "skills", skill.Name)
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := applySkillInstall(resolveDirectoriesForScope([]string{"cursor"}, false), []skillcatalog.Skill{skill}, true, false)
	if err == nil || !strings.Contains(err.Error(), "preserved") {
		t.Fatalf("unselected agent conflict was missed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workDir, ".agents")); !os.IsNotExist(err) {
		t.Fatalf("conflicting shared install wrote files: %v", err)
	}
}

func TestSkillInstallPreservesExistingPackageUnlessForced(t *testing.T) {
	skill, _ := skillcatalog.Get("revyl-cli-auth-bypass")
	base := t.TempDir()
	path, _, err := installSkillTo(base, skill, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("customized"), 0o644); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(filepath.Dir(path), "extra.txt")
	if err := os.WriteFile(extra, []byte("extra"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, wrote, err := installSkillTo(base, skill, false); err != nil || wrote {
		t.Fatalf("reinstall did not preserve customization: wrote=%v, err=%v", wrote, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "customized" {
		t.Fatalf("reinstall modified existing content: %q, %v", data, err)
	}
	if _, wrote, err := installSkillTo(base, skill, true); err != nil || !wrote {
		t.Fatalf("explicit force failed: wrote=%v, err=%v", wrote, err)
	}
	if err := validateManagedSkill(filepath.Dir(path), skill.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(extra); !os.IsNotExist(err) {
		t.Fatalf("force did not replace complete package: %v", err)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 1 || entries[0].Name() != skill.Name {
		t.Fatalf("installation leaked staging or backup directories: %v, %v", entries, err)
	}
}

func TestSkillUpdatePreservesPackageChanges(t *testing.T) {
	for _, mutation := range []string{"removed entrypoint", "removed metadata", "invalid record", "empty local directory", "file mode"} {
		t.Run(mutation, func(t *testing.T) {
			if mutation == "file mode" && runtime.GOOS == "windows" {
				t.Skip("Windows does not preserve POSIX executable bits")
			}
			resetSkillInstallFlags(t)
			workDir := t.TempDir()
			withWorkingDir(t, workDir)
			skill, _ := skillcatalog.Get("revyl-cli-auth-bypass")
			path, _, err := installSkillTo(filepath.Join(workDir, ".agents", "skills"), skill, false)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Dir(path)
			switch mutation {
			case "removed entrypoint":
				err = os.Remove(path)
			case "removed metadata":
				err = os.Remove(filepath.Join(dir, "agents", "openai.yaml"))
			case "invalid record":
				err = os.WriteFile(filepath.Join(dir, skillInstallStateFile), []byte("invalid"), 0o644)
			case "empty local directory":
				err = os.Mkdir(filepath.Join(dir, "custom"), 0o755)
			case "file mode":
				err = os.Chmod(path, 0o755)
			}
			if err != nil {
				t.Fatal(err)
			}
			skillUpdateJSON = true
			var output bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetOut(&output)
			if err := runSkillUpdate(cmd, nil); err == nil {
				t.Fatal("update accepted a modified package")
			}
			var result skillInstallResult
			if err := json.Unmarshal(output.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Skills) != 1 || result.Skills[0].Status != "preserved" || result.Skills[0].Reason == "" {
				t.Fatalf("update did not report preserved changes: %v", result)
			}
			if err := validateManagedSkill(dir, skill.Name); err == nil {
				t.Fatal("update silently restored a customized package")
			}
		})
	}
}

func TestSkillInstallAndUpdateRejectOutOfScopeSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires symlink support")
	}
	resetSkillInstallFlags(t)
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workDir, ".agents")); err != nil {
		t.Fatal(err)
	}
	skill, _ := skillcatalog.Get("revyl-cli-create")
	if _, err := applySkillInstall(resolveDirectoriesForScope([]string{"cursor"}, false), []skillcatalog.Skill{skill}, true, false); err == nil {
		t.Fatal("install followed an out-of-scope parent link")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("install modified outside scope: %v, %v", entries, err)
	}
	path, _, err := installSkillTo(filepath.Join(outside, "skills"), skill, false)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := runSkillUpdate(&cobra.Command{}, nil); err == nil {
		t.Fatal("update followed an out-of-scope parent link")
	}
	after, err := os.Stat(path)
	if err != nil || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("update rewrote outside scope: %v", err)
	}
}

func TestSkillPickerCancellationDoesNotWrite(t *testing.T) {
	resetSkillInstallFlags(t)
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	skillInputIsTTY = func() bool { return true }
	want := errors.New("cancelled")
	selectAgentSkills = func(string, []ui.SelectOption, []string) ([]string, error) { return nil, want }
	if err := installSelectedSkills(&cobra.Command{}, nil); !errors.Is(err, want) {
		t.Fatalf("cancellation was reported as success: %v", err)
	}
	entries, err := os.ReadDir(workDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cancelled selection wrote files: %v, %v", entries, err)
	}
}

func TestSkillUpdateGlobalDoesNotChangeProjectSelection(t *testing.T) {
	resetSkillInstallFlags(t)
	workDir, homeDir := t.TempDir(), t.TempDir()
	withWorkingDir(t, workDir)
	testutil.SetHomeDir(t, homeDir)
	projectSkill, _ := skillcatalog.Get("revyl-cli-create")
	globalSkill, _ := skillcatalog.Get("revyl-cli-analyze")
	projectPath, _, err := installSkillTo(filepath.Join(workDir, ".agents", "skills"), projectSkill, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath, []byte("local only"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := installSkillTo(filepath.Join(homeDir, ".agents", "skills"), globalSkill, false); err != nil {
		t.Fatal(err)
	}
	skillUpdateGlobal = true
	if err := runSkillUpdate(&cobra.Command{}, []string{globalSkill.Name}); err != nil {
		t.Fatal(err)
	}
	if err := runSkillUpdate(&cobra.Command{}, []string{projectSkill.Name}); err == nil {
		t.Fatal("global update accepted a project-only skill")
	}
	data, err := os.ReadFile(projectPath)
	if err != nil || string(data) != "local only" {
		t.Fatalf("global update modified project content: %q, %v", data, err)
	}
}

func writeLegacySkillPackage(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, skillcatalog.SkillFileName), []byte("customized"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInteractiveInstallKeepsConfirmedSharedStoragePlan(t *testing.T) {
	resetSkillInstallFlags(t)
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	testutil.SetHomeDir(t, t.TempDir())
	writeLegacySkillPackage(t, filepath.Join(workDir, ".cursor", "skills", "revyl-cli-dev-loop"))
	skillInputIsTTY = func() bool { return true }
	confirmSkillPlan = func(string, bool) (bool, error) { return true, nil }
	skillInstallAgents = []string{"cursor"}
	err := installSelectedSkills(&cobra.Command{}, []string{"revyl-cli-dev-loop", "revyl-cli-create"})
	if !errors.Is(err, errSharedSkillStorageUnavailable) {
		t.Fatalf("confirmed shared plan err=%v, want the shared storage blocker", err)
	}
	if _, statErr := os.Lstat(filepath.Join(workDir, ".cursor", "skills", "revyl-cli-create")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("installed outside the confirmed plan: %v", statErr)
	}
}

func TestLinkedSharedInstallNamesARemedyThatSucceeds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires symlink support")
	}
	resetSkillInstallFlags(t)
	workDir := t.TempDir()
	withWorkingDir(t, workDir)
	testutil.SetHomeDir(t, t.TempDir())
	skillInstallAgents = []string{"claude"}
	if err := installSelectedSkills(&cobra.Command{}, []string{"revyl-cli-dev-loop"}); err != nil {
		t.Fatal(err)
	}
	legacyNames := []string{"revyl-cli-atlas", "revyl-cli-create"}
	for _, name := range legacyNames {
		writeLegacySkillPackage(t, filepath.Join(".cursor", "skills", name))
	}

	err := installSelectedSkills(&cobra.Command{}, nil)
	if !errors.Is(err, errSharedSkillStorageUnavailable) {
		t.Fatalf("err=%v, want the shared storage blocker instead of a copy fallback", err)
	}
	if strings.Contains(err.Error(), "use --copy") {
		t.Fatalf("error suggests --copy, which cannot replace existing links: %v", err)
	}
	for _, name := range legacyNames {
		move := "mv " + filepath.Join(".cursor", "skills", name) + " " + filepath.Join(".cursor", name+".backup")
		if !strings.Contains(err.Error(), move) {
			t.Fatalf("error %q does not name %q", err, move)
		}
	}
	link, statErr := os.Lstat(filepath.Join(".claude", "skills", "revyl-cli-dev-loop"))
	if statErr != nil || link.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("existing Claude link changed: %v, %v", link, statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(".claude", "skills", "revyl-cli-create")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("blocked install wrote beside shared links: %v", statErr)
	}

	skillInstallCopy = true
	err = installSelectedSkills(&cobra.Command{}, nil)
	skillInstallCopy = false
	if err == nil || !strings.Contains(err.Error(), "rerun without --copy") {
		t.Fatalf("--copy over shared links err=%v, want a refusal naming the shared install", err)
	}
	if _, statErr := os.Lstat(filepath.Join(".claude", "skills", "revyl-cli-atlas")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("--copy wrote a partial installation before refusing: %v", statErr)
	}

	for _, name := range legacyNames {
		if err := os.Rename(filepath.Join(".cursor", "skills", name), filepath.Join(".cursor", name+".backup")); err != nil {
			t.Fatal(err)
		}
	}
	if err := installSelectedSkills(&cobra.Command{}, nil); err != nil {
		t.Fatalf("rerun after the named moves failed: %v", err)
	}
	for _, skill := range skillcatalog.DefaultInstall() {
		link, err := os.Lstat(filepath.Join(workDir, ".claude", "skills", skill.Name))
		if err != nil || link.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s is not linked to shared storage after the rerun: %v", skill.Name, err)
		}
	}
	for _, name := range legacyNames {
		if content, err := os.ReadFile(filepath.Join(".cursor", name+".backup", skillcatalog.SkillFileName)); err != nil || string(content) != "customized" {
			t.Fatalf("moved package %s changed: %q, %v", name, content, err)
		}
	}
}
