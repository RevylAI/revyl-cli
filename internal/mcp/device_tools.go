package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NextStep suggests a follow-up action to the agent.
type NextStep struct {
	Tool   string `json:"tool"`
	Params string `json:"params,omitempty"`
	Reason string `json:"reason"`
}

// registerDeviceTools registers all device interaction MCP tools.
func (s *Server) registerDeviceTools() {
	// Session management
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "start_device_session",
		Description: "Provision a cloud-hosted Android or iOS device. Only platform is required. Returns a viewer_url to watch the device live in a browser.",
	}, s.handleStartDeviceSession)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "stop_device_session",
		Description: "Release the current device session and stop billing.",
	}, s.handleStopDeviceSession)

	// Device actions (grounded by default)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_tap",
		Description: "Tap an element by description (grounded) or coordinates (raw). Provide target OR x+y.",
	}, s.handleDeviceTap)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_double_tap",
		Description: "Double-tap an element by description (grounded) or coordinates (raw).",
	}, s.handleDeviceDoubleTap)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_long_press",
		Description: "Long press an element by description (grounded) or coordinates (raw).",
	}, s.handleDeviceLongPress)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_type",
		Description: "Type text into an element by description (grounded) or coordinates (raw). Provide target OR x+y, plus text.",
	}, s.handleDeviceType)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_swipe",
		Description: "Swipe from an element by description (grounded) or coordinates (raw). Provide target OR x+y, plus direction.",
	}, s.handleDeviceSwipe)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_drag",
		Description: "Drag from one point to another using raw coordinates.",
	}, s.handleDeviceDrag)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "screenshot",
		Description: "Capture the current device screen as a base64 PNG image.",
	}, s.handleScreenshot)

	// Standalone grounding
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "find_element",
		Description: "Locate a UI element by description without acting. Returns coordinates for assertions or batch lookups.",
	}, s.handleFindElement)

	// App management
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "install_app",
		Description: "Install an app on the device from a remote URL.",
	}, s.handleInstallApp)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "launch_app",
		Description: "Launch an installed app by bundle ID.",
	}, s.handleLaunchApp)

	// Info
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_session_info",
		Description: "Get current device session status, platform, viewer URL, and time remaining.",
	}, s.handleGetSessionInfo)

	// Diagnostics
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_doctor",
		Description: "Run diagnostics on auth, session health, worker reachability, and grounding model availability.",
	}, s.handleDeviceDoctor)
}

// --- Session Management ---

// StartDeviceSessionInput defines input for start_device_session.
type StartDeviceSessionInput struct {
	Platform       string `json:"platform" jsonschema:"description=Target platform: ios or android (REQUIRED)"`
	AppID          string `json:"app_id,omitempty" jsonschema:"description=App ID to pre-install"`
	BuildVersionID string `json:"build_version_id,omitempty" jsonschema:"description=Specific build version ID"`
	TestID         string `json:"test_id,omitempty" jsonschema:"description=Test ID to link session to"`
	SandboxID      string `json:"sandbox_id,omitempty" jsonschema:"description=Sandbox ID for dedicated device"`
	IdleTimeout    int    `json:"idle_timeout,omitempty" jsonschema:"description=Idle timeout in seconds (default 300)"`
}

