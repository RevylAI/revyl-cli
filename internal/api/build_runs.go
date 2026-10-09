package api

import (
	"context"
	"fmt"
)

type BuildRun struct {
	ExecutionID          string         `json:"execution_id"`
	TestID               *string        `json:"test_id"`
	TestName             *string        `json:"test_name"`
	Status               *string        `json:"status"`
	Success              *bool          `json:"success"`
	StartedAt            *string        `json:"started_at"`
	CompletedAt          *string        `json:"completed_at"`
	ExecutionTimeSeconds *float64       `json:"execution_time_seconds"`
	Source               *string        `json:"source"`
	ResolvedBuildVersion *string        `json:"resolved_build_version"`
	Metadata             map[string]any `json:"metadata"`
	ReportMetadata       map[string]any `json:"report_metadata"`
}

type BuildRunsPage struct {
	Items       []BuildRun `json:"items"`
	Total       int        `json:"total"`
	Page        int        `json:"page"`
	PageSize    int        `json:"page_size"`
	TotalPages  int        `json:"total_pages"`
	HasNext     bool       `json:"has_next"`
	HasPrevious bool       `json:"has_previous"`
}

func (c *Client) GetBuildRuns(ctx context.Context, buildID string, page, limit int) (*BuildRunsPage, error) {
	response, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/v1/apps/builds/%s/runs?page=%d&page_size=%d", buildID, page, limit), nil)
	if err != nil {
		return nil, err
	}
	var result BuildRunsPage
	if err = parseResponse(response, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
