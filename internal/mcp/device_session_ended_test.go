package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
)

func TestEndedSessionFromPruneInfersReason(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	endedAt := now.Add(-4 * time.Minute)
	endedAtText := endedAt.Format(time.RFC3339Nano)
	idleSession := &DeviceSession{Index: 2, SessionID: "session-a", Platform: "ios", IdleTimeout: 300 * time.Second, LastActivity: endedAt.Add(-301 * time.Second)}
	busySession := &DeviceSession{Index: 2, SessionID: "session-a", Platform: "ios", IdleTimeout: 300 * time.Second, LastActivity: endedAt.Add(-30 * time.Second)}
	errorMessage := func(text string) *string { return &text }
	enforcedIdle := map[string]interface{}{"idle_timeout_seconds": float64(300)}

	testCases := []struct {
		name        string
		session     *DeviceSession
		detail      *api.DeviceSessionDetail
		wantReason  EndedSessionReason
		wantEndedAt time.Time
		wantIdle    int
	}{
		{name: "record gone", session: idleSession, wantReason: EndedSessionUnknown, wantEndedAt: now},
		{name: "stop requested", session: idleSession, detail: &api.DeviceSessionDetail{Status: "completed", EndedAt: &endedAtText, SourceMetadata: map[string]interface{}{"stop_request": map[string]interface{}{}}}, wantReason: EndedSessionStopped, wantEndedAt: endedAt},
		{name: "cancel requested", session: busySession, detail: &api.DeviceSessionDetail{Status: "cancelled", EndedAt: &endedAtText, SourceMetadata: map[string]interface{}{"stop_request": map[string]interface{}{}}}, wantReason: EndedSessionCancelled, wantEndedAt: endedAt},
		{name: "cancelled after a long idle stays cancelled", session: idleSession, detail: &api.DeviceSessionDetail{Status: "cancelled", EndedAt: &endedAtText, SourceMetadata: enforcedIdle}, wantReason: EndedSessionCancelled, wantEndedAt: endedAt, wantIdle: 300},
		{name: "failed", session: busySession, detail: &api.DeviceSessionDetail{Status: "failed", EndedAt: &endedAtText}, wantReason: EndedSessionFailed, wantEndedAt: endedAt},
		{name: "idle named by the backend", session: busySession, detail: &api.DeviceSessionDetail{Status: "completed", EndedAt: &endedAtText, ErrorMessage: errorMessage("Idle timeout reached"), SourceMetadata: map[string]interface{}{"idle_timeout_seconds": float64(900)}}, wantReason: EndedSessionIdleTimeout, wantEndedAt: endedAt, wantIdle: 900},
		{name: "idle inferred from last activity", session: idleSession, detail: &api.DeviceSessionDetail{Status: "completed", EndedAt: &endedAtText, SourceMetadata: enforcedIdle}, wantReason: EndedSessionIdleTimeout, wantEndedAt: endedAt, wantIdle: 300},
		{name: "local idle guess is not evidence", session: idleSession, detail: &api.DeviceSessionDetail{Status: "completed", EndedAt: &endedAtText}, wantReason: EndedSessionUnknown, wantEndedAt: endedAt},
		{name: "recent activity is not idle", session: busySession, detail: &api.DeviceSessionDetail{Status: "completed", EndedAt: &endedAtText, SourceMetadata: enforcedIdle}, wantReason: EndedSessionUnknown, wantEndedAt: endedAt, wantIdle: 300},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ended := endedSessionFromPrune(tc.session, tc.detail, now)
			if ended.Reason != tc.wantReason || !ended.EndedAt.Equal(tc.wantEndedAt) || ended.IdleTimeoutSeconds != tc.wantIdle {
				t.Fatalf("ended = %+v, want reason %s ended_at %s idle %d", ended, tc.wantReason, tc.wantEndedAt, tc.wantIdle)
			}
			if ended.Index != 2 || ended.SessionID != "session-a" || ended.Platform != "ios" {
				t.Fatalf("ended identity = %+v", ended)
			}
		})
	}
}

