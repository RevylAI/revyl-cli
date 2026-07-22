package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/revyl/cli/internal/outcome"
)

func TestSelectInteractionStrategyAuto(t *testing.T) {
	t.Parallel()

	instruction, err := selectInteractionStrategy(InteractInput{Task: "log in and open settings"})
	if err != nil || instruction != "instruction" {
		t.Fatalf("instruction strategy = %q, %v", instruction, err)
	}

	semantic, err := selectInteractionStrategy(InteractInput{
		Task:            "tap Sign In",
		InteractionType: "tap",
	})
	if err != nil || semantic != "semantic" {
		t.Fatalf("semantic strategy = %q, %v", semantic, err)
	}
}

func TestSelectInteractionStrategyRejectsCoordinates(t *testing.T) {
	t.Parallel()

	if _, err := selectInteractionStrategy(InteractInput{
		Task:            "tap the small close icon precisely",
		Strategy:        "coordinates",
		InteractionType: "tap",
	}); err == nil {
		t.Fatal("coordinate strategy succeeded")
	}
}

func TestSelectInteractionStrategyRejectsUnknown(t *testing.T) {
	t.Parallel()

	if _, err := selectInteractionStrategy(InteractInput{Task: "tap", Strategy: "random"}); err == nil {
		t.Fatal("unknown strategy succeeded")
	}
}

func TestNativeImageResultContainsNoText(t *testing.T) {
	t.Parallel()

	result := nativeImageResult([]byte("png"))
	if len(result.Content) != 1 {
		t.Fatalf("content count = %d, want 1", len(result.Content))
	}
	image, ok := result.Content[0].(*mcpsdk.ImageContent)
	if !ok {
		t.Fatalf("content type = %T, want *mcp.ImageContent", result.Content[0])
	}
	if image.MIMEType != "image/png" {
		t.Fatalf("MIME type = %q", image.MIMEType)
	}
}

func TestDevHybridValidationContentIncludesVerdictAndImage(t *testing.T) {
	t.Parallel()

	handler := withDevHybridContent(func(
		context.Context,
		*mcpsdk.CallToolRequest,
		DeviceValidationInput,
	) (*mcpsdk.CallToolResult, DevValidationOutput, error) {
		return nativeImageResult([]byte("png")), DevValidationOutput{
			Success: true,
			Outcome: outcome.Completed(),
			StepOutput: map[string]any{
				"validation_result": true,
			},
		}, nil
	})

	result, output, err := handler(context.Background(), nil, DeviceValidationInput{})
	if err != nil {
		t.Fatalf("hybrid validation handler error = %v", err)
	}
	text := requireHybridTextAndImage(t, result)
	var decoded DevValidationOutput
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("decode validation fallback: %v", err)
	}
	if !decoded.Success || decoded.StepOutput["validation_result"] != true {
		t.Fatalf("validation fallback = %#v, want successful verdict", decoded)
	}
	if output.StepOutput["validation_result"] != true {
		t.Fatalf("typed validation output = %#v", output)
	}
}

func TestDevHybridInteractionContentIncludesOutcomeAndImage(t *testing.T) {
	t.Parallel()

	handler := withDevHybridContent(func(
		context.Context,
		*mcpsdk.CallToolRequest,
		InteractInput,
	) (*mcpsdk.CallToolResult, DevProfileActionOutput, error) {
		return nativeImageResult([]byte("png")), DevProfileActionOutput{
			Action:  "tap",
			Result:  map[string]any{"success": true},
			Outcome: outcome.Completed(),
		}, nil
	})

	result, _, err := handler(context.Background(), nil, InteractInput{})
	if err != nil {
		t.Fatalf("hybrid interaction handler error = %v", err)
	}
	text := requireHybridTextAndImage(t, result)
	var decoded DevProfileActionOutput
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("decode interaction fallback: %v", err)
	}
	if decoded.Action != "tap" || decoded.Result["success"] != true {
		t.Fatalf("interaction fallback = %#v, want successful tap", decoded)
	}
}

func TestDevHybridContentPreservesImageBackedError(t *testing.T) {
	t.Parallel()

	handler := withDevHybridContent(func(
		context.Context,
		*mcpsdk.CallToolRequest,
		DeviceValidationInput,
	) (*mcpsdk.CallToolResult, DevValidationOutput, error) {
		result := nativeImageResult([]byte("png"))
		result.IsError = true
		return result, DevValidationOutput{
			Success: false,
			Outcome: outcome.Failed("validation_failed", "expected text was absent", false),
			StepOutput: map[string]any{
				"validation_result": false,
			},
			Error: "expected text was absent",
		}, nil
	})

	result, _, err := handler(context.Background(), nil, DeviceValidationInput{})
	if err != nil {
		t.Fatalf("hybrid error handler error = %v", err)
	}
	text := requireHybridTextAndImage(t, result)
	if !result.IsError {
		t.Fatal("hybrid image-backed failure lost MCP error state")
	}
	var decoded DevValidationOutput
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("decode image-backed failure: %v", err)
	}
	if decoded.Success || decoded.StepOutput["validation_result"] != false {
		t.Fatalf("image-backed failure = %#v, want false verdict", decoded)
	}
}

