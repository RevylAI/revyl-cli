// Package main provides sandbox management commands for the Revyl CLI.
//
// The sandbox command group manages Fleet sandboxes — Mac Mini VMs with
// pre-configured iOS simulators and Android emulators for mobile development.
// These commands are gated behind the --dev flag for internal use.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/sandbox"
	"github.com/revyl/cli/internal/ui"
)

// sandboxCmd is the parent command for sandbox management.
var sandboxCmd = &cobra.Command{
	Use:   "sandbox",
	Short: "Manage Fleet sandboxes (internal)",
	Long: `Manage Fleet sandboxes — Mac Mini VMs with iOS simulators and Android emulators.

Sandboxes are pool-based: claim one to get an isolated dev environment,
then release it when you're done. Each sandbox comes with pre-installed
tools, SSH access via Cloudflare tunnels, and git worktree support.

Requires --dev flag (internal feature).

COMMANDS:
  status   - Show pool availability
  list     - List all sandboxes
  claim    - Claim an available sandbox
  release  - Release your sandbox back to the pool
  mine     - Show your claimed sandbox(es)
  ssh      - SSH into your sandbox
  ssh-key  - Manage SSH keys on your sandbox

EXAMPLES:
  revyl --dev sandbox status
  revyl --dev sandbox claim
  revyl --dev sandbox ssh
  revyl --dev sandbox release`,
	PersistentPreRunE: requireDevMode,
}

// requireDevMode ensures the --dev flag is set before running any sandbox command.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments
//
// Returns:
//   - error: If --dev flag is not set
func requireDevMode(cmd *cobra.Command, args []string) error {
	// Run parent PersistentPreRun first (sets up debug logging, quiet mode, etc.)
	if parent := cmd.Root(); parent.PersistentPreRun != nil {
		parent.PersistentPreRun(cmd, args)
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	if !devMode {
		return fmt.Errorf("sandbox commands require --dev flag (internal feature)")
	}
	return nil
}

// --- status ---

// sandboxStatusCmd shows the Fleet pool status.
var sandboxStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show sandbox pool availability",
	Long: `Show the current Fleet sandbox pool status.

Displays how many sandboxes are available, claimed, and in maintenance.

EXAMPLES:
  revyl --dev sandbox status
  revyl --dev sandbox status --json`,
	RunE: runSandboxStatus,
}

// runSandboxStatus fetches and displays the Fleet pool status.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (unused)
//
// Returns:
//   - error: Any error that occurred
func runSandboxStatus(cmd *cobra.Command, args []string) error {
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if !jsonOutput {
		ui.StartSpinner("Fetching pool status...")
	}

	status, err := client.GetFleetStatus(ctx)

	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to fetch pool status: %v", err)
		return err
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(status, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.Println()
	ui.PrintBox("Sandbox Pool", fmt.Sprintf(
		"%s  %d total\n%s  %d available\n%s  %d claimed\n%s  %d maintenance",
		ui.DimStyle.Render("Total:"),
		status.Total,
		ui.SuccessStyle.Render("Available:"),
		status.Available,
		ui.StatusRunningStyle.Render("Claimed:"),
		status.Claimed,
		ui.WarningStyle.Render("Maintenance:"),
		status.EffectiveMaintenance(),
	))

	if status.Available > 0 {
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Claim a sandbox", Command: "revyl --dev sandbox claim"},
		})
	} else {
		ui.Println()
		ui.PrintWarning("No sandboxes available. Try again later or ask an admin.")
	}

	return nil
}

// --- list ---

// sandboxListCmd lists all sandboxes.
var sandboxListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all sandboxes with status",
	Long: `List all sandboxes in the Fleet pool with their current status.

EXAMPLES:
  revyl --dev sandbox list
  revyl --dev sandbox list --json`,
	RunE: runSandboxList,
}

