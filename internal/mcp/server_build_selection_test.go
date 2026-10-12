package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/testutil"
)

const (
	branchToolAppID      = "11111111-1111-4111-8111-111111111111"
	branchToolBranchID   = "22222222-2222-4222-8222-222222222222"
	branchToolBuildID    = "33333333-3333-4333-8333-333333333333"
	branchToolTestID     = "44444444-4444-4444-8444-444444444444"
	branchToolWorkflowID = "55555555-5555-4555-8555-555555555555"
)

func branchToolServer(t *testing.T, handler http.HandlerFunc) *Server {
	t.Helper()
	testutil.SetHomeDir(t, t.TempDir())
	t.Setenv("REVYL_INHERITED_LAUNCH_ENV_VAR_IDS", "")
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv("REVYL_BACKEND_URL", srv.URL)
	t.Setenv("REVYL_APP_URL", "https://app.example")
	return &Server{apiClient: api.NewClientWithBaseURL("test-api-key", srv.URL), workDir: t.TempDir()}
}

func decodeBranchToolBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Errorf("decode %s: %v", r.URL.Path, err)
	}
	return body
}

func respondBranchToolJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func TestBranchToolSchemasExposeExplicitSelectors(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-api-key")
	s, err := NewServer("test", false)
	if err != nil {
		t.Fatal(err)
	}
	tools := listServerTools(t, s)
	for name, fields := range map[string][]string{
		"run_test":         {"branch_id", "branch_name", "build_selection", "build_version_id"},
		"run_workflow":     {"branch_name", "build_selection", "ios_build_id", "android_build_id"},
		"create_test":      {"branch_id", "branch_name", "build_selection", "pinned_build_id"},
		"update_test":      {"branch_id", "branch_name", "build_selection", "pinned_build_id"},
		"set_workflow_app": {"ios_branch_id", "ios_branch_name", "ios_build_selection", "ios_pinned_build_id", "android_branch_id", "android_branch_name", "android_build_selection", "android_pinned_build_id"},
		"upload_build":     {"branch_id", "branch_name"},
		"list_builds":      {"app_id", "branch_id", "branch_name"},
	} {
		tool := serverToolByName(t, tools, name)
		for _, field := range fields {
			requireSchemaPropertyType(t, tool.InputSchema, field, "string")
		}
	}
}

func TestRunTestToolForwardsOrOmitsBranchSelection(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input RunTestInput
		want  map[string]any
	}{
		{"saved", RunTestInput{}, map[string]any{}},
		{"latest", RunTestInput{BranchName: "main", BuildSelection: "latest"}, map[string]any{"branch_name": "main", "build_selection": "latest"}},
		{"exact", RunTestInput{BranchID: branchToolBranchID, BuildVersionID: branchToolBuildID}, map[string]any{"branch_id": branchToolBranchID, "build_version_id": branchToolBuildID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent map[string]any
			s := branchToolServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/execution/api/execute_test_id_async" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				sent = decodeBranchToolBody(t, r)
				w.WriteHeader(http.StatusBadRequest)
				respondBranchToolJSON(t, w, map[string]any{"detail": "test dispatch boundary"})
			})
			tc.input.TestName = branchToolTestID
			_, _, err := s.handleRunTest(context.Background(), &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{}}, tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if sent == nil {
				t.Fatal("no execution request")
			}
			for _, key := range []string{"branch_id", "branch_name", "build_selection", "build_version_id"} {
				want, present := tc.want[key]
				got, found := sent[key]
				if found != present || !reflect.DeepEqual(got, want) {
					t.Errorf("%s=%v present=%v, want %v present=%v", key, got, found, want, present)
				}
			}
		})
	}
}

func TestRunWorkflowToolForwardsBranchSelection(t *testing.T) {
	var sent map[string]any
	s := branchToolServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/workflows/get_workflow_info" {
			respondBranchToolJSON(t, w, map[string]any{"id": branchToolWorkflowID, "name": "workflow"})
			return
		}
		if r.URL.Path != "/api/v1/workflow-executions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		sent = decodeBranchToolBody(t, r)
		w.WriteHeader(http.StatusBadRequest)
		respondBranchToolJSON(t, w, map[string]any{"detail": "test dispatch boundary"})
	})
	_, _, err := s.handleRunWorkflow(context.Background(), &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{}}, RunWorkflowInput{WorkflowName: branchToolWorkflowID, BranchName: "main", BuildSelection: "latest"})
	if err != nil {
		t.Fatal(err)
	}
	if sent["branch_name"] != "main" || sent["build_selection"] != "latest" {
		t.Fatalf("selection not forwarded: %#v", sent)
	}
}

