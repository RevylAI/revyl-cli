package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/spf13/cobra"
)

func TestAtlasRuntimeBranchScope(t *testing.T) {
	const appID = "00000000-0000-4000-8000-000000000001"
	const branchID = "00000000-0000-4000-8000-000000000002"
	const buildID = "00000000-0000-4000-8000-000000000003"
	previousBuild := atlasBuild
	t.Cleanup(func() { atlasBuild = previousBuild })
	for _, tc := range []struct {
		name          string
		args          []string
		branch, build string
		wantError     bool
	}{
		{name: "branch aggregate", args: []string{"--branch-id=" + branchID}, branch: branchID},
		{name: "branch exact build", args: []string{"--branch-id=" + branchID, "--build=" + buildID}, branch: branchID, build: buildID},
		{name: "explicit all", args: []string{"--build=all"}},
		{name: "invalid branch", args: []string{"--branch-id=feature"}, wantError: true},
		{name: "conflicting all", args: []string{"--branch-id=" + branchID, "--build=all"}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/atlas/v2/apps/"+appID+"/graph" {
					t.Errorf("unexpected lookup: %s %s", r.Method, r.URL.Path)
				}
				if r.URL.Query().Get("branch_id") != tc.branch || r.URL.Query().Get("build_id") != tc.build {
					t.Errorf("unexpected evidence scope: %s", r.URL.RawQuery)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"nodes":[],"edges":[]}`))
			}))
			defer server.Close()
			client := api.NewClientWithBaseURL("fixture", server.URL)
			cmd := &cobra.Command{Use: "graph"}
			cmd.SetContext(context.Background())
			cmd.Flags().String("branch-id", "", "")
			cmd.Flags().StringVar(&atlasBuild, "build", "all", "")
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatal(err)
			}
			branch, build, err := resolveAtlasScope(cmd, client, &api.App{ID: appID})
			if (err != nil) != tc.wantError {
				t.Fatalf("resolve error = %v, want error %v", err, tc.wantError)
			}
			if requests != 0 {
				t.Fatal("branch scope performed an unnecessary app/branch/build lookup")
			}
			if err != nil {
				return
			}
			if branch != tc.branch || build != tc.build {
				t.Fatalf("resolved %q/%q, want %q/%q", branch, build, tc.branch, tc.build)
			}
			if _, err := client.GetAtlasGraph(cmd.Context(), api.AtlasQuery{AppID: appID, BranchID: branch, BuildID: build}); err != nil {
				t.Fatal(err)
			}
			if requests != 1 {
				t.Fatalf("got %d requests, want 1", requests)
			}
		})
	}
}
