package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// projectBuildInvocation is the command-owned, immutable result of resolving one
// canonical configuration. Runtime adapters do not reread it; the one explicit
// exception is the existing post-build prompt that may CAS-bind a selected app.
type projectBuildInvocation struct {
	ProjectRoot         string
	ConfigPath          string
	OriginalConfigBytes []byte
	Profile             string
	Platform            string
	AppID               string
	Recipe              config.EffectiveBuildRecipe
	BuildDefinitionHash string
	SelectionSource     string
}

type buildSelect func(message string, options []ui.SelectOption, defaultIndex int) (int, string, error)

var selectOrCreateBuildAppForInvocation = selectOrCreateBuildApp

type buildProgress struct {
	failureStage string
}

type localBuildResult struct {
	Invocation   projectBuildInvocation
	ArtifactPath string
	Duration     time.Duration
	Upload       *api.UploadBuildResponse
}

var remoteCompilationCacheEnvironmentNames = []string{
	"REVYL_XCODE_COMPILATION_CACHE_ENABLED",
	"REVYL_GRADLE_BUILD_CACHE_ENABLED",
	"REVYL_CCACHE_ENABLED",
}

func (p *buildProgress) markFailureStage(stage string) {
	if p != nil {
		p.failureStage = stage
	}
}

func runProjectConfiguredBuild(cmd *cobra.Command) (returnErr error) {
	cmd.SilenceUsage = true
	jsonOutput := buildCommandJSON
	if rootJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); rootJSON {
		jsonOutput = true
	}
	if jsonOutput {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
	}

	mode := "local"
	if buildCommandRemote {
		mode = "remote"
	}
	analyticsProperties := map[string]interface{}{"build_mode": mode}
	progress := &buildProgress{}
	defer func() {
		status := buildDomainStatus(returnErr, buildCommandRemote && buildDetachFlag)
		var completedErr *analytics.CompletedError
		if returnErr != nil && errors.As(returnErr, &completedErr) {
			for key, value := range completedErr.Completion().Properties {
				analyticsProperties[key] = value
			}
		}
		if returnErr != nil && progress.failureStage != "" {
			analyticsProperties["build_failure_stage"] = progress.failureStage
		}
		completion := analytics.CommandCompletion{
			Domain:       "build",
			DomainStatus: status,
			Properties:   analyticsProperties,
		}
		if returnErr != nil {
			completion.ExitCode = 1
		}
		analytics.SetCommandCompletion(cmd.Context(), completion)
		if returnErr != nil {
			if completedErr != nil {
				// The shared remote-build implementation reports its own terminal
				// result. Keep those bounded properties, but make the public build
				// invocation the outer analytical domain.
				returnErr = analytics.CompletedWithExitCode(returnErr, completion)
			}
			// Selection errors, authored commands, and build output can all carry
			// customer data. Preserve the original user-facing error while keeping
			// the centralized failure event deliberately generic.
			returnErr = analytics.WithSafeDiagnostic(returnErr, "build command failed")
		}
	}()

	progress.markFailureStage("validation")
	if err := validateBuildFlags(cmd); err != nil {
		return err
	}
	progress.markFailureStage("authentication")
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}
	progress.markFailureStage("configuration")
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	interactive := ui.IsInteractive() && !jsonOutput
	invocation, err := resolveBuildInvocation(
		cwd,
		"", // Root PersistentPreRun already applied -C before command execution.
		strings.TrimSpace(buildCommandProfile),
		strings.TrimSpace(buildCommandPlatform),
		interactive,
		ui.Select,
	)
	if err != nil {
		return actionableLocalConfigError(err)
	}
	analyticsProperties["build_platform"] = invocation.Platform
	analyticsProperties["selection_source"] = invocation.SelectionSource

	if buildCommandRemote {
		return runProjectRemoteBuild(cmd, invocation, apiKey, jsonOutput, progress)
	}
	return runLocalBuild(cmd, invocation, apiKey, jsonOutput, interactive, progress)
}

