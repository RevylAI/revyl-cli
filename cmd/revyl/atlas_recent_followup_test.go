package main

import (
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

func TestAtlasRecentBuildGraphFollowupKeepsOlderEvidenceExcluded(t *testing.T) {
	atlasGraphCmd.Flags().VisitAll(func(flag *pflag.Flag) {
		value := flag.Value.String()
		t.Cleanup(func() { _ = flag.Value.Set(value) })
	})
	const appID = "11111111-1111-4111-8111-111111111111"
	const branchID = "22222222-2222-4222-8222-222222222222"
	graphRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/apps/"+appID {
			_ = json.NewEncoder(w).Encode(api.App{ID: appID, DefaultBranchID: branchID})
			return
		}
		if r.URL.Path != "/api/v1/atlas/v2/apps/"+appID+"/graph" {
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
			return
		}
		graphRequests++
		if r.URL.Query().Get("branch_id") != branchID {
			t.Errorf("changed branch: %s", r.URL)
		}
		graph := atlasContractFixture()
		graph["app_id"], graph["branch_id"] = appID, branchID
		if r.URL.Query().Get("recent_build_limit") == "1" {
			graph["nodes"], graph["edges"] = []interface{}{}, []interface{}{}
			atlasMap(graph["projection"])["recent_build_limit"] = 1
		}
		_ = json.NewEncoder(w).Encode(graph)
	}))
	defer server.Close()
	client := api.NewClientWithBaseURL("test", server.URL)
	execute := func(name string, args []string) map[string]interface{} {
		t.Helper()
		var result map[string]interface{}
		cmd := &cobra.Command{Use: name, SilenceUsage: true, SilenceErrors: true, RunE: func(cmd *cobra.Command, _ []string) error {
			_, app, graph, err := fetchAtlasAgentGraph(cmd, client)
			if err != nil {
				return err
			}
			if name == "brief" {
				result = buildAtlasBrief(app, graph)
			} else {
				result = buildAtlasKnowledgeGraph(app, graph)
			}
			return nil
		}}
		addAtlasScopeFlags(cmd, true)
		addAtlasOutputFlags(cmd)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	graph := execute("graph", []string{"--app", appID, "--branch-id", branchID, "--recent-build-limit", "1"})
	if atlasInt(atlasMap(graph["returned"])["nodes"]) != 0 {
		t.Fatalf("recent graph included older evidence: %#v", graph)
	}
	action := atlasStringSlice(graph["next_actions"])[0]
	parser := csv.NewReader(strings.NewReader(action))
	parser.Comma = ' '
	args, err := parser.Read()
	if err != nil {
		t.Fatal(err)
	}
	if args[2] != "brief" {
		t.Fatalf("expected brief follow-up: %q", action)
	}
	brief := execute(args[2], args[3:])
	if graphRequests != 2 || atlasInt(atlasMap(brief["stats"])["nodes"]) != 0 || atlasInt(atlasMap(brief["projection"])["recent_build_limit"]) != 1 {
		t.Fatalf("follow-up %q restored older evidence: requests=%d brief=%#v", action, graphRequests, brief)
	}
}

func TestAtlasRecentBuildFollowupsRespectSupportedCommands(t *testing.T) {
	for _, limit := range []int{0, 1} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			query := api.AtlasQuery{RecentBuildLimit: limit}
			for _, cmd := range atlasCmd.Commands() {
				action := "revyl atlas " + cmd.Name() + " --app app-1"
				followup := atlasScopedNextActionsForQuery(query, []string{action})[0]
				wantLimit := limit > 0 && cmd.Flags().Lookup("recent-build-limit") != nil
				if strings.Contains(followup, "--recent-build-limit 1") != wantLimit {
					t.Errorf("unexpected recent-build scope: %s", followup)
				}
			}
			if projection := atlasEvidenceProjection(query); (projection["recent_build_limit"] != nil) != (limit > 0) {
				t.Errorf("unexpected query projection: %#v", projection)
			}
			graph := api.AtlasResponse{"projection": atlasEvidenceProjection(query)}
			if projection := atlasProjectionContract(graph); (projection["recent_build_limit"] != nil) != (limit > 0) {
				t.Errorf("unexpected graph projection: %#v", projection)
			}
		})
	}
}
