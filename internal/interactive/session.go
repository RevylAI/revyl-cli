// Package interactive records tests step by step over a worker WebSocket.
package interactive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

type SessionState string

const (
	StateInitializing SessionState = "initializing"

	StateConnecting SessionState = "connecting"

	// StateReady means the worker connection is ready, not that device initialization has completed.
	StateReady SessionState = "ready"

	StateExecuting SessionState = "executing"

	StateStopping SessionState = "stopping"

	StateStopped SessionState = "stopped"

	StateReconnecting SessionState = "reconnecting"

	StateError SessionState = "error"
)

type StepRecord struct {
	ID string

	BlockType string

	StepType string

	Instruction string

	Index int

	// Success is nil when the worker result does not include an outcome.
	Success *bool

	Error string

	// Duration is the execution time in milliseconds.
	Duration int64

	ActionsTaken []api.ActionTaken

	ExecutedAt time.Time
}

type SessionConfig struct {
	// TestID is the test ID to associate with this session.
	// Required for non-simulation mode.
	TestID string

	TestName string

	Platform string

	// IsSimulation is passed to device startup and step dispatch and skips backend test saves.
	IsSimulation bool

	APIKey string

	DevMode bool

	// HotReloadURL is used as the default for interactive navigation.
	HotReloadURL string

	// StepTimeout is the worker step budget and the limit for waiting on its result.
	StepTimeout time.Duration

	// ConnectionTimeout bounds the worker URL lookup.
	ConnectionTimeout time.Duration
}

type Session struct {
	config SessionConfig

	client *api.Client

	wsClient *api.WorkerWSClient

	workflowRunID string

	// sessionID is the canonical backend device session identifier.
	sessionID string

	workerWSURL string

	state SessionState

	steps []StepRecord

	currentStepIndex int

	// testVersion is sent as the expected version on the next save.
	testVersion int

	mu sync.RWMutex

	ctx context.Context

	cancel context.CancelFunc

	onStateChange func(SessionState)

	onStepResult func(*StepRecord)

	onStepProgress func(*api.StepStreamMessage)

	onLog func(string)

	// deviceReady is true when DEVICE_INIT_STATUS with status "initialized" has been received.
	// Step commands (device actions) should be rejected until this is true.
	deviceReady atomic.Bool

	// guarded by mu
	deviceReadyWaiters []func()
}

func NewSession(config SessionConfig) *Session {
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

func (s *Session) SetOnStateChange(callback func(SessionState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onStateChange = callback
}

func (s *Session) SetOnStepResult(callback func(*StepRecord)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onStepResult = callback
}

func (s *Session) SetOnStepProgress(callback func(*api.StepStreamMessage)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onStepProgress = callback
}

func (s *Session) SetOnLog(callback func(string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onLog = callback
}

func (s *Session) setState(state SessionState) {
	s.mu.Lock()
	s.state = state
	callback := s.onStateChange
	s.mu.Unlock()

	if callback != nil {
		callback(state)
	}
}

func (s *Session) State() SessionState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// IsDeviceReady returns true when the device has finished initializing (streaming, app setup, etc.).
// Step commands that perform actions on the device should check this before executing.
//
// Returns:
//   - bool: True if DEVICE_INIT_STATUS with status "initialized" has been received
func (s *Session) IsDeviceReady() bool {
	return s.deviceReady.Load()
}

// WhenDeviceReady runs callback once the device reports initialized; callback must not block.
func (s *Session) WhenDeviceReady(callback func()) {
	s.mu.Lock()
	if !s.deviceReady.Load() {
		s.deviceReadyWaiters = append(s.deviceReadyWaiters, callback)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	callback()
}

// Steps returns a shallow copy of the recorded steps.
func (s *Session) Steps() []StepRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]StepRecord, len(s.steps))
	copy(result, s.steps)
	return result
}

func (s *Session) WorkflowRunID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workflowRunID
}

