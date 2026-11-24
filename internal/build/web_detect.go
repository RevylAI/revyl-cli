package build

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// WebApp describes a Web app found in one directory: its framework, how its
// local dev server starts, and public addresses its own files name.
type WebApp struct {
	Framework      string       `json:"framework"`
	PackageManager string       `json:"package_manager,omitempty"`
	DevScript      string       `json:"dev_script,omitempty"`
	DevCommand     string       `json:"dev_command,omitempty"`
	DevPort        int          `json:"dev_port,omitempty"`
	BuildScript    string       `json:"build_script,omitempty"`
	URLHints       []WebURLHint `json:"url_hints,omitempty"`
}

// WebURLHint is a public address the app's own files name, with the file it
// came from. Nothing is fetched to find it.
type WebURLHint struct {
	URL    string `json:"url"`
	Source string `json:"source"`
}

type webFramework struct {
	name        string
	packages    []string
	binaries    []string
	configFiles []string
	devPort     int
}

// webFrameworks are checked in order: frameworks that build on Vite come
// before Vite itself.
var webFrameworks = []webFramework{
	{name: "nextjs", packages: []string{"next"}, binaries: []string{"next"}, configFiles: []string{"next.config.js", "next.config.mjs", "next.config.ts", "next.config.cjs"}, devPort: 3000},
	{name: "remix", packages: []string{"@remix-run/dev"}, binaries: []string{"remix"}, configFiles: []string{"remix.config.js", "remix.config.mjs"}, devPort: 5173},
	{name: "react-router", packages: []string{"@react-router/dev"}, binaries: []string{"react-router"}, configFiles: []string{"react-router.config.ts", "react-router.config.js"}, devPort: 5173},
	{name: "astro", packages: []string{"astro"}, binaries: []string{"astro"}, configFiles: []string{"astro.config.mjs", "astro.config.ts", "astro.config.js"}, devPort: 4321},
	{name: "nuxt", packages: []string{"nuxt"}, binaries: []string{"nuxt", "nuxi"}, configFiles: []string{"nuxt.config.ts", "nuxt.config.js"}, devPort: 3000},
	{name: "sveltekit", packages: []string{"@sveltejs/kit"}, binaries: []string{"svelte-kit"}, configFiles: []string{"svelte.config.js", "svelte.config.ts"}, devPort: 5173},
	{name: "solid-start", packages: []string{"@solidjs/start"}, binaries: []string{"vinxi"}, devPort: 3000},
	{name: "angular", packages: []string{"@angular/core"}, binaries: []string{"ng"}, configFiles: []string{"angular.json"}, devPort: 4200},
	{name: "gatsby", packages: []string{"gatsby"}, binaries: []string{"gatsby"}, configFiles: []string{"gatsby-config.js", "gatsby-config.ts", "gatsby-config.mjs"}, devPort: 8000},
	{name: "docusaurus", packages: []string{"@docusaurus/core"}, binaries: []string{"docusaurus"}, configFiles: []string{"docusaurus.config.js", "docusaurus.config.ts"}, devPort: 3000},
	{name: "create-react-app", packages: []string{"react-scripts"}, binaries: []string{"react-scripts"}, devPort: 3000},
	{name: "vue-cli", packages: []string{"@vue/cli-service"}, binaries: []string{"vue-cli-service"}, devPort: 8080},
	{name: "vite", packages: []string{"vite"}, binaries: []string{"vite"}, configFiles: []string{"vite.config.ts", "vite.config.js", "vite.config.mjs", "vite.config.mts"}, devPort: 5173},
	{name: "spa", packages: []string{"parcel", "webpack-dev-server", "@rsbuild/core", "@rspack/cli"}, binaries: []string{"parcel", "webpack", "rsbuild", "rspack"}, devPort: 8080},
}

var errNotProjectFile = errors.New("not a regular project file within the size limit")

const (
	maxWebURLHints      = 5
	maxProjectFileBytes = 1 << 20
)

