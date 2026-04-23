package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/api"
)

func strPtr(v string) *string {
	return &v
}

func TestSelectedDeviceViewerURL_PrefersAppViewer(t *testing.T) {
	t.Setenv("REVYL_APP_URL", "https://viewer.example")

	m := newHubModel("dev", false)
	m.selectedDeviceID = "session-1"
	m.deviceSessions = []api.ActiveDeviceSessionItem{
		{
			Id:            "session-1",
			Platform:      "ios",
			WorkflowRunId: strPtr("wf-123"),
			WhepUrl:       strPtr("https://whep.example/stream"),
		},
	}

	got := m.selectedDeviceViewerURL()
	want := "https://viewer.example/sessions/session-1"
	if got != want {
		t.Fatalf("selectedDeviceViewerURL() = %q, want %q", got, want)
	}
}

func TestSelectedDeviceViewerURL_PrefersSessionRoute(t *testing.T) {
	m := newHubModel("dev", false)
	m.selectedDeviceID = "session-1"
	m.deviceSessions = []api.ActiveDeviceSessionItem{
		{
			Id:       "session-1",
			Platform: "ios",
			WhepUrl:  strPtr("https://whep.example/stream"),
		},
	}

	got := m.selectedDeviceViewerURL()
	want := "https://app.revyl.ai/sessions/session-1"
	if got != want {
		t.Fatalf("selectedDeviceViewerURL() = %q, want %q", got, want)
	}
}

func TestSelectedDeviceViewerURL_EncodesQueryParams(t *testing.T) {
	t.Setenv("REVYL_APP_URL", "https://viewer.example")

	sessionID := "session 1/abc"
	workflowRunID := "wf 123+abc&x=y"
	platform := "ios beta/18"

	m := newHubModel("dev", false)
	m.selectedDeviceID = sessionID
	m.deviceSessions = []api.ActiveDeviceSessionItem{
		{
			Id:            sessionID,
			Platform:      platform,
			WorkflowRunId: strPtr(workflowRunID),
		},
	}

	got := m.selectedDeviceViewerURL()
	want := "https://viewer.example/sessions/" + url.PathEscape(sessionID)
	if got != want {
		t.Fatalf("selectedDeviceViewerURL() = %q, want %q", got, want)
	}
}

