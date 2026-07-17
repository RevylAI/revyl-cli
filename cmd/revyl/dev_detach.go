package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/ui"
)

// devDetachedEnv marks a re-exec'd background dev loop process. The child
// behaves like a normal foreground loop except it never opens a browser and
// its stdin is not a TTY (so keybinds are disabled automatically).
const devDetachedEnv = "REVYL_DEV_DETACHED"

const (
	devDetachContextFile   = "detach.log"
	devDetachReadyTimeout  = 5 * time.Minute
	devDetachPollInterval  = 500 * time.Millisecond
	devDetachLogTailOnFail = 40
)

var openDetachedDevBrowser = ui.OpenBrowser

func isDetachedDevChild() bool {
	return os.Getenv(devDetachedEnv) == "1"
}

// devDetachHandshake is the JSON printed by `revyl dev --detach --json` once
// the background loop has a live device session. The build may still be
// running; poll `revyl dev status` for build progress and install completion.
type devDetachHandshake struct {
	Context      string            `json:"context"`
	State        string            `json:"state"`
	PID          int               `json:"pid"`
	Platform     string            `json:"platform,omitempty"`
	SessionID    string            `json:"session_id,omitempty"`
	SessionIndex int               `json:"session_index"`
	ViewerURL    string            `json:"viewer_url,omitempty"`
	LogPath      string            `json:"log_path,omitempty"`
	Build        *devRebuildInfo   `json:"build,omitempty"`
	AuthBypass   *authBypassStatus `json:"auth_bypass,omitempty"`
	// SeededVersion / InstalledSeed surface a prior build installed immediately
	// (revyl dev --remote --seed-latest) so consumers know the app is already
	// interactive while the fresh build is still compiling.
	SeededVersion string `json:"seeded_version,omitempty"`
	InstalledSeed bool   `json:"installed_seed,omitempty"`
	// OpenedBrowser reports whether the CLI opened the live viewer in the
	// user's browser. When false (headless VM, --no-open), whoever consumes
	// this handshake should present viewer_url as a clickable link instead.
	OpenedBrowser bool `json:"opened_browser"`
}

// shouldAutoOpenViewer reports whether the detach parent can open the live viewer.
//
// Parameters:
//   - cmd: Active parent command containing explicit flag state
//   - cwd: Project root containing .revyl/config.yaml
//
// Returns:
//   - bool: Whether browser opening is enabled and supported by the environment
func shouldAutoOpenViewer(cmd *cobra.Command, cwd string) bool {
	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	if !effectiveDevOpenBrowser(cmd, configPath) {
		return false
	}
	if os.Getenv("CI") != "" || os.Getenv("SSH_CONNECTION") != "" {
		return false
	}
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return false
	}
	return true
}

// filterDetachArgs strips flags that must not propagate to the background
// child: --detach (would recurse), --json (child logs human output), and
// --open (child cannot open a browser).
func filterDetachArgs(args []string) []string {
	filtered := make([]string, 0, len(args))
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		switch {
		case arg == "--detach" || arg == "--json" || arg == "--open":
			continue
		case arg == "--open=true" || arg == "--open=false" ||
			strings.HasPrefix(arg, "--detach=") || strings.HasPrefix(arg, "--json="):
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered
}

func devDetachLogPath(cwd string) string {
	return filepath.Join(cwd, ".revyl", devContextsDir, devDetachContextFile)
}

// spawnDetachedDevLoop re-execs the current invocation as a background
// process, waits for it to publish a live device session, and prints a
// machine-readable handshake. Returns once the session is ready (build may
// still be running in the background).
func spawnDetachedDevLoop(cmd *cobra.Command, cwd string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve executable for --detach: %w", err)
	}

	logPath := devDetachLogPath(cwd)
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return fmt.Errorf("failed to create dev context directory: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open detach log %s: %w", logPath, err)
	}
	defer logFile.Close()
	_, _ = fmt.Fprintf(logFile, "\n--- revyl dev detached %s ---\n", time.Now().UTC().Format(time.RFC3339))

	args := append(filterDetachArgs(os.Args[1:]), "--no-open")
	child := exec.Command(exe, args...)
	child.Dir = cwd
	child.Env = append(os.Environ(), devDetachedEnv+"=1")
	child.Stdout = logFile
	child.Stderr = logFile
	configureDetachedDevCommand(child)
	if err := child.Start(); err != nil {
		return fmt.Errorf("failed to start background dev loop: %w", err)
	}
	childPID := child.Process.Pid

	exitCh := make(chan error, 1)
	go func() { exitCh <- child.Wait() }()

	if !devStartJSON {
		ui.PrintInfo("Dev loop starting in background (pid %d, log %s)", childPID, logPath)
	}

	deadline := time.Now().Add(devDetachReadyTimeout)
	for {
		select {
		case exitErr := <-exitCh:
			rootCause := detachRootCauseFromLog(logPath)
			if !devStartJSON {
				printDetachLogTail(logPath)
			}
			message := fmt.Sprintf("background dev loop exited before it was ready (see %s)", logPath)
			if rootCause != "" {
				message = rootCause
			} else if exitErr != nil {
				message = fmt.Sprintf("background dev loop exited before it was ready: %v", exitErr)
			}
			return emitDetachFailure(cwd, "detach_failed", message, logPath)
		default:
		}
		if time.Now().After(deadline) {
			message := fmt.Sprintf("timed out after %v waiting for the background dev loop (pid %d)", devDetachReadyTimeout, childPID)
			if rootCause := detachRootCauseFromLog(logPath); rootCause != "" {
				message = fmt.Sprintf("%s; last error: %s", message, rootCause)
			}
			return emitDetachFailure(cwd, "detach_timeout", message, logPath)
		}

		if devCtx := findDevContextByPID(cwd, childPID); devCtx != nil && devCtx.SessionID != "" {
			printDetachHandshake(cmd, cwd, devCtx, logPath)
			return nil
		}
		time.Sleep(devDetachPollInterval)
	}
}

