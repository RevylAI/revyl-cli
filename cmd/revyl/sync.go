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
	Short: "Sync tests, workflows, and app links with upstream state",
	Long: `Synchronize local project state against your Revyl organization.

By default, sync reconciles tests, workflows, and app link mappings in
.revyl/config.yaml. Prompts are shown only when a decision is required
(conflicts, stale/deleted mappings, duplicates).

EXAMPLES:
  revyl sync                        # Sync tests + workflows + app links
  revyl sync --tests                # Sync tests only
  revyl sync --workflows --apps     # Sync workflows and app links
  revyl sync --non-interactive      # No prompts; deterministic defaults
  revyl sync --prune                # Auto-prune stale mappings
  revyl sync --dry-run --json       # Preview actions as JSON`,
	RunE: runSync,
}

func init() {
	registerSyncFlags(syncCmd)
}

type syncOptions struct {
	Prompt bool
	Prune  bool
	DryRun bool
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
	workflows        bool
	apps             bool
	nonInteractive   bool
	interactive      bool
	prune            bool
	dryRun           bool
	skipHotReloadChk bool
}

func registerSyncFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("tests", false, "Sync tests")
	cmd.Flags().Bool("workflows", false, "Sync workflows")
	cmd.Flags().Bool("apps", false, "Sync build platform app_id links")
	cmd.Flags().Bool("non-interactive", false, "Disable prompts and apply deterministic defaults")
	cmd.Flags().Bool("interactive", false, "Force interactive prompts (requires TTY stdin)")
	cmd.Flags().Bool("prune", false, "Auto-prune stale/deleted mappings")
	cmd.Flags().Bool("dry-run", false, "Show planned actions without writing files")
	cmd.Flags().Bool("skip-hotreload-check", false, "Skip validating hotreload platform key mappings")
}

