// Package interactive provides the interactive test creation mode for the CLI.
//
// This file contains the command parser that maps user input to step types.
package interactive

import (
	"fmt"
	"strings"
)

// CommandType represents the type of command parsed from user input.
type CommandType string

const (
	// CommandInstruction is a natural language instruction step.
	CommandInstruction CommandType = "instruction"

	// CommandValidation is a validation/assertion step.
	CommandValidation CommandType = "validation"

	// CommandWait is a wait/delay step.
	CommandWait CommandType = "wait"

	// CommandNavigate is a navigation step (deep link or URL).
	CommandNavigate CommandType = "navigate"

	// CommandBack is a back button press.
	CommandBack CommandType = "back"

	// CommandHome is a home button press.
	CommandHome CommandType = "go_home"

	// CommandOpenApp is an app launch step.
	CommandOpenApp CommandType = "open_app"

	// CommandKillApp is an app termination step.
	CommandKillApp CommandType = "kill_app"

	// CommandUndo removes the last step.
	CommandUndo CommandType = "undo"

	// CommandSave exports the test to YAML.
	CommandSave CommandType = "save"

	// CommandHelp shows help information.
	CommandHelp CommandType = "help"

	// CommandReplay re-executes a previous step.
	CommandReplay CommandType = "replay"

	// CommandRun executes all steps from the beginning.
	CommandRun CommandType = "run"

	// CommandQuit exits the interactive session.
	CommandQuit CommandType = "quit"

	// CommandStatus shows the current session status.
	CommandStatus CommandType = "status"

	// CommandList lists all recorded steps.
	CommandList CommandType = "list"

	// CommandClear clears all recorded steps.
	CommandClear CommandType = "clear"
)

// ParsedCommand represents a parsed user command.
type ParsedCommand struct {
	// Type is the command type.
	Type CommandType

	// Instruction is the instruction text (for instruction/validation steps).
	Instruction string

	// Args contains additional arguments for the command.
	Args []string

	// Raw is the original input string.
	Raw string
}

// reservedCommands maps command keywords to their types.
var reservedCommands = map[string]CommandType{
	"validate": CommandValidation,
	"assert":   CommandValidation,
	"wait":     CommandWait,
	"back":     CommandBack,
	"navigate": CommandNavigate,
	"home":     CommandHome,
	"open-app": CommandOpenApp,
	"openapp":  CommandOpenApp,
	"kill-app": CommandKillApp,
	"killapp":  CommandKillApp,
	"undo":     CommandUndo,
	"save":     CommandSave,
	"help":     CommandHelp,
	"?":        CommandHelp,
	"replay":   CommandReplay,
	"run":      CommandRun,
	"quit":     CommandQuit,
	"exit":     CommandQuit,
	"q":        CommandQuit,
	"status":   CommandStatus,
	"list":     CommandList,
	"ls":       CommandList,
	"clear":    CommandClear,
}

// stepTypeCommands are commands that create steps (vs. session commands).
var stepTypeCommands = map[CommandType]bool{
	CommandInstruction: true,
	CommandValidation:  true,
	CommandWait:        true,
	CommandNavigate:    true,
	CommandBack:        true,
	CommandHome:        true,
	CommandOpenApp:     true,
	CommandKillApp:     true,
}

// ParseCommand parses user input into a command.
//
// The parsing logic follows these rules:
// 1. If the first word is a reserved command, parse as that command type
// 2. Otherwise, treat the entire input as an instruction step
//
// Parameters:
//   - input: The raw user input string
//
// Returns:
//   - *ParsedCommand: The parsed command
//   - error: Any parsing error
func ParseCommand(input string) (*ParsedCommand, error) {
	return ParseCommandWithDefaults(input, "")
}

// ParseCommandWithDefaults parses user input into a command with optional defaults.
//
// This function extends ParseCommand by allowing default values for certain commands.
// For example, when hotReloadURL is provided, the 'navigate' command without arguments
// will use that URL as the default destination.
//
// Parameters:
//   - input: The raw user input string
//   - hotReloadURL: Optional default URL for the navigate command (empty string to disable)
//
// Returns:
//   - *ParsedCommand: The parsed command
//   - error: Any parsing error
func ParseCommandWithDefaults(input string, hotReloadURL string) (*ParsedCommand, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, fmt.Errorf("empty input")
	}

	// Split into words
	words := strings.Fields(input)
	if len(words) == 0 {
		return nil, fmt.Errorf("empty input")
	}

	firstWord := strings.ToLower(words[0])

	// Check if first word is a reserved command
	if cmdType, ok := reservedCommands[firstWord]; ok {
		return parseReservedCommandWithDefaults(cmdType, words[1:], input, hotReloadURL)
	}

	// Default: treat entire input as instruction
	return &ParsedCommand{
		Type:        CommandInstruction,
		Instruction: input,
		Raw:         input,
	}, nil
}

