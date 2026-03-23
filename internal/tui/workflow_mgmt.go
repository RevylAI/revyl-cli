// Package tui provides the workflow management screens: list, detail, create wizard,
// and execution monitor.
//
// Workflows can be browsed, created with a multi-step wizard, executed with inline
// progress monitoring, and deleted from the TUI.
package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/sync"
	"github.com/revyl/cli/internal/ui"
)

// workflowCreateStep tracks the current step in the workflow create wizard.
type workflowCreateStep int

const (
	wfCreateStepName    workflowCreateStep = iota // name input
	wfCreateStepTests                             // multi-select tests
	wfCreateStepConfirm                           // confirm + create
)

type workflowDetailAction struct {
	Key  string
	Desc string
}

var workflowDetailActions = []workflowDetailAction{
	{Key: "r", Desc: "Run workflow"},
	{Key: "o", Desc: "Open in browser"},
	{Key: "h", Desc: "Run history"},
	{Key: "x", Desc: "Delete workflow"},
	{Key: "s", Desc: "Settings"},
	{Key: "y", Desc: "Sync tests"},
}

// --- Commands ---

// fetchWorkflowBrowseListCmd fetches the workflow list with last-run info in a
// single API call. The get_with_last_status endpoint already returns test_count
// and last_execution, so no per-workflow detail fetches are needed.
//
// Parameters:
//   - client: the API client
//
// Returns:
//   - tea.Cmd: command producing WorkflowBrowseListMsg
func fetchWorkflowBrowseListCmd(client *api.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		resp, err := client.ListWorkflows(ctx)
		if err != nil {
			return WorkflowBrowseListMsg{Err: err}
		}

		var items []WorkflowItem
		for _, w := range resp.Workflows {
			item := WorkflowItem{
				ID:        w.ID,
				Name:      w.Name,
				TestCount: w.TestCount,
			}

			if w.LastExecution != nil && w.LastExecution.Status != "not_run" {
				item.LastRunStatus = w.LastExecution.Status
				if w.LastExecution.LastRun != nil {
					if t, err := time.Parse(time.RFC3339Nano, *w.LastExecution.LastRun); err == nil {
						item.LastRunTime = t
					}
				}
			}

			items = append(items, item)
		}

		return WorkflowBrowseListMsg{Workflows: items}
	}
}

// fetchWorkflowDetailCmd fetches a single workflow's detail including resolved
// test names/platforms and last execution status.
//
// Parameters:
//   - client: the API client
//   - workflowID: the workflow to fetch
//
// Returns:
//   - tea.Cmd: command producing WorkflowDetailMsg
func fetchWorkflowDetailCmd(client *api.Client, workflowID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		full, err := client.GetWorkflow(ctx, workflowID)
		if err != nil {
			return WorkflowDetailMsg{Err: err}
		}

		item := &WorkflowItem{
			ID:        full.ID,
			Name:      full.Name,
			TestCount: len(full.Tests),
			TestNames: full.Tests,
		}

		info, infoErr := client.GetWorkflowInfo(ctx, workflowID)
		if infoErr == nil && info != nil {
			item.TestInfo = info.TestInfo
			item.TestCount = len(info.TestInfo)
			names := make([]string, 0, len(info.TestInfo))
			for _, ti := range info.TestInfo {
				names = append(names, ti.Name)
			}
			item.TestNames = names
		}

		history, hErr := client.GetWorkflowHistory(ctx, workflowID, 1, 0)
		if hErr == nil && len(history.Executions) > 0 {
			latest := history.Executions[0]
			item.LastRunStatus = latest.Status
			if latest.StartedAt != "" {
				if t, err := time.Parse(time.RFC3339Nano, latest.StartedAt); err == nil {
					item.LastRunTime = t
				}
			}
		}

		return WorkflowDetailMsg{Workflow: item}
	}
}

// executeWorkflowCmd starts a workflow execution.
//
// Parameters:
//   - client: the API client
//   - workflowID: the workflow to execute
//
// Returns:
//   - tea.Cmd: command producing WorkflowExecStartedMsg
func executeWorkflowCmd(client *api.Client, workflowID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		resp, err := client.ExecuteWorkflow(ctx, &api.ExecuteWorkflowRequest{
			WorkflowID: workflowID,
		})
		if err != nil {
			return WorkflowExecStartedMsg{Err: err}
		}
		return WorkflowExecStartedMsg{TaskID: resp.TaskID}
	}
}

// pollWorkflowStatusCmd polls the workflow execution status.
//
// Parameters:
//   - client: the API client
//   - taskID: the task ID to poll
//
// Returns:
//   - tea.Cmd: command producing WorkflowExecProgressMsg or WorkflowExecDoneMsg
func pollWorkflowStatusCmd(client *api.Client, taskID string) tea.Cmd {
	return func() tea.Msg {
		// Small delay before polling
		time.Sleep(2 * time.Second)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		resp, err := client.GetWorkflowStatus(ctx, taskID)
		if err != nil {
			return WorkflowExecProgressMsg{Err: err}
		}

		status := strings.ToLower(resp.Status)
		if status == "completed" || status == "passed" || status == "failed" || status == "cancelled" || status == "error" {
			return WorkflowExecDoneMsg{Status: resp}
		}

		return WorkflowExecProgressMsg{Status: resp}
	}
}

// cancelWorkflowCmd cancels a running workflow execution.
//
// Parameters:
//   - client: the API client
//   - taskID: the task to cancel
//
// Returns:
//   - tea.Cmd: command producing WorkflowCancelledMsg
func cancelWorkflowCmd(client *api.Client, taskID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := client.CancelWorkflow(ctx, taskID)
		return WorkflowCancelledMsg{Err: err}
	}
}

// createWorkflowAPICmd creates a new workflow via the API.
// It resolves the authenticated user's ID via ValidateAPIKey so that the
// backend receives a valid owner UUID.
//
// Parameters:
//   - client: the API client
//   - name: the workflow name
//   - testIDs: the selected test IDs
//
// Returns:
//   - tea.Cmd: command producing WorkflowCreatedMsg
func createWorkflowAPICmd(client *api.Client, name string, testIDs []string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		userInfo, err := client.ValidateAPIKey(ctx)
		if err != nil {
			return WorkflowCreatedMsg{Err: fmt.Errorf("failed to resolve user: %w", err)}
		}

		resp, err := client.CreateWorkflow(ctx, &api.CLICreateWorkflowRequest{
			Name:     name,
			Tests:    testIDs,
			Owner:    userInfo.UserID,
			Schedule: "No Schedule",
		})
		if err != nil {
			return WorkflowCreatedMsg{Err: err}
		}
		return WorkflowCreatedMsg{ID: resp.Data.ID, Name: resp.Data.Name}
	}
}

// deleteWorkflowCmd deletes a workflow by ID.
//
// Parameters:
//   - client: the API client
//   - workflowID: the workflow to delete
//
// Returns:
//   - tea.Cmd: command producing WorkflowDeletedMsg
func deleteWorkflowCmd(client *api.Client, workflowID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := client.DeleteWorkflow(ctx, workflowID)
		return WorkflowDeletedMsg{Err: err}
	}
}