// StartDeviceSessionOutput defines output for start_device_session.
type StartDeviceSessionOutput struct {
	Success   bool       `json:"success"`
	SessionID string     `json:"session_id,omitempty"`
	Platform  string     `json:"platform,omitempty"`
	ViewerURL string     `json:"viewer_url,omitempty"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleStartDeviceSession(ctx context.Context, req *mcp.CallToolRequest, input StartDeviceSessionInput) (*mcp.CallToolResult, StartDeviceSessionOutput, error) {
	if input.Platform == "" {
		return nil, StartDeviceSessionOutput{Success: false, Error: "platform is required (ios or android)"}, nil
	}
	if input.Platform != "ios" && input.Platform != "android" {
		return nil, StartDeviceSessionOutput{Success: false, Error: "platform must be 'ios' or 'android'"}, nil
	}

	timeout := time.Duration(input.IdleTimeout) * time.Second
	session, err := s.sessionMgr.StartSession(ctx, input.Platform, input.AppID, input.BuildVersionID, input.TestID, input.SandboxID, timeout)
	if err != nil {
		return nil, StartDeviceSessionOutput{Success: false, Error: err.Error()}, nil
	}

	return nil, StartDeviceSessionOutput{
		Success:   true,
		SessionID: session.SessionID,
		Platform:  session.Platform,
		ViewerURL: session.ViewerURL,
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "See the current device screen"},
			{Tool: "install_app", Reason: "Install an app on the device"},
		},
	}, nil
}

// StopDeviceSessionInput defines input for stop_device_session.
type StopDeviceSessionInput struct{}

// StopDeviceSessionOutput defines output for stop_device_session.
type StopDeviceSessionOutput struct {
	Success   bool       `json:"success"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleStopDeviceSession(ctx context.Context, req *mcp.CallToolRequest, input StopDeviceSessionInput) (*mcp.CallToolResult, StopDeviceSessionOutput, error) {
	if err := s.sessionMgr.StopSession(ctx); err != nil {
		return nil, StopDeviceSessionOutput{Success: false, Error: err.Error()}, nil
	}
	return nil, StopDeviceSessionOutput{
		Success: true,
		NextSteps: []NextStep{
			{Tool: "create_test", Reason: "Save the session as a reusable test"},
		},
	}, nil
}

// --- Dual-param validation helper ---

// resolveCoords resolves target OR x/y to concrete coordinates.
// Returns (x, y, confidence, error). Confidence is 0 when using raw coords.
func (s *Server) resolveCoords(ctx context.Context, target string, x, y *int) (int, int, float64, error) {
	s.sessionMgr.ResetIdleTimer()

	hasTarget := target != ""
	hasCoords := x != nil && y != nil

	if hasTarget && hasCoords {
		return 0, 0, 0, fmt.Errorf("provide either target OR x+y, not both")
	}
	if !hasTarget && !hasCoords {
		return 0, 0, 0, fmt.Errorf("provide target (element description) or x+y (pixel coordinates)")
	}
	if (x != nil && y == nil) || (x == nil && y != nil) {
		return 0, 0, 0, fmt.Errorf("both x and y are required when using coordinates")
	}

	if hasCoords {
		return *x, *y, 0, nil
	}

	resolved, err := s.sessionMgr.ResolveTarget(ctx, target)
	if err != nil {
		return 0, 0, 0, err
	}
	return resolved.X, resolved.Y, resolved.Confidence, nil
}

// --- Device Tap ---

// DeviceTapInput defines input for device_tap.
type DeviceTapInput struct {
	Target string `json:"target,omitempty" jsonschema:"description=Element to tap. Use visible text ('Sign In button') or visual traits ('blue rectangle'). Auto-resolves via AI grounding."`
	X      *int   `json:"x,omitempty" jsonschema:"description=Raw X pixel coordinate (bypasses grounding)"`
	Y      *int   `json:"y,omitempty" jsonschema:"description=Raw Y pixel coordinate (bypasses grounding)"`
}

// DeviceTapOutput defines output for device_tap.
type DeviceTapOutput struct {
	Success    bool       `json:"success"`
	X          int        `json:"x"`
	Y          int        `json:"y"`
	Confidence float64    `json:"confidence,omitempty"`
	LatencyMs  float64    `json:"latency_ms"`
	Error      string     `json:"error,omitempty"`
	NextSteps  []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleDeviceTap(ctx context.Context, req *mcp.CallToolRequest, input DeviceTapInput) (*mcp.CallToolResult, DeviceTapOutput, error) {
	start := time.Now()
	cx, cy, conf, err := s.resolveCoords(ctx, input.Target, input.X, input.Y)
	if err != nil {
		return nil, DeviceTapOutput{Success: false, Error: err.Error()}, nil
	}

	body := map[string]int{"x": cx, "y": cy}
	_, err = s.sessionMgr.WorkerRequest(ctx, "POST", "/tap", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceTapOutput{Success: false, X: cx, Y: cy, LatencyMs: latency, Error: err.Error()}, nil
	}

	return nil, DeviceTapOutput{
		Success: true, X: cx, Y: cy, Confidence: conf, LatencyMs: latency,
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "Verify the tap worked"},
		},
	}, nil
}

