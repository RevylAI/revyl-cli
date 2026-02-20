// Package tui provides the hub model -- the dashboard-first TUI with quick actions.
package tui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/store"
	syncpkg "github.com/revyl/cli/internal/sync"
	"github.com/revyl/cli/internal/ui"
)

// quickAction defines an item in the Quick Actions menu.
type quickAction struct {
	Label        string // display name shown in the menu
	Key          string // internal identifier for dispatch
	Desc         string // short description shown next to the label
	RequiresAuth bool   // whether this action is disabled until authenticated
}

// quickActions is the ordered list of actions on the dashboard.
var quickActions = []quickAction{
	{Label: "Create a test", Key: "create", Desc: "Define a new test from YAML", RequiresAuth: true},
	{Label: "Start Hot Reload Dev Loop", Key: "dev_loop", Desc: "Start revyl dev: hot reload + live cloud device", RequiresAuth: true},
	{Label: "Browse tests", Key: "tests", Desc: "View, sync, and manage tests", RequiresAuth: true},
	{Label: "Browse workflows", Key: "workflows", Desc: "Create, run, and manage workflows", RequiresAuth: true},
	{Label: "Manage apps", Key: "apps", Desc: "List, upload, and delete builds", RequiresAuth: true},
	{Label: "Browse modules", Key: "modules", Desc: "View reusable test modules", RequiresAuth: true},
	{Label: "Browse tags", Key: "tags", Desc: "Manage test tags and labels", RequiresAuth: true},
	{Label: "Device sessions", Key: "devices", Desc: "Start, view, and stop cloud devices", RequiresAuth: true},
	{Label: "Publish to TestFlight", Key: "publish_testflight", Desc: "Upload/distribute iOS builds with ASC", RequiresAuth: true},
	{Label: "Settings", Key: "settings", Desc: "View and edit project defaults", RequiresAuth: false},
	{Label: "Open dashboard", Key: "dashboard", Desc: "Open the web dashboard", RequiresAuth: false},
}

// dashFocus tracks which section of the dashboard has keyboard focus.
type dashFocus int

const (
	focusActions dashFocus = iota // Quick Actions menu (default)
	focusRecent                   // Recent Runs list
)

// hubModel is the top-level Bubble Tea model for the TUI hub.
type hubModel struct {
	version     string
	currentView view

	// Dashboard data
	metrics    *api.DashboardMetrics
	recentRuns []RecentRun

	// Dashboard focus and cursors
	focus           dashFocus
	actionCursor    int
	recentRunCursor int

	// Test list (sub-screen)
	tests                 []TestItem
	testCursor            int
	filterMode            bool
	filterInput           textinput.Model
	filteredTests         []TestItem
	testListConfirmDelete bool
	testListDeleteTarget  TestItem

	// Runs list (sub-screen)
	allRuns       []RecentRun
	allRunsCursor int
	allRunsLoaded bool

	// Report drill-down state
	reportReturnView     view                            // view to return to from run-history screens
	selectedTestID       string                          // test selected for run drill-down
	selectedTestName     string                          // name of selected test
	testRuns             []api.CLIEnhancedHistoryItem    // runs for the selected test
	testRunCursor        int                             // cursor for test runs list
	selectedWorkflowID   string                          // workflow selected for run drill-down
	selectedWorkflowName string                          // name of selected workflow
	workflowRuns         []api.CLIWorkflowStatusResponse // runs for the selected workflow
	workflowRunCursor    int                             // cursor for workflow runs list
	reportLoading        bool                            // loading indicator for report sub-views

	// Report drill-down filter state
	reportFilterMode  bool            // whether filter is active in report views
	reportFilterInput textinput.Model // filter input for report views

	// Manage apps state
	apps            []api.App          // cached app list
	appCursor       int                // cursor for app list
	selectedAppID   string             // app selected for detail view
	selectedAppName string             // name of selected app
	appBuilds       []api.BuildVersion // build versions for the selected app
	appBuildCursor  int                // cursor for build versions list
	appsLoading     bool               // loading indicator for app views
	confirmDelete   bool               // whether a delete confirmation is pending
	deleteTarget    string             // ID of the item pending deletion ("app" or version ID)

	// Help & status state
	healthChecks  []HealthCheck // results from the last health check
	healthLoading bool          // whether health checks are currently running

	// Setup guide state (rendered in help screen)
	setupSteps  []SetupStep
	setupCursor int

	// Test detail state
	selectedTestDetail      *TestDetail
	testDetailLoading       bool
	testDetailCursor        int
	testDetailConfirmDelete bool
	testSyncResult          string

	// Env var editor state (overlay in test detail)
	envVarEditorActive  bool
	envVars             []EnvVarItem
	envVarCursor        int
	envVarLoading       bool
	envVarShowValues    bool
	envVarAddingKey     bool
	envVarAddingValue   bool
	envVarConfirmDelete bool
	envVarKeyInput      textinput.Model
	envVarValueInput    textinput.Model

	// Tag picker state (overlay in test detail)
	tagPickerActive  bool
	tagPickerLoading bool
	tagPickerItems   []tagPickerItem
	tagPickerCursor  int

	// Tag browser state
	tagItems         []TagItem
	tagCursor        int
	tagLoading       bool
	tagConfirmDelete bool
	tagCreateActive  bool
	tagNameInput     textinput.Model

	// Device session state
	deviceSessions       []api.ActiveDeviceSessionItem
	deviceCursor         int
	devicesLoading       bool
	selectedDeviceID     string
	deviceStarting       bool   // true while provisioning a new device
	deviceStartPicking   bool   // true when platform picker overlay is showing
	devicePlatformCursor int    // cursor for platform picker (0=android, 1=ios)
	deviceConfirmStop    bool   // true when stop confirmation is pending
	deviceStopTarget     string // workflow run ID of the session to stop

	// Module browser state
	moduleItems         []ModuleItem
	moduleCursor        int
	moduleLoading       bool
	selectedModuleID    string
	selectedModule      *ModuleItem
	moduleConfirmDelete bool

	// Workflow management state
	wfItems          []WorkflowItem
	wfCursor         int
	wfListLoading    bool
	wfFilterMode     bool
	wfFilterInput    textinput.Model
	wfDetailLoading  bool
	selectedWfDetail *WorkflowItem
	wfConfirmDelete  bool
	wfDetailCursor   int
	// Workflow create wizard
	wfCreateStep          workflowCreateStep
	wfCreateNameInput     textinput.Model
	wfCreateTestCursor    int
	wfCreateSelectedTests map[int]bool
	// Workflow execution monitor
	wfExecTaskID     string
	wfExecStatus     *api.CLIWorkflowStatusResponse
	wfExecDone       bool
	wfExecStartTime  time.Time
	wfExecReturnView view

	// Shared state
	loading bool
	spinner spinner.Model
	err     error
	width   int
	height  int
	apiKey  string
	cfg     *config.ProjectConfig
	client  *api.Client
	devMode bool
	authErr error
	// True when a setup login action should return to dashboard after auth succeeds.
	returnToDashboardAfterAuth bool

	// Local project settings state
	settingsCursor       int
	settingsEditing      bool
	settingsTimeout      int
	settingsOpenBrowser  bool
	settingsTimeoutInput textinput.Model
	settingsConfigPath   string
	settingsStatus       string
	settingsStatusError  bool

	// Sub-models
	executionModel   *executionModel
	createModel      *createModel
	createAppModel   *createAppModel
	uploadBuildModel *uploadBuildModel
	publishTFModel   *publishTestFlightModel
}

// newHubModel creates the initial hub model.
//
// Parameters:
//   - version: the CLI version string for display
//   - devMode: whether to use local development servers (--dev flag)
//
// Returns:
//   - hubModel: the initialized model
func newHubModel(version string, devMode bool) hubModel {
	ti := textinput.New()
	ti.Placeholder = "filter tests..."
	ti.CharLimit = 64

	rfi := textinput.New()
	rfi.Placeholder = "filter..."
	rfi.CharLimit = 64

	eki := textinput.New()
	eki.Placeholder = "KEY"
	eki.CharLimit = 128

	evi := textinput.New()
	evi.Placeholder = "VALUE"
	evi.CharLimit = 512

	tni := textinput.New()
	tni.Placeholder = "tag name..."
	tni.CharLimit = 64

	wfni := textinput.New()
	wfni.Placeholder = "workflow name..."
	wfni.CharLimit = 128

	wffi := textinput.New()
	wffi.Placeholder = "filter workflows..."
	wffi.CharLimit = 64

	sti := textinput.New()
	sti.Placeholder = "timeout seconds"
	sti.CharLimit = 8
	sti.SetValue(fmt.Sprintf("%d", config.DefaultTimeoutSeconds))

	return hubModel{
		version:              version,
		currentView:          viewDashboard,
		loading:              true,
		spinner:              newSpinner(),
		filterInput:          ti,
		reportFilterInput:    rfi,
		envVarKeyInput:       eki,
		envVarValueInput:     evi,
		tagNameInput:         tni,
		wfCreateNameInput:    wfni,
		wfFilterInput:        wffi,
		settingsTimeout:      config.DefaultTimeoutSeconds,
		settingsOpenBrowser:  config.DefaultOpenBrowser,
		settingsTimeoutInput: sti,
		devMode:              devMode,
	}
}

// --- Tea commands for async operations ---

// AuthMsg carries the authenticated client and token after initial auth succeeds.
type AuthMsg struct {
	Client *api.Client
	Token  string
	Err    error
}

// DevLoopDoneMsg signals completion of a shell-launched `revyl dev` session.
type DevLoopDoneMsg struct {
	Err error
}

// authenticateCmd resolves the auth token and creates an API client.
// This runs once on startup; the returned client is cached on the model.
func authenticateCmd(devMode bool) tea.Cmd {
	return func() tea.Msg {
		mgr := auth.NewManager()
		token, err := mgr.GetActiveToken()
		if err != nil || token == "" {
			return AuthMsg{Err: fmt.Errorf("not authenticated — run 'revyl auth login' first")}
		}
		client := api.NewClientWithDevMode(token, devMode)
		return AuthMsg{Client: client, Token: token}
	}
}

// fetchTestsCmd fetches the org test list using an already-authenticated client.
func fetchTestsCmd(client *api.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		remoteTests, err := client.ListAllOrgTests(ctx, 200)
		if err != nil {
			return TestListMsg{Err: fmt.Errorf("failed to fetch tests: %w", err)}
		}

		items := make([]TestItem, 0, len(remoteTests))
		remoteByID := make(map[string]api.SimpleTest, len(remoteTests))
		for _, t := range remoteTests {
			remoteByID[t.ID] = t
		}

		cwd, cwdErr := os.Getwd()
		if cwdErr == nil {
			configPath := filepath.Join(cwd, ".revyl", "config.yaml")
			cfg, cfgErr := config.LoadProjectConfig(configPath)
			if cfgErr == nil {
				testsDir := filepath.Join(cwd, ".revyl", "tests")
				localTests, lErr := config.LoadLocalTests(testsDir)
				if lErr != nil {
					localTests = make(map[string]*config.LocalTest)
				}

				resolver := syncpkg.NewResolver(client, cfg, localTests)
				statuses, sErr := resolver.GetAllStatuses(ctx)
				if sErr == nil {
					usedRemoteIDs := make(map[string]bool)
					for _, s := range statuses {
						item := TestItem{
							ID:         s.RemoteID,
							Name:       s.Name,
							SyncStatus: s.Status.String(),
							Source:     deriveTestSource(cfg, localTests, s.Name),
						}

						if lt, ok := localTests[s.Name]; ok && lt != nil {
							item.Platform = lt.Test.Metadata.Platform
						}
						if rt, ok := remoteByID[s.RemoteID]; ok {
							if item.Platform == "" {
								item.Platform = rt.Platform
							}
							usedRemoteIDs[s.RemoteID] = true
						} else if s.RemoteID != "" && cfg.Tests[s.Name] != "" {
							item.SyncStatus = "stale"
							item.RemoteMissing = true
						}
						if item.SyncStatus == "" {
							item.SyncStatus = "unknown"
						}
						items = append(items, item)
					}

					for _, t := range remoteTests {
						if usedRemoteIDs[t.ID] {
							continue
						}
						items = append(items, TestItem{
							ID:         t.ID,
							Name:       t.Name,
							Platform:   t.Platform,
							SyncStatus: "remote-only",
							Source:     "remote",
						})
					}
				}
			}
		}

		if len(items) == 0 {
			items = make([]TestItem, len(remoteTests))
			for i, t := range remoteTests {
				items[i] = TestItem{
					ID:         t.ID,
					Name:       t.Name,
					Platform:   t.Platform,
					SyncStatus: "remote-only",
					Source:     "remote",
				}
			}
		}

		sort.Slice(items, func(i, j int) bool {
			nameI := strings.ToLower(items[i].Name)
			nameJ := strings.ToLower(items[j].Name)
			if nameI == nameJ {
				return items[i].ID < items[j].ID
			}
			return nameI < nameJ
		})

		return TestListMsg{Tests: items}
	}
}

// fetchDashboardMetricsCmd fetches org-level dashboard metrics.
func fetchDashboardMetricsCmd(client *api.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		metrics, err := client.GetDashboardMetrics(ctx)
		return DashboardDataMsg{Metrics: metrics, Err: err}
	}
}