var (
	devScriptNames      = []string{"dev", "start", "develop", "serve"}
	devPortFlagPattern  = regexp.MustCompile(`(?:--port[= ]|-p[= ]?|PORT=)(\d{2,5})\b`)
	astroSitePattern    = regexp.MustCompile(`\bsite\s*:\s*['"](https?://[^'"\s]+)['"]`)
	docusaurusURLKey    = regexp.MustCompile(`\burl\s*:\s*['"](https?://[^'"\s]+)['"]`)
	flyAppPattern       = regexp.MustCompile(`(?m)^\s*app\s*=\s*['"]([a-z0-9-]+)['"]`)
	envExampleURLKey    = regexp.MustCompile(`(?i)^(?:export\s+)?([A-Z0-9_]*(?:SITE|APP|PUBLIC|BASE|FRONTEND|WEB)[A-Z0-9_]*URL)\s*=\s*['"]?(https?://[^'"\s#]+)`)
	nonPublicHostSuffix = []string{".localhost", ".local", ".internal", ".test", ".example", ".invalid"}
	repositoryHosts     = map[string]bool{"github.com": true, "gitlab.com": true, "bitbucket.org": true, "npmjs.com": true, "www.npmjs.com": true}
)

// DetectWebApp reports the Web app in dir, if any. A directory holds a Web app
// when its package.json depends on a known Web framework and a script or a
// framework config file shows it is the app, rather than a workspace root that
// only hoists the dependency. A directory with an index.html and no
// package.json counts as a static site only when static is true. An Expo or
// React Native app is a mobile app, unless Capacitor wraps a Web app in it.
func DetectWebApp(dir string, static bool) (*WebApp, bool) {
	manifest, hasManifest := readPackageManifest(dir)
	if !hasManifest {
		if static && fileExists(filepath.Join(dir, "index.html")) {
			return &WebApp{Framework: "static", URLHints: webURLHints(dir, nil)}, true
		}
		return nil, false
	}
	if isJSApp(dir) && !hasCapacitorConfig(dir) {
		return nil, false
	}
	framework, ok := matchWebFramework(dir, manifest)
	if !ok {
		return nil, false
	}
	app := &WebApp{Framework: framework.name, PackageManager: detectPackageManager(dir), DevPort: framework.devPort}
	for _, name := range devScriptNames {
		script, exists := manifest.Scripts[name]
		if !exists {
			continue
		}
		app.DevScript = name
		app.DevCommand = app.PackageManager + " run " + name
		if match := devPortFlagPattern.FindStringSubmatch(script); match != nil {
			if port, err := strconv.Atoi(match[1]); err == nil && port > 0 && port < 65536 {
				app.DevPort = port
			}
		}
		break
	}
	if _, exists := manifest.Scripts["build"]; exists {
		app.BuildScript = "build"
	}
	app.URLHints = webURLHints(dir, &manifest)
	return app, true
}

