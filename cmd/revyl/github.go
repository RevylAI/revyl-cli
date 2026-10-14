// Package main provides the `revyl github` commands for GitHub PR automation.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/gitremote"
	"github.com/revyl/cli/internal/ui"
)

// githubConnectPollInterval is how often `revyl github connect` re-checks
// installation status while the user completes the browser install.
var githubConnectPollInterval = 3 * time.Second

// githubConnectPollTimeout bounds how long the CLI waits for the browser
// install to complete before giving up.
var githubConnectPollTimeout = 3 * time.Minute

var errGithubInstallTimedOut = errors.New(
	"timed out waiting for the GitHub App install; finish it in the browser, then run 'revyl github status'",
)

var ensureGithubSetupConnected = ensureGithubConnected

var githubCmd = &cobra.Command{
	Use:   "github",
	Short: "Connect GitHub and configure pull request automation",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
	Long: `Connect the Revyl GitHub App and configure pull request automation for
the nearest project declared by .revyl/config.yaml.

Typical first run:
  revyl github setup

The complete project configuration is published immediately in manual mode.
After the designated config file is committed to the default branch, Git owns
the server configuration and local changes must be validated and committed.`,
}

var githubConnectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Install the Revyl GitHub App via your browser",
	Long: `Connect GitHub by installing the Revyl GitHub App.

This opens the GitHub App install page in your browser. Complete the install
there; the CLI waits and confirms once the installation is active. If the app
is already installed, this is a no-op.

A coding agent can't show the user anything while a command runs, so pass
--no-wait to return as soon as the link is issued; the page still opens when a
browser is available, and the link is printed either way so it can be handed to
the user. Pass --no-open to skip the browser, for example when a link was
already handed out.

With --json, print one JSON object on stdout:
  status (already_connected, connected, link_issued, timed_out), connected,
  install_url, repository_count.

EXAMPLES:
  revyl github connect
  revyl github connect --no-wait --json
  revyl github connect --no-open --json`,
	Args: cobra.NoArgs,
	RunE: runGithubConnect,
}

var githubStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show GitHub and current-project review status",
	Long: `Show whether the Revyl GitHub App is connected for your organization,
whether the current Git repository is granted to that installation, and, when
run under a project configuration, whether pull request automation is
published for that exact repository-relative project root.

With --json, print one JSON object on stdout:
  connected, repository_count, repository {full_name, access_granted},
  project {root, status, authority}, project_error.
repository is null outside a Git worktree with a GitHub origin; project is
null when no .revyl/config.yaml applies or it could not be read.

EXAMPLES:
  revyl github status
  revyl github status --json
  revyl -C apps/mobile github status`,
	Args: cobra.NoArgs,
	RunE: runGithubStatus,
}

var githubSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Configure pull request automation for the current project",
	Long: `Connect GitHub if needed, complete the nearest configured project's
pr_review configuration, bind only the apps required by that review build
mode, atomically update the designated .revyl/config.yaml when necessary, and
publish the complete project configuration.

EXAMPLES:
  revyl github setup`,
	Args: cobra.NoArgs,
	RunE: runGithubSetup,
}

func init() {
	githubConnectCmd.Flags().Bool("no-open", false, "Print the install link instead of opening a browser")
	githubConnectCmd.Flags().Bool("no-wait", false, "Return once the install link is issued instead of waiting for the installation")
	githubCmd.AddCommand(githubConnectCmd, githubStatusCmd, githubSetupCmd)
}

// githubConnectReport is the stable `revyl github connect --json` contract.
type githubConnectReport struct {
	Status          string `json:"status"`
	Connected       bool   `json:"connected"`
	InstallURL      string `json:"install_url,omitempty"`
	RepositoryCount int    `json:"repository_count"`
}

