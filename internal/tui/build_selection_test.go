package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/revyl/cli/internal/api"
)

const pickerAppID = "00000000-0000-0000-0000-000000000001"
const pickerBranchID = "00000000-0000-0000-0000-000000000002"
const pickerBuildID = "00000000-0000-0000-0000-000000000003"
const pickerOtherBranchID = "00000000-0000-0000-0000-000000000004"

func pickerKey(m buildPickerModel, key tea.KeyType) (buildPickerModel, tea.Cmd) {
	return m.Update(tea.KeyMsg{Type: key})
}

func TestBuildPickerKeepsAutomaticAndNamedLatestDistinct(t *testing.T) {
	for _, named := range []bool{false, true} {
		m := newBuildPicker(nil, pickerAppID, "Build")
		m.loading = false
		m.branches = []api.BuildBranch{{ID: pickerBranchID, Name: "all"}}
		if named {
			m.cursor = 1
		}
		m, _ = pickerKey(m, tea.KeyEnter)
		m.cursor = 1
		m, _ = pickerKey(m, tea.KeyEnter)
		if !m.done || m.selection.Mode != api.BuildSelectionModeLatest {
			t.Fatalf("wrong mode: %#v", m.selection)
		}
		if named && m.selection.BranchID != pickerBranchID {
			t.Fatalf("named branch lost: %#v", m.selection)
		}
		if !named && m.selection.BranchID != "" {
			t.Fatalf("Automatic was frozen: %#v", m.selection)
		}
	}
}

func TestBranchPickerShowsTheDefaultBranchOnce(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + pickerAppID:
			fmt.Fprintf(w, `{"id":%q,"default_branch_id":%q}`, pickerAppID, pickerBranchID)
		case "/api/v1/apps/" + pickerAppID + "/branches/" + pickerBranchID:
			fmt.Fprintf(w, `{"id":%q,"name":"base"}`, pickerBranchID)
		case "/api/v1/apps/" + pickerAppID + "/branches":
			mainBranch := fmt.Sprintf(`{"id":%q,"name":"main","pinned_build_id":%q,"pinned_build_version":"main-2","pinned_build_available":true}`, pickerOtherBranchID, pickerBuildID)
			if r.URL.Query().Get("search") != "" {
				fmt.Fprintf(w, `{"branches":[%s],"total_pages":1}`, mainBranch)
				return
			}
			fmt.Fprintf(w, `{"branches":[{"id":%q,"name":"base","is_default":true,"latest_build":{"id":%q,"version":"base-4","uploaded_at":"2026-10-11T00:00:00Z"}},%s],"total_pages":1}`, pickerBranchID, pickerBuildID, mainBranch)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	load := func(currentBranchID string) buildPickerModel {
		m := newBuildPicker(api.NewClientWithBaseURL("test", server.URL), pickerAppID, "Build")
		m.currentBranchID = currentBranchID
		m, _ = m.Update(m.Init()())
		if m.err != nil {
			t.Fatal(m.err)
		}
		return m
	}
	assertOptions := func(m buildPickerModel, want ...string) {
		t.Helper()
		if got := m.options(); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("options = %q, want %q", got, want)
		}
	}

	m := load("")
	assertOptions(m, "Default branch (base) · Latest build (base-4)", "main · Pinned build (main-2)")
	chosen, _ := pickerKey(m, tea.KeyEnter)
	assertOptions(chosen, "Automatic", "Latest build", "Exact build")
	chosen, _ = pickerKey(chosen, tea.KeyEnter)
	if !chosen.done || chosen.selection.BranchID != "" || chosen.selection.Mode != api.BuildSelectionModeInherit || chosen.label != defaultBranchLabel {
		t.Fatalf("Default branch choice froze a branch: %#v, label %q", chosen.selection, chosen.label)
	}

	explicit := load(pickerBranchID)
	assertOptions(explicit, "Default branch (base) · Latest build (base-4)", "base (default) · Latest build (base-4)", "main · Pinned build (main-2)")
	explicit.cursor = 1
	explicit, _ = pickerKey(explicit, tea.KeyEnter)
	if explicit.selection.BranchID != pickerBranchID {
		t.Fatalf("current explicit default branch was not selectable: %#v", explicit.selection)
	}

	m, _ = m.Update(keyRune('/'))
	for _, ch := range "mai" {
		m, _ = m.Update(keyRune(ch))
	}
	m, cmd := pickerKey(m, tea.KeyEnter)
	m, _ = m.Update(cmd())
	assertOptions(m, "Default branch (base) · Latest build", "main · Pinned build (main-2)")
}

