package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/launchvars"
)

func TestStartSessionInsideComputerSendsComputerSessionID(t *testing.T) {
	const (
		computerSessionID = "cccccccc-cccc-4ccc-8ccc-cccccccccc01"
		buildVersionID    = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbb01"
		workflowRunID     = "99999999-9999-4999-8999-999999999901"
		sessionID         = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaa01"
	)
	for _, platform := range []string{"ios", "android"} {
		t.Run(platform, func(t *testing.T) {
			var captured struct {
				Platform          string   `json:"platform"`
				AppID             string   `json:"app_id"`
				BuildID           string   `json:"build_id"`
				DeviceModel       string   `json:"device_model"`
				ComputerSessionID string   `json:"computer_session_id"`
				AppURL            string   `json:"app_url"`
				AppPackage        string   `json:"app_package"`
				LaunchEnvVarIDs   []string `json:"launch_env_var_ids"`
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/builds/"+buildVersionID:
					_, _ = w.Write([]byte(`{"id":"` + buildVersionID + `","app_id":"app-123","version":"1","download_url":"https://example.test/app.zip","package_name":"com.example.app"}`))
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/execution/start_device":
					if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
						t.Fatalf("decode start_device request: %v", err)
					}
					_, _ = w.Write([]byte(`{"workflow_run_id":"` + workflowRunID + `","session_id":"` + sessionID + `"}`))
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/execution/streaming/worker-connection/"+workflowRunID:
					_, _ = w.Write([]byte(`{"status":"ready","workflow_run_id":"` + workflowRunID + `","worker_ws_url":"ws://` + r.Host + `/ws/stream?token=test"}`))
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/execution/device-proxy/"+workflowRunID+"/health":
					_, _ = w.Write([]byte(`{"status":"ok","device_connected":true}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			t.Setenv(launchvars.InheritedIDsEnv, "0d60e7e3-6548-476b-91c6-c48b6d620d0e")
			mgr := &DeviceSessionManager{
				apiClient:   api.NewClientWithBaseURL("test-key", server.URL),
				sessions:    make(map[int]*DeviceSession),
				idleTimers:  make(map[int]*time.Timer),
				activeIndex: -1,
			}
			index, _, err := mgr.StartSession(context.Background(), StartSessionOptions{
				Platform:          platform,
				BuildVersionID:    buildVersionID,
				ComputerSessionID: " " + computerSessionID + " ",
			})
			if err != nil {
				t.Fatalf("StartSession() error = %v", err)
			}
			t.Cleanup(func() { mgr.StopIdleTimer(index) })

			if captured.Platform != platform || captured.AppID != "app-123" || captured.BuildID != buildVersionID || captured.DeviceModel != "" || captured.ComputerSessionID != computerSessionID || captured.AppURL != "" || captured.AppPackage != "" || captured.LaunchEnvVarIDs != nil {
				t.Fatalf("start_device request = %+v", captured)
			}
		})
	}
}

func TestStartSessionInsideComputerRefusesBeforeAPIWork(t *testing.T) {
	const computerSessionID = "cccccccc-cccc-4ccc-8ccc-cccccccccc02"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected API call %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer server.Close()

	for _, tc := range []struct {
		name string
		opts StartSessionOptions
	}{
		{"not a UUID", StartSessionOptions{Platform: "ios", AppID: "app-123", ComputerSessionID: "computer-1"}},
		{"unsupported platform", StartSessionOptions{Platform: "linux", AppID: "app-123", ComputerSessionID: computerSessionID}},
		{"android device model", StartSessionOptions{Platform: "android", AppID: "app-123", DeviceModel: "Pixel 7", ComputerSessionID: computerSessionID}},
		{"android app URL", StartSessionOptions{Platform: "android", AppURL: "https://example.test/app.apk", ComputerSessionID: computerSessionID}},
		{"device model", StartSessionOptions{Platform: "ios", AppID: "app-123", DeviceModel: "iPhone 16", OsVersion: "iOS 18.5", ComputerSessionID: computerSessionID}},
		{"device runner", StartSessionOptions{Platform: "ios", AppID: "app-123", DeviceRunnerID: "runner-1", ComputerSessionID: computerSessionID}},
		{"app URL", StartSessionOptions{Platform: "ios", AppURL: "https://example.test/app.zip", ComputerSessionID: computerSessionID}},
		{"test", StartSessionOptions{Platform: "ios", AppID: "app-123", TestID: "test-1", ComputerSessionID: computerSessionID}},
		{"no build", StartSessionOptions{Platform: "ios", ComputerSessionID: computerSessionID, DisableInheritedLaunchVars: true}},
		{"launch variable", StartSessionOptions{Platform: "ios", AppID: "app-123", LaunchVars: []string{"API_URL"}, ComputerSessionID: computerSessionID}},
		{"launch environment", StartSessionOptions{Platform: "ios", AppID: "app-123", LaunchEnv: map[string]string{"A": "1"}, ComputerSessionID: computerSessionID}},
		{"launch argument", StartSessionOptions{Platform: "ios", AppID: "app-123", LaunchArguments: []string{"-flag"}, ComputerSessionID: computerSessionID}},
		{"run option", StartSessionOptions{Platform: "ios", AppID: "app-123", InitialLocale: "fr_FR", ComputerSessionID: computerSessionID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &DeviceSessionManager{
				apiClient:   api.NewClientWithBaseURL("test-key", server.URL),
				sessions:    make(map[int]*DeviceSession),
				idleTimers:  make(map[int]*time.Timer),
				activeIndex: -1,
			}
			if _, _, err := mgr.StartSession(context.Background(), tc.opts); err == nil {
				t.Fatal("StartSession() succeeded, want refusal")
			}
		})
	}
}
