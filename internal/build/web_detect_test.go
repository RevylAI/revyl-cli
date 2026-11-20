package build

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeWebFixture(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetectWebAppReadsFrameworkDevServerAndPublicAddresses(t *testing.T) {
	dir := t.TempDir()
	writeWebFixture(t, dir, map[string]string{
		"package.json":   `{"homepage":"https://github.com/acme/site#readme","scripts":{"dev":"next dev --port 3100","build":"next build"},"dependencies":{"next":"15"}}`,
		"pnpm-lock.yaml": "",
		"CNAME":          "www.acme.dev\n",
		".env.example":   "NEXT_PUBLIC_SITE_URL=https://staging.acme.dev\nAPI_URL=https://api.acme.dev\nNEXT_PUBLIC_APP_URL=http://localhost:3000\n",
		".env":           "NEXT_PUBLIC_SITE_URL=https://secret.acme.dev\n",
	})

	app, ok := DetectWebApp(dir, false)

	if !ok {
		t.Fatal("DetectWebApp() found no app")
	}
	want := &WebApp{
		Framework: "nextjs", PackageManager: "pnpm", DevScript: "dev", DevCommand: "pnpm run dev", DevPort: 3100, BuildScript: "build",
		URLHints: []WebURLHint{{URL: "https://www.acme.dev", Source: "CNAME"}, {URL: "https://staging.acme.dev", Source: ".env.example NEXT_PUBLIC_SITE_URL"}},
	}
	if !reflect.DeepEqual(app, want) {
		t.Fatalf("DetectWebApp() = %+v, want %+v", app, want)
	}
}

func TestDetectWebAppPrefersFrameworkBuiltOnVite(t *testing.T) {
	dir := t.TempDir()
	writeWebFixture(t, dir, map[string]string{
		"package.json":     `{"scripts":{"dev":"astro dev"},"dependencies":{"astro":"5","vite":"6"}}`,
		"astro.config.mjs": "export default { site: 'https://acme.dev' }",
	})

	app, ok := DetectWebApp(dir, false)

	if !ok || app.Framework != "astro" || app.DevPort != 4321 || len(app.URLHints) != 1 || app.URLHints[0].URL != "https://acme.dev" {
		t.Fatalf("DetectWebApp() = %+v, %v", app, ok)
	}
}

func TestDetectWebAppSkipsDirectoriesThatAreNotWebApps(t *testing.T) {
	cases := map[string]map[string]string{
		"workspace root hoisting a dependency": {"package.json": `{"workspaces":["apps/*"],"scripts":{"lint":"eslint ."},"devDependencies":{"vite":"6"}}`},
		"expo app":                             {"package.json": `{"scripts":{"web":"expo start --web"},"dependencies":{"expo":"52","vite":"6"}}`, "app.json": `{"expo":{}}`, "vite.config.ts": ""},
		"library":                              {"package.json": `{"scripts":{"build":"tsc"},"dependencies":{"react":"19"}}`},
		"index.html outside the scan root":     {"index.html": "<html></html>"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeWebFixture(t, dir, files)
			if app, ok := DetectWebApp(dir, false); ok {
				t.Fatalf("DetectWebApp() = %+v, want no Web app", app)
			}
		})
	}
}

func TestDetectWebAppKeepsCapacitorAppsAsWebAndStaticRoots(t *testing.T) {
	capacitor := t.TempDir()
	writeWebFixture(t, capacitor, map[string]string{
		"package.json":        `{"scripts":{"dev":"vite"},"dependencies":{"@capacitor/core":"6","vite":"6"}}`,
		"capacitor.config.ts": "",
		"android/app/.keep":   "",
	})
	if app, ok := DetectWebApp(capacitor, false); !ok || app.Framework != "vite" {
		t.Fatalf("Capacitor DetectWebApp() = %+v, %v", app, ok)
	}

	static := t.TempDir()
	writeWebFixture(t, static, map[string]string{"index.html": "<html></html>"})
	if app, ok := DetectWebApp(static, true); !ok || app.Framework != "static" {
		t.Fatalf("static DetectWebApp() = %+v, %v", app, ok)
	}
}

func TestDetectWebAppReadsOnlyRegularProjectFiles(t *testing.T) {
	outside := t.TempDir()
	writeWebFixture(t, outside, map[string]string{".env": "APP_URL=https://secret.acme.dev"})
	dir := t.TempDir()
	writeWebFixture(t, dir, map[string]string{"package.json": `{"scripts":{"dev":"vite"},"devDependencies":{"vite":"6"}}`})
	writeWebFixture(t, outside, map[string]string{"CNAME": "secret.acme.dev"})
	if err := os.Symlink(filepath.Join(outside, ".env"), filepath.Join(dir, ".env.example")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "public")); err != nil {
		t.Fatal(err)
	}

	app, ok := DetectWebApp(dir, false)

	if !ok || len(app.URLHints) != 0 {
		t.Fatalf("DetectWebApp() = %+v, %v; want no hints read through a symlink", app, ok)
	}
}