func TestBuildPickerAllIsDistinctFromBranchNamedAll(t *testing.T) {
	m := newBuildPicker(nil, pickerAppID, "Builds")
	m.loading, m.branchOnly, m.allowAll = false, true, true
	m.branches = []api.BuildBranch{{ID: pickerBranchID, Name: "all"}}
	m.cursor = 1
	named, _ := pickerKey(m, tea.KeyEnter)
	if !named.done || named.label != "all" || named.selection.BranchID != pickerBranchID {
		t.Fatalf("named all lost: %#v", named)
	}
	m.cursor = 0
	all, _ := pickerKey(m, tea.KeyEnter)
	if !all.done || all.label != "All branches" || all.selection.BranchID != "" {
		t.Fatalf("All scope lost: %#v", all)
	}
}

func TestBuildPickerPaginatesExactChoicesWithinResolvedBranch(t *testing.T) {
	buildPages := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + pickerAppID:
			fmt.Fprintf(w, `{"id":%q,"default_branch_id":%q}`, pickerAppID, pickerBranchID)
		case "/api/v1/apps/" + pickerAppID + "/branches/" + pickerBranchID:
			fmt.Fprintf(w, `{"id":%q,"app_id":%q,"name":"main"}`, pickerBranchID, pickerAppID)
		case "/api/v1/apps/" + pickerAppID + "/branches":
			if r.URL.Query().Get("page") == "1" {
				fmt.Fprintf(w, `{"branches":[{"id":%q,"name":"main"}],"total_pages":2}`, pickerBranchID)
			} else {
				fmt.Fprintf(w, `{"branches":[{"id":%q,"name":"staging"}],"total_pages":2}`, pickerOtherBranchID)
			}
		case "/api/v1/apps/" + pickerAppID + "/builds":
			buildPages++
			if r.URL.Query().Get("branch_id") != pickerBranchID {
				t.Error("build page lost branch scope")
			}
			if r.URL.Query().Get("page") == "1" {
				fmt.Fprint(w, `{"items":[],"has_next":true}`)
			} else {
				fmt.Fprintf(w, `{"items":[{"id":%q,"version":"old-exact"}],"has_next":false}`, pickerBuildID)
			}
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	m := newBuildPicker(api.NewClientWithBaseURL("test", server.URL), pickerAppID, "Build")
	m, _ = m.Update(m.Init()())
	if m.err != nil || len(m.branches) != 1 || !m.moreBranches {
		t.Fatalf("first branch page failed: %#v", m)
	}
	m, cmd := m.Update(keyRune('m'))
	m, _ = m.Update(cmd())
	if m.err != nil || len(m.branches) != 2 || m.moreBranches {
		t.Fatalf("branch pagination failed: %#v", m)
	}
	m, _ = pickerKey(m, tea.KeyEnter)
	m.cursor = 2
	m, cmd = pickerKey(m, tea.KeyEnter)
	m, _ = m.Update(cmd())
	if buildPages != 1 || !m.moreBuilds {
		t.Fatalf("build list eagerly fetched %d pages", buildPages)
	}
	m, cmd = m.Update(keyRune('m'))
	m, _ = m.Update(cmd())
	m, _ = pickerKey(m, tea.KeyEnter)
	if m.err != nil || !m.done || m.selection.BuildID != pickerBuildID || m.selection.BranchID != pickerBranchID || buildPages != 2 {
		t.Fatalf("exact pin lost branch/page: %#v, pages=%d", m.selection, buildPages)
	}
}