// fetchRecentRunsCmd fetches the most recent execution for up to N tests in parallel.
func fetchRecentRunsCmd(client *api.Client, tests []TestItem, count int) tea.Cmd {
	return func() tea.Msg {
		if len(tests) == 0 {
			return RecentRunsMsg{}
		}
		n := count
		if n > len(tests) {
			n = len(tests)
		}

		var mu sync.Mutex
		var results []RecentRun
		var wg sync.WaitGroup

		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(t TestItem) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				hist, err := client.GetTestEnhancedHistory(ctx, t.ID, 1, 0)
				if err != nil || len(hist.Items) == 0 {
					return
				}
				item := hist.Items[0]
				var ts time.Time
				if item.ExecutionTime != "" {
					ts, _ = time.Parse(time.RFC3339, item.ExecutionTime)
				}
				dur := ""
				if item.Duration != nil {
					dur = fmt.Sprintf("%.0fs", *item.Duration)
				}
				mu.Lock()
				results = append(results, RecentRun{
					TestID:   t.ID,
					TestName: t.Name,
					Status:   item.Status,
					Duration: dur,
					Time:     ts,
					TaskID:   item.ID,
				})
				mu.Unlock()
			}(tests[i])
		}
		wg.Wait()

		sort.Slice(results, func(i, j int) bool {
			return results[i].Time.After(results[j].Time)
		})
		if len(results) > 5 {
			results = results[:5]
		}

		return RecentRunsMsg{Runs: results}
	}
}

// fetchAllRunsCmd fetches all recent runs (up to 25) for the "View all runs" screen.
func fetchAllRunsCmd(client *api.Client, tests []TestItem) tea.Cmd {
	return func() tea.Msg {
		if len(tests) == 0 {
			return AllRunsMsg{}
		}
		n := len(tests)
		if n > 25 {
			n = 25
		}

		var mu sync.Mutex
		var results []RecentRun
		var wg sync.WaitGroup

		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(t TestItem) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				hist, err := client.GetTestEnhancedHistory(ctx, t.ID, 1, 0)
				if err != nil || len(hist.Items) == 0 {
					return
				}
				item := hist.Items[0]
				var ts time.Time
				if item.ExecutionTime != "" {
					ts, _ = time.Parse(time.RFC3339, item.ExecutionTime)
				}
				dur := ""
				if item.Duration != nil {
					dur = fmt.Sprintf("%.0fs", *item.Duration)
				}
				mu.Lock()
				results = append(results, RecentRun{
					TestID:   t.ID,
					TestName: t.Name,
					Status:   item.Status,
					Duration: dur,
					Time:     ts,
					TaskID:   item.ID,
				})
				mu.Unlock()
			}(tests[i])
		}
		wg.Wait()

		sort.Slice(results, func(i, j int) bool {
			return results[i].Time.After(results[j].Time)
		})

		return AllRunsMsg{Runs: results}
	}
}

// fetchTestHistoryCmd fetches execution history for a specific test.
//
// Parameters:
//   - client: authenticated API client
//   - testID: the test to fetch history for
//
// Returns:
//   - tea.Cmd: command that produces a TestHistoryMsg
func fetchTestHistoryCmd(client *api.Client, testID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := client.GetTestEnhancedHistory(ctx, testID, 20, 0)
		if err != nil {
			return TestHistoryMsg{Err: fmt.Errorf("failed to fetch test history: %w", err)}
		}
		return TestHistoryMsg{Runs: resp.Items}
	}
}

// fetchWorkflowHistoryCmd fetches execution history for a specific workflow.
//
// Parameters:
//   - client: authenticated API client
//   - workflowID: the workflow to fetch history for
//
// Returns:
//   - tea.Cmd: command that produces a WorkflowHistoryMsg
func fetchWorkflowHistoryCmd(client *api.Client, workflowID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := client.GetWorkflowHistory(ctx, workflowID, 20, 0)
		if err != nil {
			return WorkflowHistoryMsg{Err: fmt.Errorf("failed to fetch workflow history: %w", err)}
		}
		return WorkflowHistoryMsg{Runs: resp.Executions}
	}
}

// fetchAppsCmd fetches the org app list.
//
// Parameters:
//   - client: authenticated API client
//
// Returns:
//   - tea.Cmd: command that produces an AppListMsg
func fetchAppsCmd(client *api.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := client.ListApps(ctx, "", 1, 100)
		if err != nil {
			return AppListMsg{Err: fmt.Errorf("failed to fetch apps: %w", err)}
		}
		return AppListMsg{Apps: resp.Items}
	}
}

// fetchAppBuildsCmd fetches build versions for a specific app.
//
// Parameters:
//   - client: authenticated API client
//   - appID: the app to fetch builds for
//
// Returns:
//   - tea.Cmd: command that produces an AppBuildVersionsMsg
func fetchAppBuildsCmd(client *api.Client, appID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		versions, err := client.ListBuildVersions(ctx, appID)
		if err != nil {
			return AppBuildVersionsMsg{Err: fmt.Errorf("failed to fetch builds: %w", err)}
		}
		return AppBuildVersionsMsg{Versions: versions}
	}
}

// deleteAppCmd deletes an app by ID.
//
// Parameters:
//   - client: authenticated API client
//   - appID: the app to delete
//
// Returns:
//   - tea.Cmd: command that produces an AppDeletedMsg
func deleteAppCmd(client *api.Client, appID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := client.DeleteApp(ctx, appID)
		return AppDeletedMsg{Err: err}
	}
}

// deleteBuildCmd deletes a build version by ID.
//
// Parameters:
//   - client: authenticated API client
//   - versionID: the build version to delete
//
// Returns:
//   - tea.Cmd: command that produces a BuildDeletedMsg
func deleteBuildCmd(client *api.Client, versionID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := client.DeleteBuildVersion(ctx, versionID)
		return BuildDeletedMsg{Err: err}
	}
}

type testDeleteLocalResult struct {
	Deleted bool
	Warning string
}

// deleteTestFromListCmd deletes a test from remote and local project artifacts.
func deleteTestFromListCmd(client *api.Client, selected TestItem) tea.Cmd {
	return func() tea.Msg {
		msg := TestDeletedMsg{
			Name: selected.Name,
			ID:   selected.ID,
		}

		if client == nil {
			msg.Err = fmt.Errorf("no authenticated client available")
			return msg
		}

		if selected.ID != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := client.DeleteTest(ctx, selected.ID)
			if err != nil {
				var apiErr *api.APIError
				if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
					msg.Warning = fmt.Sprintf("test %q was already missing on remote", selected.Name)
				} else {
					msg.Err = fmt.Errorf("failed to delete test %q from remote: %w", selected.Name, err)
					return msg
				}
			} else {
				msg.RemoteDeleted = true
			}
		}

		localResult := deleteTestLocalArtifacts(selected.Name, selected.ID)
		msg.LocalDeleted = localResult.Deleted
		if localResult.Warning != "" {
			if msg.Warning != "" {
				msg.Warning += "; " + localResult.Warning
			} else {
				msg.Warning = localResult.Warning
			}
		}

		return msg
	}
}

func deleteTestLocalArtifacts(testName, testID string) testDeleteLocalResult {
	result := testDeleteLocalResult{}
	if testName == "" && testID == "" {
		return result
	}

	cwd, err := os.Getwd()
	if err != nil {
		result.Warning = fmt.Sprintf("failed to resolve working directory: %v", err)
		return result
	}

	testsDir := filepath.Join(cwd, ".revyl", "tests")
	configPath := filepath.Join(cwd, ".revyl", "config.yaml")

	candidates := map[string]struct{}{}
	if testName != "" {
		candidates[testName] = struct{}{}
	}
	warnings := make([]string, 0, 2)

	localTests, localErr := config.LoadLocalTests(testsDir)
	if localErr != nil && !os.IsNotExist(localErr) {
		warnings = append(warnings, fmt.Sprintf("failed to load local tests: %v", localErr))
	} else if testID != "" {
		for localName, lt := range localTests {
			if lt != nil && lt.Meta.RemoteID == testID {
				candidates[localName] = struct{}{}
			}
		}
	}

	cfg, cfgErr := config.LoadProjectConfig(configPath)
	if cfgErr != nil {
		if !os.IsNotExist(cfgErr) {
			warnings = append(warnings, fmt.Sprintf("failed to load project config: %v", cfgErr))
		}
	} else if cfg.Tests != nil {
		configChanged := false
		for alias, remoteID := range cfg.Tests {
			if alias == testName || (testID != "" && remoteID == testID) {
				delete(cfg.Tests, alias)
				candidates[alias] = struct{}{}
				configChanged = true
			}
		}
		if configChanged {
			if err := config.WriteProjectConfig(configPath, cfg); err != nil {
				warnings = append(warnings, fmt.Sprintf("failed to update config aliases: %v", err))
			} else {
				result.Deleted = true
			}
		}
	}

	for name := range candidates {
		if strings.TrimSpace(name) == "" {
			continue
		}
		path := filepath.Join(testsDir, name+".yaml")
		if err := os.Remove(path); err != nil {
			if !os.IsNotExist(err) {
				warnings = append(warnings, fmt.Sprintf("failed to remove %s: %v", path, err))
			}
			continue
		}
		result.Deleted = true
	}

	result.Warning = strings.Join(warnings, "; ")
	return result
}

// --- Bubble Tea interface ---

// Init starts the spinner and kicks off authentication.
func (m hubModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, authenticateCmd(m.devMode))
}

