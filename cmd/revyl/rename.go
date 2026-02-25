// Package main provides test/workflow rename helpers and test rename command logic.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

var (
	renameNonInteractive bool
	renameYes            bool
)

// runRenameTest renames a test remotely and reconciles local alias/file tracking.
func runRenameTest(cmd *cobra.Command, args []string) error {
	oldNameOrID, newName := parseRenameArgs(args)
	promptMode := renamePromptsEnabled(renameNonInteractive)
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
	if cfg.Tests == nil {
		cfg.Tests = make(map[string]string)
	}

	if oldNameOrID == "" {
		selectedID, selectedLabel, selectErr := selectTestRenameTarget(cmd.Context(), cfg, client)
		if selectErr != nil {
			return selectErr
		}
		oldNameOrID = selectedID
		ui.PrintInfo("Selected test: %s", selectedLabel)
	}

	testID, _, err := resolveTestID(cmd.Context(), oldNameOrID, cfg, client)
	if err != nil {
		ui.PrintError("%v", err)
		return err
	}

	ui.StartSpinner("Loading test...")
	remoteTest, err := client.GetTest(cmd.Context(), testID)
	ui.StopSpinner()
	if err != nil {
		ui.PrintError("Failed to load test: %v", err)
		return err
	}

	if newName == "" {
		suggested := sanitizeNameForAlias(remoteTest.Name)
		newName, err = promptForRenameName("test", suggested)
		if err != nil {
			return err
		}
	}

	if err := validateResourceName(newName, "test"); err != nil {
		ui.PrintError("%v", err)
		return err
	}

	// Pre-check remote name conflicts for better UX before sending update.
	ui.StartSpinner("Checking name availability...")
	testsResp, listErr := client.ListOrgTests(cmd.Context(), 200, 0)
	ui.StopSpinner()
	if listErr == nil {
		for _, t := range testsResp.Tests {
			if t.ID != testID && t.Name == newName {
				err := fmt.Errorf("a different test already uses name %q (id: %s)", newName, t.ID)
				ui.PrintError("%v", err)
				return err
			}
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}
	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	testsDir := filepath.Join(cwd, ".revyl", "tests")
	localTests, _ := config.LoadLocalTests(testsDir)

	aliasToRename, aliasAmbiguous := chooseAliasForTestRename(cfg.Tests, oldNameOrID, remoteTest.Name, testID)
	aliasMatches := aliasesForRemoteID(cfg.Tests, testID)

	localAlias, localAmbiguous := chooseLocalFileForTestRename(localTests, aliasToRename, oldNameOrID, remoteTest.Name, testID)
	localMatches := localAliasesForRemoteID(localTests, testID)

	applyLocalAliasRename := aliasToRename != "" && aliasToRename != newName
	applyLocalFileChanges := localAlias != ""

	// Ambiguous alias mappings: let user choose a safe resolution path.
	if aliasAmbiguous {
		if !promptMode {
			return fmt.Errorf("multiple local aliases map to this test (%s). Run interactively to choose resolution", strings.Join(aliasMatches, ", "))
		}
		choice, cErr := promptChoiceWithDefault(
			"Multiple local aliases map to this test. Choose action:",
			[]ui.SelectOption{
				{Label: "Rename remote only", Value: "remote-only", Description: "Keep local aliases/files unchanged."},
				{Label: "Choose alias to rename", Value: "choose-alias", Description: "Pick one local alias to rename."},
				{Label: "Abort", Value: "abort", Description: "Cancel this operation."},
			},
			0,
			renameYes,
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
			applyLocalFileChanges = false
		case "choose-alias":
			selectedAlias, sErr := promptSelectAlias("Choose alias to rename:", aliasMatches)
			if sErr != nil {
				return sErr
			}
			aliasToRename = selectedAlias
			applyLocalAliasRename = aliasToRename != "" && aliasToRename != newName
			localAlias = aliasToRename
			applyLocalFileChanges = localAlias != ""
		}
	}

	if applyLocalAliasRename {
		if existingID, exists := cfg.Tests[newName]; exists && existingID != testID {
			if !promptMode {
				return fmt.Errorf("local alias %q already points to a different test (%s)", newName, existingID)
			}
			choice, cErr := promptChoiceWithDefault(
				fmt.Sprintf("Local alias %q already maps to another test. Choose action:", newName),
				[]ui.SelectOption{
					{Label: "Rename remote only", Value: "remote-only", Description: "Leave local aliases/files unchanged."},
					{Label: "Abort", Value: "abort", Description: "Cancel this operation."},
				},
				0,
				renameYes,
			)
			if cErr != nil {
				return cErr
			}
			if choice == "abort" {
				ui.PrintInfo("Cancelled")
				return nil
			}
			applyLocalAliasRename = false
			applyLocalFileChanges = false
		}
	}

	if localAmbiguous && applyLocalFileChanges {
		if !promptMode {
			return fmt.Errorf("multiple local files map to this test (%s). Run interactively to choose resolution", strings.Join(localMatches, ", "))
		}
		choice, cErr := promptChoiceWithDefault(
			"Multiple local test files map to this test. Choose action:",
			[]ui.SelectOption{
				{Label: "Rename remote + alias only", Value: "alias-only", Description: "Skip local file changes."},
				{Label: "Choose local file", Value: "choose-file", Description: "Pick one local file to update."},
				{Label: "Rename remote only", Value: "remote-only", Description: "Skip all local changes."},
				{Label: "Abort", Value: "abort", Description: "Cancel this operation."},
			},
			0,
			renameYes,
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
			applyLocalFileChanges = false
		case "alias-only":
			applyLocalFileChanges = false
		case "choose-file":
			selectedFileAlias, sErr := promptSelectAlias("Choose local file to update:", localMatches)
			if sErr != nil {
				return sErr
			}
			localAlias = selectedFileAlias
			applyLocalFileChanges = localAlias != ""
		}
	}

	destFileAlias := localAlias
	if applyLocalFileChanges {
		// If alias is being renamed (or no alias exists), keep local file aligned to the new resource name.
		if applyLocalAliasRename || aliasToRename == "" {
			destFileAlias = newName
		}
	}

	if applyLocalFileChanges && destFileAlias != "" {
		if existing, exists := localTests[destFileAlias]; exists && destFileAlias != localAlias {
			if existing.Meta.RemoteID == "" || existing.Meta.RemoteID != testID {
				if !promptMode {
					return fmt.Errorf("local file already exists for %q at .revyl/tests/%s.yaml", destFileAlias, destFileAlias)
				}
				choice, cErr := promptChoiceWithDefault(
					fmt.Sprintf("Local file .revyl/tests/%s.yaml belongs to another test. Choose action:", destFileAlias),
					[]ui.SelectOption{
						{Label: "Rename remote + alias only", Value: "alias-only", Description: "Skip local file changes."},
						{Label: "Rename remote only", Value: "remote-only", Description: "Skip all local changes."},
						{Label: "Abort", Value: "abort", Description: "Cancel this operation."},
					},
					0,
					renameYes,
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
					applyLocalFileChanges = false
				case "alias-only":
					applyLocalFileChanges = false
				}
			}
		}
	}

	if promptMode {
		printRenamePreview("test", remoteTest.Name, newName, testID, applyLocalAliasRename, applyLocalFileChanges)
		if !renameYes {
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

	if remoteTest.Name != newName {
		ui.StartSpinner("Renaming test on Revyl...")
		_, err = client.UpdateTest(cmd.Context(), &api.UpdateTestRequest{
			TestID:          testID,
			Name:            newName,
			ExpectedVersion: remoteTest.Version,
		})
		ui.StopSpinner()
		if err != nil {
			ui.PrintError("Failed to rename test on remote: %v", err)
			return err
		}
		ui.PrintSuccess("Remote renamed: %s -> %s", remoteTest.Name, newName)
	} else {
		ui.PrintInfo("Remote test is already named '%s'", newName)
	}

	if applyLocalAliasRename {
		cfg.Tests[newName] = testID
		delete(cfg.Tests, aliasToRename)

		if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
			ui.PrintWarning("Renamed remotely, but failed to prepare config directory: %v", err)
		} else if err := config.WriteProjectConfig(configPath, cfg); err != nil {
			ui.PrintWarning("Renamed remotely, but failed to update .revyl/config.yaml: %v", err)
		} else {
			ui.PrintSuccess("Updated local alias: %s -> %s", aliasToRename, newName)
		}
	}

	if applyLocalFileChanges && localAlias != "" {
		local := localTests[localAlias]
		if local != nil {
			local.Test.Metadata.Name = newName

			sourcePath := filepath.Join(testsDir, localAlias+".yaml")
			destPath := filepath.Join(testsDir, destFileAlias+".yaml")
			if err := os.MkdirAll(testsDir, 0755); err != nil {
				ui.PrintWarning("Renamed remotely, but failed to prepare .revyl/tests directory: %v", err)
			} else if err := config.SaveLocalTest(destPath, local); err != nil {
				ui.PrintWarning("Renamed remotely, but failed to save local test file: %v", err)
			} else if sourcePath != destPath {
				if err := os.Remove(sourcePath); err != nil && !os.IsNotExist(err) {
					ui.PrintWarning("Renamed remotely, but failed to remove old local file %s: %v", sourcePath, err)
				} else {
					ui.PrintSuccess("Renamed local file: %s.yaml -> %s.yaml", localAlias, destFileAlias)
				}
			} else {
				ui.PrintSuccess("Updated local test metadata name")
			}
		}
	}

	if aliasAmbiguous && !applyLocalAliasRename {
		ui.PrintWarning("Multiple local aliases matched this test; local alias unchanged")
	}
	if localAmbiguous && !applyLocalFileChanges {
		ui.PrintWarning("Multiple local files matched this test; local file unchanged")
	}
	if !applyLocalAliasRename && !applyLocalFileChanges {
		ui.PrintDim("Local mappings/files left unchanged")
	}

	ui.Println()
	ui.PrintSuccess("Test renamed to \"%s\" (id: %s)", newName, testID)
	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Run renamed test:", Command: fmt.Sprintf("revyl test run %s", newName)},
		{Label: "List remote tests:", Command: "revyl test remote"},
	})

	return nil
}

