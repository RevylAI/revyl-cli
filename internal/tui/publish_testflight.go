package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/asc"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/publishenv"
	"github.com/revyl/cli/internal/store"
)

type publishTestFlightStep int

const (
	tfStepCredentials publishTestFlightStep = iota
	tfStepAppID
	tfStepIPAPath
	tfStepGroups
	tfStepWhatsNew
	tfStepConfirm
	tfStepRunning
	tfStepDone
)

type publishTestFlightDoneMsg struct {
	Err error
}

type publishTestFlightModel struct {
	step publishTestFlightStep

	keyIDInput      textinput.Model
	issuerIDInput   textinput.Model
	privateKeyInput textinput.Model
	credCursor      int
	appIDInput      textinput.Model
	ipaPathInput    textinput.Model
	groupsInput     textinput.Model
	whatsNewInput   textinput.Model

	waitForProcessing bool
	hasStoredCreds    bool
	storeSummary      string

	running bool
	spinner spinner.Model
	err     error

	width  int
	height int

	done       bool
	doneCursor int
	runAgain   bool
}

func newPublishTestFlightModel(cfg *config.ProjectConfig, width, height int) publishTestFlightModel {
	keyIDInput := textinput.New()
	keyIDInput.Placeholder = "ABC123DEF4"
	keyIDInput.CharLimit = 64
	keyIDInput.Focus()

	issuerInput := textinput.New()
	issuerInput.Placeholder = "00000000-0000-0000-0000-000000000000"
	issuerInput.CharLimit = 128

	privateKeyInput := textinput.New()
	privateKeyInput.Placeholder = "/path/to/AuthKey_XXXX.p8"
	privateKeyInput.CharLimit = 512

	appIDInput := textinput.New()
	appIDInput.Placeholder = "6758900172"
	appIDInput.CharLimit = 64

	ipaPathInput := textinput.New()
	ipaPathInput.Placeholder = "/path/to/app.ipa (optional; blank = latest build)"
	ipaPathInput.CharLimit = 512

	groupsInput := textinput.New()
	groupsInput.Placeholder = "Internal,External (optional)"
	groupsInput.CharLimit = 256

	whatsNewInput := textinput.New()
	whatsNewInput.Placeholder = "What changed in this build? (optional)"
	whatsNewInput.CharLimit = 500

	mgr := store.NewManager()
	storeSummary := "Not configured"
	hasStoredCreds := false
	if creds, err := mgr.Load(); err == nil && creds != nil && creds.IOS != nil {
		if creds.IOS.KeyID != "" {
			keyIDInput.SetValue(creds.IOS.KeyID)
		}
		if creds.IOS.IssuerID != "" {
			issuerInput.SetValue(creds.IOS.IssuerID)
		}
		if creds.IOS.PrivateKeyPath != "" {
			privateKeyInput.SetValue(creds.IOS.PrivateKeyPath)
		}
		if err := mgr.ValidateIOSCredentials(); err == nil {
			hasStoredCreds = true
			storeSummary = "Configured in ~/.revyl/store-credentials.json"
		}
	}

	if hasASCCredentialsInEnv() {
		hasStoredCreds = true
		storeSummary = "Configured via REVYL_ASC_* environment variables"
	}

	if cfg != nil && cfg.Publish.IOS.ASCAppID != "" {
		appIDInput.SetValue(cfg.Publish.IOS.ASCAppID)
	}
	if appIDInput.Value() == "" {
		appIDInput.SetValue(strings.TrimSpace(os.Getenv(publishenv.ASCAppID)))
	}

	if cfg != nil && len(cfg.Publish.IOS.TestFlightGroups) > 0 {
		groupsInput.SetValue(strings.Join(cfg.Publish.IOS.TestFlightGroups, ","))
	}
	if groupsInput.Value() == "" {
		groupsInput.SetValue(strings.TrimSpace(os.Getenv(publishenv.TFGroups)))
	}

	return publishTestFlightModel{
		step:              tfStepCredentials,
		keyIDInput:        keyIDInput,
		issuerIDInput:     issuerInput,
		privateKeyInput:   privateKeyInput,
		credCursor:        0,
		appIDInput:        appIDInput,
		ipaPathInput:      ipaPathInput,
		groupsInput:       groupsInput,
		whatsNewInput:     whatsNewInput,
		waitForProcessing: true,
		hasStoredCreds:    hasStoredCreds,
		storeSummary:      storeSummary,
		spinner:           newSpinner(),
		width:             width,
		height:            height,
	}
}

