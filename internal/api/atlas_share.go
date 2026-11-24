package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

type AtlasShareScope struct {
	BuildID          string
	BranchID         string
	FromTime         string
	ToTime           string
	RecentBuildLimit int
}

func (c *Client) CreateAtlasShareLink(ctx context.Context, appID string, req *AtlasShareLinkRequest) (*AtlasShareLinkStatusResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/api/v1/atlas/v2/apps/%s/share", url.PathEscape(appID)), req)
	if err != nil {
		return nil, err
	}
	var result AtlasShareLinkStatusResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetAtlasShareLinkStatus(ctx context.Context, appID string, scope AtlasShareScope) (*AtlasShareLinkStatusResponse, error) {
	values := url.Values{}
	for name, value := range map[string]string{
		"build_id":  scope.BuildID,
		"branch_id": scope.BranchID,
		"from_time": scope.FromTime,
		"to_time":   scope.ToTime,
	} {
		if value != "" {
			values.Set(name, value)
		}
	}
	if scope.RecentBuildLimit > 0 {
		values.Set("recent_build_limit", strconv.Itoa(scope.RecentBuildLimit))
	}
	path := fmt.Sprintf("/api/v1/atlas/v2/apps/%s/share", url.PathEscape(appID))
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var result AtlasShareLinkStatusResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RevokeAtlasShareLink revokes one share link. A transport error is replaced
// by a generic one because it embeds the request URL, which carries the token.
func (c *Client) RevokeAtlasShareLink(ctx context.Context, appID, token string) (*AtlasShareLinkStatusResponse, error) {
	path := fmt.Sprintf("/api/v1/atlas/v2/apps/%s/share/%s", url.PathEscape(appID), url.PathEscape(token))
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			return nil, err
		}
		return nil, errors.New("revoke Atlas share link: the request failed before the server responded")
	}
	var result AtlasShareLinkStatusResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
