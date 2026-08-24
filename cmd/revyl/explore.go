package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	startdevice "github.com/revyl/cli/internal/device"
	"github.com/revyl/cli/internal/execution"
	"github.com/revyl/cli/internal/launchvars"
)

const (
	defaultExplorePollInterval = 3 * time.Second
	defaultExploreTimeout      = 45 * time.Minute
)

type exploreAPI interface {
	GetApp(context.Context, string) (*api.App, error)
	SearchApps(context.Context, string, string, int) (*api.CLIPaginatedAppsResponse, error)
	ListOrgLaunchVariables(context.Context) (*api.OrgLaunchVariablesResponse, error)
	LaunchExploration(context.Context, string, *api.ExplorationLaunchRequest) (*api.ExplorationLaunchResponse, error)
	GetExploration(context.Context, string) (*api.ExplorationRunResponse, error)
	GetExplorationReport(context.Context, string) (*api.ExplorationRunReportResponse, error)
	CancelExploration(context.Context, string) (*api.ExplorationCancelResponse, error)
}

var (
	exploreNewClient = func(apiKey string, devMode bool) exploreAPI {
		return api.NewClientWithDevMode(apiKey, devMode)
	}
	explorePollInterval = defaultExplorePollInterval
	exploreSignals      = func() (<-chan os.Signal, func()) {
		ch := make(chan os.Signal, 2)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		return ch, func() { signal.Stop(ch) }
	}
)

var exploreCmd = &cobra.Command{
	Use:   "explore",
	Short: "Explore an app and build its Atlas map",
}

var exploreRunCmd = &cobra.Command{
	Use:   "run [app-name|app-id]",
	Short: "Start an app exploration",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runExploreRun,
}

var exploreStatusCmd = &cobra.Command{
	Use:   "status <run-id>",
	Short: "Show exploration progress",
	Args:  cobra.ExactArgs(1),
	RunE:  runExploreStatus,
}

var exploreCancelCmd = &cobra.Command{
	Use:   "cancel <run-id>",
	Short: "Cancel an exploration",
	Args:  cobra.ExactArgs(1),
	RunE:  runExploreCancel,
}

var (
	exploreBuildID               string
	explorePlatform              string
	exploreExplorerCount         int
	exploreStrategy              string
	exploreInstructions          string
	exploreAuthInstructions      string
	exploreLaunchVars            []string
	exploreLaunchEnv             []string
	exploreNoInheritedLaunchVars bool
	exploreDeviceModel           string
	exploreOSVersion             string
	exploreMaxDuration           time.Duration
	exploreIdleTimeout           time.Duration
	exploreTimeout               time.Duration
	exploreNoWait                bool
	exploreOpen                  bool
)

func init() {
	exploreCmd.AddCommand(exploreRunCmd, exploreStatusCmd, exploreCancelCmd)
	configureExploreRunFlags(exploreRunCmd)

	for _, flag := range []string{
		"build-id", "platform", "explorers", "strategy", "instructions",
		"auth-instructions", "launch-var", "launch-env", "no-inherited-launch-vars",
		"device-model", "os-version", "max-duration", "idle-timeout", "timeout",
		"no-wait", "open",
	} {
		analytics.MarkFlagValue(exploreRunCmd, flag)
	}
}

func configureExploreRunFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&exploreBuildID, "build-id", "", "Explore a specific build ID (default: latest)")
	cmd.Flags().StringVar(&explorePlatform, "platform", "", "Configured platform mapping: ios or android")
	cmd.Flags().IntVar(&exploreExplorerCount, "explorers", 3, "Number of parallel explorers")
	cmd.Flags().StringVar(&exploreStrategy, "strategy", "balanced", "Exploration strategy: balanced, surface-sweep, journey-focus, or hard-edges")
	cmd.Flags().StringVar(&exploreInstructions, "instructions", "", "Instructions shared by every explorer")
	cmd.Flags().StringVar(&exploreAuthInstructions, "auth-instructions", "", "Authentication guidance shared by every explorer")
	cmd.Flags().StringArrayVar(&exploreLaunchVars, "launch-var", nil, "Stored launch variable key or ID (repeatable)")
	cmd.Flags().StringArrayVar(&exploreLaunchEnv, "launch-env", nil, "Inline launch variable KEY=VALUE (repeatable; avoid secrets)")
	cmd.Flags().BoolVar(&exploreNoInheritedLaunchVars, "no-inherited-launch-vars", false, "Ignore inherited launch variables")
	cmd.Flags().StringVar(&exploreDeviceModel, "device-model", "", "Device model target")
	cmd.Flags().StringVar(&exploreOSVersion, "os-version", "", "OS version target")
	cmd.Flags().DurationVar(&exploreMaxDuration, "max-duration", 30*time.Minute, "Maximum exploration duration")
	cmd.Flags().DurationVar(&exploreIdleTimeout, "idle-timeout", 15*time.Minute, "Stop an idle explorer after this duration")
	cmd.Flags().DurationVar(&exploreTimeout, "timeout", defaultExploreTimeout, "Maximum time to wait locally")
	cmd.Flags().BoolVar(&exploreNoWait, "no-wait", false, "Return after the exploration is queued")
	cmd.Flags().BoolVar(&exploreOpen, "open", false, "Open the report in a browser")
}

type exploreOutput struct {
	RunID              string `json:"run_id"`
	AppID              string `json:"app_id"`
	BuildID            string `json:"build_id"`
	ExecutionStatus    string `json:"execution_status"`
	AtlasStatus        string `json:"atlas_status"`
	CustomerStatus     string `json:"customer_status"`
	Outcome            string `json:"outcome"`
	ReportURL          string `json:"report_url"`
	ExplorersRequested int    `json:"explorers_requested"`
	ExplorersLaunched  int    `json:"explorers_launched"`
	CompletedExplorers int    `json:"completed_explorers"`
	TotalExplorers     int    `json:"total_explorers"`
	FindingsCount      int    `json:"findings_count"`
	Success            bool   `json:"success"`
}

type exploreProgress struct {
	ExecutionStatus string
	AtlasStatus     string
	Completed       int
	Total           int
}

func runExploreRun(cmd *cobra.Command, args []string) error {
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	quiet, _ := cmd.Flags().GetBool("quiet")
	client := exploreNewClient(apiKey, devMode)

	request, app, err := buildExploreLaunchRequest(cmd.Context(), client, args)
	if err != nil {
		return err
	}

	if !jsonOutput && !quiet {
		fmt.Fprintf(cmd.ErrOrStderr(), "Starting exploration of %s with %d explorers...\n", app.Name, exploreExplorerCount)
	}
	launch, err := client.LaunchExploration(cmd.Context(), app.ID, request)
	if err != nil {
		return fmt.Errorf("launch exploration: %w", err)
	}

	output := exploreOutputFromRun(
		launch.Run,
		nil,
		launch.ReportUrl,
		exploreExplorerCount,
	)
	if !quiet && exploreConcurrencyWasTrimmed(output) {
		fmt.Fprintf(
			cmd.ErrOrStderr(),
			"Warning: available concurrency trimmed explorers from %d to %d.\n",
			output.ExplorersRequested,
			output.ExplorersLaunched,
		)
	}

	if exploreOpen && strings.TrimSpace(launch.ReportUrl) != "" {
		if err := runOpenBrowserFn(launch.ReportUrl); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not open report: %v\n", err)
		}
	}

	if exploreNoWait {
		output.Outcome = "queued"
		return writeExploreOutput(cmd, output, jsonOutput)
	}

	monitorCtx, cancel := context.WithTimeout(cmd.Context(), exploreTimeout)
	defer cancel()

	report, monitorErr := monitorExploration(
		monitorCtx,
		client,
		launch.Run.Id,
		cmd.ErrOrStderr(),
		!quiet,
	)
	if report != nil {
		output = exploreOutputFromRun(
			report.Run,
			report,
			launch.ReportUrl,
			exploreExplorerCount,
		)
	}
	if monitorErr != nil {
		if monitorCtx.Err() == context.DeadlineExceeded {
			output.Outcome = "timeout"
			output.Success = false
		}
		_ = writeExploreOutput(cmd, output, jsonOutput)
		return completedExploreError(output, monitorErr)
	}

	if err := writeExploreOutput(cmd, output, jsonOutput); err != nil {
		return err
	}
	if !output.Success {
		return completedExploreError(output, fmt.Errorf("exploration ended with outcome %s", output.Outcome))
	}
	return nil
}

