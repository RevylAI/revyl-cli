package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/revyl/cli/internal/outcome"
)

// InteractInput selects one visual interaction strategy through a concrete schema.
type InteractInput struct {
	Task            string  `json:"task" jsonschema:"required,Natural-language interaction intent."`
	Strategy        string  `json:"strategy,omitempty" jsonschema:"Interaction strategy: auto semantic instruction. Default auto."`
	InteractionType string  `json:"interaction_type,omitempty" jsonschema:"Interaction type: tap type swipe clear_text double_tap long_press drag pinch shake key."`
	SessionIndex    *int    `json:"session_index,omitempty" jsonschema:"Session index. Omit for active session."`
	Target          string  `json:"target,omitempty" jsonschema:"Optional visible target; defaults to task."`
	StartTarget     string  `json:"start_target,omitempty" jsonschema:"Visible drag start target."`
	EndTarget       string  `json:"end_target,omitempty" jsonschema:"Visible drag end target."`
	Text            string  `json:"text,omitempty" jsonschema:"Text for type interaction."`
	ClearFirst      bool    `json:"clear_first,omitempty" jsonschema:"Clear the field before typing."`
	Direction       string  `json:"direction,omitempty" jsonschema:"Swipe direction: up down left right."`
	DurationMs      int     `json:"duration_ms,omitempty" jsonschema:"Gesture duration in milliseconds."`
	Scale           float64 `json:"scale,omitempty" jsonschema:"Pinch scale; greater than 1 zooms in."`
	Key             string  `json:"key,omitempty" jsonschema:"Key interaction value: ENTER or BACKSPACE."`
}

// ResolvedInteractionCoordinates records coordinates selected from the pre-action screenshot.
type ResolvedInteractionCoordinates struct {
	X      *int `json:"x,omitempty"`
	Y      *int `json:"y,omitempty"`
	StartX *int `json:"start_x,omitempty"`
	StartY *int `json:"start_y,omitempty"`
	EndX   *int `json:"end_x,omitempty"`
	EndY   *int `json:"end_y,omitempty"`
}

// DevScreenshotEvidence records an internally captured native image.
type DevScreenshotEvidence struct {
	Captured  bool    `json:"captured"`
	LatencyMs float64 `json:"latency_ms,omitempty"`
}

// DevValidationOutput is the focused semantic validation contract without workflow hints.
type DevValidationOutput struct {
	Success    bool                   `json:"success"`
	Outcome    outcome.Envelope       `json:"outcome"`
	StepID     string                 `json:"step_id,omitempty"`
	SessionID  string                 `json:"session_id,omitempty"`
	StepOutput map[string]any         `json:"step_output,omitempty"`
	Screenshot *DevScreenshotEvidence `json:"screenshot,omitempty"`
	Error      string                 `json:"error,omitempty"`
}

// DevProfileActionOutput wraps one selected action with its structured result.
type DevProfileActionOutput struct {
	Action              string                          `json:"action"`
	StrategyUsed        string                          `json:"strategy_used,omitempty"`
	ResolvedTarget      string                          `json:"resolved_target,omitempty"`
	ResolvedCoordinates *ResolvedInteractionCoordinates `json:"resolved_coordinates,omitempty"`
	Result              map[string]any                  `json:"result"`
	Outcome             outcome.Envelope                `json:"outcome"`
	PreScreenshot       *DevScreenshotEvidence          `json:"pre_screenshot,omitempty"`
	PostScreenshot      *DevScreenshotEvidence          `json:"post_screenshot,omitempty"`
}

// DeviceSessionInput selects one device-session lifecycle operation.
type DeviceSessionInput struct {
	Action         string   `json:"action" jsonschema:"required,Session action: start stop list switch info doctor"`
	Platform       string   `json:"platform,omitempty" jsonschema:"Platform for start: ios or android."`
	AppID          string   `json:"app_id,omitempty" jsonschema:"App ID for start."`
	BuildVersionID string   `json:"build_version_id,omitempty" jsonschema:"Build version ID for start."`
	AppURL         string   `json:"app_url,omitempty" jsonschema:"Direct app artifact URL for start."`
	AppLink        string   `json:"app_link,omitempty" jsonschema:"Deep link to open after app launch."`
	LaunchVars     []string `json:"launch_vars,omitempty" jsonschema:"Organization launch-variable keys or IDs."`
	Timeout        int      `json:"timeout,omitempty" jsonschema:"Idle timeout in seconds."`
	SessionIndex   *int     `json:"session_index,omitempty" jsonschema:"Session index for stop switch or info."`
	All            bool     `json:"all,omitempty" jsonschema:"Stop every session."`
}

