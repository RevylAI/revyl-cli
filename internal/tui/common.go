// Package tui provides the Bubble Tea TUI hub for the Revyl CLI.
//
// The TUI launches when a human runs bare `revyl` in an interactive terminal.
// It is never activated for agents, CI/CD, or piped output -- three independent
// gates (--json, --quiet, isatty) prevent it.
package tui

import (
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/sse"
)

// --- TTY gate ---

// ShouldRunTUI returns true if the TUI should be launched.
// Returns false when stdout is not a terminal, or --json/--quiet flags are set.
//
// Parameters:
//   - jsonOutput: whether --json was passed
//   - quiet: whether --quiet was passed
//
// Returns:
//   - bool: true if the TUI should run
func ShouldRunTUI(jsonOutput, quiet bool) bool {
	if jsonOutput || quiet {
		return false
	}
	return isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
}

// --- Brand colors (mirrors internal/ui/styles.go) ---

var (
	purple   = lipgloss.Color("#9D61FF")
	teal     = lipgloss.Color("#14B8A6")
	red      = lipgloss.Color("#EF4444")
	amber    = lipgloss.Color("#F59E0B")
	green    = lipgloss.Color("#22C55E")
	gray     = lipgloss.Color("#6B7280")
	dimGray  = lipgloss.Color("#9CA3AF")
	white    = lipgloss.Color("#E5E7EB")
	darkGray = lipgloss.Color("#374151")
	subtleBg = lipgloss.Color("#1F2937")
)

// --- Shared TUI styles ---

var (
	// titleStyle renders the REVYL header.
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(purple)

	// versionStyle renders the version badge.
	versionStyle = lipgloss.NewStyle().
			Foreground(dimGray)

	// headerBannerStyle renders the top-level REVYL banner with a rounded border.
	headerBannerStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(purple).
				Padding(0, 1)

	// sectionStyle renders section headers (e.g. "Tests", "Pipeline").
	sectionStyle = lipgloss.NewStyle().
			Foreground(dimGray).
			Bold(true).
			MarginTop(1)

	// activeSectionStyle renders section headers when that section has focus.
	activeSectionStyle = lipgloss.NewStyle().
				Foreground(purple).
				Bold(true).
				MarginTop(1)

	// selectedStyle highlights the currently selected list item.
	selectedStyle = lipgloss.NewStyle().
			Foreground(purple).
			Bold(true)

	// selectedRowStyle highlights the full row of the selected item with a subtle background.
	selectedRowStyle = lipgloss.NewStyle().
				Foreground(purple).
				Bold(true).
				Background(subtleBg)

	// normalStyle renders unselected list items.
	normalStyle = lipgloss.NewStyle().
			Foreground(white)

	// dimStyle renders low-priority text.
	dimStyle = lipgloss.NewStyle().
			Foreground(dimGray)

	// successStyle renders passed/success indicators.
	successStyle = lipgloss.NewStyle().
			Foreground(green)

	// errorStyle renders failed/error indicators.
	errorStyle = lipgloss.NewStyle().
			Foreground(red).
			Bold(true)

	// warningStyle renders warning/cancelled/timeout indicators.
	warningStyle = lipgloss.NewStyle().
			Foreground(amber)

	// runningStyle renders active/running indicators.
	runningStyle = lipgloss.NewStyle().
			Foreground(teal)

	// linkStyle renders clickable URLs.
	linkStyle = lipgloss.NewStyle().
			Foreground(purple).
			Underline(true)

	// helpStyle renders the bottom key hint bar.
	helpStyle = lipgloss.NewStyle().
			Foreground(gray)

	// separatorStyle renders horizontal rules.
	separatorStyle = lipgloss.NewStyle().
			Foreground(darkGray)

	// platformStyle renders platform badges (android/ios).
	platformStyle = lipgloss.NewStyle().
			Foreground(dimGray)

	// filterPromptStyle renders the filter prompt.
	filterPromptStyle = lipgloss.NewStyle().
				Foreground(purple).
				Bold(true)

	// metricValueStyle renders stat values in bold purple.
	metricValueStyle = lipgloss.NewStyle().
				Foreground(purple).
				Bold(true)

	// wowPositiveStyle renders positive week-over-week deltas in teal.
	wowPositiveStyle = lipgloss.NewStyle().
				Foreground(teal)

	// wowNegativeStyle renders negative week-over-week deltas in red.
	wowNegativeStyle = lipgloss.NewStyle().
				Foreground(red)

	// actionDescStyle renders the dim description text next to quick action labels.
	actionDescStyle = lipgloss.NewStyle().
			Foreground(gray)
)

