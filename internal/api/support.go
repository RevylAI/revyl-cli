package api

import (
	"context"
	"net/http"
)

func (c *Client) CreateSupportRequest(ctx context.Context, req *SupportRequest) (*SupportRequestResponse, error) {
	resp, err := c.doRequestOnce(ctx, http.MethodPost, "/api/v1/support/requests", req)
	if err != nil {
		return nil, err
	}
	var result SupportRequestResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
