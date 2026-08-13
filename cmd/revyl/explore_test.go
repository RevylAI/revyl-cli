package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

type fakeExploreAPI struct {
	apps          map[string]*api.App
	searchResults []api.App
	launchVars    []api.OrgLaunchVariable
	reports       []*api.ExplorationRunReportResponse
	reportErr     error
	reportCalls   int
	cancelled     bool
}

func (f *fakeExploreAPI) GetApp(_ context.Context, id string) (*api.App, error) {
	if app := f.apps[id]; app != nil {
		copy := *app
		return &copy, nil
	}
	return nil, errors.New("app not found")
}

func (f *fakeExploreAPI) SearchApps(context.Context, string, string, int) (*api.CLIPaginatedAppsResponse, error) {
	return &api.CLIPaginatedAppsResponse{Items: f.searchResults}, nil
}

func (f *fakeExploreAPI) ListOrgLaunchVariables(context.Context) (*api.OrgLaunchVariablesResponse, error) {
	return &api.OrgLaunchVariablesResponse{Result: f.launchVars}, nil
}

func (f *fakeExploreAPI) LaunchExploration(context.Context, string, *api.ExplorationLaunchRequest) (*api.ExplorationLaunchResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeExploreAPI) GetExploration(context.Context, string) (*api.ExplorationRunResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeExploreAPI) GetExplorationReport(context.Context, string) (*api.ExplorationRunReportResponse, error) {
	if f.reportErr != nil {
		return nil, f.reportErr
	}
	if len(f.reports) == 0 {
		return nil, errors.New("no reports")
	}
	index := f.reportCalls
	if index >= len(f.reports) {
		index = len(f.reports) - 1
	}
	f.reportCalls++
	return f.reports[index], nil
}

func (f *fakeExploreAPI) CancelExploration(context.Context, string) (*api.ExplorationCancelResponse, error) {
	f.cancelled = true
	return &api.ExplorationCancelResponse{ExecutionStatus: "cancelled"}, nil
}

func TestBuildExploreLaunchRequestResolvesExplicitAppAndOptions(t *testing.T) {
	resetExploreFlagsForTest(t)
	appID := uuid.NewString()
	buildID := uuid.NewString()
	launchVarID := uuid.NewString()
	hasValue := true
	client := &fakeExploreAPI{
		searchResults: []api.App{{ID: appID, Name: "My App", Platform: "ios"}},
		launchVars: []api.OrgLaunchVariable{{
			ID: launchVarID, Key: "AUTH_TOKEN", Kind: "key_value", HasValue: &hasValue,
		}},
	}
	exploreBuildID = buildID
	explorePlatform = "ios"
	exploreExplorerCount = 5
	exploreStrategy = "surface-sweep"
	exploreInstructions = "Map checkout"
	exploreAuthInstructions = "Use the test account"
	exploreLaunchVars = []string{"AUTH_TOKEN"}
	exploreLaunchEnv = []string{"API_HOST=staging"}
	exploreDeviceModel = "iPhone 16"
	exploreOSVersion = "18.5"

	request, app, err := buildExploreLaunchRequest(context.Background(), client, []string{"My App"})
	if err != nil {
		t.Fatalf("buildExploreLaunchRequest(): %v", err)
	}
	if app.ID != appID {
		t.Fatalf("app id = %s", app.ID)
	}
	if request.LaneCount == nil || *request.LaneCount != 5 {
		t.Fatalf("lane count = %v", request.LaneCount)
	}
	if request.SwarmStrategy == nil || *request.SwarmStrategy != "surface_sweep" {
		t.Fatalf("strategy = %v", request.SwarmStrategy)
	}
	if request.BuildId == nil || request.BuildId.String() != buildID {
		t.Fatalf("build id = %v", request.BuildId)
	}
	if request.LaunchEnvVarIds == nil || len(*request.LaunchEnvVarIds) != 1 || (*request.LaunchEnvVarIds)[0].String() != launchVarID {
		t.Fatalf("launch var ids = %#v", request.LaunchEnvVarIds)
	}
	if request.EnvVars == nil || (*request.EnvVars)["API_HOST"] != "staging" {
		t.Fatalf("env vars = %#v", request.EnvVars)
	}
}

