// Package main provides handler implementations for the module command.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

// moduleBlocksFile represents the structure of a module blocks YAML file.
type moduleBlocksFile struct {
	Blocks []interface{} `yaml:"blocks"`
}

// resolveModuleNameOrID resolves a module name or UUID to a module ID and name.
// It first checks if the input looks like a UUID, then searches by name in the module list.
func resolveModuleNameOrID(cmd *cobra.Command, client *api.Client, nameOrID string) (moduleID, moduleName string, err error) {
	// If it looks like a UUID, try to fetch directly
	if looksLikeUUID(nameOrID) {
		resp, err := client.GetModule(cmd.Context(), nameOrID)
		if err == nil {
			return resp.Result.ID, resp.Result.Name, nil
		}
	}

	// Otherwise, list modules and search by name
	listResp, err := client.ListModules(cmd.Context())
	if err != nil {
		return "", "", fmt.Errorf("failed to list modules: %w", err)
	}

	for _, m := range listResp.Result {
		if strings.EqualFold(m.Name, nameOrID) {
			return m.ID, m.Name, nil
		}
	}

	return "", "", fmt.Errorf("module \"%s\" not found", nameOrID)
}

// runModuleList handles the module list command.
func runModuleList(cmd *cobra.Command, args []string) error {
	// Check JSON output flag
	jsonOutput := moduleListJSON
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
		ui.StartSpinner("Fetching modules...")
	}

	resp, err := client.ListModules(cmd.Context())
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to list modules: %v", err)
		return err
	}

	// Apply search filter if specified
	modules := resp.Result
	if moduleListSearch != "" {
		query := strings.ToLower(moduleListSearch)
		var filtered []api.CLIModuleResponse
		for _, m := range modules {
			nameLower := strings.ToLower(m.Name)
			descLower := strings.ToLower(m.Description)
			if strings.Contains(nameLower, query) || strings.Contains(descLower, query) {
				filtered = append(filtered, m)
			}
		}
		modules = filtered
	}

	if jsonOutput {
		output := make([]map[string]interface{}, 0, len(modules))
		for _, m := range modules {
			item := map[string]interface{}{
				"id":          m.ID,
				"name":        m.Name,
				"description": m.Description,
				"blocks":      len(m.Blocks),
			}
			output = append(output, item)
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(modules) == 0 {
		if moduleListSearch != "" {
			ui.PrintInfo("No modules found matching \"%s\"", moduleListSearch)
		} else {
			ui.PrintInfo("No modules found")
		}
		ui.Println()
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Create a module:", Command: "revyl module create <name> --from-file <blocks.yaml>"},
		})
		return nil
	}

	ui.Println()
	ui.PrintInfo("Modules (%d)", len(modules))
	ui.Println()

	table := ui.NewTable("NAME", "ID", "STEPS", "DESCRIPTION")
	table.SetMinWidth(0, 16) // NAME
	table.SetMinWidth(1, 36) // ID
	table.SetMinWidth(2, 6)  // STEPS
	table.SetMinWidth(3, 20) // DESCRIPTION

	for _, m := range modules {
		desc := m.Description
		if desc == "" {
			desc = "-"
		}
		if len(desc) > 50 {
			desc = desc[:47] + "..."
		}
		table.AddRow(m.Name, m.ID, fmt.Sprintf("%d", len(m.Blocks)), desc)
	}

	table.Render()

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Get module details:", Command: "revyl module get <name>"},
		{Label: "Insert into a test:", Command: "revyl module insert <name>"},
	})

	return nil
}

