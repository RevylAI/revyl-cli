package mcp

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const workspaceAppURI = "ui://revyl/workspace-v2.html"
const workspaceAppMIMEType = "text/html;profile=mcp-app"

//go:embed workspace_app.html
var workspaceAppHTML string

var chatGPTPluginIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

type OpenWorkspaceInput struct {
	View      string `json:"view,omitempty" jsonschema:"View to open: atlas (default) or device."`
	AppID     string `json:"app_id,omitempty" jsonschema:"App UUID for the atlas view only."`
	SessionID string `json:"session_id,omitempty" jsonschema:"Session UUID for the device view only."`
}

type OpenWorkspaceOutput struct {
	WorkspaceURL string `json:"workspace_url"`
	ChatGPTURL   string `json:"chatgpt_url,omitempty"`
}

func WithExperimentalWorkspace() ServerOption {
	return func(s *Server) { s.experimentalWorkspace = true }
}

func validateWorkspaceOrigin(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || strings.Contains(parsed.Host, "*") ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery ||
		parsed.Fragment != "" || strings.Contains(raw, "#") ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" {
		return "", fmt.Errorf("experimental workspace requires REVYL_APP_URL to be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", fmt.Errorf("experimental workspace requires a frontend port between 1 and 65535")
		}
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func (s *Server) workspaceToolMeta() mcp.Meta {
	if !s.experimentalWorkspace {
		return nil
	}
	return mcp.Meta{"ui": map[string]any{"resourceUri": workspaceAppURI}}
}

func (s *Server) registerWorkspaceApp() {
	resourceMeta := mcp.Meta{
		"ui": map[string]any{
			"csp": map[string]any{
				"connectDomains":  []string{},
				"resourceDomains": []string{},
				"frameDomains":    []string{s.workspaceOrigin},
			},
		},
		"openai/widgetCSP": map[string]any{
			"connect_domains":  []string{},
			"resource_domains": []string{},
			"frame_domains":    []string{s.workspaceOrigin},
		},
		"openai/ui": map[string]any{
			"availableDisplayModes": []string{"fullscreen", "inline"},
			"preferredDisplayMode":  "fullscreen",
		},
	}
	originJSON, _ := json.Marshal(s.workspaceOrigin)
	html := strings.ReplaceAll(workspaceAppHTML, "__REVYL_ORIGIN__", string(originJSON))
	s.mcpServer.AddResource(&mcp.Resource{
		URI: workspaceAppURI, Name: "Revyl workspace", MIMEType: workspaceAppMIMEType,
		Meta: resourceMeta,
	}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI: workspaceAppURI, MIMEType: workspaceAppMIMEType, Text: html, Meta: resourceMeta,
		}}}, nil
	})
	toolMeta := s.workspaceToolMeta()
	toolMeta["openai/ui"] = map[string]any{
		"entrypoints": []map[string]string{{"type": "global"}, {"type": "thread"}},
	}
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "open_revyl_workspace", Title: "Open Revyl workspace",
		Description: "Request the experimental Revyl Atlas or device workspace. Empty arguments open Atlas. Returns workspace_url and, when configured, chatgpt_url: present chatgpt_url as a clickable Open in ChatGPT link so the user can navigate the existing panel to this exact view. A successful tool call does not prove the page loaded or video is playing. Does not provision a device or change data; browser sign-in is separate from CLI authentication.",
		Meta:        toolMeta,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)},
	}, s.handleOpenWorkspace)
}

func (s *Server) handleOpenWorkspace(_ context.Context, _ *mcp.CallToolRequest, input OpenWorkspaceInput) (*mcp.CallToolResult, OpenWorkspaceOutput, error) {
	view := input.View
	if view == "" {
		view = "atlas"
	}
	if view != "atlas" && view != "device" {
		return nil, OpenWorkspaceOutput{}, fmt.Errorf("view must be atlas or device")
	}
	for name, id := range map[string]string{"app_id": input.AppID, "session_id": input.SessionID} {
		if id != "" && (len(id) != 36 || uuid.Validate(id) != nil) {
			return nil, OpenWorkspaceOutput{}, fmt.Errorf("%s must be a hyphenated UUID", name)
		}
	}
	if (view == "atlas" && input.SessionID != "") || (view == "device" && input.AppID != "") {
		return nil, OpenWorkspaceOutput{}, fmt.Errorf("app_id requires the atlas view; session_id requires the device view")
	}
	path := "/atlas"
	if view == "device" {
		path = "/sessions"
		if input.SessionID != "" {
			path += "/" + input.SessionID
		}
	} else if input.AppID != "" {
		path = "/apps/" + input.AppID + "/atlas"
	}
	output := OpenWorkspaceOutput{WorkspaceURL: s.workspaceOrigin + path}
	if s.chatGPTPluginID != "" {
		output.ChatGPTURL = "https://chatgpt.com/plugins/" + s.chatGPTPluginID + "/app/open_revyl_workspace?" + url.Values{"path": {path}}.Encode()
	}
	return nil, output, nil
}
