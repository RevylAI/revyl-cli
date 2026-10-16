package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
	"github.com/spf13/cobra"
)

func runProjectReviewSetup(cmd *cobra.Command, client *api.Client, local *config.ProjectContext, resolved *resolvedProjectConfiguration) (string, error) {
	setupCommand := cliRecoveryCommand(string(resolved.locator.Provider), "setup")
	candidate, changed, err := completeReviewSetupConfiguration(cmd, client, local)
	if err != nil {
		return "", err
	}
	canonical, err := config.MarshalCanonicalConfig(candidate)
	if err != nil {
		return "", err
	}
	aggregate, err := config.NormalizeAuthoredConfig(candidate, publicationCompilationContext(local))
	if err != nil {
		return "", err
	}
	updatedLocal := *local
	updatedLocal.Authored = &candidate
	updatedLocal.Aggregate = aggregate
	updatedResolved, err := resolveConnectedProjectConfiguration(&updatedLocal)
	if err != nil {
		return "", err
	}
	validation, err := validateReviewSetupConfig(cmd, client, updatedResolved)
	if err != nil {
		return "", actionableProjectConfigurationAPIError(
			cmd.Context(),
			client,
			updatedResolved.locator,
			updatedResolved.local.Authored.Project.ID,
			err,
			setupCommand,
			updatedResolved.local,
		)
	}

	ui.Println()
	ui.PrintInfo("Project: %s/%s (%s)", resolved.locator.Namespace, resolved.locator.RepositoryName, local.RepositoryRelativeProjectRoot)
	printReviewSetupBuildSummary(candidate.PRReview.Build)
	if changed {
		ui.PrintKeyValue("Config file:", local.ConfigPath)
	} else {
		ui.PrintDim("The local pr_review configuration is already complete.")
	}
	gitManaged := validation.Current.Resource != nil && validation.Current.Resource.Authority == api.ConfigurationAuthorityGitDefaultBranch
	if gitManaged && !changed {
		ui.PrintSuccess("The default-branch configuration is ready")
		ui.PrintDim("Run %q, then commit the designated .revyl/config.yaml.", cliRecoveryCommand("config", "validate"))
		return "proposal_preserved_for_commit", nil
	}
	confirmationPrompt := "Publish this complete project configuration to Revyl?"
	if gitManaged {
		confirmationPrompt = "Write this complete project configuration for commit?"
	}
	confirmed, err := confirmReviewSetup(confirmationPrompt, true)
	if err != nil {
		return "", err
	}
	if !confirmed {
		ui.PrintDim("Setup cancelled; the local configuration was not changed.")
		return "declined", nil
	}

	if changed {
		if err := config.ReplaceConfigAtomically(local.ConfigPath, canonical, local.OriginalBytes); err != nil {
			return "", err
		}
		updatedLocal.OriginalBytes = canonical
		ui.PrintSuccess("Updated %s", local.ConfigPath)
	}
	if gitManaged {
		ui.PrintSuccess("Prepared the default-branch configuration for commit")
		ui.PrintDim("Run %q, then commit the designated .revyl/config.yaml.", cliRecoveryCommand("config", "validate"))
		return "proposal_preserved_for_commit", nil
	}
	if err := publishReviewSetupConfig(cmd, client, updatedResolved); err != nil {
		return "", err
	}
	return "published", nil
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

func completeReviewSetupConfiguration(
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
		reviewBuild, ciPlatforms, buildErr := promptReviewBuild(*candidate)
		if buildErr != nil {
			return config.AuthoredConfig{}, false, buildErr
		}
		for _, platform := range ciPlatforms {
			appID, selectErr := selectReviewSetupApp(cmd, client, local, platform)
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
			recipe := reviewProfileRecipe(&profile, platform)
			if recipe == nil || recipe.AppID != nil {
				continue
			}
			appID, selectErr := selectReviewSetupApp(cmd, client, local, platform)
			if selectErr != nil {
				return config.AuthoredConfig{}, false, fmt.Errorf("select %s app for profile %q: %w", platform, profileName, selectErr)
			}
			recipe.AppID = &appID
			changed = true
		}
		candidate.Build.Profiles[profileName] = profile
	case "ci_upload_to_revyl":
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

func promptReviewBuild(authored config.AuthoredConfig) (config.AuthoredReviewBuild, []string, error) {
	profileNames := []string{}
	if authored.Build != nil {
		for name := range authored.Build.Profiles {
			profileNames = append(profileNames, name)
		}
		sort.Strings(profileNames)
	}

	buildKind := "ci_upload_to_revyl"
	if len(profileNames) > 0 {
		selection, err := promptReviewSetupSelect(
			"How should review builds be produced?",
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
			selection, err := promptReviewSetupSelect("Which build profile should reviews use?", profileNames)
			if err != nil {
				return config.AuthoredReviewBuild{}, nil, err
			}
			profileName = profileNames[selection]
		}
		return config.AuthoredReviewBuild{Kind: buildKind, Profile: &profileName}, nil, nil
	}

	selection, err := promptReviewSetupSelect(
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

func reviewProfileRecipe(profile *config.AuthoredBuildProfile, platform string) *config.AuthoredBuildRecipe {
	if platform == "ios" {
		return profile.IOS
	}
	return profile.Android
}

func selectOrCreateReviewSetupApp(
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

func printReviewSetupBuildSummary(build config.AuthoredReviewBuild) {
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
		if build.AppIDs.Web != nil {
			platforms = append(platforms, "Web")
		}
	}
	ui.PrintKeyValue("Review builds:", "CI uploads for "+strings.Join(platforms, " and "))
}

var (
	selectReviewSetupApp      = selectOrCreateReviewSetupApp
	promptReviewSetupSelect   = ui.PromptSelect
	confirmReviewSetup        = ui.PromptConfirm
	reviewSetupInputTTY       = ui.IsInputTTY
	validateReviewSetupConfig = validateResolvedProjectConfiguration
	publishReviewSetupConfig  = publishResolvedProjectConfiguration
)

func newSCMAPIClient(cmd *cobra.Command) (*api.Client, error) {
	apiKey, err := getAPIKey()
	if err != nil {
		return nil, err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	return api.NewClientWithDevMode(apiKey, devMode), nil
}

type reviewStatusRepository struct {
	FullName      string `json:"full_name"`
	AccessGranted bool   `json:"access_granted"`
}

type reviewStatusProject struct {
	Root       string `json:"root"`
	Status     string `json:"status"`
	Authority  string `json:"authority,omitempty"`
	NextAction string `json:"next_action,omitempty"`
}

const (
	reviewProjectStatusNotPublished  = "not_published"
	reviewProjectStatusEnabled       = "enabled"
	reviewProjectStatusDisabled      = "disabled"
	reviewProjectStatusNotConfigured = "not_configured"
	reviewProjectStatusInvalid       = "invalid"
)

func reviewProjectStatusFromRead(
	local *config.ProjectContext,
	current *api.ProjectConfigurationReadResponse,
) *reviewStatusProject {
	project := &reviewStatusProject{Root: local.RepositoryRelativeProjectRoot}
	switch {
	case current == nil || current.State == api.ProjectConfigurationReadResponseStateAbsent:
		project.Status = reviewProjectStatusNotPublished
		project.NextAction = unpublishedConfigurationNextAction(local.Authored)
	case current.State != api.ProjectConfigurationReadResponseStatePresent || current.Resource == nil:
		project.Status = reviewProjectStatusInvalid
	default:
		project.Status = reviewProjectStatusNotConfigured
		if review := current.Resource.Configuration.PrReview; review != nil {
			if review.Enabled == nil || *review.Enabled {
				project.Status = reviewProjectStatusEnabled
			} else {
				project.Status = reviewProjectStatusDisabled
			}
		}
		project.Authority = string(current.Resource.Authority)
	}
	return project
}