func TestDevHybridContentRoundTripsThroughMCP(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-environment-api-key")
	server, err := NewServer("test", false, WithProfile(ProfileDev))
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	addDevHybridTool(server.mcpServer, &mcpsdk.Tool{
		Name:        "hybrid_contract_fixture",
		Description: "Exercise image and structured result transport.",
	}, func(
		context.Context,
		*mcpsdk.CallToolRequest,
		struct{},
	) (*mcpsdk.CallToolResult, DevValidationOutput, error) {
		return nativeImageResult([]byte("png")), DevValidationOutput{
			Success: true,
			Outcome: outcome.Completed(),
			StepOutput: map[string]any{
				"validation_result": true,
			},
		}, nil
	})

	result := callServerTool(t, server, "hybrid_contract_fixture", map[string]any{})
	if result.IsError {
		t.Fatalf("hybrid fixture result = %+v, want success", result)
	}
	text := requireHybridTextAndImage(t, result)
	structured := decodeStructuredToolResult[DevValidationOutput](t, result)
	var fallback DevValidationOutput
	if err := json.Unmarshal([]byte(text), &fallback); err != nil {
		t.Fatalf("decode hybrid text: %v", err)
	}
	if structured.StepOutput["validation_result"] != true ||
		fallback.StepOutput["validation_result"] != true {
		t.Fatalf("hybrid verdicts = structured:%#v text:%#v", structured, fallback)
	}
}

func TestDevHybridSerializationFailureIsVisibleToolError(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-environment-api-key")
	server, err := NewServer("test", false, WithProfile(ProfileDev))
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	addDevHybridTool(server.mcpServer, &mcpsdk.Tool{
		Name:        "hybrid_error_fixture",
		Description: "Exercise hybrid serialization failures.",
	}, func(
		context.Context,
		*mcpsdk.CallToolRequest,
		struct{},
	) (*mcpsdk.CallToolResult, DevProfileActionOutput, error) {
		return nativeImageResult([]byte("png")), DevProfileActionOutput{
			Action:  "tap",
			Result:  map[string]any{"unsupported": func() {}},
			Outcome: outcome.Completed(),
		}, nil
	})

	result := callServerTool(t, server, "hybrid_error_fixture", map[string]any{})
	if !result.IsError {
		t.Fatalf("hybrid serialization result = %+v, want tool error", result)
	}
	if len(result.Content) != 1 {
		t.Fatalf("hybrid error content count = %d, want 1", len(result.Content))
	}
	text, ok := result.Content[0].(*mcpsdk.TextContent)
	if !ok || !strings.Contains(text.Text, "encode hybrid MCP result") {
		t.Fatalf("hybrid error content = %#v", result.Content)
	}
}

func TestSanitizeDevStructuredResultRemovesPresentationFields(t *testing.T) {
	t.Parallel()

	result := map[string]any{
		"success":    true,
		"image":      "base64",
		"next_steps": []any{"screenshot"},
		"step_output": map[string]any{
			"validation_result": true,
			"image":             "base64",
			"next_steps":        []any{"retry"},
		},
	}
	sanitizeDevStructuredResult(result)

	if _, found := result["image"]; found {
		t.Fatal("top-level image was not removed")
	}
	if _, found := result["next_steps"]; found {
		t.Fatal("top-level next_steps was not removed")
	}
	stepOutput := result["step_output"].(map[string]any)
	if _, found := stepOutput["image"]; found {
		t.Fatal("step_output image was not removed")
	}
	if _, found := stepOutput["next_steps"]; found {
		t.Fatal("step_output next_steps was not removed")
	}
}

// requireHybridTextAndImage verifies fallback JSON precedes one native image.
//
// Parameters:
//   - t: Active test.
//   - result: Hybrid MCP result to inspect.
//
// Returns:
//   - string: JSON text fallback.
func requireHybridTextAndImage(t *testing.T, result *mcpsdk.CallToolResult) string {
	t.Helper()
	if result == nil || len(result.Content) != 2 {
		t.Fatalf("hybrid content = %#v, want text and image", result)
	}
	text, ok := result.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("hybrid content[0] = %T, want *mcp.TextContent", result.Content[0])
	}
	if _, ok := result.Content[1].(*mcpsdk.ImageContent); !ok {
		t.Fatalf("hybrid content[1] = %T, want *mcp.ImageContent", result.Content[1])
	}
	return text.Text
}

func TestDevProfileVisualRefreshFailureSynchronizesContracts(t *testing.T) {
	t.Parallel()

	toolResult, output := devProfileVisualRefreshFailure(DevProfileActionOutput{
		Action:  "tap",
		Result:  map[string]any{"success": true},
		Outcome: outcome.Completed(),
	}, "post-action screenshot failed", false)

	if toolResult == nil || !toolResult.IsError {
		t.Fatalf("tool result = %+v, want MCP error", toolResult)
	}
	if success, ok := output.Result["success"].(bool); !ok || success {
		t.Fatalf("result success = %#v, want false", output.Result["success"])
	}
	if got := output.Result["error"]; got != "post-action screenshot failed" {
		t.Fatalf("result error = %#v", got)
	}
	if output.Outcome.OperationStatus != "failed" {
		t.Fatalf("operation status = %q, want failed", output.Outcome.OperationStatus)
	}
	if output.Outcome.OutcomeCode != "visual_refresh_failed" {
		t.Fatalf("outcome code = %q, want visual_refresh_failed", output.Outcome.OutcomeCode)
	}
	if output.Outcome.Reason != "post-action screenshot failed" {
		t.Fatalf("outcome reason = %q", output.Outcome.Reason)
	}
	if output.Outcome.Retryable {
		t.Fatal("post-action interaction failure must not be retryable")
	}
}
