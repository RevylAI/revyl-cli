package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

func withFastRemoteBuildPolling(t *testing.T) {
	t.Helper()
	previous := remoteBuildPollInterval
	remoteBuildPollInterval = time.Millisecond
	t.Cleanup(func() {
		remoteBuildPollInterval = previous
	})
}

func remoteBuildStatusServer(t *testing.T, status api.RemoteBuildStatusResponse) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/apps/remote/job-1/status" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(status); err != nil {
			t.Fatalf("failed to encode response: %v", err)
		}
	}))
}

func TestPollBuildStatusTreatsCancelledAsTerminalError(t *testing.T) {
	withFastRemoteBuildPolling(t)
	errMsg := "Build cancelled"
	server := remoteBuildStatusServer(t, api.RemoteBuildStatusResponse{
		Status: "cancelled",
		Error:  &errMsg,
	})
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	err := pollBuildStatus(context.Background(), client, "job-1")

	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("pollBuildStatus() error = %v, want cancelled", err)
	}
}

func TestPollBuildStatusRejectsSuccessWithoutVersionID(t *testing.T) {
	withFastRemoteBuildPolling(t)
	server := remoteBuildStatusServer(t, api.RemoteBuildStatusResponse{
		Status: "success",
	})
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	err := pollBuildStatus(context.Background(), client, "job-1")

	if err == nil || !strings.Contains(err.Error(), "no build version ID") {
		t.Fatalf("pollBuildStatus() error = %v, want missing version ID", err)
	}
}

func TestPollRemoteBuildStatusResultTreatsCancelledAsTerminalError(t *testing.T) {
	withFastRemoteBuildPolling(t)
	server := remoteBuildStatusServer(t, api.RemoteBuildStatusResponse{
		Status: "cancelled",
	})
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	_, err := pollRemoteBuildStatusResult(context.Background(), client, "job-1")

	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("pollRemoteBuildStatusResult() error = %v, want cancelled", err)
	}
}

func TestPollRemoteBuildStatusResultRejectsSuccessWithoutVersionID(t *testing.T) {
	withFastRemoteBuildPolling(t)
	server := remoteBuildStatusServer(t, api.RemoteBuildStatusResponse{
		Status: "success",
	})
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	_, err := pollRemoteBuildStatusResult(context.Background(), client, "job-1")

	if err == nil || !strings.Contains(err.Error(), "no build version ID") {
		t.Fatalf("pollRemoteBuildStatusResult() error = %v, want missing version ID", err)
	}
}

func TestPollRemoteBuildStatusResultPrintsFailureLogTail(t *testing.T) {
	withFastRemoteBuildPolling(t)
	errMsg := "xcodebuild failed"
	logs := "CompileSwift AppDelegate.swift\nerror: no such module 'DemoKit'"
	server := remoteBuildStatusServer(t, api.RemoteBuildStatusResponse{
		Status:   "failed",
		Error:    &errMsg,
		LogsTail: &logs,
	})
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	var err error
	output := captureStdoutAndStderr(t, func() {
		_, err = pollRemoteBuildStatusResult(context.Background(), client, "job-1")
	})

	if err == nil || !strings.Contains(err.Error(), "xcodebuild failed") {
		t.Fatalf("pollRemoteBuildStatusResult() error = %v, want xcodebuild failure", err)
	}
	if !strings.Contains(output, "--- Build log tail ---") || !strings.Contains(output, "no such module 'DemoKit'") {
		t.Fatalf("output did not include failure log tail:\n%s", output)
	}
}

func TestRemoteBuildTimeoutFromConfigUsesProjectDefaults(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".revyl"), 0o755); err != nil {
		t.Fatalf("mkdir .revyl: %v", err)
	}
	configYAML := []byte("defaults:\n  timeout: 7200\n")
	if err := os.WriteFile(filepath.Join(cwd, ".revyl", "config.yaml"), configYAML, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got := remoteBuildTimeoutFromConfig(cwd)
	if got != 2*time.Hour {
		t.Fatalf("remoteBuildTimeoutFromConfig() = %v, want 2h", got)
	}
}

func TestBuildRemoteCommandDoesNotExposeRunnerFlag(t *testing.T) {
	if flag := buildRemoteCmd.Flags().Lookup("runner"); flag != nil {
		t.Fatalf("remote build still exposes --runner flag")
	}
}

