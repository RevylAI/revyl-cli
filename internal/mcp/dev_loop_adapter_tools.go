package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/revyl/cli/internal/devloop"
	"github.com/revyl/cli/internal/outcome"
)

const (
	devLoopBaselineReadyTimeout = 30 * time.Second
	devLoopBaselinePollInterval = 500 * time.Millisecond
)

// DevLoopStartOutput wraps the canonical detach handshake.
type DevLoopStartOutput struct {
	Success     bool                `json:"success"`
	Outcome     outcome.Envelope    `json:"outcome"`
	Result      devloop.StartResult `json:"result"`
	Screenshot  *ScreenshotOutput   `json:"screenshot,omitempty"`
	Remediation *Remediation        `json:"remediation,omitempty"`
	Error       string              `json:"error,omitempty"`
}

// GetDevStatusInput selects an optional named context.
type GetDevStatusInput struct {
	Context    string `json:"context,omitempty" jsonschema:"Optional named dev context."`
	ProjectDir string `json:"project_dir,omitempty" jsonschema:"Optional project or monorepo root."`
}

// GetDevStatusOutput wraps the canonical dev status contract.
type GetDevStatusOutput struct {
	Success     bool                 `json:"success"`
	Outcome     outcome.Envelope     `json:"outcome"`
	Result      devloop.StatusResult `json:"result"`
	Remediation *Remediation         `json:"remediation,omitempty"`
	Error       string               `json:"error,omitempty"`
}

// CanonicalStopDevLoopInput selects an optional named context.
type CanonicalStopDevLoopInput struct {
	Context    string `json:"context,omitempty" jsonschema:"Optional named dev context."`
	ProjectDir string `json:"project_dir,omitempty" jsonschema:"Optional project or monorepo root."`
}

// CanonicalStopDevLoopOutput wraps canonical dev-loop cleanup.
type CanonicalStopDevLoopOutput struct {
	Success     bool               `json:"success"`
	Outcome     outcome.Envelope   `json:"outcome"`
	Result      devloop.StopResult `json:"result"`
	Remediation *Remediation       `json:"remediation,omitempty"`
	Error       string             `json:"error,omitempty"`
}

// RebuildInput selects one asynchronous rebuild trigger.
type RebuildInput struct {
	Context    string `json:"context,omitempty" jsonschema:"Optional named dev context."`
	ProjectDir string `json:"project_dir,omitempty" jsonschema:"Optional project or monorepo root."`
}

// TriggerRebuildOutput contains the handle for one requested rebuild.
type TriggerRebuildOutput struct {
	Success     bool                  `json:"success"`
	Outcome     outcome.Envelope      `json:"outcome"`
	Handle      devloop.RebuildHandle `json:"handle"`
	Remediation *Remediation          `json:"remediation,omitempty"`
	Error       string                `json:"error,omitempty"`
}

// WaitForRebuildInput selects one asynchronous rebuild handle to wait for.
type WaitForRebuildInput struct {
	Handle  devloop.RebuildHandle `json:"handle" jsonschema:"required,Handle returned by rebuild."`
	Timeout int                   `json:"timeout,omitempty" jsonschema:"Maximum seconds to wait for completion."`
}

// RebuildOutput contains the canonical terminal result returned by wait_for_rebuild.
type RebuildOutput struct {
	Success     bool                  `json:"success"`
	Outcome     outcome.Envelope      `json:"outcome"`
	Rebuild     devloop.RebuildResult `json:"rebuild"`
	Remediation *Remediation          `json:"remediation,omitempty"`
	Error       string                `json:"error,omitempty"`
}

// rebuildProgressNotifier sends ordered MCP progress messages for one rebuild request.
type rebuildProgressNotifier struct {
	request  *mcp.CallToolRequest
	token    any
	progress float64
}

