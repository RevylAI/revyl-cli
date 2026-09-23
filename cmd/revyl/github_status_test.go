package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

// githubStatusCommand builds a command whose root carries the persistent
// --json flag the status handler reads.
func githubStatusCommand(t *testing.T, jsonOutput bool) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "revyl"}
	root.PersistentFlags().Bool("json", false, "")
	command := &cobra.Command{Use: "status"}
	command.Flags().Bool("dev", false, "")
	command.SetContext(context.Background())
	root.AddCommand(command)
	if jsonOutput {
		if err := root.PersistentFlags().Set("json", "true"); err != nil {
			t.Fatal(err)
		}
	}
	return command
}

// withFreshCheckout simulates a Git worktree that has a GitHub origin but no
// .revyl/config.yaml yet.
func withFreshCheckout(t *testing.T, namespace, repository string) {
	t.Helper()
	originalResolve := resolveProjectContext
	originalSlug := resolveProjectRepoSlug
	originalRoot := resolveConfigPullRoot
	originalCwd := configWorkingDirectory
	resolveProjectContext = func(string, string) (*config.ProjectContext, error) {
		return nil, &config.ConfigError{Stage: "read", Code: "config_not_found"}
	}
	resolveProjectRepoSlug = func(string, string) (string, string, error) {
		return namespace, repository, nil
	}
	resolveConfigPullRoot = func(string, string) (string, string, error) {
		return "/repo", "/repo", nil
	}
	configWorkingDirectory = func() (string, error) { return "/repo", nil }
	t.Cleanup(func() {
		resolveProjectContext = originalResolve
		resolveProjectRepoSlug = originalSlug
		resolveConfigPullRoot = originalRoot
		configWorkingDirectory = originalCwd
	})
}

// githubStatusServer serves the repositories endpoint and the project
// configuration read the status command performs once a project is granted.
func githubStatusServer(t *testing.T, repos api.GithubRepositoriesResponse, read *api.ProjectConfigurationReadResponse) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/integrations/github/repositories":
			_ = json.NewEncoder(w).Encode(repos)
		case strings.HasSuffix(r.URL.Path, "/configuration/read"):
			_ = json.NewEncoder(w).Encode(read)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func decodeGithubStatusReport(t *testing.T, stdout string) githubStatusReport {
	t.Helper()
	var report githubStatusReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("stdout is not a single JSON object: %v\n%s", err, stdout)
	}
	return report
}

func TestCollectGithubStatusReportsGrantBeforeConfigExists(t *testing.T) {
	withFreshCheckout(t, "acme", "mobile")
	repos := connectedRepos()
	repos.Repositories = append(repos.Repositories, api.GithubOrgRepository{Owner: "Acme", Repo: "Mobile", InstallationID: 123})

	report := collectGithubStatus(githubStatusCommand(t, false), &fakeProjectConfigurationClient{}, &repos)

	if !report.Connected || report.RepositoryCount != 2 {
		t.Fatalf("connection summary = %+v", report)
	}
	if report.Repository == nil || report.Repository.FullName != "acme/mobile" || !report.Repository.AccessGranted {
		t.Fatalf("repository = %+v", report.Repository)
	}
	if report.Project != nil {
		t.Fatalf("project should be nil without a config, got %+v", report.Project)
	}
	if !strings.Contains(report.ProjectError, ".revyl/config.yaml") {
		t.Fatalf("project_error should explain the missing config, got %q", report.ProjectError)
	}
}

func TestCollectGithubStatusReportsMissingGrant(t *testing.T) {
	withFreshCheckout(t, "acme", "mobile")
	repos := connectedRepos()

	report := collectGithubStatus(githubStatusCommand(t, false), &fakeProjectConfigurationClient{}, &repos)

	if report.Repository == nil || report.Repository.AccessGranted {
		t.Fatalf("repository should be reported as not granted, got %+v", report.Repository)
	}
}

