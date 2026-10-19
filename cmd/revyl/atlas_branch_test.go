package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/spf13/cobra"
)

func TestAtlasHelpAdvertisesAllBranchesWithoutSelectingBuildAll(t *testing.T) {
	for _, cmd := range []*cobra.Command{atlasOpenCmd, atlasBriefCmd, atlasObservationsCmd} {
		flag := cmd.Flags().Lookup("build")
		if flag.DefValue != "" || strings.Contains(cmd.Flags().FlagUsages(), `(default "all")`) {
			t.Fatalf("%s --build should leave the branch scope unchanged by default", cmd.CommandPath())
		}
		if !strings.Contains(cmd.Flags().Lookup("branch").Usage, "default to all branches") {
			t.Fatalf("%s does not advertise the default branch scope", cmd.CommandPath())
		}
	}
}

func TestAtlasBranchScopeDefaultsAndExplicitAll(t *testing.T) {
	t.Chdir(t.TempDir())
	const branchID = "22222222-2222-4222-8222-222222222222"
	for _, test := range []struct {
		name, branch, build string
		all                 bool
		wantBranch          string
		wantCalls           int
	}{
		{name: "default"},
		{name: "named all is literal", branch: "all", wantBranch: branchID, wantCalls: 1},
		{name: "all branches", all: true},
		{name: "legacy explicit build all", build: "all"},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/app":
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "app", "default_branch_id": branchID})
				case "/api/v1/apps/app/branches/" + branchID:
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": branchID, "name": "default"})
				case "/api/v1/apps/app/branches":
					if r.URL.Query().Get("name") != "all" {
						t.Error("branch name changed")
					}
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"branches": []map[string]interface{}{{"id": branchID, "name": "all"}}})
				default:
					t.Errorf("unexpected path %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := api.NewClientWithBaseURL("test", server.URL)
			cmd := &cobra.Command{Use: "scope"}
			cmd.SetContext(context.Background())
			addAtlasBranchFlags(cmd)
			previous := atlasBuild
			t.Cleanup(func() { atlasBuild = previous })
			cmd.Flags().StringVar(&atlasBuild, "build", "", "")
			if test.branch != "" {
				_ = cmd.Flags().Set("branch", test.branch)
			}
			if test.all {
				_ = cmd.Flags().Set("all-branches", "true")
			}
			if test.build != "" {
				_ = cmd.Flags().Set("build", test.build)
			}
			branch, build, err := resolveAtlasScope(cmd, client, &api.App{ID: "app", DefaultBranchID: branchID})
			if err != nil {
				t.Fatal(err)
			}
			if branch != test.wantBranch || build != "" || calls != test.wantCalls {
				t.Fatalf("scope %q/%q with %d calls", branch, build, calls)
			}
		})
	}
}

func TestAtlasExactVersionDerivesItsBranch(t *testing.T) {
	const expectedBuildID = "33333333-3333-4333-8333-333333333333"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/apps/app/builds" {
			t.Errorf("unexpected path %s", r.URL)
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Has("branch_id") {
			t.Error("exact version should not be constrained by app default")
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"items": []map[string]interface{}{{"id": expectedBuildID, "version": "1.0"}}})
	}))
	defer server.Close()
	cmd := &cobra.Command{Use: "scope"}
	cmd.SetContext(context.Background())
	addAtlasBranchFlags(cmd)
	cmd.Flags().String("build", "all", "")
	_ = cmd.Flags().Set("build", "1.0")
	previous := atlasBuild
	atlasBuild = "1.0"
	t.Cleanup(func() { atlasBuild = previous })
	branch, build, err := resolveAtlasScope(cmd, api.NewClientWithBaseURL("test", server.URL), &api.App{ID: "app", DefaultBranchID: "22222222-2222-4222-8222-222222222222"})
	if err != nil {
		t.Fatal(err)
	}
	if branch != "" || build != expectedBuildID {
		t.Fatalf("wrong scope %q/%q", branch, build)
	}
}

func TestAtlasExplicitLatestKeepsOneBranchAndBuild(t *testing.T) {
	withWorkingDir(t, executionSourceCheckout(t))
	const branchID = "22222222-2222-4222-8222-222222222222"
	const buildID = "33333333-3333-4333-8333-333333333333"
	for _, branchName := range []string{"", "release"} {
		t.Run("branch="+branchName, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/app/branches/" + branchID:
					if branchName != "" {
						t.Error("named scope looked up the app default branch")
					}
					_ = json.NewEncoder(w).Encode(api.BuildBranch{ID: branchID, Name: "base"})
				case "/api/v1/apps/app/branches":
					if r.URL.Query().Get("name") != branchName || branchName == "" {
						t.Errorf("unexpected branch lookup: %s", r.URL)
					}
					_ = json.NewEncoder(w).Encode(api.BuildBranchesPage{Branches: []api.BuildBranch{{ID: branchID, Name: branchName}}})
				case "/api/v1/apps/resolve":
					var body map[string]interface{}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if body["build_selection"] != "latest" || (body["branch_id"] != branchID && (branchName != "" || body["git_branch_name"] != "Feature/git-branch")) {
						t.Errorf("latest selection changed: %v", body)
					}
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"version_id": buildID, "branch_id": branchID, "resolution_status": "resolved", "download_url": "https://example.com/app.apk"})
				case "/api/v1/apps/builds/" + buildID:
					_ = json.NewEncoder(w).Encode(api.BuildVersionDetail{ID: buildID})
				default:
					t.Errorf("unexpected lookup: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			cmd := &cobra.Command{Use: "scope"}
			cmd.SetContext(context.Background())
			addAtlasBranchFlags(cmd)
			previous := atlasBuild
			t.Cleanup(func() { atlasBuild = previous })
			cmd.Flags().StringVar(&atlasBuild, "build", "", "")
			_ = cmd.Flags().Set("build", "latest")
			_ = cmd.Flags().Set("branch", branchName)
			branch, build, err := resolveAtlasScope(cmd, api.NewClientWithBaseURL("test", server.URL), &api.App{ID: "app", DefaultBranchID: branchID})
			wantRequests := 3
			if branchName == "" {
				wantRequests = 4
			}
			if err != nil || branch != branchID || build != buildID || requests != wantRequests {
				t.Fatalf("latest scope = %q/%q, requests=%d, err=%v", branch, build, requests, err)
			}
		})
	}
}

func TestAtlasFlaglessReadsIgnoreGitContext(t *testing.T) {
	withWorkingDir(t, executionSourceCheckout(t))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("flagless browsing resolved a branch: %s", r.URL)
		http.NotFound(w, r)
	}))
	defer server.Close()
	cmd := &cobra.Command{Use: "scope"}
	cmd.SetContext(context.Background())
	addAtlasBranchFlags(cmd)
	cmd.Flags().String("build", "", "")
	branch, build, err := resolveAtlasScope(cmd, api.NewClientWithBaseURL("test", server.URL), &api.App{ID: "app"})
	if err != nil || branch != "" || build != "" {
		t.Fatalf("scope=%q/%q, %v", branch, build, err)
	}
}
