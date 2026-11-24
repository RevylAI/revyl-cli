package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

var (
	stdinReader   *bufio.Reader
	stdinReaderMu sync.Mutex
	stdinSource   *os.File
)

func getStdinReader() *bufio.Reader {
	stdinReaderMu.Lock()
	defer stdinReaderMu.Unlock()
	if stdinReader == nil || stdinSource != os.Stdin {
		stdinReader = bufio.NewReader(os.Stdin)
		stdinSource = os.Stdin
	}
	return stdinReader
}

func Prompt(message string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s ", InfoStyle.Render(message))

	reader := getStdinReader()
	input, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(input), nil
}

// PromptSecret disables echo before displaying the secret prompt.
func PromptSecret(message string) (string, error) {
	stdinFD := int(os.Stdin.Fd())
	if !term.IsTerminal(stdinFD) {
		return "", fmt.Errorf("secret prompt requires an interactive terminal")
	}
	state, err := term.MakeRaw(stdinFD)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "%s ", InfoStyle.Render(message))
	value, err := term.ReadPassword(stdinFD)
	restoreErr := term.Restore(stdinFD, state)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if restoreErr != nil {
		return "", restoreErr
	}
	return strings.TrimSuffix(strings.TrimSuffix(string(value), "\n"), "\r"), nil
}

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
		programOptions = append(programOptions, tea.WithOutput(os.Stderr))
	}

	time.Sleep(50 * time.Millisecond)
	drainStdin()

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

func promptSelectFallback(message string, options []string) (int, error) {
	fmt.Fprintln(os.Stderr, InfoStyle.Render(message))

	for i, opt := range options {
		fmt.Fprintf(os.Stderr, "    %s %s\n", AccentStyle.Render(fmt.Sprintf("[%d]", i+1)), InfoStyle.Render(opt))
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

func PromptSelect(message string, options []string) (int, error) {
	if !canRunInteractiveSelect() {
		return promptSelectFallback(message, options)
	}

	return runSelectTea(message, options, nil, 0)
}

type SelectOption struct {
	Label       string
	Value       string
	Description string
}

func selectFallback(message string, options []SelectOption, current int) (int, string, error) {
	fmt.Fprintln(os.Stderr, InfoStyle.Render(message))
	for i, opt := range options {
		number := AccentStyle.Render(fmt.Sprintf("[%d]", i+1))
		if i == current {
			marker := AccentStyle.Render(">")
			label := TitleStyle.Render(opt.Label)
			fmt.Fprintf(os.Stderr, "  %s %s %s\n", marker, number, label)
		} else {
			label := InfoStyle.Render(opt.Label)
			fmt.Fprintf(os.Stderr, "    %s %s\n", number, label)
		}
		if opt.Description != "" {
			fmt.Fprintf(os.Stderr, "      %s\n", DimStyle.Render(opt.Description))
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

// Select prompts the user to choose from options, returning the chosen index and value.
// Out-of-range defaultIndex values clamp to 0.
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

func IsInteractive() bool {
	return canRunInteractiveSelect()
}

// Confirm prompts for yes/no confirmation defaulting to yes, returning false on error.
func Confirm(message string) bool {
	result, err := PromptConfirm(message, true)
	if err != nil {
		return false
	}
	return result
}
