// Package build provides build execution and artifact management utilities.
package build

import (
	"os"
	"path/filepath"
	"strings"
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

	// Platforms contains platform-specific build configurations.
	Platforms map[string]BuildPlatform
}

// BuildPlatform represents a platform-specific build configuration.
type BuildPlatform struct {
	// Command is the build command for this platform.
	Command string

	// Output is the expected output path for this platform.
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
			return strings.Contains(string(content), `"expo"`)
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

	return strings.Contains(string(content), `"react-native"`)
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
		System:    SystemExpo,
		Platforms: make(map[string]BuildPlatform),
	}

	// Default to EAS local build commands
	detected.Platforms["ios"] = BuildPlatform{
		Command: "npx --yes eas-cli build --platform ios --profile development --local --output build/app.tar.gz",
		Output:  "build/app.tar.gz",
	}
	detected.Platforms["android"] = BuildPlatform{
		Command: "npx --yes eas-cli build --platform android --profile development --local --output build/app.apk",
		Output:  "build/app.apk",
	}

	// Set default command (iOS)
	detected.Command = detected.Platforms["ios"].Command
	detected.Output = detected.Platforms["ios"].Output

	return detected, nil
}

// detectReactNative returns build configuration for a React Native project.
func detectReactNative(dir string) (*DetectedBuild, error) {
	detected := &DetectedBuild{
		System:    SystemReactNative,
		Platforms: make(map[string]BuildPlatform),
	}

	// iOS build (using xcodebuild)
	detected.Platforms["ios"] = BuildPlatform{
		Command: "cd ios && xcodebuild -workspace *.xcworkspace -scheme * -configuration Debug -sdk iphonesimulator -derivedDataPath build",
		Output:  "ios/build/Build/Products/Debug-iphonesimulator/*.app",
	}

	// Android build (using Gradle)
	detected.Platforms["android"] = BuildPlatform{
		Command: "cd android && ./gradlew assembleDebug",
		Output:  "android/app/build/outputs/apk/debug/app-debug.apk",
	}

	detected.Command = detected.Platforms["android"].Command
	detected.Output = detected.Platforms["android"].Output

	return detected, nil
}

// detectFlutter returns build configuration for a Flutter project.
func detectFlutter(dir string) (*DetectedBuild, error) {
	detected := &DetectedBuild{
		System:    SystemFlutter,
		Platforms: make(map[string]BuildPlatform),
	}

	detected.Platforms["ios"] = BuildPlatform{
		Command: "flutter build ios --simulator",
		Output:  "build/ios/iphonesimulator/*.app",
	}

	detected.Platforms["android"] = BuildPlatform{
		Command: "flutter build apk --debug",
		Output:  "build/app/outputs/flutter-apk/app-debug.apk",
	}

	detected.Command = detected.Platforms["android"].Command
	detected.Output = detected.Platforms["android"].Output

	return detected, nil
}

// detectXcode returns build configuration for an Xcode project.
func detectXcode(dir string) (*DetectedBuild, error) {
	detected := &DetectedBuild{
		System:    SystemXcode,
		Platform:  "ios",
		Platforms: make(map[string]BuildPlatform),
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

	detected.Platforms["ios"] = BuildPlatform{
		Command: detected.Command,
		Output:  detected.Output,
	}

	return detected, nil
}

// detectGradle returns build configuration for a Gradle/Android project.
func detectGradle(dir string) (*DetectedBuild, error) {
	detected := &DetectedBuild{
		System:    SystemGradle,
		Platform:  "android",
		Command:   "./gradlew assembleDebug",
		Output:    "app/build/outputs/apk/debug/app-debug.apk",
		Platforms: make(map[string]BuildPlatform),
	}

	detected.Platforms["android"] = BuildPlatform{
		Command: detected.Command,
		Output:  detected.Output,
	}

	return detected, nil
}

// detectSwift returns build configuration for a Swift Package Manager project.
func detectSwift(dir string) (*DetectedBuild, error) {
	detected := &DetectedBuild{
		System:    SystemSwift,
		Platform:  "ios",
		Command:   "swift build",
		Output:    ".build/debug/*",
		Platforms: make(map[string]BuildPlatform),
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
