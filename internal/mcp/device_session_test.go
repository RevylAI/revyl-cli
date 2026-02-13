package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// TestWsURLToHTTP: Table-driven test for WebSocket-to-HTTP URL conversion.
// ---------------------------------------------------------------------------

func TestWsURLToHTTP(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "ws with /ws/ path",
			input:    "ws://host:8080/ws/abc",
			expected: "http://host:8080",
		},
		{
			name:     "wss with /ws/ nested path",
			input:    "wss://host.com/ws/abc/123?token=xyz",
			expected: "https://host.com",
		},
		{
			name:     "http already - no /ws/ path",
			input:    "http://already-http",
			expected: "http://already-http",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "wss with no /ws/ path gets scheme replaced only",
			input:    "wss://host.com/other/path",
			expected: "https://host.com/other/path",
		},
		{
			name:     "ws with /ws/ at root",
			input:    "ws://worker-xyz.revyl.ai/ws/stream?token=abc",
			expected: "http://worker-xyz.revyl.ai",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := wsURLToHTTP(tt.input)
			if result != tt.expected {
				t.Errorf("wsURLToHTTP(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_GetActive_NoSession: GetActive returns nil when
// no session is active.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_GetActive_NoSession(t *testing.T) {
	mgr := &DeviceSessionManager{}

	session := mgr.GetActive()
	if session != nil {
		t.Errorf("expected nil session, got %+v", session)
	}
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_ResetIdleTimer_NoSession: ResetIdleTimer is a
// no-op when there's no active session (should not panic).
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_ResetIdleTimer_NoSession(t *testing.T) {
	mgr := &DeviceSessionManager{}

	// Should not panic
	mgr.ResetIdleTimer()
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_StopSession_NoSession: StopSession returns an
// error when no session is active.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_StopSession_NoSession(t *testing.T) {
	mgr := &DeviceSessionManager{}

	err := mgr.StopSession(context.Background())
	if err == nil {
		t.Fatal("expected error when stopping non-existent session")
	}
	if err.Error() != "no active device session" {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_IdleTimeout: Verify that a session auto-clears
// after the idle timeout expires.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_IdleTimeout(t *testing.T) {
	mgr := &DeviceSessionManager{}

	// Manually inject a session with a very short timeout
	now := time.Now()
	mgr.session = &DeviceSession{
		SessionID:    "test-session-1",
		Platform:     "android",
		StartedAt:    now,
		LastActivity: now,
		IdleTimeout:  80 * time.Millisecond,
	}

	// Start the idle timer (use background context)
	mgr.mu.Lock()
	mgr.resetIdleTimerLocked(context.Background())
	mgr.mu.Unlock()

	// Verify session is initially active
	if mgr.GetActive() == nil {
		t.Fatal("session should be active initially")
	}

	// Wait for idle timeout to fire
	time.Sleep(200 * time.Millisecond)

	// Session should be auto-cleared
	if mgr.GetActive() != nil {
		t.Fatal("session should have been auto-cleared after idle timeout")
	}
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_IdleTimerReset: Verify that resetting the timer
// prevents the session from being cleared prematurely.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_IdleTimerReset(t *testing.T) {
	mgr := &DeviceSessionManager{}

	// Inject a session with a 100ms timeout
	now := time.Now()
	mgr.session = &DeviceSession{
		SessionID:    "test-session-2",
		Platform:     "ios",
		StartedAt:    now,
		LastActivity: now,
		IdleTimeout:  100 * time.Millisecond,
	}

	mgr.mu.Lock()
	mgr.resetIdleTimerLocked(context.Background())
	mgr.mu.Unlock()

	// At 60ms, session should still be alive; reset the timer
	time.Sleep(60 * time.Millisecond)
	if mgr.GetActive() == nil {
		t.Fatal("session should still be active at 60ms")
	}
	mgr.ResetIdleTimer()

	// At 60ms after the reset, the original 100ms from start would have
	// elapsed but the reset should have bought us another 100ms
	time.Sleep(60 * time.Millisecond)
	if mgr.GetActive() == nil {
		t.Fatal("session should still be active because idle timer was reset")
	}

	// After the full timeout from the reset, session should be gone
	time.Sleep(100 * time.Millisecond)
	if mgr.GetActive() != nil {
		t.Fatal("session should be cleared after idle timeout from last reset")
	}
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_Persistence: Verify session persistence to disk
// and restoration from disk.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_Persistence(t *testing.T) {
	tmpDir := t.TempDir()

	// Start a test HTTP server to respond to health checks
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	// Create a manager and manually set a session
	mgr := &DeviceSessionManager{workDir: tmpDir}
	now := time.Now().Truncate(time.Millisecond) // truncate for JSON round-trip

	originalSession := &DeviceSession{
		SessionID:     "persist-test-1",
		WorkflowRunID: "wf-run-xyz",
		WorkerBaseURL: ts.URL, // Use test server URL
		ViewerURL:     "https://app.revyl.ai/tests/execute?workflowRunId=wf-run-xyz",
		Platform:      "android",
		StartedAt:     now,
		LastActivity:  now,
		IdleTimeout:   5 * time.Minute,
	}
	mgr.session = originalSession
	mgr.persistSession()

	// Verify the file exists
	sessionPath := filepath.Join(tmpDir, ".revyl", "device-session.json")
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		t.Fatal("device-session.json should exist after persistSession()")
	}

	// Read and validate the contents
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("failed to read persisted session: %v", err)
	}

	var persisted DeviceSession
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("failed to unmarshal persisted session: %v", err)
	}

	if persisted.SessionID != "persist-test-1" {
		t.Errorf("expected SessionID 'persist-test-1', got %q", persisted.SessionID)
	}
	if persisted.Platform != "android" {
		t.Errorf("expected Platform 'android', got %q", persisted.Platform)
	}

	// Load into a new manager and verify
	mgr2 := &DeviceSessionManager{workDir: tmpDir}
	loaded := mgr2.LoadPersistedSession()
	if loaded == nil {
		t.Fatal("LoadPersistedSession should return the session")
	}

	if loaded.SessionID != originalSession.SessionID {
		t.Errorf("loaded SessionID %q != original %q", loaded.SessionID, originalSession.SessionID)
	}
	if loaded.WorkerBaseURL != ts.URL {
		t.Errorf("loaded WorkerBaseURL %q != original %q", loaded.WorkerBaseURL, ts.URL)
	}
	if loaded.Platform != originalSession.Platform {
		t.Errorf("loaded Platform %q != original %q", loaded.Platform, originalSession.Platform)
	}

	// GetActive should now return the loaded session
	if mgr2.GetActive() == nil {
		t.Fatal("GetActive should return the loaded session after LoadPersistedSession")
	}

	// Verify the idle timer was started on load (bug fix #3)
	if mgr2.idleTimer == nil {
		t.Fatal("idle timer should be started after LoadPersistedSession (bug fix #3)")
	}
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_Persistence_NoWorkDir: Persistence is a no-op
// when workDir is empty.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_Persistence_NoWorkDir(t *testing.T) {
	mgr := &DeviceSessionManager{}
	mgr.session = &DeviceSession{SessionID: "no-persist"}
	mgr.persistSession() // should not panic
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_ClearPersistedSession: Verify clearing removes
// the persisted file.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_ClearPersistedSession(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := &DeviceSessionManager{workDir: tmpDir}
	mgr.session = &DeviceSession{SessionID: "clear-test"}
	mgr.persistSession()

	sessionPath := filepath.Join(tmpDir, ".revyl", "device-session.json")
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		t.Fatal("file should exist before clearing")
	}

	mgr.clearPersistedSession()

	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatal("file should be removed after clearPersistedSession()")
	}
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_LoadPersistedSession_NoFile: Returns nil when
// no persisted session file exists.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_LoadPersistedSession_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := &DeviceSessionManager{workDir: tmpDir}

	loaded := mgr.LoadPersistedSession()
	if loaded != nil {
		t.Errorf("expected nil when no persisted file, got %+v", loaded)
	}
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_LoadPersistedSession_SkipsWhenActive: Returns
// the existing active session instead of reading from disk.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_LoadPersistedSession_SkipsWhenActive(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := &DeviceSessionManager{workDir: tmpDir}
	mgr.session = &DeviceSession{SessionID: "already-active"}

	loaded := mgr.LoadPersistedSession()
	if loaded == nil {
		t.Fatal("expected active session to be returned")
	}
	if loaded.SessionID != "already-active" {
		t.Errorf("expected 'already-active', got %q", loaded.SessionID)
	}
}
