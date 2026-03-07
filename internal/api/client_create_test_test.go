package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateTest_NormalizesPlatform(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/api/v1/tests/create" {
			t.Fatalf("unexpected path: %s", got)
		}
		if got := r.Method; got != http.MethodPost {
			t.Fatalf("unexpected method: %s", got)
		}

		var req CreateTestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if got := req.Platform; got != "iOS" {
			t.Fatalf("platform = %q, want %q", got, "iOS")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"test-1","version":1,"name":"testing"}`))
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)

	resp, err := client.CreateTest(context.Background(), &CreateTestRequest{
		Name:     "testing",
		Platform: "ios",
		Tasks:    []interface{}{},
		AppID:    "app-1",
		OrgID:    "org-1",
	})
	if err != nil {
		t.Fatalf("CreateTest() error = %v, want nil", err)
	}
	if resp.ID != "test-1" {
		t.Fatalf("id = %q, want %q", resp.ID, "test-1")
	}
}
