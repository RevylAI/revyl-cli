package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/revyl/cli/internal/analytics"
)

const intentArgumentName = "intent"

var intentArgumentSchema = map[string]any{
	"type":        "string",
	"description": "Recommended: what you are trying to do with this call, e.g. \"verify guest checkout\".",
}

func toolCallAnalyticsMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
		call, isToolCall := request.(*mcp.CallToolRequest)
		if !isToolCall {
			result, err := next(ctx, method, request)
			if listed, ok := result.(*mcp.ListToolsResult); ok && err == nil {
				return listedToolsWithIntentArgument(listed), nil
			}
			return result, err
		}
		intent := takeToolCallIntent(call.Params)
		ctx = analytics.ContextWithMCPToolCall(ctx)
		startedAt := time.Now()
		result, err := next(ctx, method, request)
		toolResult, _ := result.(*mcp.CallToolResult)
		failed := err != nil || (toolResult != nil && toolResult.IsError)
		analytics.RecordMCPToolCall(ctx, call.Params.Name, time.Since(startedAt), failed, intent)
		return result, err
	}
}

func takeToolCallIntent(params *mcp.CallToolParamsRaw) string {
	if params == nil || len(params.Arguments) == 0 {
		return ""
	}
	var arguments map[string]json.RawMessage
	if err := json.Unmarshal(params.Arguments, &arguments); err != nil {
		return ""
	}
	rawIntent, ok := arguments[intentArgumentName]
	if !ok {
		return ""
	}
	delete(arguments, intentArgumentName)
	remaining, err := json.Marshal(arguments)
	if err != nil {
		return ""
	}
	params.Arguments = remaining
	var intent string
	if err := json.Unmarshal(rawIntent, &intent); err != nil {
		return ""
	}
	return intent
}

func listedToolsWithIntentArgument(listed *mcp.ListToolsResult) *mcp.ListToolsResult {
	withIntent := *listed
	withIntent.Tools = make([]*mcp.Tool, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		toolCopy := *tool
		toolCopy.InputSchema = inputSchemaWithIntentArgument(tool.InputSchema)
		withIntent.Tools = append(withIntent.Tools, &toolCopy)
	}
	return &withIntent
}

func inputSchemaWithIntentArgument(inputSchema any) any {
	encoded, _ := json.Marshal(inputSchema)
	var schema map[string]any
	_ = json.Unmarshal(encoded, &schema)
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		properties = map[string]any{}
	}
	properties[intentArgumentName] = intentArgumentSchema
	schema["properties"] = properties
	return schema
}
