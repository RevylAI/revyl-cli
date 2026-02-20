package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

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
	mgr := &DeviceSessionManager{
		sessions:    make(map[int]*DeviceSession),
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: -1,
	}

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
	mgr := &DeviceSessionManager{
		sessions:    make(map[int]*DeviceSession),
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: -1,
	}

	// Should not panic (index 0 doesn't exist)
	mgr.ResetIdleTimer(0)
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_StopSession_NoSession: StopSession returns an
// error when the specified index doesn't exist.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_StopSession_NoSession(t *testing.T) {
	mgr := &DeviceSessionManager{
		sessions:    make(map[int]*DeviceSession),
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: -1,
	}

	err := mgr.StopSession(context.Background(), 0)
	if err == nil {
		t.Fatal("expected error when stopping non-existent session")
	}
	if err.Error() != "no session at index 0" {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_IdleTimeout: Verify that a session auto-clears
// after the idle timeout expires.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_IdleTimeout(t *testing.T) {
	mgr := &DeviceSessionManager{
		sessions:    make(map[int]*DeviceSession),
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: 0,
		nextIndex:   1,
	}

	// Manually inject a session with a very short timeout
	now := time.Now()
	mgr.sessions[0] = &DeviceSession{
		Index:        0,
		SessionID:    "test-session-1",
		Platform:     "android",
		StartedAt:    now,
		LastActivity: now,
		IdleTimeout:  80 * time.Millisecond,
	}

	// Start the idle timer (use background context)
	mgr.mu.Lock()
	mgr.resetIdleTimerForSessionLocked(0, context.Background())
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
	mgr := &DeviceSessionManager{
		sessions:    make(map[int]*DeviceSession),
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: 0,
		nextIndex:   1,
	}

	// Inject a session with a 100ms timeout
	now := time.Now()
	mgr.sessions[0] = &DeviceSession{
		Index:        0,
		SessionID:    "test-session-2",
		Platform:     "ios",
		StartedAt:    now,
		LastActivity: now,
		IdleTimeout:  100 * time.Millisecond,
	}

	mgr.mu.Lock()
	mgr.resetIdleTimerForSessionLocked(0, context.Background())
	mgr.mu.Unlock()

	// At 60ms, session should still be alive; reset the timer
	time.Sleep(60 * time.Millisecond)
	if mgr.GetActive() == nil {
		t.Fatal("session should still be active at 60ms")
	}
	mgr.ResetIdleTimer(0)

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
// TestDeviceSessionManager_RecompactPreservesRemainingIdleTime: Verify that
// recompact keeps the remaining idle window instead of resetting to full timeout.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_RecompactPreservesRemainingIdleTime(t *testing.T) {
	now := time.Now()
	mgr := &DeviceSessionManager{
		sessions: map[int]*DeviceSession{
			4: {
				Index:         4,
				SessionID:     "recompact-idle-window",
				WorkflowRunID: "wf-recompact-idle-window",
				WorkerBaseURL: "http://localhost:9999",
				Platform:      "ios",
				StartedAt:     now.Add(-10 * time.Second),
				LastActivity:  now.Add(-330 * time.Millisecond),
				IdleTimeout:   400 * time.Millisecond,
			},
		},
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: 4,
		nextIndex:   5,
	}

	// Existing timer at old index, which will be recreated during recompact.
	mgr.idleTimers[4] = time.AfterFunc(5*time.Second, func() {})

	mgr.mu.Lock()
	mgr.recompactIndicesLocked()
	mgr.mu.Unlock()

	if got := mgr.ActiveIndex(); got != 0 {
		t.Fatalf("expected active index 0 after recompact, got %d", got)
	}
	if mgr.GetSession(0) == nil {
		t.Fatal("expected remapped session at index 0 after recompact")
	}

	// Wait long enough to exceed remaining idle time (~70ms), but less than the
	// full timeout window (400ms). If recompact incorrectly resets to full timeout,
	// the session would still be active here.
	time.Sleep(180 * time.Millisecond)
	if mgr.GetSession(0) != nil {
		t.Fatal("expected session to expire based on remaining idle time after recompact")
	}
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_WorkerRequestForSession_ResetsIdleTimer: Verify
// that worker actions count as activity and extend idle timeout.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_WorkerRequestForSession_ResetsIdleTimer(t *testing.T) {
	workerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"action":"tap"}`))
	}))
	defer workerServer.Close()

	now := time.Now()
	mgr := &DeviceSessionManager{
		httpClient: workerServer.Client(),
		sessions: map[int]*DeviceSession{
			0: {
				Index:         0,
				SessionID:     "test-session-worker-reset",
				WorkflowRunID: "wf-worker-reset",
				WorkerBaseURL: workerServer.URL,
				Platform:      "ios",
				StartedAt:     now,
				LastActivity:  now,
				IdleTimeout:   120 * time.Millisecond,
			},
		},
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: 0,
		nextIndex:   1,
	}

	mgr.mu.Lock()
	mgr.resetIdleTimerForSessionLocked(0, context.Background())
	mgr.mu.Unlock()

	time.Sleep(80 * time.Millisecond)

	_, err := mgr.WorkerRequestForSession(context.Background(), 0, http.MethodPost, "/tap", map[string]int{
		"x": 1,
		"y": 2,
	})
	if err != nil {
		t.Fatalf("WorkerRequestForSession returned error: %v", err)
	}

	// This point is beyond the original timeout window, so without idle reset
	// the session would have been auto-cleared.
	time.Sleep(80 * time.Millisecond)
	if mgr.GetSession(0) == nil {
		t.Fatal("session should still be active after worker action reset the idle timer")
	}

	// After the refreshed timeout window, the session should expire.
	time.Sleep(90 * time.Millisecond)
	if mgr.GetSession(0) != nil {
		t.Fatal("session should expire after refreshed idle timeout window")
	}
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_MultiSession: Verify multi-session add, resolve,
// and active switching.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_MultiSession(t *testing.T) {
	mgr := &DeviceSessionManager{
		sessions:    make(map[int]*DeviceSession),
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: -1,
	}

	now := time.Now()

	// Add session 0
	mgr.mu.Lock()
	mgr.sessions[0] = &DeviceSession{Index: 0, SessionID: "s0", Platform: "android", StartedAt: now, LastActivity: now, IdleTimeout: 5 * time.Minute}
	mgr.activeIndex = 0
	mgr.nextIndex = 1
	mgr.mu.Unlock()

	// Add session 1
	mgr.mu.Lock()
	mgr.sessions[1] = &DeviceSession{Index: 1, SessionID: "s1", Platform: "ios", StartedAt: now, LastActivity: now, IdleTimeout: 5 * time.Minute}
	mgr.nextIndex = 2
	mgr.mu.Unlock()

	// ListSessions should return both, sorted
	list := mgr.ListSessions()
	if len(list) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(list))
	}
	if list[0].Index != 0 || list[1].Index != 1 {
		t.Errorf("sessions not sorted by index")
	}

	// ResolveSession(-1) should return active (0)
	s, err := mgr.ResolveSession(-1)
	if err != nil {
		t.Fatalf("resolve active: %v", err)
	}
	if s.Index != 0 {
		t.Errorf("expected active index 0, got %d", s.Index)
	}

	// ResolveSession(1) should return session 1
	s, err = mgr.ResolveSession(1)
	if err != nil {
		t.Fatalf("resolve index 1: %v", err)
	}
	if s.SessionID != "s1" {
		t.Errorf("expected s1, got %s", s.SessionID)
	}

	// SetActive(1) should switch
	if err := mgr.SetActive(1); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if mgr.ActiveIndex() != 1 {
		t.Errorf("expected active 1, got %d", mgr.ActiveIndex())
	}

	// ResolveSession(99) should error
	_, err = mgr.ResolveSession(99)
	if err == nil {
		t.Fatal("expected error for non-existent index")
	}
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_Persistence: Verify multi-session persistence
// to disk and restoration from disk.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_Persistence(t *testing.T) {
	tmpDir := t.TempDir()

	mgr := &DeviceSessionManager{
		workDir:     tmpDir,
		sessions:    make(map[int]*DeviceSession),
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: 0,
		nextIndex:   2,
	}

	now := time.Now().Truncate(time.Millisecond)
	mgr.sessions[0] = &DeviceSession{
		Index:         0,
		SessionID:     "persist-test-1",
		WorkflowRunID: "wf-run-xyz",
		WorkerBaseURL: "http://localhost:8080",
		ViewerURL:     "https://app.revyl.ai/tests/execute?workflowRunId=wf-run-xyz",
		Platform:      "android",
		StartedAt:     now,
		LastActivity:  now,
		IdleTimeout:   5 * time.Minute,
	}

	mgr.persistSessions()

	// Verify the file exists
	sessionPath := filepath.Join(tmpDir, ".revyl", "device-sessions.json")
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		t.Fatal("device-sessions.json should exist after persistSessions()")
	}

	// Read and validate the contents
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("failed to read persisted sessions: %v", err)
	}

	var persisted persistedState
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("failed to unmarshal persisted state: %v", err)
	}

	if persisted.Active != 0 {
		t.Errorf("expected Active=0, got %d", persisted.Active)
	}
	if persisted.NextIdx != 2 {
		t.Errorf("expected NextIdx=2, got %d", persisted.NextIdx)
	}
	if len(persisted.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(persisted.Sessions))
	}
	if persisted.Sessions[0].SessionID != "persist-test-1" {
		t.Errorf("expected SessionID 'persist-test-1', got %q", persisted.Sessions[0].SessionID)
	}

	// Load into a new manager and verify
	mgr2 := &DeviceSessionManager{
		workDir:     tmpDir,
		sessions:    make(map[int]*DeviceSession),
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: -1,
	}
	mgr2.loadLocalCache()

	if mgr2.activeIndex != 0 {
		t.Errorf("expected activeIndex=0 after load, got %d", mgr2.activeIndex)
	}
	if mgr2.nextIndex != 1 {
		t.Errorf("expected nextIndex=1 after load (recompacted), got %d", mgr2.nextIndex)
	}
	if len(mgr2.sessions) != 1 {
		t.Fatalf("expected 1 session after load, got %d", len(mgr2.sessions))
	}
	loaded := mgr2.sessions[0]
	if loaded.SessionID != "persist-test-1" {
		t.Errorf("loaded SessionID %q != original", loaded.SessionID)
	}
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_Persistence_NoWorkDir: Persistence is a no-op
// when workDir is empty.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_Persistence_NoWorkDir(t *testing.T) {
	mgr := &DeviceSessionManager{
		sessions:    make(map[int]*DeviceSession),
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: -1,
	}
	mgr.sessions[0] = &DeviceSession{Index: 0, SessionID: "no-persist"}
	mgr.persistSessions() // should not panic
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_Migration: Verify migration from old
// device-session.json (singular) to new device-sessions.json (plural).
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_Migration(t *testing.T) {
	tmpDir := t.TempDir()

	// Write old-format file
	oldSession := DeviceSession{
		SessionID:     "old-session",
		WorkflowRunID: "old-wf",
		WorkerBaseURL: "http://localhost:9999",
		Platform:      "ios",
		StartedAt:     time.Now(),
		LastActivity:  time.Now(),
		IdleTimeout:   5 * time.Minute,
	}
	data, _ := json.Marshal(oldSession)
	revylDir := filepath.Join(tmpDir, ".revyl")
	_ = os.MkdirAll(revylDir, 0o755)
	_ = os.WriteFile(filepath.Join(revylDir, "device-session.json"), data, 0o644)

	mgr := &DeviceSessionManager{
		workDir:     tmpDir,
		sessions:    make(map[int]*DeviceSession),
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: -1,
	}
	mgr.loadLocalCache()

	// Should have migrated
	if len(mgr.sessions) != 1 {
		t.Fatalf("expected 1 session after migration, got %d", len(mgr.sessions))
	}
	if mgr.sessions[0].SessionID != "old-session" {
		t.Errorf("expected old-session, got %s", mgr.sessions[0].SessionID)
	}
	if mgr.activeIndex != 0 {
		t.Errorf("expected activeIndex=0, got %d", mgr.activeIndex)
	}
	if mgr.nextIndex != 1 {
		t.Errorf("expected nextIndex=1, got %d", mgr.nextIndex)
	}

	// Old file should be removed
	if _, err := os.Stat(filepath.Join(revylDir, "device-session.json")); !os.IsNotExist(err) {
		t.Fatal("old device-session.json should have been removed after migration")
	}

	// New file should exist
	if _, err := os.Stat(filepath.Join(revylDir, "device-sessions.json")); os.IsNotExist(err) {
		t.Fatal("new device-sessions.json should exist after migration")
	}
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_LoadPersistedSession_NoFile: Returns nil when
// no persisted session file exists.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_LoadPersistedSession_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := &DeviceSessionManager{
		workDir:     tmpDir,
		sessions:    make(map[int]*DeviceSession),
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: -1,
	}

	loaded := mgr.LoadPersistedSession()
	if loaded != nil {
		t.Errorf("expected nil when no persisted file, got %+v", loaded)
	}
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_EnsureOrgInfoLocked_UsesValidatedIdentity: Cached
// org/user should be replaced by the currently authenticated identity.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_EnsureOrgInfoLocked_UsesValidatedIdentity(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/entity/users/get_user_uuid" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"user_id":"user-live",
			"org_id":"org-live",
			"email":"live@example.com",
			"concurrency_limit":10
		}`))
	}))
	defer server.Close()

	mgr := &DeviceSessionManager{
		apiClient:   api.NewClientWithBaseURL("test-api-key", server.URL),
		sessions:    make(map[int]*DeviceSession),
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: -1,
		orgID:       "org-stale",
		userEmail:   "stale@example.com",
	}

	mgr.mu.Lock()
	err := mgr.ensureOrgInfoLocked(context.Background())
	mgr.mu.Unlock()
	if err != nil {
		t.Fatalf("ensureOrgInfoLocked returned error: %v", err)
	}

	if mgr.orgID != "org-live" {
		t.Fatalf("expected orgID to refresh from API key, got %q", mgr.orgID)
	}
	if mgr.userEmail != "live@example.com" {
		t.Fatalf("expected userEmail to refresh from API key, got %q", mgr.userEmail)
	}
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_EnsureOrgInfoLocked_FallbackToCachedIdentity: If
// validation fails, cached org/user should still be usable.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_EnsureOrgInfoLocked_FallbackToCachedIdentity(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/entity/users/get_user_uuid" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"invalid api key"}`))
	}))
	defer server.Close()

	mgr := &DeviceSessionManager{
		apiClient:   api.NewClientWithBaseURL("bad-api-key", server.URL),
		sessions:    make(map[int]*DeviceSession),
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: -1,
		orgID:       "org-cached",
		userEmail:   "cached@example.com",
	}

	mgr.mu.Lock()
	err := mgr.ensureOrgInfoLocked(context.Background())
	mgr.mu.Unlock()
	if err != nil {
		t.Fatalf("expected cached fallback on validation failure, got error: %v", err)
	}

	if mgr.orgID != "org-cached" {
		t.Fatalf("expected cached orgID to remain, got %q", mgr.orgID)
	}
	if mgr.userEmail != "cached@example.com" {
		t.Fatalf("expected cached userEmail to remain, got %q", mgr.userEmail)
	}
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_EnsureOrgInfoLocked_NoCacheAndValidationFailure:
// Validation failure without cached org/user should be surfaced as an error.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_EnsureOrgInfoLocked_NoCacheAndValidationFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/entity/users/get_user_uuid" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"invalid api key"}`))
	}))
	defer server.Close()

	mgr := &DeviceSessionManager{
		apiClient:   api.NewClientWithBaseURL("bad-api-key", server.URL),
		sessions:    make(map[int]*DeviceSession),
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: -1,
	}

	mgr.mu.Lock()
	err := mgr.ensureOrgInfoLocked(context.Background())
	mgr.mu.Unlock()
	if err == nil {
		t.Fatal("expected error when validation fails with no cached org/user")
	}
}

