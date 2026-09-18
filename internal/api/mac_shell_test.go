package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestTerminateMacShellSession(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotFound, http.StatusForbidden, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete || r.URL.EscapedPath() != "/api/v1/execution/mac/shell-sessions/test-session%3Fprivate=value" || r.URL.RawQuery != "" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
				}
				if r.Header.Get("Authorization") != "Bearer test-key" {
					t.Error("missing Revyl authorization")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || len(body) != 0 {
					t.Error("termination request must not contain caller-supplied targeting")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				if status >= 400 {
					_, _ = io.WriteString(w, `{"detail":"Session unavailable"}`)
				}
			}))
			defer server.Close()
			client := NewClientWithBaseURL("test-key", server.URL)
			client.maxRetries = 0
			err := client.TerminateMacShellSession(context.Background(), "test-session?private=value")
			if status == http.StatusNoContent {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
					t.Fatalf("termination error = %v, want HTTP %d", err, status)
				}
			}
		})
	}
}

func TestTerminateMacShellSessionHonorsDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := NewClientWithBaseURL("test-key", server.URL).TerminateMacShellSession(ctx, "test-session")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("termination error = %v, want deadline exceeded", err)
	}
}

func TestOpenMacShellSessionDoesNotRetryMinting(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	_, err := NewClientWithBaseURL("test-key", server.URL).OpenMacShellSession(context.Background())
	if err == nil || calls.Load() != 1 {
		t.Fatalf("failed minting: err=%v, calls=%d; must not retry creating a session", err, calls.Load())
	}
}