func buildDomainStatus(err error, detachedRemote bool) string {
	if err == nil {
		if detachedRemote {
			return "queued"
		}
		return "completed"
	}
	var completedErr *analytics.CompletedError
	if errors.As(err, &completedErr) {
		switch completedErr.Completion().DomainStatus {
		case "cancelled":
			return "cancelled"
		case "failed":
			return "failed"
		}
	}
	if errors.Is(err, errRemoteBuildPollingInterrupted) {
		return "interrupted"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed_out"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "failed"
}

func validateBuildFlags(cmd *cobra.Command) error {
	platform := strings.TrimSpace(buildCommandPlatform)
	if platform != "" && platform != "ios" && platform != "android" {
		return fmt.Errorf("--platform must be ios or android")
	}
	if buildCommandRemote {
		return nil
	}
	if len(buildEnvFlags) > 0 {
		return fmt.Errorf("--env is only supported with --remote")
	}
	if strings.TrimSpace(buildCommandImage) != "" {
		return fmt.Errorf("--image is only supported with --remote")
	}
	if cmd.Flags().Changed("timeout") {
		return fmt.Errorf("--timeout is only supported with --remote")
	}
	if buildDetachFlag {
		return fmt.Errorf("--detach is only supported with --remote")
	}
	if buildNoCacheFlag {
		return fmt.Errorf("--no-cache is only supported with --remote")
	}
	return checkLocalBuildSupported()
}

func resolveBuildInvocation(
	cwd string,
	changeDirectory string,
	explicitProfile string,
	explicitPlatform string,
	interactive bool,
	selectValue buildSelect,
) (projectBuildInvocation, error) {
	project, err := config.ResolveProjectContext(cwd, changeDirectory)
	if err != nil {
		return projectBuildInvocation{}, err
	}
	profile := explicitProfile
	platform := explicitPlatform
	selectionSource := "inferred"
	if profile != "" || platform != "" {
		selectionSource = "explicit"
	}

	for {
		selection, err := config.ResolveProfilePlatform(*project.Aggregate, profile, platform, false)
		if err != nil {
			return projectBuildInvocation{}, err
		}
		if selection.Resolved != nil {
			profile = selection.Resolved.Profile
			platform = selection.Resolved.Platform
			break
		}
		if selection.Ambiguity == nil {
			return projectBuildInvocation{}, fmt.Errorf("build profile and platform selection did not resolve")
		}
		if !interactive || selectValue == nil {
			return projectBuildInvocation{}, fmt.Errorf(
				"multiple build choices require %s (available: %s)",
				selection.Ambiguity.RequiredFlag,
				strings.Join(selection.Ambiguity.Choices, ", "),
			)
		}
		options := make([]ui.SelectOption, 0, len(selection.Ambiguity.Choices))
		for _, choice := range selection.Ambiguity.Choices {
			options = append(options, ui.SelectOption{Label: choice, Value: choice})
		}
		label := "Select build profile:"
		if selection.Ambiguity.RequiredFlag == "--platform" {
			label = "Select build platform:"
		}
		_, selected, selectErr := selectValue(label, options, 0)
		if selectErr != nil {
			return projectBuildInvocation{}, fmt.Errorf("build selection: %w", selectErr)
		}
		if selection.Ambiguity.RequiredFlag == "--profile" {
			profile = selected
		} else {
			platform = selected
		}
		selectionSource = "prompted"
	}

	configuration, ok := platformConfiguration(*project.Aggregate, profile, platform)
	if !ok {
		return projectBuildInvocation{}, fmt.Errorf("resolved build recipe is unavailable")
	}
	if err := config.ValidateExecutionRecipe(configuration.Recipe); err != nil {
		return projectBuildInvocation{}, err
	}
	recipe := cloneEffectiveBuildRecipe(configuration.Recipe)
	appID := ""
	if configuration.AppID != nil {
		appID = strings.TrimSpace(*configuration.AppID)
	}
	return projectBuildInvocation{
		ProjectRoot:         project.ProjectRoot,
		ConfigPath:          project.ConfigPath,
		OriginalConfigBytes: append([]byte(nil), project.OriginalBytes...),
		Profile:             profile,
		Platform:            platform,
		AppID:               appID,
		Recipe:              recipe,
		BuildDefinitionHash: configuration.BuildDefinitionHash,
		SelectionSource:     selectionSource,
	}, nil
}

func platformConfiguration(aggregate config.NormalizedProjectAggregate, profileName, platform string) (config.NormalizedPlatformConfiguration, bool) {
	for _, profile := range aggregate.Profiles {
		if profile.Name != profileName {
			continue
		}
		for _, configuration := range profile.Configurations {
			if configuration.Platform == platform {
				return configuration, true
			}
		}
	}
	return config.NormalizedPlatformConfiguration{}, false
}

func cloneEffectiveBuildRecipe(recipe config.EffectiveBuildRecipe) config.EffectiveBuildRecipe {
	cloned := recipe
	cloned.SetupCommands = append([]string(nil), recipe.SetupCommands...)
	cloned.BuildCommands = append([]string(nil), recipe.BuildCommands...)
	cloned.SecretRefs = append([]string(nil), recipe.SecretRefs...)
	cloned.Env = make(map[string]string, len(recipe.Env))
	for key, value := range recipe.Env {
		cloned.Env[key] = value
	}
	cloned.Caches = make([]config.BuildCache, len(recipe.Caches))
	for index, cache := range recipe.Caches {
		cloned.Caches[index] = config.BuildCache{Key: cache.Key, Paths: append([]string(nil), cache.Paths...)}
	}
	if recipe.OutputPath != nil {
		value := *recipe.OutputPath
		cloned.OutputPath = &value
	}
	if recipe.Image != nil {
		value := *recipe.Image
		cloned.Image = &value
	}
	if recipe.TimeoutSeconds != nil {
		value := *recipe.TimeoutSeconds
		cloned.TimeoutSeconds = &value
	}
	return cloned
}

func runLocalBuild(cmd *cobra.Command, invocation projectBuildInvocation, apiKey string, jsonOutput, interactive bool, progress *buildProgress) error {
	if !jsonOutput {
		ui.PrintBanner(version)
		ui.PrintInfo("Building %s/%s", invocation.Profile, invocation.Platform)
		ui.Println()
	}
	result, err := performLocalBuild(cmd, invocation, apiKey, jsonOutput, interactive, !buildNoSetCurrent, progress)
	if err != nil {
		return err
	}
	return renderLocalBuildResult(result, jsonOutput, true)
}

func performLocalBuild(
	cmd *cobra.Command,
	invocation projectBuildInvocation,
	apiKey string,
	quiet bool,
	interactive bool,
	setAsCurrent bool,
	progress *buildProgress,
) (localBuildResult, error) {
	if invocation.AppID == "" && !interactive {
		progress.markFailureStage("app_resolution")
		return localBuildResult{}, missingNonInteractiveBuildAppError(invocation)
	}

	progress.markFailureStage("secret_validation")
	secretRefs, err := mergeBuildSecretRefs(invocation.Recipe.SecretRefs, buildSecretRefFlags)
	if err != nil {
		return localBuildResult{}, err
	}
	if err := validateBuildEnvSecretCollisions(invocation.Recipe.Env, secretRefs); err != nil {
		return localBuildResult{}, err
	}
	if err := validateLocalSecretEnvironment(secretRefs); err != nil {
		return localBuildResult{}, err
	}

	progress.markFailureStage("configuration")
	if invocation.AppID != "" {
		if _, err := uuid.Parse(invocation.AppID); err != nil {
			return localBuildResult{}, fmt.Errorf("configured app_id for %s/%s must be a UUID", invocation.Profile, invocation.Platform)
		}
	}
	progress.markFailureStage("artifact_resolution")
	if invocation.Recipe.OutputPath == nil || strings.TrimSpace(*invocation.Recipe.OutputPath) == "" {
		return localBuildResult{}, fmt.Errorf("output_path is required to upload the configured %s/%s build", invocation.Profile, invocation.Platform)
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	startedAt := time.Now()
	if err := executeLocalRecipe(cmd.Context(), invocation, quiet, progress); err != nil {
		return localBuildResult{}, err
	}
	duration := time.Since(startedAt)

	progress.markFailureStage("artifact_resolution")
	artifactPath := ""
	if invocation.Recipe.OutputPath != nil && strings.TrimSpace(*invocation.Recipe.OutputPath) != "" {
		artifactPath, err = build.ResolveArtifactPath(invocation.ProjectRoot, strings.TrimSpace(*invocation.Recipe.OutputPath))
		if err != nil {
			return localBuildResult{}, fmt.Errorf("configured output_path was not produced: %w", err)
		}
	}
	if invocation.AppID == "" {
		progress.markFailureStage("app_resolution")
		invocation.AppID, err = selectOrCreateBuildAppForInvocation(cmd, client, invocation)
		if err != nil {
			return localBuildResult{}, err
		}
	}
	progress.markFailureStage("upload")
	upload, err := performLocalArtifactUpload(cmd, invocation, artifactPath, duration, client, setAsCurrent, true)
	if err != nil {
		return localBuildResult{}, err
	}
	return localBuildResult{Invocation: invocation, ArtifactPath: artifactPath, Duration: duration, Upload: upload}, nil
}

func selectOrCreateBuildApp(cmd *cobra.Command, client *api.Client, invocation projectBuildInvocation) (string, error) {
	appID, err := selectOrCreateAppChoice(cmd, client, filepath.Base(invocation.ProjectRoot), invocation.Platform)
	if err != nil {
		return "", err
	}
	save := uploadYesFlag
	if !save {
		save, err = ui.PromptConfirm("Save this app to .revyl/config.yaml for future uploads?", true)
		if err != nil {
			return appID, nil
		}
	}
	if save {
		if err := saveBuildAppBinding(invocation, appID); err != nil {
			ui.PrintWarning("Failed to save config: %v", err)
		} else {
			ui.PrintSuccess("Saved to .revyl/config.yaml")
		}
	}
	return appID, nil
}

func saveBuildAppBinding(invocation projectBuildInvocation, appID string) error {
	authored, err := config.ParseAuthoredConfig(invocation.OriginalConfigBytes)
	if err != nil {
		return err
	}
	if authored.Build == nil {
		return fmt.Errorf("selected build profile is unavailable")
	}
	profile, ok := authored.Build.Profiles[invocation.Profile]
	if !ok {
		return fmt.Errorf("selected build profile is unavailable")
	}
	if invocation.Platform == "ios" {
		if profile.IOS == nil {
			return fmt.Errorf("selected iOS build recipe is unavailable")
		}
		profile.IOS.AppID = &appID
	} else {
		if profile.Android == nil {
			return fmt.Errorf("selected Android build recipe is unavailable")
		}
		profile.Android.AppID = &appID
	}
	authored.Build.Profiles[invocation.Profile] = profile
	replacement, err := config.MarshalCanonicalConfig(*authored)
	if err != nil {
		return err
	}
	return config.ReplaceConfigAtomically(invocation.ConfigPath, replacement, invocation.OriginalConfigBytes)
}

func validateLocalSecretEnvironment(secretRefs []string) error {
	missing := make([]string, 0)
	for _, name := range secretRefs {
		if _, ok := os.LookupEnv(name); !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"local build secrets are not set in the process environment: %s; export them or source a gitignored .env.local before running revyl build",
		strings.Join(missing, ", "),
	)
}

func missingNonInteractiveBuildAppError(invocation projectBuildInvocation) error {
	return fmt.Errorf(
		"no app is configured for %s/%s; add 'build.profiles.%s.%s.app_id' to .revyl/config.yaml and run 'revyl config validate', or retry in an interactive terminal to select or create an app",
		invocation.Profile,
		invocation.Platform,
		invocation.Profile,
		invocation.Platform,
	)
}

func executeLocalRecipe(parentContext context.Context, invocation projectBuildInvocation, jsonOutput bool, progress *buildProgress) error {
	ctx := parentContext
	var cancel context.CancelFunc
	if invocation.Recipe.TimeoutSeconds != nil {
		ctx, cancel = context.WithTimeout(parentContext, time.Duration(*invocation.Recipe.TimeoutSeconds)*time.Second)
		defer cancel()
	}
	runner := build.NewRunner(invocation.ProjectRoot)
	runner.Interactive = !jsonOutput
	runner.FilterOutput = !ui.IsDebugMode()
	run := func(kind string, commands []string) error {
		progress.markFailureStage(kind)
		for index, command := range commands {
			if !jsonOutput {
				ui.PrintDim("%s command %d/%d", kind, index+1, len(commands))
			}
			err := runner.RunContext(ctx, command, build.RunOptions{Environment: invocation.Recipe.Env}, func(line string) {
				if !jsonOutput {
					ui.PrintDim("  %s", line)
				}
			})
			if err != nil {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return fmt.Errorf("%s commands timed out: %w", kind, context.DeadlineExceeded)
				}
				printBuildToolErrorGuidance(err)
				return fmt.Errorf("%s command %d failed: %w", kind, index+1, err)
			}
		}
		return nil
	}
	if err := run("setup", invocation.Recipe.SetupCommands); err != nil {
		return err
	}
	return run("build", invocation.Recipe.BuildCommands)
}

func printBuildToolErrorGuidance(err error) {
	var toolErr *build.BuildToolError
	if !errors.As(err, &toolErr) {
		return
	}
	guidance := strings.TrimSpace(toolErr.Guidance)
	if guidance == "" {
		return
	}
	ui.PrintWarning("How to fix:\n\n%s", guidance)
}

func performLocalArtifactUpload(
	cmd *cobra.Command,
	invocation projectBuildInvocation,
	artifactPath string,
	duration time.Duration,
	client *api.Client,
	setAsCurrent bool,
	artifactProducedByRecipe bool,
) (*api.UploadBuildResponse, error) {
	preparedArtifact, cleanup, err := prepareLocalArtifact(artifactPath)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	versionString := buildVersion
	if versionString == "" {
		versionString = build.GenerateVersionStringForWorkDir(invocation.ProjectRoot)
	}
	metadata, err := localArtifactMetadata(cmd.Context(), invocation, duration, artifactProducedByRecipe)
	if err != nil {
		return nil, err
	}
	result, err := client.UploadBuild(cmd.Context(), &api.UploadBuildRequest{
		AppID: invocation.AppID, Version: versionString, FilePath: preparedArtifact,
		Metadata:     metadata,
		SetAsCurrent: setAsCurrent,
	})
	if err != nil {
		return nil, fmt.Errorf("upload built artifact: %w", err)
	}
	return result, nil
}

func localArtifactMetadata(
	ctx context.Context,
	invocation projectBuildInvocation,
	duration time.Duration,
	artifactProducedByRecipe bool,
) (map[string]interface{}, error) {
	metadata := build.CollectMetadata(invocation.ProjectRoot, strings.Join(invocation.Recipe.BuildCommands, " && "), invocation.Platform, duration)
	if !artifactProducedByRecipe {
		return metadata, nil
	}
	expoMetadata, err := deriveExpoBuildMetadata(ctx, invocation.ProjectRoot, invocation.Recipe.Framework, invocation.Recipe.Env)
	if err != nil {
		return nil, fmt.Errorf("resolve build artifact metadata: %w", err)
	}
	if expoMetadata != nil {
		if err := expoMetadata.attachTo(metadata); err != nil {
			return nil, fmt.Errorf("attach build artifact metadata: %w", err)
		}
	}
	return metadata, nil
}

func renderLocalBuildResult(result localBuildResult, jsonOutput, showNextSteps bool) error {
	invocation := result.Invocation
	if jsonOutput {
		buildResult := newBuildUploadJSONBuild(
			invocation.Platform,
			invocation.Platform,
			invocation.AppID,
			result.ArtifactPath,
			result.Duration,
			result.Upload,
		)
		buildResult.Profile = invocation.Profile
		outputBuildUploadJSON([]BuildUploadJSONBuild{buildResult})
		return nil
	}
	ui.PrintSuccess("Build and upload completed in %s", result.Duration.Round(time.Second))
	ui.PrintInfo("App:             %s", invocation.AppID)
	ui.PrintInfo("Build Version:   %s", result.Upload.Version)
	ui.PrintInfo("Build ID:        %s", result.Upload.VersionID)
	if result.Upload.PackageID != "" {
		ui.PrintInfo("Package ID:      %s", result.Upload.PackageID)
	}
	for _, warning := range result.Upload.Warnings {
		ui.PrintWarning("%s", warning)
	}
	ui.Println()
	ui.PrintDim("To list builds: revyl build list --app %s", invocation.AppID)
	if showNextSteps {
		printLocalBuildNextSteps(invocation.ProjectRoot)
	}
	return nil
}

func resolveBuildContinuation(cwd, profile, platform string, quiet bool) (projectBuildInvocation, error) {
	interactive := ui.IsInteractive() && !quiet
	invocation, err := resolveBuildInvocation(cwd, "", profile, platform, interactive, ui.Select)
	if err != nil {
		return projectBuildInvocation{}, actionableLocalConfigError(err)
	}
	return invocation, nil
}

func runBuildContinuation(cmd *cobra.Command, invocation projectBuildInvocation, apiKey string, quiet bool) (localBuildResult, error) {
	interactive := ui.IsInteractive() && !quiet
	result, err := performLocalBuild(
		cmd,
		invocation,
		apiKey,
		quiet,
		interactive,
		false,
		&buildProgress{},
	)
	if err != nil {
		return localBuildResult{}, err
	}
	if !quiet {
		ui.PrintSuccess("Uploaded: %s", result.Upload.Version)
		ui.Println()
	}
	return result, nil
}

func printLocalBuildNextSteps(projectRoot string) {
	testsDir := filepath.Join(projectRoot, ".revyl", "tests")
	if aliases := config.ListLocalTestAliases(testsDir); len(aliases) > 0 {
		ui.PrintNextSteps([]ui.NextStep{{Label: "Run a test:", Command: fmt.Sprintf("revyl test run %s", aliases[0])}})
		return
	}
	ui.PrintNextSteps([]ui.NextStep{{Label: "Create a test:", Command: "revyl test create <name>"}})
}

func prepareLocalArtifact(artifactPath string) (string, func(), error) {
	if build.IsTarGz(artifactPath) {
		path, err := build.ExtractAppFromTarGz(artifactPath)
		if err != nil {
			return "", nil, fmt.Errorf("extract built app: %w", err)
		}
		return path, func() { _ = os.Remove(path) }, nil
	}
	if build.IsAppBundle(artifactPath) {
		path, err := build.ZipAppBundle(artifactPath)
		if err != nil {
			return "", nil, fmt.Errorf("archive built app: %w", err)
		}
		return path, func() { _ = os.Remove(path) }, nil
	}
	return filepath.Clean(artifactPath), nil, nil
}

func runProjectRemoteBuild(cmd *cobra.Command, invocation projectBuildInvocation, apiKey string, jsonOutput bool, progress *buildProgress) error {
	progress.markFailureStage("configuration")
	if invocation.AppID == "" {
		return fmt.Errorf(
			"no app is configured for remote build %s/%s; add 'build.profiles.%s.%s.app_id' to .revyl/config.yaml, run 'revyl config validate', then retry",
			invocation.Profile,
			invocation.Platform,
			invocation.Profile,
			invocation.Platform,
		)
	}
	if _, err := uuid.Parse(invocation.AppID); err != nil {
		return fmt.Errorf("configured app_id for %s/%s must be a UUID", invocation.Profile, invocation.Platform)
	}
	envOverrides, err := parseRemoteBuildEnvOverrides(buildEnvFlags)
	if err != nil {
		return err
	}
	timeoutSeconds, err := remoteBuildTimeoutFlagSeconds(buildTimeoutSeconds, cmd.Flags().Changed("timeout"))
	if err != nil {
		return err
	}
	effectiveRecipe, definitionHash, err := applyRemoteBuildOverrides(
		invocation.Recipe, envOverrides, buildSecretRefFlags, buildCommandImage, timeoutSeconds, buildNoCacheFlag,
	)
	if err != nil {
		return err
	}
	invocation.Recipe = effectiveRecipe
	invocation.BuildDefinitionHash = definitionHash
	resolved := remoteBuildPlatformConfigFromProject(invocation)
	err = runRemoteBuildWithOptions(cmd, apiKey, remoteBuildOptions{
		Profile: invocation.Profile, ProjectRoot: invocation.ProjectRoot, Resolved: &resolved,
		Platform: invocation.Platform, Version: buildVersion,
		SetCurrent: !buildNoSetCurrent, Clean: buildNoCacheFlag, JSON: jsonOutput,
		Wait: !buildDetachFlag, IncludeDirty: true, TimeoutSeconds: effectiveRecipe.TimeoutSeconds,
		BuildDefinitionHash: invocation.BuildDefinitionHash, SetFailureStage: progress.markFailureStage,
	})
	return actionableRemoteBuildAppReferenceError(err, invocation)
}

func actionableRemoteBuildAppReferenceError(err error, invocation projectBuildInvocation) error {
	if err == nil {
		return nil
	}
	var apiError *api.APIError
	if !errors.As(err, &apiError) {
		return err
	}
	detail := strings.ToLower(strings.TrimSpace(apiError.Detail))
	missingApp := apiError.StatusCode == 404 && strings.Contains(detail, "app not found")
	wrongPlatform := strings.Contains(detail, "does not match app platform")
	if !missingApp && !wrongPlatform {
		return err
	}
	return fmt.Errorf(
		"configured app reference 'build.profiles.%s.%s.app_id' is unavailable or has the wrong platform: %w; run 'revyl app list --platform %s', replace the app_id, run 'revyl config validate', then retry",
		invocation.Profile,
		invocation.Platform,
		err,
		invocation.Platform,
	)
}

func applyRemoteBuildOverrides(
	recipe config.EffectiveBuildRecipe,
	envOverrides map[string]string,
	secretOverrides []string,
	imageOverride string,
	timeoutOverride *int,
	disableCaches bool,
) (config.EffectiveBuildRecipe, string, error) {
	effective := cloneEffectiveBuildRecipe(recipe)
	effective.Env = mergeRemoteBuildEnv(effective.Env, envOverrides)
	var err error
	effective.SecretRefs, err = mergeBuildSecretRefs(effective.SecretRefs, secretOverrides)
	if err != nil {
		return config.EffectiveBuildRecipe{}, "", err
	}
	if err := validateBuildEnvSecretCollisions(effective.Env, effective.SecretRefs); err != nil {
		return config.EffectiveBuildRecipe{}, "", err
	}
	if image := strings.TrimSpace(imageOverride); image != "" {
		effective.Image = &image
	}
	if timeoutOverride != nil {
		effective.TimeoutSeconds = timeoutOverride
	}
	if disableCaches {
		effective.Caches = []config.BuildCache{}
		if effective.Env == nil {
			effective.Env = map[string]string{}
		}
		for _, name := range remoteCompilationCacheEnvironmentNames {
			effective.Env[name] = "0"
		}
	}
	hash, err := config.HashRevylProjection("build-definition", effective)
	if err != nil {
		return config.EffectiveBuildRecipe{}, "", fmt.Errorf("hash effective build recipe: %w", err)
	}
	return effective, hash, nil
}

func remoteBuildPlatformConfigFromProject(invocation projectBuildInvocation) remoteBuildPlatformConfig {
	output := ""
	if invocation.Recipe.OutputPath != nil {
		output = *invocation.Recipe.OutputPath
	}
	image := ""
	if invocation.Recipe.Image != nil {
		image = *invocation.Recipe.Image
	}
	return remoteBuildPlatformConfig{
		Platform: invocation.Platform, PlatformKey: invocation.Platform,
		Commands:      append([]string(nil), invocation.Recipe.BuildCommands...),
		SetupCommands: append([]string(nil), invocation.Recipe.SetupCommands...),
		Output:        output, Image: image, AppID: invocation.AppID,
		Env:       cloneStringMapForBuild(invocation.Recipe.Env),
		Secrets:   append([]string(nil), invocation.Recipe.SecretRefs...),
		Caches:    append([]config.BuildCache(nil), invocation.Recipe.Caches...),
		Framework: invocation.Recipe.Framework, TimeoutSeconds: invocation.Recipe.TimeoutSeconds,
	}
}

func cloneStringMapForBuild(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
