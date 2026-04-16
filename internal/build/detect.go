// Package build provides build execution and artifact management utilities.
package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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

	// SystemBazel indicates a Bazel-managed mobile project.
	SystemBazel

	// SystemKMP indicates a Kotlin Multiplatform project with shared native binaries.
	SystemKMP
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
	case SystemBazel:
		return "Bazel"
	case SystemKMP:
		return "Kotlin Multiplatform"
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

	// IncompleteReason explains why the platform was detected but is not yet buildable.
	IncompleteReason string
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

	// Check for Flutter (pubspec.yaml with flutter SDK dependency)
	if isFlutterProject(dir) {
		return detectFlutter(dir)
	}

	// Check for Bazel workspace before Xcode/Gradle so Bazel monorepos
	// are not misclassified by the presence of ios/ or android/ directories.
	if isBazelProject(dir) {
		return detectBazel(dir)
	}

	// Check for Kotlin Multiplatform before plain Xcode/Gradle so KMP
	// projects get explicit onboarding instead of generic native detection.
	if isKMPProject(dir) {
		return detectKMP(dir)
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
	if DirExists(iosDir) {
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

	if iosPlatform, ok := detectReactNativeIOSBuildPlatform(dir); ok {
		detected.Platforms["ios"] = iosPlatform
	} else if placeholderPlatform, ok := detectReactNativeIOSPlaceholderPlatform(dir); ok {
		detected.Platforms["ios"] = placeholderPlatform
	}

	if androidPlatform, ok := detectReactNativeAndroidBuildPlatform(dir); ok {
		detected.Platforms["android"] = androidPlatform
	}

	if androidPlatform, ok := detected.Platforms["android"]; ok && strings.TrimSpace(androidPlatform.Command) != "" {
		detected.Command = androidPlatform.Command
		detected.Output = androidPlatform.Output
	} else if iosPlatform, ok := detected.Platforms["ios"]; ok && strings.TrimSpace(iosPlatform.Command) != "" {
		detected.Command = iosPlatform.Command
		detected.Output = iosPlatform.Output
	}

	return detected, nil
}

// detectReactNativeIOSBuildPlatform returns the buildable iOS platform for a React Native project.
//
// Parameters:
//   - dir: The React Native project directory to inspect
//
// Returns:
//   - BuildPlatform: The resolved iOS build command and output path
//   - bool: True when a concrete Xcode workspace or project was found
func detectReactNativeIOSBuildPlatform(dir string) (BuildPlatform, bool) {
	workspaceName := findXcodeWorkspace(dir)
	if strings.TrimSpace(workspaceName) != "" {
		return buildReactNativeIOSPlatform(workspaceName, true, false), true
	}

	projectName := findXcodeProject(dir)
	if strings.TrimSpace(projectName) != "" {
		podfilePath := filepath.Join(dir, "ios", "Podfile")
		return buildReactNativeIOSPlatform(projectName, false, fileExists(podfilePath)), true
	}

	return BuildPlatform{}, false
}

// detectReactNativeIOSPlaceholderPlatform returns a placeholder iOS platform for incomplete native setups.
//
// Parameters:
//   - dir: The React Native project directory to inspect
//
// Returns:
//   - BuildPlatform: Placeholder platform metadata with an incomplete reason
//   - bool: True when an ios/ directory exists but no Xcode project/workspace is buildable yet
func detectReactNativeIOSPlaceholderPlatform(dir string) (BuildPlatform, bool) {
	iosDir := filepath.Join(dir, "ios")
	if !DirExists(iosDir) {
		return BuildPlatform{}, false
	}

	return BuildPlatform{
		IncompleteReason: "ios/ exists, but no .xcodeproj or .xcworkspace is present yet",
	}, true
}

// detectReactNativeAndroidBuildPlatform returns the buildable Android platform for a React Native project.
//
// Parameters:
//   - dir: The React Native project directory to inspect
//
// Returns:
//   - BuildPlatform: The resolved Android build command and output path
//   - bool: True when the expected Android Gradle structure exists
func detectReactNativeAndroidBuildPlatform(dir string) (BuildPlatform, bool) {
	if !hasReactNativeAndroidProject(dir) {
		return BuildPlatform{}, false
	}

	return BuildPlatform{
		Command: "cd android && ./gradlew assembleDebug",
		Output:  "android/app/build/outputs/apk/debug/app-debug.apk",
	}, true
}

// buildReactNativeIOSPlatform builds the iOS command/output pair for a React Native project.
//
// Parameters:
//   - projectRef: Relative path to the Xcode workspace or project
//   - useWorkspace: True when projectRef points to an .xcworkspace, false for .xcodeproj
//   - installPodsIfNeeded: True when the command should bootstrap CocoaPods before building
//
// Returns:
//   - BuildPlatform: A buildable iOS platform configuration
func buildReactNativeIOSPlatform(projectRef string, useWorkspace bool, installPodsIfNeeded bool) BuildPlatform {
	buildFlag := "-workspace"
	if !useWorkspace {
		buildFlag = "-project"
	}

	ref := strings.TrimSpace(projectRef)
	outputPath := "ios/build/Build/Products/Debug-iphonesimulator/*.app"
	if strings.HasPrefix(ref, "ios/") {
		refBase := filepath.Base(ref)
		if !useWorkspace && installPodsIfNeeded {
			workspaceName := strings.TrimSuffix(refBase, filepath.Ext(refBase)) + ".xcworkspace"
			return BuildPlatform{
				Command: "cd ios && if [ ! -d Pods ] || [ ! -d " + workspaceName + " ]; then pod install; fi && xcodebuild -workspace " + workspaceName + " -scheme * -configuration Debug -sdk iphonesimulator -derivedDataPath build",
				Output:  outputPath,
			}
		}
		return BuildPlatform{
			Command: "cd ios && xcodebuild " + buildFlag + " " + refBase + " -scheme * -configuration Debug -sdk iphonesimulator -derivedDataPath build",
			Output:  outputPath,
		}
	}

	return BuildPlatform{
		Command: "xcodebuild " + buildFlag + " " + ref + " -scheme * -configuration Debug -sdk iphonesimulator -derivedDataPath ios/build",
		Output:  outputPath,
	}
}

// hasReactNativeAndroidProject checks whether a React Native project has a usable Android Gradle layout.
//
// Parameters:
//   - dir: The React Native project directory to inspect
//
// Returns:
//   - bool: True when Android build files exist under android/
func hasReactNativeAndroidProject(dir string) bool {
	androidDir := filepath.Join(dir, "android")
	if !DirExists(androidDir) {
		return false
	}

	candidateFiles := []string{
		filepath.Join(androidDir, "app", "build.gradle"),
		filepath.Join(androidDir, "app", "build.gradle.kts"),
		filepath.Join(androidDir, "build.gradle"),
		filepath.Join(androidDir, "build.gradle.kts"),
		filepath.Join(androidDir, "settings.gradle"),
		filepath.Join(androidDir, "settings.gradle.kts"),
	}
	for _, candidate := range candidateFiles {
		if fileExists(candidate) {
			return true
		}
	}

	return false
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

// isBazelProject checks whether the directory contains a Bazel workspace by
// looking for MODULE.bazel, WORKSPACE.bazel, or WORKSPACE files.
//
// Parameters:
//   - dir: The directory to check for Bazel workspace markers
//
// Returns:
//   - bool: True when a recognised Bazel workspace file exists
func isBazelProject(dir string) bool {
	markers := []string{"MODULE.bazel", "WORKSPACE.bazel", "WORKSPACE"}
	for _, m := range markers {
		if fileExists(filepath.Join(dir, m)) {
			return true
		}
	}
	return false
}

// detectBazel returns build configuration for a Bazel workspace.
// Because Bazel targets and artifact paths are project-specific, the
// returned platforms use placeholder commands that the user fills in.
//
// Parameters:
//   - dir: The Bazel workspace root directory
//
// Returns:
//   - *DetectedBuild: Detected build info with placeholder platform entries
//   - error: Any error during detection
func detectBazel(dir string) (*DetectedBuild, error) {
	detected := &DetectedBuild{
		System:    SystemBazel,
		Platforms: make(map[string]BuildPlatform),
	}

	if androidPlatform, ok := detectBazelAndroidPlatform(dir); ok {
		detected.Platforms["android"] = androidPlatform
		detected.Command = androidPlatform.Command
		detected.Output = androidPlatform.Output
	}

	if iosPlatform, ok := detectBazelIOSPlatform(dir); ok {
		detected.Platforms["ios"] = iosPlatform
		if detected.Command == "" {
			detected.Command = iosPlatform.Command
			detected.Output = iosPlatform.Output
		}
	}

	hasIOS := DirExists(filepath.Join(dir, "ios")) || hasXcodeProject(dir)
	hasAndroid := len(detected.Platforms) > 0 ||
		DirExists(filepath.Join(dir, "android")) ||
		fileExists(filepath.Join(dir, "build.gradle")) ||
		fileExists(filepath.Join(dir, "build.gradle.kts"))

	if hasIOS {
		if _, ok := detected.Platforms["ios"]; !ok {
			detected.Platforms["ios"] = BuildPlatform{
				IncompleteReason: "Bazel workspace detected, but build target and artifact path must be configured manually in .revyl/config.yaml",
			}
		}
	}
	if hasAndroid {
		if _, ok := detected.Platforms["android"]; ok {
			return detected, nil
		}
		detected.Platforms["android"] = BuildPlatform{
			IncompleteReason: "Bazel workspace detected, but build target and artifact path must be configured manually in .revyl/config.yaml",
		}
	}

	if !hasIOS && !hasAndroid {
		detected.Platforms["ios"] = BuildPlatform{
			IncompleteReason: "Bazel workspace detected, but no ios/ or android/ directory found. Configure build.platforms manually.",
		}
	}

	return detected, nil
}

func detectBazelAndroidPlatform(dir string) (BuildPlatform, bool) {
	candidates := []string{
		".",
		"app",
		"android",
		"android/app",
		"androidApp",
	}

	for _, pkg := range candidates {
		targetName := parseBazelAndroidTarget(filepath.Join(dir, pkg))
		if targetName == "" {
			continue
		}

		targetLabel := "//:" + targetName
		outputPath := "bazel-bin/" + targetName + ".apk"
		if pkg != "." {
			normalizedPkg := filepath.ToSlash(pkg)
			targetLabel = "//" + normalizedPkg + ":" + targetName
			outputPath = "bazel-bin/" + normalizedPkg + "/" + targetName + ".apk"
		}

		return BuildPlatform{
			Command: "bazel build " + targetLabel + " -c dbg",
			Output:  outputPath,
		}, true
	}

	return BuildPlatform{}, false
}

func parseBazelAndroidTarget(dir string) string {
	buildFiles := []string{
		filepath.Join(dir, "BUILD.bazel"),
		filepath.Join(dir, "BUILD"),
	}

	namePattern := regexp.MustCompile(`name\s*=\s*"([^"]+)"`)
	for _, buildFile := range buildFiles {
		if !fileExists(buildFile) {
			continue
		}

		content, err := os.ReadFile(buildFile)
		if err != nil {
			continue
		}

		lines := strings.Split(string(content), "\n")
		for idx, line := range lines {
			if !strings.Contains(line, "android_binary(") && !strings.Contains(line, "kt_android_binary(") {
				continue
			}

			for lookahead := idx; lookahead < len(lines) && lookahead < idx+12; lookahead++ {
				match := namePattern.FindStringSubmatch(lines[lookahead])
				if len(match) == 2 {
					return strings.TrimSpace(match[1])
				}
				if strings.Contains(lines[lookahead], ")") {
					break
				}
			}
		}
	}

	return ""
}

// detectBazelIOSPlatform scans common iOS package directories for an
// ios_application target in a BUILD.bazel or BUILD file. Returns a concrete
// BuildPlatform when a target is found.
//
// Parameters:
//   - dir: The Bazel workspace root directory
//
// Returns:
//   - BuildPlatform: Concrete iOS build platform with command and .app output
//   - bool: True if a concrete ios_application target was found
func detectBazelIOSPlatform(dir string) (BuildPlatform, bool) {
	candidates := []string{
		".",
		"ios",
		"iosApp",
	}

	for _, pkg := range candidates {
		targetName := parseBazelIOSTarget(filepath.Join(dir, pkg))
		if targetName == "" {
			continue
		}

		targetLabel := "//:" + targetName
		outputPath := "bazel-bin/" + targetName + "_archive-root/Payload/" + targetName + ".app"
		if pkg != "." {
			normalizedPkg := filepath.ToSlash(pkg)
			targetLabel = "//" + normalizedPkg + ":" + targetName
			outputPath = "bazel-bin/" + normalizedPkg + "/" + targetName + "_archive-root/Payload/" + targetName + ".app"
		}

		return BuildPlatform{
			Command: "bazel build " + targetLabel + " -c dbg --ios_multi_cpus=sim_arm64",
			Output:  outputPath,
		}, true
	}

	return BuildPlatform{}, false
}

// parseBazelIOSTarget extracts the target name from an ios_application rule in
// a BUILD.bazel or BUILD file within the given directory.
//
// Parameters:
//   - dir: Directory containing the BUILD file to parse
//
// Returns:
//   - string: The target name, or "" if no ios_application rule was found
func parseBazelIOSTarget(dir string) string {
	buildFiles := []string{
		filepath.Join(dir, "BUILD.bazel"),
		filepath.Join(dir, "BUILD"),
	}

	namePattern := regexp.MustCompile(`name\s*=\s*"([^"]+)"`)
	for _, buildFile := range buildFiles {
		if !fileExists(buildFile) {
			continue
		}

		content, err := os.ReadFile(buildFile)
		if err != nil {
			continue
		}

		lines := strings.Split(string(content), "\n")
		for idx, line := range lines {
			if !strings.Contains(line, "ios_application(") {
				continue
			}

			for lookahead := idx; lookahead < len(lines) && lookahead < idx+12; lookahead++ {
				match := namePattern.FindStringSubmatch(lines[lookahead])
				if len(match) == 2 {
					return strings.TrimSpace(match[1])
				}
				if strings.Contains(lines[lookahead], ")") {
					break
				}
			}
		}
	}

	return ""
}

// isKMPProject checks whether the directory contains a Kotlin Multiplatform
// project. Detection requires multi-signal evidence to avoid false positives:
//   - A shared module directory (shared/)
//   - At least one KMP native shell (iosApp/, androidApp/, or composeApp/)
//   - A KMP-specific Gradle build marker in the project
//
// Parameters:
//   - dir: The directory to check for KMP indicators
//
// Returns:
//   - bool: True only when strong multi-signal evidence confirms KMP
func isKMPProject(dir string) bool {
	if !DirExists(filepath.Join(dir, "shared")) {
		return false
	}

	hasNativeShell := DirExists(filepath.Join(dir, "iosApp")) ||
		DirExists(filepath.Join(dir, "androidApp")) ||
		DirExists(filepath.Join(dir, "composeApp"))
	if !hasNativeShell {
		return false
	}

	return hasKMPGradleMarker(dir)
}

// hasKMPGradleMarker checks for Kotlin Multiplatform-specific content in Gradle
// build files. Looks for "kotlin(\"multiplatform\")", "kotlin-multiplatform",
// or "KotlinMultiplatform" in settings.gradle(.kts) and shared/build.gradle(.kts).
//
// Parameters:
//   - dir: The project root directory
//
// Returns:
//   - bool: True when KMP-specific Gradle content is found
func hasKMPGradleMarker(dir string) bool {
	candidates := []string{
		filepath.Join(dir, "settings.gradle.kts"),
		filepath.Join(dir, "settings.gradle"),
		filepath.Join(dir, "shared", "build.gradle.kts"),
		filepath.Join(dir, "shared", "build.gradle"),
		filepath.Join(dir, "build.gradle.kts"),
		filepath.Join(dir, "build.gradle"),
	}

	kmpMarkers := []string{
		"kotlin(\"multiplatform\")",
		"kotlin-multiplatform",
		"KotlinMultiplatform",
		"org.jetbrains.kotlin.multiplatform",
	}

	for _, candidate := range candidates {
		if !fileExists(candidate) {
			continue
		}
		content, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		text := string(content)
		for _, marker := range kmpMarkers {
			if strings.Contains(text, marker) {
				return true
			}
		}
	}

	return false
}

// detectKMP returns build configuration for a Kotlin Multiplatform project.
// It detects native shell directories and generates platform-specific build
// commands or placeholders based on the available project structure.
//
// Parameters:
//   - dir: The KMP project root directory
//
// Returns:
//   - *DetectedBuild: Detected build info with native platform entries
//   - error: Any error during detection
func detectKMP(dir string) (*DetectedBuild, error) {
	detected := &DetectedBuild{
		System:    SystemKMP,
		Platforms: make(map[string]BuildPlatform),
	}

	if DirExists(filepath.Join(dir, "androidApp")) {
		gradleFile := ""
		for _, name := range []string{"build.gradle.kts", "build.gradle"} {
			if fileExists(filepath.Join(dir, "androidApp", name)) {
				gradleFile = name
				break
			}
		}
		if gradleFile != "" {
			command := "cd androidApp && ./gradlew assembleDebug"
			if fileExists(filepath.Join(dir, "gradlew")) {
				command = "./gradlew :androidApp:assembleDebug"
			}
			detected.Platforms["android"] = BuildPlatform{
				Command: command,
				Output:  "androidApp/build/outputs/apk/debug/androidApp-debug.apk",
			}
		} else {
			detected.Platforms["android"] = BuildPlatform{
				IncompleteReason: "androidApp/ exists but no build.gradle(.kts) found. Configure build command manually.",
			}
		}
	} else if DirExists(filepath.Join(dir, "composeApp")) {
		gradleFile := ""
		for _, name := range []string{"build.gradle.kts", "build.gradle"} {
			if fileExists(filepath.Join(dir, "composeApp", name)) {
				gradleFile = name
				break
			}
		}
		if gradleFile != "" {
			command := "cd composeApp && ./gradlew assembleDebug"
			if fileExists(filepath.Join(dir, "gradlew")) {
				command = "./gradlew :composeApp:assembleDebug"
			}
			detected.Platforms["android"] = BuildPlatform{
				Command: command,
				Output:  "composeApp/build/outputs/apk/debug/composeApp-debug.apk",
			}
		} else {
			detected.Platforms["android"] = BuildPlatform{
				IncompleteReason: "composeApp/ exists but no build.gradle(.kts) found. Configure build command manually.",
			}
		}
	}

	if DirExists(filepath.Join(dir, "iosApp")) {
		workspaceName := findXcodeWorkspaceIn(filepath.Join(dir, "iosApp"))
		projectName := findXcodeProjectIn(filepath.Join(dir, "iosApp"))

		if workspaceName != "" {
			detected.Platforms["ios"] = BuildPlatform{
				Command: "cd iosApp && xcodebuild -workspace " + workspaceName + " -scheme * -configuration Debug -sdk iphonesimulator -derivedDataPath build",
				Output:  "iosApp/build/Build/Products/Debug-iphonesimulator/*.app",
			}
		} else if projectName != "" {
			detected.Platforms["ios"] = BuildPlatform{
				Command: "cd iosApp && xcodebuild -project " + projectName + " -scheme * -configuration Debug -sdk iphonesimulator -derivedDataPath build",
				Output:  "iosApp/build/Build/Products/Debug-iphonesimulator/*.app",
			}
		} else {
			detected.Platforms["ios"] = BuildPlatform{
				IncompleteReason: "iosApp/ exists but no .xcodeproj or .xcworkspace found. Configure build command manually.",
			}
		}
	}

	if androidPlatform, ok := detected.Platforms["android"]; ok && strings.TrimSpace(androidPlatform.Command) != "" {
		detected.Command = androidPlatform.Command
		detected.Output = androidPlatform.Output
	} else if iosPlatform, ok := detected.Platforms["ios"]; ok && strings.TrimSpace(iosPlatform.Command) != "" {
		detected.Command = iosPlatform.Command
		detected.Output = iosPlatform.Output
	}

	return detected, nil
}

// findXcodeWorkspaceIn finds an .xcworkspace file directly within the given directory.
//
// Parameters:
//   - dir: Directory to scan
//
// Returns:
//   - string: Workspace filename, or empty if not found
func findXcodeWorkspaceIn(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".xcworkspace" {
			return entry.Name()
		}
	}
	return ""
}

// findXcodeProjectIn finds an .xcodeproj file directly within the given directory.
//
// Parameters:
//   - dir: Directory to scan
//
// Returns:
//   - string: Project filename, or empty if not found
func findXcodeProjectIn(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".xcodeproj" {
			return entry.Name()
		}
	}
	return ""
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
			return "ios/" + entry.Name()
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
			return "ios/" + entry.Name()
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

// DirExists checks if a directory exists.
func DirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// isFlutterProject checks whether the directory contains a Flutter project by
// looking for a pubspec.yaml that declares a flutter SDK dependency. This avoids
// misclassifying plain Dart packages or non-mobile pubspec.yaml projects.
//
// Parameters:
//   - dir: The directory to check for Flutter indicators
//
// Returns:
//   - bool: True when pubspec.yaml exists and references the Flutter SDK
func isFlutterProject(dir string) bool {
	pubspecPath := filepath.Join(dir, "pubspec.yaml")
	if !fileExists(pubspecPath) {
		return false
	}
	content, err := os.ReadFile(pubspecPath)
	if err != nil {
		return false
	}
	// Flutter projects declare `flutter:` as an SDK dependency and/or have a
	// top-level `flutter:` configuration key. Check for the SDK dependency
	// pattern which is the canonical marker: "sdk: flutter".
	return strings.Contains(string(content), "sdk: flutter")
}

// ParseBuildSystem maps a persisted build-system string (from config YAML) back
// to a typed BuildSystem value. Handles canonical names from BuildSystem.String()
// as well as common variants (e.g. "React Native (bare)"). This allows init UX
// code to branch on typed constants instead of ad-hoc string comparisons.
//
// Parameters:
//   - s: The build-system string (e.g. "Flutter", "Expo", "Gradle (Android)")
//
// Returns:
//   - BuildSystem: The matching typed constant, or SystemUnknown
func ParseBuildSystem(s string) BuildSystem {
	lower := strings.ToLower(strings.TrimSpace(s))
	if lower == "" {
		return SystemUnknown
	}

	// Expo check first: "expo" but not "expo react native" variants.
	if strings.HasPrefix(lower, "expo") {
		return SystemExpo
	}

	// React Native: "react native", "react native (bare)", etc. Must come
	// after the Expo check so "Expo React Native" doesn't match here.
	if strings.HasPrefix(lower, "react native") {
		return SystemReactNative
	}

	switch lower {
	case "flutter":
		return SystemFlutter
	case "xcode":
		return SystemXcode
	case "gradle (android)", "gradle":
		return SystemGradle
	case "swift package manager", "swift":
		return SystemSwift
	case "bazel":
		return SystemBazel
	case "kotlin multiplatform", "kmp":
		return SystemKMP
	default:
		return SystemUnknown
	}
}

// IsRebuildOnly reports whether this build system uses a rebuild-based dev loop
// rather than a live hot-reload dev server. Rebuild-only systems require a full
// binary rebuild + reinstall on each code change.
//
// Returns:
//   - bool: True for Flutter, Xcode, Gradle, and Swift; false otherwise
func (s BuildSystem) IsRebuildOnly() bool {
	switch s {
	case SystemFlutter, SystemXcode, SystemGradle, SystemSwift, SystemBazel, SystemKMP:
		return true
	default:
		return false
	}
}

// ListXcodeSchemes discovers available Xcode schemes by running `xcodebuild -list`
// in the given directory.
//
// Parameters:
//   - dir: The directory to run xcodebuild -list in
//
// Returns:
//   - []string: List of scheme names
//   - error: Any error that occurred during discovery
func ListXcodeSchemes(dir string) ([]string, error) {
	cmd := exec.Command("xcodebuild", "-list")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("xcodebuild -list failed: %w", err)
	}
	schemes := parseXcodebuildListOutput(string(out))
	return schemes, nil
}

