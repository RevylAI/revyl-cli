// Package main provides the `revyl github` commands for GitHub PR automation.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/gitremote"
	"github.com/revyl/cli/internal/prconfig"
	"github.com/revyl/cli/internal/ui"
)

var (
	githubInitFramework string
	githubInitForce     bool
	githubPushRepo      string
	githubSetupRepo     string
)

// githubConnectPollInterval is how often `revyl github connect` re-checks
// installation status while the user completes the browser install. It is a
// var (not a const) so tests can shorten it.
var githubConnectPollInterval = 3 * time.Second

// githubConnectPollTimeout bounds how long the CLI waits for the browser
// install to complete before giving up (the user can re-run to continue). It is
// a var (not a const) so tests can shorten it.
var githubConnectPollTimeout = 3 * time.Minute

var githubCmd = &cobra.Command{
	Use:   "github",
	Short: "Connect GitHub and manage PR automation (config-as-code)",
	Long: `Connect the Revyl GitHub App and manage PR automation defined in
.revyl/config.yaml.

Typical first run:
  revyl github setup     Connect GitHub, scaffold pr_review, and push it

The pr_review section is reconciled by Revyl when pushed (or committed to your
default branch), becoming the source of truth for preview builds, proof checks,
and curated workflows on pull requests.`,
}

var githubConnectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Install the Revyl GitHub App via your browser",
	Long: `Connect GitHub by installing the Revyl GitHub App.

This opens the GitHub App install page in your browser. Complete the install
there; the CLI waits and confirms once the installation is active. If the app
is already installed, this is a no-op.

EXAMPLES:
  revyl github connect`,
	RunE: runGithubConnect,
}

var githubStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the GitHub App connection status",
	Long: `Show whether the Revyl GitHub App is connected for your organization,
how many repositories it can access, and whether PR automation is enabled.

EXAMPLES:
  revyl github status`,
	RunE: runGithubStatus,
}

var githubSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Connect GitHub, scaffold pr_review, and push it",
	Long: `One-shot setup for GitHub PR automation.

This connects the Revyl GitHub App (if needed), scaffolds a pr_review section in
.revyl/config.yaml (if missing), and pushes it to Revyl so PR automation is
active immediately.

EXAMPLES:
  revyl github setup
  revyl github setup --repo owner/name`,
	RunE: runGithubSetup,
}

var githubInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Scaffold the pr_review section in .revyl/config.yaml",
	Long: `Detect this repo's mobile build setup and scaffold a pr_review section
in .revyl/config.yaml.

EXAMPLES:
  revyl github init
  revyl github init --framework expo_ios
  revyl github init --force`,
	RunE: runGithubInit,
}

var githubPushCmd = &cobra.Command{
	Use:   "push",
	Short: "Apply .revyl/config.yaml to Revyl without committing",
	Long: `Upload this repo's .revyl/config.yaml and apply its pr_review section to
Revyl immediately, without waiting for a commit/push to your default branch.

The repository is resolved from your git origin remote; override it with --repo.

EXAMPLES:
  revyl github push
  revyl github push --repo owner/name`,
	RunE: runGithubPush,
}

func init() {
	githubInitCmd.Flags().StringVar(
		&githubInitFramework,
		"framework",
		"",
		"Force a build framework (expo_ios, expo_android, react_native_ios, "+
			"react_native_android, native_ios, native_android)",
	)
	githubInitCmd.Flags().BoolVar(
		&githubInitForce,
		"force",
		false,
		"Overwrite an existing pr_review section",
	)
	githubPushCmd.Flags().StringVar(
		&githubPushRepo,
		"repo",
		"",
		"GitHub repository as owner/name (defaults to the git origin remote)",
	)
	githubSetupCmd.Flags().StringVar(
		&githubSetupRepo,
		"repo",
		"",
		"GitHub repository as owner/name (defaults to the git origin remote)",
	)
	githubSetupCmd.Flags().StringVar(
		&githubInitFramework,
		"framework",
		"",
		"Force a build framework when scaffolding (expo_ios, expo_android, "+
			"react_native_ios, react_native_android, native_ios, native_android)",
	)
	githubCmd.AddCommand(githubConnectCmd)
	githubCmd.AddCommand(githubStatusCmd)
	githubCmd.AddCommand(githubInitCmd)
	githubCmd.AddCommand(githubPushCmd)
	githubCmd.AddCommand(githubSetupCmd)
}

