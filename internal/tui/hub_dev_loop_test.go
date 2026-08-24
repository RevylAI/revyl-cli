package tui

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
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

	if action.Label != "Start Dev Loop" {
		t.Fatalf("unexpected label: got %q", action.Label)
	}
	if action.Desc != "Start revyl dev: hot reload + rebuild on cloud device" {
		t.Fatalf("unexpected description: got %q", action.Desc)
	}
	if !action.RequiresAuth {
		t.Fatalf("expected dev loop quick action to require auth")
	}
}

func TestQuickActionsDevLoopPosition(t *testing.T) {
	index := findQuickActionIndexByKey("dev_loop")
	if index < 0 {
		t.Fatalf("expected dev_loop quick action index to exist")
	}
	devicesIndex := findQuickActionIndexByKey("devices")
	integrationsIndex := findQuickActionIndexByKey("integrations")
	if devicesIndex < 0 || integrationsIndex < 0 {
		t.Fatalf("expected devices and integrations quick actions to exist")
	}
	if index != devicesIndex+1 || index != integrationsIndex-1 {
		t.Fatalf("expected dev_loop between devices (%d) and integrations (%d), got %d", devicesIndex, integrationsIndex, index)
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
	t.Chdir(initializeSettingsGitWorktree(t))

	index := findQuickActionIndexByKey("dev_loop")
	if index < 0 {
		t.Fatalf("expected dev_loop quick action index to exist")
	}

	m := newHubModel("dev", false)
	m.actionCursor = index
	m.apiKey = "token"
	m.client = &api.Client{}

	nextModel, cmd := m.executeQuickAction()

	next, ok := nextModel.(hubModel)
	if !ok {
		t.Fatalf("expected hubModel, got %T", nextModel)
	}

	if next.err != nil {
		if !strings.Contains(next.err.Error(), "revyl init") {
			t.Fatalf("unexpected pre-validation error: %v", next.err)
		}
		if cmd != nil {
			t.Fatalf("expected nil cmd when pre-validation fails")
		}
		return
	}

	if cmd == nil {
		t.Fatalf("expected dev loop subprocess command when config is valid")
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

func TestUpdate_DevLoopDoneMsgUsesErrDetail(t *testing.T) {
	base := newHubModel("dev", false)
	base.currentView = viewTestList

	detailModel, cmd := base.Update(DevLoopDoneMsg{
		Err:       errors.New("exit status 1"),
		ErrDetail: "Project not initialized. Run 'revyl init' first.",
	})
	if cmd != nil {
		t.Fatalf("expected nil cmd when handling dev loop error without client")
	}
	next := detailModel.(hubModel)
	if next.err == nil {
		t.Fatalf("expected error to be set")
	}
	if !strings.Contains(next.err.Error(), "Project not initialized") {
		t.Fatalf("expected ErrDetail to be used, got %v", next.err)
	}
	if strings.Contains(next.err.Error(), "exit status 1") {
		t.Fatalf("expected ErrDetail to replace generic exit error, got %v", next.err)
	}
}

func TestStderrCapture(t *testing.T) {
	var c stderrCapture
	_, _ = c.Write([]byte("line 1\nline 2\nProject not initialized\n"))

	got := c.String()
	if !strings.Contains(got, "Project not initialized") {
		t.Fatalf("expected captured stderr to contain error line, got %q", got)
	}
}

func TestStderrCaptureEmpty(t *testing.T) {
	var c stderrCapture
	if c.String() != "" {
		t.Fatalf("expected empty capture to return empty string, got %q", c.String())
	}
}

func TestDevLoopExecLastStderrLine(t *testing.T) {
	d := &devLoopExec{cmd: devLoopExecCmd(false)}
	_, _ = d.stderr.Write([]byte("some debug output\n✗ Project not initialized\n\n"))

	line := d.lastStderrLine()
	if line != "✗ Project not initialized" {
		t.Fatalf("expected last non-empty line, got %q", line)
	}
}

func TestValidateDevLoopPrereqs_NoConfig(t *testing.T) {
	orig, _ := os.Getwd()
	tmp := initializeSettingsGitWorktree(t)
	_ = os.Chdir(tmp)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	err := validateDevLoopPrereqs()
	if err == nil {
		t.Fatalf("expected error when no config exists")
	}
	if !strings.Contains(err.Error(), "revyl config pull") || !strings.Contains(err.Error(), "revyl init") {
		t.Fatalf("expected pull-or-init recovery error, got %v", err)
	}
}

func writeDevLoopConfig(t *testing.T, dir, framework string) {
	t.Helper()
	authored := canonicalSettingsConfig(300)
	if framework != "" {
		commands := []string{"build"}
		outputPath := "build/app"
		authored.Build = &config.AuthoredBuild{
			Framework: framework,
			Profiles: map[string]config.AuthoredBuildProfile{
				"development": {
					IOS: &config.AuthoredBuildRecipe{
						BuildCommands: &commands,
						OutputPath:    &outputPath,
					},
				},
			},
		}
	}
	writeSettingsConfig(t, dir, authored)
}

func TestValidateDevLoopPrereqs_ReactNativeProject(t *testing.T) {
	orig, _ := os.Getwd()
	tmp := initializeSettingsGitWorktree(t)
	_ = os.Chdir(tmp)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	writeDevLoopConfig(t, tmp, "react_native")

	err := validateDevLoopPrereqs()
	if err != nil {
		t.Fatalf("expected no error for ReactNative project with hot reload, got %v", err)
	}
}

func TestValidateDevLoopPrereqs_NoHotReload(t *testing.T) {
	orig, _ := os.Getwd()
	tmp := initializeSettingsGitWorktree(t)
	_ = os.Chdir(tmp)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	writeDevLoopConfig(t, tmp, "")

	err := validateDevLoopPrereqs()
	if err == nil {
		t.Fatalf("expected error when hot reload is not configured")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected 'not configured' error, got %v", err)
	}
}

func TestValidateDevLoopPrereqs_RebuildOnlyProject(t *testing.T) {
	orig, _ := os.Getwd()
	tmp := initializeSettingsGitWorktree(t)
	_ = os.Chdir(tmp)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	writeDevLoopConfig(t, tmp, "ios")

	err := validateDevLoopPrereqs()
	if err != nil {
		t.Fatalf("expected no error for rebuild-only project with build.platforms, got %v", err)
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
