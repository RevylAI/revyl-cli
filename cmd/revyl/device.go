package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/config"
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/ui"
)

// getDeviceSessionMgr creates an authenticated DeviceSessionManager for CLI use.
// Loads persisted sessions from disk and syncs with the backend.
func getDeviceSessionMgr(cmd *cobra.Command) (*mcppkg.DeviceSessionManager, error) {
	apiKey := os.Getenv("REVYL_API_KEY")
	if apiKey == "" {
		mgr := auth.NewManager()
		creds, err := mgr.GetCredentials()
		if err != nil || creds == nil || creds.APIKey == "" {
			return nil, fmt.Errorf("not authenticated: set REVYL_API_KEY or run 'revyl auth login'")
		}
		apiKey = creds.APIKey
	}

	devMode, _ := cmd.Flags().GetBool("dev")

	// Resolve workDir by walking up to find .revyl/ directory.
	// Falls back to cwd if no .revyl/ ancestor exists (e.g., first run).
	workDir, _ := os.Getwd()
	if repoRoot, err := config.FindRepoRoot(workDir); err == nil {
		workDir = repoRoot
	}

	client := api.NewClientWithDevMode(apiKey, devMode)
	api.SetDefaultVersion(version)
	sessionMgr := mcppkg.NewDeviceSessionManager(client, workDir)

	// Sync with backend to discover sessions from other clients.
	// Non-fatal: if sync fails, we still have local cache.
	if syncErr := sessionMgr.SyncSessions(cmd.Context()); syncErr != nil {
		ui.PrintDebug("session sync: %v", syncErr)
		// Fall back to local cache
		sessionMgr.LoadPersistedSession()
	}

	return sessionMgr, nil
}

// resolveSessionFlag reads the -s flag and resolves a session.
// Returns the resolved session. Pass -1 (flag default) for auto-resolution.
func resolveSessionFlag(cmd *cobra.Command, mgr *mcppkg.DeviceSessionManager) (*mcppkg.DeviceSession, error) {
	sidx, _ := cmd.Flags().GetInt("s")
	return mgr.ResolveSession(sidx)
}

// resolveTargetOrCoords checks whether --target was provided or --x/--y were
// explicitly set. Uses cobra's Changed() to distinguish "not provided" from 0.
func resolveTargetOrCoords(cmd *cobra.Command, mgr *mcppkg.DeviceSessionManager, sessionIndex int) (int, int, error) {
	target, _ := cmd.Flags().GetString("target")
	xChanged := cmd.Flags().Changed("x")
	yChanged := cmd.Flags().Changed("y")

	if target != "" && (xChanged || yChanged) {
		return 0, 0, fmt.Errorf("provide --target OR --x/--y, not both")
	}
	if target == "" && !xChanged && !yChanged {
		return 0, 0, fmt.Errorf("provide --target (element description) or --x/--y (coordinates)")
	}
	if (xChanged && !yChanged) || (!xChanged && yChanged) {
		return 0, 0, fmt.Errorf("both --x and --y are required when using coordinates")
	}

	if target != "" {
		resolved, err := mgr.ResolveTargetForSession(cmd.Context(), sessionIndex, target)
		if err != nil {
			return 0, 0, err
		}
		ui.PrintInfo("Resolved '%s' -> (%d, %d)", target, resolved.X, resolved.Y)
		return resolved.X, resolved.Y, nil
	}

	x, _ := cmd.Flags().GetInt("x")
	y, _ := cmd.Flags().GetInt("y")
	return x, y, nil
}

// jsonOrPrint outputs result as JSON if --json flag is set, otherwise prints the message.
func jsonOrPrint(cmd *cobra.Command, v interface{}, fallbackMsg string) {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if jsonOutput {
		data, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(data))
	} else {
		ui.PrintInfo("%s", fallbackMsg)
	}
}

