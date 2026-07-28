package analytics

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const (
	CliCommandStartedEvent   = "cli_command_started"
	CliCommandCompletedEvent = "cli_command_completed"
	CliCommandFailedEvent    = "cli_command_failed"

	// Output is buffered but never streamed: only a failing command attaches
	// its tail, so successful runs contribute no output volume at all.
	maxOutputTail = 20
)

type CommandRun struct {
	rec       *Recorder
	startedAt time.Time
	commandID string
	props     map[string]interface{}
	complete  sync.Once

	mu                   sync.Mutex
	outputTail           []map[string]interface{}
	diagnosticRedactions []string
}

// CommandCompletion describes a command that analytically completed even if it
// intentionally returns a non-zero exit code for callers such as CI.
type CommandCompletion struct {
	ExitCode     int
	Domain       string
	DomainStatus string
	Properties   map[string]interface{}
}

type CompletedError struct {
	err        error
	completion CommandCompletion
}

func CompletedWithExitCode(err error, completion CommandCompletion) error {
	if err == nil {
		err = errors.New("command completed with non-zero exit")
	}
	return &CompletedError{err: err, completion: completion}
}

func (e *CompletedError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *CompletedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *CompletedError) Completion() CommandCompletion {
	if e == nil {
		return CommandCompletion{}
	}
	return copyCommandCompletion(e.completion)
}

func (r *Recorder) StartCommand(cmd *cobra.Command, args []string) *CommandRun {
	if !r.Enabled() || cmd == nil {
		return nil
	}
	run := &CommandRun{
		rec:                  r,
		startedAt:            time.Now(),
		commandID:            uuid.NewString(),
		diagnosticRedactions: commandDiagnosticRedactions(cmd, args),
	}
	run.props = r.commandProps(cmd, args, run.commandID)
	run.capture(CliCommandStartedEvent, nil)
	return run
}

func (r *CommandRun) Complete(err error) {
	if r == nil || !r.rec.Enabled() {
		return
	}
	r.complete.Do(func() {
		r.completeCommand(err)
	})
}

func (r *CommandRun) completeCommand(err error) {
	props := map[string]interface{}{
		"duration_ms": time.Since(r.startedAt).Milliseconds(),
	}
	var completedErr *CompletedError
	if errors.As(err, &completedErr) {
		completion := completedErr.Completion()
		props["exit_code"] = completion.ExitCode
		if domain := strings.TrimSpace(completion.Domain); domain != "" {
			props["domain"] = domain
		}
		if status := strings.TrimSpace(completion.DomainStatus); status != "" {
			props["domain_status"] = status
		}
		for key, value := range completion.Properties {
			props[key] = value
		}
		r.capture(CliCommandCompletedEvent, props)
		return
	}
	if err != nil {
		props["error"] = true
		props["exit_code"] = 1
		props["error_message"] = sanitizeDiagnosticString(err.Error(), r.diagnosticRedactions)
		if tail := r.outputTailSnapshot(); len(tail) > 0 {
			props["output_tail"] = tail
		}
		r.capture(CliCommandFailedEvent, props)
	} else {
		props["exit_code"] = 0
		r.capture(CliCommandCompletedEvent, props)
	}
}

func (r *CommandRun) Flush() {
	if r == nil || !r.rec.Enabled() {
		return
	}
	r.rec.Flush()
}

// ObserveOutput keeps a bounded, sanitized tail of what the command printed so
// a failure can carry the context needed to diagnose it. Nothing is captured
// here — the tail is only attached to cli_command_failed.
func (r *CommandRun) ObserveOutput(level, message string) {
	if r == nil || !r.rec.Enabled() {
		return
	}
	level = strings.TrimSpace(level)
	message = sanitizeDiagnosticString(message, r.diagnosticRedactions)
	if message == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.outputTail = append(r.outputTail, map[string]interface{}{
		"level":     level,
		"message":   message,
		"offset_ms": time.Since(r.startedAt).Milliseconds(),
	})
	if len(r.outputTail) > maxOutputTail {
		r.outputTail = r.outputTail[len(r.outputTail)-maxOutputTail:]
	}
}

func (r *CommandRun) outputTailSnapshot() []map[string]interface{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]interface{}, len(r.outputTail))
	copy(out, r.outputTail)
	return out
}

func copyCommandCompletion(completion CommandCompletion) CommandCompletion {
	copied := completion
	if len(completion.Properties) > 0 {
		copied.Properties = make(map[string]interface{}, len(completion.Properties))
		for key, value := range completion.Properties {
			copied.Properties[key] = value
		}
	}
	return copied
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
