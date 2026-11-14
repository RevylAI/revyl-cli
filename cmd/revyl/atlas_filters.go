package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

const (
	atlasMaxLaunchTerms     = 8
	atlasRecentBuilds       = 20
	atlasLaunchStdinMaxSize = 64 << 10
)

var (
	atlasLaunch        []string
	atlasLaunchStdin   bool
	atlasRange         string
	atlasFilterGroup   string
	atlasFilterKey     string
	atlasFilterPrefix  string
	atlasFlows         bool
	atlasScreenDetails bool
)

var atlasFiltersCmd = &cobra.Command{
	Use:   "filters",
	Short: "List the map's filter options with run counts",
	Long: `List the Atlas map filter bar's options for the selected branch, build and
time scope: groups and keys by default, or one key's most common values with
--group and --key, or one group's keys with --group alone. Groups: time (build, branch), device (os, device), launch
(launch-variable keys) and runs (report, test, workflow_execution).

Secret launch keys are marked secret and never list values. They filter only
by whether they were set: pass --launch KEY, with no value, on a graph read.`,
	Example: `  revyl atlas filters --app "My App" --json
  revyl atlas filters --app "My App" --group launch --key FEATURE_FLAG --json
  revyl atlas filters --app "My App" --group device --key os --range 7d`,
	Args: cobra.NoArgs,
	RunE: runAtlasFilters,
}

func addAtlasFilterFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&atlasRange, "range", "", "Map time range: recent (latest 20 builds), 1d, 7d, 30d, or all")
	cmd.Flags().StringVar(&atlasRuntime, "os", "", "Alias for --runtime")
	cmd.Flags().StringVar(&atlasDeviceModel, "device", "", "Alias for --device-model")
}

func addAtlasLaunchFlags(cmd *cobra.Command) {
	cmd.Flags().StringArrayVar(&atlasLaunch, "launch", nil, "Launch-variable filter KEY=VALUE, or KEY alone for runs launched with it set (the only form for secret keys), repeatable up to 8 terms in total, where each ORed value is one term; values for one key OR, different keys AND")
	cmd.Flags().BoolVar(&atlasLaunchStdin, "launch-stdin", false, "Read KEY=VALUE or KEY launch filters from stdin, one per line, so values stay out of argv")
}

func atlasLaunchFilterTerms(cmd *cobra.Command) ([]string, error) {
	raw := append([]string{}, atlasLaunch...)
	if atlasLaunchStdin {
		data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), atlasLaunchStdinMaxSize+1))
		if err != nil {
			return nil, fmt.Errorf("read launch filters from stdin: %w", err)
		}
		if len(data) > atlasLaunchStdinMaxSize {
			return nil, fmt.Errorf("launch filters on stdin exceed %d bytes", atlasLaunchStdinMaxSize)
		}
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		scanner.Buffer(make([]byte, 0, 4096), atlasLaunchStdinMaxSize)
		for scanner.Scan() {
			if line := strings.TrimRight(scanner.Text(), ""); line != "" {
				raw = append(raw, line)
			}
		}
	}
	return atlasValidateLaunchTerms(raw)
}

func atlasValidateLaunchTerms(raw []string) ([]string, error) {
	if len(raw) > atlasMaxLaunchTerms {
		return nil, fmt.Errorf("at most %d launch filters are allowed", atlasMaxLaunchTerms)
	}
	terms := make([]string, 0, len(raw))
	for position, term := range raw {
		key, value, valued := strings.Cut(term, "=")
		if key == "" || key != strings.TrimSpace(key) || strings.Contains(key, ":") || len(key) > 256 || atlasHasControlCharacter(key) {
			return nil, fmt.Errorf("launch filter %d must be KEY=VALUE or KEY with a key of at most 256 characters and no colon", position+1)
		}
		if !valued {
			terms = append(terms, key)
			continue
		}
		if len(value) > 4096 || atlasHasControlCharacter(value) {
			return nil, fmt.Errorf("launch filter %d has an invalid value", position+1)
		}
		terms = append(terms, key+":"+value)
	}
	return terms, nil
}

