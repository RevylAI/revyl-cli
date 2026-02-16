// Package tui provides the hub model -- the dashboard-first TUI with quick actions.
package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// quickAction defines an item in the Quick Actions menu.
type quickAction struct {
	Label string
	Key   string
}

// quickActions is the ordered list of actions on the dashboard.
var quickActions = []quickAction{
	{Label: "Run a test", Key: "run"},
	{Label: "Create a test", Key: "create"},
	{Label: "Browse workflows", Key: "workflows"},
	{Label: "Open dashboard", Key: "dashboard"},
	{Label: "Run doctor", Key: "doctor"},
}

// hubModel is the top-level Bubble Tea model for the TUI hub.
type hubModel struct {
	version     string
	currentView view

	// Dashboard data
	metrics    *api.DashboardMetrics
	recentRuns []RecentRun

	// Quick actions
	actionCursor int

	// Test list (sub-screen)
	tests         []TestItem
	testCursor    int
	filterMode    bool
	filterInput   textinput.Model
	filteredTests []TestItem

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

	return hubModel{
		version:     version,
		currentView: viewDashboard,
		loading:     true,
		spinner:     newSpinner(),
		filterInput: ti,
		devMode:     devMode,
	}
}

// --- Tea commands for async operations ---

// initDataCmd authenticates and fetches dashboard metrics, test list, and recent runs in parallel.
func initDataCmd(devMode bool) tea.Cmd {
	return func() tea.Msg {
		mgr := auth.NewManager()
		token, err := mgr.GetActiveToken()
		if err != nil || token == "" {
			return TestListMsg{Err: fmt.Errorf("not authenticated — run 'revyl auth login' first")}
		}

		client := api.NewClientWithDevMode(token, devMode)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		// Fetch tests (blocking -- we need the list for recent runs)
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

		type result struct {
			run RecentRun
			ok  bool
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

// --- Bubble Tea interface ---

// Init starts the spinner and kicks off the initial data fetch.
func (m hubModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, initDataCmd(m.devMode))
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
		if m.currentView == viewTestList {
			return m.handleTestListKey(msg)
		}
		return m.handleDashboardKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case TestListMsg:
		m.loading = false
		if msg.Err != nil {
			m.err = msg.Err
			return m, nil
		}
		m.tests = msg.Tests
		mgr := auth.NewManager()
		token, _ := mgr.GetActiveToken()
		m.apiKey = token
		m.client = api.NewClientWithDevMode(token, m.devMode)
		// Now fetch metrics and recent runs in parallel
		return m, tea.Batch(
			fetchDashboardMetricsCmd(m.client),
			fetchRecentRunsCmd(m.client, m.tests, 10),
		)

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
	case "q", "ctrl+c":
		return m, tea.Quit

	case "up", "k":
		if m.actionCursor > 0 {
			m.actionCursor--
		}

	case "down", "j":
		if m.actionCursor < len(quickActions)-1 {
			m.actionCursor++
		}

	case "enter":
		return m.executeQuickAction()

	case "1":
		m.actionCursor = 0
		return m.executeQuickAction()
	case "2":
		m.actionCursor = 1
		return m.executeQuickAction()
	case "3":
		m.actionCursor = 2
		return m.executeQuickAction()
	case "4":
		m.actionCursor = 3
		return m.executeQuickAction()
	case "5":
		m.actionCursor = 4
		return m.executeQuickAction()

	case "/":
		// Jump straight to test list with filter active
		m.currentView = viewTestList
		m.testCursor = 0
		m.filterMode = true
		m.filterInput.Focus()
		return m, textinput.Blink

	case "R":
		m.loading = true
		m.err = nil
		m.metrics = nil
		m.recentRuns = nil
		return m, tea.Batch(m.spinner.Tick, initDataCmd(m.devMode))
	}

	return m, nil
}

// executeQuickAction dispatches the currently selected quick action.
func (m hubModel) executeQuickAction() (tea.Model, tea.Cmd) {
	if m.actionCursor >= len(quickActions) {
		return m, nil
	}

	action := quickActions[m.actionCursor]
	switch action.Key {
	case "run":
		m.currentView = viewTestList
		m.testCursor = 0
		m.filteredTests = nil
		m.filterInput.SetValue("")
		return m, nil

	case "create":
		cm := newCreateModel(m.apiKey, m.devMode, m.client, m.cfg, m.width, m.height)
		m.createModel = &cm
		m.currentView = viewCreateTest
		return m, m.createModel.Init()

	case "workflows":
		dashURL := fmt.Sprintf("%s/workflows", config.GetAppURL(m.devMode))
		_ = ui.OpenBrowser(dashURL)
		return m, nil

	case "dashboard":
		dashURL := config.GetAppURL(m.devMode)
		_ = ui.OpenBrowser(dashURL)
		return m, nil

	case "doctor":
		// For MVP, open doctor in a note; full inline TUI doctor is a future enhancement
		return m, tea.ExecProcess(doctorCmd(), func(err error) tea.Msg {
			return doctorDoneMsg{err: err}
		})
	}
	return m, nil
}

// doctorDoneMsg signals doctor subprocess completed.
type doctorDoneMsg struct{ err error }

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
		if len(tests) > 0 && m.testCursor < len(tests) && m.apiKey != "" {
			selected := tests[m.testCursor]
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
			reportURL := fmt.Sprintf("%s/tests/execute?testUid=%s", config.GetAppURL(m.devMode), selected.ID)
			_ = ui.OpenBrowser(reportURL)
		}

	case "R":
		m.loading = true
		m.err = nil
		return m, tea.Batch(m.spinner.Tick, initDataCmd(m.devMode))
	}

	return m, nil
}

