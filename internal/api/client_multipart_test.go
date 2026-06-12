package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// withMultipartThreshold lowers the multipart threshold so small test
// artifacts exercise the multipart path, restoring it afterwards.
func withMultipartThreshold(t *testing.T, threshold int64) {
	t.Helper()
	old := multipartUploadThresholdBytes
	multipartUploadThresholdBytes = threshold
	t.Cleanup(func() { multipartUploadThresholdBytes = old })
}

// writeTestArtifactOfSize writes an .apk placeholder of exactly size bytes.
func writeTestArtifactOfSize(t *testing.T, size int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact.apk")
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte('a' + i%26)
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("failed to write test artifact: %v", err)
	}
	return path
}

// multipartTestBackend wires httptest servers for the staging multipart flow:
// parts, complete, and abort go to a fake S3; start and the final create call
// go to a fake backend.
type multipartTestBackend struct {
	client *Client

	partAttempts   sync.Map // part number (int) -> *int32
	completeBody   chan string
	createBody     chan map[string]interface{}
	completeCalls  int32
	completeStatus int32        // HTTP status the S3 complete endpoint responds with
	completeXML    atomic.Value // string body the S3 complete endpoint returns
	// completeRespond, when stored, overrides completeStatus/completeXML with
	// a per-call response (func(call int32, w http.ResponseWriter)).
	completeRespond atomic.Value
	abortCalls      int32
	createCalls     int32
}

func (b *multipartTestBackend) attemptsForPart(partNumber int) int32 {
	value, ok := b.partAttempts.Load(partNumber)
	if !ok {
		return 0
	}
	return atomic.LoadInt32(value.(*int32))
}

// newMultipartTestBackend serves a multipart flow for fileSize/partSize and
// lets partHandler decide each part PUT's response (after the attempt is
// counted). Part PUT bodies are always drained.
func newMultipartTestBackend(
	t *testing.T,
	fileSize, partSize int64,
	partHandler func(partNumber int, attempt int32, w http.ResponseWriter),
) *multipartTestBackend {
	t.Helper()
	backend := &multipartTestBackend{
		completeBody:   make(chan string, 1),
		createBody:     make(chan map[string]interface{}, 1),
		completeStatus: http.StatusOK,
	}
	backend.completeXML.Store("<CompleteMultipartUploadResult><ETag>final</ETag></CompleteMultipartUploadResult>")

	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/part/"):
			if r.Method != http.MethodPut {
				t.Errorf("part upload method = %s, want PUT", r.Method)
			}
			partNumber, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/part/"))
			if err != nil {
				t.Errorf("unexpected part upload path: %s", r.URL.Path)
			}
			_, _ = io.Copy(io.Discard, r.Body)
			counter, _ := backend.partAttempts.LoadOrStore(partNumber, new(int32))
			attempt := atomic.AddInt32(counter.(*int32), 1)
			partHandler(partNumber, attempt, w)
		case r.URL.Path == "/complete":
			if r.Method != http.MethodPost {
				t.Errorf("complete method = %s, want POST", r.Method)
			}
			call := atomic.AddInt32(&backend.completeCalls, 1)
			body, _ := io.ReadAll(r.Body)
			select {
			case backend.completeBody <- string(body):
			default:
			}
			if respond, ok := backend.completeRespond.Load().(func(int32, http.ResponseWriter)); ok {
				respond(call, w)
				return
			}
			status := int(atomic.LoadInt32(&backend.completeStatus))
			w.WriteHeader(status)
			_, _ = w.Write([]byte(backend.completeXML.Load().(string)))
		case r.URL.Path == "/abort":
			if r.Method != http.MethodDelete {
				t.Errorf("abort method = %s, want DELETE", r.Method)
			}
			atomic.AddInt32(&backend.abortCalls, 1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected fake-S3 path: %s", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	t.Cleanup(s3Server.Close)

	partCount := int((fileSize + partSize - 1) / partSize)
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/apps/app-1/builds/multipart-upload/start":
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("failed to decode start body: %v", err)
			}
			if got, want := body["file_size"], float64(fileSize); got != want {
				t.Errorf("start file_size = %v, want %v", got, want)
			}
			parts := make([]map[string]interface{}, 0, partCount)
			for n := 1; n <= partCount; n++ {
				parts = append(parts, map[string]interface{}{
					"part_number": n,
					"upload_url":  fmt.Sprintf("%s/part/%d", s3Server.URL, n),
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"upload_id":         "up-mp-1",
				"part_size":         partSize,
				"upload_expires_at": 123,
				"parts":             parts,
				"complete_url":      s3Server.URL + "/complete",
				"abort_url":         s3Server.URL + "/abort",
			})
		case "/api/v1/apps/app-1/builds":
			if r.Method != http.MethodPost {
				t.Errorf("create method = %s, want POST", r.Method)
			}
			atomic.AddInt32(&backend.createCalls, 1)
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("failed to decode create body: %v", err)
			}
			select {
			case backend.createBody <- body:
			default:
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id":"ver-1",
				"version":"v1",
				"package_name":"com.example.app",
				"metadata":{
					"artifact_validation":{
						"warnings":["This Android APK does not appear to be debuggable."]
					}
				}
			}`))
		default:
			t.Errorf("unexpected backend path: %s", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	t.Cleanup(backendServer.Close)

	client := NewClientWithBaseURL("test-key", backendServer.URL)
	client.uploadClient = s3Server.Client()
	client.retryBaseDelay = time.Millisecond
	client.retryMaxDelay = 2 * time.Millisecond
	backend.client = client
	return backend
}

func okPartHandler(partNumber int, attempt int32, w http.ResponseWriter) {
	w.Header().Set("ETag", fmt.Sprintf("%q", fmt.Sprintf("etag-%d", partNumber)))
	w.WriteHeader(http.StatusOK)
}

func TestUploadBuild_SmallFileStaysOnSessionFlow(t *testing.T) {
	// Default threshold: a tiny artifact must never touch the multipart
	// endpoints — the session harness fatals on any unexpected path.
	client, artifactPath, uploadAttempts, _ := testUploadBuildClient(
		t,
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"ver-1","version":"v1"}`))
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
		t.Fatalf("version_id = %q, want ver-1", resp.VersionID)
	}
	if got := atomic.LoadInt32(uploadAttempts); got != 1 {
		t.Fatalf("single PUT attempts = %d, want 1", got)
	}
}

