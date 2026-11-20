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

const globalVarSecretMask = "********"

var (
	globalVarGetReveal                  bool
	globalVarSetSecret                  bool
	globalVarSetNoSecret                bool
	globalVarSetReadableByAutomatedRuns bool
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
	Short: "Manage org-wide global variables ({{global.name}} syntax)",
	Long: `Manage global variables shared across all tests in your organization.

Global variables use {{global.variable-name}} syntax in step descriptions and are
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
  revyl global var get password --reveal
  revyl global var set login-email=testuser@example.com
  revyl global var set "password=my secret" --secret
  revyl global var set password --readable-by-automated-runs
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
	Long: `Get a global variable's value. Secret values print masked.

With --reveal, the plaintext value prints exactly, with no trailing newline, a
secret's included; with --json, the variable comes back with its plaintext
value. Your own login or personal API key reveals any secret unless your role
is Viewer. Any other key (for example a CI or agent key) reveals only secrets
marked readable by automated runs. Revyl audits every secret reveal.

EXAMPLES:
  revyl global var get login-email
  revyl global var get password --reveal
  revyl global var get password --reveal --json`,
	Args: cobra.ExactArgs(1),
	RunE: runGlobalVarGet,
}

var globalVarSetCmd = &cobra.Command{
	Use:   "set <NAME>=<VALUE> or <NAME>",
	Short: "Add or update a global variable",
	Long: `Add or update a global variable for your organization.

If the name already exists, its value is updated. Otherwise a new variable is
created.

Value is optional -- omit the '=' to create a name-only variable (useful for
variables that are filled at runtime by extraction or code blocks).

--readable-by-automated-runs lets credentials other than an organization
member's own login or personal API key (for example a CI, agent or hosted
session-profile run key) reveal a secret; any automated run in the organization
can then read it. Only a member signed in with their own login or personal API
key can set or clear it (--readable-by-automated-runs=false). Without '=VALUE',
it changes only the mark of an existing variable.

