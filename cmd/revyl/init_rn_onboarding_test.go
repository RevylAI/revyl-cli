package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// ---------------------------------------------------------------------------
// defaultRNBuildTargetForHost
// ---------------------------------------------------------------------------

func TestDefaultRNBuildTargetForHost_DarwinPrefersIOS(t *testing.T) {
	got := defaultRNBuildTargetForHost([]string{"android", "ios"}, "darwin")
	if got != "ios" {
		t.Fatalf("defaultRNBuildTargetForHost(darwin) = %q, want %q", got, "ios")
	}
}

func TestDefaultRNBuildTargetForHost_LinuxPrefersAndroid(t *testing.T) {
	got := defaultRNBuildTargetForHost([]string{"android", "ios"}, "linux")
	if got != "android" {
		t.Fatalf("defaultRNBuildTargetForHost(linux) = %q, want %q", got, "android")
	}
}

func TestDefaultRNBuildTargetForHost_FallsBackToAvailable(t *testing.T) {
	got := defaultRNBuildTargetForHost([]string{"android"}, "darwin")
	if got != "android" {
		t.Fatalf("defaultRNBuildTargetForHost(darwin, android-only) = %q, want %q", got, "android")
	}
}

func TestDefaultRNBuildTargetForHost_EmptyPlatforms(t *testing.T) {
	got := defaultRNBuildTargetForHost(nil, "darwin")
	if got != "" {
		t.Fatalf("defaultRNBuildTargetForHost(nil) = %q, want empty", got)
	}
}

func TestDefaultRNBuildTargetForHost_SinglePlatform(t *testing.T) {
	got := defaultRNBuildTargetForHost([]string{"ios"}, "linux")
	if got != "ios" {
		t.Fatalf("defaultRNBuildTargetForHost(linux, ios-only) = %q, want %q", got, "ios")
	}
}

// ---------------------------------------------------------------------------
// isReactNativeBuildSystem
// ---------------------------------------------------------------------------

