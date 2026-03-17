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
	if next.testListDeleteTarget.ID != "" {
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
	if next.testListDeleteTarget.ID != "" {
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

// --- Test list filter tests ---

func makeFilterTestItems() []TestItem {
	return []TestItem{
		{
			ID: "t1", Name: "Login Flow", Platform: "android",
			Tags:  []api.TestTag{{ID: "tag-smoke", Name: "smoke"}, {ID: "tag-reg", Name: "regression"}},
			AppID: "app-1", AppName: "MyApp",
			SyncStatus: "remote-only", Source: "remote",
		},
		{
			ID: "t2", Name: "Search Test", Platform: "ios",
			Tags:  []api.TestTag{{ID: "tag-smoke", Name: "smoke"}},
			AppID: "app-2", AppName: "OtherApp",
			SyncStatus: "remote-only", Source: "remote",
		},
		{
			ID: "t3", Name: "Checkout", Platform: "android",
			Tags:  nil,
			AppID: "", AppName: "",
			SyncStatus: "remote-only", Source: "remote",
		},
		{
			ID: "t4", Name: "Onboarding", Platform: "ios",
			Tags:  []api.TestTag{{ID: "tag-reg", Name: "regression"}},
			AppID: "app-1", AppName: "MyApp",
			SyncStatus: "remote-only", Source: "remote",
		},
	}
}

func TestApplyFilter_TextSearchMatchesNamePlatformTagApp(t *testing.T) {
	m := newHubModel("dev", false)
	m.tests = makeFilterTestItems()

	m.filterInput.SetValue("myapp")
	m.applyFilter()

	if len(m.filteredTests) != 2 {
		t.Fatalf("expected 2 tests matching app name 'myapp', got %d", len(m.filteredTests))
	}

	m.filterInput.SetValue("smoke")
	m.applyFilter()

	if len(m.filteredTests) != 2 {
		t.Fatalf("expected 2 tests matching tag 'smoke', got %d", len(m.filteredTests))
	}

	m.filterInput.SetValue("ios")
	m.applyFilter()

	if len(m.filteredTests) != 2 {
		t.Fatalf("expected 2 tests matching platform 'ios', got %d", len(m.filteredTests))
	}
}

func TestApplyFilter_TagFilterORLogic(t *testing.T) {
	m := newHubModel("dev", false)
	m.tests = makeFilterTestItems()

	m.filterTagIDs = map[string]bool{"tag-smoke": true}
	m.applyFilter()

	if len(m.filteredTests) != 2 {
		t.Fatalf("expected 2 tests with tag 'smoke', got %d", len(m.filteredTests))
	}

	m.filterTagIDs = map[string]bool{"tag-reg": true}
	m.applyFilter()

	if len(m.filteredTests) != 2 {
		t.Fatalf("expected 2 tests with tag 'regression', got %d", len(m.filteredTests))
	}

	m.filterTagIDs = map[string]bool{"tag-smoke": true, "tag-reg": true}
	m.applyFilter()

	if len(m.filteredTests) != 3 {
		t.Fatalf("expected 3 tests with either tag (OR logic), got %d", len(m.filteredTests))
	}
}

func TestApplyFilter_AppFilter(t *testing.T) {
	m := newHubModel("dev", false)
	m.tests = makeFilterTestItems()

	m.filterAppID = "app-1"
	m.applyFilter()

	if len(m.filteredTests) != 2 {
		t.Fatalf("expected 2 tests for app-1, got %d", len(m.filteredTests))
	}

	m.filterAppID = "none"
	m.applyFilter()

	if len(m.filteredTests) != 1 {
		t.Fatalf("expected 1 test with no app, got %d", len(m.filteredTests))
	}
	if m.filteredTests[0].ID != "t3" {
		t.Fatalf("expected Checkout (no app), got %s", m.filteredTests[0].Name)
	}

	m.filterAppID = ""
	m.applyFilter()

	if m.filteredTests != nil {
		t.Fatalf("expected nil filteredTests when no filters active, got %d items", len(m.filteredTests))
	}
}

func TestApplyFilter_CombinedFilters(t *testing.T) {
	m := newHubModel("dev", false)
	m.tests = makeFilterTestItems()

	m.filterTagIDs = map[string]bool{"tag-smoke": true}
	m.filterAppID = "app-1"
	m.applyFilter()

	if len(m.filteredTests) != 1 {
		t.Fatalf("expected 1 test with tag 'smoke' AND app-1, got %d", len(m.filteredTests))
	}
	if m.filteredTests[0].ID != "t1" {
		t.Fatalf("expected Login Flow, got %s", m.filteredTests[0].Name)
	}

	m.filterInput.SetValue("login")
	m.applyFilter()

	if len(m.filteredTests) != 1 {
		t.Fatalf("expected 1 test matching all three filters, got %d", len(m.filteredTests))
	}

	m.filterInput.SetValue("nonexistent")
	m.applyFilter()

	if len(m.filteredTests) != 0 {
		t.Fatalf("expected 0 tests with non-matching text search, got %d", len(m.filteredTests))
	}
}

func TestHandleTagPickerKey_ToggleAndClose(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestList
	m.filterTagPickerOn = true
	m.filterTags = []api.CLITagResponse{
		{ID: "tag-1", Name: "smoke"},
		{ID: "tag-2", Name: "regression"},
	}
	m.tests = makeFilterTestItems()

	// Toggle first tag on
	result, _ := m.handleTagPickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	next := result.(hubModel)
	if !next.filterTagIDs["tag-1"] {
		t.Fatal("expected tag-1 to be selected after space")
	}

	// Toggle it off
	result, _ = next.handleTagPickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	next = result.(hubModel)
	if next.filterTagIDs != nil && next.filterTagIDs["tag-1"] {
		t.Fatal("expected tag-1 to be deselected after second space")
	}

	// Close with esc
	next.filterTagPickerOn = true
	result, _ = next.handleTagPickerKey(tea.KeyMsg{Type: tea.KeyEsc})
	next = result.(hubModel)
	if next.filterTagPickerOn {
		t.Fatal("expected tag picker to close on esc")
	}
}

func TestHandleAppPickerKey_SelectAndClose(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestList
	m.filterAppPickerOn = true
	m.filterApps = []api.App{
		{ID: "app-1", Name: "MyApp", Platform: "android"},
		{ID: "app-2", Name: "OtherApp", Platform: "ios"},
	}
	m.tests = makeFilterTestItems()

	// Cursor starts at 0 = "All Apps", select it
	result, _ := m.handleAppPickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := result.(hubModel)
	if next.filterAppID != "" {
		t.Fatalf("expected empty filterAppID for 'All Apps', got %q", next.filterAppID)
	}
	if next.filterAppPickerOn {
		t.Fatal("expected app picker to close after enter")
	}

	// Move to "None" and select
	m.filterAppPickerOn = true
	m.filterAppCursor = 1
	result, _ = m.handleAppPickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	next = result.(hubModel)
	if next.filterAppID != "none" {
		t.Fatalf("expected 'none' filterAppID, got %q", next.filterAppID)
	}

	// Move to first app and select
	m.filterAppPickerOn = true
	m.filterAppCursor = 2
	result, _ = m.handleAppPickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	next = result.(hubModel)
	if next.filterAppID != "app-1" {
		t.Fatalf("expected 'app-1' filterAppID, got %q", next.filterAppID)
	}
}

func TestHandleTestListKey_ClearFilters(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestList
	m.tests = makeFilterTestItems()
	m.filterTagIDs = map[string]bool{"tag-smoke": true}
	m.filterAppID = "app-1"
	m.filterInput.SetValue("login")
	m.applyFilter()

	if len(m.filteredTests) != 1 {
		t.Fatalf("precondition: expected 1 filtered test, got %d", len(m.filteredTests))
	}

	result, _ := m.handleTestListKey(keyRune('c'))
	next := result.(hubModel)

	if next.filterTagIDs != nil {
		t.Fatal("expected filterTagIDs to be nil after clear")
	}
	if next.filterAppID != "" {
		t.Fatalf("expected empty filterAppID after clear, got %q", next.filterAppID)
	}
	if next.filterInput.Value() != "" {
		t.Fatalf("expected empty filter input after clear, got %q", next.filterInput.Value())
	}
	if next.filteredTests != nil {
		t.Fatal("expected nil filteredTests after clear")
	}
}

func TestRenderTestList_ShowsFilterBadgesAndTagsAndApp(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestList
	m.width = 80
	m.height = 40
	m.tests = makeFilterTestItems()
	m.filterTags = []api.CLITagResponse{
		{ID: "tag-smoke", Name: "smoke"},
		{ID: "tag-reg", Name: "regression"},
	}
	m.filterApps = []api.App{
		{ID: "app-1", Name: "MyApp", Platform: "android"},
	}

	// Render without filters: should show tags and app inline
	output := m.renderTestList()
	if !strings.Contains(output, "[smoke]") {
		t.Error("expected tag badge [smoke] in test list output")
	}
	if !strings.Contains(output, "{MyApp}") {
		t.Error("expected app badge {MyApp} in test list output")
	}

	// Set a tag filter and check badge
	m.filterTagIDs = map[string]bool{"tag-smoke": true}
	m.filterAppID = "app-1"
	m.applyFilter()
	output = m.renderTestList()
	if !strings.Contains(output, "Tags: smoke") {
		t.Error("expected filter badge 'Tags: smoke' in output")
	}
	if !strings.Contains(output, "App: MyApp") {
		t.Error("expected filter badge 'App: MyApp' in output")
	}
}

func TestRenderTestList_ShowsTagPickerOverlay(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestList
	m.width = 80
	m.height = 40
	m.tests = makeFilterTestItems()
	m.filterTagPickerOn = true
	m.filterTags = []api.CLITagResponse{
		{ID: "tag-smoke", Name: "smoke", TestCount: 5},
		{ID: "tag-reg", Name: "regression", TestCount: 3},
	}

	output := m.renderTestList()
	if !strings.Contains(output, "Filter by Tag") {
		t.Error("expected tag picker header in output")
	}
	if !strings.Contains(output, "smoke") {
		t.Error("expected tag name 'smoke' in picker")
	}
	if !strings.Contains(output, "(5)") {
		t.Error("expected test count in tag picker")
	}
}

func TestRenderTestList_ShowsAppPickerOverlay(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestList
	m.width = 80
	m.height = 40
	m.tests = makeFilterTestItems()
	m.filterAppPickerOn = true
	m.filterApps = []api.App{
		{ID: "app-1", Name: "MyApp", Platform: "android"},
	}

	output := m.renderTestList()
	if !strings.Contains(output, "Filter by App") {
		t.Error("expected app picker header in output")
	}
	if !strings.Contains(output, "All Apps") {
		t.Error("expected 'All Apps' option in picker")
	}
	if !strings.Contains(output, "None (no app)") {
		t.Error("expected 'None (no app)' option in picker")
	}
	if !strings.Contains(output, "MyApp") {
		t.Error("expected app name 'MyApp' in picker")
	}
}
