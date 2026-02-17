// Package tui provides the hub model -- the dashboard-first TUI with quick actions.
package tui

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
	{Label: "Run a test", Key: "run", Desc: "Execute an existing test", RequiresAuth: true},
	{Label: "Create a test", Key: "create", Desc: "Define a new test from YAML", RequiresAuth: true},
	{Label: "Browse tests", Key: "tests", Desc: "View, sync, and manage tests", RequiresAuth: true},
	{Label: "Browse workflows", Key: "workflows", Desc: "Create, run, and manage workflows", RequiresAuth: true},
	{Label: "Run a workflow", Key: "run_workflow", Desc: "Execute an existing workflow", RequiresAuth: true},
	{Label: "View reports", Key: "reports", Desc: "Browse test & workflow reports", RequiresAuth: true},
	{Label: "Manage apps", Key: "apps", Desc: "List, upload, and delete builds", RequiresAuth: true},
	{Label: "Browse modules", Key: "modules", Desc: "View reusable test modules", RequiresAuth: true},
	{Label: "Browse tags", Key: "tags", Desc: "Manage test tags and labels", RequiresAuth: true},
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
	tests         []TestItem
	testCursor    int
	filterMode    bool
	filterInput   textinput.Model
	filteredTests []TestItem
	testBrowse    bool // true when entered from "View all tests" (browse mode, not run mode)

	// Runs list (sub-screen)
	allRuns       []RecentRun
	allRunsCursor int
	allRunsLoaded bool

	// Report drill-down state
	reportTypeCursor     int                             // 0 = test reports, 1 = workflow reports
	workflows            []api.SimpleWorkflow            // cached workflow list
	workflowCursor       int                             // cursor for workflow list
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
	// Workflow create wizard
	wfCreateStep          workflowCreateStep
	wfCreateNameInput     textinput.Model
	wfCreateTestCursor    int
	wfCreateSelectedTests map[int]bool
	// Workflow execution monitor
	wfExecTaskID    string
	wfExecStatus    *api.CLIWorkflowStatusResponse
	wfExecDone      bool
	wfExecStartTime time.Time
	wfRunMode       bool // true when entered from "Run a workflow" quick action (run mode, not browse mode)

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

	// Sub-models
	executionModel *executionModel
	createModel    *createModel
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

	return hubModel{
		version:           version,
		currentView:       viewDashboard,
		loading:           true,
		spinner:           newSpinner(),
		filterInput:       ti,
		reportFilterInput: rfi,
		envVarKeyInput:    eki,
		envVarValueInput:  evi,
		tagNameInput:      tni,
		wfCreateNameInput: wfni,
		wfFilterInput:     wffi,
		devMode:           devMode,
	}
}

// --- Tea commands for async operations ---

