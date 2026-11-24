package mcp

import "github.com/revyl/cli/internal/outcome"

const (
	LiveStepOutcomeFailed            = outcome.LiveStepFailed
	LiveStepOutcomeMalformed         = outcome.LiveStepMalformed
	LiveStepOutcomeValidationFailed  = outcome.LiveStepValidationFailed
	LiveStepOutcomeValidationMissing = outcome.LiveStepValidationMissing
)

type LiveStepOutcomeError = outcome.LiveStepError

// EvaluateLiveStepOutcome validates the semantic outcome of a completed worker step.
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
