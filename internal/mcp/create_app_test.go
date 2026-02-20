package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestHandleCreateAppLinksExistingOnConflict(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/builds/vars" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"detail":"app already exists"}`))
		case http.MethodGet:
			_, _ = w.Write([]byte(`{
				"items":[{"id":"app-123","name":"My-App","platform":"ios"}],
				"total":1,
				"page":1,
				"page_size":100,
				"total_pages":1,
				"has_next":false,
				"has_previous":false
			}`))
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	defer apiServer.Close()

	s := &Server{
		apiClient: api.NewClientWithBaseURL("test-api-key", apiServer.URL),
	}

	_, output, err := s.handleCreateApp(context.Background(), nil, CreateAppInput{
		Name:     "my app",
		Platform: "ios",
	})
	if err != nil {
		t.Fatalf("handleCreateApp returned Go error: %v", err)
	}
	if !output.Success {
		t.Fatalf("expected success linking existing app, got %+v", output)
	}
	if output.AppID != "app-123" {
		t.Fatalf("expected linked app id app-123, got %q", output.AppID)
	}
}

func TestHandleCreateAppFailsWhenConflictCannotBeResolved(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/builds/vars" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"detail":"app already exists"}`))
		case http.MethodGet:
			_, _ = w.Write([]byte(`{
				"items":[{"id":"app-999","name":"Different App","platform":"ios"}],
				"total":1,
				"page":1,
				"page_size":100,
				"total_pages":1,
				"has_next":false,
				"has_previous":false
			}`))
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	defer apiServer.Close()

	s := &Server{
		apiClient: api.NewClientWithBaseURL("test-api-key", apiServer.URL),
	}

	_, output, err := s.handleCreateApp(context.Background(), nil, CreateAppInput{
		Name:     "my app",
		Platform: "ios",
	})
	if err != nil {
		t.Fatalf("handleCreateApp returned Go error: %v", err)
	}
	if output.Success {
		t.Fatalf("expected failure when conflict is unresolved, got %+v", output)
	}
	if !strings.Contains(strings.ToLower(output.Error), "already exists") {
		t.Fatalf("expected already-exists error, got %q", output.Error)
	}
}
