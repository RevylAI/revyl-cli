package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestStartSessionFailureIncludesSessionIDAndReason(t *testing.T) {
	const failureReason = "WebSessionFailure: Worker tunnel exited before ready"
	for _, tc := range []struct {
		name       string
		reason     string
		readStatus int
		want       string
	}{
		{name: "backend reason", reason: failureReason, want: failureReason},
		{name: "missing backend reason", want: "Session failed"},
		{name: "detail unavailable", readStatus: http.StatusForbidden, want: "Session failed"},
		{name: "redacted reason", reason: failureReason + " https://viewer.example.test/session?token=fake-viewer-secret wss://worker.example.test/?token=fake-worker-secret token=fake-secret rk_0123456789abcdef", want: failureReason},
		{name: "bounded reason", reason: failureReason + strings.Repeat("x", 1000), want: failureReason},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cancelCalls, detailCalls := 0, 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/telemetry/cli-traces":
					w.WriteHeader(http.StatusNotFound)
				case "/api/v1/execution/start_device":
					_, _ = w.Write([]byte(`{"workflow_run_id":"` + startIDWorkflowRunID + `","session_id":"` + startIDSessionID + `"}`))
				case "/api/v1/execution/streaming/worker-connection/" + startIDWorkflowRunID:
					_, _ = w.Write([]byte(`{"status":"failed","message":"Session failed"}`))
				case "/api/v1/execution/device-sessions/" + startIDSessionID:
					detailCalls++
					if tc.readStatus != 0 {
						w.WriteHeader(tc.readStatus)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"id": startIDSessionID, "status": "failed", "error_message": tc.reason})
				case "/api/v1/execution/device/status/cancel/" + startIDWorkflowRunID:
					cancelCalls++
					_, _ = w.Write([]byte(`{"success":true,"session_settled":true,"device_released":true}`))
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer backend.Close()
			manager := NewDeviceSessionManager(api.NewClientWithBaseURL("fixture-key", backend.URL), t.TempDir())
			_, _, err := manager.StartSession(context.Background(), StartSessionOptions{Platform: "ios"})
			if err == nil {
				t.Fatal("failed start returned success")
			}
			message := err.Error()
			if !strings.Contains(message, startIDSessionID) || !strings.Contains(message, tc.want) || !strings.Contains(message, "Try again") {
				t.Fatalf("start error does not identify the session, reason and next action: %q", message)
			}
			for _, forbidden := range []string{"https://", "wss://", "fake-viewer-secret", "fake-worker-secret", "fake-secret", "rk_0123456789abcdef"} {
				if strings.Contains(message, forbidden) {
					t.Fatalf("start error leaked %q", forbidden)
				}
			}
			if len(message) > 500 || detailCalls != 1 || cancelCalls != 1 || manager.SessionCount() != 0 {
				t.Fatalf("unexpected failed start: length=%d details=%d cancellations=%d sessions=%d", len(message), detailCalls, cancelCalls, manager.SessionCount())
			}
		})
	}
}