// ---------------------------------------------------------------------------
// TestDeviceSessionManager_ResolveSession_SingleFallback: When only one
// session exists and activeIndex is -1, ResolveSession(-1) should still
// return it.
// ---------------------------------------------------------------------------

func TestDeviceSessionManager_ResolveSession_SingleFallback(t *testing.T) {
	mgr := &DeviceSessionManager{
		sessions:    make(map[int]*DeviceSession),
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: -1, // no active set
	}
	mgr.sessions[5] = &DeviceSession{Index: 5, SessionID: "only-one", Platform: "android"}

	s, err := mgr.ResolveSession(-1)
	if err != nil {
		t.Fatalf("expected single-session fallback, got error: %v", err)
	}
	if s.Index != 5 {
		t.Errorf("expected index 5, got %d", s.Index)
	}
}

// ---------------------------------------------------------------------------
// TestReconcileSessionIDsByWorkflow: local sessions seeded with workflowRunID
// should be rewritten to backend session IDs before prune logic runs.
// ---------------------------------------------------------------------------

func TestReconcileSessionIDsByWorkflow(t *testing.T) {
	sessions := map[int]*DeviceSession{
		0: {Index: 0, SessionID: "wf-123", WorkflowRunID: "wf-123"},
		1: {Index: 1, SessionID: "stable-id", WorkflowRunID: "wf-other"},
		2: {Index: 2, SessionID: "no-workflow"},
	}
	backendByWorkflow := map[string]string{
		"wf-123": "session-abc",
	}

	reconcileSessionIDsByWorkflow(sessions, backendByWorkflow)

	if sessions[0].SessionID != "session-abc" {
		t.Fatalf("expected session 0 reconciled to backend ID, got %q", sessions[0].SessionID)
	}
	if sessions[1].SessionID != "stable-id" {
		t.Fatalf("expected session 1 unchanged, got %q", sessions[1].SessionID)
	}
	if sessions[2].SessionID != "no-workflow" {
		t.Fatalf("expected session 2 unchanged, got %q", sessions[2].SessionID)
	}
}