var deviceCmd = &cobra.Command{
	Use:   "device",
	Short: "Direct device interaction (start, tap, type, screenshot, etc.)",
	Long:  "Provision cloud-hosted Android/iOS devices and interact with them directly.",
}

var deviceStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a device session",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		platform, _ := cmd.Flags().GetString("platform")
		timeout, _ := cmd.Flags().GetInt("timeout")
		openBrowser, _ := cmd.Flags().GetBool("open")
		jsonOutput, _ := cmd.Flags().GetBool("json")
		if platform == "" {
			return fmt.Errorf("--platform is required (ios or android)")
		}

		// Create a cancellable context so Ctrl+C during provisioning triggers
		// cleanup (CancelDevice on the backend) instead of orphaning the device.
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigChan)

		go func() {
			select {
			case <-sigChan:
				if !jsonOutput {
					ui.ClearLine()
					fmt.Fprintln(os.Stderr, "\nCancelling device provisioning...")
				}
				cancel()
			case <-ctx.Done():
			}
		}()

		// Show spinner while provisioning (skip in JSON mode)
		done := make(chan struct{})
		if !jsonOutput {
			go func() {
				spinner := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
				i := 0
				for {
					select {
					case <-done:
						ui.ClearLine()
						return
					case <-time.After(100 * time.Millisecond):
						fmt.Fprintf(os.Stderr, "\r%s Provisioning %s device...", spinner[i%len(spinner)], platform)
						i++
					}
				}
			}()
		}

		_, session, err := mgr.StartSession(ctx, platform, "", "", "", "", time.Duration(timeout)*time.Second)
		close(done)
		if err != nil {
			return err
		}

		if jsonOutput {
			data, _ := json.MarshalIndent(session, "", "  ")
			fmt.Println(string(data))
		} else {
			ui.PrintSuccess("Device ready! Session %d (%s)", session.Index, platform)
			ui.PrintLink("Session", session.SessionID)
			ui.PrintLink("Watch live", session.ViewerURL)
			ui.PrintNextSteps([]ui.NextStep{
				{Label: "Open in browser", Command: session.ViewerURL},
				{Label: "Take a screenshot", Command: "revyl device screenshot --out screen.png"},
				{Label: "Stop when done", Command: fmt.Sprintf("revyl device stop -s %d", session.Index)},
			})
		}

		// Auto-open browser if --open flag is set
		if openBrowser {
			_ = ui.OpenBrowser(session.ViewerURL)
		}

		return nil
	},
}

var deviceStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop a device session (-s <index> or --all)",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}

		all, _ := cmd.Flags().GetBool("all")
		if all {
			if err := mgr.StopAllSessions(cmd.Context()); err != nil {
				ui.PrintWarning("Some sessions had issues: %v", err)
			}
			jsonOrPrint(cmd, map[string]bool{"stopped_all": true}, "All sessions stopped.")
			return nil
		}

		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		sessionID := session.SessionID
		idx := session.Index
		ui.PrintInfo("Stopping session %d (%s)...", idx, sessionID)

		cancelErr := mgr.StopSession(cmd.Context(), idx)
		if cancelErr != nil {
			jsonOrPrint(cmd, map[string]interface{}{"stopped": true, "warning": cancelErr.Error()},
				"Device session stopped locally.")
			ui.PrintWarning("%v", cancelErr)
			return nil
		}
		jsonOrPrint(cmd, map[string]bool{"stopped": true}, "Device session stopped.")
		return nil
	},
}

var deviceScreenshotCmd = &cobra.Command{
	Use:   "screenshot",
	Short: "Capture device screenshot",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		imgBytes, err := mgr.ScreenshotForSession(cmd.Context(), session.Index)
		if err != nil {
			return err
		}
		out, _ := cmd.Flags().GetString("out")
		if out != "" {
			if err := os.WriteFile(out, imgBytes, 0o644); err != nil {
				return err
			}
			jsonOrPrint(cmd, map[string]string{"path": out, "bytes": fmt.Sprintf("%d", len(imgBytes))}, fmt.Sprintf("Screenshot saved: %s", out))
		} else {
			jsonOrPrint(cmd, map[string]int{"bytes": len(imgBytes)}, fmt.Sprintf("Screenshot captured (%d bytes). Use --out <path> to save.", len(imgBytes)))
		}
		return nil
	},
}

