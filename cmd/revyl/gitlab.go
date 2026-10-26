package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/gitremote"
	"github.com/revyl/cli/internal/ui"
	"github.com/spf13/cobra"
)

var promptGitlabToken = ui.PromptSecret
var confirmGitlabWebhook = ui.PromptConfirm

var gitlabCmd = &cobra.Command{
	Use: "gitlab", Short: "Connect GitLab and configure merge request automation",
	Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
}

var gitlabConnectCmd = &cobra.Command{
	Use: "connect", Short: "Connect a GitLab automation token from your terminal",
	Long: `Connect GitLab using a service-account personal access token, or a group or
project access token, with the api scope and Developer access or higher.
Token input is hidden. Use --token-stdin for non-interactive input. Tokens are
validated and encrypted by Revyl, never saved in the local CLI credential store.
The instance defaults to the Git origin, the existing connection, or gitlab.com.
Connecting API access does not install or verify repository webhooks.`,
	Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return analytics.WithSafeDiagnostic(runGitlabConnect(cmd), "GitLab connection failed")
	},
}

var gitlabStatusCmd = &cobra.Command{
	Use: "status", Short: "Show GitLab API, repository, project, and webhook status",
	Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return analytics.WithSafeDiagnostic(runGitlabStatus(cmd), "GitLab status check failed")
	},
}

var gitlabSetupCmd = &cobra.Command{
	Use: "setup", Short: "Configure this project's merge request automation",
	Long: `Complete the nearest .revyl/config.yaml project's build and proof policy.
Connect an automation token if needed, then validate and publish the project.
For Git-managed configuration, prepare the designated file for commit instead.
A GitLab Maintainer or Owner installs the webhook using Revyl's repository
settings. Setup checks actual authenticated delivery, independently of API access.
Non-interactive callers use gitlab connect --token-stdin and config validate/push.`,
	Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return analytics.WithSafeDiagnostic(runGitlabSetup(cmd), "GitLab project setup failed")
	},
}

func init() {
	gitlabConnectCmd.Flags().String("instance", "", "Public HTTPS GitLab instance origin")
	gitlabConnectCmd.Flags().Bool("token-stdin", false, "Read the automation token from stdin without echoing it")
	gitlabCmd.AddCommand(gitlabConnectCmd, gitlabStatusCmd, gitlabSetupCmd)
}

func recordGitlabOutcome(cmd *cobra.Command, operation, status string) {
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{
		Domain: "gitlab_" + operation, DomainStatus: status,
	})
}

func runGitlabConnect(cmd *cobra.Command) error {
	recordGitlabOutcome(cmd, "connect", "failed")
	client, err := newSCMAPIClient(cmd)
	if err != nil {
		return err
	}
	connection, err := client.GetGitlabConnection(cmd.Context())
	if err != nil {
		return gitlabRequestError(err, "revyl gitlab connect")
	}
	instance, _ := cmd.Flags().GetString("instance")
	if instance == "" {
		instance = gitlabInstanceForCheckout(connection)
	}
	instance, err = normalizeGitlabInstance(instance)
	if err != nil {
		return err
	}
	fromStdin, _ := cmd.Flags().GetBool("token-stdin")
	token, err := readGitlabToken(cmd.InOrStdin(), fromStdin)
	if err != nil {
		return err
	}
	connection, err = client.ConnectGitlab(cmd.Context(), api.GitlabConnectRequest{
		AccessToken: &token, InstanceUrl: &instance,
	})
	if err != nil {
		return gitlabRequestError(err, "revyl gitlab connect")
	}
	if !gitlabConnected(connection) || connection.InstanceUrl == nil || *connection.InstanceUrl != instance {
		return fmt.Errorf("Revyl returned an invalid GitLab connection; run revyl gitlab status before retrying")
	}
	recordGitlabOutcome(cmd, "connect", "connected")
	if projectConfigurationJSON(cmd) {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(connection)
	}
	ui.PrintSuccess("Connected to GitLab at %s", instance)
	ui.PrintDim("Run revyl gitlab setup in your project; webhook delivery is verified separately.")
	return nil
}

