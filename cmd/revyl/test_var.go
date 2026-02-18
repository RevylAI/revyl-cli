// Package main provides test variable management commands.
//
// Test variables use {{variable-name}} syntax in step descriptions and are
// substituted at runtime. They are distinct from app-launch environment
// variables (revyl test env) which are encrypted and injected at app start.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// testVarForce skips the confirmation prompt for clear.
var testVarForce bool

// testVarCmd is the parent command for test variable operations.
var testVarCmd = &cobra.Command{
	Use:   "var",
	Short: "Manage test variables ({{name}} syntax)",
	Long: `Manage test variables for a test.

Test variables use {{variable-name}} syntax in step descriptions and are
substituted at runtime. Variable names must be kebab-case (lowercase letters,
numbers, hyphens).

These are different from env vars (revyl test env), which are encrypted and
injected at app launch.

COMMANDS:
  list    - List all variables for a test
  set     - Add or update a variable (name=value)
  delete  - Delete a variable by name
  clear   - Delete ALL variables for a test

EXAMPLES:
  revyl test var list my-test
  revyl test var set my-test username=testuser@example.com
  revyl test var set my-test otp-code
  revyl test var delete my-test username
  revyl test var clear my-test`,
}

// testVarListCmd lists all variables for a test.
var testVarListCmd = &cobra.Command{
	Use:   "list <test-name>",
	Short: "List all variables for a test",
	Args:  cobra.ExactArgs(1),
	RunE:  runTestVarList,
}

// testVarSetCmd adds or updates a variable.
var testVarSetCmd = &cobra.Command{
	Use:   "set <test-name> <NAME>=<VALUE> or <NAME>",
	Short: "Add or update a test variable",
	Long: `Add or update a test variable for a test.

If the name already exists, its value is updated. Otherwise a new variable is
created. Variable names must be kebab-case (lowercase, numbers, hyphens).

Value is optional -- omit the '=' to create a name-only variable (useful for
extraction blocks that fill the value at runtime).

EXAMPLES:
  revyl test var set my-test username=testuser@example.com
  revyl test var set my-test "password=my secret"
  revyl test var set my-test otp-code`,
	Args: cobra.ExactArgs(2),
	RunE: runTestVarSet,
}

// testVarDeleteCmd deletes a variable by name.
var testVarDeleteCmd = &cobra.Command{
	Use:   "delete <test-name> <NAME>",
	Short: "Delete a variable by name",
	Args:  cobra.ExactArgs(2),
	RunE:  runTestVarDelete,
}

// testVarClearCmd deletes all variables for a test.
var testVarClearCmd = &cobra.Command{
	Use:   "clear <test-name>",
	Short: "Delete ALL variables for a test",
	Long: `Delete all test variables for a test.

Requires --force or interactive confirmation.`,
	Args: cobra.ExactArgs(1),
	RunE: runTestVarClear,
}

func init() {
	testVarCmd.AddCommand(testVarListCmd)
	testVarCmd.AddCommand(testVarSetCmd)
	testVarCmd.AddCommand(testVarDeleteCmd)
	testVarCmd.AddCommand(testVarClearCmd)

	testVarClearCmd.Flags().BoolVarP(&testVarForce, "force", "f", false, "Skip confirmation prompt")
}

// varSetupClient creates an API client and resolves test ID from name/alias.
//
// Parameters:
//   - cmd: The cobra command (used for flag access)
//   - testNameOrID: Test name (from config) or UUID
//
// Returns:
//   - testID: The resolved test UUID
//   - client: Configured API client
//   - error: Any error during setup
var varSetupClient = varSetupClientDefault

func varSetupClientDefault(cmd *cobra.Command, testNameOrID string) (string, *api.Client, error) {
	apiKey, err := getAPIKey()
	if err != nil {
		return "", nil, err
	}

	cwd, _ := os.Getwd()
	cfg, _ := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	testID, _, err := resolveTestID(cmd.Context(), testNameOrID, cfg, client)
	if err != nil {
		ui.PrintError("%v", err)
		return "", nil, fmt.Errorf("test not found")
	}

	return testID, client, nil
}

// isKebabCase validates that a variable name follows kebab-case convention.
//
// Parameters:
//   - name: The variable name to validate
//
// Returns:
//   - bool: True if the name is valid kebab-case
func isKebabCase(name string) bool {
	if name == "" {
		return false
	}
	if name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	for i, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
		if c == '-' && i > 0 && name[i-1] == '-' {
			return false
		}
	}
	return true
}

