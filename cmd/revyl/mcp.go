// Package main provides the MCP command for the Revyl CLI.
package main

import (
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/ui"
)

// mcpCmd is the parent command for MCP operations.
var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "MCP server commands",
	Long: `MCP (Model Context Protocol) server commands.

The MCP server allows AI agents to interact with Revyl through
the Model Context Protocol, enabling automated test execution
and management via AI assistants like Claude or Cursor.

Commands:
  serve  - Start the MCP server over stdio`,
}

// mcpServeCmd starts the MCP server.
var mcpServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start MCP server over stdio",
	Long: `Start the Revyl MCP server over stdio.

This command starts an MCP server that communicates via JSON-RPC
over stdin/stdout. It's designed to be launched by AI hosts like
Cursor or Claude Desktop.

PROFILES:

  --profile core (default)
    ~10 composite tools: device session/actions, test management,
    build management, dev loop, schema, auth. Enough for the
    dev-loop and test-creation journey.

  --profile full
    ~16 composite tools: adds workflow, module, script, tag, file,
    and variable management.

  (no --profile flag)
    Legacy mode: all ~95 individual tools registered flat.
    Kept for backward compatibility; not recommended for new setups.

Authentication:
  Set REVYL_API_KEY environment variable, or run 'revyl auth login' first.

EXAMPLE CURSOR CONFIGURATION:
  {
    "mcpServers": {
      "revyl": {
        "command": "revyl",
        "args": ["mcp", "serve", "--profile", "core"],
        "env": {
          "REVYL_API_KEY": "your-api-key"
        }
      }
    }
  }`,
	Example: `  revyl mcp serve
  revyl --dev mcp serve`,
	RunE: runMCPServe,
}

func init() {
	mcpCmd.AddCommand(mcpServeCmd)
	mcpServeCmd.Flags().String("profile", "", "Tool profile: 'core' (~10 tools, recommended) or 'full' (~16 tools). Omit for legacy flat mode.")
}

// runMCPServe starts the MCP server.
func runMCPServe(cmd *cobra.Command, args []string) error {
	devMode, _ := cmd.Flags().GetBool("dev")
	profileStr, _ := cmd.Flags().GetString("profile")

	var opts []mcp.ServerOption
	switch profileStr {
	case "core":
		opts = append(opts, mcp.WithProfile(mcp.ProfileCore))
	case "full":
		opts = append(opts, mcp.WithProfile(mcp.ProfileFull))
	case "":
		// Legacy flat mode — no profile option
	default:
		ui.PrintError("Unknown profile %q; valid values: core, full", profileStr)
		return nil
	}

	server, err := mcp.NewServer(version, devMode, opts...)
	if err != nil {
		ui.PrintError("Failed to create MCP server: %v", err)
		return err
	}

	// Set the root command for schema generation
	server.SetRootCmd(cmd.Root())

	// Run the server (blocks until client disconnects)
	return server.Run(cmd.Context())
}