type packageManifest struct {
	Homepage        string            `json:"homepage"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]any    `json:"dependencies"`
	DevDependencies map[string]any    `json:"devDependencies"`
}

func readPackageManifest(dir string) (packageManifest, bool) {
	var manifest packageManifest
	data, ok := readProjectFile(dir, "package.json")
	if !ok || json.Unmarshal(data, &manifest) != nil {
		return packageManifest{}, false
	}
	return manifest, true
}

// readProjectFile reads name below dir with readLocalFile, refusing a symlink
// in any path component as well.
func readProjectFile(dir, name string) ([]byte, bool) {
	path := dir
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		path = filepath.Join(path, part)
		if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, false
		}
	}
	data, err := readLocalFile(path)
	return data, err == nil
}

// readLocalFile reads a regular project file of at most maxProjectFileBytes.
// It refuses a symlink, so a fixed name such as package.json can't lead to a
// file such as .env.
func readLocalFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxProjectFileBytes {
		return nil, errNotProjectFile
	}
	file, err := os.Open(path) // #nosec G304 -- a fixed file name inside the local project, checked above.
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, maxProjectFileBytes))
}

func (m packageManifest) dependsOn(name string) bool {
	_, inDependencies := m.Dependencies[name]
	_, inDevDependencies := m.DevDependencies[name]
	return inDependencies || inDevDependencies
}

func matchWebFramework(dir string, manifest packageManifest) (webFramework, bool) {
	for _, framework := range webFrameworks {
		depends := false
		for _, name := range framework.packages {
			depends = depends || manifest.dependsOn(name)
		}
		if !depends {
			continue
		}
		for _, config := range framework.configFiles {
			if fileExists(filepath.Join(dir, config)) {
				return framework, true
			}
		}
		for _, script := range manifest.Scripts {
			for _, binary := range framework.binaries {
				if scriptRunsBinary(script, binary) {
					return framework, true
				}
			}
		}
	}
	return webFramework{}, false
}

func scriptRunsBinary(script, binary string) bool {
	for _, field := range strings.FieldsFunc(script, func(r rune) bool { return r == ' ' || r == '&' || r == ';' || r == '|' }) {
		if field == binary {
			return true
		}
	}
	return false
}

func hasCapacitorConfig(dir string) bool {
	for _, name := range []string{"capacitor.config.ts", "capacitor.config.json", "capacitor.config.js"} {
		if fileExists(filepath.Join(dir, name)) {
			return true
		}
	}
	return false
}

// detectPackageManager reads the lockfile in dir or its nearest ancestor up to
// the repository root, so a monorepo app uses the workspace's package manager.
// Without any lockfile, the nearest package.json packageManager pin counts.
func detectPackageManager(dir string) string {
	lockfiles := []struct{ file, manager string }{
		{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lockb", "bun"}, {"bun.lock", "bun"}, {"package-lock.json", "npm"},
	}
	current := dir
	pinned := ""
	for range 8 {
		for _, lockfile := range lockfiles {
			if fileExists(filepath.Join(current, lockfile.file)) {
				return lockfile.manager
			}
		}
		if manager, _ := pinnedPackageManager(current); pinned == "" && slices.Contains([]string{"npm", "pnpm", "yarn", "bun"}, manager) {
			pinned = manager
		}
		parent := filepath.Dir(current)
		if DirExists(filepath.Join(current, ".git")) || fileExists(filepath.Join(current, ".git")) || parent == current {
			break
		}
		current = parent
	}
	if pinned != "" {
		return pinned
	}
	return "npm"
}

func webURLHints(dir string, manifest *packageManifest) []WebURLHint {
	var hints []WebURLHint
	seen := map[string]bool{}
	add := func(raw, source string) {
		normalized, ok := publicWebURL(raw)
		if !ok || seen[normalized] || len(hints) >= maxWebURLHints {
			return
		}
		seen[normalized] = true
		hints = append(hints, WebURLHint{URL: normalized, Source: source})
	}
	if manifest != nil {
		add(manifest.Homepage, "package.json homepage")
	}
	for _, name := range []string{"CNAME", "public/CNAME", "static/CNAME"} {
		if data, ok := readProjectFile(dir, name); ok {
			if domain := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0]); domain != "" {
				add("https://"+domain, name)
			}
		}
	}
	for _, source := range []struct {
		files   []string
		pattern *regexp.Regexp
	}{
		{[]string{"astro.config.mjs", "astro.config.ts", "astro.config.js"}, astroSitePattern},
		{[]string{"docusaurus.config.js", "docusaurus.config.ts"}, docusaurusURLKey},
	} {
		for _, name := range source.files {
			if data, ok := readProjectFile(dir, name); ok {
				if match := source.pattern.FindSubmatch(data); match != nil {
					add(string(match[1]), name)
				}
			}
		}
	}
	if data, ok := readProjectFile(dir, "fly.toml"); ok {
		if match := flyAppPattern.FindSubmatch(data); match != nil {
			add("https://"+string(match[1])+".fly.dev", "fly.toml app")
		}
	}
	// Example env files hold placeholders; real .env files are never read.
	for _, name := range []string{".env.example", ".env.sample", ".env.template"} {
		data, ok := readProjectFile(dir, name)
		if !ok {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if match := envExampleURLKey.FindStringSubmatch(strings.TrimSpace(line)); match != nil {
				add(match[2], name+" "+match[1])
			}
		}
	}
	return hints
}

// publicWebURL keeps an http(s) address with a public-looking host, upgraded to
// https and without a query or fragment. It only filters obvious
// non-candidates such as localhost, placeholders, and repository links;
// revyl setup check-url and the backend apply the authoritative rules.
func publicWebURL(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if !strings.Contains(host, ".") || strings.HasPrefix(host, "127.") || strings.HasPrefix(host, "0.") || strings.HasPrefix(host, "example.") || strings.Contains(host, ".example.") || repositoryHosts[host] {
		return "", false
	}
	for _, suffix := range nonPublicHostSuffix {
		if strings.HasSuffix(host, suffix) {
			return "", false
		}
	}
	parsed.Scheme = "https"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimSuffix(parsed.String(), "/"), true
}
