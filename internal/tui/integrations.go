// Package tui — integrations screen.
//
// The integrations screen makes GitHub PR automation first-class from the TUI:
// it shows live connection status and lets the user install the Revyl GitHub
// App (browser-initiated) or open canonical project settings. Scope is
// GitHub-only for now; the screen is a list so other integrations can be added
// later.
package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/projectpublication"
	"github.com/revyl/cli/internal/ui"
)

const (
	// integrationsPollInterval is how often the connect flow re-checks the
	// installation status while the user completes the browser install.
	integrationsPollInterval = 3 * time.Second

	// integrationsPollTimeout bounds how long the connect flow waits for the
	// browser install before giving up (the user can refresh to continue).
	integrationsPollTimeout = 3 * time.Minute

	integrationsPublishTimeout = 3 * time.Minute
)

// integrationAction is a selectable action row on the integrations screen.
type integrationAction struct {
	key   string // dispatch key + hotkey letter
	label string // display label
	desc  string // short description
}

// githubIntegrationActions are the actions available for the GitHub row.
var githubIntegrationActions = []integrationAction{
	{key: "connect", label: "Connect GitHub", desc: "Install the Revyl GitHub App in your browser"},
	{key: "publish", label: "Publish config", desc: "Publish the complete project configuration"},
	{key: "open", label: "Open dashboard", desc: "Manage GitHub in the web dashboard"},
}

// --- Messages ---

// IntegrationsStatusMsg carries the result of a GitHub status fetch.
type IntegrationsStatusMsg struct {
	Repos *api.GithubRepositoriesResponse
	Err   error
}

// IntegrationsConnectStartedMsg carries the result of starting the connect flow
// (fetching the install URL and opening the browser).
type IntegrationsConnectStartedMsg struct {
	Install    *api.GithubInstallURLResponse
	BrowserErr error // non-nil when the browser could not be opened
	Err        error // non-nil when fetching the install URL failed
}

// IntegrationsPollTickMsg fires on each connect poll interval. Seq guards
// against stale loops after the user cancels or restarts.
type IntegrationsPollTickMsg struct {
	Seq int
}

// IntegrationsConnectCheckMsg carries an installation status check made during
// the connect poll loop.
type IntegrationsConnectCheckMsg struct {
	Repos *api.GithubRepositoriesResponse
	Err   error
	Seq   int
}

// IntegrationsPublishDoneMsg carries the result of publishing the complete
// canonical project configuration.
type IntegrationsPublishDoneMsg struct {
	Outcome api.ProjectConfigurationReplaceResponseOutcome
	Err     error
}

// --- Commands ---

// fetchIntegrationsStatusCmd fetches the current GitHub installation status.
//
// Parameters:
//   - client: The authenticated API client.
//
// Returns:
//   - tea.Cmd: A command producing an IntegrationsStatusMsg.
func fetchIntegrationsStatusCmd(client *api.Client) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return IntegrationsStatusMsg{Err: fmt.Errorf("not authenticated")}
		}
		repos, err := client.GetGithubRepositories(context.Background())
		return IntegrationsStatusMsg{Repos: repos, Err: err}
	}
}

// startIntegrationsConnectCmd fetches the install URL and opens it in a browser.
//
// Parameters:
//   - client: The authenticated API client.
//
// Returns:
//   - tea.Cmd: A command producing an IntegrationsConnectStartedMsg.
func startIntegrationsConnectCmd(client *api.Client) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return IntegrationsConnectStartedMsg{Err: fmt.Errorf("not authenticated")}
		}
		install, err := client.GetGithubInstallURL(context.Background())
		if err != nil {
			return IntegrationsConnectStartedMsg{Err: err}
		}
		browserErr := ui.OpenBrowser(install.InstallURL)
		return IntegrationsConnectStartedMsg{Install: install, BrowserErr: browserErr}
	}
}

// integrationsPollTickCmd schedules the next connect poll tick.
//
// Parameters:
//   - seq: The poll sequence token to echo back.
//
// Returns:
//   - tea.Cmd: A command producing an IntegrationsPollTickMsg after the interval.
func integrationsPollTickCmd(seq int) tea.Cmd {
	return tea.Tick(integrationsPollInterval, func(time.Time) tea.Msg {
		return IntegrationsPollTickMsg{Seq: seq}
	})
}

