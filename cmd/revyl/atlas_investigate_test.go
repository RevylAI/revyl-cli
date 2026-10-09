package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestInvestigationReportProjectionPreservesUnmappedAndPartial(t *testing.T) {
	const appID = "00000000-0000-4000-8000-000000000001"
	const reportA = "00000000-0000-4000-8000-000000000002"
	const reportB = "00000000-0000-4000-8000-000000000003"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/atlas/v2/apps/"+appID+"/graph" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		q := r.URL.Query()
		if q.Get("limit") != "10" || q.Get("include_variants") != "true" || q.Get("include_details") != "false" {
			t.Errorf("lost query scope: %v", q)
		}
		if q.Get("report_id") == reportA {
			json.NewEncoder(w).Encode(map[string]any{"nodes": []any{map[string]any{"id": "screen-a"}}, "edges": []any{}, "projection": map[string]any{"truncated": true}})
			return
		}
		if q.Get("report_id") != reportB {
			t.Errorf("unexpected report scope")
		}
		json.NewEncoder(w).Encode(map[string]any{"nodes": []any{}, "edges": []any{}, "projection": map[string]any{"truncated": false}})
	}))
	defer server.Close()
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	command := newAtlasReportsCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"--app", appID, "--reports", reportA + "," + reportB, "--limit", "10"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Reports []atlasReportMembership `json:"reports"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Reports) != 2 || result.Reports[0].Mapping != "partial" || result.Reports[1].Mapping != "no_matching_atlas_evidence" || result.Reports[1].ReportID != reportB {
		t.Fatalf("lost report provenance: %+v", result)
	}
}

func TestInvestigationGraphFailureNeverBecomesEmptyEvidence(t *testing.T) {
	for _, body := range []string{`{}`, `{"nodes":null}`, `{"nodes":"wrong"}`, `{"nodes":[null],"edges":[]}`, `{"nodes":[],"edges":["invalid"]}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		client := api.NewClientWithBaseURL("fixture-key", server.URL)
		if _, err := readInvestigationGraph(context.Background(), client, "app", "build", "", 10); err == nil {
			t.Error("invalid graph became empty evidence")
		}
		server.Close()
	}
}

func TestInvestigationBuildComparisonPreservesEvidenceSemantics(t *testing.T) {
	base := []map[string]interface{}{{"id": "shared"}, {"id": "base-only"}}
	head := []map[string]interface{}{{"id": "shared"}, {"id": "head-only"}}
	result := compareAtlasIDs(base, head, "id")
	if strings.Join(result.Both, ",") != "shared" || strings.Join(result.BaseOnly, ",") != "base-only" || strings.Join(result.HeadOnly, ",") != "head-only" {
		t.Fatalf("incorrect comparison: %+v", result)
	}
}
