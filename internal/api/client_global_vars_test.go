package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListGlobalVariables(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/variables/global" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}

		w.Header().Set("Content-Type", "application/json")
		resp := GlobalVariablesResponse{
			Message: "ok",
			Result: []GlobalVariable{
				{
					ID:            "uuid-1",
					OrgID:         "org-1",
					VariableName:  "login-email",
					VariableValue: strPtr("user@test.com"),
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	resp, err := client.ListGlobalVariables(context.Background())
	if err != nil {
		t.Fatalf("ListGlobalVariables() error = %v", err)
	}
	if len(resp.Result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Result))
	}
	if resp.Result[0].VariableName != "login-email" {
		t.Fatalf("expected variable name 'login-email', got %q", resp.Result[0].VariableName)
	}
}

func TestAddGlobalVariable(t *testing.T) {
	var seenBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/variables/global" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		json.NewDecoder(r.Body).Decode(&seenBody)

		w.Header().Set("Content-Type", "application/json")
		resp := GlobalVariableResponse{
			Message: "created",
			Result: GlobalVariable{
				ID:            "uuid-new",
				OrgID:         "org-1",
				VariableName:  "my-var",
				VariableValue: strPtr("my-value"),
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	resp, err := client.AddGlobalVariable(context.Background(), "my-var", "my-value")
	if err != nil {
		t.Fatalf("AddGlobalVariable() error = %v", err)
	}
	if resp.Result.VariableName != "my-var" {
		t.Fatalf("expected 'my-var', got %q", resp.Result.VariableName)
	}
	if seenBody["variable_name"] != "my-var" {
		t.Fatalf("expected body variable_name='my-var', got %v", seenBody["variable_name"])
	}
	if seenBody["variable_value"] != "my-value" {
		t.Fatalf("expected body variable_value='my-value', got %v", seenBody["variable_value"])
	}
}

func TestUpdateGlobalVariable(t *testing.T) {
	var seenBody map[string]interface{}
	var seenPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("expected PUT, got %s", r.Method)
		}
		seenPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&seenBody)

		w.Header().Set("Content-Type", "application/json")
		resp := GlobalVariableResponse{
			Message: "updated",
			Result: GlobalVariable{
				ID:            "uuid-1",
				OrgID:         "org-1",
				VariableName:  "my-var",
				VariableValue: strPtr("new-value"),
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	resp, err := client.UpdateGlobalVariable(context.Background(), "uuid-1", "my-var", "new-value")
	if err != nil {
		t.Fatalf("UpdateGlobalVariable() error = %v", err)
	}
	if seenPath != "/api/v1/variables/global/uuid-1" {
		t.Fatalf("unexpected path: %s", seenPath)
	}
	if seenBody["variable_value"] != "new-value" {
		t.Fatalf("expected body variable_value='new-value', got %v", seenBody["variable_value"])
	}
	if resp.Result.VariableName != "my-var" {
		t.Fatalf("expected 'my-var', got %q", resp.Result.VariableName)
	}
}

func TestDeleteGlobalVariable(t *testing.T) {
	var seenPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("expected DELETE, got %s", r.Method)
		}
		seenPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"message":"deleted"}`))
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	err := client.DeleteGlobalVariable(context.Background(), "uuid-1")
	if err != nil {
		t.Fatalf("DeleteGlobalVariable() error = %v", err)
	}
	if seenPath != "/api/v1/variables/global/uuid-1" {
		t.Fatalf("unexpected path: %s", seenPath)
	}
}

func TestAddGlobalVariableDuplicateReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(409)
		w.Write([]byte(`{"detail":"A global variable with the name 'my-var' already exists."}`))
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	_, err := client.AddGlobalVariable(context.Background(), "my-var", "value")
	if err == nil {
		t.Fatal("expected error on 409, got nil")
	}
}

func strPtr(s string) *string {
	return &s
}
