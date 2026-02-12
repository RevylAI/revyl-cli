// Package main provides handler implementations for the tag command.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// resolveTagNameOrID resolves a tag name or UUID to a tag ID and name.
// It first checks if the input looks like a UUID, then searches by name in the tag list.
func resolveTagNameOrID(cmd *cobra.Command, client *api.Client, nameOrID string) (tagID, tagName string, err error) {
	// List all tags (tags don't have a "get by ID" endpoint, so we always list)
	listResp, err := client.ListTags(cmd.Context())
	if err != nil {
		return "", "", fmt.Errorf("failed to list tags: %w", err)
	}

	// If it looks like a UUID, search by ID
	if looksLikeUUID(nameOrID) {
		for _, t := range listResp.Tags {
			if t.ID == nameOrID {
				return t.ID, t.Name, nil
			}
		}
	}

	// Search by name (case-insensitive)
	for _, t := range listResp.Tags {
		if strings.EqualFold(t.Name, nameOrID) {
			return t.ID, t.Name, nil
		}
	}

	return "", "", fmt.Errorf("tag \"%s\" not found", nameOrID)
}

// resolveTestForTag resolves a test name or ID for tag operations.
// Uses config aliases first, then falls back to API search.
func resolveTestForTag(cmd *cobra.Command, client *api.Client, nameOrID string) (testID, testName string, err error) {
	// Load project config for alias resolution
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	cfg, _ := config.LoadProjectConfig(configPath)

	return resolveTestNameOrID(cmd.Context(), client, cfg, nameOrID)
}

// parseTagNames splits a comma-separated tag names string into a slice.
func parseTagNames(input string) []string {
	parts := strings.Split(input, ",")
	var names []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			names = append(names, p)
		}
	}
	return names
}

// runTagList handles the tag list command.
func runTagList(cmd *cobra.Command, args []string) error {
	// Check JSON output flag
	jsonOutput := tagListJSON
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
		ui.StartSpinner("Fetching tags...")
	}

	resp, err := client.ListTags(cmd.Context())
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to list tags: %v", err)
		return err
	}

	// Apply search filter if specified
	tags := resp.Tags
	if tagListSearch != "" {
		query := strings.ToLower(tagListSearch)
		var filtered []api.CLITagResponse
		for _, t := range tags {
			if strings.Contains(strings.ToLower(t.Name), query) {
				filtered = append(filtered, t)
			}
		}
		tags = filtered
	}

	if jsonOutput {
		output := make([]map[string]interface{}, 0, len(tags))
		for _, t := range tags {
			item := map[string]interface{}{
				"id":         t.ID,
				"name":       t.Name,
				"color":      t.Color,
				"test_count": t.TestCount,
			}
			if t.Description != "" {
				item["description"] = t.Description
			}
			output = append(output, item)
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(tags) == 0 {
		if tagListSearch != "" {
			ui.PrintInfo("No tags found matching \"%s\"", tagListSearch)
		} else {
			ui.PrintInfo("No tags found")
		}
		ui.Println()
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Create a tag:", Command: "revyl tag create <name>"},
		})
		return nil
	}

	ui.Println()
	ui.PrintInfo("Tags (%d)", len(tags))
	ui.Println()

	table := ui.NewTable("NAME", "COLOR", "TESTS", "DESCRIPTION")
	table.SetMinWidth(0, 16) // NAME
	table.SetMinWidth(1, 9)  // COLOR
	table.SetMinWidth(2, 6)  // TESTS
	table.SetMinWidth(3, 20) // DESCRIPTION

	for _, t := range tags {
		desc := t.Description
		if desc == "" {
			desc = "-"
		}
		if len(desc) > 50 {
			desc = desc[:47] + "..."
		}
		color := t.Color
		if color == "" {
			color = "#6B7280"
		}
		table.AddRow(t.Name, color, fmt.Sprintf("%d", t.TestCount), desc)
	}

	table.Render()

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Create a tag:", Command: "revyl tag create <name>"},
		{Label: "Tag a test:", Command: "revyl tag set <test> <tag1,tag2>"},
	})

	return nil
}

