package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
)

const atlasDiffTestApp = "11111111-1111-4111-8111-111111111111"

func atlasDiffNode(id string, observations int) map[string]interface{} {
	return map[string]interface{}{"id": id, "semantic_name": id, "product_area": "Area", "observation_count": float64(observations)}
}

func atlasDiffEdge(source, target string, observations int) map[string]interface{} {
	return map[string]interface{}{"source_entity_id": source, "target_entity_id": target, "action_type": "tap", "action_label": "Open", "observation_count": float64(observations)}
}

func atlasDiffGraph(truncated bool, nodes []map[string]interface{}, edges []map[string]interface{}) map[string]interface{} {
	if nodes == nil {
		nodes = []map[string]interface{}{}
	}
	if edges == nil {
		edges = []map[string]interface{}{}
	}
	return map[string]interface{}{"app_id": atlasDiffTestApp, "nodes": nodes, "edges": edges, "projection": map[string]interface{}{"truncated": truncated}}
}

type atlasDiffBackend struct {
	mu       sync.Mutex
	graphs   []*http.Request
	filters  []*http.Request
	graph    func(*http.Request) map[string]interface{}
	filterFn func(*http.Request) (int, interface{})
}

func (b *atlasDiffBackend) serve(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + atlasDiffTestApp:
			_ = json.NewEncoder(w).Encode(api.App{ID: atlasDiffTestApp, Name: "Fixture"})
		case "/api/v1/apps/" + atlasDiffTestApp + "/builds":
			_, _ = w.Write([]byte(`{"items":[],"total":0}`))
		case "/api/v1/atlas/v2/apps/" + atlasDiffTestApp + "/graph":
			b.mu.Lock()
			b.graphs = append(b.graphs, r)
			b.mu.Unlock()
			_ = json.NewEncoder(w).Encode(b.graph(r))
		case "/api/v1/atlas/v2/apps/" + atlasDiffTestApp + "/graph/filters":
			b.mu.Lock()
			b.filters = append(b.filters, r)
			b.mu.Unlock()
			status, body := b.filterFn(r)
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(body)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	return server
}

func runAtlasDiffCommand(t *testing.T, stdin string, args ...string) (map[string]interface{}, error) {
	t.Helper()
	resetAtlasFilterGlobals(t)
	command := newAtlasDiffCommand()
	command.SilenceUsage, command.SilenceErrors = true, true
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetIn(strings.NewReader(stdin))
	command.SetArgs(append([]string{"--app", atlasDiffTestApp}, args...))
	if err := command.Execute(); err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON %q: %v", output.String(), err)
	}
	return result, nil
}

func atlasDiffListNames(value interface{}) []string {
	names := []string{}
	for _, item := range atlasMaps(atlasMap(value)["items"]) {
		if name := atlasString(item, "name"); name != "" {
			names = append(names, name)
		} else {
			names = append(names, atlasString(item, "source")+"->"+atlasString(item, "target"))
		}
	}
	return names
}

