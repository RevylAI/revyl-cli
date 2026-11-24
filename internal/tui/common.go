// Package tui provides the Bubble Tea TUI hub for the Revyl CLI.
// ShouldRunTUI gates launch on an interactive stdout TTY and the --json/--quiet flags.
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

var (
	purple   = lipgloss.Color("#9D61FF")
	teal     = lipgloss.Color("#14B8A6")
	red      = lipgloss.Color("#EF4444")
	amber    = lipgloss.Color("#F59E0B")
	green    = lipgloss.Color("#22C55E")
	gray     = lipgloss.Color("#6B7280")
	dimGray  = lipgloss.Color("#9CA3AF")
	darkGray = lipgloss.Color("#374151")
	subtleBg = lipgloss.Color("#1F2937")
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(purple)

	versionStyle = lipgloss.NewStyle().
			Foreground(dimGray)

	headerBannerStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(purple).
				Padding(0, 1)

	sectionStyle = lipgloss.NewStyle().
			Foreground(dimGray).
			Bold(true).
			MarginTop(1)

	activeSectionStyle = lipgloss.NewStyle().
				Foreground(purple).
				Bold(true).
				MarginTop(1)

	selectedStyle = lipgloss.NewStyle().
			Foreground(purple).
			Bold(true)

	selectedRowStyle = lipgloss.NewStyle().
				Foreground(purple).
				Bold(true).
				Background(subtleBg)

	normalStyle = lipgloss.NewStyle()

	dimStyle = lipgloss.NewStyle().
			Foreground(dimGray)

	successStyle = lipgloss.NewStyle().
			Foreground(green)

	errorStyle = lipgloss.NewStyle().
			Foreground(red).
			Bold(true)

	warningStyle = lipgloss.NewStyle().
			Foreground(amber)

	runningStyle = lipgloss.NewStyle().
			Foreground(teal)

	linkStyle = lipgloss.NewStyle().
			Foreground(purple).
			Underline(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(gray)

	separatorStyle = lipgloss.NewStyle().
			Foreground(darkGray)

	platformStyle = lipgloss.NewStyle().
			Foreground(dimGray)

	filterPromptStyle = lipgloss.NewStyle().
				Foreground(purple).
				Bold(true)

	metricValueStyle = lipgloss.NewStyle().
				Foreground(purple).
				Bold(true)

	wowPositiveStyle = lipgloss.NewStyle().
				Foreground(teal)

	wowNegativeStyle = lipgloss.NewStyle().
				Foreground(red)

	actionDescStyle = lipgloss.NewStyle().
			Foreground(gray)
)

func separator(width int) string {
	return separatorStyle.Render(strings.Repeat("─", width))
}

type TestListMsg struct {
	Tests []TestItem
	// Warning carries non-fatal browse-state warnings such as org mismatch.
	Warning string
	Err     error
}

type TestItem struct {
	ID            string
	Name          string
	Platform      string
	Tags          []api.TestTag
	AppID         string // associated app UUID (empty when no app)
	AppName       string
	SyncStatus    string // synced, modified, outdated, conflict, local-only, remote-only, stale
	Source        string // remote, config, local, merged
	RemoteMissing bool
}

type FilterTagsMsg struct {
	Tags []api.CLITagResponse
	Err  error
}

type FilterAppsMsg struct {
	Apps []api.App
	Err  error
}

type ExecutionStartedMsg struct {
	BranchFallbackNotice string
	TaskID               string
	TestID               string
	TestName             string
	Err                  error
}

// ExecutionProgressMsg carries an SSE progress update.
// NextCmd must be issued by the Update handler to continue the streaming chain.
type ExecutionProgressMsg struct {
	Status  *sse.TestStatus
	NextCmd tea.Cmd
}

type ExecutionDoneMsg struct {
	Status    *sse.TestStatus
	ReportURL string
	Err       error
}

// ExecutionCancelledMsg carries the result of an execution cancellation request.
type ExecutionCancelledMsg struct {
	Err error
}

func newSpinner() spinner.Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(teal)
	return s
}

type view int