func TestUploadBuild_LargeFileUsesMultipartFlow(t *testing.T) {
	const fileSize, partSize = 40, 16 // 3 parts: 16 + 16 + 8 bytes
	withMultipartThreshold(t, 30)

	backend := newMultipartTestBackend(t, fileSize, partSize, okPartHandler)

	resp, err := backend.client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:        "app-1",
		Version:      "v1",
		FilePath:     writeTestArtifactOfSize(t, fileSize),
		SetAsCurrent: true,
	})
	if err != nil {
		t.Fatalf("UploadBuild() error = %v, want nil", err)
	}
	if resp.VersionID != "ver-1" {
		t.Fatalf("version_id = %q, want ver-1", resp.VersionID)
	}
	if resp.PackageID != "com.example.app" {
		t.Fatalf("package_id = %q, want com.example.app", resp.PackageID)
	}
	if len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "debuggable") {
		t.Fatalf("warnings = %#v, want debuggable warning from create response", resp.Warnings)
	}

	for part := 1; part <= 3; part++ {
		if got := backend.attemptsForPart(part); got != 1 {
			t.Fatalf("part %d attempts = %d, want 1", part, got)
		}
	}
	if got := atomic.LoadInt32(&backend.completeCalls); got != 1 {
		t.Fatalf("complete calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&backend.createCalls); got != 1 {
		t.Fatalf("create calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&backend.abortCalls); got != 0 {
		t.Fatalf("abort calls = %d, want 0", got)
	}

	// The S3 complete body lists parts with their ETags in part-number order.
	completeXML := <-backend.completeBody
	wantOrder := []string{
		"<Part><PartNumber>1</PartNumber><ETag>&#34;etag-1&#34;</ETag></Part>",
		"<Part><PartNumber>2</PartNumber><ETag>&#34;etag-2&#34;</ETag></Part>",
		"<Part><PartNumber>3</PartNumber><ETag>&#34;etag-3&#34;</ETag></Part>",
	}
	lastIndex := -1
	for _, fragment := range wantOrder {
		index := strings.Index(completeXML, fragment)
		if index < 0 {
			t.Fatalf("complete XML missing %q in %q", fragment, completeXML)
		}
		if index < lastIndex {
			t.Fatalf("complete XML parts out of order: %q", completeXML)
		}
		lastIndex = index
	}

	create := <-backend.createBody
	if create["upload_id"] != "up-mp-1" {
		t.Fatalf("create upload_id = %v, want up-mp-1", create["upload_id"])
	}
	if create["set_as_current"] != true {
		t.Fatalf("create set_as_current = %v, want true", create["set_as_current"])
	}
}

func TestUploadBuild_MultipartRetriesOnlyFailedPart(t *testing.T) {
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	backend := newMultipartTestBackend(
		t, fileSize, partSize,
		func(partNumber int, attempt int32, w http.ResponseWriter) {
			if partNumber == 2 && attempt == 1 {
				http.Error(w, "broken pipe", http.StatusInternalServerError)
				return
			}
			okPartHandler(partNumber, attempt, w)
		},
	)

	_, err := backend.client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: writeTestArtifactOfSize(t, fileSize),
	})
	if err != nil {
		t.Fatalf("UploadBuild() error = %v, want nil", err)
	}

	if got := backend.attemptsForPart(1); got != 1 {
		t.Fatalf("part 1 attempts = %d, want 1 (must not retry the whole artifact)", got)
	}
	if got := backend.attemptsForPart(2); got != 2 {
		t.Fatalf("part 2 attempts = %d, want 2", got)
	}
	if got := backend.attemptsForPart(3); got != 1 {
		t.Fatalf("part 3 attempts = %d, want 1 (must not retry the whole artifact)", got)
	}
	if got := atomic.LoadInt32(&backend.completeCalls); got != 1 {
		t.Fatalf("complete calls = %d, want 1", got)
	}
}