func TestAtlasScopeDiffSplitsScreensAndRelationshipsWithSideLaunchTermsOffArgv(t *testing.T) {
	backend := &atlasDiffBackend{graph: func(r *http.Request) map[string]interface{} {
		header := r.Header.Get(api.AtlasLaunchTermsHeader)
		switch r.URL.Query().Get("device_model") {
		case "iPhone 15":
			if header != "REGION:EU,FEATURE:s3cr3t" {
				t.Errorf("scope a launch header = %q", header)
			}
			return atlasDiffGraph(false, []map[string]interface{}{atlasDiffNode("home", 8), atlasDiffNode("checkout", 3), atlasDiffNode("settings", 1)}, []map[string]interface{}{atlasDiffEdge("home", "checkout", 2)})
		case "Pixel 8":
			if header != "REGION:EU" {
				t.Errorf("scope b launch header = %q", header)
			}
			return atlasDiffGraph(false, []map[string]interface{}{atlasDiffNode("home", 5), atlasDiffNode("onboarding", 2)}, []map[string]interface{}{atlasDiffEdge("home", "onboarding", 1)})
		}
		t.Errorf("graph read lost its side scope: %s", r.URL.RawQuery)
		return atlasDiffGraph(false, nil, nil)
	}}
	backend.serve(t)

	result, err := runAtlasDiffCommand(t, "REGION=EU\na:FEATURE=s3cr3t\n", "--a", "device_model=iPhone 15", "--b", "device=Pixel 8", "--launch-stdin", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}

	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "s3cr3t") {
		t.Fatalf("launch value reached the output: %s", encoded)
	}
	for _, request := range backend.graphs {
		if strings.Contains(request.URL.String(), "s3cr3t") || request.URL.Query().Get("limit") != fmt.Sprint(atlasGraphFetchLimit) || request.URL.Query().Get("include_variants") != "false" {
			t.Errorf("graph request scope = %s", request.URL.RawQuery)
		}
	}
	if len(backend.graphs) != 2 || len(backend.filters) != 0 {
		t.Fatalf("reads = %d graphs, %d filters", len(backend.graphs), len(backend.filters))
	}
	screens := atlasMap(result["screens"])
	onlyA := atlasMap(screens["only_a"])
	if atlasInt(onlyA["total"]) != 2 || !atlasBool(onlyA["truncated"]) || strings.Join(atlasDiffListNames(onlyA), ",") != "checkout" {
		t.Fatalf("only_a = %v", onlyA)
	}
	if strings.Join(atlasDiffListNames(screens["only_b"]), ",") != "onboarding" {
		t.Fatalf("only_b = %v", screens["only_b"])
	}
	both := atlasMaps(atlasMap(screens["both"])["items"])
	if len(both) != 1 || atlasInt(both[0]["observations_a"]) != 8 || atlasInt(both[0]["observations_b"]) != 5 {
		t.Fatalf("both = %v", both)
	}
	relationships := atlasMap(result["relationships"])
	if strings.Join(atlasDiffListNames(relationships["only_a"]), ",") != "home->checkout" || strings.Join(atlasDiffListNames(relationships["only_b"]), ",") != "home->onboarding" {
		t.Fatalf("relationships = %v", relationships)
	}
	sideA := atlasMap(result["a"])
	if strings.Join(atlasStringSlice(sideA["launch_keys"]), ",") != "FEATURE,REGION" || atlasString(atlasMap(sideA["terms"]), "device_model") != "iPhone 15" || atlasString(sideA, "completeness") != "complete" {
		t.Fatalf("side a = %v", sideA)
	}
	if atlasBool(result["partial"]) || atlasString(result, "counts") != "observations" {
		t.Fatalf("diff metadata = %v", result)
	}
}

func TestAtlasScopeDiffCountsOneTransitionWithDifferentLabelsAsShared(t *testing.T) {
	labelled := func(label string, observations int) map[string]interface{} {
		edge := atlasDiffEdge("home", "checkout", observations)
		edge["action_label"] = label
		return edge
	}
	nodes := []map[string]interface{}{atlasDiffNode("home", 4), atlasDiffNode("checkout", 4)}
	backend := &atlasDiffBackend{graph: func(r *http.Request) map[string]interface{} {
		if r.URL.Query().Get("device_model") == "iPhone 15" {
			return atlasDiffGraph(false, nodes, []map[string]interface{}{labelled("Open checkout", 2), labelled("Checkout", 3)})
		}
		return atlasDiffGraph(false, nodes, []map[string]interface{}{labelled("Go to checkout", 4)})
	}}
	backend.serve(t)

	result, err := runAtlasDiffCommand(t, "", "--a", "device_model=iPhone 15", "--b", "device=Pixel 8")
	if err != nil {
		t.Fatal(err)
	}

	relationships := atlasMap(result["relationships"])
	if len(atlasDiffListNames(relationships["only_a"])) != 0 || len(atlasDiffListNames(relationships["only_b"])) != 0 {
		t.Fatalf("relationships split by label: %v", relationships)
	}
	both := atlasMaps(atlasMap(relationships["both"])["items"])
	if len(both) != 1 || atlasInt(both[0]["observations_a"]) != 5 || atlasInt(both[0]["observations_b"]) != 4 {
		t.Fatalf("both = %v", both)
	}
}

