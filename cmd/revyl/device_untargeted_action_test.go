package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

type twoLiveSessionsFlow struct {
	Server *httptest.Server
	mu     sync.Mutex
	shots  []string
}

func newTwoLiveSessionsFlow(t *testing.T) *twoLiveSessionsFlow {
	t.Helper()
	flow := &twoLiveSessionsFlow{}
	flow.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-1","email":"test@example.com"}`))
		case r.URL.Path == "/api/v1/execution/device-sessions/active":
			_, _ = w.Write([]byte(`{"org_id":"org-1","sessions":[
				{"id":"` + flowSessionID + `","org_id":"org-1","platform":"ios","status":"running","workflow_run_id":"` + flowWorkflowRunID + `","user_email":"test@example.com","created_at":"2026-02-19T00:00:00Z","started_at":"2026-02-19T00:00:00Z"},
				{"id":"` + secondFlowSessionID + `","org_id":"org-1","platform":"android","status":"running","workflow_run_id":"` + secondFlowWorkflowRunID + `","user_email":"test@example.com","created_at":"2026-02-19T00:01:00Z","started_at":"2026-02-19T00:01:00Z"}]}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/execution/streaming/worker-connection/"):
			runID := strings.TrimPrefix(r.URL.Path, "/api/v1/execution/streaming/worker-connection/")
			_, _ = w.Write([]byte(`{"status":"ready","workflow_run_id":"` + runID + `","worker_ws_url":"ws://` + r.Host + `/ws/stream?token=test"}`))
		case strings.HasSuffix(r.URL.Path, "/health"):
			_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
		case strings.HasSuffix(r.URL.Path, "/screenshot"):
			flow.mu.Lock()
			flow.shots = append(flow.shots, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/execution/device-proxy/"), "/screenshot"))
			flow.mu.Unlock()
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("png"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(flow.Server.Close)
	return flow
}

func newScreenshotTestCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "revyl"}
	device := &cobra.Command{Use: "device"}
	cmd := &cobra.Command{Use: "screenshot"}
	cmd.SetContext(context.Background())
	root.AddCommand(device)
	device.AddCommand(cmd)
	cmd.Flags().Bool("json", false, "")
	cmd.Flags().Bool("dev", false, "")
	cmd.Flags().String("out", "", "")
	registerSessionTargetFlags(cmd)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestUntargetedActionInAFreshDirectoryRefusesSeveralSessions(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	flow := newTwoLiveSessionsFlow(t)
	t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)

	stdout, _, err, terminal := runStopWithAnalytics(t, newScreenshotTestCommand(t, "--json"), deviceScreenshotCmd.RunE, nil)
	if err == nil {
		t.Fatal("device screenshot error = nil, want a refusal instead of the account's oldest session")
	}
	for _, want := range []string{
		"multiple device sessions are live and none was started or selected in this directory, so 'revyl device screenshot' did not pick one",
		"revyl device use <index>",
		"revyl device screenshot -s " + flowSessionID,
		"revyl device screenshot -s " + secondFlowSessionID,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}
	if len(flow.shots) != 0 || stdout != "" {
		t.Fatalf("screenshots = %v, stdout = %q; want no device action and the unchanged --json failure shape", flow.shots, stdout)
	}
	if terminal["domain"] != "device_session_target" || terminal["domain_status"] != "refused_none_started_here" {
		t.Fatalf("terminal analytics = %+v, want the refusal outcome", terminal)
	}
}

func seedDirectoryTargeting(t *testing.T, dir, lastUsed string) {
	t.Helper()
	state := map[string]interface{}{
		"active": 0, "next_index": 2, "org_id": "org-1", "user_email": "test@example.com",
		"sessions": []map[string]interface{}{
			{"index": 0, "session_id": flowSessionID, "workflow_run_id": flowWorkflowRunID, "platform": "ios"},
			{"index": 1, "session_id": secondFlowSessionID, "workflow_run_id": secondFlowWorkflowRunID, "platform": "android"},
		},
		"started_here": []map[string]string{{"session_id": secondFlowSessionID, "workflow_run_id": secondFlowWorkflowRunID}},
		"selected":     map[string]string{"session_id": secondFlowSessionID, "workflow_run_id": secondFlowWorkflowRunID},
	}
	if lastUsed != "" {
		state["last_untargeted"] = map[string]string{"session_id": lastUsed}
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

func TestUntargetedActionUsesTheSessionThisDirectoryStarted(t *testing.T) {
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	seedDirectoryTargeting(t, dir, flowSessionID)
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	flow := newTwoLiveSessionsFlow(t)
	t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)

	stdout, stderr, err, terminal := runStopWithAnalytics(t, newScreenshotTestCommand(t, "--json"), deviceScreenshotCmd.RunE, nil)
	if err != nil {
		t.Fatalf("device screenshot error = %v", err)
	}
	if len(flow.shots) != 1 || flow.shots[0] != secondFlowWorkflowRunID {
		t.Fatalf("screenshots = %v, want one on the session this directory started, not the active index 0", flow.shots)
	}
	wantNotice := "Using session 1, android aaaaaaaa (last started or selected in this directory). Pass -s <index or session ID> to choose another."
	if !strings.Contains(stderr, wantNotice) {
		t.Fatalf("stderr = %q, want %q because the choice changed", stderr, wantNotice)
	}
	var payload struct {
		Bytes           int              `json:"bytes"`
		DefaultsApplied []appliedDefault `json:"defaults_applied"`
	}
	if jsonErr := json.Unmarshal([]byte(stdout), &payload); jsonErr != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", jsonErr, stdout)
	}
	want := appliedDefault{Flag: "s", Value: secondFlowSessionID, Reason: "selected_here"}
	if len(payload.DefaultsApplied) != 1 || payload.DefaultsApplied[0] != want {
		t.Fatalf("stdout = %s, want defaults_applied %+v", stdout, want)
	}
	if terminal["domain"] != "device_session_target" || terminal["domain_status"] != "selected_here" {
		t.Fatalf("terminal analytics = %+v, want selected_here", terminal)
	}

	stdout, stderr, err, _ = runStopWithAnalytics(t, newScreenshotTestCommand(t, "--json"), deviceScreenshotCmd.RunE, nil)
	if err != nil || strings.Contains(stderr, "Using session") || strings.Contains(stdout, "defaults_applied") {
		t.Fatalf("repeat screenshot = %v, stderr %q, stdout %s; want the same session with no notice", err, stderr, stdout)
	}
	if len(flow.shots) != 2 || flow.shots[1] != secondFlowWorkflowRunID {
		t.Fatalf("screenshots = %v, want both on the session this directory started", flow.shots)
	}
}

func TestExplicitTargetsStillWinOverDirectoryTargeting(t *testing.T) {
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	seedDirectoryTargeting(t, dir, "")
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	flow := newTwoLiveSessionsFlow(t)
	t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)

	var runErr error
	captureStdoutAndStderrSeparate(t, func() { runErr = deviceScreenshotCmd.RunE(newScreenshotTestCommand(t, "-s", "0"), nil) })
	if runErr != nil {
		t.Fatalf("device screenshot -s 0 error = %v", runErr)
	}
	if len(flow.shots) != 1 || flow.shots[0] != flowWorkflowRunID {
		t.Fatalf("screenshots = %v, want -s 0 to target index 0", flow.shots)
	}
}

func TestDeviceStartSelectsTheNewSessionForThisDirectory(t *testing.T) {
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	seedSessionCache(t, dir, time.Now().Add(-time.Hour), nil)

	const workflowRunID = "00000000-0000-0000-0000-000000000051"
	const sessionID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaa51"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/execution/start_device":
			_, _ = w.Write([]byte(`{"workflow_run_id":"` + workflowRunID + `","session_id":"` + sessionID + `"}`))
		case "/api/v1/execution/streaming/worker-connection/" + workflowRunID:
			_, _ = w.Write([]byte(`{"status":"ready","workflow_run_id":"` + workflowRunID + `","worker_ws_url":"ws://` + r.Host + `/ws/stream?token=test"}`))
		case "/api/v1/execution/device-proxy/" + workflowRunID + "/health":
			_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	cmd := newDeviceStartTestCommand(context.Background())
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	var runErr error
	captureStdout(t, func() { runErr = deviceStartCmd.RunE(cmd, nil) })
	if runErr != nil {
		t.Fatalf("device start error = %v", runErr)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".revyl", "device-sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Active      int                 `json:"active"`
		Selected    map[string]string   `json:"selected"`
		StartedHere []map[string]string `json:"started_here"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Selected["session_id"] != sessionID || len(state.StartedHere) != 1 || state.StartedHere[0]["session_id"] != sessionID {
		t.Fatalf("session store = %s, want the new session selected and recorded as started here", data)
	}
	if state.Active != 1 {
		t.Fatalf("active = %d, want the MCP-facing active pointer unchanged at the first session", state.Active)
	}
}

func TestUntargetedDeviceReportUsesTheSessionThisDirectoryStarted(t *testing.T) {
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	seedDirectoryTargeting(t, dir, "")
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	flow := newTwoLiveSessionsFlow(t)
	var reported []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/reports-v3/reports/by-session/") {
			mu.Lock()
			reported = append(reported, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/reports-v3/reports/by-session/"), "/context"))
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"report":null}`))
			return
		}
		flow.Server.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	cmd := newDeviceReportTestCommand(context.Background())
	var runErr error
	captureStdoutAndStderrSeparate(t, func() { runErr = deviceReportCmd.RunE(cmd, nil) })
	if runErr != nil {
		t.Fatalf("device report error = %v", runErr)
	}
	if len(reported) != 1 || reported[0] != secondFlowSessionID {
		t.Fatalf("reported sessions = %v, want the session this directory started", reported)
	}
}

func TestUntargetedRefusalUsesTheFlagTheCommandAccepts(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	flow := newTwoLiveSessionsFlow(t)
	t.Setenv("REVYL_BACKEND_URL", flow.Server.URL)

	root := &cobra.Command{Use: "revyl"}
	report := &cobra.Command{Use: "report"}
	annotations := &cobra.Command{Use: "annotations"}
	list := &cobra.Command{Use: "list"}
	root.AddCommand(report)
	report.AddCommand(annotations)
	annotations.AddCommand(list)
	list.SetContext(context.Background())
	list.Flags().String(sessionIDFlagName, "", "")
	list.Flags().Bool("json", false, "")

	_, err := resolveReportAnnotationSessionID(list)
	if err == nil || !strings.Contains(err.Error(), "revyl report annotations list --session-id "+flowSessionID) {
		t.Fatalf("error = %v, want retries that use --session-id", err)
	}
	if strings.Contains(err.Error(), " -s ") {
		t.Fatalf("error = %q suggests -s, which this command does not accept", err)
	}
}

func TestUntargetedReportAnnotationsJSONNamesTheSessionIDFlag(t *testing.T) {
	dir := t.TempDir()
	withWorkingDirectory(t, dir)
	seedDirectoryTargeting(t, dir, flowSessionID)
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	flow := newTwoLiveSessionsFlow(t)
	const appID = "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"
	var listedSession string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/reports-v3/reports/by-session/" + secondFlowSessionID + "/context":
			_, _ = w.Write([]byte(`{"app_name":"` + appID + `"}`))
		case "/api/v1/apps/" + appID:
			_, _ = w.Write([]byte(`{"id":"` + appID + `","name":"checkout","platform":"android","latest_version":"1.0.0","versions_count":1}`))
		case "/api/v1/atlas/v2/annotations/feedback":
			listedSession = r.URL.Query().Get("session_id")
			_, _ = w.Write([]byte(`{"items":[],"open_count":0,"closed_count":0}`))
		default:
			flow.Server.Config.Handler.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	root := &cobra.Command{Use: "revyl", SilenceUsage: true}
	root.AddCommand(newReportCommand())
	root.SetArgs([]string{"report", "annotations", "list", "--json"})
	var runErr error
	stdout, stderr := captureStdoutAndStderrSeparate(t, func() { runErr = root.Execute() })
	if runErr != nil {
		t.Fatalf("report annotations list error = %v", runErr)
	}
	if listedSession != secondFlowSessionID {
		t.Fatalf("listed session = %q, want the session this directory started", listedSession)
	}
	if !strings.Contains(stderr, "Pass --session-id <session ID> to choose another.") {
		t.Fatalf("stderr = %q, want the notice to name --session-id", stderr)
	}
	var payload struct {
		DefaultsApplied []appliedDefault `json:"defaults_applied"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	want := appliedDefault{Flag: "session-id", Value: secondFlowSessionID, Reason: "selected_here"}
	if len(payload.DefaultsApplied) != 1 || payload.DefaultsApplied[0] != want {
		t.Fatalf("stdout = %s, want defaults_applied %+v", stdout, want)
	}
}
