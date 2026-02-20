package main

import (
	"reflect"
	"testing"

	"github.com/revyl/cli/internal/config"
)

func TestConfigureExpoBuildStreams_Defaults(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			System: "Expo",
			Platforms: map[string]config.BuildPlatform{
				"ios": {
					Command: "legacy-ios-command",
					Output:  "legacy-ios-output",
				},
				"android": {
					Command: "legacy-android-command",
					Output:  "legacy-android-output",
				},
			},
		},
	}

	configureExpoBuildStreams(cfg)

	for _, key := range []string{"ios-dev", "android-dev", "ios-ci", "android-ci"} {
		if _, ok := cfg.Build.Platforms[key]; !ok {
			t.Fatalf("expected build platform %q to be configured", key)
		}
	}
	if cfg.Build.Command == "" || cfg.Build.Output == "" {
		t.Fatalf("expected build.command/output to be set for expo defaults")
	}
}

func TestConfigureExpoBuildStreams_PreservesExplicitStreamConfig(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			System: "Expo",
			Platforms: map[string]config.BuildPlatform{
				"ios-dev": {
					Command: "custom-dev-ios",
					Output:  "custom-dev-ios-output",
				},
				"ios-ci": {
					Command: "custom-ci-ios",
					Output:  "custom-ci-ios-output",
				},
			},
		},
	}

	configureExpoBuildStreams(cfg)

	if got := cfg.Build.Platforms["ios-dev"].Command; got != "custom-dev-ios" {
		t.Fatalf("ios-dev command = %q, want %q", got, "custom-dev-ios")
	}
}

func TestConfigureExpoBuildStreams_PreservesCustomNonLegacyPlatforms(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			System: "Expo",
			Platforms: map[string]config.BuildPlatform{
				"ios-preview": {
					Command: "custom-ios-preview",
					Output:  "preview-ios-output",
				},
				"android-preview": {
					Command: "custom-android-preview",
					Output:  "preview-android-output",
				},
			},
		},
	}

	configureExpoBuildStreams(cfg)

	if got := cfg.Build.Platforms["ios-preview"].Command; got != "custom-ios-preview" {
		t.Fatalf("ios-preview command = %q, want %q", got, "custom-ios-preview")
	}
	if got := cfg.Build.Platforms["android-preview"].Command; got != "custom-android-preview" {
		t.Fatalf("android-preview command = %q, want %q", got, "custom-android-preview")
	}
	if _, ok := cfg.Build.Platforms["ios-dev"]; ok {
		t.Fatalf("did not expect ios-dev to be auto-added when custom platform keys exist")
	}
}

func TestSelectableRuntimePlatforms_FromStreamKeys(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"ios-dev":     {},
				"ios-ci":      {},
				"android-dev": {},
			},
		},
	}

	got := selectableRuntimePlatforms(cfg)
	want := []string{"ios", "android"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selectableRuntimePlatforms() = %v, want %v", got, want)
	}
}

func TestResolveAppIDForRuntimePlatform_PrefersHotReloadMapping(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"ios-dev": {AppID: "dev-app-id"},
				"ios-ci":  {AppID: "ci-app-id"},
			},
		},
		HotReload: config.HotReloadConfig{
			Providers: map[string]*config.ProviderConfig{
				"expo": {
					PlatformKeys: map[string]string{
						"ios": "ios-ci",
					},
				},
			},
		},
	}

	got := resolveAppIDForRuntimePlatform(cfg, "ios")
	if got != "ci-app-id" {
		t.Fatalf("resolveAppIDForRuntimePlatform() = %q, want %q", got, "ci-app-id")
	}
}

func TestResolveAppIDForRuntimePlatform_FallsBackToBestKey(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"ios-ci":  {AppID: "ci-app-id"},
				"ios-dev": {AppID: "dev-app-id"},
			},
		},
	}

	got := resolveAppIDForRuntimePlatform(cfg, "ios")
	if got != "dev-app-id" {
		t.Fatalf("resolveAppIDForRuntimePlatform() = %q, want %q", got, "dev-app-id")
	}
}

func TestDefaultExpoDevBuildTargetsForHost_DarwinPrefersIOS(t *testing.T) {
	got := defaultExpoDevBuildTargetsForHost([]string{"android-dev", "ios-dev"}, "darwin")
	want := []string{"ios-dev"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaultExpoDevBuildTargetsForHost() = %v, want %v", got, want)
	}
}

func TestDefaultExpoDevBuildTargetsForHost_NonDarwinPrefersAndroid(t *testing.T) {
	got := defaultExpoDevBuildTargetsForHost([]string{"ios-dev", "android-dev"}, "linux")
	want := []string{"android-dev"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaultExpoDevBuildTargetsForHost() = %v, want %v", got, want)
	}
}

func TestDefaultExpoDevBuildTargetsForHost_FallbackToAvailableStream(t *testing.T) {
	got := defaultExpoDevBuildTargetsForHost([]string{"android-dev"}, "darwin")
	want := []string{"android-dev"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaultExpoDevBuildTargetsForHost() = %v, want %v", got, want)
	}
}
