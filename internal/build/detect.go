package build

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var bazelTargetNamePattern = regexp.MustCompile(`name\s*=\s*"([^"]+)"`)

const reactNativeAndroidArchitecture = " -PreactNativeArchitectures=x86_64"

type BuildSystem int

const (
	SystemUnknown BuildSystem = iota
	SystemExpo
	SystemReactNative
	SystemFlutter
	SystemXcode
	SystemGradle
	SystemSwift
	SystemBazel
	SystemKMP
)

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

type DetectedBuild struct {
	System BuildSystem

	// Command is the suggested build command.
	Command string

	// Output is the expected output artifact path.
	Output string

	// Platform is the single detected platform (ios or android), or empty for multi-platform or unknown results.
	Platform string

	Platforms map[string]BuildPlatform
}

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

// Detect inspects dir in precedence order (Expo, React Native, Flutter, Bazel,
// Kotlin Multiplatform, XcodeGen, Xcode, Gradle, Swift) and returns the matched build configuration.
func Detect(dir string) (*DetectedBuild, error) {
	if isExpoProject(dir) {
		return detectExpo(dir)
	}

	if isReactNativeProject(dir) {
		return detectReactNative(dir)
	}

	if isFlutterProject(dir) {
		return detectFlutter(dir)
	}

	if isBazelProject(dir) {
		return detectBazel(dir)
	}

	if isKMPProject(dir) {
		return detectKMP(dir)
	}

	if !hasXcodeProject(dir) {
		if specDir, projectName := findXcodeGenSpec(dir); projectName != "" {
			return detectXcodeGen(dir, specDir, projectName), nil
		}
	}

	if hasXcodeProject(dir) {
		return detectXcode(dir)
	}

	if fileExists(filepath.Join(dir, "build.gradle")) || fileExists(filepath.Join(dir, "build.gradle.kts")) {
		return detectGradle(dir)
	}

	if fileExists(filepath.Join(dir, "Package.swift")) {
		return detectSwift(dir)
	}

	return &DetectedBuild{System: SystemUnknown}, nil
}

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
	content, err := readLocalFile(path)
	return err == nil && strings.Contains(string(content), text)
}

