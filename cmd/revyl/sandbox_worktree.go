// Package main provides worktree management commands for Fleet sandboxes.
//
// Worktree commands execute via SSH to the sandbox and run git worktree
// operations directly for reliable behavior across sandbox layouts.
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
	target, err := getClaimedSandboxNamed(cmd, worktreeSandboxName)
	if err != nil {
		return err
	}
	repoName := strings.TrimSpace(worktreeListRepo)

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	if !jsonOutput {
		ui.StartSpinner("Listing worktrees...")
	}

	worktrees, err := listSandboxWorktrees(target, repoName)

	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to list worktrees: %v", err)
		return err
	}

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

var (
	worktreeCreateBase  string
	worktreeCreateRepo  string
	worktreeListRepo    string
	worktreeSetupRepo   string
	worktreeRemoveRepo  string
	worktreeSandboxName string

	sandboxSSHExec         = sandboxpkg.SSHExec
	listSandboxWorktreesFn = listSandboxWorktrees
	getClaimedSandboxesFn  = defaultGetClaimedSandboxes
)

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

// runSandboxWorktreeCreate creates a worktree via SSH using a self-contained git script.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (branch name)
//
// Returns:
//   - error: Any error that occurred
func runSandboxWorktreeCreate(cmd *cobra.Command, args []string) error {
	branch := args[0]
	base := worktreeCreateBase
	if strings.TrimSpace(base) == "" {
		base = "staging"
	}
	repoName := strings.TrimSpace(worktreeCreateRepo)

	target, err := getClaimedSandboxNamed(cmd, worktreeSandboxName)
	if err != nil {
		return err
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	// Use a self-contained script so create remains reliable even when shell
	// helper aliases/functions are missing or stale on the sandbox.
	sshCmd := fmt.Sprintf(`
set -euo pipefail
BRANCH=%s
BASE=%s
REPO_OVERRIDE=%s

%s

if [ -n "$REPO_OVERRIDE" ]; then
  REPO_DIR="$REPO_OVERRIDE"
else
  REPO_DIR="$(basename "$REPO_PATH")"
fi

if [ -z "${REPO_DIR:-}" ]; then
  echo "ERROR: Unable to resolve repo directory for worktree path" >&2
  exit 1
fi

WORKTREE_BASE="$WORKSPACE_ROOT/$REPO_DIR"
mkdir -p "$WORKTREE_BASE"
WORKTREE_PATH="$WORKTREE_BASE/$BRANCH"

echo "Repo path: $REPO_PATH"
echo "Worktree path: $WORKTREE_PATH"
echo "Base branch: $BASE"

git -C "$REPO_PATH" fetch origin "$BASE" 2>&1 || git -C "$REPO_PATH" fetch origin 2>&1
git -C "$REPO_PATH" worktree prune 2>&1 || true

if [ -e "$WORKTREE_PATH/.git" ]; then
  CURRENT_BRANCH="$(git -C "$WORKTREE_PATH" rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
  if [ "$CURRENT_BRANCH" = "$BRANCH" ]; then
    echo "Worktree already exists at $WORKTREE_PATH on branch $BRANCH"
    echo "CREATED:$WORKTREE_PATH"
    exit 0
  fi
  echo "ERROR: Worktree path already exists on branch '$CURRENT_BRANCH': $WORKTREE_PATH" >&2
  exit 1
fi

add_worktree() {
  if git -C "$REPO_PATH" show-ref --verify --quiet "refs/heads/$BRANCH"; then
    git -C "$REPO_PATH" worktree add "$WORKTREE_PATH" "$BRANCH" 2>&1
    return
  fi

  START_POINT="origin/$BASE"
  if ! git -C "$REPO_PATH" show-ref --verify --quiet "refs/remotes/$START_POINT"; then
    if git -C "$REPO_PATH" show-ref --verify --quiet "refs/heads/$BASE"; then
      START_POINT="$BASE"
    else
      echo "ERROR: Base branch '$BASE' not found locally or on origin" >&2
      return 1
    fi
  fi
  git -C "$REPO_PATH" worktree add "$WORKTREE_PATH" -b "$BRANCH" "$START_POINT" 2>&1
}

if ! ADD_OUTPUT="$(add_worktree)"; then
  echo "$ADD_OUTPUT"
  if printf '%%s\n' "$ADD_OUTPUT" | grep -q "missing but already registered worktree"; then
    echo "Detected stale worktree registration for $WORKTREE_PATH; pruning and retrying..." >&2
    git -C "$REPO_PATH" worktree prune 2>&1 || true
    if ! RETRY_OUTPUT="$(add_worktree)"; then
      echo "$RETRY_OUTPUT"
      echo "ERROR: Worktree create failed after prune/retry" >&2
      exit 1
    fi
    echo "$RETRY_OUTPUT"
  else
    exit 1
  fi
fi

echo "CREATED:$WORKTREE_PATH"
`, shellQuote(branch), shellQuote(base), shellQuote(repoName), sandboxRepoResolutionScript())

	if !jsonOutput {
		ui.StartSpinner(fmt.Sprintf("Creating worktree %s...", branch))
	}

	output, err := sandboxSSHExec(target, sshCmd)

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

	openCommand := fmt.Sprintf("revyl --dev sandbox open %s --repo <repo>", branch)
	if strings.TrimSpace(repoName) != "" {
		openCommand = fmt.Sprintf("revyl --dev sandbox open %s --repo %s", branch, repoName)
	}
	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Open in IDE", Command: openCommand},
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
  revyl --dev sandbox worktree remove feature-x --repo my-repo
  revyl --dev sandbox worktree remove feature-x --repo my-repo --force`,
	Args: cobra.ExactArgs(1),
	RunE: runSandboxWorktreeRemove,
}

var worktreeRemoveForce bool

// runSandboxWorktreeRemove removes a worktree via SSH using repo-scoped git commands.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (branch name)
//
// Returns:
//   - error: Any error that occurred
func runSandboxWorktreeRemove(cmd *cobra.Command, args []string) error {
	branch := args[0]
	repoName := strings.TrimSpace(worktreeRemoveRepo)
	if repoName == "" {
		ui.PrintError("--repo is required for sandbox worktree remove")
		return fmt.Errorf("--repo is required")
	}

	target, err := getClaimedSandboxNamed(cmd, worktreeSandboxName)
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

	sshCmd := fmt.Sprintf(`
set -euo pipefail
BRANCH=%s
REPO_OVERRIDE=%s

%s

WORKTREE_PATH="$WORKSPACE_ROOT/$REPO_OVERRIDE/$BRANCH"
if [ ! -e "$WORKTREE_PATH" ]; then
  FOUND_PATH="$(git -C "$REPO_PATH" worktree list --porcelain | awk -v b="refs/heads/$BRANCH" '
    /^worktree / { wt=$2 }
    /^branch / { if ($2 == b) { print wt; exit } }
  ')"
  if [ -n "$FOUND_PATH" ]; then
    WORKTREE_PATH="$FOUND_PATH"
  fi
fi

if [ ! -e "$WORKTREE_PATH" ]; then
  echo "ERROR: Worktree path not found for branch '$BRANCH' in repo '$REPO_OVERRIDE'" >&2
  exit 1
fi

git -C "$REPO_PATH" worktree remove "$WORKTREE_PATH" --force 2>&1 || {
  echo "Detected stale worktree registration; pruning and retrying..." >&2
  git -C "$REPO_PATH" worktree prune 2>&1 || true
  git -C "$REPO_PATH" worktree remove "$WORKTREE_PATH" --force 2>&1
}
git -C "$REPO_PATH" branch -D "$BRANCH" 2>/dev/null || true
git -C "$REPO_PATH" worktree prune 2>&1 || true
echo "REMOVED:$WORKTREE_PATH"
`, shellQuote(branch), shellQuote(repoName), sandboxRepoResolutionScript())

	if !jsonOutput {
		ui.StartSpinner(fmt.Sprintf("Removing worktree %s...", branch))
	}

	output, err := sandboxSSHExec(target, sshCmd)

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
  revyl --dev sandbox worktree setup feature-x --repo my-repo`,
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
	repoName := strings.TrimSpace(worktreeSetupRepo)
	if repoName == "" {
		ui.PrintError("--repo is required for sandbox worktree setup")
		return fmt.Errorf("--repo is required")
	}

	target, err := getClaimedSandboxNamed(cmd, worktreeSandboxName)
	if err != nil {
		return err
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")
	worktreePath, err := resolveSandboxWorktreePathInRepo(target, branch, repoName)
	if err != nil {
		ui.PrintError("Failed to resolve worktree path for %s: %v", branch, err)
		return err
	}

	sshCmd := fmt.Sprintf(
		`set -euo pipefail && cd %s && `+
			`if [ -f .revyl/fleet.json ]; then CFG=.revyl/fleet.json; `+
			`elif [ -f fleet.json ]; then CFG=fleet.json; `+
			`else CFG=""; fi; `+
			`if [ -n "$CFG" ]; then `+
			`SCRIPT=$(python3 -c "import json; c=json.load(open('$CFG')); print(c.get('scripts',{}).get('setup',''))" 2>/dev/null); `+
			`if [ -n "$SCRIPT" ] && [ -f "$SCRIPT" ]; then chmod +x "$SCRIPT" && "$SCRIPT"; `+
			`else echo "No setup script found in $CFG"; fi; `+
			`else echo "No fleet config found (.revyl/fleet.json or fleet.json)"; fi`,
		shellQuote(worktreePath),
	)

	if !jsonOutput {
		ui.StartSpinner(fmt.Sprintf("Running setup on %s...", branch))
	}

	output, err := sandboxSSHExec(target, sshCmd)

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

// shellQuote wraps a string for safe use in POSIX shell commands.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// sandboxRepoResolutionScript returns shell code that resolves REPO_PATH and WORKSPACE_ROOT.
func sandboxRepoResolutionScript() string {
	return `
WORKSPACE_ROOT="${SANDBOX_WORKSPACE:-$HOME/workspace}"
REPO_OVERRIDE="${REPO_OVERRIDE:-}"
BASE_BRANCH="${BASE:-staging}"

is_git_repo() {
  local candidate="${1:-}"
  [ -n "$candidate" ] && git -C "$candidate" rev-parse --git-dir >/dev/null 2>&1
}

if [ -n "$REPO_OVERRIDE" ]; then
  REPO_ROOT="$WORKSPACE_ROOT/$REPO_OVERRIDE"
  REPO_PATH=""
  if is_git_repo "$REPO_ROOT/$BASE_BRANCH"; then
    REPO_PATH="$REPO_ROOT/$BASE_BRANCH"
  elif is_git_repo "$REPO_ROOT/staging"; then
    REPO_PATH="$REPO_ROOT/staging"
  elif is_git_repo "$REPO_ROOT/main"; then
    REPO_PATH="$REPO_ROOT/main"
  elif is_git_repo "$REPO_ROOT"; then
    REPO_PATH="$REPO_ROOT"
  else
    REPO_PATH="$(find "$REPO_ROOT" -maxdepth 4 \( -type d -o -type f \) -name .git -print -quit 2>/dev/null | sed 's|/.git$||')"
    if ! is_git_repo "$REPO_PATH"; then
      REPO_PATH=""
    fi
  fi
  if [ -z "${REPO_PATH:-}" ]; then
    echo "ERROR: Requested repo '$REPO_OVERRIDE' was not found under $WORKSPACE_ROOT" >&2
    exit 1
  fi
else
  if [ -n "${SANDBOX_REPO:-}" ] && is_git_repo "$SANDBOX_REPO"; then
    REPO_PATH="$SANDBOX_REPO"
  elif is_git_repo "$WORKSPACE_ROOT/staging"; then
    REPO_PATH="$WORKSPACE_ROOT/staging"
  elif is_git_repo "$WORKSPACE_ROOT/main"; then
    REPO_PATH="$WORKSPACE_ROOT/main"
  else
    REPO_PATH="$(find "$WORKSPACE_ROOT" -maxdepth 3 \( -type d -o -type f \) -name .git -print -quit 2>/dev/null | sed 's|/.git$||')"
  fi
fi

if ! is_git_repo "$REPO_PATH"; then
  echo "ERROR: Could not locate sandbox git repo under $WORKSPACE_ROOT" >&2
  exit 1
fi
`
}

func listSandboxWorktrees(sandbox *api.FleetSandbox, repoOverride string) ([]worktreeInfo, error) {
	sshCmd := fmt.Sprintf(
		"set -euo pipefail\nREPO_OVERRIDE=%s\n%s\ncd \"$REPO_PATH\"\ngit worktree list --porcelain",
		shellQuote(repoOverride),
		sandboxRepoResolutionScript(),
	)
	output, err := sandboxSSHExec(sandbox, sshCmd)
	if err != nil {
		return nil, err
	}
	return parseWorktreeList(output, sandbox), nil
}

func resolveSandboxWorktreePathInRepo(sandbox *api.FleetSandbox, branch, repoOverride string) (string, error) {
	worktrees, err := listSandboxWorktreesFn(sandbox, repoOverride)
	if err != nil {
		return "", err
	}
	for _, wt := range worktrees {
		if wt.Branch == branch {
			return wt.Path, nil
		}
	}
	if strings.TrimSpace(repoOverride) != "" {
		return "", fmt.Errorf("worktree %q not found in repo %q on %s", branch, repoOverride, sandbox.DisplayName())
	}
	return "", fmt.Errorf("worktree %q not found on %s", branch, sandbox.DisplayName())
}

func resolveSandboxWorktreePath(sandbox *api.FleetSandbox, branch string) (string, error) {
	return resolveSandboxWorktreePathInRepo(sandbox, branch, "")
}

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
	return getClaimedSandboxNamed(cmd, "")
}

// getClaimedSandboxNamed returns a claimed sandbox, optionally filtered by name.
//
// When targetName is non-empty, this function resolves by either display name
// or VM name (case-insensitive) and returns an error if no match is found.
func getClaimedSandboxNamed(cmd *cobra.Command, targetName string) (*api.FleetSandbox, error) {
	targetName = strings.TrimSpace(targetName)
	mine, err := getClaimedSandboxesFn(cmd)
	if err != nil {
		ui.PrintError("Failed to fetch your sandboxes: %v", err)
		return nil, err
	}

	if len(mine) == 0 {
		ui.PrintInfo("You don't have any claimed sandboxes")
		ui.PrintInfo("Run 'revyl --dev sandbox claim' to claim one")
		return nil, fmt.Errorf("no claimed sandbox")
	}

	if targetName != "" {
		for i := range mine {
			name := mine[i].DisplayName()
			vmName := mine[i].VmName
			if strings.EqualFold(name, targetName) || strings.EqualFold(vmName, targetName) {
				return &mine[i], nil
			}
		}
		ui.PrintError("Sandbox '%s' not found in your claimed sandboxes", targetName)
		return nil, fmt.Errorf("sandbox %q not found", targetName)
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

func defaultGetClaimedSandboxes(cmd *cobra.Command) ([]api.FleetSandbox, error) {
	apiKey, err := getAPIKey()
	if err != nil {
		return nil, err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return client.GetMySandboxes(ctx)
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
	sandboxWorktreeCreateCmd.Flags().StringVar(&worktreeCreateRepo, "repo", "", "Repo directory name under ~/workspace (recommended for multi-repo sandboxes)")
	sandboxWorktreeCreateCmd.Flags().StringVarP(&worktreeSandboxName, "name", "n", "", "Target sandbox name (required when you have multiple claimed sandboxes)")

	// worktree list/setup/remove flags
	sandboxWorktreeListCmd.Flags().StringVarP(&worktreeSandboxName, "name", "n", "", "Target sandbox name")
	sandboxWorktreeListCmd.Flags().StringVar(&worktreeListRepo, "repo", "", "Repo directory name under ~/workspace (optional for multi-repo sandboxes)")
	sandboxWorktreeSetupCmd.Flags().StringVarP(&worktreeSandboxName, "name", "n", "", "Target sandbox name")
	sandboxWorktreeSetupCmd.Flags().StringVar(&worktreeSetupRepo, "repo", "", "Repo directory name under ~/workspace (required)")
	sandboxWorktreeRemoveCmd.Flags().StringVarP(&worktreeSandboxName, "name", "n", "", "Target sandbox name")
	sandboxWorktreeRemoveCmd.Flags().StringVar(&worktreeRemoveRepo, "repo", "", "Repo directory name under ~/workspace (required)")

	// worktree remove flags
	sandboxWorktreeRemoveCmd.Flags().BoolVarP(&worktreeRemoveForce, "force", "f", false, "Skip confirmation prompt")
	_ = sandboxWorktreeSetupCmd.MarkFlagRequired("repo")
	_ = sandboxWorktreeRemoveCmd.MarkFlagRequired("repo")
}
