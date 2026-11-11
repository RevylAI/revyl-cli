// Package checkoutdefinitions reads the test, module and script files a run
// sends from a checkout of a project, and describes which definitions a run
// executed. The CLI, the MCP server and the TUI share it, so a run from a
// checkout behaves the same wherever it starts.
package checkoutdefinitions

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/buildselection"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/gitremote"
)

const (
	maxDefinitionFileBytes    = 1 << 20
	maxDefinitionRequestBytes = 10 << 20

	// GitTimeout bounds the Git commands run against a checkout.
	GitTimeout = 5 * time.Second
)

// ScriptRuntimeByFileExtension maps a script file's extension to its runtime.
var ScriptRuntimeByFileExtension = map[string]string{
	".py": "python",
	".js": "javascript",
	".ts": "typescript",
	".sh": "bash",
}

var gitCommitSHAPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

var fallbackReasonLabels = map[api.TestDefinitionFallbackReason]string{
	api.TestDefinitionFallbackReasonMissingGitBranch:               "branch not in Git",
	api.TestDefinitionFallbackReasonForkPullRequest:                "fork PR",
	api.TestDefinitionFallbackReasonMissingTestFile:                "no file at this commit",
	api.TestDefinitionFallbackReasonMissingTestFileOnDefaultBranch: "no file in Git",
}

// RunProject returns the project whose checkout files a run sends, or nil when
// it sends none. fromCheckout is the caller's explicit choice, nil when it made
// none; without one, run_test_definitions_from_git in the project's
// .revyl/config.yaml decides. An explicit choice to send files needs a project.
func RunProject(project *config.ProjectContext, fromCheckout *bool) (*config.ProjectContext, error) {
	if fromCheckout != nil {
		if !*fromCheckout {
			return nil, nil
		}
		if project == nil {
			return nil, errors.New("running this checkout's test files needs the checkout's .revyl/config.yaml")
		}
		return project, nil
	}
	if project == nil || project.Authored == nil || !project.Authored.RunTestDefinitionsFromGit {
		return nil, nil
	}
	return project, nil
}

// SavedCopiesNotice says that saving tests of a project that runs tests from
// Git only updates the saved copies a run falls back to. savedBy names what
// saved them, such as "this push".
func SavedCopiesNotice(project *config.ProjectContext, savedBy string) string {
	repository := "this repository"
	if reference, err := gitremote.ResolveRepository(project.WorktreeRoot); err == nil {
		repository = reference.Namespace + "/" + reference.Name
	}
	return fmt.Sprintf("Runs in %s use the test files in Git, so %s only updates the saved fallback copies.", repository, savedBy)
}

// Upload is what a run from a checkout sends, with the counts its definition
// summary reports.
type Upload struct {
	Files *api.TestDefinitionFiles
	// GitBranchName is the checkout's branch, empty when Git can't tell.
	GitBranchName    string
	ModuleFileCount  int
	ScriptFileCount  int
	SavedModuleCount int
	SavedScriptCount int
	// Warnings are problems the run shows before it starts, such as
	// definition files Git ignores.
	Warnings []string
}

// RequestFiles returns the files a run request carries; nil sends none.
func (u *Upload) RequestFiles() *api.TestDefinitionFiles {
	if u == nil {
		return nil
	}
	return u.Files
}

type checkoutTestFile struct {
	path string
	test *config.LocalTest
}

// DefinitionFolders maps module and script names to the checkout files that
// define them.
type DefinitionFolders struct {
	ModulePathByName map[string]string
	ScriptPathByName map[string]string
}

