package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

const (
	explorationLaunchPathTemplate = "/api/v1/explorations/apps/%s"
	explorationRunPathTemplate    = "/api/v1/explorations/%s"
	explorationReportPathTemplate = "/api/v1/explorations/%s/report"
	explorationCancelPathTemplate = "/api/v1/explorations/%s/cancel"
)

// LaunchExploration starts one exploration run. The launch request is sent
// exactly once because the endpoint is non-idempotent and a transport retry
// could create a duplicate run.
func (c *Client) LaunchExploration(
	ctx context.Context,
	appID string,
	request *ExplorationLaunchRequest,
) (*ExplorationLaunchResponse, error) {
	path := fmt.Sprintf(explorationLaunchPathTemplate, url.PathEscape(appID))
	resp, err := c.doRequestOnce(ctx, http.MethodPost, path, request)
	if err != nil {
		return nil, err
	}

	var result ExplorationLaunchResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetExploration returns the current parent-run state.
func (c *Client) GetExploration(
	ctx context.Context,
	runID string,
) (*ExplorationRunResponse, error) {
	path := fmt.Sprintf(explorationRunPathTemplate, url.PathEscape(runID))
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var result ExplorationRunResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetExplorationReport returns the parent run plus per-explorer progress.
func (c *Client) GetExplorationReport(
	ctx context.Context,
	runID string,
) (*ExplorationRunReportResponse, error) {
	path := fmt.Sprintf(explorationReportPathTemplate, url.PathEscape(runID))
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var result ExplorationRunReportResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CancelExploration requests cancellation for the parent run and its lanes.
func (c *Client) CancelExploration(
	ctx context.Context,
	runID string,
) (*ExplorationCancelResponse, error) {
	path := fmt.Sprintf(explorationCancelPathTemplate, url.PathEscape(runID))
	resp, err := c.doRequest(ctx, http.MethodPost, path, nil)
	if err != nil {
		return nil, err
	}

	var result ExplorationCancelResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
