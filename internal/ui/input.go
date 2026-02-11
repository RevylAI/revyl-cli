// Package ui provides interactive input components.
package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
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

// SelectOption represents an option in a select prompt.
type SelectOption struct {
	// Label is the display text for this option.
	Label string

	// Value is the value returned when this option is selected.
	Value string

	// Description is an optional description shown below the label.
	Description string
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
	fmt.Println(InfoStyle.Render(message))

	for i, opt := range options {
		number := AccentStyle.Render(fmt.Sprintf("[%d]", i+1))
		if i == defaultIndex {
			marker := AccentStyle.Render(">")
			label := TitleStyle.Render(opt.Label)
			fmt.Printf("  %s %s %s\n", marker, number, label)
			if opt.Description != "" {
				fmt.Printf("      %s\n", DimStyle.Render(opt.Description))
			}
		} else {
			label := InfoStyle.Render(opt.Label)
			fmt.Printf("    %s %s\n", number, label)
			if opt.Description != "" {
				fmt.Printf("      %s\n", DimStyle.Render(opt.Description))
			}
		}
	}

	defaultPrompt := ""
	if defaultIndex >= 0 && defaultIndex < len(options) {
		defaultPrompt = fmt.Sprintf(" [%d]", defaultIndex+1)
	}

	for {
		input, err := Prompt(fmt.Sprintf("Select option%s:", defaultPrompt))
		if err != nil {
			return -1, "", err
		}

		// Handle empty input with default
		if input == "" && defaultIndex >= 0 && defaultIndex < len(options) {
			return defaultIndex, options[defaultIndex].Value, nil
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
