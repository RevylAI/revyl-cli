package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseXcodebuildListOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name: "single scheme",
			input: `Information about project "MyApp":
    Targets:
        MyApp

    Build Configurations:
        Debug
        Release

    Schemes:
        MyApp
`,
			expected: []string{"MyApp"},
		},
		{
			name: "multiple schemes",
			input: `Information about project "MyApp":
    Targets:
        MyApp
        MyAppTests

    Build Configurations:
        Debug
        Release

    Schemes:
        MyApp
        MyApp-Staging
        MyAppTests
`,
			expected: []string{"MyApp", "MyApp-Staging", "MyAppTests"},
		},
		{
			name:     "no schemes section",
			input:    `Information about project "MyApp":`,
			expected: nil,
		},
		{
			name:     "empty output",
			input:    "",
			expected: nil,
		},
		{
			name: "schemes followed by another section",
			input: `Information about workspace "MyApp":
    Schemes:
        MyApp
        MyAppUITests

    Build Configurations:
        Debug
`,
			expected: []string{"MyApp", "MyAppUITests"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseXcodebuildListOutput(tt.input)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d schemes, got %d: %v", len(tt.expected), len(result), result)
			}
			for i, scheme := range result {
				if scheme != tt.expected[i] {
					t.Errorf("scheme[%d]: expected %q, got %q", i, tt.expected[i], scheme)
				}
			}
		})
	}
}

