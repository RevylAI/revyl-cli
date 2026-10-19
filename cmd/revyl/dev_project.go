package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/buildselection"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/devloop"
	"github.com/revyl/cli/internal/ui"
)

// projectDevInvocation is the immutable local configuration selected for one
// development loop. The profile and platform remain explicit for every later
// rebuild so revalidation cannot drift to a different recipe.
type projectDevInvocation struct {
	ProjectRoot         string
	ConfigPath          string
	OriginalConfigBytes []byte
	Profile             string
	Platform            string
	AppID               string
	BranchID            string
	BranchName          string
	GitBranchName       string
	Recipe              config.EffectiveBuildRecipe
	BuildDefinitionHash string
	SelectionSource     string
	Session             config.AuthoredSession

	DetachedAppDefaultSelection bool
}

func projectBuildInvocationFromDev(invocation projectDevInvocation) projectBuildInvocation {
	return projectBuildInvocation{
		ProjectRoot:         invocation.ProjectRoot,
		ConfigPath:          invocation.ConfigPath,
		OriginalConfigBytes: append([]byte(nil), invocation.OriginalConfigBytes...),
		Profile:             invocation.Profile,
		Platform:            invocation.Platform,
		AppID:               invocation.AppID,
		BranchID:            invocation.BranchID,
		Recipe:              cloneEffectiveBuildRecipe(invocation.Recipe),
		BuildDefinitionHash: invocation.BuildDefinitionHash,
		SelectionSource:     invocation.SelectionSource,
	}
}

func (invocation projectDevInvocation) withAppID(appID string) projectDevInvocation {
	invocation.AppID = strings.TrimSpace(appID)
	return invocation
}

func devHotReloadProvider(framework string) string {
	switch strings.TrimSpace(framework) {
	case "expo":
		return "expo"
	case "react_native":
		return "react-native"
	default:
		return ""
	}
}

func devUsesRebuildLoop(framework string) bool {
	return devHotReloadProvider(framework) == ""
}

func devContextProvider(framework string) string {
	if provider := devHotReloadProvider(framework); provider != "" {
		return provider
	}
	switch strings.TrimSpace(framework) {
	case "ios":
		return "xcode"
	case "android":
		return "gradle"
	default:
		return strings.TrimSpace(framework)
	}
}

func initDevSession(projectRoot string, session config.AuthoredSession) {
	initDevAuthBypass(session.AuthBypass)
	initDevBeforeSession(session.BeforeScript, projectRoot)
}

// resolveOptionalProjectContext resolves the nearest canonical config for
// commands that can also operate without a project. A missing config or Git
// worktree preserves standalone device/session behavior; any discovered but
// invalid or legacy config fails before an external action can begin.
func resolveOptionalProjectContext(startDir string) (*config.ProjectContext, error) {
	project, err := config.ResolveProjectContext(startDir, "")
	if err == nil {
		return project, nil
	}
	var configErr *config.ConfigError
	if errors.As(err, &configErr) {
		if configErr.Code == "config_not_found" {
			return nil, nil
		}
		if configErr.Code == "git_worktree_unavailable" {
			candidate := filepath.Join(startDir, ".revyl", "config.yaml")
			if _, statErr := os.Lstat(candidate); errors.Is(statErr, os.ErrNotExist) {
				return nil, nil
			}
		}
	}
	return nil, actionableLocalConfigError(err)
}

func devIdleTimeoutSeconds(cmd *cobra.Command, invocation projectDevInvocation) int {
	if cmd != nil && cmd.Flags().Changed("timeout") {
		if devStartTimeout > 0 {
			return devStartTimeout
		}
		return 300
	}
	if invocation.Session.IdleTimeoutSeconds != nil && *invocation.Session.IdleTimeoutSeconds > 0 {
		return *invocation.Session.IdleTimeoutSeconds
	}
	if devStartTimeout > 0 {
		return devStartTimeout
	}
	return 300
}

func ensureDevApp(cmd *cobra.Command, client *api.Client, invocation projectDevInvocation) (projectDevInvocation, error) {
	if strings.TrimSpace(invocation.AppID) != "" {
		return invocation, nil
	}
	appID, err := selectOrCreateBuildAppForInvocation(cmd, client, projectBuildInvocationFromDev(invocation))
	if err != nil {
		return projectDevInvocation{}, err
	}
	return invocation.withAppID(appID), nil
}