// CollectUpload gathers the test files linked to testIDs and every module and
// script file they name, checked against the folder layout and size limits
// before any request is sent. optOutInstruction tells the caller's user how to
// run without the checkout's files, such as "rerun with --from-checkout=false".
func CollectUpload(ctx context.Context, project *config.ProjectContext, testIDs []string, optOutInstruction string) (*Upload, error) {
	testFilesByRemoteID, err := readTestFilesByRemoteID(project, optOutInstruction)
	if err != nil {
		return nil, err
	}
	folders, err := ReadDefinitionFolders(project)
	if err != nil {
		return nil, err
	}

	upload := &Upload{}
	var paths []string
	moduleRefs, scriptRefs := map[string]bool{}, map[string]bool{}
	for _, testID := range slices.Compact(slices.Sorted(slices.Values(testIDs))) {
		testFiles := testFilesByRemoteID[testID]
		if len(testFiles) > 1 {
			return nil, fmt.Errorf("%s and %s both link test %s (duplicate_test_definition_file); keep one of them", ProjectRelativePath(project, testFiles[0].path), ProjectRelativePath(project, testFiles[1].path), testID)
		}
		if len(testFiles) == 1 {
			paths = append(paths, testFiles[0].path)
			CollectBlockReferences(testFiles[0].test.Test.Blocks, moduleRefs, scriptRefs)
		}
	}

	contentByPath := map[string][]byte{}
	for _, name := range slices.Sorted(maps.Keys(moduleRefs)) {
		path, ok := folders.ModulePathByName[name]
		if !ok {
			upload.SavedModuleCount++
			continue
		}
		content, err := ReadDefinitionFile(project, path)
		if err != nil {
			return nil, err
		}
		moduleScriptRefs, err := ModuleScriptReferences(project, path, content)
		if err != nil {
			return nil, err
		}
		maps.Copy(scriptRefs, moduleScriptRefs)
		contentByPath[path] = content
		paths = append(paths, path)
		upload.ModuleFileCount++
	}
	for _, name := range slices.Sorted(maps.Keys(scriptRefs)) {
		path, ok := folders.ScriptPathByName[name]
		if !ok {
			upload.SavedScriptCount++
			continue
		}
		paths = append(paths, path)
		upload.ScriptFileCount++
	}

	files := make([]api.TestDefinitionFile, 0, len(paths))
	for _, path := range paths {
		content, ok := contentByPath[path]
		if !ok {
			if content, err = ReadDefinitionFile(project, path); err != nil {
				return nil, err
			}
		}
		files = append(files, api.TestDefinitionFile{ProjectRelativePath: ProjectRelativePath(project, path), Content: string(content)})
	}

	gitCommitSHA, hasUncommittedChanges, warnings, err := readGitState(ctx, project, paths)
	if err != nil {
		return nil, err
	}
	upload.Warnings = warnings
	var projectID *string
	if id := strings.TrimSpace(project.Authored.Project.ID); id != "" {
		projectID = &id
	}
	upload.Files = &api.TestDefinitionFiles{
		ProjectID:                       projectID,
		GitCommitSHA:                    gitCommitSHA,
		HasUncommittedDefinitionChanges: hasUncommittedChanges,
		Files:                           files,
	}
	encoded, err := json.Marshal(upload.Files)
	if err != nil {
		return nil, fmt.Errorf("encode test definition files: %w", err)
	}
	if len(encoded) > maxDefinitionRequestBytes {
		return nil, fmt.Errorf("the run's %d test, module and script files total %d bytes, over the 10 MiB a run can send; %s to run without sending them", len(files), len(encoded), optOutInstruction)
	}
	if branch, err := buildselection.DetectGitBranch(ctx, buildselection.GitBranchOptions{WorkDir: project.ProjectRoot}); err == nil {
		upload.GitBranchName = branch.Name
	}
	return upload, nil
}

// readTestFilesByRemoteID reads every test file in the checkout and indexes
// the linked ones by _meta.remote_id.
func readTestFilesByRemoteID(project *config.ProjectContext, optOutInstruction string) (map[string][]checkoutTestFile, error) {
	filesByRemoteID := map[string][]checkoutTestFile{}
	for _, alias := range config.ListLocalTestAliases(project.TestsDir) {
		path := filepath.Join(project.TestsDir, alias+".yaml")
		test, err := config.LoadLocalTest(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w; fix the file or %s", ProjectRelativePath(project, path), err, optOutInstruction)
		}
		if remoteID := strings.TrimSpace(test.Meta.RemoteID); remoteID != "" {
			filesByRemoteID[remoteID] = append(filesByRemoteID[remoteID], checkoutTestFile{path: path, test: test})
		}
	}
	return filesByRemoteID, nil
}

