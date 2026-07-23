package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/devloop"
)

type fakeDevLoopRunner struct {
	startResult      devloop.StartResult
	startErr         error
	statusResults    []devloop.StatusResult
	statusErr        error
	rebuildResult    devloop.RebuildResult
	rebuildProgress  []devloop.RebuildProgressEvent
	rebuildErr       error
	triggerHandle    devloop.RebuildHandle
	triggerErr       error
	waitResult       devloop.RebuildResult
	waitProgress     []devloop.RebuildProgressEvent
	waitErr          error
	stopResult       devloop.StopResult
	stopErr          error
	statusWorkDir    string
	statusContext    string
	waitWorkDir      string
	waitHandle       devloop.RebuildHandle
	stopWorkDir      string
	stopContext      string
	rebuildDelay     time.Duration
	startCallCount   int
	statusCallCount  int
	rebuildCallCount int
	triggerCallCount int
	waitCallCount    int
	stopCallCount    int
	mu               sync.Mutex
}

// Start returns the configured startup result.
func (r *fakeDevLoopRunner) Start(
	ctx context.Context,
	workDir string,
	request devloop.StartRequest,
) (devloop.StartResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.startCallCount++
	return r.startResult, r.startErr
}

// Status returns configured snapshots in order, then repeats the last snapshot.
func (r *fakeDevLoopRunner) Status(
	ctx context.Context,
	workDir string,
	contextName string,
) (devloop.StatusResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statusCallCount++
	r.statusWorkDir = workDir
	r.statusContext = contextName
	if r.statusErr != nil {
		return devloop.StatusResult{}, r.statusErr
	}
	if len(r.statusResults) == 0 {
		return devloop.StatusResult{}, nil
	}
	index := r.statusCallCount - 1
	if index >= len(r.statusResults) {
		index = len(r.statusResults) - 1
	}
	return r.statusResults[index], nil
}

// Rebuild returns the configured rebuild result.
func (r *fakeDevLoopRunner) Rebuild(
	ctx context.Context,
	workDir string,
	request devloop.RebuildRequest,
) (devloop.RebuildResult, error) {
	r.mu.Lock()
	r.rebuildCallCount++
	progress := append([]devloop.RebuildProgressEvent(nil), r.rebuildProgress...)
	r.mu.Unlock()
	if request.OnProgress != nil {
		for _, event := range progress {
			request.OnProgress(event)
		}
	}
	if r.rebuildDelay > 0 {
		timer := time.NewTimer(r.rebuildDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return devloop.RebuildResult{}, ctx.Err()
		case <-timer.C:
		}
	}
	return r.rebuildResult, r.rebuildErr
}

// TriggerRebuild returns the configured asynchronous rebuild handle.
func (r *fakeDevLoopRunner) TriggerRebuild(
	ctx context.Context,
	workDir string,
	request devloop.TriggerRebuildRequest,
) (devloop.RebuildHandle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.triggerCallCount++
	return r.triggerHandle, r.triggerErr
}

// WaitForRebuild returns the configured terminal result and progress.
func (r *fakeDevLoopRunner) WaitForRebuild(
	ctx context.Context,
	workDir string,
	request devloop.WaitForRebuildRequest,
) (devloop.RebuildResult, error) {
	r.mu.Lock()
	r.waitCallCount++
	r.waitWorkDir = workDir
	r.waitHandle = request.Handle
	progress := append([]devloop.RebuildProgressEvent(nil), r.waitProgress...)
	r.mu.Unlock()
	if request.OnProgress != nil {
		for _, event := range progress {
			request.OnProgress(event)
		}
	}
	return r.waitResult, r.waitErr
}

// Stop returns the configured stop result.
func (r *fakeDevLoopRunner) Stop(
	ctx context.Context,
	workDir string,
	contextName string,
) (devloop.StopResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopCallCount++
	r.stopWorkDir = workDir
	r.stopContext = contextName
	return r.stopResult, r.stopErr
}