func TestAtlasScopePartitionListsScreensUniqueToEachReadValue(t *testing.T) {
	devices := []interface{}{}
	for index := 0; index < 10; index++ {
		devices = append(devices, map[string]interface{}{"value": fmt.Sprintf("device-%d", index), "run_count": float64(20 - index)})
	}
	backend := &atlasDiffBackend{
		filterFn: func(r *http.Request) (int, interface{}) {
			query := r.URL.Query()
			if query.Get("group") != "device" || query.Get("key") != "device" || query.Get("from_time") != "2026-11-01T00:00:00Z" {
				t.Errorf("partition listing scope = %s", r.URL.RawQuery)
			}
			return 200, map[string]interface{}{"level": "values", "group": "device", "key": "device", "values": devices}
		},
		graph: func(r *http.Request) map[string]interface{} {
			device := r.URL.Query().Get("device_model")
			nodes := []map[string]interface{}{atlasDiffNode("home", 4), atlasDiffNode("only-"+device, 2)}
			if device == "device-0" || device == "device-1" {
				nodes = append(nodes, atlasDiffNode("pair", 1))
			}
			return atlasDiffGraph(device == "device-7", nodes, nil)
		},
	}
	backend.serve(t)

	result, err := runAtlasDiffCommand(t, "", "--partition-by", "device", "--device-model", "canvas-device", "--from", "2026-11-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}

	if len(backend.graphs) != atlasDiffMaxPartitionValues {
		t.Fatalf("read %d partition graphs", len(backend.graphs))
	}
	for _, request := range backend.graphs {
		if request.URL.Query().Get("device_model") == "canvas-device" {
			t.Fatal("partition value did not replace the base device filter")
		}
	}
	values := atlasMaps(result["values"])
	if len(values) != atlasDiffMaxPartitionValues || !atlasBool(result["values_truncated"]) || !atlasBool(result["replaced_base_filter"]) {
		t.Fatalf("partition values = %v", result)
	}
	for index, value := range values {
		unique := atlasMap(value["unique_screens"])
		if names := atlasDiffListNames(unique); len(names) != 1 || names[0] != fmt.Sprintf("only-device-%d", index) {
			t.Errorf("value %d unique screens = %v", index, names)
		}
		if atlasInt(value["run_count"]) != 20-index {
			t.Errorf("value %d lost its run count", index)
		}
	}
	if atlasString(values[7], "completeness") != "truncated" || !atlasBool(result["partial"]) {
		t.Fatal("a truncated value graph did not mark the partition partial")
	}
	if atlasInt(result["screens_on_every_value"]) != 1 || atlasInt(result["screens_total"]) != 10 {
		t.Fatalf("partition totals = %v / %v", result["screens_on_every_value"], result["screens_total"])
	}
}

func TestAtlasScopePartitionRefusesSecretLaunchKeys(t *testing.T) {
	backend := &atlasDiffBackend{
		filterFn: func(*http.Request) (int, interface{}) {
			return 200, map[string]interface{}{"level": "values", "group": "launch", "key": "API_TOKEN", "secret": true, "values": []interface{}{}}
		},
		graph: func(*http.Request) map[string]interface{} {
			t.Error("read a graph for a secret partition")
			return atlasDiffGraph(false, nil, nil)
		},
	}
	backend.serve(t)

	if _, err := runAtlasDiffCommand(t, "", "--partition-by", "launch:API_TOKEN"); err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("secret partition err = %v", err)
	}
}

