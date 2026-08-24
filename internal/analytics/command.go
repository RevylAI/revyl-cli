package analytics

import (
	"context"
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
	completion           *CommandCompletion
}

type commandRunContextKey struct{}

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

// SafeDiagnosticError preserves the original error for the user and callers
// while supplying a bounded diagnostic for command analytics. Use it when an
// error can contain customer-authored values that generic sanitization cannot
// reliably recognize.
type SafeDiagnosticError struct {
	err        error
	diagnostic string
}

func WithSafeDiagnostic(err error, diagnostic string) error {
	if err == nil {
		return nil
	}
	return &SafeDiagnosticError{err: err, diagnostic: diagnostic}
}

func (e *SafeDiagnosticError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *SafeDiagnosticError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *SafeDiagnosticError) safeDiagnostic() string {
	if e == nil {
		return ""
	}
	return e.diagnostic
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
	mergeCommandCompletionProperties(props, r.completionSnapshot())
	var completedErr *CompletedError
	if errors.As(err, &completedErr) {
		completion := completedErr.Completion()
		props["exit_code"] = completion.ExitCode
		mergeCommandCompletionProperties(props, completion)
		r.capture(CliCommandCompletedEvent, props)
		return
	}
	if err != nil {
		props["error"] = true
		props["exit_code"] = 1
		diagnostic := err.Error()
		var safeErr interface{ safeDiagnostic() string }
		hasSafeDiagnostic := errors.As(err, &safeErr)
		if hasSafeDiagnostic {
			diagnostic = safeErr.safeDiagnostic()
		}
		props["error_message"] = sanitizeDiagnosticString(diagnostic, r.diagnosticRedactions)
		if tail := r.outputTailSnapshot(); !hasSafeDiagnostic && len(tail) > 0 {
			props["output_tail"] = tail
		}
		r.capture(CliCommandFailedEvent, props)
	} else {
		props["exit_code"] = 0
		r.capture(CliCommandCompletedEvent, props)
	}
}

// ContextWithCommandRun makes bounded terminal metadata available to the
// command implementation without exposing the recorder or a capture API.
func ContextWithCommandRun(ctx context.Context, run *CommandRun) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, commandRunContextKey{}, run)
}

// SetCommandCompletion records bounded domain metadata for the one terminal
// lifecycle event. Repeated calls merge properties and let the latest status
// describe the terminal outcome.
func SetCommandCompletion(ctx context.Context, completion CommandCompletion) {
	if ctx == nil {
		return
	}
	run, _ := ctx.Value(commandRunContextKey{}).(*CommandRun)
	if run == nil {
		return
	}
	run.setCompletion(completion)
}

func (r *CommandRun) setCompletion(completion CommandCompletion) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.completion == nil {
		copied := copyCommandCompletion(completion)
		r.completion = &copied
		return
	}
	if domain := strings.TrimSpace(completion.Domain); domain != "" {
		r.completion.Domain = domain
	}
	if status := strings.TrimSpace(completion.DomainStatus); status != "" {
		r.completion.DomainStatus = status
	}
	if completion.ExitCode != 0 {
		r.completion.ExitCode = completion.ExitCode
	}
	if r.completion.Properties == nil && len(completion.Properties) > 0 {
		r.completion.Properties = map[string]interface{}{}
	}
	for key, value := range completion.Properties {
		r.completion.Properties[key] = value
	}
}

func (r *CommandRun) completionSnapshot() CommandCompletion {
	if r == nil {
		return CommandCompletion{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.completion == nil {
		return CommandCompletion{}
	}
	return copyCommandCompletion(*r.completion)
}

func mergeCommandCompletionProperties(props map[string]interface{}, completion CommandCompletion) {
	if domain := strings.TrimSpace(completion.Domain); domain != "" {
		props["domain"] = domain
	}
	if status := strings.TrimSpace(completion.DomainStatus); status != "" {
		props["domain_status"] = status
	}
	for key, value := range completion.Properties {
		props[key] = value
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
