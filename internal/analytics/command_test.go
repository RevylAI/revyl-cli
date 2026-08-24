package analytics

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

func TestCompleteUsesCompletedErrorForDomainFailure(t *testing.T) {
	rec := testRecorder()
	run := testCommandRun(rec)
	err := CompletedWithExitCode(errors.New("test failed"), CommandCompletion{
		ExitCode:     1,
		Domain:       "test_run",
		DomainStatus: "failed",
		Properties: map[string]interface{}{
			"test_task_id": "task-123",
		},
	})

	run.Complete(err)

	event := lastEvent(t, rec)
	if event.Event != "cli_command_completed" {
		t.Fatalf("event = %q, want cli_command_completed", event.Event)
	}
	if got := event.Properties["exit_code"]; got != 1 {
		t.Fatalf("exit_code = %v, want 1", got)
	}
	if got := event.Properties["domain"]; got != "test_run" {
		t.Fatalf("domain = %v, want test_run", got)
	}
	if got := event.Properties["domain_status"]; got != "failed" {
		t.Fatalf("domain_status = %v, want failed", got)
	}
	if got := event.Properties["test_task_id"]; got != "task-123" {
		t.Fatalf("test_task_id = %v, want task-123", got)
	}
	if _, ok := event.Properties["error"]; ok {
		t.Fatalf("completed domain result should not set command error property")
	}
}

func TestFailureUsesExplicitSafeDiagnosticWithoutChangingReturnedError(t *testing.T) {
	recorder := testRecorder()
	run := testCommandRun(recorder)
	err := WithSafeDiagnostic(
		errors.New("profile customer-private-name is invalid"),
		"project configuration validation failed",
	)

	run.Complete(err)

	if got := err.Error(); got != "profile customer-private-name is invalid" {
		t.Fatalf("Error() = %q", got)
	}
	event := lastEvent(t, recorder)
	if event.Event != CliCommandFailedEvent {
		t.Fatalf("event = %q, want %q", event.Event, CliCommandFailedEvent)
	}
	if got := event.Properties["error_message"]; got != "project configuration validation failed" {
		t.Fatalf("error_message = %q", got)
	}
}

func TestCompleteWithoutOverrideKeepsCommandFailure(t *testing.T) {
	rec := testRecorder()
	run := testCommandRun(rec)

	run.Complete(errors.New("test not found"))

	event := lastEvent(t, rec)
	if event.Event != "cli_command_failed" {
		t.Fatalf("event = %q, want cli_command_failed", event.Event)
	}
	if got := event.Properties["exit_code"]; got != 1 {
		t.Fatalf("exit_code = %v, want 1", got)
	}
	if got := event.Properties["error"]; got != true {
		t.Fatalf("error = %v, want true", got)
	}
}

func TestCompleteSuccessIncludesZeroExitCode(t *testing.T) {
	rec := testRecorder()
	run := testCommandRun(rec)

	run.Complete(nil)

	event := lastEvent(t, rec)
	if event.Event != "cli_command_completed" {
		t.Fatalf("event = %q, want cli_command_completed", event.Event)
	}
	if got := event.Properties["exit_code"]; got != 0 {
		t.Fatalf("exit_code = %v, want 0", got)
	}
}

func TestCommandContextAddsBoundedTerminalMetadata(t *testing.T) {
	rec := testRecorder()
	run := testCommandRun(rec)
	ctx := ContextWithCommandRun(context.Background(), run)

	SetCommandCompletion(ctx, CommandCompletion{
		Domain:       "config_migration",
		DomainStatus: "proposal",
		Properties: map[string]interface{}{
			"config_migration_backup_created": false,
			"entity_id":                       "project-123",
		},
	})
	run.Complete(nil)

	event := lastEvent(t, rec)
	if got := event.Properties["domain"]; got != "config_migration" {
		t.Fatalf("domain = %v, want config_migration", got)
	}
	if got := event.Properties["domain_status"]; got != "proposal" {
		t.Fatalf("domain_status = %v, want proposal", got)
	}
	if got := event.Properties["config_migration_backup_created"]; got != false {
		t.Fatalf("config_migration_backup_created = %v, want false", got)
	}
	if got := event.Properties["entity_id"]; got != "project-123" {
		t.Fatalf("entity_id = %v, want project-123", got)
	}
}

