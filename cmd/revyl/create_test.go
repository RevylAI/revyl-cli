package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/testutil"
)

func TestCreateRemoteTest_UsesProjectOrgIDAndPreservesRequestFields(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())

	tasks := []interface{}{
		map[string]interface{}{
			"type":             "module_import",
			"step_description": "Login module",
			"module_id":        "mod-1",
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/tests/create":
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode create request: %v", err)
			}

			if got := req["org_id"]; got != "org-config" {
				t.Fatalf("org_id = %v, want org-config", got)
			}
			if got := req["app_id"]; got != "app-123" {
				t.Fatalf("app_id = %v, want app-123", got)
			}
			taskList, ok := req["tasks"].([]any)
			if !ok || len(taskList) != 1 {
				t.Fatalf("tasks = %#v, want single task", req["tasks"])
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"test-1","version":1}`))
		case "/api/v1/entity/users/get_user_uuid":
			t.Fatalf("unexpected validate call")
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := api.NewClientWithBaseURL("test-key", srv.URL)
	cfg := &config.ProjectConfig{
		Project: config.Project{OrgID: "org-config"},
	}

	resp, err := createRemoteTest(context.Background(), client, cfg, "dfa", "ios", tasks, "app-123")
	if err != nil {
		t.Fatalf("createRemoteTest() error = %v", err)
	}
	if resp.ID != "test-1" {
		t.Fatalf("ID = %q, want test-1", resp.ID)
	}
}

func TestCreateRemoteTest_FallsBackToValidatedOrgIDAndNormalizesEmptyTasks(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())

	validateCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/entity/users/get_user_uuid":
			validateCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-live","email":"test@example.com","concurrency_limit":1}`))
		case "/api/v1/tests/create":
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			if got := req["org_id"]; got != "org-live" {
				t.Fatalf("org_id = %v, want org-live", got)
			}
			taskList, ok := req["tasks"].([]any)
			if !ok || len(taskList) != 0 {
				t.Fatalf("tasks = %#v, want empty list", req["tasks"])
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"test-2","version":1}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := api.NewClientWithBaseURL("test-key", srv.URL)

	resp, err := createRemoteTest(context.Background(), client, &config.ProjectConfig{}, "dfa", "android", nil, "")
	if err != nil {
		t.Fatalf("createRemoteTest() error = %v", err)
	}
	if resp.ID != "test-2" {
		t.Fatalf("ID = %q, want test-2", resp.ID)
	}
	if validateCalls != 1 {
		t.Fatalf("validate calls = %d, want 1", validateCalls)
	}
}
