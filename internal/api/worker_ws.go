// Package api provides the HTTP and WebSocket clients for the Revyl API.
//
// This file contains the WorkerWSClient for connecting to the worker WebSocket
// server for interactive test creation and real-time step execution.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WorkerWSClient handles WebSocket communication with the worker server.
// Used for interactive test creation to send step execution commands
// and receive real-time results.
//
// Message Routing Architecture:
// The client uses a demultiplexer pattern to route messages to separate channels
// based on event type. This prevents race conditions where multiple goroutines
// compete to read from a single channel:
//   - stepMessages: receives STEP_EXECUTION and ERROR events (consumed by WaitForStepResult)
//   - messages: receives all other events like LOG, DEVICE_INIT_STATUS, CONNECTION (consumed by handleMessages)
type WorkerWSClient struct {
	// conn is the underlying WebSocket connection.
	conn *websocket.Conn

	// workflowRunID is the workflow run identifier.
	workflowRunID string

	// mu protects concurrent access to the connection.
	mu sync.Mutex

	// done signals when the client should stop.
	done chan struct{}

	// messages receives non-step messages from the worker (LOG, DEVICE_INIT_STATUS, CONNECTION, etc.).
	// This channel is consumed by the session's handleMessages goroutine.
	messages chan WorkerMessage

	// stepMessages receives step execution messages (STEP_EXECUTION, ERROR).
	// This channel is consumed by WaitForStepResultWithProgress to avoid race conditions
	// with the handleMessages goroutine.
	stepMessages chan WorkerMessage

	// errors receives connection errors.
	errors chan error

	// pingInterval is the interval between ping messages.
	pingInterval time.Duration

	// connected indicates if the connection is active.
	connected bool
}

// WorkerMessage represents a message received from the worker WebSocket.
type WorkerMessage struct {
	// EventType is the type of event (e.g., "STEP_STREAM", "LOG", "CONNECTION").
	EventType string `json:"event_type,omitempty"`

	// Type is an alternative field for message type (e.g., "ping", "pong").
	Type string `json:"type,omitempty"`

	// Status is the connection or execution status.
	Status string `json:"status,omitempty"`

	// WorkflowRunID is the workflow run identifier.
	WorkflowRunID string `json:"workflow_run_id,omitempty"`

	// Data contains the message payload.
	Data json.RawMessage `json:"data,omitempty"`

	// ID is used for ping/pong correlation.
	ID string `json:"id,omitempty"`

	// Timestamp is the message timestamp.
	Timestamp float64 `json:"timestamp,omitempty"`

	// Raw contains the original message bytes for debugging.
	Raw []byte `json:"-"`
}

// StepExecutionMessage represents a STEP_EXECUTION command sent to the worker.
// This triggers the LLM pipeline to execute a step on the device.
type StepExecutionMessage struct {
	// EventType must be "STEP_EXECUTION".
	EventType string `json:"event_type"`

	// Action specifies the execution action (e.g., "EXECUTE", "STOP_EXECUTION").
	Action string `json:"action"`

	// StepDetails contains the step execution payload.
	StepDetails StepDetails `json:"step_details"`
}

// StepDetails contains the step execution payload for STEP_EXECUTION messages.
// This wraps the steps array and execution metadata.
type StepDetails struct {
	// Steps contains the step definitions to execute.
	Steps []StepDefinition `json:"steps"`

	// TestId is the test identifier associated with this execution.
	TestId string `json:"testId,omitempty"`

	// IsSimulation indicates if this is a simulation (no test execution records).
	IsSimulation bool `json:"is_simulation"`
}

// StepDefinition represents a step to be executed by the worker.
type StepDefinition struct {
	// ID is a unique identifier for this step.
	ID string `json:"id"`

	// Type is the block type (instructions, manual, validation).
	Type string `json:"type"`

	// StepType is the specific step type (instruction, navigate, wait, etc.).
	StepType string `json:"step_type"`

	// StepDescription is the natural language instruction for the step.
	StepDescription string `json:"step_description"`

	// Index is the step index in the test sequence.
	Index int `json:"index"`

	// Timeout is the maximum execution time in seconds.
	Timeout int `json:"timeout,omitempty"`
}

