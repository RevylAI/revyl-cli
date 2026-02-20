package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/revyl/cli/internal/hotreload"
)

func (s *Server) registerDevLoopTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "start_dev_loop",
		Description: "Start the revyl dev equivalent for MCP: hot reload (Expo), tunnel, device session, app install/launch, and deep-link navigation.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Start Dev Loop",
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleStartDevLoop)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "stop_dev_loop",
		Description: "Stop the active dev loop session (hot reload + linked device session).",
		Annotations: &mcp.ToolAnnotations{
			Title: "Stop Dev Loop",
		},
	}, s.handleStopDevLoop)
}

type StartDevLoopInput struct {
	Platform       string `json:"platform,omitempty" jsonschema:"Target platform for the cloud device (ios or android). Default: ios."`
	PlatformKey    string `json:"platform_key,omitempty" jsonschema:"Optional build.platforms key override for resolving the dev build."`
	AppID          string `json:"app_id,omitempty" jsonschema:"Optional app ID override used to resolve latest build."`
	BuildVersionID string `json:"build_version_id,omitempty" jsonschema:"Optional explicit build version ID. Skips latest-build resolution."`
	Port           int    `json:"port,omitempty" jsonschema:"Optional hot reload dev-server port override."`
	Timeout        int    `json:"timeout,omitempty" jsonschema:"Device idle timeout in seconds (default 300)."`
}

type StartDevLoopOutput struct {
	Success            bool   `json:"success"`
	SessionIndex       int    `json:"session_index"`
	ManualStepRequired bool   `json:"manual_step_required,omitempty"`
	DeepLinkURL        string `json:"deep_link_url,omitempty"`
	ViewerURL          string `json:"viewer_url,omitempty"`
	Error              string `json:"error,omitempty"`
}

func (s *Server) clearDevLoopState() (*hotreload.Manager, int, bool) {
	s.hotReloadMu.Lock()
	defer s.hotReloadMu.Unlock()

	manager := s.hotReloadManager
	sessionIndex := s.devLoopSessionIndex
	shouldStopSession := s.devLoopActive && sessionIndex >= 0

	s.hotReloadManager = nil
	s.hotReloadResult = nil
	s.hotReloadTestID = ""
	s.devLoopActive = false
	s.devLoopSessionIndex = -1
	s.devLoopManualStepRequired = false

	return manager, sessionIndex, shouldStopSession
}

func isNoSessionAtIndexError(err error, index int) bool {
	if err == nil || index < 0 {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), strings.ToLower(fmt.Sprintf("no session at index %d", index)))
}

func (s *Server) stopDetachedDevLoop(ctx context.Context, manager *hotreload.Manager, sessionIndex int, shouldStopSession bool) error {
	if manager != nil {
		manager.Stop()
	}

	if !shouldStopSession {
		return nil
	}
	if s.sessionMgr == nil {
		return nil
	}

	if err := s.sessionMgr.StopSession(ctx, sessionIndex); err != nil {
		if isNoSessionAtIndexError(err, sessionIndex) {
			return nil
		}
		return err
	}
	return nil
}