func TestProjectResolutionHandlersReturnStructuredRemediation(t *testing.T) {
	missingRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(missingRoot, ".revyl"), 0o755); err != nil {
		t.Fatalf("create uninitialized .revyl: %v", err)
	}

	ambiguousRoot := t.TempDir()
	androidRoot := filepath.Join(ambiguousRoot, "apps", "android")
	iosRoot := filepath.Join(ambiguousRoot, "apps", "ios")
	writeDevLoopProjectAt(t, androidRoot)
	writeDevLoopProjectAt(t, iosRoot)

	type handlerResult struct {
		toolResult  *mcpsdk.CallToolResult
		success     bool
		outcomeCode string
		remediation *Remediation
	}
	handlers := []struct {
		name   string
		invoke func(*Server) (handlerResult, error)
	}{
		{
			name: "start_dev_loop",
			invoke: func(server *Server) (handlerResult, error) {
				toolResult, output, err := server.handleStartDevLoopCommand(
					context.Background(),
					nil,
					StartDevLoopInput{Remote: true},
				)
				return handlerResult{
					toolResult:  toolResult,
					success:     output.Success,
					outcomeCode: output.Outcome.OutcomeCode,
					remediation: output.Remediation,
				}, err
			},
		},
		{
			name: "get_dev_status",
			invoke: func(server *Server) (handlerResult, error) {
				toolResult, output, err := server.handleGetDevStatusCommand(
					context.Background(),
					nil,
					GetDevStatusInput{},
				)
				return handlerResult{
					toolResult:  toolResult,
					success:     output.Success,
					outcomeCode: output.Outcome.OutcomeCode,
					remediation: output.Remediation,
				}, err
			},
		},
		{
			name: "stop_dev_loop",
			invoke: func(server *Server) (handlerResult, error) {
				toolResult, output, err := server.handleStopDevLoopCommand(
					context.Background(),
					nil,
					CanonicalStopDevLoopInput{},
				)
				return handlerResult{
					toolResult:  toolResult,
					success:     output.Success,
					outcomeCode: output.Outcome.OutcomeCode,
					remediation: output.Remediation,
				}, err
			},
		},
		{
			name: "rebuild",
			invoke: func(server *Server) (handlerResult, error) {
				toolResult, output, err := server.handleRebuildCommand(
					context.Background(),
					nil,
					RebuildInput{},
				)
				return handlerResult{
					toolResult:  toolResult,
					success:     output.Success,
					outcomeCode: output.Outcome.OutcomeCode,
					remediation: output.Remediation,
				}, err
			},
		},
		{
			name: "wait_for_rebuild",
			invoke: func(server *Server) (handlerResult, error) {
				toolResult, output, err := server.handleWaitForRebuildCommand(
					context.Background(),
					nil,
					WaitForRebuildInput{},
				)
				return handlerResult{
					toolResult:  toolResult,
					success:     output.Success,
					outcomeCode: output.Outcome.OutcomeCode,
					remediation: output.Remediation,
				}, err
			},
		},
	}
	scenarios := []struct {
		name           string
		workDir        string
		outcomeCode    string
		actionKind     RemediationActionKind
		command        string
		candidateRoots []string
	}{
		{
			name:        "project not initialized",
			workDir:     missingRoot,
			outcomeCode: "project_not_initialized",
			actionKind:  remediationActionCommand,
			command:     "revyl init --non-interactive",
		},
		{
			name:           "project ambiguous",
			workDir:        ambiguousRoot,
			outcomeCode:    "project_ambiguous",
			actionKind:     remediationActionSelectProjectDir,
			candidateRoots: []string{androidRoot, iosRoot},
		},
	}

	for _, scenario := range scenarios {
		for _, handler := range handlers {
			t.Run(scenario.name+"/"+handler.name, func(t *testing.T) {
				runner := &fakeDevLoopRunner{}
				server := &Server{workDir: scenario.workDir, devLoopRunner: runner}
				result, err := handler.invoke(server)
				if err != nil {
					t.Fatalf("%s(): %v", handler.name, err)
				}
				if result.success || result.toolResult == nil || !result.toolResult.IsError {
					t.Fatalf("%s result = %+v, want MCP error", handler.name, result)
				}
				if result.outcomeCode != scenario.outcomeCode {
					t.Fatalf("%s outcome code = %q, want %q", handler.name, result.outcomeCode, scenario.outcomeCode)
				}
				if result.remediation == nil {
					t.Fatalf("%s remediation is nil", handler.name)
				}
				if result.remediation.WorkingDirectory != scenario.workDir {
					t.Fatalf(
						"%s working directory = %q, want %q",
						handler.name,
						result.remediation.WorkingDirectory,
						scenario.workDir,
					)
				}
				if result.remediation.ActionKind != scenario.actionKind {
					t.Fatalf(
						"%s action kind = %q, want %q",
						handler.name,
						result.remediation.ActionKind,
						scenario.actionKind,
					)
				}
				if result.remediation.Command != scenario.command {
					t.Fatalf("%s command = %q, want %q", handler.name, result.remediation.Command, scenario.command)
				}
				if result.remediation.ConfigPath != "" {
					t.Fatalf("%s config path = %q, want empty", handler.name, result.remediation.ConfigPath)
				}
				if len(result.remediation.CandidateRoots) != len(scenario.candidateRoots) {
					t.Fatalf(
						"%s candidate roots = %v, want %v",
						handler.name,
						result.remediation.CandidateRoots,
						scenario.candidateRoots,
					)
				}
				for index, candidateRoot := range scenario.candidateRoots {
					if result.remediation.CandidateRoots[index] != candidateRoot {
						t.Fatalf(
							"%s candidate roots = %v, want %v",
							handler.name,
							result.remediation.CandidateRoots,
							scenario.candidateRoots,
						)
					}
				}
				runner.mu.Lock()
				startCalls := runner.startCallCount
				runner.mu.Unlock()
				if startCalls != 0 {
					t.Fatalf("%s invoked the start runner %d times on setup failure", handler.name, startCalls)
				}
			})
		}
	}
}

