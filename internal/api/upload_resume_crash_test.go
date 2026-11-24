package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestMain lets a test re-exec the test binary as a real upload "process" (see
// runUploadHelper) so the crash/resume test can kill it mid-transfer. Without
// the helper env var it just runs the package's tests normally.
func TestMain(m *testing.M) {
	if os.Getenv("REVYL_TEST_UPLOAD_HELPER") == "1" {
		runUploadHelper()
		return
	}
	os.Exit(m.Run())
}

// runUploadHelper performs a single real UploadBuild against the servers and
// state dir named by the environment, then exits. It is the child process the
// crash test kills and re-launches; nothing here installs a signal handler, so
// a kill is as abrupt as a real crash.
func runUploadHelper() {
	// Force the multipart path for the tiny test artifact (the parent's
	// withMultipartThreshold override doesn't cross the process boundary).
	multipartUploadThresholdBytes = 1
	client := NewClientWithBaseURL("test-key", os.Getenv("REVYL_TEST_BACKEND_URL"))
	client.uploadStateDir = os.Getenv("REVYL_TEST_STATE_DIR")
	client.retryBaseDelay = time.Millisecond
	client.retryMaxDelay = 2 * time.Millisecond
	_, err := client.UploadBuild(context.Background(), &UploadBuildRequest{
		AppID:    "app-1",
		Version:  "v1",
		FilePath: os.Getenv("REVYL_TEST_ARTIFACT"),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper upload error:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestUploadBuild_SurvivesProcessDeathAndResumes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal semantics; not exercised on Windows")
	}
	// SIGKILL models a hard crash (uncatchable); SIGINT models Ctrl-C. Neither
	// runs any cleanup in the child, so both must be recoverable purely from the
	// write-ahead state file plus the parts S3 already holds.
	t.Run("SIGKILL", func(t *testing.T) {
		runCrashResumeScenario(t, func(p *os.Process) { _ = p.Kill() })
	})
	t.Run("SIGINT", func(t *testing.T) {
		runCrashResumeScenario(t, func(p *os.Process) { _ = p.Signal(os.Interrupt) })
	})
}

func runCrashResumeScenario(t *testing.T, killChild func(*os.Process)) {
	const fileSize, partSize int64 = 40, 16 // 3 parts: 16 + 16 + 8

	artifactDir := t.TempDir()
	artifactPath := filepath.Join(artifactDir, "artifact.apk")
	payload := make([]byte, fileSize)
	for i := range payload {
		payload[i] = byte('a' + i%26)
	}
	if err := os.WriteFile(artifactPath, payload, 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	stateDir := t.TempDir()

	var (
		heldMu      sync.Mutex
		heldParts   = map[int]int64{}
		allowPart3  atomic.Bool
		createCalls atomic.Int32
		stored2     = make(chan struct{}, 1)
		testDone    = make(chan struct{})
	)
	t.Cleanup(func() { close(testDone) })

	markHeld := func(part int, size int64) {
		heldMu.Lock()
		heldParts[part] = size
		n := len(heldParts)
		heldMu.Unlock()
		if n == 2 {
			select {
			case stored2 <- struct{}{}:
			default:
			}
		}
	}

	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/part/"):
			part, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/part/"))
			n, _ := io.Copy(io.Discard, r.Body)
			if part == 3 && !allowPart3.Load() {
				// Block so the child hangs with parts 1-2 stored, giving the
				// test a deterministic moment to kill it. Unblock when the
				// child's connection drops (it was killed) or the test ends.
				select {
				case <-r.Context().Done():
				case <-testDone:
				}
				return
			}
			w.Header().Set("ETag", fmt.Sprintf("%q", fmt.Sprintf("etag-%d", part)))
			w.WriteHeader(http.StatusOK)
			markHeld(part, n)
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
	partURLs := func() []map[string]interface{} {
		parts := make([]map[string]interface{}, 0, partCount)
		for n := 1; n <= partCount; n++ {
			parts = append(parts, map[string]interface{}{
				"part_number": n,
				"upload_url":  fmt.Sprintf("%s/part/%d", s3.URL, n),
			})
		}
		return parts
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/apps/app-1/builds/multipart-upload/start":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"upload_id": "up-mp-1", "s3_upload_id": "s3-up-1",
				"part_size": partSize, "upload_expires_at": 123,
				"parts": partURLs(), "complete_url": s3.URL + "/complete", "abort_url": s3.URL + "/abort",
			})
		case "/api/v1/apps/app-1/builds/multipart-upload/resume":
			uploaded := make([]map[string]interface{}, 0)
			heldMu.Lock()
			for n := 1; n <= partCount; n++ {
				if size, ok := heldParts[n]; ok {
					uploaded = append(uploaded, map[string]interface{}{
						"part_number": n,
						"etag":        fmt.Sprintf("%q", fmt.Sprintf("etag-%d", n)),
						"size":        size,
					})
				}
			}
			heldMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"upload_id": "up-mp-1", "s3_upload_id": "s3-up-1",
				"part_size": partSize, "upload_expires_at": 123,
				"parts": partURLs(), "uploaded_parts": uploaded,
				"complete_url": s3.URL + "/complete", "abort_url": s3.URL + "/abort",
			})
		case "/api/v1/apps/app-1/builds":
			createCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"ver-1","version":"v1"}`))
		default:
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	t.Cleanup(backend.Close)

	childEnv := append(os.Environ(),
		"REVYL_TEST_UPLOAD_HELPER=1",
		"REVYL_TEST_BACKEND_URL="+backend.URL,
		"REVYL_TEST_STATE_DIR="+stateDir,
		"REVYL_TEST_ARTIFACT="+artifactPath,
	)

	// Run 1: launch, wait until parts 1-2 are in S3, then kill it mid-transfer.
	run1 := exec.Command(os.Args[0])
	run1.Env = childEnv
	var run1err bytes.Buffer
	run1.Stderr = &run1err
	if err := run1.Start(); err != nil {
		t.Fatalf("start run 1: %v", err)
	}
	select {
	case <-stored2:
	case <-time.After(30 * time.Second):
		_ = run1.Process.Kill()
		t.Fatalf("timed out waiting for run 1 to store 2 parts; stderr=%s", run1err.String())
	}
	killChild(run1.Process)
	_ = run1.Wait() // killed mid-flight -> non-nil error expected

	// The crash left exactly the durable state that makes resume possible.
	if _, ok := heldParts[3]; ok {
		t.Fatal("part 3 must not be stored before the kill")
	}
	if n := countStateFiles(t, stateDir); n != 1 {
		t.Fatalf("resume state files after crash = %d, want 1", n)
	}
	if got := createCalls.Load(); got != 0 {
		t.Fatalf("create calls after crash = %d, want 0", got)
	}

	// Run 2: a fresh process resumes and finishes — no manual re-stitching.
	allowPart3.Store(true)
	run2 := exec.Command(os.Args[0])
	run2.Env = childEnv
	if out, err := run2.CombinedOutput(); err != nil {
		t.Fatalf("run 2 (resume) failed: %v\n%s", err, out)
	}

	if got := createCalls.Load(); got != 1 {
		t.Fatalf("create calls after resume = %d, want 1", got)
	}
	if _, ok := heldParts[3]; !ok {
		t.Fatal("resume run should have uploaded the missing part 3")
	}
	if n := countStateFiles(t, stateDir); n != 0 {
		t.Fatalf("resume state files after success = %d, want 0 (must be cleared)", n)
	}
}

func countStateFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read state dir: %v", err)
	}
	count := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			count++
		}
	}
	return count
}
