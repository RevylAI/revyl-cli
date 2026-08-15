package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestMaterializeAtlasScreenshotsTraversesTypedScreenSlices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "image/png")
		_, _ = response.Write([]byte("screenshot"))
	}))
	defer server.Close()

	previousDir := atlasScreenshotDir
	atlasScreenshotDir = t.TempDir()
	t.Cleanup(func() { atlasScreenshotDir = previousDir })

	screen := map[string]interface{}{"screenshot_url": server.URL + "/screen.png"}
	result := map[string]interface{}{"key_screens": []map[string]interface{}{screen}}
	if err := materializeAtlasScreenshots(result); err != nil {
		t.Fatal(err)
	}
	path := atlasString(screen, "local_screenshot_path")
	if path == "" {
		t.Fatal("expected local screenshot path")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected downloaded screenshot: %v", err)
	}
}

func atlasContractFixture() api.AtlasResponse {
	return api.AtlasResponse{
		"app_id": "app-1",
		"projection": map[string]interface{}{
			"data_source":              "summary",
			"summary_contract_version": float64(3),
			"truncated":                false,
		},
		"stats": map[string]interface{}{
			"nodes":        float64(2),
			"edges":        float64(1),
			"observations": float64(13),
			"product_areas": map[string]interface{}{
				"Home":     float64(1),
				"Creation": float64(1),
			},
		},
		"curation": map[string]interface{}{
			"pinned_entry_root_entity_ids": []interface{}{"home"},
		},
		"nodes": []interface{}{
			map[string]interface{}{
				"id":                            "home",
				"semantic_name":                 "home_dashboard",
				"semantic_description":          "Review spending and create expenses.",
				"product_area":                  "Home",
				"screen_kind":                   "home",
				"observation_count":             float64(8),
				"representative_observation_id": "obs-home",
				"is_entry_point":                true,
				"is_hub":                        true,
				"primary_actions":               []interface{}{"create expense"},
			},
			map[string]interface{}{
				"id":                   "create",
				"semantic_name":        "expense_create",
				"semantic_description": "Enter and save a new expense.",
				"product_area":         "Creation",
				"screen_kind":          "form",
				"observation_count":    float64(5),
				"primary_actions":      []interface{}{"save expense"},
				"primary_objects":      []interface{}{"expense"},
			},
		},
		"edges": []interface{}{
			map[string]interface{}{
				"source_entity_id":  "home",
				"target_entity_id":  "create",
				"action_type":       "tap",
				"observation_count": float64(4),
				"test_support":      float64(1),
			},
		},
	}
}

func TestBuildAtlasBriefIsSummaryBackedAndBounded(t *testing.T) {
	result := buildAtlasBrief(&api.App{ID: "app-1", Name: "Finance", Platform: "iOS"}, atlasContractFixture())

	if result["contract"] != "atlas_brief.v2" {
		t.Fatalf("contract = %v", result["contract"])
	}
	projection := atlasMap(result["projection"])
	if projection["data_source"] != "summary" {
		t.Fatalf("data source = %v", projection["data_source"])
	}
	if len(projection) != 1 {
		t.Fatalf("projection leaked implementation details: %#v", projection)
	}
	for _, internalKey := range []string{"summary_contract_version", "truncated"} {
		if _, exists := projection[internalKey]; exists {
			t.Fatalf("projection exposed internal key %q", internalKey)
		}
	}
	anchors := atlasMaps(result["starting_anchors"])
	if len(anchors) != 1 || atlasString(anchors[0], "id") != "home" {
		t.Fatalf("unexpected starting anchors: %#v", anchors)
	}
	if reasons := strings.Join(atlasStringSlice(anchors[0]["reasons"]), ","); reasons != "curated_entry,semantic_entry,observed_root" {
		t.Fatalf("unexpected anchor reasons: %q", reasons)
	}
	visualSample := atlasMaps(result["visual_sample"])
	if len(visualSample) != 2 || atlasString(visualSample[0], "product_area") == atlasString(visualSample[1], "product_area") {
		t.Fatalf("unexpected visual sample: %#v", visualSample)
	}
	for _, removedKey := range []string{"signals", "top_flows", "capabilities", "entry_screens", "hub_screens"} {
		if _, exists := result[removedKey]; exists {
			t.Fatalf("brief exposed opinionated key %q", removedKey)
		}
	}
	for _, action := range atlasStringSlice(result["next_actions"]) {
		if strings.Contains(action, " atlas audit ") {
			t.Fatalf("brief recommended removed audit command: %q", action)
		}
	}
}

