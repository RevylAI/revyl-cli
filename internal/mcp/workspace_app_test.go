package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWorkspaceRegistrationIsOptIn(t *testing.T) {
	for _, profile := range []Profile{"", ProfileCore, ProfileFull, ProfileDev} {
		t.Run(string(profile), func(t *testing.T) {
			prepareServerAuthTest(t)
			t.Setenv("REVYL_API_KEY", "test-key")
			t.Setenv("REVYL_APP_URL", "https://workspace.example.com")
			baseline, err := NewServer("test", false, WithProfile(profile))
			if err != nil {
				t.Fatal(err)
			}
			workspace, err := NewServer("test", false, WithProfile(profile), WithExperimentalWorkspace())
			if err != nil {
				t.Fatal(err)
			}
			before := listServerTools(t, baseline)
			after := listServerTools(t, workspace)
			if len(after) != len(before)+1 {
				t.Fatalf("tool count changed by more than workspace: %d -> %d", len(before), len(after))
			}
			for _, tool := range before {
				if tool.Name == "open_revyl_workspace" {
					t.Fatal("workspace advertised without opt-in")
				}
				enabled := *serverToolByName(t, after, tool.Name)
				if tool.Name == "start_device_session" {
					if tool.Meta != nil {
						t.Fatalf("default session metadata changed: %+v", tool.Meta)
					}
					ui := enabled.Meta["ui"].(map[string]any)
					if ui["resourceUri"] != workspaceAppURI {
						t.Fatalf("session resource = %v", ui)
					}
					enabled.Meta = nil
				}
				if !reflect.DeepEqual(*tool, enabled) {
					t.Errorf("existing tool %s changed beyond workspace metadata", tool.Name)
				}
			}
			tool := serverToolByName(t, after, "open_revyl_workspace")
			metaJSON, _ := json.Marshal(tool.Meta)
			want := `{"openai/ui":{"entrypoints":[{"type":"global"},{"type":"thread"}]},"ui":{"resourceUri":"ui://revyl/workspace-v2.html"}}`
			if string(metaJSON) != want {
				t.Fatalf("workspace tool metadata = %s", metaJSON)
			}
			baselineClient := connectWorkspaceTestClient(t, baseline)
			if _, err := baselineClient.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: workspaceAppURI}); err == nil {
				t.Fatal("workspace resource exposed without opt-in")
			}
		})
	}
}

func TestWorkspaceResourceAndToolCalls(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("REVYL_APP_URL", "https://workspace.example.com")
	t.Setenv("REVYL_CHATGPT_PLUGIN_ID", "")
	server, err := NewServer("test", false, WithProfile(ProfileCore), WithExperimentalWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	client := connectWorkspaceTestClient(t, server)
	resource, err := client.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: workspaceAppURI})
	if err != nil {
		t.Fatal(err)
	}
	if len(resource.Contents) != 1 {
		t.Fatalf("contents = %d", len(resource.Contents))
	}
	content := resource.Contents[0]
	if content.MIMEType != workspaceAppMIMEType || content.URI != workspaceAppURI {
		t.Fatalf("resource identity = %s %s", content.URI, content.MIMEType)
	}
	metadata, _ := json.Marshal(content.Meta)
	wantMetadata := `{"openai/ui":{"availableDisplayModes":["fullscreen","inline"],"preferredDisplayMode":"fullscreen"},"openai/widgetCSP":{"connect_domains":[],"frame_domains":["https://workspace.example.com"],"resource_domains":[]},"ui":{"csp":{"connectDomains":[],"frameDomains":["https://workspace.example.com"],"resourceDomains":[]}}}`
	if string(metadata) != wantMetadata {
		t.Fatalf("resource metadata = %s", metadata)
	}
	if !strings.Contains(content.Text, `const configuredOrigin = "https://workspace.example.com";`) || strings.Contains(content.Text, "test-key") || strings.Contains(content.Text, "__REVYL_ORIGIN__") {
		t.Fatal("resource origin substitution or credential isolation failed")
	}
	for _, removedChrome := range []string{"<nav", `id="atlas"`, `id="sessions"`, "<strong>Revyl workspace</strong>"} {
		if strings.Contains(content.Text, removedChrome) {
			t.Errorf("resource retains removed workspace chrome: %s", removedChrome)
		}
	}
	for _, required := range []string{
		`<main id="state">`,
		`<p id="status" role="status">Loading Revyl workspace…</p>`,
		`<button id="external" type="button" hidden>Open in browser</button>`,
		`<p id="browser-status" role="status" hidden></p>`,
		`iframe{display:block;width:100%;height:100%;border:0}`,
		`[hidden]{display:none!important}`,
		`place-items:center`,
		`allow-popups-to-escape-sandbox allow-downloads" hidden></iframe>`,
		`method:"ui/open-link"`,
	} {
		if !strings.Contains(content.Text, required) {
			t.Errorf("resource is missing full-panel state contract: %s", required)
		}
	}
	const id = "12345678-1234-1234-1234-123456789abc"
	for _, test := range []struct {
		name string
		args map[string]any
		path string
	}{
		{"empty", map[string]any{}, "/atlas"},
		{"atlas", map[string]any{"view": "atlas"}, "/atlas"},
		{"app", map[string]any{"app_id": id}, "/apps/" + id + "/atlas"},
		{"device", map[string]any{"view": "device"}, "/sessions"},
		{"session", map[string]any{"view": "device", "session_id": id}, "/sessions/" + id},
		{"arbitrary url", map[string]any{"url": "https://evil.example"}, ""},
		{"view url", map[string]any{"view": "https://evil.example"}, ""},
		{"app url", map[string]any{"app_id": "https://evil.example"}, ""},
		{"session traversal", map[string]any{"view": "device", "session_id": "../atlas"}, ""},
		{"compact uuid", map[string]any{"app_id": strings.ReplaceAll(id, "-", "")}, ""},
		{"app wrong view", map[string]any{"view": "device", "app_id": id}, ""},
		{"session wrong view", map[string]any{"session_id": id}, ""},
		{"two ids", map[string]any{"app_id": id, "session_id": id}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "open_revyl_workspace", Arguments: test.args})
			if test.path == "" {
				if err == nil && !result.IsError {
					t.Fatalf("invalid input accepted: %+v", result)
				}
				return
			}
			if err != nil || result.IsError {
				t.Fatalf("CallTool = %+v, %v", result, err)
			}
			encoded, _ := json.Marshal(result.StructuredContent)
			var output OpenWorkspaceOutput
			if err := json.Unmarshal(encoded, &output); err != nil {
				t.Fatal(err)
			}
			if output.WorkspaceURL != "https://workspace.example.com"+test.path {
				t.Fatalf("workspace URL = %s", output.WorkspaceURL)
			}
			if output.ChatGPTURL != "" {
				t.Fatal("unconfigured hosts must not get an invented plugin link")
			}
		})
	}
}

