package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestCancelTestDistinguishesAcceptanceFromCancellation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  string
		success bool
		want    string
	}{
		{name: "running", status: "running", success: true, want: "Test cancellation requested"},
		{name: "starting", status: "starting", success: true, want: "Test cancellation requested"},
		{name: "legacy without status", success: true, want: "Test cancellation requested"},
		{name: "cancelled", status: "cancelled", success: true, want: "Test cancelled successfully"},
		{name: "rejected", status: "completed", success: false, want: "Could not cancel test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := api.CancelTestResponse{Success: tc.success, Message: "Cancellation response", TaskId: "task-1"}
			if tc.status != "" {
				response.Status = &tc.status
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/execution/tests/status/cancel/task-1" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			t.Setenv("REVYL_API_KEY", "test-api-key")
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			cmd := newLeafCommand("cancel", runCancelTest)
			var runErr error
			output := captureStdoutAndStderr(t, func() { runErr = runCancelTest(cmd, []string{"task-1"}) })
			if (runErr == nil) != tc.success {
				t.Fatalf("error = %v, success = %v", runErr, tc.success)
			}
			if !strings.Contains(output, tc.want) {
				t.Fatalf("output = %q, want %q", output, tc.want)
			}
			if tc.status != "cancelled" && strings.Contains(output, "Test cancelled successfully") {
				t.Fatalf("unconfirmed cancellation reported: %q", output)
			}
		})
	}
}

func TestCancelWorkflowDoesNotClaimChildSettlement(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"message":"Workflow cancellation requested","task_id":"task-1"}`))
	}))
	defer server.Close()
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	cmd := newLeafCommand("cancel", runCancelWorkflow)
	var runErr error
	output := captureStdoutAndStderr(t, func() { runErr = runCancelWorkflow(cmd, []string{"task-1"}) })
	if runErr != nil {
		t.Fatal(runErr)
	}
	if !strings.Contains(output, "Workflow cancellation requested") || strings.Contains(output, "have been cancelled") {
		t.Fatalf("unexpected cancellation acknowledgement: %q", output)
	}
}