// TestMalformedProjectConfigBlocksSetupSensitiveRunners verifies start and rebuild fail before runner work.
func TestMalformedProjectConfigBlocksSetupSensitiveRunners(t *testing.T) {
	projectDir := writeInvalidDevLoopProject(t)
	configPath := filepath.Join(projectDir, ".revyl", "config.yaml")
	runner := &fakeDevLoopRunner{}
	server := &Server{workDir: projectDir, devLoopRunner: runner}

	startResult, startOutput, err := server.handleStartDevLoopCommand(
		context.Background(),
		nil,
		StartDevLoopInput{Remote: true},
	)
	if err != nil {
		t.Fatalf("handleStartDevLoopCommand(): %v", err)
	}
	if startResult == nil || !startResult.IsError || startOutput.Success {
		t.Fatalf("start result = %+v, output = %+v, want project error", startResult, startOutput)
	}
	if startOutput.Outcome.OutcomeCode != "project_invalid" ||
		startOutput.Remediation == nil ||
		startOutput.Remediation.ConfigPath != configPath ||
		startOutput.Remediation.WorkingDirectory != projectDir {
		t.Fatalf("start invalid remediation = %+v, outcome = %+v", startOutput.Remediation, startOutput.Outcome)
	}

	rebuildResult, rebuildOutput, err := server.handleRebuildCommand(
		context.Background(),
		nil,
		RebuildInput{Context: "existing"},
	)
	if err != nil {
		t.Fatalf("handleRebuildCommand(): %v", err)
	}
	if rebuildResult == nil || !rebuildResult.IsError || rebuildOutput.Success {
		t.Fatalf("rebuild result = %+v, output = %+v, want project error", rebuildResult, rebuildOutput)
	}
	if rebuildOutput.Outcome.OutcomeCode != "project_invalid" ||
		rebuildOutput.Remediation == nil ||
		rebuildOutput.Remediation.ConfigPath != configPath {
		t.Fatalf("rebuild invalid remediation = %+v, outcome = %+v", rebuildOutput.Remediation, rebuildOutput.Outcome)
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.startCallCount != 0 || runner.triggerCallCount != 0 {
		t.Fatalf(
			"setup-sensitive runner calls: start=%d trigger=%d, want zero",
			runner.startCallCount,
			runner.triggerCallCount,
		)
	}
}

// TestMalformedProjectConfigAllowsExistingOperations verifies cleanup and observation survive config damage.
func TestMalformedProjectConfigAllowsExistingOperations(t *testing.T) {
	projectDir := writeInvalidDevLoopProject(t)
	const contextName = "existing"
	runner := &fakeDevLoopRunner{
		statusResults: []devloop.StatusResult{{
			Running: true,
			Context: contextName,
		}},
		waitResult: devloop.RebuildResult{
			Status: "success",
		},
		stopResult: devloop.StopResult{
			Stopped: true,
			Context: contextName,
		},
	}
	server := &Server{workDir: projectDir, devLoopRunner: runner}

	statusResult, statusOutput, err := server.handleGetDevStatusCommand(
		context.Background(),
		nil,
		GetDevStatusInput{Context: contextName},
	)
	if err != nil {
		t.Fatalf("handleGetDevStatusCommand(): %v", err)
	}
	if statusResult != nil || !statusOutput.Success || !statusOutput.Result.Running {
		t.Fatalf("status result = %+v, output = %+v, want runner success", statusResult, statusOutput)
	}

	handle := devloop.RebuildHandle{
		ProjectDir: projectDir,
		Context:    contextName,
	}
	waitResult, waitOutput, err := server.handleWaitForRebuildCommand(
		context.Background(),
		nil,
		WaitForRebuildInput{Handle: handle},
	)
	if err != nil {
		t.Fatalf("handleWaitForRebuildCommand(): %v", err)
	}
	if waitResult != nil || !waitOutput.Success {
		t.Fatalf("wait result = %+v, output = %+v, want runner success", waitResult, waitOutput)
	}

	stopResult, stopOutput, err := server.handleStopDevLoopCommand(
		context.Background(),
		nil,
		CanonicalStopDevLoopInput{Context: contextName},
	)
	if err != nil {
		t.Fatalf("handleStopDevLoopCommand(): %v", err)
	}
	if stopResult != nil || !stopOutput.Success || !stopOutput.Result.Stopped {
		t.Fatalf("stop result = %+v, output = %+v, want runner success", stopResult, stopOutput)
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.statusCallCount != 1 ||
		runner.waitCallCount != 1 ||
		runner.stopCallCount != 1 {
		t.Fatalf(
			"existing-operation runner calls: status=%d wait=%d stop=%d, want one each",
			runner.statusCallCount,
			runner.waitCallCount,
			runner.stopCallCount,
		)
	}
	if runner.statusWorkDir != projectDir ||
		runner.waitWorkDir != projectDir ||
		runner.stopWorkDir != projectDir {
		t.Fatalf(
			"existing-operation work dirs: status=%q wait=%q stop=%q, want %q",
			runner.statusWorkDir,
			runner.waitWorkDir,
			runner.stopWorkDir,
			projectDir,
		)
	}
	if runner.statusContext != contextName || runner.stopContext != contextName {
		t.Fatalf(
			"existing-operation contexts: status=%q stop=%q, want %q",
			runner.statusContext,
			runner.stopContext,
			contextName,
		)
	}
}

func TestHandleStartDevLoopCommandUsesCanonicalRunner(t *testing.T) {
	projectDir := writeDevLoopTestProject(t)
	runner := &fakeDevLoopRunner{startResult: devloop.StartResult{
		Context:      "default",
		SessionIndex: 4,
		ViewerURL:    "https://viewer.example",
	}}
	server := &Server{workDir: projectDir, devLoopRunner: runner}

	_, output, err := server.handleStartDevLoopCommand(
		context.Background(),
		nil,
		StartDevLoopInput{Remote: true},
	)
	if err != nil {
		t.Fatalf("handleStartDevLoopCommand(): %v", err)
	}
	if !output.Success || output.Result.ViewerURL != "https://viewer.example" {
		t.Fatalf("start output = %+v", output)
	}
	if server.delegatedDevWorkDir != projectDir || server.delegatedDevContext != "default" {
		t.Fatalf("delegated target = %q, %q", server.delegatedDevWorkDir, server.delegatedDevContext)
	}
}

func TestHandleStartDevLoopCommandDisablesDelegatedSessionIdleTimer(t *testing.T) {
	projectDir := writeDevLoopTestProject(t)
	sessionManager := NewDeviceSessionManager(
		api.NewClientWithBaseURL("test-key", "http://127.0.0.1"),
		projectDir,
	)
	sessionManager.sessions[3] = &DeviceSession{
		Index:       3,
		SessionID:   "delegated-session",
		IdleTimeout: 40 * time.Millisecond,
	}
	sessionManager.activeIndex = 3
	runner := &fakeDevLoopRunner{startResult: devloop.StartResult{
		Context:      "default",
		SessionID:    "delegated-session",
		SessionIndex: 99,
		ViewerURL:    "https://viewer.example",
	}}
	server := &Server{
		workDir:       projectDir,
		devLoopRunner: runner,
		sessionMgr:    sessionManager,
	}

	_, output, err := server.handleStartDevLoopCommand(
		context.Background(),
		nil,
		StartDevLoopInput{Remote: true},
	)
	if err != nil {
		t.Fatalf("handleStartDevLoopCommand(): %v", err)
	}
	if !output.Success {
		t.Fatalf("start output = %+v", output)
	}
	if output.Result.SessionIndex != 3 ||
		output.Outcome.SessionIndex == nil ||
		*output.Outcome.SessionIndex != 3 {
		t.Fatalf("start session index = %+v, want MCP-local index 3", output)
	}

	sessionManager.ResetIdleTimer(3)
	if sessionManager.GetSession(3) == nil {
		t.Fatal("delegated session was removed by MCP idle enforcement")
	}
	if !sessionManager.idleTimerDisabled[3] {
		t.Fatal("delegated session idle enforcement was not disabled")
	}
	if len(sessionManager.idleTimers) != 0 {
		t.Fatalf("delegated session idle timers = %d, want 0", len(sessionManager.idleTimers))
	}
}

// TestHandleStartDevLoopCommandFailsWhenSessionAttachFails verifies startup never exposes a foreign process index.
func TestHandleStartDevLoopCommandFailsWhenSessionAttachFails(t *testing.T) {
	projectDir := writeDevLoopTestProject(t)
	runner := &fakeDevLoopRunner{startResult: devloop.StartResult{
		Context:      "default",
		SessionID:    "unattached-session",
		SessionIndex: 42,
		ViewerURL:    "https://viewer.example",
	}}
	server := &Server{
		workDir:       projectDir,
		devLoopRunner: runner,
		sessionMgr:    NewDeviceSessionManager(nil, projectDir),
	}

	toolResult, output, err := server.handleStartDevLoopCommand(
		context.Background(),
		nil,
		StartDevLoopInput{Remote: true},
	)
	if err != nil {
		t.Fatalf("handleStartDevLoopCommand(): %v", err)
	}
	if toolResult == nil || !toolResult.IsError || output.Success {
		t.Fatalf("start result = %+v, output = %+v, want MCP error", toolResult, output)
	}
	if output.Outcome.OutcomeCode != "dev_loop_session_attach_failed" ||
		output.Outcome.Retryable ||
		output.Outcome.SessionID != "unattached-session" ||
		output.Outcome.ViewerURL != "https://viewer.example" {
		t.Fatalf("start outcome = %+v", output.Outcome)
	}
	if output.Result.SessionIndex != -1 || output.Outcome.SessionIndex != nil {
		t.Fatalf("failed start exposed session index: %+v", output)
	}
	if !strings.Contains(output.Error, "no API client configured") ||
		!strings.Contains(output.Error, "stop_dev_loop") {
		t.Fatalf("start error = %q, want attach cause and cleanup action", output.Error)
	}
	if server.delegatedDevWorkDir != projectDir || server.delegatedDevContext != "default" {
		t.Fatalf(
			"delegated target = %q, %q, want retained target for cleanup",
			server.delegatedDevWorkDir,
			server.delegatedDevContext,
		)
	}
}

func TestHandleStartDevLoopCompatReturnsFlatOutput(t *testing.T) {
	projectDir := writeDevLoopTestProject(t)
	runner := &fakeDevLoopRunner{startResult: devloop.StartResult{
		Context:      "default",
		SessionIndex: 7,
		ViewerURL:    "https://viewer.example",
	}}
	server := &Server{workDir: projectDir, devLoopRunner: runner}

	_, output, err := server.handleStartDevLoopCompat(
		context.Background(),
		nil,
		StartDevLoopInput{Remote: true},
	)
	if err != nil {
		t.Fatalf("handleStartDevLoopCompat(): %v", err)
	}
	if !output.Success || output.SessionIndex != 7 || output.ViewerURL != "https://viewer.example" {
		t.Fatalf("compat start output = %+v", output)
	}
}

func TestHandleStartDevLoopCompatPreservesMCPError(t *testing.T) {
	projectDir := writeDevLoopTestProject(t)
	runner := &fakeDevLoopRunner{startErr: errors.New("start failed")}
	server := &Server{workDir: projectDir, devLoopRunner: runner}

	toolResult, output, err := server.handleStartDevLoopCompat(
		context.Background(),
		nil,
		StartDevLoopInput{Remote: true},
	)

	if err != nil {
		t.Fatalf("handleStartDevLoopCompat(): %v", err)
	}
	if output.Success || toolResult == nil || !toolResult.IsError {
		t.Fatalf("compat start result = %+v, output = %+v", toolResult, output)
	}
}

func TestHandleStartDevLoopCommandOmitsUnreadyRemoteBaseline(t *testing.T) {
	projectDir := writeDevLoopTestProject(t)
	runner := &fakeDevLoopRunner{
		startResult: devloop.StartResult{Context: "default", ViewerURL: "https://viewer.example"},
		statusResults: []devloop.StatusResult{{
			Build: devloop.BuildStatus{State: devloop.BuildStateFailed},
		}},
	}
	server := &Server{workDir: projectDir, devLoopRunner: runner}

	_, output, err := server.handleStartDevLoopCommand(
		context.Background(),
		nil,
		StartDevLoopInput{Remote: true, SeedLatest: true},
	)
	if err != nil {
		t.Fatalf("handleStartDevLoopCommand(): %v", err)
	}
	if !output.Success || output.Screenshot != nil {
		t.Fatalf("unready start output = %+v", output)
	}
}

func TestHandleGetDevStatusCommandUsesCanonicalRunner(t *testing.T) {
	projectDir := writeDevLoopTestProject(t)
	runner := &fakeDevLoopRunner{statusResults: []devloop.StatusResult{{
		Running:     true,
		Context:     "default",
		SessionID:   "session-1",
		ViewerURL:   "https://viewer.example",
		RemoteJobID: "job-1",
		Build: devloop.BuildStatus{
			State:       devloop.BuildStateBuilding,
			RemoteJobID: "job-1",
		},
	}}}
	server := &Server{
		workDir:             projectDir,
		devLoopRunner:       runner,
		delegatedDevWorkDir: projectDir,
		delegatedDevContext: "default",
	}

	_, output, err := server.handleGetDevStatusCommand(context.Background(), nil, GetDevStatusInput{})
	if err != nil {
		t.Fatalf("handleGetDevStatusCommand(): %v", err)
	}
	if !output.Success || output.Outcome.BuildJobID != "job-1" {
		t.Fatalf("status output = %+v", output)
	}
}

func TestHandleRebuildCommandUsesCanonicalRunner(t *testing.T) {
	projectDir := writeDevLoopTestProject(t)
	handle := devloop.RebuildHandle{
		ProjectDir:       projectDir,
		Context:          "default",
		BaselineSequence: 2,
		ExpectedSequence: 3,
		ProcessID:        42,
	}
	runner := &fakeDevLoopRunner{triggerHandle: handle}
	server := &Server{
		workDir:             projectDir,
		devLoopRunner:       runner,
		delegatedDevWorkDir: projectDir,
		delegatedDevContext: "default",
	}

	_, output, err := server.handleRebuildCommand(
		context.Background(),
		nil,
		RebuildInput{},
	)
	if err != nil {
		t.Fatalf("handleRebuildCommand(): %v", err)
	}
	if !output.Success || output.Handle != handle || output.Outcome.OperationStatus != "triggered" {
		t.Fatalf("rebuild output = %+v", output)
	}
}

func TestWaitForRebuildToolStreamsChildProgressWithoutStatusPolling(t *testing.T) {
	projectDir := writeDevLoopTestProject(t)
	handle := devloop.RebuildHandle{
		ProjectDir:           projectDir,
		Context:              "default",
		BaselineSequence:     1,
		ExpectedSequence:     2,
		ProcessID:            42,
		ProcessStartedAtNano: 1784692688213490950,
		ProcessGeneration:    "1784692688213490950",
	}
	runner := &fakeDevLoopRunner{
		triggerHandle: handle,
		waitProgress: []devloop.RebuildProgressEvent{
			{Sequence: 2, Status: "running", State: devloop.BuildStateQueued, Phase: "remote_queue"},
			{Sequence: 2, Status: "running", State: devloop.BuildStateQueued, Message: "Remote build queued"},
			{Sequence: 2, Status: "running", State: devloop.BuildStateBuilding, Phase: "compile"},
			{Sequence: 2, Status: "running", State: devloop.BuildStateBuilding, Message: "Compiling app"},
		},
		waitResult: devloop.RebuildResult{
			Status:         "success",
			DurationMs:     1200,
			RemoteJobID:    "job-1",
			BuiltVersionID: "version-1",
			Build: devloop.BuildStatus{
				State:             devloop.BuildStateSuccess,
				RemoteJobID:       "job-1",
				BuiltVersion:      "version-1",
				FreshBuildApplied: true,
			},
		},
	}

	sdkServer := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "revyl-test", Version: "test"}, nil)
	server := &Server{
		mcpServer:           sdkServer,
		workDir:             projectDir,
		devLoopRunner:       runner,
		delegatedDevWorkDir: projectDir,
		delegatedDevContext: "default",
	}
	server.registerDevLoopTools()
	server.registerRebuildTool()

	var messagesMu sync.Mutex
	var messages []string
	terminalProgressReceived := make(chan struct{}, 1)
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "test"}, &mcpsdk.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcpsdk.ProgressNotificationClientRequest) {
			messagesMu.Lock()
			messages = append(messages, req.Params.Message)
			messagesMu.Unlock()
			if req.Params.Message == "Rebuild success in 1200ms" {
				select {
				case terminalProgressReceived <- struct{}{}:
				default:
				}
			}
		},
	})
	ctx := context.Background()
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := sdkServer.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server Connect(): %v", err)
	}
	t.Cleanup(func() {
		_ = serverSession.Close()
	})
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect(): %v", err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
	})

	triggerResult, err := clientSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "rebuild",
		Arguments: map[string]any{"project_dir": projectDir},
	})
	if err != nil {
		t.Fatalf("CallTool(rebuild): %v", err)
	}
	if triggerResult.IsError {
		t.Fatalf("CallTool(rebuild) result = %+v", triggerResult)
	}
	encodedTriggerResult, err := json.Marshal(triggerResult.StructuredContent)
	if err != nil {
		t.Fatalf("marshal rebuild structured result: %v", err)
	}
	var javascriptTriggerOutput map[string]any
	if err := json.Unmarshal(encodedTriggerResult, &javascriptTriggerOutput); err != nil {
		t.Fatalf("decode rebuild result as JavaScript object: %v", err)
	}
	javascriptHandle, ok := javascriptTriggerOutput["handle"]
	if !ok {
		t.Fatalf("rebuild structured result = %#v, missing handle", javascriptTriggerOutput)
	}
	runner.mu.Lock()
	triggerCallsBeforeWait := runner.triggerCallCount
	waitCallsBeforeWait := runner.waitCallCount
	runner.mu.Unlock()
	if triggerCallsBeforeWait != 1 || waitCallsBeforeWait != 0 {
		t.Fatalf("rebuild did not return before wait: trigger=%d wait=%d", triggerCallsBeforeWait, waitCallsBeforeWait)
	}
	statusResult, err := clientSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "get_dev_status",
		Arguments: map[string]any{"project_dir": projectDir},
	})
	if err != nil {
		t.Fatalf("CallTool(get_dev_status between trigger and wait): %v", err)
	}
	if statusResult.IsError {
		t.Fatalf("CallTool(get_dev_status) result = %+v", statusResult)
	}
	result, err := clientSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "wait_for_rebuild",
		Arguments: map[string]any{
			"handle":  javascriptHandle,
			"timeout": 60,
		},
		Meta: mcpsdk.Meta{"progressToken": "rebuild-1"},
	})
	if err != nil {
		t.Fatalf("CallTool(wait_for_rebuild): %v", err)
	}
	if result.IsError {
		t.Fatalf("CallTool(wait_for_rebuild) result = %+v", result)
	}
	select {
	case <-terminalProgressReceived:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for terminal rebuild progress")
	}

	messagesMu.Lock()
	gotMessages := append([]string(nil), messages...)
	messagesMu.Unlock()
	for _, want := range []string{
		"Waiting for rebuild",
		"Rebuild queued: remote_queue",
		"Remote build queued",
		"Rebuild building: compile",
		"Compiling app",
		"Rebuild success in 1200ms",
	} {
		if !containsString(gotMessages, want) {
			t.Fatalf("progress messages = %#v, missing %q", gotMessages, want)
		}
	}
	if countString(gotMessages, "Compiling app") != 1 {
		t.Fatalf("progress messages = %#v, duplicate compile log", gotMessages)
	}

	runner.mu.Lock()
	statusCalls := runner.statusCallCount
	rebuildCalls := runner.rebuildCallCount
	triggerCalls := runner.triggerCallCount
	waitCalls := runner.waitCallCount
	waitHandle := runner.waitHandle
	runner.mu.Unlock()
	if statusCalls != 1 || rebuildCalls != 0 || triggerCalls != 1 || waitCalls != 1 {
		t.Fatalf(
			"runner calls after wait: status=%d rebuild=%d trigger=%d wait=%d",
			statusCalls,
			rebuildCalls,
			triggerCalls,
			waitCalls,
		)
	}
	if waitHandle.ProcessStartedAtNano != 1784692688213491000 {
		t.Fatalf(
			"JavaScript-round-tripped process_started_at_nano = %d, want rounded value",
			waitHandle.ProcessStartedAtNano,
		)
	}
	if waitHandle.ProcessGeneration != handle.ProcessGeneration {
		t.Fatalf(
			"JavaScript-round-tripped process_generation = %q, want %q",
			waitHandle.ProcessGeneration,
			handle.ProcessGeneration,
		)
	}

	messagesMu.Lock()
	messageCount := len(messages)
	messagesMu.Unlock()
	if _, err := clientSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "wait_for_rebuild",
		Arguments: map[string]any{
			"handle": javascriptHandle,
		},
	}); err != nil {
		t.Fatalf("CallTool(wait_for_rebuild without progress token): %v", err)
	}
	messagesMu.Lock()
	defer messagesMu.Unlock()
	if len(messages) != messageCount {
		t.Fatalf("progress without token: before=%d after=%d", messageCount, len(messages))
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.statusCallCount != 1 || runner.rebuildCallCount != 0 ||
		runner.triggerCallCount != 1 || runner.waitCallCount != 2 {
		t.Fatalf(
			"runner calls after no-token wait: status=%d rebuild=%d trigger=%d wait=%d",
			runner.statusCallCount,
			runner.rebuildCallCount,
			runner.triggerCallCount,
			runner.waitCallCount,
		)
	}
}

