package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NextStep suggests a follow-up action to the agent.
type NextStep struct {
	Tool   string `json:"tool"`
	Params string `json:"params,omitempty"`
	Reason string `json:"reason"`
}

// boolPtr returns a pointer to a bool value. Used for ToolAnnotations fields.
func boolPtr(b bool) *bool { return &b }

// registerDeviceTools registers all device interaction MCP tools.
func (s *Server) registerDeviceTools() {
	// Session management
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "start_device_session",
		Description: "Provision a cloud-hosted Android or iOS device. Only platform is required. Returns a viewer_url to watch the device live in a browser.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Start Device Session",
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleStartDeviceSession)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "stop_device_session",
		Description: "Release the current device session and stop billing.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Stop Device Session",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleStopDeviceSession)

	// Device actions (grounded by default)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_tap",
		Description: "Tap an element by description (grounded) or coordinates (raw). Provide target OR x+y.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Tap Element",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceTap)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_double_tap",
		Description: "Double-tap an element by description (grounded) or coordinates (raw). Provide target OR x+y.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Double Tap Element",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceDoubleTap)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_long_press",
		Description: "Long press an element by description (grounded) or coordinates (raw). Provide target OR x+y.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Long Press Element",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceLongPress)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_type",
		Description: "Type text into an element by description (grounded) or coordinates (raw). Provide target OR x+y, plus text.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Type Text",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceType)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_swipe",
		Description: "Swipe from an element. direction='up' moves finger up (scrolls content down). Provide target OR x+y, plus direction.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Swipe on Device",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceSwipe)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_drag",
		Description: "Drag from one point to another using raw coordinates.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Drag on Device",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceDrag)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "screenshot",
		Description: "Capture the current device screen as a PNG image. Returns the image natively for rendering.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Take Screenshot",
			ReadOnlyHint: true,
		},
	}, s.handleScreenshot)

	// Standalone grounding
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "find_element",
		Description: "Locate a UI element by description without acting. Returns coordinates for assertions or batch lookups.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Find Element",
			ReadOnlyHint: true,
		},
	}, s.handleFindElement)

	// App management
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "install_app",
		Description: "Install an app on the device from a remote URL (.apk for Android, .ipa for iOS).",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Install App",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleInstallApp)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "launch_app",
		Description: "Launch an installed app by bundle ID.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Launch App",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleLaunchApp)

	// Info
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_session_info",
		Description: "Get current device session status, platform, viewer URL, and time remaining.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Session Info",
			ReadOnlyHint: true,
		},
	}, s.handleGetSessionInfo)

	// Diagnostics
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_doctor",
		Description: "Run diagnostics on auth, session health, worker reachability, grounding model availability, and environment. First aid for any issue.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Device Doctor",
			ReadOnlyHint: true,
		},
	}, s.handleDeviceDoctor)

	// Multi-session management
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_device_sessions",
		Description: "List all active device sessions with their index, platform, status, and uptime.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Device Sessions",
			ReadOnlyHint: true,
		},
	}, s.handleListDeviceSessions)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "switch_device_session",
		Description: "Switch the active session to the given index. Subsequent commands will target this session by default.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Switch Active Session",
			DestructiveHint: boolPtr(false),
		},
	}, s.handleSwitchDeviceSession)
}

// --- Session Management ---

// StartDeviceSessionInput defines input for start_device_session.
type StartDeviceSessionInput struct {
	Platform       string `json:"platform" jsonschema:"Target platform: ios or android (REQUIRED)"`
	AppID          string `json:"app_id,omitempty" jsonschema:"App ID to pre-install"`
	BuildVersionID string `json:"build_version_id,omitempty" jsonschema:"Specific build version ID"`
	TestID         string `json:"test_id,omitempty" jsonschema:"Test ID to link session to"`
	SandboxID      string `json:"sandbox_id,omitempty" jsonschema:"Sandbox ID for dedicated device"`
	IdleTimeout    int    `json:"idle_timeout,omitempty" jsonschema:"Idle timeout in seconds (default 300)"`
}

