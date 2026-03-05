package main

import (
	"testing"

	"github.com/revyl/cli/internal/config"
)

func TestResolveBuildUploadPlatform_ExactKeyWins(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"ios":     {Command: "xcodebuild", Output: "ios.ipa"},
				"ios-dev": {Command: "xcodebuild-dev", Output: "ios-dev.ipa"},
			},
		},
	}

	resolved, err := resolveBuildUploadPlatform(cfg, "ios")
	if err != nil {
		t.Fatalf("resolveBuildUploadPlatform() error = %v", err)
	}
	if resolved.PlatformKey != "ios" {
		t.Fatalf("PlatformKey = %q, want %q", resolved.PlatformKey, "ios")
	}
	if resolved.DevicePlatform != "ios" {
		t.Fatalf("DevicePlatform = %q, want %q", resolved.DevicePlatform, "ios")
	}
}

func TestResolveBuildUploadPlatform_MobileAliasMapsToConfiguredKey(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"android-dev": {Command: "./gradlew assembleDebug", Output: "app-debug.apk"},
			},
		},
	}

	resolved, err := resolveBuildUploadPlatform(cfg, "android")
	if err != nil {
		t.Fatalf("resolveBuildUploadPlatform() error = %v", err)
	}
	if resolved.PlatformKey != "android-dev" {
		t.Fatalf("PlatformKey = %q, want %q", resolved.PlatformKey, "android-dev")
	}
	if resolved.DevicePlatform != "android" {
		t.Fatalf("DevicePlatform = %q, want %q", resolved.DevicePlatform, "android")
	}
}

func TestResolveBuildUploadPlatform_LegacyFlatConfigFallback(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			Command: "xcodebuild -scheme App",
			Output:  "build/App.ipa",
		},
	}

	resolved, err := resolveBuildUploadPlatform(cfg, "ios")
	if err != nil {
		t.Fatalf("resolveBuildUploadPlatform() error = %v", err)
	}
	if !resolved.LegacyConfig {
		t.Fatal("LegacyConfig = false, want true")
	}
	if resolved.PlatformKey != "ios" {
		t.Fatalf("PlatformKey = %q, want %q", resolved.PlatformKey, "ios")
	}
	if resolved.Config.Output != "build/App.ipa" {
		t.Fatalf("Config.Output = %q, want %q", resolved.Config.Output, "build/App.ipa")
	}
}

func TestResolveBuildUploadPlatform_UnknownPlatform(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"ios-dev": {Command: "xcodebuild", Output: "ios.ipa"},
			},
		},
	}

	if _, err := resolveBuildUploadPlatform(cfg, "android"); err == nil {
		t.Fatal("resolveBuildUploadPlatform() error = nil, want non-nil")
	}
}
