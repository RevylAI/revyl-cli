package tui

import (
	"context"
	"errors"
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
	m.testRuns = []api.CLIEnhancedHistoryItem{{
		ID: "hist-123",
		EnhancedTask: &api.CLIEnhancedTask{
			ID: "task-123",
		},
	}}
	m.testRunCursor = 0

	url := m.selectedTestRunReportURL()
	if !strings.Contains(url, "/tests/report?taskId=task-123") {
		t.Fatalf("expected test report url to include task id, got %q", url)
	}
}

func TestSelectedTestRunTaskID_PrefersEnhancedTaskID(t *testing.T) {
	m := newHubModel("dev", false)
	m.testRuns = []api.CLIEnhancedHistoryItem{{
		ID: "hist-123",
		EnhancedTask: &api.CLIEnhancedTask{
			ID: "task-123",
		},
	}}
	m.testRunCursor = 0

	taskID := m.selectedTestRunTaskID()
	if taskID != "task-123" {
		t.Fatalf("expected enhanced task id to be preferred, got %q", taskID)
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

func TestHandleTestRunsKey_ShowJSONCommand(t *testing.T) {
	m := newHubModel("dev", false)
	m.width = 100
	m.height = 24
	m.currentView = viewTestRuns
	m.selectedTestName = "Checkout"
	m.testRuns = []api.CLIEnhancedHistoryItem{{
		ID:     "hist-456",
		Status: "passed",
		EnhancedTask: &api.CLIEnhancedTask{
			ID: "task-456",
		},
	}}
	m.testRunCursor = 0

	previousClipboard := writeClipboardText
	defer func() {
		writeClipboardText = previousClipboard
	}()

	copied := ""
	writeClipboardText = func(text string) error {
		copied = text
		return nil
	}

	nextModel, cmd := m.handleTestRunsKey(keyRune('y'))
	if cmd == nil {
		t.Fatalf("expected command copy cmd when showing command preview")
	}

	next := nextModel.(hubModel)
	if !next.reportJSONCommand {
		t.Fatalf("expected JSON command preview to be visible")
	}

	out := next.renderTestRuns()
	if !strings.Contains(out, "revyl test report task-456 --json") {
		t.Fatalf("expected rendered output to include report JSON command, got: %s", out)
	}
	if !strings.Contains(out, "copy report JSON") || !strings.Contains(out, "show/copy command") {
		t.Fatalf("expected rendered output to include JSON action hints, got: %s", out)
	}

	msg := cmd()
	updatedModel, _ := next.Update(msg)
	updated := updatedModel.(hubModel)

	if copied != "revyl test report task-456 --json" {
		t.Fatalf("expected command to be copied to clipboard, got %q", copied)
	}
	if updated.reportJSONError {
		t.Fatalf("expected command copy to succeed")
	}
	if updated.reportJSONStatus != "Copied report command to clipboard" {
		t.Fatalf("expected command copy success notice, got %q", updated.reportJSONStatus)
	}
}

func TestHandleTestRunsKey_ShowJSONCommandIncludesDevFlagInDevMode(t *testing.T) {
	m := newHubModel("dev", true)
	m.width = 100
	m.height = 24
	m.currentView = viewTestRuns
	m.selectedTestName = "Checkout"
	m.testRuns = []api.CLIEnhancedHistoryItem{{
		ID:     "hist-456",
		Status: "passed",
		EnhancedTask: &api.CLIEnhancedTask{
			ID: "task-456",
		},
	}}
	m.testRunCursor = 0

	previousClipboard := writeClipboardText
	defer func() {
		writeClipboardText = previousClipboard
	}()

	copied := ""
	writeClipboardText = func(text string) error {
		copied = text
		return nil
	}

	nextModel, cmd := m.handleTestRunsKey(keyRune('y'))
	if cmd == nil {
		t.Fatalf("expected command copy cmd in dev mode")
	}

	out := nextModel.(hubModel).renderTestRuns()
	if !strings.Contains(out, "revyl test report task-456 --json --dev") {
		t.Fatalf("expected rendered output to include dev report command, got: %s", out)
	}

	msg := cmd()
	updatedModel, _ := nextModel.(hubModel).Update(msg)
	updated := updatedModel.(hubModel)

	if copied != "revyl test report task-456 --json --dev" {
		t.Fatalf("expected dev command to be copied to clipboard, got %q", copied)
	}
	if updated.reportJSONError {
		t.Fatalf("expected dev command copy to succeed")
	}
}

func TestHandleTestRunsKey_ShowJSONCommandUsesCurrentExecutableName(t *testing.T) {
	m := newHubModel("dev", true)
	m.width = 100
	m.height = 24
	m.currentView = viewTestRuns
	m.selectedTestName = "Checkout"
	m.testRuns = []api.CLIEnhancedHistoryItem{{
		ID:     "hist-456",
		Status: "passed",
		EnhancedTask: &api.CLIEnhancedTask{
			ID: "task-456",
		},
	}}
	m.testRunCursor = 0

	previousClipboard := writeClipboardText
	previousCommandName := currentReportCommandName
	defer func() {
		writeClipboardText = previousClipboard
		currentReportCommandName = previousCommandName
	}()

	copied := ""
	writeClipboardText = func(text string) error {
		copied = text
		return nil
	}
	currentReportCommandName = func() string {
		return "revyl-mahler"
	}

	nextModel, cmd := m.handleTestRunsKey(keyRune('y'))
	if cmd == nil {
		t.Fatalf("expected command copy cmd when showing command preview")
	}

	out := nextModel.(hubModel).renderTestRuns()
	if !strings.Contains(out, "revyl-mahler test report task-456 --json --dev") {
		t.Fatalf("expected rendered output to include executable-specific report command, got: %s", out)
	}

	msg := cmd()
	updatedModel, _ := nextModel.(hubModel).Update(msg)
	updated := updatedModel.(hubModel)

	if copied != "revyl-mahler test report task-456 --json --dev" {
		t.Fatalf("expected executable-specific command to be copied to clipboard, got %q", copied)
	}
	if updated.reportJSONError {
		t.Fatalf("expected executable-specific command copy to succeed")
	}
}

func TestHandleTestRunsKey_ShowJSONCommandWrapsFullCommand(t *testing.T) {
	m := newHubModel("dev", false)
	m.width = 50
	m.height = 24
	m.currentView = viewTestRuns
	m.selectedTestName = "Checkout"
	m.testRuns = []api.CLIEnhancedHistoryItem{{
		ID:     "hist-680feff6",
		Status: "passed",
		EnhancedTask: &api.CLIEnhancedTask{
			ID: "680feff6-1512-404d-b636-1b097072e88c",
		},
	}}
	m.testRunCursor = 0

	previousClipboard := writeClipboardText
	defer func() {
		writeClipboardText = previousClipboard
	}()
	writeClipboardText = func(text string) error {
		return nil
	}

	nextModel, cmd := m.handleTestRunsKey(keyRune('y'))
	if cmd == nil {
		t.Fatalf("expected command copy cmd when showing wrapped preview")
	}

	out := nextModel.(hubModel).renderTestRuns()
	if !strings.Contains(out, "json: revyl test report") {
		t.Fatalf("expected wrapped command preview label, got: %s", out)
	}
	if !strings.Contains(out, "680feff6-1512-404d-b636-1b097072e88c") {
		t.Fatalf("expected full task id in wrapped command preview, got: %s", out)
	}
	if !strings.Contains(out, "--json") {
		t.Fatalf("expected json flag in wrapped command preview, got: %s", out)
	}
}

func TestHandleTestRunsKey_ShowJSONCommandClipboardFailureShowsNotice(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestRuns
	m.selectedTestName = "Checkout"
	m.testRuns = []api.CLIEnhancedHistoryItem{{
		ID:     "hist-456",
		Status: "passed",
		EnhancedTask: &api.CLIEnhancedTask{
			ID: "task-456",
		},
	}}
	m.testRunCursor = 0

	previousClipboard := writeClipboardText
	defer func() {
		writeClipboardText = previousClipboard
	}()

	writeClipboardText = func(text string) error {
		return errors.New("clipboard unavailable")
	}

	nextModel, cmd := m.handleTestRunsKey(keyRune('y'))
	if cmd == nil {
		t.Fatalf("expected command copy cmd when showing command preview")
	}

	next := nextModel.(hubModel)
	if !next.reportJSONCommand {
		t.Fatalf("expected command preview to remain visible on copy failure")
	}

	msg := cmd()
	updatedModel, _ := next.Update(msg)
	updated := updatedModel.(hubModel)

	if !updated.reportJSONError {
		t.Fatalf("expected command copy failure to mark error state")
	}
	if !strings.Contains(updated.reportJSONStatus, "failed to copy report command") {
		t.Fatalf("expected clipboard failure notice, got %q", updated.reportJSONStatus)
	}
}

func TestHandleTestRunsKey_CopyJSONFetchesAndShowsSuccess(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestRuns
	m.client = &api.Client{}
	m.testRuns = []api.CLIEnhancedHistoryItem{{
		ID:     "hist-123",
		Status: "passed",
		EnhancedTask: &api.CLIEnhancedTask{
			ID: "task-123",
		},
	}}
	m.testRunCursor = 0

	previousFetch := fetchReportContextForTUI
	previousClipboard := writeClipboardText
	defer func() {
		fetchReportContextForTUI = previousFetch
		writeClipboardText = previousClipboard
	}()

	fetchCalled := false
	copied := ""
	fetchReportContextForTUI = func(
		ctx context.Context,
		client *api.Client,
		taskID string,
	) (*api.CLIReportContextEnvelope, error) {
		if client == nil {
			t.Fatal("expected API client to be passed to report fetcher")
		}
		if ctx == nil {
			t.Fatal("expected context to be passed to report fetcher")
		}
		fetchCalled = true
		if taskID != "task-123" {
			t.Fatalf("expected selected task id, got %q", taskID)
		}
		return &api.CLIReportContextEnvelope{Raw: []byte(`{"id":"report-1"}`)}, nil
	}
	writeClipboardText = func(text string) error {
		copied = text
		return nil
	}

	nextModel, cmd := m.handleTestRunsKey(keyRune('c'))
	if cmd == nil {
		t.Fatalf("expected report JSON fetch command")
	}

	next := nextModel.(hubModel)
	if !next.reportJSONPending {
		t.Fatalf("expected report JSON action to be pending")
	}
	if next.reportJSONStatus != "Fetching report JSON..." {
		t.Fatalf("expected loading status, got %q", next.reportJSONStatus)
	}

	msg := cmd()
	updatedModel, _ := next.Update(msg)
	updated := updatedModel.(hubModel)

	if !fetchCalled {
		t.Fatalf("expected report JSON fetch to be called")
	}
	if copied != `{"id":"report-1"}` {
		t.Fatalf("expected raw report JSON to be copied, got %q", copied)
	}
	if updated.reportJSONPending {
		t.Fatalf("expected pending state to clear after success")
	}
	if updated.reportJSONError {
		t.Fatalf("expected success status after copy")
	}
	if !strings.Contains(updated.reportJSONStatus, "Copied report JSON to clipboard for task-123") {
		t.Fatalf("expected success notice, got %q", updated.reportJSONStatus)
	}
}

func TestHandleTestRunsKey_CopyJSONFailureShowsFallbackCommand(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestRuns
	m.client = &api.Client{}
	m.testRuns = []api.CLIEnhancedHistoryItem{{
		ID:     "hist-999",
		Status: "failed",
		EnhancedTask: &api.CLIEnhancedTask{
			ID: "task-999",
		},
	}}
	m.testRunCursor = 0

	previousFetch := fetchReportContextForTUI
	defer func() {
		fetchReportContextForTUI = previousFetch
	}()

	fetchReportContextForTUI = func(
		ctx context.Context,
		client *api.Client,
		taskID string,
	) (*api.CLIReportContextEnvelope, error) {
		if client == nil {
			t.Fatal("expected API client to be passed to report fetcher")
		}
		if ctx == nil {
			t.Fatal("expected context to be passed to report fetcher")
		}
		return nil, errors.New("backend unavailable")
	}

	nextModel, cmd := m.handleTestRunsKey(keyRune('c'))
	if cmd == nil {
		t.Fatalf("expected report JSON fetch command")
	}

	msg := cmd()
	updatedModel, _ := nextModel.(hubModel).Update(msg)
	updated := updatedModel.(hubModel)

	if !updated.reportJSONError {
		t.Fatalf("expected error state after failed JSON fetch")
	}
	if !strings.Contains(updated.reportJSONStatus, "revyl test report task-999 --json") {
		t.Fatalf("expected fallback command in error notice, got %q", updated.reportJSONStatus)
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
