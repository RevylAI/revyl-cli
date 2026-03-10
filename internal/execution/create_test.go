package execution

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

func TestCreateTest_ResolvesOrgIDBuildModulesAndCreatesRunnablePayload(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	t.Setenv("REVYL_APP_URL", "https://app.example")

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
			if got := r.URL.Query().Get("platform"); got != "iOS" {
				t.Fatalf("platform query = %q, want iOS", got)
			}
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

	yamlContent := `
test:
  metadata:
    name: ignored-name
    platform: android
  build:
    name: My Test App
  blocks:
    - type: instructions
      step_description: Open the inbox
`

	result, err := CreateTest(context.Background(), "token", CreateTestParams{
		Name:             "dfa",
		Platform:         "ios",
		YAMLContent:      yamlContent,
		ModuleNamesOrIDs: []string{"login-flow"},
	})
	if err != nil {
		t.Fatalf("CreateTest() error = %v", err)
	}
	if result.TestID != "test-1" {
		t.Fatalf("TestID = %q, want test-1", result.TestID)
	}
	if result.TestURL != "https://app.example/tests/test-1" {
		t.Fatalf("TestURL = %q, want https://app.example/tests/test-1", result.TestURL)
	}

	if got := createReq["org_id"]; got != "org-live" {
		t.Fatalf("org_id = %v, want org-live", got)
	}
	if got := createReq["app_id"]; got != "app-yaml" {
		t.Fatalf("app_id = %v, want app-yaml", got)
	}

	tasks, ok := createReq["tasks"].([]any)
	if !ok {
		t.Fatalf("tasks = %#v, want []any", createReq["tasks"])
	}
	if len(tasks) != 2 {
		t.Fatalf("tasks len = %d, want 2", len(tasks))
	}

	moduleBlock, ok := tasks[0].(map[string]any)
	if !ok {
		t.Fatalf("module block = %#v, want map", tasks[0])
	}
	if got := moduleBlock["type"]; got != "module_import" {
		t.Fatalf("module block type = %v, want module_import", got)
	}
	if got := moduleBlock["module_id"]; got != "mod-1" {
		t.Fatalf("module_id = %v, want mod-1", got)
	}

	instructionBlock, ok := tasks[1].(map[string]any)
	if !ok {
		t.Fatalf("instruction block = %#v, want map", tasks[1])
	}
	if got := instructionBlock["type"]; got != "instructions" {
		t.Fatalf("instruction type = %v, want instructions", got)
	}
	if got := instructionBlock["step_description"]; got != "Open the inbox" {
		t.Fatalf("step_description = %v, want Open the inbox", got)
	}
}

func TestCreateTest_RejectsEmptyContentWhenScaffoldingIsNotAllowed(t *testing.T) {
	_, err := buildCreateTestRequest(context.Background(), &api.Client{}, CreateTestParams{
		Name:     "dfa",
		Platform: "ios",
	})
	if err == nil {
		t.Fatal("expected error for empty create")
	}
	if !strings.Contains(err.Error(), "test content is required") {
		t.Fatalf("error = %v, want empty-content guidance", err)
	}
}

func TestCreateTest_AllowEmptyShellUsesExplicitAppID(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	t.Setenv("REVYL_APP_URL", "https://app.example")

	var createReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/entity/users/get_user_uuid":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-live","email":"test@example.com","concurrency_limit":1}`))
		case "/api/v1/builds/vars/app-123":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"app-123","name":"Shell App","platform":"ios","versions_count":1,"latest_version":"1.0.0"}`))
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

	client := api.NewClientWithBaseURL("token", srv.URL)

	result, err := CreateTestWithClient(context.Background(), client, CreateTestParams{
		Name:       "dfa",
		Platform:   "ios",
		AppID:      "app-123",
		AllowEmpty: true,
	})
	if err != nil {
		t.Fatalf("CreateTestWithClient() error = %v", err)
	}
	if result.TestID != "test-2" {
		t.Fatalf("TestID = %q, want test-2", result.TestID)
	}

	tasks, ok := createReq["tasks"].([]any)
	if !ok {
		t.Fatalf("tasks = %#v, want []any", createReq["tasks"])
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks len = %d, want 0", len(tasks))
	}
	if got := createReq["app_id"]; got != "app-123" {
		t.Fatalf("app_id = %v, want app-123", got)
	}
}

func TestCreateTest_RejectsExplicitAppConflictWithYAMLBuildIntent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/builds/vars/app-explicit":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"app-explicit","name":"Explicit App","platform":"ios","versions_count":1,"latest_version":"1.0.0"}`))
		case "/api/v1/builds/vars":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"items":[{"id":"app-yaml","name":"My Test App","platform":"ios","versions_count":2,"latest_version":"1.2.3"}],
				"total":1,"page":1,"page_size":100,"total_pages":1,"has_next":false,"has_previous":false
			}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := api.NewClientWithBaseURL("token", srv.URL)
	yamlContent := `
test:
  metadata:
    name: mismatch
    platform: ios
  build:
    name: My Test App
  blocks:
    - type: instructions
      step_description: do something
`

	_, err := buildCreateTestRequest(context.Background(), client, CreateTestParams{
		Name:        "dfa",
		Platform:    "ios",
		AppID:       "app-explicit",
		YAMLContent: yamlContent,
	})
	if err == nil {
		t.Fatal("expected app conflict error")
	}
	if !strings.Contains(err.Error(), "conflicts with yaml build.name") {
		t.Fatalf("error = %v, want app conflict", err)
	}
}

func TestCreateTest_UsesConfiguredDefaultAppWhenYAMLDoesNotSpecifyBuild(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/builds/vars/app-dev":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"app-dev","name":"Configured App","platform":"ios","versions_count":3,"latest_version":"2.0.0"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := api.NewClientWithBaseURL("token", srv.URL)
	cfg := &config.ProjectConfig{
		Project: config.Project{
			OrgID: "org-config",
		},
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"ios-dev": {AppID: "app-dev"},
				"ios-ci":  {AppID: "app-ci"},
			},
		},
		HotReload: config.HotReloadConfig{
			Providers: map[string]*config.ProviderConfig{
				"expo": {PlatformKeys: map[string]string{"ios": "ios-dev"}},
			},
		},
	}

	if got := ResolveConfiguredAppID(cfg, "ios"); got != "app-dev" {
		t.Fatalf("ResolveConfiguredAppID() = %q, want app-dev", got)
	}

	req, err := buildCreateTestRequest(context.Background(), client, CreateTestParams{
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
		Config: cfg,
	})
	if err != nil {
		t.Fatalf("buildCreateTestRequest() error = %v", err)
	}
	if req.AppID != "app-dev" {
		t.Fatalf("AppID = %q, want app-dev", req.AppID)
	}
}
