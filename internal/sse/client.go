// Package sse provides Server-Sent Events client for real-time monitoring.
//
// This package handles SSE connections to the Revyl API for monitoring
// test and workflow execution progress in real-time.
package sse

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/revyl/cli/internal/config"
	statusutil "github.com/revyl/cli/internal/status"
)

const (
	// DefaultSSEURL is the default SSE endpoint.
	DefaultSSEURL = "https://backend.revyl.ai/api/v1/monitor/stream/unified"
)

// Monitor handles SSE-based execution monitoring.
type Monitor struct {
	apiKey     string
	timeout    int
	baseURL    string
	backendURL string
}

// NewMonitor creates a new SSE monitor using production URLs.
//
// Parameters:
//   - apiKey: The API key for authentication
//   - timeout: Timeout in seconds
//
// Returns:
//   - *Monitor: A new monitor instance
func NewMonitor(apiKey string, timeout int) *Monitor {
	return &Monitor{
		apiKey:     apiKey,
		timeout:    timeout,
		baseURL:    DefaultSSEURL,
		backendURL: config.ProdBackendURL,
	}
}

// NewMonitorWithDevMode creates a new SSE monitor with dev mode support.
// When devMode is true, the monitor uses localhost URLs read from .env files.
//
// Parameters:
//   - apiKey: The API key for authentication
//   - timeout: Timeout in seconds
//   - devMode: If true, use local development server URLs
//
// Returns:
//   - *Monitor: A new monitor instance
func NewMonitorWithDevMode(apiKey string, timeout int, devMode bool) *Monitor {
	backendURL := config.GetBackendURL(devMode)
	return &Monitor{
		apiKey:     apiKey,
		timeout:    timeout,
		baseURL:    backendURL + "/api/v1/monitor/stream/unified",
		backendURL: backendURL,
	}
}

// TestStatus represents the status of a test execution.
type TestStatus struct {
	// TaskID is the execution task ID.
	TaskID string

	// Status is the current status (queued, running, completed, failed).
	Status string

	// Progress is the completion percentage (0-100).
	Progress int

	// CurrentStep is the description of the current step.
	CurrentStep string

	// TestName is the name of the test.
	TestName string

	// Duration is the execution duration.
	Duration string

	// ErrorMessage is the error message if failed.
	ErrorMessage string

	// CompletedSteps is the number of completed steps.
	CompletedSteps int

	// TotalSteps is the total number of steps.
	TotalSteps int

	// Success indicates if the test passed (true) or failed (false). Nil means still running.
	Success *bool
}

// WorkflowStatus represents the status of a workflow execution.
type WorkflowStatus struct {
	// TaskID is the execution task ID.
	TaskID string

	// Status is the current status.
	Status string

	// WorkflowName is the name of the workflow.
	WorkflowName string

	// TotalTests is the total number of tests.
	TotalTests int

	// CompletedTests is the number of completed tests.
	CompletedTests int

	// PassedTests is the number of passed tests.
	PassedTests int

	// FailedTests is the number of failed tests.
	FailedTests int

	// Duration is the execution duration.
	Duration string

	// ErrorMessage is the error message if failed.
	ErrorMessage string
}

// OrgTestMonitorItem matches the backend OrgTestMonitorItem model for SSE events.
// This struct is used to parse test events from the unified SSE stream.
type OrgTestMonitorItem struct {
	ID                  string  `json:"id"`
	TaskID              string  `json:"task_id"`
	TestID              string  `json:"test_id"`
	TestName            string  `json:"test_name"`
	Status              string  `json:"status"`
	Progress            float64 `json:"progress"`
	CurrentStep         string  `json:"current_step"`
	CurrentStepIndex    int     `json:"current_step_index"`
	TotalSteps          int     `json:"total_steps"`
	StepsCompleted      int     `json:"steps_completed"`
	StartedAt           string  `json:"started_at"`
	ErrorMessage        string  `json:"error_message"`
	Platform            string  `json:"platform"`
	WorkflowExecutionID string  `json:"workflow_execution_id"`
}

// OrgWorkflowMonitorItem matches the backend OrgWorkflowMonitorItem model for SSE events.
// This struct is used to parse workflow events from the unified SSE stream.
type OrgWorkflowMonitorItem struct {
	Task         WorkflowTask `json:"task"`
	WorkflowName string       `json:"workflow_name"`
	Progress     float64      `json:"progress"`
}

