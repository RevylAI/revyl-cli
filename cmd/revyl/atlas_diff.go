package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
)

const (
	atlasDiffMaxPartitionValues = 8
	atlasDiffReadConcurrency    = 4
	atlasDiffTermValueMaxLength = 256
)

var atlasDiffTermKeys = map[string]string{
	"build":                 "build",
	"branch_id":             "branch_id",
	"branch":                "branch_id",
	"device_model":          "device_model",
	"device":                "device_model",
	"runtime":               "runtime",
	"os":                    "runtime",
	"test_id":               "test_id",
	"test":                  "test_id",
	"report_id":             "report_id",
	"report":                "report_id",
	"workflow_execution_id": "workflow_execution_id",
	"workflow_execution":    "workflow_execution_id",
	"from":                  "from",
	"to":                    "to",
	"include_variants":      "include_variants",
}

type atlasDiffList struct {
	Total     int                      `json:"total"`
	Items     []map[string]interface{} `json:"items"`
	Truncated bool                     `json:"truncated"`
}

type atlasDiffSet struct {
	OnlyA atlasDiffList `json:"only_a"`
	OnlyB atlasDiffList `json:"only_b"`
	Both  atlasDiffList `json:"both"`
}

type atlasDiffSideSummary struct {
	Terms                 map[string]string      `json:"terms"`
	LaunchKeys            []string               `json:"launch_keys"`
	ReplacedBaseFilters   []string               `json:"replaced_base_filters"`
	Projection            map[string]interface{} `json:"projection"`
	Completeness          string                 `json:"completeness"`
	Screens               int                    `json:"screens"`
	ObservedRelationships int                    `json:"observed_relationships"`
}

func atlasDiffCanonicalKey(key string) (string, error) {
	canonical, ok := atlasDiffTermKeys[strings.TrimSpace(key)]
	if !ok {
		if strings.TrimSpace(key) == "launch" {
			return "", fmt.Errorf("launch terms go in a_launch/b_launch (--a-launch/--b-launch), not in a or b")
		}
		return "", fmt.Errorf("unknown scope key %q; use build, branch_id, device_model, runtime, test_id, report_id, workflow_execution_id, from, to or include_variants", key)
	}
	return canonical, nil
}

func parseAtlasDiffTerms(raw []string) ([][2]string, error) {
	terms := make([][2]string, 0, len(raw))
	seen := map[string]bool{}
	for _, term := range raw {
		key, value, found := strings.Cut(term, "=")
		if !found {
			return nil, fmt.Errorf("scope term %q must be KEY=VALUE", term)
		}
		canonical, err := atlasDiffCanonicalKey(key)
		if err != nil {
			return nil, err
		}
		if seen[canonical] {
			return nil, fmt.Errorf("scope key %s appears twice in one side", canonical)
		}
		seen[canonical] = true
		terms = append(terms, [2]string{canonical, value})
	}
	return terms, nil
}

func applyAtlasDiffTerm(query *api.AtlasQuery, key, value string) error {
	if value == "" || len(value) > atlasDiffTermValueMaxLength || atlasHasControlCharacter(value) {
		return fmt.Errorf("%s needs a value of 1-%d characters", key, atlasDiffTermValueMaxLength)
	}
	switch key {
	case "build", "branch_id":
		if id, err := uuid.Parse(value); err != nil || id.String() != value {
			return fmt.Errorf("%s takes an exact lowercase UUID, such as a value listed by the graph's filters", key)
		}
		if key == "build" {
			query.BuildID = value
		} else {
			query.BranchID = value
		}
	case "device_model":
		query.DeviceModel = value
	case "runtime":
		query.DeviceRuntime = value
	case "test_id":
		query.TestID = value
	case "report_id":
		query.ReportID = value
	case "workflow_execution_id":
		query.WorkflowExecutionID = value
	case "from":
		query.FromTime = value
	case "to":
		query.ToTime = value
	case "include_variants":
		include, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("include_variants must be true or false")
		}
		query.IncludeVariants = &include
	default:
		return fmt.Errorf("unknown scope key %s", key)
	}
	return nil
}

func atlasDiffBaseValue(query api.AtlasQuery, key string) string {
	switch key {
	case "build":
		return query.BuildID
	case "branch_id":
		return query.BranchID
	case "device_model":
		return query.DeviceModel
	case "runtime":
		return query.DeviceRuntime
	case "test_id":
		return query.TestID
	case "report_id":
		return query.ReportID
	case "workflow_execution_id":
		return query.WorkflowExecutionID
	case "from":
		return query.FromTime
	case "to":
		return query.ToTime
	}
	return ""
}

