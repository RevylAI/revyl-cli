package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/api"
)

func TestQuickActionsDoesNotIncludeReports(t *testing.T) {
	for _, action := range quickActions {
		if action.Key == "reports" {
			t.Fatalf("expected reports quick action to be removed")
		}
	}
}

func TestHandleTestDetailKey_RunHistoryOpensTestRuns(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestDetail
	m.client = &api.Client{}
	m.selectedTestDetail = &TestDetail{ID: "test-1", Name: "Checkout"}

	nextModel, cmd := handleTestDetailKey(m, keyRune('h'))
	if cmd == nil {
		t.Fatalf("expected fetch history command when opening test run history")
	}

	next := nextModel.(hubModel)
	if next.currentView != viewTestRuns {
		t.Fatalf("expected transition to test runs view, got %v", next.currentView)
	}
	if next.selectedTestID != "test-1" || next.selectedTestName != "Checkout" {
		t.Fatalf("expected selected test context to be set, got id=%q name=%q", next.selectedTestID, next.selectedTestName)
	}
	if next.reportReturnView != viewTestDetail {
		t.Fatalf("expected report return view to be test detail, got %v", next.reportReturnView)
	}
	if !next.reportLoading {
		t.Fatalf("expected report loading state to be enabled")
	}
}

func TestHandleWorkflowDetailKey_RunHistorySetsReturnView(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewWorkflowDetail
	m.client = &api.Client{}
	m.selectedWfDetail = &WorkflowItem{ID: "wf-1", Name: "Smoke"}

	nextModel, cmd := handleWorkflowDetailKey(m, keyRune('h'))
	if cmd == nil {
		t.Fatalf("expected fetch history command when opening workflow run history")
	}

	next := nextModel.(hubModel)
	if next.currentView != viewWorkflowRuns {
		t.Fatalf("expected transition to workflow runs view, got %v", next.currentView)
	}
	if next.selectedWorkflowID != "wf-1" || next.selectedWorkflowName != "Smoke" {
		t.Fatalf("expected selected workflow context to be set, got id=%q name=%q", next.selectedWorkflowID, next.selectedWorkflowName)
	}
	if next.reportReturnView != viewWorkflowDetail {
		t.Fatalf("expected report return view to be workflow detail, got %v", next.reportReturnView)
	}
}

func TestHandleTestRunsKey_EscReturnsToDetail(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestRuns
	m.reportReturnView = viewTestDetail

	nextModel, cmd := m.handleTestRunsKey(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("expected nil cmd on esc, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.currentView != viewTestDetail {
		t.Fatalf("expected esc to return to test detail, got %v", next.currentView)
	}
}

func TestHandleWorkflowRunsKey_EscReturnsToDetail(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewWorkflowRuns
	m.reportReturnView = viewWorkflowDetail

	nextModel, cmd := m.handleWorkflowRunsKey(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("expected nil cmd on esc, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.currentView != viewWorkflowDetail {
		t.Fatalf("expected esc to return to workflow detail, got %v", next.currentView)
	}
}

func TestSelectedTestRunReportURL(t *testing.T) {
	m := newHubModel("dev", false)
	m.testRuns = []api.CLIEnhancedHistoryItem{{ID: "task-123"}}
	m.testRunCursor = 0

	url := m.selectedTestRunReportURL()
	if !strings.Contains(url, "/tests/report?taskId=task-123") {
		t.Fatalf("expected test report url to include task id, got %q", url)
	}
}

func TestSelectedWorkflowRunReportURL_UsesExecutionID(t *testing.T) {
	m := newHubModel("dev", false)
	m.workflowRuns = []api.CLIWorkflowStatusResponse{{
		ExecutionID: "exec-123",
		WorkflowID:  "wf-123",
	}}
	m.workflowRunCursor = 0

	url := m.selectedWorkflowRunReportURL()
	if !strings.Contains(url, "/workflows/report?taskId=exec-123") {
		t.Fatalf("expected workflow report url to use execution id, got %q", url)
	}
}

func TestRenderTestRuns_ShowsLinkHint(t *testing.T) {
	m := newHubModel("dev", false)
	m.width = 100
	m.height = 24
	m.selectedTestName = "Checkout"
	m.testRuns = []api.CLIEnhancedHistoryItem{{ID: "task-456", Status: "passed"}}
	m.testRunCursor = 0

	out := m.renderTestRuns()
	if !strings.Contains(out, "link: ") || !strings.Contains(out, "task-456") {
		t.Fatalf("expected test runs footer to show selected report link, got: %s", out)
	}
}

func TestRenderWorkflowRuns_ShowsLinkHint(t *testing.T) {
	m := newHubModel("dev", false)
	m.width = 100
	m.height = 24
	m.selectedWorkflowName = "Smoke"
	m.workflowRuns = []api.CLIWorkflowStatusResponse{{
		ExecutionID:    "exec-789",
		Status:         "completed",
		CompletedTests: 1,
		TotalTests:     1,
	}}
	m.workflowRunCursor = 0

	out := m.renderWorkflowRuns()
	if !strings.Contains(out, "link: ") || !strings.Contains(out, "/workflows/report?taskId=") {
		t.Fatalf("expected workflow runs footer to show selected report link, got: %s", out)
	}
}