func hasASCCredentialsInEnv() bool {
	key := strings.TrimSpace(os.Getenv(publishenv.ASCKeyID))
	issuer := strings.TrimSpace(os.Getenv(publishenv.ASCIssuerID))
	privatePath := strings.TrimSpace(os.Getenv(publishenv.ASCPrivatePath))
	privateRaw := strings.TrimSpace(os.Getenv(publishenv.ASCPrivateKey))
	return key != "" && issuer != "" && (privatePath != "" || privateRaw != "")
}

func (m publishTestFlightModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m publishTestFlightModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	case spinner.TickMsg:
		if m.running {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	case publishTestFlightDoneMsg:
		m.running = false
		m.step = tfStepDone
		m.err = msg.Err
		return m, nil
	}

	switch m.step {
	case tfStepCredentials:
		return m.updateCredentialsInputs(msg)
	case tfStepAppID:
		var cmd tea.Cmd
		m.appIDInput, cmd = m.appIDInput.Update(msg)
		return m, cmd
	case tfStepIPAPath:
		var cmd tea.Cmd
		m.ipaPathInput, cmd = m.ipaPathInput.Update(msg)
		return m, cmd
	case tfStepGroups:
		var cmd tea.Cmd
		m.groupsInput, cmd = m.groupsInput.Update(msg)
		return m, cmd
	case tfStepWhatsNew:
		var cmd tea.Cmd
		m.whatsNewInput, cmd = m.whatsNewInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m publishTestFlightModel) updateCredentialsInputs(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd
	m.keyIDInput, cmd = m.keyIDInput.Update(msg)
	cmds = append(cmds, cmd)
	m.issuerIDInput, cmd = m.issuerIDInput.Update(msg)
	cmds = append(cmds, cmd)
	m.privateKeyInput, cmd = m.privateKeyInput.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m publishTestFlightModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "esc" && !m.running {
		return m, nil
	}

	switch m.step {
	case tfStepCredentials:
		return m.handleCredentialStepKey(key)
	case tfStepAppID:
		return m.handleAppIDStepKey(key)
	case tfStepIPAPath:
		return m.handleIPAPathStepKey(key)
	case tfStepGroups:
		return m.handleGroupsStepKey(key)
	case tfStepWhatsNew:
		return m.handleWhatsNewStepKey(key)
	case tfStepConfirm:
		return m.handleConfirmStepKey(key)
	case tfStepDone:
		return m.handleDoneStepKey(key)
	}

	return m, nil
}

func (m publishTestFlightModel) handleCredentialStepKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "tab", "down", "j":
		m.credCursor = (m.credCursor + 1) % 3
		m.focusCredentialInput()
		return m, textinput.Blink
	case "shift+tab", "up", "k":
		m.credCursor = (m.credCursor + 2) % 3
		m.focusCredentialInput()
		return m, textinput.Blink
	case "enter":
		keyID := strings.TrimSpace(m.keyIDInput.Value())
		issuerID := strings.TrimSpace(m.issuerIDInput.Value())
		privatePath := strings.TrimSpace(m.privateKeyInput.Value())

		if keyID == "" && issuerID == "" && privatePath == "" && m.hasStoredCreds {
			m.step = tfStepAppID
			m.focusStepInput()
			return m, textinput.Blink
		}
		if keyID == "" || issuerID == "" || privatePath == "" {
			m.err = fmt.Errorf("key ID, issuer ID, and private key path are required (or configure REVYL_ASC_* env vars)")
			return m, nil
		}

		resolved := privatePath
		if strings.HasPrefix(resolved, "~/") {
			if home, homeErr := os.UserHomeDir(); homeErr == nil {
				resolved = filepath.Join(home, strings.TrimPrefix(resolved, "~/"))
			}
		}
		absPath, err := filepath.Abs(resolved)
		if err != nil {
			m.err = fmt.Errorf("failed to resolve private key path: %w", err)
			return m, nil
		}
		if _, err := os.Stat(absPath); err != nil {
			m.err = fmt.Errorf("private key file not found: %s", absPath)
			return m, nil
		}
		if _, err := asc.LoadPrivateKey(absPath); err != nil {
			m.err = fmt.Errorf("invalid private key: %w", err)
			return m, nil
		}

		mgr := store.NewManager()
		if err := mgr.SaveIOSCredentials(&store.IOSCredentials{
			KeyID:          keyID,
			IssuerID:       issuerID,
			PrivateKeyPath: absPath,
		}); err != nil {
			m.err = fmt.Errorf("failed to save credentials: %w", err)
			return m, nil
		}

		m.hasStoredCreds = true
		m.storeSummary = "Saved to ~/.revyl/store-credentials.json"
		m.err = nil
		m.step = tfStepAppID
		m.focusStepInput()
		return m, textinput.Blink
	}

	return m, nil
}

