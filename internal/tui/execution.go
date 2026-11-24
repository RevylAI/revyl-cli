package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/execution"
	"github.com/revyl/cli/internal/sse"
	statusutil "github.com/revyl/cli/internal/status"
	"github.com/revyl/cli/internal/ui"
)

type executionModel struct {
	branchFallbackNotice string
	buildSelection       api.BuildSelection
	overrideAppID        string
	testID               string

	testName string

	apiKey string

	devMode bool

	taskID string

	phase executionPhase

	status *sse.TestStatus

	reportURL string

	done bool

	success bool

	errorMsg string

	startTime time.Time

	spinner spinner.Model

	width  int
	height int

	// cancelFunc is invoked on user cancellation when non-nil.
	cancelFunc context.CancelFunc

	cancelled bool
}

type executionPhase int

const (
	phaseStarting executionPhase = iota
	phaseQueued
	phaseRunning
	phaseCompleted
)

func newExecutionModel(testID, testName, apiKey string, devMode bool, width, height int) executionModel {
	return executionModel{
		testID:    testID,
		testName:  testName,
		apiKey:    apiKey,
		devMode:   devMode,
		phase:     phaseStarting,
		startTime: time.Now(),
		spinner:   newSpinner(),
		width:     width,
		height:    height,
	}
}

// startMonitoredExecutionCmd starts a test execution and returns ExecutionStartedMsg
// (or ExecutionDoneMsg on setup failure); SSE monitoring begins after start succeeds.
func startMonitoredExecutionCmd(testID, testName, apiKey string, devMode bool, selections ...testExecutionBuildSelection) tea.Cmd {
	return func() tea.Msg {
		cwd, err := os.Getwd()
		if err != nil {
			return ExecutionDoneMsg{Err: fmt.Errorf("resolve current project: %w", err)}
		}
		project, err := config.ResolveProjectContext(cwd, "")
		if err != nil {
			return ExecutionDoneMsg{Err: actionableProjectConfigError(fmt.Errorf("resolve current project: %w", err))}
		}

		client := api.NewClientWithDevMode(apiKey, devMode)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		choice := testExecutionBuildSelection{}
		if len(selections) > 0 {
			choice = selections[0]
		}
		selection, notice, err := prepareTestRunBuildSelection(ctx, client, choice)
		if err != nil {
			return ExecutionDoneMsg{Err: err}
		}
		checkoutUpload, err := collectCheckoutUpload(ctx, project, []string{testID})
		if err != nil {
			return ExecutionDoneMsg{Err: err}
		}
		if selection.GitBranchName == "" && checkoutUpload != nil {
			selection.GitBranchName = checkoutUpload.GitBranchName
		}
		resp, err := client.ExecuteTest(ctx, &api.ExecuteTestRequest{
			TestID:              testID,
			Retries:             1,
			BranchID:            selection.BranchID,
			BranchName:          selection.BranchName,
			BuildSelection:      selection.Mode,
			BuildVersionID:      selection.BuildID,
			GitBranchName:       selection.GitBranchName,
			TestDefinitionFiles: checkoutUpload.RequestFiles(),
		})
		if err != nil {
			return ExecutionDoneMsg{Err: fmt.Errorf("failed to start test: %w", err)}
		}

		if resolvedNotice := ui.BranchSelectionNotice(resp.GitBranchName, resp.BranchName, string(resp.BranchSource), resp.AutoGitBranchEnabled); resolvedNotice != "" {
			notice = resolvedNotice
		}
		testCountBySource, testCountByFallbackReason := map[api.TestDefinitionSource]int{}, map[api.TestDefinitionFallbackReason]int{}
		if resp.TestDefinitionSource != "" {
			testCountBySource[resp.TestDefinitionSource] = 1
		}
		if resp.TestDefinitionFallbackReason != "" {
			testCountByFallbackReason[resp.TestDefinitionFallbackReason] = 1
		}
		notice = noticeWithTestDefinition(ui.NoticeWithTestDefinitionGitBranch(notice, resp.TestDefinitionGitBranchName), checkoutUpload, testCountBySource, testCountByFallbackReason, resp.TestDefinitionGitRead)
		return ExecutionStartedMsg{
			BranchFallbackNotice: notice,
			TaskID:               resp.TaskID,
			TestID:               testID,
			TestName:             testName,
		}
	}
}

// sseProgressCh wraps an SSE test-status receive channel.
type sseProgressCh struct {
	ch <-chan sse.TestStatus
}

