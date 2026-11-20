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
	globalLaunchVarGetReveal bool

	globalLaunchVarCreateDescription             string
	globalLaunchVarCreateSecret                  bool
	globalLaunchVarCreateReadableByAutomatedRuns bool

	globalLaunchVarUpdateKey                     string
	globalLaunchVarUpdateValue                   string
	globalLaunchVarUpdateDescription             string
	globalLaunchVarUpdateSecret                  bool
	globalLaunchVarUpdateReadableByAutomatedRuns bool

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
	Long: `Get a single launch variable. Secret values print masked.

With --reveal, the plaintext value prints exactly, with no trailing newline, a
secret's included; with --json, the variable comes back with its plaintext
value, or arguments for an iOS argument set. Argument sets need --json. Your own
login or personal API key reveals any secret unless your role is Viewer. Any
other key (for example a CI or agent key) reveals only secrets marked readable
by automated runs. Revyl audits every secret reveal.

EXAMPLES:
  revyl global launch-var get API_URL
  revyl global launch-var get API_TOKEN --reveal
  revyl global launch-var get LAUNCH_ARGS --reveal --json`,
	Args: cobra.ExactArgs(1),
	RunE: runGlobalLaunchVarGet,
}

var globalLaunchVarCreateCmd = &cobra.Command{
	Use:   "create <KEY>=<VALUE>",
	Short: "Create a launch variable",
	Long: `Create a reusable org launch variable.

--readable-by-automated-runs lets credentials other than an organization
member's own login or personal API key (for example a CI, agent or hosted
session-profile run key) reveal a secret; any automated run in the organization
can then read it. Only a member signed in with their own login or personal API
key can set it.

EXAMPLES:
  revyl global launch-var create API_URL=https://staging.example.com
  revyl global launch-var create DEBUG=true --description "Enable debug startup"
  revyl global launch-var create API_TOKEN=token --secret --readable-by-automated-runs`,
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
  revyl global launch-var update API_TOKEN --readable-by-automated-runs
  revyl global launch-var update API_TOKEN --readable-by-automated-runs=false
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

	globalLaunchVarListCmd.Flags().Bool("show-values", false, "Deprecated: non-secret values are always shown; secret values stay masked")
	globalLaunchVarCreateCmd.Flags().StringVar(&globalLaunchVarCreateDescription, "description", "", "Optional description")
	globalLaunchVarGetCmd.Flags().BoolVar(&globalLaunchVarGetReveal, "reveal", false, "Print the plaintext value exactly, a secret's included (audited)")
	globalLaunchVarCreateCmd.Flags().BoolVar(&globalLaunchVarCreateSecret, "secret", false, "Encrypt the value and mask it on reads")
	globalLaunchVarCreateCmd.Flags().BoolVar(&globalLaunchVarCreateReadableByAutomatedRuns, "readable-by-automated-runs", false, "Let any automated run in the organization reveal this secret")
	globalLaunchVarUpdateCmd.Flags().StringVar(&globalLaunchVarUpdateKey, "key", "", "New key")
	globalLaunchVarUpdateCmd.Flags().StringVar(&globalLaunchVarUpdateValue, "value", "", "New value")
	globalLaunchVarUpdateCmd.Flags().StringVar(&globalLaunchVarUpdateDescription, "description", "", "New description (use empty string to clear)")
	globalLaunchVarUpdateCmd.Flags().BoolVar(&globalLaunchVarUpdateSecret, "secret", false, "Set secrecy state (use --secret=false for non-secret)")
	globalLaunchVarUpdateCmd.Flags().BoolVar(&globalLaunchVarUpdateReadableByAutomatedRuns, "readable-by-automated-runs", false, "Let any automated run in the organization reveal this secret (=false clears)")
	globalLaunchVarDeleteCmd.Flags().BoolVarP(&globalLaunchVarDeleteForce, "force", "f", false, "Skip confirmation prompt")
}

var launchVarSetupClient = globalVarSetupClientDefault

