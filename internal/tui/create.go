// Package tui provides the create-test sub-model for inline test creation.
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
	"github.com/revyl/cli/internal/config"
)

// createStep tracks which stage of the create flow the user is on.
type createStep int

const (
	stepName     createStep = iota // entering test name
	stepPlatform                   // choosing platform
	stepConfirm                    // reviewing before submit
	stepCreating                   // API call in flight
	stepDone                       // creation complete, offer to run
)

// createModel manages the state of the inline "Create a test" TUI flow.
type createModel struct {
	step           createStep
	nameInput      textinput.Model
	platformCursor int
	platforms      []string

	// API dependencies
	apiKey  string
	devMode bool
	client  *api.Client
	cfg     *config.ProjectConfig

	// State
	creating bool
	spinner  spinner.Model
	err      error
	width    int
	height   int

	// Result
	createdID string
	done      bool
	runAfter  bool

	// Post-creation action cursor (0=run now, 1=back to dashboard)
	doneCursor int
}

// newCreateModel creates a new create-test sub-model.
//
// Parameters:
//   - apiKey: authenticated API key
//   - devMode: whether to target local dev servers
//   - client: the API client (may be nil if auth failed)
//   - cfg: project config for app resolution (may be nil)
//   - width: terminal width
//   - height: terminal height
//
// Returns:
//   - createModel: the initialized model
func newCreateModel(apiKey string, devMode bool, client *api.Client, cfg *config.ProjectConfig, width, height int) createModel {
	ti := textinput.New()
	ti.Placeholder = "my-test-name"
	ti.CharLimit = 128
	ti.Focus()

	return createModel{
		step:      stepName,
		nameInput: ti,
		platforms: []string{"android", "ios"},
		apiKey:    apiKey,
		devMode:   devMode,
		client:    client,
		cfg:       cfg,
		spinner:   newSpinner(),
		width:     width,
		height:    height,
	}
}

// --- Tea commands ---

// createTestCmd calls the API to create a test.
func createTestCmd(client *api.Client, name, platform string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		resp, err := client.CreateTest(ctx, &api.CreateTestRequest{
			Name:     name,
			Platform: platform,
			Tasks:    []interface{}{},
		})
		if err != nil {
			return TestCreatedMsg{Err: fmt.Errorf("failed to create test: %w", err)}
		}

		return TestCreatedMsg{
			TestID:   resp.ID,
			TestName: name,
			Platform: platform,
		}
	}
}

// --- Bubble Tea interface ---

// Init starts the text input blink cursor.
func (m createModel) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles messages for the create-test flow.
func (m createModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		if m.creating {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case TestCreatedMsg:
		m.creating = false
		if msg.Err != nil {
			m.err = msg.Err
			m.step = stepConfirm
			return m, nil
		}
		m.createdID = msg.TestID
		m.step = stepDone
		return m, nil
	}

	// Forward to text input when on name step
	if m.step == stepName {
		var cmd tea.Cmd
		m.nameInput, cmd = m.nameInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

// handleKey processes key events for the create flow.
func (m createModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Global escape (only when not creating)
	if key == "esc" && !m.creating {
		// Handled by parent hub model
		return m, nil
	}

	switch m.step {
	case stepName:
		return m.handleNameKey(key, msg)
	case stepPlatform:
		return m.handlePlatformKey(key)
	case stepConfirm:
		return m.handleConfirmKey(key)
	case stepDone:
		return m.handleDoneKey(key)
	}

	return m, nil
}

// handleNameKey processes keys during the name input step.
func (m createModel) handleNameKey(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key {
	case "enter":
		name := strings.TrimSpace(m.nameInput.Value())
		if name == "" {
			m.err = fmt.Errorf("test name cannot be empty")
			return m, nil
		}
		m.err = nil
		m.step = stepPlatform
		m.nameInput.Blur()
		return m, nil
	default:
		var cmd tea.Cmd
		m.nameInput, cmd = m.nameInput.Update(msg)
		m.err = nil
		return m, cmd
	}
}

// handlePlatformKey processes keys during the platform selection step.
func (m createModel) handlePlatformKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.platformCursor > 0 {
			m.platformCursor--
		}
	case "down", "j":
		if m.platformCursor < len(m.platforms)-1 {
			m.platformCursor++
		}
	case "enter":
		m.step = stepConfirm
	case "backspace":
		m.step = stepName
		m.nameInput.Focus()
		return m, textinput.Blink
	}
	return m, nil
}

