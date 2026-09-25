package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type finalizationTransport func(*http.Request) (*http.Response, error)

func (f finalizationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestBuildFinalizationTimeoutAndRetries(t *testing.T) {
	for _, firstStatus := range []int{http.StatusOK, http.StatusServiceUnavailable, http.StatusBadRequest} {
		t.Run(http.StatusText(firstStatus), func(t *testing.T) {
			client := NewClientWithBaseURL("test-key", "https://example.com")
			client.maxRetries = 1
			client.retryBaseDelay = time.Millisecond
			attempts := 0
			client.httpClient.Transport = finalizationTransport(func(req *http.Request) (*http.Response, error) {
				attempts++
				deadline, ok := req.Context().Deadline()
				if remaining := time.Until(deadline); !ok || remaining < 89*time.Second || remaining > 90*time.Second {
					t.Fatalf("finalization deadline remaining = %v, want approximately 90s", remaining)
				}
				if req.Method != http.MethodPost || req.URL.Path != "/api/v1/apps/app-1/builds" {
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
				}
				if req.Header.Get("Authorization") != "Bearer test-key" {
					t.Fatal("finalization did not preserve authentication")
				}
				var body map[string]interface{}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["upload_id"] != "upload-1" || body["version"] != "1.0.0" {
					t.Fatalf("unexpected finalization identity: %v", body)
				}
				status := http.StatusOK
				if attempts == 1 {
					status = firstStatus
				}
				return &http.Response{
					StatusCode: status,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"id":"build-1","version":"1.0.0"}`)),
				}, nil
			})
			result, err := client.createBuildFromStagedUpload(context.Background(), &UploadBuildRequest{
				AppID: "app-1", Version: "1.0.0",
			}, "upload-1")
			if firstStatus == http.StatusBadRequest {
				if err == nil {
					t.Fatal("expected terminal validation error")
				}
			} else if err != nil || result.VersionID != "build-1" {
				t.Fatalf("finalization = %v, %v", result, err)
			}
			wantAttempts := 1
			if firstStatus == http.StatusServiceUnavailable {
				wantAttempts = 2
			}
			if attempts != wantAttempts {
				t.Fatalf("attempts = %d, want %d", attempts, wantAttempts)
			}
			if client.httpClient.Timeout != 30*time.Second {
				t.Fatalf("ordinary request timeout changed to %v", client.httpClient.Timeout)
			}
		})
	}
}

func TestBuildFinalizationHonorsCallerDeadline(t *testing.T) {
	client := NewClientWithBaseURL("test-key", "https://example.com")
	client.httpClient.Transport = finalizationTransport(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := client.createBuildFromStagedUpload(ctx, &UploadBuildRequest{
		AppID: "app-1", Version: "1.0.0",
	}, "upload-1")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("finalization error = %v, want caller deadline exceeded", err)
	}
}
