// Package main provides the `revyl test launch-var` subcommand for
// managing which org-level launch variables are attached to a test.
//
// Org launch variables are created and stored once at the organization
// level (see `revyl global launch-var`); tests reference them by
// attachment. This command edits those attachments — it does not
// create or modify the underlying value.
package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

var testLaunchVarCmd = &cobra.Command{
	Use:     "launch-var",
	Aliases: []string{"launch-vars", "launch-variable"},
	Short:   "Attach/detach org launch variables to a test",
	Long: `Manage which org-level launch variables are attached to a test.

Launch variables are defined once at the org level (see ` + "`revyl global launch-var`" + `).
This command edits the per-test attachment, not the underlying value.

Variables can be referenced by key (e.g. ` + "`API_URL`" + `) or by UUID. Keys are
resolved against the current org's launch variables.

EXAMPLES:
  revyl test launch-var list login-flow
  revyl test launch-var attach login-flow API_URL DEBUG_MODE
  revyl test launch-var detach login-flow DEBUG_MODE
  revyl test launch-var set login-flow API_URL FEATURE_FLAG_X    # replace all`,
}

var testLaunchVarListCmd = &cobra.Command{
	Use:   "list <test-name|id>",
	Short: "List launch variables attached to a test",
	Args:  cobra.ExactArgs(1),
	RunE:  runTestLaunchVarList,
}

var testLaunchVarAttachCmd = &cobra.Command{
	Use:   "attach <test-name|id> <key|id> [<key|id>...]",
	Short: "Attach one or more launch variables to a test",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runTestLaunchVarAttach,
}

var testLaunchVarDetachCmd = &cobra.Command{
	Use:   "detach <test-name|id> <key|id>",
	Short: "Detach a launch variable from a test",
	Args:  cobra.ExactArgs(2),
	RunE:  runTestLaunchVarDetach,
}

var testLaunchVarSetCmd = &cobra.Command{
	Use:   "set <test-name|id> [<key|id>...]",
	Short: "Replace the test's attachments with the given list (empty list clears)",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runTestLaunchVarSet,
}

func init() {
	testLaunchVarCmd.AddCommand(testLaunchVarListCmd)
	testLaunchVarCmd.AddCommand(testLaunchVarAttachCmd)
	testLaunchVarCmd.AddCommand(testLaunchVarDetachCmd)
	testLaunchVarCmd.AddCommand(testLaunchVarSetCmd)
}

func runTestLaunchVarList(cmd *cobra.Command, args []string) error {
	testID, client, err := resolveTestClient(cmd, args[0])
	if err != nil {
		return err
	}
	resp, err := client.ListTestLaunchEnvVarAttachments(cmd.Context(), testID)
	if err != nil {
		return fmt.Errorf("list attachments: %w", err)
	}
	if len(resp.Result) == 0 {
		ui.PrintInfo("No launch variables attached to test %s", testID)
		return nil
	}
	for _, v := range resp.Result {
		fmt.Printf("  %-32s  %s\n", v.Key, v.ID)
	}
	return nil
}

func runTestLaunchVarAttach(cmd *cobra.Command, args []string) error {
	testID, client, err := resolveTestClient(cmd, args[0])
	if err != nil {
		return err
	}
	current, err := currentAttachmentIDs(cmd.Context(), client, testID)
	if err != nil {
		return err
	}
	addIDs, err := resolveLaunchVarRefs(cmd.Context(), client, args[1:])
	if err != nil {
		return err
	}
	merged := mergeUnique(current, addIDs)
	if _, err := client.ReplaceTestLaunchEnvVarAttachments(cmd.Context(), testID, merged); err != nil {
		return fmt.Errorf("update attachments: %w", err)
	}
	ui.PrintSuccess("Attached %d variable(s); test now has %d attachment(s)", len(addIDs), len(merged))
	return nil
}

func runTestLaunchVarDetach(cmd *cobra.Command, args []string) error {
	testID, client, err := resolveTestClient(cmd, args[0])
	if err != nil {
		return err
	}
	removeIDs, err := resolveLaunchVarRefs(cmd.Context(), client, args[1:])
	if err != nil {
		return err
	}
	current, err := currentAttachmentIDs(cmd.Context(), client, testID)
	if err != nil {
		return err
	}
	remaining := filterOut(current, removeIDs)
	if _, err := client.ReplaceTestLaunchEnvVarAttachments(cmd.Context(), testID, remaining); err != nil {
		return fmt.Errorf("update attachments: %w", err)
	}
	ui.PrintSuccess("Detached %s; test now has %d attachment(s)", args[1], len(remaining))
	return nil
}

func runTestLaunchVarSet(cmd *cobra.Command, args []string) error {
	testID, client, err := resolveTestClient(cmd, args[0])
	if err != nil {
		return err
	}
	ids, err := resolveLaunchVarRefs(cmd.Context(), client, args[1:])
	if err != nil {
		return err
	}
	// Dedupe while preserving order so attachments listed twice don't
	// show up twice in the replace payload.
	ids = mergeUnique(nil, ids)
	if _, err := client.ReplaceTestLaunchEnvVarAttachments(cmd.Context(), testID, ids); err != nil {
		return fmt.Errorf("update attachments: %w", err)
	}
	ui.PrintSuccess("Test now has %d attachment(s)", len(ids))
	return nil
}

func currentAttachmentIDs(ctx context.Context, client *api.Client, testID string) ([]string, error) {
	resp, err := client.ListTestLaunchEnvVarAttachments(ctx, testID)
	if err != nil {
		return nil, fmt.Errorf("list attachments: %w", err)
	}
	ids := make([]string, 0, len(resp.Result))
	for _, v := range resp.Result {
		ids = append(ids, v.ID)
	}
	return ids, nil
}

// resolveLaunchVarRefs accepts a mix of UUIDs and keys and returns UUIDs.
// Keys are looked up via the org launch-var list. Unknown keys/IDs error.
func resolveLaunchVarRefs(ctx context.Context, client *api.Client, refs []string) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	orgVars, err := client.ListOrgLaunchVariables(ctx)
	if err != nil {
		return nil, fmt.Errorf("list org launch variables: %w", err)
	}
	byKey := make(map[string]string, len(orgVars.Result))
	byID := make(map[string]struct{}, len(orgVars.Result))
	for _, v := range orgVars.Result {
		byKey[v.Key] = v.ID
		byID[v.ID] = struct{}{}
	}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if _, ok := byID[ref]; ok {
			out = append(out, ref)
			continue
		}
		if id, ok := byKey[ref]; ok {
			out = append(out, id)
			continue
		}
		return nil, fmt.Errorf("launch variable %q not found in this org (run `revyl global launch-var list` to see available)", ref)
	}
	return out, nil
}

func mergeUnique(base, add []string) []string {
	seen := make(map[string]struct{}, len(base)+len(add))
	out := make([]string, 0, len(base)+len(add))
	for _, id := range base {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range add {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func filterOut(base, remove []string) []string {
	drop := make(map[string]struct{}, len(remove))
	for _, id := range remove {
		drop[id] = struct{}{}
	}
	out := make([]string, 0, len(base))
	for _, id := range base {
		if _, ok := drop[id]; ok {
			continue
		}
		out = append(out, id)
	}
	return out
}
