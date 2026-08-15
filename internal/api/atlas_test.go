package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAtlasQuerySerializesExplicitFalseIncludeVariants(t *testing.T) {
	includeVariants := false
	values := (AtlasQuery{IncludeVariants: &includeVariants}).values()
	if values.Get("include_variants") != "false" {
		t.Fatalf("include_variants = %q", values.Get("include_variants"))
	}
}

func TestAtlasQueryOmitsUnspecifiedIncludeVariants(t *testing.T) {
	values := (AtlasQuery{}).values()
	if values.Has("include_variants") {
		t.Fatalf("unexpected include_variants = %q", values.Get("include_variants"))
	}
}

func TestGetAtlasEdgeRunsPreservesGraphScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		for key, expected := range map[string]string{
			"build_id":              "build-1",
			"report_id":             "report-1",
			"test_id":               "test-1",
			"workflow_execution_id": "workflow-1",
			"source_kind":           "test_report",
			"from_time":             "2026-08-01T00:00:00Z",
			"to_time":               "2026-08-02T00:00:00Z",
			"surface_scope":         "app+system",
			"visibility":            "included+excluded_debug",
			"source":                "source-1",
			"target":                "target-1",
			"action_type":           "tap",
			"action_label":          "Continue",
			"limit":                 "5",
		} {
			if actual := query.Get(key); actual != expected {
				t.Errorf("%s = %q, want %q", key, actual, expected)
			}
		}
		for _, omitted := range []string{"include_variants", "include_details", "include_flows", "include_screenshots"} {
			if query.Has(omitted) {
				t.Errorf("unexpected edge-runs query parameter %s", omitted)
			}
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"app_id":"app-1","runs":[]}`))
	}))
	t.Cleanup(server.Close)

	includeVariants := true
	includeDetails := false
	client := NewClientWithBaseURL("test-key", server.URL)
	_, err := client.GetAtlasEdgeRuns(context.Background(), AtlasQuery{
		AppID:               "app-1",
		BuildID:             "build-1",
		ReportID:            "report-1",
		TestID:              "test-1",
		WorkflowExecutionID: "workflow-1",
		SourceKind:          "test_report",
		FromTime:            "2026-08-01T00:00:00Z",
		ToTime:              "2026-08-02T00:00:00Z",
		SurfaceScope:        "app+system",
		Visibility:          "included+excluded_debug",
		IncludeVariants:     &includeVariants,
		IncludeDetails:      &includeDetails,
		IncludeScreenshots:  true,
		Limit:               840,
	}, "source-1", "target-1", "tap", "Continue", 5)
	if err != nil {
		t.Fatal(err)
	}
}