// syncWorkflowTestsCmd pulls/imports all tests belonging to a workflow into the
// local .revyl/tests/ directory. Tests already linked locally are pulled; tests
// not yet local are imported. Conflicts are reported (not force-resolved) so the
// user can drop to `revyl sync --workflow` for interactive resolution.
//
// Progress is streamed via WorkflowSyncProgressMsg after each test, with a
// final WorkflowSyncMsg when all tests are done. The caller chains reads from
// the returned channel using waitForSyncMsg.
//
// Parameters:
//   - client: the API client
//   - workflowID: UUID of the workflow to sync tests from
//   - devMode: whether dev mode is active
//
// Returns:
//   - <-chan tea.Msg: channel that yields progress and completion messages
//   - tea.Cmd: initial command that reads the first message from the channel
func syncWorkflowTestsCmd(client *api.Client, workflowID string, devMode bool) (<-chan tea.Msg, tea.Cmd) {
	ch := make(chan tea.Msg, 1)

	go func() {
		defer close(ch)

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		wfInfo, err := client.GetWorkflowInfo(ctx, workflowID)
		if err != nil {
			ch <- WorkflowSyncMsg{Err: fmt.Errorf("failed to fetch workflow info: %w", err)}
			return
		}
		if len(wfInfo.TestInfo) == 0 {
			ch <- WorkflowSyncMsg{Total: 0}
			return
		}

		cwd, err := os.Getwd()
		if err != nil {
			ch <- WorkflowSyncMsg{Err: fmt.Errorf("failed to get working directory: %w", err)}
			return
		}

		configPath := filepath.Join(cwd, ".revyl", "config.yaml")
		cfg, err := loadOrInitProjectConfigForSync(ctx, client, cwd, configPath)
		if err != nil {
			ch <- WorkflowSyncMsg{Err: fmt.Errorf("failed to prepare project config: %w", err)}
			return
		}

		testsDir := filepath.Join(cwd, ".revyl", "tests")
		localTests, err := config.LoadLocalTests(testsDir)
		if err != nil {
			localTests = make(map[string]*config.LocalTest)
		}

		resolver := sync.NewResolver(client, cfg, localTests)

		total := len(wfInfo.TestInfo)
		synced, conflicts, syncErrors := 0, 0, 0

		for i, ti := range wfInfo.TestInfo {
			status := syncOneWorkflowTest(ctx, resolver, localTests, testsDir, ti)

			switch {
			case strings.HasPrefix(status, "error"):
				syncErrors++
			case strings.HasPrefix(status, "conflict"):
				conflicts++
			default:
				synced++
			}

			ch <- WorkflowSyncProgressMsg{
				TestName: ti.Name,
				Status:   status,
				Current:  i + 1,
				Total:    total,
			}
		}

		if saveErr := persistProjectConfigForSync(configPath, cfg); saveErr != nil {
			syncErrors++
		}

		ch <- WorkflowSyncMsg{
			Synced:    synced,
			Conflicts: conflicts,
			Errors:    syncErrors,
			Total:     total,
		}
	}()

	return ch, waitForSyncMsg(ch)
}

// syncOneWorkflowTest syncs a single test from a workflow, returning a
// human-readable status string.
//
// Parameters:
//   - ctx: context for API calls
//   - resolver: the sync resolver
//   - localTests: map of local test aliases to definitions
//   - testsDir: path to .revyl/tests/ directory
//   - ti: the workflow test info item
//
// Returns:
//   - string: status like "pulled v3", "imported", "up to date", "conflict (resolve via CLI)", or "error: ..."
func syncOneWorkflowTest(ctx context.Context, resolver *sync.Resolver, localTests map[string]*config.LocalTest, testsDir string, ti api.WorkflowInfoTestItem) string {
	localName := ""
	for alias, lt := range localTests {
		if lt != nil && lt.Meta.RemoteID == ti.ID {
			localName = alias
			break
		}
	}

	if localName != "" {
		pullResults, pullErr := resolver.PullFromRemote(ctx, localName, testsDir, false)
		if pullErr != nil {
			return fmt.Sprintf("error: %v", pullErr)
		}
		if len(pullResults) == 0 {
			return "up to date"
		}
		r := pullResults[0]
		if r.Conflict {
			return "conflict (resolve via CLI)"
		}
		if r.Error != nil {
			return fmt.Sprintf("error: %v", r.Error)
		}
		return fmt.Sprintf("pulled v%d", r.NewVersion)
	}

	importResults, importErr := resolver.ImportRemoteTest(ctx, ti.ID, ti.Name, testsDir, false)
	if importErr != nil {
		return fmt.Sprintf("error: %v", importErr)
	}
	if len(importResults) > 0 && importResults[0].Error != nil {
		return fmt.Sprintf("error: %v", importResults[0].Error)
	}
	return "imported"
}

// waitForSyncMsg returns a tea.Cmd that blocks until the next message arrives
// on the channel. Used to chain progress reads in the bubbletea update loop.
//
// Parameters:
//   - ch: channel producing WorkflowSyncProgressMsg or WorkflowSyncMsg
//
// Returns:
//   - tea.Cmd: command that yields the next message from the channel
func waitForSyncMsg(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return WorkflowSyncMsg{Err: fmt.Errorf("sync channel closed unexpectedly")}
		}
		return msg
	}
}

// --- Key handling ---

// handleWorkflowListKey processes key events on the workflow browse list.
//
// Parameters:
//   - m: the hub model
//   - msg: the key message
//
// Returns:
//   - tea.Model: updated model
//   - tea.Cmd: next command
func handleWorkflowListKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.wfFilterMode {
		switch msg.String() {
		case "esc":
			m.wfFilterMode = false
			m.wfFilterInput.Blur()
			m.wfFilterInput.SetValue("")
			if m.wfCursor >= len(m.filteredWorkflowItems()) {
				m.wfCursor = max(0, len(m.filteredWorkflowItems())-1)
			}
			return m, nil
		case "enter":
			m.wfFilterMode = false
			m.wfFilterInput.Blur()
			if m.wfCursor >= len(m.filteredWorkflowItems()) {
				m.wfCursor = max(0, len(m.filteredWorkflowItems())-1)
			}
			return m, nil
		default:
			var cmd tea.Cmd
			m.wfFilterInput, cmd = m.wfFilterInput.Update(msg)
			if m.wfCursor >= len(m.filteredWorkflowItems()) {
				m.wfCursor = max(0, len(m.filteredWorkflowItems())-1)
			}
			return m, cmd
		}
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.wfFilterInput.SetValue("")
		m.currentView = viewDashboard
		return m, nil
	case "up", "k":
		if m.wfCursor > 0 {
			m.wfCursor--
		}
	case "down", "j":
		if m.wfCursor < len(m.filteredWorkflowItems())-1 {
			m.wfCursor++
		}
	case "enter":
		workflows := m.filteredWorkflowItems()
		if m.wfCursor < len(workflows) && m.client != nil {
			wf := workflows[m.wfCursor]
			// Browse mode: navigate to workflow detail
			m.wfDetailLoading = true
			m.wfDetailCursor = 0
			m.currentView = viewWorkflowDetail
			return m, fetchWorkflowDetailCmd(m.client, wf.ID)
		}
	case "r":
		workflows := m.filteredWorkflowItems()
		if m.wfCursor < len(workflows) && m.client != nil {
			wf := workflows[m.wfCursor]
			m.selectedWfDetail = &wf
			m.currentView = viewWorkflowExecution
			m.wfExecStatus = nil
			m.wfExecDone = false
			m.wfExecStartTime = time.Now()
			m.wfExecReturnView = viewWorkflowList
			return m, executeWorkflowCmd(m.client, wf.ID)
		}
	case "c":
		m.currentView = viewWorkflowCreate
		m.wfCreateStep = wfCreateStepName
		m.wfCreateNameInput.SetValue("")
		m.wfCreateNameInput.Focus()
		m.wfCreateSelectedTests = nil
		m.wfCreateTestFilterMode = false
		m.wfCreateTestFilterInput.SetValue("")
		m.wfCreateTestPlatformFilter = ""
		return m, textinput.Blink
	case "/":
		m.wfFilterMode = true
		m.wfFilterInput.Focus()
		return m, textinput.Blink
	case "R":
		if m.client != nil {
			m.wfListLoading = true
			return m, fetchWorkflowBrowseListCmd(m.client)
		}
	}
	return m, nil
}

// filteredWorkflowItems returns workflow items filtered by the workflow filter input.
func (m *hubModel) filteredWorkflowItems() []WorkflowItem {
	query := strings.ToLower(strings.TrimSpace(m.wfFilterInput.Value()))
	if query == "" {
		return m.wfItems
	}

	var filtered []WorkflowItem
	for _, wf := range m.wfItems {
		if strings.Contains(strings.ToLower(wf.Name), query) ||
			strings.Contains(strings.ToLower(wf.LastRunStatus), query) {
			filtered = append(filtered, wf)
		}
	}
	return filtered
}

