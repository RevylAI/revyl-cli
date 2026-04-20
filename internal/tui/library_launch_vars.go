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

func fetchLaunchVarsCmd(client *api.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := client.ListOrgLaunchVariables(ctx)
		if err != nil {
			return LaunchVarListMsg{Err: err}
		}

		items := make([]LaunchVarItem, 0, len(resp.Result))
		for _, v := range resp.Result {
			items = append(items, LaunchVarItem{
				ID:                v.ID,
				Key:               v.Key,
				Value:             v.Value,
				Description:       v.Description,
				AttachedTestCount: v.AttachedTestCount,
			})
		}
		return LaunchVarListMsg{LaunchVars: items}
	}
}

func saveLaunchVarCmd(client *api.Client, id, key, value, description string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		desc := description
		var err error
		if id == "" {
			_, err = client.AddOrgLaunchVariable(ctx, key, value, &desc)
		} else {
			_, err = client.UpdateOrgLaunchVariable(ctx, id, &key, &value, &desc)
		}
		return LaunchVarSavedMsg{Err: err}
	}
}

func deleteLaunchVarCmd(client *api.Client, id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_, err := client.DeleteOrgLaunchVariable(ctx, id)
		return LaunchVarDeletedMsg{Err: err}
	}
}

func handleLibraryLaunchVarsKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.libraryMode == libModeConfirmDelete {
		switch msg.String() {
		case "y":
			m.libraryMode = libModeList
			if m.selectedLaunchVar != nil && m.client != nil {
				m.launchVarsLoading = true
				return m, deleteLaunchVarCmd(m.client, m.selectedLaunchVar.ID)
			}
		case "n", "esc":
			m.libraryMode = libModeList
		}
		return m, nil
	}

	if m.libraryMode == libModeEditing {
		return handleLibraryLaunchVarsEditKey(m, msg)
	}

	if mm, cmd, handled := handleLibraryTabNav(m, msg); handled {
		return mm, cmd
	}

	switch msg.String() {
	case "up", "k":
		if m.launchVarCursor > 0 {
			m.launchVarCursor--
		}
	case "down", "j":
		if m.launchVarCursor < len(m.launchVarItems)-1 {
			m.launchVarCursor++
		}
	case "n":
		m = libraryLaunchVarsBeginEdit(m, nil)
		return m, textinput.Blink
	case "e", "enter":
		if m.launchVarCursor < len(m.launchVarItems) {
			v := m.launchVarItems[m.launchVarCursor]
			m = libraryLaunchVarsBeginEdit(m, &v)
			return m, textinput.Blink
		}
	case "d":
		if m.launchVarCursor < len(m.launchVarItems) {
			v := m.launchVarItems[m.launchVarCursor]
			m.selectedLaunchVar = &v
			m.libraryMode = libModeConfirmDelete
		}
	case "v":
		m.launchVarShowValues = !m.launchVarShowValues
	}
	return m, nil
}

func libraryLaunchVarsBeginEdit(m hubModel, v *LaunchVarItem) hubModel {
	m.libraryMode = libModeEditing
	m.launchVarEditField = 0
	if v == nil {
		m.selectedLaunchVar = nil
		m.launchVarIsCreating = true
		m.launchVarKeyInput.SetValue("")
		m.launchVarValueInput.SetValue("")
		m.launchVarDescriptionInput.SetValue("")
	} else {
		m.selectedLaunchVar = v
		m.launchVarIsCreating = false
		m.launchVarKeyInput.SetValue(v.Key)
		m.launchVarValueInput.SetValue(v.Value)
		m.launchVarDescriptionInput.SetValue(v.Description)
	}
	m.launchVarKeyInput.Focus()
	m.launchVarValueInput.Blur()
	m.launchVarDescriptionInput.Blur()
	return m
}

func handleLibraryLaunchVarsEditKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.libraryMode = libModeList
		m.launchVarKeyInput.Blur()
		m.launchVarValueInput.Blur()
		m.launchVarDescriptionInput.Blur()
		return m, nil
	case "tab":
		m.launchVarEditField = (m.launchVarEditField + 1) % 3
		switch m.launchVarEditField {
		case 0:
			m.launchVarKeyInput.Focus()
			m.launchVarValueInput.Blur()
			m.launchVarDescriptionInput.Blur()
		case 1:
			m.launchVarKeyInput.Blur()
			m.launchVarValueInput.Focus()
			m.launchVarDescriptionInput.Blur()
		default:
			m.launchVarKeyInput.Blur()
			m.launchVarValueInput.Blur()
			m.launchVarDescriptionInput.Focus()
		}
		return m, textinput.Blink
	case "enter":
		key := strings.TrimSpace(m.launchVarKeyInput.Value())
		value := m.launchVarValueInput.Value()
		desc := m.launchVarDescriptionInput.Value()
		if key == "" || value == "" || m.client == nil {
			return m, nil
		}

		id := ""
		if !m.launchVarIsCreating && m.selectedLaunchVar != nil {
			id = m.selectedLaunchVar.ID
		}

		m.launchVarsLoading = true
		m.libraryMode = libModeList
		m.launchVarKeyInput.Blur()
		m.launchVarValueInput.Blur()
		m.launchVarDescriptionInput.Blur()
		return m, saveLaunchVarCmd(m.client, id, key, value, desc)
	default:
		var cmd tea.Cmd
		switch m.launchVarEditField {
		case 0:
			m.launchVarKeyInput, cmd = m.launchVarKeyInput.Update(msg)
		case 1:
			m.launchVarValueInput, cmd = m.launchVarValueInput.Update(msg)
		default:
			m.launchVarDescriptionInput, cmd = m.launchVarDescriptionInput.Update(msg)
		}
		return m, cmd
	}
}

func maskLibraryLaunchVarValue(value string) string {
	if value == "" {
		return "(empty)"
	}
	return strings.Repeat("*", min(max(len(value), 4), 8))
}

func renderLibraryLaunchVarsBody(m hubModel, innerW int) string {
	_ = innerW
	var b strings.Builder
	if m.launchVarsLoading {
		b.WriteString("  " + m.spinner.View() + " Loading...\n")
		return b.String()
	}

	if m.libraryMode == libModeEditing {
		heading := "Edit launch variable"
		if m.launchVarIsCreating {
			heading = "New launch variable"
		}
		b.WriteString("  " + sectionStyle.Render(heading) + "\n\n")
		keyLabel := "  Key:         "
		valueLabel := "  Value:       "
		descLabel := "  Description: "
		switch m.launchVarEditField {
		case 0:
			keyLabel = "  " + selectedStyle.Render("Key:        ") + " "
		case 1:
			valueLabel = "  " + selectedStyle.Render("Value:      ") + " "
		default:
			descLabel = "  " + selectedStyle.Render("Description:") + " "
		}
		b.WriteString(keyLabel + m.launchVarKeyInput.View() + "\n")
		b.WriteString(valueLabel + m.launchVarValueInput.View() + "\n")
		b.WriteString(descLabel + m.launchVarDescriptionInput.View() + "\n\n")
		b.WriteString("  " + dimStyle.Render("tab: switch field  enter: save  esc: cancel") + "\n")
		return b.String()
	}

	if m.libraryMode == libModeConfirmDelete && m.selectedLaunchVar != nil {
		b.WriteString("  " + errorStyle.Render(fmt.Sprintf("Delete launch variable \"%s\"? (y/n)", m.selectedLaunchVar.Key)) + "\n")
		return b.String()
	}

	b.WriteString(sectionStyle.Render("  Launch vars") + "\n")
	if len(m.launchVarItems) == 0 {
		b.WriteString("  " + dimStyle.Render("No launch variables set") + "\n")
		b.WriteString("  " + renderLibraryCLIHint(libTabLaunchVars) + "\n")
		return b.String()
	}

	start, end := scrollWindow(m.launchVarCursor, len(m.launchVarItems), 12)
	for i := start; i < end; i++ {
		v := m.launchVarItems[i]
		cursor := "  "
		if i == m.launchVarCursor {
			cursor = selectedStyle.Render("▸ ")
		}
		value := maskLibraryLaunchVarValue(v.Value)
		if m.launchVarShowValues {
			value = v.Value
		}
		meta := dimStyle.Render(fmt.Sprintf("%d tests", v.AttachedTestCount))
		desc := ""
		if v.Description != "" {
			desc = "   " + dimStyle.Render(truncate(v.Description, 24))
		}
		b.WriteString(fmt.Sprintf("  %s%s   %s   %s%s\n", cursor, normalStyle.Render(v.Key), dimStyle.Render(truncate(value, 16)), meta, desc))
	}

	label := "show values"
	if m.launchVarShowValues {
		label = "hide values"
	}
	b.WriteString("\n  " + dimStyle.Render("v: "+label) + "\n")
	b.WriteString("  " + renderLibraryCLIHint(libTabLaunchVars) + "\n")
	return b.String()
}
