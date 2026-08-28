package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/revyl/cli/internal/launchvars"
)

func TestQueueWorkflowExecutionMergesTypedInheritedLaunchConfigurations(t *testing.T) {
	const (
		inheritedEnvID  = "0d60e7e3-6548-476b-91c6-c48b6d620d0e"
		inheritedArgsID = "11111111-1111-4111-8111-111111111111"
		explicitEnvID   = "37159693-b91e-4e99-a0cb-e8a812387986"
		explicitArgsID  = "22222222-2222-4222-8222-222222222222"
	)
	var captured struct {
		LaunchEnvVarIDs []string          `json:"launch_env_var_ids"`
		LaunchEnvVars   map[string]string `json:"launch_env_vars"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/variables/org_launch_env":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(
				`{"message":"ok","result":[` +
					`{"id":"` + inheritedEnvID + `","key":"AUTH_STATE"},` +
					`{"id":"` + inheritedArgsID + `","key":"AuthArgs","kind":"ios_arguments"},` +
					`{"id":"` + explicitEnvID + `","key":"API_URL","kind":"key_value"},` +
					`{"id":"` + explicitArgsID + `","key":"RouteArgs","kind":"ios_arguments"}` +
					`]}`,
			))
		case "/api/v1/workflow-executions":
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
	t.Setenv(launchvars.InheritedIDsEnv, inheritedEnvID+","+inheritedArgsID)

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
		[]string{"RouteArgs"},
		false,
		map[string]string{"API_URL": "https://inline.example"},
		[]string{"--uitesting"},
	)
	if err != nil {
		t.Fatalf("queueWorkflowExecution() error = %v", err)
	}
	want := []string{inheritedEnvID, inheritedArgsID, explicitEnvID, explicitArgsID}
	if !reflect.DeepEqual(captured.LaunchEnvVarIDs, want) {
		t.Fatalf("launch_env_var_ids = %v, want %v", captured.LaunchEnvVarIDs, want)
	}
	if captured.LaunchEnvVars["API_URL"] != "https://inline.example" {
		t.Fatalf("launch_env_vars = %v", captured.LaunchEnvVars)
	}
}
