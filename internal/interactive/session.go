// Package interactive provides the interactive test creation mode for the CLI.
//
// This package enables users to build and edit tests step-by-step with real-time
// device feedback. It connects to the worker WebSocket server to execute steps
// and receive results in real-time.
package interactive

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

// SessionState represents the current state of an interactive session.
type SessionState string

const (
	// StateInitializing indicates the session is starting up.
	StateInitializing SessionState = "initializing"

	// StateConnecting indicates the session is connecting to the worker.
	StateConnecting SessionState = "connecting"

	// StateReady indicates the session is ready for commands.
	StateReady SessionState = "ready"

	// StateExecuting indicates a step is currently executing.
	StateExecuting SessionState = "executing"

	// StateStopping indicates the session is shutting down.
	StateStopping SessionState = "stopping"

	// StateStopped indicates the session has stopped.
	StateStopped SessionState = "stopped"

	// StateError indicates the session encountered an error.
	StateError SessionState = "error"
)

// StepRecord represents a recorded step in the interactive session.
type StepRecord struct {
	// ID is the unique identifier for this step.
	ID string

	// BlockType is the block type (instructions, manual, validation).
	BlockType string

	// StepType is the step type (instruction, validation, wait, navigate, etc.).
	StepType string

	// Instruction is the natural language instruction.
	Instruction string

	// Index is the step index in the sequence.
	Index int

	// Success indicates if the step passed (nil if not yet executed).
	Success *bool

	// Error contains the error message if the step failed.
	Error string

	// Duration is the execution time in milliseconds.
	Duration int64

	// ActionsTaken contains the actions performed during execution.
	ActionsTaken []api.ActionTaken

	// ExecutedAt is when the step was executed.
	ExecutedAt time.Time
}

// SessionConfig contains configuration for an interactive session.
type SessionConfig struct {
	// TestID is the test ID to associate with this session.
	// Required for non-simulation mode.
	TestID string

	// TestName is the name of the test being created/edited.
	TestName string

	// Platform is the target platform (ios or android).
	Platform string

	// IsSimulation enables simulation mode (streaming without test execution).
	IsSimulation bool

	// APIKey is the API key for authentication.
	APIKey string

	// DevMode enables development mode (uses localhost URLs).
	DevMode bool

	// HotReloadURL is the deep link URL for hot reload mode.
	// When provided, the app will be launched via this URL.
	HotReloadURL string

	// StepTimeout is the maximum time to wait for step execution.
	StepTimeout time.Duration

	// ConnectionTimeout is the maximum time to wait for worker connection.
	ConnectionTimeout time.Duration
}

// Session manages an interactive test creation session.
// It handles device lifecycle, WebSocket communication, and step execution.
type Session struct {
	// config contains the session configuration.
	config SessionConfig

	// client is the API client for backend communication.
	client *api.Client

	// wsClient is the WebSocket client for worker communication.
	wsClient *api.WorkerWSClient

	// workflowRunID is the workflow run identifier.
	workflowRunID string

	// workerWSURL is the worker WebSocket URL.
	workerWSURL string

	// state is the current session state.
	state SessionState

	// steps contains the recorded steps.
	steps []StepRecord

	// currentStepIndex is the index of the next step to add.
	currentStepIndex int

	// testVersion is the current test version (for optimistic locking).
	testVersion int

	// mu protects concurrent access to session state.
	mu sync.RWMutex

	// ctx is the session context.
	ctx context.Context

	// cancel cancels the session context.
	cancel context.CancelFunc

	// onStateChange is called when the session state changes.
	onStateChange func(SessionState)

	// onStepResult is called when a step execution completes.
	onStepResult func(*StepRecord)

	// onStepProgress is called when step execution progress is received.
	onStepProgress func(*api.StepStreamMessage)

	// onLog is called when a log message is received.
	onLog func(string)
}

// NewSession creates a new interactive session.
//
// Parameters:
//   - config: The session configuration
//
// Returns:
//   - *Session: A new session instance
func NewSession(config SessionConfig) *Session {
	// Set defaults
	if config.StepTimeout == 0 {
		config.StepTimeout = 60 * time.Second
	}
	if config.ConnectionTimeout == 0 {
		config.ConnectionTimeout = 120 * time.Second
	}

	return &Session{
		config:           config,
		client:           api.NewClientWithDevMode(config.APIKey, config.DevMode),
		state:            StateInitializing,
		steps:            make([]StepRecord, 0),
		currentStepIndex: 0,
	}
}