func TestFetchDeviceSessionsCmd_UsesCachedOrgIDWithoutValidation(t *testing.T) {
	validateCalls := 0
	activeCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/entity/users/get_user_uuid":
			validateCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user_id":"u-1","org_id":"org-live","email":"user@example.com","concurrency_limit":1}`))
		case r.URL.Path == "/api/v1/execution/device-sessions/active":
			activeCalls++
			if got := r.URL.Query().Get("org_id"); got != "org-cached" {
				t.Fatalf("expected org_id=org-cached, got %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"org_id":"org-cached","sessions":[]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	msgAny := fetchDeviceSessionsCmd(client, "org-cached")()
	msg, ok := msgAny.(DeviceSessionListMsg)
	if !ok {
		t.Fatalf("expected DeviceSessionListMsg, got %T", msgAny)
	}
	if msg.Err != nil {
		t.Fatalf("fetchDeviceSessionsCmd returned error: %v", msg.Err)
	}
	if msg.OrgID != "org-cached" {
		t.Fatalf("expected OrgID org-cached, got %q", msg.OrgID)
	}
	if validateCalls != 0 {
		t.Fatalf("expected 0 validate calls, got %d", validateCalls)
	}
	if activeCalls != 1 {
		t.Fatalf("expected 1 active-session call, got %d", activeCalls)
	}
}

func TestFetchDeviceSessionsCmd_ResolvesOrgIDWhenMissing(t *testing.T) {
	validateCalls := 0
	activeCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/entity/users/get_user_uuid":
			validateCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user_id":"u-1","org_id":"org-live","email":"user@example.com","concurrency_limit":1}`))
		case r.URL.Path == "/api/v1/execution/device-sessions/active":
			activeCalls++
			if got := r.URL.Query().Get("org_id"); got != "org-live" {
				t.Fatalf("expected org_id=org-live, got %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"org_id":"org-live","sessions":[]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	msgAny := fetchDeviceSessionsCmd(client, "")()
	msg, ok := msgAny.(DeviceSessionListMsg)
	if !ok {
		t.Fatalf("expected DeviceSessionListMsg, got %T", msgAny)
	}
	if msg.Err != nil {
		t.Fatalf("fetchDeviceSessionsCmd returned error: %v", msg.Err)
	}
	if msg.OrgID != "org-live" {
		t.Fatalf("expected OrgID org-live, got %q", msg.OrgID)
	}
	if validateCalls != 1 {
		t.Fatalf("expected 1 validate call, got %d", validateCalls)
	}
	if activeCalls != 1 {
		t.Fatalf("expected 1 active-session call, got %d", activeCalls)
	}
}

func TestHandleDeviceDetailKey_OpenViewerUsesResolvedURL(t *testing.T) {
	t.Setenv("REVYL_APP_URL", "https://viewer.example")

	origOpenBrowserFn := openBrowserFn
	t.Cleanup(func() { openBrowserFn = origOpenBrowserFn })

	openedURL := ""
	openBrowserFn = func(url string) error {
		openedURL = url
		return nil
	}

	m := newHubModel("dev", false)
	m.currentView = viewDeviceDetail
	m.selectedDeviceID = "session-1"
	m.deviceSessions = []api.ActiveDeviceSessionItem{
		{
			Id:            "session-1",
			Platform:      "android",
			WorkflowRunId: strPtr("wf-999"),
		},
	}

	_, cmd := m.handleDeviceDetailKey(keyRune('o'))
	if cmd != nil {
		t.Fatalf("expected no command when opening viewer, got %v", cmd)
	}

	want := "https://viewer.example/sessions/session-1"
	if openedURL != want {
		t.Fatalf("opened URL = %q, want %q", openedURL, want)
	}
}

func TestHandleDeviceListKey_NStartsDeviceOverlay(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewDeviceList

	nextModel, cmd := m.handleDeviceListKey(keyRune('n'))
	if cmd != nil {
		t.Fatalf("expected no command when opening start overlay, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if !next.deviceStartPicking {
		t.Fatalf("expected deviceStartPicking=true")
	}
	if got := deviceStartStep(next.deviceStartStep); got != deviceStartStepPlatform {
		t.Fatalf("deviceStartStep = %v, want %v", got, deviceStartStepPlatform)
	}
}

func TestHandleDeviceListKey_DeviceStartSearchFiltersApps(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewDeviceList
	m.deviceStartPicking = true
	m.deviceStartStep = int(deviceStartStepApp)
	m.deviceStartPlatform = "ios"
	m.deviceStartApps = []api.App{
		{ID: "app-a", Name: "Alpha", Platform: "ios", VersionsCount: 1, LatestVersion: "1.0"},
		{ID: "app-b", Name: "Beta", Platform: "ios", VersionsCount: 1, LatestVersion: "1.0"},
	}

	nextModel, cmd := m.handleDeviceListKey(keyRune('/'))
	if cmd == nil {
		t.Fatalf("expected blink command when entering device app search")
	}

	next := nextModel.(hubModel)
	if !next.deviceStartFilterMode {
		t.Fatalf("expected deviceStartFilterMode=true")
	}

	nextModel, cmd = next.handleDeviceListKey(keyRune('b'))
	if cmd == nil {
		t.Fatalf("expected text input update command while typing filter")
	}

	filtered := nextModel.(hubModel).filteredDeviceStartApps()
	if len(filtered) != 1 || filtered[0].ID != "app-b" {
		t.Fatalf("filtered apps = %#v, want only app-b", filtered)
	}
}

func TestUpdate_DeviceStartAppListMsg_DoesNotMutateManageApps(t *testing.T) {
	m := newHubModel("dev", false)
	m.apps = []api.App{{ID: "manage-app", Name: "Manage App", Platform: "ios", VersionsCount: 1}}
	m.deviceStartPicking = true
	m.deviceStartStep = int(deviceStartStepApp)
	m.deviceStartPlatform = "ios"

	nextModel, cmd := m.Update(DeviceStartAppListMsg{
		Platform: "ios",
		Apps: []api.App{
			{ID: "device-app", Name: "Device App", Platform: "ios", VersionsCount: 2, LatestVersion: "2.0"},
		},
	})
	if cmd != nil {
		t.Fatalf("expected no follow-up command, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if len(next.apps) != 1 || next.apps[0].ID != "manage-app" {
		t.Fatalf("manage apps changed unexpectedly: %#v", next.apps)
	}
	if len(next.deviceStartApps) != 1 || next.deviceStartApps[0].ID != "device-app" {
		t.Fatalf("device start apps = %#v, want device-app", next.deviceStartApps)
	}
}

func TestStartDeviceSessionCmd_StartsBareDeviceWhenNoAppSelected(t *testing.T) {
	var capturedReq struct {
		Platform     string `json:"platform"`
		AppURL       string `json:"app_url"`
		AppPackage   string `json:"app_package"`
		IsSimulation bool   `json:"is_simulation"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/execution/start_device" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewDecoder(r.Body).Decode(&capturedReq); err != nil {
			t.Fatalf("decode start request: %v", err)
		}
		_, _ = w.Write([]byte(`{"workflow_run_id":"00000000-0000-0000-0000-000000000005"}`))
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	msgAny := startDeviceSessionCmd(client, "ios", "", "", "", nil)()
	msg, ok := msgAny.(DeviceStartedMsg)
	if !ok {
		t.Fatalf("expected DeviceStartedMsg, got %T", msgAny)
	}
	if msg.Err != nil {
		t.Fatalf("startDeviceSessionCmd returned error: %v", msg.Err)
	}
	if capturedReq.Platform != "ios" {
		t.Fatalf("platform = %q, want %q", capturedReq.Platform, "ios")
	}
	if capturedReq.AppURL != "" {
		t.Fatalf("app_url = %q, want empty", capturedReq.AppURL)
	}
	if capturedReq.AppPackage != "" {
		t.Fatalf("app_package = %q, want empty", capturedReq.AppPackage)
	}
	if !capturedReq.IsSimulation {
		t.Fatalf("expected is_simulation=true")
	}
}

func TestStartDeviceSessionCmd_ResolvesSelectedAppToLatestBuild(t *testing.T) {
	const (
		appID       = "app-1"
		buildID     = "build-1"
		downloadURL = "https://artifact.example/dev.ipa"
		packageName = "com.example.dev"
	)

	var capturedReq struct {
		AppURL     string `json:"app_url"`
		AppPackage string `json:"app_package"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/builds/vars/"+appID+"/versions":
			if got := r.URL.Query().Get("page_size"); got != "20" {
				t.Fatalf("expected page_size=20, got %q", got)
			}
			_, _ = w.Write([]byte(`{"items":[{"id":"` + buildID + `","version":"1.0.0"}],"total":1,"page":1,"page_size":20,"total_pages":1,"has_next":false,"has_previous":false}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/builds/builds/"+buildID:
			if got := r.URL.Query().Get("include_download_url"); got != "true" {
				t.Fatalf("expected include_download_url=true, got %q", got)
			}
			_, _ = w.Write([]byte(`{"id":"` + buildID + `","version":"1.0.0","download_url":"` + downloadURL + `","package_name":"` + packageName + `"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/execution/start_device":
			if err := json.NewDecoder(r.Body).Decode(&capturedReq); err != nil {
				t.Fatalf("decode start request: %v", err)
			}
			_, _ = w.Write([]byte(`{"workflow_run_id":"00000000-0000-0000-0000-000000000006"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
		}
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	msgAny := startDeviceSessionCmd(client, "ios", appID, "", "", nil)()
	msg, ok := msgAny.(DeviceStartedMsg)
	if !ok {
		t.Fatalf("expected DeviceStartedMsg, got %T", msgAny)
	}
	if msg.Err != nil {
		t.Fatalf("startDeviceSessionCmd returned error: %v", msg.Err)
	}
	if capturedReq.AppURL != downloadURL {
		t.Fatalf("app_url = %q, want %q", capturedReq.AppURL, downloadURL)
	}
	if capturedReq.AppPackage != packageName {
		t.Fatalf("app_package = %q, want %q", capturedReq.AppPackage, packageName)
	}
}

func TestStartDeviceSessionCmd_AttachesLaunchVarIDs(t *testing.T) {
	var capturedReq struct {
		Platform        string   `json:"platform"`
		LaunchEnvVarIds []string `json:"launch_env_var_ids"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/execution/start_device" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewDecoder(r.Body).Decode(&capturedReq); err != nil {
			t.Fatalf("decode start request: %v", err)
		}
		_, _ = w.Write([]byte(`{"workflow_run_id":"00000000-0000-0000-0000-000000000007"}`))
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	ids := []string{"lv-1", "lv-2"}
	msgAny := startDeviceSessionCmd(client, "android", "", "", "", ids)()
	msg, ok := msgAny.(DeviceStartedMsg)
	if !ok {
		t.Fatalf("expected DeviceStartedMsg, got %T", msgAny)
	}
	if msg.Err != nil {
		t.Fatalf("startDeviceSessionCmd returned error: %v", msg.Err)
	}
	if len(capturedReq.LaunchEnvVarIds) != 2 ||
		capturedReq.LaunchEnvVarIds[0] != "lv-1" ||
		capturedReq.LaunchEnvVarIds[1] != "lv-2" {
		t.Fatalf("launch_env_var_ids = %v, want [lv-1 lv-2]", capturedReq.LaunchEnvVarIds)
	}
}

func TestHandleDeviceStartLaunchVarsKey_ToggleAndStart(t *testing.T) {
	m := newHubModel("dev", false)
	m.client = &api.Client{}
	m.deviceStartPicking = true
	m.deviceStartStep = int(deviceStartStepLaunchVars)
	m.deviceStartPlatform = "ios"
	m.launchVarItems = []LaunchVarItem{
		{ID: "lv-1", Key: "API_URL"},
		{ID: "lv-2", Key: "DEBUG"},
	}

	// Toggle first entry.
	next, _ := m.handleDeviceStartLaunchVarsKey(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(hubModel)
	if !m.deviceStartLaunchVarSelected["lv-1"] {
		t.Fatalf("expected lv-1 selected, got %v", m.deviceStartLaunchVarSelected)
	}

	// Toggle off.
	next, _ = m.handleDeviceStartLaunchVarsKey(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(hubModel)
	if m.deviceStartLaunchVarSelected["lv-1"] {
		t.Fatalf("expected lv-1 unselected after second toggle")
	}

	// Select second, then press enter to start.
	m.deviceStartLaunchVarCursor = 1
	next, _ = m.handleDeviceStartLaunchVarsKey(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(hubModel)
	next, cmd := m.handleDeviceStartLaunchVarsKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(hubModel)
	if !m.deviceStarting {
		t.Fatalf("expected deviceStarting=true after enter")
	}
	if cmd == nil {
		t.Fatalf("expected start command")
	}
}

func TestHandleDeviceStartLaunchVarsKey_SkipProceedsWithoutSelection(t *testing.T) {
	m := newHubModel("dev", false)
	m.client = &api.Client{}
	m.deviceStartPicking = true
	m.deviceStartStep = int(deviceStartStepLaunchVars)
	m.deviceStartPlatform = "android"
	m.launchVarItems = []LaunchVarItem{{ID: "lv-1", Key: "API_URL"}}

	next, cmd := m.handleDeviceStartLaunchVarsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = next.(hubModel)
	if !m.deviceStarting {
		t.Fatalf("expected deviceStarting=true after skip")
	}
	if len(m.deviceStartLaunchVarSelected) != 0 {
		t.Fatalf("expected no selections after skip, got %v", m.deviceStartLaunchVarSelected)
	}
	if cmd == nil {
		t.Fatalf("expected start command on skip")
	}
}

func TestHandleDeviceListKey_EnterStartsPollingForStartingSession(t *testing.T) {
	m := newHubModel("dev", false)
	m.client = &api.Client{}
	m.deviceSessions = []api.ActiveDeviceSessionItem{
		{
			Id:            "session-1",
			Platform:      "ios",
			Status:        "starting",
			WorkflowRunId: strPtr("wf-1"),
		},
	}

	nextModel, cmd := m.handleDeviceListKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected polling command batch when opening a starting session")
	}

	next := nextModel.(hubModel)
	if next.currentView != viewDeviceDetail {
		t.Fatalf("expected viewDeviceDetail, got %v", next.currentView)
	}
	if next.selectedDeviceID != "session-1" {
		t.Fatalf("expected selected device to be session-1, got %q", next.selectedDeviceID)
	}
	if next.deviceDetailPollSeq != 1 {
		t.Fatalf("expected poll sequence to increment to 1, got %d", next.deviceDetailPollSeq)
	}
	if !next.devicesLoading {
		t.Fatalf("expected devicesLoading=true when starting poll fetch")
	}
}

func TestHandleDeviceListKey_EscInvalidatesListPolling(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewDeviceList
	m.deviceListPollSeq = 7

	nextModel, cmd := m.handleDeviceListKey(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("expected no command on esc, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.currentView != viewDashboard {
		t.Fatalf("expected viewDashboard, got %v", next.currentView)
	}
	if next.deviceListPollSeq != 8 {
		t.Fatalf("expected list poll sequence to increment to 8, got %d", next.deviceListPollSeq)
	}
}

func TestHandleDeviceListKey_EnterDoesNotPollForRunningSession(t *testing.T) {
	m := newHubModel("dev", false)
	m.client = &api.Client{}
	m.deviceSessions = []api.ActiveDeviceSessionItem{
		{
			Id:       "session-1",
			Platform: "ios",
			Status:   "running",
		},
	}

	nextModel, cmd := m.handleDeviceListKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("expected no polling command for running session, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.currentView != viewDeviceDetail {
		t.Fatalf("expected viewDeviceDetail, got %v", next.currentView)
	}
	if next.deviceDetailPollSeq != 1 {
		t.Fatalf("expected poll sequence to increment to 1, got %d", next.deviceDetailPollSeq)
	}
}

func TestHandleDeviceDetailKey_EscStopsPolling(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewDeviceDetail
	m.deviceDetailPollSeq = 7

	nextModel, cmd := m.handleDeviceDetailKey(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("expected no command on esc, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.currentView != viewDeviceList {
		t.Fatalf("expected viewDeviceList, got %v", next.currentView)
	}
	if next.deviceDetailPollSeq != 8 {
		t.Fatalf("expected poll sequence to increment to 8, got %d", next.deviceDetailPollSeq)
	}
}

func TestUpdate_DeviceDetailPollTickMsgFetchesWhileTransitional(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewDeviceDetail
	m.client = &api.Client{}
	m.selectedDeviceID = "session-1"
	m.deviceDetailPollSeq = 2
	m.deviceSessions = []api.ActiveDeviceSessionItem{
		{
			Id:       "session-1",
			Platform: "ios",
			Status:   "starting",
		},
	}

	nextModel, cmd := m.Update(DeviceDetailPollTickMsg{Seq: 2})
	if cmd == nil {
		t.Fatalf("expected poll tick to schedule fetch command")
	}

	next := nextModel.(hubModel)
	if !next.devicesLoading {
		t.Fatalf("expected devicesLoading=true while poll fetch is in flight")
	}
}

func TestUpdate_DeviceDetailPollTickMsgIgnoresStaleSequence(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewDeviceDetail
	m.client = &api.Client{}
	m.selectedDeviceID = "session-1"
	m.deviceDetailPollSeq = 5
	m.deviceSessions = []api.ActiveDeviceSessionItem{
		{
			Id:       "session-1",
			Platform: "ios",
			Status:   "starting",
		},
	}

	nextModel, cmd := m.Update(DeviceDetailPollTickMsg{Seq: 4})
	if cmd != nil {
		t.Fatalf("expected stale poll tick to be ignored, got command %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.devicesLoading {
		t.Fatalf("expected devicesLoading=false for ignored stale poll tick")
	}
}

func TestUpdate_DeviceListPollTickMsgFetchesWhileTransitional(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewDeviceList
	m.client = &api.Client{}
	m.deviceListPollSeq = 2
	m.deviceSessions = []api.ActiveDeviceSessionItem{
		{
			Id:       "session-1",
			Platform: "ios",
			Status:   "starting",
		},
	}

	nextModel, cmd := m.Update(DeviceListPollTickMsg{Seq: 2})
	if cmd == nil {
		t.Fatalf("expected list poll tick to schedule fetch command")
	}

	next := nextModel.(hubModel)
	if !next.devicesLoading {
		t.Fatalf("expected devicesLoading=true while list poll fetch is in flight")
	}
}

func TestUpdate_DeviceListPollTickMsgStopsWhenAllStable(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewDeviceList
	m.client = &api.Client{}
	m.deviceListPollSeq = 3
	m.deviceSessions = []api.ActiveDeviceSessionItem{
		{
			Id:       "session-1",
			Platform: "ios",
			Status:   "running",
		},
		{
			Id:       "session-2",
			Platform: "android",
			Status:   "cancelled",
		},
	}

	nextModel, cmd := m.Update(DeviceListPollTickMsg{Seq: 3})
	if cmd != nil {
		t.Fatalf("expected stable list poll tick to stop scheduling commands, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.devicesLoading {
		t.Fatalf("expected devicesLoading=false when all sessions are stable")
	}
}

func TestUpdate_DeviceListPollTickMsgIgnoresStaleSequence(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewDeviceList
	m.client = &api.Client{}
	m.deviceListPollSeq = 5
	m.deviceSessions = []api.ActiveDeviceSessionItem{
		{
			Id:       "session-1",
			Platform: "ios",
			Status:   "starting",
		},
	}

	nextModel, cmd := m.Update(DeviceListPollTickMsg{Seq: 4})
	if cmd != nil {
		t.Fatalf("expected stale list poll tick to be ignored, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.devicesLoading {
		t.Fatalf("expected devicesLoading=false for ignored stale list poll tick")
	}
}

func TestUpdate_DeviceListPollTickMsgNoopOutsideListView(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewDeviceDetail
	m.client = &api.Client{}
	m.deviceListPollSeq = 9
	m.deviceSessions = []api.ActiveDeviceSessionItem{
		{
			Id:       "session-1",
			Platform: "ios",
			Status:   "starting",
		},
	}

	nextModel, cmd := m.Update(DeviceListPollTickMsg{Seq: 9})
	if cmd != nil {
		t.Fatalf("expected no command when list poll tick arrives outside list view, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.devicesLoading {
		t.Fatalf("expected devicesLoading=false when list poll tick is ignored outside list view")
	}
}

func TestExecuteQuickAction_DevicesStartsListPolling(t *testing.T) {
	m := newHubModel("dev", false)
	m.client = &api.Client{}
	m.apiKey = "test-token"

	deviceActionIndex := -1
	for i, action := range quickActions {
		if action.Key == "devices" {
			deviceActionIndex = i
			break
		}
	}
	if deviceActionIndex < 0 {
		t.Fatalf("devices quick action not found")
	}
	m.actionCursor = deviceActionIndex

	nextModel, cmd := m.executeQuickAction()
	if cmd == nil {
		t.Fatalf("expected devices quick action to return a polling batch command")
	}

	next := nextModel.(hubModel)
	if next.currentView != viewDeviceList {
		t.Fatalf("expected viewDeviceList, got %v", next.currentView)
	}
	if !next.devicesLoading {
		t.Fatalf("expected devicesLoading=true after opening device list")
	}
	if next.deviceListPollSeq != 1 {
		t.Fatalf("expected list poll sequence to increment to 1, got %d", next.deviceListPollSeq)
	}
}

func TestRenderDeviceDetail_ShowsViewerHintFromWorkflowID(t *testing.T) {
	t.Setenv("REVYL_APP_URL", "https://viewer.example")

	m := newHubModel("dev", false)
	m.width = 100
	m.height = 30
	m.currentView = viewDeviceDetail
	m.selectedDeviceID = "session-1"
	m.deviceSessions = []api.ActiveDeviceSessionItem{
		{
			Id:            "session-1",
			Platform:      "ios",
			Status:        "running",
			WorkflowRunId: strPtr("wf-abc"),
		},
	}

	out := m.renderDeviceDetail()
	if !strings.Contains(out, "Viewer URL available") {
		t.Fatalf("expected viewer hint in output, got: %s", out)
	}
	if !strings.Contains(out, "viewer: https://viewer.example/sessions/session-1") {
		t.Fatalf("expected rendered output to include copyable viewer URL, got: %s", out)
	}
}
