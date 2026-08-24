package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

const buildTestProjectID = "11111111-1111-4111-8111-111111111111"

func TestResolveBuildInvocationUsesNearestProjectAndNeverMutatesConfig(t *testing.T) {
	repository := t.TempDir()
	gitInitBuildRepository(t, repository)
	projectRoot := filepath.Join(repository, "apps", "mobile")
	writeProjectBuildConfig(t, projectRoot, projectBuildConfigYAML("development", "android", "build/app.apk", true))
	nested := filepath.Join(projectRoot, "src", "screen")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(projectRoot, ".revyl", "config.yaml")
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	invocation, err := resolveBuildInvocation(nested, "", "", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	resolvedProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if invocation.ProjectRoot != resolvedProjectRoot || invocation.Profile != "development" || invocation.Platform != "android" {
		t.Fatalf("invocation = %+v", invocation)
	}
	if invocation.SelectionSource != "inferred" {
		t.Fatalf("selection source = %q, want inferred", invocation.SelectionSource)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("build invocation resolution mutated canonical configuration")
	}
}

func TestPublicBuildResolutionStartsFromAlreadyChangedDirectory(t *testing.T) {
	repository := t.TempDir()
	gitInitBuildRepository(t, repository)
	projectRoot := filepath.Join(repository, "apps", "mobile")
	writeProjectBuildConfig(t, projectRoot, projectBuildConfigYAML("development", "android", "build/app.apk", true))
	// This is the state after root PersistentPreRun has applied `-C apps/mobile`.
	invocation, err := resolveBuildInvocation(projectRoot, "", "development", "android", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	resolvedProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if invocation.ProjectRoot != resolvedProjectRoot {
		t.Fatalf("project root = %q, want %q", invocation.ProjectRoot, resolvedProjectRoot)
	}
}

func TestResolveBuildInvocationPromptsOnlyForAmbiguity(t *testing.T) {
	repository := t.TempDir()
	gitInitBuildRepository(t, repository)
	configYAML := `project:
  id: ` + buildTestProjectID + `
build:
  framework: expo
  profiles:
    development:
      ios:
        build_commands: ["true"]
        output_path: build/App.app
      android:
        build_commands: ["true"]
        output_path: build/app.apk
`
	writeProjectBuildConfig(t, repository, configYAML)
	var prompts []string
	invocation, err := resolveBuildInvocation(repository, "", "development", "", true, func(message string, options []ui.SelectOption, defaultIndex int) (int, string, error) {
		prompts = append(prompts, message)
		if len(options) != 2 || options[0].Value != "ios" || options[1].Value != "android" {
			t.Fatalf("options = %#v", options)
		}
		return 0, options[0].Value, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if invocation.Platform != "ios" || invocation.SelectionSource != "prompted" {
		t.Fatalf("invocation = %+v", invocation)
	}
	if !reflect.DeepEqual(prompts, []string{"Select build platform:"}) {
		t.Fatalf("prompts = %#v", prompts)
	}
}

func TestResolveBuildInvocationNonInteractiveAmbiguityFailsWithRequiredFlag(t *testing.T) {
	repository := t.TempDir()
	gitInitBuildRepository(t, repository)
	configYAML := `project:
  id: ` + buildTestProjectID + `
build:
  framework: expo
  profiles:
    development:
      ios:
        build_commands: ["true"]
        output_path: build/App.app
      android:
        build_commands: ["true"]
        output_path: build/app.apk
`
	writeProjectBuildConfig(t, repository, configYAML)
	_, err := resolveBuildInvocation(repository, "", "development", "", false, nil)
	if err == nil || !strings.Contains(err.Error(), "--platform") || !strings.Contains(err.Error(), "ios, android") {
		t.Fatalf("error = %v, want actionable --platform ambiguity", err)
	}
}

func TestExecuteLocalRecipePreservesSetupBuildOrderAndEnvironment(t *testing.T) {
	projectRoot := t.TempDir()
	t.Setenv("BUILD_SECRET", "secret-value")
	tracePath := filepath.Join(projectRoot, "trace.txt")
	outputPath := filepath.Join(projectRoot, "build", "app.apk")
	invocation := projectBuildInvocation{
		ProjectRoot: projectRoot,
		Profile:     "development",
		Platform:    "android",
		Recipe: config.EffectiveBuildRecipe{
			SetupCommands:  []string{buildTestHelperCommand(t, "trace-setup")},
			BuildCommands:  []string{buildTestHelperCommand(t, "trace-build-1"), buildTestHelperCommand(t, "trace-build-2")},
			Env:            map[string]string{"PLAIN": "configured"},
			SecretRefs:     []string{"BUILD_SECRET"},
			TimeoutSeconds: intPointerForBuildTest(30),
		},
	}
	if err := validateLocalSecretEnvironment(invocation.Recipe.SecretRefs); err != nil {
		t.Fatal(err)
	}
	if err := executeLocalRecipe(context.Background(), invocation, true, &buildProgress{}); err != nil {
		t.Fatal(err)
	}
	trace, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(trace), "setup:configured:secret-value\nbuild-1\nbuild-2\n"; got != want {
		t.Fatalf("trace = %q, want %q", got, want)
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("output was not produced: %v", err)
	}
}

func TestExecuteLocalRecipePrintsBuildToolGuidanceOnlyToStderr(t *testing.T) {
	projectRoot := t.TempDir()
	invocation := projectBuildInvocation{
		ProjectRoot: projectRoot,
		Recipe: config.EffectiveBuildRecipe{
			BuildCommands: []string{buildTestHelperCommand(t, "bazel-not-found")},
		},
	}

	originalStderr := os.Stderr
	stderrPath := filepath.Join(t.TempDir(), "stderr.txt")
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = stderrFile
	originalQuietMode := ui.IsQuietMode()
	ui.SetQuietMode(true)
	t.Cleanup(func() {
		ui.SetQuietMode(originalQuietMode)
		os.Stderr = originalStderr
		_ = stderrFile.Close()
	})

	var buildErr error
	stdout := captureStdout(t, func() {
		buildErr = executeLocalRecipe(context.Background(), invocation, true, &buildProgress{})
	})
	os.Stderr = originalStderr
	if err := stderrFile.Close(); err != nil {
		t.Fatal(err)
	}
	stderr, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatal(err)
	}

	if stdout != "" {
		t.Fatalf("JSON stdout was polluted: %q", stdout)
	}
	if !strings.Contains(string(stderr), "How to fix:") || !strings.Contains(string(stderr), "brew install bazelisk") {
		t.Fatalf("stderr did not preserve build-tool guidance: %q", stderr)
	}
	var toolErr *build.BuildToolError
	if !errors.As(buildErr, &toolErr) {
		t.Fatalf("error = %v, want wrapped BuildToolError", buildErr)
	}
	if got, want := buildErr.Error(), "build command 1 failed: bazel not found"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestRunLocalBuildWithoutAppUsesExistingResolutionAfterBuild(t *testing.T) {
	t.Setenv("REVYL_API_KEY", "test-key")
	projectRoot := t.TempDir()
	marker := filepath.Join(projectRoot, "built.txt")
	outputPath := "build/app.apk"
	originalSelector := selectOrCreateBuildAppForInvocation
	t.Cleanup(func() { selectOrCreateBuildAppForInvocation = originalSelector })
	selectionErr := &buildSelectionTestError{}
	selectOrCreateBuildAppForInvocation = func(_ *cobra.Command, _ *api.Client, _ projectBuildInvocation) (string, error) {
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("app resolution ran before configured build: %v", err)
		}
		return "", selectionErr
	}
	invocation := projectBuildInvocation{
		ProjectRoot: projectRoot,
		Profile:     "development",
		Platform:    "android",
		Recipe: config.EffectiveBuildRecipe{
			BuildCommands: []string{buildTestHelperCommand(t, "build-and-mark")},
			OutputPath:    &outputPath,
		},
	}
	cmd := newBuildTestCommand()
	err := runLocalBuild(cmd, invocation, "test-key", false, true, &buildProgress{})
	if err != selectionErr {
		t.Fatalf("error = %v, want existing app selection path error", err)
	}
}

func buildTestHelperCommand(t *testing.T, action string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return quoteBuildTestShellArgument(executable) + " -test.run=TestBuildHelperProcess -- " + action
}

func quoteBuildTestShellArgument(value string) string {
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func TestBuildHelperProcess(t *testing.T) {
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

	appendTrace := func(line string) {
		trace, err := os.OpenFile("trace.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatalf("open trace: %v", err)
		}
		if _, err := trace.WriteString(line + "\n"); err != nil {
			_ = trace.Close()
			t.Fatalf("write trace: %v", err)
		}
		if err := trace.Close(); err != nil {
			t.Fatalf("close trace: %v", err)
		}
	}

	switch action {
	case "trace-setup":
		appendTrace("setup:" + os.Getenv("PLAIN") + ":" + os.Getenv("BUILD_SECRET"))
	case "trace-build-1":
		appendTrace("build-1")
	case "trace-build-2":
		if err := os.MkdirAll("build", 0o755); err != nil {
			t.Fatalf("create build directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join("build", "app.apk"), []byte("artifact"), 0o644); err != nil {
			t.Fatalf("write artifact: %v", err)
		}
		appendTrace("build-2")
	case "build-and-mark":
		if err := os.MkdirAll("build", 0o755); err != nil {
			t.Fatalf("create build directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join("build", "app.apk"), nil, 0o644); err != nil {
			t.Fatalf("write artifact: %v", err)
		}
		if err := os.WriteFile("built.txt", nil, 0o644); err != nil {
			t.Fatalf("write build marker: %v", err)
		}
	case "bazel-not-found":
		_, _ = os.Stderr.WriteString("/bin/sh: bazel: command not found\n")
		os.Exit(127)
	default:
		t.Fatalf("unknown build helper action %q", action)
	}
	os.Exit(0)
}

func TestRunLocalBuildWithoutAppFailsBeforeNonInteractiveExecution(t *testing.T) {
	projectRoot := t.TempDir()
	marker := filepath.Join(projectRoot, "built.txt")
	outputPath := "build/app.apk"
	invocation := projectBuildInvocation{
		ProjectRoot: projectRoot,
		Profile:     "development",
		Platform:    "android",
		Recipe: config.EffectiveBuildRecipe{
			BuildCommands: []string{"touch built.txt"},
			OutputPath:    &outputPath,
		},
	}
	err := runLocalBuild(newBuildTestCommand(), invocation, "test-key", true, false, &buildProgress{})
	if err == nil || !strings.Contains(err.Error(), "build.profiles.development.android.app_id") || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("error = %v, want actionable app binding guidance", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("build command ran before noninteractive app validation: %v", statErr)
	}
}

func TestMissingLocalBuildSecretsExplainHowToLoadThem(t *testing.T) {
	t.Setenv("PRESENT_SECRET", "set")
	err := validateLocalSecretEnvironment([]string{"PRESENT_SECRET", "MISSING_SECRET"})
	if err == nil || !strings.Contains(err.Error(), "MISSING_SECRET") || !strings.Contains(err.Error(), "export them") || !strings.Contains(err.Error(), ".env.local") {
		t.Fatalf("error = %v, want actionable secret guidance", err)
	}
}

func TestRemoteBuildWithoutAppExplainsExactBinding(t *testing.T) {
	err := runProjectRemoteBuild(newBuildTestCommand(), projectBuildInvocation{
		Profile:  "release",
		Platform: "ios",
	}, "test-key", true, &buildProgress{})
	if err == nil || !strings.Contains(err.Error(), "build.profiles.release.ios.app_id") || !strings.Contains(err.Error(), "revyl config validate") {
		t.Fatalf("error = %v, want exact remote app binding guidance", err)
	}
}

func TestRemoteBuildAppReferenceErrorsExplainRepair(t *testing.T) {
	invocation := projectBuildInvocation{Profile: "release", Platform: "ios"}
	for _, test := range []struct {
		name string
		err  error
	}{
		{
			name: "missing app",
			err:  &api.APIError{StatusCode: 404, Detail: "App not found"},
		},
		{
			name: "wrong platform",
			err:  &api.APIError{StatusCode: 400, Detail: "Remote build platform 'ios' does not match app platform 'android'"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := actionableRemoteBuildAppReferenceError(test.err, invocation)
			for _, want := range []string{
				"build.profiles.release.ios.app_id",
				"revyl app list --platform ios",
				"replace the app_id",
				"revyl config validate",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %q, want %q", err, want)
				}
			}
			var apiError *api.APIError
			if !errors.As(err, &apiError) {
				t.Fatalf("wrapped API error was lost: %v", err)
			}
		})
	}

	ordinary := errors.New("worker unavailable")
	if got := actionableRemoteBuildAppReferenceError(ordinary, invocation); got != ordinary {
		t.Fatalf("ordinary error changed: %v", got)
	}
}

func TestRenderLocalBuildJSONPreservesUploadEnvelopeAndAddsProfile(t *testing.T) {
	output := captureStdoutAndStderr(t, func() {
		err := renderLocalBuildResult(localBuildResult{
			Invocation: projectBuildInvocation{
				Profile:  "development",
				Platform: "android",
				AppID:    "00000000-0000-4000-8000-000000000001",
			},
			ArtifactPath: "build/app.apk",
			Upload: &api.UploadBuildResponse{
				Version:   "1.2.3",
				VersionID: "build-version-id",
			},
		}, true, true)
		if err != nil {
			t.Fatal(err)
		}
	})
	var payload struct {
		Success bool `json:"success"`
		Count   int  `json:"count"`
		Build   struct {
			Profile     string `json:"profile"`
			PlatformKey string `json:"platform_key"`
			Platform    string `json:"platform"`
		} `json:"build"`
		Builds []json.RawMessage `json:"builds"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("decode JSON %q: %v", output, err)
	}
	if !payload.Success || payload.Count != 1 || len(payload.Builds) != 1 {
		t.Fatalf("upload envelope = %+v", payload)
	}
	if payload.Build.Profile != "development" || payload.Build.PlatformKey != "android" || payload.Build.Platform != "android" {
		t.Fatalf("build identity = %+v", payload.Build)
	}
}

func TestRunCommandsRejectBuildSelectorsWithoutBuild(t *testing.T) {
	originalTestBuild, originalTestProfile, originalTestPlatform, originalBuildID := runTestBuild, runTestProfile, runTestPlatform, runBuildID
	originalWorkflowBuild, originalWorkflowProfile, originalWorkflowPlatform := runWorkflowBuild, runWorkflowProfile, runWorkflowPlatform
	t.Cleanup(func() {
		runTestBuild, runTestProfile, runTestPlatform, runBuildID = originalTestBuild, originalTestProfile, originalTestPlatform, originalBuildID
		runWorkflowBuild, runWorkflowProfile, runWorkflowPlatform = originalWorkflowBuild, originalWorkflowProfile, originalWorkflowPlatform
	})

	runTestBuild, runTestProfile = false, "development"
	if err := runTestExec(&cobra.Command{}, []string{"test"}); err == nil || !strings.Contains(err.Error(), "--profile requires --build") {
		t.Fatalf("test run error = %v", err)
	}

	runWorkflowBuild, runWorkflowProfile = false, "development"
	if err := runWorkflowExec(&cobra.Command{}, []string{"workflow"}); err == nil || !strings.Contains(err.Error(), "--profile requires --build") {
		t.Fatalf("workflow run error = %v", err)
	}

	runTestProfile, runTestPlatform = "", "ios"
	if err := runTestExec(&cobra.Command{}, []string{"test"}); err == nil || !strings.Contains(err.Error(), "--platform requires --build") {
		t.Fatalf("test run error = %v", err)
	}

	runWorkflowProfile, runWorkflowPlatform = "", "android"
	if err := runWorkflowExec(&cobra.Command{}, []string{"workflow"}); err == nil || !strings.Contains(err.Error(), "--platform requires --build") {
		t.Fatalf("workflow run error = %v", err)
	}

	runTestBuild, runTestPlatform, runBuildID = true, "android", "existing-build-id"
	if err := runTestExec(&cobra.Command{}, []string{"test"}); err == nil || !strings.Contains(err.Error(), "--build cannot be used with --build-id") {
		t.Fatalf("test run error = %v", err)
	}
}

func TestSaveBuildAppBindingUpdatesOnlySelectedRecipeWithCAS(t *testing.T) {
	repository := t.TempDir()
	gitInitBuildRepository(t, repository)
	writeProjectBuildConfig(t, repository, projectBuildConfigYAML("development", "android", "build/app.apk", false))
	invocation, err := resolveBuildInvocation(repository, "", "development", "android", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	appID := "00000000-0000-4000-8000-000000000099"
	if err := saveBuildAppBinding(invocation, appID); err != nil {
		t.Fatal(err)
	}
	after, err := config.ResolveProjectContext(repository, "")
	if err != nil {
		t.Fatal(err)
	}
	configuration, ok := platformConfiguration(*after.Aggregate, "development", "android")
	if !ok || configuration.AppID == nil || *configuration.AppID != appID {
		t.Fatalf("saved configuration = %+v", configuration)
	}
	if err := config.ReplaceConfigAtomically(invocation.ConfigPath, invocation.OriginalConfigBytes, invocation.OriginalConfigBytes); err == nil {
		t.Fatal("stale expected bytes unexpectedly overwrote saved canonical config")
	}
}

func TestRemoteBuildConfigFromProjectPreservesOrderedRecipe(t *testing.T) {
	output := "dist/App.app"
	image := "ios-macos"
	timeout := 900
	invocation := projectBuildInvocation{
		Platform: "ios",
		AppID:    "00000000-0000-4000-8000-000000000001",
		Recipe: config.EffectiveBuildRecipe{
			Framework:      "expo",
			SetupCommands:  []string{"bun install", "cd ios && pod install"},
			BuildCommands:  []string{"bun generate", "xcodebuild"},
			OutputPath:     &output,
			Image:          &image,
			TimeoutSeconds: &timeout,
			Env:            map[string]string{"MODE": "ci"},
			SecretRefs:     []string{"EXPO_TOKEN"},
			Caches:         []config.BuildCache{{Key: "pods", Paths: []string{"ios/Pods"}}},
		},
	}
	resolved := remoteBuildPlatformConfigFromProject(invocation)
	apiConfig := remoteBuildConfigFromResolved(uuid.MustParse(invocation.AppID), resolved)
	if apiConfig.Steps == nil {
		t.Fatal("steps are nil")
	}
	var commands []string
	for _, step := range *apiConfig.Steps {
		if step.Command != nil {
			commands = append(commands, *step.Command)
		}
	}
	if want := []string{"bun install", "cd ios && pod install", "bun generate", "xcodebuild"}; !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
	if apiConfig.SourceSubdir != nil || apiConfig.Id != nil || apiConfig.Name != nil {
		t.Fatalf("remote config leaked saved/source identity: %+v", apiConfig)
	}
	if apiConfig.Artifacts == nil || len(*apiConfig.Artifacts) != 1 || (*apiConfig.Artifacts)[0].Path != output {
		t.Fatalf("artifacts = %#v", apiConfig.Artifacts)
	}
}

func TestRemoteOverridesRecomputeHashAndTriggerProvenance(t *testing.T) {
	base := config.EffectiveBuildRecipe{
		Framework: "expo", BuildCommands: []string{"build"}, Env: map[string]string{"BASE": "one"},
		SecretRefs: []string{"BASE_SECRET"}, Caches: []config.BuildCache{{Key: "deps", Paths: []string{"node_modules"}}},
	}
	timeout := 1200
	effective, hash, err := applyRemoteBuildOverrides(
		base,
		map[string]string{"CLI": "two"},
		[]string{"CLI_SECRET"},
		"ios-new",
		&timeout,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" || effective.Env["CLI"] != "two" || !reflect.DeepEqual(effective.SecretRefs, []string{"BASE_SECRET", "CLI_SECRET"}) {
		t.Fatalf("effective recipe/hash = %+v, %q", effective, hash)
	}
	if len(effective.Caches) != 0 || effective.Image == nil || *effective.Image != "ios-new" || effective.TimeoutSeconds == nil || *effective.TimeoutSeconds != timeout {
		t.Fatalf("effective overrides = %+v", effective)
	}
	resolved := remoteBuildPlatformConfigFromProject(projectBuildInvocation{
		Platform: "ios", AppID: "00000000-0000-4000-8000-000000000001", Recipe: effective,
	})
	var source api.RemoteBuildRequest_Source
	if err := source.FromRemoteBuildArchiveSource(api.RemoteBuildArchiveSource{Key: "archive-key"}); err != nil {
		t.Fatal(err)
	}
	request := newRemoteBuildTriggerRequest(
		source,
		uuid.MustParse("00000000-0000-4000-8000-000000000001"),
		resolved,
		remoteBuildOptions{BuildDefinitionHash: hash},
	)
	if request.BuildDefinitionHash == nil || *request.BuildDefinitionHash != hash {
		t.Fatalf("build_definition_hash = %#v, want %q", request.BuildDefinitionHash, hash)
	}
	if request.Config.Id != nil || request.Config.Name != nil || request.Config.SourceSubdir != nil {
		t.Fatalf("request leaked saved/source identity: %+v", request.Config)
	}
}

func TestRemoteBuildWithoutOverridesPreservesResolvedDefinitionHash(t *testing.T) {
	repository := t.TempDir()
	gitInitBuildRepository(t, repository)
	writeProjectBuildConfig(t, repository, projectBuildConfigYAML("development", "android", "build/app.apk", true))

	invocation, err := resolveBuildInvocation(repository, "", "development", "android", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	effective, hash, err := applyRemoteBuildOverrides(invocation.Recipe, nil, nil, "", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if hash != invocation.BuildDefinitionHash {
		t.Fatalf("effective hash = %q, want resolved hash %q", hash, invocation.BuildDefinitionHash)
	}
	if !reflect.DeepEqual(effective, invocation.Recipe) {
		t.Fatalf("no-op overrides changed recipe: effective=%#v resolved=%#v", effective, invocation.Recipe)
	}
}

func TestNoCacheChangesEffectiveHashWhenRecipeCachesAreAlreadyEmpty(t *testing.T) {
	recipe := config.EffectiveBuildRecipe{
		BuildCommands: []string{"true"},
		Env:           map[string]string{"CUSTOM_ENV": "preserved"},
		Caches:        []config.BuildCache{},
	}
	ordinary, ordinaryHash, err := applyRemoteBuildOverrides(recipe, nil, nil, "", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	cold, coldHash, err := applyRemoteBuildOverrides(recipe, nil, nil, "", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if ordinaryHash == coldHash {
		t.Fatalf("ordinary and --no-cache hashes are equal: %s", ordinaryHash)
	}
	if ordinary.Env["CUSTOM_ENV"] != "preserved" || cold.Env["CUSTOM_ENV"] != "preserved" {
		t.Fatalf("custom env was not preserved: ordinary=%v cold=%v", ordinary.Env, cold.Env)
	}
	for _, name := range remoteCompilationCacheEnvironmentNames {
		if _, ok := ordinary.Env[name]; ok {
			t.Fatalf("ordinary recipe unexpectedly set %s", name)
		}
		if cold.Env[name] != "0" {
			t.Fatalf("cold recipe %s = %q, want 0", name, cold.Env[name])
		}
	}
}

func TestPreexistingArtifactDoesNotReceiveSourceDerivedExpoMetadata(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "app.json"), []byte(`{"expo":{"scheme":"current-source"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	metadata, err := localArtifactMetadata(context.Background(), projectBuildInvocation{
		ProjectRoot: projectRoot,
		Platform:    "ios",
		Recipe:      config.EffectiveBuildRecipe{Framework: "expo"},
	}, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := metadata[artifactBuildMetadataKey]; exists {
		t.Fatalf("metadata = %#v, pre-existing artifact must not inherit current-source Expo metadata", metadata)
	}
}

func TestProjectDevSkipsPreexistingSimulatorArtifactForExpo(t *testing.T) {
	if canUseDevPreexistingSimulatorArtifact(projectDevInvocation{
		Recipe: config.EffectiveBuildRecipe{Framework: "expo"},
	}) {
		t.Fatal("Expo dev must run the selected recipe before attaching artifact metadata")
	}
	if !canUseDevPreexistingSimulatorArtifact(projectDevInvocation{
		Recipe: config.EffectiveBuildRecipe{Framework: "ios"},
	}) {
		t.Fatal("native iOS dev should retain the pre-existing simulator optimization")
	}
}

type buildSelectionTestError struct{}

func (*buildSelectionTestError) Error() string { return "selection stopped for test" }

func gitInitBuildRepository(t *testing.T, root string) {
	t.Helper()
	command := exec.Command("git", "init", "-q")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
}

func writeProjectBuildConfig(t *testing.T, projectRoot, contents string) {
	t.Helper()
	directory := filepath.Join(projectRoot, ".revyl")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "config.yaml"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func projectBuildConfigYAML(profile, platform, output string, appID bool) string {
	app := ""
	if appID {
		app = "\n        app_id: 00000000-0000-4000-8000-000000000001"
	}
	return `project:
  id: ` + buildTestProjectID + `
build:
  framework: expo
  profiles:
    ` + profile + `:
      ` + platform + `:
        build_commands: ["true"]
        output_path: ` + output + app + "\n"
}

func intPointerForBuildTest(value int) *int { return &value }
