package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/revyl/cli/internal/api"
)

func TestCompositeToolsAdvertiseObjectParameters(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-key")
	server, err := NewServer("test", false, WithProfile(ProfileFull))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, tool := range listServerTools(t, server) {
		if !strings.HasPrefix(tool.Name, "manage_") {
			continue
		}
		count++
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]struct {
				Type string `json:"type"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatal(err)
		}
		if schema.Properties["params"].Type != "object" {
			t.Errorf("%s advertises non-object params: %s", tool.Name, encoded)
		}
	}
	if count != 8 {
		t.Fatalf("checked %d composite tools, want 8", count)
	}
}

func TestCompositeBuildListHonorsAppID(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-key")
	const appID = "12345678-1234-1234-1234-123456789abc"
	versionRequests := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + appID:
			_, _ = w.Write([]byte(`{"id":"` + appID + `","name":"Fixture","platform":"Android","versions_count":2,"latest_version":"v2"}`))
		case "/api/v1/apps/" + appID + "/builds":
			versionRequests++
			if r.URL.Query().Get("page_size") != "1" {
				t.Errorf("page size = %s", r.URL.Query().Get("page_size"))
			}
			_, _ = w.Write([]byte(`{"items":[{"id":"version-id","version":"v2","uploaded_at":"2026-01-02T00:00:00Z","is_current":true,"metadata":{"private":"omit-me"}}],"total":2,"has_next":true}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	server, err := NewServer("test", false, WithProfile(ProfileCore))
	if err != nil {
		t.Fatal(err)
	}
	server.apiClient = api.NewClientWithBaseURL("test-key", backend.URL)
	client := connectWorkspaceTestClient(t, server)
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "manage_builds", Arguments: map[string]any{
		"action": "list", "params": map[string]any{"app_id": appID, "platform": "android", "limit": 1},
	}})
	if err != nil || result.IsError {
		t.Fatalf("build lookup failed: %v, %+v", err, result)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	for _, expected := range []string{`"id":"version-id"`, `"uploaded_at":"2026-01-02T00:00:00Z"`, `"is_current":true`, `"has_more":true`} {
		if !strings.Contains(string(encoded), expected) {
			t.Errorf("missing %s in %s", expected, encoded)
		}
	}
	if strings.Contains(string(encoded), "omit-me") || versionRequests != 1 {
		t.Fatalf("unexpected metadata or lookup count: %s, %d", encoded, versionRequests)
	}
	for _, input := range []ListBuildsInput{{AppID: "../other"}, {AppID: appID, Limit: 101}, {AppID: appID, Platform: "ios"}} {
		_, output, err := server.handleListBuilds(context.Background(), nil, input)
		if err != nil || output.ErrorMessage == "" {
			t.Fatalf("invalid lookup did not fail explicitly: %v, %+v", err, output)
		}
	}
	if versionRequests != 1 {
		t.Fatal("invalid lookup fetched build versions")
	}
}

func TestCompositeObjectParametersReachTypedHandler(t *testing.T) {
	server := &Server{mcpServer: mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)}
	type input struct {
		AppID    string `json:"app_id"`
		Platform string `json:"platform"`
	}
	called := 0
	addCompositeTool(server.mcpServer, &mcp.Tool{Name: "fixture"}, func(ctx context.Context, req *mcp.CallToolRequest, args CompositeInput) (*mcp.CallToolResult, CompositeOutput, error) {
		return dispatchComposite(ctx, req, args, func(_ context.Context, _ *mcp.CallToolRequest, params input) (*mcp.CallToolResult, input, error) {
			called++
			return nil, params, nil
		})
	})
	client := connectWorkspaceTestClient(t, server)
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "fixture", Arguments: map[string]any{
		"action": "list", "params": map[string]any{"app_id": "fixture-app", "platform": "android"},
	}})
	if err != nil || result.IsError || called != 1 {
		t.Fatalf("object parameters did not reach the handler: result=%+v err=%v calls=%d", result, err, called)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	if !strings.Contains(string(encoded), `"app_id":"fixture-app"`) || !strings.Contains(string(encoded), `"platform":"android"`) {
		t.Fatalf("typed parameters were lost: %s", encoded)
	}
	for _, invalid := range []any{[]any{}, "{}", 7} {
		result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "fixture", Arguments: map[string]any{"action": "list", "params": invalid}})
		if err == nil && !result.IsError {
			t.Fatalf("invalid params accepted: %#v", invalid)
		}
	}
	if called != 1 {
		t.Fatal("invalid parameter shapes reached the handler")
	}
}
