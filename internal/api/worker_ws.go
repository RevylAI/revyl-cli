package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type WorkerWSClient struct {
	conn *websocket.Conn

	workflowRunID string

	mu sync.Mutex

	done chan struct{}

	messages chan WorkerMessage

	stepMessages chan WorkerMessage

	errors chan error

	pingInterval time.Duration

	connected bool
}

type WorkerMessage struct {
	EventType string `json:"event_type,omitempty"`

	Type string `json:"type,omitempty"`

	Status string `json:"status,omitempty"`

	WorkflowRunID string `json:"workflow_run_id,omitempty"`

	Data json.RawMessage `json:"data,omitempty"`

	ID string `json:"id,omitempty"`

	Timestamp float64 `json:"timestamp,omitempty"`

	Raw []byte `json:"-"`
}

// StepExecutionMessage represents a STEP_EXECUTION command sent to the worker.
// This triggers the LLM pipeline to execute a step on the device.
type StepExecutionMessage struct {
	EventType string `json:"event_type"`

	Action string `json:"action"`

	StepDetails StepDetails `json:"step_details"`
}

type StepDetails struct {
	Steps []StepDefinition `json:"steps"`

	TestId string `json:"testId,omitempty"`

	// IsSimulation indicates if this is a simulation (no test execution records).
	IsSimulation bool `json:"is_simulation"`
}

type StepDefinition struct {
	// ID is a unique identifier for this step.
	ID string `json:"id"`

	// Type is the block type (instructions, manual, validation).
	Type string `json:"type"`

	// StepType is the specific step type (instruction, navigate, wait, etc.).
	StepType string `json:"step_type"`

	StepDescription string `json:"step_description"`

	Index int `json:"index"`

	// Timeout is the maximum execution time in seconds.
	Timeout int `json:"timeout,omitempty"`
}

type StepStreamMessage struct {
	EventType string `json:"event_type"`

	StepID string `json:"step_id"`

	// Status is the execution status (such as pending, started, in_progress, completed, error, or canceled).
	Status string `json:"status"`

	Success *bool `json:"success,omitempty"`

	Error string `json:"error,omitempty"`

	ActionType string `json:"action_type,omitempty"`

	ActionValue interface{} `json:"action_value,omitempty"`

	Result *StepResult `json:"result,omitempty"`

	ActionsTaken []ActionTaken `json:"actions_taken,omitempty"`

	// Screenshot is the base64-encoded screenshot after execution.
	Screenshot string `json:"screenshot,omitempty"`

	Duration int64 `json:"duration_ms,omitempty"`
}

type StepResult struct {
	Success bool `json:"success,omitempty"`

	ActionID string `json:"action_id,omitempty"`

	ActionType string `json:"action_type,omitempty"`

	ActionValue interface{} `json:"action_value,omitempty"`

	CurrentStep string `json:"current_step,omitempty"`

	// CurrentStepIndex is the index of the current step (0-based).
	CurrentStepIndex int `json:"current_step_index,omitempty"`

	TotalSteps int `json:"total_steps,omitempty"`

	Reasoning string `json:"reasoning,omitempty"`

	Error string `json:"error,omitempty"`

	ValidationResult interface{} `json:"validation_result,omitempty"`

	ExtractedData interface{} `json:"extracted_data,omitempty"`

	// StepDuration is the execution time in milliseconds.
	StepDuration int64 `json:"step_duration,omitempty"`

	ActionDescription string `json:"action_description,omitempty"`
}

type ActionTaken struct {
	Type string `json:"type"`

	Description string `json:"description,omitempty"`

	// Coordinates contains the x,y coordinates for tap/swipe actions.
	Coordinates *Coordinates `json:"coordinates,omitempty"`

	Text string `json:"text,omitempty"`
}

type Coordinates struct {
	X int `json:"x"`
	Y int `json:"y"`
}

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

