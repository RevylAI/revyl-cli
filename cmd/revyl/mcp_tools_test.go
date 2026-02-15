// Package main provides tests for MCP tool additions:
// - Script API client methods (CRUD + usage)
// - upload_build and update_test API endpoint verification
// - MCP server tool registration count
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

// --- Mock server for script and build endpoints ---

// newScriptMockServer creates a mock server that handles script, build upload,
// and test update endpoints.
func newScriptMockServer(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	// GET /api/v1/tests/scripts
	mux.HandleFunc("/api/v1/tests/scripts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case "GET":
			// Check for script ID in path (e.g. /api/v1/tests/scripts/script-001)
			// This handler catches the base path; specific IDs are handled below
			runtime := r.URL.Query().Get("runtime")
			scripts := []map[string]interface{}{
				{
					"id":          "script-001",
					"name":        "setup-data",
					"code":        "print('hello')",
					"runtime":     "python",
					"description": "Seeds test data",
					"created_at":  "2025-01-15T10:00:00Z",
					"updated_at":  "2025-01-15T12:00:00Z",
				},
				{
					"id":          "script-002",
					"name":        "cleanup",
					"code":        "console.log('done')",
					"runtime":     "javascript",
					"description": nil,
					"created_at":  "2025-01-14T10:00:00Z",
					"updated_at":  "2025-01-14T10:00:00Z",
				},
			}

			// Filter by runtime if specified
			if runtime != "" {
				var filtered []map[string]interface{}
				for _, s := range scripts {
					if s["runtime"] == runtime {
						filtered = append(filtered, s)
					}
				}
				scripts = filtered
			}

			json.NewEncoder(w).Encode(map[string]interface{}{
				"scripts": scripts,
				"count":   len(scripts),
			})

		case "POST":
			// Create script
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "script-new",
				"name":        body["name"],
				"code":        body["code"],
				"runtime":     body["runtime"],
				"description": body["description"],
				"created_at":  "2025-01-16T10:00:00Z",
				"updated_at":  "2025-01-16T10:00:00Z",
			})
		}
	})

	// GET/PUT/DELETE /api/v1/tests/scripts/{script_id}
	mux.HandleFunc("/api/v1/tests/scripts/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Extract script ID from path
		parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")
		scriptID := parts[len(parts)-1]

		// Handle /scripts/{id}/tests (usage endpoint)
		if scriptID == "tests" && len(parts) >= 2 {
			parentID := parts[len(parts)-2]
			json.NewEncoder(w).Encode(map[string]interface{}{
				"tests": []map[string]interface{}{
					{"id": "test-001", "name": "Login Flow"},
					{"id": "test-002", "name": "Checkout"},
				},
				"total": 2,
			})
			_ = parentID
			return
		}

		switch r.Method {
		case "GET":
			if scriptID == "not-found" {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"detail": "Script not found",
				})
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          scriptID,
				"name":        "setup-data",
				"code":        "print('hello')",
				"runtime":     "python",
				"description": "Seeds test data",
				"created_at":  "2025-01-15T10:00:00Z",
				"updated_at":  "2025-01-15T12:00:00Z",
			})

		case "PUT":
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)

			// Return updated script
			resp := map[string]interface{}{
				"id":          scriptID,
				"name":        "setup-data",
				"code":        "print('hello')",
				"runtime":     "python",
				"description": "Seeds test data",
				"created_at":  "2025-01-15T10:00:00Z",
				"updated_at":  "2025-01-16T10:00:00Z",
			}
			// Apply updates from body
			if name, ok := body["name"]; ok && name != nil {
				resp["name"] = name
			}
			if code, ok := body["code"]; ok && code != nil {
				resp["code"] = code
			}
			json.NewEncoder(w).Encode(resp)

		case "DELETE":
			w.WriteHeader(http.StatusNoContent)
		}
	})

	// PUT /api/v1/tests/update/{test_id}
	mux.HandleFunc("/api/v1/tests/update/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")
		testID := parts[len(parts)-1]
		w.Header().Set("Content-Type", "application/json")

		if testID == "conflict-test" {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"detail": "Version conflict",
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      testID,
			"version": 2,
		})
	})

	return httptest.NewServer(mux)
}