// --- Device Double Tap ---

type DeviceDoubleTapInput struct {
	Target string `json:"target,omitempty" jsonschema:"description=Element to double-tap"`
	X      *int   `json:"x,omitempty" jsonschema:"description=Raw X coordinate"`
	Y      *int   `json:"y,omitempty" jsonschema:"description=Raw Y coordinate"`
}

type DeviceDoubleTapOutput = DeviceTapOutput

func (s *Server) handleDeviceDoubleTap(ctx context.Context, req *mcp.CallToolRequest, input DeviceDoubleTapInput) (*mcp.CallToolResult, DeviceDoubleTapOutput, error) {
	start := time.Now()
	cx, cy, conf, err := s.resolveCoords(ctx, input.Target, input.X, input.Y)
	if err != nil {
		return nil, DeviceDoubleTapOutput{Success: false, Error: err.Error()}, nil
	}

	body := map[string]int{"x": cx, "y": cy}
	_, err = s.sessionMgr.WorkerRequest(ctx, "POST", "/double_tap", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceDoubleTapOutput{Success: false, X: cx, Y: cy, LatencyMs: latency, Error: err.Error()}, nil
	}

	return nil, DeviceDoubleTapOutput{
		Success: true, X: cx, Y: cy, Confidence: conf, LatencyMs: latency,
		NextSteps: []NextStep{{Tool: "screenshot", Reason: "Verify the double-tap worked"}},
	}, nil
}

// --- Device Long Press ---

type DeviceLongPressInput struct {
	Target     string `json:"target,omitempty" jsonschema:"description=Element to long-press"`
	X          *int   `json:"x,omitempty" jsonschema:"description=Raw X coordinate"`
	Y          *int   `json:"y,omitempty" jsonschema:"description=Raw Y coordinate"`
	DurationMs int    `json:"duration_ms,omitempty" jsonschema:"description=Press duration in ms (default 1500)"`
}

type DeviceLongPressOutput = DeviceTapOutput

func (s *Server) handleDeviceLongPress(ctx context.Context, req *mcp.CallToolRequest, input DeviceLongPressInput) (*mcp.CallToolResult, DeviceLongPressOutput, error) {
	start := time.Now()
	cx, cy, conf, err := s.resolveCoords(ctx, input.Target, input.X, input.Y)
	if err != nil {
		return nil, DeviceLongPressOutput{Success: false, Error: err.Error()}, nil
	}

	dur := input.DurationMs
	if dur == 0 {
		dur = 1500
	}
	body := map[string]int{"x": cx, "y": cy, "duration_ms": dur}
	_, err = s.sessionMgr.WorkerRequest(ctx, "POST", "/longpress", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceLongPressOutput{Success: false, X: cx, Y: cy, LatencyMs: latency, Error: err.Error()}, nil
	}

	return nil, DeviceLongPressOutput{
		Success: true, X: cx, Y: cy, Confidence: conf, LatencyMs: latency,
		NextSteps: []NextStep{{Tool: "screenshot", Reason: "Verify the long press worked"}},
	}, nil
}

// --- Device Type ---

type DeviceTypeInput struct {
	Target     string `json:"target,omitempty" jsonschema:"description=Element to type into (e.g. 'email input field')"`
	X          *int   `json:"x,omitempty" jsonschema:"description=Raw X coordinate"`
	Y          *int   `json:"y,omitempty" jsonschema:"description=Raw Y coordinate"`
	Text       string `json:"text" jsonschema:"description=Text to type (REQUIRED)"`
	ClearFirst bool   `json:"clear_first,omitempty" jsonschema:"description=Clear field before typing (default true)"`
}

type DeviceTypeOutput = DeviceTapOutput

