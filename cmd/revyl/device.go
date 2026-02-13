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
		ui.PrintInfo("Session: %s", session.SessionID)
		ui.PrintInfo("Watch live: %s", session.ViewerURL)
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
		ui.PrintInfo("Device session stopped.")
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
			ui.PrintInfo("Screenshot saved: %s", out)
		} else {
			ui.PrintInfo("Screenshot captured (%d bytes). Use --out <path> to save.", len(imgBytes))
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
		target, _ := cmd.Flags().GetString("target")
		x, _ := cmd.Flags().GetInt("x")
		y, _ := cmd.Flags().GetInt("y")

		if target != "" {
			resolved, err := mgr.ResolveTarget(cmd.Context(), target)
			if err != nil {
				return err
			}
			x, y = resolved.X, resolved.Y
			ui.PrintInfo("Resolved '%s' -> (%d, %d)", target, x, y)
		}
		body := map[string]int{"x": x, "y": y}
		_, err = mgr.WorkerRequest(cmd.Context(), "POST", "/tap", body)
		if err != nil {
			return err
		}
		ui.PrintInfo("Tapped (%d, %d)", x, y)
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
		target, _ := cmd.Flags().GetString("target")
		x, _ := cmd.Flags().GetInt("x")
		y, _ := cmd.Flags().GetInt("y")
		text, _ := cmd.Flags().GetString("text")

		if text == "" {
			return fmt.Errorf("--text is required")
		}
		if target != "" {
			resolved, err := mgr.ResolveTarget(cmd.Context(), target)
			if err != nil {
				return err
			}
			x, y = resolved.X, resolved.Y
		}
		body := map[string]interface{}{"x": x, "y": y, "text": text, "clear_first": true}
		_, err = mgr.WorkerRequest(cmd.Context(), "POST", "/input", body)
		if err != nil {
			return err
		}
		ui.PrintInfo("Typed '%s' at (%d, %d)", text, x, y)
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
		target, _ := cmd.Flags().GetString("target")
		x, _ := cmd.Flags().GetInt("x")
		y, _ := cmd.Flags().GetInt("y")
		direction, _ := cmd.Flags().GetString("direction")

		if direction == "" {
			return fmt.Errorf("--direction is required (up, down, left, right)")
		}
		if target != "" {
			resolved, err := mgr.ResolveTarget(cmd.Context(), target)
			if err != nil {
				return err
			}
			x, y = resolved.X, resolved.Y
		}
		body := map[string]interface{}{"x": x, "y": y, "direction": direction, "duration_ms": 500}
		_, err = mgr.WorkerRequest(cmd.Context(), "POST", "/swipe", body)
		if err != nil {
			return err
		}
		ui.PrintInfo("Swiped %s from (%d, %d)", direction, x, y)
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
		jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")
		if jsonOutput {
			data, _ := json.MarshalIndent(resolved, "", "  ")
			fmt.Println(string(data))
		} else {
			ui.PrintInfo("Found: x=%d, y=%d (confidence=%.2f)", resolved.X, resolved.Y, resolved.Confidence)
		}
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
			ui.PrintInfo("No active device session.")
			return nil
		}
		ui.PrintInfo("Session: %s", session.SessionID)
		ui.PrintInfo("Platform: %s", session.Platform)
		ui.PrintInfo("Viewer: %s", session.ViewerURL)
		ui.PrintInfo("Uptime: %.0fs", time.Since(session.StartedAt).Seconds())
		return nil
	},
}

func init() {
	// Start
	deviceStartCmd.Flags().String("platform", "", "Platform: ios or android (required)")
	deviceStartCmd.Flags().Int("timeout", 300, "Idle timeout in seconds")

	// Screenshot
	deviceScreenshotCmd.Flags().String("out", "", "Output file path")

	// Tap
	deviceTapCmd.Flags().String("target", "", "Element description (grounded)")
	deviceTapCmd.Flags().Int("x", 0, "X coordinate (raw)")
	deviceTapCmd.Flags().Int("y", 0, "Y coordinate (raw)")

	// Type
	deviceTypeCmd.Flags().String("target", "", "Element description (grounded)")
	deviceTypeCmd.Flags().Int("x", 0, "X coordinate (raw)")
	deviceTypeCmd.Flags().Int("y", 0, "Y coordinate (raw)")
	deviceTypeCmd.Flags().String("text", "", "Text to type (required)")

	// Swipe
	deviceSwipeCmd.Flags().String("target", "", "Element description (grounded)")
	deviceSwipeCmd.Flags().Int("x", 0, "X coordinate (raw)")
	deviceSwipeCmd.Flags().Int("y", 0, "Y coordinate (raw)")
	deviceSwipeCmd.Flags().String("direction", "", "Direction: up, down, left, right (required)")

	// Register subcommands
	deviceCmd.AddCommand(deviceStartCmd)
	deviceCmd.AddCommand(deviceStopCmd)
	deviceCmd.AddCommand(deviceScreenshotCmd)
	deviceCmd.AddCommand(deviceTapCmd)
	deviceCmd.AddCommand(deviceTypeCmd)
	deviceCmd.AddCommand(deviceSwipeCmd)
	deviceCmd.AddCommand(deviceFindCmd)
	deviceCmd.AddCommand(deviceInfoCmd)
}
