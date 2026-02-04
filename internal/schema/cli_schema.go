// Package schema provides CLI and YAML test schema generation.
//
// This package generates machine-readable schema documentation for the CLI
// and YAML test definitions, enabling LLMs and other tools to understand
// how to use the Revyl CLI.
package schema

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// CLISchema represents the complete CLI schema.
type CLISchema struct {
	Name        string        `json:"name"`
	Version     string        `json:"version"`
	Description string        `json:"description"`
	Commands    []CommandInfo `json:"commands"`
	GlobalFlags []FlagInfo    `json:"global_flags"`
	Workflows   []Workflow    `json:"workflows"`
}

// CommandInfo represents a CLI command.
type CommandInfo struct {
	Path        string        `json:"path"`
	Short       string        `json:"short"`
	Long        string        `json:"long,omitempty"`
	Usage       string        `json:"usage"`
	Examples    []string      `json:"examples,omitempty"`
	Flags       []FlagInfo    `json:"flags,omitempty"`
	Subcommands []CommandInfo `json:"subcommands,omitempty"`
}

// FlagInfo represents a CLI flag.
type FlagInfo struct {
	Name        string `json:"name"`
	Shorthand   string `json:"shorthand,omitempty"`
	Type        string `json:"type"`
	Default     string `json:"default,omitempty"`
	Description string `json:"description"`
}

// Workflow represents a common CLI workflow.
type Workflow struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Steps       []string `json:"steps"`
}

// GetCLISchema generates the CLI schema from a root Cobra command.
//
// Parameters:
//   - rootCmd: The root Cobra command
//   - version: CLI version string
//
// Returns:
//   - *CLISchema: The generated CLI schema
func GetCLISchema(rootCmd *cobra.Command, version string) *CLISchema {
	schema := &CLISchema{
		Name:        "revyl",
		Version:     version,
		Description: "Proactive reliability for mobile apps. Catch bugs before your users do.",
		Commands:    extractCommands(rootCmd, ""),
		GlobalFlags: extractFlags(rootCmd.PersistentFlags()),
		Workflows:   getCommonWorkflows(),
	}
	return schema
}

// extractCommands recursively extracts command information.
func extractCommands(cmd *cobra.Command, parentPath string) []CommandInfo {
	var commands []CommandInfo

	for _, subCmd := range cmd.Commands() {
		// Skip help and completion commands
		if subCmd.Name() == "help" || subCmd.Name() == "completion" {
			continue
		}

		path := subCmd.Name()
		if parentPath != "" {
			path = parentPath + " " + subCmd.Name()
		}

		info := CommandInfo{
			Path:     path,
			Short:    subCmd.Short,
			Long:     subCmd.Long,
			Usage:    subCmd.UseLine(),
			Examples: extractExamples(subCmd.Example),
			Flags:    extractFlags(subCmd.LocalFlags()),
		}

		// Recursively get subcommands
		if subCmd.HasSubCommands() {
			info.Subcommands = extractCommands(subCmd, path)
		}

		commands = append(commands, info)
	}

	return commands
}

// extractFlags extracts flag information from a FlagSet.
func extractFlags(flags *pflag.FlagSet) []FlagInfo {
	var flagInfos []FlagInfo

	flags.VisitAll(func(f *pflag.Flag) {
		// Skip hidden flags
		if f.Hidden {
			return
		}

		info := FlagInfo{
			Name:        f.Name,
			Shorthand:   f.Shorthand,
			Type:        f.Value.Type(),
			Default:     f.DefValue,
			Description: f.Usage,
		}
		flagInfos = append(flagInfos, info)
	})

	return flagInfos
}

// extractExamples parses the Example field into individual examples.
func extractExamples(example string) []string {
	if example == "" {
		return nil
	}

	var examples []string
	lines := strings.Split(example, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			examples = append(examples, line)
		}
	}
	return examples
}

// getCommonWorkflows returns common CLI workflows.
func getCommonWorkflows() []Workflow {
	return []Workflow{
		{
			Name:        "First-time setup",
			Description: "Set up Revyl for a new project",
			Steps: []string{
				"revyl auth login",
				"revyl init",
				"revyl create test <name>",
			},
		},
		{
			Name:        "Run existing test",
			Description: "Build, upload, and run a test",
			Steps: []string{
				"revyl test <name>",
			},
		},
		{
			Name:        "Run without building",
			Description: "Run a test using existing build",
			Steps: []string{
				"revyl run test <name>",
			},
		},
		{
			Name:        "CI/CD integration",
			Description: "Run tests in CI with JSON output",
			Steps: []string{
				"revyl run test <name> --output",
			},
		},
		{
			Name:        "Validate YAML tests",
			Description: "Check YAML syntax before committing",
			Steps: []string{
				"revyl tests validate tests/*.yaml",
			},
		},
		{
			Name:        "MCP server for AI agents",
			Description: "Start MCP server for AI integration",
			Steps: []string{
				"revyl mcp serve",
			},
		},
	}
}

// ToJSON converts the schema to JSON.
//
// Parameters:
//   - schema: The CLI schema to convert
//   - indent: Whether to indent the output
//
// Returns:
//   - string: JSON representation
//   - error: Any encoding error
func ToJSON(schema *CLISchema, indent bool) (string, error) {
	var data []byte
	var err error

	if indent {
		data, err = json.MarshalIndent(schema, "", "  ")
	} else {
		data, err = json.Marshal(schema)
	}

	if err != nil {
		return "", fmt.Errorf("failed to marshal schema: %w", err)
	}
	return string(data), nil
}

