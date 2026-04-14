package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/config"
	"github.com/spf13/cobra"
)

func withStdin(t *testing.T, input string, fn func()) {
	t.Helper()

	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(stdin): %v", err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("WriteString(stdin): %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close(stdin writer): %v", err)
	}
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = old
		_ = r.Close()
	})

	fn()
}

func resetInitGlobals(t *testing.T) {
	t.Helper()

	originalProjectID := initProjectID
	originalDetect := initDetect
	originalForce := initForce
	originalNonInteractive := initNonInteractive
	originalHotReloadAppScheme := initHotReloadAppScheme
	originalHotReloadProvider := initHotReloadProvider
	originalXcodeSchemeOverrides := initXcodeSchemeOverrides

	t.Cleanup(func() {
		initProjectID = originalProjectID
		initDetect = originalDetect
		initForce = originalForce
		initNonInteractive = originalNonInteractive
		initHotReloadAppScheme = originalHotReloadAppScheme
		initHotReloadProvider = originalHotReloadProvider
		initXcodeSchemeOverrides = originalXcodeSchemeOverrides
	})

	initProjectID = ""
	initDetect = false
	initForce = false
	initNonInteractive = false
	initHotReloadAppScheme = ""
	initHotReloadProvider = ""
	initXcodeSchemeOverrides = nil
}

func resetBuildUploadGlobals(t *testing.T) {
	t.Helper()

	originalBuildSkip := buildSkip
	originalBuildVersion := buildVersion
	originalBuildSetCurr := buildSetCurr
	originalUploadPlatformFlag := uploadPlatformFlag
	originalUploadAppFlag := uploadAppFlag
	originalUploadNameFlag := uploadNameFlag
	originalUploadFileFlag := uploadFileFlag
	originalUploadURLFlag := uploadURLFlag
	originalUploadHeaderFlags := uploadHeaderFlags
	originalBuildUploadJSON := buildUploadJSON
	originalBuildDryRun := buildDryRun
	originalUploadYesFlag := uploadYesFlag
	originalUploadSchemeFlag := uploadSchemeFlag

	t.Cleanup(func() {
		buildSkip = originalBuildSkip
		buildVersion = originalBuildVersion
		buildSetCurr = originalBuildSetCurr
		uploadPlatformFlag = originalUploadPlatformFlag
		uploadAppFlag = originalUploadAppFlag
		uploadNameFlag = originalUploadNameFlag
		uploadFileFlag = originalUploadFileFlag
		uploadURLFlag = originalUploadURLFlag
		uploadHeaderFlags = originalUploadHeaderFlags
		buildUploadJSON = originalBuildUploadJSON
		buildDryRun = originalBuildDryRun
		uploadYesFlag = originalUploadYesFlag
		uploadSchemeFlag = originalUploadSchemeFlag
	})

	buildSkip = false
	buildVersion = ""
	buildSetCurr = false
	uploadPlatformFlag = ""
	uploadAppFlag = ""
	uploadNameFlag = ""
	uploadFileFlag = ""
	uploadURLFlag = ""
	uploadHeaderFlags = nil
	buildUploadJSON = false
	buildDryRun = false
	uploadYesFlag = false
	uploadSchemeFlag = ""
}

func writeBuildUploadProjectConfig(t *testing.T, dir string, cfg *config.ProjectConfig) string {
	t.Helper()

	configDir := filepath.Join(dir, ".revyl")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(.revyl): %v", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	if err := config.WriteProjectConfig(configPath, cfg); err != nil {
		t.Fatalf("WriteProjectConfig(): %v", err)
	}
	return configPath
}

