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
	"github.com/revyl/cli/internal/execution"
)

// createStep tracks which stage of the create flow the user is on.
type createStep int

const (
	stepName     createStep = iota // entering test name
	stepPlatform                   // choosing platform
	stepApp                        // choosing app/build stream
	stepConfirm                    // reviewing before submit
	stepCreating                   // API call in flight
	stepDone                       // creation complete, offer next actions
)

type createDoneAction int

const (
	createDoneNone createDoneAction = iota
	createDoneOpenEditor
	createDoneBackToDashboard
	createDoneManageApps
)

// createModel manages the state of the inline "Create a test" TUI flow.
type createModel struct {
	step           createStep
	nameInput      textinput.Model
	platformCursor int
	platforms      []string
	appCursor      int
	apps           []api.App
	appsLoading    bool
	noEligibleApps bool

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
	createdID  string
	done       bool
	doneAction createDoneAction

	// Post-creation action cursor (0=open editor, 1=back to dashboard)
	doneCursor    int
	showEditorURL bool
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
func createTestCmd(client *api.Client, cfg *config.ProjectConfig, name, platform, appID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		result, err := execution.CreateTestWithClient(ctx, client, execution.CreateTestParams{
			Name:       name,
			Platform:   platform,
			AppID:      appID,
			Config:     cfg,
			AllowEmpty: true,
		})
		if err != nil {
			return TestCreatedMsg{Err: err}
		}

		return TestCreatedMsg{
			TestID:   result.TestID,
			TestName: name,
			Platform: platform,
		}
	}
}

func fetchCreateAppsCmd(client *api.Client, platform string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		page := 1
		apps := make([]api.App, 0, 16)
		for {
			resp, err := client.ListApps(ctx, platform, page, 100)
			if err != nil {
				return AppListMsg{Err: fmt.Errorf("failed to fetch apps: %w", err)}
			}
			apps = append(apps, resp.Items...)
			if !resp.HasNext {
				break
			}
			page++
		}
		return AppListMsg{Apps: apps}
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
		if m.creating || m.appsLoading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case AppListMsg:
		if m.step != stepApp {
			return m, nil
		}
		m.appsLoading = false
		if msg.Err != nil {
			m.err = msg.Err
			m.apps = nil
			m.noEligibleApps = false
			return m, nil
		}

		platform := m.selectedPlatform()
		eligibleApps := filterCreateApps(msg.Apps, platform)
		m.apps = eligibleApps
		m.appCursor = 0
		m.noEligibleApps = len(eligibleApps) == 0
		if m.noEligibleApps {
			m.err = fmt.Errorf("no %s apps with uploaded builds are available yet", platform)
			return m, nil
		}

		m.err = nil
		if preferredID := execution.ResolveConfiguredAppID(m.cfg, platform); preferredID != "" {
			for i, appInfo := range eligibleApps {
				if appInfo.ID == preferredID {
					m.appCursor = i
					break
				}
			}
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
	case stepApp:
		return m.handleAppKey(key)
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
		if m.client == nil {
			m.err = fmt.Errorf("not authenticated")
			return m, nil
		}
		m.step = stepApp
		m.apps = nil
		m.appCursor = 0
		m.appsLoading = true
		m.noEligibleApps = false
		m.err = nil
		return m, tea.Batch(m.spinner.Tick, fetchCreateAppsCmd(m.client, m.selectedPlatform()))
	case "backspace":
		m.step = stepName
		m.nameInput.Focus()
		return m, textinput.Blink
	}
	return m, nil
}

func (m createModel) handleAppKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if !m.appsLoading && m.appCursor > 0 {
			m.appCursor--
		}
	case "down", "j":
		if !m.appsLoading && m.appCursor < len(m.apps)-1 {
			m.appCursor++
		}
	case "r":
		if m.client == nil {
			m.err = fmt.Errorf("not authenticated")
			return m, nil
		}
		m.appsLoading = true
		m.err = nil
		m.noEligibleApps = false
		return m, tea.Batch(m.spinner.Tick, fetchCreateAppsCmd(m.client, m.selectedPlatform()))
	case "enter":
		if m.noEligibleApps {
			m.done = true
			m.doneAction = createDoneManageApps
			return m, nil
		}
		if m.appsLoading || len(m.apps) == 0 {
			return m, nil
		}
		m.step = stepConfirm
	case "backspace":
		m.step = stepPlatform
		m.appsLoading = false
		m.apps = nil
		m.noEligibleApps = false
		m.err = nil
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
		platform := m.selectedPlatform()
		selectedApp := m.selectedApp()
		if selectedApp == nil {
			m.creating = false
			m.step = stepApp
			m.err = fmt.Errorf("select an app before creating the test")
			return m, nil
		}
		return m, tea.Batch(m.spinner.Tick, createTestCmd(m.client, m.cfg, name, platform, selectedApp.ID))
	case "backspace", "n":
		m.step = stepApp
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
		if m.doneCursor == 0 {
			m.doneAction = createDoneOpenEditor
		} else {
			m.doneAction = createDoneBackToDashboard
		}
	case "l", "L":
		m.showEditorURL = !m.showEditorURL
	}
	return m, nil
}

