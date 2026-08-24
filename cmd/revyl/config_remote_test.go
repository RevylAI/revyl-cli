package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/spf13/cobra"
)

const configRemoteProjectID = "11111111-1111-4111-8111-111111111111"

type fakeProjectConfigurationClient struct {
	catalogResult      *api.RepositoryProjectCatalogResponse
	catalogErr         error
	catalogCalls       int
	catalogRequest     api.RepositoryProjectCatalogQuery
	readResult         *api.ProjectConfigurationReadResponse
	readResults        []*api.ProjectConfigurationReadResponse
	readProjectID      string
	readProjectIDs     []string
	readRequest        api.ProjectConfigurationReadRequest
	readCalls          int
	validateResult     *api.ProjectConfigurationValidateResponse
	replaceResult      *api.ProjectConfigurationReplaceResponse
	replaceRequest     *api.ProjectConfigurationReplaceRequest
	readErr            error
	readErrors         []error
	validateErr        error
	replaceErr         error
	authorizeResult    *api.ProjectCursorProofAuthorizationResponse
	authorizeErr       error
	authorizeCalls     int
	authorizeProjectID string
	githubResult       *api.GithubRepositoriesResponse
	githubErr          error
	githubCalls        int
}

func (f *fakeProjectConfigurationClient) ListRepositoryProjects(
	_ context.Context,
	request api.RepositoryProjectCatalogQuery,
) (*api.RepositoryProjectCatalogResponse, error) {
	f.catalogCalls++
	f.catalogRequest = request
	return f.catalogResult, f.catalogErr
}

func (f *fakeProjectConfigurationClient) ReadProjectConfiguration(
	_ context.Context, projectID string, request api.ProjectConfigurationReadRequest,
) (*api.ProjectConfigurationReadResponse, error) {
	f.readCalls++
	f.readProjectID = projectID
	f.readProjectIDs = append(f.readProjectIDs, projectID)
	f.readRequest = request
	index := f.readCalls - 1
	if index < len(f.readResults) || index < len(f.readErrors) {
		var result *api.ProjectConfigurationReadResponse
		var err error
		if index < len(f.readResults) {
			result = f.readResults[index]
		}
		if index < len(f.readErrors) {
			err = f.readErrors[index]
		}
		return result, err
	}
	return f.readResult, f.readErr
}

func (f *fakeProjectConfigurationClient) ValidateProjectConfiguration(
	context.Context, string, api.ProjectConfigurationValidateRequest,
) (*api.ProjectConfigurationValidateResponse, error) {
	return f.validateResult, f.validateErr
}

func (f *fakeProjectConfigurationClient) ReplaceProjectConfiguration(
	_ context.Context,
	_ string,
	request api.ProjectConfigurationReplaceRequest,
) (*api.ProjectConfigurationReplaceResponse, error) {
	f.replaceRequest = &request
	return f.replaceResult, f.replaceErr
}

func (f *fakeProjectConfigurationClient) GetProjectCursorProofAuthorization(
	context.Context, string,
) (*api.ProjectCursorProofAuthorizationResponse, error) {
	return nil, nil
}

func (f *fakeProjectConfigurationClient) AuthorizeProjectCursorProof(
	_ context.Context, projectID string,
) (*api.ProjectCursorProofAuthorizationResponse, error) {
	f.authorizeCalls++
	f.authorizeProjectID = projectID
	return f.authorizeResult, f.authorizeErr
}

func (f *fakeProjectConfigurationClient) GetGithubRepositories(
	context.Context,
) (*api.GithubRepositoriesResponse, error) {
	f.githubCalls++
	return f.githubResult, f.githubErr
}