func TestAtlasNamedListSummaryUsesAgentScreenContract(t *testing.T) {
	screen := atlasAgentScreen(atlasMaps(atlasContractFixture()["nodes"])[0])
	if summary := atlasNamedListSummary(screen); summary != "home_dashboard  obs=8" {
		t.Fatalf("screen summary = %q", summary)
	}
}

func TestAtlasCommandDoesNotExposeRemovedOpinionatedCommands(t *testing.T) {
	foundReport := false
	for _, command := range atlasCmd.Commands() {
		if command.Name() == "audit" || command.Name() == "flows" {
			t.Fatalf("removed Atlas command %q is still registered", command.Name())
		}
		if command.Name() == "report" {
			foundReport = true
		}
	}
	if !foundReport {
		t.Fatal("Atlas report command is not registered")
	}
}

func TestAtlasIndexAppContractHidesBackendIndexDetails(t *testing.T) {
	app, ready := atlasIndexAppContract(map[string]interface{}{
		"app_id":   "app-1",
		"app_name": "Finance",
		"platform": "iOS",
		"olap": map[string]interface{}{
			"ready":            true,
			"contract_version": float64(3),
		},
	}, false)
	if !ready || !atlasBool(app["atlas_ready"]) {
		t.Fatalf("unexpected readiness contract: %#v", app)
	}
	if _, exists := app["olap"]; exists {
		t.Fatalf("app contract exposed backend index details: %#v", app)
	}
}

func TestAtlasIndexReadinessSupportRejectsLegacyResponses(t *testing.T) {
	legacy := api.AtlasResponse{
		"apps":  []interface{}{map[string]interface{}{"app_id": "app-1"}},
		"total": float64(1),
	}
	if atlasIndexSupportsReadiness(legacy) {
		t.Fatal("legacy Atlas index response reported readiness support")
	}
	current := api.AtlasResponse{
		"apps": []interface{}{map[string]interface{}{
			"app_id": "app-1",
			"olap":   map[string]interface{}{"ready": true},
		}},
		"total": float64(1),
	}
	if !atlasIndexSupportsReadiness(current) {
		t.Fatal("current Atlas index response did not report readiness support")
	}
}

func TestBuildAtlasKnowledgeGraphIsFlatAndComplete(t *testing.T) {
	result := buildAtlasKnowledgeGraph(&api.App{ID: "app-1"}, atlasContractFixture())
	if result["contract"] != "atlas_graph.v1" || len(atlasMaps(result["nodes"])) != 2 || len(atlasMaps(result["edges"])) != 1 {
		t.Fatalf("unexpected graph contract: %#v", result)
	}
	for _, removedKey := range []string{"structure", "flows", "roots", "spine", "algorithm"} {
		if _, exists := result[removedKey]; exists {
			t.Fatalf("graph exposed opinionated key %q", removedKey)
		}
	}
	edge := atlasMaps(result["edges"])[0]
	if atlasString(edge, "edge_key") != "home->create|observed_transition|tap|" {
		t.Fatalf("unexpected stable edge key: %#v", edge)
	}
}

func TestBuildAtlasKnowledgeGraphExposesTruncationOutsideProjection(t *testing.T) {
	previousLimit := atlasLimit
	atlasLimit = 1
	t.Cleanup(func() { atlasLimit = previousLimit })

	result := buildAtlasKnowledgeGraph(&api.App{ID: "app-1"}, atlasContractFixture())
	if !atlasBool(result["truncated"]) || !atlasBool(result["has_more"]) {
		t.Fatalf("graph did not expose local truncation: %#v", result)
	}
	projection := atlasMap(result["projection"])
	if len(projection) != 1 || projection["data_source"] != "summary" {
		t.Fatalf("projection leaked completeness details: %#v", projection)
	}
}