var deviceTapCmd = &cobra.Command{
	Use:   "tap",
	Short: "Tap an element (--target or --x/--y)",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		x, y, err := resolveTargetOrCoords(cmd, mgr, session.Index)
		if err != nil {
			return err
		}
		body := map[string]int{"x": x, "y": y}
		_, err = mgr.WorkerRequestForSession(cmd.Context(), session.Index, "POST", "/tap", body)
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, map[string]int{"x": x, "y": y}, fmt.Sprintf("Tapped (%d, %d)", x, y))
		return nil
	},
}

var deviceDoubleTapCmd = &cobra.Command{
	Use:   "double-tap",
	Short: "Double-tap an element (--target or --x/--y)",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		x, y, err := resolveTargetOrCoords(cmd, mgr, session.Index)
		if err != nil {
			return err
		}
		body := map[string]int{"x": x, "y": y}
		_, err = mgr.WorkerRequestForSession(cmd.Context(), session.Index, "POST", "/double_tap", body)
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, map[string]int{"x": x, "y": y}, fmt.Sprintf("Double-tapped (%d, %d)", x, y))
		return nil
	},
}

var deviceLongPressCmd = &cobra.Command{
	Use:   "long-press",
	Short: "Long press an element (--target or --x/--y, --duration)",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		x, y, err := resolveTargetOrCoords(cmd, mgr, session.Index)
		if err != nil {
			return err
		}
		dur, _ := cmd.Flags().GetInt("duration")
		if dur == 0 {
			dur = 1500
		}
		body := map[string]int{"x": x, "y": y, "duration_ms": dur}
		_, err = mgr.WorkerRequestForSession(cmd.Context(), session.Index, "POST", "/longpress", body)
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, map[string]int{"x": x, "y": y, "duration_ms": dur}, fmt.Sprintf("Long-pressed (%d, %d) for %dms", x, y, dur))
		return nil
	},
}

var deviceTypeCmd = &cobra.Command{
	Use:   "type",
	Short: "Type text (--target or --x/--y, plus --text)",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		text, _ := cmd.Flags().GetString("text")
		if text == "" {
			return fmt.Errorf("--text is required")
		}
		x, y, err := resolveTargetOrCoords(cmd, mgr, session.Index)
		if err != nil {
			return err
		}
		clearFirst, _ := cmd.Flags().GetBool("clear-first")
		body := map[string]interface{}{"x": x, "y": y, "text": text, "clear_first": clearFirst}
		_, err = mgr.WorkerRequestForSession(cmd.Context(), session.Index, "POST", "/input", body)
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, map[string]interface{}{"x": x, "y": y, "text": text}, fmt.Sprintf("Typed '%s' at (%d, %d)", text, x, y))
		return nil
	},
}

var deviceSwipeCmd = &cobra.Command{
	Use:   "swipe",
	Short: "Swipe (--target or --x/--y, plus --direction)",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		direction, _ := cmd.Flags().GetString("direction")
		if direction == "" {
			return fmt.Errorf("--direction is required (up, down, left, right)")
		}
		x, y, err := resolveTargetOrCoords(cmd, mgr, session.Index)
		if err != nil {
			return err
		}
		dur, _ := cmd.Flags().GetInt("duration")
		if dur == 0 {
			dur = 500
		}
		body := map[string]interface{}{"x": x, "y": y, "direction": direction, "duration_ms": dur}
		_, err = mgr.WorkerRequestForSession(cmd.Context(), session.Index, "POST", "/swipe", body)
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, map[string]interface{}{"x": x, "y": y, "direction": direction}, fmt.Sprintf("Swiped %s from (%d, %d)", direction, x, y))
		return nil
	},
}

