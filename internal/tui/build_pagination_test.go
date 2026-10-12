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

func TestBuildPickerLargeCatalogSelectsNewestAfterOnePage(t *testing.T) {
	branchPages, buildPages := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + pickerAppID:
			fmt.Fprintf(w, `{"id":%q,"default_branch_id":%q}`, pickerAppID, pickerBranchID)
		case "/api/v1/apps/" + pickerAppID + "/branches/" + pickerBranchID:
			fmt.Fprintf(w, `{"id":%q,"name":"main"}`, pickerBranchID)
		case "/api/v1/apps/" + pickerAppID + "/branches":
			branchPages++
			if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("page_size") != "100" {
				t.Errorf("unexpected branch page: %s", r.URL)
			}
			fmt.Fprintf(w, `{"branches":[{"id":%q,"name":"main"}],"total_pages":10000}`, pickerBranchID)
		case "/api/v1/apps/" + pickerAppID + "/builds":
			buildPages++
			if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("branch_id") != pickerBranchID {
				t.Errorf("unexpected build page: %s", r.URL)
			}
			fmt.Fprintf(w, `{"items":[{"id":%q,"version":"newest"}],"total":1000000,"has_next":true}`, pickerBuildID)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	m := newBuildPicker(api.NewClientWithBaseURL("test", server.URL), pickerAppID, "Build")
	m, _ = m.Update(m.Init()())
	if m.err != nil || branchPages != 1 || !m.moreBranches || !strings.Contains(m.View(), "m to load more") {
		t.Fatalf("first branch choices unavailable: pages=%d, err=%v", branchPages, m.err)
	}
	m, _ = pickerKey(m, tea.KeyEnter)
	m.cursor = 2
	m, cmd := pickerKey(m, tea.KeyEnter)
	m, _ = m.Update(cmd())
	m, _ = pickerKey(m, tea.KeyEnter)
	if m.err != nil || !m.done || m.selection.BuildID != pickerBuildID || m.selection.BranchID != pickerBranchID || buildPages != 1 {
		t.Fatalf("newest pin unavailable: selection=%#v, pages=%d, err=%v", m.selection, buildPages, m.err)
	}
}

func TestBuildPickerLoadMoreFailureKeepsChoicesSelectable(t *testing.T) {
	for _, step := range []buildPickerStep{buildPickerBranch, buildPickerExact} {
		t.Run(fmt.Sprint(step), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Query().Get("page") != "2" {
					t.Errorf("load more must fetch only the next page: %s", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, `{"detail":"page temporarily unavailable"}`)
			}))
			defer server.Close()
			m := newBuildPicker(api.NewClientWithBaseURL("test", server.URL), pickerAppID, "Build")
			m.loading, m.step, m.branchOnly = false, step, true
			m.defaultBranch, m.selection.BranchID = &api.BuildBranch{ID: pickerOtherBranchID, Name: "base"}, pickerBranchID
			m.branches = []api.BuildBranch{{ID: pickerBranchID, Name: "main"}}
			m.builds = []api.BuildVersion{{ID: pickerBuildID, Version: "newest"}}
			m.branchPage, m.buildPage, m.moreBranches, m.moreBuilds = 1, 1, true, true
			if step == buildPickerBranch {
				m.cursor = 1
			}
			m, cmd := m.Update(keyRune('m'))
			if !strings.Contains(m.View(), "main") && !strings.Contains(m.View(), "newest") {
				t.Fatal("loading more hid existing choices")
			}
			m, _ = m.Update(cmd())
			if m.err == nil || m.loading || len(m.branches) != 1 || len(m.builds) != 1 || requests != api.DefaultMaxRetries+1 {
				t.Fatalf("page error discarded choices: requests=%d, err=%v", requests, m.err)
			}
			m, _ = pickerKey(m, tea.KeyEnter)
			if !m.done || m.selection.BranchID != pickerBranchID || (step == buildPickerExact && m.selection.BuildID != pickerBuildID) {
				t.Fatalf("loaded choice cannot be selected after later page failure: %#v", m.selection)
			}
		})
	}
}

