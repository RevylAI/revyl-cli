package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/config"
	startdevice "github.com/revyl/cli/internal/device"
	"github.com/revyl/cli/internal/devicetargets"
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
	sessionMgr.SetDevMode(devMode)

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
	session, err := mgr.ResolveSession(sidx)
	if err != nil {
		return nil, humanizeDeviceSessionResolveError(cmd, err)
	}
	return session, nil
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
		jsonOutput, _ := cmd.Flags().GetBool("json")
		if !jsonOutput {
			ui.PrintInfo("Resolved '%s' -> (%d, %d)", target, resolved.X, resolved.Y)
		}
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

// ActionResult is the enriched JSON output for device action commands (tap,
// double-tap, long-press, type, swipe, etc.). It includes the resolved
// coordinates, the target description (when AI-grounded), and worker-reported
// success/latency so that downstream consumers like Trailblaze can render
// click overlays without additional round-trips.
type ActionResult struct {
	Action     string      `json:"action"`
	X          int         `json:"x"`
	Y          int         `json:"y"`
	Target     string      `json:"target,omitempty"`
	Success    bool        `json:"success"`
	LatencyMs  json.Number `json:"latency_ms,omitempty"`
	DurationMs int         `json:"duration_ms,omitempty"`
	Text       string      `json:"text,omitempty"`
	Direction  string      `json:"direction,omitempty"`
	Scale      float64     `json:"scale,omitempty"`
	EndX       int         `json:"end_x,omitempty"`
	EndY       int         `json:"end_y,omitempty"`
}

// workerActionResponseFull extends the base workerActionResponse (in dev.go)
// with the latency field returned by the worker's ActionResponse.
type workerActionResponseFull struct {
	Success   *bool       `json:"success"`
	LatencyMs json.Number `json:"latency_ms"`
}

// buildActionResult creates an ActionResult by merging locally-known fields
// (coordinates, target, action name) with the worker's response body.
// If the worker body cannot be parsed, success defaults to true (the request
// did not error) and latency is omitted.
func buildActionResult(action string, x, y int, target string, workerBody []byte) ActionResult {
	result := ActionResult{
		Action:  action,
		X:       x,
		Y:       y,
		Target:  target,
		Success: true,
	}
	var wr workerActionResponseFull
	if err := json.Unmarshal(workerBody, &wr); err == nil {
		if wr.Success != nil {
			result.Success = *wr.Success
		}
		result.LatencyMs = wr.LatencyMs
	}
	return result
}

type liveStepOutputSummary struct {
	Status           string `json:"status"`
	StatusReason     string `json:"status_reason"`
	ValidationResult *bool  `json:"validation_result,omitempty"`
}

func formatLiveStepFallback(stepLabel string, response *mcppkg.LiveStepResponse) string {
	if response == nil {
		return fmt.Sprintf("%s step completed", stepLabel)
	}

	summary := liveStepOutputSummary{}
	if len(response.StepOutput) > 0 {
		_ = json.Unmarshal(response.StepOutput, &summary)
	}

	status := strings.TrimSpace(summary.Status)
	if status == "" {
		if response.Success {
			status = "success"
		} else {
			status = "failed"
		}
	}

	if summary.ValidationResult != nil {
		status = fmt.Sprintf("%s (validation=%t)", status, *summary.ValidationResult)
	}
	if strings.TrimSpace(summary.StatusReason) != "" && !response.Success {
		return fmt.Sprintf("%s step %s: %s", stepLabel, status, summary.StatusReason)
	}
	return fmt.Sprintf("%s step %s", stepLabel, status)
}

func formatDeviceInfoFallback(session *mcppkg.DeviceSession) string {
	if session == nil {
		return "No active device session."
	}

	lines := []string{
		fmt.Sprintf("Session %d: %s", session.Index, session.SessionID),
		fmt.Sprintf("Platform: %s", session.Platform),
		fmt.Sprintf("Viewer: %s", session.ViewerURL),
	}
	if session.ScreenWidth > 0 && session.ScreenHeight > 0 {
		lines = append(lines, fmt.Sprintf("Screen: %dx%d", session.ScreenWidth, session.ScreenHeight))
	}
	if session.WhepURL != nil && strings.TrimSpace(*session.WhepURL) != "" {
		lines = append(lines, fmt.Sprintf("WHEP: %s", strings.TrimSpace(*session.WhepURL)))
	}
	lines = append(lines, fmt.Sprintf("Uptime: %.0fs", time.Since(session.StartedAt).Seconds()))
	return strings.Join(lines, "\n")
}

func executeLiveStepCommand(cmd *cobra.Command, request mcppkg.LiveStepRequest, stepLabel string) error {
	mgr, err := getDeviceSessionMgr(cmd)
	if err != nil {
		return err
	}
	session, err := resolveSessionFlag(cmd, mgr)
	if err != nil {
		return err
	}

	response, err := mgr.ExecuteLiveStepForSession(cmd.Context(), session.Index, request)
	if err != nil {
		return err
	}
	jsonOrPrint(cmd, response, formatLiveStepFallback(stepLabel, response))
	return nil
}

func normalizeDeviceStartPlatform(raw string) (string, error) {
	platform := strings.ToLower(strings.TrimSpace(raw))
	if platform == "" {
		return "ios", nil
	}
	if platform != "ios" && platform != "android" {
		return "", fmt.Errorf("platform must be 'ios' or 'android'")
	}
	return platform, nil
}

// normalizeOptionalDeviceFlagValue trims a CLI flag value and preserves empties.
func normalizeOptionalDeviceFlagValue(raw string) string {
	return strings.TrimSpace(raw)
}

// normalizeDeviceStartArtifactFlags trims device-start artifact selectors and
// ensures callers choose exactly zero or one of them.
func normalizeDeviceStartArtifactFlags(appID, buildVersionID, appURL string) (string, string, string, error) {
	normalizedAppID := normalizeOptionalDeviceFlagValue(appID)
	normalizedBuildVersionID := normalizeOptionalDeviceFlagValue(buildVersionID)
	normalizedAppURL := normalizeOptionalDeviceFlagValue(appURL)

	provided := 0
	for _, candidate := range []string{normalizedAppID, normalizedBuildVersionID, normalizedAppURL} {
		if candidate != "" {
			provided++
		}
	}
	if provided > 1 {
		return "", "", "", fmt.Errorf("provide only one of --app-id, --build-version-id, or --app-url")
	}
	return normalizedAppID, normalizedBuildVersionID, normalizedAppURL, nil
}

// normalizeRequiredDeviceURLFlag trims a required URL flag and returns a
// user-facing error when the resulting value is empty.
func normalizeRequiredDeviceURLFlag(rawValue, flagName, usage string) (string, error) {
	value := normalizeOptionalDeviceFlagValue(rawValue)
	if value != "" {
		return value, nil
	}
	if usage == "" {
		return "", fmt.Errorf("%s is required", flagName)
	}
	return "", fmt.Errorf("%s is required (%s)", flagName, usage)
}

func deviceCommandPrefix(cmd *cobra.Command) string {
	devMode, _ := cmd.Flags().GetBool("dev")
	if devMode {
		return "revyl --dev"
	}
	return "revyl"
}

func humanizeDeviceSessionResolveError(cmd *cobra.Command, err error) error {
	if err == nil {
		return nil
	}

	msg := strings.TrimSpace(err.Error())
	cmdPrefix := deviceCommandPrefix(cmd)

	if strings.Contains(msg, "multiple sessions active") {
		return fmt.Errorf("multiple sessions active. Specify -s <index> or run '%s device list' to see active sessions", cmdPrefix)
	}

	msg = strings.ReplaceAll(msg,
		"Call list_device_sessions() to see active sessions",
		fmt.Sprintf("Run '%s device list' to see active sessions", cmdPrefix),
	)
	msg = strings.ReplaceAll(msg,
		"call list_device_sessions() to see them",
		fmt.Sprintf("run '%s device list' to see active sessions", cmdPrefix),
	)
	msg = strings.ReplaceAll(msg,
		"Start one with start_device_session(platform='ios') or start_device_session(platform='android')",
		fmt.Sprintf("Start one with '%s device start'", cmdPrefix),
	)

	return fmt.Errorf("%s", msg)
}

