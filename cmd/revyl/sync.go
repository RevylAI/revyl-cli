// Package main provides unified project synchronization commands.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	syncpkg "github.com/revyl/cli/internal/sync"
	"github.com/revyl/cli/internal/ui"
	"github.com/revyl/cli/internal/util"
)

// syncCmd reconciles local project config with upstream state.
var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync tests and app links with upstream state",
	Long: `Synchronize local project state against your Revyl organization.

By default, sync reconciles tests and app link mappings in .revyl/config.yaml.
Remote tests that exist in the org but not locally are imported after
confirmation (interactive) or skipped (non-interactive / --skip-import).

EXAMPLES:
  revyl sync                        # Sync tests + app links
  revyl sync --tests                # Sync tests only
  revyl sync --workflow "Smoke"     # Sync only tests in this workflow
  revyl sync --skip-import          # Only sync existing local tests, skip remote imports
  revyl sync --non-interactive      # No prompts; deterministic defaults
  revyl sync --prune                # Auto-prune stale mappings
  revyl sync --dry-run --json       # Preview actions as JSON`,
	Example: `  revyl sync
  revyl sync --non-interactive
  revyl sync --dry-run --json
  revyl sync --skip-import --prune`,
	RunE: runSync,
}

func init() {
	registerSyncFlags(syncCmd)
}

type syncOptions struct {
	Prompt         bool
	Prune          bool
	DryRun         bool
	SkipImport     bool
	JSONOutput     bool
	WorkflowFilter string
}

type syncItem struct {
	Name     string `json:"name"`
	ID       string `json:"id,omitempty"`
	Status   string `json:"status"`
	Action   string `json:"action,omitempty"`
	Message  string `json:"message,omitempty"`
	Prompted bool   `json:"prompted,omitempty"`
	Error    string `json:"error,omitempty"`
}

type syncOutput struct {
	Mode            string         `json:"mode"`
	DryRun          bool           `json:"dry_run"`
	Tests           []syncItem     `json:"tests,omitempty"`
	Workflows       []syncItem     `json:"workflows,omitempty"`
	AppLinks        []syncItem     `json:"app_links,omitempty"`
	HotReloadChecks []syncItem     `json:"hotreload_checks,omitempty"`
	Summary         map[string]int `json:"summary"`
}

type syncFlagValues struct {
	tests            bool
	apps             bool
	nonInteractive   bool
	interactive      bool
	prune            bool
	dryRun           bool
	skipImport       bool
	skipHotReloadChk bool
	bootstrap        bool
	workflow         string
}

func registerSyncFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("tests", false, "Sync tests")
	cmd.Flags().Bool("apps", false, "Sync build platform app_id links")
	cmd.Flags().Bool("non-interactive", false, "Disable prompts and apply deterministic defaults")
	cmd.Flags().Bool("interactive", false, "Force interactive prompts (requires TTY stdin)")
	cmd.Flags().Bool("prune", false, "Auto-prune stale/deleted mappings")
	cmd.Flags().Bool("dry-run", false, "Show planned actions without writing files")
	cmd.Flags().Bool("skip-import", false, "Skip importing remote-only tests (only sync tests already in .revyl/tests/)")
	cmd.Flags().String("workflow", "", "Only sync tests belonging to this workflow (name or ID)")
	cmd.Flags().Bool("skip-hotreload-check", false, "Skip validating hotreload platform key mappings")
	cmd.Flags().Bool("bootstrap", false, "Rebuild config mappings from local YAML _meta.remote_id values (useful after cloning)")
}

func readSyncFlags(cmd *cobra.Command) (syncFlagValues, error) {
	tests, err := cmd.Flags().GetBool("tests")
	if err != nil {
		return syncFlagValues{}, err
	}
	apps, err := cmd.Flags().GetBool("apps")
	if err != nil {
		return syncFlagValues{}, err
	}
	nonInteractive, err := cmd.Flags().GetBool("non-interactive")
	if err != nil {
		return syncFlagValues{}, err
	}
	interactive, err := cmd.Flags().GetBool("interactive")
	if err != nil {
		return syncFlagValues{}, err
	}
	prune, err := cmd.Flags().GetBool("prune")
	if err != nil {
		return syncFlagValues{}, err
	}
	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return syncFlagValues{}, err
	}
	skipImport, err := cmd.Flags().GetBool("skip-import")
	if err != nil {
		return syncFlagValues{}, err
	}
	workflow, err := cmd.Flags().GetString("workflow")
	if err != nil {
		return syncFlagValues{}, err
	}
	skipHotReloadChk, err := cmd.Flags().GetBool("skip-hotreload-check")
	if err != nil {
		return syncFlagValues{}, err
	}
	bootstrap, err := cmd.Flags().GetBool("bootstrap")
	if err != nil {
		return syncFlagValues{}, err
	}

	return syncFlagValues{
		tests:            tests,
		apps:             apps,
		nonInteractive:   nonInteractive,
		interactive:      interactive,
		prune:            prune,
		dryRun:           dryRun,
		skipImport:       skipImport,
		skipHotReloadChk: skipHotReloadChk,
		bootstrap:        bootstrap,
		workflow:         workflow,
	}, nil
}