func (m publishTestFlightModel) handleAppIDStepKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter":
		m.err = nil
		m.step = tfStepIPAPath
		m.focusStepInput()
		return m, textinput.Blink
	case "backspace":
		if m.appIDInput.Value() == "" {
			m.step = tfStepCredentials
			m.focusStepInput()
			return m, textinput.Blink
		}
	}
	return m, nil
}

func (m publishTestFlightModel) handleIPAPathStepKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter":
		raw := strings.TrimSpace(m.ipaPathInput.Value())
		if raw != "" {
			resolved := raw
			if strings.HasPrefix(resolved, "~/") {
				if home, homeErr := os.UserHomeDir(); homeErr == nil {
					resolved = filepath.Join(home, strings.TrimPrefix(resolved, "~/"))
				}
			}
			absPath, err := filepath.Abs(resolved)
			if err != nil {
				m.err = fmt.Errorf("failed to resolve IPA path: %w", err)
				return m, nil
			}
			if _, err := os.Stat(absPath); err != nil {
				m.err = fmt.Errorf("IPA file not found: %s", absPath)
				return m, nil
			}
			if strings.ToLower(filepath.Ext(absPath)) != ".ipa" {
				m.err = fmt.Errorf("file must be an .ipa")
				return m, nil
			}
			m.ipaPathInput.SetValue(absPath)
		}
		m.err = nil
		m.step = tfStepGroups
		m.focusStepInput()
		return m, textinput.Blink
	case "backspace":
		if m.ipaPathInput.Value() == "" {
			m.step = tfStepAppID
			m.focusStepInput()
			return m, textinput.Blink
		}
	}
	return m, nil
}

func (m publishTestFlightModel) handleGroupsStepKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter":
		m.err = nil
		m.step = tfStepWhatsNew
		m.focusStepInput()
		return m, textinput.Blink
	case "backspace":
		if m.groupsInput.Value() == "" {
			m.step = tfStepIPAPath
			m.focusStepInput()
			return m, textinput.Blink
		}
	}
	return m, nil
}

func (m publishTestFlightModel) handleWhatsNewStepKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter":
		m.err = nil
		m.step = tfStepConfirm
		m.blurAllInputs()
		return m, nil
	case "backspace":
		if m.whatsNewInput.Value() == "" {
			m.step = tfStepGroups
			m.focusStepInput()
			return m, textinput.Blink
		}
	}
	return m, nil
}

func (m publishTestFlightModel) handleConfirmStepKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "w":
		m.waitForProcessing = !m.waitForProcessing
		return m, nil
	case "backspace", "n":
		m.step = tfStepWhatsNew
		m.focusStepInput()
		return m, textinput.Blink
	case "enter", "y":
		m.running = true
		m.step = tfStepRunning
		m.err = nil
		return m, tea.Batch(m.spinner.Tick, runPublishTestFlightProcessCmd(m.buildCommandArgs()))
	}
	return m, nil
}

func (m publishTestFlightModel) handleDoneStepKey(key string) (tea.Model, tea.Cmd) {
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
		m.runAgain = m.doneCursor == 0
	}
	return m, nil
}

func (m *publishTestFlightModel) focusStepInput() {
	m.blurAllInputs()
	switch m.step {
	case tfStepCredentials:
		m.focusCredentialInput()
	case tfStepAppID:
		m.appIDInput.Focus()
	case tfStepIPAPath:
		m.ipaPathInput.Focus()
	case tfStepGroups:
		m.groupsInput.Focus()
	case tfStepWhatsNew:
		m.whatsNewInput.Focus()
	}
}