// containsString reports whether values contains target.
func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// countString returns the number of exact target matches.
func countString(values []string, target string) int {
	count := 0
	for _, value := range values {
		if value == target {
			count++
		}
	}
	return count
}

func TestHandleStopDevLoopCommandUsesCanonicalRunner(t *testing.T) {
	projectDir := writeDevLoopTestProject(t)
	runner := &fakeDevLoopRunner{stopResult: devloop.StopResult{
		Stopped: true,
		Context: "default",
	}}
	server := &Server{
		workDir:             projectDir,
		devLoopRunner:       runner,
		delegatedDevWorkDir: projectDir,
		delegatedDevContext: "default",
	}

	_, output, err := server.handleStopDevLoopCommand(context.Background(), nil, CanonicalStopDevLoopInput{})
	if err != nil {
		t.Fatalf("handleStopDevLoopCommand(): %v", err)
	}
	if !output.Success || !output.Result.Stopped {
		t.Fatalf("stop output = %+v", output)
	}
	if server.delegatedDevWorkDir != "" || server.delegatedDevContext != "" {
		t.Fatalf("delegated target was not cleared")
	}
}

func TestHandleStopDevLoopCommandClearsMatchingExplicitContext(t *testing.T) {
	projectDir := writeDevLoopTestProject(t)
	runner := &fakeDevLoopRunner{stopResult: devloop.StopResult{Stopped: true, Context: "default"}}
	server := &Server{
		workDir:             projectDir,
		devLoopRunner:       runner,
		delegatedDevWorkDir: projectDir,
		delegatedDevContext: "default",
	}

	_, output, err := server.handleStopDevLoopCommand(
		context.Background(),
		nil,
		CanonicalStopDevLoopInput{Context: "default"},
	)
	if err != nil {
		t.Fatalf("handleStopDevLoopCommand(): %v", err)
	}
	if !output.Success || server.delegatedDevWorkDir != "" || server.delegatedDevContext != "" {
		t.Fatalf("matching stop output = %+v, delegated target = %q, %q", output, server.delegatedDevWorkDir, server.delegatedDevContext)
	}
}

