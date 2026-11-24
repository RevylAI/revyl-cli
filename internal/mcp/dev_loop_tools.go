package mcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
	} else if s.profile == ProfileCore || s.profile == ProfileFull {
		mcp.AddTool(s.mcpServer, startTool, s.handleStartDevLoopCoreCompat)
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
	} else if s.profile == ProfileCore || s.profile == ProfileFull {
		mcp.AddTool(s.mcpServer, stopTool, s.handleStopDevLoopCoreCompat)
	} else {
		mcp.AddTool(s.mcpServer, stopTool, s.handleStopDevLoopCompat)
	}
}

type StartDevLoopInput struct {
	BranchName                 string   `json:"branch_name,omitempty" jsonschema:"Exact Revyl branch name; overrides the project's Git branch and the app default branch"`
	BuildSelection             string   `json:"build_selection,omitempty" jsonschema:"Build choice: inherit (Automatic: the branch's default build), latest, or pinned with build_version_id."`
	Context                    string   `json:"context,omitempty" jsonschema:"Named dev context."`
	ProjectDir                 string   `json:"project_dir,omitempty" jsonschema:"Project or monorepo root. Nested Revyl projects are detected automatically."`
	BuildProfile               string   `json:"build_profile,omitempty"`
	Profile                    string   `json:"profile,omitempty"`
	SessionProfile             string   `json:"session_profile,omitempty" jsonschema:"Session profile name, or none."`
	Platform                   string   `json:"platform,omitempty" jsonschema:"Target platform for the cloud device (ios or android). Revyl infers it when only one eligible recipe remains."`
	PlatformKey                string   `json:"platform_key,omitempty" jsonschema:"Rejected; use build_profile."`
	AppID                      string   `json:"app_id,omitempty" jsonschema:"App ID override. Without a branch or build, uses the current Git branch only when automatic Git branch detection is enabled in the app's Build branches settings and that branch has a usable build; otherwise uses the app default branch."`
	BuildVersionID             string   `json:"build_version_id,omitempty" jsonschema:"Explicit build version ID. Preserves that exact uploaded artifact."`
	LaunchVars                 []string `json:"launch_vars,omitempty" jsonschema:"Additional organization launch-variable keys or IDs."`
	LaunchArgSets              []string `json:"launch_arg_sets,omitempty" jsonschema:"Stored iOS argument-set names or IDs."`
	LaunchArguments            []string `json:"launch_arguments,omitempty" jsonschema:"Inline non-secret iOS app argument tokens in exact order."`
	DisableInheritedLaunchVars bool     `json:"disable_inherited_launch_vars,omitempty" jsonschema:"Ignore REVYL_INHERITED_LAUNCH_ENV_VAR_IDS entirely; explicit launch_vars still apply."`
	Port                       int      `json:"port,omitempty" jsonschema:"Hot reload dev-server port override."`
	Timeout                    int      `json:"timeout,omitempty" jsonschema:"Device idle timeout in seconds (default 300)."`
	Remote                     bool     `json:"remote,omitempty" jsonschema:"Build native changes on a remote Revyl build runner."`
	SeedLatest                 bool     `json:"seed_latest,omitempty" jsonschema:"Install latest from the selected branch while a remote build runs."`
	Computer                   string   `json:"computer,omitempty" jsonschema:"Computer ID to run the device inside, or none; defaults to REVYL_COMPUTER_ID."`
}
