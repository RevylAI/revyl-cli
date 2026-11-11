package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/checkoutdefinitions"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/sync"
	"github.com/revyl/cli/internal/ui"
)

const savedScriptsPageSize = 1000

const revylDefinitionGitignoreContent = `# Most files in .revyl are local runtime state.
# Only the shared project config and test definitions belong in git.
*

# Keep this file itself.
!.gitignore

# Shared Revyl project files.
!config.yaml
!tests/
!tests/**
!modules/
!modules/**
!scripts/
!scripts/**
`

const legacyRevylDefinitionGitignoreContent = `# Most files in .revyl are local runtime state.
# Only the shared project config and test definitions belong in git.
*

# Keep this file itself.
!.gitignore

# Shared Revyl project files.
!config.yaml
!tests/
!tests/**
`

// moduleOrScriptFileWrite is a module or script file a push saves, as a new
// saved copy or, with savedID, as a new version of the saved copy it differs
// from.
type moduleOrScriptFileWrite struct {
	name                     string
	path                     string
	savedID                  string
	savedModuleVersion       int
	script                   *api.CLICreateScriptRequest
	module                   *api.CLICreateModuleRequest
	otherSavedTestNamesUsing []string
}

// moduleAndScriptFilePushPlan is what a push saves from the module and script
// files its tests use before it writes any test, and the files that differ from
// their saved copy that it leaves alone without --include-modules-and-scripts.
type moduleAndScriptFilePushPlan struct {
	project        *config.ProjectContext
	scriptWrites   []moduleOrScriptFileWrite
	moduleWrites   []moduleOrScriptFileWrite
	skippedScripts []moduleOrScriptFileWrite
	skippedModules []moduleOrScriptFileWrite
}