// WorkflowTask represents the task data within a workflow monitor item.
type WorkflowTask struct {
	ID             string `json:"id"`
	WorkflowID     string `json:"workflow_id"`
	Status         string `json:"status"`
	TotalTests     int    `json:"total_tests"`
	CompletedTests int    `json:"completed_tests"`
	PassedTests    int    `json:"passed_tests"`
	FailedTests    int    `json:"failed_tests"`
	Duration       string `json:"duration"`
	ErrorMessage   string `json:"error_message"`
}

// MonitorTest monitors a test execution via SSE.
//
// Parameters:
//   - ctx: Context for cancellation
//   - taskID: The task ID to monitor
//   - testID: The test ID
//   - onProgress: Callback for progress updates
//
// Returns:
//   - *TestStatus: The final test status
//   - error: Any error that occurred
func (m *Monitor) MonitorTest(ctx context.Context, taskID, testID string, onProgress func(*TestStatus)) (*TestStatus, error) {
	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, time.Duration(m.timeout)*time.Second)
	defer cancel()

	// Try SSE first for real-time updates
	status, err := m.monitorTestSSE(ctx, taskID, testID, onProgress)
	if err != nil {
		// Fallback to polling if SSE fails (connection issues, server doesn't support it, etc.)
		return m.pollTestStatus(ctx, taskID, testID, onProgress)
	}
	return status, nil
}