// runModuleGet handles the module get command.
func runModuleGet(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	ui.StartSpinner("Fetching module...")

	moduleID, _, err := resolveModuleNameOrID(cmd, client, nameOrID)
	if err != nil {
		ui.StopSpinner()
		ui.PrintError("%v", err)
		return err
	}

	resp, err := client.GetModule(cmd.Context(), moduleID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to get module: %v", err)
		return err
	}

	m := resp.Result

	// Check JSON output
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")
	if jsonOutput {
		data, _ := json.MarshalIndent(m, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.Println()
	ui.PrintInfo("Module: %s", m.Name)
	ui.PrintDim("  ID:          %s", m.ID)
	if m.Description != "" {
		ui.PrintDim("  Description: %s", m.Description)
	}
	ui.PrintDim("  Blocks:      %d", len(m.Blocks))
	ui.PrintDim("  Created:     %s", m.CreatedAt)
	ui.PrintDim("  Updated:     %s", m.UpdatedAt)

	// Print blocks as YAML
	if len(m.Blocks) > 0 {
		ui.Println()
		ui.PrintInfo("Blocks:")
		blocksYAML, err := yaml.Marshal(map[string]interface{}{"blocks": m.Blocks})
		if err == nil {
			fmt.Println(string(blocksYAML))
		}
	}

	return nil
}

// runModuleCreate handles the module create command.
func runModuleCreate(cmd *cobra.Command, args []string) error {
	moduleName := args[0]

	// Validate module name
	if err := validateResourceName(moduleName, "module"); err != nil {
		ui.PrintError("%v", err)
		return err
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Read blocks from file
	blocksData, err := os.ReadFile(moduleCreateFromFile)
	if err != nil {
		ui.PrintError("Failed to read file: %v", err)
		return fmt.Errorf("failed to read file: %w", err)
	}

	var blocksFile moduleBlocksFile
	if err := yaml.Unmarshal(blocksData, &blocksFile); err != nil {
		ui.PrintError("Failed to parse YAML: %v", err)
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	if len(blocksFile.Blocks) == 0 {
		ui.PrintError("No blocks found in file. Expected a 'blocks:' key with an array of blocks.")
		return fmt.Errorf("no blocks found in file")
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	ui.StartSpinner("Creating module...")

	req := &api.CLICreateModuleRequest{
		Name:        moduleName,
		Description: moduleCreateDescription,
		Blocks:      blocksFile.Blocks,
	}

	resp, err := client.CreateModule(cmd.Context(), req)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to create module: %v", err)
		return err
	}

	ui.PrintSuccess("Module created: %s", resp.Result.Name)
	ui.PrintDim("  ID: %s", resp.Result.ID)
	ui.PrintDim("  Blocks: %d", len(resp.Result.Blocks))

	ui.Println()
	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Insert into a test:", Command: fmt.Sprintf("revyl module insert %s", moduleName)},
		{Label: "View module:", Command: fmt.Sprintf("revyl module get %s", moduleName)},
	})

	return nil
}

// runModuleUpdate handles the module update command.
// Fetches the current module version first for optimistic locking, then sends
// the update with expected_version. Returns a clear message on 409 conflict.
func runModuleUpdate(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	ui.StartSpinner("Resolving module...")

	moduleID, _, err := resolveModuleNameOrID(cmd, client, nameOrID)
	if err != nil {
		ui.StopSpinner()
		ui.PrintError("%v", err)
		return err
	}

	currentModule, err := client.GetModule(cmd.Context(), moduleID)
	if err != nil {
		ui.StopSpinner()
		ui.PrintError("Failed to fetch current module: %v", err)
		return err
	}

	// Build update request
	req := &api.CLIUpdateModuleRequest{}
	currentVersion := currentModule.Result.Version
	req.ExpectedVersion = &currentVersion
	hasUpdate := false

	if moduleUpdateName != "" {
		req.Name = &moduleUpdateName
		hasUpdate = true
	}

	if cmd.Flags().Changed("description") {
		req.Description = &moduleUpdateDescription
		hasUpdate = true
	}

	if moduleUpdateFromFile != "" {
		blocksData, err := os.ReadFile(moduleUpdateFromFile)
		if err != nil {
			ui.StopSpinner()
			ui.PrintError("Failed to read file: %v", err)
			return fmt.Errorf("failed to read file: %w", err)
		}

		var blocksFile moduleBlocksFile
		if err := yaml.Unmarshal(blocksData, &blocksFile); err != nil {
			ui.StopSpinner()
			ui.PrintError("Failed to parse YAML: %v", err)
			return fmt.Errorf("failed to parse YAML: %w", err)
		}

		if len(blocksFile.Blocks) == 0 {
			ui.StopSpinner()
			ui.PrintError("No blocks found in file.")
			return fmt.Errorf("no blocks found in file")
		}

		req.Blocks = &blocksFile.Blocks
		hasUpdate = true
	}

	if !hasUpdate {
		ui.StopSpinner()
		ui.PrintError("No updates specified. Use --name, --description, or --from-file.")
		return fmt.Errorf("no updates specified")
	}

	ui.StartSpinner("Updating module...")

	resp, err := client.UpdateModule(cmd.Context(), moduleID, req)
	ui.StopSpinner()

	if err != nil {
		if apiErr, ok := err.(*api.APIError); ok && apiErr.StatusCode == 409 {
			ui.PrintError("Module was modified by another user since you last fetched it")
			ui.PrintDim("  %s", apiErr.Detail)
			ui.Println()
			ui.PrintInfo("Fetch the latest version and try again.")
			return err
		}
		ui.PrintError("Failed to update module: %v", err)
		return err
	}

	ui.PrintSuccess("Module updated: %s", resp.Result.Name)
	ui.PrintDim("  ID: %s", resp.Result.ID)

	return nil
}

// runModuleDelete handles the module delete command.
func runModuleDelete(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	ui.StartSpinner("Resolving module...")

	moduleID, moduleName, err := resolveModuleNameOrID(cmd, client, nameOrID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("%v", err)
		return err
	}

	// Confirm deletion
	if !moduleDeleteForce {
		ui.Println()
		ui.PrintInfo("Delete module \"%s\"?", moduleName)
		ui.PrintDim("  ID: %s", moduleID)
		ui.Println()

		confirmed, err := ui.PromptConfirm("Are you sure?", false)
		if err != nil || !confirmed {
			ui.PrintInfo("Cancelled")
			return nil
		}
	}

	ui.StartSpinner("Deleting module...")

	resp, err := client.DeleteModule(cmd.Context(), moduleID)
	ui.StopSpinner()

	if err != nil {
		// Check for 409 conflict (module in use)
		if apiErr, ok := err.(*api.APIError); ok && apiErr.StatusCode == 409 {
			ui.PrintError("Cannot delete module \"%s\": it is in use by tests", moduleName)
			ui.PrintDim("  %s", apiErr.Detail)
			ui.Println()
			ui.PrintInfo("Remove the module from those tests first, or use a different module.")
			return err
		}
		ui.PrintError("Failed to delete module: %v", err)
		return err
	}

	ui.PrintSuccess("Module deleted: %s", moduleName)
	if resp.Message != "" {
		ui.PrintDim("  %s", resp.Message)
	}

	return nil
}

// runModuleVersions handles the module versions command.
// Lists the version history for a module in a tabular format.
func runModuleVersions(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	ui.StartSpinner("Fetching module versions...")

	moduleID, moduleName, err := resolveModuleNameOrID(cmd, client, nameOrID)
	if err != nil {
		ui.StopSpinner()
		ui.PrintError("%v", err)
		return err
	}

	resp, err := client.GetModuleVersions(cmd.Context(), moduleID, 50, 0)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to get module versions: %v", err)
		return err
	}

	if len(resp.Versions) == 0 {
		ui.PrintInfo("No version history for module \"%s\"", moduleName)
		return nil
	}

	ui.Println()
	ui.PrintInfo("Version history for module \"%s\"", moduleName)
	ui.Println()

	table := ui.NewTable("VERSION", "MODIFIED BY", "MODIFIED AT")
	table.SetMinWidth(0, 8)  // VERSION
	table.SetMinWidth(1, 20) // MODIFIED BY
	table.SetMinWidth(2, 20) // MODIFIED AT

	for _, v := range resp.Versions {
		modifiedBy := "-"
		if v.ModifiedByEmail != nil && *v.ModifiedByEmail != "" {
			modifiedBy = *v.ModifiedByEmail
		} else if v.ModifiedBy != nil && *v.ModifiedBy != "" {
			modifiedBy = *v.ModifiedBy
		}
		table.AddRow(fmt.Sprintf("%d", v.Version), modifiedBy, v.CreatedAt)
	}

	table.Render()

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Restore a version:", Command: fmt.Sprintf("revyl module restore %s --version <n>", nameOrID)},
	})

	return nil
}

