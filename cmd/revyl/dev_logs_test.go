package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeDevLogsTestStatus writes a status snapshot for build-job resolution tests.
func writeDevLogsTestStatus(t *testing.T, cwd, ctxName string, status devStatus) {
	t.Helper()
	statusPath := devCtxStatusPath(cwd, ctxName)
	if err := os.MkdirAll(filepath.Dir(statusPath), 0755); err != nil {
		t.Fatal(err)
	}
	writeDevStatusSnapshot(statusPath, status)
}

// writeDevLogsTestRunningContext records the current test process as a live dev context.
//
// Parameters:
//   - t: Test instance
//   - cwd: Project root
//   - ctxName: Dev context name
func writeDevLogsTestRunningContext(t *testing.T, cwd, ctxName string) {
	t.Helper()
	startedAtNano := time.Now().UnixNano()
	devContext := &DevContext{
		Name:          ctxName,
		PID:           os.Getpid(),
		StartedAtNano: startedAtNano,
		State:         devContextStateRunning,
	}
	if err := saveDevContext(cwd, devContext); err != nil {
		t.Fatal(err)
	}
	if err := writeDevCtxPIDFile(devCtxPIDPath(cwd, ctxName), os.Getpid(), startedAtNano); err != nil {
		t.Fatal(err)
	}
}

func TestResolveDevBuildJobID_Immediate(t *testing.T) {
	cwd := t.TempDir()
	writeDevLogsTestStatus(t, cwd, "default", devStatus{
		BuildMode: "remote",
		LastRebuild: &devRebuildInfo{
			Status:      "running",
			RemoteJobID: "job-123",
		},
	})

	jobID, err := resolveDevBuildJobID(context.Background(), cwd, "default", false, time.Second)
	if err != nil {
		t.Fatalf("resolveDevBuildJobID() error = %v", err)
	}
	if jobID != "job-123" {
		t.Fatalf("resolveDevBuildJobID() = %q, want job-123", jobID)
	}
}

func TestResolveDevBuildJobID_FollowWaitsForRegistration(t *testing.T) {
	previousInterval := devLogsJobPollInterval
	devLogsJobPollInterval = time.Millisecond
	t.Cleanup(func() { devLogsJobPollInterval = previousInterval })

	cwd := t.TempDir()
	// `revyl dev logs --follow` only runs against a live dev context, and the
	// goroutine below replaces the status file while the resolver polls it.
	// Without registering the context as running, the resolver cannot treat a
	// concurrent-replace read failure as transient, which made this flaky on
	// Windows where os.Rename is not atomic for readers.
	writeDevLogsTestRunningContext(t, cwd, "default")
	status := devStatus{
		PID:       os.Getpid(),
		SessionID: "session-1",
		BuildMode: "remote",
		LastRebuild: &devRebuildInfo{
			Status: "running",
			Seq:    1,
		},
	}
	writeDevLogsTestStatus(t, cwd, "default", status)
	statusPath := devCtxStatusPath(cwd, "default")

	registrationDone := make(chan struct{})
	go func() {
		defer close(registrationDone)
		time.Sleep(10 * time.Millisecond)
		setDevStatusSeedInstalled(statusPath, "1.2.3")
		setDevStatusRemoteJobID(statusPath, "job-delayed")
	}()

	jobID, err := resolveDevBuildJobID(context.Background(), cwd, "default", true, time.Second)
	if err != nil {
		t.Fatalf("resolveDevBuildJobID() error = %v", err)
	}
	if jobID != "job-delayed" {
		t.Fatalf("resolveDevBuildJobID() = %q, want job-delayed", jobID)
	}
	select {
	case <-registrationDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for build registration writer")
	}
	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	var updated devStatus
	if err := json.Unmarshal(data, &updated); err != nil {
		t.Fatal(err)
	}
	if !updated.InstalledSeed || updated.SeededVersion != "1.2.3" {
		t.Fatalf("seed metadata = (%v, %q), want (true, 1.2.3)", updated.InstalledSeed, updated.SeededVersion)
	}
}