func TestDeviceSessionManager_StartSession_PropagatesBuildPackageToStartDevice(t *testing.T) {
	t.Parallel()

	const (
		buildVersionID = "build-123"
		downloadURL    = "https://artifact.example/dev-client.ipa"
		packageName    = "com.example.devclient"
		workflowRunID  = "wf-run-123"
	)

	var capturedStartReq struct {
		AppURL     string `json:"app_url"`
		AppPackage string `json:"app_package"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/builds/builds/"+buildVersionID:
			_, _ = w.Write([]byte(`{"id":"` + buildVersionID + `","version":"1","download_url":"` + downloadURL + `","package_name":"` + packageName + `"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/execution/start_device":
			if err := json.NewDecoder(r.Body).Decode(&capturedStartReq); err != nil {
				t.Fatalf("decode start_device request: %v", err)
			}
			_, _ = w.Write([]byte(`{"workflow_run_id":"` + workflowRunID + `"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/execution/streaming/worker-connection/"+workflowRunID:
			_, _ = w.Write([]byte(`{"status":"ready","workflow_run_id":"` + workflowRunID + `","worker_ws_url":"ws://` + r.Host + `/ws/stream?token=test"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	mgr := &DeviceSessionManager{
		apiClient:   api.NewClientWithBaseURL("test-key", server.URL),
		httpClient:  server.Client(),
		sessions:    make(map[int]*DeviceSession),
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: -1,
	}

	_, session, err := mgr.StartSession(context.Background(), StartSessionOptions{
		Platform:       "ios",
		BuildVersionID: buildVersionID,
	})
	if err != nil {
		t.Fatalf("StartSession returned error: %v", err)
	}
	if session == nil {
		t.Fatal("expected non-nil session")
	}
	if capturedStartReq.AppURL != downloadURL {
		t.Fatalf("start_device app_url = %q, want %q", capturedStartReq.AppURL, downloadURL)
	}
	if capturedStartReq.AppPackage != packageName {
		t.Fatalf("start_device app_package = %q, want %q", capturedStartReq.AppPackage, packageName)
	}
}

func TestDeviceSessionManager_WorkerRequestForSession_ReturnsTypedWorkerHTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open_url" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Not Found"}`))
	}))
	defer server.Close()

	mgr := &DeviceSessionManager{
		httpClient: server.Client(),
		sessions: map[int]*DeviceSession{
			0: {
				Index:         0,
				SessionID:     "s-1",
				WorkflowRunID: "wf-1",
				WorkerBaseURL: server.URL,
				Platform:      "ios",
			},
		},
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: 0,
	}

	_, err := mgr.WorkerRequestForSession(context.Background(), 0, http.MethodPost, "/open_url", map[string]string{
		"url": "nof1://expo-development-client/?url=https%3A%2F%2Fexample.trycloudflare.com",
	})
	if err == nil {
		t.Fatal("expected non-nil error")
	}

	var workerErr *WorkerHTTPError
	if !errors.As(err, &workerErr) {
		t.Fatalf("expected WorkerHTTPError via errors.As, got %T: %v", err, err)
	}
	if workerErr.StatusCode != http.StatusNotFound {
		t.Fatalf("workerErr.StatusCode = %d, want %d", workerErr.StatusCode, http.StatusNotFound)
	}
	if workerErr.Path != "/open_url" {
		t.Fatalf("workerErr.Path = %q, want %q", workerErr.Path, "/open_url")
	}
	if workerErr.Body != `{"detail":"Not Found"}` {
		t.Fatalf("workerErr.Body = %q, want %q", workerErr.Body, `{"detail":"Not Found"}`)
	}
}

func TestWorkerProxyActionFromPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{name: "simple", path: "/tap", want: "tap"},
		{name: "no-leading-slash", path: "screenshot", want: "screenshot"},
		{name: "query-string", path: "/resolve_target?foo=bar", want: "resolve_target"},
		{name: "nested-path-invalid", path: "/foo/bar", wantErr: true},
		{name: "empty-invalid", path: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := workerProxyActionFromPath(tt.path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for path %q", tt.path)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("workerProxyActionFromPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestDeviceSessionManager_WorkerRequestForSession_FallbacksViaProxyOnDNSFailure(t *testing.T) {
	t.Parallel()

	proxyCalls := 0
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/execution/device-proxy/wf-1/tap" {
			http.NotFound(w, r)
			return
		}
		proxyCalls++
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST to proxy, got %s", r.Method)
		}
		var body map[string]int
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode proxy request body: %v", err)
		}
		if body["x"] != 123 || body["y"] != 456 {
			t.Fatalf("unexpected proxy body: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"action":"tap","latency_ms":10}`))
	}))
	defer apiServer.Close()

	mgr := &DeviceSessionManager{
		apiClient: api.NewClientWithBaseURL("test-api-key", apiServer.URL),
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return nil, &net.DNSError{
					Err:        "no such host",
					Name:       "cog-unresolvable.revyl.ai",
					IsNotFound: true,
				}
			}),
		},
		sessions: map[int]*DeviceSession{
			0: {
				Index:         0,
				SessionID:     "sess-1",
				WorkflowRunID: "wf-1",
				WorkerBaseURL: "https://cog-unresolvable.revyl.ai",
				Platform:      "ios",
			},
		},
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: 0,
	}

	resp, err := mgr.WorkerRequestForSession(context.Background(), 0, http.MethodPost, "/tap", map[string]int{
		"x": 123,
		"y": 456,
	})
	if err != nil {
		t.Fatalf("expected proxy fallback success, got error: %v", err)
	}
	if !strings.Contains(string(resp), `"action":"tap"`) {
		t.Fatalf("unexpected proxy response: %s", string(resp))
	}
	if proxyCalls != 1 {
		t.Fatalf("proxy calls = %d, want 1", proxyCalls)
	}
}

