// Package ui provides interactive input components.
package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Prompt displays a prompt and reads user input.
//
// Parameters:
//   - message: The prompt message to display
//
// Returns:
//   - string: The user's input
//   - error: Any error that occurred
func Prompt(message string) (string, error) {
	fmt.Printf("%s ", InfoStyle.Render(message))

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(input), nil
}

// PromptConfirm displays a yes/no confirmation prompt.
//
// Parameters:
//   - message: The prompt message to display
//   - defaultYes: Whether the default is yes (true) or no (false)
//
// Returns:
//   - bool: True if user confirmed, false otherwise
//   - error: Any error that occurred
func PromptConfirm(message string, defaultYes bool) (bool, error) {
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}

	input, err := Prompt(fmt.Sprintf("%s %s", message, suffix))
	if err != nil {
		return false, err
	}

	input = strings.ToLower(strings.TrimSpace(input))

	if input == "" {
		return defaultYes, nil
	}

	return input == "y" || input == "yes", nil
}

// selectModel is a bubbletea model for interactive arrow-key selection.
type selectModel struct {
	message      string
	options      []string
	descriptions []string
	cursor       int
	selected     int
	done         bool
	cancelled    bool
}

func (m selectModel) Init() tea.Cmd {
	return nil
}

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.cancelled = true
			m.done = true
			return m, tea.Quit

		case "enter":
			m.selected = m.cursor
			m.done = true
			return m, tea.Quit

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}

		case "down", "j":
			if m.cursor < len(m.options)-1 {
				m.cursor++
			}

		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			num := int(msg.String()[0] - '0')
			if num >= 1 && num <= len(m.options) {
				m.selected = num - 1
				m.done = true
				return m, tea.Quit
			}
		}
	}

	return m, nil
}

func (m selectModel) View() string {
	if m.done {
		return ""
	}

	var b strings.Builder
	b.WriteString(InfoStyle.Render(m.message))
	b.WriteString("\n")

	for i, opt := range m.options {
		number := AccentStyle.Render(fmt.Sprintf("[%d]", i+1))
		if i == m.cursor {
			marker := AccentStyle.Render(">")
			label := TitleStyle.Render(opt)
			b.WriteString(fmt.Sprintf("  %s %s %s\n", marker, number, label))
		} else {
			label := InfoStyle.Render(opt)
			b.WriteString(fmt.Sprintf("    %s %s\n", number, label))
		}
		if i < len(m.descriptions) && m.descriptions[i] != "" {
			b.WriteString(fmt.Sprintf("      %s\n", DimStyle.Render(m.descriptions[i])))
		}
	}

	b.WriteString("\n")
	b.WriteString(DimStyle.Render("  ↑/↓ navigate • enter select • 1-9 jump • esc cancel"))

	return b.String()
}

// runSelectTea runs the bubbletea selection program.
// Returns selected index or -1 if cancelled.
func runSelectTea(message string, options []string, descriptions []string, initialCursor int) (int, error) {
	m := selectModel{
		message:      message,
		options:      options,
		descriptions: descriptions,
		cursor:       initialCursor,
		selected:     -1,
	}

	programOptions := []tea.ProgramOption{}
	if !isOutputTTY && isStderrTTY {
		// Keep interactive menus functional when stdout is piped but stderr is a TTY.
		programOptions = append(programOptions, tea.WithOutput(os.Stderr))
	}

	p := tea.NewProgram(m, programOptions...)
	finalModel, err := p.Run()
	if err != nil {
		return -1, fmt.Errorf("interactive select failed: %w", err)
	}

	result, ok := finalModel.(selectModel)
	if !ok {
		return -1, fmt.Errorf("interactive select failed: unexpected model type")
	}
	if result.cancelled {
		return -1, fmt.Errorf("selection cancelled")
	}
	if result.selected < 0 || result.selected >= len(options) {
		return -1, fmt.Errorf("interactive select failed: invalid selection")
	}

	return result.selected, nil
}