// runSandboxList fetches and displays all sandboxes.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (unused)
//
// Returns:
//   - error: Any error that occurred
func runSandboxList(cmd *cobra.Command, args []string) error {
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if !jsonOutput {
		ui.StartSpinner("Fetching sandboxes...")
	}

	sandboxes, err := client.ListSandboxes(ctx)

	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to list sandboxes: %v", err)
		return err
	}

	if jsonOutput {
		output := map[string]interface{}{
			"sandboxes": sandboxes,
			"count":     len(sandboxes),
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(sandboxes) == 0 {
		ui.PrintInfo("No sandboxes found in the pool")
		return nil
	}

	ui.Println()
	ui.PrintInfo("Sandboxes (%d):", len(sandboxes))
	ui.Println()

	table := ui.NewTable("NAME", "STATUS", "CLAIMED BY", "TUNNEL", "ID")
	table.SetMinWidth(0, 14)
	table.SetMinWidth(1, 12)
	table.SetMaxWidth(2, 30)
	table.SetMaxWidth(3, 40)
	table.SetMinWidth(4, 36)

	for _, s := range sandboxes {
		claimedBy := "-"
		if s.EffectiveClaimedBy() != "" {
			claimedBy = s.EffectiveClaimedBy()
		}
		tunnel := "-"
		if s.EffectiveTunnelHostname() != "" {
			tunnel = s.EffectiveTunnelHostname()
		}
		statusDisplay := formatSandboxStatus(s.EffectiveStatus())
		table.AddRow(s.DisplayName(), statusDisplay, claimedBy, tunnel, s.Id)
	}

	table.Render()
	return nil
}

// --- claim ---

// sandboxClaimCmd claims an available sandbox.
var sandboxClaimCmd = &cobra.Command{
	Use:   "claim",
	Short: "Claim an available sandbox",
	Long: `Claim an available sandbox from the pool.

The system automatically picks the best available sandbox and assigns it
to you. After claiming, your SSH key is pushed for secure access.

EXAMPLES:
  revyl --dev sandbox claim
  revyl --dev sandbox claim --json`,
	RunE: runSandboxClaim,
}

// runSandboxClaim claims a sandbox and sets up SSH access.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (unused)
//
// Returns:
//   - error: Any error that occurred
func runSandboxClaim(cmd *cobra.Command, args []string) error {
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if !jsonOutput {
		ui.StartSpinner("Claiming sandbox...")
	}

	result, err := client.ClaimSandbox(ctx)

	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			if apiErr.StatusCode == 409 {
				ui.PrintWarning("You already have a claimed sandbox")
				ui.PrintInfo("Run 'revyl --dev sandbox mine' to see it")
				return fmt.Errorf("already have a claimed sandbox")
			}
		}
		ui.PrintError("Failed to claim sandbox: %v", err)
		return err
	}

	if !result.Success {
		ui.PrintWarning("Could not claim sandbox: %s", result.Message)
		return fmt.Errorf("claim failed: %s", result.Message)
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	s := result.Sandbox
	ui.Println()
	ui.PrintSuccess("Claimed sandbox: %s", s.DisplayName())
	ui.PrintInfo("  ID:     %s", s.Id)
	if s.EffectiveTunnelHostname() != "" {
		ui.PrintInfo("  Tunnel: %s", s.EffectiveTunnelHostname())
	}

	// Try to push SSH key automatically
	keyPath, keyErr := sandbox.DefaultSSHPublicKeyPath()
	if keyErr == nil {
		pubKey, readErr := sandbox.ReadSSHPublicKey(keyPath)
		if readErr == nil {
			ui.Println()
			ui.StartSpinner("Pushing SSH key...")
			_, sshErr := client.PushSSHKey(ctx, s.Id, pubKey)
			ui.StopSpinner()
			if sshErr != nil {
				ui.PrintWarning("Could not push SSH key: %v", sshErr)
				ui.PrintInfo("Push manually: revyl --dev sandbox ssh-key push")
			} else {
				ui.PrintSuccess("SSH key pushed successfully")
			}
		}
	}

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "SSH into your sandbox", Command: "revyl --dev sandbox ssh"},
		{Label: "Create a worktree", Command: "revyl --dev sandbox worktree create <branch>"},
		{Label: "Release when done", Command: "revyl --dev sandbox release"},
	})

	return nil
}

