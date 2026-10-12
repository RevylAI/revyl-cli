package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestChatBuildListingPreservesAllScopeAndPagination(t *testing.T) {
	const appID = "11111111-1111-4111-8111-111111111111"
	for _, branch := range []string{"", "release"} {
		t.Run("branch="+branch, func(t *testing.T) {
			tool, err := findInvestigationTool(rootCmd, "builds.list")
			if err != nil {
				t.Fatal(err)
			}
			input := map[string]any{"app": appID, "page": 2, "limit": 7}
			if branch != "" {
				input["branch"] = branch
			}
			args, err := tool.Arguments(input)
			if err != nil {
				t.Fatal(err)
			}
			const releaseBranchID = "22222222-2222-4222-8222-222222222222"
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/apps/"+appID+"/branches" && r.URL.Query().Get("name") == branch && branch != "" {
					fmt.Fprintf(w, `{"branches":[{"id":%q,"app_id":%q,"name":%q,"is_default":false}]}`, releaseBranchID, appID, branch)
					return
				}
				wantBranchID := ""
				if branch != "" {
					wantBranchID = releaseBranchID
				}
				if r.URL.Path != "/api/v1/apps/"+appID+"/builds" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("page_size") != "7" || r.URL.Query().Get("branch_id") != wantBranchID || r.URL.Query().Has("branch_name") {
					t.Errorf("unexpected lookup: %s", r.URL)
				}
				fmt.Fprint(w, `{"items":[],"total":20,"page":2,"page_size":7,"has_next":true}`)
			}))
			defer server.Close()
			cmd := newLeafCommand("list", nil)
			cmd.Flags().String("app", "", "")
			cmd.Flags().String("branch", "", "")
			cmd.Flags().Bool("all-branches", false, "")
			cmd.Flags().Int("page", 1, "")
			cmd.Flags().Int("limit", 20, "")
			cmd.Flags().Int("page-size", 20, "")
			if err := cmd.ParseFlags(args[2:]); err != nil {
				t.Fatal(err)
			}
			output := captureStdoutAndStderr(t, func() {
				if err := listBuildVersions(cmd, api.NewClientWithBaseURL("fixture", server.URL), appID); err != nil {
					t.Fatal(err)
				}
			})
			result := parseJSON(t, output)
			wantCalls := 1
			if branch != "" {
				wantCalls = 2
			}
			if calls != wantCalls || result["page"] != float64(2) || result["page_size"] != float64(7) || result["has_next"] != true || result["scanned_count"] != float64(0) {
				t.Fatalf("lost paginated contract: %v, calls=%d", result, calls)
			}
		})
	}
}
