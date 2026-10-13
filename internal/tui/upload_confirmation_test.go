package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/revyl/cli/internal/api"
)

type uploadConfirmationFixture struct {
	client           *api.Client
	defaultBranchID  string
	branchNames      []string
	sessions         []api.BuildUploadSessionRequest
	completions      []api.BuildCreateFromUploadRequest
	completionFields []map[string]interface{}
	artifactBodies   []string
}

func newUploadConfirmationFixture(t *testing.T) *uploadConfirmationFixture {
	t.Helper()
	fixture := &uploadConfirmationFixture{defaultBranchID: pickerBranchID}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handleTestCLITraceFallback(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + pickerAppID:
			fmt.Fprintf(w, `{"id":%q,"default_branch_id":%q}`, pickerAppID, fixture.defaultBranchID)
		case "/api/v1/apps/" + pickerAppID + "/branches/" + pickerBranchID:
			fmt.Fprintf(w, `{"id":%q,"name":"feature/one"}`, pickerBranchID)
		case "/api/v1/apps/" + pickerAppID + "/branches/" + pickerOtherBranchID:
			fmt.Fprintf(w, `{"id":%q,"name":"feature/two"}`, pickerOtherBranchID)
		case "/api/v1/apps/" + pickerAppID + "/branches":
			name := r.URL.Query().Get("name")
			if name == "" {
				fmt.Fprintf(w, `{"branches":[{"id":%q,"name":"feature/one"},{"id":%q,"name":"feature/two"}],"total_pages":1}`, pickerBranchID, pickerOtherBranchID)
				return
			}
			fixture.branchNames = append(fixture.branchNames, name)
			branchID := pickerBranchID
			if name == "feature/two" {
				branchID = pickerOtherBranchID
			} else if name != "feature/one" {
				t.Errorf("unexpected branch lookup: %q", name)
			}
			fmt.Fprintf(w, `{"branches":[{"id":%q,"name":%q}],"total_pages":1}`, branchID, name)
		case "/api/v1/apps/" + pickerAppID + "/builds/upload-session":
			var request api.BuildUploadSessionRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			fixture.sessions = append(fixture.sessions, request)
			if r.URL.Query().Get("source") != "cli_upload" {
				t.Errorf("upload attribution changed: %s", r.URL)
			}
			fmt.Fprintf(w, `{"upload_id":%q,"upload_url":%q,"content_type":"application/octet-stream"}`, pickerBuildID, server.URL+"/artifact")
		case "/artifact":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			fixture.artifactBodies = append(fixture.artifactBodies, string(body))
			w.WriteHeader(http.StatusOK)
		case "/api/v1/apps/" + pickerAppID + "/builds":
			if r.Method == http.MethodGet {
				fmt.Fprint(w, `{"items":[],"has_next":false}`)
				return
			}
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			fixture.completionFields = append(fixture.completionFields, body)
			encoded, _ := json.Marshal(body)
			var request api.BuildCreateFromUploadRequest
			if err := json.Unmarshal(encoded, &request); err != nil {
				t.Error(err)
			}
			fixture.completions = append(fixture.completions, request)
			fmt.Fprintf(w, `{"id":%q,"version":%q}`, pickerBuildID, request.Version)
		case "/api/v1/apps/resolve":
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			fmt.Fprintf(w, `{"version_id":%q,"branch_id":%q,"branch_name":"resolved","resolution_status":"resolved"}`, pickerBuildID, request["branch_id"])
		case "/api/v1/apps/builds/" + pickerBuildID:
			fmt.Fprintf(w, `{"id":%q,"app_id":%q,"version":"reviewed_version"}`, pickerBuildID, pickerAppID)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	fixture.client = api.NewClientWithBaseURL("test", server.URL)
	return fixture
}

func openUploadConfirmation(t *testing.T, client *api.Client, query appBuildQuery) uploadBuildModel {
	t.Helper()
	m := newHubModel("dev", false)
	m.client, m.selectedAppID, m.selectedAppName, m.currentView = client, pickerAppID, "App", viewAppDetail
	m.appBuildScope = query
	next, _ := m.Update(m.startAppBuildFetch(1, nil)())
	m = next.(hubModel)
	if m.err != nil {
		t.Fatal(m.err)
	}
	return openUploadConfirmationFromBrowser(t, m)
}

func openUploadConfirmationFromBrowser(t *testing.T, m hubModel) uploadBuildModel {
	t.Helper()
	scope := m.appBuildScope
	next, _ := m.handleAppDetailKey(keyRune('u'))
	m = next.(hubModel)
	if m.currentView != viewUploadBuild || m.uploadBuildModel == nil {
		t.Fatal("app build list did not open upload")
	}
	if m.appBuildScope != scope {
		t.Fatalf("opening upload changed browser filter: got %#v, want %#v", m.appBuildScope, scope)
	}
	if m.uploadBuildModel.branchSelection != (api.BuildSelection{}) || m.uploadBuildModel.branchLabel != "" || m.uploadBuildModel.pendingBranch != nil {
		t.Fatal("new upload inherited the browser's branch")
	}
	file := filepath.Join(t.TempDir(), "app.apk")
	if err := os.WriteFile(file, []byte("artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	upload := *m.uploadBuildModel
	upload.filePathInput.SetValue(file)
	next, _ = upload.Update(tea.KeyMsg{Type: tea.KeyEnter})
	upload = next.(uploadBuildModel)
	upload.versionInput.SetValue("reviewed_version")
	next, _ = upload.Update(tea.KeyMsg{Type: tea.KeyEnter})
	upload = next.(uploadBuildModel)
	if upload.err != nil || upload.step != uploadStepConfirm {
		t.Fatalf("upload confirmation unavailable: step=%v, err=%v", upload.step, upload.err)
	}
	return upload
}

func confirmUploadDestination(t *testing.T, upload uploadBuildModel, fixture *uploadConfirmationFixture, branchID string) {
	t.Helper()
	_, cmd := upload.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := cmd().(tea.BatchMsg)[1]().(BuildUploadedMsg)
	if result.Err != nil || result.Branch == nil || result.VersionID != pickerBuildID {
		t.Fatalf("upload failed: %#v", result)
	}
	if result.Branch.ID != branchID {
		t.Errorf("confirmation destination changed: got %s, want %s", result.Branch.ID, branchID)
	}
	if len(fixture.sessions) != 1 || len(fixture.completions) != 1 || len(fixture.artifactBodies) != 1 {
		t.Fatalf("unexpected upload requests: sessions=%d, completions=%d, transfers=%d", len(fixture.sessions), len(fixture.completions), len(fixture.artifactBodies))
	}
	session, completion := fixture.sessions[0], fixture.completions[0]
	if session.BranchId == nil || session.BranchId.String() != branchID || session.BranchName != nil || session.ContextBranchName != nil {
		t.Errorf("upload request changed confirmed branch: %#v", session)
	}
	if session.Version != "reviewed_version" || completion.Version != session.Version || session.FileName != "app.apk" || fixture.artifactBodies[0] != "artifact" || completion.UploadId.String() != pickerBuildID {
		t.Errorf("upload identity changed: session=%#v, completion=%#v, artifact=%q", session, completion, fixture.artifactBodies)
	}
	if completion.Metadata == nil || (*completion.Metadata)["source"] != "cli_upload" {
		t.Errorf("upload metadata changed: %#v", completion)
	}
	for _, field := range []string{"pin", "set_as_current"} {
		if _, sent := fixture.completionFields[0][field]; sent {
			t.Errorf("TUI upload must not ask to pin its branch (%s): %#v", field, fixture.completionFields[0])
		}
	}
	if !strings.Contains(result.BranchSummary, "(Latest build: runs use this upload)") {
		t.Errorf("upload summary = %q, want the branch's Latest build to use this upload", result.BranchSummary)
	}
}

func TestAppBuildUploadStartsOnDefaultBranchRegardlessOfBrowserFilter(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		query  appBuildQuery
		branch *api.BuildBranch
	}{
		{name: "all branches"},
		{name: "loaded branch", query: appBuildQuery{BranchID: pickerBranchID, Search: "build"}, branch: &api.BuildBranch{ID: pickerBranchID, Name: "feature/one"}},
		{name: "unresolved branch", query: appBuildQuery{BranchID: pickerBranchID}},
		{name: "mismatched stale branch", query: appBuildQuery{BranchID: pickerOtherBranchID}, branch: &api.BuildBranch{ID: pickerBranchID, Name: "feature/one"}},
		{name: "all branches with stale branch", branch: &api.BuildBranch{ID: pickerBranchID, Name: "feature/one"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("GITHUB_ACTIONS", "")
			t.Setenv("BUILDKITE", "")
			t.Chdir(t.TempDir())
			fixture := newUploadConfirmationFixture(t)
			m := newHubModel("dev", false)
			m.client, m.selectedAppID, m.selectedAppName, m.currentView = fixture.client, pickerAppID, "App", viewAppDetail
			m.appBuildScope, m.appBuildBranch = testCase.query, testCase.branch
			upload := openUploadConfirmationFromBrowser(t, m)
			if !strings.Contains(upload.View(), "Branch:   "+defaultBranchLabel) {
				t.Fatalf("confirmation lost Default branch label: %s", upload.View())
			}
			fixture.defaultBranchID = pickerOtherBranchID
			confirmUploadDestination(t, upload, fixture, pickerOtherBranchID)
		})
	}
}

func TestAppBuildUploadPickerKeepsExplicitDestinationAfterDefaultChanges(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("BUILDKITE", "")
	t.Chdir(t.TempDir())
	fixture := newUploadConfirmationFixture(t)
	upload := openUploadConfirmation(t, fixture.client, appBuildQuery{BranchID: pickerOtherBranchID})
	upload.branchSelection, upload.branchLabel = api.BuildSelection{BranchID: pickerBranchID}, "feature/one"
	next, cmd := upload.Update(keyRune('b'))
	upload = next.(uploadBuildModel)
	next, _ = upload.Update(cmd())
	upload = next.(uploadBuildModel)
	if upload.buildPicker == nil || upload.buildPicker.err != nil || len(upload.buildPicker.branches) != 2 || !strings.Contains(upload.buildPicker.View(), "feature/one (default)") {
		t.Fatalf("upload branch picker did not load the current explicit default branch: %#v", upload.buildPicker)
	}
	next, _ = upload.Update(tea.KeyMsg{Type: tea.KeyDown})
	upload = next.(uploadBuildModel)
	next, _ = upload.Update(tea.KeyMsg{Type: tea.KeyEnter})
	upload = next.(uploadBuildModel)
	if upload.buildPicker != nil || upload.branchSelection.BranchID != pickerBranchID || !strings.Contains(upload.View(), "feature/one") {
		t.Fatalf("upload picker did not keep its explicit destination: %s", upload.View())
	}
	fixture.defaultBranchID = pickerOtherBranchID
	confirmUploadDestination(t, upload, fixture, pickerBranchID)
}
