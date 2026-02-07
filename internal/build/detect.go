// Package build provides build execution and artifact management utilities.
package build

import (
	"os"
	"path/filepath"
)

// BuildSystem represents a detected build system type.
type BuildSystem int

const (
	// SystemUnknown indicates the build system could not be detected.
	SystemUnknown BuildSystem = iota

	// SystemExpo indicates an Expo/React Native project.
	SystemExpo

	// SystemReactNative indicates a React Native project (non-Expo).
	SystemReactNative

	// SystemFlutter indicates a Flutter project.
	SystemFlutter

	// SystemXcode indicates a native iOS Xcode project.
	SystemXcode

	// SystemGradle indicates a native Android Gradle project.
	SystemGradle

	// SystemSwift indicates a Swift Package Manager project.
	SystemSwift
)

// String returns the human-readable name of the build system.
func (s BuildSystem) String() string {
	switch s {
	case SystemExpo:
		return "Expo"
	case SystemReactNative:
		return "React Native"
	case SystemFlutter:
		return "Flutter"
	case SystemXcode:
		return "Xcode"
	case SystemGradle:
		return "Gradle (Android)"
	case SystemSwift:
		return "Swift Package Manager"
	default:
		return "Unknown"
	}
}

// DetectedBuild contains information about a detected build system.
type DetectedBuild struct {
	// System is the detected build system type.
	System BuildSystem

	// Command is the suggested build command.
	Command string

	// Output is the expected output artifact path.
	Output string

	// Platform is the detected platform (ios, android, or empty for both).
	Platform string

	// Variants contains platform-specific build configurations.
	Variants map[string]BuildVariant
}

// BuildVariant represents a platform-specific build configuration.
type BuildVariant struct {
	// Command is the build command for this variant.
	Command string

	// Output is the expected output path for this variant.
	Output string
}

// Detect attempts to detect the build system in the given directory.
//
// Parameters:
//   - dir: The directory to scan for build system indicators
//
// Returns:
//   - *DetectedBuild: Information about the detected build system
//   - error: Any error that occurred during detection
//
// The function checks for various build system indicators in order of specificity:
// Expo > React Native > Flutter > Xcode > Gradle > Swift
func Detect(dir string) (*DetectedBuild, error) {
	// Check for Expo (app.json with expo key, or eas.json)
	if isExpoProject(dir) {
		return detectExpo(dir)
	}

	// Check for React Native (react-native in package.json)
	if isReactNativeProject(dir) {
		return detectReactNative(dir)
	}

	// Check for Flutter (pubspec.yaml)
	if fileExists(filepath.Join(dir, "pubspec.yaml")) {
		return detectFlutter(dir)
	}

	// Check for Xcode project
	if hasXcodeProject(dir) {
		return detectXcode(dir)
	}

	// Check for Gradle (Android)
	if fileExists(filepath.Join(dir, "build.gradle")) || fileExists(filepath.Join(dir, "build.gradle.kts")) {
		return detectGradle(dir)
	}

	// Check for Swift Package Manager
	if fileExists(filepath.Join(dir, "Package.swift")) {
		return detectSwift(dir)
	}

	return &DetectedBuild{System: SystemUnknown}, nil
}

// isExpoProject checks if the directory contains an Expo project.
func isExpoProject(dir string) bool {
	// Check for eas.json (definitive Expo indicator)
	if fileExists(filepath.Join(dir, "eas.json")) {
		return true
	}

	// Check for app.json with expo configuration
	appJsonPath := filepath.Join(dir, "app.json")
	if fileExists(appJsonPath) {
		content, err := os.ReadFile(appJsonPath)
		if err == nil {
			// Simple check for "expo" key in JSON
			return containsString(string(content), `"expo"`)
		}
	}

	return false
}

// isReactNativeProject checks if the directory contains a React Native project.
func isReactNativeProject(dir string) bool {
	packageJsonPath := filepath.Join(dir, "package.json")
	if !fileExists(packageJsonPath) {
		return false
	}

	content, err := os.ReadFile(packageJsonPath)
	if err != nil {
		return false
	}

	return containsString(string(content), `"react-native"`)
}

// hasXcodeProject checks if the directory contains an Xcode project.
func hasXcodeProject(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}

	for _, entry := range entries {
		name := entry.Name()
		if filepath.Ext(name) == ".xcodeproj" || filepath.Ext(name) == ".xcworkspace" {
			return true
		}
	}

	// Check ios subdirectory
	iosDir := filepath.Join(dir, "ios")
	if dirExists(iosDir) {
		entries, err := os.ReadDir(iosDir)
		if err == nil {
			for _, entry := range entries {
				name := entry.Name()
				if filepath.Ext(name) == ".xcodeproj" || filepath.Ext(name) == ".xcworkspace" {
					return true
				}
			}
		}
	}

	return false
}

