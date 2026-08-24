package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

func TestConfigureExpoBuildStreams_Defaults(t *testing.T) {
	cfg := &initConfigDraft{
		Build: initBuildDraft{
			DetectedSystem: build.SystemExpo,
			Framework:      "expo",
			Recipes: map[string]initBuildRecipeDraft{
				"ios": {
					BuildCommands: []string{"legacy-ios-command"},
					OutputPath:    "legacy-ios-output",
				},
				"android": {
					BuildCommands: []string{"legacy-android-command"},
					OutputPath:    "legacy-android-output",
				},
			},
		},
	}

	configureExpoBuildStreams(cfg, t.TempDir())

	for _, key := range []string{"ios", "android"} {
		if _, ok := cfg.Build.Recipes[key]; !ok {
			t.Fatalf("expected build platform %q to be configured", key)
		}
	}
	if cfg.Build.DefaultCommand == "" || cfg.Build.DefaultOutput == "" {
		t.Fatalf("expected build.command/output to be set for expo defaults")
	}
}

func TestConfigureExpoBuildStreams_PreservesExplicitStreamConfig(t *testing.T) {
	cfg := &initConfigDraft{
		Build: initBuildDraft{
			DetectedSystem: build.SystemExpo,
			Framework:      "expo",
			Recipes: map[string]initBuildRecipeDraft{
				"ios-dev": {
					BuildCommands: []string{"custom-dev-ios"},
					OutputPath:    "custom-dev-ios-output",
				},
				"ios-ci": {
					BuildCommands: []string{"custom-ci-ios"},
					OutputPath:    "custom-ci-ios-output",
				},
			},
		},
	}

	configureExpoBuildStreams(cfg, t.TempDir())

	if got := cfg.Build.Recipes["ios-dev"].primaryBuildCommand(); got != "custom-dev-ios" {
		t.Fatalf("ios-dev command = %q, want %q", got, "custom-dev-ios")
	}
}

func TestConfigureExpoBuildStreams_PreservesCustomNonLegacyPlatforms(t *testing.T) {
	cfg := &initConfigDraft{
		Build: initBuildDraft{
			DetectedSystem: build.SystemExpo,
			Framework:      "expo",
			Recipes: map[string]initBuildRecipeDraft{
				"ios-preview": {
					BuildCommands: []string{"custom-ios-preview"},
					OutputPath:    "preview-ios-output",
				},
				"android-preview": {
					BuildCommands: []string{"custom-android-preview"},
					OutputPath:    "preview-android-output",
				},
			},
		},
	}

	configureExpoBuildStreams(cfg, t.TempDir())

	if got := cfg.Build.Recipes["ios-preview"].primaryBuildCommand(); got != "custom-ios-preview" {
		t.Fatalf("ios-preview command = %q, want %q", got, "custom-ios-preview")
	}
	if got := cfg.Build.Recipes["android-preview"].primaryBuildCommand(); got != "custom-android-preview" {
		t.Fatalf("android-preview command = %q, want %q", got, "custom-android-preview")
	}
	if _, ok := cfg.Build.Recipes["ios-dev"]; ok {
		t.Fatalf("did not expect ios-dev to be auto-added when custom platform keys exist")
	}
}

func TestEnsureInitExpoDevClientSchemeKeepsDetectedSchemeTransient(t *testing.T) {
	dir := t.TempDir()
	writeExpoPreflightFile(t, dir, "app.json", `{"expo":{"name":"Demo","scheme":"demo-dev"}}`)
	cfg := &initConfigDraft{
		Project: initProjectDraft{ID: "11111111-1111-4111-8111-111111111111"},
		Build:   initBuildDraft{DetectedSystem: build.SystemExpo},
	}

	quiet := ui.IsQuietMode()
	ui.SetQuietMode(false)
	var messages []string
	ui.SetOutputObserver(func(_ string, message string) {
		messages = append(messages, message)
	})
	t.Cleanup(func() {
		ui.SetOutputObserver(nil)
		ui.SetQuietMode(quiet)
	})

	if err := ensureInitExpoDevClientScheme(dir, cfg); err != nil {
		t.Fatalf("ensureInitExpoDevClientScheme() error = %v", err)
	}
	expo := cfg.HotReload.provider("expo")
	if expo == nil || expo.AppScheme != "demo-dev" {
		t.Fatalf("transient Expo provider = %+v, want app scheme demo-dev", expo)
	}
	authored, err := cfg.canonicalAuthoredConfig()
	if err != nil {
		t.Fatalf("canonicalAuthoredConfig() error = %v", err)
	}
	writtenBytes, err := config.MarshalCanonicalConfig(*authored)
	if err != nil {
		t.Fatalf("MarshalCanonicalConfig() error = %v", err)
	}
	written := string(writtenBytes)
	if strings.Contains(written, "demo-dev") || strings.Contains(written, "hotreload") {
		t.Fatalf("canonical config persisted transient Expo state:\n%s", written)
	}
	output := strings.Join(messages, "\n")
	if !strings.Contains(output, "using it for this init run") {
		t.Fatalf("output = %q, want transient-use wording", output)
	}
	if strings.Contains(output, "saved") || strings.Contains(output, "persist") {
		t.Fatalf("output = %q, must not claim persistence", output)
	}
}

func TestSelectableRuntimePlatforms_FromStreamKeys(t *testing.T) {
	cfg := &initConfigDraft{
		Build: initBuildDraft{
			Recipes: map[string]initBuildRecipeDraft{
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
	cfg := &initConfigDraft{
		Build: initBuildDraft{
			Recipes: map[string]initBuildRecipeDraft{
				"ios-dev": {AppID: "dev-app-id"},
				"ios-ci":  {AppID: "ci-app-id"},
			},
		},
		HotReload: initHotReloadDraft{
			Providers: map[string]*initProviderDraft{
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
	cfg := &initConfigDraft{
		Build: initBuildDraft{
			Recipes: map[string]initBuildRecipeDraft{
				"ios-ci":  {BuildCommands: []string{"xcodebuild-ci"}, OutputPath: "ci.app", AppID: "ci-app-id"},
				"ios-dev": {BuildCommands: []string{"xcodebuild-dev"}, OutputPath: "dev.app", AppID: "dev-app-id"},
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
