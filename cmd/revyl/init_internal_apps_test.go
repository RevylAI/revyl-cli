package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/revyl/cli/internal/build"
)

func TestWorkspaceRootHintsForExpoMonorepo(t *testing.T) {
	root := copyInternalAppFixture(t, filepath.Join(repoRootForInitFixtureTests(t), "internal-apps", "expo-monorepo-hoisted"), false)

	hints := findNestedMobileAppHints(root)
	if len(hints) != 1 {
		t.Fatalf("findNestedMobileAppHints(root) = %v, want exactly one Expo app hint", hints)
	}
	if hints[0].RelativePath != "apps/mobile" {
		t.Fatalf("hint path = %q, want %q", hints[0].RelativePath, "apps/mobile")
	}
	if hints[0].System != build.SystemExpo {
		t.Fatalf("hint system = %v, want %v", hints[0].System, build.SystemExpo)
	}

	if got := findNestedMobileAppHints(filepath.Join(root, "apps", "mobile")); len(got) != 0 {
		t.Fatalf("findNestedMobileAppHints(app dir) = %v, want no root hint", got)
	}
}

func TestInternalAppOnboardingFixtures(t *testing.T) {
	t.Cleanup(func() {
		initNonInteractive = false
	})
	initNonInteractive = true

	overrideOpts, err := newInitOverrideOptions(nil, "", false)
	if err != nil {
		t.Fatalf("newInitOverrideOptions() error = %v", err)
	}

	tests := []struct {
		path              string
		wantBuildSystem   string
		wantPlatforms     []string
		wantHotReload     string
		wantAppScheme     string
		wantConcreteBuild []string
	}{
		{
			path:              "bug-bazaar",
			wantBuildSystem:   "Expo",
			wantPlatforms:     []string{"android", "ios"},
			wantHotReload:     "expo",
			wantAppScheme:     "bug-bazaar",
			wantConcreteBuild: []string{"android", "ios"},
		},
		{
			path:              filepath.Join("expo-monorepo-hoisted", "apps", "mobile"),
			wantBuildSystem:   "Expo",
			wantPlatforms:     []string{"android", "ios"},
			wantHotReload:     "expo",
			wantAppScheme:     "expo-monorepo-hoisted",
			wantConcreteBuild: []string{"android", "ios"},
		},
		{
			path:              "rn-bare-minimal",
			wantBuildSystem:   "React Native",
			wantPlatforms:     []string{"android", "ios"},
			wantHotReload:     "react-native",
			wantConcreteBuild: []string{"android", "ios"},
		},
		{
			path:              "flutter-minimal",
			wantBuildSystem:   "Flutter",
			wantPlatforms:     []string{"android", "ios"},
			wantConcreteBuild: []string{"android", "ios"},
		},
		{
			path:              "android-minimal",
			wantBuildSystem:   "Gradle (Android)",
			wantPlatforms:     []string{"android"},
			wantConcreteBuild: []string{"android"},
		},
		{
			path:              "swift-minimal",
			wantBuildSystem:   "Xcode",
			wantPlatforms:     []string{"ios"},
			wantConcreteBuild: []string{"ios"},
		},
		{
			path:              "bazel-minimal",
			wantBuildSystem:   "Bazel",
			wantPlatforms:     []string{"android", "ios"},
			wantConcreteBuild: []string{"android", "ios"},
		},
		{
			path:              "kmp-minimal",
			wantBuildSystem:   "Kotlin Multiplatform",
			wantPlatforms:     []string{"android", "ios"},
			wantConcreteBuild: []string{"android", "ios"},
		},
	}

	root := repoRootForInitFixtureTests(t)
	for _, tt := range tests {
		tt := tt
		t.Run(filepath.ToSlash(tt.path), func(t *testing.T) {
			workDir := copyInternalAppFixture(t, filepath.Join(root, "internal-apps", tt.path), true)
			revylDir := filepath.Join(workDir, ".revyl")
			configPath := filepath.Join(revylDir, "config.yaml")

			cfg, err := wizardProjectSetup(workDir, revylDir, configPath, overrideOpts)
			if err != nil {
				t.Fatalf("wizardProjectSetup(%s) error = %v", tt.path, err)
			}

			if cfg.Build.System != tt.wantBuildSystem {
				t.Fatalf("build.system = %q, want %q", cfg.Build.System, tt.wantBuildSystem)
			}

			for _, platform := range tt.wantPlatforms {
				if _, ok := cfg.Build.Platforms[platform]; !ok {
					t.Fatalf("missing platform %q in %+v", platform, cfg.Build.Platforms)
				}
			}

			for _, platform := range tt.wantConcreteBuild {
				got := cfg.Build.Platforms[platform]
				if got.Command == "" || got.Output == "" {
					t.Fatalf("platform %q should be concrete, got %+v", platform, got)
				}
			}

			ready := wizardHotReloadSetup(context.Background(), nil, cfg, configPath, workDir, false, overrideOpts, "")
			if tt.wantHotReload == "" {
				if ready {
					t.Fatalf("wizardHotReloadSetup(%s) = true, want rebuild-only false", tt.path)
				}
				if cfg.HotReload.IsConfigured() {
					t.Fatalf("hotreload should not be configured for %s", tt.path)
				}
				return
			}

			if !ready {
				t.Fatalf("wizardHotReloadSetup(%s) = false, want true", tt.path)
			}
			if cfg.HotReload.Default != tt.wantHotReload {
				t.Fatalf("hotreload.default = %q, want %q", cfg.HotReload.Default, tt.wantHotReload)
			}

			providerCfg := cfg.HotReload.GetProviderConfig(tt.wantHotReload)
			if providerCfg == nil {
				t.Fatalf("missing provider config for %s", tt.wantHotReload)
			}
			if tt.wantAppScheme != "" && providerCfg.AppScheme != tt.wantAppScheme {
				t.Fatalf("provider app scheme = %q, want %q", providerCfg.AppScheme, tt.wantAppScheme)
			}
		})
	}
}

func repoRootForInitFixtureTests(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
}

func copyInternalAppFixture(t *testing.T, src string, skipRevyl bool) string {
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
		t.Fatalf("copyInternalAppFixture(%s) error = %v", src, err)
	}

	return dst
}
