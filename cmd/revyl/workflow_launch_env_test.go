package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/revyl/cli/internal/launchvars"
)

func TestQueueWorkflowExecutionMergesInheritedAndExplicitLaunchVars(t *testing.T) {
	const launchVarID = "0d60e7e3-6548-476b-91c6-c48b6d620d0e"
	var captured struct {
		LaunchEnvVarIDs []string          `json:"launch_env_var_ids"`
		LaunchEnvVars   map[string]string `json:"launch_env_vars"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/variables/org_launch_env":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message":"ok","result":[{"id":"` + launchVarID + `","key":"API_URL","value":"https://stored.example"}]}`))
		case "/api/v1/execution/api/execute_workflow_id_async":
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"task_id":"task-123"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_APP_URL", "https://app.example")
	t.Setenv(launchvars.InheritedIDsEnv, launchVarID)

	_, err := queueWorkflowExecution(
		context.Background(),
		"token",
		"workflow-123",
		"Smoke Tests",
		1,
		false,
		"",
		"",
		"",
		"",
		false,
		0,
		0,
		nil,
		[]string{"API_URL"},
		false,
		map[string]string{"API_URL": "https://inline.example"},
	)
	if err != nil {
		t.Fatalf("queueWorkflowExecution() error = %v", err)
	}
	if len(captured.LaunchEnvVarIDs) != 1 || captured.LaunchEnvVarIDs[0] != launchVarID {
		t.Fatalf("launch_env_var_ids = %v, want [%s]", captured.LaunchEnvVarIDs, launchVarID)
	}
	if captured.LaunchEnvVars["API_URL"] != "https://inline.example" {
		t.Fatalf("launch_env_vars = %v", captured.LaunchEnvVars)
	}
}