// handleStartDevLoopCommand delegates startup to the canonical CLI detach flow.
func (s *Server) handleStartDevLoopCommand(
	ctx context.Context,
	req *mcp.CallToolRequest,
	input StartDevLoopInput,
) (*mcp.CallToolResult, DevLoopStartOutput, error) {
	if failure := s.refreshDevAuthentication(); failure != nil {
		return &mcp.CallToolResult{IsError: true}, DevLoopStartOutput{
			Success:     false,
			Outcome:     failedAuthenticationOutcome(failure),
			Remediation: authenticationRemediation(failure.Code),
			Error:       failure.Message,
		}, nil
	}
	workDir, err := s.resolveValidatedDevProjectDir(input.ProjectDir)
	if err != nil {
		failureOutcome, remediation := projectResolutionFailure(err)
		return &mcp.CallToolResult{IsError: true}, DevLoopStartOutput{
			Success:     false,
			Outcome:     failureOutcome,
			Remediation: remediation,
			Error:       err.Error(),
		}, nil
	}
	result, err := s.devLoopRunner.Start(ctx, workDir, devloop.StartRequest{
		Context:        input.Context,
		Platform:       input.Platform,
		PlatformKey:    input.PlatformKey,
		AppID:          input.AppID,
		BuildVersionID: input.BuildVersionID,
		Port:           input.Port,
		TimeoutSeconds: input.Timeout,
		Remote:         input.Remote,
		SeedLatest:     input.SeedLatest,
	})
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, DevLoopStartOutput{
			Success: false,
			Outcome: outcome.Failed("dev_loop_start_failed", err.Error(), true),
			Result:  result,
			Error:   err.Error(),
		}, nil
	}
	s.rememberDelegatedDevLoop(workDir, result.Context)
	if result.SessionID != "" {
		localIndex, _, attachErr := s.sessionMgr.AttachBySessionID(ctx, result.SessionID)
		if attachErr != nil {
			errorMessage := fmt.Sprintf(
				"dev loop started, but MCP could not attach session %q for device interaction: %v; call stop_dev_loop before retrying start_dev_loop",
				result.SessionID,
				attachErr,
			)
			result.SessionIndex = -1
			failureOutcome := outcome.Failed("dev_loop_session_attach_failed", errorMessage, false)
			failureOutcome.SessionID = result.SessionID
			failureOutcome.ViewerURL = result.ViewerURL
			return &mcp.CallToolResult{IsError: true}, DevLoopStartOutput{
				Success: false,
				Outcome: failureOutcome,
				Result:  result,
				Error:   errorMessage,
			}, nil
		}
		s.sessionMgr.StopIdleTimer(localIndex)
		result.SessionIndex = localIndex
	}
	captureBaseline := !input.Remote
	if input.Remote && input.SeedLatest {
		if result.InstalledSeed || result.Build.FreshBuildApplied {
			captureBaseline = true
		} else if status, ready := waitForDevAppReady(
			ctx,
			s.devLoopRunner,
			workDir,
			result.Context,
			devLoopBaselineReadyTimeout,
			devLoopBaselinePollInterval,
		); ready {
			result.InstalledSeed = status.InstalledSeed
			result.SeededVersion = status.SeededVersion
			result.Build = status.Build
			captureBaseline = true
		}
	}
	startOutcome := outcome.Completed()
	startOutcome.SessionID = result.SessionID
	startOutcome.SessionIndex = &result.SessionIndex
	startOutcome.ViewerURL = result.ViewerURL
	output := DevLoopStartOutput{Success: true, Outcome: startOutcome, Result: result}
	if !captureBaseline {
		return nil, output, nil
	}
	screenshotResult, screenshot, screenshotErr := s.handleScreenshot(
		ctx,
		req,
		ScreenshotInput{SessionIndex: &result.SessionIndex},
	)
	if screenshotErr == nil && screenshot.Success {
		output.Screenshot = &screenshot
		return screenshotResult, output, nil
	}
	return nil, output, nil
}

// waitForDevAppReady waits until a seeded or fresh remote app is installed.
//
// Parameters:
//   - ctx: Parent cancellation context.
//   - runner: Canonical dev-loop status provider.
//   - workDir: Revyl project directory.
//   - contextName: Named dev context to inspect.
//   - timeout: Maximum readiness wait.
//   - pollInterval: Delay between status reads.
//
// Returns:
//   - devloop.StatusResult: Latest readable status.
//   - bool: Whether an app is ready for baseline capture.
func waitForDevAppReady(
	ctx context.Context,
	runner devloop.Runner,
	workDir string,
	contextName string,
	timeout time.Duration,
	pollInterval time.Duration,
) (devloop.StatusResult, bool) {
	var latest devloop.StatusResult
	if runner == nil || timeout <= 0 {
		return latest, false
	}
	if pollInterval <= 0 {
		pollInterval = devLoopBaselinePollInterval
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		status, err := runner.Status(waitCtx, workDir, contextName)
		if err == nil {
			latest = status
			if status.InstalledSeed || status.Build.FreshBuildApplied {
				return latest, true
			}
			if status.Build.State == devloop.BuildStateFailed ||
				status.Build.State == devloop.BuildStateCancelled {
				return latest, false
			}
		}

		timer := time.NewTimer(pollInterval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return latest, false
		case <-timer.C:
		}
	}
}

