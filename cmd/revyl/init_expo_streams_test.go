package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

func TestRunInitNonInteractiveExpoWritesNativePreviewAndDevClientDevelopmentProfiles(t *testing.T) {
	resetInitGlobals(t)
	initNonInteractive = true
	stubInitHasLocalCredentials(t, true)
	quiet := ui.IsQuietMode()
	ui.SetQuietMode(false)
	t.Cleanup(func() { ui.SetQuietMode(quiet) })

	workDir := filepath.Join(t.TempDir(), "expo-app")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", workDir, err)
	}
	writeExpoPreflightFile(t, workDir, "app.json", `{"expo":{"name":"Revyl Expo Minimal","slug":"revyl-playground","ios":{"bundleIdentifier":"com.example.expo"},"android":{"package":"com.example.expo"}}}`)
	writeExpoPreflightFile(t, workDir, "package.json", `{"name":"expo-app","dependencies":{"expo":"~50.0.0","react-native":"0.73.0"}}`)
	writeExpoPreflightFile(t, workDir, "eas.json", `{"build":{"preview":{"android":{"buildType":"apk"}}}}`)
	gitInitForInitTest(t, workDir)
	withWorkingDir(t, workDir)

	cmd := &cobra.Command{Use: "init"}
	cmd.Flags().Bool("dev", false, "")
	var runErr error
	stdout, stderr := captureStdoutAndStderrSeparate(t, func() { runErr = runInit(cmd, nil) })
	if runErr != nil {
		t.Fatalf("runInit() error = %v\n%s", runErr, stderr)
	}
	if stdout != "" {
		t.Fatalf("init -y stdout = %q, want human output on stderr only", stdout)
	}

	data, err := os.ReadFile(filepath.Join(workDir, ".revyl", "config.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(config.yaml) error = %v", err)
	}
	authored, err := config.ParseAuthoredConfig(data)
	if err != nil {
		t.Fatalf("ParseAuthoredConfig() error = %v\n%s", err, data)
	}
	if authored.Build == nil || authored.Build.Framework != "expo" {
		t.Fatalf("build = %+v, want expo framework", authored.Build)
	}
	preview := authored.Build.Profiles["preview"]
	assertInitRecipe(t, "preview.ios", preview.IOS,
		[]string{"npm install", "if [ ! -d ios ]; then npx expo prebuild --platform ios; fi"},
		[]string{"cd ios && if [ ! -d Pods ] || [ ! -d RevylExpoMinimal.xcworkspace ]; then pod install; fi && xcodebuild -workspace RevylExpoMinimal.xcworkspace -scheme 'RevylExpoMinimal' -configuration Release -sdk iphonesimulator -destination 'generic/platform=iOS Simulator' -derivedDataPath build"},
		"ios/build/Build/Products/Release-iphonesimulator/*.app")
	assertInitRecipe(t, "preview.android", preview.Android,
		[]string{"npm install", "if [ ! -d android ]; then npx expo prebuild --platform android; fi"},
		[]string{"cd android && ./gradlew assembleRelease"},
		"android/app/build/outputs/apk/release/app-release.apk")
	development := authored.Build.Profiles["development"]
	assertInitRecipe(t, "development.ios", development.IOS, nil,
		[]string{"npx --yes eas-cli build --platform ios --profile development --local --output build/app.tar.gz"},
		"build/app.tar.gz")
	assertInitRecipe(t, "development.android", development.Android, nil,
		[]string{"npx --yes eas-cli build --platform android --profile development --local --output build/app.apk"},
		"build/app.apk")
	if len(authored.Build.Profiles) != 2 {
		t.Fatalf("profiles = %v, want only preview and development", authored.Build.Profiles)
	}
	if strings.Contains(string(data), "EXPO_TOKEN") {
		t.Fatalf("native Expo recipes must not reference EXPO_TOKEN:\n%s", data)
	}

	if !strings.Contains(stderr, `Xcode scheme "RevylExpoMinimal" is the project name expo prebuild generates from app.json expo.name`) {
		t.Fatalf("init output must say where the Xcode scheme came from:\n%s", stderr)
	}
	assertOrderedSubstrings(t, stderr,
		"Next steps:",
		"1. Create an app per platform and put its ID in build.profiles.preview.<platform>.app_id:",
		"revyl app create --name "+quoteCLIRecoveryArgument("expo-app iOS")+" --platform ios",
		"revyl app create --name "+quoteCLIRecoveryArgument("expo-app Android")+" --platform android",
		"2. revyl config validate",
		"3. revyl github status",
		"4. revyl config push",
		"5. revyl build --profile preview --platform <ios|android> --remote",
		"6. revyl device start --app-id <app-id>",
	)
	for _, retired := range []string{"smoke-test", "revyl test create", "revyl test run", "revyl auth login", "revyl init --force"} {
		if strings.Contains(stderr, retired) {
			t.Fatalf("init -y next steps must not suggest %q:\n%s", retired, stderr)
		}
	}
}