// newGithubAPIClient builds an API client for GitHub commands using the active
// credentials and the global --dev flag.
//
// Parameters:
//   - cmd: The cobra command (used for context and the --dev flag).
//
// Returns:
//   - *api.Client: An authenticated API client.
//   - error: A non-nil error when the user is not authenticated.
func newGithubAPIClient(cmd *cobra.Command) (*api.Client, error) {
	apiKey, err := getAPIKey()
	if err != nil {
		return nil, err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	return api.NewClientWithDevMode(apiKey, devMode), nil
}

// runGithubConnect installs the Revyl GitHub App via the browser and waits for
// the installation to become active.
//
// Parameters:
//   - cmd: The cobra command (used for context and the --dev flag).
//   - args: Positional args (unused).
//
// Returns:
//   - error: A non-nil error when authentication fails, the install URL cannot
//     be fetched, or the installation does not complete before the timeout.
func runGithubConnect(cmd *cobra.Command, args []string) error {
	client, err := newGithubAPIClient(cmd)
	if err != nil {
		return err
	}

	repos, err := ensureGithubConnected(cmd.Context(), client)
	if err != nil {
		return err
	}

	ui.Println()
	printGithubStatus(repos)
	return nil
}

// runGithubStatus prints the GitHub App connection status for the org.
//
// Parameters:
//   - cmd: The cobra command (used for context and the --dev flag).
//   - args: Positional args (unused).
//
// Returns:
//   - error: A non-nil error when authentication or the status request fails.
func runGithubStatus(cmd *cobra.Command, args []string) error {
	client, err := newGithubAPIClient(cmd)
	if err != nil {
		return err
	}
	repos, err := client.GetGithubRepositories(cmd.Context())
	if err != nil {
		return fmt.Errorf("failed to fetch GitHub status: %w", err)
	}

	// PR automation is configured per repository; only fetch per-repo configs
	// when the org actually has the feature and an active installation.
	if !repos.IsConnected() || !repos.GithubIntegrationEnabled {
		printGithubStatus(repos)
		return nil
	}

	configs, cfgErr := client.ListGithubScmConfigs(cmd.Context())
	printGithubStatusDetailed(repos, configs, cfgErr)
	return nil
}

// runGithubSetup connects GitHub (if needed), scaffolds the pr_review section
// (if missing), and pushes the config so PR automation is active immediately.
//
// Parameters:
//   - cmd: The cobra command (used for context and the --dev flag).
//   - args: Positional args (unused).
//
// Returns:
//   - error: A non-nil error when any step (connect, scaffold, push) fails.
func runGithubSetup(cmd *cobra.Command, args []string) error {
	client, err := newGithubAPIClient(cmd)
	if err != nil {
		return err
	}

	if _, err := ensureGithubConnected(cmd.Context(), client); err != nil {
		return err
	}

	configPath, err := projectConfigPath()
	if err != nil {
		return err
	}
	root := filepath.Dir(filepath.Dir(configPath))

	cfg, err := config.LoadProjectConfig(configPath)
	if err != nil {
		cfg = &config.ProjectConfig{
			Project: config.Project{Name: filepath.Base(root)},
		}
	}

	if cfg.PRReview == nil {
		ui.Println()
		ui.PrintInfo("Scaffolding pr_review config from detected builds ...")
		if err := prconfig.Scaffold(root, configPath, cfg, githubInitFramework, false); err != nil {
			return err
		}
		ui.PrintSuccess("Wrote pr_review config to %s", configPath)
	}

	ui.Println()
	return pushPRReviewConfig(cmd.Context(), client, configPath, githubSetupRepo)
}

// ensureGithubConnected returns the current installation state, driving the
// browser install flow when GitHub is not yet connected.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - client: The authenticated API client.
//
// Returns:
//   - *api.GithubRepositoriesResponse: The active installation state.
//   - error: A non-nil error when the status check, install URL fetch, browser
//     launch, or the wait-for-active poll fails or times out.
func ensureGithubConnected(
	ctx context.Context,
	client *api.Client,
) (*api.GithubRepositoriesResponse, error) {
	repos, err := client.GetGithubRepositories(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch GitHub status: %w", err)
	}
	if repos.IsConnected() {
		ui.PrintSuccess("GitHub App already connected")
		return repos, nil
	}

	install, err := client.GetGithubInstallURL(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start GitHub install: %w", err)
	}

	ui.PrintInfo("Opening the GitHub App install page in your browser ...")
	if openErr := ui.OpenBrowser(install.InstallURL); openErr != nil {
		ui.PrintWarning("Could not open a browser automatically.")
		ui.PrintInfo("Open this URL to install the Revyl GitHub App:")
		ui.PrintLink("Install Revyl GitHub App", install.InstallURL)
	} else {
		ui.PrintDim("  If the page didn't open, visit: %s", install.InstallURL)
	}

	ui.Println()
	ui.PrintInfo("Waiting for the installation to complete ...")
	active, err := waitForGithubInstallation(ctx, client)
	if err != nil {
		return nil, err
	}
	return active, nil
}

// waitForGithubInstallation polls installation status until the GitHub App is
// active or the timeout elapses.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - client: The authenticated API client.
//
// Returns:
//   - *api.GithubRepositoriesResponse: The active installation state.
//   - error: A non-nil error when the context is cancelled or the timeout is
//     reached before the installation becomes active.
func waitForGithubInstallation(
	ctx context.Context,
	client *api.Client,
) (*api.GithubRepositoriesResponse, error) {
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
			if time.Now().After(deadline) {
				return nil, fmt.Errorf(
					"timed out waiting for the GitHub App install; " +
						"finish it in the browser, then run 'revyl github status'",
				)
			}
		}
	}
}