// --- release ---

var sandboxReleaseForce bool

// sandboxReleaseCmd releases a claimed sandbox.
var sandboxReleaseCmd = &cobra.Command{
	Use:   "release [sandbox-id]",
	Short: "Release your sandbox back to the pool",
	Long: `Release your claimed sandbox back to the pool.

If you have one sandbox, it's released automatically.
If you have multiple, specify the sandbox ID.

EXAMPLES:
  revyl --dev sandbox release
  revyl --dev sandbox release <sandbox-id>
  revyl --dev sandbox release --force`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSandboxRelease,
}

// runSandboxRelease releases a claimed sandbox.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Optional sandbox ID
//
// Returns:
//   - error: Any error that occurred
func runSandboxRelease(cmd *cobra.Command, args []string) error {
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Determine which sandbox to release
	var sandboxID string
	if len(args) > 0 {
		sandboxID = args[0]
	} else {
		// Find user's claimed sandbox
		mine, err := client.GetMySandboxes(ctx)
		if err != nil {
			ui.PrintError("Failed to fetch your sandboxes: %v", err)
			return err
		}
		if len(mine) == 0 {
			ui.PrintInfo("You don't have any claimed sandboxes")
			return nil
		}
		if len(mine) > 1 {
			ui.PrintWarning("You have %d claimed sandboxes. Specify which to release:", len(mine))
			for _, s := range mine {
				ui.PrintInfo("  %s  %s", s.Id, s.DisplayName())
			}
			return fmt.Errorf("multiple sandboxes claimed, specify ID")
		}
		sandboxID = mine[0].Id
	}

	// Confirm unless --force
	if !sandboxReleaseForce && !jsonOutput {
		confirmed, err := ui.PromptConfirm("Release sandbox? This will clean up all worktrees and auth.", false)
		if err != nil || !confirmed {
			ui.PrintInfo("Cancelled")
			return nil
		}
	}

	if !jsonOutput {
		ui.StartSpinner("Releasing sandbox...")
	}

	result, err := client.ReleaseSandbox(ctx, sandboxID, false)

	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to release sandbox: %v", err)
		return err
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if result.Success {
		ui.PrintSuccess("Sandbox released successfully")
	} else {
		ui.PrintWarning("Release issue: %s", result.Message)
	}

	return nil
}

// --- mine ---

// sandboxMineCmd shows the user's claimed sandboxes.
var sandboxMineCmd = &cobra.Command{
	Use:   "mine",
	Short: "Show your claimed sandbox(es)",
	Long: `Show sandboxes currently claimed by you.

EXAMPLES:
  revyl --dev sandbox mine
  revyl --dev sandbox mine --json`,
	RunE: runSandboxMine,
}

// runSandboxMine displays the user's claimed sandboxes.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (unused)
//
// Returns:
//   - error: Any error that occurred
func runSandboxMine(cmd *cobra.Command, args []string) error {
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if !jsonOutput {
		ui.StartSpinner("Fetching your sandboxes...")
	}

	mine, err := client.GetMySandboxes(ctx)

	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to fetch sandboxes: %v", err)
		return err
	}

	if jsonOutput {
		output := map[string]interface{}{
			"sandboxes": mine,
			"count":     len(mine),
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(mine) == 0 {
		ui.PrintInfo("You don't have any claimed sandboxes")
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Claim one", Command: "revyl --dev sandbox claim"},
		})
		return nil
	}

	ui.Println()
	for _, s := range mine {
		ui.PrintSuccess("Sandbox: %s", s.DisplayName())
		ui.PrintInfo("  ID:     %s", s.Id)
		if s.EffectiveTunnelHostname() != "" {
			ui.PrintInfo("  Tunnel: %s", s.EffectiveTunnelHostname())
		}
		if s.ClaimedAt != nil {
			ui.PrintDim("  Claimed: %s", s.ClaimedAt.Format(time.RFC3339))
		}
	}

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "SSH into sandbox", Command: "revyl --dev sandbox ssh"},
		{Label: "List worktrees", Command: "revyl --dev sandbox worktree list"},
		{Label: "Release sandbox", Command: "revyl --dev sandbox release"},
	})

	return nil
}