// StartDeviceSessionOutput defines output for start_device_session.
type StartDeviceSessionOutput struct {
	Success      bool       `json:"success"`
	SessionID    string     `json:"session_id,omitempty"`
	SessionIndex int        `json:"session_index"`
	Platform     string     `json:"platform,omitempty"`
	ViewerURL    string     `json:"viewer_url,omitempty"`
	Error        string     `json:"error,omitempty"`
	NextSteps    []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleStartDeviceSession(ctx context.Context, req *mcp.CallToolRequest, input StartDeviceSessionInput) (*mcp.CallToolResult, StartDeviceSessionOutput, error) {
	if input.Platform == "" {
		return nil, StartDeviceSessionOutput{Success: false, Error: "platform is required (ios or android)"}, nil
	}
	if input.Platform != "ios" && input.Platform != "android" {
		return nil, StartDeviceSessionOutput{Success: false, Error: "platform must be 'ios' or 'android'"}, nil
	}

	timeout := time.Duration(input.IdleTimeout) * time.Second
	idx, session, err := s.sessionMgr.StartSession(ctx, input.Platform, input.AppID, input.BuildVersionID, input.TestID, input.SandboxID, timeout)
	if err != nil {
		return nil, StartDeviceSessionOutput{Success: false, Error: err.Error()}, nil
	}

	return nil, StartDeviceSessionOutput{
		Success:      true,
		SessionID:    session.SessionID,
		SessionIndex: idx,
		Platform:     session.Platform,
		ViewerURL:    session.ViewerURL,
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "See the current device screen"},
			{Tool: "install_app", Reason: "Install an app on the device"},
		},
	}, nil
}

// StopDeviceSessionInput defines input for stop_device_session.
type StopDeviceSessionInput struct {
	SessionIndex *int `json:"session_index,omitempty" jsonschema:"Session index to stop. Omit to stop the active session."`
	All          bool `json:"all,omitempty" jsonschema:"Stop all sessions."`
}

