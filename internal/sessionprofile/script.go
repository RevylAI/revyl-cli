package sessionprofile

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/checkoutdefinitions"
)

// ScriptSource says where a profile's script came from, using the values of
// the session_profile_script_source analytics property.
type ScriptSource string

const (
	ScriptSourceRepositoryPath   ScriptSource = "repository_path"
	ScriptSourceRepositoryScript ScriptSource = "repository_script"
	ScriptSourceLibraryScript    ScriptSource = "library_script"
)

// interpreterByRuntime is the command a Script step runs each runtime with.
var interpreterByRuntime = map[string][]string{
	"bash":       {"bash"},
	"python":     {"python3"},
	"javascript": {"node"},
	"typescript": {"node", "--disable-warning=ExperimentalWarning"},
}

// ScriptLibrary reads the organization's saved Library Scripts.
type ScriptLibrary interface {
	ListAllScripts(ctx context.Context) ([]api.CLIScriptInfo, error)
	GetScript(ctx context.Context, scriptID string) (*api.CLIScriptInfo, error)
}

// profileScript is a resolved profile script. A repository path runs as an
// executable (runtime is empty); other scripts run with their runtime, and a
// Library Script's code is written to a private file before the run.
type profileScript struct {
	source  ScriptSource
	path    string
	runtime string
	code    string
}

// resolveScript finds the script a profile names: a value containing "/" is a
// repository path from the project root; any other value is a script name
// looked up in .revyl/scripts and then in the Library.
func resolveScript(ctx context.Context, library ScriptLibrary, projectRoot, configPath, ref string) (profileScript, error) {
	if strings.Contains(ref, "/") {
		path, err := resolveRepositoryPath(projectRoot, ref)
		if err != nil {
			return profileScript{}, err
		}
		return profileScript{source: ScriptSourceRepositoryPath, path: path}, nil
	}
	if checkoutdefinitions.ValidateDefinitionFileStem(ref) == nil {
		script, found, err := findRepositoryScript(filepath.Join(filepath.Dir(configPath), "scripts"), ref)
		if err != nil || found {
			return script, err
		}
	}
	return fetchLibraryScript(ctx, library, ref)
}

func resolveRepositoryPath(projectRoot, ref string) (string, error) {
	unavailable := func(format string, args ...any) error {
		return &Error{Outcome: OutcomeScriptUnavailable, Reason: fmt.Sprintf(format, args...)}
	}
	realProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return "", unavailable("could not resolve the project root for script %s", ref)
	}
	repositoryRoot := repositoryRootOf(realProjectRoot)
	realScript, err := filepath.EvalSymlinks(filepath.Join(realProjectRoot, filepath.FromSlash(ref)))
	if err != nil {
		return "", unavailable("script %s was not found in the repository", ref)
	}
	if realScript != repositoryRoot && !strings.HasPrefix(realScript, repositoryRoot+string(filepath.Separator)) {
		return "", unavailable("script %s resolves outside the repository", ref)
	}
	info, err := os.Stat(realScript)
	if err != nil {
		return "", unavailable("script %s could not be read", ref)
	}
	if !info.Mode().IsRegular() {
		return "", unavailable("script %s is not a regular file", ref)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", unavailable("script %s is not executable (chmod +x it)", ref)
	}
	return realScript, nil
}

// repositoryRootOf returns the nearest directory at or above projectRoot that
// holds .git, or projectRoot itself outside a Git checkout.
func repositoryRootOf(projectRoot string) string {
	for current := projectRoot; ; {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return projectRoot
		}
		current = parent
	}
}

func findRepositoryScript(scriptsDir, name string) (profileScript, bool, error) {
	var matches []profileScript
	for _, extension := range slices.Sorted(maps.Keys(checkoutdefinitions.ScriptRuntimeByFileExtension)) {
		path := filepath.Join(scriptsDir, name+extension)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return profileScript{}, false, &Error{Outcome: OutcomeScriptUnavailable, Reason: fmt.Sprintf(".revyl/scripts/%s%s is not a readable regular file", name, extension)}
		}
		matches = append(matches, profileScript{source: ScriptSourceRepositoryScript, path: path, runtime: checkoutdefinitions.ScriptRuntimeByFileExtension[extension]})
	}
	if len(matches) > 1 {
		return profileScript{}, false, &Error{Outcome: OutcomeScriptUnavailable, Reason: fmt.Sprintf(".revyl/scripts holds more than one file named %s; keep one", name)}
	}
	if len(matches) == 1 {
		return matches[0], true, nil
	}
	return profileScript{}, false, nil
}

func fetchLibraryScript(ctx context.Context, library ScriptLibrary, name string) (profileScript, error) {
	unavailable := func(format string, args ...any) error {
		return &Error{Outcome: OutcomeScriptUnavailable, Reason: fmt.Sprintf(format, args...)}
	}
	scripts, err := library.ListAllScripts(ctx)
	if err != nil {
		return profileScript{}, unavailable("could not read the Library Scripts: %v", err)
	}
	var matches []api.CLIScriptInfo
	for _, script := range scripts {
		if script.Name == name {
			matches = append(matches, script)
		}
	}
	switch len(matches) {
	case 0:
		return profileScript{}, unavailable("no .revyl/scripts/%s file and no Library Script named %q exist", name, name)
	case 1:
	default:
		return profileScript{}, unavailable("%d Library Scripts are named %q; rename all but one", len(matches), name)
	}
	script, err := library.GetScript(ctx, matches[0].ID)
	if err != nil {
		return profileScript{}, unavailable("could not read Library Script %q: %v", name, err)
	}
	if _, ok := interpreterByRuntime[script.Runtime]; !ok {
		return profileScript{}, unavailable("Library Script %q uses runtime %q, which session profiles can't run", name, script.Runtime)
	}
	if script.Code == "" {
		return profileScript{}, unavailable("Library Script %q is empty", name)
	}
	return profileScript{source: ScriptSourceLibraryScript, runtime: script.Runtime, code: script.Code}, nil
}

func scriptFileExtension(runtime string) string {
	for extension, candidate := range checkoutdefinitions.ScriptRuntimeByFileExtension {
		if candidate == runtime {
			return extension
		}
	}
	return ""
}
