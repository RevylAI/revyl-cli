// Package tui provides the execution monitor model for real-time test monitoring.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/sse"
	statusutil "github.com/revyl/cli/internal/status"
	"github.com/revyl/cli/internal/ui"
)

// executionModel manages the state of a running test execution.
type executionModel struct {
	// testID is the UUID of the test being executed.
	testID string

	// testName is the display name of the test.
	testName string

	// apiKey is the authenticated API key.
	apiKey string

	// cfg is the project config for alias/build resolution.
	cfg *config.ProjectConfig

	// devMode indicates whether to use local development servers.
	devMode bool

	// taskID is the execution task ID (set after execution starts).
	taskID string

	// phase tracks the current pipeline phase.
	phase executionPhase

	// status holds the latest SSE progress update.
	status *sse.TestStatus

	// reportURL is the URL to the execution report.
	reportURL string

	// done indicates the execution has reached a terminal state.
	done bool

	// success indicates whether the test passed.
	success bool

	// errorMsg holds the error message if the execution failed.
	errorMsg string

	// startTime records when the execution started for elapsed time display.
	startTime time.Time

	// spinner provides visual activity feedback.
	spinner spinner.Model

	// width and height track terminal dimensions.
	width  int
	height int

	// cancelFunc cancels the SSE monitoring context.
	cancelFunc context.CancelFunc

	// cancelled indicates the user pressed ctrl+c to cancel.
	cancelled bool
}

// executionPhase represents the current pipeline stage.
type executionPhase int

const (
	phaseStarting  executionPhase = iota // submitting execution request
	phaseQueued                          // waiting for device
	phaseRunning                         // test is actively executing
	phaseCompleted                       // terminal state reached
)

// newExecutionModel creates a new execution monitor for the given test.
//
// Parameters:
//   - testID: the UUID of the test to execute
//   - testName: display name for the header
//   - apiKey: API key for authentication
//   - cfg: project config (may be nil)
//   - devMode: whether to use local development servers
//   - width: terminal width
//   - height: terminal height
//
// Returns:
//   - executionModel: the initialized model
func newExecutionModel(testID, testName, apiKey string, cfg *config.ProjectConfig, devMode bool, width, height int) executionModel {
	return executionModel{
		testID:    testID,
		testName:  testName,
		apiKey:    apiKey,
		cfg:       cfg,
		devMode:   devMode,
		phase:     phaseStarting,
		startTime: time.Now(),
		spinner:   newSpinner(),
		width:     width,
		height:    height,
	}
}

// --- Tea commands ---

// startMonitoredExecutionCmd starts the test execution with real-time SSE progress updates.
// Unlike startExecutionCmd, this sends incremental progress messages to the TUI.
func startMonitoredExecutionCmd(testID, testName, apiKey string, cfg *config.ProjectConfig, devMode bool) tea.Cmd {
	return func() tea.Msg {
		// First, start the execution
		client := api.NewClientWithDevMode(apiKey, devMode)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		resp, err := client.ExecuteTest(ctx, &api.ExecuteTestRequest{
			TestID:  testID,
			Retries: 1,
		})
		if err != nil {
			return ExecutionDoneMsg{Err: fmt.Errorf("failed to start test: %w", err)}
		}

		return ExecutionStartedMsg{
			TaskID:   resp.TaskID,
			TestID:   testID,
			TestName: testName,
		}
	}
}

// sseProgressCh is a message carrying the channel that streams SSE progress events.
// The first call to waitForProgressCmd is issued by monitorExecutionCmd; each subsequent
// read re-issues itself so the TUI receives a continuous stream of ExecutionProgressMsg
// until the monitor goroutine closes the channel.
type sseProgressCh struct {
	ch <-chan sse.TestStatus
}

