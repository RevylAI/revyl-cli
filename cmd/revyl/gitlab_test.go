package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/gitremote"
	"github.com/spf13/cobra"
)

func gitlabTestCommand(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := testConfigCommand()
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	cmd.Flags().String("instance", "", "")
	cmd.Flags().Bool("token-stdin", true, "")
	return cmd
}

func TestGitlabConnectUsesHiddenPromptOrBoundedStdin(t *testing.T) {
	original := promptGitlabToken
	promptGitlabToken = func(message string) (string, error) {
		if !strings.Contains(message, "hidden") {
			t.Fatalf("prompt = %q", message)
		}
		return "fixture-token", nil
	}
	t.Cleanup(func() { promptGitlabToken = original })
	for _, fromStdin := range []bool{false, true} {
		token, err := readGitlabToken(strings.NewReader("fixture-token\r\n"), fromStdin)
		if err != nil || token != "fixture-token" {
			t.Fatalf("stdin=%t token=%q error=%v", fromStdin, token, err)
		}
	}
	for _, input := range []string{"", "a b", "a\nb", "a\x00b", strings.Repeat("x", 8193)} {
		if _, err := readGitlabToken(strings.NewReader(input), true); err == nil {
			t.Fatal("invalid token was accepted")
		}
	}
	if _, err := readGitlabToken(strings.NewReader(strings.Repeat("x", 8192)+"\r\n"), true); err != nil {
		t.Fatal(err)
	}
}

func TestGitlabConnectSubmitsTokenOnceAndKeepsItOutOfOutput(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprint(reject), func(t *testing.T) {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					fmt.Fprint(w, `{"enabled":true,"configured":false,"connected":false}`)
					return
				}
				if r.Method != http.MethodPut || r.URL.Path != "/api/v1/integrations/gitlab/connection" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				writes++
				var request api.GitlabConnectRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if request.AccessToken == nil || *request.AccessToken != "fixture-token" || request.InstanceUrl == nil || *request.InstanceUrl != "https://gitlab.example.com" {
					t.Error("incorrect connection payload")
				}
				if reject {
					w.WriteHeader(503)
					fmt.Fprint(w, `{"detail":{"message":"fixture-token","code":"gitlab_provider_unavailable"}}`)
				} else {
					fmt.Fprint(w, `{"enabled":true,"configured":true,"connected":true,"instance_url":"https://gitlab.example.com"}`)
				}
			}))
			defer server.Close()
			t.Setenv("REVYL_API_KEY", "fixture-revyl-key")
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			cmd := gitlabTestCommand(t)
			if err := cmd.Flags().Set("instance", "https://gitlab.example.com/"); err != nil {
				t.Fatal(err)
			}
			cmd.SetIn(strings.NewReader("fixture-token\n"))
			var output bytes.Buffer
			cmd.SetOut(&output)
			err := runGitlabConnect(cmd)
			if (err != nil) != reject {
				t.Fatalf("error=%v reject=%t", err, reject)
			}
			if writes != 1 {
				t.Fatalf("writes=%d", writes)
			}
			if strings.Contains(output.String(), "fixture-token") || (err != nil && strings.Contains(err.Error(), "fixture-token")) {
				t.Fatal("token leaked")
			}
			if !reject {
				var result api.GitlabConnectionSummary
				if err := json.Unmarshal(output.Bytes(), &result); err != nil || !result.Connected {
					t.Fatalf("output=%s error=%v", output.String(), err)
				}
			}
		})
	}
}

