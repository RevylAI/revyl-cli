package ui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

var ErrSelectionCancelled = errors.New("selection cancelled")

type multiSelectModel struct {
	message   string
	options   []SelectOption
	selected  map[string]bool
	cursor    int
	done      bool
	cancelled bool
}

func (m multiSelectModel) Init() tea.Cmd { return nil }

func (m multiSelectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c", "esc":
		m.cancelled, m.done = true, true
		return m, tea.Quit
	case "enter":
		m.done = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor+1 < len(m.options) {
			m.cursor++
		}
	case " ":
		value := m.options[m.cursor].Value
		m.selected[value] = !m.selected[value]
	case "a":
		allSelected := true
		for _, option := range m.options {
			allSelected = allSelected && m.selected[option.Value]
		}
		for _, option := range m.options {
			m.selected[option.Value] = !allSelected
		}
	}
	return m, nil
}

func (m multiSelectModel) View() string {
	if m.done {
		return ""
	}
	var output strings.Builder
	output.WriteString(InfoStyle.Render(m.message) + "\n\n")
	start := max(0, m.cursor-7)
	end := min(len(m.options), start+8)
	for i := start; i < end; i++ {
		option := m.options[i]
		marker := "[ ]"
		if m.selected[option.Value] {
			marker = "[x]"
		}
		prefix := "  "
		if i == m.cursor {
			prefix = "> "
		}
		fmt.Fprintf(&output, "%s%s %s\n", prefix, marker, option.Label)
		if i == m.cursor && option.Description != "" {
			output.WriteString("      " + DimStyle.Render(option.Description) + "\n")
		}
	}
	if len(m.options) > 8 {
		fmt.Fprintf(&output, "\n  %d–%d of %d\n", start+1, end, len(m.options))
	}
	output.WriteString("\n" + DimStyle.Render("↑/↓ navigate • space toggle • a toggle all • enter continue • esc cancel"))
	return output.String()
}

func MultiSelect(message string, options []SelectOption, initialValues []string) ([]string, error) {
	if !IsInteractive() {
		return nil, fmt.Errorf("selection requires an interactive terminal")
	}
	if len(options) == 0 {
		return nil, fmt.Errorf("no options provided")
	}
	selected := make(map[string]bool, len(initialValues))
	for _, value := range initialValues {
		selected[value] = true
	}
	model := multiSelectModel{message: message, options: options, selected: selected}
	program := tea.NewProgram(model, tea.WithOutput(os.Stderr))
	final, err := program.Run()
	if err != nil {
		return nil, fmt.Errorf("interactive selection failed: %w", err)
	}
	result, ok := final.(multiSelectModel)
	if !ok {
		return nil, fmt.Errorf("unexpected selection result")
	}
	if result.cancelled {
		return nil, ErrSelectionCancelled
	}
	values := make([]string, 0, len(result.selected))
	for _, option := range options {
		if result.selected[option.Value] {
			values = append(values, option.Value)
		}
	}
	return values, nil
}