func (m createModel) editorURL() string {
	if strings.TrimSpace(m.createdID) == "" {
		return ""
	}

	editor := execution.OpenTestEditor(nil, execution.OpenTestEditorParams{
		TestNameOrID: m.createdID,
		DevMode:      m.devMode,
	})
	return editor.TestURL
}

func (m createModel) selectedPlatform() string {
	if m.platformCursor < 0 || m.platformCursor >= len(m.platforms) {
		return ""
	}
	return m.platforms[m.platformCursor]
}

func (m createModel) selectedApp() *api.App {
	if m.appCursor < 0 || m.appCursor >= len(m.apps) {
		return nil
	}
	appInfo := m.apps[m.appCursor]
	return &appInfo
}

func filterCreateApps(apps []api.App, platform string) []api.App {
	filtered := make([]api.App, 0, len(apps))
	for _, appInfo := range apps {
		if strings.ToLower(strings.TrimSpace(appInfo.Platform)) != platform {
			continue
		}
		if appInfo.VersionsCount <= 0 && strings.TrimSpace(appInfo.CurrentVersion) == "" && strings.TrimSpace(appInfo.LatestVersion) == "" {
			continue
		}
		filtered = append(filtered, appInfo)
	}
	return filtered
}

func formatCreateAppDetails(appInfo api.App) string {
	parts := make([]string, 0, 3)
	if latest := strings.TrimSpace(appInfo.LatestVersion); latest != "" {
		parts = append(parts, "latest "+latest)
	}
	if current := strings.TrimSpace(appInfo.CurrentVersion); current != "" && current != strings.TrimSpace(appInfo.LatestVersion) {
		parts = append(parts, "current "+current)
	}
	if appInfo.VersionsCount > 0 {
		parts = append(parts, fmt.Sprintf("%d builds", appInfo.VersionsCount))
	}
	if len(parts) == 0 {
		return "build stream available"
	}
	return strings.Join(parts, " • ")
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
	steps := []string{"Name", "Platform", "App", "Confirm", "Create"}
	for i, s := range steps {
		style := dimStyle
		if i == int(m.step) || (m.step == stepDone && i == len(steps)-1) {
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

	case stepApp:
		b.WriteString("  " + normalStyle.Render("Select app/build stream:") + "\n\n")
		switch {
		case m.appsLoading:
			b.WriteString("  " + m.spinner.View() + " Loading apps...\n")
		case m.noEligibleApps:
			b.WriteString("  " + errorStyle.Render(m.err.Error()) + "\n\n")
			b.WriteString("  " + dimStyle.Render("Upload a build or link an app before creating a runnable test.") + "\n")
			b.WriteString("  " + dimStyle.Render("Press enter to open Manage apps.") + "\n")
			b.WriteString("\n  " + helpStyle.Render("enter to manage apps, backspace to go back") + "\n")
		case m.err != nil:
			b.WriteString("  " + errorStyle.Render(m.err.Error()) + "\n")
			b.WriteString("\n  " + helpStyle.Render("r to retry, backspace to go back") + "\n")
		default:
			for i, appInfo := range m.apps {
				cur := "  "
				style := normalStyle
				if i == m.appCursor {
					cur = selectedStyle.Render("▸ ")
					style = selectedStyle
				}
				b.WriteString("  " + cur + style.Render(appInfo.Name) + "\n")
				b.WriteString("     " + dimStyle.Render(formatCreateAppDetails(appInfo)) + "\n")
			}
			b.WriteString("\n  " + helpStyle.Render("enter to continue, backspace to go back") + "\n")
		}

	case stepConfirm:
		name := strings.TrimSpace(m.nameInput.Value())
		platform := m.selectedPlatform()
		selectedApp := m.selectedApp()
		b.WriteString("  " + normalStyle.Render("Review:") + "\n\n")
		b.WriteString("  " + dimStyle.Render("Name:     ") + normalStyle.Render(name) + "\n")
		b.WriteString("  " + dimStyle.Render("Platform: ") + normalStyle.Render(platform) + "\n")
		if selectedApp != nil {
			b.WriteString("  " + dimStyle.Render("App:      ") + normalStyle.Render(selectedApp.Name) + "\n")
		}
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

		options := []string{"Open editor", "Back to dashboard"}
		for i, opt := range options {
			cur := "  "
			style := normalStyle
			if i == m.doneCursor {
				cur = selectedStyle.Render("▸ ")
				style = selectedStyle
			}
			b.WriteString("  " + cur + style.Render(opt) + "\n")
		}
		if m.showEditorURL {
			editorURL := m.editorURL()
			if editorURL != "" {
				b.WriteString("\n  " + dimStyle.Render("Editor: ") + linkStyle.Render(editorURL) + "\n")
			}
		} else {
			b.WriteString("\n  " + dimStyle.Render("Press l to reveal the editor link for headless sessions.") + "\n")
		}
		b.WriteString("\n  " + helpStyle.Render("enter to select, l to show/hide editor link") + "\n")
	}

	return b.String()
}