// AuthMsg carries the authenticated client and token after initial auth succeeds.
type AuthMsg struct {
	Client *api.Client
	Token  string
	Err    error
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

		resp, err := client.ListOrgTests(ctx, 100, 0)
		if err != nil {
			return TestListMsg{Err: fmt.Errorf("failed to fetch tests: %w", err)}
		}

		items := make([]TestItem, len(resp.Tests))
		for i, t := range resp.Tests {
			items[i] = TestItem{ID: t.ID, Name: t.Name, Platform: t.Platform}
		}

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

// fetchWorkflowsCmd fetches the org workflow list.
//
// Parameters:
//   - client: authenticated API client
//
// Returns:
//   - tea.Cmd: command that produces a WorkflowListMsg
func fetchWorkflowsCmd(client *api.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := client.ListWorkflows(ctx)
		if err != nil {
			return WorkflowListMsg{Err: fmt.Errorf("failed to fetch workflows: %w", err)}
		}
		return WorkflowListMsg{Workflows: resp.Workflows}
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

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.currentView != viewDashboard && m.currentView != viewHelp && !m.isAuthenticated() {
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
		if m.currentView == viewReportPicker {
			return m.handleReportPickerKey(msg)
		}
		if m.currentView == viewTestReports {
			return m.handleTestReportsKey(msg)
		}
		if m.currentView == viewTestRuns {
			return m.handleTestRunsKey(msg)
		}
		if m.currentView == viewWorkflowReports {
			return m.handleWorkflowReportsKey(msg)
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
			// Derive setup steps from health check results
			m.setupSteps = deriveSetupSteps(msg.Checks, m.cfg)
			if first := firstActionableStep(m.setupSteps); first >= 0 && m.setupCursor == 0 {
				m.setupCursor = first
			}
		}
		return m, nil

	case WorkflowListMsg:
		m.reportLoading = false
		if msg.Err == nil {
			m.workflows = msg.Workflows
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
		if msg.Err == nil {
			m.currentView = viewTestList
			m.selectedTestDetail = nil
			// Refresh test list
			if m.client != nil {
				return m, fetchTestsCmd(m.client)
			}
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
		m.testBrowse = false
		m.filterMode = true
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
	case "run":
		m.currentView = viewTestList
		m.testCursor = 0
		m.testBrowse = false
		m.filteredTests = nil
		m.filterInput.SetValue("")
		return m, nil

	case "create":
		cm := newCreateModel(m.apiKey, m.devMode, m.client, m.cfg, m.width, m.height)
		m.createModel = &cm
		m.currentView = viewCreateTest
		return m, m.createModel.Init()

	case "reports":
		m.currentView = viewReportPicker
		m.reportTypeCursor = 0
		return m, nil

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
		m.testBrowse = true
		m.filteredTests = nil
		m.filterInput.SetValue("")
		return m, nil

	case "workflows":
		m.currentView = viewWorkflowList
		m.wfCursor = 0
		m.wfListLoading = true
		m.wfFilterMode = false
		m.wfFilterInput.SetValue("")
		m.wfRunMode = false
		if m.client != nil {
			return m, fetchWorkflowBrowseListCmd(m.client)
		}
		return m, nil

	case "run_workflow":
		m.currentView = viewWorkflowList
		m.wfCursor = 0
		m.wfListLoading = true
		m.wfFilterMode = false
		m.wfFilterInput.SetValue("")
		m.wfRunMode = true
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

	case "dashboard":
		dashURL := config.GetAppURL(m.devMode)
		_ = ui.OpenBrowser(dashURL)
		return m, nil
	}
	return m, nil
}

// handleTestListKey processes key events in the test list sub-screen.
func (m hubModel) handleTestListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		m.testBrowse = false
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
		if m.testBrowse {
			// Browse mode: navigate to test detail screen
			m.currentView = viewTestDetail
			m.testDetailLoading = true
			m.testDetailCursor = 0
			if m.client != nil {
				return m, fetchTestDetailCmd(m.client, selected.ID, selected.Name, selected.Platform, m.devMode)
			}
		} else if m.apiKey != "" {
			// Run mode: start execution
			em := newExecutionModel(selected.ID, selected.Name, m.apiKey, m.cfg, m.devMode, m.width, m.height)
			m.executionModel = &em
			m.currentView = viewExecution
			return m, m.executionModel.Init()
		}

	case "/":
		m.filterMode = true
		m.filterInput.Focus()
		return m, textinput.Blink

	case "r":
		tests := m.visibleTests()
		if len(tests) > 0 && m.testCursor < len(tests) {
			selected := tests[m.testCursor]
			if m.testBrowse {
				// In browse mode, r triggers execution
				if m.apiKey != "" {
					em := newExecutionModel(selected.ID, selected.Name, m.apiKey, m.cfg, m.devMode, m.width, m.height)
					m.executionModel = &em
					m.currentView = viewExecution
					return m, m.executionModel.Init()
				}
			} else {
				// In run mode, r opens in browser
				reportURL := fmt.Sprintf("%s/tests/execute?testUid=%s", config.GetAppURL(m.devMode), selected.ID)
				_ = ui.OpenBrowser(reportURL)
			}
		}

	case "R":
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
	case viewReportPicker:
		return m.renderReportPicker()
	case viewTestReports:
		return m.renderTestReports()
	case viewTestRuns:
		return m.renderTestRuns()
	case viewWorkflowReports:
		return m.renderWorkflowReports()
	case viewWorkflowRuns:
		return m.renderWorkflowRuns()
	case viewAppList:
		return m.renderAppList()
	case viewAppDetail:
		return m.renderAppDetail()
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
		b.WriteString("\n" + errorStyle.Render("  ✗ "+m.err.Error()) + "\n")
	}
	if m.authErr != nil {
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
		style := normalStyle
		disabled := a.RequiresAuth && !m.isAuthenticated()
		if m.focus == focusActions && i == m.actionCursor {
			cur = selectedStyle.Render("▸ ")
			style = selectedStyle
		}
		num := dimStyle.Render(fmt.Sprintf("[%d] ", i+1))
		descStyle := actionDescStyle
		label := a.Label
		if disabled {
			label = label + " (login required)"
			descStyle = dimStyle
			if m.focus == focusActions && i == m.actionCursor {
				style = warningStyle
			} else {
				style = dimStyle
			}
		}
		desc := descStyle.Render("  " + a.Desc)
		b.WriteString("  " + cur + num + style.Render(label) + desc + "\n")
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	jumpLabel := fmt.Sprintf("1-%d", len(quickActions))
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
// In browse mode (testBrowse=true), enter opens the report and r runs the test.
// In run mode (testBrowse=false), enter runs the test and r opens the report.
func (m hubModel) renderTestList() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	subtitle := "Select a test to run"
	if m.testBrowse {
		subtitle = "Browse all tests"
	}
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
			b.WriteString("  " + cur + nameStyle.Render(t.Name) + platBadge + "\n")
		}
		if end < len(tests) {
			b.WriteString(dimStyle.Render("  ↓ more") + "\n")
		}
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	var keys []string
	if m.testBrowse {
		keys = []string{
			helpKeyRender("enter", "view"),
			helpKeyRender("r", "run"),
			helpKeyRender("/", "filter"),
			helpKeyRender("esc", "back"),
			helpKeyRender("q", "quit"),
		}
	} else {
		keys = []string{
			helpKeyRender("enter", "run"),
			helpKeyRender("/", "filter"),
			helpKeyRender("r", "open in browser"),
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

// --- Report picker ---

// reportTypeOptions defines the report type picker items.
var reportTypeOptions = []string{"Test reports", "Workflow reports"}

// handleReportPickerKey processes key events on the report type picker screen.
func (m hubModel) handleReportPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.currentView = viewDashboard
		return m, nil
	case "up", "k":
		if m.reportTypeCursor > 0 {
			m.reportTypeCursor--
		}
	case "down", "j":
		if m.reportTypeCursor < len(reportTypeOptions)-1 {
			m.reportTypeCursor++
		}
	case "1":
		m.reportTypeCursor = 0
		return m.selectReportType()
	case "2":
		m.reportTypeCursor = 1
		return m.selectReportType()
	case "enter":
		return m.selectReportType()
	}
	return m, nil
}

// selectReportType transitions to the selected report type sub-view.
func (m hubModel) selectReportType() (tea.Model, tea.Cmd) {
	if m.reportTypeCursor == 0 {
		m.currentView = viewTestReports
		m.testCursor = 0
		m.reportFilterMode = false
		m.reportFilterInput.SetValue("")
		return m, nil
	}
	m.currentView = viewWorkflowReports
	m.workflowCursor = 0
	m.reportLoading = true
	m.reportFilterMode = false
	m.reportFilterInput.SetValue("")
	if m.client != nil {
		return m, fetchWorkflowsCmd(m.client)
	}
	return m, nil
}

// renderReportPicker renders the report type selection screen.
func (m hubModel) renderReportPicker() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render("View reports")
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	b.WriteString(sectionStyle.Render("  Report Type") + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	for i, opt := range reportTypeOptions {
		cur := "  "
		style := normalStyle
		if i == m.reportTypeCursor {
			cur = selectedStyle.Render("▸ ")
			style = selectedStyle
		}
		num := dimStyle.Render(fmt.Sprintf("[%d] ", i+1))
		b.WriteString("  " + cur + num + style.Render(opt) + "\n")
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	keys := []string{
		helpKeyRender("enter", "select"),
		helpKeyRender("1-2", "jump"),
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

// --- Test reports drill-down ---

// handleTestReportsKey processes key events on the test reports list.
func (m hubModel) handleTestReportsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		m.currentView = viewReportPicker
		m.reportFilterInput.SetValue("")
		return m, nil
	case "up", "k":
		if m.testCursor > 0 {
			m.testCursor--
		}
	case "down", "j":
		filtered := m.filteredReportTests()
		if m.testCursor < len(filtered)-1 {
			m.testCursor++
		}
	case "enter":
		filtered := m.filteredReportTests()
		if len(filtered) > 0 && m.testCursor < len(filtered) {
			selected := filtered[m.testCursor]
			m.selectedTestID = selected.ID
			m.selectedTestName = selected.Name
			m.testRunCursor = 0
			m.testRuns = nil
			m.reportLoading = true
			m.currentView = viewTestRuns
			if m.client != nil {
				return m, fetchTestHistoryCmd(m.client, selected.ID)
			}
		}
	case "/":
		m.reportFilterMode = true
		m.reportFilterInput.Focus()
		return m, textinput.Blink
	case "R":
		if m.client != nil {
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, fetchTestsCmd(m.client))
		}
	}
	return m, nil
}

// filteredReportTests returns tests filtered by the report filter input.
func (m *hubModel) filteredReportTests() []TestItem {
	query := strings.ToLower(m.reportFilterInput.Value())
	if query == "" {
		return m.tests
	}
	var filtered []TestItem
	for _, t := range m.tests {
		if strings.Contains(strings.ToLower(t.Name), query) ||
			strings.Contains(strings.ToLower(t.Platform), query) {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// handleTestRunsKey processes key events on the test runs list.
func (m hubModel) handleTestRunsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.currentView = viewTestReports
		return m, nil
	case "up", "k":
		if m.testRunCursor > 0 {
			m.testRunCursor--
		}
	case "down", "j":
		if m.testRunCursor < len(m.testRuns)-1 {
			m.testRunCursor++
		}
	case "enter":
		if len(m.testRuns) > 0 && m.testRunCursor < len(m.testRuns) {
			run := m.testRuns[m.testRunCursor]
			reportURL := fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(m.devMode), run.ID)
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

// renderTestReports renders the test list for the report drill-down.
func (m hubModel) renderTestReports() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render("Test reports")
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	if m.reportFilterMode {
		b.WriteString("\n  " + filterPromptStyle.Render("/") + " " + m.reportFilterInput.View() + "\n")
	}

	tests := m.filteredReportTests()
	countLabel := fmt.Sprintf("%d", len(tests))
	if m.reportFilterInput.Value() != "" {
		countLabel = fmt.Sprintf("%d/%d", len(tests), len(m.tests))
	}
	b.WriteString(sectionStyle.Render("  Tests") + " " + dimStyle.Render(countLabel) + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	if len(tests) == 0 {
		b.WriteString("  " + dimStyle.Render("No tests found") + "\n")
	} else {
		maxVisible := m.height - 12
		if maxVisible < 5 {
			maxVisible = 5
		}
		start, end := scrollWindow(m.testCursor, len(tests), maxVisible)
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
			b.WriteString("  " + cur + nameStyle.Render(t.Name) + platBadge + "\n")
		}
		if end < len(tests) {
			b.WriteString(dimStyle.Render("  ↓ more") + "\n")
		}
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	keys := []string{
		helpKeyRender("enter", "view runs"),
		helpKeyRender("/", "filter"),
		helpKeyRender("esc", "back"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")
	return b.String()
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
	keys := []string{
		helpKeyRender("enter", "open report"),
		helpKeyRender("R", "refresh"),
		helpKeyRender("esc", "back"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")
	return b.String()
}

// --- Workflow reports drill-down ---

// handleWorkflowReportsKey processes key events on the workflow reports list.
func (m hubModel) handleWorkflowReportsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		m.currentView = viewReportPicker
		m.reportFilterInput.SetValue("")
		return m, nil
	case "up", "k":
		if m.workflowCursor > 0 {
			m.workflowCursor--
		}
	case "down", "j":
		filtered := m.filteredWorkflows()
		if m.workflowCursor < len(filtered)-1 {
			m.workflowCursor++
		}
	case "enter":
		filtered := m.filteredWorkflows()
		if len(filtered) > 0 && m.workflowCursor < len(filtered) {
			selected := filtered[m.workflowCursor]
			m.selectedWorkflowID = selected.ID
			m.selectedWorkflowName = selected.Name
			m.workflowRunCursor = 0
			m.workflowRuns = nil
			m.reportLoading = true
			m.currentView = viewWorkflowRuns
			if m.client != nil {
				return m, fetchWorkflowHistoryCmd(m.client, selected.ID)
			}
		}
	case "/":
		m.reportFilterMode = true
		m.reportFilterInput.Focus()
		return m, textinput.Blink
	case "R":
		if m.client != nil {
			m.reportLoading = true
			m.workflows = nil
			return m, fetchWorkflowsCmd(m.client)
		}
	}
	return m, nil
}

// filteredWorkflows returns workflows filtered by the report filter input.
func (m *hubModel) filteredWorkflows() []api.SimpleWorkflow {
	query := strings.ToLower(m.reportFilterInput.Value())
	if query == "" {
		return m.workflows
	}
	var filtered []api.SimpleWorkflow
	for _, w := range m.workflows {
		if strings.Contains(strings.ToLower(w.Name), query) {
			filtered = append(filtered, w)
		}
	}
	return filtered
}

// handleWorkflowRunsKey processes key events on the workflow runs list.
func (m hubModel) handleWorkflowRunsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.currentView = viewWorkflowReports
		return m, nil
	case "up", "k":
		if m.workflowRunCursor > 0 {
			m.workflowRunCursor--
		}
	case "down", "j":
		if m.workflowRunCursor < len(m.workflowRuns)-1 {
			m.workflowRunCursor++
		}
	case "enter":
		if len(m.workflowRuns) > 0 && m.workflowRunCursor < len(m.workflowRuns) {
			run := m.workflowRuns[m.workflowRunCursor]
			taskID := run.ExecutionID
			if taskID == "" {
				taskID = run.WorkflowID
			}
			reportURL := fmt.Sprintf("%s/workflows/report?taskId=%s", config.GetAppURL(m.devMode), taskID)
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

// renderWorkflowReports renders the workflow list for the report drill-down.
func (m hubModel) renderWorkflowReports() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render("Workflow reports")
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	if m.reportFilterMode {
		b.WriteString("\n  " + filterPromptStyle.Render("/") + " " + m.reportFilterInput.View() + "\n")
	}

	workflows := m.filteredWorkflows()
	countLabel := fmt.Sprintf("%d", len(workflows))
	if m.reportFilterInput.Value() != "" && len(m.workflows) > 0 {
		countLabel = fmt.Sprintf("%d/%d", len(workflows), len(m.workflows))
	}
	b.WriteString(sectionStyle.Render("  Workflows") + " " + dimStyle.Render(countLabel) + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	if m.reportLoading {
		b.WriteString("  " + m.spinner.View() + " Loading workflows...\n")
	} else if len(workflows) == 0 {
		b.WriteString("  " + dimStyle.Render("No workflows found") + "\n")
	} else {
		maxVisible := m.height - 12
		if maxVisible < 5 {
			maxVisible = 5
		}
		start, end := scrollWindow(m.workflowCursor, len(workflows), maxVisible)
		if start > 0 {
			b.WriteString(dimStyle.Render("  ↑ more") + "\n")
		}
		for i := start; i < end; i++ {
			wf := workflows[i]
			cur := "  "
			nameStyle := normalStyle
			if i == m.workflowCursor {
				cur = selectedStyle.Render("▸ ")
				nameStyle = selectedRowStyle
			}
			b.WriteString("  " + cur + nameStyle.Render(wf.Name) + "\n")
		}
		if end < len(workflows) {
			b.WriteString(dimStyle.Render("  ↓ more") + "\n")
		}
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	keys := []string{
		helpKeyRender("enter", "view runs"),
		helpKeyRender("/", "filter"),
		helpKeyRender("R", "refresh"),
		helpKeyRender("esc", "back"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")
	return b.String()
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
	keys := []string{
		helpKeyRender("enter", "open report"),
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

		// Check 7: Tests configured
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
		if len(m.setupSteps) > 0 && m.setupCursor == 0 {
			step := m.setupSteps[m.setupCursor]
			if step.Status == "current" || step.Status == "hint" {
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

	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render("Help & Status")
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

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
		{"d", "Delete (in app/module/tag views)"},
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
