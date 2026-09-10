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

func TestStartDeviceSessionConcurrencyRejectionIncludesUpgradeAction(t *testing.T) {
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
				if r.URL.Path == "/api/v1/telemetry/cli-traces" {
					w.WriteHeader(http.StatusNotFound)
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
			mgr := NewDeviceSessionManager(client, t.TempDir())
			srv := &Server{sessionMgr: mgr}
			_, output, err := srv.handleStartDeviceSession(context.Background(), nil, StartDeviceSessionInput{
				Platform: "ios", NoOpen: true, DisableInheritedLaunchVars: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if output.Success || !strings.Contains(output.Error, api.ConcurrencyUpgradeHint) {
				t.Fatalf("missing structured failure and upgrade action: %+v", output)
			}
			encoded, err := json.Marshal(output)
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]interface{}
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if len(decoded) != 3 || decoded["success"] != false || decoded["error"] != output.Error || decoded["session_index"] != float64(0) {
				t.Fatalf("MCP rejection contract changed: %s", encoded)
			}
		})
	}
}