// filteredCreateTests returns tests filtered by the search query and platform filter
// for the Create Workflow test selection step. Both dimensions are AND'd.
//
// Returns:
//   - []TestItem: filtered test list (all tests when no filters are active)
func (m *hubModel) filteredCreateTests() []TestItem {
	platform := m.wfCreateTestPlatformFilter
	query := strings.ToLower(strings.TrimSpace(m.wfCreateTestFilterInput.Value()))

	if platform == "" && query == "" {
		return m.tests
	}

	var filtered []TestItem
	for _, t := range m.tests {
		if platform != "" && t.Platform != platform {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(t.Name), query) {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}

// handleWorkflowDetailKey processes key events on the workflow detail screen.
//
// Parameters:
//   - m: the hub model
//   - msg: the key message
//
// Returns:
//   - tea.Model: updated model
//   - tea.Cmd: next command
func handleWorkflowDetailKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Delete confirmation
	if m.wfConfirmDelete {
		switch msg.String() {
		case "y":
			m.wfConfirmDelete = false
			if m.selectedWfDetail != nil && m.client != nil {
				m.wfDetailLoading = true
				return m, deleteWorkflowCmd(m.client, m.selectedWfDetail.ID)
			}
		case "n", "esc":
			m.wfConfirmDelete = false
		}
		return m, nil
	}

	if idx, ok := actionNumberIndex(msg.String(), len(workflowDetailActions)); ok {
		m.wfDetailCursor = idx
		return executeWorkflowDetailActionByIndex(m, idx)
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.currentView = viewWorkflowList
		m.selectedWfDetail = nil
		m.wfDetailCursor = 0
		return m, nil
	case "up", "k":
		if m.wfDetailCursor > 0 {
			m.wfDetailCursor--
		}
	case "down", "j":
		if m.wfDetailCursor < len(workflowDetailActions)-1 {
			m.wfDetailCursor++
		}
	case "enter":
		return executeWorkflowDetailActionByIndex(m, m.wfDetailCursor)
	default:
		if idx := workflowDetailActionIndexByKey(msg.String()); idx >= 0 {
			m.wfDetailCursor = idx
			return executeWorkflowDetailActionByIndex(m, idx)
		}
	}
	return m, nil
}

func workflowDetailActionIndexByKey(key string) int {
	for i, action := range workflowDetailActions {
		if action.Key == key {
			return i
		}
	}
	return -1
}

func executeWorkflowDetailActionByIndex(m hubModel, idx int) (tea.Model, tea.Cmd) {
	if idx < 0 || idx >= len(workflowDetailActions) {
		return m, nil
	}
	return executeWorkflowDetailActionByKey(m, workflowDetailActions[idx].Key)
}

func executeWorkflowDetailActionByKey(m hubModel, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "r":
		if m.selectedWfDetail != nil && m.client != nil {
			m.currentView = viewWorkflowExecution
			m.wfExecStatus = nil
			m.wfExecDone = false
			m.wfExecStartTime = time.Now()
			m.wfExecReturnView = viewWorkflowDetail
			return m, executeWorkflowCmd(m.client, m.selectedWfDetail.ID)
		}
	case "o":
		if m.selectedWfDetail != nil {
			wfURL := config.GetAppURL(m.devMode) + "/workflows/" + m.selectedWfDetail.ID
			_ = ui.OpenBrowser(wfURL)
		}
	case "h":
		if m.selectedWfDetail != nil {
			m.selectedWorkflowID = m.selectedWfDetail.ID
			m.selectedWorkflowName = m.selectedWfDetail.Name
			m.reportReturnView = viewWorkflowDetail
			m.currentView = viewWorkflowRuns
			m.reportLoading = true
			if m.client != nil {
				return m, fetchWorkflowHistoryCmd(m.client, m.selectedWfDetail.ID)
			}
		}
	case "x":
		m.wfConfirmDelete = true
	case "s":
		if m.selectedWfDetail != nil && m.client != nil {
			m.currentView = viewWorkflowSettings
			m.wfSettingsLoading = true
			return m, fetchWorkflowSettingsCmd(m.client, m.selectedWfDetail.ID)
		}
	case "y":
		if m.selectedWfDetail != nil && m.client != nil {
			m.wfSyncLoading = true
			m.wfSyncDone = false
			m.wfSyncResults = nil
			m.wfSyncProgress = "Syncing tests..."
			m.wfSyncSummary = ""
			ch, cmd := syncWorkflowTestsCmd(m.client, m.selectedWfDetail.ID, m.devMode)
			m.wfSyncCh = ch
			return m, cmd
		}
	}
	return m, nil
}

// handleWorkflowCreateKey processes key events in the workflow create wizard.
//
// Parameters:
//   - m: the hub model
//   - msg: the key message
//
// Returns:
//   - tea.Model: updated model
//   - tea.Cmd: next command
func handleWorkflowCreateKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.wfCreateStep {
	case wfCreateStepName:
		switch msg.String() {
		case "esc":
			m.currentView = viewWorkflowList
			return m, nil
		case "enter":
			if m.wfCreateNameInput.Value() != "" {
				m.wfCreateStep = wfCreateStepTests
				m.wfCreateNameInput.Blur()
				m.wfCreateTestCursor = 0
				m.wfCreateSelectedTests = make(map[string]bool)
				m.wfCreateTestFilterMode = false
				m.wfCreateTestFilterInput.SetValue("")
				m.wfCreateTestPlatformFilter = ""
			}
			return m, nil
		default:
			var cmd tea.Cmd
			m.wfCreateNameInput, cmd = m.wfCreateNameInput.Update(msg)
			return m, cmd
		}

	case wfCreateStepTests:
		visible := m.filteredCreateTests()

		if m.wfCreateTestFilterMode {
			switch msg.String() {
			case "esc":
				m.wfCreateTestFilterMode = false
				m.wfCreateTestFilterInput.Blur()
				m.wfCreateTestFilterInput.SetValue("")
				m.wfCreateTestCursor = 0
				return m, nil
			case "c":
				m.wfCreateTestFilterMode = false
				m.wfCreateTestFilterInput.Blur()
				m.wfCreateStep = wfCreateStepConfirm
				return m, nil
			case "enter":
				if m.wfCreateTestCursor < len(visible) {
					id := visible[m.wfCreateTestCursor].ID
					m.wfCreateSelectedTests[id] = !m.wfCreateSelectedTests[id]
				}
				return m, nil
			case "up", "k":
				if m.wfCreateTestCursor > 0 {
					m.wfCreateTestCursor--
				}
				return m, nil
			case "down", "j":
				if m.wfCreateTestCursor < len(visible)-1 {
					m.wfCreateTestCursor++
				}
				return m, nil
			default:
				var cmd tea.Cmd
				m.wfCreateTestFilterInput, cmd = m.wfCreateTestFilterInput.Update(msg)
				m.wfCreateTestCursor = 0
				return m, cmd
			}
		}

		switch msg.String() {
		case "esc":
			m.wfCreateStep = wfCreateStepName
			m.wfCreateNameInput.Focus()
			m.wfCreateTestFilterInput.SetValue("")
			m.wfCreateTestPlatformFilter = ""
			return m, textinput.Blink
		case "/":
			m.wfCreateTestFilterMode = true
			m.wfCreateTestFilterInput.Focus()
			return m, textinput.Blink
		case "p":
			switch m.wfCreateTestPlatformFilter {
			case "":
				m.wfCreateTestPlatformFilter = "Android"
			case "Android":
				m.wfCreateTestPlatformFilter = "iOS"
			default:
				m.wfCreateTestPlatformFilter = ""
			}
			m.wfCreateTestCursor = 0
			return m, nil
		case "up", "k":
			if m.wfCreateTestCursor > 0 {
				m.wfCreateTestCursor--
			}
		case "down", "j":
			if m.wfCreateTestCursor < len(visible)-1 {
				m.wfCreateTestCursor++
			}
		case " ":
			if m.wfCreateTestCursor < len(visible) {
				id := visible[m.wfCreateTestCursor].ID
				m.wfCreateSelectedTests[id] = !m.wfCreateSelectedTests[id]
			}
		case "enter":
			m.wfCreateStep = wfCreateStepConfirm
			return m, nil
		}
		return m, nil

	case wfCreateStepConfirm:
		switch msg.String() {
		case "esc":
			m.wfCreateStep = wfCreateStepTests
			return m, nil
		case "enter", "y":
			var testIDs []string
			for id, selected := range m.wfCreateSelectedTests {
				if selected {
					testIDs = append(testIDs, id)
				}
			}
			if len(testIDs) > 0 && m.client != nil {
				m.wfDetailLoading = true
				return m, createWorkflowAPICmd(m.client, m.wfCreateNameInput.Value(), testIDs)
			}
			return m, nil
		case "n":
			m.currentView = viewWorkflowList
			return m, nil
		}
		return m, nil
	}
	return m, nil
}

