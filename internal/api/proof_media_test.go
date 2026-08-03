package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempImage(t *testing.T, name string, contents []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write temp image: %v", err)
	}
	return path
}

func TestPublishSessionProofMediaSendsMultipartAndReturnsPublicURL(t *testing.T) {
	const sessionID = "d437c539-8e4d-45cb-aad9-5f88dca32cc7"
	imageBytes := []byte("\x89PNG\r\n\x1a\nfake-body")

	var (
		seenPath        string
		seenMethod      string
		seenAuth        string
		seenFileName    string
		seenFileContent []byte
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenMethod = r.Method
		seenAuth = r.Header.Get("Authorization")

		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart form: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		part, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("read file part: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer part.Close()
		seenFileName = header.Filename
		buf := make([]byte, len(imageBytes))
		n, _ := part.Read(buf)
		seenFileContent = buf[:n]

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(SessionProofMediaResponse{
			FileName:  "shot.png",
			PublicURL: "https://backend.revyl.ai/api/v1/reports-v3/proof-media/shot.png?token=t",
		})
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	resp, err := client.PublishSessionProofMedia(
		context.Background(), sessionID, writeTempImage(t, "shot.png", imageBytes),
	)
	if err != nil {
		t.Fatalf("PublishSessionProofMedia() error = %v", err)
	}

	if seenMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", seenMethod)
	}
	if want := "/api/v1/reports-v3/sessions/" + sessionID + "/proof-media"; seenPath != want {
		t.Fatalf("unexpected path %q, want %q", seenPath, want)
	}
	if seenAuth != "Bearer test-key" {
		t.Fatalf("expected the request to be authenticated, got %q", seenAuth)
	}
	if seenFileName != "shot.png" {
		t.Fatalf("expected the file's base name to be published, got %q", seenFileName)
	}
	if string(seenFileContent) != string(imageBytes) {
		t.Fatalf("uploaded bytes did not match the file on disk")
	}
	if resp.FileName != "shot.png" {
		t.Fatalf("unexpected file name %q", resp.FileName)
	}
	if !strings.HasPrefix(resp.PublicURL, "https://backend.revyl.ai/") {
		t.Fatalf("unexpected public url %q", resp.PublicURL)
	}
}

func TestPublishSessionProofMediaSurfacesBackendRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		json.NewEncoder(w).Encode(map[string]string{
			"detail": "Proof media must be at most 15MB",
		})
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	_, err := client.PublishSessionProofMedia(
		context.Background(),
		"d437c539-8e4d-45cb-aad9-5f88dca32cc7",
		writeTempImage(t, "big.gif", []byte("GIF89a")),
	)
	if err == nil {
		t.Fatal("expected an error for a rejected upload")
	}
	if !strings.Contains(err.Error(), "15MB") {
		t.Fatalf("expected the backend's reason to survive, got %v", err)
	}
}

func TestPublishSessionProofMediaFailsBeforeUploadWhenFileIsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a missing file must not reach the backend")
	}))
	t.Cleanup(server.Close)

	client := NewClientWithBaseURL("test-key", server.URL)
	_, err := client.PublishSessionProofMedia(
		context.Background(),
		"d437c539-8e4d-45cb-aad9-5f88dca32cc7",
		filepath.Join(t.TempDir(), "absent.png"),
	)
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
}