// Update handles all incoming messages and key events.
func (m hubModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Delegate to sub-models when active
	if m.currentView == viewExecution && m.executionModel != nil {
		return m.updateExecution(msg)
	}
	if m.currentView == viewCreateTest && m.createModel != nil {
		return m.updateCreate(msg)
	}
	if m.currentView == viewCreateApp && m.createAppModel != nil {
		return m.updateCreateApp(msg)
	}
	if m.currentView == viewUploadBuild && m.uploadBuildModel != nil {
		return m.updateUploadBuild(msg)
	}
	if m.currentView == viewPublishTestFlight && m.publishTFModel != nil {
		return m.updatePublishTestFlight(msg)
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.currentView != viewDashboard && m.currentView != viewHelp && m.currentView != viewSettings && !m.isAuthenticated() {
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.currentView = viewDashboard
				return m, nil
			}
			return m.requireAuthentication("Sign in required. Press Enter on 'Log in' (or 'a' for API key).")
		}

		if m.currentView == viewTestList {
			return m.handleTestListKey(msg)
		}
		if m.currentView == viewRunsList {
			return m.handleRunsListKey(msg)
		}
		if m.currentView == viewTestRuns {
			return m.handleTestRunsKey(msg)
		}
		if m.currentView == viewWorkflowRuns {
			return m.handleWorkflowRunsKey(msg)
		}
		if m.currentView == viewAppList {
			return m.handleAppListKey(msg)
		}
		if m.currentView == viewAppDetail {
			return m.handleAppDetailKey(msg)
		}
		if m.currentView == viewHelp {
			return m.handleHelpKey(msg)
		}
		if m.currentView == viewSettings {
			return m.handleSettingsKey(msg)
		}
		if m.currentView == viewTestDetail {
			return handleTestDetailKey(m, msg)
		}
		if m.currentView == viewWorkflowList {
			return handleWorkflowListKey(m, msg)
		}
		if m.currentView == viewWorkflowDetail {
			return handleWorkflowDetailKey(m, msg)
		}
		if m.currentView == viewWorkflowCreate {
			return handleWorkflowCreateKey(m, msg)
		}
		if m.currentView == viewWorkflowExecution {
			return handleWorkflowExecKey(m, msg)
		}
		if m.currentView == viewModuleList {
			return handleModuleListKey(m, msg)
		}
		if m.currentView == viewModuleDetail {
			return handleModuleDetailKey(m, msg)
		}
		if m.currentView == viewTagList {
			return handleTagListKey(m, msg)
		}
		if m.currentView == viewDeviceList {
			return m.handleDeviceListKey(msg)
		}
		if m.currentView == viewDeviceDetail {
			return m.handleDeviceDetailKey(msg)
		}
		return m.handleDashboardKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case AuthMsg:
		if msg.Err != nil {
			m.loading = false
			m.client = nil
			m.apiKey = ""
			m.authErr = msg.Err
			m.err = nil
			m.currentView = viewHelp
			m.healthLoading = true
			m.healthChecks = nil
			return m, runHealthChecksCmd(m.devMode, nil)
		}
		m.authErr = nil
		m.err = nil
		m.apiKey = msg.Token
		m.client = msg.Client
		if m.returnToDashboardAfterAuth {
			m.currentView = viewDashboard
			m.returnToDashboardAfterAuth = false
		}
		// Fire tests, metrics, and recent-runs fetch all in parallel
		return m, tea.Batch(
			fetchTestsCmd(m.client),
			fetchDashboardMetricsCmd(m.client),
		)

	case TestListMsg:
		m.loading = false
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}
		m.tests = msg.Tests
		if strings.TrimSpace(m.filterInput.Value()) == "" {
			m.filteredTests = nil
		} else {
			m.applyFilter()
		}
		visible := m.visibleTests()
		switch {
		case len(visible) == 0:
			m.testCursor = 0
		case m.testCursor >= len(visible):
			m.testCursor = len(visible) - 1
		}
		// Now fetch recent runs (depends on test list)
		if m.client != nil {
			return m, fetchRecentRunsCmd(m.client, m.tests, 5)
		}
		return m, nil

	case DashboardDataMsg:
		if msg.Err == nil {
			m.metrics = msg.Metrics
		}
		return m, nil

	case RecentRunsMsg:
		if msg.Err == nil {
			m.recentRuns = msg.Runs
		}
		return m, nil

	case AllRunsMsg:
		m.allRunsLoaded = true
		if msg.Err == nil {
			m.allRuns = msg.Runs
		}
		return m, nil

	case HealthCheckMsg:
		m.healthLoading = false
		if msg.Err == nil {
			m.healthChecks = msg.Checks
			// Clear stale auth warning once checks confirm authentication is valid.
			for _, check := range msg.Checks {
				if check.Name == "Authentication" && check.Status == "ok" {
					m.authErr = nil
					break
				}
			}
			// Derive setup steps from health check results
			m.setupSteps = deriveSetupSteps(msg.Checks, m.cfg)
			if len(m.setupSteps) == 0 {
				m.setupCursor = 0
			} else if m.setupCursor >= len(m.setupSteps) {
				if first := firstActionableStep(m.setupSteps); first >= 0 {
					m.setupCursor = first
				} else {
					m.setupCursor = 0
				}
			}
		}
		return m, nil

	case TestHistoryMsg:
		m.reportLoading = false
		if msg.Err == nil {
			m.testRuns = msg.Runs
		}
		return m, nil

	case WorkflowHistoryMsg:
		m.reportLoading = false
		if msg.Err == nil {
			m.workflowRuns = msg.Runs
		}
		return m, nil

	case AppListMsg:
		m.appsLoading = false
		if msg.Err == nil {
			m.apps = msg.Apps
		}
		return m, nil

	case AppBuildVersionsMsg:
		m.appsLoading = false
		if msg.Err == nil {
			m.appBuilds = msg.Versions
		}
		return m, nil

	case AppDeletedMsg:
		m.appsLoading = false
		m.confirmDelete = false
		m.deleteTarget = ""
		if msg.Err == nil && m.client != nil {
			m.appsLoading = true
			return m, fetchAppsCmd(m.client)
		}
		return m, nil

	case BuildDeletedMsg:
		m.appsLoading = false
		m.confirmDelete = false
		m.deleteTarget = ""
		if msg.Err == nil && m.client != nil && m.selectedAppID != "" {
			m.appsLoading = true
			return m, fetchAppBuildsCmd(m.client, m.selectedAppID)
		}
		return m, nil

	// --- Test detail messages ---

	case TestDetailMsg:
		m.testDetailLoading = false
		if msg.Err == nil {
			m.selectedTestDetail = msg.Detail
		}
		return m, nil

	case TestSyncActionMsg:
		m.testDetailLoading = false
		if msg.Err != nil {
			m.testSyncResult = fmt.Sprintf("Error: %v", msg.Err)
		} else {
			m.testSyncResult = msg.Result
		}
		// Refresh detail after sync
		if m.selectedTestDetail != nil && m.client != nil {
			return m, fetchTestDetailCmd(m.client, m.selectedTestDetail.ID, m.selectedTestDetail.Name, m.selectedTestDetail.Platform, m.devMode)
		}
		return m, nil

	case TestDeletedMsg:
		m.loading = false
		m.testListConfirmDelete = false
		m.testListDeleteTarget = TestItem{}
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}
		if m.currentView == viewTestDetail {
			m.currentView = viewTestList
			m.selectedTestDetail = nil
		}
		m.err = nil
		// Refresh test list
		if m.client != nil {
			return m, fetchTestsCmd(m.client)
		}
		return m, nil

	// --- Env var messages ---

	case EnvVarListMsg:
		m.envVarLoading = false
		if msg.Err == nil {
			m.envVars = msg.Vars
			m.envVarCursor = 0
		}
		return m, nil

	case EnvVarAddedMsg:
		m.envVarLoading = false
		if msg.Err == nil && m.client != nil && m.selectedTestDetail != nil {
			m.envVarLoading = true
			return m, fetchEnvVarsCmd(m.client, m.selectedTestDetail.ID)
		}
		return m, nil

	case EnvVarDeletedMsg:
		m.envVarLoading = false
		if msg.Err == nil && m.client != nil && m.selectedTestDetail != nil {
			m.envVarLoading = true
			return m, fetchEnvVarsCmd(m.client, m.selectedTestDetail.ID)
		}
		return m, nil

	// --- Tag messages ---

	case TagListMsg:
		m.tagLoading = false
		if msg.Err == nil {
			m.tagItems = msg.Tags
		}
		return m, nil

	case TagCreatedMsg:
		m.tagLoading = false
		if msg.Err == nil && m.client != nil {
			m.tagLoading = true
			return m, fetchTagsCmd(m.client)
		}
		return m, nil

	case TagDeletedMsg:
		m.tagLoading = false
		if msg.Err == nil && m.client != nil {
			m.tagLoading = true
			return m, fetchTagsCmd(m.client)
		}
		return m, nil

	case tagPickerDataMsg:
		m.tagPickerLoading = false
		if msg.Err == nil {
			m.tagPickerItems = msg.Items
			m.tagPickerCursor = 0
		}
		return m, nil

	case TagsSyncedMsg:
		m.tagPickerLoading = false
		m.tagPickerActive = false
		// Refresh test detail to show updated tags
		if msg.Err == nil && m.selectedTestDetail != nil && m.client != nil {
			m.testDetailLoading = true
			return m, fetchTestDetailCmd(m.client, m.selectedTestDetail.ID, m.selectedTestDetail.Name, m.selectedTestDetail.Platform, m.devMode)
		}
		return m, nil

	// --- Module messages ---

	case ModuleListMsg:
		m.moduleLoading = false
		if msg.Err == nil {
			m.moduleItems = msg.Modules
		}
		return m, nil

	case ModuleDetailMsg:
		m.moduleLoading = false
		if msg.Err == nil {
			m.selectedModule = msg.Module
			m.currentView = viewModuleDetail
		}
		return m, nil

	case ModuleDeletedMsg:
		m.moduleLoading = false
		if msg.Err == nil {
			m.currentView = viewModuleList
			m.selectedModule = nil
			if m.client != nil {
				m.moduleLoading = true
				return m, fetchModulesCmd(m.client)
			}
		}
		return m, nil

	// --- Device session messages ---

	case DeviceSessionListMsg:
		m.devicesLoading = false
		if msg.Err == nil {
			m.deviceSessions = msg.Sessions
		}
		return m, nil

	case DeviceStartedMsg:
		m.deviceStarting = false
		if msg.Err != nil {
			m.devicesLoading = false
			return m, nil
		}
		// Open the viewer URL if available
		if msg.ViewerURL != "" {
			_ = ui.OpenBrowser(msg.ViewerURL)
		}
		// Refresh the session list
		m.devicesLoading = true
		if m.client != nil {
			return m, fetchDeviceSessionsCmd(m.client)
		}
		return m, nil

	case DeviceStoppedMsg:
		m.devicesLoading = false
		m.deviceConfirmStop = false
		m.deviceStopTarget = ""
		if msg.Err == nil && m.client != nil {
			m.devicesLoading = true
			return m, fetchDeviceSessionsCmd(m.client)
		}
		return m, nil

	// --- Workflow management messages ---

	case WorkflowBrowseListMsg:
		m.wfListLoading = false
		if msg.Err == nil {
			m.wfItems = msg.Workflows
		}
		return m, nil

	case WorkflowDetailMsg:
		m.wfDetailLoading = false
		if msg.Err == nil {
			m.selectedWfDetail = msg.Workflow
			m.wfDetailCursor = 0
		}
		return m, nil

	case WorkflowCreatedMsg:
		m.wfDetailLoading = false
		if msg.Err == nil {
			m.currentView = viewWorkflowList
			if m.client != nil {
				m.wfListLoading = true
				return m, fetchWorkflowBrowseListCmd(m.client)
			}
		}
		return m, nil

	case WorkflowDeletedMsg:
		m.wfDetailLoading = false
		if msg.Err == nil {
			m.currentView = viewWorkflowList
			m.selectedWfDetail = nil
			if m.client != nil {
				m.wfListLoading = true
				return m, fetchWorkflowBrowseListCmd(m.client)
			}
		}
		return m, nil

	case WorkflowExecStartedMsg:
		if msg.Err != nil {
			m.wfExecDone = true
			m.err = msg.Err
			return m, nil
		}
		m.wfExecTaskID = msg.TaskID
		if m.client != nil {
			return m, pollWorkflowStatusCmd(m.client, msg.TaskID)
		}
		return m, nil

	case WorkflowExecProgressMsg:
		if msg.Err == nil {
			m.wfExecStatus = msg.Status
		}
		// Continue polling
		if m.client != nil && m.wfExecTaskID != "" {
			return m, pollWorkflowStatusCmd(m.client, m.wfExecTaskID)
		}
		return m, nil

	case WorkflowExecDoneMsg:
		m.wfExecDone = true
		if msg.Err == nil {
			m.wfExecStatus = msg.Status
		}
		return m, nil

	case WorkflowCancelledMsg:
		m.wfExecDone = true
		return m, nil

	// --- Setup guide messages ---

	case SetupActionMsg:
		if msg.StepIndex == 0 {
			if msg.Err != nil {
				m.returnToDashboardAfterAuth = false
				m.authErr = fmt.Errorf("authentication canceled or failed; press Enter for browser login or 'a' for API key")
				m.healthLoading = true
				m.healthChecks = nil
				return m, runHealthChecksCmd(m.devMode, m.client)
			}
			m.loading = true
			m.authErr = nil
			return m, tea.Batch(m.spinner.Tick, authenticateCmd(m.devMode))
		}
		// Re-run health checks after a setup action completes
		m.healthLoading = true
		m.healthChecks = nil
		return m, runHealthChecksCmd(m.devMode, m.client)

	case DevLoopDoneMsg:
		m.currentView = viewDashboard
		m.loading = false
		if msg.Err != nil {
			m.err = fmt.Errorf("dev loop exited with error: %w", msg.Err)
			return m, nil
		}
		m.err = nil
		if m.client != nil {
			m.loading = true
			return m, tea.Batch(
				fetchTestsCmd(m.client),
				fetchDashboardMetricsCmd(m.client),
			)
		}
		return m, nil
	}

	// Update filter input if in filter mode (test list view)
	if m.filterMode {
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.applyFilter()
		return m, cmd
	}

	return m, nil
}

// handleDashboardKey processes key events on the dashboard landing page.
func (m hubModel) handleDashboardKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c", "esc":
		return m, tea.Quit

	case "tab":
		if m.focus == focusActions {
			if len(m.recentRuns) > 0 {
				m.focus = focusRecent
			}
		} else {
			m.focus = focusActions
		}
		return m, nil

	case "up", "k":
		if m.focus == focusActions {
			if m.actionCursor > 0 {
				m.actionCursor--
			}
		} else {
			if m.recentRunCursor > 0 {
				m.recentRunCursor--
			}
		}

	case "down", "j":
		if m.focus == focusActions {
			if m.actionCursor < len(quickActions)-1 {
				m.actionCursor++
			}
		} else {
			if m.recentRunCursor < len(m.recentRuns)-1 {
				m.recentRunCursor++
			}
		}

	case "enter":
		if m.focus == focusRecent && len(m.recentRuns) > 0 && m.recentRunCursor < len(m.recentRuns) {
			run := m.recentRuns[m.recentRunCursor]
			if run.TaskID != "" {
				reportURL := fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(m.devMode), run.TaskID)
				_ = ui.OpenBrowser(reportURL)
			}
			return m, nil
		}
		return m.executeQuickAction()

	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		idx := int(msg.String()[0]-'0') - 1
		if idx < len(quickActions) {
			m.focus = focusActions
			m.actionCursor = idx
			return m.executeQuickAction()
		}

	case "0":
		idx := 9 // "0" maps to the 10th item (index 9)
		if idx < len(quickActions) {
			m.focus = focusActions
			m.actionCursor = idx
			return m.executeQuickAction()
		}

	case "/":
		if !m.isAuthenticated() {
			return m.requireAuthentication("Sign in required to browse tests. Press Enter on 'Log in' (or 'a' for API key).")
		}
		m.currentView = viewTestList
		m.testCursor = 0
		m.filterMode = true
		m.filteredTests = nil
		m.filterInput.SetValue("")
		m.filterInput.Focus()
		return m, textinput.Blink

	case "R":
		m.loading = true
		m.err = nil
		m.authErr = nil
		m.metrics = nil
		m.recentRuns = nil
		if m.client != nil {
			// Reuse cached client for refresh
			return m, tea.Batch(
				m.spinner.Tick,
				fetchTestsCmd(m.client),
				fetchDashboardMetricsCmd(m.client),
			)
		}
		return m, tea.Batch(m.spinner.Tick, authenticateCmd(m.devMode))

	case "?":
		m.currentView = viewHelp
		m.healthLoading = true
		m.healthChecks = nil
		return m, runHealthChecksCmd(m.devMode, m.client)
	}

	return m, nil
}

func (m hubModel) commitSettingsTimeoutInput() (hubModel, bool) {
	timeoutText := strings.TrimSpace(m.settingsTimeoutInput.Value())
	timeout, err := strconv.Atoi(timeoutText)
	if err != nil || timeout <= 0 {
		m.settingsStatus = "Timeout must be a positive integer (seconds)."
		m.settingsStatusError = true
		return m, false
	}
	m.settingsTimeout = timeout
	return m, true
}

