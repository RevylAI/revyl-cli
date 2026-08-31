package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/spf13/cobra"
)

const (
	githubSetupIOSAppID     = "22222222-2222-4222-8222-222222222222"
	githubSetupAndroidAppID = "33333333-3333-4333-8333-333333333333"
)

func githubSetupContext(t *testing.T, authored config.AuthoredConfig) *config.ProjectContext {
	t.Helper()
	raw, err := config.MarshalCanonicalConfig(authored)
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := config.NormalizeAuthoredConfig(
		authored,
		config.CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."},
	)
	if err != nil {
		t.Fatal(err)
	}
	return &config.ProjectContext{
		ProjectRoot:                   "/repo/mobile",
		ConfigPath:                    "/repo/mobile/.revyl/config.yaml",
		RepositoryRelativeProjectRoot: ".",
		Authored:                      &authored,
		Aggregate:                     aggregate,
		OriginalBytes:                 raw,
	}
}

func withGithubSetupAppSelector(
	t *testing.T,
	selector func(*cobra.Command, *api.Client, *config.ProjectContext, string) (string, error),
) {
	t.Helper()
	original := selectGithubSetupApp
	selectGithubSetupApp = selector
	t.Cleanup(func() { selectGithubSetupApp = original })
}

func TestCompleteGithubSetupBindsOnlySelectedManagedProfile(t *testing.T) {
	commands := []string{"build"}
	profileName := "preview"
	authored := config.AuthoredConfig{
		Project: config.AuthoredProject{ID: configRemoteProjectID},
		Build: &config.AuthoredBuild{
			Framework: "react_native",
			Profiles: map[string]config.AuthoredBuildProfile{
				"preview":    {IOS: &config.AuthoredBuildRecipe{BuildCommands: &commands}},
				"production": {Android: &config.AuthoredBuildRecipe{BuildCommands: &commands}},
			},
		},
		PRReview: &config.AuthoredPRReview{
			Build: config.AuthoredReviewBuild{Kind: "revyl", Profile: &profileName},
		},
	}
	local := githubSetupContext(t, authored)
	calls := []string{}
	withGithubSetupAppSelector(t, func(_ *cobra.Command, _ *api.Client, _ *config.ProjectContext, platform string) (string, error) {
		calls = append(calls, platform)
		return githubSetupIOSAppID, nil
	})

	candidate, changed, err := completeGithubSetupConfiguration(testConfigCommand(), nil, local)
	if err != nil {
		t.Fatalf("completeGithubSetupConfiguration() error = %v", err)
	}
	if !changed || !reflect.DeepEqual(calls, []string{"ios"}) {
		t.Fatalf("changed = %v, app selections = %v", changed, calls)
	}
	if got := candidate.Build.Profiles["preview"].IOS.AppID; got == nil || *got != githubSetupIOSAppID {
		t.Fatalf("preview iOS app = %v", got)
	}
	if got := candidate.Build.Profiles["production"].Android.AppID; got != nil {
		t.Fatalf("unselected production Android app = %v, want nil", got)
	}
	if got := local.Authored.Build.Profiles["preview"].IOS.AppID; got != nil {
		t.Fatalf("original config was mutated: app = %v", got)
	}
}

func TestCompleteGithubSetupCreatesExplicitCIUploadBindings(t *testing.T) {
	authored := config.AuthoredConfig{Project: config.AuthoredProject{ID: configRemoteProjectID}}
	local := githubSetupContext(t, authored)
	originalPrompt := promptGithubSetupSelect
	promptGithubSetupSelect = func(_ string, _ []string) (int, error) { return 2, nil }
	t.Cleanup(func() { promptGithubSetupSelect = originalPrompt })
	withGithubSetupAppSelector(t, func(_ *cobra.Command, _ *api.Client, _ *config.ProjectContext, platform string) (string, error) {
		if platform == "ios" {
			return githubSetupIOSAppID, nil
		}
		return githubSetupAndroidAppID, nil
	})

	candidate, changed, err := completeGithubSetupConfiguration(testConfigCommand(), nil, local)
	if err != nil {
		t.Fatalf("completeGithubSetupConfiguration() error = %v", err)
	}
	if !changed || candidate.PRReview == nil || candidate.PRReview.Build.Kind != "ci_upload_to_revyl" {
		t.Fatalf("candidate = %#v, changed = %v", candidate.PRReview, changed)
	}
	appIDs := candidate.PRReview.Build.AppIDs
	if appIDs == nil || appIDs.IOS == nil || *appIDs.IOS != githubSetupIOSAppID || appIDs.Android == nil || *appIDs.Android != githubSetupAndroidAppID {
		t.Fatalf("CI app IDs = %#v", appIDs)
	}
	proof := candidate.PRReview.ProofOfChanges
	if proof == nil || proof.Enabled == nil || !*proof.Enabled || proof.Harness == nil || proof.Harness.Kind != "revyl" {
		t.Fatalf("proof defaults = %#v, want enabled Revyl harness", proof)
	}
}