// --- ssh ---

// sandboxSSHCmd opens an SSH session to the user's sandbox.
var sandboxSSHCmd = &cobra.Command{
	Use:   "ssh",
	Short: "SSH into your sandbox",
	Long: `Open an interactive SSH session to your claimed sandbox.

Connects via Cloudflare tunnel for secure access.
Requires cloudflared to be installed.

EXAMPLES:
  revyl --dev sandbox ssh`,
	RunE: runSandboxSSH,
}

// runSandboxSSH opens an interactive SSH session to the user's sandbox.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (unused)
//
// Returns:
//   - error: Any error that occurred
func runSandboxSSH(cmd *cobra.Command, args []string) error {
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Find the user's sandbox
	mine, err := client.GetMySandboxes(ctx)
	if err != nil {
		ui.PrintError("Failed to fetch your sandboxes: %v", err)
		return err
	}

	if len(mine) == 0 {
		ui.PrintInfo("You don't have any claimed sandboxes")
		ui.PrintInfo("Run 'revyl --dev sandbox claim' to claim one")
		return fmt.Errorf("no claimed sandbox")
	}

	// If multiple, let user choose
	target := &mine[0]
	if len(mine) > 1 {
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
			return fmt.Errorf("selection cancelled")
		}
		target = &mine[idx]
	}

	ui.PrintInfo("Connecting to %s...", target.DisplayName())
	return sandbox.SSHToSandbox(target)
}

// --- ssh-key ---

// sandboxSSHKeyCmd is the parent for SSH key subcommands.
var sandboxSSHKeyCmd = &cobra.Command{
	Use:   "ssh-key",
	Short: "Manage SSH keys on your sandbox",
	Long: `Manage SSH keys for sandbox access.

COMMANDS:
  push   - Push your SSH public key to your sandbox
  status - Check SSH key configuration`,
}

// sandboxSSHKeyPushCmd pushes the user's SSH key to their sandbox.
var sandboxSSHKeyPushCmd = &cobra.Command{
	Use:   "push [key-file]",
	Short: "Push your SSH public key to your sandbox",
	Long: `Push your SSH public key to your claimed sandbox.

Reads ~/.ssh/id_ed25519.pub by default, or specify a custom key file.

EXAMPLES:
  revyl --dev sandbox ssh-key push
  revyl --dev sandbox ssh-key push ~/.ssh/custom_key.pub`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSandboxSSHKeyPush,
}

// runSandboxSSHKeyPush pushes the user's SSH public key to their sandbox.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Optional path to SSH public key file
//
// Returns:
//   - error: Any error that occurred
func runSandboxSSHKeyPush(cmd *cobra.Command, args []string) error {
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	// Determine key file path
	var keyPath string
	if len(args) > 0 {
		keyPath = args[0]
	} else {
		keyPath, err = sandbox.DefaultSSHPublicKeyPath()
		if err != nil {
			ui.PrintError("%v", err)
			return err
		}
	}

	pubKey, err := sandbox.ReadSSHPublicKey(keyPath)
	if err != nil {
		ui.PrintError("%v", err)
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Find user's sandbox
	mine, err := client.GetMySandboxes(ctx)
	if err != nil {
		ui.PrintError("Failed to fetch your sandboxes: %v", err)
		return err
	}
	if len(mine) == 0 {
		ui.PrintInfo("You don't have any claimed sandboxes")
		return fmt.Errorf("no claimed sandbox")
	}

	target := &mine[0]

	if !jsonOutput {
		ui.StartSpinner("Pushing SSH key...")
	}

	result, err := client.PushSSHKey(ctx, target.Id, pubKey)

	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to push SSH key: %v", err)
		return err
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if result.EffectiveAlreadyExists() {
		ui.PrintInfo("SSH key already configured on %s", target.DisplayName())
	} else if result.Success {
		ui.PrintSuccess("SSH key pushed to %s", target.DisplayName())
	} else {
		ui.PrintWarning("SSH key push issue: %s", result.Message)
	}

	return nil
}

// sandboxSSHKeyStatusCmd checks SSH key status.
var sandboxSSHKeyStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check SSH key configuration",
	Long: `Check whether your SSH key is configured on your sandbox.

EXAMPLES:
  revyl --dev sandbox ssh-key status`,
	RunE: runSandboxSSHKeyStatus,
}

