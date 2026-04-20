package tui

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	startdevice "github.com/revyl/cli/internal/device"
	"github.com/revyl/cli/internal/devicetargets"
	"github.com/revyl/cli/internal/ui"
)

const deviceDetailPollInterval = 3 * time.Second

type deviceStartStep int

const (
	deviceStartStepPlatform deviceStartStep = iota
	deviceStartStepApp
	deviceStartStepDevice
)

var openBrowserFn = ui.OpenBrowser

// --- Tea commands ---

// fetchDeviceSessionsCmd fetches active device sessions for the org.
//
// Parameters:
//   - client: authenticated API client
//   - orgID: cached organization ID; when empty, this command resolves it once via ValidateAPIKey
//
// Returns:
//   - tea.Cmd: async command that sends DeviceSessionListMsg on completion
func fetchDeviceSessionsCmd(client *api.Client, orgID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		resolvedOrgID := strings.TrimSpace(orgID)
		if resolvedOrgID == "" {
			info, err := client.ValidateAPIKey(ctx)
			if err != nil {
				return DeviceSessionListMsg{Err: fmt.Errorf("failed to authenticate: %w", err)}
			}
			resolvedOrgID = strings.TrimSpace(info.OrgID)
			if resolvedOrgID == "" {
				return DeviceSessionListMsg{Err: fmt.Errorf("failed to authenticate: organization ID missing")}
			}
		}

		sessions, err := client.GetActiveDeviceSessions(ctx, resolvedOrgID)
		if err != nil {
			return DeviceSessionListMsg{Err: err}
		}

		return DeviceSessionListMsg{
			Sessions: sessions.Sessions,
			OrgID:    resolvedOrgID,
		}
	}
}

// fetchDeviceStartAppsCmd fetches org apps for the selected platform.
//
// Parameters:
//   - client: authenticated API client
//   - platform: "android" or "ios"
//
// Returns:
//   - tea.Cmd: async command that sends DeviceStartAppListMsg on completion
func fetchDeviceStartAppsCmd(client *api.Client, platform string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		page := 1
		apps := make([]api.App, 0, 16)
		for {
			resp, err := client.ListApps(ctx, platform, page, 100)
			if err != nil {
				return DeviceStartAppListMsg{
					Platform: platform,
					Err:      fmt.Errorf("failed to fetch apps: %w", err),
				}
			}
			apps = append(apps, resp.Items...)
			if !resp.HasNext {
				break
			}
			page++
		}

		return DeviceStartAppListMsg{
			Platform: platform,
			Apps:     apps,
		}
	}
}

// startDeviceSessionCmd starts a new device session with the given platform,
// optional app, and optional device model/OS version overrides.
//
// Parameters:
//   - client: authenticated API client
//   - platform: "android" or "ios"
//   - appID: optional app ID to resolve to the latest build artifact
//   - deviceModel: device model override (empty = platform default)
//   - osVersion: OS version override (empty = platform default)
//
// Returns:
//   - tea.Cmd: async command that sends DeviceStartedMsg on completion
func startDeviceSessionCmd(client *api.Client, platform, appID, deviceModel, osVersion string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
		defer cancel()

		req := &api.StartDeviceRequest{
			Platform:     platform,
			IsSimulation: true,
			DeviceModel:  deviceModel,
			OsVersion:    osVersion,
		}
		if strings.TrimSpace(appID) != "" {
			resolved, err := startdevice.ResolveStartArtifact(ctx, client, startdevice.StartArtifactOptions{
				AppID: appID,
			})
			if err != nil {
				return DeviceStartedMsg{Err: fmt.Errorf("failed to start device: %w", err)}
			}
			req.AppURL = resolved.AppURL
			req.AppPackage = resolved.AppPackage
		}

		resp, err := client.StartDevice(ctx, req)
		if err != nil {
			return DeviceStartedMsg{Err: fmt.Errorf("failed to start device: %w", err)}
		}

		if resp.WorkflowRunId == nil || *resp.WorkflowRunId == (openapi_types.UUID{}) {
			errMsg := "no workflow run ID returned"
			if resp.Error != nil {
				errMsg = *resp.Error
			}
			return DeviceStartedMsg{Err: fmt.Errorf("failed to start device: %s", errMsg)}
		}

		viewerURL := ""
		if resp.IframeUrl != nil {
			viewerURL = *resp.IframeUrl
		}

		return DeviceStartedMsg{
			WorkflowRunID: resp.WorkflowRunId.String(),
			Platform:      platform,
			ViewerURL:     viewerURL,
		}
	}
}

