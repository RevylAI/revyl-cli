package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/revyl/cli/internal/api"
)

const persistentComputerTestID = "11111111-1111-4111-8111-111111111111"

func persistentComputerTestStatus(id, state string) map[string]any {
	return map[string]any{
		"computer_id": id, "state": state, "run_session_id": id,
		"saved_at": nil, "durable": false, "created_at": "2026-11-27T00:00:00Z",
	}
}

func persistentComputerTestReport(id string, persistent map[string]any) map[string]any {
	report := map[string]any{
		"kind": "computer", "version": 1, "computer_session_id": id,
		"report_id": "22222222-2222-4222-8222-222222222222", "org_id": "44444444-4444-4444-8444-444444444444",
		"os": "macos", "created_at": "2026-11-27T00:00:00Z", "expires_at": "2026-11-27T02:00:00Z",
		"as_of": "2026-11-27T00:00:03Z", "state": "active", "execution_started_at": "2026-11-27T00:00:00Z",
		"release": "pending", "evidence_completeness": "partial", "evidence_write_status": "recorded", "steps": []any{}, "child_reports": []any{},
	}
	if persistent != nil {
		report["persistent"] = persistent
	}
	return report
}

type persistentComputerBackend struct {
	t        *testing.T
	requests []string
	bodies   map[string]map[string]any
	respond  func(method, path string, body map[string]any) (int, any)
}

func newPersistentComputerServer(t *testing.T, respond func(method, path string, body map[string]any) (int, any)) (*Server, *persistentComputerBackend) {
	t.Helper()
	backend := &persistentComputerBackend{t: t, bodies: map[string]map[string]any{}, respond: respond}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		key := r.Method + " " + r.URL.RequestURI()
		backend.requests = append(backend.requests, key)
		backend.bodies[key] = body
		status, response := backend.respond(r.Method, r.URL.Path, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if response != nil {
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	t.Cleanup(srv.Close)
	s := &Server{apiClient: api.NewClientWithBaseURL("test-api-key", srv.URL), workDir: t.TempDir()}
	s.mcpServer = mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "test"}, nil)
	s.registerManagePersistentComputersTool()
	return s, backend
}

func persistentComputerResult(t *testing.T, result *mcpsdk.CallToolResult) map[string]any {
	t.Helper()
	if result.IsError {
		t.Fatalf("tool failed: %s", persistentComputerErrorText(result))
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var output CompositeOutput
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatal(err)
	}
	return output.Result
}

func persistentComputerErrorText(result *mcpsdk.CallToolResult) string {
	var text []string
	for _, content := range result.Content {
		if part, ok := content.(*mcpsdk.TextContent); ok {
			text = append(text, part.Text)
		}
	}
	return strings.Join(text, " ")
}

func TestPersistentComputerCreateFromSnapshotReturnsTheNewComputer(t *testing.T) {
	var created string
	s, backend := newPersistentComputerServer(t, func(method, path string, body map[string]any) (int, any) {
		if method == http.MethodPost && path == "/api/v1/computer-runs" {
			created, _ = body["computer_session_id"].(string)
			return http.StatusOK, persistentComputerTestReport(created, nil)
		}
		return http.StatusOK, persistentComputerTestReport(created, persistentComputerTestStatus(created, "starting"))
	})

	output := persistentComputerResult(t, callServerTool(t, s, "manage_persistent_computers", map[string]any{
		"action": "create", "params": map[string]any{"from_snapshot": "app-golden", "timeout_seconds": 3600},
	}))

	request := backend.bodies["POST /api/v1/computer-runs"]
	if request["os"] != "macos" || request["persistent"] != true || request["from_snapshot"] != "app-golden" || request["timeout_seconds"] != float64(3600) {
		t.Fatalf("create request = %v", request)
	}
	computer, _ := output["computer"].(map[string]any)
	if _, err := uuid.Parse(created); err != nil || computer["computer_id"] != created || computer["state"] != "starting" {
		t.Fatalf("create output = %v, created %q", output, created)
	}
}

func TestPersistentComputerStopReportsASaveStillRunningAtTheBound(t *testing.T) {
	previous := persistentComputerSaveWait
	persistentComputerSaveWait = 0
	t.Cleanup(func() { persistentComputerSaveWait = previous })
	for _, test := range []struct {
		state       string
		wantMessage bool
	}{{"stopping", true}, {"stopped", false}} {
		t.Run(test.state, func(t *testing.T) {
			s, backend := newPersistentComputerServer(t, func(string, string, map[string]any) (int, any) {
				return http.StatusOK, persistentComputerTestReport(persistentComputerTestID, persistentComputerTestStatus(persistentComputerTestID, test.state))
			})

			output := persistentComputerResult(t, callServerTool(t, s, "manage_persistent_computers", map[string]any{
				"action": "stop", "params": map[string]any{"computer_id": persistentComputerTestID, "wait": true},
			}))

			computer, _ := output["computer"].(map[string]any)
			message, _ := output["message"].(string)
			if computer["state"] != test.state || strings.Contains(message, "still saving") != test.wantMessage {
				t.Fatalf("stop output = %v", output)
			}
			if backend.requests[0] != "POST /api/v1/computer-runs/"+persistentComputerTestID+"/stop" {
				t.Fatalf("requests = %v", backend.requests)
			}
		})
	}
}