func (s *Server) handleDeviceType(ctx context.Context, req *mcp.CallToolRequest, input DeviceTypeInput) (*mcp.CallToolResult, DeviceTypeOutput, error) {
	if input.Text == "" {
		return nil, DeviceTypeOutput{Success: false, Error: "text is required"}, nil
	}
	start := time.Now()
	cx, cy, conf, err := s.resolveCoords(ctx, input.Target, input.X, input.Y)
	if err != nil {
		return nil, DeviceTypeOutput{Success: false, Error: err.Error()}, nil
	}

	clearFirst := true
	if input.ClearFirst {
		clearFirst = input.ClearFirst
	}
	body := map[string]interface{}{"x": cx, "y": cy, "text": input.Text, "clear_first": clearFirst}
	_, err = s.sessionMgr.WorkerRequest(ctx, "POST", "/input", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceTypeOutput{Success: false, X: cx, Y: cy, LatencyMs: latency, Error: err.Error()}, nil
	}

	return nil, DeviceTypeOutput{
		Success: true, X: cx, Y: cy, Confidence: conf, LatencyMs: latency,
		NextSteps: []NextStep{{Tool: "screenshot", Reason: "Verify text was typed correctly"}},
	}, nil
}

// --- Device Swipe ---

type DeviceSwipeInput struct {
	Target     string `json:"target,omitempty" jsonschema:"description=Element to swipe from"`
	X          *int   `json:"x,omitempty" jsonschema:"description=Raw X coordinate"`
	Y          *int   `json:"y,omitempty" jsonschema:"description=Raw Y coordinate"`
	Direction  string `json:"direction" jsonschema:"description=Swipe direction: up down left right (REQUIRED)"`
	DurationMs int    `json:"duration_ms,omitempty" jsonschema:"description=Swipe duration in ms (default 500)"`
}

type DeviceSwipeOutput = DeviceTapOutput

func (s *Server) handleDeviceSwipe(ctx context.Context, req *mcp.CallToolRequest, input DeviceSwipeInput) (*mcp.CallToolResult, DeviceSwipeOutput, error) {
	if input.Direction == "" {
		return nil, DeviceSwipeOutput{Success: false, Error: "direction is required (up, down, left, right)"}, nil
	}
	start := time.Now()
	cx, cy, conf, err := s.resolveCoords(ctx, input.Target, input.X, input.Y)
	if err != nil {
		return nil, DeviceSwipeOutput{Success: false, Error: err.Error()}, nil
	}

	dur := input.DurationMs
	if dur == 0 {
		dur = 500
	}
	body := map[string]interface{}{"x": cx, "y": cy, "direction": input.Direction, "duration_ms": dur}
	_, err = s.sessionMgr.WorkerRequest(ctx, "POST", "/swipe", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceSwipeOutput{Success: false, X: cx, Y: cy, LatencyMs: latency, Error: err.Error()}, nil
	}

	return nil, DeviceSwipeOutput{
		Success: true, X: cx, Y: cy, Confidence: conf, LatencyMs: latency,
		NextSteps: []NextStep{{Tool: "screenshot", Reason: "See the result of the swipe"}},
	}, nil
}

// --- Device Drag (raw only) ---

type DeviceDragInput struct {
	StartX int `json:"start_x" jsonschema:"description=Starting X coordinate"`
	StartY int `json:"start_y" jsonschema:"description=Starting Y coordinate"`
	EndX   int `json:"end_x" jsonschema:"description=Ending X coordinate"`
	EndY   int `json:"end_y" jsonschema:"description=Ending Y coordinate"`
}

