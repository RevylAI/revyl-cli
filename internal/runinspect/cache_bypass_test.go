package runinspect

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func gzipBytes(t *testing.T, payload string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte(payload)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// TestFetchDeviceStateLines_LiveURLBypassesCache pins the fix for the
// cache-poisoning bug: a still-growing live object (URL under /live/) must
// never be written to the taskID-keyed cache, regardless of the cacheDir the
// caller passes — otherwise a later post-run read (e.g. `revyl run state`) is
// served the stale partial, and a mid-run `revyl run summary` poisons it.
func TestFetchDeviceStateLines_LiveURLBypassesCache(t *testing.T) {
	body := gzipBytes(t, `{"path":"a.plist","step":2}`+"\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	taskID := "task-live"

	liveReport := &Report{
		TaskID:         taskID,
		DeviceStateURL: srv.URL + "/sess/live/device_state.jsonl.gz",
	}
	lines, err := FetchDeviceStateLines(context.Background(), liveReport, srv.Client(), cacheDir)
	if err != nil {
		t.Fatalf("fetch live: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 parsed line, got %d", len(lines))
	}

	cachePath := filepath.Join(cacheDir, taskID, "device_state.jsonl")
	if _, statErr := os.Stat(cachePath); !os.IsNotExist(statErr) {
		t.Fatalf("live object must NOT be cached, but %s exists (err=%v)", cachePath, statErr)
	}
}

// TestFetchDeviceStateLines_FinalizedURLIsCached is the contrast: a finalized
// object (no /live/ segment) is cached under the taskID key as before.
func TestFetchDeviceStateLines_FinalizedURLIsCached(t *testing.T) {
	body := gzipBytes(t, `{"path":"a.plist","step":9}`+"\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	taskID := "task-final"
	report := &Report{
		TaskID:         taskID,
		DeviceStateURL: srv.URL + "/sess/device_state.jsonl.gz",
	}
	if _, err := FetchDeviceStateLines(context.Background(), report, srv.Client(), cacheDir); err != nil {
		t.Fatalf("fetch finalized: %v", err)
	}
	cachePath := filepath.Join(cacheDir, taskID, "device_state.jsonl")
	if _, statErr := os.Stat(cachePath); statErr != nil {
		t.Fatalf("finalized object should be cached at %s: %v", cachePath, statErr)
	}
}
