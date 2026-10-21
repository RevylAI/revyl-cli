package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestComputerMutationRefusesRedirects(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, crossOrigin := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/cross-origin=%t", status, crossOrigin), func(t *testing.T) {
				var sourceRequests, destinationRequests, destinationMutations, forwardedCredentials atomic.Int32
				destination := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					destinationRequests.Add(1)
					if r.Method != http.MethodGet && r.Method != http.MethodHead {
						destinationMutations.Add(1)
					}
					if r.Header.Get("Authorization") != "" {
						forwardedCredentials.Add(1)
					}
					w.WriteHeader(http.StatusNoContent)
				})
				other := httptest.NewServer(destination)
				defer other.Close()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/destination" {
						destination.ServeHTTP(w, r)
						return
					}
					sourceRequests.Add(1)
					if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer fixture-key" {
						t.Error("source request lost its mutation or authentication")
					}
					location := "/destination?private-location-canary"
					if crossOrigin {
						location = other.URL + location
					}
					w.Header().Set("Location", location)
					w.WriteHeader(status)
					_, _ = io.WriteString(w, "private-response-canary")
				}))
				defer server.Close()
				var originalRedirects atomic.Int32
				original := &http.Client{
					Timeout: 7 * time.Second,
					CheckRedirect: func(*http.Request, []*http.Request) error {
						originalRedirects.Add(1)
						return nil
					},
				}
				client := NewClientWithBaseURL("fixture-key", server.URL)
				client.SetHTTPClient(original)
				response, err := client.doComputerMutationOnce(context.Background(), "/mutation", []byte(`{}`), "fixture-request", time.Second)
				var apiErr *APIError
				if response != nil || !errors.As(err, &apiErr) || apiErr.Code != "computer_redirect_refused" || apiErr.StatusCode != status {
					t.Fatalf("redirect result = %v, %v", response, err)
				}
				if sourceRequests.Load() != 1 || destinationRequests.Load() != 0 || destinationMutations.Load() != 0 || forwardedCredentials.Load() != 0 {
					t.Fatalf("request counts: source=%d destination=%d mutations=%d credentials=%d", sourceRequests.Load(), destinationRequests.Load(), destinationMutations.Load(), forwardedCredentials.Load())
				}
				if original.Timeout != 7*time.Second || originalRedirects.Load() != 0 {
					t.Fatal("computer mutation changed or used the shared redirect policy")
				}
				assertComputerTransportErrorIsSafe(t, err)
				if err := original.CheckRedirect(nil, nil); err != nil || originalRedirects.Load() != 1 {
					t.Fatal("shared redirect policy was replaced")
				}
			})
		}
	}
}

func TestComputerMutationPreservesResponseAndHeaders(t *testing.T) {
	t.Setenv("REVYL_AGENT", "revyl")
	t.Setenv("GITHUB_ACTIONS", "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/mutation" {
			t.Error("unexpected method or path")
		}
		for name, expected := range map[string]string{
			"Authorization":  "Bearer fixture-key",
			"User-Agent":     "revyl-cli/fixture-version",
			"X-Revyl-Client": "cli",
			"X-Revyl-Agent":  "revyl",
			"X-Request-ID":   "fixture-request",
			"Content-Type":   "application/json",
		} {
			if r.Header.Get(name) != expected {
				t.Errorf("missing established %s header", name)
			}
		}
		if r.Header.Get("Idempotency-Key") != "" || r.Header.Get("X-Idempotency-Key") != "" {
			t.Error("mutation must not be marked replayable by net/http")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != `{"fixture":"value"}` {
			t.Error("request body changed")
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "null")
	}))
	defer server.Close()
	client := NewClientWithBaseURL("fixture-key", server.URL)
	client.SetVersion("fixture-version")
	response, err := client.doComputerMutationOnce(context.Background(), "/mutation", []byte(`{"fixture":"value"}`), "fixture-request", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusAccepted || string(body) != "null" {
		t.Fatalf("response not available after headers: status=%d error=%v", response.StatusCode, err)
	}
}

func TestComputerMutationDoesNotRetryHTTPFailures(t *testing.T) {
	for _, status := range []int{401, 403, 409, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(status)
			}))
			defer server.Close()
			client := NewClientWithBaseURL("fixture-key", server.URL)
			response, err := client.doComputerMutationOnce(context.Background(), "/mutation", []byte(`{}`), "fixture-request", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != status || calls.Load() != 1 {
				t.Fatalf("status=%d calls=%d", response.StatusCode, calls.Load())
			}
		})
	}
}