func TestBuildAtlasNeighborsUsesSummaryGraphEdges(t *testing.T) {
	graph := atlasContractFixture()
	home, err := resolveAtlasNode(graph, "home_dashboard")
	if err != nil {
		t.Fatal(err)
	}
	result := buildAtlasNeighbors(&api.App{ID: "app-1"}, graph, home, "both")
	if result["incoming_count"] != 0 || result["outgoing_count"] != 1 {
		t.Fatalf("unexpected neighbor counts: %#v", result)
	}
	edge := atlasMaps(result["outgoing"])[0]
	if !atlasBool(edge["has_test_support"]) || atlasInt(edge["observations"]) != 4 {
		t.Fatalf("unexpected edge: %#v", edge)
	}
}

func TestBuildAtlasScreenIncludesRelationshipCounts(t *testing.T) {
	graph := atlasContractFixture()
	home, err := resolveAtlasNode(graph, "home_dashboard")
	if err != nil {
		t.Fatal(err)
	}
	result := buildAtlasScreen(&api.App{ID: "app-1"}, graph, home)
	if atlasInt(result["incoming_count"]) != 0 || atlasInt(result["outgoing_count"]) != 1 {
		t.Fatalf("unexpected relationship counts: %#v", result)
	}
	if _, exists := result["flows"]; exists {
		t.Fatalf("screen exposed derived flows: %#v", result)
	}
}

func TestBuildAtlasEdgeIncludesTransitionCount(t *testing.T) {
	graph := atlasContractFixture()
	source, err := resolveAtlasNode(graph, "home_dashboard")
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolveAtlasNode(graph, "expense_create")
	if err != nil {
		t.Fatal(err)
	}
	result, _ := buildAtlasEdge(&api.App{ID: "app-1"}, graph, source, target)
	if atlasInt(result["transition_count"]) != 1 {
		t.Fatalf("unexpected transition count: %#v", result)
	}
}

func TestAtlasEvidenceProjectionIsExplicit(t *testing.T) {
	includeVariants := false
	projection := atlasEvidenceProjection(api.AtlasQuery{
		BuildID:         "build-1",
		IncludeVariants: &includeVariants,
		Limit:           3,
	})
	if projection["data_source"] != "evidence" || len(projection) != 1 {
		t.Fatalf("unexpected evidence projection: %#v", projection)
	}
}

func TestBuildAtlasReportContractBridgesEvidenceToRunContext(t *testing.T) {
	graph := atlasContractFixture()
	screen := atlasMaps(graph["nodes"])[0]
	observation := map[string]interface{}{
		"observation_id": "obs-home",
		"report_id":      "report-1",
		"execution_id":   "execution-1",
		"session_id":     "session-1",
		"test_id":        "test-1",
		"test_name":      "Create an expense",
		"step_index":     float64(2),
		"action_index":   float64(1),
	}
	report := map[string]interface{}{
		"id":                    "report-1",
		"workflow_execution_id": "workflow-1",
		"test_goal_summary":     "Create and save a reimbursable expense.",
	}

	result := buildAtlasReportContract(
		&api.App{ID: "app-1", Name: "Finance"},
		graph,
		"home_dashboard",
		"screen",
		screen,
		observation,
		report,
	)

	if result["contract"] != "atlas_report.v1" {
		t.Fatalf("contract = %v", result["contract"])
	}
	if atlasString(atlasMap(result["screen"]), "id") != "home" {
		t.Fatalf("screen = %#v", result["screen"])
	}
	provenance := atlasMap(result["provenance"])
	if atlasString(provenance, "workflow_execution_id") != "workflow-1" {
		t.Fatalf("provenance = %#v", provenance)
	}
	actions := strings.Join(atlasStringSlice(result["next_actions"]), "\n")
	for _, expected := range []string{
		"revyl atlas graph --app app-1 --report-id report-1 --json",
		"revyl test report execution-1 --json",
		"revyl workflow report workflow-1 --json",
	} {
		if !strings.Contains(actions, expected) {
			t.Fatalf("next actions %q do not include %q", actions, expected)
		}
	}
}

func TestResolveAtlasReportTargetPreservesAmbiguity(t *testing.T) {
	graph := atlasContractFixture()
	nodes := atlasMaps(graph["nodes"])
	nodes[0]["semantic_name"] = "expense_home"
	graph["nodes"] = append(atlasSlice(graph["nodes"]), map[string]interface{}{
		"id":                            "other",
		"semantic_name":                 "expense_settings",
		"representative_observation_id": "obs-other",
	})

	_, _, _, err := resolveAtlasReportTarget(graph, "expense")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}

	observationID := "00000000-0000-0000-0000-000000000123"
	resolvedID, resolvedAs, _, err := resolveAtlasReportTarget(graph, observationID)
	if err != nil || resolvedID != observationID || resolvedAs != "observation" {
		t.Fatalf("unexpected observation resolution: id=%q as=%q err=%v", resolvedID, resolvedAs, err)
	}
}