func TestCommandContextMergesFailureMetadataWithoutRawDetails(t *testing.T) {
	rec := testRecorder()
	run := testCommandRun(rec)
	ctx := ContextWithCommandRun(context.Background(), run)

	SetCommandCompletion(ctx, CommandCompletion{
		Domain:       "config_migration",
		DomainStatus: "failed",
		Properties:   map[string]interface{}{"config_migration_backup_created": true},
	})
	SetCommandCompletion(ctx, CommandCompletion{
		DomainStatus: "failed",
		Properties:   map[string]interface{}{"config_migration_failure_code": "config_changed_before_write"},
	})
	run.ObserveOutput("stderr", "omitted customer.private_field: customer-authored detail")
	run.Complete(WithSafeDiagnostic(errors.New("/private/customer/config.yaml"), "configuration migration failed"))

	event := lastEvent(t, rec)
	if got := event.Properties["domain"]; got != "config_migration" {
		t.Fatalf("domain = %v, want config_migration", got)
	}
	if got := event.Properties["config_migration_backup_created"]; got != true {
		t.Fatalf("config_migration_backup_created = %v, want true", got)
	}
	if got := event.Properties["config_migration_failure_code"]; got != "config_changed_before_write" {
		t.Fatalf("config_migration_failure_code = %v", got)
	}
	if got := event.Properties["error_message"]; got != "configuration migration failed" {
		t.Fatalf("error_message = %v", got)
	}
	if _, exists := event.Properties["output_tail"]; exists {
		t.Fatalf("safe diagnostic failure retained output tail: %#v", event.Properties["output_tail"])
	}
}

func TestCompleteEmitsOnlyOneTerminalEvent(t *testing.T) {
	rec := testRecorder()
	run := testCommandRun(rec)

	run.Complete(nil)
	run.Complete(errors.New("late duplicate completion"))

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if got := len(rec.events); got != 1 {
		t.Fatalf("terminal event count = %d, want 1", got)
	}
	if got := rec.events[0].Event; got != CliCommandCompletedEvent {
		t.Fatalf("terminal event = %q, want %q", got, CliCommandCompletedEvent)
	}
}

func TestObserveOutputEmitsNothingOnItsOwn(t *testing.T) {
	rec := testRecorder()
	run := testCommandRun(rec)

	for i := 0; i < 50; i++ {
		run.ObserveOutput("info", "ordinary progress line")
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	// Observing output must never stream events; a successful command
	// contributes no output volume at all.
	if len(rec.events) != 0 {
		t.Fatalf("ObserveOutput captured %d events, want 0", len(rec.events))
	}
}

func TestFailureTailIsSanitizedAndBounded(t *testing.T) {
	rec := testRecorder()
	run := testCommandRun(rec)

	run.ObserveOutput("error", "Authorization: Bearer abcdef0123456789")
	run.ObserveOutput("error", "contact ops@revyl.ai at https://internal.example.com/x")
	for i := 0; i < maxOutputTail+10; i++ {
		run.ObserveOutput("info", "filler line")
	}

	run.Complete(errors.New("token=hunter2 failed to reach device"))

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.events) != 1 {
		t.Fatalf("captured %d events, want 1", len(rec.events))
	}
	props := rec.events[0].Properties

	message, _ := props["error_message"].(string)
	if strings.Contains(message, "hunter2") {
		t.Fatalf("error_message leaked a secret: %q", message)
	}

	tail, ok := props["output_tail"].([]map[string]interface{})
	if !ok {
		t.Fatalf("output_tail missing or wrong type: %T", props["output_tail"])
	}
	if len(tail) > maxOutputTail {
		t.Fatalf("output_tail has %d entries, want at most %d", len(tail), maxOutputTail)
	}
	for _, entry := range tail {
		line, _ := entry["message"].(string)
		for _, leak := range []string{"abcdef0123456789", "ops@revyl.ai", "https://"} {
			if strings.Contains(line, leak) {
				t.Fatalf("output_tail leaked %q in %q", leak, line)
			}
		}
	}
}

