package ui

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	spinnerFrames         = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinnerMu             sync.Mutex
	spinnerStop           chan struct{}
	spinnerDone           chan struct{}
	spinnerActive         bool
	lastLogSpinnerMessage string
)

func StartSpinner(message string) {
	if quietMode {
		return
	}

	spinnerMu.Lock()
	defer spinnerMu.Unlock()

	if !isTTY {
		if message != lastLogSpinnerMessage {
			fmt.Fprintln(stderrW, message)
			lastLogSpinnerMessage = message
		}
		return
	}

	if spinnerActive {
		return
	}

	spinnerActive = true
	spinnerStop = make(chan struct{})
	spinnerDone = make(chan struct{})

	stopChan := spinnerStop
	doneChan := spinnerDone

	go func() {
		defer close(doneChan) // signal done after cleanup write completes
		i := 0
		for {
			select {
			case <-stopChan:
				fmt.Fprintf(stderrW, "\r%s\r", strings.Repeat(" ", len(message)+4))
				return
			default:
				frame := spinnerFrames[i%len(spinnerFrames)]
				styledFrame := StatusRunningStyle.Render(frame)
				fmt.Fprintf(stderrW, "\r%s %s", styledFrame, message)
				i++
				time.Sleep(80 * time.Millisecond)
			}
		}
	}()
}

// StopSpinner clears non-TTY dedup state and, when a spinner is active, signals it
// to stop and waits for that generation's cleanup write to finish.
func StopSpinner() {
	spinnerMu.Lock()

	lastLogSpinnerMessage = ""

	if !spinnerActive {
		spinnerMu.Unlock()
		return
	}

	close(spinnerStop)
	spinnerActive = false
	doneChan := spinnerDone
	spinnerMu.Unlock()

	<-doneChan
}

type ProgressBar struct {
	total   int
	current int
	width   int
	message string
}

func NewProgressBar(total, width int) *ProgressBar {
	return &ProgressBar{
		total: total,
		width: width,
	}
}

func (p *ProgressBar) Update(current int, message string) {
	p.current = current
	p.message = message
	p.render()
}

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

	if isTTY {
		fmt.Fprintf(stderrW, "\r%-80s", line)
	} else {
		fmt.Fprintln(stderrW, line)
	}
}

func (p *ProgressBar) Complete() {
	p.Update(p.total, "")
	fmt.Fprintln(stderrW)
}

func UpdateProgress(progress int, message string) {
	if quietMode {
		return
	}
	bar := NewProgressBar(100, 40)
	bar.Update(progress, message)
}
