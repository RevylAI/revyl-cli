package api

import "math"

func (step ReportContextStepResponse) RecordedCodeExecutionTimeoutSeconds() *int {
	if step.StepType != "code_execution" || step.TypeData == nil {
		return nil
	}
	seconds, ok := (*step.TypeData)["timeout_seconds"].(float64)
	if !ok || seconds < 1 || seconds > 300 || math.Trunc(seconds) != seconds {
		return nil
	}
	value := int(seconds)
	return &value
}
