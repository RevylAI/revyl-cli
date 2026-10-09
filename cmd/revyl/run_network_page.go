package main

import (
	"net/url"
	"sort"

	"github.com/revyl/cli/internal/runinspect"
)

func networkRequestPage(requests []runinspect.NetworkRequest, minimumDurationMs float64, slowestFirst, compact bool, limit, offset int) ([]runinspect.NetworkRequest, int, bool) {
	matched := make([]runinspect.NetworkRequest, 0, len(requests))
	for _, request := range requests {
		if request.DurationMs >= minimumDurationMs {
			matched = append(matched, request)
		}
	}
	if slowestFirst {
		sort.SliceStable(matched, func(i, j int) bool { return matched[i].DurationMs > matched[j].DurationMs })
	}
	total := len(matched)
	start := min(offset, total)
	end := total
	if limit > 0 && limit < total-start {
		end = start + limit
	}
	page := matched[start:end]
	if compact {
		for i := range page {
			request := &page[i]
			request.RequestHeaders, request.ResponseHeaders = nil, nil
			request.RequestBodyPreview, request.ResponseBodyPreview = nil, nil
			request.RequestBodyTruncated, request.ResponseBodyTruncated = nil, nil
			parsed, err := url.Parse(request.URL)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
				request.URL = "[invalid captured URL]"
			} else {
				parsed.User, parsed.RawQuery, parsed.Fragment = nil, "", ""
				request.URL = parsed.String()
			}
		}
	}
	return page, total, end < total
}