// handleGetDevStatusCommand delegates status reads to the canonical CLI contract.
func (s *Server) handleGetDevStatusCommand(
	ctx context.Context,
	req *mcp.CallToolRequest,
	input GetDevStatusInput,
) (*mcp.CallToolResult, GetDevStatusOutput, error) {
	if failure := s.refreshDevAuthentication(); failure != nil {
		return &mcp.CallToolResult{IsError: true}, GetDevStatusOutput{
			Success:     false,
			Outcome:     failedAuthenticationOutcome(failure),
			Remediation: authenticationRemediation(failure.Code),
			Error:       failure.Message,
		}, nil
	}
	workDir, contextName, err := s.delegatedDevTarget(input.ProjectDir, input.Context)
	if err != nil {
		failureOutcome, remediation := projectResolutionFailure(err)
		return &mcp.CallToolResult{IsError: true}, GetDevStatusOutput{
			Success: false, Outcome: failureOutcome, Remediation: remediation, Error: err.Error(),
		}, nil
	}
	result, err := s.devLoopRunner.Status(ctx, workDir, contextName)
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, GetDevStatusOutput{
			Success: false,
			Outcome: outcome.Failed("dev_status_failed", err.Error(), true),
			Result:  result,
			Error:   err.Error(),
		}, nil
	}
	statusOutcome := outcome.Completed()
	statusOutcome.SessionID = result.SessionID
	statusOutcome.ViewerURL = result.ViewerURL
	statusOutcome.BuildJobID = result.Build.RemoteJobID
	return nil, GetDevStatusOutput{Success: true, Outcome: statusOutcome, Result: result}, nil
}

// handleStopDevLoopCommand delegates cleanup to the canonical CLI contract.
func (s *Server) handleStopDevLoopCommand(
	ctx context.Context,
	req *mcp.CallToolRequest,
	input CanonicalStopDevLoopInput,
) (*mcp.CallToolResult, CanonicalStopDevLoopOutput, error) {
	if failure := s.refreshDevAuthentication(); failure != nil {
		return &mcp.CallToolResult{IsError: true}, CanonicalStopDevLoopOutput{
			Success:     false,
			Outcome:     failedAuthenticationOutcome(failure),
			Remediation: authenticationRemediation(failure.Code),
			Error:       failure.Message,
		}, nil
	}
	workDir, contextName, err := s.delegatedDevTarget(input.ProjectDir, input.Context)
	if err != nil {
		failureOutcome, remediation := projectResolutionFailure(err)
		return &mcp.CallToolResult{IsError: true}, CanonicalStopDevLoopOutput{
			Success: false, Outcome: failureOutcome, Remediation: remediation, Error: err.Error(),
		}, nil
	}
	result, err := s.devLoopRunner.Stop(ctx, workDir, contextName)
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, CanonicalStopDevLoopOutput{
			Success: false,
			Outcome: outcome.Failed("dev_loop_stop_failed", err.Error(), true),
			Result:  result,
			Error:   err.Error(),
		}, nil
	}
	s.clearDelegatedDevLoopIfMatches(workDir, contextName)
	return nil, CanonicalStopDevLoopOutput{Success: true, Outcome: outcome.Completed(), Result: result}, nil
}

// registerRebuildTool registers the canonical rebuild-only MCP operation.
func (s *Server) registerRebuildTool() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "rebuild",
		Description: "Trigger one local or remote rebuild and return immediately with a handle.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Rebuild",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleRebuildCommand)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "wait_for_rebuild",
		Description: "Wait for a rebuild handle, stream progress when supported, and return its terminal result.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Wait For Rebuild",
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleWaitForRebuildCommand)
}

