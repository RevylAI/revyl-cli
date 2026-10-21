package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/revyl/cli/internal/api"
)

const (
	computerMaxHoldSeconds = 5
	computerMaxWaitSeconds = 10
)

var computerActions = []string{
	"screenshot", "zoom", "left_click", "right_click", "middle_click",
	"double_click", "triple_click", "mouse_move", "left_click_drag",
	"left_mouse_down", "left_mouse_up", "scroll", "type", "key",
	"hold_key", "wait", "cursor_position",
}

type ComputerToolInput struct {
	ComputerSessionID string   `json:"computer_session_id" jsonschema:"required,Computer run UUID to target exactly."`
	Action            string   `json:"action" jsonschema:"required,Operation: screenshot zoom left_click right_click middle_click double_click triple_click mouse_move left_click_drag left_mouse_down left_mouse_up scroll type key hold_key wait or cursor_position."`
	Coordinate        []int    `json:"coordinate,omitempty" jsonschema:"Screenshot-space [x,y] coordinate."`
	StartCoordinate   []int    `json:"start_coordinate,omitempty" jsonschema:"Screenshot-space [x,y] drag start coordinate."`
	Text              string   `json:"text,omitempty" jsonschema:"Text to type, key chord, or comma-separated click modifiers."`
	ScrollDirection   string   `json:"scroll_direction,omitempty" jsonschema:"Scroll direction: up, down, left, or right."`
	ScrollAmount      *int     `json:"scroll_amount,omitempty" jsonschema:"Scroll ticks, from 1 to 20."`
	Duration          *float64 `json:"duration,omitempty" jsonschema:"Hold or wait duration in seconds."`
	Region            []int    `json:"region,omitempty" jsonschema:"Zoom crop [x0,y0,x1,y1] in screenshot pixels."`
}

type ComputerToolOutput struct {
	Width  int                      `json:"width,omitempty"`
	Height int                      `json:"height,omitempty"`
	Cursor *api.ComputerScreenPoint `json:"cursor"`
	Error  string                   `json:"error,omitempty"`
}

func WithExperimentalComputer() ServerOption {
	return func(s *Server) { s.experimentalComputer = true }
}

func (s *Server) registerComputerTool() {
	tool := &mcp.Tool{
		Meta:        screenshotAppToolMeta(),
		Name:        "computer",
		Description: "Anthropic-style desktop control on an exact computer_session_id. Coordinates use screenshot pixels (1280x720 for 1920x1080), unchanged by zoom. Returns PNG and cursor. Click/scroll text accepts Shift/Control/Option/Command; type accepts ASCII, newline/tab (500 chars). hold_key clamps to 5s and accepts one chord; waits clamp to 10s. Never retry uncertain input; screenshot first.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Control Computer Screen",
			OpenWorldHint:   boolPtr(true),
			DestructiveHint: boolPtr(true),
		},
		InputSchema: computerToolInputSchema(),
	}
	encoded, err := json.Marshal(tool.InputSchema)
	if err != nil {
		panic(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(encoded, &schema); err != nil {
		panic(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		panic(err)
	}
	tool.OutputSchema, err = jsonschema.For[ComputerToolOutput](nil)
	if err != nil {
		panic(err)
	}
	handler := withDevHybridContent(s.handleComputer)
	s.mcpServer.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var arguments any
		var input ComputerToolInput
		var result *mcp.CallToolResult
		var output ComputerToolOutput
		if json.Unmarshal(req.Params.Arguments, &arguments) != nil || resolved.Validate(arguments) != nil || json.Unmarshal(req.Params.Arguments, &input) != nil {
			result, output, _ = computerToolFailure(errors.New("invalid computer input; check the action, required fields, and field types"))
		} else {
			var handlerErr error
			result, output, handlerErr = handler(ctx, req, input)
			if handlerErr != nil {
				result, output, _ = computerToolFailure(errors.New("computer tool result could not be encoded"))
			}
		}
		if result == nil {
			result = &mcp.CallToolResult{}
		}
		encoded, marshalErr := json.Marshal(output)
		if marshalErr != nil {
			return nil, errors.New("computer tool result could not be encoded")
		}
		result.StructuredContent = json.RawMessage(encoded)
		if result.Content == nil {
			result.Content = []mcp.Content{&mcp.TextContent{Text: string(encoded)}}
		}
		return result, nil
	})
}

