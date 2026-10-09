package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/toolcatalog"
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