func TestCollectGithubStatusOmitsRepositoryOutsideGithubWorktree(t *testing.T) {
	withFreshCheckout(t, "", "")
	resolveProjectRepoSlug = func(string, string) (string, string, error) {
		return "", "", errors.New("no github origin")
	}
	repos := connectedRepos()

	report := collectGithubStatus(githubStatusCommand(t, false), &fakeProjectConfigurationClient{}, &repos)

	if report.Repository != nil {
		t.Fatalf("repository should be nil without a GitHub origin, got %+v", report.Repository)
	}
}

func TestCollectGithubStatusReadsPublishedProject(t *testing.T) {
	local := configContext(t, 300)
	enabled := true
	client := &fakeProjectConfigurationClient{
		readResult: &api.ProjectConfigurationReadResponse{
			State: api.ProjectConfigurationReadResponseStatePresent,
			Resource: &api.ProjectConfigurationResource{
				Authority: api.ConfigurationAuthorityManual,
				Configuration: api.AuthoredRevylConfig{
					PrReview: &api.AuthoredPRReview{Enabled: &enabled},
				},
			},
		},
	}
	withProjectConfigurationDependencies(t, local, "token", client)
	repos := connectedRepos()
	repos.Repositories = []api.GithubOrgRepository{{Owner: "acme", Repo: "mobile", InstallationID: 123}}

	report := collectGithubStatus(githubStatusCommand(t, false), client, &repos)

	if report.Repository == nil || !report.Repository.AccessGranted {
		t.Fatalf("repository = %+v", report.Repository)
	}
	if report.Project == nil || report.Project.Status != githubProjectStatusEnabled || report.Project.Authority != string(api.ConfigurationAuthorityManual) || report.Project.Root != "." {
		t.Fatalf("project = %+v", report.Project)
	}
	if report.ProjectError != "" {
		t.Fatalf("unexpected project_error %q", report.ProjectError)
	}
}

func TestCollectGithubStatusResolvesGitOriginOnce(t *testing.T) {
	local := configContext(t, 300)
	client := &fakeProjectConfigurationClient{
		readResult: &api.ProjectConfigurationReadResponse{State: api.ProjectConfigurationReadResponseStateAbsent},
	}
	withProjectConfigurationDependencies(t, local, "token", client)
	slugCalls := 0
	resolveProjectRepoSlug = func(string, string) (string, string, error) {
		slugCalls++
		return "acme", "mobile", nil
	}
	repos := connectedRepos()
	repos.Repositories = []api.GithubOrgRepository{{Owner: "acme", Repo: "mobile", InstallationID: 123}}

	report := collectGithubStatus(githubStatusCommand(t, false), client, &repos)

	if report.Project == nil || client.readCalls != 1 {
		t.Fatalf("project read should run once, got project=%+v reads=%d", report.Project, client.readCalls)
	}
	if slugCalls != 1 {
		t.Fatalf("git origin should be resolved once per status run, got %d", slugCalls)
	}
}

func TestCollectGithubStatusMarksUnpublishedProject(t *testing.T) {
	local := configContext(t, 300)
	client := &fakeProjectConfigurationClient{
		readResult: &api.ProjectConfigurationReadResponse{State: api.ProjectConfigurationReadResponseStateAbsent},
	}
	withProjectConfigurationDependencies(t, local, "token", client)
	repos := connectedRepos()
	repos.Repositories = []api.GithubOrgRepository{{Owner: "acme", Repo: "mobile", InstallationID: 123}}

	report := collectGithubStatus(githubStatusCommand(t, false), client, &repos)

	if report.Project == nil || report.Project.Status != githubProjectStatusNotPublished || report.Project.Authority != "" {
		t.Fatalf("project = %+v", report.Project)
	}
}

func TestCollectGithubStatusSkipsProjectReadWhenRepositoryNotGranted(t *testing.T) {
	local := configContext(t, 300)
	client := &fakeProjectConfigurationClient{}
	withProjectConfigurationDependencies(t, local, "token", client)
	repos := connectedRepos()

	report := collectGithubStatus(githubStatusCommand(t, false), client, &repos)

	if report.Repository == nil || report.Repository.AccessGranted {
		t.Fatalf("repository = %+v", report.Repository)
	}
	if report.Project != nil || client.readCalls != 0 {
		t.Fatalf("project read should be skipped, got project=%+v reads=%d", report.Project, client.readCalls)
	}
}