// monitorExecutionCmd starts SSE monitoring in a background goroutine over a
// bounded non-blocking channel and returns the initial waitForProgressCmd read.
func monitorExecutionCmd(taskID, testID, apiKey string, devMode bool) tea.Cmd {
	ch := make(chan sse.TestStatus, 16)

	go func() {
		defer close(ch)

		monitor := sse.NewMonitorWithDevMode(apiKey, execution.DefaultRunTimeoutSeconds, devMode)
		ctx := context.Background()

		onProgress := func(status *sse.TestStatus) {
			if status != nil {
				select {
				case ch <- *status:
				default:
					select {
					case <-ch:
					default:
					}
					select {
					case ch <- *status:
					default:
					}
				}
			}
		}

		finalStatus, err := monitor.MonitorTest(ctx, taskID, testID, onProgress)

		// Best-effort non-blocking send of the final status before channel close.
		if finalStatus != nil {
			select {
			case ch <- *finalStatus:
			default:
			}
		}

		// Best-effort non-blocking error sentinel when monitoring ended without terminal status.
		if err != nil && (finalStatus == nil || !statusutil.IsTerminal(finalStatus.Status)) {
			select {
			case ch <- sse.TestStatus{Status: "error", ErrorMessage: err.Error()}:
			default:
			}
		}
	}()

	// Return the initial read command; Update forwards NextCmd to continue reading.
	return waitForProgressCmd(ch, taskID, devMode)
}

// waitForProgressCmd reads one SSE event from ch, returning ExecutionDoneMsg on
// close, error sentinel, or terminal status, or ExecutionProgressMsg with NextCmd for Update to schedule.
func waitForProgressCmd(ch <-chan sse.TestStatus, taskID string, devMode bool) tea.Cmd {
	return func() tea.Msg {
		status, ok := <-ch
		if !ok {
			return ExecutionDoneMsg{
				ReportURL: fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(devMode), taskID),
			}
		}

		if status.Status == "error" && status.ErrorMessage != "" {
			return ExecutionDoneMsg{
				Err: fmt.Errorf("%s", status.ErrorMessage),
			}
		}

		if statusutil.IsTerminal(status.Status) {
			reportURL := fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(devMode), taskID)
			return ExecutionDoneMsg{
				Status:    &status,
				ReportURL: reportURL,
			}
		}

		// Non-terminal -- package the progress update and next read command for Update.
		return ExecutionProgressMsg{
			Status:  &status,
			NextCmd: waitForProgressCmd(ch, taskID, devMode),
		}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(_ time.Time) tea.Msg {
		return tickMsg{}
	})
}

type tickMsg struct{}

func cancelExecutionCmd(taskID, apiKey string, devMode bool) tea.Cmd {
	return func() tea.Msg {
		client := api.NewClientWithDevMode(apiKey, devMode)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_, err := client.CancelTest(ctx, taskID)
		return ExecutionCancelledMsg{Err: err}
	}
}

func (m executionModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		tickCmd(),
		startMonitoredExecutionCmd(m.testID, m.testName, m.apiKey, m.devMode, testExecutionBuildSelection{Selection: m.buildSelection, OverrideAppID: m.overrideAppID}),
	)
}

func (m executionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tickMsg:
		if !m.done {
			return m, tickCmd()
		}
		return m, nil

	case ExecutionStartedMsg:
		if msg.Err != nil {
			m.done = true
			m.errorMsg = msg.Err.Error()
			m.phase = phaseCompleted
			return m, nil
		}
		m.branchFallbackNotice = msg.BranchFallbackNotice
		m.taskID = msg.TaskID
		m.phase = phaseQueued
		m.reportURL = fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(m.devMode), msg.TaskID)
		return m, monitorExecutionCmd(msg.TaskID, msg.TestID, m.apiKey, m.devMode)

	case ExecutionProgressMsg:
		if msg.Status != nil {
			m.status = msg.Status
			if statusutil.IsActive(msg.Status.Status) {
				m.phase = phaseRunning
			}
		}
		return m, msg.NextCmd

	case ExecutionDoneMsg:
		m.done = true
		m.phase = phaseCompleted
		if msg.Err != nil {
			m.errorMsg = msg.Err.Error()
			return m, nil
		}
		if msg.Status != nil {
			m.status = msg.Status
			m.reportURL = msg.ReportURL
			m.success = statusutil.IsSuccess(msg.Status.Status, msg.Status.Success, msg.Status.ErrorMessage)
			if msg.Status.ErrorMessage != "" {
				m.errorMsg = msg.Status.ErrorMessage
			}
		}
		return m, nil

	case ExecutionCancelledMsg:
		m.done = true
		m.cancelled = true
		m.phase = phaseCompleted
		return m, nil
	}

	return m, nil
}

func (m executionModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if !m.done && m.taskID != "" && !m.cancelled {
			m.cancelled = true
			if m.cancelFunc != nil {
				m.cancelFunc()
			}
			return m, cancelExecutionCmd(m.taskID, m.apiKey, m.devMode)
		}
		return m, tea.Quit

	case "esc":
		if m.done {
			return m, nil
		}

	case "r":
		if m.reportURL != "" {
			_ = ui.OpenBrowser(m.reportURL)
		}

	case "q":
		if m.done {
			return m, tea.Quit
		}
	}

	return m, nil
}

