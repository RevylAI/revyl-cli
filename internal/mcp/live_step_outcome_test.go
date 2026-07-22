package mcp

import (
	"errors"
	"testing"
)

func TestEvaluateLiveStepOutcomeValidationPasses(t *testing.T) {
	t.Parallel()

	response := &LiveStepResponse{
		Success:    true,
		StepType:   "validation",
		StepOutput: []byte(`{"status":"success","validation_result":true}`),
	}

	if err := EvaluateLiveStepOutcome(response, "validation"); err != nil {
		t.Fatalf("EvaluateLiveStepOutcome() error = %v", err)
	}
}

func TestEvaluateLiveStepOutcomeValidationFails(t *testing.T) {
	t.Parallel()

	response := &LiveStepResponse{
		Success:    true,
		StepType:   "validation",
		StepOutput: []byte(`{"status":"success","validation_result":false}`),
	}

	err := EvaluateLiveStepOutcome(response, "validation")
	requireLiveStepOutcomeCode(t, err, LiveStepOutcomeValidationFailed)
}

func TestEvaluateLiveStepOutcomeValidationRequiresVerdict(t *testing.T) {
	t.Parallel()

	response := &LiveStepResponse{
		Success:    true,
		StepType:   "validation",
		StepOutput: []byte(`{"status":"success"}`),
	}

	err := EvaluateLiveStepOutcome(response, "validation")
	requireLiveStepOutcomeCode(t, err, LiveStepOutcomeValidationMissing)
}

func TestEvaluateLiveStepOutcomeRejectsMalformedOutput(t *testing.T) {
	t.Parallel()

	response := &LiveStepResponse{
		Success:    true,
		StepType:   "validation",
		StepOutput: []byte(`{`),
	}

	err := EvaluateLiveStepOutcome(response, "validation")
	requireLiveStepOutcomeCode(t, err, LiveStepOutcomeMalformed)
}

func TestEvaluateLiveStepOutcomePreservesWorkerFailureReason(t *testing.T) {
	t.Parallel()

	response := &LiveStepResponse{
		Success:    false,
		StepType:   "validation",
		StepOutput: []byte(`{"status":"failed","status_reason":"worker rejected step"}`),
	}

	err := EvaluateLiveStepOutcome(response, "validation")
	requireLiveStepOutcomeCode(t, err, LiveStepOutcomeFailed)
	if err.Error() != "worker rejected step" {
		t.Fatalf("error = %q, want worker reason", err)
	}
}

// requireLiveStepOutcomeCode verifies an error has the expected semantic code.
func requireLiveStepOutcomeCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("EvaluateLiveStepOutcome() error = nil, want %s", code)
	}
	var outcomeErr *LiveStepOutcomeError
	if !errors.As(err, &outcomeErr) {
		t.Fatalf("error type = %T, want *LiveStepOutcomeError", err)
	}
	if outcomeErr.Code != code {
		t.Fatalf("outcome code = %q, want %q", outcomeErr.Code, code)
	}
}