func parseRenameArgs(args []string) (string, string) {
	if len(args) == 0 {
		return "", ""
	}
	if len(args) == 1 {
		return args[0], ""
	}
	return args[0], args[1]
}

func renamePromptsEnabled(nonInteractive bool) bool {
	if nonInteractive {
		return false
	}
	return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
}

func promptForRenameName(kind, suggested string) (string, error) {
	for {
		message := fmt.Sprintf("Enter new %s name", kind)
		if suggested != "" {
			message = fmt.Sprintf("%s [%s]:", message, suggested)
		} else {
			message += ":"
		}

		input, err := ui.Prompt(message)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(input) == "" {
			input = suggested
		}
		input = strings.TrimSpace(input)
		if err := validateResourceName(input, kind); err != nil {
			ui.PrintWarning("%v", err)
			continue
		}
		return input, nil
	}
}

func promptChoiceWithDefault(message string, options []ui.SelectOption, defaultIndex int, autoYes bool) (string, error) {
	if len(options) == 0 {
		return "", fmt.Errorf("no options provided")
	}
	if autoYes {
		idx := defaultIndex
		if idx < 0 || idx >= len(options) {
			idx = 0
		}
		return options[idx].Value, nil
	}
	_, value, err := ui.Select(message, options, defaultIndex)
	if err != nil {
		return "", fmt.Errorf("selection cancelled")
	}
	return value, nil
}

