// Package build provides build execution and artifact management utilities.
package build

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var bazelTargetNamePattern = regexp.MustCompile(`name\s*=\s*"([^"]+)"`)

// reactNativeAndroidArchitecture is the one ABI React Native compiles its
// native code for, the ABI of Revyl's Android emulators, instead of all four.
const reactNativeAndroidArchitecture = " -PreactNativeArchitectures=x86_64"

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
	// SetupCommands run in order before Command, from the project root.
	SetupCommands []string

	// Command is the build command for this platform.
	Command string

	// Output is the expected output path for this platform.
	Output string

	// IncompleteReason explains why the platform was detected but is not yet buildable.
	IncompleteReason string

	// Note explains a detected value the user should verify.
	Note string
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
	if !isJSApp(dir) {
		return false
	}
	if fileExists(filepath.Join(dir, "eas.json")) || hasExpoDynamicConfig(dir) {
		return true
	}
	return fileContains(filepath.Join(dir, "app.json"), `"expo"`) || fileContains(filepath.Join(dir, "package.json"), `"expo"`)
}

// isJSApp reports whether dir is an Expo or React Native app rather than a
// workspace root, library, or shared package that only depends on one: it has
// an app config or a native app project.
func isJSApp(dir string) bool {
	return fileExists(filepath.Join(dir, "app.json")) ||
		hasExpoDynamicConfig(dir) ||
		findXcodeProjectIn(filepath.Join(dir, "ios")) != "" ||
		findXcodeWorkspaceIn(filepath.Join(dir, "ios")) != "" ||
		DirExists(filepath.Join(dir, "android", "app"))
}

func fileContains(path, text string) bool {
	content, err := os.ReadFile(path) // #nosec G304 -- callers pass a fixed file name inside the local project.
	return err == nil && strings.Contains(string(content), text)
}