func TestResolveExploreAppUsesConfiguredPlatformMapping(t *testing.T) {
	resetExploreFlagsForTest(t)
	appID := uuid.NewString()
	cfg := &config.ProjectConfig{Build: config.BuildConfig{Platforms: map[string]config.BuildPlatform{
		"ios": {AppID: appID},
	}}}

	client := &fakeExploreAPI{apps: map[string]*api.App{
		appID: {ID: appID, Name: "Configured", Platform: "ios"},
	}}
	app, err := resolveConfiguredExploreApp(context.Background(), client, cfg, "ios")
	if err != nil {
		t.Fatalf("resolveConfiguredExploreApp(): %v", err)
	}
	if app.ID != appID {
		t.Fatalf("app id = %s, want %s", app.ID, appID)
	}
}

func TestExploreValidation(t *testing.T) {
	if _, err := normalizeExploreStrategy("wide-open"); err == nil {
		t.Fatal("normalizeExploreStrategy() error = nil")
	}
	if _, err := normalizeExplorePlatform("windows"); err == nil {
		t.Fatal("normalizeExplorePlatform() error = nil")
	}
	if err := validateExploreDuration("--max-duration", 59*time.Second, time.Minute, 2*time.Hour); err == nil {
		t.Fatal("validateExploreDuration() error = nil")
	}
}

func TestExploreLaunchFlagsPreserveCommas(t *testing.T) {
	resetExploreFlagsForTest(t)
	cmd := &cobra.Command{}
	configureExploreRunFlags(cmd)

	if err := cmd.ParseFlags([]string{
		"--launch-env", "ALLOWED_HOSTS=a.com,b.com",
		"--launch-var", "KEY,WITH,COMMAS",
	}); err != nil {
		t.Fatalf("ParseFlags(): %v", err)
	}
	if len(exploreLaunchEnv) != 1 || exploreLaunchEnv[0] != "ALLOWED_HOSTS=a.com,b.com" {
		t.Fatalf("launch env = %#v", exploreLaunchEnv)
	}
	if len(exploreLaunchVars) != 1 || exploreLaunchVars[0] != "KEY,WITH,COMMAS" {
		t.Fatalf("launch vars = %#v", exploreLaunchVars)
	}
}

func TestExploreOutcomeAndExitSemantics(t *testing.T) {
	cases := []struct {
		name    string
		run     api.ExplorationRunResponse
		outcome string
		success bool
	}{
		{name: "completed", run: exploreRun("completed", "completed", "ready", nil), outcome: "completed", success: true},
		{name: "partial", run: exploreRun("completed", "partial", "ready", nil), outcome: "partial", success: true},
		{name: "blocked", run: exploreRun("completed", "completed", "ready", []map[string]interface{}{{"kind": "blocked_setup"}}), outcome: "blocked", success: true},
		{name: "failed", run: exploreRun("failed", "failed", "not_ready", nil), outcome: "failed", success: false},
		{name: "cancelled", run: exploreRun("cancelled", "failed", "not_ready", nil), outcome: "cancelled", success: false},
		{name: "no map", run: exploreRun("completed", "failed", "not_ready", nil), outcome: "no-map", success: false},
		{name: "map ready while running", run: exploreRun("running", "processing", "ready", nil), outcome: "running", success: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outcome, success := exploreOutcome(tc.run)
			if outcome != tc.outcome || success != tc.success {
				t.Fatalf("exploreOutcome() = (%q, %v), want (%q, %v)", outcome, success, tc.outcome, tc.success)
			}
		})
	}

	err := completedExploreError(exploreOutput{RunID: "run-1", Outcome: "no-map"}, errors.New("no map"))
	var completed *analytics.CompletedError
	if !errors.As(err, &completed) || completed.Completion().ExitCode != 1 {
		t.Fatalf("completed error = %#v", err)
	}
}

