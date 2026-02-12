// Package main provides the module command for module management.
package main

import (
	"github.com/spf13/cobra"
)

// Module command flags
var (
	moduleListJSON   bool
	moduleListSearch string

	moduleCreateDescription string
	moduleCreateFromFile    string

	moduleUpdateName        string
	moduleUpdateDescription string
	moduleUpdateFromFile    string

	moduleDeleteForce bool
)

// moduleCmd is the parent command for module operations.
var moduleCmd = &cobra.Command{
	Use:   "module",
	Short: "Manage reusable test modules",
	Long: `Manage reusable test modules (groups of test blocks).

Modules are reusable building blocks that can be imported into any test
via a module_import block. This avoids duplicating common flows like login,
onboarding, or checkout across multiple tests.

COMMANDS:
  list    - List all modules
  get     - Show a module's details and blocks
  create  - Create a new module
  update  - Update an existing module
  delete  - Delete a module
  insert  - Output a module_import YAML snippet for pasting into tests

EXAMPLES:
  revyl module list
  revyl module list --search login
  revyl module get login-flow
  revyl module create login-flow --from-file blocks.yaml --description "Standard login"
  revyl module update login-flow --name "new-name"
  revyl module delete login-flow
  revyl module insert login-flow`,
}

// moduleListCmd lists all modules.
var moduleListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all modules",
	Long: `List all modules in your organization.

EXAMPLES:
  revyl module list
  revyl module list --json
  revyl module list --search login`,
	RunE: runModuleList,
}

// moduleGetCmd shows a module's details.
var moduleGetCmd = &cobra.Command{
	Use:   "get <name|id>",
	Short: "Show a module's details and blocks",
	Long: `Show a module's metadata and blocks as readable YAML.

EXAMPLES:
  revyl module get login-flow
  revyl module get abc-123-uuid`,
	Args: cobra.ExactArgs(1),
	RunE: runModuleGet,
}

// moduleCreateCmd creates a new module.
var moduleCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new module",
	Long: `Create a new reusable module from a YAML blocks file.

The blocks file should contain a top-level 'blocks' key with an array
of test blocks:

  blocks:
    - type: instructions
      step_description: "Enter email in the email field"
    - type: instructions
      step_description: "Enter password in the password field"

EXAMPLES:
  revyl module create login-flow --from-file login-blocks.yaml
  revyl module create login-flow --from-file login-blocks.yaml --description "Standard login sequence"`,
	Args: cobra.ExactArgs(1),
	RunE: runModuleCreate,
}

// moduleUpdateCmd updates an existing module.
var moduleUpdateCmd = &cobra.Command{
	Use:   "update <name|id>",
	Short: "Update an existing module",
	Long: `Update a module's name, description, or blocks.

EXAMPLES:
  revyl module update login-flow --name "new-login-flow"
  revyl module update login-flow --description "Updated login sequence"
  revyl module update login-flow --from-file updated-blocks.yaml`,
	Args: cobra.ExactArgs(1),
	RunE: runModuleUpdate,
}

// moduleDeleteCmd deletes a module.
var moduleDeleteCmd = &cobra.Command{
	Use:   "delete <name|id>",
	Short: "Delete a module",
	Long: `Delete a module. If the module is in use by tests, the backend
returns a 409 conflict error with the test names.

EXAMPLES:
  revyl module delete login-flow
  revyl module delete login-flow --force`,
	Args: cobra.ExactArgs(1),
	RunE: runModuleDelete,
}

// moduleInsertCmd outputs a module_import YAML snippet.
var moduleInsertCmd = &cobra.Command{
	Use:   "insert <name|id>",
	Short: "Output a module_import YAML snippet",
	Long: `Output a ready-to-paste YAML snippet for importing a module into a test.

EXAMPLES:
  revyl module insert login-flow
  # Output:
  # Paste this into your test YAML:
  # - type: module_import
  #   step_description: "Login Flow"
  #   module_id: "abc-123-uuid"`,
	Args: cobra.ExactArgs(1),
	RunE: runModuleInsert,
}

func init() {
	moduleCmd.AddCommand(moduleListCmd)
	moduleCmd.AddCommand(moduleGetCmd)
	moduleCmd.AddCommand(moduleCreateCmd)
	moduleCmd.AddCommand(moduleUpdateCmd)
	moduleCmd.AddCommand(moduleDeleteCmd)
	moduleCmd.AddCommand(moduleInsertCmd)

	// module list flags
	moduleListCmd.Flags().BoolVar(&moduleListJSON, "json", false, "Output results as JSON")
	moduleListCmd.Flags().StringVar(&moduleListSearch, "search", "", "Filter modules by name or description")

	// module create flags
	moduleCreateCmd.Flags().StringVar(&moduleCreateDescription, "description", "", "Module description")
	moduleCreateCmd.Flags().StringVar(&moduleCreateFromFile, "from-file", "", "YAML file with blocks array")
	_ = moduleCreateCmd.MarkFlagRequired("from-file")

	// module update flags
	moduleUpdateCmd.Flags().StringVar(&moduleUpdateName, "name", "", "New module name")
	moduleUpdateCmd.Flags().StringVar(&moduleUpdateDescription, "description", "", "New module description")
	moduleUpdateCmd.Flags().StringVar(&moduleUpdateFromFile, "from-file", "", "YAML file with new blocks array")

	// module delete flags
	moduleDeleteCmd.Flags().BoolVarP(&moduleDeleteForce, "force", "f", false, "Skip confirmation prompt")
}
