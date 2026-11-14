package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/toolcatalog"
	"github.com/spf13/cobra"
)

func TestInvestigationCatalogMatchesExecutableCommands(t *testing.T) {
	catalog, err := toolcatalog.Build(rootCmd, investigationToolSpecs())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) < 25 {
		t.Fatalf("missing tool families: %d", len(catalog))
	}
	for _, tool := range catalog {
		t.Run(tool.Name, func(t *testing.T) {
			input := map[string]any{}
			for _, key := range tool.InputSchema.Required {
				if tool.InputSchema.Properties[key].Type == "array" {
					input[key] = []any{"00000000-0000-4000-8000-000000000001"}
					continue
				}
				value := "sample"
				if tool.InputSchema.Properties[key].Format == "uuid" {
					value = "00000000-0000-4000-8000-000000000001"
				}
				if enum := tool.InputSchema.Properties[key].Enum; len(enum) > 0 {
					value = enum[0]
				}
				input[key] = value
			}
			args, err := tool.Arguments(input)
			if err != nil {
				t.Fatal(err)
			}
			cmd, _, err := rootCmd.Find(args)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(tool.Command, " ") != strings.TrimPrefix(cmd.CommandPath(), "revyl ") {
				t.Fatalf("wrong command %s", cmd.CommandPath())
			}
			positionals := []string{}
			for _, key := range tool.Positionals {
				positionals = append(positionals, input[key].(string))
			}
			if err = cmd.ValidateArgs(positionals); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAtlasCatalogDeviceFilters(t *testing.T) {
	for _, name := range []string{"atlas.brief", "atlas.graph", "atlas.search", "atlas.area", "atlas.screen", "atlas.neighbors", "atlas.observations", "atlas.edge"} {
		t.Run(name, func(t *testing.T) {
			tool, err := findInvestigationTool(rootCmd, name)
			if err != nil {
				t.Fatal(err)
			}
			for _, filters := range []map[string]any{{}, {"device_model": "Pixel 7"}, {"runtime": "Android 14"}, {"device_model": "Pixel 7", "runtime": "Android 14"}} {
				input := map[string]any{}
				for _, key := range tool.InputSchema.Required {
					input[key] = "00000000-0000-4000-8000-000000000001"
				}
				for key, value := range filters {
					input[key] = value
				}
				args, err := tool.Arguments(input)
				if err != nil {
					t.Fatal(err)
				}
				for _, filter := range []struct{ key, flag, value string }{{"device_model", "--device-model=", "Pixel 7"}, {"runtime", "--runtime=", "Android 14"}} {
					found := false
					for _, arg := range args {
						if strings.HasPrefix(arg, filter.flag) {
							found = true
							if arg != filter.flag+filter.value {
								t.Fatalf("unexpected filter argument %q", arg)
							}
						}
					}
					_, expected := filters[filter.key]
					if found != expected {
						t.Fatalf("filter %s presence = %v, want %v: %v", filter.key, found, expected, args)
					}
				}
			}
		})
	}
}

func TestAtlasCaptureListingSupportsViewScope(t *testing.T) {
	tool, err := findInvestigationTool(rootCmd, "atlas.observations")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"recent_build_limit", "workflow_execution_id"} {
		if _, ok := tool.InputSchema.Properties[key]; !ok {
			t.Fatalf("capture listing lacks %s", key)
		}
	}
	exact, err := findInvestigationTool(rootCmd, "atlas.observation")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := exact.InputSchema.Properties["recent_build_limit"]; ok {
		t.Fatal("exact capture should not accept aggregate scope")
	}
}

func TestAtlasCatalogBranchScopeDefaults(t *testing.T) {
	const branchID = "00000000-0000-4000-8000-000000000002"
	const buildID = "00000000-0000-4000-8000-000000000003"
	for _, name := range []string{"atlas.brief", "atlas.graph", "atlas.search", "atlas.area", "atlas.screen", "atlas.neighbors", "atlas.observations", "atlas.edge"} {
		t.Run(name, func(t *testing.T) {
			tool, err := findInvestigationTool(rootCmd, name)
			if err != nil {
				t.Fatal(err)
			}
			if tool.InputSchema.Properties["branch_id"].Format != "uuid" {
				t.Fatal("branch_id must validate an immutable UUID")
			}
			for _, build := range []string{"", buildID, "all"} {
				input := map[string]any{"branch_id": branchID}
				for _, key := range tool.InputSchema.Required {
					input[key] = "00000000-0000-4000-8000-000000000001"
				}
				if build != "" {
					input["build"] = build
				}
				args, err := tool.Arguments(input)
				if err != nil {
					t.Fatal(err)
				}
				joined := strings.Join(args, " ")
				if !strings.Contains(joined, "--branch-id="+branchID) {
					t.Fatalf("branch missing: %v", args)
				}
				if build == "" && strings.Contains(joined, "--build=") {
					t.Fatalf("branch aggregate injected build: %v", args)
				}
				if build != "" && !strings.Contains(joined, "--build="+build) {
					t.Fatalf("explicit build lost: %v", args)
				}
				delete(input, "branch_id")
				delete(input, "build")
				args, err = tool.Arguments(input)
				if err != nil {
					t.Fatal(err)
				}
				wantAll := name != "atlas.observations" && name != "atlas.edge"
				if strings.Contains(strings.Join(args, " "), "--build=all") != wantAll {
					t.Fatalf("unscoped default changed: %v", args)
				}
			}
		})
	}
}

func TestAtlasBranchViewPreservesAggregateScope(t *testing.T) {
	tool, err := findInvestigationTool(rootCmd, "atlas.graph")
	if err != nil {
		t.Fatal(err)
	}
	const branchID = "00000000-0000-4000-8000-000000000002"
	view, err := tool.View(map[string]any{"app": "00000000-0000-4000-8000-000000000001", "branch_id": branchID})
	if err != nil {
		t.Fatal(err)
	}
	if view.Query.Arguments["branch_id"] != branchID {
		t.Fatalf("branch lost: %+v", view)
	}
	if _, exists := view.Query.Arguments["build"]; exists {
		t.Fatalf("build injected: %+v", view)
	}
	if err := tool.ValidateView(view); err != nil {
		t.Fatal(err)
	}
}

func TestInvestigationLaunchesAreExplicitWritesAndNeverWait(t *testing.T) {
	for _, name := range []string{"explorations.launch", "tests.run"} {
		tool, err := findInvestigationTool(rootCmd, name)
		if err != nil {
			t.Fatal(err)
		}
		if tool.ReadOnly {
			t.Fatalf("%s must be marked as a write", name)
		}
		input := map[string]any{}
		for _, key := range tool.InputSchema.Required {
			input[key] = "00000000-0000-4000-8000-000000000001"
		}
		args, err := tool.Arguments(input)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(args, " "), "--no-wait") {
			t.Fatalf("%s must return after dispatch: %v", name, args)
		}
		if _, err := tool.Arguments(map[string]any{"no_wait": false}); err == nil {
			t.Fatal("launch must not override no-wait")
		}
		if name == "explorations.launch" {
			joined := strings.Join(args, " ")
			if !strings.Contains(joined, "--explorers=1") || !strings.Contains(joined, "--max-duration=10m") {
				t.Fatalf("missing focused launch defaults: %v", args)
			}
		}
	}
}

func TestInvestigationRejectsUnsafeArguments(t *testing.T) {
	tool, err := findInvestigationTool(rootCmd, "reports.logs")
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"execution_id":"not-an-id"}`,
		`{"execution_id":"00000000-0000-4000-8000-000000000001","download":true}`,
		`{"execution_id":"00000000-0000-4000-8000-000000000001","tail":0}`,
		`{"execution_id":"00000000-0000-4000-8000-000000000001","tail":101}`,
		`{"execution_id":"00000000-0000-4000-8000-000000000001","tail":"20"}`,
		`{"execution_id":"00000000-0000-4000-8000-000000000001","level":[1]}`,
		`{"execution_id":null}`,
	} {
		var input map[string]any
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		if err = decoder.Decode(&input); err != nil {
			t.Fatal(err)
		}
		if _, err = tool.Arguments(input); err == nil {
			t.Errorf("accepted invalid arguments %s", raw)
		}
	}
}

func TestInvestigationPositionalTextCannotBecomeFlags(t *testing.T) {
	tool, err := findInvestigationTool(rootCmd, "atlas.search")
	if err != nil {
		t.Fatal(err)
	}
	args, err := tool.Arguments(map[string]any{"app": "00000000-0000-4000-8000-000000000001", "query": "--help; $(echo injected)"})
	if err != nil {
		t.Fatal(err)
	}
	if args[len(args)-2] != "--" || args[len(args)-1] != "--help; $(echo injected)" {
		t.Fatalf("positional was not isolated: %v", args)
	}
}

func TestInvestigationOutputBudgetCancelsWithoutTruncatingJSON(t *testing.T) {
	cancelled := false
	out := &boundedToolOutput{limit: 4, cancel: func() { cancelled = true }}
	if _, err := out.Write([]byte("12345")); err == nil || !cancelled || !out.exceeded || out.Len() != 0 {
		t.Fatal("output did not fail closed")
	}
}

func TestInvestigationViewsCannotExecuteWritesOrChangeRenderers(t *testing.T) {
	tool, err := findInvestigationTool(rootCmd, "atlas.graph")
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"app": "00000000-0000-4000-8000-000000000001"}
	view, err := tool.View(input)
	if err != nil {
		t.Fatal(err)
	}
	if view.Renderer != "atlas_graph" || view.Query.Arguments["limit"] != 25 || view.Query.Arguments["build"] != "all" {
		t.Fatalf("missing native view defaults: %+v", view)
	}
	if err = tool.ValidateView(view); err != nil {
		t.Fatal(err)
	}
	view.Renderer = "javascript"
	if err = tool.ValidateView(view); err == nil {
		t.Fatal("accepted an arbitrary renderer")
	}
	view.Renderer = "atlas_graph"
	view.Version = 2
	if err = tool.ValidateView(view); err == nil {
		t.Fatal("accepted an unknown version")
	}
	input["since"] = "7d"
	if _, err = tool.View(input); err == nil {
		t.Fatal("accepted moving time window in replayable recipe")
	}
	delete(input, "since")
	input["build"] = "latest"
	if _, err = tool.View(input); err == nil {
		t.Fatal("accepted moving build alias")
	}
	input["build"] = ""
	if _, err = tool.View(input); err == nil {
		t.Fatal("accepted empty build alias that resolves to latest")
	}
	network, err := findInvestigationTool(rootCmd, "reports.network")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = network.View(map[string]any{"execution_id": "00000000-0000-4000-8000-000000000001", "since": 10.0, "until": 20.0}); err != nil {
		t.Fatalf("rejected a stable run-relative time interval: %v", err)
	}
	write, err := findInvestigationTool(rootCmd, "annotations.create")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = write.View(map[string]any{}); err == nil {
		t.Fatal("mutation became a hydration query")
	}
}

func TestInvestigationErrorsNeverExposeProviderOutput(t *testing.T) {
	for _, message := range []string{"download https://example.com/media?token=private failed", "404 private payload", "no device logs: private payload"} {
		if strings.Contains(investigationFailureHint(message), "private") || strings.Contains(investigationFailureHint(message), "https://") {
			t.Fatal("provider output escaped into error")
		}
	}
}

func TestInvestigationCookbooksReferenceAvailableTools(t *testing.T) {
	for _, cookbook := range toolcatalog.Cookbooks() {
		for _, step := range cookbook.Steps {
			if _, err := findInvestigationTool(rootCmd, step.Tool); err != nil {
				t.Fatalf("cookbook %s: %v", cookbook.Name, err)
			}
		}
	}
}

func TestInvestigationConnectivityFailureIsActionableAndSanitized(t *testing.T) {
	hint := investigationFailureHint("Get https://private.example.com?token=private: dial tcp: lookup private.example.com: no such host")
	if !strings.Contains(hint, "backend unavailable") || strings.Contains(hint, "private") {
		t.Fatalf("unsafe or unhelpful hint: %s", hint)
	}
}

func TestSessionDiagnosticsKeepTheirReferenceKind(t *testing.T) {
	for _, name := range []string{"sessions.performance", "sessions.logs", "sessions.network", "sessions.state"} {
		tool, err := findInvestigationTool(rootCmd, name)
		if err != nil {
			t.Fatal(err)
		}
		args, err := tool.Arguments(map[string]any{"session_id": "00000000-0000-4000-8000-000000000001"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(args, " "), "--id-kind=session") {
			t.Fatalf("lost reference kind: %v", args)
		}
		if _, err := tool.Arguments(map[string]any{"execution_id": "00000000-0000-4000-8000-000000000001"}); err == nil {
			t.Fatal("accepted execution where session is required")
		}
	}
}

func TestReportRecoveryTargetsExecutableBoundedRead(t *testing.T) {
	tool, err := findInvestigationTool(rootCmd, "sessions.report")
	if err != nil {
		t.Fatal(err)
	}
	failure := toolFailure(tool, map[string]any{"session_id": "00000000-0000-4000-8000-000000000001"}, "result_too_large", "")
	if failure.RetryUnchanged || len(failure.Recovery) != 1 {
		t.Fatalf("invalid recovery: %+v", failure)
	}
	next := failure.Recovery[0]
	target, err := findInvestigationTool(rootCmd, next.Tool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Arguments(next.Arguments); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"reports.logs", "sessions.logs"} {
		target, err := findInvestigationTool(rootCmd, name)
		if err != nil {
			t.Fatal(err)
		}
		input := map[string]any{target.Positionals[0]: "00000000-0000-4000-8000-000000000001", "limit": 5, "context": 3}
		args, err := target.Arguments(input)
		if err != nil {
			t.Fatal(err)
		}
		for _, arg := range args {
			if strings.HasPrefix(arg, "--tail=") {
				t.Fatal("tail silently narrows a forward page")
			}
		}
	}
}

func TestAtlasCatalogLaunchFiltersStayOutOfArgvAndViews(t *testing.T) {
	for _, name := range []string{"atlas.brief", "atlas.graph", "atlas.search", "atlas.area", "atlas.screen", "atlas.neighbors", "atlas.edge", "atlas.diff"} {
		t.Run(name, func(t *testing.T) {
			tool, err := findInvestigationTool(rootCmd, name)
			if err != nil {
				t.Fatal(err)
			}
			input := map[string]any{"launch": []any{"API_TOKEN=s3cr3t", "REGION=EU"}, "range": "7d"}
			for _, key := range tool.InputSchema.Required {
				input[key] = "00000000-0000-4000-8000-000000000001"
			}
			args, err := tool.Arguments(input)
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "s3cr3t") || !strings.Contains(joined, "--launch-stdin") || !strings.Contains(joined, "--range=7d") {
				t.Fatalf("launch or range argv = %v", args)
			}
			input["launch"] = []any{"A=1", "B=1", "C=1", "D=1", "E=1", "F=1", "G=1", "H=1", "I=1"}
			if _, err := tool.Arguments(input); err == nil {
				t.Fatal("accepted more launch filters than the map allows")
			}
		})
	}
	graph, err := findInvestigationTool(rootCmd, "atlas.graph")
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []map[string]any{
		{"app": "00000000-0000-4000-8000-000000000001", "launch": []any{"REGION=EU"}},
		{"app": "00000000-0000-4000-8000-000000000001", "range": "recent"},
	} {
		if _, err := graph.View(input); err == nil {
			t.Fatalf("replayable view accepted %v", input)
		}
	}
	if _, err := graph.View(map[string]any{"app": "00000000-0000-4000-8000-000000000001", "range": "all"}); err != nil {
		t.Fatalf("rejected the stable all range: %v", err)
	}
	filters, err := findInvestigationTool(rootCmd, "atlas.filters")
	if err != nil {
		t.Fatal(err)
	}
	if !filters.ReadOnly || filters.InputSchema.Properties["group"].Enum == nil {
		t.Fatalf("atlas.filters contract = %+v", filters)
	}
}

func TestAtlasDiffSideLaunchTermsTravelOnPrefixedStdin(t *testing.T) {
	tool, err := findInvestigationTool(rootCmd, "atlas.diff")
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"app": "00000000-0000-4000-8000-000000000001", "launch": []any{"REGION=EU"}, "a_launch": []any{"API_TOKEN=s3cr3t"}, "b_launch": []any{"API_TOKEN=other"}, "a": []any{"device_model=iPhone 15, Pro"}}

	args, err := tool.Arguments(input)
	if err != nil {
		t.Fatal(err)
	}
	stdin, ok := toolcatalog.LaunchStdin(input)

	joined := strings.Join(args, " ")
	if strings.Contains(joined, "s3cr3t") || strings.Count(joined, "--launch-stdin") != 1 || !ok || stdin != "REGION=EU\na:API_TOKEN=s3cr3t\nb:API_TOKEN=other" {
		t.Fatalf("argv = %v, stdin = %q", args, stdin)
	}
	cmd, _, err := rootCmd.Find(args)
	if err != nil || cmd.Name() != "diff" {
		t.Fatalf("diff command = %v, %v", cmd, err)
	}
	if err := cmd.ParseFlags(args[2:]); err != nil {
		t.Fatal(err)
	}
	sides, _ := cmd.Flags().GetStringSlice("a")
	if len(sides) != 1 || sides[0] != "device_model=iPhone 15, Pro" {
		t.Fatalf("scope a terms = %q", sides)
	}
}

func TestAtlasBriefCarriesInlineFiltersByDefaultAndGraphOnRequest(t *testing.T) {
	for name, want := range map[string]bool{"atlas.brief": true, "atlas.graph": false} {
		tool, err := findInvestigationTool(rootCmd, name)
		if err != nil {
			t.Fatal(err)
		}
		args, err := tool.Arguments(map[string]any{"app": "00000000-0000-4000-8000-000000000001"})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.Join(args, " "), "--with-filters=true") != want {
			t.Fatalf("%s argv = %v", name, args)
		}
	}
	graph, err := findInvestigationTool(rootCmd, "atlas.graph")
	if err != nil {
		t.Fatal(err)
	}
	args, err := graph.Arguments(map[string]any{"app": "00000000-0000-4000-8000-000000000001", "with_filters": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, " "), "--with-filters=true") {
		t.Fatalf("atlas.graph opt-in argv = %v", args)
	}
}

func TestToolSearchIncludesSchemasOnlyForNarrowMatches(t *testing.T) {
	catalog, err := toolcatalog.Build(rootCmd, investigationToolSpecs())
	if err != nil {
		t.Fatal(err)
	}
	narrow := searchInvestigationTools(catalog, "atlas diff")
	if len(narrow) == 0 || len(narrow) > toolSearchSchemaLimit {
		t.Fatalf("narrow search matched %d tools", len(narrow))
	}
	for _, match := range narrow {
		if match.InputSchema == nil || len(match.InputSchema.Properties) == 0 {
			t.Fatalf("%s lacks its input schema", match.Name)
		}
	}
	broad := searchInvestigationTools(catalog, "atlas")
	if len(broad) <= toolSearchSchemaLimit {
		t.Fatalf("broad search matched %d tools", len(broad))
	}
	for _, match := range broad {
		if match.InputSchema != nil {
			t.Fatalf("broad search inlined %s's schema", match.Name)
		}
	}
}

func TestToolCallInvalidArgumentsNameTheParameterAndLimit(t *testing.T) {
	tool, err := findInvestigationTool(rootCmd, "atlas.graph")
	if err != nil {
		t.Fatal(err)
	}
	app := "00000000-0000-4000-8000-000000000001"
	terms := []any{}
	for index := 0; index < 9; index++ {
		terms = append(terms, fmt.Sprintf("KEY_%d", index))
	}
	for _, scenario := range []struct {
		input map[string]any
		want  string
	}{
		{map[string]any{"app": app, "launch": terms}, "parameter launch: expected 0 to 8 items"},
		{map[string]any{"app": app, "bogus": true}, `unknown parameter "bogus"`},
		{map[string]any{}, "required parameter app is missing"},
	} {
		err := executeInvestigationTool(&cobra.Command{}, tool, scenario.input, nil)
		var failure *investigationError
		if !errors.As(err, &failure) || failure.Code != "invalid_arguments" || !strings.Contains(failure.Message, scenario.want) {
			t.Errorf("input %v: got %v, want a message naming %q", scenario.input, err, scenario.want)
		}
	}
}

func TestToolCallPassesThroughOnlyTheAtlasInputRefusal(t *testing.T) {
	for stderr, want := range map[string]string{
		"Error: at most 8 launch filters are allowed\n": "at most 8 launch filters are allowed",
		"Error: Launch filter term 2 names a secret variable; secret launch variables can be filtered only by whether they were set, so send the key alone\n":                  "Launch filter term 2 names a secret variable; secret launch variables can be filtered only by whether they were set, so send the key alone",
		"Error: atlas screen --details does not apply launch-variable filters; drop --launch to read it unfiltered, or use a graph read such as atlas screen or atlas graph\n": "atlas screen --details does not apply launch-variable filters; drop --launch to read it unfiltered, or use a graph read such as atlas screen or atlas graph",
	} {
		code, message := classifyInvestigationFailure(stderr + "TOKEN=private-value https://private.example.com")
		if code != "invalid_arguments" || message != want {
			t.Errorf("stderr %q: got %s %q", stderr, code, message)
		}
	}
	code, message := classifyInvestigationFailure("Error: private payload from https://private.example.com")
	if code != "command_failed" || strings.Contains(message, "private") {
		t.Errorf("provider output escaped: %s %q", code, message)
	}
}

func TestAtlasGraphOverBudgetPointsToNarrowerReads(t *testing.T) {
	tool, err := findInvestigationTool(rootCmd, "atlas.graph")
	if err != nil {
		t.Fatal(err)
	}

	failure := toolFailure(tool, map[string]any{"app": "00000000-0000-4000-8000-000000000001"}, "result_too_large", "")

	for _, want := range []string{"atlas.brief", "atlas.area", "limit caps screens but not their relationships"} {
		if !strings.Contains(failure.Message, want) {
			t.Errorf("message %q does not mention %q", failure.Message, want)
		}
	}
}

func TestAtlasLaunchHelpSaysEveryORedValueCountsTowardTheCap(t *testing.T) {
	tool, err := findInvestigationTool(rootCmd, "atlas.graph")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := json.Marshal(tool)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(schema), "up to 8 terms in total, where each ORed value is one term") {
		t.Errorf("launch parameter does not explain the cap: %s", schema)
	}
}