// parseXcodebuildListOutput parses the "Schemes:" section from xcodebuild -list output.
//
// Example xcodebuild -list output:
//
//	Information about project "MyApp":
//	    Targets:
//	        MyApp
//	    Build Configurations:
//	        Debug
//	        Release
//	    Schemes:
//	        MyApp
//	        MyAppTests
func parseXcodebuildListOutput(output string) []string {
	var schemes []string
	inSchemes := false
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if inSchemes {
				break // Empty line after schemes section ends it
			}
			continue
		}
		if trimmed == "Schemes:" {
			inSchemes = true
			continue
		}
		if inSchemes {
			// A new section header (e.g. "Targets:") ends the schemes block
			if strings.HasSuffix(trimmed, ":") {
				break
			}
			schemes = append(schemes, trimmed)
		}
	}
	return schemes
}

// SimulatorBuildResult holds the path and modification time of a discovered
// simulator .app bundle from DerivedData. Callers use Age() to warn about
// stale builds.
type SimulatorBuildResult struct {
	// Path is the absolute path to the .app bundle directory.
	Path string

	// ModTime is when the .app was last modified by Xcode.
	ModTime time.Time
}

// Age returns how long ago the .app was last modified.
func (r *SimulatorBuildResult) Age() time.Duration {
	return time.Since(r.ModTime)
}

