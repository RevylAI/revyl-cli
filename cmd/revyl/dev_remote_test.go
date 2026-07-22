package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/devloop"
	mcppkg "github.com/revyl/cli/internal/mcp"
)

type fakeRemoteDevInstaller struct {
	requests    []mcppkg.DeviceInstallRequest
	workerPaths []string
	results     []*mcppkg.WorkerActionResponse
	errors      []error
}

type fakeRemoteDevBuildDetailResolver struct {
	detail *api.BuildVersionDetail
	err    error
}

// InstallAppForSession records an install and returns the configured result.
//
// Parameters:
//   - ctx: Context controlling the fake call.
//   - index: Session index targeted by the caller.
//   - req: Typed install request to record.
//
// Returns:
//   - *mcppkg.WorkerActionResponse: Configured result for this invocation.
//   - error: Configured error for this invocation.
func (f *fakeRemoteDevInstaller) InstallAppForSession(
	ctx context.Context,
	index int,
	req mcppkg.DeviceInstallRequest,
) (*mcppkg.WorkerActionResponse, error) {
	_ = ctx
	_ = index
	call := len(f.requests)
	f.requests = append(f.requests, req)
	if call < len(f.errors) && f.errors[call] != nil {
		return nil, f.errors[call]
	}
	return f.results[call], nil
}

// WorkerRequestForSession records a worker request and returns a successful action.
//
// Parameters:
//   - ctx: Context controlling the fake call.
//   - sessionIndex: Session index targeted by the caller.
//   - path: Worker endpoint requested by the caller.
//   - body: Typed request payload sent to the worker.
//
// Returns:
//   - []byte: Successful worker response.
//   - error: Always nil.
func (f *fakeRemoteDevInstaller) WorkerRequestForSession(
	ctx context.Context,
	sessionIndex int,
	path string,
	body interface{},
) ([]byte, error) {
	_ = ctx
	_ = sessionIndex
	_ = body
	f.workerPaths = append(f.workerPaths, path)
	return []byte(`{"status":"success"}`), nil
}

// GetBuildVersionDownloadURL returns the configured remote build artifact.
//
// Parameters:
//   - ctx: Context controlling the fake call.
//   - versionID: Build version requested by the caller.
//
// Returns:
//   - *api.BuildVersionDetail: Configured artifact metadata.
//   - error: Configured resolver error.
func (f *fakeRemoteDevBuildDetailResolver) GetBuildVersionDownloadURL(
	ctx context.Context,
	versionID string,
) (*api.BuildVersionDetail, error) {
	_ = ctx
	_ = versionID
	return f.detail, f.err
}

func TestInstallRemoteDevBuild_OrdersSeedAndFreshInstalls(t *testing.T) {
	installer := &fakeRemoteDevInstaller{
		results: []*mcppkg.WorkerActionResponse{
			{
				Success:  true,
				Action:   "install",
				BundleID: "com.whop.ios",
			},
			{
				Success:  true,
				Action:   "install",
				BundleID: "com.whop.ios",
			},
		},
	}
	session := &mcppkg.DeviceSession{Index: 7}

	seedBundleID, _, err := installRemoteDevBuild(
		context.Background(),
		installer,
		session,
		&api.BuildVersionDetail{DownloadURL: "https://example.test/seed.zip"},
		"",
	)
	if err != nil {
		t.Fatalf("seed install error = %v", err)
	}

	freshBundleID, _, err := installRemoteDevBuild(
		context.Background(),
		installer,
		session,
		&api.BuildVersionDetail{DownloadURL: "https://example.test/fresh.zip"},
		seedBundleID,
	)
	if err != nil {
		t.Fatalf("fresh install error = %v", err)
	}

	if len(installer.requests) != 2 {
		t.Fatalf("install requests = %d, want 2", len(installer.requests))
	}
	if installer.requests[0].AppURL != "https://example.test/seed.zip" {
		t.Fatalf("seed AppURL = %q", installer.requests[0].AppURL)
	}
	if installer.requests[1].AppURL != "https://example.test/fresh.zip" {
		t.Fatalf("fresh AppURL = %q", installer.requests[1].AppURL)
	}
	if installer.requests[1].BundleID != seedBundleID {
		t.Fatalf(
			"fresh BundleID = %q, want seeded bundle %q",
			installer.requests[1].BundleID,
			seedBundleID,
		)
	}
	if installer.requests[0].InstallMode != mcppkg.DeviceInstallModeFast ||
		installer.requests[1].InstallMode != mcppkg.DeviceInstallModeFast {
		t.Fatalf("install modes = %q, %q; want fast", installer.requests[0].InstallMode, installer.requests[1].InstallMode)
	}
	if freshBundleID != "com.whop.ios" {
		t.Fatalf("fresh BundleID result = %q", freshBundleID)
	}
}

