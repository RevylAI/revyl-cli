package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// NextStep suggests a follow-up action to the agent.
type NextStep struct {
	Tool   string `json:"tool"`
	Params string `json:"params,omitempty"`
	Reason string `json:"reason"`
}

// boolPtr returns a pointer to a bool value. Used for ToolAnnotations fields.
func boolPtr(b bool) *bool { return &b }

// syncSessionsBestEffort refreshes in-memory sessions from backend, falling
// back to local persisted cache if backend sync is unavailable.
func (s *Server) syncSessionsBestEffort(ctx context.Context) {
	if s == nil || s.sessionMgr == nil {
		return
	}
	if err := s.sessionMgr.SyncSessions(ctx); err != nil {
		ui.PrintDebug("session sync failed; falling back to persisted cache: %v", err)
		s.sessionMgr.LoadPersistedSession()
	}
}

// shouldRetryResolveAfterSync returns true for resolve errors that can be
// recovered by refreshing session state from backend/cache.
func shouldRetryResolveAfterSync(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no active device sessions") ||
		strings.Contains(msg, "no session at index")
}

// resolveSessionWithHydration resolves a session, retrying once after a
// best-effort sync when the initial lookup indicates stale/empty local state.
func (s *Server) resolveSessionWithHydration(ctx context.Context, index int) (*DeviceSession, error) {
	session, err := s.sessionMgr.ResolveSession(index)
	if err == nil || !shouldRetryResolveAfterSync(err) {
		return session, err
	}
	s.syncSessionsBestEffort(ctx)
	return s.sessionMgr.ResolveSession(index)
}

// resolveToolSession resolves the session a session-scoped tool acts on.
//
// session_id wins and never falls back to the active session, so parallel
// agents sharing this server each keep their own device. A session this server
// already tracks is used as-is, keeping its screenshot anchors and idle timer;
// any other ID goes through ResolveSessionByID, the stateless path behind the
// CLI's --session-id. A session_index that names a different session is an
// error rather than a silent override. Without session_id, session_index and
// the active-session fallback resolve exactly as before.
func (s *Server) resolveToolSession(ctx context.Context, sessionIndex *int, sessionID string) (*DeviceSession, error) {
	s.recordSessionTargetMode(ctx, sessionIndex, sessionID)
	id := strings.TrimSpace(sessionID)
	if id == "" {
		index := -1
		if sessionIndex != nil {
			index = *sessionIndex
		}
		return s.resolveSessionWithHydration(ctx, index)
	}
	if sessionIndex != nil {
		indexed, err := s.resolveSessionWithHydration(ctx, *sessionIndex)
		if err != nil || !strings.EqualFold(indexed.SessionID, id) {
			atIndex := "no live session"
			if err == nil {
				atIndex = fmt.Sprintf("session %q", indexed.SessionID)
			}
			return nil, fmt.Errorf(
				"conflicting session targets: session_id %s and session_index %d name different sessions (session_index %d is %s). Pass session_id alone",
				id, *sessionIndex, *sessionIndex, atIndex,
			)
		}
		return indexed, nil
	}
	if tracked := s.trackedSessionByID(id); tracked != nil {
		return tracked, nil
	}
	session, err := s.sessionMgr.ResolveSessionByID(ctx, id)
	if err != nil {
		return nil, explainSessionIDResolveError(err)
	}
	return session, nil
}

func (s *Server) trackedSessionByID(sessionID string) *DeviceSession {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return nil
	}
	for _, session := range s.sessionMgr.ListSessions() {
		if strings.EqualFold(session.SessionID, id) {
			return session
		}
	}
	return nil
}

func explainSessionIDResolveError(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "session not found or not accessible"):
		return fmt.Errorf("%s. Check the session_id, or call list_device_sessions() to see live sessions and their IDs", msg)
	case strings.Contains(msg, "is in terminal state"):
		return fmt.Errorf("%s. Start a new one with start_device_session(), or read its results with get_session_report(session_id=...)", msg)
	case strings.Contains(msg, "has no workflow run ID"):
		return fmt.Errorf("%s. Wait a few seconds for the device to finish provisioning, then retry", msg)
	}
	return err
}

// pinnedSessionTarget returns the session_index and session_id inputs that
// select exactly this session again, for tools that call other tools. It pins
// by server-issued ID whenever the session has one: a local index can be
// reused by a different device if the session is stopped and another starts
// mid-call, and an ID that no longer names a live session fails instead.
func pinnedSessionTarget(session *DeviceSession) (*int, string) {
	if session.SessionID != "" {
		return nil, session.SessionID
	}
	index := session.Index
	return &index, ""
}

type pinnedSessionContextKey struct{}

// withPinnedSession marks tool calls a tool makes on its own behalf, so they
// do not count as the caller choosing session_id targeting.
func withPinnedSession(ctx context.Context) context.Context {
	return context.WithValue(ctx, pinnedSessionContextKey{}, true)
}

// recordSessionTargetMode records how the caller chose the device session on
// the serve command's terminal analytics event: session_id, session_index, or
// active when it named none. Only the most explicit mode used during this
// server's lifetime is kept, and no session identifier is recorded.
func (s *Server) recordSessionTargetMode(ctx context.Context, sessionIndex *int, sessionID string) {
	if pinned, _ := ctx.Value(pinnedSessionContextKey{}).(bool); pinned {
		return
	}
	mode, rank := "active", 1
	switch {
	case strings.TrimSpace(sessionID) != "":
		mode, rank = "session_id", 3
	case sessionIndex != nil:
		mode, rank = "session_index", 2
	}
	s.sessionTargetMu.Lock()
	defer s.sessionTargetMu.Unlock()
	if rank <= s.sessionTargetRank {
		return
	}
	s.sessionTargetRank = rank
	analytics.SetCommandCompletion(ctx, analytics.CommandCompletion{Domain: "mcp_session_target", DomainStatus: mode})
}

// registerScreenshotTool registers the standalone native screenshot tool.
func (s *Server) registerScreenshotTool() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Meta:        screenshotAppToolMeta(),
		Name:        "screenshot",
		Description: "Capture the current device screen as a PNG image. Returns the image natively for rendering.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Take Screenshot",
			ReadOnlyHint: true,
		},
	}, s.handleScreenshot)
}

// registerDeviceNavigateTool registers URL and deep-link navigation.
func (s *Server) registerDeviceNavigateTool() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_navigate",
		Description: "Open a URL or deep link on the device.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Navigate to URL",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceNavigate)
}

// registerDeviceTools registers all device interaction MCP tools.
func (s *Server) registerDeviceTools() {
	// Session management
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "start_device_session",
		Meta:        s.workspaceToolMeta(),
		Description: "Provision a cloud-hosted Android or iOS device. Only platform is required; optionally provide app_id, build_version_id, app_url, or app_link. Returns session_id and a viewer_url to watch the device live in a browser. Pass that session_id to every later device tool call when more than one session may be live, for example when agents run in parallel.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Start Device Session",
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleStartDeviceSession)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "stop_device_session",
		Description: "Release a device session and stop billing. Pass session_id to stop that exact session; without it the active session is stopped.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Stop Device Session",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleStopDeviceSession)

	// Device actions (grounded by default)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_tap",
		Description: "Tap an element by description (grounded) or coordinates (raw). Provide target OR x+y.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Tap Element",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceTap)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_double_tap",
		Description: "Double-tap an element by description (grounded) or coordinates (raw). Provide target OR x+y.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Double Tap Element",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceDoubleTap)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_long_press",
		Description: "Long press an element by description (grounded) or coordinates (raw). Provide target OR x+y.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Long Press Element",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceLongPress)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_type",
		Description: "Type text into an element by description (grounded) or coordinates (raw). Provide target OR x+y, plus text.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Type Text",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceType)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_swipe",
		Description: "Swipe from an element. direction='up' moves finger up (scrolls content down). Provide target OR x+y, plus direction.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Swipe on Device",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceSwipe)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_drag",
		Description: "Drag from one point to another using raw coordinates.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Drag on Device",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceDrag)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_pinch",
		Description: "Pinch/zoom at an element by description (grounded) or coordinates (raw). Provide target OR x+y. Use scale>1 to zoom in, <1 to zoom out.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Pinch/Zoom",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDevicePinch)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_clear_text",
		Description: "Clear text from an input by description (grounded) or coordinates (raw). Provide target OR x+y.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Clear Text",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceClearText)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_wait",
		Description: "Pause for duration_ms before continuing. Useful for explicit UI settle windows.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Wait",
			DestructiveHint: boolPtr(false),
		},
	}, s.handleDeviceWait)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_back",
		Description: "Press the Android back button (returns an error on unsupported platforms).",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Back Button",
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceBack)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_key",
		Description: "Send a non-printable key to the focused field (ENTER or BACKSPACE).",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Send Key",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceKey)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_shake",
		Description: "Trigger a shake gesture on the device.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Shake Device",
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceShake)

	s.registerScreenshotTool()

	// App management
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "install_app",
		Description: "Install an app on the device. Provide either app_url (direct URL to .apk/.ipa) or build_version_id (from a previous upload_build). Returns the detected bundle_id for use with launch_app.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Install App",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleInstallApp)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "launch_app",
		Description: "Launch an app on the device. Omit bundle_id to launch the app this session installed — do not guess an ID; a bundle the device does not have is rejected with the list of what is installed.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Launch App",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleLaunchApp)

	// Device utility actions
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_go_home",
		Description: "Return to the device home screen.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Go Home",
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceGoHome)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_kill_app",
		Description: "Kill the installed app on the device.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Kill App",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceKillApp)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_open_app",
		Description: "Open a system app by friendly name (e.g. 'settings', 'safari', 'chrome') or raw bundle ID. Falls back to raw bundle ID if the name is not recognized.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Open App",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceOpenApp)

	s.registerDeviceNavigateTool()

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_set_location",
		Description: "Set device GPS coordinates (latitude and longitude).",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Set Location",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceSetLocation)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_download_file",
		Description: "Download a file to the device from a URL.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Download File",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceDownloadFile)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_instruction",
		Description: "Run one high-level instruction step directly on the active live device session.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Run Instruction Step",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceInstruction)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_validation",
		Description: "Run one high-level validation step directly on the active live device session.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Run Validation Step",
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceValidation)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_extract",
		Description: "Run one high-level extract step directly on the active live device session.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Run Extract Step",
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceExtract)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_code_execution",
		Description: "Run one high-level code_execution step directly on the active live device session using a script ID.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Run Code Execution Step",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleDeviceCodeExecution)

	// Info
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_session_info",
		Description: "Get current device session status, platform, viewer URL, and time remaining.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Session Info",
			ReadOnlyHint: true,
		},
	}, s.handleGetSessionInfo)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_session_report",
		Description: "Get the report for the current device session, including steps, actions, video URL, and status.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Session Report",
			ReadOnlyHint: true,
		},
	}, s.handleGetSessionReport)

	// Diagnostics
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_doctor",
		Description: "Run diagnostics on auth, session health, worker reachability, grounding model availability, and environment. First aid for any issue.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Device Doctor",
			ReadOnlyHint: true,
		},
	}, s.handleDeviceDoctor)

	// Multi-session management
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_device_sessions",
		Description: "List all active device sessions with their session_id, index, platform, status, and uptime. Pass a session_id to other device tools to target that session.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Device Sessions",
			ReadOnlyHint: true,
		},
	}, s.handleListDeviceSessions)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "switch_device_session",
		Description: "Switch the active session to the given index. Subsequent commands that name no session will target it by default. In parallel work, pass session_id to each tool instead of switching.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Switch Active Session",
			DestructiveHint: boolPtr(false),
		},
	}, s.handleSwitchDeviceSession)

	s.registerRebuildTool()

	// Live performance polling
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "poll_performance_metrics",
		Description: "Poll live CPU, memory, and FPS metrics from an active device session. Returns incremental samples since the last cursor. Use with cursor=\"0\" for the first call, then pass next_cursor from the response for subsequent calls.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Poll Performance Metrics",
			ReadOnlyHint: true,
		},
	}, s.handlePollPerformanceMetrics)

	// Device-state inspection (iOS sim only — Android handlers no-op).
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_state_list",
		Description: "List all UserDefaults plist files and SQLite databases in the app's data container, with table schemas and row counts. Use this first to discover what's available before calling device_state_query.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "List Device State",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleDeviceStateList)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_state_snapshot",
		Description: "Capture a full snapshot of the app's UserDefaults + SQLite state and return a snapshot_id. Pass that id to device_state_diff after performing actions to see what changed.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Snapshot Device State",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleDeviceStateSnapshot)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_state_diff",
		Description: "Return a rollup of all device-state changes since the given snapshot_id. UserDefaults diffs are precise (key/from/to). SQLite diffs include schema changes and row count deltas.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Diff Device State",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleDeviceStateDiff)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "device_state_query",
		Description: "Targeted read of one UserDefaults key OR read-only SQL against one SQLite DB. Set target='userdefaults' with plist_path (and optional key) OR target='sqlite' with db_path, sql, and optional params. SQL must be a single SELECT or WITH...SELECT.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Query Device State",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleDeviceStateQuery)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "poll_network_requests",
		Description: "Poll live network requests from an active device session. Returns incremental request rows since the last cursor. Use with cursor=\"0\" for the first call, then pass next_cursor from the response for subsequent calls.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Poll Network Requests",
			ReadOnlyHint: true,
		},
	}, s.handlePollNetworkRequests)
}

