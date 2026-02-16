// Package tui provides the hub model -- the main TUI screen showing tests and quick actions.
package tui

import (
	"context"
	"fmt"
	"strings"
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

// hubModel is the top-level Bubble Tea model for the TUI hub.
type hubModel struct {
	// version is the CLI version string.
	version string

	// currentView tracks which screen is active.
	currentView view

	// tests is the list of tests loaded from the API.
	tests []TestItem

	// cursor is the index of the selected test in the visible list.
	cursor int

	// loading indicates whether test data is being fetched.
	loading bool

	// spinner shows activity during loading.
	spinner spinner.Model

	// err holds any error from the last API call.
	err error

	// width and height track the terminal dimensions.
	width  int
	height int

	// filterMode indicates whether the filter input is active.
	filterMode bool

	// filterInput is the text input for filtering tests.
	filterInput textinput.Model

	// filteredTests is the filtered subset of tests (nil means show all).
	filteredTests []TestItem

	// apiKey is the authenticated API key.
	apiKey string

	// cfg is the loaded project config (may be nil).
	cfg *config.ProjectConfig

	// client is the API client.
	client *api.Client

	// devMode indicates whether to use local development servers.
	devMode bool

	// authErr is set when authentication fails.
	authErr error

	// executionModel is the sub-model for test execution monitoring.
	executionModel *executionModel
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
		currentView: viewHub,
		loading:     true,
		spinner:     newSpinner(),
		filterInput: ti,
		devMode:     devMode,
	}
}

// --- Tea commands for async operations ---

// fetchTestsCmd authenticates and fetches the test list from the API.
func fetchTestsCmd(m hubModel) tea.Cmd {
	return func() tea.Msg {
		mgr := auth.NewManager()
		token, err := mgr.GetActiveToken()
		if err != nil || token == "" {
			return TestListMsg{Err: fmt.Errorf("not authenticated — run 'revyl auth login' first")}
		}

		client := api.NewClientWithDevMode(token, m.devMode)

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		resp, err := client.ListOrgTests(ctx, 100, 0)
		if err != nil {
			return TestListMsg{Err: fmt.Errorf("failed to fetch tests: %w", err)}
		}

		items := make([]TestItem, len(resp.Tests))
		for i, t := range resp.Tests {
			items[i] = TestItem{
				ID:       t.ID,
				Name:     t.Name,
				Platform: t.Platform,
			}
		}

		return TestListMsg{Tests: items}
	}
}

// --- Bubble Tea interface ---

// Init starts the spinner and kicks off the test list fetch.
func (m hubModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		fetchTestsCmd(m),
	)
}

// Update handles all incoming messages and key events.
func (m hubModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// If we're in execution view, delegate to execution model
	if m.currentView == viewExecution && m.executionModel != nil {
		return m.updateExecution(msg)
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleHubKey(msg)

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
		// Store auth details for later use by execution
		mgr := auth.NewManager()
		token, _ := mgr.GetActiveToken()
		m.apiKey = token
		m.client = api.NewClientWithDevMode(token, m.devMode)
		return m, nil
	}

	// Update filter input if in filter mode
	if m.filterMode {
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.applyFilter()
		return m, cmd
	}

	return m, nil
}

// handleHubKey processes key events in the hub view.
func (m hubModel) handleHubKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Filter mode key handling
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

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}

	case "down", "j":
		maxIdx := len(m.visibleTests()) - 1
		if m.cursor < maxIdx {
			m.cursor++
		}

	case "enter":
		tests := m.visibleTests()
		if len(tests) > 0 && m.cursor < len(tests) && m.apiKey != "" {
			selected := tests[m.cursor]
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
		// Open report for selected test
		tests := m.visibleTests()
		if len(tests) > 0 && m.cursor < len(tests) {
			selected := tests[m.cursor]
			reportURL := fmt.Sprintf("%s/tests/execute?testUid=%s", config.GetAppURL(m.devMode), selected.ID)
			_ = ui.OpenBrowser(reportURL)
		}

	case "R":
		// Refresh the test list
		m.loading = true
		m.err = nil
		return m, tea.Batch(m.spinner.Tick, fetchTestsCmd(m))
	}

	return m, nil
}

// updateExecution delegates messages to the execution model and handles navigation back.
func (m hubModel) updateExecution(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle esc to go back to hub
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "esc" {
		// Only allow esc-back when execution is done or hasn't started
		if m.executionModel != nil && m.executionModel.done {
			m.currentView = viewHub
			m.executionModel = nil
			return m, nil
		}
	}

	// Window resize applies to both views
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

	// Ensure cursor stays in bounds
	if m.cursor >= len(filtered) {
		m.cursor = max(0, len(filtered)-1)
	}
}

// --- View rendering ---

// View renders the current screen.
func (m hubModel) View() string {
	if m.currentView == viewExecution && m.executionModel != nil {
		return m.executionModel.View()
	}
	return m.viewHub()
}

// viewHub renders the main hub screen.
func (m hubModel) viewHub() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}

	// Header
	header := titleStyle.Render(" REVYL") + "  " + versionStyle.Render("v"+m.version)
	b.WriteString(header + "\n")
	b.WriteString(separator(min(w, 60)) + "\n")

	// Error state
	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render("  ✗ " + m.err.Error()))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  R refresh  q quit"))
		b.WriteString("\n")
		return b.String()
	}

	// Loading state
	if m.loading {
		b.WriteString("\n")
		b.WriteString("  " + m.spinner.View() + " Loading tests...")
		b.WriteString("\n")
		return b.String()
	}

	// Filter bar
	if m.filterMode {
		b.WriteString("\n")
		b.WriteString("  " + filterPromptStyle.Render("/") + " " + m.filterInput.View())
		b.WriteString("\n")
	}

	// Section header
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
			b.WriteString(dimStyle.Render("  No tests found. Run 'revyl test create <name>' to get started.\n"))
		}
	} else {
		// Calculate how many tests we can show
		maxVisible := m.height - 12 // leave room for header, footer, padding
		if maxVisible < 5 {
			maxVisible = 5
		}

		// Scroll window: keep cursor centered
		start := 0
		if len(tests) > maxVisible {
			start = m.cursor - maxVisible/2
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
			cursor := "  "
			nameStyle := normalStyle
			if i == m.cursor {
				cursor = selectedStyle.Render("▸ ")
				nameStyle = selectedStyle
			}

			// Platform badge
			platBadge := ""
			if t.Platform != "" {
				platBadge = platformStyle.Render(" [" + t.Platform + "]")
			}

			line := cursor + nameStyle.Render(t.Name) + platBadge
			b.WriteString("  " + line + "\n")
		}

		if end < len(tests) {
			b.WriteString(dimStyle.Render("  ↓ more") + "\n")
		}
	}

	// Footer with key hints
	b.WriteString("\n")
	b.WriteString("  " + separator(min(w-4, 56)) + "\n")

	keys := []string{
		helpKeyRender("enter", "run test"),
		helpKeyRender("r", "open in browser"),
		helpKeyRender("/", "filter"),
		helpKeyRender("R", "refresh"),
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