// SetOnStateChange sets the callback for state changes.
//
// Parameters:
//   - callback: Function called when state changes
func (s *Session) SetOnStateChange(callback func(SessionState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onStateChange = callback
}

// SetOnStepResult sets the callback for step results.
//
// Parameters:
//   - callback: Function called when a step completes
func (s *Session) SetOnStepResult(callback func(*StepRecord)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onStepResult = callback
}

// SetOnStepProgress sets the callback for step progress updates.
// This is called during step execution with intermediate progress information.
//
// Parameters:
//   - callback: Function called when progress is received
func (s *Session) SetOnStepProgress(callback func(*api.StepStreamMessage)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onStepProgress = callback
}

// SetOnLog sets the callback for log messages.
//
// Parameters:
//   - callback: Function called when a log message is received
func (s *Session) SetOnLog(callback func(string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onLog = callback
}

// setState updates the session state and notifies listeners.
func (s *Session) setState(state SessionState) {
	s.mu.Lock()
	s.state = state
	callback := s.onStateChange
	s.mu.Unlock()

	if callback != nil {
		callback(state)
	}
}

// State returns the current session state.
//
// Returns:
//   - SessionState: The current state
func (s *Session) State() SessionState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// Steps returns a copy of the recorded steps.
//
// Returns:
//   - []StepRecord: Copy of the steps
func (s *Session) Steps() []StepRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]StepRecord, len(s.steps))
	copy(result, s.steps)
	return result
}

// WorkflowRunID returns the workflow run identifier.
//
// Returns:
//   - string: The workflow run ID
func (s *Session) WorkflowRunID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workflowRunID
}

// Start initializes the session by starting a device and connecting to the worker.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - error: Any error that occurred during startup
func (s *Session) Start(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)

	s.setState(StateInitializing)

	// Start the device
	startReq := &api.StartDeviceRequest{
		Platform:     s.config.Platform,
		TestID:       s.config.TestID,
		IsSimulation: s.config.IsSimulation,
	}

	startResp, err := s.client.StartDevice(s.ctx, startReq)
	if err != nil {
		s.setState(StateError)
		return fmt.Errorf("failed to start device: %w", err)
	}

	// Check for errors in response
	if startResp.Error != nil && *startResp.Error != "" {
		s.setState(StateError)
		return fmt.Errorf("device start failed: %s", *startResp.Error)
	}

	if startResp.WorkflowRunId == nil || *startResp.WorkflowRunId == "" {
		s.setState(StateError)
		return fmt.Errorf("no workflow run ID returned")
	}

	s.mu.Lock()
	s.workflowRunID = *startResp.WorkflowRunId
	s.mu.Unlock()

	// Wait for worker WebSocket URL
	s.setState(StateConnecting)

	wsURL, err := s.waitForWorkerURL(s.ctx)
	if err != nil {
		s.setState(StateError)
		return fmt.Errorf("failed to get worker URL: %w", err)
	}

	s.mu.Lock()
	s.workerWSURL = wsURL
	s.mu.Unlock()

	// Connect to worker WebSocket
	s.wsClient = api.NewWorkerWSClient(s.workflowRunID)
	if err := s.wsClient.Connect(s.ctx, wsURL); err != nil {
		s.setState(StateError)
		return fmt.Errorf("failed to connect to worker: %w", err)
	}

	// Start message handler
	go s.handleMessages()

	s.setState(StateReady)
	return nil
}

// waitForWorkerURL polls for the worker WebSocket URL until it's available.
func (s *Session) waitForWorkerURL(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, s.config.ConnectionTimeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timeout waiting for worker URL: %w", ctx.Err())

		case <-ticker.C:
			resp, err := s.client.GetWorkerWSURL(ctx, s.workflowRunID)
			if err != nil {
				// Log but continue polling
				if s.onLog != nil {
					s.onLog(fmt.Sprintf("Waiting for worker... (%v)", err))
				}
				continue
			}

			if resp.Status == "ready" && resp.WorkerWsUrl != nil && *resp.WorkerWsUrl != "" {
				return *resp.WorkerWsUrl, nil
			}

			if s.onLog != nil {
				s.onLog("Waiting for device to initialize...")
			}
		}
	}
}

