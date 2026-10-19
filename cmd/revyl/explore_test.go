package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/launchvars"
)

type fakeExploreAPI struct {
	apps           map[string]*api.App
	searchResults  []api.App
	launchVars     []api.OrgLaunchVariable
	launchVarCalls int
	launch         *api.ExplorationLaunchResponse
	launchCalls    int
	reports        []*api.ExplorationRunReportResponse
	reportErr      error
	reportCalls    int
	cancelled      bool
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
	f.launchVarCalls++
	return &api.OrgLaunchVariablesResponse{Result: f.launchVars}, nil
}

func (f *fakeExploreAPI) LaunchExploration(context.Context, string, *api.ExplorationLaunchRequest) (*api.ExplorationLaunchResponse, error) {
	f.launchCalls++
	if f.launch != nil {
		return f.launch, nil
	}
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

	request, app, err := buildExploreLaunchRequest(&cobra.Command{}, client, []string{"My App"})
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

func TestBuildExploreWebLaunchRequest(t *testing.T) {
	for _, selection := range []string{"explicit platform", "inferred by ID", "inferred by name", "default durations"} {
		t.Run(selection, func(t *testing.T) {
			resetExploreFlagsForTest(t)
			t.Setenv(launchvars.InheritedIDsEnv, "invalid-inherited-id")
			appID, buildID := uuid.NewString(), uuid.NewString()
			webApp := api.App{ID: appID, Name: "Web App", Platform: "web"}
			client := &fakeExploreAPI{
				apps:          map[string]*api.App{appID: &webApp},
				searchResults: []api.App{webApp},
			}
			cmd := &cobra.Command{}
			configureExploreRunFlags(cmd)
			flags := []string{
				"--build-id", buildID,
				"--instructions", "Map account settings",
				"--auth-instructions", "Use the configured sign-in",
			}
			idleSeconds := 900
			if selection != "default durations" {
				flags = append(flags, "--max-duration", "30m", "--idle-timeout", "60m")
				idleSeconds = 3600
			}
			args := []string{appID}
			if selection == "explicit platform" {
				flags = append(flags, "--platform", " WEB ")
			} else if selection == "inferred by name" {
				args = []string{webApp.Name}
			}
			if err := cmd.ParseFlags(flags); err != nil {
				t.Fatal(err)
			}
			request, app, err := buildExploreLaunchRequest(cmd, client, args)
			if err != nil {
				t.Fatalf("buildExploreLaunchRequest(): %v", err)
			}
			if app.ID != appID || request.Platform == nil || *request.Platform != "web" {
				t.Fatalf("app/platform = %s/%v", app.ID, request.Platform)
			}
			encoded, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]interface{}
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]interface{}{
				"platform": "web", "build_id": buildID,
				"lane_count": float64(3), "swarm_strategy": "balanced",
				"max_duration_seconds": float64(1800), "idle_timeout_seconds": float64(idleSeconds),
				"seed_instructions": "Map account settings", "auth_instructions": "Use the configured sign-in",
			} {
				if payload[key] != want {
					t.Errorf("payload[%q] = %v, want %v", key, payload[key], want)
				}
			}
			for _, key := range []string{"device_model", "os_version", "launch_env_var_ids", "env_vars", "run_config"} {
				if _, present := payload[key]; present {
					t.Errorf("Web payload contains %q", key)
				}
			}
			if client.launchVarCalls != 0 {
				t.Fatalf("resolved launch variables %d times for Web", client.launchVarCalls)
			}
		})
	}
}

