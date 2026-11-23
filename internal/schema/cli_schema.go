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

	"github.com/revyl/cli/internal/build"
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
		Description: "Proactive reliability for your apps. Catch bugs before your users do.",
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
			Name:        "Check authentication (do this first!)",
			Description: "Verify you are authenticated before running tests",
			Steps: []string{
				"revyl auth status",
				"# If not authenticated:",
				"revyl auth login",
				"# Or set environment variable:",
				"export REVYL_API_KEY=your-api-key",
			},
		},
		{
			Name:        "First-time setup",
			Description: "Set up Revyl for a new project",
			Steps: []string{
				"revyl auth login",
				"# Run init from the app's own folder, not a monorepo root:",
				"revyl init -y",
				"# A test needs an app. Find the app's id, or create the app:",
				"revyl app list --platform <ios|android|web> --json",
				"revyl app create --name <app-name> --platform <ios|android|web> --json",
				"revyl test create <name> --platform <ios|android|web> --app <app-id>",
				"revyl test create --from-session <session-id> <name> --app <app-id>",
			},
		},
		{
			Name:        "List available tests",
			Description: "See what tests are available to run",
			Steps: []string{
				"revyl test list",
				"# Or see all tests in your organization:",
				"revyl test remote",
			},
		},
		{
			Name:        "Build and run single test",
			Description: "Build, upload, and run a test (full pipeline)",
			Steps: []string{
				"# Use the test NAME (its file name under .revyl/tests), not the file path:",
				"revyl test run <name> --build",
				"# With a specific build platform or profile:",
				"revyl test run <name> --build --platform android",
				"revyl test run <name> --build --build-profile development --platform ios",
			},
		},
		{
			Name:        "Run test with specific build profile",
			Description: "Use a named build profile from config",
			Steps: []string{
				"# Build profiles are listed in .revyl/config.yaml under build.profiles",
				"revyl test run login-flow --build --build-profile development --platform android",
				"revyl workflow run smoke-tests --build --build-profile development --platform android",
			},
		},
		{
			Name:        "Build and run workflow",
			Description: "Build, upload, and run a workflow (multiple tests)",
			Steps: []string{
				"revyl workflow run <name>",
				"# Or with a specific build platform:",
				"revyl workflow run <name> --build --platform android",
			},
		},
		{
			Name:        "Run test without building",
			Description: "Run a test using existing build (no build step)",
			Steps: []string{
				"# Use the test NAME (its file name under .revyl/tests), not the file path:",
				"revyl test run <name>",
			},
		},
		{
			Name:        "Run workflow without building",
			Description: "Run a workflow using existing build",
			Steps: []string{
				"revyl workflow run <name>",
			},
		},
		{
			Name:        "Run workflow with optional build",
			Description: "Run a workflow, optionally building first",
			Steps: []string{
				"# Without build:",
				"revyl workflow run <name>",
				"# With build:",
				"revyl workflow run <name> --build --platform android",
			},
		},
		{
			Name:        "CI/CD integration",
			Description: "Run tests in CI with JSON output",
			Steps: []string{
				"revyl test run <name> --json",
				"# Or for workflows:",
				"revyl workflow run <name> --json",
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

// ExampleProjectConfig is the .revyl/config.yaml example the LLM reference
// teaches. It must pass revyl config validate. Its iOS recipe uses the
// xcodebuild simulator flags revyl init writes.
var ExampleProjectConfig = `project:
  id: "11111111-1111-4111-8111-111111111111"
build:
  framework: react_native
  profiles:
    development:
      android:
        app_id: "22222222-2222-4222-8222-222222222222"
        build_commands:
          - cd android && ./gradlew assembleDebug
        output_path: android/app/build/outputs/apk/debug/app-debug.apk
      ios:
        app_id: "33333333-3333-4333-8333-333333333333"
        build_commands:
          - ` + build.XcodebuildCommand("-workspace ios/App.xcworkspace -scheme App", "Debug") + `
        output_path: build/Build/Products/Debug-iphonesimulator/App.app
`

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

	// CRITICAL: Prerequisites section - check these FIRST
	sb.WriteString("## Prerequisites (Check First!)\n\n")
	sb.WriteString("Before running any test commands, you MUST:\n\n")
	sb.WriteString("1. **Authenticate**: `revyl auth login` OR set `REVYL_API_KEY` environment variable\n")
	sb.WriteString("2. **Check status**: `revyl auth status` (verify authentication works)\n")
	sb.WriteString("3. **Initialize project**: `revyl init -y` from the app's own folder, not a monorepo root (creates .revyl/config.yaml there)\n\n")
	sb.WriteString("If you get 'REVYL_API_KEY not found', run `revyl auth login` first.\n\n")

	// Key Concepts section - critical for understanding
	sb.WriteString("## Key Concepts (Important!)\n\n")

	sb.WriteString("### Test Names vs YAML Files vs UUIDs\n\n")
	sb.WriteString("- **YAML files** (`.revyl/tests/*.yaml`) define test steps - they are NOT passed directly to `run test`\n")
	sb.WriteString("- **Test names** are the file names under `.revyl/tests/`, without `.yaml`\n")
	sb.WriteString("- **UUIDs** are the actual test IDs on the Revyl server\n")
	sb.WriteString("- `revyl test run <name>` uses the test NAME, NOT a file path\n\n")

	sb.WriteString("To run a test: `revyl test run login-flow` (the file is `.revyl/tests/login-flow.yaml`; use the name, not the file path)\n\n")

	// Build Profiles section
	sb.WriteString("### Build Profiles\n\n")
	sb.WriteString("Build recipes are defined in `.revyl/config.yaml` under `build.profiles.<profile>.<platform>`, where the platform is ios, android, or web. Example:\n\n")
	sb.WriteString("```yaml\n")
	sb.WriteString(ExampleProjectConfig)
	sb.WriteString("```\n\n")
	sb.WriteString("To build with a profile: `revyl test run <name> --build --build-profile development --platform android`\n\n")

	// Common Mistakes section - prevent errors
	sb.WriteString("## Common Mistakes (Don't Do This!)\n\n")
	sb.WriteString("| Wrong | Correct | Why |\n")
	sb.WriteString("|-------|---------|-----|\n")
	sb.WriteString("| `revyl test run experiments.yaml` | `revyl test run experiments` | Use the test NAME, not the file path |\n")
	sb.WriteString("| `revyl test run .revyl/tests/login.yaml` | `revyl test run login` | Use the test NAME, not the file path |\n")
	sb.WriteString("| Putting a build profile or platform in the test YAML | Pass `--build-profile` and `--platform` with `--build` | Build profiles live in .revyl/config.yaml; test YAML `build.name` is the app name |\n")
	sb.WriteString("| Running tests before `revyl auth login` | Run `revyl auth login` first | Authentication is required |\n")
	sb.WriteString("| Pressing Ctrl+C to cancel test | `revyl test cancel <task_id>` | Ctrl+C only stops monitoring, not the test |\n\n")

	// Quick reference
	sb.WriteString("## Quick Reference\n\n")
	sb.WriteString("```\n")
	sb.WriteString("revyl auth login              # Authenticate (do this first!)\n")
	sb.WriteString("revyl auth status             # Check authentication\n")
	sb.WriteString("revyl init -y                 # Initialize project, from the app's own folder, not a monorepo root\n")
	sb.WriteString("revyl app list --json         # Find an app id for tests and builds\n")
	sb.WriteString("revyl app create --name <app-name> --platform <ios|android|web> --json # Create an app\n")
	sb.WriteString("revyl test create <name> --platform <ios|android|web> --app <app-id> # Create new test\n")
	sb.WriteString("revyl test create --from-session <session-id> <name> --app <app-id> # Convert session to test\n")
	sb.WriteString("revyl test rename <old> <new> # Rename test without recreating\n")
	sb.WriteString("revyl workflow rename <old> <new> # Rename workflow without recreating\n")
	sb.WriteString("revyl test run <name>         # Run a test\n")
	sb.WriteString("revyl test run <name> --build  # Build then run a test\n")
	sb.WriteString("revyl test run <name> --build --build-profile X --platform Y # Build a profile's recipe, then run\n")
	sb.WriteString("revyl workflow run <name>     # Run a workflow\n")
	sb.WriteString("revyl workflow run <name> --build # Build then run a workflow\n")
	sb.WriteString("revyl test cancel <task_id>   # Cancel a running test\n")
	sb.WriteString("revyl workflow cancel <id>    # Cancel a running workflow\n")
	sb.WriteString("revyl test list               # List available tests\n")
	sb.WriteString("revyl schema                  # Get this schema\n")
	sb.WriteString("```\n\n")

	// Command comparison
	sb.WriteString("## Command Comparison\n\n")
	sb.WriteString("| Command | Builds? | Runs |\n")
	sb.WriteString("|---------|---------|------|\n")
	sb.WriteString("| `revyl test run <name>` | No | Single test |\n")
	sb.WriteString("| `revyl test run <name> --build` | Yes | Single test |\n")
	sb.WriteString("| `revyl test run <name> --build --platform X` | Yes (platform X) | Single test |\n")
	sb.WriteString("| `revyl workflow run <name>` | No | Workflow (multiple tests) |\n")
	sb.WriteString("| `revyl workflow run <name> --build` | Yes | Workflow |\n")
	sb.WriteString("\n")

	// Early validation note
	sb.WriteString("## Early Validation\n\n")
	sb.WriteString("The CLI validates test/workflow existence BEFORE starting expensive build operations.\n")
	sb.WriteString("If a test or workflow doesn't exist, you'll get an immediate error with available options.\n\n")

	// Cancelling tests section
	sb.WriteString("## Cancelling Running Tests\n\n")
	sb.WriteString("Use `revyl test cancel` / `revyl workflow cancel` to stop a running test or workflow:\n\n")
	sb.WriteString("```\n")
	sb.WriteString("revyl test cancel <task_id>       # Cancel a running test\n")
	sb.WriteString("revyl workflow cancel <task_id>   # Cancel a running workflow (and all its tests)\n")
	sb.WriteString("```\n\n")
	sb.WriteString("**Where to find the task ID:**\n")
	sb.WriteString("- CLI output when starting: `Task ID: abc123-def456...`\n")
	sb.WriteString("- Report URL: `https://app.revyl.ai/tests/report?taskId=abc123...`\n\n")
	sb.WriteString("**Notes:**\n")
	sb.WriteString("- If the test/workflow has already completed, failed, or been cancelled, you'll get an error with the current status\n")
	sb.WriteString("- Pressing Ctrl+C only stops the CLI from monitoring - use `revyl test cancel` / `revyl workflow cancel` to actually stop the test on the server\n\n")

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