// handleWorkflowExecKey processes key events during workflow execution monitoring.
//
// Parameters:
//   - m: the hub model
//   - msg: the key message
//
// Returns:
//   - tea.Model: updated model
//   - tea.Cmd: next command
func handleWorkflowExecKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if !m.wfExecDone && m.wfExecTaskID != "" && m.client != nil {
			return m, cancelWorkflowCmd(m.client, m.wfExecTaskID)
		}
		return m, tea.Quit
	case "esc":
		if m.wfExecDone {
			returnTo := m.wfExecReturnView
			if returnTo == viewDashboard {
				returnTo = viewWorkflowDetail
			}
			m.currentView = returnTo
			return m, nil
		}
	case "o":
		if m.wfExecDone && m.selectedWfDetail != nil {
			wfURL := config.GetAppURL(m.devMode) + "/workflows/" + m.selectedWfDetail.ID
			_ = ui.OpenBrowser(wfURL)
		}
	}
	return m, nil
}

// --- Rendering ---

// renderWorkflowList renders the workflow browse list screen.
//
// Parameters:
//   - m: the hub model
//
// Returns:
//   - string: rendered output
func renderWorkflowList(m hubModel) string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render("Workflows")
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	if m.wfFilterMode {
		b.WriteString("\n  " + filterPromptStyle.Render("/") + " " + m.wfFilterInput.View() + "\n")
	}

	workflows := m.filteredWorkflowItems()
	countLabel := fmt.Sprintf("%d", len(workflows))
	if strings.TrimSpace(m.wfFilterInput.Value()) != "" {
		countLabel = fmt.Sprintf("%d/%d", len(workflows), len(m.wfItems))
	}
	b.WriteString(sectionStyle.Render(fmt.Sprintf("  Workflows  %s", countLabel)) + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	if m.wfListLoading {
		b.WriteString("  " + m.spinner.View() + " Loading...\n")
		return b.String()
	}

	if len(workflows) == 0 {
		if strings.TrimSpace(m.wfFilterInput.Value()) != "" {
			b.WriteString("  " + dimStyle.Render("No workflows match filter") + "\n")
		} else {
			b.WriteString("  " + dimStyle.Render("No workflows found") + "\n")
		}
	} else {
		start, end := scrollWindow(m.wfCursor, len(workflows), 12)
		for i := start; i < end; i++ {
			wf := workflows[i]
			cursor := "  "
			if i == m.wfCursor {
				cursor = selectedStyle.Render("▸ ")
			}
			name := normalStyle.Render(fmt.Sprintf("%-22s", wf.Name))

			statusStr := dimStyle.Render("--")
			timeStr := dimStyle.Render("never")
			if wf.LastRunStatus != "" {
				statusStr = statusStyle(wf.LastRunStatus).Render(wf.LastRunStatus)
				if !wf.LastRunTime.IsZero() {
					timeStr = dimStyle.Render(relativeTime(wf.LastRunTime))
				}
			}

			b.WriteString(fmt.Sprintf("  %s%s  %s  %s\n", cursor, name, statusStr, timeStr))
		}
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	keys := []string{
		helpKeyRender("enter", "detail"),
		helpKeyRender("r", "run"),
		helpKeyRender("c", "create"),
		helpKeyRender("/", "search"),
		helpKeyRender("R", "refresh"),
		helpKeyRender("esc", "back"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")

	return b.String()
}

// renderWorkflowDetail renders the workflow detail screen.
//
// Parameters:
//   - m: the hub model
//
// Returns:
//   - string: rendered output
func renderWorkflowDetail(m hubModel) string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	wf := m.selectedWfDetail
	if wf == nil || m.wfDetailLoading {
		bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render("Workflow")
		banner := headerBannerStyle.Width(innerW).Render(bannerContent)
		b.WriteString(banner + "\n")
		b.WriteString("  " + m.spinner.View() + " Loading...\n")
		return b.String()
	}

	// Header
	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render(wf.Name)
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	// Tests
	if len(wf.TestInfo) > 0 {
		b.WriteString(fmt.Sprintf("  %-12s %s\n",
			dimStyle.Render("Tests"),
			dimStyle.Render(fmt.Sprintf("(%d)", len(wf.TestInfo)))))
		for _, ti := range wf.TestInfo {
			b.WriteString(fmt.Sprintf("    %-22s %s\n",
				normalStyle.Render(ti.Name),
				dimStyle.Render(ti.Platform)))
		}
	} else {
		b.WriteString(fmt.Sprintf("  %-12s %s\n",
			dimStyle.Render("Tests"),
			dimStyle.Render(fmt.Sprintf("(%d)", wf.TestCount))))
	}

	if wf.LastRunStatus != "" {
		icon := statusIcon(wf.LastRunStatus)
		b.WriteString(fmt.Sprintf("  %-12s %s %s  %s\n",
			dimStyle.Render("Last Run"),
			icon,
			statusStyle(wf.LastRunStatus).Render(wf.LastRunStatus),
			dimStyle.Render(relativeTime(wf.LastRunTime))))
	} else {
		b.WriteString(fmt.Sprintf("  %-12s %s\n", dimStyle.Render("Last Run"), dimStyle.Render("no runs yet")))
	}

	// Delete confirmation
	if m.wfConfirmDelete {
		b.WriteString("\n  " + errorStyle.Render(fmt.Sprintf("Delete workflow \"%s\"? (y/n)", wf.Name)) + "\n")
		return b.String()
	}

	// Sync status
	if m.wfSyncLoading {
		b.WriteString("\n  " + dimStyle.Render(m.wfSyncProgress) + "\n")
		for _, line := range m.wfSyncResults {
			b.WriteString("    " + dimStyle.Render(line) + "\n")
		}
	} else if m.wfSyncDone {
		b.WriteString("\n  " + m.wfSyncSummary + "\n")
		visible := m.wfSyncResults
		if len(visible) > 8 {
			visible = visible[len(visible)-8:]
		}
		for _, line := range visible {
			b.WriteString("    " + dimStyle.Render(line) + "\n")
		}
	}

	// Actions
	b.WriteString("\n")
	b.WriteString(sectionStyle.Render("  ACTIONS") + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	for i, a := range workflowDetailActions {
		cursor := "  "
		descStyle := dimStyle
		if i == m.wfDetailCursor {
			cursor = selectedStyle.Render("▸ ")
			descStyle = normalStyle
		}
		num := lipgloss.NewStyle().Foreground(purple).Bold(true).Render(fmt.Sprintf("[%d]", i+1))
		b.WriteString(fmt.Sprintf("  %s%s %s\n", cursor, num, descStyle.Render(a.Desc)))
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	jumpLabel := fmt.Sprintf("1-%d", len(workflowDetailActions))
	keys := []string{
		helpKeyRender("↑/↓", "move"),
		helpKeyRender("enter", "select"),
		helpKeyRender(jumpLabel, "jump"),
		helpKeyRender("esc", "back"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")

	return b.String()
}

// renderWorkflowCreate renders the workflow create wizard.
//
// Parameters:
//   - m: the hub model
//
// Returns:
//   - string: rendered output
func renderWorkflowCreate(m hubModel) string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render("Create Workflow")
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	switch m.wfCreateStep {
	case wfCreateStepName:
		b.WriteString(sectionStyle.Render("  Step 1: Name") + "\n")
		b.WriteString("  " + separator(innerW) + "\n")
		b.WriteString("  " + m.wfCreateNameInput.View() + "\n")
		b.WriteString("\n  " + dimStyle.Render("enter to continue, esc to cancel") + "\n")

	case wfCreateStepTests:
		b.WriteString(sectionStyle.Render("  Step 2: Select Tests") + "\n")
		b.WriteString("  " + separator(innerW) + "\n")

		if m.wfCreateTestFilterMode {
			b.WriteString("  " + filterPromptStyle.Render("/") + " " + m.wfCreateTestFilterInput.View() + "\n")
		} else if strings.TrimSpace(m.wfCreateTestFilterInput.Value()) != "" {
			b.WriteString("  " + filterPromptStyle.Render("/") + " " + dimStyle.Render(m.wfCreateTestFilterInput.Value()) + "\n")
		}

		platformLabel := "All"
		if m.wfCreateTestPlatformFilter != "" {
			platformLabel = m.wfCreateTestPlatformFilter
		}
		b.WriteString("  " + dimStyle.Render("Platform: ") + normalStyle.Render(platformLabel) + "\n")

		visible := m.filteredCreateTests()
		if len(visible) != len(m.tests) {
			b.WriteString("  " + dimStyle.Render(fmt.Sprintf("%d/%d tests", len(visible), len(m.tests))) + "\n")
		}

		if len(m.tests) == 0 {
			b.WriteString("  " + dimStyle.Render("No tests available") + "\n")
		} else if len(visible) == 0 {
			b.WriteString("  " + dimStyle.Render("No tests match filter") + "\n")
		} else {
			start, end := scrollWindow(m.wfCreateTestCursor, len(visible), 12)
			for i := start; i < end; i++ {
				t := visible[i]
				cursor := "  "
				if i == m.wfCreateTestCursor {
					cursor = selectedStyle.Render("▸ ")
				}
				check := "[ ]"
				if m.wfCreateSelectedTests[t.ID] {
					check = successStyle.Render("[✓]")
				}
				b.WriteString(fmt.Sprintf("  %s%s %s  %s\n", cursor, check, normalStyle.Render(t.Name), dimStyle.Render(t.Platform)))
			}
		}

		var selectedCount int
		for _, sel := range m.wfCreateSelectedTests {
			if sel {
				selectedCount++
			}
		}
		if selectedCount > 0 {
			b.WriteString("  " + successStyle.Render(fmt.Sprintf("%d selected", selectedCount)) + "\n")
		}

		b.WriteString("\n  ")
		var keys []string
		if m.wfCreateTestFilterMode {
			keys = []string{
				helpKeyRender("enter", "toggle"),
				helpKeyRender("↑/↓", "navigate"),
				helpKeyRender("c", "continue"),
				helpKeyRender("esc", "clear"),
			}
		} else {
			keys = []string{
				helpKeyRender("/", "search"),
				helpKeyRender("p", "platform"),
				helpKeyRender("space", "toggle"),
				helpKeyRender("enter", "confirm"),
				helpKeyRender("esc", "back"),
			}
		}
		b.WriteString(strings.Join(keys, "  ") + "\n")

	case wfCreateStepConfirm:
		b.WriteString(sectionStyle.Render("  Step 3: Confirm") + "\n")
		b.WriteString("  " + separator(innerW) + "\n")

		b.WriteString(fmt.Sprintf("  Name: %s\n", normalStyle.Render(m.wfCreateNameInput.Value())))

		var selectedNames []string
		for _, t := range m.tests {
			if m.wfCreateSelectedTests[t.ID] {
				selectedNames = append(selectedNames, t.Name)
			}
		}
		b.WriteString(fmt.Sprintf("  Tests: %s (%d)\n", dimStyle.Render(strings.Join(selectedNames, ", ")), len(selectedNames)))

		b.WriteString("\n  " + normalStyle.Render("Create this workflow? (enter/y to confirm, n to cancel)") + "\n")
	}

	return b.String()
}

// renderWorkflowExecution renders the workflow execution monitor screen.
//
// Parameters:
//   - m: the hub model
//
// Returns:
//   - string: rendered output
func renderWorkflowExecution(m hubModel) string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	wfName := "Workflow"
	if m.selectedWfDetail != nil {
		wfName = m.selectedWfDetail.Name
	}

	if m.wfExecStatus == nil && !m.wfExecDone {
		bannerContent := titleStyle.Render("REVYL") + "  " + runningStyle.Render("Starting: "+wfName)
		banner := headerBannerStyle.Width(innerW).Render(bannerContent)
		b.WriteString(banner + "\n")
		b.WriteString("  " + m.spinner.View() + " Starting workflow...\n")
		return b.String()
	}

	status := m.wfExecStatus
	if status == nil {
		return b.String()
	}

	// Header
	stateStyle := runningStyle
	stateLabel := "Running"
	if m.wfExecDone {
		switch strings.ToLower(status.Status) {
		case "passed", "completed", "success":
			stateStyle = successStyle
			stateLabel = "Passed"
		case "failed", "error":
			stateStyle = errorStyle
			stateLabel = "Failed"
		case "cancelled":
			stateStyle = warningStyle
			stateLabel = "Cancelled"
		default:
			stateLabel = status.Status
		}
	}

	bannerContent := titleStyle.Render("REVYL") + "  " + stateStyle.Render(stateLabel+": "+wfName)
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	b.WriteString("  " + separator(innerW) + "\n")

	// Progress summary
	completed := status.CompletedTests
	total := status.TotalTests
	passed := status.PassedTests
	failed := status.FailedTests

	b.WriteString(fmt.Sprintf("  Overall  %s  %s  %s\n",
		normalStyle.Render(fmt.Sprintf("%d/%d tests complete", completed, total)),
		successStyle.Render(fmt.Sprintf("%d passed", passed)),
		errorStyle.Render(fmt.Sprintf("%d failed", failed)),
	))

	// Progress bar
	barWidth := innerW - 4
	if barWidth > 0 && total > 0 {
		filled := int(float64(barWidth) * float64(completed) / float64(total))
		if filled > barWidth {
			filled = barWidth
		}
		bar := stateStyle.Render(strings.Repeat("█", filled)) +
			dimStyle.Render(strings.Repeat("░", barWidth-filled))
		b.WriteString("  " + bar + "\n")
	}

	// Elapsed time
	elapsed := time.Since(m.wfExecStartTime).Round(time.Second)
	if status.Duration != "" {
		b.WriteString(fmt.Sprintf("  %s\n", dimStyle.Render("Duration: "+status.Duration)))
	} else {
		b.WriteString(fmt.Sprintf("  %s\n", dimStyle.Render(fmt.Sprintf("Elapsed: %s", elapsed))))
	}

	// Footer
	b.WriteString("\n  " + separator(innerW) + "\n")
	if m.wfExecDone {
		keys := []string{
			helpKeyRender("o", "open report"),
			helpKeyRender("esc", "back"),
		}
		b.WriteString("  " + strings.Join(keys, "  ") + "\n")
	} else {
		b.WriteString("  " + helpKeyRender("ctrl+c", "cancel") + "\n")
	}

	return b.String()
}

// --- Workflow Settings Screen ---

const (
	wfSettingsSectionTests    = 0
	wfSettingsSectionApp      = 1
	wfSettingsSectionLocation = 2
	wfSettingsSectionConfig   = 3
	wfSettingsSectionCount    = 4
)

// fetchWorkflowSettingsCmd loads the data needed for the workflow settings screen:
// the full workflow definition and all org tests (for the toggle list).
//
// Parameters:
//   - client: the API client
//   - workflowID: the workflow to configure
//
// Returns:
//   - tea.Cmd: command producing WorkflowSettingsMsg
func fetchWorkflowSettingsCmd(client *api.Client, workflowID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		wf, err := client.GetWorkflow(ctx, workflowID)
		if err != nil {
			return WorkflowSettingsMsg{Err: err}
		}

		testsResp, tErr := client.ListOrgTests(ctx, 200, 0)
		var allTests []TestItem
		if tErr == nil {
			for _, t := range testsResp.Tests {
				allTests = append(allTests, TestItem{
					ID:       t.ID,
					Name:     t.Name,
					Platform: t.Platform,
				})
			}
		}

		var allApps []api.App
		appsResp, aErr := client.ListApps(ctx, "", 1, 100)
		if aErr == nil {
			allApps = appsResp.Items
		}

		return WorkflowSettingsMsg{Workflow: wf, AllTests: allTests, AllApps: allApps}
	}
}

// saveWorkflowSettingsCmd persists the updated test list for a workflow.
//
// Parameters:
//   - client: the API client
//   - workflowID: the workflow to update
//   - testIDs: the new test list
//
// Returns:
//   - tea.Cmd: command producing WorkflowSettingsSavedMsg
func saveWorkflowSettingsCmd(client *api.Client, workflowID string, testIDs []string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := client.UpdateWorkflowTests(ctx, workflowID, testIDs)
		return WorkflowSettingsSavedMsg{Err: err}
	}
}

// saveWorkflowOverridesCmd persists app/location/run config overrides.
//
// Parameters:
//   - client: the API client
//   - workflowID: the workflow to update
//   - wf: the current workflow state with overrides
//
// Returns:
//   - tea.Cmd: command producing WorkflowSettingsSavedMsg
func saveWorkflowOverridesCmd(client *api.Client, workflowID string, wf *api.Workflow) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if err := client.UpdateWorkflowBuildConfig(ctx, workflowID, wf.BuildConfig, wf.OverrideBuildConfig); err != nil {
			return WorkflowSettingsSavedMsg{Err: err}
		}
		if err := client.UpdateWorkflowLocationConfig(ctx, workflowID, wf.LocationConfig, wf.OverrideLocation); err != nil {
			return WorkflowSettingsSavedMsg{Err: err}
		}
		if wf.RunConfig != nil {
			if err := client.UpdateWorkflowRunConfig(ctx, workflowID, wf.RunConfig); err != nil {
				return WorkflowSettingsSavedMsg{Err: err}
			}
		}

		return WorkflowSettingsSavedMsg{}
	}
}