// ReadDefinitionFolders reads the modules and scripts folders beside the
// project's config.yaml. A duplicate name, an invalid name or an extension
// outside the layout fails the whole read.
func ReadDefinitionFolders(project *config.ProjectContext) (DefinitionFolders, error) {
	definitionDir := filepath.Dir(project.ConfigPath)
	modulePathByName, err := readDefinitionFolder(project, filepath.Join(definitionDir, "modules"), []string{".yaml"})
	if err != nil {
		return DefinitionFolders{}, err
	}
	scriptPathByName, err := readDefinitionFolder(project, filepath.Join(definitionDir, "scripts"), slices.Sorted(maps.Keys(ScriptRuntimeByFileExtension)))
	if err != nil {
		return DefinitionFolders{}, err
	}
	return DefinitionFolders{ModulePathByName: modulePathByName, ScriptPathByName: scriptPathByName}, nil
}

// readDefinitionFolder maps each definition name in folder to its file.
// Subdirectories and hidden files such as .gitkeep aren't definitions.
func readDefinitionFolder(project *config.ProjectContext, folder string, extensions []string) (map[string]string, error) {
	pathByName := map[string]string{}
	entries, err := os.ReadDir(folder)
	if errors.Is(err, os.ErrNotExist) {
		return pathByName, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", ProjectRelativePath(project, folder), err)
	}
	for _, entry := range entries {
		fileName := entry.Name()
		if entry.IsDir() || strings.HasPrefix(fileName, ".") {
			continue
		}
		path := filepath.Join(folder, fileName)
		relativePath := ProjectRelativePath(project, path)
		extension := filepath.Ext(fileName)
		name := strings.TrimSuffix(fileName, extension)
		if !entry.Type().IsRegular() {
			return nil, fmt.Errorf("%s isn't a regular file; replace it with the file itself", relativePath)
		}
		if !slices.Contains(extensions, extension) {
			return nil, fmt.Errorf("%s: files in %s must end in %s", relativePath, ProjectRelativePath(project, folder), strings.Join(extensions, ", "))
		}
		if err := ValidateDefinitionFileStem(name); err != nil {
			return nil, fmt.Errorf("%s: %w", relativePath, err)
		}
		if otherPath, ok := pathByName[name]; ok {
			return nil, fmt.Errorf("%s and %s both define %q; keep one of them", ProjectRelativePath(project, otherPath), relativePath, name)
		}
		pathByName[name] = path
	}
	return pathByName, nil
}

// ValidateDefinitionFileStem checks that a file name's stem can be the exact
// module or script name it defines.
func ValidateDefinitionFileStem(name string) error {
	switch {
	case name == "":
		return errors.New("the file name has no module or script name before its extension")
	case !utf8.ValidString(name):
		return errors.New("the name isn't valid UTF-8")
	case strings.TrimSpace(name) != name:
		return errors.New("the name starts or ends with whitespace, which a module or script name can't")
	case strings.ContainsAny(name, "/\\"):
		return errors.New("the name contains a path separator")
	}
	return nil
}

// OpenProjectRoot opens the project root and returns path relative to it, so
// reads and writes through the root fail on a symlink that leaves the project.
func OpenProjectRoot(project *config.ProjectContext, path string) (*os.Root, string, error) {
	rootRelativePath, err := filepath.Rel(project.ProjectRoot, path)
	if err != nil {
		return nil, "", fmt.Errorf("locate %s in the project: %w", path, err)
	}
	projectRoot, err := os.OpenRoot(project.ProjectRoot)
	if err != nil {
		return nil, "", fmt.Errorf("open project %s: %w", project.ProjectRoot, err)
	}
	return projectRoot, rootRelativePath, nil
}

// ReadDefinitionFile reads one definition file in the project within the
// per-file limit as UTF-8 text.
func ReadDefinitionFile(project *config.ProjectContext, path string) ([]byte, error) {
	relativePath := ProjectRelativePath(project, path)
	projectRoot, rootRelativePath, err := OpenProjectRoot(project, path)
	if err != nil {
		return nil, err
	}
	defer projectRoot.Close()
	content, err := projectRoot.ReadFile(rootRelativePath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", relativePath, err)
	}
	if len(content) > maxDefinitionFileBytes {
		return nil, fmt.Errorf("%s is %d bytes; a test, module or script file can be at most 1 MiB", relativePath, len(content))
	}
	if !utf8.Valid(content) {
		return nil, fmt.Errorf("%s isn't UTF-8 text", relativePath)
	}
	return content, nil
}

