package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHandleTestDetailKey_ArrowNavigation(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestDetail
	m.selectedTestDetail = &TestDetail{ID: "test-1", Name: "Checkout"}

	nextModel, cmd := handleTestDetailKey(m, keyRune('j'))
	if cmd != nil {
		t.Fatalf("expected nil cmd when moving cursor, got %v", cmd)
	}
	next := nextModel.(hubModel)
	if next.testDetailCursor != 1 {
		t.Fatalf("expected cursor to move down to 1, got %d", next.testDetailCursor)
	}

	nextModel, cmd = handleTestDetailKey(next, keyRune('k'))
	if cmd != nil {
		t.Fatalf("expected nil cmd when moving cursor, got %v", cmd)
	}
	next = nextModel.(hubModel)
	if next.testDetailCursor != 0 {
		t.Fatalf("expected cursor to move up to 0, got %d", next.testDetailCursor)
	}
}

func TestHandleTestDetailKey_EnterExecutesSelectedAction(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestDetail
	m.selectedTestDetail = &TestDetail{ID: "test-1", Name: "Checkout"}
	m.testDetailCursor = 2 // Run history

	nextModel, cmd := handleTestDetailKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("expected nil cmd when no client is configured, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.currentView != viewTestRuns {
		t.Fatalf("expected enter to execute run history action, got view %v", next.currentView)
	}
	if next.reportReturnView != viewTestDetail {
		t.Fatalf("expected run history to set return view to test detail, got %v", next.reportReturnView)
	}
}

func TestHandleTestDetailKey_NumberExecutesAction(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestDetail
	m.selectedTestDetail = &TestDetail{ID: "test-1", Name: "Checkout"}

	nextModel, cmd := handleTestDetailKey(m, keyRune('9')) // Delete test
	if cmd != nil {
		t.Fatalf("expected nil cmd when opening delete confirmation, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if !next.testDetailConfirmDelete {
		t.Fatalf("expected numeric shortcut to trigger delete confirmation")
	}
	if next.testDetailCursor != 8 {
		t.Fatalf("expected cursor to move to selected numbered action, got %d", next.testDetailCursor)
	}
}

func TestHandleWorkflowDetailKey_ArrowAndEnterSelection(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewWorkflowDetail
	m.selectedWfDetail = &WorkflowItem{ID: "wf-1", Name: "Bug Bazaar"}
	m.wfDetailCursor = 1

	nextModel, cmd := handleWorkflowDetailKey(m, keyRune('j'))
	if cmd != nil {
		t.Fatalf("expected nil cmd when moving cursor, got %v", cmd)
	}
	next := nextModel.(hubModel)
	if next.wfDetailCursor != 2 {
		t.Fatalf("expected cursor to move down to 2, got %d", next.wfDetailCursor)
	}

	nextModel, cmd = handleWorkflowDetailKey(next, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("expected nil cmd when no client is configured, got %v", cmd)
	}
	next = nextModel.(hubModel)
	if next.currentView != viewWorkflowRuns {
		t.Fatalf("expected enter to execute selected workflow action, got %v", next.currentView)
	}
}

func TestHandleWorkflowDetailKey_NumberExecutesAction(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewWorkflowDetail
	m.selectedWfDetail = &WorkflowItem{ID: "wf-1", Name: "Bug Bazaar"}

	nextModel, cmd := handleWorkflowDetailKey(m, keyRune('4')) // Delete workflow
	if cmd != nil {
		t.Fatalf("expected nil cmd when opening delete confirmation, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if !next.wfConfirmDelete {
		t.Fatalf("expected numeric shortcut to trigger workflow delete confirmation")
	}
	if next.wfDetailCursor != 3 {
		t.Fatalf("expected cursor to move to selected numbered action, got %d", next.wfDetailCursor)
	}
}

func TestRenderDetailViews_ShowNumberedActions(t *testing.T) {
	testModel := newHubModel("dev", false)
	testModel.width = 100
	testModel.height = 24
	testModel.selectedTestDetail = &TestDetail{ID: "test-1", Name: "Checkout", Platform: "android"}

	testOut := renderTestDetail(testModel)
	if !strings.Contains(testOut, "[1]") || !strings.Contains(testOut, "[r]") {
		t.Fatalf("expected test detail to render numbered action labels, got: %s", testOut)
	}
	if !strings.Contains(testOut, "enter") || !strings.Contains(testOut, "jump") {
		t.Fatalf("expected test detail footer to include select/jump hints, got: %s", testOut)
	}

	workflowModel := newHubModel("dev", false)
	workflowModel.width = 100
	workflowModel.height = 24
	workflowModel.selectedWfDetail = &WorkflowItem{ID: "wf-1", Name: "Bug Bazaar"}

	workflowOut := renderWorkflowDetail(workflowModel)
	if !strings.Contains(workflowOut, "[1]") || !strings.Contains(workflowOut, "[r]") {
		t.Fatalf("expected workflow detail to render numbered action labels, got: %s", workflowOut)
	}
	if !strings.Contains(workflowOut, "enter") || !strings.Contains(workflowOut, "jump") {
		t.Fatalf("expected workflow detail footer to include select/jump hints, got: %s", workflowOut)
	}
}
