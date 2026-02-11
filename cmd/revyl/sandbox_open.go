// Package main provides IDE and terminal open commands for Fleet sandboxes.
//
// These commands open remote worktree directories in various editors
// using SSH remote extensions or terminal sessions.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	sandboxpkg "github.com/revyl/cli/internal/sandbox"
	"github.com/revyl/cli/internal/ui"
)

var (
	openEditor   string
	openTerminal bool
)

// sandboxOpenCmd opens a worktree in an IDE or terminal.
var sandboxOpenCmd = &cobra.Command{
	Use:   "open <branch>",
	Short: "Open a worktree in your IDE or terminal",
	Long: `Open a sandbox worktree in your preferred IDE via SSH remote,
or open a terminal session at the worktree path.

Supported editors: cursor, vscode, zed

EXAMPLES:
  revyl --dev sandbox open feature-x                     # Default: cursor
  revyl --dev sandbox open feature-x --editor vscode
  revyl --dev sandbox open feature-x --editor zed
  revyl --dev sandbox open feature-x --terminal`,
	Args: cobra.ExactArgs(1),
	RunE: runSandboxOpen,
}

// runSandboxOpen opens the specified worktree in an IDE or terminal.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (branch name)
//
// Returns:
//   - error: Any error that occurred
func runSandboxOpen(cmd *cobra.Command, args []string) error {
	branch := args[0]

	target, err := getClaimedSandbox(cmd)
	if err != nil {
		return err
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	// Build the remote path
	remotePath := fmt.Sprintf("/Users/%s/workspace/%s", target.EffectiveSSHUser(), branch)
	sshHost := target.EffectiveTunnelHostname()
	sshUser := target.EffectiveSSHUser()

	if openTerminal {
		return openSSHTerminal(target, remotePath, jsonOutput)
	}

	// Determine editor
	editor := openEditor
	if editor == "" {
		editor = "cursor"
	}

	// Build the SSH remote URI for the editor
	var openCmd *exec.Cmd
	switch editor {
	case "cursor":
		// cursor --remote ssh-remote+<user@host> <path>
		remoteArg := fmt.Sprintf("ssh-remote+%s@%s", sshUser, sshHost)
		openCmd = exec.Command("cursor", "--remote", remoteArg, remotePath)

	case "vscode", "code":
		// code --remote ssh-remote+<user@host> <path>
		remoteArg := fmt.Sprintf("ssh-remote+%s@%s", sshUser, sshHost)
		openCmd = exec.Command("code", "--remote", remoteArg, remotePath)

	case "zed":
		// zed ssh://<user@host>/<path>
		sshURI := fmt.Sprintf("ssh://%s@%s%s", sshUser, sshHost, remotePath)
		openCmd = exec.Command("zed", sshURI)

	default:
		ui.PrintError("Unsupported editor: %s. Use cursor, vscode, or zed.", editor)
		return fmt.Errorf("unsupported editor: %s", editor)
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(map[string]interface{}{
			"success":      true,
			"editor":       editor,
			"branch":       branch,
			"remote_path":  remotePath,
			"sandbox_name": target.DisplayName(),
		}, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.PrintInfo("Opening %s in %s...", branch, editor)

	if err := openCmd.Start(); err != nil {
		ui.PrintError("Failed to open %s: %v", editor, err)
		ui.PrintDim("Make sure %s is installed and available on your PATH", editor)
		return err
	}

	ui.PrintSuccess("Opened %s in %s", branch, editor)
	return nil
}

// openSSHTerminal opens a terminal session to the worktree path on the sandbox.
//
// Parameters:
//   - target: The sandbox to connect to
//   - remotePath: The worktree path on the sandbox
//   - jsonOutput: Whether to output JSON
//
// Returns:
//   - error: Any error that occurred
func openSSHTerminal(target *api.FleetSandbox, remotePath string, jsonOutput bool) error {
	if jsonOutput {
		data, _ := json.MarshalIndent(map[string]interface{}{
			"success":      true,
			"mode":         "terminal",
			"remote_path":  remotePath,
			"sandbox_name": target.DisplayName(),
		}, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	// On macOS, open a new Terminal.app or iTerm2 tab with SSH
	if runtime.GOOS == "darwin" {
		sshCmd := fmt.Sprintf("ssh -o 'ProxyCommand=cloudflared access ssh --hostname %s' -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -t %s@%s 'cd %s && exec $SHELL -l'",
			target.EffectiveTunnelHostname(),
			target.EffectiveSSHUser(),
			target.EffectiveTunnelHostname(),
			remotePath,
		)

		// Try iTerm2 first, fall back to Terminal.app
		appleScript := fmt.Sprintf(`tell application "Terminal"
	activate
	do script "%s"
end tell`, sshCmd)

		cmd := exec.Command("osascript", "-e", appleScript)
		if err := cmd.Start(); err != nil {
			// Fallback: just run SSH inline
			ui.PrintInfo("Connecting to %s at %s...", target.DisplayName(), remotePath)
			return runInlineSSH(target, remotePath)
		}

		ui.PrintSuccess("Opened terminal to %s", remotePath)
		return nil
	}

	// On other platforms, run SSH inline
	ui.PrintInfo("Connecting to %s at %s...", target.DisplayName(), remotePath)
	return runInlineSSH(target, remotePath)
}

// runInlineSSH runs an interactive SSH session that lands in the worktree path.
//
// Parameters:
//   - target: The sandbox to connect to
//   - remotePath: The directory to cd into on the sandbox
//
// Returns:
//   - error: Any error that occurred
func runInlineSSH(target *api.FleetSandbox, remotePath string) error {
	cfg, err := sandboxpkg.ResolveSSHConfig(target)
	if err != nil {
		return err
	}

	if err := sandboxpkg.EnsureCloudflared(); err != nil {
		return err
	}

	args := []string{
		"-o", fmt.Sprintf("ProxyCommand=cloudflared access ssh --hostname %s", cfg.Host),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-p", fmt.Sprintf("%d", cfg.Port),
		"-t", // Force pseudo-terminal for interactive use
		fmt.Sprintf("%s@%s", cfg.User, cfg.Host),
		fmt.Sprintf("cd %s && exec $SHELL -l", remotePath),
	}

	cmd := exec.Command("ssh", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// --- init ---

func init() {
	sandboxCmd.AddCommand(sandboxOpenCmd)

	sandboxOpenCmd.Flags().StringVar(&openEditor, "editor", "", "Editor to open in (cursor, vscode, zed). Default: cursor")
	sandboxOpenCmd.Flags().BoolVar(&openTerminal, "terminal", false, "Open a terminal session instead of an IDE")
}