// StopDeviceSessionOutput defines output for stop_device_session.
type StopDeviceSessionOutput struct {
	Success   bool       `json:"success"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleStopDeviceSession(ctx context.Context, req *mcp.CallToolRequest, input StopDeviceSessionInput) (*mcp.CallToolResult, StopDeviceSessionOutput, error) {
	if input.All {
		if err := s.sessionMgr.StopAllSessions(ctx); err != nil {
			return nil, StopDeviceSessionOutput{Success: false, Error: err.Error()}, nil
		}
		return nil, StopDeviceSessionOutput{Success: true}, nil
	}

	index := -1
	if input.SessionIndex != nil {
		index = *input.SessionIndex
	}
	session, err := s.sessionMgr.ResolveSession(index)
	if err != nil {
		return nil, StopDeviceSessionOutput{Success: false, Error: err.Error()}, nil
	}
	if err := s.sessionMgr.StopSession(ctx, session.Index); err != nil {
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

// resolveCoordsResult holds the output of resolveCoords, including the concrete
// session index so callers can reuse it for the subsequent worker request without
// re-resolving (which would be a TOCTOU race if the active session changed).
type resolveCoordsResult struct {
	X            int
	Y            int
	Confidence   float64
	SessionIndex int
}

// resolveCoords resolves target OR x/y to concrete coordinates for a given session.
// Returns the resolved coordinates AND the concrete session index that was used.
// Callers must use result.SessionIndex for any follow-up WorkerRequestForSession
// call to guarantee grounding and action target the same device.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - target: Natural language element description (mutually exclusive with x+y).
//   - x, y: Raw pixel coordinates (mutually exclusive with target).
//   - sessionIndex: Session index to use (-1 for active/auto).
//
// Returns:
//   - *resolveCoordsResult: Resolved coordinates and the concrete session index.
//   - error: Validation or resolution error.
func (s *Server) resolveCoords(ctx context.Context, target string, x, y *int, sessionIndex int) (*resolveCoordsResult, error) {
	hasTarget := target != ""
	hasCoords := x != nil && y != nil

	if hasTarget && hasCoords {
		return nil, fmt.Errorf("provide either target OR x+y, not both")
	}
	if !hasTarget && !hasCoords {
		return nil, fmt.Errorf("provide target (element description) or x+y (pixel coordinates)")
	}
	if (x != nil && y == nil) || (x == nil && y != nil) {
		return nil, fmt.Errorf("both x and y are required when using coordinates")
	}

	session, err := s.sessionMgr.ResolveSession(sessionIndex)
	if err != nil {
		return nil, err
	}
	s.sessionMgr.ResetIdleTimer(session.Index)

	if hasCoords {
		return &resolveCoordsResult{X: *x, Y: *y, SessionIndex: session.Index}, nil
	}

	resolved, err := s.sessionMgr.ResolveTargetForSession(ctx, session.Index, target)
	if err != nil {
		return nil, err
	}
	return &resolveCoordsResult{
		X: resolved.X, Y: resolved.Y,
		Confidence:   resolved.Confidence,
		SessionIndex: session.Index,
	}, nil
}

// errorNextSteps returns recovery-oriented NextSteps based on the error type.
func errorNextSteps(err error) []NextStep {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no active device session"):
		return []NextStep{{Tool: "start_device_session", Params: "platform=\"android\"", Reason: "Start a session first"}}
	case strings.Contains(msg, "could not locate") || strings.Contains(msg, "grounding"):
		return []NextStep{{Tool: "screenshot", Reason: "See the screen and rephrase the target description"}}
	case strings.Contains(msg, "worker"):
		return []NextStep{{Tool: "device_doctor", Reason: "Diagnose the worker issue"}}
	default:
		return []NextStep{{Tool: "device_doctor", Reason: "Run diagnostics to understand the failure"}}
	}
}

// --- Device Tap ---

// DeviceTapInput defines input for device_tap.
type DeviceTapInput struct {
	Target       string `json:"target,omitempty" jsonschema:"Element to tap. Use visible text ('Sign In button') or visual traits ('blue rectangle'). Auto-resolves via AI grounding."`
	X            *int   `json:"x,omitempty" jsonschema:"Raw X pixel coordinate (bypasses grounding)"`
	Y            *int   `json:"y,omitempty" jsonschema:"Raw Y pixel coordinate (bypasses grounding)"`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
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
	sidx := -1
	if input.SessionIndex != nil {
		sidx = *input.SessionIndex
	}
	rc, err := s.resolveCoords(ctx, input.Target, input.X, input.Y, sidx)
	if err != nil {
		return nil, DeviceTapOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	body := map[string]int{"x": rc.X, "y": rc.Y}
	_, err = s.sessionMgr.WorkerRequestForSession(ctx, rc.SessionIndex, "POST", "/tap", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceTapOutput{Success: false, X: rc.X, Y: rc.Y, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceTapOutput{
		Success: true, X: rc.X, Y: rc.Y, Confidence: rc.Confidence, LatencyMs: latency,
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "Verify the tap worked"},
		},
	}, nil
}

// --- Device Double Tap ---

type DeviceDoubleTapInput struct {
	Target       string `json:"target,omitempty" jsonschema:"Element to double-tap. Use visible text ('Sign In button') or visual traits ('blue rectangle'). Auto-resolves via AI grounding."`
	X            *int   `json:"x,omitempty" jsonschema:"Raw X pixel coordinate (bypasses grounding)"`
	Y            *int   `json:"y,omitempty" jsonschema:"Raw Y pixel coordinate (bypasses grounding)"`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
}

type DeviceDoubleTapOutput = DeviceTapOutput

func (s *Server) handleDeviceDoubleTap(ctx context.Context, req *mcp.CallToolRequest, input DeviceDoubleTapInput) (*mcp.CallToolResult, DeviceDoubleTapOutput, error) {
	start := time.Now()
	sidx := -1
	if input.SessionIndex != nil {
		sidx = *input.SessionIndex
	}
	rc, err := s.resolveCoords(ctx, input.Target, input.X, input.Y, sidx)
	if err != nil {
		return nil, DeviceDoubleTapOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	body := map[string]int{"x": rc.X, "y": rc.Y}
	_, err = s.sessionMgr.WorkerRequestForSession(ctx, rc.SessionIndex, "POST", "/double_tap", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceDoubleTapOutput{Success: false, X: rc.X, Y: rc.Y, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceDoubleTapOutput{
		Success: true, X: rc.X, Y: rc.Y, Confidence: rc.Confidence, LatencyMs: latency,
		NextSteps: []NextStep{{Tool: "screenshot", Reason: "Verify the double-tap worked"}},
	}, nil
}

// --- Device Long Press ---

type DeviceLongPressInput struct {
	Target       string `json:"target,omitempty" jsonschema:"Element to long-press. Use visible text ('Sign In button') or visual traits ('blue rectangle'). Auto-resolves via AI grounding."`
	X            *int   `json:"x,omitempty" jsonschema:"Raw X pixel coordinate (bypasses grounding)"`
	Y            *int   `json:"y,omitempty" jsonschema:"Raw Y pixel coordinate (bypasses grounding)"`
	DurationMs   int    `json:"duration_ms,omitempty" jsonschema:"Press duration in ms (default 1500)"`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
}

type DeviceLongPressOutput = DeviceTapOutput

func (s *Server) handleDeviceLongPress(ctx context.Context, req *mcp.CallToolRequest, input DeviceLongPressInput) (*mcp.CallToolResult, DeviceLongPressOutput, error) {
	start := time.Now()
	sidx := -1
	if input.SessionIndex != nil {
		sidx = *input.SessionIndex
	}
	rc, err := s.resolveCoords(ctx, input.Target, input.X, input.Y, sidx)
	if err != nil {
		return nil, DeviceLongPressOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	dur := input.DurationMs
	if dur == 0 {
		dur = 1500
	}
	body := map[string]int{"x": rc.X, "y": rc.Y, "duration_ms": dur}
	_, err = s.sessionMgr.WorkerRequestForSession(ctx, rc.SessionIndex, "POST", "/longpress", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceLongPressOutput{Success: false, X: rc.X, Y: rc.Y, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceLongPressOutput{
		Success: true, X: rc.X, Y: rc.Y, Confidence: rc.Confidence, LatencyMs: latency,
		NextSteps: []NextStep{{Tool: "screenshot", Reason: "Verify the long press worked"}},
	}, nil
}

// --- Device Type ---

type DeviceTypeInput struct {
	Target       string `json:"target,omitempty" jsonschema:"Element to type into (e.g. 'email input field')"`
	X            *int   `json:"x,omitempty" jsonschema:"Raw X coordinate"`
	Y            *int   `json:"y,omitempty" jsonschema:"Raw Y coordinate"`
	Text         string `json:"text" jsonschema:"Text to type (REQUIRED)"`
	ClearFirst   bool   `json:"clear_first,omitempty" jsonschema:"Clear field before typing (default true)"`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
}

type DeviceTypeOutput = DeviceTapOutput

func (s *Server) handleDeviceType(ctx context.Context, req *mcp.CallToolRequest, input DeviceTypeInput) (*mcp.CallToolResult, DeviceTypeOutput, error) {
	if input.Text == "" {
		return nil, DeviceTypeOutput{Success: false, Error: "text is required -- provide the text to type into the field"}, nil
	}
	start := time.Now()
	sidx := -1
	if input.SessionIndex != nil {
		sidx = *input.SessionIndex
	}
	rc, err := s.resolveCoords(ctx, input.Target, input.X, input.Y, sidx)
	if err != nil {
		return nil, DeviceTypeOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	// ClearFirst defaults to true (clear the field before typing).
	clearFirst := true
	if req != nil {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(req.Params.Arguments, &raw); err == nil {
			if _, exists := raw["clear_first"]; exists {
				clearFirst = input.ClearFirst
			}
		}
	}
	body := map[string]interface{}{"x": rc.X, "y": rc.Y, "text": input.Text, "clear_first": clearFirst}
	_, err = s.sessionMgr.WorkerRequestForSession(ctx, rc.SessionIndex, "POST", "/input", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceTypeOutput{Success: false, X: rc.X, Y: rc.Y, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceTypeOutput{
		Success: true, X: rc.X, Y: rc.Y, Confidence: rc.Confidence, LatencyMs: latency,
		NextSteps: []NextStep{{Tool: "screenshot", Reason: "Verify text was typed correctly"}},
	}, nil
}

// --- Device Swipe ---

type DeviceSwipeInput struct {
	Target       string `json:"target,omitempty" jsonschema:"Element to swipe from. Use visible text ('product list') or visual traits ('main content area'). Auto-resolves via AI grounding."`
	X            *int   `json:"x,omitempty" jsonschema:"Raw X pixel coordinate (bypasses grounding)"`
	Y            *int   `json:"y,omitempty" jsonschema:"Raw Y pixel coordinate (bypasses grounding)"`
	Direction    string `json:"direction" jsonschema:"Swipe direction: up, down, left, right. 'up' moves finger up (scrolls content down). REQUIRED."`
	DurationMs   int    `json:"duration_ms,omitempty" jsonschema:"Swipe duration in ms (default 500)"`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
}

type DeviceSwipeOutput = DeviceTapOutput

func (s *Server) handleDeviceSwipe(ctx context.Context, req *mcp.CallToolRequest, input DeviceSwipeInput) (*mcp.CallToolResult, DeviceSwipeOutput, error) {
	if input.Direction == "" {
		return nil, DeviceSwipeOutput{Success: false, Error: "direction is required (up, down, left, right)",
			NextSteps: []NextStep{{Tool: "screenshot", Reason: "See the screen and decide swipe direction"}},
		}, nil
	}
	validDirs := map[string]bool{"up": true, "down": true, "left": true, "right": true}
	if !validDirs[strings.ToLower(input.Direction)] {
		return nil, DeviceSwipeOutput{
			Success: false,
			Error:   fmt.Sprintf("invalid direction %q -- must be up, down, left, or right", input.Direction),
		}, nil
	}
	input.Direction = strings.ToLower(input.Direction)
	start := time.Now()
	sidx := -1
	if input.SessionIndex != nil {
		sidx = *input.SessionIndex
	}
	rc, err := s.resolveCoords(ctx, input.Target, input.X, input.Y, sidx)
	if err != nil {
		return nil, DeviceSwipeOutput{Success: false, Error: err.Error(),
			NextSteps: errorNextSteps(err),
		}, nil
	}

	dur := input.DurationMs
	if dur == 0 {
		dur = 500
	}
	body := map[string]interface{}{"x": rc.X, "y": rc.Y, "direction": input.Direction, "duration_ms": dur}
	_, err = s.sessionMgr.WorkerRequestForSession(ctx, rc.SessionIndex, "POST", "/swipe", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceSwipeOutput{Success: false, X: rc.X, Y: rc.Y, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceSwipeOutput{
		Success: true, X: rc.X, Y: rc.Y, Confidence: rc.Confidence, LatencyMs: latency,
		NextSteps: []NextStep{{Tool: "screenshot", Reason: "See the result of the swipe"}},
	}, nil
}

// --- Device Drag (raw only) ---

type DeviceDragInput struct {
	StartX       int  `json:"start_x" jsonschema:"Starting X coordinate"`
	StartY       int  `json:"start_y" jsonschema:"Starting Y coordinate"`
	EndX         int  `json:"end_x" jsonschema:"Ending X coordinate"`
	EndY         int  `json:"end_y" jsonschema:"Ending Y coordinate"`
	SessionIndex *int `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
}