// --- Script API client tests ---

func TestListScripts(t *testing.T) {
	server := newScriptMockServer(t)
	defer server.Close()
	client := api.NewClientWithBaseURL("test-key", server.URL)
	ctx := context.Background()

	resp, err := client.ListScripts(ctx, "", 100, 0)
	if err != nil {
		t.Fatalf("ListScripts: %v", err)
	}
	if len(resp.Scripts) != 2 {
		t.Errorf("ListScripts: expected 2 scripts, got %d", len(resp.Scripts))
	}
	if resp.Scripts[0].Name != "setup-data" {
		t.Errorf("ListScripts: expected first script name 'setup-data', got %q", resp.Scripts[0].Name)
	}
	if resp.Scripts[0].Runtime != "python" {
		t.Errorf("ListScripts: expected runtime 'python', got %q", resp.Scripts[0].Runtime)
	}
}

func TestListScriptsFilterByRuntime(t *testing.T) {
	server := newScriptMockServer(t)
	defer server.Close()
	client := api.NewClientWithBaseURL("test-key", server.URL)
	ctx := context.Background()

	resp, err := client.ListScripts(ctx, "python", 100, 0)
	if err != nil {
		t.Fatalf("ListScripts with runtime filter: %v", err)
	}
	if len(resp.Scripts) != 1 {
		t.Errorf("ListScripts filtered: expected 1 script, got %d", len(resp.Scripts))
	}
	if len(resp.Scripts) > 0 && resp.Scripts[0].Runtime != "python" {
		t.Errorf("ListScripts filtered: expected runtime 'python', got %q", resp.Scripts[0].Runtime)
	}
}

func TestGetScript(t *testing.T) {
	server := newScriptMockServer(t)
	defer server.Close()
	client := api.NewClientWithBaseURL("test-key", server.URL)
	ctx := context.Background()

	resp, err := client.GetScript(ctx, "script-001")
	if err != nil {
		t.Fatalf("GetScript: %v", err)
	}
	if resp.ID != "script-001" {
		t.Errorf("GetScript: expected ID 'script-001', got %q", resp.ID)
	}
	if resp.Name != "setup-data" {
		t.Errorf("GetScript: expected name 'setup-data', got %q", resp.Name)
	}
	if resp.Code != "print('hello')" {
		t.Errorf("GetScript: expected code \"print('hello')\", got %q", resp.Code)
	}
}

func TestGetScriptNotFound(t *testing.T) {
	server := newScriptMockServer(t)
	defer server.Close()
	client := api.NewClientWithBaseURL("test-key", server.URL)
	ctx := context.Background()

	_, err := client.GetScript(ctx, "not-found")
	if err == nil {
		t.Fatal("GetScript not-found: expected error, got nil")
	}
}

func TestCreateScript(t *testing.T) {
	server := newScriptMockServer(t)
	defer server.Close()
	client := api.NewClientWithBaseURL("test-key", server.URL)
	ctx := context.Background()

	desc := "A new script"
	resp, err := client.CreateScript(ctx, &api.CLICreateScriptRequest{
		Name:        "new-script",
		Code:        "echo 'hello'",
		Runtime:     "bash",
		Description: &desc,
	})
	if err != nil {
		t.Fatalf("CreateScript: %v", err)
	}
	if resp.ID != "script-new" {
		t.Errorf("CreateScript: expected ID 'script-new', got %q", resp.ID)
	}
	if resp.Name != "new-script" {
		t.Errorf("CreateScript: expected name 'new-script', got %q", resp.Name)
	}
}

