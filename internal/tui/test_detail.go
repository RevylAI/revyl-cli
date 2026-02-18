// Package tui provides the test detail screen for viewing and managing a single test.
//
// Reached from "Browse tests" → enter on a test. Shows sync status, tags, env vars,
// and provides actions for push/pull/diff, delete, run, open, tag management, and env var editing.
package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/sync"
	"github.com/revyl/cli/internal/ui"
)

// testDetailAction enumerates the available actions in the test detail screen.
type testDetailAction struct {
	Key  string // shortcut key
	Desc string // display label
}

var testDetailActions = []testDetailAction{
	{Key: "r", Desc: "Run this test"},
	{Key: "o", Desc: "Open in browser"},
	{Key: "p", Desc: "Push local → remote"},
	{Key: "l", Desc: "Pull remote → local"},
	{Key: "d", Desc: "View diff"},
	{Key: "t", Desc: "Manage tags"},
	{Key: "e", Desc: "Manage env vars"},
	{Key: "x", Desc: "Delete test"},
}

// --- Commands ---

// fetchTestDetailCmd fetches all data needed for the test detail screen.
//
// Parameters:
//   - client: the API client
//   - testID: the test to fetch
//   - testName: the test name (for display)
//   - platform: the test platform
//   - devMode: whether in dev mode
//
// Returns:
//   - tea.Cmd: command producing TestDetailMsg
func fetchTestDetailCmd(client *api.Client, testID, testName, platform string, devMode bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		detail := &TestDetail{
			ID:         testID,
			Name:       testName,
			Platform:   platform,
			SyncStatus: "unknown",
		}

		// Fetch last run from history
		histResp, err := client.GetTestEnhancedHistory(ctx, testID, 1, 0)
		if err == nil && histResp != nil && len(histResp.Items) > 0 {
			last := histResp.Items[0]
			detail.LastRunStatus = last.Status
			detail.LastRunTaskID = last.ID
			if last.Duration != nil {
				detail.LastRunDuration = fmt.Sprintf("%.0fs", *last.Duration)
			}
			if last.ExecutionTime != "" {
				if t, tErr := time.Parse(time.RFC3339, last.ExecutionTime); tErr == nil {
					detail.LastRunTime = t
				}
			}
		}

		// Fetch tags
		tags, tErr := client.GetTestTags(ctx, testID)
		if tErr == nil {
			for _, t := range tags {
				detail.Tags = append(detail.Tags, TagItem{
					ID:    t.ID,
					Name:  t.Name,
					Color: t.Color,
				})
			}
		}

		// Fetch env var count
		envResp, eErr := client.ListEnvVars(ctx, testID)
		if eErr == nil && envResp != nil {
			detail.EnvVarCount = len(envResp.Result)
		}

		// Fetch sync status if project config exists
		cwd, cwdErr := os.Getwd()
		if cwdErr == nil {
			configPath := filepath.Join(cwd, ".revyl", "config.yaml")
			cfg, cfgErr := config.LoadProjectConfig(configPath)
			if cfgErr == nil {
				testsDir := filepath.Join(cwd, ".revyl", "tests")
				localTests, ltErr := config.LoadLocalTests(testsDir)
				if ltErr == nil {
					resolver := sync.NewResolver(client, cfg, localTests)
					statuses, sErr := resolver.GetAllStatuses(ctx)
					if sErr == nil {
						for _, s := range statuses {
							if s.RemoteID == testID || s.Name == testName {
								detail.SyncStatus = s.Status.String()
								detail.SyncVersion = fmt.Sprintf("v%d", s.RemoteVersion)
								break
							}
						}
					}
				}
			}
		}

		return TestDetailMsg{Detail: detail}
	}
}