func TestComputerMutationLeavesNativeRedirectBehaviorUnchanged(t *testing.T) {
	var destinationRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/destination" {
			destinationRequests.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Location", "/destination")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client := NewClientWithBaseURL("fixture-key", server.URL)
	if response, err := client.doComputerMutationOnce(context.Background(), "/mutation", []byte(`{}`), "fixture-request", time.Second); response != nil || err == nil {
		t.Fatal("computer redirect was accepted")
	}
	response, err := client.doRequestOnce(context.Background(), http.MethodPost, "/native", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent || destinationRequests.Load() != 1 {
		t.Fatal("computer transport changed native redirect handling")
	}
}

func TestComputerMutationHidesRawTransportErrors(t *testing.T) {
	var calls atomic.Int32
	client := NewClientWithBaseURL("fixture-key", "https://fixture.invalid")
	client.SetHTTPClient(&http.Client{Transport: computerRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("private-response-canary private-location-canary fixture-key")
	})})
	response, err := client.doComputerMutationOnce(context.Background(), "/mutation", []byte(`{}`), "fixture-request", time.Second)
	if response != nil || err == nil || calls.Load() != 1 {
		t.Fatal("failed transport was accepted or retried")
	}
	assertComputerTransportErrorIsSafe(t, err)
}

func TestComputerMutationDoesNotReplayAfterDisconnect(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	}))
	defer server.Close()
	response, err := NewClientWithBaseURL("fixture-key", server.URL).doComputerMutationOnce(context.Background(), "/private-location-canary", []byte(`{}`), "fixture-request", time.Second)
	if response != nil || err == nil || calls.Load() != 1 {
		t.Fatalf("disconnect result: response=%v error=%v calls=%d", response, err, calls.Load())
	}
	assertComputerTransportErrorIsSafe(t, err)
}

func TestComputerMutationHonorsCancellationAndTimeout(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprint(canceled), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				<-r.Context().Done()
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wantError, wantCalls := context.DeadlineExceeded, int32(1)
			if canceled {
				cancel()
				wantError, wantCalls = context.Canceled, 0
			}
			response, err := NewClientWithBaseURL("fixture-key", server.URL).doComputerMutationOnce(ctx, "/private-location-canary", []byte(`{}`), "fixture-request", 100*time.Millisecond)
			if response != nil || !errors.Is(err, wantError) || calls.Load() != wantCalls {
				t.Fatalf("cancellation result: response=%v error=%v calls=%d", response, err, calls.Load())
			}
			assertComputerTransportErrorIsSafe(t, err)
		})
	}
}

func TestComputerMutationKeepsBodyDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	response, err := NewClientWithBaseURL("fixture-key", server.URL).doComputerMutationOnce(context.Background(), "/mutation", []byte(`{}`), "fixture-request", 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := io.ReadAll(response.Body); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("response body deadline error = %v", err)
	}
}

func TestComputerMutationConcurrentCallsLeaveSharedClientUnchanged(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := NewClientWithBaseURL("fixture-key", server.URL)
	original := client.httpClient
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			response, err := client.doComputerMutationOnce(context.Background(), "/mutation", []byte(`{}`), "fixture-request", time.Second)
			if err != nil {
				t.Error(err)
				return
			}
			response.Body.Close()
		})
	}
	workers.Wait()
	if calls.Load() != 12 || client.httpClient != original || original.Timeout != DefaultTimeout || original.CheckRedirect != nil {
		t.Fatal("concurrent computer calls mutated the shared client or replayed a request")
	}
}

func TestComputerMutationRejectsUnboundedTimeoutBeforeDispatch(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	for _, timeout := range []time.Duration{0, -time.Second} {
		response, err := NewClientWithBaseURL("fixture-key", server.URL).doComputerMutationOnce(context.Background(), "/mutation", nil, "fixture-request", timeout)
		if response != nil || err == nil || calls.Load() != 0 {
			t.Fatal("unbounded request was dispatched")
		}
	}
}

func assertComputerTransportErrorIsSafe(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a safe error")
	}
	for _, private := range []string{"private-location-canary", "private-response-canary", "fixture-key", "127.0.0.1"} {
		if strings.Contains(err.Error(), private) {
			t.Fatal("transport error exposed private details")
		}
	}
}

type computerRoundTripper func(*http.Request) (*http.Response, error)

func (f computerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