func TestWizardProjectSetupPrintsInferredBuildSettingsForAndroidFixture(t *testing.T) {
	overrideOpts, err := newInitOverrideOptions(nil, "", false)
	if err != nil {
		t.Fatalf("newInitOverrideOptions() error = %v", err)
	}

	workDir := copyInternalAppFixture(t, filepath.Join(repoRootForInitFixtureTests(t), "internal-apps", "android-minimal"), true)
	revylDir := filepath.Join(workDir, ".revyl")
	configPath := filepath.Join(revylDir, "config.yaml")

	output := captureStdoutAndStderr(t, func() {
		if _, err := wizardProjectSetup(workDir, revylDir, configPath, overrideOpts); err != nil {
			t.Fatalf("wizardProjectSetup() error = %v", err)
		}
	})

	if !strings.Contains(output, "Inferred build settings from your project:") {
		t.Fatalf("expected inferred build settings copy, got:\n%s", output)
	}
	if !strings.Contains(output, "android build command:") {
		t.Fatalf("expected build command label, got:\n%s", output)
	}
	if !strings.Contains(output, "android artifact path:") {
		t.Fatalf("expected artifact path label, got:\n%s", output)
	}
}

func TestRunInitSkipBuildSetupForNowCreatesPlaceholderPlatforms(t *testing.T) {
	resetInitGlobals(t)

	workDir := copyInternalAppFixture(t, filepath.Join(repoRootForInitFixtureTests(t), "internal-apps", "android-minimal"), true)
	withWorkingDir(t, workDir)

	cmd := &cobra.Command{Use: "init"}
	cmd.Flags().Bool("dev", false, "")

	output := captureStdoutAndStderr(t, func() {
		withStdin(t, "3\n4\n", func() {
			if err := runInit(cmd, nil); err != nil {
				t.Fatalf("runInit() error = %v", err)
			}
		})
	})

	if !strings.Contains(output, "Skip build setup for now") {
		t.Fatalf("expected new menu option in output, got:\n%s", output)
	}
	if !strings.Contains(output, "Skipped build setup for now") {
		t.Fatalf("expected skip confirmation, got:\n%s", output)
	}
	if !strings.Contains(output, "Kept placeholder platforms: android") {
		t.Fatalf("expected placeholder platform confirmation, got:\n%s", output)
	}

	cfg, err := config.LoadProjectConfig(filepath.Join(workDir, ".revyl", "config.yaml"))
	if err != nil {
		t.Fatalf("LoadProjectConfig() error = %v", err)
	}

	if cfg.Build.System != "Gradle (Android)" {
		t.Fatalf("build.system = %q, want %q", cfg.Build.System, "Gradle (Android)")
	}
	if cfg.Build.Command != "" || cfg.Build.Output != "" {
		t.Fatalf("top-level build config not cleared: %+v", cfg.Build)
	}

	platformCfg, ok := cfg.Build.Platforms["android"]
	if !ok {
		t.Fatalf("missing android placeholder platform in %+v", cfg.Build.Platforms)
	}
	if platformCfg.Command != "" || platformCfg.Output != "" || platformCfg.Scheme != "" || platformCfg.AppID != "" {
		t.Fatalf("android placeholder platform not cleared: %+v", platformCfg)
	}
}

