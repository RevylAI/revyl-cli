// Package tui — Global Variables tab of the Library hub.
//
// Global variables support full in-TUI CRUD: the wizard is small enough
// (name + value) that create/edit are cheap to ship.
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

const variableSecretMask = "********"

// --- Commands ---

// fetchVariablesCmd loads the org's global variables.
func fetchVariablesCmd(client *api.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := client.ListGlobalVariables(ctx)
		if err != nil {
			return VariableListMsg{Err: err}
		}
		items := make([]VariableItem, 0, len(resp.Result))
		for _, v := range resp.Result {
			value := ""
			if v.VariableValue != nil {
				value = *v.VariableValue
			}
			isSecret := v.IsSecret != nil && *v.IsSecret
			items = append(items, VariableItem{
				ID:       v.Id.String(),
				Name:     v.VariableName,
				Value:    value,
				IsSecret: isSecret,
			})
		}
		return VariableListMsg{Variables: items}
	}
}

// saveVariableCmd creates a new variable (when id is empty) or updates an
// existing one.
func saveVariableCmd(client *api.Client, id, name string, value *string, isSecret bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var err error
		opts := api.GlobalVariableWriteOptions{IsSecret: &isSecret}
		if id == "" {
			writeValue := ""
			if value != nil {
				writeValue = *value
			}
			_, err = client.AddGlobalVariable(ctx, name, writeValue, opts)
		} else {
			_, err = client.UpdateGlobalVariable(ctx, id, name, value, opts)
		}
		return VariableSavedMsg{Err: err}
	}
}

// deleteVariableCmd deletes a global variable by ID.
func deleteVariableCmd(client *api.Client, id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := client.DeleteGlobalVariable(ctx, id)
		return VariableDeletedMsg{Err: err}
	}
}

// --- Key handling ---

// handleLibraryVariablesKey handles key events for the Variables tab.
func handleLibraryVariablesKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.libraryMode == libModeConfirmDelete {
		switch msg.String() {
		case "y":
			m.libraryMode = libModeList
			if m.selectedVar != nil && m.client != nil {
				m.varsLoading = true
				return m, deleteVariableCmd(m.client, m.selectedVar.ID)
			}
		case "n", "esc":
			m.libraryMode = libModeList
		}
		return m, nil
	}

	if m.libraryMode == libModeEditing {
		return handleLibraryVariablesEditKey(m, msg)
	}

	// list mode
	if mm, cmd, handled := handleLibraryTabNav(m, msg); handled {
		return mm, cmd
	}
	switch msg.String() {
	case "up", "k":
		if m.varCursor > 0 {
			m.varCursor--
		}
	case "down", "j":
		if m.varCursor < len(m.varItems)-1 {
			m.varCursor++
		}
	case "n":
		m = libraryVariablesBeginEdit(m, nil)
		return m, textinput.Blink
	case "e", "enter":
		if m.varCursor < len(m.varItems) {
			v := m.varItems[m.varCursor]
			m = libraryVariablesBeginEdit(m, &v)
			return m, textinput.Blink
		}
	case "d":
		if m.varCursor < len(m.varItems) {
			v := m.varItems[m.varCursor]
			m.selectedVar = &v
			m.libraryMode = libModeConfirmDelete
		}
	}
	return m, nil
}

// libraryVariablesBeginEdit opens the edit overlay for an existing variable,
// or the create overlay when v is nil.
func libraryVariablesBeginEdit(m hubModel, v *VariableItem) hubModel {
	m.libraryMode = libModeEditing
	m.varEditField = 0
	if v == nil {
		m.selectedVar = nil
		m.varIsCreating = true
		m.varIsSecret = false
		m.varNameInput.SetValue("")
		m.varValueInput.SetValue("")
	} else {
		m.selectedVar = v
		m.varIsCreating = false
		m.varIsSecret = v.IsSecret
		m.varNameInput.SetValue(v.Name)
		if v.IsSecret {
			m.varValueInput.SetValue(variableSecretMask)
		} else {
			m.varValueInput.SetValue(v.Value)
		}
	}
	m.varNameInput.Focus()
	m.varValueInput.Blur()
	return m
}