func TestEndedSessionDescribe(t *testing.T) {
	now := time.Now()
	ended := &EndedSession{Index: 0, SessionID: "9f3c1a2b-4d5e", Platform: "ios", EndedAt: now.Add(-6 * time.Minute), Reason: EndedSessionIdleTimeout, IdleTimeoutSeconds: 300}
	if got, want := ended.describe(now), "session 0 (ios 9f3c1a2b) ended 6m ago after 300s without device activity (idle timeout)"; got != want {
		t.Fatalf("describe() = %q, want %q", got, want)
	}
	for _, tc := range []struct {
		age  time.Duration
		want string
	}{
		{47 * time.Hour, "47h"},
		{48 * time.Hour, "2d"},
		{10*24*time.Hour + time.Hour, "10d"},
	} {
		if got := FormatAge(tc.age); got != tc.want {
			t.Fatalf("FormatAge(%s) = %q, want %q", tc.age, got, tc.want)
		}
	}
	ended.Reason = EndedSessionStopped
	ended.EndedAt = now.Add(-3 * time.Hour)
	if got, want := ended.describe(now), "session 0 (ios 9f3c1a2b) ended 3h ago because a stop was requested"; got != want {
		t.Fatalf("describe() = %q, want %q", got, want)
	}
}

func TestMergeEndedSessionsKeepsRecentBoundedHistory(t *testing.T) {
	now := time.Now()
	var existing []*EndedSession
	for i := 0; i < endedSessionLimit+3; i++ {
		existing = append(existing, &EndedSession{SessionID: fmt.Sprintf("s-%d", i), EndedAt: now.Add(-time.Duration(i) * time.Minute)})
	}
	existing = append(existing, &EndedSession{SessionID: "expired", EndedAt: now.Add(-endedSessionRetention - time.Minute)})
	newer := &EndedSession{SessionID: "s-5", EndedAt: now.Add(time.Second), Reason: EndedSessionStopped}
	withoutSessionID := &EndedSession{WorkflowRunID: "run-only", EndedAt: now.Add(time.Second / 2)}

	merged := mergeEndedSessions(existing, []*EndedSession{newer, withoutSessionID}, now)
	if len(merged) != endedSessionLimit {
		t.Fatalf("merged %d records, want the %d most recent", len(merged), endedSessionLimit)
	}
	if merged[0] != newer || merged[1] != withoutSessionID {
		t.Fatalf("merged[:2] = %+v, %+v, want the newest record for s-5, then the record keyed by workflow run", merged[0], merged[1])
	}
	for i, ended := range merged {
		if ended.SessionID == "expired" {
			t.Fatal("expired record survived the merge")
		}
		if i > 0 && merged[i-1].EndedAt.Before(ended.EndedAt) {
			t.Fatal("merged records are not newest first")
		}
		if i > 0 && ended.SessionID == "s-5" {
			t.Fatal("s-5 appears twice")
		}
	}
}

// newPruneTestServer serves an empty active-session list and a terminal,
// released detail record for the local session sync is about to prune.
func newPruneTestServer(t *testing.T, sessionID string, endedAt time.Time) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-1","email":"test@example.test"}`))
		case "/api/v1/execution/device-sessions/active":
			_, _ = w.Write([]byte(`{"org_id":"org-1","sessions":[]}`))
		case "/api/v1/execution/device-sessions/" + sessionID:
			fmt.Fprintf(w, `{"id":%q,"org_id":"org-1","platform":"ios","status":"completed","ended_at":%q,"source_metadata":{"device_released_at":%q,"idle_timeout_seconds":300}}`,
				sessionID, endedAt.Format(time.RFC3339Nano), endedAt.Format(time.RFC3339Nano))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestSyncSessionsExplainsPrunedSessionInLookupErrors(t *testing.T) {
	const sessionID = "9f3c1a2b-4d5e-4f60-8a71-b2c3d4e5f607"
	endedAt := time.Now().Add(-6 * time.Minute)
	for _, tc := range []struct {
		surface      ErrorSurface
		wantNoActive string
		wantIndex    string
	}{
		{
			surface:      ErrorSurfaceCLI,
			wantNoActive: "no active device sessions. Session 0 (ios 9f3c1a2b) ended 6m ago after 300s without device activity (idle timeout). Start a new one with 'revyl device start --platform ios'",
			wantIndex:    "no session at index 0. Session 0 (ios 9f3c1a2b) ended 6m ago after 300s without device activity (idle timeout). Start a new one with 'revyl device start --platform ios'",
		},
		{
			surface:      ErrorSurfaceMCP,
			wantNoActive: "no active device sessions. Session 0 (ios 9f3c1a2b) ended 6m ago after 300s without device activity (idle timeout). Start a new one with start_device_session(platform='ios')",
			wantIndex:    "no session at index 0. Session 0 (ios 9f3c1a2b) ended 6m ago",
		},
	} {
		t.Run(fmt.Sprintf("surface-%d", tc.surface), func(t *testing.T) {
			server := newPruneTestServer(t, sessionID, endedAt)
			dir := t.TempDir()
			client := api.NewClientWithBaseURL("test-key", server.URL)
			starter := NewDeviceSessionManager(client, dir)
			if _, err := starter.registerStartedSession(&DeviceSession{
				SessionID: sessionID, WorkflowRunID: "run-1", Platform: "ios",
				StartedAt: endedAt.Add(-time.Hour), LastActivity: endedAt.Add(-5 * time.Minute), IdleTimeout: 300 * time.Second,
			}); err != nil {
				t.Fatal(err)
			}
			starter.StopIdleTimer(0)

			mgr := NewDeviceSessionManager(client, dir)
			mgr.SetErrorSurface(tc.surface)
			if err := mgr.SyncSessions(context.Background()); err != nil {
				t.Fatalf("SyncSessions() error = %v", err)
			}
			if _, err := mgr.ResolveSession(-1); err == nil || !strings.Contains(err.Error(), tc.wantNoActive) {
				t.Fatalf("ResolveSession(-1) error = %v, want %q", err, tc.wantNoActive)
			}
			if _, err := mgr.ResolveSession(0); err == nil || !strings.Contains(err.Error(), tc.wantIndex) {
				t.Fatalf("ResolveSession(0) error = %v, want %q", err, tc.wantIndex)
			}

			reloaded := NewDeviceSessionManager(client, dir)
			reloaded.LoadPersistedSession()
			ended, ok := reloaded.EndedSessionAtIndex(0)
			if !ok || ended.SessionID != sessionID || ended.Reason != EndedSessionIdleTimeout || ended.IdleTimeoutSeconds != 300 || ended.EndedAt.Sub(endedAt).Abs() > time.Millisecond {
				t.Fatalf("persisted ended session = %+v (found %v), want the pruned idle-timeout session", ended, ok)
			}
		})
	}
}