var deviceCmd = &cobra.Command{
	Use:   "device",
	Short: "Cloud device sessions and actions (start, tap, type, screenshot, etc.)",
	Long: `Provision cloud-hosted Android/iOS devices and interact with them directly.

Device sessions are the foundation for all device interaction. Use 'revyl dev'
to layer a local development loop (hot reload, rebuild, tunnel) on top of a
device session.

Examples:
  revyl device start --platform android --open
  revyl device tap --target "Sign In"
  revyl device screenshot --out screen.png
  revyl device instruction "log in with test@example.com and verify the dashboard loads"`,
}

var deviceStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a device session",
	Example: `  revyl device start --platform ios
  revyl device start --platform android --timeout 600
  revyl device start --platform ios --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		platform, _ := cmd.Flags().GetString("platform")
		timeout, _ := cmd.Flags().GetInt("timeout")
		openBrowser, _ := cmd.Flags().GetBool("open")
		appID, _ := cmd.Flags().GetString("app-id")
		buildVersionID, _ := cmd.Flags().GetString("build-version-id")
		appURL, _ := cmd.Flags().GetString("app-url")
		appLink, _ := cmd.Flags().GetString("app-link")
		launchVars, _ := cmd.Flags().GetStringArray("launch-var")
		jsonOutput, _ := cmd.Flags().GetBool("json")
		platform, err := normalizeDeviceStartPlatform(platform)
		if err != nil {
			return err
		}
		appID, buildVersionID, appURL, err = normalizeDeviceStartArtifactFlags(appID, buildVersionID, appURL)
		if err != nil {
			return err
		}
		appLink = normalizeOptionalDeviceFlagValue(appLink)
		if !cmd.Flags().Changed("timeout") {
			cwd, cwdErr := os.Getwd()
			if cwdErr == nil {
				cfg, cfgErr := config.LoadProjectConfig(filepath.Join(cwd, ".revyl", "config.yaml"))
				if cfgErr == nil {
					timeout = config.EffectiveTimeoutSeconds(cfg, timeout)
				}
			}
		}

		targetCatalog := loadCommandDeviceTargetCatalog(cmd.Context(), cmd)

		// Resolve device model/OS version selection
		deviceNameFlag, _ := cmd.Flags().GetString("device-name")
		deviceSelectFlag, _ := cmd.Flags().GetBool("device")
		deviceModelFlag, _ := cmd.Flags().GetString("device-model")
		osVersionFlag, _ := cmd.Flags().GetString("os-version")

		var selectedDeviceModel, selectedOsVersion string
		if deviceNameFlag != "" {
			presetPlatform, presetModel, presetRuntime, presetErr := targetCatalog.ResolvePreset(deviceNameFlag)
			if presetErr != nil {
				return presetErr
			}
			platform = presetPlatform
			selectedDeviceModel = presetModel
			selectedOsVersion = presetRuntime
		} else if deviceModelFlag != "" || osVersionFlag != "" {
			if deviceModelFlag == "" || osVersionFlag == "" {
				return fmt.Errorf("--device-model and --os-version must both be provided")
			}
			if err := targetCatalog.ValidateDevicePair(platform, deviceModelFlag, osVersionFlag); err != nil {
				return err
			}
			selectedDeviceModel = deviceModelFlag
			selectedOsVersion = osVersionFlag
		} else if deviceSelectFlag {
			pairs, pairsErr := targetCatalog.GetAvailableTargetPairs(platform)
			if pairsErr != nil {
				return pairsErr
			}
			defaultPair, _ := targetCatalog.GetDefaultPair(platform)
			options := make([]ui.SelectOption, 0, len(pairs)+1)
			options = append(options, ui.SelectOption{
				Label:       fmt.Sprintf("Auto (%s)", devicetargets.FormatPairLabel(defaultPair)),
				Value:       "auto",
				Description: "Use platform default",
			})
			for _, p := range pairs {
				options = append(options, ui.SelectOption{
					Label: devicetargets.FormatPairLabel(p),
					Value: fmt.Sprintf("%s|%s", p.Model, p.Runtime),
				})
			}
			_, selected, selectErr := ui.Select("Select device:", options, 0)
			if selectErr != nil {
				return fmt.Errorf("device selection failed: %w", selectErr)
			}
			if selected != "auto" {
				parts := strings.SplitN(selected, "|", 2)
				if len(parts) != 2 {
					return fmt.Errorf("unexpected device selection value: %q", selected)
				}
				selectedDeviceModel = parts[0]
				selectedOsVersion = parts[1]
			}
		}
		if selectedDeviceModel != "" && !jsonOutput {
			ui.PrintInfo("Device: %s", devicetargets.FormatPairLabel(devicetargets.DevicePair{
				Model: selectedDeviceModel, Runtime: selectedOsVersion,
			}))
		}

		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
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
					ui.StopSpinner()
					ui.PrintWarning("Cancelling device provisioning...")
				}
				cancel()
			case <-ctx.Done():
			}
		}()

		startOpts := mcppkg.StartSessionOptions{
			Platform:       platform,
			AppID:          appID,
			BuildVersionID: buildVersionID,
			AppURL:         appURL,
			AppLink:        appLink,
			LaunchVars:     launchVars,
			IdleTimeout:    time.Duration(timeout) * time.Second,
			DeviceModel:    selectedDeviceModel,
			OsVersion:      selectedOsVersion,
		}

		var session *mcppkg.DeviceSession
		if jsonOutput {
			_, session, err = mgr.StartSession(ctx, startOpts)
		} else {
			_, session, err = startDevSessionWithProgress(
				ctx,
				mgr,
				startOpts,
				30*time.Second,
				nil,
			)
		}
		if err != nil {
			return err
		}

		if jsonOutput {
			data, _ := json.MarshalIndent(session, "", "  ")
			fmt.Println(string(data))
		} else {
			devMode, _ := cmd.Flags().GetBool("dev")
			reportURL := fmt.Sprintf(
				"%s/tests/report?sessionId=%s",
				config.GetAppURL(devMode),
				session.SessionID,
			)
			ui.PrintSuccess("Device ready! Session %d (%s)", session.Index, platform)
			ui.PrintLink("Session", session.SessionID)
			ui.PrintLink("Live View", reportURL)
			cmdPrefix := deviceCommandPrefix(cmd)
			ui.PrintNextSteps([]ui.NextStep{
				{Label: "Take a screenshot", Command: fmt.Sprintf("%s device screenshot --out screen.png", cmdPrefix)},
				{Label: "Stop when done", Command: fmt.Sprintf("%s device stop -s %d", cmdPrefix, session.Index)},
			})
		}

		if openBrowser {
			devMode, _ := cmd.Flags().GetBool("dev")
			reportURL := fmt.Sprintf("%s/tests/report?sessionId=%s",
				config.GetAppURL(devMode), session.SessionID)
			_ = ui.OpenBrowser(reportURL)
		}

		return nil
	},
}

var deviceStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop a device session (-s <index> or --all)",
	Example: `  revyl device stop
  revyl device stop --all
  revyl device stop -s 1`,
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
		jsonOutput, _ := cmd.Flags().GetBool("json")
		if !jsonOutput {
			ui.PrintInfo("Stopping session %d (%s)...", idx, sessionID)
		}

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
	Example: `  revyl device screenshot
  revyl device screenshot --out before.png`,
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

var deviceHierarchyCmd = &cobra.Command{
	Use:   "hierarchy",
	Short: "Dump the device UI hierarchy (Android XML / iOS JSON)",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		respBytes, err := mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/hierarchy", nil)
		if err != nil {
			return err
		}
		out, _ := cmd.Flags().GetString("out")
		if out != "" {
			if err := os.WriteFile(out, respBytes, 0o644); err != nil {
				return err
			}
			jsonOrPrint(cmd, map[string]string{"path": out, "bytes": fmt.Sprintf("%d", len(respBytes))}, fmt.Sprintf("Hierarchy saved: %s (%d bytes)", out, len(respBytes)))
		} else {
			wantJSON, _ := cmd.Flags().GetBool("json")
			if wantJSON {
				envelope := map[string]interface{}{
					"hierarchy": string(respBytes),
					"bytes":     len(respBytes),
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(envelope)
			}
			fmt.Println(string(respBytes))
		}
		return nil
	},
}

var deviceTapCmd = &cobra.Command{
	Use:   "tap",
	Short: "Tap an element (--target or --x/--y)",
	Example: `  revyl device tap --target "Sign In button"
  revyl device tap --x 200 --y 450
  revyl device tap --target "Submit" --json`,
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
		target, _ := cmd.Flags().GetString("target")
		body := map[string]interface{}{"x": x, "y": y}
		if target != "" {
			body["target"] = target
		}
		respBody, err := mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/tap", body)
		if err != nil {
			return err
		}
		result := buildActionResult("tap", x, y, target, respBody)
		jsonOrPrint(cmd, result, fmt.Sprintf("Tapped (%d, %d)", x, y))
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
		target, _ := cmd.Flags().GetString("target")
		body := map[string]interface{}{"x": x, "y": y}
		if target != "" {
			body["target"] = target
		}
		respBody, err := mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/double_tap", body)
		if err != nil {
			return err
		}
		result := buildActionResult("double_tap", x, y, target, respBody)
		jsonOrPrint(cmd, result, fmt.Sprintf("Double-tapped (%d, %d)", x, y))
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
		target, _ := cmd.Flags().GetString("target")
		body := map[string]interface{}{"x": x, "y": y, "duration_ms": dur}
		if target != "" {
			body["target"] = target
		}
		respBody, err := mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/longpress", body)
		if err != nil {
			return err
		}
		result := buildActionResult("long_press", x, y, target, respBody)
		result.DurationMs = dur
		jsonOrPrint(cmd, result, fmt.Sprintf("Long-pressed (%d, %d) for %dms", x, y, dur))
		return nil
	},
}

var deviceTypeCmd = &cobra.Command{
	Use:   "type",
	Short: "Type text (--target or --x/--y, plus --text)",
	Example: `  revyl device type --target "email field" --text "user@example.com"
  revyl device type --x 200 --y 300 --text "hello"`,
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
		target, _ := cmd.Flags().GetString("target")
		body := map[string]interface{}{"x": x, "y": y, "text": text, "clear_first": clearFirst}
		if target != "" {
			body["target"] = target
		}
		respBody, err := mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/input", body)
		if err != nil {
			return err
		}
		result := buildActionResult("type", x, y, target, respBody)
		result.Text = text
		jsonOrPrint(cmd, result, fmt.Sprintf("Typed '%s' at (%d, %d)", text, x, y))
		return nil
	},
}

var deviceSwipeCmd = &cobra.Command{
	Use:   "swipe [direction]",
	Short: "Swipe (--target or --x/--y, plus direction)",
	Example: `  revyl device swipe down
  revyl device swipe up --target "product list"
  revyl device swipe --direction down --x 200 --y 400`,
	Args: cobra.MaximumNArgs(1),
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
		if len(args) > 0 && args[0] != "" {
			direction = args[0]
		}
		if direction == "" {
			return fmt.Errorf("direction is required: revyl device swipe <up|down|left|right>")
		}
		x, y, err := resolveTargetOrCoords(cmd, mgr, session.Index)
		if err != nil {
			return err
		}
		dur, _ := cmd.Flags().GetInt("duration")
		if dur == 0 {
			dur = 500
		}
		target, _ := cmd.Flags().GetString("target")
		body := map[string]interface{}{"x": x, "y": y, "direction": direction, "duration_ms": dur}
		if target != "" {
			body["target"] = target
		}
		respBody, err := mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/swipe", body)
		if err != nil {
			return err
		}
		result := buildActionResult("swipe", x, y, target, respBody)
		result.Direction = direction
		result.DurationMs = dur
		jsonOrPrint(cmd, result, fmt.Sprintf("Swiped %s from (%d, %d)", direction, x, y))
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
		respBody, err := mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/drag", body)
		if err != nil {
			return err
		}
		result := buildActionResult("drag", sx, sy, "", respBody)
		result.EndX = ex
		result.EndY = ey
		jsonOrPrint(cmd, result, fmt.Sprintf("Dragged (%d,%d) -> (%d,%d)", sx, sy, ex, ey))
		return nil
	},
}

var deviceWaitCmd = &cobra.Command{
	Use:   "wait",
	Short: "Wait for a fixed duration on the device session",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		durationMs, _ := cmd.Flags().GetInt("duration-ms")
		if durationMs < 0 {
			return fmt.Errorf("--duration-ms must be >= 0")
		}
		body := map[string]int{"duration_ms": durationMs}
		_, err = mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/wait", body)
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, body, fmt.Sprintf("Waited %dms", durationMs))
		return nil
	},
}

var devicePinchCmd = &cobra.Command{
	Use:   "pinch",
	Short: "Pinch/zoom an element (--target or --x/--y, plus --scale)",
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
		scale, _ := cmd.Flags().GetFloat64("scale")
		durationMs, _ := cmd.Flags().GetInt("duration")
		if durationMs <= 0 {
			durationMs = 300
		}
		body := map[string]interface{}{
			"x":           x,
			"y":           y,
			"scale":       scale,
			"duration_ms": durationMs,
		}
		respBody, err := mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/pinch", body)
		if err != nil {
			return err
		}
		result := buildActionResult("pinch", x, y, "", respBody)
		result.Scale = scale
		result.DurationMs = durationMs
		jsonOrPrint(cmd, result, fmt.Sprintf("Pinched (%d, %d) scale=%.2f", x, y, scale))
		return nil
	},
}

var deviceClearTextCmd = &cobra.Command{
	Use:   "clear-text",
	Short: "Clear text in an element (--target or --x/--y)",
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
		respBody, err := mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/clear_text", body)
		if err != nil {
			return err
		}
		result := buildActionResult("clear_text", x, y, "", respBody)
		jsonOrPrint(cmd, result, fmt.Sprintf("Cleared text at (%d, %d)", x, y))
		return nil
	},
}

var deviceBackCmd = &cobra.Command{
	Use:   "back",
	Short: "Press Android back button",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		respBody, err := mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/back", nil)
		if err != nil {
			return err
		}
		result := buildActionResult("back", 0, 0, "", respBody)
		jsonOrPrint(cmd, result, "Pressed back button")
		return nil
	},
}

var deviceKeyCmd = &cobra.Command{
	Use:   "key [key]",
	Short: "Send a non-printable key (ENTER or BACKSPACE)",
	Example: `  revyl device key ENTER
  revyl device key BACKSPACE
  revyl device key --key ENTER`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		rawKey, _ := cmd.Flags().GetString("key")
		if len(args) > 0 && args[0] != "" {
			rawKey = args[0]
		}
		if rawKey == "" {
			return fmt.Errorf("key is required: revyl device key <ENTER|BACKSPACE>")
		}
		normalized := strings.ToUpper(strings.TrimSpace(rawKey))
		switch normalized {
		case "RETURN":
			normalized = "ENTER"
		case "DELETE":
			normalized = "BACKSPACE"
		}
		if normalized != "ENTER" && normalized != "BACKSPACE" {
			return fmt.Errorf("--key must be ENTER or BACKSPACE")
		}
		body := map[string]string{"key": normalized}
		respBody, err := mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/key", body)
		if err != nil {
			return err
		}
		result := buildActionResult("key", 0, 0, "", respBody)
		jsonOrPrint(cmd, result, fmt.Sprintf("Sent key %s", normalized))
		return nil
	},
}

var deviceShakeCmd = &cobra.Command{
	Use:   "shake",
	Short: "Trigger shake gesture",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		_, err = mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/shake", nil)
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, map[string]bool{"success": true}, "Triggered shake gesture")
		return nil
	},
}

var deviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install an app from a URL, build version ID, or app ID",
	RunE: func(cmd *cobra.Command, args []string) error {
		appID, _ := cmd.Flags().GetString("app-id")
		appURL, _ := cmd.Flags().GetString("app-url")
		buildVersionID, _ := cmd.Flags().GetString("build-version-id")
		bundleID, _ := cmd.Flags().GetString("bundle-id")

		appID, buildVersionID, appURL, err := normalizeDeviceStartArtifactFlags(appID, buildVersionID, appURL)
		if err != nil {
			return err
		}
		bundleID = normalizeOptionalDeviceFlagValue(bundleID)

		if appID == "" && buildVersionID == "" && appURL == "" {
			return fmt.Errorf("--app-url, --build-version-id, or --app-id is required")
		}

		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}

		resolved, err := startdevice.ResolveStartArtifact(cmd.Context(), mgr.APIClient(), startdevice.StartArtifactOptions{
			AppID:          appID,
			BuildVersionID: buildVersionID,
			AppURL:         appURL,
		})
		if err != nil {
			return err
		}
		appURL = resolved.AppURL
		if bundleID == "" && resolved.AppPackage != "" {
			bundleID = resolved.AppPackage
		}

		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		body := map[string]string{"app_url": appURL}
		if bundleID != "" {
			body["bundle_id"] = bundleID
		}
		respBody, err := mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/install", body)
		if err != nil {
			return err
		}
		if err := ensureWorkerActionSucceeded(respBody, "install"); err != nil {
			return err
		}

		payload := map[string]string{"app_url": appURL, "status": "installed"}
		message := fmt.Sprintf("Installed from %s", appURL)
		if detectedBundleID := extractInstallBundleID(respBody); detectedBundleID != "" {
			payload["bundle_id"] = detectedBundleID
			message = fmt.Sprintf("Installed %s from %s", detectedBundleID, appURL)
		}

		jsonOrPrint(cmd, payload, message)
		return nil
	},
}

var deviceLaunchCmd = &cobra.Command{
	Use:   "launch [bundle-id]",
	Short: "Launch an installed app by bundle ID",
	Example: `  revyl device launch com.example.app
  revyl device launch --bundle-id com.example.app`,
	Args: cobra.MaximumNArgs(1),
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
		if len(args) > 0 && args[0] != "" {
			bundleID = args[0]
		}
		if bundleID == "" {
			return fmt.Errorf("bundle ID is required: revyl device launch <bundle-id> (e.g. 'com.example.app')")
		}
		body := map[string]string{"bundle_id": bundleID}
		_, err = mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/launch", body)
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, map[string]string{"bundle_id": bundleID, "status": "launched"}, fmt.Sprintf("Launched %s", bundleID))
		return nil
	},
}

var deviceHomeCmd = &cobra.Command{
	Use:   "home",
	Short: "Go to device home screen",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		_, err = mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/go_home", nil)
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, map[string]bool{"success": true}, "Returned to home screen")
		return nil
	},
}

var deviceKillAppCmd = &cobra.Command{
	Use:   "kill-app",
	Short: "Kill the installed app",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		_, err = mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/kill_app", nil)
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, map[string]bool{"success": true}, "Killed installed app")
		return nil
	},
}

var deviceOpenAppCmd = &cobra.Command{
	Use:   "open-app [app]",
	Short: "Open a system app by name (e.g. settings, safari, chrome)",
	Example: `  revyl device open-app settings
  revyl device open-app --app safari`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		appName, _ := cmd.Flags().GetString("app")
		if len(args) > 0 && args[0] != "" {
			appName = args[0]
		}
		if appName == "" {
			return fmt.Errorf("app name is required: revyl device open-app <name> (e.g. 'settings', 'safari', or a raw bundle ID)")
		}
		bundleID := mcppkg.ResolveSystemApp(session.Platform, appName)
		body := map[string]string{"bundle_id": bundleID}
		_, err = mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/launch", body)
		if err != nil {
			return err
		}
		if bundleID != appName {
			jsonOrPrint(cmd, map[string]string{"app": appName, "bundle_id": bundleID, "status": "opened"},
				fmt.Sprintf("Opened %s (%s)", appName, bundleID))
		} else {
			jsonOrPrint(cmd, map[string]string{"bundle_id": bundleID, "status": "opened"},
				fmt.Sprintf("Opened %s", bundleID))
		}
		return nil
	},
}

var deviceNavigateCmd = &cobra.Command{
	Use:   "navigate [url]",
	Short: "Open a URL or deep link on device",
	Example: `  revyl device navigate https://example.com
  revyl device navigate --url https://example.com`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		url, _ := cmd.Flags().GetString("url")
		if len(args) > 0 && args[0] != "" {
			url = args[0]
		}
		if url == "" {
			return fmt.Errorf("URL is required: revyl device navigate <url>")
		}
		body := map[string]string{"url": url}
		_, err = mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/open_url", body)
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, map[string]string{"url": url, "status": "opened"}, fmt.Sprintf("Opened %s", url))
		return nil
	},
}