// printGithubStatus prints a concise summary of the installation state. The
// "PR automation" line reflects the org-level feature gate (availability), not
// per-repo state — use printGithubStatusDetailed for the per-repo breakdown.
//
// Parameters:
//   - repos: The installation state to summarize.
func printGithubStatus(repos *api.GithubRepositoriesResponse) {
	if repos == nil || !repos.IsConnected() {
		ui.PrintWarning("GitHub App not connected")
		ui.PrintDim("  Run 'revyl github connect' to install the Revyl GitHub App.")
		return
	}

	ui.PrintSuccess("GitHub App connected")
	ui.PrintKeyValue("  Repositories:", fmt.Sprintf("%d", len(repos.Repositories)))
	if repos.GithubIntegrationEnabled {
		ui.PrintKeyValue("  PR automation:", "available for your org")
		ui.PrintDim("  Run 'revyl github push' in a repo to enable it there.")
	} else {
		ui.PrintKeyValue("  PR automation:", "not enabled for your org yet")
		ui.PrintDim("  Contact Revyl to enable PR automation for your org.")
	}
}

// printGithubStatusDetailed prints a per-repository PR-automation summary: how
// many of the accessible repos have automation enabled, plus the status of the
// repository in the current working directory (when resolvable).
//
// Parameters:
//   - repos: The installation state (for repo access count).
//   - configs: The per-repo PR-automation configs (may be nil on error).
//   - cfgErr: A non-nil error when configs could not be loaded.
func printGithubStatusDetailed(
	repos *api.GithubRepositoriesResponse,
	configs *api.GithubScmConfigsResponse,
	cfgErr error,
) {
	ui.PrintSuccess("GitHub App connected")
	ui.PrintKeyValue("  Repositories:", fmt.Sprintf("%d", len(repos.Repositories)))

	if cfgErr != nil || configs == nil {
		ui.PrintKeyValue("  PR automation:", "available for your org")
		ui.PrintDim("  Could not load per-repo configuration; try 'revyl github status' again.")
		return
	}

	enabled := 0
	for i := range configs.Configs {
		if configs.Configs[i].IsAutomationEnabled() {
			enabled++
		}
	}
	ui.PrintKeyValue(
		"  PR automation:",
		fmt.Sprintf("enabled on %d of %d repositories", enabled, len(repos.Repositories)),
	)

	// Best-effort: when run inside a GitHub repo, report that repo's status.
	namespace, project, err := currentRepoSlug()
	if err != nil {
		return
	}
	fullName := namespace + "/" + project
	cfg := findRepoConfig(configs.Configs, fullName)
	switch {
	case cfg.IsAutomationEnabled():
		ui.PrintKeyValue("  This repo:", fullName+" — enabled")
	case cfg != nil:
		ui.PrintKeyValue("  This repo:", fullName+" — configured but disabled")
	default:
		ui.PrintKeyValue("  This repo:", fullName+" — not configured")
		ui.PrintDim("  Run 'revyl github push' to enable PR automation here.")
	}
}

