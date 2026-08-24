package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestListRepositoryProjectsUsesRetryableVerifiedCatalogRequest(t *testing.T) {
	const projectID = "11111111-1111-4111-8111-111111111111"
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/projects/catalog" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Revyl-Client") != "cli" {
			t.Fatalf("X-Revyl-Client = %q", r.Header.Get("X-Revyl-Client"))
		}
		var request RepositoryProjectCatalogQuery
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode catalog request: %v", err)
		}
		if request.Provider != "github" || request.Namespace != "acme" || request.RepositoryName != "mobile" {
			t.Fatalf("catalog request = %#v", request)
		}
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"repository_projects_provider_unavailable","message":"unavailable"}`))
			return
		}
		_, _ = w.Write([]byte(`{"repository":{"provider":"github","namespace":"Acme","repository_name":"Mobile"},"projects":[{"project_id":"` + projectID + `","repository_relative_project_root":"apps/mobile","repository_relative_config_path":"apps/mobile/.revyl/config.yaml"}]}`))
	}))
	defer server.Close()

	client := NewClientWithBaseURL("token", server.URL)
	client.retryBaseDelay = 0
	result, err := client.ListRepositoryProjects(
		context.Background(),
		RepositoryProjectCatalogQuery{
			Provider:       "github",
			Namespace:      "acme",
			RepositoryName: "mobile",
		},
	)
	if err != nil {
		t.Fatalf("ListRepositoryProjects() error = %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
	if result.Repository.Namespace != "Acme" || len(result.Projects) != 1 {
		t.Fatalf("catalog result = %#v", result)
	}
	project := result.Projects[0]
	if project.ProjectId.String() != projectID || project.RepositoryRelativeProjectRoot != "apps/mobile" || project.RepositoryRelativeConfigPath != "apps/mobile/.revyl/config.yaml" {
		t.Fatalf("catalog project = %#v", project)
	}
}

func projectConfigurationLocator() ProjectConfigurationRepositoryLocator {
	return ProjectConfigurationRepositoryLocator{
		Provider:                      "github",
		Namespace:                     "acme",
		RepositoryName:                "mobile",
		RepositoryRelativeProjectRoot: ".",
	}
}

func TestProjectConfigurationRequestsAllowProviderVerificationBudget(t *testing.T) {
	client := NewClientWithBaseURL("token", "https://backend.example")
	if got := client.projectConfigurationHTTPClient().Timeout; got != 90*time.Second {
		t.Fatalf("project configuration timeout = %s, want 90s", got)
	}
	if client.httpClient.Timeout >= client.projectConfigurationHTTPClient().Timeout {
		t.Fatal("project configuration client did not extend the default timeout")
	}
}

func TestProjectConfigurationReadAndValidateUseRetryableRequests(t *testing.T) {
	var validateAttempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Revyl-Client") != "cli" {
			t.Fatalf("X-Revyl-Client = %q", r.Header.Get("X-Revyl-Client"))
		}
		switch r.URL.Path {
		case "/api/v1/projects/project-1/configuration/read":
			_, _ = w.Write([]byte(`{"state":"absent"}`))
		case "/api/v1/projects/project-1/configuration/validate":
			if validateAttempts.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"message":"unavailable"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"valid","candidate_project_configuration_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","current":{"state":"absent"}}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClientWithBaseURL("token", server.URL)
	client.retryBaseDelay = 0
	read, err := client.ReadProjectConfiguration(
		context.Background(),
		"project-1",
		ProjectConfigurationReadRequest{Locator: projectConfigurationLocator()},
	)
	if err != nil {
		t.Fatalf("ReadProjectConfiguration() error = %v", err)
	}
	if read.State != ProjectConfigurationReadResponseStateAbsent {
		t.Fatalf("read state = %q", read.State)
	}
	validated, err := client.ValidateProjectConfiguration(
		context.Background(),
		"project-1",
		ProjectConfigurationValidateRequest{
			Locator:       projectConfigurationLocator(),
			Configuration: AuthoredRevylConfig{},
		},
	)
	if err != nil {
		t.Fatalf("ValidateProjectConfiguration() error = %v", err)
	}
	if validated.Status != "valid" || validateAttempts.Load() != 2 {
		t.Fatalf("validation = %#v, attempts = %d", validated, validateAttempts.Load())
	}
}

func TestReplaceProjectConfigurationNeverRetriesAmbiguousResponse(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"message":"ambiguous"}`))
	}))
	defer server.Close()

	client := NewClientWithBaseURL("token", server.URL)
	client.retryBaseDelay = 0
	precondition := ProjectConfigurationReplaceRequest_Precondition{}
	if err := precondition.FromProjectConfigurationAbsentPrecondition(
		ProjectConfigurationAbsentPrecondition{State: "absent"},
	); err != nil {
		t.Fatal(err)
	}
	_, err := client.ReplaceProjectConfiguration(
		context.Background(),
		"project-1",
		ProjectConfigurationReplaceRequest{
			Locator:       projectConfigurationLocator(),
			Configuration: AuthoredRevylConfig{},
			Precondition:  precondition,
		},
	)
	if err == nil {
		t.Fatal("ReplaceProjectConfiguration() error = nil")
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts = %d, want 1", attempts.Load())
	}
}

func TestProjectCursorProofAuthorizationRequestMethods(t *testing.T) {
	const projectID = "11111111-1111-4111-8111-111111111111"
	var getAttempts atomic.Int32
	var putAttempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Revyl-Client") != "cli" {
			t.Fatalf("X-Revyl-Client = %q", r.Header.Get("X-Revyl-Client"))
		}
		if r.URL.Path != "/api/v1/projects/"+projectID+"/cursor-proof-authorization" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		switch r.Method {
		case http.MethodGet:
			if getAttempts.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"code":"unavailable","message":"unavailable"}`))
				return
			}
			_, _ = w.Write([]byte(`{"project_id":"` + projectID + `","required":true,"authorized":false,"authorized_at":null,"repository":{"provider":"github","namespace":"acme","repository_name":"mobile","repository_relative_project_root":"."}}`))
		case http.MethodPut:
			putAttempts.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"ambiguous","message":"ambiguous"}`))
		default:
			t.Fatalf("unexpected method %q", r.Method)
		}
	}))
	defer server.Close()

	client := NewClientWithBaseURL("token", server.URL)
	client.retryBaseDelay = 0
	result, err := client.GetProjectCursorProofAuthorization(context.Background(), projectID)
	if err != nil {
		t.Fatalf("GetProjectCursorProofAuthorization() error = %v", err)
	}
	if !result.Required || result.Authorized || getAttempts.Load() != 2 {
		t.Fatalf("result = %#v, attempts = %d", result, getAttempts.Load())
	}
	if _, err := client.AuthorizeProjectCursorProof(context.Background(), projectID); err == nil {
		t.Fatal("AuthorizeProjectCursorProof() error = nil")
	}
	if putAttempts.Load() != 1 {
		t.Fatalf("put attempts = %d, want 1", putAttempts.Load())
	}
}
