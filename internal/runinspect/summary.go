package runinspect

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/revyl/cli/internal/api"
)

// Summary is the consolidated "what happened on this run" output —
// shared by `revyl run summary` (CLI JSON mode) and the
// get_run_summary MCP tool.
type Summary struct {
	TaskID                 string                                `json:"task_id,omitempty"`
	TestID                 string                                `json:"test_id,omitempty"`
	TestName               string                                `json:"test_name,omitempty"`
	Platform               string                                `json:"platform,omitempty"`
	RunSuccess             *bool                                 `json:"run_success,omitempty"`
	DurationSeconds        *float64                              `json:"duration_seconds,omitempty"`
	TotalSteps             int                                   `json:"total_steps,omitempty"`
	FailedSteps            int                                   `json:"failed_steps,omitempty"`
	FailedStepIndex        *int                                  `json:"failed_step_index,omitempty"`
	Steps                  []SummaryStep                         `json:"steps,omitempty"`
	Artifacts              Artifacts                             `json:"artifacts"`
	IdentityHighlights     []IdentityField                       `json:"identity_highlights,omitempty"`
	RuntimeCheckerFindings []api.IOSRuntimeCheckerFindingSummary `json:"runtime_checker_findings,omitempty"`
}

// SummaryStep is the per-step shape inside Summary.
type SummaryStep struct {
	Index          int    `json:"index"`
	StepType       string `json:"step_type"`
	Description    string `json:"description,omitempty"`
	Status         string `json:"status"`
	StatusReason   string `json:"status_reason,omitempty"`
	TimeoutSeconds *int   `json:"timeout_seconds,omitempty"`
}

// Artifacts reports which run artifacts have a download available.
type Artifacts struct {
	DeviceStateAvailable     bool `json:"device_state_available"`
	NetworkRequestsAvailable bool `json:"network_requests_available"`
	HardwareMetricsAvailable bool `json:"hardware_metrics_available"`
	PerfettoTraceAvailable   bool `json:"perfetto_trace_available"`
}

// BuildSummary projects a Report (+ optional identity highlights) into
// the shared Summary shape.
func BuildSummary(report *Report, identity []IdentityField) Summary {
	out := Summary{
		TaskID:          report.TaskID,
		TestID:          report.TestID,
		TestName:        report.TestName,
		Platform:        report.Platform,
		RunSuccess:      report.Success,
		DurationSeconds: report.DurationSeconds,
		TotalSteps:      report.TotalSteps,
		FailedSteps:     report.FailedSteps,
		Artifacts: Artifacts{
			DeviceStateAvailable:     report.DeviceStateURL != "",
			NetworkRequestsAvailable: report.NetworkRequestsURL != "",
			HardwareMetricsAvailable: report.HardwareMetricsURL != "",
			PerfettoTraceAvailable:   report.PerfettoTraceURL != "",
		},
		IdentityHighlights:     identity,
		RuntimeCheckerFindings: report.RuntimeCheckerFindings,
	}
	for _, s := range report.Steps {
		out.Steps = append(out.Steps, SummaryStep{
			Index:          s.ExecutionOrder,
			StepType:       s.StepType,
			Description:    s.Description,
			Status:         s.Status,
			StatusReason:   s.StatusReason,
			TimeoutSeconds: s.TimeoutSeconds,
		})
		if s.Status == "failed" && out.FailedStepIndex == nil {
			idx := s.ExecutionOrder
			out.FailedStepIndex = &idx
		}
	}
	return out
}

// LoadArtifacts loads a report and its device state under a shared timeout.
// It returns the report with nil lines when no device-state URL is available.
func LoadArtifacts(
	ctx context.Context,
	client *api.Client,
	taskID string,
) (*Report, []DeviceStateLine, error) {
	return LoadArtifactsReference(ctx, client, taskID, "execution")
}

func LoadArtifactsReference(ctx context.Context, client *api.Client, taskID, kind string) (*Report, []DeviceStateLine, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	report, err := FetchReportReference(ctx, client, taskID, kind)
	if err != nil {
		if errors.Is(err, ErrReportNotFound) {
			return nil, nil, fmt.Errorf(
				"no report found for task %s — check the task_id and whether the run has completed",
				taskID,
			)
		}
		return nil, nil, err
	}

	httpClient := &http.Client{Timeout: 60 * time.Second}
	lines, err := FetchDeviceStateLines(
		ctx, report, httpClient, DefaultCacheDir(),
	)
	if err != nil {
		if errors.Is(err, ErrNoDeviceStateArtifact) {
			return report, nil, nil
		}
		return nil, nil, fmt.Errorf("fetch device-state artifact: %w", err)
	}
	return report, lines, nil
}

// DetectIdentityHighlights applies the summary filter to captured identity fields.
func DetectIdentityHighlights(
	lines []DeviceStateLine, report *Report,
) []IdentityField {
	if len(lines) == 0 {
		return nil
	}
	all := DetectIdentity(
		lines, IndexerFromReport(report), IdentityOptions{},
	)
	return SummaryHighlights(all)
}