// monitorTestSSE monitors a test execution using Server-Sent Events.
// This provides real-time updates with lower latency than polling.
//
// Parameters:
//   - ctx: Context for cancellation
//   - taskID: The task ID to monitor (execution ID)
//   - testID: The test ID
//   - onProgress: Callback for progress updates
//
// Returns:
//   - *TestStatus: The final test status
//   - error: Any error that occurred
func (m *Monitor) monitorTestSSE(ctx context.Context, taskID, testID string, onProgress func(*TestStatus)) (*TestStatus, error) {
	// Connect to unified SSE stream
	sseURL := fmt.Sprintf("%s/api/v1/monitor/stream/unified", m.backendURL)

	req, err := http.NewRequestWithContext(ctx, "GET", sseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSE request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	// Use a client with no timeout for streaming connections
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("SSE connection failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("SSE connection failed with status: %d", resp.StatusCode)
	}

	events, errs := m.readSSEStream(ctx, resp)

	var lastStatus *TestStatus

	for {
		select {
		case <-ctx.Done():
			// Always return the context error when cancelled, even if we have a last status
			return lastStatus, ctx.Err()

		case err := <-errs:
			if err != nil {
				if lastStatus != nil {
					return lastStatus, nil
				}
				return nil, fmt.Errorf("SSE stream error: %w", err)
			}

		case event, ok := <-events:
			if !ok {
				// Channel closed
				if lastStatus != nil {
					return lastStatus, nil
				}
				return nil, fmt.Errorf("SSE stream closed unexpectedly")
			}

			status := m.handleTestEvent(event, taskID)
			if status != nil {
				lastStatus = status
				if onProgress != nil {
					onProgress(status)
				}

				if statusutil.IsTerminal(status.Status) {
					return status, nil
				}
			}
		}
	}
}

// handleTestEvent processes an SSE event and returns a TestStatus if the event
// is relevant to the target task ID.
//
// Parameters:
//   - event: The SSE event to process
//   - targetTaskID: The task ID we're monitoring
//
// Returns:
//   - *TestStatus: The test status if event is relevant, nil otherwise
func (m *Monitor) handleTestEvent(event SSEEvent, targetTaskID string) *TestStatus {
	switch event.Event {
	case "initial_state":
		// Check if our test is in the initial state
		var data struct {
			RunningTests []OrgTestMonitorItem `json:"running_tests"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return nil
		}
		for _, test := range data.RunningTests {
			if test.ID == targetTaskID || test.TaskID == targetTaskID {
				return testMonitorItemToStatus(&test)
			}
		}

	case "test_started", "test_updated":
		var data struct {
			Test OrgTestMonitorItem `json:"test"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return nil
		}
		if data.Test.ID == targetTaskID || data.Test.TaskID == targetTaskID {
			return testMonitorItemToStatus(&data.Test)
		}

	case "test_completed", "test_failed", "test_cancelled",
		"test_completed_with_data", "test_failed_with_data", "test_cancelled_with_data":
		// Handle completion events
		var data struct {
			Test OrgTestMonitorItem `json:"test"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return nil
		}
		if data.Test.ID == targetTaskID || data.Test.TaskID == targetTaskID {
			status := testMonitorItemToStatus(&data.Test)
			// Ensure terminal status is set based on event type
			if strings.HasPrefix(event.Event, "test_completed") {
				status.Status = "completed"
			} else if strings.HasPrefix(event.Event, "test_failed") {
				status.Status = "failed"
			} else if strings.HasPrefix(event.Event, "test_cancelled") {
				status.Status = "cancelled"
			}
			return status
		}

	case "heartbeat", "connection_ready":
		// Ignore system events
		return nil
	}
	return nil
}

// testMonitorItemToStatus converts an OrgTestMonitorItem to a TestStatus.
//
// Parameters:
//   - item: The monitor item to convert
//
// Returns:
//   - *TestStatus: The converted test status
func testMonitorItemToStatus(item *OrgTestMonitorItem) *TestStatus {
	// Convert progress from 0.0-1.0 to 0-100 if needed
	progressPercent := int(item.Progress)
	if item.Progress > 0 && item.Progress <= 1.0 {
		progressPercent = int(item.Progress * 100)
	}

	return &TestStatus{
		TaskID:         item.TaskID,
		Status:         item.Status,
		Progress:       progressPercent,
		CurrentStep:    item.CurrentStep,
		TestName:       item.TestName,
		ErrorMessage:   item.ErrorMessage,
		CompletedSteps: item.StepsCompleted,
		TotalSteps:     item.TotalSteps,
	}
}

// pollTestStatus polls for test status updates.
func (m *Monitor) pollTestStatus(ctx context.Context, taskID, testID string, onProgress func(*TestStatus)) (*TestStatus, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	// Use the correct endpoint: /api/v1/tests/get_test_execution_task?task_id={taskID}
	statusURL := fmt.Sprintf("%s/api/v1/tests/get_test_execution_task?task_id=%s", m.backendURL, taskID)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastStatus *TestStatus
	var consecutiveErrors int
	const maxConsecutiveErrors = 10

	for {
		select {
		case <-ctx.Done():
			// Always return the context error when cancelled, even if we have a last status
			return lastStatus, ctx.Err()

		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, "GET", statusURL, nil)
			if err != nil {
				consecutiveErrors++
				if consecutiveErrors >= maxConsecutiveErrors {
					return nil, fmt.Errorf("polling failed: too many consecutive errors creating request (last: %v)", err)
				}
				continue
			}

			req.Header.Set("Authorization", "Bearer "+m.apiKey)

			resp, err := client.Do(req)
			if err != nil {
				consecutiveErrors++
				if consecutiveErrors >= maxConsecutiveErrors {
					return nil, fmt.Errorf("polling failed: too many consecutive network errors (last: %v)", err)
				}
				continue
			}

			// The backend returns TestExecutionTasksEnhanced model from test_executions_full view
			// Fields: id, test_id, session_id, success, progress, current_step, current_step_index,
			//         total_steps, steps_completed, error_message, status (from device_sessions),
			//         started_at, completed_at, execution_time_seconds, platform
			var statusResp struct {
				Status               string   `json:"status"`   // From device_sessions
				Progress             float64  `json:"progress"` // 0.0-1.0
				CurrentStep          string   `json:"current_step"`
				CurrentStepIndex     int      `json:"current_step_index"`
				TotalSteps           *int     `json:"total_steps"`
				StepsCompleted       int      `json:"steps_completed"`
				ErrorMessage         string   `json:"error_message"`
				Success              *bool    `json:"success"`
				ExecutionTimeSeconds *float64 `json:"execution_time_seconds"` // From device_sessions
			}

			if err := json.NewDecoder(resp.Body).Decode(&statusResp); err != nil {
				resp.Body.Close()
				consecutiveErrors++
				if consecutiveErrors >= maxConsecutiveErrors {
					return nil, fmt.Errorf("polling failed: too many consecutive decode errors (last: %v)", err)
				}
				continue
			}
			resp.Body.Close()

			// Reset consecutive errors on successful response
			consecutiveErrors = 0

			// Convert progress from 0.0-1.0 to 0-100 if needed
			progressPercent := int(statusResp.Progress)
			if statusResp.Progress > 0 && statusResp.Progress <= 1.0 {
				progressPercent = int(statusResp.Progress * 100)
			}

			// Format duration
			duration := ""
			if statusResp.ExecutionTimeSeconds != nil && *statusResp.ExecutionTimeSeconds > 0 {
				duration = fmt.Sprintf("%.1fs", *statusResp.ExecutionTimeSeconds)
			}

			totalSteps := 0
			if statusResp.TotalSteps != nil {
				totalSteps = *statusResp.TotalSteps
			}

			status := &TestStatus{
				TaskID:         taskID,
				Status:         statusResp.Status,
				Progress:       progressPercent,
				CurrentStep:    statusResp.CurrentStep,
				Duration:       duration,
				ErrorMessage:   statusResp.ErrorMessage,
				CompletedSteps: statusResp.StepsCompleted,
				TotalSteps:     totalSteps,
				Success:        statusResp.Success,
			}

			lastStatus = status

			if onProgress != nil {
				onProgress(status)
			}

			// Check if execution is complete using the shared status package
			if statusutil.IsTerminal(status.Status) {
				return status, nil
			}

			// If status is empty but success is explicitly set, the test has finished
			// but the status field wasn't populated - this is a completion signal
			if status.Status == "" && statusResp.Success != nil {
				// Infer status from success field
				if *statusResp.Success {
					status.Status = "completed"
				} else {
					status.Status = "failed"
				}
				return status, nil
			}
		}
	}
}

// MonitorWorkflow monitors a workflow execution via SSE.
//
// Parameters:
//   - ctx: Context for cancellation
//   - taskID: The task ID to monitor (workflow execution ID)
//   - workflowID: The workflow ID
//
// Returns:
//   - *WorkflowStatus: The final workflow status
//   - error: Any error that occurred
func (m *Monitor) MonitorWorkflow(ctx context.Context, taskID, workflowID string, onProgress func(*WorkflowStatus)) (*WorkflowStatus, error) {
	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, time.Duration(m.timeout)*time.Second)
	defer cancel()

	// Try SSE first for real-time updates
	status, err := m.monitorWorkflowSSE(ctx, taskID, workflowID, onProgress)
	if err != nil {
		// Fallback to polling if SSE fails
		return m.pollWorkflowStatus(ctx, taskID, workflowID, onProgress)
	}
	return status, nil
}

// monitorWorkflowSSE monitors a workflow execution using Server-Sent Events.
// This provides real-time updates with lower latency than polling.
//
// Parameters:
//   - ctx: Context for cancellation
//   - taskID: The task ID to monitor (workflow execution ID)
//   - workflowID: The workflow ID
//   - onProgress: Optional callback for progress updates
//
// Returns:
//   - *WorkflowStatus: The final workflow status
//   - error: Any error that occurred
func (m *Monitor) monitorWorkflowSSE(ctx context.Context, taskID, workflowID string, onProgress func(*WorkflowStatus)) (*WorkflowStatus, error) {
	// Connect to unified SSE stream
	sseURL := fmt.Sprintf("%s/api/v1/monitor/stream/unified", m.backendURL)

	req, err := http.NewRequestWithContext(ctx, "GET", sseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSE request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	// Use a client with no timeout for streaming connections
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("SSE connection failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("SSE connection failed with status: %d", resp.StatusCode)
	}

	events, errs := m.readSSEStream(ctx, resp)

	var lastStatus *WorkflowStatus

	for {
		select {
		case <-ctx.Done():
			// Always return the context error when cancelled, even if we have a last status
			return lastStatus, ctx.Err()

		case err := <-errs:
			if err != nil {
				if lastStatus != nil {
					return lastStatus, nil
				}
				return nil, fmt.Errorf("SSE stream error: %w", err)
			}

		case event, ok := <-events:
			if !ok {
				// Channel closed
				if lastStatus != nil {
					return lastStatus, nil
				}
				return nil, fmt.Errorf("SSE stream closed unexpectedly")
			}

			status := m.handleWorkflowEvent(event, taskID)
			if status != nil {
				lastStatus = status

				if onProgress != nil {
					onProgress(status)
				}

				if statusutil.IsTerminal(status.Status) {
					return status, nil
				}
			}
		}
	}
}

// handleWorkflowEvent processes an SSE event and returns a WorkflowStatus if the event
// is relevant to the target task ID.
//
// Parameters:
//   - event: The SSE event to process
//   - targetTaskID: The workflow execution ID we're monitoring
//
// Returns:
//   - *WorkflowStatus: The workflow status if event is relevant, nil otherwise
func (m *Monitor) handleWorkflowEvent(event SSEEvent, targetTaskID string) *WorkflowStatus {
	switch event.Event {
	case "initial_state":
		// Check if our workflow is in the initial state
		var data struct {
			RunningWorkflows []OrgWorkflowMonitorItem `json:"running_workflows"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return nil
		}
		for _, workflow := range data.RunningWorkflows {
			if workflow.Task.ID == targetTaskID {
				return workflowMonitorItemToStatus(&workflow)
			}
		}

	case "workflow_started", "workflow_updated":
		var data struct {
			Workflow OrgWorkflowMonitorItem `json:"workflow"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return nil
		}
		if data.Workflow.Task.ID == targetTaskID {
			return workflowMonitorItemToStatus(&data.Workflow)
		}

	case "workflow_completed", "workflow_failed", "workflow_cancelled":
		// Handle completion events
		var data struct {
			Workflow OrgWorkflowMonitorItem `json:"workflow"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return nil
		}
		if data.Workflow.Task.ID == targetTaskID {
			status := workflowMonitorItemToStatus(&data.Workflow)
			// Ensure terminal status is set based on event type
			if event.Event == "workflow_completed" {
				status.Status = "completed"
			} else if event.Event == "workflow_failed" {
				status.Status = "failed"
			} else if event.Event == "workflow_cancelled" {
				status.Status = "cancelled"
			}
			return status
		}

	case "heartbeat", "connection_ready":
		// Ignore system events
		return nil
	}
	return nil
}

// workflowMonitorItemToStatus converts an OrgWorkflowMonitorItem to a WorkflowStatus.
//
// Parameters:
//   - item: The monitor item to convert
//
// Returns:
//   - *WorkflowStatus: The converted workflow status
func workflowMonitorItemToStatus(item *OrgWorkflowMonitorItem) *WorkflowStatus {
	return &WorkflowStatus{
		TaskID:         item.Task.ID,
		Status:         item.Task.Status,
		WorkflowName:   item.WorkflowName,
		TotalTests:     item.Task.TotalTests,
		CompletedTests: item.Task.CompletedTests,
		PassedTests:    item.Task.PassedTests,
		FailedTests:    item.Task.FailedTests,
		Duration:       item.Task.Duration,
		ErrorMessage:   item.Task.ErrorMessage,
	}
}

// pollWorkflowStatus polls for workflow status updates.
func (m *Monitor) pollWorkflowStatus(ctx context.Context, taskID, workflowID string, onProgress func(*WorkflowStatus)) (*WorkflowStatus, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	// Use the correct endpoint: /api/v1/workflows/status/{task_id}
	statusURL := fmt.Sprintf("%s/api/v1/workflows/status/%s", m.backendURL, taskID)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastStatus *WorkflowStatus
	var consecutiveErrors int
	const maxConsecutiveErrors = 10

	for {
		select {
		case <-ctx.Done():
			// Always return the context error when cancelled, even if we have a last status
			return lastStatus, ctx.Err()

		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, "GET", statusURL, nil)
			if err != nil {
				consecutiveErrors++
				if consecutiveErrors >= maxConsecutiveErrors {
					return nil, fmt.Errorf("polling failed: too many consecutive errors creating request (last: %v)", err)
				}
				continue
			}

			req.Header.Set("Authorization", "Bearer "+m.apiKey)

			resp, err := client.Do(req)
			if err != nil {
				consecutiveErrors++
				if consecutiveErrors >= maxConsecutiveErrors {
					return nil, fmt.Errorf("polling failed: too many consecutive network errors (last: %v)", err)
				}
				continue
			}

			var statusResp struct {
				Status         string `json:"status"`
				TotalTests     int    `json:"total_tests"`
				CompletedTests int    `json:"completed_tests"`
				PassedTests    int    `json:"passed_tests"`
				FailedTests    int    `json:"failed_tests"`
				Duration       string `json:"duration"`
				ErrorMessage   string `json:"error_message"`
			}

			if err := json.NewDecoder(resp.Body).Decode(&statusResp); err != nil {
				resp.Body.Close()
				consecutiveErrors++
				if consecutiveErrors >= maxConsecutiveErrors {
					return nil, fmt.Errorf("polling failed: too many consecutive decode errors (last: %v)", err)
				}
				continue
			}
			resp.Body.Close()

			// Reset consecutive errors on successful response
			consecutiveErrors = 0

			status := &WorkflowStatus{
				TaskID:         taskID,
				Status:         statusResp.Status,
				TotalTests:     statusResp.TotalTests,
				CompletedTests: statusResp.CompletedTests,
				PassedTests:    statusResp.PassedTests,
				FailedTests:    statusResp.FailedTests,
				Duration:       statusResp.Duration,
				ErrorMessage:   statusResp.ErrorMessage,
			}

			lastStatus = status

			if onProgress != nil {
				onProgress(status)
			}

			// Check if execution is complete using the shared status package
			if statusutil.IsTerminal(status.Status) {
				return status, nil
			}
		}
	}
}

