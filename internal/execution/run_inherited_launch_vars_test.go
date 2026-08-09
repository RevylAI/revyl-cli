package execution

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/launchvars"
)

func TestRunTestAppliesTypedInheritedAndExplicitLaunchConfigurations(t *testing.T) {
	const (
		inheritedEnvID  = "0d60e7e3-6548-476b-91c6-c48b6d620d0e"
		inheritedArgsID = "11111111-1111-4111-8111-111111111111"
		explicitEnvID   = "37159693-b91e-4e99-a0cb-e8a812387986"
		explicitArgsID  = "22222222-2222-4222-8222-222222222222"
	)
	var captured struct {
		LaunchEnvVarIDs []string `json:"launch_env_var_ids"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/variables/org_launch_env":
			_, _ = w.Write([]byte(
				`{"message":"ok","result":[` +
					`{"id":"` + inheritedEnvID + `","key":"AUTH_STATE"},` +
					`{"id":"` + inheritedArgsID + `","key":"AuthArgs","kind":"ios_arguments"},` +
					`{"id":"` + explicitEnvID + `","key":"API_URL","kind":"key_value"},` +
					`{"id":"` + explicitArgsID + `","key":"RouteArgs","kind":"ios_arguments"}` +
					`]}`,
			))
		case "/api/v1/execution/api/execute_test_id_async":
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatalf("decode execute-test request: %v", err)
			}
			_, _ = w.Write([]byte(`{"id":"execution-1","task_id":"task-1","status":"queued"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_APP_URL", "https://app.example")
	t.Setenv(launchvars.InheritedIDsEnv, inheritedEnvID+","+inheritedArgsID)

	_, err := RunTest(
		context.Background(),
		"runtime-key",
		nil,
		RunTestParams{
			TestNameOrID:  "test-id",
			LaunchVars:    []string{"API_URL"},
			LaunchArgSets: []string{"RouteArgs"},
			NoWait:        true,
		},
	)
	if err != nil {
		t.Fatalf("RunTest() error = %v", err)
	}
	want := []string{inheritedEnvID, inheritedArgsID, explicitEnvID, explicitArgsID}
	if !reflect.DeepEqual(captured.LaunchEnvVarIDs, want) {
		t.Fatalf("launch_env_var_ids = %v, want %v", captured.LaunchEnvVarIDs, want)
	}
}

func TestRunTestOptOutKeepsExplicitLaunchVariables(t *testing.T) {
	const explicitID = "37159693-b91e-4e99-a0cb-e8a812387986"
	var captured struct {
		LaunchEnvVarIDs []string `json:"launch_env_var_ids"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/variables/org_launch_env":
			_, _ = w.Write([]byte(
				`{"message":"ok","result":[{"id":"` + explicitID + `","key":"API_URL"}]}`,
			))
		case "/api/v1/execution/api/execute_test_id_async":
			if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
				t.Fatalf("decode execute-test request: %v", err)
			}
			_, _ = w.Write([]byte(`{"id":"execution-1","task_id":"task-1","status":"queued"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_APP_URL", "https://app.example")
	t.Setenv(launchvars.InheritedIDsEnv, "not-a-uuid")

	_, err := RunTest(
		context.Background(),
		"runtime-key",
		nil,
		RunTestParams{
			TestNameOrID:               "test-id",
			LaunchVars:                 []string{"API_URL"},
			DisableInheritedLaunchVars: true,
			NoWait:                     true,
		},
	)
	if err != nil {
		t.Fatalf("RunTest() error = %v", err)
	}
	if !reflect.DeepEqual(captured.LaunchEnvVarIDs, []string{explicitID}) {
		t.Fatalf(
			"launch_env_var_ids = %v, want [%s]",
			captured.LaunchEnvVarIDs,
			explicitID,
		)
	}
}

func TestRunTestRejectsInvalidInheritedLaunchVariables(t *testing.T) {
	t.Setenv(launchvars.InheritedIDsEnv, "not-a-uuid")

	_, err := RunTest(
		context.Background(),
		"runtime-key",
		nil,
		RunTestParams{TestNameOrID: "test-id"},
	)

	if err == nil || !strings.Contains(err.Error(), launchvars.InheritedIDsEnv) {
		t.Fatalf("RunTest() error = %v, want invalid defaults error", err)
	}
}

func TestRunWorkflowRejectsInvalidInheritedLaunchVariables(t *testing.T) {
	t.Setenv(launchvars.InheritedIDsEnv, "not-a-uuid")

	_, err := RunWorkflow(
		context.Background(),
		"runtime-key",
		nil,
		RunWorkflowParams{WorkflowNameOrID: "workflow-id"},
	)

	if err == nil || !strings.Contains(err.Error(), launchvars.InheritedIDsEnv) {
		t.Fatalf("RunWorkflow() error = %v, want invalid defaults error", err)
	}
}