func (m *publishTestFlightModel) focusCredentialInput() {
	m.keyIDInput.Blur()
	m.issuerIDInput.Blur()
	m.privateKeyInput.Blur()
	switch m.credCursor {
	case 1:
		m.issuerIDInput.Focus()
	case 2:
		m.privateKeyInput.Focus()
	default:
		m.keyIDInput.Focus()
	}
}

func (m *publishTestFlightModel) blurAllInputs() {
	m.keyIDInput.Blur()
	m.issuerIDInput.Blur()
	m.privateKeyInput.Blur()
	m.appIDInput.Blur()
	m.ipaPathInput.Blur()
	m.groupsInput.Blur()
	m.whatsNewInput.Blur()
}

func (m publishTestFlightModel) buildCommandArgs() []string {
	args := []string{"publish", "testflight"}
	if v := strings.TrimSpace(m.appIDInput.Value()); v != "" {
		args = append(args, "--app-id", v)
	}
	if v := strings.TrimSpace(m.ipaPathInput.Value()); v != "" {
		args = append(args, "--ipa", v)
	}
	if v := strings.TrimSpace(m.groupsInput.Value()); v != "" {
		args = append(args, "--group", v)
	}
	if v := strings.TrimSpace(m.whatsNewInput.Value()); v != "" {
		args = append(args, "--whats-new", v)
	}
	if !m.waitForProcessing {
		args = append(args, "--wait=false")
	}
	return args
}

func runPublishTestFlightProcessCmd(args []string) tea.Cmd {
	return tea.ExecProcess(publishExecCmd(args), func(err error) tea.Msg {
		return publishTestFlightDoneMsg{Err: err}
	})
}

func publishExecCmd(args []string) *exec.Cmd {
	exe, err := os.Executable()
	if err != nil {
		exe = "revyl"
	}
	return exec.Command(exe, args...)
}

