package tui

import (
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
	want := "https://viewer.example/tests/execute?workflowRunId=wf-123&platform=ios"
	if got != want {
		t.Fatalf("selectedDeviceViewerURL() = %q, want %q", got, want)
	}
}

func TestSelectedDeviceViewerURL_FallsBackToWHEP(t *testing.T) {
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
	want := "https://whep.example/stream"
	if got != want {
		t.Fatalf("selectedDeviceViewerURL() = %q, want %q", got, want)
	}
}

func TestSelectedDeviceViewerURL_EncodesQueryParams(t *testing.T) {
	t.Setenv("REVYL_APP_URL", "https://viewer.example")

	workflowRunID := "wf 123+abc&x=y"
	platform := "ios beta/18"

	m := newHubModel("dev", false)
	m.selectedDeviceID = "session-1"
	m.deviceSessions = []api.ActiveDeviceSessionItem{
		{
			Id:            "session-1",
			Platform:      platform,
			WorkflowRunId: strPtr(workflowRunID),
		},
	}

	got := m.selectedDeviceViewerURL()
	want := "https://viewer.example/tests/execute?workflowRunId=" +
		url.QueryEscape(workflowRunID) +
		"&platform=" + url.QueryEscape(platform)
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

	want := "https://viewer.example/tests/execute?workflowRunId=wf-999&platform=android"
	if openedURL != want {
		t.Fatalf("opened URL = %q, want %q", openedURL, want)
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
	if !strings.Contains(out, "viewer: https://viewer.example/tests/execute?workflowRunId=wf-abc&platform=ios") {
		t.Fatalf("expected rendered output to include copyable viewer URL, got: %s", out)
	}
}
