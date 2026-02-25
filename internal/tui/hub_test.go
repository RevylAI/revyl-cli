package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/api"
)

func keyRune(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func TestHandleTestListKey_BrowseDeleteStartsConfirm(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestList
	m.tests = []TestItem{{ID: "test-1", Name: "Add to Cart"}}

	nextModel, cmd := m.handleTestListKey(keyRune('x'))
	if cmd != nil {
		t.Fatalf("expected nil cmd when opening delete confirmation, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if !next.testListConfirmDelete {
		t.Fatalf("expected delete confirmation to be active")
	}
	if next.testListDeleteTarget.Name != "Add to Cart" {
		t.Fatalf("expected selected test to be delete target, got %q", next.testListDeleteTarget.Name)
	}
}

func TestHandleTestListKey_DeleteStartsConfirm(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestList
	m.tests = []TestItem{{ID: "test-1", Name: "Add to Cart"}}

	nextModel, cmd := m.handleTestListKey(keyRune('x'))
	if cmd != nil {
		t.Fatalf("expected nil cmd when opening delete confirmation, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if !next.testListConfirmDelete {
		t.Fatalf("expected delete confirmation to be enabled")
	}
}

func TestHandleTestListKey_DeleteConfirmYStartsDelete(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestList
	m.client = &api.Client{}
	m.testListConfirmDelete = true
	m.testListDeleteTarget = TestItem{ID: "test-1", Name: "Add to Cart"}

	nextModel, cmd := m.handleTestListKey(keyRune('y'))
	if cmd == nil {
		t.Fatalf("expected delete command when confirming with y")
	}

	next := nextModel.(hubModel)
	if next.testListConfirmDelete {
		t.Fatalf("expected delete confirmation to be cleared after confirm")
	}
	if next.testListDeleteTarget != (TestItem{}) {
		t.Fatalf("expected delete target to be cleared after confirm")
	}
	if !next.loading {
		t.Fatalf("expected loading=true while delete command runs")
	}
}

func TestHandleTestListKey_DeleteConfirmCancel(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestList
	m.testListConfirmDelete = true
	m.testListDeleteTarget = TestItem{ID: "test-1", Name: "Add to Cart"}

	nextModel, cmd := m.handleTestListKey(keyRune('n'))
	if cmd != nil {
		t.Fatalf("expected nil cmd when canceling delete, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.testListConfirmDelete {
		t.Fatalf("expected delete confirmation to be canceled")
	}
	if next.testListDeleteTarget != (TestItem{}) {
		t.Fatalf("expected delete target to be cleared after cancel")
	}
}

func TestHandleTestListKey_DeleteConfirmBlocksOtherActions(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestList
	m.tests = []TestItem{{ID: "test-1", Name: "Add to Cart"}}
	m.testListConfirmDelete = true
	m.testListDeleteTarget = TestItem{ID: "test-1", Name: "Add to Cart"}

	nextModel, cmd := m.handleTestListKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("expected nil cmd while delete confirmation is active, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.currentView != viewTestList {
		t.Fatalf("expected to remain on test list during delete confirmation, got %v", next.currentView)
	}
}

func TestRenderTestList_BrowseIncludesDeleteHint(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestList
	m.width = 100
	m.height = 24
	m.tests = []TestItem{{ID: "test-1", Name: "Add to Cart"}}

	out := m.renderTestList()
	if !strings.Contains(out, "delete") {
		t.Fatalf("expected browse test list help to include delete hint, got: %s", out)
	}
}

func TestRenderTestList_DeleteConfirmPrompt(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestList
	m.width = 100
	m.height = 24
	m.tests = []TestItem{{ID: "test-1", Name: "Add to Cart"}}
	m.testListConfirmDelete = true
	m.testListDeleteTarget = TestItem{ID: "test-1", Name: "Add to Cart"}

	out := m.renderTestList()
	if !strings.Contains(out, `Delete test "Add to Cart"? (y/n)`) {
		t.Fatalf("expected delete confirmation prompt, got: %s", out)
	}
}

func TestUpdate_TestDeletedMsgSuccessFromDetail(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestDetail
	m.selectedTestDetail = &TestDetail{Name: "Add to Cart"}
	m.client = &api.Client{}
	m.testListConfirmDelete = true
	m.testListDeleteTarget = TestItem{ID: "test-1", Name: "Add to Cart"}

	nextModel, cmd := m.Update(TestDeletedMsg{Name: "Add to Cart", ID: "test-1"})
	if cmd == nil {
		t.Fatalf("expected refresh command after successful delete")
	}

	next := nextModel.(hubModel)
	if next.currentView != viewTestList {
		t.Fatalf("expected to return to test list after delete, got %v", next.currentView)
	}
	if next.selectedTestDetail != nil {
		t.Fatalf("expected selected test detail to be cleared")
	}
	if next.testListConfirmDelete {
		t.Fatalf("expected list delete confirmation state to be cleared")
	}
}

func TestUpdate_TestDeletedMsgErrorSetsErr(t *testing.T) {
	m := newHubModel("dev", false)
	deleteErr := errors.New("boom")

	nextModel, cmd := m.Update(TestDeletedMsg{Err: deleteErr})
	if cmd != nil {
		t.Fatalf("expected nil cmd on delete error, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.err == nil || next.err.Error() != deleteErr.Error() {
		t.Fatalf("expected delete error to be surfaced, got %v", next.err)
	}
}

func TestUpdate_TestRenamedMsgSuccessUpdatesDetailState(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestDetail
	m.selectedTestDetail = &TestDetail{ID: "test-1", Name: "Checkout", Platform: "ios"}
	m.testRenameActive = true
	m.testRenameConfirm = true
	m.testRenameLoading = true
	m.testRenameInput.SetValue("Checkout v2")

	nextModel, cmd := m.Update(TestRenamedMsg{
		ID:      "test-1",
		OldName: "Checkout",
		NewName: "Checkout v2",
		Summary: "Remote renamed: Checkout -> Checkout v2",
	})
	if cmd != nil {
		t.Fatalf("expected nil cmd without client, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.selectedTestDetail == nil || next.selectedTestDetail.Name != "Checkout v2" {
		t.Fatalf("expected selected detail name to update, got %#v", next.selectedTestDetail)
	}
	if next.testRenameActive || next.testRenameConfirm || next.testRenameLoading {
		t.Fatalf("expected rename state to reset after success")
	}
	if next.testSyncResult == "" {
		t.Fatalf("expected rename summary to be shown in result panel")
	}
}

func TestUpdate_TestRenamedMsgErrorKeepsRenameFlowActive(t *testing.T) {
	m := newHubModel("dev", false)
	m.testRenameActive = true
	m.testRenameConfirm = true
	m.testRenameLoading = true

	nextModel, cmd := m.Update(TestRenamedMsg{Err: errors.New("rename failed")})
	if cmd == nil {
		t.Fatalf("expected blink cmd on rename error")
	}

	next := nextModel.(hubModel)
	if !next.testRenameActive {
		t.Fatalf("expected rename flow to remain active")
	}
	if next.testRenameConfirm {
		t.Fatalf("expected confirmation mode to clear after rename error")
	}
	if next.testRenameError == "" {
		t.Fatalf("expected rename error message to be stored")
	}
}

func TestQuickActions_RunItemsRemoved(t *testing.T) {
	for _, action := range quickActions {
		if action.Key == "run" || action.Key == "run_workflow" {
			t.Fatalf("expected run quick actions to be removed, found key %q", action.Key)
		}
	}
}

func TestHandleDashboardKey_SlashOpensBrowseFilter(t *testing.T) {
	m := newHubModel("dev", false)
	m.client = &api.Client{}
	m.apiKey = "token"
	m.currentView = viewDashboard

	nextModel, cmd := m.handleDashboardKey(keyRune('/'))
	if cmd == nil {
		t.Fatalf("expected blink command when opening filter")
	}

	next := nextModel.(hubModel)
	if next.currentView != viewTestList {
		t.Fatalf("expected slash to navigate to test list, got %v", next.currentView)
	}
	if !next.filterMode {
		t.Fatalf("expected slash to enable filter mode")
	}
}

func TestHandleWorkflowListKey_RunStartsExecution(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewWorkflowList
	m.client = &api.Client{}
	m.wfItems = []WorkflowItem{{ID: "wf-1", Name: "Smoke"}}
	m.wfCursor = 0

	nextModel, cmd := handleWorkflowListKey(m, keyRune('r'))
	if cmd == nil {
		t.Fatalf("expected run command when pressing r on workflow list")
	}

	next := nextModel.(hubModel)
	if next.currentView != viewWorkflowExecution {
		t.Fatalf("expected workflow execution view, got %v", next.currentView)
	}
	if next.selectedWfDetail == nil || next.selectedWfDetail.ID != "wf-1" {
		t.Fatalf("expected selected workflow detail to be set")
	}
	if next.wfExecReturnView != viewWorkflowList {
		t.Fatalf("expected execution return view to be workflow list, got %v", next.wfExecReturnView)
	}
}

func TestHandleWorkflowExecKey_DoneEscReturnsToConfiguredView(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewWorkflowExecution
	m.wfExecDone = true
	m.wfExecReturnView = viewWorkflowList

	nextModel, cmd := handleWorkflowExecKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("expected nil cmd on esc when execution done, got %v", cmd)
	}
	next := nextModel.(hubModel)
	if next.currentView != viewWorkflowList {
		t.Fatalf("expected esc to return to workflow list, got %v", next.currentView)
	}
}
