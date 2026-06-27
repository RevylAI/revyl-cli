// Package prconfig scaffolds the pr_review (GitHub PR automation) section of a
// .revyl/config.yaml. It is shared by the `revyl github` commands and the TUI
// integrations screen so both generate identical, backend-compatible configs.
package prconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
)

// frameworkDefault holds the default build command and artifact path for a
// framework. It mirrors FRAMEWORK_DEFAULTS in the backend
// (cognisim_backend/app/services/scm_config_file.py) so CLI-generated configs
// match what the backend reconciles when a command is omitted.
type frameworkDefault struct {
	platform string
	command  string
	artifact string
}

// frameworkDefaults maps a framework id to its default build command/artifact.
var frameworkDefaults = map[string]frameworkDefault{
	"expo_ios": {
		platform: "ios",
		command: strings.Join([]string{
			"bun install",
			"bunx expo prebuild --platform ios --non-interactive",
			"cd ios && pod install",
			"xcodebuild -workspace ios/App.xcworkspace -scheme App " +
				"-configuration Debug -sdk iphonesimulator -derivedDataPath build",
		}, "\n"),
		artifact: "build/Build/Products/Debug-iphonesimulator/*.app",
	},
	"expo_android": {
		platform: "android",
		command:  "bun install\ncd android && ./gradlew assembleDebug",
		artifact: "android/app/build/outputs/apk/debug/*.apk",
	},
	"react_native_ios": {
		platform: "ios",
		command: "xcodebuild -workspace ios/App.xcworkspace -scheme App " +
			"-configuration Debug -sdk iphonesimulator -derivedDataPath build",
		artifact: "build/Build/Products/Debug-iphonesimulator/*.app",
	},
	"react_native_android": {
		platform: "android",
		command:  "cd android && ./gradlew assembleDebug",
		artifact: "android/app/build/outputs/apk/debug/*.apk",
	},
	"native_ios": {
		platform: "ios",
		command: "xcodebuild -scheme App -configuration Debug " +
			"-sdk iphonesimulator -derivedDataPath build",
		artifact: "build/Build/Products/Debug-iphonesimulator/*.app",
	},
	"native_android": {
		platform: "android",
		command:  "./gradlew assembleDebug",
		artifact: "app/build/outputs/apk/debug/*.apk",
	},
}

// SupportedFrameworks lists the framework ids accepted by Scaffold/BuildBuilds.
var SupportedFrameworks = []string{
	"expo_ios", "expo_android",
	"react_native_ios", "react_native_android",
	"native_ios", "native_android",
}

