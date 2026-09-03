package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetBillingPlanUsesReadOnlyEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/execution/billing/plan" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(BillingPlanResponse{Plan: "none"}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	plan, err := client.GetBillingPlan(context.Background())
	if err != nil {
		t.Fatalf("GetBillingPlan() error = %v", err)
	}
	if plan.Plan != "none" {
		t.Fatalf("GetBillingPlan() plan = %q, want none", plan.Plan)
	}
}