func (m executionModel) View() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}

	elapsed := time.Since(m.startTime).Truncate(time.Second)
	statusIcon := m.phaseIcon()
	header := titleStyle.Render(" REVYL") + "  " +
		selectedStyle.Render(m.testName) + "  " +
		statusIcon + "  " +
		dimStyle.Render(elapsed.String())
	b.WriteString(header + "\n")
	b.WriteString(separator(min(w, 60)) + "\n")
	if m.branchFallbackNotice != "" {
		b.WriteString(lipgloss.NewStyle().Width(min(w, 60)-2).MarginLeft(2).Render(warningStyle.Render(m.branchFallbackNotice)) + "\n")
	}

	b.WriteString(sectionStyle.Render("  Pipeline") + "\n")
	b.WriteString(m.renderPipeline())

	if m.status != nil && m.status.TotalSteps > 0 {
		b.WriteString(sectionStyle.Render("  Steps") + "\n")
		b.WriteString("  " + separator(min(w-4, 56)) + "\n")
		b.WriteString(m.renderSteps())
	}

	if m.status != nil || m.reportURL != "" {
		b.WriteString("\n")
		b.WriteString(m.renderMetadata())
	}

	if m.done {
		b.WriteString("\n")
		b.WriteString(m.renderResult())
	}

	b.WriteString("\n")
	b.WriteString("  " + separator(min(w-4, 56)) + "\n")
	b.WriteString("  " + m.renderHelp() + "\n")

	return b.String()
}

func (m executionModel) phaseIcon() string {
	if m.done {
		if m.cancelled {
			return warningStyle.Render("⊘")
		}
		if m.success {
			return successStyle.Render("✓")
		}
		return errorStyle.Render("✗")
	}
	return m.spinner.View()
}

func (m executionModel) renderPipeline() string {
	var b strings.Builder

	phases := []struct {
		label  string
		done   bool
		active bool
	}{
		{"Starting execution", m.phase > phaseStarting, m.phase == phaseStarting},
		{"Waiting for device", m.phase > phaseQueued, m.phase == phaseQueued},
		{"Running test", m.phase > phaseRunning || (m.done && m.phase == phaseCompleted), m.phase == phaseRunning && !m.done},
	}

	for _, p := range phases {
		icon := dimStyle.Render("  ⏳ ")
		labelSt := dimStyle
		if p.done {
			icon = successStyle.Render("  ✓  ")
			labelSt = normalStyle
		} else if p.active {
			icon = "  " + m.spinner.View() + " "
			labelSt = runningStyle
		}
		b.WriteString(icon + labelSt.Render(p.label) + "\n")
	}

	return b.String()
}

func (m executionModel) renderSteps() string {
	if m.status == nil {
		return ""
	}

	var b strings.Builder
	total := m.status.TotalSteps
	completed := m.status.CompletedSteps
	currentStep := m.status.CurrentStep

	for i := 1; i <= total; i++ {
		var icon string
		var labelSt func(string) string

		switch {
		case i <= completed:
			icon = successStyle.Render("✓")
			labelSt = func(s string) string { return normalStyle.Render(s) }
		case i == completed+1:
			icon = m.spinner.View()
			labelSt = func(s string) string { return runningStyle.Render(s) }
		default:
			icon = dimStyle.Render("⏳")
			labelSt = func(s string) string { return dimStyle.Render(s) }
		}

		stepLabel := fmt.Sprintf("Step %d", i)
		if i == completed+1 && currentStep != "" {
			stepLabel = currentStep
		}

		line := fmt.Sprintf("   %s  %s  %s", icon, dimStyle.Render(fmt.Sprintf("%2d.", i)), labelSt(stepLabel))
		b.WriteString(line + "\n")
	}

	pct := 0
	if total > 0 {
		pct = (completed * 100) / total
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("  %d/%d steps  %d%%", completed, total, pct)) + "\n")

	return b.String()
}

func (m executionModel) renderMetadata() string {
	var parts []string

	if m.status != nil && m.status.Duration != "" {
		parts = append(parts, dimStyle.Render("duration: "+m.status.Duration))
	}

	if m.reportURL != "" {
		parts = append(parts, "report: "+linkStyle.Render(m.reportURL))
	}

	if len(parts) == 0 {
		return ""
	}
	return "  " + strings.Join(parts, "  ") + "\n"
}

func (m executionModel) renderResult() string {
	if m.cancelled {
		return warningStyle.Render("  ⊘ Execution cancelled") + "\n"
	}

	if m.success {
		return successStyle.Render("  ✓ Test passed") + "\n"
	}

	msg := "  ✗ Test failed"
	if m.errorMsg != "" {
		msg += "\n  " + dimStyle.Render(m.errorMsg)
	}
	return errorStyle.Render(msg) + "\n"
}

func (m executionModel) renderHelp() string {
	var keys []string

	if m.done {
		keys = append(keys, helpKeyRender("esc", "back"))
		if m.reportURL != "" {
			keys = append(keys, helpKeyRender("r", "open report"))
		}
		keys = append(keys, helpKeyRender("q", "quit"))
	} else {
		keys = append(keys, helpKeyRender("ctrl+c", "cancel"))
		if m.reportURL != "" {
			keys = append(keys, helpKeyRender("r", "open report"))
		}
	}

	return strings.Join(keys, "  ")
}