// syncTestActionCmd performs a push, pull, or diff for a test.
//
// Parameters:
//   - client: the API client
//   - action: "push", "pull", or "diff"
//   - testName: the test name
//   - devMode: dev mode flag
//
// Returns:
//   - tea.Cmd: command producing TestSyncActionMsg
func syncTestActionCmd(client *api.Client, action, testName, testID string, devMode bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		cwd, err := os.Getwd()
		if err != nil {
			return TestSyncActionMsg{Action: action, Err: fmt.Errorf("failed to get working directory: %w", err)}
		}

		configPath := filepath.Join(cwd, ".revyl", "config.yaml")
		cfg, err := config.LoadProjectConfig(configPath)
		if err != nil {
			return TestSyncActionMsg{Action: action, Err: fmt.Errorf("no project config: %w", err)}
		}

		testsDir := filepath.Join(cwd, ".revyl", "tests")
		localTests, err := config.LoadLocalTests(testsDir)
		if err != nil {
			return TestSyncActionMsg{Action: action, Err: fmt.Errorf("failed to load local tests: %w", err)}
		}

		resolver := sync.NewResolver(client, cfg, localTests)

		// Resolve a stable local key for resolver operations.
		targetName := testName
		if testID != "" {
			for alias, id := range cfg.Tests {
				if id == testID {
					targetName = alias
					break
				}
			}
			if targetName == testName {
				for localName, lt := range localTests {
					if lt != nil && lt.Meta.RemoteID == testID {
						targetName = localName
						break
					}
				}
			}
		}

		switch action {
		case "push":
			results, sErr := resolver.SyncToRemote(ctx, targetName, testsDir, false)
			if sErr != nil {
				return TestSyncActionMsg{Action: action, Err: sErr}
			}
			if len(results) == 0 {
				return TestSyncActionMsg{Action: action, Result: "No changes to push"}
			}
			r := results[0]
			if r.Error != nil {
				return TestSyncActionMsg{Action: action, Err: r.Error}
			}
			return TestSyncActionMsg{Action: action, Result: fmt.Sprintf("Pushed %s → v%d", r.Name, r.NewVersion)}

		case "pull":
			results, sErr := resolver.PullFromRemote(ctx, targetName, testsDir, false)
			if sErr != nil {
				return TestSyncActionMsg{Action: action, Err: sErr}
			}
			if len(results) == 0 {
				return TestSyncActionMsg{Action: action, Result: "No changes to pull"}
			}
			r := results[0]
			if r.Error != nil {
				return TestSyncActionMsg{Action: action, Err: r.Error}
			}
			return TestSyncActionMsg{Action: action, Result: fmt.Sprintf("Pulled %s → v%d", r.Name, r.NewVersion)}

		case "diff":
			diff, dErr := resolver.GetDiff(ctx, targetName)
			if dErr != nil {
				return TestSyncActionMsg{Action: action, Err: dErr}
			}
			if diff == "" {
				return TestSyncActionMsg{Action: action, Result: "No differences found"}
			}
			return TestSyncActionMsg{Action: action, Result: diff}
		}

		return TestSyncActionMsg{Action: action, Err: fmt.Errorf("unknown sync action: %s", action)}
	}
}

// deleteTestCmd deletes a test by ID.
//
// Parameters:
//   - client: the API client
//   - testID: the test to delete
//
// Returns:
//   - tea.Cmd: command producing TestDeletedMsg
func deleteTestCmd(client *api.Client, testID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := client.DeleteTest(ctx, testID)
		return TestDeletedMsg{Err: err}
	}
}

// fetchEnvVarsCmd fetches environment variables for a test.
//
// Parameters:
//   - client: the API client
//   - testID: the test ID
//
// Returns:
//   - tea.Cmd: command producing EnvVarListMsg
func fetchEnvVarsCmd(client *api.Client, testID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := client.ListEnvVars(ctx, testID)
		if err != nil {
			return EnvVarListMsg{Err: err}
		}
		var vars []EnvVarItem
		for _, v := range resp.Result {
			vars = append(vars, EnvVarItem{ID: v.ID, Key: v.Key, Value: v.Value})
		}
		return EnvVarListMsg{Vars: vars}
	}
}

// addEnvVarCmd adds a new environment variable to a test.
//
// Parameters:
//   - client: the API client
//   - testID: the test ID
//   - key: the env var key
//   - value: the env var value
//
// Returns:
//   - tea.Cmd: command producing EnvVarAddedMsg
func addEnvVarCmd(client *api.Client, testID, key, value string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := client.AddEnvVar(ctx, testID, key, value)
		return EnvVarAddedMsg{Err: err}
	}
}

// deleteEnvVarCmd deletes an environment variable.
//
// Parameters:
//   - client: the API client
//   - envVarID: the env var ID
//
// Returns:
//   - tea.Cmd: command producing EnvVarDeletedMsg
func deleteEnvVarCmd(client *api.Client, envVarID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := client.DeleteEnvVar(ctx, envVarID)
		return EnvVarDeletedMsg{Err: err}
	}
}

// --- Key handling ---