// updateExecution delegates messages to the execution model and handles navigation back.
func (m hubModel) updateExecution(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "esc" {
		if m.executionModel != nil && m.executionModel.done {
			m.currentView = viewDashboard
			m.executionModel = nil
			// Refresh recent runs after execution
			if m.client != nil {
				return m, fetchRecentRunsCmd(m.client, m.tests, 10)
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

// doctorCmd returns an exec.Cmd for running `revyl doctor` as a subprocess.
func doctorCmd() *exec.Cmd {
	exe, err := os.Executable()
	if err != nil {
		exe = "revyl"
	}
	return exec.Command(exe, "doctor")
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
	}
	return m.renderDashboard()
}

// renderDashboard renders the dashboard landing page.
func (m hubModel) renderDashboard() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	sepW := min(w, 60)

	b.WriteString(titleStyle.Render(" REVYL") + "  " + versionStyle.Render("v"+m.version) + "\n")
	b.WriteString(separator(sepW) + "\n")

	if m.err != nil {
		b.WriteString("\n" + errorStyle.Render("  ✗ "+m.err.Error()) + "\n\n")
		b.WriteString(helpStyle.Render("  R refresh  q quit") + "\n")
		return b.String()
	}

	if m.loading {
		b.WriteString("\n  " + m.spinner.View() + " Loading...\n")
		return b.String()
	}

	// Stats
	b.WriteString(sectionStyle.Render("  STATS") + "\n")
	b.WriteString("  " + separator(min(w-4, 56)) + "\n")
	b.WriteString(m.renderStats())

	// Recent runs
	b.WriteString(sectionStyle.Render("  RECENT RUNS") + "\n")
	b.WriteString("  " + separator(min(w-4, 56)) + "\n")
	b.WriteString(m.renderRecentRuns())

	// Quick actions
	b.WriteString(sectionStyle.Render("  QUICK ACTIONS") + "\n")
	b.WriteString("  " + separator(min(w-4, 56)) + "\n")
	for i, a := range quickActions {
		cur := "  "
		style := normalStyle
		if i == m.actionCursor {
			cur = selectedStyle.Render("▸ ")
			style = selectedStyle
		}
		num := dimStyle.Render(fmt.Sprintf("[%d] ", i+1))
		b.WriteString("  " + cur + num + style.Render(a.Label) + "\n")
	}

	b.WriteString("\n  " + separator(min(w-4, 56)) + "\n")
	keys := []string{
		helpKeyRender("enter", "select"),
		helpKeyRender("1-5", "jump"),
		helpKeyRender("/", "search"),
		helpKeyRender("R", "refresh"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")
	return b.String()
}

// renderStats renders the metrics summary row.
func (m hubModel) renderStats() string {
	if m.metrics == nil {
		return "  " + dimStyle.Render("Loading metrics...") + "\n"
	}
	mt := m.metrics
	parts := []string{
		metricRender("Tests", fmt.Sprintf("%d", mt.TotalTests)),
		metricRender("Workflows", fmt.Sprintf("%d", mt.TotalWorkflows)),
		metricRender("Runs", fmt.Sprintf("%d", mt.TestRuns)),
		metricRender("Fail", fmt.Sprintf("%.0f%%", mt.TestsFailingPercent)),
	}
	if mt.AvgTestDuration != nil {
		parts = append(parts, metricRender("Avg", fmt.Sprintf("%.0fs", *mt.AvgTestDuration)))
	}
	return "  " + strings.Join(parts, "    ") + "\n"
}

// metricRender formats a metric label/value pair.
func metricRender(label, value string) string {
	return dimStyle.Render(label+" ") + lipgloss.NewStyle().Foreground(white).Bold(true).Render(value)
}

// renderRecentRuns renders the recent runs section.
func (m hubModel) renderRecentRuns() string {
	if len(m.recentRuns) == 0 {
		return "  " + dimStyle.Render("No recent runs") + "\n"
	}
	var b strings.Builder
	for _, r := range m.recentRuns {
		icon := statusIcon(r.Status)
		name := normalStyle.Render(r.TestName)
		status := dimStyle.Render(r.Status)
		ago := dimStyle.Render(relativeTime(r.Time))
		b.WriteString(fmt.Sprintf("  %s  %-30s  %-12s  %s\n", icon, name, status, ago))
	}
	return b.String()
}

// renderTestList renders the test list sub-screen.
func (m hubModel) renderTestList() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	sepW := min(w, 60)

	b.WriteString(titleStyle.Render(" REVYL") + "  " + dimStyle.Render("Select a test to run") + "\n")
	b.WriteString(separator(sepW) + "\n")

	if m.filterMode {
		b.WriteString("\n  " + filterPromptStyle.Render("/") + " " + m.filterInput.View() + "\n")
	}

	tests := m.visibleTests()
	countLabel := fmt.Sprintf("%d", len(tests))
	if m.filteredTests != nil {
		countLabel = fmt.Sprintf("%d/%d", len(m.filteredTests), len(m.tests))
	}
	b.WriteString(sectionStyle.Render("  Tests") + " " + dimStyle.Render(countLabel) + "\n")
	b.WriteString("  " + separator(min(w-4, 56)) + "\n")

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
				nameStyle = selectedStyle
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

	b.WriteString("\n  " + separator(min(w-4, 56)) + "\n")
	keys := []string{
		helpKeyRender("enter", "run"),
		helpKeyRender("/", "filter"),
		helpKeyRender("r", "open in browser"),
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
