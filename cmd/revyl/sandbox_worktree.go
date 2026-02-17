// Package main provides worktree management commands for Fleet sandboxes.
//
// Worktree commands execute via SSH to the sandbox, using the shell helpers
// (wt, wts, wtrm) that are pre-installed during sandbox provisioning.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	sandboxpkg "github.com/revyl/cli/internal/sandbox"
	"github.com/revyl/cli/internal/ui"
)

// sandboxWorktreeCmd is the parent command for worktree operations.
var sandboxWorktreeCmd = &cobra.Command{
	Use:   "worktree",
	Short: "Manage git worktrees on your sandbox",
	Long: `Manage git worktrees on your claimed sandbox.

Worktrees allow multiple branches to be checked out simultaneously,
each in its own directory with isolated environment and device slots.

COMMANDS:
  list   - List worktrees on your sandbox
  create - Create a new worktree from a branch
  remove - Remove a worktree
  setup  - Re-run setup on an existing worktree

EXAMPLES:
  revyl --dev sandbox worktree list
  revyl --dev sandbox worktree create feature-x
  revyl --dev sandbox worktree remove feature-x`,
}

// sandboxWorktreeListCmd lists worktrees on the user's sandbox.
var sandboxWorktreeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List worktrees on your sandbox",
	Long: `List all git worktrees on your claimed sandbox.

Shows branch name, path, and whether it's the main worktree.

EXAMPLES:
  revyl --dev sandbox worktree list
  revyl --dev sandbox worktree list --json`,
	RunE: runSandboxWorktreeList,
}

// runSandboxWorktreeList lists worktrees by executing 'git worktree list' via SSH.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (unused)
//
// Returns:
//   - error: Any error that occurred
func runSandboxWorktreeList(cmd *cobra.Command, args []string) error {
	target, err := getClaimedSandbox(cmd)
	if err != nil {
		return err
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	if !jsonOutput {
		ui.StartSpinner("Listing worktrees...")
	}

	output, err := sandboxpkg.SSHExec(target, "cd ~/workspace/main && git worktree list --porcelain")

	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to list worktrees: %v", err)
		return err
	}

	worktrees := parseWorktreeList(output, target)

	if jsonOutput {
		data, _ := json.MarshalIndent(map[string]interface{}{
			"worktrees": worktrees,
			"count":     len(worktrees),
		}, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(worktrees) == 0 {
		ui.PrintInfo("No worktrees found on %s", target.DisplayName())
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Create one", Command: "revyl --dev sandbox worktree create <branch>"},
		})
		return nil
	}

	ui.Println()
	ui.PrintInfo("Worktrees on %s (%d):", target.DisplayName(), len(worktrees))
	ui.Println()

	table := ui.NewTable("BRANCH", "PATH", "TYPE")
	table.SetMinWidth(0, 20)
	table.SetMinWidth(1, 30)

	for _, wt := range worktrees {
		wtType := "worktree"
		if wt.IsMain {
			wtType = "main"
		}
		table.AddRow(wt.Branch, wt.Path, wtType)
	}

	table.Render()
	return nil
}

var worktreeCreateBase string

// sandboxWorktreeCreateCmd creates a new worktree on the user's sandbox.
var sandboxWorktreeCreateCmd = &cobra.Command{
	Use:   "create <branch>",
	Short: "Create a new worktree on your sandbox",
	Long: `Create a new git worktree on your claimed sandbox.

Creates a new branch (or uses an existing one) and sets up the worktree
with .env files, dependencies, and device slot allocation.

EXAMPLES:
  revyl --dev sandbox worktree create feature-x
  revyl --dev sandbox worktree create feature-x --base staging`,
	Args: cobra.ExactArgs(1),
	RunE: runSandboxWorktreeCreate,
}

// runSandboxWorktreeCreate creates a worktree via SSH using the 'wt' shell helper.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (branch name)
//
// Returns:
//   - error: Any error that occurred
func runSandboxWorktreeCreate(cmd *cobra.Command, args []string) error {
	branch := args[0]

	target, err := getClaimedSandbox(cmd)
	if err != nil {
		return err
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	// Build the command — 'wt' is the shell helper installed on sandboxes
	sshCmd := fmt.Sprintf("source ~/.zshrc && wt %s", branch)
	if worktreeCreateBase != "" {
		sshCmd = fmt.Sprintf("source ~/.zshrc && wt %s %s", branch, worktreeCreateBase)
	}

	if !jsonOutput {
		ui.StartSpinner(fmt.Sprintf("Creating worktree %s...", branch))
	}

	output, err := sandboxpkg.SSHExec(target, sshCmd)

	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to create worktree: %v", err)
		return err
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(map[string]interface{}{
			"success":      true,
			"branch":       branch,
			"sandbox_name": target.DisplayName(),
			"output":       output,
		}, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.PrintSuccess("Created worktree: %s", branch)
	if output != "" {
		ui.PrintDim("%s", output)
	}

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Open in IDE", Command: fmt.Sprintf("revyl --dev sandbox open %s", branch)},
		{Label: "Start a tunnel", Command: fmt.Sprintf("revyl --dev sandbox tunnel start %s", branch)},
	})

	return nil
}