func TestResolveDevBuildJobID_FollowRetriesTransientMissingStatus(t *testing.T) {
	previousInterval := devLogsJobPollInterval
	devLogsJobPollInterval = time.Millisecond
	t.Cleanup(func() { devLogsJobPollInterval = previousInterval })

	cwd := t.TempDir()
	writeDevLogsTestRunningContext(t, cwd, "default")
	statusPath := devCtxStatusPath(cwd, "default")

	statusWritten := make(chan struct{})
	go func() {
		defer close(statusWritten)
		time.Sleep(10 * time.Millisecond)
		writeDevStatusSnapshot(statusPath, devStatus{
			BuildMode: "remote",
			LastRebuild: &devRebuildInfo{
				Status:      "running",
				RemoteJobID: "job-after-gap",
			},
		})
	}()

	jobID, err := resolveDevBuildJobID(
		context.Background(),
		cwd,
		"default",
		true,
		time.Second,
	)
	if err != nil {
		t.Fatalf("resolveDevBuildJobID() error = %v", err)
	}
	if jobID != "job-after-gap" {
		t.Fatalf("resolveDevBuildJobID() = %q, want job-after-gap", jobID)
	}
	select {
	case <-statusWritten:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for transient status writer")
	}
}

func TestResolveDevBuildJobID_FollowRejectsMissingSession(t *testing.T) {
	previousInterval := devLogsJobPollInterval
	devLogsJobPollInterval = time.Millisecond
	t.Cleanup(func() { devLogsJobPollInterval = previousInterval })

	cwd := t.TempDir()

	_, err := resolveDevBuildJobID(
		context.Background(),
		cwd,
		"default",
		true,
		20*time.Millisecond,
	)
	if err == nil || !strings.Contains(err.Error(), "no dev status") {
		t.Fatalf("resolveDevBuildJobID() error = %v, want immediate missing-session error", err)
	}
}

func TestResolveDevBuildJobID_FollowTimesOut(t *testing.T) {
	previousInterval := devLogsJobPollInterval
	devLogsJobPollInterval = time.Millisecond
	t.Cleanup(func() { devLogsJobPollInterval = previousInterval })

	cwd := t.TempDir()
	writeDevLogsTestStatus(t, cwd, "default", devStatus{
		BuildMode: "remote",
		LastRebuild: &devRebuildInfo{
			Status: "running",
		},
	})

	_, err := resolveDevBuildJobID(context.Background(), cwd, "default", true, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "not available after") {
		t.Fatalf("resolveDevBuildJobID() error = %v, want timeout", err)
	}
}

func TestResolveDevBuildJobID_RejectsTerminalWithoutJobID(t *testing.T) {
	cwd := t.TempDir()
	writeDevLogsTestStatus(t, cwd, "default", devStatus{
		BuildMode: "remote",
		LastRebuild: &devRebuildInfo{
			Status: "build_failed",
		},
	})

	_, err := resolveDevBuildJobID(context.Background(), cwd, "default", true, time.Second)
	if err == nil || !strings.Contains(err.Error(), "ended with status") {
		t.Fatalf("resolveDevBuildJobID() error = %v, want terminal-status error", err)
	}
}

func TestResolveDevBuildJobID_RejectsLocalBuild(t *testing.T) {
	cwd := t.TempDir()
	writeDevLogsTestStatus(t, cwd, "default", devStatus{
		BuildMode: "local",
		LastRebuild: &devRebuildInfo{
			Status: "running",
		},
	})

	_, err := resolveDevBuildJobID(context.Background(), cwd, "default", true, time.Second)
	if err == nil || !strings.Contains(err.Error(), "local builds have no remote logs") {
		t.Fatalf("resolveDevBuildJobID() error = %v, want local-build error", err)
	}
}

func TestResolveDevBuildJobID_NonFollowDoesNotWait(t *testing.T) {
	cwd := t.TempDir()
	writeDevLogsTestStatus(t, cwd, "default", devStatus{
		BuildMode: "remote",
		LastRebuild: &devRebuildInfo{
			Status: "running",
		},
	})

	_, err := resolveDevBuildJobID(context.Background(), cwd, "default", false, time.Second)
	if err == nil || !strings.Contains(err.Error(), "use --follow") {
		t.Fatalf("resolveDevBuildJobID() error = %v, want --follow guidance", err)
	}
}

