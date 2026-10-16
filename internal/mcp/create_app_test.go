package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestHandleCreateWebAppUsesCanonicalPlatform(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		name := "create"
		if conflict {
			name = "reuse"
		}
		t.Run(name, func(t *testing.T) {
			requests := 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path != "/api/v1/apps/" {
					t.Errorf("unexpected path %s", r.URL.Path)
					return
				}
				if r.Method == http.MethodGet {
					if r.URL.Query().Get("platform") != "web" {
						t.Error("noncanonical app filter")
					}
					_, _ = w.Write([]byte(`{"items":[{"id":"fixture-app","name":"Fixture","platform":"Web","versions_count":1,"latest_version":"1","current_version":"1"}],"has_next":false}`))
					return
				}
				var input api.CreateAppRequest
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Platform != "web" {
					t.Error("noncanonical create request")
				}
				if conflict {
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"detail":"app already exists"}`))
					return
				}
				_, _ = w.Write([]byte(`{"id":"fixture-app","name":"Fixture","platform":"web"}`))
			}))
			defer backend.Close()
			server := &Server{apiClient: api.NewClientWithBaseURL("fixture-key", backend.URL)}
			_, result, err := server.handleCreateApp(context.Background(), nil, CreateAppInput{Name: "Fixture", Platform: "Web"})
			if err != nil || !result.Success || result.AppID != "fixture-app" {
				t.Fatalf("result=%#v error=%v", result, err)
			}
			expected := 1
			if conflict {
				expected = 2
			}
			if requests != expected {
				t.Fatalf("requests=%d want %d", requests, expected)
			}
		})
	}
}

func TestHandleCreateAppLinksExistingOnConflict(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/apps/" {
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
		if r.URL.Path != "/api/v1/apps/" {
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
