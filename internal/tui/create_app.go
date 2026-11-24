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
)

type appCreateStep int

const (
	appStepName appCreateStep = iota
	appStepPlatform
	appStepConfirm
	appStepCreating
	appStepDone
)

type createAppModel struct {
	step           appCreateStep
	nameInput      textinput.Model
	platformCursor int
	platforms      []string

	client *api.Client

	creating bool
	spinner  spinner.Model
	err      error
	width    int
	height   int

	createdID   string
	createdName string
	done        bool
	uploadNow   bool

	doneCursor int
}

// newCreateAppModel creates a new create-app sub-model.
//
// Parameters:
//   - client: the API client (may be nil if auth failed)
//   - width: terminal width
//   - height: terminal height
//
// Returns:
//   - createAppModel: the initialized model
func newCreateAppModel(client *api.Client, width, height int) createAppModel {
	ti := textinput.New()
	ti.Placeholder = "my-app-name"
	ti.CharLimit = 128
	ti.Focus()

	return createAppModel{
		step:      appStepName,
		nameInput: ti,
		platforms: []string{"android", "ios", "web"},
		client:    client,
		spinner:   newSpinner(),
		width:     width,
		height:    height,
	}
}

func createAppAPICmd(client *api.Client, name, platform string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		resp, err := client.CreateApp(ctx, &api.CreateAppRequest{
			Name:     name,
			Platform: platform,
		})
		if err != nil {
			return AppCreatedMsg{Err: fmt.Errorf("failed to create app: %w", err)}
		}

		return AppCreatedMsg{
			AppID:    resp.ID,
			AppName:  resp.Name,
			Platform: resp.Platform,
		}
	}
}

func (m createAppModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m createAppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

	case AppCreatedMsg:
		m.creating = false
		if msg.Err != nil {
			m.err = msg.Err
			m.step = appStepConfirm
			return m, nil
		}
		m.createdID = msg.AppID
		m.createdName = msg.AppName
		m.step = appStepDone
		return m, nil
	}

	if m.step == appStepName {
		var cmd tea.Cmd
		m.nameInput, cmd = m.nameInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m createAppModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "esc" && !m.creating {
		return m, nil
	}

	switch m.step {
	case appStepName:
		return m.handleNameKey(key, msg)
	case appStepPlatform:
		return m.handlePlatformKey(key)
	case appStepConfirm:
		return m.handleConfirmKey(key)
	case appStepDone:
		return m.handleDoneKey(key)
	}

	return m, nil
}

func (m createAppModel) handleNameKey(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key {
	case "enter":
		name := strings.TrimSpace(m.nameInput.Value())
		if name == "" {
			m.err = fmt.Errorf("app name cannot be empty")
			return m, nil
		}
		m.err = nil
		m.step = appStepPlatform
		m.nameInput.Blur()
		return m, nil
	default:
		var cmd tea.Cmd
		m.nameInput, cmd = m.nameInput.Update(msg)
		m.err = nil
		return m, cmd
	}
}

func (m createAppModel) handlePlatformKey(key string) (tea.Model, tea.Cmd) {
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
		m.step = appStepConfirm
	case "backspace":
		m.step = appStepName
		m.nameInput.Focus()
		return m, textinput.Blink
	}
	return m, nil
}

func (m createAppModel) handleConfirmKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter", "y":
		if m.client == nil {
			m.err = fmt.Errorf("not authenticated")
			return m, nil
		}
		m.creating = true
		m.err = nil
		m.step = appStepCreating
		name := strings.TrimSpace(m.nameInput.Value())
		platform := m.platforms[m.platformCursor]
		return m, tea.Batch(m.spinner.Tick, createAppAPICmd(m.client, name, platform))
	case "backspace", "n":
		m.step = appStepPlatform
		m.err = nil
	}
	return m, nil
}

func (m createAppModel) handleDoneKey(key string) (tea.Model, tea.Cmd) {
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
		m.uploadNow = m.doneCursor == 0 && m.platforms[m.platformCursor] != "web"
	}
	return m, nil
}

func (m createAppModel) View() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	sepW := min(w, 60)

	b.WriteString(titleStyle.Render(" REVYL") + "  " + dimStyle.Render("Create an app") + "\n")
	b.WriteString(separator(sepW) + "\n\n")

	steps := []string{"Name", "Platform", "Confirm", "Create"}
	for i, s := range steps {
		style := dimStyle
		if i == int(m.step) || (m.step == appStepDone && i == 3) {
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
	case appStepName:
		b.WriteString("  " + normalStyle.Render("App name:") + "\n")
		b.WriteString("  " + m.nameInput.View() + "\n")
		if m.err != nil {
			b.WriteString("  " + errorStyle.Render(m.err.Error()) + "\n")
		}
		b.WriteString("\n  " + helpStyle.Render("enter to continue, esc to cancel") + "\n")

	case appStepPlatform:
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

	case appStepConfirm:
		name := strings.TrimSpace(m.nameInput.Value())
		platform := m.platforms[m.platformCursor]
		b.WriteString("  " + normalStyle.Render("Review:") + "\n\n")
		b.WriteString("  " + dimStyle.Render("Name:     ") + normalStyle.Render(name) + "\n")
		b.WriteString("  " + dimStyle.Render("Platform: ") + normalStyle.Render(platform) + "\n")
		if m.err != nil {
			b.WriteString("\n  " + errorStyle.Render(m.err.Error()) + "\n")
		}
		b.WriteString("\n  " + helpStyle.Render("enter/y to create, backspace/n to go back, esc to cancel") + "\n")

	case appStepCreating:
		b.WriteString("  " + m.spinner.View() + " Creating app...\n")

	case appStepDone:
		name := strings.TrimSpace(m.nameInput.Value())
		b.WriteString("  " + successStyle.Render("✓ Created app: "+name) + "\n")
		b.WriteString("  " + dimStyle.Render("ID: "+m.createdID) + "\n\n")
		if m.platforms[m.platformCursor] == "web" {
			b.WriteString("  " + normalStyle.Render("Register a deployment URL as a build:") + "\n")
			b.WriteString("  " + dimStyle.Render("revyl build upload --platform web --app "+m.createdID+" --url <https-url>") + "\n\n")
			b.WriteString("  " + helpStyle.Render("enter to finish") + "\n")
			break
		}
		b.WriteString("  " + normalStyle.Render("Upload a build now?") + "\n\n")

		options := []string{"Upload build now", "Maybe later"}
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
