package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/spf13/cobra"
)

func gitInitForInitTest(t *testing.T, root string) {
	t.Helper()
	if output, err := exec.Command("git", "-C", root, "init", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
}

func TestInitPublicFlagsExcludeRetiredLegacyOptions(t *testing.T) {
	for _, name := range []string{"provider", "hotreload-app-scheme"} {
		if initCmd.Flags().Lookup(name) != nil {
			t.Fatalf("init flag --%s is still public", name)
		}
	}
	for _, name := range []string{"project", "detect", "force", "non-interactive", "xcode-scheme"} {
		if initCmd.Flags().Lookup(name) == nil {
			t.Fatalf("init flag --%s is missing", name)
		}
	}
}

func TestRunInitWritesStrictCanonicalContractWithGeneratedProjectID(t *testing.T) {
	resetInitGlobals(t)
	initNonInteractive = true
	workDir := t.TempDir()
	gitInitForInitTest(t, workDir)
	withWorkingDir(t, workDir)

	cmd := &cobra.Command{Use: "init"}
	cmd.Flags().Bool("dev", false, "")
	if err := runInit(cmd, nil); err != nil {
		t.Fatalf("runInit() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(workDir, ".revyl", "config.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(config.yaml) error = %v", err)
	}
	authored, err := config.ParseAuthoredConfig(data)
	if err != nil {
		t.Fatalf("ParseAuthoredConfig() error = %v\n%s", err, data)
	}
	if _, err := uuid.Parse(authored.Project.ID); err != nil {
		t.Fatalf("project.id = %q, want UUID", authored.Project.ID)
	}
	for _, retired := range []string{"name:", "org_id:", "defaults:", "hotreload:", "source:", "no_build:", "last_synced_at:"} {
		if strings.Contains(string(data), retired) {
			t.Fatalf("canonical config contains retired field %q:\n%s", retired, data)
		}
	}
}

func TestAuthoredConfigFromInitDraftMapsCanonicalBuildDirectly(t *testing.T) {
	projectID := uuid.NewString()
	draft := &initConfigDraft{
		Project:            initProjectDraft{ID: projectID, Name: "local display name", OrgID: "legacy-org-binding"},
		IdleTimeoutSeconds: 42,
		Build: initBuildDraft{
			DetectedSystem: build.SystemExpo,
			Framework:      "expo",
			Recipes: map[string]initBuildRecipeDraft{
				"ios": {
					BuildCommands: []string{"npx eas build --platform ios"},
					OutputPath:    "build/app.ipa",
					AppID:         "22222222-2222-4222-8222-222222222222",
				},
				"android": {
					BuildCommands: []string{"npx eas build --platform android"},
					OutputPath:    "build/app.apk",
				},
			},
		},
	}

	authored, err := draft.canonicalAuthoredConfig()
	if err != nil {
		t.Fatalf("canonicalAuthoredConfig() error = %v", err)
	}
	if authored.Project.ID != projectID {
		t.Fatalf("project.id = %q, want %q", authored.Project.ID, projectID)
	}
	if authored.Session == nil || authored.Session.IdleTimeoutSeconds == nil || *authored.Session.IdleTimeoutSeconds != 42 {
		t.Fatalf("session = %+v, want idle timeout 42", authored.Session)
	}
	if authored.Build == nil || authored.Build.Framework != "expo" {
		t.Fatalf("build = %+v, want expo framework", authored.Build)
	}
	profile, ok := authored.Build.Profiles["development"]
	if !ok || profile.IOS == nil || profile.Android == nil {
		t.Fatalf("development profile = %+v, want iOS and Android recipes", profile)
	}
	if profile.IOS.BuildCommands == nil || len(*profile.IOS.BuildCommands) != 1 || (*profile.IOS.BuildCommands)[0] != "npx eas build --platform ios" {
		t.Fatalf("iOS build commands = %+v", profile.IOS.BuildCommands)
	}
	if profile.IOS.OutputPath == nil || *profile.IOS.OutputPath != "build/app.ipa" || profile.IOS.AppID == nil || *profile.IOS.AppID != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("iOS recipe = %+v", profile.IOS)
	}

	data, err := config.MarshalCanonicalConfig(*authored)
	if err != nil {
		t.Fatalf("MarshalCanonicalConfig() error = %v", err)
	}
	for _, retired := range []string{"name:", "org_id:", "hotreload:", "platforms:"} {
		if strings.Contains(string(data), retired) {
			t.Fatalf("canonical config contains retired init field %q:\n%s", retired, data)
		}
	}
}

func TestRunInitRejectsInvalidExplicitProjectID(t *testing.T) {
	resetInitGlobals(t)
	initNonInteractive = true
	initProjectID = "not-a-uuid"
	workDir := t.TempDir()
	gitInitForInitTest(t, workDir)
	withWorkingDir(t, workDir)

	cmd := &cobra.Command{Use: "init"}
	cmd.Flags().Bool("dev", false, "")
	err := runInit(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "--project must be a UUID") {
		t.Fatalf("runInit() error = %v, want invalid --project error", err)
	}
	if _, err := os.Stat(filepath.Join(workDir, ".revyl", "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("config.yaml was created after invalid --project: %v", err)
	}
}

func TestAuthoredConfigFromInitDraftBuildsCanonicalProfilesDirectly(t *testing.T) {
	draft := &initConfigDraft{
		Project: initProjectDraft{ID: uuid.NewString()},
		Build: initBuildDraft{
			DetectedSystem: build.SystemExpo,
			Framework:      "expo",
			Caches:         []config.BuildCache{{Key: "shared", Paths: []string{"node_modules"}}},
			Recipes: map[string]initBuildRecipeDraft{
				"ios-dev": {
					BuildCommands: []string{"npm ci", "npx eas build --platform ios"},
					OutputPath:    "dist/app.app",
					Env:           map[string]string{"MODE": "dev"},
					Caches:        []config.BuildCache{{Key: "ios", Paths: []string{"ios/Pods"}}},
				},
			},
		},
	}

	authored, err := draft.canonicalAuthoredConfig()
	if err != nil {
		t.Fatalf("canonicalAuthoredConfig() error = %v", err)
	}
	if authored.Build == nil || authored.Build.Framework != "expo" {
		t.Fatalf("build = %+v, want expo", authored.Build)
	}
	if len(authored.Build.Caches) != 1 || authored.Build.Caches[0].Key != "shared" {
		t.Fatalf("inherited caches = %+v", authored.Build.Caches)
	}
	recipe := authored.Build.Profiles["dev"].IOS
	if recipe == nil || recipe.BuildCommands == nil || len(*recipe.BuildCommands) != 2 {
		t.Fatalf("iOS recipe = %+v", recipe)
	}
	if len(recipe.Caches) != 1 || recipe.Caches[0].Key != "ios" {
		t.Fatalf("recipe caches = %+v, want only recipe-local cache", recipe.Caches)
	}
	if _, err := config.NormalizeAuthoredConfig(*authored, config.CompilationContext{
		RepositoryRelativeProjectRoot: ".",
		ExecutionDirectory:            ".",
	}); err != nil {
		t.Fatalf("NormalizeAuthoredConfig() error = %v", err)
	}
}

func TestWizardProjectSetupAuthorsStandaloneSwiftDetectionAsCanonicalIOSRecipe(t *testing.T) {
	resetInitGlobals(t)
	initProjectID = uuid.NewString()
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "Package.swift"), []byte("// swift-tools-version: 6.0\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(Package.swift) error = %v", err)
	}
	revylDir := filepath.Join(workDir, ".revyl")
	configPath := filepath.Join(revylDir, "config.yaml")
	overrideOpts, err := newInitOverrideOptions(nil, "", false)
	if err != nil {
		t.Fatalf("newInitOverrideOptions() error = %v", err)
	}

	draft, err := wizardProjectSetup(workDir, revylDir, configPath, overrideOpts)
	if err != nil {
		t.Fatalf("wizardProjectSetup() error = %v", err)
	}
	iosDraft, ok := draft.Build.Recipes["ios"]
	if !ok || iosDraft.Profile != "development" || iosDraft.Platform != "ios" || iosDraft.primaryBuildCommand() != "swift build" || iosDraft.OutputPath != ".build/debug/*" {
		t.Fatalf("iOS draft = %+v, want standalone Swift canonical recipe", iosDraft)
	}
	written, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config.yaml) error = %v", err)
	}
	authored, err := config.ParseAuthoredConfig(written)
	if err != nil {
		t.Fatalf("ParseAuthoredConfig() error = %v", err)
	}
	ios := authored.Build.Profiles["development"].IOS
	if authored.Build.Framework != "ios" || ios == nil || ios.BuildCommands == nil || !reflect.DeepEqual(*ios.BuildCommands, []string{"swift build"}) || ios.OutputPath == nil || *ios.OutputPath != ".build/debug/*" {
		t.Fatalf("canonical Swift build = %+v", authored.Build)
	}
}