const (
	viewDashboard view = iota
	viewTestList
	viewRunsList
	viewCreateTest
	viewExecution
	viewTestRuns
	viewWorkflowRuns
	viewAppList
	viewAppDetail
	viewCreateApp
	viewUploadBuild
	viewSettings
	viewHelp
	viewTestDetail
	viewWorkflowList
	viewWorkflowDetail
	viewWorkflowCreate
	viewWorkflowExecution
	viewWorkflowSettings
	viewModuleList
	viewModuleDetail
	viewTagList
	viewDeviceList
	viewDeviceDetail
	viewLibrary
	viewIntegrations
)

type DashboardDataMsg struct {
	Metrics *api.DashboardMetrics
	Err     error
}

type RecentRunsMsg struct {
	Runs []RecentRun
	Err  error
}

// AllRunsMsg carries up to one latest execution per test for the first 25 tests.
type AllRunsMsg struct {
	Runs []RecentRun
	Err  error
}

type RecentRun struct {
	TestID   string
	TestName string
	Status   string
	Duration string
	Time     time.Time
	TaskID   string
}

type TestCreatedMsg struct {
	TestID   string
	TestName string
	Platform string
	Err      error
}

type AppCreatedMsg struct {
	AppID    string
	AppName  string
	Platform string
	Err      error
}

type TestHistoryMsg struct {
	Runs []api.CLIEnhancedHistoryItem
	Err  error
}

type WorkflowHistoryMsg struct {
	Runs []api.CLIWorkflowStatusResponse
	Err  error
}

type ReportJSONActionMsg struct {
	TaskID  string
	Command string
	Err     error
}

type ReportCommandActionMsg struct {
	Command string
	Err     error
}

type AppListMsg struct {
	Apps []api.App
	Err  error
}

type AppBuildVersionsMsg struct {
	RequestID string
	Page      int
	HasNext   bool
	AppID     string
	Query     appBuildQuery
	Branch    *api.BuildBranch
	Versions  []api.BuildVersion
	Err       error
}

type AppDeletedMsg struct {
	Err error
}

type BuildDeletedMsg struct {
	Err error
}

type BuildUploadedMsg struct {
	Branch    *api.BuildBranch
	VersionID string
	Version   string
	// BranchSummary names the upload's branch and the build a plain run on it
	// now uses; empty when that check failed.
	BranchSummary string
	Err           error
}

type HealthCheck struct {
	Name    string
	Status  string
	Code    string // stable local classification used to select a recovery action
	Message string
}

type HealthCheckMsg struct {
	Checks              []HealthCheck
	ProjectConfigStatus projectConfigStatus
	Err                 error
}

type SetupStep struct {
	Label   string
	Status  string // "done", "current", "blocked", "hint"
	Message string
	Action  setupAction
}

type SetupActionMsg struct {
	Action setupAction
	Err    error
}

type ProjectConfigMigrationDoneMsg struct {
	Err error
}

type TestDetail struct {
	AppID           string
	Version         int
	BuildSelection  api.BuildSelection
	BuildLabel      string
	ID              string
	Name            string
	Platform        string
	SyncStatus      string // "synced", "modified", "outdated", "conflict", "local-only", "remote-only", "unknown"
	SyncVersion     string
	LastRunStatus   string
	LastRunTime     time.Time
	LastRunDuration string
	LastRunTaskID   string
	Tags            []TagItem
	EnvVarCount     int
}

type TestDetailMsg struct {
	Detail *TestDetail
	Err    error
}

type TestSyncActionMsg struct {
	Action string
	Result string
	Err    error
}

// WorkflowSyncProgressMsg carries a per-test progress update during a
// workflow-scoped sync. The hub handler chains the next channel read after
// processing each progress message.
type WorkflowSyncProgressMsg struct {
	TestName string
	Status   string
	Current  int
	Total    int
}

type WorkflowSyncMsg struct {
	Synced    int
	Conflicts int
	Errors    int
	Total     int
	Err       error
}

type TestDeletedMsg struct {
	Name          string
	ID            string
	RemoteDeleted bool
	LocalDeleted  bool
	Warning       string
	Err           error
}

type TestRenamedMsg struct {
	OldName string
	NewName string
	ID      string
	Summary string
	Err     error
}