// promptSelectFallback is the non-TTY fallback for PromptSelect.
func promptSelectFallback(message string, options []string) (int, error) {
	fmt.Println(InfoStyle.Render(message))

	for i, opt := range options {
		fmt.Printf("    %s %s\n", AccentStyle.Render(fmt.Sprintf("[%d]", i+1)), InfoStyle.Render(opt))
	}

	for {
		input, err := Prompt("Select option:")
		if err != nil {
			return -1, err
		}

		var selection int
		_, err = fmt.Sscanf(input, "%d", &selection)
		if err != nil || selection < 1 || selection > len(options) {
			PrintWarning("Please enter a number between 1 and %d", len(options))
			continue
		}

		return selection - 1, nil
	}
}

// PromptSelect displays a selection prompt.
//
// Parameters:
//   - message: The prompt message to display
//   - options: List of options to choose from
//
// Returns:
//   - int: Index of selected option
//   - error: Any error that occurred
func PromptSelect(message string, options []string) (int, error) {
	if !canRunInteractiveSelect() {
		return promptSelectFallback(message, options)
	}

	return runSelectTea(message, options, nil, 0)
}

// SelectOption represents an option in a select prompt.
type SelectOption struct {
	// Label is the display text for this option.
	Label string

	// Value is the value returned when this option is selected.
	Value string

	// Description is an optional description shown below the label.
	Description string
}

// selectFallback is the non-TTY fallback for Select.
func selectFallback(message string, options []SelectOption, current int) (int, string, error) {
	fmt.Println(InfoStyle.Render(message))
	for i, opt := range options {
		number := AccentStyle.Render(fmt.Sprintf("[%d]", i+1))
		if i == current {
			marker := AccentStyle.Render(">")
			label := TitleStyle.Render(opt.Label)
			fmt.Printf("  %s %s %s\n", marker, number, label)
		} else {
			label := InfoStyle.Render(opt.Label)
			fmt.Printf("    %s %s\n", number, label)
		}
		if opt.Description != "" {
			fmt.Printf("      %s\n", DimStyle.Render(opt.Description))
		}
	}

	for {
		input, err := Prompt(fmt.Sprintf("Select option [%d]:", current+1))
		if err != nil {
			return -1, "", err
		}
		if input == "" {
			return current, options[current].Value, nil
		}

		var selection int
		_, err = fmt.Sscanf(input, "%d", &selection)
		if err != nil || selection < 1 || selection > len(options) {
			PrintWarning("Please enter a number between 1 and %d", len(options))
			continue
		}
		idx := selection - 1
		return idx, options[idx].Value, nil
	}
}

// Select prompts the user to select from a list of options with values.
//
// Parameters:
//   - message: The prompt message to display
//   - options: List of options to choose from
//   - defaultIndex: Default selection index (0-based), -1 for no default
//
// Returns:
//   - int: Index of selected option
//   - string: Value of selected option
//   - error: Any error that occurred
func Select(message string, options []SelectOption, defaultIndex int) (int, string, error) {
	if len(options) == 0 {
		return -1, "", fmt.Errorf("no options provided")
	}
	current := defaultIndex
	if current < 0 || current >= len(options) {
		current = 0
	}

	if !canRunInteractiveSelect() {
		return selectFallback(message, options, current)
	}

	labels := make([]string, len(options))
	descriptions := make([]string, len(options))
	for i, opt := range options {
		labels[i] = opt.Label
		descriptions[i] = opt.Description
	}

	idx, err := runSelectTea(message, labels, descriptions, current)
	if err != nil {
		return -1, "", err
	}

	return idx, options[idx].Value, nil
}

func canRunInteractiveSelect() bool {
	return isInputTTY && (isOutputTTY || isStderrTTY)
}

// Confirm prompts the user for a yes/no confirmation.
// This is an alias for PromptConfirm for convenience.
//
// Parameters:
//   - message: The prompt message to display
//
// Returns:
//   - bool: True if user confirmed, false otherwise
func Confirm(message string) bool {
	result, err := PromptConfirm(message, true)
	if err != nil {
		return false
	}
	return result
}