func TestRunInitSkipBuildSetupForNowDefersHotReloadForPlaceholderProjects(t *testing.T) {
	tests := []struct {
		name     string
		fixture  string
		platform string
		stdin    string
	}{
		{name: "expo", fixture: "bug-bazaar", platform: "ios", stdin: "3\n4\n"},
		// rn-bare-minimal detects iOS with -scheme *, which triggers a scheme
		// prompt when xcodebuild is absent (CI). Prepend "\n" to skip it.
		{name: "react-native", fixture: "rn-bare-minimal", platform: "ios", stdin: "\n3\n4\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetInitGlobals(t)

			workDir := copyInternalAppFixture(t, filepath.Join(repoRootForInitFixtureTests(t), "internal-apps", tt.fixture), true)
			withWorkingDir(t, workDir)

			cmd := &cobra.Command{Use: "init"}
			cmd.Flags().Bool("dev", false, "")

			output := captureStdoutAndStderr(t, func() {
				withStdin(t, tt.stdin, func() {
					if err := runInit(cmd, nil); err != nil {
						t.Fatalf("runInit() error = %v", err)
					}
				})
			})

			cfg, err := config.LoadProjectConfig(filepath.Join(workDir, ".revyl", "config.yaml"))
			if err != nil {
				t.Fatalf("LoadProjectConfig() error = %v", err)
			}

			platformCfg, ok := cfg.Build.Platforms[tt.platform]
			if !ok {
				t.Fatalf("missing %s placeholder platform in %+v", tt.platform, cfg.Build.Platforms)
			}
			if platformCfg.Command != "" || platformCfg.Output != "" || platformCfg.Scheme != "" || platformCfg.AppID != "" {
				t.Fatalf("%s placeholder platform not cleared: %+v", tt.platform, platformCfg)
			}
			if cfg.HotReload.IsConfigured() {
				t.Fatalf("hot reload should not be configured after skipping build setup for %s", tt.fixture)
			}

			if !strings.Contains(output, "Hot reload and live dev setup are deferred until at least one build platform has a build command and artifact path.") {
				t.Fatalf("expected deferred hot reload guidance, got:\n%s", output)
			}
			if strings.Contains(output, "Hot reload configured during init") {
				t.Fatalf("unexpected hot reload configured message in output:\n%s", output)
			}
			if strings.Contains(output, "revyl dev") {
				t.Fatalf("unexpected revyl dev suggestion in output:\n%s", output)
			}
		})
	}
}

func TestRunBuildUploadPlaceholderGuidanceListsPlaceholderPlatforms(t *testing.T) {
	resetBuildUploadGlobals(t)
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())

	tmp := t.TempDir()
	withWorkingDir(t, tmp)

	writeBuildUploadProjectConfig(t, tmp, &config.ProjectConfig{
		Build: config.BuildConfig{
			System: "Gradle (Android)",
			Platforms: map[string]config.BuildPlatform{
				"android": {},
			},
		},
	})

	cmd := newBuildUploadTestCommand()
	output := captureStdoutAndStderr(t, func() {
		err := runBuildUpload(cmd, nil)
		if err == nil {
			t.Fatal("runBuildUpload() error = nil, want placeholder guidance error")
		}
	})

	if !strings.Contains(output, "Detected build platforms are present but not configured yet") {
		t.Fatalf("expected placeholder guidance, got:\n%s", output)
	}
	if !strings.Contains(output, "Placeholder platforms: android") {
		t.Fatalf("expected placeholder platform list, got:\n%s", output)
	}
}

func TestRunBuildUploadExplicitPlaceholderPlatformShowsSetupGuidance(t *testing.T) {
	resetBuildUploadGlobals(t)
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())

	tmp := t.TempDir()
	withWorkingDir(t, tmp)

	writeBuildUploadProjectConfig(t, tmp, &config.ProjectConfig{
		Build: config.BuildConfig{
			System: "Gradle (Android)",
			Platforms: map[string]config.BuildPlatform{
				"android": {},
			},
		},
	})

	uploadPlatformFlag = "android"

	cmd := newBuildUploadTestCommand()
	output := captureStdoutAndStderr(t, func() {
		err := runBuildUpload(cmd, nil)
		if err == nil {
			t.Fatal("runBuildUpload() error = nil, want placeholder guidance error")
		}
		if !strings.Contains(err.Error(), "not ready yet") {
			t.Fatalf("error = %q, want placeholder readiness guidance", err.Error())
		}
	})

	if strings.Contains(output, "Unknown platform") {
		t.Fatalf("unexpected unknown-platform output for placeholder platform:\n%s", output)
	}
	if !strings.Contains(output, "Build platform android is not ready yet") {
		t.Fatalf("expected placeholder readiness message, got:\n%s", output)
	}
	if !strings.Contains(output, "Finish native setup or add build.platforms.android.command and build.platforms.android.output in .revyl/config.yaml") {
		t.Fatalf("expected placeholder setup guidance, got:\n%s", output)
	}
}

