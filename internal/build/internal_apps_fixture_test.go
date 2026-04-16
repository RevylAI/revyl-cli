package build

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestInternalAppsFixtures(t *testing.T) {
	t.Parallel()

	fixtures := []struct {
		path                string
		wantSystem          BuildSystem
		wantPlatforms       []string
		wantConcreteTargets []string
	}{
		{
			path:                "bug-bazaar",
			wantSystem:          SystemExpo,
			wantPlatforms:       []string{"android", "ios"},
			wantConcreteTargets: []string{"android", "ios"},
		},
		{
			path:                "rn-bare-minimal",
			wantSystem:          SystemReactNative,
			wantPlatforms:       []string{"android", "ios"},
			wantConcreteTargets: []string{"android", "ios"},
		},
		{
			path:                "flutter-minimal",
			wantSystem:          SystemFlutter,
			wantPlatforms:       []string{"android", "ios"},
			wantConcreteTargets: []string{"android", "ios"},
		},
		{
			path:                "android-minimal",
			wantSystem:          SystemGradle,
			wantPlatforms:       []string{"android"},
			wantConcreteTargets: []string{"android"},
		},
		{
			path:                "swift-minimal",
			wantSystem:          SystemXcode,
			wantPlatforms:       []string{"ios"},
			wantConcreteTargets: []string{"ios"},
		},
		{
			path:                filepath.Join("expo-monorepo-hoisted", "apps", "mobile"),
			wantSystem:          SystemExpo,
			wantPlatforms:       []string{"android", "ios"},
			wantConcreteTargets: []string{"android", "ios"},
		},
		{
			path:                "bazel-minimal",
			wantSystem:          SystemBazel,
			wantPlatforms:       []string{"android", "ios"},
			wantConcreteTargets: []string{"android", "ios"},
		},
		{
			path:                "kmp-minimal",
			wantSystem:          SystemKMP,
			wantPlatforms:       []string{"android", "ios"},
			wantConcreteTargets: []string{"android", "ios"},
		},
	}

	root := repoRootForFixtureTests(t)
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(filepath.ToSlash(fixture.path), func(t *testing.T) {
			src := filepath.Join(root, "internal-apps", fixture.path)
			workDir := copyFixtureTree(t, src, false)

			detected, err := Detect(workDir)
			if err != nil {
				t.Fatalf("Detect(%s) error = %v", fixture.path, err)
			}

			if detected.System != fixture.wantSystem {
				t.Fatalf("system = %v, want %v", detected.System, fixture.wantSystem)
			}

			for _, platform := range fixture.wantPlatforms {
				if _, ok := detected.Platforms[platform]; !ok {
					t.Fatalf("missing platform %q in %v", platform, fixtureKeys(detected.Platforms))
				}
			}

			for _, platform := range fixture.wantConcreteTargets {
				got := detected.Platforms[platform]
				if got.Command == "" || got.Output == "" {
					t.Fatalf("platform %q should be concrete, got %+v", platform, got)
				}
			}

			if fixture.wantSystem == SystemBazel {
				android := detected.Platforms["android"]
				if android.Command != "bazel build //app:bazel_minimal -c dbg" {
					t.Fatalf("bazel android command = %q, want concrete target command", android.Command)
				}

				ios := detected.Platforms["ios"]
				if ios.Command != "bazel build //ios:bazel_minimal_ios -c dbg --ios_multi_cpus=sim_arm64" {
					t.Fatalf("bazel ios command = %q, want concrete target command", ios.Command)
				}

				buildFile := filepath.Join(src, "app", "BUILD.bazel")
				buildContent, err := os.ReadFile(buildFile)
				if err != nil {
					t.Fatalf("read BUILD.bazel: %v", err)
				}
				if !strings.Contains(string(buildContent), "load(") {
					t.Fatalf("bazel-minimal BUILD.bazel is missing load() statements — rules will be undefined at build time")
				}

				iosBuildFile := filepath.Join(src, "ios", "BUILD.bazel")
				iosBuildContent, err := os.ReadFile(iosBuildFile)
				if err != nil {
					t.Fatalf("read ios/BUILD.bazel: %v", err)
				}
				if !strings.Contains(string(iosBuildContent), "load(") {
					t.Fatalf("bazel-minimal ios/BUILD.bazel is missing load() statements — rules will be undefined at build time")
				}

				moduleFile := filepath.Join(src, "MODULE.bazel")
				moduleContent, err := os.ReadFile(moduleFile)
				if err != nil {
					t.Fatalf("read MODULE.bazel: %v", err)
				}
				if !strings.Contains(string(moduleContent), "bazel_dep(") {
					t.Fatalf("bazel-minimal MODULE.bazel is missing bazel_dep() — external rules will not resolve")
				}
			}

			if fixture.wantSystem == SystemKMP {
				android := detected.Platforms["android"]
				if android.Command != "./gradlew :androidApp:assembleDebug" {
					t.Fatalf("kmp android command = %q, want root gradlew command", android.Command)
				}
			}
		})
	}
}

func repoRootForFixtureTests(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
}

func copyFixtureTree(t *testing.T, src string, skipRevyl bool) string {
	t.Helper()

	if _, err := os.Stat(src); os.IsNotExist(err) {
		t.Skipf("fixture directory %s not found (running outside monorepo?)", src)
	}

	dst := t.TempDir()
	skipDirs := map[string]struct{}{
		"node_modules": {},
		"Pods":         {},
		"build":        {},
		".gradle":      {},
		"dev-sessions": {},
	}

	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		base := filepath.Base(path)
		if d.IsDir() {
			if _, skip := skipDirs[base]; skip {
				return filepath.SkipDir
			}
			if skipRevyl && base == ".revyl" {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		info, err := os.Stat(path)
		if err != nil {
			return err
		}

		return os.WriteFile(target, data, info.Mode())
	})
	if err != nil {
		t.Fatalf("copyFixtureTree(%s) error = %v", src, err)
	}

	return dst
}

func fixtureKeys(m map[string]BuildPlatform) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
