package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/revyl/cli/internal/api"
)

func TestWorkflowExecutionFallbackVisibleWhileRunningAndAfterFailure(t *testing.T) {
	for _, done := range []bool{false, true} {
		model := hubModel{width: 80, height: 24, wfExecDone: done, wfExecStartTime: time.Now(), wfExecStatus: &api.CLIWorkflowStatusResponse{
			Status: "failed", TotalTests: 3, ChildTasks: []api.CLIWorkflowChildExecution{
				{AppID: "ios", AppName: "Workflow Swift Fixture October 2026", BranchID: "base", BranchName: "base", BranchSource: api.BranchSourceDefaultBranch, GitBranchName: "feat"},
				{AppID: "ios", AppName: "Workflow Swift Fixture October 2026", BranchID: "base", BranchName: "base", BranchSource: api.BranchSourceDefaultBranch, GitBranchName: "feat"},
				{AppID: "android", AppName: "Workflow Expo Fixture October 2026", BranchID: "main", BranchName: "main", BranchSource: api.BranchSourceDefaultBranch, GitBranchName: "feat"},
				{AppID: "strict", AppName: "Strict Android", BranchID: "saved", BranchName: "saved", BranchSource: api.BranchSourceExplicitBranch},
			},
		}}
		rendered := renderWorkflowExecution(model)
		flat := strings.Join(strings.Fields(rendered), " ")
		if strings.Count(rendered, "\n") > 24 {
			t.Fatalf("80x24 overflow: %s", rendered)
		}
		if !strings.Contains(flat, `on "Workflow Expo Fixture October 2026"`) || (done && !strings.Contains(flat, "open report")) || (!done && !strings.Contains(flat, "cancel")) {
			t.Fatalf("second app or footer absent: %s", rendered)
		}
		if strings.Count(flat, `has no usable build on "Workflow Swift Fixture October 2026"`) != 1 || !strings.Contains(flat, `using the app's default branch "base"`) || strings.Contains(flat, `"Strict Android"`) {
			t.Fatalf("notice absent/duplicated: %s", rendered)
		}
		for _, line := range strings.Split(rendered, "\n") {
			if lipgloss.Width(line) > 80 {
				t.Fatalf("clipped notice line: %q", line)
			}
		}
	}
}
