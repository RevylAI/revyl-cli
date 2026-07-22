package mcp

import "github.com/revyl/cli/internal/outcome"

const (
	LiveStepOutcomeFailed            = outcome.LiveStepFailed
	LiveStepOutcomeMalformed         = outcome.LiveStepMalformed
	LiveStepOutcomeValidationFailed  = outcome.LiveStepValidationFailed
	LiveStepOutcomeValidationMissing = outcome.LiveStepValidationMissing
)

// LiveStepOutcomeError aliases the shared CLI/MCP semantic failure.
type LiveStepOutcomeError = outcome.LiveStepError

// EvaluateLiveStepOutcome validates the semantic result of a completed worker step.
//
// Parameters:
//   - response: Completed worker response.
//   - expectedStepType: Step type requested by the caller.
//
// Returns:
//   - nil when the step achieved its requested outcome.
//   - *LiveStepOutcomeError when output is failed, malformed, or lacks a verdict.
func EvaluateLiveStepOutcome(response *LiveStepResponse, expectedStepType string) error {
	if response == nil {
		return &outcome.LiveStepError{
			Code:   outcome.LiveStepMalformed,
			Reason: "live step returned no response",
		}
	}
	return outcome.EvaluateLiveStep(
		response.Success,
		expectedStepType,
		response.StepType,
		response.StepOutput,
	)
}