func runExploreStatus(cmd *cobra.Command, args []string) error {
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}
	if _, err := uuid.Parse(args[0]); err != nil {
		return fmt.Errorf("invalid run ID %q", args[0])
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	client := exploreNewClient(apiKey, devMode)
	report, err := client.GetExplorationReport(cmd.Context(), args[0])
	if err != nil {
		return fmt.Errorf("get exploration status: %w", err)
	}
	output := exploreOutputFromRun(
		report.Run,
		report,
		exploreReportURL(devMode, report.Run.Id),
		exploreConfiguredLaneCount(report.Run),
	)
	if err := writeExploreOutput(cmd, output, jsonOutput); err != nil {
		return err
	}
	if isExploreTerminal(report.Run) && !output.Success {
		return completedExploreError(output, fmt.Errorf("exploration ended with outcome %s", output.Outcome))
	}
	return nil
}

func runExploreCancel(cmd *cobra.Command, args []string) error {
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}
	if _, err := uuid.Parse(args[0]); err != nil {
		return fmt.Errorf("invalid run ID %q", args[0])
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	client := exploreNewClient(apiKey, devMode)
	result, err := client.CancelExploration(cmd.Context(), args[0])
	if err != nil {
		return fmt.Errorf("cancel exploration: %w", err)
	}
	output := exploreCancelOutput(result, devMode)
	return writeExploreOutput(cmd, output, jsonOutput)
}

func exploreCancelOutput(result *api.ExplorationCancelResponse, devMode bool) exploreOutput {
	return exploreOutput{
		RunID:           result.RunId,
		ExecutionStatus: result.ExecutionStatus,
		Outcome:         strings.ToLower(strings.TrimSpace(result.ExecutionStatus)),
		ReportURL:       exploreReportURL(devMode, result.RunId),
	}
}

