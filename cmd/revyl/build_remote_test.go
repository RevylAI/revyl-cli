package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
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
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/devloop"
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

func remoteBuildStatusSequenceServer(t *testing.T, statuses ...api.RemoteBuildStatusResponse) *httptest.Server {
	t.Helper()
	requests := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/remote/job-1/logs":
			_, _ = w.Write([]byte(`{"events":[]}`))
		case "/api/v1/apps/remote/job-1/status":
			_ = json.NewEncoder(w).Encode(statuses[min(requests, len(statuses)-1)])
			requests++
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
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
	_, err := pollRemoteBuildStatusResult(context.Background(), client, "job-1", false, false)

	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("pollRemoteBuildStatusResult() error = %v, want cancelled", err)
	}
}

func TestPollRemoteBuildStatusResultTreatsTimeoutAsTerminalError(t *testing.T) {
	withFastRemoteBuildPolling(t)
	server := remoteBuildStatusServer(t, api.RemoteBuildStatusResponse{
		Status: "timeout",
	})
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	_, err := pollRemoteBuildStatusResult(context.Background(), client, "job-1", false, false)

	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("pollRemoteBuildStatusResult() error = %v, want timeout", err)
	}
}

func TestPollRemoteBuildStatusResultRejectsSuccessWithoutVersionID(t *testing.T) {
	withFastRemoteBuildPolling(t)
	server := remoteBuildStatusServer(t, api.RemoteBuildStatusResponse{
		Status: "success",
	})
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	_, err := pollRemoteBuildStatusResult(context.Background(), client, "job-1", false, false)

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
		_, err = pollRemoteBuildStatusResult(context.Background(), client, "job-1", false, false)
	})

	if err == nil || !strings.Contains(err.Error(), "xcodebuild failed") {
		t.Fatalf("pollRemoteBuildStatusResult() error = %v, want xcodebuild failure", err)
	}
	if !strings.Contains(output, "--- Build log tail ---") || !strings.Contains(output, "no such module 'DemoKit'") {
		t.Fatalf("output did not include failure log tail:\n%s", output)
	}
}

