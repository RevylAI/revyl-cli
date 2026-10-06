package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestUpcomingMinimumVersionHeaderNameMatchesBackend(t *testing.T) {
	if UpcomingMinimumVersionHeader != "X-Revyl-CLI-Upcoming-Minimum-Version" {
		t.Fatalf("header = %q; the backend sets X-Revyl-CLI-Upcoming-Minimum-Version", UpcomingMinimumVersionHeader)
	}
}

func TestBackendResponsesRecordAnnouncedUpcomingMinimumVersion(t *testing.T) {
	for _, test := range []struct {
		name   string
		header string
		want   string
	}{
		{name: "absent", header: "", want: ""},
		{name: "announced", header: "0.1.140", want: "0.1.140"},
		{name: "announced with prefix", header: " v0.1.140 ", want: "0.1.140"},
		{name: "malformed", header: "soon", want: ""},
		{name: "partial version", header: "0.1", want: ""},
	} {
		for _, path := range []struct {
			name string
			send func(*Client) (*http.Response, error)
		}{
			{name: "single request", send: func(c *Client) (*http.Response, error) {
				return c.doRequest(context.Background(), http.MethodGet, "/probe", nil)
			}},
			{name: "retrying request", send: func(c *Client) (*http.Response, error) {
				return c.doRequestWithRetry(context.Background(), http.MethodGet, "/probe", nil)
			}},
			{name: "proof comment", send: func(c *Client) (*http.Response, error) {
				return nil, c.PublishProofComment(context.Background(), "body", "", "")
			}},
			{name: "proof media", send: func(c *Client) (*http.Response, error) {
				image := filepath.Join(t.TempDir(), "screen.png")
				if err := os.WriteFile(image, []byte("png"), 0o600); err != nil {
					t.Fatal(err)
				}
				_, err := c.PublishSessionProofMedia(context.Background(), "session-1", image)
				return nil, err
			}},
		} {
			t.Run(test.name+"/"+path.name, func(t *testing.T) {
				upcomingMinimumVersion = atomic.Value{}
				t.Cleanup(func() { upcomingMinimumVersion = atomic.Value{} })
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if test.header != "" {
						w.Header().Set(UpcomingMinimumVersionHeader, test.header)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{}`))
				}))
				defer server.Close()

				resp, err := path.send(NewClientWithBaseURL("test-key", server.URL))
				if err != nil {
					t.Fatalf("request failed: %v", err)
				}
				if resp != nil {
					resp.Body.Close()
				}

				if got := UpcomingMinimumVersion(); got != test.want {
					t.Fatalf("UpcomingMinimumVersion() = %q, want %q", got, test.want)
				}
			})
		}
	}
}
