// Package main provides the script command for code execution script management.
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

var (
	scriptListJSON    bool
	scriptListRuntime string

	scriptCreateRuntime     string
	scriptCreateDescription string
	scriptCreateFromFile    string

	scriptUpdateFromFile    string
	scriptUpdateDescription string
	scriptUpdateName        string

	scriptDeleteForce bool
)

var scriptCmd = &cobra.Command{
	Use:   "script",
	Short: "Manage code execution scripts",
	Long: `Manage code execution scripts used by code_execution blocks in tests.

Scripts are sandboxed code snippets (Python, JavaScript, TypeScript, Bash)
that run during test execution to perform custom logic, data validation, or
API calls.

COMMANDS:
  list    - List all scripts
  get     - Show a script's details and code
  create  - Create a new script from a local file
  update  - Update an existing script
  delete  - Delete a script
  usage   - Show tests that use a script
  insert  - Output a code_execution YAML snippet

EXAMPLES:
  revyl script list
  revyl script list --runtime python
  revyl script get my-validator
  revyl script create my-validator --runtime python --file validate.py
  revyl script update my-validator --file validate.py
  revyl script delete my-validator
  revyl script usage my-validator
  revyl script insert my-validator`,
}

var scriptListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all scripts",
	Long: `List all code execution scripts in your organization.

EXAMPLES:
  revyl script list
  revyl script list --json
  revyl script list --runtime python`,
	RunE: runScriptList,
}

var scriptGetCmd = &cobra.Command{
	Use:   "get <name|id>",
	Short: "Show a script's details and code",
	Long: `Show a script's metadata and source code.

EXAMPLES:
  revyl script get my-validator
  revyl script get abc-123-uuid`,
	Args: cobra.ExactArgs(1),
	RunE: runScriptGet,
}

var scriptCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new script from a local file",
	Long: `Create a new code execution script from a local source file.

EXAMPLES:
  revyl script create my-validator --runtime python --file validate.py
  revyl script create price-check --runtime javascript --file check.js --description "Validate prices"`,
	Args: cobra.ExactArgs(1),
	RunE: runScriptCreate,
}

var scriptUpdateCmd = &cobra.Command{
	Use:   "update <name|id>",
	Short: "Update an existing script",
	Long: `Update a script's name, description, or code.

EXAMPLES:
  revyl script update my-validator --file validate.py
  revyl script update my-validator --name "new-name"
  revyl script update my-validator --description "Updated validator"`,
	Args: cobra.ExactArgs(1),
	RunE: runScriptUpdate,
}

var scriptDeleteCmd = &cobra.Command{
	Use:   "delete <name|id>",
	Short: "Delete a script",
	Long: `Delete a code execution script.

EXAMPLES:
  revyl script delete my-validator
  revyl script delete my-validator --force`,
	Args: cobra.ExactArgs(1),
	RunE: runScriptDelete,
}

var scriptUsageCmd = &cobra.Command{
	Use:   "usage <name|id>",
	Short: "Show tests that use a script",
	Long: `Show all tests that reference a script via code_execution blocks.

EXAMPLES:
  revyl script usage my-validator`,
	Args: cobra.ExactArgs(1),
	RunE: runScriptUsage,
}

var scriptInsertCmd = &cobra.Command{
	Use:   "insert <name|id>",
	Short: "Output a code_execution YAML snippet",
	Long: `Output a ready-to-paste YAML snippet for using a script in a test.

EXAMPLES:
  revyl script insert my-validator`,
	Args: cobra.ExactArgs(1),
	RunE: runScriptInsert,
}