// registerDevProfileTools registers the focused development and device surface.
func (s *Server) registerDevProfileTools() {
	s.registerSetupStatusTool()
	s.registerScreenshotTool()
	s.registerDeviceNavigateTool()
	s.registerDevLoopTools()
	s.registerInteractTool()
	s.registerDeviceSessionTool()
	s.registerRebuildTool()
	s.registerDevValidationTool()
}

// registerInteractTool registers the single typed gesture-dispatch tool.
func (s *Server) registerInteractTool() {
	addDevHybridTool(s.mcpServer, &mcp.Tool{
		Meta:        screenshotAppToolMeta(),
		Name:        "interact",
		Description: "Capture the screen, resolve natural-language targets in Revyl, execute the interaction, and return the resulting screen.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Interact With Device",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleInteract)
}

// registerDeviceSessionTool registers the compact session lifecycle tool.
func (s *Server) registerDeviceSessionTool() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_session",
		Description: "Start, stop, list, switch, inspect, or diagnose Revyl device sessions.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Manage Device Session",
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleDeviceSession)
}

// registerDevValidationTool registers the focused semantic validation contract.
func (s *Server) registerDevValidationTool() {
	addDevHybridTool(s.mcpServer, &mcp.Tool{
		Meta:        screenshotAppToolMeta(),
		Name:        "device_validation",
		Description: "Validate one visible outcome and return an authoritative semantic result.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Validate Device Outcome",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleDevValidation)
}

// addDevHybridTool registers a typed tool with image-plus-JSON compatibility content.
//
// Parameters:
//   - server: MCP server that owns the tool.
//   - tool: Tool definition and schemas.
//   - handler: Typed handler that may return native image content.
func addDevHybridTool[In, Out any](
	server *mcp.Server,
	tool *mcp.Tool,
	handler mcp.ToolHandlerFor[In, Out],
) {
	mcp.AddTool(server, tool, withDevHybridContent(handler))
}

// withDevHybridContent mirrors typed output into text when native image content is present.
//
// Parameters:
//   - handler: Typed MCP handler to adapt.
//
// Returns:
//   - mcp.ToolHandlerFor[In, Out]: Handler that preserves images and structured output.
func withDevHybridContent[In, Out any](
	handler mcp.ToolHandlerFor[In, Out],
) mcp.ToolHandlerFor[In, Out] {
	return func(
		ctx context.Context,
		req *mcp.CallToolRequest,
		input In,
	) (*mcp.CallToolResult, Out, error) {
		result, output, err := handler(ctx, req, input)
		if err != nil || result == nil || !containsImageContent(result.Content) {
			return result, output, err
		}
		encoded, err := json.Marshal(output)
		if err != nil {
			return result, output, fmt.Errorf("encode hybrid MCP result: %w", err)
		}
		result.Content = append(
			[]mcp.Content{&mcp.TextContent{Text: string(encoded)}},
			result.Content...,
		)
		return result, output, nil
	}
}

// containsImageContent reports whether an MCP result contains native visual evidence.
//
// Parameters:
//   - content: MCP content blocks to inspect.
//
// Returns:
//   - bool: True when at least one block is an image.
func containsImageContent(content []mcp.Content) bool {
	for _, item := range content {
		if _, ok := item.(*mcp.ImageContent); ok {
			return true
		}
	}
	return false
}

// handleDevValidation adapts the shared evaluator without success-path next steps.
func (s *Server) handleDevValidation(
	ctx context.Context,
	req *mcp.CallToolRequest,
	input DeviceValidationInput,
) (*mcp.CallToolResult, DevValidationOutput, error) {
	if failure := s.refreshDevAuthentication(); failure != nil {
		return &mcp.CallToolResult{IsError: true}, DevValidationOutput{
			Success: false,
			Outcome: failedAuthenticationOutcome(failure),
			Error:   failure.Message,
		}, nil
	}
	toolResult, validation, err := s.handleDeviceValidation(ctx, req, input)
	sanitizeDevStructuredResult(validation.StepOutput)
	output := DevValidationOutput{
		Success:    validation.Success,
		Outcome:    outcome.Completed(),
		StepID:     validation.StepID,
		SessionID:  validation.SessionID,
		StepOutput: validation.StepOutput,
		Error:      validation.Error,
	}
	if err != nil {
		output.Success = false
		output.Error = err.Error()
		output.Outcome = outcome.Failed("validation_failed", output.Error, false)
	} else if !validation.Success {
		output.Outcome = outcome.Failed("validation_failed", validation.Error, false)
	}

	screenshotResult, screenshot, screenshotErr := s.handleScreenshot(ctx, req, ScreenshotInput{
		SessionIndex: input.SessionIndex,
	})
	if screenshotErr == nil && screenshot.Success {
		output.Screenshot = &DevScreenshotEvidence{Captured: true, LatencyMs: screenshot.LatencyMs}
		toolResult = screenshotResult
	}
	if !output.Success {
		if toolResult == nil {
			toolResult = &mcp.CallToolResult{}
		}
		toolResult.IsError = true
	}
	return toolResult, output, nil
}

// handleInteract owns pre/post screenshots and dispatches the selected strategy.
func (s *Server) handleInteract(
	ctx context.Context,
	req *mcp.CallToolRequest,
	input InteractInput,
) (*mcp.CallToolResult, DevProfileActionOutput, error) {
	if failure := s.refreshDevAuthentication(); failure != nil {
		return devProfileAuthenticationError("interact", failure)
	}
	if strings.TrimSpace(input.Task) == "" {
		return &mcp.CallToolResult{IsError: true}, devProfileInputError("interact", "task is required"), nil
	}
	sidx := -1
	if input.SessionIndex != nil {
		sidx = *input.SessionIndex
	}
	session, err := s.resolveSessionWithHydration(ctx, sidx)
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, devProfileInputError(input.InteractionType, err.Error()), nil
	}
	input.SessionIndex = &session.Index

	preResult, preScreenshot, preErr := s.handleScreenshot(
		ctx,
		req,
		ScreenshotInput{SessionIndex: &session.Index},
	)
	if preErr != nil || !preScreenshot.Success {
		reason := preScreenshot.Error
		if preErr != nil {
			reason = preErr.Error()
		}
		output := devProfileInputError(input.InteractionType, reason)
		output.Outcome = outcome.Failed("visual_refresh_failed", reason, true)
		return &mcp.CallToolResult{IsError: true}, output, nil
	}

	strategy, err := selectInteractionStrategy(input)
	if err != nil {
		output := devProfileInputError(input.InteractionType, err.Error())
		output.PreScreenshot = &DevScreenshotEvidence{Captured: true, LatencyMs: preScreenshot.LatencyMs}
		preResult.IsError = true
		return preResult, output, nil
	}
	toolResult, output, err := s.dispatchInteract(ctx, req, input, strategy, preScreenshot.ScreenToken)
	output.StrategyUsed = strategy
	output.PreScreenshot = &DevScreenshotEvidence{Captured: true, LatencyMs: preScreenshot.LatencyMs}
	output.ResolvedTarget = firstNonEmptyInteractionTarget(input)
	output.ResolvedCoordinates = resolvedCoordinatesFromResult(output.Result)
	if err != nil || output.Result["success"] != true {
		if toolResult == nil {
			toolResult = preResult
		}
		toolResult.IsError = true
		return toolResult, output, err
	}

	screenshotResult, screenshot, screenshotErr := s.handleScreenshot(
		ctx,
		req,
		ScreenshotInput{SessionIndex: &session.Index},
	)
	if screenshotErr != nil || !screenshot.Success {
		reason := screenshot.Error
		if screenshotErr != nil {
			reason = screenshotErr.Error()
		}
		toolResult, failedOutput := devProfileVisualRefreshFailure(output, reason, false)
		return toolResult, failedOutput, nil
	}
	output.PostScreenshot = &DevScreenshotEvidence{Captured: true, LatencyMs: screenshot.LatencyMs}
	return screenshotResult, output, nil
}