// handleRebuildCommand triggers one rebuild and returns its correlation handle.
//
// Parameters:
//   - ctx: Cancellation and timeout context for the tool call.
//   - req: MCP request metadata.
//   - input: Project and context selection.
//
// Returns:
//   - *mcp.CallToolResult: Error metadata when the rebuild fails.
//   - TriggerRebuildOutput: Structured asynchronous rebuild handle.
//   - error: Transport-level handler failure.
func (s *Server) handleRebuildCommand(
	ctx context.Context,
	req *mcp.CallToolRequest,
	input RebuildInput,
) (*mcp.CallToolResult, TriggerRebuildOutput, error) {
	if failure := s.refreshDevAuthentication(); failure != nil {
		return &mcp.CallToolResult{IsError: true}, TriggerRebuildOutput{
			Success:     false,
			Outcome:     failedAuthenticationOutcome(failure),
			Remediation: authenticationRemediation(failure.Code),
			Error:       failure.Message,
		}, nil
	}
	workDir, contextName, targetErr := s.delegatedDevTarget(input.ProjectDir, input.Context)
	if targetErr == nil {
		targetErr = validateDevProjectConfig(workDir)
	}
	if targetErr != nil {
		failureOutcome, remediation := projectResolutionFailure(targetErr)
		return &mcp.CallToolResult{IsError: true}, TriggerRebuildOutput{
			Success: false, Outcome: failureOutcome, Remediation: remediation, Error: targetErr.Error(),
		}, nil
	}
	handle, triggerErr := s.devLoopRunner.TriggerRebuild(ctx, workDir, devloop.TriggerRebuildRequest{
		Context: contextName,
	})
	if triggerErr != nil {
		return &mcp.CallToolResult{IsError: true}, TriggerRebuildOutput{
			Success: false,
			Outcome: outcome.Failed("rebuild_trigger_failed", triggerErr.Error(), true),
			Handle:  handle,
			Error:   triggerErr.Error(),
		}, nil
	}
	return nil, TriggerRebuildOutput{
		Success: true,
		Outcome: outcome.Envelope{OperationStatus: "triggered"},
		Handle:  handle,
	}, nil
}

// handleWaitForRebuildCommand waits for one handle and returns its terminal result.
//
// Parameters:
//   - ctx: Cancellation and timeout context for the tool call.
//   - req: MCP request carrying the optional progress token.
//   - input: Rebuild handle and wait timeout.
//
// Returns:
//   - *mcp.CallToolResult: Error metadata when the wait or rebuild fails.
//   - RebuildOutput: Structured terminal rebuild result.
//   - error: Transport-level handler failure.
func (s *Server) handleWaitForRebuildCommand(
	ctx context.Context,
	req *mcp.CallToolRequest,
	input WaitForRebuildInput,
) (*mcp.CallToolResult, RebuildOutput, error) {
	if failure := s.refreshDevAuthentication(); failure != nil {
		return &mcp.CallToolResult{IsError: true}, RebuildOutput{
			Success:     false,
			Outcome:     failedAuthenticationOutcome(failure),
			Remediation: authenticationRemediation(failure.Code),
			Error:       failure.Message,
		}, nil
	}
	workDir, targetErr := s.resolveDevProjectDir(input.Handle.ProjectDir)
	if targetErr != nil {
		failureOutcome, remediation := projectResolutionFailure(targetErr)
		return &mcp.CallToolResult{IsError: true}, RebuildOutput{
			Success: false, Outcome: failureOutcome, Remediation: remediation, Error: targetErr.Error(),
		}, nil
	}
	notifier := newRebuildProgressNotifier(req)
	notifier.notify(ctx, "Waiting for rebuild")
	waitRequest := devloop.WaitForRebuildRequest{
		Handle:         input.Handle,
		TimeoutSeconds: input.Timeout,
	}
	if notifier != nil {
		waitRequest.OnProgress = func(event devloop.RebuildProgressEvent) {
			notifier.notify(ctx, rebuildProgressEventMessage(event))
		}
	}
	rebuild, rebuildErr := s.devLoopRunner.WaitForRebuild(ctx, workDir, waitRequest)
	output := RebuildOutput{Success: rebuildErr == nil, Outcome: outcome.Completed(), Rebuild: rebuild}
	if rebuildErr != nil {
		output.Error = rebuildErr.Error()
		output.Outcome = outcome.Failed(string(rebuild.Build.State), output.Error, rebuild.Build.Retryable)
		notifier.notify(ctx, rebuildProgressMessage(rebuild, output.Error))
		return &mcp.CallToolResult{IsError: true}, output, nil
	}
	if !successfulRebuildStatus(rebuild.Status) {
		output.Success = false
		output.Error = strings.TrimSpace(rebuild.Error)
		if output.Error == "" {
			output.Error = fmt.Sprintf("rebuild ended with status %q", rebuild.Status)
		}
		output.Outcome = outcome.Failed(string(rebuild.Build.State), output.Error, rebuild.Build.Retryable)
		notifier.notify(ctx, rebuildProgressMessage(rebuild, output.Error))
		return &mcp.CallToolResult{IsError: true}, output, nil
	}
	notifier.notify(ctx, rebuildProgressMessage(rebuild, ""))
	return nil, output, nil
}

// newRebuildProgressNotifier returns a notifier when the client supplied a progress token.
//
// Parameters:
//   - req: MCP request to inspect.
//
// Returns:
//   - *rebuildProgressNotifier: Configured notifier, or nil when progress is unsupported.
func newRebuildProgressNotifier(req *mcp.CallToolRequest) *rebuildProgressNotifier {
	if req == nil || req.Params == nil || req.Session == nil {
		return nil
	}
	token := req.Params.GetProgressToken()
	if token == nil {
		return nil
	}
	return &rebuildProgressNotifier{request: req, token: token}
}

