package buildselection

import (
	"fmt"
	"strings"
	"time"
)

// BuildClass categorizes a build by its compilation mode.
// Used to determine hot-reload compatibility in the dev loop.
type BuildClass string

const (
	// BuildClassDevClient is a development client build (e.g. EAS development profile).
	// Always compatible with hot reload.
	BuildClassDevClient BuildClass = "Dev Client"

	// BuildClassDebug is a debug-configuration build (e.g. xcodebuild -configuration Debug).
	// Typically compatible with hot reload.
	BuildClassDebug BuildClass = "Debug"

	// BuildClassRelease is a release-configuration build that bakes JS into the binary.
	// Never compatible with hot reload.
	BuildClassRelease BuildClass = "Release"

	// BuildClassUnknown means no build_command metadata was available for classification.
	BuildClassUnknown BuildClass = "Unknown"
)

// Compatibility represents the tri-state compatibility verdict.
type Compatibility int

const (
	// CompatibleYes means the build supports hot reload.
	CompatibleYes Compatibility = iota
	// CompatibleNo means the build cannot support hot reload.
	CompatibleNo
	// CompatibleUnknown means we lack metadata to determine compatibility.
	CompatibleUnknown
)

// PreflightResult holds the full classification and compatibility analysis of a build.
//
// Fields:
//   - Class: the detected build type (DevClient, Debug, Release, Unknown)
//   - Compatible: tri-state verdict (Yes, No, Unknown)
//   - Reason: human-readable explanation when incompatible or unknown
//   - FixCommands: actionable CLI commands the user can run to fix the issue
//   - Warnings: non-fatal observations (stale build, missing package name, etc.)
type PreflightResult struct {
	Class       BuildClass
	Compatible  Compatibility
	Reason      string
	FixCommands []string
	Warnings    []string
}

const staleThresholdDays = 14

// ClassifyBuild examines build metadata and returns a classification with compatibility verdict.
//
// Parameters:
//   - metadata: the build's metadata map (from API response or build selection)
//   - providerName: the hot-reload provider (e.g. "expo", "react-native", "swift")
//   - platformKey: the build platform key (e.g. "ios-dev", "android-dev") used for fix commands
//
// Returns:
//   - PreflightResult with class, compatibility, explanation, fix commands, and warnings
func ClassifyBuild(metadata map[string]interface{}, providerName, platformKey string) PreflightResult {
	result := PreflightResult{
		Class:      BuildClassUnknown,
		Compatible: CompatibleUnknown,
	}

	buildCommand := extractBuildCommand(metadata)
	result.Class, result.Compatible, result.Reason = classifyFromBuildCommand(buildCommand, providerName)

	if result.Compatible == CompatibleNo {
		result.FixCommands = fixCommandsForPlatform(platformKey, providerName)
	}

	result.Warnings = append(result.Warnings, checkBuildAge(metadata)...)
	result.Warnings = append(result.Warnings, checkPackageName(metadata)...)

	return result
}

// classifyFromBuildCommand determines build class and compatibility from the build_command string.
//
// Parameters:
//   - command: the build_command value from metadata
//   - providerName: hot-reload provider for context-sensitive classification
//
// Returns:
//   - class: the detected BuildClass
//   - compat: the compatibility verdict
//   - reason: human-readable explanation when incompatible or unknown
func classifyFromBuildCommand(command, providerName string) (BuildClass, Compatibility, string) {
	if command == "" {
		reason := "Build metadata has no build_command; unable to verify hot-reload compatibility."
		if providerName == "expo" || providerName == "react-native" {
			reason += " This is usually fine for Expo/RN if you uploaded a dev client."
		}
		return BuildClassUnknown, CompatibleUnknown, reason
	}

	lower := strings.ToLower(command)

	if isEASDevProfile(lower) {
		return BuildClassDevClient, CompatibleYes, ""
	}

	if isEASReleaseProfile(lower) {
		return BuildClassRelease, CompatibleNo,
			"This build was created with an EAS production/preview profile. " +
				"It bakes JavaScript into the binary and cannot connect to Metro for hot reload."
	}

	if strings.Contains(lower, "-configuration release") {
		return BuildClassRelease, CompatibleNo,
			"This build uses -configuration Release, which bakes JavaScript into the binary. " +
				"Hot reload requires a Debug configuration that can connect to Metro bundler."
	}

	if strings.Contains(lower, "-configuration debug") {
		return BuildClassDebug, CompatibleYes, ""
	}

	if isAndroidRelease(lower) {
		return BuildClassRelease, CompatibleNo,
			"This build uses a release variant. " +
				"Release builds cannot connect to a dev server for hot reload."
	}

	if isAndroidDebug(lower) {
		return BuildClassDebug, CompatibleYes, ""
	}

	if isBazelCommand(lower) {
		return classifyBazelCommand(lower)
	}

	return BuildClassUnknown, CompatibleUnknown,
		"Could not determine build type from command. Verify this is a debug/dev-client build."
}