var deviceDragCmd = &cobra.Command{
	Use:   "drag",
	Short: "Drag from one point to another (--start-x/--start-y/--end-x/--end-y)",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		sx, _ := cmd.Flags().GetInt("start-x")
		sy, _ := cmd.Flags().GetInt("start-y")
		ex, _ := cmd.Flags().GetInt("end-x")
		ey, _ := cmd.Flags().GetInt("end-y")
		body := map[string]int{"start_x": sx, "start_y": sy, "end_x": ex, "end_y": ey}
		_, err = mgr.WorkerRequestForSession(cmd.Context(), session.Index, "POST", "/drag", body)
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, body, fmt.Sprintf("Dragged (%d,%d) -> (%d,%d)", sx, sy, ex, ey))
		return nil
	},
}

var deviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install an app from a URL",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		appURL, _ := cmd.Flags().GetString("app-url")
		bundleID, _ := cmd.Flags().GetString("bundle-id")
		if appURL == "" {
			return fmt.Errorf("--app-url is required (URL to .apk or .ipa)")
		}
		body := map[string]string{"app_url": appURL}
		if bundleID != "" {
			body["bundle_id"] = bundleID
		}
		_, err = mgr.WorkerRequestForSession(cmd.Context(), session.Index, "POST", "/install", body)
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, map[string]string{"app_url": appURL, "status": "installed"}, fmt.Sprintf("Installed from %s", appURL))
		return nil
	},
}

var deviceLaunchCmd = &cobra.Command{
	Use:   "launch",
	Short: "Launch an installed app by bundle ID",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		bundleID, _ := cmd.Flags().GetString("bundle-id")
		if bundleID == "" {
			return fmt.Errorf("--bundle-id is required (e.g. 'com.example.app')")
		}
		body := map[string]string{"bundle_id": bundleID}
		_, err = mgr.WorkerRequestForSession(cmd.Context(), session.Index, "POST", "/launch", body)
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, map[string]string{"bundle_id": bundleID, "status": "launched"}, fmt.Sprintf("Launched %s", bundleID))
		return nil
	},
}

var deviceFindCmd = &cobra.Command{
	Use:   "find [target]",
	Short: "Find an element by description (returns coordinates)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		resolved, err := mgr.ResolveTargetForSession(cmd.Context(), session.Index, args[0])
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, resolved, fmt.Sprintf("Found: x=%d, y=%d", resolved.X, resolved.Y))
		return nil
	},
}

var deviceInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show session info (-s <index> for specific session)",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			jsonOrPrint(cmd, map[string]interface{}{"active": false, "total_sessions": mgr.SessionCount()}, "No active device session.")
			return nil
		}
		jsonOrPrint(cmd, session, fmt.Sprintf("Session %d: %s\nPlatform: %s\nViewer: %s\nUptime: %.0fs",
			session.Index, session.SessionID, session.Platform, session.ViewerURL, time.Since(session.StartedAt).Seconds()))
		return nil
	},
}

var deviceDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run diagnostics on auth, session, worker, and grounding health",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			// Even if auth fails, show that as a doctor finding
			ui.PrintInfo("Auth check: FAIL (%s)", err.Error())
			return nil
		}

		session, resolveErr := resolveSessionFlag(cmd, mgr)
		if resolveErr != nil || session == nil {
			total := mgr.SessionCount()
			if total == 0 {
				ui.PrintInfo("Session: NONE (no active session)")
			} else {
				ui.PrintInfo("Session: could not resolve (%s). %d session(s) exist.", resolveErr.Error(), total)
			}
		} else {
			ui.PrintInfo("Session %d: PASS (platform=%s, uptime=%.0fs)", session.Index, session.Platform, time.Since(session.StartedAt).Seconds())
			respBytes, werr := mgr.WorkerRequestForSession(cmd.Context(), session.Index, "GET", "/health", nil)
			if werr != nil {
				ui.PrintInfo("Worker: FAIL (%s)", werr.Error())
			} else {
				ui.PrintInfo("Worker: PASS")
				var health struct {
					DeviceConnected bool `json:"device_connected"`
				}
				if json.Unmarshal(respBytes, &health) == nil {
					if health.DeviceConnected {
						ui.PrintInfo("Device: PASS")
					} else {
						ui.PrintInfo("Device: FAIL (device not connected)")
					}
				}
			}
		}
		ui.PrintInfo("Auth: PASS")

		// Show all sessions summary
		sessions := mgr.ListSessions()
		if len(sessions) > 0 {
			ui.PrintInfo("Active sessions: %d", len(sessions))
			for _, s := range sessions {
				marker := " "
				if s.Index == mgr.ActiveIndex() {
					marker = "*"
				}
				ui.PrintInfo("  %s%d  %s  %s  %.0fs", marker, s.Index, s.Platform, truncatePrefix(s.SessionID, 8), time.Since(s.StartedAt).Seconds())
			}
		}

		return nil
	},
}

var deviceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all active device sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}

		sessions := mgr.ListSessions()
		jsonOutput, _ := cmd.Flags().GetBool("json")

		if jsonOutput {
			data, _ := json.MarshalIndent(sessions, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		if len(sessions) == 0 {
			ui.PrintInfo("No active device sessions.")
			return nil
		}

		activeIdx := mgr.ActiveIndex()
		fmt.Printf("  %-3s %-10s %-10s %-12s %s\n", "#", "PLATFORM", "STATUS", "SESSION ID", "UPTIME")
		for _, s := range sessions {
			marker := " "
			if s.Index == activeIdx {
				marker = "*"
			}
			idShort := truncatePrefix(s.SessionID, 8)
			uptime := time.Since(s.StartedAt).Round(time.Second)
			fmt.Printf("%s %-3d %-10s %-10s %-12s %s\n", marker, s.Index, s.Platform, "running", idShort, uptime)
		}
		return nil
	},
}

var deviceUseCmd = &cobra.Command{
	Use:   "use <index>",
	Short: "Switch active session to the given index",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}

		var idx int
		if _, parseErr := fmt.Sscanf(args[0], "%d", &idx); parseErr != nil {
			return fmt.Errorf("invalid session index: %s (must be an integer)", args[0])
		}

		if err := mgr.SetActive(idx); err != nil {
			return err
		}

		session := mgr.GetSession(idx)
		if session != nil {
			ui.PrintSuccess("Switched to session %d (%s)", idx, session.Platform)
		} else {
			ui.PrintSuccess("Switched to session %d", idx)
		}
		return nil
	},
}