func TestRunInitRejectsNonGitDirectoryBeforeWritingAnything(t *testing.T) {
	resetInitGlobals(t)
	initNonInteractive = true
	workDir := t.TempDir()
	withWorkingDir(t, workDir)

	cmd := &cobra.Command{Use: "init"}
	cmd.Flags().Bool("dev", false, "")
	err := runInit(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "requires an active Git worktree") {
		t.Fatalf("runInit() error = %v, want Git worktree guidance", err)
	}
	if _, statErr := os.Stat(filepath.Join(workDir, ".revyl")); !os.IsNotExist(statErr) {
		t.Fatalf(".revyl was created outside Git: %v", statErr)
	}
}

func TestRunInitRejectsRecursivelyNestedProjectsWithExactRecoveryCommands(t *testing.T) {
	resetInitGlobals(t)
	initNonInteractive = true
	workDir := t.TempDir()
	gitInitForInitTest(t, workDir)
	withWorkingDir(t, workDir)

	projectRoots := make([]string, 0, 2)
	for _, relativeRoot := range []string{filepath.Join("apps", "android"), filepath.Join("apps", "ios")} {
		projectRoot := filepath.Join(workDir, relativeRoot)
		projectRoots = append(projectRoots, projectRoot)
		if err := os.MkdirAll(filepath.Join(projectRoot, ".revyl"), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", projectRoot, err)
		}
		contents, err := config.MarshalCanonicalConfig(config.AuthoredConfig{
			Project: config.AuthoredProject{ID: uuid.NewString()},
		})
		if err != nil {
			t.Fatalf("MarshalCanonicalConfig(): %v", err)
		}
		if err := os.WriteFile(filepath.Join(projectRoot, ".revyl", "config.yaml"), contents, 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", projectRoot, err)
		}
	}

	cmd := &cobra.Command{Use: "init"}
	cmd.Flags().Bool("dev", false, "")
	var runErr error
	output := captureStdoutAndStderr(t, func() {
		runErr = runInit(cmd, nil)
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "nested Revyl project found") {
		t.Fatalf("runInit() error = %v, want nested-project rejection", runErr)
	}
	if _, err := os.Stat(filepath.Join(workDir, ".revyl", "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("root config was created despite nested projects: %v", err)
	}

	recoveryCommands := make([]string, 0, len(projectRoots))
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "revyl -C ") && strings.HasSuffix(line, " config path") {
			recoveryCommands = append(recoveryCommands, line)
		}
	}
	if len(recoveryCommands) != len(projectRoots) {
		t.Fatalf("init output recovery commands = %v, want one per nested project:\n%s", recoveryCommands, output)
	}
	for index, projectRoot := range projectRoots {
		requireCLIRecoveryCommand(t, recoveryCommands[index], projectRoot, "config", "path")
	}
}