func TestHandleStopDevLoopCommandClearsMatchingExplicitProjectWithoutContext(t *testing.T) {
	projectDir := writeDevLoopTestProject(t)
	runner := &fakeDevLoopRunner{stopResult: devloop.StopResult{Stopped: true, Context: "default"}}
	server := &Server{
		workDir:             projectDir,
		devLoopRunner:       runner,
		delegatedDevWorkDir: projectDir,
		delegatedDevContext: "default",
	}

	_, output, err := server.handleStopDevLoopCommand(
		context.Background(),
		nil,
		CanonicalStopDevLoopInput{ProjectDir: projectDir},
	)
	if err != nil {
		t.Fatalf("handleStopDevLoopCommand(): %v", err)
	}
	if !output.Success || runner.stopWorkDir != projectDir || runner.stopContext != "" {
		t.Fatalf("explicit-project stop output = %+v, runner target = %q, %q", output, runner.stopWorkDir, runner.stopContext)
	}
	if server.delegatedDevWorkDir != "" || server.delegatedDevContext != "" {
		t.Fatalf("delegated target = %q, %q, want cleared", server.delegatedDevWorkDir, server.delegatedDevContext)
	}
}

func TestHandleStopDevLoopCommandPreservesDifferentExplicitContext(t *testing.T) {
	projectDir := writeDevLoopTestProject(t)
	runner := &fakeDevLoopRunner{stopResult: devloop.StopResult{Stopped: true, Context: "other"}}
	server := &Server{
		workDir:             projectDir,
		devLoopRunner:       runner,
		delegatedDevWorkDir: projectDir,
		delegatedDevContext: "default",
	}

	_, output, err := server.handleStopDevLoopCommand(
		context.Background(),
		nil,
		CanonicalStopDevLoopInput{Context: "other"},
	)
	if err != nil {
		t.Fatalf("handleStopDevLoopCommand(): %v", err)
	}
	if !output.Success || runner.stopWorkDir != projectDir || runner.stopContext != "other" {
		t.Fatalf("different-context stop output = %+v, runner target = %q, %q", output, runner.stopWorkDir, runner.stopContext)
	}
	if server.delegatedDevWorkDir != projectDir || server.delegatedDevContext != "default" {
		t.Fatalf("remembered target = %q, %q, want original target", server.delegatedDevWorkDir, server.delegatedDevContext)
	}
}

