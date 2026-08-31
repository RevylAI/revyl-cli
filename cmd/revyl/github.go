// Package main provides the `revyl github` commands for GitHub PR automation.
package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// githubConnectPollInterval is how often `revyl github connect` re-checks
// installation status while the user completes the browser install.
var githubConnectPollInterval = 3 * time.Second

// githubConnectPollTimeout bounds how long the CLI waits for the browser
// install to complete before giving up.
var githubConnectPollTimeout = 3 * time.Minute

var (
	ensureGithubSetupConnected = ensureGithubConnected
	selectGithubSetupApp       = selectOrCreateGithubSetupApp
	promptGithubSetupSelect    = ui.PromptSelect
	confirmGithubSetup         = ui.PromptConfirm
	githubSetupInputTTY        = ui.IsInputTTY
	validateGithubSetupConfig  = validateResolvedProjectConfiguration
	publishGithubSetupConfig   = publishResolvedProjectConfiguration
)

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

EXAMPLES:
  revyl github connect`,
	Args: cobra.NoArgs,
	RunE: runGithubConnect,
}

var githubStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show GitHub and current-project review status",
	Long: `Show whether the Revyl GitHub App is connected for your organization
and, when run under a project configuration, whether pull request
automation is published for that exact repository-relative project root.

EXAMPLES:
  revyl github status`,
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
	githubCmd.AddCommand(githubConnectCmd, githubStatusCmd, githubSetupCmd)
}

// newGithubAPIClient builds an API client for GitHub commands using the active
// credentials and the global --dev flag.
func newGithubAPIClient(cmd *cobra.Command) (*api.Client, error) {
	apiKey, err := getAPIKey()
	if err != nil {
		return nil, err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	return api.NewClientWithDevMode(apiKey, devMode), nil
}

func runGithubConnect(cmd *cobra.Command, _ []string) error {
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

func runGithubStatus(cmd *cobra.Command, _ []string) error {
	client, err := newGithubAPIClient(cmd)
	if err != nil {
		return err
	}
	repos, err := client.GetGithubRepositories(cmd.Context())
	if err != nil {
		return actionableGithubStatusError(err, "revyl github status")
	}
	printGithubStatus(repos)
	if !repos.IsConnected() {
		return nil
	}

	local, err := resolveLocalProjectConfiguration()
	if err != nil {
		ui.PrintWarning("  Current project: %v", actionableLocalConfigError(err))
		return nil
	}
	resolved, err := resolveConnectedProjectConfiguration(local)
	if err != nil {
		ui.PrintWarning("  Current project: %v", err)
		return nil
	}
	fullName := resolved.locator.Namespace + "/" + resolved.locator.RepositoryName
	if !githubRepositoryAvailable(repos, resolved.locator.Namespace, resolved.locator.RepositoryName) {
		ui.PrintKeyValue("  Current project:", fullName+" — repository access not granted")
		ui.PrintDim("  Grant this repository to the Revyl GitHub App, then retry.")
		return nil
	}

	current, readErr := readRemoteProjectConfiguration(cmd, client, resolved)
	if readErr != nil {
		ui.PrintWarning(
			"  Current project: %v",
			actionableProjectConfigurationAPIError(
				cmd.Context(),
				client,
				resolved.locator,
				resolved.local.Authored.Project.ID,
				readErr,
				"revyl github status",
				resolved.local,
			),
		)
		return nil
	}
	printGithubProjectStatus(local, fullName, current)
	return nil
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
	client, err := newGithubAPIClient(cmd)
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
	if !githubSetupInputTTY() {
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
	candidate, changed, err := completeGithubSetupConfiguration(cmd, client, local)
	if err != nil {
		return err
	}
	canonical, err := config.MarshalCanonicalConfig(candidate)
	if err != nil {
		return err
	}
	aggregate, err := config.NormalizeAuthoredConfig(candidate, publicationCompilationContext(local))
	if err != nil {
		return err
	}
	updatedLocal := *local
	updatedLocal.Authored = &candidate
	updatedLocal.Aggregate = aggregate
	updatedResolved, err := resolveConnectedProjectConfiguration(&updatedLocal)
	if err != nil {
		return err
	}
	validation, err := validateGithubSetupConfig(cmd, client, updatedResolved)
	if err != nil {
		return actionableProjectConfigurationAPIError(
			cmd.Context(),
			client,
			updatedResolved.locator,
			updatedResolved.local.Authored.Project.ID,
			err,
			"revyl github setup",
			updatedResolved.local,
		)
	}

	ui.Println()
	ui.PrintInfo("Project: %s/%s (%s)", resolved.locator.Namespace, resolved.locator.RepositoryName, local.RepositoryRelativeProjectRoot)
	printGithubSetupBuildSummary(candidate.PRReview.Build)
	if changed {
		ui.PrintKeyValue("Config file:", local.ConfigPath)
	} else {
		ui.PrintDim("The local pr_review configuration is already complete.")
	}
	gitManaged := validation.Current.Resource != nil && validation.Current.Resource.Authority == api.ConfigurationAuthorityGitDefaultBranch
	if gitManaged && !changed {
		recordGithubSetupOutcome(cmd, "proposal_preserved_for_commit")
		ui.PrintSuccess("The default-branch configuration is ready")
		ui.PrintDim("Run %q, then commit the designated .revyl/config.yaml.", cliRecoveryCommand("config", "validate"))
		return nil
	}
	confirmationPrompt := "Publish this complete project configuration to Revyl?"
	if gitManaged {
		confirmationPrompt = "Write this complete project configuration for commit?"
	}
	confirmed, err := confirmGithubSetup(confirmationPrompt, true)
	if err != nil {
		return err
	}
	if !confirmed {
		recordGithubSetupOutcome(cmd, "declined")
		ui.PrintDim("GitHub setup cancelled; the local configuration was not changed.")
		return nil
	}

	if changed {
		if err := config.ReplaceConfigAtomically(local.ConfigPath, canonical, local.OriginalBytes); err != nil {
			return err
		}
		updatedLocal.OriginalBytes = canonical
		ui.PrintSuccess("Updated %s", local.ConfigPath)
	}
	if gitManaged {
		recordGithubSetupOutcome(cmd, "proposal_preserved_for_commit")
		ui.PrintSuccess("Prepared the default-branch configuration for commit")
		ui.PrintDim("Run %q, then commit the designated .revyl/config.yaml.", cliRecoveryCommand("config", "validate"))
		return nil
	}
	if err := publishGithubSetupConfig(cmd, client, updatedResolved); err != nil {
		return err
	}
	recordGithubSetupOutcome(cmd, "published")
	return nil
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

func validateResolvedProjectConfiguration(
	cmd *cobra.Command,
	client *api.Client,
	resolved *resolvedProjectConfiguration,
) (*api.ProjectConfigurationValidateResponse, error) {
	return client.ValidateProjectConfiguration(
		cmd.Context(),
		resolved.local.Authored.Project.ID,
		api.ProjectConfigurationValidateRequest{
			Locator: resolved.locator, Configuration: resolved.authored,
		},
	)
}

func completeGithubSetupConfiguration(
	cmd *cobra.Command,
	client *api.Client,
	local *config.ProjectContext,
) (config.AuthoredConfig, bool, error) {
	candidateBytes, err := config.MarshalCanonicalConfig(*local.Authored)
	if err != nil {
		return config.AuthoredConfig{}, false, err
	}
	candidate, err := config.ParseAuthoredConfig(candidateBytes)
	if err != nil {
		return config.AuthoredConfig{}, false, err
	}
	changed := false

	if candidate.PRReview == nil {
		reviewBuild, ciPlatforms, buildErr := promptGithubReviewBuild(*candidate)
		if buildErr != nil {
			return config.AuthoredConfig{}, false, buildErr
		}
		for _, platform := range ciPlatforms {
			appID, selectErr := selectGithubSetupApp(cmd, client, local, platform)
			if selectErr != nil {
				return config.AuthoredConfig{}, false, fmt.Errorf("select %s app for CI uploads: %w", platform, selectErr)
			}
			if platform == "ios" {
				reviewBuild.AppIDs.IOS = &appID
			} else {
				reviewBuild.AppIDs.Android = &appID
			}
		}
		proofEnabled := true
		candidate.PRReview = &config.AuthoredPRReview{
			Build: reviewBuild,
			ProofOfChanges: &config.AuthoredProofOfChanges{
				Enabled: &proofEnabled,
				Harness: &config.AuthoredProofHarness{Kind: "revyl"},
			},
		}
		changed = true
	}

	switch candidate.PRReview.Build.Kind {
	case "revyl":
		profileName := *candidate.PRReview.Build.Profile
		profile := candidate.Build.Profiles[profileName]
		for _, platform := range []string{"ios", "android"} {
			recipe := githubProfileRecipe(&profile, platform)
			if recipe == nil || recipe.AppID != nil {
				continue
			}
			appID, selectErr := selectGithubSetupApp(cmd, client, local, platform)
			if selectErr != nil {
				return config.AuthoredConfig{}, false, fmt.Errorf("select %s app for profile %q: %w", platform, profileName, selectErr)
			}
			recipe.AppID = &appID
			changed = true
		}
		candidate.Build.Profiles[profileName] = profile
	case "ci_upload_to_revyl":
		// Existing CI-upload policies already carry explicit platform app IDs by
		// contract. New policies receive them in promptGithubReviewBuild.
	default:
		return config.AuthoredConfig{}, false, fmt.Errorf("unsupported pr_review build kind %q", candidate.PRReview.Build.Kind)
	}

	if err := candidate.ValidateContract(); err != nil {
		return config.AuthoredConfig{}, false, err
	}
	if _, err := config.NormalizeAuthoredConfig(*candidate, publicationCompilationContext(local)); err != nil {
		return config.AuthoredConfig{}, false, err
	}
	return *candidate, changed, nil
}

func promptGithubReviewBuild(authored config.AuthoredConfig) (config.AuthoredReviewBuild, []string, error) {
	profileNames := []string{}
	if authored.Build != nil {
		for name := range authored.Build.Profiles {
			profileNames = append(profileNames, name)
		}
		sort.Strings(profileNames)
	}

	buildKind := "ci_upload_to_revyl"
	if len(profileNames) > 0 {
		selection, err := promptGithubSetupSelect(
			"How should pull request builds be produced?",
			[]string{"Build with Revyl from a named profile", "Upload builds from CI"},
		)
		if err != nil {
			return config.AuthoredReviewBuild{}, nil, err
		}
		if selection == 0 {
			buildKind = "revyl"
		}
	}

	if buildKind == "revyl" {
		profileName := profileNames[0]
		if len(profileNames) > 1 {
			selection, err := promptGithubSetupSelect("Which build profile should pull requests use?", profileNames)
			if err != nil {
				return config.AuthoredReviewBuild{}, nil, err
			}
			profileName = profileNames[selection]
		}
		return config.AuthoredReviewBuild{Kind: buildKind, Profile: &profileName}, nil, nil
	}

	selection, err := promptGithubSetupSelect(
		"Which platforms will CI upload to Revyl?",
		[]string{"iOS", "Android", "iOS and Android"},
	)
	if err != nil {
		return config.AuthoredReviewBuild{}, nil, err
	}
	platforms := [][]string{{"ios"}, {"android"}, {"ios", "android"}}[selection]
	return config.AuthoredReviewBuild{
		Kind:   buildKind,
		AppIDs: &config.AuthoredExternalCIAppIDs{},
	}, platforms, nil
}

func githubProfileRecipe(profile *config.AuthoredBuildProfile, platform string) *config.AuthoredBuildRecipe {
	if platform == "ios" {
		return profile.IOS
	}
	return profile.Android
}

func selectOrCreateGithubSetupApp(
	cmd *cobra.Command,
	client *api.Client,
	local *config.ProjectContext,
	platform string,
) (string, error) {
	projectName := filepath.Base(local.ProjectRoot)
	if projectName == "." || projectName == string(filepath.Separator) || projectName == "" {
		projectName = "project"
	}
	return selectOrCreateAppChoice(cmd, client, projectName, platform)
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

func printGithubSetupBuildSummary(build config.AuthoredReviewBuild) {
	if build.Kind == "revyl" && build.Profile != nil {
		ui.PrintKeyValue("Review builds:", "Revyl profile "+*build.Profile)
		return
	}
	platforms := []string{}
	if build.AppIDs != nil {
		if build.AppIDs.IOS != nil {
			platforms = append(platforms, "iOS")
		}
		if build.AppIDs.Android != nil {
			platforms = append(platforms, "Android")
		}
	}
	ui.PrintKeyValue("Review builds:", "CI uploads for "+strings.Join(platforms, " and "))
}

func printGithubProjectStatus(
	local *config.ProjectContext,
	fullName string,
	current *api.ProjectConfigurationReadResponse,
) {
	projectLabel := fullName + " (" + local.RepositoryRelativeProjectRoot + ")"
	if current == nil || current.State == api.ProjectConfigurationReadResponseStateAbsent {
		ui.PrintKeyValue("  Current project:", projectLabel+" — not published")
		ui.PrintDim("  Run 'revyl github setup' to configure pull request automation.")
		return
	}
	if current.State != api.ProjectConfigurationReadResponseStatePresent || current.Resource == nil {
		ui.PrintKeyValue("  Current project:", projectLabel+" — invalid server state")
		return
	}
	status := "not configured"
	if review := current.Resource.Configuration.PrReview; review != nil {
		if review.Enabled == nil || *review.Enabled {
			status = "enabled"
		} else {
			status = "configured but disabled"
		}
	}
	ui.PrintKeyValue("  Current project:", projectLabel+" — "+status)
	ui.PrintKeyValue("  Authority:", string(current.Resource.Authority))
}

// ensureGithubConnected returns the current installation state, driving the
// browser install flow when GitHub is not yet connected.
func ensureGithubConnected(ctx context.Context, client *api.Client) (*api.GithubRepositoriesResponse, error) {
	repos, err := client.GetGithubRepositories(ctx)
	if err != nil {
		return nil, actionableGithubStatusError(err, "revyl github connect")
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
	return waitForGithubInstallation(ctx, client)
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
				return nil, fmt.Errorf(
					"timed out waiting for the GitHub App install; finish it in the browser, then run 'revyl github status'",
				)
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
			return fmt.Errorf("Revyl authentication is no longer valid; run 'revyl auth login', then retry '%s'", retryCommand)
		case 403:
			return fmt.Errorf("the active Revyl account cannot access GitHub integration status; run 'revyl auth status' to verify the account and organization, then retry '%s'", retryCommand)
		default:
			if apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
				return fmt.Errorf("Revyl rejected the GitHub installation status request; run 'revyl auth status' to verify the active account and organization, then retry '%s'; run 'revyl doctor' if it still fails", retryCommand)
			}
		}
	}
	return fmt.Errorf("could not fetch GitHub status: %v; retry '%s', then run 'revyl doctor' if it still fails", err, retryCommand)
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
