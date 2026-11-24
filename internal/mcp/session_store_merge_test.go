package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newStoreTestManager(workDir string) *DeviceSessionManager {
	return &DeviceSessionManager{
		workDir:           workDir,
		sessions:          make(map[int]*DeviceSession),
		ownedSessions:     make(map[int]bool),
		idleTimerDisabled: make(map[int]bool),
		idleTimers:        make(map[int]*time.Timer),
		screenAnchors:     make(map[int]*screenAnchorState),
		activeIndex:       -1,
	}
}

func addStoreTestSession(mgr *DeviceSessionManager, index int, sessionID string) *DeviceSession {
	now := time.Now()
	session := &DeviceSession{
		Index:         index,
		SessionID:     sessionID,
		WorkflowRunID: "wf-" + sessionID,
		Platform:      "ios",
		StartedAt:     now,
		LastActivity:  now,
	}
	mgr.sessions[index] = session
	return session
}

func persistStoreTestManager(mgr *DeviceSessionManager) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	mgr.persistAllSessionsForBootstrap()
}

func readStoreTestState(t *testing.T, workDir string) persistedState {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(workDir, ".revyl", "device-sessions.json"))
	if err != nil {
		t.Fatalf("read device-sessions.json: %v", err)
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("unmarshal device-sessions.json: %v", err)
	}
	return state
}

func storeTestSessionIDs(state persistedState) map[string]int {
	byID := make(map[string]int, len(state.Sessions))
	for _, session := range state.Sessions {
		byID[session.SessionID] = session.Index
	}
	return byID
}

// A parallel batch starts every worker against one worktree. Under the previous
// whole-file overwrite each writer erased the last, so only one session
// survived; every start must now land.
func TestPersistSessions_ConcurrentWritersAllSurvive(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	const writers = 8

	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			mgr := newStoreTestManager(workDir)
			addStoreTestSession(mgr, 0, "session-"+string(rune('a'+n)))
			persistStoreTestManager(mgr)
		}(i)
	}
	wg.Wait()

	state := readStoreTestState(t, workDir)
	if len(state.Sessions) != writers {
		t.Fatalf("persisted sessions = %d, want %d: %+v", len(state.Sessions), writers, storeTestSessionIDs(state))
	}

	seenIndexes := make(map[int]bool, writers)
	for _, session := range state.Sessions {
		if seenIndexes[session.Index] {
			t.Fatalf("duplicate index %d in %+v", session.Index, storeTestSessionIDs(state))
		}
		seenIndexes[session.Index] = true
	}
	for i := range writers {
		id := "session-" + string(rune('a'+i))
		if _, ok := storeTestSessionIDs(state)[id]; !ok {
			t.Fatalf("session %q missing from store: %+v", id, storeTestSessionIDs(state))
		}
	}
}

func TestPersistSessions_KeepsSessionsOwnedByOtherProcesses(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()

	first := newStoreTestManager(workDir)
	addStoreTestSession(first, 0, "session-first")
	persistStoreTestManager(first)

	second := newStoreTestManager(workDir)
	secondSession := addStoreTestSession(second, 0, "session-second")
	persistStoreTestManager(second)

	byID := storeTestSessionIDs(readStoreTestState(t, workDir))
	if len(byID) != 2 {
		t.Fatalf("persisted sessions = %v, want both processes represented", byID)
	}
	if byID["session-first"] != 0 {
		t.Fatalf("session-first index = %d, want its published index 0 preserved", byID["session-first"])
	}
	if byID["session-second"] == 0 {
		t.Fatal("session-second kept index 0, want reassignment away from the published index")
	}
	if secondSession.Index != byID["session-second"] {
		t.Fatalf("in-memory index = %d, persisted index = %d: caller would report the wrong session",
			secondSession.Index, byID["session-second"])
	}
}