func TestExploreWebRejectsInvalidFlagsBeforeLaunch(t *testing.T) {
	cases := []struct {
		name  string
		flags []string
		want  string
	}{
		{"device", []string{"--device-model", "iPhone 16"}, "--device-model is not available"},
		{"empty device", []string{"--device-model="}, "--device-model is not available"},
		{"os", []string{"--os-version", "18.5"}, "--os-version is not available"},
		{"stored variables", []string{"--launch-var", "AUTH_TOKEN"}, "--launch-var is not available"},
		{"inline variables", []string{"--launch-env", "INVALID"}, "--launch-env is not available"},
		{"disable inherited", []string{"--no-inherited-launch-vars"}, "--no-inherited-launch-vars is not available"},
		{"explicit inheritance", []string{"--no-inherited-launch-vars=false"}, "--no-inherited-launch-vars is not available"},
		{"max duration", []string{"--max-duration", "30m1s"}, "--max-duration must be between"},
		{"idle timeout", []string{"--idle-timeout", "60m1s"}, "--idle-timeout must be between"},
		{"minimum duration", []string{"--max-duration", "59s"}, "--max-duration must be between"},
		{"minimum idle", []string{"--idle-timeout", "59s"}, "--idle-timeout must be between"},
		{"fractional duration", []string{"--max-duration", "60.5s"}, "--max-duration must use whole seconds"},
		{"fractional idle", []string{"--idle-timeout", "60.5s"}, "--idle-timeout must use whole seconds"},
		{"invalid build", []string{"--build-id", "not-a-uuid"}, "build ID must be a UUID"},
		{"mismatched platform", []string{"--platform", "ios"}, "does not match app platform web"},
	}
	for _, explicitPlatform := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%s/explicit=%t", tc.name, explicitPlatform), func(t *testing.T) {
				resetExploreFlagsForTest(t)
				t.Setenv("REVYL_API_KEY", "test-key")
				appID := uuid.NewString()
				client := &fakeExploreAPI{apps: map[string]*api.App{
					appID: {ID: appID, Name: "Web App", Platform: "web"},
				}}
				previous := exploreNewClient
				exploreNewClient = func(string, bool) exploreAPI { return client }
				t.Cleanup(func() { exploreNewClient = previous })
				cmd := &cobra.Command{}
				configureExploreRunFlags(cmd)
				flags := []string{"--no-wait"}
				if explicitPlatform {
					flags = append(flags, "--platform", "web")
				}
				flags = append(flags, tc.flags...)
				if err := cmd.ParseFlags(flags); err != nil {
					t.Fatal(err)
				}
				err := runExploreRun(cmd, []string{appID})
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("runExploreRun() error = %v, want %q", err, tc.want)
				}
				if client.launchCalls != 0 || client.launchVarCalls != 0 {
					t.Fatalf("invalid Web flags made API calls: launch=%d, variables=%d", client.launchCalls, client.launchVarCalls)
				}
			})
		}
	}
}

func TestExploreMobileDurationLimitsRemainUnchanged(t *testing.T) {
	for _, platform := range []string{"ios", "android"} {
		t.Run(platform, func(t *testing.T) {
			resetExploreFlagsForTest(t)
			exploreNoInheritedLaunchVars = true
			exploreMaxDuration, exploreIdleTimeout = 2*time.Hour, 2*time.Hour
			appID := uuid.NewString()
			client := &fakeExploreAPI{apps: map[string]*api.App{
				appID: {ID: appID, Name: "Mobile App", Platform: platform},
			}}
			request, _, err := buildExploreLaunchRequest(&cobra.Command{}, client, []string{appID})
			if err != nil {
				t.Fatal(err)
			}
			if *request.MaxDurationSeconds != 7200 || *request.IdleTimeoutSeconds != 7200 {
				t.Fatalf("mobile durations = %d, %d", *request.MaxDurationSeconds, *request.IdleTimeoutSeconds)
			}
		})
	}
}

func TestResolveCanonicalConfiguredExploreWebApp(t *testing.T) {
	appID, mobileAppID, secondAppID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		name       string
		platform   string
		webAppIDs  []string
		includeIOS bool
		wantErr    string
	}{
		{"inferred", "", []string{appID}, false, ""},
		{"select web", "web", []string{appID}, true, ""},
		{"same app across profiles", "web", []string{appID, appID}, false, ""},
		{"ambiguous web apps", "web", []string{appID, secondAppID}, false, "multiple web apps"},
		{"missing web app", "web", nil, true, "no configured app for platform web"},
		{"platform required", "", []string{appID}, true, "--platform ios|android|web"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := &config.ProjectContext{Aggregate: &config.NormalizedProjectAggregate{}}
			for _, id := range tc.webAppIDs {
				project.Aggregate.Profiles = append(project.Aggregate.Profiles, config.NormalizedBuildProfile{
					Configurations: []config.NormalizedPlatformConfiguration{{Platform: "web", AppID: &id}},
				})
			}
			if tc.includeIOS {
				project.Aggregate.Profiles = append(project.Aggregate.Profiles, config.NormalizedBuildProfile{
					Configurations: []config.NormalizedPlatformConfiguration{{Platform: "ios", AppID: &mobileAppID}},
				})
			}
			client := &fakeExploreAPI{apps: map[string]*api.App{appID: {ID: appID, Name: "Configured Web App", Platform: "web"}}}
			app, err := resolveCanonicalConfiguredExploreApp(context.Background(), client, project, tc.platform)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || app.ID != appID {
				t.Fatalf("app = %#v, error = %v", app, err)
			}
		})
	}
}