func init() {
	// Global -s flag for session selection (added to all action commands)
	sessionFlag := func(cmd *cobra.Command) {
		cmd.Flags().IntP("s", "s", -1, "Session index to target (-1 for active)")
	}

	// Start
	deviceStartCmd.Flags().String("platform", "", "Platform: ios or android (required)")
	deviceStartCmd.Flags().Int("timeout", 300, "Idle timeout in seconds")
	deviceStartCmd.Flags().Bool("open", false, "Open viewer in browser after device is ready")
	deviceStartCmd.Flags().Bool("json", false, "Output as JSON")

	// Stop
	deviceStopCmd.Flags().Bool("json", false, "Output as JSON")
	deviceStopCmd.Flags().Bool("all", false, "Stop all sessions")
	sessionFlag(deviceStopCmd)

	// Screenshot
	deviceScreenshotCmd.Flags().String("out", "", "Output file path")
	deviceScreenshotCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceScreenshotCmd)

	// Tap
	deviceTapCmd.Flags().String("target", "", "Element description (grounded)")
	deviceTapCmd.Flags().Int("x", 0, "X coordinate (raw)")
	deviceTapCmd.Flags().Int("y", 0, "Y coordinate (raw)")
	deviceTapCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceTapCmd)

	// Double Tap
	deviceDoubleTapCmd.Flags().String("target", "", "Element description (grounded)")
	deviceDoubleTapCmd.Flags().Int("x", 0, "X coordinate (raw)")
	deviceDoubleTapCmd.Flags().Int("y", 0, "Y coordinate (raw)")
	deviceDoubleTapCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceDoubleTapCmd)

	// Long Press
	deviceLongPressCmd.Flags().String("target", "", "Element description (grounded)")
	deviceLongPressCmd.Flags().Int("x", 0, "X coordinate (raw)")
	deviceLongPressCmd.Flags().Int("y", 0, "Y coordinate (raw)")
	deviceLongPressCmd.Flags().Int("duration", 1500, "Press duration in ms")
	deviceLongPressCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceLongPressCmd)

	// Type
	deviceTypeCmd.Flags().String("target", "", "Element description (grounded)")
	deviceTypeCmd.Flags().Int("x", 0, "X coordinate (raw)")
	deviceTypeCmd.Flags().Int("y", 0, "Y coordinate (raw)")
	deviceTypeCmd.Flags().String("text", "", "Text to type (required)")
	deviceTypeCmd.Flags().Bool("clear-first", true, "Clear field before typing")
	deviceTypeCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceTypeCmd)

	// Swipe
	deviceSwipeCmd.Flags().String("target", "", "Element description (grounded)")
	deviceSwipeCmd.Flags().Int("x", 0, "X coordinate (raw)")
	deviceSwipeCmd.Flags().Int("y", 0, "Y coordinate (raw)")
	deviceSwipeCmd.Flags().String("direction", "", "Direction: up, down, left, right (required)")
	deviceSwipeCmd.Flags().Int("duration", 500, "Swipe duration in ms")
	deviceSwipeCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceSwipeCmd)

	// Drag
	deviceDragCmd.Flags().Int("start-x", 0, "Starting X coordinate")
	deviceDragCmd.Flags().Int("start-y", 0, "Starting Y coordinate")
	deviceDragCmd.Flags().Int("end-x", 0, "Ending X coordinate")
	deviceDragCmd.Flags().Int("end-y", 0, "Ending Y coordinate")
	deviceDragCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceDragCmd)

	// Install
	deviceInstallCmd.Flags().String("app-url", "", "URL to download app from (required)")
	deviceInstallCmd.Flags().String("bundle-id", "", "Bundle ID (optional, auto-detected)")
	deviceInstallCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceInstallCmd)

	// Launch
	deviceLaunchCmd.Flags().String("bundle-id", "", "App bundle ID to launch (required)")
	deviceLaunchCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceLaunchCmd)

	// Find
	deviceFindCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceFindCmd)

	// Info
	deviceInfoCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceInfoCmd)

	// Doctor
	sessionFlag(deviceDoctorCmd)

	// List
	deviceListCmd.Flags().Bool("json", false, "Output as JSON")

	// Register subcommands
	deviceCmd.AddCommand(deviceStartCmd)
	deviceCmd.AddCommand(deviceStopCmd)
	deviceCmd.AddCommand(deviceScreenshotCmd)
	deviceCmd.AddCommand(deviceTapCmd)
	deviceCmd.AddCommand(deviceDoubleTapCmd)
	deviceCmd.AddCommand(deviceLongPressCmd)
	deviceCmd.AddCommand(deviceTypeCmd)
	deviceCmd.AddCommand(deviceSwipeCmd)
	deviceCmd.AddCommand(deviceDragCmd)
	deviceCmd.AddCommand(deviceInstallCmd)
	deviceCmd.AddCommand(deviceLaunchCmd)
	deviceCmd.AddCommand(deviceFindCmd)
	deviceCmd.AddCommand(deviceInfoCmd)
	deviceCmd.AddCommand(deviceDoctorCmd)
	deviceCmd.AddCommand(deviceListCmd)
	deviceCmd.AddCommand(deviceUseCmd)
}