func TestBuildPickerEmptyBranchCannotInventPinAndIgnoresOldResponse(t *testing.T) {
	m := newBuildPicker(nil, pickerAppID, "Build")
	m.loading, m.step = false, buildPickerExact
	m, _ = m.Update(buildPickerDataMsg{id: "old", builds: []api.BuildVersion{{ID: pickerBuildID}}})
	m, _ = pickerKey(m, tea.KeyEnter)
	if m.done || m.selection.BuildID != "" {
		t.Fatal("empty branch created a pin")
	}
	if !strings.Contains(m.View(), "No builds on this branch") {
		t.Fatal("empty branch is not actionable")
	}
	m, _ = pickerKey(m, tea.KeyEsc)
	if !m.done || !m.cancelled {
		t.Fatal("cancel was not retained")
	}
}

func TestBuildPickerRetryResetsCursorForShorterExactChoices(t *testing.T) {
	for _, remaining := range []int{0, 1} {
		t.Run(fmt.Sprintf("remaining_%d", remaining), func(t *testing.T) {
			m := newBuildPicker(nil, pickerAppID, "Build")
			m.loading, m.step, m.cursor = false, buildPickerExact, 3
			m.builds = []api.BuildVersion{{ID: "old-1"}, {ID: "old-2"}, {ID: "old-3"}, {ID: "old-4"}}
			m, cmd := m.Update(keyRune('R'))
			if !m.loading || cmd == nil {
				t.Fatal("retry did not request refreshed choices")
			}
			builds := []api.BuildVersion{{ID: pickerBuildID, Version: "remaining"}}
			m, _ = m.Update(buildPickerDataMsg{id: m.id, branchID: pickerBranchID, builds: builds[:remaining]})
			if m.cursor != 0 {
				t.Fatalf("refreshed cursor = %d, want 0", m.cursor)
			}
			m, _ = pickerKey(m, tea.KeyEnter)
			if remaining == 0 {
				if m.done || m.selection.BuildID != "" {
					t.Fatal("empty refresh created an exact pin")
				}
			} else if !m.done || m.selection.BuildID != pickerBuildID || m.selection.BranchID != pickerBranchID {
				t.Fatalf("remaining exact choice was not selected: %#v", m.selection)
			}
		})
	}
}

