package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	devicepkg "github.com/revyl/cli/internal/device"
	"github.com/revyl/cli/internal/ui"
)

var (
	globalLaunchVarListShowValues bool

	globalLaunchVarCreateDescription string

	globalLaunchVarUpdateKey         string
	globalLaunchVarUpdateValue       string
	globalLaunchVarUpdateDescription string

	globalLaunchVarDeleteForce bool
)

var globalLaunchVarCmd = &cobra.Command{
	Use:     "launch-var",
	Aliases: []string{"launch-vars", "launch-variable"},
	Short:   "Manage org-wide launch variables",
	Long: `Manage org-wide reusable launch variables.

Launch variables are stored once at the organization level and then attached to
tests from the web UI. This command manages the reusable library entries.`,
}

var globalLaunchVarListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all org launch variables",
	Args:  cobra.NoArgs,
	RunE:  runGlobalLaunchVarList,
}

var globalLaunchVarGetCmd = &cobra.Command{
	Use:   "get <key|id>",
	Short: "Get a single launch variable",
	Args:  cobra.ExactArgs(1),
	RunE:  runGlobalLaunchVarGet,
}

var globalLaunchVarCreateCmd = &cobra.Command{
	Use:   "create <KEY>=<VALUE>",
	Short: "Create a launch variable",
	Long: `Create a reusable org launch variable.

EXAMPLES:
  revyl global launch-var create API_URL=https://staging.example.com
  revyl global launch-var create DEBUG=true --description "Enable debug startup"`,
	Args: cobra.ExactArgs(1),
	RunE: runGlobalLaunchVarCreate,
}

var globalLaunchVarUpdateCmd = &cobra.Command{
	Use:   "update <key|id>",
	Short: "Update a launch variable",
	Long: `Update a reusable org launch variable.

EXAMPLES:
  revyl global launch-var update API_URL --value https://prod.example.com
  revyl global launch-var update API_URL --key API_BASE_URL --description "Shared API endpoint"
  revyl global launch-var update 11111111-1111-1111-1111-111111111111 --description ""`,
	Args: cobra.ExactArgs(1),
	RunE: runGlobalLaunchVarUpdate,
}

var globalLaunchVarDeleteCmd = &cobra.Command{
	Use:   "delete <key|id>",
	Short: "Delete a launch variable",
	Long: `Delete a reusable org launch variable.

If the variable is attached to tests, those attachments are removed as part of
the delete.`,
	Args: cobra.ExactArgs(1),
	RunE: runGlobalLaunchVarDelete,
}

func init() {
	globalCmd.AddCommand(globalLaunchVarCmd)
	globalLaunchVarCmd.AddCommand(globalLaunchVarListCmd)
	globalLaunchVarCmd.AddCommand(globalLaunchVarGetCmd)
	globalLaunchVarCmd.AddCommand(globalLaunchVarCreateCmd)
	globalLaunchVarCmd.AddCommand(globalLaunchVarUpdateCmd)
	globalLaunchVarCmd.AddCommand(globalLaunchVarDeleteCmd)

	globalLaunchVarListCmd.Flags().BoolVar(&globalLaunchVarListShowValues, "show-values", false, "Show unmasked values in list output")
	globalLaunchVarCreateCmd.Flags().StringVar(&globalLaunchVarCreateDescription, "description", "", "Optional description")
	globalLaunchVarUpdateCmd.Flags().StringVar(&globalLaunchVarUpdateKey, "key", "", "New key")
	globalLaunchVarUpdateCmd.Flags().StringVar(&globalLaunchVarUpdateValue, "value", "", "New value")
	globalLaunchVarUpdateCmd.Flags().StringVar(&globalLaunchVarUpdateDescription, "description", "", "New description (use empty string to clear)")
	globalLaunchVarDeleteCmd.Flags().BoolVarP(&globalLaunchVarDeleteForce, "force", "f", false, "Skip confirmation prompt")
}

var launchVarSetupClient = globalVarSetupClientDefault

func maskLaunchVarValue(value string) string {
	if value == "" {
		return "(empty)"
	}
	return strings.Repeat("*", min(max(len(value), 4), 8))
}

func encodeJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func parseKeyValueArg(arg string) (string, string, error) {
	idx := strings.Index(arg, "=")
	if idx <= 0 {
		return "", "", fmt.Errorf("invalid format, expected KEY=VALUE")
	}
	key := strings.TrimSpace(arg[:idx])
	if key == "" {
		return "", "", fmt.Errorf("key cannot be empty")
	}
	return key, arg[idx+1:], nil
}

func resolveLaunchVarKeyOrID(cmd *cobra.Command, client *api.Client, keyOrID string) (api.OrgLaunchVariable, error) {
	return devicepkg.ResolveLaunchVar(cmd.Context(), client, keyOrID)
}

func runGlobalLaunchVarList(cmd *cobra.Command, args []string) error {
	client, err := launchVarSetupClient(cmd)
	if err != nil {
		return err
	}

	ui.StartSpinner("Fetching launch variables...")
	resp, err := client.ListOrgLaunchVariables(cmd.Context())
	ui.StopSpinner()
	if err != nil {
		ui.PrintError("Failed to list launch variables: %v", err)
		return err
	}

	jsonMode, _ := cmd.Root().PersistentFlags().GetBool("json")
	if jsonMode {
		return encodeJSON(resp)
	}

	if len(resp.Result) == 0 {
		ui.PrintInfo("No launch variables found")
		ui.Println()
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Create a launch variable:", Command: "revyl global launch-var create KEY=VALUE"},
		})
		return nil
	}

	ui.Println()
	ui.PrintInfo("Launch variables (%d)", len(resp.Result))
	ui.Println()

	table := ui.NewTable("KEY", "VALUE", "DESCRIPTION", "TESTS", "UPDATED")
	table.SetMinWidth(0, 20)
	table.SetMinWidth(1, 12)
	table.SetMinWidth(2, 24)
	table.SetMinWidth(3, 5)
	table.SetMinWidth(4, 20)

	for _, v := range resp.Result {
		value := maskLaunchVarValue(v.Value)
		if globalLaunchVarListShowValues {
			value = v.Value
		}
		desc := v.Description
		if desc == "" {
			desc = "-"
		}
		table.AddRow(v.Key, value, desc, fmt.Sprintf("%d", v.AttachedTestCount), v.UpdatedAt)
	}

	table.Render()
	return nil
}

