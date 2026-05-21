package analytics

import (
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const (
	maxOutputEvents = 30
	maxOutputTail   = 20
)

type CommandRun struct {
	rec       *Recorder
	startedAt time.Time
	commandID string
	props     map[string]interface{}

	mu          sync.Mutex
	outputCount int
	outputTail  []map[string]interface{}
}

func (r *Recorder) StartCommand(cmd *cobra.Command, args []string) *CommandRun {
	if !r.Enabled() || cmd == nil {
		return nil
	}
	run := &CommandRun{
		rec:       r,
		startedAt: time.Now(),
		commandID: uuid.NewString(),
	}
	run.props = r.commandProps(cmd, args, run.commandID)
	run.capture("cli_command_started", nil)
	return run
}

func (r *CommandRun) Complete(err error) {
	if r == nil || !r.rec.Enabled() {
		return
	}
	props := map[string]interface{}{
		"duration_ms": time.Since(r.startedAt).Milliseconds(),
	}
	if err != nil {
		props["error"] = true
		props["error_message"] = sanitizeString(err.Error())
		props["output_tail"] = r.outputTailSnapshot()
		r.capture("cli_command_failed", props)
	} else {
		r.capture("cli_command_completed", props)
	}
}

func (r *CommandRun) Flush() {
	if r == nil || !r.rec.Enabled() {
		return
	}
	r.rec.Flush()
}

func (r *CommandRun) ObserveOutput(level, message string) {
	if r == nil || !r.rec.Enabled() {
		return
	}
	level = strings.TrimSpace(level)
	message = sanitizeString(message)
	if message == "" {
		return
	}

	r.mu.Lock()
	r.outputCount++
	index := r.outputCount
	offset := time.Since(r.startedAt).Milliseconds()
	tailEntry := map[string]interface{}{
		"level":     level,
		"message":   message,
		"offset_ms": offset,
	}
	r.outputTail = append(r.outputTail, tailEntry)
	if len(r.outputTail) > maxOutputTail {
		r.outputTail = r.outputTail[len(r.outputTail)-maxOutputTail:]
	}
	shouldCapture := index <= maxOutputEvents
	r.mu.Unlock()

	if !shouldCapture {
		return
	}
	r.capture("cli_output", map[string]interface{}{
		"level":     level,
		"message":   message,
		"offset_ms": offset,
		"index":     index,
	})
}

func (r *CommandRun) outputTailSnapshot() []map[string]interface{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]interface{}, len(r.outputTail))
	copy(out, r.outputTail)
	return out
}

type TelemetryPayload struct {
	Events []TelemetryEvent `json:"events"`
}

type TelemetryEvent struct {
	Event      string                 `json:"event"`
	Timestamp  time.Time              `json:"timestamp"`
	Properties map[string]interface{} `json:"properties,omitempty"`
}

func (r *CommandRun) capture(event string, props map[string]interface{}) {
	if r == nil || !r.rec.Enabled() || strings.TrimSpace(event) == "" {
		return
	}
	merged := r.rec.eventProps(r)
	for key, value := range props {
		merged[key] = value
	}

	evt := TelemetryEvent{
		Event:      event,
		Timestamp:  time.Now(),
		Properties: merged,
	}

	r.rec.mu.Lock()
	r.rec.events = append(r.rec.events, evt)
	r.rec.mu.Unlock()
}
