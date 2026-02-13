// Package main provides the `revyl services` command group for managing
// service sessions defined in .revyl/sessions.json.
//
// The sessions file uses the Terminal Keeper format, enabling shared session
// definitions across VS Code (Terminal Keeper), Fleet Dashboard, and this CLI.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// servicesCmd is the parent command for service session management.
var servicesCmd = &cobra.Command{
	Use:   "services",
	Short: "Manage service sessions from .revyl/sessions.json",
	Long: `Manage service sessions defined in .revyl/sessions.json.

The sessions file uses the Terminal Keeper format — the same format used by
the Terminal Keeper VS Code extension. This enables shared session definitions
that work across:

  • VS Code (Terminal Keeper extension)
  • Fleet Dashboard (Ghostty terminal tabs)
  • Revyl CLI (this command)

Each session profile defines a set of terminal tabs with shell commands.
For example, the "default" session might start a frontend, backend, action
server, and workers — all in parallel with colored log output.

Run 'revyl services docs' for the full format reference.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

// servicesListCmd lists available session profiles.
var servicesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available session profiles",
	Long: `List all session profiles defined in .revyl/sessions.json.

Shows each session name, the number of terminals it defines, and which
session is marked as active (default).`,
	RunE: runServicesList,
}

// servicesStartCmd starts services for a session profile.
var servicesStartCmd = &cobra.Command{
	Use:   "start [session]",
	Short: "Start services for a session profile",
	Long: `Start all services defined in a session profile.

Reads .revyl/sessions.json from the current repository, resolves the session
name (defaults to the "active" session), and spawns each terminal's commands
as a background process. Output from all processes is streamed with colored
name prefixes (similar to docker-compose).

Handles SIGINT/SIGTERM to gracefully stop all spawned processes.

EXAMPLES:
  revyl services start                    # Start the active/default session
  revyl services start "default + tools"  # Start a specific session
  revyl services start --list             # Show sessions then start one`,
	Args: cobra.MaximumNArgs(1),
	RunE: runServicesStart,
}

// servicesStopCmd stops all running services.
var servicesStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop all running services",
	Long: `Stop all services previously started by 'revyl services start'.

Reads the PID file at .revyl/.services.pid and sends SIGTERM to each
process. Falls back to SIGKILL after a brief timeout.`,
	RunE: runServicesStop,
}

// servicesDocsCmd prints the full session format reference.
var servicesDocsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Print the .revyl/ session format reference",
	Long:  "Print comprehensive documentation for the .revyl/ directory and session format.",
	Run:   runServicesDocs,
}

func init() {
	servicesCmd.AddCommand(servicesListCmd)
	servicesCmd.AddCommand(servicesStartCmd)
	servicesCmd.AddCommand(servicesStopCmd)
	servicesCmd.AddCommand(servicesDocsCmd)
}

// pidFilePath returns the path to the .revyl/.services.pid file for a given repo root.
//
// Parameters:
//   - repoRoot: Path to the repository root.
//
// Returns:
//   - string: Absolute path to the PID file.
func pidFilePath(repoRoot string) string {
	return filepath.Join(repoRoot, ".revyl", ".services.pid")
}

