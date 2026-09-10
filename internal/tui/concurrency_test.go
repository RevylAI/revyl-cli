package tui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestDeviceConcurrencyRejectionRendersUpgradeAction(t *testing.T) {
	for _, response := range []struct {
		name   string
		status int
		body   string
	}{
		{"cancelled", http.StatusOK, `{"status":"cancelled","error":"Concurrency limit reached. Please upgrade your plan to continue.","retry_allowed":true,"platform":"ios"}`},
		{"API error", http.StatusTooManyRequests, `{"detail":"Concurrency limit reached"}`},
	} {
		t.Run(response.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if handleTestCLITraceFallback(w, r) {
					return
				}
				if r.URL.Path != "/api/v1/execution/start_device" {
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(response.status)
				_, _ = w.Write([]byte(response.body))
			}))
			defer server.Close()
			client := api.NewClientWithBaseURL("test-key", server.URL)
			msg := startDeviceSessionCmd(client, "ios", "", "", "", nil)().(DeviceStartedMsg)
			if msg.Err == nil || !strings.Contains(msg.Err.Error(), api.ConcurrencyUpgradeHint) {
				t.Fatalf("missing upgrade action: %v", msg.Err)
			}
			model := newHubModel("dev", false)
			model.err = msg.Err
			if view := model.renderErrorDashboard("", 100); !strings.Contains(view, api.ConcurrencyUpgradeHint) {
				t.Fatalf("TUI error view missing upgrade action:\n%s", view)
			}
		})
	}
}
