package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

// --- Tea commands ---

// fetchDeviceSessionsCmd fetches active device sessions for the org.
//
// Parameters:
//   - client: authenticated API client
//
// Returns:
//   - tea.Cmd: async command that sends DeviceSessionListMsg on completion
func fetchDeviceSessionsCmd(client *api.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		// Validate to get the org ID
		info, err := client.ValidateAPIKey(ctx)
		if err != nil {
			return DeviceSessionListMsg{Err: fmt.Errorf("failed to authenticate: %w", err)}
		}

		sessions, err := client.GetActiveDeviceSessions(ctx, info.OrgID)
		if err != nil {
			return DeviceSessionListMsg{Err: err}
		}

		return DeviceSessionListMsg{Sessions: sessions.Sessions}
	}
}

// startDeviceSessionCmd starts a new device session with the given platform.
//
// Parameters:
//   - client: authenticated API client
//   - platform: "android" or "ios"
//
// Returns:
//   - tea.Cmd: async command that sends DeviceStartedMsg on completion
func startDeviceSessionCmd(client *api.Client, platform string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
		defer cancel()

		resp, err := client.StartDevice(ctx, &api.StartDeviceRequest{
			Platform:     platform,
			IsSimulation: true,
		})
		if err != nil {
			return DeviceStartedMsg{Err: fmt.Errorf("failed to start device: %w", err)}
		}

		if resp.WorkflowRunId == nil || *resp.WorkflowRunId == "" {
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
			WorkflowRunID: *resp.WorkflowRunId,
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
		}
	case "n":
		// Start new device - show platform picker
		m.deviceStartPicking = true
		m.devicePlatformCursor = 0
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
			return m, fetchDeviceSessionsCmd(m.client)
		}
	case "esc":
		m.currentView = viewDashboard
		return m, nil
	case "q":
		return m, tea.Quit
	}

	// Platform picker overlay
	if m.deviceStartPicking {
		switch msg.String() {
		case "1":
			m.deviceStartPicking = false
			m.deviceStarting = true
			return m, startDeviceSessionCmd(m.client, "android")
		case "2":
			m.deviceStartPicking = false
			m.deviceStarting = true
			return m, startDeviceSessionCmd(m.client, "ios")
		case "esc":
			m.deviceStartPicking = false
		}
	}

	return m, nil
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
		// Open viewer URL in browser
		session := m.selectedDeviceSession()
		if session != nil && session.WhepUrl != nil && *session.WhepUrl != "" {
			_ = ui.OpenBrowser(*session.WhepUrl)
		}
	case "d":
		session := m.selectedDeviceSession()
		if session != nil && session.WorkflowRunId != nil && *session.WorkflowRunId != "" {
			m.deviceConfirmStop = true
			m.deviceStopTarget = *session.WorkflowRunId
		}
	case "esc":
		m.currentView = viewDeviceList
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
		b.WriteString("\n  " + sectionStyle.Render("Select platform:") + "\n")
		b.WriteString("    " + helpKeyRender("1", "Android") + "    " + helpKeyRender("2", "iOS") + "    " + helpKeyRender("esc", "cancel") + "\n\n")
	}

	if m.deviceStarting {
		b.WriteString("  " + m.spinner.View() + " Starting device...\n")
	} else if m.devicesLoading {
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
				if t, err := time.Parse(time.RFC3339, *s.StartedAt); err == nil {
					uptime = dimStyle.Render("  " + formatDuration(time.Since(t)))
				}
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
			if t, err := time.Parse(time.RFC3339, *session.StartedAt); err == nil {
				b.WriteString("  " + sectionStyle.Render("Uptime") + "        " + normalStyle.Render(formatDuration(time.Since(t))) + "\n")
			}
		}
		if session.UserEmail != nil && *session.UserEmail != "" {
			b.WriteString("  " + sectionStyle.Render("User") + "          " + dimStyle.Render(*session.UserEmail) + "\n")
		}
		if session.ScreenWidth != nil && session.ScreenHeight != nil {
			b.WriteString("  " + sectionStyle.Render("Screen") + "        " + dimStyle.Render(fmt.Sprintf("%dx%d", *session.ScreenWidth, *session.ScreenHeight)) + "\n")
		}

		hasViewer := session.WhepUrl != nil && *session.WhepUrl != ""
		if hasViewer {
			b.WriteString("\n  " + dimStyle.Render("Viewer URL available — press 'o' to open in browser") + "\n")
		}
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	keys := []string{
		helpKeyRender("o", "open viewer"),
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