func atlasLaunchTermKey(term string) string {
	key, _, _ := strings.Cut(term, ":")
	return key
}

func mergeAtlasLaunchTerms(base, side []string) ([]string, []string, error) {
	sideKeys := map[string]bool{}
	for _, term := range side {
		sideKeys[atlasLaunchTermKey(term)] = true
	}
	merged := []string{}
	replaced := map[string]bool{}
	for _, term := range base {
		if sideKeys[atlasLaunchTermKey(term)] {
			replaced[atlasLaunchTermKey(term)] = true
			continue
		}
		merged = append(merged, term)
	}
	merged = append(merged, side...)
	if len(merged) > atlasMaxLaunchTerms {
		return nil, nil, fmt.Errorf("a scope may combine at most %d launch filters", atlasMaxLaunchTerms)
	}
	replacedKeys := []string{}
	for key := range replaced {
		replacedKeys = append(replacedKeys, "launch:"+key)
	}
	sort.Strings(replacedKeys)
	return merged, replacedKeys, nil
}

func atlasLaunchKeys(terms []string) []string {
	seen := map[string]bool{}
	keys := []string{}
	for _, term := range terms {
		if key := atlasLaunchTermKey(term); !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func buildAtlasDiffSide(base api.AtlasQuery, raw []string, launch []string) (api.AtlasQuery, atlasDiffSideSummary, error) {
	terms, err := parseAtlasDiffTerms(raw)
	if err != nil {
		return api.AtlasQuery{}, atlasDiffSideSummary{}, err
	}
	query := base
	summary := atlasDiffSideSummary{Terms: map[string]string{}, ReplacedBaseFilters: []string{}}
	for _, term := range terms {
		if previous := atlasDiffBaseValue(base, term[0]); previous != "" && previous != term[1] {
			summary.ReplacedBaseFilters = append(summary.ReplacedBaseFilters, term[0])
		}
		if err := applyAtlasDiffTerm(&query, term[0], term[1]); err != nil {
			return api.AtlasQuery{}, atlasDiffSideSummary{}, err
		}
		summary.Terms[term[0]] = term[1]
	}
	merged, replaced, err := mergeAtlasLaunchTerms(base.LaunchTerms, launch)
	if err != nil {
		return api.AtlasQuery{}, atlasDiffSideSummary{}, err
	}
	query.LaunchTerms = merged
	summary.ReplacedBaseFilters = append(summary.ReplacedBaseFilters, replaced...)
	summary.LaunchKeys = atlasLaunchKeys(merged)
	return query, summary, nil
}

func readAtlasDiffGraphs(ctx context.Context, client *api.Client, queries []api.AtlasQuery, labels []string) ([]atlasScopedGraph, error) {
	graphs := make([]atlasScopedGraph, len(queries))
	failures := make([]error, len(queries))
	slots := make(chan struct{}, atlasDiffReadConcurrency)
	var wait sync.WaitGroup
	for index, query := range queries {
		wait.Add(1)
		go func() {
			defer wait.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			graphs[index], failures[index] = readScopedInvestigationGraph(ctx, client, query)
		}()
	}
	wait.Wait()
	for index, failure := range failures {
		if failure != nil {
			return nil, fmt.Errorf("Atlas graph read for %s failed: %w", labels[index], failure)
		}
	}
	return graphs, nil
}

func atlasDiffScreenSummary(item map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"id": atlasString(item, "id"), "name": item["name"], "product_area": item["product_area"]}
}

func atlasDiffEdgeSummary(item map[string]interface{}) map[string]interface{} {
	source, target := atlasMap(item["source"]), atlasMap(item["target"])
	return map[string]interface{}{"source_id": atlasString(source, "id"), "source": source["name"], "target_id": atlasString(target, "id"), "target": target["name"], "label": item["label"], "relation_type": item["relation_type"]}
}

func atlasDiffSortKey(item map[string]interface{}) string {
	for _, key := range []string{"name", "source", "id"} {
		if text, ok := item[key].(string); ok && text != "" {
			return text + "\x00" + atlasString(item, "label") + "\x00" + atlasString(item, "target")
		}
	}
	return ""
}

func sortAtlasDiffItems(items []map[string]interface{}, weight func(map[string]interface{}) int) {
	sort.SliceStable(items, func(i, j int) bool {
		if left, right := weight(items[i]), weight(items[j]); left != right {
			return left > right
		}
		if left, right := atlasDiffSortKey(items[i]), atlasDiffSortKey(items[j]); left != right {
			return left < right
		}
		return atlasString(items[i], "id")+atlasString(items[i], "source_id")+atlasString(items[i], "target_id") < atlasString(items[j], "id")+atlasString(items[j], "source_id")+atlasString(items[j], "target_id")
	})
}

func boundAtlasDiffList(items []map[string]interface{}, limit int) atlasDiffList {
	return atlasDiffList{Total: len(items), Items: atlasLimitMaps(items, limit), Truncated: len(items) > limit}
}

func atlasDiffScreenIdentity(item map[string]interface{}) string {
	return atlasString(item, "id")
}

// Relationships match on endpoints and type only: the edge key also carries the
// model's free-text action label, which differs between scopes for one transition.
func atlasDiffRelationshipIdentity(item map[string]interface{}) string {
	return atlasString(atlasMap(item["source"]), "id") + "->" + atlasString(atlasMap(item["target"]), "id") + "|" + atlasString(item, "relation_type")
}

func indexAtlasDiffItems(items []map[string]interface{}, identity func(map[string]interface{}) string) map[string]map[string]interface{} {
	indexed := map[string]map[string]interface{}{}
	for _, item := range items {
		key := identity(item)
		existing, seen := indexed[key]
		if !seen {
			indexed[key] = item
			continue
		}
		merged := map[string]interface{}{}
		for field, value := range existing {
			merged[field] = value
		}
		merged["observations"] = atlasInt(existing["observations"]) + atlasInt(item["observations"])
		indexed[key] = merged
	}
	return indexed
}

func diffAtlasItems(a, b []map[string]interface{}, identity func(map[string]interface{}) string, summarize func(map[string]interface{}) map[string]interface{}, limit int) atlasDiffSet {
	left, right := indexAtlasDiffItems(a, identity), indexAtlasDiffItems(b, identity)
	onlyA, onlyB, both := []map[string]interface{}{}, []map[string]interface{}{}, []map[string]interface{}{}
	for id, item := range left {
		summary := summarize(item)
		if other, shared := right[id]; shared {
			summary["observations_a"] = atlasInt(item["observations"])
			summary["observations_b"] = atlasInt(other["observations"])
			both = append(both, summary)
			continue
		}
		summary["observations"] = atlasInt(item["observations"])
		onlyA = append(onlyA, summary)
	}
	for id, item := range right {
		if _, shared := left[id]; !shared {
			summary := summarize(item)
			summary["observations"] = atlasInt(item["observations"])
			onlyB = append(onlyB, summary)
		}
	}
	single := func(item map[string]interface{}) int { return atlasInt(item["observations"]) }
	sortAtlasDiffItems(onlyA, single)
	sortAtlasDiffItems(onlyB, single)
	sortAtlasDiffItems(both, func(item map[string]interface{}) int {
		return atlasInt(item["observations_a"]) + atlasInt(item["observations_b"])
	})
	return atlasDiffSet{OnlyA: boundAtlasDiffList(onlyA, limit), OnlyB: boundAtlasDiffList(onlyB, limit), Both: boundAtlasDiffList(both, limit)}
}

type atlasPartitionResult struct {
	Unique              []atlasDiffList
	ScreensTotal        int
	ScreensOnEveryValue int
}

func partitionAtlasScreens(graphs []atlasScopedGraph, limit int) atlasPartitionResult {
	owners := map[string][]int{}
	nodes := make([]map[string]map[string]interface{}, len(graphs))
	unique := make([][]map[string]interface{}, len(graphs))
	for index, graph := range graphs {
		nodes[index] = map[string]map[string]interface{}{}
		unique[index] = []map[string]interface{}{}
		for _, node := range graph.Nodes {
			id := atlasString(node, "id")
			if _, seen := nodes[index][id]; !seen {
				nodes[index][id] = node
				owners[id] = append(owners[id], index)
			}
		}
	}
	result := atlasPartitionResult{ScreensTotal: len(owners)}
	for id, indexes := range owners {
		if len(indexes) == len(graphs) {
			result.ScreensOnEveryValue++
		}
		if len(indexes) != 1 {
			continue
		}
		node := nodes[indexes[0]][id]
		summary := atlasDiffScreenSummary(node)
		summary["observations"] = atlasInt(node["observations"])
		unique[indexes[0]] = append(unique[indexes[0]], summary)
	}
	for _, items := range unique {
		sortAtlasDiffItems(items, func(item map[string]interface{}) int { return atlasInt(item["observations"]) })
		result.Unique = append(result.Unique, boundAtlasDiffList(items, limit))
	}
	return result
}

func atlasGraphCompleteness(graphs []atlasScopedGraph) bool {
	for _, graph := range graphs {
		if graph.Completeness != "complete" {
			return false
		}
	}
	return true
}

func splitAtlasDiffLaunchStdin(reader io.Reader) (base, a, b []string, err error) {
	data, err := io.ReadAll(io.LimitReader(reader, atlasLaunchStdinMaxSize+1))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read launch filters from stdin: %w", err)
	}
	if len(data) > atlasLaunchStdinMaxSize {
		return nil, nil, nil, fmt.Errorf("launch filters on stdin exceed %d bytes", atlasLaunchStdinMaxSize)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 0, 4096), atlasLaunchStdinMaxSize)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		switch {
		case line == "":
		case strings.HasPrefix(line, "a:"):
			a = append(a, strings.TrimPrefix(line, "a:"))
		case strings.HasPrefix(line, "b:"):
			b = append(b, strings.TrimPrefix(line, "b:"))
		default:
			base = append(base, line)
		}
	}
	return base, a, b, nil
}

