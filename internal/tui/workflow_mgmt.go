// Package tui provides the workflow management screens: list, detail, create wizard,
// and execution monitor.
//
// Workflows can be browsed, created with a multi-step wizard, executed with inline
// progress monitoring, and deleted from the TUI.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// workflowCreateStep tracks the current step in the workflow create wizard.
type workflowCreateStep int

const (
	wfCreateStepName    workflowCreateStep = iota // name input
	wfCreateStepTests                             // multi-select tests
	wfCreateStepConfirm                           // confirm + create
)

// --- Commands ---

// fetchWorkflowBrowseListCmd fetches the workflow list enriched with last-run info.
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
				ID:   w.ID,
				Name: w.Name,
			}

			// Fetch full workflow to get test list
			full, wErr := client.GetWorkflow(ctx, w.ID)
			if wErr == nil && full != nil {
				item.TestCount = len(full.Tests)
				item.TestNames = full.Tests // These are IDs, displayed as count
			}

			items = append(items, item)
		}

		return WorkflowBrowseListMsg{Workflows: items}
	}
}

// fetchWorkflowDetailCmd fetches a single workflow's detail.
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
		resp, err := client.CreateWorkflow(ctx, &api.CLICreateWorkflowRequest{
			Name:  name,
			Tests: testIDs,
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
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.currentView = viewDashboard
		return m, nil
	case "up", "k":
		if m.wfCursor > 0 {
			m.wfCursor--
		}
	case "down", "j":
		if m.wfCursor < len(m.wfItems)-1 {
			m.wfCursor++
		}
	case "enter":
		if m.wfCursor < len(m.wfItems) && m.client != nil {
			m.wfDetailLoading = true
			m.currentView = viewWorkflowDetail
			return m, fetchWorkflowDetailCmd(m.client, m.wfItems[m.wfCursor].ID)
		}
	case "c":
		// Start create wizard
		m.currentView = viewWorkflowCreate
		m.wfCreateStep = wfCreateStepName
		m.wfCreateNameInput.SetValue("")
		m.wfCreateNameInput.Focus()
		m.wfCreateSelectedTests = nil
		return m, textinput.Blink
	}
	return m, nil
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

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.currentView = viewWorkflowList
		m.selectedWfDetail = nil
		return m, nil
	case "r":
		if m.selectedWfDetail != nil && m.client != nil {
			m.currentView = viewWorkflowExecution
			m.wfExecStatus = nil
			m.wfExecDone = false
			m.wfExecStartTime = time.Now()
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
			m.currentView = viewWorkflowRuns
			m.reportLoading = true
			if m.client != nil {
				return m, fetchWorkflowHistoryCmd(m.client, m.selectedWfDetail.ID)
			}
		}
	case "x":
		m.wfConfirmDelete = true
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
				m.wfCreateSelectedTests = make(map[int]bool)
			}
			return m, nil
		default:
			var cmd tea.Cmd
			m.wfCreateNameInput, cmd = m.wfCreateNameInput.Update(msg)
			return m, cmd
		}

	case wfCreateStepTests:
		switch msg.String() {
		case "esc":
			m.wfCreateStep = wfCreateStepName
			m.wfCreateNameInput.Focus()
			return m, textinput.Blink
		case "up", "k":
			if m.wfCreateTestCursor > 0 {
				m.wfCreateTestCursor--
			}
		case "down", "j":
			if m.wfCreateTestCursor < len(m.tests)-1 {
				m.wfCreateTestCursor++
			}
		case " ":
			if m.wfCreateTestCursor < len(m.tests) {
				m.wfCreateSelectedTests[m.wfCreateTestCursor] = !m.wfCreateSelectedTests[m.wfCreateTestCursor]
			}
		case "enter":
			// Move to confirm step
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
			// Collect selected test IDs
			var testIDs []string
			for idx, selected := range m.wfCreateSelectedTests {
				if selected && idx < len(m.tests) {
					testIDs = append(testIDs, m.tests[idx].ID)
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
			m.currentView = viewWorkflowDetail
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

	b.WriteString(sectionStyle.Render(fmt.Sprintf("  Workflows  %d", len(m.wfItems))) + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	if m.wfListLoading {
		b.WriteString("  " + m.spinner.View() + " Loading...\n")
		return b.String()
	}

	if len(m.wfItems) == 0 {
		b.WriteString("  " + dimStyle.Render("No workflows found") + "\n")
	} else {
		start, end := scrollWindow(m.wfCursor, len(m.wfItems), 12)
		for i := start; i < end; i++ {
			wf := m.wfItems[i]
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
		helpKeyRender("c", "create"),
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

	// Info
	b.WriteString(fmt.Sprintf("  %-12s %s (%d)\n",
		dimStyle.Render("Tests"),
		dimStyle.Render("test IDs"),
		wf.TestCount))

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

	// Actions
	b.WriteString("\n")
	b.WriteString(sectionStyle.Render("  ACTIONS") + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	actions := []struct{ key, desc string }{
		{"r", "Run workflow"},
		{"o", "Open in browser"},
		{"h", "Run history"},
		{"x", "Delete workflow"},
	}
	for _, a := range actions {
		key := lipgloss.NewStyle().Foreground(purple).Bold(true).Render("[" + a.key + "]")
		b.WriteString(fmt.Sprintf("    %s %s\n", key, dimStyle.Render(a.desc)))
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	keys := []string{
		helpKeyRender("esc", "back"),
		helpKeyRender("r", "run"),
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

		if len(m.tests) == 0 {
			b.WriteString("  " + dimStyle.Render("No tests available") + "\n")
		} else {
			start, end := scrollWindow(m.wfCreateTestCursor, len(m.tests), 12)
			for i := start; i < end; i++ {
				t := m.tests[i]
				cursor := "  "
				if i == m.wfCreateTestCursor {
					cursor = selectedStyle.Render("▸ ")
				}
				check := "[ ]"
				if m.wfCreateSelectedTests[i] {
					check = successStyle.Render("[✓]")
				}
				b.WriteString(fmt.Sprintf("  %s%s %s  %s\n", cursor, check, normalStyle.Render(t.Name), dimStyle.Render(t.Platform)))
			}
		}
		b.WriteString("\n  ")
		keys := []string{
			helpKeyRender("space", "toggle"),
			helpKeyRender("enter", "confirm"),
			helpKeyRender("esc", "back"),
		}
		b.WriteString(strings.Join(keys, "  ") + "\n")

	case wfCreateStepConfirm:
		b.WriteString(sectionStyle.Render("  Step 3: Confirm") + "\n")
		b.WriteString("  " + separator(innerW) + "\n")

		b.WriteString(fmt.Sprintf("  Name: %s\n", normalStyle.Render(m.wfCreateNameInput.Value())))

		var selectedNames []string
		for idx, selected := range m.wfCreateSelectedTests {
			if selected && idx < len(m.tests) {
				selectedNames = append(selectedNames, m.tests[idx].Name)
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