// --- Session Management ---

// StartDeviceSessionInput defines input for start_device_session.
type StartDeviceSessionInput struct {
	Platform                   string   `json:"platform" jsonschema:"Target platform: ios or android (REQUIRED)"`
	AppID                      string   `json:"app_id,omitempty" jsonschema:"App ID to pre-install"`
	BuildVersionID             string   `json:"build_version_id,omitempty" jsonschema:"Specific build version ID"`
	AppURL                     string   `json:"app_url,omitempty" jsonschema:"URL to download app from (.apk or .ipa). Provide this OR build_version_id."`
	AppLink                    string   `json:"app_link,omitempty" jsonschema:"Deep link URL to launch after app start (optional)."`
	LaunchVars                 []string `json:"launch_vars,omitempty" jsonschema:"Org launch variable keys or IDs to apply to a raw session at boot."`
	LaunchArgSets              []string `json:"launch_arg_sets,omitempty" jsonschema:"Stored iOS argument-set names or IDs to apply at boot."`
	LaunchArguments            []string `json:"launch_arguments,omitempty" jsonschema:"Inline non-secret iOS app argument tokens in exact order."`
	DisableInheritedLaunchVars bool     `json:"disable_inherited_launch_vars,omitempty" jsonschema:"Ignore REVYL_INHERITED_LAUNCH_ENV_VAR_IDS entirely; explicit launch_vars still apply."`
	TestID                     string   `json:"test_id,omitempty" jsonschema:"Test ID to link session to"`
	IdleTimeout                int      `json:"idle_timeout,omitempty" jsonschema:"Idle timeout in seconds (default 900)"`
	NoOpen                     bool     `json:"no_open,omitempty" jsonschema:"Skip opening the local browser (default: false). The experimental workspace always skips local browser opening."`
}

// StartDeviceSessionOutput defines output for start_device_session.
type StartDeviceSessionOutput struct {
	Success            bool       `json:"success"`
	SessionID          string     `json:"session_id,omitempty"`
	SessionIndex       int        `json:"session_index"`
	Platform           string     `json:"platform,omitempty"`
	ViewerURL          string     `json:"viewer_url,omitempty"`
	WhepURL            string     `json:"whep_url,omitempty"`
	IdleTimeoutSeconds float64    `json:"idle_timeout_seconds,omitempty"`
	Error              string     `json:"error,omitempty"`
	NextSteps          []NextStep `json:"next_steps,omitempty"`
}

var openDeviceSessionBrowser = ui.OpenBrowser

func (s *Server) handleStartDeviceSession(ctx context.Context, req *mcp.CallToolRequest, input StartDeviceSessionInput) (*mcp.CallToolResult, StartDeviceSessionOutput, error) {
	platform := strings.ToLower(normalizeOptionalToolInput(input.Platform))
	if platform == "" {
		return nil, StartDeviceSessionOutput{Success: false, Error: "platform is required (ios or android)"}, nil
	}
	if platform != "ios" && platform != "android" {
		return nil, StartDeviceSessionOutput{Success: false, Error: "platform must be 'ios' or 'android'"}, nil
	}
	appID, buildVersionID, appURL, err := normalizeStartArtifactInputs(input.AppID, input.BuildVersionID, input.AppURL)
	if err != nil {
		return nil, StartDeviceSessionOutput{Success: false, Error: err.Error()}, nil
	}

	timeoutSecs := input.IdleTimeout
	if timeoutSecs <= 0 {
		timeoutSecs = 900
		if s.project != nil && s.project.Authored.Session != nil && s.project.Authored.Session.IdleTimeoutSeconds != nil {
			timeoutSecs = *s.project.Authored.Session.IdleTimeoutSeconds
		}
	}
	timeout := time.Duration(timeoutSecs) * time.Second
	idx, session, err := s.sessionMgr.StartSession(ctx, StartSessionOptions{
		Platform:                   platform,
		AppID:                      appID,
		BuildVersionID:             buildVersionID,
		AppURL:                     appURL,
		AppLink:                    normalizeOptionalToolInput(input.AppLink),
		LaunchVars:                 input.LaunchVars,
		LaunchArgSets:              input.LaunchArgSets,
		LaunchArguments:            input.LaunchArguments,
		DisableInheritedLaunchVars: input.DisableInheritedLaunchVars,
		TestID:                     input.TestID,
		IdleTimeout:                timeout,
	})
	if err != nil {
		return nil, StartDeviceSessionOutput{Success: false, Error: err.Error()}, nil
	}

	if !input.NoOpen && !s.experimentalWorkspace {
		reportURL := fmt.Sprintf("%s/tests/report?sessionId=%s",
			config.GetAppURL(s.devMode), session.SessionID)
		_ = openDeviceSessionBrowser(reportURL)
	}

	return nil, StartDeviceSessionOutput{
		Success:            true,
		SessionID:          session.SessionID,
		SessionIndex:       idx,
		Platform:           session.Platform,
		ViewerURL:          session.ViewerURL,
		WhepURL:            stringValue(session.WhepURL),
		IdleTimeoutSeconds: timeout.Seconds(),
		NextSteps: []NextStep{
			{Tool: "screenshot", Params: sessionIDParam(session.SessionID), Reason: "See the current device screen"},
			{Tool: "install_app", Params: sessionIDParam(session.SessionID), Reason: "Install an app on the device"},
		},
	}, nil
}

func sessionIDParam(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	return fmt.Sprintf("session_id=%q", sessionID)
}

// StopDeviceSessionInput defines input for stop_device_session.
type StopDeviceSessionInput struct {
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to stop. Omit to stop the active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
	All          bool   `json:"all,omitempty" jsonschema:"Stop all sessions."`
}

// StopDeviceSessionOutput defines output for stop_device_session.
type StopDeviceSessionOutput struct {
	Success         bool                      `json:"success"`
	Error           string                    `json:"error,omitempty"`
	NextSteps       []NextStep                `json:"next_steps,omitempty"`
	RequestAccepted bool                      `json:"request_accepted"`
	SessionSettled  bool                      `json:"session_settled"`
	DeviceReleased  bool                      `json:"device_released"`
	Results         []DeviceSessionStopResult `json:"results,omitempty"`
}

func (s *Server) handleStopDeviceSession(ctx context.Context, req *mcp.CallToolRequest, input StopDeviceSessionInput) (*mcp.CallToolResult, StopDeviceSessionOutput, error) {
	if input.All {
		s.syncSessionsBestEffort(ctx)
		result, err := s.sessionMgr.StopAllSessions(ctx)
		output := StopDeviceSessionOutput{
			Success: result.RequestAccepted, RequestAccepted: result.RequestAccepted,
			SessionSettled: result.SessionSettled, DeviceReleased: result.DeviceReleased,
			Results: result.Results,
		}
		if err != nil {
			output.Error = err.Error()
		}
		if !result.SessionSettled || !result.DeviceReleased {
			output.NextSteps = []NextStep{{Tool: "list_device_sessions", Reason: "Check remaining sessions and retry Stop if needed"}}
		}
		return nil, output, nil
	}

	var session *DeviceSession
	if id := strings.TrimSpace(input.SessionID); id != "" && input.SessionIndex == nil {
		s.recordSessionTargetMode(ctx, nil, id)
		// Like the CLI's stop --session-id, an ID this server does not track
		// is stopped by ID without resolving it first, so a session that is
		// still provisioning can be stopped too.
		session = s.trackedSessionByID(id)
		if session == nil {
			session = &DeviceSession{Index: UnattachedSessionIndex, SessionID: id}
		}
	} else {
		resolved, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
		if err != nil {
			return nil, StopDeviceSessionOutput{Success: false, Error: err.Error()}, nil
		}
		session = resolved
	}
	if err := s.sessionMgr.StopResolvedSession(ctx, session); err != nil {
		var pending *api.DeviceSessionStopPendingError
		if errors.As(err, &pending) {
			return nil, StopDeviceSessionOutput{
				Success:         true,
				RequestAccepted: true,
				SessionSettled:  pending.Response.SessionSettled != nil && *pending.Response.SessionSettled,
				DeviceReleased:  pending.Response.DeviceReleased != nil && *pending.Response.DeviceReleased,
				NextSteps:       []NextStep{{Tool: "list_device_sessions", Reason: "Stop requested; check the session while cleanup finishes"}},
			}, nil
		}
		return nil, StopDeviceSessionOutput{Success: false, Error: err.Error()}, nil
	}
	return nil, StopDeviceSessionOutput{
		Success:         true,
		RequestAccepted: true,
		SessionSettled:  true,
		DeviceReleased:  true,
		NextSteps: []NextStep{
			{Tool: "create_test", Reason: "Save the session as a reusable test"},
		},
	}, nil
}

// --- Dual-param validation helper ---

// resolveCoordsResult holds the output of resolveCoords, including the concrete
// session so callers can reuse it for the subsequent worker request without
// re-resolving (which would be a TOCTOU race if the active session changed).
type resolveCoordsResult struct {
	X       int
	Y       int
	Session *DeviceSession
}

// resolveCoords resolves target OR x/y to concrete coordinates for a given session.
// Returns the resolved coordinates AND the concrete session that was used.
// Callers must use result.Session for any follow-up WorkerRequestOnSession
// call to guarantee grounding and action target the same device.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - target: Natural language element description (mutually exclusive with x+y).
//   - x, y: Raw pixel coordinates (mutually exclusive with target).
//   - sessionIndex, sessionID: The tool's session targeting inputs (see resolveToolSession).
//
// Returns:
//   - *resolveCoordsResult: Resolved coordinates and the concrete session.
//   - error: Validation or resolution error.
func (s *Server) resolveCoords(ctx context.Context, target string, x, y *int, sessionIndex *int, sessionID string) (*resolveCoordsResult, error) {
	hasTarget := target != ""
	hasCoords := x != nil && y != nil

	if hasTarget && hasCoords {
		return nil, fmt.Errorf("provide either target OR x+y, not both")
	}
	if !hasTarget && !hasCoords {
		return nil, fmt.Errorf("provide target (element description) or x+y (pixel coordinates)")
	}
	if (x != nil && y == nil) || (x == nil && y != nil) {
		return nil, fmt.Errorf("both x and y are required when using coordinates")
	}

	session, err := s.resolveToolSession(ctx, sessionIndex, sessionID)
	if err != nil {
		return nil, err
	}
	s.sessionMgr.ResetIdleTimer(session.Index)

	if hasCoords {
		return &resolveCoordsResult{X: *x, Y: *y, Session: session}, nil
	}

	resolved, err := s.sessionMgr.ResolveTargetOnSession(ctx, session, target)
	if err != nil {
		return nil, err
	}
	return &resolveCoordsResult{
		X:       resolved.X,
		Y:       resolved.Y,
		Session: session,
	}, nil
}