func TestUploadBuild_MultipartAbortsOnExhaustedRetries(t *testing.T) {
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	backend := newMultipartTestBackend(
		t, fileSize, partSize,
		func(partNumber int, attempt int32, w http.ResponseWriter) {
			if partNumber == 2 {
				http.Error(w, "still broken", http.StatusInternalServerError)
				return
			}
			okPartHandler(partNumber, attempt, w)
		},
	)
	backend.client.maxRetries = 1

	_, err := backend.client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: writeTestArtifactOfSize(t, fileSize),
	})
	if err == nil {
		t.Fatal("UploadBuild() error = nil, want part-upload failure")
	}
	if !strings.Contains(err.Error(), "part 2") {
		t.Fatalf("error = %q, want mention of part 2", err)
	}

	if got := backend.attemptsForPart(2); got != 2 {
		t.Fatalf("part 2 attempts = %d, want 2 (maxRetries+1)", got)
	}
	if got := atomic.LoadInt32(&backend.abortCalls); got != 1 {
		t.Fatalf("abort calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&backend.completeCalls); got != 0 {
		t.Fatalf("complete calls = %d, want 0", got)
	}
	if got := atomic.LoadInt32(&backend.createCalls); got != 0 {
		t.Fatalf("create calls = %d, want 0", got)
	}
}

func TestUploadBuild_MultipartCancellationAborts(t *testing.T) {
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	ctx, cancel := context.WithCancel(context.Background())
	backend := newMultipartTestBackend(
		t, fileSize, partSize,
		func(partNumber int, attempt int32, w http.ResponseWriter) {
			// Cancel mid-upload: the first part to arrive pulls the plug.
			cancel()
			okPartHandler(partNumber, attempt, w)
		},
	)

	_, err := backend.client.UploadBuild(ctx, &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: writeTestArtifactOfSize(t, fileSize),
	})
	if err == nil {
		t.Fatal("UploadBuild() error = nil, want cancellation error")
	}

	if got := atomic.LoadInt32(&backend.abortCalls); got != 1 {
		t.Fatalf("abort calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&backend.completeCalls); got != 0 {
		t.Fatalf("complete calls = %d, want 0", got)
	}
	if got := atomic.LoadInt32(&backend.createCalls); got != 0 {
		t.Fatalf("create calls = %d, want 0", got)
	}
}