func computerToolInputSchema() map[string]any {
	coordinate := map[string]any{
		"type": "array", "minItems": 2, "maxItems": 2,
		"items": map[string]any{"type": "integer", "minimum": 0, "maximum": 4095},
	}
	region := map[string]any{
		"type": "array", "minItems": 4, "maxItems": 4,
		"items": map[string]any{"type": "integer", "minimum": 0, "maximum": 4095},
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"computer_session_id", "action"},
		"properties": map[string]any{
			"computer_session_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
			"action":              map[string]any{"type": "string", "enum": computerActions},
			"coordinate":          coordinate,
			"start_coordinate":    coordinate,
			"text":                map[string]any{"type": "string", "maxLength": 500},
			"scroll_direction":    map[string]any{"type": "string", "enum": []string{"up", "down", "left", "right"}},
			"scroll_amount":       map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
			"duration":            map[string]any{"type": "number", "minimum": 0},
			"region":              region,
		},
	}
}

func (s *Server) handleComputer(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input ComputerToolInput,
) (*mcp.CallToolResult, ComputerToolOutput, error) {
	output := ComputerToolOutput{}
	if failure := s.refreshDevAuthentication(); failure != nil {
		return authenticationGateResult(failure), ComputerToolOutput{
			Error: failure.Message,
		}, nil
	}
	id, err := uuid.Parse(strings.TrimSpace(input.ComputerSessionID))
	if err != nil || id == uuid.Nil {
		return computerToolFailure(errors.New("computer_session_id must be a nonzero UUID"))
	}
	if err := validateComputerToolInput(input); err != nil {
		return computerToolFailure(err)
	}

	var screenshot *api.ComputerScreenshot
	switch input.Action {
	case "screenshot", "zoom", "wait", "cursor_position":
		if input.Action == "wait" {
			if err := waitComputer(ctx, minComputerDuration(*input.Duration, computerMaxWaitSeconds)); err != nil {
				return computerToolFailure(err)
			}
		}
		screenshot, err = s.apiClient.GetComputerScreenshot(ctx, id, input.Region, "mcp")
	default:
		request, mapErr := computerScreenAction(input)
		if mapErr != nil {
			return computerToolFailure(mapErr)
		}
		var result *api.ComputerScreenActionResult
		result, err = s.apiClient.PerformComputerScreenAction(ctx, id, request)
		if err == nil && result != nil {
			screenshot = result.Screenshot
			output.Cursor = result.Cursor
		}
	}
	if err != nil {
		return computerToolFailure(err)
	}
	if screenshot == nil {
		return computerToolFailure(errors.New("computer response did not include a screenshot"))
	}
	image, err := screenshot.DecodePNG()
	if err != nil {
		return computerToolFailure(fmt.Errorf("decode computer screenshot: %w", err))
	}
	if output.Cursor == nil {
		output.Cursor = screenshot.Cursor
	}
	output.Width, output.Height = screenshot.Width, screenshot.Height
	return nativeImageResult(image), output, nil
}

func computerToolFailure(err error) (*mcp.CallToolResult, ComputerToolOutput, error) {
	return &mcp.CallToolResult{IsError: true}, ComputerToolOutput{Error: err.Error()}, nil
}

func validateComputerToolInput(input ComputerToolInput) error {
	if input.Action == "wait" || input.Action == "hold_key" {
		if input.Duration == nil {
			return errors.New("duration is required for hold_key or wait")
		}
	} else if input.Duration != nil {
		return errors.New("duration is only valid for hold_key or wait")
	}
	if input.Coordinate == nil && coordinateRequired(input.Action) {
		return errors.New("coordinate is required for this action")
	}
	if input.StartCoordinate == nil && input.Action == "left_click_drag" {
		return errors.New("start_coordinate is required for left_click_drag")
	}
	if input.StartCoordinate != nil && input.Action != "left_click_drag" {
		return errors.New("start_coordinate is only valid for left_click_drag")
	}
	if input.Coordinate != nil && !coordinateRequired(input.Action) && input.Action != "left_mouse_down" && input.Action != "left_mouse_up" {
		return errors.New("coordinate is not valid for this action")
	}
	if input.Action == "zoom" || input.Region != nil {
		if input.Action != "zoom" {
			return errors.New("region is only valid for zoom")
		}
		if input.Region == nil {
			return errors.New("region is required for zoom")
		}
		if input.Region[2] <= input.Region[0] || input.Region[3] <= input.Region[1] {
			return errors.New("region must have positive bounds")
		}
	}
	if input.Action == "scroll" {
		if input.ScrollDirection == "" {
			return errors.New("scroll_direction is required for scroll")
		}
		if input.ScrollAmount == nil {
			return errors.New("scroll_amount is required for scroll")
		}
	} else if input.ScrollAmount != nil || input.ScrollDirection != "" {
		return errors.New("scroll_direction and scroll_amount are only valid for scroll")
	}
	if input.Action == "type" && input.Text == "" {
		return errors.New("text is required for type")
	}
	if input.Action == "key" || input.Action == "hold_key" {
		if strings.TrimSpace(input.Text) == "" {
			return errors.New("text is required for key actions")
		}
		if input.Action == "hold_key" && strings.ContainsAny(input.Text, " \t\r\n") {
			return errors.New("hold_key accepts one chord")
		}
	}
	if input.Text != "" && !slices.Contains([]string{"left_click", "right_click", "middle_click", "double_click", "triple_click", "scroll", "type", "key", "hold_key"}, input.Action) {
		return errors.New("text is not valid for this action")
	}
	return nil
}

