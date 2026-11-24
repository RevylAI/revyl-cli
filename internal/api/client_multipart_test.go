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

func withMultipartThreshold(t *testing.T, threshold int64) {
	t.Helper()
	old := multipartUploadThresholdBytes
	multipartUploadThresholdBytes = threshold
	t.Cleanup(func() { multipartUploadThresholdBytes = old })
}

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

type partHandlerFunc func(partNumber int, attempt int32, w http.ResponseWriter)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

type multipartTestBackend struct {
	client *Client

	partAttempts    sync.Map
	partHandlerFn   atomic.Value
	completeBody    chan string
	createBody      chan map[string]interface{}
	completeCalls   int32
	completeStatus  int32
	completeXML     atomic.Value
	completeRespond atomic.Value
	abortCalls      int32
	createCalls     int32
	startCalls      int32
	resumeCalls     int32
	resumeStatus    int32
	resumeUploaded  atomic.Value
	resumeBadSize   int32
	resumeEmptyEtag int32
	failFirstCreate int32

	heldMu    sync.Mutex
	heldParts map[int]int64
}

func (b *multipartTestBackend) attemptsForPart(partNumber int) int32 {
	value, ok := b.partAttempts.Load(partNumber)
	if !ok {
		return 0
	}
	return atomic.LoadInt32(value.(*int32))
}

func (b *multipartTestBackend) setPartHandler(fn partHandlerFunc) {
	b.partHandlerFn.Store(fn)
}

func (b *multipartTestBackend) markPartHeld(partNumber int, size int64) {
	b.heldMu.Lock()
	b.heldParts[partNumber] = size
	b.heldMu.Unlock()
}