func (s *Server) handleStartDevLoop(ctx context.Context, req *mcp.CallToolRequest, input StartDevLoopInput) (*mcp.CallToolResult, StartDevLoopOutput, error) {
	if s.config == nil {
		return nil, StartDevLoopOutput{Success: false, SessionIndex: -1, Error: "project config not loaded. Run revyl init first"}, nil
	}
	if !s.config.HotReload.IsConfigured() {
		return nil, StartDevLoopOutput{Success: false, SessionIndex: -1, Error: "hot reload is not configured. Run revyl init --hotreload"}, nil
	}

	platform := normalizePlatform(input.Platform)
	if platform != "ios" && platform != "android" {
		return nil, StartDevLoopOutput{Success: false, SessionIndex: -1, Error: "platform must be 'ios' or 'android'"}, nil
	}

	providerName, err := s.config.HotReload.GetActiveProvider("")
	if err != nil {
		return nil, StartDevLoopOutput{Success: false, SessionIndex: -1, Error: fmt.Sprintf("failed to resolve hot reload provider: %v", err)}, nil
	}
	if providerName != "expo" {
		return nil, StartDevLoopOutput{
			Success:      false,
			SessionIndex: -1,
			Error:        fmt.Sprintf("provider %q is configured but start_dev_loop currently supports Expo only", providerName),
		}, nil
	}

	baseProviderCfg := s.config.HotReload.GetProviderConfig(providerName)
	if baseProviderCfg == nil {
		return nil, StartDevLoopOutput{
			Success:      false,
			SessionIndex: -1,
			Error:        fmt.Sprintf("hotreload provider %q is not configured", providerName),
		}, nil
	}
	providerCfg := *baseProviderCfg
	if input.Port > 0 {
		providerCfg.Port = input.Port
	}

	if strings.TrimSpace(providerCfg.AppScheme) == "" {
		return nil, StartDevLoopOutput{
			Success:      false,
			SessionIndex: -1,
			Error:        "hotreload.providers.expo.app_scheme is required",
		}, nil
	}

	// Resolve build target.
	selectedAppID := strings.TrimSpace(input.AppID)
	selectedBuildVersionID := strings.TrimSpace(input.BuildVersionID)
	platformKey := strings.TrimSpace(input.PlatformKey)

	if selectedBuildVersionID == "" {
		if selectedAppID == "" {
			if platformKey == "" {
				platformKey = strings.TrimSpace(providerCfg.PlatformKeys[platform])
				if platformKey == "" {
					return nil, StartDevLoopOutput{
						Success:      false,
						SessionIndex: -1,
						Error:        fmt.Sprintf("no platform key mapping for %q in hotreload.providers.expo.platform_keys", platform),
					}, nil
				}
			}

			cfgForKey, ok := s.config.Build.Platforms[platformKey]
			if !ok {
				return nil, StartDevLoopOutput{
					Success:      false,
					SessionIndex: -1,
					Error:        fmt.Sprintf("build.platforms.%s not found", platformKey),
				}, nil
			}
			selectedAppID = strings.TrimSpace(cfgForKey.AppID)
			if selectedAppID == "" {
				return nil, StartDevLoopOutput{
					Success:      false,
					SessionIndex: -1,
					Error:        fmt.Sprintf("build.platforms.%s.app_id is empty", platformKey),
				}, nil
			}
		}

		latestVersion, latestErr := s.apiClient.GetLatestBuildVersion(ctx, selectedAppID)
		if latestErr != nil {
			return nil, StartDevLoopOutput{
				Success:      false,
				SessionIndex: -1,
				Error:        fmt.Sprintf("failed to resolve latest build for app %s: %v", selectedAppID, latestErr),
			}, nil
		}
		if latestVersion == nil {
			return nil, StartDevLoopOutput{
				Success:      false,
				SessionIndex: -1,
				Error:        fmt.Sprintf("no builds found for app %s", selectedAppID),
			}, nil
		}
		selectedBuildVersionID = latestVersion.ID
	}

	buildDetail, err := s.apiClient.GetBuildVersionDownloadURL(ctx, selectedBuildVersionID)
	if err != nil {
		return nil, StartDevLoopOutput{
			Success:      false,
			SessionIndex: -1,
			Error:        fmt.Sprintf("failed to resolve build download URL: %v", err),
		}, nil
	}

	// Stop any existing hot-reload/dev-loop state before creating a new one.
	prevManager, prevSessionIndex, prevShouldStopSession := s.clearDevLoopState()
	if err := s.stopDetachedDevLoop(context.Background(), prevManager, prevSessionIndex, prevShouldStopSession); err != nil {
		return nil, StartDevLoopOutput{
			Success:      false,
			SessionIndex: prevSessionIndex,
			Error: fmt.Sprintf(
				"failed to stop previous dev loop session %d: %v. "+
					"Use list_device_sessions() and stop_device_session(session_index=%d) to clean up before retrying.",
				prevSessionIndex,
				err,
				prevSessionIndex,
			),
		}, nil
	}

	manager := hotreload.NewManager(providerName, &providerCfg, s.workDir)
	startResult, startErr := manager.Start(context.Background())
	if startErr != nil {
		return nil, StartDevLoopOutput{
			Success:      false,
			SessionIndex: -1,
			Error:        fmt.Sprintf("failed to start hot reload: %v", startErr),
		}, nil
	}

	timeoutSecs := input.Timeout
	if timeoutSecs <= 0 {
		timeoutSecs = 300
	}

	_, session, err := s.sessionMgr.StartSession(ctx, StartSessionOptions{
		Platform:       platform,
		AppID:          selectedAppID,
		BuildVersionID: selectedBuildVersionID,
		AppURL:         strings.TrimSpace(buildDetail.DownloadURL),
		AppPackage:     strings.TrimSpace(buildDetail.PackageName),
		AppLink:        startResult.DeepLinkURL,
		IdleTimeout:    time.Duration(timeoutSecs) * time.Second,
	})
	if err != nil {
		manager.Stop()
		return nil, StartDevLoopOutput{
			Success:      false,
			SessionIndex: -1,
			Error:        fmt.Sprintf("failed to start device session: %v", err),
		}, nil
	}

	cleanupOnError := func(msg string) (*mcp.CallToolResult, StartDevLoopOutput, error) {
		_ = s.sessionMgr.StopSession(context.Background(), session.Index)
		manager.Stop()
		return nil, StartDevLoopOutput{
			Success:      false,
			SessionIndex: session.Index,
			ViewerURL:    session.ViewerURL,
			DeepLinkURL:  startResult.DeepLinkURL,
			Error:        msg,
		}, nil
	}

	// Ensure deterministic install + launch + deep-link behavior, same spirit as revyl dev.
	installBody := map[string]string{
		"app_url": strings.TrimSpace(buildDetail.DownloadURL),
	}
	if pkg := strings.TrimSpace(buildDetail.PackageName); pkg != "" {
		installBody["bundle_id"] = pkg
	}

	installResp, err := s.sessionMgr.WorkerRequestForSession(ctx, session.Index, "POST", "/install", installBody)
	if err != nil {
		return cleanupOnError(fmt.Sprintf("device started but app install failed: %v", err))
	}
	if err := ensureWorkerActionSucceeded(installResp, "install"); err != nil {
		return cleanupOnError(fmt.Sprintf("device started but app install failed: %v", err))
	}

	bundleID := strings.TrimSpace(buildDetail.PackageName)
	if bundleID == "" {
		bundleID = extractWorkerBundleID(installResp)
	}
	if bundleID != "" {
		launchResp, err := s.sessionMgr.WorkerRequestForSession(ctx, session.Index, "POST", "/launch", map[string]string{
			"bundle_id": bundleID,
		})
		if err != nil {
			return cleanupOnError(fmt.Sprintf("app install succeeded but app launch failed: %v", err))
		}
		if err := ensureWorkerActionSucceeded(launchResp, "launch"); err != nil {
			return cleanupOnError(fmt.Sprintf("app install succeeded but app launch failed: %v", err))
		}
	}

	manualStepRequired := false
	if deepLink := strings.TrimSpace(startResult.DeepLinkURL); deepLink != "" {
		openURLResp, err := s.sessionMgr.WorkerRequestForSession(ctx, session.Index, "POST", "/open_url", map[string]string{"url": deepLink})
		if err != nil {
			if isUnsupportedWorkerRoute(err, "/open_url") {
				manualStepRequired = true
			} else {
				return cleanupOnError(fmt.Sprintf("deep-link navigation failed: %v", err))
			}
		} else if err := ensureWorkerActionSucceeded(openURLResp, "open_url"); err != nil {
			return cleanupOnError(fmt.Sprintf("deep-link navigation failed: %v", err))
		}
	}

	s.hotReloadMu.Lock()
	s.hotReloadManager = manager
	s.hotReloadResult = startResult
	s.hotReloadTestID = ""
	s.devLoopActive = true
	s.devLoopSessionIndex = session.Index
	s.devLoopManualStepRequired = manualStepRequired
	s.hotReloadMu.Unlock()

	return nil, StartDevLoopOutput{
		Success:            true,
		SessionIndex:       session.Index,
		ManualStepRequired: manualStepRequired,
		DeepLinkURL:        startResult.DeepLinkURL,
		ViewerURL:          session.ViewerURL,
	}, nil
}

