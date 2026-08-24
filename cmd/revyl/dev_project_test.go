package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

const devProjectID = "11111111-1111-4111-8111-111111111111"

func TestProjectDevContextProviderPreservesRuntimeLabels(t *testing.T) {
	testCases := map[string]string{
		"expo":         "expo",
		"react_native": "react-native",
		"flutter":      "flutter",
		"ios":          "xcode",
		"android":      "gradle",
	}
	for framework, want := range testCases {
		if got := devContextProvider(framework); got != want {
			t.Errorf("devContextProvider(%q) = %q, want %q", framework, got, want)
		}
	}
}

func TestInitDevSessionConsumesCanonicalSession(t *testing.T) {
	resetSessionRuntimes(t)
	projectRoot := t.TempDir()
	scriptPath := "scripts/prepare.sh"
	beforeTimeout := 45
	deepLink := "demo://auth?token=${AUTH_TOKEN}"

	initDevSession(projectRoot, config.AuthoredSession{
		BeforeScript: &config.AuthoredBeforeScript{
			ScriptPath:     &scriptPath,
			TimeoutSeconds: &beforeTimeout,
		},
		AuthBypass: &config.AuthoredAuthBypass{
			LaunchVars: []string{"AUTH_TOKEN"},
			DeepLink:   &deepLink,
		},
	})

	if devBeforeSession == nil || devBeforeSession.repoRoot != projectRoot ||
		authoredBeforeScriptPath(devBeforeSession.cfg) != scriptPath {
		t.Fatalf("before-session runtime = %+v", devBeforeSession)
	}
	if devAuthBypass == nil || authoredAuthBypassDeepLink(devAuthBypass.cfg) != deepLink ||
		!reflect.DeepEqual(devAuthBypass.LaunchVarKeys(), []string{"AUTH_TOKEN"}) {
		t.Fatalf("auth-bypass runtime = %+v", devAuthBypass)
	}
}

func TestProjectDevFlagsExposeProfileWithoutPlatformDefaultOrPlatformKey(t *testing.T) {
	oldProfile, oldPlatform := devStartProfile, devStartPlatform
	t.Cleanup(func() { devStartProfile, devStartPlatform = oldProfile, oldPlatform })
	command := &cobra.Command{Use: "dev"}
	registerDevStartFlags(command)
	if command.Flags().Lookup("profile") == nil {
		t.Fatal("--profile is not registered")
	}
	if command.Flags().Lookup("platform-key") != nil {
		t.Fatal("deprecated --platform-key remains public")
	}
	if got := command.Flags().Lookup("platform").DefValue; got != "" {
		t.Fatalf("--platform default = %q, want no implicit platform", got)
	}
}

