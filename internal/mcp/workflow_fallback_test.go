package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestMCPWorkflowFallbackResults(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprint(fallback), func(t *testing.T) {
			s := branchToolServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/workflows/get_workflow_info":
					respondBranchToolJSON(t, w, map[string]any{"id": branchToolWorkflowID, "name": "Smoke"})
				case "/api/v1/workflow-executions":
					respondBranchToolJSON(t, w, map[string]any{"task_id": "workflow-task"})
				case "/api/v1/monitor/stream/unified":
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "event: workflow_completed\ndata: {\"task_id\":\"workflow-task\"}\n\n")
				case "/api/v1/workflows/share/unified-report":
					respondBranchToolJSON(t, w, map[string]any{"child_tasks": []any{map[string]any{"task_id": "child", "app_id": branchToolAppID, "app_name": "Example", "branch_id": branchToolBranchID, "branch_name": "base", "build_id": branchToolBuildID, "branch_source": workflowChildBranchSource(fallback), "git_branch_name": "feature-login"}}})
				default:
					t.Errorf("unexpected path: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			})
			_, out, err := s.handleRunWorkflow(context.Background(), &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{}}, RunWorkflowInput{WorkflowName: branchToolWorkflowID})
			if err != nil || len(out.Tests) != 1 {
				t.Fatalf("output=%+v err=%v", out, err)
			}
			child := out.Tests[0]
			if child.BranchSource != workflowChildBranchSource(fallback) || child.GitBranchName != "feature-login" || child.AppName != "Example" || child.AppID != branchToolAppID || child.BranchName != "base" || child.BuildID != branchToolBuildID {
				t.Fatalf("child=%+v", child)
			}
			data, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), `"branch_source":"`+string(workflowChildBranchSource(fallback))+`"`) {
				t.Fatalf("branch source not serialized: %s", data)
			}
		})
	}
}

func workflowChildBranchSource(fallback bool) api.BranchSource {
	if fallback {
		return api.BranchSourceDefaultBranch
	}
	return api.BranchSourceGitBranch
}