func TestDeviceSessionManager_WorkerRequestForSession_ProxyHTTPErrorReturnsTypedWorkerError(t *testing.T) {
	t.Parallel()

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/execution/device-proxy/wf-2/resolve_target" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Action not allowed"}`))
	}))
	defer apiServer.Close()

	mgr := &DeviceSessionManager{
		apiClient: api.NewClientWithBaseURL("test-api-key", apiServer.URL),
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return nil, &net.DNSError{
					Err:        "no such host",
					Name:       "cog-unresolvable.revyl.ai",
					IsNotFound: true,
				}
			}),
		},
		sessions: map[int]*DeviceSession{
			0: {
				Index:         0,
				SessionID:     "sess-2",
				WorkflowRunID: "wf-2",
				WorkerBaseURL: "https://cog-unresolvable.revyl.ai",
				Platform:      "ios",
			},
		},
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: 0,
	}

	_, err := mgr.WorkerRequestForSession(
		context.Background(),
		0,
		http.MethodPost,
		"/resolve_target",
		map[string]string{"target": "Continue button"},
	)
	if err == nil {
		t.Fatal("expected non-nil error")
	}

	var workerErr *WorkerHTTPError
	if !errors.As(err, &workerErr) {
		t.Fatalf("expected WorkerHTTPError via errors.As, got %T: %v", err, err)
	}
	if workerErr.StatusCode != http.StatusNotFound {
		t.Fatalf("workerErr.StatusCode = %d, want %d", workerErr.StatusCode, http.StatusNotFound)
	}
	if workerErr.Path != "/resolve_target" {
		t.Fatalf("workerErr.Path = %q, want %q", workerErr.Path, "/resolve_target")
	}
	if workerErr.Body != `{"detail":"Action not allowed"}` {
		t.Fatalf("workerErr.Body = %q, want %q", workerErr.Body, `{"detail":"Action not allowed"}`)
	}
}

func TestDeviceSessionManager_ResolveTargetForSession_UsesWorkerResolveEndpoint(t *testing.T) {
	t.Parallel()

	resolveCalls := 0
	screenshotCalls := 0

	workerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/resolve_target":
			resolveCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"found":true,"x":111,"y":222}`))
		case "/screenshot":
			screenshotCalls++
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("unused"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer workerServer.Close()

	mgr := &DeviceSessionManager{
		httpClient: workerServer.Client(),
		sessions: map[int]*DeviceSession{
			0: {
				Index:         0,
				SessionID:     "sess-1",
				WorkflowRunID: "wf-1",
				WorkerBaseURL: workerServer.URL,
				Platform:      "ios",
			},
		},
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: 0,
	}

	resolved, err := mgr.ResolveTargetForSession(context.Background(), 0, "Continue button")
	if err != nil {
		t.Fatalf("ResolveTargetForSession returned error: %v", err)
	}
	if resolved.X != 111 || resolved.Y != 222 {
		t.Fatalf("resolved = (%d,%d), want (111,222)", resolved.X, resolved.Y)
	}
	if resolveCalls != 1 {
		t.Fatalf("resolve_target calls = %d, want 1", resolveCalls)
	}
	if screenshotCalls != 0 {
		t.Fatalf("screenshot calls = %d, want 0 for worker-native path", screenshotCalls)
	}
}