// separator returns a horizontal line of the given width.
func separator(width int) string {
	return separatorStyle.Render(strings.Repeat("─", width))
}

// --- Shared message types ---

// TestListMsg carries the fetched test list from the API.
type TestListMsg struct {
	Tests []TestItem
	Err   error
}

// TestItem represents a test in the hub list.
type TestItem struct {
	ID       string
	Name     string
	Platform string
}

// ExecutionStartedMsg signals that a test execution has been created.
type ExecutionStartedMsg struct {
	TaskID   string
	TestID   string
	TestName string
	Err      error
}

// ExecutionProgressMsg carries an SSE progress update.
// NextCmd must be issued by the Update handler to continue the streaming chain.
type ExecutionProgressMsg struct {
	Status  *sse.TestStatus
	NextCmd tea.Cmd
}

// ExecutionDoneMsg signals that execution has completed (terminal state).
type ExecutionDoneMsg struct {
	Status    *sse.TestStatus
	ReportURL string
	Err       error
}

// ExecutionCancelledMsg signals that the user cancelled the execution.
type ExecutionCancelledMsg struct {
	Err error
}

// --- Shared spinner factory ---

// newSpinner creates a consistently styled braille spinner.
func newSpinner() spinner.Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(teal)
	return s
}

// --- View enum for hub navigation ---

// view represents the current TUI screen.
type view int

const (
	viewDashboard         view = iota // dashboard landing with stats + quick actions
	viewTestList                      // browsable test list (sub-screen)
	viewRunsList                      // browsable runs list (sub-screen)
	viewCreateTest                    // create-a-test flow (sub-screen)
	viewExecution                     // test execution monitor
	viewReportPicker                  // report type picker (test reports / workflow reports)
	viewTestReports                   // test list for report drill-down
	viewTestRuns                      // run history for a specific test
	viewWorkflowReports               // workflow list for report drill-down
	viewWorkflowRuns                  // run history for a specific workflow
	viewAppList                       // app list for manage apps
	viewAppDetail                     // build versions for a specific app
	viewCreateApp                     // create-an-app flow (sub-screen)
	viewUploadBuild                   // upload-a-build flow (sub-screen)
	viewHelp                          // help & status screen (doctor + keybindings)
	viewTestDetail                    // test detail + management screen
	viewWorkflowList                  // workflow browse list
	viewWorkflowDetail                // workflow detail + actions
	viewWorkflowCreate                // workflow create wizard
	viewWorkflowExecution             // workflow execution progress monitor
	viewModuleList                    // module browse list
	viewModuleDetail                  // module detail showing blocks
	viewTagList                       // tag browse list
	viewDeviceList                    // device session list
	viewDeviceDetail                  // device session detail
)

// --- Dashboard data types ---

// DashboardDataMsg carries the fetched dashboard metrics from the API.
type DashboardDataMsg struct {
	Metrics *api.DashboardMetrics
	Err     error
}

// RecentRunsMsg carries the fetched recent execution runs across tests.
type RecentRunsMsg struct {
	Runs []RecentRun
	Err  error
}

// AllRunsMsg carries the fetched full run list for the "View all runs" screen.
type AllRunsMsg struct {
	Runs []RecentRun
	Err  error
}

