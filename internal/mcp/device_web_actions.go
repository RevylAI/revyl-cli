package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/revyl/cli/internal/api"
)

func ParseDeviceKey(raw string, platform string) (*api.DeviceKeyRequest, error) {
	if platform != "web" {
		key := strings.ToUpper(strings.TrimSpace(raw))
		switch key {
		case "RETURN":
			key = "ENTER"
		case "DELETE":
			key = "BACKSPACE"
		}
		if key != "ENTER" && key != "BACKSPACE" {
			return nil, fmt.Errorf("key is not supported on this platform; use ENTER or BACKSPACE, or use a Web session")
		}
		return &api.DeviceKeyRequest{Key: key}, nil
	}
	if len(raw) > 64 {
		return nil, fmt.Errorf("invalid key; use a named Web key or a Ctrl/Alt/Meta/Shift chord")
	}
	for _, character := range raw {
		if character > 127 {
			return nil, fmt.Errorf("invalid key; use ASCII key and modifier names")
		}
	}
	parts := strings.Split(strings.TrimSpace(raw), "+")
	if len(parts) > 5 {
		return nil, fmt.Errorf("invalid key chord; use at most four unique Ctrl/Alt/Meta/Shift modifiers")
	}
	request := &api.DeviceKeyRequest{}
	seen := make(map[string]bool, 4)
	for _, part := range parts[:len(parts)-1] {
		modifier := strings.ToLower(strings.TrimSpace(part))
		switch modifier {
		case "ctrl", "alt", "meta", "shift":
		default:
			return nil, fmt.Errorf("invalid key modifier; use Ctrl, Alt, Meta, or Shift")
		}
		if seen[modifier] {
			return nil, fmt.Errorf("invalid key chord; modifiers must be unique")
		}
		seen[modifier] = true
		request.Modifiers = append(request.Modifiers, modifier)
	}
	rawKey := strings.TrimSpace(parts[len(parts)-1])
	key := strings.ToUpper(rawKey)
	canonical := map[string]string{
		"ENTER": "Enter", "RETURN": "Enter", "BACKSPACE": "Backspace",
		"TAB": "Tab", "ESCAPE": "Escape", "ESC": "Escape", "DELETE": "Delete",
		"SPACE": "Space", "ARROWUP": "ArrowUp", "ARROWDOWN": "ArrowDown",
		"ARROWLEFT": "ArrowLeft", "ARROWRIGHT": "ArrowRight", "HOME": "Home",
		"END": "End", "PAGEUP": "PageUp", "PAGEDOWN": "PageDown",
	}[key]
	if canonical == "" && len(request.Modifiers) > 0 && len(rawKey) == 1 && (key[0] >= 'A' && key[0] <= 'Z' || key[0] >= '0' && key[0] <= '9') {
		canonical = strings.ToLower(key)
	}
	if canonical == "" {
		return nil, fmt.Errorf("invalid key; use a named Web key or a modified ASCII letter or digit")
	}
	request.Key = canonical
	return request, nil
}

func (m *DeviceSessionManager) PressKeyOnSession(ctx context.Context, session *DeviceSession, raw string) ([]byte, error) {
	if session == nil {
		return nil, fmt.Errorf("no session resolved for key")
	}
	request, err := ParseDeviceKey(raw, session.Platform)
	if err != nil {
		return nil, err
	}
	if session.Platform != "web" {
		return m.WorkerRequestOnSession(ctx, session, "/key", map[string]string{"key": request.Key})
	}
	return m.performWebAction(ctx, session, "key", request)
}

func (m *DeviceSessionManager) ScrollOnSession(ctx context.Context, session *DeviceSession, direction string, amount int, target string, x, y *int, screenToken string) ([]byte, error) {
	if session == nil {
		return nil, fmt.Errorf("no session resolved for scroll")
	}
	if err := CheckDeviceActionPlatform(session, "scroll"); err != nil {
		return nil, err
	}
	if amount < 1 || amount > 16384 {
		return nil, fmt.Errorf("scroll amount must be between 1 and 16384 pixels")
	}
	request := &api.DeviceScrollRequest{}
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "down":
		request.DY = amount
	case "up":
		request.DY = -amount
	case "right":
		request.DX = amount
	case "left":
		request.DX = -amount
	default:
		return nil, fmt.Errorf("scroll direction must be up, down, left, or right")
	}
	var err error
	request.X, request.Y, request.ScreenToken, err = m.resolveWebActionTarget(ctx, session, target, x, y, screenToken, false)
	if err != nil {
		return nil, err
	}
	return m.performWebAction(ctx, session, "scroll", request)
}