func runGithubConnect(cmd *cobra.Command, _ []string) error {
	client, err := newSCMAPIClient(cmd)
	if err != nil {
		return err
	}
	noOpen, _ := cmd.Flags().GetBool("no-open")
	noWait, _ := cmd.Flags().GetBool("no-wait")
	outcome, err := connectGithub(cmd.Context(), client, githubConnectOptions{
		openBrowser: !noOpen,
		wait:        !noWait,
	})
	completionStatus := outcome.status
	if completionStatus == "" && err != nil {
		completionStatus = githubConnectFailed
	}
	if completionStatus != "" {
		analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{
			Domain:       "github_connect",
			DomainStatus: completionStatus,
		})
	}
	if jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json"); jsonOutput && outcome.status != "" {
		report := githubConnectReport{
			Status:     outcome.status,
			Connected:  outcome.repos.IsConnected(),
			InstallURL: outcome.installURL,
		}
		if outcome.repos != nil {
			report.RepositoryCount = len(outcome.repos.Repositories)
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if encodeErr := encoder.Encode(report); encodeErr != nil && err == nil {
			return encodeErr
		}
		return err
	}
	if err != nil {
		return err
	}
	if outcome.status == githubConnectLinkIssued {
		ui.PrintDim("  After the install finishes, run 'revyl github status' to confirm it.")
		return nil
	}
	ui.Println()
	printGithubStatus(outcome.repos)
	return nil
}

// githubStatusReport is the stable `revyl github status --json` contract; the
// human output renders the same facts.
type githubStatusReport struct {
	Connected       bool                    `json:"connected"`
	RepositoryCount int                     `json:"repository_count"`
	Repository      *reviewStatusRepository `json:"repository"`
	Project         *reviewStatusProject    `json:"project"`
	ProjectError    string                  `json:"project_error,omitempty"`
}

func runGithubStatus(cmd *cobra.Command, _ []string) error {
	client, err := newSCMAPIClient(cmd)
	if err != nil {
		return err
	}
	repos, err := client.GetGithubRepositories(cmd.Context())
	if err != nil {
		return actionableGithubStatusError(err, "revyl github status")
	}
	report := collectGithubStatus(cmd, client, repos)
	if jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json"); jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	printGithubStatusReport(repos, report)
	return nil
}

// collectGithubStatus gathers the installation, repository, and project facts
// behind `revyl github status`. The repository grant is resolved from the Git
// worktree even before a .revyl/config.yaml exists, so a fresh checkout can
// learn whether it must still be added to the installation.
func collectGithubStatus(
	cmd *cobra.Command,
	client projectConfigurationClient,
	repos *api.GithubRepositoriesResponse,
) githubStatusReport {
	report := githubStatusReport{Connected: repos.IsConnected()}
	if repos != nil {
		report.RepositoryCount = len(repos.Repositories)
	}
	if !report.Connected {
		return report
	}

	local, localErr := resolveLocalProjectConfiguration()
	worktreeRoot := ""
	if localErr == nil {
		worktreeRoot = local.WorktreeRoot
	} else {
		report.ProjectError = actionableLocalConfigError(localErr).Error()
		cwd, cwdErr := configWorkingDirectory()
		if cwdErr != nil {
			return report
		}
		_, root, rootErr := resolveConfigPullRoot(cwd, "")
		if rootErr != nil {
			return report
		}
		worktreeRoot = root
	}

	namespace, repositoryName, slugErr := resolveProjectRepoSlug(worktreeRoot, "")
	if slugErr != nil {
		if localErr == nil {
			report.ProjectError = actionableGithubOriginError(worktreeRoot).Error()
		}
		return report
	}
	report.Repository = &reviewStatusRepository{
		FullName:      namespace + "/" + repositoryName,
		AccessGranted: githubRepositoryAvailable(repos, namespace, repositoryName),
	}
	if localErr != nil || !report.Repository.AccessGranted {
		return report
	}

	resolved, err := connectProjectConfiguration(local, gitremote.RepositoryReference{Provider: "github", Namespace: namespace, Name: repositoryName})
	if err != nil {
		report.ProjectError = err.Error()
		return report
	}
	current, readErr := readRemoteProjectConfiguration(cmd, client, resolved)
	if readErr != nil {
		report.ProjectError = actionableProjectConfigurationAPIError(
			cmd.Context(),
			client,
			resolved.locator,
			resolved.local.Authored.Project.ID,
			readErr,
			"revyl github status",
			resolved.local,
		).Error()
		return report
	}
	report.Project = reviewProjectStatusFromRead(local, current)
	return report
}

