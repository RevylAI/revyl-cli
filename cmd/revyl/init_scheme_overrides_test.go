package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/config"
)

func TestParseXcodeSchemeOverrides(t *testing.T) {
	overrides, err := parseXcodeSchemeOverrides([]string{"ios=AppScheme", " ios-dev = Dev Scheme ", "ios=Updated"})
	if err != nil {
		t.Fatalf("parseXcodeSchemeOverrides() error = %v", err)
	}

	if got := overrides["ios"]; got != "Updated" {
		t.Fatalf("overrides[ios] = %q, want %q", got, "Updated")
	}
	if got := overrides["ios-dev"]; got != "Dev Scheme" {
		t.Fatalf("overrides[ios-dev] = %q, want %q", got, "Dev Scheme")
	}
}

func TestParseXcodeSchemeOverridesRejectsInvalidFormat(t *testing.T) {
	if _, err := parseXcodeSchemeOverrides([]string{"ios"}); err == nil {
		t.Fatal("expected error for missing '='")
	}
	if _, err := parseXcodeSchemeOverrides([]string{"=MyScheme"}); err == nil {
		t.Fatal("expected error for empty platform key")
	}
	if _, err := parseXcodeSchemeOverrides([]string{"ios="}); err == nil {
		t.Fatal("expected error for empty scheme")
	}
}

func TestApplyXcodeSchemeOverridesRejectsUnknownPlatformKey(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"ios": {},
			},
		},
	}

	err := applyXcodeSchemeOverrides(cfg, map[string]string{"ios-dev": "MyScheme"})
	if err == nil {
		t.Fatal("expected error for unknown platform key")
	}
	if !strings.Contains(err.Error(), "ios-dev") {
		t.Fatalf("error = %q, want to mention unknown key", err.Error())
	}
}

func TestApplyXcodeSchemeOverridesAppliesSchemeAndCommand(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"ios": {
					Command: "xcodebuild -scheme * -configuration Debug",
				},
			},
		},
	}

	if err := applyXcodeSchemeOverrides(cfg, map[string]string{"ios": "MyScheme"}); err != nil {
		t.Fatalf("applyXcodeSchemeOverrides() error = %v", err)
	}

	platformCfg := cfg.Build.Platforms["ios"]
	if platformCfg.Scheme != "MyScheme" {
		t.Fatalf("platform scheme = %q, want %q", platformCfg.Scheme, "MyScheme")
	}
	if !strings.Contains(platformCfg.Command, "-scheme 'MyScheme'") {
		t.Fatalf("platform command = %q, want to contain %q", platformCfg.Command, "-scheme 'MyScheme'")
	}
}

func TestSetBuildPlatformSchemeReplacesExistingSchemeValue(t *testing.T) {
	platformCfg := config.BuildPlatform{
		Command: "xcodebuild -scheme 'OldScheme' -configuration Debug",
		Scheme:  "OldScheme",
	}

	updated := setBuildPlatformScheme(platformCfg, "NewScheme")

	if updated.Scheme != "NewScheme" {
		t.Fatalf("updated scheme = %q, want %q", updated.Scheme, "NewScheme")
	}
	if !strings.Contains(updated.Command, "-scheme 'NewScheme'") {
		t.Fatalf("updated command = %q, want to contain %q", updated.Command, "-scheme 'NewScheme'")
	}
	if strings.Contains(updated.Command, "OldScheme") {
		t.Fatalf("updated command = %q, did not expect old scheme", updated.Command)
	}
}

func TestApplyExpoAppSchemeOverrideUsesExplicitValue(t *testing.T) {
	providerCfg := &config.ProviderConfig{AppScheme: "old-scheme"}
	applyExpoAppSchemeOverride(providerCfg, "new-scheme", false)
	if providerCfg.AppScheme != "new-scheme" {
		t.Fatalf("providerCfg.AppScheme = %q, want %q", providerCfg.AppScheme, "new-scheme")
	}
}