func readSyncFlags(cmd *cobra.Command) (syncFlagValues, error) {
	tests, err := cmd.Flags().GetBool("tests")
	if err != nil {
		return syncFlagValues{}, err
	}
	workflows, err := cmd.Flags().GetBool("workflows")
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
	skipHotReloadChk, err := cmd.Flags().GetBool("skip-hotreload-check")
	if err != nil {
		return syncFlagValues{}, err
	}

	return syncFlagValues{
		tests:            tests,
		workflows:        workflows,
		apps:             apps,
		nonInteractive:   nonInteractive,
		interactive:      interactive,
		prune:            prune,
		dryRun:           dryRun,
		skipHotReloadChk: skipHotReloadChk,
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
	runWorkflows := flags.workflows
	runApps := flags.apps
	if !runTests && !runWorkflows && !runApps {
		runTests = true
		runWorkflows = true
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

	opts := syncOptions{
		Prompt: interactiveWanted,
		Prune:  flags.prune,
		DryRun: flags.dryRun,
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

	if runWorkflows {
		items, domainChanged, domainErr := syncWorkflowsDomain(ctx, client, cfg, opts)
		out.Workflows = items
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

	ui.Println()
	ui.PrintInfo("Summary")
	keys := make([]string, 0, len(out.Summary))
	for k := range out.Summary {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ui.PrintDim("  %s: %d", k, out.Summary[k])
	}
}

func printSyncSection(title string, items []syncItem) {
	if len(items) == 0 {
		return
	}

	ui.Println()
	ui.PrintInfo("%s", title)
	for _, it := range items {
		status := it.Status
		if status == "" {
			status = "unknown"
		}
		action := it.Action
		if action == "" {
			action = "none"
		}
		line := fmt.Sprintf("  - %s [%s/%s]", it.Name, status, action)
		if it.ID != "" {
			line += " " + truncatePrefix(it.ID, 8)
		}
		if it.Message != "" {
			line += " — " + it.Message
		}
		if it.Error != "" {
			ui.PrintError("%s | %s", line, it.Error)
		} else if status == "warning" || status == "stale" || status == "conflict" {
			ui.PrintWarning("%s", line)
		} else if status == "synced" || action == "pull" || action == "push" || action == "import" || action == "relink" || action == "detach" || action == "prune-all" || action == "prune-alias" {
			ui.PrintSuccess("%s", line)
		} else {
			ui.PrintDim("%s", line)
		}
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
	for _, id := range cfg.Tests {
		existingIDs[id] = true
	}
	for _, lt := range localTests {
		if lt != nil && lt.Meta.RemoteID != "" {
			existingIDs[lt.Meta.RemoteID] = true
		}
	}

	for _, rt := range remoteTests {
		if existingIDs[rt.ID] {
			continue
		}
		alias := util.SanitizeForFilename(rt.Name)
		if alias == "" {
			alias = fmt.Sprintf("test-%s", truncatePrefix(rt.ID, 8))
		}
		alias = ensureUniqueAlias(alias, cfg.Tests)

		action := "import"
		if opts.DryRun {
			action = "would-import"
		} else {
			cfg.Tests[alias] = rt.ID
			existingIDs[rt.ID] = true
			changed = true
		}
		items = append(items, syncItem{
			Name:    alias,
			ID:      rt.ID,
			Status:  "remote-only",
			Action:  action,
			Message: "discovered from organization and added to config",
		})
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

		if cfgID, ok := cfg.Tests[st.Name]; ok && cfgID != "" {
			if _, exists := remoteByID[cfgID]; !exists {
				item.Status = "stale"
				localTest, localPath, hasLocalFile := resolveLocalTestForAlias(localTests, testsDir, st.Name)
				hasLocalChanges := false
				if hasLocalFile {
					if localTest == nil {
						loaded, lErr := config.LoadLocalTest(localPath)
						if lErr != nil {
							// Be conservative: if we cannot inspect the file reliably, don't auto-delete it.
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
						delete(cfg.Tests, st.Name)
						changed = true
					case "prune-all":
						delete(cfg.Tests, st.Name)
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

	dups := duplicateAliasesByID(cfg.Tests)
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
				delete(cfg.Tests, alias)
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

func syncWorkflowsDomain(ctx context.Context, client *api.Client, cfg *config.ProjectConfig, opts syncOptions) ([]syncItem, bool, error) {
	items := make([]syncItem, 0)
	changed := false
	hadErr := false

	remoteWf, err := client.ListAllWorkflows(ctx, 200)
	if err != nil {
		items = append(items, syncItem{Name: "workflows", Status: "error", Action: "list", Error: err.Error()})
		return items, changed, err
	}

	remoteByID := make(map[string]api.SimpleWorkflow, len(remoteWf))
	for _, w := range remoteWf {
		remoteByID[w.ID] = w
	}

	sort.Slice(remoteWf, func(i, j int) bool {
		if strings.EqualFold(remoteWf[i].Name, remoteWf[j].Name) {
			return remoteWf[i].ID < remoteWf[j].ID
		}
		return strings.ToLower(remoteWf[i].Name) < strings.ToLower(remoteWf[j].Name)
	})

	existingIDs := make(map[string]bool)
	for _, id := range cfg.Workflows {
		existingIDs[id] = true
	}

	for _, w := range remoteWf {
		if existingIDs[w.ID] {
			continue
		}
		alias := util.SanitizeForFilename(w.Name)
		if alias == "" {
			alias = fmt.Sprintf("workflow-%s", truncatePrefix(w.ID, 8))
		}
		alias = ensureUniqueAlias(alias, cfg.Workflows)

		action := "import"
		if opts.DryRun {
			action = "would-import"
		} else {
			cfg.Workflows[alias] = w.ID
			existingIDs[w.ID] = true
			changed = true
		}

		items = append(items, syncItem{
			Name:    alias,
			ID:      w.ID,
			Status:  "remote-only",
			Action:  action,
			Message: "discovered from organization and added to config",
		})
	}

	aliases := sortedKeys(cfg.Workflows)
	for _, alias := range aliases {
		id := cfg.Workflows[alias]
		if _, ok := remoteByID[id]; ok {
			continue
		}

		item := syncItem{Name: alias, ID: id, Status: "stale"}
		action := "keep"
		if opts.Prune {
			action = "prune"
		} else if opts.Prompt {
			action = promptStaleWorkflowAction(alias)
			item.Prompted = true
		}
		item.Action = action
		if opts.DryRun && action == "prune" {
			item.Action = "would-prune"
		} else if !opts.DryRun && action == "prune" {
			delete(cfg.Workflows, alias)
			changed = true
		}
		items = append(items, item)
	}

	dups := duplicateAliasesByID(cfg.Workflows)
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
				action = "prune"
			} else if opts.Prompt {
				action = promptDuplicateAliasAction("workflow", alias, keep)
				item.Prompted = true
			}
			item.Action = action
			if opts.DryRun && action == "prune" {
				item.Action = "would-prune"
			} else if !opts.DryRun && action == "prune" {
				delete(cfg.Workflows, alias)
				changed = true
			}
			items = append(items, item)
		}
	}

	if hadErr {
		return items, changed, fmt.Errorf("one or more workflow sync actions failed")
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

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedBuildPlatforms(cfg *config.ProjectConfig) []string {
	keys := make([]string, 0, len(cfg.Build.Platforms))
	for k := range cfg.Build.Platforms {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func ensureUniqueAlias(base string, existing map[string]string) string {
	if existing[base] == "" {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if existing[candidate] == "" {
			return candidate
		}
	}
}

func duplicateAliasesByID(m map[string]string) map[string][]string {
	byID := make(map[string][]string)
	for alias, id := range m {
		if id == "" {
			continue
		}
		byID[id] = append(byID[id], alias)
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

func detachTestLink(cfg *config.ProjectConfig, alias string, localTest *config.LocalTest, localPath string) (bool, error) {
	changed := false

	if _, ok := cfg.Tests[alias]; ok {
		delete(cfg.Tests, alias)
		changed = true
	}

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
		{Label: "Keep mapping", Value: "keep", Description: "Leave alias and local file unchanged."},
		{Label: "Prune alias", Value: "prune-alias", Description: "Remove alias from .revyl/config.yaml."},
	}
	if hasLocalFile {
		options = append(options, ui.SelectOption{Label: "Prune alias + file", Value: "prune-all", Description: "Remove alias and local test YAML."})
	}
	_, value, err := ui.Select(fmt.Sprintf("Test '%s' is missing upstream. Choose action:", name), options, 0)
	if err != nil {
		return "keep"
	}
	return value
}

func promptOrphanedTestAction(name string, issue syncpkg.RemoteLinkIssue, hasLocalFile bool) string {
	issueLabel := "stale"
	switch issue {
	case syncpkg.RemoteLinkIssueMissing:
		issueLabel = "missing upstream"
	case syncpkg.RemoteLinkIssueInvalidID:
		issueLabel = "invalid remote id"
	case syncpkg.RemoteLinkIssueUnauthorized:
		issueLabel = "unauthorized"
	case syncpkg.RemoteLinkIssueForbidden:
		issueLabel = "access denied"
	}

	options := []ui.SelectOption{
		{Label: "Keep mapping", Value: "keep", Description: "Leave alias and local file unchanged."},
		{Label: "Detach mapping", Value: "detach", Description: "Remove remote link and keep local test file."},
	}
	if hasLocalFile {
		options = append(options, ui.SelectOption{Label: "Prune alias + file", Value: "prune-all", Description: "Remove alias and local test YAML."})
	}

	_, value, err := ui.Select(fmt.Sprintf("Test '%s' link is %s. Choose action:", name, issueLabel), options, 1)
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

func promptStaleWorkflowAction(name string) string {
	options := []ui.SelectOption{
		{Label: "Keep mapping", Value: "keep", Description: "Leave workflow alias unchanged."},
		{Label: "Prune alias", Value: "prune", Description: "Remove alias from .revyl/config.yaml."},
	}
	_, value, err := ui.Select(fmt.Sprintf("Workflow '%s' is missing upstream. Choose action:", name), options, 0)
	if err != nil {
		return "keep"
	}
	return value
}

func promptDuplicateAliasAction(kind, alias, keepAlias string) string {
	options := []ui.SelectOption{
		{Label: "Keep duplicate", Value: "keep", Description: "Retain both aliases."},
		{Label: "Prune duplicate", Value: "prune", Description: "Remove duplicate alias mapping."},
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