// ModuleScriptReferences parses a module file and returns the scripts it
// names. A module file that imports a module fails, as saved modules do.
func ModuleScriptReferences(project *config.ProjectContext, path string, content []byte) (map[string]bool, error) {
	relativePath := ProjectRelativePath(project, path)
	var moduleFile struct {
		Blocks []config.TestBlock `yaml:"blocks"`
	}
	if err := yaml.Unmarshal(content, &moduleFile); err != nil {
		return nil, fmt.Errorf("%s: %w", relativePath, err)
	}
	moduleRefs, scriptRefs := map[string]bool{}, map[string]bool{}
	CollectBlockReferences(moduleFile.Blocks, moduleRefs, scriptRefs)
	if len(moduleRefs) > 0 {
		return nil, fmt.Errorf("%s imports module %q; a module can't import a module, so move those steps into the module or the tests using it", relativePath, slices.Sorted(maps.Keys(moduleRefs))[0])
	}
	return scriptRefs, nil
}

// CollectBlockReferences adds each module and script a block tree uses, by
// name or, for a block without a name, by ID.
func CollectBlockReferences(blocks []config.TestBlock, moduleRefs, scriptRefs map[string]bool) {
	for _, block := range blocks {
		if ref := cmp.Or(block.Module, block.ModuleID); block.Type == "module_import" && ref != "" {
			moduleRefs[ref] = true
		}
		if ref := cmp.Or(block.Script, block.ScriptID); block.Type == "code_execution" && ref != "" {
			scriptRefs[ref] = true
		}
		CollectBlockReferences(block.Then, moduleRefs, scriptRefs)
		CollectBlockReferences(block.Else, moduleRefs, scriptRefs)
		CollectBlockReferences(block.Body, moduleRefs, scriptRefs)
	}
}

// readGitState returns HEAD's commit, nil before the first commit; whether git
// status shows changes in the tests, modules or scripts folder or Git ignores a
// file the run sends; and the warnings about ignored files.
func readGitState(ctx context.Context, project *config.ProjectContext, uploadedPaths []string) (*string, bool, []string, error) {
	ctx, cancel := context.WithTimeout(ctx, GitTimeout)
	defer cancel()
	var gitCommitSHA *string
	if head, err := RunGit(ctx, project.ProjectRoot, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		if head = strings.TrimSpace(head); gitCommitSHAPattern.MatchString(head) {
			gitCommitSHA = &head
		}
	}
	definitionDir, err := filepath.Rel(project.ProjectRoot, filepath.Dir(project.ConfigPath))
	if err != nil {
		return nil, false, nil, fmt.Errorf("locate the .revyl folder: %w", err)
	}
	definitionPaths := []string{
		filepath.Join(definitionDir, "tests"), filepath.Join(definitionDir, "modules"), filepath.Join(definitionDir, "scripts")}
	status, err := RunGit(ctx, project.ProjectRoot,
		append([]string{"status", "--porcelain", "--untracked-files=all", "--"}, definitionPaths...)...)
	if err != nil {
		return nil, false, nil, fmt.Errorf("check the checkout's test definition folders for uncommitted changes: %w", err)
	}
	var ignoredPaths []string
	if project.Authored != nil && project.Authored.RunTestDefinitionsFromGit && len(uploadedPaths) > 0 {
		ignoredArgs := []string{"ls-files", "-z", "-o", "-i", "--exclude-standard", "--"}
		for _, uploadedPath := range uploadedPaths {
			relativePath, err := filepath.Rel(project.ProjectRoot, uploadedPath)
			if err != nil {
				return nil, false, nil, fmt.Errorf("locate %s in the project: %w", uploadedPath, err)
			}
			ignoredArgs = append(ignoredArgs, ":(literal)"+filepath.ToSlash(relativePath))
		}
		ignoredOutput, err := RunGit(ctx, project.ProjectRoot, ignoredArgs...)
		if err != nil {
			return nil, false, nil, fmt.Errorf("list ignored files in the checkout's test definition folders: %w", err)
		}
		ignoredPaths = ignoredDefinitionPathsFromGitLsFiles(ignoredOutput)
	}
	var warnings []string
	if len(ignoredPaths) > 0 {
		absolutePaths := make([]string, 0, len(ignoredPaths))
		for _, ignoredPath := range ignoredPaths {
			absolutePaths = append(absolutePaths, filepath.Join(project.ProjectRoot, filepath.FromSlash(ignoredPath)))
		}
		if warnings, err = IgnoredDefinitionFileWarnings(ctx, project, absolutePaths); err != nil {
			return nil, false, nil, err
		}
		warnings = append(warnings, fmt.Sprintf("Git ignores these definition files, so this checkout run has uncommitted definition changes: %s. Fix the ignore rule and run git add -A.", strings.Join(ignoredPaths, ", ")))
	}
	return gitCommitSHA, strings.TrimSpace(status) != "" || len(ignoredPaths) > 0, warnings, nil
}