func TestBuildExploreLaunchRequestInfersConfiguredWebApp(t *testing.T) {
	for _, explicitPlatform := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit=%t", explicitPlatform), func(t *testing.T) {
			resetExploreFlagsForTest(t)
			t.Setenv(launchvars.InheritedIDsEnv, "invalid-inherited-id")
			root := t.TempDir()
			gitInitBuildRepository(t, root)
			appID := uuid.NewString()
			writeProjectBuildConfig(t, root, "project: {id: "+buildTestProjectID+"}\nbuild:\n  profiles:\n    preview:\n      web: {app_id: "+appID+"}\n")
			withWorkingDirectory(t, root)
			client := &fakeExploreAPI{apps: map[string]*api.App{
				appID: {ID: appID, Name: "Configured Web App", Platform: "web"},
			}}
			cmd := &cobra.Command{}
			configureExploreRunFlags(cmd)
			if explicitPlatform {
				if err := cmd.ParseFlags([]string{"--platform", "web"}); err != nil {
					t.Fatal(err)
				}
			}
			request, app, err := buildExploreLaunchRequest(cmd, client, nil)
			if err != nil {
				t.Fatalf("buildExploreLaunchRequest(): %v", err)
			}
			if app.ID != appID || request.Platform == nil || *request.Platform != "web" {
				t.Fatalf("configured app/platform = %s/%v", app.ID, request.Platform)
			}
		})
	}
}

