// Package main provides tunnel management commands for Fleet sandboxes.
//
// Tunnel commands manage Cloudflare tunnels on sandboxes via SSH,
// exposing running services (frontend, backend) to public URLs.
package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	sandboxpkg "github.com/revyl/cli/internal/sandbox"
	"github.com/revyl/cli/internal/ui"
)

// sandboxTunnelCmd is the parent command for tunnel operations.
var sandboxTunnelCmd = &cobra.Command{
	Use:   "tunnel",
	Short: "Manage Cloudflare tunnels on your sandbox",
	Long: `Manage Cloudflare tunnels that expose services on your sandbox to public URLs.

COMMANDS:
  list  - List active tunnels
  start - Start a tunnel for a port
  stop  - Stop a tunnel

EXAMPLES:
  revyl --dev sandbox tunnel list
  revyl --dev sandbox tunnel start --port 3000
  revyl --dev sandbox tunnel stop --port 3000`,
}

// sandboxTunnelListCmd lists active tunnels.
var sandboxTunnelListCmd = &cobra.Command{
	Use:   "list",
	Short: "List active tunnels on your sandbox",
	Long: `List all active Cloudflare tunnels on your claimed sandbox.

EXAMPLES:
  revyl --dev sandbox tunnel list
  revyl --dev sandbox tunnel list --json`,
	RunE: runSandboxTunnelList,
}

// runSandboxTunnelList lists active tunnels by checking cloudflared processes via SSH.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (unused)
//
// Returns:
//   - error: Any error that occurred
func runSandboxTunnelList(cmd *cobra.Command, args []string) error {
	target, err := getClaimedSandbox(cmd)
	if err != nil {
		return err
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	if !jsonOutput {
		ui.StartSpinner("Listing tunnels...")
	}

	// List cloudflared tunnel processes
	output, err := sandboxpkg.SSHExec(target, "ps aux | grep '[c]loudflared.*--url' | awk '{for(i=1;i<=NF;i++) if($i==\"--url\") print $(i+1)}' || true")

	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to list tunnels: %v", err)
		return err
	}

	tunnels := parseTunnelList(output)

	if jsonOutput {
		data, _ := json.MarshalIndent(map[string]interface{}{
			"tunnels": tunnels,
			"count":   len(tunnels),
		}, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(tunnels) == 0 {
		ui.PrintInfo("No active tunnels on %s", target.DisplayName())
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Start a tunnel", Command: "revyl --dev sandbox tunnel start --port 3000"},
		})
		return nil
	}

	ui.Println()
	ui.PrintInfo("Active tunnels on %s (%d):", target.DisplayName(), len(tunnels))
	ui.Println()

	table := ui.NewTable("PORT", "URL")
	table.SetMinWidth(0, 6)
	table.SetMinWidth(1, 40)

	for _, t := range tunnels {
		table.AddRow(t.Port, t.URL)
	}

	table.Render()
	return nil
}

var (
	tunnelStartPort int
	tunnelStartName string
)

// sandboxTunnelStartCmd starts a tunnel for a port.
var sandboxTunnelStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a Cloudflare tunnel for a port",
	Long: `Start a Cloudflare tunnel to expose a local port on your sandbox.

Common ports:
  3000 - Frontend (Next.js)
  8000 - Backend (FastAPI)

EXAMPLES:
  revyl --dev sandbox tunnel start --port 3000
  revyl --dev sandbox tunnel start --port 8000 --name backend`,
	RunE: runSandboxTunnelStart,
}