// RecentRun represents a recent test execution for the dashboard view.
type RecentRun struct {
	TestID   string
	TestName string
	Status   string
	Duration string
	Time     time.Time
	TaskID   string
}

// TestCreatedMsg signals that a test has been created via the TUI.
type TestCreatedMsg struct {
	TestID   string
	TestName string
	Platform string
	Err      error
}

// AppCreatedMsg signals that an app has been created via the TUI.
type AppCreatedMsg struct {
	AppID    string
	AppName  string
	Platform string
	Err      error
}

// WorkflowListMsg carries the fetched workflow list from the API.
type WorkflowListMsg struct {
	Workflows []api.SimpleWorkflow
	Err       error
}

// TestHistoryMsg carries the fetched execution history for a specific test.
type TestHistoryMsg struct {
	Runs []api.CLIEnhancedHistoryItem
	Err  error
}

// WorkflowHistoryMsg carries the fetched execution history for a specific workflow.
type WorkflowHistoryMsg struct {
	Runs []api.CLIWorkflowStatusResponse
	Err  error
}

// AppListMsg carries the fetched app list from the API.
type AppListMsg struct {
	Apps []api.App
	Err  error
}

// AppBuildVersionsMsg carries the fetched build versions for an app.
type AppBuildVersionsMsg struct {
	Versions []api.BuildVersion
	Err      error
}

// AppDeletedMsg signals that an app has been deleted.
type AppDeletedMsg struct {
	Err error
}

// BuildDeletedMsg signals that a build version has been deleted.
type BuildDeletedMsg struct {
	Err error
}

// BuildUploadedMsg signals that a build has been uploaded via the TUI.
type BuildUploadedMsg struct {
	VersionID string
	Version   string
	Err       error
}

// HealthCheck represents a single diagnostic check result for the help screen.
type HealthCheck struct {
	Name    string // e.g. "Version", "Authentication"
	Status  string // "ok", "warning", "error"
	Message string // human-readable result
}

// HealthCheckMsg carries results from the async health check command.
type HealthCheckMsg struct {
	Checks []HealthCheck
	Err    error
}

// --- Setup guide types ---

// SetupStep represents a single step in the getting-started guide on the help screen.
type SetupStep struct {
	Label   string // display label (e.g. "Log in")
	Status  string // "done", "current", "blocked", "hint"
	Message string // contextual message (e.g. "authenticated" or "press enter to set up")
}

// SetupActionMsg signals that a setup step action completed.
type SetupActionMsg struct {
	StepIndex int
	Err       error
}

// --- Test detail types ---

// TestDetail bundles fetched information for the test detail screen.
type TestDetail struct {
	ID       string
	Name     string
	Platform string
	// Sync
	SyncStatus  string // "synced", "modified", "outdated", "conflict", "local-only", "remote-only", "unknown"
	SyncVersion string // e.g. "v3"
	// Last run
	LastRunStatus   string
	LastRunTime     time.Time
	LastRunDuration string
	LastRunTaskID   string
	// Tags
	Tags []TagItem
	// Env vars count
	EnvVarCount int
}

// TestDetailMsg carries the fetched test detail data.
type TestDetailMsg struct {
	Detail *TestDetail
	Err    error
}

// TestSyncActionMsg signals that a push/pull/diff action completed.
type TestSyncActionMsg struct {
	Action string // "push", "pull", "diff"
	Result string // human-readable result
	Err    error
}

// TestDeletedMsg signals that a test has been deleted.
type TestDeletedMsg struct {
	Err error
}

// --- Env var types ---

// EnvVarItem represents a single environment variable for display.
type EnvVarItem struct {
	ID    string
	Key   string
	Value string
}

// EnvVarListMsg carries the fetched env var list.
type EnvVarListMsg struct {
	Vars []EnvVarItem
	Err  error
}

// EnvVarAddedMsg signals that an env var was added.
type EnvVarAddedMsg struct {
	Err error
}