func TestInitSchemeEditStatePromptsOnce(t *testing.T) {
	promptCalls := 0
	contextCalls := 0
	state := newInitSchemeEditState(true, func(message string, defaultYes bool) (bool, error) {
		if message != initEditDetectedSettingsPrompt {
			t.Fatalf("prompt message = %q, want %q", message, initEditDetectedSettingsPrompt)
		}
		promptCalls++
		return true, nil
	}, func() {
		contextCalls++
	})

	if !state.ShouldEdit() {
		t.Fatal("first ShouldEdit() = false, want true")
	}
	if !state.ShouldEdit() {
		t.Fatal("second ShouldEdit() = false, want true")
	}
	if promptCalls != 1 {
		t.Fatalf("prompt calls = %d, want %d", promptCalls, 1)
	}
	if contextCalls != 1 {
		t.Fatalf("context calls = %d, want %d", contextCalls, 1)
	}
}

func TestInitSchemeEditStateNoContextWhenDeclined(t *testing.T) {
	contextCalls := 0
	state := newInitSchemeEditState(true, func(message string, defaultYes bool) (bool, error) {
		return false, nil
	}, func() {
		contextCalls++
	})

	if state.ShouldEdit() {
		t.Fatal("ShouldEdit() = true, want false")
	}
	if contextCalls != 0 {
		t.Fatalf("context calls = %d, want %d", contextCalls, 0)
	}
}

func TestNewInitOverrideOptionsDisablesInteractivePromptWhenExplicitOverridesExist(t *testing.T) {
	opts, err := newInitOverrideOptions([]string{"ios=MyScheme"}, "", true)
	if err != nil {
		t.Fatalf("newInitOverrideOptions() error = %v", err)
	}
	if opts.ShouldPromptForDetectedEdits() {
		t.Fatal("ShouldPromptForDetectedEdits() = true, want false when explicit overrides are provided")
	}
}

func TestInitSchemeEditStateCanAskTransitionsAfterPrompt(t *testing.T) {
	state := newInitSchemeEditState(true, func(message string, defaultYes bool) (bool, error) {
		return false, nil
	}, nil)

	if !state.CanAsk() {
		t.Fatal("CanAsk() = false before first prompt, want true")
	}

	_ = state.ShouldEdit()
	if state.CanAsk() {
		t.Fatal("CanAsk() = true after first prompt, want false")
	}
}