func (c *WorkerWSClient) Connect(ctx context.Context, wsURL string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return fmt.Errorf("already connected")
	}

	parsedURL, err := url.Parse(wsURL)
	if err != nil {
		return fmt.Errorf("invalid WebSocket URL: %w", err)
	}

	if parsedURL.Scheme == "http" {
		parsedURL.Scheme = "ws"
	} else if parsedURL.Scheme == "https" {
		parsedURL.Scheme = "wss"
	}

	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 30 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, parsedURL.String(), nil)
	if err != nil {
		return fmt.Errorf("WebSocket connection failed; check sandbox network permissions, HTTP_PROXY/HTTPS_PROXY, and trusted CA certificates: %w", err)
	}

	c.conn = conn
	c.connected = true

	go c.readLoop()

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
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
	c.connected = false
	c.done = make(chan struct{})
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

	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 30 * time.Second,
	}
	conn, _, err := dialer.DialContext(ctx, parsedURL.String(), nil)
	if err != nil {
		return fmt.Errorf("WebSocket reconnection failed; check sandbox network permissions, HTTP_PROXY/HTTPS_PROXY, and trusted CA certificates: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.mu.Unlock()

	go c.readLoop()
	go c.pingLoop()
	return nil
}

func (c *WorkerWSClient) readLoop() {
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
			msg.Raw = message
		} else {
			msg.Raw = message
		}

		if msg.Type == "ping" {
			c.sendPong(msg.ID)
			continue
		}

		switch msg.EventType {
		case "STEP_EXECUTION", "ERROR":
			select {
			case <-done:
				return
			case stepMessages <- msg:
			}
		default:
			select {
			case <-done:
				return
			case messages <- msg:
			}
		}
	}
}

func (c *WorkerWSClient) pingLoop() {
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

// SendStepExecution sends a STEP_EXECUTION command to the worker; ctx is accepted for
// signature compatibility and is not consulted during the write.
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

// SendRaw marshals and writes msg to the worker WebSocket; ctx is not consulted and no
// per-write deadline is set.
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

// SendTaskList sends a TASK_LIST message to the worker via SendRaw; ctx is not consulted
// during the write.
func (c *WorkerWSClient) SendTaskList(ctx context.Context, tasks []map[string]interface{}) error {
	msg := map[string]interface{}{
		"event_type": "TASK_LIST",
		"tasks":      tasks,
	}
	return c.SendRaw(ctx, msg)
}

func (c *WorkerWSClient) Messages() <-chan WorkerMessage {
	return c.messages
}

func (c *WorkerWSClient) Errors() <-chan error {
	return c.errors
}

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
		c.done = nil
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

type StepProgressCallback func(msg *StepStreamMessage)

func (c *WorkerWSClient) WaitForStepResult(ctx context.Context, stepID string, timeout time.Duration) (*StepStreamMessage, error) {
	return c.WaitForStepResultWithProgress(ctx, stepID, timeout, nil)
}

// WaitForStepResultWithProgress waits on the dedicated stepMessages channel until stepID
// reaches a terminal status or ctx/timeout expires. onProgress runs synchronously and is not
// bounded by timeout; the error channel is shared across consumers.
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

			if msg.EventType == "ERROR" {
				var errorMsg struct {
					Message string `json:"message"`
				}
				if err := json.Unmarshal(msg.Raw, &errorMsg); err == nil && errorMsg.Message != "" {
					return nil, fmt.Errorf("backend error: %s", errorMsg.Message)
				}
				return nil, fmt.Errorf("backend error (unknown)")
			}

			if msg.EventType == "STEP_EXECUTION" {
				var stepResult StepStreamMessage
				if err := json.Unmarshal(msg.Raw, &stepResult); err != nil {
					continue
				}

				if stepResult.StepID == stepID {
					if stepResult.Status == "completed" || stepResult.Status == "error" || stepResult.Status == "canceled" {
						return &stepResult, nil
					}

					if onProgress != nil {
						onProgress(&stepResult)
					}
				}
			}
		}
	}
}