func printGithubStatusReport(repos *api.GithubRepositoriesResponse, report githubStatusReport) {
	printGithubStatus(repos)
	if !report.Connected {
		return
	}
	if report.Repository != nil {
		if report.Repository.AccessGranted {
			ui.PrintKeyValue("  Current repository:", report.Repository.FullName+" — access granted")
		} else {
			ui.PrintKeyValue("  Current repository:", report.Repository.FullName+" — access not granted")
			ui.PrintDim("  Grant this repository to the Revyl GitHub App, then retry.")
		}
	}
	if report.ProjectError != "" {
		ui.PrintWarning("  Current project: %s", report.ProjectError)
		return
	}
	if report.Project == nil {
		return
	}
	projectLabel := report.Repository.FullName + " (" + report.Project.Root + ")"
	switch report.Project.Status {
	case reviewProjectStatusNotPublished:
		ui.PrintKeyValue("  Current project:", projectLabel+" — not published")
		ui.PrintDim("  Next: %s to turn on pull request automation.", report.Project.NextAction)
	case reviewProjectStatusInvalid:
		ui.PrintKeyValue("  Current project:", projectLabel+" — invalid server state")
	default:
		ui.PrintKeyValue("  Current project:", projectLabel+" — "+strings.ReplaceAll(report.Project.Status, "_", " "))
		ui.PrintKeyValue("  Authority:", report.Project.Authority)
	}
}

func runGithubSetup(cmd *cobra.Command, _ []string) (returnErr error) {
	recordGithubSetupOutcome(cmd, "failed")
	defer func() {
		if returnErr != nil {
			// Setup output and errors can contain repository, project, path, or app
			// identifiers. Preserve the original user-facing error while keeping
			// centralized failure analytics bounded to the semantic outcome.
			returnErr = analytics.CompletedWithExitCode(
				returnErr,
				analytics.CommandCompletion{Domain: "github_setup", ExitCode: 1},
			)
		}
	}()
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
	return runGithubSetupForProject(cmd, client, local, resolved)
}

func runGithubSetupForProject(
	cmd *cobra.Command,
	client *api.Client,
	local *config.ProjectContext,
	resolved *resolvedProjectConfiguration,
) error {
	if resolved.locator.Provider != api.ProjectConfigurationRepositoryLocatorProviderGithub {
		return fmt.Errorf("this repository uses %s; run revyl %s setup", resolved.locator.Provider, resolved.locator.Provider)
	}
	if !reviewSetupInputTTY() {
		return fmt.Errorf("revyl github setup requires an interactive terminal; edit the project config directly, run %q, then run %q or commit the designated file when Git-managed", cliRecoveryCommand("config", "validate"), cliRecoveryCommand("config", "push"))
	}
	repos, err := ensureGithubSetupConnected(cmd.Context(), client)
	if err != nil {
		return err
	}
	if !githubRepositoryAvailable(repos, resolved.locator.Namespace, resolved.locator.RepositoryName) {
		return fmt.Errorf(
			"the Revyl GitHub App cannot access %s/%s; grant that repository, then retry",
			resolved.locator.Namespace,
			resolved.locator.RepositoryName,
		)
	}
	status, err := runProjectReviewSetup(cmd, client, local, resolved)
	if err == nil {
		recordGithubSetupOutcome(cmd, status)
	}
	return err
}

func recordGithubSetupOutcome(cmd *cobra.Command, status string) {
	if cmd == nil {
		return
	}
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{
		Domain:       "github_setup",
		DomainStatus: status,
	})
}

func githubRepositoryAvailable(repos *api.GithubRepositoriesResponse, namespace, repository string) bool {
	if repos == nil {
		return false
	}
	for _, candidate := range repos.Repositories {
		if strings.EqualFold(candidate.Owner, namespace) && strings.EqualFold(candidate.Repo, repository) {
			return true
		}
	}
	return false
}

const (
	githubConnectAlreadyConnected = "already_connected"
	githubConnectConnected        = "connected"
	githubConnectLinkIssued       = "link_issued"
	githubConnectTimedOut         = "timed_out"
	githubConnectFailed           = "failed"
)

// openGithubInstallPage opens the install link; tests replace it so no browser
// is launched.
var openGithubInstallPage = ui.OpenBrowser

type githubConnectOptions struct {
	openBrowser bool
	wait        bool
}

// githubConnectOutcome is the bounded result of one connect attempt.
type githubConnectOutcome struct {
	status     string
	installURL string
	repos      *api.GithubRepositoriesResponse
}

// ensureGithubConnected returns the current installation state, driving the
// browser install flow when GitHub is not yet connected.
func ensureGithubConnected(ctx context.Context, client *api.Client) (*api.GithubRepositoriesResponse, error) {
	outcome, err := connectGithub(ctx, client, githubConnectOptions{openBrowser: true, wait: true})
	return outcome.repos, err
}