func TestCompleteGithubSetupLeavesCompletePolicyUnchanged(t *testing.T) {
	commands := []string{"build"}
	profileName := "preview"
	appID := githubSetupIOSAppID
	authored := config.AuthoredConfig{
		Project: config.AuthoredProject{ID: configRemoteProjectID},
		Build: &config.AuthoredBuild{
			Framework: "ios",
			Profiles: map[string]config.AuthoredBuildProfile{
				"preview": {IOS: &config.AuthoredBuildRecipe{AppID: &appID, BuildCommands: &commands}},
			},
		},
		PRReview: &config.AuthoredPRReview{Build: config.AuthoredReviewBuild{Kind: "revyl", Profile: &profileName}},
	}
	local := githubSetupContext(t, authored)
	withGithubSetupAppSelector(t, func(_ *cobra.Command, _ *api.Client, _ *config.ProjectContext, _ string) (string, error) {
		t.Fatal("complete policy prompted for an app")
		return "", nil
	})

	_, changed, err := completeGithubSetupConfiguration(testConfigCommand(), nil, local)
	if err != nil {
		t.Fatalf("completeGithubSetupConfiguration() error = %v", err)
	}
	if changed {
		t.Fatal("completeGithubSetupConfiguration() changed a complete policy")
	}
}

func TestGithubSetupRejectsNonInteractiveUseBeforeConnecting(t *testing.T) {
	local := githubSetupContext(t, config.AuthoredConfig{
		Project: config.AuthoredProject{ID: configRemoteProjectID},
	})
	resolved, err := resolveConnectedProjectConfigurationForTest(local)
	if err != nil {
		t.Fatal(err)
	}
	originalTTY := githubSetupInputTTY
	originalEnsure := ensureGithubSetupConnected
	githubSetupInputTTY = func() bool { return false }
	connectionAttempted := false
	ensureGithubSetupConnected = func(context.Context, *api.Client) (*api.GithubRepositoriesResponse, error) {
		connectionAttempted = true
		return nil, nil
	}
	t.Cleanup(func() {
		githubSetupInputTTY = originalTTY
		ensureGithubSetupConnected = originalEnsure
	})

	err = runGithubSetupForProject(testConfigCommand(), &api.Client{}, local, resolved)
	if err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("runGithubSetupForProject() error = %v", err)
	}
	for _, command := range []string{"revyl config validate", "revyl config push", "commit the designated file"} {
		if !strings.Contains(err.Error(), command) {
			t.Fatalf("runGithubSetupForProject() error = %v, want %q", err, command)
		}
	}
	if connectionAttempted {
		t.Fatal("non-interactive setup attempted to connect GitHub")
	}
}