func (b *multipartTestBackend) heldPart(partNumber int) (int64, bool) {
	b.heldMu.Lock()
	defer b.heldMu.Unlock()
	size, ok := b.heldParts[partNumber]
	return size, ok
}

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
		heldParts:      make(map[int]int64),
	}
	backend.completeXML.Store("<CompleteMultipartUploadResult><ETag>final</ETag></CompleteMultipartUploadResult>")
	backend.resumeUploaded.Store([]int(nil))
	backend.partHandlerFn.Store(partHandlerFunc(partHandler))

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
			n, _ := io.Copy(io.Discard, r.Body)
			counter, _ := backend.partAttempts.LoadOrStore(partNumber, new(int32))
			attempt := atomic.AddInt32(counter.(*int32), 1)
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			backend.partHandlerFn.Load().(partHandlerFunc)(partNumber, attempt, rec)
			if rec.status < 400 {
				backend.markPartHeld(partNumber, n)
			}
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
			atomic.AddInt32(&backend.startCalls, 1)
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
				"s3_upload_id":      "s3-up-1",
				"part_size":         partSize,
				"upload_expires_at": 123,
				"parts":             parts,
				"complete_url":      s3Server.URL + "/complete",
				"abort_url":         s3Server.URL + "/abort",
			})
		case "/api/v1/apps/app-1/builds/multipart-upload/resume":
			atomic.AddInt32(&backend.resumeCalls, 1)
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("failed to decode resume body: %v", err)
			}
			if status := int(atomic.LoadInt32(&backend.resumeStatus)); status >= 400 {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"detail":"multipart upload no longer exists"}`))
				return
			}
			parts := make([]map[string]interface{}, 0, partCount)
			for n := 1; n <= partCount; n++ {
				parts = append(parts, map[string]interface{}{
					"part_number": n,
					"upload_url":  fmt.Sprintf("%s/part/%d", s3Server.URL, n),
				})
			}
			uploaded := make([]map[string]interface{}, 0)
			if override, _ := backend.resumeUploaded.Load().([]int); override != nil {
				badSize := int(atomic.LoadInt32(&backend.resumeBadSize))
				emptyEtag := int(atomic.LoadInt32(&backend.resumeEmptyEtag))
				for _, n := range override {
					size := partLength(n, partSize, fileSize)
					if n == badSize {
						size++
					}
					etag := fmt.Sprintf("%q", fmt.Sprintf("etag-%d", n))
					if n == emptyEtag {
						etag = ""
					}
					uploaded = append(uploaded, map[string]interface{}{
						"part_number": n,
						"etag":        etag,
						"size":        size,
					})
				}
			} else {
				for n := 1; n <= partCount; n++ {
					if size, ok := backend.heldPart(n); ok {
						uploaded = append(uploaded, map[string]interface{}{
							"part_number": n,
							"etag":        fmt.Sprintf("%q", fmt.Sprintf("etag-%d", n)),
							"size":        size,
						})
					}
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"upload_id":         "up-mp-1",
				"s3_upload_id":      "s3-up-1",
				"part_size":         partSize,
				"upload_expires_at": 123,
				"parts":             parts,
				"uploaded_parts":    uploaded,
				"complete_url":      s3Server.URL + "/complete",
				"abort_url":         s3Server.URL + "/abort",
			})
		case "/api/v1/apps/app-1/builds":
			if r.Method != http.MethodPost {
				t.Errorf("create method = %s, want POST", r.Method)
			}
			call := atomic.AddInt32(&backend.createCalls, 1)
			if call == 1 && atomic.LoadInt32(&backend.failFirstCreate) == 1 {
				http.Error(w, "transient", http.StatusInternalServerError)
				return
			}
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
	client.uploadStateDir = t.TempDir()
	backend.client = client
	return backend
}

func okPartHandler(partNumber int, attempt int32, w http.ResponseWriter) {
	w.Header().Set("ETag", fmt.Sprintf("%q", fmt.Sprintf("etag-%d", partNumber)))
	w.WriteHeader(http.StatusOK)
}

func seedResumeState(t *testing.T, client *Client, appID, version, filePath, fileName string, fileSize, partSize int64) {
	t.Helper()
	hash, err := fileSHA256(filePath)
	if err != nil {
		t.Fatalf("failed to hash seed artifact: %v", err)
	}
	client.saveResumableUploadState(
		uploadStateKey(appID, version, filePath, fileSize),
		&resumableUploadState{
			UploadID:   "up-mp-1",
			S3UploadID: "s3-up-1",
			AppID:      appID,
			Version:    version,
			FileName:   fileName,
			FilePath:   filePath,
			FileSize:   fileSize,
			FileHash:   hash,
			PartSize:   partSize,
		},
	)
}

func resumeStateExists(client *Client, appID, version, filePath string, fileSize int64) bool {
	return client.loadResumableUploadState(uploadStateKey(appID, version, filePath, fileSize)) != nil
}

func TestUploadBuild_SmallFileStaysOnSessionFlow(t *testing.T) {
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
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	backend := newMultipartTestBackend(t, fileSize, partSize, okPartHandler)
	artifactPath := writeTestArtifactOfSize(t, fileSize)

	resp, err := backend.client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: artifactPath,
		Pin:      true,
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
	if got := atomic.LoadInt32(&backend.resumeCalls); got != 0 {
		t.Fatalf("resume calls = %d, want 0 (a fresh upload must not call resume)", got)
	}
	if resumeStateExists(backend.client, "app-1", "v1", artifactPath, fileSize) {
		t.Fatal("resume state should be deleted after a successful upload")
	}

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
	if create["pin"] != true {
		t.Fatalf("create pin = %v, want true", create["pin"])
	}
	if _, sent := create["set_as_current"]; sent {
		t.Fatalf("create sent set_as_current: %v", create)
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

func TestUploadBuild_MultipartTransientStallKeepsStateForResume(t *testing.T) {
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
	backend.client.maxRetries = 0
	artifactPath := writeTestArtifactOfSize(t, fileSize)

	_, err := backend.client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: artifactPath,
	})
	if err == nil {
		t.Fatal("UploadBuild() error = nil, want interrupted-upload error")
	}
	if !strings.Contains(err.Error(), "part 2") {
		t.Fatalf("error = %q, want mention of part 2", err)
	}
	if !strings.Contains(err.Error(), "resume") {
		t.Fatalf("error = %q, want a re-run-to-resume hint", err)
	}

	if got := atomic.LoadInt32(&backend.abortCalls); got != 0 {
		t.Fatalf("abort calls = %d, want 0 (a transient stall must not abort)", got)
	}
	if got := atomic.LoadInt32(&backend.completeCalls); got != 0 {
		t.Fatalf("complete calls = %d, want 0", got)
	}
	if got := atomic.LoadInt32(&backend.createCalls); got != 0 {
		t.Fatalf("create calls = %d, want 0", got)
	}
	if !resumeStateExists(backend.client, "app-1", "v1", artifactPath, fileSize) {
		t.Fatal("resume state should be kept after a transient interruption")
	}
}

func TestUploadBuild_MultipartCancellationKeepsStateForResume(t *testing.T) {
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	ctx, cancel := context.WithCancel(context.Background())
	backend := newMultipartTestBackend(
		t, fileSize, partSize,
		func(partNumber int, attempt int32, w http.ResponseWriter) {
			cancel()
			okPartHandler(partNumber, attempt, w)
		},
	)
	artifactPath := writeTestArtifactOfSize(t, fileSize)

	_, err := backend.client.UploadBuild(ctx, &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: artifactPath,
	})
	if err == nil {
		t.Fatal("UploadBuild() error = nil, want cancellation error")
	}

	if got := atomic.LoadInt32(&backend.abortCalls); got != 0 {
		t.Fatalf("abort calls = %d, want 0 (cancellation must not abort)", got)
	}
	if got := atomic.LoadInt32(&backend.completeCalls); got != 0 {
		t.Fatalf("complete calls = %d, want 0", got)
	}
	if got := atomic.LoadInt32(&backend.createCalls); got != 0 {
		t.Fatalf("create calls = %d, want 0", got)
	}
	if !resumeStateExists(backend.client, "app-1", "v1", artifactPath, fileSize) {
		t.Fatal("resume state should be kept after cancellation")
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
	artifactPath := writeTestArtifactOfSize(t, fileSize)

	_, err := backend.client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: artifactPath,
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
	// A first-attempt NoSuchUpload is permanent — re-completing the same parts
	// can never succeed — so the upload is abandoned (abort + state cleared),
	// not left to loop on every re-run.
	if got := atomic.LoadInt32(&backend.abortCalls); got != 1 {
		t.Fatalf("abort calls = %d, want 1", got)
	}
	if strings.Contains(err.Error(), "resume") {
		t.Fatalf("error = %q, should NOT suggest resume for a permanent failure", err)
	}
	if resumeStateExists(backend.client, "app-1", "v1", artifactPath, fileSize) {
		t.Fatal("resume state should be cleared after a permanent complete failure")
	}
}

func TestUploadBuild_MultipartRetriedNoSuchUploadConvergesToCreate(t *testing.T) {
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
				w.WriteHeader(http.StatusOK)
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
	if got := atomic.LoadInt32(&backend.abortCalls); got != 0 {
		t.Fatalf("abort calls = %d, want 0", got)
	}
	if got := atomic.LoadInt32(&backend.createCalls); got != 0 {
		t.Fatalf("create calls = %d, want 0", got)
	}
}

func TestUploadBuild_MultipartFallsBackWhenUnsupported(t *testing.T) {
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

func TestUploadBuild_ResumeSkipsAlreadyUploadedParts(t *testing.T) {
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	backend := newMultipartTestBackend(t, fileSize, partSize, okPartHandler)
	backend.resumeUploaded.Store([]int{1, 2})
	artifactPath := writeTestArtifactOfSize(t, fileSize)
	seedResumeState(t, backend.client, "app-1", "v1", artifactPath, "artifact.apk", fileSize, partSize)

	resp, err := backend.client.UploadBuild(context.Background(), &UploadBuildRequest{
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

	if got := atomic.LoadInt32(&backend.resumeCalls); got != 1 {
		t.Fatalf("resume calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&backend.startCalls); got != 0 {
		t.Fatalf("start calls = %d, want 0 (resume must not start a new upload)", got)
	}
	for _, part := range []int{1, 2} {
		if got := backend.attemptsForPart(part); got != 0 {
			t.Fatalf("part %d attempts = %d, want 0 (already in S3)", part, got)
		}
	}
	if got := backend.attemptsForPart(3); got != 1 {
		t.Fatalf("part 3 attempts = %d, want 1 (only the missing part uploads)", got)
	}
	if got := atomic.LoadInt32(&backend.createCalls); got != 1 {
		t.Fatalf("create calls = %d, want 1", got)
	}

	completeXML := <-backend.completeBody
	for _, fragment := range []string{
		"<Part><PartNumber>1</PartNumber><ETag>&#34;etag-1&#34;</ETag></Part>",
		"<Part><PartNumber>2</PartNumber><ETag>&#34;etag-2&#34;</ETag></Part>",
		"<Part><PartNumber>3</PartNumber><ETag>&#34;etag-3&#34;</ETag></Part>",
	} {
		if !strings.Contains(completeXML, fragment) {
			t.Fatalf("complete XML missing %q in %q", fragment, completeXML)
		}
	}
	if resumeStateExists(backend.client, "app-1", "v1", artifactPath, fileSize) {
		t.Fatal("resume state should be deleted after a successful resume")
	}
}

func TestUploadBuild_ResumeGoneStartsFresh(t *testing.T) {
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	backend := newMultipartTestBackend(t, fileSize, partSize, okPartHandler)
	atomic.StoreInt32(&backend.resumeStatus, http.StatusGone)
	artifactPath := writeTestArtifactOfSize(t, fileSize)
	seedResumeState(t, backend.client, "app-1", "v1", artifactPath, "artifact.apk", fileSize, partSize)

	resp, err := backend.client.UploadBuild(context.Background(), &UploadBuildRequest{
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
	if got := atomic.LoadInt32(&backend.resumeCalls); got != 1 {
		t.Fatalf("resume calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&backend.startCalls); got != 1 {
		t.Fatalf("start calls = %d, want 1 (a gone upload restarts fresh)", got)
	}
	for part := 1; part <= 3; part++ {
		if got := backend.attemptsForPart(part); got != 1 {
			t.Fatalf("part %d attempts = %d, want 1", part, got)
		}
	}
	if got := atomic.LoadInt32(&backend.createCalls); got != 1 {
		t.Fatalf("create calls = %d, want 1", got)
	}
}

func TestUploadBuild_ResumeArtifactChangedStartsFresh(t *testing.T) {
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	backend := newMultipartTestBackend(t, fileSize, partSize, okPartHandler)
	artifactPath := writeTestArtifactOfSize(t, fileSize)
	seedResumeState(t, backend.client, "app-1", "v1", artifactPath, "artifact.apk", fileSize, partSize)

	changed := make([]byte, fileSize)
	for i := range changed {
		changed[i] = byte('Z')
	}
	if err := os.WriteFile(artifactPath, changed, 0o644); err != nil {
		t.Fatalf("rewrite artifact: %v", err)
	}

	resp, err := backend.client.UploadBuild(context.Background(), &UploadBuildRequest{
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
	if got := atomic.LoadInt32(&backend.resumeCalls); got != 0 {
		t.Fatalf("resume calls = %d, want 0 (a changed artifact must not resume)", got)
	}
	if got := atomic.LoadInt32(&backend.startCalls); got != 1 {
		t.Fatalf("start calls = %d, want 1", got)
	}
}

func TestUploadBuild_AdaptiveConcurrencyEventuallyCompletes(t *testing.T) {
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	backend := newMultipartTestBackend(
		t, fileSize, partSize,
		func(partNumber int, attempt int32, w http.ResponseWriter) {
			if partNumber == 2 && attempt < 4 {
				http.Error(w, "saturated", http.StatusInternalServerError)
				return
			}
			okPartHandler(partNumber, attempt, w)
		},
	)
	backend.client.maxRetries = 0
	artifactPath := writeTestArtifactOfSize(t, fileSize)

	resp, err := backend.client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: artifactPath,
	})
	if err != nil {
		t.Fatalf("UploadBuild() error = %v, want nil (adaptive passes should finish it)", err)
	}
	if resp.VersionID != "ver-1" {
		t.Fatalf("version_id = %q, want ver-1", resp.VersionID)
	}
	if got := backend.attemptsForPart(2); got != 4 {
		t.Fatalf("part 2 attempts = %d, want 4 (retried across passes until it landed)", got)
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
	if resumeStateExists(backend.client, "app-1", "v1", artifactPath, fileSize) {
		t.Fatal("resume state should be deleted after eventual success")
	}
}

func TestUploadConcurrencyEnvOverride(t *testing.T) {
	t.Setenv("REVYL_UPLOAD_CONCURRENCY", "")
	if got := uploadConcurrency(); got != defaultMultipartUploadConcurrency {
		t.Fatalf("default concurrency = %d, want %d", got, defaultMultipartUploadConcurrency)
	}
	t.Setenv("REVYL_UPLOAD_CONCURRENCY", "1")
	if got := uploadConcurrency(); got != 1 {
		t.Fatalf("concurrency with env=1 = %d, want 1", got)
	}
	t.Setenv("REVYL_UPLOAD_CONCURRENCY", "garbage")
	if got := uploadConcurrency(); got != defaultMultipartUploadConcurrency {
		t.Fatalf("invalid env concurrency = %d, want default %d", got, defaultMultipartUploadConcurrency)
	}
	t.Setenv("REVYL_UPLOAD_CONCURRENCY", "0")
	if got := uploadConcurrency(); got != defaultMultipartUploadConcurrency {
		t.Fatalf("env=0 concurrency = %d, want default %d (must be >=1)", got, defaultMultipartUploadConcurrency)
	}
	t.Setenv("REVYL_UPLOAD_CONCURRENCY", "100")
	if got := uploadConcurrency(); got != maxMultipartUploadConcurrency {
		t.Fatalf("env=100 concurrency = %d, want clamp %d", got, maxMultipartUploadConcurrency)
	}
}

func TestUploadBuild_ResumeReuploadsPartWithMismatchedSize(t *testing.T) {
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	backend := newMultipartTestBackend(t, fileSize, partSize, okPartHandler)
	backend.resumeUploaded.Store([]int{1, 2})
	atomic.StoreInt32(&backend.resumeBadSize, 1)
	artifactPath := writeTestArtifactOfSize(t, fileSize)
	seedResumeState(t, backend.client, "app-1", "v1", artifactPath, "artifact.apk", fileSize, partSize)

	resp, err := backend.client.UploadBuild(context.Background(), &UploadBuildRequest{
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
	if got := backend.attemptsForPart(1); got != 1 {
		t.Fatalf("part 1 attempts = %d, want 1 (mismatched size must re-upload)", got)
	}
	if got := backend.attemptsForPart(2); got != 0 {
		t.Fatalf("part 2 attempts = %d, want 0 (valid size, skip)", got)
	}
	if got := backend.attemptsForPart(3); got != 1 {
		t.Fatalf("part 3 attempts = %d, want 1", got)
	}
}

func TestUploadBuild_MultipartPermanentCompleteFailureAborts(t *testing.T) {
	// A non-retryable failure assembling the object (e.g. 400 InvalidPart) can
	// never succeed on a re-run with the same parts, so the upload is abandoned
	// (abort + state cleared) instead of looping with a "re-run to resume" hint.
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	backend := newMultipartTestBackend(t, fileSize, partSize, okPartHandler)
	atomic.StoreInt32(&backend.completeStatus, http.StatusBadRequest)
	backend.completeXML.Store(`<Error><Code>InvalidPart</Code><Message>One or more of the specified parts could not be found.</Message></Error>`)
	artifactPath := writeTestArtifactOfSize(t, fileSize)

	_, err := backend.client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: artifactPath,
	})
	if err == nil {
		t.Fatal("UploadBuild() error = nil, want permanent complete failure")
	}
	if !strings.Contains(err.Error(), "InvalidPart") {
		t.Fatalf("error = %q, want InvalidPart detail", err)
	}
	if strings.Contains(err.Error(), "resume") {
		t.Fatalf("error = %q, should NOT suggest resume for a permanent failure", err)
	}
	if got := atomic.LoadInt32(&backend.completeCalls); got != 1 {
		t.Fatalf("complete calls = %d, want 1 (non-retryable must not retry)", got)
	}
	if got := atomic.LoadInt32(&backend.abortCalls); got != 1 {
		t.Fatalf("abort calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&backend.createCalls); got != 0 {
		t.Fatalf("create calls = %d, want 0", got)
	}
	if resumeStateExists(backend.client, "app-1", "v1", artifactPath, fileSize) {
		t.Fatal("resume state should be cleared after a permanent complete failure")
	}
}

func TestUploadBuild_TwoInvocationResumeCompletes(t *testing.T) {
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	backend := newMultipartTestBackend(
		t, fileSize, partSize,
		func(partNumber int, attempt int32, w http.ResponseWriter) {
			if partNumber == 3 {
				http.Error(w, "interrupted", http.StatusInternalServerError)
				return
			}
			okPartHandler(partNumber, attempt, w)
		},
	)
	backend.client.maxRetries = 0
	artifactPath := writeTestArtifactOfSize(t, fileSize)
	req := &UploadBuildRequest{AppID: "app-1", Version: "v1", FilePath: artifactPath}

	if _, err := backend.client.UploadBuild(context.Background(), req); err == nil {
		t.Fatal("run 1 should fail with a resumable interruption")
	}
	if _, ok := backend.heldPart(1); !ok {
		t.Fatal("run 1 should have stored part 1 in S3")
	}
	if _, ok := backend.heldPart(2); !ok {
		t.Fatal("run 1 should have stored part 2 in S3")
	}
	if _, ok := backend.heldPart(3); ok {
		t.Fatal("part 3 must not be stored after run 1")
	}
	if !resumeStateExists(backend.client, "app-1", "v1", artifactPath, fileSize) {
		t.Fatal("run 1 must leave resume state on disk")
	}
	if got := atomic.LoadInt32(&backend.createCalls); got != 0 {
		t.Fatalf("create calls after run 1 = %d, want 0", got)
	}
	p1, p2, p3 := backend.attemptsForPart(1), backend.attemptsForPart(2), backend.attemptsForPart(3)

	backend.setPartHandler(okPartHandler)
	resp, err := backend.client.UploadBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("run 2 error = %v, want nil", err)
	}
	if resp.VersionID != "ver-1" {
		t.Fatalf("run 2 version_id = %q, want ver-1", resp.VersionID)
	}

	if got := atomic.LoadInt32(&backend.startCalls); got != 1 {
		t.Fatalf("start calls = %d, want 1 (only run 1 starts; run 2 resumes)", got)
	}
	if got := atomic.LoadInt32(&backend.resumeCalls); got != 1 {
		t.Fatalf("resume calls = %d, want 1", got)
	}
	if got := backend.attemptsForPart(1); got != p1 {
		t.Fatalf("part 1 attempts = %d, want %d (must not re-upload on resume)", got, p1)
	}
	if got := backend.attemptsForPart(2); got != p2 {
		t.Fatalf("part 2 attempts = %d, want %d (must not re-upload on resume)", got, p2)
	}
	if got := backend.attemptsForPart(3); got != p3+1 {
		t.Fatalf("part 3 attempts = %d, want %d (exactly one more in run 2)", got, p3+1)
	}
	if got := atomic.LoadInt32(&backend.createCalls); got != 1 {
		t.Fatalf("create calls = %d, want 1", got)
	}
	if resumeStateExists(backend.client, "app-1", "v1", artifactPath, fileSize) {
		t.Fatal("resume state should be deleted after run 2 succeeds")
	}
}

func TestUploadBuild_RecoversFromConnectionResetsUnderConcurrency(t *testing.T) {
	// Reproduces the customer's failure: the link can't sustain parallel PUTs,
	// so S3 connections are reset (a transport error, not an HTTP status)
	// whenever more than one part is in flight — yet a single connection works.
	// The adaptive back-off to one connection must let the upload grind through.
	const fileSize, partSize int64 = 64, 16
	withMultipartThreshold(t, 1)

	var (
		mu          sync.Mutex
		held        = map[int]int64{}
		inflight    int
		maxInflight int
		resetCount  int
	)
	var createCalls int32

	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/part/"):
			part, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/part/"))
			n, _ := io.Copy(io.Discard, r.Body)

			mu.Lock()
			inflight++
			if inflight > maxInflight {
				maxInflight = inflight
			}
			mu.Unlock()
			defer func() { mu.Lock(); inflight--; mu.Unlock() }()

			time.Sleep(10 * time.Millisecond)
			mu.Lock()
			concurrent := inflight
			mu.Unlock()

			if concurrent > 1 {
				mu.Lock()
				resetCount++
				mu.Unlock()
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Errorf("hijack failed: %v", err)
					return
				}
				_ = conn.Close()
				return
			}

			w.Header().Set("ETag", fmt.Sprintf("%q", fmt.Sprintf("etag-%d", part)))
			w.WriteHeader(http.StatusOK)
			mu.Lock()
			held[part] = n
			mu.Unlock()
		case r.URL.Path == "/complete":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<CompleteMultipartUploadResult><ETag>final</ETag></CompleteMultipartUploadResult>"))
		case r.URL.Path == "/abort":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	t.Cleanup(s3.Close)

	partCount := int((fileSize + partSize - 1) / partSize)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/apps/app-1/builds/multipart-upload/start":
			parts := make([]map[string]interface{}, 0, partCount)
			for n := 1; n <= partCount; n++ {
				parts = append(parts, map[string]interface{}{
					"part_number": n,
					"upload_url":  fmt.Sprintf("%s/part/%d", s3.URL, n),
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"upload_id": "up-mp-1", "s3_upload_id": "s3-up-1",
				"part_size": partSize, "upload_expires_at": 123,
				"parts": parts, "complete_url": s3.URL + "/complete", "abort_url": s3.URL + "/abort",
			})
		case "/api/v1/apps/app-1/builds":
			atomic.AddInt32(&createCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"ver-1","version":"v1"}`))
		default:
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	t.Cleanup(backend.Close)

	client := NewClientWithBaseURL("test-key", backend.URL)
	client.uploadStateDir = t.TempDir()
	client.retryBaseDelay = time.Millisecond
	client.retryMaxDelay = 2 * time.Millisecond
	client.maxRetries = 0

	resp, err := client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: writeTestArtifactOfSize(t, int(fileSize)),
	})
	if err != nil {
		t.Fatalf("UploadBuild() error = %v, want nil (back-off should grind through resets)", err)
	}
	if resp.VersionID != "ver-1" {
		t.Fatalf("version_id = %q, want ver-1", resp.VersionID)
	}
	if got := atomic.LoadInt32(&createCalls); got != 1 {
		t.Fatalf("create calls = %d, want 1", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if maxInflight < 2 {
		t.Fatalf("max concurrent PUTs = %d, want >= 2 (the parallel wave must have happened)", maxInflight)
	}
	if resetCount == 0 {
		t.Fatal("no connection resets occurred; the broken-pipe path was not exercised")
	}
	if len(held) != partCount {
		t.Fatalf("parts stored = %d, want %d (every part must land after back-off)", len(held), partCount)
	}
}

func TestUploadBuild_ResumeAfterCompleteSkipsToFinalize(t *testing.T) {
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	backend := newMultipartTestBackend(t, fileSize, partSize, okPartHandler)
	backend.client.maxRetries = 0
	atomic.StoreInt32(&backend.failFirstCreate, 1)
	artifactPath := writeTestArtifactOfSize(t, fileSize)
	req := &UploadBuildRequest{AppID: "app-1", Version: "v1", FilePath: artifactPath}

	if _, err := backend.client.UploadBuild(context.Background(), req); err == nil {
		t.Fatal("run 1 should fail at the create step")
	}
	if got := atomic.LoadInt32(&backend.completeCalls); got != 1 {
		t.Fatalf("complete calls after run 1 = %d, want 1", got)
	}
	if !resumeStateExists(backend.client, "app-1", "v1", artifactPath, fileSize) {
		t.Fatal("state must be kept after a post-complete create failure")
	}
	p1, p2, p3 := backend.attemptsForPart(1), backend.attemptsForPart(2), backend.attemptsForPart(3)

	resp, err := backend.client.UploadBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("run 2 error = %v, want nil", err)
	}
	if resp.VersionID != "ver-1" {
		t.Fatalf("run 2 version_id = %q, want ver-1", resp.VersionID)
	}
	if got := atomic.LoadInt32(&backend.resumeCalls); got != 0 {
		t.Fatalf("resume calls = %d, want 0 (an assembled upload skips straight to finalize)", got)
	}
	if got := atomic.LoadInt32(&backend.completeCalls); got != 1 {
		t.Fatalf("complete calls = %d, want 1 (must not re-complete)", got)
	}
	for part, before := range map[int]int32{1: p1, 2: p2, 3: p3} {
		if got := backend.attemptsForPart(part); got != before {
			t.Fatalf("part %d attempts = %d, want %d (no re-upload on finalize)", part, got, before)
		}
	}
	if got := atomic.LoadInt32(&backend.createCalls); got != 2 {
		t.Fatalf("create calls = %d, want 2 (retried finalize)", got)
	}
	if resumeStateExists(backend.client, "app-1", "v1", artifactPath, fileSize) {
		t.Fatal("state should be deleted after finalize succeeds")
	}
}

