package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestRevokeCLIAPIKeyPostsExpectedPayload(t *testing.T) {
	var (
		seenAuthorization string
		seenClientHeader  string
		seenRequest       RevokeCLIAPIKeyRequest
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/api/v1/entity/users/revoke_cli_api_key" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		seenAuthorization = r.Header.Get("Authorization")
		seenClientHeader = r.Header.Get("X-Revyl-Client")

		if err := json.NewDecoder(r.Body).Decode(&seenRequest); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"message":"CLI API key revoked"}`))
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)

	if err := client.RevokeCLIAPIKey(context.Background(), "key-123"); err != nil {
		t.Fatalf("RevokeCLIAPIKey() error = %v, want nil", err)
	}
	if seenAuthorization != "Bearer test-key" {
		t.Fatalf("Authorization header = %q, want %q", seenAuthorization, "Bearer test-key")
	}
	if seenClientHeader != "cli" {
		t.Fatalf("X-Revyl-Client header = %q, want %q", seenClientHeader, "cli")
	}
	if seenRequest.APIKeyID != "key-123" {
		t.Fatalf("request api_key_id = %q, want %q", seenRequest.APIKeyID, "key-123")
	}
}

// TestSetAPIKeyUpdatesSubsequentAuthorization verifies credential refreshes affect later requests.
func TestSetAPIKeyUpdatesSubsequentAuthorization(t *testing.T) {
	var seenAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuthorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_uuid":"user-123","org_id":"org-123","email":"user@example.com"}`))
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("stale-token", server.URL)
	client.SetAPIKey("refreshed-token")

	if _, err := client.ValidateAPIKey(context.Background()); err != nil {
		t.Fatalf("ValidateAPIKey() error = %v, want nil", err)
	}
	if seenAuthorization != "Bearer refreshed-token" {
		t.Fatalf("Authorization header = %q, want refreshed token", seenAuthorization)
	}
	if got := client.GetAPIKey(); got != "refreshed-token" {
		t.Fatalf("GetAPIKey() = %q, want refreshed token", got)
	}
}

// TestAPIKeyAccessIsConcurrentSafe verifies credential refresh can overlap request reads.
func TestAPIKeyAccessIsConcurrentSafe(t *testing.T) {
	client := NewClient("initial-token")
	var waitGroup sync.WaitGroup
	for index := 0; index < 100; index++ {
		waitGroup.Add(2)
		go func(value int) {
			defer waitGroup.Done()
			client.SetAPIKey(fmt.Sprintf("token-%d", value))
		}(index)
		go func() {
			defer waitGroup.Done()
			_ = client.GetAPIKey()
		}()
	}
	waitGroup.Wait()

	if client.GetAPIKey() == "" {
		t.Fatal("concurrent credential refresh cleared the API key")
	}
}