func readGitlabToken(reader io.Reader, fromStdin bool) (string, error) {
	const maxTokenBytes = 8192
	var token string
	if fromStdin {
		data, err := io.ReadAll(io.LimitReader(reader, maxTokenBytes+3))
		if err != nil {
			return "", fmt.Errorf("could not read GitLab token from stdin")
		}
		token = strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	} else {
		value, err := promptGitlabToken("GitLab automation token (hidden):")
		if err != nil {
			return "", fmt.Errorf("GitLab token entry requires a terminal; use --token-stdin for non-interactive input")
		}
		token = value
	}
	if token == "" || len(token) > maxTokenBytes || strings.IndexFunc(token, func(character rune) bool { return unicode.IsSpace(character) || unicode.IsControl(character) }) >= 0 {
		return "", fmt.Errorf("GitLab token must be nonempty, contain no whitespace, and be at most 8192 bytes")
	}
	return token, nil
}

func normalizeGitlabInstance(instance string) (string, error) {
	parsed, err := url.Parse(instance)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("use a public HTTPS GitLab instance origin without credentials or a path")
	}
	repository, err := gitremote.ParseRepositoryRemote(strings.TrimRight(instance, "/") + "/group/project")
	if err != nil || repository.Provider != "gitlab" || repository.InstanceURL == nil {
		return "", fmt.Errorf("use a valid HTTPS GitLab instance origin")
	}
	return *repository.InstanceURL, nil
}

func gitlabInstanceForCheckout(connection *api.GitlabConnectionSummary) string {
	cwd, err := configWorkingDirectory()
	if err == nil {
		_, root, err := resolveConfigPullRoot(cwd, "")
		if err == nil {
			repository, err := resolveProjectRepository(root)
			if err == nil && repository.Provider == "gitlab" && repository.InstanceURL != nil {
				return *repository.InstanceURL
			}
		}
	}
	if connection.InstanceUrl != nil {
		return *connection.InstanceUrl
	}
	return "https://gitlab.com"
}

func gitlabConnected(connection *api.GitlabConnectionSummary) bool {
	return connection != nil && connection.Connected &&
		(connection.NeedsReconnect == nil || !*connection.NeedsReconnect)
}

func gitlabRequestError(err error, command string) error {
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("Revyl could not complete the GitLab request; retry %s", command)
	}
	code := apiErr.Code
	if code == "" {
		code = apiErr.DetailString("code")
	}
	switch code {
	case "gitlab_token_invalid":
		return fmt.Errorf("GitLab token expired or was revoked; replace it with revyl gitlab connect")
	case "gitlab_automation_account_required":
		return fmt.Errorf("use a service-account, group, or project access token; human-account tokens are not supported")
	case "gitlab_insufficient_scope":
		return fmt.Errorf("the GitLab automation token needs the api scope")
	case "gitlab_instance_conflict":
		return fmt.Errorf("GitLab instance does not match the organization's saved repository identities; check the origin and connection")
	case "gitlab_instance_not_public":
		return fmt.Errorf("the GitLab instance must be reachable over public HTTPS")
	case "gitlab_repository_inaccessible", "repository_projects_inaccessible":
		return fmt.Errorf("the GitLab automation account needs Developer access or higher to this repository")
	}
	switch apiErr.StatusCode {
	case 401:
		return fmt.Errorf("Revyl authentication is no longer valid; run revyl auth login")
	case 403:
		return fmt.Errorf("access denied; check Revyl organization permissions and GitLab token permissions")
	case 422:
		return fmt.Errorf("GitLab connection input was rejected; check the instance origin and token requirements")
	default:
		return fmt.Errorf("Revyl could not complete the GitLab request (HTTP %d); retry %s", apiErr.StatusCode, command)
	}
}

type gitlabStatusReport struct {
	Enabled        bool                     `json:"enabled"`
	Connected      bool                     `json:"connected"`
	InstanceURL    *string                  `json:"instance_url"`
	TokenExpiresAt *time.Time               `json:"token_expires_at"`
	Repository     *reviewStatusRepository  `json:"repository"`
	Project        *reviewStatusProject     `json:"project"`
	Webhook        *api.GitlabWebhookStatus `json:"webhook"`
	NextAction     string                   `json:"next_action,omitempty"`
	ProjectError   string                   `json:"project_error,omitempty"`
}