func parseAtlasPartitionKey(value string) (atlasFilterDimension, string, error) {
	text := strings.TrimSpace(value)
	if launchKey, isLaunch := strings.CutPrefix(text, "launch:"); isLaunch {
		if launchKey == "" || strings.Contains(launchKey, ":") || len(launchKey) > 256 || atlasHasControlCharacter(launchKey) {
			return atlasFilterDimension{}, "", fmt.Errorf("partition_by launch:KEY needs a launch key of at most 256 characters")
		}
		return atlasFilterDimension{group: "launch", key: launchKey, parameter: "launch"}, launchKey, nil
	}
	canonical, err := atlasDiffCanonicalKey(text)
	if err != nil {
		return atlasFilterDimension{}, "", err
	}
	for _, dimension := range atlasFilterDimensions {
		if dimension.parameter == canonical {
			return dimension, "", nil
		}
	}
	return atlasFilterDimension{}, "", fmt.Errorf("partition_by must be build, branch_id, device_model, runtime, test_id, report_id, workflow_execution_id or launch:KEY")
}

func listAtlasPartitionValues(ctx context.Context, client *api.Client, query api.AtlasQuery, dimension atlasFilterDimension, explicit []string) ([]map[string]interface{}, bool, error) {
	if len(explicit) > 0 {
		if dimension.group == "launch" {
			return nil, false, fmt.Errorf("launch partitions use the listed non-secret values; omit partition_values")
		}
		if len(explicit) > atlasDiffMaxPartitionValues {
			return nil, false, fmt.Errorf("pass at most %d partition values", atlasDiffMaxPartitionValues)
		}
		values := []map[string]interface{}{}
		seen := map[string]bool{}
		for _, value := range explicit {
			if !seen[value] {
				seen[value] = true
				values = append(values, map[string]interface{}{"value": value})
			}
		}
		return values, false, nil
	}
	request := atlasFilterScope(query)
	request.Group, request.Key = dimension.group, dimension.key
	response, err := client.GetAtlasGraphFilters(ctx, request)
	if err != nil {
		return nil, false, fmt.Errorf("list %s values: %w; pass partition_values to choose values explicitly", dimension.key, err)
	}
	if atlasBool(response["secret"]) {
		return nil, false, fmt.Errorf("launch key %s is secret, so its values are never listed or partitioned; secret keys filter only by whether they were set, with a launch term KEY", dimension.key)
	}
	listed := atlasMaps(response["values"])
	values := []map[string]interface{}{}
	for _, item := range atlasLimitMaps(listed, atlasDiffMaxPartitionValues) {
		value := map[string]interface{}{"value": atlasString(item, "value"), "run_count": atlasInt(item["run_count"])}
		if label := atlasString(item, "label"); label != "" {
			value["label"] = atlasTruncateLabel(label)
		}
		values = append(values, value)
	}
	return values, len(listed) > atlasDiffMaxPartitionValues || atlasBool(response["truncated"]), nil
}

