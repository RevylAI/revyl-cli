package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestUntargetedDeviceStopRefusesSeveralLiveSessions(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	flow := newInventoryStopAllServer(t)
	t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)

	cmd := newStopTestCommand(t, "--json")
	stdout, _, err, terminal := runStopWithAnalytics(t, cmd, deviceStopCmd.RunE, nil)
	if err == nil {
		t.Fatal("device stop error = nil, want a refusal while two sessions are live")
	}
	for _, want := range []string{
		"multiple device sessions are live, so 'revyl device stop' did not pick one",
		"revyl device stop --all",
		"9f3c1a2b  ios",
		"revyl device stop -s " + flowSessionID,
		"aaaaaaaa  android",
		"revyl device stop -s " + secondFlowSessionID,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}
	if flow.CancelCalls.Load() != 0 {
		t.Fatalf("cancel calls = %d, want none without a target", flow.CancelCalls.Load())
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want the unchanged --json failure shape (no document)", stdout)
	}
	if terminal["domain"] != "device_session_stop" || terminal["domain_status"] != "multiple_live_sessions" {
		t.Fatalf("terminal analytics = %+v, want the multiple_live_sessions outcome", terminal)
	}
}

func TestTargetedDeviceStopsIgnoreOtherLiveSessions(t *testing.T) {
	for _, args := range [][]string{{"--json", "-s", "1"}, {"--json", "--all"}} {
		withWorkingDirectory(t, t.TempDir())
		t.Setenv("REVYL_API_KEY", "test-api-key")
		t.Setenv(sessionIDEnvVar, "")
		flow := newInventoryStopAllServer(t)
		t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)

		cmd := newStopTestCommand(t, args...)
		var stopErr error
		captureStdout(t, func() { stopErr = deviceStopCmd.RunE(cmd, nil) })
		if stopErr != nil {
			t.Fatalf("device stop %v error = %v, want the targeted stop to proceed", args, stopErr)
		}
	}
}

func TestUntargetedDeviceStopStopsTheOnlyLiveSession(t *testing.T) {
	liveStartedAt := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	seedSessionCache(t, dir, liveStartedAt, nil)
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	flow := newSingleLiveSessionFlow(t, liveStartedAt)
	t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)

	cmd := newStopTestCommand(t, "--json")
	var stopErr error
	stdout := captureStdout(t, func() { stopErr = deviceStopCmd.RunE(cmd, nil) })
	if stopErr != nil {
		t.Fatalf("device stop error = %v, want the only live session stopped", stopErr)
	}
	if flow.CancelCalls.Load() != 1 || !strings.Contains(stdout, `"stopped": true`) {
		t.Fatalf("cancel calls = %d, stdout = %s; want one confirmed stop", flow.CancelCalls.Load(), stdout)
	}
}