// HasExpoDevClient reports whether the app in dir depends on expo-dev-client,
// which lets revyl dev open the app on Metro with a deep link. Without it, a
// Debug build is told where Metro is when it launches, like bare React Native.
func HasExpoDevClient(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "package.json")) // #nosec G304 -- the path uses the local project root and a fixed file name.
	if err != nil {
		return false
	}
	var manifest struct {
		Dependencies    map[string]json.RawMessage `json:"dependencies"`
		DevDependencies map[string]json.RawMessage `json:"devDependencies"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return false
	}
	_, inDependencies := manifest.Dependencies["expo-dev-client"]
	_, inDevDependencies := manifest.DevDependencies["expo-dev-client"]
	return inDependencies || inDevDependencies
}

// isReactNativeProject checks if the directory contains a React Native project.
func isReactNativeProject(dir string) bool {
	return isJSApp(dir) && fileContains(filepath.Join(dir, "package.json"), `"react-native"`)
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

// detectExpo returns build configuration for an Expo project. Its recipes
// install dependencies, run expo prebuild when the native project is not
// committed, and build with Xcode or Gradle, so they need no Expo account.
func detectExpo(dir string) (*DetectedBuild, error) {
	return detectReactNativeApp(SystemExpo, dir), nil
}

// detectReactNative returns build configuration for a React Native project
// without Expo: the same recipes as Expo, without prebuild.
func detectReactNative(dir string) (*DetectedBuild, error) {
	return detectReactNativeApp(SystemReactNative, dir), nil
}

// detectReactNativeApp returns the two profiles every Expo and React Native app
// gets. The "ios" and "android" platforms are Debug builds that load JavaScript
// from Metro, for revyl dev. The "ios-preview" and "android-preview" platforms
// are Release builds with the JavaScript bundled, for revyl build, test runs,
// and pull requests.
func detectReactNativeApp(system BuildSystem, dir string) *DetectedBuild {
	installCommand := JSInstallCommand(dir)
	detected := &DetectedBuild{System: system, Platforms: make(map[string]BuildPlatform)}
	for suffix, configuration := range map[string]string{"": "Debug", "-preview": "Release"} {
		for _, platform := range []string{"ios", "android"} {
			recipe, ok := reactNativePlatform(system, dir, platform, configuration)
			if !ok {
				continue
			}
			if recipe.Command != "" {
				recipe.SetupCommands = []string{installCommand}
				if system == SystemExpo {
					recipe.SetupCommands = append(recipe.SetupCommands, expoPrebuildCommand(platform))
				}
			}
			detected.Platforms[platform+suffix] = recipe
		}
	}
	for _, key := range []string{"ios-preview", "android-preview"} {
		if recipe := detected.Platforms[key]; recipe.Command != "" {
			detected.Command, detected.Output = recipe.Command, recipe.Output
			break
		}
	}
	return detected
}

// reactNativePlatform returns one platform's recipe in the given build
// configuration, or false when the app has no project for that platform.
// Expo generates missing native projects with prebuild, so it always has both.
func reactNativePlatform(system BuildSystem, dir, platform, configuration string) (BuildPlatform, bool) {
	nativeDir := filepath.Join(dir, platform)
	if platform == "android" {
		if system != SystemExpo && !hasReactNativeAndroidProject(dir) {
			return BuildPlatform{}, false
		}
		gradle := "./gradlew"
		if DirExists(nativeDir) {
			gradle = gradleCommand(nativeDir)
		}
		output, note := apkOutput(dir, "android/app", strings.ToLower(configuration))
		return BuildPlatform{
			Command: "cd android && " + gradle + " assemble" + configuration + reactNativeAndroidArchitecture,
			Output:  output,
			Note:    note,
		}, true
	}

	output := "ios/build/Build/Products/" + configuration + "-iphonesimulator/*.app"
	projectFile := findXcodeProjectIn(nativeDir)
	workspaceFile := findXcodeWorkspaceIn(nativeDir)
	switch {
	case projectFile != "" || workspaceFile != "":
		command := "cd ios && "
		if fileExists(filepath.Join(nativeDir, "Podfile")) {
			if workspaceFile == "" {
				workspaceFile = strings.TrimSuffix(projectFile, ".xcodeproj") + ".xcworkspace"
			}
			command += "if [ ! -d Pods ] || [ ! -d " + shellQuote(workspaceFile) + " ]; then pod install; fi && "
		}
		container, flag := workspaceFile, "-workspace"
		if container == "" {
			container, flag = projectFile, "-project"
		}
		return BuildPlatform{
			Command: command + xcodebuildCommand(flag+" "+shellQuote(container)+" -scheme "+shellQuote(xcodeScheme(nativeDir, container)), configuration),
			Output:  output,
		}, true
	case DirExists(nativeDir):
		reason := "ios/ exists, but no .xcodeproj or .xcworkspace is present yet"
		if system == SystemExpo {
			reason = "ios/ exists without an .xcodeproj, so expo prebuild will not regenerate it; run npx expo prebuild --platform ios, then revyl init --detect"
		}
		return BuildPlatform{IncompleteReason: reason}, true
	case system == SystemExpo:
		// expo prebuild names the workspace and its app scheme after the app,
		// which app.config.js/ts can compute, so read the name after prebuild.
		return BuildPlatform{
			Command: `cd ios && if [ ! -d Pods ]; then pod install; fi && WS=$(ls -d *.xcworkspace | head -n 1) && ` + xcodebuildCommand(`-workspace "$WS" -scheme "${WS%.xcworkspace}"`, configuration),
			Output:  output,
		}, true
	default:
		return BuildPlatform{}, false
	}
}

// xcodebuildCommand builds target, the container and scheme arguments, for
// the iOS simulator. ARCHS=arm64 builds only the slice Revyl's Apple silicon
// simulators run, instead of every simulator architecture.
func xcodebuildCommand(target, configuration string) string {
	return "xcodebuild " + target + " -configuration " + configuration + " -sdk iphonesimulator -destination 'generic/platform=iOS Simulator' ARCHS=arm64 -derivedDataPath build"
}

// gradleCommand returns how to run Gradle in dir: its wrapper, through sh when
// Git checked the wrapper out without its executable bit, or the gradle on
// PATH when the project has no wrapper.
func gradleCommand(dir string) string {
	info, err := os.Stat(filepath.Join(dir, "gradlew"))
	switch {
	case err != nil:
		return "gradle"
	case info.Mode()&0o111 == 0:
		return "sh ./gradlew"
	default:
		return "./gradlew"
	}
}

// apkOutput returns where Gradle writes the APK of module, a directory
// relative to dir, for a build type such as debug. A module with product
// flavors writes one APK per flavor under apk/<flavor>/<type>/, so the path
// matches any of them and the note says how to build just one.
func apkOutput(dir, module, buildType string) (output, note string) {
	outputs := module + "/build/outputs/apk/"
	moduleDir := filepath.Join(dir, module)
	if !fileContains(filepath.Join(moduleDir, "build.gradle"), "productFlavors") && !fileContains(filepath.Join(moduleDir, "build.gradle.kts"), "productFlavors") {
		return outputs + buildType + "/" + filepath.Base(module) + "-" + buildType + ".apk", ""
	}
	variant := strings.ToUpper(buildType[:1]) + buildType[1:]
	return outputs + "*/" + buildType + "/*.apk", fmt.Sprintf("the app has product flavors, so assemble%[1]s builds every flavor; name one to build only it, such as assembleStaging%[1]s", variant)
}

// expoPrebuildCommand generates a platform's native project only when it is
// absent, matching `expo run` and EAS Build: committed native directories are
// never regenerated or overwritten. The build command installs CocoaPods.
func expoPrebuildCommand(platform string) string {
	return fmt.Sprintf("if [ ! -d %s ]; then npx expo prebuild --platform %s --no-install; fi", platform, platform)
}

// xcodeScheme returns the scheme to build from the Xcode workspace or project
// named container in dir: the shared scheme named after the app's project,
// else the shortest shared scheme that starts with the project name, such as
// "Holy Book Dev", else the shortest shared scheme, else the project name,
// which Xcode gives the scheme it creates for the app target. Test schemes are
// never chosen.
func xcodeScheme(dir, container string) string {
	projectName := strings.TrimSuffix(container, filepath.Ext(container))
	if project := findXcodeProjectIn(dir); project != "" {
		projectName = strings.TrimSuffix(project, ".xcodeproj")
	}

	var schemes []string
	seen := map[string]bool{}
	for _, pattern := range []string{
		filepath.Join(dir, container, "xcshareddata", "xcschemes", "*.xcscheme"),
		filepath.Join(dir, "*.xcodeproj", "xcshareddata", "xcschemes", "*.xcscheme"),
	} {
		paths, _ := filepath.Glob(pattern)
		for _, path := range paths {
			name := strings.TrimSuffix(filepath.Base(path), ".xcscheme")
			if seen[name] || strings.HasSuffix(name, "Tests") {
				continue
			}
			seen[name] = true
			if strings.EqualFold(name, projectName) {
				return name
			}
			schemes = append(schemes, name)
		}
	}
	if len(schemes) == 0 {
		return projectName
	}
	prefix := strings.ToLower(projectName)
	sort.Slice(schemes, func(i, j int) bool {
		iPrefixed, jPrefixed := strings.HasPrefix(strings.ToLower(schemes[i]), prefix), strings.HasPrefix(strings.ToLower(schemes[j]), prefix)
		if iPrefixed != jPrefixed {
			return iPrefixed
		}
		if len(schemes[i]) != len(schemes[j]) {
			return len(schemes[i]) < len(schemes[j])
		}
		return schemes[i] < schemes[j]
	})
	return schemes[0]
}

// shellQuote single-quotes a value for a POSIX shell command.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func hasExpoDynamicConfig(dir string) bool {
	for _, filename := range []string{"app.config.js", "app.config.ts", "app.config.mjs", "app.config.cjs"} {
		if fileExists(filepath.Join(dir, filename)) {
			return true
		}
	}
	return false
}

var lockfilePackageManagers = []struct{ lockfile, manager string }{
	{"yarn.lock", "yarn"},
	{"pnpm-lock.yaml", "pnpm"},
	{"bun.lockb", "bun"},
	{"bun.lock", "bun"},
	{"package-lock.json", "npm"},
}

// JSInstallCommand returns the dependency install command for the nearest
// lockfile in dir or a parent directory up to the repository root, run where
// the lockfile is so a workspace package installs with its workspace's package
// manager. Yarn 2 and later, pinned by package.json's packageManager field,
// enables Corepack first, since build machines ship Yarn 1. Without a
// lockfile, it installs with npm in dir.
func JSInstallCommand(dir string) string {
	for current := dir; ; current = filepath.Dir(current) {
		for _, candidate := range lockfilePackageManagers {
			if !fileExists(filepath.Join(current, candidate.lockfile)) {
				continue
			}
			command := candidate.manager + " install"
			if candidate.manager == "yarn" && usesModernYarn(current) {
				command = "corepack enable && " + command
			}
			if relative, err := filepath.Rel(dir, current); err == nil && relative != "." {
				command = "cd " + filepath.ToSlash(relative) + " && " + command
			}
			return command
		}
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil || filepath.Dir(current) == current {
			return "npm install"
		}
	}
}

// usesModernYarn reports whether package.json in dir pins Yarn 2 or later
// with its packageManager field, such as "yarn@4.10.2".
func usesModernYarn(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "package.json")) // #nosec G304 -- the path uses a local project directory and a fixed file name.
	if err != nil {
		return false
	}
	var manifest struct {
		PackageManager string `json:"packageManager"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return false
	}
	version, ok := strings.CutPrefix(manifest.PackageManager, "yarn@")
	return ok && version != "" && !strings.HasPrefix(version, "1.")
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