func TestBuildPickerSearchIsSubmittedAndPaginatedOnServer(t *testing.T) {
	for _, step := range []buildPickerStep{buildPickerBranch, buildPickerExact} {
		t.Run(fmt.Sprint(step), func(t *testing.T) {
			var pages, searches []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/" + pickerAppID:
					fmt.Fprintf(w, `{"id":%q,"default_branch_id":%q}`, pickerAppID, pickerBranchID)
				case "/api/v1/apps/" + pickerAppID + "/branches/" + pickerBranchID:
					fmt.Fprintf(w, `{"id":%q,"name":"main"}`, pickerBranchID)
				case "/api/v1/apps/" + pickerAppID + "/branches", "/api/v1/apps/" + pickerAppID + "/builds":
					pages = append(pages, r.URL.Query().Get("page"))
					searches = append(searches, r.URL.Query().Get("search"))
					if step == buildPickerExact && r.URL.Query().Get("branch_id") != pickerBranchID {
						t.Errorf("search lost resolved branch: %s", r.URL)
					}
					fmt.Fprint(w, `{"branches":[],"items":[],"has_next":true,"total_pages":10000}`)
				default:
					t.Errorf("unexpected request: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			m := newBuildPicker(api.NewClientWithBaseURL("test", server.URL), pickerAppID, "Build")
			m.loading, m.step = false, step
			m.selection.BranchID = pickerBranchID
			m, _ = m.Update(keyRune('/'))
			for _, ch := range "release&/β" {
				m, _ = m.Update(keyRune(ch))
			}
			if len(pages) != 0 {
				t.Fatal("typing triggered requests")
			}
			m, cmd := pickerKey(m, tea.KeyEnter)
			m, _ = m.Update(cmd())
			m, cmd = m.Update(keyRune('m'))
			m, _ = m.Update(cmd())
			m, _ = m.Update(keyRune('/'))
			m.searchInput.SetValue("")
			m, cmd = pickerKey(m, tea.KeyEnter)
			m, _ = m.Update(cmd())
			if m.err != nil || !reflect.DeepEqual(pages, []string{"1", "2", "1"}) || !reflect.DeepEqual(searches, []string{"release&/β", "release&/β", ""}) {
				t.Fatalf("search must fetch one scoped page per action: pages=%v, searches=%v, err=%v", pages, searches, m.err)
			}
		})
	}
}

func TestBuildPickerLoadingBranchesKeepsAllBranchesSelected(t *testing.T) {
	m := newBuildPicker(nil, pickerAppID, "Branch")
	m.loading, m.allowAll, m.branchOnly = false, true, true
	m.branches = []api.BuildBranch{{ID: pickerBranchID, Name: "main"}}
	m.cursor = 0
	m, _ = m.Update(buildPickerDataMsg{id: m.id, page: 2, branches: []api.BuildBranch{{ID: pickerOtherBranchID, Name: "other"}}})
	m, _ = pickerKey(m, tea.KeyEnter)
	if !m.done || m.label != "All branches" || m.selection.BranchID != "" {
		t.Fatalf("appending branches changed the selected All branches action: %#v", m.selection)
	}
}

func TestAppBuildListLoadsOnePageAndRetainsChoicesOnPageFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	branchReads := 0
	var pages []string
	failPage := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + pickerAppID:
			fmt.Fprintf(w, `{"id":%q,"default_branch_id":%q}`, pickerAppID, pickerBranchID)
		case "/api/v1/apps/" + pickerAppID + "/branches/" + pickerBranchID:
			branchReads++
			fmt.Fprintf(w, `{"id":%q,"name":"main"}`, pickerBranchID)
		case "/api/v1/apps/" + pickerAppID + "/builds":
			pages = append(pages, r.URL.Query().Get("page"))
			if r.URL.Query().Get("branch_id") != pickerBranchID || r.URL.Query().Get("search") != "release" {
				t.Errorf("build page lost scope: %s", r.URL)
			}
			if r.URL.Query().Get("page") == "2" {
				if failPage {
					w.WriteHeader(http.StatusServiceUnavailable)
					fmt.Fprint(w, `{"detail":"page unavailable"}`)
					return
				}
				fmt.Fprint(w, `{"items":[{"id":"older-build","version":"older"}],"has_next":false}`)
				return
			}
			fmt.Fprintf(w, `{"items":[{"id":%q,"version":"newest"}],"total":1000000,"has_next":true}`, pickerBuildID)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	m := newHubModel("dev", false)
	m.client, m.selectedAppID, m.currentView = api.NewClientWithBaseURL("test", server.URL), pickerAppID, viewAppDetail
	m.appBuildScope = appBuildQuery{BranchID: pickerBranchID, Search: "release"}
	next, _ := m.Update(fetchAppBuildsCmd(m.client, pickerAppID, m.appBuildScope)())
	m = next.(hubModel)
	if m.err != nil || len(pages) != 1 || len(m.appBuilds) != 1 || !m.appBuildHasNext {
		t.Fatalf("first page unavailable: pages=%v, err=%v", pages, m.err)
	}
	next, cmd := m.handleAppDetailKey(keyRune('m'))
	m = next.(hubModel)
	if !strings.Contains(m.renderAppDetail(), "newest") {
		t.Fatal("loading more hid loaded builds")
	}
	next, _ = m.Update(cmd())
	m = next.(hubModel)
	if m.err == nil || len(m.appBuilds) != 1 || m.appBuildPage != 1 || !m.appBuildHasNext {
		t.Fatalf("page error lost existing choices: page=%d, err=%v", m.appBuildPage, m.err)
	}
	failPage = false
	next, cmd = m.handleAppDetailKey(keyRune('m'))
	m = next.(hubModel)
	next, _ = m.Update(cmd())
	m = next.(hubModel)
	wantPages := []string{"1"}
	for range api.DefaultMaxRetries + 2 {
		wantPages = append(wantPages, "2")
	}
	if m.err != nil || len(m.appBuilds) != 2 || m.appBuildHasNext || branchReads != 1 || !reflect.DeepEqual(pages, wantPages) {
		t.Fatalf("page retry failed or re-resolved branch: pages=%v, branchReads=%d, err=%v", pages, branchReads, m.err)
	}
	next, cmd = m.handleAppDetailKey(keyRune('R'))
	m = next.(hubModel)
	next, _ = m.Update(cmd())
	m = next.(hubModel)
	wantPages = append(wantPages, "1")
	if m.err != nil || m.appBuildScope != (appBuildQuery{BranchID: pickerBranchID, Search: "release"}) || m.appBuildPage != 1 || len(m.appBuilds) != 1 || branchReads != 2 || !reflect.DeepEqual(pages, wantPages) {
		t.Fatalf("refresh lost branch or search scope: scope=%#v, pages=%v, err=%v", m.appBuildScope, pages, m.err)
	}
}

