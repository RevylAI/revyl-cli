package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFindUploadBranchUsesAppPolicyWithoutChangingExplicitChoices(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "explicit"}[explicit], func(t *testing.T) {
			appReads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/app":
					appReads++
					_, _ = w.Write([]byte(`{"id":"app","auto_git_branch_enabled":false,"default_branch_id":"` + branchTestID + `"}`))
				case "/api/v1/apps/app/branches/" + branchTestID:
					_ = json.NewEncoder(w).Encode(BuildBranch{ID: branchTestID, Name: "base"})
				case "/api/v1/apps/app/branches":
					if !explicit || r.URL.Query().Get("name") != "release" {
						t.Fatalf("automatic upload looked up Git branch: %s", r.URL)
					}
					_ = json.NewEncoder(w).Encode(BuildBranchesPage{Branches: []BuildBranch{{ID: branchTestID, Name: "release"}}})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			name := ""
			if explicit {
				name = "release"
			}
			branch, err := NewClientWithBaseURL("test", server.URL).FindUploadBranch(context.Background(), "app", "", name, " invalid for Revyl ")
			if err != nil {
				t.Fatal(err)
			}
			if explicit {
				if branch.Name != "release" || appReads != 0 {
					t.Fatalf("explicit selection changed: %+v reads=%d", branch, appReads)
				}
			} else if branch.Name != "base" || appReads != 1 || branch.AutoGitBranchEnabled == nil || *branch.AutoGitBranchEnabled || branch.BranchSource != BranchSourceDefaultBranch || branch.GitBranchName != "" {
				t.Fatalf("disabled selection incorrect: %+v reads=%d", branch, appReads)
			}
		})
	}
}

func TestResolveBuildSelectionPreservesDisabledPolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/apps/resolve" {
			var request buildResolutionRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.GitBranchName != "feature" || request.BranchID != "" || request.BranchName != "" {
				t.Fatalf("automatic request became explicit: %+v", request)
			}
			_, _ = w.Write([]byte(`{"version_id":"` + buildTestID + `","branch_id":"` + branchTestID + `","branch_name":"base","branch_source":"default_branch","auto_git_branch_enabled":false,"resolution_status":"resolved","download_url":"https://example.com/artifact"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(BuildVersionDetail{ID: buildTestID})
	}))
	defer server.Close()
	build, err := NewClientWithBaseURL("test", server.URL).ResolveBuildSelection(context.Background(), "app", BuildSelection{GitBranchName: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	if build.BranchName != "base" || build.AutoGitBranchEnabled == nil || *build.AutoGitBranchEnabled || build.GitBranchName != "" || build.BranchSource != BranchSourceDefaultBranch {
		t.Fatalf("disabled policy lost: %+v", build)
	}
}