func runSync(cmd *cobra.Command, args []string) error {
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")
	devMode, _ := cmd.Flags().GetBool("dev")

	flags, err := readSyncFlags(cmd)
	if err != nil {
		return fmt.Errorf("failed to read sync flags: %w", err)
	}

	runTests := flags.tests
	runApps := flags.apps
	if !runTests && !runApps {
		runTests = true
		runApps = true
	}

	interactiveWanted := true
	if flags.nonInteractive {
		interactiveWanted = false
	}
	if flags.interactive {
		interactiveWanted = true
	}
	if jsonOutput {
		interactiveWanted = false
	}

	stdinTTY := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	if !stdinTTY && interactiveWanted {
		if flags.interactive {
			return fmt.Errorf("--interactive requires a TTY on stdin")
		}
		interactiveWanted = false
	}

	skipImport := flags.skipImport
	if !interactiveWanted && !flags.skipImport {
		skipImport = true
	}

	opts := syncOptions{
		Prompt:         interactiveWanted,
		Prune:          flags.prune,
		DryRun:         flags.dryRun,
		SkipImport:     skipImport,
		JSONOutput:     jsonOutput,
		WorkflowFilter: flags.workflow,
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}
	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	testsDir := filepath.Join(cwd, ".revyl", "tests")

	cfg, err := config.LoadProjectConfig(configPath)
	if err != nil {
		ui.PrintError("Project not initialized. Run 'revyl init' first.")
		return err
	}

	if flags.bootstrap {
		return runBootstrap(cfg, configPath, testsDir)
	}

	client := api.NewClientWithDevMode(apiKey, devMode)

	useSpinner := !jsonOutput && !opts.Prompt
	if useSpinner {
		ui.StartSpinner("Reconciling project state...")
	}

	ctx := cmd.Context()
	out := syncOutput{
		Mode:    modeLabel(opts.Prompt),
		DryRun:  opts.DryRun,
		Summary: map[string]int{},
	}

	changed := false
	hadError := false

	if runTests {
		items, domainChanged, domainErr := syncTestsDomain(ctx, client, cfg, testsDir, opts)
		out.Tests = items
		if domainChanged {
			changed = true
		}
		if domainErr != nil {
			hadError = true
		}
	}

	if runApps {
		items, domainChanged, domainErr := syncAppLinksDomain(ctx, client, cfg, opts)
		out.AppLinks = items
		if domainChanged {
			changed = true
		}
		if domainErr != nil {
			hadError = true
		}
	}

	if !flags.skipHotReloadChk {
		items, domainErr := syncHotReloadDomain(ctx, client, cfg)
		out.HotReloadChecks = items
		if domainErr != nil {
			hadError = true
		}
	}

	if changed && !opts.DryRun {
		cfg.MarkSynced()
		if err := config.WriteProjectConfig(configPath, cfg); err != nil {
			hadError = true
			out.Tests = append(out.Tests, syncItem{
				Name:    ".revyl/config.yaml",
				Status:  "error",
				Action:  "write",
				Error:   err.Error(),
				Message: "failed to persist project config",
			})
		}
	}

	if useSpinner {
		ui.StopSpinner()
	}

	out.Summary = computeSyncSummary(out)

	if jsonOutput {
		data, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(data))
	} else {
		printSyncOutput(out)
	}

	if hadError {
		return fmt.Errorf("sync completed with errors")
	}
	return nil
}

func modeLabel(prompt bool) string {
	if prompt {
		return "interactive"
	}
	return "non_interactive"
}

func computeSyncSummary(out syncOutput) map[string]int {
	summary := map[string]int{
		"tests":            len(out.Tests),
		"workflows":        len(out.Workflows),
		"app_links":        len(out.AppLinks),
		"hotreload_checks": len(out.HotReloadChecks),
	}
	for _, group := range [][]syncItem{out.Tests, out.Workflows, out.AppLinks, out.HotReloadChecks} {
		for _, item := range group {
			status := strings.TrimSpace(item.Status)
			if status == "" {
				continue
			}
			summary["status_"+status]++
			if item.Action != "" {
				summary["action_"+item.Action]++
			}
			if item.Error != "" {
				summary["errors"]++
			}
		}
	}
	return summary
}

func printSyncOutput(out syncOutput) {
	ui.Println()
	ui.PrintInfo("Sync mode: %s", out.Mode)
	if out.DryRun {
		ui.PrintWarning("Dry-run enabled: no changes were written")
	}

	printSyncSection("Tests", out.Tests)
	printSyncSection("Workflows", out.Workflows)
	printSyncSection("App Links", out.AppLinks)
	printSyncSection("Hot Reload", out.HotReloadChecks)

	printSyncSummary(out.Summary)
}

// printSyncSummary renders a human-readable summary with aligned key-value
// pairs. Top-level totals are always shown; action breakdowns only appear
// when non-zero.
func printSyncSummary(summary map[string]int) {
	ui.Println()
	ui.PrintInfo("Summary")
	ui.PrintKeyValue("Tests:", fmt.Sprintf("%d", summary["tests"]))
	if summary["workflows"] > 0 {
		ui.PrintKeyValue("Workflows:", fmt.Sprintf("%d", summary["workflows"]))
	}
	if summary["app_links"] > 0 {
		ui.PrintKeyValue("App links:", fmt.Sprintf("%d", summary["app_links"]))
	}

	actionLabels := []struct {
		key   string
		label string
	}{
		{"action_import", "Imported:"},
		{"action_would-import", "Would import:"},
		{"action_link", "Linked:"},
		{"action_would-link", "Would link:"},
		{"action_skip-import", "Skipped import:"},
		{"action_pull", "Pulled:"},
		{"action_would-pull", "Would pull:"},
		{"action_push", "Pushed:"},
		{"action_would-push", "Would push:"},
		{"action_keep-local", "Kept local:"},
		{"action_detach", "Detached:"},
		{"action_would-detach", "Would detach:"},
		{"action_relink", "Relinked:"},
		{"action_would-relink", "Would relink:"},
		{"action_prune-all", "Pruned:"},
		{"action_prune-alias", "Alias pruned:"},
		{"errors", "Errors:"},
	}
	for _, al := range actionLabels {
		if v := summary[al.key]; v > 0 {
			ui.PrintKeyValue(al.label, fmt.Sprintf("%d", v))
		}
	}
}

const (
	syncCollapseThreshold = 10
	syncCollapsedVisible  = 5
)

// syncGroupMeta defines display properties for a group of sync items
// sharing the same (status, action) pair.
type syncGroupMeta struct {
	icon  string
	label string
	style func(...string) string
}