// integrationsConnectCheckCmd checks installation status during the poll loop.
//
// Parameters:
//   - client: The authenticated API client.
//   - seq: The poll sequence token to echo back.
//
// Returns:
//   - tea.Cmd: A command producing an IntegrationsConnectCheckMsg.
func integrationsConnectCheckCmd(client *api.Client, seq int) tea.Cmd {
	return func() tea.Msg {
		repos, err := client.GetGithubRepositories(context.Background())
		return IntegrationsConnectCheckMsg{Repos: repos, Err: err, Seq: seq}
	}
}

// publishProjectConfigurationCmd resolves and publishes the nearest canonical
// project config through the same observed-state operation as config push.
func publishProjectConfigurationCmd(client *api.Client, cwd string) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return IntegrationsPublishDoneMsg{Err: fmt.Errorf("not authenticated")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), integrationsPublishTimeout)
		defer cancel()
		candidate, err := projectpublication.ResolveCandidate(cwd)
		if err != nil {
			return IntegrationsPublishDoneMsg{Err: err}
		}
		result, err := projectpublication.Publish(ctx, client, *candidate)
		if err != nil {
			return IntegrationsPublishDoneMsg{Err: err}
		}
		if result == nil {
			return IntegrationsPublishDoneMsg{Err: fmt.Errorf("server returned no publication result")}
		}
		return IntegrationsPublishDoneMsg{Outcome: result.Outcome}
	}
}

// --- Model transitions ---

// enterIntegrationsView switches to the integrations screen and kicks off the
// initial status fetch.
//
// Returns:
//   - tea.Model: The updated model.
//   - tea.Cmd: The status fetch command, or nil when unauthenticated.
func (m hubModel) enterIntegrationsView() (tea.Model, tea.Cmd) {
	m.currentView = viewIntegrations
	m.integrationsCursor = 0
	m.integrationsRepos = nil
	m.integrationsStatus = ""
	m.integrationsStatusErr = false
	m.integrationsConnecting = false
	if m.client != nil {
		m.integrationsLoading = true
		return m, fetchIntegrationsStatusCmd(m.client)
	}
	m.integrationsLoading = false
	return m, nil
}

// handleIntegrationsKey processes key events on the integrations screen.
//
// Parameters:
//   - m: The hub model.
//   - msg: The key message.
//
// Returns:
//   - tea.Model: The updated model.
//   - tea.Cmd: A command to run, or nil.
func handleIntegrationsKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		// Invalidate any in-flight connect poll before leaving.
		m.integrationsPollSeq++
		m.integrationsConnecting = false
		m.currentView = viewDashboard
		return m, nil
	case "up", "k":
		if m.integrationsCursor > 0 {
			m.integrationsCursor--
		}
		return m, nil
	case "down", "j":
		if m.integrationsCursor < len(githubIntegrationActions)-1 {
			m.integrationsCursor++
		}
		return m, nil
	case "r", "R":
		return m.refreshIntegrationsStatus()
	case "c":
		return m.startGithubConnect()
	case "p":
		return m.startProjectConfigurationPublish()
	case "o":
		return m.openIntegrationsDashboard()
	case "enter":
		if m.integrationsCursor < 0 || m.integrationsCursor >= len(githubIntegrationActions) {
			return m, nil
		}
		switch githubIntegrationActions[m.integrationsCursor].key {
		case "connect":
			return m.startGithubConnect()
		case "publish":
			return m.startProjectConfigurationPublish()
		case "open":
			return m.openIntegrationsDashboard()
		}
	}
	return m, nil
}

// refreshIntegrationsStatus re-fetches the GitHub installation status.
func (m hubModel) refreshIntegrationsStatus() (tea.Model, tea.Cmd) {
	if m.client == nil || m.integrationsPublishing {
		return m, nil
	}
	m.integrationsLoading = true
	m.integrationsStatus = ""
	m.integrationsStatusErr = false
	return m, fetchIntegrationsStatusCmd(m.client)
}