func runGitlabStatus(cmd *cobra.Command) error {
	client, err := newSCMAPIClient(cmd)
	if err != nil {
		return err
	}
	report, err := collectGitlabStatus(cmd, client)
	if err != nil {
		return err
	}
	if projectConfigurationJSON(cmd) {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(report)
	}
	ui.PrintKeyValue("API connected:", fmt.Sprint(report.Connected))
	if report.InstanceURL != nil {
		ui.PrintKeyValue("Instance:", *report.InstanceURL)
	}
	if report.TokenExpiresAt != nil {
		ui.PrintKeyValue("Token expires:", report.TokenExpiresAt.Format(time.RFC3339))
	}
	if report.Repository != nil {
		ui.PrintKeyValue("Repository:", fmt.Sprintf("%s (access: %t)", report.Repository.FullName, report.Repository.AccessGranted))
	}
	if report.Project != nil {
		ui.PrintKeyValue("Project:", report.Project.Status)
	}
	if report.Webhook != nil {
		ui.PrintKeyValue("Webhook verified:", fmt.Sprint(report.Webhook.VerifiedAt != nil))
	}
	if report.ProjectError != "" {
		ui.PrintWarning("%s", report.ProjectError)
	}
	if report.NextAction != "" {
		ui.PrintDim("Next: %s", report.NextAction)
	}
	return nil
}

func collectGitlabStatus(cmd *cobra.Command, client *api.Client) (gitlabStatusReport, error) {
	connection, err := client.GetGitlabConnection(cmd.Context())
	if err != nil {
		return gitlabStatusReport{}, gitlabRequestError(err, "revyl gitlab status")
	}
	report := gitlabStatusReport{Enabled: true, Connected: gitlabConnected(connection), InstanceURL: connection.InstanceUrl, TokenExpiresAt: connection.TokenExpiresAt}
	if !report.Connected {
		report.NextAction = "revyl gitlab connect"
		return report, nil
	}
	if err := client.CheckGitlabAccess(cmd.Context()); err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.DetailString("code") == "gitlab_token_invalid" {
			report.Connected = false
			report.NextAction = "revyl gitlab connect"
			return report, nil
		}
		return report, gitlabRequestError(err, "revyl gitlab status")
	}
	local, localErr := resolveLocalProjectConfiguration()
	worktreeRoot := ""
	if localErr == nil {
		worktreeRoot = local.WorktreeRoot
	} else {
		cwd, err := configWorkingDirectory()
		if err != nil {
			return report, nil
		}
		_, root, err := resolveConfigPullRoot(cwd, "")
		if err != nil {
			return report, nil
		}
		worktreeRoot = root
	}
	repository, err := resolveProjectRepository(worktreeRoot)
	if err != nil {
		report.NextAction = "configure a GitLab Git origin"
		return report, nil
	}
	if repository.Provider != "gitlab" {
		report.NextAction = "run revyl github status for this GitHub checkout"
		return report, nil
	}
	report.Repository = &reviewStatusRepository{FullName: repository.Namespace + "/" + repository.Name}
	if repository.InstanceURL == nil || connection.InstanceUrl == nil || *repository.InstanceURL != *connection.InstanceUrl {
		report.NextAction = "Git origin and connected GitLab instance must match"
		return report, nil
	}
	_, err = client.ListRepositoryProjects(cmd.Context(), api.RepositoryProjectCatalogQuery{Provider: api.RepositoryProjectCatalogQueryProviderGitlab, InstanceUrl: repository.InstanceURL, Namespace: repository.Namespace, RepositoryName: repository.Name})
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && (apiErr.StatusCode == 403 || apiErr.StatusCode == 404) {
			report.NextAction = "grant the automation account Developer access to this GitLab repository"
			return report, nil
		}
		return report, gitlabRequestError(err, "revyl gitlab status")
	}
	report.Repository.AccessGranted = true
	report.Webhook, err = client.GetGitlabWebhook(cmd.Context(), repository.Namespace, repository.Name)
	if err != nil {
		return report, gitlabRequestError(err, "revyl gitlab status")
	}
	if report.Webhook.VerifiedAt == nil {
		report.NextAction = "revyl gitlab setup"
	}
	if localErr != nil {
		report.ProjectError = actionableLocalConfigError(localErr).Error()
		return report, nil
	}
	resolved, err := connectProjectConfiguration(local, repository)
	if err != nil {
		report.ProjectError = err.Error()
		return report, nil
	}
	current, err := readRemoteProjectConfiguration(cmd, client, resolved)
	if err != nil {
		report.ProjectError = actionableProjectConfigurationAPIError(
			cmd.Context(),
			client,
			resolved.locator,
			resolved.local.Authored.Project.ID,
			err,
			"revyl gitlab status",
			resolved.local,
		).Error()
		return report, nil
	}
	report.Project = reviewProjectStatusFromRead(local, current)
	if report.Project.NextAction != "" {
		report.NextAction = report.Project.NextAction
	}
	return report, nil
}

