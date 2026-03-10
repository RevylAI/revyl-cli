package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/testutil"
)

func TestHandleCreateTest_RejectsEmptyScaffoldRequests(t *testing.T) {
	s := &Server{
		apiClient: &api.Client{},
		workDir:   t.TempDir(),
	}

	_, output, err := s.handleCreateTest(context.Background(), nil, CreateTestInput{
		Name:     "dfa",
		Platform: "ios",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if output.Success {
		t.Fatalf("expected failure for empty scaffold request")
	}
	if !strings.Contains(output.Error, "test content is required") {
		t.Fatalf("error = %q, want empty-content guidance", output.Error)
	}
}

func TestHandleCreateTest_CreatesRunnablePayloadWithModules(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())

	var createReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/entity/users/get_user_uuid":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-live","email":"test@example.com","concurrency_limit":1}`))
		case "/api/v1/modules/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message":"ok","result":[{"id":"mod-1","name":"login-flow","blocks":[]}]}`))
		case "/api/v1/builds/vars":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"items":[{"id":"app-yaml","name":"My Test App","platform":"ios","versions_count":2,"latest_version":"1.2.3"}],
				"total":1,"page":1,"page_size":100,"total_pages":1,"has_next":false,"has_previous":false
			}`))
		case "/api/v1/tests/create":
			if err := json.NewDecoder(r.Body).Decode(&createReq); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"test-1","version":1}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	t.Setenv("REVYL_BACKEND_URL", srv.URL)
	t.Setenv("REVYL_APP_URL", "https://app.example")

	s := &Server{
		apiClient: api.NewClientWithBaseURL("test-api-key", srv.URL),
		workDir:   t.TempDir(),
	}

	yamlContent := `
test:
  metadata:
    name: ignored
    platform: ios
  build:
    name: My Test App
  blocks:
    - type: instructions
      step_description: Open the inbox
`

	_, output, err := s.handleCreateTest(context.Background(), nil, CreateTestInput{
		Name:             "dfa",
		Platform:         "ios",
		YAMLContent:      yamlContent,
		ModuleNamesOrIDs: []string{"login-flow"},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !output.Success {
		t.Fatalf("expected success, got error: %s", output.Error)
	}
	if output.TestID != "test-1" {
		t.Fatalf("TestID = %q, want test-1", output.TestID)
	}

	if got := createReq["app_id"]; got != "app-yaml" {
		t.Fatalf("app_id = %v, want app-yaml", got)
	}
	tasks, ok := createReq["tasks"].([]any)
	if !ok || len(tasks) != 2 {
		t.Fatalf("tasks = %#v, want two merged blocks", createReq["tasks"])
	}
	firstBlock, ok := tasks[0].(map[string]any)
	if !ok || firstBlock["type"] != "module_import" {
		t.Fatalf("first task = %#v, want module_import block", tasks[0])
	}
}

func TestHandleCreateTest_UsesConfiguredDefaultAppWhenBuildNameIsOmitted(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())

	var createReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/entity/users/get_user_uuid":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-live","email":"test@example.com","concurrency_limit":1}`))
		case "/api/v1/builds/vars/app-config":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"app-config","name":"Configured App","platform":"ios","versions_count":3,"latest_version":"2.0.0"}`))
		case "/api/v1/tests/create":
			if err := json.NewDecoder(r.Body).Decode(&createReq); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"test-2","version":1}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	t.Setenv("REVYL_BACKEND_URL", srv.URL)
	t.Setenv("REVYL_APP_URL", "https://app.example")

	s := &Server{
		apiClient: api.NewClientWithBaseURL("test-api-key", srv.URL),
		workDir:   t.TempDir(),
		config: &config.ProjectConfig{
			Build: config.BuildConfig{
				Platforms: map[string]config.BuildPlatform{
					"ios": {AppID: "app-config"},
				},
			},
		},
	}

	_, output, err := s.handleCreateTest(context.Background(), nil, CreateTestInput{
		Name:     "dfa",
		Platform: "ios",
		YAMLContent: `
test:
  metadata:
    name: dfa
    platform: ios
  blocks:
    - type: instructions
      step_description: Open app
`,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !output.Success {
		t.Fatalf("expected success, got error: %s", output.Error)
	}
	if got := createReq["app_id"]; got != "app-config" {
		t.Fatalf("app_id = %v, want app-config", got)
	}
}