func TestRunInitNonInteractiveStartsNextStepsWithLoginOnlyWithoutLocalCredentials(t *testing.T) {
	for _, hasCredentials := range []bool{false, true} {
		t.Run(fmt.Sprintf("credentials=%v", hasCredentials), func(t *testing.T) {
			resetInitGlobals(t)
			initNonInteractive = true
			stubInitHasLocalCredentials(t, hasCredentials)
			quiet := ui.IsQuietMode()
			ui.SetQuietMode(false)
			t.Cleanup(func() { ui.SetQuietMode(quiet) })
			workDir := t.TempDir()
			gitInitForInitTest(t, workDir)
			withWorkingDir(t, workDir)

			cmd := &cobra.Command{Use: "init"}
			cmd.Flags().Bool("dev", false, "")
			var runErr error
			_, stderr := captureStdoutAndStderrSeparate(t, func() { runErr = runInit(cmd, nil) })
			if runErr != nil {
				t.Fatalf("runInit() error = %v\n%s", runErr, stderr)
			}

			firstStep := "1. Add build_commands and output_path"
			if !hasCredentials {
				firstStep = "1. revyl auth login"
				assertOrderedSubstrings(t, stderr, "Next steps:", firstStep, "2. Add build_commands and output_path")
			} else if strings.Contains(stderr, "revyl auth login") {
				t.Fatalf("next steps must not ask an authenticated user to log in:\n%s", stderr)
			}
			assertOrderedSubstrings(t, stderr, "Next steps:", firstStep)
		})
	}
}

func stubInitHasLocalCredentials(t *testing.T, hasCredentials bool) {
	t.Helper()
	original := initHasLocalCredentials
	initHasLocalCredentials = func() bool { return hasCredentials }
	t.Cleanup(func() { initHasLocalCredentials = original })
}

func TestPrintInitNextStepsBuildsTheOnlyRunnableProfilePlatform(t *testing.T) {
	cfg := &initConfigDraft{
		Project: initProjectDraft{Name: "rn-app"},
		Build: initBuildDraft{Recipes: map[string]initBuildRecipeDraft{
			"android": {Profile: "development", Platform: "android", BuildCommands: []string{"cd android && ./gradlew assembleDebug"}, OutputPath: "android/app/build/outputs/apk/debug/app-debug.apk"},
			"ios":     {Profile: "development", Platform: "ios", IncompleteDetectorPlaceholder: true},
		}},
	}

	messages := captureInitNextSteps(t, cfg, true)

	want := []string{
		"Next steps:",
		"  1. revyl auth login",
		"  2. Create an app per platform and put its ID in build.profiles.development.<platform>.app_id:",
		"       revyl app create --name " + quoteCLIRecoveryArgument("rn-app Android") + " --platform android",
		"  3. revyl config validate",
		"  4. revyl github status   # if GitHub is not connected: revyl github connect",
		"  5. revyl config push",
		"  6. revyl build --profile development --platform android --remote",
		"  7. revyl device start --app-id <app-id>",
	}
	if !reflect.DeepEqual(messages, want) {
		t.Fatalf("next steps =\n%s\nwant\n%s", strings.Join(messages, "\n"), strings.Join(want, "\n"))
	}
}