type DeviceDragOutput struct {
	Success   bool       `json:"success"`
	LatencyMs float64    `json:"latency_ms"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleDeviceDrag(ctx context.Context, req *mcp.CallToolRequest, input DeviceDragInput) (*mcp.CallToolResult, DeviceDragOutput, error) {
	s.sessionMgr.ResetIdleTimer()
	start := time.Now()
	body := map[string]int{"start_x": input.StartX, "start_y": input.StartY, "end_x": input.EndX, "end_y": input.EndY}
	_, err := s.sessionMgr.WorkerRequest(ctx, "POST", "/drag", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceDragOutput{Success: false, LatencyMs: latency, Error: err.Error()}, nil
	}

	return nil, DeviceDragOutput{
		Success: true, LatencyMs: latency,
		NextSteps: []NextStep{{Tool: "screenshot", Reason: "Verify the drag result"}},
	}, nil
}

// --- Screenshot ---

type ScreenshotInput struct{}

type ScreenshotOutput struct {
	Success   bool       `json:"success"`
	ImageB64  string     `json:"image_base64,omitempty"`
	LatencyMs float64    `json:"latency_ms"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleScreenshot(ctx context.Context, req *mcp.CallToolRequest, input ScreenshotInput) (*mcp.CallToolResult, ScreenshotOutput, error) {
	s.sessionMgr.ResetIdleTimer()
	start := time.Now()
	imgBytes, err := s.sessionMgr.Screenshot(ctx)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, ScreenshotOutput{Success: false, LatencyMs: latency, Error: err.Error()}, nil
	}

	b64 := base64.StdEncoding.EncodeToString(imgBytes)
	return nil, ScreenshotOutput{
		Success: true, ImageB64: b64, LatencyMs: latency,
		NextSteps: []NextStep{
			{Tool: "device_tap", Params: "target=\"...\"", Reason: "Tap an element you see"},
			{Tool: "device_type", Params: "target=\"...\", text=\"...\"", Reason: "Type into a field"},
		},
	}, nil
}

// --- Find Element ---

type FindElementInput struct {
	Target string `json:"target" jsonschema:"description=Element to locate. Use visible text or visual traits. Returns coords without acting."`
}

type FindElementOutput struct {
	Success    bool       `json:"success"`
	X          int        `json:"x"`
	Y          int        `json:"y"`
	Confidence float64    `json:"confidence"`
	LatencyMs  float64    `json:"latency_ms"`
	Error      string     `json:"error,omitempty"`
	NextSteps  []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleFindElement(ctx context.Context, req *mcp.CallToolRequest, input FindElementInput) (*mcp.CallToolResult, FindElementOutput, error) {
	if input.Target == "" {
		return nil, FindElementOutput{Success: false, Error: "target is required"}, nil
	}
	s.sessionMgr.ResetIdleTimer()
	start := time.Now()
	resolved, err := s.sessionMgr.ResolveTarget(ctx, input.Target)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, FindElementOutput{Success: false, LatencyMs: latency, Error: err.Error()}, nil
	}

	return nil, FindElementOutput{
		Success: true, X: resolved.X, Y: resolved.Y, Confidence: resolved.Confidence, LatencyMs: latency,
		NextSteps: []NextStep{
			{Tool: "device_tap", Params: fmt.Sprintf("x=%d, y=%d", resolved.X, resolved.Y), Reason: "Tap the found element"},
		},
	}, nil
}

// --- Install App ---

type InstallAppInput struct {
	AppURL   string `json:"app_url" jsonschema:"description=URL to download app from (REQUIRED)"`
	BundleID string `json:"bundle_id,omitempty" jsonschema:"description=Bundle ID (auto-detected if omitted)"`
}

type InstallAppOutput struct {
	Success   bool       `json:"success"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleInstallApp(ctx context.Context, req *mcp.CallToolRequest, input InstallAppInput) (*mcp.CallToolResult, InstallAppOutput, error) {
	if input.AppURL == "" {
		return nil, InstallAppOutput{Success: false, Error: "app_url is required"}, nil
	}
	s.sessionMgr.ResetIdleTimer()

	body := map[string]string{"app_url": input.AppURL}
	if input.BundleID != "" {
		body["bundle_id"] = input.BundleID
	}
	respBody, err := s.sessionMgr.WorkerRequest(ctx, "POST", "/install", body)
	if err != nil {
		return nil, InstallAppOutput{Success: false, Error: err.Error()}, nil
	}

	var resp struct{ Success bool }
	_ = json.Unmarshal(respBody, &resp)

	return nil, InstallAppOutput{
		Success: resp.Success,
		NextSteps: []NextStep{
			{Tool: "launch_app", Reason: "Launch the installed app"},
			{Tool: "screenshot", Reason: "See the device screen"},
		},
	}, nil
}

// --- Launch App ---

type LaunchAppInput struct {
	BundleID string `json:"bundle_id" jsonschema:"description=App bundle ID to launch (REQUIRED)"`
}

type LaunchAppOutput struct {
	Success   bool       `json:"success"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleLaunchApp(ctx context.Context, req *mcp.CallToolRequest, input LaunchAppInput) (*mcp.CallToolResult, LaunchAppOutput, error) {
	if input.BundleID == "" {
		return nil, LaunchAppOutput{Success: false, Error: "bundle_id is required"}, nil
	}
	s.sessionMgr.ResetIdleTimer()

	body := map[string]string{"bundle_id": input.BundleID}
	_, err := s.sessionMgr.WorkerRequest(ctx, "POST", "/launch", body)
	if err != nil {
		return nil, LaunchAppOutput{Success: false, Error: err.Error()}, nil
	}

	return nil, LaunchAppOutput{
		Success: true,
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "See the launched app"},
		},
	}, nil
}