// StepStreamMessage represents a STEP_EXECUTION response from the worker.
// Contains the result of step execution.
// Note: Despite the name, this handles STEP_EXECUTION events from the backend.
type StepStreamMessage struct {
	// EventType is "STEP_EXECUTION".
	EventType string `json:"event_type"`

	// StepID is the ID of the executed step.
	StepID string `json:"step_id"`

	// Status is the execution status (started, in_progress, completed, error, canceled).
	Status string `json:"status"`

	// Success indicates if the step passed.
	Success *bool `json:"success,omitempty"`

	// Error contains the error message if the step failed.
	Error string `json:"error,omitempty"`

	// ActionType is the type of action performed (tap, type, swipe, etc.).
	ActionType string `json:"action_type,omitempty"`

	// ActionValue contains the value for the action (e.g., text typed).
	ActionValue interface{} `json:"action_value,omitempty"`

	// Result contains the detailed execution result from the backend.
	Result *StepResult `json:"result,omitempty"`

	// ActionsTaken contains the actions performed during execution.
	ActionsTaken []ActionTaken `json:"actions_taken,omitempty"`

	// Screenshot is the base64-encoded screenshot after execution.
	Screenshot string `json:"screenshot,omitempty"`

	// Duration is the execution time in milliseconds.
	Duration int64 `json:"duration_ms,omitempty"`
}

// StepResult contains detailed execution result data from the backend.
type StepResult struct {
	// Success indicates if the step passed.
	Success bool `json:"success,omitempty"`

	// ActionID is the identifier for the action performed.
	ActionID string `json:"action_id,omitempty"`

	// ActionType is the type of action (tap, type, swipe, etc.).
	ActionType string `json:"action_type,omitempty"`

	// ActionValue contains the value for the action.
	ActionValue interface{} `json:"action_value,omitempty"`

	// CurrentStep is a description of the current step being executed.
	CurrentStep string `json:"current_step,omitempty"`

	// CurrentStepIndex is the index of the current step (0-based).
	CurrentStepIndex int `json:"current_step_index,omitempty"`

	// TotalSteps is the total number of steps in the execution.
	TotalSteps int `json:"total_steps,omitempty"`

	// Reasoning contains the AI's reasoning for the action.
	Reasoning string `json:"reasoning,omitempty"`

	// Error contains any error message.
	Error string `json:"error,omitempty"`

	// ValidationResult contains the result of a validation step.
	ValidationResult interface{} `json:"validation_result,omitempty"`

	// ExtractedData contains data extracted during the step.
	ExtractedData interface{} `json:"extracted_data,omitempty"`

	// StepDuration is the execution time in milliseconds.
	StepDuration int64 `json:"step_duration,omitempty"`

	// ActionDescription is a human-readable description of the action.
	ActionDescription string `json:"action_description,omitempty"`
}

// ActionTaken represents an action performed during step execution.
type ActionTaken struct {
	// Type is the action type (tap, type, swipe, etc.).
	Type string `json:"type"`

	// Description is a human-readable description of the action.
	Description string `json:"description,omitempty"`

	// Coordinates contains the x,y coordinates for tap/swipe actions.
	Coordinates *Coordinates `json:"coordinates,omitempty"`

	// Text contains the text for type actions.
	Text string `json:"text,omitempty"`
}

// Coordinates represents x,y screen coordinates.
type Coordinates struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// NewWorkerWSClient creates a new WorkerWSClient.
//
// Parameters:
//   - workflowRunID: The workflow run identifier
//
// Returns:
//   - *WorkerWSClient: A new client instance
func NewWorkerWSClient(workflowRunID string) *WorkerWSClient {
	return &WorkerWSClient{
		workflowRunID: workflowRunID,
		done:          make(chan struct{}),
		messages:      make(chan WorkerMessage, 100),
		stepMessages:  make(chan WorkerMessage, 100),
		errors:        make(chan error, 10),
		pingInterval:  25 * time.Second,
	}
}