func TestUpdateTestToolPreservesOmissionAndExplicitAutomatic(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input UpdateTestInput
		want  map[string]any
	}{
		{"omit", UpdateTestInput{}, map[string]any{}},
		{"automatic", UpdateTestInput{BuildSelection: "inherit"}, map[string]any{"branch_id": nil, "build_selection": "inherit", "pinned_build_id": nil}},
		{"yaml", UpdateTestInput{YAMLContent: "test:\n  build:\n    branch_id: " + branchToolBranchID + "\n    build_selection: latest\n  blocks: []\n"}, map[string]any{"branch_id": branchToolBranchID, "build_selection": "latest", "pinned_build_id": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent map[string]any
			s := branchToolServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/tests/get_test_by_id/" + branchToolTestID:
					respondBranchToolJSON(t, w, map[string]any{"id": branchToolTestID, "app_id": branchToolAppID})
				case "/api/v1/tests/update/" + branchToolTestID:
					sent = decodeBranchToolBody(t, r)
					respondBranchToolJSON(t, w, map[string]any{"id": branchToolTestID, "version": 2})
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			tc.input.TestNameOrID = branchToolTestID
			if tc.input.YAMLContent == "" {
				tc.input.YAMLContent = "test:\n  blocks: []\n"
			}
			_, out, err := s.handleUpdateTest(context.Background(), nil, tc.input)
			if err != nil || !out.Success {
				t.Fatalf("update failed: %+v %v", out, err)
			}
			for _, key := range []string{"branch_id", "build_selection", "pinned_build_id"} {
				want, present := tc.want[key]
				got, found := sent[key]
				if found != present || !reflect.DeepEqual(got, want) {
					t.Errorf("%s=%v present=%v, want %v present=%v", key, got, found, want, present)
				}
			}
		})
	}
}

func TestWorkflowAppToolPreservesOtherPlatformAndClearRemovesSelections(t *testing.T) {
	for _, clear := range []bool{false, true} {
		t.Run(map[bool]string{false: "set", true: "clear"}[clear], func(t *testing.T) {
			original := map[string]any{
				"ios_build":     map[string]any{"app_id": branchToolAppID, "branch_id": branchToolBranchID, "build_selection": "pinned", "pinned_build_id": branchToolBuildID},
				"android_build": map[string]any{"app_id": "android-app", "branch_id": "android-branch", "build_selection": "latest"},
			}
			var sent map[string]any
			s := branchToolServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/workflows/get_workflow_info":
					respondBranchToolJSON(t, w, map[string]any{"id": branchToolWorkflowID, "build_config": original, "override_build_config": true})
				case "/api/v1/workflows/update_build_config/" + branchToolWorkflowID:
					sent = decodeBranchToolBody(t, r)
					respondBranchToolJSON(t, w, map[string]any{"message": "ok"})
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			var out SimpleSuccessOutput
			var err error
			if clear {
				_, out, err = s.handleClearWorkflowApp(context.Background(), nil, ClearWorkflowAppInput{WorkflowNameOrID: branchToolWorkflowID})
			} else {
				_, out, err = s.handleSetWorkflowApp(context.Background(), nil, SetWorkflowAppInput{WorkflowNameOrID: branchToolWorkflowID, IOSAppID: branchToolAppID, IOSBuildSelection: "inherit"})
			}
			if err != nil || !out.Success {
				t.Fatalf("workflow update failed: %+v %v", out, err)
			}
			if sent["override_build_config"] != !clear {
				t.Fatalf("wrong override: %#v", sent)
			}
			if clear {
				if config, present := sent["build_config"]; !present || config != nil {
					t.Fatalf("clear kept saved selections: %#v", sent)
				}
				return
			}
			config, ok := sent["build_config"].(map[string]any)
			if !ok {
				t.Fatal("saved config lost")
			}
			if !reflect.DeepEqual(config["android_build"], original["android_build"]) {
				t.Fatal("other platform changed")
			}
			ios := config["ios_build"].(map[string]any)
			if ios["branch_id"] != nil || ios["pinned_build_id"] != nil || ios["build_selection"] != "inherit" {
				t.Fatalf("Automatic reset not explicit: %#v", ios)
			}
		})
	}
}