func TestResolveDevInvocationUsesNearestProjectAndDevelopmentProfile(t *testing.T) {
	repository := initDevRepository(t)
	projectRoot := filepath.Join(repository, "apps", "mobile")
	writeProjectDevConfig(t, projectRoot, `project:
  id: `+devProjectID+`
session:
  idle_timeout_seconds: 420
  before_script:
    script_path: scripts/prepare.sh
    timeout_seconds: 45
  auth_bypass:
    launch_vars: [AUTH_TOKEN]
    deep_link: "demo://auth?token=${AUTH_TOKEN}"
build:
  framework: expo
  profiles:
    production:
      ios:
        app_id: 22222222-2222-4222-8222-222222222222
        build_commands: [build-production]
    ios-dev:
      ios:
        app_id: 33333333-3333-4333-8333-333333333333
        setup_commands: [setup-dev]
        build_commands: [build-dev]
        output_path: build/dev.app
`)

	invocation, err := resolveDevInvocation(
		filepath.Join(projectRoot, "src", "screens"), "", "", "", false, nil,
	)
	if err != nil {
		t.Fatalf("resolveDevInvocation() error = %v", err)
	}
	canonicalProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if invocation.ProjectRoot != canonicalProjectRoot {
		t.Fatalf("ProjectRoot = %q, want %q", invocation.ProjectRoot, canonicalProjectRoot)
	}
	if invocation.ConfigPath != filepath.Join(canonicalProjectRoot, ".revyl", "config.yaml") {
		t.Fatalf("ConfigPath = %q", invocation.ConfigPath)
	}
	if invocation.Profile != "ios-dev" || invocation.Platform != "ios" {
		t.Fatalf("selection = %s/%s, want ios-dev/ios", invocation.Profile, invocation.Platform)
	}
	if invocation.SelectionSource != "inferred" {
		t.Fatalf("SelectionSource = %q, want inferred", invocation.SelectionSource)
	}
	if invocation.AppID != "33333333-3333-4333-8333-333333333333" {
		t.Fatalf("AppID = %q", invocation.AppID)
	}
	if invocation.BuildDefinitionHash == "" {
		t.Fatal("BuildDefinitionHash is empty")
	}
	if !reflect.DeepEqual(invocation.Recipe.SetupCommands, []string{"setup-dev"}) ||
		!reflect.DeepEqual(invocation.Recipe.BuildCommands, []string{"build-dev"}) {
		t.Fatalf("Recipe = %+v", invocation.Recipe)
	}
	if invocation.Session.IdleTimeoutSeconds == nil || *invocation.Session.IdleTimeoutSeconds != 420 {
		t.Fatalf("Session idle timeout = %#v", invocation.Session.IdleTimeoutSeconds)
	}
	if invocation.Session.BeforeScript == nil || invocation.Session.BeforeScript.ScriptPath == nil ||
		*invocation.Session.BeforeScript.ScriptPath != "scripts/prepare.sh" {
		t.Fatalf("Session before script = %#v", invocation.Session.BeforeScript)
	}
	if invocation.Session.AuthBypass == nil ||
		!reflect.DeepEqual(invocation.Session.AuthBypass.LaunchVars, []string{"AUTH_TOKEN"}) {
		t.Fatalf("Session auth bypass = %#v", invocation.Session.AuthBypass)
	}
	if len(invocation.OriginalConfigBytes) == 0 {
		t.Fatal("OriginalConfigBytes is empty")
	}
}

func TestResolveDevInvocationFiltersProfilesByExplicitPlatformFirst(t *testing.T) {
	repository := initDevRepository(t)
	writeProjectDevConfig(t, repository, `project:
  id: `+devProjectID+`
build:
  framework: react_native
  profiles:
    development:
      ios:
        build_commands: [build-ios]
    production:
      android:
        build_commands: [build-android]
`)

	invocation, err := resolveDevInvocation(repository, "", "", "android", false, nil)
	if err != nil {
		t.Fatalf("resolveDevInvocation() error = %v", err)
	}
	if invocation.Profile != "production" || invocation.Platform != "android" {
		t.Fatalf("selection = %s/%s, want production/android", invocation.Profile, invocation.Platform)
	}
	if invocation.SelectionSource != "explicit" {
		t.Fatalf("SelectionSource = %q, want explicit", invocation.SelectionSource)
	}
}

func TestResolveDevInvocationDoesNotDefaultAmbiguousPlatformToIOS(t *testing.T) {
	repository := initDevRepository(t)
	writeProjectDevConfig(t, repository, `project:
  id: `+devProjectID+`
build:
  framework: flutter
  profiles:
    development:
      ios:
        build_commands: [build-ios]
      android:
        build_commands: [build-android]
`)

	_, err := resolveDevInvocation(repository, "", "", "", false, nil)
	if err == nil {
		t.Fatal("resolveDevInvocation() error = nil, want platform ambiguity")
	}
	if got := err.Error(); !strings.Contains(got, "require --platform") ||
		!strings.Contains(got, "ios, android") {
		t.Fatalf("error = %q, want --platform choices", got)
	}
}