// handleWorkflowSettingsKey processes key events on the workflow settings screen.
//
// Parameters:
//   - m: the hub model
//   - msg: the key message
//
// Returns:
//   - tea.Model: updated model
//   - tea.Cmd: next command
//
// isAppEditField returns true when the current edit targets an app override.
func isAppEditField(field string) bool {
	return field == "ios_app" || field == "android_app"
}

// filterAppsByQuery returns apps whose name contains the query (case-insensitive).
func filterAppsByQuery(apps []api.App, query string) []api.App {
	if query == "" {
		return apps
	}
	q := strings.ToLower(query)
	var out []api.App
	for _, a := range apps {
		if strings.Contains(strings.ToLower(a.Name), q) {
			out = append(out, a)
		}
	}
	return out
}

// filterAppsByPlatform returns apps matching the given platform.
func filterAppsByPlatform(apps []api.App, platform string) []api.App {
	if platform == "" {
		return apps
	}
	p := strings.ToLower(platform)
	var out []api.App
	for _, a := range apps {
		if strings.ToLower(a.Platform) == p {
			out = append(out, a)
		}
	}
	return out
}

// renderAppAutocompleteList renders the filtered app matches below the input.
func renderAppAutocompleteList(b *strings.Builder, m hubModel) {
	if len(m.wfSettingsAppMatches) == 0 {
		b.WriteString("    " + dimStyle.Render("No matching apps") + "\n")
		return
	}
	maxVisible := 5
	start, end := scrollWindow(m.wfSettingsAppCursor, len(m.wfSettingsAppMatches), maxVisible)
	for i := start; i < end; i++ {
		app := m.wfSettingsAppMatches[i]
		cursor := "  "
		nameStyle := dimStyle
		if i == m.wfSettingsAppCursor {
			cursor = selectedStyle.Render("▸ ")
			nameStyle = normalStyle
		}
		b.WriteString(fmt.Sprintf("    %s%s  %s\n", cursor,
			nameStyle.Render(app.Name),
			dimStyle.Render(app.Platform)))
	}
}