type WorkflowItem struct {
	ID            string
	Name          string
	TestCount     int
	TestNames     []string
	TestInfo      []api.WorkflowInfoTestItem
	LastRunStatus string
	LastRunTime   time.Time
}

type WorkflowBrowseListMsg struct {
	Workflows []WorkflowItem
	Err       error
}

type WorkflowDetailMsg struct {
	Workflow *WorkflowItem
	Err      error
}

type WorkflowCreatedMsg struct {
	ID   string
	Name string
	Err  error
}

type WorkflowDeletedMsg struct {
	Err error
}

type WorkflowExecStartedMsg struct {
	TaskID string
	// Notice says what the run executes and any problems with the files it
	// sent.
	Notice string
	Err    error
}

type WorkflowExecProgressMsg struct {
	Status *api.CLIWorkflowStatusResponse
	Err    error
}

type WorkflowExecDoneMsg struct {
	Status    *api.CLIWorkflowStatusResponse
	ReportURL string
	Err       error
}

// WorkflowCancelledMsg carries the result of a workflow cancellation request.
type WorkflowCancelledMsg struct {
	Err error
}

type WorkflowSettingsMsg struct {
	BranchLabels map[string]string
	Workflow     *api.Workflow
	AllTests     []TestItem
	AllApps      []api.App
	Err          error
}

type WorkflowSettingsSavedMsg struct {
	Err error
}

type ModuleItem struct {
	ID          string
	Name        string
	Description string
	BlockCount  int
	Blocks      []interface{}
}

type ModuleListMsg struct {
	Modules []ModuleItem
	Err     error
}

type ModuleDetailMsg struct {
	Module *ModuleItem
	Err    error
}

type ModuleDeletedMsg struct {
	Err error
}

type ScriptItem struct {
	ID          string
	Name        string
	Runtime     string
	Description string
	Code        string
}

type ScriptListMsg struct {
	Scripts []ScriptItem
	Err     error
}

type ScriptDetailMsg struct {
	Script *ScriptItem
	Err    error
}

type ScriptUpdatedMsg struct {
	Err error
}

type ScriptDeletedMsg struct {
	Err error
}

type FileItem struct {
	ID          string
	Filename    string
	FileSize    int64
	ContentType string
	Description string
}

type FileListMsg struct {
	Files []FileItem
	Err   error
}

type FileUpdatedMsg struct {
	Err error
}

type FileDeletedMsg struct {
	Err error
}

type VariableItem struct {
	ID       string
	Name     string
	Value    string
	IsSecret bool
}

type VariableListMsg struct {
	Variables []VariableItem
	Err       error
}

type VariableSavedMsg struct {
	Err error
}

type VariableDeletedMsg struct {
	Err error
}

type LaunchVarItem struct {
	ID                string
	Key               string
	Value             string
	IsSecret          bool
	Description       string
	AttachedTestCount int
}

type LaunchVarListMsg struct {
	LaunchVars []LaunchVarItem
	Err        error
}

type LaunchVarSavedMsg struct {
	Err error
}

type LaunchVarDeletedMsg struct {
	Err error
}

type TagItem struct {
	ID          string
	Name        string
	Color       string
	Description string
	TestCount   int
}

type TagListMsg struct {
	Tags []TagItem
	Err  error
}

type TagCreatedMsg struct {
	ID   string
	Name string
	Err  error
}

type TagDeletedMsg struct {
	Err error
}

type TagsSyncedMsg struct {
	Err error
}

type DeviceSessionListMsg struct {
	Sessions []api.ActiveDeviceSessionItem
	OrgID    string
	Err      error
}

type DeviceStartedMsg struct {
	BranchFallbackNotice string
	WorkflowRunID        string
	Platform             string
	ViewerURL            string
	Err                  error
}

type DeviceStartAppListMsg struct {
	Platform string
	Apps     []api.App
	Err      error
}

type DeviceStoppedMsg struct {
	Err error
}

type DeviceDetailPollTickMsg struct {
	Seq int
}

type DeviceListPollTickMsg struct {
	Seq int
}

func RunHub(version string, devMode bool) error {
	p := tea.NewProgram(
		newHubModel(version, devMode),
		tea.WithAltScreen(),
	)
	_, err := p.Run()
	return err
}