func TestUploadBuild_ResumeConflictStartsFresh(t *testing.T) {
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	backend := newMultipartTestBackend(t, fileSize, partSize, okPartHandler)
	atomic.StoreInt32(&backend.resumeStatus, http.StatusConflict)
	artifactPath := writeTestArtifactOfSize(t, fileSize)
	seedResumeState(t, backend.client, "app-1", "v1", artifactPath, "artifact.apk", fileSize, partSize)

	resp, err := backend.client.UploadBuild(context.Background(), &UploadBuildRequest{
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
	if got := atomic.LoadInt32(&backend.resumeCalls); got != 1 {
		t.Fatalf("resume calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&backend.startCalls); got != 1 {
		t.Fatalf("start calls = %d, want 1 (a 409 resume must discard state and start fresh)", got)
	}
}

func TestUploadBuild_ResumeReuploadsPartWithEmptyETag(t *testing.T) {
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	backend := newMultipartTestBackend(t, fileSize, partSize, okPartHandler)
	backend.resumeUploaded.Store([]int{1, 2})
	atomic.StoreInt32(&backend.resumeEmptyEtag, 1)
	artifactPath := writeTestArtifactOfSize(t, fileSize)
	seedResumeState(t, backend.client, "app-1", "v1", artifactPath, "artifact.apk", fileSize, partSize)

	resp, err := backend.client.UploadBuild(context.Background(), &UploadBuildRequest{
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
	if got := backend.attemptsForPart(1); got != 1 {
		t.Fatalf("part 1 attempts = %d, want 1 (empty ETag must re-upload)", got)
	}
	if got := backend.attemptsForPart(2); got != 0 {
		t.Fatalf("part 2 attempts = %d, want 0 (valid ETag, skip)", got)
	}
	if got := backend.attemptsForPart(3); got != 1 {
		t.Fatalf("part 3 attempts = %d, want 1", got)
	}
}

func TestUploadBuild_ResumeTransientStatusKeepsState(t *testing.T) {
	const fileSize, partSize = 40, 16
	withMultipartThreshold(t, 30)

	backend := newMultipartTestBackend(t, fileSize, partSize, okPartHandler)
	backend.client.maxRetries = 0
	atomic.StoreInt32(&backend.resumeStatus, http.StatusTooManyRequests)
	artifactPath := writeTestArtifactOfSize(t, fileSize)
	seedResumeState(t, backend.client, "app-1", "v1", artifactPath, "artifact.apk", fileSize, partSize)

	_, err := backend.client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: artifactPath,
	})
	if err == nil {
		t.Fatal("UploadBuild() error = nil, want the transient resume error surfaced")
	}
	if got := atomic.LoadInt32(&backend.startCalls); got != 0 {
		t.Fatalf("start calls = %d, want 0 (a transient resume failure must not start fresh)", got)
	}
	if !resumeStateExists(backend.client, "app-1", "v1", artifactPath, fileSize) {
		t.Fatal("resume state must be kept after a transient resume failure")
	}
}