func ignoredDefinitionPathsFromGitLsFiles(output string) []string {
	paths := make([]string, 0)
	for _, path := range strings.Split(output, "\x00") {
		if path == "" {
			continue
		}
		paths = append(paths, filepath.ToSlash(path))
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

// IgnoredDefinitionFileWarnings returns, for each of paths Git ignores, the
// ignore rule that matches it and how to undo it.
func IgnoredDefinitionFileWarnings(ctx context.Context, project *config.ProjectContext, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	relativePaths := make([]string, 0, len(paths))
	for _, path := range paths {
		relativePath, err := filepath.Rel(project.WorktreeRoot, path)
		if err != nil || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("locate %s in the Git worktree", ProjectRelativePath(project, path))
		}
		relativePaths = append(relativePaths, filepath.ToSlash(relativePath))
	}
	// #nosec G204 -- Git receives a fixed flag set and the worktree root as argv, and the paths on stdin, without a shell.
	command := exec.CommandContext(ctx, "git", "-C", project.WorktreeRoot, "check-ignore", "-z", "-v", "--stdin")
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	command.Stdin = strings.NewReader(strings.Join(relativePaths, "\x00") + "\x00")
	output, err := command.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("check whether the definition files are ignored: %w", err)
	}
	fields := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	if len(fields)%4 != 0 {
		return nil, errors.New("read the definition files' Git ignore rules: unexpected git check-ignore output")
	}
	var warnings []string
	for index := 0; index < len(fields); index += 4 {
		if strings.HasPrefix(fields[index+2], "!") {
			continue
		}
		sourceDirectory := filepath.Dir(filepath.FromSlash(fields[index]))
		definitionFolder, err := filepath.Rel(sourceDirectory, filepath.Dir(filepath.FromSlash(fields[index+3])))
		if err != nil || strings.HasPrefix(definitionFolder, ".."+string(filepath.Separator)) {
			definitionFolder = filepath.ToSlash(filepath.Dir(filepath.FromSlash(fields[index+3])))
		} else {
			definitionFolder = filepath.ToSlash(definitionFolder)
		}
		if definitionFolder == "." {
			definitionFolder = ""
		}
		warnings = append(warnings, fmt.Sprintf("Git ignores %s because of %s:%s:%s. Remove that rule, or add `!%s/` and `!%s/**` after it, then run git add -A.", fields[index+3], fields[index], fields[index+1], fields[index+2], definitionFolder, definitionFolder))
	}
	return warnings, nil
}

// RunGit runs a Git subcommand in dir without prompting and returns its output.
func RunGit(ctx context.Context, dir string, args ...string) (string, error) {
	// #nosec G204 -- Git receives its callers' fixed subcommands and repository-relative paths as argv, without a shell.
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	output, err := command.Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(exitErr.Stderr)))
	}
	if err != nil {
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return string(output), nil
}