// resolveAppName looks up an app ID in the loaded apps list and returns
// the app's display name, or the raw ID if not found.
func resolveAppName(apps []api.App, appID string) string {
	for _, a := range apps {
		if a.ID == appID {
			return a.Name
		}
	}
	return appID
}

// refreshAppMatches re-filters the app list based on the current input value.
func refreshAppMatches(m *hubModel) {
	platform := ""
	if m.wfSettingsEditField == "ios_app" {
		platform = "ios"
	} else if m.wfSettingsEditField == "android_app" {
		platform = "android"
	}
	platformApps := filterAppsByPlatform(m.wfSettingsApps, platform)
	m.wfSettingsAppMatches = filterAppsByQuery(platformApps, m.wfSettingsInput.Value())
	if m.wfSettingsAppCursor >= len(m.wfSettingsAppMatches) {
		m.wfSettingsAppCursor = max(0, len(m.wfSettingsAppMatches)-1)
	}
}

func handleWorkflowSettingsKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.wfSettingsEditing {
		if isAppEditField(m.wfSettingsEditField) {
			switch msg.String() {
			case "esc":
				m.wfSettingsEditing = false
				m.wfSettingsInput.Blur()
				return m, nil
			case "enter":
				m.wfSettingsEditing = false
				m.wfSettingsInput.Blur()
				applySettingsEdit(&m)
				return m, nil
			case "up", "ctrl+p":
				if m.wfSettingsAppCursor > 0 {
					m.wfSettingsAppCursor--
				}
				return m, nil
			case "down", "ctrl+n":
				if m.wfSettingsAppCursor < len(m.wfSettingsAppMatches)-1 {
					m.wfSettingsAppCursor++
				}
				return m, nil
			default:
				var cmd tea.Cmd
				m.wfSettingsInput, cmd = m.wfSettingsInput.Update(msg)
				refreshAppMatches(&m)
				return m, cmd
			}
		}

		switch msg.String() {
		case "esc":
			m.wfSettingsEditing = false
			m.wfSettingsInput.Blur()
			return m, nil
		case "enter":
			m.wfSettingsEditing = false
			m.wfSettingsInput.Blur()
			applySettingsEdit(&m)
			return m, nil
		default:
			var cmd tea.Cmd
			m.wfSettingsInput, cmd = m.wfSettingsInput.Update(msg)
			return m, cmd
		}
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.currentView = viewWorkflowDetail
		m.wfSettingsWorkflow = nil
		return m, nil
	case "tab":
		m.wfSettingsSection = (m.wfSettingsSection + 1) % wfSettingsSectionCount
		m.wfSettingsCursor = 0
		return m, nil
	case "shift+tab":
		m.wfSettingsSection = (m.wfSettingsSection - 1 + wfSettingsSectionCount) % wfSettingsSectionCount
		m.wfSettingsCursor = 0
		return m, nil
	case "up", "k":
		if m.wfSettingsCursor > 0 {
			m.wfSettingsCursor--
		}
	case "down", "j":
		maxCursor := settingsSectionMaxCursor(m)
		if m.wfSettingsCursor < maxCursor {
			m.wfSettingsCursor++
		}
	case " ":
		if m.wfSettingsSection == wfSettingsSectionTests {
			toggleSettingsTest(&m)
		}
	case "enter":
		if m.wfSettingsSection != wfSettingsSectionTests {
			startSettingsEdit(&m)
			return m, textinput.Blink
		}
	case "s":
		if m.wfSettingsDirty && m.selectedWfDetail != nil && m.client != nil {
			m.wfSettingsLoading = true
			testIDs := collectToggledTestIDs(m)
			return m, tea.Batch(
				saveWorkflowSettingsCmd(m.client, m.selectedWfDetail.ID, testIDs),
				saveWorkflowOverridesCmd(m.client, m.selectedWfDetail.ID, m.wfSettingsWorkflow),
			)
		}
	}
	return m, nil
}

