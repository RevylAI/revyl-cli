package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/gitremote"
	"github.com/revyl/cli/internal/projectpublication"
	"github.com/revyl/cli/internal/ui"
	"github.com/spf13/cobra"
)

type projectConfigurationClient interface {
	ListRepositoryProjects(context.Context, api.RepositoryProjectCatalogQuery) (*api.RepositoryProjectCatalogResponse, error)
	ReadProjectConfiguration(context.Context, string, api.ProjectConfigurationReadRequest) (*api.ProjectConfigurationReadResponse, error)
	ValidateProjectConfiguration(context.Context, string, api.ProjectConfigurationValidateRequest) (*api.ProjectConfigurationValidateResponse, error)
	ReplaceProjectConfiguration(context.Context, string, api.ProjectConfigurationReplaceRequest) (*api.ProjectConfigurationReplaceResponse, error)
	GetProjectCursorProofAuthorization(context.Context, string) (*api.ProjectCursorProofAuthorizationResponse, error)
	AuthorizeProjectCursorProof(context.Context, string) (*api.ProjectCursorProofAuthorizationResponse, error)
}

type githubRepositoryStatusReader interface {
	GetGithubRepositories(context.Context) (*api.GithubRepositoriesResponse, error)
}

var (
	resolveProjectContext  = config.ResolveProjectContext
	resolveProjectRepoSlug = gitremote.ResolveSlug
	resolveConfigPullRoot  = config.ResolveGitWorktreeRoot
	createPulledConfig     = config.CreateConfigIfAbsent
	configWorkingDirectory = os.Getwd
	readActiveConfigToken  = func() (string, error) { return auth.NewManager().GetActiveToken() }
	newProjectConfigClient = func(token string, devMode bool) projectConfigurationClient {
		return api.NewClientWithDevMode(token, devMode)
	}
)

var configValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate the complete project configuration",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return analytics.WithSafeDiagnostic(
			runConfigValidate(cmd, args),
			"project configuration validation failed",
		)
	},
}

var configPushCmd = &cobra.Command{
	Use:   "push",
	Short: "Publish the complete project configuration",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return analytics.WithSafeDiagnostic(
			runConfigPush(cmd, args),
			"project configuration publication failed",
		)
	},
}