// isBazelCommand checks whether the build command invokes Bazel or Bazelisk.
func isBazelCommand(lower string) bool {
	return strings.Contains(lower, "bazel") || strings.Contains(lower, "bazelisk")
}

// classifyBazelCommand classifies a Bazel build command by its compilation mode.
// Looks for -c or --compilation_mode flags with dbg/fastbuild/opt values.
//
// Parameters:
//   - lower: The lowercased build command string
//
// Returns:
//   - class: The detected BuildClass
//   - compat: The compatibility verdict
//   - reason: Human-readable explanation
func classifyBazelCommand(lower string) (BuildClass, Compatibility, string) {
	debugSignals := []string{"-c dbg", "--compilation_mode=dbg", "-c fastbuild", "--compilation_mode=fastbuild"}
	for _, sig := range debugSignals {
		if strings.Contains(lower, sig) {
			return BuildClassDebug, CompatibleYes, ""
		}
	}

	releaseSignals := []string{"-c opt", "--compilation_mode=opt"}
	for _, sig := range releaseSignals {
		if strings.Contains(lower, sig) {
			return BuildClassRelease, CompatibleNo,
				"This Bazel build uses -c opt (optimized/release mode). " +
					"Use -c dbg or -c fastbuild for a debug build compatible with the dev loop."
		}
	}

	return BuildClassUnknown, CompatibleUnknown,
		"Bazel build detected but no compilation mode flag found (-c dbg, -c opt, etc.). " +
			"Verify this produces a debug build suitable for the dev loop."
}

// isEASDevProfile checks if the command uses an EAS development-compatible profile.
func isEASDevProfile(lower string) bool {
	if !strings.Contains(lower, "eas") {
		return false
	}
	devProfiles := []string{"--profile development", "--profile revyl-build", "--profile dev"}
	for _, p := range devProfiles {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// isEASReleaseProfile checks if the command uses an EAS production or preview profile.
func isEASReleaseProfile(lower string) bool {
	if !strings.Contains(lower, "eas") {
		return false
	}
	releaseProfiles := []string{"--profile production", "--profile preview"}
	for _, p := range releaseProfiles {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// isAndroidRelease checks for Android release-variant build signals.
func isAndroidRelease(lower string) bool {
	signals := []string{"assemblerelease", "bundlerelease", "--variant=release", "--variant release"}
	for _, s := range signals {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// isAndroidDebug checks for Android debug-variant build signals.
func isAndroidDebug(lower string) bool {
	signals := []string{"assembledebug", "bundledebug", "--variant=debug", "--variant debug"}
	for _, s := range signals {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// checkBuildAge returns a warning if the build is older than staleThresholdDays.
func checkBuildAge(metadata map[string]interface{}) []string {
	builtAt := readString(metadata, "built_at")
	if builtAt == "" {
		return nil
	}

	t, err := time.Parse(time.RFC3339, builtAt)
	if err != nil {
		return nil
	}

	age := time.Since(t)
	days := int(age.Hours() / 24)
	if days >= staleThresholdDays {
		return []string{
			fmt.Sprintf("This build is %d days old. Native code changes since then won't be reflected.", days),
		}
	}
	return nil
}

// checkPackageName returns a warning if the build has no package_name in metadata.
func checkPackageName(metadata map[string]interface{}) []string {
	if len(metadata) == 0 {
		return nil
	}
	pkg := readString(metadata, "package_name")
	if pkg != "" {
		return nil
	}
	pkgID := readString(metadata, "package_id")
	if pkgID != "" {
		return nil
	}
	return []string{
		"Build has no package name; install may require manual bundle ID resolution.",
	}
}

// extractBuildCommand reads the build_command string from metadata.
func extractBuildCommand(metadata map[string]interface{}) string {
	if len(metadata) == 0 {
		return ""
	}
	return readString(metadata, "build_command")
}

// fixCommandsForPlatform returns provider- and platform-aware fix commands.
//
// Parameters:
//   - platformKey: e.g. "ios-dev", "android-dev"
//   - providerName: e.g. "expo", "react-native", "swift"
//
// Returns:
//   - ordered list of fix suggestion strings
func fixCommandsForPlatform(platformKey, providerName string) []string {
	uploadCmd := "revyl build upload"
	if platformKey != "" {
		uploadCmd += " --platform " + platformKey
	}

	cmds := []string{uploadCmd}

	switch providerName {
	case "expo":
		cmds = append(cmds, "eas build --profile development  (Expo/EAS)")
	case "react-native":
		cmds = append(cmds,
			"xcodebuild -configuration Debug  (iOS)",
			"./gradlew assembleDebug          (Android)",
		)
	default:
		cmds = append(cmds,
			"eas build --profile development  (Expo/EAS)",
			"xcodebuild -configuration Debug  (native iOS)",
			"./gradlew assembleDebug          (native Android)",
		)
	}

	return cmds
}