func TestWorkspaceChatGPTDeepLinks(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("REVYL_APP_URL", "https://workspace.example.com")
	t.Setenv("REVYL_CHATGPT_PLUGIN_ID", "Plugin_example")
	server, err := NewServer("test", false, WithProfile(ProfileCore), WithExperimentalWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	const id = "12345678-1234-1234-1234-123456789abc"
	for _, input := range []OpenWorkspaceInput{{}, {View: "atlas", AppID: id}, {View: "device", SessionID: id}} {
		_, output, err := server.handleOpenWorkspace(context.Background(), nil, input)
		if err != nil {
			t.Fatal(err)
		}
		link, err := url.Parse(output.ChatGPTURL)
		if err != nil {
			t.Fatal(err)
		}
		if link.Scheme != "https" || link.Host != "chatgpt.com" || link.Path != "/plugins/Plugin_example/app/open_revyl_workspace" || len(link.Query()) != 1 || "https://workspace.example.com"+link.Query().Get("path") != output.WorkspaceURL {
			t.Fatalf("incorrect deep link: %s", output.ChatGPTURL)
		}
	}
	for _, invalid := range []string{"../other", "https://evil.example", "Plugin_test?token=private", " Plugin_example ", strings.Repeat("x", 129)} {
		t.Setenv("REVYL_CHATGPT_PLUGIN_ID", invalid)
		if _, err := NewServer("test", false, WithExperimentalWorkspace()); err == nil || strings.Contains(err.Error(), invalid) {
			t.Fatalf("invalid plugin ID must fail without echoing its value: %v", err)
		}
		if _, err := NewServer("test", false); err != nil {
			t.Fatalf("unrelated plugin config changed opt-out behavior: %v", err)
		}
	}
}

func TestWorkspaceOriginValidation(t *testing.T) {
	for _, raw := range []string{"https://app.example.com", "http://localhost:3000", "http://127.0.0.1:3001/", "http://[::1]:3000"} {
		if _, err := validateWorkspaceOrigin(raw); err != nil {
			t.Errorf("valid origin %q: %v", raw, err)
		}
	}
	for _, raw := range []string{"", "//example.com", "javascript:alert(1)", "data:text/html,test", "https://name:secret@example.com", "https://example.com/path", "https://example.com?secret=test", "https://example.com#token", "https://example.com?", "https://example.com#", "https://example.com/%2f", "https://", "https://*.example.com", "http://localhost:99999", "http://localhost:0"} {
		if _, err := validateWorkspaceOrigin(raw); err == nil {
			t.Errorf("invalid origin %q accepted", raw)
		}
	}
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("REVYL_APP_URL", "https://example.com?private=must-not-echo")
	if _, err := NewServer("test", false, WithProfile(ProfileCore)); err != nil {
		t.Fatalf("opt-out URL behavior changed: %v", err)
	}
	if _, err := NewServer("test", false, WithExperimentalWorkspace()); err == nil || strings.Contains(err.Error(), "must-not-echo") {
		t.Fatalf("opt-in should reject without exposing configured URL: %v", err)
	}
}

func TestWorkspaceUsesConfiguredFrontend(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-key")
	t.Setenv("REVYL_APP_URL", "")
	t.Setenv("REVYL_FRONTEND_PORT", "3217")
	server, err := NewServer("test", true, WithProfile(ProfileDev), WithExperimentalWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	if server.workspaceOrigin != "http://localhost:3217" {
		t.Fatalf("dev frontend = %s", server.workspaceOrigin)
	}
	t.Setenv("REVYL_APP_URL", "https://preview.example.com")
	server, err = NewServer("test", true, WithProfile(ProfileDev), WithExperimentalWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	if server.workspaceOrigin != "https://preview.example.com" {
		t.Fatalf("override frontend = %s", server.workspaceOrigin)
	}
}

func connectWorkspaceTestClient(t *testing.T, server *Server) *mcp.ClientSession {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.mcpServer.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "workspace-test", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}