func TestIsReactNativeBuildSystem(t *testing.T) {
	tests := []struct {
		system string
		want   bool
	}{
		{"React Native", true},
		{"react native", true},
		{"React Native (bare)", true},
		{"Expo", false},
		{"Expo React Native", false},
		{"Flutter", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isReactNativeBuildSystem(tt.system)
		if got != tt.want {
			t.Errorf("isReactNativeBuildSystem(%q) = %v, want %v", tt.system, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// detectRNPrerequisiteIssues
// ---------------------------------------------------------------------------

func TestDetectRNPrerequisiteIssues_MissingNodeModules(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"react-native":"*"}}`), 0644)

	issues := detectRNPrerequisiteIssues(dir, "android")
	if len(issues) == 0 {
		t.Fatal("expected at least one issue for missing node_modules")
	}
	if issues[0].BootstrapCmd != "npm install" {
		t.Fatalf("expected npm install, got %q", issues[0].BootstrapCmd)
	}
}

func TestDetectRNPrerequisiteIssues_YarnLockDetected(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{}`), 0644)
	os.WriteFile(filepath.Join(dir, "yarn.lock"), []byte(""), 0644)

	issues := detectRNPrerequisiteIssues(dir, "android")
	if len(issues) == 0 {
		t.Fatal("expected issue for missing node_modules")
	}
	if issues[0].BootstrapCmd != "yarn install" {
		t.Fatalf("expected yarn install, got %q", issues[0].BootstrapCmd)
	}
}

func TestDetectRNPrerequisiteIssues_PnpmLockDetected(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{}`), 0644)
	os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte(""), 0644)

	issues := detectRNPrerequisiteIssues(dir, "android")
	if len(issues) == 0 {
		t.Fatal("expected issue for missing node_modules")
	}
	if issues[0].BootstrapCmd != "pnpm install" {
		t.Fatalf("expected pnpm install, got %q", issues[0].BootstrapCmd)
	}
}

func TestDetectRNPrerequisiteIssues_NodeModulesPresent(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "node_modules"), 0755)

	issues := detectRNPrerequisiteIssues(dir, "android")
	if len(issues) != 0 {
		t.Fatalf("expected no issues when node_modules exists, got %d", len(issues))
	}
}

func TestDetectRNPrerequisiteIssues_MissingPods(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "node_modules"), 0755)
	os.MkdirAll(filepath.Join(dir, "ios"), 0755)
	os.WriteFile(filepath.Join(dir, "ios", "Podfile"), []byte(""), 0644)

	issues := detectRNPrerequisiteIssues(dir, "ios")
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue (missing Pods), got %d", len(issues))
	}
	if issues[0].BootstrapCmd != "cd ios && pod install" {
		t.Fatalf("expected pod install, got %q", issues[0].BootstrapCmd)
	}
}

func TestDetectRNPrerequisiteIssues_MissingPodsWithGemfile(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "node_modules"), 0755)
	os.MkdirAll(filepath.Join(dir, "ios"), 0755)
	os.WriteFile(filepath.Join(dir, "ios", "Podfile"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "Gemfile"), []byte(""), 0644)

	issues := detectRNPrerequisiteIssues(dir, "ios")
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	if issues[0].BootstrapCmd != "cd ios && bundle exec pod install" {
		t.Fatalf("expected bundle exec pod install, got %q", issues[0].BootstrapCmd)
	}
}

func TestDetectRNPrerequisiteIssues_PodsPresent(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "node_modules"), 0755)
	os.MkdirAll(filepath.Join(dir, "ios", "Pods"), 0755)
	os.WriteFile(filepath.Join(dir, "ios", "Podfile"), []byte(""), 0644)

	issues := detectRNPrerequisiteIssues(dir, "ios")
	if len(issues) != 0 {
		t.Fatalf("expected no issues when Pods dir exists, got %d", len(issues))
	}
}

func TestDetectRNPrerequisiteIssues_BothMissing(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "ios"), 0755)
	os.WriteFile(filepath.Join(dir, "ios", "Podfile"), []byte(""), 0644)

	issues := detectRNPrerequisiteIssues(dir, "ios")
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues (node_modules + Pods), got %d", len(issues))
	}
}

// ---------------------------------------------------------------------------
// classifyRNBuildFailure
// ---------------------------------------------------------------------------

func TestClassifyRNBuildFailure_GradlePlugin(t *testing.T) {
	err := errors.New("Could not find @react-native/gradle-plugin")
	cause, fix := classifyRNBuildFailure(err, "android", t.TempDir())
	if cause == "" {
		t.Fatal("expected a cause for gradle-plugin error")
	}
	if fix != "npm install" {
		t.Fatalf("expected npm install fix, got %q", fix)
	}
}

func TestClassifyRNBuildFailure_NodeModules(t *testing.T) {
	err := errors.New("Cannot find module 'react-native/package.json' in node_modules")
	cause, fix := classifyRNBuildFailure(err, "android", t.TempDir())
	if cause == "" {
		t.Fatal("expected a cause for node_modules error")
	}
	if fix == "" {
		t.Fatal("expected a fix command")
	}
}

func TestClassifyRNBuildFailure_NodeModulesYarn(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "yarn.lock"), []byte(""), 0644)

	err := errors.New("Cannot find module '@react-native' in node_modules")
	cause, fix := classifyRNBuildFailure(err, "android", dir)
	if cause == "" {
		t.Fatal("expected a cause")
	}
	if fix != "yarn install" {
		t.Fatalf("expected yarn install, got %q", fix)
	}
}

func TestClassifyRNBuildFailure_GradlewPermission(t *testing.T) {
	err := errors.New("gradlew: permission denied")
	cause, fix := classifyRNBuildFailure(err, "android", t.TempDir())
	if cause == "" {
		t.Fatal("expected a cause")
	}
	if fix != "chmod +x android/gradlew" {
		t.Fatalf("expected chmod fix, got %q", fix)
	}
}

func TestClassifyRNBuildFailure_IOSPods(t *testing.T) {
	err := errors.New("Unable to find a target named 'RnBareMinimal'")
	cause, fix := classifyRNBuildFailure(err, "ios", t.TempDir())
	if cause == "" {
		t.Fatal("expected a cause for iOS target error")
	}
	if fix != "cd ios && pod install" {
		t.Fatalf("expected pod install fix, got %q", fix)
	}
}

func TestClassifyRNBuildFailure_UnknownError(t *testing.T) {
	err := errors.New("something completely unexpected")
	cause, fix := classifyRNBuildFailure(err, "android", t.TempDir())
	if cause != "" || fix != "" {
		t.Fatalf("expected empty cause/fix for unknown error, got cause=%q fix=%q", cause, fix)
	}
}

// ---------------------------------------------------------------------------
// printRNDeferredBuildHint (unit test via helper extraction)
// ---------------------------------------------------------------------------

func TestDefaultRNBuildTargetForHost_PreservesKeyNames(t *testing.T) {
	got := defaultRNBuildTargetForHost([]string{"ios-dev", "android-dev"}, "darwin")
	if got != "ios-dev" {
		t.Fatalf("expected ios-dev, got %q", got)
	}
}

func TestDefaultRNBuildTargetForHost_WindowsFallsBackToAndroid(t *testing.T) {
	got := defaultRNBuildTargetForHost([]string{"ios", "android"}, "windows")
	if got != "android" {
		t.Fatalf("expected android on windows, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Integration: RN platform selection mirrors Expo semantics
// ---------------------------------------------------------------------------

func TestDefaultRNAndExpoPreferSamePlatform(t *testing.T) {
	platforms := []string{"ios", "android"}

	rnTarget := defaultRNBuildTargetForHost(platforms, "darwin")
	expoTargets := defaultExpoDevBuildTargetsForHost(platforms, "darwin")

	if len(expoTargets) == 0 {
		t.Fatal("expo returned no targets")
	}
	if mobilePlatformForBuildKey(rnTarget) != mobilePlatformForBuildKey(expoTargets[0]) {
		t.Fatalf("RN picked %q but Expo picked %q — should agree on darwin", rnTarget, expoTargets[0])
	}

	rnTarget = defaultRNBuildTargetForHost(platforms, "linux")
	expoTargets = defaultExpoDevBuildTargetsForHost(platforms, "linux")

	if len(expoTargets) == 0 {
		t.Fatal("expo returned no targets for linux")
	}
	want := mobilePlatformForBuildKey(expoTargets[0])
	got := mobilePlatformForBuildKey(rnTarget)
	if got != want {
		t.Fatalf("RN picked %q but Expo picked %q — should agree on linux", got, want)
	}
}

// ---------------------------------------------------------------------------
// Helpers used by the tests
// ---------------------------------------------------------------------------

func TestDetectRNPrerequisiteIssues_AndroidIgnoresPods(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "ios"), 0755)
	os.WriteFile(filepath.Join(dir, "ios", "Podfile"), []byte(""), 0644)
	os.MkdirAll(filepath.Join(dir, "node_modules"), 0755)

	issues := detectRNPrerequisiteIssues(dir, "android")
	if len(issues) != 0 {
		t.Fatalf("expected no android issues when ios Pods are missing, got %d", len(issues))
	}
}

func TestDetectRNPrerequisiteIssues_ReturnsInBootstrapOrder(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "ios"), 0755)
	os.WriteFile(filepath.Join(dir, "ios", "Podfile"), []byte(""), 0644)

	issues := detectRNPrerequisiteIssues(dir, "ios")
	if len(issues) < 2 {
		t.Fatalf("expected 2 issues, got %d", len(issues))
	}

	got := []string{issues[0].BootstrapCmd, issues[1].BootstrapCmd}
	want := []string{"npm install", "cd ios && pod install"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected bootstrap order %v, got %v", want, got)
	}
}