func buildExploreLaunchRequest(
	ctx context.Context,
	client exploreAPI,
	args []string,
) (*api.ExplorationLaunchRequest, *api.App, error) {
	platform, err := normalizeExplorePlatform(explorePlatform)
	if err != nil {
		return nil, nil, err
	}
	strategy, err := normalizeExploreStrategy(exploreStrategy)
	if err != nil {
		return nil, nil, err
	}
	if exploreExplorerCount < 1 || exploreExplorerCount > 100 {
		return nil, nil, fmt.Errorf("--explorers must be between 1 and 100")
	}
	if err := validateExploreDuration("--max-duration", exploreMaxDuration, time.Minute, 2*time.Hour); err != nil {
		return nil, nil, err
	}
	if err := validateExploreDuration("--idle-timeout", exploreIdleTimeout, time.Minute, 2*time.Hour); err != nil {
		return nil, nil, err
	}
	if exploreTimeout <= 0 {
		return nil, nil, fmt.Errorf("--timeout must be greater than zero")
	}
	if len(exploreInstructions) > 4000 {
		return nil, nil, fmt.Errorf("--instructions cannot exceed 4000 characters")
	}
	if len(exploreAuthInstructions) > 4000 {
		return nil, nil, fmt.Errorf("--auth-instructions cannot exceed 4000 characters")
	}

	app, err := resolveExploreApp(ctx, client, args, platform)
	if err != nil {
		return nil, nil, err
	}
	appPlatform, err := normalizeExplorePlatform(app.Platform)
	if err != nil {
		return nil, nil, fmt.Errorf("app %s has unsupported platform %q", app.ID, app.Platform)
	}
	if platform != "" && platform != appPlatform {
		return nil, nil, fmt.Errorf("--platform %s does not match app platform %s", platform, appPlatform)
	}
	platform = appPlatform

	inlineEnv, err := parseLaunchEnvVars(exploreLaunchEnv)
	if err != nil {
		return nil, nil, err
	}
	inheritedIDs, err := launchvars.LoadInheritedIDs(exploreNoInheritedLaunchVars)
	if err != nil {
		return nil, nil, fmt.Errorf("load inherited launch variables: %w", err)
	}
	launchIDs, err := startdevice.ResolveLaunchConfigurationSelection(
		ctx,
		client,
		inheritedIDs,
		exploreLaunchVars,
		nil,
	)
	if err != nil {
		return nil, nil, err
	}
	typedLaunchIDs := make([]uuid.UUID, 0, len(launchIDs))
	for _, id := range launchIDs {
		parsed, parseErr := uuid.Parse(id)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("invalid launch variable ID %q: %w", id, parseErr)
		}
		typedLaunchIDs = append(typedLaunchIDs, parsed)
	}

	request := &api.ExplorationLaunchRequest{
		Platform:           stringPointer(platform),
		LaneCount:          intPointer(exploreExplorerCount),
		SwarmStrategy:      stringPointer(strategy),
		MaxDurationSeconds: intPointer(int(exploreMaxDuration.Seconds())),
		IdleTimeoutSeconds: intPointer(int(exploreIdleTimeout.Seconds())),
	}
	if value := strings.TrimSpace(exploreInstructions); value != "" {
		request.SeedInstructions = &value
	}
	if value := strings.TrimSpace(exploreAuthInstructions); value != "" {
		request.AuthInstructions = &value
	}
	if value := strings.TrimSpace(exploreDeviceModel); value != "" {
		request.DeviceModel = &value
	}
	if value := strings.TrimSpace(exploreOSVersion); value != "" {
		request.OsVersion = &value
	}
	if len(typedLaunchIDs) > 0 {
		request.LaunchEnvVarIds = &typedLaunchIDs
	}
	if len(inlineEnv) > 0 {
		request.EnvVars = &inlineEnv
	}
	if value := strings.TrimSpace(exploreBuildID); value != "" {
		parsed, parseErr := uuid.Parse(value)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("invalid --build-id %q", value)
		}
		request.BuildId = &parsed
	}
	return request, app, nil
}

func resolveExploreApp(
	ctx context.Context,
	client exploreAPI,
	args []string,
	platform string,
) (*api.App, error) {
	if len(args) == 1 {
		app, err := resolveExploreAppNameOrID(ctx, client, args[0])
		if err != nil {
			return nil, err
		}
		return app, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}
	project, err := config.ResolveProjectContext(cwd, "")
	if err != nil {
		return nil, actionableLocalConfigError(err)
	}
	return resolveCanonicalConfiguredExploreApp(ctx, client, project, platform)
}

func resolveCanonicalConfiguredExploreApp(
	ctx context.Context,
	client exploreAPI,
	project *config.ProjectContext,
	platform string,
) (*api.App, error) {
	appID := ""
	if platform != "" {
		var err error
		appID, err = execution.ResolveCanonicalConfiguredAppID(project, platform)
		if err != nil {
			return nil, fmt.Errorf("%w; pass --platform %s with an explicit app name or ID", err, platform)
		}
		if appID == "" {
			return nil, fmt.Errorf("no configured app for platform %s; pass an app name or ID", platform)
		}
	} else {
		configured := make(map[string]struct{})
		for _, profile := range project.Aggregate.Profiles {
			for _, recipe := range profile.Configurations {
				if recipe.AppID != nil && strings.TrimSpace(*recipe.AppID) != "" {
					configured[strings.TrimSpace(*recipe.AppID)] = struct{}{}
				}
			}
		}
		if len(configured) == 0 {
			return nil, fmt.Errorf("no configured app found; pass an app name or ID")
		}
		if len(configured) > 1 {
			return nil, fmt.Errorf("multiple configured apps found; pass --platform ios|android or an app name or ID")
		}
		for configuredAppID := range configured {
			appID = configuredAppID
		}
	}

	app, err := client.GetApp(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("resolve configured app %s: %w", appID, err)
	}
	return app, nil
}