// handleConfirmKey processes keys during the confirm step.
func (m createModel) handleConfirmKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter", "y":
		if m.client == nil {
			m.err = fmt.Errorf("not authenticated")
			return m, nil
		}
		m.creating = true
		m.err = nil
		m.step = stepCreating
		name := strings.TrimSpace(m.nameInput.Value())
		platform := m.platforms[m.platformCursor]
		return m, tea.Batch(m.spinner.Tick, createTestCmd(m.client, name, platform))
	case "backspace", "n":
		m.step = stepPlatform
		m.err = nil
	}
	return m, nil
}

// handleDoneKey processes keys after test creation is complete.
func (m createModel) handleDoneKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.doneCursor > 0 {
			m.doneCursor--
		}
	case "down", "j":
		if m.doneCursor < 1 {
			m.doneCursor++
		}
	case "enter":
		m.done = true
		m.runAfter = m.doneCursor == 0
	}
	return m, nil
}

// --- View rendering ---

// View renders the create-test flow.
func (m createModel) View() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	sepW := min(w, 60)

	b.WriteString(titleStyle.Render(" REVYL") + "  " + dimStyle.Render("Create a test") + "\n")
	b.WriteString(separator(sepW) + "\n\n")

	// Progress indicator
	steps := []string{"Name", "Platform", "Confirm", "Create"}
	for i, s := range steps {
		style := dimStyle
		if i == int(m.step) || (m.step == stepDone && i == 3) {
			style = selectedStyle
		} else if i < int(m.step) {
			style = lipgloss.NewStyle().Foreground(green)
		}
		if i > 0 {
			b.WriteString(dimStyle.Render(" → "))
		}
		b.WriteString(style.Render(s))
	}
	b.WriteString("\n\n")

	switch m.step {
	case stepName:
		b.WriteString("  " + normalStyle.Render("Test name:") + "\n")
		b.WriteString("  " + m.nameInput.View() + "\n")
		if m.err != nil {
			b.WriteString("  " + errorStyle.Render(m.err.Error()) + "\n")
		}
		b.WriteString("\n  " + helpStyle.Render("enter to continue, esc to cancel") + "\n")

	case stepPlatform:
		b.WriteString("  " + normalStyle.Render("Select platform:") + "\n\n")
		for i, p := range m.platforms {
			cur := "  "
			style := normalStyle
			if i == m.platformCursor {
				cur = selectedStyle.Render("▸ ")
				style = selectedStyle
			}
			b.WriteString("  " + cur + style.Render(p) + "\n")
		}
		b.WriteString("\n  " + helpStyle.Render("enter to continue, backspace to go back") + "\n")

	case stepConfirm:
		name := strings.TrimSpace(m.nameInput.Value())
		platform := m.platforms[m.platformCursor]
		b.WriteString("  " + normalStyle.Render("Review:") + "\n\n")
		b.WriteString("  " + dimStyle.Render("Name:     ") + normalStyle.Render(name) + "\n")
		b.WriteString("  " + dimStyle.Render("Platform: ") + normalStyle.Render(platform) + "\n")
		if m.err != nil {
			b.WriteString("\n  " + errorStyle.Render(m.err.Error()) + "\n")
		}
		b.WriteString("\n  " + helpStyle.Render("enter/y to create, backspace/n to go back, esc to cancel") + "\n")

	case stepCreating:
		b.WriteString("  " + m.spinner.View() + " Creating test...\n")

	case stepDone:
		name := strings.TrimSpace(m.nameInput.Value())
		b.WriteString("  " + successStyle.Render("✓ Created test: "+name) + "\n")
		b.WriteString("  " + dimStyle.Render("ID: "+m.createdID) + "\n\n")
		b.WriteString("  " + normalStyle.Render("What next?") + "\n\n")

		options := []string{"Run this test now", "Back to dashboard"}
		for i, opt := range options {
			cur := "  "
			style := normalStyle
			if i == m.doneCursor {
				cur = selectedStyle.Render("▸ ")
				style = selectedStyle
			}
			b.WriteString("  " + cur + style.Render(opt) + "\n")
		}
		b.WriteString("\n  " + helpStyle.Render("enter to select") + "\n")
	}

	return b.String()
}
