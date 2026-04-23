package main

import (
	"testing"

	mcppkg "github.com/revyl/cli/internal/mcp"
)

func TestClassifyLiveRequestType(t *testing.T) {
	jsonCT := "application/json"
	imageCT := "image/png"

	tests := []struct {
		name string
		item mcppkg.LiveNetworkRequestItem
		want string
	}{
		{
			name: "auth request wins",
			item: mcppkg.LiveNetworkRequestItem{
				URL:         "https://example.com/oauth/token",
				Method:      "POST",
				IsAuth:      true,
				ContentType: &jsonCT,
			},
			want: "auth",
		},
		{
			name: "json content type is api",
			item: mcppkg.LiveNetworkRequestItem{
				URL:         "https://example.com/api/users",
				Method:      "GET",
				ContentType: &jsonCT,
			},
			want: "api",
		},
		{
			name: "image content type is img",
			item: mcppkg.LiveNetworkRequestItem{
				URL:         "https://cdn.example.com/avatar",
				Method:      "GET",
				ContentType: &imageCT,
			},
			want: "img",
		},
		{
			name: "fallback to other",
			item: mcppkg.LiveNetworkRequestItem{
				URL:    "https://example.com/unknown",
				Method: "GET",
			},
			want: "other",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyLiveRequestType(tt.item)
			if got != tt.want {
				t.Fatalf("classifyLiveRequestType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatLiveRequestHelpers(t *testing.T) {
	videoRelative := 2.5
	item := mcppkg.LiveNetworkRequestItem{
		URL:            "https://example.com/some/really/long/path/that/needs/truncation?with=query",
		Method:         "get",
		StatusCode:     201,
		VideoRelativeS: &videoRelative,
		StartTimeS:     8.0,
	}

	if got := formatLiveRequestStart(item); got != "+2.5s" {
		t.Fatalf("formatLiveRequestStart() = %q, want +2.5s", got)
	}
	if got := liveRequestStatus(item); got != "201" {
		t.Fatalf("liveRequestStatus() = %q, want 201", got)
	}
	if got := formatLiveRequestBytes(2048); got != "2.0KB" {
		t.Fatalf("formatLiveRequestBytes() = %q, want 2.0KB", got)
	}
	if got := truncateLiveRequestURL(item.URL, 24); got != "https://example.com/s..." {
		t.Fatalf("truncateLiveRequestURL() = %q", got)
	}
}