var deviceSetLocationCmd = &cobra.Command{
	Use:   "set-location",
	Short: "Set device GPS location",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		lat, _ := cmd.Flags().GetFloat64("lat")
		lon, _ := cmd.Flags().GetFloat64("lon")
		if lat < -90 || lat > 90 {
			return fmt.Errorf("--lat must be between -90 and 90, got %f", lat)
		}
		if lon < -180 || lon > 180 {
			return fmt.Errorf("--lon must be between -180 and 180, got %f", lon)
		}
		body := map[string]float64{"latitude": lat, "longitude": lon}
		_, err = mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/set_location", body)
		if err != nil {
			return err
		}
		jsonOrPrint(cmd, map[string]interface{}{"latitude": lat, "longitude": lon, "status": "set"},
			fmt.Sprintf("Location set to (%g, %g)", lat, lon))
		return nil
	},
}

var deviceNetworkCmd = &cobra.Command{
	Use:   "network",
	Short: "Toggle device network connectivity (airplane mode)",
	Long:  `Enable or disable airplane mode on the device. Use --connected to restore network or --disconnected to enable airplane mode.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		connected, _ := cmd.Flags().GetBool("connected")
		disconnected, _ := cmd.Flags().GetBool("disconnected")
		if !connected && !disconnected {
			return fmt.Errorf("specify --connected or --disconnected")
		}
		if connected && disconnected {
			return fmt.Errorf("--connected and --disconnected are mutually exclusive")
		}
		airplaneEnabled := disconnected
		body := map[string]bool{"enabled": airplaneEnabled}
		_, err = mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/set_airplane_mode", body)
		if err != nil {
			return err
		}
		status := "connected"
		if airplaneEnabled {
			status = "disconnected (airplane mode)"
		}
		jsonOrPrint(cmd, map[string]interface{}{"airplane_mode": airplaneEnabled, "status": status},
			fmt.Sprintf("Network: %s", status))
		return nil
	},
}

var deviceDownloadFileCmd = &cobra.Command{
	Use:   "download-file [url]",
	Short: "Download a file to device from URL",
	Example: `  revyl device download-file https://example.com/file.pdf
  revyl device download-file --url https://example.com/file.pdf --filename report.pdf`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		url, _ := cmd.Flags().GetString("url")
		if len(args) > 0 && args[0] != "" {
			url = args[0]
		}
		url, err := normalizeRequiredDeviceURLFlag(url, "--url", "")
		if err != nil {
			return err
		}
		filename, _ := cmd.Flags().GetString("filename")
		filename = normalizeOptionalDeviceFlagValue(filename)

		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}
		response, err := mgr.DownloadFileForSession(
			cmd.Context(),
			session.Index,
			mcppkg.DeviceDownloadFileRequest{
				URL:      url,
				Filename: filename,
			},
		)
		if err != nil {
			return err
		}
		if response == nil {
			return fmt.Errorf("download_file failed on the device")
		}
		if !response.Success {
			errMsg := strings.TrimSpace(response.Error)
			if errMsg == "" {
				errMsg = "download_file failed on the device"
			}
			return fmt.Errorf("%s", errMsg)
		}

		fallback := fmt.Sprintf("Downloaded from %s", url)
		if response != nil && strings.TrimSpace(response.DevicePath) != "" {
			fallback = fmt.Sprintf("Downloaded %s to %s", url, response.DevicePath)
		}
		jsonOrPrint(cmd, response, fallback)
		return nil
	},
}