func (m publishTestFlightModel) View() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	sepW := min(w, 72)

	b.WriteString(titleStyle.Render(" REVYL") + "  " + dimStyle.Render("Publish to TestFlight") + "\n")
	b.WriteString(separator(sepW) + "\n\n")

	steps := []string{"Credentials", "App", "IPA", "Groups", "Notes", "Confirm", "Run"}
	for i, step := range steps {
		style := dimStyle
		if i == int(m.step) || (m.step == tfStepDone && i == len(steps)-1) {
			style = selectedStyle
		} else if i < int(m.step) {
			style = successStyle
		}
		if i > 0 {
			b.WriteString(dimStyle.Render(" → "))
		}
		b.WriteString(style.Render(step))
	}
	b.WriteString("\n\n")

	switch m.step {
	case tfStepCredentials:
		b.WriteString("  " + normalStyle.Render("App Store Connect credentials") + "\n")
		b.WriteString("  " + dimStyle.Render("Status: "+m.storeSummary) + "\n\n")
		b.WriteString("  " + dimStyle.Render("Key ID") + "\n")
		b.WriteString("  " + m.keyIDInput.View() + "\n")
		b.WriteString("  " + dimStyle.Render("Issuer ID") + "\n")
		b.WriteString("  " + m.issuerIDInput.View() + "\n")
		b.WriteString("  " + dimStyle.Render("Private key path (.p8)") + "\n")
		b.WriteString("  " + m.privateKeyInput.View() + "\n")
		b.WriteString("  " + dimStyle.Render("You can also use REVYL_ASC_* env vars for CI/non-interactive runs") + "\n")
		if m.err != nil {
			b.WriteString("  " + errorStyle.Render(m.err.Error()) + "\n")
		}
		b.WriteString("\n  " + helpStyle.Render("tab/↑/↓ switch field, enter continue/save, esc cancel") + "\n")

	case tfStepAppID:
		b.WriteString("  " + normalStyle.Render("App Store Connect app ID") + " " + dimStyle.Render("(optional)") + "\n")
		b.WriteString("  " + m.appIDInput.View() + "\n")
		b.WriteString("  " + dimStyle.Render("Leave empty to resolve from config/env bundle ID") + "\n")
		if m.err != nil {
			b.WriteString("  " + errorStyle.Render(m.err.Error()) + "\n")
		}
		b.WriteString("\n  " + helpStyle.Render("enter to continue, backspace (empty) to go back") + "\n")

	case tfStepIPAPath:
		b.WriteString("  " + normalStyle.Render("IPA path") + " " + dimStyle.Render("(optional)") + "\n")
		b.WriteString("  " + m.ipaPathInput.View() + "\n")
		b.WriteString("  " + dimStyle.Render("Leave empty to distribute the latest processed build") + "\n")
		if m.err != nil {
			b.WriteString("  " + errorStyle.Render(m.err.Error()) + "\n")
		}
		b.WriteString("\n  " + helpStyle.Render("enter to continue, backspace (empty) to go back") + "\n")

	case tfStepGroups:
		b.WriteString("  " + normalStyle.Render("TestFlight groups") + " " + dimStyle.Render("(optional)") + "\n")
		b.WriteString("  " + m.groupsInput.View() + "\n")
		b.WriteString("  " + dimStyle.Render("Comma-separated group names, e.g. Internal,External") + "\n")
		if m.err != nil {
			b.WriteString("  " + errorStyle.Render(m.err.Error()) + "\n")
		}
		b.WriteString("\n  " + helpStyle.Render("enter to continue, backspace (empty) to go back") + "\n")

	case tfStepWhatsNew:
		b.WriteString("  " + normalStyle.Render("\"What to Test\" notes") + " " + dimStyle.Render("(optional)") + "\n")
		b.WriteString("  " + m.whatsNewInput.View() + "\n")
		if m.err != nil {
			b.WriteString("  " + errorStyle.Render(m.err.Error()) + "\n")
		}
		b.WriteString("\n  " + helpStyle.Render("enter to continue, backspace (empty) to go back") + "\n")

	case tfStepConfirm:
		waitValue := "true"
		if !m.waitForProcessing {
			waitValue = "false"
		}
		ipaValue := strings.TrimSpace(m.ipaPathInput.Value())
		if ipaValue == "" {
			ipaValue = "(latest build)"
		}
		groupsValue := strings.TrimSpace(m.groupsInput.Value())
		if groupsValue == "" {
			groupsValue = "(none)"
		}
		notesValue := strings.TrimSpace(m.whatsNewInput.Value())
		if notesValue == "" {
			notesValue = "(none)"
		}
		b.WriteString("  " + normalStyle.Render("Review") + "\n\n")
		b.WriteString("  " + dimStyle.Render("App ID:      ") + normalStyle.Render(strings.TrimSpace(m.appIDInput.Value())) + "\n")
		b.WriteString("  " + dimStyle.Render("IPA:         ") + normalStyle.Render(ipaValue) + "\n")
		b.WriteString("  " + dimStyle.Render("Groups:      ") + normalStyle.Render(groupsValue) + "\n")
		b.WriteString("  " + dimStyle.Render("What to test:") + normalStyle.Render(" "+notesValue) + "\n")
		b.WriteString("  " + dimStyle.Render("Wait:        ") + normalStyle.Render(waitValue) + "\n\n")
		b.WriteString("  " + dimStyle.Render("Command: revyl "+strings.Join(m.buildCommandArgs(), " ")) + "\n")
		if m.err != nil {
			b.WriteString("\n  " + errorStyle.Render(m.err.Error()) + "\n")
		}
		b.WriteString("\n  " + helpStyle.Render("enter/y run, backspace/n go back, w toggle wait") + "\n")

	case tfStepRunning:
		b.WriteString("  " + m.spinner.View() + " Running publish command...\n")
		b.WriteString("  " + dimStyle.Render("Terminal output is streamed directly while the command runs") + "\n")

	case tfStepDone:
		if m.err != nil {
			b.WriteString("  " + errorStyle.Render("✗ Publish failed") + "\n")
			b.WriteString("  " + dimStyle.Render(m.err.Error()) + "\n\n")
		} else {
			b.WriteString("  " + successStyle.Render("✓ Publish command completed") + "\n\n")
		}

		options := []string{"Publish another build", "Back to dashboard"}
		for i, option := range options {
			cur := "  "
			style := normalStyle
			if i == m.doneCursor {
				cur = selectedStyle.Render("▸ ")
				style = selectedStyle
			}
			b.WriteString("  " + cur + style.Render(option) + "\n")
		}
		b.WriteString("\n  " + helpStyle.Render("enter to select") + "\n")
	}

	return b.String()
}