func (m *DeviceSessionManager) SelectOnSession(ctx context.Context, session *DeviceSession, optionLabel string, target string, x, y *int, screenToken string) ([]byte, error) {
	if session == nil {
		return nil, fmt.Errorf("no session resolved for select")
	}
	if err := CheckDeviceActionPlatform(session, "select"); err != nil {
		return nil, err
	}
	if !utf8.ValidString(optionLabel) || utf8.RuneCountInString(optionLabel) < 1 || utf8.RuneCountInString(optionLabel) > 256 {
		return nil, fmt.Errorf("select option_label must contain 1 to 256 valid Unicode characters")
	}
	request := &api.DeviceSelectRequest{OptionLabel: optionLabel}
	var err error
	request.X, request.Y, request.ScreenToken, err = m.resolveWebActionTarget(ctx, session, target, x, y, screenToken, true)
	if err != nil {
		return nil, err
	}
	return m.performWebAction(ctx, session, "select", request)
}

func (m *DeviceSessionManager) resolveWebActionTarget(ctx context.Context, session *DeviceSession, target string, x, y *int, screenToken string, required bool) (*int, *int, string, error) {
	if (x == nil) != (y == nil) {
		return nil, nil, "", fmt.Errorf("both x and y are required when using coordinates")
	}
	if target != "" && x != nil {
		return nil, nil, "", fmt.Errorf("provide either target OR x+y, not both")
	}
	if target == "" && x == nil && required {
		return nil, nil, "", fmt.Errorf("provide target or x+y for select")
	}
	if x != nil && (*x < 0 || *x > 11520 || *y < 0 || *y > 6480) {
		return nil, nil, "", fmt.Errorf("coordinates must be within x=0..11520 and y=0..6480")
	}
	if target != "" && (!utf8.ValidString(target) || strings.TrimSpace(target) == "" || utf8.RuneCountInString(target) > 1000) {
		return nil, nil, "", fmt.Errorf("target must contain 1 to 1000 valid Unicode characters")
	}
	runtimeToken, err := m.RuntimeScreenTokenForAnchor(session, screenToken)
	if err != nil {
		return nil, nil, "", err
	}
	if target == "" {
		return x, y, runtimeToken, nil
	}
	var resolved *ResolvedTarget
	if screenToken == "" {
		resolved, err = m.ResolveTargetOnSession(ctx, session, target)
	} else {
		resolved, err = m.resolveTargetViaWorkerWithToken(ctx, session, target, runtimeToken)
	}
	if err != nil {
		return nil, nil, "", err
	}
	return &resolved.X, &resolved.Y, resolved.ScreenToken, nil
}

func (m *DeviceSessionManager) performWebAction(ctx context.Context, session *DeviceSession, action string, request any) ([]byte, error) {
	response, err := m.WorkerRequestOnSession(ctx, session, "/"+action, request)
	if err != nil {
		return nil, err
	}
	var result workerActionResultEnvelope
	if json.Unmarshal(response, &result) != nil || result.SuccessLower == nil || result.Action != "" && result.Action != action {
		return nil, &WorkerHTTPError{Path: "/" + action, Code: "protocol_error", ExecutionUncertain: true}
	}
	if !*result.SuccessLower {
		return nil, fmt.Errorf("device %s failed; inspect the session report before repeating the action", action)
	}
	return response, nil
}

type DeviceScrollInput struct {
	Direction    string `json:"direction" jsonschema:"Scroll direction: up, down, left, or right."`
	Amount       *int   `json:"amount,omitempty" jsonschema:"Scroll distance in pixels, 1–16384; defaults to 600."`
	Target       string `json:"target,omitempty" jsonschema:"Scrollable element description; omit for the viewport. Mutually exclusive with x+y."`
	X            *int   `json:"x,omitempty" jsonschema:"Raw X pixel coordinate; requires y."`
	Y            *int   `json:"y,omitempty" jsonschema:"Raw Y pixel coordinate; requires x."`
	ScreenToken  string `json:"screen_token,omitempty" jsonschema:"Local screenshot anchor for this session."`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID. Wins over session_index and never falls back to the active session."`
}