var deviceReportCmd = &cobra.Command{
	Use:   "report",
	Short: "View the report for a device session",
	Long: `View the report for a device session. By default uses the active session.

Use --session-id to fetch a report by session ID directly without needing
to attach first.

Examples:
  revyl device report                                          # active session
  revyl device report --session-id e2b927a6-723f-4ddb-...      # by ID
  revyl device report --session-id e2b927a6-723f-... --json    # JSON output`,
	RunE: func(cmd *cobra.Command, args []string) error {
		directSessionID, _ := cmd.Flags().GetString("session-id")

		var targetSessionID string
		if directSessionID != "" {
			targetSessionID = directSessionID
		} else {
			mgr, err := getDeviceSessionMgr(cmd)
			if err != nil {
				return err
			}
			session, err := resolveSessionFlag(cmd, mgr)
			if err != nil {
				return fmt.Errorf("no active session (use --session-id to specify one directly): %w", err)
			}
			targetSessionID = session.SessionID
		}

		apiKey := os.Getenv("REVYL_API_KEY")
		if apiKey == "" {
			creds, credErr := auth.NewManager().GetCredentials()
			if credErr != nil || creds == nil || creds.APIKey == "" {
				return fmt.Errorf("not authenticated: set REVYL_API_KEY or run 'revyl auth login'")
			}
			apiKey = creds.APIKey
		}
		devMode, _ := cmd.Flags().GetBool("dev")
		client := api.NewClientWithDevMode(apiKey, devMode)

		envelope, err := client.GetReportBySession(cmd.Context(), targetSessionID, true, true, false)
		if err != nil {
			return fmt.Errorf("failed to fetch session report: %w", err)
		}
		if envelope.Report == nil {
			jsonOrPrint(cmd, map[string]string{"session_id": targetSessionID, "status": "no_report"}, "No report available for this session yet.")
			return nil
		}
		jsonOrPrint(cmd, envelope.Raw, formatSessionReportFallback(envelope.Report, targetSessionID))
		return nil
	},
}