func resolveConfiguredExploreApp(
	ctx context.Context,
	client exploreAPI,
	cfg *config.ProjectConfig,
	platform string,
) (*api.App, error) {
	appID := ""
	if platform != "" {
		appID = execution.ResolveConfiguredAppID(cfg, platform)
		if appID == "" {
			return nil, fmt.Errorf("no configured app for platform %s; pass an app name or ID", platform)
		}
	} else {
		configured := make(map[string]struct{})
		for _, platformConfig := range cfg.Build.Platforms {
			if id := strings.TrimSpace(platformConfig.AppID); id != "" {
				configured[id] = struct{}{}
			}
		}
		if len(configured) == 0 {
			return nil, fmt.Errorf("no configured app found; pass an app name or ID")
		}
		if len(configured) > 1 {
			return nil, fmt.Errorf("multiple configured apps found; pass --platform ios|android or an app name or ID")
		}
		for id := range configured {
			appID = id
		}
	}

	app, err := client.GetApp(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("resolve configured app %s: %w", appID, err)
	}
	return app, nil
}

func resolveExploreAppNameOrID(ctx context.Context, client exploreAPI, nameOrID string) (*api.App, error) {
	needle := strings.TrimSpace(nameOrID)
	if needle == "" {
		return nil, fmt.Errorf("app name or ID cannot be empty")
	}
	if _, err := uuid.Parse(needle); err == nil {
		app, getErr := client.GetApp(ctx, needle)
		if getErr != nil {
			return nil, fmt.Errorf("resolve app %s: %w", needle, getErr)
		}
		return app, nil
	}

	result, err := client.SearchApps(ctx, needle, "", 20)
	if err != nil {
		return nil, fmt.Errorf("search apps: %w", err)
	}
	match, err := selectExactNameApp(result.Items, needle)
	if err != nil {
		return nil, err
	}
	return &match, nil
}

func monitorExploration(
	ctx context.Context,
	client exploreAPI,
	runID string,
	progressWriter io.Writer,
	showProgress bool,
) (*api.ExplorationRunReportResponse, error) {
	ticker := time.NewTicker(explorePollInterval)
	defer ticker.Stop()

	sigCh, stopSignals := exploreSignals()
	defer stopSignals()

	var last exploreProgress
	first := true
	for {
		report, err := client.GetExplorationReport(ctx, runID)
		if err != nil {
			return nil, fmt.Errorf("monitor exploration: %w", err)
		}
		current := exploreProgressFromReport(report)
		if showProgress && (first || current != last) {
			fmt.Fprintf(
				progressWriter,
				"Explore %s · Atlas %s · explorers %d/%d complete\n",
				current.ExecutionStatus,
				current.AtlasStatus,
				current.Completed,
				current.Total,
			)
		}
		last = current
		first = false
		if isExploreTerminal(report.Run) {
			return report, nil
		}

		select {
		case <-ctx.Done():
			return report, fmt.Errorf("stopped waiting for exploration %s: %w", runID, ctx.Err())
		case <-ticker.C:
			continue
		case <-sigCh:
			fmt.Fprintln(progressWriter, "Cancelling exploration... Press Ctrl-C again to stop waiting.")
			cancelResult := make(chan error, 1)
			go func() {
				cancelCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				_, cancelErr := client.CancelExploration(cancelCtx, runID)
				cancelResult <- cancelErr
			}()
			select {
			case cancelErr := <-cancelResult:
				if cancelErr != nil {
					return report, fmt.Errorf("cancel exploration: %w", cancelErr)
				}
				refreshCtx, refreshCancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer refreshCancel()
				return client.GetExplorationReport(refreshCtx, runID)
			case <-sigCh:
				return report, fmt.Errorf("forced stop while cancellation was in progress")
			case <-ctx.Done():
				return report, fmt.Errorf("stopped waiting for cancellation: %w", ctx.Err())
			}
		}
	}
}