func TestResolveDevInvocationPromptsForAmbiguousPlatform(t *testing.T) {
	repository := initDevRepository(t)
	writeProjectDevConfig(t, repository, `project:
  id: `+devProjectID+`
build:
  framework: expo
  profiles:
    dev:
      ios:
        build_commands: [build-ios]
      android:
        build_commands: [build-android]
`)

	var prompt string
	var choices []string
	invocation, err := resolveDevInvocation(
		repository, "", "", "", true,
		func(message string, options []ui.SelectOption, defaultIndex int) (int, string, error) {
			prompt = message
			for _, option := range options {
				choices = append(choices, option.Value)
			}
			return 1, options[1].Value, nil
		},
	)
	if err != nil {
		t.Fatalf("resolveDevInvocation() error = %v", err)
	}
	if prompt != "Select development platform:" {
		t.Fatalf("prompt = %q", prompt)
	}
	if !reflect.DeepEqual(choices, []string{"ios", "android"}) {
		t.Fatalf("choices = %#v", choices)
	}
	if invocation.Profile != "dev" || invocation.Platform != "android" {
		t.Fatalf("selection = %s/%s, want dev/android", invocation.Profile, invocation.Platform)
	}
	if invocation.SelectionSource != "prompted" {
		t.Fatalf("SelectionSource = %q, want prompted", invocation.SelectionSource)
	}
}

func TestResolveDevInvocationDeviceIsNotDevelopmentLike(t *testing.T) {
	repository := initDevRepository(t)
	writeProjectDevConfig(t, repository, `project:
  id: `+devProjectID+`
build:
  framework: ios
  profiles:
    device:
      ios:
        build_commands: [build-device]
    production:
      ios:
        build_commands: [build-production]
`)

	_, err := resolveDevInvocation(repository, "", "", "", false, nil)
	if err == nil {
		t.Fatal("resolveDevInvocation() error = nil, want profile ambiguity")
	}
	if got := err.Error(); !strings.Contains(got, "require --profile") ||
		!strings.Contains(got, "device, production") {
		t.Fatalf("error = %q, want --profile choices", got)
	}
}

