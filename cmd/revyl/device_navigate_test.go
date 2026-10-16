package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	mcppkg "github.com/revyl/cli/internal/mcp"
)

func TestNavigateDeviceCanonicalActionAndSingleRequest(t *testing.T) {
	const url = "https://example.com/login"
	for _, platform := range []string{"web", "ios", "android"} {
		expectedAction, wrongAction := "open_url", "navigate"
		if platform == "web" {
			expectedAction, wrongAction = "navigate", "open_url"
		}
		for _, tc := range []struct {
			name, action, wantError string
			status                  int
			success                 bool
		}{
			{name: "success", action: expectedAction, status: 200, success: true},
			{name: "worker failure", action: expectedAction, status: 200, wantError: "navigation rejected"},
			{name: "wrong action", action: wrongAction, status: 200, success: true, wantError: "expected"},
			{name: "unrelated action", action: "tap", status: 200, success: true, wantError: "expected"},
			{name: "HTTP failure", action: expectedAction, status: 400, wantError: "400"},
			{name: "unavailable", action: expectedAction, status: 503, wantError: "503"},
		} {
			t.Run(platform+"/"+tc.name, func(t *testing.T) {
				calls := 0
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						http.NotFound(w, r)
						return
					}
					calls++
					if r.Method != http.MethodPost || r.URL.Path != "/api/v1/execution/device-proxy/"+flowWorkflowRunID+"/open_url" {
						t.Error("navigation changed its transport route")
					}
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 || body["url"] != url {
						t.Error("navigation changed its request body")
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					fmt.Fprintf(w, `{"success":%t,"action":%q,"error":"navigation rejected"}`, tc.success, tc.action)
				}))
				t.Cleanup(backend.Close)
				manager := mcppkg.NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", backend.URL), "")
				session := &mcppkg.DeviceSession{Index: mcppkg.UnattachedSessionIndex, SessionID: flowSessionID, WorkflowRunID: flowWorkflowRunID, Platform: platform}
				err := navigateDevice(context.Background(), manager, session, url)
				if calls != 1 {
					t.Fatalf("navigation submitted %d requests, want one", calls)
				}
				if tc.wantError == "" {
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error=%v, want %q", err, tc.wantError)
				}
			})
		}
	}
}
