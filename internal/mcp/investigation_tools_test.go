package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestInvestigateCallsCatalogWithArgumentsOnStdin(t *testing.T) {
	var gotArgs []string
	var gotStdin []byte
	server := &Server{devMode: true, runInvestigation: func(_ context.Context, args []string, stdin []byte) ([]byte, error) {
		gotArgs, gotStdin = args, stdin
		return []byte(`{"contract":"atlas_graph.v1"}`), nil
	}}
	result, output, err := server.handleInvestigate(context.Background(), nil, InvestigateInput{
		Action:    "call",
		Name:      "atlas.graph",
		Arguments: map[string]any{"app": "app-1", "launch": []any{"API_TOKEN=s3cr3t"}},
	})
	if err != nil || result != nil || output.Result["contract"] != "atlas_graph.v1" {
		t.Fatalf("result=%v output=%v err=%v", result, output, err)
	}
	if strings.Join(gotArgs, " ") != "--dev tools call atlas.graph - --max-output-bytes=60000" {
		t.Fatalf("args = %q", gotArgs)
	}
	var arguments map[string]any
	if err := json.Unmarshal(gotStdin, &arguments); err != nil || arguments["app"] != "app-1" {
		t.Fatalf("stdin = %s", gotStdin)
	}
}

func TestInvestigateWrapsListsAndReportsToolFailures(t *testing.T) {
	server := &Server{runInvestigation: func(_ context.Context, args []string, _ []byte) ([]byte, error) {
		if args[1] == "search" {
			return []byte(`[{"name":"atlas.filters"}]`), nil
		}
		return []byte(`{"schema_version":1,"error":{"code":"invalid_arguments"}}`), context.Canceled
	}}
	_, output, err := server.handleInvestigate(context.Background(), nil, InvestigateInput{Action: "search", Query: "filters"})
	if err != nil || len(output.Result["tools"].([]any)) != 1 {
		t.Fatalf("search output=%v err=%v", output, err)
	}
	result, output, err := server.handleInvestigate(context.Background(), nil, InvestigateInput{Action: "call", Name: "atlas.graph"})
	if err != nil || result == nil || !result.IsError || output.Result["error"] == nil {
		t.Fatalf("failure result=%v output=%v err=%v", result, output, err)
	}
	for _, input := range []InvestigateInput{{Action: "call", Name: "--json"}, {Action: "delete"}} {
		if _, _, err := server.handleInvestigate(context.Background(), nil, input); err == nil {
			t.Fatalf("accepted %+v", input)
		}
	}
}