// stopDeviceSessionCmd stops a device session by cancelling its workflow run.
//
// Parameters:
//   - client: authenticated API client
//   - workflowRunID: the workflow run ID to cancel
//
// Returns:
//   - tea.Cmd: async command that sends DeviceStoppedMsg on completion
func stopDeviceSessionCmd(client *api.Client, workflowRunID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		_, err := client.CancelDevice(ctx, workflowRunID)
		return DeviceStoppedMsg{Err: err}
	}
}

func deviceDetailPollTickCmd(seq int) tea.Cmd {
	return tea.Tick(deviceDetailPollInterval, func(time.Time) tea.Msg {
		return DeviceDetailPollTickMsg{Seq: seq}
	})
}

func deviceListPollTickCmd(seq int) tea.Cmd {
	return tea.Tick(deviceDetailPollInterval, func(time.Time) tea.Msg {
		return DeviceListPollTickMsg{Seq: seq}
	})
}

// --- Key handlers ---

// handleDeviceListKey processes key events on the device session list screen.
//
// Parameters:
//   - msg: the key message to handle
//
// Returns:
//   - tea.Model: updated model
//   - tea.Cmd: follow-up command (if any)
func (m hubModel) handleDeviceListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.deviceConfirmStop {
		switch msg.String() {
		case "y", "Y":
			m.deviceConfirmStop = false
			if m.deviceStopTarget != "" && m.client != nil {
				m.devicesLoading = true
				return m, stopDeviceSessionCmd(m.client, m.deviceStopTarget)
			}
			return m, nil
		default:
			m.deviceConfirmStop = false
			m.deviceStopTarget = ""
			return m, nil
		}
	}

	if m.deviceStartPicking {
		return m.handleDeviceStartKey(msg)
	}

	sessions := m.deviceSessions
	switch msg.String() {
	case "up", "k":
		if m.deviceCursor > 0 {
			m.deviceCursor--
		}
	case "down", "j":
		if m.deviceCursor < len(sessions)-1 {
			m.deviceCursor++
		}
	case "enter":
		if len(sessions) > 0 && m.deviceCursor < len(sessions) {
			selected := sessions[m.deviceCursor]
			m.selectedDeviceID = selected.Id
			m.currentView = viewDeviceDetail
			m.deviceDetailPollSeq++
			if m.client != nil && isDeviceStatusTransitional(selected.Status) {
				m.devicesLoading = true
				return m, tea.Batch(
					fetchDeviceSessionsCmd(m.client, m.deviceOrgID),
					deviceDetailPollTickCmd(m.deviceDetailPollSeq),
				)
			}
		}
	case "n":
		m = m.beginDeviceStart()
	case "d":
		if len(sessions) > 0 && m.deviceCursor < len(sessions) {
			selected := sessions[m.deviceCursor]
			if selected.WorkflowRunId != nil && *selected.WorkflowRunId != "" {
				m.deviceConfirmStop = true
				m.deviceStopTarget = *selected.WorkflowRunId
			}
		}
	case "R":
		if m.client != nil {
			m.devicesLoading = true
			return m, fetchDeviceSessionsCmd(m.client, m.deviceOrgID)
		}
	case "esc":
		m = m.resetDeviceStartOverlay()
		m.currentView = viewDashboard
		m.deviceListPollSeq++
		return m, nil
	case "q":
		return m, tea.Quit
	}

	return m, nil
}

