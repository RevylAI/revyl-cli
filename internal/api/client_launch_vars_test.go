package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListOrgLaunchVariables(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/variables/org_launch_env" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(OrgLaunchVariablesResponse{
			Message: "ok",
			Result: []OrgLaunchVariable{
				{
					ID:                "11111111-1111-1111-1111-111111111111",
					OrgID:             "22222222-2222-2222-2222-222222222222",
					Key:               "API_URL",
					Value:             "https://staging.example.com",
					Description:       "shared endpoint",
					AttachedTestCount: 3,
				},
			},
		})
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	resp, err := client.ListOrgLaunchVariables(context.Background())
	if err != nil {
		t.Fatalf("ListOrgLaunchVariables() error = %v", err)
	}
	if len(resp.Result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Result))
	}
	if resp.Result[0].Key != "API_URL" {
		t.Fatalf("expected key API_URL, got %q", resp.Result[0].Key)
	}
}

func TestAddOrgLaunchVariable(t *testing.T) {
	var seenBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/variables/org_launch_env" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		_ = json.NewDecoder(r.Body).Decode(&seenBody)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(OrgLaunchVariableResponse{
			Message: "created",
			Result: OrgLaunchVariable{
				ID:          "33333333-3333-3333-3333-333333333333",
				OrgID:       "22222222-2222-2222-2222-222222222222",
				Key:         "DEBUG",
				Value:       "true",
				Description: "toggle debug mode",
			},
		})
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	desc := "toggle debug mode"
	resp, err := client.AddOrgLaunchVariable(context.Background(), "DEBUG", "true", &desc)
	if err != nil {
		t.Fatalf("AddOrgLaunchVariable() error = %v", err)
	}
	if resp.Result.Key != "DEBUG" {
		t.Fatalf("expected key DEBUG, got %q", resp.Result.Key)
	}
	if seenBody["key"] != "DEBUG" {
		t.Fatalf("expected body key=DEBUG, got %v", seenBody["key"])
	}
	if seenBody["value"] != "true" {
		t.Fatalf("expected body value=true, got %v", seenBody["value"])
	}
	if seenBody["description"] != "toggle debug mode" {
		t.Fatalf("expected body description, got %v", seenBody["description"])
	}
}

func TestUpdateOrgLaunchVariable(t *testing.T) {
	var seenBody map[string]interface{}
	var seenPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("expected PUT, got %s", r.Method)
		}
		seenPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&seenBody)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(OrgLaunchVariableResponse{
			Message: "updated",
			Result: OrgLaunchVariable{
				ID:                "11111111-1111-1111-1111-111111111111",
				OrgID:             "22222222-2222-2222-2222-222222222222",
				Key:               "NEW_KEY",
				Value:             "new-value",
				Description:       "updated description",
				AttachedTestCount: 2,
			},
		})
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	newKey := "NEW_KEY"
	newValue := "new-value"
	newDesc := "updated description"
	resp, err := client.UpdateOrgLaunchVariable(
		context.Background(),
		"11111111-1111-1111-1111-111111111111",
		&newKey,
		&newValue,
		&newDesc,
	)
	if err != nil {
		t.Fatalf("UpdateOrgLaunchVariable() error = %v", err)
	}
	if seenPath != "/api/v1/variables/org_launch_env/11111111-1111-1111-1111-111111111111" {
		t.Fatalf("unexpected path: %s", seenPath)
	}
	if seenBody["key"] != "NEW_KEY" {
		t.Fatalf("expected body key NEW_KEY, got %v", seenBody["key"])
	}
	if seenBody["value"] != "new-value" {
		t.Fatalf("expected body value new-value, got %v", seenBody["value"])
	}
	if seenBody["description"] != "updated description" {
		t.Fatalf("expected body description, got %v", seenBody["description"])
	}
	if resp.Result.AttachedTestCount != 2 {
		t.Fatalf("expected attached count 2, got %d", resp.Result.AttachedTestCount)
	}
}

func TestDeleteOrgLaunchVariable(t *testing.T) {
	var seenPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("expected DELETE, got %s", r.Method)
		}
		seenPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"deleted","result":{"id":"uuid-1","org_id":"org-1","key":"API_URL","value":"https://example.com"},"detached_test_count":4}`))
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	resp, err := client.DeleteOrgLaunchVariable(context.Background(), "uuid-1")
	if err != nil {
		t.Fatalf("DeleteOrgLaunchVariable() error = %v", err)
	}
	if seenPath != "/api/v1/variables/org_launch_env/uuid-1" {
		t.Fatalf("unexpected path: %s", seenPath)
	}
	if resp.DetachedTestCount != 4 {
		t.Fatalf("expected detached count 4, got %d", resp.DetachedTestCount)
	}
}

func TestListTestLaunchEnvVarAttachments(t *testing.T) {
	var seenQuery string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/variables/org_launch_env/test-attachments" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		seenQuery = r.URL.RawQuery

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(OrgLaunchVariablesResponse{
			Message: "ok",
			Result: []OrgLaunchVariable{
				{
					ID:    "11111111-1111-1111-1111-111111111111",
					Key:   "API_URL",
					Value: "https://staging.example.com",
				},
			},
		})
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	resp, err := client.ListTestLaunchEnvVarAttachments(context.Background(), "test-123")
	if err != nil {
		t.Fatalf("ListTestLaunchEnvVarAttachments() error = %v", err)
	}
	if seenQuery != "test_id=test-123" {
		t.Fatalf("unexpected query: %s", seenQuery)
	}
	if len(resp.Result) != 1 || resp.Result[0].Key != "API_URL" {
		t.Fatalf("unexpected response: %+v", resp.Result)
	}
}

func TestReplaceTestLaunchEnvVarAttachments(t *testing.T) {
	var seenBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/variables/org_launch_env/test-attachments" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		if err := json.NewDecoder(r.Body).Decode(&seenBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(OrgLaunchVariablesResponse{
			Message: "updated",
			Result: []OrgLaunchVariable{
				{
					ID:    "env-1",
					Key:   "API_URL",
					Value: "https://staging.example.com",
				},
			},
		})
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	resp, err := client.ReplaceTestLaunchEnvVarAttachments(
		context.Background(),
		"test-123",
		[]string{"env-1", "env-2"},
	)
	if err != nil {
		t.Fatalf("ReplaceTestLaunchEnvVarAttachments() error = %v", err)
	}
	if seenBody["test_id"] != "test-123" {
		t.Fatalf("expected test_id test-123, got %v", seenBody["test_id"])
	}
	envIDs, ok := seenBody["env_var_ids"].([]any)
	if !ok || len(envIDs) != 2 || envIDs[0] != "env-1" || envIDs[1] != "env-2" {
		t.Fatalf("unexpected env_var_ids: %#v", seenBody["env_var_ids"])
	}
	if len(resp.Result) != 1 || resp.Result[0].ID != "env-1" {
		t.Fatalf("unexpected response: %+v", resp.Result)
	}
}