// currentRepoSlug resolves the GitHub owner/name of the repository in the
// current working directory from its git origin remote.
//
// Returns:
//   - string: The repository owner/namespace.
//   - string: The repository name.
//   - error: A non-nil error when the directory or remote cannot be resolved.
func currentRepoSlug() (string, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	root := cwd
	if repoRoot, findErr := config.FindRepoRoot(cwd); findErr == nil {
		root = repoRoot
	}
	return gitremote.ResolveSlug(root, "")
}

// findRepoConfig returns the config matching fullName ("owner/name"), or nil.
//
// Parameters:
//   - configs: The per-repo configs to search.
//   - fullName: The "owner/name" identity to match (case-insensitive).
//
// Returns:
//   - *api.GithubScmConfigResponse: The matching config, or nil when none matches.
func findRepoConfig(configs []api.GithubScmConfigResponse, fullName string) *api.GithubScmConfigResponse {
	for i := range configs {
		if strings.EqualFold(configs[i].RepoFullName, fullName) {
			return &configs[i]
		}
	}
	return nil
}

// runGithubInit scaffolds the pr_review section into .revyl/config.yaml.
//
// Parameters:
//   - cmd: The cobra command (unused).
//   - args: Positional args (unused).
//
// Returns:
//   - error: A non-nil error when the repo cannot be resolved, the framework
//     flag is invalid, pr_review already exists without --force, or the config
//     file cannot be written.
func runGithubInit(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}
	root := cwd
	if repoRoot, findErr := config.FindRepoRoot(cwd); findErr == nil {
		root = repoRoot
	}

	configPath, err := projectConfigPath()
	if err != nil {
		return err
	}

	cfg, err := config.LoadProjectConfig(configPath)
	if err != nil {
		cfg = &config.ProjectConfig{
			Project: config.Project{Name: filepath.Base(root)},
		}
	}

	if err := prconfig.Scaffold(root, configPath, cfg, githubInitFramework, githubInitForce); err != nil {
		return err
	}

	ui.PrintSuccess("Wrote pr_review config to %s", configPath)
	ui.Println()
	ui.PrintInfo("Detected builds:")
	for _, platform := range []string{"ios", "android"} {
		entry := prconfig.EntryForPlatform(cfg.PRReview.Builds, platform)
		if entry != nil && entry.Enabled {
			ui.PrintKeyValue("  "+prconfig.PlatformLabel(platform)+":", entry.Framework)
		}
	}
	ui.Println()
	ui.PrintInfo("Next steps:")
	ui.PrintDim("  1. Review %s (set app names and any required env secrets)", configPath)
	ui.PrintDim("  2. Run 'revyl github push' to apply it now, or")
	ui.PrintDim("  3. git add .revyl/config.yaml, commit, and merge to your default branch")
	return nil
}