func (s *Session) Start(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)

	s.setState(StateInitializing)

	startReq := &api.StartDeviceRequest{
		Platform:     s.config.Platform,
		TestID:       s.config.TestID,
		IsSimulation: s.config.IsSimulation,
		SessionID:    uuid.NewString(),
	}

	startResp, err := s.client.StartDevice(s.ctx, startReq)
	if err != nil {
		s.setState(StateError)
		return fmt.Errorf("failed to start device: %w", err)
	}

	if startResp.Error != nil && *startResp.Error != "" {
		s.setState(StateError)
		return fmt.Errorf("device start failed: %s", *startResp.Error)
	}

	if startResp.WorkflowRunId == nil || *startResp.WorkflowRunId == uuid.Nil {
		s.setState(StateError)
		return fmt.Errorf("no workflow run ID returned")
	}

	s.mu.Lock()
	s.sessionID = startReq.SessionID
	s.workflowRunID = startResp.WorkflowRunId.String()
	s.mu.Unlock()

	s.setState(StateConnecting)

	wsURL, err := s.waitForWorkerURL(s.ctx)
	if err != nil {
		s.setState(StateError)
		return fmt.Errorf("failed to get worker URL: %w", err)
	}

	s.mu.Lock()
	s.workerWSURL = wsURL
	s.mu.Unlock()

	s.wsClient = api.NewWorkerWSClient(s.workflowRunID)
	if err := s.wsClient.Connect(s.ctx, wsURL); err != nil {
		s.setState(StateError)
		return fmt.Errorf("failed to connect to worker: %w", err)
	}

	go s.handleMessages()

	s.setState(StateReady)
	return nil
}

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

// handleMessages processes incoming WebSocket messages. On connection loss it attempts
// reconnect with backoff and restarts a new handleMessages goroutine on success.
func (s *Session) handleMessages() {
	for {
		select {
		case <-s.ctx.Done():
			return

		case err := <-s.wsClient.Errors():
			if s.onLog != nil {
				s.onLog(fmt.Sprintf("Connection lost: %v. Reconnecting...", err))
			}
			if s.tryReconnectAndResume() {
				return // New handleMessages goroutine is running
			}
			s.setState(StateError)
			return

		case msg, ok := <-s.wsClient.Messages():
			if !ok {
				if s.onLog != nil {
					s.onLog("Connection closed. Reconnecting...")
				}
				if s.tryReconnectAndResume() {
					return
				}
				s.setState(StateError)
				return
			}

			switch msg.EventType {
			case "LOG":
				if s.onLog != nil {
					s.onLog(string(msg.Data))
				}

			case "DEVICE_INIT_STATUS":
				var initStatus struct {
					Status string `json:"status"`
				}
				if err := json.Unmarshal(msg.Raw, &initStatus); err == nil && initStatus.Status == "initialized" {
					s.mu.Lock()
					s.deviceReady.Store(true)
					waiters := s.deviceReadyWaiters
					s.deviceReadyWaiters = nil
					s.mu.Unlock()
					if s.onLog != nil {
						s.onLog("Device initialized and ready")
					}
					for _, waiter := range waiters {
						waiter()
					}
				}

			case "CONNECTION":
				if s.onLog != nil {
					s.onLog(fmt.Sprintf("Connection: %s", msg.Status))
				}
			}
		}
	}
}

// tryReconnectAndResume attempts reconnection with exponential backoff. On success resets
// deviceReady, starts a new handleMessages goroutine, and returns true. Returns false on failure.
func (s *Session) tryReconnectAndResume() bool {
	s.setState(StateReconnecting)
	const maxAttempts = 5
	baseDelay := 1 * time.Second
	maxDelay := 30 * time.Second

	s.mu.RLock()
	wsURL := s.workerWSURL
	s.mu.RUnlock()

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if s.ctx.Err() != nil {
			return false
		}
		delay := baseDelay * (1 << attempt)
		if delay > maxDelay {
			delay = maxDelay
		}
		time.Sleep(delay)

		err := s.wsClient.Reconnect(s.ctx, wsURL)
		if err == nil {
			s.deviceReady.Store(false)
			if s.onLog != nil {
				s.onLog("Reconnected. Waiting for device to be ready...")
			}
			s.setState(StateReady)
			go s.handleMessages()
			return true
		}
		if s.onLog != nil {
			s.onLog(fmt.Sprintf("Reconnect attempt %d/%d failed: %v", attempt+1, maxAttempts, err))
		}
	}
	if s.onLog != nil {
		s.onLog("Failed to reconnect after maximum attempts")
	}
	return false
}

