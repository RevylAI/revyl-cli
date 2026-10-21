package api

import (
	"time"

	"github.com/google/uuid"
)

type ComputerReportStatus struct{ State, Release, Evidence string }

func InspectComputerReport(report ComputerReport) ComputerReportStatus {
	return ComputerReportStatus{
		State:    computerString(report.State, "active"),
		Release:  computerString(report.Release, "pending"),
		Evidence: computerString(report.EvidenceWriteStatus, "recorded"),
	}
}

func computerString[T ~string](value *T, fallback string) string {
	if value == nil {
		return fallback
	}
	return string(*value)
}

func validComputerLifecycle(status ComputerReportStatus, created, expires, asOf time.Time, started, ended, released *time.Time) bool {
	if created.IsZero() || !expires.After(created) || asOf.Before(created) || (status.Release == "confirmed") != (released != nil) {
		return false
	}
	if status.State == "stopped" && ended == nil {
		return false
	}
	if (status.State == "active" || status.State == "closing" || status.State == "stopped") && started == nil {
		return false
	}
	if status.State == "starting" && (started != nil || ended != nil || status.Release != "pending") {
		return false
	}
	if ended != nil && started == nil {
		return false
	}
	if status.State == "active" && (ended != nil || status.Release != "pending") {
		return false
	}
	if status.Release != "pending" && status.State != "stopped" && status.State != "failed" && status.State != "owner_lost" {
		return false
	}
	for _, value := range []*time.Time{started, ended, released} {
		if value != nil && (value.Before(created) || value.After(asOf)) {
			return false
		}
	}
	if started != nil && ended != nil && ended.Before(*started) {
		return false
	}
	if released != nil && (started != nil && released.Before(*started) || ended != nil && released.Before(*ended)) {
		return false
	}
	return true
}

func validComputerReportStates(report ComputerReport) bool {
	status := InspectComputerReport(report)
	if !validComputerLifecycle(status, report.CreatedAt, report.ExpiresAt, report.AsOf, report.ExecutionStartedAt, report.ExecutionEndedAt, report.ReleasedAt) {
		return false
	}
	var steps []ComputerOperationEvidence
	if report.Steps != nil {
		steps = *report.Steps
	}
	if status.State == "starting" && len(steps) != 0 {
		return false
	}
	complete := (status.State == "stopped" || status.State == "failed") && status.Release == "confirmed" && (report.ExecutionStartedAt == nil || report.ExecutionEndedAt != nil) && status.Evidence == "recorded"
	seen := make(map[uuid.UUID]bool)
	for i, step := range steps {
		if !validComputerEvidence(step) || step.ExecutionOrder != i+1 || seen[step.StepId] || report.ExecutionStartedAt == nil || step.StartedAt.Before(*report.ExecutionStartedAt) || step.StartedAt.After(report.AsOf) || step.CompletedAt != nil && step.CompletedAt.After(report.AsOf) {
			return false
		}
		seen[step.StepId] = true
		outcome := computerString(step.Outcome, "started")
		if outcome == "started" || outcome == "unknown" {
			complete = false
		}
	}
	if report.ChildReports != nil {
		seenChildren := make(map[uuid.UUID]bool)
		for _, child := range *report.ChildReports {
			if seenChildren[child.ReportId] {
				return false
			}
			seenChildren[child.ReportId] = true
		}
	}
	return complete == (computerString(report.EvidenceCompleteness, "partial") == "complete")
}

func validComputerEvidence(step ComputerOperationEvidence) bool {
	if step.StepId == uuid.Nil || step.StartedAt.IsZero() {
		return false
	}
	outcome := computerString(step.Outcome, "started")
	if outcome == "started" && step.CompletedAt != nil || outcome != "started" && outcome != "unknown" && step.CompletedAt == nil || step.CompletedAt != nil && step.CompletedAt.Before(step.StartedAt) {
		return false
	}
	errorCode := computerString(step.ErrorCode, "")
	if outcome == "started" && (step.ErrorCode != nil || step.Summary != nil || step.Termination != nil || step.ObservedExitCode != nil) {
		return false
	}
	var exitCode *int
	if step.Summary != nil {
		kind, err := step.Summary.Discriminator()
		if err != nil || kind != string(step.Kind) {
			return false
		}
		value, err := step.Summary.ValueByDiscriminator()
		if err != nil {
			return false
		}
		switch summary := value.(type) {
		case ComputerExecSummary:
			if summary.StdoutByteCount > ComputerMaxOutputBytes-summary.StderrByteCount {
				return false
			}
			exitCode = &summary.ExitCode
		case ComputerProcessInspectSummary:
			if summary.State == "running" && summary.ExitCode != nil {
				return false
			}
		}
	}
	switch outcome {
	case "succeeded":
		if step.Summary == nil || step.ErrorCode != nil || exitCode != nil && *exitCode != 0 {
			return false
		}
	case "failed":
		if errorCode == "exec_nonzero" {
			if exitCode == nil || *exitCode == 0 {
				return false
			}
		} else if (errorCode != "not_found" && errorCode != "access_denied") || step.Summary != nil {
			return false
		}
	case "timed_out", "unknown":
		if step.Summary != nil || step.Termination == nil {
			return false
		}
		if outcome == "timed_out" && errorCode != "timed_out" {
			return false
		}
		if outcome == "unknown" {
			switch errorCode {
			case "lost_ack", "executor_error", "invalid_executor_result", "owner_lost", "stopped", "start_evidence_failed":
			default:
				return false
			}
		}
	}
	if outcome != "timed_out" && outcome != "unknown" && (step.Termination != nil || step.ObservedExitCode != nil) {
		return false
	}
	return step.ObservedExitCode == nil || step.Kind == "exec" && outcome == "timed_out"
}

func validComputerATIF(result ComputerATIFTrajectory) bool {
	if result.TrajectoryId != result.Extra.Revyl.ReportId {
		return false
	}
	seen := make(map[uuid.UUID]bool)
	for i, step := range result.Steps {
		if step.StepId != i+1 || step.Timestamp.IsZero() || step.LlmCallCount != nil && *step.LlmCallCount != 0 {
			return false
		}
		if step.Source == "system" {
			if step.ToolCalls != nil || step.Observation != nil || step.LlmCallCount != nil || step.Message == "Computer operation accepted" {
				return false
			}
			continue
		}
		if step.Source != "agent" || step.LlmCallCount == nil || step.ToolCalls == nil || len(*step.ToolCalls) != 1 || step.Message != "Computer operation accepted" {
			return false
		}
		call := (*step.ToolCalls)[0]
		if call.ToolCallId == uuid.Nil || seen[call.ToolCallId] || string(call.FunctionName) != "computer."+string(call.Arguments.Kind) {
			return false
		}
		seen[call.ToolCallId] = true
		if step.Observation != nil {
			if len(step.Observation.Results) != 1 {
				return false
			}
			observed := step.Observation.Results[0]
			evidence := observed.Extra.Revyl.Evidence
			if !validComputerEvidence(evidence) || observed.SourceCallId != call.ToolCallId || evidence.StepId != call.ToolCallId || string(evidence.Kind) != string(call.Arguments.Kind) || computerString(evidence.Outcome, "started") != string(observed.Content) || !evidence.StartedAt.Equal(step.Timestamp) {
				return false
			}
		}
	}
	return true
}