func TestInitNextStepsProfilePrefersRunnableProfileOverPlaceholderPreview(t *testing.T) {
	runnable := initBuildRecipeDraft{BuildCommands: []string{"build"}, OutputPath: "app.apk"}
	tests := []struct {
		name          string
		recipes       map[string]initBuildRecipeDraft
		wantProfile   string
		wantPlatforms []string
	}{
		{
			name: "placeholder preview yields to runnable development",
			recipes: map[string]initBuildRecipeDraft{
				"ios-preview": {Profile: "preview", Platform: "ios", IncompleteDetectorPlaceholder: true},
				"android":     {Profile: "development", Platform: "android", BuildCommands: runnable.BuildCommands, OutputPath: runnable.OutputPath},
			},
			wantProfile:   "development",
			wantPlatforms: []string{"android"},
		},
		{
			name: "runnable preview wins",
			recipes: map[string]initBuildRecipeDraft{
				"android-preview": {Profile: "preview", Platform: "android", BuildCommands: runnable.BuildCommands, OutputPath: runnable.OutputPath},
				"android":         {Profile: "development", Platform: "android", BuildCommands: runnable.BuildCommands, OutputPath: runnable.OutputPath},
			},
			wantProfile:   "preview",
			wantPlatforms: []string{"android"},
		},
		{
			name: "declared preview wins when nothing is runnable",
			recipes: map[string]initBuildRecipeDraft{
				"ios-preview": {Profile: "preview", Platform: "ios"},
				"android":     {Profile: "development", Platform: "android"},
			},
			wantProfile:   "preview",
			wantPlatforms: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile, platforms := initNextStepsProfile(&initConfigDraft{Build: initBuildDraft{Recipes: tt.recipes}})
			if profile != tt.wantProfile || !reflect.DeepEqual(platforms, tt.wantPlatforms) {
				t.Fatalf("initNextStepsProfile() = %q, %v; want %q, %v", profile, platforms, tt.wantProfile, tt.wantPlatforms)
			}
		})
	}
}

func TestPrintInitNextStepsAsksForAPreviewRecipeWhenNoneIsRunnable(t *testing.T) {
	messages := strings.Join(captureInitNextSteps(t, &initConfigDraft{Project: initProjectDraft{Name: "app"}}, false), "\n")

	assertOrderedSubstrings(t, messages,
		"1. Add build_commands and output_path to build.profiles.preview.<ios|android> in .revyl/config.yaml",
		`revyl app create --name "<name> <platform>" --platform <ios|android>`,
		"revyl build --profile preview --platform <ios|android> --remote",
	)
}

func captureInitNextSteps(t *testing.T, cfg *initConfigDraft, includeAuthLogin bool) []string {
	t.Helper()
	quiet := ui.IsQuietMode()
	ui.SetQuietMode(false)
	var messages []string
	ui.SetOutputObserver(func(_ string, message string) {
		messages = append(messages, message)
	})
	t.Cleanup(func() {
		ui.SetOutputObserver(nil)
		ui.SetQuietMode(quiet)
	})
	printInitNextSteps(cfg, includeAuthLogin)
	return messages
}

func assertInitRecipe(t *testing.T, name string, recipe *config.AuthoredBuildRecipe, wantSetup, wantBuild []string, wantOutput string) {
	t.Helper()
	if recipe == nil || recipe.BuildCommands == nil || recipe.OutputPath == nil {
		t.Fatalf("%s recipe = %+v, want build commands and output path", name, recipe)
	}
	if !reflect.DeepEqual(recipe.SetupCommands, config.CommandStepItems(wantSetup)) {
		t.Fatalf("%s setup_commands = %+v, want %q", name, recipe.SetupCommands, wantSetup)
	}
	if !reflect.DeepEqual(*recipe.BuildCommands, config.CommandStepItems(wantBuild)) {
		t.Fatalf("%s build_commands = %+v, want %q", name, *recipe.BuildCommands, wantBuild)
	}
	if *recipe.OutputPath != wantOutput {
		t.Fatalf("%s output_path = %q, want %q", name, *recipe.OutputPath, wantOutput)
	}
}

func assertOrderedSubstrings(t *testing.T, output string, substrings ...string) {
	t.Helper()
	offset := 0
	for _, substring := range substrings {
		index := strings.Index(output[offset:], substring)
		if index < 0 {
			t.Fatalf("output is missing %q after offset %d:\n%s", substring, offset, output)
		}
		offset += index + len(substring)
	}
}