// ToMarkdown converts the schema to Markdown documentation.
//
// Parameters:
//   - schema: The CLI schema to convert
//
// Returns:
//   - string: Markdown documentation
func ToMarkdown(schema *CLISchema) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# %s CLI Reference\n\n", schema.Name))
	sb.WriteString(fmt.Sprintf("**Version:** %s\n\n", schema.Version))
	sb.WriteString(fmt.Sprintf("%s\n\n", schema.Description))

	// Global flags
	sb.WriteString("## Global Flags\n\n")
	sb.WriteString("| Flag | Type | Default | Description |\n")
	sb.WriteString("|------|------|---------|-------------|\n")
	for _, f := range schema.GlobalFlags {
		name := "--" + f.Name
		if f.Shorthand != "" {
			name = "-" + f.Shorthand + ", " + name
		}
		sb.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s |\n", name, f.Type, f.Default, f.Description))
	}
	sb.WriteString("\n")

	// Commands
	sb.WriteString("## Commands\n\n")
	for _, cmd := range schema.Commands {
		writeCommandMarkdown(&sb, cmd, 3)
	}

	// Workflows
	sb.WriteString("## Common Workflows\n\n")
	for _, w := range schema.Workflows {
		sb.WriteString(fmt.Sprintf("### %s\n\n", w.Name))
		if w.Description != "" {
			sb.WriteString(fmt.Sprintf("%s\n\n", w.Description))
		}
		sb.WriteString("```bash\n")
		for _, step := range w.Steps {
			sb.WriteString(step + "\n")
		}
		sb.WriteString("```\n\n")
	}

	return sb.String()
}

// writeCommandMarkdown writes a command to markdown.
func writeCommandMarkdown(sb *strings.Builder, cmd CommandInfo, level int) {
	heading := strings.Repeat("#", level)
	sb.WriteString(fmt.Sprintf("%s `%s`\n\n", heading, cmd.Path))
	sb.WriteString(fmt.Sprintf("%s\n\n", cmd.Short))

	if cmd.Long != "" {
		sb.WriteString(fmt.Sprintf("%s\n\n", cmd.Long))
	}

	sb.WriteString(fmt.Sprintf("**Usage:** `%s`\n\n", cmd.Usage))

	if len(cmd.Flags) > 0 {
		sb.WriteString("**Flags:**\n\n")
		sb.WriteString("| Flag | Type | Default | Description |\n")
		sb.WriteString("|------|------|---------|-------------|\n")
		for _, f := range cmd.Flags {
			name := "--" + f.Name
			if f.Shorthand != "" {
				name = "-" + f.Shorthand + ", " + name
			}
			sb.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s |\n", name, f.Type, f.Default, f.Description))
		}
		sb.WriteString("\n")
	}

	if len(cmd.Examples) > 0 {
		sb.WriteString("**Examples:**\n\n```bash\n")
		for _, ex := range cmd.Examples {
			sb.WriteString(ex + "\n")
		}
		sb.WriteString("```\n\n")
	}

	// Subcommands
	for _, sub := range cmd.Subcommands {
		writeCommandMarkdown(sb, sub, level+1)
	}
}

// ToLLMFormat converts the schema to an LLM-optimized single-file format.
//
// Parameters:
//   - schema: The CLI schema to convert
//   - yamlSchema: The YAML test schema documentation
//
// Returns:
//   - string: LLM-optimized documentation
func ToLLMFormat(schema *CLISchema, yamlSchema string) string {
	var sb strings.Builder

	sb.WriteString("# Revyl CLI - Complete Reference for LLMs\n\n")
	sb.WriteString("This document contains everything needed to use the Revyl CLI and generate YAML tests.\n\n")

	// Quick reference
	sb.WriteString("## Quick Reference\n\n")
	sb.WriteString("```\n")
	sb.WriteString("revyl auth login          # Authenticate\n")
	sb.WriteString("revyl init                # Initialize project\n")
	sb.WriteString("revyl create test <name>  # Create new test\n")
	sb.WriteString("revyl test <name>         # Build and run test\n")
	sb.WriteString("revyl run test <name>     # Run without building\n")
	sb.WriteString("revyl tests validate <f>  # Validate YAML\n")
	sb.WriteString("revyl schema              # Get this schema\n")
	sb.WriteString("```\n\n")

	// CLI Commands section
	sb.WriteString("## CLI Commands\n\n")
	for _, cmd := range schema.Commands {
		writeLLMCommand(&sb, cmd)
	}

	// YAML Test Schema section
	sb.WriteString("---\n\n")
	sb.WriteString(yamlSchema)

	return sb.String()
}

// writeLLMCommand writes a command in LLM-friendly format.
func writeLLMCommand(sb *strings.Builder, cmd CommandInfo) {
	sb.WriteString(fmt.Sprintf("### %s\n\n", cmd.Path))
	sb.WriteString(fmt.Sprintf("%s\n\n", cmd.Short))

	if cmd.Long != "" {
		sb.WriteString(fmt.Sprintf("%s\n\n", cmd.Long))
	}

	if len(cmd.Flags) > 0 {
		sb.WriteString("Flags:\n")
		for _, f := range cmd.Flags {
			name := "--" + f.Name
			if f.Shorthand != "" {
				name = "-" + f.Shorthand + "/" + name
			}
			sb.WriteString(fmt.Sprintf("  %s (%s): %s\n", name, f.Type, f.Description))
		}
		sb.WriteString("\n")
	}

	if len(cmd.Examples) > 0 {
		sb.WriteString("Examples:\n")
		for _, ex := range cmd.Examples {
			sb.WriteString(fmt.Sprintf("  %s\n", ex))
		}
		sb.WriteString("\n")
	}

	// Subcommands
	for _, sub := range cmd.Subcommands {
		writeLLMCommand(sb, sub)
	}
}