func configContext(t *testing.T, idleTimeout int) *config.ProjectContext {
	t.Helper()
	authored := config.AuthoredConfig{
		Project: config.AuthoredProject{ID: configRemoteProjectID},
		Session: &config.AuthoredSession{IdleTimeoutSeconds: &idleTimeout},
	}
	raw, err := config.MarshalCanonicalConfig(authored)
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := config.NormalizeAuthoredConfig(
		authored,
		config.CompilationContext{
			RepositoryRelativeProjectRoot: ".",
			ExecutionDirectory:            ".",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return &config.ProjectContext{
		WorktreeRoot:                         t.TempDir(),
		ConfigPath:                           "/repo/.revyl/config.yaml",
		RepositoryRelativeProjectRoot:        ".",
		RepositoryRelativeExecutionDirectory: ".",
		Authored:                             &authored,
		Aggregate:                            aggregate,
		OriginalBytes:                        raw,
	}
}

func withProjectConfigurationDependencies(
	t *testing.T,
	local *config.ProjectContext,
	token string,
	client projectConfigurationClient,
) {
	t.Helper()
	originalResolve := resolveProjectContext
	originalSlug := resolveProjectRepoSlug
	originalToken := readActiveConfigToken
	originalClient := newProjectConfigClient
	resolveProjectContext = func(string, string) (*config.ProjectContext, error) {
		return local, nil
	}
	resolveProjectRepoSlug = func(string, string) (string, string, error) {
		return "acme", "mobile", nil
	}
	readActiveConfigToken = func() (string, error) { return token, nil }
	newProjectConfigClient = func(string, bool) projectConfigurationClient { return client }
	t.Cleanup(func() {
		resolveProjectContext = originalResolve
		resolveProjectRepoSlug = originalSlug
		readActiveConfigToken = originalToken
		newProjectConfigClient = originalClient
	})
}

func testConfigCommand() *cobra.Command {
	command := &cobra.Command{}
	command.Flags().Bool("json", false, "")
	command.Flags().Bool("dev", false, "")
	return command
}

func TestConfigValidateWithoutAuthenticationStaysLocal(t *testing.T) {
	local := configContext(t, 300)
	clientCreated := false
	withProjectConfigurationDependencies(t, local, "", nil)
	originalClient := newProjectConfigClient
	newProjectConfigClient = func(string, bool) projectConfigurationClient {
		clientCreated = true
		return nil
	}
	resolveProjectRepoSlug = func(string, string) (string, string, error) {
		return "", "", context.Canceled
	}
	t.Cleanup(func() { newProjectConfigClient = originalClient })

	if err := runConfigValidate(testConfigCommand(), nil); err != nil {
		t.Fatalf("runConfigValidate() error = %v", err)
	}
	if clientCreated {
		t.Fatal("local validation created a server client")
	}
}

func TestConfigValidateJSONUsesOneSchemaForLocalAndConnected(t *testing.T) {
	local := configContext(t, 300)
	localCommand := testConfigCommand()
	_ = localCommand.Flags().Set("json", "true")
	withProjectConfigurationDependencies(t, local, "", nil)
	var localErr error
	localOutput := captureStdout(t, func() {
		localErr = runConfigValidate(localCommand, nil)
	})
	if localErr != nil {
		t.Fatal(localErr)
	}

	client := &fakeProjectConfigurationClient{
		validateResult: &api.ProjectConfigurationValidateResponse{
			Status:                            "valid",
			CandidateProjectConfigurationHash: local.Aggregate.ProjectConfigurationHash,
			Current: api.ProjectConfigurationReadResponse{
				State: api.ProjectConfigurationReadResponseStateAbsent,
			},
		},
	}
	withProjectConfigurationDependencies(t, local, "token", client)
	connectedCommand := testConfigCommand()
	_ = connectedCommand.Flags().Set("json", "true")
	var connectedErr error
	connectedOutput := captureStdout(t, func() {
		connectedErr = runConfigValidate(connectedCommand, nil)
	})
	if connectedErr != nil {
		t.Fatal(connectedErr)
	}

	decode := func(raw string) map[string]any {
		var value map[string]any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	localJSON := decode(localOutput)
	connectedJSON := decode(connectedOutput)
	localKeys := make([]string, 0, len(localJSON))
	connectedKeys := make([]string, 0, len(connectedJSON))
	for key := range localJSON {
		localKeys = append(localKeys, key)
	}
	for key := range connectedJSON {
		connectedKeys = append(connectedKeys, key)
	}
	sort.Strings(localKeys)
	sort.Strings(connectedKeys)
	if !reflect.DeepEqual(localKeys, connectedKeys) {
		t.Fatalf("local keys = %v, connected keys = %v", localKeys, connectedKeys)
	}
	if localJSON["current_state"] != nil || localJSON["authority"] != nil {
		t.Fatalf("local connected fields = %#v", localJSON)
	}
	if connectedJSON["current_state"] != "absent" {
		t.Fatalf("connected current_state = %#v", connectedJSON["current_state"])
	}
	localConnected := localJSON["connected"].(map[string]any)
	connectedConnected := connectedJSON["connected"].(map[string]any)
	if localConnected["status"] != "skipped" || connectedConnected["status"] != "succeeded" {
		t.Fatalf("connected sections = local %#v connected %#v", localConnected, connectedConnected)
	}
	if localJSON["scope"] != "local" || connectedJSON["scope"] != "local" {
		t.Fatalf("validation scopes = local %#v connected %#v", localJSON["scope"], connectedJSON["scope"])
	}
}

func TestConfigValidateAuthenticatedWithoutGithubRemoteStillReportsLocal(t *testing.T) {
	local := configContext(t, 300)
	withProjectConfigurationDependencies(t, local, "token", nil)
	resolveProjectRepoSlug = func(string, string) (string, string, error) {
		return "", "", errors.New("no supported GitHub remote")
	}
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	output := captureStdout(t, func() {
		if err := runConfigValidate(command, nil); err != nil {
			t.Fatal(err)
		}
	})
	var decoded projectConfigurationValidationOutput
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Status != "valid" || decoded.CandidateProjectConfigurationHash != local.Aggregate.ProjectConfigurationHash || decoded.Connected.Status != "skipped" {
		t.Fatalf("validation output = %#v", decoded)
	}
	addOrigin := gitRecoveryCommand(local.WorktreeRoot, "remote", "add", "origin", "https://github.com/<owner>/<repository>.git")
	setOrigin := gitRecoveryCommand(local.WorktreeRoot, "remote", "set-url", "origin", "https://github.com/<owner>/<repository>.git")
	if !strings.Contains(decoded.Connected.NextAction, strconv.Quote(addOrigin)) ||
		!strings.Contains(decoded.Connected.NextAction, strconv.Quote(setOrigin)) {
		t.Fatalf("connected next action = %q, want exact add and set-url commands", decoded.Connected.NextAction)
	}
}

func TestConfigValidateConnectedFailureStillEmitsLocalJSON(t *testing.T) {
	local := configContext(t, 300)
	withProjectConfigurationDependencies(t, local, "token", &fakeProjectConfigurationClient{
		validateErr: errors.New("server unavailable"),
	})
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	var validationErr error
	output := captureStdout(t, func() {
		validationErr = runConfigValidate(command, nil)
	})
	if validationErr == nil {
		t.Fatal("connected failure returned nil")
	}
	var decoded projectConfigurationValidationOutput
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("local validation report is not JSON: %v\n%s", err, output)
	}
	if decoded.Status != "valid" || decoded.CandidateProjectConfigurationHash != local.Aggregate.ProjectConfigurationHash || decoded.Connected.Status != "failed" {
		t.Fatalf("validation output = %#v", decoded)
	}
}

func TestConfigValidateConnectedAppFailureReportsExactRecoveryInJSON(t *testing.T) {
	local := configContext(t, 300)
	detail := "configuration.build.profiles.<profile-0>.ios.app_id: Select an active iOS app accessible to this organization, then republish."
	cleanDetail := "configuration.build.profiles.<profile-0>.ios.app_id: Select an active iOS app accessible to this organization"
	withProjectConfigurationDependencies(t, local, "token", &fakeProjectConfigurationClient{
		validateErr: &api.APIError{
			StatusCode: 422,
			Message:    "Validation error",
			Detail:     detail,
			ValidationIssues: []api.APIValidationIssue{{
				Field:   "configuration.build.profiles.<profile-0>.ios.app_id",
				Message: "Select an active iOS app accessible to this organization, then republish.",
				Type:    "referenced_app_not_available",
			}},
		},
	})
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	var validationErr error
	output := captureStdout(t, func() {
		validationErr = runConfigValidate(command, nil)
	})
	if validationErr == nil || !strings.Contains(validationErr.Error(), "revyl app list --platform ios") {
		t.Fatalf("validation error = %v", validationErr)
	}
	var decoded projectConfigurationValidationOutput
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("validation report is not JSON: %v\n%s", err, output)
	}
	if decoded.Connected.Explanation != cleanDetail || !strings.Contains(decoded.Connected.NextAction, "revyl app list --platform ios") {
		t.Fatalf("connected validation = %#v", decoded.Connected)
	}
}

func TestConfigValidateHasNoLocalModeFlag(t *testing.T) {
	if flag := configValidateCmd.Flags().Lookup("local"); flag != nil {
		t.Fatalf("unexpected compatibility flag = %#v", flag)
	}
}

func TestConfigPushUsesExplicitAbsentPrecondition(t *testing.T) {
	local := configContext(t, 300)
	client := &fakeProjectConfigurationClient{
		readResult: &api.ProjectConfigurationReadResponse{
			State: api.ProjectConfigurationReadResponseStateAbsent,
		},
		replaceResult: &api.ProjectConfigurationReplaceResponse{
			Outcome: api.ProjectConfigurationReplaceResponseOutcomeApplied,
		},
	}
	withProjectConfigurationDependencies(t, local, "token", client)

	if err := runConfigPush(testConfigCommand(), nil); err != nil {
		t.Fatalf("runConfigPush() error = %v", err)
	}
	if client.replaceRequest == nil {
		t.Fatal("replace request was not sent")
	}
	absent, err := client.replaceRequest.Precondition.AsProjectConfigurationAbsentPrecondition()
	if err != nil || absent.State != "absent" {
		t.Fatalf("precondition = %#v, error = %v", absent, err)
	}
}

func TestConfigPushExposesForceFlag(t *testing.T) {
	flag := configPushCmd.Flags().Lookup("force")
	if flag == nil || flag.DefValue != "false" {
		t.Fatalf("force flag = %#v", flag)
	}
}

func TestConfigPushRefusesGitAuthorityBeforeReplace(t *testing.T) {
	local := configContext(t, 300)
	client := &fakeProjectConfigurationClient{
		readResult: &api.ProjectConfigurationReadResponse{
			State: api.ProjectConfigurationReadResponseStatePresent,
			Resource: &api.ProjectConfigurationResource{
				Authority: api.ConfigurationAuthorityGitDefaultBranch,
			},
		},
	}
	withProjectConfigurationDependencies(t, local, "token", client)

	err := runConfigPush(testConfigCommand(), nil)
	if err == nil || client.replaceRequest != nil ||
		!strings.Contains(err.Error(), "revyl config push --force") {
		t.Fatalf("runConfigPush() error = %v, replace = %#v", err, client.replaceRequest)
	}
}

func TestConfigPushForcePublishesWithoutChangingGitAuthority(t *testing.T) {
	local := configContext(t, 300)
	client := &fakeProjectConfigurationClient{
		readResult: &api.ProjectConfigurationReadResponse{
			State: api.ProjectConfigurationReadResponseStatePresent,
			Resource: &api.ProjectConfigurationResource{
				Authority:                api.ConfigurationAuthorityGitDefaultBranch,
				ProjectConfigurationHash: "observed-hash",
			},
		},
		replaceResult: &api.ProjectConfigurationReplaceResponse{
			Outcome: api.ProjectConfigurationReplaceResponseOutcomeApplied,
			Resource: api.ProjectConfigurationResource{
				Authority: api.ConfigurationAuthorityGitDefaultBranch,
			},
		},
	}
	withProjectConfigurationDependencies(t, local, "token", client)
	configPushForce = true
	t.Cleanup(func() { configPushForce = false })

	if err := runConfigPush(testConfigCommand(), nil); err != nil {
		t.Fatalf("runConfigPush() error = %v", err)
	}
	if client.replaceRequest == nil || client.replaceRequest.Force == nil ||
		!*client.replaceRequest.Force {
		t.Fatalf("replace force = %#v", client.replaceRequest)
	}
	if client.replaceResult.Resource.Authority != api.ConfigurationAuthorityGitDefaultBranch {
		t.Fatalf("authority = %q", client.replaceResult.Resource.Authority)
	}
}

func TestConfigPushMapsConcurrentAndAuthorityConflicts(t *testing.T) {
	for _, test := range []struct {
		name string
		code string
		want string
	}{
		{
			name: "concurrent replacement",
			code: "observed_configuration_changed",
			want: "revyl config pull",
		},
		{
			name: "authority changed",
			code: "git_authority_rejects_manual_write",
			want: "revyl config validate",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			local := configContext(t, 300)
			client := &fakeProjectConfigurationClient{
				readResult: &api.ProjectConfigurationReadResponse{
					State: api.ProjectConfigurationReadResponseStateAbsent,
				},
				replaceErr: &api.APIError{StatusCode: 409, Code: test.code},
			}
			withProjectConfigurationDependencies(t, local, "token", client)

			err := runConfigPush(testConfigCommand(), nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("runConfigPush() error = %v, want %q", err, test.want)
			}
			if test.code == "observed_configuration_changed" &&
				(!strings.Contains(err.Error(), `retry "revyl config push"`) ||
					strings.Contains(err.Error(), "--force")) {
				t.Fatalf("ordinary push recovery changed: %v", err)
			}
		})
	}
}

