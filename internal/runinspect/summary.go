// summary.go owns the projection of a fetched report (+ identity)
// into the cross-surface Summary shape, plus the timeout/cache/error
// policy for loading run artifacts in a single round.
//
// Single owner: every CLI subcommand under `revyl run *` and every
// `get_run_*` MCP tool goes through LoadArtifacts/BuildSummary, so
// adding logs/network/perf as future subcommands inherits the same
// fetch contract.

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
	TaskID             string          `json:"task_id,omitempty"`
	TestID             string          `json:"test_id,omitempty"`
	TestName           string          `json:"test_name,omitempty"`
	Platform           string          `json:"platform,omitempty"`
	RunSuccess         *bool           `json:"run_success,omitempty"`
	DurationSeconds    *float64        `json:"duration_seconds,omitempty"`
	TotalSteps         int             `json:"total_steps,omitempty"`
	FailedSteps        int             `json:"failed_steps,omitempty"`
	FailedStepIndex    *int            `json:"failed_step_index,omitempty"`
	Steps              []SummaryStep   `json:"steps,omitempty"`
	Artifacts          Artifacts       `json:"artifacts"`
	IdentityHighlights []IdentityField `json:"identity_highlights,omitempty"`
}

// SummaryStep is the per-step shape inside Summary.
type SummaryStep struct {
	Index        int    `json:"index"`
	StepType     string `json:"step_type"`
	Description  string `json:"description,omitempty"`
	Status       string `json:"status"`
	StatusReason string `json:"status_reason,omitempty"`
}

// Artifacts lists which post-run artifacts are downloadable for the
// run. Expand as new artifacts ship (logs / network / perf are next).
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
		IdentityHighlights: identity,
	}
	for _, s := range report.Steps {
		out.Steps = append(out.Steps, SummaryStep{
			Index:        s.ExecutionOrder,
			StepType:     s.StepType,
			Description:  s.Description,
			Status:       s.Status,
			StatusReason: s.StatusReason,
		})
		if s.Status == "failed" && out.FailedStepIndex == nil {
			idx := s.ExecutionOrder
			out.FailedStepIndex = &idx
		}
	}
	return out
}

// LoadArtifacts fetches the report + device-state JSONL for a task in
// one timeout-bounded round. Returns the report with lines=nil when
// the run produced no device-state artifact (Android pre-sampler
// runs, runs killed before the upload phase).
func LoadArtifacts(
	ctx context.Context,
	client *api.Client,
	taskID string,
) (*Report, []DeviceStateLine, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	report, err := FetchReport(ctx, client, taskID)
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

// DetectIdentityHighlights returns the summary-view subset of identity
// fields (user / org / role + a couple of vendor IDs). Returns nil if
// there are no captured lines to read from.
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
