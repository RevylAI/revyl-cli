package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const edgeBlockedHTML = "<!DOCTYPE html><html><head><title>Attention Required! | Cloudflare</title></head><body>private-canary</body></html>"

func newEdgeServer(t *testing.T, status int, contentType, ray, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		if ray != "" {
			w.Header().Set("cf-ray", ray)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func requireEdgeBlocked(t *testing.T, err error, ray string) {
	t.Helper()
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiErr.Code != EdgeBlockedCode || apiErr.StatusCode != http.StatusForbidden {
		t.Fatalf("code/status = %q/%d, want %q/403", apiErr.Code, apiErr.StatusCode, EdgeBlockedCode)
	}
	message := err.Error()
	for _, want := range []string{"blocked at Revyl's edge, not a client error", "cf-ray " + ray, "support"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not contain %q", message, want)
		}
	}
	for _, leaked := range []string{"private-canary", "<html", "Cloudflare</title>"} {
		if strings.Contains(message, leaked) {
			t.Fatalf("error %q leaks HTML body text %q", message, leaked)
		}
	}
}

func TestSharedErrorPathMapsCloudflareHTML403ToEdgeBlocked(t *testing.T) {
	server := newEdgeServer(t, http.StatusForbidden, "text/html; charset=UTF-8", "8f1a2b3c4d5e6f70-SJC", edgeBlockedHTML)
	client := NewClientWithBaseURL("test-key", server.URL)

	_, listErr := client.ListOrgLaunchVariables(context.Background())
	requireEdgeBlocked(t, listErr, "8f1a2b3c4d5e6f70-SJC")

	_, reportErr := client.GetReportContextByExecution(context.Background(), "exec-1", false, false, false)
	requireEdgeBlocked(t, reportErr, "8f1a2b3c4d5e6f70-SJC")
}

func TestComputerRequestsMapCloudflareHTML403ToEdgeBlocked(t *testing.T) {
	server := newEdgeServer(t, http.StatusForbidden, "text/html", "8f1a2b3c4d5e6f70-SJC", edgeBlockedHTML)

	_, err := NewClientWithBaseURL("test-key", server.URL).GetComputerRun(context.Background(), uuid.New())

	requireEdgeBlocked(t, err, "8f1a2b3c4d5e6f70-SJC")
}

func TestEdgeBlockedOmitsMalformedRayFromHint(t *testing.T) {
	server := newEdgeServer(t, http.StatusForbidden, "text/html", "<script>x</script>", edgeBlockedHTML)

	_, err := NewClientWithBaseURL("test-key", server.URL).ListOrgLaunchVariables(context.Background())

	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != EdgeBlockedCode {
		t.Fatalf("error = %v, want edge_blocked", err)
	}
	if strings.Contains(err.Error(), "script") || strings.Contains(err.Error(), "cf-ray") {
		t.Fatalf("error %q echoes an unvalidated cf-ray header", err.Error())
	}
}

func TestNonEdgeResponsesKeepStandardErrorMapping(t *testing.T) {
	for name, tc := range map[string]struct {
		status      int
		contentType string
		ray         string
		body        string
	}{
		"json 403 behind cloudflare": {http.StatusForbidden, "application/json", "8f1a2b3c4d5e6f70-SJC", `{"detail":"Not allowed"}`},
		"html 403 without cf-ray":    {http.StatusForbidden, "text/html", "", edgeBlockedHTML},
		"html 502 with cf-ray":       {http.StatusBadGateway, "text/html", "8f1a2b3c4d5e6f70-SJC", edgeBlockedHTML},
	} {
		t.Run(name, func(t *testing.T) {
			server := newEdgeServer(t, tc.status, tc.contentType, tc.ray, tc.body)
			client := NewClientWithBaseURL("test-key", server.URL)
			client.maxRetries = 0

			_, err := client.ListOrgLaunchVariables(context.Background())

			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error = %v, want *APIError", err)
			}
			if apiErr.Code == EdgeBlockedCode || apiErr.StatusCode != tc.status {
				t.Fatalf("code/status = %q/%d, want standard mapping with status %d", apiErr.Code, apiErr.StatusCode, tc.status)
			}
		})
	}
}