// runModuleRestore handles the module restore command.
// Restores a module to the specified historical version.
func runModuleRestore(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	ui.StartSpinner("Restoring module...")

	moduleID, moduleName, err := resolveModuleNameOrID(cmd, client, nameOrID)
	if err != nil {
		ui.StopSpinner()
		ui.PrintError("%v", err)
		return err
	}

	if err := client.RestoreModuleVersion(cmd.Context(), moduleID, moduleRestoreVersion); err != nil {
		ui.StopSpinner()
		ui.PrintError("Failed to restore module: %v", err)
		return err
	}

	ui.StopSpinner()

	ui.PrintSuccess("Module \"%s\" restored to version %d", moduleName, moduleRestoreVersion)

	ui.Println()
	ui.PrintNextSteps([]ui.NextStep{
		{Label: "View restored module:", Command: fmt.Sprintf("revyl module get %s", nameOrID)},
		{Label: "View version history:", Command: fmt.Sprintf("revyl module versions %s", nameOrID)},
	})

	return nil
}

// runModuleUsage handles the module usage command.
// Lists all tests that reference the given module via module_import blocks.
func runModuleUsage(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	ui.StartSpinner("Fetching module usage...")

	moduleID, moduleName, err := resolveModuleNameOrID(cmd, client, nameOrID)
	if err != nil {
		ui.StopSpinner()
		ui.PrintError("%v", err)
		return err
	}

	resp, err := client.GetModuleUsage(cmd.Context(), moduleID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to get module usage: %v", err)
		return err
	}

	if len(resp.Tests) == 0 {
		ui.PrintInfo("Module \"%s\" is not used by any tests", moduleName)
		return nil
	}

	ui.Println()
	ui.PrintInfo("Module \"%s\" is used by %d test(s)", moduleName, len(resp.Tests))
	ui.Println()

	table := ui.NewTable("TEST NAME", "TEST ID")
	table.SetMinWidth(0, 20) // TEST NAME
	table.SetMinWidth(1, 36) // TEST ID

	for _, t := range resp.Tests {
		table.AddRow(t.Name, t.Id)
	}

	table.Render()

	return nil
}

// runModuleInsert handles the module insert command.
// It outputs a ready-to-paste YAML snippet for a module_import block.
func runModuleInsert(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	moduleID, moduleName, err := resolveModuleNameOrID(cmd, client, nameOrID)
	if err != nil {
		ui.PrintError("%v", err)
		return err
	}

	// Check JSON output
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")
	if jsonOutput {
		data, _ := json.MarshalIndent(map[string]string{
			"type":             "module_import",
			"step_description": moduleName,
			"module_id":        moduleID,
		}, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	fmt.Println("# Paste this into your test YAML:")
	fmt.Printf("- type: module_import\n")
	fmt.Printf("  step_description: \"%s\"\n", moduleName)
	fmt.Printf("  module_id: \"%s\"\n", moduleID)

	return nil
}
