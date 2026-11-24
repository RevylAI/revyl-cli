// Package sse monitors test and workflow executions by stream or status polling.
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

	"github.com/charmbracelet/log"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/backendheaders"
	"github.com/revyl/cli/internal/config"
	statusutil "github.com/revyl/cli/internal/status"
)

const (
	sseStreamPath = "/api/v1/monitor/stream/unified"

	defaultPollInterval = 2 * time.Second
)

type MonitoringMode string

const (
	// MonitoringModeAuto tries SSE, then polls on error within the same deadline.
	MonitoringModeAuto MonitoringMode = "auto"
	// MonitoringModePolling uses status polling only and never opens an SSE stream.
	MonitoringModePolling MonitoringMode = "polling"
)

func normalizeMonitoringMode(mode MonitoringMode) MonitoringMode {
	if mode == MonitoringModePolling {
		return MonitoringModePolling
	}
	return MonitoringModeAuto
}

type Monitor struct {
	apiKey     string
	timeout    int
	baseURL    string
	backendURL string
}

// NewMonitor creates a new SSE monitor using the resolved backend URL.
// Respects REVYL_BACKEND_URL environment variable for custom environments.
//
// Parameters:
//   - apiKey: The API key for authentication
//   - timeout: Timeout in seconds
//
// Returns:
//   - *Monitor: A new monitor instance
func NewMonitor(apiKey string, timeout int) *Monitor {
	backendURL := config.GetBackendURL(false)
	return &Monitor{
		apiKey:     apiKey,
		timeout:    timeout,
		baseURL:    backendURL + sseStreamPath,
		backendURL: backendURL,
	}
}

// NewMonitorWithDevMode uses the backend URL selected by devMode.
// The timeout is in seconds and is shared by streaming and fallback polling.
func NewMonitorWithDevMode(apiKey string, timeout int, devMode bool) *Monitor {
	backendURL := config.GetBackendURL(devMode)
	return &Monitor{
		apiKey:     apiKey,
		timeout:    timeout,
		baseURL:    backendURL + sseStreamPath,
		backendURL: backendURL,
	}
}

type TestStatus struct {
	TaskID string

	Status string

	// Progress is the completion percentage (0-100).
	Progress int

	CurrentStep string

	TestName string

	Duration string

	ErrorMessage string

	CompletedSteps int

	TotalSteps int

	// Success is nil when no outcome was reported.
	Success *bool
}

type ChildTestProgress struct {
	TestName string
	Platform string
	Status   string
	// Success is nil when no outcome was reported.
	Success *bool
	// Duration is the execution duration as a human-readable string.
	Duration string
}

type WorkflowStatus struct {
	ChildTasks []api.CLIWorkflowChildExecution

	TaskID string

	Status string

	WorkflowName string

	TotalTests int

	CompletedTests int

	PassedTests int

	FailedTests int

	Duration string

	ErrorMessage string

	// ChildTests contains per-test progress when available from polling.
	ChildTests []ChildTestProgress
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
	ChildTasks   []api.CLIWorkflowChildExecution `json:"child_tasks"`
	Task         WorkflowTask                    `json:"task"`
	WorkflowName string                          `json:"workflow_name"`
	Progress     float64                         `json:"progress"`
}

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

// completedTestEnriched is the enriched test data embedded in _with_data SSE events.
// It corresponds to the backend's CompletedTestData model and carries final execution
// details such as duration, status, and step-level information via EnhancedTask.
type completedTestEnriched struct {
	ID                   string                    `json:"id"`
	TestUID              string                    `json:"test_uid"`
	Status               string                    `json:"status"`
	ExecutionTimeSeconds float64                   `json:"execution_time_seconds"`
	EnhancedTask         completedTestEnrichedTask `json:"enhanced_task"`
}

type completedTestEnrichedTask struct {
	TotalSteps     int    `json:"total_steps"`
	StepsCompleted int    `json:"steps_completed"`
	ErrorMessage   string `json:"error_message"`
	Success        *bool  `json:"success"`
}

// MonitorTest uses auto mode to monitor taskID, the test execution ID.
func (m *Monitor) MonitorTest(ctx context.Context, taskID, testID string, onProgress func(*TestStatus)) (*TestStatus, error) {
	return m.MonitorTestWithMode(ctx, taskID, testID, MonitoringModeAuto, onProgress)
}