func TestSavedTestAutomaticClearsCanonicalPinWithNulls(t *testing.T) {
	selectGitBranchWorktree(t, "Feature/Customer")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handleTestCLITraceFallback(w, r) {
			return
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		for _, field := range []string{"branch_id", "pinned_build_id"} {
			if value, ok := payload[field]; !ok || value != nil {
				t.Errorf("%s must explicitly clear, got %#v", field, payload)
			}
		}
		if payload["build_selection"] != "inherit" || payload["expected_version"] != float64(7) {
			t.Errorf("wrong update: %#v", payload)
		}
		if _, found := payload["git_branch_name"]; found {
			t.Error("saving Automatic persisted detected context")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"test","version":8}`)
	}))
	defer server.Close()
	msg := saveTestBuildCmd(api.NewClientWithBaseURL("test", server.URL), "test", 7, api.BuildSelection{Mode: api.BuildSelectionModeInherit})().(testBuildSavedMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
}

func TestWorkflowBuildSettingsPreservePinWhileDisablingAndReselectingApp(t *testing.T) {
	row := map[string]interface{}{"app_id": pickerAppID, "branch_id": pickerBranchID, "pinned_build_id": pickerBuildID, "build_selection": "pinned"}
	m := newHubModel("dev", false)
	m.wfSettingsSection = wfSettingsSectionApp
	m.wfSettingsWorkflow = &api.Workflow{BuildConfig: map[string]interface{}{"ios_build": row}, OverrideBuildConfig: true}
	next, _ := handleWorkflowSettingsKey(m, keyRune('e'))
	m = next.(hubModel)
	if m.wfSettingsWorkflow.OverrideBuildConfig || row["pinned_build_id"] != pickerBuildID {
		t.Fatal("disable erased saved pin")
	}
	m.wfSettingsEditField = "ios_app"
	m.wfSettingsAppMatches = []api.App{{ID: pickerAppID}}
	applySettingsEdit(&m)
	if got := m.wfSettingsWorkflow.BuildConfig["ios_build"].(map[string]interface{})["pinned_build_id"]; got != pickerBuildID {
		t.Fatal("reselecting same app erased saved pin")
	}
}

func TestUploadBuildFreezesExplicitBranchAndPreservesAutomaticContext(t *testing.T) {
	selectGitBranchWorktree(t, "main")
	file := filepath.Join(t.TempDir(), "app.apk")
	if err := os.WriteFile(file, []byte("artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handleTestCLITraceFallback(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + pickerAppID:
			fmt.Fprintf(w, `{"id":%q,"default_branch_id":%q}`, pickerAppID, pickerBranchID)
		case "/api/v1/apps/" + pickerAppID + "/branches":
			if r.URL.Query().Get("name") != "main" {
				t.Errorf("destination name lost: %s", r.URL)
			}
			fmt.Fprintf(w, `{"branches":[{"id":%q,"name":"main"}],"total_pages":1}`, pickerBranchID)
		case "/api/v1/apps/" + pickerAppID + "/builds/upload-session":
			var payload map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["version"] == "v0" {
				if payload["branch_id"] != pickerBranchID || payload["branch_name"] != nil || payload["git_branch_name"] != nil {
					t.Errorf("explicit upload must freeze destination ID: %#v", payload)
				}
			} else if payload["branch_id"] != nil || payload["branch_name"] != nil || payload["git_branch_name"] != "main" {
				t.Errorf("automatic upload must preserve Git context: %#v", payload)
			}
			fmt.Fprintf(w, `{"upload_id":"upload","upload_url":%q,"content_type":"application/octet-stream","branch_id":%q}`, server.URL+"/artifact", pickerBranchID)
		case "/artifact":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/apps/" + pickerAppID + "/builds":
			fmt.Fprintf(w, `{"id":%q,"version":"v1","branch_id":%q,"branch_name":"renamed"}`, pickerBuildID, pickerBranchID)
		case "/api/v1/apps/resolve":
			fmt.Fprintf(w, `{"version_id":"older-build","branch_id":%q,"branch_name":"renamed","resolution_status":"resolved"}`, pickerBranchID)
		case "/api/v1/apps/" + pickerAppID + "/branches/" + pickerBranchID:
			fmt.Fprintf(w, `{"id":%q,"name":"renamed"}`, pickerBranchID)
		case "/api/v1/apps/builds/older-build":
			fmt.Fprintf(w, `{"id":"older-build","app_id":%q,"version":"v0"}`, pickerAppID)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	for index, selection := range []api.BuildSelection{{BranchName: "main"}, {}} {
		msg := uploadBuildCmd(api.NewClientWithBaseURL("test", server.URL), pickerAppID, file, fmt.Sprintf("v%d", index), selection)().(BuildUploadedMsg)
		if msg.Err != nil || msg.VersionID != pickerBuildID || msg.Branch == nil || msg.Branch.ID != pickerBranchID {
			t.Fatalf("upload failed or lost frozen branch: %#v", msg)
		}
		if msg.BranchSummary != "renamed (Latest build: runs use v0)" {
			t.Fatalf("upload summary = %q, want the build the branch runs now", msg.BranchSummary)
		}
	}
}

func TestUploadAnotherRetainsBranchAndPickerCancelStaysInFlow(t *testing.T) {
	m := newHubModel("dev", false)
	m.appBuildScope = appBuildQuery{BranchID: pickerOtherBranchID, Search: "old"}
	browseScope := m.appBuildScope
	um := newUploadBuildModel(nil, pickerAppID, "App", 80, 24)
	um.branchSelection = api.BuildSelection{BranchID: pickerBranchID}
	um.branchLabel = "main"
	um.step, um.done, um.uploadMore = uploadStepDone, true, true
	m.uploadBuildModel, m.currentView = &um, viewUploadBuild
	next, _ := m.updateUploadBuild(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(hubModel)
	if m.currentView != viewUploadBuild || m.uploadBuildModel.branchSelection.BranchID != pickerBranchID || m.uploadBuildModel.branchLabel != "main" || m.appBuildScope != browseScope {
		t.Fatal("repeat upload changed destination")
	}
	picker := newBuildPicker(nil, pickerAppID, "Branch")
	m.uploadBuildModel.buildPicker = &picker
	next, _ = m.updateUploadBuild(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(hubModel)
	if m.currentView != viewUploadBuild || m.uploadBuildModel == nil || m.uploadBuildModel.buildPicker != nil || m.appBuildScope != browseScope {
		t.Fatal("picker cancel left upload flow")
	}
}

func TestExplicitAutomaticRunOverridesSavedBranch(t *testing.T) {
	m := newHubModel("dev", false)
	m.selectedTestDetail = &TestDetail{ID: "test", AppID: pickerAppID, BuildSelection: api.BuildSelection{BranchID: pickerOtherBranchID, Mode: api.BuildSelectionModePinned, BuildID: pickerBuildID}}
	picker := newBuildPicker(nil, pickerAppID, "Run override")
	picker.loading, picker.done = false, true
	picker.defaultBranch = &api.BuildBranch{ID: pickerBranchID, Name: "main"}
	picker.selection.Mode = api.BuildSelectionModeInherit
	m.buildPicker, m.buildPickerTarget = &picker, "test-run"
	next, _ := m.updateHubBuildPicker(tea.WindowSizeMsg{})
	m = next.(hubModel)
	if m.executionModel.buildSelection.BranchID != "" || m.executionModel.buildSelection.BuildID != "" || m.executionModel.overrideAppID != pickerAppID {
		t.Fatal("Automatic must resolve fresh runtime context before overriding saved branch/pin")
	}
	if m.selectedTestDetail.BuildSelection.BranchID != pickerOtherBranchID {
		t.Fatal("run override mutated saved selection")
	}
}

func TestWorkflowRuntimeOverrideKeepsSavedSelectionWhenOmitted(t *testing.T) {
	selectGitBranchWorktree(t, "Feature/Customer")
	for _, selection := range []api.BuildSelection{{}, {BranchName: "main", Mode: api.BuildSelectionModeLatest}} {
		t.Run(string(selection.Mode), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if handleTestCLITraceFallback(w, r) {
					return
				}
				var payload map[string]interface{}
				_ = json.NewDecoder(r.Body).Decode(&payload)
				if selection.BranchName == "" {
					if payload["git_branch_name"] != "Feature/Customer" {
						t.Error("ordinary run lost separate Git branch")
					}
					if _, found := payload["branch_name"]; found {
						t.Error("ordinary run overrode saved branch")
					}
					if _, found := payload["build_selection"]; found {
						t.Error("ordinary run overrode saved pin")
					}
				} else if payload["branch_name"] != "main" || payload["build_selection"] != "latest" {
					t.Errorf("override lost: %#v", payload)
				} else if _, found := payload["git_branch_name"]; found {
					t.Error("explicit branch was inferred again")
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"task_id":"run"}`)
			}))
			defer server.Close()
			msg := executeWorkflowCmd(api.NewClientWithBaseURL("test", server.URL), "workflow", selection)().(WorkflowExecStartedMsg)
			if msg.Err != nil {
				t.Fatal(msg.Err)
			}
		})
	}
}