// syncGroupDisplay returns the display icon, label, and lipgloss style
// renderer for a given (status, action) combination.
func syncGroupDisplay(status, action string) syncGroupMeta {
	switch {
	case action == "import":
		return syncGroupMeta{"↓", "Imported from remote", ui.SuccessStyle.Render}
	case action == "would-import":
		return syncGroupMeta{"↓", "Remote only — would import", ui.DimStyle.Render}
	case action == "pull":
		return syncGroupMeta{"↓", "Pulled from remote", ui.SuccessStyle.Render}
	case action == "would-pull":
		return syncGroupMeta{"↓", "Would pull from remote", ui.DimStyle.Render}
	case action == "push":
		return syncGroupMeta{"↑", "Pushed to remote", ui.SuccessStyle.Render}
	case action == "would-push":
		return syncGroupMeta{"↑", "Would push to remote", ui.DimStyle.Render}
	case action == "link":
		return syncGroupMeta{"↔", "Linked to remote", ui.SuccessStyle.Render}
	case action == "would-link":
		return syncGroupMeta{"↔", "Name match — would link", ui.WarningStyle.Render}
	case action == "skip-import":
		return syncGroupMeta{"·", "Skipped import", ui.DimStyle.Render}
	case action == "keep-local":
		return syncGroupMeta{"●", "Local only", ui.DimStyle.Render}
	case action == "detach":
		return syncGroupMeta{"✗", "Detached", ui.WarningStyle.Render}
	case action == "would-detach":
		return syncGroupMeta{"✗", "Would detach", ui.WarningStyle.Render}
	case action == "relink":
		return syncGroupMeta{"→", "Relinked", ui.SuccessStyle.Render}
	case action == "would-relink":
		return syncGroupMeta{"→", "Would relink", ui.DimStyle.Render}
	case action == "prune-all", action == "would-prune-all":
		return syncGroupMeta{"✗", "Pruned", ui.WarningStyle.Render}
	case action == "prune-alias", action == "would-prune-alias":
		return syncGroupMeta{"✗", "Alias pruned", ui.WarningStyle.Render}
	case status == "synced":
		return syncGroupMeta{"✓", "Synced", ui.SuccessStyle.Render}
	case status == "warning":
		return syncGroupMeta{"⚠", "Warnings", ui.WarningStyle.Render}
	case status == "stale":
		return syncGroupMeta{"⚠", "Stale", ui.WarningStyle.Render}
	case status == "conflict":
		return syncGroupMeta{"⚠", "Conflicts", ui.WarningStyle.Render}
	default:
		return syncGroupMeta{"·", status + "/" + action, ui.DimStyle.Render}
	}
}

func printSyncSection(title string, items []syncItem) {
	if len(items) == 0 {
		return
	}

	ui.Println()
	fmt.Fprintf(os.Stderr, "%s %s\n",
		ui.TitleStyle.Render(title),
		ui.AccentStyle.Render(fmt.Sprintf("(%d)", len(items))))

	type groupKey struct{ status, action string }
	var keyOrder []groupKey
	groups := make(map[groupKey][]syncItem)
	for _, it := range items {
		st := it.Status
		if st == "" {
			st = "unknown"
		}
		act := it.Action
		if act == "" {
			act = "none"
		}
		k := groupKey{st, act}
		if _, exists := groups[k]; !exists {
			keyOrder = append(keyOrder, k)
		}
		groups[k] = append(groups[k], it)
	}

	for _, k := range keyOrder {
		grp := groups[k]
		meta := syncGroupDisplay(k.status, k.action)

		header := fmt.Sprintf("  %s %s (%d)", meta.icon, meta.label, len(grp))
		fmt.Fprintln(os.Stderr, meta.style(header))

		visible := len(grp)
		collapsed := false
		if visible > syncCollapseThreshold {
			visible = syncCollapsedVisible
			collapsed = true
		}

		for _, it := range grp[:visible] {
			printSyncItemLine(it)
		}
		if collapsed {
			remaining := len(grp) - visible
			fmt.Fprintln(os.Stderr, ui.DimStyle.Render(
				fmt.Sprintf("    ... and %d more", remaining)))
		}
	}
}

// printSyncItemLine renders a single sync item as an indented line with the
// test name (truncated to 45 chars) and an optional abbreviated ID.
func printSyncItemLine(it syncItem) {
	name := it.Name
	if len(name) > 45 {
		name = name[:42] + "..."
	}
	line := fmt.Sprintf("%-45s", name)
	if it.ID != "" {
		line += "  " + truncatePrefix(it.ID, 8)
	}
	if it.Error != "" {
		ui.PrintError("    %s  %s", line, it.Error)
	} else {
		fmt.Fprintln(os.Stderr, ui.DimStyle.Render("    "+line))
	}
}