func TestUploadBuild_MultipartFirstAttemptNoSuchUploadFails(t *testing.T) {
	// NoSuchUpload on the very first complete attempt means the upload was
	// aborted or expired out-of-band — no earlier attempt can have assembled
	// the object, so the CLI must fail with the real reason instead of
	// reporting success and deferring to a misleading create error.
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	backend := newMultipartTestBackend(t, fileSize, partSize, okPartHandler)
	atomic.StoreInt32(&backend.completeStatus, http.StatusNotFound)
	backend.completeXML.Store(`<Error><Code>NoSuchUpload</Code><Message>The specified upload does not exist.</Message></Error>`)

	_, err := backend.client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: writeTestArtifactOfSize(t, fileSize),
	})
	if err == nil {
		t.Fatal("UploadBuild() error = nil, want aborted-or-expired error")
	}
	if !strings.Contains(err.Error(), "aborted or expired") {
		t.Fatalf("error = %q, want aborted-or-expired detail", err)
	}
	if got := atomic.LoadInt32(&backend.completeCalls); got != 1 {
		t.Fatalf("complete calls = %d, want 1 (NoSuchUpload must not retry)", got)
	}
	if got := atomic.LoadInt32(&backend.createCalls); got != 0 {
		t.Fatalf("create calls = %d, want 0", got)
	}
	if got := atomic.LoadInt32(&backend.abortCalls); got != 1 {
		t.Fatalf("abort calls = %d, want 1", got)
	}
}

func TestUploadBuild_MultipartRetriedNoSuchUploadConvergesToCreate(t *testing.T) {
	// A retried complete whose earlier attempt already assembled the object
	// gets NoSuchUpload from S3 — the CLI proceeds to the create call, which
	// is the authority on whether the staged artifact exists.
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	backend := newMultipartTestBackend(t, fileSize, partSize, okPartHandler)
	backend.completeRespond.Store(func(call int32, w http.ResponseWriter) {
		if call == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`<Error><Code>InternalError</Code></Error>`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<Error><Code>NoSuchUpload</Code><Message>The specified upload does not exist.</Message></Error>`))
	})

	resp, err := backend.client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: writeTestArtifactOfSize(t, fileSize),
	})
	if err != nil {
		t.Fatalf("UploadBuild() error = %v, want nil", err)
	}
	if resp.VersionID != "ver-1" {
		t.Fatalf("version_id = %q, want ver-1", resp.VersionID)
	}
	if got := atomic.LoadInt32(&backend.completeCalls); got != 2 {
		t.Fatalf("complete calls = %d, want 2", got)
	}
	if got := atomic.LoadInt32(&backend.createCalls); got != 1 {
		t.Fatalf("create calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&backend.abortCalls); got != 0 {
		t.Fatalf("abort calls = %d, want 0", got)
	}
}

func TestUploadBuild_MultipartMissingETagFailsWithoutRetry(t *testing.T) {
	// A 2xx part PUT without an ETag header is deterministic (S3 always sends
	// one; only a header-stripping proxy loses it) — retrying re-uploads the
	// part for the same outcome, so the part must fail fast.
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	backend := newMultipartTestBackend(
		t, fileSize, partSize,
		func(partNumber int, attempt int32, w http.ResponseWriter) {
			if partNumber == 1 {
				w.WriteHeader(http.StatusOK) // no ETag header
				return
			}
			okPartHandler(partNumber, attempt, w)
		},
	)

	_, err := backend.client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: writeTestArtifactOfSize(t, fileSize),
	})
	if err == nil {
		t.Fatal("UploadBuild() error = nil, want missing-ETag error")
	}
	if !strings.Contains(err.Error(), "no ETag") {
		t.Fatalf("error = %q, want missing-ETag detail", err)
	}
	if got := backend.attemptsForPart(1); got != 1 {
		t.Fatalf("part 1 attempts = %d, want 1 (missing ETag must not retry)", got)
	}
	if got := atomic.LoadInt32(&backend.abortCalls); got != 1 {
		t.Fatalf("abort calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&backend.createCalls); got != 0 {
		t.Fatalf("create calls = %d, want 0", got)
	}
}

func TestUploadBuild_MultipartRetries200WithErrorBody(t *testing.T) {
	// S3 can answer CompleteMultipartUpload with HTTP 200 and an <Error>
	// document in the body; the CLI must treat that as a retryable failure,
	// not success.
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	backend := newMultipartTestBackend(t, fileSize, partSize, okPartHandler)
	backend.completeXML.Store(`<Error><Code>InternalError</Code><Message>We encountered an internal error.</Message></Error>`)
	backend.client.maxRetries = 1

	_, err := backend.client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: writeTestArtifactOfSize(t, fileSize),
	})
	if err == nil {
		t.Fatal("UploadBuild() error = nil, want complete failure")
	}
	if !strings.Contains(err.Error(), "InternalError") {
		t.Fatalf("error = %q, want InternalError detail", err)
	}

	if got := atomic.LoadInt32(&backend.completeCalls); got != 2 {
		t.Fatalf("complete calls = %d, want 2 (200-with-error must retry)", got)
	}
	if got := atomic.LoadInt32(&backend.abortCalls); got != 1 {
		t.Fatalf("abort calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&backend.createCalls); got != 0 {
		t.Fatalf("create calls = %d, want 0", got)
	}
}

func TestUploadBuild_MultipartFallsBackWhenUnsupported(t *testing.T) {
	// A backend without the multipart endpoints (404) must fall back to the
	// session flow so a new CLI keeps working against an older backend.
	withMultipartThreshold(t, 4)

	uploadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(uploadServer.Close)

	var sessionUsed int32
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/apps/app-1/builds/multipart-upload/start":
			http.Error(w, "not found", http.StatusNotFound)
		case "/api/v1/apps/app-1/builds/upload-session":
			atomic.AddInt32(&sessionUsed, 1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(
				w,
				`{"upload_id":"up-1","upload_url":"%s/upload","upload_expires_at":123,"content_type":"application/vnd.android.package-archive"}`,
				uploadServer.URL,
			)
		case "/api/v1/apps/app-1/builds":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"ver-1","version":"v1"}`))
		default:
			t.Errorf("unexpected backend path: %s", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	t.Cleanup(backendServer.Close)

	client := NewClientWithBaseURL("test-key", backendServer.URL)
	client.uploadClient = uploadServer.Client()

	resp, err := client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: writeTestArtifactOfSize(t, 16),
	})
	if err != nil {
		t.Fatalf("UploadBuild() error = %v, want nil", err)
	}
	if resp.VersionID != "ver-1" {
		t.Fatalf("version_id = %q, want ver-1", resp.VersionID)
	}
	if got := atomic.LoadInt32(&sessionUsed); got != 1 {
		t.Fatalf("upload-session calls = %d, want 1", got)
	}
}

