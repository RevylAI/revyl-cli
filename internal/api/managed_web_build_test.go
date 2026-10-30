package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUploadManagedWebBuildScalesUploadTimeoutWithBundleSize(t *testing.T) {
	const versionID = "4a7cb2a7-1d6c-4f39-8f55-0f3b2b8f2b11"
	var uploaded []byte
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/builds/upload-url"):
			json.NewEncoder(w).Encode(map[string]string{"version_id": versionID, "version": "v1", "upload_url": server.URL + "/bundle", "content_type": "application/zstd"})
		case r.Method == http.MethodPut && r.URL.Path == "/bundle":
			time.Sleep(50 * time.Millisecond)
			uploaded, _ = io.ReadAll(r.Body)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/complete-upload"):
			json.NewEncoder(w).Encode(map[string]string{"version_id": versionID, "version": "v1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	archive := filepath.Join(t.TempDir(), "bundle.tar.zst")
	if err := os.WriteFile(archive, []byte("bundle"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := NewClientWithBaseURL("test", server.URL)
	client.uploadClient = &http.Client{Timeout: 10 * time.Millisecond}
	result, err := client.UploadManagedWebBuild(context.Background(), &UploadBuildRequest{AppID: "app", Version: "v1", FilePath: archive}, ManagedWebBuildMetadata{Artifact: "tarball", SHA256: strings.Repeat("0", 64), SizeBytes: 6})
	if err != nil {
		t.Fatalf("upload with a size-scaled timeout failed: %v", err)
	}
	if result.VersionID != versionID || string(uploaded) != "bundle" {
		t.Fatalf("result = %+v, uploaded = %q", result, uploaded)
	}
	if partUploadAttemptTimeout(1<<30) <= time.Hour {
		t.Fatalf("1 GiB Web upload timeout = %s", partUploadAttemptTimeout(1<<30))
	}
}