func syncTestsDomain(ctx context.Context, client *api.Client, cfg *config.ProjectConfig, testsDir string, opts syncOptions) ([]syncItem, bool, error) {
	items := make([]syncItem, 0)
	changed := false
	hadErr := false

	remoteTests, err := client.ListAllOrgTests(ctx, 200)
	if err != nil {
		items = append(items, syncItem{Name: "tests", Status: "error", Action: "list", Error: err.Error()})
		return items, changed, err
	}

	// When --workflow is set, narrow remoteTests to only tests in that workflow.
	if opts.WorkflowFilter != "" {
		wfID, wfName, wfErr := resolveWorkflowID(ctx, opts.WorkflowFilter, cfg, client)
		if wfErr != nil {
			items = append(items, syncItem{Name: opts.WorkflowFilter, Status: "error", Action: "resolve-workflow", Error: wfErr.Error()})
			return items, changed, wfErr
		}
		wfInfo, wfInfoErr := client.GetWorkflowInfo(ctx, wfID)
		if wfInfoErr != nil {
			items = append(items, syncItem{Name: opts.WorkflowFilter, Status: "error", Action: "fetch-workflow", Error: wfInfoErr.Error()})
			return items, changed, wfInfoErr
		}
		if wfName == "" && wfInfo != nil {
			wfName = wfInfo.Name
		}
		allowedIDs := make(map[string]bool, len(wfInfo.TestInfo))
		for _, ti := range wfInfo.TestInfo {
			allowedIDs[ti.ID] = true
		}
		filtered := make([]api.SimpleTest, 0, len(allowedIDs))
		for _, rt := range remoteTests {
			if allowedIDs[rt.ID] {
				filtered = append(filtered, rt)
			}
		}
		remoteTests = filtered

		if !opts.JSONOutput {
			ui.PrintInfo("Syncing tests from workflow: %q (%d tests)", wfName, len(remoteTests))
			ui.Println()
		}
	}

	remoteByID := make(map[string]api.SimpleTest, len(remoteTests))
	for _, t := range remoteTests {
		remoteByID[t.ID] = t
	}

	localTests, lErr := config.LoadLocalTests(testsDir)
	if lErr != nil {
		items = append(items, syncItem{Name: "local-tests", Status: "warning", Action: "load", Error: lErr.Error(), Message: "continuing with remote/config state"})
		localTests = make(map[string]*config.LocalTest)
	}

	sort.Slice(remoteTests, func(i, j int) bool {
		if strings.EqualFold(remoteTests[i].Name, remoteTests[j].Name) {
			return remoteTests[i].ID < remoteTests[j].ID
		}
		return strings.ToLower(remoteTests[i].Name) < strings.ToLower(remoteTests[j].Name)
	})

	existingIDs := make(map[string]bool)
	for _, lt := range localTests {
		if lt != nil && lt.Meta.RemoteID != "" {
			existingIDs[lt.Meta.RemoteID] = true
		}
	}

	// Phase 1: Classify remote-only tests into name-matches and pure imports.
	type pendingImport struct {
		alias string
		rt    api.SimpleTest
	}
	var nameMatches []pendingImport
	var pureImports []pendingImport
	justLinked := make(map[string]bool)

	for _, rt := range remoteTests {
		if existingIDs[rt.ID] {
			continue
		}
		alias := util.SanitizeForFilename(rt.Name)
		if alias == "" {
			alias = fmt.Sprintf("test-%s", truncatePrefix(rt.ID, 8))
		}

		if lt, exists := localTests[alias]; exists && lt != nil && lt.Meta.RemoteID == "" {
			nameMatches = append(nameMatches, pendingImport{alias: alias, rt: rt})
		} else {
			pureImports = append(pureImports, pendingImport{alias: ensureUniqueAlias(alias, localTests), rt: rt})
		}
	}

	// Phase 2: Handle name-matched tests (local file exists, same name, no remote_id).
	for _, nm := range nameMatches {
		if justLinked[nm.alias] {
			items = append(items, syncItem{
				Name:    nm.alias,
				ID:      nm.rt.ID,
				Status:  "collision",
				Action:  "skip",
				Message: "alias already linked to another remote test this sync",
			})
			continue
		}

		lt := localTests[nm.alias]
		localSummary := describeLocalTest(lt)
		remoteSummary := describeRemoteSimpleTest(nm.rt)

		if opts.DryRun {
			items = append(items, syncItem{
				Name:    nm.alias,
				ID:      nm.rt.ID,
				Status:  "name-match",
				Action:  "would-link",
				Message: fmt.Sprintf("would pull remote → local | local: %s | remote: %s", localSummary, remoteSummary),
			})
			continue
		}

		decision := "pull"
		if opts.Prompt {
			ui.Println()
			ui.PrintInfo("Name match: '%s' (%s)", nm.alias, truncatePrefix(nm.rt.ID, 8))
			ui.PrintDim("  Local:  %s", localSummary)
			ui.PrintDim("  Remote: %s", remoteSummary)
			decision = promptNameMatchAction(nm.alias)
		}

		if decision == "skip" {
			items = append(items, syncItem{
				Name:    nm.alias,
				ID:      nm.rt.ID,
				Status:  "name-match",
				Action:  "skip",
				Message: "name match skipped",
			})
			continue
		}

		lt.Meta.RemoteID = nm.rt.ID
		localPath := filepath.Join(testsDir, nm.alias+".yaml")
		if saveErr := config.SaveLocalTest(localPath, lt); saveErr != nil {
			items = append(items, syncItem{
				Name: nm.alias, ID: nm.rt.ID, Status: "error",
				Action: "link", Error: saveErr.Error(),
			})
			continue
		}
		existingIDs[nm.rt.ID] = true
		justLinked[nm.alias] = true
		changed = true

		switch decision {
		case "push":
			if pushErr := pushSingleTest(ctx, client, cfg, testsDir, nm.alias); pushErr != nil {
				items = append(items, syncItem{
					Name: nm.alias, ID: nm.rt.ID, Status: "name-match",
					Action: "link", Error: "linked but push failed: " + pushErr.Error(),
				})
			} else {
				items = append(items, syncItem{
					Name:    nm.alias,
					ID:      nm.rt.ID,
					Status:  "name-match",
					Action:  "link",
					Message: "linked and pushed local → remote",
				})
			}
		case "pull":
			if pullErr := pullSingleTest(ctx, client, cfg, testsDir, nm.alias); pullErr != nil {
				items = append(items, syncItem{
					Name: nm.alias, ID: nm.rt.ID, Status: "name-match",
					Action: "link", Error: "linked but pull failed: " + pullErr.Error(),
				})
			} else {
				items = append(items, syncItem{
					Name:    nm.alias,
					ID:      nm.rt.ID,
					Status:  "name-match",
					Action:  "link",
					Message: "linked and pulled remote → local",
				})
			}
		}
	}

	// Phase 3: Handle pure imports (remote tests with no local counterpart).
	if opts.SkipImport {
		if len(pureImports) > 0 && !opts.JSONOutput {
			ui.PrintDim("  Skipped %d remote-only test(s) (--skip-import)", len(pureImports))
		}
		for _, pi := range pureImports {
			items = append(items, syncItem{
				Name:    pi.alias,
				ID:      pi.rt.ID,
				Status:  "remote-only",
				Action:  "skip-import",
				Message: "skipped (--skip-import)",
			})
		}
	} else if opts.DryRun {
		for _, pi := range pureImports {
			items = append(items, syncItem{
				Name:    pi.alias,
				ID:      pi.rt.ID,
				Status:  "remote-only",
				Action:  "would-import",
				Message: "discovered from organization and added to local tests",
			})
		}
	} else {
		doImport := true
		if opts.Prompt && len(pureImports) > 0 {
			doImport = ui.Confirm(fmt.Sprintf(
				"Import %d remote-only test(s) into .revyl/tests/?", len(pureImports)))
		}

		if doImport {
			for _, pi := range pureImports {
				if mkErr := os.MkdirAll(testsDir, 0755); mkErr != nil {
					items = append(items, syncItem{
						Name: pi.alias, ID: pi.rt.ID, Status: "error",
						Action: "import", Error: mkErr.Error(),
					})
					continue
				}
				newTest := &config.LocalTest{
					Meta: config.TestMeta{RemoteID: pi.rt.ID},
				}
				localPath := filepath.Join(testsDir, pi.alias+".yaml")
				if saveErr := config.SaveLocalTest(localPath, newTest); saveErr != nil {
					items = append(items, syncItem{
						Name: pi.alias, ID: pi.rt.ID, Status: "error",
						Action: "import", Error: saveErr.Error(),
					})
					continue
				}
				localTests[pi.alias] = newTest
				existingIDs[pi.rt.ID] = true
				changed = true
				items = append(items, syncItem{
					Name:    pi.alias,
					ID:      pi.rt.ID,
					Status:  "remote-only",
					Action:  "import",
					Message: "discovered from organization and added to local tests",
				})
			}
		} else {
			for _, pi := range pureImports {
				items = append(items, syncItem{
					Name:    pi.alias,
					ID:      pi.rt.ID,
					Status:  "remote-only",
					Action:  "skip-import",
					Message: "import declined",
				})
			}
		}
	}

	resolver := syncpkg.NewResolver(client, cfg, localTests)
	statuses, sErr := resolver.GetAllStatuses(ctx)
	if sErr != nil {
		items = append(items, syncItem{Name: "test-status", Status: "error", Action: "status", Error: sErr.Error()})
		return items, changed, sErr
	}

	sort.Slice(statuses, func(i, j int) bool {
		return strings.ToLower(statuses[i].Name) < strings.ToLower(statuses[j].Name)
	})

	for _, st := range statuses {
		if justLinked[st.Name] {
			continue
		}

		item := syncItem{
			Name:   st.Name,
			ID:     st.RemoteID,
			Status: st.Status.String(),
		}

		if st.Status == syncpkg.StatusOrphaned {
			item.Status = "stale"
			if st.LinkIssueMessage != "" {
				item.Message = st.LinkIssueMessage
			} else {
				item.Message = "remote link is stale or inaccessible"
			}

			localTest, localPath, hasLocalFile := resolveLocalTestForAlias(localTests, testsDir, st.Name)

			action := "keep"
			if opts.Prune {
				action = "detach"
			} else if opts.Prompt {
				action = promptOrphanedTestAction(st.Name, st.LinkIssue, hasLocalFile)
				item.Prompted = true
			}
			item.Action = action

			if opts.DryRun {
				if action != "keep" {
					item.Action = "would-" + action
				}
				items = append(items, item)
				continue
			}

			switch action {
			case "detach":
				mutated, err := detachTestLink(cfg, st.Name, localTest, localPath)
				if err != nil {
					item.Error = err.Error()
					hadErr = true
				} else if mutated {
					changed = true
					item.Message = "detached stale remote link and kept local file"
				}
			case "prune-all":
				mutated, err := detachTestLink(cfg, st.Name, localTest, localPath)
				if err != nil {
					item.Error = err.Error()
					hadErr = true
					items = append(items, item)
					continue
				}
				if mutated {
					changed = true
				}
				if hasLocalFile {
					rmErr := os.Remove(localPath)
					if rmErr != nil && !os.IsNotExist(rmErr) {
						item.Error = rmErr.Error()
						hadErr = true
					} else {
						changed = true
						item.Message = "removed stale mapping and local test file"
					}
				}
			default:
				item.Message = "stale link kept unchanged"
			}

			items = append(items, item)
			continue
		}

		if st.ErrorMessage != "" {
			item.Error = st.ErrorMessage
			hadErr = true
			items = append(items, item)
			continue
		}

		if staleLT := localTests[st.Name]; staleLT != nil && staleLT.Meta.RemoteID != "" {
			if _, exists := remoteByID[staleLT.Meta.RemoteID]; !exists {
				item.Status = "stale"
				localTest, localPath, hasLocalFile := resolveLocalTestForAlias(localTests, testsDir, st.Name)
				hasLocalChanges := false
				if hasLocalFile {
					if localTest == nil {
						loaded, lErr := config.LoadLocalTest(localPath)
						if lErr != nil {
							hasLocalChanges = true
						} else {
							localTest = loaded
						}
					}
					if localTest != nil {
						hasLocalChanges = localTest.HasLocalChanges()
					}
				}

				action := "keep"
				if opts.Prune {
					if hasLocalFile && !hasLocalChanges {
						action = "prune-all"
					} else if hasLocalFile && hasLocalChanges {
						action = "detach"
					} else {
						action = "prune-alias"
					}
				} else if opts.Prompt {
					action = promptStaleTestAction(st.Name, hasLocalFile)
					item.Prompted = true
				}
				item.Action = action

				if !opts.DryRun {
					switch action {
					case "detach":
						mutated, err := detachTestLink(cfg, st.Name, localTest, localPath)
						if err != nil {
							item.Error = err.Error()
							hadErr = true
						} else if mutated {
							changed = true
							item.Message = "detached stale mapping and kept modified local test file"
						}
					case "prune-alias":
						mutated, pErr := detachTestLink(cfg, st.Name, localTest, localPath)
						if pErr != nil {
							item.Error = pErr.Error()
							hadErr = true
						} else if mutated {
							changed = true
						}
					case "prune-all":
						changed = true
						if hasLocalFile {
							rmErr := os.Remove(localPath)
							if rmErr != nil && !os.IsNotExist(rmErr) {
								item.Error = rmErr.Error()
								hadErr = true
							}
						}
					}
				} else if action != "keep" {
					item.Action = "would-" + action
				}

				items = append(items, item)
				continue
			}
		}

		switch st.Status {
		case syncpkg.StatusOutdated, syncpkg.StatusRemoteOnly:
			item.Action = "pull"
			if opts.DryRun {
				item.Action = "would-pull"
				items = append(items, item)
				continue
			}
			if err := pullSingleTest(ctx, client, cfg, testsDir, st.Name); err != nil {
				item.Error = err.Error()
				hadErr = true
			} else {
				changed = true
				item.Message = "pulled remote changes"
			}

		case syncpkg.StatusModified:
			item.Action = "push"
			if opts.DryRun {
				item.Action = "would-push"
				items = append(items, item)
				continue
			}
			if err := pushSingleTest(ctx, client, cfg, testsDir, st.Name); err != nil {
				item.Error = err.Error()
				hadErr = true
			} else {
				changed = true
				item.Message = "pushed local changes"
			}

		case syncpkg.StatusLocalOnly:
			item.Action = "keep-local"
			item.Message = "local-only test kept unchanged (no remote link)"

		case syncpkg.StatusConflict:
			decision := "skip"
			if opts.Prompt {
				decision = promptConflictAction(st.Name)
				item.Prompted = true
			}
			item.Action = decision
			if opts.DryRun {
				item.Action = "would-" + decision
				items = append(items, item)
				continue
			}
			switch decision {
			case "pull":
				if err := pullSingleTest(ctx, client, cfg, testsDir, st.Name); err != nil {
					item.Error = err.Error()
					hadErr = true
				} else {
					changed = true
				}
			case "push":
				if err := pushSingleTest(ctx, client, cfg, testsDir, st.Name); err != nil {
					item.Error = err.Error()
					hadErr = true
				} else {
					changed = true
				}
			default:
				item.Message = "conflict left unchanged"
			}

		default:
			item.Action = "none"
		}

		items = append(items, item)
	}

	dups := duplicateAliasesByID(localTests)
	for id, aliasesForID := range dups {
		if len(aliasesForID) < 2 {
			continue
		}
		sort.Strings(aliasesForID)
		keep := aliasesForID[0]
		for _, alias := range aliasesForID[1:] {
			item := syncItem{
				Name:    alias,
				ID:      id,
				Status:  "duplicate",
				Message: fmt.Sprintf("duplicates %s", keep),
			}
			action := "keep"
			if opts.Prune {
				action = "prune-alias"
			} else if opts.Prompt {
				action = promptDuplicateAliasAction("test", alias, keep)
				item.Prompted = true
			}
			item.Action = action
			if opts.DryRun && action == "prune-alias" {
				item.Action = "would-prune-alias"
			} else if !opts.DryRun && action == "prune-alias" {
				if dupLT := localTests[alias]; dupLT != nil {
					dupLT.Meta.RemoteID = ""
					dupLT.Meta.RemoteVersion = 0
					dupLT.Meta.LastSyncedAt = ""
					dupPath := filepath.Join(testsDir, alias+".yaml")
					_ = config.SaveLocalTest(dupPath, dupLT)
				}
				changed = true
			}
			items = append(items, item)
		}
	}

	if hadErr {
		return items, changed, fmt.Errorf("one or more test sync actions failed")
	}
	return items, changed, nil
}