func (m hubModel) handleDeviceStartKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.deviceStarting {
		switch msg.String() {
		case "q":
			return m, tea.Quit
		default:
			return m, nil
		}
	}

	switch deviceStartStep(m.deviceStartStep) {
	case deviceStartStepPlatform:
		return m.handleDeviceStartPlatformKey(msg)
	case deviceStartStepApp:
		return m.handleDeviceStartAppKey(msg)
	case deviceStartStepDevice:
		return m.handleDeviceStartDeviceKey(msg)
	default:
		return m, nil
	}
}

func (m hubModel) handleDeviceStartPlatformKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "esc":
		m = m.resetDeviceStartOverlay()
		return m, nil
	case "up", "k":
		if m.devicePlatformCursor > 0 {
			m.devicePlatformCursor--
		}
		return m, nil
	case "down", "j":
		if m.devicePlatformCursor < len(deviceStartPlatforms())-1 {
			m.devicePlatformCursor++
		}
		return m, nil
	case "1":
		m.devicePlatformCursor = 0
		return m.beginDeviceStartAppSelection()
	case "2":
		m.devicePlatformCursor = 1
		return m.beginDeviceStartAppSelection()
	case "enter":
		return m.beginDeviceStartAppSelection()
	default:
		return m, nil
	}
}

func (m hubModel) handleDeviceStartAppKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.deviceStartFilterMode {
		switch msg.String() {
		case "esc", "enter":
			m.deviceStartFilterMode = false
			m.deviceStartFilterInput.Blur()
			return m, nil
		default:
			var cmd tea.Cmd
			m.deviceStartFilterInput, cmd = m.deviceStartFilterInput.Update(msg)
			m.clampDeviceStartCursor()
			return m, cmd
		}
	}

	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "esc":
		m = m.resetDeviceStartOverlay()
		return m, nil
	case "backspace":
		m.deviceStartStep = int(deviceStartStepPlatform)
		m.deviceStartLoading = false
		m.deviceStartApps = nil
		m.deviceStartAppCursor = 0
		m.deviceStartErr = ""
		m.deviceStartFilterMode = false
		m.deviceStartFilterInput.Blur()
		m.deviceStartFilterInput.SetValue("")
		return m, nil
	case "/":
		m.deviceStartFilterMode = true
		m.deviceStartFilterInput.Focus()
		return m, textinput.Blink
	case "r", "R":
		if m.client == nil {
			m.deviceStartErr = "not authenticated"
			return m, nil
		}
		m.deviceStartLoading = true
		m.deviceStartErr = ""
		return m, fetchDeviceStartAppsCmd(m.client, m.deviceStartPlatform)
	case "up", "k":
		if m.deviceStartAppCursor > 0 {
			m.deviceStartAppCursor--
		}
		return m, nil
	case "down", "j":
		if m.deviceStartAppCursor < len(m.filteredDeviceStartApps()) {
			m.deviceStartAppCursor++
		}
		return m, nil
	case "enter":
		if m.deviceStartLoading {
			return m, nil
		}
		m.deviceStartFilterMode = false
		m.deviceStartFilterInput.Blur()
		m.deviceStartStep = int(deviceStartStepDevice)
		m.deviceStartDeviceCursor = 0
		m.deviceStartDeviceModel = ""
		m.deviceStartOsVersion = ""
		return m, nil
	default:
		return m, nil
	}
}

// deviceStartDeviceOptions returns "Auto" plus all valid pairs for the
// selected platform. Index 0 is always Auto.
func (m hubModel) deviceStartDeviceOptions() []devicetargets.DevicePair {
	pairs, err := devicetargets.GetAvailableTargetPairs(m.deviceStartPlatform)
	if err != nil {
		return nil
	}
	return pairs
}

func (m hubModel) handleDeviceStartDeviceKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	options := m.deviceStartDeviceOptions()
	optionCount := len(options) + 1 // +1 for the "Auto" entry at index 0

	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "esc":
		m = m.resetDeviceStartOverlay()
		return m, nil
	case "backspace":
		m.deviceStartStep = int(deviceStartStepApp)
		m.deviceStartDeviceCursor = 0
		return m, nil
	case "up", "k":
		if m.deviceStartDeviceCursor > 0 {
			m.deviceStartDeviceCursor--
		}
		return m, nil
	case "down", "j":
		if m.deviceStartDeviceCursor < optionCount-1 {
			m.deviceStartDeviceCursor++
		}
		return m, nil
	case "enter":
		if m.client == nil {
			m.deviceStartErr = "not authenticated"
			return m, nil
		}

		if m.deviceStartDeviceCursor == 0 {
			m.deviceStartDeviceModel = ""
			m.deviceStartOsVersion = ""
		} else {
			pair := options[m.deviceStartDeviceCursor-1]
			m.deviceStartDeviceModel = pair.Model
			m.deviceStartOsVersion = pair.Runtime
		}

		selectedApp := m.selectedDeviceStartApp()
		appID := ""
		if selectedApp != nil {
			appID = selectedApp.ID
		}
		m.deviceStarting = true
		m.deviceStartErr = ""
		return m, startDeviceSessionCmd(
			m.client,
			m.deviceStartPlatform,
			appID,
			m.deviceStartDeviceModel,
			m.deviceStartOsVersion,
		)
	default:
		return m, nil
	}
}

func (m hubModel) beginDeviceStart() hubModel {
	m.deviceStartPicking = true
	m.deviceStartStep = int(deviceStartStepPlatform)
	m.devicePlatformCursor = 0
	m.deviceStartPlatform = ""
	m.deviceStartApps = nil
	m.deviceStartAppCursor = 0
	m.deviceStartLoading = false
	m.deviceStartFilterMode = false
	m.deviceStartFilterInput.Blur()
	m.deviceStartFilterInput.SetValue("")
	m.deviceStartErr = ""
	m.deviceStartDeviceCursor = 0
	m.deviceStartDeviceModel = ""
	m.deviceStartOsVersion = ""
	return m
}

func (m hubModel) beginDeviceStartAppSelection() (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.deviceStartErr = "not authenticated"
		return m, nil
	}

	platforms := deviceStartPlatforms()
	if m.devicePlatformCursor < 0 || m.devicePlatformCursor >= len(platforms) {
		m.devicePlatformCursor = 0
	}

	m.deviceStartStep = int(deviceStartStepApp)
	m.deviceStartPlatform = platforms[m.devicePlatformCursor]
	m.deviceStartApps = nil
	m.deviceStartAppCursor = 0
	m.deviceStartLoading = true
	m.deviceStartErr = ""
	m.deviceStartFilterMode = false
	m.deviceStartFilterInput.Blur()
	m.deviceStartFilterInput.SetValue("")
	return m, fetchDeviceStartAppsCmd(m.client, m.deviceStartPlatform)
}

func (m hubModel) resetDeviceStartOverlay() hubModel {
	m.deviceStartPicking = false
	m.deviceStartStep = int(deviceStartStepPlatform)
	m.devicePlatformCursor = 0
	m.deviceStartPlatform = ""
	m.deviceStartApps = nil
	m.deviceStartAppCursor = 0
	m.deviceStartLoading = false
	m.deviceStartFilterMode = false
	m.deviceStartFilterInput.Blur()
	m.deviceStartFilterInput.SetValue("")
	m.deviceStartErr = ""
	m.deviceStarting = false
	m.deviceStartDeviceCursor = 0
	m.deviceStartDeviceModel = ""
	m.deviceStartOsVersion = ""
	return m
}