func (m hubModel) saveSettings() (hubModel, tea.Cmd) {
	m.settingsStatus = ""
	m.settingsStatusError = false

	var ok bool
	m, ok = m.commitSettingsTimeoutInput()
	if !ok {
		return m, nil
	}

	if m.settingsConfigPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			m.settingsStatus = fmt.Sprintf("Failed to resolve config path: %v", err)
			m.settingsStatusError = true
			return m, nil
		}
		m.settingsConfigPath = filepath.Join(cwd, ".revyl", "config.yaml")
	}

	cfg, err := config.LoadProjectConfig(m.settingsConfigPath)
	if err != nil {
		m.settingsStatus = fmt.Sprintf("Failed to load config: %v", err)
		m.settingsStatusError = true
		return m, nil
	}

	openBrowser := m.settingsOpenBrowser
	cfg.Defaults.OpenBrowser = &openBrowser
	cfg.Defaults.Timeout = m.settingsTimeout

	if err := config.WriteProjectConfig(m.settingsConfigPath, cfg); err != nil {
		m.settingsStatus = fmt.Sprintf("Failed to save settings: %v", err)
		m.settingsStatusError = true
		return m, nil
	}

	m.cfg = cfg
	m.settingsStatus = "Settings saved."
	m.settingsStatusError = false
	return m, nil
}

func (m hubModel) handleSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.settingsEditing {
		switch msg.String() {
		case "esc":
			m.settingsEditing = false
			m.settingsTimeoutInput.Blur()
			m.settingsTimeoutInput.SetValue(fmt.Sprintf("%d", m.settingsTimeout))
			return m, nil
		case "enter":
			var ok bool
			m, ok = m.commitSettingsTimeoutInput()
			if !ok {
				return m, nil
			}
			m.settingsEditing = false
			m.settingsTimeoutInput.Blur()
			m.settingsStatus = ""
			m.settingsStatusError = false
			return m, nil
		default:
			var cmd tea.Cmd
			m.settingsTimeoutInput, cmd = m.settingsTimeoutInput.Update(msg)
			return m, cmd
		}
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.currentView = viewDashboard
		return m, nil
	case "up", "k":
		if m.settingsCursor > 0 {
			m.settingsCursor--
		}
		return m, nil
	case "down", "j":
		if m.settingsCursor < 2 {
			m.settingsCursor++
		}
		return m, nil
	case "left", "right", " ":
		if m.settingsCursor == 0 {
			m.settingsOpenBrowser = !m.settingsOpenBrowser
			m.settingsStatus = ""
			m.settingsStatusError = false
		}
		return m, nil
	case "s":
		return m.saveSettings()
	case "enter":
		switch m.settingsCursor {
		case 0:
			m.settingsOpenBrowser = !m.settingsOpenBrowser
			m.settingsStatus = ""
			m.settingsStatusError = false
			return m, nil
		case 1:
			m.settingsEditing = true
			m.settingsTimeoutInput.Focus()
			return m, textinput.Blink
		case 2:
			return m.saveSettings()
		}
	}

	return m, nil
}

// executeQuickAction dispatches the currently selected quick action.
func (m hubModel) executeQuickAction() (tea.Model, tea.Cmd) {
	if m.actionCursor >= len(quickActions) {
		return m, nil
	}

	action := quickActions[m.actionCursor]
	if action.RequiresAuth && !m.isAuthenticated() {
		return m.requireAuthentication(fmt.Sprintf("%s requires authentication. Press Enter on 'Log in' (or 'a' for API key).", action.Label))
	}

	switch action.Key {
	case "create":
		cm := newCreateModel(m.apiKey, m.devMode, m.client, m.cfg, m.width, m.height)
		m.createModel = &cm
		m.currentView = viewCreateTest
		return m, m.createModel.Init()

	case "apps":
		m.currentView = viewAppList
		m.appCursor = 0
		m.appsLoading = true
		if m.client != nil {
			return m, fetchAppsCmd(m.client)
		}
		return m, nil

	case "tests":
		m.currentView = viewTestList
		m.testCursor = 0
		m.filteredTests = nil
		m.filterInput.SetValue("")
		return m, nil

	case "workflows":
		m.currentView = viewWorkflowList
		m.wfCursor = 0
		m.wfListLoading = true
		m.wfFilterMode = false
		m.wfFilterInput.SetValue("")
		if m.client != nil {
			return m, fetchWorkflowBrowseListCmd(m.client)
		}
		return m, nil

	case "modules":
		m.currentView = viewModuleList
		m.moduleCursor = 0
		m.moduleLoading = true
		if m.client != nil {
			return m, fetchModulesCmd(m.client)
		}
		return m, nil

	case "tags":
		m.currentView = viewTagList
		m.tagCursor = 0
		m.tagLoading = true
		if m.client != nil {
			return m, fetchTagsCmd(m.client)
		}
		return m, nil

	case "devices":
		m.currentView = viewDeviceList
		m.deviceCursor = 0
		m.devicesLoading = true
		if m.client != nil {
			return m, fetchDeviceSessionsCmd(m.client)
		}
		return m, nil

	case "dev_loop":
		return m, runDevLoopProcessCmd(m.devMode)

	case "publish_testflight":
		cfg := loadCurrentProjectConfig()
		pm := newPublishTestFlightModel(cfg, m.width, m.height)
		m.publishTFModel = &pm
		m.currentView = viewPublishTestFlight
		return m, m.publishTFModel.Init()

	case "settings":
		cwd, err := os.Getwd()
		if err != nil {
			m.settingsConfigPath = ".revyl/config.yaml"
		} else {
			m.settingsConfigPath = filepath.Join(cwd, ".revyl", "config.yaml")
		}

		cfg := loadCurrentProjectConfig()
		if cfg != nil {
			m.cfg = cfg
			m.settingsOpenBrowser = config.EffectiveOpenBrowser(cfg)
			m.settingsTimeout = config.EffectiveTimeoutSeconds(cfg, config.DefaultTimeoutSeconds)
			m.settingsStatus = ""
			m.settingsStatusError = false
		} else {
			m.settingsOpenBrowser = config.DefaultOpenBrowser
			m.settingsTimeout = config.DefaultTimeoutSeconds
			m.settingsStatus = "Project config not found. Run 'revyl init' to create .revyl/config.yaml."
			m.settingsStatusError = true
		}
		m.settingsCursor = 0
		m.settingsEditing = false
		m.settingsTimeoutInput.SetValue(fmt.Sprintf("%d", m.settingsTimeout))
		m.settingsTimeoutInput.Blur()
		m.currentView = viewSettings
		return m, nil

	case "dashboard":
		dashURL := config.GetAppURL(m.devMode)
		_ = ui.OpenBrowser(dashURL)
		return m, nil
	}
	return m, nil
}

func runDevLoopProcessCmd(devMode bool) tea.Cmd {
	return tea.ExecProcess(devLoopExecCmd(devMode), func(err error) tea.Msg {
		return DevLoopDoneMsg{Err: err}
	})
}

func devLoopExecCmd(devMode bool) *exec.Cmd {
	exe, err := os.Executable()
	if err != nil {
		exe = "revyl"
	}
	args := []string{"dev"}
	if devMode {
		args = append([]string{"--dev"}, args...)
	}
	return exec.Command(exe, args...)
}

// handleTestListKey processes key events in the test list sub-screen.
func (m hubModel) handleTestListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.testListConfirmDelete {
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "y", "Y":
			target := m.testListDeleteTarget
			m.testListConfirmDelete = false
			m.testListDeleteTarget = TestItem{}
			m.loading = true
			m.err = nil
			if m.client != nil {
				return m, tea.Batch(m.spinner.Tick, deleteTestFromListCmd(m.client, target))
			}
			m.loading = false
			m.err = fmt.Errorf("not authenticated")
			return m, nil
		case "n", "N", "esc":
			m.testListConfirmDelete = false
			m.testListDeleteTarget = TestItem{}
			return m, nil
		default:
			return m, nil
		}
	}

	if m.filterMode {
		switch msg.String() {
		case "esc":
			m.filterMode = false
			m.filterInput.Blur()
			m.filterInput.SetValue("")
			m.filteredTests = nil
			return m, nil
		case "enter":
			m.filterMode = false
			m.filterInput.Blur()
			return m, nil
		default:
			var cmd tea.Cmd
			m.filterInput, cmd = m.filterInput.Update(msg)
			m.applyFilter()
			return m, cmd
		}
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "esc":
		m.currentView = viewDashboard
		m.filteredTests = nil
		m.filterInput.SetValue("")
		m.testListConfirmDelete = false
		m.testListDeleteTarget = TestItem{}
		return m, nil

	case "up", "k":
		if m.testCursor > 0 {
			m.testCursor--
		}

	case "down", "j":
		maxIdx := len(m.visibleTests()) - 1
		if m.testCursor < maxIdx {
			m.testCursor++
		}

	case "enter":
		tests := m.visibleTests()
		if len(tests) == 0 || m.testCursor >= len(tests) {
			break
		}
		selected := tests[m.testCursor]
		if selected.ID == "" {
			m.err = fmt.Errorf("test '%s' has no remote ID yet; run 'revyl sync' to reconcile", selected.Name)
			return m, nil
		}
		// Browse mode: navigate to test detail screen
		m.currentView = viewTestDetail
		m.testDetailLoading = true
		m.testDetailCursor = 0
		if m.client != nil {
			return m, fetchTestDetailCmd(m.client, selected.ID, selected.Name, selected.Platform, m.devMode)
		}

	case "/":
		m.filterMode = true
		m.filterInput.Focus()
		return m, textinput.Blink

	case "r":
		tests := m.visibleTests()
		if len(tests) > 0 && m.testCursor < len(tests) {
			selected := tests[m.testCursor]
			if m.apiKey != "" {
				if selected.ID == "" {
					m.err = fmt.Errorf("test '%s' has no remote ID and cannot be run yet", selected.Name)
					return m, nil
				}
				em := newExecutionModel(selected.ID, selected.Name, m.apiKey, m.cfg, m.devMode, m.width, m.height)
				m.executionModel = &em
				m.currentView = viewExecution
				return m, m.executionModel.Init()
			}
		}

	case "x", "X":
		tests := m.visibleTests()
		if len(tests) == 0 || m.testCursor >= len(tests) {
			return m, nil
		}
		m.testListConfirmDelete = true
		m.testListDeleteTarget = tests[m.testCursor]
		return m, nil

	case "s":
		// quick status refresh for selected test list
		m.loading = true
		m.err = nil
		if m.client != nil {
			return m, tea.Batch(m.spinner.Tick, fetchTestsCmd(m.client))
		}
		return m, tea.Batch(m.spinner.Tick, authenticateCmd(m.devMode))

	case "R":
		m.loading = true
		m.err = nil
		if m.client != nil {
			return m, tea.Batch(m.spinner.Tick, fetchTestsCmd(m.client))
		}
		return m, tea.Batch(m.spinner.Tick, authenticateCmd(m.devMode))

	case "S":
		// full refresh entry-point from test list (mirrors sync intent in TUI)
		m.loading = true
		m.err = nil
		if m.client != nil {
			return m, tea.Batch(m.spinner.Tick, fetchTestsCmd(m.client))
		}
		return m, tea.Batch(m.spinner.Tick, authenticateCmd(m.devMode))
	}

	return m, nil
}

// updateExecution delegates messages to the execution model and handles navigation back.
func (m hubModel) updateExecution(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "esc" {
		if m.executionModel != nil && m.executionModel.done {
			m.currentView = viewDashboard
			m.executionModel = nil
			// Refresh recent runs after execution using cached client
			if m.client != nil {
				return m, fetchRecentRunsCmd(m.client, m.tests, 5)
			}
			return m, nil
		}
	}

	if wsMsg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = wsMsg.Width
		m.height = wsMsg.Height
		if m.executionModel != nil {
			m.executionModel.width = wsMsg.Width
			m.executionModel.height = wsMsg.Height
		}
	}

	if m.executionModel != nil {
		em, cmd := m.executionModel.Update(msg)
		execModel := em.(executionModel)
		m.executionModel = &execModel
		return m, cmd
	}

	return m, nil
}

// updateCreate delegates messages to the create model and handles navigation back.
func (m hubModel) updateCreate(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "esc" {
		if m.createModel != nil && !m.createModel.creating {
			m.currentView = viewDashboard
			m.createModel = nil
			return m, nil
		}
	}

	if wsMsg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = wsMsg.Width
		m.height = wsMsg.Height
	}

	if m.createModel != nil {
		cm, cmd := m.createModel.Update(msg)
		createMdl := cm.(createModel)
		m.createModel = &createMdl

		// If creation completed and user chose to run, transition to execution
		if m.createModel.done && m.createModel.runAfter {
			testID := m.createModel.createdID
			testName := m.createModel.nameInput.Value()
			m.createModel = nil
			// Add to test list
			m.tests = append([]TestItem{{ID: testID, Name: testName}}, m.tests...)
			em := newExecutionModel(testID, testName, m.apiKey, m.cfg, m.devMode, m.width, m.height)
			m.executionModel = &em
			m.currentView = viewExecution
			return m, m.executionModel.Init()
		}

		// If creation completed and user chose to go back
		if m.createModel.done && !m.createModel.runAfter {
			testID := m.createModel.createdID
			testName := m.createModel.nameInput.Value()
			platform := m.createModel.platforms[m.createModel.platformCursor]
			m.createModel = nil
			if testID != "" {
				m.tests = append([]TestItem{{ID: testID, Name: testName, Platform: platform}}, m.tests...)
			}
			m.currentView = viewDashboard
			return m, nil
		}

		return m, cmd
	}

	return m, nil
}

