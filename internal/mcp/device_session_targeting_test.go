package mcp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
)

// startTargetingSession registers a session as started by this manager's
// directory, the way a successful device start does.
func startTargetingSession(t *testing.T, mgr *DeviceSessionManager, name string, startedAt time.Time) *DeviceSession {
	t.Helper()
	session := &DeviceSession{
		SessionID: name + "-session", WorkflowRunID: name + "-run", Platform: "ios",
		StartedAt: startedAt, LastActivity: startedAt, IdleTimeout: time.Hour,
	}
	index, err := mgr.registerStartedSession(session)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mgr.StopIdleTimer(index) })
	return session
}

// addAccountSession adds a live session the way sync discovers one that
// another directory started.
func addAccountSession(mgr *DeviceSessionManager, name string, startedAt time.Time) *DeviceSession {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	session := &DeviceSession{Index: mgr.nextIndex, SessionID: name + "-session", WorkflowRunID: name + "-run", Platform: "android", StartedAt: startedAt}
	mgr.nextIndex++
	mgr.sessions[session.Index] = session
	if err := mgr.persistSessionsWithMutation(sessionCacheMutation{addedSessions: []*DeviceSession{session}}); err != nil {
		panic(err)
	}
	return session
}

func newTargetingManager(t *testing.T, dir string) *DeviceSessionManager {
	t.Helper()
	mgr := NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", "http://127.0.0.1:1"), dir)
	mgr.LoadPersistedSession()
	return mgr
}

func mustResolveUntargeted(t *testing.T, mgr *DeviceSessionManager) UntargetedSessionChoice {
	t.Helper()
	choice, err := mgr.ResolveUntargetedSession()
	if err != nil {
		t.Fatalf("ResolveUntargetedSession() error = %v", err)
	}
	return choice
}

func TestUntargetedResolutionFollowsTheSessionThisDirectoryStarted(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	mgr := newTargetingManager(t, dir)
	addAccountSession(mgr, "other-agent", now.Add(-time.Hour))
	first := startTargetingSession(t, mgr, "first", now.Add(-10*time.Minute))

	choice := mustResolveUntargeted(t, mgr)
	if choice.Session.SessionID != first.SessionID || choice.Rule != UntargetedSelected || !choice.Changed || choice.HadPrevious {
		t.Fatalf("choice = %+v, want the session this directory started, first use", choice)
	}
	mgr.RecordUntargetedChoice(choice.Session)

	second := startTargetingSession(t, mgr, "second", now.Add(-time.Minute))
	reloaded := newTargetingManager(t, dir)
	choice = mustResolveUntargeted(t, reloaded)
	if choice.Session.SessionID != second.SessionID || choice.Rule != UntargetedSelected || !choice.Changed || !choice.HadPrevious || choice.StartedHere != 2 {
		t.Fatalf("choice after a second start = %+v, want the new session, announced as a change", choice)
	}
	reloaded.RecordUntargetedChoice(choice.Session)
	if again := mustResolveUntargeted(t, newTargetingManager(t, dir)); again.Changed {
		t.Fatalf("repeat choice = %+v, want no change", again)
	}

	if err := reloaded.SetActive(first.Index); err != nil {
		t.Fatal(err)
	}
	choice = mustResolveUntargeted(t, newTargetingManager(t, dir))
	if choice.Session.SessionID != first.SessionID || choice.Rule != UntargetedSelected {
		t.Fatalf("choice after device use = %+v, want the selected session", choice)
	}
}

func TestUntargetedResolutionFallsBackToTheNewestSessionStartedHere(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	mgr := newTargetingManager(t, dir)
	older := startTargetingSession(t, mgr, "older", now.Add(-20*time.Minute))
	newer := startTargetingSession(t, mgr, "newer", now.Add(-10*time.Minute))
	selected := startTargetingSession(t, mgr, "selected", now.Add(-time.Minute))
	addAccountSession(mgr, "other-agent", now)

	mgr.mu.Lock()
	delete(mgr.sessions, selected.Index)
	persistErr := mgr.persistSessionsWithMutation(sessionCacheMutation{removedSessions: []sessionCacheIdentity{sessionCacheIdentityFor(selected)}})
	mgr.mu.Unlock()
	if persistErr != nil {
		t.Fatal(persistErr)
	}

	choice := mustResolveUntargeted(t, newTargetingManager(t, dir))
	if choice.Session.SessionID != newer.SessionID || choice.Rule != UntargetedStartedHere || choice.StartedHere != 2 {
		t.Fatalf("choice = %+v, want the newest live session started here (not %s or the other agent's)", choice, older.SessionID)
	}
}