func coordinateRequired(action string) bool {
	switch action {
	case "left_click", "right_click", "middle_click", "double_click", "triple_click", "mouse_move", "left_click_drag", "scroll":
		return true
	default:
		return false
	}
}

func computerScreenAction(input ComputerToolInput) (api.ComputerScreenActionRequest, error) {
	action := api.ComputerScreenAction{}
	if len(input.Coordinate) == 2 {
		action.X, action.Y = &input.Coordinate[0], &input.Coordinate[1]
	}
	var err error
	switch input.Action {
	case "left_click", "right_click", "middle_click", "double_click", "triple_click":
		action.Action = "click"
		action.Button = "left"
		if input.Action == "right_click" {
			action.Button = "right"
		} else if input.Action == "middle_click" {
			action.Button = "middle"
		}
		count := 1
		if input.Action == "double_click" {
			count = 2
		} else if input.Action == "triple_click" {
			count = 3
		}
		action.Count = &count
		action.Modifiers, err = parseComputerModifiers(input.Text)
		if err != nil {
			return api.ComputerScreenActionRequest{}, err
		}
	case "mouse_move":
		action.Action = "move"
	case "left_click_drag":
		action.Action = "drag"
		action.StartX, action.StartY = &input.StartCoordinate[0], &input.StartCoordinate[1]
		action.EndX, action.EndY = action.X, action.Y
		action.X, action.Y = nil, nil
	case "left_mouse_down", "left_mouse_up":
		action.Action = strings.TrimPrefix(input.Action, "left_")
		action.Button = "left"
	case "scroll":
		action.Action = "scroll"
		action.Direction, action.Amount = input.ScrollDirection, input.ScrollAmount
		action.Modifiers, err = parseComputerModifiers(input.Text)
		if err != nil {
			return api.ComputerScreenActionRequest{}, err
		}
	case "type":
		action.Action, action.Text = "type", input.Text
	case "key", "hold_key":
		action.Action, action.Keys = "key", input.Text
		if input.Duration != nil {
			action.HoldSeconds = minComputerDuration(*input.Duration, computerMaxHoldSeconds)
		}
	}
	return api.ComputerScreenActionRequest{Action: action, Screenshot: true, Via: "mcp"}, nil
}

func parseComputerModifiers(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '+' || r == ' ' || r == '\t' })
	if len(parts) == 0 {
		return nil, errors.New("click modifiers must be Shift, Control, Option, or Command")
	}
	seen := make(map[string]struct{}, len(parts))
	modifiers := make([]string, 0, len(parts))
	for _, part := range parts {
		var canonical string
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "shift":
			canonical = "shift"
		case "ctrl", "control":
			canonical = "ctrl"
		case "alt", "option", "opt":
			canonical = "alt"
		case "cmd", "command", "meta", "super":
			canonical = "cmd"
		default:
			return nil, errors.New("click modifiers must be Shift, Control, Option, or Command")
		}
		if _, exists := seen[canonical]; exists {
			return nil, errors.New("click modifiers must be unique")
		}
		seen[canonical] = struct{}{}
		modifiers = append(modifiers, canonical)
	}
	return modifiers, nil
}

func waitComputer(ctx context.Context, duration float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(time.Duration(duration * float64(time.Second)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func minComputerDuration(duration float64, maximum int) float64 {
	if duration > float64(maximum) {
		return float64(maximum)
	}
	return duration
}