func TestRunBuildUploadDryRunUsesConfiguredLabels(t *testing.T) {
	resetBuildUploadGlobals(t)
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())

	tmp := t.TempDir()
	withWorkingDir(t, tmp)

	writeBuildUploadProjectConfig(t, tmp, &config.ProjectConfig{
		Build: config.BuildConfig{
			System: "Gradle (Android)",
			Platforms: map[string]config.BuildPlatform{
				"android": {
					Command: "./gradlew assembleDebug",
					Output:  "app/build/outputs/apk/debug/app-debug.apk",
				},
			},
		},
	})

	buildDryRun = true
	uploadPlatformFlag = "android"

	cmd := newBuildUploadTestCommand()
	output := captureStdoutAndStderr(t, func() {
		if err := runBuildUpload(cmd, nil); err != nil {
			t.Fatalf("runBuildUpload() error = %v", err)
		}
	})

	if !strings.Contains(output, "Configured Build Command") {
		t.Fatalf("expected configured build command label, got:\n%s", output)
	}
	if !strings.Contains(output, "Configured Artifact Path") {
		t.Fatalf("expected configured artifact path label, got:\n%s", output)
	}
}

func TestRunBuildUploadSkipBuildMentionsConfiguredArtifactPath(t *testing.T) {
	resetBuildUploadGlobals(t)
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())

	tmp := t.TempDir()
	withWorkingDir(t, tmp)

	writeBuildUploadProjectConfig(t, tmp, &config.ProjectConfig{
		Build: config.BuildConfig{
			System: "Gradle (Android)",
			Platforms: map[string]config.BuildPlatform{
				"android": {
					Command: "./gradlew assembleDebug",
					Output:  "app/build/outputs/apk/debug/app-debug.apk",
				},
			},
		},
	})

	buildSkip = true
	uploadPlatformFlag = "android"

	cmd := newBuildUploadTestCommand()
	output := captureStdoutAndStderr(t, func() {
		err := runBuildUpload(cmd, nil)
		if err == nil {
			t.Fatal("runBuildUpload() error = nil, want missing artifact error")
		}
	})

	if !strings.Contains(output, "Skipping build step") {
		t.Fatalf("expected skip-build message, got:\n%s", output)
	}
	if !strings.Contains(output, "Resolving configured artifact path from .revyl/config.yaml") {
		t.Fatalf("expected configured artifact resolution message, got:\n%s", output)
	}
	if !strings.Contains(output, "Configured artifact path not found") {
		t.Fatalf("expected configured artifact-path error, got:\n%s", output)
	}
}

func TestFirstBuildOutcomeTracking(t *testing.T) {
	t.Run("empty outcome reports no attempts and no successes", func(t *testing.T) {
		o := &firstBuildOutcome{}
		if o.WasAttempted() {
			t.Fatal("empty outcome should not report WasAttempted")
		}
		if o.HasSucceeded() {
			t.Fatal("empty outcome should not report HasSucceeded")
		}
	})

	t.Run("single failure records attempted without success", func(t *testing.T) {
		o := &firstBuildOutcome{}
		o.RecordFailure("android")

		if !o.WasAttempted() {
			t.Fatal("should report WasAttempted after failure")
		}
		if o.HasSucceeded() {
			t.Fatal("should not report HasSucceeded after only failures")
		}
		if len(o.Failed) != 1 || o.Failed[0] != "android" {
			t.Fatalf("Failed = %v, want [android]", o.Failed)
		}
	})

	t.Run("success after failure removes from Failed and clears HasFailed", func(t *testing.T) {
		o := &firstBuildOutcome{}
		o.RecordFailure("android")

		if !o.HasFailed() {
			t.Fatal("should report HasFailed after RecordFailure")
		}

		o.RecordSuccess("android")

		if !o.HasSucceeded() {
			t.Fatal("should report HasSucceeded after retry success")
		}
		if o.HasFailed() {
			t.Fatal("should no longer report HasFailed after the failed platform succeeds")
		}
		if len(o.Failed) != 0 {
			t.Fatalf("Failed = %v, want empty after retry success", o.Failed)
		}
	})

	t.Run("duplicate records are deduplicated", func(t *testing.T) {
		o := &firstBuildOutcome{}
		o.RecordFailure("android")
		o.RecordFailure("android")

		if len(o.Attempted) != 1 {
			t.Fatalf("Attempted = %v, want 1 entry", o.Attempted)
		}
		if len(o.Failed) != 1 {
			t.Fatalf("Failed = %v, want 1 entry", o.Failed)
		}
	})
}