func TestListBuildsToolScopesActualVersionsAndAllowsEmptyBranch(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "builds", true: "empty"}[empty], func(t *testing.T) {
			s := branchToolServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/apps/" + branchToolAppID + "/branches":
					if r.URL.Query().Get("name") != "main" {
						t.Error("branch name not forwarded")
					}
					respondBranchToolJSON(t, w, map[string]any{"branches": []map[string]any{{"id": branchToolBranchID, "app_id": branchToolAppID, "name": "main"}}, "total": 1})
				case "/api/v1/apps/" + branchToolAppID + "/builds":
					if r.URL.Query().Get("branch_id") != branchToolBranchID {
						t.Error("branch filter missing")
					}
					items := []map[string]any{}
					if !empty {
						items = append(items, map[string]any{"id": branchToolBuildID, "version": "v1", "branch_id": branchToolBranchID})
					}
					respondBranchToolJSON(t, w, map[string]any{"items": items, "total": len(items), "page": 1})
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			_, out, err := s.handleListBuilds(context.Background(), nil, ListBuildsInput{AppID: branchToolAppID, BranchName: "main"})
			if err != nil || out.ErrorMessage != "" {
				t.Fatalf("list failed: %+v %v", out, err)
			}
			if out.BranchID != branchToolBranchID || out.BranchName != "main" || out.AppID != branchToolAppID {
				t.Fatalf("scope lost: %+v", out)
			}
			if empty && len(out.Versions) != 0 {
				t.Fatal("empty branch fell back")
			}
		})
	}
}

func TestListBuildsInvocationDefaultsToAllBranches(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-api-key")
	const otherBranchID = "66666666-6666-4666-8666-666666666666"
	requests := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		query := r.URL.Query()
		if r.URL.Path != "/api/v1/apps/"+branchToolAppID+"/builds" || query.Has("branch_id") || query.Has("branch_name") || query.Get("page") != "2" || query.Get("page_size") != "2" {
			t.Errorf("unexpected unfiltered lookup: %s", r.URL)
			http.NotFound(w, r)
			return
		}
		respondBranchToolJSON(t, w, api.BuildVersionsPage{
			Items: []api.BuildVersion{
				{ID: branchToolBuildID, Version: "base-1", BranchID: branchToolBranchID, BranchName: "base"},
				{ID: "77777777-7777-4777-8777-777777777777", Version: "release-1", BranchID: otherBranchID, BranchName: "release"},
			},
			Total: 5, Page: 2, PageSize: 2, HasNext: true,
		})
	}))
	defer backend.Close()
	s, err := NewServer("test", false)
	if err != nil {
		t.Fatal(err)
	}
	s.apiClient = api.NewClientWithBaseURL("test-api-key", backend.URL)
	client := connectWorkspaceTestClient(t, s)
	result, err := client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "list_builds", Arguments: map[string]any{"app_id": branchToolAppID, "page": 2, "limit": 2},
	})
	if err != nil || result.IsError {
		t.Fatalf("list_builds failed: result=%+v err=%v", result, err)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var output ListBuildsOutput
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatal(err)
	}
	if output.ErrorMessage != "" || len(output.Versions) != 2 || output.Total != 5 || output.Page != 2 || !output.HasNext || !output.HasMore || requests != 1 {
		t.Fatalf("pagination changed: output=%+v requests=%d", output, requests)
	}
	if output.Versions[0].BranchID != branchToolBranchID || output.Versions[0].BranchName != "base" || output.Versions[1].BranchID != otherBranchID || output.Versions[1].BranchName != "release" {
		t.Fatalf("row provenance changed: %+v", output.Versions)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"branch_id", "branch_name"} {
		if _, present := object[field]; present {
			t.Fatalf("unfiltered list invented a top-level %s: %s", field, encoded)
		}
	}
}