// resolveCoordsFromAnchor grounds a target against one captured screenshot.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - target: Natural-language element description.
//   - screenToken: Token for the screenshot that must be used for grounding.
//   - sessionIndex, sessionID: The tool's session targeting inputs (see resolveToolSession).
//
// Returns:
//   - *resolveCoordsResult: Grounded coordinates and the concrete session.
//   - error: Validation, session, anchor, or grounding failure.
func (s *Server) resolveCoordsFromAnchor(
	ctx context.Context,
	target string,
	screenToken string,
	sessionIndex *int,
	sessionID string,
) (*resolveCoordsResult, error) {
	if strings.TrimSpace(target) == "" {
		return nil, fmt.Errorf("target is required for anchored grounding")
	}
	if strings.TrimSpace(screenToken) == "" {
		return nil, fmt.Errorf("screen_token is required for anchored grounding")
	}

	session, err := s.resolveToolSession(ctx, sessionIndex, sessionID)
	if err != nil {
		return nil, err
	}
	if session.Index == UnattachedSessionIndex {
		return nil, fmt.Errorf(
			"session %s was not started or listed by this MCP server, so this server keeps no screenshots of it and cannot ground a drag between two targets. Describe the drag as an instruction instead: interact(strategy=\"instruction\", task=\"<the drag>\", session_id=\"%s\")",
			session.SessionID, session.SessionID,
		)
	}
	s.sessionMgr.ResetIdleTimer(session.Index)

	resolved, err := s.sessionMgr.ResolveTargetFromAnchor(ctx, session.Index, screenToken, target)
	if err != nil {
		return nil, err
	}
	return &resolveCoordsResult{
		X:       resolved.X,
		Y:       resolved.Y,
		Session: session,
	}, nil
}

// errorNextSteps returns recovery-oriented NextSteps based on the error type.
func errorNextSteps(err error) []NextStep {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no active device session"):
		return []NextStep{{Tool: "start_device_session", Params: "platform=\"android\"", Reason: "Start a session first"}}
	case strings.Contains(msg, "is in terminal state"):
		return []NextStep{
			{Tool: "get_session_report", Reason: "The session has ended; read its results by passing the same session_id"},
			{Tool: "start_device_session", Reason: "Start a new session to keep testing"},
		}
	case strings.Contains(msg, "conflicting session targets") ||
		strings.Contains(msg, "session not found or not accessible") ||
		strings.Contains(msg, "multiple sessions active") ||
		strings.Contains(msg, "no session at index"):
		return []NextStep{{Tool: "list_device_sessions", Reason: "See live sessions and their session_id"}}
	case strings.Contains(msg, "screenshot required") ||
		strings.Contains(msg, "screen_token") ||
		strings.Contains(msg, "action limit reached") ||
		strings.Contains(msg, "re-anchor"):
		return []NextStep{{Tool: "screenshot", Reason: "Re-anchor to the latest screen before continuing"}}
	case strings.Contains(msg, "could not locate") || strings.Contains(msg, "grounding"):
		return []NextStep{{Tool: "screenshot", Reason: "See the screen and rephrase the target description"}}
	case strings.Contains(msg, "worker"):
		return []NextStep{{Tool: "device_doctor", Reason: "Diagnose the worker issue"}}
	default:
		return []NextStep{{Tool: "device_doctor", Reason: "Run diagnostics to understand the failure"}}
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

// normalizeOptionalToolInput trims whitespace from a tool input field.
func normalizeOptionalToolInput(value string) string {
	return strings.TrimSpace(value)
}

// normalizeRequiredToolInput trims a required tool input and errors when the
// resulting value is empty.
func normalizeRequiredToolInput(value, field string) (string, error) {
	normalized := normalizeOptionalToolInput(value)
	if normalized == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	return normalized, nil
}

// validateExternalURL checks that a URL uses http(s) and does not point at
// internal/metadata addresses (RFC 1918, link-local, cloud metadata).
// Returns the cleaned URL string or an error describing why it was rejected.
//
// Parameters:
//   - rawURL: The user-provided URL string.
//
// Returns:
//   - string: The validated URL (unchanged if valid).
//   - error: Non-nil when the URL scheme is not http/https, the host cannot
//     be resolved, or the resolved IP is in a blocked range.
func validateExternalURL(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("URL scheme %q is not allowed (only http/https)", parsed.Scheme)
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return "", fmt.Errorf("URL has no hostname")
	}

	ip := net.ParseIP(hostname)
	if ip == nil {
		ips, lookupErr := net.LookupIP(hostname)
		if lookupErr == nil && len(ips) > 0 {
			ip = ips[0]
		}
	}

	if ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return "", fmt.Errorf("URL host %s resolves to a private/internal address — not allowed", hostname)
		}
		if ip.Equal(net.ParseIP("169.254.169.254")) {
			return "", fmt.Errorf("URL host %s points at the cloud metadata service — not allowed", hostname)
		}
	}

	return rawURL, nil
}

// normalizeStartArtifactInputs trims start-session artifact selectors and
// ensures the caller does not provide more than one source.
func normalizeStartArtifactInputs(appID, buildVersionID, appURL string) (string, string, string, error) {
	normalizedAppID := normalizeOptionalToolInput(appID)
	normalizedBuildVersionID := normalizeOptionalToolInput(buildVersionID)
	normalizedAppURL := normalizeOptionalToolInput(appURL)

	provided := 0
	for _, candidate := range []string{normalizedAppID, normalizedBuildVersionID, normalizedAppURL} {
		if candidate != "" {
			provided++
		}
	}
	if provided > 1 {
		return "", "", "", fmt.Errorf("provide only one of app_id, build_version_id, or app_url")
	}
	return normalizedAppID, normalizedBuildVersionID, normalizedAppURL, nil
}

type liveStepOutputSummary struct {
	Status       string `json:"status"`
	StatusReason string `json:"status_reason"`
}

type DeviceLiveStepOutput struct {
	Success       bool           `json:"success"`
	StepType      string         `json:"step_type,omitempty"`
	StepID        string         `json:"step_id,omitempty"`
	WorkflowRunID string         `json:"workflow_run_id,omitempty"`
	SessionID     string         `json:"session_id,omitempty"`
	ExecutionID   string         `json:"execution_id,omitempty"`
	ReportID      string         `json:"report_id,omitempty"`
	StepOutput    map[string]any `json:"step_output,omitempty"`
	Error         string         `json:"error,omitempty"`
	NextSteps     []NextStep     `json:"next_steps,omitempty"`
}

func liveStepErrorFromResponse(response *LiveStepResponse) string {
	if response == nil {
		return "live step failed"
	}
	if len(response.StepOutput) > 0 {
		var summary liveStepOutputSummary
		if err := json.Unmarshal(response.StepOutput, &summary); err == nil && strings.TrimSpace(summary.StatusReason) != "" {
			return strings.TrimSpace(summary.StatusReason)
		}
	}
	if response.Success {
		return ""
	}
	return "live step failed"
}

func (s *Server) executeLiveStep(
	ctx context.Context,
	sessionIndex *int,
	sessionID string,
	request LiveStepRequest,
	successReason string,
) (*DeviceLiveStepOutput, error) {
	session, err := s.resolveToolSession(ctx, sessionIndex, sessionID)
	if err != nil {
		return &DeviceLiveStepOutput{
			Success:   false,
			Error:     err.Error(),
			NextSteps: errorNextSteps(err),
		}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)

	response, err := s.sessionMgr.ExecuteLiveStepOnSession(ctx, session, request)
	if err != nil {
		return &DeviceLiveStepOutput{
			Success:   false,
			StepType:  request.StepType,
			Error:     err.Error(),
			NextSteps: errorNextSteps(err),
		}, nil
	}

	outcomeErr := EvaluateLiveStepOutcome(response, request.StepType)
	if outcomeErr != nil {
		response.Success = false
	}
	stepOutput := make(map[string]any)
	if len(response.StepOutput) > 0 {
		_ = json.Unmarshal(response.StepOutput, &stepOutput)
	}
	output := &DeviceLiveStepOutput{
		Success:       response.Success,
		StepType:      response.StepType,
		StepID:        response.StepID,
		WorkflowRunID: response.WorkflowRunID,
		SessionID:     response.SessionID,
		ExecutionID:   response.ExecutionID,
		ReportID:      response.ReportID,
		StepOutput:    stepOutput,
	}
	if response.Success {
		output.NextSteps = []NextStep{
			{Tool: "screenshot", Reason: successReason},
		}
		return output, nil
	}

	if outcomeErr != nil {
		output.Error = outcomeErr.Error()
	} else {
		output.Error = liveStepErrorFromResponse(response)
	}
	output.NextSteps = errorNextSteps(fmt.Errorf("%s", output.Error))
	return output, nil
}

