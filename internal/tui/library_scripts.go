// Package tui — Scripts tab of the Library hub.
//
// Scripts are browsed/inspected/deleted here; name and description can be
// edited inline. Script code edits stay in the `revyl script` CLI.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/api"
)

// --- Commands ---

// fetchScriptsCmd loads the org's scripts.
func fetchScriptsCmd(client *api.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := client.ListScripts(ctx, "", 100, 0)
		if err != nil {
			return ScriptListMsg{Err: err}
		}
		items := make([]ScriptItem, 0, len(resp.Scripts))
		for _, s := range resp.Scripts {
			desc := ""
			if s.Description != nil {
				desc = *s.Description
			}
			items = append(items, ScriptItem{
				ID:          s.ID,
				Name:        s.Name,
				Runtime:     s.Runtime,
				Description: desc,
				Code:        s.Code,
			})
		}
		return ScriptListMsg{Scripts: items}
	}
}

// fetchScriptDetailCmd loads a single script's full record.
func fetchScriptDetailCmd(client *api.Client, scriptID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s, err := client.GetScript(ctx, scriptID)
		if err != nil {
			return ScriptDetailMsg{Err: err}
		}
		desc := ""
		if s.Description != nil {
			desc = *s.Description
		}
		return ScriptDetailMsg{Script: &ScriptItem{
			ID:          s.ID,
			Name:        s.Name,
			Runtime:     s.Runtime,
			Description: desc,
			Code:        s.Code,
		}}
	}
}

// updateScriptMetaCmd updates a script's name and description only.
func updateScriptMetaCmd(client *api.Client, scriptID, name, description string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		desc := description
		req := &api.CLIUpdateScriptRequest{
			Name:        &name,
			Description: &desc,
		}
		_, err := client.UpdateScript(ctx, scriptID, req)
		return ScriptUpdatedMsg{Err: err}
	}
}

// deleteScriptCmd deletes a script by ID.
func deleteScriptCmd(client *api.Client, scriptID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := client.DeleteScript(ctx, scriptID)
		return ScriptDeletedMsg{Err: err}
	}
}

// --- Key handling ---

// handleLibraryScriptsKey handles key events for the Scripts tab.
func handleLibraryScriptsKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.libraryMode == libModeConfirmDelete {
		switch msg.String() {
		case "y":
			m.libraryMode = libModeList
			if m.selectedScript != nil && m.client != nil {
				m.scriptsLoading = true
				return m, deleteScriptCmd(m.client, m.selectedScript.ID)
			}
		case "n", "esc":
			m.libraryMode = libModeList
		}
		return m, nil
	}

	if m.libraryMode == libModeEditing {
		return handleLibraryScriptsEditKey(m, msg)
	}

	if m.libraryMode == libModeDetail {
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.libraryMode = libModeList
			return m, nil
		case "d":
			m.libraryMode = libModeConfirmDelete
		case "e":
			if m.selectedScript != nil {
				m = libraryScriptsBeginEdit(m, *m.selectedScript)
				return m, textinput.Blink
			}
		}
		return m, nil
	}

	// list mode
	if mm, cmd, handled := handleLibraryTabNav(m, msg); handled {
		return mm, cmd
	}
	switch msg.String() {
	case "up", "k":
		if m.scriptCursor > 0 {
			m.scriptCursor--
		}
	case "down", "j":
		if m.scriptCursor < len(m.scriptItems)-1 {
			m.scriptCursor++
		}
	case "enter":
		if m.scriptCursor < len(m.scriptItems) && m.client != nil {
			m.scriptsLoading = true
			return m, fetchScriptDetailCmd(m.client, m.scriptItems[m.scriptCursor].ID)
		}
	case "e":
		if m.scriptCursor < len(m.scriptItems) {
			m = libraryScriptsBeginEdit(m, m.scriptItems[m.scriptCursor])
			return m, textinput.Blink
		}
	case "d":
		if m.scriptCursor < len(m.scriptItems) {
			m.selectedScript = &m.scriptItems[m.scriptCursor]
			m.libraryMode = libModeConfirmDelete
		}
	}
	return m, nil
}

// libraryScriptsBeginEdit prepares the edit overlay for the given script.
func libraryScriptsBeginEdit(m hubModel, s ScriptItem) hubModel {
	m.selectedScript = &s
	m.libraryMode = libModeEditing
	m.scriptEditField = 0
	m.scriptEditNameInput.SetValue(s.Name)
	m.scriptEditNameInput.Focus()
	m.scriptEditDescInput.SetValue(s.Description)
	m.scriptEditDescInput.Blur()
	return m
}