func TestUntargetedResolutionInAFreshDirectory(t *testing.T) {
	now := time.Now()

	single := newTargetingManager(t, t.TempDir())
	only := addAccountSession(single, "only", now)
	if choice := mustResolveUntargeted(t, single); choice.Session.SessionID != only.SessionID || choice.Rule != UntargetedOnlyLive {
		t.Fatalf("choice = %+v, want the account's only live session", choice)
	}

	several := newTargetingManager(t, t.TempDir())
	addAccountSession(several, "oldest", now.Add(-time.Hour))
	addAccountSession(several, "newest", now)
	_, err := several.ResolveUntargetedSession()
	var lookupErr *SessionLookupError
	if !errors.As(err, &lookupErr) || lookupErr.Reason != SessionLookupNoneStartedHere {
		t.Fatalf("ResolveUntargetedSession() error = %v, want the none-started-here refusal instead of the oldest session", err)
	}

	withStarting := newTargetingManager(t, t.TempDir())
	addAccountSession(withStarting, "running", now)
	withStarting.unreachableSessions = []DeviceSession{{Index: UnattachedSessionIndex, SessionID: "starting-session"}}
	if _, err := withStarting.ResolveUntargetedSession(); !errors.As(err, &lookupErr) || lookupErr.Reason != SessionLookupNoneStartedHere {
		t.Fatalf("ResolveUntargetedSession() with a starting session error = %v, want the refusal", err)
	}

	empty := newTargetingManager(t, t.TempDir())
	if _, err := empty.ResolveUntargetedSession(); !errors.As(err, &lookupErr) || lookupErr.Reason != SessionLookupNoActiveSessions {
		t.Fatalf("ResolveUntargetedSession() with nothing live error = %v, want the no-active-session error", err)
	}
}

func TestDirectoryTargetingSurvivesSessionIDReconciliation(t *testing.T) {
	dir := t.TempDir()
	mgr := newTargetingManager(t, dir)
	session := &DeviceSession{WorkflowRunID: "run-late-id", Platform: "ios", StartedAt: time.Now(), IdleTimeout: time.Hour}
	index, err := mgr.registerStartedSession(session)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mgr.StopIdleTimer(index) })
	addAccountSession(mgr, "other-agent", time.Now())

	mgr.mu.Lock()
	mgr.sessions[index].SessionID = "issued-later"
	persistErr := mgr.persistSessionsWithMutation(sessionCacheMutation{updatedSessions: []*DeviceSession{mgr.sessions[index]}})
	mgr.mu.Unlock()
	if persistErr != nil {
		t.Fatal(persistErr)
	}

	choice := mustResolveUntargeted(t, newTargetingManager(t, dir))
	if choice.Session.SessionID != "issued-later" || choice.Rule != UntargetedSelected {
		t.Fatalf("choice = %+v, want the reconciled session still tied to this directory", choice)
	}
	if got := fmt.Sprint(newTargetingManager(t, dir).startedHere); got == "[]" {
		t.Fatal("started_here was dropped when the session ID was issued")
	}
}

func TestSelectionThatCannotBeSavedIsRolledBack(t *testing.T) {
	dir := t.TempDir()
	mgr := newTargetingManager(t, dir)
	first := startTargetingSession(t, mgr, "first", time.Now().Add(-time.Minute))
	second := startTargetingSession(t, mgr, "second", time.Now())
	activeBefore := mgr.ActiveIndex()

	revylDir := filepath.Join(dir, ".revyl")
	if err := os.Chmod(revylDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(revylDir, 0o700) })
	if probe, err := os.Create(filepath.Join(revylDir, "probe")); err == nil {
		_ = probe.Close()
		t.Skip("the session store directory is still writable (running as root)")
	}

	target := first.Index
	if activeBefore == first.Index {
		target = second.Index
	}
	if err := mgr.SetActive(target); err == nil {
		t.Fatal("SetActive() error = nil, want the save failure")
	}
	if mgr.ActiveIndex() != activeBefore {
		t.Fatalf("active index = %d, want %d restored after the failed save", mgr.ActiveIndex(), activeBefore)
	}
}