// resolveDevInvocation selects the nearest canonical project configuration
// using the development-specific profile preference. It never supplies a
// platform default: an unresolved platform is prompted for interactively or
// returned as an actionable ambiguity.
func resolveDevInvocation(
	cwd string,
	changeDirectory string,
	explicitProfile string,
	explicitPlatform string,
	interactive bool,
	selectValue buildSelect,
) (projectDevInvocation, error) {
	project, err := config.ResolveProjectContext(cwd, changeDirectory)
	if err != nil {
		return projectDevInvocation{}, err
	}

	profile := strings.TrimSpace(explicitProfile)
	platform := strings.TrimSpace(explicitPlatform)
	selectionSource := "inferred"
	if profile != "" || platform != "" {
		selectionSource = "explicit"
	}

	for {
		selection, err := config.ResolveProfilePlatform(*project.Aggregate, profile, platform, true)
		if err != nil {
			return projectDevInvocation{}, err
		}
		if selection.Resolved != nil {
			profile = selection.Resolved.Profile
			platform = selection.Resolved.Platform
			break
		}
		if selection.Ambiguity == nil {
			return projectDevInvocation{}, fmt.Errorf("development profile and platform selection did not resolve")
		}
		if !interactive || selectValue == nil {
			return projectDevInvocation{}, fmt.Errorf(
				"multiple development choices require %s (available: %s)",
				selection.Ambiguity.RequiredFlag,
				strings.Join(selection.Ambiguity.Choices, ", "),
			)
		}

		options := make([]ui.SelectOption, 0, len(selection.Ambiguity.Choices))
		for _, choice := range selection.Ambiguity.Choices {
			options = append(options, ui.SelectOption{Label: choice, Value: choice})
		}
		label := "Select development profile:"
		if selection.Ambiguity.RequiredFlag == "--platform" {
			label = "Select development platform:"
		}
		_, selected, selectErr := selectValue(label, options, 0)
		if selectErr != nil {
			return projectDevInvocation{}, fmt.Errorf("development selection: %w", selectErr)
		}
		if selection.Ambiguity.RequiredFlag == "--profile" {
			profile = selected
		} else {
			platform = selected
		}
		selectionSource = "prompted"
	}

	configuration, ok := buildablePlatformConfiguration(*project.Aggregate, profile, platform)
	if !ok {
		return projectDevInvocation{}, fmt.Errorf("resolved development recipe is unavailable")
	}
	if err := config.ValidateExecutionRecipe(*configuration.Recipe); err != nil {
		return projectDevInvocation{}, err
	}

	appID := ""
	if configuration.AppID != nil {
		appID = strings.TrimSpace(*configuration.AppID)
	}
	return projectDevInvocation{
		ProjectRoot:         project.ProjectRoot,
		ConfigPath:          project.ConfigPath,
		OriginalConfigBytes: append([]byte(nil), project.OriginalBytes...),
		Profile:             profile,
		Platform:            platform,
		AppID:               appID,
		Recipe:              cloneEffectiveBuildRecipe(*configuration.Recipe),
		BuildDefinitionHash: configuration.BuildDefinitionHash,
		SelectionSource:     selectionSource,
		Session:             cloneDevSession(project.Aggregate.Session),
	}, nil
}

func cloneDevSession(session config.AuthoredSession) config.AuthoredSession {
	cloned := config.AuthoredSession{}
	if session.IdleTimeoutSeconds != nil {
		value := *session.IdleTimeoutSeconds
		cloned.IdleTimeoutSeconds = &value
	}
	if session.BeforeScript != nil {
		cloned.BeforeScript = &config.AuthoredBeforeScript{}
		if session.BeforeScript.ScriptPath != nil {
			value := *session.BeforeScript.ScriptPath
			cloned.BeforeScript.ScriptPath = &value
		}
		if session.BeforeScript.TimeoutSeconds != nil {
			value := *session.BeforeScript.TimeoutSeconds
			cloned.BeforeScript.TimeoutSeconds = &value
		}
	}
	if session.AuthBypass != nil {
		cloned.AuthBypass = &config.AuthoredAuthBypass{
			LaunchVars: append([]string(nil), session.AuthBypass.LaunchVars...),
		}
		if session.AuthBypass.DeepLink != nil {
			value := *session.AuthBypass.DeepLink
			cloned.AuthBypass.DeepLink = &value
		}
	}
	return cloned
}

