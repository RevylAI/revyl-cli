package tui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/api"
)

func TestUpdateCreate_OpenEditorReturnsToDashboardAndOpensBrowser(t *testing.T) {
	origOpenBrowserFn := openBrowserFn
	t.Cleanup(func() { openBrowserFn = origOpenBrowserFn })

	var openedURL string
	openBrowserFn = func(url string) error {
		openedURL = url
		return nil
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
		t.Fatalf("expected nil cmd after open-editor completion, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.currentView != viewDashboard {
		t.Fatalf("currentView = %v, want viewDashboard", next.currentView)
	}
	if openedURL == "" || openedURL != "https://app.revyl.ai/tests/test-1" {
		t.Fatalf("openedURL = %q, want https://app.revyl.ai/tests/test-1", openedURL)
	}
	if len(next.tests) == 0 || next.tests[0].ID != "test-1" {
		t.Fatalf("expected created test to be prepended, got %#v", next.tests)
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
	if got := next.err.Error(); got != "failed to open test editor: headless (open manually: https://app.revyl.ai/tests/test-1)" {
		t.Fatalf("err = %q", got)
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
