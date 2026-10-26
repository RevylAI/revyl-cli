package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestPaymentRequiredErrorShowsTheServerReason(t *testing.T) {
	capReached := "This organization reached its usage cap for the current billing period, so new runs, device sessions, and builds are paused until the period resets on November 16, 2026 at 23:10 UTC. Contact Revyl to change the cap."
	body, err := json.Marshal(map[string]string{"detail": capReached})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	message := parseAPIErrorBody(http.StatusPaymentRequired, body).Error()

	if !strings.Contains(message, capReached) {
		t.Fatalf("error = %q, want the server's reason", message)
	}
	if strings.Contains(message, "unpaid invoices") {
		t.Fatalf("error = %q, want no generic billing copy beside the server's reason", message)
	}
	if !strings.HasSuffix(message, "→ revyl auth billing") {
		t.Fatalf("error = %q, want it to point to revyl auth billing", message)
	}
}

func TestPaymentRequiredErrorWithoutAReasonExplainsWhatToReview(t *testing.T) {
	for _, body := range []string{"", "{}", `{"detail":null}`} {
		apiErr := parseAPIErrorBody(http.StatusPaymentRequired, []byte(body))
		if !strings.Contains(apiErr.Hint, "Review your plan, available credits, and unpaid invoices") {
			t.Fatalf("body %q: hint = %q, want the generic billing prompt", body, apiErr.Hint)
		}
	}
}
