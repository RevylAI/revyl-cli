// Package tui provides the tag browser and tag picker overlay for test management.
//
// The tag list shows all org tags with color dots and test counts.
// The tag picker is an overlay used from the test detail screen to attach/detach tags.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/revyl/cli/internal/api"
)

// --- Commands ---

// fetchTagsCmd fetches the full tag list for the org.
//
// Parameters:
//   - client: the API client
//
// Returns:
//   - tea.Cmd: command producing TagListMsg
func fetchTagsCmd(client *api.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := client.ListTags(ctx)
		if err != nil {
			return TagListMsg{Err: err}
		}
		var tags []TagItem
		for _, t := range resp.Tags {
			tags = append(tags, TagItem{
				ID:          t.ID,
				Name:        t.Name,
				Color:       t.Color,
				Description: t.Description,
				TestCount:   t.TestCount,
			})
		}
		return TagListMsg{Tags: tags}
	}
}

// createTagCmd creates a new tag.
//
// Parameters:
//   - client: the API client
//   - name: the tag name
//   - color: optional hex color
//
// Returns:
//   - tea.Cmd: command producing TagCreatedMsg
func createTagCmd(client *api.Client, name, color string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := client.CreateTag(ctx, &api.CLICreateTagRequest{
			Name:  name,
			Color: color,
		})
		if err != nil {
			return TagCreatedMsg{Err: err}
		}
		return TagCreatedMsg{ID: resp.ID, Name: resp.Name}
	}
}

// deleteTagCmd deletes a tag by ID.
//
// Parameters:
//   - client: the API client
//   - tagID: the tag ID to delete
//
// Returns:
//   - tea.Cmd: command producing TagDeletedMsg
func deleteTagCmd(client *api.Client, tagID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := client.DeleteTag(ctx, tagID)
		return TagDeletedMsg{Err: err}
	}
}

// fetchTagPickerDataCmd fetches both the full tag list and the current test's tags
// for the tag picker overlay.
//
// Parameters:
//   - client: the API client
//   - testID: the test whose tags to fetch
//
// Returns:
//   - tea.Cmd: command producing tagPickerDataMsg
func fetchTagPickerDataCmd(client *api.Client, testID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Fetch all org tags
		allResp, err := client.ListTags(ctx)
		if err != nil {
			return tagPickerDataMsg{Err: err}
		}

		// Fetch test's current tags
		testTags, tErr := client.GetTestTags(ctx, testID)
		if tErr != nil {
			return tagPickerDataMsg{Err: tErr}
		}

		// Build set of currently attached tag names
		attached := make(map[string]bool)
		for _, t := range testTags {
			attached[t.Name] = true
		}

		var items []tagPickerItem
		for _, t := range allResp.Tags {
			items = append(items, tagPickerItem{
				Name:     t.Name,
				Color:    t.Color,
				Selected: attached[t.Name],
			})
		}

		return tagPickerDataMsg{Items: items}
	}
}

// syncTestTagsCmd syncs the selected tags to a test.
//
// Parameters:
//   - client: the API client
//   - testID: the test ID
//   - tagNames: the selected tag names
//
// Returns:
//   - tea.Cmd: command producing TagsSyncedMsg
func syncTestTagsCmd(client *api.Client, testID string, tagNames []string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := client.SyncTestTags(ctx, testID, &api.CLISyncTagsRequest{
			TagNames: tagNames,
		})
		return TagsSyncedMsg{Err: err}
	}
}

// --- Internal message types (not exported) ---

// tagPickerDataMsg carries both all tags and test tag state for the picker.
type tagPickerDataMsg struct {
	Items []tagPickerItem
	Err   error
}

// tagPickerItem represents a tag in the picker with selection state.
type tagPickerItem struct {
	Name     string
	Color    string
	Selected bool
}

// --- Key handling ---

// handleTagListKey processes key events on the tag browser screen.
//
// Parameters:
//   - m: the hub model
//   - msg: the key message
//
// Returns:
//   - tea.Model: updated model
//   - tea.Cmd: next command
func handleTagListKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Create tag input mode
	if m.tagCreateActive {
		return handleTagCreateKey(m, msg)
	}

	// Delete confirmation
	if m.tagConfirmDelete {
		switch msg.String() {
		case "y":
			m.tagConfirmDelete = false
			if m.tagCursor < len(m.tagItems) && m.client != nil {
				m.tagLoading = true
				return m, deleteTagCmd(m.client, m.tagItems[m.tagCursor].ID)
			}
		case "n", "esc":
			m.tagConfirmDelete = false
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.currentView = viewDashboard
		return m, nil
	case "up", "k":
		if m.tagCursor > 0 {
			m.tagCursor--
		}
	case "down", "j":
		if m.tagCursor < len(m.tagItems)-1 {
			m.tagCursor++
		}
	case "n":
		m.tagCreateActive = true
		m.tagNameInput.SetValue("")
		m.tagNameInput.Focus()
		return m, textinput.Blink
	case "d":
		if len(m.tagItems) > 0 {
			m.tagConfirmDelete = true
		}
	}
	return m, nil
}