// dispatchInteract validates and dispatches one interaction to existing handlers.
func (s *Server) dispatchInteract(
	ctx context.Context,
	req *mcp.CallToolRequest,
	input InteractInput,
	strategy string,
	screenToken string,
) (*mcp.CallToolResult, DevProfileActionOutput, error) {
	action := strings.ToLower(strings.TrimSpace(input.InteractionType))
	if strategy == "instruction" {
		return dispatchDevProfileAction(ctx, req, "instruction", DeviceInstructionInput{
			Description:  input.Task,
			SessionIndex: input.SessionIndex,
		}, s.handleDeviceInstruction)
	}
	if action == "" {
		return nil, devProfileInputError(action, "interaction_type is required for semantic or coordinate strategy"), nil
	}
	target := firstNonEmptyInteractionTarget(input)
	switch action {
	case "tap":
		return dispatchDevProfileAction(ctx, req, action, DeviceTapInput{
			Target: target, SessionIndex: input.SessionIndex,
		}, s.handleDeviceTap)
	case "type":
		return dispatchDevProfileAction(ctx, req, action, DeviceTypeInput{
			Target: target, Text: input.Text, ClearFirst: input.ClearFirst,
			SessionIndex: input.SessionIndex,
		}, s.handleDeviceType)
	case "swipe":
		return dispatchDevProfileAction(ctx, req, action, DeviceSwipeInput{
			Target: target, Direction: input.Direction, DurationMs: input.DurationMs,
			SessionIndex: input.SessionIndex,
		}, s.handleDeviceSwipe)
	case "clear_text":
		return dispatchDevProfileAction(ctx, req, action, DeviceClearTextInput{
			Target: target, SessionIndex: input.SessionIndex,
		}, s.handleDeviceClearText)
	case "double_tap":
		return dispatchDevProfileAction(ctx, req, action, DeviceDoubleTapInput{
			Target: target, SessionIndex: input.SessionIndex,
		}, s.handleDeviceDoubleTap)
	case "long_press":
		return dispatchDevProfileAction(ctx, req, action, DeviceLongPressInput{
			Target: target, DurationMs: input.DurationMs,
			SessionIndex: input.SessionIndex,
		}, s.handleDeviceLongPress)
	case "drag":
		return s.dispatchGroundedDrag(ctx, req, input, screenToken)
	case "pinch":
		return dispatchDevProfileAction(ctx, req, action, DevicePinchInput{
			Target: target, Scale: input.Scale, DurationMs: input.DurationMs,
			SessionIndex: input.SessionIndex,
		}, s.handleDevicePinch)
	case "shake":
		return dispatchDevProfileAction(ctx, req, action, DeviceShakeInput{
			SessionIndex: input.SessionIndex,
		}, s.handleDeviceShake)
	case "key":
		return dispatchDevProfileAction(ctx, req, action, DeviceKeyInput{
			Key: input.Key, SessionIndex: input.SessionIndex,
		}, s.handleDeviceKey)
	default:
		return nil, DevProfileActionOutput{Action: action, Result: map[string]any{
			"success": false,
			"error":   "interaction_type must be tap, type, swipe, clear_text, double_tap, long_press, drag, pinch, shake, or key",
		}}, nil
	}
}

