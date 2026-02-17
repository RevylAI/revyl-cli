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

Test execution:
  - run_test: Run a test by name or ID (supports --location override)
  - run_workflow: Run a workflow by name or ID (supports app and location overrides)
  - get_test_status: Get status of a running test
  - cancel_test: Cancel a running test execution
  - cancel_workflow: Cancel a running workflow execution

Test management:
  - list_tests: List available tests from .revyl/config.yaml
  - list_remote_tests: List all tests in the organization from the API
  - create_test: Create a new test from YAML content
  - update_test: Push updated YAML content to an existing test
  - delete_test: Delete a test by name or UUID
  - validate_yaml: Validate YAML test syntax without creating

Workflow management:
  - list_workflows: List all workflows in the organization
  - create_workflow: Create a new workflow
  - delete_workflow: Delete a workflow
  - open_workflow_editor: Get the URL to open a workflow in the browser editor

Build & app management:
  - list_builds: List available build versions
  - upload_build: Upload a local build file (.apk/.ipa/.zip) to an app
  - create_app: Create a new app for build uploads
  - delete_app: Delete an app and all its build versions

Module management:
  - list_modules: List reusable test modules
  - get_module: Get module details by ID
  - create_module: Create a new reusable module
  - delete_module: Delete a module
  - insert_module_block: Generate a module_import YAML snippet

Script management:
  - list_scripts: List code execution scripts
  - get_script: Get script details including source code
  - create_script: Create a new code execution script
  - update_script: Update an existing script
  - delete_script: Delete a script
  - insert_script_block: Generate a code_execution YAML snippet

Tag management:
  - list_tags: List all tags with test counts
  - create_tag: Create a new tag (upsert)
  - delete_tag: Delete a tag
  - get_test_tags: Get tags for a test
  - set_test_tags: Replace all tags on a test
  - add_remove_test_tags: Add/remove tags without replacing all

Environment variables:
  - list_env_vars: List env vars for a test
  - set_env_var: Add or update a test env var
  - delete_env_var: Delete a test env var by key
  - clear_env_vars: Delete all env vars for a test

Workflow settings:
  - get_workflow_settings: Get workflow location and app settings
  - set_workflow_location: Set GPS location override for a workflow
  - clear_workflow_location: Remove location override
  - set_workflow_app: Set app overrides for a workflow
  - clear_workflow_app: Remove app overrides

Live editing:
  - open_test_editor: Open test in browser editor with optional hot reload
  - stop_hot_reload: Stop the hot reload session
  - hot_reload_status: Check if a hot reload session is active

Utilities:
  - auth_status: Check authentication status
  - get_schema: Get CLI and YAML test schema reference

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
	devMode, _ := cmd.Flags().GetBool("dev")
	server, err := mcp.NewServer(version, devMode)
	if err != nil {
		ui.PrintError("Failed to create MCP server: %v", err)
		return err
	}

	// Set the root command for schema generation
	server.SetRootCmd(cmd.Root())

	// Run the server (blocks until client disconnects)
	return server.Run(cmd.Context())
}
