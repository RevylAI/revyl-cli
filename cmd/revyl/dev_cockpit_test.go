package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/revyl/cli/internal/build"
	mcppkg "github.com/revyl/cli/internal/mcp"
)

func TestDevCockpitRoutesAndTokenEnforcement(t *testing.T) {
	cwd := seedDevCockpitContext(t)
	rebuilds := make(chan struct{}, 1)
	stops := make(chan struct{}, 1)

	cockpit := startTestDevCockpit(t, devCockpitOptions{
		Port:         0,
		CWD:          cwd,
		ContextName:  "default",
		ViewerURL:    "https://app.revyl.ai/sessions/sess-123",
		BuildEnabled: true,
		TriggerRebuild: func() error {
			rebuilds <- struct{}{}
			return nil
		},
		Stop: func() {
			stops <- struct{}{}
		},
	})

	indexResp, err := http.Get(cockpit.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	indexBody := readAndClose(t, indexResp)
	if indexResp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, body=%s", indexResp.StatusCode, indexBody)
	}
	for _, expected := range []string{
		"Revyl dev",
		"Open in Revyl",
		`id="sessionFrame"`,
	} {
		if !strings.Contains(indexBody, expected) {
			t.Fatalf("GET / missing %q\nbody:\n%s", expected, indexBody)
		}
	}
	for _, unwanted := range []string{
		"revyl-cockpit-token",
		"Next action",
		"Rebuild",
		"Latest failure",
		"Dev context",
		"Commands",
		"Event log",
		"rebuildButton",
		"stopButton",
		"Copy agent context",
		"Copy rebuild JSON",
		"cmdScreenshot",
		"cmdInstruction",
		"Pass clicks",
		"No pass clicks",
		"tapOverlay",
		"/tap",
		"EventSource",
		"setInterval",
	} {
		if strings.Contains(indexBody, unwanted) {
			t.Fatalf("GET / unexpectedly contains %q\nbody:\n%s", unwanted, indexBody)
		}
	}

	apiResp, err := http.Get(cockpit.URL + "/api")
	if err != nil {
		t.Fatalf("GET /api: %v", err)
	}
	defer apiResp.Body.Close()
	if apiResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api status = %d", apiResp.StatusCode)
	}
	var snapshot devCockpitSnapshot
	if err := json.NewDecoder(apiResp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("Decode /api: %v", err)
	}
	if snapshot.Context != "default" {
		t.Fatalf("context = %q, want default", snapshot.Context)
	}
	if snapshot.Profile != "development" {
		t.Fatalf("profile = %q, want development", snapshot.Profile)
	}
	if snapshot.PlatformKey != "ios-dev" {
		t.Fatalf("platform_key = %q, want ios-dev", snapshot.PlatformKey)
	}
	if snapshot.ViewerURL != "https://app.revyl.ai/sessions/sess-123" {
		t.Fatalf("viewer_url = %q", snapshot.ViewerURL)
	}
	if snapshot.CockpitURL != cockpit.URL {
		t.Fatalf("cockpit_url = %q, want %q", snapshot.CockpitURL, cockpit.URL)
	}
	if !snapshot.RebuildAvailable {
		t.Fatal("rebuild_available = false, want true")
	}
	if snapshot.LastRebuildStatus != "success" {
		t.Fatalf("last_rebuild_status = %q, want success", snapshot.LastRebuildStatus)
	}
	if len(snapshot.RebuildLogs) == 0 {
		t.Fatal("rebuild_logs is empty")
	}
	if snapshot.NextAction == "" {
		t.Fatal("next_action is empty")
	}
	if !strings.Contains(snapshot.AgentContext, "Revyl dev context: default") {
		t.Fatalf("agent_context missing context: %q", snapshot.AgentContext)
	}
	if !strings.Contains(snapshot.AgentContext, "Profile: development") {
		t.Fatalf("agent_context missing profile: %q", snapshot.AgentContext)
	}
	if !strings.Contains(snapshot.AgentContext, "Rebuild command: revyl dev rebuild --wait --json") {
		t.Fatalf("agent_context missing rebuild command: %q", snapshot.AgentContext)
	}
	if snapshot.Commands["use"] != "revyl dev use default" {
		t.Fatalf("use command = %q", snapshot.Commands["use"])
	}
	if snapshot.Commands["rebuild"] != "revyl dev rebuild --wait" {
		t.Fatalf("rebuild command = %q", snapshot.Commands["rebuild"])
	}
	if _, ok := snapshot.Commands["screenshot"]; ok {
		t.Fatalf("screenshot command should not be in V2 commands: %#v", snapshot.Commands)
	}
	if _, ok := snapshot.Commands["instruction"]; ok {
		t.Fatalf("instruction command should not be in V2 commands: %#v", snapshot.Commands)
	}
	if len(snapshot.Commands) > 5 {
		t.Fatalf("commands length = %d, want at most 5: %#v", len(snapshot.Commands), snapshot.Commands)
	}

	redirectClient := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	viewerResp, err := redirectClient.Get(cockpit.URL + "/viewer")
	if err != nil {
		t.Fatalf("GET /viewer: %v", err)
	}
	_ = viewerResp.Body.Close()
	if viewerResp.StatusCode != http.StatusFound {
		t.Fatalf("/viewer status = %d, want 302", viewerResp.StatusCode)
	}
	if got := viewerResp.Header.Get("Location"); got != "https://app.revyl.ai/sessions/sess-123" {
		t.Fatalf("/viewer location = %q", got)
	}

	noTokenResp, err := http.Post(cockpit.URL+"/rebuild", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /rebuild without token: %v", err)
	}
	_ = noTokenResp.Body.Close()
	if noTokenResp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /rebuild without token status = %d, want 403", noTokenResp.StatusCode)
	}

	tapResp, err := http.Post(cockpit.URL+"/tap", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /tap: %v", err)
	}
	_ = tapResp.Body.Close()
	if tapResp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST /tap status = %d, want 404", tapResp.StatusCode)
	}

	noTokenStopResp, err := http.Post(cockpit.URL+"/stop", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /stop without token: %v", err)
	}
	_ = noTokenStopResp.Body.Close()
	if noTokenStopResp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /stop without token status = %d, want 403", noTokenStopResp.StatusCode)
	}

	postWithToken(t, cockpit, "/rebuild")
	select {
	case <-rebuilds:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for rebuild callback")
	}

	postWithToken(t, cockpit, "/stop")
	select {
	case <-stops:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stop callback")
	}

	eventsResp, err := http.Get(cockpit.URL + "/events")
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	_ = eventsResp.Body.Close()
	if eventsResp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /events status = %d, want 404", eventsResp.StatusCode)
	}
}

