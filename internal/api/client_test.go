package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func writeTestArtifact(t *testing.T) string {
	t.Helper()
	// .apk files bypass the local-zip structural pre-flight in
	// validateLocalBuildArtifact. The byte contents only matter on the server
	// side (which is mocked here), so a tiny placeholder is sufficient.
	path := filepath.Join(t.TempDir(), "artifact.apk")
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
			if got := r.URL.Query().Get("file_name"); got != "artifact.apk" {
				t.Fatalf("unexpected file_name query param: got %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(
				w,
				`{"version_id":"ver-1","version":"v1","upload_url":"%s/upload","content_type":"application/vnd.android.package-archive"}`,
				uploadServer.URL,
			)
		case "/api/v1/builds/versions/ver-1/extract-package-id":
			// Default happy-path stub; specific tests can override the
			// complete-upload behavior, but extraction always succeeds here
			// because the failing-upload tests never reach this hop.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"package_id":"com.example.app"}`))
		case "/api/v1/builds/versions/ver-1/complete-upload":
			atomic.AddInt32(&completeCalls, 1)
			completeHandler(w, r)
		case "/api/v1/builds/versions/ver-1":
			// bestEffortDeleteBuildVersion fires on every UploadBuild failure
			// path. It uses a detached context so it runs even when the
			// caller cancels mid-upload — accept the call silently here.
			if r.Method != http.MethodDelete {
				t.Fatalf("unexpected method for version endpoint: %s", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message":"deleted"}`))
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

// ---------------------------------------------------------------------------
// CreateBuildFromURL tests
// ---------------------------------------------------------------------------

func TestCreateBuildFromURL_Success(t *testing.T) {
	var capturedBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/builds/app-1/builds/from-url" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"ver-url-1",
			"app_id":"app-1",
			"version":"1.0.0",
			"artifact_url":"org/builds/app-1/1.0.0/app.ipa",
			"package_name":"com.example.app"
		}`))
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	resp, err := client.CreateBuildFromURL(context.Background(), &CreateBuildFromURLRequest{
		AppID:   "app-1",
		FromURL: "https://artifacts.internal.company.com/builds/app-latest.ipa",
		Headers: map[string]string{"Authorization": "Bearer secret"},
		Version: "1.0.0",
		Metadata: map[string]interface{}{
			"source": "cli_url_upload",
		},
		SetAsCurrent: true,
	})
	if err != nil {
		t.Fatalf("CreateBuildFromURL() error = %v", err)
	}
	if resp.ID != "ver-url-1" {
		t.Fatalf("ID = %q, want %q", resp.ID, "ver-url-1")
	}
	if resp.Version != "1.0.0" {
		t.Fatalf("Version = %q, want %q", resp.Version, "1.0.0")
	}
	if resp.PackageName != "com.example.app" {
		t.Fatalf("PackageName = %q, want %q", resp.PackageName, "com.example.app")
	}

	// Verify the request body was correctly serialized.
	if fromURL, ok := capturedBody["from_url"].(string); !ok || fromURL != "https://artifacts.internal.company.com/builds/app-latest.ipa" {
		t.Fatalf("from_url = %v, want the artifact URL", capturedBody["from_url"])
	}
	if headers, ok := capturedBody["headers"].(map[string]interface{}); !ok || headers["Authorization"] != "Bearer secret" {
		t.Fatalf("headers = %v, want Authorization header", capturedBody["headers"])
	}
}

func TestCreateBuildFromURL_IdempotentReuse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"ver-existing",
			"app_id":"app-1",
			"version":"1.0.0",
			"artifact_url":"org/builds/app-1/1.0.0/app.ipa",
			"was_reused":true
		}`))
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	resp, err := client.CreateBuildFromURL(context.Background(), &CreateBuildFromURLRequest{
		AppID:   "app-1",
		FromURL: "https://example.com/app.ipa",
		Version: "1.0.0",
	})
	if err != nil {
		t.Fatalf("CreateBuildFromURL() error = %v", err)
	}
	if !resp.WasReused {
		t.Fatalf("WasReused = false, want true")
	}
}

func TestCreateBuildFromURL_BackendFetchFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"detail":"Failed to fetch source URL"}`, http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	_, err := client.CreateBuildFromURL(context.Background(), &CreateBuildFromURLRequest{
		AppID:   "app-1",
		FromURL: "https://bad-url.example.com/missing.ipa",
		Version: "1.0.0",
	})
	if err == nil {
		t.Fatal("CreateBuildFromURL() expected error for 502, got nil")
	}
	if !strings.Contains(err.Error(), "502") && !strings.Contains(err.Error(), "Failed to fetch") {
		t.Fatalf("error should mention 502 or fetch failure, got: %v", err)
	}
}

func TestCreateBuildFromURL_NoHeaders(t *testing.T) {
	var capturedBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"ver-2",
			"app_id":"app-1",
			"version":"2.0.0",
			"artifact_url":"s3-key"
		}`))
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	_, err := client.CreateBuildFromURL(context.Background(), &CreateBuildFromURLRequest{
		AppID:   "app-1",
		FromURL: "https://public.example.com/app.apk",
		Version: "2.0.0",
	})
	if err != nil {
		t.Fatalf("CreateBuildFromURL() error = %v", err)
	}
	// Headers should be omitted from the JSON body when nil.
	if _, exists := capturedBody["headers"]; exists {
		t.Fatalf("headers should be omitted when nil, got: %v", capturedBody["headers"])
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

func TestListAllApps_PaginatesAcrossAllPages(t *testing.T) {
	var requestedPages []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/builds/vars" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method for list apps: %s", r.Method)
		}

		page := r.URL.Query().Get("page")
		requestedPages = append(requestedPages, page)

		w.Header().Set("Content-Type", "application/json")
		switch page {
		case "1":
			_, _ = w.Write([]byte(`{
				"items": [{"id":"app-1","name":"Alpha","platform":"ios","versions_count":1,"latest_version":"1.0.0"}],
				"total": 2,
				"page": 1,
				"page_size": 100,
				"total_pages": 2,
				"has_next": true,
				"has_previous": false
			}`))
		case "2":
			_, _ = w.Write([]byte(`{
				"items": [{"id":"app-2","name":"Zulu","platform":"ios","versions_count":1,"latest_version":"2.0.0"}],
				"total": 2,
				"page": 2,
				"page_size": 100,
				"total_pages": 2,
				"has_next": false,
				"has_previous": true
			}`))
		default:
			t.Fatalf("unexpected page: %s", page)
		}
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)

	apps, err := client.ListAllApps(context.Background(), "ios", 100)
	if err != nil {
		t.Fatalf("ListAllApps() error = %v, want nil", err)
	}

	if got, want := requestedPages, []string{"1", "2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("requested pages = %v, want %v", got, want)
	}
	if len(apps) != 2 {
		t.Fatalf("ListAllApps() returned %d items, want 2", len(apps))
	}
	if apps[0].ID != "app-1" || apps[1].ID != "app-2" {
		t.Fatalf("unexpected apps = %#v", apps)
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

func TestStartDeviceExportsCLITraceHandoff(t *testing.T) {
	var telemetryRequestID string
	var startRequestID string
	var exportedTraceID string
	var startTraceparent string
	var startToken string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/telemetry/cli-traces":
			if r.Method != http.MethodPost {
				t.Fatalf("telemetry method = %s, want POST", r.Method)
			}
			if got := r.Header.Get("Content-Type"); got != otlpProtobufContentType {
				t.Fatalf("telemetry content-type = %q, want %q", got, otlpProtobufContentType)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
				t.Fatalf("telemetry authorization = %q", got)
			}
			telemetryRequestID = r.Header.Get(traceRequestIDHeader)
			if telemetryRequestID == "" {
				t.Fatal("telemetry request missing X-Request-ID")
			}
			payload, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read telemetry body: %v", err)
			}
			var export tracepb.TracesData
			if err := proto.Unmarshal(payload, &export); err != nil {
				t.Fatalf("decode telemetry body: %v", err)
			}
			if len(export.ResourceSpans) != 1 ||
				len(export.ResourceSpans[0].ScopeSpans) != 1 ||
				len(export.ResourceSpans[0].ScopeSpans[0].Spans) != 1 {
				t.Fatalf("unexpected telemetry shape: %+v", export)
			}
			span := export.ResourceSpans[0].ScopeSpans[0].Spans[0]
			if span.Name != cliTraceSpanName {
				t.Fatalf("span name = %q, want %q", span.Name, cliTraceSpanName)
			}
			exportedTraceID = fmt.Sprintf("%x", span.TraceId)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(
				w,
				`{"handoff_token":"handoff-token","trace_id":%q,"request_id":%q}`,
				exportedTraceID,
				telemetryRequestID,
			)
		case "/api/v1/execution/start_device":
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode start_device request: %v", err)
			}
			if _, ok := body["trace_context"]; ok {
				t.Fatalf("start_device body unexpectedly included trace_context: %#v", body)
			}
			startRequestID = r.Header.Get(traceRequestIDHeader)
			startTraceparent = r.Header.Get(cliTraceparentHeader)
			startToken = r.Header.Get(cliTraceHandoffHeader)
			if startRequestID == "" || startRequestID != telemetryRequestID {
				t.Fatalf("start request ID = %q, telemetry request ID = %q", startRequestID, telemetryRequestID)
			}
			if startToken != "handoff-token" {
				t.Fatalf("handoff token = %q, want handoff-token", startToken)
			}
			if startTraceparent == "" || !strings.Contains(startTraceparent, exportedTraceID) {
				t.Fatalf("start traceparent = %q, exported trace ID = %q", startTraceparent, exportedTraceID)
			}
			if got := r.Header.Get("traceparent"); got != "" {
				t.Fatalf("standard traceparent header = %q, want empty", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"workflow_run_id":"11111111-1111-1111-1111-111111111111","trace_id":%q}`, exportedTraceID)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	req := &StartDeviceRequest{Platform: "ios"}
	resp, err := client.StartDevice(context.Background(), req)
	if err != nil {
		t.Fatalf("StartDevice() error = %v, want nil", err)
	}
	if resp.TraceId == nil {
		t.Fatal("StartDevice() response missing trace_id")
	}
	if *resp.TraceId != exportedTraceID {
		t.Fatalf("response trace_id = %q, want %q", *resp.TraceId, exportedTraceID)
	}
}

func TestStartDeviceFallsBackWhenCLITraceExportFails(t *testing.T) {
	var sawStart bool
	var startRequestID string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/telemetry/cli-traces":
			http.Error(w, "collector unavailable", http.StatusBadGateway)
		case "/api/v1/execution/start_device":
			sawStart = true
			startRequestID = r.Header.Get(traceRequestIDHeader)
			if got := r.Header.Get(cliTraceparentHeader); got != "" {
				t.Fatalf("fallback start sent CLI traceparent = %q", got)
			}
			if got := r.Header.Get(cliTraceHandoffHeader); got != "" {
				t.Fatalf("fallback start sent handoff token = %q", got)
			}
			if got := r.Header.Get("traceparent"); got != "" {
				t.Fatalf("fallback start sent standard traceparent = %q", got)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode start_device request: %v", err)
			}
			if _, ok := body["trace_context"]; ok {
				t.Fatalf("fallback body unexpectedly included trace_context: %#v", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"workflow_run_id":"11111111-1111-1111-1111-111111111111","trace_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	resp, err := client.StartDevice(context.Background(), &StartDeviceRequest{Platform: "ios"})
	if err != nil {
		t.Fatalf("StartDevice() error = %v, want nil", err)
	}
	if !sawStart {
		t.Fatal("StartDevice() did not call start_device after telemetry failure")
	}
	if startRequestID == "" {
		t.Fatal("fallback start missing X-Request-ID")
	}
	if resp.TraceId == nil || *resp.TraceId != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("TraceId = %v, want backend returned fallback trace ID", resp.TraceId)
	}
}

func TestUploadOrgFile_CallsCompleteUpload(t *testing.T) {
	var completeCalls int32

	uploadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected upload method: %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(uploadServer.Close)

	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/files/upload-url" && r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{
				"file":{"id":"file-1","org_id":"org-1","user_id":"u-1","filename":"test.txt","file_size":16,"status":"pending","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"},
				"upload_url":"%s/upload",
				"expires_in":3600,
				"content_type":"text/plain"
			}`, uploadServer.URL)
		case r.URL.Path == "/api/v1/files/file-1/complete-upload" && r.Method == http.MethodPost:
			atomic.AddInt32(&completeCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"file-1","org_id":"org-1","user_id":"u-1","filename":"test.txt","file_size":16,"status":"ready","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(backendServer.Close)

	client := NewClientWithBaseURL("test-key", backendServer.URL)
	client.uploadClient = uploadServer.Client()
	client.retryBaseDelay = time.Millisecond

	filePath := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(filePath, []byte("fake-file-bytes!"), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	result, err := client.UploadOrgFile(context.Background(), filePath, "test.txt", "")
	if err != nil {
		t.Fatalf("UploadOrgFile() error = %v, want nil", err)
	}
	if got := atomic.LoadInt32(&completeCalls); got != 1 {
		t.Fatalf("complete-upload calls = %d, want 1", got)
	}
	if result.Status != "ready" {
		t.Fatalf("returned file status = %q, want %q", result.Status, "ready")
	}
}

func TestReplaceOrgFileContent_CallsCompleteUpload(t *testing.T) {
	var completeCalls int32
	var capturedCompleteBody CLIOrgFileCompleteUploadRequest

	uploadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected upload method: %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(uploadServer.Close)

	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/files/file-1/replace-url" && r.Method == http.MethodPut:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{
				"file":{"id":"file-1","org_id":"org-1","user_id":"u-1","filename":"old.txt","file_size":16,"status":"ready","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"},
				"upload_url":"%s/upload",
				"expires_in":3600,
				"content_type":"text/plain",
				"s3_key":"org/org-1/file-1/new.txt"
			}`, uploadServer.URL)
		case r.URL.Path == "/api/v1/files/file-1/complete-upload" && r.Method == http.MethodPost:
			atomic.AddInt32(&completeCalls, 1)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("failed to read complete-upload body: %v", err)
			}
			if err := json.Unmarshal(body, &capturedCompleteBody); err != nil {
				t.Fatalf("failed to decode complete-upload body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"file-1","org_id":"org-1","user_id":"u-1","filename":"new.txt","file_size":16,"status":"ready","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(backendServer.Close)

	client := NewClientWithBaseURL("test-key", backendServer.URL)
	client.uploadClient = uploadServer.Client()
	client.retryBaseDelay = time.Millisecond

	filePath := filepath.Join(t.TempDir(), "new.txt")
	if err := os.WriteFile(filePath, []byte("fake-file-bytes!"), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	result, err := client.ReplaceOrgFileContent(context.Background(), "file-1", filePath, "new.txt", "")
	if err != nil {
		t.Fatalf("ReplaceOrgFileContent() error = %v, want nil", err)
	}
	if got := atomic.LoadInt32(&completeCalls); got != 1 {
		t.Fatalf("complete-upload calls = %d, want 1", got)
	}
	if capturedCompleteBody.S3Key != "org/org-1/file-1/new.txt" {
		t.Fatalf("complete-upload s3_key = %q, want %q", capturedCompleteBody.S3Key, "org/org-1/file-1/new.txt")
	}
	if capturedCompleteBody.Filename != "new.txt" {
		t.Fatalf("complete-upload filename = %q, want %q", capturedCompleteBody.Filename, "new.txt")
	}
	if capturedCompleteBody.FileSize != 16 {
		t.Fatalf("complete-upload file_size = %d, want 16", capturedCompleteBody.FileSize)
	}
	if result.Filename != "new.txt" {
		t.Fatalf("returned file filename = %q, want %q", result.Filename, "new.txt")
	}
}

func TestUploadOrgFile_ConfirmFailure_ReturnsError(t *testing.T) {
	var completeCalls int32

	uploadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(uploadServer.Close)

	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/files/upload-url" && r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{
				"file":{"id":"file-1","org_id":"org-1","user_id":"u-1","filename":"test.txt","file_size":16,"status":"pending","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"},
				"upload_url":"%s/upload",
				"expires_in":3600,
				"content_type":"text/plain"
			}`, uploadServer.URL)
		case r.URL.Path == "/api/v1/files/file-1/complete-upload" && r.Method == http.MethodPost:
			atomic.AddInt32(&completeCalls, 1)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(backendServer.Close)

	client := NewClientWithBaseURL("test-key", backendServer.URL)
	client.uploadClient = uploadServer.Client()
	client.retryBaseDelay = time.Millisecond
	client.maxRetries = 0 // no retries so we get exactly 1 call

	filePath := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(filePath, []byte("fake-file-bytes!"), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := client.UploadOrgFile(context.Background(), filePath, "test.txt", "")
	if err == nil {
		t.Fatal("UploadOrgFile() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "failed to confirm") {
		t.Fatalf("error = %q, want it to contain %q", err.Error(), "failed to confirm")
	}
	if got := atomic.LoadInt32(&completeCalls); got != 1 {
		t.Fatalf("complete-upload calls = %d, want 1", got)
	}
}

func TestHotReloadRelaySessionConnectWebSocketURL_DoesNotEmbedToken(t *testing.T) {
	session := &HotReloadRelaySession{
		ConnectURL:   "wss://relay-a.revyl.ai/api/v1/hotreload/relays/a-123abc/connect",
		ConnectToken: "secret-token",
	}

	got, err := session.ConnectWebSocketURL()
	if err != nil {
		t.Fatalf("ConnectWebSocketURL() error = %v", err)
	}
	if got != session.ConnectURL {
		t.Fatalf("ConnectWebSocketURL() = %q, want %q", got, session.ConnectURL)
	}
	if strings.Contains(got, "secret-token") {
		t.Fatalf("ConnectWebSocketURL() leaked token in url: %q", got)
	}
}

func TestHotReloadRelaySessionConnectAuthHeader(t *testing.T) {
	session := &HotReloadRelaySession{ConnectToken: "secret-token"}

	if got := session.ConnectAuthHeader(); got != "Bearer secret-token" {
		t.Fatalf("ConnectAuthHeader() = %q, want Bearer secret-token", got)
	}
}

func TestCreateHotReloadRelay(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/hotreload/relays" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"relay_id":"a-123abc",
			"public_url":"https://hr-a-123abc.relay-a.revyl.ai",
			"connect_url":"wss://relay-a.revyl.ai/api/v1/hotreload/relays/a-123abc/connect",
			"connect_token":"relay-connect-token",
			"expires_at":"2026-04-10T12:00:00Z",
			"transport":"relay"
		}`)
	}))
	defer server.Close()

	client := NewClientWithBaseURL("backend-token", server.URL)
	resp, err := client.CreateHotReloadRelay(context.Background(), HotReloadRelayCreateParams{
		Provider: "expo",
	})
	if err != nil {
		t.Fatalf("CreateHotReloadRelay() error = %v", err)
	}
	if authorization != "Bearer backend-token" {
		t.Fatalf("Authorization header = %q, want Bearer backend-token", authorization)
	}
	if resp.RelayID != "a-123abc" {
		t.Fatalf("RelayID = %q, want a-123abc", resp.RelayID)
	}
}

func TestProxyWorkerMethodForAction(t *testing.T) {
	t.Parallel()

	// Read-only device-proxy actions: the worker exposes these as GET only,
	// so the CLI must not fall through to the POST default.
	getActions := []string{
		"screenshot",
		"health",
		"device_info",
		"step_status",
		"step_status/abc-123",
		"hierarchy",
		"performance_metrics",
		"network_requests",
		"device_logs",
	}
	for _, action := range getActions {
		if got := proxyWorkerMethodForAction(action); got != http.MethodGet {
			t.Errorf("proxyWorkerMethodForAction(%q) = %q, want GET", action, got)
		}
	}

	// Mutating actions default to POST.
	postActions := []string{"tap", "swipe", "execute_step", "install", "launch"}
	for _, action := range postActions {
		if got := proxyWorkerMethodForAction(action); got != http.MethodPost {
			t.Errorf("proxyWorkerMethodForAction(%q) = %q, want POST", action, got)
		}
	}
}