func TestHandleStopDevLoopCommandPreservesDifferentExplicitProject(t *testing.T) {
	projectDir := writeDevLoopTestProject(t)
	otherProjectDir := writeDevLoopTestProject(t)
	runner := &fakeDevLoopRunner{stopResult: devloop.StopResult{Stopped: true, Context: "other"}}
	server := &Server{
		workDir:             projectDir,
		devLoopRunner:       runner,
		delegatedDevWorkDir: projectDir,
		delegatedDevContext: "default",
	}

	_, output, err := server.handleStopDevLoopCommand(
		context.Background(),
		nil,
		CanonicalStopDevLoopInput{ProjectDir: otherProjectDir, Context: "other"},
	)
	if err != nil {
		t.Fatalf("handleStopDevLoopCommand(): %v", err)
	}
	if !output.Success || runner.stopWorkDir != otherProjectDir || runner.stopContext != "other" {
		t.Fatalf("different-project stop output = %+v, runner target = %q, %q", output, runner.stopWorkDir, runner.stopContext)
	}
	if server.delegatedDevWorkDir != projectDir || server.delegatedDevContext != "default" {
		t.Fatalf("remembered target = %q, %q, want original target", server.delegatedDevWorkDir, server.delegatedDevContext)
	}
}

func TestHandleStopDevLoopCompatReturnsFlatOutput(t *testing.T) {
	projectDir := writeDevLoopTestProject(t)
	runner := &fakeDevLoopRunner{stopResult: devloop.StopResult{Stopped: true, Context: "default"}}
	server := &Server{
		workDir:             projectDir,
		devLoopRunner:       runner,
		delegatedDevWorkDir: projectDir,
		delegatedDevContext: "default",
	}

	_, output, err := server.handleStopDevLoopCompat(context.Background(), nil, StopDevLoopInput{})
	if err != nil {
		t.Fatalf("handleStopDevLoopCompat(): %v", err)
	}
	if !output.Success || output.Message != "Dev loop stopped" {
		t.Fatalf("compat stop output = %+v", output)
	}
}