// settingsSectionMaxCursor returns the maximum cursor index for the current section.
func settingsSectionMaxCursor(m hubModel) int {
	switch m.wfSettingsSection {
	case wfSettingsSectionTests:
		return max(0, len(m.wfSettingsAllTests)-1)
	case wfSettingsSectionApp:
		return 1
	case wfSettingsSectionLocation:
		return 1
	case wfSettingsSectionConfig:
		return 1
	}
	return 0
}

// toggleSettingsTest toggles a test's inclusion in the workflow.
func toggleSettingsTest(m *hubModel) {
	if m.wfSettingsCursor < len(m.wfSettingsAllTests) {
		tid := m.wfSettingsAllTests[m.wfSettingsCursor].ID
		m.wfSettingsTestToggle[tid] = !m.wfSettingsTestToggle[tid]
		m.wfSettingsDirty = true
	}
}

// collectToggledTestIDs returns the list of test IDs currently toggled on.
func collectToggledTestIDs(m hubModel) []string {
	var ids []string
	for _, t := range m.wfSettingsAllTests {
		if m.wfSettingsTestToggle[t.ID] {
			ids = append(ids, t.ID)
		}
	}
	return ids
}

// startSettingsEdit enters edit mode for the current field.
func startSettingsEdit(m *hubModel) {
	m.wfSettingsEditing = true
	m.wfSettingsInput = textinput.New()
	m.wfSettingsInput.Focus()

	wf := m.wfSettingsWorkflow
	if wf == nil {
		return
	}

	switch m.wfSettingsSection {
	case wfSettingsSectionApp:
		currentAppID := ""
		if m.wfSettingsCursor == 0 {
			m.wfSettingsEditField = "ios_app"
			m.wfSettingsInput.Placeholder = "Search iOS apps..."
			if wf.BuildConfig != nil {
				if iosBuild, ok := wf.BuildConfig["ios_build"].(map[string]interface{}); ok {
					if id, ok := iosBuild["app_id"].(string); ok {
						currentAppID = id
					}
				}
			}
		} else {
			m.wfSettingsEditField = "android_app"
			m.wfSettingsInput.Placeholder = "Search Android apps..."
			if wf.BuildConfig != nil {
				if androidBuild, ok := wf.BuildConfig["android_build"].(map[string]interface{}); ok {
					if id, ok := androidBuild["app_id"].(string); ok {
						currentAppID = id
					}
				}
			}
		}
		refreshAppMatches(m)
		m.wfSettingsAppCursor = 0
		if currentAppID != "" {
			for i, a := range m.wfSettingsAppMatches {
				if a.ID == currentAppID {
					m.wfSettingsAppCursor = i
					break
				}
			}
		}
	case wfSettingsSectionLocation:
		if m.wfSettingsCursor == 0 {
			m.wfSettingsEditField = "latitude"
			m.wfSettingsInput.Placeholder = "Latitude (e.g. 37.7749)"
			if wf.LocationConfig != nil {
				if lat, ok := wf.LocationConfig["latitude"]; ok {
					m.wfSettingsInput.SetValue(fmt.Sprintf("%v", lat))
				}
			}
		} else {
			m.wfSettingsEditField = "longitude"
			m.wfSettingsInput.Placeholder = "Longitude (e.g. -122.4194)"
			if wf.LocationConfig != nil {
				if lng, ok := wf.LocationConfig["longitude"]; ok {
					m.wfSettingsInput.SetValue(fmt.Sprintf("%v", lng))
				}
			}
		}
	case wfSettingsSectionConfig:
		if m.wfSettingsCursor == 0 {
			m.wfSettingsEditField = "parallelism"
			m.wfSettingsInput.Placeholder = "Parallelism (e.g. 2)"
			if wf.RunConfig != nil && wf.RunConfig.Parallelism > 0 {
				m.wfSettingsInput.SetValue(fmt.Sprintf("%d", wf.RunConfig.Parallelism))
			}
		} else {
			m.wfSettingsEditField = "max_retries"
			m.wfSettingsInput.Placeholder = "Max retries (e.g. 3)"
			if wf.RunConfig != nil && wf.RunConfig.MaxRetries > 0 {
				m.wfSettingsInput.SetValue(fmt.Sprintf("%d", wf.RunConfig.MaxRetries))
			}
		}
	}
}

// applySettingsEdit applies the edited value back to the workflow model.
func applySettingsEdit(m *hubModel) {
	val := strings.TrimSpace(m.wfSettingsInput.Value())
	wf := m.wfSettingsWorkflow
	if wf == nil {
		return
	}

	switch m.wfSettingsEditField {
	case "ios_app":
		if wf.BuildConfig == nil {
			wf.BuildConfig = make(map[string]interface{})
		}
		appID := ""
		if m.wfSettingsAppCursor < len(m.wfSettingsAppMatches) {
			appID = m.wfSettingsAppMatches[m.wfSettingsAppCursor].ID
		}
		if appID != "" {
			wf.BuildConfig["ios_build"] = map[string]interface{}{"app_id": appID}
			wf.OverrideBuildConfig = true
		} else {
			delete(wf.BuildConfig, "ios_build")
			wf.OverrideBuildConfig = len(wf.BuildConfig) > 0
		}
		m.wfSettingsDirty = true
	case "android_app":
		if wf.BuildConfig == nil {
			wf.BuildConfig = make(map[string]interface{})
		}
		appID := ""
		if m.wfSettingsAppCursor < len(m.wfSettingsAppMatches) {
			appID = m.wfSettingsAppMatches[m.wfSettingsAppCursor].ID
		}
		if appID != "" {
			wf.BuildConfig["android_build"] = map[string]interface{}{"app_id": appID}
			wf.OverrideBuildConfig = true
		} else {
			delete(wf.BuildConfig, "android_build")
			wf.OverrideBuildConfig = len(wf.BuildConfig) > 0
		}
		m.wfSettingsDirty = true
	case "latitude":
		if wf.LocationConfig == nil {
			wf.LocationConfig = make(map[string]interface{})
		}
		if val == "" {
			delete(wf.LocationConfig, "latitude")
		} else {
			wf.LocationConfig["latitude"] = val
		}
		wf.OverrideLocation = len(wf.LocationConfig) > 0
		m.wfSettingsDirty = true
	case "longitude":
		if wf.LocationConfig == nil {
			wf.LocationConfig = make(map[string]interface{})
		}
		if val == "" {
			delete(wf.LocationConfig, "longitude")
		} else {
			wf.LocationConfig["longitude"] = val
		}
		wf.OverrideLocation = len(wf.LocationConfig) > 0
		m.wfSettingsDirty = true
	case "parallelism":
		if wf.RunConfig == nil {
			wf.RunConfig = &api.WorkflowRunConfig{}
		}
		n := 0
		fmt.Sscanf(val, "%d", &n)
		wf.RunConfig.Parallelism = n
		m.wfSettingsDirty = true
	case "max_retries":
		if wf.RunConfig == nil {
			wf.RunConfig = &api.WorkflowRunConfig{}
		}
		n := 0
		fmt.Sscanf(val, "%d", &n)
		wf.RunConfig.MaxRetries = n
		m.wfSettingsDirty = true
	}
}