func TestGithubStatusJSONKeepsStdoutParseable(t *testing.T) {
	withFreshCheckout(t, "acme", "mobile")
	repos := connectedRepos()
	repos.Repositories = []api.GithubOrgRepository{{Owner: "acme", Repo: "mobile", InstallationID: 123}}
	server := githubReposServer(t, func() api.GithubRepositoriesResponse { return repos })
	t.Cleanup(server.Close)
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	var runErr error
	stdout, stderr := captureStdoutAndStderrSeparate(t, func() {
		runErr = runGithubStatus(githubStatusCommand(t, true), nil)
	})
	if runErr != nil {
		t.Fatalf("runGithubStatus() error = %v", runErr)
	}
	if stderr != "" {
		t.Fatalf("--json must keep stderr free of status text, got %q", stderr)
	}
	report := decodeGithubStatusReport(t, stdout)
	if !report.Connected || report.RepositoryCount != 1 {
		t.Fatalf("connection summary = %+v", report)
	}
	if report.Repository == nil || report.Repository.FullName != "acme/mobile" || !report.Repository.AccessGranted {
		t.Fatalf("repository = %+v", report.Repository)
	}
	if report.Project != nil {
		t.Fatalf("project = %+v", report.Project)
	}
	if !strings.Contains(stdout, `"project": null`) || !strings.Contains(stdout, `"project_error"`) {
		t.Fatalf("stable keys missing from stdout:\n%s", stdout)
	}
}

func TestGithubStatusJSONWhenNotConnected(t *testing.T) {
	withFreshCheckout(t, "acme", "mobile")
	server := githubReposServer(t, notConnectedRepos)
	t.Cleanup(server.Close)
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	stdout, _ := captureStdoutAndStderrSeparate(t, func() {
		if err := runGithubStatus(githubStatusCommand(t, true), nil); err != nil {
			t.Errorf("runGithubStatus() error = %v", err)
		}
	})
	report := decodeGithubStatusReport(t, stdout)
	if report.Connected || report.Repository != nil || report.Project != nil {
		t.Fatalf("not-connected report = %+v", report)
	}
}