const atlasDiffCountNote = "Counts are observation counts inside each scope. The graph does not serve per-screen run counts; run counts come from the filter listings."

func runAtlasScopeDiff(ctx context.Context, client *api.Client, app *api.App, query api.AtlasQuery, sideA, sideB, launchA, launchB []string, limit int) (map[string]interface{}, error) {
	queryA, summaryA, err := buildAtlasDiffSide(query, sideA, launchA)
	if err != nil {
		return nil, fmt.Errorf("scope a: %w", err)
	}
	queryB, summaryB, err := buildAtlasDiffSide(query, sideB, launchB)
	if err != nil {
		return nil, fmt.Errorf("scope b: %w", err)
	}
	graphs, err := readAtlasDiffGraphs(ctx, client, []api.AtlasQuery{queryA, queryB}, []string{"scope a", "scope b"})
	if err != nil {
		return nil, err
	}
	for index, summary := range []*atlasDiffSideSummary{&summaryA, &summaryB} {
		summary.Projection = graphs[index].Projection
		summary.Completeness = graphs[index].Completeness
		summary.Screens = len(graphs[index].Nodes)
		summary.ObservedRelationships = len(graphs[index].Edges)
	}
	return map[string]interface{}{
		"contract":       "atlas_scope_diff.v1",
		"app":            atlasAppSummary(app, api.AtlasResponse{"app_id": query.AppID}),
		"counts":         "observations",
		"count_note":     atlasDiffCountNote,
		"a":              summaryA,
		"b":              summaryB,
		"screens":        diffAtlasItems(graphs[0].Nodes, graphs[1].Nodes, atlasDiffScreenIdentity, atlasDiffScreenSummary, limit),
		"relationships":  diffAtlasItems(graphs[0].Edges, graphs[1].Edges, atlasDiffRelationshipIdentity, atlasDiffEdgeSummary, limit),
		"partial":        !atlasGraphCompleteness(graphs),
		"interpretation": "Both sides share every base filter; each side's terms replace the base value for that key. Only-in-one-side means observed in that scope and not in the other, not added or removed. Shared identities are candidates, not verified pixel matches. A partial side cannot establish absence. Observation counts come from testing and exploration, not user traffic.",
	}, nil
}

