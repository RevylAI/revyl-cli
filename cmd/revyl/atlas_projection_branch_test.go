package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestAtlasProjectionAndNextActionsPreserveBranch(t *testing.T) {
	const branchID = "22222222-2222-4222-8222-222222222222"
	const buildID = "33333333-3333-4333-8333-333333333333"
	for _, tc := range []struct{ name, branch, build string }{
		{"branch aggregate", branchID, ""},
		{"branch exact build", branchID, buildID},
		{"exact build", "", buildID},
		{"all branches", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := api.AtlasQuery{BranchID: tc.branch, BuildID: tc.build}
			graph := atlasContractFixture()
			graph["branch_id"] = tc.branch
			atlasMap(graph["projection"])["requested_build_id"] = tc.build
			brief := buildAtlasBrief(&api.App{ID: "app-1"}, graph)
			area := buildAtlasAreaGraph(&api.App{ID: "app-1"}, graph, atlasString(atlasMaps(graph["nodes"])[0], "product_area"))
			for _, projection := range []map[string]interface{}{atlasMap(brief["projection"]), atlasEvidenceProjection(query)} {
				if atlasString(projection, "branch_id") != tc.branch || atlasString(projection, "requested_build_id") != tc.build {
					t.Fatalf("lost selection: %#v", projection)
				}
			}
			actions := append(atlasStringSlice(brief["next_actions"]), atlasScopedNextActionsForQuery(query, []string{"revyl atlas observation observation-1 --app app-1", "revyl test report execution-1"})...)
			actions = append(atlasStringSlice(area["next_actions"]), actions...)
			for _, action := range actions {
				if !strings.HasPrefix(action, "revyl atlas ") {
					continue
				}
				if strings.Contains(action, "--branch-id "+strconv.Quote(tc.branch)) != (tc.branch != "") {
					t.Fatalf("branch scope lost: %s", action)
				}
				if strings.Contains(action, "--build "+strconv.Quote(tc.build)) != (tc.build != "") {
					t.Fatalf("build scope lost: %s", action)
				}
				if strings.Contains(action, "--all-branches") != (tc.branch == "" && tc.build == "") {
					t.Fatalf("All scope changed: %s", action)
				}
			}
			if actions[len(actions)-1] != "revyl test report execution-1" {
				t.Fatal("non-Atlas follow-up changed")
			}
		})
	}
}