func syncAppLinksDomain(ctx context.Context, client *api.Client, cfg *config.ProjectConfig, opts syncOptions) ([]syncItem, bool, error) {
	items := make([]syncItem, 0)
	changed := false
	hadErr := false

	platforms := sortedBuildPlatforms(cfg)
	for _, platformKey := range platforms {
		platformCfg := cfg.Build.Platforms[platformKey]
		if platformCfg.AppID == "" {
			continue
		}

		expectedPlatform := inferPlatformFromKey(platformKey)
		item := syncItem{Name: platformKey, ID: platformCfg.AppID, Status: "synced", Action: "none"}

		app, err := client.GetApp(ctx, platformCfg.AppID)
		if err != nil {
			if isAPIStatus(err, 404) {
				item.Status = "stale"
				action := "keep"
				if opts.Prune {
					action = "clear"
				} else if opts.Prompt {
					action = promptAppLinkAction(platformKey, expectedPlatform, true)
					item.Prompted = true
				}
				item.Action = action
				if opts.DryRun {
					if action != "keep" {
						item.Action = "would-" + action
					}
				} else {
					if action == "clear" {
						platformCfg.AppID = ""
						cfg.Build.Platforms[platformKey] = platformCfg
						changed = true
					} else if action == "relink" {
						appID, appName, rErr := promptRelinkApp(ctx, client, expectedPlatform)
						if rErr != nil {
							item.Error = rErr.Error()
							hadErr = true
						} else if appID != "" {
							platformCfg.AppID = appID
							cfg.Build.Platforms[platformKey] = platformCfg
							item.Action = "relink"
							item.Message = fmt.Sprintf("relinked to %s", appName)
							changed = true
						}
					}
				}
				items = append(items, item)
				continue
			}

			item.Status = "error"
			item.Action = "validate"
			item.Error = err.Error()
			hadErr = true
			items = append(items, item)
			continue
		}

		actualPlatform := strings.ToLower(app.Platform)
		if expectedPlatform != "" && syncNormalizePlatform(actualPlatform) != syncNormalizePlatform(expectedPlatform) {
			item.Status = "mismatch"
			item.Message = fmt.Sprintf("app platform is %s", app.Platform)

			action := "keep"
			if opts.Prune {
				action = "clear"
			} else if opts.Prompt {
				action = promptAppLinkAction(platformKey, expectedPlatform, false)
				item.Prompted = true
			}
			item.Action = action

			if opts.DryRun {
				if action != "keep" {
					item.Action = "would-" + action
				}
			} else {
				if action == "clear" {
					platformCfg.AppID = ""
					cfg.Build.Platforms[platformKey] = platformCfg
					changed = true
				} else if action == "relink" {
					appID, appName, rErr := promptRelinkApp(ctx, client, expectedPlatform)
					if rErr != nil {
						item.Error = rErr.Error()
						hadErr = true
					} else if appID != "" {
						platformCfg.AppID = appID
						cfg.Build.Platforms[platformKey] = platformCfg
						item.Action = "relink"
						item.Message = fmt.Sprintf("relinked to %s", appName)
						changed = true
					}
				}
			}
		} else {
			item.Status = "synced"
			item.Action = "none"
			item.Message = fmt.Sprintf("linked to %s (%s)", app.Name, app.Platform)
		}

		items = append(items, item)
	}

	if hadErr {
		return items, changed, fmt.Errorf("one or more app-link sync actions failed")
	}
	return items, changed, nil
}