func runAtlasScopePartition(ctx context.Context, client *api.Client, app *api.App, query api.AtlasQuery, partitionBy string, explicit []string, limit int) (map[string]interface{}, error) {
	dimension, launchKey, err := parseAtlasPartitionKey(partitionBy)
	if err != nil {
		return nil, err
	}
	values, valuesTruncated, err := listAtlasPartitionValues(ctx, client, query, dimension, explicit)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("no recorded %s values in this branch, build and time scope", dimension.key)
	}
	queries := make([]api.AtlasQuery, len(values))
	labels := make([]string, len(values))
	replacedBase := false
	for index, item := range values {
		value := atlasString(item, "value")
		labels[index] = dimension.key + " value " + strconv.Itoa(index+1)
		queries[index] = query
		if launchKey != "" {
			merged, replaced, err := mergeAtlasLaunchTerms(query.LaunchTerms, []string{launchKey + ":" + value})
			if err != nil {
				return nil, err
			}
			queries[index].LaunchTerms = merged
			replacedBase = replacedBase || len(replaced) > 0
			continue
		}
		if atlasDiffBaseValue(query, dimension.parameter) != "" {
			replacedBase = true
		}
		if err := applyAtlasDiffTerm(&queries[index], dimension.parameter, value); err != nil {
			return nil, err
		}
	}
	graphs, err := readAtlasDiffGraphs(ctx, client, queries, labels)
	if err != nil {
		return nil, err
	}
	partition := partitionAtlasScreens(graphs, limit)
	partitions := make([]map[string]interface{}, len(values))
	for index, item := range values {
		entry := map[string]interface{}{}
		for key, value := range item {
			entry[key] = value
		}
		entry["completeness"] = graphs[index].Completeness
		entry["screens"] = len(graphs[index].Nodes)
		entry["unique_screens"] = partition.Unique[index]
		partitions[index] = entry
	}
	note := fmt.Sprintf("Read %d values of %s.", len(values), dimension.key)
	if valuesTruncated {
		note += fmt.Sprintf(" More values exist; only the %d most common were read, so a screen unique among them may still appear under an unread value. Pass partition_values to choose others.", len(values))
	}
	if len(values) == 1 {
		note += " Only one value was recorded, so every screen is trivially unique to it."
	}
	return map[string]interface{}{
		"contract":               "atlas_scope_partition.v1",
		"app":                    atlasAppSummary(app, api.AtlasResponse{"app_id": query.AppID}),
		"partition_by":           strings.TrimSpace(partitionBy),
		"replaced_base_filter":   replacedBase,
		"counts":                 "observations",
		"count_note":             atlasDiffCountNote + " Each value's run_count is from the filter listing.",
		"values":                 partitions,
		"values_truncated":       valuesTruncated,
		"values_note":            note,
		"screens_total":          partition.ScreensTotal,
		"screens_on_every_value": partition.ScreensOnEveryValue,
		"partial":                !atlasGraphCompleteness(graphs),
		"interpretation":         "A screen under unique_screens was observed in that value's scope and in no other value read here. Runs without a recorded value, unread values and partial graphs can hide other occurrences. Shared identities can over-group distinct screens. Observation counts come from testing and exploration, not user traffic.",
	}, nil
}

