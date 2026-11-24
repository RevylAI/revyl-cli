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
	integrationsPollInterval = 3 * time.Second

	// integrationsPollTimeout is the deadline checked after unsuccessful connect
	// polls before stopping the poll loop and prompting a manual refresh.
	integrationsPollTimeout = 3 * time.Minute

	integrationsPublishTimeout = 3 * time.Minute
)

type integrationAction struct {
	key   string
	label string
	desc  string
}

var githubIntegrationActions = []integrationAction{
	{key: "connect", label: "Connect GitHub", desc: "Install the Revyl GitHub App in your browser"},
	{key: "publish", label: "Publish config", desc: "Publish the complete project configuration"},
	{key: "open", label: "Open dashboard", desc: "Manage GitHub in the web dashboard"},
}

type IntegrationsStatusMsg struct {
	Repos *api.GithubRepositoriesResponse
	Err   error
}

type IntegrationsConnectStartedMsg struct {
	Install    *api.GithubInstallURLResponse
	BrowserErr error
	Err        error
}

// IntegrationsPollTickMsg fires on each connect poll interval. Seq guards
// against stale loops after the user cancels or restarts.
type IntegrationsPollTickMsg struct {
	Seq int
}

type IntegrationsConnectCheckMsg struct {
	Repos *api.GithubRepositoriesResponse
	Err   error
	Seq   int
}

type IntegrationsPublishDoneMsg struct {
	Outcome api.ProjectConfigurationReplaceResponseOutcome
	Err     error
}

func fetchIntegrationsStatusCmd(client *api.Client) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return IntegrationsStatusMsg{Err: fmt.Errorf("not authenticated")}
		}
		repos, err := client.GetGithubRepositories(context.Background())
		return IntegrationsStatusMsg{Repos: repos, Err: err}
	}
}

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

func integrationsPollTickCmd(seq int) tea.Cmd {
	return tea.Tick(integrationsPollInterval, func(time.Time) tea.Msg {
		return IntegrationsPollTickMsg{Seq: seq}
	})
}

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

func (m hubModel) openIntegrationsDashboard() (tea.Model, tea.Cmd) {
	url := config.GetAppURL(m.devMode) + "/integrations/github"
	_ = ui.OpenBrowser(url)
	m.integrationsStatus = "Opened the dashboard in your browser."
	m.integrationsStatusErr = false
	return m, nil
}

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

func renderIntegrations(m hubModel) string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	bannerContent := titleStyle.Render("REVYL") + "  " + versionStyle.Render(ui.DisplayVersion(m.version))
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

func renderGithubStatusBadge(m hubModel) string {
	if m.integrationsLoading && m.integrationsRepos == nil {
		return dimStyle.Render("checking...")
	}
	if m.integrationsRepos.IsConnected() {
		return successStyle.Render("connected · PR automation available")
	}
	return warningStyle.Render("not connected")
}

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
