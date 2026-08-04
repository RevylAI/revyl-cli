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
				IsSecret:          v.IsSecret,
				Description:       v.Description,
				AttachedTestCount: v.AttachedTestCount,
			})
		}
		return LaunchVarListMsg{LaunchVars: items}
	}
}

func saveLaunchVarCmd(client *api.Client, id, key string, value *string, description string, isSecret bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		desc := description
		opts := api.OrgLaunchVariableWriteOptions{IsSecret: &isSecret}
		var err error
		if id == "" {
			if value == nil {
				return LaunchVarSavedMsg{Err: fmt.Errorf("launch variable value is required")}
			}
			_, err = client.AddOrgLaunchVariable(ctx, key, *value, &desc, opts)
		} else {
			_, err = client.UpdateOrgLaunchVariable(ctx, id, &key, value, &desc, opts)
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
	}
	return m, nil
}

func libraryLaunchVarsBeginEdit(m hubModel, v *LaunchVarItem) hubModel {
	m.libraryMode = libModeEditing
	m.launchVarEditField = 0
	if v == nil {
		m.selectedLaunchVar = nil
		m.launchVarIsCreating = true
		m.launchVarIsSecret = false
		m.launchVarKeyInput.SetValue("")
		m.launchVarValueInput.SetValue("")
		m.launchVarDescriptionInput.SetValue("")
	} else {
		m.selectedLaunchVar = v
		m.launchVarIsCreating = false
		m.launchVarIsSecret = v.IsSecret
		m.launchVarKeyInput.SetValue(v.Key)
		if v.IsSecret {
			m.launchVarValueInput.SetValue(variableSecretMask)
		} else {
			m.launchVarValueInput.SetValue(v.Value)
		}
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
	case "s":
		if m.launchVarKeyInput.Focused() || m.launchVarValueInput.Focused() || m.launchVarDescriptionInput.Focused() {
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
		fallthrough
	case "ctrl+s":
		nextSecret := !m.launchVarIsSecret
		if !nextSecret && m.selectedLaunchVar != nil && m.selectedLaunchVar.IsSecret && m.launchVarValueInput.Value() == variableSecretMask {
			m.launchVarValueInput.SetValue("")
		}
		if nextSecret && m.selectedLaunchVar != nil && m.selectedLaunchVar.IsSecret && m.launchVarValueInput.Value() == "" {
			m.launchVarValueInput.SetValue(variableSecretMask)
		}
		m.launchVarIsSecret = nextSecret
		return m, nil
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
		valuePtr := &value
		if !m.launchVarIsCreating && m.selectedLaunchVar != nil {
			if m.selectedLaunchVar.IsSecret && !m.launchVarIsSecret && (value == "" || value == variableSecretMask) {
				return m, nil
			}
			if m.selectedLaunchVar.IsSecret && m.launchVarIsSecret && value == variableSecretMask {
				valuePtr = nil
			}
		}

		m.launchVarsLoading = true
		m.libraryMode = libModeList
		m.launchVarKeyInput.Blur()
		m.launchVarValueInput.Blur()
		m.launchVarDescriptionInput.Blur()
		return m, saveLaunchVarCmd(m.client, id, key, valuePtr, desc, m.launchVarIsSecret)
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

func maskLibraryLaunchVarValue() string {
	return "********"
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
		secretState := "off"
		if m.launchVarIsSecret {
			secretState = "on"
		}
		b.WriteString("  " + dimStyle.Render(fmt.Sprintf("Secret: %s", secretState)) + "\n")
		b.WriteString("  " + dimStyle.Render("tab: switch field  ctrl+s: toggle secret  enter: save  esc: cancel") + "\n")
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
		value := v.Value
		secrecy := "plain"
		if v.IsSecret {
			value = maskLibraryLaunchVarValue()
			secrecy = "secret"
		}
		meta := dimStyle.Render(fmt.Sprintf("%d tests", v.AttachedTestCount))
		desc := ""
		if v.Description != "" {
			desc = "   " + dimStyle.Render(truncate(v.Description, 24))
		}
		b.WriteString(fmt.Sprintf("  %s%s   %s   %s   %s%s\n", cursor, normalStyle.Render(v.Key), dimStyle.Render(truncate(value, 16)), dimStyle.Render(secrecy), meta, desc))
	}

	b.WriteString("\n  " + dimStyle.Render("Non-secret values are visible; secrets stay masked") + "\n")
	b.WriteString("  " + renderLibraryCLIHint(libTabLaunchVars) + "\n")
	return b.String()
}