func TestResolveRemoteBuildPlatformAndroidReadsConfig(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, ".revyl")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	cfg := &config.ProjectConfig{
		Project: config.Project{Name: "Demo"},
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"android": {
					AppID:   "app-android",
					Setup:   "pnpm install",
					Command: "./gradlew assembleDebug",
					Output:  "app/build/outputs/apk/debug/app-debug.apk",
					Env: map[string]string{
						"API_URL": "https://config.example.com",
					},
				},
			},
		},
	}
	if err := config.WriteProjectConfig(filepath.Join(configDir, "config.yaml"), cfg); err != nil {
		t.Fatalf("WriteProjectConfig(): %v", err)
	}

	resolved, err := resolveRemoteBuildPlatform(tmp, "android", "")
	if err != nil {
		t.Fatalf("resolveRemoteBuildPlatform(): %v", err)
	}

	if resolved.Platform != "android" {
		t.Fatalf("Platform = %q, want android", resolved.Platform)
	}
	if resolved.AppID != "app-android" {
		t.Fatalf("AppID = %q, want app-android", resolved.AppID)
	}
	if resolved.Setup != "pnpm install" {
		t.Fatalf("Setup = %q, want pnpm install", resolved.Setup)
	}
	if resolved.Command != "./gradlew assembleDebug" {
		t.Fatalf("Command = %q, want ./gradlew assembleDebug", resolved.Command)
	}
	if resolved.Output != "app/build/outputs/apk/debug/app-debug.apk" {
		t.Fatalf("Output = %q, want APK path", resolved.Output)
	}
	if resolved.Env["API_URL"] != "https://config.example.com" {
		t.Fatalf("Env = %#v, want API_URL from config", resolved.Env)
	}
}

func TestResolveRemoteBuildPlatformReadsMultipleCommands(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, ".revyl")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	configYAML := []byte(`project:
  name: Demo
build:
  platforms:
    ios:
      app_id: app-ios
      command: legacy build
      commands:
        - npm ci
        - bundle exec fastlane build_simulator_debug
      output: build/Example.app.zip
`)
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), configYAML, 0o644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}

	resolved, err := resolveRemoteBuildPlatform(tmp, "ios", "")
	if err != nil {
		t.Fatalf("resolveRemoteBuildPlatform(): %v", err)
	}

	if len(resolved.Commands) != 2 || resolved.Commands[0] != "npm ci" || resolved.Commands[1] != "bundle exec fastlane build_simulator_debug" {
		t.Fatalf("Commands = %#v", resolved.Commands)
	}
	if resolved.Command != "npm ci && bundle exec fastlane build_simulator_debug" {
		t.Fatalf("Command = %q", resolved.Command)
	}
}

func TestResolveRemoteBuildPlatformReadsRepoBackedSource(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, ".revyl")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	configYAML := []byte(`project:
  name: Organic Maps
build:
  source:
    type: git
    repo_url: https://github.com/organicmaps/organicmaps.git
    ref: master
    subdir: android
    lfs: true
  platforms:
    android:
      app_id: app-android
      command: cd android && ./gradlew assembleWebDebug
      output: android/app/build/outputs/apk/web/debug/*.apk
      runner_id: stale-runner-label
`)
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), configYAML, 0o644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}

	resolved, err := resolveRemoteBuildPlatform(tmp, "android", "")
	if err != nil {
		t.Fatalf("resolveRemoteBuildPlatform(): %v", err)
	}

	if !remoteBuildUsesGitSource(resolved.Source) {
		t.Fatalf("expected git source to be enabled: %#v", resolved.Source)
	}
	normalized := normalizeRemoteGitSource(resolved.Source)
	if normalized.RepoURL != "https://github.com/organicmaps/organicmaps.git" {
		t.Fatalf("RepoURL = %q, want Organic Maps repo", normalized.RepoURL)
	}
	if normalized.Ref != "master" || normalized.Subdir != "android" || !normalized.LFS {
		t.Fatalf("source = %#v, want ref/subdir/lfs preserved", normalized)
	}
}

func TestRemoteBuildSuccessJSONIncludesAndroidArtifactFields(t *testing.T) {
	versionID := "version-123"
	version := "remote-1"
	artifactType := "apk"
	packageID := "com.example.app"
	logs := "last log line"
	durationMs := 1200
	startedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	phaseTimings := []api.RemoteBuildPhaseTiming{
		{
			Phase:      "build",
			StartedAt:  startedAt,
			DurationMs: &durationMs,
		},
	}

	result := remoteBuildSuccessJSON(
		remoteBuildPlatformConfig{
			Platform: "android",
			AppID:    "app-android",
		},
		"job-1",
		&api.RemoteBuildStatusResponse{
			Status:       "success",
			VersionId:    &versionID,
			Version:      &version,
			ArtifactType: &artifactType,
			PackageId:    &packageID,
			LogsTail:     &logs,
			PhaseTimings: &phaseTimings,
		},
	)

	if result.Status != "success" || result.Platform != "android" {
		t.Fatalf("status/platform = %s/%s, want success/android", result.Status, result.Platform)
	}
	if result.BuildJobID != "job-1" || result.BuildVersionID != versionID {
		t.Fatalf("job/version = %s/%s, want job-1/%s", result.BuildJobID, result.BuildVersionID, versionID)
	}
	if result.ArtifactType != "apk" || result.PackageID != packageID {
		t.Fatalf("artifact/package = %s/%s, want apk/%s", result.ArtifactType, result.PackageID, packageID)
	}
	if result.AppID != "app-android" || result.LogsTail != logs {
		t.Fatalf("app/logs = %s/%s, want app-android/%s", result.AppID, result.LogsTail, logs)
	}
	if len(result.PhaseTimings) != 1 || result.PhaseTimings[0].Phase != "build" {
		t.Fatalf("PhaseTimings = %#v, want build timing", result.PhaseTimings)
	}
}