func TestSetDevStatusRemoteJobID_PreservesSeedAndLogs(t *testing.T) {
	cwd := t.TempDir()
	status := devStatus{
		PID:            os.Getpid(),
		SessionID:      "session-1",
		BuildMode:      "remote",
		InstalledSeed:  true,
		SeededVersion:  "1.2.3",
		RebuildCount:   1,
		DeltaCacheWarm: true,
		LastRebuild: &devRebuildInfo{
			Status: "running",
			Seq:    1,
			Logs:   []devRebuildLogEntry{newDevRebuildLog("info", "Rebuild requested")},
		},
	}
	writeDevLogsTestStatus(t, cwd, "default", status)
	statusPath := devCtxStatusPath(cwd, "default")

	setDevStatusRemoteJobID(statusPath, "job-123")

	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	var updated devStatus
	if err := json.Unmarshal(data, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.LastRebuild == nil || updated.LastRebuild.RemoteJobID != "job-123" {
		t.Fatalf("remote_job_id = %#v, want job-123", updated.LastRebuild)
	}
	if !updated.InstalledSeed || updated.SeededVersion != "1.2.3" {
		t.Fatalf("seed metadata = (%v, %q), want (true, 1.2.3)", updated.InstalledSeed, updated.SeededVersion)
	}
	if len(updated.LastRebuild.Logs) != 2 {
		t.Fatalf("logs = %#v, want existing and registration entries", updated.LastRebuild.Logs)
	}
}

func TestSetDevStatusSeedInstalled_PreservesRemoteJobAndRebuildState(t *testing.T) {
	cwd := t.TempDir()
	status := devStatus{
		PID:          os.Getpid(),
		SessionID:    "session-1",
		BuildMode:    "remote",
		RebuildCount: 1,
		LastRebuild: &devRebuildInfo{
			StartedAt: "2026-07-13T12:00:00Z",
			Status:    "running",
			Seq:       1,
			Logs:      []devRebuildLogEntry{newDevRebuildLog("info", "Rebuild requested")},
		},
	}
	writeDevLogsTestStatus(t, cwd, "default", status)
	statusPath := devCtxStatusPath(cwd, "default")
	setDevStatusRemoteJobID(statusPath, "job-123")

	setDevStatusSeedInstalled(statusPath, "1.2.3")

	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	var updated devStatus
	if err := json.Unmarshal(data, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.LastRebuild == nil || updated.LastRebuild.RemoteJobID != "job-123" {
		t.Fatalf("remote_job_id = %#v, want job-123", updated.LastRebuild)
	}
	if updated.LastRebuild.StartedAt != "2026-07-13T12:00:00Z" {
		t.Fatalf("started_at = %q, want original value", updated.LastRebuild.StartedAt)
	}
	if !updated.InstalledSeed || updated.SeededVersion != "1.2.3" {
		t.Fatalf("seed metadata = (%v, %q), want (true, 1.2.3)", updated.InstalledSeed, updated.SeededVersion)
	}
	if len(updated.LastRebuild.Logs) != 3 {
		t.Fatalf("logs = %#v, want existing, registration, and seed entries", updated.LastRebuild.Logs)
	}
}

// TestShouldRetryDevBuildStatusRead_TransientErrorsOnLiveContext pins the retry
// classification that keeps `--follow` alive across a status-file replacement.
//
// The writer swaps this file with os.Rename. That is atomic for POSIX readers,
// but on Windows a concurrent open can fail with a sharing violation or access
// denial rather than a not-exist gap, so classifying only os.ErrNotExist as
// transient made the resolver fail whenever it lost that race. The Windows
// errno values are not reproducible here, so assert the classification instead.
func TestShouldRetryDevBuildStatusRead_TransientErrorsOnLiveContext(t *testing.T) {
	cwd := t.TempDir()
	writeDevLogsTestRunningContext(t, cwd, "default")

	for _, tc := range []struct {
		name  string
		cause error
	}{
		{"not exist", os.ErrNotExist},
		{"permission denied", os.ErrPermission},
		{"sharing violation", errors.New("CreateFile: The process cannot access the file because it is being used by another process.")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := &devBuildStatusReadError{contextName: "default", cause: tc.cause}
			if !shouldRetryDevBuildStatusRead(cwd, "default", err) {
				t.Fatalf("shouldRetryDevBuildStatusRead(%v) = false, want true for a live context", tc.cause)
			}
		})
	}
}

// TestShouldRetryDevBuildStatusRead_DeadContextIsTerminal keeps the widened
// retry from masking a genuinely absent dev context.
func TestShouldRetryDevBuildStatusRead_DeadContextIsTerminal(t *testing.T) {
	cwd := t.TempDir()
	err := &devBuildStatusReadError{contextName: "default", cause: os.ErrNotExist}
	if shouldRetryDevBuildStatusRead(cwd, "default", err) {
		t.Fatal("shouldRetryDevBuildStatusRead() = true, want false when no dev context is registered")
	}
}

// TestShouldRetryDevBuildStatusRead_NonReadErrorIsTerminal keeps parse failures
// and other non-filesystem errors terminal.
func TestShouldRetryDevBuildStatusRead_NonReadErrorIsTerminal(t *testing.T) {
	cwd := t.TempDir()
	writeDevLogsTestRunningContext(t, cwd, "default")
	if shouldRetryDevBuildStatusRead(cwd, "default", errors.New("could not parse dev status")) {
		t.Fatal("shouldRetryDevBuildStatusRead() = true, want false for a non-read error")
	}
}