func (m hubModel) startGithubConnect() (tea.Model, tea.Cmd) {
	if m.client == nil {
		return m.requireAuthentication("Connecting GitHub requires authentication.")
	}
	if m.integrationsConnecting || m.integrationsPublishing {
		return m, nil
	}
	if m.integrationsRepos.IsConnected() {
		m.integrationsStatus = "GitHub is already connected."
		m.integrationsStatusErr = false
		return m, nil
	}
	m.integrationsConnecting = true
	m.integrationsStatus = "Opening the GitHub App install page in your browser ..."
	m.integrationsStatusErr = false
	return m, startIntegrationsConnectCmd(m.client)
}

func (m hubModel) startProjectConfigurationPublish() (tea.Model, tea.Cmd) {
	if m.client == nil {
		return m.requireAuthentication("Publishing project configuration requires authentication.")
	}
	if m.integrationsLoading || m.integrationsConnecting || m.integrationsPublishing {
		return m, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		m.integrationsStatus = fmt.Sprintf("Could not resolve project directory: %v", err)
		m.integrationsStatusErr = true
		return m, nil
	}
	m.integrationsPublishing = true
	m.integrationsStatus = "Publishing the complete project configuration ..."
	m.integrationsStatusErr = false
	return m, publishProjectConfigurationCmd(m.client, cwd)
}

// openIntegrationsDashboard opens the web dashboard's GitHub integration page.
func (m hubModel) openIntegrationsDashboard() (tea.Model, tea.Cmd) {
	url := config.GetAppURL(m.devMode) + "/integrations/github"
	_ = ui.OpenBrowser(url)
	m.integrationsStatus = "Opened the dashboard in your browser."
	m.integrationsStatusErr = false
	return m, nil
}

// --- Message handlers ---

// updateIntegrationsStatus applies a status fetch result.
func updateIntegrationsStatus(m hubModel, msg IntegrationsStatusMsg) (tea.Model, tea.Cmd) {
	m.integrationsLoading = false
	if msg.Err != nil {
		m.integrationsStatus = fmt.Sprintf("Failed to load GitHub status: %v", msg.Err)
		m.integrationsStatusErr = true
		return m, nil
	}
	m.integrationsRepos = msg.Repos
	return m, nil
}

// updateIntegrationsConnectStarted handles the result of opening the browser and
// starts the poll loop on success.
func updateIntegrationsConnectStarted(m hubModel, msg IntegrationsConnectStartedMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.integrationsConnecting = false
		m.integrationsStatus = fmt.Sprintf("Could not start GitHub install: %v", msg.Err)
		m.integrationsStatusErr = true
		return m, nil
	}
	if msg.BrowserErr != nil && msg.Install != nil {
		m.integrationsStatus = "Open this URL to install: " + msg.Install.InstallURL
	} else {
		m.integrationsStatus = "Waiting for the install to finish in your browser ..."
	}
	m.integrationsStatusErr = false
	m.integrationsPollSeq++
	m.integrationsConnectDeadline = time.Now().Add(integrationsPollTimeout)
	return m, integrationsPollTickCmd(m.integrationsPollSeq)
}

// updateIntegrationsPollTick triggers a status check on each poll tick.
func updateIntegrationsPollTick(m hubModel, msg IntegrationsPollTickMsg) (tea.Model, tea.Cmd) {
	if msg.Seq != m.integrationsPollSeq || !m.integrationsConnecting {
		return m, nil
	}
	if m.client == nil {
		m.integrationsConnecting = false
		return m, nil
	}
	return m, integrationsConnectCheckCmd(m.client, msg.Seq)
}

// updateIntegrationsConnectCheck advances the connect loop: completing on an
// active install, timing out past the deadline, or scheduling the next tick.
func updateIntegrationsConnectCheck(m hubModel, msg IntegrationsConnectCheckMsg) (tea.Model, tea.Cmd) {
	if msg.Seq != m.integrationsPollSeq || !m.integrationsConnecting {
		return m, nil
	}
	if msg.Err == nil && msg.Repos.IsConnected() {
		m.integrationsConnecting = false
		m.integrationsRepos = msg.Repos
		m.integrationsStatus = "GitHub connected."
		m.integrationsStatusErr = false
		return m, nil
	}
	if time.Now().After(m.integrationsConnectDeadline) {
		m.integrationsConnecting = false
		m.integrationsStatus = "Timed out waiting for the install. Finish it in the browser, then press r to refresh."
		m.integrationsStatusErr = true
		return m, nil
	}
	return m, integrationsPollTickCmd(msg.Seq)
}