func TestExploreCancelOutputUsesReturnedExecutionStatus(t *testing.T) {
	result := &api.ExplorationCancelResponse{
		RunId:           "run-1",
		ExecutionStatus: "completed",
	}
	output := exploreCancelOutput(result, false)
	if output.Outcome != "completed" || output.ExecutionStatus != "completed" {
		t.Fatalf("cancel output = %#v", output)
	}
}

func TestFailedExplorationWaitsForActiveAtlasWork(t *testing.T) {
	processing := exploreRun("failed", "processing", "processing", nil)
	if isExploreTerminal(processing) {
		t.Fatal("failed exploration with active Atlas work was terminal")
	}
	failedBeforeLanes := exploreRun("failed", "not_started", "not_ready", nil)
	if !isExploreTerminal(failedBeforeLanes) {
		t.Fatal("failed exploration with no Atlas work was not terminal")
	}
}

func TestExploreConcurrencyAndStableJSON(t *testing.T) {
	configMap := map[string]interface{}{"lane_count": float64(2)}
	run := exploreRun("running", "processing", "processing", nil)
	run.Config = &configMap
	output := exploreOutputFromRun(run, nil, "https://app.example/report", 5)
	if output.ExplorersRequested != 5 || output.ExplorersLaunched != 2 {
		t.Fatalf("concurrency = requested %d, launched %d", output.ExplorersRequested, output.ExplorersLaunched)
	}

	cmd := &cobra.Command{}
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	if err := writeExploreOutput(cmd, output, true); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("JSON output: %v", err)
	}
	for _, key := range []string{
		"run_id", "app_id", "build_id", "execution_status", "atlas_status",
		"customer_status", "outcome", "report_url", "explorers_requested",
		"explorers_launched", "completed_explorers", "total_explorers",
		"findings_count", "success",
	} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("JSON output missing %q", key)
		}
	}
}