// ExecuteStep records a returned worker result and attempts to save and broadcast
// it. Save and broadcast errors go to the log callback, not the caller.
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

	blockType := GetBlockType(cmdType)
	stepType := GetStepType(cmdType)

	step := api.StepDefinition{
		ID:              stepID,
		Type:            blockType,
		StepType:        stepType,
		StepDescription: instruction,
		Index:           stepIndex,
		Timeout:         int(s.config.StepTimeout.Seconds()),
	}

	if err := s.wsClient.SendStepExecution(ctx, step, s.config.TestID, s.config.IsSimulation); err != nil {
		s.setState(StateReady)
		return nil, fmt.Errorf("failed to send step: %w", err)
	}

	s.mu.RLock()
	progressCallback := s.onStepProgress
	s.mu.RUnlock()

	result, err := s.wsClient.WaitForStepResultWithProgress(ctx, stepID, s.config.StepTimeout, progressCallback)
	if err != nil {
		s.setState(StateReady)
		return nil, fmt.Errorf("step execution failed: %w", err)
	}

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

	s.mu.Lock()
	s.steps = append(s.steps, *record)
	s.currentStepIndex++
	s.state = StateReady
	callback := s.onStepResult
	s.mu.Unlock()

	if callback != nil {
		callback(record)
	}

	// Sync to backend (auto-save)
	if err := s.syncToBackend(ctx); err != nil {
		if s.onLog != nil {
			s.onLog(fmt.Sprintf("Warning: failed to sync step to backend: %v", err))
		}
	}

	if err := s.broadcastTaskList(ctx); err != nil {
		if s.onLog != nil {
			s.onLog(fmt.Sprintf("Warning: failed to broadcast tasks to frontend: %v", err))
		}
	}

	s.setState(StateReady)
	return record, nil
}

// syncToBackend skips saves for simulation and sessions without a test ID.
func (s *Session) syncToBackend(ctx context.Context) error {
	if s.config.TestID == "" || s.config.IsSimulation {
		return nil // Nothing to sync in simulation mode
	}

	s.mu.RLock()
	steps := make([]StepRecord, len(s.steps))
	copy(steps, s.steps)
	testVersion := s.testVersion
	s.mu.RUnlock()

	blocks := make([]map[string]interface{}, len(steps))
	for i, step := range steps {
		blocks[i] = map[string]interface{}{
			"id":               step.ID,
			"type":             step.BlockType,
			"step_type":        step.StepType,
			"step_description": step.Instruction,
		}
	}

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

// broadcastTaskList sends the current task list to the frontend via WebSocket.
// This enables real-time sync of steps from CLI to the frontend block editor.
func (s *Session) broadcastTaskList(ctx context.Context) error {
	if s.wsClient == nil || !s.wsClient.IsConnected() {
		return nil // No connection to broadcast to
	}

	s.mu.RLock()
	steps := make([]StepRecord, len(s.steps))
	copy(steps, s.steps)
	s.mu.RUnlock()

	blocks := make([]map[string]interface{}, len(steps))
	for i, step := range steps {
		blocks[i] = map[string]interface{}{
			"id":               step.ID,
			"type":             step.BlockType,
			"step_type":        step.StepType,
			"step_description": step.Instruction,
		}
	}

	return s.wsClient.SendTaskList(ctx, blocks)
}

func (s *Session) UndoLastStep() (*StepRecord, error) {
	s.mu.Lock()
	if len(s.steps) == 0 {
		s.mu.Unlock()
		return nil, fmt.Errorf("no steps to undo")
	}

	lastStep := s.steps[len(s.steps)-1]
	s.steps = s.steps[:len(s.steps)-1]
	s.currentStepIndex--
	s.mu.Unlock()

	if err := s.syncToBackend(s.ctx); err != nil {
		if s.onLog != nil {
			s.onLog(fmt.Sprintf("Warning: failed to sync undo to backend: %v", err))
		}
	}

	if err := s.broadcastTaskList(s.ctx); err != nil {
		if s.onLog != nil {
			s.onLog(fmt.Sprintf("Warning: failed to broadcast undo to frontend: %v", err))
		}
	}

	return &lastStep, nil
}

// Stop attempts WebSocket close and workflow cancellation, reporting failures
// through the log callback.
func (s *Session) Stop() error {
	s.setState(StateStopping)

	if s.cancel != nil {
		s.cancel()
	}

	if s.wsClient != nil {
		if err := s.wsClient.Close(); err != nil {
			if s.onLog != nil {
				s.onLog(fmt.Sprintf("Warning: error closing WebSocket: %v", err))
			}
		}
	}

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

func (s *Session) GetTestID() string {
	return s.config.TestID
}

func (s *Session) GetPlatform() string {
	return s.config.Platform
}

func (s *Session) GetTestName() string {
	return s.config.TestName
}

// GetFrontendURL builds the URL for the server-issued session ID, or returns
// empty before that ID is known.
func (s *Session) GetFrontendURL() string {
	baseURL := config.GetAppURL(s.config.DevMode)

	s.mu.RLock()
	sessionID := s.sessionID
	s.mu.RUnlock()

	if sessionID != "" {
		return fmt.Sprintf("%s/sessions/%s", baseURL, url.PathEscape(sessionID))
	}

	return ""
}

func (s *Session) GetHotReloadURL() string {
	return s.config.HotReloadURL
}