// sandboxWorktreeRemoveCmd removes a worktree from the user's sandbox.
var sandboxWorktreeRemoveCmd = &cobra.Command{
	Use:   "remove <branch>",
	Short: "Remove a worktree from your sandbox",
	Long: `Remove a git worktree from your claimed sandbox.

Stops any associated tunnels and removes the worktree directory.

EXAMPLES:
  revyl --dev sandbox worktree remove feature-x
  revyl --dev sandbox worktree remove feature-x --force`,
	Args: cobra.ExactArgs(1),
	RunE: runSandboxWorktreeRemove,
}

var worktreeRemoveForce bool

// runSandboxWorktreeRemove removes a worktree via SSH using the 'wtrm' shell helper.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (branch name)
//
// Returns:
//   - error: Any error that occurred
func runSandboxWorktreeRemove(cmd *cobra.Command, args []string) error {
	branch := args[0]

	target, err := getClaimedSandbox(cmd)
	if err != nil {
		return err
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	// Confirm unless --force
	if !worktreeRemoveForce && !jsonOutput {
		confirmed, err := ui.PromptConfirm(fmt.Sprintf("Remove worktree '%s'?", branch), false)
		if err != nil || !confirmed {
			ui.PrintInfo("Cancelled")
			return nil
		}
	}

	sshCmd := fmt.Sprintf("source ~/.zshrc && wtrm %s", branch)

	if !jsonOutput {
		ui.StartSpinner(fmt.Sprintf("Removing worktree %s...", branch))
	}

	output, err := sandboxpkg.SSHExec(target, sshCmd)

	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to remove worktree: %v", err)
		return err
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(map[string]interface{}{
			"success":      true,
			"branch":       branch,
			"sandbox_name": target.DisplayName(),
		}, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.PrintSuccess("Removed worktree: %s", branch)
	if output != "" {
		ui.PrintDim("%s", output)
	}

	return nil
}

// sandboxWorktreeSetupCmd re-runs setup on an existing worktree.
var sandboxWorktreeSetupCmd = &cobra.Command{
	Use:   "setup <branch>",
	Short: "Re-run setup on an existing worktree",
	Long: `Re-run the setup script on an existing worktree via SSH.

Reads fleet.json from the worktree to find the setup script path
(e.g. "./scripts/worktree-setup.sh") and executes it.

Useful after pulling new changes that modify setup requirements.

EXAMPLES:
  revyl --dev sandbox worktree setup feature-x`,
	Args: cobra.ExactArgs(1),
	RunE: runSandboxWorktreeSetup,
}

// runSandboxWorktreeSetup re-runs the setup script on an existing worktree via SSH.
//
// Checks .revyl/fleet.json first, then falls back to fleet.json in the repo root
// to discover the setup script path (e.g. "./scripts/worktree-setup.sh") and
// executes it. Falls back to a warning if no config or setup script is found.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (branch name)
//
// Returns:
//   - error: Any error that occurred
func runSandboxWorktreeSetup(cmd *cobra.Command, args []string) error {
	branch := args[0]

	target, err := getClaimedSandbox(cmd)
	if err != nil {
		return err
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	sshCmd := fmt.Sprintf(
		`cd ~/workspace/%s && source ~/.zshrc && `+
			`if [ -f .revyl/fleet.json ]; then CFG=.revyl/fleet.json; `+
			`elif [ -f fleet.json ]; then CFG=fleet.json; `+
			`else CFG=""; fi; `+
			`if [ -n "$CFG" ]; then `+
			`SCRIPT=$(python3 -c "import json; c=json.load(open('$CFG')); print(c.get('scripts',{}).get('setup',''))" 2>/dev/null); `+
			`if [ -n "$SCRIPT" ] && [ -f "$SCRIPT" ]; then chmod +x "$SCRIPT" && "$SCRIPT"; `+
			`else echo "No setup script found in $CFG"; fi; `+
			`else echo "No fleet config found (.revyl/fleet.json or fleet.json)"; fi`,
		branch,
	)

	if !jsonOutput {
		ui.StartSpinner(fmt.Sprintf("Running setup on %s...", branch))
	}

	output, err := sandboxpkg.SSHExec(target, sshCmd)

	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to run setup: %v", err)
		return err
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(map[string]interface{}{
			"success":      true,
			"branch":       branch,
			"sandbox_name": target.DisplayName(),
			"output":       output,
		}, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.PrintSuccess("Setup complete for worktree: %s", branch)
	if output != "" {
		ui.PrintDim("%s", output)
	}

	return nil
}

// --- helpers ---

// getClaimedSandbox returns the user's claimed sandbox, prompting to choose if multiple.
// This is a shared helper for worktree, tunnel, and open commands.
//
// Parameters:
//   - cmd: The cobra command (for --dev flag and context)
//
// Returns:
//   - *api.FleetSandbox: The user's claimed sandbox
//   - error: If no sandbox is claimed or API call fails
func getClaimedSandbox(cmd *cobra.Command) (*api.FleetSandbox, error) {
	apiKey, err := getAPIKey()
	if err != nil {
		return nil, err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	mine, err := client.GetMySandboxes(ctx)
	if err != nil {
		ui.PrintError("Failed to fetch your sandboxes: %v", err)
		return nil, err
	}

	if len(mine) == 0 {
		ui.PrintInfo("You don't have any claimed sandboxes")
		ui.PrintInfo("Run 'revyl --dev sandbox claim' to claim one")
		return nil, fmt.Errorf("no claimed sandbox")
	}

	if len(mine) == 1 {
		return &mine[0], nil
	}

	// Multiple sandboxes — prompt user
	options := make([]ui.SelectOption, len(mine))
	for i, s := range mine {
		options[i] = ui.SelectOption{
			Value:       s.Id,
			Label:       s.DisplayName(),
			Description: s.EffectiveTunnelHostname(),
		}
	}

	idx, _, err := ui.Select("Which sandbox?", options, 0)
	if err != nil {
		return nil, fmt.Errorf("selection cancelled")
	}

	return &mine[idx], nil
}

// parseWorktreeList parses 'git worktree list --porcelain' output into FleetWorktree structs.
//
// Porcelain format:
//
//	worktree /path/to/worktree
//	HEAD abc123
//	branch refs/heads/branch-name
//	<blank line>
//
// Parameters:
//   - output: The raw porcelain output from git worktree list
//   - sandbox: The sandbox the worktrees are on
//
// Returns:
//   - []worktreeInfo: Parsed worktree entries
type worktreeInfo struct {
	Branch string `json:"branch"`
	Path   string `json:"path"`
	IsMain bool   `json:"is_main"`
}

func parseWorktreeList(output string, sb *api.FleetSandbox) []worktreeInfo {
	var worktrees []worktreeInfo
	var current worktreeInfo

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(line, "worktree "):
			if current.Path != "" {
				worktrees = append(worktrees, current)
			}
			current = worktreeInfo{Path: strings.TrimPrefix(line, "worktree ")}

		case strings.HasPrefix(line, "branch refs/heads/"):
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
			// The first worktree (bare repo or main) is typically "main" or "staging"
			if current.Branch == "main" || current.Branch == "staging" {
				current.IsMain = true
			}

		case line == "detached":
			// Detached HEAD -- branch will be extracted from path below

		case line == "bare":
			// Bare repository entry -- branch will be extracted from path below

		case line == "":
			if current.Path != "" {
				worktrees = append(worktrees, current)
				current = worktreeInfo{}
			}
		}
	}

	// Don't forget the last entry
	if current.Path != "" {
		worktrees = append(worktrees, current)
	}

	// Fallback: extract branch from path (last component) when branch is empty.
	// This handles detached HEAD, bare repo entries, or any other format that
	// doesn't include "branch refs/heads/...".
	for i := range worktrees {
		if worktrees[i].Branch == "" && worktrees[i].Path != "" {
			parts := strings.Split(worktrees[i].Path, "/")
			worktrees[i].Branch = parts[len(parts)-1]
			if worktrees[i].Branch == "main" || worktrees[i].Branch == "staging" {
				worktrees[i].IsMain = true
			}
		}
	}

	return worktrees
}

// --- init ---

func init() {
	// Register worktree subcommands
	sandboxCmd.AddCommand(sandboxWorktreeCmd)

	sandboxWorktreeCmd.AddCommand(sandboxWorktreeListCmd)
	sandboxWorktreeCmd.AddCommand(sandboxWorktreeCreateCmd)
	sandboxWorktreeCmd.AddCommand(sandboxWorktreeRemoveCmd)
	sandboxWorktreeCmd.AddCommand(sandboxWorktreeSetupCmd)

	// worktree create flags
	sandboxWorktreeCreateCmd.Flags().StringVar(&worktreeCreateBase, "base", "", "Base branch to create from (default: staging)")

	// worktree remove flags
	sandboxWorktreeRemoveCmd.Flags().BoolVarP(&worktreeRemoveForce, "force", "f", false, "Skip confirmation prompt")
}