func syncHotReloadDomain(ctx context.Context, client *api.Client, cfg *config.ProjectConfig) ([]syncItem, error) {
	_ = ctx
	_ = client

	items := make([]syncItem, 0)
	hadErr := false

	providerNames := make([]string, 0, len(cfg.HotReload.Providers))
	for name := range cfg.HotReload.Providers {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)

	for _, providerName := range providerNames {
		providerCfg := cfg.HotReload.Providers[providerName]
		if providerCfg == nil {
			continue
		}

		if len(providerCfg.PlatformKeys) == 0 {
			continue
		}

		targetPlatforms := make([]string, 0, len(providerCfg.PlatformKeys))
		for platform := range providerCfg.PlatformKeys {
			targetPlatforms = append(targetPlatforms, platform)
		}
		sort.Strings(targetPlatforms)

		for _, targetPlatform := range targetPlatforms {
			platformKey := strings.TrimSpace(providerCfg.PlatformKeys[targetPlatform])
			if platformKey == "" {
				continue
			}

			item := syncItem{
				Name:   fmt.Sprintf("%s.%s", providerName, targetPlatform),
				ID:     platformKey,
				Status: "ok",
				Action: "validate",
			}

			normalizedPlatform := syncNormalizePlatform(targetPlatform)
			if normalizedPlatform != "ios" && normalizedPlatform != "android" {
				item.Status = "warning"
				item.Message = "unknown target platform in platform_keys (expected ios/android)"
				hadErr = true
				items = append(items, item)
				continue
			}

			platformCfg, ok := cfg.Build.Platforms[platformKey]
			if !ok {
				item.Status = "warning"
				item.Message = "mapped build platform key not found in build.platforms"
				hadErr = true
			} else if strings.TrimSpace(platformCfg.AppID) == "" {
				item.Status = "warning"
				item.Message = "mapped build platform has no app_id"
			} else {
				item.Status = "synced"
				item.Action = "none"
				item.Message = fmt.Sprintf("mapped to build.platforms.%s", platformKey)
			}

			items = append(items, item)
		}
	}

	if hadErr {
		return items, fmt.Errorf("one or more hotreload platform mappings are invalid")
	}
	return items, nil
}

