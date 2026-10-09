package main

import (
	"testing"

	"github.com/revyl/cli/internal/runinspect"
)

func TestNetworkPageFiltersBeforeStablePagingWithoutMutatingCapture(t *testing.T) {
	body := "private body"
	requests := []runinspect.NetworkRequest{
		{URL: "https://example.test/fast", DurationMs: 5},
		{URL: "https://example.test/first", DurationMs: 900},
		{URL: "https://user:secret@example.test/second?token=private#fragment", DurationMs: 900, RequestHeaders: map[string]string{"Authorization": "private"}, ResponseBodyPreview: &body},
		{URL: "https://example.test/third", DurationMs: 500},
	}
	page, total, more := networkRequestPage(requests, 100, true, true, 1, 1)
	if total != 3 || !more || len(page) != 1 || page[0].URL != "https://example.test/second" {
		t.Fatalf("unexpected filtered page: total=%d more=%v count=%d", total, more, len(page))
	}
	if page[0].RequestHeaders != nil || page[0].ResponseBodyPreview != nil {
		t.Fatal("compact page retained private headers or body")
	}
	if requests[2].RequestHeaders == nil || requests[2].ResponseBodyPreview == nil {
		t.Fatal("paging mutated the original capture")
	}
	page, total, more = networkRequestPage(requests, 100, true, true, 2, 3)
	if len(page) != 0 || total != 3 || more {
		t.Fatal("exhausted cursor must remain empty with its matching total")
	}
	page, total, more = networkRequestPage(requests, 0, false, false, 0, 0)
	if len(page) != 4 || total != 4 || more || page[2].ResponseBodyPreview == nil {
		t.Fatal("direct CLI defaults must preserve complete detail")
	}
}
