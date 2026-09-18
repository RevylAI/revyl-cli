package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestListComputers(t *testing.T) {
	for _, testCase := range []struct {
		name string
		body string
		want int
	}{
		{"assigned", `{"computers":[{"instance_id":"computer-a","status":"online"},{"instance_id":"computer-b","status":"offline"}]}`, 2},
		{"empty", `{"computers":[]}`, 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/execution/computers" || r.URL.RawQuery != "" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
				}
				if r.Header.Get("Authorization") != "Bearer test-key" {
					t.Error("missing Revyl authorization")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || len(body) != 0 {
					t.Error("computer listing must not send caller-supplied targeting")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer server.Close()
			result, err := NewClientWithBaseURL("test-key", server.URL).ListComputers(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if result.Computers == nil || len(result.Computers) != testCase.want {
				t.Fatalf("computers = %#v, want a list of length %d", result.Computers, testCase.want)
			}
			if testCase.want > 0 && (result.Computers[0].InstanceId != "computer-a" || result.Computers[0].Status != "online" || result.Computers[1].InstanceId != "computer-b" || result.Computers[1].Status != "offline") {
				t.Fatalf("unexpected computer IDs, statuses or order: %#v", result.Computers)
			}
		})
	}
}

func TestListComputersPropagatesAPIErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/execution/computers" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"detail":"Computer inventory unavailable"}`)
			}))
			defer server.Close()
			client := NewClientWithBaseURL("test-key", server.URL)
			client.retryBaseDelay = 0
			result, err := client.ListComputers(context.Background())
			var apiErr *APIError
			if result != nil || !errors.As(err, &apiErr) || apiErr.StatusCode != status {
				t.Fatalf("list result = %#v, error = %v, want HTTP %d", result, err, status)
			}
			wantCalls := int32(1)
			if status == http.StatusServiceUnavailable {
				wantCalls = int32(DefaultMaxRetries + 1)
			}
			if calls.Load() != wantCalls {
				t.Fatalf("requests = %d, want %d", calls.Load(), wantCalls)
			}
		})
	}
}

func TestListComputersHonorsCancellationAndDeadline(t *testing.T) {
	for _, alreadyCanceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "canceled"}[alreadyCanceled], func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				<-r.Context().Done()
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			wantErr := context.DeadlineExceeded
			wantCalls := int32(1)
			if alreadyCanceled {
				cancel()
				wantErr = context.Canceled
				wantCalls = 0
			}
			result, err := NewClientWithBaseURL("test-key", server.URL).ListComputers(ctx)
			if result != nil || !errors.Is(err, wantErr) || calls.Load() != wantCalls {
				t.Fatalf("list result = %#v, error = %v, calls = %d; want %v and %d requests", result, err, calls.Load(), wantErr, wantCalls)
			}
		})
	}
}

func TestListComputersRejectsMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"computers":`)
	}))
	defer server.Close()
	result, err := NewClientWithBaseURL("test-key", server.URL).ListComputers(context.Background())
	if result != nil || err == nil || !strings.Contains(err.Error(), "failed to parse response") {
		t.Fatalf("malformed response = %#v, error = %v", result, err)
	}
}

func TestOpenComputerShellSession(t *testing.T) {
	const instanceID = "mi-0123456789abcdef0"
	for _, status := range []int{http.StatusOK, http.StatusForbidden, http.StatusNotFound, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/execution/computers/"+instanceID+"/shell-sessions" || r.URL.RawQuery != "" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
				}
				if r.Header.Get("Authorization") != "Bearer test-key" {
					t.Error("missing Revyl authorization")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || len(body) != 0 {
					t.Error("targeted shell must not send a request body")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				if status == http.StatusOK {
					_ = json.NewEncoder(w).Encode(MacShellSession{SessionId: "test-session", InstanceId: instanceID})
				} else {
					_, _ = io.WriteString(w, `{"detail":"Computer unavailable"}`)
				}
			}))
			defer server.Close()
			session, err := NewClientWithBaseURL("test-key", server.URL).OpenComputerShellSession(context.Background(), instanceID)
			if status == http.StatusOK {
				if err != nil || session == nil || session.SessionId != "test-session" || session.InstanceId != instanceID {
					t.Fatalf("targeted shell = %#v, err = %v", session, err)
				}
			} else {
				var apiErr *APIError
				if session != nil || !errors.As(err, &apiErr) || apiErr.StatusCode != status {
					t.Fatalf("targeted shell = %#v, err = %v, want HTTP %d", session, err, status)
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("shell creation requests = %d, want exactly 1 without retries or fallback", calls.Load())
			}
		})
	}
}

func TestOpenComputerShellSessionEscapesInstanceID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/v1/execution/computers/computer%2Fother%3Fvalue/shell-sessions" || r.URL.RawQuery != "" {
			t.Errorf("unexpected targeted URL: %s", r.URL.String())
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	_, err := NewClientWithBaseURL("test-key", server.URL).OpenComputerShellSession(context.Background(), "computer/other?value")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("targeted shell error = %v, want backend rejection", err)
	}
}

func TestOpenComputerShellSessionHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a canceled shell request reached the backend")
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewClientWithBaseURL("test-key", server.URL).OpenComputerShellSession(ctx, "mi-0123456789abcdef0")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("targeted shell error = %v, want cancellation", err)
	}
}