func formatSessionReportFallback(r *api.CLIReportContextResponse, sessionID string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Session Report: %s\n", sessionID))
	if r.SessionStatus != nil {
		b.WriteString(fmt.Sprintf("  Status:   %s\n", *r.SessionStatus))
	}
	if r.Platform != nil {
		b.WriteString(fmt.Sprintf("  Platform: %s\n", *r.Platform))
	}
	if r.DeviceModel != nil {
		b.WriteString(fmt.Sprintf("  Device:   %s\n", *r.DeviceModel))
	}
	if r.TotalSteps != nil {
		b.WriteString(fmt.Sprintf("  Steps:    %d\n", *r.TotalSteps))
	}
	if r.VideoURL != nil {
		b.WriteString(fmt.Sprintf("  Video:    %s\n", *r.VideoURL))
	}
	if r.ReportURL != nil {
		b.WriteString(fmt.Sprintf("  Report:   %s\n", *r.ReportURL))
	}
	return b.String()
}

var deviceTargetsCmd = &cobra.Command{
	Use:   "targets",
	Short: "List available device models and OS versions",
	Long:  `List available device models and OS versions for each platform.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		platform, _ := cmd.Flags().GetString("platform")
		jsonOutput, _ := cmd.Flags().GetBool("json")
		targetCatalog := loadCommandDeviceTargetCatalog(cmd.Context(), cmd)

		platforms := []string{"ios", "android"}
		if platform != "" {
			p, err := normalizeDeviceStartPlatform(platform)
			if err != nil {
				return err
			}
			platforms = []string{p}
		}

		if jsonOutput {
			allPairs := make(map[string][]devicetargets.DevicePair, len(platforms))
			for _, p := range platforms {
				pairs, err := targetCatalog.GetAvailableTargetPairs(p)
				if err != nil {
					return err
				}
				allPairs[p] = pairs
			}
			data, _ := json.MarshalIndent(allPairs, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		for i, p := range platforms {
			pairs, err := targetCatalog.GetAvailableTargetPairs(p)
			if err != nil {
				return err
			}
			defaultPair, _ := targetCatalog.GetDefaultPair(p)

			if i > 0 {
				ui.Println()
			}
			ui.PrintInfo("%s (%d targets)", strings.ToUpper(p), len(pairs))
			ui.Println()

			table := ui.NewTable("MODEL", "OS VERSION", "DEFAULT")
			for _, pair := range pairs {
				isDefault := ""
				if pair.Model == defaultPair.Model && pair.Runtime == defaultPair.Runtime {
					isDefault = "*"
				}
				table.AddRow(pair.Model, pair.Runtime, isDefault)
			}
			table.Render()
		}
		return nil
	},
}

var deviceHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "Show device session history",
	Long:  `Show recent device session history from the server.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		apiKey, err := getAPIKey()
		if err != nil {
			return err
		}

		devMode, _ := cmd.Flags().GetBool("dev")
		client := api.NewClientWithDevMode(apiKey, devMode)

		limit, _ := cmd.Flags().GetInt("limit")
		jsonOutput, _ := cmd.Flags().GetBool("json")

		if !jsonOutput {
			ui.StartSpinner("Fetching session history...")
		}
		result, err := client.GetDeviceSessionHistory(cmd.Context(), limit, 0)
		if !jsonOutput {
			ui.StopSpinner()
		}

		if err != nil {
			return fmt.Errorf("failed to fetch session history: %w", err)
		}

		if jsonOutput {
			data, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		if len(result.Sessions) == 0 {
			ui.PrintInfo("No device session history found.")
			return nil
		}

		ui.Println()
		ui.PrintInfo("Device Sessions (%d total)", result.Total)
		ui.Println()

		table := ui.NewTable("ID", "PLATFORM", "STATUS", "CREATED", "DURATION")
		table.SetMinWidth(0, 12)
		for _, s := range result.Sessions {
			idShort := truncatePrefix(s.ID, 8)
			duration := "-"
			if s.Duration > 0 {
				duration = fmt.Sprintf("%.0fs", s.Duration)
			}
			table.AddRow(idShort, s.Platform, s.Status, s.CreatedAt, duration)
		}
		table.Render()
		return nil
	},
}

var deviceInstructionCmd = &cobra.Command{
	Use:   "instruction <description>",
	Short: "Run one instruction step on the active device",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		description := strings.TrimSpace(strings.Join(args, " "))
		if description == "" {
			return fmt.Errorf("instruction description is required")
		}
		return executeLiveStepCommand(
			cmd,
			mcppkg.LiveStepRequest{
				StepType:        "instruction",
				StepDescription: description,
			},
			"Instruction",
		)
	},
}

var deviceValidationCmd = &cobra.Command{
	Use:   "validation <description>",
	Short: "Run one validation step on the active device",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		description := strings.TrimSpace(strings.Join(args, " "))
		if description == "" {
			return fmt.Errorf("validation description is required")
		}
		return executeLiveStepCommand(
			cmd,
			mcppkg.LiveStepRequest{
				StepType:        "validation",
				StepDescription: description,
			},
			"Validation",
		)
	},
}