func TestAtlasDiffRejectsAmbiguousScopes(t *testing.T) {
	backend := &atlasDiffBackend{graph: func(*http.Request) map[string]interface{} {
		t.Error("read a graph for an invalid diff")
		return atlasDiffGraph(false, nil, nil)
	}}
	backend.serve(t)
	for _, args := range [][]string{
		{},
		{"--partition-by", "device_model", "--a", "build=00000000-0000-4000-8000-000000000001"},
		{"--a", "launch=FEATURE=on"},
		{"--a", "build=1.2.3"},
		{"--a", "device_model=x", "--a", "device=y"},
		{"--a", "colour=blue"},
		{"--partition-values", "x", "--a", "device_model=x"},
	} {
		if _, err := runAtlasDiffCommand(t, "", args...); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}

func TestAtlasGraphReturnsBoundedFiltersReadConcurrently(t *testing.T) {
	resetAtlasFilterGlobals(t)
	filtersArrived := make(chan struct{})
	var arrived sync.Once
	launchKeys := []interface{}{map[string]interface{}{"key": "API_TOKEN", "run_count": float64(3), "secret": true}}
	for index := 0; index < 24; index++ {
		launchKeys = append(launchKeys, map[string]interface{}{"key": fmt.Sprintf("FLAG_%02d", index), "run_count": float64(1)})
	}
	devices := []interface{}{}
	for index := 0; index < 10; index++ {
		devices = append(devices, map[string]interface{}{"value": fmt.Sprintf("Device %d", index), "run_count": float64(10 - index)})
	}
	backend := &atlasDiffBackend{
		graph: func(r *http.Request) map[string]interface{} {
			select {
			case <-filtersArrived:
			case <-time.After(10 * time.Second):
				t.Error("graph read waited for filters that never ran concurrently")
			}
			if r.URL.Query().Get("device_model") != "Pixel 7" {
				t.Errorf("graph lost its device filter: %s", r.URL.RawQuery)
			}
			graph := atlasContractFixture()
			graph["app_id"] = atlasDiffTestApp
			return graph
		},
		filterFn: func(r *http.Request) (int, interface{}) {
			arrived.Do(func() { close(filtersArrived) })
			query := r.URL.Query()
			if query.Get("from_time") != "2026-11-01T00:00:00Z" || query.Has("device_model") {
				t.Errorf("filter scope = %s", r.URL.RawQuery)
			}
			switch query.Get("key") {
			case "":
				return 200, map[string]interface{}{"level": "keys", "run_count": float64(30), "runs_without_launch_data": float64(2), "groups": []interface{}{
					map[string]interface{}{"id": "time", "keys": []interface{}{map[string]interface{}{"key": "build"}, map[string]interface{}{"key": "branch"}}},
					map[string]interface{}{"id": "device", "keys": []interface{}{map[string]interface{}{"key": "device"}, map[string]interface{}{"key": "os"}}},
					map[string]interface{}{"id": "launch", "keys": launchKeys},
				}}
			case "device":
				return 200, map[string]interface{}{"level": "values", "group": "device", "key": "device", "values": devices}
			case "build":
				return 200, map[string]interface{}{"level": "values", "group": "time", "key": "build", "values": []interface{}{map[string]interface{}{"value": "00000000-0000-4000-8000-000000000009", "label": strings.Repeat("v", 200), "run_count": float64(4)}}}
			case "os":
				return 422, map[string]interface{}{"detail": map[string]interface{}{"code": "atlas_filter_key_unknown", "message": "Unknown filter key"}}
			}
			return 200, map[string]interface{}{"level": "values", "group": query.Get("group"), "key": query.Get("key"), "values": []interface{}{}}
		},
	}
	backend.serve(t)
	cmd := newAtlasGraphFamilyCommand("graph", runAtlasGraph)
	cmd.Flags().BoolVar(&atlasWithFilters, "with-filters", false, "")
	t.Cleanup(func() { atlasWithFilters = false })
	cmd.SetArgs([]string{"--app", atlasDiffTestApp, "--device-model", "Pixel 7", "--from", "2026-11-01T00:00:00Z", "--with-filters", "--json"})

	var runErr error
	output := captureStdout(t, func() { runErr = cmd.Execute() })
	if runErr != nil {
		t.Fatal(runErr)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(atlasMaps(result["nodes"])) == 0 {
		t.Fatal("graph nodes missing")
	}
	if len(backend.filters) != len(atlasInlineFilterDimensions)+1 {
		t.Fatalf("filter reads = %d", len(backend.filters))
	}
	for _, request := range backend.filters {
		if request.URL.Query().Get("group") == "runs" {
			t.Fatalf("inline filters read a runs key: %s", request.URL.RawQuery)
		}
	}
	filters := atlasMap(result["filters"])
	keys := atlasMap(filters["keys"])
	device := atlasMap(keys["device"])
	if len(atlasMaps(device["values"])) != atlasInlineFilterValueLimit || !atlasBool(device["truncated"]) || atlasString(device, "parameter") != "device_model" {
		t.Fatalf("device filter = %v", device)
	}
	build := atlasMaps(atlasMap(keys["build"])["values"])
	if len(build) != 1 || len([]rune(atlasString(build[0], "label"))) != atlasInlineLabelLimit {
		t.Fatalf("build filter = %v", build)
	}
	if atlasString(atlasMap(keys["os"]), "error") == "" || !atlasBool(filters["available"]) {
		t.Fatalf("os failure was not isolated: %v", keys["os"])
	}
	if _, listed := keys["report"]; listed || atlasString(filters, "runs_usage") == "" {
		t.Fatalf("runs keys were inlined: %v", keys)
	}
	launch := atlasMaps(filters["launch_keys"])
	if len(launch) != atlasInlineLaunchKeyLimit || !atlasBool(launch[0]["secret"]) || launch[0]["values"] != nil {
		t.Fatalf("launch keys = %v", launch)
	}
	notes := strings.Join(atlasStringSlice(filters["truncated"]), "\n")
	if !strings.Contains(notes, "device:") || !strings.Contains(notes, "launch keys") {
		t.Fatalf("truncation notes = %q", notes)
	}
	if len(output) > 60000 {
		t.Fatalf("graph with filters is %d bytes", len(output))
	}
}

func TestAtlasGraphKeepsGraphWhenFilterOptionsAreUnsupported(t *testing.T) {
	resetAtlasFilterGlobals(t)
	backend := &atlasDiffBackend{
		graph: func(*http.Request) map[string]interface{} {
			graph := atlasContractFixture()
			graph["app_id"] = atlasDiffTestApp
			return graph
		},
		filterFn: func(*http.Request) (int, interface{}) {
			return 422, map[string]interface{}{"detail": map[string]interface{}{"code": "atlas_filter_options_unsupported", "message": "Filter options need the ClickHouse graph reader for this app"}}
		},
	}
	backend.serve(t)
	cmd := newAtlasGraphFamilyCommand("graph", runAtlasGraph)
	cmd.Flags().BoolVar(&atlasWithFilters, "with-filters", false, "")
	t.Cleanup(func() { atlasWithFilters = false })
	cmd.SetArgs([]string{"--app", atlasDiffTestApp, "--with-filters", "--json"})

	var runErr error
	output := captureStdout(t, func() { runErr = cmd.Execute() })
	if runErr != nil {
		t.Fatal(runErr)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	filters := atlasMap(result["filters"])
	if atlasBool(filters["available"]) || atlasString(filters, "reason") == "" || len(atlasMaps(result["nodes"])) == 0 {
		t.Fatalf("unsupported filters = %v", filters)
	}
	if len(backend.filters) != 1 {
		t.Fatalf("filter reads after an unsupported keys read = %d", len(backend.filters))
	}
}

func TestAtlasPartitionComparesCanonicalIdentitiesAcrossValues(t *testing.T) {
	graphs := []atlasScopedGraph{
		{Nodes: []map[string]interface{}{{"id": "a", "name": "A", "observations": 1}, {"id": "shared", "name": "Shared", "observations": 9}, {"id": "low", "name": "Low", "observations": 1}, {"id": "high", "name": "High", "observations": 5}}, Completeness: "complete"},
		{Nodes: []map[string]interface{}{{"id": "shared", "name": "Shared", "observations": 2}}, Completeness: "complete"},
		{Nodes: []map[string]interface{}{}, Completeness: "complete"},
	}
	result := partitionAtlasScreens(graphs, 2)
	first := result.Unique[0]
	if first.Total != 3 || !first.Truncated || atlasString(first.Items[0], "id") != "high" || atlasString(first.Items[1], "id") != "a" {
		t.Fatalf("first value unique = %+v", first)
	}
	if result.Unique[1].Total != 0 || result.Unique[2].Total != 0 || result.ScreensOnEveryValue != 0 || result.ScreensTotal != 4 {
		t.Fatalf("partition = %+v", result)
	}
}