func TestAppBuildListDefaultAndExplicitScopes(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/" + pickerAppID + "/branches/" + pickerBranchID:
					if !explicit {
						t.Error("unfiltered browsing resolved a branch")
					}
					fmt.Fprintf(w, `{"id":%q,"name":"base","pinned_build_id":%q,"pinned_build_available":false}`, pickerBranchID, pickerBuildID)
				case "/api/v1/apps/" + pickerAppID + "/builds":
					want := ""
					if explicit {
						want = pickerBranchID
					}
					if r.URL.Query().Get("branch_id") != want {
						t.Errorf("wrong scope: %s", r.URL)
					}
					fmt.Fprintf(w, `{"items":[{"id":%q,"version":"base-build","branch_name":"base"}`, pickerBuildID)
					if !explicit {
						fmt.Fprint(w, `,{"id":"other-build","version":"feature-build","branch_name":"feature"}`)
					}
					fmt.Fprint(w, `]}`)
				default:
					t.Errorf("unexpected path: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			query := appBuildQuery{}
			if explicit {
				query.BranchID = pickerBranchID
			}
			msg := fetchAppBuildsCmd(api.NewClientWithBaseURL("test", server.URL), pickerAppID, query)().(AppBuildVersionsMsg)
			wantCount := 2
			if explicit {
				wantCount = 1
			}
			if msg.Err != nil || len(msg.Versions) != wantCount {
				t.Fatalf("list failed: %#v", msg)
			}
			if explicit && (msg.Branch == nil || msg.Branch.PinnedBuildID == nil) {
				t.Fatal("branch policy label lost")
			}
			if !explicit && msg.Branch != nil {
				t.Fatal("unfiltered browsing acquired one branch")
			}
		})
	}
}