// HasExpoDevClient reports whether the app in dir depends on expo-dev-client,
// which lets revyl dev open the app on Metro with a deep link. Without it, a
// Debug build is told where Metro is when it launches, like bare React Native.
func HasExpoDevClient(dir string) bool {
	data, err := readLocalFile(filepath.Join(dir, "package.json"))
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

func isReactNativeProject(dir string) bool {
	return isJSApp(dir) && fileContains(filepath.Join(dir, "package.json"), `"react-native"`)
}

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
	expoPlatforms, expoPlatformsKnown := expoDeclaredPlatforms(dir)
	detected := &DetectedBuild{System: system, Platforms: make(map[string]BuildPlatform)}
	for suffix, configuration := range map[string]string{"": "Debug", "-preview": "Release"} {
		for _, platform := range []string{"ios", "android"} {
			generatedByPrebuild := system == SystemExpo && !DirExists(filepath.Join(dir, platform))
			if generatedByPrebuild && expoPlatformsKnown && !slices.Contains(expoPlatforms, platform) {
				continue
			}
			recipe, ok := reactNativePlatform(system, dir, platform, configuration)
			if !ok {
				continue
			}
			if generatedByPrebuild && hasExpoDynamicConfig(dir) {
				recipe.Note = "app.config.js/ts can set expo.platforms, which init cannot read; remove this profile if the app does not ship on " + platform
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
			Command: command + XcodebuildCommand(flag+" "+shellQuote(container)+" -scheme "+shellQuote(xcodeScheme(nativeDir, container)), configuration),
			Output:  output,
		}, true
	case DirExists(nativeDir):
		reason := "ios/ exists, but no .xcodeproj or .xcworkspace is present yet"
		if system == SystemExpo {
			reason = "ios/ exists without an .xcodeproj, so expo prebuild will not regenerate it; run npx expo prebuild --platform ios, then revyl init --detect"
		}
		return BuildPlatform{IncompleteReason: reason}, true
	case system == SystemExpo:
		return BuildPlatform{
			Command: `cd ios && if [ ! -d Pods ]; then pod install; fi && WS=$(ls -d *.xcworkspace | head -n 1) && ` + XcodebuildCommand(`-workspace "$WS" -scheme "${WS%.xcworkspace}"`, configuration),
			Output:  output,
		}, true
	default:
		return BuildPlatform{}, false
	}
}

// XcodebuildCommand returns the iOS simulator xcodebuild command for target and configuration.
func XcodebuildCommand(target, configuration string) string {
	return "xcodebuild " + target + " -configuration " + configuration + " -sdk iphonesimulator -destination 'generic/platform=iOS Simulator' ARCHS=arm64 -derivedDataPath build"
}

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

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func expoDeclaredPlatforms(dir string) ([]string, bool) {
	if hasExpoDynamicConfig(dir) {
		return nil, false
	}
	for _, name := range []string{"app.json", "package.json"} {
		data, err := readLocalFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var manifest struct {
			Expo struct {
				Platforms []string `json:"platforms"`
			} `json:"expo"`
		}
		if json.Unmarshal(data, &manifest) == nil && manifest.Expo.Platforms != nil {
			return manifest.Expo.Platforms, true
		}
	}
	return nil, false
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

// JSInstallCommand returns the dependency install command a build machine
// runs: LocalJSInstallCommand, after activating the pnpm or Bun version that
// package.json's packageManager field pins, since build machines ship other
// versions.
func JSInstallCommand(dir string) string {
	return jsInstallCommand(dir, true)
}

// LocalJSInstallCommand returns the dependency install command for the
// nearest lockfile in dir or a parent directory up to the repository root, run
// where the lockfile is so a workspace package installs with its workspace's
// package manager. It installs exactly what the lockfile records. Yarn 2 and
// later, pinned by package.json's packageManager field or configured by
// .yarnrc.yml, enables Corepack first, since machines ship Yarn 1. Without a
// lockfile, it installs with npm in dir.
func LocalJSInstallCommand(dir string) string {
	return jsInstallCommand(dir, false)
}

func jsInstallCommand(dir string, activatePinnedVersion bool) string {
	for current := dir; ; current = filepath.Dir(current) {
		for _, candidate := range lockfilePackageManagers {
			if !fileExists(filepath.Join(current, candidate.lockfile)) {
				continue
			}
			command := frozenInstallCommand(current, candidate.manager, activatePinnedVersion)
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

func frozenInstallCommand(dir, manager string, activatePinnedVersion bool) string {
	version := ""
	if pinnedManager, pinnedVersion := pinnedPackageManager(dir); pinnedManager == manager {
		version = pinnedVersion
	}
	switch manager {
	case "npm":
		return "npm ci"
	case "pnpm":
		if activatePinnedVersion && version != "" {
			return "corepack enable && corepack prepare pnpm@" + version + " --activate && pnpm install --frozen-lockfile"
		}
		return "pnpm install --frozen-lockfile"
	case "bun":
		if activatePinnedVersion && version != "" {
			return "npx --yes bun@" + version + " install --frozen-lockfile"
		}
		return "bun install --frozen-lockfile"
	default:
		modernYarn := fileExists(filepath.Join(dir, ".yarnrc.yml"))
		if version != "" {
			modernYarn = !strings.HasPrefix(version, "1.")
		}
		if modernYarn {
			return "corepack enable && yarn install --immutable"
		}
		return "yarn install --frozen-lockfile"
	}
}

var packageManagerVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)

// pinnedPackageManager extracts a strict semver pin from package.json's untrusted
// packageManager field so only validated versions reach shell commands.
func pinnedPackageManager(dir string) (manager, version string) {
	data, err := readLocalFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return "", ""
	}
	var manifest struct {
		PackageManager string `json:"packageManager"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return "", ""
	}
	manager, version, _ = strings.Cut(manifest.PackageManager, "@")
	version, _, _ = strings.Cut(version, "+")
	if !packageManagerVersionPattern.MatchString(version) {
		return "", ""
	}
	return manager, version
}

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
	detected.Command = XcodebuildCommand(flag+" "+shellQuote(ref)+" -scheme "+shellQuote(scheme), "Debug")

	detected.Output = "build/Build/Products/Debug-iphonesimulator/*.app"

	detected.Platforms["ios"] = BuildPlatform{
		SetupCommands: setupCommands,
		Command:       detected.Command,
		Output:        detected.Output,
	}

	return detected, nil
}

func findXcodeGenSpec(dir string) (specDir, projectName string) {
	for _, candidate := range []string{".", "ios"} {
		candidateDir := filepath.Join(dir, candidate)
		raw, err := readLocalFile(filepath.Join(candidateDir, "project.yml"))
		if err != nil || findXcodeProjectIn(candidateDir) != "" || findXcodeWorkspaceIn(candidateDir) != "" {
			continue
		}
		var spec struct {
			Name    string    `yaml:"name"`
			Targets yaml.Node `yaml:"targets"`
		}
		if yaml.Unmarshal(raw, &spec) != nil || spec.Targets.Kind == 0 {
			continue
		}
		if name := strings.TrimSpace(spec.Name); name != "" {
			return candidate, name
		}
	}
	return "", ""
}

func detectXcodeGen(dir, specDir, projectName string) *DetectedBuild {
	generate := "xcodegen generate"
	ref := projectName + ".xcodeproj"
	if specDir != "." {
		generate += " --spec " + shellQuote(specDir+"/project.yml")
		ref = specDir + "/" + ref
	}
	flag := "-project"
	var setupCommands []string
	if fileExists(filepath.Join(dir, specDir, "Podfile")) {
		podInstall := "pod install"
		if specDir != "." {
			podInstall = "cd " + shellQuote(specDir) + " && pod install"
		}
		setupCommands = []string{generate + " && " + podInstall}
		flag, ref = "-workspace", strings.TrimSuffix(ref, ".xcodeproj")+".xcworkspace"
	}
	command := XcodebuildCommand(flag+" "+shellQuote(ref)+" -scheme "+shellQuote(projectName), "Debug")
	if setupCommands == nil {
		command = generate + " && " + command
	}
	platform := BuildPlatform{
		SetupCommands: setupCommands,
		Command:       command,
		Output:        "build/Build/Products/Debug-iphonesimulator/*.app",
		Note:          "the Xcode project is generated by XcodeGen, so the scheme defaults to the project name; change -scheme if the app's scheme has another name",
	}
	return &DetectedBuild{
		System:    SystemXcode,
		Platform:  "ios",
		Command:   platform.Command,
		Output:    platform.Output,
		Platforms: map[string]BuildPlatform{"ios": platform},
	}
}

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

func isBazelProject(dir string) bool {
	markers := []string{"MODULE.bazel", "WORKSPACE.bazel", "WORKSPACE"}
	for _, m := range markers {
		if fileExists(filepath.Join(dir, m)) {
			return true
		}
	}
	return false
}

// detectBazel infers concrete Bazel Android/iOS targets when found, otherwise emits incomplete placeholders.
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

		content, err := readLocalFile(buildFile)
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

// detectBazelIOSPlatform scans common iOS directories for an ios_application
// rule in BUILD.bazel or BUILD and returns a suggested BuildPlatform if matched.
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
// BUILD.bazel or BUILD under dir, or returns "" if none is matched.
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

		content, err := readLocalFile(buildFile)
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

// isKMPProject reports whether dir matches heuristic KMP native-shell and Gradle markers.
func isKMPProject(dir string) bool {
	hasNativeShell := DirExists(filepath.Join(dir, "iosApp")) ||
		DirExists(filepath.Join(dir, "androidApp")) ||
		DirExists(filepath.Join(dir, "composeApp"))
	if !hasNativeShell {
		return false
	}

	return hasKMPGradleMarker(dir)
}

func hasKMPGradleMarker(dir string) bool {
	candidates := []string{
		filepath.Join(dir, "settings.gradle.kts"),
		filepath.Join(dir, "settings.gradle"),
		filepath.Join(dir, "build.gradle.kts"),
		filepath.Join(dir, "build.gradle"),
		filepath.Join(dir, "gradle", "libs.versions.toml"),
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
				candidates = append(candidates,
					filepath.Join(dir, entry.Name(), "build.gradle.kts"),
					filepath.Join(dir, entry.Name(), "build.gradle"),
				)
			}
		}
	}

	kmpMarkers := []string{
		"kotlin(\"multiplatform\")",
		"kotlin-multiplatform",
		"kotlinmultiplatform",
		"org.jetbrains.kotlin.multiplatform",
	}

	for _, candidate := range candidates {
		if !fileExists(candidate) {
			continue
		}
		content, err := readLocalFile(candidate)
		if err != nil {
			continue
		}
		text := strings.ToLower(string(content))
		for _, marker := range kmpMarkers {
			if strings.Contains(text, marker) {
				return true
			}
		}
	}

	return false
}

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
				Command: "cd iosApp && " + XcodebuildCommand(flag+" "+shellQuote(container)+" -scheme "+shellQuote(xcodeScheme(iosAppDir, container)), "Debug"),
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

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

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
func isFlutterProject(dir string) bool {
	pubspecPath := filepath.Join(dir, "pubspec.yaml")
	if !fileExists(pubspecPath) {
		return false
	}
	content, err := readLocalFile(pubspecPath)
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
func ParseBuildSystem(s string) BuildSystem {
	lower := strings.ToLower(strings.TrimSpace(s))
	if lower == "" {
		return SystemUnknown
	}

	if strings.HasPrefix(lower, "expo") {
		return SystemExpo
	}

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
// rather than a live hot-reload dev server (Flutter, Xcode, Gradle, Swift,
// Bazel, and KMP). Rebuild-only systems require a full binary rebuild and
// reinstall on each code change.
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

func parseXcodebuildListOutput(output string) []string {
	var schemes []string
	inSchemes := false
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if inSchemes {
				break
			}
			continue
		}
		if trimmed == "Schemes:" {
			inSchemes = true
			continue
		}
		if inSchemes {
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

func (r *SimulatorBuildResult) Age() time.Duration {
	return time.Since(r.ModTime)
}

// IsStale returns true if the build is older than the given threshold.
func (r *SimulatorBuildResult) IsStale(threshold time.Duration) bool {
	return r.Age() > threshold
}

// FindSimulatorBuild scans Xcode DerivedData for the most recently modified
// simulator .app bundle that matches the Xcode project in dir, or returns nil
// if none is found. This lets `revyl dev` reuse an app the developer already
// built in Xcode without running a separate build command.
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
// the most recently modified simulator .app matching projectName, or returns
// nil if none is found. Useful for testing with synthetic directory trees.
func FindSimulatorBuildInDir(derivedDataRoot, projectName string) *SimulatorBuildResult {
	return findSimulatorBuildInRoot(derivedDataRoot, projectName)
}

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

func xcodeProjectBaseName(dir string) string {
	if name := findXcodeWorkspace(dir); name != "" {
		return strings.TrimSuffix(filepath.Base(name), ".xcworkspace")
	}
	if name := findXcodeProject(dir); name != "" {
		return strings.TrimSuffix(filepath.Base(name), ".xcodeproj")
	}
	return ""
}

func isTestRunnerBundle(appPath string) bool {
	base := filepath.Base(appPath)
	name := strings.TrimSuffix(base, ".app")
	return strings.HasSuffix(name, "Tests") || strings.HasSuffix(name, "UITests")
}

// ApplySchemeToCommand replaces `-scheme *` in a build command with `-scheme <name>`.
// If scheme is empty or the command doesn't contain `-scheme *`, the command is returned unchanged.
func ApplySchemeToCommand(command, scheme string) string {
	if scheme == "" {
		return command
	}
	if !strings.Contains(command, "-scheme *") {
		return command
	}
	return strings.Replace(command, "-scheme *", "-scheme "+shellQuote(scheme), 1)
}
