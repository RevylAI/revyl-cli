package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUploadStateKeyStableAndDistinct(t *testing.T) {
	base := uploadStateKey("app-1", "v1", "/tmp/a.apk", 100)
	if base == "" {
		t.Fatal("uploadStateKey returned empty string")
	}
	if again := uploadStateKey("app-1", "v1", "/tmp/a.apk", 100); again != base {
		t.Fatalf("uploadStateKey not stable: %q != %q", again, base)
	}

	for name, key := range map[string]string{
		"different app":     uploadStateKey("app-2", "v1", "/tmp/a.apk", 100),
		"different version": uploadStateKey("app-1", "v2", "/tmp/a.apk", 100),
		"different path":    uploadStateKey("app-1", "v1", "/tmp/b.apk", 100),
		"different size":    uploadStateKey("app-1", "v1", "/tmp/a.apk", 101),
	} {
		if key == base {
			t.Fatalf("%s should produce a distinct key, got the same as base", name)
		}
	}
}

func TestResumableUploadStateRoundTrip(t *testing.T) {
	client := NewClientWithBaseURL("k", "http://example.invalid")
	client.uploadStateDir = t.TempDir()

	const key = "abc123"
	if got := client.loadResumableUploadState(key); got != nil {
		t.Fatalf("load of missing state = %+v, want nil", got)
	}

	want := &resumableUploadState{
		UploadID:   "up-1",
		S3UploadID: "s3-1",
		AppID:      "app-1",
		Version:    "v1",
		FileName:   "a.apk",
		FilePath:   "/tmp/a.apk",
		FileSize:   100,
		FileHash:   "deadbeef",
		PartSize:   16,
	}
	client.saveResumableUploadState(key, want)

	got := client.loadResumableUploadState(key)
	if got == nil {
		t.Fatal("load after save = nil, want state")
	}
	if *got != *want {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", *got, *want)
	}

	client.deleteResumableUploadState(key)
	if got := client.loadResumableUploadState(key); got != nil {
		t.Fatalf("load after delete = %+v, want nil", got)
	}
}

func TestResumableUploadStateIgnoresRecordMissingIDs(t *testing.T) {
	client := NewClientWithBaseURL("k", "http://example.invalid")
	dir := t.TempDir()
	client.uploadStateDir = dir

	// A record missing the S3 upload id can't be resumed; load must treat it as
	// absent so the caller starts fresh instead of calling resume with junk.
	if err := os.WriteFile(filepath.Join(dir, "k.json"), []byte(`{"upload_id":"u"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := client.loadResumableUploadState("k"); got != nil {
		t.Fatalf("load of record without s3_upload_id = %+v, want nil", got)
	}

	// Corrupt JSON is likewise treated as absent, never a hard error.
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`{not json`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := client.loadResumableUploadState("bad"); got != nil {
		t.Fatalf("load of corrupt record = %+v, want nil", got)
	}
}

func TestResumableUploadStateMatches(t *testing.T) {
	state := &resumableUploadState{FileSize: 100, FileHash: "abc123"}

	if !state.matches(100, "abc123") {
		t.Fatal("matches = false for identical size+hash, want true")
	}
	if state.matches(101, "abc123") {
		t.Fatal("matches = true after size change, want false")
	}
	// The critical case: same byte size, different content (hash) — must NOT
	// match, or a rebuilt artifact would reuse stale S3 parts and corrupt.
	if state.matches(100, "def456") {
		t.Fatal("matches = true after content (hash) change, want false")
	}
}

func TestFileSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.bin")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// echo -n hello | shasum -a 256
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	got, err := fileSHA256(path)
	if err != nil {
		t.Fatalf("fileSHA256: %v", err)
	}
	if got != want {
		t.Fatalf("fileSHA256 = %q, want %q", got, want)
	}
}