func TestApplySchemeToCommand(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		scheme   string
		expected string
	}{
		{
			name:     "normal scheme name",
			command:  "xcodebuild -workspace MyApp.xcworkspace -scheme * -configuration Debug",
			scheme:   "MyApp",
			expected: "xcodebuild -workspace MyApp.xcworkspace -scheme 'MyApp' -configuration Debug",
		},
		{
			name:     "scheme with spaces gets quoted",
			command:  "xcodebuild -workspace MyApp.xcworkspace -scheme * -configuration Debug",
			scheme:   "My App",
			expected: "xcodebuild -workspace MyApp.xcworkspace -scheme 'My App' -configuration Debug",
		},
		{
			name:     "scheme with single quote gets escaped",
			command:  "xcodebuild -workspace MyApp.xcworkspace -scheme * -configuration Debug",
			scheme:   "My'App",
			expected: "xcodebuild -workspace MyApp.xcworkspace -scheme 'My'\\''App' -configuration Debug",
		},
		{
			name:     "empty scheme is no-op",
			command:  "xcodebuild -workspace MyApp.xcworkspace -scheme * -configuration Debug",
			scheme:   "",
			expected: "xcodebuild -workspace MyApp.xcworkspace -scheme * -configuration Debug",
		},
		{
			name:     "command without -scheme * is no-op",
			command:  "./gradlew assembleDebug",
			scheme:   "MyApp",
			expected: "./gradlew assembleDebug",
		},
		{
			name:     "react native ios command",
			command:  "cd ios && xcodebuild -workspace *.xcworkspace -scheme * -configuration Debug -sdk iphonesimulator -derivedDataPath build",
			scheme:   "MyRNApp",
			expected: "cd ios && xcodebuild -workspace *.xcworkspace -scheme 'MyRNApp' -configuration Debug -sdk iphonesimulator -derivedDataPath build",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ApplySchemeToCommand(tt.command, tt.scheme)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestDetectReactNative_FullProjectUsesPodBootstrapForFreshIOSClone(t *testing.T) {
	dir := t.TempDir()
	writeDetectTestFile(t, filepath.Join(dir, "package.json"), `{"name":"rn-bare-minimal","dependencies":{"react-native":"0.74.0"}}`)
	writeDetectTestFile(t, filepath.Join(dir, "android", "app", "build.gradle"), "apply plugin: \"com.android.application\"\n")
	writeDetectTestFile(t, filepath.Join(dir, "ios", "Podfile"), "platform :ios, '13.4'\n")
	if err := os.MkdirAll(filepath.Join(dir, "ios", "RnBareMinimal.xcodeproj"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if detected.System != SystemReactNative {
		t.Fatalf("System = %v, want %v", detected.System, SystemReactNative)
	}

	iosPlatform, ok := detected.Platforms["ios"]
	if !ok {
		t.Fatal("expected ios platform to be detected")
	}
	if !strings.Contains(iosPlatform.Command, "pod install") {
		t.Fatalf("ios command = %q, want pod install bootstrap", iosPlatform.Command)
	}
	if !strings.Contains(iosPlatform.Command, "-workspace RnBareMinimal.xcworkspace") {
		t.Fatalf("ios command = %q, want workspace build after pod install", iosPlatform.Command)
	}
	if !strings.Contains(iosPlatform.Command, "-destination 'generic/platform=iOS Simulator'") {
		t.Fatalf("ios command = %q, want explicit generic simulator destination", iosPlatform.Command)
	}
	if iosPlatform.Output != "ios/build/Build/Products/Debug-iphonesimulator/*.app" {
		t.Fatalf("ios output = %q, want standard RN ios output", iosPlatform.Output)
	}

	androidPlatform, ok := detected.Platforms["android"]
	if !ok {
		t.Fatal("expected android platform to be detected")
	}
	if androidPlatform.Command != "cd android && ./gradlew assembleDebug" {
		t.Fatalf("android command = %q, want gradle debug build", androidPlatform.Command)
	}
	if detected.Command != androidPlatform.Command {
		t.Fatalf("default command = %q, want android default %q", detected.Command, androidPlatform.Command)
	}
}

func TestDetectReactNative_AndroidOnlyProjectSkipsIOSPlatform(t *testing.T) {
	dir := t.TempDir()
	writeDetectTestFile(t, filepath.Join(dir, "package.json"), `{"name":"rn-android-minimal","dependencies":{"react-native":"0.74.0"}}`)
	writeDetectTestFile(t, filepath.Join(dir, "android", "app", "build.gradle"), "apply plugin: \"com.android.application\"\n")

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if _, ok := detected.Platforms["ios"]; ok {
		t.Fatal("did not expect ios platform for Android-only RN project")
	}
	if _, ok := detected.Platforms["android"]; !ok {
		t.Fatal("expected android platform for Android-only RN project")
	}
}

func TestDetectReactNative_IncompleteIOSKeepsPlaceholderPlatform(t *testing.T) {
	dir := t.TempDir()
	writeDetectTestFile(t, filepath.Join(dir, "package.json"), `{"name":"rn-bare-minimal","dependencies":{"react-native":"0.74.0"}}`)
	writeDetectTestFile(t, filepath.Join(dir, "android", "app", "build.gradle"), "apply plugin: \"com.android.application\"\n")
	if err := os.MkdirAll(filepath.Join(dir, "ios"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	iosPlatform, ok := detected.Platforms["ios"]
	if !ok {
		t.Fatal("expected placeholder ios platform to be detected")
	}
	if iosPlatform.Command != "" || iosPlatform.Output != "" {
		t.Fatalf("placeholder ios platform = %+v, want empty command/output", iosPlatform)
	}
	if !strings.Contains(iosPlatform.IncompleteReason, "no .xcodeproj or .xcworkspace") {
		t.Fatalf("ios incomplete reason = %q, want placeholder guidance", iosPlatform.IncompleteReason)
	}
}

func writeDetectTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Bazel detection tests
// ---------------------------------------------------------------------------

func TestDetectBazel_ModuleBazel(t *testing.T) {
	dir := t.TempDir()
	writeDetectTestFile(t, filepath.Join(dir, "MODULE.bazel"), `module(name = "myapp")`)
	if err := os.MkdirAll(filepath.Join(dir, "ios"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if detected.System != SystemBazel {
		t.Fatalf("System = %v, want %v", detected.System, SystemBazel)
	}
	if _, ok := detected.Platforms["ios"]; !ok {
		t.Fatal("expected ios platform placeholder for Bazel project with ios/")
	}
}

func TestDetectBazel_WorkspaceBazel(t *testing.T) {
	dir := t.TempDir()
	writeDetectTestFile(t, filepath.Join(dir, "WORKSPACE.bazel"), "")

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if detected.System != SystemBazel {
		t.Fatalf("System = %v, want %v", detected.System, SystemBazel)
	}
}

func TestDetectBazel_LegacyWorkspace(t *testing.T) {
	dir := t.TempDir()
	writeDetectTestFile(t, filepath.Join(dir, "WORKSPACE"), "")
	if err := os.MkdirAll(filepath.Join(dir, "android"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if detected.System != SystemBazel {
		t.Fatalf("System = %v, want %v", detected.System, SystemBazel)
	}
	if _, ok := detected.Platforms["android"]; !ok {
		t.Fatal("expected android platform for Bazel project with android/")
	}
}

func TestDetectBazel_BothPlatforms(t *testing.T) {
	dir := t.TempDir()
	writeDetectTestFile(t, filepath.Join(dir, "MODULE.bazel"), "")
	if err := os.MkdirAll(filepath.Join(dir, "ios"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "android"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if _, ok := detected.Platforms["ios"]; !ok {
		t.Fatal("expected ios platform")
	}
	if _, ok := detected.Platforms["android"]; !ok {
		t.Fatal("expected android platform")
	}
}

func TestDetectBazel_IOSApplicationTarget(t *testing.T) {
	dir := t.TempDir()
	writeDetectTestFile(t, filepath.Join(dir, "MODULE.bazel"), `module(name = "myapp")`)

	iosDir := filepath.Join(dir, "ios")
	if err := os.MkdirAll(iosDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeDetectTestFile(t, filepath.Join(iosDir, "BUILD.bazel"), `
load("@build_bazel_rules_apple//apple:ios.bzl", "ios_application")

ios_application(
    name = "MyApp",
    bundle_id = "com.example.myapp",
    families = ["iphone"],
    minimum_os_version = "15.0",
    deps = [":lib"],
)
`)

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if detected.System != SystemBazel {
		t.Fatalf("System = %v, want %v", detected.System, SystemBazel)
	}
	iosPlatform, ok := detected.Platforms["ios"]
	if !ok {
		t.Fatal("expected ios platform")
	}
	if iosPlatform.Command != "bazel build //ios:MyApp -c dbg --ios_multi_cpus=sim_arm64" {
		t.Fatalf("ios command = %q, want concrete target command", iosPlatform.Command)
	}
	if iosPlatform.Output != "bazel-bin/ios/MyApp_archive-root/Payload/MyApp.app" {
		t.Fatalf("ios output = %q, want concrete artifact path", iosPlatform.Output)
	}
	if iosPlatform.IncompleteReason != "" {
		t.Fatalf("ios should be concrete, got IncompleteReason = %q", iosPlatform.IncompleteReason)
	}
}

func TestDetectBazel_ConcreteAndroidAndIOS(t *testing.T) {
	dir := t.TempDir()
	writeDetectTestFile(t, filepath.Join(dir, "MODULE.bazel"), `module(name = "myapp")`)

	appDir := filepath.Join(dir, "app")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeDetectTestFile(t, filepath.Join(appDir, "BUILD.bazel"), `
load("@rules_android//android:rules.bzl", "android_binary")

android_binary(
    name = "myapp",
    manifest = "AndroidManifest.xml",
)
`)

	iosDir := filepath.Join(dir, "ios")
	if err := os.MkdirAll(iosDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeDetectTestFile(t, filepath.Join(iosDir, "BUILD.bazel"), `
load("@build_bazel_rules_apple//apple:ios.bzl", "ios_application")

ios_application(
    name = "myapp_ios",
    bundle_id = "com.example.myapp",
)
`)

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	android := detected.Platforms["android"]
	if android.Command == "" {
		t.Fatal("expected concrete android platform")
	}

	ios := detected.Platforms["ios"]
	if ios.Command == "" {
		t.Fatal("expected concrete ios platform")
	}
	if ios.Command != "bazel build //ios:myapp_ios -c dbg --ios_multi_cpus=sim_arm64" {
		t.Fatalf("ios command = %q, want concrete target command", ios.Command)
	}
}

func TestDetectBazel_ExpoTakesPrecedenceOverBazel(t *testing.T) {
	dir := t.TempDir()
	writeDetectTestFile(t, filepath.Join(dir, "MODULE.bazel"), "")
	writeDetectTestFile(t, filepath.Join(dir, "eas.json"), "{}")

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if detected.System != SystemExpo {
		t.Fatalf("System = %v, want Expo to take precedence over Bazel", detected.System)
	}
}

// ---------------------------------------------------------------------------
// KMP detection tests
// ---------------------------------------------------------------------------

func TestDetectKMP_SharedAndroidIOSWithGradleMarker(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "shared"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "iosApp"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "androidApp"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeDetectTestFile(t, filepath.Join(dir, "androidApp", "build.gradle.kts"), "android {}")
	writeDetectTestFile(t, filepath.Join(dir, "settings.gradle.kts"),
		`plugins { id("org.jetbrains.kotlin.multiplatform") }`)

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if detected.System != SystemKMP {
		t.Fatalf("System = %v, want %v", detected.System, SystemKMP)
	}
	if _, ok := detected.Platforms["ios"]; !ok {
		t.Fatal("expected ios platform for KMP project with iosApp/")
	}
	if _, ok := detected.Platforms["android"]; !ok {
		t.Fatal("expected android platform for KMP project with androidApp/")
	}
}

func TestDetectKMP_ComposeMultiplatform(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "shared"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "composeApp"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeDetectTestFile(t, filepath.Join(dir, "composeApp", "build.gradle.kts"), "android {}")
	writeDetectTestFile(t, filepath.Join(dir, "shared", "build.gradle.kts"),
		`kotlin("multiplatform")`)

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if detected.System != SystemKMP {
		t.Fatalf("System = %v, want %v", detected.System, SystemKMP)
	}
	if _, ok := detected.Platforms["android"]; !ok {
		t.Fatal("expected android platform for Compose Multiplatform project")
	}
}

func TestDetectKMP_AndroidOnlyKMP(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "shared"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "androidApp"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeDetectTestFile(t, filepath.Join(dir, "androidApp", "build.gradle.kts"), "android {}")
	writeDetectTestFile(t, filepath.Join(dir, "settings.gradle.kts"),
		`id("org.jetbrains.kotlin.multiplatform")`)

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if detected.System != SystemKMP {
		t.Fatalf("System = %v, want %v", detected.System, SystemKMP)
	}
	if _, ok := detected.Platforms["ios"]; ok {
		t.Fatal("did not expect ios platform for Android-only KMP project")
	}
}

// ---------------------------------------------------------------------------
// KMP negative tests (precision over recall)
// ---------------------------------------------------------------------------

func TestDetectKMP_SharedDirAloneIsNotKMP(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "shared"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeDetectTestFile(t, filepath.Join(dir, "build.gradle.kts"), "android {}")

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if detected.System == SystemKMP {
		t.Fatal("shared/ directory alone should not trigger KMP detection")
	}
}

func TestDetectKMP_ComposeAppWithoutKMPMarkerIsGradle(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "shared"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "composeApp"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeDetectTestFile(t, filepath.Join(dir, "build.gradle.kts"),
		`plugins { id("com.android.application") }`)

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if detected.System == SystemKMP {
		t.Fatal("composeApp/ without KMP Gradle marker should not trigger KMP detection")
	}
}

func TestDetectKMP_SharedAndAndroidAppWithoutKMPMarkerIsGradle(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "shared"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "androidApp"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeDetectTestFile(t, filepath.Join(dir, "build.gradle.kts"),
		`plugins { id("com.android.application") }`)

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if detected.System == SystemKMP {
		t.Fatal("shared/ + androidApp/ without KMP Gradle markers should not trigger KMP detection")
	}
}

func TestDetectKMP_ExpoProjectIsNotKMP(t *testing.T) {
	dir := t.TempDir()
	writeDetectTestFile(t, filepath.Join(dir, "eas.json"), "{}")
	if err := os.MkdirAll(filepath.Join(dir, "shared"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "androidApp"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeDetectTestFile(t, filepath.Join(dir, "settings.gradle.kts"),
		`id("org.jetbrains.kotlin.multiplatform")`)

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if detected.System == SystemKMP {
		t.Fatal("Expo project should take precedence even when KMP markers exist")
	}
	if detected.System != SystemExpo {
		t.Fatalf("System = %v, want Expo", detected.System)
	}
}

func TestDetectKMP_ReactNativeIsNotKMP(t *testing.T) {
	dir := t.TempDir()
	writeDetectTestFile(t, filepath.Join(dir, "package.json"),
		`{"dependencies":{"react-native":"0.74.0"}}`)
	if err := os.MkdirAll(filepath.Join(dir, "shared"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "androidApp"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeDetectTestFile(t, filepath.Join(dir, "settings.gradle.kts"),
		`id("org.jetbrains.kotlin.multiplatform")`)

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if detected.System == SystemKMP {
		t.Fatal("React Native project should take precedence over KMP")
	}
	if detected.System != SystemReactNative {
		t.Fatalf("System = %v, want React Native", detected.System)
	}
}

func TestDetectKMP_BazelWithKMPLayoutIsBazel(t *testing.T) {
	dir := t.TempDir()
	writeDetectTestFile(t, filepath.Join(dir, "MODULE.bazel"), "")
	if err := os.MkdirAll(filepath.Join(dir, "shared"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "androidApp"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeDetectTestFile(t, filepath.Join(dir, "settings.gradle.kts"),
		`id("org.jetbrains.kotlin.multiplatform")`)

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if detected.System != SystemBazel {
		t.Fatalf("System = %v, want Bazel to take precedence over KMP", detected.System)
	}
}

// ---------------------------------------------------------------------------
// Existing stack guardrail tests
// ---------------------------------------------------------------------------

func TestDetect_PlainGradleStaysGradle(t *testing.T) {
	dir := t.TempDir()
	writeDetectTestFile(t, filepath.Join(dir, "build.gradle"), "apply plugin: 'com.android.application'")

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if detected.System != SystemGradle {
		t.Fatalf("System = %v, want Gradle", detected.System)
	}
}

func TestDetect_PlainXcodeStaysXcode(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "MyApp.xcodeproj"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	detected, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if detected.System != SystemXcode {
		t.Fatalf("System = %v, want Xcode", detected.System)
	}
}

func TestParseBuildSystem_BazelAndKMP(t *testing.T) {
	if ParseBuildSystem("Bazel") != SystemBazel {
		t.Fatal("ParseBuildSystem(Bazel) should return SystemBazel")
	}
	if ParseBuildSystem("bazel") != SystemBazel {
		t.Fatal("ParseBuildSystem(bazel) should return SystemBazel")
	}
	if ParseBuildSystem("Kotlin Multiplatform") != SystemKMP {
		t.Fatal("ParseBuildSystem(Kotlin Multiplatform) should return SystemKMP")
	}
	if ParseBuildSystem("kmp") != SystemKMP {
		t.Fatal("ParseBuildSystem(kmp) should return SystemKMP")
	}
}

func TestIsRebuildOnly_BazelAndKMP(t *testing.T) {
	if !SystemBazel.IsRebuildOnly() {
		t.Fatal("Bazel should be rebuild-only")
	}
	if !SystemKMP.IsRebuildOnly() {
		t.Fatal("KMP should be rebuild-only")
	}
}

// ---------------------------------------------------------------------------
// FindSimulatorBuildInDir tests
// ---------------------------------------------------------------------------

// createSyntheticDerivedData builds a fake DerivedData layout and returns the root.
func createSyntheticDerivedData(t *testing.T, projectName string, appNames []string) string {
	t.Helper()
	root := t.TempDir()
	hashDir := filepath.Join(root, projectName+"-abcdef1234567890", "Build", "Products", "Debug-iphonesimulator")
	if err := os.MkdirAll(hashDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for _, name := range appNames {
		appDir := filepath.Join(hashDir, name)
		if err := os.MkdirAll(appDir, 0755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		infoPath := filepath.Join(appDir, "Info.plist")
		if err := os.WriteFile(infoPath, []byte("<plist/>"), 0644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	return root
}

func TestFindSimulatorBuildInDir_FindsApp(t *testing.T) {
	root := createSyntheticDerivedData(t, "MyApp", []string{"MyApp.app"})
	result := FindSimulatorBuildInDir(root, "MyApp")
	if result == nil {
		t.Fatal("FindSimulatorBuildInDir() returned nil, want a result")
	}
	if !strings.HasSuffix(result.Path, "MyApp.app") {
		t.Fatalf("result.Path = %q, want path ending in MyApp.app", result.Path)
	}
	if result.ModTime.IsZero() {
		t.Fatal("result.ModTime is zero, want a real timestamp")
	}
}

func TestFindSimulatorBuildInDir_SkipsTestBundles(t *testing.T) {
	root := createSyntheticDerivedData(t, "MyApp", []string{"MyAppTests.app", "MyAppUITests.app"})
	result := FindSimulatorBuildInDir(root, "MyApp")
	if result != nil {
		t.Fatalf("FindSimulatorBuildInDir() = %q, want nil (only test bundles)", result.Path)
	}
}

func TestFindSimulatorBuildInDir_PrefersAppOverTests(t *testing.T) {
	root := createSyntheticDerivedData(t, "MyApp", []string{"MyApp.app", "MyAppTests.app"})
	result := FindSimulatorBuildInDir(root, "MyApp")
	if result == nil {
		t.Fatal("FindSimulatorBuildInDir() returned nil")
	}
	if !strings.HasSuffix(result.Path, "MyApp.app") {
		t.Fatalf("result.Path = %q, want MyApp.app (not test bundle)", result.Path)
	}
}

func TestFindSimulatorBuildInDir_NoMatchingProject(t *testing.T) {
	root := createSyntheticDerivedData(t, "OtherProject", []string{"OtherProject.app"})
	result := FindSimulatorBuildInDir(root, "MyApp")
	if result != nil {
		t.Fatalf("FindSimulatorBuildInDir() = %q, want nil (project mismatch)", result.Path)
	}
}

func TestFindSimulatorBuildInDir_EmptyRoot(t *testing.T) {
	root := t.TempDir()
	result := FindSimulatorBuildInDir(root, "MyApp")
	if result != nil {
		t.Fatalf("FindSimulatorBuildInDir() = %q, want nil", result.Path)
	}
}

func TestSimulatorBuildResult_IsStale(t *testing.T) {
	fresh := &SimulatorBuildResult{Path: "/tmp/MyApp.app", ModTime: time.Now().Add(-1 * time.Hour)}
	if fresh.IsStale(4 * time.Hour) {
		t.Fatal("1-hour-old build should not be stale at 4h threshold")
	}

	old := &SimulatorBuildResult{Path: "/tmp/MyApp.app", ModTime: time.Now().Add(-6 * time.Hour)}
	if !old.IsStale(4 * time.Hour) {
		t.Fatal("6-hour-old build should be stale at 4h threshold")
	}
}

func TestIsTestRunnerBundle(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/path/to/MyApp.app", false},
		{"/path/to/MyAppTests.app", true},
		{"/path/to/MyAppUITests.app", true},
		{"/path/to/TestRunner.app", false},
	}
	for _, tt := range tests {
		t.Run(filepath.Base(tt.path), func(t *testing.T) {
			if got := isTestRunnerBundle(tt.path); got != tt.want {
				t.Fatalf("isTestRunnerBundle(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestXcodeProjectBaseName(t *testing.T) {
	dir := t.TempDir()
	projDir := filepath.Join(dir, "MyApp.xcodeproj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	name := xcodeProjectBaseName(dir)
	if name != "MyApp" {
		t.Fatalf("xcodeProjectBaseName() = %q, want %q", name, "MyApp")
	}
}

func TestXcodeProjectBaseName_NoProject(t *testing.T) {
	dir := t.TempDir()
	name := xcodeProjectBaseName(dir)
	if name != "" {
		t.Fatalf("xcodeProjectBaseName() = %q, want empty", name)
	}
}

// ---------------------------------------------------------------------------
// Dogfood matrix: comprehensive detection validation across all stack shapes
// ---------------------------------------------------------------------------

type dogfoodFixture struct {
	name          string
	dirs          []string
	files         map[string]string
	wantSystem    BuildSystem
	wantPlatforms []string
	notWantSystem BuildSystem
}

func TestDogfoodMatrix(t *testing.T) {
	fixtures := []dogfoodFixture{
		{
			name: "kmp-shared-ios-android",
			dirs: []string{"shared", "iosApp", "androidApp"},
			files: map[string]string{
				"settings.gradle.kts":           `id("org.jetbrains.kotlin.multiplatform")`,
				"androidApp/build.gradle.kts":   "android {}",
				"iosApp/iosApp.xcodeproj/dummy": "",
			},
			wantSystem:    SystemKMP,
			wantPlatforms: []string{"ios", "android"},
		},
		{
			name: "kmp-compose-multiplatform",
			dirs: []string{"shared", "composeApp"},
			files: map[string]string{
				"shared/build.gradle.kts":     `kotlin("multiplatform")`,
				"composeApp/build.gradle.kts": "android {}",
			},
			wantSystem:    SystemKMP,
			wantPlatforms: []string{"android"},
		},
		{
			name: "kmp-android-only",
			dirs: []string{"shared", "androidApp"},
			files: map[string]string{
				"settings.gradle.kts":         `id("org.jetbrains.kotlin.multiplatform")`,
				"androidApp/build.gradle.kts": "android {}",
			},
			wantSystem:    SystemKMP,
			wantPlatforms: []string{"android"},
		},
		{
			name: "kmp-ios-only",
			dirs: []string{"shared", "iosApp"},
			files: map[string]string{
				"settings.gradle.kts":           `id("org.jetbrains.kotlin.multiplatform")`,
				"iosApp/iosApp.xcodeproj/dummy": "",
			},
			wantSystem:    SystemKMP,
			wantPlatforms: []string{"ios"},
		},
		{
			name: "kmp-incomplete-native-shell",
			dirs: []string{"shared", "iosApp", "androidApp"},
			files: map[string]string{
				"settings.gradle.kts": `id("org.jetbrains.kotlin.multiplatform")`,
			},
			wantSystem: SystemKMP,
		},
		{
			name: "bazel-kmp-monorepo",
			dirs: []string{"shared", "androidApp"},
			files: map[string]string{
				"MODULE.bazel":                "",
				"settings.gradle.kts":         `id("org.jetbrains.kotlin.multiplatform")`,
				"androidApp/build.gradle.kts": "android {}",
			},
			wantSystem:    SystemBazel,
			notWantSystem: SystemKMP,
		},
		{
			name: "rn-kmp-monorepo",
			dirs: []string{"shared", "androidApp"},
			files: map[string]string{
				"package.json":                `{"dependencies":{"react-native":"0.74.0"}}`,
				"settings.gradle.kts":         `id("org.jetbrains.kotlin.multiplatform")`,
				"androidApp/build.gradle.kts": "android {}",
			},
			wantSystem:    SystemReactNative,
			notWantSystem: SystemKMP,
		},
		{
			name: "plain-compose-android",
			dirs: []string{"shared", "composeApp"},
			files: map[string]string{
				"build.gradle.kts": `plugins { id("com.android.application") }`,
			},
			notWantSystem: SystemKMP,
		},
		{
			name: "plain-monorepo-shared-dir",
			dirs: []string{"shared"},
			files: map[string]string{
				"build.gradle.kts": `plugins { id("com.android.application") }`,
			},
			notWantSystem: SystemKMP,
		},
		{
			name: "weak-kmp-signals",
			dirs: []string{"shared", "androidApp"},
			files: map[string]string{
				"build.gradle.kts": `plugins { id("com.android.application") }`,
			},
			notWantSystem: SystemKMP,
		},
		{
			name: "expo-guardrail",
			files: map[string]string{
				"eas.json": "{}",
			},
			wantSystem: SystemExpo,
		},
		{
			name: "react-native-guardrail",
			files: map[string]string{
				"package.json": `{"dependencies":{"react-native":"0.74.0"}}`,
			},
			wantSystem: SystemReactNative,
		},
		{
			name:       "xcode-guardrail",
			dirs:       []string{"MyApp.xcodeproj"},
			wantSystem: SystemXcode,
		},
		{
			name: "gradle-guardrail",
			files: map[string]string{
				"build.gradle": "apply plugin: 'com.android.application'",
			},
			wantSystem: SystemGradle,
		},
		{
			name: "bazel-guardrail-no-kmp",
			dirs: []string{"ios", "android"},
			files: map[string]string{
				"MODULE.bazel": "",
			},
			wantSystem:    SystemBazel,
			notWantSystem: SystemKMP,
		},
	}

	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			dir := t.TempDir()

			for _, d := range fx.dirs {
				if err := os.MkdirAll(filepath.Join(dir, d), 0755); err != nil {
					t.Fatalf("MkdirAll(%s) error = %v", d, err)
				}
			}
			for path, content := range fx.files {
				writeDetectTestFile(t, filepath.Join(dir, path), content)
			}

			detected, err := Detect(dir)
			if err != nil {
				t.Fatalf("Detect() error = %v", err)
			}

			if fx.wantSystem != SystemUnknown && detected.System != fx.wantSystem {
				t.Errorf("System = %v, want %v", detected.System, fx.wantSystem)
			}

			if fx.notWantSystem != SystemUnknown && detected.System == fx.notWantSystem {
				t.Errorf("System = %v, must NOT be %v", detected.System, fx.notWantSystem)
			}

			for _, wantPlat := range fx.wantPlatforms {
				if _, ok := detected.Platforms[wantPlat]; !ok {
					t.Errorf("expected platform %q in detected platforms %v", wantPlat, keysOf(detected.Platforms))
				}
			}
		})
	}
}

func keysOf(m map[string]BuildPlatform) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
