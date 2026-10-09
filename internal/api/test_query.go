package api

import (
	"context"
	"net/url"
	"strconv"
)

type TestQuery struct {
	Limit, Offset           int
	Search, Platform, AppID string
}

func (c *Client) QueryTests(ctx context.Context, query TestQuery) (*CLITestListWithTagsResponse, error) {
	values := url.Values{
		"limit": {strconv.Itoa(query.Limit)}, "offset": {strconv.Itoa(query.Offset)},
		"sort_by": {"name"}, "sort_dir": {"asc"},
	}
	for key, value := range map[string]string{"search": query.Search, "platform": query.Platform, "app_id": query.AppID} {
		if value != "" {
			values.Set(key, value)
		}
	}
	response, err := c.doRequest(ctx, "GET", "/api/v1/tests/get_tests?"+values.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var page CLITestListWithTagsResponse
	if err := parseResponse(response, &page); err != nil {
		return nil, err
	}
	return &page, nil
}