// Connect establishes a WebSocket connection to the worker.
//
// Parameters:
//   - ctx: Context for cancellation
//   - wsURL: The WebSocket URL (e.g., "wss://worker.example.com/ws/stream?token=xxx")
//
// Returns:
//   - error: Any error that occurred during connection
func (c *WorkerWSClient) Connect(ctx context.Context, wsURL string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return fmt.Errorf("already connected")
	}

	// Parse and validate the URL
	parsedURL, err := url.Parse(wsURL)
	if err != nil {
		return fmt.Errorf("invalid WebSocket URL: %w", err)
	}

	// Ensure we're using the WebSocket scheme
	if parsedURL.Scheme == "http" {
		parsedURL.Scheme = "ws"
	} else if parsedURL.Scheme == "https" {
		parsedURL.Scheme = "wss"
	}

	// Connect with a dialer that respects the context
	dialer := websocket.Dialer{
		HandshakeTimeout: 30 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, parsedURL.String(), nil)
	if err != nil {
		return fmt.Errorf("WebSocket connection failed: %w", err)
	}

	c.conn = conn
	c.connected = true

	// Start the read loop
	go c.readLoop()

	// Start the ping loop
	go c.pingLoop()

	return nil
}

// Reconnect establishes a new WebSocket connection after a disconnect.
// Call this when the connection has been lost (e.g. readLoop exited). Re-initializes
// the done channel and message channels so new readLoop/pingLoop goroutines have a
// fresh done signal (avoids using a done channel that was already closed by Close()).
//
// Parameters:
//   - ctx: Context for cancellation (used for dial)
//   - wsURL: The WebSocket URL to connect to
//
// Returns:
//   - error: Any error that occurred during reconnection
func (c *WorkerWSClient) Reconnect(ctx context.Context, wsURL string) error {
	c.mu.Lock()
	// Close existing connection if any (release resource; readLoop already exited)
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
	c.connected = false
	// New done channel so new readLoop/pingLoop do not see a previously closed done
	c.done = make(chan struct{})
	// Replace channels so consumers (e.g. handleMessages) can read from new ones
	c.messages = make(chan WorkerMessage, 100)
	c.stepMessages = make(chan WorkerMessage, 100)
	c.errors = make(chan error, 10)
	c.mu.Unlock()

	parsedURL, err := url.Parse(wsURL)
	if err != nil {
		return fmt.Errorf("invalid WebSocket URL: %w", err)
	}
	if parsedURL.Scheme == "http" {
		parsedURL.Scheme = "ws"
	} else if parsedURL.Scheme == "https" {
		parsedURL.Scheme = "wss"
	}

	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
	conn, _, err := dialer.DialContext(ctx, parsedURL.String(), nil)
	if err != nil {
		return fmt.Errorf("WebSocket reconnection failed: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.mu.Unlock()

	go c.readLoop()
	go c.pingLoop()
	return nil
}

// readLoop continuously reads messages from the WebSocket connection.
// It demultiplexes messages based on EventType to prevent race conditions:
//   - STEP_EXECUTION and ERROR events go to stepMessages channel
//   - All other events (LOG, DEVICE_INIT_STATUS, CONNECTION, etc.) go to messages channel
func (c *WorkerWSClient) readLoop() {
	// Capture the channels and done at start so defer closes only these, not any
	// channels created by a concurrent Reconnect().
	c.mu.Lock()
	messages := c.messages
	stepMessages := c.stepMessages
	done := c.done
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.connected = false
		c.mu.Unlock()
		close(messages)
		close(stepMessages)
	}()

	for {
		select {
		case <-done:
			return
		default:
		}

		_, message, err := c.conn.ReadMessage()
		if err != nil {
			select {
			case <-done:
				return
			case c.errors <- fmt.Errorf("read error: %w", err):
			default:
			}
			return
		}

		var msg WorkerMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			// Store raw message for debugging
			msg.Raw = message
		} else {
			msg.Raw = message
		}

		// Handle ping messages automatically
		if msg.Type == "ping" {
			c.sendPong(msg.ID)
			continue
		}

		// Route messages based on event type to prevent race conditions
		// between handleMessages goroutine and WaitForStepResultWithProgress
		switch msg.EventType {
		case "STEP_EXECUTION", "ERROR":
			// Step execution messages go to dedicated channel
			select {
			case <-done:
				return
			case stepMessages <- msg:
			}
		default:
			// All other messages (LOG, DEVICE_INIT_STATUS, CONNECTION, etc.)
			select {
			case <-done:
				return
			case messages <- msg:
			}
		}
	}
}