func TestUntargetedDeviceStopCountsStartingSessionsAsLive(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	var cancels int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-1","email":"test@example.com"}`))
		case r.URL.Path == "/api/v1/execution/device-sessions/active":
			_, _ = w.Write([]byte(`{"org_id":"org-1","sessions":[
				{"id":"` + flowSessionID + `","org_id":"org-1","platform":"ios","status":"running","workflow_run_id":"` + flowWorkflowRunID + `","user_email":"test@example.com","created_at":"2026-02-19T00:00:00Z"},
				{"id":"` + secondFlowSessionID + `","org_id":"org-1","platform":"android","status":"starting","workflow_run_id":"` + secondFlowWorkflowRunID + `","user_email":"test@example.com","created_at":"2026-02-19T00:01:00Z"}]}`))
		case r.URL.Path == "/api/v1/execution/streaming/worker-connection/"+flowWorkflowRunID:
			_, _ = w.Write([]byte(`{"status":"ready","workflow_run_id":"` + flowWorkflowRunID + `","worker_ws_url":"ws://` + r.Host + `/ws/stream?token=test"}`))
		case r.URL.Path == "/api/v1/execution/device-proxy/"+flowWorkflowRunID+"/health":
			_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
		case r.URL.Path == "/api/v1/execution/streaming/worker-connection/"+secondFlowWorkflowRunID:
			_, _ = w.Write([]byte(`{"status":"not_ready","workflow_run_id":"` + secondFlowWorkflowRunID + `"}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/execution/device/status/cancel/"):
			cancels++
			_, _ = w.Write([]byte(`{"success":true,"request_accepted":true,"session_settled":true,"device_released":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	cmd := newStopTestCommand(t, "--json")
	var stopErr error
	captureStdout(t, func() { stopErr = deviceStopCmd.RunE(cmd, nil) })
	if stopErr == nil || !strings.Contains(stopErr.Error(), "aaaaaaaa  android  still starting") {
		t.Fatalf("device stop error = %v, want the starting session listed", stopErr)
	}
	if cancels != 0 {
		t.Fatalf("cancel calls = %d, want none", cancels)
	}
}

func saveTestDevContext(t *testing.T, dir string, ctx *DevContext) {
	t.Helper()
	if err := saveDevContext(dir, ctx); err != nil {
		t.Fatal(err)
	}
}

func newDevStopTestCommand(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "stop"}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("json", true, "")
	cmd.Flags().String("context", "", "")
	return cmd
}

func TestUntargetedDevStopRefusesSeveralLiveContexts(t *testing.T) {
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("REVYL_API_KEY", "")
	previousAll := devStopAll
	devStopAll = false
	t.Cleanup(func() { devStopAll = previousAll })

	created := time.Now().Add(-12 * time.Minute)
	saveTestDevContext(t, dir, &DevContext{Name: "ios-main", Platform: "ios", SessionID: flowSessionID, SessionOwned: true, State: devContextStateStopped, CreatedAt: created})
	saveTestDevContext(t, dir, &DevContext{Name: "default-2", Platform: "android", SessionID: secondFlowSessionID, SessionOwned: true, State: devContextStateStopped, CreatedAt: created})
	saveTestDevContext(t, dir, &DevContext{Name: "old", Platform: "ios", State: devContextStateStopped})
	if err := setCurrentDevContext(dir, "default-2"); err != nil {
		t.Fatal(err)
	}

	cmd := newDevStopTestCommand(t)
	stdout, _, err, terminal := runStopWithAnalytics(t, cmd, runDevStop, nil)
	if err == nil {
		t.Fatal("dev stop error = nil, want a refusal while two contexts hold sessions")
	}
	for _, want := range []string{
		"multiple dev contexts are live in this worktree, so 'revyl dev stop' did not pick one",
		"revyl dev stop --all",
		"revyl dev stop ios-main",
		"revyl dev stop default-2",
		"holds its device session, started 12m ago",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "revyl dev stop old") {
		t.Fatalf("error = %q lists a stopped context", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want the unchanged --json failure shape", stdout)
	}
	if terminal["domain"] != "dev_stop" || terminal["domain_status"] != "multiple_live_contexts" {
		t.Fatalf("terminal analytics = %+v, want the multiple_live_contexts outcome", terminal)
	}
	for _, name := range []string{"ios-main", "default-2"} {
		ctx, loadErr := loadDevContext(dir, name)
		if loadErr != nil || ctx.SessionID == "" {
			t.Fatalf("context %s = %+v, %v; want it left untouched", name, ctx, loadErr)
		}
	}

	stdout = captureStdout(t, func() { err = runDevStop(newDevStopTestCommand(t), []string{"ios-main"}) })
	if err != nil || !strings.Contains(stdout, `"context": "ios-main"`) {
		t.Fatalf("dev stop ios-main = %v, stdout %s; want the named context stopped", err, stdout)
	}
	var payload map[string]interface{}
	if jsonErr := json.Unmarshal([]byte(stdout), &payload); jsonErr != nil || payload["stopped"] != true {
		t.Fatalf("dev stop ios-main stdout = %s, want stopped=true", stdout)
	}
}

func TestUntargetedDevStopWithOneLiveContextKeepsTodaysBehavior(t *testing.T) {
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	saveTestDevContext(t, dir, &DevContext{Name: "ios-main", Platform: "ios", SessionID: flowSessionID, SessionOwned: true})
	saveTestDevContext(t, dir, &DevContext{Name: "old", Platform: "ios", SessionID: secondFlowSessionID, SessionOwned: false})
	if err := os.WriteFile(filepath.Join(dir, ".revyl", devContextsDir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := soleLiveDevContextForUntargetedStop(newDevStopTestCommand(t), dir); err != nil {
		t.Fatalf("one live context (and one attached, not owned) = %v, want no refusal", err)
	}
}

func TestUntargetedDeviceStopWithUnreadableInventorySaysSessionsAreCached(t *testing.T) {
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	t.Setenv("REVYL_BACKEND_URL", newEmptyInventoryServer(t, http.StatusForbidden).URL)
	state := `{"active":0,"next_index":2,"org_id":"org-1","user_email":"test@example.com","sessions":[
		{"index":0,"session_id":"` + flowSessionID + `","workflow_run_id":"` + flowWorkflowRunID + `","platform":"ios"},
		{"index":1,"session_id":"` + secondFlowSessionID + `","workflow_run_id":"` + secondFlowWorkflowRunID + `","platform":"android"}]}`
	if err := os.MkdirAll(filepath.Join(dir, ".revyl"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".revyl", "device-sessions.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newStopTestCommand(t, "--json")
	_, _, err, terminal := runStopWithAnalytics(t, cmd, deviceStopCmd.RunE, nil)
	if err == nil || !strings.Contains(err.Error(), "could not read your live sessions from Revyl and 2 sessions are cached here, so 'revyl device stop' did not pick one") {
		t.Fatalf("device stop error = %v, want the unconfirmed-cache refusal", err)
	}
	if terminal["domain_status"] != "multiple_cached_sessions" {
		t.Fatalf("terminal analytics = %+v, want multiple_cached_sessions", terminal)
	}
}

func TestUntargetedDevStopIgnoresContextsWhoseSessionEnded(t *testing.T) {
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	flow := newSingleLiveSessionFlow(t, time.Now().Add(-time.Minute))
	t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)
	seedSessionCache(t, dir, time.Now().Add(-time.Minute), nil)

	saveTestDevContext(t, dir, &DevContext{Name: "crashed", Platform: "ios", SessionID: flowSessionID, SessionOwned: true, State: devContextStateStopped})
	saveTestDevContext(t, dir, &DevContext{Name: "android-main", Platform: "android", SessionID: secondFlowSessionID, SessionOwned: true, State: devContextStateStopped})

	cmd := newDevStopTestCommand(t)
	if _, err := soleLiveDevContextForUntargetedStop(cmd, dir); err != nil {
		t.Fatalf("one context whose session Revyl still lists = %v, want no refusal", err)
	}
}

func TestUntargetedDevStopStopsTheOnlyLiveContextWhenTheCurrentOneIsDead(t *testing.T) {
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	previousAll := devStopAll
	devStopAll = false
	t.Cleanup(func() { devStopAll = previousAll })
	flow := newSingleLiveSessionFlow(t, time.Now().Add(-time.Minute))
	t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)
	seedSessionCache(t, dir, time.Now().Add(-time.Minute), nil)

	saveTestDevContext(t, dir, &DevContext{Name: "android-main", Platform: "android", SessionID: secondFlowSessionID, SessionOwned: true, State: devContextStateStopped})
	saveTestDevContext(t, dir, &DevContext{Name: "crashed", Platform: "ios", SessionID: flowSessionID, SessionOwned: true, State: devContextStateStopped})
	if err := setCurrentDevContext(dir, "crashed"); err != nil {
		t.Fatal(err)
	}

	var stopErr error
	stdout, stderr := captureStdoutAndStderrSeparate(t, func() { stopErr = runDevStop(newDevStopTestCommand(t), nil) })
	if stopErr != nil {
		t.Fatalf("dev stop error = %v", stopErr)
	}
	if flow.CancelCalls.Load() != 1 {
		t.Fatalf("cancel calls = %d, want the live context's device session stopped", flow.CancelCalls.Load())
	}
	wantNotice := "Using dev context 'android-main' (the only live context in this worktree; the current context 'crashed' is not running). Pass a context name to choose another."
	if !strings.Contains(stderr, wantNotice) {
		t.Fatalf("stderr = %q, want %q", stderr, wantNotice)
	}
	var payload struct {
		Context         string           `json:"context"`
		Stopped         bool             `json:"stopped"`
		DefaultsApplied []appliedDefault `json:"defaults_applied"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	want := appliedDefault{Flag: "context", Value: "android-main", Reason: "only_live_context"}
	if payload.Context != "android-main" || !payload.Stopped || len(payload.DefaultsApplied) != 1 || payload.DefaultsApplied[0] != want {
		t.Fatalf("stdout = %s, want android-main stopped with defaults_applied %+v", stdout, want)
	}
}

func TestUntargetedDevStopKeepsFailingOnAnUnreadableCurrentContext(t *testing.T) {
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	previousAll := devStopAll
	devStopAll = false
	t.Cleanup(func() { devStopAll = previousAll })
	flow := newSingleLiveSessionFlow(t, time.Now().Add(-time.Minute))
	t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)
	seedSessionCache(t, dir, time.Now().Add(-time.Minute), nil)

	saveTestDevContext(t, dir, &DevContext{Name: "android-main", Platform: "android", SessionID: secondFlowSessionID, SessionOwned: true, State: devContextStateStopped})
	saveTestDevContext(t, dir, &DevContext{Name: "current", Platform: "ios", State: devContextStateRunning})
	if err := os.WriteFile(filepath.Join(dir, ".revyl", devContextsDir, "current", devContextMetaFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setCurrentDevContext(dir, "current"); err != nil {
		t.Fatal(err)
	}

	var stopErr error
	captureStdoutAndStderrSeparate(t, func() { stopErr = runDevStop(newDevStopTestCommand(t), nil) })
	if stopErr == nil || !strings.Contains(stopErr.Error(), "could not read dev context 'current'") {
		t.Fatalf("dev stop error = %v, want the unreadable current context to fail as before", stopErr)
	}
	if flow.CancelCalls.Load() != 0 {
		t.Fatalf("cancel calls = %d, want the other context's session left running", flow.CancelCalls.Load())
	}
}