func TestAppBuildSearchUsesOnePageAndIgnoresStaleQuery(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(fmt.Sprint(all), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				wantBranch := pickerBranchID
				if all {
					wantBranch = ""
				}
				if r.URL.Path != "/api/v1/apps/"+pickerAppID+"/builds" || r.URL.Query().Get("page") != "1" || r.URL.Query().Get("search") != "release&/β" || r.URL.Query().Get("branch_id") != wantBranch {
					t.Errorf("unexpected search request: %s", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"items":[{"id":%q,"version":"release"}],"has_next":true}`, pickerBuildID)
			}))
			defer server.Close()
			m := newHubModel("dev", false)
			m.client, m.selectedAppID, m.currentView = api.NewClientWithBaseURL("test", server.URL), pickerAppID, viewAppDetail
			if !all {
				m.appBuildScope.BranchID = pickerBranchID
			}
			m.appBuildBranch = &api.BuildBranch{ID: pickerBranchID, Name: "main"}
			next, _ := m.handleAppDetailKey(keyRune('/'))
			m = next.(hubModel)
			for _, ch := range "release&/β" {
				next, _ = m.handleAppDetailKey(keyRune(ch))
				m = next.(hubModel)
			}
			if requests != 0 {
				t.Fatal("typing triggered requests")
			}
			next, cmd := m.handleAppDetailKey(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(hubModel)
			next, _ = m.Update(AppBuildVersionsMsg{AppID: pickerAppID, Query: appBuildQuery{BranchID: m.appBuildScope.BranchID}, Versions: []api.BuildVersion{{ID: "stale"}}})
			m = next.(hubModel)
			if !m.appsLoading || len(m.appBuilds) != 0 {
				t.Fatal("stale query replaced pending search")
			}
			next, _ = m.Update(cmd())
			m = next.(hubModel)
			if m.err != nil || requests != 1 || len(m.appBuilds) != 1 || m.appBuilds[0].ID != pickerBuildID {
				t.Fatalf("search results unavailable: requests=%d, err=%v", requests, m.err)
			}
		})
	}
}

func TestAppBuildListIgnoresOldPageAfterReopeningSameApp(t *testing.T) {
	for _, oldPageFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(oldPageFirst), func(t *testing.T) {
			m := newHubModel("dev", false)
			m.client = api.NewClientWithBaseURL("test", "http://unused.invalid")
			m.currentView, m.selectedAppID = viewAppDetail, pickerAppID
			m.apps = []api.App{{ID: pickerAppID, Name: "App"}}
			m.appBuilds = []api.BuildVersion{{ID: pickerBuildID, Version: "newest"}}
			m.appBuildPage, m.appBuildHasNext = 1, true
			next, _ := m.handleAppDetailKey(keyRune('m'))
			m = next.(hubModel)
			oldPage := AppBuildVersionsMsg{RequestID: m.appBuildRequestID, AppID: pickerAppID, Query: m.appBuildScope, Page: 2, HasNext: true, Versions: []api.BuildVersion{{ID: "old-page-two"}}}
			next, _ = m.handleAppDetailKey(tea.KeyMsg{Type: tea.KeyEsc})
			m = next.(hubModel)
			next, _ = m.handleAppListKey(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(hubModel)
			firstPage := AppBuildVersionsMsg{RequestID: m.appBuildRequestID, AppID: pickerAppID, Query: m.appBuildScope, Page: 1, HasNext: true, Versions: []api.BuildVersion{{ID: pickerBuildID}}}
			if oldPageFirst {
				next, _ = m.Update(oldPage)
				m = next.(hubModel)
				if !m.appsLoading || len(m.appBuilds) != 0 || m.appBuildPage != 0 {
					t.Fatal("old page completed new first-page request")
				}
			}
			next, _ = m.Update(firstPage)
			m = next.(hubModel)
			next, _ = m.handleAppDetailKey(keyRune('m'))
			m = next.(hubModel)
			next, _ = m.Update(oldPage)
			m = next.(hubModel)
			if !m.appsLoading || len(m.appBuilds) != 1 || m.appBuildPage != 1 {
				t.Fatal("old page duplicated rows or completed current page request")
			}
			next, _ = m.Update(AppBuildVersionsMsg{RequestID: m.appBuildRequestID, AppID: pickerAppID, Query: m.appBuildScope, Page: 2, Versions: []api.BuildVersion{{ID: "current-page-two"}}})
			m = next.(hubModel)
			if m.appsLoading || len(m.appBuilds) != 2 || m.appBuilds[1].ID != "current-page-two" {
				t.Fatal("current page unavailable after ignoring stale response")
			}
		})
	}
}