func TestDevCockpitPortFallback(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port

	cockpit := startTestDevCockpit(t, devCockpitOptions{
		Port:           port,
		PortSearchSpan: 20,
		CWD:            seedDevCockpitContext(t),
		ContextName:    "default",
		ViewerURL:      "https://app.revyl.ai/sessions/sess-123",
		BuildEnabled:   true,
		TriggerRebuild: func() error { return nil },
	})

	if cockpit.Port == port {
		t.Fatalf("cockpit used busy port %d", port)
	}
	if cockpit.Port < port || cockpit.Port > port+20 {
		t.Fatalf("cockpit port = %d, want in fallback range after %d", cockpit.Port, port)
	}
}

func TestDevCockpitFailureGuidance(t *testing.T) {
	cockpit := startTestDevCockpit(t, devCockpitOptions{
		Port:         0,
		CWD:          seedDevCockpitFailureContext(t),
		ContextName:  "default",
		ViewerURL:    "https://app.revyl.ai/sessions/sess-123",
		BuildEnabled: true,
		TriggerRebuild: func() error {
			return nil
		},
	})

	resp, err := http.Get(cockpit.URL + "/api")
	if err != nil {
		t.Fatalf("GET /api: %v", err)
	}
	defer resp.Body.Close()
	var snapshot devCockpitSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("Decode /api: %v", err)
	}
	if snapshot.LastRebuildStatus != "build_failed" {
		t.Fatalf("last_rebuild_status = %q, want build_failed", snapshot.LastRebuildStatus)
	}
	if !strings.Contains(snapshot.FailureSummary, `platform "" not found in config`) {
		t.Fatalf("failure_summary = %q", snapshot.FailureSummary)
	}
	if !strings.Contains(snapshot.NextAction, "revyl dev rebuild --wait --json") {
		t.Fatalf("next_action = %q", snapshot.NextAction)
	}
	for _, expected := range []string{
		"Revyl dev context: default",
		"Session: sess-123",
		"Profile: development",
		"Platform: ios / ios-dev",
		"Rebuild command: revyl dev rebuild --wait --json",
		`Latest error: platform "" not found in config`,
	} {
		if !strings.Contains(snapshot.AgentContext, expected) {
			t.Fatalf("agent_context missing %q\n%s", expected, snapshot.AgentContext)
		}
	}
	if len(snapshot.LastRebuild.BuildErrors) != 1 {
		t.Fatalf("build_errors len = %d, want 1", len(snapshot.LastRebuild.BuildErrors))
	}
	if len(snapshot.RebuildLogs) == 0 {
		t.Fatal("failure rebuild_logs is empty")
	}
	if snapshot.RebuildLogs[0].Kind != "error" {
		t.Fatalf("expected error rebuild log, got %#v", snapshot.RebuildLogs[0])
	}

	indexResp, err := http.Get(cockpit.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body := readAndClose(t, indexResp)
	for _, expected := range []string{
		"Revyl dev",
		"Open in Revyl",
		`id="sessionFrame"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("GET / missing %q\nbody:\n%s", expected, body)
		}
	}
	for _, unwanted := range []string{
		"Latest failure",
		"Copy rebuild JSON",
		"Copy agent context",
		"agentContextPreview",
		"platform \"\" not found in config",
	} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("GET / unexpectedly contains %q\nbody:\n%s", unwanted, body)
		}
	}
}

func TestDevCockpitRebuildRunningGuidance(t *testing.T) {
	cwd := seedDevCockpitContext(t)
	writeDevStatusRebuildStarted(
		devCtxStatusPath(cwd, "default"),
		&mcppkg.DeviceSession{SessionID: "sess-123"},
		"https://app.revyl.ai/sessions/sess-123",
		"https://hr-a-test.relay.revyl.ai",
		"nof1://expo-development-client/?url=https%3A%2F%2Fhr-a-test.relay.revyl.ai",
		"relay",
		"ios",
		3,
		true,
		"ios-dev",
	)
	cockpit := startTestDevCockpit(t, devCockpitOptions{
		Port:         0,
		CWD:          cwd,
		ContextName:  "default",
		ViewerURL:    "https://app.revyl.ai/sessions/sess-123",
		BuildEnabled: true,
		TriggerRebuild: func() error {
			return nil
		},
	})

	resp, err := http.Get(cockpit.URL + "/api")
	if err != nil {
		t.Fatalf("GET /api: %v", err)
	}
	defer resp.Body.Close()
	var snapshot devCockpitSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("Decode /api: %v", err)
	}
	if snapshot.LastRebuildStatus != "running" {
		t.Fatalf("last_rebuild_status = %q, want running", snapshot.LastRebuildStatus)
	}
	if snapshot.FailureSummary != "" {
		t.Fatalf("failure_summary = %q, want empty while rebuild is running", snapshot.FailureSummary)
	}
	if !strings.Contains(snapshot.NextAction, "Rebuild is running") {
		t.Fatalf("next_action = %q", snapshot.NextAction)
	}
	if len(snapshot.RebuildLogs) < 2 {
		t.Fatalf("rebuild_logs = %#v, want started logs", snapshot.RebuildLogs)
	}
}

func TestDevCockpitRebuildDisabled(t *testing.T) {
	cockpit := startTestDevCockpit(t, devCockpitOptions{
		Port:         0,
		CWD:          seedDevCockpitContext(t),
		ContextName:  "default",
		ViewerURL:    "https://app.revyl.ai/sessions/sess-123",
		BuildEnabled: false,
		TriggerRebuild: func() error {
			t.Fatal("TriggerRebuild should not be called when build is disabled")
			return nil
		},
	})

	req, err := http.NewRequest(http.MethodPost, cockpit.URL+"/rebuild", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Revyl-Cockpit-Token", cockpit.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /rebuild: %v", err)
	}
	body := readAndClose(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("POST /rebuild status = %d, want 409; body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "rebuild is disabled") {
		t.Fatalf("POST /rebuild body missing disabled message: %s", body)
	}
}

func startTestDevCockpit(t *testing.T, options devCockpitOptions) *devCockpitServer {
	t.Helper()
	if options.Host == "" {
		options.Host = devCockpitDefaultHost
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cockpit, err := startDevCockpitServer(ctx, options)
	if err != nil {
		t.Fatalf("startDevCockpitServer: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), devCockpitShutdownTimeout)
		defer shutdownCancel()
		_ = cockpit.Close(shutdownCtx)
	})
	return cockpit
}

func seedDevCockpitContext(t *testing.T) string {
	t.Helper()
	cwd := t.TempDir()
	nonce := time.Now().UnixNano()
	ctxMeta := &DevContext{
		Name:          "default",
		Platform:      "ios",
		Profile:       "development",
		PlatformKey:   "ios-dev",
		Provider:      "expo",
		SessionID:     "sess-123",
		SessionIndex:  2,
		SessionOwned:  true,
		ViewerURL:     "https://app.revyl.ai/sessions/sess-123",
		TunnelURL:     "https://hr-a-test.relay.revyl.ai",
		DeepLinkURL:   "nof1://expo-development-client/?url=https%3A%2F%2Fhr-a-test.relay.revyl.ai",
		Transport:     "relay",
		RelayID:       "a-test",
		PID:           os.Getpid(),
		StartedAtNano: nonce,
		State:         devContextStateRunning,
		Port:          8081,
		CreatedAt:     time.Now(),
		LastActivity:  time.Now(),
	}
	if err := saveDevContext(cwd, ctxMeta); err != nil {
		t.Fatalf("saveDevContext: %v", err)
	}
	if err := writeDevCtxPIDFile(devCtxPIDPath(cwd, "default"), os.Getpid(), nonce); err != nil {
		t.Fatalf("writeDevCtxPIDFile: %v", err)
	}
	writeDevStatus(
		devCtxStatusPath(cwd, "default"),
		&mcppkg.DeviceSession{SessionID: "sess-123"},
		"https://app.revyl.ai/sessions/sess-123",
		"https://hr-a-test.relay.revyl.ai",
		"nof1://expo-development-client/?url=https%3A%2F%2Fhr-a-test.relay.revyl.ai",
		"relay",
		"ios",
		1,
		true,
		devRebuildResult{
			elapsed:       1200 * time.Millisecond,
			buildDuration: 900 * time.Millisecond,
			pushDuration:  300 * time.Millisecond,
			usedDelta:     true,
			dataPreserved: true,
			filesChanged:  3,
			manifest:      &build.AppManifest{Hash: "hash"},
		},
	)
	return cwd
}

func seedDevCockpitFailureContext(t *testing.T) string {
	t.Helper()
	cwd := seedDevCockpitContext(t)
	writeDevStatus(
		devCtxStatusPath(cwd, "default"),
		&mcppkg.DeviceSession{SessionID: "sess-123"},
		"https://app.revyl.ai/sessions/sess-123",
		"https://hr-a-test.relay.revyl.ai",
		"nof1://expo-development-client/?url=https%3A%2F%2Fhr-a-test.relay.revyl.ai",
		"relay",
		"ios",
		2,
		true,
		devRebuildResult{
			buildErr:      errors.New(`platform "" not found in config`),
			elapsed:       2100 * time.Millisecond,
			buildDuration: 1800 * time.Millisecond,
			buildErrors: []build.BuildError{
				{File: "app.config.ts", Line: 12, Column: 7, Severity: "error", Message: "platform is required"},
			},
		},
	)
	return cwd
}

func postWithToken(t *testing.T, cockpit *devCockpitServer, path string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, cockpit.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Revyl-Cockpit-Token", cockpit.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	body := readAndClose(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s status = %d, body=%s", path, resp.StatusCode, body)
	}
}

func readAndClose(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return string(data)
}

func TestDevCockpitTokenIsRandomHex(t *testing.T) {
	token, err := newDevCockpitToken()
	if err != nil {
		t.Fatalf("newDevCockpitToken: %v", err)
	}
	if len(token) != devCockpitControlTokenBytes*2 {
		t.Fatalf("token length = %d", len(token))
	}
	if _, err := hex.DecodeString(token); err != nil {
		t.Fatalf("token is not hex: %v", err)
	}
}
