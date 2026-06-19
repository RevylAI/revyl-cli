package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUpsertStoreKitConfigRef(t *testing.T) {
	var seenBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/variables/ios_storekit/refs" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&seenBody)

		w.Header().Set("Content-Type", "application/json")
		fileID, filename := "f1", "App.storekit"
		json.NewEncoder(w).Encode(StoreKitConfigRefResponse{
			Message: "ok",
			Result: &StoreKitConfigRef{
				ID: "r1", ScopeType: "app", ScopeID: "a1", Mode: "file",
				FileID: &fileID, Filename: &filename,
			},
		})
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	fileID := "f1"
	resp, err := client.UpsertStoreKitConfigRef(context.Background(), &StoreKitConfigRefUpsertRequest{
		ScopeType: "app", ScopeID: "a1", Mode: "file", FileID: &fileID,
	})
	if err != nil {
		t.Fatalf("UpsertStoreKitConfigRef() error = %v", err)
	}
	if resp.Result == nil || resp.Result.Mode != "file" {
		t.Fatalf("unexpected result: %+v", resp.Result)
	}
	if seenBody["scope_type"] != "app" || seenBody["mode"] != "file" || seenBody["file_id"] != "f1" {
		t.Fatalf("unexpected request body: %+v", seenBody)
	}
}

// disabled mode must send file_id as JSON null, not omit it.
func TestUpsertStoreKitConfigRefDisabledSendsNullFileID(t *testing.T) {
	var raw map[string]json.RawMessage

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&raw)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(StoreKitConfigRefResponse{Message: "ok"})
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	if _, err := client.UpsertStoreKitConfigRef(context.Background(), &StoreKitConfigRefUpsertRequest{
		ScopeType: "app", ScopeID: "a1", Mode: "disabled", FileID: nil,
	}); err != nil {
		t.Fatalf("UpsertStoreKitConfigRef() error = %v", err)
	}
	if got := string(raw["file_id"]); got != "null" {
		t.Fatalf("expected file_id to serialize as null, got %s", got)
	}
}

// Unset scope returns 200 with result:null, not an error.
func TestGetStoreKitConfigRefUnsetReturnsNilResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/variables/ios_storekit/refs" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("scope_type") != "app" || q.Get("scope_id") != "a1" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(StoreKitConfigRefResponse{Message: "none", Result: nil})
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	resp, err := client.GetStoreKitConfigRef(context.Background(), "app", "a1")
	if err != nil {
		t.Fatalf("GetStoreKitConfigRef() error = %v", err)
	}
	if resp.Result != nil {
		t.Fatalf("expected nil result for unset scope, got %+v", resp.Result)
	}
}

func TestDeleteStoreKitConfigRefAcceptsNoContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Query().Get("scope_id") != "a1" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	if err := client.DeleteStoreKitConfigRef(context.Background(), "app", "a1"); err != nil {
		t.Fatalf("DeleteStoreKitConfigRef() error = %v", err)
	}
}

// Delete is idempotent: an unset scope returns 200, not 404.
func TestDeleteStoreKitConfigRefIdempotent200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(StoreKitConfigRefResponse{Message: "deleted", Result: nil})
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	if err := client.DeleteStoreKitConfigRef(context.Background(), "app", "a1"); err != nil {
		t.Fatalf("DeleteStoreKitConfigRef() error = %v", err)
	}
}
