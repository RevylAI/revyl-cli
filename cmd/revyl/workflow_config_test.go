package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
)

const workflowConfigTestID = "wf-uuid-001"

// storedWorkflowRunConfigJSON mirrors a workflow whose run config override
// is on and whose run_config carries TestRunConfig keys the CLI does not model.
const storedWorkflowRunConfigJSON = `{
	"id": "wf-uuid-001",
	"name": "smoke-tests",
	"override_run_config": true,
	"run_config": {
		"llm_config": {"model": "configured-model"},
		"execution_mode": {"retries": 1, "enable_reflection": false, "initial_orientation": "landscape"},
		"fail_fast": true
	}
}`

type workflowConfigServer struct {
	*httptest.Server
	getWorkflowStatus int
	putBodies         [][]byte
	requestCount      int
}

func newWorkflowConfigServer(t *testing.T, workflowJSON string) *workflowConfigServer {
	t.Helper()
	server := &workflowConfigServer{getWorkflowStatus: http.StatusOK}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.requestCount++
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/workflows/get_workflow_info":
			w.WriteHeader(server.getWorkflowStatus)
			if server.getWorkflowStatus != http.StatusOK {
				_, _ = w.Write([]byte(`{"detail":"temporarily unavailable"}`))
				return
			}
			_, _ = w.Write([]byte(workflowJSON))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/workflows/update_run_config/"+workflowConfigTestID:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read update_run_config body: %v", err)
			}
			server.putBodies = append(server.putBodies, body)
			_, _ = w.Write([]byte(`{"message":"Workflow run configuration updated successfully","data":{}}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	withWfSettingsMockClient(t, server.Server, workflowConfigTestID)
	return server
}

func newWorkflowConfigSetCommand(t *testing.T, flagArgs ...string) *cobra.Command {
	t.Helper()
	leaf := newLeafCommand("set", runWorkflowConfigSet)
	leaf.Flags().Int("retries", 0, "")
	leaf.Flags().Int("parallel", 0, "")
	if err := leaf.ParseFlags(flagArgs); err != nil {
		t.Fatalf("parse flags %v: %v", flagArgs, err)
	}
	return leaf
}

func TestWorkflowConfigSetSendsBackendRequestSchema(t *testing.T) {
	server := newWorkflowConfigServer(t, storedWorkflowRunConfigJSON)
	cmd := newWorkflowConfigSetCommand(t, "--retries", "3")

	if err := cmd.RunE(cmd, []string{"smoke-tests"}); err != nil {
		t.Fatalf("workflow config set: %v", err)
	}

	if len(server.putBodies) != 1 {
		t.Fatalf("update_run_config requests = %d, want 1", len(server.putBodies))
	}
	assertRequestBodyMatchesOpenAPI(t, http.MethodPut, "/api/v1/workflows/update_run_config/{workflow_id}", server.putBodies[0])
}

func TestWorkflowConfigSetPreservesStoredRunConfigAndOverride(t *testing.T) {
	server := newWorkflowConfigServer(t, storedWorkflowRunConfigJSON)
	cmd := newWorkflowConfigSetCommand(t, "--retries", "3")

	if err := cmd.RunE(cmd, []string{"smoke-tests"}); err != nil {
		t.Fatalf("workflow config set: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(server.putBodies[0], &got); err != nil {
		t.Fatalf("decode update_run_config body: %v", err)
	}
	want := map[string]interface{}{
		"override_run_config": true,
		"run_config": map[string]interface{}{
			"llm_config": map[string]interface{}{"model": "configured-model"},
			"execution_mode": map[string]interface{}{
				"retries":             float64(3),
				"enable_reflection":   false,
				"initial_orientation": "landscape",
			},
			"fail_fast": true,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("update_run_config body = %#v, want %#v", got, want)
	}
}

func TestWorkflowConfigSetCreatesRunConfigWhenUnset(t *testing.T) {
	server := newWorkflowConfigServer(t, `{"id":"wf-uuid-001","name":"smoke-tests","run_config":null,"override_run_config":false}`)
	cmd := newWorkflowConfigSetCommand(t, "--retries", "2", "--json")

	stdout := captureStdout(t, func() {
		if err := cmd.RunE(cmd, []string{"smoke-tests"}); err != nil {
			t.Fatalf("workflow config set: %v", err)
		}
	})

	var body map[string]interface{}
	if err := json.Unmarshal(server.putBodies[0], &body); err != nil {
		t.Fatalf("decode update_run_config body: %v", err)
	}
	wantBody := map[string]interface{}{
		"override_run_config": false,
		"run_config": map[string]interface{}{
			"execution_mode": map[string]interface{}{"retries": float64(2)},
		},
	}
	if !reflect.DeepEqual(body, wantBody) {
		t.Fatalf("update_run_config body = %#v, want %#v", body, wantBody)
	}

	output := parseJSON(t, stdout)
	if output["retries"] != float64(2) || output["override_run_config"] != false {
		t.Fatalf("config set --json = %#v, want retries 2 and override_run_config false", output)
	}
}

func TestWorkflowConfigShowFailuresDescribeOnlyTheRead(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{{http.StatusServiceUnavailable, "cannot read workflow run config"}, {http.StatusNotFound, "revyl workflow list"}} {
		server := newWorkflowConfigServer(t, storedWorkflowRunConfigJSON)
		server.getWorkflowStatus = tc.status
		cmd := newLeafCommand("show", runWorkflowConfigShow)

		err := cmd.RunE(cmd, []string{"smoke-tests"})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("status=%d error=%v, want %q", tc.status, err, tc.want)
		}
		if strings.Contains(err.Error(), "nothing was changed") || strings.Contains(err.Error(), "--retries") {
			t.Fatalf("status=%d: read-only show described a write: %v", tc.status, err)
		}
	}
}

func TestWorkflowConfigSetStopsWhenCurrentConfigCannotBeRead(t *testing.T) {
	server := newWorkflowConfigServer(t, storedWorkflowRunConfigJSON)
	server.getWorkflowStatus = http.StatusServiceUnavailable
	cmd := newWorkflowConfigSetCommand(t, "--retries", "2")

	if err := cmd.RunE(cmd, []string{"smoke-tests"}); err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("expected an actionable read failure, got %v", err)
	}
	if server.requestCount != api.DefaultMaxRetries+1 {
		t.Fatalf("read attempts=%d, want bounded retry", server.requestCount)
	}
	if len(server.putBodies) != 0 {
		t.Fatalf("update_run_config requests = %d, want 0 so stored settings are never overwritten blind", len(server.putBodies))
	}
}

func TestWorkflowConfigSetParallelIsHiddenAndAccepted(t *testing.T) {
	if flag := workflowConfigSetCmd.Flags().Lookup("parallel"); flag == nil || !flag.Hidden {
		t.Fatal("parallel must be hidden")
	}
	for _, mixed := range []bool{false, true} {
		t.Run(strconv.FormatBool(mixed), func(t *testing.T) {
			server := newWorkflowConfigServer(t, storedWorkflowRunConfigJSON)
			flags := []string{"--parallel", "3", "--json"}
			if mixed {
				flags = append(flags, "--retries", "2")
			}
			cmd := newWorkflowConfigSetCommand(t, flags...)
			var notices strings.Builder
			cmd.SetErr(&notices)
			stdout := captureStdout(t, func() {
				if err := cmd.RunE(cmd, []string{"smoke-tests"}); err != nil {
					t.Fatal(err)
				}
			})
			if !strings.Contains(notices.String(), "revyl auth billing") || !strings.Contains(stdout, "workflow_parallelism_ignored") {
				t.Fatalf("missing notice: %s %s", notices.String(), stdout)
			}
			if mixed && len(server.putBodies) != 1 || !mixed && server.requestCount != 0 {
				t.Fatalf("requests=%d puts=%d", server.requestCount, len(server.putBodies))
			}
		})
	}
}

func TestWorkflowConfigSetRetriesValidation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		flagArgs  []string
		wantError string
	}{
		{name: "missing", flagArgs: []string{"--json"}, wantError: "--retries is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newWorkflowConfigServer(t, storedWorkflowRunConfigJSON)
			cmd := newWorkflowConfigSetCommand(t, tc.flagArgs...)

			var runErr error
			stdout := captureStdout(t, func() {
				runErr = cmd.RunE(cmd, []string{"smoke-tests"})
			})

			if server.requestCount != 0 {
				t.Fatalf("backend requests = %d, want 0", server.requestCount)
			}
			if got := reportedFailureClass(t, runErr); got != string(analytics.FailureClassUsage) {
				t.Fatalf("failure_class = %v, want %q", got, analytics.FailureClassUsage)
			}
			if stdout != "" {
				t.Fatalf("validation stdout = %q, want no new JSON failure output", stdout)
			}
			if !strings.Contains(runErr.Error(), tc.wantError) {
				t.Fatalf("error = %q, want %q", runErr, tc.wantError)
			}
		})
	}
}

func TestWorkflowConfigShowJSONReportsStoredRunConfig(t *testing.T) {
	newWorkflowConfigServer(t, storedWorkflowRunConfigJSON)
	cmd := newLeafCommand("show", runWorkflowConfigShow)
	if err := cmd.ParseFlags([]string{"--json"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	stdout := captureStdout(t, func() {
		if err := cmd.RunE(cmd, []string{"smoke-tests"}); err != nil {
			t.Fatalf("workflow config show: %v", err)
		}
	})

	output := parseJSON(t, stdout)
	if output["retries"] != float64(1) || output["override_run_config"] != true {
		t.Fatalf("config show --json = %#v, want retries 1 and override_run_config true", output)
	}
	if len(output) != 2 {
		t.Fatalf("config show --json = %#v, want only retries and override_run_config", output)
	}
}

// assertRequestBodyMatchesOpenAPI checks a captured request body against the
// committed backend OpenAPI contract, which FastAPI enforces: every required
// property present with its declared JSON type and no undeclared property
// that the backend would silently drop.
func assertRequestBodyMatchesOpenAPI(t *testing.T, method, path string, body []byte) {
	t.Helper()
	specBytes, err := os.ReadFile("../../openapi.json")
	if err != nil {
		t.Fatalf("read OpenAPI contract: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			RequestBody struct {
				Content map[string]struct {
					Schema struct {
						Ref string `json:"$ref"`
					} `json:"schema"`
				} `json:"content"`
			} `json:"requestBody"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Type string `json:"type"`
				} `json:"properties"`
				Required []string `json:"required"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(specBytes, &spec); err != nil {
		t.Fatalf("decode OpenAPI contract: %v", err)
	}
	operation, ok := spec.Paths[path][strings.ToLower(method)]
	if !ok {
		t.Fatalf("OpenAPI contract has no %s %s", method, path)
	}
	ref := operation.RequestBody.Content["application/json"].Schema.Ref
	schema, ok := spec.Components.Schemas[strings.TrimPrefix(ref, "#/components/schemas/")]
	if !ok {
		t.Fatalf("OpenAPI contract has no request schema %q for %s %s", ref, method, path)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("request body is not a JSON object: %v\n%s", err, body)
	}
	for _, name := range schema.Required {
		if _, present := got[name]; !present {
			t.Errorf("request body is missing required %q: %s", name, body)
		}
	}
	for name, value := range got {
		property, declared := schema.Properties[name]
		if !declared {
			t.Errorf("request body sends %q, which %s does not declare", name, ref)
			continue
		}
		switch property.Type {
		case "boolean":
			if _, ok := value.(bool); !ok {
				t.Errorf("%q = %#v, want a JSON boolean", name, value)
			}
		case "object":
			if _, ok := value.(map[string]interface{}); !ok {
				t.Errorf("%q = %#v, want a JSON object", name, value)
			}
		}
	}
}

func TestWorkflowInfoJSONKeepsTheOverrideStateWithoutARunConfig(t *testing.T) {
	for _, override := range []bool{true, false} {
		stdout := captureStdout(t, func() {
			if err := outputWorkflowInfoJSON(
				&api.WorkflowInfoResponse{ID: "wf-uuid-001", Name: "smoke-tests"},
				&api.Workflow{ID: "wf-uuid-001", Name: "smoke-tests", OverrideRunConfig: override},
				nil,
			); err != nil {
				t.Fatalf("outputWorkflowInfoJSON(): %v", err)
			}
		})
		var output map[string]interface{}
		if err := json.Unmarshal([]byte(stdout), &output); err != nil {
			t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
		}
		if output["override_run_config"] != override {
			t.Fatalf("override_run_config = %#v, want %v", output["override_run_config"], override)
		}
		if _, ok := output["run_config"]; ok {
			t.Fatalf("run_config = %#v, want it omitted", output["run_config"])
		}
	}
}