// runGithubPush uploads the local .revyl/config.yaml and applies its pr_review
// section to Revyl immediately (no commit/push required).
//
// Parameters:
//   - cmd: The cobra command (used for context and the --dev flag).
//   - args: Positional args (unused).
//
// Returns:
//   - error: A non-nil error when the config file is missing/lacks a pr_review
//     section, the repo cannot be resolved, authentication fails, or the
//     backend rejects the config.
func runGithubPush(cmd *cobra.Command, args []string) error {
	client, err := newGithubAPIClient(cmd)
	if err != nil {
		return err
	}

	configPath, err := projectConfigPath()
	if err != nil {
		return err
	}
	root := filepath.Dir(filepath.Dir(configPath))

	cfg, err := config.LoadProjectConfig(configPath)
	if err != nil {
		cfg = &config.ProjectConfig{
			Project: config.Project{Name: filepath.Base(root)},
		}
	}

	if cfg.PRReview == nil {
		ui.PrintInfo("No pr_review section found in %s; scaffolding one ...", configPath)
		if err := prconfig.Scaffold(root, configPath, cfg, "", false); err != nil {
			return err
		}
		ui.PrintSuccess("Wrote pr_review config to %s", configPath)
		ui.Println()
	}

	return pushPRReviewConfig(cmd.Context(), client, configPath, githubPushRepo)
}

// pushPRReviewConfig reads the config file and applies its pr_review section to
// Revyl for the resolved repository.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - client: The authenticated API client.
//   - configPath: The .revyl/config.yaml path to upload.
//   - repoOverride: Optional "owner/name" override for the target repo.
//
// Returns:
//   - error: A non-nil error when the repo cannot be resolved, the file cannot
//     be read, GitHub is not connected (403/404), or the backend rejects the
//     config.
func pushPRReviewConfig(
	ctx context.Context,
	client *api.Client,
	configPath string,
	repoOverride string,
) error {
	content, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", configPath, err)
	}

	root := filepath.Dir(filepath.Dir(configPath))
	namespace, project, err := gitremote.ResolveSlug(root, repoOverride)
	if err != nil {
		return err
	}

	relPath := ".revyl/config.yaml"
	if rel, relErr := filepath.Rel(root, configPath); relErr == nil {
		relPath = filepath.ToSlash(rel)
	}

	ui.PrintInfo("Pushing pr_review config for %s/%s ...", namespace, project)
	resp, err := client.PushPRReviewConfig(ctx, api.PushPRReviewConfigRequest{
		Namespace:      namespace,
		Project:        project,
		Content:        string(content),
		ConfigFilePath: relPath,
	})
	if err != nil {
		if isGithubNotConnectedErr(err) {
			ui.PrintError("GitHub PR automation isn't available for this repo yet.")
			ui.PrintDim("  Run 'revyl github connect' to install the Revyl GitHub App,")
			ui.PrintDim("  and make sure %s/%s is one of the granted repositories.", namespace, project)
		}
		return fmt.Errorf("failed to push config: %w", err)
	}

	state := resp.State
	if state.Status == "error" {
		ui.PrintError("Config pushed but could not be applied")
		if state.Error != "" {
			ui.PrintDim("  %s", state.Error)
		}
		return fmt.Errorf("config file error")
	}

	ui.PrintSuccess("Applied pr_review config to %s/%s", namespace, project)
	if state.Summary != nil && len(state.Summary.Builds) > 0 {
		ui.Println()
		ui.PrintInfo("Preview builds:")
		for _, b := range state.Summary.Builds {
			ui.PrintKeyValue("  "+prconfig.PlatformLabel(b.Platform)+":", b.Framework)
		}
	}
	ui.Println()
	ui.PrintDim("The repo settings page will update automatically.")
	return nil
}

// isGithubNotConnectedErr reports whether err indicates the org has no active
// GitHub App installation or PR-automation access for the target repo.
//
// Parameters:
//   - err: The error returned by a push/config request.
//
// Returns:
//   - bool: true when the error is an HTTP 403 or 404 APIError.
func isGithubNotConnectedErr(err error) bool {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusForbidden ||
			apiErr.StatusCode == http.StatusNotFound
	}
	return false
}