type DeviceInstructionInput struct {
	Description  string `json:"description" jsonschema:"Natural-language instruction to run on the active device session (REQUIRED)."`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceValidationInput struct {
	Description  string `json:"description" jsonschema:"Natural-language validation to run on the active device session (REQUIRED)."`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceExtractInput struct {
	Description  string `json:"description" jsonschema:"Natural-language extract step to run on the active device session (REQUIRED)."`
	VariableName string `json:"variable_name,omitempty" jsonschema:"Optional variable name for storing the extracted value."`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceCodeExecutionInput struct {
	ScriptID     string `json:"script_id" jsonschema:"Script ID for the code_execution step (REQUIRED)."`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

func (s *Server) handleDeviceInstruction(ctx context.Context, req *mcp.CallToolRequest, input DeviceInstructionInput) (*mcp.CallToolResult, DeviceLiveStepOutput, error) {
	if strings.TrimSpace(input.Description) == "" {
		return nil, DeviceLiveStepOutput{Success: false, Error: "description is required"}, nil
	}
	output, err := s.executeLiveStep(
		ctx,
		input.SessionIndex,
		input.SessionID,
		LiveStepRequest{
			StepType:        "instruction",
			StepDescription: strings.TrimSpace(input.Description),
		},
		"Review the screen after the instruction step",
	)
	if err != nil {
		return nil, DeviceLiveStepOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	if !output.Success {
		return &mcp.CallToolResult{IsError: true}, *output, nil
	}
	return nil, *output, nil
}

func (s *Server) handleDeviceValidation(ctx context.Context, req *mcp.CallToolRequest, input DeviceValidationInput) (*mcp.CallToolResult, DeviceLiveStepOutput, error) {
	if strings.TrimSpace(input.Description) == "" {
		return nil, DeviceLiveStepOutput{Success: false, Error: "description is required"}, nil
	}
	output, err := s.executeLiveStep(
		ctx,
		input.SessionIndex,
		input.SessionID,
		LiveStepRequest{
			StepType:        "validation",
			StepDescription: strings.TrimSpace(input.Description),
		},
		"Review the screen after the validation step",
	)
	if err != nil {
		return nil, DeviceLiveStepOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	if !output.Success {
		return &mcp.CallToolResult{IsError: true}, *output, nil
	}
	return nil, *output, nil
}

func (s *Server) handleDeviceExtract(ctx context.Context, req *mcp.CallToolRequest, input DeviceExtractInput) (*mcp.CallToolResult, DeviceLiveStepOutput, error) {
	if strings.TrimSpace(input.Description) == "" {
		return nil, DeviceLiveStepOutput{Success: false, Error: "description is required"}, nil
	}

	request := LiveStepRequest{
		StepType:        "extract",
		StepDescription: strings.TrimSpace(input.Description),
	}
	if strings.TrimSpace(input.VariableName) != "" {
		request.Metadata = map[string]any{
			"variable_name": strings.TrimSpace(input.VariableName),
		}
	}

	output, err := s.executeLiveStep(
		ctx,
		input.SessionIndex,
		input.SessionID,
		request,
		"Review the screen after the extract step",
	)
	if err != nil {
		return nil, DeviceLiveStepOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	return nil, *output, nil
}

func (s *Server) handleDeviceCodeExecution(ctx context.Context, req *mcp.CallToolRequest, input DeviceCodeExecutionInput) (*mcp.CallToolResult, DeviceLiveStepOutput, error) {
	if strings.TrimSpace(input.ScriptID) == "" {
		return nil, DeviceLiveStepOutput{Success: false, Error: "script_id is required"}, nil
	}
	output, err := s.executeLiveStep(
		ctx,
		input.SessionIndex,
		input.SessionID,
		LiveStepRequest{
			StepType:        "code_execution",
			StepDescription: strings.TrimSpace(input.ScriptID),
		},
		"Review the screen after the code execution step",
	)
	if err != nil {
		return nil, DeviceLiveStepOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	return nil, *output, nil
}

// --- Device Tap ---

// DeviceTapInput defines input for device_tap.
type DeviceTapInput struct {
	Target       string `json:"target,omitempty" jsonschema:"Element to tap. Use visible text ('Sign In button') or visual traits ('blue rectangle'). Auto-resolves via AI grounding."`
	X            *int   `json:"x,omitempty" jsonschema:"Raw X pixel coordinate (bypasses grounding)"`
	Y            *int   `json:"y,omitempty" jsonschema:"Raw Y pixel coordinate (bypasses grounding)"`
	ScreenToken  string `json:"screen_token,omitempty" jsonschema:"Optional screen token from screenshot()."`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

// DeviceTapOutput defines output for device_tap.
type DeviceTapOutput struct {
	Success   bool       `json:"success"`
	X         int        `json:"x"`
	Y         int        `json:"y"`
	LatencyMs float64    `json:"latency_ms"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

type workerTapTargetResponse struct {
	Success   bool    `json:"success"`
	Found     bool    `json:"found"`
	X         int     `json:"x"`
	Y         int     `json:"y"`
	LatencyMs float64 `json:"latency_ms"`
	Error     string  `json:"error,omitempty"`
}

func (s *Server) handleDeviceTap(ctx context.Context, req *mcp.CallToolRequest, input DeviceTapInput) (*mcp.CallToolResult, DeviceTapOutput, error) {
	start := time.Now()
	hasTarget := input.Target != ""
	hasCoords := input.X != nil && input.Y != nil
	if hasTarget && hasCoords {
		err := fmt.Errorf("provide either target OR x+y, not both")
		return nil, DeviceTapOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	if !hasTarget && !hasCoords {
		err := fmt.Errorf("provide target (element description) or x+y (pixel coordinates)")
		return nil, DeviceTapOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	if (input.X != nil && input.Y == nil) || (input.X == nil && input.Y != nil) {
		err := fmt.Errorf("both x and y are required when using coordinates")
		return nil, DeviceTapOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	if hasTarget {
		session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
		if err != nil {
			return nil, DeviceTapOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
		}
		respBody, err := s.sessionMgr.WorkerRequestOnSession(ctx, session, "/tap_target", map[string]string{
			"target":     input.Target,
			"session_id": session.SessionID,
		})
		latency := float64(time.Since(start).Milliseconds())
		if err != nil {
			return nil, DeviceTapOutput{Success: false, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
		}
		var resp workerTapTargetResponse
		if err := json.Unmarshal(respBody, &resp); err != nil {
			return nil, DeviceTapOutput{Success: false, LatencyMs: latency, Error: fmt.Sprintf("worker tap_target returned invalid JSON: %v", err), NextSteps: errorNextSteps(err)}, nil
		}
		if !resp.Success {
			err := fmt.Errorf("%s", resp.Error)
			if resp.Error == "" {
				err = fmt.Errorf("tap_target failed")
			}
			return nil, DeviceTapOutput{Success: false, X: resp.X, Y: resp.Y, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
		}
		if resp.LatencyMs > 0 {
			latency = resp.LatencyMs
		}
		return nil, DeviceTapOutput{
			Success: true, X: resp.X, Y: resp.Y, LatencyMs: latency,
		}, nil
	}

	rc, err := s.resolveCoords(ctx, input.Target, input.X, input.Y, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceTapOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	body := map[string]int{"x": rc.X, "y": rc.Y}
	_, err = s.sessionMgr.WorkerRequestOnSession(ctx, rc.Session, "/tap", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceTapOutput{Success: false, X: rc.X, Y: rc.Y, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceTapOutput{
		Success: true, X: rc.X, Y: rc.Y, LatencyMs: latency,
	}, nil
}

// --- Device Double Tap ---

type DeviceDoubleTapInput struct {
	Target       string `json:"target,omitempty" jsonschema:"Element to double-tap. Use visible text ('Sign In button') or visual traits ('blue rectangle'). Auto-resolves via AI grounding."`
	X            *int   `json:"x,omitempty" jsonschema:"Raw X pixel coordinate (bypasses grounding)"`
	Y            *int   `json:"y,omitempty" jsonschema:"Raw Y pixel coordinate (bypasses grounding)"`
	ScreenToken  string `json:"screen_token,omitempty" jsonschema:"Optional screen token from screenshot()."`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceDoubleTapOutput = DeviceTapOutput

func (s *Server) handleDeviceDoubleTap(ctx context.Context, req *mcp.CallToolRequest, input DeviceDoubleTapInput) (*mcp.CallToolResult, DeviceDoubleTapOutput, error) {
	start := time.Now()
	rc, err := s.resolveCoords(ctx, input.Target, input.X, input.Y, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceDoubleTapOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	body := map[string]int{"x": rc.X, "y": rc.Y}
	_, err = s.sessionMgr.WorkerRequestOnSession(ctx, rc.Session, "/double_tap", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceDoubleTapOutput{Success: false, X: rc.X, Y: rc.Y, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceDoubleTapOutput{
		Success: true, X: rc.X, Y: rc.Y, LatencyMs: latency,
	}, nil
}

// --- Device Long Press ---

type DeviceLongPressInput struct {
	Target       string `json:"target,omitempty" jsonschema:"Element to long-press. Use visible text ('Sign In button') or visual traits ('blue rectangle'). Auto-resolves via AI grounding."`
	X            *int   `json:"x,omitempty" jsonschema:"Raw X pixel coordinate (bypasses grounding)"`
	Y            *int   `json:"y,omitempty" jsonschema:"Raw Y pixel coordinate (bypasses grounding)"`
	DurationMs   int    `json:"duration_ms,omitempty" jsonschema:"Press duration in ms (default 1500)"`
	ScreenToken  string `json:"screen_token,omitempty" jsonschema:"Optional screen token from screenshot()."`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceLongPressOutput = DeviceTapOutput

func (s *Server) handleDeviceLongPress(ctx context.Context, req *mcp.CallToolRequest, input DeviceLongPressInput) (*mcp.CallToolResult, DeviceLongPressOutput, error) {
	start := time.Now()
	rc, err := s.resolveCoords(ctx, input.Target, input.X, input.Y, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceLongPressOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	dur := input.DurationMs
	if dur == 0 {
		dur = 1500
	}
	body := map[string]int{"x": rc.X, "y": rc.Y, "duration_ms": dur}
	_, err = s.sessionMgr.WorkerRequestOnSession(ctx, rc.Session, "/longpress", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceLongPressOutput{Success: false, X: rc.X, Y: rc.Y, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceLongPressOutput{
		Success: true, X: rc.X, Y: rc.Y, LatencyMs: latency,
	}, nil
}

// --- Device Type ---

type DeviceTypeInput struct {
	Target       string `json:"target,omitempty" jsonschema:"Element to type into (e.g. 'email input field')"`
	X            *int   `json:"x,omitempty" jsonschema:"Raw X coordinate"`
	Y            *int   `json:"y,omitempty" jsonschema:"Raw Y coordinate"`
	Text         string `json:"text" jsonschema:"Text to type (REQUIRED)"`
	ClearFirst   bool   `json:"clear_first,omitempty" jsonschema:"Clear field before typing (default true)"`
	ScreenToken  string `json:"screen_token,omitempty" jsonschema:"Optional screen token from screenshot()."`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceTypeOutput = DeviceTapOutput

func (s *Server) handleDeviceType(ctx context.Context, req *mcp.CallToolRequest, input DeviceTypeInput) (*mcp.CallToolResult, DeviceTypeOutput, error) {
	if input.Text == "" {
		return nil, DeviceTypeOutput{Success: false, Error: "text is required -- provide the text to type into the field"}, nil
	}
	start := time.Now()
	rc, err := s.resolveCoords(ctx, input.Target, input.X, input.Y, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceTypeOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	// ClearFirst defaults to true (clear the field before typing).
	clearFirst := true
	if req != nil {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(req.Params.Arguments, &raw); err == nil {
			if _, exists := raw["clear_first"]; exists {
				clearFirst = input.ClearFirst
			}
		}
	}
	body := map[string]interface{}{"x": rc.X, "y": rc.Y, "text": input.Text, "clear_first": clearFirst}
	_, err = s.sessionMgr.WorkerRequestOnSession(ctx, rc.Session, "/input", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceTypeOutput{Success: false, X: rc.X, Y: rc.Y, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceTypeOutput{
		Success: true, X: rc.X, Y: rc.Y, LatencyMs: latency,
	}, nil
}

// --- Device Swipe ---

type DeviceSwipeInput struct {
	Target       string `json:"target,omitempty" jsonschema:"Element to swipe from. Use visible text ('product list') or visual traits ('main content area'). Auto-resolves via AI grounding."`
	X            *int   `json:"x,omitempty" jsonschema:"Raw X pixel coordinate (bypasses grounding)"`
	Y            *int   `json:"y,omitempty" jsonschema:"Raw Y pixel coordinate (bypasses grounding)"`
	Direction    string `json:"direction" jsonschema:"Swipe direction: up, down, left, right. 'up' moves finger up (scrolls content down). REQUIRED."`
	DurationMs   int    `json:"duration_ms,omitempty" jsonschema:"Swipe duration in ms (default 500)"`
	ScreenToken  string `json:"screen_token,omitempty" jsonschema:"Optional screen token from screenshot()."`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceSwipeOutput = DeviceTapOutput

func (s *Server) handleDeviceSwipe(ctx context.Context, req *mcp.CallToolRequest, input DeviceSwipeInput) (*mcp.CallToolResult, DeviceSwipeOutput, error) {
	if input.Direction == "" {
		return nil, DeviceSwipeOutput{Success: false, Error: "direction is required (up, down, left, right)",
			NextSteps: []NextStep{{Tool: "screenshot", Reason: "See the screen and decide swipe direction"}},
		}, nil
	}
	validDirs := map[string]bool{"up": true, "down": true, "left": true, "right": true}
	if !validDirs[strings.ToLower(input.Direction)] {
		return nil, DeviceSwipeOutput{
			Success: false,
			Error:   fmt.Sprintf("invalid direction %q -- must be up, down, left, or right", input.Direction),
		}, nil
	}
	input.Direction = strings.ToLower(input.Direction)
	start := time.Now()
	rc, err := s.resolveCoords(ctx, input.Target, input.X, input.Y, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceSwipeOutput{Success: false, Error: err.Error(),
			NextSteps: errorNextSteps(err),
		}, nil
	}

	dur := input.DurationMs
	if dur == 0 {
		dur = 500
	}
	body := map[string]interface{}{"x": rc.X, "y": rc.Y, "direction": input.Direction, "duration_ms": dur}
	_, err = s.sessionMgr.WorkerRequestOnSession(ctx, rc.Session, "/swipe", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceSwipeOutput{Success: false, X: rc.X, Y: rc.Y, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceSwipeOutput{
		Success: true, X: rc.X, Y: rc.Y, LatencyMs: latency,
	}, nil
}

// --- Device Drag (raw only) ---

type DeviceDragInput struct {
	StartX       int    `json:"start_x" jsonschema:"Starting X coordinate"`
	StartY       int    `json:"start_y" jsonschema:"Starting Y coordinate"`
	EndX         int    `json:"end_x" jsonschema:"Ending X coordinate"`
	EndY         int    `json:"end_y" jsonschema:"Ending Y coordinate"`
	ScreenToken  string `json:"screen_token,omitempty" jsonschema:"Optional screen token from screenshot()."`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceDragOutput struct {
	Success   bool       `json:"success"`
	LatencyMs float64    `json:"latency_ms"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleDeviceDrag(ctx context.Context, req *mcp.CallToolRequest, input DeviceDragInput) (*mcp.CallToolResult, DeviceDragOutput, error) {
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceDragOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)
	start := time.Now()
	body := map[string]int{"start_x": input.StartX, "start_y": input.StartY, "end_x": input.EndX, "end_y": input.EndY}
	_, err = s.sessionMgr.WorkerRequestOnSession(ctx, session, "/drag", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceDragOutput{Success: false, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceDragOutput{
		Success: true, LatencyMs: latency,
	}, nil
}

// --- Device Pinch ---

type DevicePinchInput struct {
	Target       string  `json:"target,omitempty" jsonschema:"Element to pinch/zoom (grounded)"`
	X            *int    `json:"x,omitempty" jsonschema:"Raw X coordinate (bypasses grounding)"`
	Y            *int    `json:"y,omitempty" jsonschema:"Raw Y coordinate (bypasses grounding)"`
	Scale        float64 `json:"scale,omitempty" jsonschema:"Zoom scale (>1 zoom in, <1 zoom out). Default 2.0."`
	DurationMs   int     `json:"duration_ms,omitempty" jsonschema:"Pinch duration in ms (default 300)"`
	ScreenToken  string  `json:"screen_token,omitempty" jsonschema:"Optional screen token from screenshot()."`
	SessionIndex *int    `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string  `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DevicePinchOutput = DeviceTapOutput

func (s *Server) handleDevicePinch(ctx context.Context, req *mcp.CallToolRequest, input DevicePinchInput) (*mcp.CallToolResult, DevicePinchOutput, error) {
	start := time.Now()
	rc, err := s.resolveCoords(ctx, input.Target, input.X, input.Y, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DevicePinchOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	scale := input.Scale
	if scale == 0 {
		scale = 2.0
	}
	durationMs := input.DurationMs
	if durationMs == 0 {
		durationMs = 300
	}
	body := map[string]interface{}{"x": rc.X, "y": rc.Y, "scale": scale, "duration_ms": durationMs}
	_, err = s.sessionMgr.WorkerRequestOnSession(ctx, rc.Session, "/pinch", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DevicePinchOutput{Success: false, X: rc.X, Y: rc.Y, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DevicePinchOutput{
		Success: true, X: rc.X, Y: rc.Y, LatencyMs: latency,
	}, nil
}

// --- Device Clear Text ---

type DeviceClearTextInput struct {
	Target       string `json:"target,omitempty" jsonschema:"Element to clear (grounded)"`
	X            *int   `json:"x,omitempty" jsonschema:"Raw X coordinate (bypasses grounding)"`
	Y            *int   `json:"y,omitempty" jsonschema:"Raw Y coordinate (bypasses grounding)"`
	ScreenToken  string `json:"screen_token,omitempty" jsonschema:"Optional screen token from screenshot()."`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceClearTextOutput = DeviceTapOutput

func (s *Server) handleDeviceClearText(ctx context.Context, req *mcp.CallToolRequest, input DeviceClearTextInput) (*mcp.CallToolResult, DeviceClearTextOutput, error) {
	start := time.Now()
	rc, err := s.resolveCoords(ctx, input.Target, input.X, input.Y, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceClearTextOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	body := map[string]int{"x": rc.X, "y": rc.Y}
	_, err = s.sessionMgr.WorkerRequestOnSession(ctx, rc.Session, "/clear_text", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceClearTextOutput{Success: false, X: rc.X, Y: rc.Y, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceClearTextOutput{
		Success: true, X: rc.X, Y: rc.Y, LatencyMs: latency,
	}, nil
}

// --- Device Wait ---

type DeviceWaitInput struct {
	DurationMs   int    `json:"duration_ms,omitempty" jsonschema:"Wait duration in milliseconds (default 1000)"`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceWaitOutput struct {
	Success    bool       `json:"success"`
	DurationMs int        `json:"duration_ms"`
	LatencyMs  float64    `json:"latency_ms"`
	Error      string     `json:"error,omitempty"`
	NextSteps  []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleDeviceWait(ctx context.Context, req *mcp.CallToolRequest, input DeviceWaitInput) (*mcp.CallToolResult, DeviceWaitOutput, error) {
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceWaitOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)

	durationMs := input.DurationMs
	if durationMs == 0 {
		durationMs = 1000
	}
	if durationMs < 0 {
		return nil, DeviceWaitOutput{Success: false, Error: "duration_ms must be >= 0"}, nil
	}

	start := time.Now()
	body := map[string]int{"duration_ms": durationMs}
	_, err = s.sessionMgr.WorkerRequestOnSession(ctx, session, "/wait", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceWaitOutput{Success: false, DurationMs: durationMs, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceWaitOutput{
		Success:    true,
		DurationMs: durationMs,
		LatencyMs:  latency,
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "Check the screen after waiting"},
		},
	}, nil
}

// --- Device Back ---

type DeviceBackInput struct {
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceBackOutput struct {
	Success   bool       `json:"success"`
	LatencyMs float64    `json:"latency_ms"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleDeviceBack(ctx context.Context, req *mcp.CallToolRequest, input DeviceBackInput) (*mcp.CallToolResult, DeviceBackOutput, error) {
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceBackOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)

	start := time.Now()
	_, err = s.sessionMgr.WorkerRequestOnSession(ctx, session, "/back", nil)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceBackOutput{Success: false, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceBackOutput{
		Success:   true,
		LatencyMs: latency,
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "Verify navigation after back action"},
		},
	}, nil
}

// --- Device Key ---

type DeviceKeyInput struct {
	Key          string `json:"key" jsonschema:"Key to send: ENTER or BACKSPACE (REQUIRED)"`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceKeyOutput struct {
	Success   bool       `json:"success"`
	Key       string     `json:"key,omitempty"`
	LatencyMs float64    `json:"latency_ms"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleDeviceKey(ctx context.Context, req *mcp.CallToolRequest, input DeviceKeyInput) (*mcp.CallToolResult, DeviceKeyOutput, error) {
	if input.Key == "" {
		return nil, DeviceKeyOutput{Success: false, Error: "key is required (ENTER or BACKSPACE)"}, nil
	}
	normalized := strings.ToUpper(strings.TrimSpace(input.Key))
	switch normalized {
	case "RETURN":
		normalized = "ENTER"
	case "DELETE":
		normalized = "BACKSPACE"
	}
	if normalized != "ENTER" && normalized != "BACKSPACE" {
		return nil, DeviceKeyOutput{Success: false, Error: "key must be ENTER or BACKSPACE"}, nil
	}

	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceKeyOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)

	start := time.Now()
	body := map[string]string{"key": normalized}
	_, err = s.sessionMgr.WorkerRequestOnSession(ctx, session, "/key", body)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceKeyOutput{Success: false, Key: normalized, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceKeyOutput{
		Success:   true,
		Key:       normalized,
		LatencyMs: latency,
	}, nil
}

// --- Device Shake ---

type DeviceShakeInput struct {
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceShakeOutput struct {
	Success   bool       `json:"success"`
	LatencyMs float64    `json:"latency_ms"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleDeviceShake(ctx context.Context, req *mcp.CallToolRequest, input DeviceShakeInput) (*mcp.CallToolResult, DeviceShakeOutput, error) {
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceShakeOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)

	start := time.Now()
	_, err = s.sessionMgr.WorkerRequestOnSession(ctx, session, "/shake", nil)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, DeviceShakeOutput{Success: false, LatencyMs: latency, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceShakeOutput{
		Success:   true,
		LatencyMs: latency,
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "Verify shake-driven UI changes"},
		},
	}, nil
}

// --- Screenshot ---

type ScreenshotInput struct {
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to screenshot. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type ScreenshotOutput struct {
	Success     bool       `json:"success"`
	LatencyMs   float64    `json:"latency_ms"`
	ScreenToken string     `json:"screen_token,omitempty"`
	ImagePath   string     `json:"image_path,omitempty"`
	ErrorCode   string     `json:"error_code,omitempty"`
	Error       string     `json:"error,omitempty"`
	NextSteps   []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleScreenshot(ctx context.Context, req *mcp.CallToolRequest, input ScreenshotInput) (*mcp.CallToolResult, ScreenshotOutput, error) {
	if failure := s.refreshDevAuthentication(); failure != nil {
		return authenticationGateResult(failure), ScreenshotOutput{
			Success:   false,
			ErrorCode: string(failure.Code),
			Error:     failure.Message,
		}, nil
	}
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, ScreenshotOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)
	start := time.Now()
	imgBytes, err := s.sessionMgr.ScreenshotOnSession(ctx, session)
	latency := float64(time.Since(start).Milliseconds())
	if err != nil {
		return nil, ScreenshotOutput{Success: false, LatencyMs: latency, Error: err.Error(),
			NextSteps: errorNextSteps(err),
		}, nil
	}

	result := nativeImageResult(imgBytes)
	if session.Index == UnattachedSessionIndex {
		return result, ScreenshotOutput{Success: true, LatencyMs: latency}, nil
	}
	screenToken := s.sessionMgr.MarkScreenshotAnchorWithImage(session.Index, imgBytes)
	imagePath, err := s.sessionMgr.PersistAnchorImage(session.Index, screenToken, imgBytes)
	if err != nil {
		return nil, ScreenshotOutput{Success: false, LatencyMs: latency, ScreenToken: screenToken, Error: fmt.Sprintf("failed to persist screenshot anchor: %v", err)}, nil
	}
	return result, ScreenshotOutput{
		Success: true, LatencyMs: latency, ScreenToken: screenToken, ImagePath: imagePath,
	}, nil
}

// nativeImageResult returns one image-only MCP chat result.
func nativeImageResult(imageBytes []byte) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.ImageContent{Data: imageBytes, MIMEType: "image/png"},
		},
	}
}

// --- Install App ---

type InstallAppInput struct {
	AppURL         string `json:"app_url,omitempty" jsonschema:"URL to download app from (.apk or .ipa). Provide this OR build_version_id."`
	BuildVersionID string `json:"build_version_id,omitempty" jsonschema:"Build version ID from a previous upload_build. The download URL is resolved automatically. Provide this OR app_url."`
	BundleID       string `json:"bundle_id,omitempty" jsonschema:"Bundle ID (auto-detected if omitted)"`
	SessionIndex   *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID      string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type InstallAppOutput struct {
	Success   bool       `json:"success"`
	BundleID  string     `json:"bundle_id,omitempty"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleInstallApp(ctx context.Context, req *mcp.CallToolRequest, input InstallAppInput) (*mcp.CallToolResult, InstallAppOutput, error) {
	appURL := normalizeOptionalToolInput(input.AppURL)
	buildVersionID := normalizeOptionalToolInput(input.BuildVersionID)
	bundleID := normalizeOptionalToolInput(input.BundleID)
	if appURL != "" && buildVersionID != "" {
		return nil, InstallAppOutput{Success: false, Error: "provide only one of app_url or build_version_id"}, nil
	}

	// Resolve build_version_id to a download URL if provided
	if appURL == "" && buildVersionID != "" {
		detail, err := s.apiClient.GetBuildVersionDownloadURL(ctx, buildVersionID)
		if err != nil {
			return nil, InstallAppOutput{
				Success: false,
				Error:   fmt.Sprintf("failed to resolve build version %s: %v", buildVersionID, err),
				NextSteps: []NextStep{
					{Tool: "list_builds", Reason: "List available builds to find a valid version ID"},
				},
			}, nil
		}
		appURL = strings.TrimSpace(detail.DownloadURL)
		if appURL == "" {
			return nil, InstallAppOutput{
				Success: false,
				Error:   fmt.Sprintf("build version %s has no download URL", buildVersionID),
				NextSteps: []NextStep{
					{Tool: "list_builds", Reason: "Choose a build version that has a downloadable artifact"},
				},
			}, nil
		}
		// Use the package_name from the build as bundle_id hint if not explicitly provided
		if bundleID == "" && detail.PackageName != "" {
			bundleID = strings.TrimSpace(detail.PackageName)
		}
	}

	if appURL == "" {
		return nil, InstallAppOutput{Success: false, Error: "either app_url or build_version_id is required -- provide a URL to an .apk/.ipa file, or the ID of a previously uploaded build"}, nil
	}

	if buildVersionID == "" {
		if validated, vErr := validateExternalURL(appURL); vErr != nil {
			return nil, InstallAppOutput{Success: false, Error: fmt.Sprintf("rejected app_url: %v", vErr)}, nil
		} else {
			appURL = validated
		}
	}

	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, InstallAppOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)

	body := map[string]string{"app_url": appURL}
	if bundleID != "" {
		body["bundle_id"] = bundleID
	}
	respBody, err := s.sessionMgr.WorkerRequestOnSession(ctx, session, "/install", body)
	if err != nil {
		return nil, InstallAppOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	var resp struct {
		Success     bool   `json:"Success"`
		BundleID    string `json:"bundle_id"`
		PackageName string `json:"package_name"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		truncated := string(respBody)
		if len(truncated) > 200 {
			truncated = truncated[:200] + "..."
		}
		return nil, InstallAppOutput{
			Success:   false,
			Error:     fmt.Sprintf("failed to parse worker response: %v (body: %s)", err, truncated),
			NextSteps: errorNextSteps(err),
		}, nil
	}

	// Resolve bundle ID from response, input, or build metadata (in priority order)
	detectedBundleID := resp.BundleID
	if detectedBundleID == "" {
		detectedBundleID = resp.PackageName
	}
	if detectedBundleID == "" {
		detectedBundleID = bundleID
	}

	output := InstallAppOutput{Success: resp.Success, BundleID: detectedBundleID}
	if resp.Success {
		launchReason := "Launch the installed app"
		if detectedBundleID != "" {
			launchReason = fmt.Sprintf("Launch the installed app (bundle_id=%q)", detectedBundleID)
		}
		output.NextSteps = []NextStep{
			{Tool: "launch_app", Reason: launchReason},
			{Tool: "screenshot", Reason: "See the device screen"},
		}
	} else {
		output.Error = "install reported failure"
		output.NextSteps = []NextStep{
			{Tool: "screenshot", Reason: "See the device screen for errors"},
		}
	}
	return nil, output, nil
}

// --- Launch App ---

type LaunchAppInput struct {
	BundleID     string `json:"bundle_id,omitempty" jsonschema:"App bundle ID to launch. Omit to launch the app this session installed — prefer omitting it over guessing an ID."`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type LaunchAppOutput struct {
	Success   bool       `json:"success"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

// handleLaunchApp launches an app on the session's device.
//
// bundle_id is optional on purpose. A caller attached to an existing session
// has no reliable way to know which bundle that session installed, so
// requiring one only produces invented IDs that fail against a healthy
// device. Omitted, the worker launches the app it installed; supplied, the
// worker rejects it immediately when the device says it is not installed.
func (s *Server) handleLaunchApp(ctx context.Context, req *mcp.CallToolRequest, input LaunchAppInput) (*mcp.CallToolResult, LaunchAppOutput, error) {
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, LaunchAppOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)

	body := map[string]string{}
	if bundleID := strings.TrimSpace(input.BundleID); bundleID != "" {
		body["bundle_id"] = bundleID
	}
	respBody, err := s.sessionMgr.WorkerRequestOnSession(ctx, session, "/launch", body)
	if err != nil {
		return nil, LaunchAppOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	// The worker reports a failed launch as HTTP 200 with success=false, so the
	// transport error alone is not enough: without this an agent that supplied a
	// bundle id the device does not have is told the launch succeeded and never
	// sees the installed-apps rejection.
	if err := EnsureWorkerActionSucceeded(respBody, "launch"); err != nil {
		return nil, LaunchAppOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, LaunchAppOutput{
		Success: true,
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "See the launched app"},
		},
	}, nil
}

// --- Get Session Info ---

type GetSessionInfoInput struct {
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to query. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type GetSessionInfoOutput struct {
	Active    bool   `json:"active"`
	SessionID string `json:"session_id,omitempty"`
	// SessionIndex is omitted when this server does not track the session,
	// because no local index selects it.
	SessionIndex  *int    `json:"session_index,omitempty"`
	Platform      string  `json:"platform,omitempty"`
	ViewerURL     string  `json:"viewer_url,omitempty"`
	WhepURL       string  `json:"whep_url,omitempty"`
	UptimeSeconds float64 `json:"uptime_seconds,omitempty"`
	IdleSeconds   float64 `json:"idle_seconds,omitempty"`
	// IdleTimeoutSeconds is the auto-stop threshold; the session ends when
	// IdleSeconds reaches it. Any tool call resets the idle timer.
	IdleTimeoutSeconds float64    `json:"idle_timeout_seconds,omitempty"`
	LastActivityAt     string     `json:"last_activity_at,omitempty"`
	TotalSessions      int        `json:"total_sessions"`
	Error              string     `json:"error,omitempty"`
	NextSteps          []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleGetSessionInfo(ctx context.Context, req *mcp.CallToolRequest, input GetSessionInfoInput) (*mcp.CallToolResult, GetSessionInfoOutput, error) {
	s.syncSessionsBestEffort(ctx)
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, GetSessionInfoOutput{
			Active:        false,
			TotalSessions: s.sessionMgr.SessionCount(),
			Error:         err.Error(),
			NextSteps:     errorNextSteps(err),
		}, nil
	}

	var sessionIndex *int
	if session.Index != UnattachedSessionIndex {
		sessionIndex = &session.Index
	}
	now := time.Now()
	return nil, GetSessionInfoOutput{
		Active:             true,
		SessionID:          session.SessionID,
		SessionIndex:       sessionIndex,
		Platform:           session.Platform,
		ViewerURL:          session.ViewerURL,
		WhepURL:            stringValue(session.WhepURL),
		UptimeSeconds:      now.Sub(session.StartedAt).Seconds(),
		IdleSeconds:        now.Sub(session.LastActivity).Seconds(),
		IdleTimeoutSeconds: session.IdleTimeout.Seconds(),
		LastActivityAt:     session.LastActivity.UTC().Format(time.RFC3339),
		TotalSessions:      s.sessionMgr.SessionCount(),
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "Re-anchor on the current screen before taking action"},
		},
	}, nil
}

// --- Device Doctor ---

type DeviceDoctorInput struct {
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to diagnose. Omit to diagnose the active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID to diagnose. Wins over session_index; omit both to diagnose the active session."`
}

type DiagnosticCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

type DeviceDoctorOutput struct {
	Checks           []DiagnosticCheck `json:"checks"`
	AllPassed        bool              `json:"all_passed"`
	TroubleshootTips []string          `json:"troubleshoot_tips,omitempty"`
	Error            string            `json:"error,omitempty"`
	NextSteps        []NextStep        `json:"next_steps,omitempty"`
}

func (s *Server) handleDeviceDoctor(ctx context.Context, req *mcp.CallToolRequest, input DeviceDoctorInput) (*mcp.CallToolResult, DeviceDoctorOutput, error) {
	var checks []DiagnosticCheck
	allPassed := true

	// Check 1: Auth
	_, err := s.apiClient.ValidateAPIKey(ctx)
	if err != nil {
		checks = append(checks, DiagnosticCheck{Name: "auth", Status: "fail", Detail: err.Error(), Fix: "Set REVYL_API_KEY or run 'revyl auth login'"})
		allPassed = false
	} else {
		checks = append(checks, DiagnosticCheck{Name: "auth", Status: "pass"})
	}

	// Check 2: The requested session, or the active one
	session := s.sessionMgr.GetActive()
	var sessionErr error
	if input.SessionIndex != nil || strings.TrimSpace(input.SessionID) != "" {
		session, sessionErr = s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	}
	switch {
	case sessionErr != nil:
		checks = append(checks, DiagnosticCheck{Name: "session", Status: "fail", Detail: sessionErr.Error(), Fix: "Call list_device_sessions() to see live sessions and their IDs"})
		allPassed = false
	case session == nil:
		checks = append(checks, DiagnosticCheck{Name: "session", Status: "none", Detail: "No active session", Fix: "Call start_device_session(platform='android')"})
	default:
		checks = append(checks, DiagnosticCheck{Name: "session", Status: "pass", Detail: fmt.Sprintf("platform=%s, uptime=%.0fs", session.Platform, time.Since(session.StartedAt).Seconds())})

		// Check 3: Worker reachability (only if session exists)
		respBytes, werr := s.sessionMgr.WorkerRequestOnSession(ctx, session, "/health", nil)
		if werr != nil {
			checks = append(checks, DiagnosticCheck{Name: "worker", Status: "fail", Detail: werr.Error(), Fix: "stop_device_session() and start a new one"})
			allPassed = false
		} else {
			checks = append(checks, DiagnosticCheck{Name: "worker", Status: "pass"})

			// Check 3b: Device connectivity (parse /health response body)
			var health struct {
				DeviceConnected bool `json:"device_connected"`
			}
			if json.Unmarshal(respBytes, &health) == nil {
				if health.DeviceConnected {
					checks = append(checks, DiagnosticCheck{Name: "device", Status: "pass"})
				} else {
					checks = append(checks, DiagnosticCheck{Name: "device", Status: "fail", Detail: "Worker is running but device is not connected", Fix: "stop_device_session() and start a new one"})
					allPassed = false
				}
			}
		}
	}

	// Check 4: CLI version
	checks = append(checks, DiagnosticCheck{Name: "cli_version", Status: "info", Detail: s.version})
	checks = append(checks, DiagnosticCheck{Name: "mcp_dev_mode", Status: "info", Detail: fmt.Sprintf("%t", s.devMode)})
	checks = append(checks, DiagnosticCheck{Name: "mcp_backend_url", Status: "info", Detail: config.GetBackendURL(s.devMode)})
	if s.workDir != "" {
		checks = append(checks, DiagnosticCheck{Name: "mcp_workdir", Status: "info", Detail: s.workDir})
	}
	if exePath, exeErr := os.Executable(); exeErr == nil && exePath != "" {
		checks = append(checks, DiagnosticCheck{Name: "mcp_binary", Status: "info", Detail: exePath})
	}

	// Check 5: Session persistence
	persistPath := ""
	if s.sessionMgr.WorkDir() != "" {
		persistPath = s.sessionMgr.WorkDir() + "/.revyl/device-sessions.json"
		if _, fErr := os.Stat(persistPath); fErr == nil {
			checks = append(checks, DiagnosticCheck{Name: "persist_file", Status: "pass", Detail: persistPath})
		} else {
			checks = append(checks, DiagnosticCheck{Name: "persist_file", Status: "none", Detail: "No persisted session file"})
		}
	}

	// Check 7: Environment
	apiKeyMasked := maskEnv("REVYL_API_KEY")
	checks = append(checks, DiagnosticCheck{Name: "env_api_key", Status: "info", Detail: apiKeyMasked})
	checks = append(checks, DiagnosticCheck{Name: "env_local", Status: "info", Detail: envOrDefault("LOCAL", "false")})

	// Troubleshooting tips
	tips := []string{
		"If worker is unreachable, stop and start a new session.",
		"If grounding fails, try a more specific target description.",
		"Sessions auto-stop after the idle timeout (default 15 min). Use get_session_info() to check idle_seconds vs idle_timeout_seconds.",
		"Use screenshot() before every action to see the current screen state.",
	}

	output := DeviceDoctorOutput{Checks: checks, AllPassed: allPassed, TroubleshootTips: tips}
	if sessionErr != nil {
		output.Error = sessionErr.Error()
		output.NextSteps = errorNextSteps(sessionErr)
		return &mcp.CallToolResult{IsError: true}, output, nil
	}
	if allPassed {
		output.NextSteps = []NextStep{
			{Tool: "screenshot", Reason: "Everything looks good -- see the device screen"},
		}
	} else {
		output.NextSteps = []NextStep{
			{Tool: "device_doctor", Reason: "Re-run diagnostics after fixing issues"},
		}
	}
	return nil, output, nil
}

// maskEnv returns a masked version of an environment variable value.
func maskEnv(key string) string {
	val := os.Getenv(key)
	if val == "" {
		return "(not set)"
	}
	return "(set)"
}

// envOrDefault returns an environment variable value or the provided default.
func envOrDefault(key, def string) string {
	val := os.Getenv(key)
	if val == "" {
		return def
	}
	return val
}

// --- List Device Sessions ---

// ListDeviceSessionsInput defines input for list_device_sessions.
type ListDeviceSessionsInput struct{}

// ListDeviceSessionsSessionItem represents a single session in the list output.
type ListDeviceSessionsSessionItem struct {
	SessionID          string  `json:"session_id"`
	Index              int     `json:"index"`
	Platform           string  `json:"platform"`
	Status             string  `json:"status"`
	Uptime             float64 `json:"uptime_seconds"`
	IdleSeconds        float64 `json:"idle_seconds,omitempty"`
	IdleTimeoutSeconds float64 `json:"idle_timeout_seconds,omitempty"`
	Active             bool    `json:"active"`
	WhepURL            string  `json:"whep_url,omitempty"`
}

// ListDeviceSessionsOutput defines output for list_device_sessions.
type ListDeviceSessionsOutput struct {
	Sessions    []ListDeviceSessionsSessionItem `json:"sessions"`
	ActiveIndex int                             `json:"active_index"`
	NextSteps   []NextStep                      `json:"next_steps,omitempty"`
}

func (s *Server) handleListDeviceSessions(ctx context.Context, req *mcp.CallToolRequest, input ListDeviceSessionsInput) (*mcp.CallToolResult, ListDeviceSessionsOutput, error) {
	s.syncSessionsBestEffort(ctx)
	sessions := s.sessionMgr.ListSessions()
	activeIdx := s.sessionMgr.ActiveIndex()

	items := make([]ListDeviceSessionsSessionItem, 0, len(sessions))
	for _, sess := range sessions {
		items = append(items, ListDeviceSessionsSessionItem{
			SessionID:          sess.SessionID,
			Index:              sess.Index,
			Platform:           sess.Platform,
			Status:             "running",
			Uptime:             time.Since(sess.StartedAt).Seconds(),
			IdleSeconds:        time.Since(sess.LastActivity).Seconds(),
			IdleTimeoutSeconds: sess.IdleTimeout.Seconds(),
			Active:             sess.Index == activeIdx,
			WhepURL:            stringValue(sess.WhepURL),
		})
	}

	output := ListDeviceSessionsOutput{
		Sessions:    items,
		ActiveIndex: activeIdx,
	}

	if len(sessions) == 0 {
		output.NextSteps = []NextStep{
			{Tool: "start_device_session", Params: "platform=\"android\"", Reason: "No sessions -- start one"},
		}
	} else {
		output.NextSteps = []NextStep{
			{Tool: "screenshot", Reason: "See the active session's screen"},
		}
	}

	return nil, output, nil
}

// --- Switch Device Session ---

// SwitchDeviceSessionInput defines input for switch_device_session.
type SwitchDeviceSessionInput struct {
	Index int `json:"index" jsonschema:"Session index to switch to (REQUIRED)"`
}

// SwitchDeviceSessionOutput defines output for switch_device_session.
type SwitchDeviceSessionOutput struct {
	Success   bool       `json:"success"`
	Index     int        `json:"index"`
	Platform  string     `json:"platform,omitempty"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleSwitchDeviceSession(ctx context.Context, req *mcp.CallToolRequest, input SwitchDeviceSessionInput) (*mcp.CallToolResult, SwitchDeviceSessionOutput, error) {
	if err := s.sessionMgr.SetActive(input.Index); err != nil {
		return nil, SwitchDeviceSessionOutput{Success: false, Error: err.Error()}, nil
	}

	session := s.sessionMgr.GetSession(input.Index)
	platform := ""
	if session != nil {
		platform = session.Platform
		s.sessionMgr.ClearScreenshotAnchor(session.Index)
	}

	return nil, SwitchDeviceSessionOutput{
		Success:  true,
		Index:    input.Index,
		Platform: platform,
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "See the newly active session's screen"},
		},
	}, nil
}

// --- Device Go Home ---

type DeviceGoHomeInput struct {
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceGoHomeOutput struct {
	Success   bool       `json:"success"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleDeviceGoHome(ctx context.Context, req *mcp.CallToolRequest, input DeviceGoHomeInput) (*mcp.CallToolResult, DeviceGoHomeOutput, error) {
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceGoHomeOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)

	_, err = s.sessionMgr.WorkerRequestOnSession(ctx, session, "/go_home", nil)
	if err != nil {
		return nil, DeviceGoHomeOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceGoHomeOutput{
		Success: true,
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "See the home screen"},
		},
	}, nil
}

// --- Device Kill App ---

type DeviceKillAppInput struct {
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceKillAppOutput struct {
	Success   bool       `json:"success"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleDeviceKillApp(ctx context.Context, req *mcp.CallToolRequest, input DeviceKillAppInput) (*mcp.CallToolResult, DeviceKillAppOutput, error) {
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceKillAppOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)

	_, err = s.sessionMgr.WorkerRequestOnSession(ctx, session, "/kill_app", nil)
	if err != nil {
		return nil, DeviceKillAppOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceKillAppOutput{
		Success: true,
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "See the device screen after killing the app"},
		},
	}, nil
}

// --- Device Open App ---

type DeviceOpenAppInput struct {
	App          string `json:"app" jsonschema:"System app name (e.g. 'settings', 'safari', 'chrome') or raw bundle ID (REQUIRED)"`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceOpenAppOutput struct {
	Success   bool       `json:"success"`
	App       string     `json:"app,omitempty"`
	BundleID  string     `json:"bundle_id,omitempty"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleDeviceOpenApp(ctx context.Context, req *mcp.CallToolRequest, input DeviceOpenAppInput) (*mcp.CallToolResult, DeviceOpenAppOutput, error) {
	if input.App == "" {
		return nil, DeviceOpenAppOutput{Success: false, Error: "app is required (e.g. 'settings', 'safari', or a raw bundle ID)"}, nil
	}
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceOpenAppOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)

	bundleID := ResolveSystemApp(session.Platform, input.App)
	body := map[string]string{"bundle_id": bundleID}
	respBody, err := s.sessionMgr.WorkerRequestOnSession(ctx, session, "/launch", body)
	if err != nil {
		return nil, DeviceOpenAppOutput{Success: false, App: input.App, BundleID: bundleID, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	// Same /launch route, same success=false-on-HTTP-200 envelope: an alias that
	// resolves to a bundle the device lacks must not report success.
	if err := EnsureWorkerActionSucceeded(respBody, "launch"); err != nil {
		return nil, DeviceOpenAppOutput{Success: false, App: input.App, BundleID: bundleID, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceOpenAppOutput{
		Success:  true,
		App:      input.App,
		BundleID: bundleID,
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "See the opened app"},
		},
	}, nil
}

// --- Device Navigate ---

type DeviceNavigateInput struct {
	URL          string `json:"url" jsonschema:"URL or deep link to open on the device (REQUIRED)"`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceNavigateOutput struct {
	Success   bool       `json:"success"`
	URL       string     `json:"url,omitempty"`
	ErrorCode string     `json:"error_code,omitempty"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleDeviceNavigate(ctx context.Context, req *mcp.CallToolRequest, input DeviceNavigateInput) (*mcp.CallToolResult, DeviceNavigateOutput, error) {
	if failure := s.refreshDevAuthentication(); failure != nil {
		return authenticationGateResult(failure), DeviceNavigateOutput{
			Success:   false,
			ErrorCode: string(failure.Code),
			Error:     failure.Message,
		}, nil
	}
	if input.URL == "" {
		return nil, DeviceNavigateOutput{Success: false, Error: "url is required"}, nil
	}
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceNavigateOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)

	body := map[string]string{"url": input.URL}
	respBody, err := s.sessionMgr.WorkerRequestOnSession(ctx, session, "/open_url", body)
	if err != nil {
		return nil, DeviceNavigateOutput{Success: false, URL: input.URL, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	if err := EnsureWorkerActionSucceeded(respBody, "open_url"); err != nil {
		return nil, DeviceNavigateOutput{Success: false, URL: input.URL, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ClearScreenshotAnchor(session.Index)

	return nil, DeviceNavigateOutput{
		Success: true,
		URL:     input.URL,
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "See what loaded"},
		},
	}, nil
}

// --- Device Set Location ---

type DeviceSetLocationInput struct {
	Latitude     float64 `json:"latitude" jsonschema:"Latitude (-90 to 90, REQUIRED)"`
	Longitude    float64 `json:"longitude" jsonschema:"Longitude (-180 to 180, REQUIRED)"`
	SessionIndex *int    `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string  `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceSetLocationOutput struct {
	Success   bool       `json:"success"`
	Latitude  float64    `json:"latitude,omitempty"`
	Longitude float64    `json:"longitude,omitempty"`
	Error     string     `json:"error,omitempty"`
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleDeviceSetLocation(ctx context.Context, req *mcp.CallToolRequest, input DeviceSetLocationInput) (*mcp.CallToolResult, DeviceSetLocationOutput, error) {
	if input.Latitude < -90 || input.Latitude > 90 {
		return nil, DeviceSetLocationOutput{Success: false, Error: fmt.Sprintf("latitude must be between -90 and 90, got %f", input.Latitude)}, nil
	}
	if input.Longitude < -180 || input.Longitude > 180 {
		return nil, DeviceSetLocationOutput{Success: false, Error: fmt.Sprintf("longitude must be between -180 and 180, got %f", input.Longitude)}, nil
	}
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceSetLocationOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)

	body := map[string]float64{"latitude": input.Latitude, "longitude": input.Longitude}
	_, err = s.sessionMgr.WorkerRequestOnSession(ctx, session, "/set_location", body)
	if err != nil {
		return nil, DeviceSetLocationOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceSetLocationOutput{
		Success:   true,
		Latitude:  input.Latitude,
		Longitude: input.Longitude,
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "Verify the location change"},
		},
	}, nil
}

// --- Device Download File ---

type DeviceDownloadFileInput struct {
	URL          string `json:"url" jsonschema:"URL to download file from (REQUIRED)"`
	Filename     string `json:"filename,omitempty" jsonschema:"Optional destination filename on the device."`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceDownloadFileOutput struct {
	Success    bool       `json:"success"`
	URL        string     `json:"url,omitempty"`
	Filename   string     `json:"filename,omitempty"`
	DevicePath string     `json:"device_path,omitempty"`
	Error      string     `json:"error,omitempty"`
	NextSteps  []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handleDeviceDownloadFile(ctx context.Context, req *mcp.CallToolRequest, input DeviceDownloadFileInput) (*mcp.CallToolResult, DeviceDownloadFileOutput, error) {
	rawURL, err := normalizeRequiredToolInput(input.URL, "url")
	if err != nil {
		return nil, DeviceDownloadFileOutput{Success: false, Error: err.Error()}, nil
	}
	url, err := validateExternalURL(rawURL)
	if err != nil {
		return nil, DeviceDownloadFileOutput{Success: false, Error: fmt.Sprintf("rejected url: %v", err)}, nil
	}
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceDownloadFileOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)

	response, err := s.sessionMgr.DownloadFileOnSession(
		ctx,
		session,
		DeviceDownloadFileRequest{
			URL:      url,
			Filename: normalizeOptionalToolInput(input.Filename),
		},
	)
	if err != nil {
		return nil, DeviceDownloadFileOutput{Success: false, URL: url, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}

	return nil, DeviceDownloadFileOutput{
		Success:    response.Success,
		URL:        url,
		Filename:   normalizeOptionalToolInput(input.Filename),
		DevicePath: response.DevicePath,
		Error:      response.Error,
		NextSteps: []NextStep{
			{Tool: "screenshot", Reason: "Verify the file was downloaded"},
		},
	}, nil
}

// ---------------------------------------------------------------------------
// get_session_report
// ---------------------------------------------------------------------------

type GetSessionReportInput struct {
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index (omit for active session)"`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type GetSessionReportOutput struct {
	Success       bool                            `json:"success"`
	SessionID     string                          `json:"session_id,omitempty"`
	ReportURL     string                          `json:"report_url,omitempty"`
	SessionStatus string                          `json:"session_status,omitempty"`
	Platform      string                          `json:"platform,omitempty"`
	DeviceModel   string                          `json:"device_model,omitempty"`
	TotalSteps    int                             `json:"total_steps"`
	PassedSteps   int                             `json:"passed_steps"`
	FailedSteps   int                             `json:"failed_steps"`
	VideoURL      string                          `json:"video_url,omitempty"`
	Steps         []api.ReportContextStepResponse `json:"steps,omitempty"`
	Error         string                          `json:"error,omitempty"`
	NextSteps     []NextStep                      `json:"next_steps,omitempty"`
}

func (s *Server) handleGetSessionReport(ctx context.Context, req *mcp.CallToolRequest, input GetSessionReportInput) (*mcp.CallToolResult, GetSessionReportOutput, error) {
	// A report is a backend read, so a session_id alone skips session
	// resolution, as the CLI's device report --session-id does, and also
	// works for sessions that have already ended.
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID != "" && input.SessionIndex == nil {
		s.recordSessionTargetMode(ctx, nil, sessionID)
	} else {
		session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
		if err != nil {
			return nil, GetSessionReportOutput{Success: false, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
		}
		sessionID = session.SessionID
	}

	envelope, err := s.apiClient.GetReportBySession(ctx, sessionID, true, true, false)
	if err != nil {
		return nil, GetSessionReportOutput{
			Success:   false,
			SessionID: sessionID,
			Error:     fmt.Sprintf("No report available: %v", err),
			NextSteps: []NextStep{
				{Tool: "screenshot", Reason: "Session may still be active - take a screenshot to verify"},
			},
		}, nil
	}
	r := envelope.Report
	out := GetSessionReportOutput{
		Success:   true,
		SessionID: sessionID,
	}
	if r.ReportUrl != nil {
		out.ReportURL = *r.ReportUrl
	}
	if r.SessionStatus != nil {
		out.SessionStatus = *r.SessionStatus
	}
	if r.Platform != nil {
		out.Platform = *r.Platform
	}
	if r.DeviceModel != nil {
		out.DeviceModel = *r.DeviceModel
	}
	if r.TotalSteps != nil {
		out.TotalSteps = *r.TotalSteps
	}
	if r.PassedSteps != nil {
		out.PassedSteps = *r.PassedSteps
	}
	if r.FailedSteps != nil {
		out.FailedSteps = *r.FailedSteps
	}
	if r.VideoUrl != nil {
		out.VideoURL = *r.VideoUrl
	}
	if r.Steps != nil {
		out.Steps = *r.Steps
	}
	return nil, out, nil
}

// ---------------------------------------------------------------------------
// poll_performance_metrics tool
// ---------------------------------------------------------------------------

// PollPerformanceMetricsInput defines parameters for the poll_performance_metrics MCP tool.
type PollPerformanceMetricsInput struct {
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to query. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
	Cursor       string `json:"cursor,omitempty" jsonschema:"Opaque cursor from a previous response. Use '0' for the first call."`
	Limit        int    `json:"limit,omitempty" jsonschema:"Maximum number of samples to return. Default 100."`
}

// PollPerformanceMetricsOutput wraps the PerfPollResponse for MCP consumers.
type PollPerformanceMetricsOutput struct {
	PerfPollResponse
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handlePollPerformanceMetrics(ctx context.Context, req *mcp.CallToolRequest, input PollPerformanceMetricsInput) (*mcp.CallToolResult, PollPerformanceMetricsOutput, error) {
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, PollPerformanceMetricsOutput{
			PerfPollResponse: PerfPollResponse{Success: false},
			NextSteps: []NextStep{
				{Tool: "start_device_session", Reason: "No active session to poll metrics from"},
			},
		}, nil
	}

	cursor := input.Cursor
	if cursor == "" {
		cursor = "0"
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 100
	}

	resp, pollErr := s.sessionMgr.PollPerformanceMetricsOnSession(ctx, session, cursor, limit)
	if pollErr != nil {
		return nil, PollPerformanceMetricsOutput{
			PerfPollResponse: PerfPollResponse{
				Success:    false,
				Platform:   session.Platform,
				NextCursor: cursor,
			},
		}, fmt.Errorf("poll failed: %w", pollErr)
	}

	output := PollPerformanceMetricsOutput{
		PerfPollResponse: *resp,
	}
	if len(resp.Items) > 0 {
		output.NextSteps = []NextStep{
			{Tool: "poll_performance_metrics", Params: fmt.Sprintf("cursor=\"%s\"", resp.NextCursor), Reason: "Continue polling for new samples"},
		}
	}
	return nil, output, nil
}

// --- Device State Inspector (Phase 6) ---
//
// These types are shared between the MCP handlers below and the
// `revyl device state ...` CLI subcommands in cmd/revyl/device_state.go,
// so adding a field once threads through both surfaces.

// DeviceStateSessionInput is the bare session selector. All device-state
// tools support an optional session_index.
type DeviceStateSessionInput struct {
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

// DeviceStateListInput defines input for device_state_list.
type DeviceStateListInput = DeviceStateSessionInput

// DeviceStateListOutput mirrors the worker's DeviceStateListResponse.
type DeviceStateListOutput struct {
	Success      bool                     `json:"success"`
	Platform     string                   `json:"platform,omitempty"`
	UserDefaults []map[string]interface{} `json:"userdefaults,omitempty"`
	SQLite       []map[string]interface{} `json:"sqlite,omitempty"`
	Errors       []string                 `json:"errors,omitempty"`
	Error        string                   `json:"error,omitempty"`
}

func (s *Server) handleDeviceStateList(ctx context.Context, req *mcp.CallToolRequest, input DeviceStateListInput) (*mcp.CallToolResult, DeviceStateListOutput, error) {
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceStateListOutput{Success: false, Error: err.Error()}, nil
	}
	body, err := s.sessionMgr.WorkerRequestOnSession(ctx, session, "/device_state/list", nil)
	if err != nil {
		return nil, DeviceStateListOutput{Success: false, Error: err.Error()}, nil
	}
	var out DeviceStateListOutput
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, DeviceStateListOutput{Success: false, Error: fmt.Sprintf("decode: %v", err)}, nil
	}
	return nil, out, nil
}

// DeviceStateSnapshotInput defines input for device_state_snapshot.
type DeviceStateSnapshotInput = DeviceStateSessionInput

// DeviceStateSnapshotOutput mirrors the worker's DeviceStateSnapshotResponse.
type DeviceStateSnapshotOutput struct {
	Success    bool                   `json:"success"`
	Platform   string                 `json:"platform,omitempty"`
	SnapshotID string                 `json:"snapshot_id,omitempty"`
	Line       map[string]interface{} `json:"line,omitempty"`
	Error      string                 `json:"error,omitempty"`
}

func (s *Server) handleDeviceStateSnapshot(ctx context.Context, req *mcp.CallToolRequest, input DeviceStateSnapshotInput) (*mcp.CallToolResult, DeviceStateSnapshotOutput, error) {
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceStateSnapshotOutput{Success: false, Error: err.Error()}, nil
	}
	body, err := s.sessionMgr.WorkerRequestOnSession(ctx, session, "/device_state/snapshot", map[string]any{})
	if err != nil {
		return nil, DeviceStateSnapshotOutput{Success: false, Error: err.Error()}, nil
	}
	var out DeviceStateSnapshotOutput
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, DeviceStateSnapshotOutput{Success: false, Error: fmt.Sprintf("decode: %v", err)}, nil
	}
	return nil, out, nil
}

// DeviceStateDiffInput defines input for device_state_diff.
type DeviceStateDiffInput struct {
	SnapshotID   string `json:"snapshot_id" jsonschema:"Snapshot id returned by device_state_snapshot (REQUIRED)."`
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

// DeviceStateDiffOutput mirrors the worker's DeviceStateDiffResponse.
type DeviceStateDiffOutput struct {
	Success        bool                              `json:"success"`
	Platform       string                            `json:"platform,omitempty"`
	FromSnapshotID string                            `json:"from_snapshot_id,omitempty"`
	ToCursor       string                            `json:"to_cursor,omitempty"`
	UserDefaults   map[string]map[string]interface{} `json:"userdefaults,omitempty"`
	SQLite         map[string]map[string]interface{} `json:"sqlite,omitempty"`
	Error          string                            `json:"error,omitempty"`
}

func (s *Server) handleDeviceStateDiff(ctx context.Context, req *mcp.CallToolRequest, input DeviceStateDiffInput) (*mcp.CallToolResult, DeviceStateDiffOutput, error) {
	if strings.TrimSpace(input.SnapshotID) == "" {
		return nil, DeviceStateDiffOutput{Success: false, Error: "snapshot_id is required"}, nil
	}
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceStateDiffOutput{Success: false, Error: err.Error()}, nil
	}
	body, err := s.sessionMgr.WorkerRequestOnSession(ctx, session, "/device_state/diff", map[string]any{"snapshot_id": input.SnapshotID})
	if err != nil {
		return nil, DeviceStateDiffOutput{Success: false, Error: err.Error()}, nil
	}
	var out DeviceStateDiffOutput
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, DeviceStateDiffOutput{Success: false, Error: fmt.Sprintf("decode: %v", err)}, nil
	}
	return nil, out, nil
}

// DeviceStateQueryInput is a tagged union: target='userdefaults' OR 'sqlite'.
// Go has no built-in unions, so we declare all the fields optional and rely
// on the handler to enforce the right combination.
type DeviceStateQueryInput struct {
	Target       string        `json:"target" jsonschema:"Either 'userdefaults' or 'sqlite' (REQUIRED)."`
	PlistPath    string        `json:"plist_path,omitempty" jsonschema:"For userdefaults target — container-relative plist path (e.g. 'Library/Preferences/com.x.plist')."`
	Key          string        `json:"key,omitempty" jsonschema:"For userdefaults target — top-level plist key. Omit to return the whole plist."`
	DBPath       string        `json:"db_path,omitempty" jsonschema:"For sqlite target — container-relative sqlite path."`
	SQL          string        `json:"sql,omitempty" jsonschema:"For sqlite target — a single SELECT or WITH...SELECT statement."`
	Params       []interface{} `json:"params,omitempty" jsonschema:"For sqlite target — positional '?' placeholders. JSON-typed."`
	SessionIndex *int          `json:"session_index,omitempty" jsonschema:"Session index. Omit for active session."`
	SessionID    string        `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

// DeviceStateQueryOutput covers both targets — only the relevant subset
// will be populated.
type DeviceStateQueryOutput struct {
	Success bool `json:"success"`
	// Common
	Platform string `json:"platform,omitempty"`
	Error    string `json:"error,omitempty"`
	// userdefaults
	Value json.RawMessage `json:"value,omitempty"`
	Found *bool           `json:"found,omitempty"`
	// sqlite
	Cols      []string        `json:"cols,omitempty"`
	Rows      [][]interface{} `json:"rows,omitempty"`
	Truncated bool            `json:"truncated,omitempty"`
}

func (s *Server) handleDeviceStateQuery(ctx context.Context, req *mcp.CallToolRequest, input DeviceStateQueryInput) (*mcp.CallToolResult, DeviceStateQueryOutput, error) {
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceStateQueryOutput{Success: false, Error: err.Error()}, nil
	}
	switch input.Target {
	case "userdefaults":
		if input.PlistPath == "" {
			return nil, DeviceStateQueryOutput{Success: false, Error: "plist_path required for target=userdefaults"}, nil
		}
		reqBody := map[string]any{"plist_path": input.PlistPath}
		if input.Key != "" {
			reqBody["key"] = input.Key
		}
		body, err := s.sessionMgr.WorkerRequestOnSession(ctx, session, "/device_state/userdefaults", reqBody)
		if err != nil {
			return nil, DeviceStateQueryOutput{Success: false, Error: err.Error()}, nil
		}
		var out DeviceStateQueryOutput
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, DeviceStateQueryOutput{Success: false, Error: fmt.Sprintf("decode: %v", err)}, nil
		}
		return nil, out, nil
	case "sqlite":
		if input.DBPath == "" || input.SQL == "" {
			return nil, DeviceStateQueryOutput{Success: false, Error: "db_path and sql required for target=sqlite"}, nil
		}
		params := input.Params
		if params == nil {
			params = []interface{}{}
		}
		reqBody := map[string]any{
			"db_path": input.DBPath,
			"sql":     input.SQL,
			"params":  params,
		}
		body, err := s.sessionMgr.WorkerRequestOnSession(ctx, session, "/device_state/sqlite/query", reqBody)
		if err != nil {
			return nil, DeviceStateQueryOutput{Success: false, Error: err.Error()}, nil
		}
		var out DeviceStateQueryOutput
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, DeviceStateQueryOutput{Success: false, Error: fmt.Sprintf("decode: %v", err)}, nil
		}
		return nil, out, nil
	default:
		return nil, DeviceStateQueryOutput{Success: false, Error: "target must be 'userdefaults' or 'sqlite'"}, nil
	}
}

// ---------------------------------------------------------------------------
// poll_network_requests tool
// ---------------------------------------------------------------------------

// PollNetworkRequestsInput defines parameters for the poll_network_requests MCP tool.
type PollNetworkRequestsInput struct {
	SessionIndex *int   `json:"session_index,omitempty" jsonschema:"Session index to query. Omit for active session."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
	Cursor       string `json:"cursor,omitempty" jsonschema:"Opaque cursor from a previous response. Use '0' for the first call."`
	Limit        int    `json:"limit,omitempty" jsonschema:"Maximum number of requests to return. Default 100."`
	MaxBytes     int    `json:"max_bytes,omitempty" jsonschema:"Maximum encoded payload bytes to return. Default 262144."`
}

// PollNetworkRequestsOutput wraps the NetworkPollResponse for MCP consumers.
type PollNetworkRequestsOutput struct {
	NetworkPollResponse
	NextSteps []NextStep `json:"next_steps,omitempty"`
}

func (s *Server) handlePollNetworkRequests(ctx context.Context, req *mcp.CallToolRequest, input PollNetworkRequestsInput) (*mcp.CallToolResult, PollNetworkRequestsOutput, error) {
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, PollNetworkRequestsOutput{
			NetworkPollResponse: NetworkPollResponse{Success: false},
			NextSteps: []NextStep{
				{Tool: "start_device_session", Reason: "No active session to poll network requests from"},
			},
		}, nil
	}

	cursor := input.Cursor
	if cursor == "" {
		cursor = "0"
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 100
	}
	maxBytes := input.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 262144
	}

	resp, pollErr := s.sessionMgr.PollNetworkRequestsOnSession(ctx, session, cursor, limit, maxBytes)
	if pollErr != nil {
		return nil, PollNetworkRequestsOutput{
			NetworkPollResponse: NetworkPollResponse{
				Success:    false,
				Platform:   session.Platform,
				NextCursor: cursor,
			},
		}, fmt.Errorf("poll failed: %w", pollErr)
	}

	output := PollNetworkRequestsOutput{
		NetworkPollResponse: *resp,
	}
	if len(resp.Items) > 0 {
		output.NextSteps = []NextStep{
			{Tool: "poll_network_requests", Params: fmt.Sprintf("cursor=\"%s\"", resp.NextCursor), Reason: "Continue polling for new requests"},
		}
	}
	return nil, output, nil
}