// planModuleAndScriptFilePush finds the modules the pushed tests use, and the
// scripts those tests and modules use, that have a file in the checkout. It
// plans to create each one with no saved copy and, when updatesSavedCopies is
// set, to update each saved copy its file differs from, naming the other saved
// tests that use it.
func planModuleAndScriptFilePush(ctx context.Context, client *api.Client, project *config.ProjectContext, localTests map[string]*config.LocalTest, pushedTestNames []string, updatesSavedCopies bool) (*moduleAndScriptFilePushPlan, error) {
	plan := &moduleAndScriptFilePushPlan{project: project}
	folders, err := checkoutdefinitions.ReadDefinitionFolders(project)
	if err != nil {
		return nil, err
	}
	moduleRefs, scriptRefs := map[string]bool{}, map[string]bool{}
	pushedTestIDs := map[string]bool{}
	for _, name := range pushedTestNames {
		if test := localTests[name]; test != nil {
			checkoutdefinitions.CollectBlockReferences(test.Test.Blocks, moduleRefs, scriptRefs)
			pushedTestIDs[strings.TrimSpace(test.Meta.RemoteID)] = true
		}
	}
	usesAFile := false
	for name := range moduleRefs {
		_, hasFile := folders.ModulePathByName[name]
		usesAFile = usesAFile || hasFile
	}
	for name := range scriptRefs {
		_, hasFile := folders.ScriptPathByName[name]
		usesAFile = usesAFile || hasFile
	}
	if !usesAFile {
		return plan, nil
	}

	savedModules, err := client.ListModules(ctx)
	if err != nil {
		return nil, fmt.Errorf("list saved modules: %w", err)
	}
	savedModuleByName, moduleNameByID := map[string]api.CLIModuleResponse{}, map[string]string{}
	for _, module := range savedModules.Result {
		savedModuleByName[strings.TrimSpace(module.Name)] = module
		moduleNameByID[module.ID] = module.Name
	}
	savedScripts, err := listAllSavedScripts(ctx, client)
	if err != nil {
		return nil, err
	}
	savedScriptByName, scriptNameByID := map[string]api.CLIScriptInfo{}, map[string]string{}
	for _, script := range savedScripts {
		savedScriptByName[strings.TrimSpace(script.Name)] = script
		scriptNameByID[script.ID] = script.Name
	}

	for _, name := range slices.Sorted(maps.Keys(moduleRefs)) {
		path, ok := folders.ModulePathByName[name]
		if !ok {
			continue
		}
		relativePath := checkoutdefinitions.ProjectRelativePath(project, path)
		content, err := checkoutdefinitions.ReadDefinitionFile(project, path)
		if err != nil {
			return nil, err
		}
		moduleScriptRefs, err := checkoutdefinitions.ModuleScriptReferences(project, path, content)
		if err != nil {
			return nil, err
		}
		maps.Copy(scriptRefs, moduleScriptRefs)
		var moduleFile moduleBlocksFile
		if err := yaml.Unmarshal(content, &moduleFile); err != nil {
			return nil, fmt.Errorf("%s: %w", relativePath, err)
		}
		if len(moduleFile.Blocks) == 0 {
			return nil, fmt.Errorf("%s has no blocks; a module file needs a top-level blocks: list", relativePath)
		}
		write := moduleOrScriptFileWrite{name: name, path: path, module: &api.CLICreateModuleRequest{Name: name, Blocks: moduleFile.Blocks}}
		if saved, ok := savedModuleByName[name]; ok {
			var authoredModuleFile struct {
				Blocks []config.TestBlock `yaml:"blocks"`
			}
			if err := yaml.Unmarshal(content, &authoredModuleFile); err != nil {
				return nil, fmt.Errorf("%s: %w", relativePath, err)
			}
			fileBlocks, err := sync.AuthoredBlocks(authoredModuleFile.Blocks, scriptNameByID, moduleNameByID)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", relativePath, err)
			}
			savedBlocks, err := sync.AuthoredBlocks(saved.Blocks, scriptNameByID, moduleNameByID)
			if err != nil {
				return nil, fmt.Errorf("read saved module %s: %w", name, err)
			}
			if reflect.DeepEqual(fileBlocks, savedBlocks) {
				continue
			}
			if !updatesSavedCopies {
				plan.skippedModules = append(plan.skippedModules, write)
				continue
			}
			usage, err := client.GetModuleUsage(ctx, saved.ID)
			if err != nil {
				return nil, fmt.Errorf("list the saved tests that use module %s: %w", name, err)
			}
			testNameByID := map[string]string{}
			for _, test := range usage.Tests {
				testNameByID[test.Id] = test.Name
			}
			write.savedID, write.savedModuleVersion = saved.ID, saved.Version
			write.otherSavedTestNamesUsing = otherSavedTestNames(testNameByID, pushedTestIDs)
		}
		plan.moduleWrites = append(plan.moduleWrites, write)
	}

	for _, name := range slices.Sorted(maps.Keys(scriptRefs)) {
		path, ok := folders.ScriptPathByName[name]
		if !ok {
			continue
		}
		content, err := checkoutdefinitions.ReadDefinitionFile(project, path)
		if err != nil {
			return nil, err
		}
		runtime := checkoutdefinitions.ScriptRuntimeByFileExtension[filepath.Ext(path)]
		write := moduleOrScriptFileWrite{name: name, path: path, script: &api.CLICreateScriptRequest{Name: name, Code: string(content), Runtime: runtime}}
		if saved, ok := savedScriptByName[name]; ok {
			if saved.Code == string(content) && saved.Runtime == runtime {
				continue
			}
			if !updatesSavedCopies {
				plan.skippedScripts = append(plan.skippedScripts, write)
				continue
			}
			usage, err := client.GetScriptUsage(ctx, saved.ID)
			if err != nil {
				return nil, fmt.Errorf("list the saved tests that use script %s: %w", name, err)
			}
			testNameByID := map[string]string{}
			for _, test := range usage.Tests {
				testNameByID[test.ID] = test.Name
			}
			for _, module := range usage.Modules {
				moduleUsage, err := client.GetModuleUsage(ctx, module.ID)
				if err != nil {
					return nil, fmt.Errorf("list the saved tests that use script %s through module %s: %w", name, module.Name, err)
				}
				for _, test := range moduleUsage.Tests {
					testNameByID[test.Id] = test.Name
				}
			}
			write.savedID = saved.ID
			write.otherSavedTestNamesUsing = otherSavedTestNames(testNameByID, pushedTestIDs)
		}
		plan.scriptWrites = append(plan.scriptWrites, write)
	}
	return plan, nil
}

