package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestResolveWorkflowIDMCPSearchesAllPages(t *testing.T) {
	var requestedOffsets []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workflows/get_with_last_status" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		offset := r.URL.Query().Get("offset")
		requestedOffsets = append(requestedOffsets, offset)
		w.Header().Set("Content-Type", "application/json")
		switch offset {
		case "0":
			workflows := make([]api.SimpleWorkflow, 200)
			for i := range workflows {
				workflows[i] = api.SimpleWorkflow{ID: fmt.Sprintf("wf-%03d", i), Name: fmt.Sprintf("dummy-%03d", i)}
			}
			_ = json.NewEncoder(w).Encode(api.CLIWorkflowListResponse{Workflows: workflows, Count: 201})
		case "200":
			_ = json.NewEncoder(w).Encode(api.CLIWorkflowListResponse{
				Workflows: []api.SimpleWorkflow{{ID: "wf-target", Name: "late-workflow"}},
				Count:     201,
			})
		default:
			t.Fatalf("unexpected offset: %s", offset)
		}
	}))
	defer server.Close()

	s := &Server{apiClient: api.NewClientWithBaseURL("token", server.URL)}
	workflowID, err := s.resolveWorkflowID(context.Background(), "late-workflow")
	if err != nil {
		t.Fatalf("resolveWorkflowID() error = %v", err)
	}
	if workflowID != "wf-target" {
		t.Fatalf("workflowID = %q, want wf-target", workflowID)
	}
	if got := strings.Join(requestedOffsets, ","); got != "0,200" {
		t.Fatalf("requested offsets = %s, want 0,200", got)
	}
}

func TestResolveWorkflowIDMCPRejectsDuplicateNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workflows/get_with_last_status" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.CLIWorkflowListResponse{
			Workflows: []api.SimpleWorkflow{
				{ID: "wf-b", Name: "nightly"},
				{ID: "wf-a", Name: "nightly"},
			},
			Count: 2,
		})
	}))
	defer server.Close()

	s := &Server{apiClient: api.NewClientWithBaseURL("token", server.URL)}
	_, err := s.resolveWorkflowID(context.Background(), "nightly")
	if err == nil {
		t.Fatal("expected duplicate workflow error")
	}
	if !strings.Contains(err.Error(), "multiple workflows named") {
		t.Fatalf("error = %v", err)
	}
}
