package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func writeTestArtifact(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact.zip")
	if err := os.WriteFile(path, []byte("fake-build-bytes"), 0o644); err != nil {
		t.Fatalf("failed to write test artifact: %v", err)
	}
	return path
}

func testUploadBuildClient(
	t *testing.T,
	uploadHandler http.HandlerFunc,
	completeHandler http.HandlerFunc,
) (*Client, string, *int32, *int32) {
	t.Helper()

	var uploadAttempts int32
	var completeCalls int32

	uploadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&uploadAttempts, 1)
		uploadHandler(w, r)
	}))
	t.Cleanup(uploadServer.Close)

	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/builds/vars/app-1/versions/upload-url":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method for presign endpoint: %s", r.Method)
			}
			if got := r.URL.Query().Get("version"); got == "" {
				t.Fatalf("missing version query param")
			}
			if got := r.URL.Query().Get("file_name"); got != "artifact.zip" {
				t.Fatalf("unexpected file_name query param: got %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(
				w,
				`{"version_id":"ver-1","version":"v1","upload_url":"%s/upload","content_type":"application/octet-stream"}`,
				uploadServer.URL,
			)
		case "/api/v1/builds/versions/ver-1/complete-upload":
			atomic.AddInt32(&completeCalls, 1)
			completeHandler(w, r)
		default:
			t.Fatalf("unexpected backend path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(backendServer.Close)

	client := NewClientWithBaseURL("test-key", backendServer.URL)
	client.uploadClient = uploadServer.Client()
	client.retryBaseDelay = time.Millisecond
	client.retryMaxDelay = 2 * time.Millisecond

	return client, writeTestArtifact(t), &uploadAttempts, &completeCalls
}

func TestUploadBuild_RetriesRetryableStatusThenSucceeds(t *testing.T) {
	var seen int32
	client, artifactPath, uploadAttempts, completeCalls := testUploadBuildClient(
		t,
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Fatalf("unexpected upload method: %s", r.Method)
			}
			if atomic.AddInt32(&seen, 1) == 1 {
				http.Error(w, "temporary outage", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		},
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected complete method: %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"v1"}`))
		},
	)

	resp, err := client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: artifactPath,
	})
	if err != nil {
		t.Fatalf("UploadBuild() error = %v, want nil", err)
	}
	if resp.VersionID != "ver-1" {
		t.Fatalf("UploadBuild() version_id = %q, want %q", resp.VersionID, "ver-1")
	}
	if got := atomic.LoadInt32(uploadAttempts); got != 2 {
		t.Fatalf("upload attempts = %d, want 2", got)
	}
	if got := atomic.LoadInt32(completeCalls); got != 1 {
		t.Fatalf("complete-upload calls = %d, want 1", got)
	}
}

type failFirstTransport struct {
	base  http.RoundTripper
	err   error
	calls int32
}

func (t *failFirstTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if atomic.AddInt32(&t.calls, 1) == 1 {
		return nil, t.err
	}
	if t.base == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	return t.base.RoundTrip(req)
}

func TestUploadBuild_RetriesTransportErrorThenSucceeds(t *testing.T) {
	client, artifactPath, uploadAttempts, completeCalls := testUploadBuildClient(
		t,
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"v1"}`))
		},
	)

	baseTransport := client.uploadClient.Transport
	failTransport := &failFirstTransport{
		base: baseTransport,
		err:  errors.New("write: broken pipe"),
	}
	client.uploadClient = &http.Client{
		Transport: failTransport,
		Timeout:   UploadTimeout,
	}

	_, err := client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: artifactPath,
	})
	if err != nil {
		t.Fatalf("UploadBuild() error = %v, want nil", err)
	}
	if got := atomic.LoadInt32(&failTransport.calls); got != 2 {
		t.Fatalf("transport calls = %d, want 2", got)
	}
	// First transport failure happens before hitting the upload server.
	if got := atomic.LoadInt32(uploadAttempts); got != 1 {
		t.Fatalf("upload attempts = %d, want 1", got)
	}
	if got := atomic.LoadInt32(completeCalls); got != 1 {
		t.Fatalf("complete-upload calls = %d, want 1", got)
	}
}

func TestUploadBuild_DoesNotRetryNonRetryableStatus(t *testing.T) {
	client, artifactPath, uploadAttempts, completeCalls := testUploadBuildClient(
		t,
		func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		},
		func(w http.ResponseWriter, r *http.Request) {
			t.Fatalf("complete-upload should not be called when upload fails")
		},
	)

	_, err := client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: artifactPath,
	})
	if err == nil {
		t.Fatal("UploadBuild() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "status 403") {
		t.Fatalf("error = %q, want status 403", err.Error())
	}
	if got := atomic.LoadInt32(uploadAttempts); got != 1 {
		t.Fatalf("upload attempts = %d, want 1", got)
	}
	if got := atomic.LoadInt32(completeCalls); got != 0 {
		t.Fatalf("complete-upload calls = %d, want 0", got)
	}
}