// --- Get Session Info ---

type GetSessionInfoInput struct{}

type GetSessionInfoOutput struct {
	Active        bool       `json:"active"`
	SessionID     string     `json:"session_id,omitempty"`
	Platform      string     `json:"platform,omitempty"`
	ViewerURL     string     `json:"viewer_url,omitempty"`
	UptimeSeconds float64    `json:"uptime_seconds,omitempty"`
	IdleSeconds   float64    `json:"idle_seconds,omitempty"`
	Error         string     `json:"error,omitempty"`
	NextSteps     []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleGetSessionInfo(ctx context.Context, req *mcp.CallToolRequest, input GetSessionInfoInput) (*mcp.CallToolResult, GetSessionInfoOutput, error) {
	session := s.sessionMgr.GetActive()
	if session == nil {
		return nil, GetSessionInfoOutput{
			Active: false,
			NextSteps: []NextStep{
				{Tool: "start_device_session", Params: "platform=\"android\"", Reason: "Start a device session"},
			},
		}, nil
	}

	now := time.Now()
	return nil, GetSessionInfoOutput{
		Active:        true,
		SessionID:     session.SessionID,
		Platform:      session.Platform,
		ViewerURL:     session.ViewerURL,
		UptimeSeconds: now.Sub(session.StartedAt).Seconds(),
		IdleSeconds:   now.Sub(session.LastActivity).Seconds(),
	}, nil
}

// --- Device Doctor ---

type DeviceDoctorInput struct{}

type DiagnosticCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

type DeviceDoctorOutput struct {
	Checks    []DiagnosticCheck `json:"checks"`
	AllPassed bool              `json:"all_passed"`
}

func (s *Server) handleDeviceDoctor(ctx context.Context, req *mcp.CallToolRequest, input DeviceDoctorInput) (*mcp.CallToolResult, DeviceDoctorOutput, error) {
	var checks []DiagnosticCheck
	allPassed := true

	// Check 1: Auth
	_, err := s.apiClient.ValidateAPIKey(ctx)
	if err != nil {
		checks = append(checks, DiagnosticCheck{Name: "auth", Status: "fail", Detail: err.Error(), Fix: "Set REVYL_API_KEY or run 'revyl auth login'"})
		allPassed = false
	} else {
		checks = append(checks, DiagnosticCheck{Name: "auth", Status: "pass"})
	}

	// Check 2: Active session
	session := s.sessionMgr.GetActive()
	if session == nil {
		checks = append(checks, DiagnosticCheck{Name: "session", Status: "none", Detail: "No active session", Fix: "Call start_device_session(platform='android')"})
	} else {
		checks = append(checks, DiagnosticCheck{Name: "session", Status: "pass", Detail: fmt.Sprintf("platform=%s, uptime=%.0fs", session.Platform, time.Since(session.StartedAt).Seconds())})

		// Check 3: Worker reachability (only if session exists)
		_, err := s.sessionMgr.WorkerRequest(ctx, "GET", "/health", nil)
		if err != nil {
			checks = append(checks, DiagnosticCheck{Name: "worker", Status: "fail", Detail: err.Error(), Fix: "stop_device_session() and start a new one"})
			allPassed = false
		} else {
			checks = append(checks, DiagnosticCheck{Name: "worker", Status: "pass"})
		}
	}

	return nil, DeviceDoctorOutput{Checks: checks, AllPassed: allPassed}, nil
}