// updateCreateApp delegates messages to the create-app model and handles navigation back.
func (m hubModel) updateCreateApp(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "esc" {
		if m.createAppModel != nil && !m.createAppModel.creating {
			m.currentView = viewAppList
			m.createAppModel = nil
			return m, nil
		}
	}

	if wsMsg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = wsMsg.Width
		m.height = wsMsg.Height
	}

	if m.createAppModel != nil {
		cm, cmd := m.createAppModel.Update(msg)
		appMdl := cm.(createAppModel)
		m.createAppModel = &appMdl

		// If creation completed and user chose "View builds"
		if m.createAppModel.done && m.createAppModel.viewBuilds {
			appID := m.createAppModel.createdID
			appName := m.createAppModel.createdName
			if appName == "" {
				appName = strings.TrimSpace(m.createAppModel.nameInput.Value())
			}
			m.createAppModel = nil
			m.selectedAppID = appID
			m.selectedAppName = appName
			m.appBuildCursor = 0
			m.appBuilds = nil
			m.appsLoading = true
			m.currentView = viewAppDetail
			if m.client != nil {
				return m, tea.Batch(fetchAppBuildsCmd(m.client, appID), fetchAppsCmd(m.client))
			}
			return m, nil
		}

		// If creation completed and user chose "Back to apps"
		if m.createAppModel.done && !m.createAppModel.viewBuilds {
			m.createAppModel = nil
			m.appsLoading = true
			m.currentView = viewAppList
			if m.client != nil {
				return m, fetchAppsCmd(m.client)
			}
			return m, nil
		}

		return m, cmd
	}

	return m, nil
}

// updateUploadBuild delegates messages to the upload-build model and handles navigation back.
func (m hubModel) updateUploadBuild(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "esc" {
		if m.uploadBuildModel != nil && !m.uploadBuildModel.uploading {
			m.currentView = viewAppDetail
			m.uploadBuildModel = nil
			return m, nil
		}
	}

	if wsMsg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = wsMsg.Width
		m.height = wsMsg.Height
	}

	if m.uploadBuildModel != nil {
		um, cmd := m.uploadBuildModel.Update(msg)
		uploadMdl := um.(uploadBuildModel)
		m.uploadBuildModel = &uploadMdl

		// If upload completed and user chose "Upload another"
		if m.uploadBuildModel.done && m.uploadBuildModel.uploadMore {
			appID := m.uploadBuildModel.appID
			appName := m.uploadBuildModel.appName
			m.uploadBuildModel = nil
			newUM := newUploadBuildModel(m.client, appID, appName, m.width, m.height)
			m.uploadBuildModel = &newUM
			return m, m.uploadBuildModel.Init()
		}

		// If upload completed and user chose "Back to builds"
		if m.uploadBuildModel.done && !m.uploadBuildModel.uploadMore {
			m.uploadBuildModel = nil
			m.appsLoading = true
			m.appBuilds = nil
			m.currentView = viewAppDetail
			if m.client != nil {
				return m, fetchAppBuildsCmd(m.client, m.selectedAppID)
			}
			return m, nil
		}

		return m, cmd
	}

	return m, nil
}

// updatePublishTestFlight delegates messages to the publish-testflight model.
func (m hubModel) updatePublishTestFlight(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "esc" {
		if m.publishTFModel != nil && !m.publishTFModel.running {
			m.currentView = viewDashboard
			m.publishTFModel = nil
			return m, nil
		}
	}

	if wsMsg, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = wsMsg.Width
		m.height = wsMsg.Height
	}

	if m.publishTFModel != nil {
		pm, cmd := m.publishTFModel.Update(msg)
		publishMdl := pm.(publishTestFlightModel)
		m.publishTFModel = &publishMdl

		if m.publishTFModel.done && m.publishTFModel.runAgain {
			cfg := loadCurrentProjectConfig()
			m.publishTFModel = nil
			newPM := newPublishTestFlightModel(cfg, m.width, m.height)
			m.publishTFModel = &newPM
			return m, m.publishTFModel.Init()
		}

		if m.publishTFModel.done && !m.publishTFModel.runAgain {
			m.currentView = viewDashboard
			m.publishTFModel = nil
			return m, nil
		}

		return m, cmd
	}

	return m, nil
}

// --- Helpers ---

// visibleTests returns the filtered test list, or all tests if no filter is active.
func (m *hubModel) visibleTests() []TestItem {
	if m.filteredTests != nil {
		return m.filteredTests
	}
	return m.tests
}

// applyFilter filters the test list based on the current filter input value.
func (m *hubModel) applyFilter() {
	query := strings.ToLower(m.filterInput.Value())
	if query == "" {
		m.filteredTests = nil
		return
	}

	var filtered []TestItem
	for _, t := range m.tests {
		if strings.Contains(strings.ToLower(t.Name), query) ||
			strings.Contains(strings.ToLower(t.Platform), query) {
			filtered = append(filtered, t)
		}
	}
	m.filteredTests = filtered

	if m.testCursor >= len(filtered) {
		m.testCursor = max(0, len(filtered)-1)
	}
}

// deriveTestSource returns a compact source label for a test entry.
func deriveTestSource(cfg *config.ProjectConfig, localTests map[string]*config.LocalTest, name string) string {
	hasCfg := cfg != nil && cfg.Tests[name] != ""
	_, hasLocal := localTests[name]
	switch {
	case hasCfg && hasLocal:
		return "config+local"
	case hasCfg:
		return "config"
	case hasLocal:
		return "local"
	default:
		return "remote"
	}
}

func (m hubModel) isAuthenticated() bool {
	return m.client != nil && m.apiKey != ""
}

func (m hubModel) requireAuthentication(reason string) (tea.Model, tea.Cmd) {
	m.currentView = viewHelp
	m.healthLoading = true
	m.healthChecks = nil
	m.err = nil
	m.authErr = fmt.Errorf("%s", reason)
	return m, runHealthChecksCmd(m.devMode, m.client)
}

func (m hubModel) isLoginOnlyState() bool {
	if len(m.setupSteps) != 1 {
		return false
	}
	step := m.setupSteps[0]
	return step.Label == "Log in" && (step.Status == "current" || step.Status == "hint")
}

func loadCurrentProjectConfig() *config.ProjectConfig {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	cfg, err := config.LoadProjectConfig(configPath)
	if err != nil {
		return nil
	}
	return cfg
}

// relativeTime formats a timestamp as a human-readable relative time string.
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		mins := int(d.Minutes())
		if mins == 1 {
			return "1m ago"
		}
		return fmt.Sprintf("%dm ago", mins)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return fmt.Sprintf("%dd ago", days)
	}
}

// statusIcon returns a styled icon for a given execution status string.
func statusIcon(status string) string {
	s := strings.ToLower(status)
	switch {
	case s == "passed" || s == "completed" || s == "success":
		return successStyle.Render("✓")
	case s == "failed" || s == "error":
		return errorStyle.Render("✗")
	case s == "running" || s == "active":
		return runningStyle.Render("●")
	case s == "queued" || s == "pending":
		return warningStyle.Render("⏳")
	case s == "cancelled" || s == "timeout":
		return warningStyle.Render("⊘")
	default:
		return dimStyle.Render("·")
	}
}

// --- View rendering ---

// View renders the current screen.
func (m hubModel) View() string {
	switch m.currentView {
	case viewExecution:
		if m.executionModel != nil {
			return m.executionModel.View()
		}
	case viewCreateTest:
		if m.createModel != nil {
			return m.createModel.View()
		}
	case viewTestList:
		return m.renderTestList()
	case viewRunsList:
		return m.renderRunsList()
	case viewTestRuns:
		return m.renderTestRuns()
	case viewWorkflowRuns:
		return m.renderWorkflowRuns()
	case viewAppList:
		return m.renderAppList()
	case viewAppDetail:
		return m.renderAppDetail()
	case viewCreateApp:
		if m.createAppModel != nil {
			return m.createAppModel.View()
		}
	case viewUploadBuild:
		if m.uploadBuildModel != nil {
			return m.uploadBuildModel.View()
		}
	case viewPublishTestFlight:
		if m.publishTFModel != nil {
			return m.publishTFModel.View()
		}
	case viewSettings:
		return m.renderSettings()
	case viewHelp:
		return m.renderHelp()
	case viewTestDetail:
		return renderTestDetail(m)
	case viewWorkflowList:
		return renderWorkflowList(m)
	case viewWorkflowDetail:
		return renderWorkflowDetail(m)
	case viewWorkflowCreate:
		return renderWorkflowCreate(m)
	case viewWorkflowExecution:
		return renderWorkflowExecution(m)
	case viewModuleList:
		return renderModuleList(m)
	case viewModuleDetail:
		return renderModuleDetail(m)
	case viewTagList:
		return renderTagList(m)
	case viewDeviceList:
		return m.renderDeviceList()
	case viewDeviceDetail:
		return m.renderDeviceDetail()
	}
	return m.renderDashboard()
}

