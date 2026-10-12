package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/spf13/cobra"
)

func TestAtlasExactVersionUsesOneFilteredRequest(t *testing.T) {
	const branchID = "22222222-2222-4222-8222-222222222222"
	const expectedBuildID = "33333333-3333-4333-8333-333333333333"
	const version = "1.0+release/a&b"
	for _, test := range []struct {
		name, branch string
		missing      bool
	}{
		{name: "older exact match across branches"},
		{name: "older exact match within branch", branch: branchID},
		{name: "package and partial version collisions", missing: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			builds := []api.BuildVersion{
				{ID: "newer-package-match", Version: "2.0", PackageName: version},
				{ID: "newer-partial-version", Version: version + "-preview"},
			}
			for index := 0; index < 100; index++ {
				builds = append(builds, api.BuildVersion{ID: fmt.Sprintf("newer-build-%d", index), Version: "2.0"})
			}
			if !test.missing {
				builds = append(builds, api.BuildVersion{ID: expectedBuildID, Version: version}, api.BuildVersion{ID: "older-exact-match", Version: version})
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				query := r.URL.Query()
				if r.URL.Path != "/api/v1/apps/app/builds" || query.Get("exact_version") != version || query.Get("branch_id") != test.branch || query.Get("page") != "1" || query.Get("page_size") != "1" || query.Has("search") || query.Has("include_jobs") {
					t.Errorf("unexpected version lookup: %s", r.URL)
					http.Error(w, "expected one exact-version lookup", http.StatusBadRequest)
					return
				}
				matches := []api.BuildVersion{}
				for _, build := range builds {
					if build.Version == query.Get("exact_version") {
						matches = append(matches, build)
					}
				}
				total := len(matches)
				if total > 1 {
					matches = matches[:1]
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(api.BuildVersionsPage{Items: matches, Total: total, HasNext: total > 1})
			}))
			defer server.Close()
			cmd := &cobra.Command{Use: "scope"}
			cmd.SetContext(context.Background())
			addAtlasBranchFlags(cmd)
			previous := atlasBuild
			t.Cleanup(func() { atlasBuild = previous })
			cmd.Flags().StringVar(&atlasBuild, "build", "all", "")
			if err := cmd.Flags().Set("build", version); err != nil {
				t.Fatal(err)
			}
			if test.branch != "" {
				if err := cmd.Flags().Set("branch-id", test.branch); err != nil {
					t.Fatal(err)
				}
			}
			branch, build, err := resolveAtlasScope(cmd, api.NewClientWithBaseURL("test", server.URL), &api.App{ID: "app", DefaultBranchID: branchID})
			if calls != 1 {
				t.Fatalf("version lookup issued %d requests", calls)
			}
			if test.missing {
				if err == nil || !strings.Contains(err.Error(), "build version not found") || build != "" {
					t.Fatalf("missing exact version resolved as %q: %v", build, err)
				}
			} else if err != nil || branch != test.branch || build != expectedBuildID {
				t.Fatalf("scope = %q/%q, err = %v", branch, build, err)
			}
		})
	}
}

func TestAtlasExactVersionRejectsUnfilteredResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.BuildVersionsPage{Items: []api.BuildVersion{{ID: "wrong-build", Version: "1.0-preview"}}})
	}))
	defer server.Close()
	cmd := &cobra.Command{Use: "scope"}
	cmd.SetContext(context.Background())
	addAtlasBranchFlags(cmd)
	previous := atlasBuild
	t.Cleanup(func() { atlasBuild = previous })
	cmd.Flags().StringVar(&atlasBuild, "build", "all", "")
	if err := cmd.Flags().Set("build", "1.0"); err != nil {
		t.Fatal(err)
	}
	_, build, err := resolveAtlasScope(cmd, api.NewClientWithBaseURL("test", server.URL), &api.App{ID: "app"})
	if err == nil || build != "" {
		t.Fatalf("unfiltered response resolved as %q: %v", build, err)
	}
}

func TestAtlasExactBuildIDNeedsNoVersionLookup(t *testing.T) {
	const buildID = "33333333-3333-4333-8333-333333333333"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected lookup for exact build ID: %s", r.URL)
		http.Error(w, "unexpected request", http.StatusBadRequest)
	}))
	defer server.Close()
	cmd := &cobra.Command{Use: "scope"}
	cmd.SetContext(context.Background())
	addAtlasBranchFlags(cmd)
	previous := atlasBuild
	t.Cleanup(func() { atlasBuild = previous })
	cmd.Flags().StringVar(&atlasBuild, "build", "all", "")
	if err := cmd.Flags().Set("build", buildID); err != nil {
		t.Fatal(err)
	}
	branch, build, err := resolveAtlasScope(cmd, api.NewClientWithBaseURL("test", server.URL), &api.App{ID: "app"})
	if err != nil || branch != "" || build != buildID {
		t.Fatalf("scope = %q/%q, err = %v", branch, build, err)
	}
}