// Scaffold detects the repo's build setup and writes a pr_review section into
// cfg at configPath.
//
// Parameters:
//   - root: The repository root to scan for build systems.
//   - configPath: The .revyl/config.yaml path to write.
//   - cfg: The project config to populate (mutated in place).
//   - framework: An optional framework id to force; empty means auto-detect.
//   - force: When false and a pr_review section already exists, returns an error
//     instructing the user to re-run with force enabled.
//
// Returns:
//   - error: A non-nil error when pr_review exists without force, the framework
//     id is invalid, or the file cannot be written.
func Scaffold(
	root, configPath string,
	cfg *config.ProjectConfig,
	framework string,
	force bool,
) error {
	if cfg == nil {
		return fmt.Errorf("project config is nil")
	}
	if cfg.PRReview != nil && !force {
		return fmt.Errorf(
			"pr_review already exists in %s\nre-run with --force to overwrite",
			configPath,
		)
	}

	builds, err := BuildBuilds(root, cfg, framework)
	if err != nil {
		return err
	}

	cfg.PRReview = &config.PRReviewConfig{
		Enabled:    true,
		SkipDrafts: true,
		Actions:    config.PRReviewActions{PreviewLink: true},
		Builds:     builds,
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	return config.WriteProjectConfig(configPath, cfg)
}

// BuildBuilds detects (or, when framework is set, forces) the per-platform
// preview builds for a pr_review config.
//
// Parameters:
//   - root: The repository root to scan.
//   - cfg: The current project config (used for the app naming hint).
//   - framework: An optional framework id to force; empty means auto-detect.
//
// Returns:
//   - config.PRReviewBuilds: The scaffolded builds.
//   - error: A non-nil error when an explicit framework id is invalid or the
//     build system cannot be detected.
func BuildBuilds(
	root string,
	cfg *config.ProjectConfig,
	framework string,
) (config.PRReviewBuilds, error) {
	builds := config.PRReviewBuilds{}
	appBaseName := appBaseName(root, cfg)

	if framework != "" {
		defaults, ok := frameworkDefaults[framework]
		if !ok {
			return builds, fmt.Errorf(
				"unknown framework %q (expected one of: %s)",
				framework, strings.Join(SupportedFrameworks, ", "),
			)
		}
		entry := entryFromDefaults(framework, defaults, appBaseName)
		assignBuildEntry(&builds, defaults.platform, entry)
		return builds, nil
	}

	detected, err := build.Detect(root)
	if err != nil {
		return builds, fmt.Errorf("failed to detect build system: %w", err)
	}

	for _, platform := range []string{"ios", "android"} {
		platformBuild, ok := detected.Platforms[platform]
		if !ok || strings.TrimSpace(platformBuild.Command) == "" {
			continue
		}
		entry := &config.PRReviewBuildEntry{
			Enabled:      true,
			Framework:    frameworkForSystem(detected.System, platform),
			App:          fmt.Sprintf("%s %s", appBaseName, PlatformLabel(platform)),
			RootDir:      "./",
			BuildCommand: platformBuild.Command,
			ArtifactPath: platformBuild.Output,
		}
		assignBuildEntry(&builds, platform, entry)
	}

	// Nothing detected: scaffold Expo placeholders for both platforms so the
	// user has a concrete starting point to edit.
	if builds.IOS == nil && builds.Android == nil {
		builds.IOS = entryFromDefaults(
			"expo_ios", frameworkDefaults["expo_ios"], appBaseName,
		)
		builds.Android = entryFromDefaults(
			"expo_android", frameworkDefaults["expo_android"], appBaseName,
		)
	}

	return builds, nil
}

// EntryForPlatform returns the build entry for a platform, or nil when absent.
//
// Parameters:
//   - builds: The scaffolded builds.
//   - platform: "ios" or "android".
//
// Returns:
//   - *config.PRReviewBuildEntry: The entry for the platform, if present.
func EntryForPlatform(
	builds config.PRReviewBuilds,
	platform string,
) *config.PRReviewBuildEntry {
	if platform == "ios" {
		return builds.IOS
	}
	return builds.Android
}

// PlatformLabel returns the human label for a platform key.
//
// Parameters:
//   - platform: "ios" or "android".
//
// Returns:
//   - string: "iOS" or "Android".
func PlatformLabel(platform string) string {
	if platform == "ios" {
		return "iOS"
	}
	return "Android"
}

// entryFromDefaults builds a preview build entry from framework defaults.
func entryFromDefaults(
	framework string,
	defaults frameworkDefault,
	appBase string,
) *config.PRReviewBuildEntry {
	return &config.PRReviewBuildEntry{
		Enabled:      true,
		Framework:    framework,
		App:          fmt.Sprintf("%s %s", appBase, PlatformLabel(defaults.platform)),
		RootDir:      "./",
		BuildCommand: defaults.command,
		ArtifactPath: defaults.artifact,
	}
}

// frameworkForSystem maps a detected build system + platform to a framework id.
func frameworkForSystem(system build.BuildSystem, platform string) string {
	switch system {
	case build.SystemExpo:
		return "expo_" + platform
	case build.SystemReactNative:
		return "react_native_" + platform
	case build.SystemXcode, build.SystemSwift:
		if platform == "ios" {
			return "native_ios"
		}
	case build.SystemGradle:
		if platform == "android" {
			return "native_android"
		}
	}
	return "native_" + platform
}

// assignBuildEntry stores an entry on the correct platform field.
func assignBuildEntry(
	builds *config.PRReviewBuilds,
	platform string,
	entry *config.PRReviewBuildEntry,
) {
	if platform == "ios" {
		builds.IOS = entry
		return
	}
	builds.Android = entry
}

// appBaseName derives a friendly base app name from the config or repo dir.
func appBaseName(root string, cfg *config.ProjectConfig) string {
	if cfg != nil && strings.TrimSpace(cfg.Project.Name) != "" {
		return strings.TrimSpace(cfg.Project.Name)
	}
	base := filepath.Base(root)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "Preview app"
	}
	return base
}