func TestRunDevRecipeWithHooksExecutesImmutableSetupAndBuild(t *testing.T) {
	projectRoot := t.TempDir()
	outputPath := "build/output.txt"
	timeout := 10
	invocation := projectDevInvocation{
		ProjectRoot: projectRoot,
		Profile:     "development",
		Platform:    "ios",
		Recipe: config.EffectiveBuildRecipe{
			SetupCommands:  []string{devProjectTestHelperCommand(t, "write-environment")},
			BuildCommands:  []string{devProjectTestHelperCommand(t, "verify-environment")},
			OutputPath:     &outputPath,
			TimeoutSeconds: &timeout,
			Env:            map[string]string{"RECIPE_VALUE": "original"},
		},
	}

	result := runDevRecipeWithHooks(context.Background(), invocation, nil)
	if result.Err != nil {
		t.Fatalf("runDevRecipeWithHooks() error = %v", result.Err)
	}
	contents, err := os.ReadFile(filepath.Join(projectRoot, outputPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "original" {
		t.Fatalf("output = %q, want immutable invocation env", contents)
	}
}

func devProjectTestHelperCommand(t *testing.T, action string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return quoteBuildTestShellArgument(executable) + " -test.run=TestDevProjectHelperProcess -- " + action
}

func TestDevProjectHelperProcess(t *testing.T) {
	action := ""
	for index, argument := range os.Args {
		if argument == "--" && index+1 < len(os.Args) {
			action = os.Args[index+1]
			break
		}
	}
	if action == "" {
		return
	}

	switch action {
	case "write-environment":
		if err := os.MkdirAll("build", 0o755); err != nil {
			t.Fatalf("create build directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join("build", "setup.txt"), []byte(os.Getenv("RECIPE_VALUE")), 0o644); err != nil {
			t.Fatalf("write setup output: %v", err)
		}
	case "verify-environment":
		contents, err := os.ReadFile(filepath.Join("build", "setup.txt"))
		if err != nil {
			t.Fatalf("read setup output: %v", err)
		}
		if string(contents) != "original" {
			t.Fatalf("setup output = %q, want immutable invocation env", contents)
		}
		if err := os.WriteFile(filepath.Join("build", "output.txt"), contents, 0o644); err != nil {
			t.Fatalf("write build output: %v", err)
		}
	default:
		t.Fatalf("unknown dev project helper action %q", action)
	}
}

func TestRunDevRecipeWithHooksValidatesSecretsBeforeExecutingCommands(t *testing.T) {
	projectRoot := t.TempDir()
	missingSecret := "REVYL_TEST_MISSING_DEV_RECIPE_SECRET"
	t.Setenv(missingSecret, "")
	if err := os.Unsetenv(missingSecret); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(projectRoot, "executed")
	invocation := projectDevInvocation{
		ProjectRoot: projectRoot,
		Profile:     "development",
		Platform:    "ios",
		Recipe: config.EffectiveBuildRecipe{
			SetupCommands: []string{"touch executed"},
			BuildCommands: []string{"touch built"},
			SecretRefs:    []string{missingSecret},
		},
	}

	result := runDevRecipeWithHooks(context.Background(), invocation, nil)
	if result.Err == nil || !strings.Contains(result.Err.Error(), missingSecret) {
		t.Fatalf("error = %v, want missing secret", result.Err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("setup command executed before secret validation: %v", err)
	}
}

func TestRunDevRecipeWithHooksReportsQuietCommands(t *testing.T) {
	originalInterval := devRecipeQuietPeriodInterval
	devRecipeQuietPeriodInterval = 20 * time.Millisecond
	t.Cleanup(func() { devRecipeQuietPeriodInterval = originalInterval })

	quietCalls := 0
	result := runDevRecipeWithHooks(context.Background(), projectDevInvocation{
		ProjectRoot: t.TempDir(),
		Profile:     "development",
		Platform:    "ios",
		Recipe: config.EffectiveBuildRecipe{
			BuildCommands: []string{"sleep 0.12"},
		},
	}, &BuildProgressHooks{
		OnQuietPeriod: func(lineCount int, _ time.Duration, recentLines []string) {
			quietCalls++
			if lineCount != 0 || len(recentLines) != 0 {
				t.Fatalf("silent command recap = count %d lines %#v", lineCount, recentLines)
			}
		},
	})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if quietCalls == 0 {
		t.Fatal("OnQuietPeriod was not called during silent command")
	}
}

func TestRunDevStatusIncludesPersistedProjectSelection(t *testing.T) {
	projectRoot := initDevRepository(t)
	originalDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(projectRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDirectory) })

	contextName := "project-selection"
	pid := os.Getpid()
	if err := saveDevContext(projectRoot, &DevContext{
		Name: contextName, PID: pid, Platform: "ios", Profile: "development",
		State: devContextStateRunning, SessionOwned: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeDevCtxPIDFile(devCtxPIDPath(projectRoot, contextName), pid, 0); err != nil {
		t.Fatal(err)
	}
	writeDevStatusSnapshot(devCtxStatusPath(projectRoot, contextName), devStatus{
		State: devContextStateRunning, PID: pid, Profile: "development", Platform: "ios",
		BuildDefinitionHash: strings.Repeat("a", 64),
	})

	command := &cobra.Command{}
	command.Flags().String("context", contextName, "")
	output := captureStdout(t, func() {
		if err := runDevStatus(command, nil); err != nil {
			t.Fatal(err)
		}
	})
	var status map[string]interface{}
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		t.Fatalf("decode status output %q: %v", output, err)
	}
	if status["profile"] != "development" || status["build_definition_hash"] != strings.Repeat("a", 64) {
		t.Fatalf("status = %#v, want persisted profile/hash", status)
	}
}

func initDevRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	command := exec.Command("git", "init", "-q")
	command.Dir = repository
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	return repository
}

func writeProjectDevConfig(t *testing.T, projectRoot, source string) {
	t.Helper()
	configDirectory := filepath.Join(projectRoot, ".revyl")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectRoot, "src", "screens"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "config.yaml"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}
