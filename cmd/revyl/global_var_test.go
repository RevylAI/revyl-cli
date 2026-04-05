package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/revyl/cli/internal/api"
)

// newGlobalVarMockServer creates a test server handling global variable endpoints.
func newGlobalVarMockServer(t *testing.T, vars []api.GlobalVariableRow) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/variables/global", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(api.GlobalVariablesResponse{
				Message: "ok",
				Result:  vars,
			})
		case http.MethodPost:
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)

			name, _ := body["variable_name"].(string)
			// Check for duplicate
			for _, v := range vars {
				if v.VariableName == name {
					w.WriteHeader(409)
					json.NewEncoder(w).Encode(map[string]string{
						"detail": "A global variable with the name '" + name + "' already exists.",
					})
					return
				}
			}

			val, _ := body["variable_value"].(string)
			json.NewEncoder(w).Encode(api.GlobalVariableResponse{
				Message: "created",
				Result:  newGlobalVariableRow(t, "33333333-3333-3333-3333-333333333333", name, &val),
			})
		default:
			w.WriteHeader(405)
		}
	})

	// Handle PUT/DELETE with variable_id in path
	mux.HandleFunc("/api/v1/variables/global/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodPut:
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)
			name, _ := body["variable_name"].(string)
			val, _ := body["variable_value"].(string)
			json.NewEncoder(w).Encode(api.GlobalVariableResponse{
				Message: "updated",
				Result:  newGlobalVariableRow(t, "11111111-1111-1111-1111-111111111111", name, &val),
			})
		case http.MethodDelete:
			json.NewEncoder(w).Encode(map[string]string{"message": "deleted"})
		default:
			w.WriteHeader(405)
		}
	})

	return httptest.NewServer(mux)
}

func TestGlobalVarSetCreatesNew(t *testing.T) {
	server := newGlobalVarMockServer(t, nil)
	t.Cleanup(server.Close)

	client := api.NewClientWithBaseURL("test-key", server.URL)

	// The set command should call POST since no existing var matches
	resp, err := client.ListGlobalVariables(t.Context())
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if len(resp.Result) != 0 {
		t.Fatalf("expected 0 vars, got %d", len(resp.Result))
	}

	addResp, err := client.AddGlobalVariable(t.Context(), "new-var", "new-value")
	if err != nil {
		t.Fatalf("add error: %v", err)
	}
	if addResp.Result.VariableName != "new-var" {
		t.Fatalf("expected 'new-var', got %q", addResp.Result.VariableName)
	}
}

func TestGlobalVarSetUpdatesExisting(t *testing.T) {
	existingVal := "old-value"
	server := newGlobalVarMockServer(t, []api.GlobalVariableRow{
		newGlobalVariableRow(t, "11111111-1111-1111-1111-111111111111", "my-var", &existingVal),
	})
	t.Cleanup(server.Close)

	client := api.NewClientWithBaseURL("test-key", server.URL)

	// List should find the existing var
	resp, err := client.ListGlobalVariables(t.Context())
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if len(resp.Result) != 1 {
		t.Fatalf("expected 1 var, got %d", len(resp.Result))
	}

	// Update should succeed
	updateResp, err := client.UpdateGlobalVariable(t.Context(), "11111111-1111-1111-1111-111111111111", "my-var", "new-value")
	if err != nil {
		t.Fatalf("update error: %v", err)
	}
	if *updateResp.Result.VariableValue != "new-value" {
		t.Fatalf("expected 'new-value', got %q", *updateResp.Result.VariableValue)
	}
}

func TestGlobalVarDeleteResolvesNameToID(t *testing.T) {
	val := "some-value"
	server := newGlobalVarMockServer(t, []api.GlobalVariableRow{
		newGlobalVariableRow(t, "11111111-1111-1111-1111-111111111111", "my-var", &val),
	})
	t.Cleanup(server.Close)

	client := api.NewClientWithBaseURL("test-key", server.URL)

	// List to resolve name
	resp, err := client.ListGlobalVariables(t.Context())
	if err != nil {
		t.Fatalf("list error: %v", err)
	}

	var targetID string
	for _, v := range resp.Result {
		if v.VariableName == "my-var" {
			targetID = v.Id.String()
			break
		}
	}
	if targetID == "" {
		t.Fatal("expected to find 'my-var' in list")
	}

	// Delete by resolved ID
	err = client.DeleteGlobalVariable(t.Context(), targetID)
	if err != nil {
		t.Fatalf("delete error: %v", err)
	}
}

func TestGlobalVarAddDuplicateReturns409(t *testing.T) {
	val := "existing"
	server := newGlobalVarMockServer(t, []api.GlobalVariableRow{
		newGlobalVariableRow(t, "11111111-1111-1111-1111-111111111111", "my-var", &val),
	})
	t.Cleanup(server.Close)

	client := api.NewClientWithBaseURL("test-key", server.URL)

	_, err := client.AddGlobalVariable(t.Context(), "my-var", "new-value")
	if err == nil {
		t.Fatal("expected 409 error on duplicate, got nil")
	}
}

func newGlobalVariableRow(t *testing.T, id, name string, value *string) api.GlobalVariableRow {
	t.Helper()

	timestamp := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	return api.GlobalVariableRow{
		Id:            uuid.MustParse(id),
		OrgId:         uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		VariableName:  name,
		VariableValue: value,
		CreatedAt:     timestamp,
		UpdatedAt:     timestamp,
	}
}
