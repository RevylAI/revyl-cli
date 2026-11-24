package ui

import (
	"fmt"
	"os"
	"sync"
)

// StepTracker tracks step progress and displays completed steps as a growing list.
// It maintains a history of completed step descriptions and prints them as they complete.
//
// The tracker detects step completions by monitoring the CompletedSteps count and
// captures the CurrentStep description just before it increments.
type StepTracker struct {
	completedSteps     []string
	lastCompletedCount int
	lastCurrentStep    string
	verbose            bool
	mu                 sync.Mutex
}

func NewStepTracker(verbose bool) *StepTracker {
	return &StepTracker{
		completedSteps:     make([]string, 0),
		lastCompletedCount: 0,
		verbose:            verbose,
	}
}

type StepStatus struct {
	Status         string
	CurrentStep    string
	CompletedSteps int
	TotalSteps     int
	Duration       string
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

	if status.CompletedSteps > t.lastCompletedCount {
		// A step just completed - the lastCurrentStep was the one that finished
		if t.lastCurrentStep != "" {
			t.completedSteps = append(t.completedSteps, t.lastCurrentStep)
			t.printCompletedStep(t.lastCurrentStep)
		}
		t.lastCompletedCount = status.CompletedSteps
	}

	t.lastCurrentStep = status.CurrentStep

	t.printCurrentStatus(status)
}

func (t *StepTracker) printCompletedStep(stepDescription string) {
	clearLine()
	fmt.Fprintln(os.Stderr, SuccessStyle.Render("✓ "+stepDescription))
}

func (t *StepTracker) printCurrentStatus(status *StepStatus) {
	clearLine()

	icon := getStyledStatusIcon(status.Status)
	statusLine := fmt.Sprintf("%s %s", icon, status.CurrentStep)

	if status.TotalSteps > 0 {
		statusLine += DimStyle.Render(fmt.Sprintf(" [%d/%d steps]", status.CompletedSteps+1, status.TotalSteps))
	}

	if t.verbose && status.Duration != "" {
		statusLine += DimStyle.Render(fmt.Sprintf(" (%s)", status.Duration))
	}

	fmt.Fprint(os.Stderr, statusLine)
}

// Finish clears the in-place status line on stderr when connected to a TTY.
func (t *StepTracker) Finish() {
	t.mu.Lock()
	defer t.mu.Unlock()

	clearLine()
}

func (t *StepTracker) GetCompletedSteps() []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	result := make([]string, len(t.completedSteps))
	copy(result, t.completedSteps)
	return result
}