// pingLoop sends periodic ping messages to keep the connection alive.
func (c *WorkerWSClient) pingLoop() {
	// Capture done at start so we exit when THIS generation's done is closed,
	// preventing goroutine leaks during Reconnect().
	c.mu.Lock()
	done := c.done
	c.mu.Unlock()

	ticker := time.NewTicker(c.pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			c.mu.Lock()
			if !c.connected || c.conn == nil {
				c.mu.Unlock()
				return
			}

			pingMsg := map[string]interface{}{
				"type":      "ping",
				"id":        fmt.Sprintf("cli-%d", time.Now().UnixNano()),
				"timestamp": float64(time.Now().UnixNano()) / 1e9,
			}

			if err := c.conn.WriteJSON(pingMsg); err != nil {
				c.mu.Unlock()
				select {
				case c.errors <- fmt.Errorf("ping failed: %w", err):
				default:
				}
				return
			}
			c.mu.Unlock()
		}
	}
}

// sendPong sends a pong response to a ping message.
func (c *WorkerWSClient) sendPong(pingID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.conn == nil {
		return
	}

	pongMsg := map[string]interface{}{
		"type": "pong",
		"id":   pingID,
	}

	_ = c.conn.WriteJSON(pongMsg)
}

// SendStepExecution sends a step execution command to the worker.
//
// Parameters:
//   - ctx: Context for cancellation
//   - step: The step definition to execute
//   - testID: The test identifier for this execution
//   - isSimulation: Whether this is a simulation (no test execution records)
//
// Returns:
//   - error: Any error that occurred during sending
func (c *WorkerWSClient) SendStepExecution(ctx context.Context, step StepDefinition, testID string, isSimulation bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.conn == nil {
		return fmt.Errorf("not connected")
	}

	msg := StepExecutionMessage{
		EventType: "STEP_EXECUTION",
		Action:    "EXECUTE",
		StepDetails: StepDetails{
			Steps:        []StepDefinition{step},
			TestId:       testID,
			IsSimulation: isSimulation,
		},
	}

	if err := c.conn.WriteJSON(msg); err != nil {
		return fmt.Errorf("failed to send step execution: %w", err)
	}

	return nil
}

// SendRaw sends a raw JSON message to the worker.
//
// Parameters:
//   - ctx: Context for cancellation
//   - msg: The message to send (will be JSON marshaled)
//
// Returns:
//   - error: Any error that occurred during sending
func (c *WorkerWSClient) SendRaw(ctx context.Context, msg interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.conn == nil {
		return fmt.Errorf("not connected")
	}

	if err := c.conn.WriteJSON(msg); err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	return nil
}

// SendTaskList sends a TASK_LIST event to broadcast updated tasks to connected clients.
// This is used to sync steps from CLI to the frontend in real-time.
//
// Parameters:
//   - ctx: Context for cancellation
//   - tasks: Array of task/block definitions to broadcast
//
// Returns:
//   - error: Any error that occurred during sending
func (c *WorkerWSClient) SendTaskList(ctx context.Context, tasks []map[string]interface{}) error {
	msg := map[string]interface{}{
		"event_type": "TASK_LIST",
		"tasks":      tasks,
	}
	return c.SendRaw(ctx, msg)
}

// Messages returns the channel for receiving worker messages.
//
// Returns:
//   - <-chan WorkerMessage: Channel of incoming messages
func (c *WorkerWSClient) Messages() <-chan WorkerMessage {
	return c.messages
}

