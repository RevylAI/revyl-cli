package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/launchvars"
)

func TestStartSessionAppliesInheritedAndExplicitLaunchVariables(t *testing.T) {
	const (
		inheritedID   = "0d60e7e3-6548-476b-91c6-c48b6d620d0e"
		explicitID    = "37159693-b91e-4e99-a0cb-e8a812387986"
		workflowRunID = "99999999-9999-4999-8999-999999999991"
	)
	var captured struct {
		LaunchEnvVarIDs []string `json:"launch_env_var_ids"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/variables/org_launch_env":
			_, _ = w.Write([]byte(
				`{"message":"ok","result":[` +
					`{"id":"` + inheritedID + `","key":"AUTH_STATE"},` +
					`{"id":"` + explicitID + `","key":"API_URL"}` +
					`]}`,
			))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/execution/start_device":
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatalf("decode start-device request: %v", err)
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

	t.Setenv(launchvars.InheritedIDsEnv, inheritedID)
	manager := NewDeviceSessionManager(
		api.NewClientWithBaseURL("runtime-key", server.URL),
		t.TempDir(),
	)
	index, _, err := manager.StartSession(
		context.Background(),
		StartSessionOptions{
			Platform:   "ios",
			LaunchVars: []string{"API_URL"},
		},
	)
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	t.Cleanup(func() { manager.StopIdleTimer(index) })

	want := []string{inheritedID, explicitID}
	if !reflect.DeepEqual(captured.LaunchEnvVarIDs, want) {
		t.Fatalf("launch_env_var_ids = %v, want %v", captured.LaunchEnvVarIDs, want)
	}
}

func TestStartSessionOptOutKeepsExplicitLaunchVariables(t *testing.T) {
	const (
		explicitID    = "37159693-b91e-4e99-a0cb-e8a812387986"
		workflowRunID = "99999999-9999-4999-8999-999999999992"
	)
	var captured struct {
		LaunchEnvVarIDs []string `json:"launch_env_var_ids"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/variables/org_launch_env":
			_, _ = w.Write([]byte(
				`{"message":"ok","result":[{"id":"` + explicitID + `","key":"API_URL"}]}`,
			))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/execution/start_device":
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatalf("decode start-device request: %v", err)
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

	t.Setenv(launchvars.InheritedIDsEnv, "not-a-uuid")
	manager := NewDeviceSessionManager(
		api.NewClientWithBaseURL("runtime-key", server.URL),
		t.TempDir(),
	)
	index, _, err := manager.StartSession(
		context.Background(),
		StartSessionOptions{
			Platform:                   "ios",
			LaunchVars:                 []string{"API_URL"},
			DisableInheritedLaunchVars: true,
		},
	)
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	t.Cleanup(func() { manager.StopIdleTimer(index) })

	if !reflect.DeepEqual(captured.LaunchEnvVarIDs, []string{explicitID}) {
		t.Fatalf(
			"launch_env_var_ids = %v, want [%s]",
			captured.LaunchEnvVarIDs,
			explicitID,
		)
	}
}

func TestStartSessionRejectsInvalidInheritedLaunchVariables(t *testing.T) {
	t.Setenv(launchvars.InheritedIDsEnv, "not-a-uuid")
	manager := NewDeviceSessionManager(nil, t.TempDir())

	_, _, err := manager.StartSession(
		context.Background(),
		StartSessionOptions{Platform: "ios"},
	)

	if err == nil || !strings.Contains(err.Error(), launchvars.InheritedIDsEnv) {
		t.Fatalf("StartSession() error = %v, want invalid defaults error", err)
	}
}
