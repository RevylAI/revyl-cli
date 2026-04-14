package buildselection

import (
	"fmt"
	"testing"
	"time"
)

func TestClassifyBuild_BuildCommand(t *testing.T) {
	tests := []struct {
		name         string
		metadata     map[string]interface{}
		provider     string
		wantClass    BuildClass
		wantCompat   Compatibility
		wantReasonNe bool // true if we expect a non-empty Reason
	}{
		{
			name:       "EAS development profile",
			metadata:   meta("npx eas-cli build --profile development --platform ios"),
			provider:   "expo",
			wantClass:  BuildClassDevClient,
			wantCompat: CompatibleYes,
		},
		{
			name:       "EAS revyl-build profile",
			metadata:   meta("npx --yes eas-cli build --profile revyl-build --platform android"),
			provider:   "expo",
			wantClass:  BuildClassDevClient,
			wantCompat: CompatibleYes,
		},
		{
			name:       "EAS dev profile shorthand",
			metadata:   meta("eas build --profile dev --platform ios"),
			provider:   "expo",
			wantClass:  BuildClassDevClient,
			wantCompat: CompatibleYes,
		},
		{
			name:         "EAS production profile",
			metadata:     meta("npx eas-cli build --profile production --platform ios"),
			provider:     "expo",
			wantClass:    BuildClassRelease,
			wantCompat:   CompatibleNo,
			wantReasonNe: true,
		},
		{
			name:         "EAS preview profile",
			metadata:     meta("eas build --profile preview --platform android"),
			provider:     "expo",
			wantClass:    BuildClassRelease,
			wantCompat:   CompatibleNo,
			wantReasonNe: true,
		},
		{
			name:       "xcodebuild Debug configuration",
			metadata:   meta("cd ios && xcodebuild -workspace ios/app.xcworkspace -scheme app -configuration Debug -sdk iphonesimulator"),
			provider:   "react-native",
			wantClass:  BuildClassDebug,
			wantCompat: CompatibleYes,
		},
		{
			name:         "xcodebuild Release configuration",
			metadata:     meta("xcodebuild -workspace ios/bookwise.xcworkspace -scheme bookwise -configuration Release -archivePath build/bookwise.xcarchive archive"),
			provider:     "react-native",
			wantClass:    BuildClassRelease,
			wantCompat:   CompatibleNo,
			wantReasonNe: true,
		},
		{
			name:         "xcodebuild Release case insensitive",
			metadata:     meta("xcodebuild -configuration RELEASE -sdk iphoneos"),
			provider:     "swift",
			wantClass:    BuildClassRelease,
			wantCompat:   CompatibleNo,
			wantReasonNe: true,
		},
		{
			name:       "Gradle assembleDebug",
			metadata:   meta("cd android && ./gradlew assembleDebug"),
			provider:   "react-native",
			wantClass:  BuildClassDebug,
			wantCompat: CompatibleYes,
		},
		{
			name:         "Gradle assembleRelease",
			metadata:     meta("cd android && ./gradlew assembleRelease"),
			provider:     "react-native",
			wantClass:    BuildClassRelease,
			wantCompat:   CompatibleNo,
			wantReasonNe: true,
		},
		{
			name:         "Gradle bundleRelease",
			metadata:     meta("./gradlew bundleRelease"),
			provider:     "android",
			wantClass:    BuildClassRelease,
			wantCompat:   CompatibleNo,
			wantReasonNe: true,
		},
		{
			name:         "variant=release flag",
			metadata:     meta("./gradlew assemble --variant=release"),
			provider:     "android",
			wantClass:    BuildClassRelease,
			wantCompat:   CompatibleNo,
			wantReasonNe: true,
		},
		{
			name:       "variant=debug flag",
			metadata:   meta("./gradlew assemble --variant=debug"),
			provider:   "android",
			wantClass:  BuildClassDebug,
			wantCompat: CompatibleYes,
		},
		{
			name:         "empty build command",
			metadata:     meta(""),
			provider:     "expo",
			wantClass:    BuildClassUnknown,
			wantCompat:   CompatibleUnknown,
			wantReasonNe: true,
		},
		{
			name:         "nil metadata",
			metadata:     nil,
			provider:     "expo",
			wantClass:    BuildClassUnknown,
			wantCompat:   CompatibleUnknown,
			wantReasonNe: true,
		},
		{
			name:         "unrecognized command",
			metadata:     meta("make build-ios"),
			provider:     "swift",
			wantClass:    BuildClassUnknown,
			wantCompat:   CompatibleUnknown,
			wantReasonNe: true,
		},
		{
			name:       "Bazel debug build (-c dbg)",
			metadata:   meta("bazel build //ios:MyApp -c dbg"),
			provider:   "swift",
			wantClass:  BuildClassDebug,
			wantCompat: CompatibleYes,
		},
		{
			name:       "Bazel fastbuild",
			metadata:   meta("bazel build //android:app -c fastbuild"),
			provider:   "android",
			wantClass:  BuildClassDebug,
			wantCompat: CompatibleYes,
		},
		{
			name:         "Bazel opt build (release)",
			metadata:     meta("bazel build //ios:MyApp -c opt"),
			provider:     "swift",
			wantClass:    BuildClassRelease,
			wantCompat:   CompatibleNo,
			wantReasonNe: true,
		},
		{
			name:         "Bazel compilation_mode=opt",
			metadata:     meta("bazel build //android:app --compilation_mode=opt"),
			provider:     "android",
			wantClass:    BuildClassRelease,
			wantCompat:   CompatibleNo,
			wantReasonNe: true,
		},
		{
			name:       "Bazel compilation_mode=dbg",
			metadata:   meta("bazel build //ios:MyApp --compilation_mode=dbg"),
			provider:   "swift",
			wantClass:  BuildClassDebug,
			wantCompat: CompatibleYes,
		},
		{
			name:         "Bazel no compilation mode",
			metadata:     meta("bazel build //ios:MyApp"),
			provider:     "swift",
			wantClass:    BuildClassUnknown,
			wantCompat:   CompatibleUnknown,
			wantReasonNe: true,
		},
		{
			name:       "Bazelisk debug build",
			metadata:   meta("bazelisk build //ios:MyApp -c dbg"),
			provider:   "swift",
			wantClass:  BuildClassDebug,
			wantCompat: CompatibleYes,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ClassifyBuild(tt.metadata, tt.provider, "ios-dev")

			if result.Class != tt.wantClass {
				t.Errorf("Class = %q, want %q", result.Class, tt.wantClass)
			}
			if result.Compatible != tt.wantCompat {
				t.Errorf("Compatible = %d, want %d", result.Compatible, tt.wantCompat)
			}
			if tt.wantReasonNe && result.Reason == "" {
				t.Error("expected non-empty Reason")
			}
			if !tt.wantReasonNe && result.Reason != "" {
				t.Errorf("expected empty Reason, got %q", result.Reason)
			}
		})
	}
}