func TestAtlasUserFacingReportRemovesInternalStorageKeys(t *testing.T) {
	report, err := atlasUserFacingReport(json.RawMessage(`{
		"id": "report-1",
		"test_name": "Create an expense",
		"video_s3_key": "private/report.mp4",
		"steps": [{"step_description": "Open the form", "screenshot_after_s3_key": "private/screen.png"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := report["video_s3_key"]; exists {
		t.Fatalf("report exposed internal storage key: %#v", report)
	}
	steps := atlasMaps(report["steps"])
	if len(steps) != 1 {
		t.Fatalf("steps = %#v", report["steps"])
	}
	if _, exists := steps[0]["screenshot_after_s3_key"]; exists {
		t.Fatalf("step exposed internal storage key: %#v", steps[0])
	}
}

func TestAtlasEdgeReportNextActionsUseExactEvidenceRun(t *testing.T) {
	evidence := []map[string]interface{}{
		{
			"runs": api.AtlasResponse{
				"runs": []interface{}{
					map[string]interface{}{
						"report_id":    "report-1",
						"execution_id": "execution-1",
					},
				},
			},
		},
	}
	actions := strings.Join(atlasEdgeReportNextActions("app-1", evidence), "\n")
	if !strings.Contains(actions, "--report-id report-1") || !strings.Contains(actions, "test report execution-1") {
		t.Fatalf("unexpected edge report actions: %q", actions)
	}
}

func TestAtlasKnowledgeGraphQuerySkipsDerivedFlows(t *testing.T) {
	query := atlasKnowledgeGraphQuery(api.AtlasQuery{Limit: 20})
	if query.Limit != atlasGraphFetchLimit {
		t.Fatalf("graph fetch limit = %d", query.Limit)
	}
	if query.IncludeFlows == nil || *query.IncludeFlows {
		t.Fatalf("derived flows were requested: %#v", query.IncludeFlows)
	}
}

func TestAtlasSearchRequiresEveryQueryTerm(t *testing.T) {
	result := buildAtlasSearch(&api.App{ID: "app-1"}, atlasContractFixture(), "create expense", 10)
	matches := atlasMaps(result["results"])
	if len(matches) != 2 || atlasString(matches[0], "name") != "expense_create" {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	missing := buildAtlasSearch(&api.App{ID: "app-1"}, atlasContractFixture(), "create invoice", 10)
	if atlasInt(missing["count"]) != 0 {
		t.Fatalf("unexpected missing-term matches: %#v", missing)
	}
}

func TestAtlasSearchExposesIncompleteResults(t *testing.T) {
	result := buildAtlasSearch(&api.App{ID: "app-1"}, atlasContractFixture(), "expense", 1)
	if !atlasBool(result["truncated"]) || !atlasBool(result["has_more"]) {
		t.Fatalf("search did not expose result truncation: %#v", result)
	}

	graph := atlasContractFixture()
	atlasMap(graph["projection"])["truncated"] = true
	result = buildAtlasSearch(&api.App{ID: "app-1"}, graph, "missing", 10)
	if !atlasBool(result["truncated"]) {
		t.Fatalf("search did not expose source graph truncation: %#v", result)
	}
}

func TestAtlasAreaMatchesProductAreaNotScreenName(t *testing.T) {
	graph := atlasContractFixture()
	result := buildAtlasAreaGraph(&api.App{ID: "app-1"}, graph, "Home")
	if atlasInt(result["screen_count"]) != 1 {
		t.Fatalf("unexpected area matches: %#v", result["nodes"])
	}
	if atlasString(atlasMaps(result["nodes"])[0], "name") != "home_dashboard" {
		t.Fatalf("unexpected screen contract: %#v", result["nodes"])
	}
	edges := atlasMaps(result["edges"])
	if len(edges) != 1 || atlasString(edges[0], "scope") != "outbound_boundary" || atlasInt(result["boundary_edges"]) != 1 {
		t.Fatalf("area did not preserve boundary edge: %#v", result)
	}
}