// handleTestDetailKey processes key events on the test detail screen.
//
// Parameters:
//   - m: the hub model
//   - msg: the key message
//
// Returns:
//   - tea.Model: the updated model
//   - tea.Cmd: next command
func handleTestDetailKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Env var editor overlay
	if m.envVarEditorActive {
		return handleEnvVarEditorKey(m, msg)
	}

	// Tag picker overlay
	if m.tagPickerActive {
		return handleTagPickerKeyFromDetail(m, msg)
	}

	// Delete confirmation
	if m.testDetailConfirmDelete {
		switch msg.String() {
		case "y":
			m.testDetailConfirmDelete = false
			if m.client != nil && m.selectedTestDetail != nil {
				return m, deleteTestCmd(m.client, m.selectedTestDetail.ID)
			}
			return m, nil
		case "n", "esc":
			m.testDetailConfirmDelete = false
			return m, nil
		}
		return m, nil
	}

	// Sync result display -- any key dismisses, but quit keys still work
	if m.testSyncResult != "" {
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		default:
			m.testSyncResult = ""
			return m, nil
		}
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.currentView = viewTestList
		m.selectedTestDetail = nil
		return m, nil
	case "r":
		if m.selectedTestDetail != nil {
			return startTestExecution(m, m.selectedTestDetail.ID, m.selectedTestDetail.Name)
		}
	case "o":
		if m.selectedTestDetail != nil {
			testURL := config.GetAppURL(m.devMode) + "/tests/" + m.selectedTestDetail.ID
			_ = ui.OpenBrowser(testURL)
		}
	case "p":
		if m.client != nil && m.selectedTestDetail != nil {
			m.testDetailLoading = true
			return m, syncTestActionCmd(m.client, "push", m.selectedTestDetail.Name, m.selectedTestDetail.ID, m.devMode)
		}
	case "l":
		if m.client != nil && m.selectedTestDetail != nil {
			m.testDetailLoading = true
			return m, syncTestActionCmd(m.client, "pull", m.selectedTestDetail.Name, m.selectedTestDetail.ID, m.devMode)
		}
	case "d":
		if m.client != nil && m.selectedTestDetail != nil {
			m.testDetailLoading = true
			return m, syncTestActionCmd(m.client, "diff", m.selectedTestDetail.Name, m.selectedTestDetail.ID, m.devMode)
		}
	case "t":
		if m.client != nil && m.selectedTestDetail != nil {
			m.tagPickerActive = true
			m.tagPickerLoading = true
			return m, fetchTagPickerDataCmd(m.client, m.selectedTestDetail.ID)
		}
	case "e":
		if m.client != nil && m.selectedTestDetail != nil {
			m.envVarEditorActive = true
			m.envVarLoading = true
			return m, fetchEnvVarsCmd(m.client, m.selectedTestDetail.ID)
		}
	case "x":
		m.testDetailConfirmDelete = true
		return m, nil
	}

	return m, nil
}

// handleEnvVarEditorKey processes key events in the env var editor overlay.
func handleEnvVarEditorKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Adding a new env var -- input mode
	if m.envVarAddingKey {
		switch msg.String() {
		case "esc":
			m.envVarAddingKey = false
			m.envVarKeyInput.Blur()
			return m, nil
		case "enter":
			m.envVarAddingKey = false
			m.envVarAddingValue = true
			m.envVarKeyInput.Blur()
			m.envVarValueInput.Focus()
			return m, textinput.Blink
		default:
			var cmd tea.Cmd
			m.envVarKeyInput, cmd = m.envVarKeyInput.Update(msg)
			return m, cmd
		}
	}
	if m.envVarAddingValue {
		switch msg.String() {
		case "esc":
			m.envVarAddingValue = false
			m.envVarValueInput.Blur()
			return m, nil
		case "enter":
			key := m.envVarKeyInput.Value()
			value := m.envVarValueInput.Value()
			m.envVarAddingValue = false
			m.envVarValueInput.Blur()
			m.envVarKeyInput.SetValue("")
			m.envVarValueInput.SetValue("")
			if key != "" && m.client != nil && m.selectedTestDetail != nil {
				m.envVarLoading = true
				return m, addEnvVarCmd(m.client, m.selectedTestDetail.ID, key, value)
			}
			return m, nil
		default:
			var cmd tea.Cmd
			m.envVarValueInput, cmd = m.envVarValueInput.Update(msg)
			return m, cmd
		}
	}

	// Delete confirmation
	if m.envVarConfirmDelete {
		switch msg.String() {
		case "y":
			m.envVarConfirmDelete = false
			if m.envVarCursor < len(m.envVars) && m.client != nil {
				m.envVarLoading = true
				return m, deleteEnvVarCmd(m.client, m.envVars[m.envVarCursor].ID)
			}
		case "n", "esc":
			m.envVarConfirmDelete = false
		}
		return m, nil
	}

	switch msg.String() {
	case "esc":
		m.envVarEditorActive = false
		return m, nil
	case "up", "k":
		if m.envVarCursor > 0 {
			m.envVarCursor--
		}
	case "down", "j":
		if m.envVarCursor < len(m.envVars)-1 {
			m.envVarCursor++
		}
	case "n":
		m.envVarAddingKey = true
		m.envVarKeyInput.Focus()
		return m, textinput.Blink
	case "d":
		if len(m.envVars) > 0 {
			m.envVarConfirmDelete = true
		}
	case "v":
		m.envVarShowValues = !m.envVarShowValues
	}
	return m, nil
}