func resolveDevDestination(ctx context.Context, client *api.Client, invocation projectDevInvocation, branchID, branchName, buildID, mode string, allowCreate bool) (projectDevInvocation, error) {
	selection := api.BuildSelection{BranchID: branchID, BranchName: branchName, BuildID: buildID, Mode: api.BuildSelectionMode(mode)}
	if err := selection.Validate(); err != nil {
		return invocation, err
	}
	if buildID != "" {
		if invocation.AppID == "" {
			detail, err := client.GetBuildVersionDownloadURL(ctx, buildID)
			if err != nil {
				return invocation, fmt.Errorf("resolve development app: %w", err)
			}
			invocation.AppID = detail.AppID
		}
		detail, err := client.ResolveBuildSelection(ctx, invocation.AppID, selection)
		if err != nil {
			return invocation, fmt.Errorf("resolve development seed: %w", err)
		}
		invocation.AppID, invocation.BranchID, invocation.BranchName = detail.AppID, detail.BranchID, detail.BranchName
		return invocation, nil
	}
	var branch *api.BuildBranch
	var err error
	gitBranchName := ""
	if branchID == "" && branchName == "" {
		if inherited, provided := os.LookupEnv(devloop.GitBranchEnvironment); provided {
			gitBranchName = inherited
		} else {
			gitBranch, detectErr := buildselection.DetectGitBranch(ctx, buildselection.GitBranchOptions{WorkDir: invocation.ProjectRoot})
			if detectErr != nil {
				return invocation, detectErr
			}
			gitBranchName = gitBranch.Name
		}
		invocation.GitBranchName = gitBranchName
	}
	if allowCreate {
		branch, err = client.ResolveUploadBranchWithGitBranch(ctx, invocation.AppID, branchID, branchName, gitBranchName)
	} else {
		branch, err = client.ResolveBranchWithGitBranch(ctx, invocation.AppID, branchID, branchName, gitBranchName, api.BuildSelectionMode(mode))
	}
	if err != nil {
		return invocation, fmt.Errorf("resolve development branch: %w", err)
	}
	if branch.BranchSource.FellBackFromGitBranch(gitBranchName) {
		ui.PrintWarning("%s", ui.BranchFallbackNotice(gitBranchName, branch.Name))
	}
	invocation.BranchID, invocation.BranchName = branch.ID, branch.Name
	// Capture the handoff before the detach parent exits after session readiness.
	invocation.DetachedAppDefaultSelection = isDetachedDevAppDefault(branchID, branchName, buildID, mode)
	return invocation, nil
}

func devSelectionIsAppDefault(invocation projectDevInvocation) bool {
	if invocation.GitBranchName != "" || devStartBranch != "" || devStartSeedLatest || strings.TrimSpace(devStartBuildVerID) != "" {
		return false
	}
	return (devStartBranchID == "" && devStartBuildSelection == "") ||
		(invocation.DetachedAppDefaultSelection && devStartBranchID == invocation.BranchID && devStartBuildSelection == string(api.BuildSelectionModeLatest))
}

func devBuildSelection(invocation projectDevInvocation) api.BuildSelection {
	mode := api.BuildSelectionMode(devStartBuildSelection)
	if devStartSeedLatest || devSelectionIsAppDefault(invocation) {
		mode = api.BuildSelectionModeLatest
	}
	return api.BuildSelection{BranchID: invocation.BranchID, Mode: mode, BuildID: strings.TrimSpace(devStartBuildVerID)}
}

func devResolveErrorNeedsBuild(invocation projectDevInvocation, err error) bool {
	return errors.Is(err, api.ErrBranchHasNoBuilds) ||
		(devSelectionIsAppDefault(invocation) && errors.Is(err, api.ErrBranchHasNoCompatibleBuild))
}

func selectDevStartBuild(ctx context.Context, client buildselection.ResolverClient, appID string, invocation projectDevInvocation) (*api.BuildVersion, string, []string, error) {
	selected, source, warnings, err := buildselection.SelectBuildVersion(ctx, client, appID, devBuildSelection(invocation))
	if devResolveErrorNeedsBuild(invocation, err) {
		return nil, "", nil, nil
	}
	return selected, source, warnings, err
}