func runGlobalLaunchVarGet(cmd *cobra.Command, args []string) error {
	client, err := launchVarSetupClient(cmd)
	if err != nil {
		return err
	}

	ui.StartSpinner("Fetching launch variable...")
	variable, err := resolveLaunchVarKeyOrID(cmd, client, args[0])
	ui.StopSpinner()
	if err != nil {
		ui.PrintError("%v", err)
		return err
	}

	jsonMode, _ := cmd.Root().PersistentFlags().GetBool("json")
	if jsonMode {
		return encodeJSON(variable)
	}

	ui.PrintInfo("Launch variable: %s", variable.Key)
	ui.PrintDim("  ID:          %s", variable.ID)
	ui.PrintDim("  Value:       %s", variable.Value)
	if variable.Description != "" {
		ui.PrintDim("  Description: %s", variable.Description)
	}
	ui.PrintDim("  Tests:       %d", variable.AttachedTestCount)
	ui.PrintDim("  Updated:     %s", variable.UpdatedAt)
	return nil
}

func runGlobalLaunchVarCreate(cmd *cobra.Command, args []string) error {
	key, value, err := parseKeyValueArg(args[0])
	if err != nil {
		ui.PrintError("%v", err)
		return err
	}

	client, err := launchVarSetupClient(cmd)
	if err != nil {
		return err
	}

	var descPtr *string
	if cmd.Flags().Changed("description") {
		descPtr = &globalLaunchVarCreateDescription
	}

	ui.StartSpinner("Creating launch variable...")
	resp, err := client.AddOrgLaunchVariable(cmd.Context(), key, value, descPtr)
	ui.StopSpinner()
	if err != nil {
		ui.PrintError("Failed to create launch variable: %v", err)
		return err
	}

	jsonMode, _ := cmd.Root().PersistentFlags().GetBool("json")
	if jsonMode {
		return encodeJSON(resp)
	}

	ui.PrintSuccess("Created launch variable '%s'", resp.Result.Key)
	if resp.Result.Description != "" {
		ui.PrintDim("  Description: %s", resp.Result.Description)
	}
	return nil
}

func runGlobalLaunchVarUpdate(cmd *cobra.Command, args []string) error {
	client, err := launchVarSetupClient(cmd)
	if err != nil {
		return err
	}

	ui.StartSpinner("Resolving launch variable...")
	variable, err := resolveLaunchVarKeyOrID(cmd, client, args[0])
	ui.StopSpinner()
	if err != nil {
		ui.PrintError("%v", err)
		return err
	}

	var keyPtr, valuePtr, descPtr *string
	if cmd.Flags().Changed("key") {
		keyPtr = &globalLaunchVarUpdateKey
	}
	if cmd.Flags().Changed("value") {
		valuePtr = &globalLaunchVarUpdateValue
	}
	if cmd.Flags().Changed("description") {
		descPtr = &globalLaunchVarUpdateDescription
	}
	if keyPtr == nil && valuePtr == nil && descPtr == nil {
		err := fmt.Errorf("nothing to update; provide at least one of --key, --value, or --description")
		ui.PrintError("%v", err)
		return err
	}

	ui.StartSpinner("Updating launch variable...")
	resp, err := client.UpdateOrgLaunchVariable(cmd.Context(), variable.ID, keyPtr, valuePtr, descPtr)
	ui.StopSpinner()
	if err != nil {
		ui.PrintError("Failed to update launch variable: %v", err)
		return err
	}

	jsonMode, _ := cmd.Root().PersistentFlags().GetBool("json")
	if jsonMode {
		return encodeJSON(resp)
	}

	ui.PrintSuccess("Updated launch variable '%s'", resp.Result.Key)
	return nil
}

func runGlobalLaunchVarDelete(cmd *cobra.Command, args []string) error {
	client, err := launchVarSetupClient(cmd)
	if err != nil {
		return err
	}

	ui.StartSpinner("Resolving launch variable...")
	variable, err := resolveLaunchVarKeyOrID(cmd, client, args[0])
	ui.StopSpinner()
	if err != nil {
		ui.PrintError("%v", err)
		return err
	}

	if !globalLaunchVarDeleteForce {
		ui.PrintWarning("This will delete launch variable '%s'", variable.Key)
		confirmed, promptErr := ui.PromptConfirm("Continue?", false)
		if promptErr != nil || !confirmed {
			ui.PrintInfo("Cancelled")
			return nil
		}
	}

	ui.StartSpinner("Deleting launch variable...")
	resp, err := client.DeleteOrgLaunchVariable(cmd.Context(), variable.ID)
	ui.StopSpinner()
	if err != nil {
		ui.PrintError("Failed to delete launch variable: %v", err)
		return err
	}

	jsonMode, _ := cmd.Root().PersistentFlags().GetBool("json")
	if jsonMode {
		return encodeJSON(resp)
	}

	ui.PrintSuccess("Deleted launch variable '%s'", variable.Key)
	if resp.DetachedTestCount > 0 {
		ui.PrintDim("  Detached from %d test(s)", resp.DetachedTestCount)
	}
	return nil
}