type DeviceSelectInput struct {
	OptionLabel  string `json:"option_label" jsonschema:"Exact visible label of the native select option, 1–256 Unicode characters."`
	Target       string `json:"target,omitempty" jsonschema:"Native select element description; mutually exclusive with x+y."`
	X            *int   `json:"x,omitempty" jsonschema:"Raw X pixel coordinate; requires y."`
	Y            *int   `json:"y,omitempty" jsonschema:"Raw Y pixel coordinate; requires x."`
	ScreenToken  string `json:"screen_token,omitempty" jsonschema:"Local screenshot anchor for this session."`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID. Wins over session_index and never falls back to the active session."`
}

type DeviceWebActionOutput struct {
	Success   bool       `json:"success"`
	LatencyMs float64    `json:"latency_ms"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleDeviceScroll(ctx context.Context, req *mcp.CallToolRequest, input DeviceScrollInput) (*mcp.CallToolResult, DeviceWebActionOutput, error) {
	start := time.Now()
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID, "scroll")
	if err == nil {
		amount := 600
		if input.Amount != nil {
			amount = *input.Amount
		}
		_, err = s.sessionMgr.ScrollOnSession(ctx, session, input.Direction, amount, input.Target, input.X, input.Y, input.ScreenToken)
	}
	output := DeviceWebActionOutput{Success: err == nil, LatencyMs: float64(time.Since(start).Milliseconds())}
	if err != nil {
		output.Error, output.NextSteps = err.Error(), errorNextSteps(err)
	}
	return nil, output, nil
}

func (s *Server) handleDeviceSelect(ctx context.Context, req *mcp.CallToolRequest, input DeviceSelectInput) (*mcp.CallToolResult, DeviceWebActionOutput, error) {
	start := time.Now()
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID, "select")
	if err == nil {
		_, err = s.sessionMgr.SelectOnSession(ctx, session, input.OptionLabel, input.Target, input.X, input.Y, input.ScreenToken)
	}
	output := DeviceWebActionOutput{Success: err == nil, LatencyMs: float64(time.Since(start).Milliseconds())}
	if err != nil {
		output.Error, output.NextSteps = err.Error(), errorNextSteps(err)
	}
	return nil, output, nil
}

func deviceActionInputSchema(action string) map[string]any {
	properties := map[string]any{
		"session_index": map[string]any{"type": "integer", "minimum": 0, "description": "Session index; omit for the active session."},
		"session_id":    map[string]any{"type": "string", "minLength": 1, "maxLength": 256, "description": "Server-issued session ID; never falls back to the active session."},
	}
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if action == "key" {
		properties["key"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 64, "description": "Phones: ENTER/BACKSPACE. Web: Enter, Backspace, Tab, Escape (Esc), Delete, Space, ArrowUp/Down/Left/Right, Home, End, PageUp/Down; Ctrl/Alt/Meta/Shift chords also accept ASCII letters/digits. RETURN aliases Enter. No F keys."}
		schema["required"] = []string{"key"}
		return schema
	}
	properties["target"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 1000, "description": "Element description; mutually exclusive with x+y."}
	properties["x"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 11520}
	properties["y"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 6480}
	properties["screen_token"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 8192, "description": "Local screenshot anchor for this session."}
	noTarget := map[string]any{"not": map[string]any{"required": []string{"target"}}}
	noCoordinates := map[string]any{"not": map[string]any{"anyOf": []any{map[string]any{"required": []string{"x"}}, map[string]any{"required": []string{"y"}}}}}
	locations := []any{
		map[string]any{"required": []string{"target"}, "allOf": []any{noCoordinates}},
		map[string]any{"required": []string{"x", "y"}, "allOf": []any{noTarget}},
	}
	if action == "scroll" {
		properties["direction"] = map[string]any{"type": "string", "enum": []string{"up", "down", "left", "right"}}
		properties["amount"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 16384, "default": 600}
		schema["required"] = []string{"direction"}
		locations = append(locations, map[string]any{"allOf": []any{noTarget, noCoordinates}})
	} else {
		properties["option_label"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 256, "description": "Exact visible option label, not its value attribute."}
		schema["required"] = []string{"option_label"}
	}
	schema["oneOf"] = locations
	return schema
}