// notify emits one monotonically increasing MCP progress notification.
//
// Parameters:
//   - ctx: Notification context.
//   - message: User-visible progress message.
func (n *rebuildProgressNotifier) notify(ctx context.Context, message string) {
	if n == nil || n.request == nil || n.request.Session == nil || n.token == nil || strings.TrimSpace(message) == "" {
		return
	}
	n.progress++
	_ = n.request.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
		ProgressToken: n.token,
		Message:       strings.TrimSpace(message),
		Progress:      n.progress,
	})
}

// rebuildProgressEventMessage formats one child-process progress event for MCP.
//
// Parameters:
//   - event: Structured CLI rebuild progress event.
//
// Returns:
//   - string: Concise user-visible progress message.
func rebuildProgressEventMessage(event devloop.RebuildProgressEvent) string {
	if message := strings.TrimSpace(event.Message); message != "" {
		return message
	}
	state := strings.TrimSpace(string(event.State))
	phase := strings.TrimSpace(event.Phase)
	switch {
	case state != "" && phase != "":
		return fmt.Sprintf("Rebuild %s: %s", state, phase)
	case state != "":
		return "Rebuild " + state
	case strings.TrimSpace(event.Status) != "":
		return "Rebuild " + strings.TrimSpace(event.Status)
	default:
		return ""
	}
}

// successfulRebuildStatus reports whether a terminal CLI status is successful.
//
// Parameters:
//   - status: Terminal rebuild status.
//
// Returns:
//   - bool: Whether the status represents a successful rebuild.
func successfulRebuildStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "completed", "skipped":
		return true
	default:
		return false
	}
}

// rebuildProgressMessage formats the terminal rebuild notification.
//
// Parameters:
//   - rebuild: Terminal rebuild result.
//   - failure: Optional failure message.
//
// Returns:
//   - string: User-visible terminal progress message.
func rebuildProgressMessage(rebuild devloop.RebuildResult, failure string) string {
	if strings.TrimSpace(failure) != "" {
		return "Rebuild failed: " + strings.TrimSpace(failure)
	}
	if rebuild.DurationMs > 0 {
		return fmt.Sprintf("Rebuild %s in %dms", rebuild.Status, rebuild.DurationMs)
	}
	return "Rebuild " + strings.TrimSpace(rebuild.Status)
}

// rememberDelegatedDevLoop records the canonical context owned by this MCP process.
func (s *Server) rememberDelegatedDevLoop(workDir, contextName string) {
	s.hotReloadMu.Lock()
	defer s.hotReloadMu.Unlock()
	s.delegatedDevWorkDir = workDir
	s.delegatedDevContext = contextName
}

// clearDelegatedDevLoopIfMatches clears state only when it still identifies the stopped target.
//
// Parameters:
//   - workDir: Canonical project directory passed to the successful stop.
//   - contextName: Explicit context passed to the successful stop. Empty context
//     matches any remembered context for the same project.
//
// Edge cases:
//   - A concurrent start or a stop for another target leaves the newer remembered state intact.
func (s *Server) clearDelegatedDevLoopIfMatches(workDir, contextName string) {
	s.hotReloadMu.Lock()
	defer s.hotReloadMu.Unlock()
	contextName = strings.TrimSpace(contextName)
	if s.delegatedDevWorkDir != workDir {
		return
	}
	if contextName != "" && s.delegatedDevContext != contextName {
		return
	}
	s.delegatedDevWorkDir = ""
	s.delegatedDevContext = ""
}

// delegatedDevTarget resolves explicit inputs, then remembered context, then project detection.
func (s *Server) delegatedDevTarget(projectDir, contextName string) (string, string, error) {
	if strings.TrimSpace(projectDir) != "" {
		workDir, err := s.resolveDevProjectDir(projectDir)
		return workDir, strings.TrimSpace(contextName), err
	}

	s.hotReloadMu.Lock()
	rememberedWorkDir := s.delegatedDevWorkDir
	rememberedContext := s.delegatedDevContext
	s.hotReloadMu.Unlock()
	if strings.TrimSpace(rememberedWorkDir) != "" {
		if strings.TrimSpace(contextName) != "" {
			rememberedContext = strings.TrimSpace(contextName)
		}
		return rememberedWorkDir, rememberedContext, nil
	}
	workDir, err := s.resolveDevProjectDir("")
	return workDir, strings.TrimSpace(contextName), err
}
