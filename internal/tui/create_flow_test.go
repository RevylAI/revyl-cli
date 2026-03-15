package tui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/api"
)

func TestFetchTestsCmd_NoConfigReturnsRemoteTests(t *testing.T) {
	t.Chdir(t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests/get_simple_tests" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"tests":[{"id":"test-1","name":"dfa","platform":"ios"}],
			"count":1
		}`))
	}))
	defer srv.Close()

	msgAny := fetchTestsCmd(api.NewClientWithBaseURL("token", srv.URL))()
	msg, ok := msgAny.(TestListMsg)
	if !ok {
		t.Fatalf("message type = %T, want TestListMsg", msgAny)
	}
	if msg.Err != nil {
		t.Fatalf("msg.Err = %v, want nil", msg.Err)
	}
	if msg.Warning != "" {
		t.Fatalf("msg.Warning = %q, want empty", msg.Warning)
	}
	if len(msg.Tests) != 1 {
		t.Fatalf("len(msg.Tests) = %d, want 1", len(msg.Tests))
	}
	if msg.Tests[0].SyncStatus != "remote-only" {
		t.Fatalf("syncStatus = %q, want remote-only", msg.Tests[0].SyncStatus)
	}
}

func TestFetchTestsCmd_OrgMismatchAddsWarning(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	revylDir := filepath.Join(tempDir, ".revyl")
	if err := os.MkdirAll(revylDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(revylDir, "config.yaml"), []byte(`project:
  name: demo
  org_id: org-config
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/tests/get_simple_tests":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"tests":[{"id":"test-1","name":"dfa","platform":"ios"}],
				"count":1
			}`))
		case "/api/v1/entity/users/get_user_uuid":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-live","email":"test@example.com","concurrency_limit":1}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	msgAny := fetchTestsCmd(api.NewClientWithBaseURL("token", srv.URL))()
	msg, ok := msgAny.(TestListMsg)
	if !ok {
		t.Fatalf("message type = %T, want TestListMsg", msgAny)
	}
	if msg.Err != nil {
		t.Fatalf("msg.Err = %v, want nil", msg.Err)
	}
	if !strings.Contains(msg.Warning, `Project is bound to "org-config", current login is "org-live"`) {
		t.Fatalf("msg.Warning = %q", msg.Warning)
	}
}

func TestUpdateCreate_OpenEditorReturnsToDashboardAndOpensBrowser(t *testing.T) {
	origOpenBrowserFn := openBrowserFn
	t.Cleanup(func() { openBrowserFn = origOpenBrowserFn })

	var openedURL string
	openBrowserFn = func(url string) error {
		openedURL = url
		return nil
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests/get_simple_tests" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"tests":[{"id":"test-1","name":"dfa","platform":"ios"}],
			"count":1
		}`))
	}))
	defer srv.Close()

	m := newHubModel("dev", false)
	m.client = api.NewClientWithBaseURL("token", srv.URL)
	m.currentView = viewCreateTest
	cm := newCreateModel("token", false, m.client, nil, 80, 24)
	cm.step = stepDone
	cm.done = true
	cm.doneAction = createDoneOpenEditor
	cm.createdID = "test-1"
	cm.platformCursor = 1
	cm.nameInput.SetValue("dfa")
	m.createModel = &cm

	nextModel, cmd := m.updateCreate(tea.WindowSizeMsg{Width: 80, Height: 24})
	if cmd == nil {
		t.Fatalf("expected refresh cmd after open-editor completion")
	}

	next := nextModel.(hubModel)
	if next.currentView != viewDashboard {
		t.Fatalf("currentView = %v, want viewDashboard", next.currentView)
	}
	if openedURL == "" || openedURL != "https://app.revyl.ai/tests/execute?testUid=test-1" {
		t.Fatalf("openedURL = %q, want https://app.revyl.ai/tests/execute?testUid=test-1", openedURL)
	}
	if len(next.tests) != 0 {
		t.Fatalf("expected tests to refresh from remote list instead of optimistic insert, got %#v", next.tests)
	}

	refreshMsg := cmd()
	refreshedModel, nextCmd := next.Update(refreshMsg)
	if nextCmd == nil {
		t.Fatalf("expected recent-runs follow-up cmd after refreshed test list")
	}

	refreshed := refreshedModel.(hubModel)
	if len(refreshed.tests) != 1 || refreshed.tests[0].ID != "test-1" {
		t.Fatalf("expected refreshed remote tests to include test-1, got %#v", refreshed.tests)
	}
	if refreshed.tests[0].SyncStatus != "remote-only" {
		t.Fatalf("syncStatus = %q, want remote-only", refreshed.tests[0].SyncStatus)
	}
	if refreshed.err != nil {
		t.Fatalf("expected nil error after successful refresh, got %v", refreshed.err)
	}
}

