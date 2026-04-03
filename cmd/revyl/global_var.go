package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

// globalCmd is the parent command for org-wide global resources.
var globalCmd = &cobra.Command{
	Use:   "global",
	Short: "Manage org-wide global resources",
	Long:  `Manage global resources shared across all tests in your organization.`,
}

// globalVarCmd is the subcommand for org-wide global variable operations.
var globalVarCmd = &cobra.Command{
	Use:   "var",
	Short: "Manage org-wide global variables ({{name}} syntax)",
	Long: `Manage global variables shared across all tests in your organization.

Global variables use {{variable-name}} syntax in step descriptions and are
available to every test. If a test defines a local variable with the same name,
the local value takes precedence.

These are different from test variables (revyl test var), which are scoped to
a single test.

COMMANDS:
  list    - List all global variables
  get     - Get a single variable's value
  set     - Add or update a global variable (name=value)
  delete  - Delete a global variable by name

EXAMPLES:
  revyl global var list
  revyl global var get login-email
  revyl global var set login-email=testuser@example.com
  revyl global var delete login-email`,
}

var globalVarListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all global variables",
	Args:  cobra.NoArgs,
	RunE:  runGlobalVarList,
}

var globalVarGetCmd = &cobra.Command{
	Use:   "get <NAME>",
	Short: "Get a global variable's value",
	Args:  cobra.ExactArgs(1),
	RunE:  runGlobalVarGet,
}

var globalVarSetCmd = &cobra.Command{
	Use:   "set <NAME>=<VALUE> or <NAME>",
	Short: "Add or update a global variable",
	Long: `Add or update a global variable for your organization.

If the name already exists, its value is updated. Otherwise a new variable is
created.

Value is optional -- omit the '=' to create a name-only variable (useful for
variables that are filled at runtime by extraction or code blocks).

EXAMPLES:
  revyl var set login-email=testuser@example.com
  revyl var set "password=my secret"
  revyl var set otp-code`,
	Args: cobra.ExactArgs(1),
	RunE: runGlobalVarSet,
}

var globalVarDeleteCmd = &cobra.Command{
	Use:   "delete <NAME>",
	Short: "Delete a global variable by name",
	Args:  cobra.ExactArgs(1),
	RunE:  runGlobalVarDelete,
}

func init() {
	globalCmd.AddCommand(globalVarCmd)
	globalVarCmd.AddCommand(globalVarListCmd)
	globalVarCmd.AddCommand(globalVarGetCmd)
	globalVarCmd.AddCommand(globalVarSetCmd)
	globalVarCmd.AddCommand(globalVarDeleteCmd)
}

// globalVarSetupClient creates an API client for global variable operations.
// Simpler than varSetupClient since no test ID resolution is needed.
var globalVarSetupClient = globalVarSetupClientDefault

func globalVarSetupClientDefault(cmd *cobra.Command) (*api.Client, error) {
	apiKey, err := getAPIKey()
	if err != nil {
		return nil, err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)
	return client, nil
}

func runGlobalVarList(cmd *cobra.Command, args []string) error {
	client, err := globalVarSetupClient(cmd)
	if err != nil {
		return err
	}

	ui.StartSpinner("Fetching global variables...")
	resp, err := client.ListGlobalVariables(cmd.Context())
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to list global variables: %v", err)
		return err
	}

	jsonMode, _ := cmd.Root().PersistentFlags().GetBool("json")
	if jsonMode {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	if len(resp.Result) == 0 {
		ui.PrintInfo("No global variables set")
		ui.Println()
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Set a variable:", Command: "revyl global var set name=value"},
		})
		return nil
	}

	ui.Println()
	ui.PrintInfo("Global variables (%d)", len(resp.Result))
	ui.Println()

	table := ui.NewTable("NAME", "VALUE", "DESCRIPTION", "USAGE")
	table.SetMinWidth(0, 20)
	table.SetMinWidth(1, 25)
	table.SetMinWidth(2, 20)
	table.SetMinWidth(3, 20)

	for _, v := range resp.Result {
		value := ""
		if v.VariableValue != nil {
			value = *v.VariableValue
		}
		if value == "" {
			value = "(empty)"
		}
		desc := ""
		if v.Description != nil {
			desc = *v.Description
		}
		usage := fmt.Sprintf("{{global.%s}}", v.VariableName)
		table.AddRow(v.VariableName, value, desc, usage)
	}

	table.Render()
	return nil
}

