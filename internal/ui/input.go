// Package ui provides interactive input components.
package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
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

// PromptPassword displays a prompt and reads password input (hidden).
//
// Parameters:
//   - message: The prompt message to display
//
// Returns:
//   - string: The user's input
//   - error: Any error that occurred
func PromptPassword(message string) (string, error) {
	fmt.Printf("%s ", InfoStyle.Render(message))

	// Read password without echo
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println() // New line after password input

	if err != nil {
		// Fallback to regular input if terminal not available
		return Prompt("")
	}

	return string(password), nil
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
		fmt.Printf("  %s %s\n", DimStyle.Render(fmt.Sprintf("[%d]", i+1)), opt)
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
