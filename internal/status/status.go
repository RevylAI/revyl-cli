// Package status provides shared status constants and helpers for test/workflow execution.
//
// This package centralizes all status-related logic to ensure consistency across the CLI.
// It mirrors the backend SessionStatus and WorkflowStatus enums and provides helper
// functions for determining terminal states, success conditions, and status display.
package status

import "strings"

// SessionStatus represents the status of a test execution session.
// This mirrors the backend SessionStatus enum in cognisim_schemas.
type SessionStatus string

const (
	// StatusQueued indicates the session is created and waiting for a device.
	StatusQueued SessionStatus = "queued"

	// StatusStarting indicates the device is being acquired/initialized.
	StatusStarting SessionStatus = "starting"

	// StatusRunning indicates the device is active and test can execute.
	StatusRunning SessionStatus = "running"

	// StatusVerifying indicates post-execution verification (HITL).
	StatusVerifying SessionStatus = "verifying"

	// StatusStopping indicates the device is being released.
	StatusStopping SessionStatus = "stopping"

	// StatusCompleted indicates the session ended successfully.
	StatusCompleted SessionStatus = "completed"

	// StatusFailed indicates the session ended with an error.
	StatusFailed SessionStatus = "failed"

	// StatusCancelled indicates the session was cancelled by user.
	StatusCancelled SessionStatus = "cancelled"

	// StatusTimeout indicates the session timed out.
	StatusTimeout SessionStatus = "timeout"
)

// WorkflowStatus represents the status of a workflow execution.
// This mirrors the backend WorkflowStatus enum in cognisim_schemas.
type WorkflowStatus string

const (
	// WorkflowQueued indicates the workflow is waiting to start.
	WorkflowQueued WorkflowStatus = "queued"

	// WorkflowSetup indicates the workflow is being set up.
	WorkflowSetup WorkflowStatus = "setup"

	// WorkflowRunning indicates the workflow is actively running tests.
	WorkflowRunning WorkflowStatus = "running"

	// WorkflowCompleted indicates the workflow finished successfully.
	WorkflowCompleted WorkflowStatus = "completed"

	// WorkflowFailed indicates the workflow failed.
	WorkflowFailed WorkflowStatus = "failed"

	// WorkflowCancelled indicates the workflow was cancelled.
	WorkflowCancelled WorkflowStatus = "cancelled"

	// WorkflowTimeout indicates the workflow timed out.
	WorkflowTimeout WorkflowStatus = "timeout"
)

// terminalStatuses contains all statuses that indicate execution has ended.
var terminalStatuses = map[string]bool{
	string(StatusCompleted): true,
	string(StatusFailed):    true,
	string(StatusCancelled): true,
	string(StatusTimeout):   true,
	"success":               true, // Legacy status value
	"failure":               true, // Legacy status value
}

// activeStatuses contains all statuses that indicate execution is in progress.
var activeStatuses = map[string]bool{
	string(StatusQueued):    true,
	string(StatusStarting):  true,
	string(StatusRunning):   true,
	string(StatusVerifying): true,
	string(StatusStopping):  true,
	string(WorkflowSetup):   true,
}

// IsTerminal checks if a status string indicates execution has ended.
//
// Parameters:
//   - status: The status string to check (case-insensitive)
//
// Returns:
//   - bool: True if the status is terminal (completed, failed, cancelled, timeout, success, failure)
func IsTerminal(status string) bool {
	return terminalStatuses[strings.ToLower(status)]
}

// IsActive checks if a status string indicates execution is in progress.
//
// Parameters:
//   - status: The status string to check (case-insensitive)
//
// Returns:
//   - bool: True if the status is active (queued, starting, running, verifying, stopping, setup)
func IsActive(status string) bool {
	return activeStatuses[strings.ToLower(status)]
}

// IsSuccess determines if an execution was successful based on status and success field.
//
// The logic follows the backend's map_to_display_status:
// 1. If success field is set (non-nil), use it as the authoritative source
// 2. If status is "completed" or "success", consider it passed (unless success=false)
// 3. If status is "failed", "timeout", "cancelled", or "failure", consider it failed
// 4. If there's an error message, consider it failed
//
// Parameters:
//   - status: The status string from the execution
//   - success: The success field (nil means not set, use status to infer)
//   - errorMessage: Any error message from the execution
//
// Returns:
//   - bool: True if the execution was successful
func IsSuccess(status string, success *bool, errorMessage string) bool {
	// If success field is explicitly set, use it as authoritative source
	if success != nil {
		return *success
	}

	statusLower := strings.ToLower(status)

	// Explicit failure statuses
	switch statusLower {
	case string(StatusFailed), string(StatusTimeout), string(StatusCancelled), "failure":
		return false
	case string(StatusCompleted), "success":
		// Completed/success status with no error message suggests success
		return errorMessage == ""
	}

	// Unknown status - assume failure to be safe
	return false
}

// IsWorkflowSuccess determines if a workflow execution was successful.
//
// A workflow is successful if:
// 1. Status is "completed" or "success"
// 2. AND there are no failed tests
//
// Parameters:
//   - status: The workflow status string
//   - failedTests: Number of failed tests in the workflow
//
// Returns:
//   - bool: True if the workflow was successful
func IsWorkflowSuccess(status string, failedTests int) bool {
	statusLower := strings.ToLower(status)

	// Must be in a completed state
	if statusLower != string(WorkflowCompleted) && statusLower != "success" {
		return false
	}

	// Must have no failed tests
	return failedTests == 0
}

// StatusIcon returns the appropriate icon for a status.
//
// Icons:
//   - queued: ⏳ (hourglass)
//   - running/setup/verifying/starting/stopping: ▶ (play)
//   - completed/success: ✓ (checkmark)
//   - failed/failure: ✗ (x mark)
//   - cancelled: ⊘ (circle with slash)
//   - timeout: ⏱ (stopwatch)
//   - unknown: ● (bullet)
//
// Parameters:
//   - status: The status string
//
// Returns:
//   - string: The icon character for the status
func StatusIcon(status string) string {
	switch strings.ToLower(status) {
	case string(StatusQueued):
		return "⏳"
	case string(StatusRunning), string(WorkflowSetup), string(StatusVerifying), string(StatusStarting), string(StatusStopping):
		return "▶"
	case string(StatusCompleted), "success":
		return "✓"
	case string(StatusFailed), "failure":
		return "✗"
	case string(StatusCancelled):
		return "⊘"
	case string(StatusTimeout):
		return "⏱"
	default:
		return "●"
	}
}

// StatusCategory returns the category of a status for styling purposes.
//
// Categories:
//   - "dim": queued, unknown
//   - "info": running, setup, verifying, starting, stopping
//   - "success": completed, success
//   - "error": failed, failure
//   - "warning": cancelled, timeout
//
// Parameters:
//   - status: The status string
//
// Returns:
//   - string: The category name for styling
func StatusCategory(status string) string {
	switch strings.ToLower(status) {
	case string(StatusQueued):
		return "dim"
	case string(StatusRunning), string(WorkflowSetup), string(StatusVerifying), string(StatusStarting), string(StatusStopping):
		return "info"
	case string(StatusCompleted), "success":
		return "success"
	case string(StatusFailed), "failure":
		return "error"
	case string(StatusCancelled), string(StatusTimeout):
		return "warning"
	default:
		return "dim"
	}
}
