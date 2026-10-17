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

func intPtr(value int) *int { return &value }

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
			statusCode              *int
		}{
			{name: "success", action: expectedAction, status: 200, success: true, statusCode: intPtr(200)},
			{name: "HTTP error page", action: expectedAction, status: 200, success: true, statusCode: intPtr(404)},
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
					var requestBody map[string]string
					if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil || len(requestBody) != 1 || requestBody["url"] != url {
						t.Error("navigation changed its request body")
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					body := fmt.Sprintf(`{"success":%t,"action":%q,"error":"navigation rejected"`, tc.success, tc.action)
					if tc.statusCode != nil && platform == "web" {
						body += fmt.Sprintf(`,"status_code":%d`, *tc.statusCode)
					}
					fmt.Fprintln(w, body+"}")
				}))
				t.Cleanup(backend.Close)
				manager := mcppkg.NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", backend.URL), "")
				session := &mcppkg.DeviceSession{Index: mcppkg.UnattachedSessionIndex, SessionID: flowSessionID, WorkflowRunID: flowWorkflowRunID, Platform: platform}
				statusCode, err := navigateDeviceResult(context.Background(), manager, session, url)
				if calls != 1 {
					t.Fatalf("navigation submitted %d requests, want one", calls)
				}
				if tc.wantError == "" {
					if err != nil {
						t.Fatal(err)
					}
					if platform == "web" && statusCode == nil {
						t.Fatal("web navigation did not return status_code")
					}
					if platform == "web" && tc.name == "HTTP error page" && *statusCode != 404 {
						t.Fatalf("web navigation status_code=%d, want 404", *statusCode)
					}
					if platform != "web" && statusCode != nil {
						t.Fatalf("native navigation returned status_code=%d", *statusCode)
					}
				} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error=%v, want %q", err, tc.wantError)
				}
			})
		}
	}
}