// monitorExecutionCmd starts SSE monitoring in a background goroutine and returns
// a tea.Cmd that begins reading progress events from a channel. The SSE onProgress
// callback pushes every update into the channel, and a chain of waitForProgressCmd
// calls drains it one event at a time -- the standard Bubble Tea streaming pattern.
//
// Parameters:
//   - taskID: the execution task ID to monitor
//   - testID: the test UUID
//   - apiKey: API key for authentication
//   - devMode: whether to use local development servers
//
// Returns:
//   - tea.Cmd that yields the first progress or done message
func monitorExecutionCmd(taskID, testID, apiKey string, devMode bool) tea.Cmd {
	ch := make(chan sse.TestStatus, 16)

	// Background goroutine: runs MonitorTest with a real onProgress callback,
	// then signals completion by closing the channel.
	go func() {
		defer close(ch)

		monitor := sse.NewMonitorWithDevMode(apiKey, 3600, devMode)
		ctx := context.Background()

		onProgress := func(status *sse.TestStatus) {
			if status != nil {
				// Non-blocking send; if the TUI falls behind we drop the oldest.
				select {
				case ch <- *status:
				default:
					// Channel full -- drain one stale event and push the fresh one.
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

		// Push the final status into the channel so waitForProgressCmd sees it
		// before the channel closes.
		if finalStatus != nil {
			select {
			case ch <- *finalStatus:
			default:
			}
		}

		// If there was an error and no terminal status, push a sentinel with
		// the error message so the done-handler can display it.
		if err != nil && (finalStatus == nil || !statusutil.IsTerminal(finalStatus.Status)) {
			select {
			case ch <- sse.TestStatus{Status: "error", ErrorMessage: err.Error()}:
			default:
			}
		}
	}()

	// Return the first read command -- it will chain itself until the channel closes.
	return waitForProgressCmd(ch, taskID, devMode)
}

// waitForProgressCmd reads the next SSE event from the channel. If the channel
// is still open and the status is non-terminal, it yields an ExecutionProgressMsg
// and re-issues itself. When the channel closes or a terminal status arrives, it
// yields an ExecutionDoneMsg instead.
//
// Parameters:
//   - ch: the channel of SSE progress events
//   - taskID: the execution task ID (for building the report URL)
//   - devMode: whether to use local development servers
//
// Returns:
//   - tea.Cmd that yields either ExecutionProgressMsg or ExecutionDoneMsg
func waitForProgressCmd(ch <-chan sse.TestStatus, taskID string, devMode bool) tea.Cmd {
	return func() tea.Msg {
		status, ok := <-ch
		if !ok {
			// Channel closed -- monitoring finished with no further data.
			return ExecutionDoneMsg{
				ReportURL: fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(devMode), taskID),
			}
		}

		// Sentinel error status pushed by the goroutine.
		if status.Status == "error" && status.ErrorMessage != "" {
			return ExecutionDoneMsg{
				Err: fmt.Errorf("%s", status.ErrorMessage),
			}
		}

		// Terminal status -- this is the final message.
		if statusutil.IsTerminal(status.Status) {
			reportURL := fmt.Sprintf("%s/tests/report?taskId=%s", config.GetAppURL(devMode), taskID)
			return ExecutionDoneMsg{
				Status:    &status,
				ReportURL: reportURL,
			}
		}

		// Non-terminal -- deliver the progress update and re-subscribe.
		return ExecutionProgressMsg{
			Status:  &status,
			NextCmd: waitForProgressCmd(ch, taskID, devMode),
		}
	}
}

// tickCmd sends a tick every second for the elapsed time display.
func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(_ time.Time) tea.Msg {
		return tickMsg{}
	})
}

// tickMsg is sent every second to update the elapsed timer.
type tickMsg struct{}

// cancelExecutionCmd sends a cancel request to the backend.
func cancelExecutionCmd(taskID, apiKey string, devMode bool) tea.Cmd {
	return func() tea.Msg {
		client := api.NewClientWithDevMode(apiKey, devMode)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_, err := client.CancelTest(ctx, taskID)
		return ExecutionCancelledMsg{Err: err}
	}
}

// --- Bubble Tea interface ---

// Init starts the execution and the spinner.
func (m executionModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		tickCmd(),
		startMonitoredExecutionCmd(m.testID, m.testName, m.apiKey, m.cfg, m.devMode),
	)
}

// Update handles messages for the execution monitor.
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
		// Continue the streaming chain -- NextCmd reads the next event from the channel.
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

// handleKey processes key events in the execution view.
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
			// Handled by parent (hubModel.updateExecution)
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

// --- View rendering ---

// View renders the execution monitor screen.
func (m executionModel) View() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}

	// Header with test name and elapsed time
	elapsed := time.Since(m.startTime).Truncate(time.Second)
	statusIcon := m.phaseIcon()
	header := titleStyle.Render(" REVYL") + "  " +
		selectedStyle.Render(m.testName) + "  " +
		statusIcon + "  " +
		dimStyle.Render(elapsed.String())
	b.WriteString(header + "\n")
	b.WriteString(separator(min(w, 60)) + "\n")

	// Pipeline phase tracker
	b.WriteString(sectionStyle.Render("  Pipeline") + "\n")
	b.WriteString(m.renderPipeline())

	// Step tracker (only visible once running)
	if m.status != nil && m.status.TotalSteps > 0 {
		b.WriteString(sectionStyle.Render("  Steps") + "\n")
		b.WriteString("  " + separator(min(w-4, 56)) + "\n")
		b.WriteString(m.renderSteps())
	}

	// Metadata bar
	if m.status != nil || m.reportURL != "" {
		b.WriteString("\n")
		b.WriteString(m.renderMetadata())
	}

	// Result box (when done)
	if m.done {
		b.WriteString("\n")
		b.WriteString(m.renderResult())
	}

	// Footer with key hints
	b.WriteString("\n")
	b.WriteString("  " + separator(min(w-4, 56)) + "\n")
	b.WriteString("  " + m.renderHelp() + "\n")

	return b.String()
}

// phaseIcon returns the appropriate icon for the current phase.
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

// renderPipeline renders the pipeline phase tracker.
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

// renderSteps renders the step-by-step progress tracker.
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

	// Progress summary
	pct := 0
	if total > 0 {
		pct = (completed * 100) / total
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("  %d/%d steps  %d%%", completed, total, pct)) + "\n")

	return b.String()
}

// renderMetadata renders the bottom metadata bar (platform, report URL).
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

// renderResult renders the final pass/fail result box.
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

// renderHelp renders the bottom key hint bar.
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