func TestHandleStopDevLoopCompatPreservesMCPError(t *testing.T) {
	projectDir := writeDevLoopTestProject(t)
	runner := &fakeDevLoopRunner{stopErr: errors.New("stop failed")}
	server := &Server{
		workDir:             projectDir,
		devLoopRunner:       runner,
		delegatedDevWorkDir: projectDir,
		delegatedDevContext: "default",
	}

	toolResult, output, err := server.handleStopDevLoopCompat(
		context.Background(),
		nil,
		StopDevLoopInput{},
	)

	if err != nil {
		t.Fatalf("handleStopDevLoopCompat(): %v", err)
	}
	if output.Success || toolResult == nil || !toolResult.IsError {
		t.Fatalf("compat stop result = %+v, output = %+v", toolResult, output)
	}
}

func TestWaitForDevAppReadyWaitsForInstalledSeed(t *testing.T) {
	runner := &fakeDevLoopRunner{statusResults: []devloop.StatusResult{
		{Build: devloop.BuildStatus{State: devloop.BuildStatePreparing}},
		{
			InstalledSeed: true,
			SeededVersion: "1.2.3",
			Build: devloop.BuildStatus{
				State:         devloop.BuildStateBuilding,
				SeededVersion: "1.2.3",
			},
		},
	}}

	status, ready := waitForDevAppReady(
		context.Background(),
		runner,
		t.TempDir(),
		"default",
		100*time.Millisecond,
		time.Millisecond,
	)
	if !ready || !status.InstalledSeed || runner.statusCallCount != 2 {
		t.Fatalf("readiness = %+v, %t after %d calls", status, ready, runner.statusCallCount)
	}
}

// writeDevLoopTestProject creates the minimum project marker used by MCP handlers.
func writeDevLoopTestProject(t *testing.T) string {
	t.Helper()
	projectDir := t.TempDir()
	writeDevLoopProjectAt(t, projectDir)
	return projectDir
}

// writeDevLoopProjectAt creates the minimum initialized project marker used by MCP handlers.
func writeDevLoopProjectAt(t *testing.T, projectDir string) {
	t.Helper()
	configDir := filepath.Join(projectDir, ".revyl")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create .revyl: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("project:\n  name: fixture\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// writeInvalidDevLoopProject creates a project whose config cannot be parsed.
//
// Parameters:
//   - t: Active test.
//
// Returns:
//   - string: Project directory containing the malformed configuration.
func writeInvalidDevLoopProject(t *testing.T) string {
	t.Helper()
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".revyl")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create invalid .revyl: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("project: [invalid"), 0o600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}
	return projectDir
}
