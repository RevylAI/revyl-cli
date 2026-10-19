package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/revyl/cli/internal/api"
)

const fallbackChildJSON = `{"id":"child","test_id":"test","app_id":"app","app_name":"Example","branch_id":"branch","branch_name":"base","build_id":"build","branch_source":"default_branch","git_branch_name":"feature-login"}`

func TestWorkflowFallbackSSEEvents(t *testing.T) {
	workflow := `{"task":{"id":"workflow","status":"running"},"child_tasks":[` + fallbackChildJSON + `]}`
	for _, tc := range []struct{ event, data string }{
		{"initial_state", `{"running_workflows":[` + workflow + `]}`},
		{"workflow_updated", `{"workflow":` + workflow + `}`},
		{"workflow_completed", `{"workflow":` + workflow + `}`},
		{"workflow_failed", `{"task_id":"workflow","child_tasks":[` + fallbackChildJSON + `]}`},
		{"workflow_cancelled", `{"id":"workflow","child_tasks":[` + fallbackChildJSON + `]}`},
	} {
		t.Run(tc.event, func(t *testing.T) {
			got := (&Monitor{}).handleWorkflowEvent(SSEEvent{Event: tc.event, Data: json.RawMessage(tc.data)}, "workflow")
			if got == nil || len(got.ChildTasks) != 1 {
				t.Fatalf("status=%+v", got)
			}
			child := got.ChildTasks[0]
			if child.BranchSource != api.BranchSourceDefaultBranch || child.GitBranchName != "feature-login" || child.AppID != "app" || child.AppName != "Example" || child.BranchName != "base" || child.BuildID != "build" {
				t.Fatalf("child=%+v", child)
			}
		})
	}
}

func TestWorkflowFallbackSurvivesFlatTerminalEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: workflow_updated\ndata: {\"workflow\":{\"task\":{\"id\":\"workflow\",\"status\":\"running\"},\"child_tasks\":[%s]}}\n\n", fallbackChildJSON)
		fmt.Fprint(w, "event: workflow_failed\ndata: {\"task_id\":\"workflow\"}\n\n")
	}))
	defer server.Close()
	monitor := (&Monitor{apiKey: "key", timeout: 5, backendURL: server.URL})
	updates := 0
	got, err := monitor.monitorWorkflowSSE(context.Background(), "workflow", "definition", func(status *WorkflowStatus) {
		updates++
		if len(status.ChildTasks) != 1 || status.ChildTasks[0].BranchSource != api.BranchSourceDefaultBranch {
			t.Errorf("lost child: %+v", status)
		}
	})
	if err != nil || got.Status != "failed" || updates != 2 {
		t.Fatalf("status=%+v err=%v updates=%d", got, err, updates)
	}
}