// detectExpo returns build configuration for an Expo project.
func detectExpo(dir string) (*DetectedBuild, error) {
	detected := &DetectedBuild{
		System:   SystemExpo,
		Variants: make(map[string]BuildVariant),
	}

	// Default to EAS local build commands
	detected.Variants["ios"] = BuildVariant{
		Command: "eas build --platform ios --profile development --local --output build/app.tar.gz",
		Output:  "build/app.tar.gz",
	}
	detected.Variants["android"] = BuildVariant{
		Command: "eas build --platform android --profile development --local --output build/app.apk",
		Output:  "build/app.apk",
	}

	// Set default command (iOS)
	detected.Command = detected.Variants["ios"].Command
	detected.Output = detected.Variants["ios"].Output

	return detected, nil
}

// detectReactNative returns build configuration for a React Native project.
func detectReactNative(dir string) (*DetectedBuild, error) {
	detected := &DetectedBuild{
		System:   SystemReactNative,
		Variants: make(map[string]BuildVariant),
	}

	// iOS build (using xcodebuild)
	detected.Variants["ios"] = BuildVariant{
		Command: "cd ios && xcodebuild -workspace *.xcworkspace -scheme * -configuration Debug -sdk iphonesimulator -derivedDataPath build",
		Output:  "ios/build/Build/Products/Debug-iphonesimulator/*.app",
	}

	// Android build (using Gradle)
	detected.Variants["android"] = BuildVariant{
		Command: "cd android && ./gradlew assembleDebug",
		Output:  "android/app/build/outputs/apk/debug/app-debug.apk",
	}

	detected.Command = detected.Variants["android"].Command
	detected.Output = detected.Variants["android"].Output

	return detected, nil
}

// detectFlutter returns build configuration for a Flutter project.
func detectFlutter(dir string) (*DetectedBuild, error) {
	detected := &DetectedBuild{
		System:   SystemFlutter,
		Variants: make(map[string]BuildVariant),
	}

	detected.Variants["ios"] = BuildVariant{
		Command: "flutter build ios --simulator",
		Output:  "build/ios/iphonesimulator/*.app",
	}

	detected.Variants["android"] = BuildVariant{
		Command: "flutter build apk --debug",
		Output:  "build/app/outputs/flutter-apk/app-debug.apk",
	}

	detected.Command = detected.Variants["android"].Command
	detected.Output = detected.Variants["android"].Output

	return detected, nil
}

// detectXcode returns build configuration for an Xcode project.
func detectXcode(dir string) (*DetectedBuild, error) {
	detected := &DetectedBuild{
		System:   SystemXcode,
		Platform: "ios",
		Variants: make(map[string]BuildVariant),
	}

	// Find workspace or project
	workspaceName := findXcodeWorkspace(dir)
	if workspaceName != "" {
		detected.Command = "xcodebuild -workspace " + workspaceName + " -scheme * -configuration Debug -sdk iphonesimulator -derivedDataPath build"
	} else {
		projectName := findXcodeProject(dir)
		if projectName != "" {
			detected.Command = "xcodebuild -project " + projectName + " -scheme * -configuration Debug -sdk iphonesimulator -derivedDataPath build"
		} else {
			detected.Command = "xcodebuild -configuration Debug -sdk iphonesimulator -derivedDataPath build"
		}
	}

	detected.Output = "build/Build/Products/Debug-iphonesimulator/*.app"

	detected.Variants["ios"] = BuildVariant{
		Command: detected.Command,
		Output:  detected.Output,
	}

	return detected, nil
}

// detectGradle returns build configuration for a Gradle/Android project.
func detectGradle(dir string) (*DetectedBuild, error) {
	detected := &DetectedBuild{
		System:   SystemGradle,
		Platform: "android",
		Command:  "./gradlew assembleDebug",
		Output:   "app/build/outputs/apk/debug/app-debug.apk",
		Variants: make(map[string]BuildVariant),
	}

	detected.Variants["android"] = BuildVariant{
		Command: detected.Command,
		Output:  detected.Output,
	}

	return detected, nil
}

// detectSwift returns build configuration for a Swift Package Manager project.
func detectSwift(dir string) (*DetectedBuild, error) {
	detected := &DetectedBuild{
		System:   SystemSwift,
		Platform: "ios",
		Command:  "swift build",
		Output:   ".build/debug/*",
		Variants: make(map[string]BuildVariant),
	}

	return detected, nil
}

// findXcodeWorkspace finds an .xcworkspace file in the directory.
func findXcodeWorkspace(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".xcworkspace" {
			return entry.Name()
		}
	}

	// Check ios subdirectory
	iosDir := filepath.Join(dir, "ios")
	entries, err = os.ReadDir(iosDir)
	if err != nil {
		return ""
	}

	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".xcworkspace" {
			return filepath.Join("ios", entry.Name())
		}
	}

	return ""
}

// findXcodeProject finds an .xcodeproj file in the directory.
func findXcodeProject(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".xcodeproj" {
			return entry.Name()
		}
	}

	// Check ios subdirectory
	iosDir := filepath.Join(dir, "ios")
	entries, err = os.ReadDir(iosDir)
	if err != nil {
		return ""
	}

	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".xcodeproj" {
			return filepath.Join("ios", entry.Name())
		}
	}

	return ""
}

// fileExists checks if a file exists.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// dirExists checks if a directory exists.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// containsString checks if a string contains a substring.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

// containsSubstring is a simple substring check.
func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
