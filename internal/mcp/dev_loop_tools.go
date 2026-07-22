package mcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerDevLoopTools registers the canonical CLI-backed dev-loop surface.
func (s *Server) registerDevLoopTools() {
	startTool := &mcp.Tool{
		Meta:        screenshotAppToolMeta(),
		Name:        "start_dev_loop",
		Description: "Start or reuse the canonical detached Revyl dev loop and return the viewer immediately.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Start Dev Loop",
			OpenWorldHint: boolPtr(true),
		},
	}
	if s.profile == ProfileDev {
		mcp.AddTool(s.mcpServer, startTool, s.handleStartDevLoopCommand)
	} else {
		mcp.AddTool(s.mcpServer, startTool, s.handleStartDevLoopCompat)
	}

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_dev_status",
		Description: "Get canonical device and build status for the active dev context.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Dev Status",
			ReadOnlyHint: true,
		},
	}, s.handleGetDevStatusCommand)

	stopTool := &mcp.Tool{
		Name:        "stop_dev_loop",
		Description: "Stop the canonical dev context and its owned device session.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Stop Dev Loop",
		},
	}
	if s.profile == ProfileDev {
		mcp.AddTool(s.mcpServer, stopTool, s.handleStopDevLoopCommand)
	} else {
		mcp.AddTool(s.mcpServer, stopTool, s.handleStopDevLoopCompat)
	}
}

// StartDevLoopInput contains the focused canonical dev-loop inputs.
type StartDevLoopInput struct {
	Context        string `json:"context,omitempty" jsonschema:"Optional named dev context."`
	ProjectDir     string `json:"project_dir,omitempty" jsonschema:"Optional project or monorepo root. Nested Revyl projects are detected automatically."`
	Platform       string `json:"platform,omitempty" jsonschema:"Target platform for the cloud device (ios or android). Default: ios."`
	PlatformKey    string `json:"platform_key,omitempty" jsonschema:"Optional build.platforms key override for resolving the dev build."`
	AppID          string `json:"app_id,omitempty" jsonschema:"Optional app ID override used to resolve latest build."`
	BuildVersionID string `json:"build_version_id,omitempty" jsonschema:"Optional explicit build version ID. Skips latest-build resolution."`
	Port           int    `json:"port,omitempty" jsonschema:"Optional hot reload dev-server port override."`
	Timeout        int    `json:"timeout,omitempty" jsonschema:"Device idle timeout in seconds (default 300)."`
	Remote         bool   `json:"remote,omitempty" jsonschema:"Build native changes on a remote Revyl build runner."`
	SeedLatest     bool   `json:"seed_latest,omitempty" jsonschema:"Install the latest existing build while a remote build runs."`
}