func TestClassifyBuild_FixCommands(t *testing.T) {
	release := meta("xcodebuild -configuration Release")

	result := ClassifyBuild(release, "expo", "ios-dev")
	if result.Compatible != CompatibleNo {
		t.Fatal("expected CompatibleNo for Release build")
	}
	if len(result.FixCommands) == 0 {
		t.Fatal("expected fix commands for incompatible build")
	}

	foundUpload := false
	for _, cmd := range result.FixCommands {
		if cmd == "revyl build upload --platform ios-dev" {
			foundUpload = true
		}
	}
	if !foundUpload {
		t.Errorf("expected 'revyl build upload --platform ios-dev' in fix commands, got %v", result.FixCommands)
	}

	compatible := meta("eas build --profile development --platform ios")
	resultOK := ClassifyBuild(compatible, "expo", "ios-dev")
	if len(resultOK.FixCommands) != 0 {
		t.Errorf("expected no fix commands for compatible build, got %v", resultOK.FixCommands)
	}
}

func TestClassifyBuild_StaleWarning(t *testing.T) {
	old := map[string]interface{}{
		"build_command": "eas build --profile development --platform ios",
		"built_at":      time.Now().Add(-20 * 24 * time.Hour).UTC().Format(time.RFC3339),
	}
	result := ClassifyBuild(old, "expo", "ios-dev")
	if result.Compatible != CompatibleYes {
		t.Fatal("expected CompatibleYes for dev client build")
	}

	foundStale := false
	for _, w := range result.Warnings {
		if len(w) > 0 {
			foundStale = true
		}
	}
	if !foundStale {
		t.Error("expected stale-build warning for 20-day-old build")
	}
}

func TestClassifyBuild_FreshNoStaleWarning(t *testing.T) {
	fresh := map[string]interface{}{
		"build_command": "eas build --profile development --platform ios",
		"built_at":      time.Now().Add(-2 * 24 * time.Hour).UTC().Format(time.RFC3339),
		"package_name":  "com.example.app",
	}
	result := ClassifyBuild(fresh, "expo", "ios-dev")
	for _, w := range result.Warnings {
		if w != "" && len(w) > 10 {
			t.Errorf("unexpected warning for fresh build: %q", w)
		}
	}
}

func TestClassifyBuild_MissingPackageName(t *testing.T) {
	noPackage := map[string]interface{}{
		"build_command": "eas build --profile development --platform ios",
	}
	result := ClassifyBuild(noPackage, "expo", "ios-dev")

	foundPkgWarning := false
	for _, w := range result.Warnings {
		if len(w) > 0 && contains(w, "package name") {
			foundPkgWarning = true
		}
	}
	if !foundPkgWarning {
		t.Error("expected package-name warning when package_name is missing")
	}
}

func TestClassifyBuild_HasPackageName(t *testing.T) {
	withPackage := map[string]interface{}{
		"build_command": "eas build --profile development --platform ios",
		"package_name":  "com.example.app",
	}
	result := ClassifyBuild(withPackage, "expo", "ios-dev")

	for _, w := range result.Warnings {
		if contains(w, "package name") {
			t.Errorf("unexpected package-name warning: %q", w)
		}
	}
}

func TestFixCommandsForPlatform_ProviderVariants(t *testing.T) {
	tests := []struct {
		provider    string
		platformKey string
		wantContain string
	}{
		{"expo", "ios-dev", "eas build --profile development"},
		{"react-native", "android-dev", "assembleDebug"},
		{"swift", "ios-prod", "eas build --profile development"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/%s", tt.provider, tt.platformKey), func(t *testing.T) {
			cmds := fixCommandsForPlatform(tt.platformKey, tt.provider)
			found := false
			for _, c := range cmds {
				if contains(c, tt.wantContain) {
					found = true
				}
			}
			if !found {
				t.Errorf("expected fix command containing %q, got %v", tt.wantContain, cmds)
			}
		})
	}
}

func meta(buildCommand string) map[string]interface{} {
	if buildCommand == "" {
		return map[string]interface{}{}
	}
	return map[string]interface{}{
		"build_command": buildCommand,
		"package_name":  "com.example.app",
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && len(substr) > 0 && indexSubstring(s, substr) >= 0
}

func indexSubstring(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