func TestConfigPushForcePreservesForceInConcurrentConflictRecovery(t *testing.T) {
	local := configContext(t, 300)
	client := &fakeProjectConfigurationClient{
		readResult: &api.ProjectConfigurationReadResponse{
			State: api.ProjectConfigurationReadResponseStatePresent,
			Resource: &api.ProjectConfigurationResource{
				Authority:                api.ConfigurationAuthorityGitDefaultBranch,
				ProjectConfigurationHash: "observed-hash",
			},
		},
		replaceErr: &api.APIError{StatusCode: 409, Code: "observed_configuration_changed"},
	}
	withProjectConfigurationDependencies(t, local, "token", client)
	configPushForce = true
	t.Cleanup(func() { configPushForce = false })

	err := runConfigPush(testConfigCommand(), nil)
	if err == nil || !strings.Contains(err.Error(), `retry "revyl config push --force"`) {
		t.Fatalf("runConfigPush() error = %v", err)
	}
}

func TestConnectedConfigurationCommandsExplainRemovedProjectWithoutAnID(t *testing.T) {
	projectRoot := t.TempDir()
	local := configContext(t, 300)
	local.ProjectRoot = projectRoot
	local.ConfigPath = filepath.Join(projectRoot, ".revyl", "config.yaml")
	removed := &api.APIError{StatusCode: 409, Code: "project_removed"}

	for _, test := range []struct {
		name string
		run  func(*fakeProjectConfigurationClient) error
	}{
		{
			name: "validate",
			run: func(client *fakeProjectConfigurationClient) error {
				client.validateErr = removed
				return runConfigValidate(testConfigCommand(), nil)
			},
		},
		{
			name: "push",
			run: func(client *fakeProjectConfigurationClient) error {
				client.readErr = removed
				return runConfigPush(testConfigCommand(), nil)
			},
		},
		{
			name: "forced push",
			run: func(client *fakeProjectConfigurationClient) error {
				client.readErr = removed
				configPushForce = true
				defer func() { configPushForce = false }()
				return runConfigPush(testConfigCommand(), nil)
			},
		},
		{
			name: "cursor authorization",
			run: func(client *fakeProjectConfigurationClient) error {
				client.authorizeErr = removed
				return runConfigAuthorizeCursorProof(testConfigCommand(), nil)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeProjectConfigurationClient{}
			withProjectConfigurationDependencies(t, local, "token", client)
			err := test.run(client)
			if err == nil {
				t.Fatal("error = nil")
			}
			for _, want := range []string{
				"was deleted",
				"still works for local-only commands",
				"create a replacement project",
				"revyl -C <replacement-root> config pull",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want %q", err, want)
				}
			}
			if strings.Contains(err.Error(), configRemoteProjectID) || strings.Contains(err.Error(), "--force") {
				t.Fatalf("removed-project recovery exposed identity or force bypass: %v", err)
			}
		})
	}
}

func TestConfigPushDistinguishesRepositoryAccessFailures(t *testing.T) {
	connected := &api.GithubRepositoriesResponse{
		Installation:             &api.GithubOrgInstallation{Status: "active"},
		HasAccess:                true,
		GithubIntegrationEnabled: true,
	}
	for _, test := range []struct {
		name          string
		github        *api.GithubRepositoriesResponse
		catalog       *api.RepositoryProjectCatalogResponse
		want          string
		wantSecondary string
	}{
		{
			name: "github app disconnected",
			github: &api.GithubRepositoriesResponse{
				GithubIntegrationEnabled: true,
			},
			want:          "revyl github connect",
			wantSecondary: "revyl auth status",
		},
		{
			name: "github integration disabled for organization",
			github: &api.GithubRepositoriesResponse{
				Installation: connected.Installation,
				HasAccess:    true,
			},
			want:          "revyl github status",
			wantSecondary: "contact Revyl",
		},
		{
			name:   "repository access missing",
			github: connected,
			want:   "grant that repository",
		},
		{
			name: "different project registered at root",
			github: &api.GithubRepositoriesResponse{
				Installation:             connected.Installation,
				HasAccess:                true,
				GithubIntegrationEnabled: true,
				Repositories: []api.GithubOrgRepository{{
					Owner: "acme",
					Repo:  "mobile",
				}},
			},
			catalog: &api.RepositoryProjectCatalogResponse{
				Projects: []api.RepositoryProjectCatalogItem{{
					ProjectId:                     uuid.MustParse("22222222-2222-4222-8222-222222222222"),
					RepositoryRelativeProjectRoot: ".",
				}},
			},
			want:          "different Revyl project",
			wantSecondary: "revyl config pull --project 22222222-2222-4222-8222-222222222222",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			local := configContext(t, 300)
			client := &fakeProjectConfigurationClient{
				readErr: &api.APIError{
					StatusCode: 404,
					Code:       "project_configuration_inaccessible",
					Message:    "Project configuration operation could not be completed",
				},
				githubResult:  test.github,
				catalogResult: test.catalog,
			}
			withProjectConfigurationDependencies(t, local, "token", client)

			var pushErr error
			stdout := captureStdout(t, func() {
				pushErr = runConfigPush(testConfigCommand(), nil)
			})
			if pushErr == nil || !strings.Contains(pushErr.Error(), test.want) ||
				(test.wantSecondary != "" && !strings.Contains(pushErr.Error(), test.wantSecondary)) {
				t.Fatalf("runConfigPush() error = %v, want %q and %q", pushErr, test.want, test.wantSecondary)
			}
			if strings.Contains(pushErr.Error(), "could not be completed") {
				t.Fatalf("generic API message leaked: %v", pushErr)
			}
			if stdout != "" {
				t.Fatalf("failed push wrote stdout = %q", stdout)
			}
		})
	}
}