func TestUploadBuild_FailsAfterRetryExhaustion(t *testing.T) {
	client, artifactPath, uploadAttempts, completeCalls := testUploadBuildClient(
		t,
		func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "temporary outage", http.StatusServiceUnavailable)
		},
		func(w http.ResponseWriter, r *http.Request) {
			t.Fatalf("complete-upload should not be called when upload fails")
		},
	)
	client.maxRetries = 2 // 3 total attempts

	_, err := client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: artifactPath,
	})
	if err == nil {
		t.Fatal("UploadBuild() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "upload failed after 3 attempts") {
		t.Fatalf("error = %q, want retry exhaustion message", err.Error())
	}
	if !strings.Contains(err.Error(), "status 503") {
		t.Fatalf("error = %q, want status 503", err.Error())
	}
	if got := atomic.LoadInt32(uploadAttempts); got != 3 {
		t.Fatalf("upload attempts = %d, want 3", got)
	}
	if got := atomic.LoadInt32(completeCalls); got != 0 {
		t.Fatalf("complete-upload calls = %d, want 0", got)
	}
}

func TestUploadBuild_CancelledDuringRetryBackoff(t *testing.T) {
	firstAttempt := make(chan struct{}, 1)
	client, artifactPath, uploadAttempts, completeCalls := testUploadBuildClient(
		t,
		func(w http.ResponseWriter, r *http.Request) {
			select {
			case firstAttempt <- struct{}{}:
			default:
			}
			http.Error(w, "temporary outage", http.StatusServiceUnavailable)
		},
		func(w http.ResponseWriter, r *http.Request) {
			t.Fatalf("complete-upload should not be called when upload fails")
		},
	)
	client.maxRetries = 3
	client.retryBaseDelay = 250 * time.Millisecond
	client.retryMaxDelay = 250 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-firstAttempt
		cancel()
	}()

	_, err := client.UploadBuild(ctx, &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: artifactPath,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("UploadBuild() error = %v, want context canceled", err)
	}
	if got := atomic.LoadInt32(uploadAttempts); got != 1 {
		t.Fatalf("upload attempts = %d, want 1", got)
	}
	if got := atomic.LoadInt32(completeCalls); got != 0 {
		t.Fatalf("complete-upload calls = %d, want 0", got)
	}
}

func TestUploadBuild_UsesPresignVersionIDWhenCompleteOmitted(t *testing.T) {
	client, artifactPath, _, completeCalls := testUploadBuildClient(
		t,
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Fatalf("unexpected upload method: %s", r.Method)
			}
			if _, err := io.ReadAll(r.Body); err != nil {
				t.Fatalf("failed to read upload body: %v", err)
			}
			w.WriteHeader(http.StatusOK)
		},
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"v1","package_id":"com.example.app"}`))
		},
	)

	resp, err := client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: artifactPath,
	})
	if err != nil {
		t.Fatalf("UploadBuild() error = %v, want nil", err)
	}
	if resp.VersionID != "ver-1" {
		t.Fatalf("UploadBuild() version_id = %q, want %q", resp.VersionID, "ver-1")
	}
	if resp.PackageID != "com.example.app" {
		t.Fatalf("UploadBuild() package_id = %q, want %q", resp.PackageID, "com.example.app")
	}
	if got := atomic.LoadInt32(completeCalls); got != 1 {
		t.Fatalf("complete-upload calls = %d, want 1", got)
	}
}

func TestDoRequestWithRetry_NegativeMaxRetriesStillAttemptsOnce(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("temporary outage"))
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	client.maxRetries = -1

	resp, err := client.doRequestWithRetry(context.Background(), http.MethodGet, "/", nil)
	if err != nil {
		t.Fatalf("doRequestWithRetry() error = %v, want nil", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		t.Fatalf("failed to read response body: %v", readErr)
	}
	if got := strings.TrimSpace(string(body)); got != "temporary outage" {
		t.Fatalf("response body = %q, want %q", got, "temporary outage")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

func TestDoRequestWithRetry_ReturnsFinalRetryableResponseWithReadableBody(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprintf(w, "attempt-%d", attempt)
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	client.maxRetries = 2 // 3 total attempts
	client.retryBaseDelay = time.Millisecond
	client.retryMaxDelay = time.Millisecond

	resp, err := client.doRequestWithRetry(context.Background(), http.MethodGet, "/", nil)
	if err != nil {
		t.Fatalf("doRequestWithRetry() error = %v, want nil", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		t.Fatalf("failed to read response body: %v", readErr)
	}
	if got := strings.TrimSpace(string(body)); got != "attempt-3" {
		t.Fatalf("response body = %q, want %q", got, "attempt-3")
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestListApps_HydratesMissingVersionSummaries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/builds/vars":
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method for list apps: %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"items": [
					{"id":"app-1","name":"Yahoo Mail","platform":"android","versions_count":0},
					{"id":"app-2","name":"ios-test","platform":"ios","versions_count":2,"latest_version":"2.0.0"}
				],
				"total": 2,
				"page": 1,
				"page_size": 100,
				"total_pages": 1,
				"has_next": false,
				"has_previous": false
			}`))
		case "/api/v1/builds/vars/app-1/versions":
			if got := r.URL.Query().Get("page"); got != "1" {
				t.Fatalf("unexpected page query for app-1 versions: %q", got)
			}
			if got := r.URL.Query().Get("page_size"); got != "1" {
				t.Fatalf("unexpected page_size query for app-1 versions: %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"items": [{"id":"ver-1","version":"2026.03.05","uploaded_at":"2026-03-05T00:00:00Z"}],
				"total": 1,
				"page": 1,
				"page_size": 1,
				"total_pages": 1,
				"has_next": false,
				"has_previous": false
			}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)

	resp, err := client.ListApps(context.Background(), "", 1, 100)
	if err != nil {
		t.Fatalf("ListApps() error = %v, want nil", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("ListApps() returned %d items, want 2", len(resp.Items))
	}
	if got := resp.Items[0].VersionsCount; got != 1 {
		t.Fatalf("app-1 versions_count = %d, want 1 after hydration", got)
	}
	if got := resp.Items[0].LatestVersion; got != "2026.03.05" {
		t.Fatalf("app-1 latest_version = %q, want %q", got, "2026.03.05")
	}
	if got := resp.Items[1].VersionsCount; got != 2 {
		t.Fatalf("app-2 versions_count = %d, want 2", got)
	}
	if got := resp.Items[1].LatestVersion; got != "2.0.0" {
		t.Fatalf("app-2 latest_version = %q, want %q", got, "2.0.0")
	}
}