// runTagCreate handles the tag create command.
func runTagCreate(cmd *cobra.Command, args []string) error {
	tagName := args[0]

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	ui.StartSpinner("Creating tag...")

	req := &api.CLICreateTagRequest{
		Name:  tagName,
		Color: tagCreateColor,
	}

	resp, err := client.CreateTag(cmd.Context(), req)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to create tag: %v", err)
		return err
	}

	ui.PrintSuccess("Tag created: %s", resp.Name)
	ui.PrintDim("  ID:    %s", resp.ID)
	if resp.Color != "" {
		ui.PrintDim("  Color: %s", resp.Color)
	}

	ui.Println()
	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Tag a test:", Command: fmt.Sprintf("revyl tag set <test> %s", tagName)},
		{Label: "List tags:", Command: "revyl tag list"},
	})

	return nil
}

// runTagUpdate handles the tag update command.
func runTagUpdate(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	ui.StartSpinner("Resolving tag...")

	tagID, _, err := resolveTagNameOrID(cmd, client, nameOrID)
	if err != nil {
		ui.StopSpinner()
		ui.PrintError("%v", err)
		return err
	}

	// Build update request
	req := &api.CLIUpdateTagRequest{}
	hasUpdate := false

	if tagUpdateName != "" {
		req.Name = &tagUpdateName
		hasUpdate = true
	}

	if tagUpdateColor != "" {
		req.Color = &tagUpdateColor
		hasUpdate = true
	}

	if cmd.Flags().Changed("description") {
		req.Description = &tagUpdateDescription
		hasUpdate = true
	}

	if !hasUpdate {
		ui.StopSpinner()
		ui.PrintError("No updates specified. Use --name, --color, or --description.")
		return fmt.Errorf("no updates specified")
	}

	ui.StartSpinner("Updating tag...")

	resp, err := client.UpdateTag(cmd.Context(), tagID, req)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to update tag: %v", err)
		return err
	}

	ui.PrintSuccess("Tag updated: %s", resp.Name)
	ui.PrintDim("  ID:    %s", resp.ID)
	if resp.Color != "" {
		ui.PrintDim("  Color: %s", resp.Color)
	}

	return nil
}

// runTagDelete handles the tag delete command.
func runTagDelete(cmd *cobra.Command, args []string) error {
	nameOrID := args[0]

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	ui.StartSpinner("Resolving tag...")

	tagID, tagName, err := resolveTagNameOrID(cmd, client, nameOrID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("%v", err)
		return err
	}

	// Confirm deletion
	if !tagDeleteForce {
		ui.Println()
		ui.PrintInfo("Delete tag \"%s\"?", tagName)
		ui.PrintDim("  ID: %s", tagID)
		ui.PrintDim("  This will remove the tag from all tests.")
		ui.Println()

		confirmed, err := ui.PromptConfirm("Are you sure?", false)
		if err != nil || !confirmed {
			ui.PrintInfo("Cancelled")
			return nil
		}
	}

	ui.StartSpinner("Deleting tag...")

	err = client.DeleteTag(cmd.Context(), tagID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to delete tag: %v", err)
		return err
	}

	ui.PrintSuccess("Tag deleted: %s", tagName)

	return nil
}

// runTagGet handles the tag get command (show tags for a test).
func runTagGet(cmd *cobra.Command, args []string) error {
	testNameOrID := args[0]

	// Check JSON output
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	if !jsonOutput {
		ui.StartSpinner("Fetching tags...")
	}

	testID, testName, err := resolveTestForTag(cmd, client, testNameOrID)
	if err != nil {
		if !jsonOutput {
			ui.StopSpinner()
		}
		ui.PrintError("%v", err)
		return err
	}

	tags, err := client.GetTestTags(cmd.Context(), testID)
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to get tags: %v", err)
		return err
	}

	if jsonOutput {
		output := make([]map[string]interface{}, 0, len(tags))
		for _, t := range tags {
			item := map[string]interface{}{
				"id":    t.ID,
				"name":  t.Name,
				"color": t.Color,
			}
			if t.Description != "" {
				item["description"] = t.Description
			}
			output = append(output, item)
		}
		data, _ := json.MarshalIndent(map[string]interface{}{
			"test_id":   testID,
			"test_name": testName,
			"tags":      output,
		}, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(tags) == 0 {
		ui.Println()
		ui.PrintInfo("No tags on \"%s\"", testName)
		ui.Println()
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Add tags:", Command: fmt.Sprintf("revyl tag set %s <tag1,tag2>", testNameOrID)},
		})
		return nil
	}

	ui.Println()
	ui.PrintInfo("Tags for \"%s\" (%d)", testName, len(tags))
	for _, t := range tags {
		ui.PrintDim("  %s", t.Name)
	}
	ui.Println()

	return nil
}