// parseReservedCommandWithDefaults parses a reserved command with its arguments and optional defaults.
func parseReservedCommandWithDefaults(cmdType CommandType, args []string, raw string, hotReloadURL string) (*ParsedCommand, error) {
	cmd := &ParsedCommand{
		Type: cmdType,
		Args: args,
		Raw:  raw,
	}

	switch cmdType {
	case CommandValidation:
		// validate <assertion text>
		if len(args) == 0 {
			return nil, fmt.Errorf("validate requires an assertion (e.g., 'validate Welcome message is visible')")
		}
		cmd.Instruction = strings.Join(args, " ")

	case CommandWait:
		// wait <duration> or wait <condition>
		if len(args) == 0 {
			return nil, fmt.Errorf("wait requires a duration or condition (e.g., 'wait 3s' or 'wait for loading to complete')")
		}
		cmd.Instruction = strings.Join(args, " ")

	case CommandNavigate:
		// navigate [url or deep link]
		// If no URL provided and hot reload URL is available, use that as default
		if len(args) == 0 {
			if hotReloadURL != "" {
				cmd.Instruction = hotReloadURL
			} else {
				return nil, fmt.Errorf("navigate requires a URL or deep link (e.g., 'navigate myapp://home')")
			}
		} else {
			cmd.Instruction = strings.Join(args, " ")
		}

	case CommandOpenApp:
		// open-app <bundle_id>
		if len(args) == 0 {
			return nil, fmt.Errorf("open-app requires a bundle ID (e.g., 'open-app com.example.app')")
		}
		cmd.Instruction = args[0]

	case CommandKillApp:
		// kill-app [bundle_id] - optional, defaults to current app
		if len(args) > 0 {
			cmd.Instruction = args[0]
		}

	case CommandReplay:
		// replay [step_index]
		if len(args) > 0 {
			cmd.Instruction = args[0]
		}

	case CommandBack, CommandHome, CommandUndo, CommandSave, CommandHelp,
		CommandRun, CommandQuit, CommandStatus, CommandList, CommandClear:
		// These commands don't require additional arguments
	}

	return cmd, nil
}

// IsStepCommand returns true if the command creates a step.
//
// Parameters:
//   - cmd: The parsed command
//
// Returns:
//   - bool: True if this command creates a step
func IsStepCommand(cmd *ParsedCommand) bool {
	return stepTypeCommands[cmd.Type]
}

// GetStepType returns the step type string for backend API.
//
// Parameters:
//   - cmdType: The command type
//
// Returns:
//   - string: The step type for the backend
func GetStepType(cmdType CommandType) string {
	switch cmdType {
	case CommandInstruction:
		return "instruction"
	case CommandValidation:
		return "validation"
	case CommandWait:
		return "wait"
	case CommandNavigate:
		return "navigate"
	case CommandBack:
		return "back"
	case CommandHome:
		return "go_home"
	case CommandOpenApp:
		return "open_app"
	case CommandKillApp:
		return "kill_app"
	default:
		return string(cmdType)
	}
}

// GetBlockType returns the block type for a given command type.
//
// Block types are the high-level categories used by the backend schema:
//   - "instructions": Regular action steps (tap, type, swipe, etc.)
//   - "manual": System-level actions (navigate, wait, open_app, etc.)
//   - "validation": Assertion/verification steps
//
// Parameters:
//   - cmdType: The command type
//
// Returns:
//   - string: The block type for the backend
func GetBlockType(cmdType CommandType) string {
	switch cmdType {
	case CommandNavigate, CommandWait, CommandBack, CommandHome, CommandOpenApp, CommandKillApp:
		return "manual"
	case CommandValidation:
		return "validation"
	default:
		return "instructions"
	}
}

// StepTypeToCommandType converts a step type string back to a CommandType.
//
// This is used when replaying steps from recorded history.
//
// Parameters:
//   - stepType: The step type string (e.g., "navigate", "instruction")
//
// Returns:
//   - CommandType: The corresponding command type
func StepTypeToCommandType(stepType string) CommandType {
	switch stepType {
	case "instruction":
		return CommandInstruction
	case "validation":
		return CommandValidation
	case "wait":
		return CommandWait
	case "navigate":
		return CommandNavigate
	case "back":
		return CommandBack
	case "go_home":
		return CommandHome
	case "open_app":
		return CommandOpenApp
	case "kill_app":
		return CommandKillApp
	default:
		return CommandInstruction
	}
}

// HelpText returns the help text for interactive mode.
//
// Returns:
//   - string: The help text
func HelpText() string {
	return `Interactive Test Creation Commands:

STEP COMMANDS (create test steps):
  <natural language>     Execute an instruction step
                         Example: Tap the Sign In button
                         Example: Type "hello@example.com" in the email field

  validate <assertion>   Add a validation step
                         Example: validate Welcome message is visible
                         Example: validate User is logged in

  wait <duration/cond>   Add a wait step
                         Example: wait 3s
                         Example: wait for loading to complete

  navigate <url>         Navigate to a URL or deep link
                         Example: navigate myapp://settings
                         Example: navigate https://example.com
                         In hot reload mode: just type 'navigate' to open the dev app

  back                   Press the back button
  home                   Press the home button
  open-app <bundle_id>   Launch an app by bundle ID
  kill-app [bundle_id]   Terminate an app (current app if no ID)

SESSION COMMANDS:
  undo                   Remove the last step
  save [filename]        Export test to YAML file
  list                   Show all recorded steps
  status                 Show session status
  clear                  Clear all recorded steps
  replay [index]         Re-execute a step by index
  run                    Execute all steps from beginning
  help, ?                Show this help message
  quit, exit, q          Exit interactive mode

TIPS:
  - Steps are auto-saved to the backend after each execution
  - Use natural language for most interactions
  - The AI will interpret your intent and perform the action
`
}

// CommandSuggestions returns command suggestions for autocomplete.
//
// Parameters:
//   - prefix: The current input prefix
//
// Returns:
//   - []string: List of matching command suggestions
func CommandSuggestions(prefix string) []string {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return nil
	}

	var suggestions []string
	for cmd := range reservedCommands {
		if strings.HasPrefix(cmd, prefix) {
			suggestions = append(suggestions, cmd)
		}
	}

	return suggestions
}