// IsStale returns true if the build is older than the given threshold.
func (r *SimulatorBuildResult) IsStale(threshold time.Duration) bool {
	return r.Age() > threshold
}

// FindSimulatorBuild scans Xcode DerivedData for the most recently modified
// simulator .app bundle that matches the Xcode project in dir. This lets
// `revyl dev` reuse an app the developer already built in Xcode without
// running a separate build command.
//
// Parameters:
//   - dir: The project directory containing .xcodeproj or .xcworkspace
//
// Returns:
//   - *SimulatorBuildResult: The discovered build with path and mod time, or nil if none found
func FindSimulatorBuild(dir string) *SimulatorBuildResult {
	projectName := xcodeProjectBaseName(dir)
	if projectName == "" {
		return nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	derivedDataRoot := filepath.Join(homeDir, "Library", "Developer", "Xcode", "DerivedData")
	return findSimulatorBuildInRoot(derivedDataRoot, projectName)
}

// FindSimulatorBuildInDir scans a specific DerivedData-style directory root for
// the most recently modified simulator .app. Useful for testing with synthetic
// directory trees.
//
// Parameters:
//   - derivedDataRoot: Directory to scan for project-hash/Build/Products/Debug-iphonesimulator/*.app
//   - projectName: Xcode project base name (without .xcodeproj)
//
// Returns:
//   - *SimulatorBuildResult: The discovered build with path and mod time, or nil if none found
func FindSimulatorBuildInDir(derivedDataRoot, projectName string) *SimulatorBuildResult {
	return findSimulatorBuildInRoot(derivedDataRoot, projectName)
}

// findSimulatorBuildInRoot is the shared implementation for FindSimulatorBuild
// and FindSimulatorBuildInDir.
func findSimulatorBuildInRoot(derivedDataRoot, projectName string) *SimulatorBuildResult {
	entries, err := os.ReadDir(derivedDataRoot)
	if err != nil {
		return nil
	}

	var candidates []string
	prefix := projectName + "-"
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		productsDir := filepath.Join(derivedDataRoot, entry.Name(), "Build", "Products", "Debug-iphonesimulator")
		apps, globErr := filepath.Glob(filepath.Join(productsDir, "*.app"))
		if globErr != nil || len(apps) == 0 {
			continue
		}
		for _, app := range apps {
			if isTestRunnerBundle(app) {
				continue
			}
			candidates = append(candidates, app)
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	best, err := getMostRecentFile(candidates)
	if err != nil {
		return nil
	}

	info, err := os.Stat(best)
	if err != nil {
		return nil
	}

	return &SimulatorBuildResult{
		Path:    best,
		ModTime: info.ModTime(),
	}
}

// xcodeProjectBaseName extracts the Xcode project name (without extension) from
// the given directory. Checks for .xcworkspace first, then .xcodeproj, then
// the ios/ subdirectory.
func xcodeProjectBaseName(dir string) string {
	if name := findXcodeWorkspace(dir); name != "" {
		return strings.TrimSuffix(filepath.Base(name), ".xcworkspace")
	}
	if name := findXcodeProject(dir); name != "" {
		return strings.TrimSuffix(filepath.Base(name), ".xcodeproj")
	}
	return ""
}

// isTestRunnerBundle returns true if the .app appears to be an XCTest runner
// rather than a real application. Test runners are conventionally named
// *Tests.app or *UITests.app.
func isTestRunnerBundle(appPath string) bool {
	base := filepath.Base(appPath)
	name := strings.TrimSuffix(base, ".app")
	return strings.HasSuffix(name, "Tests") || strings.HasSuffix(name, "UITests")
}

// ApplySchemeToCommand replaces `-scheme *` in a build command with `-scheme <name>`.
// If scheme is empty or the command doesn't contain `-scheme *`, the command is returned unchanged.
//
// Parameters:
//   - command: The build command string
//   - scheme: The scheme name to substitute
//
// Returns:
//   - string: The modified command
func ApplySchemeToCommand(command, scheme string) string {
	if scheme == "" {
		return command
	}
	if !strings.Contains(command, "-scheme *") {
		return command
	}
	// Shell-escape the scheme name to prevent injection via crafted names.
	// Single-quote the value and escape any embedded single quotes.
	escaped := strings.ReplaceAll(scheme, "'", "'\\''")
	escapedScheme := fmt.Sprintf("'%s'", escaped)
	return strings.Replace(command, "-scheme *", "-scheme "+escapedScheme, 1)
}
