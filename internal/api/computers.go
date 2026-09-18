package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

func (c *Client) ListComputers(ctx context.Context) (*CustomerComputerList, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/execution/computers", nil)
	if err != nil {
		return nil, err
	}

	var result CustomerComputerList
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) OpenComputerShellSession(ctx context.Context, instanceID string) (*MacShellSession, error) {
	path := fmt.Sprintf("/api/v1/execution/computers/%s/shell-sessions", url.PathEscape(instanceID))
	resp, err := c.doRequestOnce(ctx, http.MethodPost, path, nil)
	if err != nil {
		return nil, err
	}

	var result MacShellSession
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
