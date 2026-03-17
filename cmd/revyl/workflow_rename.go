// Package main provides the workflow rename command.
package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

var (
	workflowRenameNonInteractive bool
	workflowRenameYes            bool
)

// runRenameWorkflow renames a workflow remotely.
func runRenameWorkflow(cmd *cobra.Command, args []string) error {
	oldNameOrID, newName := parseRenameArgs(args)
	promptMode := renamePromptsEnabled(workflowRenameNonInteractive)
	if (oldNameOrID == "" || newName == "") && !promptMode {
		return fmt.Errorf("missing required arguments: <old-name|id> <new-name> (or run in a TTY for guided prompts)")
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	_, cfg, client, err := loadConfigAndClient(devMode)
	if err != nil {
		return err
	}

	if oldNameOrID == "" {
		selectedID, selectedLabel, selectErr := selectWorkflowRenameTarget(cmd.Context(), client)
		if selectErr != nil {
			return selectErr
		}
		oldNameOrID = selectedID
		ui.PrintInfo("Selected workflow: %s", selectedLabel)
	}

	workflowID, _, err := resolveWorkflowID(cmd.Context(), oldNameOrID, cfg, client)
	if err != nil {
		ui.PrintError("%v", err)
		return err
	}

	ui.StartSpinner("Loading workflow...")
	workflow, err := client.GetWorkflow(cmd.Context(), workflowID)
	ui.StopSpinner()
	if err != nil {
		ui.PrintError("Failed to load workflow: %v", err)
		return err
	}

	if newName == "" {
		suggested := sanitizeNameForAlias(workflow.Name)
		newName, err = promptForRenameName("workflow", suggested)
		if err != nil {
			return err
		}
	}

	if err := validateResourceName(newName, "workflow"); err != nil {
		ui.PrintError("%v", err)
		return err
	}

	ui.StartSpinner("Checking name availability...")
	workflowList, listErr := client.ListWorkflows(cmd.Context())
	ui.StopSpinner()
	if listErr == nil {
		for _, w := range workflowList.Workflows {
			if w.ID != workflowID && w.Name == newName {
				err := fmt.Errorf("a different workflow already uses name %q (id: %s)", newName, w.ID)
				ui.PrintError("%v", err)
				return err
			}
		}
	}

	if promptMode {
		ui.Println()
		ui.PrintDim("  Remote: %s -> %s (id: %s)", workflow.Name, newName, workflowID)
		if !workflowRenameYes {
			confirmed, cErr := ui.PromptConfirm("Apply rename?", true)
			if cErr != nil {
				return cErr
			}
			if !confirmed {
				ui.PrintInfo("Cancelled")
				return nil
			}
		}
	}

	if workflow.Name != newName {
		ui.StartSpinner("Renaming workflow on Revyl...")
		err = client.UpdateWorkflowName(cmd.Context(), workflowID, newName)
		ui.StopSpinner()
		if err != nil {
			ui.PrintError("Failed to rename workflow on remote: %v", err)
			return err
		}
		ui.PrintSuccess("Remote renamed: %s -> %s", workflow.Name, newName)
	} else {
		ui.PrintInfo("Remote workflow is already named '%s'", newName)
	}

	ui.Println()
	ui.PrintSuccess("Workflow renamed to \"%s\" (id: %s)", newName, workflowID)
	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Run workflow:", Command: fmt.Sprintf("revyl workflow run \"%s\"", newName)},
	})
	return nil
}

func selectWorkflowRenameTarget(ctx context.Context, client *api.Client) (string, string, error) {
	ui.StartSpinner("Fetching workflows...")
	resp, err := client.ListWorkflows(ctx)
	ui.StopSpinner()
	if err != nil {
		return "", "", fmt.Errorf("failed to list workflows: %w", err)
	}
	if len(resp.Workflows) == 0 {
		return "", "", fmt.Errorf("no workflows found in organization")
	}

	type item struct {
		label string
		desc  string
		id    string
	}
	items := make([]item, 0, len(resp.Workflows))
	for _, w := range resp.Workflows {
		items = append(items, item{
			label: w.Name,
			desc:  fmt.Sprintf("Remote: %s | ID: %s", w.Name, w.ID),
			id:    w.ID,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		li := strings.ToLower(items[i].label)
		lj := strings.ToLower(items[j].label)
		if li == lj {
			return items[i].id < items[j].id
		}
		return li < lj
	})

	options := make([]ui.SelectOption, 0, len(items))
	for _, it := range items {
		options = append(options, ui.SelectOption{
			Label:       it.label,
			Value:       it.id,
			Description: it.desc,
		})
	}

	_, selectedID, err := ui.Select("Select a workflow to rename:", options, 0)
	if err != nil {
		return "", "", fmt.Errorf("selection cancelled")
	}

	for _, it := range items {
		if it.id == selectedID {
			return it.id, it.label, nil
		}
	}
	return selectedID, selectedID, nil
}
