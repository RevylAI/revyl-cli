// Package main provides workflow quarantine commands for managing test failure policies.
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

// workflowQuarantineCmd is the parent command for quarantine operations.
var workflowQuarantineCmd = &cobra.Command{
	Use:   "quarantine",
	Short: "Manage quarantined tests in a workflow",
	Long: `Manage test quarantine within a workflow.

Quarantined tests still run but their failures are ignored when determining
overall workflow pass/fail status. Use this to unblock CI while flaky or
known-broken tests are investigated.

COMMANDS:
  add    - Quarantine tests (set failure_policy to ignore_failure)
  remove - Unquarantine tests (set failure_policy to fail_workflow)
  list   - Show quarantined tests in a workflow

EXAMPLES:
  revyl workflow quarantine list smoke-tests
  revyl workflow quarantine add smoke-tests login-flow checkout
  revyl workflow quarantine remove smoke-tests login-flow`,
}

// workflowQuarantineAddCmd marks tests as quarantined in a workflow.
var workflowQuarantineAddCmd = &cobra.Command{
	Use:   "add <workflow> <test...>",
	Short: "Quarantine tests in a workflow",
	Long: `Mark one or more tests as quarantined in a workflow.

Quarantined tests still execute but their failures do not fail the workflow.

EXAMPLES:
  revyl workflow quarantine add smoke-tests login-flow
  revyl workflow quarantine add smoke-tests login-flow checkout payment`,
	Args: cobra.MinimumNArgs(2),
	RunE: runQuarantineAdd,
}

// workflowQuarantineRemoveCmd removes quarantine from tests in a workflow.
var workflowQuarantineRemoveCmd = &cobra.Command{
	Use:   "remove <workflow> <test...>",
	Short: "Remove quarantine from tests in a workflow",
	Long: `Unquarantine one or more tests in a workflow so their failures count again.

EXAMPLES:
  revyl workflow quarantine remove smoke-tests login-flow
  revyl workflow quarantine remove smoke-tests login-flow checkout`,
	Args: cobra.MinimumNArgs(2),
	RunE: runQuarantineRemove,
}

// workflowQuarantineListCmd shows quarantined tests in a workflow.
var workflowQuarantineListCmd = &cobra.Command{
	Use:   "list <workflow>",
	Short: "List quarantined tests in a workflow",
	Long: `Show all tests in a workflow and their quarantine status.

EXAMPLES:
  revyl workflow quarantine list smoke-tests`,
	Args: cobra.ExactArgs(1),
	RunE: runQuarantineList,
}

func init() {
	workflowQuarantineCmd.AddCommand(workflowQuarantineAddCmd)
	workflowQuarantineCmd.AddCommand(workflowQuarantineRemoveCmd)
	workflowQuarantineCmd.AddCommand(workflowQuarantineListCmd)
}

// quarantineSetupClient creates an API client, resolves the workflow ID, and
// fetches workflow tests with their failure policies.
//
// Parameters:
//   - cmd: The cobra command (used for flag access)
//   - workflowNameOrID: Workflow name (from config) or UUID
//
// Returns:
//   - workflowTests: The workflow tests with failure policies
//   - cfg: The loaded project config (may be nil)
//   - client: Configured API client
//   - error: Any error during setup
func quarantineSetupClient(cmd *cobra.Command, workflowNameOrID string) ([]api.WorkflowTestWithPolicy, *config.ProjectConfig, *api.Client, error) {
	apiKey, err := getAPIKey()
	if err != nil {
		return nil, nil, nil, err
	}

	cwd, _ := os.Getwd()
	cfg, _ := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	workflowID, _, err := resolveWorkflowID(cmd.Context(), workflowNameOrID, cfg, client)
	if err != nil {
		ui.PrintError("%v", err)
		return nil, nil, nil, fmt.Errorf("workflow not found")
	}

	ui.StartSpinner("Fetching workflow tests...")
	workflowTests, err := client.GetWorkflowTestsWithPolicy(cmd.Context(), workflowID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to fetch workflow tests: %v", err)
		return nil, nil, nil, err
	}

	return workflowTests, cfg, client, nil
}

// findWorkflowTestByName finds the workflow-test entry matching a given test name or ID.
//
// Parameters:
//   - testNameOrID: Test name (from config) or UUID
//   - workflowTests: The list of workflow tests to search
//   - cfg: Project config for name→ID resolution
//   - client: API client for name→ID resolution
//   - cmd: The cobra command for context
//
// Returns:
//   - *api.WorkflowTestWithPolicy: The matched workflow test, or nil if not found
func findWorkflowTestByName(testNameOrID string, workflowTests []api.WorkflowTestWithPolicy, cfg *config.ProjectConfig, client *api.Client, cmd *cobra.Command) *api.WorkflowTestWithPolicy {
	testID, _, err := resolveTestID(cmd.Context(), testNameOrID, cfg, client)
	if err != nil {
		return nil
	}

	for i := range workflowTests {
		if workflowTests[i].TestID == testID {
			return &workflowTests[i]
		}
	}
	return nil
}

