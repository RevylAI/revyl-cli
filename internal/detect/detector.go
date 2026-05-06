// Package detect mirrors the backend stack detector in Go so users can run
// `revyl detect` locally and see exactly what the templater would have done
// from the GitHub App's webhook handler.
//
// Heuristics here MUST stay in sync with
// cognisim_backend/app/services/rebel/stack_detector.py — when one moves, the
// other moves with it. Both fall through to StackUnknown rather than guessing,
// so a templater PR is never opened against a repo we can't classify.
package detect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Stack identifies the mobile-app shape of a repository.
type Stack string

const (
	StackExpo          Stack = "expo"
	StackBareRN        Stack = "bare-rn"
	StackNativeIOS     Stack = "native-ios"
	StackNativeAndroid Stack = "native-android"
	StackFlutter       Stack = "flutter"
	StackKMP           Stack = "kmp"
	StackUnknown       Stack = "unknown"
)

// Result is the JSON-serializable output of a detection run.
type Result struct {
	Stack            Stack    `json:"stack"`
	WorkingDirectory string   `json:"working_directory"`
	IOSWorkspace     string   `json:"ios_workspace,omitempty"`
	IOSScheme        string   `json:"ios_scheme,omitempty"`
	IOSPods          bool     `json:"ios_pods"`
	AndroidModule    string   `json:"android_module"`
	PackageManager   string   `json:"package_manager"`
	Notes            []string `json:"notes,omitempty"`
}

// Actionable reports whether a templater would proceed for this stack.
func (r Result) Actionable() bool { return r.Stack != StackUnknown }

// MarshalJSON ensures stable field ordering for golden tests.
func (r Result) MarshalJSON() ([]byte, error) {
	type alias Result
	return json.Marshal(alias(r))
}

// Detect inspects the repo at root and classifies it. It performs no network
// I/O — pass an absolute path to the local checkout.
func Detect(root string) Result {
	return detectIn(root, root)
}

func detectIn(root, originalRoot string) Result {
	notes := []string{}

	pkg := readPackageJSON(filepath.Join(root, "package.json"))
	deps := mergedDeps(pkg)

	hasAppJSONExpo := readAppJSONExpo(filepath.Join(root, "app.json"))
	hasIOSDir := dirExists(filepath.Join(root, "ios"))
	hasAndroidDir := dirExists(filepath.Join(root, "android"))
	hasPubspec := fileExists(filepath.Join(root, "pubspec.yaml"))
	hasXcodeproj := globExists(root, ".xcodeproj")
	hasXcworkspace := globExists(root, ".xcworkspace")
	hasRootGradle := fileExists(filepath.Join(root, "build.gradle")) ||
		fileExists(filepath.Join(root, "build.gradle.kts"))
	hasKMPMarkers := dirExists(filepath.Join(root, "composeApp")) ||
		(dirExists(filepath.Join(root, "shared")) &&
			dirExists(filepath.Join(root, "iosApp")))

	pm := detectPackageManager(root)

	relWD := "."
	if root != originalRoot {
		if rel, err := filepath.Rel(originalRoot, root); err == nil {
			relWD = filepath.ToSlash(rel)
		}
	}

	res := Result{
		WorkingDirectory: relWD,
		AndroidModule:    "app",
		PackageManager:   pm,
		Notes:            notes,
	}

	switch {
	case hasPubspec:
		res.Stack = StackFlutter
		return res

	case hasKMPMarkers:
		res.Stack = StackKMP
		return res
	}

	isExpo := hasAppJSONExpo || hasDep(deps, "expo")
	isRN := hasDep(deps, "react-native")

	if isExpo && !(hasIOSDir && hasAndroidDir) {
		res.Stack = StackExpo
		return res
	}

	if isRN && (hasIOSDir || hasAndroidDir) {
		res.Stack = StackBareRN
		ws, scheme := iosHints(filepath.Join(root, "ios"))
		res.IOSWorkspace = ws
		res.IOSScheme = scheme
		res.IOSPods = fileExists(filepath.Join(root, "ios", "Podfile"))
		return res
	}

	// Native iOS only
	if pkg == nil && (hasXcodeproj || hasXcworkspace) {
		res.Stack = StackNativeIOS
		ws, scheme := iosHints(root)
		res.IOSWorkspace = ws
		res.IOSScheme = scheme
		res.IOSPods = fileExists(filepath.Join(root, "Podfile"))
		if !hasXcworkspace && hasXcodeproj {
			res.Notes = append(res.Notes,
				"native-ios detected via .xcodeproj; template uses -project not -workspace")
		}
		return res
	}

	// Native Android only
	if hasRootGradle && !hasIOSDir && pkg == nil {
		res.Stack = StackNativeAndroid
		return res
	}

	// Monorepo subdir search — one level deep
	if root == originalRoot {
		for _, candidate := range []string{
			"apps/mobile", "apps/app", "packages/mobile", "mobile",
		} {
			subRoot := filepath.Join(root, candidate)
			if dirExists(subRoot) {
				inner := detectIn(subRoot, originalRoot)
				if inner.Actionable() {
					inner.WorkingDirectory = filepath.ToSlash(candidate)
					inner.Notes = append(inner.Notes,
						"detected mobile app under "+candidate+"/")
					return inner
				}
			}
		}
	}

	res.Stack = StackUnknown
	if len(res.Notes) == 0 {
		res.Notes = []string{"no mobile signals"}
	}
	return res
}

// ---- helpers ----------------------------------------------------------------

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func globExists(root, suffix string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), suffix) {
			return true
		}
	}
	return false
}

func readPackageJSON(path string) map[string]any {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

func mergedDeps(pkg map[string]any) map[string]string {
	out := map[string]string{}
	for _, key := range []string{"dependencies", "devDependencies"} {
		raw, ok := pkg[key]
		if !ok {
			continue
		}
		section, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for k, v := range section {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
	}
	return out
}

func hasDep(deps map[string]string, name string) bool {
	_, ok := deps[name]
	return ok
}

func readAppJSONExpo(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return false
	}
	_, ok := m["expo"]
	return ok
}

var packageManagerLockfiles = []struct {
	pm   string
	file string
}{
	{"pnpm", "pnpm-lock.yaml"},
	{"yarn", "yarn.lock"},
	{"npm", "package-lock.json"},
}

func detectPackageManager(root string) string {
	for _, entry := range packageManagerLockfiles {
		if fileExists(filepath.Join(root, entry.file)) {
			return entry.pm
		}
	}
	return "npm"
}

// iosHints returns (workspace, scheme) inferred from the iOS folder layout.
func iosHints(iosRoot string) (string, string) {
	entries, err := os.ReadDir(iosRoot)
	if err != nil {
		return "", ""
	}
	var ws, proj string
	for _, e := range entries {
		switch {
		case strings.HasSuffix(e.Name(), ".xcworkspace"):
			ws = e.Name()
		case strings.HasSuffix(e.Name(), ".xcodeproj"):
			proj = e.Name()
		}
	}
	if ws != "" {
		return filepath.Join(filepath.Base(iosRoot), ws), strings.TrimSuffix(ws, ".xcworkspace")
	}
	if proj != "" {
		return filepath.Join(filepath.Base(iosRoot), proj), strings.TrimSuffix(proj, ".xcodeproj")
	}
	return "", ""
}

// nodeVersion is intentionally not detected. The templater pins Node 22 in
// generated workflows because per-repo `.nvmrc` / `engines.node` values are
// frequently behind transitive-dep requirements (e.g. modern @react-native
// packages need >=20). Users who need an older Node can edit the merged YAML.
var _ = regexp.MustCompile