func TestExploreAutomaticUsesSourceGitBranchWithoutPersistingBranch(t *testing.T) {
	for _, scenario := range []struct{ name, branch, mode, build, wantGitBranch string }{
		{"automatic", "", "", "", "Feature/git-branch"},
		{"latest", "", "latest", "", "Feature/git-branch"},
		{"explicit branch", "main", "", "", ""},
		{"exact build", "", "", "22222222-2222-4222-8222-222222222222", ""},
		{"all branches", "", "latest_all_branches", "", ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			resetExploreFlagsForTest(t)
			withWorkingDir(t, executionSourceCheckout(t))
			client := &fakeExploreAPI{searchResults: []api.App{{ID: uuid.NewString(), Name: "App", Platform: "ios"}}}
			cmd := &cobra.Command{}
			configureExploreRunFlags(cmd)
			exploreBranchName, exploreBuildSelection, exploreBuildID = scenario.branch, scenario.mode, scenario.build
			request, _, err := buildExploreLaunchRequest(cmd, client, []string{"App"})
			if err != nil {
				t.Fatal(err)
			}
			if scenario.wantGitBranch == "" {
				if request.GitBranchName != nil {
					t.Fatalf("explicit selection inferred context: %+v", request)
				}
			} else if request.GitBranchName == nil || *request.GitBranchName != scenario.wantGitBranch || request.BranchName != nil {
				t.Fatalf("context became explicit or was omitted: %+v", request)
			}
		})
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

func TestExploreConcurrencyTrimIncludesUpgradeWithoutChangingJSON(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		for _, quiet := range []bool{false, true} {
			for _, launched := range []int{0, 2, 5} {
				for _, noWait := range []bool{false, true} {
					t.Run(fmt.Sprintf("json=%t/quiet=%t/launched=%d/no-wait=%t", jsonMode, quiet, launched, noWait), func(t *testing.T) {
						resetExploreFlagsForTest(t)
						t.Setenv("REVYL_API_KEY", "test-key")
						exploreExplorerCount, exploreNoWait, exploreNoInheritedLaunchVars = 5, noWait, true
						appID := uuid.NewString()
						run := exploreRun("running", "processing", "processing", nil)
						configMap := map[string]interface{}{"lane_count": float64(launched)}
						run.Config = &configMap
						report := exploreReport("completed", "completed", "ready", launched, launched)
						report.Run.Config = &configMap
						client := &fakeExploreAPI{
							apps:    map[string]*api.App{appID: {ID: appID, Name: "Example", Platform: "ios"}},
							launch:  &api.ExplorationLaunchResponse{Run: run, ReportUrl: "https://app.example/report"},
							reports: []*api.ExplorationRunReportResponse{report},
						}
						previous := exploreNewClient
						exploreNewClient = func(string, bool) exploreAPI { return client }
						t.Cleanup(func() { exploreNewClient = previous })
						cmd := &cobra.Command{}
						cmd.SetContext(context.Background())
						cmd.Flags().Bool("json", jsonMode, "")
						cmd.Flags().Bool("quiet", quiet, "")
						var stdout, stderr bytes.Buffer
						cmd.SetOut(&stdout)
						cmd.SetErr(&stderr)
						if err := runExploreRun(cmd, []string{appID}); err != nil {
							t.Fatal(err)
						}
						if jsonMode {
							var output exploreOutput
							if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
								t.Fatalf("stdout is not valid JSON: %v", err)
							}
							if output.ExplorersRequested != 5 || output.ExplorersLaunched != launched {
								t.Fatalf("explorer counts changed: %+v", output)
							}
						}
						wantCount := 0
						if !quiet && launched == 2 {
							wantCount = 1
						}
						if strings.Count(stderr.String(), api.ConcurrencyUpgradeHint) != wantCount || strings.Contains(stdout.String(), api.ConcurrencyUpgradeHint) {
							t.Fatalf("want %d hints on stderr only: stdout=%s stderr=%s", wantCount, stdout.String(), stderr.String())
						}
					})
				}
			}
		}
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
	oldBranch, oldSelection, oldAllBranches := exploreBranchName, exploreBuildSelection, exploreAllBranches
	exploreBranchName, exploreBuildSelection, exploreAllBranches = "", "", false
	t.Cleanup(func() {
		exploreBranchName, exploreBuildSelection, exploreAllBranches = oldBranch, oldSelection, oldAllBranches
	})
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

func TestExploreBranchSelectorPreservesOmissionAndExplicitMode(t *testing.T) {
	for _, tc := range []struct {
		name, branch, mode string
		allBranches        bool
		wantMode           string
	}{
		{name: "automatic"},
		{name: "explicit branch", branch: "main"},
		{name: "latest", branch: "feature/one", mode: "latest", wantMode: "latest"},
		{name: "all branches", allBranches: true, wantMode: "latest_all_branches"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetExploreFlagsForTest(t)
			exploreBranchName, exploreBuildSelection, exploreAllBranches = tc.branch, tc.mode, tc.allBranches
			client := &fakeExploreAPI{searchResults: []api.App{{ID: uuid.NewString(), Name: "App", Platform: "ios"}}}
			req, _, err := buildExploreLaunchRequest(&cobra.Command{}, client, []string{"App"})
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]interface{}
			if err := json.Unmarshal(body, &fields); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{"branch_name": tc.branch, "build_selection": tc.wantMode} {
				got, exists := fields[key]
				if want == "" && exists {
					t.Fatalf("%s should be omitted: %s", key, body)
				}
				if want != "" && got != want {
					t.Fatalf("%s=%v want %s", key, got, want)
				}
			}
		})
	}
}

func TestExploreRejectsExactBuildAndLatestBeforeResolvingApp(t *testing.T) {
	resetExploreFlagsForTest(t)
	exploreBuildID, exploreBuildSelection = uuid.NewString(), "latest"
	if _, _, err := buildExploreLaunchRequest(&cobra.Command{}, &fakeExploreAPI{}, []string{"App"}); err == nil || !strings.Contains(err.Error(), "exact build") {
		t.Fatalf("error=%v", err)
	}
}

func TestExploreOutputReportsRecordedBranchProvenance(t *testing.T) {
	for _, test := range []struct {
		config                    map[string]interface{}
		wantSource, wantGitBranch string
	}{
		{config: map[string]interface{}{"branch_source": "default_branch", "git_branch_name": "feature-login"}, wantSource: "default_branch", wantGitBranch: "feature-login"},
		{config: map[string]interface{}{"branch_source": "explicit_branch"}, wantSource: "explicit_branch"},
		{config: map[string]interface{}{"branch_source": "not_a_source"}},
		{config: map[string]interface{}{}},
	} {
		run := exploreRun("running", "processing", "processing", nil)
		run.Config = &test.config
		output := exploreOutputFromRun(run, nil, "https://app.example/report", 1)
		if string(output.BranchSource) != test.wantSource || output.GitBranchName != test.wantGitBranch {
			t.Errorf("config %v: branch_source=%q git_branch_name=%q", test.config, output.BranchSource, output.GitBranchName)
		}
	}
}