func TestRemoteBuildProgressQueuedPreservesPhase(t *testing.T) {
	status := &api.RemoteBuildStatusResponse{
		Status: "pending",
		Phase:  stringPtrOrNil("dispatch"),
	}

	progress := remoteBuildProgressFromStatus(status)

	if progress.State != devloop.BuildStateQueued {
		t.Fatalf("state = %q, want %q", progress.State, devloop.BuildStateQueued)
	}
	if progress.Phase != "dispatch" {
		t.Fatalf("phase = %q, want dispatch", progress.Phase)
	}
	if progress.Message != "Remote build pending" {
		t.Fatalf("message = %q, want generic queued status", progress.Message)
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

func TestRemoteBuildFailureJSONPreservesTimeoutStatus(t *testing.T) {
	result := remoteBuildFailureJSON(
		remoteBuildPlatformConfig{Platform: "ios", AppID: "app-ios"},
		"job-1",
		&api.RemoteBuildStatusResponse{Status: "timeout"},
		context.DeadlineExceeded,
	)

	if result.Status != "timeout" {
		t.Fatalf("status = %q, want timeout", result.Status)
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

func TestSourceArchivesPreserveMonorepoLayoutWithoutRevylignore(t *testing.T) {
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

	for _, archive := range []struct {
		name string
		make func(string, string) (string, error)
	}{
		{"committed", createSourceArchive},
		{"working tree", func(worktreeRoot, projectRoot string) (string, error) {
			return createSourceArchiveIncludingWorkingTree(worktreeRoot, projectRoot, true)
		}},
	} {
		t.Run(archive.name, func(t *testing.T) {
			archivePath, err := archive.make(repoRoot, projectRoot)
			if err != nil {
				t.Fatal(err)
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
		})
	}
}

func TestSourceArchivesApplyRevylignore(t *testing.T) {
	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	for _, dir := range []string{"app", "web", "docs"} {
		if err := os.MkdirAll(filepath.Join(repoRoot, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(repoRoot, ".revylignore"), "/web/\n/docs/*\n!/docs/keep.md\n")
	writeFile(t, filepath.Join(repoRoot, ".gitignore"), "/app/main.txt\n")
	writeFile(t, filepath.Join(repoRoot, "app", "main.txt"), "app")
	writeFile(t, filepath.Join(repoRoot, "web", "old.txt"), "web")
	writeFile(t, filepath.Join(repoRoot, "docs", "drop.md"), "drop")
	writeFile(t, filepath.Join(repoRoot, "docs", "keep.md"), "keep")
	runGit(t, repoRoot, "add", ".")
	runGit(t, repoRoot, "add", "-f", "app/main.txt")
	runGit(t, repoRoot, "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "fixture")

	runGit(t, repoRoot, "rm", "web/old.txt")
	if err := os.MkdirAll(filepath.Join(repoRoot, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repoRoot, "web", "new.txt"), "new")
	writeFile(t, filepath.Join(repoRoot, "docs", "staged.md"), "staged")
	runGit(t, repoRoot, "add", "docs/staged.md")

	for name, archive := range map[string]func(string) (string, error){
		"committed": func(root string) (string, error) {
			return createSourceArchive(root, root)
		},
		"working tree": func(root string) (string, error) {
			return createSourceArchiveIncludingWorkingTree(root, root, true)
		},
	} {
		t.Run(name, func(t *testing.T) {
			archivePath, err := archive(repoRoot)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(archivePath)
			files := readTarGz(t, archivePath)
			for _, path := range []string{"app/main.txt", "docs/keep.md"} {
				if _, ok := files[path]; !ok {
					t.Errorf("archive missing %q", path)
				}
			}
			for _, path := range []string{"web/old.txt", "web/new.txt", "docs/drop.md", "docs/staged.md"} {
				if _, ok := files[path]; ok {
					t.Errorf("archive included ignored file %q", path)
				}
			}
		})
	}
}

func TestSourceArchivesUseEachProjectRevylignore(t *testing.T) {
	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	iosRoot := filepath.Join(repoRoot, "apps", "ios")
	androidRoot := filepath.Join(repoRoot, "apps", "android")
	fallbackRoot := filepath.Join(repoRoot, "apps", "other")
	for _, root := range []string{iosRoot, androidRoot, fallbackRoot} {
		if err := os.MkdirAll(filepath.Join(root, ".revyl"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(root, ".revyl", "config.yaml"), "project:\n  name: mobile\n")
	}
	writeFile(t, filepath.Join(repoRoot, ".revylignore"), "/apps/ios/\n")
	writeFile(t, filepath.Join(iosRoot, ".revylignore"), "/apps/android/\n")
	writeFile(t, filepath.Join(androidRoot, ".revylignore"), "/apps/ios/\n")
	writeFile(t, filepath.Join(iosRoot, "main.txt"), "ios")
	writeFile(t, filepath.Join(androidRoot, "main.txt"), "android")
	writeFile(t, filepath.Join(fallbackRoot, "main.txt"), "other")
	writeFile(t, filepath.Join(repoRoot, "shared.txt"), "shared")
	runGit(t, repoRoot, "add", ".")
	runGit(t, repoRoot, "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "fixture")

	for _, project := range []struct {
		name, root, included, excluded string
	}{
		{"ios", iosRoot, "apps/ios/main.txt", "apps/android/main.txt"},
		{"android", androidRoot, "apps/android/main.txt", "apps/ios/main.txt"},
		{"root fallback", fallbackRoot, "apps/other/main.txt", "apps/ios/main.txt"},
	} {
		for _, archive := range []struct {
			name string
			make func(string, string) (string, error)
		}{
			{"committed", createSourceArchive},
			{"working tree", func(worktreeRoot, projectRoot string) (string, error) {
				return createSourceArchiveIncludingWorkingTree(worktreeRoot, projectRoot, true)
			}},
		} {
			t.Run(project.name+"/"+archive.name, func(t *testing.T) {
				archivePath, err := archive.make(repoRoot, project.root)
				if err != nil {
					t.Fatal(err)
				}
				defer os.Remove(archivePath)
				files := readTarGz(t, archivePath)
				for _, path := range []string{project.included, "shared.txt"} {
					if _, ok := files[path]; !ok {
						t.Errorf("archive missing %q", path)
					}
				}
				if _, ok := files[project.excluded]; ok {
					t.Errorf("archive included sibling app %q", project.excluded)
				}
			})
		}
	}
}

func TestCommittedSourceArchiveUsesCommittedRevylignore(t *testing.T) {
	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	for _, dir := range []string{"app", "web"} {
		if err := os.MkdirAll(filepath.Join(repoRoot, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(repoRoot, ".revylignore"), "/web/\n")
	writeFile(t, filepath.Join(repoRoot, "app", "main.txt"), "app")
	writeFile(t, filepath.Join(repoRoot, "web", "main.txt"), "web")
	runGit(t, repoRoot, "add", ".")
	runGit(t, repoRoot, "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "fixture")

	for name, changeRules := range map[string]func(){
		"edited": func() { writeFile(t, filepath.Join(repoRoot, ".revylignore"), "/app/\n") },
		"deleted": func() {
			if err := os.Remove(filepath.Join(repoRoot, ".revylignore")); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			changeRules()
			archivePath, err := createSourceArchive(repoRoot, repoRoot)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(archivePath)
			files := readTarGz(t, archivePath)
			if _, ok := files["app/main.txt"]; !ok {
				t.Error("committed app file missing from archive")
			}
			if _, ok := files["web/main.txt"]; ok {
				t.Error("committed ignore rule did not exclude web file")
			}
		})
	}
}

func TestGitHubPRRemoteBuildUsesUploadCIIdentity(t *testing.T) {
	const appID = "00000000-0000-4000-8000-000000000001"
	prHeadSHA := strings.Repeat("a", 40)
	for _, test := range []struct {
		name   string
		source config.BuildSource
	}{
		{name: "working tree archive"},
		{name: "git commit", source: config.BuildSource{Type: "git", RepoURL: "https://github.com/acme/mobile", Ref: prHeadSHA}},
		{name: "git branch", source: config.BuildSource{Type: "git", RepoURL: "https://github.com/acme/mobile", Ref: "main"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repoRoot := t.TempDir()
			runGit(t, repoRoot, "init")
			writeFile(t, filepath.Join(repoRoot, "main.txt"), "committed")
			runGit(t, repoRoot, "add", ".")
			runGit(t, repoRoot, "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "fixture")
			writeFile(t, filepath.Join(repoRoot, "main.txt"), "generated modification")
			writeFile(t, filepath.Join(repoRoot, "build-info.json"), "{}")
			eventPath := filepath.Join(t.TempDir(), "event.json")
			writeFile(t, eventPath, `{"pull_request":{"number":42,"head":{"sha":"`+prHeadSHA+`"}}}`)
			t.Setenv("GITHUB_ACTIONS", "true")
			t.Setenv("GITHUB_EVENT_PATH", eventPath)
			t.Setenv("GITHUB_REPOSITORY", "acme/mobile")
			t.Setenv("GITHUB_SHA", strings.Repeat("b", 40))
			uploadMetadata := build.CollectMetadata(repoRoot, "", "android", 0)
			var submitted *api.RemoteBuildRequest
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/" + appID:
					_ = json.NewEncoder(w).Encode(api.App{ID: appID, Platform: "Android", LatestVersion: "1.0", VersionsCount: 1})
				case "/api/v1/apps/remote/upload-url":
					_ = json.NewEncoder(w).Encode(api.RemoteBuildSourceUploadResponse{UploadUrl: server.URL + "/source", SourceKey: "source-key"})
				case "/source":
					if r.Header.Get("X-CI-System") != "" {
						t.Error("CI identity leaked to source storage")
					}
				case "/api/v1/apps/remote":
					for header, metadataKey := range map[string]string{
						"X-CI-Commit-SHA": "scm_head_sha",
						"X-CI-Repository": "scm_repo",
						"X-CI-System":     "ci_system",
					} {
						if r.Header.Get(header) != uploadMetadata[metadataKey] {
							t.Errorf("%s = %q, upload metadata = %v", header, r.Header.Get(header), uploadMetadata[metadataKey])
						}
					}
					if r.Header.Get("X-CI-PR-Number") != "42" || r.Header.Get("X-CI-Commit-SHA") != prHeadSHA {
						t.Error("remote build lost the PR identity")
					}
					if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
						t.Error(err)
					}
					_, _ = w.Write([]byte(`{"build_job_id":"job-1"}`))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			output := captureStdout(t, func() {
				err := runRemoteBuildWithOptions(newBuildTestCommand(), "test-key", remoteBuildOptions{
					ProjectRoot: repoRoot, WorktreeRoot: repoRoot, JSON: true, IncludeDirty: true,
					Resolved: &remoteBuildPlatformConfig{AppID: appID, Platform: "android", Command: "true", Output: "app.apk", Source: test.source},
				})
				if err != nil {
					t.Fatal(err)
				}
			})
			assertJSONString(t, parseJSON(t, output), "build_job_id", "job-1")
			if submitted == nil {
				t.Fatal("remote build was not submitted")
			}
			if test.source.Type == "git" {
				source, err := submitted.Source.AsRemoteBuildGitSource()
				if err != nil || source.Ref == nil || *source.Ref != test.source.Ref || source.PatchKey == nil {
					t.Fatalf("Git source ref or working tree patch was lost: %+v, %v", source, err)
				}
			} else {
				source, err := submitted.Source.AsRemoteBuildArchiveSource()
				if err != nil || source.Key != "source-key" {
					t.Fatalf("source archive was lost: %+v, %v", source, err)
				}
			}
		})
	}
}

func TestRemoteBuildConfigIncludesSecretReferences(t *testing.T) {
	appID := uuid.MustParse("00000000-0000-0000-0000-000000000456")
	config, err := remoteBuildConfigFromResolved(appID, remoteBuildPlatformConfig{
		Platform: "ios",
		Command:  "xcodebuild",
		Output:   "build/App.app",
		Secrets:  []string{"EXPO_TOKEN"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if config.SecretRefs == nil || len(*config.SecretRefs) != 1 || (*config.SecretRefs)[0] != "EXPO_TOKEN" {
		t.Fatalf("SecretRefs = %#v, want EXPO_TOKEN", config.SecretRefs)
	}
	if config.Env != nil {
		t.Fatalf("Env = %#v, want nil", config.Env)
	}
}

func TestRemoteBuildConfigIncludesResolvedSourceSubdir(t *testing.T) {
	appID := uuid.MustParse("00000000-0000-0000-0000-000000000456")
	buildConfig, err := remoteBuildConfigFromResolved(appID, remoteBuildPlatformConfig{
		Platform:     "ios",
		Command:      "xcodebuild",
		Output:       "build/App.app",
		SourceSubdir: "apps/mobile",
	})
	if err != nil {
		t.Fatal(err)
	}

	if buildConfig.SourceSubdir == nil || *buildConfig.SourceSubdir != "apps/mobile" {
		t.Fatalf("SourceSubdir = %#v, want apps/mobile", buildConfig.SourceSubdir)
	}
}

func TestRemoteBuildConfigOmitsRepositoryRootSourceSubdir(t *testing.T) {
	appID := uuid.MustParse("00000000-0000-0000-0000-000000000456")
	buildConfig, err := remoteBuildConfigFromResolved(appID, remoteBuildPlatformConfig{
		Platform:     "ios",
		Command:      "xcodebuild",
		Output:       "build/App.app",
		SourceSubdir: ".",
	})
	if err != nil {
		t.Fatal(err)
	}

	if buildConfig.SourceSubdir != nil {
		t.Fatalf("SourceSubdir = %#v, want nil for repository root", buildConfig.SourceSubdir)
	}
}

func TestRemoteBuildConfigExplicitGitSubdirOverridesResolvedProject(t *testing.T) {
	appID := uuid.MustParse("00000000-0000-0000-0000-000000000456")
	buildConfig, err := remoteBuildConfigFromResolved(appID, remoteBuildPlatformConfig{
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
	if err != nil {
		t.Fatal(err)
	}

	if buildConfig.SourceSubdir == nil || *buildConfig.SourceSubdir != "clients/mobile" {
		t.Fatalf("SourceSubdir = %#v, want clients/mobile", buildConfig.SourceSubdir)
	}
}

func TestRemoteBuildConfigPreservesEmptyExplicitGitSubdir(t *testing.T) {
	appID := uuid.MustParse("00000000-0000-0000-0000-000000000456")
	buildConfig, err := remoteBuildConfigFromResolved(appID, remoteBuildPlatformConfig{
		Platform:     "ios",
		Command:      "xcodebuild",
		Output:       "build/App.app",
		SourceSubdir: "apps/mobile",
		Source: config.BuildSource{
			Type:    "git",
			RepoURL: "https://example.com/example/repository.git",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if buildConfig.SourceSubdir != nil {
		t.Fatalf("SourceSubdir = %#v, want nil for an explicit root Git source", buildConfig.SourceSubdir)
	}
}

func TestRemoteBuildStepsRejectUnencodableTypedStepInputs(t *testing.T) {
	items := []config.BuildStepItem{{Step: map[string]any{"ios-signing": map[string]any{"certificate": math.NaN()}}}}
	if _, err := remoteBuildStepsFromItems("build", items); err == nil || !strings.Contains(err.Error(), "build step 1: encode ios-signing inputs") {
		t.Fatalf("remoteBuildStepsFromItems() error = %v, want encode failure", err)
	}
}