type DeviceDragOutput struct {
	Success   bool       `json:"success"`
	LatencyMs float64    `json:"latency_ms"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleDeviceDrag(ctx context.Context, req *mcp.CallToolRequest, input DeviceDragInput) (*mcp.CallToolResult, DeviceDragOutput, error) {
	sidx := -1
	if input.SessionIndex != nil {
		sidx = *input.SessionIndex
	}
	session, err := s.sessionMgr.ResolveSession(sidx)
	if err != nil {
		return nil, DeviceDragOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)
	start := time.Now()
	body := map[string]int{"start_x": input.StartX, "start_y": input.StartY, "end_x": input.EndX, "end_y": input.EndY}
	_, err = s.sessionMgr.WorkerRequestForSession(ctx, session.Index, "POST", "/drag", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceDragOutput{Success: false, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceDragOutput{
		Success: true, LatencyMs: latency,
		NextSteps: []NextStep{{Tool: "screenshot", Reason: "Verify the drag result"}},
	}, nil
}

// --- Screenshot ---

type ScreenshotInput struct {
	SessionIndex *int `json:"session_index,omitempty" jsonschema:"Session index to screenshot. Omit for active session."`
}

type ScreenshotOutput struct {
	Success   bool       `json:"success"`
	LatencyMs float64    `json:"latency_ms"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleScreenshot(ctx context.Context, req *mcp.CallToolRequest, input ScreenshotInput) (*mcp.CallToolResult, ScreenshotOutput, error) {
	sidx := -1
	if input.SessionIndex != nil {
		sidx = *input.SessionIndex
	}
	session, err := s.sessionMgr.ResolveSession(sidx)
	if err != nil {
		return nil, ScreenshotOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)
	start := time.Now()
	imgBytes, err := s.sessionMgr.ScreenshotForSession(ctx, session.Index)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, ScreenshotOutput{Success: false, LatencyMs: latency, Error: err.Error(),
			NextSteps: errorNextSteps(err),
		}, nil
	}

	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.ImageContent{Data: imgBytes, MIMEType: "image/png"},
		},
	}
	return result, ScreenshotOutput{
		Success: true, LatencyMs: latency,
		NextSteps: []NextStep{
			{Tool: "device_tap", Params: "target=\"...\"", Reason: "Tap an element you see"},
			{Tool: "device_type", Params: "target=\"...\", text=\"...\"", Reason: "Type into a field"},
		},
	}, nil
}

