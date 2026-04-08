package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
