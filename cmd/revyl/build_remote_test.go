package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
      keep_derived_data: true
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
	if !resolved.KeepDerivedData {
		t.Fatalf("KeepDerivedData = false, want true")
	}
}

func TestRemoteBuildRunnerAvailabilityUnknownFailsClosed(t *testing.T) {
	decision := decideRemoteBuildRunnerAvailability(&api.BuildRunnerStatus{
		Available:   false,
		RunnerCount: -1,
	}, nil)

	if decision != remoteBuildRunnerAvailabilityUnavailable {
		t.Fatalf("decision = %v, want unavailable", decision)
	}
}

func TestRemoteBuildRunnerAvailabilityConfirmedUnavailableFails(t *testing.T) {
	decision := decideRemoteBuildRunnerAvailability(&api.BuildRunnerStatus{
		Available:   false,
		RunnerCount: 0,
	}, nil)

	if decision != remoteBuildRunnerAvailabilityUnavailable {
		t.Fatalf("decision = %v, want unavailable", decision)
	}
}

func TestRemoteBuildSuccessJSONIncludesAndroidArtifactFields(t *testing.T) {
	versionID := "version-123"
	version := "remote-1"
	artifactType := "apk"
	packageID := "com.example.app"
	logs := "last log line"

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
}

func TestRemoteBuildFailureJSONIncludesDiscoveryGuidance(t *testing.T) {
	phase := "artifact_discovery"
	errMsg := "Multiple APK artifacts found"
	fix := "Set build.platforms.android.output"
	candidates := []string{"app-debug.apk", "app-release.apk"}

	result := remoteBuildFailureJSON(
		remoteBuildPlatformConfig{Platform: "android", AppID: "app-android"},
		"job-1",
		&api.RemoteBuildStatusResponse{
			Status:             "failed",
			Error:              &errMsg,
			Phase:              &phase,
			SuggestedFix:       &fix,
			CandidateArtifacts: &candidates,
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
}
