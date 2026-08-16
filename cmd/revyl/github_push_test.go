package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func githubPushServer(
	t *testing.T,
	status string,
	captured *api.PushPRReviewConfigRequest,
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/scm/github/configs/push" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request: %v", err)
		}
		if err := json.Unmarshal(body, captured); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(api.PushPRReviewConfigResponse{
			State: api.PRReviewConfigFileState{Status: status},
		}); err != nil {
			t.Fatalf("failed to encode response: %v", err)
		}
	}))
}

func TestPushPRReviewConfigUploadsExistingFileWithoutScaffold(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".revyl", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	original := "project:\n  name: demo\n"
	if err := os.WriteFile(configPath, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var captured api.PushPRReviewConfigRequest
	server := githubPushServer(t, "none", &captured)
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	if err := pushPRReviewConfig(
		context.Background(),
		client,
		configPath,
		"acme/mobile",
	); err != nil {
		t.Fatalf("pushPRReviewConfig() error = %v", err)
	}

	if captured.Content != original {
		t.Fatalf("uploaded content = %q, want original file %q", captured.Content, original)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(after) != original {
		t.Fatalf("local file was rewritten: %q", after)
	}
}

func TestPushPRReviewConfigMissingFileUploadsEmpty(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".revyl", "config.yaml")

	var captured api.PushPRReviewConfigRequest
	server := githubPushServer(t, "none", &captured)
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	if err := pushPRReviewConfig(
		context.Background(),
		client,
		configPath,
		"acme/mobile",
	); err != nil {
		t.Fatalf("pushPRReviewConfig() error = %v", err)
	}

	if captured.Content != "" {
		t.Fatalf("uploaded content = %q, want empty string", captured.Content)
	}
}
