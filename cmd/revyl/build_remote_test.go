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

func TestPrintRemoteBuildConcurrencyWaitIsConcise(t *testing.T) {
	output := captureStdoutAndStderr(t, printRemoteBuildConcurrencyWait)

	if !strings.Contains(output, remoteBuildConcurrencyWaitMessage) {
		t.Fatalf("output missing concurrency wait message:\n%s", output)
	}
	for _, unwanted := range []string{"another build", "Upgrade", "settings/plans"} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("output contains obsolete text %q:\n%s", unwanted, output)
		}
	}
}

func TestOrganizationConcurrencyWaitRequiresQueuedStatusAndExactPhase(t *testing.T) {
	phase := "organization_concurrency"
	waiting := &api.RemoteBuildStatusResponse{Status: "pending", Phase: &phase}
	if !isOrganizationConcurrencyWait(waiting) {
		t.Fatal("pending organization_concurrency build should be recognized as waiting for organization capacity")
	}
	if got := remoteBuildDisplayStatus(waiting); got != remoteBuildConcurrencyWaitMessage {
		t.Fatalf("remoteBuildDisplayStatus() = %q, want %q", got, remoteBuildConcurrencyWaitMessage)
	}

	dispatch := "dispatch"
	if isOrganizationConcurrencyWait(&api.RemoteBuildStatusResponse{Status: "pending", Phase: &dispatch}) {
		t.Fatal("normal dispatch queue should not be presented as organization concurrency")
	}
	if isOrganizationConcurrencyWait(&api.RemoteBuildStatusResponse{Status: "building", Phase: &phase}) {
		t.Fatal("building status should not be presented as waiting for organization concurrency")
	}
	if remoteBuildDisplayKey(&api.RemoteBuildStatusResponse{Status: "pending", Phase: &phase}) ==
		remoteBuildDisplayKey(&api.RemoteBuildStatusResponse{Status: "pending", Phase: &dispatch}) {
		t.Fatal("display state should change when a pending build enters organization concurrency")
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

func TestPrintRemoteBuildStartedLinksToAppScopedLogs(t *testing.T) {
	t.Setenv("REVYL_APP_URL", "https://preview.revyl.example/")
	output := captureStdoutAndStderr(t, func() {
		printRemoteBuildStarted(false, "app-123", "job-456")
	})

	buildStartedIndex := strings.Index(output, "Build started")
	viewLogsIndex := strings.Index(output, "View logs:")
	if buildStartedIndex == -1 || viewLogsIndex < buildStartedIndex {
		t.Fatalf("output should print the logs link after the build confirmation:\n%s", output)
	}
	if !strings.Contains(output, "https://preview.revyl.example/apps/app-123/builds/job-456#logs") {
		t.Fatalf("output did not include the app-scoped logs URL:\n%s", output)
	}
	if strings.Contains(output, "Started build with id") {
		t.Fatalf("output still exposes the legacy build-id message:\n%s", output)
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

func TestCreateSourceArchivePreservesMonorepoLayout(t *testing.T) {
	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	projectRoot := filepath.Join(repoRoot, "apps", "mobile")
	if err := os.MkdirAll(filepath.Join(projectRoot, ".revyl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, "packages", "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repoRoot, "package.json"), "{}\n")
	writeFile(t, filepath.Join(projectRoot, ".revyl", "config.yaml"), "project:\n  name: mobile\n")
	writeFile(t, filepath.Join(projectRoot, "app.json"), "{}\n")
	writeFile(t, filepath.Join(repoRoot, "packages", "shared", "package.json"), "{}\n")
	runGit(t, repoRoot, "add", ".")
	runGit(t, repoRoot, "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "fixture")

	archivePath, err := createSourceArchive(repoRoot)
	if err != nil {
		t.Fatalf("createSourceArchive(): %v", err)
	}
	defer os.Remove(archivePath)

	files := readTarGz(t, archivePath)
	for _, path := range []string{
		"package.json",
		"apps/mobile/.revyl/config.yaml",
		"apps/mobile/app.json",
		"packages/shared/package.json",
	} {
		if _, ok := files[path]; !ok {
			t.Fatalf("archive missing %q; files = %v", path, files)
		}
	}
	if _, ok := files["app.json"]; ok {
		t.Fatal("archive flattened the app subtree into the source root")
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

func TestRemoteBuildConfigIncludesResolvedSourceSubdir(t *testing.T) {
	appID := uuid.MustParse("00000000-0000-0000-0000-000000000456")
	buildConfig := remoteBuildConfigFromResolved(appID, remoteBuildPlatformConfig{
		Platform:     "ios",
		Command:      "xcodebuild",
		Output:       "build/App.app",
		SourceSubdir: "apps/mobile",
	})

	if buildConfig.SourceSubdir == nil || *buildConfig.SourceSubdir != "apps/mobile" {
		t.Fatalf("SourceSubdir = %#v, want apps/mobile", buildConfig.SourceSubdir)
	}
}

func TestRemoteBuildConfigOmitsRepositoryRootSourceSubdir(t *testing.T) {
	appID := uuid.MustParse("00000000-0000-0000-0000-000000000456")
	buildConfig := remoteBuildConfigFromResolved(appID, remoteBuildPlatformConfig{
		Platform:     "ios",
		Command:      "xcodebuild",
		Output:       "build/App.app",
		SourceSubdir: ".",
	})

	if buildConfig.SourceSubdir != nil {
		t.Fatalf("SourceSubdir = %#v, want nil for repository root", buildConfig.SourceSubdir)
	}
}

func TestRemoteBuildConfigExplicitGitSubdirOverridesResolvedProject(t *testing.T) {
	appID := uuid.MustParse("00000000-0000-0000-0000-000000000456")
	buildConfig := remoteBuildConfigFromResolved(appID, remoteBuildPlatformConfig{
		Platform:     "android",
		Command:      "./gradlew assembleRelease",
		Output:       "build/app.apk",
		SourceSubdir: "apps/mobile",
		Source: config.BuildSource{
			Type:    "git",
			RepoURL: "https://example.com/example/repository.git",
			Subdir:  "clients/mobile",
		},
	})

	if buildConfig.SourceSubdir == nil || *buildConfig.SourceSubdir != "clients/mobile" {
		t.Fatalf("SourceSubdir = %#v, want clients/mobile", buildConfig.SourceSubdir)
	}
}

func TestRemoteBuildConfigPreservesEmptyExplicitGitSubdir(t *testing.T) {
	appID := uuid.MustParse("00000000-0000-0000-0000-000000000456")
	buildConfig := remoteBuildConfigFromResolved(appID, remoteBuildPlatformConfig{
		Platform:     "ios",
		Command:      "xcodebuild",
		Output:       "build/App.app",
		SourceSubdir: "apps/mobile",
		Source: config.BuildSource{
			Type:    "git",
			RepoURL: "https://example.com/example/repository.git",
		},
	})

	if buildConfig.SourceSubdir != nil {
		t.Fatalf("SourceSubdir = %#v, want nil for an explicit root Git source", buildConfig.SourceSubdir)
	}
}