// handleLibraryScriptsEditKey handles key events while the script edit
// overlay is open (two fields: name, description).
func handleLibraryScriptsEditKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.libraryMode = libModeDetail
		m.scriptEditNameInput.Blur()
		m.scriptEditDescInput.Blur()
		return m, nil
	case "tab":
		m.scriptEditField = (m.scriptEditField + 1) % 2
		if m.scriptEditField == 0 {
			m.scriptEditNameInput.Focus()
			m.scriptEditDescInput.Blur()
		} else {
			m.scriptEditNameInput.Blur()
			m.scriptEditDescInput.Focus()
		}
		return m, textinput.Blink
	case "enter":
		if m.selectedScript == nil || m.client == nil {
			return m, nil
		}
		name := strings.TrimSpace(m.scriptEditNameInput.Value())
		desc := m.scriptEditDescInput.Value()
		if name == "" {
			return m, nil
		}
		m.scriptsLoading = true
		m.libraryMode = libModeDetail
		m.scriptEditNameInput.Blur()
		m.scriptEditDescInput.Blur()
		return m, updateScriptMetaCmd(m.client, m.selectedScript.ID, name, desc)
	default:
		var cmd tea.Cmd
		if m.scriptEditField == 0 {
			m.scriptEditNameInput, cmd = m.scriptEditNameInput.Update(msg)
		} else {
			m.scriptEditDescInput, cmd = m.scriptEditDescInput.Update(msg)
		}
		return m, cmd
	}
}

// --- Rendering ---

// renderLibraryScriptsBody renders the Scripts tab body.
func renderLibraryScriptsBody(m hubModel, innerW int) string {
	var b strings.Builder
	if m.scriptsLoading {
		b.WriteString("  " + m.spinner.View() + " Loading...\n")
		return b.String()
	}

	if m.libraryMode == libModeEditing && m.selectedScript != nil {
		b.WriteString("  " + sectionStyle.Render("Edit script metadata") + "\n\n")
		nameLabel := "  Name:        "
		descLabel := "  Description: "
		if m.scriptEditField == 0 {
			nameLabel = "  " + selectedStyle.Render("Name:       ") + " "
		} else {
			descLabel = "  " + selectedStyle.Render("Description:") + " "
		}
		b.WriteString(nameLabel + m.scriptEditNameInput.View() + "\n")
		b.WriteString(descLabel + m.scriptEditDescInput.View() + "\n\n")
		b.WriteString("  " + dimStyle.Render("tab: switch field  enter: save  esc: cancel") + "\n")
		return b.String()
	}

	if m.libraryMode == libModeConfirmDelete && m.selectedScript != nil {
		b.WriteString("  " + errorStyle.Render(fmt.Sprintf("Delete script \"%s\"? (y/n)", m.selectedScript.Name)) + "\n")
		return b.String()
	}

	if m.libraryMode == libModeDetail && m.selectedScript != nil {
		s := m.selectedScript
		b.WriteString("  " + normalStyle.Render(s.Name) + "  " + dimStyle.Render(s.Runtime) + "\n")
		if s.Description != "" {
			b.WriteString("  " + dimStyle.Render(s.Description) + "\n")
		}
		b.WriteString("\n  " + sectionStyle.Render("CODE PREVIEW") + "\n")
		lines := strings.Split(s.Code, "\n")
		maxPreview := 12
		for i, ln := range lines {
			if i >= maxPreview {
				b.WriteString("  " + dimStyle.Render(fmt.Sprintf("… %d more lines", len(lines)-maxPreview)) + "\n")
				break
			}
			b.WriteString("  " + dimStyle.Render(truncate(ln, innerW)) + "\n")
		}
		b.WriteString("\n  " + renderLibraryCLIHint(libTabScripts) + "\n")
		return b.String()
	}

	// list mode
	b.WriteString(sectionStyle.Render(fmt.Sprintf("  Scripts  %d", len(m.scriptItems))) + "\n")
	if len(m.scriptItems) == 0 {
		b.WriteString("  " + dimStyle.Render("No scripts found") + "\n")
		b.WriteString("  " + renderLibraryCLIHint(libTabScripts) + "\n")
		return b.String()
	}
	start, end := scrollWindow(m.scriptCursor, len(m.scriptItems), 12)
	for i := start; i < end; i++ {
		s := m.scriptItems[i]
		cursor := "  "
		if i == m.scriptCursor {
			cursor = selectedStyle.Render("▸ ")
		}
		name := normalStyle.Render(fmt.Sprintf("%-22s", s.Name))
		runtime := dimStyle.Render(fmt.Sprintf("%-10s", s.Runtime))
		desc := ""
		if s.Description != "" {
			desc = "   " + dimStyle.Render(truncate(s.Description, 24))
		}
		b.WriteString(fmt.Sprintf("  %s%s  %s%s\n", cursor, name, runtime, desc))
	}
	b.WriteString("\n  " + renderLibraryCLIHint(libTabScripts) + "\n")
	return b.String()
}