// --- Find Element ---

type FindElementInput struct {
	Target       string `json:"target" jsonschema:"Element to locate. Use visible text or visual traits. Returns coords without acting."`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
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
		return nil, FindElementOutput{Success: false, Error: "target is required -- describe the element to locate (e.g. 'Sign In button')",
			NextSteps: []NextStep{{Tool: "screenshot", Reason: "See the screen first, then describe the element"}},
		}, nil
	}
	sidx := -1
	if input.SessionIndex != nil {
		sidx = *input.SessionIndex
	}
	session, err := s.sessionMgr.ResolveSession(sidx)
	if err != nil {
		return nil, FindElementOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)
	start := time.Now()
	resolved, err := s.sessionMgr.ResolveTargetForSession(ctx, session.Index, input.Target)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, FindElementOutput{Success: false, LatencyMs: latency, Error: err.Error(),
			NextSteps: errorNextSteps(err),
		}, nil
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
	AppURL       string `json:"app_url" jsonschema:"URL to download app from (REQUIRED)"`
	BundleID     string `json:"bundle_id,omitempty" jsonschema:"Bundle ID (auto-detected if omitted)"`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
}

type InstallAppOutput struct {
	Success   bool       `json:"success"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleInstallApp(ctx context.Context, req *mcp.CallToolRequest, input InstallAppInput) (*mcp.CallToolResult, InstallAppOutput, error) {
	if input.AppURL == "" {
		return nil, InstallAppOutput{Success: false, Error: "app_url is required -- provide a URL to an .apk (Android) or .ipa (iOS) file"}, nil
	}
	sidx := -1
	if input.SessionIndex != nil {
		sidx = *input.SessionIndex
	}
	session, err := s.sessionMgr.ResolveSession(sidx)
	if err != nil {
		return nil, InstallAppOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)

	body := map[string]string{"app_url": input.AppURL}
	if input.BundleID != "" {
		body["bundle_id"] = input.BundleID
	}
	respBody, err := s.sessionMgr.WorkerRequestForSession(ctx, session.Index, "POST", "/install", body)
	if err != nil {
		return nil, InstallAppOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
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
	BundleID     string `json:"bundle_id" jsonschema:"App bundle ID to launch (REQUIRED)"`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
}

type LaunchAppOutput struct {
	Success   bool       `json:"success"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleLaunchApp(ctx context.Context, req *mcp.CallToolRequest, input LaunchAppInput) (*mcp.CallToolResult, LaunchAppOutput, error) {
	if input.BundleID == "" {
		return nil, LaunchAppOutput{Success: false, Error: "bundle_id is required (e.g. 'com.example.app'). Use install_app first if not installed."}, nil
	}
	sidx := -1
	if input.SessionIndex != nil {
		sidx = *input.SessionIndex
	}
	session, err := s.sessionMgr.ResolveSession(sidx)
	if err != nil {
		return nil, LaunchAppOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)

	body := map[string]string{"bundle_id": input.BundleID}
	_, err = s.sessionMgr.WorkerRequestForSession(ctx, session.Index, "POST", "/launch", body)
	if err != nil {
		return nil, LaunchAppOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, LaunchAppOutput{
		Success: true,
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "See the launched app"},
		},
	}, nil
}