// runQuarantineAdd marks tests as quarantined (failure_policy=ignore_failure).
func runQuarantineAdd(cmd *cobra.Command, args []string) error {
	workflowNameOrID := args[0]
	testNames := args[1:]

	workflowTests, cfg, client, err := quarantineSetupClient(cmd, workflowNameOrID)
	if err != nil {
		return err
	}

	var quarantined, skipped, notFound []string

	for _, name := range testNames {
		wt := findWorkflowTestByName(name, workflowTests, cfg, client, cmd)
		if wt == nil {
			notFound = append(notFound, name)
			continue
		}

		if wt.FailurePolicy == "ignore_failure" {
			skipped = append(skipped, name)
			continue
		}

		ui.StartSpinner(fmt.Sprintf("Quarantining '%s'...", name))
		err := client.UpdateWorkflowTestFailurePolicy(cmd.Context(), wt.WorkflowTestID, "ignore_failure")
		ui.StopSpinner()

		if err != nil {
			ui.PrintError("Failed to quarantine '%s': %v", name, err)
			return err
		}
		quarantined = append(quarantined, name)
	}

	if len(quarantined) > 0 {
		ui.PrintSuccess("Quarantined %d test(s) in '%s': %s", len(quarantined), workflowNameOrID, strings.Join(quarantined, ", "))
	}
	if len(skipped) > 0 {
		ui.PrintInfo("Already quarantined (skipped): %s", strings.Join(skipped, ", "))
	}
	if len(notFound) > 0 {
		ui.PrintWarning("Not found in workflow (skipped): %s", strings.Join(notFound, ", "))
	}

	return nil
}

// runQuarantineRemove removes quarantine from tests (failure_policy=fail_workflow).
func runQuarantineRemove(cmd *cobra.Command, args []string) error {
	workflowNameOrID := args[0]
	testNames := args[1:]

	workflowTests, cfg, client, err := quarantineSetupClient(cmd, workflowNameOrID)
	if err != nil {
		return err
	}

	var unquarantined, skipped, notFound []string

	for _, name := range testNames {
		wt := findWorkflowTestByName(name, workflowTests, cfg, client, cmd)
		if wt == nil {
			notFound = append(notFound, name)
			continue
		}

		if wt.FailurePolicy != "ignore_failure" {
			skipped = append(skipped, name)
			continue
		}

		ui.StartSpinner(fmt.Sprintf("Unquarantining '%s'...", name))
		err := client.UpdateWorkflowTestFailurePolicy(cmd.Context(), wt.WorkflowTestID, "fail_workflow")
		ui.StopSpinner()

		if err != nil {
			ui.PrintError("Failed to unquarantine '%s': %v", name, err)
			return err
		}
		unquarantined = append(unquarantined, name)
	}

	if len(unquarantined) > 0 {
		ui.PrintSuccess("Unquarantined %d test(s) in '%s': %s", len(unquarantined), workflowNameOrID, strings.Join(unquarantined, ", "))
	}
	if len(skipped) > 0 {
		ui.PrintInfo("Not quarantined (skipped): %s", strings.Join(skipped, ", "))
	}
	if len(notFound) > 0 {
		ui.PrintWarning("Not found in workflow (skipped): %s", strings.Join(notFound, ", "))
	}

	return nil
}

// runQuarantineList shows quarantined tests in a workflow.
func runQuarantineList(cmd *cobra.Command, args []string) error {
	workflowNameOrID := args[0]

	workflowTests, _, _, err := quarantineSetupClient(cmd, workflowNameOrID)
	if err != nil {
		return err
	}

	if len(workflowTests) == 0 {
		ui.PrintInfo("No tests found in workflow '%s'", workflowNameOrID)
		return nil
	}

	quarantineCount := 0
	for _, wt := range workflowTests {
		if wt.FailurePolicy == "ignore_failure" {
			quarantineCount++
		}
	}

	ui.Println()
	ui.PrintInfo("Tests in workflow '%s' (%d total, %d quarantined)", workflowNameOrID, len(workflowTests), quarantineCount)
	ui.Println()

	table := ui.NewTable("TEST", "PLATFORM", "STATUS")
	table.SetMinWidth(0, 25)
	table.SetMinWidth(1, 10)
	table.SetMinWidth(2, 15)

	for _, wt := range workflowTests {
		testName := wt.TestName
		if testName == "" {
			testName = wt.TestID
		}
		platform := wt.Platform
		if platform == "" {
			platform = "-"
		}
		status := "active"
		if wt.FailurePolicy == "ignore_failure" {
			status = "quarantined"
		}
		table.AddRow(testName, platform, status)
	}

	table.Render()

	if quarantineCount > 0 {
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Unquarantine a test:", Command: fmt.Sprintf("revyl workflow quarantine remove %s <test>", workflowNameOrID)},
		})
	} else {
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Quarantine a test:", Command: fmt.Sprintf("revyl workflow quarantine add %s <test>", workflowNameOrID)},
		})
	}

	return nil
}