func TestPersistSessions_RemovesOnlyDeliberatelyStoppedSessions(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()

	first := newStoreTestManager(workDir)
	addStoreTestSession(first, 0, "session-first")
	persistStoreTestManager(first)

	second := newStoreTestManager(workDir)
	addStoreTestSession(second, 0, "session-second")
	persistStoreTestManager(second)

	stopper := newStoreTestManager(workDir)
	stopper.mu.Lock()
	stopper.loadLocalCache()
	var stopIndex int
	var stopSession *DeviceSession
	for index, session := range stopper.sessions {
		if session.SessionID == "session-first" {
			stopIndex, stopSession = index, session
		}
	}
	if stopSession == nil {
		t.Fatal("session-first not loaded from the shared store")
	}
	if err := stopper.stopSessionAtIndexLocked(context.Background(), stopIndex, stopSession); err != nil {
		t.Fatalf("stopSessionAtIndexLocked() error = %v", err)
	}
	stopper.persistSessionsWithMutation(sessionCacheMutation{
		removedSessions: []sessionCacheIdentity{sessionCacheIdentityFor(stopSession)},
		updateActive:    true,
	})
	stopper.mu.Unlock()

	byID := storeTestSessionIDs(readStoreTestState(t, workDir))
	if _, present := byID["session-first"]; present {
		t.Fatalf("stopped session survived the merge: %v", byID)
	}
	if _, present := byID["session-second"]; !present {
		t.Fatalf("unrelated session removed by an unrelated stop: %v", byID)
	}
}

func TestPersistSessions_BackfilledSessionIDUpdatesExistingRow(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	mgr := newStoreTestManager(workDir)

	session := addStoreTestSession(mgr, 0, "")
	session.WorkflowRunID = "wf-late-id"
	persistStoreTestManager(mgr)

	if got := len(readStoreTestState(t, workDir).Sessions); got != 1 {
		t.Fatalf("sessions after the pre-ID write = %d, want 1", got)
	}

	session.SessionID = "session-late"
	persistStoreTestManager(mgr)

	state := readStoreTestState(t, workDir)
	if len(state.Sessions) != 1 {
		t.Fatalf("sessions after backfill = %d, want one row for one device: %v",
			len(state.Sessions), storeTestSessionIDs(state))
	}
	if state.Sessions[0].SessionID != "session-late" {
		t.Fatalf("persisted session ID = %q, want the backfilled ID", state.Sessions[0].SessionID)
	}
	if state.Sessions[0].Index != 0 {
		t.Fatalf("persisted index = %d, want the published index 0 kept", state.Sessions[0].Index)
	}
	if session.Index != 0 {
		t.Fatalf("in-memory index = %d, want 0: a backfill must not move the live session", session.Index)
	}
}

func TestPersistSessions_PreservesBackfilledSessionIDWhenLocalStillEmpty(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	const viewerURL = "https://app.revyl.ai/sessions/session-late"

	starter := newStoreTestManager(workDir)
	pending := addStoreTestSession(starter, 0, "")
	pending.WorkflowRunID = "wf-late-id"
	persistStoreTestManager(starter)

	peer := newStoreTestManager(workDir)
	filled := addStoreTestSession(peer, 0, "session-late")
	filled.WorkflowRunID = "wf-late-id"
	filled.ViewerURL = viewerURL
	persistStoreTestManager(peer)

	persistStoreTestManager(starter)

	state := readStoreTestState(t, workDir)
	if len(state.Sessions) != 1 {
		t.Fatalf("sessions after the stale persist = %d, want one row: %v",
			len(state.Sessions), storeTestSessionIDs(state))
	}
	if state.Sessions[0].SessionID != "session-late" {
		t.Fatalf("persisted session ID = %q, want the peer-written ID kept", state.Sessions[0].SessionID)
	}
	if state.Sessions[0].ViewerURL != viewerURL {
		t.Fatalf("persisted viewer URL = %q, want the peer-written URL kept", state.Sessions[0].ViewerURL)
	}
	if pending.SessionID != "session-late" {
		t.Fatalf("in-memory session ID = %q, want the stored ID copied onto the live session", pending.SessionID)
	}
}

func TestPersistSessions_StopRemovesRowWrittenBeforeSessionIDArrived(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()

	starter := newStoreTestManager(workDir)
	pending := addStoreTestSession(starter, 0, "")
	pending.WorkflowRunID = "wf-stopped"
	persistStoreTestManager(starter)

	stopper := newStoreTestManager(workDir)
	stopped := addStoreTestSession(stopper, 0, "session-stopped")
	stopped.WorkflowRunID = "wf-stopped"
	stopper.mu.Lock()
	delete(stopper.sessions, 0)
	stopper.persistSessionsWithMutation(sessionCacheMutation{
		removedSessions: []sessionCacheIdentity{sessionCacheIdentityFor(stopped)},
		updateActive:    true,
	})
	stopper.mu.Unlock()

	state := readStoreTestState(t, workDir)
	if len(state.Sessions) != 0 {
		t.Fatalf("sessions after stop = %v, want the workflow-keyed row removed",
			storeTestSessionIDs(state))
	}
}