func exploreProgressFromReport(report *api.ExplorationRunReportResponse) exploreProgress {
	return exploreProgress{
		ExecutionStatus: report.Run.ExecutionStatus,
		AtlasStatus:     report.Run.AtlasStatus,
		Completed:       pointerInt(report.CompletedChildren),
		Total:           pointerInt(report.TotalChildren),
	}
}

func exploreOutputFromRun(
	run api.ExplorationRunResponse,
	report *api.ExplorationRunReportResponse,
	reportURL string,
	requested int,
) exploreOutput {
	configured := exploreConfiguredLaneCount(run)
	total := configured
	completed := terminalExploreSessionCount(run)
	if report != nil {
		if value := pointerInt(report.TotalChildren); value > 0 {
			total = value
		}
		completed = pointerInt(report.CompletedChildren)
	}
	buildID := ""
	if run.BuildId != nil {
		buildID = *run.BuildId
	}
	findingsCount := 0
	if run.Findings != nil {
		findingsCount = len(*run.Findings)
	}
	outcome, success := exploreOutcome(run)
	return exploreOutput{
		RunID:              run.Id,
		AppID:              run.AppId,
		BuildID:            buildID,
		ExecutionStatus:    run.ExecutionStatus,
		AtlasStatus:        run.AtlasStatus,
		CustomerStatus:     run.CustomerStatus,
		Outcome:            outcome,
		ReportURL:          reportURL,
		ExplorersRequested: requested,
		ExplorersLaunched:  configured,
		CompletedExplorers: completed,
		TotalExplorers:     total,
		FindingsCount:      findingsCount,
		Success:            success,
	}
}

func exploreOutcome(run api.ExplorationRunResponse) (string, bool) {
	executionStatus := strings.ToLower(strings.TrimSpace(run.ExecutionStatus))
	atlasStatus := strings.ToLower(strings.TrimSpace(run.AtlasStatus))
	mapReady := strings.EqualFold(strings.TrimSpace(run.CustomerStatus), "ready")

	if executionStatus == "cancelled" {
		return "cancelled", false
	}
	if !isExploreTerminal(run) {
		return executionStatus, false
	}
	if mapReady {
		if atlasStatus == "partial" {
			return "partial", true
		}
		if hasBlockedSetupFinding(run) {
			return "blocked", true
		}
		return "completed", true
	}
	if executionStatus == "failed" {
		return "failed", false
	}
	if executionStatus == "completed" && (atlasStatus == "failed" || atlasStatus == "partial") {
		return "no-map", false
	}
	return executionStatus, false
}

func exploreConcurrencyWasTrimmed(output exploreOutput) bool {
	return output.ExplorersLaunched > 0 && output.ExplorersLaunched < output.ExplorersRequested
}

func hasBlockedSetupFinding(run api.ExplorationRunResponse) bool {
	if run.Findings == nil {
		return false
	}
	for _, finding := range *run.Findings {
		kind, _ := finding["kind"].(string)
		if strings.EqualFold(strings.TrimSpace(kind), "blocked_setup") {
			return true
		}
	}
	return false
}

func isExploreTerminal(run api.ExplorationRunResponse) bool {
	executionStatus := strings.ToLower(strings.TrimSpace(run.ExecutionStatus))
	if executionStatus == "cancelled" {
		return true
	}
	if executionStatus != "completed" && executionStatus != "failed" {
		return false
	}
	atlasStatus := strings.ToLower(strings.TrimSpace(run.AtlasStatus))
	if executionStatus == "failed" && atlasStatus == "not_started" {
		return true
	}
	switch atlasStatus {
	case "completed", "partial", "failed":
		return true
	default:
		return false
	}
}