// emitDetachFailure reports a detach failure. In --json mode it emits a
// structured object on stdout (with the log tail so the agent never has to
// find the internal detach log); the returned error keeps a non-zero exit.
func emitDetachFailure(cwd, code, message, logPath string) error {
	if devStartJSON {
		payload := map[string]interface{}{
			"ok":       false,
			"code":     code,
			"message":  message,
			"log_path": logPath,
			"log_tail": detachLogTailLines(logPath, devDetachLogTailOnFail),
		}
		data, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Println(string(data))
	}
	return fmt.Errorf("%s", message)
}

// detachRootCauseFromLog extracts the last error-looking line from the detach
// log so the real failure surfaces in CLI output instead of only in the file.
func detachRootCauseFromLog(logPath string) string {
	lines := detachLogTailLines(logPath, devDetachLogTailOnFail)
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		lower := strings.ToLower(line)
		if strings.HasPrefix(line, "✗") || strings.HasPrefix(lower, "error") ||
			strings.Contains(lower, "error:") || strings.Contains(lower, "failed") {
			return strings.TrimSpace(strings.TrimPrefix(line, "✗"))
		}
	}
	return ""
}

func detachLogTailLines(logPath string, max int) []string {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > max {
		lines = lines[len(lines)-max:]
	}
	return lines
}

// findDevContextByPID locates the dev context owned by a specific process.
func findDevContextByPID(cwd string, pid int) *DevContext {
	contexts, err := listDevContexts(cwd)
	if err != nil {
		return nil
	}
	for _, ctx := range contexts {
		if ctx != nil && ctx.PID == pid && ctx.State == devContextStateRunning {
			return ctx
		}
	}
	return nil
}

func printDetachHandshake(cmd *cobra.Command, cwd string, devCtx *DevContext, logPath string) {
	handshake := devDetachHandshake{
		Context:      devCtx.Name,
		State:        "ready",
		PID:          devCtx.PID,
		Platform:     devCtx.Platform,
		SessionID:    devCtx.SessionID,
		SessionIndex: devCtx.SessionIndex,
		ViewerURL:    devCtx.ViewerURL,
		LogPath:      logPath,
	}

	if data, err := os.ReadFile(devCtxStatusPath(cwd, devCtx.Name)); err == nil {
		var ds devStatus
		if json.Unmarshal(data, &ds) == nil {
			handshake.Build = ds.LastRebuild
			handshake.AuthBypass = ds.AuthBypass
			handshake.SeededVersion = ds.SeededVersion
			handshake.InstalledSeed = ds.InstalledSeed
			if ds.State == "building" {
				handshake.State = "building"
			}
		}
	}

	// The live viewer IS the product moment: pop it for the user unless they
	// opted out or there is no display to reach them on. Best-effort only.
	if handshake.ViewerURL != "" && shouldAutoOpenViewer(cmd, cwd) {
		if err := openDetachedDevBrowser(handshake.ViewerURL); err == nil {
			handshake.OpenedBrowser = true
		}
	}

	if devStartJSON {
		data, _ := json.MarshalIndent(handshake, "", "  ")
		fmt.Println(string(data))
		return
	}

	ui.PrintSuccess("Dev loop running in background (context %s, pid %d)", handshake.Context, handshake.PID)
	if handshake.ViewerURL != "" {
		ui.PrintLink("Live View", handshake.ViewerURL)
		if handshake.OpenedBrowser {
			ui.PrintDim("Opened the live viewer in your browser (--no-open to disable)")
		}
	}
	if handshake.InstalledSeed {
		ui.PrintInfo("Seeded build %s is on the device now; the fresh build will hot-swap when it lands", strings.TrimSpace(handshake.SeededVersion))
	}
	if handshake.Build != nil && devCockpitRebuildRunningStatus(handshake.Build.Status) {
		ui.PrintInfo("Build in progress — watch with `revyl dev status` or `revyl dev logs --build --follow`")
	}
	ui.PrintInfo("Stop with `revyl dev stop`")
}

func printDetachLogTail(logPath string) {
	lines := detachLogTailLines(logPath, devDetachLogTailOnFail)
	if len(lines) == 0 {
		return
	}
	ui.PrintDim("%s", strings.Join(lines, "\n"))
}