// otherSavedTestNames returns the sorted names of the tests that aren't among
// the pushed ones.
func otherSavedTestNames(testNameByID map[string]string, pushedTestIDs map[string]bool) []string {
	var names []string
	for id, name := range testNameByID {
		if !pushedTestIDs[id] {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// printModuleAndScriptFilePushPlan lists what a dry-run push would save from
// module and script files and what it would leave alone.
func printModuleAndScriptFilePushPlan(plan *moduleAndScriptFilePushPlan) {
	if len(plan.scriptWrites)+len(plan.moduleWrites)+len(plan.skippedScripts)+len(plan.skippedModules) == 0 {
		return
	}
	ui.Println()
	ui.PrintInfo("Scripts and modules this push would save from files first:")
	for _, write := range plan.scriptWrites {
		if write.savedID == "" {
			ui.PrintInfo("  create script %s from %s", write.name, checkoutdefinitions.ProjectRelativePath(plan.project, write.path))
		} else {
			ui.PrintInfo("  update script %s from %s", write.name, checkoutdefinitions.ProjectRelativePath(plan.project, write.path))
		}
		printOtherSavedTestsUsing("script", write)
	}
	for _, write := range plan.moduleWrites {
		if write.savedID == "" {
			ui.PrintInfo("  create module %s from %s", write.name, checkoutdefinitions.ProjectRelativePath(plan.project, write.path))
		} else {
			ui.PrintInfo("  update module %s from %s (saved copy is v%d)", write.name, checkoutdefinitions.ProjectRelativePath(plan.project, write.path), write.savedModuleVersion)
		}
		printOtherSavedTestsUsing("module", write)
	}
	for _, write := range plan.skippedScripts {
		ui.PrintDim("  skip script %s (%s differs from the saved copy; --include-modules-and-scripts updates it)", write.name, checkoutdefinitions.ProjectRelativePath(plan.project, write.path))
	}
	for _, write := range plan.skippedModules {
		ui.PrintDim("  skip module %s (%s differs from the saved copy; --include-modules-and-scripts updates it)", write.name, checkoutdefinitions.ProjectRelativePath(plan.project, write.path))
	}
}

// printOtherSavedTestsUsing names the saved tests outside the push that run an
// updated module or script.
func printOtherSavedTestsUsing(kind string, write moduleOrScriptFileWrite) {
	if len(write.otherSavedTestNamesUsing) > 0 {
		ui.PrintWarning("Other saved tests use %s %s too: %s", kind, write.name, strings.Join(write.otherSavedTestNamesUsing, ", "))
	}
}

// saveModuleAndScriptFiles creates and updates the plan's scripts, then its
// modules, and stops at the first failure, naming what it already saved.
func saveModuleAndScriptFiles(ctx context.Context, client *api.Client, plan *moduleAndScriptFilePushPlan) error {
	var createdScriptCount, createdModuleCount int
	defer func() {
		analytics.SetCommandCompletion(ctx, analytics.CommandCompletion{Properties: map[string]interface{}{
			"created_script_count": createdScriptCount,
			"skipped_script_count": len(plan.skippedScripts),
			"created_module_count": createdModuleCount,
			"skipped_module_count": len(plan.skippedModules),
		}})
	}()
	for _, write := range plan.skippedScripts {
		ui.PrintWarning("Skipped script %s: %s differs from the saved copy; add --include-modules-and-scripts to update it", write.name, checkoutdefinitions.ProjectRelativePath(plan.project, write.path))
	}
	for _, write := range plan.skippedModules {
		ui.PrintWarning("Skipped module %s: %s differs from the saved copy; add --include-modules-and-scripts to update it", write.name, checkoutdefinitions.ProjectRelativePath(plan.project, write.path))
	}
	var saved []string
	writeFailed := func(kind string, write moduleOrScriptFileWrite, err error) error {
		action := "create"
		if write.savedID != "" {
			action = "update"
		}
		savedSoFar := "nothing"
		if len(saved) > 0 {
			savedSoFar = strings.Join(saved, ", ")
		}
		return analytics.WithSafeDiagnostic(
			fmt.Errorf("%s %s %q from %s: %w; no test was pushed, and before the failure the push saved %s, which a rerun skips", action, kind, write.name, checkoutdefinitions.ProjectRelativePath(plan.project, write.path), err, savedSoFar),
			action+" "+kind+" from file failed",
		)
	}
	for _, write := range plan.scriptWrites {
		relativePath := checkoutdefinitions.ProjectRelativePath(plan.project, write.path)
		if write.savedID == "" {
			if _, err := client.CreateScript(ctx, write.script); err != nil {
				return writeFailed("script", write, err)
			}
			createdScriptCount++
			saved = append(saved, "created script "+write.name)
			ui.PrintSuccess("Created script %s from %s", write.name, relativePath)
			continue
		}
		if _, err := client.UpdateScript(ctx, write.savedID, &api.CLIUpdateScriptRequest{Code: &write.script.Code, Runtime: &write.script.Runtime}); err != nil {
			return writeFailed("script", write, err)
		}
		saved = append(saved, "updated script "+write.name)
		ui.PrintSuccess("Updated script %s from %s", write.name, relativePath)
		printOtherSavedTestsUsing("script", write)
	}
	for _, write := range plan.moduleWrites {
		relativePath := checkoutdefinitions.ProjectRelativePath(plan.project, write.path)
		if write.savedID == "" {
			response, err := client.CreateModule(ctx, write.module)
			if err != nil {
				return writeFailed("module", write, err)
			}
			createdModuleCount++
			saved = append(saved, "created module "+write.name)
			ui.PrintSuccess("Created module %s from %s as v%d", write.name, relativePath, response.Result.Version)
			continue
		}
		response, err := client.UpdateModule(ctx, write.savedID, &api.CLIUpdateModuleRequest{Blocks: &write.module.Blocks, ExpectedVersion: &write.savedModuleVersion})
		if err != nil {
			return writeFailed("module", write, err)
		}
		saved = append(saved, "updated module "+write.name)
		ui.PrintSuccess("Updated module %s from %s to v%d", write.name, relativePath, response.Result.Version)
		printOtherSavedTestsUsing("module", write)
	}
	return nil
}

// pushLocalTests saves the plan's scripts and modules, then validates and
// pushes the tests the way revyl test push does.
func pushLocalTests(cmd *cobra.Command, client *api.Client, project *config.ProjectContext, resolver *sync.Resolver, plan *moduleAndScriptFilePushPlan, testName string, filesToValidate []string, force bool) error {
	if err := saveModuleAndScriptFiles(cmd.Context(), client, plan); err != nil {
		ui.PrintError("%v", err)
		return err
	}
	if err := validateYAMLFilesWithBackend(cmd, filesToValidate); err != nil {
		return err
	}

	ui.StartSpinner("Pushing tests...")
	results, err := resolver.SyncToRemote(cmd.Context(), testName, project.TestsDir, force)
	ui.StopSpinner()
	if err != nil {
		ui.PrintError("Push failed: %v", err)
		return err
	}

	ui.Println()
	var failures []error
	for _, r := range results {
		if r.Error != nil {
			ui.PrintError("%s: %v", r.Name, r.Error)
			failures = append(failures, fmt.Errorf("%s: %w", r.Name, r.Error))
		} else if r.Conflict {
			ui.PrintWarning("%s: conflict detected (use --force to overwrite)", r.Name)
			failures = append(failures, fmt.Errorf("%s: conflict detected (use --force to overwrite)", r.Name))
		} else {
			ui.PrintSuccess("%s: pushed to v%d", r.Name, r.NewVersion)
			if r.TagSyncError != nil {
				ui.PrintWarning("%s: tags failed to sync: %v", r.Name, r.TagSyncError)
			}
		}
	}
	return errors.Join(failures...)
}

// savedDefinitionsUsedBy is what the modules and scripts a set of tests
// reference resolve to in Revyl: the saved module or script behind each
// reference by name or ID, and the name of each saved module and script by
// ID, for authoring their files.
type savedDefinitionsUsedBy struct {
	moduleRefs, scriptRefs         map[string]bool
	savedModuleByRef               map[string]api.CLIModuleResponse
	savedScriptByRef               map[string]api.CLIScriptInfo
	moduleNameByID, scriptNameByID map[string]string
}

// listSavedDefinitionsUsedBy resolves the module and script references a set
// of test blocks make against the saved modules and scripts, and adds the
// scripts the referenced saved modules use, so the result names every file a
// run of those tests from Git reads.
func listSavedDefinitionsUsedBy(ctx context.Context, client *api.Client, moduleRefs, scriptRefs map[string]bool) (savedDefinitionsUsedBy, error) {
	used := savedDefinitionsUsedBy{
		moduleRefs:       moduleRefs,
		scriptRefs:       scriptRefs,
		savedModuleByRef: map[string]api.CLIModuleResponse{},
		savedScriptByRef: map[string]api.CLIScriptInfo{},
		moduleNameByID:   map[string]string{},
		scriptNameByID:   map[string]string{},
	}
	ui.StartSpinner("Fetching the modules and scripts the tests use...")
	savedModules, err := client.ListModules(ctx)
	if err != nil {
		ui.StopSpinner()
		return used, fmt.Errorf("list saved modules: %w", err)
	}
	savedScripts, err := listAllSavedScripts(ctx, client)
	ui.StopSpinner()
	if err != nil {
		return used, err
	}
	for _, module := range savedModules.Result {
		used.savedModuleByRef[strings.TrimSpace(module.Name)], used.savedModuleByRef[module.ID] = module, module
		used.moduleNameByID[module.ID] = module.Name
	}
	for _, script := range savedScripts {
		used.savedScriptByRef[strings.TrimSpace(script.Name)], used.savedScriptByRef[script.ID] = script, script
		used.scriptNameByID[script.ID] = script.Name
	}
	for ref := range moduleRefs {
		module, saved := used.savedModuleByRef[ref]
		if !saved {
			continue
		}
		if blocks, err := sync.AuthoredBlocks(module.Blocks, used.scriptNameByID, used.moduleNameByID); err == nil {
			checkoutdefinitions.CollectBlockReferences(blocks, map[string]bool{}, scriptRefs)
		}
	}
	return used, nil
}

// warnAboutUnsavedDefinition reports a module or script a test names that is
// saved nowhere and has no file, because runs of that test fail without it.
func warnAboutUnsavedDefinition(kind, ref string, pathByName map[string]string) {
	if _, hasFile := pathByName[ref]; !hasFile {
		ui.PrintWarning("%s %s: not saved in Revyl and no file in .revyl/%ss, so runs of the tests that use it fail", kind, ref, kind)
	}
}

// pullModuleAndScriptFilesUsedBy writes the saved modules the test files at
// testPaths use, and the scripts those tests and modules use, to the
// checkout's definition folders, the files a run from Git reads, and leaves
// every other saved module and script alone.
func pullModuleAndScriptFilesUsedBy(ctx context.Context, client *api.Client, project *config.ProjectContext, testPaths []string, force bool) error {
	moduleRefs, scriptRefs := map[string]bool{}, map[string]bool{}
	for _, path := range testPaths {
		test, err := config.LoadLocalTest(path)
		if err != nil {
			return fmt.Errorf("%s: %w", checkoutdefinitions.ProjectRelativePath(project, path), err)
		}
		checkoutdefinitions.CollectBlockReferences(test.Test.Blocks, moduleRefs, scriptRefs)
	}
	if len(moduleRefs) == 0 && len(scriptRefs) == 0 {
		return nil
	}
	folders, err := checkoutdefinitions.ReadDefinitionFolders(project)
	if err != nil {
		return err
	}
	used, err := listSavedDefinitionsUsedBy(ctx, client, moduleRefs, scriptRefs)
	if err != nil {
		return err
	}

	ui.Println()
	var failures []error
	for _, ref := range slices.Sorted(maps.Keys(used.moduleRefs)) {
		module, saved := used.savedModuleByRef[ref]
		if !saved {
			warnAboutUnsavedDefinition("module", ref, folders.ModulePathByName)
			continue
		}
		if err := pullModuleFile(project, module, used.scriptNameByID, used.moduleNameByID, force); err != nil {
			failures = append(failures, err)
		}
	}
	for _, ref := range slices.Sorted(maps.Keys(used.scriptRefs)) {
		script, saved := used.savedScriptByRef[ref]
		if !saved {
			warnAboutUnsavedDefinition("script", ref, folders.ScriptPathByName)
			continue
		}
		if err := pullScriptFile(project, script, force); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// printModuleAndScriptFilesPullWouldWrite lists, for a dry run, the saved
// modules and scripts a pull with --include-modules-and-scripts would write
// for tests whose blocks after the pull are blocksByTest: the saved test's
// blocks for a test the pull writes, and the file's blocks for one it keeps.
func printModuleAndScriptFilesPullWouldWrite(ctx context.Context, client *api.Client, project *config.ProjectContext, blocksByTest map[string][]config.TestBlock) error {
	moduleRefs, scriptRefs := map[string]bool{}, map[string]bool{}
	for _, blocks := range blocksByTest {
		checkoutdefinitions.CollectBlockReferences(blocks, moduleRefs, scriptRefs)
	}
	if len(moduleRefs) == 0 && len(scriptRefs) == 0 {
		return nil
	}
	folders, err := checkoutdefinitions.ReadDefinitionFolders(project)
	if err != nil {
		return err
	}
	used, err := listSavedDefinitionsUsedBy(ctx, client, moduleRefs, scriptRefs)
	if err != nil {
		return err
	}
	ui.Println()
	ui.PrintInfo("Modules and scripts the tests use, which the pull would also write:")
	for _, ref := range slices.Sorted(maps.Keys(used.moduleRefs)) {
		if module, saved := used.savedModuleByRef[ref]; saved {
			ui.PrintInfo("  module %s", module.Name)
		} else {
			warnAboutUnsavedDefinition("module", ref, folders.ModulePathByName)
		}
	}
	for _, ref := range slices.Sorted(maps.Keys(used.scriptRefs)) {
		if script, saved := used.savedScriptByRef[ref]; saved {
			ui.PrintInfo("  script %s", script.Name)
		} else {
			warnAboutUnsavedDefinition("script", ref, folders.ScriptPathByName)
		}
	}
	return nil
}

// listAllSavedScripts pages through every saved script in the organization.
func listAllSavedScripts(ctx context.Context, client *api.Client) ([]api.CLIScriptInfo, error) {
	var scripts []api.CLIScriptInfo
	for {
		page, err := client.ListScripts(ctx, "", savedScriptsPageSize, len(scripts))
		if err != nil {
			return nil, fmt.Errorf("list saved scripts: %w", err)
		}
		scripts = append(scripts, page.Scripts...)
		if len(page.Scripts) < savedScriptsPageSize || len(scripts) >= page.Count {
			return scripts, nil
		}
	}
}

// pullableDefinitionName reports whether a saved module or script name can be
// a file name in the checkout's definition folders.
func pullableDefinitionName(name string) bool {
	return !strings.HasPrefix(name, ".") && checkoutdefinitions.ValidateDefinitionFileStem(name) == nil
}

// pullDefinitionFile writes a saved module or script to its file. A file with
// different content is kept unless force is set, like revyl test pull.
func pullDefinitionFile(project *config.ProjectContext, kind, name, path string, content []byte, force bool) error {
	if err := ensureDefinitionFilesAreGitVisible(project, path); err != nil {
		return err
	}
	relativePath := checkoutdefinitions.ProjectRelativePath(project, path)
	projectRoot, rootRelativePath, err := checkoutdefinitions.OpenProjectRoot(project, path)
	if err != nil {
		return err
	}
	defer projectRoot.Close()
	existing, err := projectRoot.ReadFile(rootRelativePath)
	switch {
	case err == nil && bytes.Equal(existing, content):
		ui.PrintDim("%s %s: %s is up to date", kind, name, relativePath)
		return nil
	case err == nil && !force:
		ui.PrintWarning("%s %s: %s has local changes that would be overwritten (use --force)", kind, name, relativePath)
		return nil
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("read %s: %w", relativePath, err)
	}
	if err := projectRoot.MkdirAll(filepath.Dir(rootRelativePath), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", checkoutdefinitions.ProjectRelativePath(project, filepath.Dir(path)), err)
	}
	if err := projectRoot.WriteFile(rootRelativePath, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", relativePath, err)
	}
	ui.PrintSuccess("%s %s: wrote %s", kind, name, relativePath)
	return nil
}

func ensureDefinitionFilesAreGitVisible(project *config.ProjectContext, paths ...string) error {
	if project == nil || project.Authored == nil || !project.Authored.RunTestDefinitionsFromGit {
		return nil
	}
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, checkoutdefinitions.GitTimeout)
	defer cancel()
	if _, err := checkoutdefinitions.RunGit(ctx, project.ProjectRoot, "rev-parse", "--show-toplevel"); err != nil {
		return nil
	}

	gitignorePath := filepath.Join(filepath.Dir(project.ConfigPath), ".gitignore")
	projectRoot, rootRelativePath, err := checkoutdefinitions.OpenProjectRoot(project, gitignorePath)
	if err != nil {
		return err
	}
	defer projectRoot.Close()
	metadata, err := projectRoot.Lstat(rootRelativePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", checkoutdefinitions.ProjectRelativePath(project, gitignorePath), err)
	}
	if err == nil && metadata.Mode().IsRegular() {
		content, err := projectRoot.ReadFile(rootRelativePath)
		if err != nil {
			return fmt.Errorf("read %s: %w", checkoutdefinitions.ProjectRelativePath(project, gitignorePath), err)
		}
		if strings.ReplaceAll(string(content), "\r\n", "\n") == legacyRevylDefinitionGitignoreContent {
			if err := projectRoot.WriteFile(rootRelativePath, []byte(revylDefinitionGitignoreContent), metadata.Mode().Perm()); err != nil {
				return fmt.Errorf("update %s: %w", checkoutdefinitions.ProjectRelativePath(project, gitignorePath), err)
			}
			ui.PrintInfo("Updated %s so modules and scripts can be committed with git add -A.", checkoutdefinitions.ProjectRelativePath(project, gitignorePath))
		}
	}
	if len(paths) == 0 {
		definitionDirectory := filepath.Dir(project.ConfigPath)
		paths = []string{
			filepath.Join(definitionDirectory, "tests"),
			filepath.Join(definitionDirectory, "modules"),
			filepath.Join(definitionDirectory, "scripts"),
		}
	}
	warnings, err := checkoutdefinitions.IgnoredDefinitionFileWarnings(ctx, project, paths)
	if err != nil {
		return err
	}
	for _, warning := range warnings {
		ui.PrintWarning("%s", warning)
	}
	return nil
}
