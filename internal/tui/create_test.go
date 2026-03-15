package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/testutil"
)

func TestCreateTestCmd_UsesSelectedAppAndCreatesEmptyShell(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())

	sawCreate := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/builds/vars/app-ios":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"app-ios","name":"Shell App","platform":"ios","versions_count":0}`))
		case "/api/v1/builds/vars/app-ios/versions":
			if got := r.URL.Query().Get("page"); got != "1" {
				t.Fatalf("page = %q, want 1", got)
			}
			if got := r.URL.Query().Get("page_size"); got != "1" {
				t.Fatalf("page_size = %q, want 1", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"items": [{"id":"ver-1","version":"1.0.0","uploaded_at":"2026-03-05T00:00:00Z"}],
				"total": 1,
				"page": 1,
				"page_size": 1,
				"total_pages": 1,
				"has_next": false,
				"has_previous": false
			}`))
		case "/api/v1/tests/create":
			sawCreate = true

			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode create request: %v", err)
			}

			if got := req["org_id"]; got != "org-config" {
				t.Fatalf("org_id = %v, want org-config", got)
			}
			if got := req["app_id"]; got != "app-ios" {
				t.Fatalf("app_id = %v, want app-ios", got)
			}
			taskList, ok := req["tasks"].([]any)
			if !ok || len(taskList) != 0 {
				t.Fatalf("tasks = %#v, want empty list", req["tasks"])
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"test-1","version":1}`))
		case "/api/v1/entity/users/get_user_uuid":
			t.Fatalf("unexpected validate call")
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := api.NewClientWithBaseURL("test-key", srv.URL)
	cfg := &config.ProjectConfig{
		Project: config.Project{OrgID: "org-config"},
	}

	msgAny := createTestCmd(client, cfg, "dfa", "ios", "app-ios")()
	msg, ok := msgAny.(TestCreatedMsg)
	if !ok {
		t.Fatalf("expected TestCreatedMsg, got %T", msgAny)
	}
	if msg.Err != nil {
		t.Fatalf("createTestCmd error = %v", msg.Err)
	}
	if msg.TestID != "test-1" {
		t.Fatalf("TestID = %q, want test-1", msg.TestID)
	}
	if !sawCreate {
		t.Fatal("expected create request to be sent")
	}
}

