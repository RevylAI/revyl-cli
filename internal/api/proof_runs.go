package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

const (
	proofRunsPath        = "/api/v1/scm/proof-runs"
	proofRunPathTemplate = "/api/v1/scm/proof-runs/%s"
	proofRunCancelPath   = "/api/v1/scm/proof-runs/%s/cancel"
)

// CreateProofRun starts one proof run. The request is sent exactly once
// because the endpoint is non-idempotent and a transport retry could start a
// second run.
func (c *Client) CreateProofRun(
	ctx context.Context,
	request *ProofRunCreateRequest,
) (*ProofRun, error) {
	resp, err := c.doRequestOnce(ctx, http.MethodPost, proofRunsPath, request)
	if err != nil {
		return nil, err
	}

	var result ProofRun
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetProofRun returns the current state of one proof run.
func (c *Client) GetProofRun(ctx context.Context, runID string) (*ProofRun, error) {
	path := fmt.Sprintf(proofRunPathTemplate, url.PathEscape(runID))
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var result ProofRun
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CancelProofRun cancels a queued or running proof run and returns it as
// stored afterwards. Cancelling a finished run changes nothing, so a retried
// request is safe.
func (c *Client) CancelProofRun(ctx context.Context, runID string) (*ProofRun, error) {
	path := fmt.Sprintf(proofRunCancelPath, url.PathEscape(runID))
	resp, err := c.doRequest(ctx, http.MethodPost, path, nil)
	if err != nil {
		return nil, err
	}

	var result ProofRun
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