func TestPersistentComputerDeletesNeedConfirm(t *testing.T) {
	for _, test := range []struct {
		action string
		params map[string]any
		path   string
	}{
		{"delete", map[string]any{"computer_id": persistentComputerTestID}, "DELETE /api/v1/computer-runs/" + persistentComputerTestID},
		{"snapshot_delete", map[string]any{"snapshot": "app-golden"}, "DELETE /api/v1/computer-runs/snapshots/app-golden"},
	} {
		t.Run(test.action, func(t *testing.T) {
			s, backend := newPersistentComputerServer(t, func(string, string, map[string]any) (int, any) {
				return http.StatusNoContent, nil
			})

			unconfirmed := callServerTool(t, s, "manage_persistent_computers", map[string]any{"action": test.action, "params": test.params})
			if !unconfirmed.IsError || !strings.Contains(persistentComputerErrorText(unconfirmed), "confirm true") || len(backend.requests) != 0 {
				t.Fatalf("unconfirmed %s = %+v, requests %v", test.action, unconfirmed, backend.requests)
			}
			test.params["confirm"] = true
			output := persistentComputerResult(t, callServerTool(t, s, "manage_persistent_computers", map[string]any{"action": test.action, "params": test.params}))
			if output["deleted"] != true || len(backend.requests) != 1 || backend.requests[0] != test.path {
				t.Fatalf("confirmed %s = %v, requests %v", test.action, output, backend.requests)
			}
		})
	}
}

func TestPersistentComputerGetRefusesAComputerThatIsNotPersistent(t *testing.T) {
	s, _ := newPersistentComputerServer(t, func(string, string, map[string]any) (int, any) {
		return http.StatusOK, persistentComputerTestReport(persistentComputerTestID, nil)
	})

	result := callServerTool(t, s, "manage_persistent_computers", map[string]any{"action": "get", "params": map[string]any{"computer_id": persistentComputerTestID}})

	if !result.IsError || !strings.Contains(persistentComputerErrorText(result), "isn't persistent") {
		t.Fatalf("get = %+v", result)
	}
}

func TestPersistentComputerRefusalsNameTheNextAction(t *testing.T) {
	for _, test := range []struct {
		code, action string
		params       map[string]any
		says         string
	}{
		{"snapshot_limit", "snapshot_save", map[string]any{"computer_id": persistentComputerTestID, "name": "app-golden"}, "action snapshot_delete"},
		{"persistent_computer_limit", "create", nil, "action delete"},
		{"computer_not_stopped", "snapshot_save", map[string]any{"computer_id": persistentComputerTestID, "name": "app-golden"}, "wait true"},
		{"inference_unavailable", "start", map[string]any{"computer_id": persistentComputerTestID}, "without_inference true"},
		{"run_conflict", "start", map[string]any{"computer_id": persistentComputerTestID}, "try start again"},
	} {
		t.Run(test.code, func(t *testing.T) {
			s, _ := newPersistentComputerServer(t, func(string, string, map[string]any) (int, any) {
				return http.StatusConflict, map[string]any{"detail": test.code}
			})

			result := callServerTool(t, s, "manage_persistent_computers", map[string]any{"action": test.action, "params": test.params})

			if !result.IsError || !strings.Contains(persistentComputerErrorText(result), test.says) {
				t.Fatalf("%s refusal = %q", test.code, persistentComputerErrorText(result))
			}
		})
	}
}

func TestPersistentComputerListsPageByLimitAndOffset(t *testing.T) {
	s, backend := newPersistentComputerServer(t, func(_ string, path string, _ map[string]any) (int, any) {
		if strings.HasSuffix(path, "/snapshots") {
			return http.StatusOK, map[string]any{"snapshots": []any{map[string]any{
				"id": "33333333-3333-4333-8333-333333333333", "name": "app-golden", "description": nil,
				"computer_id": persistentComputerTestID, "saved_at": "2026-11-27T00:00:00Z", "expires_at": nil,
			}}, "total": 26, "limit": 25, "offset": 25}
		}
		return http.StatusOK, map[string]any{"computers": []any{persistentComputerTestStatus(persistentComputerTestID, "stopped")}, "total": 1, "limit": 25, "offset": 0}
	})

	computers := persistentComputerResult(t, callServerTool(t, s, "manage_persistent_computers", map[string]any{"action": "list"}))
	snapshots := persistentComputerResult(t, callServerTool(t, s, "manage_persistent_computers", map[string]any{"action": "snapshot_list", "params": map[string]any{"offset": 25}}))

	if computers["total"] != float64(1) || snapshots["total"] != float64(26) || len(snapshots["snapshots"].([]any)) != 1 {
		t.Fatalf("lists = %v, %v", computers, snapshots)
	}
	if strings.Join(backend.requests, ",") != "GET /api/v1/computer-runs/persistent?limit=25,GET /api/v1/computer-runs/snapshots?limit=25&offset=25" {
		t.Fatalf("requests = %v", backend.requests)
	}
}