var deviceExtractCmd = &cobra.Command{
	Use:   "extract <description>",
	Short: "Run one extract step on the active device",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		description := strings.TrimSpace(strings.Join(args, " "))
		if description == "" {
			return fmt.Errorf("extract description is required")
		}

		request := mcppkg.LiveStepRequest{
			StepType:        "extract",
			StepDescription: description,
		}
		variableName, _ := cmd.Flags().GetString("variable-name")
		if strings.TrimSpace(variableName) != "" {
			request.Metadata = map[string]any{
				"variable_name": strings.TrimSpace(variableName),
			}
		}

		return executeLiveStepCommand(cmd, request, "Extract")
	},
}

var deviceCodeExecutionCmd = &cobra.Command{
	Use:   "code-execution [script-id]",
	Short: "Run one code_execution step on the active device",
	Long: `Run a code execution step on the active device session.

Three modes:
  1. By script ID:    revyl device code-execution <script-id>
  2. From local file: revyl device code-execution --file ./script.py --runtime python
  3. Inline code:     revyl device code-execution --code "print('hello')" --runtime python

Modes 2 and 3 create an ephemeral script on the backend, execute it, then clean up.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		codeExecFile, _ := cmd.Flags().GetString("file")
		codeExecInline, _ := cmd.Flags().GetString("code")
		codeExecRuntime, _ := cmd.Flags().GetString("runtime")

		hasScriptID := len(args) > 0 && strings.TrimSpace(args[0]) != ""
		hasFile := codeExecFile != ""
		hasInline := codeExecInline != ""

		modeCount := 0
		if hasScriptID {
			modeCount++
		}
		if hasFile {
			modeCount++
		}
		if hasInline {
			modeCount++
		}

		if modeCount == 0 {
			return fmt.Errorf("provide a script ID, --file, or --code")
		}
		if modeCount > 1 {
			return fmt.Errorf("use only one of: script ID, --file, or --code")
		}

		if hasScriptID {
			return executeLiveStepCommand(
				cmd,
				mcppkg.LiveStepRequest{
					StepType:        "code_execution",
					StepDescription: strings.TrimSpace(args[0]),
				},
				"Code execution",
			)
		}

		if codeExecRuntime == "" {
			return fmt.Errorf("--runtime is required when using --file or --code")
		}

		var code string
		if hasFile {
			data, err := os.ReadFile(codeExecFile)
			if err != nil {
				return fmt.Errorf("failed to read file: %w", err)
			}
			code = string(data)
		} else {
			code = codeExecInline
		}

		apiKey, err := getAPIKey()
		if err != nil {
			return err
		}
		devMode, _ := cmd.Flags().GetBool("dev")
		client := api.NewClientWithDevMode(apiKey, devMode)

		ephemeralName := fmt.Sprintf("_ephemeral_%d", time.Now().UnixMilli())
		created, err := client.CreateScript(cmd.Context(), &api.CLICreateScriptRequest{
			Name:    ephemeralName,
			Code:    code,
			Runtime: codeExecRuntime,
		})
		if err != nil {
			return fmt.Errorf("failed to create ephemeral script: %w", err)
		}

		execErr := executeLiveStepCommand(
			cmd,
			mcppkg.LiveStepRequest{
				StepType:        "code_execution",
				StepDescription: created.ID,
			},
			"Code execution",
		)

		_ = client.DeleteScript(cmd.Context(), created.ID)

		return execErr
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
		jsonOrPrint(cmd, session, formatDeviceInfoFallback(session))
		return nil
	},
}

var deviceDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run diagnostics on auth, session, worker, and grounding health",
	RunE: func(cmd *cobra.Command, args []string) error {
		var checks []mcppkg.DiagnosticCheck
		allPassed := true

		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			checks = append(checks, mcppkg.DiagnosticCheck{Name: "auth", Status: "fail", Detail: err.Error(), Fix: "Set REVYL_API_KEY or run 'revyl auth login'"})
			allPassed = false
			output := mcppkg.DeviceDoctorOutput{Checks: checks, AllPassed: false}
			jsonOrPrint(cmd, output, "Auth check: FAIL ("+err.Error()+")")
			return nil
		}
		checks = append(checks, mcppkg.DiagnosticCheck{Name: "auth", Status: "pass"})

		session, resolveErr := resolveSessionFlag(cmd, mgr)
		if resolveErr != nil || session == nil {
			total := mgr.SessionCount()
			detail := "No active session"
			if total > 0 {
				detail = fmt.Sprintf("could not resolve (%s). %d session(s) exist", resolveErr.Error(), total)
			}
			checks = append(checks, mcppkg.DiagnosticCheck{Name: "session", Status: "none", Detail: detail, Fix: "Start a session with 'revyl device start'"})
			allPassed = false
		} else {
			sessionDetail := fmt.Sprintf("platform=%s, uptime=%.0fs", session.Platform, time.Since(session.StartedAt).Seconds())
			checks = append(checks, mcppkg.DiagnosticCheck{Name: "session", Status: "pass", Detail: sessionDetail})

			respBytes, werr := mgr.WorkerRequestForSession(cmd.Context(), session.Index, "/health", nil)
			if werr != nil {
				checks = append(checks, mcppkg.DiagnosticCheck{Name: "worker", Status: "fail", Detail: werr.Error(), Fix: "Stop and start a new session"})
				allPassed = false
			} else {
				checks = append(checks, mcppkg.DiagnosticCheck{Name: "worker", Status: "pass"})
				var health struct {
					DeviceConnected bool `json:"device_connected"`
				}
				if json.Unmarshal(respBytes, &health) == nil {
					if health.DeviceConnected {
						checks = append(checks, mcppkg.DiagnosticCheck{Name: "device", Status: "pass"})
					} else {
						checks = append(checks, mcppkg.DiagnosticCheck{Name: "device", Status: "fail", Detail: "Worker running but device not connected", Fix: "Wait a few seconds or stop and start a new session"})
						allPassed = false
					}
				}
			}
		}

		output := mcppkg.DeviceDoctorOutput{Checks: checks, AllPassed: allPassed}

		jsonOutput, _ := cmd.Flags().GetBool("json")
		if jsonOutput {
			data, _ := json.MarshalIndent(output, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		for _, c := range checks {
			status := strings.ToUpper(c.Status)
			if c.Detail != "" {
				ui.PrintInfo("%s: %s (%s)", c.Name, status, c.Detail)
			} else {
				ui.PrintInfo("%s: %s", c.Name, status)
			}
		}

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

var deviceAttachCmd = &cobra.Command{
	Use:   "attach <session-id>",
	Short: "Attach to an existing session by ID",
	Long: `Attach to a running device session using its session ID.

This bypasses normal session discovery and directly connects to the specified
session, making it your active session. All subsequent device commands will
target this session.

The session ID can be found in the browser session viewer (Connect button)
or from the sessions API.

Examples:
  revyl device attach e2b927a6-723f-4ddb-b9a3-ff8f652d4c58
  revyl device attach e2b927a6    # prefix match (at least 8 chars)`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sessionID := args[0]

		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}

		idx, session, err := mgr.AttachBySessionID(cmd.Context(), sessionID)
		if err != nil {
			return fmt.Errorf("failed to attach: %w", err)
		}

		ui.PrintSuccess("Attached to session %d (%s)", idx, session.Platform)

		deviceLabel := session.Platform
		if session.ViewerURL != "" {
			ui.PrintInfo("Viewer:    %s", session.ViewerURL)
		}
		ui.PrintInfo("Session:   %s", session.SessionID)
		ui.PrintDim("")
		ui.PrintDim("Device ready. Run commands against this %s session:", deviceLabel)
		ui.PrintDim("  revyl device screenshot")
		ui.PrintDim("  revyl device tap --x 200 --y 400")
		ui.PrintDim("  revyl device instruction \"tap the login button\"")

		return nil
	},
}

