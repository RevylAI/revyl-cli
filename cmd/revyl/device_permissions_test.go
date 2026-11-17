package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestDevicePermissionsSetRefusesIOSMicrophoneDenyBeforeTheWorker(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv(sessionIDEnvVar, "")
	workerCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-1","email":"test@example.com"}`))
		case r.URL.Path == "/api/v1/execution/device-sessions/active":
			_, _ = w.Write([]byte(`{"org_id":"org-1","sessions":[{"id":"` + flowSessionID + `","org_id":"org-1","platform":"ios","status":"running","workflow_run_id":"` + flowWorkflowRunID + `","user_email":"test@example.com","created_at":"2026-02-19T00:00:00Z","started_at":"2026-02-19T00:00:00Z"}]}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/execution/streaming/worker-connection/"):
			runID := strings.TrimPrefix(r.URL.Path, "/api/v1/execution/streaming/worker-connection/")
			_, _ = w.Write([]byte(`{"status":"ready","workflow_run_id":"` + runID + `","worker_ws_url":"ws://` + r.Host + `/ws/stream?token=test"}`))
		case strings.HasSuffix(r.URL.Path, "/health"):
			_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
		case strings.HasSuffix(r.URL.Path, "/set_permissions"):
			workerCalls++
			_, _ = w.Write([]byte(`{"success":true,"action":"set_permissions","latency_ms":1}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	cmd := &cobra.Command{Use: "set"}
	cmd.SetContext(context.Background())
	cmd.Flags().String("bundle-id", "", "")
	cmd.Flags().Bool("json", false, "")
	registerSessionTargetFlags(cmd)
	if err := cmd.ParseFlags([]string{"--bundle-id", "com.example.app"}); err != nil {
		t.Fatal(err)
	}

	_, _, err, terminal := runStopWithAnalytics(t, cmd, devicePermissionsSetCmd.RunE, []string{"location=allow", "microphone=deny"})

	if err == nil || !strings.Contains(err.Error(), "audio injection grants the iOS microphone on every app launch") {
		t.Fatalf("error = %v, want the microphone refusal", err)
	}
	if workerCalls != 0 {
		t.Fatalf("worker received %d set_permissions calls, want none", workerCalls)
	}
	if terminal["domain"] != "device_permissions_set" || terminal["domain_status"] != "failed" {
		t.Fatalf("terminal analytics = %+v, want device_permissions_set failed", terminal)
	}
}
