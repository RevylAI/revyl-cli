package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

type persistedDeviceSessionState struct {
	Active   int                      `json:"active"`
	NextIdx  int                      `json:"next_index"`
	Sessions []persistedDeviceSession `json:"sessions"`
}

type persistedDeviceSession struct {
	Index         int           `json:"index"`
	SessionID     string        `json:"session_id"`
	WorkflowRunID string        `json:"workflow_run_id"`
	WorkerBaseURL string        `json:"worker_base_url"`
	ViewerURL     string        `json:"viewer_url"`
	Platform      string        `json:"platform"`
	StartedAt     time.Time     `json:"started_at"`
	LastActivity  time.Time     `json:"last_activity"`
	IdleTimeout   time.Duration `json:"idle_timeout"`
}

// withWorkingDirectory switches the process working directory for one test.
func withWorkingDirectory(t *testing.T, dir string) {
	t.Helper()

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q) error = %v", dir, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWD)
	})
}

// writePersistedDeviceSession writes a minimal .revyl/device-sessions.json cache
// so CLI tests can resolve an active raw device session without live backend sync.
func writePersistedDeviceSession(t *testing.T, dir, workflowRunID string) {
	t.Helper()

	now := time.Unix(1_700_000_000, 0).UTC()
	state := persistedDeviceSessionState{
		Active:  0,
		NextIdx: 1,
		Sessions: []persistedDeviceSession{
			{
				Index:         0,
				SessionID:     "sess-1",
				WorkflowRunID: workflowRunID,
				WorkerBaseURL: "https://worker.example",
				ViewerURL:     "https://app.revyl.ai/tests/execute?workflowRunId=" + workflowRunID + "&platform=ios",
				Platform:      "ios",
				StartedAt:     now,
				LastActivity:  now,
				IdleTimeout:   5 * time.Minute,
			},
		},
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal persisted state: %v", err)
	}
	revylDir := filepath.Join(dir, ".revyl")
	if err := os.MkdirAll(revylDir, 0o755); err != nil {
		t.Fatalf("mkdir .revyl: %v", err)
	}
	if err := os.WriteFile(filepath.Join(revylDir, "device-sessions.json"), data, 0o644); err != nil {
		t.Fatalf("write device-sessions.json: %v", err)
	}
}

// newDeviceStartTestCommand builds a fresh command with just the flags that
// device start consumes, so tests can invoke the real RunE without mutating
// global Cobra state.
func newDeviceStartTestCommand(ctx context.Context) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	cmd.Flags().String("platform", "ios", "")
	cmd.Flags().Int("timeout", 300, "")
	cmd.Flags().Bool("open", false, "")
	cmd.Flags().String("app-id", "", "")
	cmd.Flags().String("build-version-id", "", "")
	cmd.Flags().String("app-url", "", "")
	cmd.Flags().String("app-link", "", "")
	cmd.Flags().Bool("json", false, "")
	cmd.Flags().Bool("dev", false, "")
	return cmd
}

// newDeviceInstallTestCommand builds a fresh command for exercising device install.
func newDeviceInstallTestCommand(ctx context.Context) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	cmd.Flags().String("app-id", "", "")
	cmd.Flags().String("build-version-id", "", "")
	cmd.Flags().String("app-url", "", "")
	cmd.Flags().String("bundle-id", "", "")
	cmd.Flags().Bool("json", false, "")
	cmd.Flags().IntP("s", "s", -1, "")
	cmd.Flags().Bool("dev", false, "")
	return cmd
}

// newDeviceDownloadFileTestCommand builds a fresh command for download-file tests.
func newDeviceDownloadFileTestCommand(ctx context.Context) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	cmd.Flags().String("url", "", "")
	cmd.Flags().String("filename", "", "")
	cmd.Flags().Bool("json", false, "")
	cmd.Flags().IntP("s", "s", -1, "")
	cmd.Flags().Bool("dev", false, "")
	return cmd
}

func TestDeviceStartCommand_RejectsMultipleArtifactFlags(t *testing.T) {
	cmd := newDeviceStartTestCommand(context.Background())
	if err := cmd.Flags().Set("app-id", "app-1"); err != nil {
		t.Fatalf("set app-id flag: %v", err)
	}
	if err := cmd.Flags().Set("app-url", "https://artifact.example/app.ipa"); err != nil {
		t.Fatalf("set app-url flag: %v", err)
	}

	err := deviceStartCmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("device start error = nil, want artifact conflict")
	}
	if got := err.Error(); got != "provide only one of --app-id, --build-version-id, or --app-url" {
		t.Fatalf("device start error = %q, want conflict guidance", got)
	}
}

func TestDeviceInstallCommand_RejectsWhitespaceAppURL(t *testing.T) {
	cmd := newDeviceInstallTestCommand(context.Background())
	if err := cmd.Flags().Set("app-url", "   "); err != nil {
		t.Fatalf("set app-url flag: %v", err)
	}

	err := deviceInstallCmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("device install error = nil, want required artifact selector")
	}
	if got := err.Error(); got != "--app-url, --build-version-id, or --app-id is required" {
		t.Fatalf("device install error = %q, want required artifact message", got)
	}
}