type StopDevLoopInput struct{}

type StopDevLoopOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleStopDevLoop(ctx context.Context, req *mcp.CallToolRequest, input StopDevLoopInput) (*mcp.CallToolResult, StopDevLoopOutput, error) {
	manager, sessionIndex, shouldStopSession := s.clearDevLoopState()
	if err := s.stopDetachedDevLoop(ctx, manager, sessionIndex, shouldStopSession); err != nil {
		return nil, StopDevLoopOutput{
			Success: false,
			Error:   fmt.Sprintf("stopped hot reload but failed to stop device session %d: %v", sessionIndex, err),
		}, nil
	}

	if shouldStopSession {
		return nil, StopDevLoopOutput{Success: true, Message: "Dev loop stopped"}, nil
	}
	return nil, StopDevLoopOutput{Success: true, Message: "No active dev loop session"}, nil
}

type workerActionResponse struct {
	SuccessLower *bool  `json:"success"`
	SuccessUpper *bool  `json:"Success"`
	Action       string `json:"action"`
	Error        string `json:"error"`
}

func ensureWorkerActionSucceeded(respBody []byte, expectedAction string) error {
	var resp workerActionResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("failed to parse worker %s response: %w", expectedAction, err)
	}

	if resp.Action != "" && expectedAction != "" && resp.Action != expectedAction {
		return fmt.Errorf("worker returned action=%q, expected %q", resp.Action, expectedAction)
	}

	successKnown := false
	success := false
	if resp.SuccessLower != nil {
		successKnown = true
		success = *resp.SuccessLower
	}
	if resp.SuccessUpper != nil {
		successKnown = true
		success = *resp.SuccessUpper
	}
	if !successKnown {
		return fmt.Errorf("worker %s response missing success field", expectedAction)
	}
	if !success {
		errMsg := strings.TrimSpace(resp.Error)
		if errMsg == "" {
			errMsg = fmt.Sprintf("worker reported %s failure", expectedAction)
		}
		return fmt.Errorf("%s", errMsg)
	}
	return nil
}

type workerInstallMetadata struct {
	BundleID    string `json:"bundle_id"`
	PackageName string `json:"package_name"`
	AppPackage  string `json:"app_package"`
}

func extractWorkerBundleID(respBody []byte) string {
	var meta workerInstallMetadata
	if err := json.Unmarshal(respBody, &meta); err != nil {
		return ""
	}
	for _, candidate := range []string{meta.BundleID, meta.PackageName, meta.AppPackage} {
		if value := strings.TrimSpace(candidate); value != "" {
			return value
		}
	}
	return ""
}

func isUnsupportedWorkerRoute(err error, path string) bool {
	var workerErr *WorkerHTTPError
	if !errors.As(err, &workerErr) {
		return false
	}
	return workerErr.StatusCode == 404 && strings.TrimSpace(workerErr.Path) == strings.TrimSpace(path)
}

func normalizePlatform(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "ios"
	}
	return value
}
