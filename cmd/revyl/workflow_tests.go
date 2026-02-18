// Package main provides workflow test management commands.
//
// These commands allow adding and removing tests from existing workflows.
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

// workflowAddTestsCmd adds tests to an existing workflow.
var workflowAddTestsCmd = &cobra.Command{
	Use:   "add-tests <workflow> <test1> [test2...]",
	Short: "Add tests to an existing workflow",
	Long: `Add one or more tests to an existing workflow.

Tests already in the workflow are silently skipped (no duplicates).
Accepts test names (from .revyl/config.yaml) or UUIDs.

EXAMPLES:
  revyl workflow add-tests smoke-tests login-flow
  revyl workflow add-tests smoke-tests login-flow checkout payment
  revyl workflow add-tests 550e8400-... 6ba7b810-...`,
	Args: cobra.MinimumNArgs(2),
	RunE: runWorkflowAddTests,
}

// workflowRemoveTestsCmd removes tests from an existing workflow.
var workflowRemoveTestsCmd = &cobra.Command{
	Use:   "remove-tests <workflow> <test1> [test2...]",
	Short: "Remove tests from an existing workflow",
	Long: `Remove one or more tests from an existing workflow.

Tests not found in the workflow are silently skipped.
Accepts test names (from .revyl/config.yaml) or UUIDs.

EXAMPLES:
  revyl workflow remove-tests smoke-tests login-flow
  revyl workflow remove-tests smoke-tests login-flow checkout`,
	Args: cobra.MinimumNArgs(2),
	RunE: runWorkflowRemoveTests,
}

func init() {
	workflowCmd.AddCommand(workflowAddTestsCmd)
	workflowCmd.AddCommand(workflowRemoveTestsCmd)
}

// workflowTestsSetupClient creates an API client and resolves a workflow ID.
//
// Parameters:
//   - cmd: The cobra command (used for flag access)
//   - workflowNameOrID: Workflow name (from config) or UUID
//
// Returns:
//   - workflowID: The resolved workflow UUID
//   - cfg: The loaded project config (may be nil)
//   - client: Configured API client
//   - error: Any error during setup
func workflowTestsSetupClient(cmd *cobra.Command, workflowNameOrID string) (string, *config.ProjectConfig, *api.Client, error) {
	apiKey, err := getAPIKey()
	if err != nil {
		return "", nil, nil, err
	}

	cwd, _ := os.Getwd()
	cfg, _ := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	workflowID, _, err := resolveWorkflowID(cmd.Context(), workflowNameOrID, cfg, client)
	if err != nil {
		ui.PrintError("%v", err)
		return "", nil, nil, fmt.Errorf("workflow not found")
	}

	return workflowID, cfg, client, nil
}

// runWorkflowAddTests adds tests to an existing workflow (deduped).
func runWorkflowAddTests(cmd *cobra.Command, args []string) error {
	workflowNameOrID := args[0]
	testNamesOrIDs := args[1:]

	workflowID, cfg, client, err := workflowTestsSetupClient(cmd, workflowNameOrID)
	if err != nil {
		return err
	}

	// Get current workflow to fetch existing tests
	ui.StartSpinner("Fetching workflow...")
	workflow, err := client.GetWorkflow(cmd.Context(), workflowID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to get workflow: %v", err)
		return err
	}

	// Build set of existing test IDs
	existingSet := make(map[string]bool, len(workflow.Tests))
	for _, id := range workflow.Tests {
		existingSet[id] = true
	}

	// Resolve test names/IDs and append new ones
	newTestIDs := make([]string, len(workflow.Tests))
	copy(newTestIDs, workflow.Tests)
	var added, skipped []string

	ui.StartSpinner("Resolving tests...")
	for _, nameOrID := range testNamesOrIDs {
		testID, _, resolveErr := resolveTestID(cmd.Context(), nameOrID, cfg, client)
		if resolveErr != nil {
			ui.StopSpinner()
			ui.PrintError("Failed to resolve test '%s': %v", nameOrID, resolveErr)
			return resolveErr
		}
		if existingSet[testID] {
			skipped = append(skipped, nameOrID)
			continue
		}
		newTestIDs = append(newTestIDs, testID)
		existingSet[testID] = true
		added = append(added, nameOrID)
	}
	ui.StopSpinner()

	if len(added) == 0 {
		ui.PrintInfo("All specified tests are already in workflow '%s' (skipped: %s)", workflowNameOrID, strings.Join(skipped, ", "))
		return nil
	}

	ui.StartSpinner("Updating workflow...")
	err = client.UpdateWorkflowTests(cmd.Context(), workflowID, newTestIDs)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to update workflow: %v", err)
		return err
	}

	ui.PrintSuccess("Added %d test(s) to workflow '%s': %s", len(added), workflowNameOrID, strings.Join(added, ", "))
	if len(skipped) > 0 {
		ui.PrintInfo("Skipped (already present): %s", strings.Join(skipped, ", "))
	}

	return nil
}

// runWorkflowRemoveTests removes tests from an existing workflow.
func runWorkflowRemoveTests(cmd *cobra.Command, args []string) error {
	workflowNameOrID := args[0]
	testNamesOrIDs := args[1:]

	workflowID, cfg, client, err := workflowTestsSetupClient(cmd, workflowNameOrID)
	if err != nil {
		return err
	}

	// Get current workflow to fetch existing tests
	ui.StartSpinner("Fetching workflow...")
	workflow, err := client.GetWorkflow(cmd.Context(), workflowID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to get workflow: %v", err)
		return err
	}

	// Resolve all test names to IDs and build removal set
	removeSet := make(map[string]bool)
	ui.StartSpinner("Resolving tests...")
	for _, nameOrID := range testNamesOrIDs {
		testID, _, resolveErr := resolveTestID(cmd.Context(), nameOrID, cfg, client)
		if resolveErr != nil {
			ui.StopSpinner()
			ui.PrintError("Failed to resolve test '%s': %v", nameOrID, resolveErr)
			return resolveErr
		}
		removeSet[testID] = true
	}
	ui.StopSpinner()

	// Build new list excluding removed tests
	var newTestIDs []string
	removedSet := make(map[string]bool)
	for _, id := range workflow.Tests {
		if removeSet[id] {
			removedSet[id] = true
			continue
		}
		newTestIDs = append(newTestIDs, id)
	}

	// Track which names were actually removed vs not found
	var removed, notFound []string
	for _, nameOrID := range testNamesOrIDs {
		testID, _, _ := resolveTestID(cmd.Context(), nameOrID, cfg, client)
		if removedSet[testID] {
			removed = append(removed, nameOrID)
		} else {
			notFound = append(notFound, nameOrID)
		}
	}

	if len(removed) == 0 {
		ui.PrintInfo("None of the specified tests were in workflow '%s'", workflowNameOrID)
		return nil
	}

	if newTestIDs == nil {
		newTestIDs = []string{}
	}

	ui.StartSpinner("Updating workflow...")
	err = client.UpdateWorkflowTests(cmd.Context(), workflowID, newTestIDs)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to update workflow: %v", err)
		return err
	}

	ui.PrintSuccess("Removed %d test(s) from workflow '%s': %s", len(removed), workflowNameOrID, strings.Join(removed, ", "))
	if len(notFound) > 0 {
		ui.PrintInfo("Not found in workflow (skipped): %s", strings.Join(notFound, ", "))
	}

	return nil
}