// selectInteractionStrategy resolves the requested or automatic execution strategy.
func selectInteractionStrategy(input InteractInput) (string, error) {
	strategy := strings.ToLower(strings.TrimSpace(input.Strategy))
	if strategy == "" || strategy == "auto" {
		if strings.TrimSpace(input.InteractionType) == "" {
			return "instruction", nil
		}
		return "semantic", nil
	}
	switch strategy {
	case "semantic", "instruction":
		return strategy, nil
	default:
		return "", fmt.Errorf("strategy must be auto, semantic, or instruction")
	}
}

// firstNonEmptyInteractionTarget returns the explicit target or task text.
func firstNonEmptyInteractionTarget(input InteractInput) string {
	if strings.TrimSpace(input.Target) != "" {
		return strings.TrimSpace(input.Target)
	}
	return strings.TrimSpace(input.Task)
}

// dispatchGroundedDrag grounds both visible endpoints against the pre-action screen.
func (s *Server) dispatchGroundedDrag(
	ctx context.Context,
	req *mcp.CallToolRequest,
	input InteractInput,
	screenToken string,
) (*mcp.CallToolResult, DevProfileActionOutput, error) {
	if strings.TrimSpace(input.StartTarget) == "" || strings.TrimSpace(input.EndTarget) == "" {
		return nil, devProfileInputError("drag", "start_target and end_target are required for drag"), nil
	}
	sidx := -1
	if input.SessionIndex != nil {
		sidx = *input.SessionIndex
	}
	start, err := s.resolveCoordsFromAnchor(ctx, input.StartTarget, screenToken, sidx)
	if err != nil {
		output := devProfileInputError("drag", err.Error())
		output.Outcome = outcome.Failed("grounding_failed", err.Error(), true)
		return &mcp.CallToolResult{IsError: true}, output, nil
	}
	end, err := s.resolveCoordsFromAnchor(ctx, input.EndTarget, screenToken, sidx)
	if err != nil {
		output := devProfileInputError("drag", err.Error())
		output.Outcome = outcome.Failed("grounding_failed", err.Error(), true)
		return &mcp.CallToolResult{IsError: true}, output, nil
	}
	toolResult, output, handlerErr := dispatchDevProfileAction(ctx, req, "drag", DeviceDragInput{
		StartX: start.X, StartY: start.Y, EndX: end.X, EndY: end.Y, SessionIndex: input.SessionIndex,
	}, s.handleDeviceDrag)
	if output.Result == nil {
		output.Result = make(map[string]any)
	}
	output.Result["start_x"] = start.X
	output.Result["start_y"] = start.Y
	output.Result["end_x"] = end.X
	output.Result["end_y"] = end.Y
	return toolResult, output, handlerErr
}