func TestFailureDiagnosticsRedactCommandInputs(t *testing.T) {
	rec := testRecorder()
	cmd := &cobra.Command{Use: "run <name|id>"}
	cmd.Flags().String("build", "", "")
	cmd.Flags().Bool("json", false, "")
	if err := cmd.Flags().Set("build", "customer-build-123"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	run := rec.StartCommand(cmd, []string{"customer-test-name"})

	run.ObserveOutput(
		"error",
		"test 'customer-test-name' failed against customer-build-123",
	)
	run.Complete(errors.New("customer-test-name was not found"))

	event := lastEvent(t, rec)
	message, _ := event.Properties["error_message"].(string)
	if strings.Contains(message, "customer-test-name") {
		t.Fatalf("error_message leaked a positional value: %q", message)
	}
	tail, ok := event.Properties["output_tail"].([]map[string]interface{})
	if !ok || len(tail) != 1 {
		t.Fatalf("output_tail missing or wrong type: %T", event.Properties["output_tail"])
	}
	line, _ := tail[0]["message"].(string)
	for _, leak := range []string{"customer-test-name", "customer-build-123"} {
		if strings.Contains(line, leak) {
			t.Fatalf("output_tail leaked command input %q in %q", leak, line)
		}
	}
	if strings.Contains(line, "<command-input>") == false {
		t.Fatalf("output_tail did not preserve a redaction marker: %q", line)
	}
}

func TestFailureDiagnosticsKeepUnrelatedShortValuesIntact(t *testing.T) {
	message := sanitizeDiagnosticString(
		"simulator timed out after 30s; retry count 3",
		[]string{"3"},
	)

	if !strings.Contains(message, "30s") {
		t.Fatalf("short redaction corrupted an unrelated duration: %q", message)
	}
	if strings.Contains(message, "count 3") {
		t.Fatalf("standalone command input was not redacted: %q", message)
	}
}

func TestSanitizeStringTruncatesAtRuneBoundary(t *testing.T) {
	value := strings.Repeat("🙂", maxSanitizedStringLength+1)
	sanitized := sanitizeString(value)

	if !utf8.ValidString(sanitized) {
		t.Fatalf("sanitized value is not valid UTF-8: %q", sanitized)
	}
	if !strings.HasSuffix(sanitized, "...<truncated>") {
		t.Fatalf("sanitized value did not retain truncation marker: %q", sanitized)
	}
}

func TestCompletedErrorUnwrapsOriginalError(t *testing.T) {
	original := errors.New("workflow had 1 failed tests")
	err := CompletedWithExitCode(original, CommandCompletion{
		ExitCode:     1,
		Domain:       "workflow_run",
		DomainStatus: "failed",
	})

	if !errors.Is(err, original) {
		t.Fatalf("expected completed error to unwrap original error")
	}
	if err.Error() != original.Error() {
		t.Fatalf("error text = %q, want %q", err.Error(), original.Error())
	}
}

func TestCompleteUsesWorkflowCompletedError(t *testing.T) {
	rec := testRecorder()
	run := testCommandRun(rec)
	err := CompletedWithExitCode(errors.New("workflow had 1 failed tests"), CommandCompletion{
		ExitCode:     1,
		Domain:       "workflow_run",
		DomainStatus: "failed",
	})
	run.Complete(err)

	event := lastEvent(t, rec)
	if event.Event != "cli_command_completed" {
		t.Fatalf("event = %q, want cli_command_completed", event.Event)
	}
	if got := event.Properties["domain"]; got != "workflow_run" {
		t.Fatalf("domain = %v, want workflow_run", got)
	}
}

func testRecorder() *Recorder {
	return &Recorder{
		enabled:   true,
		flush:     func(TelemetryPayload) {},
		baseProps: map[string]interface{}{"service": "revyl-cli"},
	}
}

func testCommandRun(rec *Recorder) *CommandRun {
	return &CommandRun{
		rec:       rec,
		startedAt: time.Now(),
		commandID: "command-123",
		props:     map[string]interface{}{"command": "revyl test run"},
	}
}

func lastEvent(t *testing.T, rec *Recorder) TelemetryEvent {
	t.Helper()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.events) == 0 {
		t.Fatal("expected recorded event")
	}
	return rec.events[len(rec.events)-1]
}
