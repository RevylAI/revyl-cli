// Package main provides the workflow rename command.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

var (
	workflowRenameNonInteractive bool
	workflowRenameYes            bool
)

// runRenameWorkflow renames a workflow remotely and reconciles local alias tracking.
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
	if cfg == nil {
		cfg = &config.ProjectConfig{}
	}
	if cfg.Workflows == nil {
		cfg.Workflows = make(map[string]string)
	}

	if oldNameOrID == "" {
		selectedID, selectedLabel, selectErr := selectWorkflowRenameTarget(cmd.Context(), cfg, client)
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

	aliasToRename, aliasAmbiguous := chooseAliasForTestRename(cfg.Workflows, oldNameOrID, workflow.Name, workflowID)
	aliasMatches := aliasesForRemoteID(cfg.Workflows, workflowID)
	applyLocalAliasRename := aliasToRename != "" && aliasToRename != newName

	if aliasAmbiguous {
		if !promptMode {
			return fmt.Errorf("multiple local aliases map to this workflow (%s). Run interactively to choose resolution", strings.Join(aliasMatches, ", "))
		}
		choice, cErr := promptChoiceWithDefault(
			"Multiple local aliases map to this workflow. Choose action:",
			[]ui.SelectOption{
				{Label: "Rename remote only", Value: "remote-only", Description: "Keep local aliases unchanged."},
				{Label: "Choose alias to rename", Value: "choose-alias", Description: "Pick one local alias to rename."},
				{Label: "Abort", Value: "abort", Description: "Cancel this operation."},
			},
			0,
			workflowRenameYes,
		)
		if cErr != nil {
			return cErr
		}
		switch choice {
		case "abort":
			ui.PrintInfo("Cancelled")
			return nil
		case "remote-only":
			applyLocalAliasRename = false
		case "choose-alias":
			selectedAlias, sErr := promptSelectAlias("Choose alias to rename:", aliasMatches)
			if sErr != nil {
				return sErr
			}
			aliasToRename = selectedAlias
			applyLocalAliasRename = aliasToRename != "" && aliasToRename != newName
		}
	}

	if applyLocalAliasRename {
		if existingID, exists := cfg.Workflows[newName]; exists && existingID != workflowID {
			if !promptMode {
				return fmt.Errorf("local alias %q already points to a different workflow (%s)", newName, existingID)
			}
			choice, cErr := promptChoiceWithDefault(
				fmt.Sprintf("Local alias %q already maps to another workflow. Choose action:", newName),
				[]ui.SelectOption{
					{Label: "Rename remote only", Value: "remote-only", Description: "Leave local aliases unchanged."},
					{Label: "Abort", Value: "abort", Description: "Cancel this operation."},
				},
				0,
				workflowRenameYes,
			)
			if cErr != nil {
				return cErr
			}
			if choice == "abort" {
				ui.PrintInfo("Cancelled")
				return nil
			}
			applyLocalAliasRename = false
		}
	}

	if promptMode {
		printRenamePreview("workflow", workflow.Name, newName, workflowID, applyLocalAliasRename, false)
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

	if applyLocalAliasRename {
		cfg.Workflows[newName] = workflowID
		delete(cfg.Workflows, aliasToRename)

		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			ui.PrintWarning("Renamed remotely, but failed to get current directory for config update: %v", cwdErr)
		} else {
			configPath := filepath.Join(cwd, ".revyl", "config.yaml")
			if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
				ui.PrintWarning("Renamed remotely, but failed to prepare config directory: %v", err)
			} else if err := config.WriteProjectConfig(configPath, cfg); err != nil {
				ui.PrintWarning("Renamed remotely, but failed to update .revyl/config.yaml: %v", err)
			} else {
				ui.PrintSuccess("Updated local alias: %s -> %s", aliasToRename, newName)
			}
		}
	}

	if aliasAmbiguous && !applyLocalAliasRename {
		ui.PrintWarning("Multiple local aliases matched this workflow; local alias unchanged")
	}
	if !applyLocalAliasRename {
		ui.PrintDim("Local aliases left unchanged")
	}

	ui.Println()
	ui.PrintSuccess("Workflow renamed to \"%s\" (id: %s)", newName, workflowID)
	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Run renamed workflow:", Command: fmt.Sprintf("revyl workflow run %s", newName)},
		{Label: "List workflows:", Command: "revyl workflow list"},
	})

	return nil
}

func selectWorkflowRenameTarget(ctx context.Context, cfg *config.ProjectConfig, client *api.Client) (string, string, error) {
	ui.StartSpinner("Fetching workflows...")
	resp, err := client.ListWorkflows(ctx)
	ui.StopSpinner()
	if err != nil {
		return "", "", fmt.Errorf("failed to list workflows: %w", err)
	}
	if len(resp.Workflows) == 0 {
		return "", "", fmt.Errorf("no workflows found in organization")
	}

	aliasesByID := make(map[string][]string)
	if cfg != nil && cfg.Workflows != nil {
		for alias, id := range cfg.Workflows {
			aliasesByID[id] = append(aliasesByID[id], alias)
		}
		for id := range aliasesByID {
			sort.Strings(aliasesByID[id])
		}
	}

	type item struct {
		label string
		desc  string
		id    string
	}
	items := make([]item, 0, len(resp.Workflows))
	for _, w := range resp.Workflows {
		aliases := aliasesByID[w.ID]
		label := w.Name
		if len(aliases) > 0 {
			label = aliases[0]
		}
		desc := fmt.Sprintf("Remote: %s | ID: %s", w.Name, w.ID)
		if len(aliases) > 0 {
			desc = fmt.Sprintf("Aliases: %s | %s", strings.Join(aliases, ", "), desc)
		}
		items = append(items, item{label: label, desc: desc, id: w.ID})
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