func TestDescribeBuildPlatformStream(t *testing.T) {
	cases := map[string]string{
		"ios-dev":     "iOS development build for local iteration",
		"android-dev": "Android development build for local iteration",
		"ios-ci":      "iOS CI / preview build",
		"android-ci":  "Android CI / preview build",
		"ios-beta":    "iOS build",
		"android-qa":  "Android build",
		"custom":      "Custom build",
	}
	for key, want := range cases {
		if got := describeBuildPlatformStream(key); got != want {
			t.Fatalf("describeBuildPlatformStream(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestDescribeBuildPlatformLinkIncludesAppNamePattern(t *testing.T) {
	cfg := &config.ProjectConfig{
		Project: config.Project{Name: "hira-clapton"},
	}

	got := describeBuildPlatformLink(cfg, "ios-dev")
	want := "iOS development build for local iteration (mobile: ios, app stream name: hira-clapton-ios-dev)"
	if got != want {
		t.Fatalf("describeBuildPlatformLink() = %q, want %q", got, want)
	}
}

func TestExpectedInitAppName(t *testing.T) {
	cfg := &config.ProjectConfig{
		Project: config.Project{Name: "hira-clapton"},
	}

	if got := expectedInitAppName(cfg, "android-ci"); got != "hira-clapton-android-ci" {
		t.Fatalf("expectedInitAppName() = %q, want %q", got, "hira-clapton-android-ci")
	}
	if got := expectedInitAppName(cfg, ""); got != "" {
		t.Fatalf("expectedInitAppName() with empty key = %q, want empty", got)
	}
	if got := expectedInitAppName(&config.ProjectConfig{}, "ios-dev"); got != "" {
		t.Fatalf("expectedInitAppName() without project name = %q, want empty", got)
	}
}

func TestDescribeRuntimeDefaultForBuildKey(t *testing.T) {
	mapping := map[string]string{
		"ios":     "ios-dev",
		"android": "android-dev",
	}

	if got := describeRuntimeDefaultForBuildKey(mapping, "ios-dev"); got != "ios" {
		t.Fatalf("describeRuntimeDefaultForBuildKey(ios-dev) = %q, want %q", got, "ios")
	}
	if got := describeRuntimeDefaultForBuildKey(mapping, "android-dev"); got != "android" {
		t.Fatalf("describeRuntimeDefaultForBuildKey(android-dev) = %q, want %q", got, "android")
	}
	if got := describeRuntimeDefaultForBuildKey(mapping, "ios-ci"); got != "-" {
		t.Fatalf("describeRuntimeDefaultForBuildKey(ios-ci) = %q, want %q", got, "-")
	}

	mapping["android"] = "ios-dev"
	if got := describeRuntimeDefaultForBuildKey(mapping, "ios-dev"); got != "ios, android" {
		t.Fatalf("describeRuntimeDefaultForBuildKey(shared) = %q, want %q", got, "ios, android")
	}
}

func TestOrderedBuildPlatformKeysForReview(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"android-ci":  {},
				"ios-ci":      {},
				"ios-dev":     {},
				"android-dev": {},
				"custom-dev":  {},
				"zeta":        {},
			},
		},
	}

	got := orderedBuildPlatformKeysForReview(cfg)
	want := []string{"ios-dev", "android-dev", "ios-ci", "android-ci", "custom-dev", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("orderedBuildPlatformKeysForReview() = %v, want %v", got, want)
	}
}

func TestPromptBuildSetupReviewWithPromptExpoEditsPlatformConfigs(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			System:  "Expo",
			Command: "top-level-command",
			Output:  "top-level-output",
			Platforms: map[string]config.BuildPlatform{
				"ios-dev": {
					Command: "ios-dev-command",
					Output:  "ios-dev-output",
				},
				"android-dev": {
					Command: "android-dev-command",
					Output:  "android-dev-output",
				},
				"ios-ci": {
					Command: "ios-ci-command",
					Output:  "ios-ci-output",
				},
				"android-ci": {
					Command: "android-ci-command",
					Output:  "android-ci-output",
				},
			},
		},
	}

	var prompts []string
	promptFn := func(label, current string) string {
		prompts = append(prompts, label)
		return current + "-edited"
	}

	promptBuildSetupReviewWithPrompt(cfg, promptFn)

	// Top-level is synced from the first platform entry (ios-dev)
	if cfg.Build.Command != "ios-dev-command-edited" {
		t.Fatalf("top-level build.command = %q; expected synced from first platform", cfg.Build.Command)
	}
	if cfg.Build.Output != "ios-dev-output-edited" {
		t.Fatalf("top-level build.output = %q; expected synced from first platform", cfg.Build.Output)
	}

	wantPrompts := []string{
		"command", "output",
		"command", "output",
		"command", "output",
		"command", "output",
	}
	if !reflect.DeepEqual(prompts, wantPrompts) {
		t.Fatalf("prompt order = %v, want %v", prompts, wantPrompts)
	}

	for _, key := range []string{"ios-dev", "android-dev", "ios-ci", "android-ci"} {
		plat := cfg.Build.Platforms[key]
		if !strings.HasSuffix(plat.Command, "-edited") {
			t.Fatalf("%s command = %q, expected edited suffix", key, plat.Command)
		}
		if !strings.HasSuffix(plat.Output, "-edited") {
			t.Fatalf("%s output = %q, expected edited suffix", key, plat.Output)
		}
	}
}

