package tui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
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

func TestHandleTestDetailKey_RenameShortcutOpensOverlay(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestDetail
	m.selectedTestDetail = &TestDetail{ID: "test-1", Name: "Checkout"}

	nextModel, cmd := handleTestDetailKey(m, keyRune('n'))
	if cmd == nil {
		t.Fatalf("expected blink cmd when entering rename mode")
	}

	next := nextModel.(hubModel)
	if !next.testRenameActive {
		t.Fatalf("expected rename overlay to activate")
	}
	if next.testRenameInput.Value() != "Checkout" {
		t.Fatalf("expected rename input to seed current name, got %q", next.testRenameInput.Value())
	}
}

func TestHandleTestDetailKey_RenameEnterTransitionsToConfirm(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewTestDetail
	m.selectedTestDetail = &TestDetail{ID: "test-1", Name: "Checkout"}
	m.testRenameActive = true
	m.testRenameInput.SetValue("Checkout v2")

	nextModel, cmd := handleTestDetailKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("expected nil cmd when preparing rename confirmation, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if !next.testRenameConfirm {
		t.Fatalf("expected rename confirmation mode after enter")
	}
	if next.testRenameTargetName != "Checkout v2" {
		t.Fatalf("expected rename target to be captured, got %q", next.testRenameTargetName)
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
	if !strings.Contains(testOut, "[1]") {
		t.Fatalf("expected test detail to render numbered action labels, got: %s", testOut)
	}
	if strings.Contains(testOut, "[r]") {
		t.Fatalf("expected test detail to hide letter shortcut labels, got: %s", testOut)
	}
	if !strings.Contains(testOut, "enter") || !strings.Contains(testOut, "jump") {
		t.Fatalf("expected test detail footer to include select/jump hints, got: %s", testOut)
	}

	workflowModel := newHubModel("dev", false)
	workflowModel.width = 100
	workflowModel.height = 24
	workflowModel.selectedWfDetail = &WorkflowItem{ID: "wf-1", Name: "Bug Bazaar"}

	workflowOut := renderWorkflowDetail(workflowModel)
	if !strings.Contains(workflowOut, "[1]") {
		t.Fatalf("expected workflow detail to render numbered action labels, got: %s", workflowOut)
	}
	if strings.Contains(workflowOut, "[r]") {
		t.Fatalf("expected workflow detail to hide letter shortcut labels, got: %s", workflowOut)
	}
	if !strings.Contains(workflowOut, "enter") || !strings.Contains(workflowOut, "jump") {
		t.Fatalf("expected workflow detail footer to include select/jump hints, got: %s", workflowOut)
	}
}

func TestSyncTestActionCmd_PullRemoteOnlyCreatesConfigAndLocalTest(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-live","email":"test@example.com","concurrency_limit":1}`))
		case r.URL.Path == "/api/v1/tests/get_test_by_id/test-1":
			_, _ = w.Write([]byte(`{"id":"test-1","name":"Checkout Flow","platform":"ios","tasks":[],"version":7}`))
		case r.URL.Path == "/api/v1/tests/tags/tests/test-1":
			_, _ = w.Write([]byte(`[]`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/variables/custom/read_variables"),
			strings.HasPrefix(r.URL.Path, "/api/v1/variables/app_launch_env/read"):
			_, _ = w.Write([]byte(`{"result":[]}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/tests/scripts"):
			_, _ = w.Write([]byte(`{"scripts":[],"count":0}`))
		case r.URL.Path == "/api/v1/modules/list":
			_, _ = w.Write([]byte(`{"message":"ok","result":[]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	msgAny := syncTestActionCmd(api.NewClientWithBaseURL("token", srv.URL), "pull", "Checkout Flow", "test-1", false)()
	msg, ok := msgAny.(TestSyncActionMsg)
	if !ok {
		t.Fatalf("message type = %T, want TestSyncActionMsg", msgAny)
	}
	if msg.Err != nil {
		t.Fatalf("msg.Err = %v, want nil", msg.Err)
	}
	if !strings.Contains(msg.Result, "Pulled") {
		t.Fatalf("msg.Result = %q, want pull success", msg.Result)
	}

	cfg, err := config.LoadProjectConfig(filepath.Join(tempDir, ".revyl", "config.yaml"))
	if err != nil {
		t.Fatalf("LoadProjectConfig() error = %v", err)
	}
	if cfg.Project.OrgID != "org-live" {
		t.Fatalf("cfg.Project.OrgID = %q, want org-live", cfg.Project.OrgID)
	}
	gotID, idErr := config.GetLocalTestRemoteID(filepath.Join(tempDir, ".revyl", "tests"), "checkout-flow")
	if idErr != nil {
		t.Fatalf("GetLocalTestRemoteID() error = %v", idErr)
	}
	if gotID != "test-1" {
		t.Fatalf("checkout-flow remote_id = %q, want test-1", gotID)
	}

	localTest, err := config.LoadLocalTest(filepath.Join(tempDir, ".revyl", "tests", "checkout-flow.yaml"))
	if err != nil {
		t.Fatalf("LoadLocalTest() error = %v", err)
	}
	if localTest.Meta.RemoteID != "test-1" {
		t.Fatalf("localTest.Meta.RemoteID = %q, want test-1", localTest.Meta.RemoteID)
	}
	if localTest.Test.Metadata.Name != "Checkout Flow" {
		t.Fatalf("local test name = %q, want Checkout Flow", localTest.Test.Metadata.Name)
	}
}

func TestSyncTestActionCmd_PushConflictReturnsError(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	localTest := &config.LocalTest{
		Meta: config.TestMeta{
			RemoteID:      "test-1",
			RemoteVersion: 7,
			LocalVersion:  7,
		},
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: "Checkout Flow", Platform: "ios"},
			Build:    config.TestBuildConfig{Name: "MyApp"},
			Blocks: []config.TestBlock{
				{Type: "instructions", StepDescription: "Tap checkout"},
			},
		},
	}
	localPath := filepath.Join(tempDir, ".revyl", "tests", "checkout-flow.yaml")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := config.SaveLocalTest(localPath, localTest); err != nil {
		t.Fatalf("SaveLocalTest() error = %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"user_id":"user-1","org_id":"org-live","email":"test@example.com","concurrency_limit":1}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/tests/scripts"):
			_, _ = w.Write([]byte(`{"scripts":[],"count":0}`))
		case r.URL.Path == "/api/v1/modules/list":
			_, _ = w.Write([]byte(`{"message":"ok","result":[]}`))
		case r.URL.Path == "/api/v1/builds/vars":
			_, _ = w.Write([]byte(`{"items":[{"id":"app-1","name":"MyApp","platform":"ios","versions_count":1,"latest_version":"1.0.0"}],"total":1,"page":1,"page_size":100,"total_pages":1,"has_next":false,"has_previous":false}`))
		case r.URL.Path == "/api/v1/tests/update/test-1":
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"detail":"conflict"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	msgAny := syncTestActionCmd(
		api.NewClientWithBaseURL("token", srv.URL),
		"push",
		"Checkout Flow",
		"test-1",
		false,
	)()
	msg, ok := msgAny.(TestSyncActionMsg)
	if !ok {
		t.Fatalf("message type = %T, want TestSyncActionMsg", msgAny)
	}
	if msg.Err == nil {
		t.Fatalf("msg.Err = nil, want conflict error")
	}
	if !strings.Contains(msg.Err.Error(), "conflict detected") {
		t.Fatalf("msg.Err = %v, want conflict detected", msg.Err)
	}
	if msg.Result != "" {
		t.Fatalf("msg.Result = %q, want empty on conflict", msg.Result)
	}
}