func TestRunInitForceAllowsRootProjectAlongsideRecursivelyNestedProject(t *testing.T) {
	resetInitGlobals(t)
	initForce = true
	initNonInteractive = true
	workDir := t.TempDir()
	gitInitForInitTest(t, workDir)
	withWorkingDir(t, workDir)

	nestedConfigDir := filepath.Join(workDir, "apps", "ios", ".revyl")
	if err := os.MkdirAll(nestedConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(nested config): %v", err)
	}
	contents, err := config.MarshalCanonicalConfig(config.AuthoredConfig{
		Project: config.AuthoredProject{ID: uuid.NewString()},
	})
	if err != nil {
		t.Fatalf("MarshalCanonicalConfig(): %v", err)
	}
	if err := os.WriteFile(filepath.Join(nestedConfigDir, "config.yaml"), contents, 0o644); err != nil {
		t.Fatalf("WriteFile(nested config): %v", err)
	}

	cmd := &cobra.Command{Use: "init"}
	cmd.Flags().Bool("dev", false, "")
	if err := runInit(cmd, nil); err != nil {
		t.Fatalf("runInit() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(workDir, ".revyl", "config.yaml")); err != nil {
		t.Fatalf("forced root config was not created: %v", err)
	}
}

func TestRunInitDirectsLegacyConfigToReplacement(t *testing.T) {
	resetInitGlobals(t)
	workDir := t.TempDir()
	gitInitForInitTest(t, workDir)
	withWorkingDir(t, workDir)
	configDir := filepath.Join(workDir, ".revyl")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(.revyl) error = %v", err)
	}
	legacy := []byte("project:\n  id: " + uuid.NewString() + "\n  name: example\nbuild:\n  system: Expo\n")
	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, legacy, 0o644); err != nil {
		t.Fatalf("WriteFile(config.yaml) error = %v", err)
	}

	cmd := &cobra.Command{Use: "init"}
	cmd.Flags().Bool("dev", false, "")
	err := runInit(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "revyl config migrate") {
		t.Fatalf("runInit() error = %v, want migrate guidance", err)
	}
	after, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("ReadFile(config.yaml) error = %v", readErr)
	}
	if string(after) != string(legacy) {
		t.Fatalf("legacy config changed:\n%s", after)
	}
}

