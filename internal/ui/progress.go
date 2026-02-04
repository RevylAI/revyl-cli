// Package ui provides progress bar and spinner components.
package ui

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinnerMu     sync.Mutex
	spinnerStop   chan struct{}
	spinnerActive bool
)

// StartSpinner starts an animated spinner with a message.
//
// Parameters:
//   - message: The message to display next to the spinner
func StartSpinner(message string) {
	spinnerMu.Lock()
	defer spinnerMu.Unlock()

	if spinnerActive {
		return
	}

	spinnerActive = true
	spinnerStop = make(chan struct{})

	go func() {
		i := 0
		for {
			select {
			case <-spinnerStop:
				// Clear the spinner line
				fmt.Printf("\r%s\r", strings.Repeat(" ", len(message)+4))
				return
			default:
				frame := spinnerFrames[i%len(spinnerFrames)]
				styledFrame := StatusRunningStyle.Render(frame)
				fmt.Printf("\r%s %s", styledFrame, message)
				i++
				time.Sleep(80 * time.Millisecond)
			}
		}
	}()
}

// StopSpinner stops the current spinner.
func StopSpinner() {
	spinnerMu.Lock()
	defer spinnerMu.Unlock()

	if !spinnerActive {
		return
	}

	close(spinnerStop)
	spinnerActive = false
	time.Sleep(100 * time.Millisecond) // Allow cleanup
}

// ProgressBar represents a progress bar state.
type ProgressBar struct {
	total   int
	current int
	width   int
	message string
}

// NewProgressBar creates a new progress bar.
//
// Parameters:
//   - total: The total value (100 for percentage)
//   - width: The width of the progress bar in characters
//
// Returns:
//   - *ProgressBar: A new progress bar instance
func NewProgressBar(total, width int) *ProgressBar {
	return &ProgressBar{
		total: total,
		width: width,
	}
}

// Update updates the progress bar.
//
// Parameters:
//   - current: The current progress value
//   - message: Optional message to display
func (p *ProgressBar) Update(current int, message string) {
	p.current = current
	p.message = message
	p.render()
}

// render draws the progress bar.
func (p *ProgressBar) render() {
	percent := float64(p.current) / float64(p.total)
	filled := int(percent * float64(p.width))
	empty := p.width - filled

	bar := strings.Repeat("█", filled) + strings.Repeat("░", empty)
	styledBar := ProgressBarStyle.Render(bar)

	percentStr := fmt.Sprintf("%3d%%", int(percent*100))
	styledPercent := TitleStyle.Render(percentStr)

	line := fmt.Sprintf("\r%s %s", styledBar, styledPercent)
	if p.message != "" {
		styledMsg := DimStyle.Render(p.message)
		line += fmt.Sprintf(" %s", styledMsg)
	}

	// Pad to clear previous content
	fmt.Printf("%-80s", line)
}

// Complete marks the progress bar as complete.
func (p *ProgressBar) Complete() {
	p.Update(p.total, "")
	fmt.Println()
}

// UpdateProgress is a convenience function for updating progress display.
//
// Parameters:
//   - progress: Progress percentage (0-100)
//   - message: Current step message
func UpdateProgress(progress int, message string) {
	bar := NewProgressBar(100, 40)
	bar.Update(progress, message)
}