// runSandboxTunnelStart starts a cloudflared tunnel via SSH.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (unused)
//
// Returns:
//   - error: Any error that occurred
func runSandboxTunnelStart(cmd *cobra.Command, args []string) error {
	if tunnelStartPort == 0 {
		ui.PrintError("--port is required")
		return fmt.Errorf("--port is required")
	}

	target, err := getClaimedSandbox(cmd)
	if err != nil {
		return err
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	// Start cloudflared in the background on the sandbox
	sshCmd := fmt.Sprintf(
		"nohup cloudflared tunnel --url http://localhost:%d > /tmp/tunnel_%d.log 2>&1 & echo $!",
		tunnelStartPort, tunnelStartPort,
	)

	if !jsonOutput {
		ui.StartSpinner(fmt.Sprintf("Starting tunnel for port %d...", tunnelStartPort))
	}

	pidOutput, err := sandboxpkg.SSHExec(target, sshCmd)

	if err != nil {
		if !jsonOutput {
			ui.StopSpinner()
		}
		ui.PrintError("Failed to start tunnel: %v", err)
		return err
	}

	// Wait briefly for cloudflared to output its URL
	urlOutput, _ := sandboxpkg.SSHExec(target, fmt.Sprintf(
		"sleep 3 && grep -o 'https://[^ ]*\\.trycloudflare\\.com' /tmp/tunnel_%d.log 2>/dev/null | head -1",
		tunnelStartPort,
	))

	if !jsonOutput {
		ui.StopSpinner()
	}

	tunnelURL := strings.TrimSpace(urlOutput)

	if jsonOutput {
		data, _ := json.MarshalIndent(map[string]interface{}{
			"success":      true,
			"port":         tunnelStartPort,
			"pid":          strings.TrimSpace(pidOutput),
			"url":          tunnelURL,
			"sandbox_name": target.DisplayName(),
		}, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.PrintSuccess("Tunnel started for port %d", tunnelStartPort)
	ui.PrintInfo("  PID: %s", strings.TrimSpace(pidOutput))
	if tunnelURL != "" {
		ui.PrintInfo("  URL: %s", ui.LinkStyle.Render(tunnelURL))
	} else {
		ui.PrintDim("  URL will be available shortly. Check with: revyl --dev sandbox tunnel list")
	}

	return nil
}

var tunnelStopPort int

// sandboxTunnelStopCmd stops a tunnel.
var sandboxTunnelStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop a Cloudflare tunnel",
	Long: `Stop a Cloudflare tunnel running on your sandbox.

EXAMPLES:
  revyl --dev sandbox tunnel stop --port 3000
  revyl --dev sandbox tunnel stop --all`,
	RunE: runSandboxTunnelStop,
}

var tunnelStopAll bool

// runSandboxTunnelStop stops a cloudflared tunnel via SSH.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command arguments (unused)
//
// Returns:
//   - error: Any error that occurred
func runSandboxTunnelStop(cmd *cobra.Command, args []string) error {
	if tunnelStopPort == 0 && !tunnelStopAll {
		ui.PrintError("Specify --port or --all")
		return fmt.Errorf("specify --port or --all")
	}

	target, err := getClaimedSandbox(cmd)
	if err != nil {
		return err
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	var sshCmd string
	if tunnelStopAll {
		sshCmd = "pkill -f 'cloudflared tunnel' 2>/dev/null; echo 'done'"
	} else {
		sshCmd = fmt.Sprintf("pkill -f 'cloudflared tunnel.*--url http://localhost:%d' 2>/dev/null; echo 'done'", tunnelStopPort)
	}

	if !jsonOutput {
		ui.StartSpinner("Stopping tunnel...")
	}

	_, err = sandboxpkg.SSHExec(target, sshCmd)

	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to stop tunnel: %v", err)
		return err
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(map[string]interface{}{
			"success": true,
			"port":    tunnelStopPort,
			"all":     tunnelStopAll,
		}, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if tunnelStopAll {
		ui.PrintSuccess("All tunnels stopped")
	} else {
		ui.PrintSuccess("Tunnel stopped for port %d", tunnelStopPort)
	}

	return nil
}

// --- helpers ---

// tunnelEntry represents a parsed tunnel from process listing.
type tunnelEntry struct {
	Port string `json:"port"`
	URL  string `json:"url"`
}

// parseTunnelList parses the output of the tunnel process listing.
//
// Parameters:
//   - output: Raw output from the SSH command
//
// Returns:
//   - []tunnelEntry: Parsed tunnel entries
func parseTunnelList(output string) []tunnelEntry {
	var tunnels []tunnelEntry
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Extract port from URL like "http://localhost:3000"
		if strings.Contains(line, "localhost:") {
			parts := strings.Split(line, ":")
			if len(parts) >= 3 {
				port := parts[len(parts)-1]
				tunnels = append(tunnels, tunnelEntry{Port: port, URL: line})
			}
		}
	}
	return tunnels
}

// --- init ---

func init() {
	sandboxCmd.AddCommand(sandboxTunnelCmd)

	sandboxTunnelCmd.AddCommand(sandboxTunnelListCmd)
	sandboxTunnelCmd.AddCommand(sandboxTunnelStartCmd)
	sandboxTunnelCmd.AddCommand(sandboxTunnelStopCmd)

	// tunnel start flags
	sandboxTunnelStartCmd.Flags().IntVar(&tunnelStartPort, "port", 0, "Local port to tunnel (required)")
	sandboxTunnelStartCmd.Flags().StringVar(&tunnelStartName, "name", "", "Service name label (e.g., frontend, backend)")
	_ = sandboxTunnelStartCmd.MarkFlagRequired("port")

	// tunnel stop flags
	sandboxTunnelStopCmd.Flags().IntVar(&tunnelStopPort, "port", 0, "Port of tunnel to stop")
	sandboxTunnelStopCmd.Flags().BoolVar(&tunnelStopAll, "all", false, "Stop all tunnels")
}
