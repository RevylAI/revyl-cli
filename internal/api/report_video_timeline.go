package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// GetReportVideoTimelineBySession returns a device session report's actions placed on its
// recording, with optional suggested clip windows.
func (c *Client) GetReportVideoTimelineBySession(ctx context.Context, sessionID string) (*ReportVideoTimelineResponse, error) {
	return c.getReportVideoTimeline(ctx, fmt.Sprintf("/api/v1/reports-v3/reports/by-session/%s/video-timeline", url.PathEscape(sessionID)))
}

// GetReportVideoTimelineByExecution returns a test execution report's actions placed on its
// recording, with optional suggested clip windows.
func (c *Client) GetReportVideoTimelineByExecution(ctx context.Context, executionID string) (*ReportVideoTimelineResponse, error) {
	return c.getReportVideoTimeline(ctx, fmt.Sprintf("/api/v1/reports-v3/reports/by-execution/%s/video-timeline", url.PathEscape(executionID)))
}

func (c *Client) getReportVideoTimeline(ctx context.Context, path string) (*ReportVideoTimelineResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var timeline ReportVideoTimelineResponse
	if err := parseResponse(resp, &timeline); err != nil {
		return nil, err
	}
	return &timeline, nil
}