func init() {
	scriptCmd.AddCommand(scriptListCmd)
	scriptCmd.AddCommand(scriptGetCmd)
	scriptCmd.AddCommand(scriptCreateCmd)
	scriptCmd.AddCommand(scriptUpdateCmd)
	scriptCmd.AddCommand(scriptDeleteCmd)
	scriptCmd.AddCommand(scriptUsageCmd)
	scriptCmd.AddCommand(scriptInsertCmd)

	scriptListCmd.Flags().BoolVar(&scriptListJSON, "json", false, "Output as JSON")
	scriptListCmd.Flags().StringVar(&scriptListRuntime, "runtime", "", "Filter by runtime (python, javascript, typescript, bash)")

	scriptCreateCmd.Flags().StringVar(&scriptCreateRuntime, "runtime", "", "Script runtime (python, javascript, typescript, bash)")
	scriptCreateCmd.Flags().StringVar(&scriptCreateFromFile, "file", "", "Path to source file")
	scriptCreateCmd.Flags().StringVar(&scriptCreateDescription, "description", "", "Script description")
	_ = scriptCreateCmd.MarkFlagRequired("file")
	_ = scriptCreateCmd.MarkFlagRequired("runtime")

	scriptUpdateCmd.Flags().StringVar(&scriptUpdateFromFile, "file", "", "Path to source file with updated code")
	scriptUpdateCmd.Flags().StringVar(&scriptUpdateName, "name", "", "New script name")
	scriptUpdateCmd.Flags().StringVar(&scriptUpdateDescription, "description", "", "New script description")

	scriptDeleteCmd.Flags().BoolVarP(&scriptDeleteForce, "force", "f", false, "Skip confirmation prompt")
}

// resolveScriptNameOrID resolves a script name or UUID to a script ID and name.
func resolveScriptNameOrID(cmd *cobra.Command, client *api.Client, nameOrID string) (string, string, error) {
	if looksLikeUUID(nameOrID) {
		resp, err := client.GetScript(cmd.Context(), nameOrID)
		if err == nil {
			return resp.ID, resp.Name, nil
		}
	}

	listResp, err := client.ListScripts(cmd.Context(), "", 200, 0)
	if err != nil {
		return "", "", fmt.Errorf("failed to list scripts: %w", err)
	}

	for _, s := range listResp.Scripts {
		if strings.EqualFold(s.Name, nameOrID) {
			return s.ID, s.Name, nil
		}
	}

	return "", "", fmt.Errorf("script \"%s\" not found", nameOrID)
}

