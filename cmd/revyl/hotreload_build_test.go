package main

import (
	"testing"

	"github.com/revyl/cli/internal/config"
)

func TestResolveHotReloadBuildPlatform_UsesProviderMapping(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"ios-dev": {},
				"ios-ci":  {},
			},
		},
	}
	providerCfg := &config.ProviderConfig{
		PlatformKeys: map[string]string{
			"ios": "ios-ci",
		},
	}

	platformKey, devicePlatform, err := resolveHotReloadBuildPlatform(cfg, providerCfg, "ios", "ios")
	if err != nil {
		t.Fatalf("resolveHotReloadBuildPlatform() error = %v", err)
	}
	if platformKey != "ios-ci" {
		t.Fatalf("platformKey = %q, want %q", platformKey, "ios-ci")
	}
	if devicePlatform != "ios" {
		t.Fatalf("devicePlatform = %q, want %q", devicePlatform, "ios")
	}
}

func TestResolveHotReloadBuildPlatform_PrefersDevKey(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"ios-ci":  {},
				"ios-dev": {},
				"ios":     {},
			},
		},
	}

	platformKey, devicePlatform, err := resolveHotReloadBuildPlatform(cfg, nil, "ios", "ios")
	if err != nil {
		t.Fatalf("resolveHotReloadBuildPlatform() error = %v", err)
	}
	if platformKey != "ios-dev" {
		t.Fatalf("platformKey = %q, want %q", platformKey, "ios-dev")
	}
	if devicePlatform != "ios" {
		t.Fatalf("devicePlatform = %q, want %q", devicePlatform, "ios")
	}
}

func TestResolveHotReloadBuildPlatform_AcceptsExplicitPlatformKey(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"android-dev": {},
			},
		},
	}

	platformKey, devicePlatform, err := resolveHotReloadBuildPlatform(cfg, nil, "android-dev", "ios")
	if err != nil {
		t.Fatalf("resolveHotReloadBuildPlatform() error = %v", err)
	}
	if platformKey != "android-dev" {
		t.Fatalf("platformKey = %q, want %q", platformKey, "android-dev")
	}
	if devicePlatform != "android" {
		t.Fatalf("devicePlatform = %q, want %q", devicePlatform, "android")
	}
}
