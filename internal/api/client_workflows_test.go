package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestListAllWorkflowsUsesCatalogPaginationMetadata(t *testing.T) {
	var requestedOffsets []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workflows/get_with_last_status" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("limit"); got != "2" {
			t.Fatalf("limit = %q, want 2", got)
		}
		if got := r.URL.Query().Get("history_limit"); got != "1" {
			t.Fatalf("history_limit = %q, want 1", got)
		}

		offset := r.URL.Query().Get("offset")
		requestedOffsets = append(requestedOffsets, offset)
		w.Header().Set("Content-Type", "application/json")
		switch offset {
		case "0":
			_, _ = w.Write([]byte(`{
				"data": [
					{"id":"11111111-1111-4111-8111-111111111111","name":"alpha","test_count":1},
					{"id":"22222222-2222-4222-8222-222222222222","name":"beta","test_count":2}
				],
				"count": 2,
				"limit": 2,
				"offset": 0,
				"total_count": 3,
				"has_more": true
			}`))
		case "2":
			_, _ = w.Write([]byte(`{
				"data": [
					{"id":"33333333-3333-4333-8333-333333333333","name":"gamma","test_count":3}
				],
				"count": 1,
				"limit": 2,
				"offset": 2,
				"total_count": 3,
				"has_more": false
			}`))
		default:
			t.Fatalf("unexpected offset: %s", offset)
		}
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	workflows, err := client.ListAllWorkflows(context.Background(), 2)
	if err != nil {
		t.Fatalf("ListAllWorkflows() error = %v", err)
	}
	if got, want := requestedOffsets, []string{"0", "2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("requested offsets = %v, want %v", got, want)
	}
	if len(workflows) != 3 {
		t.Fatalf("workflow count = %d, want 3", len(workflows))
	}
}

func TestListWorkflowsBoundedRejectsReportedCatalogAboveLimit(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [{"id":"11111111-1111-4111-8111-111111111111","name":"alpha","test_count":1}],
			"count": 1,
			"total_count": 101,
			"has_more": true
		}`))
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	_, err := client.ListWorkflowsBounded(context.Background(), 50, 100)
	if err == nil {
		t.Fatal("expected catalog limit error")
	}
	var limitErr *WorkflowCatalogLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("error type = %T, want *WorkflowCatalogLimitError", err)
	}
	if limitErr.MaxWorkflows != 100 || limitErr.TotalCount == nil || *limitErr.TotalCount != 101 {
		t.Fatalf("limit error = %#v", limitErr)
	}
	if requestCount != 1 {
		t.Fatalf("request count = %d, want 1", requestCount)
	}
}

func TestListWorkflowsBoundedRejectsUnreportedOverflow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("offset") {
		case "0":
			_, _ = w.Write([]byte(`{
				"data": [
					{"id":"11111111-1111-4111-8111-111111111111","name":"alpha","test_count":1},
					{"id":"22222222-2222-4222-8222-222222222222","name":"beta","test_count":1}
				],
				"count": 2,
				"has_more": true
			}`))
		default:
			t.Fatalf("bounded client should reject at the cap before requesting another page")
		}
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	_, err := client.ListWorkflowsBounded(context.Background(), 2, 2)
	var limitErr *WorkflowCatalogLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("error = %v (%T), want *WorkflowCatalogLimitError", err, err)
	}
}