func connectGithub(ctx context.Context, client *api.Client, opts githubConnectOptions) (githubConnectOutcome, error) {
	var outcome githubConnectOutcome
	repos, err := client.GetGithubRepositories(ctx)
	if err != nil {
		return outcome, actionableGithubStatusError(err, "revyl github connect")
	}
	if repos.IsConnected() {
		ui.PrintSuccess("GitHub App already connected")
		outcome.status, outcome.repos = githubConnectAlreadyConnected, repos
		return outcome, nil
	}

	install, err := client.GetGithubInstallURL(ctx)
	if err != nil {
		return outcome, fmt.Errorf("failed to start GitHub install: %w", err)
	}
	outcome.installURL, outcome.repos = install.InstallURL, repos
	switch {
	case !opts.openBrowser:
		ui.PrintInfo("Open this URL to install the Revyl GitHub App:")
		ui.PrintLink("Install Revyl GitHub App", install.InstallURL)
	case openGithubInstallPage(install.InstallURL) != nil:
		ui.PrintWarning("Could not open a browser automatically.")
		ui.PrintInfo("Open this URL to install the Revyl GitHub App:")
		ui.PrintLink("Install Revyl GitHub App", install.InstallURL)
	default:
		ui.PrintInfo("Opened the GitHub App install page in your browser.")
		ui.PrintDim("  If the page didn't open, visit: %s", install.InstallURL)
	}
	if !opts.wait {
		outcome.status = githubConnectLinkIssued
		return outcome, nil
	}

	ui.Println()
	ui.PrintInfo("Waiting for the installation to complete ...")
	connected, err := waitForGithubInstallation(ctx, client)
	switch {
	case err == nil:
		outcome.status, outcome.repos = githubConnectConnected, connected
	case errors.Is(err, errGithubInstallTimedOut):
		outcome.status = githubConnectTimedOut
	}
	return outcome, err
}

func waitForGithubInstallation(ctx context.Context, client *api.Client) (*api.GithubRepositoriesResponse, error) {
	deadline := time.Now().Add(githubConnectPollTimeout)
	ticker := time.NewTicker(githubConnectPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			repos, err := client.GetGithubRepositories(ctx)
			if err == nil && repos.IsConnected() {
				return repos, nil
			}
			if terminalErr := terminalGithubInstallationPollingError(err); terminalErr != nil {
				return nil, terminalErr
			}
			if time.Now().After(deadline) {
				return nil, analytics.WithFailureClass(errGithubInstallTimedOut, analytics.FailureClassGitHub)
			}
		}
	}
}

func terminalGithubInstallationPollingError(err error) error {
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		return nil
	}
	if apiErr.StatusCode < 400 || apiErr.StatusCode >= 500 || apiErr.StatusCode == 408 || apiErr.StatusCode == 429 {
		return nil
	}
	return actionableGithubStatusError(err, "revyl github connect")
}

func actionableGithubStatusError(err error, retryCommand string) error {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401:
			return analytics.WithFailureClass(fmt.Errorf("Revyl authentication is no longer valid; run 'revyl auth login', then retry '%s'", retryCommand), analytics.FailureClassAuth)
		case 403:
			return analytics.WithFailureClass(fmt.Errorf("the active Revyl account cannot access GitHub integration status; run 'revyl auth status' to verify the account and organization, then retry '%s'", retryCommand), analytics.FailureClassAuth)
		default:
			if apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
				return analytics.WithFailureClass(fmt.Errorf("Revyl rejected the GitHub installation status request; run 'revyl auth status' to verify the active account and organization, then retry '%s'; run 'revyl doctor' if it still fails", retryCommand), analytics.FailureClassGitHub)
			}
		}
	}
	return fmt.Errorf("could not fetch GitHub status: %w; retry '%s', then run 'revyl doctor' if it still fails", err, retryCommand)
}

func printGithubStatus(repos *api.GithubRepositoriesResponse) {
	if repos == nil || !repos.IsConnected() {
		ui.PrintWarning("GitHub App not connected")
		ui.PrintDim("  Run 'revyl github connect' to install the Revyl GitHub App.")
		return
	}

	ui.PrintSuccess("GitHub App connected")
	ui.PrintKeyValue("  Repositories:", fmt.Sprintf("%d", len(repos.Repositories)))
	ui.PrintKeyValue("  PR automation:", "available")
	ui.PrintDim("  Run 'revyl github setup' in a project to configure it there.")
}
