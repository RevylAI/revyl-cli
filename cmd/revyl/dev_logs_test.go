package main

import (
	"context"
	"encoding/json"
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

	go func() {
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