func TestCreateModel_UpdateResolverFailureReturnsToConfirm(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/builds/vars/app-ios":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"app-ios","name":"Shell App","platform":"ios","versions_count":1,"latest_version":"1.0.0"}`))
		case "/api/v1/entity/users/get_user_uuid":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"","email":"test@example.com","concurrency_limit":1}`))
		case "/api/v1/tests/create":
			t.Fatalf("unexpected create call when org resolution fails")
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := api.NewClientWithBaseURL("test-key", srv.URL)
	msgAny := createTestCmd(client, nil, "dfa", "ios", "app-ios")()
	msg, ok := msgAny.(TestCreatedMsg)
	if !ok {
		t.Fatalf("expected TestCreatedMsg, got %T", msgAny)
	}
	if msg.Err == nil {
		t.Fatal("expected createTestCmd to return an error")
	}

	m := newCreateModel("token", false, client, nil, 80, 24)
	m.step = stepCreating
	m.creating = true

	nextModel, cmd := m.Update(msg)
	if cmd != nil {
		t.Fatalf("expected nil cmd on create failure, got %v", cmd)
	}

	next := nextModel.(createModel)
	if next.step != stepConfirm {
		t.Fatalf("step = %v, want %v", next.step, stepConfirm)
	}
	if next.err == nil || !strings.Contains(next.err.Error(), "could not resolve organization ID") {
		t.Fatalf("expected org resolution error, got %v", next.err)
	}
	if next.creating {
		t.Fatal("expected creating=false after failure")
	}
}

func TestCreateModel_AppListPreselectsConfiguredDefaultAndFiltersToRunnableApps(t *testing.T) {
	m := newCreateModel("token", false, &api.Client{}, &config.ProjectConfig{
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"ios": {AppID: "app-default"},
			},
		},
	}, 80, 24)
	m.step = stepApp
	m.platformCursor = 1
	m.appsLoading = true

	nextModel, cmd := m.Update(AppListMsg{Apps: []api.App{
		{ID: "app-other", Name: "Other App", Platform: "ios", VersionsCount: 2, LatestVersion: "2.0.0"},
		{ID: "app-default", Name: "Default App", Platform: "ios", VersionsCount: 1, LatestVersion: "1.0.0"},
		{ID: "app-empty", Name: "Empty App", Platform: "ios", VersionsCount: 0},
		{ID: "app-android", Name: "Android App", Platform: "android", VersionsCount: 3, LatestVersion: "3.0.0"},
	}})
	if cmd != nil {
		t.Fatalf("expected nil cmd after app list load, got %v", cmd)
	}

	next := nextModel.(createModel)
	if next.appsLoading {
		t.Fatalf("expected appsLoading=false after app list load")
	}
	if len(next.apps) != 2 {
		t.Fatalf("apps len = %d, want 2 runnable ios apps", len(next.apps))
	}
	if next.appCursor != 1 {
		t.Fatalf("appCursor = %d, want configured default selection at index 1", next.appCursor)
	}
	if next.selectedApp() == nil || next.selectedApp().ID != "app-default" {
		t.Fatalf("selected app = %#v, want app-default", next.selectedApp())
	}
}

func TestCreateModel_NoEligibleAppsRoutesToManageApps(t *testing.T) {
	m := newCreateModel("token", false, &api.Client{}, nil, 80, 24)
	m.step = stepApp
	m.platformCursor = 1
	m.appsLoading = true

	nextModel, cmd := m.Update(AppListMsg{Apps: []api.App{
		{ID: "app-empty", Name: "Empty App", Platform: "ios", VersionsCount: 0},
	}})
	if cmd != nil {
		t.Fatalf("expected nil cmd after no-eligible app list load, got %v", cmd)
	}

	next := nextModel.(createModel)
	if !next.noEligibleApps {
		t.Fatalf("expected noEligibleApps=true")
	}
	if next.err == nil || !strings.Contains(next.err.Error(), "no ios apps with uploaded builds") {
		t.Fatalf("expected actionable no-build error, got %v", next.err)
	}

	nextModel, cmd = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("expected nil cmd when selecting manage apps, got %v", cmd)
	}
	next = nextModel.(createModel)
	if !next.done {
		t.Fatalf("expected done=true after choosing manage apps")
	}
	if next.doneAction != createDoneManageApps {
		t.Fatalf("doneAction = %v, want createDoneManageApps", next.doneAction)
	}
}

func TestCreateModel_ConfirmViewIncludesSelectedApp(t *testing.T) {
	m := newCreateModel("token", false, &api.Client{}, nil, 80, 24)
	m.step = stepConfirm
	m.platformCursor = 1
	m.apps = []api.App{{ID: "app-ios", Name: "Default App", Platform: "ios", VersionsCount: 1, LatestVersion: "1.0.0"}}
	m.nameInput.SetValue("dfa")

	out := m.View()
	if !strings.Contains(out, "Default App") {
		t.Fatalf("expected confirm view to include selected app, got: %s", out)
	}
	if !strings.Contains(out, "Platform:") {
		t.Fatalf("expected confirm view to include platform, got: %s", out)
	}
}

func TestCreateModel_DoneViewRevealsEditorLink(t *testing.T) {
	m := newCreateModel("token", false, &api.Client{}, nil, 80, 24)
	m.step = stepDone
	m.createdID = "test-1"
	m.nameInput.SetValue("dfa")

	out := m.View()
	if !strings.Contains(out, "Press l to reveal the editor link") {
		t.Fatalf("expected done view to advertise link shortcut, got: %s", out)
	}

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd when toggling editor link, got %v", cmd)
	}

	next := nextModel.(createModel)
	if !next.showEditorURL {
		t.Fatalf("expected showEditorURL=true after pressing l")
	}

	out = next.View()
	if !strings.Contains(out, "https://app.revyl.ai/tests/execute?testUid=test-1") {
		t.Fatalf("expected done view to include editor URL, got: %s", out)
	}
}