func TestCreateTestToolSavesNamedBranchByStableID(t *testing.T) {
	var sent map[string]any
	s := branchToolServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/entity/users/get_user_uuid":
			respondBranchToolJSON(t, w, map[string]any{"user_id": "user", "org_id": "org"})
		case "/api/v1/apps/" + branchToolAppID:
			respondBranchToolJSON(t, w, map[string]any{"id": branchToolAppID, "name": "app", "platform": "ios", "versions_count": 1, "latest_version": "v1"})
		case "/api/v1/apps/" + branchToolAppID + "/builds":
			respondBranchToolJSON(t, w, map[string]any{"items": []map[string]any{{"id": branchToolBuildID, "version": "v1"}}, "total": 1})
		case "/api/v1/apps/" + branchToolAppID + "/branches":
			if r.URL.Query().Get("name") != "main" {
				t.Error("name not forwarded")
			}
			respondBranchToolJSON(t, w, map[string]any{"branches": []map[string]any{{"id": branchToolBranchID, "app_id": branchToolAppID, "name": "main"}}, "total": 1})
		case "/api/v1/tests/create":
			sent = decodeBranchToolBody(t, r)
			respondBranchToolJSON(t, w, map[string]any{"id": branchToolTestID, "version": 1})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	_, out, err := s.handleCreateTest(context.Background(), nil, CreateTestInput{Name: "branch test", Platform: "ios", AppID: branchToolAppID, BranchName: "main", BuildSelection: "latest", YAMLContent: "test:\n  blocks:\n    - type: instructions\n      step_description: Open app\n"})
	if err != nil || !out.Success {
		t.Fatalf("create failed: %+v %v", out, err)
	}
	if sent["branch_id"] != branchToolBranchID || sent["build_selection"] != "latest" {
		t.Fatalf("selection lost: %#v", sent)
	}
	if _, ok := sent["branch_name"]; ok {
		t.Fatal("saved selector contains mutable name")
	}
}

func TestUploadBuildToolForwardsDestinationAndReturnsFrozenBranch(t *testing.T) {
	var sessionBody, finalBody map[string]any
	s := branchToolServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/apps/" + branchToolAppID + "/builds/upload-session":
			sessionBody = decodeBranchToolBody(t, r)
			respondBranchToolJSON(t, w, map[string]any{"upload_id": "upload-session", "upload_url": "http://" + r.Host + "/artifact", "content_type": "application/octet-stream", "branch_id": branchToolBranchID, "branch_name": "main"})
		case "/artifact":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/apps/" + branchToolAppID + "/builds":
			finalBody = decodeBranchToolBody(t, r)
			respondBranchToolJSON(t, w, map[string]any{"id": branchToolBuildID, "version": "v1", "branch_id": branchToolBranchID, "branch_name": "main"})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	artifact := filepath.Join(t.TempDir(), "app.apk")
	if err := os.WriteFile(artifact, []byte("local transport fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	_, out, err := s.handleUploadBuild(context.Background(), nil, UploadBuildInput{AppID: branchToolAppID, BranchName: "main", Version: "v1", FilePath: artifact})
	if err != nil || !out.Success {
		t.Fatalf("upload failed: %+v %v", out, err)
	}
	if sessionBody["branch_name"] != "main" || finalBody["upload_id"] != "upload-session" {
		t.Fatalf("upload destination lost: session=%#v final=%#v", sessionBody, finalBody)
	}
	if out.BranchID != branchToolBranchID || out.BranchName != "main" || out.VersionID != branchToolBuildID {
		t.Fatalf("identity lost: %+v", out)
	}
}

func TestBranchToolsRejectConflictingSelectorsBeforeMutation(t *testing.T) {
	calls := 0
	s := branchToolServer(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusBadRequest) })
	_, run, err := s.handleRunTest(context.Background(), &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{}}, RunTestInput{TestName: branchToolTestID, BranchID: branchToolBranchID, BranchName: "main"})
	if err != nil || run.ErrorMessage == "" {
		t.Fatalf("conflicting branch accepted: %+v %v", run, err)
	}
	_, list, err := s.handleListBuilds(context.Background(), nil, ListBuildsInput{BranchName: "main"})
	if err != nil || list.ErrorMessage == "" {
		t.Fatalf("unscoped branch accepted: %+v %v", list, err)
	}
	if calls != 0 {
		t.Fatalf("invalid selection made %d requests", calls)
	}
}

func TestListBuildsToolPreservesLegacyAppSummaryLimitNormalization(t *testing.T) {
	for _, tc := range []struct {
		limit int
		want  string
	}{{0, "20"}, {-1, "50"}, {500, "100"}} {
		t.Run(tc.want, func(t *testing.T) {
			s := branchToolServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/apps/" || r.URL.Query().Get("page_size") != tc.want {
					t.Errorf("unexpected query: %s", r.URL.String())
				}
				respondBranchToolJSON(t, w, map[string]any{"items": []map[string]any{{"id": branchToolAppID, "name": "app", "platform": "ios", "versions_count": 1, "latest_version": "v1"}}, "total": 1})
			})
			_, out, err := s.handleListBuilds(context.Background(), nil, ListBuildsInput{Limit: tc.limit})
			if err != nil || out.ErrorMessage != "" || len(out.Builds) != 1 || out.Builds[0].ID != branchToolAppID {
				t.Fatalf("legacy list failed: %+v %v", out, err)
			}
		})
	}
}