// runServicesList prints all available session profiles.
//
// Parameters:
//   - cmd: The cobra command.
//   - args: Command arguments (unused).
//
// Returns:
//   - error: Any error during execution.
func runServicesList(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	repoRoot, err := config.FindRepoRoot(cwd)
	if err != nil {
		ui.PrintError("No .revyl/ directory found. Run 'revyl init' or create .revyl/sessions.json manually.")
		return err
	}

	cfg, err := config.LoadSessionsConfig(repoRoot)
	if err != nil {
		ui.PrintError("Failed to load sessions: %v", err)
		return err
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	if jsonOutput {
		type sessionInfo struct {
			Name      string `json:"name"`
			Terminals int    `json:"terminals"`
			IsActive  bool   `json:"is_active"`
		}

		var infos []sessionInfo
		for _, name := range config.SessionNames(cfg) {
			defs, _ := config.FlattenSession(cfg.Sessions[name])
			infos = append(infos, sessionInfo{
				Name:      name,
				Terminals: len(defs),
				IsActive:  name == cfg.Active,
			})
		}

		data, _ := json.MarshalIndent(infos, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	fmt.Printf("\nAvailable sessions (active: %s):\n\n", cfg.Active)

	for _, name := range config.SessionNames(cfg) {
		defs, err := config.FlattenSession(cfg.Sessions[name])
		if err != nil {
			log.Warn("Failed to parse session", "name", name, "error", err)
			continue
		}

		marker := "  "
		if name == cfg.Active {
			marker = "★ "
		}
		fmt.Printf("  %s%-25s %d terminals\n", marker, name, len(defs))
	}
	fmt.Println()

	return nil
}

// ANSI color codes for terminal output prefixes.
var colors = []string{
	"\033[35m", // magenta
	"\033[32m", // green
	"\033[33m", // yellow
	"\033[31m", // red
	"\033[36m", // cyan
	"\033[34m", // blue
	"\033[37m", // white
}

const resetColor = "\033[0m"

// runServicesStart starts all services for the specified session.
//
// Parameters:
//   - cmd: The cobra command.
//   - args: Optional session name (defaults to active session).
//
// Returns:
//   - error: Any error during execution.
func runServicesStart(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	repoRoot, err := config.FindRepoRoot(cwd)
	if err != nil {
		ui.PrintError("No .revyl/ directory found. Run 'revyl init' or create .revyl/sessions.json manually.")
		return err
	}

	cfg, err := config.LoadSessionsConfig(repoRoot)
	if err != nil {
		ui.PrintError("Failed to load sessions: %v", err)
		return err
	}

	// Resolve session name
	sessionName := cfg.Active
	if len(args) > 0 {
		sessionName = args[0]
	}
	if sessionName == "" {
		ui.PrintError("No session specified and no active session set in .revyl/sessions.json")
		return fmt.Errorf("no session specified")
	}

	items, ok := cfg.Sessions[sessionName]
	if !ok {
		ui.PrintError("Session '%s' not found. Available: %s", sessionName, strings.Join(config.SessionNames(cfg), ", "))
		return fmt.Errorf("session not found: %s", sessionName)
	}

	defs, err := config.FlattenSession(items)
	if err != nil {
		ui.PrintError("Failed to parse session '%s': %v", sessionName, err)
		return err
	}

	// Filter to auto-execute terminals only
	var autoExecDefs []config.TerminalDefinition
	for _, def := range defs {
		if def.ShouldAutoExecute() {
			autoExecDefs = append(autoExecDefs, def)
		}
	}

	if len(autoExecDefs) == 0 {
		ui.PrintInfo("No auto-execute terminals in session '%s'", sessionName)
		return nil
	}

	ui.PrintSuccess("Starting session: %s (%d services)", sessionName, len(autoExecDefs))
	fmt.Println()

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var pids []int
	var cmds []*exec.Cmd

	// Spawn each service
	for i, def := range autoExecDefs {
		color := colors[i%len(colors)]
		joinedCommands := strings.Join(def.Commands, " && ")
		padName := fmt.Sprintf("%-20s", def.Name)

		shellCmd := exec.Command("/bin/sh", "-c", joinedCommands)
		shellCmd.Dir = repoRoot
		shellCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		// Create pipes for stdout and stderr
		stdout, err := shellCmd.StdoutPipe()
		if err != nil {
			log.Warn("Failed to create stdout pipe", "service", def.Name, "error", err)
			continue
		}
		stderr, err := shellCmd.StderrPipe()
		if err != nil {
			log.Warn("Failed to create stderr pipe", "service", def.Name, "error", err)
			continue
		}

		if err := shellCmd.Start(); err != nil {
			log.Warn("Failed to start service", "service", def.Name, "error", err)
			continue
		}

		mu.Lock()
		pids = append(pids, shellCmd.Process.Pid)
		cmds = append(cmds, shellCmd)
		mu.Unlock()

		fmt.Printf("%s%s%s | started (pid %d)\n", color, padName, resetColor, shellCmd.Process.Pid)

		// Stream stdout with colored prefix
		wg.Add(1)
		go func(name string, color string, reader io.Reader) {
			defer wg.Done()
			scanner := bufio.NewScanner(reader)
			scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer
			for scanner.Scan() {
				fmt.Printf("%s%s%s | %s\n", color, fmt.Sprintf("%-20s", name), resetColor, scanner.Text())
			}
		}(def.Name, color, stdout)

		// Stream stderr with colored prefix
		wg.Add(1)
		go func(name string, color string, reader io.Reader) {
			defer wg.Done()
			scanner := bufio.NewScanner(reader)
			scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
			for scanner.Scan() {
				fmt.Printf("%s%s%s | %s\n", color, fmt.Sprintf("%-20s", name), resetColor, scanner.Text())
			}
		}(def.Name, color, stderr)
	}

	// Write PID file for `revyl services stop`
	writePIDFile(pidFilePath(repoRoot), pids)

	// Wait for signal or all processes to exit
	done := make(chan struct{})
	go func() {
		for _, c := range cmds {
			_ = c.Wait()
		}
		close(done)
	}()

	select {
	case sig := <-sigChan:
		fmt.Printf("\n%sReceived %s, stopping all services...%s\n", "\033[33m", sig, resetColor)
		stopProcesses(cmds)
	case <-done:
		fmt.Println("\nAll services exited.")
	}

	// Clean up PID file
	_ = os.Remove(pidFilePath(repoRoot))

	wg.Wait()
	return nil
}

// runServicesStop stops all services from a previous `revyl services start`.
//
// Parameters:
//   - cmd: The cobra command.
//   - args: Command arguments (unused).
//
// Returns:
//   - error: Any error during execution.
func runServicesStop(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	repoRoot, err := config.FindRepoRoot(cwd)
	if err != nil {
		ui.PrintError("No .revyl/ directory found")
		return err
	}

	pidFile := pidFilePath(repoRoot)
	data, err := os.ReadFile(pidFile)
	if err != nil {
		ui.PrintInfo("No running services found (no PID file at %s)", pidFile)
		return nil
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var pids []int
	stopped := 0
	for _, line := range lines {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			continue
		}

		// Guard against stale PIDs: verify the process is a shell we spawned
		if !isServiceProcess(pid) {
			log.Debug("PID is not a revyl service process, skipping", "pid", pid)
			continue
		}

		// Send SIGTERM to the process group (negative PID)
		if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
			log.Debug("Process group already exited", "pid", pid)
			continue
		}
		pids = append(pids, pid)
		stopped++
		log.Debug("Sent SIGTERM to process group", "pid", pid)
	}

	// Wait briefly, then SIGKILL any survivors
	if len(pids) > 0 {
		time.Sleep(3 * time.Second)
		for _, pid := range pids {
			if err := syscall.Kill(-pid, syscall.Signal(0)); err == nil {
				log.Warn("Process group did not exit after SIGTERM, sending SIGKILL", "pid", pid)
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}
	}

	_ = os.Remove(pidFile)

	if stopped > 0 {
		ui.PrintSuccess("Stopped %d service(s)", stopped)
	} else {
		ui.PrintInfo("No running services found")
	}

	return nil
}

// stopProcesses sends SIGTERM to all process groups and falls back to SIGKILL
// after a brief timeout if any process is still running.
//
// Parameters:
//   - cmds: Slice of exec.Cmd to stop.
func stopProcesses(cmds []*exec.Cmd) {
	// Send SIGTERM to each process group
	for _, c := range cmds {
		if c.Process != nil {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGTERM)
		}
	}

	// Wait up to 3 seconds for processes to exit, then SIGKILL survivors
	time.Sleep(3 * time.Second)
	for _, c := range cmds {
		if c.Process != nil {
			// Signal(0) checks if process is still alive
			if err := c.Process.Signal(syscall.Signal(0)); err == nil {
				log.Warn("Process did not exit after SIGTERM, sending SIGKILL", "pid", c.Process.Pid)
				_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
			}
		}
	}
}

// writePIDFile writes process IDs to a file, one per line.
//
// Parameters:
//   - path: Path to the PID file.
//   - pids: Process IDs to write.
func writePIDFile(path string, pids []int) {
	var lines []string
	for _, pid := range pids {
		lines = append(lines, strconv.Itoa(pid))
	}
	_ = os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

// isServiceProcess checks whether a PID belongs to a shell process spawned by
// revyl services start. This prevents killing unrelated processes if the PID
// file is stale and the OS has recycled the PID.
//
// Parameters:
//   - pid: The process ID to verify.
//
// Returns:
//   - bool: True if the process looks like a revyl-spawned shell.
func isServiceProcess(pid int) bool {
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	comm := strings.TrimSpace(string(out))
	// The spawned processes are /bin/sh -c "..." so the comm should be sh or /bin/sh
	return comm == "sh" || comm == "/bin/sh"
}

// runServicesDocs prints the full .revyl/ session format reference.
//
// Parameters:
//   - cmd: The cobra command.
//   - args: Command arguments (unused).
func runServicesDocs(cmd *cobra.Command, args []string) {
	fmt.Print(`
.revyl/ Directory Reference
============================

The .revyl/ directory contains configuration files for a Revyl-powered repository.
It is committed to version control (like .vscode/) so the team shares the same
service definitions.

Directory Structure
-------------------

  .revyl/
  ├── fleet.json          Worktree setup/teardown scripts (committed)
  ├── sessions.json       Service session definitions (committed)
  ├── config.yaml         Revyl CLI project config (committed)
  ├── tests/              Local test definitions (committed)
  ├── remote.json         Per-machine remote connection config (generated, gitignored)
  ├── shell-init.sh       Per-machine shell setup script (generated, gitignored)
  └── .services.pid       PIDs of running services from 'revyl services start' (generated)

sessions.json Format
--------------------

Uses the Terminal Keeper VS Code extension format (schema v11). This means
sessions.json is compatible with VS Code, Fleet Dashboard, and this CLI.

  {
    "active": "default",              // Session to launch by default
    "activateOnStartup": true,        // Terminal Keeper: auto-launch on VS Code start
    "keepExistingTerminals": false,    // Terminal Keeper: kill existing terminals first
    "sessions": {
      "my-session": [                 // Session name → array of items
        {                             // Standalone terminal (JSON object)
          "name": "backend",
          "autoExecuteCommands": true, // true (default) = run commands; false = show only
          "icon": "database",          // VS Code codicon name
          "color": "terminal.ansiYellow",
          "focus": false,
          "commands": [
            "cd backend",
            "source ../.venv/bin/activate",
            "python main.py"
          ]
        },
        [                             // Split group (JSON array of objects)
          { "name": "worker-1", "commands": ["cd workers", "python run.py --id=1"] },
          { "name": "worker-2", "commands": ["cd workers", "python run.py --id=2"] }
        ]
      ]
    }
  }

Terminal Definition Fields
--------------------------

  name                 (required) Display name for the terminal tab
  commands             (required) Array of shell commands to run sequentially
  autoExecuteCommands  (optional) Whether to auto-run commands. Default: true
  icon                 (optional) VS Code codicon name (e.g. "server", "database")
  color                (optional) Tab color (e.g. "terminal.ansiGreen")
  focus                (optional) Whether to focus this tab when created

Split Groups
------------

Terminals inside a JSON array (instead of a standalone object) form a split group.
In VS Code, they appear as split panes in a single panel. In Fleet Dashboard and
the CLI, each terminal in the group runs as a separate tab/process.

How Each Tool Uses sessions.json
---------------------------------

  VS Code (Terminal Keeper):
    Reads from .vscode/sessions.json (can symlink to .revyl/sessions.json).
    Creates split panes for groups, standalone terminals for objects.

  Fleet Dashboard:
    Reads from .revyl/sessions.json via readSessionConfig().
    Creates Ghostty terminal tabs. For remote/synced worktrees, the shell
    hook auto-SSHes and commands run on the remote machine.

  Revyl CLI:
    'revyl services list'   — Show available sessions and terminal counts
    'revyl services start'  — Spawn all terminals as parallel processes
    'revyl services stop'   — Stop processes via PID file

fleet.json Format
-----------------

  {
    "scripts": {
      "setup": "./scripts/worktree-setup.sh",     // Run after creating a worktree
      "teardown": "./scripts/worktree-teardown.sh" // Run before removing a worktree
    }
  }

Adding a New Session
--------------------

  1. Open .revyl/sessions.json
  2. Add a new key under "sessions" with an array of terminal definitions
  3. Set "active" to your new session name to make it the default

Adding a Terminal to an Existing Session
-----------------------------------------

  1. Open .revyl/sessions.json
  2. Find the session you want to modify
  3. Add a new object to the session array (or inside an existing split group)

Committed vs Generated Files
-----------------------------

  COMMITTED (shared with team):
    .revyl/fleet.json
    .revyl/sessions.json
    .revyl/config.yaml
    .revyl/tests/*.yaml

  GENERATED (per-machine, gitignored):
    .revyl/remote.json      — Created by Fleet Dashboard for rshell auto-connect
    .revyl/shell-init.sh    — Created by Fleet Dashboard for remote shell setup
    .revyl/.services.pid    — Created by 'revyl services start' for process tracking

`)
}