func TestGetApp_HydratesMissingVersionSummaries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/builds/vars/app-1":
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method for get app: %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id":"app-1",
				"name":"ios-test",
				"platform":"ios",
				"versions_count":0
			}`))
		case "/api/v1/builds/vars/app-1/versions":
			if got := r.URL.Query().Get("page"); got != "1" {
				t.Fatalf("unexpected page query for app-1 versions: %q", got)
			}
			if got := r.URL.Query().Get("page_size"); got != "1" {
				t.Fatalf("unexpected page_size query for app-1 versions: %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"items": [{"id":"ver-1","version":"2026.03.05","uploaded_at":"2026-03-05T00:00:00Z"}],
				"total": 1,
				"page": 1,
				"page_size": 1,
				"total_pages": 1,
				"has_next": false,
				"has_previous": false
			}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)

	app, err := client.GetApp(context.Background(), "app-1")
	if err != nil {
		t.Fatalf("GetApp() error = %v, want nil", err)
	}
	if got := app.VersionsCount; got != 1 {
		t.Fatalf("versions_count = %d, want 1 after hydration", got)
	}
	if got := app.LatestVersion; got != "2026.03.05" {
		t.Fatalf("latest_version = %q, want %q", got, "2026.03.05")
	}
}

func TestProxyWorkerRequest_InferMethodFromAction(t *testing.T) {
	tests := []struct {
		name           string
		action         string
		body           interface{}
		wantMethod     string
		wantBody       string
		wantStatusCode int
	}{
		{
			name:           "read only action uses get and drops body",
			action:         "health",
			body:           map[string]string{"ignored": "value"},
			wantMethod:     http.MethodGet,
			wantBody:       "",
			wantStatusCode: http.StatusOK,
		},
		{
			name:           "mutating action uses post and forwards body",
			action:         "tap",
			body:           map[string]int{"x": 12, "y": 34},
			wantMethod:     http.MethodPost,
			wantBody:       `{"x":12,"y":34}`,
			wantStatusCode: http.StatusCreated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Path; got != "/api/v1/execution/device-proxy/wf-1/"+tt.action {
					t.Fatalf("unexpected path: %s", got)
				}
				if got := r.Method; got != tt.wantMethod {
					t.Fatalf("method = %s, want %s", got, tt.wantMethod)
				}

				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("failed to read request body: %v", err)
				}
				if got := strings.TrimSpace(string(body)); got != tt.wantBody {
					t.Fatalf("body = %q, want %q", got, tt.wantBody)
				}

				w.WriteHeader(tt.wantStatusCode)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			t.Cleanup(server.Close)

			client := NewClientWithBaseURL("test-key", server.URL)

			_, statusCode, err := client.ProxyWorkerRequest(context.Background(), "wf-1", tt.action, tt.body)
			if err != nil {
				t.Fatalf("ProxyWorkerRequest() error = %v, want nil", err)
			}
			if statusCode != tt.wantStatusCode {
				t.Fatalf("statusCode = %d, want %d", statusCode, tt.wantStatusCode)
			}
		})
	}
}
