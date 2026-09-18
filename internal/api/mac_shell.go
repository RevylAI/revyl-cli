package api

import (
	"context"
	"fmt"
	"net/url"
)

func (c *Client) OpenMacShellSession(ctx context.Context) (*MacShellSession, error) {
	resp, err := c.doRequestOnce(ctx, "POST", "/api/v1/execution/mac/shell-sessions", nil)
	if err != nil {
		return nil, err
	}

	var result MacShellSession
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *Client) TerminateMacShellSession(ctx context.Context, sessionID string) error {
	resp, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/api/v1/execution/mac/shell-sessions/%s", url.PathEscape(sessionID)), nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}
