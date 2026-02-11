// Package ui provides terminal UI components using Charm libraries.
package ui

import (
	"fmt"
	"sync"
)

// StepTracker tracks step progress and displays completed steps as a growing list.
// It maintains a history of completed step descriptions and prints them as they complete.
//
// The tracker detects step completions by monitoring the CompletedSteps count and
// captures the CurrentStep description just before it increments.
type StepTracker struct {
	// completedSteps stores the descriptions of completed steps in order.
	completedSteps []string

	// lastCompletedCount tracks the last seen CompletedSteps value to detect new completions.
	lastCompletedCount int

	// lastCurrentStep stores the last seen current step description.
	lastCurrentStep string

	// verbose enables additional detail like duration per step.
	verbose bool

	// mu protects concurrent access to tracker state.
	mu sync.Mutex
}

// NewStepTracker creates a new step tracker.
//
// Parameters:
//   - verbose: If true, shows additional detail like duration per step
//
// Returns:
//   - *StepTracker: A new step tracker instance
func NewStepTracker(verbose bool) *StepTracker {
	return &StepTracker{
		completedSteps:     make([]string, 0),
		lastCompletedCount: 0,
		verbose:            verbose,
	}
}

// StepStatus contains the status information for a step update.
// This is a simplified interface to avoid importing the sse package.
type StepStatus struct {
	// Status is the current status (queued, running, completed, failed).
	Status string

	// CurrentStep is the description of the current step.
	CurrentStep string

	// CompletedSteps is the number of completed steps.
	CompletedSteps int

	// TotalSteps is the total number of steps.
	TotalSteps int

	// Duration is the execution duration string.
	Duration string
}

// Update processes a status update and prints any newly completed steps.
// When a step completes, it prints: ✓ Step description
// For the current step, it updates the status line: ▶ Current step [n/total steps]
//
// Parameters:
//   - status: The current step status information
func (t *StepTracker) Update(status *StepStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Detect newly completed steps by comparing CompletedSteps count
	if status.CompletedSteps > t.lastCompletedCount {
		// A step just completed - the lastCurrentStep was the one that finished
		if t.lastCurrentStep != "" {
			t.completedSteps = append(t.completedSteps, t.lastCurrentStep)
			// Print the completed step
			t.printCompletedStep(t.lastCurrentStep)
		}
		t.lastCompletedCount = status.CompletedSteps
	}

	// Store current step for next iteration
	t.lastCurrentStep = status.CurrentStep

	// Print current step status line (updates in place)
	t.printCurrentStatus(status)
}

// printCompletedStep prints a completed step with a checkmark.
//
// Parameters:
//   - stepDescription: The description of the completed step
func (t *StepTracker) printCompletedStep(stepDescription string) {
	// Clear the current line first (in case there was a status line)
	clearLine()
	fmt.Println(SuccessStyle.Render("✓ " + stepDescription))
}

// printCurrentStatus prints the current step status line.
// This line updates in place to show progress.
//
// Parameters:
//   - status: The current step status information
func (t *StepTracker) printCurrentStatus(status *StepStatus) {
	// Clear current line
	clearLine()

	// Build status line
	icon := getStyledStatusIcon(status.Status)
	statusLine := fmt.Sprintf("%s %s", icon, status.CurrentStep)

	// Add step progress
	if status.TotalSteps > 0 {
		statusLine += DimStyle.Render(fmt.Sprintf(" [%d/%d steps]", status.CompletedSteps+1, status.TotalSteps))
	}

	// Add duration in verbose mode
	if t.verbose && status.Duration != "" {
		statusLine += DimStyle.Render(fmt.Sprintf(" (%s)", status.Duration))
	}

	// Print without newline so it updates in place
	fmt.Print(statusLine)
}

// Finish clears the status line and prints a final newline.
// Call this when execution completes to ensure clean output.
func (t *StepTracker) Finish() {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Clear the in-place status line
	clearLine()
}

// GetCompletedSteps returns a copy of the completed step descriptions.
//
// Returns:
//   - []string: A copy of the completed step descriptions
func (t *StepTracker) GetCompletedSteps() []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	result := make([]string, len(t.completedSteps))
	copy(result, t.completedSteps)
	return result
}