func TestExploreConcurrencyWarningRequiresKnownTrimmedCount(t *testing.T) {
	cases := []struct {
		name      string
		requested int
		launched  int
		want      bool
	}{
		{name: "unknown launch count", requested: 3, launched: 0, want: false},
		{name: "full launch count", requested: 3, launched: 3, want: false},
		{name: "trimmed launch count", requested: 3, launched: 2, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output := exploreOutput{
				ExplorersRequested: tc.requested,
				ExplorersLaunched:  tc.launched,
			}
			if got := exploreConcurrencyWasTrimmed(output); got != tc.want {
				t.Fatalf("exploreConcurrencyWasTrimmed() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMonitorExplorationReportsProgressAndCompletes(t *testing.T) {
	oldInterval := explorePollInterval
	explorePollInterval = time.Millisecond
	t.Cleanup(func() { explorePollInterval = oldInterval })

	client := &fakeExploreAPI{reports: []*api.ExplorationRunReportResponse{
		exploreReport("running", "processing", "processing", 0, 2),
		exploreReport("completed", "partial", "ready", 2, 2),
	}}
	var stderr bytes.Buffer
	report, err := monitorExploration(context.Background(), client, "run-1", &stderr, true)
	if err != nil {
		t.Fatalf("monitorExploration(): %v", err)
	}
	if report.Run.AtlasStatus != "partial" {
		t.Fatalf("atlas status = %s", report.Run.AtlasStatus)
	}
	if !strings.Contains(stderr.String(), "explorers 0/2 complete") || !strings.Contains(stderr.String(), "explorers 2/2 complete") {
		t.Fatalf("progress output = %q", stderr.String())
	}
}

func TestMonitorExplorationTimesOutWithoutCancelling(t *testing.T) {
	oldInterval := explorePollInterval
	explorePollInterval = time.Millisecond
	t.Cleanup(func() { explorePollInterval = oldInterval })

	client := &fakeExploreAPI{reports: []*api.ExplorationRunReportResponse{
		exploreReport("running", "processing", "processing", 0, 2),
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := monitorExploration(ctx, client, "run-1", &bytes.Buffer{}, true)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("monitorExploration() error = %v", err)
	}
	if client.cancelled {
		t.Fatal("local timeout cancelled the remote exploration")
	}
}

func TestMonitorExplorationCancelsOnInterrupt(t *testing.T) {
	oldInterval := explorePollInterval
	oldSignals := exploreSignals
	explorePollInterval = time.Hour
	signals := make(chan os.Signal, 2)
	exploreSignals = func() (<-chan os.Signal, func()) { return signals, func() {} }
	t.Cleanup(func() {
		explorePollInterval = oldInterval
		exploreSignals = oldSignals
	})

	client := &fakeExploreAPI{reports: []*api.ExplorationRunReportResponse{
		exploreReport("running", "processing", "processing", 0, 2),
		exploreReport("cancelled", "failed", "not_ready", 2, 2),
	}}
	signals <- os.Interrupt
	var stderr bytes.Buffer
	report, err := monitorExploration(context.Background(), client, "run-1", &stderr, true)
	if err != nil {
		t.Fatalf("monitorExploration(): %v", err)
	}
	if !client.cancelled || report.Run.ExecutionStatus != "cancelled" {
		t.Fatalf("cancelled = %v, status = %s", client.cancelled, report.Run.ExecutionStatus)
	}
	if !strings.Contains(stderr.String(), "Cancelling exploration") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func exploreRun(executionStatus, atlasStatus, customerStatus string, findings []map[string]interface{}) api.ExplorationRunResponse {
	return api.ExplorationRunResponse{
		Id:              "run-1",
		AppId:           "app-1",
		Mode:            "explore",
		ExecutionStatus: executionStatus,
		AtlasStatus:     atlasStatus,
		CustomerStatus:  customerStatus,
		Findings:        &findings,
	}
}

func exploreReport(executionStatus, atlasStatus, customerStatus string, completed, total int) *api.ExplorationRunReportResponse {
	return &api.ExplorationRunReportResponse{
		Run:               exploreRun(executionStatus, atlasStatus, customerStatus, nil),
		CompletedChildren: &completed,
		TotalChildren:     &total,
	}
}

func resetExploreFlagsForTest(t *testing.T) {
	t.Helper()
	old := struct {
		buildID, platform, strategy, instructions, authInstructions string
		explorerCount                                               int
		launchVars, launchEnv                                       []string
		noInherited                                                 bool
		deviceModel, osVersion                                      string
		maxDuration, idleTimeout, timeout                           time.Duration
		noWait, open                                                bool
	}{
		exploreBuildID, explorePlatform, exploreStrategy, exploreInstructions,
		exploreAuthInstructions, exploreExplorerCount,
		append([]string(nil), exploreLaunchVars...), append([]string(nil), exploreLaunchEnv...),
		exploreNoInheritedLaunchVars, exploreDeviceModel, exploreOSVersion,
		exploreMaxDuration, exploreIdleTimeout, exploreTimeout, exploreNoWait, exploreOpen,
	}
	t.Cleanup(func() {
		exploreBuildID = old.buildID
		explorePlatform = old.platform
		exploreStrategy = old.strategy
		exploreInstructions = old.instructions
		exploreAuthInstructions = old.authInstructions
		exploreExplorerCount = old.explorerCount
		exploreLaunchVars = old.launchVars
		exploreLaunchEnv = old.launchEnv
		exploreNoInheritedLaunchVars = old.noInherited
		exploreDeviceModel = old.deviceModel
		exploreOSVersion = old.osVersion
		exploreMaxDuration = old.maxDuration
		exploreIdleTimeout = old.idleTimeout
		exploreTimeout = old.timeout
		exploreNoWait = old.noWait
		exploreOpen = old.open
	})
	exploreBuildID = ""
	explorePlatform = ""
	exploreExplorerCount = 3
	exploreStrategy = "balanced"
	exploreInstructions = ""
	exploreAuthInstructions = ""
	exploreLaunchVars = nil
	exploreLaunchEnv = nil
	exploreNoInheritedLaunchVars = false
	exploreDeviceModel = ""
	exploreOSVersion = ""
	exploreMaxDuration = 30 * time.Minute
	exploreIdleTimeout = 15 * time.Minute
	exploreTimeout = defaultExploreTimeout
	exploreNoWait = false
	exploreOpen = false
}
