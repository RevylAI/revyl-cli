// Package outcome defines semantic results shared by CLI and MCP renderers.
package outcome

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	LiveStepFailed            = "step_failed"
	LiveStepMalformed         = "malformed_step_output"
	LiveStepValidationFailed  = "validation_failed"
	LiveStepValidationMissing = "validation_result_missing"
)

// LiveStepError describes a semantic step failure after transport succeeded.
type LiveStepError struct {
	Code   string
	Reason string
}

// Error returns the stable semantic failure reason.
func (e *LiveStepError) Error() string {
	if e == nil || strings.TrimSpace(e.Reason) == "" {
		return "live step failed"
	}
	return e.Reason
}

type liveStepSummary struct {
	Status           string `json:"status"`
	StatusReason     string `json:"status_reason"`
	ValidationResult *bool  `json:"validation_result,omitempty"`
}

// EvaluateLiveStep distinguishes transport completion from the requested outcome.
func EvaluateLiveStep(
	transportSuccess bool,
	expectedStepType string,
	actualStepType string,
	stepOutput json.RawMessage,
) error {
	var summary liveStepSummary
	if len(stepOutput) > 0 {
		if err := json.Unmarshal(stepOutput, &summary); err != nil {
			return &LiveStepError{
				Code:   LiveStepMalformed,
				Reason: fmt.Sprintf("live step returned malformed output: %v", err),
			}
		}
	}
	if !transportSuccess {
		reason := strings.TrimSpace(summary.StatusReason)
		if reason == "" {
			reason = "live step failed"
		}
		return &LiveStepError{Code: LiveStepFailed, Reason: reason}
	}

	stepType := strings.TrimSpace(expectedStepType)
	if stepType == "" {
		stepType = strings.TrimSpace(actualStepType)
	}
	if stepType != "validation" {
		return nil
	}
	if summary.ValidationResult == nil {
		return &LiveStepError{
			Code:   LiveStepValidationMissing,
			Reason: "validation completed without a validation result",
		}
	}
	if !*summary.ValidationResult {
		reason := strings.TrimSpace(summary.StatusReason)
		if reason == "" {
			reason = "validation failed"
		}
		return &LiveStepError{Code: LiveStepValidationFailed, Reason: reason}
	}
	return nil
}