// renderDashboard renders the dashboard landing page with a styled header banner,
// colorful stats, recent runs table, and quick actions with descriptions.
func (m hubModel) renderDashboard() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	// Header banner with rounded border
	bannerContent := titleStyle.Render("REVYL") + "  " + versionStyle.Render("v"+m.version)
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	if m.err != nil {
		b.WriteString("\n" + errorStyle.Render("  ✗ "+m.err.Error()) + "\n\n")
		b.WriteString("  " + strings.Join([]string{helpKeyRender("R", "refresh"), helpKeyRender("?", "help"), helpKeyRender("q", "quit")}, "  ") + "\n")
		return b.String()
	}
	if m.authErr != nil && !m.isAuthenticated() {
		b.WriteString("\n" + warningStyle.Render("  ⚠ "+m.authErr.Error()) + "\n")
		b.WriteString("  " + dimStyle.Render("Press ? for setup, then Enter for browser login or 'a' for API key login.") + "\n")
	}

	if m.loading {
		b.WriteString("\n  " + m.spinner.View() + " Loading...\n")
		return b.String()
	}

	// Stats
	b.WriteString(sectionStyle.Render("  STATS") + "\n")
	b.WriteString("  " + separator(innerW) + "\n")
	b.WriteString(m.renderStats())

	// Recent runs
	if m.focus == focusRecent {
		b.WriteString(activeSectionStyle.Render("  RECENT RUNS") + "\n")
	} else {
		b.WriteString(sectionStyle.Render("  RECENT RUNS") + "\n")
	}
	b.WriteString("  " + separator(innerW) + "\n")
	b.WriteString(m.renderRecentRuns())

	// Quick actions
	if m.focus == focusActions {
		b.WriteString(activeSectionStyle.Render("  QUICK ACTIONS") + "\n")
	} else {
		b.WriteString(sectionStyle.Render("  QUICK ACTIONS") + "\n")
	}
	b.WriteString("  " + separator(innerW) + "\n")
	for i, a := range quickActions {
		cur := "  "
		labelStyle := normalStyle
		descStyle := actionDescStyle
		disabled := a.RequiresAuth && !m.isAuthenticated()
		isSelected := m.focus == focusActions && i == m.actionCursor
		if m.focus == focusActions && i == m.actionCursor {
			cur = selectedStyle.Render("▸ ")
		}
		num := dimStyle.Render(fmt.Sprintf("[%d] ", i+1))
		label := a.Label
		if disabled {
			label = label + " (login required)"
			descStyle = dimStyle
			if isSelected {
				labelStyle = warningStyle
			} else {
				labelStyle = dimStyle
			}
		} else if isSelected {
			labelStyle = selectedStyle
		}
		desc := descStyle.Render("  " + a.Desc)
		b.WriteString("  " + cur + num + labelStyle.Render(label) + desc + "\n")
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	jumpLabel := "1-9"
	if len(quickActions) >= 10 {
		jumpLabel = "1-9,0"
	}
	keys := []string{
		helpKeyRender("enter", "select"),
		helpKeyRender("tab", "section"),
		helpKeyRender(jumpLabel, "jump"),
		helpKeyRender("/", "search"),
		helpKeyRender("R", "refresh"),
		helpKeyRender("?", "help"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")
	return b.String()
}

func (m hubModel) renderSettings() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	bannerContent := titleStyle.Render("REVYL") + "  " + versionStyle.Render("v"+m.version)
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	b.WriteString(sectionStyle.Render("  SETTINGS") + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	configPath := m.settingsConfigPath
	if configPath == "" {
		configPath = ".revyl/config.yaml"
	}
	b.WriteString("  " + dimStyle.Render(configPath) + "\n\n")

	items := []string{"open_browser", "timeout", "Save settings"}
	for i := range items {
		cur := "  "
		if i == m.settingsCursor {
			cur = selectedStyle.Render("▸ ")
		}

		switch i {
		case 0:
			b.WriteString("  " + cur + dimStyle.Render("open_browser: "))
			b.WriteString(normalStyle.Render(fmt.Sprintf("%t", m.settingsOpenBrowser)) + "\n")
		case 1:
			b.WriteString("  " + cur + dimStyle.Render("timeout:      "))
			if m.settingsEditing {
				b.WriteString(m.settingsTimeoutInput.View() + "\n")
			} else {
				b.WriteString(normalStyle.Render(fmt.Sprintf("%d", m.settingsTimeout)) + "\n")
			}
		case 2:
			b.WriteString("  " + cur + normalStyle.Render("Save settings") + "\n")
		}
	}

	if m.settingsStatus != "" {
		b.WriteString("\n")
		if m.settingsStatusError {
			b.WriteString("  " + errorStyle.Render("✗ "+m.settingsStatus) + "\n")
		} else {
			b.WriteString("  " + successStyle.Render("✓ "+m.settingsStatus) + "\n")
		}
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	keys := []string{
		helpKeyRender("enter", "toggle/edit/save"),
		helpKeyRender("s", "save"),
		helpKeyRender("esc", "back"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")
	return b.String()
}

// renderStats renders the metrics summary row with colored values and WoW deltas.
func (m hubModel) renderStats() string {
	if m.metrics == nil {
		return "  " + dimStyle.Render("Loading metrics...") + "\n"
	}
	mt := m.metrics
	parts := []string{
		metricRender("Tests", fmt.Sprintf("%d", mt.TotalTests), mt.TotalTestsWow),
		metricRender("Workflows", fmt.Sprintf("%d", mt.TotalWorkflows), mt.TotalWorkflowsWow),
		metricRender("Runs", fmt.Sprintf("%d", mt.TestRuns), mt.TestRunsWow),
		metricRenderFail("Fail", fmt.Sprintf("%.0f%%", mt.TestsFailingPercent), mt.TestsFailingPercent, mt.TestsFailingPercentWow),
	}
	if mt.AvgTestDuration != nil {
		parts = append(parts, metricRender("Avg", fmt.Sprintf("%.0fs", *mt.AvgTestDuration), mt.AvgTestDurationWow))
	}
	return "  " + strings.Join(parts, "    ") + "\n"
}

// metricRender formats a metric label/value pair with an optional WoW delta arrow.
//
// Parameters:
//   - label: the metric name (e.g. "Tests")
//   - value: the formatted metric value (e.g. "69")
//   - wow: optional week-over-week percentage change (nil = no delta shown)
//
// Returns:
//   - string: the styled metric string
func metricRender(label, value string, wow *float32) string {
	out := dimStyle.Render(label+" ") + metricValueStyle.Render(value)
	if wow != nil && *wow != 0 {
		out += " " + wowDelta(*wow)
	}
	return out
}

// metricRenderFail formats the failure metric with conditional coloring.
// Values above 20% are red, above 10% amber, otherwise green.
//
// Parameters:
//   - label: the metric name
//   - value: the formatted value string
//   - pct: the raw percentage for color thresholds
//   - wow: optional WoW delta
//
// Returns:
//   - string: the styled metric string
func metricRenderFail(label, value string, pct float32, wow *float32) string {
	var valStyle lipgloss.Style
	switch {
	case pct > 20:
		valStyle = lipgloss.NewStyle().Foreground(red).Bold(true)
	case pct > 10:
		valStyle = lipgloss.NewStyle().Foreground(amber).Bold(true)
	default:
		valStyle = lipgloss.NewStyle().Foreground(green).Bold(true)
	}
	out := dimStyle.Render(label+" ") + valStyle.Render(value)
	if wow != nil && *wow != 0 {
		out += " " + wowDelta(*wow)
	}
	return out
}

// wowDelta renders a week-over-week percentage change with a colored arrow.
// Positive values are shown in teal with an up arrow, negative in red with a down arrow.
//
// Parameters:
//   - delta: the percentage change value
//
// Returns:
//   - string: the styled delta string (e.g. "^5%" or "v3%")
func wowDelta(delta float32) string {
	if delta > 0 {
		return wowPositiveStyle.Render(fmt.Sprintf("↑%.0f%%", delta))
	}
	return wowNegativeStyle.Render(fmt.Sprintf("↓%.0f%%", -delta))
}

// renderRecentRuns renders the recent runs section with fixed-width columns for
// icon, test name, status, and relative time.
func (m hubModel) renderRecentRuns() string {
	if len(m.recentRuns) == 0 {
		return "  " + dimStyle.Render("No recent runs") + "\n"
	}
	var b strings.Builder
	nameW := 32
	statusW := 12
	for i, r := range m.recentRuns {
		icon := statusIcon(r.Status)
		cur := "  "
		name := r.TestName
		if len(name) > nameW {
			name = name[:nameW-1] + "…"
		}

		isSelected := m.focus == focusRecent && i == m.recentRunCursor
		if isSelected {
			cur = selectedStyle.Render("▸ ")
			name = selectedRowStyle.Render(fmt.Sprintf("%-*s", nameW, name))
		} else {
			name = normalStyle.Render(fmt.Sprintf("%-*s", nameW, name))
		}

		status := statusStyle(r.Status).Render(fmt.Sprintf("%-*s", statusW, r.Status))
		ago := dimStyle.Render(relativeTime(r.Time))
		b.WriteString(fmt.Sprintf("  %s%s  %s  %s  %s\n", cur, icon, name, status, ago))
	}
	return b.String()
}

// statusStyle returns a lipgloss.Style appropriate for the given execution status.
//
// Parameters:
//   - status: the execution status string (e.g. "passed", "failed", "running")
//
// Returns:
//   - lipgloss.Style: a colored style for rendering the status text
func statusStyle(status string) lipgloss.Style {
	s := strings.ToLower(status)
	switch {
	case s == "passed" || s == "completed" || s == "success":
		return successStyle
	case s == "failed" || s == "error":
		return errorStyle
	case s == "running" || s == "active":
		return runningStyle
	case s == "queued" || s == "pending":
		return warningStyle
	case s == "cancelled" || s == "timeout":
		return warningStyle
	default:
		return dimStyle
	}
}

// renderTestList renders the test list sub-screen.
func (m hubModel) renderTestList() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	subtitle := "Browse tests"
	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render(subtitle)
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	if m.filterMode {
		b.WriteString("\n  " + filterPromptStyle.Render("/") + " " + m.filterInput.View() + "\n")
	}

	tests := m.visibleTests()
	countLabel := fmt.Sprintf("%d", len(tests))
	if m.filteredTests != nil {
		countLabel = fmt.Sprintf("%d/%d", len(m.filteredTests), len(m.tests))
	}
	b.WriteString(sectionStyle.Render("  Tests") + " " + dimStyle.Render(countLabel) + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	if len(tests) == 0 {
		if m.filteredTests != nil {
			b.WriteString(dimStyle.Render("  No tests match filter\n"))
		} else {
			b.WriteString(dimStyle.Render("  No tests found. Press esc to go back.\n"))
		}
	} else {
		maxVisible := m.height - 12
		if maxVisible < 5 {
			maxVisible = 5
		}
		start := 0
		if len(tests) > maxVisible {
			start = m.testCursor - maxVisible/2
			if start < 0 {
				start = 0
			}
			if start+maxVisible > len(tests) {
				start = len(tests) - maxVisible
			}
		}
		end := start + maxVisible
		if end > len(tests) {
			end = len(tests)
		}
		if start > 0 {
			b.WriteString(dimStyle.Render("  ↑ more") + "\n")
		}
		for i := start; i < end; i++ {
			t := tests[i]
			cur := "  "
			nameStyle := normalStyle
			if i == m.testCursor {
				cur = selectedStyle.Render("▸ ")
				nameStyle = selectedRowStyle
			}
			platBadge := ""
			if t.Platform != "" {
				platBadge = platformStyle.Render(" [" + t.Platform + "]")
			}
			syncState := t.SyncStatus
			if syncState == "" {
				syncState = "unknown"
			}
			syncBadge := dimStyle.Render(" " + syncStatusStyle(syncState).Render(syncState))
			sourceBadge := ""
			if t.Source != "" {
				sourceBadge = dimStyle.Render(" (" + t.Source + ")")
			}
			if t.RemoteMissing {
				sourceBadge += warningStyle.Render(" [missing-upstream]")
			}
			b.WriteString("  " + cur + syncStatusIcon(syncState) + " " + nameStyle.Render(t.Name) + platBadge + syncBadge + sourceBadge + "\n")
		}
		if end < len(tests) {
			b.WriteString(dimStyle.Render("  ↓ more") + "\n")
		}
	}

	if m.testListConfirmDelete {
		name := m.testListDeleteTarget.Name
		b.WriteString("\n  " + errorStyle.Render(fmt.Sprintf("Delete test %q? (y/n)", name)) + "\n")
		b.WriteString("  " + dimStyle.Render("Deletes remote + local artifacts when available.") + "\n")
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	var keys []string
	if m.testListConfirmDelete {
		keys = []string{
			helpKeyRender("y", "confirm"),
			helpKeyRender("n", "cancel"),
			helpKeyRender("esc", "cancel"),
			helpKeyRender("q", "quit"),
		}
	} else {
		keys = []string{
			helpKeyRender("enter", "detail"),
			helpKeyRender("r", "run"),
			helpKeyRender("x", "delete"),
			helpKeyRender("/", "filter"),
			helpKeyRender("S", "refresh"),
			helpKeyRender("esc", "back"),
			helpKeyRender("q", "quit"),
		}
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")
	return b.String()
}

// --- Runs list sub-screen ---

// handleRunsListKey processes key events in the runs list sub-screen.
func (m hubModel) handleRunsListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "esc":
		m.currentView = viewDashboard
		return m, nil

	case "up", "k":
		if m.allRunsCursor > 0 {
			m.allRunsCursor--
		}

	case "down", "j":
		if m.allRunsCursor < len(m.allRuns)-1 {
			m.allRunsCursor++
		}

	case "enter":
		if len(m.allRuns) > 0 && m.allRunsCursor < len(m.allRuns) {
			run := m.allRuns[m.allRunsCursor]
			if run.TaskID != "" {
				reportURL := fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(m.devMode), run.TaskID)
				_ = ui.OpenBrowser(reportURL)
			}
		}

	case "R":
		m.allRunsLoaded = false
		m.allRuns = nil
		if m.client != nil {
			return m, fetchAllRunsCmd(m.client, m.tests)
		}
	}

	return m, nil
}

// renderRunsList renders the full runs list sub-screen with scrollable entries.
func (m hubModel) renderRunsList() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render("All runs")
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	countLabel := fmt.Sprintf("%d", len(m.allRuns))
	b.WriteString(sectionStyle.Render("  Runs") + " " + dimStyle.Render(countLabel) + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	if !m.allRunsLoaded {
		b.WriteString("  " + dimStyle.Render("Loading runs...") + "\n")
	} else if len(m.allRuns) == 0 {
		b.WriteString("  " + dimStyle.Render("No runs found") + "\n")
	} else {
		nameW := 32
		statusW := 12
		maxVisible := m.height - 10
		if maxVisible < 5 {
			maxVisible = 5
		}
		start := 0
		if len(m.allRuns) > maxVisible {
			start = m.allRunsCursor - maxVisible/2
			if start < 0 {
				start = 0
			}
			if start+maxVisible > len(m.allRuns) {
				start = len(m.allRuns) - maxVisible
			}
		}
		end := start + maxVisible
		if end > len(m.allRuns) {
			end = len(m.allRuns)
		}
		if start > 0 {
			b.WriteString(dimStyle.Render("  ↑ more") + "\n")
		}
		for i := start; i < end; i++ {
			r := m.allRuns[i]
			icon := statusIcon(r.Status)
			cur := "  "
			name := r.TestName
			if len(name) > nameW {
				name = name[:nameW-1] + "…"
			}
			isSelected := i == m.allRunsCursor
			if isSelected {
				cur = selectedStyle.Render("▸ ")
				name = selectedRowStyle.Render(fmt.Sprintf("%-*s", nameW, name))
			} else {
				name = normalStyle.Render(fmt.Sprintf("%-*s", nameW, name))
			}
			status := statusStyle(r.Status).Render(fmt.Sprintf("%-*s", statusW, r.Status))
			ago := dimStyle.Render(relativeTime(r.Time))
			dur := ""
			if r.Duration != "" {
				dur = dimStyle.Render(" " + r.Duration)
			}
			b.WriteString(fmt.Sprintf("  %s%s  %s  %s  %s%s\n", cur, icon, name, status, ago, dur))
		}
		if end < len(m.allRuns) {
			b.WriteString(dimStyle.Render("  ↓ more") + "\n")
		}
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	keys := []string{
		helpKeyRender("enter", "open report"),
		helpKeyRender("R", "refresh"),
		helpKeyRender("esc", "back"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")
	return b.String()
}

// helpKeyRender formats a key hint like "enter run test".
func helpKeyRender(key, desc string) string {
	return lipgloss.NewStyle().Foreground(purple).Bold(true).Render(key) +
		" " + helpStyle.Render(desc)
}

// actionNumberIndex converts a numeric key string (1-based) to a zero-based action index.
func actionNumberIndex(key string, actionCount int) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(key))
	if err != nil || n < 1 || n > actionCount {
		return 0, false
	}
	return n - 1, true
}

// handleTestRunsKey processes key events on the test runs list.
func (m hubModel) handleTestRunsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		returnTo := m.reportReturnView
		if returnTo == viewDashboard {
			returnTo = viewTestDetail
		}
		m.currentView = returnTo
		return m, nil
	case "up", "k":
		if m.testRunCursor > 0 {
			m.testRunCursor--
		}
	case "down", "j":
		if m.testRunCursor < len(m.testRuns)-1 {
			m.testRunCursor++
		}
	case "enter", "o":
		reportURL := m.selectedTestRunReportURL()
		if reportURL != "" {
			_ = ui.OpenBrowser(reportURL)
		}
	case "R":
		if m.client != nil && m.selectedTestID != "" {
			m.reportLoading = true
			m.testRuns = nil
			return m, fetchTestHistoryCmd(m.client, m.selectedTestID)
		}
	}
	return m, nil
}

func (m hubModel) selectedTestRunReportURL() string {
	if len(m.testRuns) == 0 || m.testRunCursor < 0 || m.testRunCursor >= len(m.testRuns) {
		return ""
	}
	runID := strings.TrimSpace(m.testRuns[m.testRunCursor].ID)
	if runID == "" {
		return ""
	}
	return fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(m.devMode), runID)
}

// renderTestRuns renders the run history list for a specific test.
func (m hubModel) renderTestRuns() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render(m.selectedTestName)
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	b.WriteString(sectionStyle.Render("  Runs") + " " + dimStyle.Render(fmt.Sprintf("%d", len(m.testRuns))) + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	if m.reportLoading {
		b.WriteString("  " + m.spinner.View() + " Loading runs...\n")
	} else if len(m.testRuns) == 0 {
		b.WriteString("  " + dimStyle.Render("No runs found") + "\n")
	} else {
		nameW := 14
		statusW := 12
		maxVisible := m.height - 10
		if maxVisible < 5 {
			maxVisible = 5
		}
		start, end := scrollWindow(m.testRunCursor, len(m.testRuns), maxVisible)
		if start > 0 {
			b.WriteString(dimStyle.Render("  ↑ more") + "\n")
		}
		for i := start; i < end; i++ {
			r := m.testRuns[i]
			icon := statusIcon(r.Status)
			cur := "  "
			isSelected := i == m.testRunCursor
			if isSelected {
				cur = selectedStyle.Render("▸ ")
			}
			var ts time.Time
			if r.ExecutionTime != "" {
				ts, _ = time.Parse(time.RFC3339, r.ExecutionTime)
			}
			dur := ""
			if r.Duration != nil {
				dur = fmt.Sprintf("%.0fs", *r.Duration)
			}
			status := statusStyle(r.Status).Render(fmt.Sprintf("%-*s", statusW, r.Status))
			durStr := dimStyle.Render(fmt.Sprintf("%-*s", nameW, dur))
			ago := dimStyle.Render(relativeTime(ts))
			b.WriteString(fmt.Sprintf("  %s%s  %s  %s  %s\n", cur, icon, status, durStr, ago))
		}
		if end < len(m.testRuns) {
			b.WriteString(dimStyle.Render("  ↓ more") + "\n")
		}
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	if reportURL := m.selectedTestRunReportURL(); reportURL != "" {
		b.WriteString("  " + dimStyle.Render(truncateText("link: "+reportURL, innerW)) + "\n")
	}
	keys := []string{
		helpKeyRender("enter/o", "open report"),
		helpKeyRender("R", "refresh"),
		helpKeyRender("esc", "back"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")
	return b.String()
}

// handleWorkflowRunsKey processes key events on the workflow runs list.
func (m hubModel) handleWorkflowRunsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		returnTo := m.reportReturnView
		if returnTo == viewDashboard {
			returnTo = viewWorkflowDetail
		}
		m.currentView = returnTo
		return m, nil
	case "up", "k":
		if m.workflowRunCursor > 0 {
			m.workflowRunCursor--
		}
	case "down", "j":
		if m.workflowRunCursor < len(m.workflowRuns)-1 {
			m.workflowRunCursor++
		}
	case "enter", "o":
		reportURL := m.selectedWorkflowRunReportURL()
		if reportURL != "" {
			_ = ui.OpenBrowser(reportURL)
		}
	case "R":
		if m.client != nil && m.selectedWorkflowID != "" {
			m.reportLoading = true
			m.workflowRuns = nil
			return m, fetchWorkflowHistoryCmd(m.client, m.selectedWorkflowID)
		}
	}
	return m, nil
}

func (m hubModel) selectedWorkflowRunReportURL() string {
	if len(m.workflowRuns) == 0 || m.workflowRunCursor < 0 || m.workflowRunCursor >= len(m.workflowRuns) {
		return ""
	}
	run := m.workflowRuns[m.workflowRunCursor]
	taskID := strings.TrimSpace(run.ExecutionID)
	if taskID == "" {
		taskID = strings.TrimSpace(run.WorkflowID)
	}
	if taskID == "" {
		return ""
	}
	return fmt.Sprintf("%s/workflows/report?taskId=%s", config.GetAppURL(m.devMode), taskID)
}

func truncateText(value string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(value) <= maxLen {
		return value
	}
	if maxLen <= 3 {
		return value[:maxLen]
	}
	return value[:maxLen-3] + "..."
}

// renderWorkflowRuns renders the run history list for a specific workflow.
func (m hubModel) renderWorkflowRuns() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render(m.selectedWorkflowName)
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	b.WriteString(sectionStyle.Render("  Runs") + " " + dimStyle.Render(fmt.Sprintf("%d", len(m.workflowRuns))) + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	if m.reportLoading {
		b.WriteString("  " + m.spinner.View() + " Loading runs...\n")
	} else if len(m.workflowRuns) == 0 {
		b.WriteString("  " + dimStyle.Render("No runs found") + "\n")
	} else {
		statusW := 12
		maxVisible := m.height - 10
		if maxVisible < 5 {
			maxVisible = 5
		}
		start, end := scrollWindow(m.workflowRunCursor, len(m.workflowRuns), maxVisible)
		if start > 0 {
			b.WriteString(dimStyle.Render("  ↑ more") + "\n")
		}
		for i := start; i < end; i++ {
			r := m.workflowRuns[i]
			icon := statusIcon(r.Status)
			cur := "  "
			if i == m.workflowRunCursor {
				cur = selectedStyle.Render("▸ ")
			}
			status := statusStyle(r.Status).Render(fmt.Sprintf("%-*s", statusW, r.Status))
			progress := dimStyle.Render(fmt.Sprintf("%d/%d tests", r.CompletedTests, r.TotalTests))
			dur := ""
			if r.Duration != "" {
				dur = dimStyle.Render("  " + r.Duration)
			}
			var ts time.Time
			if r.StartedAt != "" {
				ts, _ = time.Parse(time.RFC3339, r.StartedAt)
			}
			ago := dimStyle.Render(relativeTime(ts))
			b.WriteString(fmt.Sprintf("  %s%s  %s  %s%s  %s\n", cur, icon, status, progress, dur, ago))
		}
		if end < len(m.workflowRuns) {
			b.WriteString(dimStyle.Render("  ↓ more") + "\n")
		}
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	if reportURL := m.selectedWorkflowRunReportURL(); reportURL != "" {
		b.WriteString("  " + dimStyle.Render(truncateText("link: "+reportURL, innerW)) + "\n")
	}
	keys := []string{
		helpKeyRender("enter/o", "open report"),
		helpKeyRender("R", "refresh"),
		helpKeyRender("esc", "back"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")
	return b.String()
}

// --- Manage apps ---

// handleAppListKey processes key events on the app list screen.
func (m hubModel) handleAppListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirmDelete {
		switch msg.String() {
		case "y", "Y":
			filtered := m.filteredApps()
			if m.appCursor < len(filtered) && m.client != nil {
				m.appsLoading = true
				m.confirmDelete = false
				return m, deleteAppCmd(m.client, filtered[m.appCursor].ID)
			}
		default:
			m.confirmDelete = false
			m.deleteTarget = ""
		}
		return m, nil
	}

	if m.reportFilterMode {
		switch msg.String() {
		case "esc":
			m.reportFilterMode = false
			m.reportFilterInput.Blur()
			m.reportFilterInput.SetValue("")
			return m, nil
		case "enter":
			m.reportFilterMode = false
			m.reportFilterInput.Blur()
			return m, nil
		default:
			var cmd tea.Cmd
			m.reportFilterInput, cmd = m.reportFilterInput.Update(msg)
			return m, cmd
		}
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.currentView = viewDashboard
		m.reportFilterInput.SetValue("")
		return m, nil
	case "up", "k":
		if m.appCursor > 0 {
			m.appCursor--
		}
	case "down", "j":
		filtered := m.filteredApps()
		if m.appCursor < len(filtered)-1 {
			m.appCursor++
		}
	case "enter":
		filtered := m.filteredApps()
		if len(filtered) > 0 && m.appCursor < len(filtered) {
			selected := filtered[m.appCursor]
			m.selectedAppID = selected.ID
			m.selectedAppName = selected.Name
			m.appBuildCursor = 0
			m.appBuilds = nil
			m.appsLoading = true
			m.currentView = viewAppDetail
			if m.client != nil {
				return m, fetchAppBuildsCmd(m.client, selected.ID)
			}
		}
	case "d":
		filtered := m.filteredApps()
		if len(filtered) > 0 && m.appCursor < len(filtered) {
			m.confirmDelete = true
			m.deleteTarget = "app"
		}
	case "/":
		m.reportFilterMode = true
		m.reportFilterInput.Focus()
		return m, textinput.Blink
	case "R":
		if m.client != nil {
			m.appsLoading = true
			m.apps = nil
			return m, fetchAppsCmd(m.client)
		}
	case "c":
		cam := newCreateAppModel(m.client, m.width, m.height)
		m.createAppModel = &cam
		m.currentView = viewCreateApp
		return m, m.createAppModel.Init()
	}
	return m, nil
}

// filteredApps returns apps filtered by the report filter input.
func (m *hubModel) filteredApps() []api.App {
	query := strings.ToLower(m.reportFilterInput.Value())
	if query == "" {
		return m.apps
	}
	var filtered []api.App
	for _, a := range m.apps {
		if strings.Contains(strings.ToLower(a.Name), query) ||
			strings.Contains(strings.ToLower(a.Platform), query) {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

// handleAppDetailKey processes key events on the app detail (build versions) screen.
func (m hubModel) handleAppDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirmDelete {
		switch msg.String() {
		case "y", "Y":
			if m.appBuildCursor < len(m.appBuilds) && m.client != nil {
				m.appsLoading = true
				m.confirmDelete = false
				return m, deleteBuildCmd(m.client, m.appBuilds[m.appBuildCursor].ID)
			}
		default:
			m.confirmDelete = false
			m.deleteTarget = ""
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.currentView = viewAppList
		m.confirmDelete = false
		return m, nil
	case "up", "k":
		if m.appBuildCursor > 0 {
			m.appBuildCursor--
		}
	case "down", "j":
		if m.appBuildCursor < len(m.appBuilds)-1 {
			m.appBuildCursor++
		}
	case "d":
		if len(m.appBuilds) > 0 && m.appBuildCursor < len(m.appBuilds) {
			m.confirmDelete = true
			m.deleteTarget = m.appBuilds[m.appBuildCursor].ID
		}
	case "R":
		if m.client != nil && m.selectedAppID != "" {
			m.appsLoading = true
			m.appBuilds = nil
			return m, fetchAppBuildsCmd(m.client, m.selectedAppID)
		}
	case "u":
		um := newUploadBuildModel(m.client, m.selectedAppID, m.selectedAppName, m.width, m.height)
		m.uploadBuildModel = &um
		m.currentView = viewUploadBuild
		return m, m.uploadBuildModel.Init()
	case "p":
		cfg := loadCurrentProjectConfig()
		pm := newPublishTestFlightModel(cfg, m.width, m.height)
		m.publishTFModel = &pm
		m.currentView = viewPublishTestFlight
		return m, m.publishTFModel.Init()
	}
	return m, nil
}

// renderAppList renders the app list screen.
func (m hubModel) renderAppList() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render("Manage apps")
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	if m.reportFilterMode {
		b.WriteString("\n  " + filterPromptStyle.Render("/") + " " + m.reportFilterInput.View() + "\n")
	}

	apps := m.filteredApps()
	countLabel := fmt.Sprintf("%d", len(apps))
	if m.reportFilterInput.Value() != "" && len(m.apps) > 0 {
		countLabel = fmt.Sprintf("%d/%d", len(apps), len(m.apps))
	}
	b.WriteString(sectionStyle.Render("  Apps") + " " + dimStyle.Render(countLabel) + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	if m.confirmDelete {
		b.WriteString("\n  " + warningStyle.Render("Delete this app? (y/n)") + "\n\n")
	}

	if m.appsLoading {
		b.WriteString("  " + m.spinner.View() + " Loading apps...\n")
	} else if len(apps) == 0 {
		b.WriteString("  " + dimStyle.Render("No apps found") + "\n")
	} else {
		maxVisible := m.height - 12
		if maxVisible < 5 {
			maxVisible = 5
		}
		start, end := scrollWindow(m.appCursor, len(apps), maxVisible)
		if start > 0 {
			b.WriteString(dimStyle.Render("  ↑ more") + "\n")
		}
		for i := start; i < end; i++ {
			a := apps[i]
			cur := "  "
			nameStyle := normalStyle
			if i == m.appCursor {
				cur = selectedStyle.Render("▸ ")
				nameStyle = selectedRowStyle
			}
			platBadge := ""
			if a.Platform != "" {
				platBadge = platformStyle.Render(" [" + a.Platform + "]")
			}
			versions := dimStyle.Render(fmt.Sprintf("  %d versions", a.VersionsCount))
			b.WriteString("  " + cur + nameStyle.Render(a.Name) + platBadge + versions + "\n")
		}
		if end < len(apps) {
			b.WriteString(dimStyle.Render("  ↓ more") + "\n")
		}
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	keys := []string{
		helpKeyRender("enter", "view builds"),
		helpKeyRender("c", "create"),
		helpKeyRender("d", "delete"),
		helpKeyRender("/", "filter"),
		helpKeyRender("R", "refresh"),
		helpKeyRender("esc", "back"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")
	return b.String()
}

// renderAppDetail renders the build versions list for a specific app.
func (m hubModel) renderAppDetail() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render(m.selectedAppName)
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	b.WriteString(sectionStyle.Render("  Build Versions") + " " + dimStyle.Render(fmt.Sprintf("%d", len(m.appBuilds))) + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	if m.confirmDelete {
		b.WriteString("\n  " + warningStyle.Render("Delete this build? (y/n)") + "\n\n")
	}

	if m.appsLoading {
		b.WriteString("  " + m.spinner.View() + " Loading builds...\n")
	} else if len(m.appBuilds) == 0 {
		b.WriteString("  " + dimStyle.Render("No builds found") + "\n")
	} else {
		maxVisible := m.height - 10
		if maxVisible < 5 {
			maxVisible = 5
		}
		start, end := scrollWindow(m.appBuildCursor, len(m.appBuilds), maxVisible)
		if start > 0 {
			b.WriteString(dimStyle.Render("  ↑ more") + "\n")
		}
		for i := start; i < end; i++ {
			bv := m.appBuilds[i]
			cur := "  "
			nameStyle := normalStyle
			if i == m.appBuildCursor {
				cur = selectedStyle.Render("▸ ")
				nameStyle = selectedRowStyle
			}
			version := nameStyle.Render(bv.Version)
			currentBadge := ""
			if bv.IsCurrent {
				currentBadge = successStyle.Render(" (current)")
			}
			var ts time.Time
			if bv.UploadedAt != "" {
				ts, _ = time.Parse(time.RFC3339, bv.UploadedAt)
			}
			ago := dimStyle.Render("  " + relativeTime(ts))
			b.WriteString("  " + cur + version + currentBadge + ago + "\n")
		}
		if end < len(m.appBuilds) {
			b.WriteString(dimStyle.Render("  ↓ more") + "\n")
		}
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	keys := []string{
		helpKeyRender("u", "upload"),
		helpKeyRender("p", "publish"),
		helpKeyRender("d", "delete"),
		helpKeyRender("R", "refresh"),
		helpKeyRender("esc", "back"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")
	return b.String()
}

// --- Help & Status screen ---

// runHealthChecksCmd runs diagnostic checks asynchronously and returns results.
//
// Parameters:
//   - devMode: whether development servers are in use
//   - client: authenticated API client (may be nil)
//
// Returns:
//   - tea.Cmd: command that produces a HealthCheckMsg
func runHealthChecksCmd(devMode bool, client *api.Client) tea.Cmd {
	return func() tea.Msg {
		var checks []HealthCheck

		// Check 1: Authentication
		mgr := auth.NewManager()
		creds, err := mgr.GetCredentials()
		if err != nil || creds == nil || !creds.HasValidAuth() {
			checks = append(checks, HealthCheck{
				Name:    "Authentication",
				Status:  "error",
				Message: "Not authenticated — Enter for browser login, or 'a' for API key login",
			})
			// Keep the unauthenticated help screen focused on login only.
			return HealthCheckMsg{Checks: checks}
		} else {
			msg := "Authenticated"
			if creds.Email != "" {
				msg = fmt.Sprintf("Authenticated as %s", creds.Email)
			}
			checks = append(checks, HealthCheck{
				Name:    "Authentication",
				Status:  "ok",
				Message: msg,
			})
		}

		// Check 2: API connectivity
		baseURL := config.GetBackendURL(devMode)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, reqErr := http.NewRequestWithContext(ctx, "GET", baseURL+"/health_check", nil)
		if reqErr != nil {
			checks = append(checks, HealthCheck{
				Name:    "API Connection",
				Status:  "error",
				Message: "Failed to create request",
			})
		} else {
			httpClient := &http.Client{Timeout: 5 * time.Second}
			start := time.Now()
			resp, httpErr := httpClient.Do(req)
			latency := time.Since(start)
			if httpErr != nil {
				checks = append(checks, HealthCheck{
					Name:    "API Connection",
					Status:  "error",
					Message: fmt.Sprintf("Connection failed: %v", httpErr),
				})
			} else {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					checks = append(checks, HealthCheck{
						Name:    "API Connection",
						Status:  "ok",
						Message: fmt.Sprintf("Connected (latency: %dms)", latency.Milliseconds()),
					})
				} else {
					checks = append(checks, HealthCheck{
						Name:    "API Connection",
						Status:  "warning",
						Message: fmt.Sprintf("Unexpected status: %d", resp.StatusCode),
					})
				}
			}
		}

		// Check 3: Project config
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			checks = append(checks, HealthCheck{
				Name:    "Project Config",
				Status:  "warning",
				Message: "Could not determine working directory",
			})
		} else {
			configPath := filepath.Join(cwd, ".revyl", "config.yaml")
			cfg, cfgErr := config.LoadProjectConfig(configPath)
			if cfgErr != nil {
				checks = append(checks, HealthCheck{
					Name:    "Project Config",
					Status:  "warning",
					Message: "No project config — run 'revyl init'",
				})
			} else {
				parts := []string{}
				if cfg.Project.Name != "" {
					parts = append(parts, cfg.Project.Name)
				}
				if len(cfg.Tests) > 0 {
					parts = append(parts, fmt.Sprintf("%d tests", len(cfg.Tests)))
				}
				msg := "Found"
				if len(parts) > 0 {
					msg = strings.Join(parts, ", ")
				}
				checks = append(checks, HealthCheck{
					Name:    "Project Config",
					Status:  "ok",
					Message: msg,
				})
			}
		}

		// Check 4: Build system
		if cwdErr == nil {
			detected, detectErr := build.Detect(cwd)
			if detectErr != nil || detected.System == build.SystemUnknown {
				checks = append(checks, HealthCheck{
					Name:    "Build System",
					Status:  "warning",
					Message: "Not detected",
				})
			} else {
				checks = append(checks, HealthCheck{
					Name:    "Build System",
					Status:  "ok",
					Message: fmt.Sprintf("Detected: %s", detected.System.String()),
				})
			}
		}

		// Check 5: App linked
		var appLinked bool
		if cwdErr == nil {
			configPath := filepath.Join(cwd, ".revyl", "config.yaml")
			cfg, cfgErr := config.LoadProjectConfig(configPath)
			if cfgErr == nil && cfg != nil {
				for _, p := range cfg.Build.Platforms {
					if p.AppID != "" {
						appLinked = true
						break
					}
				}
			}
		}
		if appLinked {
			checks = append(checks, HealthCheck{
				Name:    "App Linked",
				Status:  "ok",
				Message: "App linked",
			})
		} else {
			checks = append(checks, HealthCheck{
				Name:    "App Linked",
				Status:  "warning",
				Message: "No app linked — set app_id in config or use 'Manage apps'",
			})
		}

		// Check 6: Build uploaded
		if appLinked && client != nil {
			configPath := filepath.Join(cwd, ".revyl", "config.yaml")
			cfg, cfgErr := config.LoadProjectConfig(configPath)
			if cfgErr == nil && cfg != nil {
				buildFound := false
				for _, p := range cfg.Build.Platforms {
					if p.AppID != "" {
						bCtx, bCancel := context.WithTimeout(context.Background(), 5*time.Second)
						versions, bErr := client.ListBuildVersions(bCtx, p.AppID)
						bCancel()
						if bErr == nil && len(versions) > 0 {
							buildFound = true
							break
						}
					}
				}
				if buildFound {
					checks = append(checks, HealthCheck{
						Name:    "Build Uploaded",
						Status:  "ok",
						Message: "Build available",
					})
				} else {
					checks = append(checks, HealthCheck{
						Name:    "Build Uploaded",
						Status:  "warning",
						Message: "No builds — run 'revyl build upload'",
					})
				}
			}
		} else if !appLinked {
			checks = append(checks, HealthCheck{
				Name:    "Build Uploaded",
				Status:  "warning",
				Message: "Requires app link first",
			})
		}

		// Check 7: ASC credentials
		storeMgr := store.NewManager()
		if hasASCCredentialsInEnv() || storeMgr.ValidateIOSCredentials() == nil {
			checks = append(checks, HealthCheck{
				Name:    "ASC Credentials",
				Status:  "ok",
				Message: "Configured for TestFlight publishing",
			})
		} else {
			checks = append(checks, HealthCheck{
				Name:    "ASC Credentials",
				Status:  "warning",
				Message: "Not configured — use 'Publish to TestFlight' or 'revyl publish auth ios'",
			})
		}

		// Check 8: Tests configured
		if cwdErr == nil {
			configPath := filepath.Join(cwd, ".revyl", "config.yaml")
			cfg, cfgErr := config.LoadProjectConfig(configPath)
			if cfgErr == nil && cfg != nil && len(cfg.Tests) > 0 {
				checks = append(checks, HealthCheck{
					Name:    "Tests Configured",
					Status:  "ok",
					Message: fmt.Sprintf("%d tests configured", len(cfg.Tests)),
				})
			} else {
				checks = append(checks, HealthCheck{
					Name:    "Tests Configured",
					Status:  "warning",
					Message: "No tests — use 'Create a test' or 'revyl test create'",
				})
			}
		}

		return HealthCheckMsg{Checks: checks}
	}
}

// handleHelpKey processes key events on the help & status screen.
func (m hubModel) handleHelpKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.currentView = viewDashboard
		return m, nil
	case "R":
		m.healthLoading = true
		m.healthChecks = nil
		return m, runHealthChecksCmd(m.devMode, m.client)
	case "up", "k":
		if len(m.setupSteps) > 0 {
			m.setupCursor = prevActionableStep(m.setupSteps, m.setupCursor)
		}
	case "down", "j":
		if len(m.setupSteps) > 0 {
			m.setupCursor = nextActionableStep(m.setupSteps, m.setupCursor)
		}
	case "enter":
		if len(m.setupSteps) > 0 && m.setupCursor < len(m.setupSteps) {
			updated, cmd := executeSetupStep(m, m.setupSteps, m.setupCursor)
			return updated, cmd
		}
	case "a", "A":
		for _, step := range m.setupSteps {
			if step.Label == "Log in" && (step.Status == "current" || step.Status == "hint") {
				m.returnToDashboardAfterAuth = true
				return m, tea.ExecProcess(authLoginCmd(true), func(err error) tea.Msg {
					return SetupActionMsg{StepIndex: 0, Err: err}
				})
			}
		}
	}
	return m, nil
}

// renderHelp renders the help & status screen with health checks, setup guide, and keybindings.
func (m hubModel) renderHelp() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	subtitle := "Help & Status"
	if m.isLoginOnlyState() {
		subtitle = "Getting Started"
	}
	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render(subtitle)
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	if m.isLoginOnlyState() {
		b.WriteString(sectionStyle.Render("  SIGN IN TO CONTINUE") + "\n")
		b.WriteString("  " + separator(innerW) + "\n")
		if m.healthLoading {
			b.WriteString("  " + m.spinner.View() + " Checking authentication...\n")
		} else {
			b.WriteString("  " + normalStyle.Render("Use browser login for the fastest setup.") + "\n\n")
			b.WriteString("  " + selectedStyle.Render("▸ enter") + " " + normalStyle.Render("Continue with browser login") + "\n")
			b.WriteString("  " + selectedStyle.Render("▸ a") + " " + normalStyle.Render("Use API key login (SSH/headless)") + "\n")
		}
		if m.authErr != nil {
			b.WriteString("\n" + warningStyle.Render("  ⚠ "+m.authErr.Error()) + "\n")
		}
		b.WriteString("\n  " + separator(innerW) + "\n")
		keys := []string{
			helpKeyRender("enter", "browser login"),
			helpKeyRender("a", "api key"),
			helpKeyRender("R", "refresh"),
			helpKeyRender("q", "quit"),
		}
		b.WriteString("  " + strings.Join(keys, "  ") + "\n")
		return b.String()
	}

	// Health checks section
	b.WriteString(sectionStyle.Render("  HEALTH CHECKS") + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	if m.healthLoading {
		b.WriteString("  " + m.spinner.View() + " Running checks...\n")
	} else if len(m.healthChecks) == 0 {
		b.WriteString("  " + dimStyle.Render("No checks run yet") + "\n")
	} else {
		for _, check := range m.healthChecks {
			var icon string
			switch check.Status {
			case "ok":
				icon = successStyle.Render("✓")
			case "warning":
				icon = warningStyle.Render("⚠")
			case "error":
				icon = errorStyle.Render("✗")
			default:
				icon = dimStyle.Render("·")
			}
			name := dimStyle.Render(fmt.Sprintf("%-18s", check.Name))
			b.WriteString(fmt.Sprintf("  %s %s %s\n", icon, name, normalStyle.Render(check.Message)))
		}

		// Render setup guide when checks are loaded
		guide := renderSetupGuide(m.setupSteps, m.setupCursor, innerW)
		if guide != "" {
			b.WriteString("\n")
			b.WriteString(guide)
		}
	}

	// Keyboard shortcuts section
	b.WriteString(sectionStyle.Render("  KEYBOARD SHORTCUTS") + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	shortcuts := []struct{ key, desc string }{
		{"enter", "Select / drill in / open"},
		{"a", "API key login (on Log in step)"},
		{"esc", "Go back one level"},
		{"tab", "Switch dashboard section"},
		{"1-9", "Jump to quick action"},
		{"/", "Filter / search lists"},
		{"R", "Refresh current view"},
		{"x", "Delete tests/workflows (where shown)"},
		{"d", "Delete in app/module/tag views"},
		{"?", "This help screen"},
		{"q", "Quit"},
	}
	for _, s := range shortcuts {
		key := lipgloss.NewStyle().Foreground(purple).Bold(true).Width(8).Render(s.key)
		b.WriteString(fmt.Sprintf("  %s %s\n", key, dimStyle.Render(s.desc)))
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	keys := []string{
		helpKeyRender("R", "re-run checks"),
		helpKeyRender("esc", "back"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")
	return b.String()
}

// scrollWindow computes the visible window [start, end) for a scrollable list.
//
// Parameters:
//   - cursor: current cursor position
//   - total: total number of items
//   - maxVisible: max items to show at once
//
// Returns:
//   - start: first visible index (inclusive)
//   - end: last visible index (exclusive)
func scrollWindow(cursor, total, maxVisible int) (int, int) {
	if total <= maxVisible {
		return 0, total
	}
	start := cursor - maxVisible/2
	if start < 0 {
		start = 0
	}
	if start+maxVisible > total {
		start = total - maxVisible
	}
	end := start + maxVisible
	if end > total {
		end = total
	}
	return start, end
}

// min returns the smaller of two ints.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// max returns the larger of two ints.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
