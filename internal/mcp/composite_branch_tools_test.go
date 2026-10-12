package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBranchCompositeToolsAdvertiseObjectParamsAndSelectors(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-api-key")
	s, err := NewServer("test", false, WithProfile(ProfileFull))
	if err != nil {
		t.Fatal(err)
	}
	tools := listServerTools(t, s)
	for name, selectors := range map[string][]string{
		"manage_tests":     {"branch_id", "branch_name", "build_selection", "pinned_build_id", "build_version_id"},
		"manage_builds":    {"branch_id", "branch_name", "app_id"},
		"manage_workflows": {"branch_name", "build_selection", "ios_build_id", "android_build_id", "ios_branch_id", "android_branch_id", "ios_pinned_build_id", "android_pinned_build_id"},
	} {
		tool := serverToolByName(t, tools, name)
		requireSchemaPropertyType(t, tool.InputSchema, "params", []any{"object", "null"})
		for _, selector := range selectors {
			if !strings.Contains(tool.Description, selector) {
				t.Errorf("%s description omits %s", name, selector)
			}
		}
	}
}

func TestBuildCompositeAcceptsObjectAndForwardsBranchBeforeListing(t *testing.T) {
	s := branchToolServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/apps/" + branchToolAppID + "/branches":
			if r.URL.Query().Get("name") != "feature/ui" {
				t.Errorf("branch name = %q", r.URL.Query().Get("name"))
			}
			respondBranchToolJSON(t, w, map[string]any{"branches": []map[string]any{{"id": branchToolBranchID, "app_id": branchToolAppID, "name": "feature/ui"}}, "total": 1})
		case "/api/v1/apps/" + branchToolAppID + "/builds":
			if r.URL.Query().Get("branch_id") != branchToolBranchID {
				t.Error("resolved branch filter missing")
			}
			respondBranchToolJSON(t, w, map[string]any{"items": []map[string]any{{"id": branchToolBuildID, "branch_id": branchToolBranchID}}, "total": 1, "page": 1})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	s.mcpServer = mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "test"}, nil)
	s.registerManageBuildsTool()
	result := callServerTool(t, s, "manage_builds", map[string]any{
		"action": "list",
		"params": map[string]any{"app_id": branchToolAppID, "branch_name": "feature/ui", "limit": 1},
	})
	if result.IsError {
		t.Fatalf("object arguments rejected: %+v", result)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var output CompositeOutput
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatal(err)
	}
	if output.Result["branch_id"] != branchToolBranchID || output.Result["branch_name"] != "feature/ui" {
		t.Fatalf("branch result lost: %+v", output)
	}
}

func TestBuildCompositeRejectsMisspelledBranchBeforeBackend(t *testing.T) {
	s := branchToolServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected backend request %s", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	})
	s.mcpServer = mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "test"}, nil)
	s.registerManageBuildsTool()
	result := callServerTool(t, s, "manage_builds", map[string]any{
		"action": "list",
		"params": map[string]any{"app_id": branchToolAppID, "branch": "feature/ui"},
	})
	if !result.IsError {
		t.Fatalf("unknown branch selector accepted: %+v", result)
	}
}

func TestBuildCompositePreservesNullAndOmittedParams(t *testing.T) {
	s := branchToolServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/apps/" {
			t.Errorf("unexpected backend request %s", r.URL.Path)
		}
		respondBranchToolJSON(t, w, map[string]any{"items": []any{}, "total": 0})
	})
	s.mcpServer = mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "test"}, nil)
	s.registerManageBuildsTool()
	for _, input := range []map[string]any{{"action": "list"}, {"action": "list", "params": nil}} {
		result := callServerTool(t, s, "manage_builds", input)
		if result.IsError {
			t.Fatalf("compatible empty params rejected: %+v", result)
		}
	}
}
