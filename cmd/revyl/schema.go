// Package main provides the schema command for CLI introspection.
package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/schema"
)

var schemaFormat string

// schemaCmd outputs CLI schema for LLM/tooling integration.
var schemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Output CLI schema for LLM/tooling integration",
	Long: `Output a machine-readable schema of all CLI commands.

This command introspects the CLI and outputs structured documentation
that LLMs and other tools can use to understand how to use the CLI.

FORMATS:
  json     - Full JSON schema with commands, flags, examples (default)
  markdown - Markdown documentation suitable for docs sites
  llm      - Single-file format optimized for LLM context windows

The schema includes:
  - All CLI commands with their flags and examples
  - Common workflows for typical use cases
  - Complete YAML test schema for generating tests

EXAMPLES:
  revyl schema                    # JSON to stdout
  revyl schema --format markdown  # Markdown docs
  revyl schema --format llm       # LLM-optimized single file
  revyl schema > cli-schema.json  # Save to file`,
	RunE: runSchema,
}

func init() {
	schemaCmd.Flags().StringVar(&schemaFormat, "format", "json", "Output format: json, markdown, llm")
}

// runSchema generates and outputs the CLI schema.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (unused)
//
// Returns:
//   - error: Any error that occurred
func runSchema(cmd *cobra.Command, args []string) error {
	// Get the root command to introspect
	root := cmd.Root()

	// Generate CLI schema
	cliSchema := schema.GetCLISchema(root, version)

	switch schemaFormat {
	case "json":
		// Include YAML test schema in JSON output
		output := map[string]interface{}{
			"cli_schema":       cliSchema,
			"yaml_test_schema": schema.YAMLTestSchemaJSON(),
		}
		data, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal schema: %w", err)
		}
		fmt.Println(string(data))

	case "markdown":
		// Output markdown documentation
		md := schema.ToMarkdown(cliSchema)
		fmt.Println(md)
		fmt.Println("---")
		fmt.Println()
		fmt.Println(schema.GetYAMLTestSchema())

	case "llm":
		// Output LLM-optimized format
		llmOutput := schema.ToLLMFormat(cliSchema, schema.GetYAMLTestSchema())
		fmt.Println(llmOutput)

	default:
		return fmt.Errorf("unknown format '%s': must be json, markdown, or llm", schemaFormat)
	}

	return nil
}