func runGlobalVarGet(cmd *cobra.Command, args []string) error {
	name := args[0]

	client, err := globalVarSetupClient(cmd)
	if err != nil {
		return err
	}

	ui.StartSpinner("Fetching variable...")
	resp, err := client.ListGlobalVariables(cmd.Context())
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to fetch global variables: %v", err)
		return err
	}

	jsonMode, _ := cmd.Root().PersistentFlags().GetBool("json")

	for _, v := range resp.Result {
		if v.VariableName == name {
			if jsonMode {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(v)
			}
			value := ""
			if v.VariableValue != nil {
				value = *v.VariableValue
			}
			if value == "" {
				value = "(empty)"
			}
			fmt.Println(value)
			return nil
		}
	}

	ui.PrintError("Global variable '%s' not found", name)
	return fmt.Errorf("variable not found")
}

func runGlobalVarSet(cmd *cobra.Command, args []string) error {
	nameValueArg := args[0]

	var name, value string
	idx := strings.Index(nameValueArg, "=")
	if idx >= 0 {
		name = nameValueArg[:idx]
		value = nameValueArg[idx+1:]
	} else {
		name = nameValueArg
		value = ""
	}

	if name == "" {
		ui.PrintError("Variable name cannot be empty")
		return fmt.Errorf("empty variable name")
	}

	client, err := globalVarSetupClient(cmd)
	if err != nil {
		return err
	}

	// Upsert: list existing, then create or update
	ui.StartSpinner("Checking existing variables...")
	existing, err := client.ListGlobalVariables(cmd.Context())
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to check existing variables: %v", err)
		return err
	}

	var existingVar *api.GlobalVariable
	for _, v := range existing.Result {
		if v.VariableName == name {
			existingVar = &v
			break
		}
	}

	if existingVar != nil {
		ui.StartSpinner("Updating variable...")
		_, err = client.UpdateGlobalVariable(cmd.Context(), existingVar.ID, name, value)
		ui.StopSpinner()

		if err != nil {
			ui.PrintError("Failed to update variable: %v", err)
			return err
		}
		ui.PrintSuccess("Updated global variable '%s'", name)
	} else {
		ui.StartSpinner("Adding variable...")
		_, err = client.AddGlobalVariable(cmd.Context(), name, value)
		ui.StopSpinner()

		if err != nil {
			ui.PrintError("Failed to add variable: %v", err)
			return err
		}
		ui.PrintSuccess("Added global variable '%s' (use as {{global.%s}} in step descriptions)", name, name)
	}

	return nil
}

func runGlobalVarDelete(cmd *cobra.Command, args []string) error {
	name := args[0]

	client, err := globalVarSetupClient(cmd)
	if err != nil {
		return err
	}

	// Resolve name to UUID
	ui.StartSpinner("Finding variable...")
	resp, err := client.ListGlobalVariables(cmd.Context())
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to fetch global variables: %v", err)
		return err
	}

	var variableID string
	for _, v := range resp.Result {
		if v.VariableName == name {
			variableID = v.ID
			break
		}
	}

	if variableID == "" {
		ui.PrintError("Global variable '%s' not found", name)
		return fmt.Errorf("variable not found")
	}

	ui.StartSpinner("Deleting variable...")
	err = client.DeleteGlobalVariable(cmd.Context(), variableID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to delete variable: %v", err)
		return err
	}

	ui.PrintSuccess("Deleted global variable '%s'", name)
	return nil
}