func TestUpdateCreate_OpenEditorFailureKeepsManualURL(t *testing.T) {
	origOpenBrowserFn := openBrowserFn
	t.Cleanup(func() { openBrowserFn = origOpenBrowserFn })

	openBrowserFn = func(url string) error {
		return errors.New("headless")
	}

	m := newHubModel("dev", false)
	m.currentView = viewCreateTest
	cm := newCreateModel("token", false, &api.Client{}, nil, 80, 24)
	cm.step = stepDone
	cm.done = true
	cm.doneAction = createDoneOpenEditor
	cm.createdID = "test-1"
	cm.platformCursor = 1
	cm.nameInput.SetValue("dfa")
	m.createModel = &cm

	nextModel, cmd := m.updateCreate(tea.WindowSizeMsg{Width: 80, Height: 24})
	if cmd != nil {
		t.Fatalf("expected nil cmd after open-editor failure, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.err == nil {
		t.Fatal("expected manual-open error message")
	}
	if got := next.err.Error(); got != "failed to open test editor: headless (open manually: https://app.revyl.ai/tests/execute?testUid=test-1)" {
		t.Fatalf("err = %q", got)
	}
}

func TestUpdateCreate_RefreshFailureShowsExplicitError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests/get_simple_tests" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail":"boom"}`))
	}))
	defer srv.Close()

	m := newHubModel("dev", false)
	m.client = api.NewClientWithBaseURL("token", srv.URL)
	m.currentView = viewCreateTest

	cm := newCreateModel("token", false, m.client, nil, 80, 24)
	cm.step = stepDone
	cm.done = true
	cm.doneAction = createDoneBackToDashboard
	cm.createdID = "test-1"
	cm.nameInput.SetValue("dfa")
	m.createModel = &cm

	nextModel, cmd := m.updateCreate(tea.WindowSizeMsg{Width: 80, Height: 24})
	if cmd == nil {
		t.Fatalf("expected refresh cmd after create completion")
	}

	next := nextModel.(hubModel)
	refreshedModel, followUpCmd := next.Update(cmd())
	if followUpCmd != nil {
		t.Fatalf("expected no follow-up cmd on refresh failure, got %v", followUpCmd)
	}

	refreshed := refreshedModel.(hubModel)
	if refreshed.err == nil {
		t.Fatal("expected explicit refresh failure error")
	}
	if !strings.Contains(refreshed.err.Error(), `test "dfa" was created, but could not be refreshed from the remote list`) {
		t.Fatalf("err = %q", refreshed.err.Error())
	}
	if len(refreshed.tests) != 0 {
		t.Fatalf("expected no optimistic placeholder test row, got %#v", refreshed.tests)
	}
}

func TestUpdateCreate_MissingCreatedTestShowsExplicitError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests/get_simple_tests" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"tests":[{"id":"other-test","name":"existing","platform":"ios"}],
			"count":1
		}`))
	}))
	defer srv.Close()

	m := newHubModel("dev", false)
	m.client = api.NewClientWithBaseURL("token", srv.URL)
	m.currentView = viewCreateTest

	cm := newCreateModel("token", false, m.client, nil, 80, 24)
	cm.step = stepDone
	cm.done = true
	cm.doneAction = createDoneBackToDashboard
	cm.createdID = "test-1"
	cm.nameInput.SetValue("dfa")
	m.createModel = &cm

	nextModel, cmd := m.updateCreate(tea.WindowSizeMsg{Width: 80, Height: 24})
	if cmd == nil {
		t.Fatalf("expected refresh cmd after create completion")
	}

	next := nextModel.(hubModel)
	refreshedModel, followUpCmd := next.Update(cmd())
	if followUpCmd == nil {
		t.Fatalf("expected recent-runs follow-up cmd after successful list refresh")
	}

	refreshed := refreshedModel.(hubModel)
	if refreshed.err == nil {
		t.Fatal("expected explicit missing-created-test error")
	}
	if !strings.Contains(refreshed.err.Error(), `test "dfa" was created, but could not be refreshed from the remote list`) {
		t.Fatalf("err = %q", refreshed.err.Error())
	}
	if len(refreshed.tests) != 1 || refreshed.tests[0].ID != "other-test" {
		t.Fatalf("expected authoritative remote list contents, got %#v", refreshed.tests)
	}
	if refreshed.tests[0].SyncStatus != "remote-only" {
		t.Fatalf("syncStatus = %q, want remote-only", refreshed.tests[0].SyncStatus)
	}
}

func TestUpdateCreate_ManageAppsRoutesToAppList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/builds/vars" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"items":[{"id":"app-1","name":"Shell App","platform":"ios","versions_count":1,"latest_version":"1.0.0"}],
			"total":1,"page":1,"page_size":100,"total_pages":1,"has_next":false,"has_previous":false
		}`))
	}))
	defer srv.Close()

	m := newHubModel("dev", false)
	m.currentView = viewCreateTest
	m.client = api.NewClientWithBaseURL("token", srv.URL)

	cm := newCreateModel("token", false, m.client, nil, 80, 24)
	cm.done = true
	cm.doneAction = createDoneManageApps
	m.createModel = &cm

	nextModel, cmd := m.updateCreate(tea.WindowSizeMsg{Width: 80, Height: 24})
	if cmd == nil {
		t.Fatalf("expected app refresh cmd when routing to manage apps")
	}

	next := nextModel.(hubModel)
	if next.currentView != viewAppList {
		t.Fatalf("currentView = %v, want viewAppList", next.currentView)
	}
	if !next.appsLoading {
		t.Fatalf("expected appsLoading=true while app list refresh is in flight")
	}
}