func TestDeviceSessionManager_ResolveTargetForSession_FallbacksToBackendOnLegacyWorker(t *testing.T) {
	t.Parallel()

	resolveCalls := 0
	groundCalls := 0

	workerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/resolve_target":
			resolveCalls++
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Not Found"}`))
		case "/screenshot":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("fallback-screenshot"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer workerServer.Close()

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/execution/ground" {
			http.NotFound(w, r)
			return
		}
		groundCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"found":true,"x":321,"y":654}`))
	}))
	defer apiServer.Close()

	mgr := &DeviceSessionManager{
		apiClient:  api.NewClientWithBaseURL("test-api-key", apiServer.URL),
		httpClient: workerServer.Client(),
		sessions: map[int]*DeviceSession{
			0: {
				Index:         0,
				SessionID:     "sess-2",
				WorkflowRunID: "wf-2",
				WorkerBaseURL: workerServer.URL,
				Platform:      "ios",
			},
		},
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: 0,
	}

	resolved, err := mgr.ResolveTargetForSession(context.Background(), 0, "Sign in button")
	if err != nil {
		t.Fatalf("ResolveTargetForSession returned error: %v", err)
	}
	if resolved.X != 321 || resolved.Y != 654 {
		t.Fatalf("resolved = (%d,%d), want (321,654)", resolved.X, resolved.Y)
	}
	if resolveCalls != 1 {
		t.Fatalf("resolve_target calls = %d, want 1", resolveCalls)
	}
	if groundCalls != 1 {
		t.Fatalf("backend ground calls = %d, want 1", groundCalls)
	}
}

func TestDeviceSessionManager_ResolveTargetForSession_WorkerMissDoesNotFallback(t *testing.T) {
	t.Parallel()

	groundCalls := 0

	workerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/resolve_target" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"found":false,"error":"Could not locate 'Continue button' in the screenshot"}`))
	}))
	defer workerServer.Close()

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/execution/ground" {
			groundCalls++
		}
		http.NotFound(w, r)
	}))
	defer apiServer.Close()

	mgr := &DeviceSessionManager{
		apiClient:  api.NewClientWithBaseURL("test-api-key", apiServer.URL),
		httpClient: workerServer.Client(),
		sessions: map[int]*DeviceSession{
			0: {
				Index:         0,
				SessionID:     "sess-3",
				WorkflowRunID: "wf-3",
				WorkerBaseURL: workerServer.URL,
				Platform:      "ios",
			},
		},
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: 0,
	}

	_, err := mgr.ResolveTargetForSession(context.Background(), 0, "Continue button")
	if err == nil {
		t.Fatal("expected error for unresolved target")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "could not locate") {
		t.Fatalf("unexpected error: %v", err)
	}
	if groundCalls != 0 {
		t.Fatalf("backend ground should not be called on worker miss, got %d", groundCalls)
	}
}