func runGitlabSetup(cmd *cobra.Command) error {
	recordGitlabOutcome(cmd, "setup", "failed")
	if !reviewSetupInputTTY() {
		return fmt.Errorf("revyl gitlab setup requires an interactive terminal; use gitlab connect --token-stdin, then edit .revyl/config.yaml and run config validate/push or commit the Git-managed file")
	}
	local, err := resolveLocalProjectConfiguration()
	if err != nil {
		return actionableLocalConfigError(err)
	}
	resolved, err := resolveConnectedProjectConfiguration(local)
	if err != nil {
		return err
	}
	client, err := newSCMAPIClient(cmd)
	if err != nil {
		return err
	}
	return runGitlabSetupForProject(cmd, client, local, resolved)
}

func runGitlabSetupForProject(cmd *cobra.Command, client *api.Client, local *config.ProjectContext, resolved *resolvedProjectConfiguration) error {
	if resolved.locator.Provider != api.ProjectConfigurationRepositoryLocatorProviderGitlab {
		return fmt.Errorf("this repository uses GitHub; run revyl github setup")
	}
	connection, err := client.GetGitlabConnection(cmd.Context())
	if err != nil {
		return gitlabRequestError(err, "revyl gitlab setup")
	}
	if !gitlabConnected(connection) {
		if resolved.locator.InstanceUrl == nil {
			return fmt.Errorf("GitLab repository instance is missing; check the Git origin")
		}
		instance := *resolved.locator.InstanceUrl
		token, err := readGitlabToken(cmd.InOrStdin(), false)
		if err != nil {
			return err
		}
		connection, err = client.ConnectGitlab(cmd.Context(), api.GitlabConnectRequest{AccessToken: &token, InstanceUrl: &instance})
		if err != nil {
			return gitlabRequestError(err, "revyl gitlab setup")
		}
	}
	if !gitlabConnected(connection) || connection.InstanceUrl == nil || resolved.locator.InstanceUrl == nil || *connection.InstanceUrl != *resolved.locator.InstanceUrl {
		return fmt.Errorf("Git origin and connected GitLab instance must match; check revyl gitlab status")
	}
	_, err = client.ListRepositoryProjects(cmd.Context(), api.RepositoryProjectCatalogQuery{
		Provider:       api.RepositoryProjectCatalogQueryProviderGitlab,
		InstanceUrl:    resolved.locator.InstanceUrl,
		Namespace:      resolved.locator.Namespace,
		RepositoryName: resolved.locator.RepositoryName,
	})
	if err != nil {
		return gitlabRequestError(err, "revyl gitlab setup")
	}
	status, err := runProjectReviewSetup(cmd, client, local, resolved)
	if err != nil {
		return err
	}
	recordGitlabOutcome(cmd, "setup", status)
	if status == "declined" {
		return nil
	}
	webhook, err := client.GetGitlabWebhook(cmd.Context(), resolved.locator.Namespace, resolved.locator.RepositoryName)
	if err != nil {
		return gitlabRequestError(err, "revyl gitlab setup")
	}
	if webhook.VerifiedAt != nil {
		ui.PrintSuccess("GitLab webhook delivery verified")
		return nil
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	query := url.Values{"namespace": {resolved.locator.Namespace}, "repository": {resolved.locator.RepositoryName}}
	setupURL := config.GetAppURL(devMode) + "/integrations/gitlab/repository?" + query.Encode()
	ui.PrintInfo("Project configuration prepared; webhook delivery is not verified.")
	ui.PrintDim("Open %s and select Webhook setup in the same Revyl organization.", setupURL)
	ui.PrintDim("A GitLab Maintainer/Owner copies the URL and secret into Settings → Webhooks, enables Merge request and Push events, keeps SSL verification enabled, and sends a test Push event.")
	check, err := confirmGitlabWebhook("Check authenticated webhook delivery now?", true)
	if err != nil {
		return err
	}
	if check {
		webhook, err = client.GetGitlabWebhook(cmd.Context(), resolved.locator.Namespace, resolved.locator.RepositoryName)
		if err != nil {
			return gitlabRequestError(err, "revyl gitlab setup")
		}
		if webhook.VerifiedAt != nil {
			recordGitlabOutcome(cmd, "setup", "webhook_verified")
			ui.PrintSuccess("GitLab webhook delivery verified")
			return nil
		}
	}
	recordGitlabOutcome(cmd, "setup", "webhook_pending")
	return fmt.Errorf("project configuration prepared; webhook delivery is still unverified; finish webhook setup, then rerun revyl gitlab setup")
}