func runScriptList(cmd *cobra.Command, _ []string) error {
	jsonOutput := scriptListJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	if !jsonOutput {
		ui.StartSpinner("Fetching scripts...")
	}

	resp, err := client.ListScripts(cmd.Context(), scriptListRuntime, 200, 0)
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to list scripts: %v", err)
		return err
	}

	if jsonOutput {
		data, err := json.MarshalIndent(resp.Scripts, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON output: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	if len(resp.Scripts) == 0 {
		ui.PrintInfo("No scripts found.")
		return nil
	}

	ui.PrintInfo("Scripts (%d):", len(resp.Scripts))
	ui.Println()

	table := ui.NewTable("NAME", "RUNTIME", "ID", "DESCRIPTION")
	for _, s := range resp.Scripts {
		desc := ""
		if s.Description != nil && *s.Description != "" {
			desc = *s.Description
		}
		table.AddRow(s.Name, s.Runtime, s.ID[:8], desc)
	}
	table.Render()

	return nil
}

func runScriptGet(cmd *cobra.Command, args []string) error {
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	scriptID, _, err := resolveScriptNameOrID(cmd, client, args[0])
	if err != nil {
		ui.PrintError("%v", err)
		return err
	}

	script, err := client.GetScript(cmd.Context(), scriptID)
	if err != nil {
		ui.PrintError("Failed to get script: %v", err)
		return err
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")
	if jsonOutput {
		data, err := json.MarshalIndent(script, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON output: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	ui.PrintInfo("Name:    %s", script.Name)
	ui.PrintInfo("ID:      %s", script.ID)
	ui.PrintInfo("Runtime: %s", script.Runtime)
	if script.Description != nil && *script.Description != "" {
		ui.PrintInfo("Desc:    %s", *script.Description)
	}
	ui.PrintInfo("Updated: %s", script.UpdatedAt)
	ui.Println()
	fmt.Println(script.Code)

	return nil
}

func runScriptCreate(cmd *cobra.Command, args []string) error {
	code, err := os.ReadFile(scriptCreateFromFile)
	if err != nil {
		ui.PrintError("Failed to read file: %v", err)
		return err
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	req := &api.CLICreateScriptRequest{
		Name:    args[0],
		Code:    string(code),
		Runtime: scriptCreateRuntime,
	}
	if scriptCreateDescription != "" {
		req.Description = &scriptCreateDescription
	}

	ui.StartSpinner("Creating script...")
	resp, err := client.CreateScript(cmd.Context(), req)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to create script: %v", err)
		return err
	}

	ui.PrintSuccess("Created script \"%s\" (ID: %s)", resp.Name, resp.ID)
	return nil
}

func runScriptUpdate(cmd *cobra.Command, args []string) error {
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	scriptID, scriptName, err := resolveScriptNameOrID(cmd, client, args[0])
	if err != nil {
		ui.PrintError("%v", err)
		return err
	}

	req := &api.CLIUpdateScriptRequest{}
	hasUpdate := false

	if scriptUpdateFromFile != "" {
		code, readErr := os.ReadFile(scriptUpdateFromFile)
		if readErr != nil {
			ui.PrintError("Failed to read file: %v", readErr)
			return readErr
		}
		codeStr := string(code)
		req.Code = &codeStr
		hasUpdate = true
	}
	if scriptUpdateName != "" {
		req.Name = &scriptUpdateName
		hasUpdate = true
	}
	if scriptUpdateDescription != "" {
		req.Description = &scriptUpdateDescription
		hasUpdate = true
	}

	if !hasUpdate {
		ui.PrintWarning("Nothing to update. Use --file, --name, or --description.")
		return nil
	}

	ui.StartSpinner("Updating script...")
	resp, err := client.UpdateScript(cmd.Context(), scriptID, req)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to update script: %v", err)
		return err
	}

	displayName := scriptName
	if resp.Name != "" {
		displayName = resp.Name
	}
	ui.PrintSuccess("Updated script \"%s\"", displayName)
	return nil
}

func runScriptDelete(cmd *cobra.Command, args []string) error {
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	scriptID, scriptName, err := resolveScriptNameOrID(cmd, client, args[0])
	if err != nil {
		ui.PrintError("%v", err)
		return err
	}

	if !scriptDeleteForce {
		ui.PrintWarning("Delete script \"%s\" (%s)? This cannot be undone.", scriptName, scriptID)
		confirmed, err := ui.PromptConfirm("Delete script \""+scriptName+"\"?", false)
		if err != nil || !confirmed {
			ui.PrintInfo("Cancelled.")
			return nil
		}
	}

	ui.StartSpinner("Deleting script...")
	err = client.DeleteScript(cmd.Context(), scriptID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to delete script: %v", err)
		return err
	}

	ui.PrintSuccess("Deleted script \"%s\"", scriptName)
	return nil
}

func runScriptUsage(cmd *cobra.Command, args []string) error {
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	scriptID, scriptName, err := resolveScriptNameOrID(cmd, client, args[0])
	if err != nil {
		ui.PrintError("%v", err)
		return err
	}

	ui.StartSpinner("Fetching usage...")
	resp, err := client.GetScriptUsage(cmd.Context(), scriptID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to get script usage: %v", err)
		return err
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")
	if jsonOutput {
		data, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if resp.Total == 0 {
		ui.PrintInfo("Script \"%s\" is not used by any tests.", scriptName)
		return nil
	}

	ui.PrintInfo("Script \"%s\" is used by %d test(s):", scriptName, resp.Total)
	ui.Println()

	table := ui.NewTable("TEST", "ID")
	for _, t := range resp.Tests {
		table.AddRow(t.Name, t.ID[:8])
	}
	table.Render()

	return nil
}

func runScriptInsert(cmd *cobra.Command, args []string) error {
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	scriptID, scriptName, err := resolveScriptNameOrID(cmd, client, args[0])
	if err != nil {
		ui.PrintError("%v", err)
		return err
	}

	fmt.Println("# Paste this into your test YAML:")
	fmt.Printf("- type: code_execution\n")
	fmt.Printf("  step_description: \"%s\"\n", scriptID)
	fmt.Printf("  # script: %s\n", scriptName)

	return nil
}