// runTestVarList lists all test variables for a given test.
func runTestVarList(cmd *cobra.Command, args []string) error {
	testNameOrID := args[0]

	testID, client, err := varSetupClient(cmd, testNameOrID)
	if err != nil {
		return err
	}

	ui.StartSpinner("Fetching variables...")
	resp, err := client.ListCustomVariables(cmd.Context(), testID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to list variables: %v", err)
		return err
	}

	if len(resp.Result) == 0 {
		ui.PrintInfo("No variables set for test '%s'", testNameOrID)
		ui.Println()
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Set a variable:", Command: fmt.Sprintf("revyl test var set %s name=value", testNameOrID)},
		})
		return nil
	}

	ui.Println()
	ui.PrintInfo("Variables for '%s' (%d)", testNameOrID, len(resp.Result))
	ui.Println()

	table := ui.NewTable("NAME", "VALUE", "USAGE")
	table.SetMinWidth(0, 20) // NAME
	table.SetMinWidth(1, 30) // VALUE
	table.SetMinWidth(2, 25) // USAGE

	for _, v := range resp.Result {
		value := v.VariableValue
		if value == "" {
			value = "(empty)"
		}
		usage := fmt.Sprintf("{{%s}}", v.VariableName)
		table.AddRow(v.VariableName, value, usage)
	}

	table.Render()
	return nil
}

// runTestVarSet adds or updates a test variable (upsert pattern).
func runTestVarSet(cmd *cobra.Command, args []string) error {
	testNameOrID := args[0]
	nameValueArg := args[1]

	// Parse NAME=VALUE or NAME (value optional)
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

	if !isKebabCase(name) {
		ui.PrintError("Invalid variable name '%s': must be kebab-case (lowercase letters, numbers, hyphens)", name)
		return fmt.Errorf("invalid variable name")
	}

	testID, client, err := varSetupClient(cmd, testNameOrID)
	if err != nil {
		return err
	}

	// Check if variable already exists (upsert logic)
	ui.StartSpinner("Checking existing variables...")
	existing, err := client.ListCustomVariables(cmd.Context(), testID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to check existing variables: %v", err)
		return err
	}

	var existingVar *api.CustomVariable
	for _, v := range existing.Result {
		if v.VariableName == name {
			existingVar = &v
			break
		}
	}

	if existingVar != nil {
		// Update existing
		ui.StartSpinner("Updating variable...")
		err = client.UpdateCustomVariableValue(cmd.Context(), testID, existingVar.ID, value)
		ui.StopSpinner()

		if err != nil {
			ui.PrintError("Failed to update variable: %v", err)
			return err
		}
		ui.PrintSuccess("Updated '%s' for test '%s'", name, testNameOrID)
	} else {
		// Add new
		ui.StartSpinner("Adding variable...")
		_, err = client.AddCustomVariable(cmd.Context(), testID, name, value)
		ui.StopSpinner()

		if err != nil {
			ui.PrintError("Failed to add variable: %v", err)
			return err
		}
		ui.PrintSuccess("Added '%s' for test '%s' (use as {{%s}} in step descriptions)", name, testNameOrID, name)
	}

	return nil
}

// runTestVarDelete deletes a test variable by name.
func runTestVarDelete(cmd *cobra.Command, args []string) error {
	testNameOrID := args[0]
	name := args[1]

	testID, client, err := varSetupClient(cmd, testNameOrID)
	if err != nil {
		return err
	}

	ui.StartSpinner("Deleting variable...")
	err = client.DeleteCustomVariable(cmd.Context(), testID, name)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to delete variable: %v", err)
		return err
	}

	ui.PrintSuccess("Deleted '%s' from test '%s'", name, testNameOrID)
	return nil
}

// runTestVarClear deletes all test variables for a test.
func runTestVarClear(cmd *cobra.Command, args []string) error {
	testNameOrID := args[0]

	if !testVarForce {
		ui.PrintWarning("This will delete ALL variables for test '%s'", testNameOrID)
		confirmed, promptErr := ui.PromptConfirm("Continue?", false)
		if promptErr != nil || !confirmed {
			ui.PrintInfo("Cancelled")
			return nil
		}
	}

	testID, client, err := varSetupClient(cmd, testNameOrID)
	if err != nil {
		return err
	}

	ui.StartSpinner("Clearing all variables...")
	err = client.DeleteAllCustomVariables(cmd.Context(), testID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to clear variables: %v", err)
		return err
	}

	ui.PrintSuccess("Cleared all variables for test '%s'", testNameOrID)
	return nil
}