func TestProjectConfigurationWrongRootRecoveryUsesRegisteredRoot(t *testing.T) {
	worktreeRoot := t.TempDir()
	wrongRoot := filepath.Join(worktreeRoot, "apps", "wrong")
	registeredRoot := filepath.Join(worktreeRoot, "apps", "mobile")
	local := configContext(t, 300)
	local.WorktreeRoot = worktreeRoot
	client := &fakeProjectConfigurationClient{
		githubResult: &api.GithubRepositoriesResponse{
			Installation:             &api.GithubOrgInstallation{Status: "active"},
			HasAccess:                true,
			GithubIntegrationEnabled: true,
			Repositories: []api.GithubOrgRepository{{
				Owner: "acme",
				Repo:  "mobile",
			}},
		},
		catalogResult: &api.RepositoryProjectCatalogResponse{
			Projects: []api.RepositoryProjectCatalogItem{{
				ProjectId:                     uuid.MustParse(configRemoteProjectID),
				RepositoryRelativeProjectRoot: "apps/mobile",
			}},
		},
	}
	originalRoot, _ := rootCmd.PersistentFlags().GetString("chdir")
	_ = rootCmd.PersistentFlags().Set("chdir", wrongRoot)
	t.Cleanup(func() { _ = rootCmd.PersistentFlags().Set("chdir", originalRoot) })

	recovery := actionableProjectConfigurationAPIError(
		context.Background(),
		client,
		api.ProjectConfigurationRepositoryLocator{
			Provider:                      "github",
			Namespace:                     "acme",
			RepositoryName:                "mobile",
			RepositoryRelativeProjectRoot: "apps/wrong",
		},
		configRemoteProjectID,
		&api.APIError{StatusCode: 404, Code: "project_configuration_inaccessible"},
		cliRecoveryCommand("config", "pull"),
		local,
	)
	expectedCommand := cliRecoveryCommandInDirectory(
		registeredRoot,
		"config",
		"pull",
		"--project",
		configRemoteProjectID,
	)
	if !strings.Contains(recovery.Error(), strconv.Quote(expectedCommand)) {
		t.Fatalf("recovery = %v, want %q", recovery, expectedCommand)
	}
	wrongCommand := cliRecoveryCommandInDirectory(wrongRoot, "config", "pull", "--project", configRemoteProjectID)
	if strings.Contains(recovery.Error(), strconv.Quote(wrongCommand)) {
		t.Fatalf("recovery reused mismatched root: %v", recovery)
	}
}

func TestProjectCatalogLimitRecoveryDoesNotInventABypass(t *testing.T) {
	recovery := actionableProjectConfigurationAPIError(
		context.Background(),
		&fakeProjectConfigurationClient{},
		api.ProjectConfigurationRepositoryLocator{},
		configRemoteProjectID,
		&api.APIError{StatusCode: 409, Code: "repository_projects_limit_exceeded"},
		"revyl config push",
	)
	if !strings.Contains(recovery.Error(), "contact Revyl support") || !strings.Contains(recovery.Error(), "revyl config push") {
		t.Fatalf("recovery = %v", recovery)
	}
	if strings.Contains(recovery.Error(), "config pull --project") || strings.Contains(recovery.Error(), "config push --project") {
		t.Fatalf("recovery invented a catalog bypass: %v", recovery)
	}
}

func TestProjectConfigurationPayloadLimitRecoveryDoesNotRetryUnchangedPayload(t *testing.T) {
	recovery := actionableProjectConfigurationAPIError(
		context.Background(),
		&fakeProjectConfigurationClient{},
		api.ProjectConfigurationRepositoryLocator{},
		configRemoteProjectID,
		&api.APIError{StatusCode: 413, Code: "project_configuration_payload_too_large"},
		"revyl config push",
	)
	for _, want := range []string{"request-size limit", "revyl config path", "reduce its size", "revyl config validate", "revyl config push"} {
		if !strings.Contains(recovery.Error(), want) {
			t.Fatalf("recovery = %v, want %q", recovery, want)
		}
	}
}

func TestProjectConfigurationForbiddenRecoveryIsOperationNeutral(t *testing.T) {
	for _, retryCommand := range []string{"revyl config validate", "revyl config pull", "revyl config push"} {
		recovery := actionableProjectConfigurationAPIError(
			context.Background(),
			&fakeProjectConfigurationClient{},
			api.ProjectConfigurationRepositoryLocator{},
			configRemoteProjectID,
			&api.APIError{StatusCode: 403},
			retryCommand,
		)
		if !strings.Contains(recovery.Error(), "cannot access this project") || strings.Contains(recovery.Error(), "cannot update") {
			t.Fatalf("recovery for %q = %v", retryCommand, recovery)
		}
	}
}

func TestProjectConfigurationAppReferenceRecoveryNamesResourceAndCommands(t *testing.T) {
	for _, retryCommand := range []string{"revyl config push", "revyl config validate"} {
		recovery := actionableProjectConfigurationAPIError(
			context.Background(),
			&fakeProjectConfigurationClient{},
			api.ProjectConfigurationRepositoryLocator{},
			configRemoteProjectID,
			&api.APIError{
				StatusCode: 422,
				Detail:     "configuration.build.profiles.<profile-0>.ios.app_id: Select an active iOS app accessible to this organization, then republish.",
				ValidationIssues: []api.APIValidationIssue{{
					Field:   "configuration.build.profiles.<profile-0>.ios.app_id",
					Message: "Select an active iOS app accessible to this organization, then republish.",
					Type:    "referenced_app_not_available",
				}},
			},
			retryCommand,
		)
		for _, want := range []string{"configuration.build.profiles.<profile-0>.ios.app_id", "revyl app list --platform ios", "revyl config validate"} {
			if !strings.Contains(recovery.Error(), want) {
				t.Fatalf("recovery for %q = %v, want %q", retryCommand, recovery, want)
			}
		}
		if strings.Contains(recovery.Error(), "connectivity") || strings.Contains(recovery.Error(), "repository access") {
			t.Fatalf("recovery for %q misdiagnosed reference failure: %v", retryCommand, recovery)
		}
		if strings.Contains(recovery.Error(), "republish.;") || strings.Contains(recovery.Error(), "then then") {
			t.Fatalf("recovery for %q has malformed wording: %v", retryCommand, recovery)
		}
	}
}

func TestProjectConfigurationMissingManagedAppRecoveryUsesLocalProfilePath(t *testing.T) {
	commands := []string{"build"}
	profile := "pr-review"
	authored := config.AuthoredConfig{
		Project: config.AuthoredProject{ID: configRemoteProjectID},
		Build: &config.AuthoredBuild{
			Framework: "ios",
			Profiles: map[string]config.AuthoredBuildProfile{
				profile: {IOS: &config.AuthoredBuildRecipe{BuildCommands: &commands}},
			},
		},
		PRReview: &config.AuthoredPRReview{
			Build: config.AuthoredReviewBuild{Kind: "revyl", Profile: &profile},
		},
	}
	aggregate, err := config.NormalizeAuthoredConfig(authored, config.CompilationContext{
		RepositoryRelativeProjectRoot: ".",
		ExecutionDirectory:            ".",
	})
	if err != nil {
		t.Fatal(err)
	}
	local := &config.ProjectContext{Authored: &authored, Aggregate: aggregate}

	for _, retryCommand := range []string{"revyl config validate", "revyl config push"} {
		recovery := actionableProjectConfigurationAPIError(
			context.Background(),
			&fakeProjectConfigurationClient{},
			api.ProjectConfigurationRepositoryLocator{},
			configRemoteProjectID,
			&api.APIError{StatusCode: 422, Code: "invalid_project_configuration"},
			retryCommand,
			local,
		)
		for _, want := range []string{
			"build.profiles.pr-review.ios.app_id",
			"revyl app list --platform ios",
			"revyl config validate",
		} {
			if !strings.Contains(recovery.Error(), want) {
				t.Fatalf("recovery for %q = %v, want %q", retryCommand, recovery, want)
			}
		}
		if strings.Contains(recovery.Error(), "repository-bound configuration reference") || strings.Contains(recovery.Error(), "then then") {
			t.Fatalf("recovery for %q is generic or malformed: %v", retryCommand, recovery)
		}
	}
}