// handleDeviceDetailKey processes key events on the device detail screen.
//
// Parameters:
//   - msg: the key message to handle
//
// Returns:
//   - tea.Model: updated model
//   - tea.Cmd: follow-up command (if any)
func (m hubModel) handleDeviceDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.deviceConfirmStop {
		switch msg.String() {
		case "y", "Y":
			m.deviceConfirmStop = false
			if m.deviceStopTarget != "" && m.client != nil {
				m.devicesLoading = true
				m.currentView = viewDeviceList
				m.deviceDetailPollSeq++
				return m, stopDeviceSessionCmd(m.client, m.deviceStopTarget)
			}
			return m, nil
		default:
			m.deviceConfirmStop = false
			m.deviceStopTarget = ""
			return m, nil
		}
	}

	switch msg.String() {
	case "o":
		if viewerURL := m.selectedDeviceViewerURL(); viewerURL != "" {
			_ = openBrowserFn(viewerURL)
		}
	case "r":
		if reportURL := m.selectedDeviceReportURL(); reportURL != "" {
			_ = openBrowserFn(reportURL)
		}
	case "d":
		session := m.selectedDeviceSession()
		if session != nil && session.WorkflowRunId != nil && *session.WorkflowRunId != "" {
			m.deviceConfirmStop = true
			m.deviceStopTarget = *session.WorkflowRunId
		}
	case "esc":
		m.currentView = viewDeviceList
		m.deviceDetailPollSeq++
		return m, nil
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

// selectedDeviceSession returns the currently selected device session, or nil.
func (m hubModel) selectedDeviceSession() *api.ActiveDeviceSessionItem {
	for i := range m.deviceSessions {
		if m.deviceSessions[i].Id == m.selectedDeviceID {
			return &m.deviceSessions[i]
		}
	}
	return nil
}

func (m hubModel) selectedDeviceViewerURL() string {
	session := m.selectedDeviceSession()
	if session == nil {
		return ""
	}

	appURL := config.GetAppURL(m.devMode)
	sessionID := strings.TrimSpace(session.Id)

	if sessionID != "" {
		return fmt.Sprintf("%s/sessions/%s", appURL, url.PathEscape(sessionID))
	}

	if session.WhepUrl != nil {
		return strings.TrimSpace(*session.WhepUrl)
	}
	return ""
}

// selectedDeviceReportURL returns the report URL for the selected device session.
func (m hubModel) selectedDeviceReportURL() string {
	session := m.selectedDeviceSession()
	if session == nil {
		return ""
	}
	return fmt.Sprintf(
		"%s/tests/report?sessionId=%s",
		config.GetAppURL(m.devMode),
		url.QueryEscape(session.Id),
	)
}

// --- Renderers ---

// renderDeviceList renders the device session list screen.
//
// Returns:
//   - string: the rendered TUI content
func (m hubModel) renderDeviceList() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render("Device sessions")
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	sessions := m.deviceSessions
	b.WriteString(sectionStyle.Render("  Sessions") + " " + dimStyle.Render(fmt.Sprintf("%d", len(sessions))) + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	if m.deviceConfirmStop {
		b.WriteString("\n  " + warningStyle.Render("Stop this device session? (y/n)") + "\n\n")
	}

	if m.deviceStartPicking {
		b.WriteString("\n" + m.renderDeviceStartOverlay(innerW))
	}

	if m.devicesLoading {
		b.WriteString("  " + m.spinner.View() + " Loading sessions...\n")
	} else if len(sessions) == 0 {
		b.WriteString("  " + dimStyle.Render("No active device sessions") + "\n")
		b.WriteString("  " + dimStyle.Render("Press 'n' to start a new device") + "\n")
	} else {
		maxVisible := m.height - 12
		if maxVisible < 5 {
			maxVisible = 5
		}
		start, end := scrollWindow(m.deviceCursor, len(sessions), maxVisible)
		if start > 0 {
			b.WriteString(dimStyle.Render("  ↑ more") + "\n")
		}
		for i := start; i < end; i++ {
			s := sessions[i]
			cur := "  "
			nameStyle := normalStyle
			if i == m.deviceCursor {
				cur = selectedStyle.Render("▸ ")
				nameStyle = selectedRowStyle
			}
			platBadge := platformStyle.Render(" [" + s.Platform + "]")
			statusBadge := deviceStatusBadge(s.Status)
			uptime := ""
			if s.StartedAt != nil {
				uptime = dimStyle.Render("  " + formatDuration(time.Since(*s.StartedAt)))
			}
			idShort := s.Id
			if len(idShort) > 8 {
				idShort = idShort[:8]
			}
			b.WriteString("  " + cur + nameStyle.Render(idShort) + platBadge + statusBadge + uptime + "\n")
		}
		if end < len(sessions) {
			b.WriteString(dimStyle.Render("  ↓ more") + "\n")
		}
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	keys := []string{
		helpKeyRender("enter", "details"),
		helpKeyRender("n", "new device"),
		helpKeyRender("d", "stop"),
		helpKeyRender("R", "refresh"),
		helpKeyRender("esc", "back"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")
	return b.String()
}

func (m hubModel) renderDeviceStartOverlay(innerW int) string {
	var b strings.Builder

	b.WriteString("  " + sectionStyle.Render("Start device") + "\n")
	b.WriteString("  " + separator(innerW) + "\n")
	b.WriteString("  " + m.renderDeviceStartProgress() + "\n")

	if m.deviceStartErr != "" {
		b.WriteString("\n  " + errorStyle.Render(m.deviceStartErr) + "\n")
	}

	switch deviceStartStep(m.deviceStartStep) {
	case deviceStartStepPlatform:
		b.WriteString("\n  " + normalStyle.Render("Select platform:") + "\n\n")
		for i, platform := range deviceStartPlatforms() {
			cur := "  "
			style := normalStyle
			if i == m.devicePlatformCursor {
				cur = selectedStyle.Render("▸ ")
				style = selectedStyle
			}
			label := platform
			if platform == "ios" {
				label = "iOS"
			}
			if platform == "android" {
				label = "Android"
			}
			b.WriteString("  " + cur + style.Render(label) + "\n")
		}
		b.WriteString("\n  " + helpStyle.Render("enter to continue, 1/2 shortcuts, esc to cancel") + "\n")
	case deviceStartStepApp:
		b.WriteString("\n  " + dimStyle.Render("Platform: ") + platformStyle.Render(m.deviceStartPlatform) + "\n")
		if m.deviceStartFilterMode {
			b.WriteString("\n  " + filterPromptStyle.Render("/") + " " + m.deviceStartFilterInput.View() + "\n")
		}
		b.WriteString("\n  " + normalStyle.Render("Choose app:") + "\n\n")

		switch {
		case m.deviceStarting:
			b.WriteString("  " + m.spinner.View() + " Starting device...\n")
		case m.deviceStartLoading:
			b.WriteString("  " + m.spinner.View() + " Loading apps...\n")
		default:
			filteredApps := m.filteredDeviceStartApps()
			totalOptions := len(filteredApps) + 1
			maxVisible := m.height - 18
			if maxVisible < 5 {
				maxVisible = 5
			}
			start, end := scrollWindow(m.deviceStartAppCursor, totalOptions, maxVisible)
			if start > 0 {
				b.WriteString(dimStyle.Render("  ↑ more") + "\n")
			}
			for i := start; i < end; i++ {
				cur := "  "
				style := normalStyle
				if i == m.deviceStartAppCursor {
					cur = selectedStyle.Render("▸ ")
					style = selectedStyle
				}
				if i == 0 {
					b.WriteString("  " + cur + style.Render("No app") + "\n")
					b.WriteString("     " + dimStyle.Render("Start a bare streaming device") + "\n")
					continue
				}

				appInfo := filteredApps[i-1]
				b.WriteString("  " + cur + style.Render(appInfo.Name) + "\n")
				b.WriteString("     " + dimStyle.Render(formatCreateAppDetails(appInfo)) + "\n")
			}
			if end < totalOptions {
				b.WriteString(dimStyle.Render("  ↓ more") + "\n")
			}
			if len(filteredApps) == 0 && strings.TrimSpace(m.deviceStartFilterInput.Value()) != "" {
				b.WriteString("  " + dimStyle.Render("No apps match the current search") + "\n")
			}
		}

		b.WriteString("\n  " + helpStyle.Render("enter to continue, / to search, r to refresh, backspace to change platform, esc to cancel") + "\n")
	case deviceStartStepDevice:
		b.WriteString("\n  " + dimStyle.Render("Platform: ") + platformStyle.Render(m.deviceStartPlatform) + "\n")
		selectedApp := m.selectedDeviceStartApp()
		appLabel := "No app"
		if selectedApp != nil {
			appLabel = selectedApp.Name
		}
		b.WriteString("  " + dimStyle.Render("App: ") + normalStyle.Render(appLabel) + "\n")

		if m.deviceStarting {
			b.WriteString("\n  " + m.spinner.View() + " Starting device...\n")
		} else {
			b.WriteString("\n  " + normalStyle.Render("Select device:") + "\n\n")

			defaultPair, _ := devicetargets.GetDefaultPair(m.deviceStartPlatform)
			options := m.deviceStartDeviceOptions()

			autoLabel := "Auto"
			if defaultPair.Model != "" {
				autoLabel = fmt.Sprintf("Auto (%s)", devicetargets.FormatPairLabel(defaultPair))
			}

			// Auto entry at index 0
			cur := "  "
			style := normalStyle
			if m.deviceStartDeviceCursor == 0 {
				cur = selectedStyle.Render("▸ ")
				style = selectedStyle
			}
			b.WriteString("  " + cur + style.Render(autoLabel) + "\n")
			b.WriteString("     " + dimStyle.Render("Use platform default") + "\n")

			for i, pair := range options {
				cur = "  "
				style = normalStyle
				if i+1 == m.deviceStartDeviceCursor {
					cur = selectedStyle.Render("▸ ")
					style = selectedStyle
				}
				b.WriteString("  " + cur + style.Render(devicetargets.FormatPairLabel(pair)) + "\n")
			}
		}

		b.WriteString("\n  " + helpStyle.Render("enter to start, backspace to change app, esc to cancel") + "\n")
	}

	return b.String() + "\n"
}

func (m hubModel) renderDeviceStartProgress() string {
	steps := []string{"Platform", "App", "Device", "Start"}
	current := 0
	switch deviceStartStep(m.deviceStartStep) {
	case deviceStartStepApp:
		current = 1
	case deviceStartStepDevice:
		current = 2
	}
	if m.deviceStarting {
		current = 3
	}

	parts := make([]string, 0, len(steps)*2)
	for i, step := range steps {
		style := dimStyle
		if i == current {
			style = selectedStyle
		} else if i < current {
			style = successStyle
		}
		parts = append(parts, style.Render(step))
	}
	return strings.Join(parts, dimStyle.Render(" -> "))
}

// renderDeviceDetail renders the device session detail screen.
//
// Returns:
//   - string: the rendered TUI content
func (m hubModel) renderDeviceDetail() string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render("Device detail")
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n\n")

	session := m.selectedDeviceSession()
	if session == nil {
		b.WriteString("  " + dimStyle.Render("Session not found") + "\n")
	} else {
		if m.deviceConfirmStop {
			b.WriteString("  " + warningStyle.Render("Stop this device session? (y/n)") + "\n\n")
		}

		b.WriteString("  " + sectionStyle.Render("Session ID") + "    " + normalStyle.Render(session.Id) + "\n")
		b.WriteString("  " + sectionStyle.Render("Platform") + "      " + platformStyle.Render(session.Platform) + "\n")
		b.WriteString("  " + sectionStyle.Render("Status") + "        " + deviceStatusBadge(session.Status) + "\n")

		if session.DeviceModel != nil && *session.DeviceModel != "" {
			b.WriteString("  " + sectionStyle.Render("Device") + "        " + normalStyle.Render(*session.DeviceModel) + "\n")
		}
		if session.OsVersion != nil && *session.OsVersion != "" {
			b.WriteString("  " + sectionStyle.Render("OS") + "            " + normalStyle.Render(*session.OsVersion) + "\n")
		}
		if session.StartedAt != nil {
			b.WriteString("  " + sectionStyle.Render("Uptime") + "        " + normalStyle.Render(formatDuration(time.Since(*session.StartedAt))) + "\n")
		}
		if session.UserEmail != nil && *session.UserEmail != "" {
			b.WriteString("  " + sectionStyle.Render("User") + "          " + dimStyle.Render(*session.UserEmail) + "\n")
		}
		if session.ScreenWidth != nil && session.ScreenHeight != nil {
			b.WriteString("  " + sectionStyle.Render("Screen") + "        " + dimStyle.Render(fmt.Sprintf("%dx%d", *session.ScreenWidth, *session.ScreenHeight)) + "\n")
		}

		viewerURL := m.selectedDeviceViewerURL()
		if viewerURL != "" {
			b.WriteString("\n  " + dimStyle.Render("Viewer URL available — press 'o' to open in browser") + "\n")
			b.WriteString("  " + dimStyle.Render("viewer: ") + linkStyle.Render(viewerURL) + "\n")
		}

		reportURL := m.selectedDeviceReportURL()
		if reportURL != "" {
			b.WriteString("  " + dimStyle.Render("Report URL available — press 'r' to open in browser") + "\n")
			b.WriteString("  " + dimStyle.Render("report: ") + linkStyle.Render(reportURL) + "\n")
		}
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	keys := []string{
		helpKeyRender("o", "open viewer"),
		helpKeyRender("r", "open report"),
		helpKeyRender("d", "stop"),
		helpKeyRender("esc", "back"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")
	return b.String()
}

// --- Helpers ---

// deviceStatusBadge returns a styled status string for a device session.
func deviceStatusBadge(status string) string {
	switch status {
	case "running":
		return runningStyle.Render(" running")
	case "starting":
		return dimStyle.Render(" starting")
	case "stopping", "cancelled":
		return dimStyle.Render(" " + status)
	default:
		return dimStyle.Render(" " + status)
	}
}

func isDeviceStatusTransitional(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "starting", "stopping", "queued", "provisioning":
		return true
	default:
		return false
	}
}

func deviceStartPlatforms() []string {
	return []string{"ios", "android"}
}

func (m hubModel) filteredDeviceStartApps() []api.App {
	query := strings.ToLower(strings.TrimSpace(m.deviceStartFilterInput.Value()))
	if query == "" {
		return m.deviceStartApps
	}

	filtered := make([]api.App, 0, len(m.deviceStartApps))
	for _, appInfo := range m.deviceStartApps {
		if strings.Contains(strings.ToLower(appInfo.Name), query) ||
			strings.Contains(strings.ToLower(appInfo.Platform), query) {
			filtered = append(filtered, appInfo)
		}
	}
	return filtered
}

func (m hubModel) selectedDeviceStartApp() *api.App {
	if m.deviceStartAppCursor <= 0 {
		return nil
	}
	filtered := m.filteredDeviceStartApps()
	appIdx := m.deviceStartAppCursor - 1
	if appIdx < 0 || appIdx >= len(filtered) {
		return nil
	}
	appInfo := filtered[appIdx]
	return &appInfo
}

func (m *hubModel) clampDeviceStartCursor() {
	maxIdx := len(m.filteredDeviceStartApps())
	if m.deviceStartAppCursor > maxIdx {
		m.deviceStartAppCursor = maxIdx
	}
	if m.deviceStartAppCursor < 0 {
		m.deviceStartAppCursor = 0
	}
}

// formatDuration formats a duration as a human-readable string (e.g. "3m 12s").
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}