// resolvedCoordinatesFromResult extracts coordinates returned by existing handlers.
func resolvedCoordinatesFromResult(result map[string]any) *ResolvedInteractionCoordinates {
	if len(result) == 0 {
		return nil
	}
	coordinates := &ResolvedInteractionCoordinates{
		X:      numericResultPointer(result["x"]),
		Y:      numericResultPointer(result["y"]),
		StartX: numericResultPointer(result["start_x"]),
		StartY: numericResultPointer(result["start_y"]),
		EndX:   numericResultPointer(result["end_x"]),
		EndY:   numericResultPointer(result["end_y"]),
	}
	if coordinates.X == nil && coordinates.Y == nil &&
		coordinates.StartX == nil && coordinates.StartY == nil &&
		coordinates.EndX == nil && coordinates.EndY == nil {
		return nil
	}
	return coordinates
}

// numericResultPointer converts a JSON-normalized number to an integer pointer.
func numericResultPointer(value any) *int {
	switch number := value.(type) {
	case float64:
		converted := int(number)
		return &converted
	case int:
		converted := number
		return &converted
	default:
		return nil
	}
}

// handleDeviceSession validates and dispatches a session lifecycle action.
func (s *Server) handleDeviceSession(
	ctx context.Context,
	req *mcp.CallToolRequest,
	input DeviceSessionInput,
) (*mcp.CallToolResult, DevProfileActionOutput, error) {
	action := strings.ToLower(strings.TrimSpace(input.Action))
	if failure := s.refreshDevAuthentication(); failure != nil {
		return devProfileAuthenticationError(action, failure)
	}
	switch action {
	case "start":
		return dispatchDevProfileAction(ctx, req, action, StartDeviceSessionInput{
			Platform: input.Platform, AppID: input.AppID, BuildVersionID: input.BuildVersionID,
			AppURL: input.AppURL, AppLink: input.AppLink, LaunchVars: input.LaunchVars,
			IdleTimeout: input.Timeout, NoOpen: true,
		}, s.handleStartDeviceSession)
	case "stop":
		return dispatchDevProfileAction(ctx, req, action, StopDeviceSessionInput{
			SessionIndex: input.SessionIndex, All: input.All,
		}, s.handleStopDeviceSession)
	case "list":
		return dispatchDevProfileAction(ctx, req, action, ListDeviceSessionsInput{}, s.handleListDeviceSessions)
	case "switch":
		if input.SessionIndex == nil {
			return nil, devProfileInputError(action, "session_index is required for switch"), nil
		}
		return dispatchDevProfileAction(ctx, req, action, SwitchDeviceSessionInput{
			Index: *input.SessionIndex,
		}, s.handleSwitchDeviceSession)
	case "info":
		return dispatchDevProfileAction(ctx, req, action, GetSessionInfoInput{
			SessionIndex: input.SessionIndex,
		}, s.handleGetSessionInfo)
	case "doctor":
		return dispatchDevProfileAction(ctx, req, action, DeviceDoctorInput{}, s.handleDeviceDoctor)
	default:
		return nil, devProfileInputError(action, "action must be start, stop, list, switch, info, or doctor"), nil
	}
}