EXAMPLES:
  revyl global var set login-email=testuser@example.com
  revyl global var set "password=my secret" --secret
  revyl global var set "password=new value" --no-secret
  revyl global var set password --readable-by-automated-runs
  revyl global var set password --readable-by-automated-runs=false
  revyl global var set otp-code`,
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

	globalVarGetCmd.Flags().BoolVar(&globalVarGetReveal, "reveal", false, "Print the plaintext value exactly, a secret's included (audited)")
	globalVarSetCmd.Flags().BoolVar(&globalVarSetSecret, "secret", false, "Store the value as an encrypted secret")
	globalVarSetCmd.Flags().BoolVar(&globalVarSetNoSecret, "no-secret", false, "Store the value as plaintext")
	globalVarSetCmd.Flags().BoolVar(&globalVarSetReadableByAutomatedRuns, "readable-by-automated-runs", false, "Let any automated run in the organization reveal this secret (=false clears)")
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

func globalVarIsSecret(v api.GlobalVariableRow) bool {
	return v.IsSecret != nil && *v.IsSecret
}

func formatGlobalVarValue(v api.GlobalVariableRow) string {
	if globalVarIsSecret(v) {
		return globalVarSecretMask
	}
	if v.VariableValue == nil || *v.VariableValue == "" {
		return "(empty)"
	}
	return *v.VariableValue
}

// requireAutomatedRunsMarkApplied fails a write whose requested mark the
// server did not return: a backend that predates the mark ignores the field.
func requireAutomatedRunsMarkApplied(requested, returned *bool) error {
	if requested == nil || (returned != nil && *returned == *requested) {
		return nil
	}
	return fmt.Errorf("the variable was saved, but the server did not apply --readable-by-automated-runs=%t; it may not support this flag yet", *requested)
}

func globalVarWriteOptions(cmd *cobra.Command, existing *api.GlobalVariableRow) (api.GlobalVariableWriteOptions, error) {
	secretChanged := cmd.Flags().Changed("secret")
	noSecretChanged := cmd.Flags().Changed("no-secret")
	if secretChanged && noSecretChanged {
		return api.GlobalVariableWriteOptions{}, fmt.Errorf("use either --secret or --no-secret, not both")
	}
	var opts api.GlobalVariableWriteOptions
	if cmd.Flags().Changed("readable-by-automated-runs") {
		opts.ReadableByAutomatedRuns = &globalVarSetReadableByAutomatedRuns
	}
	switch {
	case secretChanged:
		isSecret := true
		opts.IsSecret = &isSecret
	case noSecretChanged:
		isSecret := false
		opts.IsSecret = &isSecret
	case existing != nil && globalVarIsSecret(*existing):
		keepSecret := true
		opts.IsSecret = &keepSecret
	}
	return opts, nil
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
		desc := ""
		if v.Description != nil {
			desc = *v.Description
		}
		usage := fmt.Sprintf("{{global.%s}}", v.VariableName)
		table.AddRow(v.VariableName, formatGlobalVarValue(v), desc, usage)
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
			if globalVarGetReveal {
				return revealGlobalVar(cmd, client, v, jsonMode)
			}
			if jsonMode {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(v)
			}
			fmt.Println(formatGlobalVarValue(v))
			return nil
		}
	}

	ui.PrintError("Global variable '%s' not found", name)
	return fmt.Errorf("variable not found")
}

func revealGlobalVar(cmd *cobra.Command, client *api.Client, v api.GlobalVariableRow, jsonMode bool) error {
	ui.StartSpinner("Revealing variable...")
	resp, err := client.RevealGlobalVariable(cmd.Context(), v.Id.String())
	ui.StopSpinner()
	if err != nil {
		return fmt.Errorf("failed to reveal global variable '%s': %w", v.VariableName, err)
	}

	if jsonMode {
		return encodeJSON(resp.Result)
	}
	if resp.Result.VariableValue != nil {
		fmt.Print(*resp.Result.VariableValue)
	}
	return nil
}

func runGlobalVarSet(cmd *cobra.Command, args []string) error {
	nameValueArg := args[0]

	if cmd.Flags().Changed("secret") && cmd.Flags().Changed("no-secret") {
		err := fmt.Errorf("use either --secret or --no-secret, not both")
		ui.PrintError("%v", err)
		return err
	}

	var name, value string
	idx := strings.Index(nameValueArg, "=")
	hasValue := idx >= 0
	if hasValue {
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

	var existingVar *api.GlobalVariableRow
	for _, v := range existing.Result {
		if v.VariableName == name {
			v := v
			existingVar = &v
			break
		}
	}

	opts, err := globalVarWriteOptions(cmd, existingVar)
	if err != nil {
		ui.PrintError("%v", err)
		return err
	}

	if existingVar != nil {
		valuePtr := &value
		if !hasValue && opts.ReadableByAutomatedRuns != nil {
			valuePtr = nil
		}
		ui.StartSpinner("Updating variable...")
		resp, err := client.UpdateGlobalVariable(cmd.Context(), existingVar.Id.String(), name, valuePtr, opts)
		ui.StopSpinner()

		if err != nil {
			ui.PrintError("Failed to update variable: %v", err)
			return err
		}
		if err := requireAutomatedRunsMarkApplied(opts.ReadableByAutomatedRuns, resp.Result.ReadableByAutomatedRuns); err != nil {
			ui.PrintError("%v", err)
			return err
		}
		ui.PrintSuccess("Updated global variable '%s'", name)
	} else {
		ui.StartSpinner("Adding variable...")
		resp, err := client.AddGlobalVariable(cmd.Context(), name, value, opts)
		ui.StopSpinner()

		if err != nil {
			ui.PrintError("Failed to add variable: %v", err)
			return err
		}
		if err := requireAutomatedRunsMarkApplied(opts.ReadableByAutomatedRuns, resp.Result.ReadableByAutomatedRuns); err != nil {
			ui.PrintError("%v", err)
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
			variableID = v.Id.String()
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