// handleTagCreateKey processes key events during tag creation.
func handleTagCreateKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.tagCreateActive = false
		m.tagNameInput.Blur()
		return m, nil
	case "enter":
		name := m.tagNameInput.Value()
		m.tagCreateActive = false
		m.tagNameInput.Blur()
		if name != "" && m.client != nil {
			m.tagLoading = true
			return m, createTagCmd(m.client, name, "")
		}
		return m, nil
	default:
		var cmd tea.Cmd
		m.tagNameInput, cmd = m.tagNameInput.Update(msg)
		return m, cmd
	}
}

// handleTagPickerKeyFromDetail processes key events in the tag picker overlay
// (used from the test detail screen).
//
// Parameters:
//   - m: the hub model
//   - msg: the key message
//
// Returns:
//   - tea.Model: updated model
//   - tea.Cmd: next command
func handleTagPickerKeyFromDetail(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.tagPickerActive = false
		m.tagPickerItems = nil
		return m, nil
	case "up", "k":
		if m.tagPickerCursor > 0 {
			m.tagPickerCursor--
		}
	case "down", "j":
		if m.tagPickerCursor < len(m.tagPickerItems)-1 {
			m.tagPickerCursor++
		}
	case " ":
		if m.tagPickerCursor < len(m.tagPickerItems) {
			m.tagPickerItems[m.tagPickerCursor].Selected = !m.tagPickerItems[m.tagPickerCursor].Selected
		}
	case "enter":
		if m.client != nil && m.selectedTestDetail != nil {
			var selected []string
			for _, item := range m.tagPickerItems {
				if item.Selected {
					selected = append(selected, item.Name)
				}
			}
			m.tagPickerActive = false
			m.tagPickerLoading = true
			return m, syncTestTagsCmd(m.client, m.selectedTestDetail.ID, selected)
		}
	}
	return m, nil
}

// --- Rendering ---

// renderTagList renders the tag browser screen.
//
// Parameters:
//   - m: the hub model
//
// Returns:
//   - string: rendered output
func renderTagList(m hubModel) string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render("Tags")
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	b.WriteString(sectionStyle.Render(fmt.Sprintf("  Tags  %d", len(m.tagItems))) + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	if m.tagLoading {
		b.WriteString("  " + m.spinner.View() + " Loading...\n")
		return b.String()
	}

	if m.tagConfirmDelete && m.tagCursor < len(m.tagItems) {
		b.WriteString("  " + errorStyle.Render(fmt.Sprintf("Delete tag \"%s\"? (y/n)", m.tagItems[m.tagCursor].Name)) + "\n")
		return b.String()
	}

	if m.tagCreateActive {
		b.WriteString("  Tag name: " + m.tagNameInput.View() + "\n")
		return b.String()
	}

	if len(m.tagItems) == 0 {
		b.WriteString("  " + dimStyle.Render("No tags found") + "\n")
	} else {
		start, end := scrollWindow(m.tagCursor, len(m.tagItems), 15)
		for i := start; i < end; i++ {
			tag := m.tagItems[i]
			cursor := "  "
			if i == m.tagCursor {
				cursor = selectedStyle.Render("▸ ")
			}
			// Color dot
			dot := dimStyle.Render("●")
			if tag.Color != "" {
				dot = lipgloss.NewStyle().Foreground(lipgloss.Color(tag.Color)).Render("●")
			}
			name := normalStyle.Render(fmt.Sprintf("%-18s", tag.Name))
			count := dimStyle.Render(fmt.Sprintf("%d tests", tag.TestCount))
			desc := ""
			if tag.Description != "" {
				desc = "  " + dimStyle.Render(truncate(tag.Description, 24))
			}
			b.WriteString(fmt.Sprintf("  %s%s %s  %s%s\n", cursor, dot, name, count, desc))
		}
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	keys := []string{
		helpKeyRender("n", "create"),
		helpKeyRender("d", "delete"),
		helpKeyRender("esc", "back"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")

	return b.String()
}

// renderTagPickerOverlay renders the tag picker for the test detail screen.
//
// Parameters:
//   - m: the hub model
//   - innerW: the inner width for layout
//
// Returns:
//   - string: rendered overlay
func renderTagPickerOverlay(m hubModel, innerW int) string {
	var b strings.Builder

	detailName := ""
	if m.selectedTestDetail != nil {
		detailName = m.selectedTestDetail.Name
	}
	b.WriteString(sectionStyle.Render(fmt.Sprintf("  Manage tags for: %s", detailName)) + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	if m.tagPickerLoading {
		b.WriteString("  " + m.spinner.View() + " Loading...\n")
		return b.String()
	}

	if len(m.tagPickerItems) == 0 {
		b.WriteString("  " + dimStyle.Render("No tags available. Create tags first.") + "\n")
	} else {
		for i, item := range m.tagPickerItems {
			cursor := "  "
			if i == m.tagPickerCursor {
				cursor = selectedStyle.Render("▸ ")
			}
			check := "[ ]"
			if item.Selected {
				check = successStyle.Render("[✓]")
			}
			name := normalStyle.Render(item.Name)
			b.WriteString(fmt.Sprintf("  %s%s %s\n", cursor, check, name))
		}
	}

	b.WriteString("\n  ")
	keys := []string{
		helpKeyRender("space", "toggle"),
		helpKeyRender("enter", "confirm"),
		helpKeyRender("esc", "cancel"),
	}
	b.WriteString(strings.Join(keys, "  ") + "\n")
	return b.String()
}

// truncate shortens a string to maxLen, adding "…" if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}
