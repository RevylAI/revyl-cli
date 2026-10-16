package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/ui"
)

type webURLUploadReviewJSON struct {
	Repository string `json:"repository"`
	PRNumber   int    `json:"pr_number,omitempty"`
	HeadSHA    string `json:"head_sha"`
}

type webURLUploadJSON struct {
	Platform  string                  `json:"platform"`
	AppID     string                  `json:"app_id"`
	Version   string                  `json:"version"`
	VersionID string                  `json:"version_id"`
	WasReused bool                    `json:"was_reused"`
	SourceURL string                  `json:"source_url"`
	CommitSHA string                  `json:"commit_sha,omitempty"`
	Review    *webURLUploadReviewJSON `json:"review,omitempty"`
}

// runWebURLUpload registers a deployment URL as a Web app build. Revyl fetches
// nothing; the URL is what a Web session opens. Re-registering the same URL and
// commit returns the existing build and makes it the app's latest.
func runWebURLUpload(cmd *cobra.Command, apiKey string) error {
	if uploadURLFlag == "" || uploadFileFlag != "" {
		return usageErrorf("--platform web registers a deployment URL; pass --url https://... instead of --file")
	}
	for _, name := range []string{"header", "version", buildPinFlagName, buildSetCurrentFlagName, buildNoSetCurrentFlagName, "preview", "name"} {
		if cmd.Flags().Changed(name) {
			return usageErrorf("--%s is not available with --platform web", name)
		}
	}
	if err := mcppkg.ValidateWebURL(uploadURLFlag); err != nil {
		return usageErrorf("--%s", err)
	}
	branchName := commandBuildBranch(cmd)
	if cmd.Flags().Changed("branch") {
		if err := api.ValidateBranchName(branchName); err != nil {
			return usageErrorf("--branch: %v", err)
		}
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)
	appID, err := resolveWebUploadApp(cmd, client)
	if err != nil {
		return err
	}

	request := &api.ExternalURLBuildRequest{BranchName: branchName, URL: uploadURLFlag, Metadata: build.CIMetadata(cmd.Context(), "web")}
	ci, hasCI := build.CIContextFromContext(cmd.Context())
	if commitSHA := strings.ToLower(strings.TrimSpace(ci.CommitSHA)); hasCI && fullGitCommitSHA.MatchString(commitSHA) {
		request.CommitSHA = commitSHA
	}
	var review *webURLUploadReviewJSON
	if hasCI && ci.SCMProvider == "github" && ci.Repository != "" && ci.CommitSHA != "" {
		review = &webURLUploadReviewJSON{Repository: ci.Repository, PRNumber: ci.ReviewNumber, HeadSHA: ci.CommitSHA}
	}

	ui.PrintBanner(version)
	ui.StartSpinner("Registering URL build...")
	result, err := client.CreateExternalURLBuild(cmd.Context(), appID, request)
	ui.StopSpinner()
	if err != nil {
		ui.PrintError("Registration failed: %v", err)
		return err
	}

	if buildUploadJSON {
		payload := webURLUploadJSON{
			Platform:  "web",
			AppID:     appID,
			Version:   result.Version,
			VersionID: result.ID,
			WasReused: result.WasReused,
			SourceURL: uploadURLFlag,
			CommitSHA: request.CommitSHA,
			Review:    review,
		}
		data, err := json.MarshalIndent(withAppliedDefaults(cmd, payload), "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
		return nil
	}

	if result.WasReused {
		ui.PrintSuccess("URL build already registered; it is now the app's latest")
	} else {
		ui.PrintSuccess("URL build registered")
	}
	ui.PrintInfo("App:             %s", appID)
	ui.PrintInfo("Build Version:   %s", result.Version)
	ui.PrintInfo("Build ID:        %s", result.ID)
	if review != nil {
		reviewLabel := review.Repository + "@" + review.HeadSHA
		if review.PRNumber > 0 {
			reviewLabel = fmt.Sprintf("%s#%d", review.Repository, review.PRNumber)
		}
		ui.PrintInfo("Review:          %s", reviewLabel)
	}
	ui.Println()
	ui.PrintDim("To open it: revyl device start --platform web --app %s --build-version-id %s", appID, result.ID)
	return nil
}

var fullGitCommitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

func resolveWebUploadApp(cmd *cobra.Command, client *api.Client) (string, error) {
	if appFlag := strings.TrimSpace(uploadAppFlag); appFlag != "" {
		if looksLikeUUID(appFlag) {
			return appFlag, nil
		}
		appID, _, err := resolveAppNameOrID(cmd, client, appFlag)
		if err != nil {
			ui.PrintError("Could not resolve app %q: %v", appFlag, err)
			return "", err
		}
		return appID, nil
	}
	appID, err := resolveConfiguredWebAppID()
	if err != nil {
		return "", err
	}
	if appID == "" {
		return "", analytics.WithFailureClass(fmt.Errorf("no Web app is configured; pass '--app <name-or-id>' or add 'build.profiles.<name>.web.app_id' to .revyl/config.yaml (revyl init sets it up)"), analytics.FailureClassConfig)
	}
	recordAppliedDefault(cmd, appliedDefault{Flag: "app", Value: appID, Reason: "project_web_profile"})
	return appID, nil
}

// resolveConfiguredWebAppID reads the Web app from .revyl/config.yaml. A
// missing config is not an error, so URL builds stay usable outside configured
// repositories.
func resolveConfiguredWebAppID() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	project, err := config.ResolveProjectContext(cwd, "")
	if err != nil {
		var configErr *config.ConfigError
		if errors.As(err, &configErr) && configErr.Stage == "read" &&
			(configErr.Code == "config_not_found" || configErr.Code == "git_worktree_unavailable") {
			return "", nil
		}
		return "", actionableLocalConfigError(err)
	}
	appID, err := project.Authored.Build.ConfiguredWebAppID()
	if err != nil {
		return "", analytics.WithFailureClass(fmt.Errorf("%w; pass '--app <name-or-id>'", err), analytics.FailureClassConfig)
	}
	return appID, nil
}
