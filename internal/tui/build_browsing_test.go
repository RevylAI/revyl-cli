package tui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/revyl/cli/internal/api"
)

func TestAppBuildBrowserAllIncludesEveryBranchAndLegacyRows(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/apps/"+pickerAppID+"/builds" || r.URL.Query().Get("branch_id") != "" {
			t.Errorf("All browsing resolved a branch: %s", r.URL)
		}
		if r.URL.Query().Get("page_size") != "100" {
			t.Errorf("pagination limit changed: %s", r.URL)
		}
		pages = append(pages, r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"items":[{"id":"legacy-build","version":"legacy"}],"has_next":false}`)
			return
		}
		fmt.Fprintf(w, `{"items":[{"id":%q,"version":"base-build","branch_name":"base"},{"id":"feature-build","version":"feature-build","branch_name":"feature"}],"has_next":true}`, pickerBuildID)
	}))
	defer server.Close()
	m := newHubModel("dev", false)
	m.width, m.height = 120, 42
	m.client, m.selectedAppID, m.currentView = api.NewClientWithBaseURL("test", server.URL), pickerAppID, viewAppDetail
	next, _ := m.Update(m.startAppBuildFetch(1, nil)())
	m = next.(hubModel)
	if m.err != nil || len(m.appBuilds) != 2 || m.appBuildBranch != nil || !m.appBuildHasNext {
		t.Fatalf("All first page unavailable: builds=%#v, err=%v", m.appBuilds, m.err)
	}
	view := m.renderAppDetail()
	for _, label := range []string{"All branches", "[base]", "[feature]"} {
		if !strings.Contains(view, label) {
			t.Errorf("missing scope/provenance %q: %s", label, view)
		}
	}
	next, cmd := m.handleAppDetailKey(keyRune('m'))
	m = next.(hubModel)
	next, _ = m.Update(cmd())
	m = next.(hubModel)
	if m.err != nil || len(m.appBuilds) != 3 || m.appBuilds[2].BranchName != "" || m.appBuildHasNext {
		t.Fatalf("All pagination lost rows or invented provenance: builds=%#v, err=%v", m.appBuilds, m.err)
	}
	next, cmd = m.handleAppDetailKey(keyRune('R'))
	m = next.(hubModel)
	next, _ = m.Update(cmd())
	m = next.(hubModel)
	if m.err != nil || len(m.appBuilds) != 2 || m.appBuildPage != 1 || !reflect.DeepEqual(pages, []string{"1", "2", "1"}) {
		t.Fatalf("All refresh changed pagination: pages=%v, err=%v", pages, m.err)
	}
}

func TestAppBuildBrowserAcceptsEmptyBranch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + pickerAppID + "/branches/" + pickerBranchID:
			fmt.Fprintf(w, `{"id":%q,"name":"empty"}`, pickerBranchID)
		case "/api/v1/apps/" + pickerAppID + "/builds":
			if r.URL.Query().Get("branch_id") != pickerBranchID {
				t.Errorf("empty branch fell back: %s", r.URL)
			}
			fmt.Fprint(w, `{"items":[],"has_next":false}`)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	msg := fetchAppBuildsCmd(api.NewClientWithBaseURL("test", server.URL), pickerAppID, appBuildQuery{BranchID: pickerBranchID})().(AppBuildVersionsMsg)
	if msg.Err != nil || len(msg.Versions) != 0 || msg.Branch == nil || msg.Branch.Name != "empty" {
		t.Fatalf("empty branch is not a valid scope: %#v", msg)
	}
}

func TestAppBuildBrowserFilterListsBranchesWithoutRuntimeResolution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/apps/"+pickerAppID+"/branches" {
			t.Errorf("browse filter resolved runtime selection: %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"branches":[{"id":%q,"name":"base","is_default":true},{"id":%q,"name":"empty"}],"total_pages":1}`, pickerBranchID, pickerOtherBranchID)
	}))
	defer server.Close()
	m := newHubModel("dev", false)
	m.client, m.selectedAppID = api.NewClientWithBaseURL("test", server.URL), pickerAppID
	m.appBuildScope.Search = "release"
	next, cmd := m.beginBuildPicker(pickerAppID, "Filter builds by branch", "app-filter", "")
	m = next.(hubModel)
	next, _ = m.updateHubBuildPicker(cmd())
	m = next.(hubModel)
	if view := m.buildPicker.View(); m.buildPicker.err != nil || m.buildPicker.options()[0] != "All branches" || strings.Contains(view, defaultBranchLabel) || strings.Contains(view, "Automatic") || !strings.Contains(view, "base (default)") {
		t.Fatalf("browse filter has runtime defaults: %s", m.buildPicker.View())
	}
	m.buildPicker.cursor = 2
	next, _ = m.updateHubBuildPicker(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(hubModel)
	if m.appBuildScope.BranchID != pickerOtherBranchID || m.appBuildScope.Search != "release" {
		t.Fatalf("filter selection changed search or lost empty branch: %#v", m.appBuildScope)
	}
}