func TestGitlabConnectRejectsUnauthorizedOrganizationBeforeTokenEntry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("unauthorized organization attempted a write")
		}
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"detail":"GitLab integration access is unavailable"}`)
	}))
	defer server.Close()
	t.Setenv("REVYL_API_KEY", "fixture-revyl-key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	cmd := gitlabTestCommand(t)
	cmd.SetIn(strings.NewReader("fixture-token"))
	if err := runGitlabConnect(cmd); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("error=%v", err)
	}
}

func TestGitlabStatusChecksLiveTokenAndKeepsWebhookSeparate(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprint(invalid), func(t *testing.T) {
			originalRoot, originalRepository, originalContext := resolveConfigPullRoot, resolveProjectRepository, resolveProjectContext
			resolveConfigPullRoot = func(string, string) (string, string, error) { return "/repo", "/repo", nil }
			resolveProjectRepository = func(string) (gitremote.RepositoryReference, error) {
				instance := "https://gitlab.example.com"
				return gitremote.RepositoryReference{Provider: "gitlab", InstanceURL: &instance, Namespace: "group/subgroup", Name: "mobile"}, nil
			}
			resolveProjectContext = func(string, string) (*config.ProjectContext, error) { return nil, errors.New("no config") }
			t.Cleanup(func() {
				resolveConfigPullRoot, resolveProjectRepository, resolveProjectContext = originalRoot, originalRepository, originalContext
			})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/integrations/gitlab/connection":
					fmt.Fprint(w, `{"enabled":true,"configured":true,"connected":true,"instance_url":"https://gitlab.example.com"}`)
				case "/api/v1/integrations/gitlab/repositories":
					if r.URL.Query().Get("per_page") != "1" {
						t.Error("unbounded health request")
					}
					if invalid {
						w.WriteHeader(409)
						fmt.Fprint(w, `{"detail":{"code":"gitlab_token_invalid"}}`)
					} else {
						fmt.Fprint(w, `{"repositories":[],"next_page":null}`)
					}
				case "/api/v1/projects/catalog":
					if invalid {
						t.Fatal("revoked token reached repository catalog")
					}
					var request api.RepositoryProjectCatalogQuery
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if request.InstanceUrl == nil || *request.InstanceUrl != "https://gitlab.example.com" || request.Namespace != "group/subgroup" {
						t.Error("repository identity lost")
					}
					fmt.Fprint(w, `{"repository":{"provider":"gitlab","namespace":"group/subgroup","repository_name":"mobile","report_mode":"comment"},"projects":[]}`)
				case "/api/v1/integrations/gitlab/webhook":
					if r.Method != http.MethodGet || r.URL.Query().Get("namespace") != "group/subgroup" {
						t.Error("incorrect webhook query")
					}
					fmt.Fprint(w, `{"configured":true,"verified_at":null}`)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
				}
			}))
			defer server.Close()
			report, err := collectGitlabStatus(gitlabTestCommand(t), api.NewClientWithBaseURL("fixture-key", server.URL))
			if err != nil {
				t.Fatal(err)
			}
			if invalid {
				if report.Connected || report.NextAction != "revyl gitlab connect" || report.Repository != nil {
					t.Fatalf("report=%+v", report)
				}
			} else {
				if !report.Connected || report.Repository == nil || !report.Repository.AccessGranted || report.Webhook == nil || report.Webhook.VerifiedAt != nil {
					t.Fatalf("report=%+v", report)
				}
			}
		})
	}
}

func TestGitlabStatusProjectReadFailurePreservesJSONReport(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		statusCode int
		body       string
		wantError  string
	}{
		{"invalid configuration", http.StatusUnprocessableEntity, `{"detail":"Invalid project configuration"}`, "revyl config validate"},
		{"inaccessible project", http.StatusNotFound, `{"code":"project_configuration_inaccessible"}`, "project identity"},
		{"provider unavailable", http.StatusServiceUnavailable, `{"code":"repository_provider_unavailable"}`, "repository verification is temporarily unavailable"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			local := configContext(t, 300)
			withProjectConfigurationDependencies(t, local, "fixture-key", nil)
			instance := "https://gitlab.example.com"
			resolveProjectRepository = func(string) (gitremote.RepositoryReference, error) {
				return gitremote.RepositoryReference{Provider: "gitlab", InstanceURL: &instance, Namespace: "group", Name: "mobile"}, nil
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/integrations/gitlab/connection":
					fmt.Fprint(w, `{"enabled":true,"configured":true,"connected":true,"instance_url":"https://gitlab.example.com"}`)
				case "/api/v1/integrations/gitlab/repositories":
					fmt.Fprint(w, `{"repositories":[],"next_page":null}`)
				case "/api/v1/projects/catalog":
					fmt.Fprint(w, `{"repository":{"provider":"gitlab","namespace":"group","repository_name":"mobile","report_mode":"comment"},"projects":[]}`)
				case "/api/v1/integrations/gitlab/webhook":
					fmt.Fprint(w, `{"configured":true,"verified_at":"2026-10-12T12:00:00Z"}`)
				case "/api/v1/projects/" + configRemoteProjectID + "/configuration/read":
					w.WriteHeader(testCase.statusCode)
					fmt.Fprint(w, testCase.body)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			t.Setenv("REVYL_API_KEY", "fixture-key")
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			cmd := gitlabTestCommand(t)
			var output bytes.Buffer
			cmd.SetOut(&output)
			if err := runGitlabStatus(cmd); err != nil {
				t.Fatalf("runGitlabStatus() error = %v", err)
			}
			var report gitlabStatusReport
			if err := json.Unmarshal(output.Bytes(), &report); err != nil {
				t.Fatalf("status output is not JSON: %v", err)
			}
			if !report.Enabled || !report.Connected || report.Repository == nil || !report.Repository.AccessGranted || report.Webhook == nil || report.Webhook.VerifiedAt == nil || report.Project != nil {
				t.Fatalf("report = %+v", report)
			}
			if !strings.Contains(report.ProjectError, testCase.wantError) || strings.Contains(report.ProjectError, "connection input") || strings.Contains(report.ProjectError, "GitHub") {
				t.Fatalf("project_error = %q", report.ProjectError)
			}
		})
	}
}

func TestGitlabSetupMissingOriginSuggestsSupportedRepository(t *testing.T) {
	local := configContext(t, 300)
	withProjectConfigurationDependencies(t, local, "fixture-key", nil)
	resolveProjectRepository = func(string) (gitremote.RepositoryReference, error) {
		return gitremote.RepositoryReference{}, errors.New("origin is missing")
	}
	originalTTY := reviewSetupInputTTY
	reviewSetupInputTTY = func() bool { return true }
	t.Cleanup(func() { reviewSetupInputTTY = originalTTY })
	err := runGitlabSetup(gitlabTestCommand(t))
	if err == nil {
		t.Fatal("setup with a missing origin succeeded")
	}
	for _, want := range []string{"GitHub or GitLab", "remote add origin", "remote set-url origin", "<repository-url>"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "github.com") {
		t.Fatalf("GitLab setup recommended a GitHub remote: %v", err)
	}
}

func TestGitlabSetupRejectsRepositoryAccessBeforeAppSelection(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		statusCode int
		code       string
		wantError  string
	}{
		{"revoked token", http.StatusConflict, "gitlab_token_invalid", "revyl gitlab connect"},
		{"insufficient access", http.StatusNotFound, "repository_projects_inaccessible", "Developer access"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			local := configContext(t, 300)
			withProjectConfigurationDependencies(t, local, "fixture-key", nil)
			instance := "https://gitlab.example.com"
			resolveProjectRepository = func(string) (gitremote.RepositoryReference, error) {
				return gitremote.RepositoryReference{Provider: "gitlab", InstanceURL: &instance, Namespace: "group/subgroup", Name: "mobile"}, nil
			}
			resolved, err := resolveConnectedProjectConfiguration(local)
			if err != nil {
				t.Fatal(err)
			}
			originalPrompt, originalSelect := promptReviewSetupSelect, selectReviewSetupApp
			promptReviewSetupSelect = func(string, []string) (int, error) {
				t.Error("repository access failure reached setup prompts")
				return 0, nil
			}
			selectReviewSetupApp = func(*cobra.Command, *api.Client, *config.ProjectContext, string) (string, error) {
				t.Error("repository access failure reached app selection")
				return "", errors.New("unexpected app selection")
			}
			t.Cleanup(func() { promptReviewSetupSelect, selectReviewSetupApp = originalPrompt, originalSelect })
			catalogRequests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/integrations/gitlab/connection":
					fmt.Fprint(w, `{"enabled":true,"configured":true,"connected":true,"instance_url":"https://gitlab.example.com"}`)
				case "/api/v1/projects/catalog":
					catalogRequests++
					var query api.RepositoryProjectCatalogQuery
					if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
						t.Error(err)
					}
					if r.Method != http.MethodPost || query.Provider != api.RepositoryProjectCatalogQueryProviderGitlab || query.InstanceUrl == nil || *query.InstanceUrl != instance || query.Namespace != "group/subgroup" || query.RepositoryName != "mobile" {
						t.Errorf("incorrect repository access query: %+v", query)
					}
					w.WriteHeader(testCase.statusCode)
					fmt.Fprintf(w, `{"code":%q}`, testCase.code)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			err = runGitlabSetupForProject(gitlabTestCommand(t), api.NewClientWithBaseURL("fixture-key", server.URL), local, resolved)
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) || catalogRequests != 1 {
				t.Fatalf("setup error = %v, catalog requests = %d", err, catalogRequests)
			}
		})
	}
}

func TestGitlabSetupResumesPendingWebhookWithoutRepublishing(t *testing.T) {
	for _, verified := range []bool{false, true} {
		t.Run(fmt.Sprint(verified), func(t *testing.T) {
			originalValidate, originalRepository, originalConfirm, originalCheck := validateReviewSetupConfig, resolveProjectRepository, confirmReviewSetup, confirmGitlabWebhook
			instance := "https://gitlab.example.com"
			accessChecked := false
			resolveProjectRepository = func(string) (gitremote.RepositoryReference, error) {
				return gitremote.RepositoryReference{Provider: "gitlab", InstanceURL: &instance, Namespace: "group", Name: "mobile"}, nil
			}
			confirmReviewSetup = func(string, bool) (bool, error) {
				t.Fatal("unchanged Git-managed configuration prompted for publication")
				return false, nil
			}
			confirmGitlabWebhook = func(string, bool) (bool, error) { return false, nil }
			validateReviewSetupConfig = func(*cobra.Command, *api.Client, *resolvedProjectConfiguration) (*api.ProjectConfigurationValidateResponse, error) {
				if !accessChecked {
					t.Fatal("setup validation preceded repository access verification")
				}
				return &api.ProjectConfigurationValidateResponse{Current: api.ProjectConfigurationReadResponse{Resource: &api.ProjectConfigurationResource{Authority: api.ConfigurationAuthorityGitDefaultBranch}}}, nil
			}
			t.Cleanup(func() {
				validateReviewSetupConfig, resolveProjectRepository, confirmReviewSetup, confirmGitlabWebhook = originalValidate, originalRepository, originalConfirm, originalCheck
			})
			commands := config.CommandStepItems([]string{"build"})
			profile, app := "preview", githubSetupIOSAppID
			local := githubSetupContext(t, config.AuthoredConfig{Project: config.AuthoredProject{ID: configRemoteProjectID}, Build: &config.AuthoredBuild{Framework: "ios", Profiles: map[string]config.AuthoredBuildProfile{"preview": {IOS: &config.AuthoredBuildRecipe{AppID: &app, BuildCommands: &commands}}}}, PRReview: &config.AuthoredPRReview{Build: config.AuthoredReviewBuild{Kind: "revyl", Profile: &profile}}})
			resolved, err := resolveConnectedProjectConfiguration(local)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/catalog" {
					accessChecked = true
					fmt.Fprint(w, `{"repository":{"provider":"gitlab","namespace":"group","repository_name":"mobile","report_mode":"comment"},"projects":[]}`)
					return
				}
				if r.Method != http.MethodGet {
					t.Fatal("resume attempted a write")
				}
				if r.URL.Path == "/api/v1/integrations/gitlab/connection" {
					fmt.Fprint(w, `{"enabled":true,"configured":true,"connected":true,"instance_url":"https://gitlab.example.com"}`)
					return
				}
				if r.URL.Path != "/api/v1/integrations/gitlab/webhook" {
					t.Errorf("unexpected path=%s", r.URL.Path)
				}
				status := api.GitlabWebhookStatus{Configured: true}
				if verified {
					now := time.Now().UTC()
					status.VerifiedAt = &now
				}
				if err := json.NewEncoder(w).Encode(status); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			err = runGitlabSetupForProject(gitlabTestCommand(t), api.NewClientWithBaseURL("fixture-key", server.URL), local, resolved)
			if verified && err != nil {
				t.Fatal(err)
			}
			if !verified && (err == nil || !strings.Contains(err.Error(), "unverified")) {
				t.Fatalf("pending webhook error=%v", err)
			}
		})
	}
}

func TestGitlabSetupRejectsWrongProviderAndNoninteractiveInput(t *testing.T) {
	originalTTY := reviewSetupInputTTY
	reviewSetupInputTTY = func() bool { return false }
	t.Cleanup(func() { reviewSetupInputTTY = originalTTY })
	if err := runGitlabSetup(gitlabTestCommand(t)); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("error=%v", err)
	}
	resolved := &resolvedProjectConfiguration{locator: api.ProjectConfigurationRepositoryLocator{Provider: api.ProjectConfigurationRepositoryLocatorProviderGithub}}
	if err := runGitlabSetupForProject(gitlabTestCommand(t), nil, nil, resolved); err == nil || !strings.Contains(err.Error(), "github setup") {
		t.Fatalf("error=%v", err)
	}
}

func TestNormalizeGitlabInstance(t *testing.T) {
	for _, invalid := range []string{"http://gitlab.com", "https://user:password@gitlab.com", "https://gitlab.com/path", "https://gitlab.com?token=x", "https://github.com", "https://gitlab.com:0"} {
		if _, err := normalizeGitlabInstance(invalid); err == nil {
			t.Fatal("invalid instance accepted")
		}
	}
	if got, err := normalizeGitlabInstance("https://gitlab.example.com:8443/"); err != nil || got != "https://gitlab.example.com:8443" {
		t.Fatalf("instance=%q error=%v", got, err)
	}
}