// detectFlutter returns build configuration for a Flutter app's ios/ and
// android/ projects. A Flutter package has neither, so it gets no platforms.
// The APK holds only the x86_64 code Revyl's Android emulators run.
func detectFlutter(dir string) (*DetectedBuild, error) {
	detected := &DetectedBuild{
		System:    SystemFlutter,
		Platforms: make(map[string]BuildPlatform),
	}

	if DirExists(filepath.Join(dir, "ios")) {
		detected.Platforms["ios"] = BuildPlatform{
			Command: "flutter build ios --simulator",
			Output:  "build/ios/iphonesimulator/*.app",
		}
		detected.Command, detected.Output = detected.Platforms["ios"].Command, detected.Platforms["ios"].Output
	}
	if DirExists(filepath.Join(dir, "android")) {
		detected.Platforms["android"] = BuildPlatform{
			Command: "flutter build apk --debug --target-platform android-x64",
			Output:  "build/app/outputs/flutter-apk/app-debug.apk",
		}
		detected.Command, detected.Output = detected.Platforms["android"].Command, detected.Platforms["android"].Output
	}

	return detected, nil
}

// detectXcode returns build configuration for an Xcode project. A Podfile
// next to the project installs CocoaPods first and builds the workspace that
// pod install creates.
func detectXcode(dir string) (*DetectedBuild, error) {
	detected := &DetectedBuild{
		System:    SystemXcode,
		Platform:  "ios",
		Platforms: make(map[string]BuildPlatform),
	}

	var setupCommands []string
	flag, ref := "-workspace", findXcodeWorkspace(dir)
	if ref == "" {
		flag, ref = "-project", findXcodeProject(dir)
	}
	refDir := filepath.Dir(ref)
	if fileExists(filepath.Join(dir, refDir, "Podfile")) {
		setupCommands = []string{"pod install"}
		if refDir != "." {
			setupCommands = []string{"cd " + shellQuote(refDir) + " && pod install"}
		}
		if flag == "-project" {
			flag, ref = "-workspace", strings.TrimSuffix(ref, ".xcodeproj")+".xcworkspace"
		}
	}
	scheme := xcodeScheme(filepath.Join(dir, refDir), filepath.Base(ref))
	detected.Command = xcodebuildCommand(flag+" "+shellQuote(ref)+" -scheme "+shellQuote(scheme), "Debug")

	detected.Output = "build/Build/Products/Debug-iphonesimulator/*.app"

	detected.Platforms["ios"] = BuildPlatform{
		SetupCommands: setupCommands,
		Command:       detected.Command,
		Output:        detected.Output,
	}

	return detected, nil
}