func TestWhatsNextMenuGatingOnBuildOutcome(t *testing.T) {
	runnableCfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			System: "Bazel",
			Platforms: map[string]config.BuildPlatform{
				"android": {
					Command: "bazel build //app:app -c dbg",
					Output:  "bazel-bin/app/app.apk",
					AppID:   "test-app-id",
				},
			},
		},
	}

	t.Run("no build attempted allows live dev", func(t *testing.T) {
		outcome := &firstBuildOutcome{}
		canStart := hasRunnableBuildPlatforms(runnableCfg) && (!outcome.WasAttempted() || outcome.HasSucceeded())

		if !canStart {
			t.Fatal("live dev should be allowed when no build was attempted but config is runnable")
		}
	})

	t.Run("all builds failed suppresses live dev", func(t *testing.T) {
		outcome := &firstBuildOutcome{}
		outcome.RecordFailure("android")

		canStart := hasRunnableBuildPlatforms(runnableCfg) && (!outcome.WasAttempted() || outcome.HasSucceeded())
		hasFailedBuilds := outcome.HasFailed()

		if canStart {
			t.Fatal("live dev should be suppressed when the only build failed")
		}
		if !hasFailedBuilds {
			t.Fatal("should offer retry when builds failed")
		}
	})

	t.Run("at least one success enables live dev", func(t *testing.T) {
		outcome := &firstBuildOutcome{}
		outcome.RecordFailure("ios")
		outcome.RecordSuccess("android")

		canStart := hasRunnableBuildPlatforms(runnableCfg) && (!outcome.WasAttempted() || outcome.HasSucceeded())

		if !canStart {
			t.Fatal("live dev should be allowed when at least one build succeeded")
		}
	})

	t.Run("partial failure still offers retry for failed platforms", func(t *testing.T) {
		outcome := &firstBuildOutcome{}
		outcome.RecordFailure("ios")
		outcome.RecordSuccess("android")

		hasFailedBuilds := outcome.HasFailed()

		if !hasFailedBuilds {
			t.Fatal("should offer retry when some platforms failed even if others succeeded")
		}
		if !outcome.HasSucceeded() {
			t.Fatal("should also report HasSucceeded for the successful platform")
		}
	})
}

func TestRecordBatchOutcome(t *testing.T) {
	outcome := &firstBuildOutcome{}
	results := []wizardBuildResult{
		{Platform: "ios", Err: fmt.Errorf("build failed")},
		{Platform: "android", Version: "1.0.0"},
	}

	recordBatchOutcome(outcome, results)

	if len(outcome.Failed) != 1 || outcome.Failed[0] != "ios" {
		t.Fatalf("Failed = %v, want [ios]", outcome.Failed)
	}
	if len(outcome.Succeeded) != 1 || outcome.Succeeded[0] != "android" {
		t.Fatalf("Succeeded = %v, want [android]", outcome.Succeeded)
	}
	if !outcome.HasSucceeded() {
		t.Fatal("should report HasSucceeded with one successful platform")
	}
}
