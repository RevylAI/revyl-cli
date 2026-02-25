package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/api"
)

func keyRuneCreateApp(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func TestCreateAppDoneViewShowsUploadPrompt(t *testing.T) {
	m := newCreateAppModel(nil, 100, 24)
	m.step = appStepDone
	m.createdID = "app-1"
	m.nameInput.SetValue("fdsa")

	out := m.View()
	if !strings.Contains(out, "Upload a build now?") {
		t.Fatalf("expected upload prompt in done view, got: %s", out)
	}
	if !strings.Contains(out, "Upload build now") {
		t.Fatalf("expected 'Upload build now' option in done view, got: %s", out)
	}
	if !strings.Contains(out, "Maybe later") {
		t.Fatalf("expected 'Maybe later' option in done view, got: %s", out)
	}
}

func TestCreateAppDoneEnterDefaultsToUploadNow(t *testing.T) {
	m := newCreateAppModel(nil, 80, 24)
	m.step = appStepDone

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("expected nil cmd on done enter, got %v", cmd)
	}

	next := nextModel.(createAppModel)
	if !next.done {
		t.Fatalf("expected done=true after selecting done action")
	}
	if !next.uploadNow {
		t.Fatalf("expected uploadNow=true for default done option")
	}
}

func TestCreateAppDoneMaybeLaterSelection(t *testing.T) {
	m := newCreateAppModel(nil, 80, 24)
	m.step = appStepDone

	nextModel, cmd := m.Update(keyRuneCreateApp('j'))
	if cmd != nil {
		t.Fatalf("expected nil cmd on cursor move, got %v", cmd)
	}
	next := nextModel.(createAppModel)
	if next.doneCursor != 1 {
		t.Fatalf("expected done cursor to move to defer option, got %d", next.doneCursor)
	}

	nextModel, cmd = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("expected nil cmd on done enter, got %v", cmd)
	}
	next = nextModel.(createAppModel)
	if !next.done {
		t.Fatalf("expected done=true after selecting done action")
	}
	if next.uploadNow {
		t.Fatalf("expected uploadNow=false for defer option")
	}
}

func TestUpdateCreateApp_DoneUploadNowStartsUploadFlow(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewCreateApp

	cam := newCreateAppModel(nil, 100, 24)
	cam.step = appStepDone
	cam.createdID = "app-1"
	cam.createdName = "fdsa"
	m.createAppModel = &cam

	nextModel, cmd := m.updateCreateApp(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected init command when transitioning to upload flow")
	}

	next := nextModel.(hubModel)
	if next.currentView != viewUploadBuild {
		t.Fatalf("expected upload build view, got %v", next.currentView)
	}
	if next.createAppModel != nil {
		t.Fatalf("expected create app model to be cleared after completion")
	}
	if next.uploadBuildModel == nil {
		t.Fatalf("expected upload build model to be initialized")
	}
	if next.selectedAppID != "app-1" {
		t.Fatalf("expected selected app id to be app-1, got %q", next.selectedAppID)
	}
	if next.uploadBuildModel.appID != "app-1" {
		t.Fatalf("expected upload model app id to be app-1, got %q", next.uploadBuildModel.appID)
	}
}

func TestUpdateCreateApp_DoneMaybeLaterOpensAppDetail(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewCreateApp
	m.client = &api.Client{}

	cam := newCreateAppModel(nil, 100, 24)
	cam.step = appStepDone
	cam.createdID = "app-2"
	cam.createdName = "defer-app"
	cam.doneCursor = 1
	m.createAppModel = &cam

	nextModel, cmd := m.updateCreateApp(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected fetch command when transitioning to app detail")
	}

	next := nextModel.(hubModel)
	if next.currentView != viewAppDetail {
		t.Fatalf("expected app detail view, got %v", next.currentView)
	}
	if next.createAppModel != nil {
		t.Fatalf("expected create app model to be cleared after completion")
	}
	if next.uploadBuildModel != nil {
		t.Fatalf("expected upload build model to remain nil on defer path")
	}
	if next.selectedAppID != "app-2" {
		t.Fatalf("expected selected app id to be app-2, got %q", next.selectedAppID)
	}
	if next.selectedAppName != "defer-app" {
		t.Fatalf("expected selected app name to be defer-app, got %q", next.selectedAppName)
	}
	if !next.appsLoading {
		t.Fatalf("expected appsLoading=true while app builds are fetched")
	}
}

func TestUpdateUploadBuild_EscReturnsToAppDetailAndRefreshes(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewUploadBuild
	m.client = &api.Client{}

	um := newUploadBuildModel(nil, "app-esc", "escape-app", 100, 24)
	m.uploadBuildModel = &um

	nextModel, cmd := m.updateUploadBuild(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatalf("expected refresh command batch when exiting upload via esc")
	}

	next := nextModel.(hubModel)
	if next.currentView != viewAppDetail {
		t.Fatalf("expected app detail view, got %v", next.currentView)
	}
	if next.uploadBuildModel != nil {
		t.Fatalf("expected upload model cleared on esc")
	}
	if next.selectedAppID != "app-esc" {
		t.Fatalf("expected selected app id to persist from upload model, got %q", next.selectedAppID)
	}
	if next.selectedAppName != "escape-app" {
		t.Fatalf("expected selected app name to persist from upload model, got %q", next.selectedAppName)
	}
	if !next.appsLoading {
		t.Fatalf("expected appsLoading=true while refresh commands run")
	}
}