func TestInstallRemoteDevBuild_ReturnsTerminalWorkerFailure(t *testing.T) {
	installer := &fakeRemoteDevInstaller{
		results: []*mcppkg.WorkerActionResponse{
			{
				Success: false,
				Action:  "install",
				Error:   "simctl install failed",
			},
		},
	}

	_, _, err := installRemoteDevBuild(
		context.Background(),
		installer,
		&mcppkg.DeviceSession{Index: 1},
		&api.BuildVersionDetail{DownloadURL: "https://example.test/broken.zip"},
		"",
	)
	if err == nil {
		t.Fatal("installRemoteDevBuild() error = nil, want terminal failure")
	}
	if !strings.Contains(err.Error(), "simctl install failed") {
		t.Fatalf("install error = %q", err)
	}
}

func TestDevStatusRemoteBuildProgressSinkPreservesRemoteMetadata(t *testing.T) {
	cwd := t.TempDir()
	status := devStatus{
		PID:           os.Getpid(),
		SessionID:     "session-1",
		BuildMode:     "remote",
		InstalledSeed: true,
		SeededVersion: "1.2.3",
		RebuildCount:  2,
		Build: &devloop.BuildStatus{
			State:         devloop.BuildStateQueued,
			RemoteJobID:   "job-123",
			SeededVersion: "1.2.3",
		},
		LastRebuild: &devRebuildInfo{
			Status:      "running",
			Seq:         2,
			RemoteJobID: "job-123",
			Logs:        []devRebuildLogEntry{newDevRebuildLog("info", "Remote build queued")},
		},
	}
	writeDevLogsTestStatus(t, cwd, "default", status)
	statusPath := devCtxStatusPath(cwd, "default")
	sink := newDevStatusRemoteBuildProgressSink(statusPath)

	publishRemoteDevBuildProgress(sink, remoteDevBuildProgress{
		State: devloop.BuildStateInstalling, Phase: "device_install", Message: "Installing remote build on device",
	})

	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	var updated devStatus
	if err := json.Unmarshal(data, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Build == nil ||
		updated.Build.State != devloop.BuildStateInstalling ||
		updated.Build.Phase != "device_install" {
		t.Fatalf("build progress = %+v", updated.Build)
	}
	if updated.Build.RemoteJobID != "job-123" || updated.LastRebuild.RemoteJobID != "job-123" {
		t.Fatalf("remote job metadata was not preserved: build=%+v rebuild=%+v", updated.Build, updated.LastRebuild)
	}
	if !updated.InstalledSeed || updated.SeededVersion != "1.2.3" {
		t.Fatalf("seed metadata = (%v, %q)", updated.InstalledSeed, updated.SeededVersion)
	}
	if got := updated.LastRebuild.Logs[len(updated.LastRebuild.Logs)-1].Message; got != "Installing remote build on device" {
		t.Fatalf("last progress log = %q", got)
	}
}

func TestRemoteBuildProgressFromStatusPreservesBackendPhase(t *testing.T) {
	phase := "xcodebuild"
	progress := remoteBuildProgressFromStatus(&api.RemoteBuildStatusResponse{
		Status: "building",
		Phase:  &phase,
	})

	if progress.State != devloop.BuildStateBuilding || progress.Phase != phase {
		t.Fatalf("remote progress = %+v", progress)
	}
}

func TestRemoteBuildProgressFromStatusMovesSuccessfulBuildToInstalling(t *testing.T) {
	phase := "artifact_upload"
	progress := remoteBuildProgressFromStatus(&api.RemoteBuildStatusResponse{
		Status: "success",
		Phase:  &phase,
	})

	if progress.State != devloop.BuildStateInstalling ||
		progress.Phase != phase ||
		progress.Message != "Remote build completed" {
		t.Fatalf("remote progress = %+v", progress)
	}
}

func TestInstallAndLaunchRemoteDevBuildPublishesDeviceProgress(t *testing.T) {
	resolver := &fakeRemoteDevBuildDetailResolver{
		detail: &api.BuildVersionDetail{
			DownloadURL: "https://example.test/fresh.zip",
			PackageName: "com.example.app",
			Version:     "1.2.3",
		},
	}
	deviceMgr := &fakeRemoteDevInstaller{
		results: []*mcppkg.WorkerActionResponse{{
			Success:  true,
			Action:   "install",
			BundleID: "com.example.app",
		}},
	}
	bundleID := ""
	var progress []remoteDevBuildProgress

	err := installAndLaunchRemoteDevBuild(
		context.Background(),
		resolver,
		deviceMgr,
		&mcppkg.DeviceSession{Index: 7, SessionID: "session-1"},
		"ios",
		remoteDevBuildResult{
			jobID:     "job-1",
			versionID: "version-1",
			version:   "1.2.3",
			duration:  time.Second,
		},
		&bundleID,
		filepath.Join(t.TempDir(), "status.json"),
		"https://example.test/viewer",
		func(update remoteDevBuildProgress) {
			progress = append(progress, update)
		},
	)
	if err != nil {
		t.Fatalf("installAndLaunchRemoteDevBuild() error = %v", err)
	}
	if len(progress) != 2 {
		t.Fatalf("progress updates = %+v, want install and launch", progress)
	}
	if progress[0].State != devloop.BuildStateInstalling || progress[0].Phase != "device_install" {
		t.Fatalf("install progress = %+v", progress[0])
	}
	if progress[1].State != devloop.BuildStateLaunching || progress[1].Phase != "app_launch" {
		t.Fatalf("launch progress = %+v", progress[1])
	}
	if len(deviceMgr.workerPaths) != 1 || deviceMgr.workerPaths[0] != "/launch" {
		t.Fatalf("worker paths = %+v, want launch", deviceMgr.workerPaths)
	}
}

func TestWaitRemoteDevBuildPublishesPhaseAndVersion(t *testing.T) {
	withFastRemoteBuildPolling(t)
	versionID := "version-123"
	version := "1.2.3"
	phase := "artifact_upload"
	server := remoteBuildStatusServer(t, api.RemoteBuildStatusResponse{
		Status:    "success",
		VersionId: &versionID,
		Version:   &version,
		Phase:     &phase,
	})
	defer server.Close()

	var progress []remoteDevBuildProgress
	result, err := waitRemoteDevBuild(
		context.Background(),
		api.NewClientWithBaseURL("test-key", server.URL),
		remoteDevBuildJob{jobID: "job-1", started: time.Now()},
		t.TempDir(),
		func(update remoteDevBuildProgress) {
			progress = append(progress, update)
		},
	)
	if err != nil {
		t.Fatalf("waitRemoteDevBuild(): %v", err)
	}
	if result.jobID != "job-1" || result.versionID != versionID || result.version != version {
		t.Fatalf("remote build result = %+v", result)
	}
	if len(progress) != 1 ||
		progress[0].State != devloop.BuildStateInstalling ||
		progress[0].Phase != phase ||
		progress[0].Message != "Remote build completed" {
		t.Fatalf("remote progress = %+v", progress)
	}
}

func TestValidateRemoteDevStartFlags(t *testing.T) {
	oldPlatform := devStartPlatform
	oldNoBuild := devStartNoBuild
	oldBuildVersionID := devStartBuildVerID
	oldTunnel := devStartTunnelURL
	defer func() {
		devStartPlatform = oldPlatform
		devStartNoBuild = oldNoBuild
		devStartBuildVerID = oldBuildVersionID
		devStartTunnelURL = oldTunnel
	}()

	tests := []struct {
		name    string
		setup   func()
		wantErr string
	}{
		{
			name: "ios valid",
			setup: func() {
				devStartPlatform = "ios"
				devStartNoBuild = false
				devStartBuildVerID = ""
				devStartTunnelURL = ""
			},
		},
		{
			name: "android valid",
			setup: func() {
				devStartPlatform = "android"
				devStartNoBuild = false
				devStartBuildVerID = ""
				devStartTunnelURL = ""
			},
		},
		{
			name: "no build rejected",
			setup: func() {
				devStartPlatform = "ios"
				devStartNoBuild = true
				devStartBuildVerID = ""
				devStartTunnelURL = ""
			},
			wantErr: "--no-build",
		},
		{
			name: "build version allowed as seed source",
			setup: func() {
				devStartPlatform = "ios"
				devStartNoBuild = false
				devStartBuildVerID = "bv_123"
				devStartTunnelURL = ""
			},
		},
		{
			name: "tunnel rejected",
			setup: func() {
				devStartPlatform = "ios"
				devStartNoBuild = false
				devStartBuildVerID = ""
				devStartTunnelURL = "https://example.ngrok.app"
			},
			wantErr: "--tunnel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()
			err := validateRemoteDevStartFlags()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateRemoteDevStartFlags() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateRemoteDevStartFlags() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestSeedRequested(t *testing.T) {
	oldSeedLatest := devStartSeedLatest
	oldBuildVersionID := devStartBuildVerID
	defer func() {
		devStartSeedLatest = oldSeedLatest
		devStartBuildVerID = oldBuildVersionID
	}()

	tests := []struct {
		name        string
		seedLatest  bool
		buildVerID  string
		wantSeeding bool
	}{
		{name: "default no seed", seedLatest: false, buildVerID: "", wantSeeding: false},
		{name: "seed-latest flag", seedLatest: true, buildVerID: "", wantSeeding: true},
		{name: "explicit build version seeds", seedLatest: false, buildVerID: "bv_123", wantSeeding: true},
		{name: "blank build version ignored", seedLatest: false, buildVerID: "   ", wantSeeding: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			devStartSeedLatest = tt.seedLatest
			devStartBuildVerID = tt.buildVerID
			if got := seedRequested(); got != tt.wantSeeding {
				t.Fatalf("seedRequested() = %v, want %v", got, tt.wantSeeding)
			}
		})
	}
}

func TestRemoteDevTriggerRequestAllowsFastlane(t *testing.T) {
	appID := uuid.MustParse("00000000-0000-0000-0000-000000000456")
	req, err := remoteDevTriggerRequest(
		appID,
		"org/00000000-0000-0000-0000-000000000123/build-sources/app/source.tar.gz",
		"ios",
		"abc123",
		config.BuildPlatform{
			Command: "bundle exec fastlane build_simulator_debug",
			Output:  "build/Example.app.zip",
			AppID:   "00000000-0000-0000-0000-000000000456",
			Setup:   "bash .revyl/setup-ios-remote.sh",
		},
		nil,
	)
	if err != nil {
		t.Fatalf("remoteDevTriggerRequest(): %v", err)
	}

	if len(*req.Config.Steps) != 3 || *(*req.Config.Steps)[2].Command != "bundle exec fastlane build_simulator_debug" {
		t.Fatalf("Config.Steps = %#v", *req.Config.Steps)
	}
	if len(*req.Config.Artifacts) != 1 || (*req.Config.Artifacts)[0].Path != "build/Example.app.zip" {
		t.Fatalf("Config.Artifacts = %#v", *req.Config.Artifacts)
	}
	if *(*req.Config.Steps)[1].Command != "bash .revyl/setup-ios-remote.sh" {
		t.Fatalf("setup step = %#v", (*req.Config.Steps)[1])
	}
}

func TestRemoteDevTriggerRequestCarriesMultipleBuildCommands(t *testing.T) {
	appID := uuid.MustParse("00000000-0000-0000-0000-000000000456")
	req, err := remoteDevTriggerRequest(
		appID,
		"org/00000000-0000-0000-0000-000000000123/build-sources/app/source.tar.gz",
		"ios",
		"abc123",
		config.BuildPlatform{
			Commands: []string{
				"npm ci",
				"bundle exec fastlane build_simulator_debug",
			},
			Output: "build/Example.app.zip",
			AppID:  "00000000-0000-0000-0000-000000000456",
		},
		nil,
	)
	if err != nil {
		t.Fatalf("remoteDevTriggerRequest(): %v", err)
	}

	if len(*req.Config.Steps) != 3 {
		t.Fatalf("Config.Steps = %#v", *req.Config.Steps)
	}
	if *(*req.Config.Steps)[1].Command != "npm ci" {
		t.Fatalf("first build step = %#v", (*req.Config.Steps)[1])
	}
	if *(*req.Config.Steps)[2].Command != "bundle exec fastlane build_simulator_debug" {
		t.Fatalf("second build step = %#v", (*req.Config.Steps)[2])
	}
}

func TestCreateSourceArchiveIncludingWorkingTree(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")

	if err := os.MkdirAll(filepath.Join(dir, "SwiftMinimal"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "build"), 0755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(dir, ".gitignore"), "build/\nignored.txt\n")
	writeFile(t, filepath.Join(dir, "SwiftMinimal", "ContentView.swift"), "old marker\n")
	runGit(t, dir, "add", ".gitignore", "SwiftMinimal/ContentView.swift")

	writeFile(t, filepath.Join(dir, "SwiftMinimal", "ContentView.swift"), "dirty marker\n")
	writeFile(t, filepath.Join(dir, "SwiftMinimal", "NewView.swift"), "new file\n")
	writeFile(t, filepath.Join(dir, "build", "ignored.o"), "generated\n")
	writeFile(t, filepath.Join(dir, "ignored.txt"), "ignored\n")

	archivePath, err := createSourceArchiveIncludingWorkingTree(dir)
	if err != nil {
		t.Fatalf("createSourceArchiveIncludingWorkingTree() error = %v", err)
	}
	defer os.Remove(archivePath)

	files := readTarGz(t, archivePath)
	if got := files["SwiftMinimal/ContentView.swift"]; got != "dirty marker\n" {
		t.Fatalf("ContentView.swift = %q, want dirty working-tree content", got)
	}
	if got := files["SwiftMinimal/NewView.swift"]; got != "new file\n" {
		t.Fatalf("NewView.swift = %q, want untracked unignored file", got)
	}
	if _, ok := files["build/ignored.o"]; ok {
		t.Fatal("archive included ignored build artifact")
	}
	if _, ok := files["ignored.txt"]; ok {
		t.Fatal("archive included ignored file")
	}
}

func TestCreateSourceArchiveIncludingWorkingTree_FallsBackForIgnoredSandbox(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	writeFile(t, filepath.Join(dir, ".gitignore"), "sandbox/\n")

	sandbox := filepath.Join(dir, "sandbox")
	if err := os.MkdirAll(filepath.Join(sandbox, "SwiftMinimal"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sandbox, ".revyl", "dev-sessions"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sandbox, "build"), 0755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(sandbox, ".revyl", "config.yaml"), "project:\n  name: sandbox\n")
	writeFile(t, filepath.Join(sandbox, ".revyl", ".dev-status.json"), "{}\n")
	writeFile(t, filepath.Join(sandbox, ".revyl", "dev-sessions", "default.json"), "{}\n")
	writeFile(t, filepath.Join(sandbox, "SwiftMinimal", "ContentView.swift"), "standalone source\n")
	writeFile(t, filepath.Join(sandbox, "build", "generated.o"), "generated\n")

	archivePath, err := createSourceArchiveIncludingWorkingTree(sandbox)
	if err != nil {
		t.Fatalf("createSourceArchiveIncludingWorkingTree() error = %v", err)
	}
	defer os.Remove(archivePath)

	files := readTarGz(t, archivePath)
	if got := files["SwiftMinimal/ContentView.swift"]; got != "standalone source\n" {
		t.Fatalf("ContentView.swift = %q, want standalone source", got)
	}
	if got := files[".revyl/config.yaml"]; got != "project:\n  name: sandbox\n" {
		t.Fatalf(".revyl/config.yaml = %q, want config included", got)
	}
	if _, ok := files[".revyl/.dev-status.json"]; ok {
		t.Fatal("archive included dev status runtime file")
	}
	if _, ok := files[".revyl/dev-sessions/default.json"]; ok {
		t.Fatal("archive included dev session runtime file")
	}
	if _, ok := files["build/generated.o"]; ok {
		t.Fatal("archive included generated build output")
	}
}

func TestRevylRemoteDevloopTemplateShape(t *testing.T) {
	root := filepath.Clean("../../../revyl-remote-devloop")
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			t.Skip("revyl-remote-devloop is a local ignored sandbox template")
		}
		t.Fatalf("failed to inspect remote devloop template: %v", err)
	}
	required := []string{
		"README.md",
		".gitignore",
		".revyl/config.yaml",
		"SwiftMinimal.xcodeproj/project.pbxproj",
		"SwiftMinimal/ContentView.swift",
		"SwiftMinimal/SwiftMinimalApp.swift",
	}
	for _, rel := range required {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("required template file %s missing: %v", rel, err)
		}
	}

	configData, err := os.ReadFile(filepath.Join(root, ".revyl/config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	configText := string(configData)
	for _, want := range []string{"xcodebuild", "iphonesimulator", "SwiftMinimal.app"} {
		if !strings.Contains(configText, want) {
			t.Fatalf("config missing %q:\n%s", want, configText)
		}
	}

	ignoreData, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	ignoreText := string(ignoreData)
	for _, want := range []string{"build/", "DerivedData/"} {
		if !strings.Contains(ignoreText, want) {
			t.Fatalf(".gitignore missing %q:\n%s", want, ignoreText)
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func readTarGz(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	files := map[string]string{}
	for {
		header, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name] = string(data)
	}
	return files
}
