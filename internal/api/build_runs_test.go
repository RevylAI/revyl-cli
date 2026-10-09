package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetBuildRunsPreservesPaginationAndUnknownOutcome(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/apps/builds/build-id/runs" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("page_size") != "5" {
			t.Errorf("incorrect scope: %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"items":[{"execution_id":"execution-id","success":null,"status":"cancelled"}],"total":14,"page":2,"page_size":5,"total_pages":3,"has_next":true,"has_previous":true}`))
	}))
	defer server.Close()
	result, err := NewClientWithBaseURL("fixture-key", server.URL).GetBuildRuns(context.Background(), "build-id", 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	if result.Page != 2 || !result.HasNext || len(result.Items) != 1 || result.Items[0].Success != nil {
		t.Fatalf("lost outcome or pagination: %+v", result)
	}
}
