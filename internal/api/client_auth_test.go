package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