func TestUpdateScript(t *testing.T) {
	server := newScriptMockServer(t)
	defer server.Close()
	client := api.NewClientWithBaseURL("test-key", server.URL)
	ctx := context.Background()

	newName := "updated-name"
	resp, err := client.UpdateScript(ctx, "script-001", &api.CLIUpdateScriptRequest{
		Name: &newName,
	})
	if err != nil {
		t.Fatalf("UpdateScript: %v", err)
	}
	if resp.ID != "script-001" {
		t.Errorf("UpdateScript: expected ID 'script-001', got %q", resp.ID)
	}
	if resp.Name != "updated-name" {
		t.Errorf("UpdateScript: expected name 'updated-name', got %q", resp.Name)
	}
}

func TestDeleteScript(t *testing.T) {
	server := newScriptMockServer(t)
	defer server.Close()
	client := api.NewClientWithBaseURL("test-key", server.URL)
	ctx := context.Background()

	err := client.DeleteScript(ctx, "script-001")
	if err != nil {
		t.Fatalf("DeleteScript: %v", err)
	}
}

func TestGetScriptUsage(t *testing.T) {
	server := newScriptMockServer(t)
	defer server.Close()
	client := api.NewClientWithBaseURL("test-key", server.URL)
	ctx := context.Background()

	resp, err := client.GetScriptUsage(ctx, "script-001")
	if err != nil {
		t.Fatalf("GetScriptUsage: %v", err)
	}
	if resp.Total != 2 {
		t.Errorf("GetScriptUsage: expected total 2, got %d", resp.Total)
	}
	if len(resp.Tests) != 2 {
		t.Errorf("GetScriptUsage: expected 2 tests, got %d", len(resp.Tests))
	}
}

// --- UpdateTest API client tests ---

func TestUpdateTest(t *testing.T) {
	server := newScriptMockServer(t)
	defer server.Close()
	client := api.NewClientWithBaseURL("test-key", server.URL)
	ctx := context.Background()

	resp, err := client.UpdateTest(ctx, &api.UpdateTestRequest{
		TestID: "test-uuid-001",
		Tasks:  []interface{}{map[string]string{"type": "instructions", "step_description": "Tap login"}},
	})
	if err != nil {
		t.Fatalf("UpdateTest: %v", err)
	}
	if resp.ID != "test-uuid-001" {
		t.Errorf("UpdateTest: expected ID 'test-uuid-001', got %q", resp.ID)
	}
	if resp.Version != 2 {
		t.Errorf("UpdateTest: expected version 2, got %d", resp.Version)
	}
}

func TestUpdateTestConflict(t *testing.T) {
	server := newScriptMockServer(t)
	defer server.Close()
	client := api.NewClientWithBaseURL("test-key", server.URL)
	ctx := context.Background()

	_, err := client.UpdateTest(ctx, &api.UpdateTestRequest{
		TestID: "conflict-test",
		Tasks:  []interface{}{},
	})
	if err == nil {
		t.Fatal("UpdateTest conflict: expected error, got nil")
	}
	apiErr, ok := err.(*api.APIError)
	if !ok {
		t.Fatalf("UpdateTest conflict: expected *api.APIError, got %T", err)
	}
	if apiErr.StatusCode != 409 {
		t.Errorf("UpdateTest conflict: expected status 409, got %d", apiErr.StatusCode)
	}
}

// --- HTTP method verification tests ---

func TestScriptListEndpointMethod(t *testing.T) {
	var receivedMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/tests/scripts") && !strings.Contains(r.URL.Path, "/scripts/") {
			receivedMethod = r.Method
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"scripts":[],"count":0}`)
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	_, _ = client.ListScripts(context.Background(), "", 100, 0)

	if receivedMethod != "GET" {
		t.Errorf("expected GET method for ListScripts, got %q", receivedMethod)
	}
}

func TestScriptCreateEndpointMethod(t *testing.T) {
	var receivedMethod string
	var receivedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":"x","name":"x","code":"x","runtime":"python","created_at":"","updated_at":""}`)
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	_, _ = client.CreateScript(context.Background(), &api.CLICreateScriptRequest{
		Name:    "test",
		Code:    "x",
		Runtime: "python",
	})

	if receivedMethod != "POST" {
		t.Errorf("expected POST method for CreateScript, got %q", receivedMethod)
	}
	if receivedPath != "/api/v1/tests/scripts" {
		t.Errorf("expected path '/api/v1/tests/scripts', got %q", receivedPath)
	}
}

