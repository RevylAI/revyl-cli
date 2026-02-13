package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/ui"
)

// getDeviceSessionMgr creates an authenticated DeviceSessionManager for CLI use.
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

	workDir, _ := os.Getwd()
	client := api.NewClient(apiKey)
	api.SetDefaultVersion(version)
	return mcppkg.NewDeviceSessionManager(client, workDir), nil
}

// resolveTargetOrCoords checks whether --target was provided or --x/--y were
// explicitly set. Uses cobra's Changed() to distinguish "not provided" from 0.
func resolveTargetOrCoords(cmd *cobra.Command, mgr *mcppkg.DeviceSessionManager) (int, int, error) {
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
		resolved, err := mgr.ResolveTarget(cmd.Context(), target)
		if err != nil {
			return 0, 0, err
		}
		ui.PrintInfo("Resolved '%s' -> (%d, %d) [confidence=%.2f]", target, resolved.X, resolved.Y, resolved.Confidence)
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
		if platform == "" {
			return fmt.Errorf("--platform is required (ios or android)")
		}
		ui.PrintInfo("Starting %s device...", platform)
		session, err := mgr.StartSession(cmd.Context(), platform, "", "", "", "", time.Duration(timeout)*time.Second)
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, session, fmt.Sprintf("Session: %s\nWatch live: %s", session.SessionID, session.ViewerURL))
		return nil
	},
}

var deviceStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the active device session",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		mgr.LoadPersistedSession()
		if err := mgr.StopSession(cmd.Context()); err != nil {
			return err
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
		mgr.LoadPersistedSession()
		imgBytes, err := mgr.Screenshot(cmd.Context())
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
		mgr.LoadPersistedSession()
		x, y, err := resolveTargetOrCoords(cmd, mgr)
		if err != nil {
			return err
		}
		body := map[string]int{"x": x, "y": y}
		_, err = mgr.WorkerRequest(cmd.Context(), "POST", "/tap", body)
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
		mgr.LoadPersistedSession()
		x, y, err := resolveTargetOrCoords(cmd, mgr)
		if err != nil {
			return err
		}
		body := map[string]int{"x": x, "y": y}
		_, err = mgr.WorkerRequest(cmd.Context(), "POST", "/double_tap", body)
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
		mgr.LoadPersistedSession()
		x, y, err := resolveTargetOrCoords(cmd, mgr)
		if err != nil {
			return err
		}
		dur, _ := cmd.Flags().GetInt("duration")
		if dur == 0 {
			dur = 1500
		}
		body := map[string]int{"x": x, "y": y, "duration_ms": dur}
		_, err = mgr.WorkerRequest(cmd.Context(), "POST", "/longpress", body)
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
		mgr.LoadPersistedSession()
		text, _ := cmd.Flags().GetString("text")
		if text == "" {
			return fmt.Errorf("--text is required")
		}
		x, y, err := resolveTargetOrCoords(cmd, mgr)
		if err != nil {
			return err
		}
		clearFirst, _ := cmd.Flags().GetBool("clear-first")
		body := map[string]interface{}{"x": x, "y": y, "text": text, "clear_first": clearFirst}
		_, err = mgr.WorkerRequest(cmd.Context(), "POST", "/input", body)
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
		mgr.LoadPersistedSession()
		direction, _ := cmd.Flags().GetString("direction")
		if direction == "" {
			return fmt.Errorf("--direction is required (up, down, left, right)")
		}
		x, y, err := resolveTargetOrCoords(cmd, mgr)
		if err != nil {
			return err
		}
		dur, _ := cmd.Flags().GetInt("duration")
		if dur == 0 {
			dur = 500
		}
		body := map[string]interface{}{"x": x, "y": y, "direction": direction, "duration_ms": dur}
		_, err = mgr.WorkerRequest(cmd.Context(), "POST", "/swipe", body)
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
		mgr.LoadPersistedSession()
		sx, _ := cmd.Flags().GetInt("start-x")
		sy, _ := cmd.Flags().GetInt("start-y")
		ex, _ := cmd.Flags().GetInt("end-x")
		ey, _ := cmd.Flags().GetInt("end-y")
		body := map[string]int{"start_x": sx, "start_y": sy, "end_x": ex, "end_y": ey}
		_, err = mgr.WorkerRequest(cmd.Context(), "POST", "/drag", body)
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
		mgr.LoadPersistedSession()
		appURL, _ := cmd.Flags().GetString("app-url")
		bundleID, _ := cmd.Flags().GetString("bundle-id")
		if appURL == "" {
			return fmt.Errorf("--app-url is required (URL to .apk or .ipa)")
		}
		body := map[string]string{"app_url": appURL}
		if bundleID != "" {
			body["bundle_id"] = bundleID
		}
		_, err = mgr.WorkerRequest(cmd.Context(), "POST", "/install", body)
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
		mgr.LoadPersistedSession()
		bundleID, _ := cmd.Flags().GetString("bundle-id")
		if bundleID == "" {
			return fmt.Errorf("--bundle-id is required (e.g. 'com.example.app')")
		}
		body := map[string]string{"bundle_id": bundleID}
		_, err = mgr.WorkerRequest(cmd.Context(), "POST", "/launch", body)
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
		mgr.LoadPersistedSession()
		resolved, err := mgr.ResolveTarget(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, resolved, fmt.Sprintf("Found: x=%d, y=%d (confidence=%.2f)", resolved.X, resolved.Y, resolved.Confidence))
		return nil
	},
}

var deviceInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show active session info",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session := mgr.LoadPersistedSession()
		if session == nil {
			jsonOrPrint(cmd, map[string]bool{"active": false}, "No active device session.")
			return nil
		}
		jsonOrPrint(cmd, session, fmt.Sprintf("Session: %s\nPlatform: %s\nViewer: %s\nUptime: %.0fs",
			session.SessionID, session.Platform, session.ViewerURL, time.Since(session.StartedAt).Seconds()))
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
		mgr.LoadPersistedSession()

		session := mgr.GetActive()
		if session != nil {
			ui.PrintInfo("Session: PASS (platform=%s, uptime=%.0fs)", session.Platform, time.Since(session.StartedAt).Seconds())
			_, werr := mgr.WorkerRequest(cmd.Context(), "GET", "/health", nil)
			if werr != nil {
				ui.PrintInfo("Worker: FAIL (%s)", werr.Error())
			} else {
				ui.PrintInfo("Worker: PASS")
			}
		} else {
			ui.PrintInfo("Session: NONE (no active session)")
		}
		ui.PrintInfo("Grounding URL: %s", mgr.GroundingURL())
		ui.PrintInfo("Auth: PASS")
		return nil
	},
}

func init() {
	// Start
	deviceStartCmd.Flags().String("platform", "", "Platform: ios or android (required)")
	deviceStartCmd.Flags().Int("timeout", 300, "Idle timeout in seconds")
	deviceStartCmd.Flags().Bool("json", false, "Output as JSON")

	// Stop
	deviceStopCmd.Flags().Bool("json", false, "Output as JSON")

	// Screenshot
	deviceScreenshotCmd.Flags().String("out", "", "Output file path")
	deviceScreenshotCmd.Flags().Bool("json", false, "Output as JSON")

	// Tap
	deviceTapCmd.Flags().String("target", "", "Element description (grounded)")
	deviceTapCmd.Flags().Int("x", 0, "X coordinate (raw)")
	deviceTapCmd.Flags().Int("y", 0, "Y coordinate (raw)")
	deviceTapCmd.Flags().Bool("json", false, "Output as JSON")

	// Double Tap
	deviceDoubleTapCmd.Flags().String("target", "", "Element description (grounded)")
	deviceDoubleTapCmd.Flags().Int("x", 0, "X coordinate (raw)")
	deviceDoubleTapCmd.Flags().Int("y", 0, "Y coordinate (raw)")
	deviceDoubleTapCmd.Flags().Bool("json", false, "Output as JSON")

	// Long Press
	deviceLongPressCmd.Flags().String("target", "", "Element description (grounded)")
	deviceLongPressCmd.Flags().Int("x", 0, "X coordinate (raw)")
	deviceLongPressCmd.Flags().Int("y", 0, "Y coordinate (raw)")
	deviceLongPressCmd.Flags().Int("duration", 1500, "Press duration in ms")
	deviceLongPressCmd.Flags().Bool("json", false, "Output as JSON")

	// Type
	deviceTypeCmd.Flags().String("target", "", "Element description (grounded)")
	deviceTypeCmd.Flags().Int("x", 0, "X coordinate (raw)")
	deviceTypeCmd.Flags().Int("y", 0, "Y coordinate (raw)")
	deviceTypeCmd.Flags().String("text", "", "Text to type (required)")
	deviceTypeCmd.Flags().Bool("clear-first", true, "Clear field before typing")
	deviceTypeCmd.Flags().Bool("json", false, "Output as JSON")

	// Swipe
	deviceSwipeCmd.Flags().String("target", "", "Element description (grounded)")
	deviceSwipeCmd.Flags().Int("x", 0, "X coordinate (raw)")
	deviceSwipeCmd.Flags().Int("y", 0, "Y coordinate (raw)")
	deviceSwipeCmd.Flags().String("direction", "", "Direction: up, down, left, right (required)")
	deviceSwipeCmd.Flags().Int("duration", 500, "Swipe duration in ms")
	deviceSwipeCmd.Flags().Bool("json", false, "Output as JSON")

	// Drag
	deviceDragCmd.Flags().Int("start-x", 0, "Starting X coordinate")
	deviceDragCmd.Flags().Int("start-y", 0, "Starting Y coordinate")
	deviceDragCmd.Flags().Int("end-x", 0, "Ending X coordinate")
	deviceDragCmd.Flags().Int("end-y", 0, "Ending Y coordinate")
	deviceDragCmd.Flags().Bool("json", false, "Output as JSON")

	// Install
	deviceInstallCmd.Flags().String("app-url", "", "URL to download app from (required)")
	deviceInstallCmd.Flags().String("bundle-id", "", "Bundle ID (optional, auto-detected)")
	deviceInstallCmd.Flags().Bool("json", false, "Output as JSON")

	// Launch
	deviceLaunchCmd.Flags().String("bundle-id", "", "App bundle ID to launch (required)")
	deviceLaunchCmd.Flags().Bool("json", false, "Output as JSON")

	// Find
	deviceFindCmd.Flags().Bool("json", false, "Output as JSON")

	// Info
	deviceInfoCmd.Flags().Bool("json", false, "Output as JSON")

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
}