// handleMessages processes incoming WebSocket messages.
func (s *Session) handleMessages() {
	for {
		select {
		case <-s.ctx.Done():
			return

		case err := <-s.wsClient.Errors():
			if s.onLog != nil {
				s.onLog(fmt.Sprintf("WebSocket error: %v", err))
			}
			s.setState(StateError)
			return

		case msg, ok := <-s.wsClient.Messages():
			if !ok {
				return
			}

			// Handle different message types
			switch msg.EventType {
			case "LOG":
				if s.onLog != nil {
					s.onLog(string(msg.Data))
				}

			case "DEVICE_INIT_STATUS":
				if s.onLog != nil {
					s.onLog("Device initialized")
				}

			case "CONNECTION":
				if s.onLog != nil {
					s.onLog(fmt.Sprintf("Connection: %s", msg.Status))
				}
			}
		}
	}
}

// ExecuteStep executes a step and waits for the result.
//
// Parameters:
//   - ctx: Context for cancellation
//   - cmdType: The command type (determines block type and step type)
//   - instruction: The natural language instruction
//
// Returns:
//   - *StepRecord: The step result
//   - error: Any error that occurred
func (s *Session) ExecuteStep(ctx context.Context, cmdType CommandType, instruction string) (*StepRecord, error) {
	s.mu.Lock()
	if s.state != StateReady {
		s.mu.Unlock()
		return nil, fmt.Errorf("session not ready (state: %s)", s.state)
	}

	stepID := fmt.Sprintf("step-%d-%d", s.currentStepIndex, time.Now().UnixNano())
	stepIndex := s.currentStepIndex
	s.state = StateExecuting
	s.mu.Unlock()

	s.setState(StateExecuting)

	// Get block type and step type from command type
	blockType := GetBlockType(cmdType)
	stepType := GetStepType(cmdType)

	// Create step definition with proper block format
	step := api.StepDefinition{
		ID:              stepID,
		Type:            blockType,
		StepType:        stepType,
		StepDescription: instruction,
		Index:           stepIndex,
		Timeout:         int(s.config.StepTimeout.Seconds()),
	}

	// Send step execution command
	if err := s.wsClient.SendStepExecution(ctx, step, s.config.TestID, s.config.IsSimulation); err != nil {
		s.setState(StateReady)
		return nil, fmt.Errorf("failed to send step: %w", err)
	}

	// Get progress callback
	s.mu.RLock()
	progressCallback := s.onStepProgress
	s.mu.RUnlock()

	// Wait for result with progress updates
	result, err := s.wsClient.WaitForStepResultWithProgress(ctx, stepID, s.config.StepTimeout, progressCallback)
	if err != nil {
		s.setState(StateReady)
		return nil, fmt.Errorf("step execution failed: %w", err)
	}

	// Create step record
	record := &StepRecord{
		ID:           stepID,
		BlockType:    blockType,
		StepType:     stepType,
		Instruction:  instruction,
		Index:        stepIndex,
		Success:      result.Success,
		Error:        result.Error,
		Duration:     result.Duration,
		ActionsTaken: result.ActionsTaken,
		ExecutedAt:   time.Now(),
	}

	// Add to steps and sync
	s.mu.Lock()
	s.steps = append(s.steps, *record)
	s.currentStepIndex++
	s.state = StateReady
	callback := s.onStepResult
	s.mu.Unlock()

	// Notify listener
	if callback != nil {
		callback(record)
	}

	// Sync to backend (auto-save)
	if err := s.syncToBackend(ctx); err != nil {
		if s.onLog != nil {
			s.onLog(fmt.Sprintf("Warning: failed to sync step to backend: %v", err))
		}
	}

	s.setState(StateReady)
	return record, nil
}