// readSSEStream reads events from an SSE stream and sends them to channels.
// It handles the text/event-stream format: id, event, data fields.
//
// Parameters:
//   - ctx: Context for cancellation
//   - resp: The HTTP response with the SSE stream body
//
// Returns:
//   - <-chan SSEEvent: Channel that receives parsed SSE events
//   - <-chan error: Channel that receives any errors
func (m *Monitor) readSSEStream(ctx context.Context, resp *http.Response) (<-chan SSEEvent, <-chan error) {
	events := make(chan SSEEvent, 10)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)
		defer resp.Body.Close()

		reader := bufio.NewReader(resp.Body)
		var event SSEEvent
		var dataBuilder strings.Builder

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			line, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					errs <- err
				}
				return
			}

			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")

			if line == "" {
				// Empty line = end of event, dispatch if we have data
				if event.Event != "" || dataBuilder.Len() > 0 {
					if dataBuilder.Len() > 0 {
						event.Data = json.RawMessage(dataBuilder.String())
					}
					select {
					case events <- event:
					case <-ctx.Done():
						return
					}
					event = SSEEvent{}
					dataBuilder.Reset()
				}
				continue
			}

			// Parse SSE field
			if strings.HasPrefix(line, "event:") {
				event.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			} else if strings.HasPrefix(line, "data:") {
				data := strings.TrimPrefix(line, "data:")
				// Handle multi-line data by appending
				if dataBuilder.Len() > 0 {
					dataBuilder.WriteString("\n")
				}
				dataBuilder.WriteString(strings.TrimSpace(data))
			}
			// Ignore id: and retry: lines for now
		}
	}()

	return events, errs
}