// --- Get Session Info ---

type GetSessionInfoInput struct {
	SessionIndex *int `json:"session_index,omitempty" jsonschema:"Session index to query. Omit for active session."`
}

type GetSessionInfoOutput struct {
	Active        bool       `json:"active"`
	SessionID     string     `json:"session_id,omitempty"`
	SessionIndex  int        `json:"session_index"`
	Platform      string     `json:"platform,omitempty"`
	ViewerURL     string     `json:"viewer_url,omitempty"`
	UptimeSeconds float64    `json:"uptime_seconds,omitempty"`
	IdleSeconds   float64    `json:"idle_seconds,omitempty"`
	TotalSessions int        `json:"total_sessions"`
	Error         string     `json:"error,omitempty"`
	NextSteps     []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleGetSessionInfo(ctx context.Context, req *mcp.CallToolRequest, input GetSessionInfoInput) (*mcp.CallToolResult, GetSessionInfoOutput, error) {
	sidx := -1
	if input.SessionIndex != nil {
		sidx = *input.SessionIndex
	}
	session, err := s.sessionMgr.ResolveSession(sidx)
	if err != nil {
		return nil, GetSessionInfoOutput{
			Active:        false,
			TotalSessions: s.sessionMgr.SessionCount(),
			NextSteps: []NextStep{
				{Tool: "start_device_session", Params: "platform=\"android\"", Reason: "Start a device session"},
			},
		}, nil
	}

	now := time.Now()
	return nil, GetSessionInfoOutput{
		Active:        true,
		SessionID:     session.SessionID,
		SessionIndex:  session.Index,
		Platform:      session.Platform,
		ViewerURL:     session.ViewerURL,
		UptimeSeconds: now.Sub(session.StartedAt).Seconds(),
		IdleSeconds:   now.Sub(session.LastActivity).Seconds(),
		TotalSessions: s.sessionMgr.SessionCount(),
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "See the current device screen"},
			{Tool: "device_tap", Params: "target=\"...\"", Reason: "Interact with an element"},
		},
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
	Checks           []DiagnosticCheck `json:"checks"`
	AllPassed        bool              `json:"all_passed"`
	TroubleshootTips []string          `json:"troubleshoot_tips,omitempty"`
	NextSteps        []NextStep        `json:"next_steps,omitempty"`
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
		respBytes, werr := s.sessionMgr.WorkerRequest(ctx, "GET", "/health", nil)
		if werr != nil {
			checks = append(checks, DiagnosticCheck{Name: "worker", Status: "fail", Detail: werr.Error(), Fix: "stop_device_session() and start a new one"})
			allPassed = false
		} else {
			checks = append(checks, DiagnosticCheck{Name: "worker", Status: "pass"})

			// Check 3b: Device connectivity (parse /health response body)
			var health struct {
				DeviceConnected bool `json:"device_connected"`
			}
			if json.Unmarshal(respBytes, &health) == nil {
				if health.DeviceConnected {
					checks = append(checks, DiagnosticCheck{Name: "device", Status: "pass"})
				} else {
					checks = append(checks, DiagnosticCheck{Name: "device", Status: "fail", Detail: "Worker is running but device is not connected", Fix: "stop_device_session() and start a new one"})
					allPassed = false
				}
			}
		}
	}

	// Check 4: CLI version
	checks = append(checks, DiagnosticCheck{Name: "cli_version", Status: "info", Detail: s.version})

	// Check 5: Session persistence
	persistPath := ""
	if s.sessionMgr.WorkDir() != "" {
		persistPath = s.sessionMgr.WorkDir() + "/.revyl/device-sessions.json"
		if _, fErr := os.Stat(persistPath); fErr == nil {
			checks = append(checks, DiagnosticCheck{Name: "persist_file", Status: "pass", Detail: persistPath})
		} else {
			checks = append(checks, DiagnosticCheck{Name: "persist_file", Status: "none", Detail: "No persisted session file"})
		}
	}

	// Check 7: Environment
	apiKeyMasked := maskEnv("REVYL_API_KEY")
	checks = append(checks, DiagnosticCheck{Name: "env_api_key", Status: "info", Detail: apiKeyMasked})
	checks = append(checks, DiagnosticCheck{Name: "env_local", Status: "info", Detail: envOrDefault("LOCAL", "false")})

	// Troubleshooting tips
	tips := []string{
		"If worker is unreachable, stop and start a new session.",
		"If grounding fails, try a more specific target description.",
		"Sessions auto-terminate after 5 min idle. Use get_session_info() to check.",
		"Use screenshot() before every action to see the current screen state.",
	}

	output := DeviceDoctorOutput{Checks: checks, AllPassed: allPassed, TroubleshootTips: tips}
	if allPassed {
		output.NextSteps = []NextStep{
			{Tool: "screenshot", Reason: "Everything looks good -- see the device screen"},
		}
	} else {
		output.NextSteps = []NextStep{
			{Tool: "device_doctor", Reason: "Re-run diagnostics after fixing issues"},
		}
	}
	return nil, output, nil
}