var devicePerfCmd = &cobra.Command{
	Use:   "perf",
	Short: "Poll live performance metrics (CPU%, RSS, FPS) from an active session",
	Long: `Stream live performance metrics from a device session.

By default, polls continuously (--follow) printing one compact line per batch.
Use --no-follow for a single snapshot. Columns adapt to the platform:
  Android: TIME  CPU%  RSS  SYS MEM
  iOS:     TIME  CPU%  RSS  FPS`,
	Example: `  revyl device perf
  revyl device perf --no-follow
  revyl device perf --interval 5s --json
  revyl device perf -s 0 -f --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionFlag(cmd, mgr)
		if err != nil {
			return err
		}

		follow, _ := cmd.Flags().GetBool("follow")
		noFollow, _ := cmd.Flags().GetBool("no-follow")
		if noFollow {
			follow = false
		}
		intervalStr, _ := cmd.Flags().GetString("interval")
		interval, parseErr := time.ParseDuration(intervalStr)
		if parseErr != nil {
			interval = 2 * time.Second
		}
		jsonOutput, _ := cmd.Flags().GetBool("json")

		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			cancel()
		}()

		cursor := "0"
		headerPrinted := false
		platform := session.Platform

		if !jsonOutput {
			ui.PrintInfo("Polling session %d (%s)...", session.Index, platform)
		}

		for {
			resp, pollErr := pollPerfWithRetry(ctx, mgr, session.Index, cursor, 100)
			if pollErr != nil {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("failed to poll performance metrics: %w", pollErr)
			}
			cursor = resp.NextCursor

			if len(resp.Items) > 0 {
				if jsonOutput {
					data, _ := json.Marshal(resp)
					fmt.Println(string(data))
				} else {
					if !headerPrinted {
						printPerfHeader(platform)
						headerPrinted = true
					}
					printPerfSamples(resp, platform)
				}
			}

			if !follow {
				return nil
			}

			if !resp.CaptureRunning && len(resp.Items) == 0 {
				if !jsonOutput {
					ui.PrintDim("  Capture not running, waiting...")
				}
			}

			select {
			case <-ctx.Done():
				return nil
			case <-time.After(interval):
			}
		}
	},
}

func pollPerfWithRetry(
	ctx context.Context,
	mgr *mcppkg.DeviceSessionManager,
	sessionIndex int,
	cursor string,
	limit int,
) (*mcppkg.PerfPollResponse, error) {
	backoffs := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	var lastErr error
	for attempt := 0; attempt <= len(backoffs); attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		resp, err := mgr.PollPerformanceMetricsForSession(ctx, sessionIndex, cursor, limit)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if attempt < len(backoffs) {
			ui.PrintDim("  reconnecting...")
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoffs[attempt]):
			}
		}
	}
	return nil, lastErr
}

func printPerfHeader(platform string) {
	switch strings.ToLower(platform) {
	case "android":
		fmt.Printf("%-10s  %7s  %10s  %-20s  %s\n", "TIME", "CPU%", "RSS", "SYS MEM", "[samples]")
	case "ios":
		fmt.Printf("%-10s  %7s  %10s  %6s  %s\n", "TIME", "CPU%", "RSS", "FPS", "[samples]")
	default:
		fmt.Printf("%-10s  %7s  %10s  %s\n", "TIME", "CPU%", "RSS", "[samples]")
	}
}

func printPerfSamples(resp *mcppkg.PerfPollResponse, platform string) {
	if resp.Summary == nil || len(resp.Items) == 0 {
		return
	}

	ts := time.Now().Format("15:04:05")
	sampleCount := len(resp.Items)

	var cpuStr string
	if resp.Summary.AvgCPUPercent != nil {
		cpuStr = fmt.Sprintf("%.1f%%", *resp.Summary.AvgCPUPercent)
	} else {
		cpuStr = "--"
	}

	var rssStr string
	if resp.Summary.AvgRSSMB != nil {
		cpuStr2 := *resp.Summary.AvgRSSMB
		if cpuStr2 >= 1024 {
			rssStr = fmt.Sprintf("%.1f GB", cpuStr2/1024)
		} else {
			rssStr = fmt.Sprintf("%.1f MB", cpuStr2)
		}
	} else {
		rssStr = "--"
	}

	switch strings.ToLower(platform) {
	case "android":
		sysMemStr := "--"
		last := resp.Items[len(resp.Items)-1]
		if last.MemorySystem != nil {
			totalKB, tok := last.MemorySystem["total_kb"]
			availKB, aok := last.MemorySystem["available_kb"]
			usedPct, pok := last.MemorySystem["used_percent"]
			if tok && aok && pok {
				totalGB := totalKB.(float64) / (1024 * 1024)
				availGB := availKB.(float64) / (1024 * 1024)
				sysMemStr = fmt.Sprintf("%.1f/%.1f GB %.0f%%", availGB, totalGB, usedPct.(float64))
			}
		}
		fmt.Printf("%-10s  %7s  %10s  %-20s  [%d]\n", ts, cpuStr, rssStr, sysMemStr, sampleCount)

	case "ios":
		fpsStr := "--"
		if resp.Summary.AvgFPS != nil {
			fpsStr = fmt.Sprintf("%.1f", *resp.Summary.AvgFPS)
		}
		fmt.Printf("%-10s  %7s  %10s  %6s  [%d]\n", ts, cpuStr, rssStr, fpsStr, sampleCount)

	default:
		fmt.Printf("%-10s  %7s  %10s  [%d]\n", ts, cpuStr, rssStr, sampleCount)
	}
}

func init() {
	// Global -s flag for session selection (added to all action commands)
	sessionFlag := func(cmd *cobra.Command) {
		cmd.Flags().IntP("s", "s", -1, "Session index to target (-1 for active)")
	}

	// Start
	deviceStartCmd.Flags().String("platform", "ios", "Platform: ios or android")
	deviceStartCmd.Flags().Int("timeout", 300, "Idle timeout in seconds")
	deviceStartCmd.Flags().Bool("open", true, "Open viewer in browser after device is ready")
	deviceStartCmd.Flags().String("app-id", "", "App ID to resolve latest build from")
	deviceStartCmd.Flags().String("build-version-id", "", "Build version ID to install")
	deviceStartCmd.Flags().String("app-url", "", "Direct app artifact URL (.apk/.ipa/.zip)")
	deviceStartCmd.Flags().String("app-link", "", "Deep link to launch after app start")
	deviceStartCmd.Flags().StringArray("launch-var", nil, "Org launch variable key or ID to apply to a raw session (repeatable)")
	deviceStartCmd.Flags().Bool("json", false, "Output as JSON")
	deviceStartCmd.Flags().Bool("device", false, "Interactively select device model and OS version")
	deviceStartCmd.Flags().String("device-model", "", "Target device model (e.g. \"iPhone 16\")")
	deviceStartCmd.Flags().String("os-version", "", "Target OS version (e.g. \"iOS 18.5\")")
	deviceStartCmd.Flags().String("device-name", "", "Named device preset (e.g. \"revyl-android-phone\", \"revyl-ios-iphone\")")

	// Stop
	deviceStopCmd.Flags().Bool("json", false, "Output as JSON")
	deviceStopCmd.Flags().Bool("all", false, "Stop all sessions")
	sessionFlag(deviceStopCmd)

	// Screenshot
	deviceScreenshotCmd.Flags().String("out", "", "Output file path")
	deviceScreenshotCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceScreenshotCmd)

	// Hierarchy
	deviceHierarchyCmd.Flags().String("out", "", "Output file path (write raw hierarchy to file)")
	deviceHierarchyCmd.Flags().Bool("json", false, "Wrap output in a JSON envelope")
	sessionFlag(deviceHierarchyCmd)

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

	// Wait
	deviceWaitCmd.Flags().Int("duration-ms", 1000, "Wait duration in milliseconds")
	deviceWaitCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceWaitCmd)

	// Pinch
	devicePinchCmd.Flags().String("target", "", "Element description (grounded)")
	devicePinchCmd.Flags().Int("x", 0, "X coordinate (raw)")
	devicePinchCmd.Flags().Int("y", 0, "Y coordinate (raw)")
	devicePinchCmd.Flags().Float64("scale", 2.0, "Zoom scale (>1 zoom in, <1 zoom out)")
	devicePinchCmd.Flags().Int("duration", 300, "Pinch duration in ms")
	devicePinchCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(devicePinchCmd)

	// Clear Text
	deviceClearTextCmd.Flags().String("target", "", "Element description (grounded)")
	deviceClearTextCmd.Flags().Int("x", 0, "X coordinate (raw)")
	deviceClearTextCmd.Flags().Int("y", 0, "Y coordinate (raw)")
	deviceClearTextCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceClearTextCmd)

	// Back
	deviceBackCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceBackCmd)

	// Key
	deviceKeyCmd.Flags().String("key", "", "Key to send (ENTER or BACKSPACE)")
	deviceKeyCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceKeyCmd)

	// Shake
	deviceShakeCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceShakeCmd)

	// Install
	deviceInstallCmd.Flags().String("app-id", "", "App ID to resolve latest build from")
	deviceInstallCmd.Flags().String("build-version-id", "", "Build version ID from a previous upload; download URL is resolved automatically")
	deviceInstallCmd.Flags().String("app-url", "", "URL to download app from (.apk or .ipa)")
	deviceInstallCmd.Flags().String("bundle-id", "", "Bundle ID (optional, auto-detected)")
	deviceInstallCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceInstallCmd)

	// Launch
	deviceLaunchCmd.Flags().String("bundle-id", "", "App bundle ID to launch (required)")
	deviceLaunchCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceLaunchCmd)

	// Home
	deviceHomeCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceHomeCmd)

	// Kill App
	deviceKillAppCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceKillAppCmd)

	// Open App
	deviceOpenAppCmd.Flags().String("app", "", "App name (e.g. settings, safari) or raw bundle ID (required)")
	deviceOpenAppCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceOpenAppCmd)

	// Navigate
	deviceNavigateCmd.Flags().String("url", "", "URL or deep link to open (required)")
	deviceNavigateCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceNavigateCmd)

	// Set Location
	deviceSetLocationCmd.Flags().Float64("lat", 0, "Latitude (-90 to 90, required)")
	deviceSetLocationCmd.Flags().Float64("lon", 0, "Longitude (-180 to 180, required)")
	deviceSetLocationCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceSetLocationCmd)

	// Network (airplane mode)
	deviceNetworkCmd.Flags().Bool("connected", false, "Restore network connectivity (disable airplane mode)")
	deviceNetworkCmd.Flags().Bool("disconnected", false, "Enable airplane mode (disable network)")
	deviceNetworkCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceNetworkCmd)

	// Download File
	deviceDownloadFileCmd.Flags().String("url", "", "URL to download from (required)")
	deviceDownloadFileCmd.Flags().String("filename", "", "Optional destination filename on the device")
	deviceDownloadFileCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceDownloadFileCmd)

	// Live high-level steps
	deviceInstructionCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceInstructionCmd)

	deviceValidationCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceValidationCmd)

	deviceExtractCmd.Flags().String("variable-name", "", "Optional variable name to store extracted data under")
	deviceExtractCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceExtractCmd)

	deviceCodeExecutionCmd.Flags().Bool("json", false, "Output as JSON")
	deviceCodeExecutionCmd.Flags().String("file", "", "Run code from a local file (creates ephemeral script)")
	deviceCodeExecutionCmd.Flags().String("code", "", "Run inline code string (creates ephemeral script)")
	deviceCodeExecutionCmd.Flags().String("runtime", "python", "Script runtime for --file/--code (python, javascript, typescript, bash)")
	sessionFlag(deviceCodeExecutionCmd)

	// Info
	deviceInfoCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceInfoCmd)

	// Doctor
	deviceDoctorCmd.Flags().Bool("json", false, "Output as JSON")
	sessionFlag(deviceDoctorCmd)

	// List
	deviceListCmd.Flags().Bool("json", false, "Output as JSON")

	// Targets
	deviceTargetsCmd.Flags().String("platform", "", "Filter to a specific platform (ios or android)")
	deviceTargetsCmd.Flags().Bool("json", false, "Output as JSON")

	// History
	deviceHistoryCmd.Flags().Int("limit", 20, "Maximum number of sessions to show")
	deviceHistoryCmd.Flags().Bool("json", false, "Output as JSON")

	// Register subcommands
	deviceCmd.AddCommand(deviceStartCmd)
	deviceCmd.AddCommand(deviceStopCmd)
	deviceCmd.AddCommand(deviceScreenshotCmd)
	deviceCmd.AddCommand(deviceHierarchyCmd)
	deviceCmd.AddCommand(deviceTapCmd)
	deviceCmd.AddCommand(deviceDoubleTapCmd)
	deviceCmd.AddCommand(deviceLongPressCmd)
	deviceCmd.AddCommand(deviceTypeCmd)
	deviceCmd.AddCommand(deviceSwipeCmd)
	deviceCmd.AddCommand(deviceDragCmd)
	deviceCmd.AddCommand(deviceWaitCmd)
	deviceCmd.AddCommand(devicePinchCmd)
	deviceCmd.AddCommand(deviceClearTextCmd)
	deviceCmd.AddCommand(deviceBackCmd)
	deviceCmd.AddCommand(deviceKeyCmd)
	deviceCmd.AddCommand(deviceShakeCmd)
	deviceCmd.AddCommand(deviceInstallCmd)
	deviceCmd.AddCommand(deviceLaunchCmd)
	deviceCmd.AddCommand(deviceHomeCmd)
	deviceCmd.AddCommand(deviceKillAppCmd)
	deviceCmd.AddCommand(deviceOpenAppCmd)
	deviceCmd.AddCommand(deviceNavigateCmd)
	deviceCmd.AddCommand(deviceSetLocationCmd)
	deviceCmd.AddCommand(deviceNetworkCmd)
	deviceCmd.AddCommand(deviceDownloadFileCmd)
	deviceCmd.AddCommand(deviceInstructionCmd)
	deviceCmd.AddCommand(deviceValidationCmd)
	deviceCmd.AddCommand(deviceExtractCmd)
	deviceCmd.AddCommand(deviceCodeExecutionCmd)
	deviceCmd.AddCommand(deviceInfoCmd)
	deviceCmd.AddCommand(deviceDoctorCmd)
	deviceCmd.AddCommand(deviceListCmd)
	deviceCmd.AddCommand(deviceUseCmd)
	deviceCmd.AddCommand(deviceAttachCmd)
	deviceCmd.AddCommand(deviceReportCmd)
	sessionFlag(deviceReportCmd)
	deviceReportCmd.Flags().Bool("json", false, "Output as JSON")
	deviceReportCmd.Flags().String("session-id", "", "Session ID to fetch report for (bypasses active session)")
	deviceCmd.AddCommand(deviceTargetsCmd)
	deviceCmd.AddCommand(deviceHistoryCmd)

	// Perf
	deviceCmd.AddCommand(devicePerfCmd)
	sessionFlag(devicePerfCmd)
	devicePerfCmd.Flags().BoolP("follow", "f", true, "Continuously poll (default: true)")
	devicePerfCmd.Flags().Bool("no-follow", false, "Single snapshot then exit")
	devicePerfCmd.Flags().String("interval", "2s", "Poll interval (e.g. 1s, 500ms)")
	devicePerfCmd.Flags().Bool("json", false, "Output raw JSON per poll")
}