// syncToBackend saves the current steps to the backend.
func (s *Session) syncToBackend(ctx context.Context) error {
	if s.config.TestID == "" || s.config.IsSimulation {
		return nil // Nothing to sync in simulation mode
	}

	s.mu.RLock()
	steps := make([]StepRecord, len(s.steps))
	copy(steps, s.steps)
	testVersion := s.testVersion
	s.mu.RUnlock()

	// Convert steps to blocks format
	blocks := make([]map[string]interface{}, len(steps))
	for i, step := range steps {
		blocks[i] = map[string]interface{}{
			"id":               step.ID,
			"type":             step.BlockType,
			"step_type":        step.StepType,
			"step_description": step.Instruction,
		}
	}

	// Update test via API
	updateReq := &api.UpdateTestRequest{
		TestID:          s.config.TestID,
		Tasks:           blocks,
		ExpectedVersion: testVersion,
	}

	resp, err := s.client.UpdateTest(ctx, updateReq)
	if err != nil {
		return fmt.Errorf("failed to update test: %w", err)
	}

	// Update version for optimistic locking
	s.mu.Lock()
	s.testVersion = resp.Version
	s.mu.Unlock()

	return nil
}

// UndoLastStep removes the last step from the session.
//
// Returns:
//   - *StepRecord: The removed step, or nil if no steps
//   - error: Any error that occurred
func (s *Session) UndoLastStep() (*StepRecord, error) {
	s.mu.Lock()
	if len(s.steps) == 0 {
		s.mu.Unlock()
		return nil, fmt.Errorf("no steps to undo")
	}

	// Remove last step
	lastStep := s.steps[len(s.steps)-1]
	s.steps = s.steps[:len(s.steps)-1]
	s.currentStepIndex--
	s.mu.Unlock()

	// Sync to backend
	if err := s.syncToBackend(s.ctx); err != nil {
		if s.onLog != nil {
			s.onLog(fmt.Sprintf("Warning: failed to sync undo to backend: %v", err))
		}
	}

	return &lastStep, nil
}

// Stop gracefully shuts down the session.
//
// Returns:
//   - error: Any error that occurred during shutdown
func (s *Session) Stop() error {
	s.setState(StateStopping)

	// Cancel context
	if s.cancel != nil {
		s.cancel()
	}

	// Close WebSocket connection
	if s.wsClient != nil {
		if err := s.wsClient.Close(); err != nil {
			if s.onLog != nil {
				s.onLog(fmt.Sprintf("Warning: error closing WebSocket: %v", err))
			}
		}
	}

	// Cancel device workflow
	if s.workflowRunID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if _, err := s.client.CancelDevice(ctx, s.workflowRunID); err != nil {
			if s.onLog != nil {
				s.onLog(fmt.Sprintf("Warning: error cancelling device: %v", err))
			}
		}
	}

	s.setState(StateStopped)
	return nil
}

// GetTestID returns the test ID associated with this session.
//
// Returns:
//   - string: The test ID
func (s *Session) GetTestID() string {
	return s.config.TestID
}

// GetPlatform returns the platform for this session.
//
// Returns:
//   - string: The platform (ios or android)
func (s *Session) GetPlatform() string {
	return s.config.Platform
}

// GetTestName returns the test name for this session.
//
// Returns:
//   - string: The test name
func (s *Session) GetTestName() string {
	return s.config.TestName
}

// GetFrontendURL returns the URL to view this session in the frontend.
// The URL includes both testUid and workflowRunId so the frontend can
// join the existing session instead of starting a new one.
//
// Returns:
//   - string: The frontend URL with query parameters
func (s *Session) GetFrontendURL() string {
	baseURL := config.GetAppURL(s.config.DevMode)

	s.mu.RLock()
	workflowRunID := s.workflowRunID
	s.mu.RUnlock()

	return fmt.Sprintf("%s/tests/execute?testUid=%s&workflowRunId=%s",
		baseURL, s.config.TestID, workflowRunID)
}

// GetHotReloadURL returns the hot reload deep link URL for this session.
// Returns an empty string if hot reload is not configured.
//
// Returns:
//   - string: The hot reload deep link URL, or empty string if not configured
func (s *Session) GetHotReloadURL() string {
	return s.config.HotReloadURL
}
