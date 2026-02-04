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

The server exposes the following tools:
  - run_test: Run a test by name or ID
  - run_workflow: Run a workflow by name or ID
  - list_tests: List available tests from .revyl/config.yaml
  - get_test_status: Get status of a running test

Authentication:
  Set REVYL_API_KEY environment variable, or run 'revyl auth login' first.

Example Cursor configuration:
  {
    "mcpServers": {
      "revyl": {
        "command": "revyl",
        "args": ["mcp", "serve"],
        "env": {
          "REVYL_API_KEY": "your-api-key"
        }
      }
    }
  }`,
	RunE: runMCPServe,
}

func init() {
	mcpCmd.AddCommand(mcpServeCmd)
}

// runMCPServe starts the MCP server.
func runMCPServe(cmd *cobra.Command, args []string) error {
	server, err := mcp.NewServer(version)
	if err != nil {
		ui.PrintError("Failed to create MCP server: %v", err)
		return err
	}

	// Set the root command for schema generation
	server.SetRootCmd(cmd.Root())

	// Run the server (blocks until client disconnects)
	return server.Run(cmd.Context())
}