func TestScriptUpdateEndpointMethod(t *testing.T) {
	var receivedMethod string
	var receivedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"script-001","name":"x","code":"x","runtime":"python","created_at":"","updated_at":""}`)
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	name := "updated"
	_, _ = client.UpdateScript(context.Background(), "script-001", &api.CLIUpdateScriptRequest{
		Name: &name,
	})

	if receivedMethod != "PUT" {
		t.Errorf("expected PUT method for UpdateScript, got %q", receivedMethod)
	}
	if receivedPath != "/api/v1/tests/scripts/script-001" {
		t.Errorf("expected path '/api/v1/tests/scripts/script-001', got %q", receivedPath)
	}
}

func TestScriptDeleteEndpointMethod(t *testing.T) {
	var receivedMethod string
	var receivedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	_ = client.DeleteScript(context.Background(), "script-001")

	if receivedMethod != "DELETE" {
		t.Errorf("expected DELETE method for DeleteScript, got %q", receivedMethod)
	}
	if receivedPath != "/api/v1/tests/scripts/script-001" {
		t.Errorf("expected path '/api/v1/tests/scripts/script-001', got %q", receivedPath)
	}
}

func TestScriptGetUsageEndpointMethod(t *testing.T) {
	var receivedMethod string
	var receivedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"tests":[],"total":0}`)
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	_, _ = client.GetScriptUsage(context.Background(), "script-001")

	if receivedMethod != "GET" {
		t.Errorf("expected GET method for GetScriptUsage, got %q", receivedMethod)
	}
	if receivedPath != "/api/v1/tests/scripts/script-001/tests" {
		t.Errorf("expected path '/api/v1/tests/scripts/script-001/tests', got %q", receivedPath)
	}
}

func TestUpdateTestEndpointMethod(t *testing.T) {
	var receivedMethod string
	var receivedPath string
	var receivedBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"test-001","version":3}`)
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	_, _ = client.UpdateTest(context.Background(), &api.UpdateTestRequest{
		TestID: "test-001",
		Tasks:  []interface{}{map[string]string{"type": "instructions"}},
		AppID:  "app-001",
	})

	if receivedMethod != "PUT" {
		t.Errorf("expected PUT method for UpdateTest, got %q", receivedMethod)
	}
	if receivedPath != "/api/v1/tests/update/test-001" {
		t.Errorf("expected path '/api/v1/tests/update/test-001', got %q", receivedPath)
	}
	// Verify body contains tasks and app_id
	if receivedBody["tasks"] == nil {
		t.Error("expected 'tasks' in request body")
	}
	if receivedBody["app_id"] != "app-001" {
		t.Errorf("expected app_id 'app-001', got %v", receivedBody["app_id"])
	}
}

// --- ListScripts query parameter tests ---

func TestListScriptsQueryParams(t *testing.T) {
	var receivedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"scripts":[],"count":0}`)
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)

	// With all params
	_, _ = client.ListScripts(context.Background(), "python", 50, 10)

	if !strings.Contains(receivedQuery, "runtime=python") {
		t.Errorf("expected query to contain 'runtime=python', got %q", receivedQuery)
	}
	if !strings.Contains(receivedQuery, "limit=50") {
		t.Errorf("expected query to contain 'limit=50', got %q", receivedQuery)
	}
	if !strings.Contains(receivedQuery, "offset=10") {
		t.Errorf("expected query to contain 'offset=10', got %q", receivedQuery)
	}
}

func TestListScriptsNoFilters(t *testing.T) {
	var receivedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"scripts":[],"count":0}`)
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)

	// With no filters (empty runtime, zero limit/offset)
	_, _ = client.ListScripts(context.Background(), "", 0, 0)

	// Should have empty query string (no runtime, limit, or offset)
	if receivedQuery != "" {
		t.Errorf("expected empty query string with no filters, got %q", receivedQuery)
	}
}