// MonitorTestWithMode shares one deadline across streaming and polling.
func (m *Monitor) MonitorTestWithMode(ctx context.Context, taskID, testID string, mode MonitoringMode, onProgress func(*TestStatus)) (*TestStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(m.timeout)*time.Second)
	defer cancel()

	if normalizeMonitoringMode(mode) == MonitoringModePolling {
		return m.pollTestStatus(ctx, taskID, testID, onProgress)
	}

	status, err := m.monitorTestSSE(ctx, taskID, testID, onProgress)
	if err != nil {
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
	sseURL := fmt.Sprintf("%s/api/v1/monitor/stream/unified", m.backendURL)

	req, err := http.NewRequestWithContext(ctx, "GET", sseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSE request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	backendheaders.SetCloudAgentConversationContext(req)

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

func (m *Monitor) handleTestEvent(event SSEEvent, targetTaskID string) *TestStatus {
	switch event.Event {
	case "initial_state":
		var data struct {
			RunningTests []OrgTestMonitorItem `json:"running_tests"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			log.Debug("Failed to parse initial_state event for test", "error", err)
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
			log.Debug("Failed to parse test event", "event", event.Event, "error", err)
			return nil
		}
		if data.Test.ID == targetTaskID || data.Test.TaskID == targetTaskID {
			return testMonitorItemToStatus(&data.Test)
		}

	case "test_completed", "test_failed", "test_cancelled",
		"test_completed_with_data", "test_failed_with_data", "test_cancelled_with_data":
		// Terminal events can carry a nested test or flat IDs with status-specific data.
		var data struct {
			Test          OrgTestMonitorItem     `json:"test"`
			ID            string                 `json:"id"`
			TaskID        string                 `json:"task_id"`
			TestID        string                 `json:"test_id"`
			TestName      string                 `json:"test_name"`
			Platform      string                 `json:"platform"`
			CompletedTest *completedTestEnriched `json:"completed_test"`
			FailedTest    *completedTestEnriched `json:"failed_test"`
			CancelledTest *completedTestEnriched `json:"cancelled_test"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			log.Debug("Failed to parse test completion event", "event", event.Event, "error", err)
			return nil
		}
		testID := data.Test.ID
		testTaskID := data.Test.TaskID
		if testID == "" {
			testID = data.ID
		}
		if testTaskID == "" {
			testTaskID = data.TaskID
		}

		if testID == targetTaskID || testTaskID == targetTaskID {
			// Use the nested Test if populated (non-_with_data events), otherwise
			// build status from flat base_data fields and enriched test data.
			var s *TestStatus
			if data.Test.ID != "" {
				s = testMonitorItemToStatus(&data.Test)
			} else {
				s = &TestStatus{
					TaskID:   testTaskID,
					TestName: data.TestName,
				}
				enriched := data.CompletedTest
				if enriched == nil {
					enriched = data.FailedTest
				}
				if enriched == nil {
					enriched = data.CancelledTest
				}
				if enriched != nil {
					if enriched.ExecutionTimeSeconds > 0 {
						s.Duration = fmt.Sprintf("%.1fs", enriched.ExecutionTimeSeconds)
					}
					if enriched.Status != "" {
						s.Status = enriched.Status
					}
					if enriched.EnhancedTask.ErrorMessage != "" {
						s.ErrorMessage = enriched.EnhancedTask.ErrorMessage
					}
					if enriched.EnhancedTask.TotalSteps > 0 {
						s.TotalSteps = enriched.EnhancedTask.TotalSteps
						s.CompletedSteps = enriched.EnhancedTask.StepsCompleted
					}
					s.Success = enriched.EnhancedTask.Success
				}
			}
			// Override status and success from event type to ensure correctness.
			// The enriched data may carry stale success=true from before cancellation,
			// so we must override it based on the authoritative event type.
			if strings.HasPrefix(event.Event, "test_completed") {
				s.Status = "completed"
			} else if strings.HasPrefix(event.Event, "test_failed") {
				s.Status = "failed"
				falseVal := false
				s.Success = &falseVal
			} else if strings.HasPrefix(event.Event, "test_cancelled") {
				s.Status = "cancelled"
				falseVal := false
				s.Success = &falseVal
			}
			return s
		}

	case "heartbeat", "connection_ready":
		return nil
	}
	return nil
}

func testMonitorItemToStatus(item *OrgTestMonitorItem) *TestStatus {
	// Backend sends progress as 0-100 float; truncate to int.
	progressPercent := int(item.Progress)

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

func (m *Monitor) pollTestStatus(ctx context.Context, taskID, testID string, onProgress func(*TestStatus)) (*TestStatus, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	statusURL := fmt.Sprintf("%s/api/v1/tests/get_test_execution_task?task_id=%s", m.backendURL, taskID)

	ticker := time.NewTicker(defaultPollInterval)
	defer ticker.Stop()

	var lastStatus *TestStatus
	var consecutiveErrors int
	const maxConsecutiveErrors = 10
	firstPoll := true

	for {
		if !firstPoll {
			select {
			case <-ctx.Done():
				// Always return the context error when cancelled, even if we have a last status
				return lastStatus, ctx.Err()
			case <-ticker.C:
			}
		} else {
			firstPoll = false
		}

		req, err := http.NewRequestWithContext(ctx, "GET", statusURL, nil)
		if err != nil {
			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				return nil, fmt.Errorf("polling failed: too many consecutive errors creating request (last: %v)", err)
			}
			continue
		}

		req.Header.Set("Authorization", "Bearer "+m.apiKey)
		backendheaders.SetCloudAgentConversationContext(req)

		resp, err := client.Do(req)
		if err != nil {
			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				return nil, fmt.Errorf("polling failed: too many consecutive network errors (last: %v)", err)
			}
			continue
		}

		var statusResp struct {
			Status               string   `json:"status"`   // From device_sessions
			Progress             float64  `json:"progress"` // 0-100
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

		consecutiveErrors = 0

		// Backend sends progress as 0-100 float; truncate to int.
		progressPercent := int(statusResp.Progress)

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

		if statusutil.IsTerminal(status.Status) {
			return status, nil
		}

		// If status is empty but success is explicitly set, the test has finished
		// but the status field wasn't populated - this is a completion signal
		if status.Status == "" && statusResp.Success != nil {
			if *statusResp.Success {
				status.Status = "completed"
			} else {
				status.Status = "failed"
			}
			return status, nil
		}
	}
}

// MonitorWorkflow uses auto mode to monitor taskID, the workflow execution ID.
func (m *Monitor) MonitorWorkflow(ctx context.Context, taskID, workflowID string, onProgress func(*WorkflowStatus)) (*WorkflowStatus, error) {
	return m.MonitorWorkflowWithMode(ctx, taskID, workflowID, MonitoringModeAuto, onProgress)
}

// MonitorWorkflowWithMode shares one deadline across streaming and polling.
func (m *Monitor) MonitorWorkflowWithMode(ctx context.Context, taskID, workflowID string, mode MonitoringMode, onProgress func(*WorkflowStatus)) (*WorkflowStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(m.timeout)*time.Second)
	defer cancel()

	if normalizeMonitoringMode(mode) == MonitoringModePolling {
		return m.pollWorkflowStatus(ctx, taskID, workflowID, onProgress)
	}

	status, err := m.monitorWorkflowSSE(ctx, taskID, workflowID, onProgress)
	if err != nil {
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
	sseURL := fmt.Sprintf("%s/api/v1/monitor/stream/unified", m.backendURL)

	req, err := http.NewRequestWithContext(ctx, "GET", sseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSE request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	backendheaders.SetCloudAgentConversationContext(req)

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
				if lastStatus != nil {
					return lastStatus, nil
				}
				return nil, fmt.Errorf("SSE stream closed unexpectedly")
			}

			status := m.handleWorkflowEvent(event, taskID)
			if status != nil {
				if status.ChildTasks == nil && lastStatus != nil {
					status.ChildTasks = lastStatus.ChildTasks
				}
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

func (m *Monitor) handleWorkflowEvent(event SSEEvent, targetTaskID string) *WorkflowStatus {
	switch event.Event {
	case "initial_state":
		var data struct {
			RunningWorkflows []OrgWorkflowMonitorItem `json:"running_workflows"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			log.Debug("Failed to parse initial_state event for workflow", "error", err)
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
			log.Debug("Failed to parse workflow event", "event", event.Event, "error", err)
			return nil
		}
		if data.Workflow.Task.ID == targetTaskID {
			return workflowMonitorItemToStatus(&data.Workflow)
		}

	case "workflow_completed", "workflow_failed", "workflow_cancelled":
		// Handle completion events — supports both nested (workflow wrapper)
		// and flat (id/task_id at top level) shapes.
		var data struct {
			Workflow   OrgWorkflowMonitorItem          `json:"workflow"`
			ID         string                          `json:"id"`
			TaskID     string                          `json:"task_id"`
			Status     string                          `json:"status"`
			ChildTasks []api.CLIWorkflowChildExecution `json:"child_tasks"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			log.Debug("Failed to parse workflow completion event", "event", event.Event, "error", err)
			return nil
		}
		matchID := data.Workflow.Task.ID
		if matchID == "" {
			matchID = data.ID
		}
		if matchID == "" {
			matchID = data.TaskID
		}
		if matchID == targetTaskID {
			var status *WorkflowStatus
			if data.Workflow.Task.ID != "" {
				status = workflowMonitorItemToStatus(&data.Workflow)
			} else {
				status = &WorkflowStatus{
					TaskID:     matchID,
					Status:     data.Status,
					ChildTasks: data.ChildTasks,
				}
			}
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
		return nil
	}
	return nil
}

func workflowMonitorItemToStatus(item *OrgWorkflowMonitorItem) *WorkflowStatus {
	return &WorkflowStatus{
		TaskID:         item.Task.ID,
		ChildTasks:     item.ChildTasks,
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

func (m *Monitor) pollWorkflowStatus(ctx context.Context, taskID, workflowID string, onProgress func(*WorkflowStatus)) (*WorkflowStatus, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	statusURL := fmt.Sprintf("%s/api/v1/workflows/status/%s", m.backendURL, taskID)

	ticker := time.NewTicker(defaultPollInterval)
	defer ticker.Stop()

	var lastStatus *WorkflowStatus
	var lastChildTests []ChildTestProgress
	var consecutiveErrors int
	const maxConsecutiveErrors = 10
	firstPoll := true
	pollCount := 0
	const childTestPollInterval = 3 // fetch child tests every Nth poll

	for {
		if !firstPoll {
			select {
			case <-ctx.Done():
				// Always return the context error when cancelled, even if we have a last status
				return lastStatus, ctx.Err()
			case <-ticker.C:
			}
		} else {
			firstPoll = false
		}

		pollCount++

		req, err := http.NewRequestWithContext(ctx, "GET", statusURL, nil)
		if err != nil {
			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				return nil, fmt.Errorf("polling failed: too many consecutive errors creating request (last: %v)", err)
			}
			continue
		}

		req.Header.Set("Authorization", "Bearer "+m.apiKey)
		backendheaders.SetCloudAgentConversationContext(req)

		resp, err := client.Do(req)
		if err != nil {
			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				return nil, fmt.Errorf("polling failed: too many consecutive network errors (last: %v)", err)
			}
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				return lastStatus, fmt.Errorf("polling failed: server returned status %d after %d attempts", resp.StatusCode, maxConsecutiveErrors)
			}
			continue
		}

		var statusResp api.CLIWorkflowStatusResponse

		if err := json.NewDecoder(resp.Body).Decode(&statusResp); err != nil {
			resp.Body.Close()
			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				return nil, fmt.Errorf("polling failed: too many consecutive decode errors (last: %v)", err)
			}
			continue
		}
		resp.Body.Close()

		consecutiveErrors = 0

		if pollCount%childTestPollInterval == 0 {
			if children := m.fetchChildTestProgress(ctx, client, taskID); children != nil {
				lastChildTests = children
			}
		}

		status := &WorkflowStatus{
			TaskID:         taskID,
			Status:         statusResp.Status,
			TotalTests:     statusResp.TotalTests,
			CompletedTests: statusResp.CompletedTests,
			PassedTests:    statusResp.PassedTests,
			FailedTests:    statusResp.FailedTests,
			Duration:       statusResp.Duration,
			ErrorMessage:   statusResp.ErrorMessage,
			ChildTests:     lastChildTests,
			ChildTasks:     statusResp.ChildTasks,
		}

		lastStatus = status

		if onProgress != nil {
			onProgress(status)
		}

		if statusutil.IsTerminal(status.Status) {
			return status, nil
		}
	}
}

// fetchChildTestProgress returns nil on a report request or decode failure so
// polling can still report aggregate progress.
func (m *Monitor) fetchChildTestProgress(ctx context.Context, client *http.Client, taskID string) []ChildTestProgress {
	url := fmt.Sprintf("%s/api/v1/workflows/share/unified-report", m.backendURL)
	body := fmt.Sprintf(`{"workflow_task_id":%q}`, taskID)

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(body))
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", "application/json")
	backendheaders.SetCloudAgentConversationContext(req)

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var report struct {
		ChildTasks []struct {
			TestName             string   `json:"test_name"`
			Platform             string   `json:"platform"`
			Status               string   `json:"status"`
			Success              *bool    `json:"success"`
			ExecutionTimeSeconds *float64 `json:"execution_time_seconds"`
		} `json:"child_tasks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		return nil
	}

	children := make([]ChildTestProgress, 0, len(report.ChildTasks))
	for _, ct := range report.ChildTasks {
		cp := ChildTestProgress{
			TestName: ct.TestName,
			Platform: ct.Platform,
			Status:   ct.Status,
			Success:  ct.Success,
		}
		if ct.ExecutionTimeSeconds != nil && *ct.ExecutionTimeSeconds > 0 {
			cp.Duration = fmt.Sprintf("%.1fs", *ct.ExecutionTimeSeconds)
		}
		children = append(children, cp)
	}
	return children
}

// readSSEStream emits events delimited by blank lines. It closes the response
// body and both result channels when the reader exits.
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

			if strings.HasPrefix(line, "event:") {
				event.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			} else if strings.HasPrefix(line, "data:") {
				data := strings.TrimPrefix(line, "data:")
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

type SSEEvent struct {
	Event string

	Data json.RawMessage
}
