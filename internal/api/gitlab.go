package api

import (
	"context"
	"net/url"
)

func (c *Client) GetGitlabConnection(ctx context.Context) (*GitlabConnectionSummary, error) {
	response, err := c.doRequestWithRetry(ctx, "GET", "/api/v1/integrations/gitlab/connection", nil)
	if err != nil {
		return nil, err
	}
	var result GitlabConnectionSummary
	if err := parseResponse(response, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ConnectGitlab(ctx context.Context, request GitlabConnectRequest) (*GitlabConnectionSummary, error) {
	response, err := c.doRequestOnce(ctx, "PUT", "/api/v1/integrations/gitlab/connection", request)
	if err != nil {
		return nil, err
	}
	var result GitlabConnectionSummary
	if err := parseResponse(response, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) CheckGitlabAccess(ctx context.Context) error {
	response, err := c.doRequestWithRetry(ctx, "GET", "/api/v1/integrations/gitlab/repositories?per_page=1", nil)
	if err != nil {
		return err
	}
	var result GitlabRepositoryCatalog
	return parseResponse(response, &result)
}

func (c *Client) GetGitlabWebhook(ctx context.Context, namespace, repository string) (*GitlabWebhookStatus, error) {
	query := url.Values{"namespace": {namespace}, "repository_name": {repository}}
	response, err := c.doRequestWithRetry(ctx, "GET", "/api/v1/integrations/gitlab/webhook?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var result GitlabWebhookStatus
	if err := parseResponse(response, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