func TestRemoteBuildFailureJSONIncludesDiscoveryGuidance(t *testing.T) {
	phase := "artifact_discovery"
	errMsg := "Multiple APK artifacts found"
	fix := "Set build.platforms.android.output"
	candidates := []string{"app-debug.apk", "app-release.apk"}
	durationMs := 2500
	startedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	phaseTimings := []api.RemoteBuildPhaseTiming{
		{
			Phase:      "artifact",
			StartedAt:  startedAt,
			DurationMs: &durationMs,
		},
	}

	result := remoteBuildFailureJSON(
		remoteBuildPlatformConfig{Platform: "android", AppID: "app-android"},
		"job-1",
		&api.RemoteBuildStatusResponse{
			Status:             "failed",
			Error:              &errMsg,
			Phase:              &phase,
			SuggestedFix:       &fix,
			CandidateArtifacts: &candidates,
			PhaseTimings:       &phaseTimings,
		},
		context.Canceled,
	)

	if result.Status != "failed" || result.Phase != phase {
		t.Fatalf("status/phase = %s/%s, want failed/%s", result.Status, result.Phase, phase)
	}
	if result.Error != errMsg || result.SuggestedFix != fix {
		t.Fatalf("error/fix = %s/%s, want backend guidance", result.Error, result.SuggestedFix)
	}
	if len(result.CandidateArtifacts) != 2 || result.CandidateArtifacts[0] != "app-debug.apk" {
		t.Fatalf("CandidateArtifacts = %#v, want APK candidates", result.CandidateArtifacts)
	}
	if len(result.PhaseTimings) != 1 || result.PhaseTimings[0].Phase != "artifact" {
		t.Fatalf("PhaseTimings = %#v, want artifact timing", result.PhaseTimings)
	}
}

func TestCompletedRemoteBuildStatusErrorWrapsTerminalFailure(t *testing.T) {
	phase := "build"
	platform := "android"
	appID := "app-android"
	versionID := "version-123"
	err := completedRemoteBuildStatusError("job-1", &api.RemoteBuildStatusResponse{
		Status:    "failed",
		Phase:     &phase,
		Platform:  &platform,
		AppId:     &appID,
		VersionId: &versionID,
	}, errors.New("remote build failed"))

	var completed *analytics.CompletedError
	if !errors.As(err, &completed) {
		t.Fatalf("error = %T, want CompletedError", err)
	}
	completion := completed.Completion()
	if completion.Domain != "remote_build" || completion.DomainStatus != "failed" || completion.ExitCode != 1 {
		t.Fatalf("completion = %#v, want failed remote build completion", completion)
	}
	if got := completion.Properties["remote_build_job_id"]; got != "job-1" {
		t.Fatalf("remote_build_job_id = %v, want job-1", got)
	}
	if got := completion.Properties["remote_build_platform"]; got != "android" {
		t.Fatalf("remote_build_platform = %v, want android", got)
	}
	if got := completion.Properties["remote_build_app_id"]; got != "app-android" {
		t.Fatalf("remote_build_app_id = %v, want app-android", got)
	}
	if got := completion.Properties["remote_build_version_id"]; got != "version-123" {
		t.Fatalf("remote_build_version_id = %v, want version-123", got)
	}
	if got := completion.Properties["remote_build_phase"]; got != "build" {
		t.Fatalf("remote_build_phase = %v, want build", got)
	}
}

func TestCompletedRemoteBuildStatusErrorKeepsNonTerminalErrorsAsCommandFailures(t *testing.T) {
	original := errors.New("remote build polling timed out")
	err := completedRemoteBuildStatusError("job-1", &api.RemoteBuildStatusResponse{
		Status: "running",
	}, original)

	if err != original {
		t.Fatalf("error = %v, want original error", err)
	}
	var completed *analytics.CompletedError
	if errors.As(err, &completed) {
		t.Fatalf("running status should not be wrapped as completed domain result")
	}
}