func TestDeviceInstallCommand_RejectsMultipleArtifactFlags(t *testing.T) {
	cmd := newDeviceInstallTestCommand(context.Background())
	if err := cmd.Flags().Set("app-id", "app-1"); err != nil {
		t.Fatalf("set app-id flag: %v", err)
	}
	if err := cmd.Flags().Set("app-url", "https://artifact.example/app.ipa"); err != nil {
		t.Fatalf("set app-url flag: %v", err)
	}

	err := deviceInstallCmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("device install error = nil, want artifact conflict")
	}
	if got := err.Error(); got != "provide only one of --app-id, --build-version-id, or --app-url" {
		t.Fatalf("device install error = %q, want conflict guidance", got)
	}
}

func TestDeviceDownloadFileCommand_RejectsWhitespaceURL(t *testing.T) {
	cmd := newDeviceDownloadFileTestCommand(context.Background())
	if err := cmd.Flags().Set("url", "   "); err != nil {
		t.Fatalf("set url flag: %v", err)
	}

	err := deviceDownloadFileCmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("device download-file error = nil, want required url")
	}
	if got := err.Error(); got != "--url is required" {
		t.Fatalf("device download-file error = %q, want required url message", got)
	}
}

func TestDeviceStartCommand_PropagatesAppURLToStartDevice(t *testing.T) {
	tmpDir := t.TempDir()
	withWorkingDirectory(t, tmpDir)
	t.Setenv("REVYL_API_KEY", "test-api-key")

	const expectedAppURL = "https://artifact.example/trimmed-app.ipa"
	const workflowRunID = "wf-start-1"

	var capturedStartReq struct {
		AppURL string `json:"app_url"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/execution/start_device":
			if err := json.NewDecoder(r.Body).Decode(&capturedStartReq); err != nil {
				t.Fatalf("decode start_device request: %v", err)
			}
			_, _ = w.Write([]byte(`{"workflow_run_id":"` + workflowRunID + `"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/execution/streaming/worker-connection/"+workflowRunID:
			_, _ = w.Write([]byte(`{"status":"ready","workflow_run_id":"` + workflowRunID + `","worker_ws_url":"ws://` + r.Host + `/ws/stream?token=test"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/execution/device-proxy/"+workflowRunID+"/health":
			_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	cmd := newDeviceStartTestCommand(context.Background())
	if err := cmd.Flags().Set("platform", "ios"); err != nil {
		t.Fatalf("set platform flag: %v", err)
	}
	if err := cmd.Flags().Set("app-url", "  "+expectedAppURL+"  "); err != nil {
		t.Fatalf("set app-url flag: %v", err)
	}
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatalf("set json flag: %v", err)
	}

	if err := deviceStartCmd.RunE(cmd, nil); err != nil {
		t.Fatalf("device start returned error: %v", err)
	}
	if capturedStartReq.AppURL != expectedAppURL {
		t.Fatalf("start_device app_url = %q, want %q", capturedStartReq.AppURL, expectedAppURL)
	}
}

func TestDeviceInstallCommand_FailsWhenWorkerReportsFailure(t *testing.T) {
	tmpDir := t.TempDir()
	withWorkingDirectory(t, tmpDir)
	writePersistedDeviceSession(t, tmpDir, "wf-install-1")
	t.Setenv("REVYL_API_KEY", "test-api-key")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/execution/device-proxy/wf-install-1/install":
			_, _ = w.Write([]byte(`{"success":false,"action":"install","error":"install failed"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	cmd := newDeviceInstallTestCommand(context.Background())
	if err := cmd.Flags().Set("app-url", "https://artifact.example/app.ipa"); err != nil {
		t.Fatalf("set app-url flag: %v", err)
	}

	err := deviceInstallCmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("device install error = nil, want worker failure")
	}
	if got := err.Error(); !strings.Contains(got, "install failed") {
		t.Fatalf("device install error = %q, want worker failure", got)
	}
}

func TestDeviceDownloadFileCommand_FailsWhenWorkerReportsFailure(t *testing.T) {
	tmpDir := t.TempDir()
	withWorkingDirectory(t, tmpDir)
	writePersistedDeviceSession(t, tmpDir, "wf-download-1")
	t.Setenv("REVYL_API_KEY", "test-api-key")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/execution/device-proxy/wf-download-1/download_file":
			_, _ = w.Write([]byte(`{"success":false,"action":"download_file","error":"download failed"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	cmd := newDeviceDownloadFileTestCommand(context.Background())
	if err := cmd.Flags().Set("url", "https://example.com/file.pdf"); err != nil {
		t.Fatalf("set url flag: %v", err)
	}

	err := deviceDownloadFileCmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("device download-file error = nil, want worker failure")
	}
	if got := err.Error(); !strings.Contains(got, "download failed") {
		t.Fatalf("device download-file error = %q, want worker failure", got)
	}
}
