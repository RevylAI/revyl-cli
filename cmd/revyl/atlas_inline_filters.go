package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

const (
	atlasInlineFilterValueLimit = 8
	atlasInlineLaunchKeyLimit   = 20
	atlasInlineLabelLimit       = 80
)

var atlasWithFilters bool

type atlasFilterDimension struct {
	group, key, parameter string
}

var atlasInlineFilterDimensions = []atlasFilterDimension{
	{"time", "build", "build"},
	{"time", "branch", "branch_id"},
	{"device", "device", "device_model"},
	{"device", "os", "runtime"},
}

// Run keys stay out of the inline set: each value read collects the scope's
// run sessions and then queries Postgres, and can fail on large scopes.
var atlasFilterDimensions = append(append([]atlasFilterDimension{}, atlasInlineFilterDimensions...),
	atlasFilterDimension{"runs", "test", "test_id"},
	atlasFilterDimension{"runs", "report", "report_id"},
	atlasFilterDimension{"runs", "workflow_execution", "workflow_execution_id"},
)

func atlasFilterScope(query api.AtlasQuery) api.AtlasFilterQuery {
	return api.AtlasFilterQuery{
		AppID:            query.AppID,
		BranchID:         query.BranchID,
		BuildID:          query.BuildID,
		FromTime:         query.FromTime,
		ToTime:           query.ToTime,
		RecentBuildLimit: query.RecentBuildLimit,
	}
}

func atlasFilterReadFailure(err error) string {
	message := err.Error()
	if len(message) > 240 {
		message = message[:240]
	}
	return message
}

func atlasTruncateLabel(label string) string {
	runes := []rune(label)
	if len(runes) > atlasInlineLabelLimit {
		return string(runes[:atlasInlineLabelLimit-1]) + "…"
	}
	return label
}

func readAtlasInlineFilters(ctx context.Context, client *api.Client, query api.AtlasQuery) map[string]interface{} {
	scope := atlasFilterScope(query)
	responses := make([]api.AtlasResponse, len(atlasInlineFilterDimensions)+1)
	failures := make([]error, len(responses))
	responses[0], failures[0] = client.GetAtlasGraphFilters(ctx, scope)
	if failures[0] != nil {
		return map[string]interface{}{"available": false, "reason": atlasFilterReadFailure(failures[0])}
	}
	var wait sync.WaitGroup
	for index, dimension := range atlasInlineFilterDimensions {
		request := scope
		request.Group, request.Key = dimension.group, dimension.key
		wait.Add(1)
		go func() {
			defer wait.Done()
			responses[index+1], failures[index+1] = client.GetAtlasGraphFilters(ctx, request)
		}()
	}
	wait.Wait()
	truncated := []string{}
	keys := map[string]interface{}{}
	for index, dimension := range atlasInlineFilterDimensions {
		entry := map[string]interface{}{"parameter": dimension.parameter}
		keys[dimension.key] = entry
		if failure := failures[index+1]; failure != nil {
			entry["error"] = atlasFilterReadFailure(failure)
			continue
		}
		response := responses[index+1]
		listed := atlasMaps(response["values"])
		values := make([]map[string]interface{}, 0, min(len(listed), atlasInlineFilterValueLimit))
		for _, item := range atlasLimitMaps(listed, atlasInlineFilterValueLimit) {
			value := map[string]interface{}{"value": atlasString(item, "value"), "run_count": atlasInt(item["run_count"])}
			if label := atlasString(item, "label"); label != "" {
				value["label"] = atlasTruncateLabel(label)
			}
			values = append(values, value)
		}
		entry["values"] = values
		if len(listed) > atlasInlineFilterValueLimit || atlasBool(response["truncated"]) {
			entry["truncated"] = true
			truncated = append(truncated, fmt.Sprintf("%s: %d most common values shown; atlas.filters with group=%s key=%s (and prefix) lists more", dimension.key, len(values), dimension.group, dimension.key))
		}
	}
	launchKeys := []map[string]interface{}{}
	listedLaunchKeys := 0
	for _, group := range atlasMaps(responses[0]["groups"]) {
		if atlasString(group, "id") != "launch" {
			continue
		}
		for _, key := range atlasMaps(group["keys"]) {
			listedLaunchKeys++
			if len(launchKeys) < atlasInlineLaunchKeyLimit {
				launchKeys = append(launchKeys, map[string]interface{}{"key": atlasString(key, "key"), "run_count": atlasInt(key["run_count"]), "secret": atlasBool(key["secret"])})
			}
		}
	}
	if listedLaunchKeys > len(launchKeys) || atlasBool(responses[0]["keys_truncated"]) {
		truncated = append(truncated, fmt.Sprintf("launch keys: %d shown; atlas.filters with group=launch lists the keys", len(launchKeys)))
	}
	result := map[string]interface{}{
		"available":                true,
		"scope":                    "Run counts cover the branch, build and time scope; this read's device, run and launch filters do not narrow them.",
		"run_count":                atlasInt(responses[0]["run_count"]),
		"runs_without_launch_data": atlasInt(responses[0]["runs_without_launch_data"]),
		"keys":                     keys,
		"launch_keys":              launchKeys,
		"launch_usage":             "Filter with launch: [\"KEY=VALUE\"], or [\"KEY\"] for runs launched with KEY set. Values are not listed here; atlas.filters with group=launch and key lists a non-secret key's values. Secret keys never list values and filter only by whether they were set, with [\"KEY\"].",
		"runs_usage":               "Tests, reports and workflow executions are not listed here; atlas.filters with group=runs and key test, report or workflow_execution lists them.",
		"truncated":                truncated,
	}
	if recorded, present := responses[0]["launch_recorded_in_other_builds"].(bool); present {
		result["launch_recorded_in_other_builds"] = recorded
	}
	return result
}

func printAtlasInlineFilters(value interface{}) {
	filters := atlasMap(value)
	if len(filters) == 0 {
		return
	}
	ui.Println()
	if !atlasBool(filters["available"]) {
		ui.PrintDim("Filter options unavailable: %s", atlasString(filters, "reason"))
		return
	}
	ui.PrintInfo("Filters over %d runs", atlasInt(filters["run_count"]))
	keys := atlasMap(filters["keys"])
	for _, dimension := range atlasInlineFilterDimensions {
		entry := atlasMap(keys[dimension.key])
		if failure := atlasString(entry, "error"); failure != "" {
			ui.PrintDim("  %-20s unavailable: %s", dimension.key, failure)
			continue
		}
		labels := []string{}
		for _, item := range atlasMaps(entry["values"]) {
			label := atlasString(item, "label")
			if label == "" {
				label = atlasString(item, "value")
			}
			labels = append(labels, fmt.Sprintf("%s (%d)", label, atlasInt(item["run_count"])))
		}
		ui.PrintDim("  %-20s %s", dimension.key, strings.Join(labels, ", "))
	}
	launch := []string{}
	for _, key := range atlasMaps(filters["launch_keys"]) {
		launch = append(launch, atlasString(key, "key"))
	}
	if len(launch) > 0 {
		ui.PrintDim("  %-20s %s", "launch keys", strings.Join(launch, ", "))
	}
	for _, note := range atlasStringSlice(filters["truncated"]) {
		ui.PrintDim("  %s", note)
	}
	ui.PrintDim("  Tests, reports and workflow executions: revyl atlas filters --group runs --key test|report|workflow_execution")
}
