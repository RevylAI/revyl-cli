package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestAtlasGeneratedFollowupsKeepScopeInAnotherGitCheckout(t *testing.T) {
	withWorkingDir(t, executionSourceCheckout(t))
	atlasGraphCmd.Flags().VisitAll(func(flag *pflag.Flag) {
		value := flag.Value.String()
		t.Cleanup(func() { _ = flag.Value.Set(value) })
	})
	const appID = "11111111-1111-4111-8111-111111111111"
	const branchID = "22222222-2222-4222-8222-222222222222"
	const buildID = "33333333-3333-4333-8333-333333333333"
	for _, scope := range []struct {
		name, branch, build string
		recentBuildLimit    int
	}{
		{"branch", branchID, "", 0},
		{"branch exact build", branchID, buildID, 0},
		{"exact build", "", buildID, 0},
		{"all branches", "", "", 0},
		{"branch recent builds", branchID, "", 1},
		{"all branches recent builds", "", "", 1},
	} {
		t.Run(scope.name, func(t *testing.T) {
			graph := atlasContractFixture()
			graph["app_id"] = appID
			graph["branch_id"] = scope.branch
			atlasMap(graph["projection"])["requested_build_id"] = scope.build
			atlasMap(graph["projection"])["recent_build_limit"] = scope.recentBuildLimit
			app := &api.App{ID: appID, DefaultBranchID: "44444444-4444-4444-8444-444444444444"}
			nodes := atlasMaps(graph["nodes"])
			edge, _ := buildAtlasEdge(app, graph, nodes[0], nodes[1])
			observation := map[string]interface{}{"observation_id": "observation-1", "report_id": "report-1"}
			query := api.AtlasQuery{AppID: appID, BranchID: scope.branch, BuildID: scope.build, RecentBuildLimit: scope.recentBuildLimit}
			producers := map[string][]string{
				"brief":     atlasStringSlice(buildAtlasBrief(app, graph)["next_actions"]),
				"graph":     atlasStringSlice(buildAtlasKnowledgeGraph(app, graph)["next_actions"]),
				"area":      atlasStringSlice(buildAtlasAreaGraph(app, graph, "Home")["next_actions"]),
				"screen":    atlasStringSlice(buildAtlasScreen(app, graph, nodes[0])["next_actions"]),
				"neighbors": atlasStringSlice(buildAtlasNeighbors(app, graph, nodes[0], "both")["next_actions"]),
				"search":    atlasStringSlice(buildAtlasSearch(app, graph, "expense", 10)["next_actions"]),
				"edge":      atlasStringSlice(edge["next_actions"]),
				"report":    atlasStringSlice(buildAtlasReportContract(app, graph, "home", "screen", nodes[0], observation, map[string]interface{}{})["next_actions"]),
				"observations": atlasScopedNextActionsForQuery(query, []string{
					"revyl atlas observation observation-1 --app " + appID,
				}),
				"observation": atlasScopedNextActionsForQuery(query, []string{
					"revyl atlas report observation-1 --app " + appID + " --json",
				}),
				"edge evidence": atlasScopedNextActions(graph, atlasEdgeReportNextActions(appID, []map[string]interface{}{
					{"runs": api.AtlasResponse{"runs": []interface{}{map[string]interface{}{"report_id": "report-1"}}}},
				})),
			}
			for producer, actions := range producers {
				t.Run(producer, func(t *testing.T) {
					if len(actions) == 0 {
						t.Fatal("fixture generated no follow-up actions")
					}
					for _, action := range actions {
						if !strings.HasPrefix(action, "revyl atlas ") {
							continue
						}
						wantRecentBuildLimit := ""
						if scope.recentBuildLimit > 0 && !strings.HasPrefix(action, "revyl atlas observation ") && !strings.HasPrefix(action, "revyl atlas report ") {
							wantRecentBuildLimit = strconv.Itoa(scope.recentBuildLimit)
						}
						requests := 0
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							w.Header().Set("Content-Type", "application/json")
							if r.URL.Path == "/api/v1/apps/"+appID {
								_ = json.NewEncoder(w).Encode(app)
								return
							}
							if r.URL.Path != "/api/v1/atlas/v2/apps/"+appID+"/graph" {
								t.Errorf("follow-up unexpectedly resolves Git or default scope: %s", r.URL)
								http.NotFound(w, r)
								return
							}
							requests++
							if r.URL.Query().Get("branch_id") != scope.branch || r.URL.Query().Get("build_id") != scope.build {
								t.Errorf("follow-up changed scope: %s", r.URL)
							}
							if r.URL.Query().Get("recent_build_limit") != wantRecentBuildLimit {
								t.Errorf("follow-up changed recent-build scope: %s", r.URL)
							}
							_ = json.NewEncoder(w).Encode(graph)
						}))
						client := api.NewClientWithBaseURL("test", server.URL)
						parser := csv.NewReader(strings.NewReader(action))
						parser.Comma = ' '
						args, err := parser.Read()
						if err != nil {
							t.Fatal(err)
						}
						cmd := &cobra.Command{Use: args[2], SilenceUsage: true, SilenceErrors: true, RunE: func(cmd *cobra.Command, _ []string) error {
							query, _, err := atlasQueryFor(cmd, client)
							if err != nil {
								return err
							}
							_, err = client.GetAtlasGraph(cmd.Context(), query)
							return err
						}}
						cmd.SetContext(context.Background())
						addAtlasScopeFlags(cmd, true)
						addAtlasOutputFlags(cmd)
						cmd.Flags().Bool("runs", false, "")
						cmd.SetArgs(args[3:])
						err = cmd.Execute()
						server.Close()
						if err != nil || requests != 1 {
							t.Fatalf("follow-up %q made %d graph requests: %v", action, requests, err)
						}
					}
				})
			}
		})
	}
}