// --- Rendering ---

// renderTestDetail renders the test detail screen.
//
// Parameters:
//   - m: the hub model
//
// Returns:
//   - string: rendered output
func renderTestDetail(m hubModel) string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	detail := m.selectedTestDetail
	if detail == nil {
		b.WriteString("  " + m.spinner.View() + " Loading test detail...\n")
		return b.String()
	}

	// Header
	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render(detail.Name)
	idPrefix := detail.ID
	if len(idPrefix) > 8 {
		idPrefix = idPrefix[:8]
	}
	idBadge := dimStyle.Render(detail.Platform + "  " + idPrefix)
	headerLine := bannerContent + strings.Repeat(" ", max(1, innerW-lipgloss.Width(bannerContent)-lipgloss.Width(idBadge)+4)) + idBadge
	banner := headerBannerStyle.Width(innerW).Render(headerLine)
	b.WriteString(banner + "\n")

	// Status section
	b.WriteString(sectionStyle.Render("  STATUS") + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	// Last run
	if detail.LastRunStatus != "" {
		icon := statusIcon(detail.LastRunStatus)
		timeAgo := relativeTime(detail.LastRunTime)
		dur := ""
		if detail.LastRunDuration != "" {
			dur = dimStyle.Render("  (" + detail.LastRunDuration + ")")
		}
		b.WriteString(fmt.Sprintf("  %-12s %s %s  %s%s\n",
			dimStyle.Render("Last Run"),
			icon,
			statusStyle(detail.LastRunStatus).Render(detail.LastRunStatus),
			dimStyle.Render(timeAgo),
			dur))
	} else {
		b.WriteString(fmt.Sprintf("  %-12s %s\n", dimStyle.Render("Last Run"), dimStyle.Render("no runs yet")))
	}

	// Sync
	syncIcon := syncStatusIcon(detail.SyncStatus)
	syncColor := syncStatusStyle(detail.SyncStatus)
	syncVer := ""
	if detail.SyncVersion != "" {
		syncVer = dimStyle.Render("  (" + detail.SyncVersion + ")")
	}
	b.WriteString(fmt.Sprintf("  %-12s %s %s%s\n",
		dimStyle.Render("Sync"),
		syncIcon,
		syncColor.Render(detail.SyncStatus),
		syncVer))

	// Tags
	tagStr := dimStyle.Render("none")
	if len(detail.Tags) > 0 {
		names := make([]string, len(detail.Tags))
		for i, t := range detail.Tags {
			if t.Color != "" {
				names[i] = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Color)).Render(t.Name)
			} else {
				names[i] = normalStyle.Render(t.Name)
			}
		}
		tagStr = strings.Join(names, ", ")
	}
	b.WriteString(fmt.Sprintf("  %-12s %s\n", dimStyle.Render("Tags"), tagStr))

	// Env vars
	envStr := dimStyle.Render("none")
	if detail.EnvVarCount > 0 {
		envStr = normalStyle.Render(fmt.Sprintf("%d configured", detail.EnvVarCount))
	}
	b.WriteString(fmt.Sprintf("  %-12s %s\n", dimStyle.Render("Env Vars"), envStr))

	// Sync result display
	if m.testSyncResult != "" {
		b.WriteString("\n")
		b.WriteString(sectionStyle.Render("  RESULT") + "\n")
		b.WriteString("  " + separator(innerW) + "\n")
		for _, line := range strings.Split(m.testSyncResult, "\n") {
			b.WriteString("  " + normalStyle.Render(line) + "\n")
		}
		b.WriteString("\n  " + dimStyle.Render("press any key to dismiss") + "\n")
		return b.String()
	}

	// Loading indicator
	if m.testDetailLoading {
		b.WriteString("\n  " + m.spinner.View() + " Working...\n")
		return b.String()
	}

	// Delete confirmation
	if m.testDetailConfirmDelete {
		b.WriteString("\n  " + errorStyle.Render("Delete test \""+detail.Name+"\"? (y/n)") + "\n")
		return b.String()
	}

	// Env var editor overlay
	if m.envVarEditorActive {
		b.WriteString("\n")
		b.WriteString(renderEnvVarEditor(m, innerW))
		return b.String()
	}

	// Tag picker overlay
	if m.tagPickerActive {
		b.WriteString("\n")
		b.WriteString(renderTagPickerOverlay(m, innerW))
		return b.String()
	}

	// Actions section
	b.WriteString("\n")
	b.WriteString(sectionStyle.Render("  ACTIONS") + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	for i, a := range testDetailActions {
		cursor := "  "
		if i == m.testDetailCursor {
			cursor = selectedStyle.Render("▸ ")
		}
		key := lipgloss.NewStyle().Foreground(purple).Bold(true).Render("[" + a.Key + "]")
		b.WriteString(fmt.Sprintf("  %s%s %s\n", cursor, key, dimStyle.Render(a.Desc)))
	}

	// Footer
	b.WriteString("\n  " + separator(innerW) + "\n")
	keys := []string{
		helpKeyRender("esc", "back"),
		helpKeyRender("r", "run"),
		helpKeyRender("o", "open"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")

	return b.String()
}

// renderEnvVarEditor renders the env var editor overlay.
func renderEnvVarEditor(m hubModel, innerW int) string {
	var b strings.Builder

	b.WriteString(sectionStyle.Render("  ENV VARS") + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	if m.envVarLoading {
		b.WriteString("  " + m.spinner.View() + " Loading...\n")
		return b.String()
	}

	if m.envVarConfirmDelete && m.envVarCursor < len(m.envVars) {
		b.WriteString("  " + errorStyle.Render(fmt.Sprintf("Delete %s? (y/n)", m.envVars[m.envVarCursor].Key)) + "\n")
		return b.String()
	}

	if m.envVarAddingKey {
		b.WriteString("  Key: " + m.envVarKeyInput.View() + "\n")
		return b.String()
	}
	if m.envVarAddingValue {
		b.WriteString("  Key: " + normalStyle.Render(m.envVarKeyInput.Value()) + "\n")
		b.WriteString("  Value: " + m.envVarValueInput.View() + "\n")
		return b.String()
	}

	if len(m.envVars) == 0 {
		b.WriteString("  " + dimStyle.Render("No environment variables configured") + "\n")
	} else {
		for i, v := range m.envVars {
			cursor := "  "
			if i == m.envVarCursor {
				cursor = selectedStyle.Render("▸ ")
			}
			val := strings.Repeat("•", min(len(v.Value), 20))
			if m.envVarShowValues {
				val = v.Value
			}
			b.WriteString(fmt.Sprintf("  %s%-20s = %s\n", cursor, normalStyle.Render(v.Key), dimStyle.Render(val)))
		}
	}

	b.WriteString("\n  ")
	keys := []string{
		helpKeyRender("n", "add"),
		helpKeyRender("d", "delete"),
		helpKeyRender("v", "toggle values"),
		helpKeyRender("esc", "close"),
	}
	b.WriteString(strings.Join(keys, "  ") + "\n")
	return b.String()
}

// --- Helpers ---

// syncStatusIcon returns a colored icon for sync status.
func syncStatusIcon(status string) string {
	switch status {
	case "synced":
		return successStyle.Render("✓")
	case "modified", "outdated":
		return warningStyle.Render("~")
	case "conflict":
		return errorStyle.Render("✗")
	case "local-only":
		return warningStyle.Render("L")
	case "remote-only":
		return runningStyle.Render("R")
	case "stale":
		return warningStyle.Render("!")
	default:
		return dimStyle.Render("?")
	}
}

// syncStatusStyle returns the style for a sync status string.
func syncStatusStyle(status string) lipgloss.Style {
	switch status {
	case "synced":
		return successStyle
	case "modified", "outdated":
		return warningStyle
	case "conflict":
		return errorStyle
	case "local-only", "stale":
		return warningStyle
	case "remote-only":
		return runningStyle
	default:
		return dimStyle
	}
}

// startTestExecution transitions the hub model to the execution screen for a test.
// This is a helper that avoids duplicating the pattern across views.
func startTestExecution(m hubModel, testID, testName string) (tea.Model, tea.Cmd) {
	em := newExecutionModel(testID, testName, m.apiKey, m.cfg, m.devMode, m.width, m.height)
	m.executionModel = &em
	m.currentView = viewExecution
	return m, m.executionModel.Init()
}