// handleLibraryVariablesEditKey handles key events while the variable
// create/edit overlay is open.
func handleLibraryVariablesEditKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.libraryMode = libModeList
		m.varNameInput.Blur()
		m.varValueInput.Blur()
		return m, nil
	case "tab":
		m.varEditField = (m.varEditField + 1) % 2
		if m.varEditField == 0 {
			m.varNameInput.Focus()
			m.varValueInput.Blur()
		} else {
			m.varNameInput.Blur()
			m.varValueInput.Focus()
		}
		return m, textinput.Blink
	case "s":
		if m.varNameInput.Focused() || m.varValueInput.Focused() {
			var cmd tea.Cmd
			if m.varEditField == 0 {
				m.varNameInput, cmd = m.varNameInput.Update(msg)
			} else {
				m.varValueInput, cmd = m.varValueInput.Update(msg)
			}
			return m, cmd
		}
		fallthrough
	case "ctrl+s":
		nextSecret := !m.varIsSecret
		if !nextSecret && m.selectedVar != nil && m.selectedVar.IsSecret && m.varValueInput.Value() == variableSecretMask {
			m.varValueInput.SetValue("")
		}
		if nextSecret && m.selectedVar != nil && m.selectedVar.IsSecret && m.varValueInput.Value() == "" {
			m.varValueInput.SetValue(variableSecretMask)
		}
		m.varIsSecret = nextSecret
		return m, nil
	case "enter":
		name := strings.TrimSpace(m.varNameInput.Value())
		value := m.varValueInput.Value()
		if name == "" || m.client == nil {
			return m, nil
		}
		if m.varIsSecret && value == "" {
			return m, nil
		}
		id := ""
		valuePtr := &value
		if !m.varIsCreating && m.selectedVar != nil {
			id = m.selectedVar.ID
			if m.selectedVar.IsSecret && !m.varIsSecret && (value == "" || value == variableSecretMask) {
				return m, nil
			}
			if m.selectedVar.IsSecret && m.varIsSecret && value == variableSecretMask {
				valuePtr = nil
			}
		}
		m.varsLoading = true
		m.libraryMode = libModeList
		m.varNameInput.Blur()
		m.varValueInput.Blur()
		return m, saveVariableCmd(m.client, id, name, valuePtr, m.varIsSecret)
	default:
		var cmd tea.Cmd
		if m.varEditField == 0 {
			m.varNameInput, cmd = m.varNameInput.Update(msg)
		} else {
			m.varValueInput, cmd = m.varValueInput.Update(msg)
		}
		return m, cmd
	}
}

// --- Rendering ---

// renderLibraryVariablesBody renders the Variables tab body.
func renderLibraryVariablesBody(m hubModel, innerW int) string {
	_ = innerW
	var b strings.Builder
	if m.varsLoading {
		b.WriteString("  " + m.spinner.View() + " Loading...\n")
		return b.String()
	}

	if m.libraryMode == libModeEditing {
		heading := "Edit global variable"
		if m.varIsCreating {
			heading = "New global variable"
		}
		b.WriteString("  " + sectionStyle.Render(heading) + "\n\n")
		nameLabel := "  Name:  "
		valueLabel := "  Value: "
		if m.varEditField == 0 {
			nameLabel = "  " + selectedStyle.Render("Name: ") + " "
		} else {
			valueLabel = "  " + selectedStyle.Render("Value:") + " "
		}
		b.WriteString(nameLabel + m.varNameInput.View() + "\n")
		b.WriteString(valueLabel + m.varValueInput.View() + "\n\n")
		secretState := "off"
		if m.varIsSecret {
			secretState = "on"
		}
		b.WriteString("  " + dimStyle.Render(fmt.Sprintf("Secret: %s", secretState)) + "\n")
		b.WriteString("  " + dimStyle.Render("tab: switch field  ctrl+s: toggle secret  enter: save  esc: cancel") + "\n")
		return b.String()
	}

	if m.libraryMode == libModeConfirmDelete && m.selectedVar != nil {
		b.WriteString("  " + errorStyle.Render("Delete variable \""+m.selectedVar.Name+"\"? (y/n)") + "\n")
		return b.String()
	}

	// list mode
	b.WriteString(sectionStyle.Render("  Global variables") + "\n")
	if len(m.varItems) == 0 {
		b.WriteString("  " + dimStyle.Render("No global variables set") + "\n")
		b.WriteString("  " + renderLibraryCLIHint(libTabVariables) + "\n")
		return b.String()
	}
	start, end := scrollWindow(m.varCursor, len(m.varItems), 12)
	for i := start; i < end; i++ {
		v := m.varItems[i]
		cursor := "  "
		if i == m.varCursor {
			cursor = selectedStyle.Render("▸ ")
		}
		name := normalStyle.Render("{{global." + v.Name + "}}")
		value := v.Value
		if v.IsSecret {
			value = variableSecretMask
		}
		val := dimStyle.Render(truncate(value, 32))
		b.WriteString("  " + cursor + name + "   " + val + "\n")
	}
	b.WriteString("\n  " + renderLibraryCLIHint(libTabVariables) + "\n")
	return b.String()
}