// ProjectRelativePath returns path relative to the project root with forward
// slashes, or path itself when it isn't under the root.
func ProjectRelativePath(project *config.ProjectContext, path string) string {
	relativePath, err := filepath.Rel(project.ProjectRoot, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(relativePath)
}

// DefinitionLine says what a run executed. It reads like "Checkout @ 1a2b3c4 ·
// feature-x · uncommitted changes · 7 tests, 9 modules and 4 scripts from
// files · 2 modules saved · 1 test saved (no file in Git)", and like "2 tests
// from Git branch feature-x @ 1a2b3c4 · 1 test saved (no file in Git)" for a
// run that sent no files, which a nil upload is. A backend that doesn't say
// where it read Git leaves out the branch.
func (u *Upload) DefinitionLine(testCountBySource map[api.TestDefinitionSource]int, testCountByFallbackReason map[api.TestDefinitionFallbackReason]int, gitRead api.TestDefinitionGitRead) string {
	var parts []string
	if u != nil {
		checkout := "Checkout"
		if u.Files.GitCommitSHA != nil {
			checkout += " @ " + (*u.Files.GitCommitSHA)[:7]
		}
		parts = append(parts, checkout)
		if u.GitBranchName != "" {
			parts = append(parts, u.GitBranchName)
		}
		if u.Files.HasUncommittedDefinitionChanges {
			parts = append(parts, "uncommitted changes")
		}
		if fromFiles := joinCountPhrases(
			countPhrase(testCountBySource[api.TestDefinitionSourceCLIUpload], "test"),
			countPhrase(u.ModuleFileCount, "module"),
			countPhrase(u.ScriptFileCount, "script"),
		); fromFiles != "" {
			parts = append(parts, fromFiles+" from files")
		}
		if saved := joinCountPhrases(countPhrase(u.SavedModuleCount, "module"), countPhrase(u.SavedScriptCount, "script")); saved != "" {
			parts = append(parts, saved+" saved")
		}
	}
	gitReadReasons, savedTestReasons := map[api.TestDefinitionFallbackReason]int{}, map[api.TestDefinitionFallbackReason]int{}
	for reason, count := range testCountByFallbackReason {
		if reason == api.TestDefinitionFallbackReasonMissingTestFileOnDefaultBranch {
			savedTestReasons[reason] = count
		} else {
			gitReadReasons[reason] = count
		}
	}
	if count := testCountBySource[api.TestDefinitionSourceGitCommitRead]; count > 0 {
		fromGit := countPhrase(count, "test") + " from Git"
		if gitRead.TestDefinitionGitBranchName != "" {
			fromGit += " branch " + gitRead.TestDefinitionGitBranchName
			if sha := gitRead.TestDefinitionGitCommitSHA; len(sha) >= 7 {
				fromGit += " @ " + sha[:7]
			}
		}
		parts = append(parts, fromGit+fallbackReasonsSuffix(gitReadReasons, count, gitRead.TestDefinitionMissingGitBranchName))
	}
	if count := testCountBySource[api.TestDefinitionSourceSavedTest]; count > 0 {
		parts = append(parts, countPhrase(count, "test")+" saved"+fallbackReasonsSuffix(savedTestReasons, count, ""))
	}
	return strings.Join(parts, " · ")
}

// fallbackReasonsSuffix names missingGitBranchName, when known, in the
// missing_git_branch label.
func fallbackReasonsSuffix(testCountByReason map[api.TestDefinitionFallbackReason]int, testCount int, missingGitBranchName string) string {
	reasons := slices.Sorted(maps.Keys(testCountByReason))
	switch {
	case len(reasons) == 0:
		return ""
	case len(reasons) == 1 && testCountByReason[reasons[0]] == testCount:
		return " (" + fallbackReasonLabel(reasons[0], missingGitBranchName) + ")"
	}
	phrases := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		phrases = append(phrases, fmt.Sprintf("%d %s", testCountByReason[reason], fallbackReasonLabel(reason, missingGitBranchName)))
	}
	return " (" + strings.Join(phrases, ", ") + ")"
}

func fallbackReasonLabel(reason api.TestDefinitionFallbackReason, missingGitBranchName string) string {
	if reason == api.TestDefinitionFallbackReasonMissingGitBranch && missingGitBranchName != "" {
		return missingGitBranchName + " not in Git"
	}
	return cmp.Or(fallbackReasonLabels[reason], string(reason))
}

func countPhrase(count int, noun string) string {
	switch count {
	case 0:
		return ""
	case 1:
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

func joinCountPhrases(phrases ...string) string {
	phrases = slices.DeleteFunc(phrases, func(phrase string) bool { return phrase == "" })
	if len(phrases) <= 1 {
		return strings.Join(phrases, "")
	}
	return strings.Join(phrases[:len(phrases)-1], ", ") + " and " + phrases[len(phrases)-1]
}
