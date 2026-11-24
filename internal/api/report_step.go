package api

import "math"

// RecordedCodeExecutionTimeoutSeconds reads the execution snapshot, never the current test configuration.
func (step ReportContextStepResponse) RecordedCodeExecutionTimeoutSeconds() *int {
	if step.StepType != "code_execution" || step.TypeData == nil {
		return nil
	}
	// OpenAPI exposes type_data as a JSON object; encoding/json decodes its numbers as float64.
	seconds, ok := (*step.TypeData)["timeout_seconds"].(float64)
	if !ok || seconds < 1 || seconds > 300 || math.Trunc(seconds) != seconds {
		return nil
	}
	value := int(seconds)
	return &value
}