func TestProjectConfigurationValidationDetailRemovesRepublishWording(t *testing.T) {
	detail := "first: Add commands, then republish.; second: Set output, then republish."
	got := projectConfigurationValidationDetail(detail)
	if got != "first: Add commands; second: Set output" {
		t.Fatalf("detail = %q", got)
	}
}

func TestProjectConfigurationValidateAndRetryGrammar(t *testing.T) {
	if got := projectConfigurationValidateAndRetry("revyl config validate", "revyl config validate"); got != `retry "revyl config validate"` {
		t.Fatalf("same-command recovery = %q", got)
	}
	if got := projectConfigurationValidateAndRetry("revyl config validate", "revyl config push"); got != `run "revyl config validate" and retry "revyl config push"` {
		t.Fatalf("cross-command recovery = %q", got)
	}
}

func TestProjectConfigurationUnavailableReferenceRecoveryUsesExistingListCommands(t *testing.T) {
	for _, test := range []struct {
		code string
		want string
	}{
		{code: "referenced_workflow_not_available", want: "revyl workflow list"},
		{code: "referenced_secret_not_available", want: "revyl build secret list"},
		{code: "referenced_launch_variable_not_available", want: "revyl global launch-var list"},
	} {
		recovery := actionableProjectConfigurationAPIError(
			context.Background(),
			&fakeProjectConfigurationClient{},
			api.ProjectConfigurationRepositoryLocator{},
			configRemoteProjectID,
			&api.APIError{StatusCode: 422, Code: test.code},
			"revyl config push",
		)
		if !strings.Contains(recovery.Error(), test.want) || !strings.Contains(recovery.Error(), "revyl config validate") {
			t.Fatalf("recovery for %q = %v", test.code, recovery)
		}
	}
}

