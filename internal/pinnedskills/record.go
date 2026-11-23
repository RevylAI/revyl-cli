package pinnedskills

import (
	"encoding/json"
	"fmt"
	"strings"
)

type schemaFlag struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

type schemaCommand struct {
	Path        string          `json:"path"`
	Flags       []schemaFlag    `json:"flags"`
	Subcommands []schemaCommand `json:"subcommands"`
}

// CommandTreeText converts the output of a release's
// `revyl schema --format json` into its commands/<release tag>.txt record:
// the global long flags, the feedback --type help, and every command path in
// depth-first order with its long flags. A flag that takes a value ends in "=".
func CommandTreeText(schemaJSON []byte) ([]byte, error) {
	var document struct {
		CLISchema *struct {
			GlobalFlags []schemaFlag    `json:"global_flags"`
			Commands    []schemaCommand `json:"commands"`
		} `json:"cli_schema"`
	}
	if err := json.Unmarshal(schemaJSON, &document); err != nil {
		return nil, fmt.Errorf("read revyl schema output: %w", err)
	}
	if document.CLISchema == nil || len(document.CLISchema.Commands) == 0 {
		return nil, fmt.Errorf("revyl schema output has no cli_schema commands")
	}
	var text strings.Builder
	text.WriteString("global" + longFlags(document.CLISchema.GlobalFlags) + "\n")
	feedbackTypeHelp := ""
	for _, command := range document.CLISchema.Commands {
		if command.Path != "feedback" {
			continue
		}
		for _, flag := range command.Flags {
			if flag.Name == "type" {
				feedbackTypeHelp = flag.Description
				break
			}
		}
		break
	}
	text.WriteString("feedback-type " + feedbackTypeHelp + "\n")
	var writeCommands func([]schemaCommand)
	writeCommands = func(commands []schemaCommand) {
		for _, command := range commands {
			text.WriteString("command " + command.Path + longFlags(command.Flags) + "\n")
			writeCommands(command.Subcommands)
		}
	}
	writeCommands(document.CLISchema.Commands)
	return []byte(text.String()), nil
}

func longFlags(flags []schemaFlag) string {
	var text strings.Builder
	for _, flag := range flags {
		text.WriteString(" --" + flag.Name)
		if flag.Type != "bool" {
			text.WriteString("=")
		}
	}
	return text.String()
}