func TestUploadBuild_MultipartFallbackRejectsOversizedSinglePut(t *testing.T) {
	// When an old backend lacks multipart support, the single-PUT fallback is
	// hard-capped by S3 at 5 GiB — an artifact above the cap must fail fast
	// with the real reason instead of dying mid-transfer on an opaque S3 error.
	withMultipartThreshold(t, 4)
	oldMax := maxSinglePutUploadBytes
	maxSinglePutUploadBytes = 8
	t.Cleanup(func() { maxSinglePutUploadBytes = oldMax })

	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/apps/app-1/builds/multipart-upload/start" {
			t.Errorf("unexpected backend path: %s", r.URL.Path)
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(backendServer.Close)

	client := NewClientWithBaseURL("test-key", backendServer.URL)

	_, err := client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: writeTestArtifactOfSize(t, 16),
	})
	if err == nil {
		t.Fatal("UploadBuild() error = nil, want single-upload-limit error")
	}
	if !strings.Contains(err.Error(), "does not support multipart uploads") {
		t.Fatalf("error = %q, want backend-support detail", err)
	}
}

func TestUploadBuild_MultipartAppNotFoundDoesNotFallBack(t *testing.T) {
	// A 404 from a matched route ("App not found") is a real resource error;
	// falling back would cascade the bad app id through every legacy flow and
	// surface the error from the wrong endpoint.
	withMultipartThreshold(t, 4)

	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/apps/app-1/builds/multipart-upload/start" {
			t.Errorf("unexpected backend path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"App not found"}`))
	}))
	t.Cleanup(backendServer.Close)

	client := NewClientWithBaseURL("test-key", backendServer.URL)

	_, err := client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: writeTestArtifactOfSize(t, 16),
	})
	if err == nil {
		t.Fatal("UploadBuild() error = nil, want app-not-found error")
	}
	if !strings.Contains(err.Error(), "App not found") {
		t.Fatalf("error = %q, want App not found", err)
	}
}