// maskEnv returns a masked version of an environment variable value.
func maskEnv(key string) string {
	val := os.Getenv(key)
	if val == "" {
		return "(not set)"
	}
	if len(val) <= 8 {
		return val[:2] + "***"
	}
	return val[:4] + "..." + val[len(val)-4:]
}

// envOrDefault returns an environment variable value or the provided default.
func envOrDefault(key, def string) string {
	val := os.Getenv(key)
	if val == "" {
		return def
	}
	return val
}

// --- List Device Sessions ---

// ListDeviceSessionsInput defines input for list_device_sessions.
type ListDeviceSessionsInput struct{}

// ListDeviceSessionsSessionItem represents a single session in the list output.
type ListDeviceSessionsSessionItem struct {
	Index    int     `json:"index"`
	Platform string  `json:"platform"`
	Status   string  `json:"status"`
	Uptime   float64 `json:"uptime_seconds"`
	Active   bool    `json:"active"`
}

// ListDeviceSessionsOutput defines output for list_device_sessions.
type ListDeviceSessionsOutput struct {
	Sessions    []ListDeviceSessionsSessionItem `json:"sessions"`
	ActiveIndex int                             `json:"active_index"`
	NextSteps   []NextStep                      `json:"next_steps,omitempty"`
}

func (s *Server) handleListDeviceSessions(ctx context.Context, req *mcp.CallToolRequest, input ListDeviceSessionsInput) (*mcp.CallToolResult, ListDeviceSessionsOutput, error) {
	sessions := s.sessionMgr.ListSessions()
	activeIdx := s.sessionMgr.ActiveIndex()

	items := make([]ListDeviceSessionsSessionItem, 0, len(sessions))
	for _, sess := range sessions {
		items = append(items, ListDeviceSessionsSessionItem{
			Index:    sess.Index,
			Platform: sess.Platform,
			Status:   "running",
			Uptime:   time.Since(sess.StartedAt).Seconds(),
			Active:   sess.Index == activeIdx,
		})
	}

	output := ListDeviceSessionsOutput{
		Sessions:    items,
		ActiveIndex: activeIdx,
	}

	if len(sessions) == 0 {
		output.NextSteps = []NextStep{
			{Tool: "start_device_session", Params: "platform=\"android\"", Reason: "No sessions -- start one"},
		}
	} else {
		output.NextSteps = []NextStep{
			{Tool: "screenshot", Reason: "See the active session's screen"},
		}
	}

	return nil, output, nil
}

// --- Switch Device Session ---

// SwitchDeviceSessionInput defines input for switch_device_session.
type SwitchDeviceSessionInput struct {
	Index int `json:"index" jsonschema:"Session index to switch to (REQUIRED)"`
}

// SwitchDeviceSessionOutput defines output for switch_device_session.
type SwitchDeviceSessionOutput struct {
	Success   bool       `json:"success"`
	Index     int        `json:"index"`
	Platform  string     `json:"platform,omitempty"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleSwitchDeviceSession(ctx context.Context, req *mcp.CallToolRequest, input SwitchDeviceSessionInput) (*mcp.CallToolResult, SwitchDeviceSessionOutput, error) {
	if err := s.sessionMgr.SetActive(input.Index); err != nil {
		return nil, SwitchDeviceSessionOutput{Success: false, Error: err.Error()}, nil
	}

	session := s.sessionMgr.GetSession(input.Index)
	platform := ""
	if session != nil {
		platform = session.Platform
	}

	return nil, SwitchDeviceSessionOutput{
		Success:  true,
		Index:    input.Index,
		Platform: platform,
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "See the newly active session's screen"},
		},
	}, nil
}