// SSEEvent represents a parsed SSE event.
type SSEEvent struct {
	// Event is the event type.
	Event string

	// Data is the event data.
	Data json.RawMessage
}

// TestStartedEvent represents a test_started SSE event.
type TestStartedEvent struct {
	TaskID   string `json:"task_id"`
	TestID   string `json:"test_id"`
	TestName string `json:"test_name"`
}

// TestUpdatedEvent represents a test_updated SSE event.
type TestUpdatedEvent struct {
	TaskID         string `json:"task_id"`
	TestID         string `json:"test_id"`
	Status         string `json:"status"`
	Progress       int    `json:"progress"`
	CurrentStep    string `json:"current_step"`
	CompletedSteps int    `json:"completed_steps"`
	TotalSteps     int    `json:"total_steps"`
}

// TestCompletedEvent represents a test_completed SSE event.
type TestCompletedEvent struct {
	TaskID       string `json:"task_id"`
	TestID       string `json:"test_id"`
	Status       string `json:"status"`
	Duration     string `json:"duration"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// WorkflowStartedEvent represents a workflow_started SSE event.
type WorkflowStartedEvent struct {
	TaskID       string `json:"task_id"`
	WorkflowID   string `json:"workflow_id"`
	WorkflowName string `json:"workflow_name"`
	TotalTests   int    `json:"total_tests"`
}

// WorkflowUpdatedEvent represents a workflow_updated SSE event.
type WorkflowUpdatedEvent struct {
	TaskID         string `json:"task_id"`
	WorkflowID     string `json:"workflow_id"`
	Status         string `json:"status"`
	CompletedTests int    `json:"completed_tests"`
	PassedTests    int    `json:"passed_tests"`
	FailedTests    int    `json:"failed_tests"`
	CurrentTest    string `json:"current_test"`
}

// WorkflowCompletedEvent represents a workflow_completed SSE event.
type WorkflowCompletedEvent struct {
	TaskID       string `json:"task_id"`
	WorkflowID   string `json:"workflow_id"`
	Status       string `json:"status"`
	TotalTests   int    `json:"total_tests"`
	PassedTests  int    `json:"passed_tests"`
	FailedTests  int    `json:"failed_tests"`
	Duration     string `json:"duration"`
	ErrorMessage string `json:"error_message,omitempty"`
}