func exploreConfiguredLaneCount(run api.ExplorationRunResponse) int {
	if run.Config == nil {
		return len(pointerSessions(run.Sessions))
	}
	value, ok := (*run.Config)["lane_count"]
	if !ok {
		return len(pointerSessions(run.Sessions))
	}
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return len(pointerSessions(run.Sessions))
	}
}

func terminalExploreSessionCount(run api.ExplorationRunResponse) int {
	count := 0
	for _, session := range pointerSessions(run.Sessions) {
		if session.SessionStatus == nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(*session.SessionStatus)) {
		case "completed", "failed", "cancelled", "timeout":
			count++
		}
	}
	return count
}

func pointerSessions(value *[]api.ExplorationSessionResponse) []api.ExplorationSessionResponse {
	if value == nil {
		return nil
	}
	return *value
}

func writeExploreOutput(cmd *cobra.Command, output exploreOutput, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetEscapeHTML(false)
		return encoder.Encode(output)
	}

	statusLine := fmt.Sprintf("Exploration %s", output.Outcome)
	if output.Success {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: usable Atlas map ready.\n", statusLine)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "%s.\n", statusLine)
	}
	if output.RunID != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Run: %s\n", output.RunID)
	}
	if output.TotalExplorers > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Explorers: %d/%d complete\n", output.CompletedExplorers, output.TotalExplorers)
	}
	if output.FindingsCount > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Findings: %d\n", output.FindingsCount)
	}
	if output.ReportURL != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Report: %s\n", output.ReportURL)
	}
	if output.Outcome == "partial" {
		fmt.Fprintln(cmd.ErrOrStderr(), "Warning: Explore produced a usable partial map; some explorers did not finish mapping.")
	}
	if output.Outcome == "blocked" {
		fmt.Fprintln(cmd.ErrOrStderr(), "Warning: Explore produced a usable map and reported a setup blocker.")
	}
	return nil
}

func completedExploreError(output exploreOutput, err error) error {
	return analytics.CompletedWithExitCode(err, analytics.CommandCompletion{
		ExitCode:     1,
		Domain:       "exploration",
		DomainStatus: output.Outcome,
		Properties: map[string]interface{}{
			"exploration_run_id":           output.RunID,
			"exploration_execution_status": output.ExecutionStatus,
			"exploration_atlas_status":     output.AtlasStatus,
			"exploration_customer_status":  output.CustomerStatus,
			"exploration_success":          output.Success,
		},
	})
}

func exploreReportURL(devMode bool, runID string) string {
	return fmt.Sprintf(
		"%s/explorations/report?runId=%s",
		strings.TrimRight(config.GetAppURL(devMode), "/"),
		runID,
	)
}

func normalizeExplorePlatform(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "ios" || value == "android" {
		return value, nil
	}
	return "", fmt.Errorf("invalid --platform %q: expected ios or android", value)
}

func normalizeExploreStrategy(value string) (string, error) {
	normalized := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "-", "_")
	valid := []string{"balanced", "hard_edges", "journey_focus", "surface_sweep"}
	for _, candidate := range valid {
		if normalized == candidate {
			return normalized, nil
		}
	}
	return "", fmt.Errorf("invalid --strategy %q: expected balanced, surface-sweep, journey-focus, or hard-edges", value)
}

func validateExploreDuration(name string, value, minimum, maximum time.Duration) error {
	if value < minimum || value > maximum {
		return fmt.Errorf("%s must be between %s and %s", name, minimum, maximum)
	}
	if value%time.Second != 0 {
		return fmt.Errorf("%s must use whole seconds", name)
	}
	return nil
}

func intPointer(value int) *int          { return &value }
func stringPointer(value string) *string { return &value }

func pointerInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