// EnvVarDeletedMsg signals that an env var was deleted.
type EnvVarDeletedMsg struct {
	Err error
}

// --- Workflow management types ---

// WorkflowItem represents a workflow in the browse list.
type WorkflowItem struct {
	ID            string
	Name          string
	TestCount     int
	TestNames     []string
	LastRunStatus string
	LastRunTime   time.Time
}

// WorkflowBrowseListMsg carries the fetched workflow browse list with enriched data.
type WorkflowBrowseListMsg struct {
	Workflows []WorkflowItem
	Err       error
}

// WorkflowDetailMsg carries full workflow detail for the detail screen.
type WorkflowDetailMsg struct {
	Workflow *WorkflowItem
	Err      error
}

// WorkflowCreatedMsg signals that a workflow was created.
type WorkflowCreatedMsg struct {
	ID   string
	Name string
	Err  error
}

// WorkflowDeletedMsg signals that a workflow was deleted.
type WorkflowDeletedMsg struct {
	Err error
}

// WorkflowExecStartedMsg signals that a workflow execution was started.
type WorkflowExecStartedMsg struct {
	TaskID string
	Err    error
}

// WorkflowExecProgressMsg carries a workflow execution status update.
type WorkflowExecProgressMsg struct {
	Status *api.CLIWorkflowStatusResponse
	Err    error
}

// WorkflowExecDoneMsg signals that a workflow execution completed.
type WorkflowExecDoneMsg struct {
	Status    *api.CLIWorkflowStatusResponse
	ReportURL string
	Err       error
}

// WorkflowCancelledMsg signals that a workflow execution was cancelled.
type WorkflowCancelledMsg struct {
	Err error
}

// --- Module types ---

// ModuleItem represents a module in the browse list.
type ModuleItem struct {
	ID          string
	Name        string
	Description string
	BlockCount  int
	Blocks      []interface{} // raw block data for detail view
}

// ModuleListMsg carries the fetched module list.
type ModuleListMsg struct {
	Modules []ModuleItem
	Err     error
}

// ModuleDetailMsg carries a single module's full detail.
type ModuleDetailMsg struct {
	Module *ModuleItem
	Err    error
}

// ModuleDeletedMsg signals that a module was deleted.
type ModuleDeletedMsg struct {
	Err error
}

// --- Tag types ---

// TagItem represents a tag in the browse list.
type TagItem struct {
	ID          string
	Name        string
	Color       string
	Description string
	TestCount   int
}

// TagListMsg carries the fetched tag list.
type TagListMsg struct {
	Tags []TagItem
	Err  error
}

// TagCreatedMsg signals that a tag was created.
type TagCreatedMsg struct {
	ID   string
	Name string
	Err  error
}

// TagDeletedMsg signals that a tag was deleted.
type TagDeletedMsg struct {
	Err error
}

// TagsSyncedMsg signals that tags were synced on a test.
type TagsSyncedMsg struct {
	Err error
}

// --- Device session messages ---

// DeviceSessionListMsg carries the fetched active device sessions from the API.
type DeviceSessionListMsg struct {
	Sessions []api.ActiveDeviceSessionItem
	Err      error
}

// DeviceStartedMsg signals that a device session was started (workflow run created).
type DeviceStartedMsg struct {
	WorkflowRunID string
	Platform      string
	ViewerURL     string
	Err           error
}

// DeviceStoppedMsg signals that a device session was stopped.
type DeviceStoppedMsg struct {
	Err error
}

// --- Tea program runner ---

// RunHub launches the TUI hub. This is the main entry point called from cmd/revyl/main.go.
//
// Parameters:
//   - version: the CLI version string for display
//   - devMode: whether to use local development servers (--dev flag)
//
// Returns:
//   - error: any error from the Bubble Tea runtime
func RunHub(version string, devMode bool) error {
	p := tea.NewProgram(
		newHubModel(version, devMode),
		tea.WithAltScreen(),
	)
	_, err := p.Run()
	return err
}