func promptSelectAlias(message string, aliases []string) (string, error) {
	if len(aliases) == 0 {
		return "", fmt.Errorf("no aliases to choose from")
	}
	options := make([]ui.SelectOption, 0, len(aliases))
	for _, alias := range aliases {
		options = append(options, ui.SelectOption{Label: alias, Value: alias})
	}
	_, value, err := ui.Select(message, options, 0)
	if err != nil {
		return "", fmt.Errorf("selection cancelled")
	}
	return value, nil
}

func printRenamePreview(kind, oldName, newName, id string, applyAliasRename, applyFileChange bool) {
	ui.Println()
	ui.PrintInfo("Rename preview (%s):", kind)
	ui.PrintDim("  Remote: %s -> %s (id: %s)", oldName, newName, id)
	if applyAliasRename {
		ui.PrintDim("  Local alias: will be updated")
	} else {
		ui.PrintDim("  Local alias: unchanged")
	}
	if kind == "test" {
		if applyFileChange {
			ui.PrintDim("  Local test file: will be updated")
		} else {
			ui.PrintDim("  Local test file: unchanged")
		}
	}
	ui.Println()
}

func selectTestRenameTarget(ctx context.Context, cfg *config.ProjectConfig, client *api.Client) (string, string, error) {
	ui.StartSpinner("Fetching tests...")
	resp, err := client.ListOrgTests(ctx, 200, 0)
	ui.StopSpinner()
	if err != nil {
		return "", "", fmt.Errorf("failed to list tests: %w", err)
	}
	if len(resp.Tests) == 0 {
		return "", "", fmt.Errorf("no tests found in organization")
	}

	aliasesByID := make(map[string][]string)
	if cfg != nil && cfg.Tests != nil {
		for alias, id := range cfg.Tests {
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
	items := make([]item, 0, len(resp.Tests))
	for _, t := range resp.Tests {
		aliases := aliasesByID[t.ID]
		label := t.Name
		if len(aliases) > 0 {
			label = aliases[0]
		}
		desc := fmt.Sprintf("Remote: %s | Platform: %s | ID: %s", t.Name, t.Platform, t.ID)
		if len(aliases) > 0 {
			desc = fmt.Sprintf("Aliases: %s | %s", strings.Join(aliases, ", "), desc)
		}
		items = append(items, item{label: label, desc: desc, id: t.ID})
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

	_, selectedID, err := ui.Select("Select a test to rename:", options, 0)
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

func aliasesForRemoteID(testAliases map[string]string, testID string) []string {
	var aliases []string
	for alias, id := range testAliases {
		if id == testID {
			aliases = append(aliases, alias)
		}
	}
	sort.Strings(aliases)
	return aliases
}

func localAliasesForRemoteID(localTests map[string]*config.LocalTest, testID string) []string {
	var aliases []string
	for alias, lt := range localTests {
		if lt != nil && lt.Meta.RemoteID == testID {
			aliases = append(aliases, alias)
		}
	}
	sort.Strings(aliases)
	return aliases
}

func chooseAliasForTestRename(testAliases map[string]string, oldNameOrID, remoteName, testID string) (string, bool) {
	if len(testAliases) == 0 {
		return "", false
	}

	if id, ok := testAliases[oldNameOrID]; ok && id == testID {
		return oldNameOrID, false
	}
	if id, ok := testAliases[remoteName]; ok && id == testID {
		return remoteName, false
	}

	aliases := aliasesForRemoteID(testAliases, testID)
	if len(aliases) == 1 {
		return aliases[0], false
	}
	if len(aliases) > 1 {
		return "", true
	}
	return "", false
}

func chooseLocalFileForTestRename(localTests map[string]*config.LocalTest, aliasToRename, oldNameOrID, remoteName, testID string) (string, bool) {
	if len(localTests) == 0 {
		return "", false
	}

	if aliasToRename != "" {
		if lt, ok := localTests[aliasToRename]; ok {
			if lt.Meta.RemoteID == "" || lt.Meta.RemoteID == testID {
				return aliasToRename, false
			}
		}
	}

	for _, candidate := range []string{oldNameOrID, remoteName} {
		if candidate == "" {
			continue
		}
		lt, ok := localTests[candidate]
		if !ok {
			continue
		}
		if lt.Meta.RemoteID == testID {
			return candidate, false
		}
	}

	matches := localAliasesForRemoteID(localTests, testID)
	if len(matches) == 0 {
		return "", false
	}
	return matches[0], len(matches) > 1
}
