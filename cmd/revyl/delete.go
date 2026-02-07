// Package main provides delete commands for removing tests, workflows, and builds.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// Delete command flags (used by test delete, workflow delete, build delete)
var (
	deleteForce        bool
	deleteRemoteOnly   bool
	deleteLocalOnly    bool
	deleteBuildVersion string
)

// runDeleteTest handles the delete test command execution.
func runDeleteTest(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	// Get API key
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Load project config
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	cfg, cfgErr := config.LoadProjectConfig(configPath)

	// Resolve name to ID
	testID, testName, err := resolveTestNameOrID(cmd.Context(), client, cfg, nameOrID)
	if err != nil && !deleteLocalOnly {
		return err
	}

	// For local-only, we just need the name
	if deleteLocalOnly && testName == "" {
		testName = nameOrID
	}

	// Build info about what will be deleted
	localFilePath := filepath.Join(cwd, ".revyl", "tests", testName+".yaml")
	localFileExists := fileExists(localFilePath)
	hasConfigAlias := cfg != nil && cfg.Tests != nil && cfg.Tests[testName] != ""

	// Show what will be deleted
	if !deleteForce {
		ui.Println()
		ui.PrintInfo("Delete test \"%s\"?", testName)

		if !deleteLocalOnly && testID != "" {
			ui.PrintDim("  - Remote: will be deleted from Revyl")
		}
		if !deleteRemoteOnly && localFileExists {
			ui.PrintDim("  - Local:  %s", localFilePath)
		}
		if !deleteRemoteOnly && hasConfigAlias {
			ui.PrintDim("  - Config: .revyl/config.yaml (tests.%s)", testName)
		}

		ui.Println()
		confirmed, err := ui.PromptConfirm("Are you sure?", false)
		if err != nil || !confirmed {
			ui.PrintInfo("Cancelled")
			return nil
		}
	}

	// Delete from remote
	if !deleteLocalOnly && testID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		_, err := client.DeleteTest(ctx, testID)
		if err != nil {
			var apiErr *api.APIError
			if errors.As(err, &apiErr) {
				if apiErr.StatusCode == 404 {
					ui.PrintWarning("Test not found on remote (may already be deleted)")
				} else if apiErr.StatusCode == 403 {
					ui.PrintError("Permission denied")
					return fmt.Errorf("not authorized to delete this test")
				} else {
					return fmt.Errorf("failed to delete test: %w", err)
				}
			} else {
				return fmt.Errorf("failed to delete test: %w", err)
			}
		} else {
			ui.PrintSuccess("Deleted from Revyl")
		}
	}

	// Delete local file
	if !deleteRemoteOnly && localFileExists {
		if err := os.Remove(localFilePath); err != nil {
			ui.PrintWarning("Failed to remove local file: %v", err)
		} else {
			ui.PrintSuccess("Removed %s", localFilePath)
		}
	}

	// Remove from config
	if !deleteRemoteOnly && hasConfigAlias && cfgErr == nil {
		delete(cfg.Tests, testName)
		if err := config.WriteProjectConfig(configPath, cfg); err != nil {
			ui.PrintWarning("Failed to update config: %v", err)
		} else {
			ui.PrintSuccess("Removed alias from .revyl/config.yaml")
		}
	}

	// Handle JSON output
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if jsonOutput {
		output := map[string]interface{}{
			"success":     true,
			"test_name":   testName,
			"test_id":     testID,
			"remote_only": deleteRemoteOnly,
			"local_only":  deleteLocalOnly,
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.Println()
	ui.PrintSuccess("Test \"%s\" deleted successfully.", testName)
	return nil
}

// runDeleteWorkflow handles the delete workflow command execution.
func runDeleteWorkflow(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	// Get API key
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Load project config
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	cfg, cfgErr := config.LoadProjectConfig(configPath)

	// Resolve name to ID
	workflowID, workflowName, err := resolveWorkflowNameOrID(cfg, nameOrID)
	if err != nil {
		return err
	}

	hasConfigAlias := cfg != nil && cfg.Workflows != nil && cfg.Workflows[workflowName] != ""

	// Show what will be deleted
	if !deleteForce {
		ui.Println()
		ui.PrintInfo("Delete workflow \"%s\"?", workflowName)
		ui.PrintDim("  - Remote: will be deleted from Revyl")
		if hasConfigAlias {
			ui.PrintDim("  - Config: .revyl/config.yaml (workflows.%s)", workflowName)
		}

		ui.Println()
		confirmed, err := ui.PromptConfirm("Are you sure?", false)
		if err != nil || !confirmed {
			ui.PrintInfo("Cancelled")
			return nil
		}
	}

	// Delete from remote
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = client.DeleteWorkflow(ctx, workflowID)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			if apiErr.StatusCode == 404 {
				ui.PrintWarning("Workflow not found on remote (may already be deleted)")
			} else if apiErr.StatusCode == 403 {
				ui.PrintError("Permission denied")
				return fmt.Errorf("not authorized to delete this workflow")
			} else {
				return fmt.Errorf("failed to delete workflow: %w", err)
			}
		} else {
			return fmt.Errorf("failed to delete workflow: %w", err)
		}
	} else {
		ui.PrintSuccess("Deleted from Revyl")
	}

	// Remove from config
	if hasConfigAlias && cfgErr == nil {
		delete(cfg.Workflows, workflowName)
		if err := config.WriteProjectConfig(configPath, cfg); err != nil {
			ui.PrintWarning("Failed to update config: %v", err)
		} else {
			ui.PrintSuccess("Removed alias from .revyl/config.yaml")
		}
	}

	// Handle JSON output
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if jsonOutput {
		output := map[string]interface{}{
			"success":       true,
			"workflow_name": workflowName,
			"workflow_id":   workflowID,
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.Println()
	ui.PrintSuccess("Workflow \"%s\" deleted successfully.", workflowName)
	return nil
}

// runDeleteBuild handles the delete build command execution.
func runDeleteBuild(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	// Get API key
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Get dev mode flag
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Load project config
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	cfg, cfgErr := config.LoadProjectConfig(configPath)

	// Resolve name to ID
	buildVarID, buildVarName, err := resolveBuildVarNameOrID(cmd.Context(), client, nameOrID)
	if err != nil {
		return err
	}

	// If deleting specific version
	if deleteBuildVersion != "" {
		return deleteSpecificBuildVersion(cmd, client, buildVarID, buildVarName, deleteBuildVersion)
	}

	// Check if build var is referenced in config
	var configRefs []string
	if cfg != nil && cfg.Build.Variants != nil {
		for variantName, variant := range cfg.Build.Variants {
			if variant.BuildVarID == buildVarID {
				configRefs = append(configRefs, variantName)
			}
		}
	}

	// Show what will be deleted
	if !deleteForce {
		ui.Println()
		ui.PrintInfo("Delete build variable \"%s\"?", buildVarName)
		ui.PrintDim("  - Remote: will delete build variable and ALL versions")
		if len(configRefs) > 0 {
			ui.PrintDim("  - Config: will remove build_var_id from variants: %v", configRefs)
		}

		ui.Println()
		confirmed, err := ui.PromptConfirm("Are you sure?", false)
		if err != nil || !confirmed {
			ui.PrintInfo("Cancelled")
			return nil
		}
	}

	// Delete from remote
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.DeleteBuildVar(ctx, buildVarID)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			if apiErr.StatusCode == 404 {
				ui.PrintWarning("Build variable not found on remote (may already be deleted)")
			} else if apiErr.StatusCode == 403 {
				ui.PrintError("Permission denied")
				return fmt.Errorf("not authorized to delete this build variable")
			} else {
				return fmt.Errorf("failed to delete build variable: %w", err)
			}
		} else {
			return fmt.Errorf("failed to delete build variable: %w", err)
		}
	} else {
		ui.PrintSuccess("Deleted from Revyl")
		if resp.DetachedTests > 0 {
			ui.PrintInfo("Detached %d test(s) from this build", resp.DetachedTests)
		}
	}

	// Remove from config
	if len(configRefs) > 0 && cfgErr == nil {
		for _, variantName := range configRefs {
			variant := cfg.Build.Variants[variantName]
			variant.BuildVarID = ""
			cfg.Build.Variants[variantName] = variant
		}
		if err := config.WriteProjectConfig(configPath, cfg); err != nil {
			ui.PrintWarning("Failed to update config: %v", err)
		} else {
			ui.PrintSuccess("Removed build_var_id from config variants")
		}
	}

	// Handle JSON output
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if jsonOutput {
		output := map[string]interface{}{
			"success":        true,
			"build_var_name": buildVarName,
			"build_var_id":   buildVarID,
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.Println()
	ui.PrintSuccess("Build variable \"%s\" deleted successfully.", buildVarName)
	return nil
}

// deleteSpecificBuildVersion deletes a specific build version.
func deleteSpecificBuildVersion(cmd *cobra.Command, client *api.Client, buildVarID, buildVarName, versionStr string) error {
	// Find the version ID
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	versions, err := client.ListBuildVersions(ctx, buildVarID)
	if err != nil {
		return fmt.Errorf("failed to list versions: %w", err)
	}

	var versionID string
	for _, v := range versions {
		if v.Version == versionStr || v.ID == versionStr {
			versionID = v.ID
			break
		}
	}

	if versionID == "" {
		return fmt.Errorf("version \"%s\" not found for build \"%s\"", versionStr, buildVarName)
	}

	// Show what will be deleted
	if !deleteForce {
		ui.Println()
		ui.PrintInfo("Delete version \"%s\" from build \"%s\"?", versionStr, buildVarName)

		ui.Println()
		confirmed, err := ui.PromptConfirm("Are you sure?", false)
		if err != nil || !confirmed {
			ui.PrintInfo("Cancelled")
			return nil
		}
	}

	// Delete the version
	_, err = client.DeleteBuildVersion(ctx, versionID)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			if apiErr.StatusCode == 404 {
				ui.PrintWarning("Version not found (may already be deleted)")
			} else if apiErr.StatusCode == 403 {
				ui.PrintError("Permission denied")
				return fmt.Errorf("not authorized to delete this version")
			} else {
				return fmt.Errorf("failed to delete version: %w", err)
			}
		} else {
			return fmt.Errorf("failed to delete version: %w", err)
		}
	} else {
		ui.PrintSuccess("Deleted version from Revyl")
	}

	// Handle JSON output
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if jsonOutput {
		output := map[string]interface{}{
			"success":        true,
			"build_var_name": buildVarName,
			"build_var_id":   buildVarID,
			"version":        versionStr,
			"version_id":     versionID,
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.Println()
	ui.PrintSuccess("Version \"%s\" deleted successfully.", versionStr)
	return nil
}

// resolveTestNameOrID resolves a test name or ID to both values.
func resolveTestNameOrID(ctx context.Context, client *api.Client, cfg *config.ProjectConfig, nameOrID string) (testID, testName string, err error) {
	// Check if it's in config aliases
	if cfg != nil && cfg.Tests != nil {
		if id, ok := cfg.Tests[nameOrID]; ok {
			return id, nameOrID, nil
		}
	}

	// Check if it looks like a UUID (try to use as ID directly)
	if looksLikeUUID(nameOrID) {
		// Verify it exists
		test, err := client.GetTest(ctx, nameOrID)
		if err == nil {
			return nameOrID, test.Name, nil
		}
	}

	// Try to find by name in org tests
	result, err := client.ListOrgTests(ctx, 100, 0)
	if err != nil {
		return "", "", fmt.Errorf("failed to list tests: %w", err)
	}

	for _, t := range result.Tests {
		if t.Name == nameOrID {
			return t.ID, t.Name, nil
		}
	}

	return "", "", fmt.Errorf("test \"%s\" not found", nameOrID)
}

// resolveWorkflowNameOrID resolves a workflow name or ID to both values.
func resolveWorkflowNameOrID(cfg *config.ProjectConfig, nameOrID string) (workflowID, workflowName string, err error) {
	// Check if it's in config aliases
	if cfg != nil && cfg.Workflows != nil {
		if id, ok := cfg.Workflows[nameOrID]; ok {
			return id, nameOrID, nil
		}
	}

	// Check if it looks like a UUID
	if looksLikeUUID(nameOrID) {
		return nameOrID, nameOrID, nil
	}

	return "", "", fmt.Errorf("workflow \"%s\" not found in config (use workflow ID or add alias to .revyl/config.yaml)", nameOrID)
}

// resolveBuildVarNameOrID resolves a build variable name or ID to both values.
func resolveBuildVarNameOrID(ctx context.Context, client *api.Client, nameOrID string) (buildVarID, buildVarName string, err error) {
	// Check if it looks like a UUID
	if looksLikeUUID(nameOrID) {
		bv, err := client.GetBuildVar(ctx, nameOrID)
		if err == nil {
			return nameOrID, bv.Name, nil
		}
	}

	// Search by name
	result, err := client.ListOrgBuildVars(ctx, "", 1, 100)
	if err != nil {
		return "", "", fmt.Errorf("failed to list build variables: %w", err)
	}

	for _, bv := range result.Items {
		if bv.Name == nameOrID {
			return bv.ID, bv.Name, nil
		}
	}

	return "", "", fmt.Errorf("build variable \"%s\" not found", nameOrID)
}

// fileExists checks if a file exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