func TestPersistSessions_RemapsIndexBookkeepingOnCollision(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()

	published := newStoreTestManager(workDir)
	addStoreTestSession(published, 0, "session-published")
	persistStoreTestManager(published)

	colliding := newStoreTestManager(workDir)
	session := addStoreTestSession(colliding, 0, "session-colliding")
	colliding.ownedSessions[0] = true
	colliding.idleTimerDisabled[0] = true
	colliding.screenAnchors[0] = &screenAnchorState{Token: "anchor-token"}
	colliding.activeIndex = 0

	persistStoreTestManager(colliding)

	newIndex := session.Index
	if newIndex == 0 {
		t.Fatal("colliding session kept index 0, want reassignment")
	}
	if colliding.sessions[newIndex] != session {
		t.Fatalf("sessions[%d] does not hold the remapped session", newIndex)
	}
	if publishedSession := colliding.sessions[0]; publishedSession == nil || publishedSession.SessionID != "session-published" {
		t.Fatalf("sessions[0] = %#v, want the already-published peer session", publishedSession)
	}
	if !colliding.ownedSessions[newIndex] || colliding.ownedSessions[0] {
		t.Fatalf("ownedSessions not remapped: %v", colliding.ownedSessions)
	}
	if !colliding.idleTimerDisabled[newIndex] || colliding.idleTimerDisabled[0] {
		t.Fatalf("idleTimerDisabled not remapped: %v", colliding.idleTimerDisabled)
	}
	anchor, ok := colliding.screenAnchors[newIndex]
	if !ok || anchor.Token != "anchor-token" {
		t.Fatalf("screenAnchors not remapped: %v", colliding.screenAnchors)
	}
	if colliding.activeIndex != newIndex {
		t.Fatalf("activeIndex = %d, want %d", colliding.activeIndex, newIndex)
	}
}

func TestPersistSessions_PreservesStoredOrgIdentity(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()

	identified := newStoreTestManager(workDir)
	identified.orgID = "org-1"
	identified.userEmail = "test@example.com"
	addStoreTestSession(identified, 0, "session-first")
	persistStoreTestManager(identified)

	anonymous := newStoreTestManager(workDir)
	addStoreTestSession(anonymous, 0, "session-second")
	persistStoreTestManager(anonymous)

	state := readStoreTestState(t, workDir)
	if state.OrgID != "org-1" {
		t.Fatalf("OrgID = %q, want the stored value preserved", state.OrgID)
	}
	if state.UserEmail != "test@example.com" {
		t.Fatalf("UserEmail = %q, want the stored value preserved", state.UserEmail)
	}
}

func TestPersistSessions_LeavesNoTemporaryFiles(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	mgr := newStoreTestManager(workDir)
	addStoreTestSession(mgr, 0, "session-first")
	persistStoreTestManager(mgr)

	entries, err := os.ReadDir(filepath.Join(workDir, ".revyl"))
	if err != nil {
		t.Fatalf("read .revyl: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temporary file %q left behind", entry.Name())
		}
	}
}

func TestLockSessionStore_SerializesWriters(t *testing.T) {
	t.Parallel()

	lockPath := filepath.Join(t.TempDir(), "device-sessions.lock")

	release, err := lockSessionStore(lockPath)
	if err != nil {
		t.Fatalf("lockSessionStore() error = %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		secondRelease, secondErr := lockSessionStore(lockPath)
		if secondErr == nil {
			secondRelease()
		}
		close(acquired)
	}()

	select {
	case <-acquired:
		t.Fatal("second writer acquired the lock while it was held")
	case <-time.After(100 * time.Millisecond):
	}

	release()

	select {
	case <-acquired:
	case <-time.After(sessionStoreLockTimeout):
		t.Fatal("second writer never acquired the lock after release")
	}
}