// runTagSet handles the tag set command (replace all tags on a test).
func runTagSet(cmd *cobra.Command, args []string) error {
	testNameOrID := args[0]
	tagNames := parseTagNames(args[1])

	if len(tagNames) == 0 {
		ui.PrintError("No tag names provided")
		return fmt.Errorf("no tag names provided")
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	ui.StartSpinner("Setting tags...")

	testID, testName, err := resolveTestForTag(cmd, client, testNameOrID)
	if err != nil {
		ui.StopSpinner()
		ui.PrintError("%v", err)
		return err
	}

	resp, err := client.SyncTestTags(cmd.Context(), testID, &api.CLISyncTagsRequest{
		TagNames: tagNames,
	})
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to set tags: %v", err)
		return err
	}

	ui.PrintSuccess("Tags set on \"%s\"", testName)
	for _, t := range resp.Tags {
		suffix := ""
		if t.Created {
			suffix = " (created)"
		}
		ui.PrintDim("  %s%s", t.Name, suffix)
	}

	return nil
}

// runTagAdd handles the tag add command (add tags to a test, keep existing).
func runTagAdd(cmd *cobra.Command, args []string) error {
	testNameOrID := args[0]
	tagNames := parseTagNames(args[1])

	if len(tagNames) == 0 {
		ui.PrintError("No tag names provided")
		return fmt.Errorf("no tag names provided")
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	ui.StartSpinner("Adding tags...")

	testID, testName, err := resolveTestForTag(cmd, client, testNameOrID)
	if err != nil {
		ui.StopSpinner()
		ui.PrintError("%v", err)
		return err
	}

	resp, err := client.BulkSyncTestTags(cmd.Context(), &api.CLIBulkSyncTagsRequest{
		TestIDs:   []string{testID},
		TagsToAdd: tagNames,
	})
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to add tags: %v", err)
		return err
	}

	if resp.ErrorCount > 0 {
		for _, r := range resp.Results {
			if !r.Success && r.Error != nil {
				ui.PrintError("Failed for test %s: %s", r.TestID, *r.Error)
			}
		}
		return fmt.Errorf("some operations failed")
	}

	ui.PrintSuccess("Tags added to \"%s\": %s", testName, strings.Join(tagNames, ", "))

	return nil
}

// runTagRemove handles the tag remove command (remove tags from a test).
func runTagRemove(cmd *cobra.Command, args []string) error {
	testNameOrID := args[0]
	tagNames := parseTagNames(args[1])

	if len(tagNames) == 0 {
		ui.PrintError("No tag names provided")
		return fmt.Errorf("no tag names provided")
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	ui.StartSpinner("Removing tags...")

	testID, testName, err := resolveTestForTag(cmd, client, testNameOrID)
	if err != nil {
		ui.StopSpinner()
		ui.PrintError("%v", err)
		return err
	}

	resp, err := client.BulkSyncTestTags(cmd.Context(), &api.CLIBulkSyncTagsRequest{
		TestIDs:      []string{testID},
		TagsToRemove: tagNames,
	})
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to remove tags: %v", err)
		return err
	}

	if resp.ErrorCount > 0 {
		for _, r := range resp.Results {
			if !r.Success && r.Error != nil {
				ui.PrintError("Failed for test %s: %s", r.TestID, *r.Error)
			}
		}
		return fmt.Errorf("some operations failed")
	}

	ui.PrintSuccess("Tags removed from \"%s\": %s", testName, strings.Join(tagNames, ", "))

	return nil
}