func TestRunInitCanonicalConfigIsNoOpWithoutForceOrDetect(t *testing.T) {
	resetInitGlobals(t)
	workDir := t.TempDir()
	gitInitForInitTest(t, workDir)
	withWorkingDir(t, workDir)
	configDir := filepath.Join(workDir, ".revyl")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(.revyl) error = %v", err)
	}
	original, err := config.MarshalCanonicalConfig(config.AuthoredConfig{
		Project: config.AuthoredProject{ID: uuid.NewString()},
	})
	if err != nil {
		t.Fatalf("MarshalCanonicalConfig() error = %v", err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, original, 0o644); err != nil {
		t.Fatalf("WriteFile(config.yaml) error = %v", err)
	}

	cmd := &cobra.Command{Use: "init"}
	cmd.Flags().Bool("dev", false, "")
	if err := runInit(cmd, nil); err != nil {
		t.Fatalf("runInit() error = %v", err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config.yaml) error = %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("canonical config changed without --force/--detect:\n%s", after)
	}
}

func TestRunInitDetectRefreshesDetectedFieldsAndPreservesUnrelatedCanonicalState(t *testing.T) {
	resetInitGlobals(t)
	initDetect = true
	initNonInteractive = true
	workDir := t.TempDir()
	gitInitForInitTest(t, workDir)
	withWorkingDir(t, workDir)
	configDir := filepath.Join(workDir, ".revyl")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(.revyl) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "package.json"), []byte(`{"dependencies":{"expo":"latest"}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(package.json) error = %v", err)
	}

	idleTimeout := 55
	beforeScriptPath := "scripts/before-session.sh"
	beforeScriptTimeout := 91
	deepLink := "example://auth"
	projectID := uuid.NewString()
	iosAppID := uuid.NewString()
	ciIOSAppID := uuid.NewString()
	oldIOSCommands := []string{"old-ios-build"}
	releaseCommands := []string{"release-build"}
	oldOutput := "old/app.ipa"
	image := "macos-15"
	buildTimeout := 1200
	prEnabled := true
	existing := config.AuthoredConfig{
		Project: config.AuthoredProject{ID: projectID},
		Session: &config.AuthoredSession{
			IdleTimeoutSeconds: &idleTimeout,
			BeforeScript: &config.AuthoredBeforeScript{
				ScriptPath:     &beforeScriptPath,
				TimeoutSeconds: &beforeScriptTimeout,
			},
			AuthBypass: &config.AuthoredAuthBypass{
				LaunchVars: []string{"AUTH_TOKEN"},
				DeepLink:   &deepLink,
			},
		},
		Build: &config.AuthoredBuild{
			Framework: "expo",
			Env:       map[string]string{"NODE_ENV": "test"},
			Secrets:   []string{"EAS_TOKEN"},
			Caches:    []config.BuildCache{{Key: "shared", Paths: []string{"node_modules"}}},
			Profiles: map[string]config.AuthoredBuildProfile{
				"development": {
					IOS: &config.AuthoredBuildRecipe{
						AppID:          &iosAppID,
						SetupCommands:  []string{"npm ci"},
						BuildCommands:  &oldIOSCommands,
						OutputPath:     &oldOutput,
						Image:          &image,
						TimeoutSeconds: &buildTimeout,
						Env:            map[string]string{"PLATFORM_ENV": "kept"},
						Secrets:        []string{"MATCH_PASSWORD"},
						Caches:         []config.BuildCache{{Key: "ios", Paths: []string{"ios/Pods"}}},
					},
				},
				"release": {
					IOS: &config.AuthoredBuildRecipe{BuildCommands: &releaseCommands},
				},
			},
		},
		PRReview: &config.AuthoredPRReview{
			Enabled: &prEnabled,
			Build: config.AuthoredReviewBuild{
				Kind:   "ci_upload_to_revyl",
				AppIDs: &config.AuthoredExternalCIAppIDs{IOS: &ciIOSAppID},
			},
		},
	}
	original, err := config.MarshalCanonicalConfig(existing)
	if err != nil {
		t.Fatalf("MarshalCanonicalConfig() error = %v", err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, original, 0o640); err != nil {
		t.Fatalf("WriteFile(config.yaml) error = %v", err)
	}

	cmd := &cobra.Command{Use: "init"}
	cmd.Flags().Bool("dev", false, "")
	if err := runInit(cmd, nil); err != nil {
		t.Fatalf("runInit() error = %v", err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config.yaml) error = %v", err)
	}
	authored, err := config.ParseAuthoredConfig(after)
	if err != nil {
		t.Fatalf("ParseAuthoredConfig() error = %v", err)
	}
	if authored.Project.ID != projectID {
		t.Fatalf("project.id = %q, want %q", authored.Project.ID, projectID)
	}
	if !reflect.DeepEqual(authored.Session, existing.Session) {
		t.Fatalf("session changed:\n got: %+v\nwant: %+v", authored.Session, existing.Session)
	}
	if !reflect.DeepEqual(authored.PRReview, existing.PRReview) {
		t.Fatalf("pr_review changed:\n got: %+v\nwant: %+v", authored.PRReview, existing.PRReview)
	}
	if authored.Build == nil {
		t.Fatal("build was removed")
	}
	if !reflect.DeepEqual(authored.Build.Env, existing.Build.Env) || !reflect.DeepEqual(authored.Build.Secrets, existing.Build.Secrets) || !reflect.DeepEqual(authored.Build.Caches, existing.Build.Caches) {
		t.Fatalf("build defaults changed:\n got: %+v\nwant: %+v", authored.Build, existing.Build)
	}
	if !reflect.DeepEqual(authored.Build.Profiles["release"], existing.Build.Profiles["release"]) {
		t.Fatalf("unrelated release profile changed:\n got: %+v\nwant: %+v", authored.Build.Profiles["release"], existing.Build.Profiles["release"])
	}
	ios := authored.Build.Profiles["development"].IOS
	if ios == nil || ios.BuildCommands == nil || len(*ios.BuildCommands) != 1 || (*ios.BuildCommands)[0] == "old-ios-build" {
		t.Fatalf("detected iOS build command was not refreshed: %+v", ios)
	}
	if ios.OutputPath == nil || *ios.OutputPath != "build/app.tar.gz" {
		t.Fatalf("detected iOS output path = %+v, want build/app.tar.gz", ios.OutputPath)
	}
	if ios.AppID == nil || *ios.AppID != iosAppID || !reflect.DeepEqual(ios.SetupCommands, []string{"npm ci"}) || ios.Image == nil || *ios.Image != image || ios.TimeoutSeconds == nil || *ios.TimeoutSeconds != buildTimeout || !reflect.DeepEqual(ios.Env, map[string]string{"PLATFORM_ENV": "kept"}) || !reflect.DeepEqual(ios.Secrets, []string{"MATCH_PASSWORD"}) || !reflect.DeepEqual(ios.Caches, []config.BuildCache{{Key: "ios", Paths: []string{"ios/Pods"}}}) {
		t.Fatalf("unrelated iOS recipe state changed: %+v", ios)
	}
	if authored.Build.Profiles["development"].Android == nil {
		t.Fatal("newly detected Android recipe was not merged")
	}
	backups, err := filepath.Glob(configPath + ".bak.*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backup paths = %v, error = %v", backups, err)
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatalf("ReadFile(backup) error = %v", err)
	}
	if string(backup) != string(original) {
		t.Fatalf("backup differs from original:\n%s", backup)
	}
}

func TestRunInitDetectIncompletePlaceholderPreservesCollidingBuildCommandsAndExactBackup(t *testing.T) {
	resetInitGlobals(t)
	initDetect = true
	initNonInteractive = true
	workDir := t.TempDir()
	gitInitForInitTest(t, workDir)
	withWorkingDir(t, workDir)
	configDir := filepath.Join(workDir, ".revyl")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(.revyl) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "package.json"), []byte(`{"dependencies":{"react-native":"latest"}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(package.json) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workDir, "ios"), 0o755); err != nil {
		t.Fatalf("MkdirAll(ios) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workDir, "android", "app"), 0o755); err != nil {
		t.Fatalf("MkdirAll(android/app) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "android", "app", "build.gradle"), []byte("plugins {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(android/app/build.gradle) error = %v", err)
	}

	projectID := uuid.NewString()
	iosCommands := []string{
		"ruby scripts/bootstrap-ios.rb",
		"xcodebuild -workspace Existing.xcworkspace -scheme Existing",
	}
	androidCommands := []string{"old-android-build"}
	iosOutput := "existing/Revyl.app"
	androidOutput := "existing/app.apk"
	existing := config.AuthoredConfig{
		Project: config.AuthoredProject{ID: projectID},
		Build: &config.AuthoredBuild{
			Framework: "react_native",
			Profiles: map[string]config.AuthoredBuildProfile{
				"development": {
					IOS: &config.AuthoredBuildRecipe{
						BuildCommands: &iosCommands,
						OutputPath:    &iosOutput,
					},
					Android: &config.AuthoredBuildRecipe{
						BuildCommands: &androidCommands,
						OutputPath:    &androidOutput,
					},
				},
			},
		},
	}
	original, err := config.MarshalCanonicalConfig(existing)
	if err != nil {
		t.Fatalf("MarshalCanonicalConfig() error = %v", err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, original, 0o640); err != nil {
		t.Fatalf("WriteFile(config.yaml) error = %v", err)
	}

	cmd := &cobra.Command{Use: "init"}
	cmd.Flags().Bool("dev", false, "")
	if err := runInit(cmd, nil); err != nil {
		t.Fatalf("runInit() error = %v", err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config.yaml) error = %v", err)
	}
	authored, err := config.ParseAuthoredConfig(after)
	if err != nil {
		t.Fatalf("ParseAuthoredConfig() error = %v", err)
	}
	development := authored.Build.Profiles["development"]
	if development.IOS == nil || development.IOS.BuildCommands == nil || !reflect.DeepEqual(*development.IOS.BuildCommands, iosCommands) {
		t.Fatalf("incomplete iOS detection changed build commands: %+v", development.IOS)
	}
	wantAndroidCommands := []string{"cd android && ./gradlew assembleDebug"}
	if development.Android == nil || development.Android.BuildCommands == nil || !reflect.DeepEqual(*development.Android.BuildCommands, wantAndroidCommands) {
		t.Fatalf("complete Android detection did not replace build commands: %+v", development.Android)
	}
	backups, err := filepath.Glob(configPath + ".bak.*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backup paths = %v, error = %v", backups, err)
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatalf("ReadFile(backup) error = %v", err)
	}
	if !reflect.DeepEqual(backup, original) {
		t.Fatalf("backup differs byte-for-byte from original:\n%s", backup)
	}
}

func TestRunInitIncompletePlaceholderWritesEmptyBuildCommandsForNewProject(t *testing.T) {
	resetInitGlobals(t)
	initNonInteractive = true
	workDir := t.TempDir()
	gitInitForInitTest(t, workDir)
	withWorkingDir(t, workDir)
	if err := os.WriteFile(filepath.Join(workDir, "package.json"), []byte(`{"dependencies":{"react-native":"latest"}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(package.json) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workDir, "ios"), 0o755); err != nil {
		t.Fatalf("MkdirAll(ios) error = %v", err)
	}

	cmd := &cobra.Command{Use: "init"}
	cmd.Flags().Bool("dev", false, "")
	if err := runInit(cmd, nil); err != nil {
		t.Fatalf("runInit() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(workDir, ".revyl", "config.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(config.yaml) error = %v", err)
	}
	authored, err := config.ParseAuthoredConfig(data)
	if err != nil {
		t.Fatalf("ParseAuthoredConfig() error = %v", err)
	}
	ios := authored.Build.Profiles["development"].IOS
	if ios == nil || ios.BuildCommands == nil || len(*ios.BuildCommands) != 0 {
		t.Fatalf("new incomplete iOS recipe build commands = %+v, want []", ios)
	}
	if !strings.Contains(string(data), "build_commands: []") {
		t.Fatalf("new incomplete iOS recipe did not emit build_commands: []:\n%s", data)
	}
}

func TestBuildReviewPreservesIncompleteDetectorCommandsUntilExplicitSkip(t *testing.T) {
	existingCommands := []string{"existing-build"}
	existing := &config.AuthoredConfig{
		Project: config.AuthoredProject{ID: uuid.NewString()},
		Build: &config.AuthoredBuild{
			Framework: "react_native",
			Profiles: map[string]config.AuthoredBuildProfile{
				"development": {IOS: &config.AuthoredBuildRecipe{BuildCommands: &existingCommands}},
			},
		},
	}

	for _, testCase := range []struct {
		name         string
		edit         func(*initConfigDraft)
		wantCommands []string
	}{
		{
			name: "artifact-only prompt edit",
			edit: func(draft *initConfigDraft) {
				promptBuildSetupReviewWithPrompt(draft, func(label, current string) string {
					if label == initArtifactPathLabel {
						return "new/Revyl.app"
					}
					return current
				})
			},
			wantCommands: existingCommands,
		},
		{
			name: "explicit skip",
			edit: func(draft *initConfigDraft) {
				skipBuildSetupForNow(draft)
			},
			wantCommands: []string{},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			draft := &initConfigDraft{
				Project: initProjectDraft{ID: existing.Project.ID},
				Build: initBuildDraft{
					Framework: "react_native",
					Recipes: map[string]initBuildRecipeDraft{
						"ios": {
							Profile:                       "development",
							Platform:                      "ios",
							BuildCommands:                 []string{},
							IncompleteDetectorPlaceholder: true,
						},
					},
				},
			}
			testCase.edit(draft)
			authored, err := draft.canonicalAuthoredConfig()
			if err != nil {
				t.Fatalf("canonicalAuthoredConfig() error = %v", err)
			}
			if err := mergeExistingCanonicalInitConfig(authored, existing, draft); err != nil {
				t.Fatalf("mergeExistingCanonicalInitConfig() error = %v", err)
			}
			commands := authored.Build.Profiles["development"].IOS.BuildCommands
			if commands == nil || len(*commands) != len(testCase.wantCommands) || (len(testCase.wantCommands) > 0 && !reflect.DeepEqual(*commands, testCase.wantCommands)) {
				t.Fatalf("build commands = %v, want %v", commands, testCase.wantCommands)
			}
		})
	}
}

func TestPreserveManagedReviewProfileKeepsMissingReferencedProfile(t *testing.T) {
	buildCommands := []string{"xcodebuild archive"}
	profileName := "pull-request"
	authored := &config.AuthoredConfig{
		Project: config.AuthoredProject{ID: uuid.NewString()},
		Build: &config.AuthoredBuild{
			Framework: "ios",
			Profiles: map[string]config.AuthoredBuildProfile{
				"development": {IOS: &config.AuthoredBuildRecipe{BuildCommands: &buildCommands}},
			},
		},
	}
	existing := &config.AuthoredConfig{
		Build: &config.AuthoredBuild{
			Framework: "ios",
			Profiles: map[string]config.AuthoredBuildProfile{
				profileName: {IOS: &config.AuthoredBuildRecipe{BuildCommands: &buildCommands}},
			},
		},
		PRReview: &config.AuthoredPRReview{
			Build: config.AuthoredReviewBuild{Kind: "revyl", Profile: &profileName},
		},
	}

	if err := preserveManagedReviewProfile(authored, existing); err != nil {
		t.Fatalf("preserveManagedReviewProfile() error = %v", err)
	}
	authored.PRReview = existing.PRReview
	if _, ok := authored.Build.Profiles[profileName]; !ok {
		t.Fatalf("build profile %q was not preserved", profileName)
	}
	if _, err := config.MarshalCanonicalConfig(*authored); err != nil {
		t.Fatalf("MarshalCanonicalConfig() error = %v", err)
	}
}

func TestPreserveManagedReviewProfileRejectsFrameworkChange(t *testing.T) {
	buildCommands := []string{"xcodebuild archive"}
	profileName := "pull-request"
	authored := &config.AuthoredConfig{
		Build: &config.AuthoredBuild{
			Framework: "expo",
			Profiles: map[string]config.AuthoredBuildProfile{
				"development": {IOS: &config.AuthoredBuildRecipe{BuildCommands: &buildCommands}},
			},
		},
	}
	existing := &config.AuthoredConfig{
		Build: &config.AuthoredBuild{
			Framework: "ios",
			Profiles: map[string]config.AuthoredBuildProfile{
				profileName: {IOS: &config.AuthoredBuildRecipe{BuildCommands: &buildCommands}},
			},
		},
		PRReview: &config.AuthoredPRReview{
			Build: config.AuthoredReviewBuild{Kind: "revyl", Profile: &profileName},
		},
	}

	err := preserveManagedReviewProfile(authored, existing)
	if err == nil || !strings.Contains(err.Error(), "build framework changed") {
		t.Fatalf("preserveManagedReviewProfile() error = %v, want framework-change guidance", err)
	}
}