func TestPromptBuildSetupReviewWithPromptNonExpoUsesTopLevel(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			System:  "Xcode",
			Command: "xcodebuild",
			Output:  "build/app.tar.gz",
			Platforms: map[string]config.BuildPlatform{
				"ios": {
					Command: "xcodebuild -scheme App",
					Output:  "build/ios.tar.gz",
				},
			},
		},
	}

	var prompts []string
	promptFn := func(label, current string) string {
		prompts = append(prompts, label)
		return current + "-edited"
	}

	promptBuildSetupReviewWithPrompt(cfg, promptFn)

	wantPrompts := []string{"command", "output"}
	if !reflect.DeepEqual(prompts, wantPrompts) {
		t.Fatalf("prompts = %v, want %v", prompts, wantPrompts)
	}
	if cfg.Build.Platforms["ios"].Command != "xcodebuild -scheme App-edited" {
		t.Fatalf("platform command = %q, expected edited suffix", cfg.Build.Platforms["ios"].Command)
	}
	if cfg.Build.Platforms["ios"].Output != "build/ios.tar.gz-edited" {
		t.Fatalf("platform output = %q, expected edited suffix", cfg.Build.Platforms["ios"].Output)
	}
	// Top-level is synced from the first (only) platform entry
	if cfg.Build.Command != "xcodebuild -scheme App-edited" {
		t.Fatalf("build.command = %q, want synced from platform", cfg.Build.Command)
	}
	if cfg.Build.Output != "build/ios.tar.gz-edited" {
		t.Fatalf("build.output = %q, want synced from platform", cfg.Build.Output)
	}
}

func TestPrintProjectConfigReviewPromptContextNonExpoShowsBuildInfo(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			System:  "Gradle (Android)",
			Command: "./gradlew assembleDebug",
			Output:  "app/build/outputs/apk/debug/app-debug.apk",
			Platforms: map[string]config.BuildPlatform{
				"android": {
					Command: "./gradlew assembleDebug",
					Output:  "app/build/outputs/apk/debug/app-debug.apk",
				},
			},
		},
	}

	output := captureStdoutAndStderr(t, func() {
		printProjectConfigReviewPromptContext(cfg)
	})

	if !strings.Contains(output, "android command") {
		t.Fatalf("expected output to contain %q, got:\n%s", "android command", output)
	}
	if !strings.Contains(output, "android output") {
		t.Fatalf("expected output to contain %q, got:\n%s", "android output", output)
	}
	if !strings.Contains(output, "app/build/outputs/apk/debug/app-debug.apk") {
		t.Fatalf("expected output to contain the output path, got:\n%s", output)
	}
}

func TestPrintProjectConfigReviewPromptContextNonExpoFlatBuild(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			System:  "Gradle (Android)",
			Command: "./gradlew assembleDebug",
			Output:  "app/build/outputs/apk/debug/app-debug.apk",
		},
	}

	output := captureStdoutAndStderr(t, func() {
		printProjectConfigReviewPromptContext(cfg)
	})

	if !strings.Contains(output, "Build command") {
		t.Fatalf("expected output to contain %q, got:\n%s", "Build command", output)
	}
	if !strings.Contains(output, "Build output") {
		t.Fatalf("expected output to contain %q, got:\n%s", "Build output", output)
	}
	if !strings.Contains(output, "app/build/outputs/apk/debug/app-debug.apk") {
		t.Fatalf("expected output to contain the output path, got:\n%s", output)
	}
}

func TestPrintProjectConfigReviewPromptContextExpoShowsStreamTable(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			System: "Expo",
			Platforms: map[string]config.BuildPlatform{
				"ios-dev":     {Command: "npx eas build --platform ios"},
				"android-dev": {Command: "npx eas build --platform android"},
			},
		},
	}

	output := captureStdoutAndStderr(t, func() {
		printProjectConfigReviewPromptContext(cfg)
	})

	// Expo should show stream table columns, NOT key-value pairs
	if !strings.Contains(output, "STREAM") {
		t.Fatalf("expected Expo output to contain stream table header, got:\n%s", output)
	}
	if strings.Contains(output, "build.command") {
		t.Fatalf("Expo output should not contain non-Expo key-value format, got:\n%s", output)
	}
}