func TestEnsureInitExpoDevClientSchemeKeepsDetectedSchemeTransient(t *testing.T) {
	dir := t.TempDir()
	writeExpoPreflightFile(t, dir, "app.json", `{"expo":{"name":"Demo","scheme":"demo-dev"}}`)
	cfg := &initConfigDraft{
		Project: initProjectDraft{ID: "11111111-1111-4111-8111-111111111111"},
		Build:   initBuildDraft{DetectedSystem: build.SystemExpo},
	}

	quiet := ui.IsQuietMode()
	ui.SetQuietMode(false)
	var messages []string
	ui.SetOutputObserver(func(_ string, message string) {
		messages = append(messages, message)
	})
	t.Cleanup(func() {
		ui.SetOutputObserver(nil)
		ui.SetQuietMode(quiet)
	})

	if err := ensureInitExpoDevClientScheme(dir, cfg); err != nil {
		t.Fatalf("ensureInitExpoDevClientScheme() error = %v", err)
	}
	expo := cfg.HotReload.provider("expo")
	if expo == nil || expo.AppScheme != "demo-dev" {
		t.Fatalf("transient Expo provider = %+v, want app scheme demo-dev", expo)
	}
	authored, err := cfg.canonicalAuthoredConfig()
	if err != nil {
		t.Fatalf("canonicalAuthoredConfig() error = %v", err)
	}
	writtenBytes, err := config.MarshalCanonicalConfig(*authored)
	if err != nil {
		t.Fatalf("MarshalCanonicalConfig() error = %v", err)
	}
	written := string(writtenBytes)
	if strings.Contains(written, "demo-dev") || strings.Contains(written, "hotreload") {
		t.Fatalf("canonical config persisted transient Expo state:\n%s", written)
	}
	output := strings.Join(messages, "\n")
	if !strings.Contains(output, "using it for this init run") {
		t.Fatalf("output = %q, want transient-use wording", output)
	}
	if strings.Contains(output, "saved") || strings.Contains(output, "persist") {
		t.Fatalf("output = %q, must not claim persistence", output)
	}
}

func TestSelectableRuntimePlatforms_FromStreamKeys(t *testing.T) {
	cfg := &initConfigDraft{
		Build: initBuildDraft{
			Recipes: map[string]initBuildRecipeDraft{
				"ios-dev":     {},
				"ios-ci":      {},
				"android-dev": {},
			},
		},
	}

	got := selectableRuntimePlatforms(cfg)
	want := []string{"ios", "android"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selectableRuntimePlatforms() = %v, want %v", got, want)
	}
}

func TestResolveAppIDForRuntimePlatform_PrefersHotReloadMapping(t *testing.T) {
	cfg := &initConfigDraft{
		Build: initBuildDraft{
			Recipes: map[string]initBuildRecipeDraft{
				"ios-dev": {AppID: "dev-app-id"},
				"ios-ci":  {AppID: "ci-app-id"},
			},
		},
		HotReload: initHotReloadDraft{
			Providers: map[string]*initProviderDraft{
				"expo": {
					PlatformKeys: map[string]string{
						"ios": "ios-ci",
					},
				},
			},
		},
	}

	got := resolveAppIDForRuntimePlatform(cfg, "ios")
	if got != "ci-app-id" {
		t.Fatalf("resolveAppIDForRuntimePlatform() = %q, want %q", got, "ci-app-id")
	}
}

func TestResolveAppIDForRuntimePlatform_FallsBackToBestKey(t *testing.T) {
	cfg := &initConfigDraft{
		Build: initBuildDraft{
			Recipes: map[string]initBuildRecipeDraft{
				"ios-ci":  {BuildCommands: []string{"xcodebuild-ci"}, OutputPath: "ci.app", AppID: "ci-app-id"},
				"ios-dev": {BuildCommands: []string{"xcodebuild-dev"}, OutputPath: "dev.app", AppID: "dev-app-id"},
			},
		},
	}

	got := resolveAppIDForRuntimePlatform(cfg, "ios")
	if got != "dev-app-id" {
		t.Fatalf("resolveAppIDForRuntimePlatform() = %q, want %q", got, "dev-app-id")
	}
}

func TestDefaultExpoDevBuildTargetsForHost_DarwinPrefersIOS(t *testing.T) {
	got := defaultExpoDevBuildTargetsForHost([]string{"android-dev", "ios-dev"}, "darwin")
	want := []string{"ios-dev"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaultExpoDevBuildTargetsForHost() = %v, want %v", got, want)
	}
}

func TestDefaultExpoDevBuildTargetsForHost_NonDarwinPrefersAndroid(t *testing.T) {
	got := defaultExpoDevBuildTargetsForHost([]string{"ios-dev", "android-dev"}, "linux")
	want := []string{"android-dev"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaultExpoDevBuildTargetsForHost() = %v, want %v", got, want)
	}
}

func TestDefaultExpoDevBuildTargetsForHost_FallbackToAvailableStream(t *testing.T) {
	got := defaultExpoDevBuildTargetsForHost([]string{"android-dev"}, "darwin")
	want := []string{"android-dev"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaultExpoDevBuildTargetsForHost() = %v, want %v", got, want)
	}
}