func atlasHasControlCharacter(text string) bool {
	return strings.IndexFunc(text, unicode.IsControl) >= 0
}

// applyAtlasRange maps --range onto the graph scope like the map's time picker.
func applyAtlasRange(cmd *cobra.Command, query *api.AtlasQuery, now time.Time) error {
	if atlasRange == "" {
		return nil
	}
	for _, name := range []string{"from", "since", "recent-build-limit"} {
		if flag := cmd.Flags().Lookup(name); flag != nil && flag.Changed {
			return fmt.Errorf("--range cannot be combined with --%s", name)
		}
	}
	days := map[string]int{"1d": 1, "7d": 7, "30d": 30}
	switch value := strings.ToLower(atlasRange); value {
	case "recent":
		if query.BuildID == "" {
			query.RecentBuildLimit = atlasRecentBuilds
		}
	case "1d", "7d", "30d":
		today := now.UTC().Truncate(24 * time.Hour)
		query.FromTime = today.AddDate(0, 0, -days[value]).Format(time.RFC3339)
	case "all":
	default:
		return fmt.Errorf("--range must be recent, 1d, 7d, 30d, or all")
	}
	return nil
}

func refuseAtlasLaunchFilter(what string) error {
	if len(atlasLaunch) == 0 && !atlasLaunchStdin {
		return nil
	}
	return fmt.Errorf("%s does not apply launch-variable filters; drop --launch to read it unfiltered, or use a graph read such as atlas screen or atlas graph", what)
}

func recordAtlasGraphRead(cmd *cobra.Command, query api.AtlasQuery, graph api.AtlasResponse) {
	status := "unfiltered"
	if len(query.LaunchTerms) > 0 {
		status = "launch_matched"
		if atlasInt(atlasMap(graph["launch_filter"])["matched_run_count"]) == 0 {
			status = "launch_no_match"
		}
	}
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{Domain: "atlas_graph_read", DomainStatus: status})
}

func runAtlasFilters(cmd *cobra.Command, args []string) error {
	if atlasFilterKey != "" && atlasFilterGroup == "" {
		return fmt.Errorf("--key needs --group")
	}
	groupKeysOnly := atlasFilterGroup != "" && atlasFilterKey == ""
	valueGroup := atlasFilterGroup
	if groupKeysOnly {
		valueGroup = ""
	}
	client, err := atlasClient(cmd)
	if err != nil {
		return err
	}
	query, app, err := atlasQueryFor(cmd, client)
	if err != nil {
		return err
	}
	response, err := client.GetAtlasGraphFilters(cmd.Context(), api.AtlasFilterQuery{
		AppID:            query.AppID,
		Group:            valueGroup,
		Key:              atlasFilterKey,
		Prefix:           atlasFilterPrefix,
		BranchID:         query.BranchID,
		BuildID:          query.BuildID,
		FromTime:         query.FromTime,
		ToTime:           query.ToTime,
		RecentBuildLimit: query.RecentBuildLimit,
	})
	if err != nil {
		return err
	}
	level := "keys"
	if groupKeysOnly {
		groups := []map[string]interface{}{}
		for _, group := range atlasMaps(response["groups"]) {
			if atlasString(group, "id") == atlasFilterGroup {
				groups = append(groups, group)
			}
		}
		response["groups"] = groups
	} else if atlasFilterGroup != "" {
		level = "values_" + atlasFilterGroup
	}
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{Domain: "atlas_filter_options", DomainStatus: level})
	result := map[string]interface{}{
		"contract":   "atlas_filters.v1",
		"app":        atlasAppSummary(app, api.AtlasResponse{"app_id": query.AppID}),
		"projection": atlasEvidenceProjection(query),
	}
	for key, value := range response {
		result[key] = value
	}
	result["next_actions"] = atlasFilterNextActions(query, response)
	return printAtlasContract(cmd, result, printAtlasFilters)
}