func TestResolveSessionByIDExplainsWhenTerminalSessionEnded(t *testing.T) {
	const sessionID = "9f3c1a2b-4d5e-4f60-8a71-b2c3d4e5f607"
	endedAt := time.Now().Add(-2 * time.Minute)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":%q,"org_id":"org-1","platform":"ios","status":"completed","workflow_run_id":"run-1","ended_at":%q,"error_message":"Idle timeout reached","source_metadata":{"idle_timeout_seconds":300}}`,
			sessionID, endedAt.Format(time.RFC3339Nano))
	}))
	t.Cleanup(server.Close)

	mgr := NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", server.URL), "")
	_, err := mgr.ResolveSessionByID(context.Background(), sessionID)
	want := "session 9f3c1a2b is in terminal state: completed; it ended 2m ago after 300s without device activity (idle timeout)"
	if err == nil || err.Error() != want {
		t.Fatalf("ResolveSessionByID() error = %v, want %q", err, want)
	}
}

func TestLocalStopRecordsEndedSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"request_accepted":true,"session_settled":true,"device_released":true}`))
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	mgr := NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", server.URL), dir)
	mgr.SetErrorSurface(ErrorSurfaceCLI)
	for i, startedAgo := range []time.Duration{10 * time.Minute, 5 * time.Minute} {
		index, err := mgr.registerStartedSession(&DeviceSession{
			SessionID: fmt.Sprintf("session-%d-0000", i), WorkflowRunID: fmt.Sprintf("run-%d", i), Platform: "ios",
			StartedAt: time.Now().Add(-startedAgo), LastActivity: time.Now(), IdleTimeout: time.Hour,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { mgr.StopIdleTimer(index) })
	}
	if err := mgr.StopSession(context.Background(), 0); err != nil {
		t.Fatalf("StopSession(0) error = %v", err)
	}

	reloaded := NewDeviceSessionManager(mgr.apiClient, dir)
	reloaded.LoadPersistedSession()
	t.Cleanup(func() { reloaded.StopIdleTimer(1) })
	ended, ok := reloaded.EndedSessionAtIndex(0)
	if !ok || ended.SessionID != "session-0-0000" || ended.Reason != EndedSessionStopped {
		t.Fatalf("ended session at index 0 = %+v (found %v), want the locally stopped session", ended, ok)
	}
	if live := reloaded.GetSession(1); live == nil || !live.StartedAt.Before(ended.EndedAt) {
		t.Fatalf("live session = %+v, want it to have started before index 0 was stopped", live)
	}
	if _, err := mgr.ResolveSession(0); err == nil || !strings.Contains(err.Error(), "Session 0 (ios session-) ended 0s ago because a stop was requested") {
		t.Fatalf("ResolveSession(0) error = %v, want the stop explained", err)
	}
}
