// Package main provides auth sync/wipe commands for Fleet sandboxes.
//
// These commands manage authentication credentials on sandboxes,
// syncing local configs (GitHub CLI, Claude, Codex, git identity)
// to the remote sandbox and wiping them on release.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	sandboxpkg "github.com/revyl/cli/internal/sandbox"
	"github.com/revyl/cli/internal/ui"
)

// sandboxAuthCmd is the parent command for auth sync/wipe operations.
var sandboxAuthCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage auth credentials on your sandbox",
	Long: `Sync or wipe authentication credentials between your local machine and sandbox.

COMMANDS:
  sync - Copy local auth configs (gh, claude, codex, git identity) to sandbox
  wipe - Remove all auth configs from sandbox

EXAMPLES:
  revyl --dev sandbox auth sync
  revyl --dev sandbox auth wipe`,
}

// sandboxAuthSyncCmd copies local auth configs to the sandbox.
var sandboxAuthSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync local auth credentials to your sandbox",
	Long: `Copy local authentication configs to your claimed sandbox.

Syncs the following:
  - ~/.config/gh/       (GitHub CLI)
  - ~/.claude/          (Claude AI)
  - ~/.codex/           (Codex AI)
  - git user.name       (git identity)
  - git user.email      (git identity)

EXAMPLES:
  revyl --dev sandbox auth sync
  revyl --dev sandbox auth sync --json`,
	RunE: runSandboxAuthSync,
}

// authSyncItem describes a single credential directory or config to sync.
type authSyncItem struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Dest     string `json:"dest"`
	Synced   bool   `json:"synced"`
	Skipped  bool   `json:"skipped,omitempty"`
	ErrorMsg string `json:"error,omitempty"`
}

// runSandboxAuthSync copies local auth credentials to the sandbox via SCP/SSH.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (unused)
//
// Returns:
//   - error: Any error that occurred
func runSandboxAuthSync(cmd *cobra.Command, args []string) error {
	target, err := getClaimedSandbox(cmd)
	if err != nil {
		return err
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	cfg, err := sandboxpkg.ResolveSSHConfig(target)
	if err != nil {
		return err
	}

	if err := sandboxpkg.EnsureCloudflared(); err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not determine home directory: %w", err)
	}

	// Define credential directories to sync
	syncDirs := []struct {
		name   string
		local  string
		remote string
	}{
		{"GitHub CLI", filepath.Join(home, ".config", "gh"), "~/.config/gh"},
		{"Claude", filepath.Join(home, ".claude"), "~/.claude"},
		{"Codex", filepath.Join(home, ".codex"), "~/.codex"},
	}

	if !jsonOutput {
		ui.StartSpinner("Syncing auth credentials...")
	}

	var results []authSyncItem

	// Sync directories via SCP
	for _, dir := range syncDirs {
		item := authSyncItem{
			Name:   dir.name,
			Source: dir.local,
			Dest:   dir.remote,
		}

		// Check if local directory exists
		if _, statErr := os.Stat(dir.local); os.IsNotExist(statErr) {
			item.Skipped = true
			results = append(results, item)
			continue
		}

		// Ensure remote parent directory exists
		parentDir := filepath.Dir(strings.Replace(dir.remote, "~", fmt.Sprintf("/Users/%s", cfg.User), 1))
		_, _ = sandboxpkg.SSHExec(target, fmt.Sprintf("mkdir -p %s", parentDir))

		// SCP the directory
		remoteDest := fmt.Sprintf("%s@%s:%s", cfg.User, cfg.Host, dir.remote)
		scpArgs := []string{
			"-o", fmt.Sprintf("ProxyCommand=cloudflared access ssh --hostname %s", cfg.Host),
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "LogLevel=ERROR",
			"-r",
			dir.local,
			remoteDest,
		}

		scpCmd := exec.Command("scp", scpArgs...)
		if scpErr := scpCmd.Run(); scpErr != nil {
			item.ErrorMsg = scpErr.Error()
			results = append(results, item)
			continue
		}

		item.Synced = true
		results = append(results, item)
	}

	// Sync git identity
	gitItem := authSyncItem{
		Name:   "Git Identity",
		Source: "local git config",
		Dest:   "remote git config",
	}

	gitName, _ := exec.Command("git", "config", "--global", "user.name").Output()
	gitEmail, _ := exec.Command("git", "config", "--global", "user.email").Output()

	name := strings.TrimSpace(string(gitName))
	email := strings.TrimSpace(string(gitEmail))

	if name != "" || email != "" {
		var gitCmds []string
		if name != "" {
			gitCmds = append(gitCmds, fmt.Sprintf("git config --global user.name '%s'", name))
		}
		if email != "" {
			gitCmds = append(gitCmds, fmt.Sprintf("git config --global user.email '%s'", email))
		}
		_, sshErr := sandboxpkg.SSHExec(target, strings.Join(gitCmds, " && "))
		if sshErr != nil {
			gitItem.ErrorMsg = sshErr.Error()
		} else {
			gitItem.Synced = true
		}
	} else {
		gitItem.Skipped = true
	}
	results = append(results, gitItem)

	if !jsonOutput {
		ui.StopSpinner()
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(map[string]interface{}{
			"results":      results,
			"sandbox_name": target.DisplayName(),
		}, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	// Display results
	for _, item := range results {
		if item.Synced {
			ui.PrintSuccess("Synced %s", item.Name)
		} else if item.Skipped {
			ui.PrintDim("Skipped %s (not found locally)", item.Name)
		} else {
			ui.PrintWarning("Failed %s: %s", item.Name, item.ErrorMsg)
		}
	}

	return nil
}

// sandboxAuthWipeCmd removes all auth credentials from the sandbox.
var sandboxAuthWipeCmd = &cobra.Command{
	Use:   "wipe",
	Short: "Remove all auth credentials from your sandbox",
	Long: `Remove all authentication configs from your claimed sandbox.

Removes the following:
  - ~/.config/gh/       (GitHub CLI)
  - ~/.claude/          (Claude AI)
  - ~/.codex/           (Codex AI)
  - git user.name       (git identity)
  - git user.email      (git identity)

EXAMPLES:
  revyl --dev sandbox auth wipe
  revyl --dev sandbox auth wipe --json`,
	RunE: runSandboxAuthWipe,
}

// runSandboxAuthWipe removes all auth credentials from the sandbox via SSH.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (unused)
//
// Returns:
//   - error: Any error that occurred
func runSandboxAuthWipe(cmd *cobra.Command, args []string) error {
	target, err := getClaimedSandbox(cmd)
	if err != nil {
		return err
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	if !jsonOutput {
		ui.StartSpinner("Wiping auth credentials...")
	}

	wipeCmd := "rm -rf ~/.config/gh ~/.claude ~/.codex 2>/dev/null; " +
		"git config --global --remove-section user 2>/dev/null; " +
		"echo 'done'"

	_, sshErr := sandboxpkg.SSHExec(target, wipeCmd)

	if !jsonOutput {
		ui.StopSpinner()
	}

	if sshErr != nil {
		ui.PrintError("Failed to wipe auth credentials: %v", sshErr)
		return sshErr
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(map[string]interface{}{
			"success":      true,
			"sandbox_name": target.DisplayName(),
		}, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.PrintSuccess("Auth credentials wiped from %s", target.DisplayName())
	return nil
}

// --- init ---

func init() {
	sandboxCmd.AddCommand(sandboxAuthCmd)

	sandboxAuthCmd.AddCommand(sandboxAuthSyncCmd)
	sandboxAuthCmd.AddCommand(sandboxAuthWipeCmd)
}