func maskLaunchVarValue() string {
	return "********"
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

	table := ui.NewTable("KEY", "VALUE", "TYPE", "DESCRIPTION", "TESTS", "UPDATED")
	table.SetMinWidth(0, 20)
	table.SetMinWidth(1, 12)
	table.SetMinWidth(2, 8)
	table.SetMinWidth(3, 24)
	table.SetMinWidth(4, 5)
	table.SetMinWidth(5, 20)

	for _, v := range resp.Result {
		value := v.Value
		secrecy := "plain"
		if v.IsSecret {
			value = maskLaunchVarValue()
			secrecy = "secret"
		}
		desc := v.Description
		if desc == "" {
			desc = "-"
		}
		table.AddRow(v.Key, value, secrecy, desc, fmt.Sprintf("%d", v.AttachedTestCount), v.UpdatedAt)
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
	if globalLaunchVarGetReveal {
		return revealGlobalLaunchVar(cmd, client, variable, jsonMode)
	}
	if jsonMode {
		return encodeJSON(variable)
	}

	ui.PrintInfo("Launch variable: %s", variable.Key)
	ui.PrintDim("  ID:          %s", variable.ID)
	value := variable.Value
	if variable.IsSecret {
		value = maskLaunchVarValue()
	}
	ui.PrintDim("  Value:       %s", value)
	ui.PrintDim("  Secret:      %t", variable.IsSecret)
	if variable.Description != "" {
		ui.PrintDim("  Description: %s", variable.Description)
	}
	ui.PrintDim("  Tests:       %d", variable.AttachedTestCount)
	ui.PrintDim("  Updated:     %s", variable.UpdatedAt)
	return nil
}

func revealGlobalLaunchVar(cmd *cobra.Command, client *api.Client, variable api.OrgLaunchVariable, jsonMode bool) error {
	if variable.Kind == string(api.LaunchConfigurationKindIosArguments) && !jsonMode {
		return fmt.Errorf("launch variable '%s' is an iOS argument set; reveal it with --reveal --json", variable.Key)
	}

	ui.StartSpinner("Revealing launch variable...")
	resp, err := client.RevealOrgLaunchVariable(cmd.Context(), variable.ID)
	ui.StopSpinner()
	if err != nil {
		return fmt.Errorf("failed to reveal launch variable '%s': %w", variable.Key, err)
	}

	if jsonMode {
		return encodeJSON(resp.Result)
	}
	fmt.Print(resp.Result.Value)
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

	opts := api.OrgLaunchVariableWriteOptions{IsSecret: &globalLaunchVarCreateSecret}
	if cmd.Flags().Changed("readable-by-automated-runs") {
		opts.ReadableByAutomatedRuns = &globalLaunchVarCreateReadableByAutomatedRuns
	}

	ui.StartSpinner("Creating launch variable...")
	resp, err := client.AddOrgLaunchVariable(
		cmd.Context(),
		key,
		value,
		descPtr,
		opts,
	)
	ui.StopSpinner()
	if err != nil {
		ui.PrintError("Failed to create launch variable: %v", err)
		return err
	}
	if err := requireAutomatedRunsMarkApplied(opts.ReadableByAutomatedRuns, resp.Result.ReadableByAutomatedRuns); err != nil {
		ui.PrintError("%v", err)
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
	var secretPtr, readableByAutomatedRunsPtr *bool
	if cmd.Flags().Changed("key") {
		keyPtr = &globalLaunchVarUpdateKey
	}
	if cmd.Flags().Changed("value") {
		valuePtr = &globalLaunchVarUpdateValue
	}
	if cmd.Flags().Changed("description") {
		descPtr = &globalLaunchVarUpdateDescription
	}
	if cmd.Flags().Changed("secret") {
		secretPtr = &globalLaunchVarUpdateSecret
	}
	if cmd.Flags().Changed("readable-by-automated-runs") {
		readableByAutomatedRunsPtr = &globalLaunchVarUpdateReadableByAutomatedRuns
	}
	if keyPtr == nil && valuePtr == nil && descPtr == nil && secretPtr == nil && readableByAutomatedRunsPtr == nil {
		err := fmt.Errorf("nothing to update; provide at least one of --key, --value, --description, --secret, or --readable-by-automated-runs")
		ui.PrintError("%v", err)
		return err
	}

	ui.StartSpinner("Updating launch variable...")
	resp, err := client.UpdateOrgLaunchVariable(
		cmd.Context(),
		variable.ID,
		keyPtr,
		valuePtr,
		descPtr,
		api.OrgLaunchVariableWriteOptions{IsSecret: secretPtr, ReadableByAutomatedRuns: readableByAutomatedRunsPtr},
	)
	ui.StopSpinner()
	if err != nil {
		ui.PrintError("Failed to update launch variable: %v", err)
		return err
	}
	if err := requireAutomatedRunsMarkApplied(readableByAutomatedRunsPtr, resp.Result.ReadableByAutomatedRuns); err != nil {
		ui.PrintError("%v", err)
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
