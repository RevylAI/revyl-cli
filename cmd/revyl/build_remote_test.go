package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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

func remoteBuildStatusServer(t *testing.T, status api.RemoteBuildStatusResponse, logLines ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/remote/job-1/status":
			if err := json.NewEncoder(w).Encode(status); err != nil {
				t.Fatalf("failed to encode response: %v", err)
			}
		case "/api/v1/apps/remote/job-1/logs":
			events := []api.RemoteBuildLogEvent{}
			nextCursor := ""
			for i, line := range logLines {
				if strings.TrimSpace(line) == "" {
					continue
				}
				level := "info"
				lower := strings.ToLower(line)
				if strings.Contains(lower, "error:") {
					level = "error"
				} else if strings.Contains(lower, "warning:") {
					level = "warning"
				}
				nextCursor = strconv.Itoa(i+1) + "-0"
				events = append(events, api.RemoteBuildLogEvent{
					Id:      nextCursor,
					Level:   &level,
					Message: line,
				})
			}
			if err := json.NewEncoder(w).Encode(api.RemoteBuildLogsResponse{
				Events:     &events,
				NextCursor: &nextCursor,
			}); err != nil {
				t.Fatalf("failed to encode response: %v", err)
			}
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
}

func TestPollRemoteBuildStatusResultTreatsCancelledAsTerminalError(t *testing.T) {
	withFastRemoteBuildPolling(t)
	server := remoteBuildStatusServer(t, api.RemoteBuildStatusResponse{
		Status: "cancelled",
	})
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	_, err := pollRemoteBuildStatusResult(context.Background(), client, "job-1", false)

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
	_, err := pollRemoteBuildStatusResult(context.Background(), client, "job-1", false)

	if err == nil || !strings.Contains(err.Error(), "no build version ID") {
		t.Fatalf("pollRemoteBuildStatusResult() error = %v, want missing version ID", err)
	}
}

func TestPollRemoteBuildStatusResultPrintsFailureLogTail(t *testing.T) {
	withFastRemoteBuildPolling(t)
	errMsg := "xcodebuild failed"
	server := remoteBuildStatusServer(t, api.RemoteBuildStatusResponse{
		Status: "failed",
		Error:  &errMsg,
	}, "CompileSwift AppDelegate.swift", "error: no such module 'DemoKit'")
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	var err error
	output := captureStdoutAndStderr(t, func() {
		_, err = pollRemoteBuildStatusResult(context.Background(), client, "job-1", false)
	})

	if err == nil || !strings.Contains(err.Error(), "xcodebuild failed") {
		t.Fatalf("pollRemoteBuildStatusResult() error = %v, want xcodebuild failure", err)
	}
	if !strings.Contains(output, "--- Build log tail ---") || !strings.Contains(output, "no such module 'DemoKit'") {
		t.Fatalf("output did not include failure log tail:\n%s", output)
	}
}

func TestPrintRemoteBuildStatusSummaryPrintsStatusAfterLogs(t *testing.T) {
	platform := "ios"
	versionID := "version-1"
	status := api.RemoteBuildStatusResponse{
		Status:    "success",
		Platform:  &platform,
		VersionId: &versionID,
	}
	server := remoteBuildStatusServer(t, status, "first log line", "** BUILD SUCCEEDED **")
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	output := captureStdoutAndStderr(t, func() {
		printRemoteBuildStatusSummary(context.Background(), client, "job-1", &status)
	})

	logIndex := strings.LastIndex(output, "** BUILD SUCCEEDED **")
	statusIndex := strings.LastIndex(output, "Status:")
	if logIndex == -1 || statusIndex == -1 || statusIndex < logIndex {
		t.Fatalf("status should print after logs:\n%s", output)
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, "Status:") || !strings.Contains(lastLine, "success") {
		t.Fatalf("last line = %q, want final status", lastLine)
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
					Secrets: []string{"EXPO_TOKEN"},
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
	if len(resolved.Secrets) != 1 || resolved.Secrets[0] != "EXPO_TOKEN" {
		t.Fatalf("Secrets = %#v, want EXPO_TOKEN from config", resolved.Secrets)
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

func TestResolveRemoteBuildPlatformTimeoutFromNonCanonicalKey(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, ".revyl")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	configYAML := []byte(`project:
  name: Demo
build:
  platforms:
    ios-release:
      app_id: app-ios
      command: bundle exec fastlane build_release
      output: build/Example.app.zip
      timeout: 5400
`)
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), configYAML, 0o644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}

	resolved, err := resolveRemoteBuildPlatform(tmp, "ios", "")
	if err != nil {
		t.Fatalf("resolveRemoteBuildPlatform(): %v", err)
	}

	if resolved.PlatformKey != "ios-release" {
		t.Fatalf("PlatformKey = %q, want ios-release", resolved.PlatformKey)
	}
	if resolved.TimeoutSeconds == nil || *resolved.TimeoutSeconds != 5400 {
		t.Fatalf("TimeoutSeconds = %v, want 5400", resolved.TimeoutSeconds)
	}
}