var atlasFilterValueFlags = map[string]string{
	"build":              "--build",
	"branch":             "--branch-id",
	"os":                 "--os",
	"device":             "--device",
	"report":             "--report-id",
	"test":               "--test-id",
	"workflow_execution": "--workflow-execution-id",
}

func atlasFilterNextActions(query api.AtlasQuery, response api.AtlasResponse) []string {
	appID := query.AppID
	if atlasString(response, "level") != "values" {
		actions := []string{}
		for _, group := range atlasMaps(response["groups"]) {
			if keys := atlasMaps(group["keys"]); len(keys) > 0 {
				actions = append(actions, fmt.Sprintf("revyl atlas filters --app %s --group %s --key %s", appID, atlasString(group, "id"), shellQuoteAtlas(atlasString(keys[0], "key"))))
			}
		}
		return atlasScopedNextActionsForQuery(query, actions)
	}
	key := atlasString(response, "key")
	if atlasBool(response["secret"]) {
		return atlasScopedNextActionsForQuery(query, []string{fmt.Sprintf("revyl atlas graph --app %s --launch %s", appID, shellQuoteAtlas(key))})
	}
	values := atlasMaps(response["values"])
	if len(values) == 0 {
		return []string{}
	}
	value := atlasString(values[0], "value")
	if atlasString(response, "group") == "launch" {
		return atlasScopedNextActionsForQuery(query, []string{fmt.Sprintf("revyl atlas graph --app %s --launch %s", appID, shellQuoteAtlas(key+"="+value))})
	}
	flag := atlasFilterValueFlags[key]
	if flag == "" {
		return []string{}
	}
	return atlasScopedNextActionsForQuery(query, []string{fmt.Sprintf("revyl atlas graph --app %s %s %s", appID, flag, shellQuoteAtlas(value))})
}

func shellQuoteAtlas(value string) string {
	safe := value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_.:=/@+", r))
	}) < 0
	if safe {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func printAtlasFilters(result map[string]interface{}) {
	if atlasString(result, "level") == "values" {
		ui.PrintInfo("%s / %s values", atlasString(result, "group"), atlasString(result, "key"))
		if atlasBool(result["secret"]) {
			ui.PrintDim("  Secret key: values are never listed. Filter by whether it was set with --launch KEY.")
		}
		for _, value := range atlasMaps(result["values"]) {
			label := atlasString(value, "label")
			if label == "" {
				label = atlasString(value, "value")
			}
			ui.PrintDim("  %-40s runs=%d  %s", label, atlasInt(value["run_count"]), atlasString(value, "value"))
		}
		if atlasBool(result["truncated"]) {
			ui.PrintDim("  More values exist; narrow with --prefix.")
		}
	} else {
		ui.PrintInfo("Filter options over %d runs (%d without launch data)", atlasInt(result["run_count"]), atlasInt(result["runs_without_launch_data"]))
		for _, group := range atlasMaps(result["groups"]) {
			ui.PrintInfo("%s", atlasString(group, "id"))
			for _, key := range atlasMaps(group["keys"]) {
				secret := ""
				if atlasBool(key["secret"]) {
					secret = "  secret"
				}
				runs := "runs=n/a"
				if key["run_count"] != nil {
					runs = fmt.Sprintf("runs=%d", atlasInt(key["run_count"]))
				}
				ui.PrintDim("  %-40s %s%s", atlasString(key, "key"), runs, secret)
			}
		}
		if atlasBool(result["launch_recorded_in_other_builds"]) {
			ui.PrintDim("The selected builds recorded no launch variables; other builds on the branch did. Omit --build to include them.")
		}
	}
	printAtlasNext(result["next_actions"])
}