func newAtlasDiffCommand() *cobra.Command {
	var sideA, sideB, launchA, launchB, baseLaunch, partitionValues []string
	var launchStdin bool
	var partitionBy string
	var itemLimit int
	command := &cobra.Command{
		Use:   "diff",
		Short: "Diff Atlas screens and relationships between two filter scopes, or partition one filter key",
		Long: `Read Atlas graphs for several filter scopes concurrently and diff their screen
and relationship identities in one call.

Every scope shares the base filters (--build, --branch-id, --from, --to, --range,
--device-model, --runtime, --test-id, --report-id, --workflow-execution-id,
--launch). --a and --b add KEY=VALUE terms for each side; a side's term replaces
the base value for the same key. Keys: build, branch_id, device_model, runtime,
test_id, report_id, workflow_execution_id, from, to and include_variants. build
and branch_id take exact UUIDs.

--partition-by reads one graph per value of a filter key (device_model, runtime,
build, branch_id, test_id, report_id, workflow_execution_id or launch:KEY) and
lists the screens observed under exactly one value. Values default to the %d most
common listed values.

Launch terms are KEY=VALUE, or KEY alone for runs launched with that key set;
secret keys take only the KEY form. Launch values never travel in argv for
agents: with --launch-stdin, stdin lines apply to both sides, and lines
prefixed a: or b: apply to one side.

Counts are observation counts; the graph does not serve per-screen run counts.`,
		Example: `  revyl atlas diff --app "My App" --partition-by device_model --json
  revyl atlas diff --app "My App" --a device_model="iPhone 15" --b device_model="Pixel 8" --json
  printf 'a:FEATURE=on\nb:FEATURE=off\n' | revyl atlas diff --app "My App" --launch-stdin --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if itemLimit < 1 || itemLimit > 100 {
				return fmt.Errorf("--limit must be between 1 and 100")
			}
			stdinBase, stdinA, stdinB := []string{}, []string{}, []string{}
			if launchStdin {
				var err error
				if stdinBase, stdinA, stdinB, err = splitAtlasDiffLaunchStdin(cmd.InOrStdin()); err != nil {
					return err
				}
			}
			partition := strings.TrimSpace(partitionBy) != ""
			hasSides := len(sideA)+len(sideB)+len(launchA)+len(launchB)+len(stdinA)+len(stdinB) > 0
			if partition && hasSides {
				return fmt.Errorf("use either --partition-by or --a/--b scopes, not both")
			}
			if !partition && !hasSides {
				return fmt.Errorf("pass --a and/or --b scope terms, or --partition-by")
			}
			if !partition && len(partitionValues) > 0 {
				return fmt.Errorf("--partition-values needs --partition-by")
			}
			baseTerms, err := atlasValidateLaunchTerms(append(append([]string{}, baseLaunch...), stdinBase...))
			if err != nil {
				return err
			}
			termsA, err := atlasValidateLaunchTerms(append(append([]string{}, launchA...), stdinA...))
			if err != nil {
				return fmt.Errorf("scope a: %w", err)
			}
			termsB, err := atlasValidateLaunchTerms(append(append([]string{}, launchB...), stdinB...))
			if err != nil {
				return fmt.Errorf("scope b: %w", err)
			}
			client, err := atlasClient(cmd)
			if err != nil {
				return err
			}
			query, app, err := atlasQueryFor(cmd, client)
			if err != nil {
				return err
			}
			query.LaunchTerms = baseTerms
			query.Limit = atlasGraphFetchLimit
			query.Visibility = "included"
			query.IncludeScreenshots = false
			ctx, cancel := context.WithTimeout(cmd.Context(), 55*time.Second)
			defer cancel()
			var result map[string]interface{}
			if partition {
				result, err = runAtlasScopePartition(ctx, client, app, query, partitionBy, partitionValues, itemLimit)
			} else {
				result, err = runAtlasScopeDiff(ctx, client, app, query, sideA, sideB, termsA, termsB, itemLimit)
			}
			if err != nil {
				return err
			}
			form, completeness := "scopes", "complete"
			if partition {
				form = "partition"
			}
			if atlasBool(result["partial"]) {
				completeness = "partial"
			}
			analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{Domain: "atlas_scope_diff", DomainStatus: form + "_" + completeness})
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		},
	}
	command.Long = fmt.Sprintf(command.Long, atlasDiffMaxPartitionValues)
	addAtlasBranchFlags(command)
	flags := command.Flags()
	flags.StringVar(&atlasApp, "app", "", "App name or app id")
	flags.StringVar(&atlasBuild, "build", "", "Base build id, build version, latest, or all; defaults to all builds in the branch scope")
	flags.StringVar(&atlasFrom, "from", "", "Base start time filter (ISO timestamp with zone)")
	flags.StringVar(&atlasTo, "to", "", "Base end time filter (ISO timestamp with zone)")
	flags.StringVar(&atlasSince, "since", "", "Relative base start time, such as 7d")
	flags.StringVar(&atlasRange, "range", "", "Base time range: recent (latest 20 builds), 1d, 7d, 30d, or all")
	flags.IntVar(&atlasRecentBuildLimit, "recent-build-limit", 0, "Restrict the base scope to the latest N builds (1-100); 0 disables")
	flags.StringVar(&atlasReportID, "report-id", "", "Base filter to one report")
	flags.StringVar(&atlasTestID, "test-id", "", "Base filter to one test")
	flags.StringVar(&atlasWorkflowExecutionID, "workflow-execution-id", "", "Base filter to one workflow execution")
	flags.StringVar(&atlasDeviceModel, "device-model", "", "Base filter by device model")
	flags.StringVar(&atlasRuntime, "runtime", "", "Base filter by OS runtime")
	flags.BoolVar(&atlasIncludeVariants, "include-variants", false, "Diff state-variant nodes instead of canonical screens")
	flags.StringArrayVar(&baseLaunch, "launch", nil, "Base launch-variable filter KEY=VALUE, or KEY for runs launched with it set, for both sides, repeatable")
	flags.BoolVar(&launchStdin, "launch-stdin", false, "Read launch filters from stdin: KEY=VALUE or KEY for both sides, a: or b: prefixed for one side")
	flags.StringSliceVar(&sideA, "a", nil, "Scope A terms KEY=VALUE (build, branch_id, device_model, runtime, test_id, report_id, workflow_execution_id, from, to, include_variants)")
	flags.StringSliceVar(&sideB, "b", nil, "Scope B terms KEY=VALUE, with the same keys as scope A")
	flags.StringArrayVar(&launchA, "a-launch", nil, "Scope A launch-variable filter KEY=VALUE or KEY; replaces a base launch term with the same key")
	flags.StringArrayVar(&launchB, "b-launch", nil, "Scope B launch-variable filter KEY=VALUE or KEY; replaces a base launch term with the same key")
	flags.StringVar(&partitionBy, "partition-by", "", "List screens unique to each value of one key: device_model, runtime, build, branch_id, test_id, report_id, workflow_execution_id or launch:KEY")
	flags.StringSliceVar(&partitionValues, "partition-values", nil, "Partition values to read (at most 8); defaults to the most common listed values")
	flags.IntVar(&itemLimit, "limit", 25, "Maximum items per returned list (1-100)")
	flags.Bool("json", false, "Output JSON (always enabled for this composition)")
	_ = command.MarkFlagRequired("app")
	return command
}