func TestResolveRemoteBuildPlatformRejectsNegativeTimeout(t *testing.T) {
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
      command: xcodebuild
      output: build/Example.app.zip
      timeout: -60
`)
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), configYAML, 0o644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}

	_, err := resolveRemoteBuildPlatform(tmp, "ios", "")
	if err == nil || !strings.Contains(err.Error(), "build.platforms.ios.timeout") {
		t.Fatalf("error = %v, want negative timeout rejection", err)
	}
}

func TestBuildPlatformTimeoutSeconds(t *testing.T) {
	if got, err := buildPlatformTimeoutSeconds(config.BuildPlatform{}, "ios"); err != nil || got != nil {
		t.Fatalf("unset timeout = (%v, %v), want (nil, nil)", got, err)
	}
	got, err := buildPlatformTimeoutSeconds(config.BuildPlatform{Timeout: 900}, "ios-dev")
	if err != nil || got == nil || *got != 900 {
		t.Fatalf("timeout 900 = (%v, %v), want 900", got, err)
	}
	if _, err := buildPlatformTimeoutSeconds(config.BuildPlatform{Timeout: -1}, "ios-dev"); err == nil || !strings.Contains(err.Error(), "build.platforms.ios-dev.timeout") {
		t.Fatalf("negative timeout error = %v, want key-labeled error", err)
	}
}

func TestRemoteBuildTimeoutFlagSeconds(t *testing.T) {
	if got, err := remoteBuildTimeoutFlagSeconds(0, false); err != nil || got != nil {
		t.Fatalf("unchanged flag = (%v, %v), want (nil, nil)", got, err)
	}
	got, err := remoteBuildTimeoutFlagSeconds(120, true)
	if err != nil || got == nil || *got != 120 {
		t.Fatalf("flag 120 = (%v, %v), want 120", got, err)
	}
	if _, err := remoteBuildTimeoutFlagSeconds(0, true); err == nil {
		t.Fatal("flag 0 error = nil, want positive-seconds error")
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
	if result.AppID != "app-android" {
		t.Fatalf("app = %s, want app-android", result.AppID)
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

func TestMergeBuildSecretRefsValidatesAndDeduplicates(t *testing.T) {
	got, err := mergeBuildSecretRefs(
		[]string{"EXPO_TOKEN", " SHARED_TOKEN "},
		[]string{"EXPO_TOKEN", "CLI_TOKEN"},
	)
	if err != nil {
		t.Fatalf("mergeBuildSecretRefs() error = %v", err)
	}
	want := []string{"EXPO_TOKEN", "SHARED_TOKEN", "CLI_TOKEN"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("mergeBuildSecretRefs() = %#v, want %#v", got, want)
	}

	if _, err := mergeBuildSecretRefs([]string{"invalid-name"}, nil); err == nil {
		t.Fatal("mergeBuildSecretRefs() error = nil, want invalid name error")
	}
}

func TestValidateBuildEnvSecretCollisions(t *testing.T) {
	err := validateBuildEnvSecretCollisions(
		map[string]string{"EXPO_TOKEN": "plaintext"},
		[]string{"EXPO_TOKEN"},
	)
	if err == nil || !strings.Contains(err.Error(), "EXPO_TOKEN") {
		t.Fatalf("validateBuildEnvSecretCollisions() error = %v, want EXPO_TOKEN collision", err)
	}
}

func TestRemoteBuildConfigIncludesSecretReferences(t *testing.T) {
	appID := uuid.MustParse("00000000-0000-0000-0000-000000000456")
	config := remoteBuildConfigFromResolved(appID, remoteBuildPlatformConfig{
		Platform: "ios",
		Command:  "xcodebuild",
		Output:   "build/App.app",
		Secrets:  []string{"EXPO_TOKEN"},
	})

	if config.SecretRefs == nil || len(*config.SecretRefs) != 1 || (*config.SecretRefs)[0] != "EXPO_TOKEN" {
		t.Fatalf("SecretRefs = %#v, want EXPO_TOKEN", config.SecretRefs)
	}
	if config.Env != nil {
		t.Fatalf("Env = %#v, want nil", config.Env)
	}
}
