package execution

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

const selectorTestBranchID = "11111111-1111-4111-8111-111111111111"
const selectorTestBuildID = "22222222-2222-4222-8222-222222222222"
const selectorTestAppID = "33333333-3333-4333-8333-333333333333"

func TestRunTestBuildSelectorWire(t *testing.T) {
	for _, tc := range []struct{ name, branch, mode, build string }{
		{"saved", "", "", ""},
		{"branch policy", "feature/a", "", ""},
		{"latest saved branch", "", "latest", ""},
		{"exact", "feature/a", "", selectorTestBuildID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/api/v1/execution/api/execute_test_id_async" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected", 500)
					return
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				_, _ = w.Write([]byte(`{"task_id":"task"}`))
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			result, err := RunTest(context.Background(), "key", nil, RunTestParams{TestNameOrID: "test", NoWait: true, BranchName: tc.branch, BuildSelection: tc.mode, BuildVersionID: tc.build})
			if err != nil || result.Status != "queued" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			for key, value := range map[string]string{"branch_name": tc.branch, "build_selection": tc.mode, "build_version_id": tc.build} {
				got, exists := body[key]
				if value == "" && exists {
					t.Errorf("%s should be omitted: %v", key, got)
				}
				if value != "" && got != value {
					t.Errorf("%s=%v want %s", key, got, value)
				}
			}
		})
	}
}