var configAuthorizeCursorProofCmd = &cobra.Command{
	Use:   "authorize-cursor-proof",
	Short: "Authorize Cursor proof runs for the current project",
	Long: `Authorize unattended Cursor proof runs for the current project.

This records server-owned launch authority only. It does not edit or publish
.revyl/config.yaml, and succeeds only when the current project policy selects
enabled Cursor proof and the caller's live Cursor catalog includes the repository.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return analytics.WithSafeDiagnostic(
			runConfigAuthorizeCursorProof(cmd, args),
			"project Cursor proof authorization failed",
		)
	},
}

var (
	configPullProjectID string
	configPushForce     bool
)

var configPullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Pull the project configuration",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return analytics.WithSafeDiagnostic(
			runConfigPull(cmd, args),
			"project configuration pull failed",
		)
	},
}

func init() {
	configPushCmd.Flags().BoolVar(
		&configPushForce,
		"force",
		false,
		"Publish once even when the default branch manages configuration",
	)
	configPullCmd.Flags().StringVar(
		&configPullProjectID,
		"project",
		"",
		"Assert the expected project ID",
	)
	configCmd.AddCommand(
		configValidateCmd,
		configPushCmd,
		configPullCmd,
		configAuthorizeCursorProofCmd,
	)
}

type resolvedProjectConfiguration struct {
	local    *config.ProjectContext
	locator  api.ProjectConfigurationRepositoryLocator
	authored api.AuthoredRevylConfig
}

type projectConfigurationValidationOutput struct {
	Status                            string                                 `json:"status"`
	Scope                             string                                 `json:"scope"`
	ProjectID                         string                                 `json:"project_id"`
	CandidateProjectConfigurationHash string                                 `json:"candidate_project_configuration_hash"`
	CurrentState                      *string                                `json:"current_state"`
	Authority                         *api.ConfigurationAuthority            `json:"authority"`
	Connected                         connectedConfigurationValidationOutput `json:"connected"`
}

type connectedConfigurationValidationOutput struct {
	Status       string                      `json:"status"`
	CurrentState *string                     `json:"current_state,omitempty"`
	Authority    *api.ConfigurationAuthority `json:"authority,omitempty"`
	NextAction   string                      `json:"next_action"`
	Explanation  string                      `json:"explanation"`
}

func resolveProjectConfiguration() (*resolvedProjectConfiguration, error) {
	local, err := resolveLocalProjectConfiguration()
	if err != nil {
		return nil, err
	}
	return resolveConnectedProjectConfiguration(local)
}

func resolveLocalProjectConfiguration() (*config.ProjectContext, error) {
	cwd, err := configWorkingDirectory()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}
	return resolveProjectContext(cwd, "")
}

func resolveConnectedProjectConfiguration(
	local *config.ProjectContext,
) (*resolvedProjectConfiguration, error) {
	namespace, repositoryName, err := resolveProjectRepoSlug(local.WorktreeRoot, "")
	if err != nil {
		return nil, actionableGithubOriginError(local.WorktreeRoot)
	}
	authored, err := authoredConfigForAPI(*local.Authored)
	if err != nil {
		return nil, err
	}
	return &resolvedProjectConfiguration{
		local: local,
		locator: api.ProjectConfigurationRepositoryLocator{
			Provider:                      "github",
			Namespace:                     namespace,
			RepositoryName:                repositoryName,
			RepositoryRelativeProjectRoot: local.RepositoryRelativeProjectRoot,
		},
		authored: authored,
	}, nil
}

func actionableGithubOriginError(worktreeRoot string) error {
	return fmt.Errorf("the Git worktree has no supported GitHub origin; run %q or %q, then retry the Revyl command", gitRecoveryCommand(worktreeRoot, "remote", "add", "origin", "https://github.com/<owner>/<repository>.git"), gitRecoveryCommand(worktreeRoot, "remote", "set-url", "origin", "https://github.com/<owner>/<repository>.git"))
}

func authoredConfigForAPI(authored config.AuthoredConfig) (api.AuthoredRevylConfig, error) {
	payload, err := json.Marshal(authored)
	if err != nil {
		return api.AuthoredRevylConfig{}, fmt.Errorf("encode project configuration: %w", err)
	}
	var converted api.AuthoredRevylConfig
	if err := json.Unmarshal(payload, &converted); err != nil {
		return api.AuthoredRevylConfig{}, fmt.Errorf("encode project configuration: %w", err)
	}
	return converted, nil
}

func authoredConfigFromAPI(authored api.AuthoredRevylConfig) (config.AuthoredConfig, error) {
	payload, err := json.Marshal(authored)
	if err != nil {
		return config.AuthoredConfig{}, fmt.Errorf("decode project configuration: %w", err)
	}
	var converted config.AuthoredConfig
	if err := json.Unmarshal(payload, &converted); err != nil {
		return config.AuthoredConfig{}, fmt.Errorf("decode project configuration: %w", err)
	}
	if err := converted.ValidateContract(); err != nil {
		return config.AuthoredConfig{}, err
	}
	return converted, nil
}

func publicationCompilationContext(local *config.ProjectContext) config.CompilationContext {
	return config.CompilationContext{
		RepositoryRelativeProjectRoot: local.RepositoryRelativeProjectRoot,
		ExecutionDirectory:            local.RepositoryRelativeProjectRoot,
	}
}

func projectConfigurationJSON(cmd *cobra.Command) bool {
	value, _ := cmd.Flags().GetBool("json")
	return value
}

func projectConfigurationClientForCommand(cmd *cobra.Command, token string) projectConfigurationClient {
	devMode, _ := cmd.Flags().GetBool("dev")
	return newProjectConfigClient(token, devMode)
}

func requireProjectConfigurationToken() (string, error) {
	token, err := readActiveConfigToken()
	if err != nil {
		return "", fmt.Errorf("Revyl authentication could not be read; run %q, then run %q and retry the configuration command", cliRecoveryCommand("auth", "status"), cliRecoveryCommand("auth", "login"))
	}
	if strings.TrimSpace(token) == "" {
		ui.PrintError("Not authenticated")
		ui.PrintInfo("Run %q to authenticate", cliRecoveryCommand("auth", "login"))
		return "", fmt.Errorf("not authenticated")
	}
	return token, nil
}

func printProjectConfigurationJSON(value any) error {
	return json.NewEncoder(os.Stdout).Encode(value)
}

func runConfigValidate(cmd *cobra.Command, _ []string) error {
	local, err := resolveLocalProjectConfiguration()
	if err != nil {
		return actionableLocalConfigError(err)
	}
	localAggregate, err := config.NormalizeAuthoredConfig(
		*local.Authored,
		publicationCompilationContext(local),
	)
	if err != nil {
		return actionableLocalConfigError(err)
	}
	output := projectConfigurationValidationOutput{
		Status:                            "valid",
		Scope:                             "local",
		ProjectID:                         local.Authored.Project.ID,
		CandidateProjectConfigurationHash: localAggregate.ProjectConfigurationHash,
	}
	complete := func(connected connectedConfigurationValidationOutput, connectedErr error) error {
		output.Connected = connected
		output.CurrentState = connected.CurrentState
		output.Authority = connected.Authority
		if projectConfigurationJSON(cmd) {
			if err := printProjectConfigurationJSON(output); err != nil {
				return err
			}
		} else {
			ui.PrintSuccess("Configuration is valid locally")
			switch connected.Status {
			case "succeeded":
				ui.PrintSuccess("Connected validation succeeded")
			case "skipped":
				ui.PrintDim("Connected validation skipped: %s", connected.Explanation)
			case "failed":
				ui.PrintWarning("Connected validation failed: %s", connected.Explanation)
			}
			if connected.Authority != nil {
				ui.PrintKeyValue("Authority:", string(*connected.Authority))
			}
			if connected.NextAction != "none" && connectedErr == nil {
				ui.PrintDim("Next: %s", connected.NextAction)
			}
		}
		return connectedErr
	}
	token, err := readActiveConfigToken()
	if err != nil {
		authStatus := cliRecoveryCommand("auth", "status")
		authLogin := cliRecoveryCommand("auth", "login")
		return complete(connectedConfigurationValidationOutput{
			Status: "failed", NextAction: fmt.Sprintf("run %q, then %q and retry", authStatus, authLogin),
			Explanation: "authentication could not be read",
		}, fmt.Errorf("Revyl authentication could not be read; run %q, then %q and retry %q: %w", authStatus, authLogin, cliRecoveryCommand("config", "validate"), err))
	}
	if strings.TrimSpace(token) == "" {
		return complete(connectedConfigurationValidationOutput{
			Status: "skipped", NextAction: fmt.Sprintf("run %q to enable connected validation", cliRecoveryCommand("auth", "login")),
			Explanation: "Revyl is not authenticated",
		}, nil)
	}
	namespace, repositoryName, err := resolveProjectRepoSlug(local.WorktreeRoot, "")
	if err != nil {
		addOrigin := gitRecoveryCommand(local.WorktreeRoot, "remote", "add", "origin", "https://github.com/<owner>/<repository>.git")
		setOrigin := gitRecoveryCommand(local.WorktreeRoot, "remote", "set-url", "origin", "https://github.com/<owner>/<repository>.git")
		return complete(connectedConfigurationValidationOutput{
			Status: "skipped", NextAction: fmt.Sprintf("run %q if origin is missing or %q to update the existing origin, then retry", addOrigin, setOrigin),
			Explanation: "the worktree has no supported GitHub repository remote",
		}, nil)
	}
	authored, err := authoredConfigForAPI(*local.Authored)
	if err != nil {
		return complete(connectedConfigurationValidationOutput{
			Status: "failed", NextAction: "review the local configuration and retry",
			Explanation: "the local configuration could not be prepared for connected validation",
		}, err)
	}
	locator := api.ProjectConfigurationRepositoryLocator{
		Provider:                      "github",
		Namespace:                     namespace,
		RepositoryName:                repositoryName,
		RepositoryRelativeProjectRoot: local.RepositoryRelativeProjectRoot,
	}
	client := projectConfigurationClientForCommand(cmd, token)
	result, err := client.ValidateProjectConfiguration(
		cmd.Context(),
		local.Authored.Project.ID,
		api.ProjectConfigurationValidateRequest{
			Locator: locator, Configuration: authored,
		},
	)
	if err != nil {
		connectedExplanation := "Revyl could not validate the repository-bound configuration"
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && strings.TrimSpace(apiErr.Detail) != "" {
			connectedExplanation = projectConfigurationValidationDetail(apiErr.Detail)
		}
		err = actionableProjectConfigurationAPIError(
			cmd.Context(),
			client,
			locator,
			local.Authored.Project.ID,
			err,
			cliRecoveryCommand("config", "validate"),
			local,
		)
		return complete(connectedConfigurationValidationOutput{
			Status: "failed", NextAction: err.Error(),
			Explanation: connectedExplanation,
		}, err)
	}
	if result == nil || result.CandidateProjectConfigurationHash != localAggregate.ProjectConfigurationHash {
		err := fmt.Errorf("server returned an invalid project configuration validation result")
		return complete(connectedConfigurationValidationOutput{
			Status: "failed", NextAction: "retry connected validation",
			Explanation: "Revyl returned an inconsistent candidate hash",
		}, err)
	}
	if (result.Current.State == api.ProjectConfigurationReadResponseStatePresent && result.Current.Resource == nil) ||
		(result.Current.State == api.ProjectConfigurationReadResponseStateAbsent && result.Current.Resource != nil) ||
		(result.Current.State != api.ProjectConfigurationReadResponseStatePresent && result.Current.State != api.ProjectConfigurationReadResponseStateAbsent) {
		err := fmt.Errorf("server returned an invalid project configuration state")
		return complete(connectedConfigurationValidationOutput{
			Status: "failed", NextAction: "retry connected validation",
			Explanation: "Revyl returned an invalid project configuration state",
		}, err)
	}
	state := string(result.Current.State)
	connected := connectedConfigurationValidationOutput{
		Status: "succeeded", CurrentState: &state, NextAction: "none",
		Explanation: "Revyl validated repository identity, authority, and referenced resources",
	}
	if result.Current.Resource != nil {
		connected.Authority = &result.Current.Resource.Authority
	}
	return complete(connected, nil)
}

func readRemoteProjectConfiguration(
	cmd *cobra.Command,
	client projectConfigurationClient,
	resolved *resolvedProjectConfiguration,
) (*api.ProjectConfigurationReadResponse, error) {
	return client.ReadProjectConfiguration(
		cmd.Context(),
		resolved.local.Authored.Project.ID,
		api.ProjectConfigurationReadRequest{Locator: resolved.locator},
	)
}

func runConfigPush(cmd *cobra.Command, _ []string) error {
	resolved, err := resolveProjectConfiguration()
	if err != nil {
		return actionableLocalConfigError(err)
	}
	token, err := requireProjectConfigurationToken()
	if err != nil {
		return err
	}
	client := projectConfigurationClientForCommand(cmd, token)
	return publishResolvedProjectConfiguration(cmd, client, resolved)
}

func runConfigAuthorizeCursorProof(cmd *cobra.Command, _ []string) error {
	local, err := resolveLocalProjectConfiguration()
	if err != nil {
		return actionableLocalConfigError(err)
	}
	token, err := requireProjectConfigurationToken()
	if err != nil {
		return err
	}
	result, err := projectConfigurationClientForCommand(
		cmd,
		token,
	).AuthorizeProjectCursorProof(
		cmd.Context(),
		local.Authored.Project.ID,
	)
	if err != nil {
		return actionableCursorProofAuthorizationError(err, local)
	}
	if !result.Required || !result.Authorized || result.AuthorizedAt == nil {
		return fmt.Errorf("Revyl returned an invalid Cursor proof authorization state; retry %q, then run %q if it still fails", cliRecoveryCommand("config", "authorize-cursor-proof"), cliRecoveryCommand("doctor"))
	}
	if projectConfigurationJSON(cmd) {
		return printProjectConfigurationJSON(result)
	}
	ui.PrintSuccess("Authorized Cursor proof runs for this project")
	ui.PrintKeyValue(
		"Repository:",
		result.Repository.Namespace+"/"+result.Repository.RepositoryName,
	)
	ui.PrintKeyValue(
		"Project root:",
		result.Repository.RepositoryRelativeProjectRoot,
	)
	return nil
}

func actionableCursorProofAuthorizationError(err error, local *config.ProjectContext) error {
	authorize := cliRecoveryCommand("config", "authorize-cursor-proof")
	validate := cliRecoveryCommand("config", "validate")
	push := cliRecoveryCommand("config", "push")
	pull := cliRecoveryCommand("config", "pull")
	githubStatus := cliRecoveryCommand("github", "status")
	authLogin := cliRecoveryCommand("auth", "login")
	authStatus := cliRecoveryCommand("auth", "status")
	doctor := cliRecoveryCommand("doctor")
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch apiErr.Code {
	case "project_removed":
		return removedProjectConfigurationError(local)
	case "cursor_proof_not_configured", "cursor_proof_policy_changed":
		return fmt.Errorf("project does not currently select enabled Cursor proof; update .revyl/config.yaml, run %q, then run %q or commit the Git-managed file before retrying %q", validate, push, authorize)
	case "cursor_connection_unavailable":
		return fmt.Errorf("connect a usable Cursor API key, then retry %q", authorize)
	case "cursor_repository_not_accessible":
		return fmt.Errorf("the active Cursor connection cannot access this repository; update Cursor repository access, then retry %q", authorize)
	case "cursor_repository_catalog_unavailable":
		return fmt.Errorf("Cursor repository access could not be verified; retry %q", authorize)
	case "project_not_found":
		return fmt.Errorf("Revyl cannot find the configured project; run %q, then run %q to restore its project identity before retrying %q", githubStatus, pull, authorize)
	default:
		switch apiErr.StatusCode {
		case 401:
			return fmt.Errorf("Revyl authentication is no longer valid; run %q, then retry %q", authLogin, authorize)
		case 403:
			return fmt.Errorf("the active Revyl account cannot authorize this project; run %q, ask an organization admin for member access if needed, then retry %q", authStatus, authorize)
		default:
			return fmt.Errorf("Revyl could not authorize Cursor proof; retry %q, then run %q if it still fails", authorize, doctor)
		}
	}
}

func actionableProjectConfigurationAPIError(
	ctx context.Context,
	client projectConfigurationClient,
	locator api.ProjectConfigurationRepositoryLocator,
	projectID string,
	err error,
	retryCommand string,
	localContexts ...*config.ProjectContext,
) error {
	authLogin := cliRecoveryCommand("auth", "login")
	authStatus := cliRecoveryCommand("auth", "status")
	githubStatus := cliRecoveryCommand("github", "status")
	githubConnect := cliRecoveryCommand("github", "connect")
	configPull := cliRecoveryCommand("config", "pull")
	configValidate := cliRecoveryCommand("config", "validate")
	forcedConfigPush := cliRecoveryCommand("config", "push", "--force")
	doctor := cliRecoveryCommand("doctor")
	var publicationErr *projectpublication.Error
	if errors.As(err, &publicationErr) {
		switch publicationErr.Code {
		case "project_removed":
			return removedProjectConfigurationError(firstProjectContext(localContexts))
		case "git_authority_rejects_manual_write":
			return fmt.Errorf("configuration is managed from the default branch; run %q, then commit the designated .revyl/config.yaml; to publish once without changing that management mode, run %q", configValidate, forcedConfigPush)
		case "observed_configuration_changed":
			return fmt.Errorf("project configuration changed on Revyl; run %q, review the result, and retry %q", configPull, retryCommand)
		}
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		if strings.Contains(err.Error(), "revyl ") {
			return err
		}
		return fmt.Errorf("Revyl could not complete the repository-bound configuration request: %v; retry %q, then run %q if it still fails", err, retryCommand, doctor)
	}
	switch apiErr.Code {
	case "project_removed":
		return removedProjectConfigurationError(firstProjectContext(localContexts))
	case "repository_provider_unavailable", "repository_projects_provider_unavailable":
		return fmt.Errorf("GitHub repository verification is temporarily unavailable; retry '%s'", retryCommand)
	case "repository_projects_limit_exceeded":
		return fmt.Errorf("this repository has more Revyl projects than the bounded project catalog can return safely; contact Revyl support to repair or raise the repository project limit before retrying '%s'", retryCommand)
	case "project_configuration_payload_too_large":
		return fmt.Errorf("the project configuration exceeds Revyl's request-size limit; run %q to locate it, reduce its size, then %s", cliRecoveryCommand("config", "path"), projectConfigurationValidateAndRetry(configValidate, retryCommand))
	case "referenced_workflow_not_available":
		return unavailableProjectConfigurationReferenceError(
			"workflow_id",
			cliRecoveryCommand("workflow", "list"),
			configValidate,
			retryCommand,
		)
	case "referenced_secret_not_available":
		return unavailableProjectConfigurationReferenceError(
			"build secret",
			cliRecoveryCommand("build", "secret", "list"),
			configValidate,
			retryCommand,
		)
	case "referenced_launch_variable_not_available":
		return unavailableProjectConfigurationReferenceError(
			"launch variable",
			cliRecoveryCommand("global", "launch-var", "list"),
			configValidate,
			retryCommand,
		)
	}
	if apiErr.StatusCode != 404 && apiErr.Code != "project_configuration_inaccessible" && apiErr.Code != "repository_projects_inaccessible" {
		switch apiErr.StatusCode {
		case 401:
			return fmt.Errorf("Revyl authentication is no longer valid; run %q, then retry %q", authLogin, retryCommand)
		case 402, 426:
			return err
		case 403:
			return fmt.Errorf("the active Revyl account cannot access this project; run %q to verify the account and organization, ask an organization admin for member access if needed, then retry %q", authStatus, retryCommand)
		case 409:
			return fmt.Errorf("the project configuration changed; run %q, review the result, then retry %q", configPull, retryCommand)
		case 413:
			return fmt.Errorf("the project configuration exceeds Revyl's request-size limit; run %q to locate it, reduce its size, then %s", cliRecoveryCommand("config", "path"), projectConfigurationValidateAndRetry(configValidate, retryCommand))
		case 422:
			if paths, platform := missingManagedReviewAppIDPaths(localContexts); len(paths) > 0 {
				listCommand := cliRecoveryCommand("app", "list")
				if platform != "" {
					listCommand = cliRecoveryCommand("app", "list", "--platform", platform)
				}
				return fmt.Errorf(
					"the managed PR-review profile is missing a required app_id at %s; run %q, set each reported app_id to an App ID from that list (or follow its create-app instruction if empty), then %s",
					strings.Join(paths, ", "),
					listCommand,
					projectConfigurationValidateAndRetry(configValidate, retryCommand),
				)
			}
			if hasProjectConfigurationValidationIssue(apiErr, "referenced_app_not_available") {
				platform := projectConfigurationAppIssuePlatform(apiErr)
				detail := projectConfigurationValidationDetail(apiErr.Detail)
				if detail == "" {
					detail = "one or more reported app_id fields do not resolve to an active app accessible to this organization"
				}
				listCommand := cliRecoveryCommand("app", "list")
				if platform != "" {
					listCommand = cliRecoveryCommand("app", "list", "--platform", platform)
				}
				return fmt.Errorf(
					"Revyl rejected the %sapp reference: %s; run %q, replace each reported app_id field in .revyl/config.yaml with an App ID from that list (or follow its create-app instruction if empty), then %s",
					projectConfigurationPlatformLabel(platform), detail,
					listCommand,
					projectConfigurationValidateAndRetry(configValidate, retryCommand),
				)
			}
			if detail := projectConfigurationValidationDetail(apiErr.Detail); detail != "" {
				return fmt.Errorf("Revyl rejected the repository-bound configuration: %s; then %s", detail, projectConfigurationValidateAndRetry(configValidate, retryCommand))
			}
			return fmt.Errorf("Revyl rejected a repository-bound configuration reference; repair the referenced organization resource, then %s", projectConfigurationValidateAndRetry(configValidate, retryCommand))
		default:
			if apiErr.StatusCode >= 500 {
				return fmt.Errorf("Revyl or GitHub could not complete repository verification; retry %q, then run %q if it still fails", retryCommand, doctor)
			}
			return fmt.Errorf("Revyl could not complete the repository-bound configuration request; run %q, then retry %q", configValidate, retryCommand)
		}
	}

	fullName := strings.Trim(strings.TrimSpace(locator.Namespace)+"/"+strings.TrimSpace(locator.RepositoryName), "/")
	if fullName == "" {
		fullName = "the current repository"
	}
	if statusReader, ok := client.(githubRepositoryStatusReader); ok {
		repositories, statusErr := statusReader.GetGithubRepositories(ctx)
		if statusErr == nil {
			switch {
			case repositories == nil || !repositories.IsConnected():
				return fmt.Errorf("Revyl cannot access %s because the GitHub App is not connected for the active account and organization; run %q to verify them, run %q, then retry %q", fullName, authStatus, githubConnect, retryCommand)
			case !githubRepositoryAvailable(repositories, locator.Namespace, locator.RepositoryName):
				return fmt.Errorf("the Revyl GitHub App is connected but cannot access %s; grant that repository to the existing GitHub App, run %q to verify access, then retry %q", fullName, githubStatus, retryCommand)
			}
		}
	}

	if apiErr.Code == "project_configuration_inaccessible" && strings.TrimSpace(projectID) != "" {
		catalog, catalogErr := client.ListRepositoryProjects(
			ctx,
			api.RepositoryProjectCatalogQuery{
				Provider:       "github",
				Namespace:      locator.Namespace,
				RepositoryName: locator.RepositoryName,
			},
		)
		if catalogErr == nil && catalog != nil {
			for _, project := range catalog.Projects {
				if project.RepositoryRelativeProjectRoot == locator.RepositoryRelativeProjectRoot &&
					!strings.EqualFold(project.ProjectId.String(), projectID) {
					return fmt.Errorf("%s already has a different Revyl project at this project root; run %q, move that local file aside, then run %q", fullName, cliRecoveryCommand("config", "path"), cliRecoveryCommand("config", "pull", "--project", project.ProjectId.String()))
				}
			}
			for _, project := range catalog.Projects {
				if strings.EqualFold(project.ProjectId.String(), projectID) &&
					project.RepositoryRelativeProjectRoot != locator.RepositoryRelativeProjectRoot {
					return fmt.Errorf("Revyl project %s belongs to repository root %q, not %q; run %q from that registered project root", projectID, project.RepositoryRelativeProjectRoot, locator.RepositoryRelativeProjectRoot, registeredProjectPullRecoveryCommand(localContexts, project.RepositoryRelativeProjectRoot, projectID))
				}
			}
		}
	}

	return fmt.Errorf("Revyl could not verify %s or its project identity; run %q, then retry %q; if repository access is healthy, run %q to restore the registered project configuration", fullName, githubStatus, retryCommand, configPull)
}

func registeredProjectPullRecoveryCommand(
	localContexts []*config.ProjectContext,
	repositoryRelativeProjectRoot string,
	projectID string,
) string {
	if len(localContexts) == 0 || localContexts[0] == nil ||
		strings.TrimSpace(localContexts[0].WorktreeRoot) == "" ||
		!validRepositoryProjectRoot(repositoryRelativeProjectRoot) {
		return cliRecoveryCommand("config", "pull", "--project", projectID)
	}
	registeredProjectDirectory := localContexts[0].WorktreeRoot
	if repositoryRelativeProjectRoot != "." {
		registeredProjectDirectory = filepath.Join(
			registeredProjectDirectory,
			filepath.FromSlash(repositoryRelativeProjectRoot),
		)
	}
	return cliRecoveryCommandInDirectory(
		registeredProjectDirectory,
		"config",
		"pull",
		"--project",
		projectID,
	)
}

func firstProjectContext(contexts []*config.ProjectContext) *config.ProjectContext {
	if len(contexts) == 0 {
		return nil
	}
	return contexts[0]
}

func removedProjectConfigurationError(local *config.ProjectContext) error {
	projectRoot := "."
	if local != nil {
		if strings.TrimSpace(local.RepositoryRelativeProjectRoot) != "" {
			projectRoot = local.RepositoryRelativeProjectRoot
		}
	}
	return fmt.Errorf(
		"the Revyl project for repository root %q was deleted; this local configuration still works for local-only commands, but it cannot synchronize or restore automation under its deleted identity; create a replacement project at the intended root in GitHub settings, then run %q",
		projectRoot,
		"revyl -C <replacement-root> config pull",
	)
}

func hasProjectConfigurationValidationIssue(apiErr *api.APIError, issueType string) bool {
	for _, issue := range apiErr.ValidationIssues {
		if issue.Type == issueType {
			return true
		}
	}
	return false
}

func projectConfigurationAppIssuePlatform(apiErr *api.APIError) string {
	platform := ""
	for _, issue := range apiErr.ValidationIssues {
		if issue.Type != "referenced_app_not_available" {
			continue
		}
		current := ""
		switch {
		case strings.Contains(issue.Field, ".ios."):
			current = "ios"
		case strings.Contains(issue.Field, ".android."):
			current = "android"
		}
		if platform != "" && current != platform {
			return ""
		}
		if current != "" {
			platform = current
		}
	}
	return platform
}

func projectConfigurationPlatformLabel(platform string) string {
	switch platform {
	case "ios":
		return "iOS "
	case "android":
		return "Android "
	default:
		return ""
	}
}

func projectConfigurationValidateAndRetry(validateCommand, retryCommand string) string {
	if retryCommand == validateCommand {
		return fmt.Sprintf("retry %q", retryCommand)
	}
	return fmt.Sprintf("run %q and retry %q", validateCommand, retryCommand)
}

func unavailableProjectConfigurationReferenceError(
	referenceName string,
	listCommand string,
	validateCommand string,
	retryCommand string,
) error {
	return fmt.Errorf(
		"Revyl rejected a %s reference because it is not available to this organization; run %q, repair the reference in .revyl/config.yaml, then %s",
		referenceName,
		listCommand,
		projectConfigurationValidateAndRetry(validateCommand, retryCommand),
	)
}

func projectConfigurationValidationDetail(detail string) string {
	detail = strings.TrimSpace(detail)
	detail = strings.ReplaceAll(detail, ", then republish.", "")
	detail = strings.ReplaceAll(detail, ", then republish", "")
	return strings.TrimSuffix(strings.TrimSpace(detail), ".")
}

func missingManagedReviewAppIDPaths(localContexts []*config.ProjectContext) ([]string, string) {
	if len(localContexts) == 0 || localContexts[0] == nil || localContexts[0].Aggregate == nil {
		return nil, ""
	}
	aggregate := localContexts[0].Aggregate
	if aggregate.ReviewPolicy == nil || aggregate.ReviewPolicy.Build.Kind != "revyl" || aggregate.ReviewPolicy.Build.Profile == nil {
		return nil, ""
	}
	managedProfile := *aggregate.ReviewPolicy.Build.Profile
	paths := []string{}
	platform := ""
	for _, profile := range aggregate.Profiles {
		if profile.Name != managedProfile {
			continue
		}
		for _, configuration := range profile.Configurations {
			if configuration.AppID != nil {
				continue
			}
			paths = append(paths, strings.Join([]string{"build", "profiles", profile.Name, configuration.Platform, "app_id"}, "."))
			if platform == "" {
				platform = configuration.Platform
			} else if platform != configuration.Platform {
				platform = "multiple"
			}
		}
	}
	if platform == "multiple" {
		platform = ""
	}
	return paths, platform
}

func publishResolvedProjectConfiguration(
	cmd *cobra.Command,
	client projectConfigurationClient,
	resolved *resolvedProjectConfiguration,
) error {
	retryCommand := cliRecoveryCommand("config", "push")
	if configPushForce {
		retryCommand = cliRecoveryCommand("config", "push", "--force")
	}
	result, err := projectpublication.Publish(
		cmd.Context(),
		client,
		projectpublication.Candidate{
			ProjectID:                 resolved.local.Authored.Project.ID,
			Locator:                   resolved.locator,
			Configuration:             resolved.authored,
			AllowGitAuthorityOverride: configPushForce,
		},
	)
	if err != nil {
		return actionableProjectConfigurationAPIError(
			cmd.Context(),
			client,
			resolved.locator,
			resolved.local.Authored.Project.ID,
			err,
			retryCommand,
			resolved.local,
		)
	}
	if projectConfigurationJSON(cmd) {
		return printProjectConfigurationJSON(result)
	}
	if result.Outcome == api.ProjectConfigurationReplaceResponseOutcomeUnchanged {
		ui.PrintSuccess("Revyl already has this project configuration")
	} else {
		ui.PrintSuccess("Published the complete project configuration")
	}
	if configPushForce && result.Resource.Authority == api.ConfigurationAuthorityGitDefaultBranch {
		ui.PrintWarning("Default-branch configuration management remains enabled; its next accepted change can replace this publication")
	}
	return nil
}

func runConfigPull(cmd *cobra.Command, _ []string) error {
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{
		Domain:       "project_configuration_pull",
		DomainStatus: "failed",
	})
	resolved, err := resolveProjectConfiguration()
	if err != nil {
		var configError *config.ConfigError
		if !errors.As(err, &configError) || configError.Code != "config_not_found" {
			return actionableLocalConfigError(err)
		}
		return bootstrapConfigPull(cmd)
	}
	requestedProjectID := strings.TrimSpace(configPullProjectID)
	projectIDMatchesLocal := requestedProjectID == "" || strings.EqualFold(
		requestedProjectID,
		resolved.local.Authored.Project.ID,
	)
	if !projectIDMatchesLocal {
		if resolved.local.RepositoryRelativeExecutionDirectory !=
			resolved.local.RepositoryRelativeProjectRoot {
			bootstrapped, bootstrapErr := bootstrapCloserConfigPullProject(
				cmd,
				resolved.local.RepositoryRelativeProjectRoot,
				requestedProjectID,
			)
			if bootstrapErr != nil {
				return bootstrapErr
			}
			if bootstrapped {
				return nil
			}
		}
		return fmt.Errorf("--project does not match the local project ID; run %q to inspect the local ID, then retry %q", cliRecoveryCommand("config", "show", "--json"), cliRecoveryCommand("config", "pull", "--project", "<local-project-id>"))
	}
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{
		Properties: map[string]interface{}{
			"entity_id": resolved.local.Authored.Project.ID,
		},
	})
	if requestedProjectID == "" &&
		resolved.local.RepositoryRelativeExecutionDirectory !=
			resolved.local.RepositoryRelativeProjectRoot {
		bootstrapped, bootstrapErr := bootstrapCloserConfigPullProject(
			cmd,
			resolved.local.RepositoryRelativeProjectRoot,
			"",
		)
		if bootstrapErr != nil {
			return bootstrapErr
		}
		if bootstrapped {
			return nil
		}
	}
	token, err := requireProjectConfigurationToken()
	if err != nil {
		return err
	}
	client := projectConfigurationClientForCommand(cmd, token)
	current, err := readRemoteProjectConfiguration(
		cmd,
		client,
		resolved,
	)
	if err != nil {
		if requestedProjectID == "" && isProjectRemovedError(err) {
			return replaceRemovedLocalProjectConfiguration(cmd, client, resolved)
		}
		return actionableProjectConfigurationAPIError(
			cmd.Context(),
			client,
			resolved.locator,
			resolved.local.Authored.Project.ID,
			err,
			cliRecoveryCommand("config", "pull"),
			resolved.local,
		)
	}
	if current.State == api.ProjectConfigurationReadResponseStateAbsent {
		return fmt.Errorf("Revyl has no published configuration for this project; run %q to publish the local configuration", cliRecoveryCommand("config", "push"))
	}
	if current.State != api.ProjectConfigurationReadResponseStatePresent || current.Resource == nil {
		return fmt.Errorf("Revyl returned an invalid project configuration state; retry %q, then run %q if it still fails", cliRecoveryCommand("config", "pull"), cliRecoveryCommand("doctor"))
	}
	authored, err := authoredConfigFromAPI(current.Resource.Configuration)
	if err != nil {
		return fmt.Errorf("Revyl returned a project configuration that the CLI could not decode; retry %q, then run %q if it still fails", cliRecoveryCommand("config", "pull"), cliRecoveryCommand("doctor"))
	}
	canonical, err := config.MarshalCanonicalConfig(authored)
	if err != nil {
		return fmt.Errorf("Revyl returned a project configuration that the CLI could not render; retry %q, then run %q if it still fails", cliRecoveryCommand("config", "pull"), cliRecoveryCommand("doctor"))
	}
	comparison, err := config.CompareConfigSemantics(
		resolved.local.OriginalBytes,
		canonical,
		publicationCompilationContext(resolved.local),
	)
	if err != nil {
		return fmt.Errorf("the local and Revyl configurations could not be compared; run %q, then retry %q", cliRecoveryCommand("config", "validate"), cliRecoveryCommand("config", "pull"))
	}
	if comparison.RightHash != current.Resource.ProjectConfigurationHash {
		return fmt.Errorf("Revyl returned a project configuration with an inconsistent hash; retry %q, then run %q if it still fails", cliRecoveryCommand("config", "pull"), cliRecoveryCommand("doctor"))
	}
	if comparison.Equal {
		analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{
			DomainStatus: "unchanged",
		})
		if projectConfigurationJSON(cmd) {
			return printProjectConfigurationJSON(map[string]any{"outcome": "unchanged"})
		}
		ui.PrintSuccess("Local configuration already has the same project meaning")
		return nil
	}
	return replacePulledProjectConfiguration(cmd, resolved.local, canonical)
}

func replacePulledProjectConfiguration(
	cmd *cobra.Command,
	local *config.ProjectContext,
	canonical []byte,
) error {
	backupPath, err := config.CreateConfigBackup(local.ConfigPath)
	if err != nil {
		return actionableLocalConfigError(err)
	}
	if err := config.ReplaceConfigAtomically(
		local.ConfigPath,
		canonical,
		local.OriginalBytes,
	); err != nil {
		return actionableLocalConfigError(err)
	}
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{
		DomainStatus: "replaced",
	})
	if projectConfigurationJSON(cmd) {
		return printProjectConfigurationJSON(map[string]any{
			"outcome":     "replaced",
			"backup_path": backupPath,
		})
	}
	ui.PrintSuccess("Replaced the local project configuration")
	ui.PrintKeyValue("Backup:", backupPath)
	return nil
}

func isProjectRemovedError(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.Code == "project_removed"
}

func replaceRemovedLocalProjectConfiguration(
	cmd *cobra.Command,
	client projectConfigurationClient,
	resolved *resolvedProjectConfiguration,
) error {
	catalog, err := client.ListRepositoryProjects(
		cmd.Context(),
		api.RepositoryProjectCatalogQuery{
			Provider:       "github",
			Namespace:      resolved.locator.Namespace,
			RepositoryName: resolved.locator.RepositoryName,
		},
	)
	if err != nil {
		return actionableProjectConfigurationAPIError(
			cmd.Context(),
			client,
			resolved.locator,
			resolved.local.Authored.Project.ID,
			err,
			cliRecoveryCommand("config", "pull"),
			resolved.local,
		)
	}
	target, found, err := sameRootReplacementPullTarget(catalog, resolved)
	if err != nil {
		return err
	}
	if !found {
		return removedProjectConfigurationError(resolved.local)
	}
	canonical, err := readCanonicalConfigPullTarget(cmd, client, target, resolved.local)
	if err != nil {
		return err
	}
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{
		Properties: map[string]interface{}{
			"entity_id": target.projectID,
		},
	})
	return replacePulledProjectConfiguration(cmd, resolved.local, canonical)
}

func sameRootReplacementPullTarget(
	catalog *api.RepositoryProjectCatalogResponse,
	resolved *resolvedProjectConfiguration,
) (configPullBootstrapTarget, bool, error) {
	if catalog == nil ||
		catalog.Repository.Provider != "github" ||
		!strings.EqualFold(catalog.Repository.Namespace, resolved.locator.Namespace) ||
		!strings.EqualFold(catalog.Repository.RepositoryName, resolved.locator.RepositoryName) {
		return configPullBootstrapTarget{}, false, fmt.Errorf("Revyl returned invalid repository project identity; retry %q, then run %q if it still fails", cliRecoveryCommand("config", "pull"), cliRecoveryCommand("doctor"))
	}

	var replacement *api.RepositoryProjectCatalogItem
	for index := range catalog.Projects {
		project := &catalog.Projects[index]
		root := project.RepositoryRelativeProjectRoot
		if !validRepositoryProjectRoot(root) ||
			project.RepositoryRelativeConfigPath != path.Join(root, ".revyl/config.yaml") {
			return configPullBootstrapTarget{}, false, fmt.Errorf("Revyl returned an invalid repository project path; retry %q, then run %q if it still fails", cliRecoveryCommand("config", "pull"), cliRecoveryCommand("doctor"))
		}
		if root != resolved.locator.RepositoryRelativeProjectRoot {
			continue
		}
		if strings.EqualFold(project.ProjectId.String(), resolved.local.Authored.Project.ID) || replacement != nil {
			return configPullBootstrapTarget{}, false, fmt.Errorf("Revyl returned multiple or inconsistent active projects for repository root %q; run %q, then contact Revyl support if it remains inconsistent", root, cliRecoveryCommand("doctor"))
		}
		replacement = project
	}
	if replacement == nil {
		return configPullBootstrapTarget{}, false, nil
	}
	return configPullBootstrapTarget{
		projectID: replacement.ProjectId.String(),
		locator: api.ProjectConfigurationRepositoryLocator{
			Provider:                      "github",
			Namespace:                     catalog.Repository.Namespace,
			RepositoryName:                catalog.Repository.RepositoryName,
			RepositoryRelativeProjectRoot: replacement.RepositoryRelativeProjectRoot,
		},
		configPath: resolved.local.ConfigPath,
	}, true, nil
}

type configPullBootstrapTarget struct {
	projectID  string
	locator    api.ProjectConfigurationRepositoryLocator
	configPath string
}

var errNoConfigPullBootstrapTarget = errors.New("no configured project contains the current directory")

var errRequestedConfigPullProjectNotFound = errors.New("--project does not match a configured project containing the current directory")

func bootstrapConfigPull(cmd *cobra.Command) error {
	client, target, err := resolveConfigPullBootstrapTarget(
		cmd,
		strings.TrimSpace(configPullProjectID),
	)
	if err != nil {
		if errors.Is(err, errNoConfigPullBootstrapTarget) {
			return fmt.Errorf("Revyl has no configured project containing the current directory; create one in GitHub settings or run %q: %w", cliRecoveryCommand("init", "-y"), err)
		}
		return err
	}
	return pullConfigBootstrapTarget(cmd, client, target)
}

func bootstrapCloserConfigPullProject(
	cmd *cobra.Command,
	localProjectRoot string,
	expectedProjectID string,
) (bool, error) {
	client, target, err := resolveConfigPullBootstrapTarget(cmd, expectedProjectID)
	if errors.Is(err, errNoConfigPullBootstrapTarget) {
		return false, nil
	}
	if expectedProjectID != "" && errors.Is(err, errRequestedConfigPullProjectNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	targetRoot := target.locator.RepositoryRelativeProjectRoot
	if targetRoot == localProjectRoot ||
		!repositoryProjectRootIsNestedWithin(targetRoot, localProjectRoot) {
		return false, nil
	}
	return true, pullConfigBootstrapTarget(cmd, client, target)
}

func resolveConfigPullBootstrapTarget(
	cmd *cobra.Command,
	expectedProjectID string,
) (projectConfigurationClient, configPullBootstrapTarget, error) {
	cwd, err := configWorkingDirectory()
	if err != nil {
		return nil, configPullBootstrapTarget{}, fmt.Errorf("get working directory: %w", err)
	}
	effectiveDirectory, worktreeRoot, err := resolveConfigPullRoot(cwd, "")
	if err != nil {
		return nil, configPullBootstrapTarget{}, actionableLocalConfigError(err)
	}
	namespace, repositoryName, err := resolveProjectRepoSlug(worktreeRoot, "")
	if err != nil {
		return nil, configPullBootstrapTarget{}, actionableGithubOriginError(worktreeRoot)
	}
	token, err := requireProjectConfigurationToken()
	if err != nil {
		return nil, configPullBootstrapTarget{}, err
	}
	client := projectConfigurationClientForCommand(cmd, token)
	catalog, err := client.ListRepositoryProjects(
		cmd.Context(),
		api.RepositoryProjectCatalogQuery{
			Provider:       "github",
			Namespace:      namespace,
			RepositoryName: repositoryName,
		},
	)
	if err != nil {
		return nil, configPullBootstrapTarget{}, actionableProjectConfigurationAPIError(
			cmd.Context(),
			client,
			api.ProjectConfigurationRepositoryLocator{
				Provider:       "github",
				Namespace:      namespace,
				RepositoryName: repositoryName,
			},
			expectedProjectID,
			err,
			cliRecoveryCommand("config", "pull"),
		)
	}
	target, err := selectConfigPullBootstrapTarget(
		catalog,
		namespace,
		repositoryName,
		effectiveDirectory,
		worktreeRoot,
		expectedProjectID,
	)
	if err != nil {
		return nil, configPullBootstrapTarget{}, err
	}
	return client, target, nil
}

func pullConfigBootstrapTarget(
	cmd *cobra.Command,
	client projectConfigurationClient,
	target configPullBootstrapTarget,
) error {
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{
		Properties: map[string]interface{}{
			"entity_id": target.projectID,
		},
	})
	canonical, err := readCanonicalConfigPullTarget(cmd, client, target)
	if err != nil {
		return err
	}
	if err := createPulledConfig(target.configPath, canonical, 0o644); err != nil {
		return actionableLocalConfigError(err)
	}
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{
		DomainStatus: "bootstrapped",
	})
	if projectConfigurationJSON(cmd) {
		return printProjectConfigurationJSON(map[string]any{"outcome": "created"})
	}
	ui.PrintSuccess("Created the local project configuration from Revyl")
	return nil
}

func readCanonicalConfigPullTarget(
	cmd *cobra.Command,
	client projectConfigurationClient,
	target configPullBootstrapTarget,
	localContexts ...*config.ProjectContext,
) ([]byte, error) {
	current, err := client.ReadProjectConfiguration(
		cmd.Context(),
		target.projectID,
		api.ProjectConfigurationReadRequest{Locator: target.locator},
	)
	if err != nil {
		return nil, actionableProjectConfigurationAPIError(
			cmd.Context(),
			client,
			target.locator,
			target.projectID,
			err,
			cliRecoveryCommand("config", "pull"),
			localContexts...,
		)
	}
	if current == nil || current.State != api.ProjectConfigurationReadResponseStatePresent || current.Resource == nil {
		return nil, fmt.Errorf("Revyl returned an invalid configured project for the current directory; retry %q, then run %q if it still fails", cliRecoveryCommand("config", "pull"), cliRecoveryCommand("doctor"))
	}
	if current.Resource.Provider != "github" ||
		!strings.EqualFold(current.Resource.Namespace, target.locator.Namespace) ||
		!strings.EqualFold(
			current.Resource.RepositoryName,
			target.locator.RepositoryName,
		) ||
		current.Resource.RepositoryRelativeProjectRoot != target.locator.RepositoryRelativeProjectRoot ||
		current.Resource.RepositoryRelativeConfigPath != path.Join(
			target.locator.RepositoryRelativeProjectRoot,
			".revyl/config.yaml",
		) {
		return nil, fmt.Errorf("Revyl returned an invalid project locator; retry %q, then run %q if it still fails", cliRecoveryCommand("config", "pull"), cliRecoveryCommand("doctor"))
	}
	authored, err := authoredConfigFromAPI(current.Resource.Configuration)
	if err != nil {
		return nil, fmt.Errorf("Revyl returned a project configuration that the CLI could not decode; retry %q, then run %q if it still fails", cliRecoveryCommand("config", "pull"), cliRecoveryCommand("doctor"))
	}
	if !strings.EqualFold(authored.Project.ID, target.projectID) {
		return nil, fmt.Errorf("Revyl returned an invalid project identity; retry %q, then run %q if it still fails", cliRecoveryCommand("config", "pull"), cliRecoveryCommand("doctor"))
	}
	canonical, err := config.MarshalCanonicalConfig(authored)
	if err != nil {
		return nil, fmt.Errorf("Revyl returned a project configuration that the CLI could not render; retry %q, then run %q if it still fails", cliRecoveryCommand("config", "pull"), cliRecoveryCommand("doctor"))
	}
	aggregate, err := config.NormalizeAuthoredConfig(
		authored,
		config.CompilationContext{
			RepositoryRelativeProjectRoot: target.locator.RepositoryRelativeProjectRoot,
			ExecutionDirectory:            target.locator.RepositoryRelativeProjectRoot,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("Revyl returned a project configuration that the CLI could not normalize; retry %q, then run %q if it still fails", cliRecoveryCommand("config", "pull"), cliRecoveryCommand("doctor"))
	}
	if aggregate.ProjectConfigurationHash != current.Resource.ProjectConfigurationHash {
		return nil, fmt.Errorf("Revyl returned a project configuration with an inconsistent hash; retry %q, then run %q if it still fails", cliRecoveryCommand("config", "pull"), cliRecoveryCommand("doctor"))
	}
	return canonical, nil
}

func selectConfigPullBootstrapTarget(
	catalog *api.RepositoryProjectCatalogResponse,
	namespace string,
	repositoryName string,
	effectiveDirectory string,
	worktreeRoot string,
	expectedProjectID string,
) (configPullBootstrapTarget, error) {
	if catalog == nil ||
		catalog.Repository.Provider != "github" ||
		!strings.EqualFold(catalog.Repository.Namespace, namespace) ||
		!strings.EqualFold(catalog.Repository.RepositoryName, repositoryName) {
		return configPullBootstrapTarget{}, fmt.Errorf("Revyl returned invalid repository project identity")
	}
	resolvedWorktreeRoot, err := filepath.EvalSymlinks(worktreeRoot)
	if err != nil {
		return configPullBootstrapTarget{}, fmt.Errorf("active Git worktree is unavailable")
	}
	resolvedEffectiveDirectory, err := filepath.EvalSymlinks(effectiveDirectory)
	if err != nil {
		return configPullBootstrapTarget{}, fmt.Errorf("current directory is unavailable")
	}
	repositoryRelativeExecutionDirectory, err := filepath.Rel(
		resolvedWorktreeRoot,
		resolvedEffectiveDirectory,
	)
	if err != nil || repositoryRelativeExecutionDirectory == ".." ||
		strings.HasPrefix(repositoryRelativeExecutionDirectory, ".."+string(filepath.Separator)) {
		return configPullBootstrapTarget{}, fmt.Errorf("current directory is outside the active Git worktree")
	}
	repositoryRelativeExecutionDirectory = filepath.ToSlash(repositoryRelativeExecutionDirectory)
	if repositoryRelativeExecutionDirectory == "" {
		repositoryRelativeExecutionDirectory = "."
	}

	var selected *api.RepositoryProjectCatalogItem
	for index := range catalog.Projects {
		project := &catalog.Projects[index]
		root := project.RepositoryRelativeProjectRoot
		if !validRepositoryProjectRoot(root) ||
			project.RepositoryRelativeConfigPath != path.Join(root, ".revyl/config.yaml") {
			return configPullBootstrapTarget{}, fmt.Errorf("Revyl returned an invalid repository project path")
		}
		if expectedProjectID != "" && !strings.EqualFold(project.ProjectId.String(), expectedProjectID) {
			continue
		}
		if !repositoryProjectContainsDirectory(root, repositoryRelativeExecutionDirectory) {
			continue
		}
		if selected == nil || len(root) > len(selected.RepositoryRelativeProjectRoot) {
			selected = project
			continue
		}
		if len(root) == len(selected.RepositoryRelativeProjectRoot) {
			return configPullBootstrapTarget{}, fmt.Errorf("Revyl returned multiple configured projects for the current directory")
		}
	}
	if selected == nil {
		if expectedProjectID != "" {
			return configPullBootstrapTarget{}, errRequestedConfigPullProjectNotFound
		}
		return configPullBootstrapTarget{}, errNoConfigPullBootstrapTarget
	}

	projectRoot := resolvedWorktreeRoot
	if selected.RepositoryRelativeProjectRoot != "." {
		projectRoot = filepath.Join(
			resolvedWorktreeRoot,
			filepath.FromSlash(selected.RepositoryRelativeProjectRoot),
		)
	}
	resolvedProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return configPullBootstrapTarget{}, fmt.Errorf("configured project root is unavailable")
	}
	resolvedRelativeRoot, err := filepath.Rel(
		resolvedWorktreeRoot,
		resolvedProjectRoot,
	)
	if err != nil || filepath.ToSlash(resolvedRelativeRoot) != selected.RepositoryRelativeProjectRoot {
		return configPullBootstrapTarget{}, fmt.Errorf("configured project root does not resolve inside the active Git worktree")
	}

	return configPullBootstrapTarget{
		projectID: selected.ProjectId.String(),
		locator: api.ProjectConfigurationRepositoryLocator{
			Provider:                      "github",
			Namespace:                     namespace,
			RepositoryName:                repositoryName,
			RepositoryRelativeProjectRoot: selected.RepositoryRelativeProjectRoot,
		},
		configPath: filepath.Join(resolvedProjectRoot, ".revyl", "config.yaml"),
	}, nil
}

func validRepositoryProjectRoot(root string) bool {
	if root == "." {
		return true
	}
	return root != "" &&
		len(root) <= 1024 &&
		!strings.HasPrefix(root, "/") &&
		!strings.Contains(root, "\\") &&
		!strings.ContainsRune(root, '\x00') &&
		!hasInvalidRepositoryProjectRootSegment(root) &&
		len(path.Join(root, ".revyl/config.yaml")) <= 1200 &&
		path.Clean(root) == root
}

func hasInvalidRepositoryProjectRootSegment(root string) bool {
	for _, segment := range strings.Split(root, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

func repositoryProjectContainsDirectory(projectRoot string, directory string) bool {
	return projectRoot == "." ||
		directory == projectRoot ||
		strings.HasPrefix(directory, projectRoot+"/")
}

func repositoryProjectRootIsNestedWithin(candidate string, ancestor string) bool {
	return ancestor == "." || strings.HasPrefix(candidate, ancestor+"/")
}