func updateIntegrationsPublishDone(m hubModel, msg IntegrationsPublishDoneMsg) (tea.Model, tea.Cmd) {
	m.integrationsPublishing = false
	if msg.Err != nil {
		m.integrationsStatus = fmt.Sprintf("Publish failed: %v", actionableProjectConfigError(msg.Err))
		m.integrationsStatusErr = true
		return m, nil
	}
	if msg.Outcome == api.ProjectConfigurationReplaceResponseOutcomeUnchanged {
		m.integrationsStatus = "Revyl already has this project configuration."
	} else {
		m.integrationsStatus = "Published the complete project configuration."
	}
	m.integrationsStatusErr = false
	return m, nil
}

// --- Rendering ---

// renderIntegrations renders the integrations screen.
//
// Parameters:
//   - m: The hub model.
//
// Returns:
//   - string: The full screen view.
func renderIntegrations(m hubModel) string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	bannerContent := titleStyle.Render("REVYL") + "  " + versionStyle.Render("v"+m.version)
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	b.WriteString(sectionStyle.Render("  INTEGRATIONS") + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	b.WriteString("  " + normalStyle.Render("GitHub") + "   " + renderGithubStatusBadge(m) + "\n")
	b.WriteString("  " + dimStyle.Render(githubStatusDetail(m)) + "\n\n")

	for i, a := range githubIntegrationActions {
		cur := "  "
		labelStyle := normalStyle
		if i == m.integrationsCursor {
			cur = selectedStyle.Render("▸ ")
			labelStyle = selectedStyle
		}
		key := dimStyle.Render(fmt.Sprintf("[%s] ", integrationActionHotkey(a.key)))
		b.WriteString("  " + cur + key + labelStyle.Render(a.label) + actionDescStyle.Render("  "+a.desc) + "\n")
	}

	if m.integrationsPublishing {
		b.WriteString("\n  " + runningStyle.Render("● ") + normalStyle.Render(m.integrationsStatus) + "\n")
	} else if m.integrationsLoading {
		b.WriteString("\n  " + dimStyle.Render("Loading status...") + "\n")
	} else if m.integrationsConnecting {
		b.WriteString("\n  " + runningStyle.Render("● ") + normalStyle.Render(m.integrationsStatus) + "\n")
	} else if m.integrationsStatus != "" {
		if m.integrationsStatusErr {
			b.WriteString("\n  " + errorStyle.Render("✗ "+m.integrationsStatus) + "\n")
		} else {
			b.WriteString("\n  " + successStyle.Render("✓ "+m.integrationsStatus) + "\n")
		}
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	keys := []string{
		helpKeyRender("↑/↓", "move"),
		helpKeyRender("enter", "select"),
		helpKeyRender("c", "connect"),
		helpKeyRender("p", "publish"),
		helpKeyRender("o", "open"),
		helpKeyRender("r", "refresh"),
		helpKeyRender("esc", "back"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")
	return b.String()
}

// integrationActionHotkey returns the single-key shortcut for an action.
func integrationActionHotkey(key string) string {
	switch key {
	case "connect":
		return "c"
	case "publish":
		return "p"
	case "open":
		return "o"
	}
	return "?"
}

// renderGithubStatusBadge renders the GitHub connection status badge.
func renderGithubStatusBadge(m hubModel) string {
	if m.integrationsLoading && m.integrationsRepos == nil {
		return dimStyle.Render("checking...")
	}
	if m.integrationsRepos.IsConnected() {
		return successStyle.Render("connected · PR automation available")
	}
	return warningStyle.Render("not connected")
}

// githubStatusDetail renders the secondary status detail line.
func githubStatusDetail(m hubModel) string {
	if m.integrationsRepos == nil {
		return "Connect the Revyl GitHub App to enable PR automation."
	}
	if !m.integrationsRepos.IsConnected() {
		return "Press c to install the Revyl GitHub App in your browser."
	}
	n := len(m.integrationsRepos.Repositories)
	if n == 1 {
		return "1 repository accessible · open the dashboard to manage projects"
	}
	return fmt.Sprintf("%d repositories accessible · open the dashboard to manage projects", n)
}
