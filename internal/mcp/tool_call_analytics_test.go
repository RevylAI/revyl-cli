package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
)

type probeInput struct {
	Platform string `json:"platform"`
}

type recordedPayloads struct {
	mu     sync.Mutex
	events []analytics.TelemetryEvent
}

func (r *recordedPayloads) toolEvents() []analytics.TelemetryEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []analytics.TelemetryEvent
	for _, event := range r.events {
		if event.Event == analytics.McpToolCalledEvent {
			out = append(out, event)
		}
	}
	return out
}

func connectAnalyticsTestClient(t *testing.T, ctx context.Context, server *mcpsdk.Server) *mcpsdk.ClientSession {
	t.Helper()
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { serverSession.Close() })
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "analytics-test-client", Version: "test"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { clientSession.Close() })
	return clientSession
}

func TestEveryToolCallIsRecordedLikeACommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	recorded := &recordedPayloads{}
	recorder := analytics.NewWithFlusher(analytics.Config{Version: "test"}, func(payload analytics.TelemetryPayload) {
		recorded.mu.Lock()
		recorded.events = append(recorded.events, payload.Events...)
		recorded.mu.Unlock()
	})
	serveCommand := &cobra.Command{Use: "serve"}
	serveCommand.Flags().String("intent", "", "")
	if err := serveCommand.Flags().Set("intent", "start the MCP server"); err != nil {
		t.Fatal(err)
	}
	ctx := analytics.ContextWithCommandRun(context.Background(), recorder.StartCommand(serveCommand, nil))

	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "test"}, nil)
	server.AddReceivingMiddleware(toolCallAnalyticsMiddleware)
	var gotPlatform string
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "typed_probe"}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, input probeInput) (*mcpsdk.CallToolResult, any, error) {
		gotPlatform = input.Platform
		return &mcpsdk.CallToolResult{}, nil, nil
	})
	var strictArguments json.RawMessage
	server.AddTool(&mcpsdk.Tool{
		Name: "strict_probe",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
	}, func(ctx context.Context, request *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		strictArguments = request.Params.Arguments
		return &mcpsdk.CallToolResult{IsError: true}, nil
	})
	session := connectAnalyticsTestClient(t, ctx, server)

	if _, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "typed_probe",
		Arguments: map[string]any{"platform": "ios", "intent": "verify guest checkout"},
	}); err != nil {
		t.Fatal(err)
	}
	if gotPlatform != "ios" {
		t.Fatalf("platform = %q", gotPlatform)
	}
	result, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "strict_probe",
		Arguments: map[string]any{"intent": 42},
	})
	if err != nil || !result.IsError {
		t.Fatalf("strict_probe: err=%v result=%+v", err, result)
	}
	if string(strictArguments) != "{}" {
		t.Fatalf("strict tool received %s, want the intent removed", strictArguments)
	}

	recorder.Flush()
	events := recorded.toolEvents()
	if len(events) != 2 {
		t.Fatalf("recorded %d tool events, want 2", len(events))
	}
	byTool := map[string]map[string]interface{}{}
	for _, event := range events {
		byTool[event.Properties["mcp_tool"].(string)] = event.Properties
	}
	typed, strict := byTool["typed_probe"], byTool["strict_probe"]
	if typed["error"] != false || typed["intent"] != "verify guest checkout" || typed["mcp_server"] != "local" {
		t.Fatalf("typed_probe event = %v", typed)
	}
	if typed["command"] != "serve" || typed["command_id"] == "" {
		t.Fatalf("tool event lacks the serve command's properties: %v", typed)
	}
	if strict["error"] != true {
		t.Fatalf("strict_probe event = %v", strict)
	}
	if strict["intent"] != nil {
		t.Fatalf("tool event intent = %v, want none: neither a non-string intent nor the server's own", strict["intent"])
	}
}

func TestEveryListedToolAdvertisesOptionalIntent(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-key")
	for _, profile := range []Profile{"", ProfileFull} {
		var options []ServerOption
		if profile != "" {
			options = append(options, WithProfile(profile))
		}
		server, err := NewServer("test", false, options...)
		if err != nil {
			t.Fatalf("NewServer(profile=%q): %v", profile, err)
		}
		session := connectAnalyticsTestClient(t, context.Background(), server.mcpServer)
		listed, err := session.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatalf("ListTools(profile=%q): %v", profile, err)
		}
		if len(listed.Tools) == 0 {
			t.Fatalf("profile %q listed no tools", profile)
		}
		for _, tool := range listed.Tools {
			encoded, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			}
			if err := json.Unmarshal(encoded, &schema); err != nil {
				t.Fatal(err)
			}
			var intentProperty struct {
				Type string `json:"type"`
			}
			if raw, ok := schema.Properties[intentArgumentName]; ok {
				if err := json.Unmarshal(raw, &intentProperty); err != nil {
					t.Fatal(err)
				}
			}
			if intentProperty.Type != "string" {
				t.Errorf("profile %q tool %s does not advertise a string intent", profile, tool.Name)
			}
			for _, required := range schema.Required {
				if required == intentArgumentName {
					t.Errorf("profile %q tool %s requires intent", profile, tool.Name)
				}
			}
		}
	}
}