// renderWorkflowSettings renders the workflow settings screen.
//
// Parameters:
//   - m: the hub model
//
// Returns:
//   - string: rendered output
func renderWorkflowSettings(m hubModel) string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	wfName := "Workflow"
	if m.selectedWfDetail != nil {
		wfName = m.selectedWfDetail.Name
	}

	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render("Settings: "+wfName)
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	if m.wfSettingsLoading {
		b.WriteString("  " + m.spinner.View() + " Loading...\n")
		return b.String()
	}

	wf := m.wfSettingsWorkflow

	// --- Tests section ---
	selectedCount := 0
	for _, on := range m.wfSettingsTestToggle {
		if on {
			selectedCount++
		}
	}

	testHeader := fmt.Sprintf("TESTS (%d/%d)", selectedCount, len(m.wfSettingsAllTests))
	if m.wfSettingsSection == wfSettingsSectionTests {
		b.WriteString("\n" + sectionStyle.Render("  "+testHeader) + "  " + selectedStyle.Render("◀") + "\n")
	} else {
		b.WriteString("\n" + sectionStyle.Render("  "+testHeader) + "\n")
	}
	b.WriteString("  " + separator(innerW) + "\n")

	if len(m.wfSettingsAllTests) == 0 {
		b.WriteString("  " + dimStyle.Render("No tests available") + "\n")
	} else {
		start, end := scrollWindow(m.wfSettingsCursor, len(m.wfSettingsAllTests), 8)
		for i := start; i < end; i++ {
			t := m.wfSettingsAllTests[i]
			cursor := "  "
			if m.wfSettingsSection == wfSettingsSectionTests && i == m.wfSettingsCursor {
				cursor = selectedStyle.Render("▸ ")
			}
			check := "[ ]"
			if m.wfSettingsTestToggle[t.ID] {
				check = successStyle.Render("[✓]")
			}
			b.WriteString(fmt.Sprintf("  %s%s %-22s %s\n", cursor, check,
				normalStyle.Render(t.Name), dimStyle.Render(t.Platform)))
		}
	}

	// --- App Overrides section ---
	if m.wfSettingsSection == wfSettingsSectionApp {
		b.WriteString("\n" + sectionStyle.Render("  APP OVERRIDES") + "  " + selectedStyle.Render("◀") + "\n")
	} else {
		b.WriteString("\n" + sectionStyle.Render("  APP OVERRIDES") + "\n")
	}
	b.WriteString("  " + separator(innerW) + "\n")

	iosApp := dimStyle.Render("(not set)")
	androidApp := dimStyle.Render("(not set)")
	if wf != nil && wf.OverrideBuildConfig && wf.BuildConfig != nil {
		if iosBuild, ok := wf.BuildConfig["ios_build"].(map[string]interface{}); ok {
			if appID, ok := iosBuild["app_id"].(string); ok && appID != "" {
				iosApp = normalStyle.Render(resolveAppName(m.wfSettingsApps, appID))
			}
		}
		if androidBuild, ok := wf.BuildConfig["android_build"].(map[string]interface{}); ok {
			if appID, ok := androidBuild["app_id"].(string); ok && appID != "" {
				androidApp = normalStyle.Render(resolveAppName(m.wfSettingsApps, appID))
			}
		}
	}

	iosCursor := "  "
	androidCursor := "  "
	editingIOS := false
	editingAndroid := false
	if m.wfSettingsSection == wfSettingsSectionApp {
		if m.wfSettingsCursor == 0 {
			iosCursor = selectedStyle.Render("▸ ")
			if m.wfSettingsEditing && m.wfSettingsEditField == "ios_app" {
				editingIOS = true
				iosApp = m.wfSettingsInput.View()
			}
		} else {
			androidCursor = selectedStyle.Render("▸ ")
			if m.wfSettingsEditing && m.wfSettingsEditField == "android_app" {
				editingAndroid = true
				androidApp = m.wfSettingsInput.View()
			}
		}
	}
	b.WriteString(fmt.Sprintf("  %s%-12s %s\n", iosCursor, dimStyle.Render("iOS:"), iosApp))
	if editingIOS {
		renderAppAutocompleteList(&b, m)
	}
	b.WriteString(fmt.Sprintf("  %s%-12s %s\n", androidCursor, dimStyle.Render("Android:"), androidApp))
	if editingAndroid {
		renderAppAutocompleteList(&b, m)
	}

	// --- Location section ---
	if m.wfSettingsSection == wfSettingsSectionLocation {
		b.WriteString("\n" + sectionStyle.Render("  LOCATION OVERRIDE") + "  " + selectedStyle.Render("◀") + "\n")
	} else {
		b.WriteString("\n" + sectionStyle.Render("  LOCATION OVERRIDE") + "\n")
	}
	b.WriteString("  " + separator(innerW) + "\n")

	latVal := dimStyle.Render("(not set)")
	lngVal := dimStyle.Render("(not set)")
	if wf != nil && wf.OverrideLocation && wf.LocationConfig != nil {
		if lat, ok := wf.LocationConfig["latitude"]; ok {
			latVal = normalStyle.Render(fmt.Sprintf("%v", lat))
		}
		if lng, ok := wf.LocationConfig["longitude"]; ok {
			lngVal = normalStyle.Render(fmt.Sprintf("%v", lng))
		}
	}

	latCursor := "  "
	lngCursor := "  "
	if m.wfSettingsSection == wfSettingsSectionLocation {
		if m.wfSettingsCursor == 0 {
			latCursor = selectedStyle.Render("▸ ")
			if m.wfSettingsEditing {
				latVal = m.wfSettingsInput.View()
			}
		} else {
			lngCursor = selectedStyle.Render("▸ ")
			if m.wfSettingsEditing {
				lngVal = m.wfSettingsInput.View()
			}
		}
	}
	b.WriteString(fmt.Sprintf("  %s%-12s %s\n", latCursor, dimStyle.Render("Latitude:"), latVal))
	b.WriteString(fmt.Sprintf("  %s%-12s %s\n", lngCursor, dimStyle.Render("Longitude:"), lngVal))

	// --- Run Config section ---
	if m.wfSettingsSection == wfSettingsSectionConfig {
		b.WriteString("\n" + sectionStyle.Render("  RUN CONFIG") + "  " + selectedStyle.Render("◀") + "\n")
	} else {
		b.WriteString("\n" + sectionStyle.Render("  RUN CONFIG") + "\n")
	}
	b.WriteString("  " + separator(innerW) + "\n")

	parallelVal := dimStyle.Render("(default)")
	retriesVal := dimStyle.Render("(default)")
	if wf != nil && wf.RunConfig != nil {
		if wf.RunConfig.Parallelism > 0 {
			parallelVal = normalStyle.Render(fmt.Sprintf("%d", wf.RunConfig.Parallelism))
		}
		if wf.RunConfig.MaxRetries > 0 {
			retriesVal = normalStyle.Render(fmt.Sprintf("%d", wf.RunConfig.MaxRetries))
		}
	}

	parCursor := "  "
	retCursor := "  "
	if m.wfSettingsSection == wfSettingsSectionConfig {
		if m.wfSettingsCursor == 0 {
			parCursor = selectedStyle.Render("▸ ")
			if m.wfSettingsEditing {
				parallelVal = m.wfSettingsInput.View()
			}
		} else {
			retCursor = selectedStyle.Render("▸ ")
			if m.wfSettingsEditing {
				retriesVal = m.wfSettingsInput.View()
			}
		}
	}
	b.WriteString(fmt.Sprintf("  %s%-14s %s\n", parCursor, dimStyle.Render("Parallelism:"), parallelVal))
	b.WriteString(fmt.Sprintf("  %s%-14s %s\n", retCursor, dimStyle.Render("Max Retries:"), retriesVal))

	// --- Footer ---
	b.WriteString("\n  " + separator(innerW) + "\n")
	keys := []string{
		helpKeyRender("space", "toggle"),
		helpKeyRender("tab", "section"),
		helpKeyRender("enter", "edit"),
	}
	if m.wfSettingsDirty {
		keys = append(keys, helpKeyRender("s", "save"))
	}
	keys = append(keys, helpKeyRender("esc", "back"))
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")

	return b.String()
}