func TestConfigAuthorizeCursorProofUsesCurrentProjectAndPrintsStableJSON(t *testing.T) {
	local := configContext(t, 300)
	authorizedAt := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	client := &fakeProjectConfigurationClient{
		authorizeResult: &api.ProjectCursorProofAuthorizationResponse{
			ProjectId:    uuid.MustParse(configRemoteProjectID),
			Required:     true,
			Authorized:   true,
			AuthorizedAt: &authorizedAt,
			Repository: api.ProjectCursorProofRepository{
				Provider:                      "github",
				Namespace:                     "Acme",
				RepositoryName:                "Mobile",
				RepositoryRelativeProjectRoot: ".",
			},
		},
	}
	withProjectConfigurationDependencies(t, local, "token", client)
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")

	var runErr error
	output := captureStdout(t, func() {
		runErr = runConfigAuthorizeCursorProof(command, nil)
	})

	if runErr != nil {
		t.Fatal(runErr)
	}
	if client.authorizeCalls != 1 || client.authorizeProjectID != configRemoteProjectID {
		t.Fatalf("authorize calls = %d, project = %q", client.authorizeCalls, client.authorizeProjectID)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["authorized"] != true || decoded["required"] != true {
		t.Fatalf("output = %#v", decoded)
	}
	repository := decoded["repository"].(map[string]any)
	if repository["repository_relative_project_root"] != "." {
		t.Fatalf("repository = %#v", repository)
	}
}

func TestConfigAuthorizeCursorProofMapsActionableCursorFailure(t *testing.T) {
	local := configContext(t, 300)
	client := &fakeProjectConfigurationClient{
		authorizeErr: &api.APIError{
			StatusCode: 400,
			Code:       "cursor_connection_unavailable",
		},
	}
	withProjectConfigurationDependencies(t, local, "token", client)

	err := runConfigAuthorizeCursorProof(testConfigCommand(), nil)
	if err == nil || !strings.Contains(err.Error(), "connect a usable Cursor API key") {
		t.Fatalf("runConfigAuthorizeCursorProof() error = %v", err)
	}
}

func TestConfigCommandWrapsCustomerErrorsWithSafeAnalyticsDiagnostic(t *testing.T) {
	originalResolve := resolveProjectContext
	resolveProjectContext = func(string, string) (*config.ProjectContext, error) {
		return nil, errors.New("profile customer-private-name is invalid")
	}
	t.Cleanup(func() { resolveProjectContext = originalResolve })

	err := configValidateCmd.RunE(testConfigCommand(), nil)
	var safeErr *analytics.SafeDiagnosticError
	if !errors.As(err, &safeErr) || err.Error() != "profile customer-private-name is invalid" {
		t.Fatalf("safe diagnostic error = %v", err)
	}
}

func TestConfigPullReplacesDivergenceAndReportsBackup(t *testing.T) {
	local := configContext(t, 300)
	serverLocal := configContext(t, 600)
	serverAuthored, err := authoredConfigForAPI(*serverLocal.Authored)
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeProjectConfigurationClient{
		readResult: &api.ProjectConfigurationReadResponse{
			State: api.ProjectConfigurationReadResponseStatePresent,
			Resource: &api.ProjectConfigurationResource{
				Configuration:            serverAuthored,
				ProjectConfigurationHash: serverLocal.Aggregate.ProjectConfigurationHash,
			},
		},
	}
	configDir := filepath.Join(t.TempDir(), ".revyl")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	local.ConfigPath = filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(local.ConfigPath, local.OriginalBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	withProjectConfigurationDependencies(t, local, "token", client)
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")

	var runErr error
	output := captureStdout(t, func() {
		runErr = runConfigPull(command, nil)
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["outcome"] != "replaced" {
		t.Fatalf("output = %#v", decoded)
	}
	backupPath, ok := decoded["backup_path"].(string)
	if !ok || backupPath == "" {
		t.Fatalf("backup path = %#v", decoded["backup_path"])
	}
	backup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(backup, local.OriginalBytes) {
		t.Fatalf("backup = %q, want %q", backup, local.OriginalBytes)
	}
	replaced, err := os.ReadFile(local.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replaced, serverLocal.OriginalBytes) {
		t.Fatalf("replacement = %q, want %q", replaced, serverLocal.OriginalBytes)
	}
}

func TestConfigPullReplacesConfirmedRemovedProjectWithSameRootProject(t *testing.T) {
	const replacementProjectID = "22222222-2222-4222-8222-222222222222"
	projectRoot := t.TempDir()
	configDir := filepath.Join(projectRoot, ".revyl")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	local := configContext(t, 300)
	local.WorktreeRoot = projectRoot
	local.ProjectRoot = projectRoot
	local.ConfigPath = filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(local.ConfigPath, local.OriginalBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	replacementTimeout := 600
	replacement := config.AuthoredConfig{
		Project: config.AuthoredProject{ID: replacementProjectID},
		Session: &config.AuthoredSession{IdleTimeoutSeconds: &replacementTimeout},
	}
	replacementBytes, err := config.MarshalCanonicalConfig(replacement)
	if err != nil {
		t.Fatal(err)
	}
	replacementAggregate, err := config.NormalizeAuthoredConfig(
		replacement,
		config.CompilationContext{
			RepositoryRelativeProjectRoot: ".",
			ExecutionDirectory:            ".",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	replacementAPI, err := authoredConfigForAPI(replacement)
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeProjectConfigurationClient{
		catalogResult: &api.RepositoryProjectCatalogResponse{
			Repository: api.RepositoryProjectCatalogRepository{
				Provider: "github", Namespace: "acme", RepositoryName: "mobile",
			},
			Projects: []api.RepositoryProjectCatalogItem{{
				ProjectId:                     uuid.MustParse(replacementProjectID),
				RepositoryRelativeProjectRoot: ".",
				RepositoryRelativeConfigPath:  ".revyl/config.yaml",
			}},
		},
		readErrors: []error{
			&api.APIError{StatusCode: 409, Code: "project_removed"},
			nil,
		},
		readResults: []*api.ProjectConfigurationReadResponse{
			nil,
			{
				State: api.ProjectConfigurationReadResponseStatePresent,
				Resource: &api.ProjectConfigurationResource{
					Configuration:                 replacementAPI,
					ProjectConfigurationHash:      replacementAggregate.ProjectConfigurationHash,
					Provider:                      "github",
					Namespace:                     "acme",
					RepositoryName:                "mobile",
					RepositoryRelativeProjectRoot: ".",
					RepositoryRelativeConfigPath:  ".revyl/config.yaml",
				},
			},
		},
	}
	withProjectConfigurationDependencies(t, local, "token", client)
	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")

	var runErr error
	output := captureStdout(t, func() {
		runErr = runConfigPull(command, nil)
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, output)
	}
	if decoded["outcome"] != "replaced" {
		t.Fatalf("output = %#v", decoded)
	}
	backupPath, ok := decoded["backup_path"].(string)
	if !ok || backupPath == "" {
		t.Fatalf("backup path = %#v", decoded["backup_path"])
	}
	backup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(backup, local.OriginalBytes) {
		t.Fatalf("backup = %q, want %q", backup, local.OriginalBytes)
	}
	got, err := os.ReadFile(local.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, replacementBytes) {
		t.Fatalf("replacement = %q, want %q", got, replacementBytes)
	}
	if client.catalogCalls != 1 || !reflect.DeepEqual(client.readProjectIDs, []string{configRemoteProjectID, replacementProjectID}) {
		t.Fatalf("catalog calls = %d, read IDs = %#v", client.catalogCalls, client.readProjectIDs)
	}
}

func TestConfigPullRemovedProjectDoesNotGuessReplacement(t *testing.T) {
	projectRoot := t.TempDir()
	local := configContext(t, 300)
	local.ProjectRoot = projectRoot
	local.ConfigPath = filepath.Join(projectRoot, ".revyl", "config.yaml")
	client := &fakeProjectConfigurationClient{
		catalogResult: &api.RepositoryProjectCatalogResponse{
			Repository: api.RepositoryProjectCatalogRepository{
				Provider: "github", Namespace: "acme", RepositoryName: "mobile",
			},
			Projects: []api.RepositoryProjectCatalogItem{{
				ProjectId:                     uuid.MustParse("22222222-2222-4222-8222-222222222222"),
				RepositoryRelativeProjectRoot: "apps/other",
				RepositoryRelativeConfigPath:  "apps/other/.revyl/config.yaml",
			}},
		},
		readErr: &api.APIError{StatusCode: 409, Code: "project_removed"},
	}
	withProjectConfigurationDependencies(t, local, "token", client)

	err := runConfigPull(testConfigCommand(), nil)
	if err == nil || !strings.Contains(err.Error(), "create a replacement project") ||
		!strings.Contains(err.Error(), "revyl -C <replacement-root> config pull") {
		t.Fatalf("error = %v", err)
	}
	if client.catalogCalls != 1 || client.readCalls != 1 {
		t.Fatalf("catalog calls = %d, read calls = %d", client.catalogCalls, client.readCalls)
	}
}

func TestConfigPullProjectAssertionDoesNotAdoptAfterRemoval(t *testing.T) {
	local := configContext(t, 300)
	client := &fakeProjectConfigurationClient{
		readErr: &api.APIError{StatusCode: 409, Code: "project_removed"},
		catalogResult: &api.RepositoryProjectCatalogResponse{
			Repository: api.RepositoryProjectCatalogRepository{
				Provider: "github", Namespace: "acme", RepositoryName: "mobile",
			},
			Projects: []api.RepositoryProjectCatalogItem{{
				ProjectId:                     uuid.MustParse("22222222-2222-4222-8222-222222222222"),
				RepositoryRelativeProjectRoot: ".",
				RepositoryRelativeConfigPath:  ".revyl/config.yaml",
			}},
		},
	}
	withProjectConfigurationDependencies(t, local, "token", client)
	withConfigPullProjectID(t, configRemoteProjectID)

	err := runConfigPull(testConfigCommand(), nil)
	if err == nil || !strings.Contains(err.Error(), "was deleted") {
		t.Fatalf("error = %v", err)
	}
	if client.catalogCalls != 0 || client.readCalls != 1 {
		t.Fatalf("catalog calls = %d, read calls = %d", client.catalogCalls, client.readCalls)
	}
}

func TestConfigPullGenericInaccessibleDoesNotAdoptReplacement(t *testing.T) {
	local := configContext(t, 300)
	client := &fakeProjectConfigurationClient{
		readErr: &api.APIError{StatusCode: 404, Code: "project_configuration_inaccessible"},
		catalogResult: &api.RepositoryProjectCatalogResponse{
			Repository: api.RepositoryProjectCatalogRepository{
				Provider: "github", Namespace: "acme", RepositoryName: "mobile",
			},
		},
	}
	withProjectConfigurationDependencies(t, local, "token", client)

	err := runConfigPull(testConfigCommand(), nil)
	if err == nil || strings.Contains(err.Error(), "was deleted") {
		t.Fatalf("error = %v", err)
	}
	if client.readCalls != 1 {
		t.Fatalf("read calls = %d", client.readCalls)
	}
}

func TestRemovedProjectReplacementRequiresOneDifferentActiveProjectAtExactRoot(t *testing.T) {
	resolved := &resolvedProjectConfiguration{
		local: configContext(t, 300),
		locator: api.ProjectConfigurationRepositoryLocator{
			Provider:                      "github",
			Namespace:                     "acme",
			RepositoryName:                "mobile",
			RepositoryRelativeProjectRoot: ".",
		},
	}
	validRepository := api.RepositoryProjectCatalogRepository{
		Provider: "github", Namespace: "acme", RepositoryName: "mobile",
	}
	for _, test := range []struct {
		name     string
		projects []api.RepositoryProjectCatalogItem
	}{
		{
			name: "deleted ID still active",
			projects: []api.RepositoryProjectCatalogItem{{
				ProjectId:                     uuid.MustParse(configRemoteProjectID),
				RepositoryRelativeProjectRoot: ".",
				RepositoryRelativeConfigPath:  ".revyl/config.yaml",
			}},
		},
		{
			name: "multiple exact-root projects",
			projects: []api.RepositoryProjectCatalogItem{
				{
					ProjectId:                     uuid.MustParse("22222222-2222-4222-8222-222222222222"),
					RepositoryRelativeProjectRoot: ".",
					RepositoryRelativeConfigPath:  ".revyl/config.yaml",
				},
				{
					ProjectId:                     uuid.MustParse("33333333-3333-4333-8333-333333333333"),
					RepositoryRelativeProjectRoot: ".",
					RepositoryRelativeConfigPath:  ".revyl/config.yaml",
				},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, found, err := sameRootReplacementPullTarget(
				&api.RepositoryProjectCatalogResponse{
					Repository: validRepository,
					Projects:   test.projects,
				},
				resolved,
			)
			if err == nil || found || !strings.Contains(err.Error(), "multiple or inconsistent") {
				t.Fatalf("found = %t, error = %v", found, err)
			}
		})
	}
}

func TestConfigPullBootstrapsNearestConfiguredProjectWithoutExposingItsID(t *testing.T) {
	const nestedProjectID = "22222222-2222-4222-8222-222222222222"
	worktreeRoot := t.TempDir()
	effectiveDirectory := filepath.Join(worktreeRoot, "apps", "mobile", "src")
	if err := os.MkdirAll(effectiveDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	authored := config.AuthoredConfig{
		Project: config.AuthoredProject{ID: nestedProjectID},
	}
	aggregate, err := config.NormalizeAuthoredConfig(
		authored,
		config.CompilationContext{
			RepositoryRelativeProjectRoot: "apps/mobile",
			ExecutionDirectory:            "apps/mobile",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	serverAuthored, err := authoredConfigForAPI(authored)
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeProjectConfigurationClient{
		catalogResult: &api.RepositoryProjectCatalogResponse{
			Repository: api.RepositoryProjectCatalogRepository{
				Provider: "github", Namespace: "acme", RepositoryName: "mobile",
			},
			Projects: []api.RepositoryProjectCatalogItem{
				{
					ProjectId:                     uuid.MustParse(configRemoteProjectID),
					RepositoryRelativeProjectRoot: ".",
					RepositoryRelativeConfigPath:  ".revyl/config.yaml",
				},
				{
					ProjectId:                     uuid.MustParse(nestedProjectID),
					RepositoryRelativeProjectRoot: "apps/mobile",
					RepositoryRelativeConfigPath:  "apps/mobile/.revyl/config.yaml",
				},
			},
		},
		readResult: &api.ProjectConfigurationReadResponse{
			State: api.ProjectConfigurationReadResponseStatePresent,
			Resource: &api.ProjectConfigurationResource{
				Configuration:                 serverAuthored,
				ProjectConfigurationHash:      aggregate.ProjectConfigurationHash,
				Provider:                      "github",
				Namespace:                     "acme",
				RepositoryName:                "mobile",
				RepositoryRelativeProjectRoot: "apps/mobile",
				RepositoryRelativeConfigPath:  "apps/mobile/.revyl/config.yaml",
			},
		},
	}
	withConfigPullBootstrapDependencies(t, effectiveDirectory, worktreeRoot, client)

	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	var runErr error
	output := captureStdout(t, func() {
		runErr = runConfigPull(command, nil)
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	if output != "{\"outcome\":\"created\"}\n" {
		t.Fatalf("output = %q", output)
	}
	if strings.Contains(output, nestedProjectID) {
		t.Fatalf("output exposed internal project ID: %q", output)
	}
	if client.catalogCalls != 1 || client.readCalls != 1 || client.readProjectID != nestedProjectID {
		t.Fatalf("catalog calls = %d read calls = %d project = %q", client.catalogCalls, client.readCalls, client.readProjectID)
	}
	if client.readRequest.Locator.RepositoryRelativeProjectRoot != "apps/mobile" {
		t.Fatalf("read locator = %#v", client.readRequest.Locator)
	}
	createdPath := filepath.Join(worktreeRoot, "apps", "mobile", ".revyl", "config.yaml")
	createdBytes, err := os.ReadFile(createdPath)
	if err != nil {
		t.Fatal(err)
	}
	created, err := config.ParseAuthoredConfig(createdBytes)
	if err != nil {
		t.Fatal(err)
	}
	if created.Project.ID != nestedProjectID {
		t.Fatalf("created project ID = %q", created.Project.ID)
	}
	if _, err := os.Stat(filepath.Join(worktreeRoot, ".revyl", "config.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repository-root config unexpectedly exists: %v", err)
	}
}

func TestConfigPullBootstrapsCloserConfiguredProjectThanLocalAncestor(t *testing.T) {
	const nestedProjectID = "22222222-2222-4222-8222-222222222222"
	worktreeRoot := t.TempDir()
	effectiveDirectory := filepath.Join(worktreeRoot, "apps", "mobile", "src")
	if err := os.MkdirAll(effectiveDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	authored := config.AuthoredConfig{
		Project: config.AuthoredProject{ID: nestedProjectID},
	}
	aggregate, err := config.NormalizeAuthoredConfig(
		authored,
		config.CompilationContext{
			RepositoryRelativeProjectRoot: "apps/mobile",
			ExecutionDirectory:            "apps/mobile",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	serverAuthored, err := authoredConfigForAPI(authored)
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeProjectConfigurationClient{
		catalogResult: &api.RepositoryProjectCatalogResponse{
			Repository: api.RepositoryProjectCatalogRepository{
				Provider: "github", Namespace: "acme", RepositoryName: "mobile",
			},
			Projects: []api.RepositoryProjectCatalogItem{
				{
					ProjectId:                     uuid.MustParse(configRemoteProjectID),
					RepositoryRelativeProjectRoot: ".",
					RepositoryRelativeConfigPath:  ".revyl/config.yaml",
				},
				{
					ProjectId:                     uuid.MustParse(nestedProjectID),
					RepositoryRelativeProjectRoot: "apps/mobile",
					RepositoryRelativeConfigPath:  "apps/mobile/.revyl/config.yaml",
				},
			},
		},
		readResult: &api.ProjectConfigurationReadResponse{
			State: api.ProjectConfigurationReadResponseStatePresent,
			Resource: &api.ProjectConfigurationResource{
				Configuration:                 serverAuthored,
				ProjectConfigurationHash:      aggregate.ProjectConfigurationHash,
				Provider:                      "github",
				Namespace:                     "acme",
				RepositoryName:                "mobile",
				RepositoryRelativeProjectRoot: "apps/mobile",
				RepositoryRelativeConfigPath:  "apps/mobile/.revyl/config.yaml",
			},
		},
	}
	local := configContext(t, 300)
	local.WorktreeRoot = worktreeRoot
	local.EffectiveDirectory = effectiveDirectory
	local.RepositoryRelativeExecutionDirectory = "apps/mobile/src"
	withProjectConfigurationDependencies(t, local, "token", client)
	originalRoot := resolveConfigPullRoot
	originalCWD := configWorkingDirectory
	resolveConfigPullRoot = func(string, string) (string, string, error) {
		return effectiveDirectory, worktreeRoot, nil
	}
	configWorkingDirectory = func() (string, error) { return effectiveDirectory, nil }
	t.Cleanup(func() {
		resolveConfigPullRoot = originalRoot
		configWorkingDirectory = originalCWD
	})

	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	if err := runConfigPull(command, nil); err != nil {
		t.Fatal(err)
	}
	if client.readProjectID != nestedProjectID {
		t.Fatalf("read project = %q", client.readProjectID)
	}
	createdPath := filepath.Join(worktreeRoot, "apps", "mobile", ".revyl", "config.yaml")
	createdBytes, err := os.ReadFile(createdPath)
	if err != nil {
		t.Fatal(err)
	}
	created, err := config.ParseAuthoredConfig(createdBytes)
	if err != nil {
		t.Fatal(err)
	}
	if created.Project.ID != nestedProjectID {
		t.Fatalf("created project ID = %q", created.Project.ID)
	}
}

func TestConfigPullExplicitProjectBootstrapsCloserConfiguredProjectThanLocalAncestor(t *testing.T) {
	const nestedProjectID = "22222222-2222-4222-8222-222222222222"
	local, client, effectiveDirectory, worktreeRoot := closerProjectPullFixture(
		t,
		nestedProjectID,
	)
	withProjectConfigurationDependencies(t, local, "token", client)
	withConfigPullFilesystemDependencies(t, effectiveDirectory, worktreeRoot)
	withConfigPullProjectID(t, nestedProjectID)

	command := testConfigCommand()
	_ = command.Flags().Set("json", "true")
	if err := runConfigPull(command, nil); err != nil {
		t.Fatal(err)
	}
	if client.catalogCalls != 1 || client.readCalls != 1 || client.readProjectID != nestedProjectID {
		t.Fatalf("catalog calls = %d read calls = %d project = %q", client.catalogCalls, client.readCalls, client.readProjectID)
	}
	createdPath := filepath.Join(worktreeRoot, "apps", "mobile", ".revyl", "config.yaml")
	createdBytes, err := os.ReadFile(createdPath)
	if err != nil {
		t.Fatal(err)
	}
	created, err := config.ParseAuthoredConfig(createdBytes)
	if err != nil {
		t.Fatal(err)
	}
	if created.Project.ID != nestedProjectID {
		t.Fatalf("created project ID = %q", created.Project.ID)
	}
}

func TestConfigPullExplicitProjectProtectsLocalAncestorWhenCloserProjectDoesNotMatch(t *testing.T) {
	const nestedProjectID = "22222222-2222-4222-8222-222222222222"
	const requestedProjectID = "33333333-3333-4333-8333-333333333333"
	local, client, effectiveDirectory, worktreeRoot := closerProjectPullFixture(
		t,
		nestedProjectID,
	)
	withProjectConfigurationDependencies(t, local, "token", client)
	withConfigPullFilesystemDependencies(t, effectiveDirectory, worktreeRoot)
	withConfigPullProjectID(t, requestedProjectID)

	err := runConfigPull(testConfigCommand(), nil)
	if err == nil || !strings.Contains(err.Error(), "--project does not match the local project ID") {
		t.Fatalf("error = %v", err)
	}
	if client.catalogCalls != 1 || client.readCalls != 0 {
		t.Fatalf("catalog calls = %d read calls = %d", client.catalogCalls, client.readCalls)
	}
	if _, err := os.Stat(filepath.Join(worktreeRoot, "apps", "mobile", ".revyl", "config.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nested config unexpectedly exists: %v", err)
	}
}

func TestConfigPullExplicitProjectProtectsApplicableLocalProject(t *testing.T) {
	const requestedProjectID = "33333333-3333-4333-8333-333333333333"
	local := configContext(t, 300)
	client := &fakeProjectConfigurationClient{}
	withProjectConfigurationDependencies(t, local, "token", client)
	withConfigPullProjectID(t, requestedProjectID)

	err := runConfigPull(testConfigCommand(), nil)
	if err == nil || !strings.Contains(err.Error(), "--project does not match the local project ID") {
		t.Fatalf("error = %v", err)
	}
	if client.catalogCalls != 0 || client.readCalls != 0 {
		t.Fatalf("catalog calls = %d read calls = %d", client.catalogCalls, client.readCalls)
	}
}

func closerProjectPullFixture(
	t *testing.T,
	nestedProjectID string,
) (*config.ProjectContext, *fakeProjectConfigurationClient, string, string) {
	t.Helper()
	worktreeRoot := t.TempDir()
	effectiveDirectory := filepath.Join(worktreeRoot, "apps", "mobile", "src")
	if err := os.MkdirAll(effectiveDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	authored := config.AuthoredConfig{
		Project: config.AuthoredProject{ID: nestedProjectID},
	}
	aggregate, err := config.NormalizeAuthoredConfig(
		authored,
		config.CompilationContext{
			RepositoryRelativeProjectRoot: "apps/mobile",
			ExecutionDirectory:            "apps/mobile",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	serverAuthored, err := authoredConfigForAPI(authored)
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeProjectConfigurationClient{
		catalogResult: &api.RepositoryProjectCatalogResponse{
			Repository: api.RepositoryProjectCatalogRepository{
				Provider: "github", Namespace: "acme", RepositoryName: "mobile",
			},
			Projects: []api.RepositoryProjectCatalogItem{
				{
					ProjectId:                     uuid.MustParse(configRemoteProjectID),
					RepositoryRelativeProjectRoot: ".",
					RepositoryRelativeConfigPath:  ".revyl/config.yaml",
				},
				{
					ProjectId:                     uuid.MustParse(nestedProjectID),
					RepositoryRelativeProjectRoot: "apps/mobile",
					RepositoryRelativeConfigPath:  "apps/mobile/.revyl/config.yaml",
				},
			},
		},
		readResult: &api.ProjectConfigurationReadResponse{
			State: api.ProjectConfigurationReadResponseStatePresent,
			Resource: &api.ProjectConfigurationResource{
				Configuration:                 serverAuthored,
				ProjectConfigurationHash:      aggregate.ProjectConfigurationHash,
				Provider:                      "github",
				Namespace:                     "acme",
				RepositoryName:                "mobile",
				RepositoryRelativeProjectRoot: "apps/mobile",
				RepositoryRelativeConfigPath:  "apps/mobile/.revyl/config.yaml",
			},
		},
	}
	local := configContext(t, 300)
	local.WorktreeRoot = worktreeRoot
	local.EffectiveDirectory = effectiveDirectory
	local.RepositoryRelativeExecutionDirectory = "apps/mobile/src"
	return local, client, effectiveDirectory, worktreeRoot
}

func withConfigPullFilesystemDependencies(
	t *testing.T,
	effectiveDirectory string,
	worktreeRoot string,
) {
	t.Helper()
	originalRoot := resolveConfigPullRoot
	originalCWD := configWorkingDirectory
	resolveConfigPullRoot = func(string, string) (string, string, error) {
		return effectiveDirectory, worktreeRoot, nil
	}
	configWorkingDirectory = func() (string, error) { return effectiveDirectory, nil }
	t.Cleanup(func() {
		resolveConfigPullRoot = originalRoot
		configWorkingDirectory = originalCWD
	})
}

func withConfigPullProjectID(t *testing.T, projectID string) {
	t.Helper()
	originalProjectID := configPullProjectID
	configPullProjectID = projectID
	t.Cleanup(func() { configPullProjectID = originalProjectID })
}

func TestConfigPullBootstrapRequiresConfiguredAncestorAndValidCatalogPaths(t *testing.T) {
	for _, test := range []struct {
		name        string
		projectRoot string
		configPath  string
		want        string
	}{
		{
			name:        "unrelated project",
			projectRoot: "apps/other",
			configPath:  "apps/other/.revyl/config.yaml",
			want:        "no configured project containing the current directory",
		},
		{
			name:        "invalid catalog path",
			projectRoot: "apps/mobile",
			configPath:  ".revyl/config.yaml",
			want:        "invalid repository project path",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			worktreeRoot := t.TempDir()
			effectiveDirectory := filepath.Join(worktreeRoot, "apps", "mobile")
			if err := os.MkdirAll(effectiveDirectory, 0o755); err != nil {
				t.Fatal(err)
			}
			client := &fakeProjectConfigurationClient{
				catalogResult: &api.RepositoryProjectCatalogResponse{
					Repository: api.RepositoryProjectCatalogRepository{
						Provider: "github", Namespace: "acme", RepositoryName: "mobile",
					},
					Projects: []api.RepositoryProjectCatalogItem{{
						ProjectId:                     uuid.MustParse(configRemoteProjectID),
						RepositoryRelativeProjectRoot: test.projectRoot,
						RepositoryRelativeConfigPath:  test.configPath,
					}},
				},
			}
			withConfigPullBootstrapDependencies(t, effectiveDirectory, worktreeRoot, client)

			err := runConfigPull(testConfigCommand(), nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if client.readCalls != 0 {
				t.Fatalf("read calls = %d", client.readCalls)
			}
		})
	}
}

func withConfigPullBootstrapDependencies(
	t *testing.T,
	effectiveDirectory string,
	worktreeRoot string,
	client projectConfigurationClient,
) {
	t.Helper()
	originalResolve := resolveProjectContext
	originalRoot := resolveConfigPullRoot
	originalCWD := configWorkingDirectory
	originalSlug := resolveProjectRepoSlug
	originalToken := readActiveConfigToken
	originalClient := newProjectConfigClient
	originalProjectID := configPullProjectID
	resolveProjectContext = func(string, string) (*config.ProjectContext, error) {
		return nil, &config.ConfigError{Stage: "read", Code: "config_not_found"}
	}
	resolveConfigPullRoot = func(string, string) (string, string, error) {
		return effectiveDirectory, worktreeRoot, nil
	}
	configWorkingDirectory = func() (string, error) { return effectiveDirectory, nil }
	resolveProjectRepoSlug = func(string, string) (string, string, error) {
		return "acme", "mobile", nil
	}
	readActiveConfigToken = func() (string, error) { return "token", nil }
	newProjectConfigClient = func(string, bool) projectConfigurationClient { return client }
	configPullProjectID = ""
	t.Cleanup(func() {
		resolveProjectContext = originalResolve
		resolveConfigPullRoot = originalRoot
		configWorkingDirectory = originalCWD
		resolveProjectRepoSlug = originalSlug
		readActiveConfigToken = originalToken
		newProjectConfigClient = originalClient
		configPullProjectID = originalProjectID
	})
}