func pullSingleTest(ctx context.Context, client *api.Client, cfg *config.ProjectConfig, testsDir, testName string) error {
	localTests, err := config.LoadLocalTests(testsDir)
	if err != nil {
		localTests = make(map[string]*config.LocalTest)
	}
	resolver := syncpkg.NewResolver(client, cfg, localTests)
	results, err := resolver.PullFromRemote(ctx, testName, testsDir, false)
	if err != nil {
		return err
	}
	if len(results) > 0 {
		if results[0].Error != nil {
			return results[0].Error
		}
		if results[0].Conflict {
			return fmt.Errorf("conflict detected")
		}
	}
	return nil
}

func pushSingleTest(ctx context.Context, client *api.Client, cfg *config.ProjectConfig, testsDir, testName string) error {
	localTests, err := config.LoadLocalTests(testsDir)
	if err != nil {
		return err
	}
	if err := validateTestsForPush([]string{testName}, testsDir, localTests); err != nil {
		return err
	}
	resolver := syncpkg.NewResolver(client, cfg, localTests)
	results, err := resolver.SyncToRemote(ctx, testName, testsDir, false)
	if err != nil {
		return err
	}
	if len(results) > 0 {
		if results[0].Error != nil {
			return results[0].Error
		}
		if results[0].Conflict {
			return fmt.Errorf("conflict detected")
		}
	}
	return nil
}