// dispatchDevProfileAction invokes an existing typed handler and wraps its output.
func dispatchDevProfileAction[I any, O any](
	ctx context.Context,
	req *mcp.CallToolRequest,
	action string,
	input I,
	handler func(context.Context, *mcp.CallToolRequest, I) (*mcp.CallToolResult, O, error),
) (*mcp.CallToolResult, DevProfileActionOutput, error) {
	toolResult, output, err := handler(ctx, req, input)
	return wrapDevProfileAction(action, toolResult, output, err)
}

// wrapDevProfileAction converts an existing typed handler result to the dev-profile envelope.
func wrapDevProfileAction[O any](
	action string,
	toolResult *mcp.CallToolResult,
	output O,
	err error,
) (*mcp.CallToolResult, DevProfileActionOutput, error) {
	if err != nil {
		return toolResult, DevProfileActionOutput{Action: action}, err
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return toolResult, DevProfileActionOutput{Action: action}, fmt.Errorf("encode %s result: %w", action, err)
	}
	result := make(map[string]any)
	if err := json.Unmarshal(encoded, &result); err != nil {
		return toolResult, DevProfileActionOutput{Action: action}, fmt.Errorf("normalize %s result: %w", action, err)
	}
	sanitizeDevStructuredResult(result)
	envelope := outcome.Completed()
	if success, present := result["success"].(bool); present && !success {
		reason, _ := result["error"].(string)
		envelope = outcome.Failed("operation_failed", reason, false)
	}
	return toolResult, DevProfileActionOutput{Action: action, Result: result, Outcome: envelope}, nil
}

// sanitizeDevStructuredResult removes presentation-only fields from dev output.
func sanitizeDevStructuredResult(result map[string]any) {
	delete(result, "next_steps")
	delete(result, "image")
	if stepOutput, ok := result["step_output"].(map[string]any); ok {
		delete(stepOutput, "next_steps")
		delete(stepOutput, "image")
	}
}

// devProfileVisualRefreshFailure synchronizes a failed visual refresh across MCP result contracts.
//
// Parameters:
//   - output: Interaction output to mark as failed.
//   - reason: Screenshot failure reason exposed to the client.
//   - retryable: Whether replaying the interaction is safe.
//
// Returns:
//   - *mcp.CallToolResult: MCP error metadata.
//   - DevProfileActionOutput: Structured interaction failure.
func devProfileVisualRefreshFailure(
	output DevProfileActionOutput,
	reason string,
	retryable bool,
) (*mcp.CallToolResult, DevProfileActionOutput) {
	if output.Result == nil {
		output.Result = make(map[string]any)
	}
	output.Result["success"] = false
	output.Result["error"] = reason
	output.Outcome = outcome.Failed("visual_refresh_failed", reason, retryable)
	return &mcp.CallToolResult{IsError: true}, output
}

// devProfileInputError returns one structured input-validation failure.
func devProfileInputError(action, message string) DevProfileActionOutput {
	return DevProfileActionOutput{Action: action, Result: map[string]any{
		"success": false,
		"error":   message,
	}, Outcome: outcome.Failed("invalid_input", message, false)}
}

// devProfileAuthenticationError returns one structured authentication failure for composite dev tools.
//
// Parameters:
//   - action: Requested dev-profile action.
//   - failure: Authentication failure returned by the central credential gate.
//
// Returns:
//   - *mcp.CallToolResult: MCP error metadata.
//   - DevProfileActionOutput: Structured authentication failure.
//   - error: Always nil because authentication failures are tool results.
func devProfileAuthenticationError(
	action string,
	failure *devAuthenticationFailure,
) (*mcp.CallToolResult, DevProfileActionOutput, error) {
	return &mcp.CallToolResult{IsError: true}, DevProfileActionOutput{
		Action: action,
		Result: map[string]any{
			"success":    false,
			"error":      failure.Message,
			"error_code": failure.Code,
		},
		Outcome: failedAuthenticationOutcome(failure),
	}, nil
}
