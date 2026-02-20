package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func findQuickActionByKey(key string) (quickAction, bool) {
	for _, action := range quickActions {
		if action.Key == key {
			return action, true
		}
	}
	return quickAction{}, false
}

func findQuickActionIndexByKey(key string) int {
	for i, action := range quickActions {
		if action.Key == key {
			return i
		}
	}
	return -1
}

func TestQuickActionsIncludesDevLoop(t *testing.T) {
	action, found := findQuickActionByKey("dev_loop")
	if !found {
		t.Fatalf("expected quick action key %q to exist", "dev_loop")
	}

	if action.Label != "Start Hot Reload Dev Loop" {
		t.Fatalf("unexpected label: got %q", action.Label)
	}
	if action.Desc != "Start revyl dev: hot reload + live cloud device" {
		t.Fatalf("unexpected description: got %q", action.Desc)
	}
	if !action.RequiresAuth {
		t.Fatalf("expected dev loop quick action to require auth")
	}
}

func TestQuickActionsDevLoopIsSecond(t *testing.T) {
	index := findQuickActionIndexByKey("dev_loop")
	if index < 0 {
		t.Fatalf("expected dev_loop quick action index to exist")
	}
	if index != 1 {
		t.Fatalf("expected dev_loop quick action index to be 1 (option 2), got %d", index)
	}
}

func TestDevLoopExecCmd_Default(t *testing.T) {
	cmd := devLoopExecCmd(false)
	if len(cmd.Args) < 2 {
		t.Fatalf("expected at least executable + command args, got %v", cmd.Args)
	}
	if cmd.Args[len(cmd.Args)-1] != "dev" {
		t.Fatalf("expected last arg to be dev, got %v", cmd.Args)
	}
	for _, arg := range cmd.Args {
		if arg == "--dev" {
			t.Fatalf("did not expect --dev flag in non-dev mode args: %v", cmd.Args)
		}
	}
}

func TestDevLoopExecCmd_DevMode(t *testing.T) {
	cmd := devLoopExecCmd(true)
	if len(cmd.Args) < 3 {
		t.Fatalf("expected executable + --dev + dev, got %v", cmd.Args)
	}
	if cmd.Args[len(cmd.Args)-2] != "--dev" || cmd.Args[len(cmd.Args)-1] != "dev" {
		t.Fatalf("expected args to end with --dev dev, got %v", cmd.Args)
	}
}

func TestExecuteQuickAction_DevLoopRequiresAuth(t *testing.T) {
	index := findQuickActionIndexByKey("dev_loop")
	if index < 0 {
		t.Fatalf("expected dev_loop quick action index to exist")
	}

	m := newHubModel("dev", false)
	m.actionCursor = index

	nextModel, cmd := m.executeQuickAction()
	if cmd == nil {
		t.Fatalf("expected auth recovery command when unauthenticated")
	}

	next, ok := nextModel.(hubModel)
	if !ok {
		t.Fatalf("expected hubModel, got %T", nextModel)
	}
	if next.currentView != viewHelp {
		t.Fatalf("expected unauthenticated action to route to help view, got %v", next.currentView)
	}
	if next.authErr == nil || !strings.Contains(next.authErr.Error(), "requires authentication") {
		t.Fatalf("expected auth error describing authentication requirement, got %v", next.authErr)
	}
}

func TestExecuteQuickAction_DevLoopAuthenticated(t *testing.T) {
	index := findQuickActionIndexByKey("dev_loop")
	if index < 0 {
		t.Fatalf("expected dev_loop quick action index to exist")
	}

	m := newHubModel("dev", false)
	m.actionCursor = index
	m.apiKey = "token"
	m.client = &api.Client{}

	nextModel, cmd := m.executeQuickAction()
	if cmd == nil {
		t.Fatalf("expected dev loop subprocess command")
	}

	next, ok := nextModel.(hubModel)
	if !ok {
		t.Fatalf("expected hubModel, got %T", nextModel)
	}
	if next.currentView != viewDashboard {
		t.Fatalf("expected to stay on dashboard while launching dev loop, got %v", next.currentView)
	}
}

func TestUpdate_DevLoopDoneMsg(t *testing.T) {
	base := newHubModel("dev", false)
	base.currentView = viewTestList

	nextModel, cmd := base.Update(DevLoopDoneMsg{})
	if cmd != nil {
		t.Fatalf("expected nil cmd when no client is configured")
	}
	next := nextModel.(hubModel)
	if next.currentView != viewDashboard {
		t.Fatalf("expected return to dashboard, got %v", next.currentView)
	}
	if next.err != nil {
		t.Fatalf("expected no error on clean dev loop exit, got %v", next.err)
	}

	errModel, errCmd := base.Update(DevLoopDoneMsg{Err: errors.New("boom")})
	if errCmd != nil {
		t.Fatalf("expected nil cmd when handling dev loop error without client")
	}
	errNext := errModel.(hubModel)
	if errNext.currentView != viewDashboard {
		t.Fatalf("expected return to dashboard on error, got %v", errNext.currentView)
	}
	if errNext.err == nil || !strings.Contains(errNext.err.Error(), "dev loop exited with error") {
		t.Fatalf("expected wrapped dev loop error message, got %v", errNext.err)
	}
}

func TestUpdate_DevLoopDoneMsgRefreshesWhenClientAvailable(t *testing.T) {
	m := newHubModel("dev", false)
	m.client = &api.Client{}
	m.apiKey = "token"
	m.currentView = viewTestList

	nextModel, cmd := m.Update(DevLoopDoneMsg{})
	if cmd == nil {
		t.Fatalf("expected refresh command batch when client is available")
	}

	next := nextModel.(hubModel)
	if !next.loading {
		t.Fatalf("expected loading=true while dashboard refresh is in progress")
	}
	if next.currentView != viewDashboard {
		t.Fatalf("expected return to dashboard, got %v", next.currentView)
	}
}