func sortedBuildPlatforms(cfg *config.ProjectConfig) []string {
	keys := make([]string, 0, len(cfg.Build.Platforms))
	for k := range cfg.Build.Platforms {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func ensureUniqueAlias(base string, localTests map[string]*config.LocalTest) string {
	if localTests[base] == nil {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if localTests[candidate] == nil {
			return candidate
		}
	}
}

func duplicateAliasesByID(localTests map[string]*config.LocalTest) map[string][]string {
	byID := make(map[string][]string)
	for alias, lt := range localTests {
		if lt == nil || lt.Meta.RemoteID == "" {
			continue
		}
		byID[lt.Meta.RemoteID] = append(byID[lt.Meta.RemoteID], alias)
	}
	dups := make(map[string][]string)
	for id, aliases := range byID {
		if len(aliases) > 1 {
			dups[id] = aliases
		}
	}
	return dups
}

func syncFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func resolveLocalTestForAlias(localTests map[string]*config.LocalTest, testsDir, alias string) (*config.LocalTest, string, bool) {
	if lt, ok := localTests[alias]; ok {
		path := filepath.Join(testsDir, alias+".yaml")
		return lt, path, true
	}

	sanitized := util.SanitizeForFilename(alias)
	if sanitized != "" {
		path := filepath.Join(testsDir, sanitized+".yaml")
		if lt, ok := localTests[sanitized]; ok {
			return lt, path, true
		}
		return nil, path, syncFileExists(path)
	}

	path := filepath.Join(testsDir, alias+".yaml")
	return nil, path, syncFileExists(path)
}

func detachTestLink(_ *config.ProjectConfig, _ string, localTest *config.LocalTest, localPath string) (bool, error) {
	changed := false

	if localTest != nil {
		metaChanged := false
		if localTest.Meta.RemoteID != "" {
			localTest.Meta.RemoteID = ""
			metaChanged = true
		}
		if localTest.Meta.RemoteVersion != 0 {
			localTest.Meta.RemoteVersion = 0
			metaChanged = true
		}
		if localTest.Meta.LastSyncedAt != "" {
			localTest.Meta.LastSyncedAt = ""
			metaChanged = true
		}

		if metaChanged {
			if err := config.SaveLocalTest(localPath, localTest); err != nil {
				return changed, err
			}
			changed = true
		}
	}

	return changed, nil
}

func inferPlatformFromKey(key string) string {
	k := strings.ToLower(key)
	switch {
	case strings.Contains(k, "ios"):
		return "ios"
	case strings.Contains(k, "android"):
		return "android"
	default:
		return ""
	}
}

func syncNormalizePlatform(platform string) string {
	s := strings.ToLower(strings.TrimSpace(platform))
	switch s {
	case "ios":
		return "ios"
	case "android":
		return "android"
	default:
		return s
	}
}

func isAPIStatus(err error, statusCode int) bool {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == statusCode
	}
	return false
}

func promptStaleTestAction(name string, hasLocalFile bool) string {
	options := []ui.SelectOption{
		{Label: "Keep as-is", Value: "keep", Description: "Leave the test file and remote link unchanged."},
		{Label: "Unlink remote", Value: "prune-alias", Description: "Clear the remote link in the test YAML (keep the file)."},
	}
	if hasLocalFile {
		options = append(options, ui.SelectOption{Label: "Delete test file", Value: "prune-all", Description: "Remove the remote link and delete the local .yaml file."})
	}
	_, value, err := ui.Select(fmt.Sprintf("Test '%s' was not found remotely. Choose action:", name), options, 0)
	if err != nil {
		return "keep"
	}
	return value
}

func promptOrphanedTestAction(name string, issue syncpkg.RemoteLinkIssue, hasLocalFile bool) string {
	issueLabel := "stale"
	switch issue {
	case syncpkg.RemoteLinkIssueMissing:
		issueLabel = "not found remotely"
	case syncpkg.RemoteLinkIssueInvalidID:
		issueLabel = "invalid remote id"
	case syncpkg.RemoteLinkIssueUnauthorized:
		issueLabel = "unauthorized"
	case syncpkg.RemoteLinkIssueForbidden:
		issueLabel = "access denied"
	}

	options := []ui.SelectOption{
		{Label: "Keep as-is", Value: "keep", Description: "Leave the test file and remote link unchanged."},
		{Label: "Unlink remote", Value: "detach", Description: "Clear the remote link in the test YAML (keep the file)."},
	}
	if hasLocalFile {
		options = append(options, ui.SelectOption{Label: "Delete test file", Value: "prune-all", Description: "Remove the remote link and delete the local .yaml file."})
	}

	_, value, err := ui.Select(fmt.Sprintf("Test '%s' remote link is %s. Choose action:", name, issueLabel), options, 1)
	if err != nil {
		return "keep"
	}
	return value
}

func promptConflictAction(name string) string {
	options := []ui.SelectOption{
		{Label: "Skip", Value: "skip", Description: "Leave conflict unresolved for now."},
		{Label: "Pull remote", Value: "pull", Description: "Accept remote state and overwrite local if clean."},
		{Label: "Push local", Value: "push", Description: "Push local state to remote."},
	}
	_, value, err := ui.Select(fmt.Sprintf("Conflict for test '%s'. Choose action:", name), options, 0)
	if err != nil {
		return "skip"
	}
	return value
}

func promptNameMatchAction(name string) string {
	options := []ui.SelectOption{
		{Label: "Pull remote → local", Value: "pull", Description: "Take remote content, overwrite local."},
		{Label: "Push local → remote", Value: "push", Description: "Keep local content, overwrite remote."},
		{Label: "Skip (different tests)", Value: "skip", Description: "Don't link — leave both unchanged."},
	}
	_, value, err := ui.Select(fmt.Sprintf("'%s' exists locally and remotely. Which version to keep?", name), options, 0)
	if err != nil {
		return "pull"
	}
	return value
}

func describeLocalTest(lt *config.LocalTest) string {
	if lt == nil {
		return "empty"
	}
	platform := lt.Test.Metadata.Platform
	if platform == "" {
		platform = "?"
	}
	blocks := len(lt.Test.Blocks)
	build := lt.Test.Build.Name
	if build != "" {
		return fmt.Sprintf("%s, %d blocks, build: %s", platform, blocks, build)
	}
	return fmt.Sprintf("%s, %d blocks", platform, blocks)
}

func describeRemoteSimpleTest(rt api.SimpleTest) string {
	platform := strings.ToLower(rt.Platform)
	if platform == "" {
		platform = "?"
	}
	if rt.AppName != "" {
		return fmt.Sprintf("%s, build: %s", platform, rt.AppName)
	}
	return platform
}

func promptDuplicateAliasAction(kind, alias, keepAlias string) string {
	options := []ui.SelectOption{
		{Label: "Keep duplicate", Value: "keep", Description: "Retain both aliases."},
		{Label: "Prune duplicate", Value: "prune-alias", Description: "Remove duplicate alias mapping."},
	}
	_, value, err := ui.Select(fmt.Sprintf("Duplicate %s alias '%s' (also '%s'). Choose action:", kind, alias, keepAlias), options, 0)
	if err != nil {
		return "keep"
	}
	return value
}

func promptAppLinkAction(platformKey, expectedPlatform string, missing bool) string {
	msg := fmt.Sprintf("App link for '%s' needs attention.", platformKey)
	if missing {
		msg = fmt.Sprintf("App link for '%s' points to a missing app.", platformKey)
	}
	if expectedPlatform != "" {
		msg += fmt.Sprintf(" Expected platform: %s.", expectedPlatform)
	}

	options := []ui.SelectOption{
		{Label: "Keep as-is", Value: "keep", Description: "Leave app_id unchanged."},
		{Label: "Clear app_id", Value: "clear", Description: "Unset this platform app link."},
		{Label: "Relink app", Value: "relink", Description: "Select another app ID for this platform."},
	}
	_, value, err := ui.Select(msg, options, 0)
	if err != nil {
		return "keep"
	}
	return value
}

func promptRelinkApp(ctx context.Context, client *api.Client, platform string) (string, string, error) {
	apps, err := listAppsForPlatform(ctx, client, platform)
	if err != nil {
		return "", "", err
	}
	if len(apps) == 0 {
		if platform == "" {
			return "", "", fmt.Errorf("no apps available for relink")
		}
		return "", "", fmt.Errorf("no %s apps available for relink", platform)
	}

	options := make([]ui.SelectOption, 0, len(apps))
	for _, app := range apps {
		options = append(options, ui.SelectOption{
			Label:       fmt.Sprintf("%s (%s)", app.Name, app.Platform),
			Value:       app.ID,
			Description: app.ID,
		})
	}

	idx, value, err := ui.Select("Select app to relink:", options, 0)
	if err != nil {
		return "", "", err
	}
	return value, apps[idx].Name, nil
}

func listAppsForPlatform(ctx context.Context, client *api.Client, platform string) ([]api.App, error) {
	page := 1
	pageSize := 100
	items := make([]api.App, 0)

	for {
		resp, err := client.ListApps(ctx, platform, page, pageSize)
		if err != nil {
			return nil, err
		}
		items = append(items, resp.Items...)
		if !resp.HasNext {
			break
		}
		page++
	}

	sort.Slice(items, func(i, j int) bool {
		if strings.EqualFold(items[i].Name, items[j].Name) {
			return items[i].ID < items[j].ID
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})

	return items, nil
}

// runBootstrap verifies local YAML _meta.remote_id links. Local YAML files
// in .revyl/tests/ are the sole source of truth; this reports their state.
//
// Parameters:
//   - cfg: The project config (unused, retained for caller compatibility)
//   - configPath: Path to the config file (unused)
//   - testsDir: Path to .revyl/tests/ directory
//
// Returns:
//   - error: Any error that occurred
func runBootstrap(_ *config.ProjectConfig, _, testsDir string) error {
	localTests, err := config.LoadLocalTests(testsDir)
	if err != nil {
		return fmt.Errorf("failed to load local tests: %w", err)
	}

	if len(localTests) == 0 {
		ui.PrintWarning("No local test YAML files found in %s", testsDir)
		return nil
	}

	linked := 0
	skipped := 0
	for alias, test := range localTests {
		if test.Meta.RemoteID == "" {
			skipped++
			continue
		}
		ui.PrintSuccess("  %s -> %s", alias, test.Meta.RemoteID)
		linked++
	}

	if linked == 0 && skipped == 0 {
		ui.PrintInfo("No local test files found.")
		return nil
	}

	if linked > 0 {
		ui.PrintSuccess("Found %d test(s) with remote links in local YAML metadata.", linked)
	}
	if skipped > 0 {
		ui.PrintWarning("%d local test(s) have no remote_id (local-only).", skipped)
	}

	return nil
}