func TestExecutionContextRemainsAnUnvalidatedEphemeralHint(t *testing.T) {
	for _, workflow := range []bool{false, true} {
		t.Run(map[bool]string{false: "test", true: "workflow"}[workflow], func(t *testing.T) {
			contextName := strings.Repeat("x", 256)
			var body map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/workflow-executions" && r.URL.Path != "/api/v1/execution/api/execute_test_id_async" {
					t.Errorf("unexpected saved-target lookup: %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				_, _ = w.Write([]byte(`{"task_id":"task"}`))
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			var err error
			if workflow {
				_, err = RunWorkflow(context.Background(), "key", nil, RunWorkflowParams{WorkflowNameOrID: "workflow", NoWait: true, ContextBranchName: contextName})
			} else {
				_, err = RunTest(context.Background(), "key", nil, RunTestParams{TestNameOrID: "test", NoWait: true, ContextBranchName: contextName})
			}
			if err != nil || body["context_branch_name"] != contextName {
				t.Fatalf("server-owned saved selector cannot ignore unsupported context: request=%v err=%v", body, err)
			}
			for _, field := range []string{"branch_name", "branch_id", "build_selection", "build_config", "pinned_build_id"} {
				if _, present := body[field]; present {
					t.Fatalf("hint manufactured explicit %s: %v", field, body)
				}
			}
		})
	}
}

func TestRunWorkflowExactBuildHandoff(t *testing.T) {
	var executed map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/apps/resolve":
			var request map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if request["pinned_build_id"] != selectorTestBuildID || request["branch_name"] != "main" {
				t.Errorf("resolver request=%v", request)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"version_id": selectorTestBuildID, "branch_id": selectorTestBranchID, "branch_name": "main", "download_url": "https://example.invalid/build"})
		case "/api/v1/apps/builds/" + selectorTestBuildID:
			_ = json.NewEncoder(w).Encode(map[string]string{"id": selectorTestBuildID, "app_id": selectorTestAppID, "branch_id": selectorTestBranchID, "version": "duplicate-label"})
		case "/api/v1/workflow-executions":
			if err := json.NewDecoder(r.Body).Decode(&executed); err != nil {
				t.Error(err)
			}
			_, _ = w.Write([]byte(`{"task_id":"task"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	result, err := RunWorkflow(context.Background(), "key", nil, RunWorkflowParams{WorkflowNameOrID: "workflow", IOSAppID: selectorTestAppID, IOSBuildID: selectorTestBuildID, BranchName: "main", NoWait: true})
	if err != nil || result.Status != "queued" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	slot := executed["build_config"].(map[string]interface{})["ios_build"].(map[string]interface{})
	if slot["pinned_build_id"] != selectorTestBuildID || slot["branch_id"] != selectorTestBranchID || slot["build_selection"] != "pinned" {
		t.Fatalf("slot=%v", slot)
	}
	if _, exists := slot["pinned_version"]; exists {
		t.Fatalf("exact handoff sent a version label: %v", slot)
	}
}

func TestRunWorkflowRejectsConflictingSelectorsBeforeRequest(t *testing.T) {
	for _, params := range []RunWorkflowParams{
		{IOSBuildID: selectorTestBuildID},
		{AndroidBuild: "1.0"},
		{IOSAppID: selectorTestAppID, IOSBuildID: selectorTestBuildID, BuildSelection: "latest"},
		{IOSAppID: selectorTestAppID, IOSBuildID: selectorTestBuildID, IOSBuild: "1.0"},
	} {
		if _, err := RunWorkflow(context.Background(), "key", nil, params); err == nil {
			t.Fatalf("expected invalid selector: %+v", params)
		}
	}
}

func TestSavedBuildSelectionConfigOmissionAndReset(t *testing.T) {
	omitted, err := SavedBuildSelectionFromConfig(config.TestBuildConfig{Name: "App"})
	if err != nil || omitted != nil {
		t.Fatalf("omitted=%+v err=%v", omitted, err)
	}
	reset, err := SavedBuildSelectionFromConfig(config.TestBuildConfig{BuildSelection: "inherit"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(&api.UpdateTestRequest{TestID: "test", SavedBuildSelection: reset})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"branch_id", "pinned_build_id"} {
		if value, exists := body[key]; !exists || value != nil {
			t.Errorf("explicit reset %s=%v exists=%t", key, value, exists)
		}
	}
	if body["build_selection"] != "inherit" {
		t.Fatal(body)
	}
	legacy, err := SavedBuildSelectionFromConfig(config.TestBuildConfig{PinnedVersion: "1.0"})
	if err != nil || legacy != nil {
		t.Fatalf("legacy=%+v err=%v", legacy, err)
	}
	if _, err := SavedBuildSelectionFromConfig(config.TestBuildConfig{PinnedVersion: "1.0", BuildSelection: "latest"}); err == nil {
		t.Fatal("accepted conflicting legacy and canonical selection")
	}
}

func TestResolveSavedBranchAllowsAnEmptyBranch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/apps/"+selectorTestAppID+"/branches" || r.URL.Query().Get("name") != "empty/branch" {
			t.Errorf("unexpected request %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"branches": []map[string]string{{"id": selectorTestBranchID, "app_id": selectorTestAppID, "name": "empty/branch"}}, "total": 1})
	}))
	defer server.Close()
	selection, err := ResolveSavedBuildSelection(context.Background(), api.NewClientWithBaseURL("key", server.URL), selectorTestAppID, "empty/branch", "", "latest", "")
	if err != nil {
		t.Fatal(err)
	}
	if selection.BranchID == nil || *selection.BranchID != selectorTestBranchID || selection.BuildSelection != api.BuildSelectionModeLatest {
		t.Fatalf("selector=%+v", selection)
	}
}

func TestCreateTestAllowsEmptySavedBranchAndEmptyDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + selectorTestAppID:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": selectorTestAppID, "name": "App", "platform": "ios", "default_branch_id": selectorTestBranchID, "versions_count": 0})
		case "/api/v1/apps/":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"items": []map[string]interface{}{{"id": selectorTestAppID, "name": "App", "platform": "ios", "default_branch_id": selectorTestBranchID, "versions_count": 0}}, "total": 1, "has_next": false})
		case "/api/v1/apps/" + selectorTestAppID + "/builds":
			_, _ = w.Write([]byte(`{"items":[],"total":0,"has_next":false}`))
		case "/api/v1/apps/" + selectorTestAppID + "/branches":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"branches": []map[string]string{{"id": selectorTestBranchID, "app_id": selectorTestAppID, "name": "empty/branch"}}, "total": 1})
		default:
			t.Errorf("saved selection attempted runtime build resolution: %s", r.URL.Path)
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()
	for _, appID := range []string{"", selectorTestAppID} {
		req, err := buildCreateTestRequest(context.Background(), api.NewClientWithBaseURL("key", server.URL), CreateTestParams{
			Name: "Test", Platform: "ios", OrgID: "org", AppID: appID, BranchName: "empty/branch", AllowEmpty: true,
			YAMLContent: "test:\n  build:\n    name: App\n",
		})
		if err != nil || req.AppID != selectorTestAppID || req.BranchID == nil || *req.BranchID != selectorTestBranchID || req.BuildSelection != api.BuildSelectionModeInherit {
			t.Fatalf("empty saved branch rejected: request=%+v err=%v", req, err)
		}
	}
}

func TestCreateTestPreservesCanonicalAndLegacyYAMLSelectors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/apps/"+selectorTestAppID {
			t.Errorf("unexpected request %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": selectorTestAppID, "name": "App", "platform": "ios", "latest_version": "1.0", "versions_count": 1})
	}))
	defer server.Close()
	for _, tc := range []struct{ yaml, mode, legacy string }{
		{"    branch_id: " + selectorTestBranchID + "\n    build_selection: pinned\n    pinned_build_id: " + selectorTestBuildID, "pinned", ""},
		{"    pinned_version: 1.0.0", "", "1.0.0"},
		{"    branch_id: " + selectorTestBranchID + "\n    build_selection: pinned\n    pinned_build_id: " + selectorTestBuildID + "\n    pinned_version: 1.0.0", "pinned", "1.0.0"},
	} {
		req, err := buildCreateTestRequest(context.Background(), api.NewClientWithBaseURL("key", server.URL), CreateTestParams{Name: "Test", Platform: "ios", OrgID: "org", AppID: selectorTestAppID, AllowEmpty: true, YAMLContent: "test:\n  build:\n" + tc.yaml + "\n"})
		if err != nil {
			t.Fatal(err)
		}
		if req.PinnedVersionID != tc.legacy {
			t.Fatalf("legacy=%s", req.PinnedVersionID)
		}
		if tc.mode == "" && req.SavedBuildSelection != nil {
			t.Fatal("legacy selector gained canonical defaults")
		}
		if tc.mode != "" && (req.SavedBuildSelection == nil || string(req.BuildSelection) != tc.mode || req.PinnedBuildID == nil || *req.PinnedBuildID != selectorTestBuildID) {
			t.Fatalf("selector=%+v", req.SavedBuildSelection)
		}
	}
}

func TestCreateTestMixedYAMLDelegatesPinConsistencyToServer(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "matching exported pin", true: "contradictory version"}[rejected], func(t *testing.T) {
			created := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/" + selectorTestAppID:
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": selectorTestAppID, "name": "App", "platform": "ios", "latest_version": "1.0", "versions_count": 1})
				case "/api/v1/apps/":
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"items": []map[string]interface{}{{"id": selectorTestAppID, "name": "App", "platform": "ios", "latest_version": "1.0", "versions_count": 1}}, "total": 1, "has_next": false})
				case "/api/v1/tests/create":
					created = true
					var body map[string]interface{}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body["pinned_build_id"] != selectorTestBuildID || body["branch_id"] != selectorTestBranchID || body["build_selection"] != "pinned" || body["pinned_version"] != "1.0.0" {
						t.Errorf("mixed selector was dropped before server consistency check: %v", body)
					}
					if rejected {
						w.WriteHeader(http.StatusUnprocessableEntity)
						_, _ = w.Write([]byte(`{"detail":"The saved version doesn't match the exact build's version"}`))
					} else {
						_, _ = w.Write([]byte(`{"id":"test","version":1}`))
					}
				default:
					t.Errorf("unexpected metadata/artifact resolution: %s", r.URL.Path)
					http.Error(w, "unexpected", http.StatusNotFound)
				}
			}))
			defer server.Close()
			result, err := CreateTestWithClient(context.Background(), api.NewClientWithBaseURL("key", server.URL), CreateTestParams{
				Name: "Exported", Platform: "ios", OrgID: "org", AppID: selectorTestAppID, AllowEmpty: true,
				YAMLContent: "test:\n  build:\n    name: App\n    branch_id: " + selectorTestBranchID + "\n    build_selection: pinned\n    pinned_build_id: " + selectorTestBuildID + "\n    pinned_version: 1.0.0\n",
			})
			if !created {
				t.Fatal("import did not reach authorized server validation")
			}
			if rejected {
				if err == nil || result != nil || !strings.Contains(err.Error(), "The saved version doesn't match the exact build's version") {
					t.Fatalf("server rejection was swallowed: result=%+v err=%v", result, err)
				}
			} else if err != nil || result.TestID != "test" {
				t.Fatalf("matching mixed pin rejected: result=%+v err=%v", result, err)
			}
		})
	}
}
