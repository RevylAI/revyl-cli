package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

const branchTestID = "22222222-2222-4222-8222-222222222222"
const buildTestID = "33333333-3333-4333-8333-333333333333"

func TestSavedBuildSelectionClearsAndOmitsIndependently(t *testing.T) {
	automatic, err := NewSavedBuildSelection("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		selector *SavedBuildSelection
		want     map[string]interface{}
	}{
		{"omitted", nil, map[string]interface{}{}},
		{"automatic", automatic, map[string]interface{}{"branch_id": nil, "build_selection": "inherit", "pinned_build_id": nil}},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(UpdateTestRequest{SavedBuildSelection: test.selector})
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]interface{}
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("got %s", encoded)
			}
		})
	}
	if _, err := NewSavedBuildSelection("", "pinned", buildTestID); err == nil {
		t.Fatal("saved pin must require branch")
	}
	if _, err := NewSavedBuildSelection(branchTestID, "latest", buildTestID); err == nil {
		t.Fatal("latest cannot carry exact pin")
	}
}

func TestResolveBuildSelectionFreezesCanonicalBuild(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/resolve":
			if r.URL.Query().Get("include_download_url") != "true" {
				t.Error("runnable resolution must request a download URL")
			}
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["branch_name"] != "all" || body["build_selection"] != "latest" {
				t.Errorf("wrong selection: %#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"version_id": buildTestID, "branch_id": branchTestID, "branch_name": "all", "download_url": "https://example.com/artifact", "resolution_status": "resolved"})
		case "/api/v1/apps/builds/" + buildTestID:
			if r.URL.Query().Get("include_download_url") != "false" {
				t.Error("resolution should reuse its artifact URL")
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": buildTestID, "version": "v1", "app_id": "app", "metadata": map[string]interface{}{"git": map[string]interface{}{"branch": "feature/source"}}})
		default:
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient("test")
	client.baseURL = server.URL
	got, err := client.ResolveBuildSelection(context.Background(), "app", BuildSelection{BranchName: "all", Mode: BuildSelectionModeLatest})
	if err != nil {
		t.Fatal(err)
	}
	if got.BranchID != branchTestID || got.ID != buildTestID || got.DownloadURL == "" {
		t.Fatalf("wrong resolved detail %#v", got)
	}
}

func TestResolveBuildSelectionOnlyBootstrapsConfirmedEmpty(t *testing.T) {
	for _, test := range []struct {
		status       string
		httpStatus   int
		empty        bool
		incompatible bool
	}{{"branch_has_no_builds", 200, true, false}, {"no_compatible_build", 200, false, true}, {"", 400, false, false}} {
		t.Run(test.status, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.httpStatus)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"resolution_status": test.status, "detail": "selected build unavailable"})
			}))
			defer server.Close()
			client := NewClient("test")
			client.baseURL = server.URL
			_, err := client.ResolveBuildSelection(context.Background(), "app", BuildSelection{})
			if err == nil || errors.Is(err, ErrBranchHasNoBuilds) != test.empty || errors.Is(err, ErrBranchHasNoCompatibleBuild) != test.incompatible {
				t.Fatalf("wrong error %v", err)
			}
		})
	}
}

func TestResolveBranchDefaultDoesNotFetchBuilds(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/app":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "app", "default_branch_id": branchTestID})
		case "/api/v1/apps/app/branches/" + branchTestID:
			_ = json.NewEncoder(w).Encode(BuildBranch{ID: branchTestID, AppID: "app", Name: "default"})
		default:
			t.Errorf("unexpected request %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient("test")
	client.baseURL = server.URL
	got, err := client.ResolveBranch(context.Background(), "app", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != branchTestID || calls != 2 {
		t.Fatalf("wrong result %#v (%d calls)", got, calls)
	}
}

func TestBuildDestinationResumeIdentity(t *testing.T) {
	legacy := uploadStateKey("app", "v1", "app.apk", 10)
	if got := uploadDestinationStateKey("app", "v1", "app.apk", 10, "", ""); got != legacy {
		t.Fatal("legacy resume identity must remain discoverable")
	}
	a := uploadDestinationStateKey("app", "v1", "app.apk", 10, "", "main")
	b := uploadDestinationStateKey("app", "v1", "app.apk", 10, "", "staging")
	if a == b || a == legacy {
		t.Fatal("branch destinations must not share upload resume state")
	}
}

func TestUploadDestinationIsFrozenByUploadSession(t *testing.T) {
	artifact := writeTestArtifact(t)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/app/builds/upload-session":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["branch_name"] != "staging" {
				t.Fatalf("destination not sent: %#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"upload_id": "upload", "upload_url": server.URL + "/artifact", "content_type": "application/octet-stream", "branch_id": branchTestID})
		case "/artifact":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/apps/app/builds":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if _, ok := body["branch_name"]; ok {
				t.Error("finalization must not re-resolve a branch name after rename")
			}
			if body["upload_id"] != "upload" {
				t.Error("finalization must use frozen session")
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": buildTestID, "version": "v1", "branch_id": branchTestID, "branch_name": "renamed"})
		default:
			t.Errorf("unexpected path %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient("test")
	client.baseURL = server.URL
	result, err := client.UploadBuild(context.Background(), &UploadBuildRequest{AppID: "app", BranchName: "staging", Version: "v1", FilePath: artifact})
	if err != nil {
		t.Fatal(err)
	}
	if result.BranchID != branchTestID || result.BranchName != "renamed" {
		t.Fatalf("missing destination provenance: %#v", result)
	}
}

func TestWorkflowDisablePreservesStoredSelector(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !reflect.DeepEqual(body, map[string]interface{}{"override_build_config": false}) {
			t.Errorf("unexpected payload %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client := NewClient("test")
	client.baseURL = server.URL
	if err := client.SetWorkflowBuildOverrideEnabled(context.Background(), "workflow", false); err != nil {
		t.Fatal(err)
	}
}

func TestAppSummaryDoesNotReplaceEmptyDefaultWithAnotherBranch(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/app":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "app", "default_branch_id": branchTestID, "versions_count": 0, "latest_version": nil})
		case "/api/v1/apps/app/builds":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"total": 1, "items": []map[string]interface{}{{"id": buildTestID, "version": "feature-only"}}})
		default:
			t.Errorf("unexpected path %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClientWithBaseURL("test", server.URL)
	app, err := client.GetApp(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if app.LatestVersion != "" || app.VersionsCount != 0 || calls != 1 {
		t.Fatalf("default summary replaced by global summary: %#v, calls=%d", app, calls)
	}
}

func TestBranchListKeepsDefaultMarkerOnlyWhereTheAPIReturnsIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"branches":[{"id":"default","app_id":"app","name":"base","is_default":true},{"id":"other","app_id":"app","name":"staging","is_default":false}],"total":2}`))
	}))
	defer server.Close()
	page, err := NewClientWithBaseURL("test", server.URL).ListBuildBranches(context.Background(), "app", BranchListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Branches[0].IsDefault == nil || !*page.Branches[0].IsDefault || page.Branches[1].IsDefault == nil || *page.Branches[1].IsDefault {
		t.Fatalf("default markers not decoded: %#v", page.Branches)
	}
	listed, err := json.Marshal(page.Branches[1])
	if err != nil {
		t.Fatal(err)
	}
	single, err := json.Marshal(BuildBranch{ID: "other", Name: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(listed, []byte(`"is_default":false`)) || bytes.Contains(single, []byte("is_default")) {
		t.Fatalf("listed=%s single=%s", listed, single)
	}
}