func TestAppBuildBrowserEntryAndReentryResetToAll(t *testing.T) {
	for _, entry := range []string{"app_list", "created_app", "created_app_upload"} {
		t.Run(entry, func(t *testing.T) {
			m := newHubModel("dev", false)
			m.appBuildScope = appBuildQuery{BranchID: pickerOtherBranchID, Search: "old"}
			m.appBuildBranch = &api.BuildBranch{ID: pickerOtherBranchID, Name: "old"}
			m.appBuilds = []api.BuildVersion{{ID: pickerBuildID}}
			m.appBuildCursor, m.appBuildPage, m.appBuildHasNext = 2, 3, true
			var next tea.Model
			if entry == "app_list" {
				m.apps = []api.App{{ID: pickerAppID, Name: "App"}}
				next, _ = m.handleAppListKey(tea.KeyMsg{Type: tea.KeyEnter})
			} else {
				app := newCreateAppModel(nil, 80, 24)
				app.done, app.createdID, app.createdName = true, pickerAppID, "App"
				app.uploadNow = entry == "created_app_upload"
				m.createAppModel = &app
				next, _ = m.updateCreateApp(tea.WindowSizeMsg{Width: 80, Height: 24})
			}
			m = next.(hubModel)
			if m.appBuildScope != (appBuildQuery{}) || m.appBuildBranch != nil || len(m.appBuilds) != 0 || m.appBuildPage != 0 || m.appBuildHasNext || m.appBuildCursor != 0 {
				t.Fatalf("entry retained previous browsing scope: %#v", m.appBuildScope)
			}
			if entry == "created_app_upload" && m.uploadBuildModel.branchSelection != (api.BuildSelection{}) {
				t.Fatal("new app browser default changed upload destination")
			}
		})
	}
}

func TestAppBuildBrowserReturnFromUploadResetsToAll(t *testing.T) {
	for _, action := range []string{"escape", "back_to_builds"} {
		t.Run(action, func(t *testing.T) {
			buildRequests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				buildRequests++
				query := r.URL.Query()
				if r.URL.Path != "/api/v1/apps/"+pickerAppID+"/builds" || query.Get("page") != "1" || query.Has("branch_id") || query.Has("search") {
					t.Errorf("return did not fetch All page one: %s", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"items":[{"id":%q,"version":"new_upload","branch_name":"base"},{"id":"feature-build","version":"feature_build","branch_name":"feature"}],"has_next":false}`, pickerBuildID)
			}))
			defer server.Close()
			m := newHubModel("dev", false)
			m.client = api.NewClientWithBaseURL("test", server.URL)
			m.width, m.height = 80, 24
			m.selectedAppID, m.currentView = pickerAppID, viewUploadBuild
			m.appBuildScope = appBuildQuery{BranchID: pickerOtherBranchID, Search: "old"}
			m.appBuildBranch = &api.BuildBranch{ID: pickerOtherBranchID, Name: "feature"}
			m.appBuilds = []api.BuildVersion{{ID: "stale", Version: "old"}}
			m.appBuildCursor, m.appBuildPage, m.appBuildHasNext, m.appBuildSearching = 52, 3, true, true
			upload := newUploadBuildModel(m.client, pickerAppID, "App", 80, 24)
			upload.branchSelection.BranchID, upload.branchLabel = pickerBranchID, "base"
			m.uploadBuildModel = &upload
			key := tea.KeyMsg{Type: tea.KeyEsc}
			if action == "back_to_builds" {
				upload.step, upload.doneCursor = uploadStepDone, 1
				key.Type = tea.KeyEnter
			}
			next, cmd := m.updateUploadBuild(key)
			m = next.(hubModel)
			if m.currentView != viewAppDetail || m.uploadBuildModel != nil || m.appBuildScope != (appBuildQuery{}) || m.appBuildBranch != nil || m.appBuildCursor != 0 || m.appBuildPage != 0 || len(m.appBuilds) != 0 || m.appBuildHasNext || m.appBuildSearching || !m.appsLoading {
				t.Fatalf("return retained previous browsing state: scope=%#v, branch=%#v, cursor=%d, page=%d", m.appBuildScope, m.appBuildBranch, m.appBuildCursor, m.appBuildPage)
			}
			if upload.branchSelection.BranchID != pickerBranchID || upload.branchLabel != "base" {
				t.Fatal("browser reset changed the upload destination")
			}
			if cmd == nil {
				t.Fatal("return did not request builds")
			}
			buildFetch := cmd().(tea.BatchMsg)[0]
			next, _ = m.Update(buildFetch())
			m = next.(hubModel)
			if m.err != nil || buildRequests != 1 || len(m.appBuilds) != 2 || m.appBuildPage != 1 || m.appsLoading || m.appBuildBranch != nil {
				t.Fatalf("All response not accepted: requests=%d, builds=%#v, err=%v", buildRequests, m.appBuilds, m.err)
			}
			view := m.renderAppDetail()
			for _, label := range []string{"All branches", "new_upload", "[base]", "feature_build", "[feature]"} {
				if !strings.Contains(view, label) {
					t.Errorf("return omitted %q: %s", label, view)
				}
			}
			if strings.Contains(view, "Search:") {
				t.Fatalf("return retained search: %s", view)
			}
		})
	}
}