func TestGithubStatusHumanOutputNamesRepositoryGrant(t *testing.T) {
	withFreshCheckout(t, "acme", "mobile")
	repos := connectedRepos()
	server := githubReposServer(t, func() api.GithubRepositoriesResponse { return repos })
	t.Cleanup(server.Close)
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	stdout, stderr := captureStdoutAndStderrSeparate(t, func() {
		if err := runGithubStatus(githubStatusCommand(t, false), nil); err != nil {
			t.Errorf("runGithubStatus() error = %v", err)
		}
	})
	if stdout != "" {
		t.Fatalf("human output belongs on stderr, stdout = %q", stdout)
	}
	for _, want := range []string{"GitHub App connected", "acme/mobile", "access not granted", "Grant this repository"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

// TestGithubStatusJSONProjectStates pins the serialized contract for each
// published-project state, including authority inclusion and stable keys.
func TestGithubStatusJSONProjectStates(t *testing.T) {
	enabled := true
	disabled := false
	cases := []struct {
		name          string
		read          *api.ProjectConfigurationReadResponse
		wantStatus    string
		wantAuthority string
	}{
		{
			name:       "not published",
			read:       &api.ProjectConfigurationReadResponse{State: api.ProjectConfigurationReadResponseStateAbsent},
			wantStatus: githubProjectStatusNotPublished,
		},
		{
			name: "enabled",
			read: &api.ProjectConfigurationReadResponse{
				State: api.ProjectConfigurationReadResponseStatePresent,
				Resource: &api.ProjectConfigurationResource{
					Authority:     api.ConfigurationAuthorityManual,
					Configuration: api.AuthoredRevylConfig{PrReview: &api.AuthoredPRReview{Enabled: &enabled}},
				},
			},
			wantStatus:    githubProjectStatusEnabled,
			wantAuthority: string(api.ConfigurationAuthorityManual),
		},
		{
			name: "enabled by default",
			read: &api.ProjectConfigurationReadResponse{
				State: api.ProjectConfigurationReadResponseStatePresent,
				Resource: &api.ProjectConfigurationResource{
					Authority:     api.ConfigurationAuthorityGitDefaultBranch,
					Configuration: api.AuthoredRevylConfig{PrReview: &api.AuthoredPRReview{}},
				},
			},
			wantStatus:    githubProjectStatusEnabled,
			wantAuthority: string(api.ConfigurationAuthorityGitDefaultBranch),
		},
		{
			name: "disabled",
			read: &api.ProjectConfigurationReadResponse{
				State: api.ProjectConfigurationReadResponseStatePresent,
				Resource: &api.ProjectConfigurationResource{
					Authority:     api.ConfigurationAuthorityManual,
					Configuration: api.AuthoredRevylConfig{PrReview: &api.AuthoredPRReview{Enabled: &disabled}},
				},
			},
			wantStatus:    githubProjectStatusDisabled,
			wantAuthority: string(api.ConfigurationAuthorityManual),
		},
		{
			name: "not configured",
			read: &api.ProjectConfigurationReadResponse{
				State:    api.ProjectConfigurationReadResponseStatePresent,
				Resource: &api.ProjectConfigurationResource{Authority: api.ConfigurationAuthorityManual},
			},
			wantStatus:    githubProjectStatusNotConfigured,
			wantAuthority: string(api.ConfigurationAuthorityManual),
		},
		{
			name:       "invalid",
			read:       &api.ProjectConfigurationReadResponse{State: api.ProjectConfigurationReadResponseStatePresent},
			wantStatus: githubProjectStatusInvalid,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			local := configContext(t, 300)
			withProjectConfigurationDependencies(t, local, "token", &fakeProjectConfigurationClient{})
			repos := connectedRepos()
			repos.Repositories = []api.GithubOrgRepository{{Owner: "acme", Repo: "mobile", InstallationID: 123}}
			server := githubStatusServer(t, repos, testCase.read)
			t.Cleanup(server.Close)
			t.Setenv("REVYL_API_KEY", "test-key")
			t.Setenv("REVYL_BACKEND_URL", server.URL)

			var runErr error
			stdout, stderr := captureStdoutAndStderrSeparate(t, func() {
				runErr = runGithubStatus(githubStatusCommand(t, true), nil)
			})
			if runErr != nil {
				t.Fatalf("runGithubStatus() error = %v", runErr)
			}
			if stderr != "" {
				t.Fatalf("--json must keep stderr empty, got %q", stderr)
			}

			var raw map[string]json.RawMessage
			if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
				t.Fatalf("stdout is not one JSON object: %v\n%s", err, stdout)
			}
			for _, key := range []string{"connected", "repository_count", "repository", "project"} {
				if _, ok := raw[key]; !ok {
					t.Fatalf("stable key %q missing from stdout:\n%s", key, stdout)
				}
			}
			if _, ok := raw["project_error"]; ok {
				t.Fatalf("project_error must be omitted for a readable project:\n%s", stdout)
			}

			var project map[string]string
			if err := json.Unmarshal(raw["project"], &project); err != nil {
				t.Fatalf("project is not an object: %v\n%s", err, stdout)
			}
			if project["root"] != "." || project["status"] != testCase.wantStatus {
				t.Fatalf("project = %v, want root=. status=%s", project, testCase.wantStatus)
			}
			authority, hasAuthority := project["authority"]
			if testCase.wantAuthority == "" && hasAuthority {
				t.Fatalf("authority must be omitted for %s, got %q", testCase.wantStatus, authority)
			}
			if testCase.wantAuthority != "" && authority != testCase.wantAuthority {
				t.Fatalf("authority = %q, want %q", authority, testCase.wantAuthority)
			}
		})
	}
}