func TestGithubSetupPreparesGitManagedProposalWithoutPublishing(t *testing.T) {
	commands := []string{"build"}
	outputPath := "dist/app.app"
	authored := config.AuthoredConfig{
		Project: config.AuthoredProject{ID: configRemoteProjectID},
		Build: &config.AuthoredBuild{
			Framework: "ios",
			Profiles: map[string]config.AuthoredBuildProfile{
				"preview": {IOS: &config.AuthoredBuildRecipe{
					BuildCommands: &commands,
					OutputPath:    &outputPath,
				}},
			},
		},
	}
	local := githubSetupContext(t, authored)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, local.OriginalBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	local.ConfigPath = configPath
	local.WorktreeRoot = filepath.Dir(configPath)
	resolved, err := resolveConnectedProjectConfigurationForTest(local)
	if err != nil {
		t.Fatal(err)
	}

	originalEnsure := ensureGithubSetupConnected
	originalTTY := githubSetupInputTTY
	originalPrompt := promptGithubSetupSelect
	originalConfirm := confirmGithubSetup
	originalSelector := selectGithubSetupApp
	originalValidate := validateGithubSetupConfig
	originalPublish := publishGithubSetupConfig
	originalSlug := resolveProjectRepoSlug
	ensureGithubSetupConnected = func(context.Context, *api.Client) (*api.GithubRepositoriesResponse, error) {
		return &api.GithubRepositoriesResponse{
			Repositories: []api.GithubOrgRepository{{Owner: "acme", Repo: "mobile"}},
			Installation: &api.GithubOrgInstallation{Status: "active"},
			HasAccess:    true,
		}, nil
	}
	githubSetupInputTTY = func() bool { return true }
	promptGithubSetupSelect = func(_ string, _ []string) (int, error) { return 0, nil }
	confirmationPrompt := ""
	confirmGithubSetup = func(prompt string, _ bool) (bool, error) {
		confirmationPrompt = prompt
		return true, nil
	}
	selectGithubSetupApp = func(_ *cobra.Command, _ *api.Client, _ *config.ProjectContext, _ string) (string, error) {
		return githubSetupIOSAppID, nil
	}
	validateGithubSetupConfig = func(_ *cobra.Command, _ *api.Client, proposal *resolvedProjectConfiguration) (*api.ProjectConfigurationValidateResponse, error) {
		if proposal.authored.PrReview == nil {
			t.Fatal("validation did not receive the completed aggregate")
		}
		return &api.ProjectConfigurationValidateResponse{
			Status: "valid",
			Current: api.ProjectConfigurationReadResponse{
				State: api.ProjectConfigurationReadResponseStatePresent,
				Resource: &api.ProjectConfigurationResource{
					Authority: api.ConfigurationAuthorityGitDefaultBranch,
				},
			},
		}, nil
	}
	publicationAttempted := false
	publishGithubSetupConfig = func(_ *cobra.Command, _ projectConfigurationClient, _ *resolvedProjectConfiguration) error {
		publicationAttempted = true
		return nil
	}
	resolveProjectRepoSlug = func(string, string) (string, string, error) { return "acme", "mobile", nil }
	t.Cleanup(func() {
		ensureGithubSetupConnected = originalEnsure
		githubSetupInputTTY = originalTTY
		promptGithubSetupSelect = originalPrompt
		confirmGithubSetup = originalConfirm
		selectGithubSetupApp = originalSelector
		validateGithubSetupConfig = originalValidate
		publishGithubSetupConfig = originalPublish
		resolveProjectRepoSlug = originalSlug
	})

	err = runGithubSetupForProject(testConfigCommand(), &api.Client{}, local, resolved)
	if err != nil {
		t.Fatalf("runGithubSetupForProject() error = %v", err)
	}
	if confirmationPrompt != "Write this complete project configuration for commit?" {
		t.Fatalf("confirmation prompt = %q", confirmationPrompt)
	}
	if publicationAttempted {
		t.Fatal("Git-authoritative configuration attempted a manual publication")
	}
	updatedBytes, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	updated, parseErr := config.ParseAuthoredConfig(updatedBytes)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	if updated.PRReview == nil || updated.Build.Profiles["preview"].IOS.AppID == nil {
		t.Fatalf("local proposal was not preserved: %#v", updated)
	}
	if got := updated.Build.Profiles["preview"].IOS.OutputPath; got == nil || *got != outputPath {
		t.Fatalf("output_path changed: %v", got)
	}
}

func resolveConnectedProjectConfigurationForTest(
	local *config.ProjectContext,
) (*resolvedProjectConfiguration, error) {
	authored, err := authoredConfigForAPI(*local.Authored)
	if err != nil {
		return nil, err
	}
	return &resolvedProjectConfiguration{
		local: local,
		locator: api.ProjectConfigurationRepositoryLocator{
			Provider:                      "github",
			Namespace:                     "acme",
			RepositoryName:                "mobile",
			RepositoryRelativeProjectRoot: local.RepositoryRelativeProjectRoot,
		},
		authored: authored,
	}, nil
}