func TestWorkflowBranchSaveFailureRemainsVisibleAndDirty(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handleTestCLITraceFallback(w, r) {
			return
		}
		requests++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/workflows/update_tests/workflow":
			fmt.Fprint(w, `{}`)
		case "/api/v1/workflows/update_build_config/workflow":
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"detail":"selected pin unavailable"}`)
		default:
			t.Errorf("save continued after branch error: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	msg := saveWorkflowSettingsCmd(api.NewClientWithBaseURL("test", server.URL), "workflow", &api.Workflow{}, nil)().(WorkflowSettingsSavedMsg)
	if msg.Err == nil || requests != 2 {
		t.Fatalf("save outcome wrong: %#v (%d requests)", msg, requests)
	}
	m := newHubModel("dev", false)
	m.currentView, m.wfSettingsDirty, m.wfSettingsLoading = viewWorkflowSettings, true, true
	next, _ := m.Update(msg)
	m = next.(hubModel)
	if m.currentView != viewWorkflowSettings || !m.wfSettingsDirty || m.err == nil {
		t.Fatal("failed pin save looked successful")
	}
	if want := "settings save failed: " + msg.Err.Error() + "; your edits are kept; retry saving"; m.err.Error() != want {
		t.Fatalf("failed pin save error = %q, want %q", m.err, want)
	}
	if rendered := renderWorkflowSettings(m); strings.Count(rendered, msg.Err.Error()) != 1 {
		t.Fatalf("failed pin save must render exactly one error line:\n%s", rendered)
	}
}

func TestDeviceBranchPickerCancellationReturnsToAppSelection(t *testing.T) {
	m := newHubModel("dev", false)
	m.deviceStartStep = int(deviceStartStepDevice)
	picker := newBuildPicker(nil, pickerAppID, "Session build")
	m.buildPicker, m.buildPickerTarget = &picker, "device"
	next, _ := m.updateHubBuildPicker(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(hubModel)
	if m.deviceStartStep != int(deviceStartStepApp) || m.buildPicker != nil {
		t.Fatal("cancel silently chose app default")
	}
}

func TestDevLoopExplicitBranchIsPassedAsAnArgument(t *testing.T) {
	cmd := devLoopExecCmd(true, "feature/release candidate")
	if got := strings.Join(cmd.Args[1:], "|"); got != "--dev|dev|--branch|feature/release candidate" {
		t.Fatalf("branch not forwarded exactly: %s", got)
	}
}

func TestWorkflowPickerBackReturnsToBranchName(t *testing.T) {
	m := newBuildPicker(nil, "", "Workflow")
	m.workflow, m.loading, m.step = true, false, buildPickerMode
	m, _ = pickerKey(m, tea.KeyBackspace)
	if m.step != buildPickerName || m.loading {
		t.Fatal("workflow attempted an app default lookup without an app")
	}
}

func TestHubBranchPickerKeepsBackgroundMessagesFlowing(t *testing.T) {
	m := newHubModel("dev", false)
	picker := newBuildPicker(nil, pickerAppID, "Build")
	m.buildPicker = &picker
	next, _ := m.Update(AppListMsg{Apps: []api.App{{ID: pickerAppID, Name: "Updated"}}})
	m = next.(hubModel)
	if len(m.apps) != 1 || m.buildPicker == nil {
		t.Fatal("picker swallowed background update")
	}
}

func TestBuildPickerOffersAllBranchesForRunsOnly(t *testing.T) {
	isDefault := true
	newPicker := func(offer bool) buildPickerModel {
		m := newBuildPicker(nil, pickerAppID, "Build")
		m.loading, m.offerAllBranches = false, offer
		m.defaultBranch = &api.BuildBranch{ID: pickerBranchID, Name: "base"}
		m.branches = []api.BuildBranch{{ID: pickerBranchID, Name: "base", IsDefault: &isDefault}, {ID: pickerOtherBranchID, Name: "main"}}
		return m
	}
	m := newPicker(true)
	if got := m.options(); len(got) != 3 || got[1] != allBranchesLatestLabel || !strings.HasPrefix(got[2], "main") {
		t.Fatalf("options = %q, want All branches after Default branch", got)
	}
	m.cursor = 1
	all, _ := pickerKey(m, tea.KeyEnter)
	if !all.done || all.selection != (api.BuildSelection{Mode: api.BuildSelectionModeLatestAllBranches}) || all.label != allBranchesLatestLabel {
		t.Fatalf("All branches = %#v, label %q", all.selection, all.label)
	}
	if buildSelectionLabel(all.selection) != allBranchesLatestLabel {
		t.Fatalf("label = %q", buildSelectionLabel(all.selection))
	}
	m.cursor = 2
	named, _ := pickerKey(m, tea.KeyEnter)
	if named.done || named.selection.BranchID != pickerOtherBranchID || named.step != buildPickerMode {
		t.Fatalf("branch row after All branches = %#v", named)
	}
	if got := newPicker(false).options(); len(got) != 2 || strings.Contains(strings.Join(got, "\n"), allBranchesLatestLabel) {
		t.Fatalf("upload or browse picker offered All branches: %q", got)
	}

	hub := newHubModel("dev", false)
	hub.client = api.NewClientWithBaseURL("test", "http://127.0.0.1:1")
	for target, want := range map[string]bool{"device": true, "test-run": true, "test-save": true, "workflow-ios": true, "app-filter": false} {
		next, _ := hub.beginBuildPicker(pickerAppID, "Build", target, "")
		if picker := next.(hubModel).buildPicker; picker == nil || picker.offerAllBranches != want {
			t.Fatalf("%s offers All branches = %v, want %t", target, picker, want)
		}
	}
}

func TestWorkflowPickerOffersAllBranchesForABlankName(t *testing.T) {
	for _, choice := range []struct {
		cursor int
		want   api.BuildSelection
	}{
		{cursor: 0},
		{cursor: 1, want: api.BuildSelection{Mode: api.BuildSelectionModeLatestAllBranches}},
	} {
		m := newBuildPicker(nil, "", "Workflow")
		m.workflow, m.offerAllBranches, m.loading, m.step = true, true, false, buildPickerName
		m, _ = pickerKey(m, tea.KeyEnter)
		if m.done || m.step != buildPickerMode || strings.Join(m.options(), "|") != keepSavedSettingsLabel+"|"+allBranchesLatestLabel {
			t.Fatalf("blank workflow name = step %v options %q", m.step, m.options())
		}
		m.cursor = choice.cursor
		m, _ = pickerKey(m, tea.KeyEnter)
		if !m.done || m.selection != choice.want {
			t.Fatalf("cursor %d chose %#v, want %#v", choice.cursor, m.selection, choice.want)
		}
	}
	devLoop := newBuildPicker(nil, "", "Dev loop")
	devLoop.workflow, devLoop.branchOnly, devLoop.loading, devLoop.step = true, true, false, buildPickerName
	devLoop, _ = pickerKey(devLoop, tea.KeyEnter)
	if !devLoop.done || devLoop.selection != (api.BuildSelection{}) {
		t.Fatalf("dev loop picker offered All branches: %#v", devLoop)
	}
}