// Errors returns the channel for receiving connection errors.
//
// Returns:
//   - <-chan error: Channel of connection errors
func (c *WorkerWSClient) Errors() <-chan error {
	return c.errors
}

// IsConnected returns whether the client is currently connected.
//
// Returns:
//   - bool: True if connected, false otherwise
func (c *WorkerWSClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Close closes the WebSocket connection and signals readLoop/pingLoop to exit.
// Safe to call multiple times: only closes the current done channel once (then sets to nil).
// After Reconnect(), a new done channel exists so Close() can close it again.
//
// Returns:
//   - error: Any error that occurred during close
func (c *WorkerWSClient) Close() error {
	c.mu.Lock()
	done := c.done
	if done != nil {
		c.done = nil // prevent double-close of the same channel
	}
	c.connected = false
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()

	if done != nil {
		close(done)
	}
	var closeErr error
	if conn != nil {
		_ = conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "client closing"),
		)
		closeErr = conn.Close()
	}
	return closeErr
}

// StepProgressCallback is called when step execution progress is received.
// This allows the caller to display intermediate progress updates.
type StepProgressCallback func(msg *StepStreamMessage)

// WaitForStepResult waits for a step execution result with timeout.
//
// Parameters:
//   - ctx: Context for cancellation
//   - stepID: The step ID to wait for
//   - timeout: Maximum time to wait
//
// Returns:
//   - *StepStreamMessage: The step result
//   - error: Any error that occurred
func (c *WorkerWSClient) WaitForStepResult(ctx context.Context, stepID string, timeout time.Duration) (*StepStreamMessage, error) {
	return c.WaitForStepResultWithProgress(ctx, stepID, timeout, nil)
}

// WaitForStepResultWithProgress waits for a step execution result with timeout,
// calling the progress callback for intermediate updates.
//
// This method reads from the dedicated stepMessages channel which only receives
// STEP_EXECUTION and ERROR events, preventing race conditions with the session's
// handleMessages goroutine that reads from the messages channel.
//
// Parameters:
//   - ctx: Context for cancellation
//   - stepID: The step ID to wait for
//   - timeout: Maximum time to wait
//   - onProgress: Optional callback for progress updates (can be nil)
//
// Returns:
//   - *StepStreamMessage: The step result
//   - error: Any error that occurred
func (c *WorkerWSClient) WaitForStepResultWithProgress(ctx context.Context, stepID string, timeout time.Duration, onProgress StepProgressCallback) (*StepStreamMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case err := <-c.errors:
			return nil, fmt.Errorf("connection error: %w", err)

		case msg, ok := <-c.stepMessages:
			if !ok {
				return nil, fmt.Errorf("connection closed")
			}

			// Handle ERROR events from the backend
			if msg.EventType == "ERROR" {
				var errorMsg struct {
					Message string `json:"message"`
				}
				if err := json.Unmarshal(msg.Raw, &errorMsg); err == nil && errorMsg.Message != "" {
					return nil, fmt.Errorf("backend error: %s", errorMsg.Message)
				}
				return nil, fmt.Errorf("backend error (unknown)")
			}

			// Check if this is a STEP_EXECUTION message for our step
			// The backend sends event_type: "STEP_EXECUTION" with status values from StepExecutionStatus enum
			if msg.EventType == "STEP_EXECUTION" {
				var stepResult StepStreamMessage
				if err := json.Unmarshal(msg.Raw, &stepResult); err != nil {
					continue
				}

				if stepResult.StepID == stepID {
					// Check if this is a terminal status
					// Backend uses: "completed", "error", "canceled" (from StepExecutionStatus enum)
					if stepResult.Status == "completed" || stepResult.Status == "error" || stepResult.Status == "canceled" {
						return &stepResult, nil
					}

					// For non-terminal statuses (started, in_progress), call progress callback
					if onProgress != nil {
						onProgress(&stepResult)
					}
				}
			}
		}
	}
}