// detectGradle returns build configuration for a Gradle/Android project.
func detectGradle(dir string) (*DetectedBuild, error) {
	output, note := apkOutput(dir, "app", "debug")
	detected := &DetectedBuild{
		System:    SystemGradle,
		Platform:  "android",
		Command:   gradleCommand(dir) + " assembleDebug",
		Output:    output,
		Platforms: make(map[string]BuildPlatform),
	}

	detected.Platforms["android"] = BuildPlatform{
		Command: detected.Command,
		Output:  detected.Output,
		Note:    note,
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
			IncompleteReason: "Bazel workspace detected, but no ios/ or android/ directory found. Configure a platform-oriented named build profile manually.",
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

	namePattern := bazelTargetNamePattern
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

	namePattern := bazelTargetNamePattern
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

	for _, module := range []string{"androidApp", "composeApp"} {
		if !DirExists(filepath.Join(dir, module)) {
			continue
		}
		if !fileExists(filepath.Join(dir, module, "build.gradle.kts")) && !fileExists(filepath.Join(dir, module, "build.gradle")) {
			detected.Platforms["android"] = BuildPlatform{
				IncompleteReason: module + "/ exists but no build.gradle(.kts) found. Configure build command manually.",
			}
			break
		}
		output, note := apkOutput(dir, module, "debug")
		detected.Platforms["android"] = BuildPlatform{
			Command: gradleCommand(dir) + " :" + module + ":assembleDebug",
			Output:  output,
			Note:    note,
		}
		break
	}

	if DirExists(filepath.Join(dir, "iosApp")) {
		iosAppDir := filepath.Join(dir, "iosApp")
		flag, container := "-workspace", findXcodeWorkspaceIn(iosAppDir)
		if container == "" {
			flag, container = "-project", findXcodeProjectIn(iosAppDir)
		}
		if container != "" {
			detected.Platforms["ios"] = BuildPlatform{
				Command: "cd iosApp && " + xcodebuildCommand(flag+" "+shellQuote(container)+" -scheme "+shellQuote(xcodeScheme(iosAppDir, container)), "Debug"),
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
	return strings.Replace(command, "-scheme *", "-scheme "+shellQuote(scheme), 1)
}
