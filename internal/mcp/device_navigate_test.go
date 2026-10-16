package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestHandleDeviceNavigateCanonicalActionAndSingleRequest(t *testing.T) {
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
					if r.Method != http.MethodPost || r.URL.Path != "/api/v1/execution/device-proxy/"+startIDWorkflowRunID+"/open_url" {
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
				manager := NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", backend.URL), "")
				manager.sessions[0] = &DeviceSession{Index: 0, SessionID: startIDSessionID, WorkflowRunID: startIDWorkflowRunID, Platform: platform}
				manager.activeIndex = 0
				t.Cleanup(func() { manager.StopIdleTimer(0) })
				server := &Server{sessionMgr: manager}
				_, output, err := server.handleDeviceNavigate(context.Background(), nil, DeviceNavigateInput{URL: url})
				if err != nil || calls != 1 || output.URL != url || output.Success != (tc.wantError == "") {
					t.Fatalf("calls=%d success=%t error=%v output=%+v", calls, output.Success, err, output)
				}
				if tc.wantError == "" && output.Error != "" || tc.wantError != "" && !strings.Contains(output.Error, tc.wantError) {
					t.Fatalf("error=%q, want %q", output.Error, tc.wantError)
				}
			})
		}
	}
}