// runSandboxSSHKeyStatus checks SSH key configuration on the user's sandbox.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (unused)
//
// Returns:
//   - error: Any error that occurred
func runSandboxSSHKeyStatus(cmd *cobra.Command, args []string) error {
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	mine, err := client.GetMySandboxes(ctx)
	if err != nil {
		ui.PrintError("Failed to fetch your sandboxes: %v", err)
		return err
	}
	if len(mine) == 0 {
		ui.PrintInfo("You don't have any claimed sandboxes")
		return fmt.Errorf("no claimed sandbox")
	}

	target := &mine[0]

	if !jsonOutput {
		ui.StartSpinner("Checking SSH key status...")
	}

	result, err := client.GetSSHKeyStatus(ctx, target.Id)

	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to check SSH key status: %v", err)
		return err
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if result.Configured {
		ui.PrintSuccess("SSH key configured on %s", target.DisplayName())
		if result.EffectiveKeyFingerprint() != "" {
			ui.PrintDim("  Fingerprint: %s", result.EffectiveKeyFingerprint())
		}
		if result.EffectiveSandboxReachable() {
			ui.PrintSuccess("Sandbox is reachable via SSH")
		} else {
			ui.PrintWarning("Sandbox is not reachable (may be starting up)")
		}
	} else {
		ui.PrintWarning("No SSH key configured on %s", target.DisplayName())
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Push your key", Command: "revyl --dev sandbox ssh-key push"},
		})
	}

	return nil
}

// --- helpers ---

// formatSandboxStatus returns a styled status string for display.
//
// Parameters:
//   - status: The sandbox status string
//
// Returns:
//   - string: Styled status string
func formatSandboxStatus(status string) string {
	switch status {
	case "available":
		return ui.SuccessStyle.Render("● available")
	case "claimed":
		return ui.StatusRunningStyle.Render("● claimed")
	case "maintenance":
		return ui.WarningStyle.Render("● maintenance")
	case "reserved":
		return ui.DimStyle.Render("● reserved")
	default:
		return ui.DimStyle.Render("● " + status)
	}
}

// --- init ---

func init() {
	// Register subcommands under sandbox
	sandboxCmd.AddCommand(sandboxStatusCmd)
	sandboxCmd.AddCommand(sandboxListCmd)
	sandboxCmd.AddCommand(sandboxClaimCmd)
	sandboxCmd.AddCommand(sandboxReleaseCmd)
	sandboxCmd.AddCommand(sandboxMineCmd)
	sandboxCmd.AddCommand(sandboxSSHCmd)
	sandboxCmd.AddCommand(sandboxSSHKeyCmd)

	// SSH key subcommands
	sandboxSSHKeyCmd.AddCommand(sandboxSSHKeyPushCmd)
	sandboxSSHKeyCmd.AddCommand(sandboxSSHKeyStatusCmd)

	// Release flags
	sandboxReleaseCmd.Flags().BoolVarP(&sandboxReleaseForce, "force", "f", false, "Skip confirmation prompt")
}
