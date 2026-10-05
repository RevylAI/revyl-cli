package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// singleLiveSessionFlow serves exactly one live Android session and records
// which worker actions and stops reached it.
type singleLiveSessionFlow struct {
	Server      *httptest.Server
	Screenshots atomic.Int32
	CancelCalls atomic.Int32
}

func newSingleLiveSessionFlow(t *testing.T, startedAt time.Time) *singleLiveSessionFlow {
	t.Helper()
	flow := &singleLiveSessionFlow{}
	flow.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-1","email":"test@example.com"}`))
		case r.URL.Path == "/api/v1/execution/device-sessions/active":
			_, _ = w.Write([]byte(`{"org_id":"org-1","sessions":[{"id":"` + secondFlowSessionID + `","org_id":"org-1","platform":"android","source":"cli","status":"running","workflow_run_id":"` + secondFlowWorkflowRunID + `","user_email":"test@example.com","created_at":"` + startedAt.Format(time.RFC3339) + `","started_at":"` + startedAt.Format(time.RFC3339) + `"}]}`))
		case r.URL.Path == "/api/v1/execution/device-proxy/"+secondFlowWorkflowRunID+"/screenshot":
			flow.Screenshots.Add(1)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("png-bytes"))
		case strings.HasPrefix(r.URL.Path, "/api/v1/execution/device/status/cancel/"):
			flow.CancelCalls.Add(1)
			_, _ = w.Write([]byte(`{"success":true,"request_accepted":true,"session_settled":true,"device_released":true}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(flow.Server.Close)
	return flow
}

// seedSessionCache writes a local cache holding the live Android session at
// index 1 and, optionally, a record that index 0's iOS session ended.
func seedSessionCache(t *testing.T, dir string, liveStartedAt time.Time, indexZeroEndedAt *time.Time) {
	t.Helper()
	state := map[string]interface{}{
		"active":     1,
		"next_index": 2,
		"org_id":     "org-1",
		"user_email": "test@example.com",
		"sessions": []map[string]interface{}{{
			"index": 1, "session_id": secondFlowSessionID, "workflow_run_id": secondFlowWorkflowRunID,
			"platform": "android", "started_at": liveStartedAt.Format(time.RFC3339Nano),
		}},
	}
	if indexZeroEndedAt != nil {
		state["ended_sessions"] = []map[string]interface{}{{
			"index": 0, "session_id": flowSessionID, "platform": "ios",
			"ended_at": indexZeroEndedAt.Format(time.RFC3339Nano), "reason": "idle_timeout", "idle_timeout_seconds": 300,
		}}
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".revyl"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".revyl", "device-sessions.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStaleIndexAdoptsOnlyLiveSession(t *testing.T) {
	liveStartedAt := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	endedBeforeLiveStarted := liveStartedAt.Add(-5 * time.Minute)
	endedWhileLiveRan := liveStartedAt.Add(5 * time.Minute)

	testCases := []struct {
		name      string
		endedAt   *time.Time
		wantAdopt bool
	}{
		{name: "stale index with no ended record", wantAdopt: true},
		{name: "live session replaced the ended one", endedAt: &endedBeforeLiveStarted, wantAdopt: true},
		{name: "live session ran alongside the ended one", endedAt: &endedWhileLiveRan, wantAdopt: false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			withWorkingDirectory(t, dir)
			seedSessionCache(t, dir, liveStartedAt, tc.endedAt)
			t.Setenv("REVYL_API_KEY", "test-api-key")
			t.Setenv(sessionIDEnvVar, "")
			flow := newSingleLiveSessionFlow(t, liveStartedAt)
			t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)

			cmd := newStopTestCommand(t, "--json", "-s", "0")
			cmd.Flags().String("out", "", "")
			stdout, stderr, err, terminal := runStopWithAnalytics(t, cmd, deviceScreenshotCmd.RunE, nil)

			if !tc.wantAdopt {
				if err == nil || !strings.Contains(err.Error(), "was already running before that session ended") ||
					!strings.Contains(err.Error(), "Session 0 (ios 9f3c1a2b) ended") || !strings.Contains(err.Error(), "pass -s 1") {
					t.Fatalf("error = %v, want the ended-session explanation and the refused fallback", err)
				}
				if flow.Screenshots.Load() != 0 {
					t.Fatal("a refused fallback still sent a worker action to the other session")
				}
				if terminal["domain_status"] == "adopted_only_active_session" {
					t.Fatalf("terminal analytics = %+v, want no adoption", terminal)
				}
				return
			}

			if err != nil {
				t.Fatalf("device screenshot error = %v, want the only live session to be used", err)
			}
			if flow.Screenshots.Load() != 1 {
				t.Fatalf("screenshots = %d, want 1 on the live session", flow.Screenshots.Load())
			}
			wantNotice := "Using session 1, android aaaaaaaa (index 0 is stale and this is the only active session). Pass -s <index or session ID> to choose another."
			if !strings.Contains(stderr, wantNotice) {
				t.Fatalf("stderr = %q, want %q", stderr, wantNotice)
			}
			var payload struct {
				Bytes           int              `json:"bytes"`
				DefaultsApplied []appliedDefault `json:"defaults_applied"`
			}
			if jsonErr := json.Unmarshal([]byte(stdout), &payload); jsonErr != nil {
				t.Fatalf("stdout is not JSON: %v\n%s", jsonErr, stdout)
			}
			want := appliedDefault{Flag: "s", Value: "1", Reason: "stale_index_only_active_session"}
			if payload.Bytes != len("png-bytes") || len(payload.DefaultsApplied) != 1 || payload.DefaultsApplied[0] != want {
				t.Fatalf("stdout = %s, want the screenshot result plus defaults_applied %+v", stdout, want)
			}
			if terminal["domain"] != "device_session_target" || terminal["domain_status"] != "adopted_only_active_session" {
				t.Fatalf("terminal analytics = %+v, want the adoption outcome", terminal)
			}
		})
	}
}

func TestDeviceStopNeverAdoptsAnotherSessionForAStaleIndex(t *testing.T) {
	liveStartedAt := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	seedSessionCache(t, dir, liveStartedAt, nil)
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	flow := newSingleLiveSessionFlow(t, liveStartedAt)
	t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)

	cmd := newStopTestCommand(t, "--json", "-s", "0")
	stdout, _, err, _ := runStopWithAnalytics(t, cmd, deviceStopCmd.RunE, nil)
	if err != nil {
		t.Fatalf("device stop error = %v, want nothing to stop", err)
	}
	if flow.CancelCalls.Load() != 0 {
		t.Fatalf("cancel calls = %d, a stale index must never stop the other live session", flow.CancelCalls.Load())
	}
	if !strings.Contains(stdout, `"already_stopped": true`) || strings.Contains(stdout, "defaults_applied") {
		t.Fatalf("stdout = %s, want a no-op stop without an applied default", stdout)
	}
}

func TestWithAppliedDefaultsKeepsExistingKeys(t *testing.T) {
	cmd := newStopTestCommand(t)
	payload := map[string]interface{}{"path": "screen.png", "bytes": "12"}
	if got := withAppliedDefaults(cmd, payload); !mapsEqualJSON(t, got, payload) {
		t.Fatalf("payload without applied defaults changed: %v", got)
	}

	recordAppliedDefault(cmd, appliedDefault{Flag: "s", Value: "1", Reason: "stale_index_only_active_session"})
	encoded, err := json.Marshal(withAppliedDefaults(cmd, payload))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["path"] != "screen.png" || decoded["bytes"] != "12" {
		t.Fatalf("existing keys changed: %s", encoded)
	}
	applied, ok := decoded["defaults_applied"].([]interface{})
	if !ok || len(applied) != 1 {
		t.Fatalf("defaults_applied = %v, want one entry", decoded["defaults_applied"])
	}
	entry, _ := applied[0].(map[string]interface{})
	if entry["flag"] != "s" || entry["value"] != "1" || entry["reason"] != "stale_index_only_active_session" || len(entry) != 3 {
		t.Fatalf("defaults_applied entry = %v, want flag, value, and reason only", entry)
	}

	list := []string{"a"}
	if got := withAppliedDefaults(cmd, list); !mapsEqualJSON(t, got, list) {
		t.Fatalf("a non-object payload changed: %v", got)
	}
}

func mapsEqualJSON(t *testing.T, left, right interface{}) bool {
	t.Helper()
	leftJSON, err := json.Marshal(left)
	if err != nil {
		t.Fatal(err)
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		t.Fatal(err)
	}
	return string(leftJSON) == string(rightJSON)
}

func TestStaleIndexDoesNotAdoptWhileAnotherSessionIsStarting(t *testing.T) {
	const startingSessionID = "eeeeeeee-1111-4222-8333-444444444444"
	const startingWorkflowRunID = "55555555-6666-4777-8888-999999999999"
	liveStartedAt := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	seedSessionCache(t, dir, liveStartedAt, nil)
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	var screenshots atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-1","email":"test@example.com"}`))
		case r.URL.Path == "/api/v1/execution/device-sessions/active":
			_, _ = w.Write([]byte(`{"org_id":"org-1","sessions":[
				{"id":"` + secondFlowSessionID + `","org_id":"org-1","platform":"android","source":"cli","status":"running","workflow_run_id":"` + secondFlowWorkflowRunID + `","user_email":"test@example.com","created_at":"` + liveStartedAt.Format(time.RFC3339) + `","started_at":"` + liveStartedAt.Format(time.RFC3339) + `"},
				{"id":"` + startingSessionID + `","org_id":"org-1","platform":"ios","source":"cli","status":"starting","workflow_run_id":"` + startingWorkflowRunID + `","user_email":"test@example.com","created_at":"` + time.Now().Format(time.RFC3339) + `"}]}`))
		case r.URL.Path == "/api/v1/execution/streaming/worker-connection/"+startingWorkflowRunID:
			_, _ = w.Write([]byte(`{"status":"not_ready","workflow_run_id":"` + startingWorkflowRunID + `"}`))
		case strings.HasSuffix(r.URL.Path, "/screenshot"):
			screenshots.Add(1)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("png-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	cmd := newStopTestCommand(t, "--json", "-s", "0")
	cmd.Flags().String("out", "", "")
	_, _, err, terminal := runStopWithAnalytics(t, cmd, deviceScreenshotCmd.RunE, nil)
	if err == nil || !strings.Contains(err.Error(), "Session eeeeeeee is still starting, so 1 (android aaaaaaaa) is not the only live session") {
		t.Fatalf("error = %v, want the stale index refused while another session is starting", err)
	}
	if screenshots.Load() != 0 {
		t.Fatal("a refused fallback still sent a worker action")
	}
	if terminal["domain_status"] == "adopted_only_active_session" {
		t.Fatalf("terminal analytics = %+v, want no adoption", terminal)
	}
}
